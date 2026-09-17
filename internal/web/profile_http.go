package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bklimczak/workspace/internal/appleprofile"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

func (s *Server) profilePage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	var successMsg string
	switch r.URL.Query().Get("success") {
	case "password":
		successMsg = "Your password has been changed successfully."
	case "profile":
		successMsg = "Your profile details have been updated."
	case "invite":
		successMsg = "Welcome! Your account is ready — grab the iPhone profile below for one-tap mail setup."
	}

	if fresh, err := s.users.Get(r.Context(), user.ID); err == nil && fresh != nil {
		user = fresh
	}

	s.renderProfile(w, r, user, "", successMsg)
}

func (s *Server) profileUpdate(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderProfile(w, r, user, "Invalid form submission.", "")
		return
	}

	displayName := strings.TrimSpace(r.FormValue("display_name"))
	if err := s.users.Update(r.Context(), user.ID, displayName, user.Enabled); err != nil {
		obs.Log(r.Context(), slog.LevelError, "failed to update profile", "user_id", user.ID, "error", err)
		s.renderProfile(w, r, user, "Could not update profile details.", "")
		return
	}

	if username := strings.TrimSpace(r.FormValue("username")); username != "" && username != user.Username {
		if err := s.users.SetUsername(r.Context(), user.ID, username); err != nil {
			obs.Log(r.Context(), slog.LevelWarn, "invalid username", "user_id", user.ID, "error", err)
			s.renderProfile(w, r, user, "Could not change username: "+err.Error(), "")
			return
		}
	}

	http.Redirect(w, r, "/profile?success=profile", http.StatusSeeOther)
}

func (s *Server) profileChangePassword(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderProfile(w, r, user, "Invalid form submission.", "")
		return
	}

	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirmPassword := r.FormValue("confirm_password")

	if currentPassword == "" {
		s.renderProfile(w, r, user, "Current password is required.", "")
		return
	}
	if newPassword == "" {
		s.renderProfile(w, r, user, "New password is required.", "")
		return
	}
	if len(newPassword) < 8 {
		s.renderProfile(w, r, user, "New password must be at least 8 characters long.", "")
		return
	}
	if newPassword != confirmPassword {
		s.renderProfile(w, r, user, "New passwords do not match.", "")
		return
	}

	// Verify current password against credentials
	if _, err := s.users.Authenticate(r.Context(), user.Email, currentPassword); err != nil {
		s.renderProfile(w, r, user, "Current password is incorrect.", "")
		return
	}

	// Change password in store
	if err := s.users.ChangePassword(r.Context(), user.ID, newPassword); err != nil {
		obs.Log(r.Context(), slog.LevelError, "failed to change password", "user_id", user.ID, "error", err)
		s.renderProfile(w, r, user, "Could not change password. Please try again.", "")
		return
	}

	// Re-establish session since changing password revokes existing sessions
	session, err := s.sessions.Create(r.Context(), user.ID, sessionTTL)
	if err == nil && session != nil {
		s.setSessionCookie(w, r, session.Token)
	}

	http.Redirect(w, r, "/profile?success=password", http.StatusSeeOther)
}

func (s *Server) renderProfile(w http.ResponseWriter, r *http.Request, user *identity.User, errMsg, successMsg string) {
	s.renderProfileWithInvite(w, r, user, errMsg, successMsg, "", "")
}

func (s *Server) renderProfileWithPassword(w http.ResponseWriter, r *http.Request, user *identity.User, plain, name string) {
	var passwords []identity.AppPassword
	if s.appPasswords != nil {
		if list, err := s.appPasswords.List(r.Context(), user.ID); err == nil {
			passwords = list
		}
	}
	renderView(w, r, s.views.profileT, "layout", viewData{
		Title:              "Profile",
		Section:            "profile",
		User:               user,
		CSRFToken:          csrfTokenFromRequest(r),
		AppPasswords:       passwords,
		DevicesReady:       s.appPasswords != nil,
		NewAppPassword:     plain,
		NewAppPasswordName: name,
	})
}

func (s *Server) renderProfileWithInvite(w http.ResponseWriter, r *http.Request, user *identity.User, errMsg, successMsg, inviteLink, inviteEmail string) {
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
	renderView(w, r, s.views.profileT, "layout", viewData{
		Title:        "Profile",
		Section:      "profile",
		User:         user,
		CSRFToken:    csrfTokenFromRequest(r),
		Error:        errMsg,
		Success:      successMsg,
		AppPasswords: passwords,
		DevicesReady: s.appPasswords != nil,
		AdminUsers:   adminUsers,
		Invites:      invites,
		InviteLink:   inviteLink,
		InviteEmail:  inviteEmail,
	})
}

// iphoneProfile rotates the named app password and serves a mobileconfig
// with it embedded, so iOS installs Mail, Contacts, Calendar and Files
// without asking for passwords. The plaintext exists only in this download
// response; the store keeps the bcrypt hash.
func (s *Server) iphoneProfile(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.appPasswords == nil {
		s.renderProfile(w, r, user, "Device setup is not configured.", "")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	plain, _, err := s.appPasswords.Rotate(r.Context(), user.ID, name)
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "rotate app password failed", "user_id", user.ID, "error", err)
		s.renderProfile(w, r, user, "Could not generate the iPhone profile.", "")
		return
	}
	profile, err := appleprofile.Build(user.Email, s.mailHost, s.davHost, requestHost(r), plain)
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "build iphone profile failed", "user_id", user.ID, "error", err)
		s.renderProfile(w, r, user, "Could not generate the iPhone profile.", "")
		return
	}

	_, domain, _ := strings.Cut(user.Email, "@")
	token := iphoneDownloadTokens.put(profile, domain)
	http.Redirect(w, r, "/profile/iphone-profile/download?token="+token, http.StatusSeeOther)
}

