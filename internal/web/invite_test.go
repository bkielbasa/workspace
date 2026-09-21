package web_test

import (
	"context"
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
	sessSvc := newProfileMockSessionService(userID)
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
	if loc := rec.Header().Get("Location"); loc != "/settings?section=profile&success=invite" {
		t.Errorf("redirect = %q", loc)
	}
	if rec.Header().Get("Set-Cookie") == "" {
		t.Errorf("no session cookie set")
	}

	// Revoke burns the token.
	form = url.Values{"_csrf": {"test-csrf-token"}, "id": {apps.invite.ID.String()}}
	rec = invitePost(t, mux, "/settings/invites/revoke", form, token)
	if rec.Code != http.StatusSeeOther || len(apps.revoked) != 1 {
		t.Errorf("revoke = %d, revoked %v", rec.Code, apps.revoked)
	}
}

type testWebServer struct {
	mux    *http.ServeMux
	server *web.Server
}

type mockInvitesService struct {
	invites map[string]*identity.Invite
}

func (m *mockInvitesService) CreateInvite(ctx context.Context, email, displayName string) (string, *identity.Invite, error) {
	token := "tok-" + uuid.New().String()
	inv := &identity.Invite{
		ID:           uuid.New(),
		InvitedEmail: email,
		Email:        email,
		DisplayName:  displayName,
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}
	m.invites[token] = inv
	return token, inv, nil
}

func (m *mockInvitesService) Lookup(ctx context.Context, token string) (*identity.Invite, error) {
	inv, ok := m.invites[token]
	if !ok {
		return nil, identity.ErrInviteNotFound
	}
	return inv, nil
}

func (m *mockInvitesService) Accept(ctx context.Context, token, username, displayName, password string) (*identity.User, error) {
	if _, ok := m.invites[token]; !ok {
		return nil, identity.ErrInviteNotFound
	}
	u := &identity.User{
		ID:          uuid.New(),
		Email:       username + "@cloudlift.run",
		Username:    username,
		DisplayName: displayName,
		Enabled:     true,
	}
	return u, nil
}

func (m *mockInvitesService) List(ctx context.Context) ([]identity.Invite, error) {
	var list []identity.Invite
	for _, inv := range m.invites {
		list = append(list, *inv)
	}
	return list, nil
}

func (m *mockInvitesService) Revoke(ctx context.Context, id uuid.UUID) error {
	for k, inv := range m.invites {
		if inv.ID == id {
			delete(m.invites, k)
			break
		}
	}
	return nil
}

type mockUsersService struct {
	profileMockUserService
	users map[uuid.UUID]*identity.User
}

func (m *mockUsersService) Get(ctx context.Context, id uuid.UUID) (*identity.User, error) {
	if u, ok := m.users[id]; ok {
		return u, nil
	}
	return m.profileMockUserService.Get(ctx, id)
}

func (m *mockUsersService) Update(ctx context.Context, id uuid.UUID, displayName string, enabled bool) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.lastUpdatedName = displayName
	if u, ok := m.users[id]; ok {
		u.DisplayName = displayName
	} else if m.user != nil {
		m.user.DisplayName = displayName
	}
	return nil
}

func (m *mockUsersService) Authenticate(ctx context.Context, email, password string) (*identity.User, error) {
	for _, u := range m.users {
		if u.Email == email || u.Username == email {
			return u, nil
		}
	}
	return m.profileMockUserService.Authenticate(ctx, email, password)
}

func newTestServerWithInvites(t *testing.T) (*testWebServer, *mockInvitesService, *mockUsersService, *profileMockSessionService) {
	t.Helper()
	files := os.DirFS("../..")
	mockInvites := &mockInvitesService{invites: make(map[string]*identity.Invite)}
	mockUsers := &mockUsersService{users: make(map[uuid.UUID]*identity.User)}
	sessSvc := newProfileMockSessionService(uuid.New())

	server, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessSvc, mockUsers, false)
	if err != nil {
		t.Fatalf("web.New() error = %v", err)
	}
	server.SetInvites(mockInvites)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	return &testWebServer{mux: mux, server: server}, mockInvites, mockUsers, sessSvc
}

func TestInviteAcceptanceWithUsername(t *testing.T) {
	srv, mockInvites, _, _ := newTestServerWithInvites(t)
	// Mock invite with token "tok123"
	mockInvites.invites["tok123"] = &identity.Invite{
		InvitedEmail: "guest@example.com",
		DisplayName:  "Guest",
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}

	// 1. GET /invite/accept?token=tok123 renders username input
	req := httptest.NewRequest("GET", "/invite/accept?token=tok123", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /invite/accept failed: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="username"`) {
		t.Errorf("expected invite form to have username field")
	}

	// 2. POST /invite/accept with username
	form := url.Values{
		"token":            {"tok123"},
		"username":         {"guestuser"},
		"display_name":     {"Guest User"},
		"password":         {"password123"},
		"confirm_password": {"password123"},
	}
	req = httptest.NewRequest("POST", "/invite/accept", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/settings?section=profile&success=invite" {
		t.Errorf("expected redirect to /settings?section=profile&success=invite, got %q", loc)
	}
}
