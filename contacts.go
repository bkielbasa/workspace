package main

import (
    "context"
    "database/sql"
    "fmt"
    "time"

    "github.com/google/uuid"
)

type Contact struct {
    ID        uuid.UUID
    UserID    uuid.UUID
    Email     string
    Name      string
    ETag      string
    CreatedAt time.Time
    UpdatedAt time.Time
}

type Contacts struct {
    db *sql.DB
}

func (c *Contacts) List(ctx context.Context, userID uuid.UUID) ([]Contact, error) {
    rows, err := c.db.QueryContext(ctx, `
        SELECT id, user_id, email, name, etag, created_at, updated_at
        FROM contacts
        WHERE user_id = $1
        ORDER BY updated_at DESC
    `, userID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var res []Contact
    for rows.Next() {
        var ct Contact
        if err := rows.Scan(&ct.ID, &ct.UserID, &ct.Email, &ct.Name, &ct.ETag, &ct.CreatedAt, &ct.UpdatedAt); err != nil {
            return nil, err
        }
        res = append(res, ct)
    }
    return res, rows.Err()
}

func (c *Contacts) Upsert(ctx context.Context, userID uuid.UUID, email, name string) (*Contact, error) {
    etag := uuid.New().String()

    ct := &Contact{}
    err := c.db.QueryRowContext(ctx, `
        INSERT INTO contacts (user_id, email, name, etag)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (user_id, email)
        DO UPDATE SET name = EXCLUDED.name, etag = EXCLUDED.etag, updated_at = NOW()
        RETURNING id, user_id, email, name, etag, created_at, updated_at
    `, userID, email, name, etag).Scan(
        &ct.ID,
        &ct.UserID,
        &ct.Email,
        &ct.Name,
        &ct.ETag,
        &ct.CreatedAt,
        &ct.UpdatedAt,
    )
    if err != nil {
        return nil, fmt.Errorf("upsert contact: %w", err)
    }
    return ct, nil
}

func (c *Contacts) Delete(ctx context.Context, userID uuid.UUID, email string) error {
    _, err := c.db.ExecContext(ctx, `
        DELETE FROM contacts
        WHERE user_id = $1 AND email = $2
    `, userID, email)
    return err
}
