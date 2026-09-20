package identity

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type memInviteRepo struct {
	mu     sync.Mutex
	recs   map[uuid.UUID]Invite
	byHash map[string]uuid.UUID
}

func newMemInviteRepo() *memInviteRepo {
	return &memInviteRepo{
		recs:   map[uuid.UUID]Invite{},
		byHash: map[string]uuid.UUID{},
	}
}

func newMemInviteStore() *memInviteRepo {
	return newMemInviteRepo()
}

func (m *memInviteRepo) Create(_ context.Context, invitedEmail, displayName, hash string, expires time.Time) (*Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv := Invite{
		ID:           uuid.New(),
		InvitedEmail: invitedEmail,
		Email:        invitedEmail,
		DisplayName:  displayName,
		ExpiresAt:    expires,
		CreatedAt:    time.Now(),
	}
	m.recs[inv.ID] = inv
	m.byHash[hash] = inv.ID
	return &inv, nil
}

func (m *memInviteRepo) GetByHash(_ context.Context, hash string) (*Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byHash[hash]
	if !ok {
		return nil, ErrInviteNotFound
	}
	inv := m.recs[id]
	if inv.Email == "" {
		inv.Email = inv.InvitedEmail
	}
	return &inv, nil
}

func (m *memInviteRepo) MarkUsed(_ context.Context, id uuid.UUID, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.recs[id]
	if !ok {
		return ErrInviteNotFound
	}
	now := time.Now()
	inv.UsedAt = &now
	inv.UserID = &userID
	m.recs[id] = inv
	return nil
}

func (m *memInviteRepo) List(context.Context) ([]Invite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Invite
	for _, inv := range m.recs {
		if inv.Email == "" {
			inv.Email = inv.InvitedEmail
		}
		out = append(out, inv)
	}
	return out, nil
}

func (m *memInviteRepo) Delete(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for hash, iid := range m.byHash {
		if iid == id {
			delete(m.byHash, hash)
		}
	}
	delete(m.recs, id)
	return nil
}

func (m *memInviteRepo) DeleteForEmail(_ context.Context, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, inv := range m.recs {
		if strings.EqualFold(inv.InvitedEmail, email) || strings.EqualFold(inv.Email, email) {
			delete(m.recs, id)
		}
	}
	for hash, iid := range m.byHash {
		if _, ok := m.recs[iid]; !ok {
			delete(m.byHash, hash)
		}
	}
	return nil
}

func setupInvites(t *testing.T) (*Invites, *Users) {
	t.Helper()
	store := newMemUserStore()
	users := NewUsers(store, nil, "cloudlift.run")
	return NewInvites(newMemInviteStore(), users), users
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
	if err != nil || found.UserID != nil {
		t.Fatalf("lookup = %+v, %v", found, err)
	}

	user, err := invites.Accept(ctx, token, "kid", "Kiddo", "long-enough-secret")
	if err != nil {
		t.Fatalf("accept = %v", err)
	}
	if !user.Enabled || user.DisplayName != "Kiddo" {
		t.Fatalf("activated = %+v", user)
	}
	if user.Username != "kid" {
		t.Fatalf("username = %q", user.Username)
	}
	if user.Email != "kid@cloudlift.run" {
		t.Fatalf("user email = %q", user.Email)
	}
	// Master password works after accept.
	if _, err := users.Authenticate(ctx, "kid", "long-enough-secret"); err != nil {
		t.Fatalf("login after accept: %v", err)
	}

	// Double use burns.
	if _, err := invites.Accept(ctx, token, "kid", "Kiddo", "another-secret-pass"); err == nil {
		t.Fatal("double accept succeeded")
	}
}

