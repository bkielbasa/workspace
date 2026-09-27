package identity

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUserNotFound        = errors.New("user not found")
	ErrUserAlreadyExists   = errors.New("user already exists")
	ErrUsernameImmutable   = errors.New("username cannot be changed once set")
	ErrDomainNotAllowed    = errors.New("domain not allowed")
	ErrSessionNotFound     = errors.New("session not found")
	ErrSessionExpired      = errors.New("session expired")
	ErrDomainNotFound      = errors.New("domain not found")
	ErrDomainAlreadyExists = errors.New("domain already exists")
	ErrAliasNotFound       = errors.New("alias not found")
	ErrAliasAlreadyExists  = errors.New("alias already exists")
	ErrInviteNotFound      = errors.New("invite not found")
	ErrInviteExpired       = errors.New("invite expired")
	ErrInviteUsed          = errors.New("invite already used")
	ErrInvalidUsername           = errors.New("invalid username")
	ErrInvalidPassword           = errors.New("invalid password")
	ErrOrganizationNotFound      = errors.New("organization not found")
	ErrOrganizationAlreadyExists = errors.New("organization already exists")
	ErrOrgDomainNotFound         = errors.New("organization domain not found")
	ErrOrgDomainAlreadyExists    = errors.New("organization domain already exists")
	ErrMemberNotFound            = errors.New("organization member not found")
	ErrMemberAlreadyExists       = errors.New("organization member already exists")
	ErrInvalidRole               = errors.New("invalid organization role")
	ErrSSONotFound               = errors.New("organization sso not found")
)

type User struct {
	ID             uuid.UUID
	OrganizationID *uuid.UUID
	Email          string
	Username       string
	PasswordHash   string
	DisplayName    string
	Enabled        bool
	IsAdmin        bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Session struct {
	Token     string
	UserID    uuid.UUID
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Domain struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Alias struct {
	ID          uuid.UUID `json:"id"`
	DomainID    uuid.UUID `json:"domain_id"`
	Address     string    `json:"address"`
	Destination string    `json:"destination"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

type Organization struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OrganizationDomain struct {
	ID                uuid.UUID  `json:"id"`
	OrganizationID    uuid.UUID  `json:"organization_id"`
	Domain            string     `json:"domain"`
	VerificationToken string     `json:"verification_token"`
	VerifiedAt        *time.Time `json:"verified_at"`
	CreatedAt         time.Time  `json:"created_at"`
}

type OrganizationMember struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	UserID         uuid.UUID `json:"user_id"`
	Role           string    `json:"role"`
	CreatedAt      time.Time `json:"created_at"`
}

type OrganizationSSO struct {
	ID             uuid.UUID `json:"id"`
	OrganizationID uuid.UUID `json:"organization_id"`
	Name           string    `json:"name"`
	Issuer         string    `json:"issuer"`
	ClientID       string    `json:"client_id"`
	ClientSecret   string    `json:"client_secret"`
	Scopes         []string  `json:"scopes"`
	EnforceSSO     bool      `json:"enforce_sso"`
	AutoProvision  bool      `json:"auto_provision"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
