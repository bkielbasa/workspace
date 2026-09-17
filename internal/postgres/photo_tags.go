package postgres

import (
	"context"
	"database/sql"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type photoTagRepository struct{ db *sql.DB }

func NewPhotoTagRepository(db *sql.DB) identity.PhotoTagRepository {
	return &photoTagRepository{db: db}
}

func (r *photoTagRepository) Set(ctx context.Context, userID uuid.UUID, path string, tags []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM photo_tags WHERE user_id = $1 AND path = $2
	`, userID, path); err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO photo_tags (user_id, path, tag) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING
		`, userID, path, tag); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *photoTagRepository) ByPhoto(ctx context.Context, userID uuid.UUID) (map[string][]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT path, tag FROM photo_tags WHERE user_id = $1 ORDER BY path, tag
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var path, tag string
		if err := rows.Scan(&path, &tag); err != nil {
			return nil, err
		}
		out[path] = append(out[path], tag)
	}
	return out, rows.Err()
}

func (r *photoTagRepository) All(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT tag FROM photo_tags WHERE user_id = $1 ORDER BY tag
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		out = append(out, tag)
	}
	return out, rows.Err()
}
