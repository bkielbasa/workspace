package main

import "testing"

func TestSMTPHeader(t *testing.T) {
	raw := "Message-ID: <message@example.com>\r\nSubject: Hello\r\n\r\nBody"
	if got := smtpHeader(raw, "Message-ID"); got != "<message@example.com>" {
		t.Fatalf("Message-ID = %q", got)
	}
	if got := smtpHeader(raw, "Subject"); got != "Hello" {
		t.Fatalf("Subject = %q", got)
	}
}
