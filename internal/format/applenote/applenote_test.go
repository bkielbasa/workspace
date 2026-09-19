package applenote

import (
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

func TestFormatAndParseAppleNote(t *testing.T) {
	noteID := uuid.New()
	now := time.Date(2026, 9, 19, 14, 30, 0, 0, time.UTC)
	origNote := &notes.Note{
		ID:        noteID,
		Title:     "Meeting Notes",
		Body:      "Line 1: Discussion\nLine 2: Action items",
		UpdatedAt: now,
	}

	raw := Format(origNote, "alice@example.com")

	if !strings.Contains(raw, "X-Uniform-Type-Identifier: com.apple.mail-note") {
		t.Errorf("missing X-Uniform-Type-Identifier header")
	}
	if !strings.Contains(raw, "X-Universally-Unique-Identifier: "+noteID.String()) {
		t.Errorf("missing X-Universally-Unique-Identifier header")
	}
	if !strings.Contains(raw, "Subject: Meeting Notes") {
		t.Errorf("missing Subject header")
	}
	if !strings.Contains(raw, "Line 1: Discussion\nLine 2: Action items") {
		t.Errorf("missing body text")
	}

	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if parsed.ID != noteID {
		t.Errorf("expected parsed ID %s, got %s", noteID, parsed.ID)
	}
	if parsed.Title != "Meeting Notes" {
		t.Errorf("expected parsed Title 'Meeting Notes', got %q", parsed.Title)
	}
	if parsed.Body != "Line 1: Discussion\nLine 2: Action items" {
		t.Errorf("expected parsed Body, got %q", parsed.Body)
	}
}

func TestParseAppleNoteHTMLBody(t *testing.T) {
	rawHTMLNote := "From: alice@example.com\r\n" +
		"Subject: HTML Note Title\r\n" +
		"X-Uniform-Type-Identifier: com.apple.mail-note\r\n" +
		"X-Universally-Unique-Identifier: 7b29a27c-9b8e-4a6f-b258-7558ec404eb4\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		"<html><body>HTML Note Title<div>First bullet</div><div>Second bullet</div></body></html>"

	parsed, err := Parse(rawHTMLNote)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	expectedID, _ := uuid.Parse("7b29a27c-9b8e-4a6f-b258-7558ec404eb4")
	if parsed.ID != expectedID {
		t.Errorf("expected ID %s, got %s", expectedID, parsed.ID)
	}
	if parsed.Title != "HTML Note Title" {
		t.Errorf("expected Title 'HTML Note Title', got %q", parsed.Title)
	}
	if !strings.Contains(parsed.Body, "First bullet") || !strings.Contains(parsed.Body, "Second bullet") {
		t.Errorf("expected plain text extracted from HTML divs, got: %q", parsed.Body)
	}
}
