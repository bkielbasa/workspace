package notes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/format/applenote"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

type mockMailboxes struct {
	mu        sync.Mutex
	mailboxes map[string]*mail.Mailbox
}

func (m *mockMailboxes) CreateDefault(ctx context.Context, userID uuid.UUID) error { return nil }
func (m *mockMailboxes) List(ctx context.Context, userID uuid.UUID) ([]mail.Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []mail.Mailbox
	for _, mb := range m.mailboxes {
		res = append(res, *mb)
	}
	return res, nil
}
func (m *mockMailboxes) ListWithCounts(ctx context.Context, userID uuid.UUID) ([]mail.MailboxInfo, error) {
	return nil, nil
}
func (m *mockMailboxes) GetByName(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, mb := range m.mailboxes {
		if strings.EqualFold(k, name) {
			return mb, nil
		}
	}
	return nil, mail.ErrMailboxNotFound
}
func (m *mockMailboxes) Create(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mb := &mail.Mailbox{ID: uuid.New(), UserID: userID, Name: name, UIDValidity: 1}
	m.mailboxes[name] = mb
	return mb, nil
}

type mockMessages struct {
	mu       sync.Mutex
	messages map[uuid.UUID]*mail.Message
}

func (m *mockMessages) Append(ctx context.Context, msg *mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg.ID == uuid.Nil {
		msg.ID = uuid.New()
	}
	msg.UID = uint64(len(m.messages) + 1)
	m.messages[msg.ID] = msg
	return nil
}
func (m *mockMessages) Get(ctx context.Context, id uuid.UUID) (*mail.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg, ok := m.messages[id]
	if !ok {
		return nil, mail.ErrMessageNotFound
	}
	return msg, nil
}
func (m *mockMessages) GetForUser(ctx context.Context, userID, id uuid.UUID) (*mail.Message, *mail.Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg, ok := m.messages[id]
	if !ok {
		return nil, nil, mail.ErrMessageNotFound
	}
	return msg, nil, nil
}
func (m *mockMessages) List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg, ok := m.messages[id]; ok {
		msg.Deleted = deleted
	}
	return nil
}
func (m *mockMessages) Delete(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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

func TestIMAPBridgeUpdateExistingNote(t *testing.T) {
	notesRepo := newMockRepo()
	broker := NewBroker()
	notesSvc := NewService(notesRepo, broker)

	mboxes := &mockMailboxes{mailboxes: make(map[string]*mail.Mailbox)}
	msgs := &mockMessages{messages: make(map[uuid.UUID]*mail.Message)}

	bridge := NewIMAPBridge(notesSvc, msgs, mboxes)
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com"}
	ctx := context.Background()

	createdNote, err := notesSvc.CreateNote(ctx, user.ID, Note{
		Title: "Version 1",
		Body:  "First draft",
		Kind:  KindNote,
	})
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	if err := bridge.SyncNoteToIMAP(ctx, user, createdNote); err != nil {
		t.Fatalf("SyncNoteToIMAP failed: %v", err)
	}

	notesBox, err := bridge.EnsureNotesMailbox(ctx, user.ID)
	if err != nil {
		t.Fatalf("EnsureNotesMailbox failed: %v", err)
	}

	list1, _ := msgs.List(ctx, notesBox.ID, 100, 0)
	if len(list1) != 1 {
		t.Fatalf("expected 1 message, got %d", len(list1))
	}

	// Update note and sync again
	createdNote.Title = "Version 2"
	createdNote.Body = "Revised text"
	updatedNote, err := notesSvc.UpdateNote(ctx, user.ID, false, *createdNote)
	if err != nil {
		t.Fatalf("UpdateNote failed: %v", err)
	}

	if err := bridge.SyncNoteToIMAP(ctx, user, updatedNote); err != nil {
		t.Fatalf("SyncNoteToIMAP update failed: %v", err)
	}

	list2, _ := msgs.List(ctx, notesBox.ID, 100, 0)
	if len(list2) != 1 {
		t.Fatalf("expected 1 message after replacement, got %d", len(list2))
	}
	if !strings.Contains(list2[0].RawMessage, "Revised text") {
		t.Errorf("expected updated body in raw message, got: %s", list2[0].RawMessage)
	}
}

func TestIMAPBridgeDeleteNoteFromIMAP(t *testing.T) {
	notesRepo := newMockRepo()
	broker := NewBroker()
	notesSvc := NewService(notesRepo, broker)

	mboxes := &mockMailboxes{mailboxes: make(map[string]*mail.Mailbox)}
	msgs := &mockMessages{messages: make(map[uuid.UUID]*mail.Message)}

	bridge := NewIMAPBridge(notesSvc, msgs, mboxes)
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com"}
	ctx := context.Background()

	createdNote, err := notesSvc.CreateNote(ctx, user.ID, Note{
		Title: "Temporary Note",
		Body:  "Delete me soon",
		Kind:  KindNote,
	})
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	if err := bridge.SyncNoteToIMAP(ctx, user, createdNote); err != nil {
		t.Fatalf("SyncNoteToIMAP failed: %v", err)
	}

	notesBox, err := bridge.EnsureNotesMailbox(ctx, user.ID)
	if err != nil {
		t.Fatalf("EnsureNotesMailbox failed: %v", err)
	}

	list1, _ := msgs.List(ctx, notesBox.ID, 100, 0)
	if len(list1) != 1 {
		t.Fatalf("expected 1 message, got %d", len(list1))
	}

	if err := bridge.DeleteNoteFromIMAP(ctx, user.ID, createdNote.ID); err != nil {
		t.Fatalf("DeleteNoteFromIMAP failed: %v", err)
	}

	list2, _ := msgs.List(ctx, notesBox.ID, 100, 0)
	if len(list2) != 0 {
		t.Fatalf("expected 0 messages after delete, got %d", len(list2))
	}
}

func TestIMAPBridgeHandleIMAPAppendUpdate(t *testing.T) {
	notesRepo := newMockRepo()
	broker := NewBroker()
	notesSvc := NewService(notesRepo, broker)

	mboxes := &mockMailboxes{mailboxes: make(map[string]*mail.Mailbox)}
	msgs := &mockMessages{messages: make(map[uuid.UUID]*mail.Message)}

	bridge := NewIMAPBridge(notesSvc, msgs, mboxes)
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com"}
	ctx := context.Background()

	createdNote, err := notesSvc.CreateNote(ctx, user.ID, Note{
		Title: "Initial Title",
		Body:  "Initial Body",
		Kind:  KindNote,
	})
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	// Apple Notes client updates this note and appends to IMAP
	updatedRaw := applenote.Format(&Note{
		ID:    createdNote.ID,
		Title: "Updated from iOS",
		Body:  "Updated Body from iOS",
	}, user.Email)

	res, err := bridge.HandleIMAPAppend(ctx, user.ID, "Notes", updatedRaw)
	if err != nil {
		t.Fatalf("HandleIMAPAppend failed: %v", err)
	}
	if res.ID != createdNote.ID {
		t.Fatalf("expected ID %s, got %s", createdNote.ID, res.ID)
	}
	if res.Title != "Updated from iOS" {
		t.Fatalf("expected title 'Updated from iOS', got %q", res.Title)
	}

	dbNote, err := notesSvc.GetNote(ctx, user.ID, createdNote.ID)
	if err != nil {
		t.Fatalf("GetNote failed: %v", err)
	}
	if dbNote.Title != "Updated from iOS" || dbNote.Body != "Updated Body from iOS" {
		t.Errorf("expected updated title and body in db, got: %+v", dbNote)
	}
}

func TestIMAPBridgeHandleIMAPAppendIgnored(t *testing.T) {
	notesRepo := newMockRepo()
	broker := NewBroker()
	notesSvc := NewService(notesRepo, broker)

	mboxes := &mockMailboxes{mailboxes: make(map[string]*mail.Mailbox)}
	msgs := &mockMessages{messages: make(map[uuid.UUID]*mail.Message)}

	bridge := NewIMAPBridge(notesSvc, msgs, mboxes)
	userID := uuid.New()
	ctx := context.Background()

	// Append regular email to INBOX
	rawEmail := "From: bob@example.com\r\nSubject: Hello\r\n\r\nJust saying hi"
	res, err := bridge.HandleIMAPAppend(ctx, userID, "INBOX", rawEmail)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result for non-note append, got: %+v", res)
	}
}

