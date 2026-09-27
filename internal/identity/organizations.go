package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// OrganizationRepository defines the persistence contract for organizations,
// custom domains, memberships, and SSO configurations.
type OrganizationRepository interface {
	// Organizations
	CreateOrganization(ctx context.Context, name, slug string) (*Organization, error)
	GetOrganization(ctx context.Context, id uuid.UUID) (*Organization, error)
	GetOrganizationBySlug(ctx context.Context, slug string) (*Organization, error)
	GetOrganizationByDomain(ctx context.Context, domain string) (*Organization, *OrganizationDomain, error)
	ListOrganizations(ctx context.Context) ([]Organization, error)
	DeleteOrganization(ctx context.Context, id uuid.UUID) error

	// Domains
	CreateDomain(ctx context.Context, orgID uuid.UUID, domain, verificationToken string) (*OrganizationDomain, error)
	GetDomain(ctx context.Context, id uuid.UUID) (*OrganizationDomain, error)
	GetDomainByName(ctx context.Context, domain string) (*OrganizationDomain, error)
	ListDomains(ctx context.Context, orgID uuid.UUID) ([]OrganizationDomain, error)
	UpdateDomain(ctx context.Context, domain *OrganizationDomain) error
	DeleteDomain(ctx context.Context, id uuid.UUID) error

	// Members
	AddMember(ctx context.Context, orgID, userID uuid.UUID, role string) (*OrganizationMember, error)
	GetMember(ctx context.Context, orgID, userID uuid.UUID) (*OrganizationMember, error)
	ListMembers(ctx context.Context, orgID uuid.UUID) ([]OrganizationMember, error)
	UpdateMemberRole(ctx context.Context, orgID, userID uuid.UUID, role string) error
	RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error

	// SSO
	GetSSO(ctx context.Context, orgID uuid.UUID) (*OrganizationSSO, error)
	SaveSSO(ctx context.Context, sso *OrganizationSSO) error
}

// MemoryOrganizationRepository provides an in-memory implementation of OrganizationRepository
// suitable for unit tests and local development.
type MemoryOrganizationRepository struct {
	mu            sync.RWMutex
	organizations map[uuid.UUID]*Organization
	orgsBySlug    map[string]*Organization
	domains       map[uuid.UUID]*OrganizationDomain
	domainsByName map[string]*OrganizationDomain
	members       map[uuid.UUID][]*OrganizationMember
	sso           map[uuid.UUID]*OrganizationSSO
}

var _ OrganizationRepository = (*MemoryOrganizationRepository)(nil)

func NewMemoryOrganizationRepository() *MemoryOrganizationRepository {
	return &MemoryOrganizationRepository{
		organizations: make(map[uuid.UUID]*Organization),
		orgsBySlug:    make(map[string]*Organization),
		domains:       make(map[uuid.UUID]*OrganizationDomain),
		domainsByName: make(map[string]*OrganizationDomain),
		members:       make(map[uuid.UUID][]*OrganizationMember),
		sso:           make(map[uuid.UUID]*OrganizationSSO),
	}
}

