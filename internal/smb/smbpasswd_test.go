package smb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bklimczak/workspace/internal/identity"
)

func TestNTHashVector(t *testing.T) {
	// Well-known NTLM test vector for "password".
	got, err := identity.NTHash("password")
	if err != nil {
		t.Fatal(err)
	}
	if got != "8846F7EAEE8FB117AD06BDD830B7586C" {
		t.Fatalf("NTHash = %q", got)
	}
}

func TestManagerRoundtrip(t *testing.T) {
	m, err := NewManager(filepath.Join(t.TempDir(), "smbpasswd"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetPassword("alice@example.com", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPassword("bob@example.com", "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(m.path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q", raw)
	}
	for _, line := range lines {
		parts := strings.Split(line, ":")
		if len(parts) != 6 {
			t.Fatalf("fields in %q", line)
		}
		if parts[2] != "XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX" {
			t.Errorf("LM not disabled in %q", line)
		}
		if !strings.HasPrefix(parts[4], "[U") {
			t.Errorf("not enabled in %q", line)
		}
	}

	// Disable preserves the hash.
	if err := m.SetEnabled("alice@example.com", false); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(m.path)
	if !strings.Contains(string(raw), "[D          ]") {
		t.Errorf("disable flag missing:\n%s", raw)
	}
	if !strings.Contains(string(raw), "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Errorf("hash lost on disable:\n%s", raw)
	}

	// Unknown login is an error, not a silent create.
	if err := m.SetEnabled("nobody@example.com", true); err == nil {
		t.Errorf("expected error for unknown login")
	}

	// Rotation replaces the hash, removal deletes the row.
	if err := m.SetPassword("alice@example.com", "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveUser("bob@example.com"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(m.path)
	if strings.Contains(string(raw), "bob@example.com") || !strings.Contains(string(raw), "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC") {
		t.Errorf("rotate/remove wrong:\n%s", raw)
	}
}

func TestManagerFileMode(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(filepath.Join(dir, "sub", "smbpasswd"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetPassword("a@b.c", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "sub", "smbpasswd"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
}
