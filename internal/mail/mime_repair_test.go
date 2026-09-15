package mail

import (
	"strings"
	"testing"
)

// corruptAppleShape mimics a stored inbound message whose blank line between
// headers and body went missing, with a folded Content-Type like Apple sends.
func corruptAppleShape() string {
	return strings.Join([]string{
		"From: Boss <boss@example.com>",
		"To: alice@example.com",
		"Subject: Invite",
		"Content-Type: multipart/mixed;",
		"\tboundary=\"outer\"",
		"--outer",
		"Content-Type: text/plain",
		"",
		"see attached",
		"--outer",
		"Content-Type: text/calendar; method=REQUEST",
		"",
		"BEGIN:VCALENDAR",
		"UID:rep-1",
		"END:VCALENDAR",
		"--outer--",
		"",
	}, "\r\n")
}

func TestEnsureHeaderBodySeparatorRepairs(t *testing.T) {
	// Remove the blank line the join below creates after the headers.
	raw := strings.Replace(corruptAppleShape(), "boundary=\"outer\"\r\n\r\n--outer", "boundary=\"outer\"\r\n--outer", 1)
	if strings.Contains(raw, "boundary=\"outer\"\r\n\r\n--outer") {
		t.Fatal("fixture should have no blank line before the boundary")
	}
	fixed := EnsureHeaderBodySeparator(raw)
	if !strings.Contains(fixed, "boundary=\"outer\"\r\n\r\n--outer") {
		t.Fatalf("separator not restored:\n%q", fixed[:200])
	}
	// Body survives the repair.
	if !strings.Contains(fixed, "UID:rep-1") {
		t.Fatalf("body lost:\n%s", fixed)
	}
}

func TestEnsureHeaderBodySeparatorKeepsGood(t *testing.T) {
	raw := "From: a@b.c\r\nContent-Type: text/plain\r\n\r\nHello\r\n\r\nSecond para."
	if got := EnsureHeaderBodySeparator(raw); got != raw {
		t.Fatalf("good message rewritten:\n%q", got)
	}
	mime := "Content-Type: multipart/mixed; boundary=\"b1\"\r\n\r\n--b1\r\nContent-Type: text/plain\r\n\r\nhi\r\n--b1--\r\n"
	if got := EnsureHeaderBodySeparator(mime); got != mime {
		t.Fatalf("good MIME rewritten:\n%q", got)
	}
}

func TestEnsureHeaderBodySeparatorNoBoundary(t *testing.T) {
	// Single-part without a separator is ambiguous: leave it alone.
	raw := "Subject: Hi\r\nHello there"
	if got := EnsureHeaderBodySeparator(raw); got != raw {
		t.Fatalf("rewritten:\n%q", got)
	}
}

func TestPrepareOutboundMessageKeepsSeparator(t *testing.T) {
	raw := "From: a@b.c\r\nTo: c@d.e\r\nSubject: Hi\r\nContent-Type: text/plain\r\n\r\nHello"
	got := prepareOutboundMessage(raw, "a@b.c", "c@d.e", "mail.local")
	if !strings.Contains(got, "\r\n\r\nHello") {
		t.Fatalf("separator eaten:\n%q", got)
	}
}
