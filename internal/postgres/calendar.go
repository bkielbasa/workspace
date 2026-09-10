package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/google/uuid"
)

type calendarRepository struct{ db *sql.DB }

func NewCalendarRepository(db *sql.DB) calendar.Repository {
	return &calendarRepository{db: db}
}

func (r *calendarRepository) GetByResource(ctx context.Context, userID uuid.UUID, resource string) (*calendar.Event, error) {
	event, err := scanEvent(r.db.QueryRowContext(ctx, `
		SELECT `+eventColumns+` FROM events WHERE user_id = $1 AND resource = $2
	`, userID, resource))
	if err != nil {
		return nil, err
	}
	return &event, nil
}

func (r *calendarRepository) List(ctx context.Context, userID uuid.UUID) ([]calendar.Event, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+eventColumns+` FROM events
		WHERE user_id = $1 ORDER BY starts_at NULLS LAST
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []calendar.Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *calendarRepository) Put(ctx context.Context, event calendar.Event) (*calendar.Event, error) {
	var row *sql.Row
	if event.ID != uuid.Nil {
		row = r.db.QueryRowContext(ctx, `
			INSERT INTO events (
				id, user_id, title, location, description, starts_at, ends_at, uid, ics, resource, etag
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (user_id, resource) DO UPDATE SET
				title=EXCLUDED.title, location=EXCLUDED.location,
				description=EXCLUDED.description, starts_at=EXCLUDED.starts_at,
				ends_at=EXCLUDED.ends_at, uid=EXCLUDED.uid,
				ics=EXCLUDED.ics, etag=EXCLUDED.etag, updated_at=NOW()
			RETURNING `+eventColumns,
			event.ID, event.UserID, event.Title, event.Location, event.Description,
			nullableTime(event.StartsAt), nullableTime(event.EndsAt), event.UID,
			event.ICS, event.Resource, event.ETag,
		)
	} else {
		row = r.db.QueryRowContext(ctx, `
			INSERT INTO events (
				user_id, title, location, description, starts_at, ends_at, uid, ics, resource, etag
			)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (user_id, resource) DO UPDATE SET
				title=EXCLUDED.title, location=EXCLUDED.location,
				description=EXCLUDED.description, starts_at=EXCLUDED.starts_at,
				ends_at=EXCLUDED.ends_at, uid=EXCLUDED.uid,
				ics=EXCLUDED.ics, etag=EXCLUDED.etag, updated_at=NOW()
			RETURNING `+eventColumns,
			event.UserID, event.Title, event.Location, event.Description,
			nullableTime(event.StartsAt), nullableTime(event.EndsAt), event.UID,
			event.ICS, event.Resource, event.ETag,
		)
	}
	saved, err := scanEvent(row)
	if err != nil {
		return nil, fmt.Errorf("put event: %w", err)
	}
	return &saved, nil
}

func (r *calendarRepository) Delete(ctx context.Context, userID, eventID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM events WHERE id = $1 AND user_id = $2`, eventID, userID)
	return err
}

func (r *calendarRepository) DeleteByResource(ctx context.Context, userID uuid.UUID, resource string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM events WHERE user_id = $1 AND resource = $2`, userID, resource)
	return err
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}
