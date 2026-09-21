# Mail Settings, Signatures, and Rules Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Mail Settings to Workspace allowing users to create and manage email signatures (with a designated default inserted into compose) and configure Outlook-style incoming mail rules (with conditions, actions, reordering, and on-demand inbox execution).

**Architecture:** 
1. Relational tables `mail_signatures` and `mail_rules` (JSONB for conditions & actions) with user-isolated Postgres repositories.
2. An in-memory `RuleEngine` evaluating message headers, body, and attachment presence against rules sorted by priority with `stop_processing` support.
3. Integration with `mail.Delivery.Deliver` for automatic routing upon receipt, and `mail.Service.ApplyRulesToInbox` for manual processing.
4. Web UI under `/mail/settings` with Signatures and Rules tabs, linked from `/mail` and `/profile`, plus compose auto-fill via JavaScript.

**Tech Stack:** Go 1.22+, PostgreSQL, HTML templates, Vanilla JS, CSS.

**Spec:** [`docs/superpowers/specs/2026-09-21-mail-settings-signatures-and-rules-design.md`](file:///Users/bartlomiej.klimczak/Projects/workspace/docs/superpowers/specs/2026-09-21-mail-settings-signatures-and-rules-design.md)

## Global Constraints
- Target Go version: 1.22+
- All IDs use UUIDs (`github.com/google/uuid`)
- Multi-tenancy: every database operation must enforce `user_id = $1`
- CSRF protection: every mutating POST handler must require auth and CSRF
- Fallback resilience: rule action targeting a missing mailbox must log a warning and fallback to `"INBOX"`, never dropping messages
- Full regression suite passing: `go test -count=1 ./...` and `go build ./...`

---

### Task 1: Migration 028 & PostgreSQL Repositories (`mail_signatures`, `mail_rules`)

**Files:**
- Create: `migrations/028_mail_signatures_and_rules.up.sql`
- Create: `migrations/028_mail_signatures_and_rules.down.sql`
- Create: `internal/mail/signatures.go`
- Create: `internal/mail/rules.go`
- Create: `internal/postgres/mail_settings.go`
- Test: `internal/postgres/mail_settings_test.go`

**Interfaces:**
- Consumes: `*sql.DB` from `internal/postgres`
- Produces:
  ```go
  // internal/mail/signatures.go
  type Signature struct {
      ID        uuid.UUID `json:"id"`
      UserID    uuid.UUID `json:"user_id"`
      Name      string    `json:"name"`
      Content   string    `json:"content"`
      IsDefault bool      `json:"is_default"`
      CreatedAt time.Time `json:"created_at"`
      UpdatedAt time.Time `json:"updated_at"`
  }

  type SignatureRepository interface {
      Create(ctx context.Context, sig *Signature) (*Signature, error)
      GetByID(ctx context.Context, userID, id uuid.UUID) (*Signature, error)
      ListByUser(ctx context.Context, userID uuid.UUID) ([]Signature, error)
      GetDefault(ctx context.Context, userID uuid.UUID) (*Signature, error)
      Update(ctx context.Context, sig *Signature) (*Signature, error)
      SetDefault(ctx context.Context, userID, id uuid.UUID) error
      Delete(ctx context.Context, userID, id uuid.UUID) error
  }

  // internal/mail/rules.go
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
      Target string         `json:"target,omitempty"`
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

- [ ] **Step 1: Write migration files `028_mail_signatures_and_rules`**

Create `migrations/028_mail_signatures_and_rules.up.sql`:
```sql
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

Create `migrations/028_mail_signatures_and_rules.down.sql`:
```sql
DROP TABLE IF EXISTS mail_rules;
DROP TABLE IF EXISTS mail_signatures;
```

- [ ] **Step 2: Create domain definitions `internal/mail/signatures.go` and `internal/mail/rules.go`**

Define `Signature`, `SignatureRepository`, `Rule`, `RuleCondition`, `RuleAction`, and `RuleRepository` as specified in Interfaces.

- [ ] **Step 3: Write failing integration test for Postgres repositories**

Create `internal/postgres/mail_settings_test.go`:
```go
package postgres

import (
	"context"
	"testing"

	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

func TestPostgresSignaturesAndRules(t *testing.T) {
	db := testDB(t)
	sigRepo := NewSignatureRepository(db)
	ruleRepo := NewRuleRepository(db)
	ctx := context.Background()

	userID := createTestUser(t, db, "user1@cloudlift.run", "user1")
	otherUser := createTestUser(t, db, "user2@cloudlift.run", "user2")

	// 1. Create signature
	sig1, err := sigRepo.Create(ctx, &mail.Signature{
		UserID:    userID,
		Name:      "Work",
		Content:   "Best regards,\nAlice",
		IsDefault: true,
	})
	if err != nil {
		t.Fatalf("create sig1: %v", err)
	}

	sig2, err := sigRepo.Create(ctx, &mail.Signature{
		UserID:    userID,
		Name:      "Personal",
		Content:   "Cheers,\nAli",
		IsDefault: false,
	})
	if err != nil {
		t.Fatalf("create sig2: %v", err)
	}

	// 2. Set default switches default atomically
	if err := sigRepo.SetDefault(ctx, userID, sig2.ID); err != nil {
		t.Fatalf("set default sig2: %v", err)
	}
	def, err := sigRepo.GetDefault(ctx, userID)
	if err != nil || def.ID != sig2.ID {
		t.Fatalf("expected def sig2, got %v (err: %v)", def, err)
	}

	// 3. User isolation
	otherList, err := sigRepo.ListByUser(ctx, otherUser)
	if err != nil || len(otherList) != 0 {
		t.Fatalf("otherUser signatures leaked: %v", otherList)
	}

	// 4. Create Rules
	r1, err := ruleRepo.Create(ctx, &mail.Rule{
		UserID:    userID,
		Name:      "Archive Newsletters",
		Priority:  1,
		Enabled:   true,
		MatchMode: "all",
		Conditions: []mail.RuleCondition{
			{Field: mail.FieldFrom, Operator: mail.OpContains, Value: "newsletter"},
		},
		Actions: []mail.RuleAction{
			{Type: mail.ActionMoveToFolder, Target: "Archive"},
			{Type: mail.ActionMarkRead},
		},
		StopProcessing: true,
	})
	if err != nil {
		t.Fatalf("create rule1: %v", err)
	}

	r2, err := ruleRepo.Create(ctx, &mail.Rule{
		UserID:    userID,
		Name:      "Star Boss",
		Priority:  2,
		Enabled:   true,
		MatchMode: "all",
		Conditions: []mail.RuleCondition{
			{Field: mail.FieldFrom, Operator: mail.OpEquals, Value: "boss@corp.com"},
		},
		Actions: []mail.RuleAction{
			{Type: mail.ActionStar},
		},
		StopProcessing: false,
	})
	if err != nil {
		t.Fatalf("create rule2: %v", err)
	}

	// 5. Reorder rules
	if err := ruleRepo.Reorder(ctx, userID, []uuid.UUID{r2.ID, r1.ID}); err != nil {
		t.Fatalf("reorder rules: %v", err)
	}
	list, err := ruleRepo.ListByUser(ctx, userID)
	if err != nil || len(list) != 2 || list[0].ID != r2.ID {
		t.Fatalf("expected r2 first after reorder, got %v", list)
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test -v ./internal/postgres -run TestPostgresSignaturesAndRules`
Expected: Compilation failure or undefined functions `NewSignatureRepository`, `NewRuleRepository`.

- [ ] **Step 5: Implement `internal/postgres/mail_settings.go`**

Implement `signatureRepository` and `ruleRepository`:
- Query/insert/update/delete with JSONB marshaling/unmarshaling for `conditions` and `actions`.
- In `SetDefault`: use a transaction to `UPDATE mail_signatures SET is_default = FALSE WHERE user_id = $1` and `UPDATE mail_signatures SET is_default = TRUE WHERE user_id = $1 AND id = $2`.
- In `Reorder`: update `priority` based on index of `orderedIDs`.

- [ ] **Step 6: Run test to verify it passes**

Run: `go test -v ./internal/postgres -run TestPostgresSignaturesAndRules`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add migrations/028* internal/mail/signatures.go internal/mail/rules.go internal/postgres/mail_settings*
git commit -m "feat(db,mail): add mail_signatures and mail_rules schema and postgres repositories"
```

---

### Task 2: Rules Evaluation Engine (`internal/mail/rules_engine.go`)

**Files:**
- Create: `internal/mail/rules_engine.go`
- Test: `internal/mail/rules_engine_test.go`

**Interfaces:**
- Consumes: `Rule`, `RuleCondition`, `RuleAction`, `Message` from `internal/mail`
- Produces:
  ```go
  type RuleResult struct {
      TargetFolder string
      MarkRead     bool
      Star         bool
      Discard      bool
  }

  type RuleEngine struct{}
  func NewRuleEngine() *RuleEngine
  func (e *RuleEngine) Evaluate(rules []Rule, msg *Message, parsedBody string, hasAttachments bool) RuleResult
  ```

- [ ] **Step 1: Write unit tests for condition matching, match modes, actions, and stop_processing**

Create `internal/mail/rules_engine_test.go`:
```go
package mail

import (
	"testing"

	"github.com/google/uuid"
)

func TestRuleEngine_ConditionOperators(t *testing.T) {
	engine := NewRuleEngine()
	msg := &Message{
		Sender:     "alice@example.com",
		Recipients: []string{"bob@example.com", "team@example.com"},
		Subject:    "Weekly Report [URGENT]",
	}
	body := "Please review the attached quarterly summary."
	hasAtt := true

	tests := []struct {
		name      string
		condition RuleCondition
		expected  bool
	}{
		{"from contains", RuleCondition{Field: FieldFrom, Operator: OpContains, Value: "example.com"}, true},
		{"from equals", RuleCondition{Field: FieldFrom, Operator: OpEquals, Value: "alice@example.com"}, true},
		{"from not contains", RuleCondition{Field: FieldFrom, Operator: OpNotContains, Value: "spam.com"}, true},
		{"to contains", RuleCondition{Field: FieldTo, Operator: OpContains, Value: "team@"}, true},
		{"subject starts with", RuleCondition{Field: FieldSubject, Operator: OpStartsWith, Value: "weekly"}, true},
		{"subject ends with", RuleCondition{Field: FieldSubject, Operator: OpEndsWith, Value: "[urgent]"}, true},
		{"body contains", RuleCondition{Field: FieldBody, Operator: OpContains, Value: "quarterly"}, true},
		{"has attachment is true", RuleCondition{Field: FieldHasAttachment, Operator: OpIs, Value: "true"}, true},
		{"has attachment is false", RuleCondition{Field: FieldHasAttachment, Operator: OpIs, Value: "false"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := Rule{
				Enabled:        true,
				MatchMode:      "all",
				Conditions:     []RuleCondition{tc.condition},
				Actions:        []RuleAction{{Type: ActionMarkRead}},
				StopProcessing: true,
			}
			res := engine.Evaluate([]Rule{rule}, msg, body, hasAtt)
			if tc.expected && !res.MarkRead {
				t.Errorf("expected match for %s", tc.name)
			} else if !tc.expected && res.MarkRead {
				t.Errorf("expected no match for %s", tc.name)
			}
		})
	}
}

func TestRuleEngine_StopProcessingAndOrder(t *testing.T) {
	engine := NewRuleEngine()
	msg := &Message{
		Sender:  "alerts@service.com",
		Subject: "Critical Alert",
	}

	rules := []Rule{
		{
			ID:             uuid.New(),
			Priority:       1,
			Enabled:        true,
			MatchMode:      "all",
			Conditions:     []RuleCondition{{Field: FieldSubject, Operator: OpContains, Value: "Critical"}},
			Actions:        []RuleAction{{Type: ActionMoveToFolder, Target: "Urgent"}, {Type: ActionStar}},
			StopProcessing: true,
		},
		{
			ID:             uuid.New(),
			Priority:       2,
			Enabled:        true,
			MatchMode:      "all",
			Conditions:     []RuleCondition{{Field: FieldFrom, Operator: OpContains, Value: "alerts@"}},
			Actions:        []RuleAction{{Type: ActionMoveToFolder, Target: "Archive"}},
			StopProcessing: false,
		},
	}

	res := engine.Evaluate(rules, msg, "", false)
	if res.TargetFolder != "Urgent" {
		t.Errorf("expected TargetFolder 'Urgent', got %q", res.TargetFolder)
	}
	if !res.Star {
		t.Errorf("expected Star=true")
	}
}

func TestRuleEngine_MatchModeAny(t *testing.T) {
	engine := NewRuleEngine()
	msg := &Message{
		Sender:  "friend@personal.com",
		Subject: "Hello",
	}

	rule := Rule{
		Enabled:   true,
		MatchMode: "any",
		Conditions: []RuleCondition{
			{Field: FieldFrom, Operator: OpContains, Value: "work.com"},
			{Field: FieldSubject, Operator: OpContains, Value: "Hello"},
		},
		Actions: []RuleAction{{Type: ActionMarkRead}},
	}

	res := engine.Evaluate([]Rule{rule}, msg, "", false)
	if !res.MarkRead {
		t.Errorf("expected match with match_mode=any")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/mail -run TestRuleEngine_`
Expected: FAIL with undefined `NewRuleEngine`.

- [ ] **Step 3: Implement `internal/mail/rules_engine.go`**

Implement `RuleEngine.Evaluate`:
- Sort rules by `Priority` ascending.
- Check each enabled rule against message fields, parsed body, and attachment presence.
- Support string operators (case-insensitive `strings.ToLower`) and boolean comparison for attachments.
- Aggregate actions into `RuleResult`.
- Halt if `rule.StopProcessing` is true.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/mail -run TestRuleEngine_`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mail/rules_engine.go internal/mail/rules_engine_test.go
git commit -m "feat(mail): implement rule evaluation engine"
```

---

### Task 3: Delivery Integration & Service Inbox Rules

**Files:**
- Modify: `internal/mail/delivery.go`
- Modify: `internal/mail/service.go`
- Test: `internal/mail/delivery_rules_test.go`

**Interfaces:**
- Consumes: `RuleRepository` and `RuleEngine`
- Modifies: `Delivery` struct and constructor to accept `rules RuleRepository`
- Adds to `Service`:
  ```go
  func (s *Service) ApplyRulesToInbox(ctx context.Context, userID uuid.UUID) (int, error)
  ```

- [ ] **Step 1: Write integration tests for delivery rule evaluation and manual inbox execution**

Create `internal/mail/delivery_rules_test.go`:
```go
package mail

import (
	"context"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type memRuleRepo struct {
	rules []Rule
}

func (m *memRuleRepo) Create(ctx context.Context, rule *Rule) (*Rule, error) {
	m.rules = append(m.rules, *rule)
	return rule, nil
}
func (m *memRuleRepo) GetByID(ctx context.Context, userID, id uuid.UUID) (*Rule, error) { return nil, nil }
func (m *memRuleRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]Rule, error) { return m.rules, nil }
func (m *memRuleRepo) ListEnabled(ctx context.Context, userID uuid.UUID) ([]Rule, error) {
	var enabled []Rule
	for _, r := range m.rules {
		if r.Enabled && r.UserID == userID {
			enabled = append(enabled, r)
		}
	}
	return enabled, nil
}
func (m *memRuleRepo) Update(ctx context.Context, rule *Rule) (*Rule, error) { return rule, nil }
func (m *memRuleRepo) SetEnabled(ctx context.Context, userID, id uuid.UUID, enabled bool) error { return nil }
func (m *memRuleRepo) Reorder(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) error { return nil }
func (m *memRuleRepo) Delete(ctx context.Context, userID, id uuid.UUID) error { return nil }

func TestDeliveryWithRules(t *testing.T) {
	// Verify Delivery routes messages to custom mailbox and sets flags
	// based on matching rules
}

func TestServiceApplyRulesToInbox(t *testing.T) {
	// Verify ApplyRulesToInbox scans INBOX, executes rules, moves messages to target mailbox,
	// and updates message flags
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/mail -run TestDeliveryWithRules`
Expected: FAIL.

- [ ] **Step 3: Update `Delivery` in `internal/mail/delivery.go`**

- Add `rules RuleRepository` and `engine *RuleEngine` to `Delivery`.
- Update `NewDelivery(...)` or add `SetRules(repo RuleRepository)`.
- In `Deliver(ctx, recipient, message)`:
  - If `d.rules != nil`:
    - Fetch user's enabled rules via `d.rules.ListEnabled(ctx, user.ID)`.
    - Extract text snippet/body and attachment status.
    - Evaluate rules with `d.engine.Evaluate(...)`.
    - If `result.Discard`: return nil without appending (or move to Trash).
    - If `result.TargetFolder != ""`:
      - Lookup or create target mailbox via `d.mailboxes.GetByName(ctx, user.ID, result.TargetFolder)`.
      - If found, `message.MailboxID = targetBox.ID`.
      - If not found, attempt `d.mailboxes.Create(ctx, user.ID, result.TargetFolder)`. Fall back to `"INBOX"` on error.
    - If `result.MarkRead`: `message.Seen = true`.
    - If `result.Star`: `message.Flagged = true`.

- [ ] **Step 4: Implement `ApplyRulesToInbox` in `internal/mail/service.go`**

- Add `ApplyRulesToInbox(ctx context.Context, userID uuid.UUID) (int, error)`:
  - Get `"INBOX"` mailbox.
  - List all messages in `"INBOX"`.
  - Fetch enabled rules.
  - For each message:
    - Evaluate rules.
    - If target folder is not `"INBOX"`: move message to target mailbox (`s.messages.Move(ctx, msg.ID, targetBox.ID)` or update `mailbox_id`).
    - If `Seen` or `Flagged` flags changed: update message seen/flagged state.
    - Increment modified count.
  - Return count.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -v ./internal/mail -run "TestDeliveryWithRules|TestServiceApplyRulesToInbox"`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/mail/delivery.go internal/mail/service.go internal/mail/delivery_rules_test.go
git commit -m "feat(mail): wire rule engine into Delivery and add ApplyRulesToInbox"
```

---

### Task 4: Web UI Settings Page, Handlers & Compose Integration

**Files:**
- Create: `internal/web/mail_settings.go`
- Create: `web/templates/mail_settings.html`
- Modify: `internal/web/view.go`
- Modify: `internal/web/web_http.go`
- Modify: `web/templates/mail.html`
- Modify: `web/templates/profile.html`
- Modify: `web/static/js/mail.js`
- Test: `internal/web/mail_settings_test.go`

**Interfaces:**
- Consumes: `SignatureRepository`, `RuleRepository`, `mail.Service`
- Produces:
  - `GET /mail/settings`
  - `POST /mail/settings/signatures`
  - `POST /mail/settings/signatures/{id}/delete`
  - `POST /mail/settings/signatures/{id}/default`
  - `POST /mail/settings/rules`
  - `POST /mail/settings/rules/{id}/delete`
  - `POST /mail/settings/rules/{id}/toggle`
  - `POST /mail/settings/rules/reorder`
  - `POST /mail/settings/rules/apply-inbox`

- [ ] **Step 1: Write web tests for Mail Settings endpoints**

Create `internal/web/mail_settings_test.go`:
```go
package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMailSettings_Get(t *testing.T) {
	// Test GET /mail/settings renders 200 OK with Signatures and Rules tabs
}

func TestMailSettings_SignaturesCRUD(t *testing.T) {
	// Test creating, setting default, and deleting signature
}

func TestMailSettings_RulesCRUDAndApply(t *testing.T) {
	// Test creating rule, toggling enabled, and POST /mail/settings/rules/apply-inbox
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/web -run TestMailSettings_`
Expected: FAIL.

- [ ] **Step 3: Create template `web/templates/mail_settings.html`**

- Header: "Mail Settings", tabs for "Signatures" and "Rules".
- Signatures tab:
  - List of signatures with badges (`Default`), Edit/Delete buttons.
  - Signature creation form (`name`, `content`, `is_default`).
- Rules tab:
  - "Run rules on Inbox now" button.
  - Rules list with priority reorder buttons (▲ / ▼), enable/disable toggle, edit/delete.
  - Rule creation form with dynamic condition rows (`field`, `operator`, `value`) and action options (`move_to_folder`, `mark_read`, `star`, `stop_processing`).

- [ ] **Step 4: Implement handlers in `internal/web/mail_settings.go` and register routes in `internal/web/web_http.go`**

- Add `mailSettingsPage`, `signatureCreateOrUpdate`, `signatureDelete`, `signatureSetDefault`.
- Add `ruleCreateOrUpdate`, `ruleDelete`, `ruleToggle`, `ruleReorder`, `ruleApplyInbox`.
- Wire routes into `RegisterRoutes(mux *http.ServeMux)`.

- [ ] **Step 5: Update `web/templates/mail.html`, `web/templates/profile.html`, and `web/static/js/mail.js`**

- In `mail.html`: add gear icon in sidebar linking to `/mail/settings`.
- In `profile.html`: add "Mail Settings" card linking to `/mail/settings`.
- In `mail.js`:
  - When opening compose (`openCompose`), check if `#compose-body` is empty and default signature exists. If so, append `\n\n-- \n<default-signature>` and focus before signature.
  - If multiple signatures exist, enable signature selection in compose.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test -v ./internal/web -run TestMailSettings_`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/web/mail_settings* web/templates/mail_settings.html web/templates/mail.html web/templates/profile.html web/static/js/mail.js
git commit -m "feat(web): add mail settings page, signatures editor, rules editor, and compose integration"
```

---

### Task 5: System Wiring, End-to-End Verification & Quality Gate

**Files:**
- Modify: `main.go`
- Test: `tests/integration/mail_settings_test.go`

**Interfaces:**
- Connects `postgres.NewSignatureRepository(db)` and `postgres.NewRuleRepository(db)` to `mail.Delivery`, `mail.Service`, and `web.Server`.

- [ ] **Step 1: Write integration test for end-to-end flow**

Create `tests/integration/mail_settings_test.go`:
- Test that creating a rule to move emails from a specific sender to "Archive" automatically moves an incoming message during delivery to "Archive" and marks it read.
- Test that manual inbox rule execution moves pre-existing messages in INBOX.
- Test that signatures are persisted and returned in settings.

- [ ] **Step 2: Run integration test to verify it fails**

Run: `go test -v ./tests/integration -run TestMailSettingsIntegration`
Expected: FAIL.

- [ ] **Step 3: Wire components in `main.go`**

- Initialize `sigRepo := postgres.NewSignatureRepository(db)`.
- Initialize `ruleRepo := postgres.NewRuleRepository(db)`.
- Pass `ruleRepo` to `mailDelivery`.
- Provide `sigRepo` and `ruleRepo` to `webUI` via setters or constructor.

- [ ] **Step 4: Run integration test to verify it passes**

Run: `go test -v ./tests/integration -run TestMailSettingsIntegration`
Expected: PASS

- [ ] **Step 5: Verify entire workspace suite**

Run:
```bash
go test -count=1 ./...
go build ./...
```
Expected: PASS across all packages, clean binary build.

- [ ] **Step 6: Commit**

```bash
git add main.go tests/integration/mail_settings_test.go
git commit -m "feat: wire mail signatures and rules into server and add integration tests"
```
