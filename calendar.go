package main

import (
    "context"
    "database/sql"
    "fmt"
    "time"

    "github.com/google/uuid"
)

type Event struct {
    ID        uuid.UUID
    UserID    uuid.UUID
    Title     string
    StartsAt  time.Time
    EndsAt    time.Time
    ETag      string
    CreatedAt time.Time
    UpdatedAt time.Time
}

type Calendar struct {
    db *sql.DB
}

func (c *Calendar) List(ctx context.Context, userID uuid.UUID) ([]Event, error) {
    rows, err := c.db.QueryContext(ctx, `
        SELECT id, user_id, title, starts_at, ends_at, etag, created_at, updated_at
        FROM events WHERE user_id=$1 ORDER BY starts_at
    `, userID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var res []Event
    for rows.Next() {
        var e Event
        if err := rows.Scan(&e.ID, &e.UserID, &e.Title, &e.StartsAt, &e.EndsAt, &e.ETag, &e.CreatedAt, &e.UpdatedAt); err != nil {
            return nil, err
        }
        res = append(res, e)
    }
    return res, rows.Err()
}

func (c *Calendar) Upsert(ctx context.Context, userID uuid.UUID, title string, start, end time.Time) (*Event, error) {
    etag := uuid.New().String()
    e := &Event{}

    err := c.db.QueryRowContext(ctx, `
        INSERT INTO events (user_id, title, starts_at, ends_at, etag)
        VALUES ($1,$2,$3,$4,$5)
        RETURNING id, user_id, title, starts_at, ends_at, etag, created_at, updated_at
    `, userID, title, start, end, etag).Scan(
        &e.ID, &e.UserID, &e.Title, &e.StartsAt, &e.EndsAt, &e.ETag, &e.CreatedAt, &e.UpdatedAt,
    )
    if err != nil {
        return nil, fmt.Errorf("upsert event: %w", err)
    }
    return e, nil
}
