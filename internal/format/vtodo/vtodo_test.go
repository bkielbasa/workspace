package vtodo

import (
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

func TestVTODOFormatAndParse(t *testing.T) {
	itemID := uuid.New()
	noteID := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)

	item := notes.NoteItem{
		ID:          itemID,
		NoteID:      noteID,
		Content:     "Buy oat milk & apples",
		Completed:   true,
		CompletedAt: &now,
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now,
	}

	ics := Format(item)
	if !strings.Contains(ics, "BEGIN:VTODO") || !strings.Contains(ics, "STATUS:COMPLETED") {
		t.Fatalf("unexpected formatted VTODO:\n%s", ics)
	}

	parsed, err := Parse(ics)
	if err != nil {
		t.Fatalf("failed to parse formatted VTODO: %v", err)
	}

	if parsed.ID != item.ID {
		t.Errorf("expected ID %v, got %v", item.ID, parsed.ID)
	}
	if parsed.Content != item.Content {
		t.Errorf("expected Content %q, got %q", item.Content, parsed.Content)
	}
	if !parsed.Completed {
		t.Errorf("expected Completed = true")
	}
}

func TestVTODOFormatAndParse_EdgeCases(t *testing.T) {
	// 1. Uncompleted item with special characters in content
	itemID := uuid.New()
	noteID := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)

	item := notes.NoteItem{
		ID:        itemID,
		NoteID:    noteID,
		Content:   "Escape: \\ backslash, ; semicolon, , comma, \n newline",
		Completed: false,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
	}

	ics := Format(item)
	if !strings.Contains(ics, "STATUS:NEEDS-ACTION") {
		t.Errorf("expected STATUS:NEEDS-ACTION for incomplete item")
	}
	if strings.Contains(ics, "COMPLETED") && !strings.Contains(ics, "STATUS:NEEDS-ACTION") {
		t.Errorf("unexpected COMPLETED field or STATUS for incomplete item")
	}

	parsed, err := Parse(ics)
	if err != nil {
		t.Fatalf("failed to parse incomplete item VTODO: %v", err)
	}

	if parsed.ID != item.ID {
		t.Errorf("expected ID %v, got %v", item.ID, parsed.ID)
	}
	if parsed.Content != item.Content {
		t.Errorf("expected Content %q, got %q", item.Content, parsed.Content)
	}
	if parsed.Completed {
		t.Errorf("expected Completed = false")
	}
	if parsed.CompletedAt != nil {
		t.Errorf("expected CompletedAt to be nil, got %v", parsed.CompletedAt)
	}

	// 2. Parse fallback for missing ID
	icsMissingUID := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\nSUMMARY:No UID\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	parsedMissingUID, err := Parse(icsMissingUID)
	if err != nil {
		t.Fatalf("failed to parse missing UID VTODO: %v", err)
	}
	if parsedMissingUID.ID == uuid.Nil {
		t.Errorf("expected non-nil ID generated for missing UID")
	}
	if parsedMissingUID.Content != "No UID" {
		t.Errorf("expected content 'No UID', got %q", parsedMissingUID.Content)
	}
}
