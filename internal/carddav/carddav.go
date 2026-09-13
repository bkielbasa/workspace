package carddav

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/format/vcard"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

type contactService interface {
	List(ctx context.Context, userID uuid.UUID) ([]contacts.Contact, error)
	Get(ctx context.Context, userID, contactID uuid.UUID) (contacts.Contact, error)
	PutCard(ctx context.Context, userID uuid.UUID, id *uuid.UUID, contact contacts.Contact) (*contacts.Contact, error)
	Delete(ctx context.Context, userID uuid.UUID, email string) error
	DeleteByID(ctx context.Context, userID, contactID uuid.UUID) error
}

type authenticator interface {
	Authenticate(ctx context.Context, email, password string) (*identity.User, error)
}

type handler struct {
	contacts contactService
	users    authenticator
}

// New returns a CardDAV HTTP handler backed by the supplied application services.
func New(contacts contactService, users authenticator) http.Handler {
	return &handler{contacts: contacts, users: users}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/.well-known/carddav" {
		http.Redirect(w, r, "/dav/", http.StatusMovedPermanently)
		return
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("DAV", "1, 2, 3, addressbook")
		w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, PROPFIND, REPORT")
		w.WriteHeader(http.StatusOK)
		return
	}

	email, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", "Basic realm=carddav")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	user, err := h.users.Authenticate(r.Context(), email, password)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	obs.Log(r.Context(), slog.LevelInfo, "carddav request",
		"method", r.Method,
		"path", r.URL.Path,
	)

	switch r.Method {
	case "PROPFIND":
		h.list(w, r, user.ID)
	case "REPORT":
		h.report(w, r, user.ID)
	case http.MethodPut:
		h.put(w, r, user.ID)
	case http.MethodDelete:
		h.delete(w, r, user.ID)
	case http.MethodGet:
		h.get(w, r, user.ID)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *handler) list(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)

	var list []contacts.Contact
	if h.contacts != nil {
		list, _ = h.contacts.List(r.Context(), userID)
	}

	if r.URL.Path == "/dav/" || r.URL.Path == "/dav" {
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">
  <d:response>
    <d:href>/dav/</d:href>
    <d:propstat>
      <d:prop>
        <d:current-user-principal>
          <d:href>/dav/%s/</d:href>
        </d:current-user-principal>
        <card:addressbook-home-set>
          <d:href>/dav/%s/contacts/</d:href>
        </card:addressbook-home-set>
        <d:resourcetype>
          <d:collection/>
        </d:resourcetype>
        <d:displayname>CardDAV Root</d:displayname>
        <d:sync-token>token-%s</d:sync-token>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, userID, userID, userID)
		return
	}

	if segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/"); len(segments) == 2 {
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">
  <d:response>
    <d:href>/dav/%s/</d:href>
    <d:propstat>
      <d:prop>
        <d:current-user-principal>
          <d:href>/dav/%s/</d:href>
        </d:current-user-principal>
        <card:addressbook-home-set>
          <d:href>/dav/%s/contacts/</d:href>
        </card:addressbook-home-set>
        <d:resourcetype>
          <d:collection/>
          <d:principal/>
        </d:resourcetype>
        <d:displayname>Contacts principal</d:displayname>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, userID, userID, userID)
		return
	}

	if strings.HasSuffix(r.URL.Path, "/contacts/") {
		etags := make([]string, 0, len(list))
		for _, contact := range list {
			etags = append(etags, contact.ETag)
		}
		token := listToken(etags)
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav" xmlns:cs="http://calendarserver.org/ns/">
  <d:response>
    <d:href>/dav/%s/contacts/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype>
          <d:collection/>
          <card:addressbook/>
        </d:resourcetype>
        <d:displayname>Contacts</d:displayname>
        <cs:getctag>%s</cs:getctag>
        <d:sync-token>%s</d:sync-token>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, userID, token, token)

		if r.Header.Get("Depth") == "1" {
			for _, contact := range list {
				fmt.Fprintf(w, `
  <d:response>
    <d:href>/dav/%s/contacts/%s.vcf</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <d:getcontenttype>text/vcard; charset=utf-8</d:getcontenttype>
        <d:resourcetype/>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, userID, contact.ID, quoteETag(contact.ETag))
			}
		}

		fmt.Fprint(w, `
</d:multistatus>`)
		return
	}

	fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">`)
	for _, contact := range list {
		fmt.Fprintf(w, `
  <d:response>
    <d:href>/dav/%s/contacts/%s.vcf</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <d:getcontenttype>text/vcard; charset=utf-8</d:getcontenttype>
        <d:resourcetype/>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, userID, contact.ID, quoteETag(contact.ETag))
	}
	fmt.Fprint(w, `
</d:multistatus>`)
}

func (h *handler) report(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))

	var req reportRequest
	if err := xml.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}

	var list []contacts.Contact
	if h.contacts != nil {
		list, _ = h.contacts.List(r.Context(), userID)
	}
	byID := make(map[uuid.UUID]contacts.Contact, len(list))
	etags := make([]string, 0, len(list))
	for _, contact := range list {
		byID[contact.ID] = contact
		etags = append(etags, contact.ETag)
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	switch req.XMLName.Local {
	case "addressbook-multiget":
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">`)
		for _, href := range req.Hrefs {
			id, err := cardIDFromPath(href)
			contact, ok := byID[id]
			if err != nil || !ok {
				fmt.Fprintf(w, `
  <d:response>
    <d:href>%s</d:href>
    <d:status>HTTP/1.1 404 Not Found</d:status>
  </d:response>`, escapeXML(href))
				continue
			}
			fmt.Fprintf(w, `
  <d:response>
    <d:href>%s</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <card:address-data>%s</card:address-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, escapeXML(href), quoteETag(contact.ETag), escapeXML(contactCard(contact)))
		}
		fmt.Fprint(w, `
</d:multistatus>`)
	case "sync-collection":
		token := listToken(etags)
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">`)
		for _, contact := range list {
			fmt.Fprintf(w, `
  <d:response>
    <d:href>/dav/%s/contacts/%s.vcf</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <card:address-data>%s</card:address-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, userID, contact.ID, quoteETag(contact.ETag), escapeXML(contactCard(contact)))
		}
		fmt.Fprintf(w, `
  <d:sync-token>%s</d:sync-token>
</d:multistatus>`, token)
	default:
		http.Error(w, "unsupported report", http.StatusUnsupportedMediaType)
	}
}

// reportRequest decodes the REPORT body enough to route it. Field tags
// without a namespace match the local name in any namespace, which is what
// iOS and other clients send (D:, d:, card:, cal: prefixes vary).
type reportRequest struct {
	XMLName xml.Name
	Hrefs   []string `xml:"href"`
}

// contactCard returns the storable vCard, rebuilding it when the contact
// has no raw card yet (e.g. created in the web UI).
func contactCard(contact contacts.Contact) string {
	if strings.TrimSpace(contact.VCard) != "" {
		return contact.VCard
	}
	return vcard.Encode(contact, "")
}

// quoteETag renders an entity-tag per RFC 4918. Stored etags are bare
// UUIDs; emitting them quoted is what sync clients compare against.
func quoteETag(etag string) string {
	etag = strings.TrimSpace(etag)
	if strings.HasPrefix(etag, `"`) && strings.HasSuffix(etag, `"`) {
		return etag
	}
	return `"` + etag + `"`
}

func escapeXML(s string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

func (h *handler) put(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	body, _ := io.ReadAll(r.Body)
	contact := vcard.Parse(string(body))

	var id *uuid.UUID
	if parsed, err := cardIDFromPath(r.URL.Path); err == nil {
		id = &parsed
	}

	saved, err := h.contacts.PutCard(r.Context(), userID, id, contact)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("ETag", quoteETag(saved.ETag))
	w.WriteHeader(http.StatusCreated)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	if id, err := cardIDFromPath(r.URL.Path); err == nil {
		if err := h.contacts.DeleteByID(r.Context(), userID, id); err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	email := r.URL.Query().Get("email")
	_ = h.contacts.Delete(r.Context(), userID, email)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	id, err := cardIDFromPath(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contact, err := h.contacts.Get(r.Context(), userID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	card := contactCard(contact)
	w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
	w.Header().Set("ETag", quoteETag(contact.ETag))
	fmt.Fprint(w, card)
}

func cardIDFromPath(path string) (uuid.UUID, error) {
	last := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		last = path[i+1:]
	}
	return uuid.Parse(strings.TrimSuffix(last, ".vcf"))
}

func listToken(etags []string) string {
	hash := sha256.New()
	for _, etag := range etags {
		hash.Write([]byte(etag))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))[:20]
}
