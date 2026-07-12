package main

import (
    "context"
    "database/sql"
    "fmt"

    "github.com/google/uuid"
)

type Label struct {
    ID     uuid.UUID
    UserID uuid.UUID
    Name   string
}

type Labels struct{ db *sql.DB }

func (l *Labels) Create(ctx context.Context, userID uuid.UUID, name string) (*Label, error) {
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
    _, err := l.db.ExecContext(ctx, `
        INSERT INTO message_labels (message_id, label_id)
        VALUES ($1,$2)
        ON CONFLICT DO NOTHING
    `, messageID, labelID)
    return err
}

func (l *Labels) RemoveFromMessage(ctx context.Context, messageID, labelID uuid.UUID) error {
    _, err := l.db.ExecContext(ctx, `
        DELETE FROM message_labels
        WHERE message_id=$1 AND label_id=$2
    `, messageID, labelID)
    return err
}
