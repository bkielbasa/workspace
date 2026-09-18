package web_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
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

// noFollowClient never follows redirects, so tests can inspect each hop.
func noFollowClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// testSSOService records what the SSO handler claims about the sign-in and
// resolves it to a fixed user, standing in for identity.SSO.
type testSSOService struct {
	mu         sync.Mutex
	provider   string
	subject    string
	email      string
	display    string
	autoCreate bool
	user       *identity.User
}

func (s *testSSOService) Authenticate(_ context.Context, provider, subject, email, displayName string, autoCreate bool) (*identity.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider, s.subject, s.email, s.display, s.autoCreate = provider, subject, email, displayName, autoCreate
	return s.user, nil
}

// fakeIdP is a minimal OIDC provider serving discovery, JWKS, the
// authorization endpoint, the token endpoint and userinfo, all self-contained
// on one httptest server.
type fakeIdP struct {
	srv       *httptest.Server
	key       *rsa.PrivateKey
	issuer    string
	clientID  string
	mu        sync.Mutex
	nonce     string
	challenge string
	email     string
	verified  bool
	sub       string
	name      string
	idName    string // mirrors name unless set to "" to drop the id_token claim
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: key, email: "alice@example.com", verified: true, sub: "subj-123", name: "Alice Example", idName: "Alice Example"}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	f.issuer = f.srv.URL
	f.clientID = "workspace-client"
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.issuer,
			"authorization_endpoint":                f.issuer + "/authorize",
			"token_endpoint":                        f.issuer + "/token",
			"jwks_uri":                              f.issuer + "/jwks",
			"userinfo_endpoint":                     f.issuer + "/userinfo",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"code_challenge_methods_supported":      []string{"S256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := key.N.FillBytes(make([]byte, key.Size()))
		e := big.NewInt(int64(key.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(n),
				"e": base64.RawURLEncoding.EncodeToString(e),
			}},
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		challenge := r.URL.Query().Get("code_challenge")
		redirect := r.URL.Query().Get("redirect_uri")
		f.mu.Lock()
		f.nonce, f.challenge = r.URL.Query().Get("nonce"), challenge
		f.mu.Unlock()
		if redirect == "" || state == "" {
			http.Error(w, "missing redirect_uri or state", http.StatusBadRequest)
			return
		}
		v := url.Values{}
		v.Set("code", "test-code")
		v.Set("state", state)
		http.Redirect(w, r, redirect+"?"+v.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.FormValue("code") != "test-code" {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-test", "token_type": "Bearer", "expires_in": 3600,
			"id_token": f.signIDToken(t),
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sub": f.sub, "email": f.email, "email_verified": f.verified, "name": f.name,
		})
	})
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) signIDToken(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().Unix()
	claims := map[string]any{
		"iss": f.issuer, "sub": f.sub, "aud": f.clientID,
		"exp": now + 3600, "iat": now, "nonce": f.nonce,
		"email": f.email, "email_verified": f.verified,
	}
	if f.idName != "" {
		claims["name"] = f.idName
	}
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-key"}
	seg := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := seg(header) + "." + seg(claims)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// newSSOServer spins up the app handler with OIDC wired to f.
func newSSOServer(t *testing.T, f *fakeIdP, svc *testSSOService, idp web.OIDCProvider) *httptest.Server {
	t.Helper()
	files := os.DirFS("../..")
	server, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{},
		testSessionService{userID: svc.user.ID}, testUserService{userID: svc.user.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	if idp.Issuer == "" {
		idp.Issuer = f.issuer
	}
	if idp.ClientID == "" {
		idp.ClientID = f.clientID
	}
	if idp.ClientSecret == "" {
		idp.ClientSecret = "secret"
	}
	if idp.RedirectURL == "" {
		idp.RedirectURL = "http://localhost/callback"
	}
	if idp.Name == "" {
		idp.Name = "Authentik"
	}
	// Provisioning is the default when the env wiring is missing: the
	// callers in main.go always resolve this from OIDC_AUTO_CREATE.
	idp.AutoCreate = true
	if err := server.SetOIDC(idp, svc); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// walkLoginFlow drives the whole dance with a jarred client and returns the
// callback hop so tests can assert on it.
func walkLoginFlow(t *testing.T, srv, f *httptest.Server) *http.Response {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	start, err := client.Get(srv.URL + "/login/sso")
	if err != nil {
		t.Fatal(err)
	}
	start.Body.Close()
	authURL, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	authorize, err := client.Get(f.URL + authURL.RequestURI())
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
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestSSOLoginPageShowsButton(t *testing.T) {
	f := newFakeIdP(t)
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com", Enabled: true}
	srv := newSSOServer(t, f, &testSSOService{user: user}, web.OIDCProvider{})

	res, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "Sign in with Authentik") {
		t.Fatalf("login page missing SSO button:\n%s", body)
	}
	if !strings.Contains(string(body), "/login/sso") {
		t.Fatalf("login page missing SSO link:\n%s", body)
	}
}

func TestSSOSignInFlow(t *testing.T) {
	f := newFakeIdP(t)
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com", Enabled: true}
	svc := &testSSOService{user: user}
	srv := newSSOServer(t, f, svc, web.OIDCProvider{})

	// Start the dance and inspect the provider-bound hop.
	client := noFollowClient()
	start, err := client.Get(srv.URL + "/login/sso")
	if err != nil {
		t.Fatal(err)
	}
	start.Body.Close()
	if start.StatusCode != http.StatusFound {
		t.Fatalf("start status = %d, want 302", start.StatusCode)
	}
	authURL, _ := url.Parse(start.Header.Get("Location"))
	if authURL.Path != "/authorize" {
		t.Fatalf("redirected to %q, want /authorize", authURL.Path)
	}
	q := authURL.Query()
	if q.Get("response_type") != "code" || q.Get("code_challenge") == "" ||
		q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" || q.Get("state") == "" {
		t.Fatalf("authorize params wrong: %v", q)
	}
	if authURL.Host != f.srv.URL[len("http://"):] {
		t.Fatalf("start redirected host %q, want fake provider", authURL.Host)
	}
	state := q.Get("state")
	var ssoCookie *http.Cookie
	for _, c := range start.Cookies() {
		if c.Name == "sso" {
			ssoCookie = c
		}
	}
	if ssoCookie == nil || !ssoCookie.HttpOnly || ssoCookie.Path != "/login/sso/callback" {
		t.Fatalf("missing/odd sso cookie: %+v", start.Cookies())
	}

	// Round-trip through the fake provider's authorize endpoint.
	authorize, err := client.Get(f.srv.URL + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	authorize.Body.Close()
	cb, _ := url.Parse(authorize.Header.Get("Location"))
	if cb.Query().Get("code") != "test-code" || cb.Query().Get("state") != state {
		t.Fatalf("callback params wrong: %v", cb.Query())
	}

	// Arrive at the callback like the browser would.
	cbReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/login/sso/callback?"+cb.RawQuery, nil)
	cbReq.AddCookie(ssoCookie)
	done, err := client.Do(cbReq)
	if err != nil {
		t.Fatal(err)
	}
	done.Body.Close()
	if done.StatusCode != http.StatusSeeOther {
		t.Fatalf("callback status = %d, want 303", done.StatusCode)
	}
	if got := done.Header.Get("Location"); got != "/" {
		t.Fatalf("post-login redirect = %q, want /", got)
	}
	sawSession := false
	for _, c := range done.Cookies() {
		if c.Name == "session" && c.Value != "" {
			sawSession = true
		}
	}
	if !sawSession {
		t.Fatalf("no session cookie after SSO: %+v", done.Cookies())
	}

	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.subject != "subj-123" || svc.email != "alice@example.com" || svc.display != "Alice Example" {
		t.Fatalf("sso service claims = %q/%q/%q", svc.subject, svc.email, svc.display)
	}
	if svc.provider != f.issuer {
		t.Fatalf("provider = %q, want %q", svc.provider, f.issuer)
	}
	if !svc.autoCreate {
		t.Fatal("auto-create should be on by default")
	}
}

func TestSSOCallbackRejectsStateMismatch(t *testing.T) {
	f := newFakeIdP(t)
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com", Enabled: true}
	srv := newSSOServer(t, f, &testSSOService{user: user}, web.OIDCProvider{})

	client := noFollowClient()
	start, _ := client.Get(srv.URL + "/login/sso")
	start.Body.Close()
	var ssoCookie *http.Cookie
	for _, c := range start.Cookies() {
		if c.Name == "sso" {
			ssoCookie = c
		}
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/login/sso/callback?code=test-code&state=tampered", nil)
	req.AddCookie(ssoCookie)
	cb, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cb.Body.Close()
	if cb.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 back to login", cb.StatusCode)
	}
	if !strings.Contains(cb.Header.Get("Location"), "/login?error=") {
		t.Fatalf("redirect = %q, want login?error=", cb.Header.Get("Location"))
	}
}

func TestSSOAllowedDomainsBlockOtherDomains(t *testing.T) {
	f := newFakeIdP(t)
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com", Enabled: true}
	srv := newSSOServer(t, f, &testSSOService{user: user},
		web.OIDCProvider{AllowedDomains: []string{"example.net"}})

	resp := walkLoginFlow(t, srv, f.srv)
	if !strings.Contains(resp.Header.Get("Location"), "/login?error=This+email") {
		t.Fatalf("redirect = %q, want blocked-email error", resp.Header.Get("Location"))
	}
}

func TestSSORejectsUnverifiedEmail(t *testing.T) {
	f := newFakeIdP(t)
	f.verified = false
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com", Enabled: true}
	srv := newSSOServer(t, f, &testSSOService{user: user},
		web.OIDCProvider{RequireEmailVerified: true})

	resp := walkLoginFlow(t, srv, f.srv)
	if !strings.Contains(resp.Header.Get("Location"), "/login?error=SSO+sign-in+requires") {
		t.Fatalf("redirect = %q, want unverified-email error", resp.Header.Get("Location"))
	}
}

func TestSSOAcceptsUnverifiedWhenNotRequired(t *testing.T) {
	f := newFakeIdP(t)
	f.verified = false // an IdP like stock Authentik that never verifies
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com", Enabled: true}
	svc := &testSSOService{user: user}
	srv := newSSOServer(t, f, svc, web.OIDCProvider{})

	resp := walkLoginFlow(t, srv, f.srv)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 past the email_verified gate", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("post-login redirect = %q, want /", loc)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.email != "alice@example.com" {
		t.Fatalf("claims email = %q, want alice@example.com", svc.email)
	}
}

func TestSSOUsesUserInfoWhenIDTokenLacksEmail(t *testing.T) {
	f := newFakeIdP(t)
	f.idName = "" // drop the name claim from the id_token, keep it in userinfo
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com", Enabled: true}
	svc := &testSSOService{user: user}
	srv := newSSOServer(t, f, svc, web.OIDCProvider{})

	resp := walkLoginFlow(t, srv, f.srv)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.display != "Alice Example" {
		t.Fatalf("display = %q, want userinfo fallback value", svc.display)
	}
}