func (m *MemoryOrganizationRepository) CreateOrganization(_ context.Context, name, slug string) (*Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slug = strings.ToLower(strings.TrimSpace(slug))
	if _, exists := m.orgsBySlug[slug]; exists {
		return nil, ErrOrganizationAlreadyExists
	}

	now := time.Now().UTC()
	org := &Organization{
		ID:        uuid.New(),
		Name:      name,
		Slug:      slug,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.organizations[org.ID] = copyOrg(org)
	m.orgsBySlug[slug] = copyOrg(org)
	return copyOrg(org), nil
}

func (m *MemoryOrganizationRepository) GetOrganization(_ context.Context, id uuid.UUID) (*Organization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	org, exists := m.organizations[id]
	if !exists {
		return nil, ErrOrganizationNotFound
	}
	return copyOrg(org), nil
}

func (m *MemoryOrganizationRepository) GetOrganizationBySlug(_ context.Context, slug string) (*Organization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	slug = strings.ToLower(strings.TrimSpace(slug))
	org, exists := m.orgsBySlug[slug]
	if !exists {
		return nil, ErrOrganizationNotFound
	}
	return copyOrg(org), nil
}

func (m *MemoryOrganizationRepository) GetOrganizationByDomain(_ context.Context, domain string) (*Organization, *OrganizationDomain, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	domain = strings.ToLower(strings.TrimSpace(domain))
	d, exists := m.domainsByName[domain]
	if !exists {
		return nil, nil, ErrOrgDomainNotFound
	}
	org, exists := m.organizations[d.OrganizationID]
	if !exists {
		return nil, nil, ErrOrganizationNotFound
	}
	return copyOrg(org), copyDomain(d), nil
}

func (m *MemoryOrganizationRepository) ListOrganizations(_ context.Context) ([]Organization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Organization, 0, len(m.organizations))
	for _, o := range m.organizations {
		result = append(result, *copyOrg(o))
	}
	return result, nil
}

func (m *MemoryOrganizationRepository) DeleteOrganization(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	org, exists := m.organizations[id]
	if !exists {
		return ErrOrganizationNotFound
	}
	delete(m.orgsBySlug, org.Slug)
	delete(m.organizations, id)

	// Clean up related domains
	for domID, dom := range m.domains {
		if dom.OrganizationID == id {
			delete(m.domainsByName, dom.Domain)
			delete(m.domains, domID)
		}
	}
	// Clean up members & sso
	delete(m.members, id)
	delete(m.sso, id)
	return nil
}

func (m *MemoryOrganizationRepository) CreateDomain(_ context.Context, orgID uuid.UUID, domain, verificationToken string) (*OrganizationDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.organizations[orgID]; !exists {
		return nil, ErrOrganizationNotFound
	}

	domain = strings.ToLower(strings.TrimSpace(domain))
	if _, exists := m.domainsByName[domain]; exists {
		return nil, ErrOrgDomainAlreadyExists
	}

	now := time.Now().UTC()
	d := &OrganizationDomain{
		ID:                uuid.New(),
		OrganizationID:    orgID,
		Domain:            domain,
		VerificationToken: verificationToken,
		CreatedAt:         now,
	}
	m.domains[d.ID] = copyDomain(d)
	m.domainsByName[domain] = copyDomain(d)
	return copyDomain(d), nil
}

func (m *MemoryOrganizationRepository) GetDomain(_ context.Context, id uuid.UUID) (*OrganizationDomain, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	d, exists := m.domains[id]
	if !exists {
		return nil, ErrOrgDomainNotFound
	}
	return copyDomain(d), nil
}

func (m *MemoryOrganizationRepository) GetDomainByName(_ context.Context, domain string) (*OrganizationDomain, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	domain = strings.ToLower(strings.TrimSpace(domain))
	d, exists := m.domainsByName[domain]
	if !exists {
		return nil, ErrOrgDomainNotFound
	}
	return copyDomain(d), nil
}

func (m *MemoryOrganizationRepository) ListDomains(_ context.Context, orgID uuid.UUID) ([]OrganizationDomain, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []OrganizationDomain
	for _, d := range m.domains {
		if d.OrganizationID == orgID {
			result = append(result, *copyDomain(d))
		}
	}
	return result, nil
}

func (m *MemoryOrganizationRepository) UpdateDomain(_ context.Context, domain *OrganizationDomain) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, exists := m.domains[domain.ID]
	if !exists {
		return ErrOrgDomainNotFound
	}
	// If domain name changed, update lookup map
	oldName := existing.Domain
	newName := strings.ToLower(strings.TrimSpace(domain.Domain))
	if oldName != newName {
		if _, taken := m.domainsByName[newName]; taken {
			return ErrOrgDomainAlreadyExists
		}
		delete(m.domainsByName, oldName)
		m.domainsByName[newName] = copyDomain(domain)
	} else {
		m.domainsByName[newName] = copyDomain(domain)
	}

	m.domains[domain.ID] = copyDomain(domain)
	return nil
}

