package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
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

	if strings.Contains(request.Email, "@") && s.orgs != nil {
		parts := strings.Split(request.Email, "@")
		if len(parts) == 2 && parts[1] != "" {
			domain := parts[1]
			org, _, err := s.orgs.GetByDomain(r.Context(), domain)
			if err == nil && org != nil {
				sso, err := s.orgs.GetSSO(r.Context(), org.ID)
				if err == nil && sso != nil && sso.Enabled && sso.EnforceSSO {
					writeJSON(w, http.StatusForbidden, map[string]string{
						"error":        "sso_required",
						"redirect_url": "/login/sso?domain=" + domain,
						"message":      "Your organization requires Single Sign-On.",
					})
					return
				}
			}
		}
	}

	user, err := s.users.Authenticate(r.Context(), request.Email, request.Password)
	if err != nil {
		s.limiter.recordFailure(ip)
		writeJSONError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	csrf, err := s.establishSession(w, r, user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not start session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"user": toUserResponse(user), "csrf": csrf})
}

type signupViewData struct {
	Error         string
	PrimaryDomain string
}

func (s *Server) signupPage(w http.ResponseWriter, r *http.Request) {
	if s.validSession(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	primaryDomain := s.primaryDomain
	if primaryDomain == "" {
		primaryDomain = "cloudlift.run"
	}
	data := signupViewData{
		Error:         r.URL.Query().Get("error"),
		PrimaryDomain: primaryDomain,
	}
	renderView(w, r, s.views.signup, "signup", data)
}

type userCreator interface {
	Create(ctx context.Context, loginOrUsername, password, displayName string) (*identity.User, error)
}

type signupRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	isJSON := strings.Contains(r.Header.Get("Content-Type"), "application/json") || r.Header.Get("Accept") == "application/json"

	var req signupRequest
	if isJSON {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/signup?error=invalid+form+data", http.StatusSeeOther)
			return
		}
		req.Username = r.FormValue("username")
		req.Password = r.FormValue("password")
		req.DisplayName = r.FormValue("display_name")
	}

	username := identity.NormalizeUsername(req.Username)
	if err := identity.ValidateUsername(username); err != nil {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, err.Error())
		} else {
			http.Redirect(w, r, "/signup?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		}
		return
	}

	if err := identity.ValidatePassword(req.Password); err != nil {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, err.Error())
		} else {
			http.Redirect(w, r, "/signup?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		}
		return
	}

	primaryDomain := s.primaryDomain
	if primaryDomain == "" {
		primaryDomain = "cloudlift.run"
	}
	email := fmt.Sprintf("%s@%s", username, primaryDomain)

	creator, ok := s.users.(userCreator)
	if !ok {
		if isJSON {
			writeJSONError(w, http.StatusInternalServerError, "user creation not supported")
		} else {
			http.Redirect(w, r, "/signup?error=user+creation+not+supported", http.StatusSeeOther)
		}
		return
	}

	user, err := creator.Create(r.Context(), email, req.Password, strings.TrimSpace(req.DisplayName))
	if err != nil {
		if errors.Is(err, identity.ErrUserAlreadyExists) {
			if isJSON {
				writeJSONError(w, http.StatusConflict, "username is already taken")
			} else {
				http.Redirect(w, r, "/signup?error=username+is+already+taken", http.StatusSeeOther)
			}
			return
		}
		if errors.Is(err, identity.ErrInvalidUsername) || errors.Is(err, identity.ErrInvalidPassword) {
			if isJSON {
				writeJSONError(w, http.StatusBadRequest, err.Error())
			} else {
				http.Redirect(w, r, "/signup?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			}
			return
		}
		if isJSON {
			writeJSONError(w, http.StatusInternalServerError, "could not create user")
		} else {
			http.Redirect(w, r, "/signup?error=could+not+create+user", http.StatusSeeOther)
		}
		return
	}

	csrf, err := s.establishSession(w, r, user)
	if err != nil {
		if isJSON {
			writeJSONError(w, http.StatusInternalServerError, "could not start session")
		} else {
			http.Redirect(w, r, "/signup?error=could+not+start+session", http.StatusSeeOther)
		}
		return
	}

	if isJSON {
		writeJSON(w, http.StatusOK, map[string]any{"user": toUserResponse(user), "csrf": csrf})
	} else {
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// establishSession issues a session token and CSRF nonce as cookies for user.
// Returns the CSRF token so callers can hand it to the client.
func (s *Server) establishSession(w http.ResponseWriter, r *http.Request, user *identity.User) (string, error) {
	session, err := s.sessions.Create(r.Context(), user.ID, sessionTTL)
	if err != nil {
		return "", err
	}
	csrf, err := newRandomToken()
	if err != nil {
		return "", err
	}
	s.setSessionCookie(w, r, session.Token)
	s.setCSRFCookie(w, r, csrf)
	return csrf, nil
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
