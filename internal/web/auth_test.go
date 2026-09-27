package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

type mockAuthUsers struct {
	users map[string]*identity.User
}

func (m *mockAuthUsers) Authenticate(_ context.Context, login, password string) (*identity.User, error) {
	u, ok := m.users[login]
	if !ok || password != "secret123" {
		return nil, identity.ErrUserNotFound
	}
	return u, nil
}

func (m *mockAuthUsers) Get(context.Context, uuid.UUID) (*identity.User, error)   { return nil, nil }
func (m *mockAuthUsers) GetByEmail(_ context.Context, email string) (*identity.User, error) {
	u, ok := m.users[email]
	if !ok {
		return nil, identity.ErrUserNotFound
	}
	return u, nil
}
func (m *mockAuthUsers) CreateWithOrg(_ context.Context, email, name string, orgID uuid.UUID) (*identity.User, error) {
	u := &identity.User{ID: uuid.New(), Email: email, DisplayName: name, OrganizationID: &orgID, Enabled: true}
	m.users[email] = u
	return u, nil
}
func (m *mockAuthUsers) SetOrganization(_ context.Context, userID, orgID uuid.UUID) error {
	for _, u := range m.users {
		if u.ID == userID {
			u.OrganizationID = &orgID
			return nil
		}
	}
	return nil
}
func (m *mockAuthUsers) Update(context.Context, uuid.UUID, string, bool) error    { return nil }
func (m *mockAuthUsers) ChangePassword(context.Context, uuid.UUID, string) error { return nil }
func (m *mockAuthUsers) List(context.Context) ([]identity.User, error)            { return nil, nil }
func (m *mockAuthUsers) Delete(context.Context, uuid.UUID) error                  { return nil }
func (m *mockAuthUsers) SetUsername(context.Context, uuid.UUID, string) error    { return nil }

func setupTestServer(t *testing.T) (*web.Server, *http.ServeMux, *mockAuthUsers, *identity.Organizations) {
	t.Helper()
	users := &mockAuthUsers{
		users: make(map[string]*identity.User),
	}
	orgRepo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(orgRepo)

	files := os.DirFS("../..")
	srv, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessionService{}, users, false)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	srv.SetOrganizations(orgs)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)

	return srv, mux, users, orgs
}

func TestLogin_SSOEnforced(t *testing.T) {
	ctx := context.Background()
	_, mux, users, orgs := setupTestServer(t)

	// Create org Acme Corp with domain acme.corp and enforced SSO
	ownerID := uuid.New()
	org, _, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("failed to create org: %v", err)
	}

	err = orgs.SaveSSO(ctx, &identity.OrganizationSSO{
		OrganizationID: org.ID,
		Name:           "Acme Okta",
		Issuer:         "https://acme.okta.com",
		ClientID:       "client-id",
		ClientSecret:   "client-secret",
		Scopes:         []string{"openid", "email"},
		EnforceSSO:     true,
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("failed to save sso: %v", err)
	}

	users.users["bob@acme.corp"] = &identity.User{
		ID:          uuid.New(),
		Email:       "bob@acme.corp",
		Username:    "bob",
		Enabled:     true,
	}

	// Attempt login with bob@acme.corp
	body, _ := json.Marshal(map[string]string{
		"email":    "bob@acme.corp",
		"password": "secret123",
	})
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 Forbidden, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var res map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if res["error"] != "sso_required" {
		t.Errorf("expected error 'sso_required', got %q", res["error"])
	}
	expectedRedirect := "/login/sso?domain=acme.corp"
	if res["redirect_url"] != expectedRedirect {
		t.Errorf("expected redirect_url %q, got %q", expectedRedirect, res["redirect_url"])
	}
	if res["message"] != "Your organization requires Single Sign-On." {
		t.Errorf("expected message 'Your organization requires Single Sign-On.', got %q", res["message"])
	}
}

func TestLogin_SSOOptional(t *testing.T) {
	ctx := context.Background()
	_, mux, users, orgs := setupTestServer(t)

	// Create org Beta Corp with domain beta.io and optional SSO (EnforceSSO = false)
	ownerID := uuid.New()
	org, _, err := orgs.Create(ctx, ownerID, "Beta Corp", "beta.io")
	if err != nil {
		t.Fatalf("failed to create org: %v", err)
	}

	err = orgs.SaveSSO(ctx, &identity.OrganizationSSO{
		OrganizationID: org.ID,
		Name:           "Beta SSO",
		Issuer:         "https://beta.sso.io",
		ClientID:       "client-id",
		ClientSecret:   "client-secret",
		Scopes:         []string{"openid", "email"},
		EnforceSSO:     false,
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("failed to save sso: %v", err)
	}

	users.users["carol@beta.io"] = &identity.User{
		ID:       uuid.New(),
		Email:    "carol@beta.io",
		Username: "carol",
		Enabled:  true,
	}

	body, _ := json.Marshal(map[string]string{
		"email":    "carol@beta.io",
		"password": "secret123",
	})
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestLogin_PersonalUser(t *testing.T) {
	_, mux, users, _ := setupTestServer(t)

	users.users["alice@cloudlift.run"] = &identity.User{
		ID:       uuid.New(),
		Email:    "alice@cloudlift.run",
		Username: "alice",
		Enabled:  true,
	}

	body, _ := json.Marshal(map[string]string{
		"email":    "alice@cloudlift.run",
		"password": "secret123",
	})
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}
