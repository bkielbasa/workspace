package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

var defaultMailboxNames = []string{
	"INBOX", "Sent", "Drafts", "Trash", "Archive", "Spam", "All", "Important",
}

type userRepository struct{ db *sql.DB }

func NewUserRepository(db *sql.DB) identity.UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) Create(ctx context.Context, email, passwordHash, displayName string) (*identity.User, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var existing uuid.UUID
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&existing)
	switch {
	case err == nil:
		return nil, identity.ErrUserAlreadyExists
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("check existing user: %w", err)
	}

	user := &identity.User{}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash, display_name)
		VALUES ($1, $2, $3)
		RETURNING id, email, password_hash, display_name, enabled, created_at, updated_at
	`, email, passwordHash, displayName).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.DisplayName,
		&user.Enabled, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	if err := createDefaultMailboxes(ctx, tx, user.ID); err != nil {
		return nil, fmt.Errorf("create mailboxes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return user, nil
}

func (r *userRepository) Get(ctx context.Context, id uuid.UUID) (*identity.User, error) {
	return r.get(ctx, `WHERE id = $1`, id)
}

func (r *userRepository) GetByEmail(ctx context.Context, email string) (*identity.User, error) {
	return r.get(ctx, `WHERE email = $1`, email)
}

func (r *userRepository) get(ctx context.Context, where string, arg any) (*identity.User, error) {
	user := &identity.User{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, email, password_hash, display_name, enabled, created_at, updated_at
		FROM users `+where, arg).Scan(
		&user.ID, &user.Email, &user.PasswordHash, &user.DisplayName,
		&user.Enabled, &user.CreatedAt, &user.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return user, nil
}

func (r *userRepository) List(ctx context.Context) ([]identity.User, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, email, password_hash, display_name, enabled, created_at, updated_at
		FROM users ORDER BY created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := make([]identity.User, 0)
	for rows.Next() {
		var user identity.User
		if err := rows.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.DisplayName,
			&user.Enabled, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (r *userRepository) Update(ctx context.Context, id uuid.UUID, displayName string, enabled bool) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE users SET display_name = $2, enabled = $3, updated_at = NOW()
		WHERE id = $1
	`, id, displayName, enabled)
	return affectedOrNotFound(result, err, "update user", identity.ErrUserNotFound)
}

func (r *userRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id)
	return affectedOrNotFound(result, err, "delete user", identity.ErrUserNotFound)
}

func (r *userRepository) ChangePassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE users SET password_hash = $2, updated_at = NOW() WHERE id = $1
	`, id, passwordHash)
	return affectedOrNotFound(result, err, "change password", identity.ErrUserNotFound)
}

type sessionRepository struct{ db *sql.DB }

func NewSessionRepository(db *sql.DB) identity.SessionRepository {
	return &sessionRepository{db: db}
}

func (r *sessionRepository) Create(ctx context.Context, userID uuid.UUID, ttl time.Duration) (*identity.Session, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().UTC()
	session := &identity.Session{Token: token, UserID: userID, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $3)
	`, hashToken(token), userID, session.CreatedAt, session.ExpiresAt); err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}
	return session, nil
}

func (r *sessionRepository) GetByToken(ctx context.Context, token string) (*identity.Session, error) {
	session := &identity.Session{Token: token}
	hash := hashToken(token)
	err := r.db.QueryRowContext(ctx, `
		SELECT user_id, created_at, expires_at FROM sessions WHERE token_hash = $1
	`, hash).Scan(&session.UserID, &session.CreatedAt, &session.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		return nil, identity.ErrSessionExpired
	}
	_, _ = r.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = $1 WHERE token_hash = $2`, time.Now().UTC(), hash)
	return session, nil
}

func (r *sessionRepository) Delete(ctx context.Context, token string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (r *sessionRepository) DeleteAllForUser(ctx context.Context, userID uuid.UUID) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

func (r *sessionRepository) CleanupExpired(ctx context.Context) (int64, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= NOW()`)
	if err != nil {
		return 0, fmt.Errorf("cleanup sessions: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawStdEncoding.EncodeToString(sum[:])
}

type domainRepository struct{ db *sql.DB }

func NewDomainRepository(db *sql.DB) identity.DomainRepository {
	return &domainRepository{db: db}
}

func (r *domainRepository) Create(ctx context.Context, name string) (*identity.Domain, error) {
	var existing uuid.UUID
	err := r.db.QueryRowContext(ctx, `SELECT id FROM domains WHERE name = $1`, name).Scan(&existing)
	switch {
	case err == nil:
		return nil, identity.ErrDomainAlreadyExists
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("check existing domain: %w", err)
	}
	domain := &identity.Domain{}
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO domains (name) VALUES ($1)
		RETURNING id, name, created_at, updated_at
	`, name).Scan(&domain.ID, &domain.Name, &domain.CreatedAt, &domain.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create domain: %w", err)
	}
	return domain, nil
}

