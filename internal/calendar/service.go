package calendar

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Repository is the persistence required by Service.
type Repository interface {
	Get(ctx context.Context, userID, eventID uuid.UUID) (*Event, error)
	GetByResource(ctx context.Context, userID uuid.UUID, resource string) (*Event, error)
	List(ctx context.Context, userID uuid.UUID) ([]Event, error)
	Put(ctx context.Context, e Event) (*Event, error)
	Delete(ctx context.Context, userID, eventID uuid.UUID) error
	DeleteByResource(ctx context.Context, userID uuid.UUID, resource string) error
}

// Service is the calendar business API used by CalDAV, the web UI, and others.
type Service struct {
	repo   Repository
	tracer trace.Tracer
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, tracer: otel.Tracer("calendar")}
}

func (s *Service) Delete(ctx context.Context, userID, eventID uuid.UUID) error {
	ctx, span := s.tracer.Start(ctx, "calendar.delete")
	defer span.End()
	return s.repo.Delete(ctx, userID, eventID)
}

func (s *Service) DeleteByResource(ctx context.Context, userID uuid.UUID, resource string) error {
	ctx, span := s.tracer.Start(ctx, "calendar.delete_by_resource")
	defer span.End()
	return s.repo.DeleteByResource(ctx, userID, resource)
}

func (s *Service) Get(ctx context.Context, userID, eventID uuid.UUID) (*Event, error) {
	ctx, span := s.tracer.Start(ctx, "calendar.get")
	defer span.End()
	return s.repo.Get(ctx, userID, eventID)
}

func (s *Service) GetByResource(ctx context.Context, userID uuid.UUID, resource string) (*Event, error) {
	ctx, span := s.tracer.Start(ctx, "calendar.get_by_resource")
	defer span.End()
	return s.repo.GetByResource(ctx, userID, resource)
}

func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Event, error) {
	ctx, span := s.tracer.Start(ctx, "calendar.list")
	defer span.End()
	return s.repo.List(ctx, userID)
}

func (s *Service) Put(ctx context.Context, e Event) (*Event, error) {
	ctx, span := s.tracer.Start(ctx, "calendar.put")
	defer span.End()
	if e.UserID == uuid.Nil || e.Resource == "" {
		return nil, ErrInvalidEvent
	}
	if e.ETag == "" {
		e.ETag = uuid.NewString()
	}
	return s.repo.Put(ctx, e)
}
