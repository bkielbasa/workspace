package identity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DNSResolver defines the interface for performing DNS TXT queries.
type DNSResolver interface {
	LookupTXT(ctx context.Context, domain string) ([]string, error)
}

// NetDNSResolver is a default DNSResolver implementation using net.Resolver.
type NetDNSResolver struct{}

func (n *NetDNSResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	var r net.Resolver
	return r.LookupTXT(ctx, domain)
}

// MemoryDomainRepository provides an in-memory implementation of DomainRepository
// suitable for unit tests and local development.
type MemoryDomainRepository struct {
	mu      sync.RWMutex
	domains map[uuid.UUID]*Domain
	byName  map[string]*Domain
}

var _ DomainRepository = (*MemoryDomainRepository)(nil)

func NewMemoryDomainRepository() *MemoryDomainRepository {
	return &MemoryDomainRepository{
		domains: make(map[uuid.UUID]*Domain),
		byName:  make(map[string]*Domain),
	}
}

func (m *MemoryDomainRepository) Create(_ context.Context, name string) (*Domain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name = strings.ToLower(strings.TrimSpace(name))
	if _, exists := m.byName[name]; exists {
		return nil, ErrDomainAlreadyExists
	}

	now := time.Now().UTC()
	d := &Domain{
		ID:        uuid.New(),
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.domains[d.ID] = copyGlobalDomain(d)
	m.byName[name] = copyGlobalDomain(d)
	return copyGlobalDomain(d), nil
}

func (m *MemoryDomainRepository) Get(_ context.Context, id uuid.UUID) (*Domain, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	d, exists := m.domains[id]
	if !exists {
		return nil, ErrDomainNotFound
	}
	return copyGlobalDomain(d), nil
}

func (m *MemoryDomainRepository) GetByName(_ context.Context, name string) (*Domain, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	name = strings.ToLower(strings.TrimSpace(name))
	d, exists := m.byName[name]
	if !exists {
		return nil, ErrDomainNotFound
	}
	return copyGlobalDomain(d), nil
}

func (m *MemoryDomainRepository) List(_ context.Context) ([]Domain, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Domain, 0, len(m.domains))
	for _, d := range m.domains {
		result = append(result, *copyGlobalDomain(d))
	}
	return result, nil
}

func (m *MemoryDomainRepository) Delete(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, exists := m.domains[id]
	if !exists {
		return ErrDomainNotFound
	}
	delete(m.byName, d.Name)
	delete(m.domains, id)
	return nil
}

func (m *MemoryDomainRepository) Exists(_ context.Context, name string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	name = strings.ToLower(strings.TrimSpace(name))
	_, exists := m.byName[name]
	return exists, nil
}

func copyGlobalDomain(d *Domain) *Domain {
	if d == nil {
		return nil
	}
	c := *d
	return &c
}

// DomainVerifier handles TXT record DNS verification for organization custom domains
// and synchronizes successfully verified domains to the global domains table.
type DomainVerifier struct {
	orgRepo       OrganizationRepository
	globalDomains DomainRepository
	resolver      DNSResolver
}

// NewDomainVerifier constructs a new DomainVerifier instance.
func NewDomainVerifier(orgRepo OrganizationRepository, globalDomains DomainRepository, resolver DNSResolver) *DomainVerifier {
	if resolver == nil {
		resolver = &NetDNSResolver{}
	}
	return &DomainVerifier{
		orgRepo:       orgRepo,
		globalDomains: globalDomains,
		resolver:      resolver,
	}
}

// Verify checks DNS TXT records for workspace-verify=<token>, marks the domain as verified,
// and ensures the domain is registered in the global domains repository.
func (v *DomainVerifier) Verify(ctx context.Context, domainID uuid.UUID) error {
	dom, err := v.orgRepo.GetDomain(ctx, domainID)
	if err != nil {
		return err
	}

	if dom.VerifiedAt != nil {
		if exists, err := v.globalDomains.Exists(ctx, dom.Domain); err != nil {
			return fmt.Errorf("check global domain: %w", err)
		} else if !exists {
			if _, err := v.globalDomains.Create(ctx, dom.Domain); err != nil && !errors.Is(err, ErrDomainAlreadyExists) {
				return fmt.Errorf("sync global domain: %w", err)
			}
		}
		return nil
	}

	records, err := v.resolver.LookupTXT(ctx, dom.Domain)
	if err != nil {
		return fmt.Errorf("lookup DNS TXT: %w", err)
	}

	expectedToken := "workspace-verify=" + dom.VerificationToken
	found := false
	for _, r := range records {
		if strings.Contains(r, expectedToken) {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("DNS TXT verification record not found for %s", dom.Domain)
	}

	now := time.Now().UTC()
	dom.VerifiedAt = &now
	if err := v.orgRepo.UpdateDomain(ctx, dom); err != nil {
		return fmt.Errorf("update domain: %w", err)
	}

	if exists, err := v.globalDomains.Exists(ctx, dom.Domain); err != nil {
		return fmt.Errorf("check global domain: %w", err)
	} else if !exists {
		if _, err := v.globalDomains.Create(ctx, dom.Domain); err != nil && !errors.Is(err, ErrDomainAlreadyExists) {
			return fmt.Errorf("sync global domain: %w", err)
		}
	}

	return nil
}
