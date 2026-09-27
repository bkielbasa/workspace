package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type organizationRepository struct {
	db *sql.DB
}

var _ identity.OrganizationRepository = (*organizationRepository)(nil)

func NewOrganizationRepository(db *sql.DB) identity.OrganizationRepository {
	return &organizationRepository{db: db}
}

// CreateOrganization inserts a new organization with the given name and unique slug.
func (r *organizationRepository) CreateOrganization(ctx context.Context, name, slug string) (*identity.Organization, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))

	var existing uuid.UUID
	err := r.db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE slug = $1`, slug).Scan(&existing)
	switch {
	case err == nil:
		return nil, identity.ErrOrganizationAlreadyExists
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("check existing org slug: %w", err)
	}

	org := &identity.Organization{}
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO organizations (name, slug, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW())
		RETURNING id, name, slug, created_at, updated_at
	`, name, slug).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, identity.ErrOrganizationAlreadyExists
		}
		return nil, fmt.Errorf("create organization: %w", err)
	}
	return org, nil
}

// GetOrganization fetches an organization by its ID.
func (r *organizationRepository) GetOrganization(ctx context.Context, id uuid.UUID) (*identity.Organization, error) {
	org := &identity.Organization{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, slug, created_at, updated_at
		FROM organizations WHERE id = $1
	`, id).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt, &org.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrOrganizationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get organization: %w", err)
	}
	return org, nil
}

// GetOrganizationBySlug fetches an organization by its unique slug.
func (r *organizationRepository) GetOrganizationBySlug(ctx context.Context, slug string) (*identity.Organization, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	org := &identity.Organization{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, slug, created_at, updated_at
		FROM organizations WHERE slug = $1
	`, slug).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt, &org.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrOrganizationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get organization by slug: %w", err)
	}
	return org, nil
}

// GetOrganizationByDomain retrieves both the organization and the matched custom domain record.
func (r *organizationRepository) GetOrganizationByDomain(ctx context.Context, domain string) (*identity.Organization, *identity.OrganizationDomain, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	org := &identity.Organization{}
	dom := &identity.OrganizationDomain{}
	var verifiedAt sql.NullTime

	err := r.db.QueryRowContext(ctx, `
		SELECT o.id, o.name, o.slug, o.created_at, o.updated_at,
		       d.id, d.organization_id, d.domain, d.verification_token, d.verified_at, d.created_at
		FROM organizations o
		JOIN organization_domains d ON o.id = d.organization_id
		WHERE lower(d.domain) = lower($1)
	`, domain).Scan(
		&org.ID, &org.Name, &org.Slug, &org.CreatedAt, &org.UpdatedAt,
		&dom.ID, &dom.OrganizationID, &dom.Domain, &dom.VerificationToken, &verifiedAt, &dom.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, identity.ErrOrgDomainNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get organization by domain: %w", err)
	}
	if verifiedAt.Valid {
		dom.VerifiedAt = &verifiedAt.Time
	}
	return org, dom, nil
}

// ListOrganizations lists all organizations ordered by creation date.
func (r *organizationRepository) ListOrganizations(ctx context.Context) ([]identity.Organization, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, slug, created_at, updated_at
		FROM organizations ORDER BY created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	var result []identity.Organization
	for rows.Next() {
		var o identity.Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

// DeleteOrganization deletes an organization and cascades to domains, members, and sso.
func (r *organizationRepository) DeleteOrganization(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM organizations WHERE id = $1`, id)
	return affectedOrNotFound(result, err, "delete organization", identity.ErrOrganizationNotFound)
}

// CreateDomain registers a new custom domain for an organization.
func (r *organizationRepository) CreateDomain(ctx context.Context, orgID uuid.UUID, domain, verificationToken string) (*identity.OrganizationDomain, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))

	var existing uuid.UUID
	err := r.db.QueryRowContext(ctx, `SELECT id FROM organization_domains WHERE lower(domain) = lower($1)`, domain).Scan(&existing)
	switch {
	case err == nil:
		return nil, identity.ErrOrgDomainAlreadyExists
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("check existing domain: %w", err)
	}

	dom := &identity.OrganizationDomain{}
	var verifiedAt sql.NullTime
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO organization_domains (organization_id, domain, verification_token, created_at)
		VALUES ($1, $2, $3, NOW())
		RETURNING id, organization_id, domain, verification_token, verified_at, created_at
	`, orgID, domain, verificationToken).Scan(
		&dom.ID, &dom.OrganizationID, &dom.Domain, &dom.VerificationToken, &verifiedAt, &dom.CreatedAt,
	)
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, identity.ErrOrganizationNotFound
		}
		if isUniqueViolation(err) {
			return nil, identity.ErrOrgDomainAlreadyExists
		}
		return nil, fmt.Errorf("create domain: %w", err)
	}
	if verifiedAt.Valid {
		dom.VerifiedAt = &verifiedAt.Time
	}
	return dom, nil
}

