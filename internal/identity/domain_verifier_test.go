package identity_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type mockDNSResolver struct {
	records map[string][]string
	err     error
}

func (m *mockDNSResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.records[domain], nil
}

func TestDomainVerificationSuccess(t *testing.T) {
	ctx := context.Background()
	resolver := &mockDNSResolver{
		records: map[string][]string{
			"acme.corp": {"v=spf1 ~all", "workspace-verify=valid-secret-token-123"},
		},
	}
	repo := identity.NewMemoryOrganizationRepository()
	globalDomains := identity.NewMemoryDomainRepository()

	org, err := repo.CreateOrganization(ctx, "Acme Corp", "acme")
	if err != nil {
		t.Fatalf("failed to create test org: %v", err)
	}

	dom, err := repo.CreateDomain(ctx, org.ID, "acme.corp", "valid-secret-token-123")
	if err != nil {
		t.Fatalf("failed to create test domain: %v", err)
	}

	verifier := identity.NewDomainVerifier(repo, globalDomains, resolver)
	if err := verifier.Verify(ctx, dom.ID); err != nil {
		t.Fatalf("expected verification to succeed, got: %v", err)
	}

	verified, err := repo.GetDomain(ctx, dom.ID)
	if err != nil || verified.VerifiedAt == nil {
		t.Fatalf("expected domain to be marked verified")
	}

	exists, err := globalDomains.Exists(ctx, "acme.corp")
	if err != nil || !exists {
		t.Fatalf("expected domain to be registered in global domains")
	}
}

func TestDomainVerificationMissingRecord(t *testing.T) {
	ctx := context.Background()
	resolver := &mockDNSResolver{
		records: map[string][]string{
			"acme.corp": {"v=spf1 ~all", "other-token-value"},
		},
	}
	repo := identity.NewMemoryOrganizationRepository()
	globalDomains := identity.NewMemoryDomainRepository()

	org, err := repo.CreateOrganization(ctx, "Acme Corp", "acme")
	if err != nil {
		t.Fatalf("failed to create test org: %v", err)
	}

	dom, err := repo.CreateDomain(ctx, org.ID, "acme.corp", "expected-secret-token")
	if err != nil {
		t.Fatalf("failed to create test domain: %v", err)
	}

	verifier := identity.NewDomainVerifier(repo, globalDomains, resolver)
	err = verifier.Verify(ctx, dom.ID)
	if err == nil {
		t.Fatalf("expected verification to fail due to missing record, got nil")
	}
	if !strings.Contains(err.Error(), "record not found") {
		t.Fatalf("expected error to mention record not found, got: %v", err)
	}

	verified, err := repo.GetDomain(ctx, dom.ID)
	if err != nil {
		t.Fatalf("failed to get domain: %v", err)
	}
	if verified.VerifiedAt != nil {
		t.Fatalf("expected domain to remain unverified, got VerifiedAt: %v", verified.VerifiedAt)
	}

	exists, err := globalDomains.Exists(ctx, "acme.corp")
	if err != nil || exists {
		t.Fatalf("expected domain not to be registered in global domains")
	}
}

func TestDomainVerificationDNSError(t *testing.T) {
	ctx := context.Background()
	dnsErr := errors.New("network timeout reaching dns server")
	resolver := &mockDNSResolver{
		err: dnsErr,
	}
	repo := identity.NewMemoryOrganizationRepository()
	globalDomains := identity.NewMemoryDomainRepository()

	org, err := repo.CreateOrganization(ctx, "Acme Corp", "acme")
	if err != nil {
		t.Fatalf("failed to create test org: %v", err)
	}

	dom, err := repo.CreateDomain(ctx, org.ID, "acme.corp", "valid-secret-token-123")
	if err != nil {
		t.Fatalf("failed to create test domain: %v", err)
	}

	verifier := identity.NewDomainVerifier(repo, globalDomains, resolver)
	err = verifier.Verify(ctx, dom.ID)
	if err == nil {
		t.Fatalf("expected verification to fail due to DNS error, got nil")
	}
	if !errors.Is(err, dnsErr) && !strings.Contains(err.Error(), dnsErr.Error()) {
		t.Fatalf("expected error wrapping DNS error, got: %v", err)
	}

	verified, err := repo.GetDomain(ctx, dom.ID)
	if err != nil {
		t.Fatalf("failed to get domain: %v", err)
	}
	if verified.VerifiedAt != nil {
		t.Fatalf("expected domain to remain unverified, got VerifiedAt: %v", verified.VerifiedAt)
	}

	exists, err := globalDomains.Exists(ctx, "acme.corp")
	if err != nil || exists {
		t.Fatalf("expected domain not to be registered in global domains")
	}
}

func TestDomainVerificationAlreadyVerified(t *testing.T) {
	ctx := context.Background()
	// Resolver has NO records to prove that resolver is not queried or missing records don't fail an already-verified domain
	resolver := &mockDNSResolver{
		records: map[string][]string{},
	}
	repo := identity.NewMemoryOrganizationRepository()
	globalDomains := identity.NewMemoryDomainRepository()

	org, err := repo.CreateOrganization(ctx, "Acme Corp", "acme")
	if err != nil {
		t.Fatalf("failed to create test org: %v", err)
	}

	dom, err := repo.CreateDomain(ctx, org.ID, "acme.corp", "valid-secret-token-123")
	if err != nil {
		t.Fatalf("failed to create test domain: %v", err)
	}

	// Mark domain as already verified with a specific timestamp
	origVerifiedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	dom.VerifiedAt = &origVerifiedAt
	if err := repo.UpdateDomain(ctx, dom); err != nil {
		t.Fatalf("failed to update domain: %v", err)
	}

	verifier := identity.NewDomainVerifier(repo, globalDomains, resolver)
	if err := verifier.Verify(ctx, dom.ID); err != nil {
		t.Fatalf("expected verify on already-verified domain to succeed, got: %v", err)
	}

	verified, err := repo.GetDomain(ctx, dom.ID)
	if err != nil {
		t.Fatalf("failed to get domain: %v", err)
	}
	if verified.VerifiedAt == nil || !verified.VerifiedAt.Equal(origVerifiedAt) {
		t.Fatalf("expected VerifiedAt to remain %v, got %v", origVerifiedAt, verified.VerifiedAt)
	}

	exists, err := globalDomains.Exists(ctx, "acme.corp")
	if err != nil || !exists {
		t.Fatalf("expected domain to be synchronized into global domains")
	}
}

func TestDomainVerificationNotFound(t *testing.T) {
	ctx := context.Background()
	resolver := &mockDNSResolver{}
	repo := identity.NewMemoryOrganizationRepository()
	globalDomains := identity.NewMemoryDomainRepository()

	verifier := identity.NewDomainVerifier(repo, globalDomains, resolver)
	err := verifier.Verify(ctx, uuid.New())
	if !errors.Is(err, identity.ErrOrgDomainNotFound) {
		t.Fatalf("expected ErrOrgDomainNotFound, got: %v", err)
	}
}

func TestNewDomainVerifierDefaultResolver(t *testing.T) {
	repo := identity.NewMemoryOrganizationRepository()
	globalDomains := identity.NewMemoryDomainRepository()

	verifier := identity.NewDomainVerifier(repo, globalDomains, nil)
	if verifier == nil {
		t.Fatalf("expected verifier to be initialized with default resolver")
	}
}

