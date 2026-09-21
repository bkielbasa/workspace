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
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

// -----------------------------------------------------------------------------
// Shared Test Mocks & Helpers
// -----------------------------------------------------------------------------

type mockSignatureRepo struct {
	sigs []mail.Signature
}

func (m *mockSignatureRepo) Create(ctx context.Context, sig *mail.Signature) error {
	sig.ID = uuid.New()
	if sig.IsDefault {
		for i := range m.sigs {
			m.sigs[i].IsDefault = false
		}
	}
	m.sigs = append(m.sigs, *sig)
	return nil
}

func (m *mockSignatureRepo) GetByID(ctx context.Context, userID, id uuid.UUID) (*mail.Signature, error) {
	for _, s := range m.sigs {
		if s.UserID == userID && s.ID == id {
			return &s, nil
		}
	}
	return nil, mail.ErrSignatureNotFound
}

func (m *mockSignatureRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]mail.Signature, error) {
	var userSigs []mail.Signature
	for _, s := range m.sigs {
		if s.UserID == userID {
			userSigs = append(userSigs, s)
		}
	}
	return userSigs, nil
}

func (m *mockSignatureRepo) GetDefault(ctx context.Context, userID uuid.UUID) (*mail.Signature, error) {
	for _, s := range m.sigs {
		if s.UserID == userID && s.IsDefault {
			return &s, nil
		}
	}
	return nil, mail.ErrSignatureNotFound
}

func (m *mockSignatureRepo) Update(ctx context.Context, sig *mail.Signature) error {
	for i, s := range m.sigs {
		if s.UserID == sig.UserID && s.ID == sig.ID {
			if sig.IsDefault {
				for j := range m.sigs {
					m.sigs[j].IsDefault = false
				}
			}
			m.sigs[i] = *sig
			return nil
		}
	}
	return mail.ErrSignatureNotFound
}

func (m *mockSignatureRepo) SetDefault(ctx context.Context, userID, id uuid.UUID) error {
	found := false
	for i, s := range m.sigs {
		if s.UserID == userID && s.ID == id {
			m.sigs[i].IsDefault = true
			found = true
		} else if s.UserID == userID {
			m.sigs[i].IsDefault = false
		}
	}
	if !found {
		return mail.ErrSignatureNotFound
	}
	return nil
}

func (m *mockSignatureRepo) Delete(ctx context.Context, userID, id uuid.UUID) error {
	for i, s := range m.sigs {
		if s.UserID == userID && s.ID == id {
			m.sigs = append(m.sigs[:i], m.sigs[i+1:]...)
			return nil
		}
	}
	return mail.ErrSignatureNotFound
}

type mockRuleRepo struct {
	rules []mail.Rule
}

func (m *mockRuleRepo) Create(ctx context.Context, rule *mail.Rule) error {
	rule.ID = uuid.New()
	rule.Priority = len(m.rules)
	m.rules = append(m.rules, *rule)
	return nil
}

func (m *mockRuleRepo) GetByID(ctx context.Context, userID, id uuid.UUID) (*mail.Rule, error) {
	for _, r := range m.rules {
		if r.UserID == userID && r.ID == id {
			return &r, nil
		}
	}
	return nil, mail.ErrRuleNotFound
}

func (m *mockRuleRepo) ListByUser(ctx context.Context, userID uuid.UUID) ([]mail.Rule, error) {
	var userRules []mail.Rule
	for _, r := range m.rules {
		if r.UserID == userID {
			userRules = append(userRules, r)
		}
	}
	return userRules, nil
}

func (m *mockRuleRepo) ListEnabled(ctx context.Context, userID uuid.UUID) ([]mail.Rule, error) {
	var enabled []mail.Rule
	for _, r := range m.rules {
		if r.UserID == userID && r.Enabled {
			enabled = append(enabled, r)
		}
	}
	return enabled, nil
}

func (m *mockRuleRepo) Update(ctx context.Context, rule *mail.Rule) error {
	for i, r := range m.rules {
		if r.UserID == rule.UserID && r.ID == rule.ID {
			m.rules[i] = *rule
			return nil
		}
	}
	return mail.ErrRuleNotFound
}

func (m *mockRuleRepo) SetEnabled(ctx context.Context, userID, id uuid.UUID, enabled bool) error {
	for i, r := range m.rules {
		if r.UserID == userID && r.ID == id {
			m.rules[i].Enabled = enabled
			return nil
		}
	}
	return mail.ErrRuleNotFound
}

