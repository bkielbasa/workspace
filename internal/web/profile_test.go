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
	usersList         []identity.User
	authErr           error
	updateErr         error
	changePasswordErr error
	lastUpdatedName   string
	lastNewPassword   string
	lastUsername      string
	setUsernameErr    error
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
	if !strings.Contains(body, "iPhone Setup") {
		t.Errorf("expected body to contain iPhone setup card")
	}
	if !strings.Contains(body, "/apple/mail.mobileconfig?email=testuser%40example.com") {
		t.Errorf("expected body to link the iPhone profile for the user")
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

func TestIPhoneProfileEmbedsAppPassword(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	user := &identity.User{ID: userID, Email: "testuser@example.com", DisplayName: "Test User", Enabled: true}
	userSvc := &profileMockUserService{user: user}
	sessSvc := newProfileMockSessionService()
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
	req := httptest.NewRequest(http.MethodPost, "/profile/iphone-profile", strings.NewReader(form.Encode()))
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
	if !strings.HasPrefix(loc, "/profile/iphone-profile/download?token=") {
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
	sessSvc := newProfileMockSessionService()
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
	req := httptest.NewRequest(http.MethodGet, "/profile", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, "iPhone") || !strings.Contains(body, "Revoke") {
		t.Errorf("profile missing password list")
	}

	// Revoke removes it.
	form := url.Values{"_csrf": {"test-csrf-token"}, "id": {apps.recs[0].ID.String()}}
	req = httptest.NewRequest(http.MethodPost, "/profile/app-passwords/revoke", strings.NewReader(form.Encode()))
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
	sessSvc := newProfileMockSessionService()
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
	req := httptest.NewRequest(http.MethodPost, "/profile/app-passwords", strings.NewReader(form.Encode()))
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
	req = httptest.NewRequest(http.MethodGet, "/profile", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sess.Token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "test-device-secret") {
		t.Errorf("plaintext persisted beyond the creation response")
	}
}

type stubInvites struct {
	token   string
	invite  *identity.Invite
	created int
	revoked []uuid.UUID
	user    *identity.User
}

func (s *stubInvites) CreateInvite(_ context.Context, email, _ string) (string, *identity.Invite, error) {
	s.created++
	inv := &identity.Invite{ID: uuid.New(), Email: email, ExpiresAt: time.Now().Add(time.Hour)}
	if s.invite != nil {
		inv.UserID = s.invite.UserID
	}
	s.invite = inv
	return s.token, inv, nil
}

func (s *stubInvites) Lookup(_ context.Context, token string) (*identity.Invite, error) {
	if s.invite == nil || token != s.token {
		return nil, identity.ErrInviteNotFound
	}
	return s.invite, nil
}

func (s *stubInvites) Accept(_ context.Context, token, _, _, _ string) (*identity.User, error) {
	if s.invite == nil || token != s.token {
		return nil, identity.ErrInviteNotFound
	}
	return s.user, nil
}

func (s *stubInvites) List(context.Context) ([]identity.Invite, error) {
	if s.invite == nil {
		return nil, nil
	}
	return []identity.Invite{*s.invite}, nil
}

func (s *stubInvites) Revoke(_ context.Context, id uuid.UUID) error {
	s.revoked = append(s.revoked, id)
	return nil
}

func setupInviteTestServer(t *testing.T, admin bool) (*http.ServeMux, *stubInvites, *mailServiceStub, string) {
	t.Helper()
	files := os.DirFS("../..")
	userID := uuid.New()
	user := &identity.User{ID: userID, Email: "admin@example.com", DisplayName: "Admin", Enabled: true, IsAdmin: admin}
	userSvc := &profileMockUserService{user: user}
	sessSvc := newProfileMockSessionService()
	sess, _ := sessSvc.Create(context.Background(), userID, time.Hour)
	mailSvc := &mailServiceStub{}
	apps := &stubInvites{token: "test-token-123", user: user}

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, sessSvc, userSvc, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetInvites(apps)
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	return mux, apps, mailSvc, sess.Token
}

func invitePost(t *testing.T, mux *http.ServeMux, target string, form url.Values, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf-token"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestInviteCreateFlow(t *testing.T) {
	mux, apps, mailSvc, token := setupInviteTestServer(t, true)

	// Admin sees the Family card.
	req := httptest.NewRequest(http.MethodGet, "/profile", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, "Family") {
		t.Errorf("admin missing Family card")
	}

	// Create sends the email and shows the one-time link.
	form := url.Values{"_csrf": {"test-csrf-token"}, "email": {"kid@example.com"}, "display_name": {"Kid"}}
	rec = invitePost(t, mux, "/admin/invites", form, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d", rec.Code)
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
	rec = invitePost(t, mux2, "/admin/invites", form, token2)
	if body := rec.Body.String(); !strings.Contains(body, "Admins only") {
		t.Errorf("non-admin not refused")
	}
	req = httptest.NewRequest(http.MethodGet, "/profile", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: token2})
	rec = httptest.NewRecorder()
	mux2.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "Family") {
		t.Errorf("non-admin sees Family card")
	}
}

func TestInviteAcceptFlow(t *testing.T) {
	mux, apps, _, token := setupInviteTestServer(t, true)
	apps.invite = &identity.Invite{ID: uuid.New(), Email: "kid@example.com", ExpiresAt: time.Now().Add(time.Hour)}

	// Bad token explains itself.
	req := httptest.NewRequest(http.MethodGet, "/invite/accept?token=nope", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "invalid") {
		t.Fatalf("bad token page = %d", rec.Code)
	}

	// Good token renders the form with the email.
	req = httptest.NewRequest(http.MethodGet, "/invite/accept?token=test-token-123", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, "kid@example.com") {
		t.Errorf("accept page missing email")
	}

	// Mismatched passwords re-render with an error.
	form := url.Values{"token": {"test-token-123"}, "username": {"kid"}, "display_name": {"Kid"}, "password": {"secret-123"}, "confirm_password": {"other"}}
	rec = invitePost(t, mux, "/invite/accept", form, token)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "do not match") {
		t.Fatalf("mismatch = %d", rec.Code)
	}

	// Accept signs straight in.
	form.Set("confirm_password", "secret-123")
	rec = invitePost(t, mux, "/invite/accept", form, token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("accept = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/profile?success=invite" {
		t.Errorf("redirect = %q", loc)
	}
	if rec.Header().Get("Set-Cookie") == "" {
		t.Errorf("no session cookie set")
	}

	// Revoke burns the token.
	form = url.Values{"_csrf": {"test-csrf-token"}, "id": {apps.invite.ID.String()}}
	rec = invitePost(t, mux, "/admin/invites/revoke", form, token)
	if rec.Code != http.StatusSeeOther || len(apps.revoked) != 1 {
		t.Errorf("revoke = %d, revoked %v", rec.Code, apps.revoked)
	}
}

func TestProfileUsernameUpdate(t *testing.T) {
	mux, userSvc, _, _, token := setupProfileTestServer(t)

	csrfToken := "test-csrf-token"
	form := url.Values{
		"_csrf":        {csrfToken},
		"display_name": {"Test User"},
		"username":     {"tester"},
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
	if userSvc.lastUsername != "tester" {
		t.Errorf("username not saved, got %q", userSvc.lastUsername)
	}
}
