package main

import "testing"

func TestFormatEnvelopeHasAllRequiredFields(t *testing.T) {
	message := &Message{
		Sender:     "sender@example.com",
		Recipients: []string{"recipient@example.com"},
		Subject:    "A subject",
		InReplyTo:  "<parent@example.com>",
		MessageID:  "<message@example.com>",
	}

	got := formatEnvelope(message, "Thu, 11 Jul 2026 12:00:00 +0000")
	want := "(\"Thu, 11 Jul 2026 12:00:00 +0000\" \"A subject\" ((NIL NIL \"sender\" \"example.com\")) ((NIL NIL \"sender\" \"example.com\")) ((NIL NIL \"sender\" \"example.com\")) ((NIL NIL \"recipient\" \"example.com\")) NIL NIL \"<parent@example.com>\" \"<message@example.com>\")"
	if got != want {
		t.Fatalf("ENVELOPE = %q\nwant %q", got, want)
	}
}
