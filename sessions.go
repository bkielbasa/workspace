package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session expired")
)

const sessionDefaultTTL = 24 * time.Hour

type Session struct {
	Token     string
	UserID    uuid.UUID
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Sessions struct {
	db *sql.DB
}

// Create issues a new session for the given user and returns the plaintext
// token. Only the plaintext token is ever handed to the client; the database
// stores a SHA-256 hash of it.
func (s *Sessions) Create(ctx context.Context, userID uuid.UUID, ttl time.Duration) (*Session, error) {
	if ttl <= 0 {
		ttl = sessionDefaultTTL
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := hashToken(token)
	now := time.Now().UTC()
	expires := now.Add(ttl)

	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at)
		 VALUES ($1, $2, $3, $4, $3)`,
		hash,
		userID,
		now,
		expires,
	); err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	return &Session{
		Token:     token,
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: expires,
	}, nil
}

func (s *Sessions) GetByToken(ctx context.Context, token string) (*Session, error) {
	hash := hashToken(token)

	session := &Session{
		Token: token,
	}

	err := s.db.QueryRowContext(
		ctx,
		`SELECT user_id, created_at, expires_at
		 FROM sessions
		 WHERE token_hash = $1`,
		hash,
	).Scan(
		&session.UserID,
		&session.CreatedAt,
		&session.ExpiresAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	if time.Now().UTC().After(session.ExpiresAt) {
		return nil, ErrSessionExpired
	}

	// best-effort touch; failure is non-fatal
	_, _ = s.db.ExecContext(
		ctx,
		`UPDATE sessions SET last_seen_at = $1 WHERE token_hash = $2`,
		time.Now().UTC(),
		hash,
	)

	return session, nil
}

func (s *Sessions) Delete(ctx context.Context, token string) error {
	hash := hashToken(token)

	if _, err := s.db.ExecContext(
		ctx,
		`DELETE FROM sessions WHERE token_hash = $1`,
		hash,
	); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}

	return nil
}

// DeleteAllForUser revokes every session belonging to a user. Best practice is
// to invalidate all sessions when a password is changed or an account is
// disabled, so a stolen token cannot outlive the credential change.
func (s *Sessions) DeleteAllForUser(ctx context.Context, userID uuid.UUID) error {
	if _, err := s.db.ExecContext(
		ctx,
		`DELETE FROM sessions WHERE user_id = $1`,
		userID,
	); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}

	return nil
}

// CleanupExpired removes sessions whose expiry has passed. It is intended to be
// called on an interval from a background goroutine so stale rows do not
// accumulate in the table.
func (s *Sessions) CleanupExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(
		ctx,
		`DELETE FROM sessions WHERE expires_at <= NOW()`,
	)
	if err != nil {
		return 0, fmt.Errorf("cleanup sessions: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}

	return n, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawStdEncoding.EncodeToString(sum[:])
}
