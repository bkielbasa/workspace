package postgres

import (
	"context"
	"database/sql"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type photoAlbumRepository struct{ db *sql.DB }

func NewPhotoAlbumRepository(db *sql.DB) identity.PhotoAlbumRepository {
	return &photoAlbumRepository{db: db}
}

func (r *photoAlbumRepository) Create(ctx context.Context, userID uuid.UUID, name string) (*identity.PhotoAlbum, error) {
	var album identity.PhotoAlbum
	// DO UPDATE on identical values is a no-op that still RETURNINGs the
	// existing row, so re-creating an album name just returns it.
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO photo_albums (user_id, name) VALUES ($1, $2)
		ON CONFLICT (user_id, name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, name, created_at
	`, userID, name).Scan(&album.ID, &album.Name, &album.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &album, nil
}

func (r *photoAlbumRepository) List(ctx context.Context, userID uuid.UUID) ([]identity.PhotoAlbum, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.name, a.created_at, COUNT(i.path)
		FROM photo_albums a LEFT JOIN photo_album_items i ON i.album_id = a.id
		WHERE a.user_id = $1
		GROUP BY a.id ORDER BY a.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.PhotoAlbum
	for rows.Next() {
		var album identity.PhotoAlbum
		if err := rows.Scan(&album.ID, &album.Name, &album.CreatedAt, &album.Count); err != nil {
			return nil, err
		}
		out = append(out, album)
	}
	return out, rows.Err()
}

func (r *photoAlbumRepository) Delete(ctx context.Context, userID, albumID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM photo_albums WHERE id = $1 AND user_id = $2
	`, albumID, userID)
	return err
}

func (r *photoAlbumRepository) Add(ctx context.Context, userID, albumID uuid.UUID, path string) error {
	var ok bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT true FROM photo_albums WHERE id = $1 AND user_id = $2
	`, albumID, userID).Scan(&ok); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO photo_album_items (album_id, user_id, path) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, albumID, userID, path)
	return err
}

func (r *photoAlbumRepository) Remove(ctx context.Context, userID, albumID uuid.UUID, path string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM photo_album_items WHERE album_id = $1 AND user_id = $2 AND path = $3
	`, albumID, userID, path)
	return err
}

func (r *photoAlbumRepository) Paths(ctx context.Context, userID, albumID uuid.UUID) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT i.path FROM photo_album_items i
		JOIN photo_albums a ON a.id = i.album_id AND a.user_id = $1
		WHERE i.album_id = $2 AND i.user_id = $1
	`, userID, albumID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return out, rows.Err()
}

func (r *photoAlbumRepository) Memberships(ctx context.Context, userID uuid.UUID) (map[string][]uuid.UUID, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT path, album_id FROM photo_album_items WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]uuid.UUID{}
	for rows.Next() {
		var path string
		var albumID uuid.UUID
		if err := rows.Scan(&path, &albumID); err != nil {
			return nil, err
		}
		out[path] = append(out[path], albumID)
	}
	return out, rows.Err()
}
