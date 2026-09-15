package smtp

import (
	"strings"
	"testing"
)

func TestPrepareMessageKeepsHeaderBodySeparator(t *testing.T) {
	raw := "From: a@b.c\r\nTo: c@d.e\r\nSubject: Hi\r\nDate: Mon, 01 Jan 2024 00:00:00 +0000\r\nMessage-ID: <1@x>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain\r\n\r\nHello"
	got := prepareMessage(raw, "a@b.c", "c@d.e", "mail.local")
	if !strings.Contains(got, "Content-Type: text/plain\r\n\r\nHello") {
		t.Fatalf("separator eaten:\n%q", got)
	}
}

func TestPrepareMessageAddsMissingHeaders(t *testing.T) {
	got := prepareMessage("Subject: Hi\r\n\r\nBody here", "a@b.c", "c@d.e", "mail.local")
	for _, want := range []string{"From: a@b.c", "To: c@d.e", "Date: ", "Message-ID: ", "MIME-Version: 1.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "\r\n\r\nBody here") {
		t.Errorf("body separator broken:\n%q", got)
	}
}
