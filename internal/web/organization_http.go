package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

func isJSONRequest(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Content-Type"), "application/json") || strings.Contains(r.Header.Get("Accept"), "application/json")
}

func (s *Server) checkOrgAdmin(ctx context.Context, user *identity.User) (uuid.UUID, bool, error) {
	if user == nil || user.OrganizationID == nil {
		return uuid.Nil, false, nil
	}
	orgID := *user.OrganizationID
	if user.IsAdmin {
		return orgID, true, nil
	}
	if s.orgs == nil {
		return orgID, false, nil
	}
	member, err := s.orgs.GetMember(ctx, orgID, user.ID)
	if err != nil {
		return orgID, false, err
	}
	if member != nil && (member.Role == identity.RoleOwner || member.Role == identity.RoleAdmin) {
		return orgID, true, nil
	}
	return orgID, false, nil
}

func (s *Server) settingsOrganizationPage(w http.ResponseWriter, r *http.Request, user *identity.User) {
	successVal := r.URL.Query().Get("success")
	var successMsg string
	switch successVal {
	case "organization_created":
		successMsg = "Organization created successfully."
	case "domain_added":
		successMsg = "Custom domain added. Please add the DNS TXT record below to verify ownership."
	case "domain_verified":
		successMsg = "Custom domain verified successfully!"
	case "sso_updated":
		successMsg = "SSO configuration saved successfully."
	case "member_invited":
		successMsg = "Member invited successfully."
	default:
		successMsg = successVal
	}

	errorMsg := r.URL.Query().Get("error")

	// If the user has no organization yet, show the creation form
	if user.OrganizationID == nil {
		data := viewData{
			Title:     "Create Organization",
			Tab:       "organization",
			User:      user,
			CSRFToken: csrfTokenFromRequest(r),
			Error:     errorMsg,
			Success:   successMsg,
		}
		renderView(w, r, s.views.organizationNewT, "layout", data)
		return
	}

	// Fetch organization details
	ctx := r.Context()
	orgID := *user.OrganizationID
	if s.orgs == nil {
		http.Error(w, "organization service unavailable", http.StatusServiceUnavailable)
		return
	}

	org, err := s.orgs.Get(ctx, orgID)
	if err != nil {
		http.Error(w, "organization not found", http.StatusNotFound)
		return
	}

	domains, err := s.orgs.ListDomains(ctx, orgID)
	if err != nil {
		domains = nil
	}

	sso, err := s.orgs.GetSSO(ctx, orgID)
	if err != nil {
		sso = nil
	}

	rawMembers, err := s.orgs.ListMembers(ctx, orgID)
	if err != nil {
		rawMembers = nil
	}

	var memberViews []orgMemberView
	for _, m := range rawMembers {
		mv := orgMemberView{
			UserID:    m.UserID,
			Role:      m.Role,
			CreatedAt: m.CreatedAt,
		}
		if s.users != nil {
			if u, err := s.users.Get(ctx, m.UserID); err == nil && u != nil {
				mv.Email = u.Email
				mv.DisplayName = u.DisplayName
			}
		}
		memberViews = append(memberViews, mv)
	}

	_, isOrgAdmin, _ := s.checkOrgAdmin(ctx, user)

	// Redact SSO Client Secret if user is not an org admin
	if !isOrgAdmin && sso != nil {
		ssoCopy := *sso
		ssoCopy.ClientSecret = ""
		sso = &ssoCopy
	}

	data := viewData{
		Title:        "Organization Settings",
		Tab:          "organization",
		User:         user,
		CSRFToken:    csrfTokenFromRequest(r),
		Error:        errorMsg,
		Success:      successMsg,
		Organization: org,
		Domains:      domains,
		SSO:          sso,
		Members:      memberViews,
		IsOrgAdmin:   isOrgAdmin,
	}
	renderView(w, r, s.views.organizationT, "layout", data)
}

type organizationCreateRequest struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
}

func (s *Server) settingsOrganizationCreate(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	isJSON := isJSONRequest(r)

	if user.OrganizationID != nil {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, "user already belongs to an organization")
		} else {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape("user already belongs to an organization"), http.StatusSeeOther)
		}
		return
	}

	var req organizationCreateRequest
	if isJSON {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape("invalid form data"), http.StatusSeeOther)
			return
		}
		req.Name = r.FormValue("name")
		req.Domain = r.FormValue("domain")
	}

	name := strings.TrimSpace(req.Name)
	domain := strings.TrimSpace(strings.ToLower(req.Domain))

	if name == "" || domain == "" {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, "organization name and domain are required")
		} else {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape("organization name and domain are required"), http.StatusSeeOther)
		}
		return
	}

	if s.orgs == nil {
		if isJSON {
			writeJSONError(w, http.StatusInternalServerError, "organization service unavailable")
		} else {
			http.Error(w, "organization service unavailable", http.StatusInternalServerError)
		}
		return
	}

	ctx := r.Context()
	org, dom, err := s.orgs.Create(ctx, user.ID, name, domain)
	if err != nil {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, err.Error())
		} else {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		}
		return
	}

	if s.users != nil {
		if err := s.users.SetOrganization(ctx, user.ID, org.ID); err != nil {
			if isJSON {
				writeJSONError(w, http.StatusInternalServerError, "failed to associate user with organization")
			} else {
				http.Error(w, "failed to associate user with organization", http.StatusInternalServerError)
			}
			return
		}
	}
	user.OrganizationID = &org.ID

	if isJSON {
		writeJSON(w, http.StatusCreated, map[string]any{
			"organization": org,
			"domain":       dom,
		})
		return
	}

	http.Redirect(w, r, "/settings/organization?success=organization_created", http.StatusSeeOther)
}

