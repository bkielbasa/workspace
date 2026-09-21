# Design Specification: Mail Settings, Signatures, and Rules Engine

- **Date:** 2026-09-21
- **Status:** Approved
- **Scope:** Architectural

---

## 1. Executive Summary

This specification defines the architecture, data models, schema migrations, and user flows to add **Mail Settings** to Workspace. Within Mail Settings, users can:
1. **Manage Signatures**: Create, edit, delete, and designate a default plain-text signature that is automatically injected into the mail compose window.
2. **Configure Rules (Outlook-Style)**: Create, edit, toggle, reorder, and delete incoming mail rules. Rules evaluate conditions against incoming messages (`From`, `To`, `Subject`, `Body`, `Has Attachment`) and execute actions (`Move to Folder`, `Mark as Read`, `Star / Flag`, `Move to Trash`, `Stop Processing`).
3. **Execute Rules on Demand**: Manually trigger rule evaluation against the existing `INBOX` directly from Mail Settings.

---

## 2. Database Schema & Migration

### 2.1 Migration `migrations/028_mail_signatures_and_rules.up.sql`

```sql
-- 1. Signatures Table
CREATE TABLE IF NOT EXISTS mail_signatures (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    content TEXT NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mail_signatures_user_id ON mail_signatures(user_id);

-- 2. Rules Table
CREATE TABLE IF NOT EXISTS mail_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    priority INT NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    match_mode VARCHAR(10) NOT NULL DEFAULT 'all',
    conditions JSONB NOT NULL DEFAULT '[]'::jsonb,
    actions JSONB NOT NULL DEFAULT '[]'::jsonb,
    stop_processing BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mail_rules_user_priority ON mail_rules(user_id, priority ASC);
```

### 2.2 Migration `migrations/028_mail_signatures_and_rules.down.sql`

```sql
DROP TABLE IF EXISTS mail_rules;
DROP TABLE IF EXISTS mail_signatures;
```

---

## 3. Domain Models & Storage Interfaces (`internal/mail`)

### 3.1 Types (`internal/mail/types.go` or `internal/mail/signatures.go` / `internal/mail/rules.go`)

```go
type Signature struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type RuleField string
const (
	FieldFrom          RuleField = "from"
	FieldTo            RuleField = "to"
	FieldSubject       RuleField = "subject"
	FieldBody          RuleField = "body"
	FieldHasAttachment RuleField = "has_attachment"
)

type RuleOperator string
const (
	OpContains    RuleOperator = "contains"
	OpNotContains RuleOperator = "not_contains"
	OpEquals      RuleOperator = "equals"
	OpNotEquals   RuleOperator = "not_equals"
	OpStartsWith  RuleOperator = "starts_with"
	OpEndsWith    RuleOperator = "ends_with"
	OpIs          RuleOperator = "is"
)

type RuleCondition struct {
	Field    RuleField    `json:"field"`
	Operator RuleOperator `json:"operator"`
	Value    string       `json:"value"`
}

type RuleActionType string
const (
	ActionMoveToFolder RuleActionType = "move_to_folder"
	ActionMarkRead     RuleActionType = "mark_read"
	ActionStar         RuleActionType = "star"
	ActionMoveToTrash  RuleActionType = "move_to_trash"
	ActionDelete       RuleActionType = "delete"
)

type RuleAction struct {
	Type   RuleActionType `json:"type"`
	Target string         `json:"target,omitempty"` // Mailbox name e.g. "Archive", "Trash"
}

type Rule struct {
	ID             uuid.UUID       `json:"id"`
	UserID         uuid.UUID       `json:"user_id"`
	Name           string          `json:"name"`
	Priority       int             `json:"priority"`
	Enabled        bool            `json:"enabled"`
	MatchMode      string          `json:"match_mode"` // "all" | "any"
	Conditions     []RuleCondition `json:"conditions"`
	Actions        []RuleAction    `json:"actions"`
	StopProcessing bool            `json:"stop_processing"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}
```

### 3.2 Repository Interfaces

```go
type SignatureRepository interface {
	Create(ctx context.Context, sig *Signature) (*Signature, error)
	GetByID(ctx context.Context, userID, id uuid.UUID) (*Signature, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]Signature, error)
	GetDefault(ctx context.Context, userID uuid.UUID) (*Signature, error)
	Update(ctx context.Context, sig *Signature) (*Signature, error)
	SetDefault(ctx context.Context, userID, id uuid.UUID) error
	Delete(ctx context.Context, userID, id uuid.UUID) error
}

