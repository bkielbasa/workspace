package identity

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/md4"
)

type UserRepository interface {
	Create(ctx context.Context, email, username, passwordHash, displayName string) (*User, error)
	Get(ctx context.Context, id uuid.UUID) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	List(ctx context.Context) ([]User, error)
	Update(ctx context.Context, id uuid.UUID, displayName string, enabled bool) error
	Delete(ctx context.Context, id uuid.UUID) error
	ChangePassword(ctx context.Context, id uuid.UUID, passwordHash string) error
	SetUsername(ctx context.Context, id uuid.UUID, username string) error
}

type Users struct {
	repo     UserRepository
	sessions SessionRepository
	sync     []PasswordSync
	tracer   trace.Tracer
}

// PasswordSync mirrors account lifecycle into external credential stores
// (currently the Samba user database). It only ever receives hashes.
type PasswordSync interface {
	SetPassword(email, ntHash string) error
	SetEnabled(email string, enabled bool) error
	RemoveUser(email string) error
}

func NewUsers(repo UserRepository, sessions SessionRepository, sync ...PasswordSync) *Users {
	return &Users{repo: repo, sessions: sessions, sync: sync, tracer: otel.Tracer("users")}
}

// NormalizeUsername lowercases and trims a login name.
func NormalizeUsername(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ValidateUsername enforces login-name rules: 2-32 chars of letters,
// digits, dot, underscore, plus and dash.
func ValidateUsername(name string) error {
	name = NormalizeUsername(name)
	if len(name) < 2 || len(name) > 32 {
		return fmt.Errorf("username must be 2-32 characters")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '+', r == '-':
		default:
			return fmt.Errorf("username may only contain letters, digits, dot, underscore, plus and dash")
		}
	}
	return nil
}

// deriveUsername proposes a unique login from an email local part.
func (u *Users) deriveUsername(ctx context.Context, email string) (string, error) {
	base := NormalizeUsername(email)
	if i := strings.IndexByte(base, '@'); i >= 0 {
		base = base[:i]
	}
	var clean strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '+', r == '-':
			clean.WriteRune(r)
		}
	}
	base = clean.String()
	if len(base) < 2 {
		base = "user"
	}
	if len(base) > 28 {
		base = base[:28]
	}
	for i := 0; ; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s%d", base, i+1)
		}
		if _, err := u.repo.GetByUsername(ctx, candidate); err != nil {
			return candidate, nil
		}
		if i > 999 {
			return "", fmt.Errorf("no free username for %s", email)
		}
	}
}

// NTHash renders the NTLM response hash for a plaintext password.
func NTHash(password string) (string, error) {
	h := md4.New()
	var buf [2]byte
	for _, r := range password {
		for _, v := range utf16.Encode([]rune{r}) {
			binary.LittleEndian.PutUint16(buf[:], v)
			if _, err := h.Write(buf[:]); err != nil {
				return "", err
			}
		}
	}
	sum := h.Sum(nil)
	const hexdigits = "0123456789ABCDEF"
	out := make([]byte, 0, 32)
	for _, b := range sum {
		out = append(out, hexdigits[b>>4], hexdigits[b&0x0f])
	}
	return string(out), nil
}

func (u *Users) syncSetPassword(email, password string) {
	if len(u.sync) == 0 || password == "" {
		return
	}
	hash, err := NTHash(password)
	if err != nil {
		return
	}
	for _, s := range u.sync {
		_ = s.SetPassword(email, hash)
	}
}

func (u *Users) syncSetEnabled(email string, enabled bool) {
	for _, s := range u.sync {
		_ = s.SetEnabled(email, enabled)
	}
}

func (u *Users) syncRemoveUser(email string) {
	for _, s := range u.sync {
		_ = s.RemoveUser(email)
	}
}

func (u *Users) Create(ctx context.Context, email, password, displayName string) (*User, error) {
	ctx, span := u.tracer.Start(ctx, "users.create")
	defer span.End()

	email = strings.ToLower(strings.TrimSpace(email))
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, fmt.Errorf("invalid email address")
	}

	username, err := u.deriveUsername(ctx, email)
	if err != nil {
		return nil, err
	}
	passwordHashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	user, err := u.repo.Create(ctx, email, username, string(passwordHashBytes), displayName)
	if err != nil {
		return nil, err
	}
	u.syncSetPassword(user.Email, password)
	return user, nil
}

