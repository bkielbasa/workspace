package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type inviteRepository struct{ db *sql.DB }

func NewInviteRepository(db *sql.DB) identity.InviteRepository {
	return &inviteRepository{db: db}
}

const inviteColumns = `t.id, t.user_id, u.email, t.expires_at, t.used_at, t.created_at`

func scanInvite(row scanRow) (identity.Invite, error) {
	var inv identity.Invite
	var used sql.NullTime
	err := row.Scan(&inv.ID, &inv.UserID, &inv.Email, &inv.ExpiresAt, &used, &inv.CreatedAt)
	if err != nil {
		return inv, err
	}
	if used.Valid {
		t := used.Time
		inv.UsedAt = &t
	}
	return inv, nil
}

func (r *inviteRepository) Create(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) (*identity.Invite, error) {
	var id uuid.UUID
	if err := r.db.QueryRowContext(ctx, `
		INSERT INTO invite_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id
	`, userID, tokenHash, expiresAt).Scan(&id); err != nil {
		return nil, err
	}
	inv, err := scanInvite(r.db.QueryRowContext(ctx, `
		SELECT `+inviteColumns+`
		FROM invite_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.id = $1
	`, id))
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (r *inviteRepository) GetByHash(ctx context.Context, tokenHash string) (*identity.Invite, error) {
	inv, err := scanInvite(r.db.QueryRowContext(ctx, `
		SELECT `+inviteColumns+`
		FROM invite_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = $1
	`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrInviteNotFound
	}
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

func (r *inviteRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE invite_tokens SET used_at = NOW() WHERE id = $1
	`, id)
	return err
}

func (r *inviteRepository) List(ctx context.Context) ([]identity.Invite, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+inviteColumns+`
		FROM invite_tokens t JOIN users u ON u.id = t.user_id
		ORDER BY t.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (r *inviteRepository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM invite_tokens WHERE id = $1`, id)
	return err
}

func (r *inviteRepository) DeleteForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM invite_tokens WHERE user_id = $1`, userID)
	return err
}
