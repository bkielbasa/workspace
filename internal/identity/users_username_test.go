package identity

import (
	"context"
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
	users := NewUsers(newMemUserStore(), nil)

	created, err := users.Create(ctx, "Alice@Example.com", "s3cret-pw", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if created.Username != "alice" {
		t.Fatalf("derived username = %q", created.Username)
	}
	if _, err := users.Authenticate(ctx, "alice@example.com", "s3cret-pw"); err != nil {
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
	users := NewUsers(newMemUserStore(), nil)

	created, err := users.Create(ctx, "bob@example.com", "s3cret-pw", "Bob")
	if err != nil {
		t.Fatal(err)
	}
	if err := users.SetUsername(ctx, created.ID, "bobby"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := users.Authenticate(ctx, "bobby", "s3cret-pw"); err != nil {
		t.Fatalf("renamed login: %v", err)
	}
	if _, err := users.Authenticate(ctx, "bob", "s3cret-pw"); err == nil {
		t.Fatal("old username still works")
	}
	if err := users.SetUsername(ctx, created.ID, "bad name!"); err == nil {
		t.Fatal("invalid username accepted")
	}
}
