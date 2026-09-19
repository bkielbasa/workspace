# Apple Notes (IMAP) & Apple Reminders (CalDAV) Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable full bidirectional synchronization between Workspace Notes & Lists and native Apple iPhone apps: text notes synchronize with the native Apple Notes app via an IMAP Notes bridge, and checklist notes synchronize with native Apple Reminders via CalDAV task collections.

**Architecture:** 
1. **Apple Reminders (CalDAV):** Correct CalDAV `calendar-home-set` discovery so `PROPFIND Depth: 1` on the user home collection discovers both default calendar events and checklist task collections (`VTODO`). Support list creation (`MKCALENDAR` / `MKCOL`), deletion, and renaming via CalDAV.
2. **Apple Notes (IMAP):** Apple Notes on iOS stores notes as RFC 822 MIME email messages with header `X-Uniform-Type-Identifier: com.apple.mail-note` in an IMAP mailbox named `Notes`. We introduce an RFC 822 Apple Note serializer/parser in `internal/format/applenote` and an `IMAPNotesBridge` in `internal/notes` that connects `notes.Service` and `mail.MessageRepository`/`mail.MailboxRepository`.
3. **Bidirectional Event Flow:** Web mutations broadcast through `notes.Broker` and are mirrored into the user's `Notes` IMAP folder. IMAP `APPEND`, `STORE \Deleted`, and `EXPUNGE` commands in the `Notes` folder trigger note creation, modification, and deletion in `notes.Service` with real-time SSE broadcasts to the web UI.

**Tech Stack:** Go 1.22+, PostgreSQL, IMAP4rev1 (RFC 3501, Apple Mail-Note MIME), CalDAV (RFC 4791, RFC 5545 iCalendar VTODO).

**Spec:** Native iOS integration for Workspace Notes & Lists.

## Global Constraints
- Target Go version: 1.22+
- All IDs use UUIDs (`github.com/google/uuid`)
- Notes privacy: Notes are strictly private to their creator user
- No data loss on round-trip between Web UI and Apple Notes / Reminders
- Strictly adhere to TDD: failing test before implementation, verifying pass, then commit

---

### Task 1: CalDAV Task Collection Discovery & Lifecycle (Apple Reminders)

**Files:**
- Modify: `internal/caldav/caldav.go:130-220, 480-570`
- Test: `internal/caldav/caldav_tasks_test.go`

**Interfaces:**
- Consumes: `notes.Service.ListNotes`, `notes.Service.CreateNote`, `notes.Service.DeleteNote`, `notes.Service.UpdateNote`
- Produces: CalDAV Depth: 1 multi-status containing list collections, `MKCALENDAR` collection creation, `DELETE` list collection, `PROPPATCH` list renaming

- [ ] **Step 1: Write failing tests in `internal/caldav/caldav_tasks_test.go`**

