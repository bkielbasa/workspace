package imap

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/google/uuid"
)

type fakeAuth struct{ user *identity.User }

func (f fakeAuth) Authenticate(_ context.Context, email, _ string) (*identity.User, error) {
	if f.user == nil || f.user.Email != email {
		return nil, errors.New("bad credentials")
	}
	return f.user, nil
}

type fakeStore struct {
	mu        sync.Mutex
	mailboxes []mail.Mailbox
	messages  []mail.Message
	listCalls int32
	sumCalls  int32
	delay     time.Duration
}

func (f *fakeStore) wait(ctx context.Context) error {
	if f.delay == 0 {
		return nil
	}
	select {
	case <-time.After(f.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeStore) List(ctx context.Context, mailboxID uuid.UUID, _, _ int) ([]mail.Message, error) {
	atomic.AddInt32(&f.listCalls, 1)
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	return f.forMailbox(mailboxID), nil
}

func (f *fakeStore) ListSummary(ctx context.Context, mailboxID uuid.UUID, _, _ int) ([]mail.Message, error) {
	atomic.AddInt32(&f.sumCalls, 1)
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	out := f.forMailbox(mailboxID)
	for i := range out {
		out[i].RawMessage = ""
	}
	return out, nil
}

func (f *fakeStore) forMailbox(id uuid.UUID) []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []mail.Message
	for _, m := range f.messages {
		if m.MailboxID == id {
			out = append(out, m)
		}
	}
	return out
}

func (f *fakeStore) Get(ctx context.Context, id uuid.UUID) (*mail.Message, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.messages {
		if f.messages[i].ID == id {
			m := f.messages[i]
			return &m, nil
		}
	}
	return nil, mail.ErrMessageNotFound
}

func (f *fakeStore) Append(context.Context, *mail.Message) error { return nil }
func (f *fakeStore) UpdateFlags(context.Context, uuid.UUID, bool, bool, bool, bool, bool) error {
	return nil
}
func (f *fakeStore) Delete(context.Context, uuid.UUID) error          { return nil }
func (f *fakeStore) Move(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeStore) Copy(context.Context, uuid.UUID, uuid.UUID) (*mail.Message, error) {
	return nil, nil
}

func (f *fakeStore) ListMailboxes(context.Context, uuid.UUID) ([]mail.Mailbox, error) {
	return f.mailboxes, nil
}

type fakeMailboxes struct{ store *fakeStore }

func (f fakeMailboxes) List(_ context.Context, _ uuid.UUID) ([]mail.Mailbox, error) {
	return f.store.mailboxes, nil
}

func (f fakeMailboxes) GetByName(_ context.Context, _ uuid.UUID, name string) (*mail.Mailbox, error) {
	for i := range f.store.mailboxes {
		if f.store.mailboxes[i].Name == name {
			mb := f.store.mailboxes[i]
			return &mb, nil
		}
	}
	return nil, errors.New("no such mailbox")
}

func (f fakeMailboxes) Create(_ context.Context, _ uuid.UUID, _ string) (*mail.Mailbox, error) {
	return nil, errors.New("not supported")
}
func (f fakeMailboxes) EnsureDefaults(context.Context, uuid.UUID) error { return nil }

// startTestServer runs a plaintext IMAP server on a random port.
func startTestServer(t *testing.T, store *fakeStore) (string, *Server) {
	t.Helper()
	user := &identity.User{ID: uuid.New(), Email: "contact@cloudlift.run", Enabled: true}
	srv := NewServer("127.0.0.1:0", fakeAuth{user: user}, store, fakeMailboxes{store: store})

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

type session struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dial(t *testing.T, addr string) *session {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	s := &session{t: t, conn: c, r: bufio.NewReader(c)}
	s.readLine() // greeting
	return s
}

func (s *session) readLine() string {
	s.t.Helper()
	_ = s.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := s.r.ReadString('\n')
	if err != nil {
		s.t.Fatalf("read: %v", err)
	}
	return line
}

// do sends a command and collects lines until the tagged reply.
func (s *session) do(tag, cmd string) []string {
	s.t.Helper()
	if _, err := fmt.Fprintf(s.conn, "%s %s\r\n", tag, cmd); err != nil {
		s.t.Fatalf("write: %v", err)
	}
	var out []string
	for {
		line := s.readLine()
		out = append(out, line)
		if strings.HasPrefix(line, tag+" ") {
			return out
		}
	}
}

func (s *session) login() {
	s.t.Helper()
	cred := base64.StdEncoding.EncodeToString([]byte("\x00contact@cloudlift.run\x00pw"))
	resp := s.do("A1", "AUTHENTICATE PLAIN "+cred)
	if !strings.Contains(resp[len(resp)-1], "OK") {
		s.t.Fatalf("auth failed: %q", resp)
	}
}

func polishStore() *fakeStore {
	mb := mail.Mailbox{ID: uuid.New(), Name: "Sent", UIDValidity: 1}
	raw := "Subject: test\r\nFrom: contact@cloudlift.run\r\n\r\nbody\r\n"
	return &fakeStore{
		mailboxes: []mail.Mailbox{mb},
		messages: []mail.Message{{
			ID: uuid.New(), MailboxID: mb.ID, UID: 2,
			Sender:     "contact@cloudlift.run",
			Recipients: []string{"someone@example.pl"},
			// The subjects that wedged Apple Mail in production.
			Subject:    "Re: Bartłomiej Klimczak invite",
			RawMessage: raw,
			ReceivedAt: time.Now(),
		}},
	}
}

// The regression that made iPhone Mail loop: every byte the server writes
// must be 7-bit, because 8-bit is only legal inside literals.
func TestFetchResponseIsSevenBitClean(t *testing.T) {
	addr, _ := startTestServer(t, polishStore())
	s := dial(t, addr)
	s.login()
	s.do("A2", `SELECT "Sent"`)
	lines := s.do("A3", "UID FETCH 2:2 (UID INTERNALDATE RFC822.SIZE FLAGS BODY.PEEK[HEADER])")

	joined := strings.Join(lines, "")
	if !strings.Contains(joined, "A3 OK") {
		t.Fatalf("FETCH did not complete: %q", joined)
	}
	// Only the BODY literal may carry 8-bit; this fixture's body is ASCII,
	// so the entire response must be clean.
	for i := 0; i < len(joined); i++ {
		if joined[i] > 127 {
			t.Fatalf("8-bit byte at %d in FETCH response: %q", i, joined)
		}
	}
	if !strings.Contains(strings.ToLower(joined), "=?utf-8?") {
		t.Errorf("expected an encoded subject in ENVELOPE, got %q", joined)
	}
}

// SELECT must not drag message bodies out of the database.
func TestSelectUsesSummaryNotBodies(t *testing.T) {
	store := polishStore()
	addr, _ := startTestServer(t, store)
	s := dial(t, addr)
	s.login()
	resp := s.do("A2", `SELECT "Sent"`)
	if !strings.Contains(resp[len(resp)-1], "OK") {
		t.Fatalf("SELECT failed: %q", resp)
	}
	if got := atomic.LoadInt32(&store.listCalls); got != 0 {
		t.Errorf("SELECT made %d body-loading List calls, want 0", got)
	}
	if got := atomic.LoadInt32(&store.sumCalls); got == 0 {
		t.Error("SELECT never used ListSummary")
	}
}

// An empty mailbox returns "* SEARCH" with no trailing space.
func TestUIDSearchEmptyMailbox(t *testing.T) {
	store := &fakeStore{mailboxes: []mail.Mailbox{{ID: uuid.New(), Name: "INBOX", UIDValidity: 1}}}
	addr, _ := startTestServer(t, store)
	s := dial(t, addr)
	s.login()
	s.do("A2", `SELECT "INBOX"`)
	lines := s.do("A3", "UID SEARCH UID 1:*")
	for _, l := range lines {
		if strings.HasPrefix(l, "* SEARCH") && l != "* SEARCH\r\n" {
			t.Errorf("empty SEARCH = %q, want %q", l, "* SEARCH\r\n")
		}
	}
}

// A stalled store must surface as a reply, not as silence: the bounded
// wrapper is what stops "the server doesn't respond".
func TestBoundedStoreDeadlineIsApplied(t *testing.T) {
	inner := &fakeStore{delay: 2 * time.Second}
	bounded := newBoundedMessages(inner, 50*time.Millisecond)

	start := time.Now()
	_, err := bounded.ListSummary(context.Background(), uuid.New(), 10, 0)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a deadline error from the bounded store")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Errorf("call took %v, deadline was not enforced", elapsed)
	}
}

// Beyond the cap the server says BYE instead of spawning without limit.
func TestConnectionCapRefusesPolitely(t *testing.T) {
	store := polishStore()
	user := &identity.User{ID: uuid.New(), Email: "contact@cloudlift.run", Enabled: true}
	srv := NewServer("127.0.0.1:0", fakeAuth{user: user}, store, fakeMailboxes{store: store})
	// Shrink the cap to make the boundary observable.
	srv.slots = make(chan struct{}, 1)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
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

	first, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	br := bufio.NewReader(first)
	_ = first.SetReadDeadline(time.Now().Add(5 * time.Second))
	if line, _ := br.ReadString('\n'); !strings.Contains(line, "OK") {
		t.Fatalf("first connection greeting = %q", line)
	}

	second, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, _ := bufio.NewReader(second).ReadString('\n')
	if !strings.Contains(line, "BYE") {
		t.Errorf("over-cap connection = %q, want a BYE", line)
	}
}

// IDLE must answer "+ idling" immediately, keep the connection alive
// across several wake intervals without polling the database each time,
// and complete cleanly on DONE. The wake/poll split is easy to get wrong:
// too eager and it hammers the store, too lazy and DONE hangs.
func TestIdleWakesWithoutHammeringTheStore(t *testing.T) {
	store := polishStore()
	addr, _ := startTestServer(t, store)
	s := dial(t, addr)
	s.login()
	s.do("A2", `SELECT "Sent"`)
	before := atomic.LoadInt32(&store.sumCalls)

	if _, err := fmt.Fprintf(s.conn, "A3 IDLE\r\n"); err != nil {
		t.Fatal(err)
	}
	if line := s.readLine(); !strings.HasPrefix(line, "+ ") {
		t.Fatalf("IDLE continuation = %q, want a '+' response", line)
	}

	// Sit idle across several wake intervals. Polling is on a much longer
	// timer, so the store must stay untouched here.
	time.Sleep(3 * idleWakeInterval)
	if got := atomic.LoadInt32(&store.sumCalls) - before; got != 0 {
		t.Errorf("IDLE made %d store calls in %v, want 0", got, 3*idleWakeInterval)
	}

	if _, err := fmt.Fprintf(s.conn, "DONE\r\n"); err != nil {
		t.Fatal(err)
	}
	line := s.readLine()
	if !strings.Contains(line, "A3 OK") {
		t.Fatalf("DONE reply = %q, want A3 OK", line)
	}

	// The session must still be usable after idling.
	resp := s.do("A4", "CAPABILITY")
	if !strings.Contains(resp[len(resp)-1], "OK") {
		t.Errorf("session broken after IDLE: %q", resp)
	}
}
