package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	for _, ok := range []string{"alice", "bob.smith", "a_b-c+d", "u2"} {
		if err := ValidateUsername(ok); err != nil {
			t.Errorf("ValidateUsername(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "x", "has space", "upper@x", "a!", "no/slash", strings.Repeat("a", 33)} {
		if err := ValidateUsername(bad); err == nil {
			t.Errorf("ValidateUsername(%q) accepted", bad)
		}
	}
}

func TestLoginByEmailOrUsername(t *testing.T) {
	ctx := context.Background()
	users := NewUsers(newMemUserStore(), nil, "cloudlift.run")

	created, err := users.Create(ctx, "Alice@Example.com", "s3cret-pw", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if created.Username != "alice" {
		t.Fatalf("derived username = %q", created.Username)
	}
	if _, err := users.Authenticate(ctx, "alice@cloudlift.run", "s3cret-pw"); err != nil {
		t.Fatalf("email login: %v", err)
	}
	if _, err := users.Authenticate(ctx, "alice", "s3cret-pw"); err != nil {
		t.Fatalf("username login: %v", err)
	}
	if _, err := users.Authenticate(ctx, "ALICE", "s3cret-pw"); err != nil {
		t.Fatalf("uppercase username login: %v", err)
	}

	// Same local part on another domain gets a suffix.
	other, err := users.Create(ctx, "alice@other.com", "s3cret-pw", "Other Alice")
	if err != nil {
		t.Fatal(err)
	}
	if other.Username != "alice2" {
		t.Fatalf("dedup username = %q", other.Username)
	}
}

func TestSetUsername(t *testing.T) {
	ctx := context.Background()
	store := newMemUserStore()
	users := NewUsers(store, nil, "cloudlift.run")

	// Setting username on a user who has no username yet succeeds
	legacy, err := store.Create(ctx, "legacy@cloudlift.run", "", "hash", "Legacy")
	if err != nil {
		t.Fatal(err)
	}
	if err := users.SetUsername(ctx, legacy.ID, "legacyuser"); err != nil {
		t.Fatalf("set username on legacy user: %v", err)
	}
	updated, err := store.Get(ctx, legacy.ID)
	if err != nil || updated.Username != "legacyuser" {
		t.Fatalf("expected username legacyuser, got %v (err: %v)", updated, err)
	}

	// Changing an existing username returns ErrUsernameImmutable
	if err := users.SetUsername(ctx, legacy.ID, "newname"); !errors.Is(err, ErrUsernameImmutable) {
		t.Fatalf("expected ErrUsernameImmutable, got %v", err)
	}

	// Setting the same username is a no-op
	if err := users.SetUsername(ctx, legacy.ID, "legacyuser"); err != nil {
		t.Fatalf("expected no-op for same username, got %v", err)
	}

	// Setting a username that is already taken by another user returns ErrUserAlreadyExists
	_, err = store.Create(ctx, "other@cloudlift.run", "otheruser", "hash", "Other")
	if err != nil {
		t.Fatal(err)
	}
	legacy2, err := store.Create(ctx, "legacy2@cloudlift.run", "", "hash", "Legacy 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := users.SetUsername(ctx, legacy2.ID, "otheruser"); !errors.Is(err, ErrUserAlreadyExists) {
		t.Fatalf("expected ErrUserAlreadyExists, got %v", err)
	}

	// Invalid username is rejected
	if err := users.SetUsername(ctx, legacy.ID, "bad name!"); err == nil {
		t.Fatal("invalid username accepted")
	}
}

func TestUsersPrimaryEmailFormatting(t *testing.T) {
	ctx := context.Background()
	users := NewUsers(newMemUserStore(), nil, "cloudlift.run")

	u1, err := users.Create(ctx, "alice", "s3cret-password", "Alice")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if u1.Username != "alice" {
		t.Errorf("expected username 'alice', got %q", u1.Username)
	}
	if u1.Email != "alice@cloudlift.run" {
		t.Errorf("expected primary email 'alice@cloudlift.run', got %q", u1.Email)
	}

	// Passing an external email also extracts username and formats with primary domain
	u2, err := users.Create(ctx, "bob@external.com", "s3cret-password", "Bob")
	if err != nil {
		t.Fatalf("Create with external email failed: %v", err)
	}
	if u2.Username != "bob" {
		t.Errorf("expected username 'bob', got %q", u2.Username)
	}
	if u2.Email != "bob@cloudlift.run" {
		t.Errorf("expected primary email 'bob@cloudlift.run', got %q", u2.Email)
	}
}

func TestUsersUsernameImmutability(t *testing.T) {
	ctx := context.Background()
	users := NewUsers(newMemUserStore(), nil, "cloudlift.run")

	u, err := users.Create(ctx, "charlie", "s3cret-password", "Charlie")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Setting identical username is allowed (no-op)
	if err := users.SetUsername(ctx, u.ID, "charlie"); err != nil {
		t.Errorf("expected no-op for same username, got error: %v", err)
	}

	// Changing username is rejected
	err = users.SetUsername(ctx, u.ID, "charlotte")
	if !errors.Is(err, ErrUsernameImmutable) {
		t.Errorf("expected ErrUsernameImmutable, got %v", err)
	}
}

func TestUsersPrimaryDomainDefault(t *testing.T) {
	users := NewUsers(newMemUserStore(), nil, "")
	if got := users.PrimaryDomain(); got != "cloudlift.run" {
		t.Errorf("expected default primary domain 'cloudlift.run', got %q", got)
	}
}