// CreateDisabled provisions an account that cannot log in yet (invite flow).
// The password is a random unknown value; Accept replaces it.
func (u *Users) CreateDisabled(ctx context.Context, email, displayName string) (*User, error) {
	ctx, span := u.tracer.Start(ctx, "users.create_disabled")
	defer span.End()

	email = strings.ToLower(strings.TrimSpace(email))
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, fmt.Errorf("invalid email address")
	}
	bootstrap, err := GeneratePassword()
	if err != nil {
		return nil, err
	}
	username, err := u.deriveUsername(ctx, email)
	if err != nil {
		return nil, err
	}
	passwordHashBytes, err := bcrypt.GenerateFromPassword([]byte(bootstrap), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	user, err := u.repo.Create(ctx, email, username, string(passwordHashBytes), displayName)
	if err != nil {
		return nil, err
	}
	if err := u.repo.Update(ctx, user.ID, displayName, false); err != nil {
		return nil, err
	}
	user.Enabled = false
	return user, nil
}

// Provision creates an enabled account with no working password, for
// SSO-only users created on first sign-in. Unlike Create it never mirrors
// anything into password stores (Samba): the account has no password.
func (u *Users) Provision(ctx context.Context, email, displayName string) (*User, error) {
	ctx, span := u.tracer.Start(ctx, "users.provision")
	defer span.End()

	email = strings.ToLower(strings.TrimSpace(email))
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, fmt.Errorf("invalid email address")
	}
	bootstrap, err := GeneratePassword()
	if err != nil {
		return nil, err
	}
	passwordHashBytes, err := bcrypt.GenerateFromPassword([]byte(bootstrap), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	username, err := u.deriveUsername(ctx, email)
	if err != nil {
		return nil, err
	}
	return u.repo.Create(ctx, email, username, string(passwordHashBytes), displayName)
}

// SetUsername renames a login alias. Emails, DAV principals and share paths
// all keep using the email address, so this is safe to change any time.
func (u *Users) SetUsername(ctx context.Context, id uuid.UUID, username string) error {
	ctx, span := u.tracer.Start(ctx, "users.set_username")
	defer span.End()

	username = NormalizeUsername(username)
	if err := ValidateUsername(username); err != nil {
		return err
	}
	if existing, err := u.repo.GetByUsername(ctx, username); err == nil && existing != nil && existing.ID != id {
		return ErrUserAlreadyExists
	}
	return u.repo.SetUsername(ctx, id, username)
}

// GetByLogin resolves an email address or a username, for sign-in forms.
func (u *Users) GetByLogin(ctx context.Context, login string) (*User, error) {
	ctx, span := u.tracer.Start(ctx, "users.get_by_login")
	defer span.End()

	login = strings.TrimSpace(login)
	if strings.Contains(login, "@") {
		return u.repo.GetByEmail(ctx, strings.ToLower(login))
	}
	return u.repo.GetByUsername(ctx, NormalizeUsername(login))
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
		_ = u.sessions.DeleteAllForUser(ctx, id)
	}
	if existing, err := u.repo.Get(ctx, id); err == nil && existing != nil {
		u.syncSetEnabled(existing.Email, enabled)
	}
	return nil
}

func (u *Users) Delete(ctx context.Context, id uuid.UUID) error {
	ctx, span := u.tracer.Start(ctx, "users.delete")
	defer span.End()
	existing, err := u.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := u.repo.Delete(ctx, id); err != nil {
		return err
	}
	if existing != nil {
		u.syncRemoveUser(existing.Email)
	}
	return nil
}

func (u *Users) Authenticate(ctx context.Context, login, password string) (*User, error) {
	ctx, span := u.tracer.Start(ctx, "users.authenticate")
	defer span.End()

	user, err := u.GetByLogin(ctx, login)
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
	if existing, err := u.repo.Get(ctx, id); err == nil && existing != nil {
		u.syncSetPassword(existing.Email, password)
	}
	if u.sessions != nil {
		return u.sessions.DeleteAllForUser(ctx, id)
	}
	return nil
}
