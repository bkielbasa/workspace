package web

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

func TestSettingsTemplateRendering(t *testing.T) {
	v, err := NewView()
	if err != nil {
		t.Fatalf("failed to initialize view: %v", err)
	}

	uid := uuid.New()
	data := &viewData{
		Title:   "Settings",
		Section: "settings",
		User: &identity.User{
			ID:          uid,
			Email:       "alice@cloudlift.run",
			Username:    "alice",
			DisplayName: "Alice Smith",
			CreatedAt:   time.Now(),
		},
		Signatures: []mail.Signature{
			{ID: uuid.New(), UserID: uid, Name: "Default", Content: "Best regards,\nAlice", IsDefault: true},
		},
		Rules: []mail.Rule{
			{ID: uuid.New(), UserID: uid, Name: "Test Rule", Enabled: true, Priority: 1},
		},
		Mailboxes: []mail.MailboxInfo{
			{ID: uuid.New(), UserID: uid, Name: "INBOX"},
		},
		CSRFToken: "dummy-csrf",
	}

	sections := []string{"profile", "password", "signatures", "rules", "iphone", "app-passwords", "invites"}
	for _, sec := range sections {
		data.Tab = sec
		var buf bytes.Buffer
		if err := v.settingsT.ExecuteTemplate(&buf, "layout", data); err != nil {
			t.Fatalf("failed to execute settings template for section %q: %v", sec, err)
		}
		output := buf.String()
		if len(output) == 0 {
			t.Errorf("expected non-empty output for section %q", sec)
		}
	}
}

func TestTemplateNavigationLinks(t *testing.T) {
	v, err := NewView()
	if err != nil {
		t.Fatalf("failed to initialize view: %v", err)
	}

	uid := uuid.New()
	data := &viewData{
		Title:   "Home",
		Section: "home",
		User: &identity.User{
			ID:          uid,
			Email:       "alice@cloudlift.run",
			Username:    "alice",
			DisplayName: "Alice Smith",
			CreatedAt:   time.Now(),
		},
		CSRFToken: "dummy-csrf",
	}

	// 1. Render nav.html (via v.home's "nav" template) and verify it contains href="/settings"
	{
		var buf bytes.Buffer
		if err := v.home.ExecuteTemplate(&buf, "nav", data); err != nil {
			t.Fatalf("failed to execute nav template: %v", err)
		}
		output := buf.String()
		if !strings.Contains(output, `href="/settings"`) {
			t.Errorf("nav.html: expected output to contain href=\"/settings\", got:\n%s", output)
		}
		// Also verify update to nav user menu: class="nav-user-link" and title="Account settings"
		if !strings.Contains(output, `title="Account settings"`) {
			t.Errorf("nav.html: expected output to contain title=\"Account settings\", got:\n%s", output)
		}
	}

	// 2. Render home.html (via v.home's "content" template) and verify it contains href="/settings"
	{
		var buf bytes.Buffer
		if err := v.home.ExecuteTemplate(&buf, "content", data); err != nil {
			t.Fatalf("failed to execute home content template: %v", err)
		}
		output := buf.String()
		if !strings.Contains(output, `href="/settings"`) {
			t.Errorf("home.html: expected output to contain href=\"/settings\", got:\n%s", output)
		}
		if !strings.Contains(output, `Settings</a>`) {
			t.Errorf("home.html: expected output to contain \"Settings</a>\", got:\n%s", output)
		}
	}

	// 3. Render mail.html (via v.mailT's "content" template) and verify it contains href="/settings?section=signatures"
	{
		var buf bytes.Buffer
		if err := v.mailT.ExecuteTemplate(&buf, "content", data); err != nil {
			t.Fatalf("failed to execute mail content template: %v", err)
		}
		output := buf.String()
		if !strings.Contains(output, `href="/settings?section=signatures"`) {
			t.Errorf("mail.html: expected output to contain href=\"/settings?section=signatures\", got:\n%s", output)
		}
	}

	// 4. Verify neither profile.html nor mail_settings.html exist
	paths := []string{
		"../../web/templates/profile.html",
		"../../web/templates/mail_settings.html",
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("expected %s to be deleted, but it still exists", p)
		} else if !os.IsNotExist(err) {
			t.Errorf("unexpected error checking %s: %v", p, err)
		}
	}
}
