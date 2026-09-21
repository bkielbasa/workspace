# Mail Settings Signatures and Rules Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the single fix wave for Whole-Branch Code Review findings in the `mail-settings-signatures-and-rules` branch.

**Architecture:** Address UI/backend mismatches, add missing features in HTML template and backend evaluation/sanitization, implement pagination in retroactive rules application, and resolve a negation evaluation logic flaw with proper unit and integration testing.

**Tech Stack:** Go (Golang), HTML, Go templates, JavaScript.

**Spec:** Inside the instruction request prompt.

## Global Constraints
- Do not introduce unrelated modifications.
- Preserve all existing comments and docstrings.
- Ensure all tests pass with zero errors across all packages.

---

### Task 1: UI/Backend Enum Mismatch & Missing Features in Web UI

**Files:**
- Modify: `web/templates/mail_settings.html:230-280`

**Interfaces:**
- Consumes: None
- Produces: Updated DOM options for rule builder in web UI.

- [ ] **Step 1: Edit HTML Template**
  Update `web/templates/mail_settings.html` to:
  1. Replace `<option value="mark_flagged">Mark as Starred (Flagged)</option>` with `<option value="star">Star message</option>` inside the `action_type[]` select.
  2. Add `<option value="has_attachment">Has attachments</option>` under `cond_field[]` select.
  3. Add `<option value="is">Is</option>` and `<option value="not_equals">Does Not Equal</option>` under `cond_op[]` select.
  4. Add `<option value="delete">Discard (Delete immediately)</option>` under `action_type[]` select.

- [ ] **Step 2: Commit changes**
  ```bash
  git add web/templates/mail_settings.html
  git commit -m "fix: update web UI options for mail rules to match backend and support missing features"
  ```

---

### Task 2: Action Target Sanitization in Backend

**Files:**
- Modify: `internal/web/mail_settings.go:245-260`

**Interfaces:**
- Consumes: `mail.RuleActionMoveToFolder` from `internal/mail/rules.go`
- Produces: Sanitized target for non-move rule actions.

- [ ] **Step 1: Add Sanitization in ruleSave**
  In `internal/web/mail_settings.go`, inside the action loop of `ruleSave`, if the rule action type is not `mail.RuleActionMoveToFolder`, force `target` to empty string `""`.

- [ ] **Step 2: Commit changes**
  ```bash
  git add internal/web/mail_settings.go
  git commit -m "fix: sanitize target field in ruleSave for non-move actions"
  ```

---

### Task 3: Multi-Valued Negation Logic Flaw Fix & Unit Test

**Files:**
- Modify: `internal/mail/rules_engine.go:152-163`
- Modify: `internal/mail/rules_engine_test.go`

**Interfaces:**
- Consumes: Evaluator logic inside `evaluateCondition`
- Produces: Correct evaluation for negated operators with empty values list.

- [ ] **Step 1: Fix logic flaw in evaluateCondition**
  In `internal/mail/rules_engine.go`, update the check for `isNegated` such that if `len(values) == 0`, it returns `true`.

- [ ] **Step 2: Write unit test**
  Add unit test in `internal/mail/rules_engine_test.go` covering an empty recipient list with `not_contains` evaluating to `true`.

- [ ] **Step 3: Run rules engine tests**
  ```bash
  go test -count=1 -run TestEvaluateCondition_NegatedEmptyValues ./internal/mail/...
  ```

- [ ] **Step 4: Commit changes**
  ```bash
  git add internal/mail/rules_engine.go internal/mail/rules_engine_test.go
  git commit -m "fix: correct negated evaluation logic for empty values list and add test case"
  ```

---

### Task 4: Pagination and Discard Action in Retroactive Application & Test

**Files:**
- Modify: `internal/mail/service.go:63-128`
- Modify: `internal/mail/delivery_rules_test.go`

**Interfaces:**
- Consumes: `s.messages.List` and `s.messages.Delete` in `internal/mail/service.go`
- Produces: Paginated retrieval and delete operation on discard during ApplyRulesToInbox.

- [ ] **Step 1: Implement pagination and Discard action in ApplyRulesToInbox**
  In `internal/mail/service.go`, modify `ApplyRulesToInbox` to:
  1. Retrieve messages using pagination in a loop, chunked by size 500, until the returned batch is empty or has a size less than 500.
  2. Implement the `Discard` action: if `res.Discard` is true, delete the message via `s.messages.Delete(ctx, m.ID)` (log warning on error), increment `affectedCount++`, and `continue` to the next message.

- [ ] **Step 2: Write integration/unit test**
  Add test in `internal/mail/delivery_rules_test.go` covering `ApplyRulesToInbox` with a rule that has `Discard` action, verifying that the message is deleted.

- [ ] **Step 3: Run delivery rules tests**
  ```bash
  go test -count=1 -run TestApplyRulesToInbox_Discard ./internal/mail/...
  ```

- [ ] **Step 4: Commit changes**
  ```bash
  git add internal/mail/service.go internal/mail/delivery_rules_test.go
  git commit -m "fix: implement pagination and discard action in retroactive rules application with test"
  ```
