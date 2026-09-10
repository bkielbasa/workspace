package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type AliasRepository interface {
	Create(ctx context.Context, domainID uuid.UUID, address, destination string) (*Alias, error)
	Get(ctx context.Context, id uuid.UUID) (*Alias, error)
	ListByDomain(ctx context.Context, domainID uuid.UUID) ([]Alias, error)
	Delete(ctx context.Context, id uuid.UUID) error
	Resolve(ctx context.Context, address string) (string, error)
}

type Aliases struct {
	repo    AliasRepository
	domains *Domains
	tracer  trace.Tracer
}

func NewAliases(repo AliasRepository, domains *Domains) *Aliases {
	return &Aliases{repo: repo, domains: domains, tracer: otel.Tracer("aliases")}
}

func (a *Aliases) Create(ctx context.Context, domainID uuid.UUID, address, destination string) (*Alias, error) {
	ctx, span := a.tracer.Start(ctx, "aliases.create")
	defer span.End()
	address = strings.ToLower(strings.TrimSpace(address))
	destination = strings.ToLower(strings.TrimSpace(destination))
	if _, err := a.domains.Get(ctx, domainID); err != nil {
		return nil, fmt.Errorf("domain lookup: %w", err)
	}
	return a.repo.Create(ctx, domainID, address, destination)
}

func (a *Aliases) Get(ctx context.Context, id uuid.UUID) (*Alias, error) {
	ctx, span := a.tracer.Start(ctx, "aliases.get")
	defer span.End()
	return a.repo.Get(ctx, id)
}

func (a *Aliases) ListByDomain(ctx context.Context, domainID uuid.UUID) ([]Alias, error) {
	ctx, span := a.tracer.Start(ctx, "aliases.list_by_domain")
	defer span.End()
	return a.repo.ListByDomain(ctx, domainID)
}

func (a *Aliases) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := a.tracer.Start(ctx, "aliases.delete")
	defer span.End()
	return a.repo.Delete(ctx, id)
}

func (a *Aliases) Resolve(ctx context.Context, address string) (string, error) {
	ctx, span := a.tracer.Start(ctx, "aliases.resolve")
	defer span.End()
	return a.repo.Resolve(ctx, strings.ToLower(strings.TrimSpace(address)))
}
