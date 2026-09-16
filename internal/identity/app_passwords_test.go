package identity

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type memAppRepo struct {
	mu   sync.Mutex
	recs []AppPassword
	hash map[uuid.UUID]string
}

func newMemAppRepo() *memAppRepo {
	return &memAppRepo{hash: map[uuid.UUID]string{}}
}

func (m *memAppRepo) Create(_ context.Context, userID uuid.UUID, name, hash string) (*AppPassword, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := AppPassword{ID: uuid.New(), UserID: userID, Name: name}
	m.recs = append(m.recs, rec)
	m.hash[rec.ID] = hash
	return &rec, nil
}

func (m *memAppRepo) List(_ context.Context, userID uuid.UUID) ([]AppPassword, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppPassword
	for _, r := range m.recs {
		if r.UserID == userID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memAppRepo) Hashes(_ context.Context, userID uuid.UUID) ([]AppPasswordHash, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppPasswordHash
	for _, r := range m.recs {
		if r.UserID == userID {
			out = append(out, AppPasswordHash{ID: r.ID, Hash: m.hash[r.ID]})
		}
	}
	return out, nil
}

func (m *memAppRepo) Touch(_ context.Context, id uuid.UUID) error { return nil }

func (m *memAppRepo) Delete(_ context.Context, userID, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.recs[:0]
	for _, r := range m.recs {
		if r.ID != id || r.UserID != userID {
			kept = append(kept, r)
		}
	}
	m.recs = kept
	return nil
}

func (m *memAppRepo) DeleteByName(_ context.Context, userID uuid.UUID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.recs[:0]
	for _, r := range m.recs {
		if r.Name != name || r.UserID != userID {
			kept = append(kept, r)
		}
	}
	m.recs = kept
	return nil
}

type memUserRepo struct {
	users map[string]*User
}

func (m memUserRepo) Create(context.Context, string, string, string, string) (*User, error) {
	return nil, nil
}

func (m memUserRepo) GetByUsername(context.Context, string) (*User, error) {
	return nil, errors.New("not found")
}

func (m memUserRepo) SetUsername(context.Context, uuid.UUID, string) error {
	return nil
}

func (m memUserRepo) Get(context.Context, uuid.UUID) (*User, error) { return nil, nil }

func (m memUserRepo) GetByEmail(_ context.Context, email string) (*User, error) {
	if u, ok := m.users[email]; ok {
		return u, nil
	}
	return nil, errors.New("not found")
}

func (m memUserRepo) List(context.Context) ([]User, error) { return nil, nil }

func (m memUserRepo) Update(context.Context, uuid.UUID, string, bool) error { return nil }

func (m memUserRepo) Delete(context.Context, uuid.UUID) error { return nil }

func (m memUserRepo) ChangePassword(context.Context, uuid.UUID, string) error { return nil }

func setupAppPasswords(t *testing.T) (*AppPasswords, *DeviceAuth, *User, *memAppRepo) {
	t.Helper()
	userID := uuid.New()
	hash, _ := bcrypt.GenerateFromPassword([]byte("master-secret"), bcrypt.DefaultCost)
	users := NewUsers(memUserRepo{users: map[string]*User{
		"alice@example.com": {ID: userID, Email: "alice@example.com", PasswordHash: string(hash), Enabled: true},
	}}, nil)
	repo := newMemAppRepo()
	apps := NewAppPasswords(repo, users)
	user, err := users.GetByEmail(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return apps, NewDeviceAuth(users, apps), user, repo
}

func TestGenerateRotateListRevoke(t *testing.T) {
	ctx := context.Background()
	apps, _, user, _ := setupAppPasswords(t)

	plain, rec, err := apps.Generate(ctx, user.ID, "Phone")
	if err != nil || plain == "" || rec.Name != "Phone" {
		t.Fatalf("generate = %q, %+v, %v", plain, rec, err)
	}
	if _, _, err := apps.Generate(ctx, user.ID, "Phone"); err != nil {
		t.Fatal(err)
	}
	if list, _ := apps.List(ctx, user.ID); len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	// Rotate replaces same-named entries.
	if _, _, err := apps.Rotate(ctx, user.ID, "Phone"); err != nil {
		t.Fatal(err)
	}
	if list, _ := apps.List(ctx, user.ID); len(list) != 1 || list[0].Name != "Phone" {
		t.Fatalf("rotate left %+v", list)
	}
	// Revoke empties it.
	revoked, _ := apps.List(ctx, user.ID)
	if err := apps.Revoke(ctx, user.ID, revoked[0].ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := apps.List(ctx, user.ID); len(list) != 0 {
		t.Fatalf("revoke left %+v", list)
	}
}

func TestDeviceAuthMasterAndApp(t *testing.T) {
	ctx := context.Background()
	apps, device, user, _ := setupAppPasswords(t)

	if _, err := device.Authenticate(ctx, "alice@example.com", "master-secret"); err != nil {
		t.Fatalf("master rejected: %v", err)
	}
	plain, _, err := apps.Generate(ctx, user.ID, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	got, err := device.Authenticate(ctx, "alice@example.com", plain)
	if err != nil || got.ID != user.ID {
		t.Fatalf("app password rejected: %v", err)
	}
	if _, err := device.Authenticate(ctx, "alice@example.com", "wrong"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if _, err := device.Authenticate(ctx, "nobody@example.com", plain); err == nil {
		t.Fatal("unknown user accepted")
	}
}
