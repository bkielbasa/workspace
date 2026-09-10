package identity

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type SessionRepository interface {
	Create(ctx context.Context, userID uuid.UUID, ttl time.Duration) (*Session, error)
	GetByToken(ctx context.Context, token string) (*Session, error)
	Delete(ctx context.Context, token string) error
	DeleteAllForUser(ctx context.Context, userID uuid.UUID) error
	CleanupExpired(ctx context.Context) (int64, error)
}

type Sessions struct {
	repo   SessionRepository
	tracer trace.Tracer
}

func NewSessions(repo SessionRepository) *Sessions {
	return &Sessions{repo: repo, tracer: otel.Tracer("sessions")}
}

func (s *Sessions) Create(ctx context.Context, userID uuid.UUID, ttl time.Duration) (*Session, error) {
	ctx, span := s.tracer.Start(ctx, "sessions.create")
	defer span.End()
	return s.repo.Create(ctx, userID, ttl)
}

func (s *Sessions) GetByToken(ctx context.Context, token string) (*Session, error) {
	ctx, span := s.tracer.Start(ctx, "sessions.get_by_token")
	defer span.End()
	return s.repo.GetByToken(ctx, token)
}

func (s *Sessions) Delete(ctx context.Context, token string) error {
	ctx, span := s.tracer.Start(ctx, "sessions.delete")
	defer span.End()
	return s.repo.Delete(ctx, token)
}

func (s *Sessions) DeleteAllForUser(ctx context.Context, userID uuid.UUID) error {
	ctx, span := s.tracer.Start(ctx, "sessions.delete_all_for_user")
	defer span.End()
	return s.repo.DeleteAllForUser(ctx, userID)
}

func (s *Sessions) CleanupExpired(ctx context.Context) (int64, error) {
	ctx, span := s.tracer.Start(ctx, "sessions.cleanup_expired")
	defer span.End()
	return s.repo.CleanupExpired(ctx)
}
