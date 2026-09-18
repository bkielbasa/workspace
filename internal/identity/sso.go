package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// SSOLink binds one external identity (provider + subject) to a workspace
// account. A user can have several links, so sign-in works through any of
// their configured identity providers.
type SSOLink struct {
	UserID    uuid.UUID
	Provider  string
	Subject   string
	Email     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SSORepository persists (provider, subject) -> user mappings.
type SSORepository interface {
	GetByProviderSubject(ctx context.Context, provider, subject string) (*SSOLink, error)
	Link(ctx context.Context, userID uuid.UUID, provider, subject, email string) error
}

// SSO maps verified OIDC identities onto workspace accounts. It never sees
// secrets: the web layer exchanges the code, verifies the id_token and hands
// over the claimed (subject, email, name). Only the account email is reused
// across providers; sign-in sessions are the usual cookie sessions.
type SSO struct {
	repo   SSORepository
	users  *Users
	tracer trace.Tracer
}

var ErrSSONotAllowed = errors.New("account not allowed to sign in via this provider")

func NewSSO(repo SSORepository, users *Users) *SSO {
	return &SSO{repo: repo, users: users, tracer: otel.Tracer("sso")}
}

// Authenticate resolves provider+subject to a workspace user.
//
// autoCreate controls just-in-time provisioning: when no link exists and no
// account owns the claimed email, the sign-in creates one (enabled, no
// password). When an account with the claimed email already exists it is
// linked and returned regardless — email addresses are unique, so this is the
// only safe behaviour, and the web layer only passes verified emails on.
func (s *SSO) Authenticate(ctx context.Context, provider, subject, email, displayName string, autoCreate bool) (*User, error) {
	ctx, span := s.tracer.Start(ctx, "sso.authenticate")
	defer span.End()

	email = strings.ToLower(strings.TrimSpace(email))

	link, err := s.repo.GetByProviderSubject(ctx, provider, subject)
	if err == nil {
		user, err := s.users.Get(ctx, link.UserID)
		if err != nil {
			return nil, err
		}
		if !user.Enabled {
			return nil, ErrSSONotAllowed
		}
		return user, nil
	}
	if !errors.Is(err, ErrUserNotFound) {
		return nil, err
	}

	// First sign-in with this provider. Reuse an account that already owns
	// the claimed email, or provision a new one when allowed.
	if existing, err := s.users.GetByEmail(ctx, email); err == nil && existing != nil {
		if !existing.Enabled {
			return nil, ErrSSONotAllowed
		}
		if err := s.repo.Link(ctx, existing.ID, provider, subject, email); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if !autoCreate {
		return nil, ErrUserNotFound
	}
	user, err := s.users.Provision(ctx, email, displayName)
	if err != nil {
		if errors.Is(err, ErrUserAlreadyExists) {
			// Lost a provisioning race: link the winner instead.
			if existing, getErr := s.users.GetByEmail(ctx, email); getErr == nil && existing != nil {
				if err := s.repo.Link(ctx, existing.ID, provider, subject, email); err != nil {
					return nil, err
				}
				return existing, nil
			}
		}
		return nil, err
	}
	if err := s.repo.Link(ctx, user.ID, provider, subject, email); err != nil {
		return nil, err
	}
	return user, nil
}
