package integration

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/caldav"
	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/imap"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

// --- In-memory Test Doubles ---

type memMailboxRepo struct {
	mu        sync.Mutex
	mailboxes map[string]*mail.Mailbox
}

func newMemMailboxRepo() *memMailboxRepo {
	return &memMailboxRepo{mailboxes: make(map[string]*mail.Mailbox)}
}

func (m *memMailboxRepo) Create(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mb := &mail.Mailbox{
		ID:          uuid.New(),
		UserID:      userID,
		Name:        name,
		UIDValidity: 1,
	}
	m.mailboxes[strings.ToLower(name)] = mb
	return mb, nil
}

func (m *memMailboxRepo) CreateDefault(ctx context.Context, userID uuid.UUID) error {
	for _, name := range []string{"INBOX", "Sent", "Drafts", "Trash", "Notes"} {
		if _, err := m.Create(ctx, userID, name); err != nil {
			return err
		}
	}
	return nil
}

func (m *memMailboxRepo) GetByName(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mb, ok := m.mailboxes[strings.ToLower(name)]
	if !ok {
		return nil, mail.ErrMailboxNotFound
	}
	return mb, nil
}

func (m *memMailboxRepo) List(ctx context.Context, userID uuid.UUID) ([]mail.Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []mail.Mailbox
	for _, mb := range m.mailboxes {
		if mb.UserID == userID {
			list = append(list, *mb)
		}
	}
	return list, nil
}

func (m *memMailboxRepo) ListWithCounts(ctx context.Context, userID uuid.UUID) ([]mail.MailboxInfo, error) {
	return nil, nil
}

func (m *memMailboxRepo) EnsureDefaults(ctx context.Context, userID uuid.UUID) error {
	return nil
}

type memMessageRepo struct {
	mu       sync.Mutex
	messages []*mail.Message
}

func newMemMessageRepo() *memMessageRepo {
	return &memMessageRepo{}
}

func (m *memMessageRepo) Append(ctx context.Context, msg *mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg.ID == uuid.Nil {
		msg.ID = uuid.New()
	}
	msg.UID = uint64(len(m.messages) + 1)
	cp := *msg
	m.messages = append(m.messages, &cp)
	return nil
}

func (m *memMessageRepo) Get(ctx context.Context, id uuid.UUID) (*mail.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.messages {
		if msg.ID == id {
			cp := *msg
			return &cp, nil
		}
	}
	return nil, mail.ErrMessageNotFound
}

func (m *memMessageRepo) GetForUser(ctx context.Context, userID, id uuid.UUID) (*mail.Message, *mail.Mailbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.messages {
		if msg.ID == id {
			cp := *msg
			return &cp, nil, nil
		}
	}
	return nil, nil, mail.ErrMessageNotFound
}

func (m *memMessageRepo) List(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
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

func (m *memMessageRepo) ListSummary(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []mail.Message
	for _, msg := range m.messages {
		if msg.MailboxID == mailboxID && !msg.Deleted {
			cp := *msg
			cp.RawMessage = ""
			res = append(res, cp)
		}
	}
	return res, nil
}

func (m *memMessageRepo) UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.messages {
		if msg.ID == id {
			msg.Seen = seen
			msg.Flagged = flagged
			msg.Answered = answered
			msg.Deleted = deleted
			msg.Draft = draft
			return nil
		}
	}
	return mail.ErrMessageNotFound
}