func (m *MemoryOrganizationRepository) DeleteDomain(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, exists := m.domains[id]
	if !exists {
		return ErrOrgDomainNotFound
	}
	delete(m.domainsByName, d.Domain)
	delete(m.domains, id)
	return nil
}

func (m *MemoryOrganizationRepository) AddMember(_ context.Context, orgID, userID uuid.UUID, role string) (*OrganizationMember, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !isValidRole(role) {
		return nil, ErrInvalidRole
	}
	if _, exists := m.organizations[orgID]; !exists {
		return nil, ErrOrganizationNotFound
	}

	for _, existing := range m.members[orgID] {
		if existing.UserID == userID {
			return nil, ErrMemberAlreadyExists
		}
	}

	member := &OrganizationMember{
		ID:             uuid.New(),
		OrganizationID: orgID,
		UserID:         userID,
		Role:           role,
		CreatedAt:      time.Now().UTC(),
	}
	m.members[orgID] = append(m.members[orgID], copyMember(member))
	return copyMember(member), nil
}

func (m *MemoryOrganizationRepository) GetMember(_ context.Context, orgID, userID uuid.UUID) (*OrganizationMember, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, member := range m.members[orgID] {
		if member.UserID == userID {
			return copyMember(member), nil
		}
	}
	return nil, ErrMemberNotFound
}

func (m *MemoryOrganizationRepository) ListMembers(_ context.Context, orgID uuid.UUID) ([]OrganizationMember, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	members := m.members[orgID]
	result := make([]OrganizationMember, 0, len(members))
	for _, mem := range members {
		result = append(result, *copyMember(mem))
	}
	return result, nil
}

func (m *MemoryOrganizationRepository) UpdateMemberRole(_ context.Context, orgID, userID uuid.UUID, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !isValidRole(role) {
		return ErrInvalidRole
	}

	for _, member := range m.members[orgID] {
		if member.UserID == userID {
			member.Role = role
			return nil
		}
	}
	return ErrMemberNotFound
}

func (m *MemoryOrganizationRepository) RemoveMember(_ context.Context, orgID, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	members := m.members[orgID]
	for i, member := range members {
		if member.UserID == userID {
			m.members[orgID] = append(members[:i], members[i+1:]...)
			return nil
		}
	}
	return ErrMemberNotFound
}

func (m *MemoryOrganizationRepository) GetSSO(_ context.Context, orgID uuid.UUID) (*OrganizationSSO, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sso, exists := m.sso[orgID]
	if !exists {
		return nil, ErrSSONotFound
	}
	return copySSO(sso), nil
}

func (m *MemoryOrganizationRepository) SaveSSO(_ context.Context, sso *OrganizationSSO) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sso.OrganizationID == uuid.Nil {
		return errors.New("organization_id cannot be nil")
	}
	if _, exists := m.organizations[sso.OrganizationID]; !exists {
		return ErrOrganizationNotFound
	}

	now := time.Now().UTC()
	copied := copySSO(sso)
	if copied.ID == uuid.Nil {
		copied.ID = uuid.New()
	}
	if copied.CreatedAt.IsZero() {
		copied.CreatedAt = now
	}
	copied.UpdatedAt = now
	if copied.Name == "" {
		copied.Name = "Single Sign-On"
	}
	if len(copied.Scopes) == 0 {
		copied.Scopes = []string{"openid", "profile", "email"}
	}
	m.sso[sso.OrganizationID] = copied
	sso.ID = copied.ID
	sso.CreatedAt = copied.CreatedAt
	sso.UpdatedAt = copied.UpdatedAt
	sso.Name = copied.Name
	sso.Scopes = copied.Scopes
	return nil
}

// Organizations coordinates multi-tenant organization operations, custom domain management,
// member management, and SSO configuration.
type Organizations struct {
	repo   OrganizationRepository
	tracer trace.Tracer
}

