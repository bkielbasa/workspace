package contacts

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Repository is the persistence required by Service.
type Repository interface {
	List(ctx context.Context, userID uuid.UUID) ([]Contact, error)
	Get(ctx context.Context, userID, contactID uuid.UUID) (Contact, error)
	ByEmail(ctx context.Context, userID uuid.UUID, email string) (Contact, error)
	Put(ctx context.Context, userID uuid.UUID, id *uuid.UUID, ct Contact) (*Contact, error)
	Delete(ctx context.Context, userID uuid.UUID, email string) error
	DeleteByID(ctx context.Context, userID, contactID uuid.UUID) error
}

// CardBuilder rebuilds a vCard from structured fields. The format package implements this.
type CardBuilder func(ct Contact, previousCard string) string

type Service struct {
	repo   Repository
	build  CardBuilder
	tracer trace.Tracer
}

func NewService(repo Repository, build CardBuilder) *Service {
	return &Service{repo: repo, build: build, tracer: otel.Tracer("contacts")}
}

func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Contact, error) {
	ctx, span := s.tracer.Start(ctx, "contacts.list")
	defer span.End()
	return s.repo.List(ctx, userID)
}

func (s *Service) Get(ctx context.Context, userID, contactID uuid.UUID) (Contact, error) {
	ctx, span := s.tracer.Start(ctx, "contacts.get")
	defer span.End()
	return s.repo.Get(ctx, userID, contactID)
}

func (s *Service) ByEmail(ctx context.Context, userID uuid.UUID, email string) (Contact, error) {
	ctx, span := s.tracer.Start(ctx, "contacts.by_email")
	defer span.End()
	return s.repo.ByEmail(ctx, userID, email)
}

// PutStructured stores a contact from structured fields and rebuilds the card.
func (s *Service) PutStructured(ctx context.Context, userID uuid.UUID, id *uuid.UUID, ct Contact) (*Contact, error) {
	ctx, span := s.tracer.Start(ctx, "contacts.put")
	defer span.End()
	Normalize(&ct)

	var prev string
	if id != nil && *id != uuid.Nil {
		if old, err := s.repo.Get(ctx, userID, *id); err == nil {
			prev = old.VCard
		}
	} else if ct.Email != "" {
		if old, err := s.repo.ByEmail(ctx, userID, ct.Email); err == nil && old.ID != uuid.Nil {
			prev = old.VCard
		}
	}
	if strings.TrimSpace(ct.UID) == "" {
		ct.UID = uuid.NewString()
	}
	if s.build != nil {
		ct.VCard = s.build(ct, prev)
	}
	return s.repo.Put(ctx, userID, id, ct)
}

// PutCard stores an incoming card; the card body is authoritative.
func (s *Service) PutCard(ctx context.Context, userID uuid.UUID, id *uuid.UUID, ct Contact) (*Contact, error) {
	ctx, span := s.tracer.Start(ctx, "contacts.put_card")
	defer span.End()
	Normalize(&ct)
	if strings.TrimSpace(ct.VCard) == "" && s.build != nil {
		ct.VCard = s.build(ct, "")
	}
	return s.repo.Put(ctx, userID, id, ct)
}

func (s *Service) Delete(ctx context.Context, userID uuid.UUID, email string) error {
	ctx, span := s.tracer.Start(ctx, "contacts.delete")
	defer span.End()
	return s.repo.Delete(ctx, userID, email)
}

func (s *Service) DeleteByID(ctx context.Context, userID, contactID uuid.UUID) error {
	ctx, span := s.tracer.Start(ctx, "contacts.delete_by_id")
	defer span.End()
	return s.repo.DeleteByID(ctx, userID, contactID)
}
