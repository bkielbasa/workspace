package identity

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type memInviteRepo struct {
	mu    sync.Mutex
	recs  map[uuid.UUID]Invite
	byID  map[string]uuid.UUID
	users *memUserStore
}

func newMemInviteRepo(users *memUserStore) *memInviteRepo {
	return &memInviteRepo{recs: map[uuid.UUID]Invite{}, byID: map[string]uuid.UUID{}, users: users}
}

func (m *memInviteRepo) Create(_ context.Context, userID uuid.UUID, hash string, expires time.Time) (*Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv := Invite{ID: uuid.New(), UserID: userID, ExpiresAt: expires, CreatedAt: time.Now()}
	m.recs[inv.ID] = inv
	m.byID[hash] = inv.ID
	return &inv, nil
}

func (m *memInviteRepo) GetByHash(_ context.Context, hash string) (*Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byID[hash]
	if !ok {
		return nil, ErrInviteNotFound
	}
	inv := m.recs[id]
	// Mirror the JOIN in the real repository.
	if m.users != nil {
		for _, u := range m.users.byEmail {
			if u.ID == inv.UserID {
				inv.Email = u.Email
			}
		}
	}
	return &inv, nil
}

func (m *memInviteRepo) MarkUsed(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv := m.recs[id]
	now := time.Now()
	inv.UsedAt = &now
	m.recs[id] = inv
	return nil
}

func (m *memInviteRepo) List(context.Context) ([]Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Invite
	for _, inv := range m.recs {
		out = append(out, inv)
	}
	return out, nil
}

func (m *memInviteRepo) Delete(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for hash, iid := range m.byID {
		if iid == id {
			delete(m.byID, hash)
		}
	}
	delete(m.recs, id)
	return nil
}

func (m *memInviteRepo) DeleteForUser(_ context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, inv := range m.recs {
		if inv.UserID == userID {
			delete(m.recs, id)
		}
	}
	for hash, iid := range m.byID {
		if _, ok := m.recs[iid]; !ok {
			delete(m.byID, hash)
		}
	}
	return nil
}

func setupInvites(t *testing.T) (*Invites, *Users) {
	t.Helper()
	store := newMemUserStore()
	users := NewUsers(store, nil, "cloudlift.run")
	return NewInvites(newMemInviteRepo(store), users), users
}

func TestInviteCreateAcceptFlow(t *testing.T) {
	ctx := context.Background()
	invites, users := setupInvites(t)

	token, inv, err := invites.CreateInvite(ctx, "kid@example.com", "Kid")
	if err != nil || token == "" {
		t.Fatalf("create = %q, %+v, %v", token, inv, err)
	}
	if inv.Email != "kid@example.com" {
		t.Fatalf("invite email = %q", inv.Email)
	}

	// Lookup works before accept.
	found, err := invites.Lookup(ctx, token)
	if err != nil || found.UserID != inv.UserID {
		t.Fatalf("lookup = %+v, %v", found, err)
	}

	user, err := invites.Accept(ctx, token, "Kiddo", "long-enough-secret")
	if err != nil {
		t.Fatalf("accept = %v", err)
	}
	if !user.Enabled || user.DisplayName != "Kiddo" {
		t.Fatalf("activated = %+v", user)
	}
	// Master password works after accept.
	if _, err := users.Authenticate(ctx, "kid@example.com", "long-enough-secret"); err != nil {
		t.Fatalf("login after accept: %v", err)
	}

	// Double use burns.
	if _, err := invites.Accept(ctx, token, "Kiddo", "another-secret-pass"); err == nil {
		t.Fatal("double accept succeeded")
	}

	// Active accounts cannot be re-invited.
	if _, _, err := invites.CreateInvite(ctx, "kid@example.com", "Kid"); err == nil {
		t.Fatal("re-invite of active account succeeded")
	}
}

func TestInviteRejectsShortPassword(t *testing.T) {
	ctx := context.Background()
	invites, _ := setupInvites(t)
	token, _, err := invites.CreateInvite(ctx, "a@example.com", "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invites.Accept(ctx, token, "A", "short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestInviteUnknownToken(t *testing.T) {
	ctx := context.Background()
	invites, _ := setupInvites(t)
	if _, err := invites.Lookup(ctx, "nope"); err == nil {
		t.Fatal("unknown token found")
	}
	if _, err := invites.Accept(ctx, "nope", "A", "long-enough-secret"); err == nil {
		t.Fatal("unknown token accepted")
	}
}
