package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/bklimczak/workspace/internal/format/applenote"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

type mockNotesBridge struct {
	mu           sync.Mutex
	appendedRaw  string
	appendedUser uuid.UUID
	appendedMbox string
	expungedIDs  []uuid.UUID
	expungedUser uuid.UUID
	expungedMbox string
}

func (m *mockNotesBridge) HandleIMAPAppend(ctx context.Context, userID uuid.UUID, mailboxName, raw string) (*notes.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appendedRaw = raw
	m.appendedUser = userID
	m.appendedMbox = mailboxName
	return &notes.Note{ID: uuid.New(), Title: "Synced"}, nil
}

func (m *mockNotesBridge) HandleIMAPExpunge(ctx context.Context, userID uuid.UUID, mailboxName string, messageIDs []uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expungedIDs = append(m.expungedIDs, messageIDs...)
	m.expungedUser = userID
	m.expungedMbox = mailboxName
	return nil
}

func (m *mockNotesBridge) getAppendedRaw() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appendedRaw
}

func (m *mockNotesBridge) getExpungedIDs() []uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]uuid.UUID, len(m.expungedIDs))
	copy(cp, m.expungedIDs)
	return cp
}

type syncTestStore struct {
	fakeStore
}

func (s *syncTestStore) UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.messages {
		if s.messages[i].ID == id {
			s.messages[i].Seen = seen
			s.messages[i].Flagged = flagged
			s.messages[i].Answered = answered
			s.messages[i].Deleted = deleted
			s.messages[i].Draft = draft
			return nil
		}
	}
	return mail.ErrMessageNotFound
}

func (s *syncTestStore) Append(ctx context.Context, msg *mail.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, *msg)
	return nil
}

func (s *syncTestStore) Delete(ctx context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.messages {
		if s.messages[i].ID == id {
			s.messages = append(s.messages[:i], s.messages[i+1:]...)
			return nil
		}
	}
	return nil
}

func readUntilOK(t *testing.T, r *bufio.Reader, tag string) []string {
	t.Helper()
	var lines []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("failed reading response for tag %s: %v", tag, err)
		}
		lines = append(lines, line)
		if strings.HasPrefix(line, tag+" OK") {
			return lines
		}
		if strings.HasPrefix(line, tag+" NO") || strings.HasPrefix(line, tag+" BAD") {
			t.Fatalf("command %s failed: %s", tag, line)
		}
	}
}

func startSyncTestServer(t *testing.T, user *identity.User, store *syncTestStore, bridge NotesBridge) (string, *Server) {
	t.Helper()
	srv := NewServer("127.0.0.1:0", fakeAuth{user: user}, store, fakeMailboxes{store: &store.fakeStore})
	if bridge != nil {
		srv.SetNotesBridge(bridge)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case srv.slots <- struct{}{}:
				go func(conn net.Conn) {
					defer func() { <-srv.slots }()
					srv.handle(conn)
				}(c)
			default:
				_, _ = c.Write([]byte("* BYE too many connections\r\n"))
				_ = c.Close()
			}
		}
	}()

	return ln.Addr().String(), srv
}

