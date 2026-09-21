package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bklimczak/workspace/internal/appleprofile"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

// settingsPage renders the settings view.
func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	section := strings.TrimSpace(r.URL.Query().Get("section"))
	if section == "" {
		section = strings.TrimSpace(r.URL.Query().Get("tab"))
	}
	if section == "" {
		section = "profile"
	}

	successVal := r.URL.Query().Get("success")
	var successMsg string
	switch successVal {
	case "password":
		successMsg = "Your password has been changed successfully."
	case "profile":
		successMsg = "Your profile details have been updated."
	case "invite":
		successMsg = "Welcome! Your account is ready — grab the iPhone profile below for one-tap mail setup."
	case "rules_applied":
		successMsg = "Inbox rules applied successfully."
	default:
		successMsg = successVal
	}

	errorMsg := r.URL.Query().Get("error")

	s.renderSettings(w, r, user, section, errorMsg, successMsg, "", "", "", "")
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, user *identity.User, tab, errMsg, successMsg, newAppPassword, newAppPasswordName, inviteLink, inviteEmail string) {
	if fresh, err := s.users.Get(r.Context(), user.ID); err == nil && fresh != nil {
		user = fresh
	}

	var sigs []mail.Signature
	var err error
	if s.views.signatures != nil {
		sigs, err = s.views.signatures.ListByUser(r.Context(), user.ID)
		if err != nil {
			obs.Log(r.Context(), slog.LevelError, "failed to list signatures", "error", err)
		}
	}

	var rules []mail.Rule
	if s.views.rules != nil {
		rules, err = s.views.rules.ListByUser(r.Context(), user.ID)
		if err != nil {
			obs.Log(r.Context(), slog.LevelError, "failed to list rules", "error", err)
		}
	}

	var boxes []mail.MailboxInfo
	if s.mail != nil {
		boxes, err = s.mail.ListMailboxes(r.Context(), user.ID)
		if err != nil {
			obs.Log(r.Context(), slog.LevelError, "failed to list mailboxes", "error", err)
		}
	}

	var passwords []identity.AppPassword
	if s.appPasswords != nil {
		if list, err := s.appPasswords.List(r.Context(), user.ID); err == nil {
			passwords = list
		}
	}

	var adminUsers []identity.User
	var invites []identity.Invite
	if user.IsAdmin {
		if s.invites != nil {
			if list, err := s.invites.List(r.Context()); err == nil {
				invites = list
			}
		}
		if list, err := s.users.List(r.Context()); err == nil {
			adminUsers = list
		}
	}

	data := viewData{
		Title:              "Settings",
		Section:            "settings",
		User:               user,
		CSRFToken:          csrfTokenFromRequest(r),
		Tab:                tab,
		Signatures:         sigs,
		Rules:              rules,
		Mailboxes:          boxes,
		AppPasswords:       passwords,
		DevicesReady:       s.appPasswords != nil,
		AdminUsers:         adminUsers,
		Invites:            invites,
		PrimaryDomain:      s.primaryDomain,
		Success:            successMsg,
		Error:              errMsg,
		NewAppPassword:     newAppPassword,
		NewAppPasswordName: newAppPasswordName,
		InviteLink:         inviteLink,
		InviteEmail:        inviteEmail,
	}

	renderView(w, r, s.views.settingsT, "layout", data)
}

// settingsProfile updates display name
func (s *Server) settingsProfile(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderSettings(w, r, user, "profile", "Invalid form submission.", "", "", "", "", "")
		return
	}

	displayName := strings.TrimSpace(r.FormValue("display_name"))
	if err := s.users.Update(r.Context(), user.ID, displayName, user.Enabled); err != nil {
		obs.Log(r.Context(), slog.LevelError, "failed to update profile", "user_id", user.ID, "error", err)
		s.renderSettings(w, r, user, "profile", "Could not update profile details.", "", "", "", "", "")
		return
	}

	http.Redirect(w, r, "/settings?section=profile&success=profile", http.StatusSeeOther)
}

