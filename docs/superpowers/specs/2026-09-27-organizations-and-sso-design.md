# Design Specification: Organizations, Custom Domains, and Per-Organization SSO

- **Date:** 2026-09-27
- **Status:** Approved
- **Scope:** Architectural

---

## 1. Executive Summary

This specification outlines the architecture, data models, database migrations, authentication flows, and user interfaces required to add **Multi-Tenant Organizations** with **Custom Domains (BYOD)** and **Per-Organization Single Sign-On (OIDC)** to Workspace.

### Key Invariants & Requirements
1. **Regular User Account:**
   - Any user can register an individual account via `/signup`.
   - Regular users have an email address in the default primary domain `<username>@cloudlift.run` (or configured `PRIMARY_DOMAIN`).
   - Logging in: Regular users enter just their `username` (or `username@cloudlift.run`), and authenticate with their password.
2. **Organization Creation & Custom Domains:**
   - Any authenticated user can create an organization from Settings (`/settings/organization/new`).
   - Organizations bring their own custom domain(s) (e.g. `acme.corp`).
   - Custom domains require DNS TXT verification (`workspace-verify=<token>`) before becoming active.
   - Once verified, the custom domain is registered in Workspace mail routing and identity layers.
3. **Organization User Identity:**
   - Organization members possess dedicated identities with emails formatted in the organization's verified domain (e.g. `alice@acme.corp`).
   - Logging in: Organization users enter their **full email** (e.g. `alice@acme.corp`).
4. **Per-Organization SSO (OIDC):**
   - Each organization can configure an OpenID Connect (OIDC) identity provider (e.g., Google Workspace, Microsoft Entra / Azure AD, Okta, Authentik, Keycloak).
   - SSO can be configured as **Optional** (password or SSO) or **Enforced** (redirecting all users of that domain to the IdP).
   - If enabled, Just-In-Time (JIT) provisioning automatically creates organization accounts and memberships for users successfully authenticating with a verified domain email.

---

## 2. Database Schema & Migrations

### 2.1 Migration: `migrations/028_organizations_and_sso.up.sql`

```sql
-- 1. Organizations
CREATE TABLE IF NOT EXISTS organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_organizations_slug ON organizations(slug);

-- 2. Organization Domains
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

-- 3. Organization Members
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

-- 4. Organization SSO Configurations
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

-- 5. Add organization_id to users
ALTER TABLE users ADD COLUMN IF NOT EXISTS organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_users_organization_id ON users(organization_id);
```

### 2.2 Migration: `migrations/028_organizations_and_sso.down.sql`

```sql
ALTER TABLE users DROP COLUMN IF EXISTS organization_id;
DROP TABLE IF EXISTS organization_sso;
DROP TABLE IF EXISTS organization_members;
DROP TABLE IF EXISTS organization_domains;
DROP TABLE IF EXISTS organizations;
```

---

## 3. Subsystem Architectures

### 3.1 Identity Subsystem (`internal/identity`)

#### New Domain Models & Interfaces:
* **`Organization`**:
  ```go
  type Organization struct {
      ID        uuid.UUID
      Name      string
      Slug      string
      CreatedAt time.Time
      UpdatedAt time.Time
  }
  ```
* **`OrganizationDomain`**:
  ```go
  type OrganizationDomain struct {
      ID                uuid.UUID
      OrganizationID    uuid.UUID
      Domain            string
      VerificationToken string
      VerifiedAt        *time.Time
      CreatedAt         time.Time
  }
  ```
* **`OrganizationSSO`**:
  ```go
  type OrganizationSSO struct {
      ID             uuid.UUID
      OrganizationID uuid.UUID
      Name           string
      Issuer         string
      ClientID       string
      ClientSecret   string
      Scopes         []string
      EnforceSSO     bool
      AutoProvision  bool
      Enabled        bool
      CreatedAt      time.Time
      UpdatedAt      time.Time
  }
  ```
* **`OrganizationMember`**:
  ```go
  type OrganizationMember struct {
      ID             uuid.UUID
      OrganizationID uuid.UUID
      UserID         uuid.UUID
      Role           string // 'owner', 'admin', 'member'
      CreatedAt      time.Time
  }
  ```

