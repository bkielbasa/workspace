package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// orgSSOBegin starts the dynamic OIDC authorization flow for an organization.
func (s *Server) orgSSOBegin(w http.ResponseWriter, r *http.Request, domain string) {
	cleanDomain := strings.ToLower(strings.TrimSpace(domain))
	if cleanDomain == "" || s.orgs == nil {
		s.ssoFail(w, r, "Single Sign-On is not configured for this organization.")
		return
	}

	ctx := r.Context()
	org, _, err := s.orgs.GetByDomain(ctx, cleanDomain)
	if err != nil || org == nil {
		s.ssoFail(w, r, "Organization not found.")
		return
	}

	sso, err := s.orgs.GetSSO(ctx, org.ID)
	if err != nil || sso == nil || !sso.Enabled {
		s.ssoFail(w, r, "Single Sign-On is disabled for this organization.")
		return
	}

	discoCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	provider, err := oidc.NewProvider(discoCtx, sso.Issuer)
	if err != nil {
		obs.Log(ctx, slog.LevelError, "dynamic org sso provider discovery failed", "error", err, "issuer", sso.Issuer)
		s.ssoFail(w, r, "Failed to connect to identity provider.")
		return
	}

	state, err := newRandomToken()
	if err != nil {
		obs.Log(ctx, slog.LevelError, "org sso token generation failed", "error", err)
		s.ssoFail(w, r, "Could not start SSO sign-in.")
		return
	}

	verifier := oauth2.GenerateVerifier()

	cookieVal := state + "." + verifier + "." + org.ID.String()
	http.SetCookie(w, &http.Cookie{
		Name:     ssoCookieName,
		Value:    cookieVal,
		Path:     ssoCookiePath,
		HttpOnly: true,
		Secure:   s.cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ssoCookieTTL.Seconds()),
		Expires:  time.Now().Add(ssoCookieTTL),
	})

	scopes := sso.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}

	oauthCfg := &oauth2.Config{
		ClientID:     sso.ClientID,
		ClientSecret: sso.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  s.orgSSORedirectURL(r),
		Scopes:       scopes,
	}

	authURL := oauthCfg.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", state),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// orgSSOCallback handles the callback from an organization's OIDC IdP.
