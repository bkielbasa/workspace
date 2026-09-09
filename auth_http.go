package main

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Auth wires session and user stores together and implements the web session
// flow: login, logout, and the current-user endpoint. It also owners the CSRF
// and rate-limit policies that protect the cookie-based flow.
type Auth struct {
	Sessions *Sessions
	Users    *Users

	// Secure forces the Secure attribute on cookies. When false (the default),
	// cookies are marked Secure only when the request arrives over TLS, so
	// plain-HTTP local development still works while production stays safe.
	Secure bool

	limiter *loginLimiter
}

// NewAuth constructs the session/CSRF auth layer for the HTTP server.
func NewAuth(sessions *Sessions, users *Users, secure bool) *Auth {
	return &Auth{
		Sessions: sessions,
		Users:    users,
		Secure:   secure,
		limiter:  newLoginLimiter(30, 15*time.Minute),
	}
}

// userResponse is the public representation of a User. It deliberately omits
// the password hash so credentials are never leaked over the wire.
type userResponse struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toUserResponse(u *User) userResponse {
	return userResponse{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Enabled:     u.Enabled,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}

//------ login ---------------------------------------------------------------

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginHandler authenticates credentials, starts a session, and sets the
// session and CSRF cookies on the response.
func (a *Auth) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if !a.limiter.allow(clientIP(r)) {
		writeJSONError(w, http.StatusTooManyRequests, "too many sign-in attempts, please try again later")
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := a.Users.Authenticate(r.Context(), req.Email, req.Password)
	if err != nil {
		// Indistinguishable error for both "no such account" and "wrong
		// password" so attackers cannot enumerate valid accounts.
		a.limiter.recordFailure(clientIP(r))
		writeJSONError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	session, err := a.Sessions.Create(r.Context(), user.ID, sessionTTL)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	csrf, err := newRandomToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not start session")
		return
	}

	a.setSessionCookie(w, r, session.Token)
	a.setCSRFCookie(w, r, csrf)

	writeJSON(w, http.StatusOK, map[string]any{
		"user": toUserResponse(user),
		"csrf": csrf,
	})
}

//------ logout --------------------------------------------------------------

// LogoutHandler revokes the active session and clears its cookies. It requires
// both an authenticated session and a valid CSRF token (it is a mutation).
func (a *Auth) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	token := sessionTokenFromContext(r.Context())
	if token != "" {
		if err := a.Sessions.Delete(r.Context(), token); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	a.clearSessionCookie(w)
	a.clearCSRFCookie(w)

	// Served through HTMX: ask it to navigate to /login after the session is
	// gone. Plain-browser POSTs just get the 204.
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", "/login")
	}
	w.WriteHeader(http.StatusNoContent)
}

// MeHandler returns the signed-in user. It is gated by requireAuth so the user
// is already resolved into context.
func (a *Auth) MeHandler(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(user))
}

//------ cookie helpers ------------------------------------------------------

func (a *Auth) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
		Expires:  time.Now().Add(sessionTTL),
	})
}

func (a *Auth) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

func (a *Auth) setCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	// Deliberately NOT HttpOnly: our JS must read it back to echo as the
	// X-CSRF-Token header (double-submit pattern).
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		Secure:   a.secure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL.Seconds()),
		Expires:  time.Now().Add(sessionTTL),
	})
}

func (a *Auth) clearCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   a.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

func (a *Auth) secure(r *http.Request) bool {
	return a.Secure || r.TLS != nil
}

//------ rate limiting -------------------------------------------------------

type attempt struct {
	windowStart time.Time
	attempts    int
}

type loginLimiter struct {
	limit    int
	window   time.Duration
	mu       sync.Mutex
	attempts map[string]*attempt
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	if limit <= 0 {
		limit = 30
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	return &loginLimiter{
		limit:    limit,
		window:   window,
		attempts: make(map[string]*attempt),
	}
}

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.attempts[key]
	now := time.Now()
	if !ok || now.Sub(a.windowStart) >= l.window {
		l.attempts[key] = &attempt{windowStart: now}
		return true
	}
	return a.attempts < l.limit
}

func (l *loginLimiter) recordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.attempts[key]
	if !ok {
		l.attempts[key] = &attempt{windowStart: time.Now(), attempts: 1}
		return
	}
	a.attempts++
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