func NewOrganizations(repo OrganizationRepository) *Organizations {
	return &Organizations{
		repo:   repo,
		tracer: otel.Tracer("organizations"),
	}
}

// Create creates a new organization with an initial custom domain and binds the owner.
func (o *Organizations) Create(ctx context.Context, ownerID uuid.UUID, name, initialDomain string) (*Organization, *OrganizationDomain, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.create")
	defer span.End()

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, errors.New("organization name cannot be empty")
	}

	initialDomain = strings.ToLower(strings.TrimSpace(initialDomain))
	if initialDomain == "" {
		return nil, nil, errors.New("initial domain cannot be empty")
	}

	slug := slugify(name)
	token, err := generateVerificationToken()
	if err != nil {
		return nil, nil, fmt.Errorf("generate verification token: %w", err)
	}

	org, err := o.repo.CreateOrganization(ctx, name, slug)
	if err != nil {
		return nil, nil, err
	}

	domain, err := o.repo.CreateDomain(ctx, org.ID, initialDomain, token)
	if err != nil {
		_ = o.repo.DeleteOrganization(context.WithoutCancel(ctx), org.ID)
		return nil, nil, err
	}

	if ownerID != uuid.Nil {
		if _, err := o.repo.AddMember(ctx, org.ID, ownerID, RoleOwner); err != nil {
			_ = o.repo.DeleteOrganization(context.WithoutCancel(ctx), org.ID)
			return nil, nil, fmt.Errorf("assign owner: %w", err)
		}
	}

	return org, domain, nil
}

// CreateOrganization is an alias for Create.
func (o *Organizations) CreateOrganization(ctx context.Context, ownerID uuid.UUID, name, initialDomain string) (*Organization, *OrganizationDomain, error) {
	return o.Create(ctx, ownerID, name, initialDomain)
}

func (o *Organizations) Get(ctx context.Context, id uuid.UUID) (*Organization, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.get")
	defer span.End()
	return o.repo.GetOrganization(ctx, id)
}

// GetOrganization is an alias for Get.
func (o *Organizations) GetOrganization(ctx context.Context, id uuid.UUID) (*Organization, error) {
	return o.Get(ctx, id)
}

func (o *Organizations) GetBySlug(ctx context.Context, slug string) (*Organization, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.get_by_slug")
	defer span.End()
	return o.repo.GetOrganizationBySlug(ctx, strings.ToLower(strings.TrimSpace(slug)))
}

func (o *Organizations) GetByDomain(ctx context.Context, domain string) (*Organization, *OrganizationDomain, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.get_by_domain")
	defer span.End()
	return o.repo.GetOrganizationByDomain(ctx, strings.ToLower(strings.TrimSpace(domain)))
}

// GetOrganizationByDomain is an alias for GetByDomain.
func (o *Organizations) GetOrganizationByDomain(ctx context.Context, domain string) (*Organization, *OrganizationDomain, error) {
	return o.GetByDomain(ctx, domain)
}

func (o *Organizations) List(ctx context.Context) ([]Organization, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.list")
	defer span.End()
	return o.repo.ListOrganizations(ctx)
}

// Domain management methods

func (o *Organizations) CreateDomain(ctx context.Context, orgID uuid.UUID, domain string) (*OrganizationDomain, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.create_domain")
	defer span.End()

	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, errors.New("domain cannot be empty")
	}
	token, err := generateVerificationToken()
	if err != nil {
		return nil, fmt.Errorf("generate verification token: %w", err)
	}
	return o.repo.CreateDomain(ctx, orgID, domain, token)
}

func (o *Organizations) GetDomain(ctx context.Context, id uuid.UUID) (*OrganizationDomain, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.get_domain")
	defer span.End()
	return o.repo.GetDomain(ctx, id)
}

func (o *Organizations) GetDomainByName(ctx context.Context, domain string) (*OrganizationDomain, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.get_domain_by_name")
	defer span.End()
	return o.repo.GetDomainByName(ctx, strings.ToLower(strings.TrimSpace(domain)))
}