func (m *mockRuleRepo) Reorder(ctx context.Context, userID uuid.UUID, orderedIDs []uuid.UUID) error {
	idMap := make(map[uuid.UUID]int)
	for i, id := range orderedIDs {
		idMap[id] = i
	}
	for i, r := range m.rules {
		if r.UserID == userID {
			if priority, ok := idMap[r.ID]; ok {
				m.rules[i].Priority = priority
			}
		}
	}
	return nil
}

func (m *mockRuleRepo) Delete(ctx context.Context, userID, id uuid.UUID) error {
	for i, r := range m.rules {
		if r.UserID == userID && r.ID == id {
			m.rules = append(m.rules[:i], m.rules[i+1:]...)
			return nil
		}
	}
	return mail.ErrRuleNotFound
}

type mockMailService struct {
	mailServiceStub
	applyRulesCalled bool
	lastApplyUserID  uuid.UUID
}

func (m *mockMailService) ApplyRulesToInbox(ctx context.Context, userID uuid.UUID) (int, error) {
	m.applyRulesCalled = true
	m.lastApplyUserID = userID
	return 3, nil
}

type profileMockUserService struct {
	testUserService
	user              *identity.User
	usersList         []identity.User
	authErr           error
	updateErr         error
	changePasswordErr error
	lastUpdatedName   string
	lastNewPassword   string
	lastUsername      string
	setUsernameErr    error
}

func (m *profileMockUserService) Get(ctx context.Context, id uuid.UUID) (*identity.User, error) {
	return m.user, nil
}

func (m *profileMockUserService) Authenticate(ctx context.Context, email, password string) (*identity.User, error) {
	if m.authErr != nil {
		return nil, m.authErr
	}
	if password == "correct-password" {
		return m.user, nil
	}
	return nil, fmt.Errorf("auth fail")
}

func (m *profileMockUserService) Update(ctx context.Context, id uuid.UUID, displayName string, enabled bool) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.lastUpdatedName = displayName
	if m.user != nil {
		m.user.DisplayName = displayName
	}
	return nil
}

func (m *profileMockUserService) List(context.Context) ([]identity.User, error) {
	return m.usersList, nil
}

