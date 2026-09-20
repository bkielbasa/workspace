package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type memUserStore struct {
	byID    map[uuid.UUID]*User
	byEmail map[string]*User
}

func newMemUserStore() *memUserStore {
	return &memUserStore{byID: map[uuid.UUID]*User{}, byEmail: map[string]*User{}}
}

func (m *memUserStore) Create(_ context.Context, email, username, hash, name string) (*User, error) {
	for _, existing := range m.byID {
		if existing.Username == username || existing.Email == email {
			return nil, errors.New("user already exists")
		}
	}
	u := &User{ID: uuid.New(), Email: email, Username: username, PasswordHash: hash, DisplayName: name, Enabled: true}
	m.byID[u.ID] = u
	m.byEmail[email] = u
	cp := *u
	return &cp, nil
}

func (m *memUserStore) Get(_ context.Context, id uuid.UUID) (*User, error) {
	u, ok := m.byID[id]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := *u
	return &cp, nil
}

func (m *memUserStore) GetByUsername(_ context.Context, username string) (*User, error) {
	for _, u := range m.byEmail {
		if u.Username == username {
			cp := *u
			return &cp, nil
		}
	}
	return nil, ErrUserNotFound
}

func (m *memUserStore) SetUsername(_ context.Context, id uuid.UUID, username string) error {
	u, ok := m.byID[id]
	if !ok {
		return errors.New("not found")
	}
	u.Username = username
	return nil
}

func (m *memUserStore) GetByEmail(_ context.Context, email string) (*User, error) {
	u, ok := m.byEmail[email]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

func (m *memUserStore) List(context.Context) ([]User, error) { return nil, nil }

func (m *memUserStore) Update(_ context.Context, id uuid.UUID, name string, enabled bool) error {
	u, ok := m.byID[id]
	if !ok {
		return errors.New("not found")
	}
	u.DisplayName, u.Enabled = name, enabled
	m.byEmail[u.Email] = u
	return nil
}

func (m *memUserStore) Delete(_ context.Context, id uuid.UUID) error {
	if u, ok := m.byID[id]; ok {
		delete(m.byEmail, u.Email)
		delete(m.byID, id)
	}
	return nil
}

func (m *memUserStore) ChangePassword(_ context.Context, id uuid.UUID, hash string) error {
	u, ok := m.byID[id]
	if !ok {
		return errors.New("not found")
	}
	u.PasswordHash = hash
	return nil
}

type memSync struct {
	passwords map[string]string
	enabled   map[string]bool
	removed   []string
}

func (m *memSync) SetPassword(email, ntHash string) error {
	if m.passwords == nil {
		m.passwords = map[string]string{}
	}
	m.passwords[email] = ntHash
	return nil
}

func (m *memSync) SetEnabled(email string, enabled bool) error {
	if m.enabled == nil {
		m.enabled = map[string]bool{}
	}
	m.enabled[email] = enabled
	return nil
}

func (m *memSync) RemoveUser(email string) error {
	m.removed = append(m.removed, email)
	return nil
}

func TestSyncLifecycle(t *testing.T) {
	ctx := context.Background()
	syncer := &memSync{}
	users := NewUsers(newMemUserStore(), nil, "cloudlift.run", syncer)

	created, err := users.Create(ctx, "bob@example.com", "s3cret-pw", "Bob")
	if err != nil {
		t.Fatal(err)
	}
	wantHash, _ := NTHash("s3cret-pw")
	if syncer.passwords[created.Email] != wantHash {
		t.Errorf("create did not sync NT hash")
	}

	if err := users.ChangePassword(ctx, created.ID, "n3w-pw"); err != nil {
		t.Fatal(err)
	}
	wantHash, _ = NTHash("n3w-pw")
	if syncer.passwords[created.Email] != wantHash {
		t.Errorf("change did not re-sync NT hash")
	}

	if err := users.Update(ctx, created.ID, "Bob", false); err != nil {
		t.Fatal(err)
	}
	if syncer.enabled[created.Email] {
		t.Errorf("disable not synced")
	}

	if err := users.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if len(syncer.removed) != 1 || syncer.removed[0] != created.Email {
		t.Errorf("delete not synced: %v", syncer.removed)
	}
}

func TestSyncAbsentIsNoop(t *testing.T) {
	ctx := context.Background()
	users := NewUsers(newMemUserStore(), nil, "cloudlift.run")
	if _, err := users.Create(ctx, "solo@example.com", "s3cret-pw", "Solo"); err != nil {
		t.Fatalf("create without syncer: %v", err)
	}
}
