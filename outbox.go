package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type Outbox struct {
	db     *sql.DB
	tracer trace.Tracer
}

func NewOutbox(db *sql.DB) *Outbox {
	return &Outbox{db: db, tracer: otel.Tracer("outbox")}
}

func (o *Outbox) Enqueue(ctx context.Context, recipient, data string) error {
	ctx, span := o.tracer.Start(ctx, "outbox.enqueue")
	defer span.End()

	_, err := o.db.ExecContext(ctx, `
        INSERT INTO outbox (recipient, data)
        VALUES ($1, $2)
    `, recipient, data)
	if err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	return nil
}

func (o *Outbox) FetchBatch(ctx context.Context, limit int) ([]OutboxMessage, error) {
	ctx, span := o.tracer.Start(ctx, "outbox.fetch_batch")
	defer span.End()

	rows, err := o.db.QueryContext(ctx, `
        SELECT id, recipient, data, attempts
        FROM outbox
        WHERE next_attempt_at <= NOW()
        ORDER BY created_at
        LIMIT $1
    `, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []OutboxMessage
	for rows.Next() {
		var m OutboxMessage
		if err := rows.Scan(&m.ID, &m.Recipient, &m.Data, &m.Attempts); err != nil {
			return nil, err
		}
		res = append(res, m)
	}
	return res, rows.Err()
}

func (o *Outbox) MarkSuccess(ctx context.Context, id string) error {
	ctx, span := o.tracer.Start(ctx, "outbox.mark_success")
	defer span.End()

	_, err := o.db.ExecContext(ctx, `DELETE FROM outbox WHERE id = $1`, id)
	return err
}

func (o *Outbox) MarkFailure(ctx context.Context, id string, attempts int) error {
	ctx, span := o.tracer.Start(ctx, "outbox.mark_failure")
	defer span.End()

	next := time.Now().Add(time.Duration(attempts+1) * time.Minute)
	_, err := o.db.ExecContext(ctx, `
        UPDATE outbox
        SET attempts = $2, next_attempt_at = $3
        WHERE id = $1
    `, id, attempts+1, next)
	return err
}

type OutboxMessage struct {
	ID        string
	Recipient string
	Data      string
	Attempts  int
}
