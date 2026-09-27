package identity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

func TestOrganizationCreationAndDomainBinding(t *testing.T) {
	repo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(repo)

	ctx := context.Background()
	ownerID := uuid.New()

	org, domain, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("unexpected error creating org: %v", err)
	}
	if org.Name != "Acme Corp" {
		t.Errorf("expected name Acme Corp, got %s", org.Name)
	}
	if org.Slug != "acme-corp" {
		t.Errorf("expected slug acme-corp, got %s", org.Slug)
	}
	if domain.Domain != "acme.corp" {
		t.Errorf("expected domain acme.corp, got %s", domain.Domain)
	}
	if domain.VerificationToken == "" {
		t.Errorf("expected non-empty verification token")
	}
	if domain.VerifiedAt != nil {
		t.Errorf("expected new domain to not be verified yet")
	}

	members, err := orgs.ListMembers(ctx, org.ID)
	if err != nil {
		t.Fatalf("failed to list members: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 member (owner), got %d", len(members))
	}
	if members[0].UserID != ownerID || members[0].Role != identity.RoleOwner {
		t.Errorf("expected owner member with RoleOwner, got %+v", members[0])
	}
}

func TestOrganizationCreationValidation(t *testing.T) {
	repo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(repo)
	ctx := context.Background()
	ownerID := uuid.New()

	if _, _, err := orgs.Create(ctx, ownerID, "", "acme.corp"); err == nil {
		t.Errorf("expected error with empty org name, got nil")
	}
	if _, _, err := orgs.Create(ctx, ownerID, "Acme Corp", ""); err == nil {
		t.Errorf("expected error with empty domain, got nil")
	}

	_, _, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Duplicate domain should be rejected
	_, _, err = orgs.Create(ctx, uuid.New(), "Another Corp", "acme.corp")
	if !errors.Is(err, identity.ErrOrgDomainAlreadyExists) {
		t.Errorf("expected ErrOrgDomainAlreadyExists, got %v", err)
	}
}

func TestOrganizationGetAndGetByDomain(t *testing.T) {
	repo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(repo)
	ctx := context.Background()
	ownerID := uuid.New()

	org, domain, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("unexpected error creating org: %v", err)
	}

	fetched, err := orgs.Get(ctx, org.ID)
	if err != nil {
		t.Fatalf("failed to get org by id: %v", err)
	}
	if fetched.ID != org.ID || fetched.Name != "Acme Corp" {
		t.Errorf("expected fetched org to match, got %+v", fetched)
	}

	_, err = orgs.Get(ctx, uuid.New())
	if !errors.Is(err, identity.ErrOrganizationNotFound) {
		t.Errorf("expected ErrOrganizationNotFound, got %v", err)
	}

	orgByDom, domByDom, err := orgs.GetByDomain(ctx, "acme.corp")
	if err != nil {
		t.Fatalf("failed to get org by domain: %v", err)
	}
	if orgByDom.ID != org.ID || domByDom.ID != domain.ID {
		t.Errorf("expected org and domain to match, got org=%+v dom=%+v", orgByDom, domByDom)
	}

	// Case-insensitivity check
	orgByUpper, _, err := orgs.GetByDomain(ctx, "ACME.CORP")
	if err != nil {
		t.Fatalf("failed to get org by uppercase domain: %v", err)
	}
	if orgByUpper.ID != org.ID {
		t.Errorf("expected org to match case-insensitively, got %+v", orgByUpper)
	}

	_, _, err = orgs.GetByDomain(ctx, "unknown.org")
	if !errors.Is(err, identity.ErrOrgDomainNotFound) {
		t.Errorf("expected ErrOrgDomainNotFound, got %v", err)
	}
}

func TestOrganizationDomainMethods(t *testing.T) {
	repo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(repo)
	ctx := context.Background()

	org, domain, err := orgs.Create(ctx, uuid.New(), "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("failed to create org: %v", err)
	}

	// GetDomain by ID
	d, err := orgs.GetDomain(ctx, domain.ID)
	if err != nil {
		t.Fatalf("GetDomain failed: %v", err)
	}
	if d.ID != domain.ID || d.Domain != "acme.corp" {
		t.Errorf("GetDomain mismatch: %+v", d)
	}

	// GetDomainByName
	dByName, err := orgs.GetDomainByName(ctx, "Acme.Corp")
	if err != nil {
		t.Fatalf("GetDomainByName failed: %v", err)
	}
	if dByName.ID != domain.ID {
		t.Errorf("GetDomainByName mismatch: %+v", dByName)
	}

	// Create additional domain
	subDom, err := orgs.CreateDomain(ctx, org.ID, "mail.acme.corp")
	if err != nil {
		t.Fatalf("CreateDomain failed: %v", err)
	}
	if subDom.Domain != "mail.acme.corp" || subDom.VerificationToken == "" {
		t.Errorf("unexpected created domain: %+v", subDom)
	}

	// ListDomains
	domList, err := orgs.ListDomains(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListDomains failed: %v", err)
	}
	if len(domList) != 2 {
		t.Errorf("expected 2 domains, got %d", len(domList))
	}

	// UpdateDomain (simulate verification)
	now := time.Now()
	domain.VerifiedAt = &now
	if err := orgs.UpdateDomain(ctx, domain); err != nil {
		t.Fatalf("UpdateDomain failed: %v", err)
	}

	updated, err := orgs.GetDomain(ctx, domain.ID)
	if err != nil {
		t.Fatalf("GetDomain after update failed: %v", err)
	}
	if updated.VerifiedAt == nil {
		t.Errorf("expected VerifiedAt to be set")
	}

	// DeleteDomain
	if err := orgs.DeleteDomain(ctx, subDom.ID); err != nil {
		t.Fatalf("DeleteDomain failed: %v", err)
	}
	_, err = orgs.GetDomain(ctx, subDom.ID)
	if !errors.Is(err, identity.ErrOrgDomainNotFound) {
		t.Errorf("expected ErrOrgDomainNotFound after delete, got %v", err)
	}
}

