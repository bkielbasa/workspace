package web_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

type profileMockUserService struct {
	user              *identity.User
	authErr           error
	updateErr         error
	changePasswordErr error
	lastUpdatedName   string
	lastNewPassword   string
}

func (m *profileMockUserService) Authenticate(ctx context.Context, email, password string) (*identity.User, error) {
	if m.authErr != nil {
		return nil, m.authErr
	}
	return m.user, nil
}

func (m *profileMockUserService) Get(ctx context.Context, id uuid.UUID) (*identity.User, error) {
	return m.user, nil
}

func (m *profileMockUserService) Update(ctx context.Context, id uuid.UUID, displayName string, enabled bool) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.lastUpdatedName = displayName
	m.user.DisplayName = displayName
	return nil
}

func (m *profileMockUserService) ChangePassword(ctx context.Context, id uuid.UUID, password string) error {
	if m.changePasswordErr != nil {
		return m.changePasswordErr
	}
	m.lastNewPassword = password
	return nil
}

type profileMockSessionService struct {
	sessions map[string]*identity.Session
}

func newProfileMockSessionService() *profileMockSessionService {
	return &profileMockSessionService{
		sessions: make(map[string]*identity.Session),
	}
}

func (s *profileMockSessionService) Create(ctx context.Context, userID uuid.UUID, ttl time.Duration) (*identity.Session, error) {
	token := fmt.Sprintf("session-%d", len(s.sessions)+1)
	sess := &identity.Session{
		Token:  token,
		UserID: userID,
	}
	s.sessions[token] = sess
	return sess, nil
}

func (s *profileMockSessionService) GetByToken(ctx context.Context, token string) (*identity.Session, error) {
	sess, ok := s.sessions[token]
	if !ok {
		return nil, errors.New("session not found")
	}
	return sess, nil
}

func (s *profileMockSessionService) Delete(ctx context.Context, token string) error {
	delete(s.sessions, token)
	return nil
}

func setupProfileTestServer(t *testing.T) (*http.ServeMux, *profileMockUserService, *profileMockSessionService, uuid.UUID, string) {
	t.Helper()
	files := os.DirFS("../..")
	userID := uuid.New()
	user := &identity.User{
		ID:          userID,
		Email:       "testuser@example.com",
		DisplayName: "Test User",
		Enabled:     true,
		CreatedAt:   time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
	}

	userSvc := &profileMockUserService{user: user}
	sessSvc := newProfileMockSessionService()
	sess, _ := sessSvc.Create(context.Background(), userID, time.Hour)

	server, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessSvc, userSvc, false)
	if err != nil {
		t.Fatalf("web.New() error = %v", err)
	}

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	return mux, userSvc, sessSvc, userID, sess.Token
}

func TestProfilePageRequiresAuth(t *testing.T) {
	mux, _, _, _, _ := setupProfileTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/profile", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("expected redirect to /login, got %q", loc)
	}
}

func TestProfilePageRenders(t *testing.T) {
	mux, _, _, _, token := setupProfileTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/profile", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Account Profile") {
		t.Errorf("expected body to contain 'Account Profile'")
	}
	if !strings.Contains(body, "testuser@example.com") {
		t.Errorf("expected body to contain user email 'testuser@example.com'")
	}
	if !strings.Contains(body, "Test User") {
		t.Errorf("expected body to contain display name 'Test User'")
	}
	if !strings.Contains(body, "Change Password") {
		t.Errorf("expected body to contain 'Change Password'")
	}
	if !strings.Contains(body, "current_password") {
		t.Errorf("expected body to contain current_password input")
	}
	if !strings.Contains(body, "new_password") {
		t.Errorf("expected body to contain new_password input")
	}
	if !strings.Contains(body, "confirm_password") {
		t.Errorf("expected body to contain confirm_password input")
	}
}

