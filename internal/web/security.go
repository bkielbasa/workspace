package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
)

const (
	sessionCookieName = "session"
	csrfCookieName    = "csrf"
	sessionTTL        = 24 * time.Hour
)

type contextKey string

const (
	userContextKey    contextKey = "web-user"
	sessionContextKey contextKey = "web-session"
	nonceContextKey   contextKey = "web-style-nonce"
)

// styleNonce returns the per-request nonce that allows the page's generated
// stylesheet under the CSP in SecurityHeaders.
func styleNonce(ctx context.Context) string {
	nonce, _ := ctx.Value(nonceContextKey).(string)
	return nonce
}

// UserFromContext returns the user installed by RequireAuth.
func UserFromContext(ctx context.Context) *identity.User {
	user, _ := ctx.Value(userContextKey).(*identity.User)
	return user
}

func sessionTokenFromContext(ctx context.Context) string {
	token, _ := ctx.Value(sessionContextKey).(string)
	return token
}

// SecurityHeaders applies the baseline browser security policy to each response.
// Pages that need computed styles (the week grid) emit a <style> element
// carrying the per-request nonce, so no inline style attributes are needed.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		styleSrc := "style-src 'self'"
		nonce, err := newRandomToken()
		if err == nil {
			styleSrc += " 'nonce-" + nonce + "'"
			r = r.WithContext(context.WithValue(r.Context(), nonceContextKey, nonce))
		}

		headers := w.Header()
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		headers.Set("Referrer-Policy", "no-referrer")
		headers.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		headers.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"base-uri 'none'; "+
				"form-action 'self'; "+
				"frame-ancestors 'none'; "+
				"object-src 'none'; "+
				styleSrc+"; "+
				"script-src 'self'; "+
				"connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

type authError struct {
	status  int
	message string
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (*identity.User, *authError) {
	token := sessionToken(r)
	if token == "" {
		return nil, &authError{http.StatusUnauthorized, "authentication required"}
	}
	session, err := s.sessions.GetByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, identity.ErrSessionNotFound) || errors.Is(err, identity.ErrSessionExpired) {
			s.clearSessionCookie(w)
			return nil, &authError{http.StatusUnauthorized, "session expired, please sign in again"}
		}
		return nil, &authError{http.StatusInternalServerError, err.Error()}
	}
	user, err := s.users.Get(r.Context(), session.UserID)
	if err != nil || !user.Enabled {
		s.clearSessionCookie(w)
		return nil, &authError{http.StatusUnauthorized, "authentication required"}
	}
	return user, nil
}

// RequireAuth resolves the session and adds its identity.User to the context.
func (s *Server) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, authErr := s.authenticate(w, r)
		if authErr != nil {
			writeJSONError(w, authErr.status, authErr.message)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		ctx = context.WithValue(ctx, sessionContextKey, sessionToken(r))
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) page(next pageHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, authErr := s.authenticate(w, r)
		if authErr != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, user)
	}
}

// RequireCSRF enforces the double-submit cookie policy for mutations.
func (s *Server) RequireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(csrfCookieName)
		if err != nil || cookie.Value == "" {
			writeJSONError(w, http.StatusForbidden, "missing CSRF token")
			return
		}
		token := r.Header.Get("X-CSRF-Token")
		if token == "" {
			token = r.FormValue("_csrf")
		}
		if token == "" {
			writeJSONError(w, http.StatusForbidden, "missing CSRF token")
			return
		}
		if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) != 1 {
			writeJSONError(w, http.StatusForbidden, "invalid CSRF token")
			return
		}
		next(w, r)
	}
}

func sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func newRandomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
