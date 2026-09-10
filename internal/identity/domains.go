package identity

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type DomainRepository interface {
	Create(ctx context.Context, name string) (*Domain, error)
	Get(ctx context.Context, id uuid.UUID) (*Domain, error)
	GetByName(ctx context.Context, name string) (*Domain, error)
	List(ctx context.Context) ([]Domain, error)
	Delete(ctx context.Context, id uuid.UUID) error
	Exists(ctx context.Context, name string) (bool, error)
}

type Domains struct {
	repo   DomainRepository
	tracer trace.Tracer
}

func NewDomains(repo DomainRepository) *Domains {
	return &Domains{repo: repo, tracer: otel.Tracer("domains")}
}

func (d *Domains) Create(ctx context.Context, name string) (*Domain, error) {
	ctx, span := d.tracer.Start(ctx, "domains.create")
	defer span.End()
	return d.repo.Create(ctx, strings.ToLower(strings.TrimSpace(name)))
}

func (d *Domains) Get(ctx context.Context, id uuid.UUID) (*Domain, error) {
	ctx, span := d.tracer.Start(ctx, "domains.get")
	defer span.End()
	return d.repo.Get(ctx, id)
}

func (d *Domains) GetByName(ctx context.Context, name string) (*Domain, error) {
	ctx, span := d.tracer.Start(ctx, "domains.get_by_name")
	defer span.End()
	return d.repo.GetByName(ctx, strings.ToLower(strings.TrimSpace(name)))
}

func (d *Domains) List(ctx context.Context) ([]Domain, error) {
	ctx, span := d.tracer.Start(ctx, "domains.list")
	defer span.End()
	return d.repo.List(ctx)
}

func (d *Domains) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := d.tracer.Start(ctx, "domains.delete")
	defer span.End()
	return d.repo.Delete(ctx, id)
}

func (d *Domains) Exists(ctx context.Context, name string) (bool, error) {
	ctx, span := d.tracer.Start(ctx, "domains.exists")
	defer span.End()
	return d.repo.Exists(ctx, strings.ToLower(strings.TrimSpace(name)))
}
