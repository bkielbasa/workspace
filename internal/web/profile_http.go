package web

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
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
	renderView(w, r, s.views.profileT, "layout", viewData{
		Title:     "Profile",
		Section:   "profile",
		User:      user,
		CSRFToken: csrfTokenFromRequest(r),
		Error:     errMsg,
		Success:   successMsg,
	})
}
