package main

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "strings"
    "time"

    "github.com/google/uuid"
    "golang.org/x/crypto/bcrypt"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrUserAlreadyExists = errors.New("user already exists")
	ErrDomainNotAllowed  = errors.New("domain not allowed")
)

type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	DisplayName  string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Users struct {
	db        *sql.DB
	mailboxes *Mailboxes
	domains   *Domains
}

func (u *Users) Create(ctx context.Context, email, password, displayName string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	// validate the email's domain is registered
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, fmt.Errorf("invalid email address")
	}
	if u.domains != nil {
		ok, err := u.domains.Exists(ctx, parts[1])
		if err != nil {
			return nil, fmt.Errorf("domain check: %w", err)
		}
		if !ok {
			return nil, ErrDomainNotAllowed
		}
	}

    passwordHashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    if err != nil {
        return nil, fmt.Errorf("hash password: %w", err)
    }
    passwordHash := string(passwordHashBytes)

	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var existing uuid.UUID

	err = tx.QueryRowContext(
		ctx,
		`SELECT id FROM users WHERE email = $1`,
		email,
	).Scan(&existing)

	switch {
	case err == nil:
		return nil, ErrUserAlreadyExists

	case !errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("check existing user: %w", err)
	}

	user := &User{}

	err = tx.QueryRowContext(
		ctx,
		`
		INSERT INTO users (
			email,
			password_hash,
			display_name
		)
		VALUES ($1, $2, $3)
		RETURNING
			id,
			email,
			password_hash,
			display_name,
			enabled,
			created_at,
			updated_at
		`,
		email,
		passwordHash,
		displayName,
	).Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.DisplayName,
		&user.Enabled,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	if err := u.mailboxes.CreateDefaultTx(ctx, tx, user.ID); err != nil {
		return nil, fmt.Errorf("create mailboxes: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return user, nil
}

func (u *Users) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	user := &User{}

	err := u.db.QueryRowContext(
		ctx,
		`
		SELECT
			id,
			email,
			password_hash,
			display_name,
			enabled,
			created_at,
			updated_at
		FROM users
		WHERE id = $1
		`,
		id,
	).Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.DisplayName,
		&user.Enabled,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	return user, nil
}

func (u *Users) GetByEmail(ctx context.Context, email string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	user := &User{}

	err := u.db.QueryRowContext(
		ctx,
		`
		SELECT
			id,
			email,
			password_hash,
			display_name,
			enabled,
			created_at,
			updated_at
		FROM users
		WHERE email = $1
		`,
		email,
	).Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.DisplayName,
		&user.Enabled,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}

	return user, nil
}

func (u *Users) List(ctx context.Context) ([]User, error) {
	rows, err := u.db.QueryContext(
		ctx,
		`
		SELECT
			id,
			email,
			password_hash,
			display_name,
			enabled,
			created_at,
			updated_at
		FROM users
		ORDER BY created_at
		`,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := make([]User, 0)

	for rows.Next() {
		var user User

		err := rows.Scan(
			&user.ID,
			&user.Email,
			&user.PasswordHash,
			&user.DisplayName,
			&user.Enabled,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}

		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}

	return users, nil
}

func (u *Users) Update(ctx context.Context, id uuid.UUID, displayName string, enabled bool) error {
	result, err := u.db.ExecContext(
		ctx,
		`
		UPDATE users
		SET
			display_name = $2,
			enabled = $3,
			updated_at = NOW()
		WHERE id = $1
		`,
		id,
		displayName,
		enabled,
	)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		return ErrUserNotFound
	}

	return nil
}

func (u *Users) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := u.db.ExecContext(
		ctx,
		`
		DELETE FROM users
		WHERE id = $1
		`,
		id,
	)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		return ErrUserNotFound
	}

	return nil
}

func (u *Users) Authenticate(ctx context.Context, email, password string) (*User, error) {
    user, err := u.GetByEmail(ctx, email)
    if err != nil {
        return nil, err
    }

    if !user.Enabled {
        return nil, errors.New("user disabled")
    }

    if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
        return nil, errors.New("invalid password")
    }

    return user, nil
}

func (u *Users) ChangePassword(ctx context.Context, id uuid.UUID, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	result, err := u.db.ExecContext(
		ctx,
		`
		UPDATE users
		SET
			password_hash = $2,
			updated_at = NOW()
		WHERE id = $1
		`,
		id,
		hash,
	)
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		return ErrUserNotFound
	}

	return nil
}
