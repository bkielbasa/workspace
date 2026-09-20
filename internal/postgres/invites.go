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

const inviteColumns = `t.id, t.user_id, COALESCE(t.invited_email, u.email, ''), COALESCE(t.display_name, u.display_name, ''), t.expires_at, t.used_at, t.created_at`

func scanInvite(row scanRow) (identity.Invite, error) {
	var inv identity.Invite
	var userID *uuid.UUID
	var used sql.NullTime
	err := row.Scan(&inv.ID, &userID, &inv.InvitedEmail, &inv.DisplayName, &inv.ExpiresAt, &used, &inv.CreatedAt)
	if err != nil {
		return inv, err
	}
	if userID != nil {
		inv.UserID = userID
	}
	if used.Valid {
		t := used.Time
		inv.UsedAt = &t
	}
	inv.Email = inv.InvitedEmail
	return inv, nil
}

func (r *inviteRepository) Create(ctx context.Context, invitedEmail, displayName, tokenHash string, expiresAt time.Time) (*identity.Invite, error) {
	var id uuid.UUID
	if err := r.db.QueryRowContext(ctx, `
		INSERT INTO invite_tokens (invited_email, display_name, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, invitedEmail, displayName, tokenHash, expiresAt).Scan(&id); err != nil {
		return nil, err
	}
	inv, err := scanInvite(r.db.QueryRowContext(ctx, `
		SELECT `+inviteColumns+`
		FROM invite_tokens t LEFT JOIN users u ON u.id = t.user_id
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
		FROM invite_tokens t LEFT JOIN users u ON u.id = t.user_id
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

func (r *inviteRepository) MarkUsed(ctx context.Context, id uuid.UUID, userID uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE invite_tokens SET used_at = NOW(), user_id = $2 WHERE id = $1
	`, id, userID)
	return err
}

func (r *inviteRepository) List(ctx context.Context) ([]identity.Invite, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+inviteColumns+`
		FROM invite_tokens t LEFT JOIN users u ON u.id = t.user_id
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

func (r *inviteRepository) DeleteForEmail(ctx context.Context, email string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM invite_tokens
		WHERE lower(invited_email) = lower($1)
		   OR user_id IN (SELECT id FROM users WHERE lower(email) = lower($1))
	`, email)
	return err
}