func (r *domainRepository) Get(ctx context.Context, id uuid.UUID) (*identity.Domain, error) {
	return r.get(ctx, `id = $1`, id)
}

func (r *domainRepository) GetByName(ctx context.Context, name string) (*identity.Domain, error) {
	return r.get(ctx, `name = $1`, name)
}

func (r *domainRepository) get(ctx context.Context, predicate string, arg any) (*identity.Domain, error) {
	domain := &identity.Domain{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, created_at, updated_at FROM domains WHERE `+predicate, arg,
	).Scan(&domain.ID, &domain.Name, &domain.CreatedAt, &domain.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrDomainNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get domain: %w", err)
	}
	return domain, nil
}

func (r *domainRepository) List(ctx context.Context) ([]identity.Domain, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, created_at, updated_at FROM domains ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()
	domains := make([]identity.Domain, 0)
	for rows.Next() {
		var domain identity.Domain
		if err := rows.Scan(&domain.ID, &domain.Name, &domain.CreatedAt, &domain.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan domain: %w", err)
		}
		domains = append(domains, domain)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate domains: %w", err)
	}
	return domains, nil
}

func (r *domainRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM domains WHERE id = $1`, id)
	return affectedOrNotFound(result, err, "delete domain", identity.ErrDomainNotFound)
}

func (r *domainRepository) Exists(ctx context.Context, name string) (bool, error) {
	var id uuid.UUID
	err := r.db.QueryRowContext(ctx, `SELECT id FROM domains WHERE name = $1`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check domain: %w", err)
	}
	return true, nil
}

type aliasRepository struct{ db *sql.DB }

func NewAliasRepository(db *sql.DB) identity.AliasRepository {
	return &aliasRepository{db: db}
}

func (r *aliasRepository) Create(ctx context.Context, domainID uuid.UUID, address, destination string) (*identity.Alias, error) {
	var existing uuid.UUID
	err := r.db.QueryRowContext(ctx, `SELECT id FROM aliases WHERE address = $1`, address).Scan(&existing)
	switch {
	case err == nil:
		return nil, identity.ErrAliasAlreadyExists
	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("check existing alias: %w", err)
	}
	alias := &identity.Alias{}
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO aliases (domain_id, address, destination) VALUES ($1, $2, $3)
		RETURNING id, domain_id, address, destination, created_at, updated_at
	`, domainID, address, destination).Scan(
		&alias.ID, &alias.DomainID, &alias.Address, &alias.Destination, &alias.CreatedAt, &alias.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create alias: %w", err)
	}
	return alias, nil
}

func (r *aliasRepository) Get(ctx context.Context, id uuid.UUID) (*identity.Alias, error) {
	alias := &identity.Alias{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, domain_id, address, destination, created_at, updated_at FROM aliases WHERE id = $1
	`, id).Scan(&alias.ID, &alias.DomainID, &alias.Address, &alias.Destination, &alias.CreatedAt, &alias.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrAliasNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get alias: %w", err)
	}
	return alias, nil
}

func (r *aliasRepository) ListByDomain(ctx context.Context, domainID uuid.UUID) ([]identity.Alias, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, domain_id, address, destination, created_at, updated_at
		FROM aliases WHERE domain_id = $1 ORDER BY address
	`, domainID)
	if err != nil {
		return nil, fmt.Errorf("list aliases: %w", err)
	}
	defer rows.Close()
	aliases := make([]identity.Alias, 0)
	for rows.Next() {
		var alias identity.Alias
		if err := rows.Scan(&alias.ID, &alias.DomainID, &alias.Address, &alias.Destination,
			&alias.CreatedAt, &alias.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan alias: %w", err)
		}
		aliases = append(aliases, alias)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate aliases: %w", err)
	}
	return aliases, nil
}

func (r *aliasRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM aliases WHERE id = $1`, id)
	return affectedOrNotFound(result, err, "delete alias", identity.ErrAliasNotFound)
}

func (r *aliasRepository) Resolve(ctx context.Context, address string) (string, error) {
	var destination string
	err := r.db.QueryRowContext(ctx, `SELECT destination FROM aliases WHERE address = $1`, address).Scan(&destination)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve alias: %w", err)
	}
	return destination, nil
}

func createDefaultMailboxes(ctx context.Context, tx *sql.Tx, userID uuid.UUID) error {
	for _, name := range defaultMailboxNames {
		if _, err := tx.ExecContext(ctx, `INSERT INTO mailboxes (user_id, name) VALUES ($1, $2)`, userID, name); err != nil {
			return fmt.Errorf("create mailbox %s: %w", name, err)
		}
	}
	return nil
}

func affectedOrNotFound(result sql.Result, err error, operation string, notFound error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return notFound
	}
	return nil
}