func (o *Organizations) ListDomains(ctx context.Context, orgID uuid.UUID) ([]OrganizationDomain, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.list_domains")
	defer span.End()
	return o.repo.ListDomains(ctx, orgID)
}

func (o *Organizations) UpdateDomain(ctx context.Context, domain *OrganizationDomain) error {
	ctx, span := o.tracer.Start(ctx, "organizations.update_domain")
	defer span.End()
	return o.repo.UpdateDomain(ctx, domain)
}

func (o *Organizations) DeleteDomain(ctx context.Context, id uuid.UUID) error {
	ctx, span := o.tracer.Start(ctx, "organizations.delete_domain")
	defer span.End()
	return o.repo.DeleteDomain(ctx, id)
}

// Member management methods

func (o *Organizations) ListMembers(ctx context.Context, orgID uuid.UUID) ([]OrganizationMember, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.list_members")
	defer span.End()
	return o.repo.ListMembers(ctx, orgID)
}

func (o *Organizations) AddMember(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	ctx, span := o.tracer.Start(ctx, "organizations.add_member")
	defer span.End()

	if !isValidRole(role) {
		return ErrInvalidRole
	}
	_, err := o.repo.AddMember(ctx, orgID, userID, role)
	return err
}

func (o *Organizations) GetMember(ctx context.Context, orgID, userID uuid.UUID) (*OrganizationMember, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.get_member")
	defer span.End()
	return o.repo.GetMember(ctx, orgID, userID)
}

func (o *Organizations) UpdateMemberRole(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	ctx, span := o.tracer.Start(ctx, "organizations.update_member_role")
	defer span.End()

	if !isValidRole(role) {
		return ErrInvalidRole
	}
	return o.repo.UpdateMemberRole(ctx, orgID, userID, role)
}

func (o *Organizations) RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error {
	ctx, span := o.tracer.Start(ctx, "organizations.remove_member")
	defer span.End()
	return o.repo.RemoveMember(ctx, orgID, userID)
}

// SSO management methods

func (o *Organizations) GetSSO(ctx context.Context, orgID uuid.UUID) (*OrganizationSSO, error) {
	ctx, span := o.tracer.Start(ctx, "organizations.get_sso")
	defer span.End()
	return o.repo.GetSSO(ctx, orgID)
}

func (o *Organizations) SaveSSO(ctx context.Context, sso *OrganizationSSO) error {
	ctx, span := o.tracer.Start(ctx, "organizations.save_sso")
	defer span.End()
	return o.repo.SaveSSO(ctx, sso)
}

// Helper functions

func isValidRole(role string) bool {
	return role == RoleOwner || role == RoleAdmin || role == RoleMember
}

func slugify(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var sb strings.Builder
	lastDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			lastDash = false
		} else if !lastDash && sb.Len() > 0 {
			sb.WriteByte('-')
			lastDash = true
		}
	}
	res := strings.TrimRight(sb.String(), "-")
	if res == "" {
		res = "org"
	}
	return res
}

func generateVerificationToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func copyOrg(o *Organization) *Organization {
	if o == nil {
		return nil
	}
	cp := *o
	return &cp
}

func copyDomain(d *OrganizationDomain) *OrganizationDomain {
	if d == nil {
		return nil
	}
	cp := *d
	if d.VerifiedAt != nil {
		v := *d.VerifiedAt
		cp.VerifiedAt = &v
	}
	return &cp
}

func copyMember(m *OrganizationMember) *OrganizationMember {
	if m == nil {
		return nil
	}
	cp := *m
	return &cp
}

func copySSO(s *OrganizationSSO) *OrganizationSSO {
	if s == nil {
		return nil
	}
	cp := *s
	if len(s.Scopes) > 0 {
		cp.Scopes = make([]string, len(s.Scopes))
		copy(cp.Scopes, s.Scopes)
	}
	return &cp
}
