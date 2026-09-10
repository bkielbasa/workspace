package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

type Label struct {
	ID     uuid.UUID
	UserID uuid.UUID
	Name   string
}

type Labels struct {
	db     *sql.DB
	tracer trace.Tracer
}

func NewLabels(db *sql.DB) *Labels {
	return &Labels{db: db, tracer: otel.Tracer("labels")}
}

func (l *Labels) Create(ctx context.Context, userID uuid.UUID, name string) (*Label, error) {
	ctx, span := l.tracer.Start(ctx, "labels.create")
	defer span.End()

	lb := &Label{}
	err := l.db.QueryRowContext(ctx, `
        INSERT INTO labels (user_id, name)
        VALUES ($1,$2)
        RETURNING id, user_id, name
    `, userID, name).Scan(&lb.ID, &lb.UserID, &lb.Name)
	if err != nil {
		return nil, fmt.Errorf("create label: %w", err)
	}
	return lb, nil
}

func (l *Labels) AddToMessage(ctx context.Context, messageID, labelID uuid.UUID) error {
	ctx, span := l.tracer.Start(ctx, "labels.add_to_message")
	defer span.End()

	_, err := l.db.ExecContext(ctx, `
        INSERT INTO message_labels (message_id, label_id)
        VALUES ($1,$2)
        ON CONFLICT DO NOTHING
    `, messageID, labelID)
	return err
}

func (l *Labels) RemoveFromMessage(ctx context.Context, messageID, labelID uuid.UUID) error {
	ctx, span := l.tracer.Start(ctx, "labels.remove_from_message")
	defer span.End()

	_, err := l.db.ExecContext(ctx, `
        DELETE FROM message_labels
        WHERE message_id=$1 AND label_id=$2
    `, messageID, labelID)
	return err
}