// settingsPassword changes password
func (s *Server) settingsPassword(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderSettings(w, r, user, "password", "Invalid form submission.", "", "", "", "", "")
		return
	}

	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirmPassword := r.FormValue("confirm_password")

	if currentPassword == "" {
		s.renderSettings(w, r, user, "password", "Current password is required.", "", "", "", "", "")
		return
	}
	if newPassword == "" {
		s.renderSettings(w, r, user, "password", "New password is required.", "", "", "", "", "")
		return
	}
	if len(newPassword) < 8 {
		s.renderSettings(w, r, user, "password", "New password must be at least 8 characters long.", "", "", "", "", "")
		return
	}
	if newPassword != confirmPassword {
		s.renderSettings(w, r, user, "password", "New passwords do not match.", "", "", "", "", "")
		return
	}

	// Verify current password against credentials
	if _, err := s.users.Authenticate(r.Context(), user.Email, currentPassword); err != nil {
		s.renderSettings(w, r, user, "password", "Current password is incorrect.", "", "", "", "", "")
		return
	}

	// Change password in store
	if err := s.users.ChangePassword(r.Context(), user.ID, newPassword); err != nil {
		obs.Log(r.Context(), slog.LevelError, "failed to change password", "user_id", user.ID, "error", err)
		s.renderSettings(w, r, user, "password", "Could not change password. Please try again.", "", "", "", "", "")
		return
	}

	// Re-establish session since changing password revokes existing sessions
	session, err := s.sessions.Create(r.Context(), user.ID, sessionTTL)
	if err == nil && session != nil {
		s.setSessionCookie(w, r, session.Token)
	}

	http.Redirect(w, r, "/settings?section=password&success=password", http.StatusSeeOther)
}

