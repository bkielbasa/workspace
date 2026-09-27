package web_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

type mockOrgSSOUsers struct {
	mu    sync.Mutex
	users map[string]*identity.User // key: email
}

func newMockOrgSSOUsers() *mockOrgSSOUsers {
	return &mockOrgSSOUsers{users: make(map[string]*identity.User)}
}

func (m *mockOrgSSOUsers) Authenticate(_ context.Context, login, password string) (*identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[login]
	if !ok {
		return nil, identity.ErrUserNotFound
	}
	return u, nil
}

func (m *mockOrgSSOUsers) Get(_ context.Context, id uuid.UUID) (*identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, identity.ErrUserNotFound
}

func (m *mockOrgSSOUsers) GetByEmail(_ context.Context, email string) (*identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Email, email) {
			return u, nil
		}
	}
	return nil, identity.ErrUserNotFound
}

func (m *mockOrgSSOUsers) CreateWithOrg(_ context.Context, email, name string, orgID uuid.UUID) (*identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Email, email) {
			return nil, identity.ErrUserAlreadyExists
		}
	}
	u := &identity.User{
		ID:             uuid.New(),
		Email:          email,
		DisplayName:    name,
		OrganizationID: &orgID,
		Enabled:        true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	m.users[email] = u
	return u, nil
}

func (m *mockOrgSSOUsers) SetOrganization(_ context.Context, userID, orgID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == userID {
			u.OrganizationID = &orgID
			return nil
		}
	}
	return identity.ErrUserNotFound
}

func (m *mockOrgSSOUsers) Update(context.Context, uuid.UUID, string, bool) error    { return nil }
func (m *mockOrgSSOUsers) ChangePassword(context.Context, uuid.UUID, string) error { return nil }
func (m *mockOrgSSOUsers) List(context.Context) ([]identity.User, error)            { return nil, nil }
func (m *mockOrgSSOUsers) Delete(context.Context, uuid.UUID) error                  { return nil }
func (m *mockOrgSSOUsers) SetUsername(context.Context, uuid.UUID, string) error    { return nil }

func setupOrgSSOServer(t *testing.T, users *mockOrgSSOUsers, orgs *identity.Organizations) *httptest.Server {
	t.Helper()
	files := os.DirFS("../..")
	srv, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessionService{}, users, false)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	srv.SetOrganizations(orgs)
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	testSrv := httptest.NewServer(mux)
	t.Cleanup(testSrv.Close)
	return testSrv
}

func TestDynamicOrgSSOBeginsWithOrgDomain(t *testing.T) {
	ctx := context.Background()
	users := newMockOrgSSOUsers()
	orgRepo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(orgRepo)
	srv := setupOrgSSOServer(t, users, orgs)

	ownerID := uuid.New()
	org, _, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	f := newFakeIdP(t)
	err = orgs.SaveSSO(ctx, &identity.OrganizationSSO{
		OrganizationID: org.ID,
		Name:           "Acme Okta",
		Issuer:         f.issuer,
		ClientID:       f.clientID,
		ClientSecret:   "secret",
		Scopes:         []string{"openid", "profile", "email"},
		Enabled:        true,
		AutoProvision:  true,
	})
	if err != nil {
		t.Fatalf("save sso: %v", err)
	}

	client := noFollowClient()
	resp, err := client.Get(srv.URL + "/login/sso?domain=acme.corp")
	if err != nil {
		t.Fatalf("get /login/sso: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 302 or 303 redirect, got %d", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, f.issuer+"/authorize") {
		t.Fatalf("expected redirect to IdP /authorize, got: %s", loc)
	}

	parsedLoc, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	q := parsedLoc.Query()
	if q.Get("state") == "" {
		t.Errorf("missing state parameter in auth URL")
	}
	if q.Get("code_challenge") == "" {
		t.Errorf("missing code_challenge in auth URL")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("expected S256 challenge method, got %q", q.Get("code_challenge_method"))
	}

	var ssoCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "sso" {
			ssoCookie = c
			break
		}
	}
	if ssoCookie == nil {
		t.Fatalf("missing sso state cookie")
	}

	parts := strings.Split(ssoCookie.Value, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3-part org SSO cookie (state.verifier.orgID), got %d parts: %q", len(parts), ssoCookie.Value)
	}
	cookieOrgID, err := uuid.Parse(parts[2])
	if err != nil {
		t.Fatalf("cookie orgID invalid uuid: %v", err)
	}
	if cookieOrgID != org.ID {
		t.Fatalf("cookie orgID mismatch: got %v, want %v", cookieOrgID, org.ID)
	}
}

