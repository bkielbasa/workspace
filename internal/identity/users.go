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

	passwordHashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	user, err := u.repo.Create(ctx, email, string(passwordHashBytes), displayName)
	if err != nil {
		return nil, err
	}
	u.syncSetPassword(user.Email, password)
	return user, nil
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
	if existing, err := u.repo.Get(ctx, id); err == nil && existing != nil {
		u.syncSetPassword(existing.Email, password)
	}
	if u.sessions != nil {
		return u.sessions.DeleteAllForUser(ctx, id)
	}
	return nil
}