func (s *Server) orgSSOCallback(w http.ResponseWriter, r *http.Request, cookie *http.Cookie) {
	http.SetCookie(w, clearCookie(ssoCookieName, ssoCookiePath))

	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 {
		s.ssoFail(w, r, "SSO sign-in expired or was not started. Please try again.")
		return
	}
	stateCookie, verifier, orgIDStr := parts[0], parts[1], parts[2]

	stateParam := r.URL.Query().Get("state")
	if subtle.ConstantTimeCompare([]byte(stateCookie), []byte(stateParam)) != 1 {
		obs.Log(r.Context(), slog.LevelWarn, "org sso state mismatch", "remote", clientIP(r))
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

	orgID, err := uuid.Parse(orgIDStr)
	if err != nil || s.orgs == nil {
		s.ssoFail(w, r, "Invalid organization SSO state.")
		return
	}

	ctx := r.Context()
	org, err := s.orgs.Get(ctx, orgID)
	if err != nil || org == nil {
		s.ssoFail(w, r, "Organization not found.")
		return
	}

	sso, err := s.orgs.GetSSO(ctx, org.ID)
	if err != nil || sso == nil || !sso.Enabled {
		s.ssoFail(w, r, "Single Sign-On is disabled for this organization.")
		return
	}

	exchangeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	provider, err := oidc.NewProvider(exchangeCtx, sso.Issuer)
	if err != nil {
		obs.Log(ctx, slog.LevelWarn, "org sso provider init failed", "error", err)
		s.ssoFail(w, r, "SSO sign-in failed during provider discovery.")
		return
	}

	verifierToken := provider.Verifier(&oidc.Config{ClientID: sso.ClientID})
	oauthCfg := &oauth2.Config{
		ClientID:     sso.ClientID,
		ClientSecret: sso.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  s.orgSSORedirectURL(r),
		Scopes:       sso.Scopes,
	}

	token, err := oauthCfg.Exchange(exchangeCtx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		obs.Log(ctx, slog.LevelWarn, "org sso token exchange failed", "error", err)
		s.ssoFail(w, r, "SSO sign-in failed during token exchange. Please try again.")
		return
	}

	rawID, _ := token.Extra("id_token").(string)
	if rawID == "" {
		s.ssoFail(w, r, "SSO sign-in failed: the provider sent no id_token.")
		return
	}

	idToken, err := verifierToken.Verify(exchangeCtx, rawID)
	if err != nil {
		obs.Log(ctx, slog.LevelWarn, "org sso id_token verification failed", "error", err)
		s.ssoFail(w, r, "SSO sign-in failed: the provider's id_token could not be verified.")
		return
	}

	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		s.ssoFail(w, r, "SSO sign-in failed: could not read provider claims.")
		return
	}

	if nonce := claimString(claims, "nonce"); subtle.ConstantTimeCompare([]byte(nonce), []byte(stateParam)) != 1 {
		obs.Log(ctx, slog.LevelWarn, "org sso nonce mismatch", "remote", clientIP(r))
		s.ssoFail(w, r, "SSO sign-in failed validation. Please try again.")
		return
	}

	email := claimString(claims, "email")
	name := claimString(claims, "name")
	if userInfo, err := provider.UserInfo(exchangeCtx, oauthCfg.TokenSource(exchangeCtx, token)); err == nil {
		ui := map[string]any{}
		if err := userInfo.Claims(&ui); err == nil {
			if email == "" {
				email = claimString(ui, "email")
			}
			if name == "" {
				name = claimString(ui, "name", "preferred_username")
			}
		}
	}

	cleanEmail := strings.ToLower(strings.TrimSpace(email))
	if cleanEmail == "" {
		s.ssoFail(w, r, "SSO sign-in failed: the provider did not share an email address.")
		return
	}

	atIdx := strings.LastIndexByte(cleanEmail, '@')
	if atIdx < 0 {
		s.ssoFail(w, r, "SSO sign-in failed: invalid email address.")
		return
	}
	emailDomain := strings.ToLower(cleanEmail[atIdx+1:])

	// Enforce email domain against organization domains
	domainAllowed := false
	if domains, err := s.orgs.ListDomains(ctx, org.ID); err == nil {
		for _, d := range domains {
			if strings.EqualFold(d.Domain, emailDomain) {
				domainAllowed = true
				break
			}
		}
	}
	if !domainAllowed {
		if lookupOrg, _, err := s.orgs.GetByDomain(ctx, emailDomain); err == nil && lookupOrg != nil && lookupOrg.ID == org.ID {
			domainAllowed = true
		}
	}
	if !domainAllowed {
		obs.Log(ctx, slog.LevelWarn, "org sso email domain mismatch", "email", cleanEmail, "org", org.ID)
		s.ssoFail(w, r, "SSO sign-in failed: email domain does not match organization domain.")
		return
	}

	if name == "" {
		name = emailLocalPart(cleanEmail)
	}

	user, err := s.users.GetByEmail(ctx, cleanEmail)
	if errors.Is(err, identity.ErrUserNotFound) || user == nil {
		if !sso.AutoProvision {
			s.ssoFail(w, r, "No account exists for this email and automatic provisioning is disabled.")
			return
		}
		newUser, err := s.users.CreateWithOrg(ctx, cleanEmail, name, org.ID)
		if err != nil {
			obs.Log(ctx, slog.LevelError, "org sso create user failed", "error", err)
			s.ssoFail(w, r, "SSO sign-in failed to create user account.")
			return
		}
		user = newUser
		if err := s.orgs.AddMember(ctx, org.ID, user.ID, identity.RoleMember); err != nil && !errors.Is(err, identity.ErrMemberAlreadyExists) {
			obs.Log(ctx, slog.LevelError, "org sso add member failed", "error", err)
		}
	} else {
		if user.OrganizationID == nil || *user.OrganizationID != org.ID {
			if err := s.users.SetOrganization(ctx, user.ID, org.ID); err != nil {
				obs.Log(ctx, slog.LevelError, "org sso set organization failed", "error", err)
			} else {
				idCopy := org.ID
				user.OrganizationID = &idCopy
			}
		}
		if _, err := s.orgs.GetMember(ctx, org.ID, user.ID); errors.Is(err, identity.ErrMemberNotFound) {
			if err := s.orgs.AddMember(ctx, org.ID, user.ID, identity.RoleMember); err != nil && !errors.Is(err, identity.ErrMemberAlreadyExists) {
				obs.Log(ctx, slog.LevelError, "org sso add member failed", "error", err)
			}
		}
	}

	if _, err := s.establishSession(w, r, user); err != nil {
		obs.Log(ctx, slog.LevelError, "org sso session establishment failed", "error", err)
		s.ssoFail(w, r, "SSO sign-in failed to start your session.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) orgSSORedirectURL(r *http.Request) string {
	scheme := "http"
	if s.cookieSecure(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/login/sso/callback"
}
