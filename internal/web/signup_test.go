package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
	"github.com/bklimczak/workspace/internal/web"
)

type mockSignupUsers struct {
	users map[string]*identity.User
}

func (m *mockSignupUsers) Authenticate(_ context.Context, login, password string) (*identity.User, error) {
	u, ok := m.users[login]
	if !ok {
		return nil, identity.ErrUserNotFound
	}
	return u, nil
}

func (m *mockSignupUsers) Get(_ context.Context, id uuid.UUID) (*identity.User, error) {
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, identity.ErrUserNotFound
}

func (m *mockSignupUsers) Update(context.Context, uuid.UUID, string, bool) error    { return nil }
func (m *mockSignupUsers) ChangePassword(context.Context, uuid.UUID, string) error { return nil }
func (m *mockSignupUsers) List(context.Context) ([]identity.User, error)            { return nil, nil }
func (m *mockSignupUsers) Delete(context.Context, uuid.UUID) error                  { return nil }
func (m *mockSignupUsers) SetUsername(context.Context, uuid.UUID, string) error    { return nil }

func (m *mockSignupUsers) Create(_ context.Context, loginOrUsername, password, displayName string) (*identity.User, error) {
	username := loginOrUsername
	if idx := strings.IndexByte(username, '@'); idx >= 0 {
		username = username[:idx]
	}
	if err := identity.ValidateUsername(username); err != nil {
		return nil, err
	}
	if err := identity.ValidatePassword(password); err != nil {
		return nil, err
	}
	if _, exists := m.users[username]; exists {
		return nil, identity.ErrUserAlreadyExists
	}
	email := username + "@cloudlift.run"
	if _, exists := m.users[email]; exists {
		return nil, identity.ErrUserAlreadyExists
	}
	u := &identity.User{
		ID:          uuid.New(),
		Email:       email,
		Username:    username,
		DisplayName: displayName,
		Enabled:     true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	m.users[username] = u
	m.users[email] = u
	return u, nil
}

type mockSignupSessions struct {
	sessions map[string]*identity.Session
}

func (m *mockSignupSessions) Create(_ context.Context, userID uuid.UUID, ttl time.Duration) (*identity.Session, error) {
	tok := uuid.NewString()
	sess := &identity.Session{
		Token:     tok,
		UserID:    userID,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(ttl),
	}
	m.sessions[tok] = sess
	return sess, nil
}

func (m *mockSignupSessions) GetByToken(_ context.Context, token string) (*identity.Session, error) {
	if s, ok := m.sessions[token]; ok {
		return s, nil
	}
	return nil, identity.ErrSessionNotFound
}

func (m *mockSignupSessions) Delete(_ context.Context, token string) error {
	delete(m.sessions, token)
	return nil
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newTestServerWithUsers(t, &mockSignupUsers{users: make(map[string]*identity.User)})
}

func newTestServerWithUsers(t *testing.T, users *mockSignupUsers) *httptest.Server {
	t.Helper()
	files := os.DirFS("../..")
	sessions := &mockSignupSessions{sessions: make(map[string]*identity.Session)}
	srv, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessions, users, false)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestSignupPageRenders(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/signup")
	if err != nil {
		t.Fatalf("GET /signup failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}

func TestSignupCreatesCloudliftUser(t *testing.T) {
	srv := newTestServer(t)
	body := `{"username":"newuser","password":"securepassword123","display_name":"New User"}`
	resp, err := http.Post(srv.URL+"/signup", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /signup failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// Verify session cookie is set
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Errorf("expected session cookie to be set in response")
	}

	// Verify response body JSON
	var result struct {
		User struct {
			Email       string `json:"email"`
			DisplayName string `json:"display_name"`
		} `json:"user"`
		CSRF string `json:"csrf"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if result.User.Email != "newuser@cloudlift.run" {
		t.Errorf("expected email newuser@cloudlift.run, got %q", result.User.Email)
	}
	if result.User.DisplayName != "New User" {
		t.Errorf("expected display_name 'New User', got %q", result.User.DisplayName)
	}
	if result.CSRF == "" {
		t.Errorf("expected non-empty CSRF token in response")
	}
}

func TestSignupValidationErrors(t *testing.T) {
	users := &mockSignupUsers{
		users: map[string]*identity.User{
			"existing": {
				ID:       uuid.New(),
				Username: "existing",
				Email:    "existing@cloudlift.run",
			},
		},
	}
	srv := newTestServerWithUsers(t, users)

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "short password",
			body:       `{"username":"alice","password":"123","display_name":"Alice"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid username with spaces",
			body:       `{"username":"bad user","password":"securepassword123","display_name":"Bad"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "short username",
			body:       `{"username":"a","password":"securepassword123","display_name":"A"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "existing user conflict",
			body:       `{"username":"existing","password":"securepassword123","display_name":"Existing"}`,
			wantStatus: http.StatusConflict,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(srv.URL+"/signup", "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("POST /signup failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("expected status %d, got %d", tc.wantStatus, resp.StatusCode)
			}
		})
	}
}

func TestSignupRedirectsIfLoggedIn(t *testing.T) {
	users := &mockSignupUsers{
		users: map[string]*identity.User{
			"alice": {
				ID:       uuid.New(),
				Username: "alice",
				Email:    "alice@cloudlift.run",
				Enabled:  true,
			},
		},
	}
	sessions := &mockSignupSessions{sessions: make(map[string]*identity.Session)}
	sess, _ := sessions.Create(context.Background(), users.users["alice"].ID, time.Hour)

	files := os.DirFS("../..")
	srv, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessions, users, false)
	if err != nil {
		t.Fatalf("web.New failed: %v", err)
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, _ := http.NewRequest("GET", ts.URL+"/signup", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /signup failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect (302 or 303), got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/" {
		t.Errorf("expected redirect to '/', got %q", loc)
	}
}

func TestSignupFormPost(t *testing.T) {
	srv := newTestServer(t)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	formData := url.Values{
		"username":     {"formuser"},
		"password":     {"securepassword123"},
		"display_name": {"Form User"},
	}
	resp, err := client.PostForm(srv.URL+"/signup", formData)
	if err != nil {
		t.Fatalf("POST /signup failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 See Other, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("expected redirect to '/', got %q", loc)
	}
}

func TestSignupFormPostValidationErrors(t *testing.T) {
	srv := newTestServer(t)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	tests := []struct {
		name        string
		values      url.Values
		errContains string
	}{
		{
			name: "short password",
			values: url.Values{
				"username":     {"validuser"},
				"password":     {"short"},
				"display_name": {"User"},
			},
			errContains: "at least 8 characters",
		},
		{
			name: "invalid username",
			values: url.Values{
				"username":     {"bad name"},
				"password":     {"securepassword123"},
				"display_name": {"User"},
			},
			errContains: "letters",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.PostForm(srv.URL+"/signup", tc.values)
			if err != nil {
				t.Fatalf("POST /signup failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("expected 303 See Other, got %d", resp.StatusCode)
			}
			loc := resp.Header.Get("Location")
			if !strings.HasPrefix(loc, "/signup?error=") {
				t.Fatalf("expected redirect to /signup?error=..., got %q", loc)
			}
		})
	}
}

func TestSignupRateLimiting(t *testing.T) {
	srv := newTestServer(t)

	// Default limit in newLoginLimiter is 30 attempts in 15 minutes.
	// Exhaust the limit with invalid requests (short password).
	for i := 0; i < 30; i++ {
		resp, err := http.Post(srv.URL+"/signup", "application/json", strings.NewReader(`{"username":"user","password":"bad"}`))
		if err != nil {
			t.Fatalf("setup POST failed at iteration %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 at iteration %d, got %d", i, resp.StatusCode)
		}
	}

	// 31st request should be rate-limited (JSON) -> 429 Too Many Requests
	resp, err := http.Post(srv.URL+"/signup", "application/json", strings.NewReader(`{"username":"validuser","password":"validpassword123"}`))
	if err != nil {
		t.Fatalf("rate limited POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d", resp.StatusCode)
	}

	var errResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if !strings.Contains(errResp["error"], "too many sign-up attempts") {
		t.Errorf("expected error message to mention 'too many sign-up attempts', got %q", errResp["error"])
	}

	// Test HTML form submission when rate-limited -> redirects to /signup?error=...
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	formResp, err := client.PostForm(srv.URL+"/signup", url.Values{
		"username": {"another"},
		"password": {"validpassword123"},
	})
	if err != nil {
		t.Fatalf("form POST failed: %v", err)
	}
	defer formResp.Body.Close()

	if formResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 See Other when rate limited, got %d", formResp.StatusCode)
	}
	loc := formResp.Header.Get("Location")
	if !strings.Contains(loc, "too+many+sign-up+attempts") && !strings.Contains(loc, "too%20many%20sign-up%20attempts") {
		t.Errorf("expected location to contain rate limit error, got %q", loc)
	}
}


