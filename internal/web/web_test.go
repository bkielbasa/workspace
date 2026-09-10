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
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

func TestNewParsesEmbeddedWebFiles(t *testing.T) {
	files := os.DirFS("../..")
	if _, err := fs.Stat(files, "web/templates/contact-edit.html"); err != nil {
		t.Fatalf("test filesystem: %v", err)
	}

	if _, err := web.New(files, contactService{}, calendarService{}, sessionService{}, userService{}, false); err != nil {
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

	server, err := web.New(files, contactService{}, cal, testSessionService{userID: userID}, testUserService{userID: userID}, false)
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