// iphoneDownloadTokens holds freshly generated profiles for a few minutes so
// iOS can fetch them with a plain GET. The Settings app refetches the profile
// URL outside the Safari session (no cookies), so the token in the URL is the
// auth; it is unguessable and short-lived. Single replica only.
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

// iphoneProfileDownload serves a generated profile to a plain GET. iOS routes
// profile installation through Settings, which refetches the URL without the
// login session — hence the bearer token instead of RequireAuth, and no
// Content-Disposition for iOS so Safari hands the file to Settings instead of
// parking it in Downloads (where reopening it re-GETs a POST-only route).
func (s *Server) iphoneProfileDownload(w http.ResponseWriter, r *http.Request) {
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

// appPasswordCreate mints a standalone app password and shows the plaintext
// once, for clients the iPhone profile doesn't cover (DAVx5, upload
// Shortcuts, desktop mail apps). Same show-once pattern as invite links.
func (s *Server) appPasswordCreate(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.appPasswords == nil {
		s.renderProfile(w, r, user, "Device setup is not configured.", "")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "Device"
	}
	plain, _, err := s.appPasswords.Rotate(r.Context(), user.ID, name)
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "create app password failed", "user_id", user.ID, "error", err)
		s.renderProfile(w, r, user, "Could not create the app password.", "")
		return
	}
	s.renderProfileWithPassword(w, r, user, plain, name)
}

// appPasswordRevoke deletes one app password; its devices stop working.
func (s *Server) appPasswordRevoke(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.appPasswords == nil {
		http.Redirect(w, r, "/profile", http.StatusSeeOther)
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(r.FormValue("id")))
	if err != nil {
		s.renderProfile(w, r, user, "Invalid app password.", "")
		return
	}
	if err := s.appPasswords.Revoke(r.Context(), user.ID, id); err != nil {
		obs.Log(r.Context(), slog.LevelError, "revoke app password failed", "user_id", user.ID, "error", err)
		s.renderProfile(w, r, user, "Could not revoke the app password.", "")
		return
	}
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
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

// inviteCreate provisions a disabled account plus a one-time token, emails
// the accept link, and shows it once for copying.
func (s *Server) inviteCreate(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !s.requireAdmin(user) {
		s.renderProfile(w, r, user, "Admins only.", "")
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	if !strings.Contains(email, "@") {
		s.renderProfile(w, r, user, "Enter a valid email address.", "")
		return
	}
	token, invite, err := s.invites.CreateInvite(r.Context(), email, displayName)
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "create invite failed", "error", err)
		s.renderProfile(w, r, user, "Could not create the invite: "+err.Error(), "")
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
	_ = invite
	s.renderProfileWithInvite(w, r, user, "", "Invite created for "+email+".", link, email)
}

// inviteRevoke burns an unused token.
func (s *Server) inviteRevoke(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if !s.requireAdmin(user) {
		s.renderProfile(w, r, user, "Admins only.", "")
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(r.FormValue("id")))
	if err != nil {
		s.renderProfile(w, r, user, "Invalid invite.", "")
		return
	}
	if err := s.invites.Revoke(r.Context(), id); err != nil {
		s.renderProfile(w, r, user, "Could not revoke the invite.", "")
		return
	}
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

// adminUserDelete removes an account entirely (mailboxes vanish with it).
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
		w.Header().Set("HX-Redirect", "/profile")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

// inviteAcceptPage renders the public activation form for a token.
func (s *Server) inviteAcceptPage(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	invite, err := s.invites.Lookup(r.Context(), token)
	if err != nil || invite == nil {
		renderView(w, r, s.views.inviteT, "layout", viewData{
			Title: "Invite", Section: "invite",
			Error: "This invite link is invalid. Ask for a new one.",
		})
		return
	}
	renderView(w, r, s.views.inviteT, "layout", viewData{
		Title:       "Accept invite",
		Section:     "invite",
		CSRFToken:   csrfTokenFromRequest(r),
		InviteEmail: invite.Email,
		InviteLink:  token,
	})
}

// inviteAccept redeems a token: sets name + password, enables the account,
// burns the token, and signs the new member straight in.
func (s *Server) inviteAccept(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.FormValue("token"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	password := r.FormValue("password")
	confirm := r.FormValue("confirm_password")

	fail := func(msg string) {
		invite, _ := s.invites.Lookup(r.Context(), token)
		email := ""
		if invite != nil {
			email = invite.Email
		}
		renderView(w, r, s.views.inviteT, "layout", viewData{
			Title: "Accept invite", Section: "invite",
			CSRFToken:   csrfTokenFromRequest(r),
			Error:       msg,
			InviteEmail: email,
			InviteLink:  token,
		})
	}

	if password == "" || password != confirm {
		fail("Passwords do not match.")
		return
	}
	user, err := s.invites.Accept(r.Context(), token, displayName, password)
	if err != nil {
		obs.Log(r.Context(), slog.LevelWarn, "accept invite failed", "error", err)
		switch {
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
	http.Redirect(w, r, "/profile?success=invite", http.StatusSeeOther)
}
