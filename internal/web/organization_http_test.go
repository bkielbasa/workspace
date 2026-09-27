package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

// -----------------------------------------------------------------------------
// Test Doubles
// -----------------------------------------------------------------------------

type testOrgUsers struct {
	mu    sync.Mutex
	users map[uuid.UUID]*identity.User
}

func newTestOrgUsers() *testOrgUsers {
	return &testOrgUsers{users: make(map[uuid.UUID]*identity.User)}
}

func (m *testOrgUsers) Authenticate(_ context.Context, login, password string) (*identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Email == login {
			return u, nil
		}
	}
	return nil, identity.ErrUserNotFound
}

func (m *testOrgUsers) Get(_ context.Context, id uuid.UUID) (*identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return nil, identity.ErrUserNotFound
	}
	cpy := *u
	return &cpy, nil
}

func (m *testOrgUsers) GetByEmail(_ context.Context, email string) (*identity.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if strings.EqualFold(u.Email, email) {
			cpy := *u
			return &cpy, nil
		}
	}
	return nil, identity.ErrUserNotFound
}

func (m *testOrgUsers) CreateWithOrg(_ context.Context, email, name string, orgID uuid.UUID) (*identity.User, error) {
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
	m.users[u.ID] = u
	cpy := *u
	return &cpy, nil
}

func (m *testOrgUsers) SetOrganization(_ context.Context, userID, orgID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return identity.ErrUserNotFound
	}
	u.OrganizationID = &orgID
	return nil
}

func (m *testOrgUsers) Update(context.Context, uuid.UUID, string, bool) error    { return nil }
func (m *testOrgUsers) ChangePassword(context.Context, uuid.UUID, string) error { return nil }
func (m *testOrgUsers) List(context.Context) ([]identity.User, error)            { return nil, nil }
func (m *testOrgUsers) Delete(context.Context, uuid.UUID) error                  { return nil }
func (m *testOrgUsers) SetUsername(context.Context, uuid.UUID, string) error    { return nil }

type testOrgSessions struct {
	mu       sync.Mutex
	sessions map[string]*identity.Session
}

func newTestOrgSessions() *testOrgSessions {
	return &testOrgSessions{sessions: make(map[string]*identity.Session)}
}

func (s *testOrgSessions) Create(_ context.Context, userID uuid.UUID, ttl time.Duration) (*identity.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := &identity.Session{
		UserID:    userID,
		Token:     uuid.New().String(),
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(ttl),
	}
	s.sessions[sess.Token] = sess
	return sess, nil
}

func (s *testOrgSessions) GetByToken(_ context.Context, token string) (*identity.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return nil, identity.ErrSessionNotFound
	}
	return sess, nil
}

func (s *testOrgSessions) Delete(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
	return nil
}

type testDomainVerifier struct {
	mu       sync.Mutex
	verified map[uuid.UUID]bool
	failWith error
}

func (v *testDomainVerifier) Verify(_ context.Context, domainID uuid.UUID) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failWith != nil {
		return v.failWith
	}
	v.verified[domainID] = true
	return nil
}

// -----------------------------------------------------------------------------
// Test Harness
// -----------------------------------------------------------------------------

type orgTestHarness struct {
	server   *httptest.Server
	users    *testOrgUsers
	sessions *testOrgSessions
	orgs     *identity.Organizations
	orgRepo  identity.OrganizationRepository
	verifier *testDomainVerifier
}

func setupOrgTestHarness(t *testing.T) *orgTestHarness {
	t.Helper()
	users := newTestOrgUsers()
	sessions := newTestOrgSessions()
	orgRepo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(orgRepo)
	verifier := &testDomainVerifier{verified: make(map[uuid.UUID]bool)}

	files := os.DirFS("../..")
	srv, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessions, users, false)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	srv.SetOrganizations(orgs)
	srv.SetDomainVerifier(verifier)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return &orgTestHarness{
		server:   ts,
		users:    users,
		sessions: sessions,
		orgs:     orgs,
		orgRepo:  orgRepo,
		verifier: verifier,
	}
}