func (m *memMessageRepo) Delete(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, msg := range m.messages {
		if msg.ID == id {
			m.messages = append(m.messages[:i], m.messages[i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *memMessageRepo) Move(ctx context.Context, id, mailboxID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.messages {
		if msg.ID == id {
			msg.MailboxID = mailboxID
			return nil
		}
	}
	return mail.ErrMessageNotFound
}

func (m *memMessageRepo) Copy(ctx context.Context, id, mailboxID uuid.UUID) (*mail.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.messages {
		if msg.ID == id {
			cp := *msg
			cp.ID = uuid.New()
			cp.MailboxID = mailboxID
			cp.UID = uint64(len(m.messages) + 1)
			m.messages = append(m.messages, &cp)
			return &cp, nil
		}
	}
	return nil, mail.ErrMessageNotFound
}

type memNotesRepo struct {
	mu    sync.Mutex
	notes map[uuid.UUID]*notes.Note
	items map[uuid.UUID]*notes.NoteItem
}

func newMemNotesRepo() *memNotesRepo {
	return &memNotesRepo{
		notes: make(map[uuid.UUID]*notes.Note),
		items: make(map[uuid.UUID]*notes.NoteItem),
	}
}

func (m *memNotesRepo) CreateNote(ctx context.Context, n notes.Note) (*notes.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n.ID == uuid.Nil {
		n.ID = uuid.New()
	}
	now := time.Now().UTC()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	if n.UpdatedAt.IsZero() {
		n.UpdatedAt = now
	}
	cp := n
	m.notes[n.ID] = &cp
	return &cp, nil
}

func (m *memNotesRepo) GetNote(ctx context.Context, id uuid.UUID) (*notes.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.notes[id]
	if !ok {
		return nil, notes.ErrNotFound
	}
	cp := *n
	for _, item := range m.items {
		if item.NoteID == id {
			cp.Items = append(cp.Items, *item)
		}
	}
	return &cp, nil
}

func (m *memNotesRepo) ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]notes.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []notes.Note
	for _, n := range m.notes {
		if (n.UserID == userID || n.IsFamilyShared) && n.IsArchived == archived {
			cp := *n
			for _, item := range m.items {
				if item.NoteID == n.ID {
					cp.Items = append(cp.Items, *item)
				}
			}
			res = append(res, cp)
		}
	}
	return res, nil
}

func (m *memNotesRepo) UpdateNote(ctx context.Context, n notes.Note) (*notes.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := n
	cp.UpdatedAt = time.Now().UTC()
	m.notes[n.ID] = &cp
	return &cp, nil
}

func (m *memNotesRepo) DeleteNote(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.notes, id)
	for itemID, item := range m.items {
		if item.NoteID == id {
			delete(m.items, itemID)
		}
	}
	return nil
}

func (m *memNotesRepo) AddItem(ctx context.Context, item notes.NoteItem) (*notes.NoteItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	cp := item
	m.items[item.ID] = &cp
	return &cp, nil
}

func (m *memNotesRepo) GetItem(ctx context.Context, id uuid.UUID) (*notes.NoteItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[id]
	if !ok {
		return nil, notes.ErrNotFound
	}
	cp := *item
	return &cp, nil
}

func (m *memNotesRepo) ListItems(ctx context.Context, noteID uuid.UUID) ([]notes.NoteItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []notes.NoteItem
	for _, item := range m.items {
		if item.NoteID == noteID {
			res = append(res, *item)
		}
	}
	return res, nil
}

func (m *memNotesRepo) ToggleItem(ctx context.Context, id uuid.UUID, completed bool) (*notes.NoteItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.items[id]
	if !ok {
		return nil, notes.ErrNotFound
	}
	now := time.Now().UTC()
	item.Completed = completed
	if completed {
		item.CompletedAt = &now
	} else {
		item.CompletedAt = nil
	}
	item.UpdatedAt = now
	cp := *item
	return &cp, nil
}

func (m *memNotesRepo) UpdateItem(ctx context.Context, item notes.NoteItem) (*notes.NoteItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := item
	cp.UpdatedAt = time.Now().UTC()
	m.items[item.ID] = &cp
	return &cp, nil
}

func (m *memNotesRepo) DeleteItem(ctx context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, id)
	return nil
}

func (m *memNotesRepo) ListTags(ctx context.Context, userID uuid.UUID) ([]notes.NoteTag, error) {
	return nil, nil
}

func (m *memNotesRepo) SetNoteTags(ctx context.Context, noteID uuid.UUID, userID uuid.UUID, tags []string) error {
	return nil
}

type memCalendarRepo struct {
	mu     sync.Mutex
	events map[string]calendar.Event
}

func newMemCalendarRepo() *memCalendarRepo {
	return &memCalendarRepo{events: make(map[string]calendar.Event)}
}

