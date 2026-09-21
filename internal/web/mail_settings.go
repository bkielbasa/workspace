package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

// mailSettingsPage renders the Mail Settings view
func (v *views) mailSettingsPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	tab := strings.TrimSpace(r.URL.Query().Get("tab"))
	if tab == "" {
		tab = "signatures"
	}

	var sigs []mail.Signature
	var err error
	if v.signatures != nil {
		sigs, err = v.signatures.ListByUser(r.Context(), user.ID)
		if err != nil {
			obs.Log(r.Context(), slog.LevelError, "failed to list signatures", "error", err)
		}
	}

	var rules []mail.Rule
	if v.rules != nil {
		rules, err = v.rules.ListByUser(r.Context(), user.ID)
		if err != nil {
			obs.Log(r.Context(), slog.LevelError, "failed to list rules", "error", err)
		}
	}

	// Fetch mailboxes so user can pick target for move_to_folder rule
	boxes, err := v.mail.ListMailboxes(r.Context(), user.ID)
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "failed to list mailboxes", "error", err)
	}

	data := viewData{
		Title:      "Mail Settings",
		Section:    "mail", // Keep mail section active in navigation
		User:       user,
		CSRFToken:  csrfTokenFromRequest(r),
		Tab:        tab,
		Signatures: sigs,
		Rules:      rules,
		Mailboxes:  boxes,
		Success:    r.URL.Query().Get("success"),
		Error:      r.URL.Query().Get("error"),
	}

	renderView(w, r, v.mailSettingsT, "layout", data)
}

// signatureSave handles both creating and updating email signatures
func (v *views) signatureSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if v.signatures == nil {
		http.Redirect(w, r, "/mail/settings?tab=signatures&error=Signatures+not+configured", http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(r.PostFormValue("name"))
	content := strings.TrimSpace(r.PostFormValue("content"))
	isDefault := r.PostFormValue("is_default") == "on" || r.PostFormValue("is_default") == "true"

	if name == "" {
		http.Redirect(w, r, "/mail/settings?tab=signatures&error=Name+cannot+be+empty", http.StatusSeeOther)
		return
	}

	idStr := r.PostFormValue("id")
	if idStr != "" {
		id, err := uuid.Parse(idStr)
		if err != nil {
			http.Redirect(w, r, "/mail/settings?tab=signatures&error=Invalid+ID", http.StatusSeeOther)
			return
		}

		sig, err := v.signatures.GetByID(ctx, user.ID, id)
		if err != nil {
			http.Redirect(w, r, "/mail/settings?tab=signatures&error=Signature+not+found", http.StatusSeeOther)
			return
		}

		sig.Name = name
		sig.Content = content
		sig.IsDefault = isDefault

		if err := v.signatures.Update(ctx, sig); err != nil {
			obs.Log(ctx, slog.LevelError, "failed to update signature", "error", err)
			http.Redirect(w, r, "/mail/settings?tab=signatures&error=Failed+to+save+signature", http.StatusSeeOther)
			return
		}
	} else {
		sig := &mail.Signature{
			UserID:    user.ID,
			Name:      name,
			Content:   content,
			IsDefault: isDefault,
		}

		if err := v.signatures.Create(ctx, sig); err != nil {
			obs.Log(ctx, slog.LevelError, "failed to create signature", "error", err)
			http.Redirect(w, r, "/mail/settings?tab=signatures&error=Failed+to+create+signature", http.StatusSeeOther)
			return
		}
	}

	http.Redirect(w, r, "/mail/settings?tab=signatures&success=Signature+saved+successfully", http.StatusSeeOther)
}

// signatureDelete handles deleting a signature
func (v *views) signatureDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if v.signatures == nil {
		http.Redirect(w, r, "/mail/settings?tab=signatures&error=Signatures+not+configured", http.StatusSeeOther)
		return
	}

	id, err := parseUUIDPath(r)
	if err != nil {
		http.Redirect(w, r, "/mail/settings?tab=signatures&error=Invalid+ID", http.StatusSeeOther)
		return
	}

	if err := v.signatures.Delete(ctx, user.ID, id); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to delete signature", "error", err)
		http.Redirect(w, r, "/mail/settings?tab=signatures&error=Failed+to+delete+signature", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/mail/settings?tab=signatures&success=Signature+deleted+successfully", http.StatusSeeOther)
}

// signatureSetDefault sets a signature as the default
func (v *views) signatureSetDefault(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if v.signatures == nil {
		http.Redirect(w, r, "/mail/settings?tab=signatures&error=Signatures+not+configured", http.StatusSeeOther)
		return
	}

	id, err := parseUUIDPath(r)
	if err != nil {
		http.Redirect(w, r, "/mail/settings?tab=signatures&error=Invalid+ID", http.StatusSeeOther)
		return
	}

	if err := v.signatures.SetDefault(ctx, user.ID, id); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to set default signature", "error", err)
		http.Redirect(w, r, "/mail/settings?tab=signatures&error=Failed+to+set+default+signature", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/mail/settings?tab=signatures&success=Default+signature+updated", http.StatusSeeOther)
}

// ruleSave handles creating incoming mail rules
func (v *views) ruleSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if v.rules == nil {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Rules+not+configured", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Invalid+form+data", http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(r.PostFormValue("name"))
	matchMode := strings.TrimSpace(r.PostFormValue("match_mode"))
	if matchMode == "" {
		matchMode = "all"
	}

	if name == "" {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Name+cannot+be+empty", http.StatusSeeOther)
		return
	}

	// Build conditions
	var conds []mail.RuleCondition
	condFields := r.PostForm["cond_field[]"]
	condOps := r.PostForm["cond_op[]"]
	condVals := r.PostForm["cond_val[]"]

	for i := 0; i < len(condFields); i++ {
		if i < len(condOps) && i < len(condVals) {
			field := strings.TrimSpace(condFields[i])
			op := strings.TrimSpace(condOps[i])
			val := strings.TrimSpace(condVals[i])

			if field != "" && op != "" {
				conds = append(conds, mail.RuleCondition{
					Field:    mail.RuleField(field),
					Operator: mail.RuleOperator(op),
					Value:    val,
				})
			}
		}
	}

	if len(conds) == 0 {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=At+least+one+condition+is+required", http.StatusSeeOther)
		return
	}

	// Build actions
	var acts []mail.RuleAction
	actionTypes := r.PostForm["action_type[]"]
	actionTargets := r.PostForm["action_target[]"]

	for i := 0; i < len(actionTypes); i++ {
		actType := strings.TrimSpace(actionTypes[i])
		if actType != "" {
			target := ""
			if i < len(actionTargets) {
				target = strings.TrimSpace(actionTargets[i])
			}
			acts = append(acts, mail.RuleAction{
				Type:   mail.RuleActionType(actType),
				Target: target,
			})
		}
	}

	if len(acts) == 0 {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=At+least+one+action+is+required", http.StatusSeeOther)
		return
	}

	stopProcessing := r.PostFormValue("stop_processing") == "on" || r.PostFormValue("stop_processing") == "true"

	priority := 1
	if existingRules, err := v.rules.ListByUser(ctx, user.ID); err == nil {
		maxPriority := 0
		for _, r := range existingRules {
			if r.Priority > maxPriority {
				maxPriority = r.Priority
			}
		}
		priority = maxPriority + 1
	}

	rule := &mail.Rule{
		UserID:         user.ID,
		Name:           name,
		Enabled:        true,
		MatchMode:      matchMode,
		Conditions:     conds,
		Actions:        acts,
		StopProcessing: stopProcessing,
		Priority:       priority,
	}

	if err := v.rules.Create(ctx, rule); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to create rule", "error", err)
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Failed+to+create+rule", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/mail/settings?tab=rules&success=Rule+created+successfully", http.StatusSeeOther)
}

// ruleDelete handles deleting a rule
func (v *views) ruleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if v.rules == nil {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Rules+not+configured", http.StatusSeeOther)
		return
	}

	id, err := parseUUIDPath(r)
	if err != nil {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Invalid+ID", http.StatusSeeOther)
		return
	}

	if err := v.rules.Delete(ctx, user.ID, id); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to delete rule", "error", err)
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Failed+to+delete+rule", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/mail/settings?tab=rules&success=Rule+deleted+successfully", http.StatusSeeOther)
}

