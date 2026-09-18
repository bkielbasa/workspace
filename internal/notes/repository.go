package notes

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	CreateNote(ctx context.Context, note Note) (*Note, error)
	GetNote(ctx context.Context, id uuid.UUID) (*Note, error)
	ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]Note, error)
	UpdateNote(ctx context.Context, note Note) (*Note, error)
	DeleteNote(ctx context.Context, id uuid.UUID) error

	AddItem(ctx context.Context, item NoteItem) (*NoteItem, error)
	GetItem(ctx context.Context, id uuid.UUID) (*NoteItem, error)
	ListItems(ctx context.Context, noteID uuid.UUID) ([]NoteItem, error)
	ToggleItem(ctx context.Context, id uuid.UUID, completed bool) (*NoteItem, error)
	UpdateItem(ctx context.Context, item NoteItem) (*NoteItem, error)
	DeleteItem(ctx context.Context, id uuid.UUID) error

	ListTags(ctx context.Context, userID uuid.UUID) ([]NoteTag, error)
	SetNoteTags(ctx context.Context, noteID uuid.UUID, userID uuid.UUID, tags []string) error
}