type domainCreateRequest struct {
	Domain string `json:"domain"`
}

func (s *Server) settingsOrganizationDomainCreate(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	isJSON := isJSONRequest(r)
	orgID, isOrgAdmin, err := s.checkOrgAdmin(r.Context(), user)
	if err != nil || !isOrgAdmin || orgID == uuid.Nil {
		if isJSON {
			writeJSONError(w, http.StatusForbidden, "forbidden")
		} else {
			http.Error(w, "forbidden", http.StatusForbidden)
		}
		return
	}

	var req domainCreateRequest
	if isJSON {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape("invalid form data"), http.StatusSeeOther)
			return
		}
		req.Domain = r.FormValue("domain")
	}

	domain := strings.TrimSpace(strings.ToLower(req.Domain))
	if domain == "" {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, "domain name is required")
		} else {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape("domain name is required"), http.StatusSeeOther)
		}
		return
	}

	dom, err := s.orgs.CreateDomain(r.Context(), orgID, domain)
	if err != nil {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, err.Error())
		} else {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		}
		return
	}

	if isJSON {
		writeJSON(w, http.StatusCreated, dom)
		return
	}

	http.Redirect(w, r, "/settings/organization?success=domain_added", http.StatusSeeOther)
}

func (s *Server) settingsOrganizationDomainVerify(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	isJSON := isJSONRequest(r)
	orgID, isOrgAdmin, err := s.checkOrgAdmin(r.Context(), user)
	if err != nil || !isOrgAdmin || orgID == uuid.Nil {
		if isJSON {
			writeJSONError(w, http.StatusForbidden, "forbidden")
		} else {
			http.Error(w, "forbidden", http.StatusForbidden)
		}
		return
	}

	domainID, err := parseUUIDPath(r)
	if err != nil {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, "invalid domain ID")
		} else {
			http.Error(w, "invalid domain ID", http.StatusBadRequest)
		}
		return
	}

	// Verify that the domain belongs to this organization
	domains, err := s.orgs.ListDomains(r.Context(), orgID)
	if err != nil {
		if isJSON {
			writeJSONError(w, http.StatusInternalServerError, "failed to list domains")
		} else {
			http.Error(w, "failed to list domains", http.StatusInternalServerError)
		}
		return
	}

	var found bool
	for _, d := range domains {
		if d.ID == domainID {
			found = true
			break
		}
	}
	if !found {
		if isJSON {
			writeJSONError(w, http.StatusNotFound, "domain not found")
		} else {
			http.Error(w, "domain not found", http.StatusNotFound)
		}
		return
	}

	if s.domainVerifier == nil {
		if isJSON {
			writeJSONError(w, http.StatusInternalServerError, "domain verifier not configured")
		} else {
			http.Error(w, "domain verifier not configured", http.StatusInternalServerError)
		}
		return
	}

	if err := s.domainVerifier.Verify(r.Context(), domainID); err != nil {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, err.Error())
		} else {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		}
		return
	}

	if isJSON {
		writeJSON(w, http.StatusOK, map[string]any{
			"verified":  true,
			"domain_id": domainID,
		})
		return
	}

	http.Redirect(w, r, "/settings/organization?success=domain_verified", http.StatusSeeOther)
}

type ssoSaveRequest struct {
	Name          string   `json:"name"`
	Issuer        string   `json:"issuer"`
	ClientID      string   `json:"client_id"`
	ClientSecret  string   `json:"client_secret"`
	Scopes        []string `json:"scopes"`
	EnforceSSO    bool     `json:"enforce_sso"`
	AutoProvision bool     `json:"auto_provision"`
	Enabled       bool     `json:"enabled"`
}