```go
func TestCalDAVHomeSetCollectionDiscovery(t *testing.T) {
	notesSvc := newMockNotesService()
	calSvc := newMockCalendarService()
	userID := uuid.New()
	handler := NewWithTasks(calSvc, notesSvc, &mockAuth{user: &identity.User{ID: userID, Email: "user@example.com"}})

	// Create a list note
	listNote, err := notesSvc.CreateNote(context.Background(), userID, notes.Note{
		Title: "Groceries",
		Kind:  notes.KindList,
	})
	if err != nil {
		t.Fatalf("failed to create list note: %v", err)
	}

	// 1. PROPFIND Depth: 0 on /cal/ - check calendar-home-set points to /cal/{userID}/
	req := httptest.NewRequest("PROPFIND", "/cal/", strings.NewReader(`
		<d:propfind xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">
			<d:prop><cal:calendar-home-set/></d:prop>
		</d:propfind>
	`))
	req.Header.Set("Depth", "0")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 Multi-Status, got %d", w.Code)
	}
	body := w.Body.String()
	expectedHomeSet := fmt.Sprintf("<cal:calendar-home-set><d:href>/cal/%s/</d:href></cal:calendar-home-set>", userID)
	if !strings.Contains(body, expectedHomeSet) {
		t.Fatalf("expected calendar-home-set to contain %q, got body:\n%s", expectedHomeSet, body)
	}

	// 2. PROPFIND Depth: 1 on /cal/{userID}/ - should return default calendar AND list collections
	req = httptest.NewRequest("PROPFIND", fmt.Sprintf("/cal/%s/", userID), nil)
	req.Header.Set("Depth", "1")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 Multi-Status on Depth 1, got %d", w.Code)
	}
	depthBody := w.Body.String()
	defaultCalHref := fmt.Sprintf("<d:href>/cal/%s/default/</d:href>", userID)
	listCalHref := fmt.Sprintf("<d:href>/cal/lists/%s/</d:href>", listNote.ID)
	if !strings.Contains(depthBody, defaultCalHref) {
		t.Errorf("expected Depth: 1 to contain default calendar %q", defaultCalHref)
	}
	if !strings.Contains(depthBody, listCalHref) {
		t.Errorf("expected Depth: 1 to contain list collection %q", listCalHref)
	}
	if !strings.Contains(depthBody, "<C:comp name=\"VTODO\"/>") {
		t.Errorf("expected Depth: 1 to advertise VTODO for list collection")
	}
}

func TestCalDAVMKCalendarAndListDeletion(t *testing.T) {
	notesSvc := newMockNotesService()
	calSvc := newMockCalendarService()
	userID := uuid.New()
	handler := NewWithTasks(calSvc, notesSvc, &mockAuth{user: &identity.User{ID: userID, Email: "user@example.com"}})

	newListID := uuid.New()
	mkBody := `<?xml version="1.0" encoding="utf-8" ?>
	<C:mkcalendar xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
		<D:set>
			<D:prop>
				<D:displayname>Packing List</D:displayname>
				<C:supported-calendar-component-set>
					<C:comp name="VTODO"/>
				</C:supported-calendar-component-set>
			</D:prop>
		</D:set>
	</C:mkcalendar>`

	req := httptest.NewRequest("MKCALENDAR", fmt.Sprintf("/cal/lists/%s/", newListID), strings.NewReader(mkBody))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on MKCALENDAR, got %d: %s", w.Code, w.Body.String())
	}

	// Verify note exists in service
	createdNote, err := notesSvc.GetNote(context.Background(), userID, newListID)
	if err != nil {
		t.Fatalf("expected note to be created with ID %s, got err: %v", newListID, err)
	}
	if createdNote.Title != "Packing List" {
		t.Errorf("expected title 'Packing List', got %q", createdNote.Title)
	}
	if createdNote.Kind != notes.KindList {
		t.Errorf("expected KindList, got %s", createdNote.Kind)
	}

	// Delete collection via DELETE /cal/lists/{id}/
	delReq := httptest.NewRequest("DELETE", fmt.Sprintf("/cal/lists/%s/", newListID), nil)
	delW := httptest.NewRecorder()
	handler.ServeHTTP(delW, delReq)
	if delW.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content on list DELETE, got %d", delW.Code)
	}

	_, err = notesSvc.GetNote(context.Background(), userID, newListID)
	if !errors.Is(err, notes.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after deletion, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/caldav/... -run "TestCalDAVHomeSetCollectionDiscovery|TestCalDAVMKCalendarAndListDeletion" -v`
Expected: FAIL (missing methods or home set mismatch)

- [ ] **Step 3: Update `internal/caldav/caldav.go`**

Implement:
1. In `list()`:
   - For `/cal/` and `/cal`: return `<cal:calendar-home-set><d:href>/cal/%s/</d:href></cal:calendar-home-set>`
   - For `/cal/%s/`:
     - If `Depth == "0"`: return collection properties for `/cal/%s/`
     - If `Depth == "1"`: return `/cal/%s/`, `/cal/%s/default/`, and for each `notes.KindList` note in `notes.ListNotes`, return `<d:response><d:href>/cal/lists/%s/</d:href>...<C:comp name="VTODO"/>...</d:response>`
