package web

import (
	"bytes"
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