// settingsSignatureCreateOrUpdate handles both creating and updating email signatures
func (s *Server) settingsSignatureCreateOrUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if s.views.signatures == nil {
		http.Redirect(w, r, "/settings?section=signatures&error=Signatures+not+configured", http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(r.PostFormValue("name"))
	content := strings.TrimSpace(r.PostFormValue("content"))
	isDefault := r.PostFormValue("is_default") == "on" || r.PostFormValue("is_default") == "true"

	if name == "" {
		http.Redirect(w, r, "/settings?section=signatures&error=Name+cannot+be+empty", http.StatusSeeOther)
		return
	}

	idStr := r.PostFormValue("id")
	if idStr != "" {
		id, err := uuid.Parse(idStr)
		if err != nil {
			http.Redirect(w, r, "/settings?section=signatures&error=Invalid+ID", http.StatusSeeOther)
			return
		}

		sig, err := s.views.signatures.GetByID(ctx, user.ID, id)
		if err != nil {
			http.Redirect(w, r, "/settings?section=signatures&error=Signature+not+found", http.StatusSeeOther)
			return
		}

		sig.Name = name
		sig.Content = content
		sig.IsDefault = isDefault

		if err := s.views.signatures.Update(ctx, sig); err != nil {
			obs.Log(ctx, slog.LevelError, "failed to update signature", "error", err)
			http.Redirect(w, r, "/settings?section=signatures&error=Failed+to+save+signature", http.StatusSeeOther)
			return
		}
	} else {
		sig := &mail.Signature{
			UserID:    user.ID,
			Name:      name,
			Content:   content,
			IsDefault: isDefault,
		}

		if err := s.views.signatures.Create(ctx, sig); err != nil {
			obs.Log(ctx, slog.LevelError, "failed to create signature", "error", err)
			http.Redirect(w, r, "/settings?section=signatures&error=Failed+to+create+signature", http.StatusSeeOther)
			return
		}
	}

	http.Redirect(w, r, "/settings?section=signatures&success=Signature+saved+successfully", http.StatusSeeOther)
}

// settingsSignatureSetDefault sets a signature as the default
func (s *Server) settingsSignatureSetDefault(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if s.views.signatures == nil {
		http.Redirect(w, r, "/settings?section=signatures&error=Signatures+not+configured", http.StatusSeeOther)
		return
	}

	id, err := parseUUIDPath(r)
	if err != nil {
		http.Redirect(w, r, "/settings?section=signatures&error=Invalid+ID", http.StatusSeeOther)
		return
	}

	if err := s.views.signatures.SetDefault(ctx, user.ID, id); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to set default signature", "error", err)
		http.Redirect(w, r, "/settings?section=signatures&error=Failed+to+set+default+signature", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/settings?section=signatures&success=Default+signature+updated", http.StatusSeeOther)
}

// settingsSignatureDelete handles deleting a signature
func (s *Server) settingsSignatureDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if s.views.signatures == nil {
		http.Redirect(w, r, "/settings?section=signatures&error=Signatures+not+configured", http.StatusSeeOther)
		return
	}

	id, err := parseUUIDPath(r)
	if err != nil {
		http.Redirect(w, r, "/settings?section=signatures&error=Invalid+ID", http.StatusSeeOther)
		return
	}

	if err := s.views.signatures.Delete(ctx, user.ID, id); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to delete signature", "error", err)
		http.Redirect(w, r, "/settings?section=signatures&error=Failed+to+delete+signature", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/settings?section=signatures&success=Signature+deleted+successfully", http.StatusSeeOther)
}

// settingsRuleCreate handles creating incoming mail rules
func (s *Server) settingsRuleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if s.views.rules == nil {
		http.Redirect(w, r, "/settings?section=rules&error=Rules+not+configured", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/settings?section=rules&error=Invalid+form+data", http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(r.PostFormValue("name"))
	matchMode := strings.TrimSpace(r.PostFormValue("match_mode"))
	if matchMode == "" {
		matchMode = "all"
	}

	if name == "" {
		http.Redirect(w, r, "/settings?section=rules&error=Name+cannot+be+empty", http.StatusSeeOther)
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
		http.Redirect(w, r, "/settings?section=rules&error=At+least+one+condition+is+required", http.StatusSeeOther)
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
			if mail.RuleActionType(actType) != mail.RuleActionMoveToFolder {
				target = ""
			}
			acts = append(acts, mail.RuleAction{
				Type:   mail.RuleActionType(actType),
				Target: target,
			})
		}
	}

	if len(acts) == 0 {
		http.Redirect(w, r, "/settings?section=rules&error=At+least+one+action+is+required", http.StatusSeeOther)
		return
	}

	stopProcessing := r.PostFormValue("stop_processing") == "on" || r.PostFormValue("stop_processing") == "true"

	priority := 1
	if existingRules, err := s.views.rules.ListByUser(ctx, user.ID); err == nil {
		maxPriority := 0
		for _, er := range existingRules {
			if er.Priority > maxPriority {
				maxPriority = er.Priority
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

	if err := s.views.rules.Create(ctx, rule); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to create rule", "error", err)
		http.Redirect(w, r, "/settings?section=rules&error=Failed+to+create+rule", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/settings?section=rules&success=Rule+created+successfully", http.StatusSeeOther)
}

// settingsRuleToggle enables/disables a rule
func (s *Server) settingsRuleToggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if s.views.rules == nil {
		http.Redirect(w, r, "/settings?section=rules&error=Rules+not+configured", http.StatusSeeOther)
		return
	}

	id, err := parseUUIDPath(r)
	if err != nil {
		http.Redirect(w, r, "/settings?section=rules&error=Invalid+ID", http.StatusSeeOther)
		return
	}

	enabled := r.PostFormValue("enabled") == "true" || r.PostFormValue("enabled") == "on"

	if err := s.views.rules.SetEnabled(ctx, user.ID, id, enabled); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to toggle rule", "error", err)
		http.Redirect(w, r, "/settings?section=rules&error=Failed+to+toggle+rule", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/settings?section=rules&success=Rule+status+updated", http.StatusSeeOther)
}

// settingsRuleDelete handles deleting a rule
func (s *Server) settingsRuleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if s.views.rules == nil {
		http.Redirect(w, r, "/settings?section=rules&error=Rules+not+configured", http.StatusSeeOther)
		return
	}

	id, err := parseUUIDPath(r)
	if err != nil {
		http.Redirect(w, r, "/settings?section=rules&error=Invalid+ID", http.StatusSeeOther)
		return
	}

	if err := s.views.rules.Delete(ctx, user.ID, id); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to delete rule", "error", err)
		http.Redirect(w, r, "/settings?section=rules&error=Failed+to+delete+rule", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/settings?section=rules&success=Rule+deleted+successfully", http.StatusSeeOther)
}

// settingsRuleReorder handles reordering rule priorities up/down
func (s *Server) settingsRuleReorder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if s.views.rules == nil {
		http.Redirect(w, r, "/settings?section=rules&error=Rules+not+configured", http.StatusSeeOther)
		return
	}

	idStr := r.PostFormValue("id")
	direction := r.PostFormValue("direction") // "up" or "down"

	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Redirect(w, r, "/settings?section=rules&error=Invalid+ID", http.StatusSeeOther)
		return
	}

	rules, err := s.views.rules.ListByUser(ctx, user.ID)
	if err != nil {
		obs.Log(ctx, slog.LevelError, "failed to list rules for reorder", "error", err)
		http.Redirect(w, r, "/settings?section=rules&error=Failed+to+load+rules", http.StatusSeeOther)
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
		http.Redirect(w, r, "/settings?section=rules&error=Rule+not+found", http.StatusSeeOther)
		return
	}

	if direction == "up" && idx > 0 {
		rules[idx], rules[idx-1] = rules[idx-1], rules[idx]
	} else if direction == "down" && idx < len(rules)-1 {
		rules[idx], rules[idx+1] = rules[idx+1], rules[idx]
	} else {
		// No shift occurred
		http.Redirect(w, r, "/settings?section=rules", http.StatusSeeOther)
		return
	}

	orderedIDs := make([]uuid.UUID, len(rules))
	for i, r := range rules {
		orderedIDs[i] = r.ID
	}

	if err := s.views.rules.Reorder(ctx, user.ID, orderedIDs); err != nil {
		obs.Log(ctx, slog.LevelError, "failed to reorder rules", "error", err)
		http.Redirect(w, r, "/settings?section=rules&error=Failed+to+reorder+rules", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/settings?section=rules&success=Rules+reordered", http.StatusSeeOther)
}

// settingsRuleApplyInbox triggers RuleEngine manual execution on current INBOX
func (s *Server) settingsRuleApplyInbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	affectedCount, err := s.mail.ApplyRulesToInbox(ctx, user.ID)
	if err != nil {
		obs.Log(ctx, slog.LevelError, "failed to apply rules to inbox", "error", err)
		http.Redirect(w, r, "/settings?section=rules&error=Failed+to+apply+rules+to+inbox", http.StatusSeeOther)
		return
	}

	successMsg := fmt.Sprintf("Inbox rules applied successfully. Affected %d messages.", affectedCount)
	http.Redirect(w, r, "/settings?section=rules&success="+url.QueryEscape(successMsg), http.StatusSeeOther)
}

// settingsIPhoneProfile rotates the named app password and serves a mobileconfig
func (s *Server) settingsIPhoneProfile(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.appPasswords == nil {
		s.renderSettings(w, r, user, "iphone", "Device setup is not configured.", "", "", "", "", "")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	plain, _, err := s.appPasswords.Rotate(r.Context(), user.ID, name)
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "rotate app password failed", "user_id", user.ID, "error", err)
		s.renderSettings(w, r, user, "iphone", "Could not generate the iPhone profile.", "", "", "", "", "")
		return
	}
	profile, err := appleprofile.Build(user.Email, s.mailHost, s.davHost, requestHost(r), plain)
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "build iphone profile failed", "user_id", user.ID, "error", err)
		s.renderSettings(w, r, user, "iphone", "Could not generate the iPhone profile.", "", "", "", "", "")
		return
	}

	_, domain, _ := strings.Cut(user.Email, "@")
	token := iphoneDownloadTokens.put(profile, domain)
	http.Redirect(w, r, "/settings/iphone-profile/download?token="+token, http.StatusSeeOther)
}

// iphoneDownloadTokens holds freshly generated profiles for a few minutes so
// iOS can fetch them with a plain GET.
var iphoneDownloadTokens = &iphoneTokenStore{tokens: map[string]iphoneToken{}}

type iphoneToken struct {
	profile []byte
	domain  string
	expires time.Time
}

type iphoneTokenStore struct {
	mu     sync.Mutex
	tokens map[string]iphoneToken
}

func (st *iphoneTokenStore) put(profile []byte, domain string) string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	token := hex.EncodeToString(raw[:])
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	for t, e := range st.tokens {
		if now.After(e.expires) {
			delete(st.tokens, t)
		}
	}
	st.tokens[token] = iphoneToken{profile: profile, domain: domain, expires: now.Add(5 * time.Minute)}
	return token
}

func (st *iphoneTokenStore) get(token string) ([]byte, string, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	e, ok := st.tokens[token]
	if !ok || time.Now().After(e.expires) {
		delete(st.tokens, token)
		return nil, "", false
	}
	return e.profile, e.domain, true
}

// settingsIPhoneProfileDownload serves a generated profile to a plain GET.
func (s *Server) settingsIPhoneProfileDownload(w http.ResponseWriter, r *http.Request) {
	profile, domain, ok := iphoneDownloadTokens.get(strings.TrimSpace(r.URL.Query().Get("token")))
	if !ok {
		http.Error(w, "this download link expired — generate the profile again", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", appleprofile.ContentType)
	if !strings.Contains(strings.ToLower(r.UserAgent()), "iphone") && !strings.Contains(strings.ToLower(r.UserAgent()), "ipad") {
		w.Header().Set("Content-Disposition", `attachment; filename="`+domain+`-iphone.mobileconfig"`)
	}
	if _, err := w.Write(profile); err != nil {
		obs.Log(r.Context(), slog.LevelError, "write iphone profile download failed", "error", err)
	}
}

// settingsAppPasswordCreate mints a standalone app password and shows the plaintext once
func (s *Server) settingsAppPasswordCreate(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.appPasswords == nil {
		s.renderSettings(w, r, user, "app-passwords", "Device setup is not configured.", "", "", "", "", "")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "Device"
	}
	plain, _, err := s.appPasswords.Rotate(r.Context(), user.ID, name)
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "create app password failed", "user_id", user.ID, "error", err)
		s.renderSettings(w, r, user, "app-passwords", "Could not create the app password.", "", "", "", "", "")
		return
	}
	s.renderSettings(w, r, user, "app-passwords", "", "", plain, name, "", "")
}

// settingsAppPasswordRevoke deletes one app password
func (s *Server) settingsAppPasswordRevoke(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.appPasswords == nil {
		http.Redirect(w, r, "/settings?section=app-passwords", http.StatusSeeOther)
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(r.FormValue("id")))
	if err != nil {
		s.renderSettings(w, r, user, "app-passwords", "Invalid app password.", "", "", "", "", "")
		return
	}
	if err := s.appPasswords.Revoke(r.Context(), user.ID, id); err != nil {
		obs.Log(r.Context(), slog.LevelError, "revoke app password failed", "user_id", user.ID, "error", err)
		s.renderSettings(w, r, user, "app-passwords", "Could not revoke the app password.", "", "", "", "", "")
		return
	}
	http.Redirect(w, r, "/settings?section=app-passwords", http.StatusSeeOther)
}

// requestHost returns the request host without any port.
func requestHost(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return "localhost"
	}
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		return h
	}
	return host
}

