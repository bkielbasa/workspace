package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type ssoRepository struct{ db *sql.DB }

func NewSSORepository(db *sql.DB) identity.SSORepository {
	return &ssoRepository{db: db}
}

func (r *ssoRepository) GetByProviderSubject(ctx context.Context, provider, subject string) (*identity.SSOLink, error) {
	link := &identity.SSOLink{}
	err := r.db.QueryRowContext(ctx, `
		SELECT user_id, provider, subject, email, created_at, updated_at
		FROM sso_identities
		WHERE provider = $1 AND subject = $2
	`, provider, subject).Scan(
		&link.UserID, &link.Provider, &link.Subject, &link.Email,
		&link.CreatedAt, &link.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get sso identity: %w", err)
	}
	return link, nil
}

func (r *ssoRepository) Link(ctx context.Context, userID uuid.UUID, provider, subject, email string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sso_identities (user_id, provider, subject, email)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, subject) DO UPDATE
		SET user_id = EXCLUDED.user_id, email = EXCLUDED.email, updated_at = NOW()
	`, userID, provider, subject, email)
	if err != nil {
		return fmt.Errorf("link sso identity: %w", err)
	}
	return nil
}
