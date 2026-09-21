# Unified Settings Hub Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unify all application and account settings into a single centralized Settings Hub (`/settings`) featuring a split-pane layout with grouped navigation on the left and a dedicated settings configuration panel on the right, removing legacy `/profile` and `/mail/settings` endpoints.

**Architecture:** A two-panel grid layout (`settings-page`) matching Mail and Contacts. The left sidebar groups configuration options with category headers and dividers (`GENERAL`, `MAIL`, `DEVICES & CLIENTS`, `ADMINISTRATION`). The right panel renders the active section based on the `?section=` query parameter. Legacy endpoints are completely removed with no backward compatibility layer.

**Tech Stack:** Go (1.22+), Go standard library `html/template`, `net/http`, CSS Grid/Flexbox, PostgreSQL, HTMX.

**Spec:** `docs/superpowers/specs/2026-09-21-unified-settings-hub-design.md`

## Global Constraints

- Target Go version: 1.22+
- All IDs use UUIDs (`github.com/google/uuid`)
- Multi-tenancy: Every query and mutation strictly enforces `user_id = $1`
- Security: All POST mutation endpoints must be protected by `RequireAuth` and `RequireCSRF`
- Direct routing only: No backward compatibility aliases or redirects for `/profile` or `/mail/settings`
- Full-suite pass: `go test -count=1 ./...` and `go build ./...` must succeed with zero errors across all packages

---

### Task 1: CSS Framework & Unified Settings Template

**Files:**
- Modify: `web/static/css/app.css`
- Create: `web/templates/settings.html`
- Modify: `internal/web/view.go`
- Test: `internal/web/settings_template_test.go`

**Interfaces:**
- Consumes: `viewData` struct in `internal/web/view.go`
- Produces: `settingsT` compiled template and CSS styling for `.settings-page`, `.settings-sidebar`, `.settings-main`, `.settings-nav-group-title`, `.settings-nav-divider`, `.settings-nav-item`

- [ ] **Step 1: Write the failing test for settings template compilation and rendering**

Create `internal/web/settings_template_test.go`:
```go
package web

import (
	"bytes"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

func TestSettingsTemplateRendering(t *testing.T) {
	v, err := NewView()
	if err != nil {
		t.Fatalf("failed to initialize view: %v", err)
	}

	uid := uuid.New()
	data := &viewData{
		Title:   "Settings",
		Section: "settings",
		User: &identity.User{
			ID:          uid,
			Email:       "alice@cloudlift.run",
			Username:    "alice",
			DisplayName: "Alice Smith",
			CreatedAt:   time.Now(),
		},
		Signatures: []mail.Signature{
			{ID: uuid.New(), UserID: uid, Name: "Default", Body: "Best regards,\nAlice", IsDefault: true},
		},
		Rules: []mail.Rule{
			{ID: uuid.New(), UserID: uid, Name: "Test Rule", Enabled: true, Priority: 1},
		},
		Mailboxes: []mail.Mailbox{
			{ID: uuid.New(), UserID: uid, Name: "INBOX"},
		},
		CSRFToken: "dummy-csrf",
	}

	sections := []string{"profile", "password", "signatures", "rules", "iphone", "app-passwords", "invites"}
	for _, sec := range sections {
		data.Tab = sec
		var buf bytes.Buffer
		if err := v.settingsT.ExecuteTemplate(&buf, "layout", data); err != nil {
			t.Fatalf("failed to execute settings template for section %q: %v", sec, err)
		}
		output := buf.String()
		if len(output) == 0 {
			t.Errorf("expected non-empty output for section %q", sec)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/web -run TestSettingsTemplateRendering`
Expected: FAIL (`v.settingsT undefined`)

- [ ] **Step 3: Add CSS classes to `web/static/css/app.css`**

