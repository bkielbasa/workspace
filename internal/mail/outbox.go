package mail

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type OutboxRepository interface {
	Enqueue(ctx context.Context, recipient, data string) error
	FetchBatch(ctx context.Context, limit int) ([]OutboxMessage, error)
	MarkSuccess(ctx context.Context, id string) error
	MarkFailure(ctx context.Context, id string, attempts int) error
}

type Outbox struct {
	repo   OutboxRepository
	tracer trace.Tracer
}

func NewOutbox(repo OutboxRepository) *Outbox {
	return &Outbox{repo: repo, tracer: otel.Tracer("outbox")}
}

func (o *Outbox) Enqueue(ctx context.Context, recipient, data string) error {
	ctx, span := o.tracer.Start(ctx, "outbox.enqueue")
	defer span.End()
	return o.repo.Enqueue(ctx, recipient, data)
}

func (o *Outbox) FetchBatch(ctx context.Context, limit int) ([]OutboxMessage, error) {
	ctx, span := o.tracer.Start(ctx, "outbox.fetch_batch")
	defer span.End()
	return o.repo.FetchBatch(ctx, limit)
}

func (o *Outbox) MarkSuccess(ctx context.Context, id string) error {
	ctx, span := o.tracer.Start(ctx, "outbox.mark_success")
	defer span.End()
	return o.repo.MarkSuccess(ctx, id)
}

func (o *Outbox) MarkFailure(ctx context.Context, id string, attempts int) error {
	ctx, span := o.tracer.Start(ctx, "outbox.mark_failure")
	defer span.End()
	return o.repo.MarkFailure(ctx, id, attempts)
}