func TestOrganizationMembers(t *testing.T) {
	repo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(repo)
	ctx := context.Background()

	ownerID := uuid.New()
	user2 := uuid.New()
	user3 := uuid.New()

	org, _, err := orgs.Create(ctx, ownerID, "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("failed to create org: %v", err)
	}

	// Add member
	if err := orgs.AddMember(ctx, org.ID, user2, identity.RoleAdmin); err != nil {
		t.Fatalf("AddMember admin failed: %v", err)
	}
	if err := orgs.AddMember(ctx, org.ID, user3, identity.RoleMember); err != nil {
		t.Fatalf("AddMember member failed: %v", err)
	}

	// Invalid role
	if err := orgs.AddMember(ctx, org.ID, uuid.New(), "superuser"); !errors.Is(err, identity.ErrInvalidRole) {
		t.Errorf("expected ErrInvalidRole, got %v", err)
	}

	// Duplicate member
	if err := orgs.AddMember(ctx, org.ID, user2, identity.RoleMember); !errors.Is(err, identity.ErrMemberAlreadyExists) {
		t.Errorf("expected ErrMemberAlreadyExists, got %v", err)
	}

	members, err := orgs.ListMembers(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListMembers failed: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(members))
	}

	// Update role
	if err := orgs.UpdateMemberRole(ctx, org.ID, user2, identity.RoleMember); err != nil {
		t.Fatalf("UpdateMemberRole failed: %v", err)
	}
	m2, err := orgs.GetMember(ctx, org.ID, user2)
	if err != nil {
		t.Fatalf("GetMember failed: %v", err)
	}
	if m2.Role != identity.RoleMember {
		t.Errorf("expected role member, got %s", m2.Role)
	}

	// Remove member
	if err := orgs.RemoveMember(ctx, org.ID, user3); err != nil {
		t.Fatalf("RemoveMember failed: %v", err)
	}
	membersAfter, err := orgs.ListMembers(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListMembers after remove failed: %v", err)
	}
	if len(membersAfter) != 2 {
		t.Errorf("expected 2 members after removal, got %d", len(membersAfter))
	}
}

func TestOrganizationSSO(t *testing.T) {
	repo := identity.NewMemoryOrganizationRepository()
	orgs := identity.NewOrganizations(repo)
	ctx := context.Background()

	org, _, err := orgs.Create(ctx, uuid.New(), "Acme Corp", "acme.corp")
	if err != nil {
		t.Fatalf("failed to create org: %v", err)
	}

	// No SSO configured initially
	_, err = orgs.GetSSO(ctx, org.ID)
	if !errors.Is(err, identity.ErrSSONotFound) {
		t.Errorf("expected ErrSSONotFound, got %v", err)
	}

	sso := &identity.OrganizationSSO{
		OrganizationID: org.ID,
		Name:           "Acme Okta",
		Issuer:         "https://acme.okta.com",
		ClientID:       "client-123",
		ClientSecret:   "secret-456",
		Scopes:         []string{"openid", "email", "profile"},
		EnforceSSO:     true,
		AutoProvision:  true,
		Enabled:        true,
	}

	if err := orgs.SaveSSO(ctx, sso); err != nil {
		t.Fatalf("SaveSSO failed: %v", err)
	}

	saved, err := orgs.GetSSO(ctx, org.ID)
	if err != nil {
		t.Fatalf("GetSSO failed: %v", err)
	}
	if saved.Name != "Acme Okta" || saved.Issuer != "https://acme.okta.com" || !saved.EnforceSSO {
		t.Errorf("saved SSO config mismatch: %+v", saved)
	}
	if len(saved.Scopes) != 3 {
		t.Errorf("expected 3 scopes, got %+v", saved.Scopes)
	}

	// Update SSO configuration
	saved.EnforceSSO = false
	saved.Name = "Acme Azure AD"
	if err := orgs.SaveSSO(ctx, saved); err != nil {
		t.Fatalf("SaveSSO update failed: %v", err)
	}

	updated, err := orgs.GetSSO(ctx, org.ID)
	if err != nil {
		t.Fatalf("GetSSO after update failed: %v", err)
	}
	if updated.EnforceSSO || updated.Name != "Acme Azure AD" {
		t.Errorf("updated SSO config mismatch: %+v", updated)
	}
}
