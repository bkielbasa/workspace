# Unified Settings Hub Design Specification

## Overview

This specification unifies all application and account settings into a centralized, scalable Settings Hub (`/settings`). The design replaces disparate profile and mail settings pages with a standardized split-pane layout consisting of a categorized left navigation sidebar and a focused right-hand configuration panel.

Per explicit architectural direction, legacy endpoints (`/profile`, `/mail/settings`) and backward compatibility layers are removed in favor of clean, direct `/settings` routing.

---

## Architecture & Layout

### 1. Two-Column Split Layout

The Settings view adheres to the application's standard split-pane visual hierarchy (`mail.html`, `contacts.html`):

```
┌───────────────────────────────────────────────────────────────────────────────────────┐
│ Top Nav: Workspace  Home  Mail  Contacts  Calendars  Notes  Files  Photos  Settings   │
├─────────────────────────┬─────────────────────────────────────────────────────────────┤
│ SETTINGS (Sidebar)      │ SETTINGS WORKSPACE (Right Panel)                            │
│                         │                                                             │
│ GENERAL                 │ Signatures                                                  │
│   👤 Profile Details    │ Manage your email signatures attached to outgoing messages  │
│   🔒 Password & Security├─────────────────────────────────────────────────────────────┤
│                         │ [✓ Signature saved successfully.                          ] │
│ MAIL                    │                                                             │
│ ► ✍️ Signatures         │ YOUR SIGNATURES                                             │
│   ⚡ Mail Rules         │ ┌─────────────────────────────────────────────────────────┐ │
│                         │ │ Work Default [Default]                                  │ │
│ DEVICES & CLIENTS       │ │ Best regards,                                           │ │
│   📱 Apple / iOS Setup  │ │ Alice Smith                                             │ │
│   🔑 App Passwords      │ │ [Edit] [Delete]                                         │ │
│                         │ └─────────────────────────────────────────────────────────┘ │
│ ADMINISTRATION          │                                                             │
│   ✉️ User Invites       │ CREATE / EDIT SIGNATURE                                     │
│                         │ Name:    [Work Default                  ]                   │
│                         │ Body:    [Best regards,                 ]                   │
│                         │          [Alice Smith                   ]                   │
│                         │ [✓] Set as default signature                                │
│                         │ [Save signature]                                            │
└─────────────────────────┴─────────────────────────────────────────────────────────────┘
```

- **Container**: `<section class="settings-page">` (CSS Grid: `18rem 1fr`, gap `1.25rem`, min-height `calc(100vh - 8rem)`).
- **Sidebar**: `<aside class="settings-sidebar card" aria-label="Settings Categories">`.
- **Right Panel**: `<article class="settings-main card" aria-label="Active Settings">`.

---

## Navigation & Grouped Headers

The sidebar navigation segments settings into distinct groups separated by uppercase category titles and subtle divider lines:

### Group 1: General
- **Profile Details** (`section=profile`): Email (readonly), Username (readonly with permanence badge), Display Name (editable), Member Since date.
- **Password & Security** (`section=password`): Current password, New password (min 8 chars), Confirm password.

### Group 2: Mail Settings
- **Signatures** (`section=signatures`): List of named signatures with default badge, set default action, inline edit, delete, and signature create/edit form.
- **Mail Rules** (`section=rules`): List of rules with priority up/down reordering, active toggle, delete, rule summary, "Run rules on Inbox now" button, and dynamic condition/action builder.

### Group 3: Devices & Clients
- **Apple / iOS Setup** (`section=iphone`): Device name input, Apple mobileconfig configuration profile generator with embedded app password, and fallback direct profile download.
- **App Passwords** (`section=app-passwords`): List of active app passwords with revocation buttons, one-time reveal banner for newly created credentials, and creation form.

### Group 4: Administration (Conditional on `User.IsAdmin`)
- **User Invites** (`section=invites`): List of pending invites with expiration status and deletion, and invite dispatch form (email, display name).

---

## HTTP Routes & Handlers

All settings interactions are grouped under `/settings`:

### Page Route
- `GET /settings`: Renders `web/templates/settings.html`.
  - Reads `?section=...` query parameter (defaults to `profile`).
  - Accepts `?success=...` and `?error=...` flash query parameters to render feedback alerts.
  - Queries domain data scoped to the user and active section:
    - User account info
    - Mailboxes (for rule folder routing targets)
    - Signatures and rules
    - App passwords
    - Pending user invites (if admin)