func TestIMAPServerNotesAppendHook(t *testing.T) {
	testUser := &identity.User{ID: uuid.New(), Email: "alice@example.com"}
	notesBox := mail.Mailbox{ID: uuid.New(), UserID: testUser.ID, Name: "Notes", UIDValidity: 1}
	inboxBox := mail.Mailbox{ID: uuid.New(), UserID: testUser.ID, Name: "INBOX", UIDValidity: 2}

	store := &syncTestStore{
		fakeStore: fakeStore{
			mailboxes: []mail.Mailbox{notesBox, inboxBox},
		},
	}
	bridge := &mockNotesBridge{}

	addr, _ := startSyncTestServer(t, testUser, store, bridge)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	r := bufio.NewReader(conn)
	_, _ = r.ReadString('\n') // Greeting

	// Login
	fmt.Fprintf(conn, "A1 LOGIN alice@example.com pass\r\n")
	readUntilOK(t, r, "A1")

	// 1. Append note to "Notes" mailbox
	rawNote := applenote.Format(&notes.Note{
		ID:    uuid.New(),
		Title: "iOS Test Note",
		Body:  "Note text",
	}, testUser.Email)

	fmt.Fprintf(conn, "A2 APPEND \"Notes\" (%d)\r\n", len(rawNote))
	for {
		line, _ := r.ReadString('\n')
		if strings.HasPrefix(line, "+") {
			break
		}
	}
	fmt.Fprintf(conn, "%s\r\n", rawNote)
	readUntilOK(t, r, "A2")

	if !strings.Contains(bridge.getAppendedRaw(), "iOS Test Note") {
		t.Fatalf("expected notes bridge to receive appended note, got: %q", bridge.getAppendedRaw())
	}
	if bridge.appendedUser != testUser.ID {
		t.Fatalf("expected user ID %s, got %s", testUser.ID, bridge.appendedUser)
	}
	if bridge.appendedMbox != "Notes" {
		t.Fatalf("expected mailbox Notes, got %s", bridge.appendedMbox)
	}

	// 2. Append note with com.apple.mail-note header to INBOX
	bridge.mu.Lock()
	bridge.appendedRaw = ""
	bridge.mu.Unlock()

	rawAppleNoteToInbox := applenote.Format(&notes.Note{
		ID:    uuid.New(),
		Title: "Apple Note in INBOX",
		Body:  "Should trigger hook",
	}, testUser.Email)

	fmt.Fprintf(conn, "A3 APPEND \"INBOX\" (%d)\r\n", len(rawAppleNoteToInbox))
	for {
		line, _ := r.ReadString('\n')
		if strings.HasPrefix(line, "+") {
			break
		}
	}
	fmt.Fprintf(conn, "%s\r\n", rawAppleNoteToInbox)
	readUntilOK(t, r, "A3")

	if !strings.Contains(bridge.getAppendedRaw(), "Apple Note in INBOX") {
		t.Fatalf("expected notes bridge to receive com.apple.mail-note in INBOX, got: %q", bridge.getAppendedRaw())
	}

	// 3. Append normal email without com.apple.mail-note to INBOX -> bridge should NOT be invoked
	bridge.mu.Lock()
	bridge.appendedRaw = ""
	bridge.mu.Unlock()

	normalEmail := "Subject: Plain Email\r\nFrom: bob@example.com\r\n\r\nHello world\r\n"
	fmt.Fprintf(conn, "A4 APPEND \"INBOX\" (%d)\r\n", len(normalEmail))
	for {
		line, _ := r.ReadString('\n')
		if strings.HasPrefix(line, "+") {
			break
		}
	}
	fmt.Fprintf(conn, "%s\r\n", normalEmail)
	readUntilOK(t, r, "A4")

	if bridge.getAppendedRaw() != "" {
		t.Fatalf("expected notes bridge to NOT be called for normal email, got: %q", bridge.getAppendedRaw())
	}
}

