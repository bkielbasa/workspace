package imap

import (
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/mail"
)

// RFC 3501 quoted strings are 7-bit only. Emitting raw UTF-8 here made
// Apple Mail drop the connection mid-FETCH and reconnect forever, which
// surfaced on the phone as "the server doesn't respond".
func TestIMAPNStringIsSevenBitClean(t *testing.T) {
	cases := []string{
		"Invitation: ja do Ciebie śle",
		"Re: Bartłomiej Klimczak invite",
		"teścik raz dwa trzy",
		"emoji 🎉 subject",
	}
	for _, in := range cases {
		got := imapNString(in)
		for i := 0; i < len(got); i++ {
			if got[i] > 127 {
				t.Errorf("imapNString(%q) = %q: byte %d is 8-bit", in, got, i)
				break
			}
		}
		if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
			t.Errorf("imapNString(%q) = %q, want a quoted string", in, got)
		}
	}
}

// ASCII subjects must stay untouched: encoding everything would be a
// pointless regression for the common case.
func TestIMAPNStringLeavesASCIIAlone(t *testing.T) {
	if got, want := imapNString("Re: Figi figi"), `"Re: Figi figi"`; got != want {
		t.Errorf("imapNString = %q, want %q", got, want)
	}
	if got := imapNString(""); got != "NIL" {
		t.Errorf("empty = %q, want NIL", got)
	}
}

// Quotes and backslashes still have to be escaped, not encoded away.
func TestIMAPNStringEscapesQuotes(t *testing.T) {
	got := imapNString(`say "hi" \ bye`)
	if !strings.Contains(got, `\"hi\"`) || !strings.Contains(got, `\\`) {
		t.Errorf("imapNString = %q, want escaped quotes and backslash", got)
	}
}

// The whole ENVELOPE must be 7-bit: one 8-bit byte anywhere in it breaks
// the client's parser for the entire FETCH response.
func TestFormatEnvelopeIsSevenBitClean(t *testing.T) {
	msg := &mail.Message{
		Sender:     "contact@cloudlift.run",
		Recipients: []string{"bartłomiej@example.pl"},
		Subject:    "Re: Bartłomiej Klimczak invite",
		MessageID:  "<abc@cloudlift.run>",
		ReceivedAt: time.Now(),
	}
	got := formatEnvelope(msg, "17-Sep-2026 20:00:00 +0000")
	for i := 0; i < len(got); i++ {
		if got[i] > 127 {
			t.Fatalf("formatEnvelope = %q: byte %d is 8-bit", got, i)
		}
	}
	// The subject must still be recoverable by the client.
	if !strings.Contains(got, "=?utf-8?") && !strings.Contains(got, "=?UTF-8?") {
		t.Errorf("formatEnvelope = %q, want an RFC 2047 encoded subject", got)
	}
}

// Credentials must never reach the logs; they were being shipped verbatim
// to Loki inside the raw protocol trace.
func TestRedactCredentialsHidesSecrets(t *testing.T) {
	secret := "AGNvbnRhY3RAZXhhbXBsZS5jb20AaHVudGVyMg=="
	cases := []string{
		"A1 AUTHENTICATE PLAIN " + secret,
		"a1 authenticate plain " + secret,
		`A2 LOGIN "user@example.com" "hunter2"`,
		"A3 LOGIN user@example.com hunter2",
	}
	for _, in := range cases {
		got := redactCredentials(in)
		if strings.Contains(got, secret) || strings.Contains(got, "hunter2") {
			t.Errorf("redactCredentials(%q) = %q: secret leaked", in, got)
		}
		if !strings.Contains(strings.ToUpper(got), "AUTHENTICATE") && !strings.Contains(strings.ToUpper(got), "LOGIN") {
			t.Errorf("redactCredentials(%q) = %q: lost the command name", in, got)
		}
	}
	// A bare continuation line carrying only base64 must be hidden too.
	if got := redactCredentials(secret); strings.Contains(got, secret) {
		t.Errorf("bare credential line leaked: %q", got)
	}
	// Ordinary commands must survive untouched for debugging.
	if got := redactCredentials("DY18 UID FETCH 2:4 (UID FLAGS)"); got != "DY18 UID FETCH 2:4 (UID FLAGS)" {
		t.Errorf("redactCredentials mangled a normal command: %q", got)
	}
}