type RuleRepository interface {
	Create(ctx context.Context, rule *Rule) (*Rule, error)
	GetByID(ctx context.Context, userID, id uuid.UUID) (*Rule, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]Rule, error)
	ListEnabled(ctx context.Context, userID uuid.UUID) ([]Rule, error)
	Update(ctx context.Context, rule *Rule) (*Rule, error)
	SetEnabled(ctx context.Context, userID, id uuid.UUID, enabled bool) error
	Reorder(ctx context.Context, userID uuid.UUID, orderedIDs []uuid.UUID) error
	Delete(ctx context.Context, userID, id uuid.UUID) error
}
```

---

## 4. Rules Evaluation Engine (`internal/mail/rules_engine.go`)

### 4.1 Evaluation Logic

The `RuleEngine` is responsible for evaluating rules against an email message and generating an execution plan:

```go
type RuleResult struct {
	TargetFolder string // Empty means default to "INBOX"
	MarkRead     bool
	Star         bool
	Discard      bool
}

type RuleEngine struct{}

func (e *RuleEngine) Evaluate(rules []Rule, msg *Message, parsedBody string, hasAttachments bool) RuleResult
```

1. **Rule Sorting**: Rules are evaluated in order of ascending `Priority`.
2. **Condition Matching**:
   * All string comparisons are normalized and case-insensitive.
   * `from`: Tested against `msg.Sender` (address and display name).
   * `to`: Tested against each recipient in `msg.Recipients`.
   * `subject`: Tested against `msg.Subject`.
   * `body`: Tested against `parsedBody` (extracted text from message MIME).
   * `has_attachment`: Evaluated against `hasAttachments` boolean.
   * Operators:
     * `contains` / `not_contains`: Substring match.
     * `equals` / `not_equals`: Exact match.
     * `starts_with` / `ends_with`: Prefix / suffix match.
     * `is`: Direct equality (used for boolean values like `has_attachment`).
3. **Match Mode**:
   * `"all"`: Every condition must evaluate to `true` (logical AND).
   * `"any"`: At least one condition must evaluate to `true` (logical OR).
4. **Action Application**:
   * If a rule matches:
     * Actions are applied into `RuleResult`.
     * If `rule.StopProcessing == true`, rule evaluation immediately halts, ignoring subsequent rules.

---

## 5. Integration with Delivery & Inbox Processing

### 5.1 Real-Time Delivery Hook (`internal/mail/delivery.go`)

In `Delivery.Deliver(ctx, recipient, message)`:
1. Lookup local recipient user.
2. Load active rules via `d.rules.ListEnabled(ctx, user.ID)`.
3. If rules exist:
   * Parse message text body and determine attachment presence.
   * Run `result := d.ruleEngine.Evaluate(rules, message, body, hasAttachments)`.
   * If `result.Discard`: message is dropped or immediately flagged deleted.
   * If `result.TargetFolder != ""`: find or create the mailbox by name for `user.ID`. Fall back to `"INBOX"` if resolution fails.
   * Apply flags: `message.Seen = message.Seen || result.MarkRead`, `message.Flagged = message.Flagged || result.Star`.
4. If no rule specified a folder, target mailbox is `"INBOX"`.
5. Append message to target mailbox and link to thread.

### 5.2 Manual INBOX Rule Execution (`internal/mail/service.go`)

Add `ApplyRulesToInbox(ctx context.Context, userID uuid.UUID) (int, error)` to `mail.Service`:
1. Find user's `"INBOX"` mailbox.
2. List messages currently in `"INBOX"`.
3. Load enabled rules.
4. For each message:
   * Evaluate rules against the message.
   * If a target folder other than `"INBOX"` is resolved: move message to target mailbox.
   * If `Seen` or `Flagged` flags changed: update message flags in repository.
   * Increment affected message counter.
5. Return count of affected messages.

---

## 6. Web UI & HTTP API

### 6.1 Navigation & Placement
- **In `/mail` (`web/templates/mail.html`)**:
  - Add a "Settings" link with gear icon at the bottom of the mail folder sidebar:
    ```html
    <div class="mail-sidebar-footer">
      <a href="/mail/settings" class="mail-settings-btn" title="Mail Settings">
        <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2">
          <!-- Gear icon SVG -->
        </svg>
        <span>Mail Settings</span>
      </a>
    </div>
    ```
- **In `/profile` (`web/templates/profile.html`)**:
  - Add a "Mail Preferences" card linking to `/mail/settings` for quick discovery.

### 6.2 Settings View (`web/templates/mail_settings.html`)
- Tabbed interface:
  - **Signatures Tab**:
    - List existing signatures with Name, preview snippet, and Default badge.
    - Buttons: "Edit", "Delete", "Set as Default".
    - "New Signature" creation form with Name, Textarea content, and "Set as default" checkbox.
  - **Rules Tab**:
    - Header with **"Run rules on Inbox now"** action button.
    - Rules table/list showing: Priority, Name, Enabled toggle, summary of conditions/actions, Reorder buttons (▲ / ▼), Edit and Delete.
    - Rule Editor (Add / Edit):
      - Rule Name
      - Match Condition: "Match all conditions (AND)" vs "Match any condition (OR)"
      - Condition Builder: Rows of [Field Selector] [Operator Selector] [Value Input] with "+ Add Condition" button.
      - Action Checkboxes:
        - [x] Move to folder: [Folder Dropdown (INBOX, Archive, Trash, Spam, custom folders)]
        - [x] Mark as read
        - [x] Star / Flag
        - [x] Stop processing further rules (checked by default)

### 6.3 Compose Integration (`web/static/js/mail.js`)
- Store user's default signature and signatures list in page context.
- When opening the compose dock (`openCompose()`):
  - If `#compose-body` is empty and default signature exists:
    - Pre-fill `#compose-body` with `\n\n-- \n` + default signature content.
    - Position cursor at the beginning of the text area.