2. In `ServeHTTP()`:
   - Handle method `MKCALENDAR` and `MKCOL`: if path matches `/cal/lists/{id}` or `/cal/lists/{id}/`, parse `<d:displayname>` from request body, create `KindList` note with ID `{id}` via `h.notes.CreateNote`, return 201 Created.
   - Handle method `DELETE` when target is a list collection (`!target.isItem && target.noteID != uuid.Nil`): call `h.notes.DeleteNote(r.Context(), userID, false, target.noteID)`, return 204 No Content.
   - Handle method `PROPPATCH` on list collection: parse `<d:displayname>`, update note title via `h.notes.UpdateNote`, return 207 Multi-Status.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/caldav/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/caldav/caldav.go internal/caldav/caldav_tasks_test.go
git commit -m "feat(caldav): support calendar-home-set depth 1 discovery and list collection lifecycle"
```

---

### Task 2: Apple Notes RFC 822 MIME Serializer & Parser

**Files:**
- Create: `internal/format/applenote/applenote.go`
- Test: `internal/format/applenote/applenote_test.go`

**Interfaces:**
- Consumes: `notes.Note`, `uuid.UUID`
- Produces: `Format(note *notes.Note, userEmail string) string`, `Parse(raw string) (*ParsedNote, error)`

- [ ] **Step 1: Write failing test in `internal/format/applenote/applenote_test.go`**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/format/applenote/...`
Expected: FAIL (package does not exist)

- [ ] **Step 3: Implement `internal/format/applenote/applenote.go`**

```go
package applenote

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

type ParsedNote struct {
	ID        uuid.UUID
	Title     string
	Body      string
	IsNote    bool
	Date      time.Time
}

func Format(note *notes.Note, userEmail string) string {
	dateStr := note.UpdatedAt.Format(time.RFC1123Z)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("From: %s\r\n", userEmail))
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", note.Title))
	sb.WriteString(fmt.Sprintf("Date: %s\r\n", dateStr))
	sb.WriteString(fmt.Sprintf("Message-ID: <%s@workspace.local>\r\n", note.ID))
	sb.WriteString("X-Uniform-Type-Identifier: com.apple.mail-note\r\n")
	sb.WriteString(fmt.Sprintf("X-Universally-Unique-Identifier: %s\r\n", note.ID))
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	sb.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	sb.WriteString(note.Body)
	return sb.String()
}

var (
	htmlDivBreakRe = regexp.MustCompile(`(?i)<(div|p|br)[^>]*>`)
	htmlTagStripRe = regexp.MustCompile(`<[^>]+>`)
)

func Parse(raw string) (*ParsedNote, error) {
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("read mime message: %w", err)
	}

	headers := msg.Header
	uniformType := headers.Get("X-Uniform-Type-Identifier")
	isNote := strings.EqualFold(uniformType, "com.apple.mail-note")

	rawUUID := headers.Get("X-Universally-Unique-Identifier")
	noteID, _ := uuid.Parse(rawUUID)

	subject := headers.Get("Subject")

	date := time.Now()
	if dateHeader := headers.Get("Date"); dateHeader != "" {
		if parsedTime, err := mail.ParseDate(dateHeader); err == nil {
			date = parsedTime
		}
	}

	buf := new(strings.Builder)
	_, _ = buf.ReadFrom(msg.Body)
	rawBody := buf.String()

	contentType := strings.ToLower(headers.Get("Content-Type"))
	body := rawBody
	if strings.Contains(contentType, "text/html") {
		// Convert HTML line breaks / divs to newlines and strip remaining tags
		withNewlines := htmlDivBreakRe.ReplaceAllString(rawBody, "\n")
		stripped := htmlTagStripRe.ReplaceAllString(withNewlines, "")
		body = strings.TrimSpace(stripped)
		// If subject was empty or default, first line can serve as title
		if subject == "" {
			lines := strings.SplitN(body, "\n", 2)
			if len(lines) > 0 {
				subject = strings.TrimSpace(lines[0])
			}
		}
		// If first line of extracted body matches the subject, remove it from body
		if lines := strings.SplitN(body, "\n", 2); len(lines) > 1 && strings.TrimSpace(lines[0]) == subject {
			body = strings.TrimSpace(lines[1])
		}
	} else {
		body = strings.TrimSpace(rawBody)
	}

	return &ParsedNote{
		ID:     noteID,
		Title:  subject,
		Body:   body,
		IsNote: isNote,
		Date:   date,
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/format/applenote/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/format/applenote/
git commit -m "feat(format): add Apple Notes RFC 822 MIME serializer and parser"
```