// GetDomain retrieves an organization domain record by its ID.
func (r *organizationRepository) GetDomain(ctx context.Context, id uuid.UUID) (*identity.OrganizationDomain, error) {
	dom := &identity.OrganizationDomain{}
	var verifiedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT id, organization_id, domain, verification_token, verified_at, created_at
		FROM organization_domains WHERE id = $1
	`, id).Scan(&dom.ID, &dom.OrganizationID, &dom.Domain, &dom.VerificationToken, &verifiedAt, &dom.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrOrgDomainNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}
	if verifiedAt.Valid {
		dom.VerifiedAt = &verifiedAt.Time
	}
	return dom, nil
}

// GetDomainByName retrieves an organization domain record by domain name (case-insensitive).
func (r *organizationRepository) GetDomainByName(ctx context.Context, domain string) (*identity.OrganizationDomain, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	dom := &identity.OrganizationDomain{}
	var verifiedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		SELECT id, organization_id, domain, verification_token, verified_at, created_at
		FROM organization_domains WHERE lower(domain) = lower($1)
	`, domain).Scan(&dom.ID, &dom.OrganizationID, &dom.Domain, &dom.VerificationToken, &verifiedAt, &dom.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrOrgDomainNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get domain by name: %w", err)
	}
	if verifiedAt.Valid {
		dom.VerifiedAt = &verifiedAt.Time
	}
	return dom, nil
}

// ListDomains lists all custom domains for a given organization.
func (r *organizationRepository) ListDomains(ctx context.Context, orgID uuid.UUID) ([]identity.OrganizationDomain, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, organization_id, domain, verification_token, verified_at, created_at
		FROM organization_domains WHERE organization_id = $1 ORDER BY created_at
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()

	var result []identity.OrganizationDomain
	for rows.Next() {
		var d identity.OrganizationDomain
		var verifiedAt sql.NullTime
		if err := rows.Scan(&d.ID, &d.OrganizationID, &d.Domain, &d.VerificationToken, &verifiedAt, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan domain: %w", err)
		}
		if verifiedAt.Valid {
			d.VerifiedAt = &verifiedAt.Time
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// UpdateDomain updates mutable fields of an organization domain (e.g. verified_at, token).
func (r *organizationRepository) UpdateDomain(ctx context.Context, domain *identity.OrganizationDomain) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE organization_domains
		SET domain = $2, verification_token = $3, verified_at = $4
		WHERE id = $1
	`, domain.ID, strings.ToLower(strings.TrimSpace(domain.Domain)), domain.VerificationToken, nullableTimePtr(domain.VerifiedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return identity.ErrOrgDomainAlreadyExists
		}
		return fmt.Errorf("update domain: %w", err)
	}
	return affectedOrNotFound(result, nil, "update domain", identity.ErrOrgDomainNotFound)
}

// DeleteDomain removes a custom domain from an organization.
func (r *organizationRepository) DeleteDomain(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM organization_domains WHERE id = $1`, id)
	return affectedOrNotFound(result, err, "delete domain", identity.ErrOrgDomainNotFound)
}

// AddMember adds a user to an organization with a specified role.
func (r *organizationRepository) AddMember(ctx context.Context, orgID, userID uuid.UUID, role string) (*identity.OrganizationMember, error) {
	if role != identity.RoleOwner && role != identity.RoleAdmin && role != identity.RoleMember {
		return nil, identity.ErrInvalidRole
	}

	var existing uuid.UUID
	err := r.db.QueryRowContext(ctx, `
		SELECT id FROM organization_members WHERE organization_id = $1 AND user_id = $2
	`, orgID, userID).Scan(&existing)
	switch {
	case err == nil:
		return nil, identity.ErrMemberAlreadyExists
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("check existing member: %w", err)
	}

	mem := &identity.OrganizationMember{}
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO organization_members (organization_id, user_id, role, created_at)
		VALUES ($1, $2, $3, NOW())
		RETURNING id, organization_id, user_id, role, created_at
	`, orgID, userID, role).Scan(&mem.ID, &mem.OrganizationID, &mem.UserID, &mem.Role, &mem.CreatedAt)
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, identity.ErrOrganizationNotFound
		}
		if isUniqueViolation(err) {
			return nil, identity.ErrMemberAlreadyExists
		}
		return nil, fmt.Errorf("add member: %w", err)
	}
	return mem, nil
}

// GetMember fetches a specific member of an organization.
func (r *organizationRepository) GetMember(ctx context.Context, orgID, userID uuid.UUID) (*identity.OrganizationMember, error) {
	mem := &identity.OrganizationMember{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, organization_id, user_id, role, created_at
		FROM organization_members WHERE organization_id = $1 AND user_id = $2
	`, orgID, userID).Scan(&mem.ID, &mem.OrganizationID, &mem.UserID, &mem.Role, &mem.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrMemberNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get member: %w", err)
	}
	return mem, nil
}