func TestOrgSSOCallbackJITProvisionsMember(t *testing.T) {
	ctx := context.Background()
	users := newMockOrgSSOUsers()
	orgRepo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(orgRepo)
	srv := setupOrgSSOServer(t, users, orgs)

	ownerID := uuid.New()
	org, _, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	f := newFakeIdP(t)
	f.mu.Lock()
	f.email = "alice@acme.corp"
	f.name = "Alice Acme"
	f.idName = "Alice Acme"
	f.mu.Unlock()

	err = orgs.SaveSSO(ctx, &identity.OrganizationSSO{
		OrganizationID: org.ID,
		Name:           "Acme Okta",
		Issuer:         f.issuer,
		ClientID:       f.clientID,
		ClientSecret:   "secret",
		Scopes:         []string{"openid", "profile", "email"},
		Enabled:        true,
		AutoProvision:  true,
	})
	if err != nil {
		t.Fatalf("save sso: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// 1. Initiate login
	start, err := client.Get(srv.URL + "/login/sso?domain=acme.corp")
	if err != nil {
		t.Fatalf("start login: %v", err)
	}
	start.Body.Close()
	if start.StatusCode != http.StatusFound && start.StatusCode != http.StatusSeeOther {
		t.Fatalf("start expected redirect, got %d", start.StatusCode)
	}

	// 2. Authorize at fake IdP
	authURL, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	authorize, err := client.Get(f.srv.URL + authURL.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	authorize.Body.Close()

	// 3. Callback at workspace app
	cb, err := url.Parse(authorize.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	cbURL := srv.URL + "/login/sso/callback?" + cb.RawQuery
	resp, err := client.Get(cbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("callback expected redirect to /, got %d (location: %s)", resp.StatusCode, resp.Header.Get("Location"))
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}

	// Verify user is created with OrganizationID
	user, err := users.GetByEmail(ctx, "alice@acme.corp")
	if err != nil {
		t.Fatalf("user not created: %v", err)
	}
	if user.OrganizationID == nil {
		t.Fatalf("expected user OrganizationID to be set, got nil")
	}
	if *user.OrganizationID != org.ID {
		t.Fatalf("expected user OrganizationID = %v, got %v", org.ID, *user.OrganizationID)
	}
	if user.DisplayName != "Alice Acme" {
		t.Errorf("expected display name Alice Acme, got %q", user.DisplayName)
	}

	// Verify user is in organization_members with role member
	member, err := orgs.GetMember(ctx, org.ID, user.ID)
	if err != nil {
		t.Fatalf("member not added to org: %v", err)
	}
	if member.Role != identity.RoleMember {
		t.Errorf("expected role %q, got %q", identity.RoleMember, member.Role)
	}

	// Verify session cookie was set
	var sessionCookie *http.Cookie
	for _, c := range jar.Cookies(cb) {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		for _, c := range resp.Cookies() {
			if c.Name == "session" {
				sessionCookie = c
				break
			}
		}
	}
	if sessionCookie == nil {
		t.Errorf("expected session cookie to be set")
	}
}

func TestDynamicOrgSSODomainMismatchRejected(t *testing.T) {
	ctx := context.Background()
	users := newMockOrgSSOUsers()
	orgRepo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(orgRepo)
	srv := setupOrgSSOServer(t, users, orgs)

	ownerID := uuid.New()
	org, _, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	f := newFakeIdP(t)
	f.mu.Lock()
	f.email = "alice@other.com"
	f.name = "Alice Other"
	f.idName = "Alice Other"
	f.mu.Unlock()

	err = orgs.SaveSSO(ctx, &identity.OrganizationSSO{
		OrganizationID: org.ID,
		Name:           "Acme Okta",
		Issuer:         f.issuer,
		ClientID:       f.clientID,
		ClientSecret:   "secret",
		Scopes:         []string{"openid", "profile", "email"},
		Enabled:        true,
		AutoProvision:  true,
	})
	if err != nil {
		t.Fatalf("save sso: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	start, err := client.Get(srv.URL + "/login/sso?domain=acme.corp")
	if err != nil {
		t.Fatal(err)
	}
	start.Body.Close()

	authURL, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	authorize, err := client.Get(f.srv.URL + authURL.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	authorize.Body.Close()

	cb, err := url.Parse(authorize.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	cbURL := srv.URL + "/login/sso/callback?" + cb.RawQuery
	resp, err := client.Get(cbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/login?error=") {
		t.Fatalf("expected redirect to /login with error, got location: %s (status %d)", loc, resp.StatusCode)
	}

	// Verify user alice@other.com was NOT created
	if _, err := users.GetByEmail(ctx, "alice@other.com"); err == nil {
		t.Errorf("user alice@other.com should not have been created")
	}
}

func TestDynamicOrgSSODisabledSSORejected(t *testing.T) {
	ctx := context.Background()
	users := newMockOrgSSOUsers()
	orgRepo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(orgRepo)
	srv := setupOrgSSOServer(t, users, orgs)

	ownerID := uuid.New()
	org, _, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	err = orgs.SaveSSO(ctx, &identity.OrganizationSSO{
		OrganizationID: org.ID,
		Name:           "Acme Okta",
		Issuer:         "https://idp.example.com",
		ClientID:       "client-id",
		ClientSecret:   "secret",
		Scopes:         []string{"openid", "email"},
		Enabled:        false,
		AutoProvision:  true,
	})
	if err != nil {
		t.Fatalf("save sso: %v", err)
	}

	client := noFollowClient()
	resp, err := client.Get(srv.URL + "/login/sso?domain=acme.corp")
	if err != nil {
		t.Fatalf("get /login/sso: %v", err)
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/login?error=") {
		t.Fatalf("expected redirect to /login with error for disabled SSO, got status %d, location %q", resp.StatusCode, loc)
	}
}

func TestOrgSSOCallbackExistingUserAssociatedWithOrg(t *testing.T) {
	ctx := context.Background()
	users := newMockOrgSSOUsers()
	orgRepo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(orgRepo)
	srv := setupOrgSSOServer(t, users, orgs)

	ownerID := uuid.New()
	org, _, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Bob exists before SSO, not associated with any org
	existingUser := &identity.User{
		ID:             uuid.New(),
		Email:          "bob@acme.corp",
		DisplayName:    "Bob Original",
		OrganizationID: nil,
		Enabled:        true,
	}
	users.users["bob@acme.corp"] = existingUser

	f := newFakeIdP(t)
	f.mu.Lock()
	f.email = "bob@acme.corp"
	f.name = "Bob Acme"
	f.idName = "Bob Acme"
	f.mu.Unlock()

	err = orgs.SaveSSO(ctx, &identity.OrganizationSSO{
		OrganizationID: org.ID,
		Name:           "Acme Okta",
		Issuer:         f.issuer,
		ClientID:       f.clientID,
		ClientSecret:   "secret",
		Scopes:         []string{"openid", "profile", "email"},
		Enabled:        true,
		AutoProvision:  true,
	})
	if err != nil {
		t.Fatalf("save sso: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	start, err := client.Get(srv.URL + "/login/sso?domain=acme.corp")
	if err != nil {
		t.Fatal(err)
	}
	start.Body.Close()

	authURL, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	authorize, err := client.Get(f.srv.URL + authURL.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	authorize.Body.Close()

	cb, err := url.Parse(authorize.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	cbURL := srv.URL + "/login/sso/callback?" + cb.RawQuery
	resp, err := client.Get(cbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect to /, got %d (loc: %s)", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Verify Bob now has OrganizationID set
	bob, err := users.GetByEmail(ctx, "bob@acme.corp")
	if err != nil {
		t.Fatalf("get bob: %v", err)
	}
	if bob.OrganizationID == nil || *bob.OrganizationID != org.ID {
		t.Fatalf("expected Bob's OrganizationID to be %v, got %v", org.ID, bob.OrganizationID)
	}

	// Verify Bob is added to organization_members
	member, err := orgs.GetMember(ctx, org.ID, bob.ID)
	if err != nil {
		t.Fatalf("bob not added as org member: %v", err)
	}
	if member.Role != identity.RoleMember {
		t.Errorf("expected role member, got %s", member.Role)
	}
}

func TestOrgSSOCallbackDisabledUserRejected(t *testing.T) {
	ctx := context.Background()
	users := newMockOrgSSOUsers()
	orgRepo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(orgRepo)
	srv := setupOrgSSOServer(t, users, orgs)

	ownerID := uuid.New()
	org, _, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Bob exists before SSO, but is disabled
	existingUser := &identity.User{
		ID:             uuid.New(),
		Email:          "bob@acme.corp",
		DisplayName:    "Bob Disabled",
		OrganizationID: &org.ID,
		Enabled:        false,
	}
	users.users["bob@acme.corp"] = existingUser

	f := newFakeIdP(t)
	f.mu.Lock()
	f.email = "bob@acme.corp"
	f.name = "Bob Disabled"
	f.idName = "Bob Disabled"
	f.mu.Unlock()

	err = orgs.SaveSSO(ctx, &identity.OrganizationSSO{
		OrganizationID: org.ID,
		Name:           "Acme Okta",
		Issuer:         f.issuer,
		ClientID:       f.clientID,
		ClientSecret:   "secret",
		Scopes:         []string{"openid", "profile", "email"},
		Enabled:        true,
		AutoProvision:  true,
	})
	if err != nil {
		t.Fatalf("save sso: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	start, err := client.Get(srv.URL + "/login/sso?domain=acme.corp")
	if err != nil {
		t.Fatal(err)
	}
	start.Body.Close()

	authURL, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	authorize, err := client.Get(f.srv.URL + authURL.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	authorize.Body.Close()

	cb, err := url.Parse(authorize.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	cbURL := srv.URL + "/login/sso/callback?" + cb.RawQuery
	resp, err := client.Get(cbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "error=") || !strings.Contains(loc, "disabled") {
		t.Fatalf("expected redirect to login with disabled error, got loc: %s (status %d)", loc, resp.StatusCode)
	}
}