func (m *profileMockUserService) Delete(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (m *profileMockUserService) SetUsername(ctx context.Context, id uuid.UUID, username string) error {
	m.lastUsername = username
	return m.setUsernameErr
}

func (m *profileMockUserService) ChangePassword(ctx context.Context, id uuid.UUID, password string) error {
	if m.changePasswordErr != nil {
		return m.changePasswordErr
	}
	m.lastNewPassword = password
	return nil
}

type profileMockSessionService struct {
	testSessionService
	sessions map[string]*identity.Session
	userID   uuid.UUID
}

func newProfileMockSessionService(userID uuid.UUID) *profileMockSessionService {
	return &profileMockSessionService{
		sessions: make(map[string]*identity.Session),
		userID:   userID,
	}
}

func (s *profileMockSessionService) Create(ctx context.Context, userID uuid.UUID, ttl time.Duration) (*identity.Session, error) {
	token := "valid-session"
	if len(s.sessions) > 0 {
		token = fmt.Sprintf("session-%d", len(s.sessions)+1)
	}
	sess := &identity.Session{
		Token:  token,
		UserID: userID,
	}
	s.sessions[token] = sess
	return sess, nil
}

func (s *profileMockSessionService) GetByToken(ctx context.Context, token string) (*identity.Session, error) {
	if sess, ok := s.sessions[token]; ok {
		return sess, nil
	}
	if token == "valid-session" {
		return &identity.Session{UserID: s.userID, Token: "valid-session"}, nil
	}
	return nil, errors.New("session not found")
}

func (s *profileMockSessionService) Delete(ctx context.Context, token string) error {
	delete(s.sessions, token)
	return nil
}

type stubAppPasswords struct {
	recs []identity.AppPassword
}

func (s *stubAppPasswords) Rotate(_ context.Context, userID uuid.UUID, name string) (string, *identity.AppPassword, error) {
	if name == "" {
		name = "iPhone"
	}
	kept := s.recs[:0]
	for _, r := range s.recs {
		if r.Name != name || r.UserID != userID {
			kept = append(kept, r)
		}
	}
	s.recs = kept
	rec := identity.AppPassword{ID: uuid.New(), UserID: userID, Name: name, CreatedAt: time.Now()}
	s.recs = append(s.recs, rec)
	return "test-device-secret", &rec, nil
}

func (s *stubAppPasswords) List(_ context.Context, userID uuid.UUID) ([]identity.AppPassword, error) {
	var out []identity.AppPassword
	for _, r := range s.recs {
		if r.UserID == userID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *stubAppPasswords) Revoke(_ context.Context, userID, id uuid.UUID) error {
	kept := s.recs[:0]
	for _, r := range s.recs {
		if r.ID != id || r.UserID != userID {
			kept = append(kept, r)
		}
	}
	s.recs = kept
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
	sessSvc := newProfileMockSessionService(userID)
	sess, _ := sessSvc.Create(context.Background(), userID, time.Hour)

	server, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessSvc, userSvc, false)
	if err != nil {
		t.Fatalf("web.New() error = %v", err)
	}

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	return mux, userSvc, sessSvc, userID, sess.Token
}

// -----------------------------------------------------------------------------
// Adapted Unified Settings Unit Tests
// -----------------------------------------------------------------------------

func TestProfilePageRequiresAuth(t *testing.T) {
	mux, _, _, _, _ := setupProfileTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
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

	// 1. Verify profile details render on default settings page (profile tab)
	{
		req := httptest.NewRequest(http.MethodGet, "/settings", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "testuser@example.com") {
			t.Errorf("expected body to contain user email 'testuser@example.com'")
		}
		if !strings.Contains(body, "Test User") {
			t.Errorf("expected body to contain display name 'Test User'")
		}
	}

	// 2. Verify Change Password renders on /settings?section=password
	{
		req := httptest.NewRequest(http.MethodGet, "/settings?section=password", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
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

	// 3. Verify iPhone Setup renders on /settings?section=iphone
	{
		req := httptest.NewRequest(http.MethodGet, "/settings?section=iphone", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "iPhone Setup") {
			t.Errorf("expected body to contain iPhone setup card")
		}
		if !strings.Contains(body, "/apple/mail.mobileconfig?email=testuser%40example.com") {
			t.Errorf("expected body to link the iPhone profile for the user")
		}
	}
}

func TestProfilePageSuccessBanners(t *testing.T) {
	mux, _, _, _, token := setupProfileTestServer(t)

	// Test password success banner
	req := httptest.NewRequest(http.MethodGet, "/settings?success=password", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "Your password has been changed successfully.") {
		t.Errorf("expected password success message in body, got: %s", rec.Body.String())
	}

	// Test profile update success banner
	req = httptest.NewRequest(http.MethodGet, "/settings?success=profile", nil)
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
	req := httptest.NewRequest(http.MethodPost, "/settings/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/settings?section=profile&success=profile" {
		t.Fatalf("expected redirect to /settings?section=profile&success=profile, got %q", loc)
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
			req := httptest.NewRequest(http.MethodPost, "/settings/password", strings.NewReader(form.Encode()))
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
	req := httptest.NewRequest(http.MethodPost, "/settings/password", strings.NewReader(form.Encode()))
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
	req := httptest.NewRequest(http.MethodPost, "/settings/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/settings?section=password&success=password" {
		t.Fatalf("expected redirect to /settings?section=password&success=password, got %q", loc)
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

func TestIPhoneProfileEmbedsAppPassword(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	user := &identity.User{ID: userID, Email: "testuser@example.com", DisplayName: "Test User", Enabled: true}
	userSvc := &profileMockUserService{user: user}
	sessSvc := newProfileMockSessionService(userID)
	sess, _ := sessSvc.Create(context.Background(), userID, time.Hour)
	apps := &stubAppPasswords{}

	server, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessSvc, userSvc, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetDeviceSetup(apps, "mail.cloudlift.run", "dav.cloudlift.run")
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	form := url.Values{"_csrf": {"test-csrf-token"}, "name": {"iPhone"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/iphone-profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "cloudlift.run"
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf-token"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/settings/iphone-profile/download?token=") {
		t.Fatalf("unexpected redirect %q", loc)
	}

	// iOS fetches the download with a plain GET, no session.
	dlReq := httptest.NewRequest(http.MethodGet, loc, nil)
	dlReq.Host = "cloudlift.run"
	dlRec := httptest.NewRecorder()
	mux.ServeHTTP(dlRec, dlReq)
	if dlRec.Code != http.StatusOK {
		t.Fatalf("download status = %d, body %s", dlRec.Code, dlRec.Body.String())
	}
	if ct := dlRec.Header().Get("Content-Type"); !strings.Contains(ct, "apple-aspen-config") {
		t.Errorf("download content-type = %q", ct)
	}
	body := dlRec.Body.String()
	for _, want := range []string{
		"<string>test-device-secret</string>",
		"<key>IncomingPassword</key>",
		"<key>CalDAVPassword</key>",
		"<key>CardDAVPassword</key>",
		"<string>https://cloudlift.run/files</string>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("profile missing %q", want)
		}
	}
	if len(apps.recs) != 1 || apps.recs[0].Name != "iPhone" {
		t.Errorf("app password not stored: %+v", apps.recs)
	}
}

func TestAppPasswordRevokeFlow(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	user := &identity.User{ID: userID, Email: "testuser@example.com", DisplayName: "Test User", Enabled: true}
	userSvc := &profileMockUserService{user: user}
	sessSvc := newProfileMockSessionService(userID)
	sess, _ := sessSvc.Create(context.Background(), userID, time.Hour)
	apps := &stubAppPasswords{recs: []identity.AppPassword{
		{ID: uuid.New(), UserID: userID, Name: "iPhone"},
	}}

	server, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessSvc, userSvc, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetDeviceSetup(apps, "mail.cloudlift.run", "dav.cloudlift.run")
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// List shows the credential.
	req := httptest.NewRequest(http.MethodGet, "/settings?section=app-passwords", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, "iPhone") || !strings.Contains(body, "Revoke") {
		t.Errorf("settings page missing app-passwords list")
	}

	// Revoke removes it.
	form := url.Values{"_csrf": {"test-csrf-token"}, "id": {apps.recs[0].ID.String()}}
	req = httptest.NewRequest(http.MethodPost, "/settings/app-passwords/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf-token"})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("revoke status = %d", rec.Code)
	}
	if len(apps.recs) != 0 {
		t.Errorf("revoke left %+v", apps.recs)
	}
}

func TestAppPasswordCreateShowsPlaintextOnce(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	user := &identity.User{ID: userID, Email: "testuser@example.com", DisplayName: "Test User", Enabled: true}
	userSvc := &profileMockUserService{user: user}
	sessSvc := newProfileMockSessionService(userID)
	sess, _ := sessSvc.Create(context.Background(), userID, time.Hour)
	apps := &stubAppPasswords{}

	server, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessSvc, userSvc, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetDeviceSetup(apps, "mail.cloudlift.run", "dav.cloudlift.run")
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	form := url.Values{"_csrf": {"test-csrf-token"}, "name": {"Uploader"}}
	req := httptest.NewRequest(http.MethodPost, "/settings/app-passwords", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf-token"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "test-device-secret") || !strings.Contains(body, "Uploader") {
		t.Errorf("create response hides the plaintext")
	}
	if !strings.Contains(body, "will not be shown again") {
		t.Errorf("create response missing show-once warning")
	}

	// A fresh page load must not repeat the secret.
	req = httptest.NewRequest(http.MethodGet, "/settings?section=app-passwords", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "test-device-secret") {
		t.Errorf("plaintext persisted beyond the creation response")
	}
}

func TestInviteCreateFlow(t *testing.T) {
	mux, apps, mailSvc, token := setupInviteTestServer(t, true)

	// Admin sees the Family card.
	req := httptest.NewRequest(http.MethodGet, "/settings?section=invites", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, "Family") {
		t.Errorf("admin missing Family card")
	}

	// Create sends the email and shows the one-time link.
	form := url.Values{"_csrf": {"test-csrf-token"}, "email": {"kid@example.com"}, "display_name": {"Kid"}}
	rec = invitePost(t, mux, "/settings/invites", form, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/invite/accept?token=test-token-123") {
		t.Errorf("one-time link not shown")
	}
	if mailSvc.lastSent == nil {
		t.Errorf("invite email not sent")
	}
	if apps.created != 1 {
		t.Errorf("invite not created")
	}

	// Non-admin is refused and sees no card.
	mux2, _, _, token2 := setupInviteTestServer(t, false)
	rec = invitePost(t, mux2, "/settings/invites", form, token2)
	if body := rec.Body.String(); !strings.Contains(body, "Admins only") {
		t.Errorf("non-admin not refused")
	}
	req = httptest.NewRequest(http.MethodGet, "/settings?section=invites", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token2})
	rec = httptest.NewRecorder()
	mux2.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "Family") {
		t.Errorf("non-admin sees Family card")
	}
}

func TestProfileUsernameIsReadOnly(t *testing.T) {
	srv, _, mockUsers, sessSvc := newTestServerWithInvites(t)
	testUser := &identity.User{
		ID:          uuid.New(),
		Email:       "alice@cloudlift.run",
		Username:    "alice",
		DisplayName: "Alice",
		Enabled:     true,
	}
	mockUsers.users[testUser.ID] = testUser
	sess, _ := sessSvc.Create(context.Background(), testUser.ID, time.Hour)

	// GET /settings renders username as disabled/readonly
	req := httptest.NewRequest("GET", "/settings", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /settings failed: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "alice@cloudlift.run") {
		t.Errorf("expected profile to show primary email")
	}
	if !strings.Contains(body, "disabled readonly") {
		t.Errorf("expected username input to be disabled readonly")
	}

	// POST /settings/profile with username does NOT update username
	csrfToken := "test-csrf-token"
	form := url.Values{
		"_csrf":        {csrfToken},
		"display_name": {"Alice Updated"},
		"username":     {"newalice"},
	}
	req = httptest.NewRequest(http.MethodPost, "/settings/profile", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if mockUsers.lastUsername != "" {
		t.Errorf("expected username update to be ignored, got SetUsername called with %q", mockUsers.lastUsername)
	}
}

func TestProfileUsernameReadOnly(t *testing.T) {
	TestProfileUsernameIsReadOnly(t)
}

func TestMailSettings_Get(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	sigs := &mockSignatureRepo{
		sigs: []mail.Signature{
			{ID: uuid.New(), UserID: userID, Name: "Work Signature", Content: "Alice\nEngineer", IsDefault: true},
		},
	}
	rules := &mockRuleRepo{
		rules: []mail.Rule{
			{
				ID:             uuid.New(),
				UserID:         userID,
				Name:           "Spam Rule",
				Enabled:        true,
				MatchMode:      "all",
				StopProcessing: true,
				Conditions:     []mail.RuleCondition{{Field: mail.RuleFieldFrom, Operator: mail.RuleOperatorContains, Value: "spam"}},
				Actions:        []mail.RuleAction{{Type: mail.RuleActionMoveToTrash}},
			},
		},
	}
	mailSvc := &mockMailService{}

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetMailSettings(sigs, rules)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// 1. Test signatures tab (default settings tab)
	{
		req := httptest.NewRequest(http.MethodGet, "/settings?section=signatures", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /settings?section=signatures status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Work Signature") {
			t.Errorf("missing signature 'Work Signature'")
		}
	}

	// 2. Test rules tab
	{
		req := httptest.NewRequest(http.MethodGet, "/settings?section=rules", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /settings?section=rules status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Spam Rule") {
			t.Errorf("missing rule 'Spam Rule'")
		}
	}
}

func TestMailSettings_SignaturesCRUD(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	sigs := &mockSignatureRepo{}
	rules := &mockRuleRepo{}
	mailSvc := &mockMailService{}

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetMailSettings(sigs, rules)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	csrfToken := "test-csrf"

	// 1. Create signature
	{
		form := url.Values{
			"name":       {"Personal"},
			"content":    {"Cheers,\nAli"},
			"is_default": {"on"},
			"_csrf":      {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/signatures", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST create signature status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		userSigs, _ := sigs.ListByUser(context.Background(), userID)
		if len(userSigs) != 1 {
			t.Fatalf("expected 1 signature, got %d", len(userSigs))
		}
		if userSigs[0].Name != "Personal" || !userSigs[0].IsDefault {
			t.Errorf("incorrect signature properties: %+v", userSigs[0])
		}
	}

	// Create second signature
	var sig2ID uuid.UUID
	{
		form := url.Values{
			"name":       {"Work"},
			"content":    {"Regards,\nAlice"},
			"is_default": {""},
			"_csrf":      {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/signatures", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		userSigs, _ := sigs.ListByUser(context.Background(), userID)
		if len(userSigs) != 2 {
			t.Fatalf("expected 2 signatures, got %d", len(userSigs))
		}
		sig2ID = userSigs[1].ID
	}

	// 2. Set default
	{
		form := url.Values{
			"_csrf": {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/settings/signatures/%s/default", sig2ID), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST set default status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		sig1, _ := sigs.GetByID(context.Background(), userID, sigs.sigs[0].ID)
		sig2, _ := sigs.GetByID(context.Background(), userID, sig2ID)

		if sig1.IsDefault || !sig2.IsDefault {
			t.Errorf("expected sig2 to be default and sig1 not default")
		}
	}

	// 3. Delete
	{
		form := url.Values{
			"_csrf": {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/settings/signatures/%s/delete", sig2ID), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST delete status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		userSigs, _ := sigs.ListByUser(context.Background(), userID)
		if len(userSigs) != 1 {
			t.Errorf("expected 1 signature after delete, got %d", len(userSigs))
		}
	}
}

func TestMailSettings_RulesCRUDAndApply(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	sigs := &mockSignatureRepo{}
	rules := &mockRuleRepo{}
	mailSvc := &mockMailService{}

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetMailSettings(sigs, rules)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	csrfToken := "test-csrf"

	// 1. Create Rule
	{
		form := url.Values{
			"name":            {"Filter News"},
			"match_mode":      {"any"},
			"cond_field[]":    {"subject", "from"},
			"cond_op[]":       {"contains", "equals"},
			"cond_val[]":      {"newsletter", "news@example.com"},
			"action_type[]":   {"move_to_folder", "mark_read"},
			"action_target[]": {"Archive", ""},
			"stop_processing": {"on"},
			"_csrf":           {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/rules", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST create rule status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		userRules, _ := rules.ListByUser(context.Background(), userID)
		if len(userRules) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(userRules))
		}
		rule := userRules[0]
		if rule.Name != "Filter News" || rule.MatchMode != "any" || !rule.StopProcessing || !rule.Enabled {
			t.Errorf("incorrect rule simple properties: %+v", rule)
		}
		if len(rule.Conditions) != 2 || rule.Conditions[0].Field != mail.RuleFieldSubject || rule.Conditions[1].Value != "news@example.com" {
			t.Errorf("incorrect rule conditions: %+v", rule.Conditions)
		}
		if len(rule.Actions) != 2 || rule.Actions[0].Type != mail.RuleActionMoveToFolder || rule.Actions[0].Target != "Archive" {
			t.Errorf("incorrect rule actions: %+v", rule.Actions)
		}
	}

	ruleID := rules.rules[0].ID

	// 2. Toggle Rule
	{
		form := url.Values{
			"enabled": {"false"},
			"_csrf":   {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/settings/rules/%s/toggle", ruleID), strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST toggle status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		r, _ := rules.GetByID(context.Background(), userID, ruleID)
		if r.Enabled {
			t.Errorf("expected rule to be disabled after toggle")
		}
	}

	// 3. Apply inbox
	{
		form := url.Values{
			"_csrf": {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/rules/apply-inbox", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST apply-inbox status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		if !mailSvc.applyRulesCalled {
			t.Errorf("expected mail.Service.ApplyRulesToInbox to be called")
		}
		if mailSvc.lastApplyUserID != userID {
			t.Errorf("expected userID %v, got %v", userID, mailSvc.lastApplyUserID)
		}
	}

	// 4. Reorder Rules
	{
		// Since we currently have 1 rule in the repo (ruleID), we create a second one.
		form := url.Values{
			"name":            {"Second Rule"},
			"match_mode":      {"all"},
			"cond_field[]":    {"subject"},
			"cond_op[]":       {"contains"},
			"cond_val[]":      {"second"},
			"action_type[]":   {"mark_read"},
			"action_target[]": {""},
			"stop_processing": {"off"},
			"_csrf":           {csrfToken},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/rules", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST create second rule status = %d, want %d", rec.Code, http.StatusSeeOther)
		}

		userRules, _ := rules.ListByUser(context.Background(), userID)
		if len(userRules) != 2 {
			t.Fatalf("expected 2 rules, got %d", len(userRules))
		}

		// rule1 (originally ruleID) and rule2 (the newly created one)
		rule1ID := ruleID
		rule2ID := userRules[1].ID

		// Call POST /settings/rules/reorder to move rule2 up
		reorderForm := url.Values{
			"id":        {rule2ID.String()},
			"direction": {"up"},
			"_csrf":     {csrfToken},
		}
		reorderReq := httptest.NewRequest(http.MethodPost, "/settings/rules/reorder", strings.NewReader(reorderForm.Encode()))
		reorderReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		reorderReq.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		reorderReq.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
		reorderRec := httptest.NewRecorder()
		mux.ServeHTTP(reorderRec, reorderReq)

		if reorderRec.Code != http.StatusSeeOther {
			t.Fatalf("POST reorder status = %d, want %d", reorderRec.Code, http.StatusSeeOther)
		}

		location := reorderRec.Header().Get("Location")
		if !strings.HasPrefix(location, "/settings?section=rules") {
			t.Errorf("expected redirect to /settings?section=rules, got %q", location)
		}

		// Assert priorities are updated in the repository
		updatedRules, _ := rules.ListByUser(context.Background(), userID)
		var r1, r2 mail.Rule
		for _, r := range updatedRules {
			if r.ID == rule1ID {
				r1 = r
			} else if r.ID == rule2ID {
				r2 = r
			}
		}

		if r2.Priority >= r1.Priority {
			t.Errorf("expected rule2 (priority %d) to be ordered before rule1 (priority %d)", r2.Priority, r1.Priority)
		}
	}

	// 5. Delete Rules
	{
		userRules, _ := rules.ListByUser(context.Background(), userID)
		for _, rule := range userRules {
			form := url.Values{
				"_csrf": {csrfToken},
			}
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/settings/rules/%s/delete", rule.ID), strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
			req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("POST delete rule status = %d, want %d", rec.Code, http.StatusSeeOther)
			}

			location := rec.Header().Get("Location")
			if !strings.HasPrefix(location, "/settings?section=rules") {
				t.Errorf("expected redirect to /settings?section=rules, got %q", location)
			}
		}

		finalRules, _ := rules.ListByUser(context.Background(), userID)
		if len(finalRules) != 0 {
			t.Errorf("expected 0 rules after delete, got %d", len(finalRules))
		}
	}
}

func TestMailSettings_AuthAndCSRF(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	sigs := &mockSignatureRepo{}
	rules := &mockRuleRepo{}
	mailSvc := &mockMailService{}

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetMailSettings(sigs, rules)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// 1. Mutate signature without authentication -> 401 unauth
	{
		form := url.Values{
			"name":  {"test"},
			"_csrf": {"test-csrf"},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/signatures", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized for unauthenticated mutate request, got %d", rec.Code)
		}
	}

	// 2. Mutate signature with auth but missing CSRF -> 403 Forbidden
	{
		form := url.Values{
			"name": {"test"},
		}
		req := httptest.NewRequest(http.MethodPost, "/settings/signatures", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for missing CSRF, got %d", rec.Code)
		}
	}
}

func TestMailSettings_MultiActionRuleParsing(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	sigs := &mockSignatureRepo{}
	rules := &mockRuleRepo{}
	mailSvc := &mockMailService{}

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetMailSettings(sigs, rules)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	csrfToken := "test-csrf"

	form := url.Values{
		"name":            {"Multi Action Parse Rule"},
		"match_mode":      {"all"},
		"cond_field[]":    {"subject"},
		"cond_op[]":       {"contains"},
		"cond_val[]":      {"multi"},
		"action_type[]":   {"mark_read", "move_to_folder"},
		"action_target[]": {"", "Archive"},
		"stop_processing": {"on"},
		"_csrf":           {csrfToken},
	}
	req := httptest.NewRequest(http.MethodPost, "/settings/rules", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST create rule status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	userRules, _ := rules.ListByUser(context.Background(), userID)
	if len(userRules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(userRules))
	}
	rule := userRules[0]
	if len(rule.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(rule.Actions))
	}
	if rule.Actions[0].Type != mail.RuleActionMarkRead || rule.Actions[0].Target != "" {
		t.Errorf("expected first action to be mark_read with empty target, got %+v", rule.Actions[0])
	}
	if rule.Actions[1].Type != mail.RuleActionMoveToFolder || rule.Actions[1].Target != "Archive" {
		t.Errorf("expected second action to be move_to_folder with Archive target, got %+v", rule.Actions[1])
	}
}
