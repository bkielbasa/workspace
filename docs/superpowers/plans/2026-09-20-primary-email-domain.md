# Primary Email Domain (@cloudlift.run) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure every user account has an email domain of `<username>@cloudlift.run` (or configured `PRIMARY_DOMAIN`) as their primary email, with permanent usernames, decoupled invites, and clean data migration.

**Architecture:** The identity subsystem derives and enforces the user's primary email strictly as `<username>@<primaryDomain>`. Invitations record external delivery emails while allowing the invitee to pick their permanent username on acceptance. An atomic database migration updates all existing accounts to `<username>@cloudlift.run` and decouples the invite tokens table.

**Tech Stack:** Go 1.22+, PostgreSQL 14+, HTML5 / Go html/template, OpenTelemetry.

**Spec:** [`docs/superpowers/specs/2026-09-20-primary-email-domain-design.md`](file:///Users/bartlomiej.klimczak/Projects/workspace/docs/superpowers/specs/2026-09-20-primary-email-domain-design.md)

## Global Constraints
- Target Go version: 1.22+
- Primary email invariant: every user's `email` column in PostgreSQL MUST be `<username>@<primaryDomain>`
- Usernames are permanent once set; updating a username after initial creation returns `identity.ErrUsernameImmutable`
- Default primary domain is `"cloudlift.run"`, configurable via `PRIMARY_DOMAIN` environment variable
- Invites store external delivery email and display name without pre-allocating a disabled account in `users`
- All tests must pass: `go test -count=1 ./...` with zero failures across all packages

---

### Task 1: Configuration & PostgreSQL Migration (`PRIMARY_DOMAIN` & 027 Migration)

**Files:**
- Modify: `config.go:8-48`
- Create: `migrations/027_primary_cloudlift_email.up.sql`
- Create: `migrations/027_primary_cloudlift_email.down.sql`
- Test: `config_test.go`

**Interfaces:**
- Consumes: PostgreSQL schema, `config.go`
- Produces: `cfg.primaryDomain string` in `config.go`, migration 027 up/down scripts

- [ ] **Step 1: Write failing test for `PRIMARY_DOMAIN` configuration**

Create `config_test.go`:
```go
package main

import (
	"os"
	"testing"
)

func TestLoadConfigPrimaryDomain(t *testing.T) {
	os.Unsetenv("PRIMARY_DOMAIN")
	cfg := loadConfig()
	if cfg.primaryDomain != "cloudlift.run" {
		t.Fatalf("expected default primaryDomain to be 'cloudlift.run', got %q", cfg.primaryDomain)
	}

	os.Setenv("PRIMARY_DOMAIN", "example.com")
	defer os.Unsetenv("PRIMARY_DOMAIN")
	cfg = loadConfig()
	if cfg.primaryDomain != "example.com" {
		t.Fatalf("expected primaryDomain to be 'example.com', got %q", cfg.primaryDomain)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v -run TestLoadConfigPrimaryDomain`
Expected: FAIL (`cfg.primaryDomain undefined`)

- [ ] **Step 3: Update `config.go` and create database migrations**

Update `config.go`:
```go
type config struct {
	httpAddr      string
	databaseURL   string
	mailHost      string
	davHost       string
	primaryDomain string
	cookieSecure  bool
	filesDataDir  string
	filesQuota    int64
	filesMaxFile  int64
	photosDataDir string
	smbPasswdFile string
}
```
In `loadConfig()`:
```go
primaryDomain := strings.ToLower(strings.TrimSpace(getEnv("PRIMARY_DOMAIN", "cloudlift.run")))
if primaryDomain == "" {
    primaryDomain = "cloudlift.run"
}
```
And set `primaryDomain: primaryDomain` in `cfg`.

Create `migrations/027_primary_cloudlift_email.up.sql`:
```sql
-- 1. Ensure all existing users have primary email formatted as <username>@cloudlift.run
UPDATE users
SET email = lower(username) || '@cloudlift.run'
WHERE email NOT LIKE '%@cloudlift.run';

-- 2. Decouple invite_tokens from pre-created disabled users
ALTER TABLE invite_tokens ADD COLUMN IF NOT EXISTS invited_email TEXT;
ALTER TABLE invite_tokens ADD COLUMN IF NOT EXISTS display_name TEXT;

-- Backfill existing pending invite records from users if any exist
UPDATE invite_tokens i
SET invited_email = u.email,
    display_name = u.display_name
FROM users u
WHERE i.user_id = u.id AND i.invited_email IS NULL;

-- Make user_id nullable for pending invites
ALTER TABLE invite_tokens ALTER COLUMN user_id DROP NOT NULL;
```

Create `migrations/027_primary_cloudlift_email.down.sql`:
```sql
ALTER TABLE invite_tokens DROP COLUMN IF EXISTS invited_email;
ALTER TABLE invite_tokens DROP COLUMN IF EXISTS display_name;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v -run TestLoadConfigPrimaryDomain`
Expected: PASS

- [ ] **Step 5: Commit changes**

```bash
git add config.go config_test.go migrations/027_primary_cloudlift_email.up.sql migrations/027_primary_cloudlift_email.down.sql
git commit -m "feat(config,db): add PRIMARY_DOMAIN config and 027 email migration"
```

---

### Task 2: Identity Users Service (`FormatEmail`, `Create`, `Provision`, `SetUsername` Immutability)

**Files:**
- Modify: `internal/identity/types.go`
- Modify: `internal/identity/users.go`
- Modify: `internal/identity/users_test.go`
- Modify: `internal/identity/users_username_test.go`
- Modify: `internal/identity/users_sync_test.go`
- Modify: `internal/identity/sso_test.go`
- Modify: `internal/identity/app_passwords_test.go`

**Interfaces:**
- Consumes: `identity.UserRepository`, `identity.SessionRepository`
- Produces:
  - `ErrUsernameImmutable = errors.New("username cannot be changed once set")`
  - `NewUsers(repo UserRepository, sessions SessionRepository, primaryDomain string, sync ...PasswordSync) *Users`
  - `(u *Users) PrimaryDomain() string`
  - `(u *Users) FormatEmail(username string) string`
  - `(u *Users) Create(ctx context.Context, loginOrUsername, password, displayName string) (*User, error)`
  - `(u *Users) Provision(ctx context.Context, claimedEmail, displayName string) (*User, error)`
  - `(u *Users) SetUsername(ctx context.Context, id uuid.UUID, username string) error`

- [ ] **Step 1: Write failing tests for `Users` primary email derivation and username immutability**

In `internal/identity/users_username_test.go`:
```go
func TestUsersPrimaryEmailFormatting(t *testing.T) {
	ctx := context.Background()
	users := NewUsers(newMemUserStore(), nil, "cloudlift.run")

	u1, err := users.Create(ctx, "alice", "s3cret-password", "Alice")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if u1.Username != "alice" {
		t.Errorf("expected username 'alice', got %q", u1.Username)
	}
	if u1.Email != "alice@cloudlift.run" {
		t.Errorf("expected primary email 'alice@cloudlift.run', got %q", u1.Email)
	}

	// Passing an external email also extracts username and formats with primary domain
	u2, err := users.Create(ctx, "bob@external.com", "s3cret-password", "Bob")
	if err != nil {
		t.Fatalf("Create with external email failed: %v", err)
	}
	if u2.Username != "bob" {
		t.Errorf("expected username 'bob', got %q", u2.Username)
	}
	if u2.Email != "bob@cloudlift.run" {
		t.Errorf("expected primary email 'bob@cloudlift.run', got %q", u2.Email)
	}
}

func TestUsersUsernameImmutability(t *testing.T) {
	ctx := context.Background()
	users := NewUsers(newMemUserStore(), nil, "cloudlift.run")

	u, err := users.Create(ctx, "charlie", "s3cret-password", "Charlie")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Setting identical username is allowed (no-op)
	if err := users.SetUsername(ctx, u.ID, "charlie"); err != nil {
		t.Errorf("expected no-op for same username, got error: %v", err)
	}

	// Changing username is rejected
	err = users.SetUsername(ctx, u.ID, "charlotte")
	if !errors.Is(err, ErrUsernameImmutable) {
		t.Errorf("expected ErrUsernameImmutable, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/identity/... -run "TestUsersPrimaryEmailFormatting|TestUsersUsernameImmutability" -v`
Expected: FAIL

- [ ] **Step 3: Implement `Users` changes and update constructors**

1. In `internal/identity/types.go`:
```go
var (
	ErrUserNotFound      = errors.New("user not found")
	ErrUserAlreadyExists = errors.New("user already exists")
	ErrUsernameImmutable = errors.New("username cannot be changed once set")
	ErrInvalidLogin      = errors.New("invalid email/username or password")
	...
)
```

2. In `internal/identity/users.go`:
Add `primaryDomain string` to `Users` struct:
```go
type Users struct {
	repo          UserRepository
	sessions      SessionRepository
	primaryDomain string
	sync          []PasswordSync
	tracer        trace.Tracer
}

func NewUsers(repo UserRepository, sessions SessionRepository, primaryDomain string, sync ...PasswordSync) *Users {
	domain := strings.ToLower(strings.TrimSpace(primaryDomain))
	if domain == "" {
		domain = "cloudlift.run"
	}
	return &Users{
		repo:          repo,
		sessions:      sessions,
		primaryDomain: domain,
		sync:          sync,
		tracer:        otel.Tracer("users"),
	}
}

func (u *Users) PrimaryDomain() string {
	return u.primaryDomain
}

func (u *Users) FormatEmail(username string) string {
	return fmt.Sprintf("%s@%s", NormalizeUsername(username), u.primaryDomain)
}
```

Update `Create`:
```go
func (u *Users) Create(ctx context.Context, loginOrUsername, password, displayName string) (*User, error) {
	ctx, span := u.tracer.Start(ctx, "users.create")
	defer span.End()

	loginOrUsername = strings.TrimSpace(loginOrUsername)
	var username string
	if strings.Contains(loginOrUsername, "@") {
		var err error
		username, err = u.deriveUsername(ctx, loginOrUsername)
		if err != nil {
			return nil, err
		}
	} else {
		username = NormalizeUsername(loginOrUsername)
	}

	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(password); err != nil {
		return nil, err
	}

	passwordHashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	email := u.FormatEmail(username)
	user, err := u.repo.Create(ctx, email, username, string(passwordHashBytes), displayName)
	if err != nil {
		return nil, err
	}
	u.syncSetPassword(ctx, user.Username, password)
	return user, nil
}
```

Update `Provision` (SSO):
```go
func (u *Users) Provision(ctx context.Context, claimedEmail, displayName string) (*User, error) {
	ctx, span := u.tracer.Start(ctx, "users.provision")
	defer span.End()

	username, err := u.deriveUsername(ctx, claimedEmail)
	if err != nil {
		return nil, err
	}
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}

	email := u.FormatEmail(username)
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("generate bootstrap password: %w", err)
	}
	password := hex.EncodeToString(raw[:])
	passwordHashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash bootstrap password: %w", err)
	}
	return u.repo.Create(ctx, email, username, string(passwordHashBytes), displayName)
}
```

Update `SetUsername`:
```go
func (u *Users) SetUsername(ctx context.Context, id uuid.UUID, username string) error {
	ctx, span := u.tracer.Start(ctx, "users.set_username")
	defer span.End()

	username = NormalizeUsername(username)
	if err := ValidateUsername(username); err != nil {
		return err
	}

	existing, err := u.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if existing.Username != "" && existing.Username != username {
		return ErrUsernameImmutable
	}
	if existing.Username == username {
		return nil
	}

	if err := u.repo.SetUsername(ctx, id, username); err != nil {
		return err
	}
	return nil
}
```

Update other test files in `internal/identity` to pass `"cloudlift.run"` to `NewUsers`:
- `internal/identity/users_username_test.go`
- `internal/identity/users_sync_test.go`
- `internal/identity/sso_test.go`
- `internal/identity/app_passwords_test.go`
- `internal/identity/invites_test.go`

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/identity/... -v`
Expected: PASS

- [ ] **Step 5: Commit changes**

```bash
git add internal/identity/
git commit -m "feat(identity): enforce primary email formatting and username immutability"
```

---

### Task 3: Decoupled Invites Service & PostgreSQL Repository

**Files:**
- Modify: `internal/identity/invites.go`
- Modify: `internal/postgres/invites.go`
- Modify: `internal/identity/invites_test.go`

**Interfaces:**
- Consumes: `identity.Users`, `identity.InviteRepository`
- Produces:
  - `identity.Invite` struct with `InvitedEmail`, `DisplayName`, and `UserID *uuid.UUID`
  - `(s *Invites) CreateInvite(ctx context.Context, email, displayName string) (string, *Invite, error)`
  - `(s *Invites) Accept(ctx context.Context, token, username, displayName, password string) (*User, error)`
  - `(s *Invites) Lookup(ctx context.Context, token string) (*Invite, error)`

- [ ] **Step 1: Write failing tests for decoupled invite creation and acceptance**

In `internal/identity/invites_test.go`:
```go
func TestInviteCreateAndAcceptWithUsername(t *testing.T) {
	ctx := context.Background()
	userStore := newMemUserStore()
	users := NewUsers(userStore, nil, "cloudlift.run")
	inviteStore := newMemInviteStore()
	invites := NewInvites(inviteStore, users)

	// Create invite for external email
	token, inv, err := invites.CreateInvite(ctx, "friend@example.com", "Friend")
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}
	if inv.InvitedEmail != "friend@example.com" {
		t.Errorf("expected invited email 'friend@example.com', got %q", inv.InvitedEmail)
	}
	if inv.UserID != nil {
		t.Errorf("expected UserID to be nil for pending invite, got %v", inv.UserID)
	}

	// Lookup invite by token
	lookup, err := invites.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if lookup.InvitedEmail != "friend@example.com" {
		t.Errorf("expected lookup email 'friend@example.com', got %q", lookup.InvitedEmail)
	}

	// Accept invite specifying chosen username
	user, err := invites.Accept(ctx, token, "frienduser", "Friend Smith", "s3cret-password")
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	if user.Username != "frienduser" {
		t.Errorf("expected username 'frienduser', got %q", user.Username)
	}
	if user.Email != "frienduser@cloudlift.run" {
		t.Errorf("expected primary email 'frienduser@cloudlift.run', got %q", user.Email)
	}

	// Re-accepting should fail with ErrInviteUsed
	_, err = invites.Accept(ctx, token, "anothername", "Other", "password123")
	if !errors.Is(err, ErrInviteUsed) {
		t.Errorf("expected ErrInviteUsed, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/identity/... -run TestInviteCreateAndAcceptWithUsername -v`
Expected: FAIL

- [ ] **Step 3: Update `internal/identity/invites.go` and `internal/postgres/invites.go`**

1. In `internal/identity/invites.go`:
Update `Invite` struct and `InviteRepository`:
```go
type Invite struct {
	ID           uuid.UUID
	UserID       *uuid.UUID
	InvitedEmail string
	DisplayName  string
	ExpiresAt    time.Time
	UsedAt       *time.Time
	CreatedAt    time.Time
}

type InviteRepository interface {
	Create(ctx context.Context, invitedEmail, displayName, tokenHash string, expiresAt time.Time) (*Invite, error)
	GetByHash(ctx context.Context, tokenHash string) (*Invite, error)
	MarkUsed(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	List(ctx context.Context) ([]Invite, error)
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteForEmail(ctx context.Context, email string) error
}
```

Update `CreateInvite`:
```go
func (s *Invites) CreateInvite(ctx context.Context, email, displayName string) (string, *Invite, error) {
	ctx, span := s.tracer.Start(ctx, "invites.create")
	defer span.End()

	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return "", nil, errors.New("valid email required")
	}

	_ = s.repo.DeleteForEmail(ctx, email)

	plain, hash, err := mintToken()
	if err != nil {
		return "", nil, err
	}
	invite, err := s.repo.Create(ctx, email, displayName, hash, time.Now().Add(InviteTTL))
	if err != nil {
		return "", nil, err
	}
	return plain, invite, nil
}
```

Update `Accept`:
```go
func (s *Invites) Accept(ctx context.Context, token, username, displayName, password string) (*User, error) {
	ctx, span := s.tracer.Start(ctx, "invites.accept")
	defer span.End()

	invite, err := s.Lookup(ctx, token)
	if err != nil {
		return nil, err
	}
	if invite.UsedAt != nil {
		return nil, ErrInviteUsed
	}
	if time.Now().After(invite.ExpiresAt) {
		return nil, ErrInviteExpired
	}

	username = NormalizeUsername(username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}

	if strings.TrimSpace(displayName) == "" {
		displayName = invite.DisplayName
	}

	user, err := s.users.Create(ctx, username, password, displayName)
	if err != nil {
		return nil, err
	}

	if err := s.repo.MarkUsed(ctx, invite.ID, user.ID); err != nil {
		return nil, err
	}
	return user, nil
}
```

2. In `internal/postgres/invites.go`:
Update SQL columns and scanning:
```go
const inviteColumns = `t.id, t.user_id, COALESCE(t.invited_email, u.email, ''), COALESCE(t.display_name, u.display_name, ''), t.expires_at, t.used_at, t.created_at`

func scanInvite(row scanRow) (identity.Invite, error) {
	var inv identity.Invite
	var userID *uuid.UUID
	var used sql.NullTime
	var uid uuid.UUID
	err := row.Scan(&inv.ID, &userID, &inv.InvitedEmail, &inv.DisplayName, &inv.ExpiresAt, &used, &inv.CreatedAt)
	if err != nil {
		return inv, err
	}
	if userID != nil {
		inv.UserID = userID
	}
	if used.Valid {
		t := used.Time
		inv.UsedAt = &t
	}
	return inv, nil
}

func (r *inviteRepository) Create(ctx context.Context, invitedEmail, displayName, tokenHash string, expiresAt time.Time) (*identity.Invite, error) {
	var id uuid.UUID
	if err := r.db.QueryRowContext(ctx, `
		INSERT INTO invite_tokens (invited_email, display_name, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, invitedEmail, displayName, tokenHash, expiresAt).Scan(&id); err != nil {
		return nil, err
	}
	inv, err := scanInvite(r.db.QueryRowContext(ctx, `
		SELECT `+inviteColumns+`
		FROM invite_tokens t LEFT JOIN users u ON u.id = t.user_id
		WHERE t.id = $1
	`, id))
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (r *inviteRepository) GetByHash(ctx context.Context, tokenHash string) (*identity.Invite, error) {
	inv, err := scanInvite(r.db.QueryRowContext(ctx, `
		SELECT `+inviteColumns+`
		FROM invite_tokens t LEFT JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1
	`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrInviteNotFound
	}
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (r *inviteRepository) MarkUsed(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE invite_tokens SET used_at = NOW(), user_id = $2 WHERE id = $1
	`, id, userID)
	return err
}

func (r *inviteRepository) List(ctx context.Context) ([]identity.Invite, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+inviteColumns+`
		FROM invite_tokens t LEFT JOIN users u ON u.id = t.user_id
		ORDER BY t.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (r *inviteRepository) DeleteForEmail(ctx context.Context, email string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM invite_tokens
		WHERE lower(invited_email) = lower($1)
		   OR user_id IN (SELECT id FROM users WHERE lower(email) = lower($1))
	`, email)
	return err
}
```

3. Update mock stores in tests to match new `InviteRepository` signatures.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/identity/... -v`
Expected: PASS

- [ ] **Step 5: Commit changes**

```bash
git add internal/identity/invites.go internal/identity/invites_test.go internal/postgres/invites.go
git commit -m "feat(identity,postgres): decouple invites and require username on acceptance"
```

---

### Task 4: Web UI Templates & Profile/Invite Handlers

**Files:**
- Modify: `internal/web/web_http.go`
- Modify: `internal/web/view.go`
- Modify: `internal/web/profile_http.go`
- Modify: `web/templates/invite.html`
- Modify: `web/templates/profile.html`
- Modify: `internal/web/profile_test.go`

**Interfaces:**
- Consumes: `identity.Users`, `identity.Invites`
- Produces:
  - `viewData.PrimaryDomain` in template rendering
  - `/invite/accept` GET showing username input with `@cloudlift.run` suffix
  - `/invite/accept` POST parsing username and activating account with `<username>@<primaryDomain>`
  - `/profile` displaying permanent username as read-only and omitting username mutation

- [ ] **Step 1: Write failing tests for invite acceptance and profile display**

In `internal/web/profile_test.go`:
```go
func TestInviteAcceptanceWithUsername(t *testing.T) {
	srv, mockInvites, _, _ := newTestServerWithInvites(t)
	// Mock invite with token "tok123"
	mockInvites.invites["tok123"] = &identity.Invite{
		InvitedEmail: "guest@example.com",
		DisplayName:  "Guest",
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}

	// 1. GET /invite/accept?token=tok123 renders username input
	req := httptest.NewRequest("GET", "/invite/accept?token=tok123", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /invite/accept failed: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="username"`) {
		t.Errorf("expected invite form to have username field")
	}

	// 2. POST /invite/accept with username
	form := url.Values{
		"token":            {"tok123"},
		"username":         {"guestuser"},
		"display_name":     {"Guest User"},
		"password":         {"password123"},
		"confirm_password": {"password123"},
	}
	req = httptest.NewRequest("POST", "/invite/accept", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/profile?success=invite" {
		t.Errorf("expected redirect to /profile?success=invite, got %q", loc)
	}
}

func TestProfileUsernameReadOnly(t *testing.T) {
	srv, _, mockUsers, sessSvc := newTestServerWithInvites(t)
	testUser := &identity.User{
		ID:          uuid.New(),
		Email:       "alice@cloudlift.run",
		Username:    "alice",
		DisplayName: "Alice",
		Enabled:     true,
	}
	mockUsers.users[testUser.ID] = testUser
	sess, _ := sessSvc.Create(context.Background(), testUser.ID, time.Hour)

	// GET /profile renders username as disabled/readonly
	req := httptest.NewRequest("GET", "/profile", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /profile failed: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "alice@cloudlift.run") {
		t.Errorf("expected profile to show primary email")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... -run "TestInviteAcceptanceWithUsername|TestProfileUsernameReadOnly" -v`
Expected: FAIL

- [ ] **Step 3: Update `profile_http.go`, `view.go`, `web_http.go`, and templates**

1. In `internal/web/web_http.go`:
Add `primaryDomain string` to `Server`, and method:
```go
func (s *Server) SetPrimaryDomain(domain string) {
	s.primaryDomain = domain
}
```
Update `invitesService` interface in `web_http.go`:
```go
type invitesService interface {
	CreateInvite(ctx context.Context, email, displayName string) (string, *identity.Invite, error)
	Lookup(ctx context.Context, token string) (*identity.Invite, error)
	Accept(ctx context.Context, token, username, displayName, password string) (*identity.User, error)
	List(ctx context.Context) ([]identity.Invite, error)
	Revoke(ctx context.Context, id uuid.UUID) error
}
```

2. In `internal/web/view.go`:
Add `PrimaryDomain string` to `viewData`.
In `renderView`, if `data.PrimaryDomain == ""`, populate from `s.primaryDomain`.

3. In `web/templates/invite.html`:
Add Username input with domain suffix:
```html
        <label for="invite-username">Username
          <div style="display: flex; align-items: center; gap: 4px;">
            <input id="invite-username" name="username" type="text" required minlength="2" maxlength="32" placeholder="username" autocomplete="username" style="flex: 1;">
            <span class="field-suffix" style="font-weight: 500; color: var(--text-muted, #666);">@{{if .PrimaryDomain}}{{.PrimaryDomain}}{{else}}cloudlift.run{{end}}</span>
          </div>
          <span class="field-hint">Your login name and primary email address</span>
        </label>
```

4. In `web/templates/profile.html`:
Make username read-only:
```html
          <label for="profile-username">Username
            <input id="profile-username" type="text" value="{{.User.Username}}" disabled readonly title="Username cannot be changed">
            <span class="field-hint">Permanent sign-in username and primary email identity</span>
          </label>
```

5. In `internal/web/profile_http.go`:
In `profileUpdate`: remove `s.users.SetUsername(...)`.
In `inviteAcceptPage`:
```go
	invite, err := s.invites.Lookup(r.Context(), token)
	if err != nil || invite == nil {
		renderView(w, r, s.views.inviteT, "layout", viewData{
			Title: "Invite", Section: "invite",
			Error: "This invite link is invalid. Ask for a new one.",
		})
		return
	}
	renderView(w, r, s.views.inviteT, "layout", viewData{
		Title:         "Accept invite",
		Section:       "invite",
		CSRFToken:     csrfTokenFromRequest(r),
		InviteEmail:   invite.InvitedEmail,
		InviteLink:    token,
		PrimaryDomain: s.primaryDomain,
	})
```
In `inviteAccept`:
```go
	token := strings.TrimSpace(r.FormValue("token"))
	username := strings.TrimSpace(r.FormValue("username"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	password := r.FormValue("password")
	confirm := r.FormValue("confirm_password")

	fail := func(msg string) {
		invite, _ := s.invites.Lookup(r.Context(), token)
		email := ""
		if invite != nil {
			email = invite.InvitedEmail
		}
		renderView(w, r, s.views.inviteT, "layout", viewData{
			Title:         "Accept invite",
			Section:       "invite",
			CSRFToken:     csrfTokenFromRequest(r),
			Error:         msg,
			InviteEmail:   email,
			InviteLink:    token,
			PrimaryDomain: s.primaryDomain,
		})
	}

	if username == "" {
		fail("Username is required.")
		return
	}
	if err := identity.ValidateUsername(username); err != nil {
		fail("Invalid username: " + err.Error())
		return
	}
	if password == "" || password != confirm {
		fail("Passwords do not match.")
		return
	}
	user, err := s.invites.Accept(r.Context(), token, username, displayName, password)
	if err != nil {
		obs.Log(r.Context(), slog.LevelWarn, "accept invite failed", "error", err)
		switch {
		case errors.Is(err, identity.ErrUserAlreadyExists):
			fail("Username is already taken. Please choose another.")
		case errors.Is(err, identity.ErrInviteUsed):
			fail("This invite was already used. Ask for a new one.")
		case errors.Is(err, identity.ErrInviteExpired):
			fail("This invite expired. Ask for a new one.")
		case errors.Is(err, identity.ErrInviteNotFound):
			fail("This invite link is invalid. Ask for a new one.")
		default:
			fail("Could not activate the account: " + err.Error())
		}
		return
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/web/... -v`
Expected: PASS

- [ ] **Step 5: Commit changes**

```bash
git add internal/web/ web/templates/
git commit -m "feat(web): update invite acceptance with username and make profile username permanent"
```

---

### Task 5: Main Application Wiring, HTTP API & End-to-End Suite Verification

**Files:**
- Modify: `main.go`
- Modify: `internal/httpapi/users.go`
- Modify: `internal/httpapi/domains.go`
- Modify: `internal/httpapi/users_test.go`
- Test: `tests/integration/notes_ios_sync_test.go`
- Test: `main_test.go`

**Interfaces:**
- Consumes: all components from Tasks 1-4
- Produces: complete wired application, verified API endpoints and full test suite passing

- [ ] **Step 1: Write failing test for `POST /users` in HTTP API**

In `internal/httpapi/users_test.go`:
```go
func TestCreateUserReturnsPrimaryEmail(t *testing.T) {
	// Verify creating a user with username creates primary email <username>@cloudlift.run
	users := identity.NewUsers(newMemUserStore(), nil, "cloudlift.run")
	handlers := NewUserHandlers(users)

	body := `{"username":"dave","password":"s3cret-password","display_name":"Dave"}`
	req := httptest.NewRequest("POST", "/users", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handlers.CreateHandler(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	json.NewDecoder(rec.Body).Decode(&res)
	if res["email"] != "dave@cloudlift.run" {
		t.Errorf("expected email 'dave@cloudlift.run', got %v", res["email"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run TestCreateUserReturnsPrimaryEmail -v`
Expected: FAIL

- [ ] **Step 3: Update `internal/httpapi/users.go`, `internal/httpapi/domains.go`, and `main.go`**

1. In `internal/httpapi/users.go`:
```go
type createUserRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

func (h *UserHandlers) CreateHandler(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	login := req.Username
	if login == "" {
		login = req.Email
	}
	user, err := h.users.Create(r.Context(), login, req.Password, req.DisplayName)
	if err != nil {
		if errors.Is(err, identity.ErrUserAlreadyExists) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toUserResponse(user))
}
```

2. In `internal/httpapi/domains.go`:
In `CreateUserHandler`:
```go
	localPart := strings.ToLower(strings.TrimSpace(req.LocalPart))
	if localPart == "" {
		writeJSONError(w, http.StatusBadRequest, "local_part is required")
		return
	}
	user, err := h.users.Create(r.Context(), localPart, req.Password, req.DisplayName)
```

3. In `main.go`:
Pass `cfg.primaryDomain` to `identity.NewUsers`:
```go
	users := identity.NewUsers(postgres.NewUserRepository(db), sessions, cfg.primaryDomain, pwSync...)
```
Register `cfg.primaryDomain` in `domains` table:
```go
	if exists, _ := domains.Exists(ctx, cfg.primaryDomain); !exists {
		if _, err := domains.Create(ctx, cfg.primaryDomain); err != nil {
			obs.Log(ctx, slog.LevelWarn, "failed to register primary domain", "domain", cfg.primaryDomain, "error", err)
		}
	}
```
Set `primaryDomain` on `webUI`:
```go
	webUI.SetPrimaryDomain(cfg.primaryDomain)
```

- [ ] **Step 4: Run full test suite and build**

Run: `go test -count=1 ./...`
Expected: PASS (all packages)
Run: `go build ./...`
Expected: PASS

- [ ] **Step 5: Commit changes**

```bash
git add main.go internal/httpapi/
git commit -m "feat: wire primary domain in main.go and update HTTP API user endpoints"
```
