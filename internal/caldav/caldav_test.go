package caldav

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type stubCalendar struct {
	events []calendar.Event
}

func (s stubCalendar) List(context.Context, uuid.UUID) ([]calendar.Event, error) {
	return s.events, nil
}

func (s stubCalendar) Put(_ context.Context, e calendar.Event) (*calendar.Event, error) {
	e.ETag = "new-etag"
	return &e, nil
}

func (s stubCalendar) GetByResource(_ context.Context, _ uuid.UUID, resource string) (*calendar.Event, error) {
	for _, e := range s.events {
		if e.Href() == resource {
			cp := e
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

func (s stubCalendar) DeleteByResource(context.Context, uuid.UUID, string) error { return nil }

type stubAuth struct{ userID uuid.UUID }

func (s stubAuth) Authenticate(context.Context, string, string) (*identity.User, error) {
	return &identity.User{ID: s.userID, Email: "u@example.com"}, nil
}

func doREPORT(t *testing.T, h http.Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("REPORT", target, strings.NewReader(body))
	req.SetBasicAuth("u@example.com", "secret")
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func testEvents(userID uuid.UUID) []calendar.Event {
	return []calendar.Event{
		{ID: uuid.New(), UserID: userID, Title: "Standup", Resource: "ev1", UID: "uid-1", ETag: "etag-1",
			ICS: "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:uid-1\r\nSUMMARY:Standup\r\nEND:VEVENT\r\nEND:VCALENDAR"},
		{ID: uuid.New(), UserID: userID, Title: "Lunch", Resource: "ev2", ETag: "etag-2"},
	}
}

func TestCalendarMultiget(t *testing.T) {
	userID := uuid.New()
	h := New(stubCalendar{events: testEvents(userID)}, stubAuth{userID: userID})

	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<C:calendar-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop><D:getetag/><C:calendar-data/></D:prop>
  <D:href>/cal/%s/default/ev1.ics</D:href>
  <D:href>https://dav.example.com/cal/%s/default/missing.ics</D:href>
</C:calendar-multiget>`, userID, userID)

	rec := doREPORT(t, h, "/cal/"+userID.String()+"/default/", body)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", rec.Code)
	}
	resp := rec.Body.String()
	if !strings.Contains(resp, "SUMMARY:Standup") {
		t.Errorf("multiget missing raw ICS data:\n%s", resp)
	}
	if !strings.Contains(resp, `<d:getetag>"etag-1"</d:getetag>`) {
		t.Errorf("multiget getetag not quoted:\n%s", resp)
	}
	if !strings.Contains(resp, "404 Not Found") {
		t.Errorf("multiget missing 404 for unknown href:\n%s", resp)
	}
}

func TestCalendarSyncCollection(t *testing.T) {
	userID := uuid.New()
	h := New(stubCalendar{events: testEvents(userID)}, stubAuth{userID: userID})

	body := `<?xml version="1.0" encoding="utf-8"?>
<D:sync-collection xmlns:D="DAV:">
  <D:sync-token/>
  <D:sync-level>1</D:sync-level>
  <D:prop><D:getetag/></D:prop>
</D:sync-collection>`

	rec := doREPORT(t, h, "/cal/"+userID.String()+"/default/", body)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", rec.Code)
	}
	resp := rec.Body.String()
	// ev1 serves stored ICS, ev2 (web-created, no ICS) is rebuilt — both
	// must carry VEVENT data for the phone to import them.
	if strings.Count(resp, "BEGIN:VEVENT") != 2 {
		t.Errorf("sync-collection should carry 2 events:\n%s", resp)
	}
	if !strings.Contains(resp, "UID:uid-1") {
		t.Errorf("sync-collection must preserve client UID:\n%s", resp)
	}
	if !strings.Contains(resp, "<d:sync-token>") {
		t.Errorf("sync-collection missing sync-token:\n%s", resp)
	}
}

func TestCalendarQueryReturnsAll(t *testing.T) {
	userID := uuid.New()
	h := New(stubCalendar{events: testEvents(userID)}, stubAuth{userID: userID})

	body := `<?xml version="1.0" encoding="utf-8"?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop><D:getetag/><C:calendar-data/></D:prop>
  <C:filter><C:comp-filter name="VCALENDAR"/></C:filter>
</C:calendar-query>`

	rec := doREPORT(t, h, "/cal/"+userID.String()+"/default/", body)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", rec.Code)
	}
	if strings.Count(rec.Body.String(), "BEGIN:VEVENT") != 2 {
		t.Errorf("calendar-query should return all events:\n%s", rec.Body.String())
	}
}
