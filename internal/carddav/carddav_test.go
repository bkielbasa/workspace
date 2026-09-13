package carddav

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type stubContacts struct {
	list []contacts.Contact
}

func (s stubContacts) List(context.Context, uuid.UUID) ([]contacts.Contact, error) {
	return s.list, nil
}

func (s stubContacts) Get(_ context.Context, _ uuid.UUID, id uuid.UUID) (contacts.Contact, error) {
	for _, c := range s.list {
		if c.ID == id {
			return c, nil
		}
	}
	return contacts.Contact{}, fmt.Errorf("not found")
}

func (s stubContacts) PutCard(_ context.Context, _ uuid.UUID, id *uuid.UUID, c contacts.Contact) (*contacts.Contact, error) {
	c.ETag = "new-etag"
	return &c, nil
}

func (s stubContacts) Delete(context.Context, uuid.UUID, string) error { return nil }

func (s stubContacts) DeleteByID(context.Context, uuid.UUID, uuid.UUID) error { return nil }

type stubAuth struct{ userID uuid.UUID }

func (s stubAuth) Authenticate(context.Context, string, string) (*identity.User, error) {
	return &identity.User{ID: s.userID, Email: "u@example.com"}, nil
}

func testServer(userID uuid.UUID, list []contacts.Contact) http.Handler {
	return New(stubContacts{list: list}, stubAuth{userID: userID})
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

func TestAddressbookMultiget(t *testing.T) {
	userID := uuid.New()
	id1, id2 := uuid.New(), uuid.New()
	h := testServer(userID, []contacts.Contact{
		{ID: id1, UserID: userID, ETag: "etag-1", VCard: "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Alice\r\nEND:VCARD"},
		{ID: id2, UserID: userID, ETag: "etag-2", VCard: "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Bob\r\nEND:VCARD"},
	})

	// iOS sends absolute hrefs for known cards and asks for address-data.
	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<C:addressbook-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">
  <D:prop><D:getetag/><C:address-data/></D:prop>
  <D:href>https://dav.example.com/dav/%s/contacts/%s.vcf</D:href>
  <D:href>/dav/%s/contacts/%s.vcf</D:href>
  <D:href>/dav/%s/contacts/00000000-0000-0000-0000-000000000000.vcf</D:href>
</C:addressbook-multiget>`, userID, id1, userID, id2, userID)

	rec := doREPORT(t, h, "/dav/"+userID.String()+"/contacts/", body)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", rec.Code)
	}
	resp := rec.Body.String()
	if !strings.Contains(resp, "FN:Alice") || !strings.Contains(resp, "FN:Bob") {
		t.Errorf("multiget missing vCard data:\n%s", resp)
	}
	if !strings.Contains(resp, `<d:getetag>"etag-1"</d:getetag>`) {
		t.Errorf("multiget getetag not quoted:\n%s", resp)
	}
	if !strings.Contains(resp, "404 Not Found") {
		t.Errorf("multiget missing 404 for unknown href:\n%s", resp)
	}
}

func TestSyncCollection(t *testing.T) {
	userID := uuid.New()
	id1 := uuid.New()
	h := testServer(userID, []contacts.Contact{
		{ID: id1, UserID: userID, ETag: "etag-1", VCard: "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Alice\r\nEND:VCARD"},
	})

	body := `<?xml version="1.0" encoding="utf-8"?>
<D:sync-collection xmlns:D="DAV:">
  <D:sync-token/>
  <D:sync-level>1</D:sync-level>
  <D:prop><D:getetag/></D:prop>
</D:sync-collection>`

	rec := doREPORT(t, h, "/dav/"+userID.String()+"/contacts/", body)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", rec.Code)
	}
	resp := rec.Body.String()
	if !strings.Contains(resp, "FN:Alice") {
		t.Errorf("sync-collection missing vCard data:\n%s", resp)
	}
	if !strings.Contains(resp, "<d:sync-token>") {
		t.Errorf("sync-collection missing sync-token:\n%s", resp)
	}
}

func TestUnsupportedReport(t *testing.T) {
	userID := uuid.New()
	h := testServer(userID, nil)
	rec := doREPORT(t, h, "/dav/", `<D:expand-property xmlns:D="DAV:"/>`)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

func TestPutReturnsQuotedETag(t *testing.T) {
	userID := uuid.New()
	h := testServer(userID, nil)
	req := httptest.NewRequest(http.MethodPut, "/dav/"+userID.String()+"/contacts/x.vcf",
		strings.NewReader("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:New\r\nEND:VCARD"))
	req.SetBasicAuth("u@example.com", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get("ETag"); got != `"new-etag"` {
		t.Errorf("ETag = %q, want quoted", got)
	}
}
