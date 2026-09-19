package notes

import (
	"time"

	"github.com/google/uuid"
)

type Kind string

const (
	KindNote Kind = "note"
	KindList Kind = "list"
)

type Note struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	Kind           Kind       `json:"kind"`
	Color          string     `json:"color"`
	IsPinned       bool       `json:"is_pinned"`
	IsArchived     bool       `json:"is_archived"`
	IsFamilyShared bool       `json:"is_family_shared"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Items          []NoteItem `json:"items,omitempty"`
	Tags           []string   `json:"tags,omitempty"`
}

func (n *Note) AppleNoteFields() (uuid.UUID, string, string, time.Time) {
	return n.ID, n.Title, n.Body, n.UpdatedAt
}

type NoteItem struct {
	ID          uuid.UUID  `json:"id"`
	NoteID      uuid.UUID  `json:"note_id"`
	Content     string     `json:"content"`
	Completed   bool       `json:"completed"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	SortOrder   int        `json:"sort_order"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type NoteTag struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Event struct {
	Type           string    `json:"type"`
	NoteID         uuid.UUID `json:"note_id"`
	ItemID         uuid.UUID `json:"item_id,omitempty"`
	Completed      bool      `json:"completed,omitempty"`
	UserID         uuid.UUID `json:"user_id"`
	IsFamilyShared bool      `json:"is_family_shared"`
}
