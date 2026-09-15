package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/bcrypt"
)

// AppPassword is a revocable per-device credential. Only the hash is stored;
// the plaintext exists solely in the generation response.
type AppPassword struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Name       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// AppPasswordHash pairs an id with its stored hash for verification.
type AppPasswordHash struct {
	ID   uuid.UUID
	Hash string
}

type AppPasswordRepository interface {
	Create(ctx context.Context, userID uuid.UUID, name, hash string) (*AppPassword, error)
	List(ctx context.Context, userID uuid.UUID) ([]AppPassword, error)
	Hashes(ctx context.Context, userID uuid.UUID) ([]AppPasswordHash, error)
	Touch(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, userID, id uuid.UUID) error
	DeleteByName(ctx context.Context, userID uuid.UUID, name string) error
}

type AppPasswords struct {
	repo   AppPasswordRepository
	users  *Users
	tracer trace.Tracer
}

func NewAppPasswords(repo AppPasswordRepository, users *Users) *AppPasswords {
	return &AppPasswords{repo: repo, users: users, tracer: otel.Tracer("app_passwords")}
}

// GeneratePassword mints a random credential for embedding in profiles.
func GeneratePassword() (string, error) {
	var b [18]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("random password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func normalizeAppName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "iPhone"
	}
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}

// Generate creates a new app password, returning the plaintext once.
func (s *AppPasswords) Generate(ctx context.Context, userID uuid.UUID, name string) (string, *AppPassword, error) {
	ctx, span := s.tracer.Start(ctx, "app_passwords.generate")
	defer span.End()

	plain, err := GeneratePassword()
	if err != nil {
		return "", nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", nil, fmt.Errorf("hash app password: %w", err)
	}
	rec, err := s.repo.Create(ctx, userID, normalizeAppName(name), string(hash))
	if err != nil {
		return "", nil, err
	}
	return plain, rec, nil
}

// Rotate replaces same-named passwords: generating an "iPhone" profile twice
// leaves exactly one iPhone credential instead of accumulating them.
func (s *AppPasswords) Rotate(ctx context.Context, userID uuid.UUID, name string) (string, *AppPassword, error) {
	ctx, span := s.tracer.Start(ctx, "app_passwords.rotate")
	defer span.End()

	name = normalizeAppName(name)
	_ = s.repo.DeleteByName(ctx, userID, name)
	return s.Generate(ctx, userID, name)
}

func (s *AppPasswords) List(ctx context.Context, userID uuid.UUID) ([]AppPassword, error) {
	ctx, span := s.tracer.Start(ctx, "app_passwords.list")
	defer span.End()
	return s.repo.List(ctx, userID)
}

func (s *AppPasswords) Revoke(ctx context.Context, userID, id uuid.UUID) error {
	ctx, span := s.tracer.Start(ctx, "app_passwords.revoke")
	defer span.End()
	return s.repo.Delete(ctx, userID, id)
}

// Authenticate verifies a device credential and returns its owner.
// Disabled users are rejected like with master passwords.
func (s *AppPasswords) Authenticate(ctx context.Context, email, password string) (*User, error) {
	ctx, span := s.tracer.Start(ctx, "app_passwords.authenticate")
	defer span.End()

	if s.users == nil {
		return nil, errors.New("invalid password")
	}
	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		return nil, errors.New("invalid password")
	}
	if !user.Enabled {
		return nil, errors.New("user disabled")
	}
	hashes, err := s.repo.Hashes(ctx, user.ID)
	if err != nil {
		return nil, errors.New("invalid password")
	}
	for _, h := range hashes {
		if bcrypt.CompareHashAndPassword([]byte(h.Hash), []byte(password)) == nil {
			_ = s.repo.Touch(ctx, h.ID)
			return user, nil
		}
	}
	return nil, errors.New("invalid password")
}

// DeviceAuth accepts master passwords first, then app passwords. It is the
// authenticator for device protocols (IMAP, SMTP, CardDAV, CalDAV). Web
// login keeps using Users directly so app passwords never unlock the UI,
// where a password change would lock the owner out.
type DeviceAuth struct {
	users *Users
	apps  *AppPasswords
}

func NewDeviceAuth(users *Users, apps *AppPasswords) *DeviceAuth {
	return &DeviceAuth{users: users, apps: apps}
}

func (a *DeviceAuth) Authenticate(ctx context.Context, email, password string) (*User, error) {
	if user, err := a.users.Authenticate(ctx, email, password); err == nil {
		return user, nil
	}
	return a.apps.Authenticate(ctx, email, password)
}