---

### Task 3: Notes IMAP Bridge & Provisioning

**Files:**
- Modify: `internal/mail/mailboxes.go:20-25`
- Create: `internal/notes/imap_bridge.go`
- Test: `internal/notes/imap_bridge_test.go`

**Interfaces:**
- Consumes: `notes.Service`, `mail.MessageRepository`, `mail.MailboxRepository`, `identity.User`
- Produces: `NotesBridge` interface:
  - `SyncNoteToIMAP(ctx context.Context, user *identity.User, n *notes.Note) error`
  - `DeleteNoteFromIMAP(ctx context.Context, userID, noteID uuid.UUID) error`
  - `HandleIMAPAppend(ctx context.Context, userID uuid.UUID, mailboxName, raw string) (*notes.Note, error)`
  - `HandleIMAPExpunge(ctx context.Context, userID uuid.UUID, mailboxName string, messageIDs []uuid.UUID) error`
  - `EnsureNotesMailbox(ctx context.Context, userID uuid.UUID) (*mail.Mailbox, error)`
  - `StartEventListener(ctx context.Context, userLookup func(uuid.UUID) (*identity.User, error))`

- [ ] **Step 1: Write failing test in `internal/notes/imap_bridge_test.go`**

```go
package notes

import (
	"context"
	"testing"

	"github.com/bklimczak/workspace/internal/format/applenote"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

type mockMailboxes struct {
	mailboxes map[string]*mail.Mailbox
}

func (m *mockMailboxes) CreateDefault(ctx context.Context, userID uuid.UUID) error { return nil }
func (m *mockMailboxes) List(ctx context.Context, userID uuid.UUID) ([]mail.Mailbox, error) {
	var res []mail.Mailbox
	for _, mb := range m.mailboxes {
		res = append(res, *mb)
	}
	return res, nil
}
func (m *mockMailboxes) ListWithCounts(ctx context.Context, userID uuid.UUID) ([]mail.MailboxInfo, error) { return nil, nil }
func (m *mockMailboxes) GetByName(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	mb, ok := m.mailboxes[name]
	if !ok {
		return nil, mail.ErrMailboxNotFound
	}
	return mb, nil
}
func (m *mockMailboxes) Create(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	mb := &mail.Mailbox{ID: uuid.New(), UserID: userID, Name: name, UIDValidity: 1}
	m.mailboxes[name] = mb
	return mb, nil
}

type mockMessages struct {
	messages map[uuid.UUID]*mail.Message
}

func (m *mockMessages) Append(ctx context.Context, msg *mail.Message) error {
	msg.ID = uuid.New()
	msg.UID = uint64(len(m.messages) + 1)
	m.messages[msg.ID] = msg
	return nil
}
func (m *mockMessages) Get(ctx context.Context, id uuid.UUID) (*mail.Message, error) {
	msg, ok := m.messages[id]
	if !ok {
		return nil, mail.ErrMessageNotFound
	}
	return msg, nil
}
func (m *mockMessages) GetForUser(ctx context.Context, userID, id uuid.UUID) (*mail.Message, *mail.Mailbox, error) { return nil, nil, nil }
func (m *mockMessages) List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
	var res []mail.Message
	for _, msg := range m.messages {
		if msg.MailboxID == mailboxID && !msg.Deleted {
			res = append(res, *msg)
		}
	}
	return res, nil
}
func (m *mockMessages) ListSummary(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
	return m.List(ctx, mailboxID, limit, offset)
}
func (m *mockMessages) UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error {
	if msg, ok := m.messages[id]; ok {
		msg.Deleted = deleted
	}
	return nil
}
func (m *mockMessages) Delete(ctx context.Context, id uuid.UUID) error {
	delete(m.messages, id)
	return nil
}
func (m *mockMessages) Move(ctx context.Context, id, mailboxID uuid.UUID) error { return nil }

func TestIMAPBridgeBidirectionalSync(t *testing.T) {
	notesRepo := newMockRepo()
	broker := NewBroker()
	notesSvc := NewService(notesRepo, broker)

	mboxes := &mockMailboxes{mailboxes: make(map[string]*mail.Mailbox)}
	msgs := &mockMessages{messages: make(map[uuid.UUID]*mail.Message)}

	bridge := NewIMAPBridge(notesSvc, msgs, mboxes)
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com"}
	ctx := context.Background()

	// 1. Web -> IMAP: Create note in notes.Service, sync to IMAP
	createdNote, err := notesSvc.CreateNote(ctx, user.ID, Note{
		Title: "Project Roadmap",
		Body:  "Launch in Q4",
		Kind:  KindNote,
	})
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	if err := bridge.SyncNoteToIMAP(ctx, user, createdNote); err != nil {
		t.Fatalf("failed to sync note to IMAP: %v", err)
	}

	notesBox, err := mboxes.GetByName(ctx, user.ID, "Notes")
	if err != nil {
		t.Fatalf("expected Notes mailbox to be created: %v", err)
	}
	mailboxMsgs, err := msgs.List(ctx, notesBox.ID, 100, 0)
	if err != nil || len(mailboxMsgs) != 1 {
		t.Fatalf("expected 1 message in Notes mailbox, got %d", len(mailboxMsgs))
	}
	if !strings.Contains(mailboxMsgs[0].RawMessage, createdNote.ID.String()) {
		t.Errorf("expected message to contain note UUID")
	}

	// 2. IMAP -> Service: Apple Notes app appends note to Notes mailbox
	appleNoteID := uuid.New()
	rawAppleNote := applenote.Format(&Note{
		ID:    appleNoteID,
		Title: "From iPhone",
		Body:  "Created on iOS device",
	}, user.Email)

	syncedNote, err := bridge.HandleIMAPAppend(ctx, user.ID, "Notes", rawAppleNote)
	if err != nil {
		t.Fatalf("HandleIMAPAppend failed: %v", err)
	}
	if syncedNote.Title != "From iPhone" {
		t.Errorf("expected title 'From iPhone', got %q", syncedNote.Title)
	}
	if syncedNote.Body != "Created on iOS device" {
		t.Errorf("expected body 'Created on iOS device', got %q", syncedNote.Body)
	}

	// Verify it's present in notes.Service
	dbNote, err := notesSvc.GetNote(ctx, user.ID, syncedNote.ID)
	if err != nil {
		t.Fatalf("expected note in service, got err: %v", err)
	}
	if dbNote.Title != "From iPhone" {
		t.Errorf("expected dbNote title 'From iPhone', got %q", dbNote.Title)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notes/... -run TestIMAPBridgeBidirectionalSync -v`
