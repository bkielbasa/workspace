package web

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

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
	var passwords []identity.AppPassword
	if s.appPasswords != nil {
		if list, err := s.appPasswords.List(r.Context(), user.ID); err == nil {
			passwords = list
		}
	}
	renderView(w, r, s.views.profileT, "layout", viewData{
		Title:         "Profile",
		Section:       "profile",
		User:          user,
		CSRFToken:     csrfTokenFromRequest(r),
		Error:         errMsg,
		Success:       successMsg,
		AppPasswords:  passwords,
		DevicesReady:  s.appPasswords != nil,
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
	w.Header().Set("Content-Type", appleprofile.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+domain+`-iphone.mobileconfig"`)
	if _, err := w.Write(profile); err != nil {
		obs.Log(r.Context(), slog.LevelError, "write iphone profile failed", "user_id", user.ID, "error", err)
	}
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
