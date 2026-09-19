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
	if !parsed.IsNote {
		t.Errorf("expected parsed.IsNote to be true")
	}
	if parsed.Date.Format(time.RFC1123Z) != origNote.UpdatedAt.Format(time.RFC1123Z) {
		t.Errorf("expected parsed date %v, got %v", origNote.UpdatedAt, parsed.Date)
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

func TestRoundtripNonASCIIAndSpacing(t *testing.T) {
	noteID := uuid.New()
	now := time.Date(2026, 9, 19, 14, 30, 0, 0, time.UTC)
	origNote := &notes.Note{
		ID:        noteID,
		Title:     "🚀 Meeting with Client 🌲",
		Body:      "   Leading and trailing spaces are preserved.   \nNewlines are kept.\n",
		UpdatedAt: now,
	}

	raw := Format(origNote, "bob@example.com")
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if parsed.Title != origNote.Title {
		t.Errorf("expected Title %q, got %q", origNote.Title, parsed.Title)
	}
	if parsed.Body != origNote.Body {
		t.Errorf("expected Body %q, got %q", origNote.Body, parsed.Body)
	}
}

func TestParseHTMLWithEntitiesAndQuotedPrintable(t *testing.T) {
	rawHTMLNote := "From: alice@example.com\r\n" +
		"Subject: HTML Entities Note\r\n" +
		"X-Uniform-Type-Identifier: com.apple.mail-note\r\n" +
		"X-Universally-Unique-Identifier: 7b29a27c-9b8e-4a6f-b258-7558ec404eb4\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
		"<html><head><style>body { font-family: sans-serif; }</style><script>alert('hello');</script></head>" +
		"<body>HTML Entities Note<div>Jack &amp; Jill went up the hill &nbsp; to fetch some water</div>" +
		"<div>This =3D that</div>" +
		"</body></html>"

	parsed, err := Parse(rawHTMLNote)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if !parsed.IsNote {
		t.Errorf("expected parsed.IsNote to be true")
	}

	expectedBodyContains1 := "Jack & Jill went up the hill"
	expectedBodyContains2 := "to fetch some water"
	expectedBodyContains3 := "This = that"

	if !strings.Contains(parsed.Body, expectedBodyContains1) {
		t.Errorf("expected body to contain %q, but got %q", expectedBodyContains1, parsed.Body)
	}
	if !strings.Contains(parsed.Body, expectedBodyContains2) {
		t.Errorf("expected body to contain %q, but got %q", expectedBodyContains2, parsed.Body)
	}
	if !strings.Contains(parsed.Body, expectedBodyContains3) {
		t.Errorf("expected body to contain %q, but got %q", expectedBodyContains3, parsed.Body)
	}
	if strings.Contains(parsed.Body, "font-family") || strings.Contains(parsed.Body, "alert") {
		t.Errorf("expected style and script content to be stripped, but got %q", parsed.Body)
	}
}
