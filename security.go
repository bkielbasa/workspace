package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookieName = "session"
	csrfCookieName    = "csrf"
	sessionTTL        = 24 * time.Hour
)

type contextKey string

const (
	ctxKeyUser    contextKey = "user"
	ctxKeySession contextKey = "session"
)

func userFromContext(ctx context.Context) *User {
	u, ok := ctx.Value(ctxKeyUser).(*User)
	if !ok {
		return nil
	}
	return u
}

func sessionTokenFromContext(ctx context.Context) string {
	token, _ := ctx.Value(ctxKeySession).(string)
	return token
}

// securityHeaders applies a baseline set of HTTP security headers to every
// response. These reduce the impact of XSS, clickjacking, MIME sniffing and
// referrer leakage.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"base-uri 'none'; "+
				"form-action 'self'; "+
				"frame-ancestors 'none'; "+
				"object-src 'none'; "+
				"style-src 'self'; "+
				"script-src 'self'; "+
				"connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

// authError carries the HTTP status and body for a failed authentication.
type authError struct {
	code    int
	message string
}

// authenticate resolves the session cookie to the signed-in user. It clears
// the cookie when the token is invalid so the browser forgets the stale one.
func (a *Auth) authenticate(w http.ResponseWriter, r *http.Request) (*User, *authError) {
	token := sessionToken(r)
	if token == "" {
		return nil, &authError{http.StatusUnauthorized, "authentication required"}
	}

	session, err := a.Sessions.GetByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrSessionExpired) {
			a.clearSessionCookie(w)
			return nil, &authError{http.StatusUnauthorized, "session expired, please sign in again"}
		}
		return nil, &authError{http.StatusInternalServerError, err.Error()}
	}

	user, err := a.Users.Get(r.Context(), session.UserID)
	if err != nil || !user.Enabled {
		a.clearSessionCookie(w)
		return nil, &authError{http.StatusUnauthorized, "authentication required"}
	}

	return user, nil
}

// requireAuth validates the session cookie and loads the owning user into the
// request context. It is the gate for every authenticated JSON endpoint.
func (a *Auth) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, aerr := a.authenticate(w, r)
		if aerr != nil {
			writeJSONError(w, aerr.code, aerr.message)
			return
		}

		token := sessionToken(r)
		ctx := context.WithValue(r.Context(), ctxKeyUser, user)
		ctx = context.WithValue(ctx, ctxKeySession, token)
		next(w, r.WithContext(ctx))
	}
}

// page protects HTML pages: it redirects unauthenticated visitors to the
// sign-in page instead of answering JSON, which is friendlier for browsers.
func (a *Auth) page(next pageHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, aerr := a.authenticate(w, r)
		if aerr != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, user)
	}
}

// requireCSRF protects state-changing requests made with a session cookie
// using the double-submit cookie pattern: the same random value stored in the
// (non-HttpOnly) csrf cookie must be echoed in the X-CSRF-Token header.
//
// A cross-site attacker cannot read the csrf cookie, so they cannot supply the
// matching header, while browsers will still attach the cookie to a forged
// cross-site request. This defends against CSRF on cookie-authenticated
// mutations.
func (a *Auth) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(csrfCookieName)
		if err != nil || cookie.Value == "" {
			writeJSONError(w, http.StatusForbidden, "missing CSRF token")
			return
		}

		header := r.Header.Get("X-CSRF-Token")
		if header == "" {
			writeJSONError(w, http.StatusForbidden, "missing CSRF token")
			return
		}

		if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
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
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
