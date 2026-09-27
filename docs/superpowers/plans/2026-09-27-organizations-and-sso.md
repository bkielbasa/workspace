# Organizations, Custom Domains, and Per-Organization SSO Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add multi-tenant organizations with Bring-Your-Own-Domain (BYOD) via DNS TXT verification, smart login identifier resolution (username for regular users, full email for org users), self-serve registration, and per-organization OIDC Single Sign-On with Just-In-Time (JIT) provisioning.

**Architecture:** Extend PostgreSQL schema with `organizations`, `organization_domains`, `organization_members`, and `organization_sso` tables, and link users via `users.organization_id`. Build an `identity.Organizations` service managing tenant life-cycle, a pluggable `DomainVerifier` for DNS TXT validation, and update the login pipeline to inspect identifier domains dynamically to route between password verification and organization-specific OIDC SSO providers.

**Tech Stack:** Go 1.22+, PostgreSQL (`jackc/pgx/v5`), OpenID Connect (`coreos/go-oidc/v3/oidc`, `golang.org/x/oauth2`), Go `html/template`, `net.LookupTXT`.

**Spec:** [`docs/superpowers/specs/2026-09-27-organizations-and-sso-design.md`](file:///Users/bartlomiej.klimczak/Projects/workspace/docs/superpowers/specs/2026-09-27-organizations-and-sso-design.md)

## Global Constraints

- Go standard library formatting (`gofmt`) and idiomatic error handling.
- PostgreSQL migrations must include both `.up.sql` and `.down.sql`.
- Follow established project conventions for UUIDs (`github.com/google/uuid`) and tracing (`go.opentelemetry.io/otel`).
- Strict validation: Usernames 2–32 characters, valid domains, verified emails from OIDC assertions.
- Multi-tenant isolation: All org operations must verify authenticated user's membership and role in `organization_members`.

---

### Task 1: Database Migration for Organizations, Domains, Members, and SSO

**Files:**
- Create: `migrations/028_organizations_and_sso.up.sql`
- Create: `migrations/028_organizations_and_sso.down.sql`
- Test: `internal/postgres/migrations_test.go` (or run migration verification test)

**Interfaces:**
- Consumes: PostgreSQL schema v027
- Produces: Tables `organizations`, `organization_domains`, `organization_members`, `organization_sso`, and column `users.organization_id`

- [ ] **Step 1: Write migration files**

`migrations/028_organizations_and_sso.up.sql`:
```sql
CREATE TABLE IF NOT EXISTS organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_organizations_slug ON organizations(slug);

CREATE TABLE IF NOT EXISTS organization_domains (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    domain TEXT NOT NULL UNIQUE,
    verification_token TEXT NOT NULL,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_organization_domains_org_id ON organization_domains(organization_id);
CREATE INDEX IF NOT EXISTS idx_organization_domains_domain ON organization_domains(lower(domain));

CREATE TABLE IF NOT EXISTS organization_members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_organization_user UNIQUE (organization_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_organization_members_user_id ON organization_members(user_id);
CREATE INDEX IF NOT EXISTS idx_organization_members_org_id ON organization_members(organization_id);

CREATE TABLE IF NOT EXISTS organization_sso (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL UNIQUE REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL DEFAULT 'Single Sign-On',
    issuer TEXT NOT NULL,
    client_id TEXT NOT NULL,
    client_secret TEXT NOT NULL,
    scopes TEXT[] NOT NULL DEFAULT ARRAY['openid', 'profile', 'email'],
    enforce_sso BOOLEAN NOT NULL DEFAULT FALSE,
    auto_provision BOOLEAN NOT NULL DEFAULT TRUE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE users ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_users_organization_id ON users(organization_id);
```

`migrations/028_organizations_and_sso.down.sql`:
```sql
ALTER TABLE users DROP COLUMN IF EXISTS organization_id;
DROP TABLE IF EXISTS organization_sso;
DROP TABLE IF EXISTS organization_members;
DROP TABLE IF EXISTS organization_domains;
DROP TABLE IF EXISTS organizations;
```

- [ ] **Step 2: Verify SQL syntax with dry-run parser or tests**

Run: `go test -v ./internal/postgres/...`
Expected: PASS

- [ ] **Step 3: Commit migration files**

```bash
git add migrations/028_organizations_and_sso.up.sql migrations/028_organizations_and_sso.down.sql
git commit -m "feat(db): add migration for organizations, domains, members, and sso"
```

---

### Task 2: Identity Models, Repositories & Organization Service

**Files:**
- Modify: `internal/identity/types.go`
- Create: `internal/identity/organizations.go`
- Create: `internal/identity/organizations_test.go`
- Create: `internal/postgres/organizations.go`

**Interfaces:**
- Consumes: `users.User`, `uuid.UUID`
- Produces: `identity.Organization`, `identity.OrganizationDomain`, `identity.OrganizationSSO`, `identity.OrganizationMember`, `identity.Organizations` service

- [ ] **Step 1: Define types in `internal/identity/types.go`**

```go
type Organization struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OrganizationDomain struct {
	ID                uuid.UUID  `json:"id"`
	OrganizationID    uuid.UUID  `json:"organization_id"`
	Domain            string     `json:"domain"`
	VerificationToken string     `json:"verification_token"`
	VerifiedAt        *time.Time `json:"verified_at"`
	CreatedAt         time.Time  `json:"created_at"`
}

type OrganizationMember struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	UserID         uuid.UUID `json:"user_id"`
	Role           string    `json:"role"`
	CreatedAt      time.Time `json:"created_at"`
}

type OrganizationSSO struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Name           string    `json:"name"`
	Issuer         string    `json:"issuer"`
	ClientID       string    `json:"client_id"`
	ClientSecret   string    `json:"client_secret"`
	Scopes         []string  `json:"scopes"`
	EnforceSSO     bool      `json:"enforce_sso"`
	AutoProvision  bool      `json:"auto_provision"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
```

- [ ] **Step 2: Write failing unit tests in `internal/identity/organizations_test.go`**

```go
package identity_test

import (
	"context"
	"testing"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

func TestOrganizationCreationAndDomainBinding(t *testing.T) {
	repo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(repo)

	ctx := context.Background()
	ownerID := uuid.New()

	org, domain, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("unexpected error creating org: %v", err)
	}
	if org.Name != "Acme Corp" {
		t.Errorf("expected name Acme Corp, got %s", org.Name)
	}
	if domain.Domain != "acme.corp" {
		t.Errorf("expected domain acme.corp, got %s", domain.Domain)
	}
	if domain.VerificationToken == "" {
		t.Errorf("expected non-empty verification token")
	}
}
```

- [ ] **Step 3: Run unit tests to verify failure**

Run: `go test -v ./internal/identity -run TestOrganizationCreationAndDomainBinding`
Expected: FAIL (compilation errors for undefined types/methods)

- [ ] **Step 4: Implement `internal/identity/organizations.go` and repository implementation**

Implement `Organizations` service methods:
- `Create(ctx, ownerID, name, domain)`
- `Get(ctx, id)`
- `GetByDomain(ctx, domain)`
- `ListMembers(ctx, orgID)`
- `AddMember(ctx, orgID, userID, role)`
- `GetSSO(ctx, orgID)`
- `SaveSSO(ctx, sso)`

- [ ] **Step 5: Run tests to verify passing**

Run: `go test -v ./internal/identity -run TestOrganization`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/identity/ internal/postgres/organizations.go
git commit -m "feat(identity): add organization service, models, and repository"
```

---

### Task 3: DNS TXT Verification Service & Domain Synchronization

**Files:**
- Create: `internal/identity/domain_verifier.go`
- Create: `internal/identity/domain_verifier_test.go`

**Interfaces:**
- Consumes: `identity.OrganizationDomain`, `identity.DomainRepository`
- Produces: `identity.DomainVerifier` verifying TXT records and syncing with global `domains`

- [ ] **Step 1: Write failing unit test in `internal/identity/domain_verifier_test.go`**

```go
package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type mockDNSResolver struct {
	records map[string][]string
}

func (m *mockDNSResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	return m.records[domain], nil
}

func TestDomainVerificationSuccess(t *testing.T) {
	ctx := context.Background()
	resolver := &mockDNSResolver{
		records: map[string][]string{
			"acme.corp": {"v=spf1 ~all", "workspace-verify=valid-secret-token-123"},
		},
	}
	repo := identity.NewMemoryOrganizationRepository()
	globalDomains := identity.NewMemoryDomainRepository()

	orgID := uuid.New()
	dom, err := repo.CreateDomain(ctx, orgID, "acme.corp", "valid-secret-token-123")
	if err != nil {
		t.Fatalf("failed to create test domain: %v", err)
	}

	verifier := identity.NewDomainVerifier(repo, globalDomains, resolver)
	if err := verifier.Verify(ctx, dom.ID); err != nil {
		t.Fatalf("expected verification to succeed, got: %v", err)
	}

	verified, err := repo.GetDomain(ctx, dom.ID)
	if err != nil || verified.VerifiedAt == nil {
		t.Fatalf("expected domain to be marked verified")
	}

	exists, err := globalDomains.Exists(ctx, "acme.corp")
	if err != nil || !exists {
		t.Fatalf("expected domain to be registered in global domains")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/identity -run TestDomainVerificationSuccess`
Expected: FAIL

- [ ] **Step 3: Implement `internal/identity/domain_verifier.go`**

```go
package identity

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
)

type DNSResolver interface {
	LookupTXT(ctx context.Context, domain string) ([]string, error)
}

type NetDNSResolver struct{}

func (n *NetDNSResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	var r net.Resolver
	return r.LookupTXT(ctx, domain)
}

type DomainVerifier struct {
	orgRepo       OrganizationRepository
	globalDomains DomainRepository
	resolver      DNSResolver
}

func NewDomainVerifier(orgRepo OrganizationRepository, globalDomains DomainRepository, resolver DNSResolver) *DomainVerifier {
	if resolver == nil {
		resolver = &NetDNSResolver{}
	}
	return &DomainVerifier{
		orgRepo:       orgRepo,
		globalDomains: globalDomains,
		resolver:      resolver,
	}
}

func (v *DomainVerifier) Verify(ctx context.Context, domainID uuid.UUID) error {
	dom, err := v.orgRepo.GetDomain(ctx, domainID)
	if err != nil {
		return err
	}
	records, err := v.resolver.LookupTXT(ctx, dom.Domain)
	if err != nil {
		return fmt.Errorf("lookup DNS TXT: %w", err)
	}
	expectedToken := "workspace-verify=" + dom.VerificationToken
	found := false
	for _, r := range records {
		if strings.Contains(r, expectedToken) {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("DNS TXT verification record not found for %s", dom.Domain)
	}
	now := time.Now()
	dom.VerifiedAt = &now
	if err := v.orgRepo.UpdateDomain(ctx, dom); err != nil {
		return err
	}
	if exists, _ := v.globalDomains.Exists(ctx, dom.Domain); !exists {
		_, _ = v.globalDomains.Create(ctx, dom.Domain)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/identity -run TestDomainVerificationSuccess`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/identity/domain_verifier.go internal/identity/domain_verifier_test.go
git commit -m "feat(identity): add domain TXT verification service"
```

---

### Task 4: Smart Login Resolution (Username vs Org Full Email)

**Files:**
- Modify: `internal/identity/users.go`
- Modify: `internal/identity/users_test.go`
- Modify: `internal/web/auth_http.go`
- Modify: `internal/web/auth_test.go`

**Interfaces:**
- Consumes: `users.GetByLogin`, `organizations.GetByDomain`, `organization_sso`
- Produces: Enhanced `Authenticate` with domain-based SSO policy enforcement

- [ ] **Step 1: Write failing test in `internal/identity/users_test.go`**

```go
func TestAuthenticateUsernameVsOrgEmail(t *testing.T) {
	ctx := context.Background()
	// user alice@cloudlift.run created with username "alice"
	// user bob@acme.corp created with username "bob" in org with acme.corp
	// Verify "alice" resolves to alice@cloudlift.run
	// Verify "bob" does NOT authenticate bob@acme.corp
	// Verify "bob@acme.corp" authenticates successfully
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/identity -run TestAuthenticateUsernameVsOrgEmail`
Expected: FAIL

- [ ] **Step 3: Update `Users.GetByLogin` and `auth_http.go:login`**

In `internal/identity/users.go`:
```go
func (u *Users) GetByLogin(ctx context.Context, login string) (*User, error) {
	login = strings.TrimSpace(login)
	if strings.Contains(login, "@") {
		return u.repo.GetByEmail(ctx, strings.ToLower(login))
	}
	// Plain username maps strictly to primaryDomain (cloudlift.run)
	email := u.FormatEmail(login)
	return u.repo.GetByEmail(ctx, email)
}
```

In `internal/web/auth_http.go`:
Inspect domain on login request. If email contains `@` and organization has `enforce_sso = true`, return:
```json
{
  "error": "sso_required",
  "redirect_url": "/login/sso?domain=acme.corp"
}
```

- [ ] **Step 4: Run tests to verify passing**

Run: `go test -v ./internal/identity ./internal/web -run TestAuthenticate`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/identity/users.go internal/identity/users_test.go internal/web/auth_http.go
git commit -m "feat(auth): add smart identifier resolution and enforced SSO detection"
```

---

### Task 5: Self-Serve User Registration (`/signup`)

**Files:**
- Create: `web/templates/signup.html`
- Modify: `internal/web/auth_http.go`
- Modify: `internal/web/web_http.go`
- Modify: `internal/web/view.go`
- Create: `internal/web/signup_test.go`

**Interfaces:**
- Consumes: `users.Create`, `sessions.Create`
- Produces: `GET /signup` and `POST /signup` endpoints

- [ ] **Step 1: Write failing test in `internal/web/signup_test.go`**

```go
package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignupPageRenders(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/signup")
	if err != nil {
		t.Fatalf("GET /signup failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}

func TestSignupCreatesCloudliftUser(t *testing.T) {
	srv := newTestServer(t)
	body := `{"username":"newuser","password":"securepassword123","display_name":"New User"}`
	resp, err := http.Post(srv.URL+"/signup", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /signup failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/web -run TestSignup`
Expected: FAIL (404 Not Found)

- [ ] **Step 3: Implement `signup.html` template and handler in `auth_http.go`**

Add `signupPage` and `signup` in `auth_http.go`, parse `signup.html` in `view.go`, and register routes in `web_http.go`:
```go
mux.HandleFunc("GET /signup", s.signupPage)
mux.HandleFunc("POST /signup", s.signup)
```

- [ ] **Step 4: Run test to verify passing**

Run: `go test -v ./internal/web -run TestSignup`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/templates/signup.html internal/web/auth_http.go internal/web/web_http.go internal/web/view.go internal/web/signup_test.go
git commit -m "feat(web): add self-serve /signup page and handler"
```

---

### Task 6: Dynamic Per-Organization OIDC SSO & JIT Provisioning

**Files:**
- Create: `internal/web/org_sso.go`
- Create: `internal/web/org_sso_test.go`
- Modify: `internal/web/sso_http.go`
- Modify: `internal/web/web_http.go`

**Interfaces:**
- Consumes: `organizations.GetSSO`, `organizations.GetByDomain`, `oidc.Provider`, `users.Provision`
- Produces: Dynamic OIDC resolution by domain, JIT provisioning for org users

- [ ] **Step 1: Write failing unit/integration test in `internal/web/org_sso_test.go`**

```go
func TestDynamicOrgSSOBeginsWithOrgDomain(t *testing.T) {
	// Setup test server with mock OIDC IdP for "acme.corp"
	// Request GET /login/sso?domain=acme.corp
	// Verify 302 Redirect to IdP authorization endpoint
}

func TestOrgSSOCallbackJITProvisionsMember(t *testing.T) {
	// Mock OIDC callback returning email alice@acme.corp
	// Verify user created with organization_id set to Acme Corp
	// Verify membership added in organization_members with role member
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/web -run TestDynamicOrgSSO`
Expected: FAIL

- [ ] **Step 3: Implement dynamic OIDC discovery & JIT callback in `org_sso.go`**

Implement:
- `ssoBeginForOrg(w, r, org, sso)`
- `ssoCallbackForOrg(w, r, org, sso)` with domain validation:
  Assert `emailDomain == orgDomain`.
  If user doesn't exist:
  `user = users.CreateWithOrg(ctx, email, orgID)`
  `orgs.AddMember(ctx, orgID, user.ID, "member")`

- [ ] **Step 4: Run test to verify passing**

Run: `go test -v ./internal/web -run TestOrgSSO`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/web/org_sso.go internal/web/org_sso_test.go internal/web/sso_http.go
git commit -m "feat(web): implement per-organization OIDC SSO and JIT provisioning"
```

---

### Task 7: Organization Management Web UI & Settings Endpoints

**Files:**
- Create: `web/templates/organization.html`
- Create: `web/templates/organization_new.html`
- Create: `internal/web/organization_http.go`
- Create: `internal/web/organization_http_test.go`
- Modify: `internal/web/web_http.go`
- Modify: `internal/web/settings.go`

**Interfaces:**
- Consumes: `identity.Organizations`, `identity.DomainVerifier`
- Produces: `/settings/organization` views, domain verification trigger, SSO settings update

- [ ] **Step 1: Write failing tests in `internal/web/organization_http_test.go`**

```go
func TestCreateOrganizationFromSettings(t *testing.T) {
	// POST /settings/organization with name "Beta Corp" and domain "beta.io"
	// Verify returns 200/303 and domain verification token generated
}

func TestTriggerDomainVerification(t *testing.T) {
	// POST /settings/organization/domains/{id}/verify
	// Verify calls DomainVerifier and returns status
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/web -run TestCreateOrganization`
Expected: FAIL

- [ ] **Step 3: Implement organization UI templates and handlers**

- `organization_new.html`: Form to create organization and initial custom domain.
- `organization.html`: Tabbed or sectioned interface for Domains, SSO, and Members.
- `organization_http.go`:
  - `POST /settings/organization`
  - `GET /settings/organization`
  - `POST /settings/organization/domains`
  - `POST /settings/organization/domains/{id}/verify`
  - `POST /settings/organization/sso`
  - `POST /settings/organization/members/invite`

- [ ] **Step 4: Run tests to verify passing**

Run: `go test -v ./internal/web -run TestCreateOrganization`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/templates/organization*.html internal/web/organization_http.go internal/web/organization_http_test.go internal/web/settings.go internal/web/web_http.go
git commit -m "feat(web): add organization settings hub for domains, sso, and members"
```

---

### Task 8: Wire-up in `main.go` and Full System Verification

**Files:**
- Modify: `main.go`
- Test: Full integration test suite

- [ ] **Step 1: Wire `Organizations` and `DomainVerifier` into `main.go`**

Instantiate `identity.NewOrganizations(orgRepo)` and `identity.NewDomainVerifier(orgRepo, domainRepo, nil)` and register them on `web.Server`.

- [ ] **Step 2: Run entire test suite**

Run: `go test -count=1 ./...`
Expected: PASS

- [ ] **Step 3: Verify binary compilation**

Run: `go build -o /dev/null ./...`
Expected: exit 0

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: wire organization services into main application"
```
