package mail

import (
	"context"
	"fmt"
	"testing"
)

type dummyOutboxRepo struct {
	messages []OutboxMessage
	success  []string
	failure  []string
}

func (d *dummyOutboxRepo) Enqueue(ctx context.Context, recipient, data string) error {
	return nil
}

func (d *dummyOutboxRepo) FetchBatch(ctx context.Context, limit int) ([]OutboxMessage, error) {
	return d.messages, nil
}

func (d *dummyOutboxRepo) MarkSuccess(ctx context.Context, id string) error {
	d.success = append(d.success, id)
	return nil
}

func (d *dummyOutboxRepo) MarkFailure(ctx context.Context, id string, attempts int) error {
	d.failure = append(d.failure, id)
	return nil
}

type dummyDKIM struct{}

func (d *dummyDKIM) Sign(raw string) (string, error) {
	return "DKIM-Signature: test\r\n" + raw, nil
}

func TestExtractFrom(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"From: Alice <alice@example.com>\r\nSubject: Hi\r\n\r\nHello", "alice@example.com"},
		{"from: bob@example.com\nSubject: Hi\n\nHello", "bob@example.com"},
		{"Subject: Hi\n\nHello", ""},
	}

	for _, tt := range tests {
		got := extractFrom(tt.raw)
		if got != tt.want {
			t.Errorf("extractFrom(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestPrepareOutboundMessage(t *testing.T) {
	msg := prepareOutboundMessage("Subject: Test\r\n\r\nBody", "sender@example.com", "recipient@example.com", "mail.example.com")
	if !contains(msg, "From: sender@example.com") {
		t.Errorf("expected From header, got %s", msg)
	}
	if !contains(msg, "To: recipient@example.com") {
		t.Errorf("expected To header, got %s", msg)
	}
	if !contains(msg, "Date: ") {
		t.Errorf("expected Date header, got %s", msg)
	}
	if !contains(msg, "Message-ID: ") {
		t.Errorf("expected Message-ID header, got %s", msg)
	}
}

func contains(s, substr string) bool {
	return fmt.Sprint(s) != "" && len(s) >= len(substr) && (s == substr || len(substr) > 0 && searchSubstr(s, substr))
}

func searchSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
