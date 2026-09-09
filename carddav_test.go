package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Clients start at the collection root and follow current-user-principal from
// there. Rejecting that path made every step after it unreachable, so Contacts
// could never be set up.
func TestCardDAVRootIsReachable(t *testing.T) {
	user := uuid.New()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("PROPFIND", "/dav/", nil)

	(&CardDAV{}).handleList(recorder, request, user)

	body := recorder.Body.String()
	if !strings.Contains(body, "<d:current-user-principal>") {
		t.Fatalf("root does not name a principal:\n%s", body)
	}
	if !strings.Contains(body, "addressbook-home-set") {
		t.Error("root does not name an address book home")
	}
}

// The principal sits between the root and the address book; without it the
// client has nowhere to go after the root.
func TestCardDAVPrincipalNamesAddressBook(t *testing.T) {
	user := uuid.New()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("PROPFIND", "/dav/"+user.String()+"/", nil)

	(&CardDAV{}).handleList(recorder, request, user)

	body := recorder.Body.String()
	if !strings.Contains(body, "addressbook-home-set") {
		t.Errorf("principal does not name an address book home:\n%s", body)
	}
	if !strings.Contains(body, "/dav/"+user.String()+"/contacts/") {
		t.Error("address book home is not the contacts collection")
	}
	// Namespaced elements are required; clients reject bare element names.
	if !strings.Contains(body, `xmlns:d="DAV:"`) {
		t.Error("response is missing its namespaces")
	}
}

// Depth: 1 additionally enumerates the cards, which needs the store; this
// covers the collection itself.
func TestCardDAVCollectionIsAnAddressBook(t *testing.T) {
	user := uuid.New()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("PROPFIND", "/dav/"+user.String()+"/contacts/", nil)
	request.Header.Set("Depth", "0")

	(&CardDAV{}).handleList(recorder, request, user)

	body := recorder.Body.String()
	if !strings.Contains(body, "<card:addressbook/>") {
		t.Errorf("collection is not marked as an address book:\n%s", body)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "</d:multistatus>") {
		t.Error("response is not closed")
	}
}

func TestCardDAVRequiresAuthentication(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("PROPFIND", "/dav/", nil)

	(&CardDAV{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", recorder.Code)
	}
	if recorder.Header().Get("WWW-Authenticate") == "" {
		t.Error("no authentication challenge")
	}
}

// A client decides whether a collection is usable by reading the DAV header
// from an OPTIONS response. Answering 405 tells it this is not a CardDAV
// server, and iOS rejects the account with a validation error.
func TestCardDAVAnnouncesItselfOnOptions(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("OPTIONS", "/dav/"+uuid.New().String()+"/", nil)

	(&CardDAV{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if dav := recorder.Header().Get("DAV"); !strings.Contains(dav, "addressbook") {
		t.Errorf("DAV header = %q, want it to advertise addressbook", dav)
	}
	if allow := recorder.Header().Get("Allow"); !strings.Contains(allow, "PROPFIND") {
		t.Errorf("Allow header = %q, want it to list PROPFIND", allow)
	}
}

func TestCalDAVAnnouncesItselfOnOptions(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("OPTIONS", "/cal/"+uuid.New().String()+"/", nil)

	(&CalDAV{}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if dav := recorder.Header().Get("DAV"); !strings.Contains(dav, "calendar-access") {
		t.Errorf("DAV header = %q, want it to advertise calendar-access", dav)
	}
}

// A client PUTs an event under a resource name of its choosing (often not a
// UUID, e.g. iOS uses the event UID). The name must survive so the client can
// GET the event back at the same href.
func TestEventResourceFromPath(t *testing.T) {
	cases := map[string]string{
		"/cal/u/default/IOS-ABC-123.ics":                          "IOS-ABC-123",
		"/cal/u/default/946205ab-60bf-4cc0-8edf-762dc1ce6da2.ics": "946205ab-60bf-4cc0-8edf-762dc1ce6da2",
		"/cal/u/default/no-extension":                             "no-extension",
	}
	for path, want := range cases {
		if got := eventResourceFromPath(path); got != want {
			t.Errorf("eventResourceFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// iOS sends CRLF line endings, folded lines and timezone-qualified times. The
// naive prefix parser dropped all of these; the parser must recover the
// summary, UID and start/end without erroring.
func TestParseICSHandlesRealWorldEvent(t *testing.T) {
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\n" +
		"UID:IOS-TEST-1234-ABCD\r\n" +
		"DTSTART;TZID=Europe/Warsaw:20260301T100000\r\n" +
		"DTEND;TZID=Europe/Warsaw:20260301T110000\r\n" +
		"SUMMARY:Dentist\\, morning\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"

	title, start, end, uid := parseICS(raw)
	if title != "Dentist, morning" {
		t.Errorf("title = %q, want %q", title, "Dentist, morning")
	}
	if uid != "IOS-TEST-1234-ABCD" {
		t.Errorf("uid = %q", uid)
	}
	if start.IsZero() || end.IsZero() {
		t.Errorf("expected non-zero times, got start=%v end=%v", start, end)
	}
	if !start.Equal(time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("start = %v", start)
	}
}

func TestParseICSAllDayDate(t *testing.T) {
	raw := "BEGIN:VEVENT\nUID:x\nDTSTART;VALUE=DATE:20260301\nSUMMARY:Holiday\nEND:VEVENT\n"
	_, start, _, _ := parseICS(raw)
	if !start.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("all-day start = %v", start)
	}
}
