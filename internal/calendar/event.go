package calendar

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidEvent = errors.New("invalid event")

// Event is a calendar event independent of any wire format.
type Event struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Title     string
	StartsAt  time.Time
	EndsAt    time.Time
	UID       string
	ICS       string
	Resource  string
	ETag      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Href is the CalDAV (or similar) resource name without extension.
func (e Event) Href() string {
	if e.Resource != "" {
		return e.Resource
	}
	return e.ID.String()
}

// NewWebEvent builds an event created from structured fields (web UI).
func NewWebEvent(userID uuid.UUID, title string, start, end time.Time) (Event, error) {
	title = strings.TrimSpace(title)
	if userID == uuid.Nil {
		return Event{}, ErrInvalidEvent
	}
	if title == "" {
		return Event{}, ErrInvalidEvent
	}
	if !end.After(start) {
		return Event{}, ErrInvalidEvent
	}
	id := uuid.New()
	return Event{
		ID:       id,
		UserID:   userID,
		Title:    title,
		StartsAt: start,
		EndsAt:   end,
		Resource: id.String(),
		ETag:     uuid.NewString(),
	}, nil
}

// FromPayload builds an event from a format adapter (ICS, etc.).
func FromPayload(userID uuid.UUID, resource, payload, uid, title string, start, end time.Time) (Event, error) {
	resource = strings.TrimSpace(resource)
	if userID == uuid.Nil || resource == "" {
		return Event{}, ErrInvalidEvent
	}
	return Event{
		UserID:   userID,
		Title:    title,
		StartsAt: start,
		EndsAt:   end,
		UID:      uid,
		ICS:      payload,
		Resource: resource,
		ETag:     uuid.NewString(),
	}, nil
}