func (s *Server) settingsOrganizationSSOSave(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	isJSON := isJSONRequest(r)
	orgID, isOrgAdmin, err := s.checkOrgAdmin(r.Context(), user)
	if err != nil || !isOrgAdmin || orgID == uuid.Nil {
		if isJSON {
			writeJSONError(w, http.StatusForbidden, "forbidden")
		} else {
			http.Error(w, "forbidden", http.StatusForbidden)
		}
		return
	}

	var req ssoSaveRequest
	if isJSON {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape("invalid form data"), http.StatusSeeOther)
			return
		}
		req.Name = r.FormValue("name")
		req.Issuer = r.FormValue("issuer")
		req.ClientID = r.FormValue("client_id")
		req.ClientSecret = r.FormValue("client_secret")

		scopesRaw := r.FormValue("scopes")
		if scopesRaw != "" {
			parts := strings.Split(scopesRaw, ",")
			for _, p := range parts {
				trimmed := strings.TrimSpace(p)
				if trimmed != "" {
					req.Scopes = append(req.Scopes, trimmed)
				}
			}
		}

		req.EnforceSSO = r.FormValue("enforce_sso") == "true" || r.FormValue("enforce_sso") == "on" || r.FormValue("enforce_sso") == "1"
		req.AutoProvision = r.FormValue("auto_provision") == "true" || r.FormValue("auto_provision") == "on" || r.FormValue("auto_provision") == "1"
		req.Enabled = r.FormValue("enabled") == "true" || r.FormValue("enabled") == "on" || r.FormValue("enabled") == "1"
	}

	if len(req.Scopes) == 0 {
		req.Scopes = []string{"openid", "profile", "email"}
	}

	clientSecret := strings.TrimSpace(req.ClientSecret)
	if clientSecret == "" {
		if existing, err := s.orgs.GetSSO(r.Context(), orgID); err == nil && existing != nil {
			clientSecret = existing.ClientSecret
		}
	}

	sso := &identity.OrganizationSSO{
		OrganizationID: orgID,
		Name:           strings.TrimSpace(req.Name),
		Issuer:         strings.TrimSpace(req.Issuer),
		ClientID:       strings.TrimSpace(req.ClientID),
		ClientSecret:   clientSecret,
		Scopes:         req.Scopes,
		EnforceSSO:     req.EnforceSSO,
		AutoProvision:  req.AutoProvision,
		Enabled:        req.Enabled,
	}

	if err := s.orgs.SaveSSO(r.Context(), sso); err != nil {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, err.Error())
		} else {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		}
		return
	}

	if isJSON {
		writeJSON(w, http.StatusOK, map[string]any{"sso": sso})
		return
	}

	http.Redirect(w, r, "/settings/organization?success=sso_updated", http.StatusSeeOther)
}

type memberInviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (s *Server) settingsOrganizationMemberInvite(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	isJSON := isJSONRequest(r)
	orgID, isOrgAdmin, err := s.checkOrgAdmin(r.Context(), user)
	if err != nil || !isOrgAdmin || orgID == uuid.Nil {
		if isJSON {
			writeJSONError(w, http.StatusForbidden, "forbidden")
		} else {
			http.Error(w, "forbidden", http.StatusForbidden)
		}
		return
	}

	var req memberInviteRequest
	if isJSON {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape("invalid form data"), http.StatusSeeOther)
			return
		}
		req.Email = r.FormValue("email")
		req.Role = r.FormValue("role")
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	role := strings.TrimSpace(strings.ToLower(req.Role))
	if role == "" {
		role = identity.RoleMember
	}

	if email == "" {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, "email is required")
		} else {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape("email is required"), http.StatusSeeOther)
		}
		return
	}

	if role != identity.RoleMember && role != identity.RoleAdmin {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, "invalid role")
		} else {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape("invalid role"), http.StatusSeeOther)
		}
		return
	}

	ctx := r.Context()
	targetUser, err := s.users.GetByEmail(ctx, email)
	if err == nil && targetUser != nil {
		if targetUser.OrganizationID != nil && *targetUser.OrganizationID != orgID {
			msg := "user already belongs to another organization"
			if isJSON {
				writeJSONError(w, http.StatusConflict, msg)
			} else {
				http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape(msg), http.StatusSeeOther)
			}
			return
		}
		_ = s.users.SetOrganization(ctx, targetUser.ID, orgID)
		err = s.orgs.AddMember(ctx, orgID, targetUser.ID, role)
	} else {
		targetUser, err = s.users.CreateWithOrg(ctx, email, "", orgID)
		if err != nil && !errors.Is(err, identity.ErrUserAlreadyExists) {
			if isJSON {
				writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("failed to provision user: %v", err))
			} else {
				http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape("failed to provision user"), http.StatusSeeOther)
			}
			return
		}
		if targetUser != nil {
			err = s.orgs.AddMember(ctx, orgID, targetUser.ID, role)
		}
	}

	if err != nil && !errors.Is(err, identity.ErrMemberAlreadyExists) {
		if isJSON {
			writeJSONError(w, http.StatusBadRequest, err.Error())
		} else {
			http.Redirect(w, r, "/settings/organization?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		}
		return
	}

	if isJSON {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "invited",
			"email":  email,
			"role":   role,
		})
		return
	}

	http.Redirect(w, r, "/settings/organization?success=member_invited", http.StatusSeeOther)
}