func TestIMAPServerNotesExpungeHook(t *testing.T) {
	testUser := &identity.User{ID: uuid.New(), Email: "alice@example.com"}
	notesBox := mail.Mailbox{ID: uuid.New(), UserID: testUser.ID, Name: "Notes", UIDValidity: 1}
	inboxBox := mail.Mailbox{ID: uuid.New(), UserID: testUser.ID, Name: "INBOX", UIDValidity: 2}

	noteMsg1 := mail.Message{
		ID:         uuid.New(),
		MailboxID:  notesBox.ID,
		UID:        1,
		RawMessage: "Subject: Note 1\r\nX-Uniform-Type-Identifier: com.apple.mail-note\r\n\r\nBody 1",
	}
	noteMsg2 := mail.Message{
		ID:         uuid.New(),
		MailboxID:  notesBox.ID,
		UID:        2,
		RawMessage: "Subject: Note 2\r\nX-Uniform-Type-Identifier: com.apple.mail-note\r\n\r\nBody 2",
	}
	inboxMsg := mail.Message{
		ID:         uuid.New(),
		MailboxID:  inboxBox.ID,
		UID:        1,
		RawMessage: "Subject: Regular Mail\r\n\r\nHello",
	}

	store := &syncTestStore{
		fakeStore: fakeStore{
			mailboxes: []mail.Mailbox{notesBox, inboxBox},
			messages:  []mail.Message{noteMsg1, noteMsg2, inboxMsg},
		},
	}
	bridge := &mockNotesBridge{}

	addr, _ := startSyncTestServer(t, testUser, store, bridge)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	r := bufio.NewReader(conn)
	_, _ = r.ReadString('\n') // Greeting

	// Login
	fmt.Fprintf(conn, "A1 LOGIN alice@example.com pass\r\n")
	readUntilOK(t, r, "A1")

	// Select Notes
	fmt.Fprintf(conn, "A2 SELECT \"Notes\"\r\n")
	readUntilOK(t, r, "A2")

	// Mark message 1 (\Deleted)
	fmt.Fprintf(conn, "A3 STORE 1 +FLAGS (\\Deleted)\r\n")
	readUntilOK(t, r, "A3")

	// Expunge
	fmt.Fprintf(conn, "A4 EXPUNGE\r\n")
	expLines := readUntilOK(t, r, "A4")

	hasExpungeLine := false
	for _, l := range expLines {
		if strings.Contains(l, "* 1 EXPUNGE") {
			hasExpungeLine = true
			break
		}
	}
	if !hasExpungeLine {
		t.Fatalf("expected '* 1 EXPUNGE' in response: %v", expLines)
	}

	expunged := bridge.getExpungedIDs()
	if len(expunged) != 1 || expunged[0] != noteMsg1.ID {
		t.Fatalf("expected bridge to receive expunged ID %s, got: %v", noteMsg1.ID, expunged)
	}

	// Now mark message 2 (\Deleted) and use UID EXPUNGE
	fmt.Fprintf(conn, "A5 UID STORE 2 +FLAGS (\\Deleted)\r\n")
	readUntilOK(t, r, "A5")

	fmt.Fprintf(conn, "A6 UID EXPUNGE 2\r\n")
	readUntilOK(t, r, "A6")

	expunged = bridge.getExpungedIDs()
	if len(expunged) != 2 || expunged[1] != noteMsg2.ID {
		t.Fatalf("expected bridge to receive second expunged ID %s, got: %v", noteMsg2.ID, expunged)
	}

	// Select INBOX and verify EXPUNGE in INBOX does NOT invoke bridge
	fmt.Fprintf(conn, "A7 SELECT \"INBOX\"\r\n")
	readUntilOK(t, r, "A7")

	fmt.Fprintf(conn, "A8 STORE 1 +FLAGS (\\Deleted)\r\n")
	readUntilOK(t, r, "A8")

	bridge.mu.Lock()
	bridge.expungedIDs = nil
	bridge.mu.Unlock()

	fmt.Fprintf(conn, "A9 EXPUNGE\r\n")
	readUntilOK(t, r, "A9")

	if len(bridge.getExpungedIDs()) != 0 {
		t.Fatalf("expected bridge to NOT be called for INBOX expunge, got: %v", bridge.getExpungedIDs())
	}
}

func TestIMAPServerSetNotesBridgeTLS(t *testing.T) {
	testUser := &identity.User{ID: uuid.New(), Email: "alice@example.com"}
	store := &syncTestStore{}
	tlsCfg := &tls.Config{}
	srv := NewTLSServer("127.0.0.1:0", fakeAuth{user: testUser}, store, fakeMailboxes{store: &store.fakeStore}, tlsCfg)

	bridge := &mockNotesBridge{}
	srv.SetNotesBridge(bridge)

	if srv.notes != bridge {
		t.Fatalf("expected srv.notes to be bridge, got: %v", srv.notes)
	}
}
