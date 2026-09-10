package web

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type userResponse struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toUserResponse(user *identity.User) userResponse {
	return userResponse{
		ID: user.ID, Email: user.Email, DisplayName: user.DisplayName,
		Enabled: user.Enabled, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
	}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.allow(ip) {
		writeJSONError(w, http.StatusTooManyRequests, "too many sign-in attempts, please try again later")
		return
	}

	var request loginRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	if request.Email == "" || request.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := s.users.Authenticate(r.Context(), request.Email, request.Password)
	if err != nil {
		s.limiter.recordFailure(ip)
		writeJSONError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	session, err := s.sessions.Create(r.Context(), user.ID, sessionTTL)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	csrf, err := newRandomToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not start session")
		return
	}

	s.setSessionCookie(w, r, session.Token)
	s.setCSRFCookie(w, r, csrf)
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserResponse(user), "csrf": csrf})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if token := sessionTokenFromContext(r.Context()); token != "" {
		if err := s.sessions.Delete(r.Context(), token); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.clearSessionCookie(w)
	s.clearCSRFCookie(w)
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", "/login")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, toUserResponse(user))
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: s.cookieSecure(r), SameSite: http.SameSiteStrictMode,
		MaxAge: int(sessionTTL.Seconds()), Expires: time.Now().Add(sessionTTL),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Path: "/", HttpOnly: true, Secure: s.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func (s *Server) setCSRFCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: token, Path: "/", HttpOnly: false,
		Secure: s.cookieSecure(r), SameSite: http.SameSiteStrictMode,
		MaxAge: int(sessionTTL.Seconds()), Expires: time.Now().Add(sessionTTL),
	})
}

func (s *Server) clearCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Path: "/", HttpOnly: false, Secure: s.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func (s *Server) cookieSecure(r *http.Request) bool {
	return s.secure || r.TLS != nil
}

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
	return &loginLimiter{limit: limit, window: window, attempts: make(map[string]*attempt)}
}

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	current, ok := l.attempts[key]
	now := time.Now()
	if !ok || now.Sub(current.windowStart) >= l.window {
		l.attempts[key] = &attempt{windowStart: now}
		return true
	}
	return current.attempts < l.limit
}

func (l *loginLimiter) recordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	current, ok := l.attempts[key]
	if !ok {
		l.attempts[key] = &attempt{windowStart: time.Now(), attempts: 1}
		return
	}
	current.attempts++
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
