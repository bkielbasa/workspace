package identity

import (
	"context"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAuthenticateUsernameVsOrgEmail(t *testing.T) {
	ctx := context.Background()
	store := newMemUserStore()
	users := NewUsers(store, nil, "cloudlift.run")

	password := "correct-password"
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}
	hash := string(hashBytes)

	// user alice@cloudlift.run created with username "alice"
	_, err = store.Create(ctx, "alice@cloudlift.run", "alice", hash, "Alice")
	if err != nil {
		t.Fatalf("failed to create alice: %v", err)
	}

	// user bob@acme.corp created with username "bob" in org with acme.corp
	_, err = store.Create(ctx, "bob@acme.corp", "bob", hash, "Bob")
	if err != nil {
		t.Fatalf("failed to create bob: %v", err)
	}

	// Verify "alice" resolves to alice@cloudlift.run
	uAlice, err := users.GetByLogin(ctx, "alice")
	if err != nil {
		t.Fatalf("GetByLogin(alice) error: %v", err)
	}
	if uAlice.Email != "alice@cloudlift.run" {
		t.Errorf("expected alice@cloudlift.run, got %s", uAlice.Email)
	}

	// Verify "alice" authenticates successfully
	if _, err := users.Authenticate(ctx, "alice", password); err != nil {
		t.Errorf("Authenticate(alice) failed: %v", err)
	}

	// Verify "alice@cloudlift.run" authenticates successfully
	if _, err := users.Authenticate(ctx, "alice@cloudlift.run", password); err != nil {
		t.Errorf("Authenticate(alice@cloudlift.run) failed: %v", err)
	}

	// Verify "bob" does NOT authenticate bob@acme.corp (checks bob@cloudlift.run which does not exist)
	if _, err := users.GetByLogin(ctx, "bob"); err == nil {
		t.Errorf("expected GetByLogin(bob) to fail, but succeeded")
	}
	if _, err := users.Authenticate(ctx, "bob", password); err == nil {
		t.Errorf("expected Authenticate(bob) to fail, but succeeded")
	}

	// Verify "bob@acme.corp" authenticates successfully
	uBob, err := users.GetByLogin(ctx, "bob@acme.corp")
	if err != nil {
		t.Fatalf("GetByLogin(bob@acme.corp) error: %v", err)
	}
	if uBob.Email != "bob@acme.corp" {
		t.Errorf("expected bob@acme.corp, got %s", uBob.Email)
	}
	if _, err := users.Authenticate(ctx, "bob@acme.corp", password); err != nil {
		t.Errorf("Authenticate(bob@acme.corp) failed: %v", err)
	}
}
