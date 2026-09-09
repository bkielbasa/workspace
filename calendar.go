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
	UID       string // client-supplied iCalendar UID
	ICS       string // raw iCalendar body, served back verbatim to clients
	Resource  string // href name the client used (defaults to the id)
	ETag      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Href is the CalDAV resource name (without extension) a client uses to address
// the event; it is the resource the client chose, or the id for web-created
// events.
func (e Event) Href() string {
	if e.Resource != "" {
		return e.Resource
	}
	return e.ID.String()
}

type Calendar struct {
	db *sql.DB
}

const eventColumns = `id, user_id, title, starts_at, ends_at, COALESCE(uid, ''), COALESCE(ics, ''), COALESCE(resource, id::text), etag, created_at, updated_at`

func scanEvent(row scanRow) (Event, error) {
	var e Event
	var starts, ends sql.NullTime
	err := row.Scan(&e.ID, &e.UserID, &e.Title, &starts, &ends, &e.UID, &e.ICS, &e.Resource, &e.ETag, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return e, err
	}
	e.StartsAt = starts.Time
	e.EndsAt = ends.Time
	return e, nil
}

func (c *Calendar) Delete(ctx context.Context, userID, eventID uuid.UUID) error {
	_, err := c.db.ExecContext(ctx, `
        DELETE FROM events
        WHERE id = $1 AND user_id = $2
    `, eventID, userID)
	return err
}

// DeleteByResource removes an event by the href name a CalDAV client used.
func (c *Calendar) DeleteByResource(ctx context.Context, userID uuid.UUID, resource string) error {
	_, err := c.db.ExecContext(ctx, `
        DELETE FROM events
        WHERE user_id = $1 AND resource = $2
    `, userID, resource)
	return err
}

// GetByResource looks up an event by the href name a CalDAV client used.
func (c *Calendar) GetByResource(ctx context.Context, userID uuid.UUID, resource string) (*Event, error) {
	row := c.db.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE user_id=$1 AND resource=$2`, userID, resource)
	e, err := scanEvent(row)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (c *Calendar) List(ctx context.Context, userID uuid.UUID) ([]Event, error) {
	rows, err := c.db.QueryContext(ctx, `
        SELECT `+eventColumns+`
        FROM events WHERE user_id=$1 ORDER BY starts_at NULLS LAST
    `, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		res = append(res, e)
	}
	return res, rows.Err()
}

// Upsert stores an event created from the web UI (no raw ICS: the CalDAV GET
// serialises one on the fly). The resource name equals the row id so the
// CalDAV href is stable.
func (c *Calendar) Upsert(ctx context.Context, userID uuid.UUID, title string, start, end time.Time) (*Event, error) {
	id := uuid.New()
	etag := uuid.New().String()
	row := c.db.QueryRowContext(ctx, `
        INSERT INTO events (id, user_id, title, starts_at, ends_at, etag, resource)
        VALUES ($1,$2,$3,$4,$5,$6,$1::text)
        RETURNING `+eventColumns, id, userID, title, nullableTime(start), nullableTime(end), etag)
	e, err := scanEvent(row)
	if err != nil {
		return nil, fmt.Errorf("upsert event: %w", err)
	}
	return &e, nil
}

// PutICS stores a CalDAV event addressed by the resource name from its PUT URL,
// keeping the raw iCalendar body so the client can GET it back byte-for-byte at
// the same href. start/end/title are best-effort, only for the web calendar.
func (c *Calendar) PutICS(ctx context.Context, userID uuid.UUID, resource, ics, uidVal, title string, start, end time.Time) (*Event, error) {
	etag := uuid.New().String()
	row := c.db.QueryRowContext(ctx, `
        INSERT INTO events (user_id, title, starts_at, ends_at, uid, ics, resource, etag)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
        ON CONFLICT (user_id, resource) DO UPDATE SET
            title = EXCLUDED.title,
            starts_at = EXCLUDED.starts_at,
            ends_at = EXCLUDED.ends_at,
            uid = EXCLUDED.uid,
            ics = EXCLUDED.ics,
            etag = EXCLUDED.etag,
            updated_at = NOW()
        RETURNING `+eventColumns,
		userID, title, nullableTime(start), nullableTime(end), uidVal, ics, resource, etag)
	e, err := scanEvent(row)
	if err != nil {
		return nil, fmt.Errorf("put ics event: %w", err)
	}
	return &e, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
