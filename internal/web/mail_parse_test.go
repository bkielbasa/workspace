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

func TestParseNestedMultipartInviteBody(t *testing.T) {
	// Apple iCloud shape: mixed > alternative > plain+html, plus calendar.
	raw := strings.Join([]string{
		`From: =?UTF-8?Q?Bart=C5=82omiej?= <noreply@email.apple.com>`,
		`Subject: =?UTF-8?Q?Test_zaproszenia_=E2=80=9D?=`,
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="outer"`,
		"",
		"--outer",
		`Content-Type: multipart/alternative; boundary="inner"`,
		"",
		"--inner",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"iCloud Calendar=C5=9Aalendars invite body",
		"--inner",
		"Content-Type: text/html; charset=UTF-8",
		"",
		"<p>html fallback</p>",
		"--inner--",
		"--outer",
		"Content-Type: text/calendar; method=REQUEST",
		"Content-Transfer-Encoding: base64",
		"",
		"QkVHSU46VkNBTEVOREFS",
		"--outer--",
		"",
	}, "\r\n")
	body, snippet := parseMailContent(raw, "")
	if !strings.Contains(body, "iCloud Calendar") || strings.Contains(body, "multipart") {
		t.Errorf("nested body not extracted: %q", body)
	}
	if !strings.Contains(snippet, "iCloud") {
		t.Errorf("nested snippet wrong: %q", snippet)
	}
}

func TestDecodeHeader(t *testing.T) {
	if got := decodeHeader("=?UTF-8?Q?Bart=C5=82omiej_Klimczak_invite?="); got != "Bartłomiej Klimczak invite" {
		t.Errorf("subject = %q", got)
	}
	// Multi-word split across folded lines decodes as one string.
	if got := decodeHeader("=?UTF-8?Q?Test_zaproszenia_=E2=80=9D?="); got != "Test zaproszenia ”" {
		t.Errorf("folded subject = %q", got)
	}
	if got := decodeHeader("plain subject"); got != "plain subject" {
		t.Errorf("plain passthrough = %q", got)
	}
	name, addr := parseSender("=?UTF-8?Q?Bart=C5=82omiej?= <noreply@email.apple.com>")
	if name != "Bartłomiej" || addr != "noreply@email.apple.com" {
		t.Errorf("sender = %q <%s>", name, addr)
	}
}

func TestParseMailHTMLNestedSanitized(t *testing.T) {
	raw := strings.Join([]string{
		"From: a@b.c",
		`Content-Type: multipart/mixed; boundary="m1"`,
		"",
		"--m1",
		`Content-Type: multipart/alternative; boundary="m2"`,
		"",
		"--m2",
		"Content-Type: text/plain",
		"",
		"plain version",
		"--m2",
		"Content-Type: text/html",
		"",
		`<p>rich <b>version</b></p><script>alert(1)</script><a href="https://example.com">link</a>`,
		"--m2--",
		"--m1--",
		"",
	}, "\r\n")
	html, _ := parseMailHTML(raw)
	if !strings.Contains(html, "rich") || !strings.Contains(html, "https://example.com") {
		t.Errorf("html lost content: %q", html)
	}
	if strings.Contains(html, "<script") || strings.Contains(html, "alert(1)") {
		t.Errorf("script not stripped: %q", html)
	}
	// Plain body still preferred for text/snippet.
	body, _ := parseMailContent(raw, "")
	if body != "plain version" {
		t.Errorf("plain body = %q", body)
	}
}

func TestParseMailHTMLAbsent(t *testing.T) {
	raw := "From: a@b.c\r\nContent-Type: text/plain\r\n\r\njust text"
	if got, css := parseMailHTML(raw); got != "" || css != "" {
		t.Errorf("expected empty, got %q / %q", got, css)
	}
	if got, css := parseMailHTML("not a message at all"); got != "" || css != "" {
		t.Errorf("expected empty for garbage, got %q / %q", got, css)
	}
}

func TestSanitizeKeepsSafeStyling(t *testing.T) {
	raw := "From: a@b.c\r\nContent-Type: text/html\r\n\r\n" +
		`<p style="color: #1d1d1f; font-size: 36px;">Big title</p>` +
		`<div style="background-image: url(https://evil.example/pixel.png)">x</div>` +
		`<script>alert(1)</script>`
	html, css := parseMailHTML(raw)
	if !strings.Contains(html, "Big title") {
		t.Errorf("content lost: %q", html)
	}
	if !strings.Contains(css, "color: #1d1d1f") || !strings.Contains(css, "font-size: 36px") {
		t.Errorf("safe styling missing from css: %q", css)
	}
	if !strings.Contains(css, ".mail-body-html .em") {
		t.Errorf("css not scoped: %q", css)
	}
	if strings.Contains(html, "<script") {
		t.Errorf("script survived: %q", html)
	}
	if strings.Contains(html+css, "url(https://evil.example") {
		t.Errorf("style url() survived: %q / %q", html, css)
	}
}

func TestScopeEmailCSS(t *testing.T) {
	html, css := parseMailHTML(
		"From: a@b.c\r\nContent-Type: text/html\r\n\r\n" +
			`<p style="color: #1d1d1f; font-size: 36px;" class="title-text">Big</p>`)
	if !strings.Contains(html, `class="em1"`) {
		t.Errorf("style not rewritten to class: %q", html)
	}
	if strings.Contains(html, "title-text") {
		t.Errorf("email class not dropped: %q", html)
	}
	if !strings.Contains(css, ".mail-body-html .em1") || !strings.Contains(css, "font-size: 36px") {
		t.Errorf("scoped css wrong: %q", css)
	}
}