func (s *Server) requireAdmin(user *identity.User) bool {
	return user != nil && user.IsAdmin && s.invites != nil
}

// settingsInviteCreate provisions a disabled account plus a one-time token, emails the accept link
func (s *Server) settingsInviteCreate(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !s.requireAdmin(user) {
		s.renderSettings(w, r, user, "invites", "Admins only.", "", "", "", "", "")
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	if !strings.Contains(email, "@") {
		s.renderSettings(w, r, user, "invites", "Enter a valid email address.", "", "", "", "", "")
		return
	}
	token, invite, err := s.invites.CreateInvite(r.Context(), email, displayName)
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "create invite failed", "error", err)
		s.renderSettings(w, r, user, "invites", "Could not create the invite: "+err.Error(), "", "", "", "", "")
		return
	}
	link := "https://" + requestHost(r) + "/invite/accept?token=" + token
	if s.views.mail != nil {
		body := "Hi " + invite.Email + ",\n\nYou've been invited to " + requestHost(r) +
			". Open this link within 7 days to set your password and activate your account:\n\n" +
			link + "\n\nAfter that, grab the iPhone profile from your account page for one-tap mail setup."
		if _, err := s.views.mail.SendMessage(r.Context(), user, invite.Email, "You're invited to "+requestHost(r), body); err != nil {
			obs.Log(r.Context(), slog.LevelWarn, "invite email failed", "error", err)
		}
	}
	s.renderSettings(w, r, user, "invites", "", "Invite created for "+email+".", "", "", link, email)
}

