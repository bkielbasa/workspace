package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type appPasswordRepository struct{ db *sql.DB }

func NewAppPasswordRepository(db *sql.DB) identity.AppPasswordRepository {
	return &appPasswordRepository{db: db}
}

func scanAppPassword(row scanRow) (identity.AppPassword, error) {
	var rec identity.AppPassword
	var lastUsed sql.NullTime
	err := row.Scan(&rec.ID, &rec.UserID, &rec.Name, &rec.CreatedAt, &lastUsed)
	if err != nil {
		return rec, err
	}
	if lastUsed.Valid {
		t := lastUsed.Time
		rec.LastUsedAt = &t
	}
	return rec, nil
}

func (r *appPasswordRepository) Create(ctx context.Context, userID uuid.UUID, name, hash string) (*identity.AppPassword, error) {
	rec, err := scanAppPassword(r.db.QueryRowContext(ctx, `
		INSERT INTO app_passwords (user_id, name, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, name, created_at, last_used_at
	`, userID, name, hash))
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *appPasswordRepository) List(ctx context.Context, userID uuid.UUID) ([]identity.AppPassword, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, name, created_at, last_used_at
		FROM app_passwords WHERE user_id = $1 ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.AppPassword
	for rows.Next() {
		rec, err := scanAppPassword(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *appPasswordRepository) Hashes(ctx context.Context, userID uuid.UUID) ([]identity.AppPasswordHash, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, password_hash FROM app_passwords WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.AppPasswordHash
	for rows.Next() {
		var h identity.AppPasswordHash
		if err := rows.Scan(&h.ID, &h.Hash); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *appPasswordRepository) Touch(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE app_passwords SET last_used_at = $2 WHERE id = $1
	`, id, time.Now())
	return err
}

func (r *appPasswordRepository) Delete(ctx context.Context, userID, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM app_passwords WHERE id = $1 AND user_id = $2
	`, id, userID)
	return err
}

func (r *appPasswordRepository) DeleteByName(ctx context.Context, userID uuid.UUID, name string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM app_passwords WHERE user_id = $1 AND name = $2
	`, userID, name)
	return err
}