func (h *orgTestHarness) createAuthenticatedUser(t *testing.T, email, name string, orgID *uuid.UUID) (*identity.User, string) {
	t.Helper()
	user := &identity.User{
		ID:             uuid.New(),
		Email:          email,
		DisplayName:    name,
		OrganizationID: orgID,
		Enabled:        true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	h.users.mu.Lock()
	h.users.users[user.ID] = user
	h.users.mu.Unlock()

	sess, err := h.sessions.Create(context.Background(), user.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return user, sess.Token
}

func (h *orgTestHarness) makeRequest(method, path string, token string, form url.Values, jsonBody any) (*http.Response, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects automatically
		},
	}

	var req *http.Request
	var err error
	targetURL := h.server.URL + path

	if jsonBody != nil {
		b, _ := json.Marshal(jsonBody)
		req, err = http.NewRequest(method, targetURL, strings.NewReader(string(b)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
	} else if form != nil {
		form.Set("_csrf", "csrf-token-123")
		req, err = http.NewRequest(method, targetURL, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req, err = http.NewRequest(method, targetURL, nil)
		if err != nil {
			return nil, err
		}
	}

	// Always add CSRF cookie and header for test requests
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "csrf-token-123"})
	req.Header.Set("X-CSRF-Token", "csrf-token-123")

	if token != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
	}

	return client.Do(req)
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func TestCreateOrganizationFromSettings(t *testing.T) {
	h := setupOrgTestHarness(t)
	user, token := h.createAuthenticatedUser(t, "alice@example.com", "Alice", nil)

	// Step A: Initial GET /settings/organization when user has no org shows new org view
	resp, err := h.makeRequest("GET", "/settings/organization", token, nil, nil)
	if err != nil {
		t.Fatalf("GET /settings/organization failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for GET /settings/organization, got %d", resp.StatusCode)
	}

	// Step B: POST /settings/organization with name "Beta Corp" and domain "beta.io"
	form := url.Values{
		"name":   {"Beta Corp"},
		"domain": {"beta.io"},
	}
	resp, err = h.makeRequest("POST", "/settings/organization", token, form, nil)
	if err != nil {
		t.Fatalf("POST /settings/organization failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected redirect (303) or 200, got %d", resp.StatusCode)
	}

	// Verify organization was created
	ctx := context.Background()
	org, dom, err := h.orgs.GetByDomain(ctx, "beta.io")
	if err != nil || org == nil || dom == nil {
		t.Fatalf("expected org and domain created for beta.io: %v", err)
	}
	if org.Name != "Beta Corp" {
		t.Errorf("expected org name 'Beta Corp', got '%s'", org.Name)
	}
	if dom.Domain != "beta.io" {
		t.Errorf("expected domain 'beta.io', got '%s'", dom.Domain)
	}
	if dom.VerificationToken == "" {
		t.Errorf("expected non-empty domain verification token")
	}

	// Verify user's organization was updated
	freshUser, err := h.users.Get(ctx, user.ID)
	if err != nil || freshUser.OrganizationID == nil {
		t.Fatalf("expected user OrganizationID to be updated, got %v", freshUser.OrganizationID)
	}
	if *freshUser.OrganizationID != org.ID {
		t.Errorf("expected user OrganizationID %s, got %s", org.ID, *freshUser.OrganizationID)
	}

	// Verify user is an owner member of the organization
	member, err := h.orgs.GetMember(ctx, org.ID, user.ID)
	if err != nil || member == nil {
		t.Fatalf("expected user to be a member of org: %v", err)
	}
	if member.Role != identity.RoleOwner {
		t.Errorf("expected role %s, got %s", identity.RoleOwner, member.Role)
	}

	// Step C: Subsequent GET /settings/organization renders the organization page
	resp, err = h.makeRequest("GET", "/settings/organization", token, nil, nil)
	if err != nil {
		t.Fatalf("GET /settings/organization after creation failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for GET /settings/organization, got %d", resp.StatusCode)
	}
}

func TestTriggerDomainVerification(t *testing.T) {
	h := setupOrgTestHarness(t)
	ctx := context.Background()

	owner, token := h.createAuthenticatedUser(t, "owner@beta.io", "Owner", nil)
	org, dom, err := h.orgs.Create(ctx, owner.ID, "Beta Corp", "beta.io")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := h.users.SetOrganization(ctx, owner.ID, org.ID); err != nil {
		t.Fatalf("set org: %v", err)
	}

	// Verify domain starts unverified
	if dom.VerifiedAt != nil {
		t.Fatalf("expected domain to start unverified")
	}

	// POST /settings/organization/domains/{id}/verify
	verifyPath := "/settings/organization/domains/" + dom.ID.String() + "/verify"
	resp, err := h.makeRequest("POST", verifyPath, token, url.Values{}, nil)
	if err != nil {
		t.Fatalf("POST verify failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 303 redirect or 200 OK, got %d", resp.StatusCode)
	}

	// Verify DomainVerifier was invoked
	h.verifier.mu.Lock()
	verified := h.verifier.verified[dom.ID]
	h.verifier.mu.Unlock()
	if !verified {
		t.Errorf("expected DomainVerifier.Verify to have been called for domain %s", dom.ID)
	}

	// Test verifier failure behavior
	h.verifier.failWith = errors.New("DNS TXT verification record not found")
	respFail, err := h.makeRequest("POST", verifyPath, token, url.Values{}, nil)
	if err != nil {
		t.Fatalf("POST verify with failure failed: %v", err)
	}
	defer respFail.Body.Close()

	// Redirect should contain error query param or return error status
	if respFail.StatusCode == http.StatusSeeOther {
		loc := respFail.Header.Get("Location")
		if !strings.Contains(loc, "error=") {
			t.Errorf("expected error in redirect Location, got %s", loc)
		}
	} else if respFail.StatusCode < 400 {
		t.Errorf("expected error status code or redirect with error, got %d", respFail.StatusCode)
	}
}

func TestUpdateSSOConfiguration(t *testing.T) {
	h := setupOrgTestHarness(t)
	ctx := context.Background()

	owner, token := h.createAuthenticatedUser(t, "admin@beta.io", "Admin", nil)
	org, _, err := h.orgs.Create(ctx, owner.ID, "Beta Corp", "beta.io")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := h.users.SetOrganization(ctx, owner.ID, org.ID); err != nil {
		t.Fatalf("set org: %v", err)
	}

	// POST /settings/organization/sso with OIDC settings
	form := url.Values{
		"name":           {"Google Workspace"},
		"issuer":         {"https://accounts.google.com"},
		"client_id":      {"google-client-id-xyz"},
		"client_secret":  {"google-client-secret-123"},
		"scopes":         {"openid, profile, email"},
		"enforce_sso":    {"true"},
		"auto_provision": {"true"},
		"enabled":        {"true"},
	}

	resp, err := h.makeRequest("POST", "/settings/organization/sso", token, form, nil)
	if err != nil {
		t.Fatalf("POST SSO update failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 303 redirect or 200 OK, got %d", resp.StatusCode)
	}

	// Verify SSO configuration was saved
	sso, err := h.orgs.GetSSO(ctx, org.ID)
	if err != nil || sso == nil {
		t.Fatalf("expected SSO config found: %v", err)
	}
	if sso.Name != "Google Workspace" {
		t.Errorf("expected name 'Google Workspace', got '%s'", sso.Name)
	}
	if sso.Issuer != "https://accounts.google.com" {
		t.Errorf("expected issuer 'https://accounts.google.com', got '%s'", sso.Issuer)
	}
	if sso.ClientID != "google-client-id-xyz" {
		t.Errorf("expected client_id 'google-client-id-xyz', got '%s'", sso.ClientID)
	}
	if sso.ClientSecret != "google-client-secret-123" {
		t.Errorf("expected client_secret 'google-client-secret-123', got '%s'", sso.ClientSecret)
	}
	if !sso.EnforceSSO {
		t.Errorf("expected EnforceSSO to be true")
	}
	if !sso.AutoProvision {
		t.Errorf("expected AutoProvision to be true")
	}
	if !sso.Enabled {
		t.Errorf("expected Enabled to be true")
	}
}

func TestOrganizationPermissions(t *testing.T) {
	h := setupOrgTestHarness(t)
	ctx := context.Background()

	// Create owner and organization
	owner, ownerToken := h.createAuthenticatedUser(t, "owner@beta.io", "Owner", nil)
	org, dom, err := h.orgs.Create(ctx, owner.ID, "Beta Corp", "beta.io")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := h.users.SetOrganization(ctx, owner.ID, org.ID); err != nil {
		t.Fatalf("set org: %v", err)
	}

	// Create regular member (RoleMember) in the organization
	memberUser, memberToken := h.createAuthenticatedUser(t, "member@beta.io", "Member", &org.ID)
	if err := h.orgs.AddMember(ctx, org.ID, memberUser.ID, identity.RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}

	verifyPath := "/settings/organization/domains/" + dom.ID.String() + "/verify"

	// Case 1: Unauthorized user (no token / session) receives 401 Unauthorized
	resp, err := h.makeRequest("POST", "/settings/organization/domains", "", url.Values{"domain": {"extra.io"}}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthorized domain add, got %d", resp.StatusCode)
	}

	resp, err = h.makeRequest("POST", "/settings/organization/sso", "", url.Values{"issuer": {"https://idp.com"}}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthorized SSO update, got %d", resp.StatusCode)
	}

	resp, err = h.makeRequest("POST", verifyPath, "", url.Values{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthorized domain verify, got %d", resp.StatusCode)
	}

	// Case 2: Non-admin user (role = "member") receives 403 Forbidden
	resp, err = h.makeRequest("POST", "/settings/organization/domains", memberToken, url.Values{"domain": {"extra.io"}}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for member creating domain, got %d", resp.StatusCode)
	}

	resp, err = h.makeRequest("POST", "/settings/organization/sso", memberToken, url.Values{"issuer": {"https://idp.com"}}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for member updating SSO, got %d", resp.StatusCode)
	}

	resp, err = h.makeRequest("POST", verifyPath, memberToken, url.Values{}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for member verifying domain, got %d", resp.StatusCode)
	}

	resp, err = h.makeRequest("POST", "/settings/organization/members/invite", memberToken, url.Values{"email": {"new@beta.io"}}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for member inviting member, got %d", resp.StatusCode)
	}

	// Case 3: Admin / Owner CAN mutate domains, SSO, and invite members
	resp, err = h.makeRequest("POST", "/settings/organization/domains", ownerToken, url.Values{"domain": {"extra.io"}}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusOK {
		t.Errorf("expected 303 or 200 for owner creating domain, got %d", resp.StatusCode)
	}

	// Verify extra domain was created
	domains, err := h.orgs.ListDomains(ctx, org.ID)
	if err != nil || len(domains) != 2 {
		t.Errorf("expected 2 domains, got %d (err: %v)", len(domains), err)
	}

	// Test owner inviting member
	resp, err = h.makeRequest("POST", "/settings/organization/members/invite", ownerToken, url.Values{
		"email": {"colleague@beta.io"},
		"role":  {"member"},
	}, nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusOK {
		t.Errorf("expected 303 or 200 for owner inviting member, got %d", resp.StatusCode)
	}
}

func TestNonAdminCannotSeeSecretOrVerificationAction(t *testing.T) {
	h := setupOrgTestHarness(t)
	ctx := context.Background()

	owner, _ := h.createAuthenticatedUser(t, "owner@beta.io", "Owner", nil)
	org, _, err := h.orgs.Create(ctx, owner.ID, "Beta Corp", "beta.io")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := h.users.SetOrganization(ctx, owner.ID, org.ID); err != nil {
		t.Fatalf("set org: %v", err)
	}

	secret := "super-secret-oidc-token-xyz"
	err = h.orgs.SaveSSO(ctx, &identity.OrganizationSSO{
		OrganizationID: org.ID,
		Name:           "Beta SSO",
		Issuer:         "https://idp.beta.io",
		ClientID:       "beta-client-id",
		ClientSecret:   secret,
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("save sso: %v", err)
	}

	memberUser, memberToken := h.createAuthenticatedUser(t, "member@beta.io", "Member", &org.ID)
	if err := h.orgs.AddMember(ctx, org.ID, memberUser.ID, identity.RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}

	resp, err := h.makeRequest("GET", "/settings/organization", memberToken, nil, nil)
	if err != nil {
		t.Fatalf("GET /settings/organization failed: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(bodyBytes)

	if strings.Contains(body, secret) {
		t.Errorf("expected secret %q NOT to appear in HTML for non-admin member, but it was found", secret)
	}
	if strings.Contains(body, "Verify DNS") {
		t.Errorf("expected 'Verify DNS' button NOT to appear in HTML for non-admin member, but it was found")
	}
}

func TestUpdateSSOConfiguration_RetainExistingSecret(t *testing.T) {
	h := setupOrgTestHarness(t)
	ctx := context.Background()

	owner, token := h.createAuthenticatedUser(t, "admin@beta.io", "Admin", nil)
	org, _, err := h.orgs.Create(ctx, owner.ID, "Beta Corp", "beta.io")
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := h.users.SetOrganization(ctx, owner.ID, org.ID); err != nil {
		t.Fatalf("set org: %v", err)
	}

	initialSecret := "initial-secret-12345"
	err = h.orgs.SaveSSO(ctx, &identity.OrganizationSSO{
		OrganizationID: org.ID,
		Name:           "Beta SSO",
		Issuer:         "https://idp.beta.io",
		ClientID:       "beta-client-id",
		ClientSecret:   initialSecret,
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("save initial sso: %v", err)
	}

	// Update SSO without passing client_secret (empty string)
	form := url.Values{
		"name":          {"Updated Beta SSO"},
		"issuer":        {"https://idp.beta.io/v2"},
		"client_id":     {"beta-client-id-updated"},
		"client_secret": {""},
		"enabled":       {"true"},
	}

	resp, err := h.makeRequest("POST", "/settings/organization/sso", token, form, nil)
	if err != nil {
		t.Fatalf("POST SSO update failed: %v", err)
	}
	defer resp.Body.Close()

	sso, err := h.orgs.GetSSO(ctx, org.ID)
	if err != nil {
		t.Fatalf("GetSSO failed: %v", err)
	}
	if sso.ClientSecret != initialSecret {
		t.Errorf("expected retained secret %q, got %q", initialSecret, sso.ClientSecret)
	}
	if sso.Name != "Updated Beta SSO" {
		t.Errorf("expected name 'Updated Beta SSO', got %q", sso.Name)
	}
}