func TestInviteRejectsShortPassword(t *testing.T) {
	ctx := context.Background()
	invites, _ := setupInvites(t)
	token, _, err := invites.CreateInvite(ctx, "a@example.com", "A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invites.Accept(ctx, token, "validuser", "A", "short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestInviteUnknownToken(t *testing.T) {
	ctx := context.Background()
	invites, _ := setupInvites(t)
	if _, err := invites.Lookup(ctx, "nope"); err == nil {
		t.Fatal("unknown token found")
	}
	if _, err := invites.Accept(ctx, "nope", "validuser", "A", "long-enough-secret"); err == nil {
		t.Fatal("unknown token accepted")
	}
}

func TestInviteRejectsInvalidUsername(t *testing.T) {
	ctx := context.Background()
	invites, _ := setupInvites(t)
	token, _, err := invites.CreateInvite(ctx, "a@example.com", "A")
	if err != nil {
		t.Fatal(err)
	}
	// Username with invalid characters
	if _, err := invites.Accept(ctx, token, "bad username!", "A", "long-enough-secret"); err == nil {
		t.Fatal("invalid username accepted")
	}
	// Username too short
	if _, err := invites.Accept(ctx, token, "x", "A", "long-enough-secret"); err == nil {
		t.Fatal("too short username accepted")
	}
}

func TestInviteRejectsDuplicateUsername(t *testing.T) {
	ctx := context.Background()
	invites, users := setupInvites(t)

	// Pre-create a user with username "existinguser"
	_, err := users.Create(ctx, "existinguser", "password-1234", "Existing User")
	if err != nil {
		t.Fatalf("failed to create existing user: %v", err)
	}

	token, _, err := invites.CreateInvite(ctx, "newperson@example.com", "New Person")
	if err != nil {
		t.Fatal(err)
	}

	// Attempt to accept with same username should fail
	if _, err := invites.Accept(ctx, token, "existinguser", "New Person", "password-5678"); err == nil {
		t.Fatal("expected duplicate username error, got nil")
	}

	// The invite should still be valid with a different username
	user, err := invites.Accept(ctx, token, "newperson", "New Person", "password-5678")
	if err != nil {
		t.Fatalf("failed to accept invite with unique username: %v", err)
	}
	if user.Username != "newperson" {
		t.Errorf("expected username 'newperson', got %q", user.Username)
	}
}

func TestInviteCreateAndAcceptWithUsername(t *testing.T) {
	ctx := context.Background()
	userStore := newMemUserStore()
	users := NewUsers(userStore, nil, "cloudlift.run")
	inviteStore := newMemInviteStore()
	invites := NewInvites(inviteStore, users)

	// Create invite for external email
	token, inv, err := invites.CreateInvite(ctx, "friend@example.com", "Friend")
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}
	if inv.InvitedEmail != "friend@example.com" {
		t.Errorf("expected invited email 'friend@example.com', got %q", inv.InvitedEmail)
	}
	if inv.UserID != nil {
		t.Errorf("expected UserID to be nil for pending invite, got %v", inv.UserID)
	}

	// Lookup invite by token
	lookup, err := invites.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if lookup.InvitedEmail != "friend@example.com" {
		t.Errorf("expected lookup email 'friend@example.com', got %q", lookup.InvitedEmail)
	}

	// Accept invite specifying chosen username
	user, err := invites.Accept(ctx, token, "frienduser", "Friend Smith", "s3cret-password")
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	if user.Username != "frienduser" {
		t.Errorf("expected username 'frienduser', got %q", user.Username)
	}
	if user.Email != "frienduser@cloudlift.run" {
		t.Errorf("expected primary email 'frienduser@cloudlift.run', got %q", user.Email)
	}

	// Re-accepting should fail with ErrInviteUsed
	_, err = invites.Accept(ctx, token, "anothername", "Other", "password123")
	if !errors.Is(err, ErrInviteUsed) {
		t.Errorf("expected ErrInviteUsed, got %v", err)
	}
}

type failMarkUsedInviteStore struct {
	*memInviteRepo
}

func (f *failMarkUsedInviteStore) MarkUsed(ctx context.Context, id, userID uuid.UUID) error {
	return errors.New("database connection lost")
}

func TestInviteAcceptRollbackOnMarkUsedFailure(t *testing.T) {
	ctx := context.Background()
	userStore := newMemUserStore()
	users := NewUsers(userStore, nil, "cloudlift.run")
	baseStore := newMemInviteStore()
	failStore := &failMarkUsedInviteStore{memInviteRepo: baseStore}
	invites := NewInvites(failStore, users)

	token, _, err := invites.CreateInvite(ctx, "victim@example.com", "Victim")
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}

	_, err = invites.Accept(ctx, token, "victimuser", "Victim", "password123")
	if err == nil {
		t.Fatal("expected error from MarkUsed failure, got nil")
	}

	// Verify user was rolled back from users store
	if _, err := userStore.GetByUsername(ctx, "victimuser"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("expected ErrUserNotFound after rollback, got %v", err)
	}
}