#### Domain Verification Service:
* A `DomainVerifier` interface to decouple DNS resolution for unit testing:
  ```go
  type DNSResolver interface {
      LookupTXT(ctx context.Context, domain string) ([]string, error)
  }
  ```
* `VerifyDomain(ctx context.Context, domainID uuid.UUID) error`:
  - Fetches the domain record.
  - Queries TXT records for the domain name.
  - Checks if any TXT record contains `workspace-verify=<token>`.
  - On match: updates `verified_at = NOW()` and inserts domain into the global `domains` table.

#### Multi-Tenant Organization Service (`internal/identity/organizations.go`):
* `CreateOrganization(ctx context.Context, ownerUserID uuid.UUID, name, initialDomain string) (*Organization, *OrganizationDomain, error)`
* `GetOrganization(ctx context.Context, id uuid.UUID) (*Organization, error)`
* `GetOrganizationByDomain(ctx context.Context, domain string) (*Organization, *OrganizationDomain, error)`
* `ListMembers(ctx context.Context, orgID uuid.UUID) ([]OrganizationMemberView, error)`
* `AddMember(ctx context.Context, orgID, userID uuid.UUID, role string) error`
* `UpdateMemberRole(ctx context.Context, orgID, userID uuid.UUID, newRole string) error`
* `RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error`
* `GetSSO(ctx context.Context, orgID uuid.UUID) (*OrganizationSSO, error)`
* `SaveSSO(ctx context.Context, sso *OrganizationSSO) error`

---

## 4. User Registration & Login Flows

```
                          ┌────────────────────────┐
                          │ User Arrives at /login │
                          └───────────┬────────────┘
                                      │
                         Identifier Input Analyzed
                                      │
                   ┌──────────────────┴──────────────────┐
                   ▼                                     ▼
        Contains "@" (Email)                   No "@" (Username)
                   │                                     │
       Extract Domain (e.g. acme.corp)         Append @cloudlift.run
                   │                                     │
       Lookup Organization by Domain                     │
                   │                                     │
      ┌────────────┴────────────┐                        │
      ▼                         ▼                        ▼
Domain Not Found /        Domain Found with       Authenticate with
No Org SSO                Active Org SSO               Password
      │                         │                        │
Authenticate with         Check enforce_sso              │
Password                        │                        │
                          ┌─────┴─────┐                  │
                          ▼           ▼                  │
                      Enforced     Optional              │
                          │           │                  │
                   Redirect to     Show Password &       │
                    Org OIDC       "Sign in with         │
                    Provider       Org SSO" Button       │
```

### 4.1 Self-Serve Signup (`/signup`)
1. User navigates to `/signup`.
2. Enters `username` (e.g. `john`), `password`, and `display_name`.
3. Validation enforces username rules (alphanumeric, 2–32 characters).
4. System sets `email = fmt.Sprintf("%s@%s", username, cfg.PrimaryDomain)` (`john@cloudlift.run`) and `organization_id = NULL`.
5. Authenticates and redirects user to `/`.

### 4.2 Login Identifier Resolution (`/login`)
1. User enters identifier (`emailOrUsername`) and `password`.
2. **If no `@` is present**:
   - Resolved as personal user: `<username>@cloudlift.run`.
   - Authenticated against password hash in `users`.
3. **If `@` is present**:
   - Extracts `domain = strings.Split(emailOrUsername, "@")[1]`.
   - Looks up `organization_domains` for verified domain.
   - If organization has `organization_sso` with `enabled = true`:
     - If `enforce_sso = true`: Rejects password login with error/redirect directive: `"Your organization requires Single Sign-On."`
     - If `enforce_sso = false`: Allows password authentication, while also presenting an SSO button.

### 4.3 Dynamic Per-Organization OIDC Flow
1. **Initiate SSO (`GET /login/sso?domain=acme.corp`)**:
   - Resolves organization by domain `acme.corp`.
   - Fetches `organization_sso`.
   - Discovers OIDC endpoints from `sso.Issuer` (using cached `oidc.Provider`).
   - Generates state and PKCE code challenge.
   - Sets secure temporary cookie `sso_state` containing `state.verifier.orgID`.
   - Redirects user to IdP Authorization URL.
