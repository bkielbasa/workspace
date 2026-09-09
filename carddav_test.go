package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
