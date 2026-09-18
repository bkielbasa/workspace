package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCProvider describes one identity provider for the Authorization Code
// + PKCE flow. Everything here maps straight to environment variables, so a
// self-hoster can point the app at any OIDC/OAuth2 provider (Authentik,
// Keycloak, Google, GitHub, ...) without touching code.
type OIDCProvider struct {
	// Name is shown on the login page button.
	Name string
	// Issuer is the provider's OIDC discovery URL (e.g.
	// https://auth.example/application/o/workspace/).
	Issuer string
	// ClientID / ClientSecret come from the provider's application.
	ClientID     string
	ClientSecret string
	// RedirectURL is where the provider returns the code. It must be
	// registered at the provider, exactly.
	RedirectURL string
	// Scopes default to openid profile email when empty.
	Scopes []string
	// EmailClaim / NameClaim pick the JWT claims carrying the user's email
	// and display name, for providers that use custom claim names.
	EmailClaim string
	NameClaim  string
	// AllowedDomains, when non-empty, restricts who can sign in: only
	// accounts whose email is in this list are admitted.
	AllowedDomains []string
	// AutoCreate governs just-in-time provisioning: unknown-but-verified
	// SSO users get an account when true.
	AutoCreate bool
}

// ssoservice maps a verified OIDC identity onto a workspace account.
type ssoservice interface {
	Authenticate(context.Context, string, string, string, string, bool) (*identity.User, error)
}

// oidcClient bundles the discovered endpoints, token exchange config and the
// id_token verifier for one provider. It is only ever read after SetOIDC,
// so no locking is needed.
type oidcClient struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
	idp      OIDCProvider
}

// SetOIDC enables SSO sign-in for one provider. It performs OIDC discovery,
// so the issuer must be reachable now; misconfiguration returns an error and
// the caller decides whether to keep password-only login or bail. Optional:
// without this, /login renders only the password form.
func (s *Server) SetOIDC(idp OIDCProvider, svc ssoservice) error {
	if idp.Issuer == "" || idp.ClientID == "" || idp.ClientSecret == "" {
		return errors.New("sso: issuer, client id and client secret are required")
	}
	if idp.RedirectURL == "" {
		return errors.New("sso: redirect URL is required")
	}
	if idp.EmailClaim == "" {
		idp.EmailClaim = "email"
	}
	if idp.NameClaim == "" {
		idp.NameClaim = "name"
	}
	if len(idp.Scopes) == 0 {
		idp.Scopes = []string{"openid", "profile", "email"}
	}
	if idp.Name == "" {
		idp.Name = "Single Sign-On"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, idp.Issuer)
	if err != nil {
		return fmt.Errorf("sso: discover provider: %w", err)
	}
	s.oidc = &oidcClient{
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: idp.ClientID}),
		oauth: oauth2.Config{
			ClientID: idp.ClientID, ClientSecret: idp.ClientSecret,
			RedirectURL: idp.RedirectURL, Endpoint: provider.Endpoint(),
			Scopes: idp.Scopes,
		},
		idp: idp,
	}
	s.sso = svc
	obs.Log(context.Background(), slog.LevelInfo, "sso enabled",
		"provider", idp.Name, "issuer", idp.Issuer,
		"auto_create", idp.AutoCreate, "allowed_domains", len(idp.AllowedDomains))
	return nil
}

const (
	ssoCookieName = "sso"
	ssoCookiePath = "/login/sso/callback"
	ssoCookieTTL  = 10 * time.Minute
)

// ssoLogin starts the authorization code + PKCE dance. With a single
// configured provider it immediately redirects to the provider.
func (s *Server) ssoLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.NotFound(w, r)
		return
	}
	if s.validSession(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.ssoBegin(w, r)
}