func TestIMAPBridgeHandleIMAPExpunge(t *testing.T) {
	notesRepo := newMockRepo()
	broker := NewBroker()
	notesSvc := NewService(notesRepo, broker)

	mboxes := &mockMailboxes{mailboxes: make(map[string]*mail.Mailbox)}
	msgs := &mockMessages{messages: make(map[uuid.UUID]*mail.Message)}

	bridge := NewIMAPBridge(notesSvc, msgs, mboxes)
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com"}
	ctx := context.Background()

	n1, err := notesSvc.CreateNote(ctx, user.ID, Note{Title: "Note 1", Body: "B1", Kind: KindNote})
	if err != nil {
		t.Fatalf("CreateNote 1 failed: %v", err)
	}
	n2, err := notesSvc.CreateNote(ctx, user.ID, Note{Title: "Note 2", Body: "B2", Kind: KindNote})
	if err != nil {
		t.Fatalf("CreateNote 2 failed: %v", err)
	}

	_ = bridge.SyncNoteToIMAP(ctx, user, n1)
	_ = bridge.SyncNoteToIMAP(ctx, user, n2)

	notesBox, _ := bridge.EnsureNotesMailbox(ctx, user.ID)
	allMsgs, _ := msgs.List(ctx, notesBox.ID, 100, 0)
	if len(allMsgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(allMsgs))
	}

	var msg1ID uuid.UUID
	for _, m := range allMsgs {
		if strings.Contains(m.RawMessage, n1.ID.String()) {
			msg1ID = m.ID
			break
		}
	}
	if msg1ID == uuid.Nil {
		t.Fatalf("could not find message for n1")
	}

	// Expunge msg1ID from "Notes" mailbox
	err = bridge.HandleIMAPExpunge(ctx, user.ID, "Notes", []uuid.UUID{msg1ID})
	if err != nil {
		t.Fatalf("HandleIMAPExpunge failed: %v", err)
	}

	// n1 should be deleted from notesSvc
	_, err = notesSvc.GetNote(ctx, user.ID, n1.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for n1, got: %v", err)
	}

	// n2 should still exist
	dbN2, err := notesSvc.GetNote(ctx, user.ID, n2.ID)
	if err != nil || dbN2 == nil {
		t.Errorf("expected n2 to exist, got: %v", err)
	}
}

