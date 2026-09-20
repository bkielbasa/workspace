# Design Specification: Primary Email Domain (@cloudlift.run) for All Users

- **Date:** 2026-09-20
- **Status:** Approved
- **Scope:** Architectural

---

## 1. Executive Summary

This specification defines the architecture, data models, schema migrations, and user flows to ensure that **every user account in Workspace has a primary email address formatted as `<username>@cloudlift.run>`** (or the configured `PRIMARY_DOMAIN`).

Key invariants established:
1. **Primary Email Invariant:** A user's primary email address is strictly `<username>@<primaryDomain>`.
2. **Username Immutability:** Usernames are permanent once created to guarantee persistent email identities.
3. **Decoupled Invitations:** Admin invitations are addressed to an external delivery email (e.g. `colleague@gmail.com`). When redeeming the invite, the invitee selects their desired `username`, which immediately provisions their workspace account with `<username>@<primaryDomain>`.
4. **Clean Migration:** Existing accounts are updated to `<username>@cloudlift.run` without alias preservation.

---

## 2. Configuration & Domain Provisioning

### 2.1 Configuration
In [`config.go`](file:///Users/bartlomiej.klimczak/Projects/workspace/config.go):
- Add `primaryDomain string` to the `config` struct.
- Read from environment variable `PRIMARY_DOMAIN` with a default of `"cloudlift.run"`:
  ```go
  primaryDomain: getEnv("PRIMARY_DOMAIN", "cloudlift.run"),
  ```
- Store `primaryDomain` in lowercase and trimmed.

### 2.2 Server Bootstrap & Domain Registry
In [`main.go`](file:///Users/bartlomiej.klimczak/Projects/workspace/main.go):
- At startup, check if `cfg.primaryDomain` exists in the `domains` table via `domains.Exists(ctx, cfg.primaryDomain)`.
- If not present, create it via `domains.Create(ctx, cfg.primaryDomain)`.
- Pass `cfg.primaryDomain` into `identity.NewUsers(repo, sessions, cfg.primaryDomain, pwSync...)` and `identity.NewInvites(...)`.

---

## 3. Database Schema & Migration

### 3.1 Migration `migrations/027_primary_cloudlift_email.up.sql`
1. **Existing User Email Migration**:
   Update all existing users to ensure their primary email is `<username>@cloudlift.run`:
   ```sql
   UPDATE users
   SET email = lower(username) || '@cloudlift.run'
   WHERE email NOT LIKE '%@cloudlift.run';
   ```
2. **Invites Schema Decoupling**:
   Decouple the `invites` table from pre-created disabled users. Invites will record the recipient email and display name directly:
   ```sql
   ALTER TABLE invites ADD COLUMN IF NOT EXISTS invited_email TEXT;
   ALTER TABLE invites ADD COLUMN IF NOT EXISTS display_name TEXT;

   -- Backfill existing pending invites from users table if any exist
   UPDATE invites i
   SET invited_email = u.email,
       display_name = u.display_name
   FROM users u
   WHERE i.user_id = u.id AND i.invited_email IS NULL;

   -- Allow user_id to be NULL while invite is pending
   ALTER TABLE invites ALTER COLUMN user_id DROP NOT NULL;
   ```

### 3.2 Migration `migrations/027_primary_cloudlift_email.down.sql`
```sql
ALTER TABLE invites DROP COLUMN IF EXISTS invited_email;
ALTER TABLE invites DROP COLUMN IF EXISTS display_name;
```

---

## 4. Identity Subsystem (`internal/identity`)

### 4.1 Users Service (`internal/identity/users.go`)
- **Field & Constructor**:
  Add `primaryDomain string` to `identity.Users`.
  ```go
  func NewUsers(repo UserRepository, sessions SessionRepository, primaryDomain string, sync ...PasswordSync) *Users
  ```
  If `primaryDomain` is empty, fallback to `"cloudlift.run"`.

- **Helper: `FormatEmail(username string) string`**:
  ```go
  func (u *Users) FormatEmail(username string) string {
      return fmt.Sprintf("%s@%s", NormalizeUsername(username), u.primaryDomain)
  }
  ```

- **`Users.Create`**:
  Takes `(ctx context.Context, loginOrUsername, password, displayName string) (*User, error)`:
  - Extracts username: if `@` is in `loginOrUsername`, splits and takes local part.
  - Normalizes and validates username with `ValidateUsername(username)`.
  - Computes `email := u.FormatEmail(username)`.
  - Hashes password.
  - Calls `u.repo.Create(ctx, email, username, passwordHash, displayName)`.
  - Syncs password to `PasswordSync` (Samba).

- **`Users.Provision` (SSO Just-In-Time)**:
  Takes `(ctx context.Context, claimedEmail, displayName string) (*User, error)`:
  - Derives a clean username from `claimedEmail` using `u.deriveUsername(ctx, claimedEmail)`.
  - Sets `email := u.FormatEmail(username)`.
  - Generates random bootstrap password and creates enabled account in repo.

- **Username Immutability (`Users.SetUsername`)**:
  - If the user already has a non-empty `username`, `SetUsername` returns `ErrUsernameImmutable = errors.New("username cannot be changed once set")`.
  - Usernames cannot be edited via profile settings.

- **`Users.Authenticate`**:
  - Resolves login via `GetByLogin(ctx, login)`.
  - Accepts either `<username>` or `<username>@<primaryDomain>` seamlessly.

### 4.2 Invites Service (`internal/identity/invites.go`)
- **Domain Model**:
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
  ```

- **Invite Creation (`Invites.CreateInvite`)**:
  `CreateInvite(ctx context.Context, invitedEmail, displayName string) (string, *Invite, error)`:
  - Validates `invitedEmail` has an `@` and is non-empty.
  - Does **not** pre-create a disabled user in `users`.
  - Mints token, computes SHA-256 hash.
  - Saves invite with `invited_email = invitedEmail`, `display_name = displayName`, `user_id = NULL`.
  - Returns plaintext token and `*Invite`.

- **Invite Acceptance (`Invites.Accept`)**:
  `Accept(ctx context.Context, token, username, displayName, password string) (*User, error)`:
  - Looks up invite by token hash.
  - Validates unexpired and `UsedAt == nil`.
  - Validates `username` using `ValidateUsername(username)`.
  - Creates the enabled user via `s.users.Create(ctx, username, password, displayName)`.
    - This automatically establishes primary email `<username>@<primaryDomain>`.
    - If username already exists, returns `ErrUserAlreadyExists`.
  - Marks invite used: sets `used_at = NOW()` and `user_id = user.ID`.
  - Returns the newly created `*User`.

---

## 5. Web UI & Templates

### 5.1 Invite Acceptance Page (`/invite/accept`)
- **Template (`web/templates/invite.html`)**:
  - Add required form field for **Username**:
    ```html
    <div class="form-group">
      <label for="username">Username</label>
      <div class="input-group">
        <input type="text" id="username" name="username" required
               pattern="[a-zA-Z0-9._+-]{2,32}"
               placeholder="username" autocomplete="username" />
        <span class="input-suffix">@cloudlift.run</span>
      </div>
      <span class="form-hint">Your login name and primary email address.</span>
    </div>
    ```
- **Handler (`internal/web/profile_http.go:inviteAccept`)**:
  - Parse `username := strings.TrimSpace(r.FormValue("username"))`.
  - If `username == ""`, re-render with error `"Username is required."`.
  - Call `s.invites.Accept(r.Context(), token, username, displayName, password)`.
  - On `ErrUserAlreadyExists`, render `"Username is already taken. Please choose another."`.
  - On success, start session and redirect to `/profile?success=invite`.

### 5.2 Profile Page (`/profile`)
- **Template (`web/templates/profile.html`)**:
  - Replace the editable username input with a read-only identity display:
    ```html
    <div class="profile-identity-card">
      <div class="profile-identity-item">
        <span class="profile-label">Primary Email</span>
        <span class="profile-value"><strong>{{.User.Email}}</strong></span>
      </div>
      <div class="profile-identity-item">
        <span class="profile-label">Username</span>
        <span class="profile-value">{{.User.Username}}</span>
      </div>
    </div>
    ```
  - Form retains editable `Display Name` and Password Change section.
- **Handler (`internal/web/profile_http.go:profileUpdate`)**:
  - Remove username mutation logic; only update `displayName`.

---

## 6. HTTP API Endpoints (`internal/httpapi`)

### 6.1 `POST /users` ([`internal/httpapi/users.go`](file:///Users/bartlomiej.klimczak/Projects/workspace/internal/httpapi/users.go))
- Request body accepts `username` (or `email`), `password`, and `display_name`.
- Passes username to `s.users.Create(r.Context(), req.Username, req.Password, req.DisplayName)`.
- Returns created user response with `email: "<username>@<primaryDomain>"`.

---

## 7. Migration & Rollout Strategy

1. **Database Migration**: Run `027_primary_cloudlift_email.up.sql` to backfill existing users and alter `invites`.
2. **Domain Registration**: Server bootstrap verifies `PRIMARY_DOMAIN` is in `domains`.
3. **Backward Compatibility**:
   - `Authenticate` supports logging in with either `<username>` or `<username>@cloudlift.run`.
   - Mail delivery routes local recipient `<username>@cloudlift.run` straight to the user's `INBOX`.
   - Apple configuration profile automatically configures native clients with `<username>@cloudlift.run`.

---

## 8. Testing Strategy

1. **Unit Tests**:
   - `internal/identity/users_test.go`:
     - Test `Users.Create` formatting email as `<username>@<primaryDomain>`.
     - Test `Users.Provision` deriving username and setting primary email `<username>@<primaryDomain>`.
     - Test `Users.SetUsername` rejection (immutability).
   - `internal/identity/invites_test.go`:
     - Test `Invites.CreateInvite` storing `invited_email`.
     - Test `Invites.Accept` taking `username` and creating account with `<username>@<primaryDomain>`.
     - Test duplicate username rejection during invite accept.
2. **Web Tests**:
   - `internal/web/profile_test.go`:
     - Test `/invite/accept` GET renders username field and domain suffix.
     - Test `/invite/accept` POST provisions `<username>@<primaryDomain>`.
     - Test `/profile` displays read-only username/email and profile updates cannot change username.
3. **Integration Tests**:
   - Verify SMTP/IMAP authentication with `<username>` and `<username>@cloudlift.run`.
   - Verify incoming mail delivery to `<username>@cloudlift.run`.
   - Run full test suite: `go test -count=1 ./...` and `go build ./...`.
