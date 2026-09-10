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
	ID          uuid.UUID
	UserID      uuid.UUID
	Title       string
	Location    string
	Description string
	StartsAt    time.Time
	EndsAt      time.Time
	UID         string
	ICS         string
	Resource    string
	ETag        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Href is the CalDAV (or similar) resource name without extension.
func (e Event) Href() string {
	if e.Resource != "" {
		return e.Resource
	}
	return e.ID.String()
}

// Details are the structured fields a person fills in from the web UI.
type Details struct {
	Title       string
	Location    string
	Description string
	StartsAt    time.Time
	EndsAt      time.Time
}

// NewWebEvent builds an event created from structured fields (web UI).
func NewWebEvent(userID uuid.UUID, details Details) (Event, error) {
	details.Title = strings.TrimSpace(details.Title)
	details.Location = strings.TrimSpace(details.Location)
	details.Description = strings.TrimSpace(details.Description)
	if userID == uuid.Nil {
		return Event{}, ErrInvalidEvent
	}
	if details.Title == "" {
		return Event{}, ErrInvalidEvent
	}
	if !details.EndsAt.After(details.StartsAt) {
		return Event{}, ErrInvalidEvent
	}
	id := uuid.New()
	return Event{
		ID:          id,
		UserID:      userID,
		Title:       details.Title,
		Location:    details.Location,
		Description: details.Description,
		StartsAt:    details.StartsAt,
		EndsAt:      details.EndsAt,
		Resource:    id.String(),
		ETag:        uuid.NewString(),
	}, nil
}

// Payload is what a format adapter extracted from a wire body.
type Payload struct {
	Resource    string
	ICS         string
	UID         string
	Title       string
	Location    string
	Description string
	StartsAt    time.Time
	EndsAt      time.Time
}

// FromPayload builds an event from a format adapter (ICS, etc.).
func FromPayload(userID uuid.UUID, payload Payload) (Event, error) {
	payload.Resource = strings.TrimSpace(payload.Resource)
	if userID == uuid.Nil || payload.Resource == "" {
		return Event{}, ErrInvalidEvent
	}
	return Event{
		UserID:      userID,
		Title:       strings.TrimSpace(payload.Title),
		Location:    strings.TrimSpace(payload.Location),
		Description: strings.TrimSpace(payload.Description),
		StartsAt:    payload.StartsAt,
		EndsAt:      payload.EndsAt,
		UID:         payload.UID,
		ICS:         payload.ICS,
		Resource:    payload.Resource,
		ETag:        uuid.NewString(),
	}, nil
}