2. **SSO Callback (`GET /login/sso/callback`)**:
   - Reads `sso_state` cookie, parses `orgID` and `verifier`.
   - Exchanges code for tokens with the organization's client credentials.
   - Verifies ID token claims (`email`, `email_verified`, `name`).
   - **Security Assertion**: Asserts `claim.email` matches a verified domain of `orgID`.
   - **JIT Provisioning**:
     - If user exists: updates profile if needed and logs in.
     - If user does not exist and `sso.AutoProvision == true`:
       - Creates user in `users` with `organization_id = orgID`.
       - Creates member in `organization_members` with `role = 'member'`.
       - Links SSO identity (`SSOLink`).
   - Creates session, clears `sso_state`, redirects to `/`.

---

## 5. Web UI & Settings

### 5.1 Public Pages
* **`/signup`**: New user registration form for `@cloudlift.run` accounts.
* **`/login`**: Updated sign-in form supporting smart identifier detection and dynamic SSO button rendering.

### 5.2 Settings Hub: Organization Section (`/settings/organization`)
* Accessible to users with `role IN ('owner', 'admin')`.
* **Create Organization (`/settings/organization/new`)**:
  - Modal or form to enter Organization Name and initial Custom Domain.
* **Domains Tab**:
  - Displays all registered domains and their verification status badges.
  - "Add Domain" action.
  - "Verify DNS" button with instruction banner:
    ```
    Record Type: TXT
    Host / Name: @ (or acme.corp)
    Value: workspace-verify=<verification_token>
    ```
* **Single Sign-On (SSO) Tab**:
  - Provider Name, Issuer URL, Client ID, Client Secret, Scopes.
  - Checkbox: "Require SSO for all users in this organization" (`enforce_sso`).
  - Checkbox: "Enable Just-In-Time account provisioning" (`auto_provision`).
  - Active toggle.
  - Display Callback URI: `https://<workspace-host>/login/sso/callback`.
* **Members Tab**:
  - Member table: Display Name, Email, Role, Joined Date.
  - Invite member action: sends invitation or provisions account in verified domain.
  - Role management (promote to Admin, demote to Member, Remove Member).

---

## 6. Security & Operational Considerations

1. **Domain Hijacking Prevention**:
   - No organization can claim or route mail for a domain without proving control via the unique DNS TXT verification token.
   - Domains are constrained to be globally unique in `organization_domains`.
2. **Break-Glass Owner Access**:
   - If an organization enforces SSO and the external IdP suffers an outage or credential invalidation, the organization `owner` can use a designated break-glass login parameter (`/login?direct=true` or password recovery flow) using their master credentials.
3. **PKCE & State Protection**:
   - Dynamic SSO uses OAuth 2.0 PKCE (RFC 7636) and state nonces stored in `SameSite=Lax`, `HttpOnly`, `Secure` cookies.
4. **Tenant Data Isolation**:
   - All organization management endpoints explicitly check the authenticated user's membership and role in `organization_members` before allowing queries or mutations.

---

## 7. Testing Strategy

1. **Unit Tests**:
   - `internal/identity/organizations_test.go`:
     - Test Organization creation, domain association, and membership role checks.
     - Test duplicate slug or domain rejection.
   - `internal/identity/domains_test.go`:
     - Test DNS TXT lookup with mock DNS resolver: successful verification on matching token, failure on missing/mismatched token.
     - Test global domain synchronization upon verification.
   - `internal/identity/users_test.go`:
     - Test login resolution: username `john` resolves to `john@cloudlift.run`; full email `alice@acme.corp` resolves to org user.
   - `internal/identity/sso_test.go`:
     - Test multi-tenant OIDC provider retrieval by domain.
     - Test JIT provisioning of new org user and rejection of mismatched email domains.
2. **Web / Handler Tests**:
   - `internal/web/auth_test.go`:
     - Test `/signup` creating personal accounts.
     - Test `/login` with username vs. email.
     - Test `/login` returning SSO redirection when `enforce_sso` is enabled.
   - `internal/web/org_settings_test.go`:
     - Test `/settings/organization` access control (members vs admins).
     - Test adding a domain and triggering TXT verification.
     - Test updating SSO configuration.
   - `internal/web/sso_test.go`:
     - Test dynamic SSO authorization and callback against a mock OIDC server.
3. **End-to-End Verification**:
   - Run `go test -count=1 ./...` and `go build ./...` across the entire project.
