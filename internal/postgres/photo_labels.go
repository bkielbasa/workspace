package postgres

import (
	"context"
	"database/sql"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type photoLabelRepository struct{ db *sql.DB }

func NewPhotoLabelRepository(db *sql.DB) identity.PhotoLabelRepository {
	return &photoLabelRepository{db: db}
}

func (r *photoLabelRepository) Get(ctx context.Context, userID uuid.UUID, path string) (string, error) {
	var label string
	err := r.db.QueryRowContext(ctx, `
		SELECT label FROM photo_labels WHERE user_id = $1 AND path = $2
	`, userID, path).Scan(&label)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return label, err
}

func (r *photoLabelRepository) Set(ctx context.Context, userID uuid.UUID, path, label string) error {	if label == "" {
		_, err := r.db.ExecContext(ctx, `
			DELETE FROM photo_labels WHERE user_id = $1 AND path = $2
		`, userID, path)
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO photo_labels (user_id, path, label, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, path) DO UPDATE SET label = EXCLUDED.label, updated_at = NOW()
	`, userID, path, label)
	return err
}

func (r *photoLabelRepository) List(ctx context.Context, userID uuid.UUID) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT path, label FROM photo_labels WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var path, label string
		if err := rows.Scan(&path, &label); err != nil {
			return nil, err
		}
		out[path] = label
	}
	return out, rows.Err()
}