Add settings layout and component classes in `web/static/css/app.css`:
```css
/* ---- Settings Page --------------------------------------------------- */

.settings-page {
  display: grid;
  grid-template-columns: 18rem 1fr;
  gap: 1.25rem;
  align-items: start;
  min-height: calc(100vh - 8rem);
}

.settings-sidebar {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 1rem 0.75rem;
}

.settings-nav-group {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.settings-nav-group-title {
  font-size: 0.75rem;
  font-weight: 700;
  letter-spacing: 0.05em;
  color: var(--text-muted);
  padding: 0.5rem 0.75rem 0.25rem;
  text-transform: uppercase;
}

.settings-nav-divider {
  border-top: 1px solid var(--border);
  margin: 0.5rem 0;
}

.settings-nav-item {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.6rem 0.75rem;
  border-radius: 6px;
  color: var(--text);
  text-decoration: none;
  font-size: 0.95rem;
  font-weight: 500;
  transition: background 0.15s ease, color 0.15s ease;
}

.settings-nav-item:hover {
  background: var(--surface-2);
}

.settings-nav-item.is-active {
  background: var(--accent-soft);
  color: var(--accent-text);
  font-weight: 600;
}

.settings-nav-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 1.25rem;
  height: 1.25rem;
  flex-shrink: 0;
}

.settings-main {
  padding: 1.5rem;
}

.settings-header {
  margin-bottom: 1.5rem;
  padding-bottom: 1rem;
  border-bottom: 1px solid var(--border);
}

.settings-header h2 {
  margin: 0 0 0.25rem;
  font-size: 1.5rem;
  font-weight: 700;
}

.settings-header .subtitle {
  margin: 0;
  color: var(--text-muted);
  font-size: 0.95rem;
}

.settings-alert {
  padding: 0.75rem 1rem;
  border-radius: 6px;
  margin-bottom: 1.5rem;
  font-size: 0.9rem;
}

.settings-alert.success {
  background: var(--success-surface);
  border: 1px solid var(--success-border);
  color: var(--success);
}

.settings-alert.error {
  background: var(--danger-surface);
  border: 1px solid var(--danger-border);
  color: var(--danger);
}

@media (max-width: 768px) {
  .settings-page {
    grid-template-columns: 1fr;
  }
}
```

- [ ] **Step 4: Create `web/templates/settings.html` and compile in `internal/web/view.go`**

Create `web/templates/settings.html` containing the sidebar navigation with grouped titles/dividers and all 7 section panels (`profile`, `password`, `signatures`, `rules`, `iphone`, `app-passwords`, `invites`).

In `internal/web/view.go`:
- Add `settingsT *template.Template` to `View`.
- In `NewView()`: compile `settingsT, err := page("web/templates/settings.html")`.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -v ./internal/web -run TestSettingsTemplateRendering`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add web/static/css/app.css web/templates/settings.html internal/web/view.go internal/web/settings_template_test.go
git commit -m "feat(web): add unified settings page layout and styles"
```

---

### Task 2: Web Server Route Registration & Settings Handlers

**Files:**
- Create: `internal/web/settings.go`
- Modify: `internal/web/web_http.go`
- Modify: `internal/web/view.go`
- Remove: `internal/web/profile_http.go`
- Remove: `internal/web/mail_settings.go`

**Interfaces:**
- Consumes: `users`, `signatures`, `rules`, `mailboxes`, `invites`, `authTokens`
- Produces: `GET /settings`, and all `POST /settings/...` endpoints

- [ ] **Step 1: Write unit tests for `/settings` HTTP handlers**

Create `internal/web/settings_handlers_test.go`:
- Test `GET /settings` renders default section `profile`.
- Test `GET /settings?section=signatures` renders signatures list.
- Test `GET /settings?section=rules` renders rules list.
- Test `POST /settings/profile` updates display name and redirects to `/settings?section=profile&success=profile`.
- Test `POST /settings/password` changes password and redirects to `/settings?section=password&success=password`.
- Test `POST /settings/signatures` creates signature and redirects to `/settings?section=signatures`.
- Test `POST /settings/rules` creates rule and redirects to `/settings?section=rules`.
- Test `POST /settings/rules/apply-inbox` invokes `ApplyRulesToInbox` and redirects to `/settings?section=rules`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/web -run TestSettingsHandlers`
Expected: FAIL

- [ ] **Step 3: Implement `internal/web/settings.go` and update `internal/web/web_http.go`**

Implement handlers in `internal/web/settings.go`:
- `settingsPage(w http.ResponseWriter, r *http.Request)`
- `settingsProfile(w http.ResponseWriter, r *http.Request)`
- `settingsPassword(w http.ResponseWriter, r *http.Request)`
- `settingsSignatureCreateOrUpdate(w http.ResponseWriter, r *http.Request)`
- `settingsSignatureSetDefault(w http.ResponseWriter, r *http.Request)`
- `settingsSignatureDelete(w http.ResponseWriter, r *http.Request)`
- `settingsRuleCreate(w http.ResponseWriter, r *http.Request)`
- `settingsRuleToggle(w http.ResponseWriter, r *http.Request)`
- `settingsRuleDelete(w http.ResponseWriter, r *http.Request)`
- `settingsRuleReorder(w http.ResponseWriter, r *http.Request)`
- `settingsRuleApplyInbox(w http.ResponseWriter, r *http.Request)`
- `settingsIPhoneProfile(w http.ResponseWriter, r *http.Request)`
- `settingsIPhoneProfileDownload(w http.ResponseWriter, r *http.Request)`
- `settingsAppPasswordCreate(w http.ResponseWriter, r *http.Request)`
- `settingsAppPasswordRevoke(w http.ResponseWriter, r *http.Request)`
- `settingsInviteCreate(w http.ResponseWriter, r *http.Request)`
- `settingsInviteDelete(w http.ResponseWriter, r *http.Request)`

In `internal/web/web_http.go`:
- Register all `/settings` routes on the mux.
- Remove obsolete `/profile` and `/mail/settings` routes.
- Remove `profile_http.go` and `mail_settings.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/web -run TestSettingsHandlers`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/web/settings.go internal/web/settings_handlers_test.go internal/web/web_http.go internal/web/view.go
git rm internal/web/profile_http.go internal/web/mail_settings.go
git commit -m "feat(web): implement unified settings HTTP handlers and routes"
```