// settingsInviteDelete revokes an invite
func (s *Server) settingsInviteDelete(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !s.requireAdmin(user) {
		s.renderSettings(w, r, user, "invites", "Admins only.", "", "", "", "", "")
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(r.FormValue("id")))
	if err != nil {
		s.renderSettings(w, r, user, "invites", "Invalid invite.", "", "", "", "", "")
		return
	}
	if err := s.invites.Revoke(r.Context(), id); err != nil {
		s.renderSettings(w, r, user, "invites", "Could not revoke the invite.", "", "", "", "", "")
		return
	}
	http.Redirect(w, r, "/settings?section=invites", http.StatusSeeOther)
}

// adminUserDelete removes an account entirely
func (s *Server) adminUserDelete(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !s.requireAdmin(user) {
		writeJSONError(w, http.StatusForbidden, "admins only")
		return
	}
	id, err := parseUUIDPath(r)
	if err != nil || id == user.ID {
		writeJSONError(w, http.StatusBadRequest, "invalid account")
		return
	}
	if err := s.users.Delete(r.Context(), id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not delete the account")
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/settings?section=invites")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/settings?section=invites", http.StatusSeeOther)
}

// inviteAcceptPage renders the public activation form for a token.
func (s *Server) inviteAcceptPage(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	invite, err := s.invites.Lookup(r.Context(), token)
	if err != nil || invite == nil {
		renderView(w, r, s.views.inviteT, "layout", viewData{
			Title:         "Invite",
			Section:       "invite",
			Error:         "This invite link is invalid. Ask for a new one.",
			PrimaryDomain: s.primaryDomain,
		})
		return
	}
	email := invite.InvitedEmail
	if email == "" {
		email = invite.Email
	}
	renderView(w, r, s.views.inviteT, "layout", viewData{
		Title:         "Accept invite",
		Section:       "invite",
		CSRFToken:     csrfTokenFromRequest(r),
		InviteEmail:   email,
		InviteLink:    token,
		PrimaryDomain: s.primaryDomain,
	})
}

// inviteAccept redeems a token
func (s *Server) inviteAccept(w http.ResponseWriter, r *http.Request) {
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
			if email == "" {
				email = invite.Email
			}
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
	username = identity.NormalizeUsername(username)
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
	session, err := s.sessions.Create(r.Context(), user.ID, sessionTTL)
	if err == nil && session != nil {
		s.setSessionCookie(w, r, session.Token)
	}
	http.Redirect(w, r, "/settings?section=profile&success=invite", http.StatusSeeOther)
}