Expected: FAIL (NewIMAPBridge undefined)

- [ ] **Step 3: Implement `internal/notes/imap_bridge.go` and update `internal/mail/mailboxes.go`**

1. In `internal/mail/mailboxes.go`:
   Add `"Notes"` to `defaultMailboxes`:
   ```go
   var defaultMailboxes = []string{
       "INBOX", "Sent", "Drafts", "Trash", "Archive", "Spam", "All", "Important", "Notes",
   }
   ```
2. In `internal/notes/imap_bridge.go`:
   Implement `IMAPBridge`:
   - `NewIMAPBridge(notesSvc *Service, mail mail.MessageRepository, mboxes mail.MailboxRepository)`
   - `EnsureNotesMailbox(ctx, userID)`: finds or creates `"Notes"` mailbox
   - `SyncNoteToIMAP(ctx, user, note)`: finds existing message with matching `X-Universally-Unique-Identifier` or `Message-ID`, deletes old message, appends formatted RFC 822 note message
   - `DeleteNoteFromIMAP(ctx, userID, noteID)`: deletes message with matching note UUID
   - `HandleIMAPAppend(ctx, userID, mailboxName, raw)`: if mailbox is `"Notes"` (case-insensitive) or `X-Uniform-Type-Identifier: com.apple.mail-note`, parses note via `applenote.Parse`. If note exists, calls `UpdateNote`, otherwise calls `CreateNote`
   - `HandleIMAPExpunge(ctx, userID, mailboxName, messageIDs)`: deletes corresponding notes

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notes/... -run TestIMAPBridgeBidirectionalSync -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mail/mailboxes.go internal/notes/imap_bridge.go internal/notes/imap_bridge_test.go
git commit -m "feat(notes): implement bidirectional IMAP Notes bridge"
```

---

### Task 4: IMAP Server Hooking & Auto-Sync

**Files:**
- Modify: `internal/imap/server.go:80-115, 395-430, 890-920`
- Test: `internal/imap/notes_sync_test.go`

**Interfaces:**
- Consumes: `notes.NotesBridge`
- Produces: `s.SetNotesBridge(bridge NotesBridge)` on `imap.Server`, triggers `HandleIMAPAppend` on `APPEND` and `HandleIMAPExpunge` on `EXPUNGE`

- [ ] **Step 1: Write failing test in `internal/imap/notes_sync_test.go`**

```go
package imap

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/format/applenote"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