func (m *memCalendarRepo) Get(ctx context.Context, userID, eventID uuid.UUID) (*calendar.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ev := range m.events {
		if ev.UserID == userID && ev.ID == eventID {
			cp := ev
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("event not found")
}

func (m *memCalendarRepo) GetByUID(ctx context.Context, userID uuid.UUID, uid string) (*calendar.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ev := range m.events {
		if ev.UserID == userID && ev.UID == uid {
			cp := ev
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("event not found")
}

func (m *memCalendarRepo) List(ctx context.Context, userID uuid.UUID) ([]calendar.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res []calendar.Event
	for _, ev := range m.events {
		if ev.UserID == userID {
			res = append(res, ev)
		}
	}
	return res, nil
}

func (m *memCalendarRepo) Put(ctx context.Context, event calendar.Event) (*calendar.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events[event.Resource] = event
	return &event, nil
}

func (m *memCalendarRepo) GetByResource(ctx context.Context, userID uuid.UUID, resource string) (*calendar.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ev, ok := m.events[resource]
	if !ok || ev.UserID != userID {
		return nil, fmt.Errorf("event not found")
	}
	return &ev, nil
}

func (m *memCalendarRepo) Delete(ctx context.Context, userID, eventID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, ev := range m.events {
		if ev.UserID == userID && ev.ID == eventID {
			delete(m.events, k)
			return nil
		}
	}
	return nil
}

func (m *memCalendarRepo) DeleteByResource(ctx context.Context, userID uuid.UUID, resource string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.events, resource)
	return nil
}

type memAuth struct {
	user *identity.User
}

func (a *memAuth) Authenticate(ctx context.Context, email, password string) (*identity.User, error) {
	if a.user != nil && a.user.Email == email {
		return a.user, nil
	}
	return nil, fmt.Errorf("invalid credentials")
}

func (a *memAuth) Get(ctx context.Context, id uuid.UUID) (*identity.User, error) {
	if a.user != nil && a.user.ID == id {
		return a.user, nil
	}
	return nil, fmt.Errorf("user not found")
}

func (a *memAuth) GetByID(ctx context.Context, id uuid.UUID) (*identity.User, error) {
	return a.Get(ctx, id)
}

// --- End-to-End Test Helpers ---

func readUntilPrefix(t *testing.T, r *bufio.Reader, prefix string) string {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("failed reading line waiting for prefix %q: %v", prefix, err)
		}
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
}

func waitForContinuation(t *testing.T, r *bufio.Reader) {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("failed reading continuation: %v", err)
		}
		if strings.HasPrefix(line, "+") {
			return
		}
		if strings.Contains(line, "BAD") || strings.Contains(line, "NO") {
			t.Fatalf("server rejected append before continuation: %s", line)
		}
	}
}

// --- End-to-End Tests ---

// 1. Note creation on web/service triggers sync to user's IMAP "Notes" mailbox.
func TestE2E_NoteCreationSyncsToIMAPMailbox(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	user := &identity.User{
		ID:       uuid.New(),
		Username: "alice",
		Email:    "alice@example.com",
		Enabled:  true,
	}
	auth := &memAuth{user: user}

	notesRepo := newMemNotesRepo()
	broker := notes.NewBroker()
	notesSvc := notes.NewService(notesRepo, broker)

	msgRepo := newMemMessageRepo()
	mboxRepo := newMemMailboxRepo()

	bridge := notes.NewIMAPBridge(notesSvc, msgRepo, mboxRepo)
	go bridge.StartEventListener(ctx, func(userID uuid.UUID) (*identity.User, error) {
		return auth.GetByID(ctx, userID)
	})
	time.Sleep(50 * time.Millisecond)

	// User creates a note via notes.Service
	created, err := notesSvc.CreateNote(ctx, user.ID, notes.Note{
		Title: "Launch Checklist",
		Body:  "Check servers\nVerify DNS",
	})
	if err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}

	// Verify note was saved with an ID
	if created.ID == uuid.Nil {
		t.Fatal("expected non-nil note ID")
	}

	// Wait for event listener goroutine to synchronize note to IMAP mailbox
	var notesMailbox *mail.Mailbox
	var messages []mail.Message
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		notesMailbox, _ = mboxRepo.GetByName(ctx, user.ID, "Notes")
		if notesMailbox != nil {
			messages, _ = msgRepo.List(ctx, notesMailbox.ID, 10, 0)
			if len(messages) > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	if notesMailbox == nil {
		t.Fatal("expected Notes mailbox to be created automatically")
	}
	if len(messages) == 0 {
		t.Fatal("expected note to be synced to IMAP Notes mailbox")
	}

	syncedMsg := messages[0]
	if !strings.Contains(syncedMsg.RawMessage, "X-Uniform-Type-Identifier: com.apple.mail-note") {
		t.Errorf("expected Apple mail-note header, got:\n%s", syncedMsg.RawMessage)
	}
	if !strings.Contains(syncedMsg.RawMessage, "Subject: Launch Checklist") {
		t.Errorf("expected Subject header with note title, got:\n%s", syncedMsg.RawMessage)
	}
	if !strings.Contains(syncedMsg.RawMessage, "Check servers") {
		t.Errorf("expected note body in message, got:\n%s", syncedMsg.RawMessage)
	}
	if !strings.Contains(syncedMsg.RawMessage, created.ID.String()) {
		t.Errorf("expected note UUID in message headers, got:\n%s", syncedMsg.RawMessage)
	}
}

