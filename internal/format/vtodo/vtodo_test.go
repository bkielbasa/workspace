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

func TestVTODO_UnescapingAndLineUnfoldingAndDates(t *testing.T) {
	// 1. Literal backslash unescaping and leading space preservation
	// SUMMARY: leading space and literal backslashes
	icsFolded := "BEGIN:VCALENDAR\r\n" +
		"BEGIN:VTODO\r\n" +
		"SUMMARY:  Some content with a \\\\ literal backslash and\r\n" +
		"  folded line.\r\n" +
		"CREATED:20260918T215300Z\r\n" +
		"LAST-MODIFIED:20260918T225300Z\r\n" +
		"END:VTODO\r\n" +
		"END:VCALENDAR\r\n"

	parsed, err := Parse(icsFolded)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	// Leading spaces should be preserved, and line unfolding should concatenate "folded line."
	expectedContent := "  Some content with a \\ literal backslash and folded line."
	if parsed.Content != expectedContent {
		t.Errorf("expected Content %q, got %q", expectedContent, parsed.Content)
	}

	// Dates should be parsed correctly
	expectedCreated := time.Date(2026, 9, 18, 21, 53, 0, 0, time.UTC)
	expectedModified := time.Date(2026, 9, 18, 22, 53, 0, 0, time.UTC)

	if !parsed.CreatedAt.Equal(expectedCreated) {
		t.Errorf("expected CreatedAt %v, got %v", expectedCreated, parsed.CreatedAt)
	}
	if !parsed.UpdatedAt.Equal(expectedModified) {
		t.Errorf("expected UpdatedAt %v, got %v", expectedModified, parsed.UpdatedAt)
	}
}
