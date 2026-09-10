package web_test

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

func TestNewParsesEmbeddedWebFiles(t *testing.T) {
	files := os.DirFS("../..")
	if _, err := fs.Stat(files, "web/templates/contact-edit.html"); err != nil {
		t.Fatalf("test filesystem: %v", err)
	}

	if _, err := web.New(files, contactService{}, calendarService{}, &mailServiceStub{}, sessionService{}, userService{}, false); err != nil {
		t.Fatalf("web.New() error = %v", err)
	}
}

type spyCalendarService struct {
	calendarService
	lastPut *calendar.Event
	event   *calendar.Event
}

func (s *spyCalendarService) Get(context.Context, uuid.UUID, uuid.UUID) (*calendar.Event, error) {
	if s.event != nil {
		cp := *s.event
		return &cp, nil
	}
	return &calendar.Event{
		ID:       uuid.New(),
		Title:    "Original Title",
		StartsAt: time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC),
		EndsAt:   time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC),
	}, nil
}

func (s *spyCalendarService) Put(ctx context.Context, e calendar.Event) (*calendar.Event, error) {
	s.lastPut = &e
	return &e, nil
}

func TestCalendarsUpdateRoute(t *testing.T) {
	files := os.DirFS("../..")
	cal := &spyCalendarService{}
	eventID := uuid.New()
	userID := uuid.New()
	cal.event = &calendar.Event{
		ID:       eventID,
		UserID:   userID,
		Title:    "Standup",
		StartsAt: time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC),
		EndsAt:   time.Date(2026, 9, 8, 9, 30, 0, 0, time.UTC),
	}

	server, err := web.New(files, contactService{}, cal, &mailServiceStub{}, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// Test 1: Missing CSRF with valid session should return 403
	req := httptest.NewRequest(http.MethodPost, "/calendars/"+eventID.String(), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 for missing CSRF, got %d", rec.Code)
	}

	// Test 2: Valid update
	csrfToken := "test-csrf-token"
	form := url.Values{
		"_csrf":       {csrfToken},
		"title":       {"Updated Standup"},
		"location":    {"Room A"},
		"description": {"Daily checkin"},
		"starts_at":   {"2026-09-08T10:00"},
		"ends_at":     {"2026-09-08T11:00"},
		"week":        {"2026-09-07"},
	}
	req = httptest.NewRequest(http.MethodPost, "/calendars/"+eventID.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected status 303 See Other, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/calendars?week=2026-09-07" {
		t.Fatalf("expected redirect to /calendars?week=2026-09-07, got %q", loc)
	}
	if cal.lastPut == nil {
		t.Fatal("expected calendar.Put to be called")
	}
	if cal.lastPut.Title != "Updated Standup" {
		t.Errorf("expected Title %q, got %q", "Updated Standup", cal.lastPut.Title)
	}
	if cal.lastPut.Location != "Room A" {
		t.Errorf("expected Location %q, got %q", "Room A", cal.lastPut.Location)
	}
	if cal.lastPut.Description != "Daily checkin" {
		t.Errorf("expected Description %q, got %q", "Daily checkin", cal.lastPut.Description)
	}
	if cal.lastPut.StartsAt.In(time.Local).Format("2006-01-02T15:04") != "2026-09-08T10:00" {
		t.Errorf("unexpected StartsAt: %v", cal.lastPut.StartsAt)
	}
	if cal.lastPut.EndsAt.In(time.Local).Format("2006-01-02T15:04") != "2026-09-08T11:00" {
		t.Errorf("unexpected EndsAt: %v", cal.lastPut.EndsAt)
	}

	// Test 3: Validation failure (end before start)
	badForm := url.Values{
		"_csrf":     {csrfToken},
		"title":     {"Invalid Times"},
		"starts_at": {"2026-09-08T12:00"},
		"ends_at":   {"2026-09-08T11:00"},
		"week":      {"2026-09-07"},
	}
	req = httptest.NewRequest(http.MethodPost, "/calendars/"+eventID.String(), strings.NewReader(badForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK for rejected edit, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "The event must end after it starts.") {
		t.Error("expected error message in response body")
	}
	if !strings.Contains(rec.Body.String(), `action="/calendars/`+eventID.String()+`"`) {
		t.Error("expected form action pointing to edit route")
	}

	// Test 4: Reschedule via drag & drop (only starts_at & ends_at provided)
	rescheduleForm := url.Values{
		"_csrf":     {csrfToken},
		"starts_at": {"2026-09-09T14:00"},
		"ends_at":   {"2026-09-09T14:30"},
		"week":      {"2026-09-07"},
	}
	req = httptest.NewRequest(http.MethodPost, "/calendars/"+eventID.String(), strings.NewReader(rescheduleForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected status 303 See Other for reschedule, got %d", rec.Code)
	}
	if cal.lastPut.Title != "Standup" {
		t.Errorf("expected original Title to be preserved, got %q", cal.lastPut.Title)
	}
	if cal.lastPut.StartsAt.In(time.Local).Format("2006-01-02T15:04") != "2026-09-09T14:00" {
		t.Errorf("unexpected StartsAt: %v", cal.lastPut.StartsAt)
	}
	if cal.lastPut.EndsAt.In(time.Local).Format("2006-01-02T15:04") != "2026-09-09T14:30" {
		t.Errorf("unexpected EndsAt: %v", cal.lastPut.EndsAt)
	}
}

type testSessionService struct {
	userID uuid.UUID
}

func (s testSessionService) Create(context.Context, uuid.UUID, time.Duration) (*identity.Session, error) {
	return &identity.Session{UserID: s.userID, Token: "valid-session"}, nil
}

func (s testSessionService) GetByToken(context.Context, string) (*identity.Session, error) {
	return &identity.Session{UserID: s.userID, Token: "valid-session"}, nil
}

func (s testSessionService) Delete(context.Context, string) error {
	return nil
}

type testUserService struct {
	userID uuid.UUID
}

func (u testUserService) Authenticate(context.Context, string, string) (*identity.User, error) {
	return &identity.User{ID: u.userID, Email: "alice@example.com", Enabled: true}, nil
}

func (u testUserService) Get(context.Context, uuid.UUID) (*identity.User, error) {
	return &identity.User{ID: u.userID, Email: "alice@example.com", Enabled: true}, nil
}

type contactService struct{}

func (contactService) List(context.Context, uuid.UUID) ([]contacts.Contact, error) {
	return nil, nil
}

func (contactService) Get(context.Context, uuid.UUID, uuid.UUID) (contacts.Contact, error) {
	return contacts.Contact{}, nil
}

func (contactService) PutStructured(context.Context, uuid.UUID, *uuid.UUID, contacts.Contact) (*contacts.Contact, error) {
	return &contacts.Contact{}, nil
}

func (contactService) DeleteByID(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

type calendarService struct{}

func (calendarService) Get(context.Context, uuid.UUID, uuid.UUID) (*calendar.Event, error) {
	return &calendar.Event{}, nil
}

func (calendarService) List(context.Context, uuid.UUID) ([]calendar.Event, error) {
	return nil, nil
}

func (calendarService) Put(context.Context, calendar.Event) (*calendar.Event, error) {
	return &calendar.Event{}, nil
}

func (calendarService) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

type sessionService struct{}

func (sessionService) Create(context.Context, uuid.UUID, time.Duration) (*identity.Session, error) {
	return &identity.Session{}, nil
}

func (sessionService) GetByToken(context.Context, string) (*identity.Session, error) {
	return &identity.Session{}, nil
}

func (sessionService) Delete(context.Context, string) error {
	return nil
}

type userService struct{}

func (userService) Authenticate(context.Context, string, string) (*identity.User, error) {
	return &identity.User{}, nil
}

func (userService) Get(context.Context, uuid.UUID) (*identity.User, error) {
	return &identity.User{}, nil
}

type mailServiceStub struct {
	mailboxes []mail.MailboxInfo
	messages  []mail.Message
	lastSent  *mail.Message
}

func (m *mailServiceStub) EnsureDefaultMailboxes(context.Context, uuid.UUID) error {
	return nil
}

func (m *mailServiceStub) ListMailboxes(context.Context, uuid.UUID) ([]mail.MailboxInfo, error) {
	if m.mailboxes != nil {
		return m.mailboxes, nil
	}
	return []mail.MailboxInfo{
		{ID: uuid.New(), Name: "INBOX", TotalCount: 1, UnreadCount: 1},
		{ID: uuid.New(), Name: "Sent", TotalCount: 0, UnreadCount: 0},
	}, nil
}

func (m *mailServiceStub) GetMailbox(ctx context.Context, userID uuid.UUID, name string) (*mail.Mailbox, error) {
	return &mail.Mailbox{ID: uuid.New(), UserID: userID, Name: name}, nil
}

func (m *mailServiceStub) ListMessages(ctx context.Context, mailboxID uuid.UUID, limit, offset int) ([]mail.Message, error) {
	return m.messages, nil
}

func (m *mailServiceStub) SearchMessages(ctx context.Context, userID uuid.UUID, query string) ([]mail.Message, error) {
	return m.messages, nil
}

func (m *mailServiceStub) GetMessage(ctx context.Context, userID, id uuid.UUID) (*mail.Message, *mail.Mailbox, error) {
	for _, msg := range m.messages {
		if msg.ID == id {
			return &msg, &mail.Mailbox{ID: msg.MailboxID, Name: "INBOX"}, nil
		}
	}
	return nil, nil, mail.ErrMessageNotFound
}

func (m *mailServiceStub) UpdateFlags(ctx context.Context, id uuid.UUID, seen, flagged, answered, deleted, draft bool) error {
	for i, msg := range m.messages {
		if msg.ID == id {
			m.messages[i].Seen = seen
			m.messages[i].Flagged = flagged
			m.messages[i].Answered = answered
			m.messages[i].Deleted = deleted
			m.messages[i].Draft = draft
			return nil
		}
	}
	return nil
}

func (m *mailServiceStub) DeleteMessage(ctx context.Context, userID, id uuid.UUID) error {
	var filtered []mail.Message
	for _, msg := range m.messages {
		if msg.ID != id {
			filtered = append(filtered, msg)
		}
	}
	m.messages = filtered
	return nil
}

func (m *mailServiceStub) SendMessage(ctx context.Context, user *identity.User, to, subject, body string) (*mail.Message, error) {
	msg := &mail.Message{
		ID:         uuid.New(),
		Sender:     user.Email,
		Recipients: []string{to},
		Subject:    subject,
		RawMessage: body,
		Seen:       true,
		ReceivedAt: time.Now(),
	}
	m.lastSent = msg
	return msg, nil
}

func TestMailRoutes(t *testing.T) {
	files := os.DirFS("../..")
	userID := uuid.New()
	msgID := uuid.New()

	mailSvc := &mailServiceStub{
		messages: []mail.Message{
			{
				ID:         msgID,
				MailboxID:  uuid.New(),
				Sender:     "Bob <bob@example.com>",
				Recipients: []string{"alice@example.com"},
				Subject:    "Project Update",
				RawMessage: "Content-Type: text/plain\r\n\r\nHere is the weekly update.",
				Seen:       false,
				Flagged:    false,
				ReceivedAt: time.Now(),
			},
		},
	}

	server, err := web.New(files, contactService{}, calendarService{}, mailSvc, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	// 1. GET /mail - lists inbox messages with compose button and search bar
	{
		req := httptest.NewRequest(http.MethodGet, "/mail", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /mail status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Project Update") {
			t.Errorf("mail list missing subject 'Project Update'")
		}
		if !strings.Contains(body, "Compose") {
			t.Errorf("mail list missing Compose button")
		}
		if !strings.Contains(body, "Search in mail") {
			t.Errorf("mail list missing search bar")
		}
	}

	// 2. GET /mail/message/{id} - detail view of the message and marks as read
	{
		req := httptest.NewRequest(http.MethodGet, "/mail/message/"+msgID.String(), nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET /mail/message/{id} status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Here is the weekly update.") {
			t.Errorf("mail detail missing body text")
		}
		if !strings.Contains(body, "Reply") {
			t.Errorf("mail detail missing reply button")
		}
		// Message should now be marked as seen
		if !mailSvc.messages[0].Seen {
			t.Errorf("expected message to be marked seen after opening detail view")
		}
	}

	// 3. POST /mail/send - sends email
	{
		form := url.Values{
			"to":      {"bob@example.com"},
			"subject": {"Thanks"},
			"body":    {"Looks good!"},
			"_csrf":   {"test-csrf-token"},
		}
		req := httptest.NewRequest(http.MethodPost, "/mail/send", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf-token"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST /mail/send status = %d, want %d", rec.Code, http.StatusSeeOther)
		}
		if mailSvc.lastSent == nil || mailSvc.lastSent.Subject != "Thanks" {
			t.Fatalf("expected lastSent to be recorded with subject 'Thanks'")
		}
	}

	// 4. POST /mail/message/{id}/toggle-star - stars/unstars message
	{
		form := url.Values{
			"box":   {"INBOX"},
			"_csrf": {"test-csrf-token"},
		}
		req := httptest.NewRequest(http.MethodPost, "/mail/message/"+msgID.String()+"/toggle-star", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf-token"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST toggle-star status = %d, want %d", rec.Code, http.StatusSeeOther)
		}
		if !mailSvc.messages[0].Flagged {
			t.Errorf("expected message to be starred")
		}
	}

	// 5. POST /mail/message/{id}/delete - deletes message
	{
		form := url.Values{
			"box":   {"INBOX"},
			"_csrf": {"test-csrf-token"},
		}
		req := httptest.NewRequest(http.MethodPost, "/mail/message/"+msgID.String()+"/delete", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
		req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf-token"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST delete status = %d, want %d", rec.Code, http.StatusSeeOther)
		}
		if len(mailSvc.messages) != 0 {
			t.Errorf("expected message to be deleted")
		}
	}
}
