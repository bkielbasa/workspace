package mail

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type SearchRepository interface {
	Messages(ctx context.Context, userID uuid.UUID, q string) ([]Message, error)
}

type Search struct {
	repo   SearchRepository
	tracer trace.Tracer
}

func NewSearch(repo SearchRepository) *Search {
	return &Search{repo: repo, tracer: otel.Tracer("search")}
}

func (s *Search) Messages(ctx context.Context, userID uuid.UUID, q string) ([]Message, error) {
	ctx, span := s.tracer.Start(ctx, "search.messages")
	defer span.End()
	return s.repo.Messages(ctx, userID, q)
}

type LabelRepository interface {
	Create(ctx context.Context, userID uuid.UUID, name string) (*Label, error)
	AddToMessage(ctx context.Context, messageID, labelID uuid.UUID) error
	RemoveFromMessage(ctx context.Context, messageID, labelID uuid.UUID) error
}

type Labels struct {
	repo   LabelRepository
	tracer trace.Tracer
}

func NewLabels(repo LabelRepository) *Labels {
	return &Labels{repo: repo, tracer: otel.Tracer("labels")}
}

func (l *Labels) Create(ctx context.Context, userID uuid.UUID, name string) (*Label, error) {
	ctx, span := l.tracer.Start(ctx, "labels.create")
	defer span.End()
	return l.repo.Create(ctx, userID, name)
}

func (l *Labels) AddToMessage(ctx context.Context, messageID, labelID uuid.UUID) error {
	ctx, span := l.tracer.Start(ctx, "labels.add_to_message")
	defer span.End()
	return l.repo.AddToMessage(ctx, messageID, labelID)
}

func (l *Labels) RemoveFromMessage(ctx context.Context, messageID, labelID uuid.UUID) error {
	ctx, span := l.tracer.Start(ctx, "labels.remove_from_message")
	defer span.End()
	return l.repo.RemoveFromMessage(ctx, messageID, labelID)
}
