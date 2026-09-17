package identity

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PhotoAlbum is a named collection; photos belong to many albums at once.
type PhotoAlbum struct {
	ID        uuid.UUID
	Name      string
	Count     int
	CreatedAt time.Time
}

// PhotoAlbumRepository persists albums and their memberships. Paths are
// library-relative (month/name.ext), like tags.
type PhotoAlbumRepository interface {
	Create(ctx context.Context, userID uuid.UUID, name string) (*PhotoAlbum, error)
	List(ctx context.Context, userID uuid.UUID) ([]PhotoAlbum, error)
	Delete(ctx context.Context, userID, albumID uuid.UUID) error
	Add(ctx context.Context, userID, albumID uuid.UUID, path string) error
	Remove(ctx context.Context, userID, albumID uuid.UUID, path string) error
	// Paths returns the library paths inside one album.
	Paths(ctx context.Context, userID, albumID uuid.UUID) ([]string, error)
	// Memberships maps each tagged path to its album IDs, for gallery state.
	Memberships(ctx context.Context, userID uuid.UUID) (map[string][]uuid.UUID, error)
}