func (s *Server) ssoBegin(w http.ResponseWriter, r *http.Request) {
	state, err := newRandomToken()
	if err != nil {
		obs.Log(r.Context(), slog.LevelError, "sso begin failed", "error", err)
		s.ssoFail(w, r, "could not start SSO sign-in")
		return
	}
	verifier := oauth2.GenerateVerifier()
	http.SetCookie(w, &http.Cookie{
		Name: ssoCookieName, Value: state + "." + verifier,
		Path: ssoCookiePath, HttpOnly: true,
		Secure: s.cookieSecure(r), SameSite: http.SameSiteLaxMode,
		MaxAge: int(ssoCookieTTL.Seconds()), Expires: time.Now().Add(ssoCookieTTL),
	})
	authURL := s.oidc.oauth.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", state),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	oc := s.oidc
	if oc == nil {
		http.NotFound(w, r)
		return
	}
	cookie, err := r.Cookie(ssoCookieName)
	if err != nil {
		s.ssoFail(w, r, "SSO sign-in expired or was not started. Please try again.")
		return
	}
	// The cookie is single-use: pop it before anything else.
	http.SetCookie(w, clearCookie(ssoCookieName, ssoCookiePath))

	seg := strings.SplitN(cookie.Value, ".", 2)
	state := r.URL.Query().Get("state")
	if len(seg) != 2 || subtle.ConstantTimeCompare([]byte(seg[0]), []byte(state)) != 1 {
		obs.Log(r.Context(), slog.LevelWarn, "sso state mismatch", "remote", clientIP(r))
		s.ssoFail(w, r, "SSO sign-in did not complete. Please try again.")
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		msg := "SSO sign-in was cancelled."
		if desc := r.URL.Query().Get("error_description"); desc != "" {
			msg = "SSO sign-in failed: " + desc
		}
		s.ssoFail(w, r, msg)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		s.ssoFail(w, r, "SSO sign-in failed: the provider returned no code.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	token, err := oc.oauth.Exchange(ctx, code, oauth2.VerifierOption(seg[1]))
	if err != nil {
		obs.Log(r.Context(), slog.LevelWarn, "sso token exchange failed", "error", err)
		s.ssoFail(w, r, "SSO sign-in failed during token exchange. Please try again.")
		return
	}
	rawID, _ := token.Extra("id_token").(string)
	if rawID == "" {
		s.ssoFail(w, r, "SSO sign-in failed: the provider sent no id_token.")
		return
	}
	idToken, err := oc.verifier.Verify(ctx, rawID)
	if err != nil {
		obs.Log(r.Context(), slog.LevelWarn, "sso id_token verification failed", "error", err)
		s.ssoFail(w, r, "SSO sign-in failed: the provider's id_token could not be verified.")
		return
	}
	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		s.ssoFail(w, r, "SSO sign-in failed: could not read provider claims.")
		return
	}
	// The nonce doubles as CSRF protection for the callback and proves this
	// token answers our own authorization request.
	if nonce := claimString(claims, "nonce"); subtle.ConstantTimeCompare([]byte(nonce), []byte(state)) != 1 {
		obs.Log(r.Context(), slog.LevelWarn, "sso nonce mismatch", "remote", clientIP(r))
		s.ssoFail(w, r, "SSO sign-in failed validation. Please try again.")
		return
	}

	// Some providers leave email/name out of the id_token and only serve
	// them from the userinfo endpoint.
	subject := idToken.Subject
	email := claimString(claims, oc.idp.EmailClaim, "email")
	name := claimString(claims, oc.idp.NameClaim, "name")
	verified, known := claimBool(claims, "email_verified")
	if userInfo, err := oc.provider.UserInfo(ctx, oc.oauth.TokenSource(ctx, token)); err == nil {
		ui := map[string]any{}
		if err := userInfo.Claims(&ui); err == nil {
			if email == "" {
				email = claimString(ui, oc.idp.EmailClaim, "email")
			}
			if name == "" {
				name = claimString(ui, oc.idp.NameClaim, "name", "preferred_username")
			}
			if !known {
				verified, known = claimBool(ui, "email_verified")
			}
		}
	}
	if known && !verified {
		s.ssoFail(w, r, "SSO sign-in requires a verified email address.")
		return
	}
	if email == "" {
		s.ssoFail(w, r, "SSO sign-in failed: the provider did not share an email address.")
		return
	}
	if !s.ssoEmailAllowed(email) {
		obs.Log(r.Context(), slog.LevelWarn, "sso email not allowed", "email", email)
		s.ssoFail(w, r, "This email address is not allowed to sign in.")
		return
	}
	if name == "" {
		name = emailLocalPart(email)
	}

	user, err := s.sso.Authenticate(ctx, oc.idp.Issuer, subject, email, name, oc.idp.AutoCreate)
	switch {
	case errors.Is(err, identity.ErrUserNotFound):
		s.ssoFail(w, r, "No account exists for this email. Sign in with your password or ask an administrator.")
		return
	case errors.Is(err, identity.ErrSSONotAllowed):
		s.ssoFail(w, r, "This account is disabled.")
		return
	case err != nil:
		obs.Log(r.Context(), slog.LevelError, "sso authenticate failed", "error", err)
		s.ssoFail(w, r, "SSO sign-in failed. Please try again.")
		return
	}
	if _, err := s.establishSession(w, r, user); err != nil {
		obs.Log(r.Context(), slog.LevelError, "sso session failed", "error", err)
		s.ssoFail(w, r, "SSO sign-in failed to start your session.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ssoFail bounces back to the login form with a user-safe message.
func (s *Server) ssoFail(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/login?error="+url.QueryEscape(message), http.StatusSeeOther)
}

// ssoEmailAllowed applies the configured domain allow-list.
func (s *Server) ssoEmailAllowed(email string) bool {
	if len(s.oidc.idp.AllowedDomains) == 0 {
		return true
	}
	_, domain, ok := strings.Cut(email, "@")
	if !ok {
		return false
	}
	for _, d := range s.oidc.idp.AllowedDomains {
		if strings.EqualFold(strings.TrimSpace(d), domain) {
			return true
		}
	}
	return false
}

// claimString reads the first non-empty string-valued claim among names.
func claimString(claims map[string]any, names ...string) string {
	for _, n := range names {
		if v, ok := claims[n].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// claimBool reads a JSON boolean claim (some providers stringify it).
func claimBool(claims map[string]any, name string) (value bool, known bool) {
	if v, ok := claims[name].(bool); ok {
		return v, true
	}
	if v, ok := claims[name].(string); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b, true
		}
	}
	return false, false
}

func emailLocalPart(email string) string {
	local, _, ok := strings.Cut(email, "@")
	if !ok {
		return email
	}
	return local
}

// clearCookie removes a cookie on path.
func clearCookie(name, path string) *http.Cookie {
	return &http.Cookie{
		Name: name, Path: path, HttpOnly: true,
		MaxAge: -1, Expires: time.Unix(1, 0),
	}
}