type mockNotesBridge struct {
	appendedRaw string
	expungedIDs []uuid.UUID
}

func (m *mockNotesBridge) SyncNoteToIMAP(ctx context.Context, user *identity.User, n *notes.Note) error { return nil }
func (m *mockNotesBridge) DeleteNoteFromIMAP(ctx context.Context, userID, noteID uuid.UUID) error       { return nil }
func (m *mockNotesBridge) HandleIMAPAppend(ctx context.Context, userID uuid.UUID, mailboxName, raw string) (*notes.Note, error) {
	m.appendedRaw = raw
	return &notes.Note{ID: uuid.New(), Title: "Synced"}, nil
}
func (m *mockNotesBridge) HandleIMAPExpunge(ctx context.Context, userID uuid.UUID, mailboxName string, messageIDs []uuid.UUID) error {
	m.expungedIDs = append(m.expungedIDs, messageIDs...)
	return nil
}

func TestIMAPServerNotesAppendHook(t *testing.T) {
	// Start test IMAP server with notes bridge
	testUser := &identity.User{ID: uuid.New(), Email: "alice@example.com"}
	auth := &mockAuth{user: testUser}
	mboxes := newMockMailboxStore()
	msgs := newMockMessageStore()
	_, _ = mboxes.Create(context.Background(), testUser.ID, "Notes")

	server := NewServer("127.0.0.1:0", auth, msgs, mboxes)
	bridge := &mockNotesBridge{}
	server.SetNotesBridge(bridge)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err == nil {
			server.handle(conn)
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	r := bufio.NewReader(conn)
	_, _ = r.ReadString('\n') // Greeting

	// Login
	fmt.Fprintf(conn, "A1 LOGIN alice@example.com pass\r\n")
	readUntilOK(t, r, "A1")

	// Append note
	rawNote := applenote.Format(&notes.Note{
		ID:    uuid.New(),
		Title: "iOS Test Note",
		Body:  "Note text",
	}, testUser.Email)

	fmt.Fprintf(conn, "A2 APPEND \"Notes\" (%d)\r\n", len(rawNote))
	// Wait for + Ready
	for {
		line, _ := r.ReadString('\n')
		if strings.HasPrefix(line, "+") {
			break
		}
	}
	fmt.Fprintf(conn, "%s\r\n", rawNote)
	readUntilOK(t, r, "A2")

	if !strings.Contains(bridge.appendedRaw, "iOS Test Note") {
		t.Fatalf("expected notes bridge to receive appended note, got: %q", bridge.appendedRaw)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/imap/... -run TestIMAPServerNotesAppendHook -v`
Expected: FAIL (SetNotesBridge undefined)

- [ ] **Step 3: Modify `internal/imap/server.go`**

1. Define `NotesBridge` interface:
   ```go
   type NotesBridge interface {
       HandleIMAPAppend(ctx context.Context, userID uuid.UUID, mailboxName, raw string) (*notes.Note, error)
       HandleIMAPExpunge(ctx context.Context, userID uuid.UUID, mailboxName string, messageIDs []uuid.UUID) error
   }
   ```
2. Add `notes NotesBridge` field to `Server` and method `func (s *Server) SetNotesBridge(b NotesBridge)`
3. In `APPEND` command handler:
   ```go
   if s.notes != nil && strings.EqualFold(mailbox.Name, "Notes") {
       _, _ = s.notes.HandleIMAPAppend(ctx, authed.ID, mailbox.Name, raw)
   }
   ```
4. In `EXPUNGE` / `UID EXPUNGE` command handler:
   If `strings.EqualFold(selected.Name, "Notes")` and `s.notes != nil`: call `s.notes.HandleIMAPExpunge(ctx, authed.ID, selected.Name, expungedIDs)`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/imap/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/imap/server.go internal/imap/notes_sync_test.go
git commit -m "feat(imap): hook Notes mailbox APPEND and EXPUNGE into notes bridge"
```

---

### Task 5: System Wiring & End-to-End Verification

**Files:**
- Modify: `main.go:135-170`
- Modify: `internal/notes/service.go:30-45` (ensure note ID persistence if provided)
- Test: `tests/integration/notes_ios_sync_test.go` (or `main_test.go`)

**Interfaces:**
- Consumes: `notes.NewIMAPBridge`, `imap.Server.SetNotesBridge`, `notes.Broker.Subscribe`
- Produces: Complete running Workspace with automatic real-time sync between Web UI, Apple Notes, and Apple Reminders

- [ ] **Step 1: Write failing end-to-end integration test**

Verify that:
1. When a note is created in `notes.Service` (or via web `/notes`), it is automatically synchronized to the user's IMAP `Notes` mailbox.
2. When an Apple Note is appended to IMAP `Notes`, it immediately appears in `notes.Service.ListNotes` and triggers real-time SSE broadcast.
3. When `PROPFIND Depth: 1` is sent to `/cal/{userID}/`, both default calendar events and checklist collections are returned with `VTODO` component support.

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./...`
Expected: FAIL (main.go not yet wired)

- [ ] **Step 3: Wire components in `main.go`**

1. Create `imapNotesBridge := notes.NewIMAPBridge(notesSvc, messages, mailboxes)`
2. Call `imapServer.SetNotesBridge(imapNotesBridge)` on both cleartext and TLS IMAP servers
3. Start background event listener:
   ```go
   go imapNotesBridge.StartEventListener(ctx, func(userID uuid.UUID) (*identity.User, error) {
       return users.GetByID(ctx, userID)
   })
   ```
4. Pass `notesSvc` into `caldav.NewWithTasks(calendarSvc, notesSvc, deviceAuth)`.

- [ ] **Step 4: Run all tests to verify 100% pass rate**

Run: `go test -count=1 ./...`
Expected: All packages pass with 0 errors.

- [ ] **Step 5: Commit**

```bash
git add main.go internal/notes/service.go
git commit -m "feat: wire Apple Notes IMAP bridge and CalDAV reminders sync"
```