// ListMembers lists all members of an organization ordered by creation date.
func (r *organizationRepository) ListMembers(ctx context.Context, orgID uuid.UUID) ([]identity.OrganizationMember, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, organization_id, user_id, role, created_at
		FROM organization_members WHERE organization_id = $1 ORDER BY created_at
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	var result []identity.OrganizationMember
	for rows.Next() {
		var m identity.OrganizationMember
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.UserID, &m.Role, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// UpdateMemberRole updates the role of an organization member.
func (r *organizationRepository) UpdateMemberRole(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	if role != identity.RoleOwner && role != identity.RoleAdmin && role != identity.RoleMember {
		return identity.ErrInvalidRole
	}

	result, err := r.db.ExecContext(ctx, `
		UPDATE organization_members SET role = $3
		WHERE organization_id = $1 AND user_id = $2
	`, orgID, userID, role)
	return affectedOrNotFound(result, err, "update member role", identity.ErrMemberNotFound)
}

// RemoveMember removes a user from an organization.
func (r *organizationRepository) RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM organization_members
		WHERE organization_id = $1 AND user_id = $2
	`, orgID, userID)
	return affectedOrNotFound(result, err, "remove member", identity.ErrMemberNotFound)
}

// GetSSO retrieves the SSO configuration for an organization.
func (r *organizationRepository) GetSSO(ctx context.Context, orgID uuid.UUID) (*identity.OrganizationSSO, error) {
	sso := &identity.OrganizationSSO{}
	var scopesStr sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, organization_id, name, issuer, client_id, client_secret,
		       COALESCE(scopes, '{}'), enforce_sso, auto_provision, enabled, created_at, updated_at
		FROM organization_sso WHERE organization_id = $1
	`, orgID).Scan(
		&sso.ID, &sso.OrganizationID, &sso.Name, &sso.Issuer, &sso.ClientID, &sso.ClientSecret,
		&scopesStr, &sso.EnforceSSO, &sso.AutoProvision, &sso.Enabled, &sso.CreatedAt, &sso.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrSSONotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get sso: %w", err)
	}
	sso.Scopes = parseTextArray(scopesStr.String)
	return sso, nil
}

// SaveSSO creates or updates the SSO configuration for an organization.
func (r *organizationRepository) SaveSSO(ctx context.Context, sso *identity.OrganizationSSO) error {
	if sso.OrganizationID == uuid.Nil {
		return errors.New("organization_id cannot be nil")
	}

	name := sso.Name
	if name == "" {
		name = "Single Sign-On"
	}

	scopes := sso.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email"}
	}

	err := r.db.QueryRowContext(ctx, `
		INSERT INTO organization_sso (
			organization_id, name, issuer, client_id, client_secret, scopes,
			enforce_sso, auto_provision, enabled, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		ON CONFLICT (organization_id) DO UPDATE SET
			name = EXCLUDED.name,
			issuer = EXCLUDED.issuer,
			client_id = EXCLUDED.client_id,
			client_secret = EXCLUDED.client_secret,
			scopes = EXCLUDED.scopes,
			enforce_sso = EXCLUDED.enforce_sso,
			auto_provision = EXCLUDED.auto_provision,
			enabled = EXCLUDED.enabled,
			updated_at = NOW()
		RETURNING id, created_at, updated_at
	`, sso.OrganizationID, name, sso.Issuer, sso.ClientID, sso.ClientSecret,
		scopes, sso.EnforceSSO, sso.AutoProvision, sso.Enabled,
	).Scan(&sso.ID, &sso.CreatedAt, &sso.UpdatedAt)

	if err != nil {
		if isForeignKeyViolation(err) {
			return identity.ErrOrganizationNotFound
		}
		return fmt.Errorf("save sso: %w", err)
	}
	sso.Name = name
	sso.Scopes = scopes
	return nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint") || strings.Contains(msg, "23505")
}

func isForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "foreign key") || strings.Contains(msg, "23503")
}

func nullableTimePtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC()
}
