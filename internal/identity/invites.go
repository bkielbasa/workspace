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
)

// Invite token lifetime: family members are slow, but tokens are bearer
// credentials, so they expire and burn on use.
const InviteTTL = 7 * 24 * time.Hour

// Invite is a pending family onboarding. The plaintext token is shown once
// at creation; only its hash is stored.
type Invite struct {
	ID           uuid.UUID
	UserID       *uuid.UUID
	InvitedEmail string
	Email        string // backwards compatibility alias for InvitedEmail
	DisplayName  string
	ExpiresAt    time.Time
	UsedAt       *time.Time
	CreatedAt    time.Time
}

type InviteRepository interface {
	Create(ctx context.Context, invitedEmail, displayName, tokenHash string, expiresAt time.Time) (*Invite, error)
	GetByHash(ctx context.Context, tokenHash string) (*Invite, error)
	MarkUsed(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	List(ctx context.Context) ([]Invite, error)
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteForEmail(ctx context.Context, email string) error
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

// CreateInvite stores an external delivery email without pre-creating a disabled user
// and returns a one-time token. Re-inviting rotates: old tokens for the email burn.
func (s *Invites) CreateInvite(ctx context.Context, email, displayName string) (string, *Invite, error) {
	ctx, span := s.tracer.Start(ctx, "invites.create")
	defer span.End()

	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return "", nil, errors.New("valid email required")
	}

	_ = s.repo.DeleteForEmail(ctx, email)

	plain, hash, err := mintToken()
	if err != nil {
		return "", nil, err
	}
	invite, err := s.repo.Create(ctx, email, displayName, hash, time.Now().Add(InviteTTL))
	if err != nil {
		return "", nil, err
	}
	if invite != nil && invite.Email == "" {
		invite.Email = invite.InvitedEmail
	}
	return plain, invite, nil
}

// Lookup resolves a presented token without consuming it (for rendering the
// accept page). Validity is NOT checked here; Accept checks.
func (s *Invites) Lookup(ctx context.Context, plain string) (*Invite, error) {
	ctx, span := s.tracer.Start(ctx, "invites.lookup")
	defer span.End()

	if strings.TrimSpace(plain) == "" {
		return nil, ErrInviteNotFound
	}
	inv, err := s.repo.GetByHash(ctx, hashToken(plain))
	if err != nil {
		return nil, err
	}
	if inv != nil && inv.Email == "" {
		inv.Email = inv.InvitedEmail
	}
	return inv, nil
}

// Accept redeems a token: validates username, creates user via s.users.Create
// (which sets primary email to <username>@<primaryDomain>), and calls s.repo.MarkUsed.
func (s *Invites) Accept(ctx context.Context, token, username, displayName, password string) (*User, error) {
	ctx, span := s.tracer.Start(ctx, "invites.accept")
	defer span.End()

	invite, err := s.Lookup(ctx, token)
	if err != nil {
		return nil, err
	}
	if invite.UsedAt != nil {
		return nil, ErrInviteUsed
	}
	if time.Now().After(invite.ExpiresAt) {
		return nil, ErrInviteExpired
	}

	username = NormalizeUsername(username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}

	if strings.TrimSpace(displayName) == "" {
		displayName = invite.DisplayName
	}

	user, err := s.users.Create(ctx, username, password, displayName)
	if err != nil {
		return nil, err
	}

	if err := s.repo.MarkUsed(ctx, invite.ID, user.ID); err != nil {
		_ = s.users.Delete(ctx, user.ID)
		return nil, fmt.Errorf("redeem invite: %w", err)
	}
	return user, nil
}

// List returns all invites, newest first, for the admin UI.
func (s *Invites) List(ctx context.Context) ([]Invite, error) {
	ctx, span := s.tracer.Start(ctx, "invites.list")
	defer span.End()
	invites, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range invites {
		if invites[i].Email == "" {
			invites[i].Email = invites[i].InvitedEmail
		}
	}
	return invites, nil
}

// Revoke deletes an invite.
func (s *Invites) Revoke(ctx context.Context, id uuid.UUID) error {
	ctx, span := s.tracer.Start(ctx, "invites.revoke")
	defer span.End()
	return s.repo.Delete(ctx, id)
}