// 2. Note appended via IMAP triggers note creation/update in notes.Service and SSE broadcast.
func TestE2E_IMAPAppendCreatesNoteAndSSEBroadcast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	user := &identity.User{
		ID:       uuid.New(),
		Username: "bob",
		Email:    "bob@example.com",
		Enabled:  true,
	}
	auth := &memAuth{user: user}

	notesRepo := newMemNotesRepo()
	broker := notes.NewBroker()
	notesSvc := notes.NewService(notesRepo, broker)

	msgRepo := newMemMessageRepo()
	mboxRepo := newMemMailboxRepo()
	notesMbox, err := mboxRepo.Create(ctx, user.ID, "Notes")
	if err != nil {
		t.Fatalf("Create mailbox failed: %v", err)
	}
	_ = notesMbox

	bridge := notes.NewIMAPBridge(notesSvc, msgRepo, mboxRepo)

	// Subscribe to SSE broker events before appending
	eventsCh := broker.Subscribe()

	// Start plaintext IMAP server on random port
	imapServer := imap.NewServer("127.0.0.1:0", auth, msgRepo, mboxRepo)
	imapServer.SetNotesBridge(bridge)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	defer ln.Close()

	go func() {
		_ = imapServer.Serve(ln)
	}()

	// Connect to IMAP server via TCP
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("net.Dial failed: %v", err)
	}
	defer conn.Close()

	r := bufio.NewReader(conn)
	readUntilPrefix(t, r, "* OK")

	// Login
	fmt.Fprintf(conn, "A1 LOGIN bob@example.com secret\r\n")
	readUntilPrefix(t, r, "A1 OK")

	// Prepare Apple Note RFC 822 content
	noteUUID := uuid.New()
	rawAppleNote := fmt.Sprintf("Subject: Meeting Notes from iPad\r\n"+
		"X-Uniform-Type-Identifier: com.apple.mail-note\r\n"+
		"X-Universally-Unique-Identifier: %s\r\n"+
		"Message-ID: <%s@workspace.local>\r\n"+
		"Content-Type: text/plain; charset=utf-8\r\n\r\n"+
		"Meeting Notes from iPad\r\n"+
		"Discussed roadmap and Q4 objectives.\r\n", noteUUID, noteUUID)

	// Send APPEND command
	fmt.Fprintf(conn, "A2 APPEND \"Notes\" {%d}\r\n", len(rawAppleNote))
	waitForContinuation(t, r)
	fmt.Fprintf(conn, "%s\r\n", rawAppleNote)
	readUntilPrefix(t, r, "A2 OK")

	// Verify note was created in notes.Service
	list, err := notesSvc.ListNotes(ctx, user.ID, false, "")
	if err != nil {
		t.Fatalf("ListNotes failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 note created in notes.Service, got %d", len(list))
	}
	if list[0].Title != "Meeting Notes from iPad" {
		t.Errorf("expected title 'Meeting Notes from iPad', got %q", list[0].Title)
	}
	if !strings.Contains(list[0].Body, "Discussed roadmap and Q4 objectives.") {
		t.Errorf("expected body to contain note text, got %q", list[0].Body)
	}

	// Verify SSE broadcast event was published
	select {
	case ev := <-eventsCh:
		if ev.Type != "note_created" && ev.Type != "note_updated" {
			t.Errorf("unexpected SSE event type: %q", ev.Type)
		}
		if ev.UserID != user.ID {
			t.Errorf("expected event userID %v, got %v", user.ID, ev.UserID)
		}
		if ev.NoteID != list[0].ID {
			t.Errorf("expected event noteID %v, got %v", list[0].ID, ev.NoteID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE broker event")
	}

	// Clean logout
	fmt.Fprintf(conn, "A3 LOGOUT\r\n")
	readUntilPrefix(t, r, "A3 OK")
}

// 3. Reminders/checklists appear under CalDAV /cal/{userID}/ with VTODO support.
func TestE2E_CalDAVRemindersVTODO(t *testing.T) {
	ctx := context.Background()

	user := &identity.User{
		ID:       uuid.New(),
		Username: "carol",
		Email:    "carol@example.com",
		Enabled:  true,
	}
	auth := &memAuth{user: user}

	notesRepo := newMemNotesRepo()
	broker := notes.NewBroker()
	notesSvc := notes.NewService(notesRepo, broker)

	calRepo := newMemCalendarRepo()
	calSvc := calendar.NewService(calRepo)

	// Create a calendar event in default calendar
	_, err := calRepo.Put(ctx, calendar.Event{
		UserID:   user.ID,
		Title:    "Dentist Appointment",
		Resource: "dentist.ics",
		ETag:     "\"etag-123\"",
	})
	if err != nil {
		t.Fatalf("Put event failed: %v", err)
	}

	// Create a checklist (Reminders list) in notes.Service
	listNote, err := notesSvc.CreateNote(ctx, user.ID, notes.Note{
		Title: "Groceries",
		Kind:  notes.KindList,
	})
	if err != nil {
		t.Fatalf("CreateNote list failed: %v", err)
	}

	// Add items to checklist
	_, err = notesSvc.AddItem(ctx, user.ID, listNote.ID, "Organic Apples")
	if err != nil {
		t.Fatalf("AddItem failed: %v", err)
	}
	_, err = notesSvc.AddItem(ctx, user.ID, listNote.ID, "Almond Milk")
	if err != nil {
		t.Fatalf("AddItem failed: %v", err)
	}

	// Create CalDAV handler wired with tasks
	caldavHandler := caldav.NewWithTasks(calSvc, notesSvc, auth)

	// Issue PROPFIND Depth: 1 on calendar home /cal/{userID}/
	req := httptest.NewRequest("PROPFIND", fmt.Sprintf("/cal/%s/", user.ID), nil)
	req.Header.Set("Depth", "1")
	req.SetBasicAuth("carol@example.com", "secret")
	w := httptest.NewRecorder()

	caldavHandler.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected status 207 Multi-Status, got %d. Body:\n%s", w.Code, w.Body.String())
	}

	body := w.Body.String()

	// 1. Check default calendar collection and VEVENT component
	if !strings.Contains(body, fmt.Sprintf("/cal/%s/default/", user.ID)) {
		t.Errorf("missing default calendar href in response:\n%s", body)
	}
	if !strings.Contains(body, "VEVENT") {
		t.Errorf("missing VEVENT component in default calendar:\n%s", body)
	}

	// 2. Check reminders checklist collection and VTODO component
	expectedListHref := fmt.Sprintf("/cal/lists/%s/", listNote.ID)
	if !strings.Contains(body, expectedListHref) {
		t.Errorf("missing list collection href %q in response:\n%s", expectedListHref, body)
	}
	if !strings.Contains(body, "VTODO") {
		t.Errorf("missing VTODO component set in checklist collection:\n%s", body)
	}
	if !strings.Contains(body, "Groceries") {
		t.Errorf("missing list displayName 'Groceries' in response:\n%s", body)
	}

	// 3. Issue PROPFIND Depth: 1 on checklist collection /cal/lists/{listID}/
	listReq := httptest.NewRequest("PROPFIND", fmt.Sprintf("/cal/lists/%s/", listNote.ID), nil)
	listReq.Header.Set("Depth", "1")
	listReq.SetBasicAuth("carol@example.com", "secret")
	listW := httptest.NewRecorder()

	caldavHandler.ServeHTTP(listW, listReq)

	if listW.Code != http.StatusMultiStatus {
		t.Fatalf("expected status 207 Multi-Status for list propfind, got %d. Body:\n%s", listW.Code, listW.Body.String())
	}

	listBody := listW.Body.String()
	if !strings.Contains(listBody, "Organic Apples") && !strings.Contains(listBody, ".ics") {
		t.Errorf("expected list items as .ics in response:\n%s", listBody)
	}
}