### Mutation Routes (All require Authentication & CSRF token)
1. **Profile**: `POST /settings/profile` -> redirects to `/settings?section=profile&success=profile`
2. **Password**: `POST /settings/password` -> redirects to `/settings?section=password&success=password`
3. **Signatures**:
   - `POST /settings/signatures` -> creates/updates signature -> redirects to `/settings?section=signatures&success=signature_saved`
   - `POST /settings/signatures/{id}/default` -> sets default -> redirects to `/settings?section=signatures`
   - `POST /settings/signatures/{id}/delete` -> deletes signature -> redirects to `/settings?section=signatures`
4. **Rules**:
   - `POST /settings/rules` -> creates rule -> redirects to `/settings?section=rules&success=rule_created`
   - `POST /settings/rules/{id}/toggle` -> toggles rule state -> redirects to `/settings?section=rules`
   - `POST /settings/rules/{id}/delete` -> deletes rule -> redirects to `/settings?section=rules`
   - `POST /settings/rules/reorder` -> moves rule up/down -> redirects to `/settings?section=rules`
   - `POST /settings/rules/apply-inbox` -> applies rules to inbox -> redirects to `/settings?section=rules&success=rules_applied&count=N`
5. **Devices & Clients**:
   - `POST /settings/iphone-profile` -> generates token and redirects to `/settings/iphone-profile/download?token=...`
   - `GET /settings/iphone-profile/download` -> serves mobileconfig payload
   - `POST /settings/app-passwords` -> creates credential -> redirects to `/settings?section=app-passwords&new_pass=...`
   - `POST /settings/app-passwords/revoke` -> revokes credential -> redirects to `/settings?section=app-passwords`
6. **Invites**:
   - `POST /settings/invites` -> creates invite -> redirects to `/settings?section=invites&success=invite`
   - `POST /settings/invites/delete` -> deletes invite -> redirects to `/settings?section=invites`

---

## Template Refactoring & Consistency

### 1. Deleted Templates
- `web/templates/profile.html`
- `web/templates/mail_settings.html`

### 2. New Template
- `web/templates/settings.html`:
  - Contains `<section class="settings-page">`.
  - Left navigation menu `<aside class="settings-sidebar card">` with group titles and dividers.
  - Right panel `<article class="settings-main card">` rendering the active section based on `.Section`.

### 3. Updated Existing Templates
- `web/templates/nav.html`:
  - Replace `Profile` with `Settings` (`/settings`) in `.nav-links`.
  - In `.nav-user`, user name/profile link points to `/settings` with `title="Account settings"`.
- `web/templates/home.html`:
  - Replace `Profile settings` action button with `Settings` (`/settings`).
- `web/templates/mail.html`:
  - Folder list link and gear icon point directly to `/settings?section=signatures`.

### 4. CSS Additions (`web/static/css/app.css`)
- `.settings-page`: Two-column grid matching `.mail-page` and `.contacts-page`.
- `.settings-sidebar`: Sticky sidebar card.
- `.settings-nav-group`: Wrapper for grouped navigation items.
- `.settings-nav-group-title`: Small uppercase muted label.
- `.settings-nav-divider`: Divider rule separating groups.
- `.settings-nav-item`: Navigation item with icon and label, matching `.mail-folder-item`.
- `.settings-nav-item.is-active`: Highlighted active item state (`--accent-soft` background, `--accent-text` color).
- `.settings-main`: Content card with unified header, alert styling, and form actions.

---

## Testing & Verification

1. **Unit Tests (`internal/web`)**:
   - Update `profile_test.go` and `mail_settings_test.go` (or consolidate into `settings_test.go`) to target `/settings` and `/settings/*` endpoints.
   - Verify all sections render properly with their respective data.
   - Verify all form submissions redirect cleanly to `/settings?section=...`.
2. **Integration & E2E Tests**:
   - Update `tests/integration/mail_settings_e2e_test.go` to use `/settings/signatures` and `/settings/rules` routes.
   - Verify full test suite passes with `go test -count=1 ./...` and `go build ./...` compiles cleanly.