func TestProfilePageSuccessBanners(t *testing.T) {
	mux, _, _, _, token := setupProfileTestServer(t)

	// Test password success banner
	req := httptest.NewRequest(http.MethodGet, "/profile?success=password", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "Your password has been changed successfully.") {
		t.Errorf("expected password success message in body, got: %s", rec.Body.String())
	}

	// Test profile update success banner
	req = httptest.NewRequest(http.MethodGet, "/profile?success=profile", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "Your profile details have been updated.") {
		t.Errorf("expected profile success message in body, got: %s", rec.Body.String())
	}
}

func TestProfileUpdate(t *testing.T) {
	mux, userSvc, _, _, token := setupProfileTestServer(t)

	csrfToken := "test-csrf-token"
	form := url.Values{
		"_csrf":        {csrfToken},
		"display_name": {"New Display Name"},
	}
	req := httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/profile?success=profile" {
		t.Fatalf("expected redirect to /profile?success=profile, got %q", loc)
	}
	if userSvc.lastUpdatedName != "New Display Name" {
		t.Fatalf("expected updated name 'New Display Name', got %q", userSvc.lastUpdatedName)
	}
}

func TestProfileChangePasswordValidation(t *testing.T) {
	mux, _, _, _, token := setupProfileTestServer(t)
	csrfToken := "test-csrf-token"

	tests := []struct {
		name            string
		currentPassword string
		newPassword     string
		confirmPassword string
		expectedError   string
	}{
		{
			name:            "empty current password",
			currentPassword: "",
			newPassword:     "newpassword123",
			confirmPassword: "newpassword123",
			expectedError:   "Current password is required.",
		},
		{
			name:            "empty new password",
			currentPassword: "oldpassword",
			newPassword:     "",
			confirmPassword: "",
			expectedError:   "New password is required.",
		},
		{
			name:            "short new password",
			currentPassword: "oldpassword",
			newPassword:     "short",
			confirmPassword: "short",
			expectedError:   "New password must be at least 8 characters long.",
		},
		{
			name:            "mismatched passwords",
			currentPassword: "oldpassword",
			newPassword:     "newpassword123",
			confirmPassword: "mismatchpassword",
			expectedError:   "New passwords do not match.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{
				"_csrf":            {csrfToken},
				"current_password": {tt.currentPassword},
				"new_password":     {tt.newPassword},
				"confirm_password": {tt.confirmPassword},
			}
			req := httptest.NewRequest(http.MethodPost, "/profile/password", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "session", Value: token})
			req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			body := rec.Body.String()
			if !strings.Contains(body, tt.expectedError) {
				t.Errorf("expected error %q in response, got: %s", tt.expectedError, body)
			}
		})
	}
}

func TestProfileChangePasswordIncorrectCurrent(t *testing.T) {
	mux, userSvc, _, _, token := setupProfileTestServer(t)
	userSvc.authErr = errors.New("invalid password")

	csrfToken := "test-csrf-token"
	form := url.Values{
		"_csrf":            {csrfToken},
		"current_password": {"wrong-password"},
		"new_password":     {"brandnewpass123"},
		"confirm_password": {"brandnewpass123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/profile/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "Current password is incorrect.") {
		t.Fatalf("expected 'Current password is incorrect.' in body, got: %s", body)
	}
	if userSvc.lastNewPassword != "" {
		t.Fatalf("expected ChangePassword not to be called, got: %q", userSvc.lastNewPassword)
	}
}

func TestProfileChangePasswordSuccess(t *testing.T) {
	mux, userSvc, _, _, token := setupProfileTestServer(t)

	csrfToken := "test-csrf-token"
	form := url.Values{
		"_csrf":            {csrfToken},
		"current_password": {"correct-password"},
		"new_password":     {"brandnewpass123"},
		"confirm_password": {"brandnewpass123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/profile/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/profile?success=password" {
		t.Fatalf("expected redirect to /profile?success=password, got %q", loc)
	}
	if userSvc.lastNewPassword != "brandnewpass123" {
		t.Fatalf("expected ChangePassword called with 'brandnewpass123', got %q", userSvc.lastNewPassword)
	}

	// Verify new session cookie was set
	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("expected session cookie to be set after password change")
	}
	if sessionCookie.Value == "" {
		t.Fatalf("expected session cookie to have a non-empty token")
	}
}
