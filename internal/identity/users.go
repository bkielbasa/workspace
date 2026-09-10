package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/bcrypt"
)

type UserRepository interface {
	Create(ctx context.Context, email, passwordHash, displayName string) (*User, error)
	Get(ctx context.Context, id uuid.UUID) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	List(ctx context.Context) ([]User, error)
	Update(ctx context.Context, id uuid.UUID, displayName string, enabled bool) error
	Delete(ctx context.Context, id uuid.UUID) error
	ChangePassword(ctx context.Context, id uuid.UUID, passwordHash string) error
}

type Users struct {
	repo     UserRepository
	sessions SessionRepository
	tracer   trace.Tracer
}

func NewUsers(repo UserRepository, sessions SessionRepository) *Users {
	return &Users{repo: repo, sessions: sessions, tracer: otel.Tracer("users")}
}

func (u *Users) Create(ctx context.Context, email, password, displayName string) (*User, error) {
	ctx, span := u.tracer.Start(ctx, "users.create")
	defer span.End()

	email = strings.ToLower(strings.TrimSpace(email))
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, fmt.Errorf("invalid email address")
	}

	passwordHashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	return u.repo.Create(ctx, email, string(passwordHashBytes), displayName)
}

func (u *Users) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	ctx, span := u.tracer.Start(ctx, "users.get")
	defer span.End()
	return u.repo.Get(ctx, id)
}

func (u *Users) GetByEmail(ctx context.Context, email string) (*User, error) {
	ctx, span := u.tracer.Start(ctx, "users.get_by_email")
	defer span.End()
	return u.repo.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
}

func (u *Users) List(ctx context.Context) ([]User, error) {
	ctx, span := u.tracer.Start(ctx, "users.list")
	defer span.End()
	return u.repo.List(ctx)
}

func (u *Users) Update(ctx context.Context, id uuid.UUID, displayName string, enabled bool) error {
	ctx, span := u.tracer.Start(ctx, "users.update")
	defer span.End()
	if err := u.repo.Update(ctx, id, displayName, enabled); err != nil {
		return err
	}
	if !enabled && u.sessions != nil {
		return u.sessions.DeleteAllForUser(ctx, id)
	}
	return nil
}

func (u *Users) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := u.tracer.Start(ctx, "users.delete")
	defer span.End()
	return u.repo.Delete(ctx, id)
}

func (u *Users) Authenticate(ctx context.Context, email, password string) (*User, error) {
	ctx, span := u.tracer.Start(ctx, "users.authenticate")
	defer span.End()

	user, err := u.GetByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if !user.Enabled {
		return nil, errors.New("user disabled")
	}
	if strings.HasPrefix(user.PasswordHash, "$argon2id$") {
		if !CheckPassword(user.PasswordHash, password) {
			return nil, errors.New("invalid password")
		}
	} else if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid password")
	}
	return user, nil
}

func (u *Users) ChangePassword(ctx context.Context, id uuid.UUID, password string) error {
	ctx, span := u.tracer.Start(ctx, "users.change_password")
	defer span.End()

	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := u.repo.ChangePassword(ctx, id, hash); err != nil {
		return err
	}
	if u.sessions != nil {
		return u.sessions.DeleteAllForUser(ctx, id)
	}
	return nil
}