func TestIMAPBridgeStartEventListener(t *testing.T) {
	notesRepo := newMockRepo()
	broker := NewBroker()
	notesSvc := NewService(notesRepo, broker)

	mboxes := &mockMailboxes{mailboxes: make(map[string]*mail.Mailbox)}
	msgs := &mockMessages{messages: make(map[uuid.UUID]*mail.Message)}

	bridge := NewIMAPBridge(notesSvc, msgs, mboxes)
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	userLookup := func(id uuid.UUID) (*identity.User, error) {
		if id == user.ID {
			return user, nil
		}
		return nil, identity.ErrUserNotFound
	}

	go bridge.StartEventListener(ctx, userLookup)
	// Give event listener a moment to subscribe
	time.Sleep(50 * time.Millisecond)

	// 1. Create Note in notesSvc -> should auto sync to IMAP
	created, err := notesSvc.CreateNote(ctx, user.ID, Note{
		Title: "Event Test",
		Body:  "Sync automatically",
		Kind:  KindNote,
	})
	if err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}

	// Wait for event processing
	var notesBox *mail.Mailbox
	deadline := time.Now().Add(1 * time.Second)
	var mailboxMsgs []mail.Message
	for time.Now().Before(deadline) {
		notesBox, _ = mboxes.GetByName(ctx, user.ID, "Notes")
		if notesBox != nil {
			mailboxMsgs, _ = msgs.List(ctx, notesBox.ID, 10, 0)
			if len(mailboxMsgs) > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	if notesBox == nil || len(mailboxMsgs) != 1 {
		t.Fatalf("expected 1 message auto-synced to IMAP, got: %d", len(mailboxMsgs))
	}

	// 2. Update Note in notesSvc -> should auto update in IMAP
	created.Title = "Event Test Updated"
	created.Body = "Updated content"
	_, err = notesSvc.UpdateNote(ctx, user.ID, false, *created)
	if err != nil {
		t.Fatalf("UpdateNote failed: %v", err)
	}

	deadline = time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mailboxMsgs, _ = msgs.List(ctx, notesBox.ID, 10, 0)
		if len(mailboxMsgs) == 1 && strings.Contains(mailboxMsgs[0].RawMessage, "Updated content") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(mailboxMsgs[0].RawMessage, "Updated content") {
		t.Errorf("expected updated content in IMAP message, got: %s", mailboxMsgs[0].RawMessage)
	}

	// 3. Delete Note in notesSvc -> should auto delete in IMAP
	err = notesSvc.DeleteNote(ctx, user.ID, false, created.ID)
	if err != nil {
		t.Fatalf("DeleteNote failed: %v", err)
	}

	deadline = time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mailboxMsgs, _ = msgs.List(ctx, notesBox.ID, 10, 0)
		if len(mailboxMsgs) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(mailboxMsgs) != 0 {
		t.Errorf("expected 0 messages in IMAP after delete, got: %d", len(mailboxMsgs))
	}
}

func TestIMAPBridgeAppleNotesUpdateWorkflow(t *testing.T) {
	notesRepo := newMockRepo()
	broker := NewBroker()
	notesSvc := NewService(notesRepo, broker)

	mboxes := &mockMailboxes{mailboxes: make(map[string]*mail.Mailbox)}
	msgs := &mockMessages{messages: make(map[uuid.UUID]*mail.Message)}

	bridge := NewIMAPBridge(notesSvc, msgs, mboxes)
	user := &identity.User{ID: uuid.New(), Email: "alice@example.com"}
	ctx := context.Background()

	notesBox, err := bridge.EnsureNotesMailbox(ctx, user.ID)
	if err != nil {
		t.Fatalf("EnsureNotesMailbox failed: %v", err)
	}

	noteUUID := uuid.New()

	// 1. Apple Notes app appends v1 of note
	rawV1 := applenote.Format(&Note{
		ID:    noteUUID,
		Title: "Note V1",
		Body:  "First version of note",
	}, user.Email)

	syncedNote1, err := bridge.HandleIMAPAppend(ctx, user.ID, "Notes", rawV1)
	if err != nil {
		t.Fatalf("HandleIMAPAppend v1 failed: %v", err)
	}
	if syncedNote1.Title != "Note V1" {
		t.Fatalf("expected title 'Note V1', got %q", syncedNote1.Title)
	}

	// In IMAP, appending created message 1 in mailbox
	msg1 := &mail.Message{
		ID:         uuid.New(),
		MailboxID:  notesBox.ID,
		MessageID:  fmt.Sprintf("<%s@workspace.local>", noteUUID),
		Sender:     user.Email,
		Recipients: []string{user.Email},
		Subject:    "Note V1",
		RawMessage: rawV1,
	}
	if err := msgs.Append(ctx, msg1); err != nil {
		t.Fatalf("append msg1 failed: %v", err)
	}

	// 2. Apple Notes app edits note: appends v2 with SAME noteUUID
	rawV2 := applenote.Format(&Note{
		ID:    noteUUID,
		Title: "Note V2",
		Body:  "Second revised version of note",
	}, user.Email)

	syncedNote2, err := bridge.HandleIMAPAppend(ctx, user.ID, "Notes", rawV2)
	if err != nil {
		t.Fatalf("HandleIMAPAppend v2 failed: %v", err)
	}
	if syncedNote2.Title != "Note V2" {
		t.Fatalf("expected title 'Note V2', got %q", syncedNote2.Title)
	}

	// In IMAP, appending created message 2 in mailbox
	msg2 := &mail.Message{
		ID:         uuid.New(),
		MailboxID:  notesBox.ID,
		MessageID:  fmt.Sprintf("<%s@workspace.local>", noteUUID),
		Sender:     user.Email,
		Recipients: []string{user.Email},
		Subject:    "Note V2",
		RawMessage: rawV2,
	}
	if err := msgs.Append(ctx, msg2); err != nil {
		t.Fatalf("append msg2 failed: %v", err)
	}

	// 3. Apple Notes client expunges message 1
	_ = msgs.UpdateFlags(ctx, msg1.ID, true, false, false, true, false)

	err = bridge.HandleIMAPExpunge(ctx, user.ID, "Notes", []uuid.UUID{msg1.ID})
	if err != nil {
		t.Fatalf("HandleIMAPExpunge failed: %v", err)
	}

	// In IMAP server, EXPUNGE permanently removes msg1
	_ = msgs.Delete(ctx, msg1.ID)

	// CRUCIAL: Note in notesSvc must NOT be deleted! It must have V2 content!
	dbNote, err := notesSvc.GetNote(ctx, user.ID, noteUUID)
	if err != nil {
		t.Fatalf("expected note to remain intact in notesSvc, but got err: %v", err)
	}
	if dbNote.Title != "Note V2" || dbNote.Body != "Second revised version of note" {
		t.Errorf("expected note to have V2 content, got title=%q body=%q", dbNote.Title, dbNote.Body)
	}

	// 4. Finally, when message 2 is actually expunged and no other active message remains
	_ = msgs.UpdateFlags(ctx, msg2.ID, true, false, false, true, false)
	err = bridge.HandleIMAPExpunge(ctx, user.ID, "Notes", []uuid.UUID{msg2.ID})
	if err != nil {
		t.Fatalf("HandleIMAPExpunge for msg2 failed: %v", err)
	}
	_ = msgs.Delete(ctx, msg2.ID)

	// Now note should be deleted from notesSvc
	_, err = notesSvc.GetNote(ctx, user.ID, noteUUID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after final expunge, got: %v", err)
	}
}

func TestIMAPBridgeNotePrivacy(t *testing.T) {
	notesRepo := newMockRepo()
	broker := NewBroker()
	notesSvc := NewService(notesRepo, broker)

	mboxes := &mockMailboxes{mailboxes: make(map[string]*mail.Mailbox)}
	msgs := &mockMessages{messages: make(map[uuid.UUID]*mail.Message)}

	bridge := NewIMAPBridge(notesSvc, msgs, mboxes)
	userA := &identity.User{ID: uuid.New(), Email: "alice@example.com"}
	userB := &identity.User{ID: uuid.New(), Email: "bob@example.com"}
	ctx := context.Background()

	noteOfUserB := &Note{
		ID:     uuid.New(),
		UserID: userB.ID,
		Title:  "Bob's Private Note",
		Body:   "Secret",
	}

	// Attempting to sync Bob's note to Alice's IMAP mailbox must fail with ErrForbidden
	err := bridge.SyncNoteToIMAP(ctx, userA, noteOfUserB)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got: %v", err)
	}
}