---

### Task 3: Template Unification Across the App & Legacy Cleanup

**Files:**
- Modify: `web/templates/nav.html`
- Modify: `web/templates/home.html`
- Modify: `web/templates/mail.html`
- Remove: `web/templates/profile.html`
- Remove: `web/templates/mail_settings.html`

**Interfaces:**
- Consumes: `/settings` route
- Produces: Navigation links across all templates pointing consistently to `/settings`

- [ ] **Step 1: Write template navigation check test**

In `internal/web/settings_template_test.go`, add `TestTemplateNavigationLinks`:
- Render `nav.html` and verify it contains `href="/settings"`.
- Render `home.html` and verify it contains `href="/settings"`.
- Render `mail.html` and verify it contains `href="/settings?section=signatures"`.
- Verify neither `profile.html` nor `mail_settings.html` exist.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/web -run TestTemplateNavigationLinks`
Expected: FAIL

- [ ] **Step 3: Update `nav.html`, `home.html`, and `mail.html`, and remove legacy templates**

- In `web/templates/nav.html`:
  - Update nav link: `<a href="/settings" {{if eq .Section "settings"}}aria-current="page"{{end}}>Settings</a>`
  - Update user menu: `<a href="/settings" class="nav-user-link" ... title="Account settings">...</a>`
- In `web/templates/home.html`:
  - `<a class="btn btn-ghost" href="/settings">Settings</a>`
- In `web/templates/mail.html`:
  - `<a href="/settings?section=signatures" class="mail-folder-item" title="Mail Settings">`
- Remove `web/templates/profile.html` and `web/templates/mail_settings.html`.
- In `internal/web/view.go`: remove `profileT` and `mailSettingsT` from `View` and `NewView()`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/web -run TestTemplateNavigationLinks`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/templates/nav.html web/templates/home.html web/templates/mail.html internal/web/view.go internal/web/settings_template_test.go
git rm web/templates/profile.html web/templates/mail_settings.html
git commit -m "feat(web): update app templates to point to /settings and remove legacy templates"
```

---

### Task 4: Test Suite Migration & End-to-End Verification

**Files:**
- Modify: `internal/web/profile_test.go` -> update or rename to use `/settings`
- Modify: `internal/web/mail_settings_test.go` -> update or rename to use `/settings`
- Modify: `tests/integration/mail_settings_e2e_test.go` -> update routes to `/settings/signatures` and `/settings/rules`

**Interfaces:**
- Consumes: All `/settings` routes and handlers
- Produces: 100% green test suite across all 22 packages

- [ ] **Step 1: Update existing web tests to `/settings` endpoints**

Update test assertions in `internal/web/profile_test.go` and `internal/web/mail_settings_test.go`:
- Change `GET /profile` to `GET /settings?section=profile`.
- Change `POST /profile` to `POST /settings/profile`.
- Change `POST /profile/password` to `POST /settings/password`.
- Change `POST /profile/app-passwords` to `POST /settings/app-passwords`.
- Change `POST /profile/iphone-profile` to `POST /settings/iphone-profile`.
- Change `GET /mail/settings` to `GET /settings?section=signatures`.
- Change `POST /mail/settings/signatures...` to `POST /settings/signatures...`.
- Change `POST /mail/settings/rules...` to `POST /settings/rules...`.
- Update redirect location expectations (e.g. `Location == "/settings?section=profile&success=profile"`).

- [ ] **Step 2: Update end-to-end integration test**

In `tests/integration/mail_settings_e2e_test.go`:
- Update any HTTP web requests to use `/settings/signatures` and `/settings/rules`.

- [ ] **Step 3: Run full test suite and verify clean compilation**

Run: `go test -count=1 ./...`
Expected: PASS across all 22 packages with 0 failures.

Run: `go build ./...`
Expected: PASS with 0 build warnings or errors.

- [ ] **Step 4: Commit**

```bash
git add internal/web/ tests/integration/
git commit -m "test(web): migrate web and integration tests to unified settings routes"
```
