package web

import (
	"strings"
	"testing"
	"time"
)

func TestParseMailContent(t *testing.T) {
	t.Run("plain text message", func(t *testing.T) {
		raw := "From: alice@example.com\r\nTo: bob@example.com\r\nSubject: Hello\r\nContent-Type: text/plain\r\n\r\nHello Bob, how are you doing today?"
		body, snippet := parseMailContent(raw, "text/plain")
		if body != "Hello Bob, how are you doing today?" {
			t.Errorf("unexpected body: %q", body)
		}
		if snippet != "Hello Bob, how are you doing today?" {
			t.Errorf("unexpected snippet: %q", snippet)
		}
	})

	t.Run("multipart alternative with html and text", func(t *testing.T) {
		raw := strings.Join([]string{
			"From: sender@example.com",
			"To: recipient@example.com",
			"Subject: Multipart Test",
			"Content-Type: multipart/alternative; boundary=\"boundary123\"",
			"",
			"--boundary123",
			"Content-Type: text/plain; charset=UTF-8",
			"",
			"Plain text version of message.",
			"--boundary123",
			"Content-Type: text/html; charset=UTF-8",
			"",
			"<p>HTML <b>version</b> of message.</p>",
			"--boundary123--",
		}, "\r\n")

		body, snippet := parseMailContent(raw, "multipart/alternative")
		if !strings.Contains(body, "Plain text version") {
			t.Errorf("expected plain text part to be selected, got: %q", body)
		}
		if !strings.Contains(snippet, "Plain text version") {
			t.Errorf("expected snippet to contain plain text, got: %q", snippet)
		}
	})

	t.Run("html only stripped to text", func(t *testing.T) {
		raw := "From: test@example.com\r\nTo: user@example.com\r\nSubject: HTML\r\nContent-Type: text/html\r\n\r\n<h1>Welcome</h1><p>Thanks for joining!</p>"
		body, snippet := parseMailContent(raw, "text/html")
		if !strings.Contains(body, "Welcome") || !strings.Contains(body, "Thanks for joining!") {
			t.Errorf("unexpected html-stripped body: %q", body)
		}
		if strings.Contains(body, "<h1>") || strings.Contains(body, "<p>") {
			t.Errorf("expected HTML tags to be stripped, got: %q", body)
		}
		if !strings.Contains(snippet, "Welcome") {
			t.Errorf("unexpected snippet: %q", snippet)
		}
	})

	t.Run("quoted printable decoding", func(t *testing.T) {
		raw := "From: user@example.com\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nHello=20World=21"
		body, _ := parseMailContent(raw, "text/plain")
		if body != "Hello World!" {
			t.Errorf("expected decoded quoted-printable 'Hello World!', got: %q", body)
		}
	})

	t.Run("base64 decoding", func(t *testing.T) {
		raw := "From: user@example.com\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\nSGVsbG8gZnJvbSBCYXNlNjQh"
		body, _ := parseMailContent(raw, "text/plain")
		if body != "Hello from Base64!" {
			t.Errorf("expected decoded base64 'Hello from Base64!', got: %q", body)
		}
	})
}

func TestParseSender(t *testing.T) {
	name, addr := parseSender("Alice Smith <alice@example.com>")
	if name != "Alice Smith" || addr != "alice@example.com" {
		t.Errorf("unexpected parseSender result: name=%q, addr=%q", name, addr)
	}

	name2, addr2 := parseSender("bob@example.com")
	if name2 != "bob" || addr2 != "bob@example.com" {
		t.Errorf("unexpected parseSender bare email: name=%q, addr=%q", name2, addr2)
	}
}

func TestContactInitialFromSender(t *testing.T) {
	if initial := contactInitialFromSender("Alice Smith <alice@example.com>"); initial != "A" {
		t.Errorf("expected 'A', got %q", initial)
	}
	if initial := contactInitialFromSender("john@example.com"); initial != "J" {
		t.Errorf("expected 'J', got %q", initial)
	}
}

func TestMailboxHelpers(t *testing.T) {
	if title := mailboxTitle("INBOX"); title != "Inbox" {
		t.Errorf("expected 'Inbox', got %q", title)
	}
	if title := mailboxTitle("Sent"); title != "Sent" {
		t.Errorf("expected 'Sent', got %q", title)
	}

	if icon := mailboxIcon("INBOX"); icon != "📥" {
		t.Errorf("expected inbox icon, got %q", icon)
	}
	if icon := mailboxIcon("Sent"); icon != "📤" {
		t.Errorf("expected sent icon, got %q", icon)
	}
	if icon := mailboxIcon("Trash"); icon != "🗑" {
		t.Errorf("expected trash icon, got %q", icon)
	}
}

func TestMailDateFormatting(t *testing.T) {
	now := time.Now()
	formattedNow := formatMailDate(now)
	if formattedNow != now.Format("15:04") {
		t.Errorf("expected today time format %q, got %q", now.Format("15:04"), formattedNow)
	}

	pastDate := time.Date(2023, time.January, 15, 10, 0, 0, 0, time.Local)
	formattedPast := formatMailDate(pastDate)
	if formattedPast != "Jan 15, 2023" {
		t.Errorf("expected 'Jan 15, 2023', got %q", formattedPast)
	}

	detailFormatted := formatDetailDate(pastDate)
	if detailFormatted != "Jan 15, 2023, 10:00" {
		t.Errorf("expected 'Jan 15, 2023, 10:00', got %q", detailFormatted)
	}
}