// ruleToggle enables/disables a rule
func (v *views) ruleToggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if v.rules == nil {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Rules+not+configured", http.StatusSeeOther)
		return
	}

	id, err := parseUUIDPath(r)
	if err != nil {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Invalid+ID", http.StatusSeeOther)
		return
	}

	enabled := r.PostFormValue("enabled") == "true" || r.PostFormValue("enabled") == "on"

	if err := v.rules.SetEnabled(ctx, user.ID, id, enabled); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to toggle rule", "error", err)
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Failed+to+toggle+rule", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/mail/settings?tab=rules&success=Rule+status+updated", http.StatusSeeOther)
}

// ruleReorder handles reordering rule priorities up/down
func (v *views) ruleReorder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if v.rules == nil {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Rules+not+configured", http.StatusSeeOther)
		return
	}

	idStr := r.PostFormValue("id")
	direction := r.PostFormValue("direction") // "up" or "down"

	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Invalid+ID", http.StatusSeeOther)
		return
	}

	rules, err := v.rules.ListByUser(ctx, user.ID)
	if err != nil {
		obs.Log(ctx, slog.LevelError, "failed to list rules for reorder", "error", err)
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Failed+to+load+rules", http.StatusSeeOther)
		return
	}

	idx := -1
	for i, r := range rules {
		if r.ID == id {
			idx = i
			break
		}
	}

	if idx == -1 {
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Rule+not+found", http.StatusSeeOther)
		return
	}

	if direction == "up" && idx > 0 {
		rules[idx], rules[idx-1] = rules[idx-1], rules[idx]
	} else if direction == "down" && idx < len(rules)-1 {
		rules[idx], rules[idx+1] = rules[idx+1], rules[idx]
	} else {
		// No shift occurred
		http.Redirect(w, r, "/mail/settings?tab=rules", http.StatusSeeOther)
		return
	}

	orderedIDs := make([]uuid.UUID, len(rules))
	for i, r := range rules {
		orderedIDs[i] = r.ID
	}

	if err := v.rules.Reorder(ctx, user.ID, orderedIDs); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to reorder rules", "error", err)
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Failed+to+reorder+rules", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/mail/settings?tab=rules&success=Rules+reordered", http.StatusSeeOther)
}

// ruleApplyInbox triggers RuleEngine manual execution on current INBOX
func (v *views) ruleApplyInbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	affectedCount, err := v.mail.ApplyRulesToInbox(ctx, user.ID)
	if err != nil {
		obs.Log(ctx, slog.LevelError, "failed to apply rules to inbox", "error", err)
		http.Redirect(w, r, "/mail/settings?tab=rules&error=Failed+to+apply+rules+to+inbox", http.StatusSeeOther)
		return
	}

	successMsg := fmt.Sprintf("Inbox rules applied successfully. Affected %d messages.", affectedCount)
	http.Redirect(w, r, "/mail/settings?tab=rules&success="+url.QueryEscape(successMsg), http.StatusSeeOther)
}
