package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/bcrypt"
)

// Invite token lifetime: family members are slow, but tokens are bearer
// credentials, so they expire and burn on use.
const InviteTTL = 7 * 24 * time.Hour

// Invite is a pending family onboarding. The plaintext token is shown once
// at creation; only its hash is stored.
type Invite struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Email     string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

type InviteRepository interface {
	Create(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) (*Invite, error)
	GetByHash(ctx context.Context, tokenHash string) (*Invite, error)
	MarkUsed(ctx context.Context, id uuid.UUID) error
	List(ctx context.Context) ([]Invite, error)
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteForUser(ctx context.Context, userID uuid.UUID) error
}

type Invites struct {
	repo   InviteRepository
	users  *Users
	tracer trace.Tracer
}

func NewInvites(repo InviteRepository, users *Users) *Invites {
	return &Invites{repo: repo, users: users, tracer: otel.Tracer("invites")}
}

func mintToken() (plain, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("random token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(plain))
	return plain, hex.EncodeToString(sum[:]), nil
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(plain)))
	return hex.EncodeToString(sum[:])
}

// CreateInvite provisions a disabled account and returns a one-time token.
// Re-inviting rotates: old tokens for the user burn.
func (s *Invites) CreateInvite(ctx context.Context, email, displayName string) (plain string, invite *Invite, err error) {
	ctx, span := s.tracer.Start(ctx, "invites.create")
	defer span.End()

	email = strings.ToLower(strings.TrimSpace(email))
	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, ErrUserNotFound) {
			return "", nil, err
		}
		// Random unknown password: the account cannot be used until accepted.
		bootstrap, genErr := GeneratePassword()
		if genErr != nil {
			return "", nil, genErr
		}
		hash, genErr := bcrypt.GenerateFromPassword([]byte(bootstrap), bcrypt.DefaultCost)
		if genErr != nil {
			return "", nil, fmt.Errorf("hash password: %w", genErr)
		}
		created, err := s.users.repo.Create(ctx, email, string(hash), displayName)
		if err != nil {
			return "", nil, err
		}
		user = created
	} else if user.Enabled {
		return "", nil, fmt.Errorf("account %s already active", email)
	}
	if err := s.users.repo.Update(ctx, user.ID, displayNameOr(user.DisplayName, displayName), false); err != nil {
		return "", nil, err
	}
	_ = s.repo.DeleteForUser(ctx, user.ID)

	plain, hash, err := mintToken()
	if err != nil {
		return "", nil, err
	}
	invite, err = s.repo.Create(ctx, user.ID, hash, time.Now().Add(InviteTTL))
	if err != nil {
		return "", nil, err
	}
	invite.Email = user.Email
	return plain, invite, nil
}

func displayNameOr(current, fallback string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return fallback
}

// Lookup resolves a presented token without consuming it (for rendering the
// accept page). Validity is NOT checked here; Accept checks.
func (s *Invites) Lookup(ctx context.Context, plain string) (*Invite, error) {
	ctx, span := s.tracer.Start(ctx, "invites.lookup")
	defer span.End()

	if strings.TrimSpace(plain) == "" {
		return nil, ErrInviteNotFound
	}
	return s.repo.GetByHash(ctx, hashToken(plain))
}

// Accept redeems a token: sets name + password, enables the account, burns
// the token. Returns the activated user for auto-login.
func (s *Invites) Accept(ctx context.Context, plain, displayName, password string) (*User, error) {
	ctx, span := s.tracer.Start(ctx, "invites.accept")
	defer span.End()

	invite, err := s.repo.GetByHash(ctx, hashToken(plain))
	if err != nil {
		return nil, ErrInviteNotFound
	}
	if invite.UsedAt != nil {
		return nil, ErrInviteUsed
	}
	if time.Now().After(invite.ExpiresAt) {
		return nil, ErrInviteExpired
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}
	user, err := s.users.GetByEmail(ctx, invite.Email)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = user.DisplayName
	}
	if err := s.users.repo.Update(ctx, user.ID, name, true); err != nil {
		return nil, err
	}
	// No sessions can exist yet, so the session-revoking variant is safe.
	if err := s.users.ChangePassword(ctx, user.ID, password); err != nil {
		return nil, err
	}
	if err := s.repo.MarkUsed(ctx, invite.ID); err != nil {
		return nil, err
	}
	return s.users.GetByEmail(ctx, invite.Email)
}

// List returns all invites, newest first, for the admin UI.
func (s *Invites) List(ctx context.Context) ([]Invite, error) {
	ctx, span := s.tracer.Start(ctx, "invites.list")
	defer span.End()
	return s.repo.List(ctx)
}

// Revoke deletes an invite; the disabled account row stays for re-invite.
func (s *Invites) Revoke(ctx context.Context, id uuid.UUID) error {
	ctx, span := s.tracer.Start(ctx, "invites.revoke")
	defer span.End()
	return s.repo.Delete(ctx, id)
}