- If multiple signatures exist:
  - Provide a signature selection dropdown in the compose dock footer to quickly insert or change the signature.

### 6.4 HTTP Endpoints (`internal/web/web_http.go`)
- `GET /mail/settings`: Render Mail Settings page with signatures and rules.
- `POST /mail/settings/signatures`: Create or update signature.
- `POST /mail/settings/signatures/{id}/delete`: Delete signature.
- `POST /mail/settings/signatures/{id}/default`: Set signature as default.
- `POST /mail/settings/rules`: Create or update rule.
- `POST /mail/settings/rules/{id}/delete`: Delete rule.
- `POST /mail/settings/rules/{id}/toggle`: Toggle rule enabled/disabled.
- `POST /mail/settings/rules/reorder`: Update rule priorities.
- `POST /mail/settings/rules/apply-inbox`: Apply enabled rules to current INBOX.

All mutating endpoints require valid user authentication and CSRF token.

---

## 7. Security, Multi-Tenancy & Edge Cases

1. **Tenant Isolation**: Every database query for signatures and rules strictly enforces `WHERE user_id = $1`. Users can never view, mutate, or trigger another user's configurations.
2. **Safe Fallbacks on Missing Folders**: If a rule specifies a folder that does not exist or fails resolution, the delivery logs a warning and falls back to `"INBOX"`. Mail is never silently lost.
3. **Loop Prevention & Performance**:
   - Single message rule evaluation runs in-memory and completes in <1ms.
   - Stop processing flag halts unnecessary further evaluations.
   - Text extraction for rule evaluation reuses existing MIME parsing logic.
4. **HTML Sanitization**: Signatures are plain text and escaped by Go's HTML template engine.

---

## 8. Testing Strategy

1. **Postgres Repositories (`internal/postgres`)**:
   - Signature repository tests: CRUD, default switching in transaction, user isolation.
   - Rule repository tests: CRUD, JSONB serialization/deserialization, priority reordering, user isolation.
2. **Rules Engine Unit Tests (`internal/mail/rules_test.go`)**:
   - Comprehensive matrix covering every condition operator (`contains`, `equals`, `starts_with`, `ends_with`, `has_attachment`).
   - Testing `all` vs `any` match modes.
   - Testing action aggregation and `stop_processing` behavior.
3. **Delivery Integration Tests (`internal/mail/delivery_test.go`)**:
   - Test incoming email triggering rules to move to Archive / Trash and mark seen / flagged.
   - Test `ApplyRulesToInbox` batch processing.
4. **Web UI Tests (`internal/web`)**:
   - Test GET `/mail/settings`, signature creation, rule creation, reordering, and manual inbox rule application.
5. **Full Suite**:
   - `go test -count=1 ./...` and `go build ./...` with zero failures.
