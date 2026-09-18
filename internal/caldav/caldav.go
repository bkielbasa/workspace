package caldav

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

	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/format/ics"
	"github.com/bklimczak/workspace/internal/format/vtodo"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/notes"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

type calendarService interface {
	List(ctx context.Context, userID uuid.UUID) ([]calendar.Event, error)
	Put(ctx context.Context, event calendar.Event) (*calendar.Event, error)
	GetByResource(ctx context.Context, userID uuid.UUID, resource string) (*calendar.Event, error)
	DeleteByResource(ctx context.Context, userID uuid.UUID, resource string) error
}

type notesService interface {
	ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]notes.Note, error)
	GetNote(ctx context.Context, userID uuid.UUID, id uuid.UUID) (*notes.Note, error)
	AddItem(ctx context.Context, userID uuid.UUID, noteID uuid.UUID, content string) (*notes.NoteItem, error)
	ToggleItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, completed bool) (*notes.NoteItem, error)
	DeleteItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID) error
}

type authenticator interface {
	Authenticate(ctx context.Context, email, password string) (*identity.User, error)
}

type handler struct {
	calendar calendarService
	notes    notesService
	users    authenticator
}

// New returns a CalDAV HTTP handler backed by the supplied application services.
func New(calendar calendarService, users authenticator) http.Handler {
	return NewWithTasks(calendar, nil, users)
}

// NewWithTasks returns a CalDAV HTTP handler backed by calendar, notes, and auth services.
func NewWithTasks(calendar calendarService, notes notesService, users authenticator) http.Handler {
	return &handler{calendar: calendar, notes: notes, users: users}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/.well-known/caldav" {
		http.Redirect(w, r, "/cal/", http.StatusMovedPermanently)
		return
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("DAV", "1, 2, 3, calendar-access")
		w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, PROPFIND, REPORT")
		w.WriteHeader(http.StatusOK)
		return
	}

	email, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", "Basic realm=caldav")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	user, err := h.users.Authenticate(r.Context(), email, password)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	obs.Log(r.Context(), slog.LevelInfo, "caldav request",
		"method", r.Method,
		"path", r.URL.Path,
	)

	target := parseListPath(r.URL.Path)
	if target.isList {
		switch r.Method {
		case "PROPFIND":
			h.listTasks(w, r, user.ID, target)
		case "REPORT":
			h.reportTasks(w, r, user.ID, target)
		case http.MethodPut:
			h.putTask(w, r, user.ID, target)
		case http.MethodGet:
			h.getTask(w, r, user.ID, target)
		case http.MethodDelete:
			h.deleteTask(w, r, user.ID, target)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	switch r.Method {
	case "PROPFIND":
		h.list(w, r, user.ID)
	case "REPORT":
		h.report(w, r, user.ID)
	case http.MethodPut:
		h.put(w, r, user.ID)
	case http.MethodGet:
		h.get(w, r, user.ID)
	case http.MethodDelete:
		h.delete(w, r, user.ID)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *handler) list(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)

	if r.URL.Path == "/cal/" || r.URL.Path == "/cal" {
		var listHrefs strings.Builder
		if h.notes != nil {
			if allNotes, err := h.notes.ListNotes(r.Context(), userID, false, ""); err == nil {
				for _, n := range allNotes {
					if n.Kind == notes.KindList {
						listHrefs.WriteString(fmt.Sprintf("\n          <d:href>/cal/lists/%s/</d:href>", n.ID))
					}
				}
			}
		}
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/cal/</d:href>
    <d:propstat>
      <d:prop>
        <d:current-user-principal>
          <d:href>/cal/%s/</d:href>
        </d:current-user-principal>
        <cal:calendar-home-set>
          <d:href>/cal/%s/default/</d:href>%s
        </cal:calendar-home-set>
        <d:resourcetype>
          <d:collection/>
        </d:resourcetype>
        <d:displayname>CalDAV Root</d:displayname>
        <d:sync-token>token-%s</d:sync-token>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, userID, userID, listHrefs.String(), userID)
		return
	}

	if segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/"); len(segments) == 2 {
		var listHrefs strings.Builder
		if h.notes != nil {
			if allNotes, err := h.notes.ListNotes(r.Context(), userID, false, ""); err == nil {
				for _, n := range allNotes {
					if n.Kind == notes.KindList {
						listHrefs.WriteString(fmt.Sprintf("\n          <d:href>/cal/lists/%s/</d:href>", n.ID))
					}
				}
			}
		}
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/cal/%s/</d:href>
    <d:propstat>
      <d:prop>
        <d:current-user-principal>
          <d:href>/cal/%s/</d:href>
        </d:current-user-principal>
        <cal:calendar-home-set>
          <d:href>/cal/%s/default/</d:href>%s
        </cal:calendar-home-set>
        <d:resourcetype>
          <d:collection/>
          <d:principal/>
        </d:resourcetype>
        <d:displayname>Calendar principal</d:displayname>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, userID, userID, userID, listHrefs.String())
		return
	}

	if strings.HasSuffix(r.URL.Path, "/default/") {
		events, _ := h.calendar.List(r.Context(), userID)
		etags := make([]string, 0, len(events))
		for _, event := range events {
			etags = append(etags, event.ETag)
		}
		token := listToken(etags)
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/">
  <d:response>
    <d:href>/cal/%s/default/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype>
          <d:collection/>
          <cal:calendar/>
        </d:resourcetype>
        <cal:supported-calendar-component-set>
          <cal:comp name="VEVENT"/>
        </cal:supported-calendar-component-set>
        <d:displayname>Default Calendar</d:displayname>
        <cs:getctag>%s</cs:getctag>
        <d:sync-token>%s</d:sync-token>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, userID, token, token)

		if r.Header.Get("Depth") == "1" {
			for _, event := range events {
				fmt.Fprintf(w, `
  <d:response>
    <d:href>/cal/%s/default/%s.ics</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <d:getcontenttype>text/calendar; charset=utf-8</d:getcontenttype>
        <d:resourcetype/>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, userID, event.Href(), quoteETag(event.ETag))
			}
		}

		fmt.Fprint(w, `
</d:multistatus>`)
		return
	}

	events, _ := h.calendar.List(r.Context(), userID)
	fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">`)
	for _, event := range events {
		fmt.Fprintf(w, `
  <d:response>
    <d:href>/cal/%s/default/%s.ics</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <d:getcontenttype>text/calendar; charset=utf-8</d:getcontenttype>
        <d:resourcetype/>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, userID, event.Href(), quoteETag(event.ETag))
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

	events, _ := h.calendar.List(r.Context(), userID)
	byResource := make(map[string]calendar.Event, len(events))
	etags := make([]string, 0, len(events))
	for _, event := range events {
		byResource[event.Href()] = event
		etags = append(etags, event.ETag)
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	switch req.XMLName.Local {
	case "calendar-multiget":
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">`)
		for _, href := range req.Hrefs {
			event, ok := byResource[eventResourceFromPath(href)]
			if !ok {
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
        <cal:calendar-data>%s</cal:calendar-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, escapeXML(href), quoteETag(event.ETag), escapeXML(eventICS(event)))
		}
		fmt.Fprint(w, `
</d:multistatus>`)
	case "calendar-query", "sync-collection":
		// Filters and time ranges are not evaluated yet: the collection
		// is small, so every report returns the full set. iOS accepts
		// this for query and for initial/delta syncs alike.
		token := listToken(etags)
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">`)
		for _, event := range events {
			fmt.Fprintf(w, `
  <d:response>
    <d:href>/cal/%s/default/%s.ics</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <cal:calendar-data>%s</cal:calendar-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, userID, event.Href(), quoteETag(event.ETag), escapeXML(eventICS(event)))
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
// iOS and other clients send (D:, d:, cal: prefixes vary).
type reportRequest struct {
	XMLName xml.Name
	Hrefs   []string `xml:"href"`
}

// eventICS returns the raw stored body when the event came from DAV,
// rebuilding it from structured fields for web-created events.
func eventICS(event calendar.Event) string {
	if strings.TrimSpace(event.ICS) != "" {
		return event.ICS
	}
	return ics.Encode(event)
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
	resource := eventResourceFromPath(r.URL.Path)
	if resource == "" {
		http.Error(w, "bad event path", http.StatusBadRequest)
		return
	}

	body, _ := io.ReadAll(r.Body)
	event, err := ics.Parse(string(body), userID, resource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	saved, err := h.calendar.Put(r.Context(), event)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("ETag", quoteETag(saved.ETag))
	w.WriteHeader(http.StatusCreated)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	resource := eventResourceFromPath(r.URL.Path)
	if resource == "" {
		http.NotFound(w, r)
		return
	}
	event, err := h.calendar.GetByResource(r.Context(), userID, resource)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("ETag", quoteETag(event.ETag))
	fmt.Fprint(w, eventICS(*event))
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	resource := eventResourceFromPath(r.URL.Path)
	if resource == "" {
		http.NotFound(w, r)
		return
	}
	if err := h.calendar.DeleteByResource(r.Context(), userID, resource); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func eventResourceFromPath(path string) string {
	last := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		last = path[i+1:]
	}
	return strings.TrimSuffix(last, ".ics")
}

func listToken(etags []string) string {
	hash := sha256.New()
	for _, etag := range etags {
		hash.Write([]byte(etag))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))[:20]
}

type listTarget struct {
	isList      bool
	isListsRoot bool
	noteID      uuid.UUID
	resource    string
	isItem      bool
}

func parseListPath(path string) listTarget {
	trimmed := strings.Trim(path, "/")
	parts := strings.Split(trimmed, "/")
	listsIdx := -1
	for i, part := range parts {
		if part == "lists" {
			listsIdx = i
			break
		}
	}
	if listsIdx == -1 {
		return listTarget{}
	}
	target := listTarget{isList: true}
	rem := parts[listsIdx+1:]
	if len(rem) == 0 {
		target.isListsRoot = true
		return target
	}
	noteID, err := uuid.Parse(rem[0])
	if err != nil {
		return target
	}
	target.noteID = noteID
	if len(rem) == 1 {
		return target
	}
	target.isItem = true
	target.resource = strings.TrimSuffix(rem[1], ".ics")
	return target
}

func (h *handler) listTasks(w http.ResponseWriter, r *http.Request, userID uuid.UUID, target listTarget) {
	if h.notes == nil {
		http.NotFound(w, r)
		return
	}

	if target.isListsRoot {
		allNotes, err := h.notes.ListNotes(r.Context(), userID, false, "")
		if err != nil {
			allNotes = nil
		}
		var listNotes []notes.Note
		for _, n := range allNotes {
			if n.Kind == notes.KindList {
				listNotes = append(listNotes, n)
			}
		}

		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav" xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/">
  <d:response>
    <d:href>/cal/lists/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype>
          <d:collection/>
        </d:resourcetype>
        <d:displayname>Lists</d:displayname>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`)

		if r.Header.Get("Depth") == "1" {
			for _, n := range listNotes {
				token := noteToken(&n)
				displayName := n.Title
				if displayName == "" {
					displayName = "List"
				}
				fmt.Fprintf(w, `
  <d:response>
    <d:href>/cal/lists/%s/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype>
          <d:collection/>
          <cal:calendar/>
        </d:resourcetype>
        <C:supported-calendar-component-set>
          <C:comp name="VTODO"/>
        </C:supported-calendar-component-set>
        <d:displayname>%s</d:displayname>
        <cs:getctag>%s</cs:getctag>
        <d:sync-token>%s</d:sync-token>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, n.ID, escapeXML(displayName), token, token)
			}
		}
		fmt.Fprint(w, `
</d:multistatus>`)
		return
	}

	if target.noteID == uuid.Nil {
		http.NotFound(w, r)
		return
	}

	note, err := h.notes.GetNote(r.Context(), userID, target.noteID)
	if err != nil || note.Kind != notes.KindList {
		http.NotFound(w, r)
		return
	}

	if target.isItem {
		var found *notes.NoteItem
		for i := range note.Items {
			if note.Items[i].ID.String() == target.resource {
				found = &note.Items[i]
				break
			}
		}
		if found == nil {
			http.NotFound(w, r)
			return
		}
		etag := itemETag(*found)
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/cal/lists/%s/%s.ics</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <d:getcontenttype>text/calendar; charset=utf-8</d:getcontenttype>
        <d:resourcetype/>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`, target.noteID, found.ID, quoteETag(etag))
		return
	}

	// Collection PROPFIND: /cal/lists/{note_id}/
	token := noteToken(note)
	displayName := note.Title
	if displayName == "" {
		displayName = "List"
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav" xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:cs="http://calendarserver.org/ns/">
  <d:response>
    <d:href>/cal/lists/%s/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype>
          <d:collection/>
          <cal:calendar/>
        </d:resourcetype>
        <C:supported-calendar-component-set>
          <C:comp name="VTODO"/>
        </C:supported-calendar-component-set>
        <d:displayname>%s</d:displayname>
        <cs:getctag>%s</cs:getctag>
        <d:sync-token>%s</d:sync-token>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, note.ID, escapeXML(displayName), token, token)

	if r.Header.Get("Depth") == "1" {
		for _, item := range note.Items {
			fmt.Fprintf(w, `
  <d:response>
    <d:href>/cal/lists/%s/%s.ics</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <d:getcontenttype>text/calendar; charset=utf-8</d:getcontenttype>
        <d:resourcetype/>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, note.ID, item.ID, quoteETag(itemETag(item)))
		}
	}
	fmt.Fprint(w, `
</d:multistatus>`)
}

func (h *handler) reportTasks(w http.ResponseWriter, r *http.Request, userID uuid.UUID, target listTarget) {
	if h.notes == nil || target.noteID == uuid.Nil {
		http.NotFound(w, r)
		return
	}
	note, err := h.notes.GetNote(r.Context(), userID, target.noteID)
	if err != nil || note.Kind != notes.KindList {
		http.NotFound(w, r)
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var req reportRequest
	if err := xml.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}

	byResource := make(map[string]notes.NoteItem, len(note.Items))
	for _, item := range note.Items {
		byResource[item.ID.String()] = item
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	switch req.XMLName.Local {
	case "calendar-multiget":
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav" xmlns:C="urn:ietf:params:xml:ns:caldav">`)
		for _, href := range req.Hrefs {
			resource := eventResourceFromPath(href)
			item, ok := byResource[resource]
			if !ok {
				fmt.Fprintf(w, `
  <d:response>
    <d:href>%s</d:href>
    <d:status>HTTP/1.1 404 Not Found</d:status>
  </d:response>`, escapeXML(href))
				continue
			}
			etag := itemETag(item)
			icsData := vtodo.Format(item)
			fmt.Fprintf(w, `
  <d:response>
    <d:href>%s</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <cal:calendar-data>%s</cal:calendar-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, escapeXML(href), quoteETag(etag), escapeXML(icsData))
		}
		fmt.Fprint(w, `
</d:multistatus>`)
	case "calendar-query", "sync-collection":
		token := noteToken(note)
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav" xmlns:C="urn:ietf:params:xml:ns:caldav">`)
		for _, item := range note.Items {
			etag := itemETag(item)
			icsData := vtodo.Format(item)
			fmt.Fprintf(w, `
  <d:response>
    <d:href>/cal/lists/%s/%s.ics</d:href>
    <d:propstat>
      <d:prop>
        <d:getetag>%s</d:getetag>
        <cal:calendar-data>%s</cal:calendar-data>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, target.noteID, item.ID, quoteETag(etag), escapeXML(icsData))
		}
		fmt.Fprintf(w, `
  <d:sync-token>%s</d:sync-token>
</d:multistatus>`, token)
	default:
		http.Error(w, "unsupported report", http.StatusUnsupportedMediaType)
	}
}

func (h *handler) getTask(w http.ResponseWriter, r *http.Request, userID uuid.UUID, target listTarget) {
	if h.notes == nil || target.noteID == uuid.Nil || !target.isItem {
		http.NotFound(w, r)
		return
	}
	note, err := h.notes.GetNote(r.Context(), userID, target.noteID)
	if err != nil || note.Kind != notes.KindList {
		http.NotFound(w, r)
		return
	}
	var found *notes.NoteItem
	for i := range note.Items {
		if note.Items[i].ID.String() == target.resource {
			found = &note.Items[i]
			break
		}
	}
	if found == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("ETag", quoteETag(itemETag(*found)))
	fmt.Fprint(w, vtodo.Format(*found))
}

func (h *handler) putTask(w http.ResponseWriter, r *http.Request, userID uuid.UUID, target listTarget) {
	if h.notes == nil || target.noteID == uuid.Nil || !target.isItem {
		http.Error(w, "bad list item path", http.StatusBadRequest)
		return
	}
	note, err := h.notes.GetNote(r.Context(), userID, target.noteID)
	if err != nil || note.Kind != notes.KindList {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.Contains(string(body), "BEGIN:VTODO") {
		http.Error(w, "missing VTODO component", http.StatusBadRequest)
		return
	}
	parsedItem, err := vtodo.Parse(string(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if itemID, err := uuid.Parse(target.resource); err == nil && parsedItem.ID == uuid.Nil {
		parsedItem.ID = itemID
	}

	var existing *notes.NoteItem
	for i := range note.Items {
		if note.Items[i].ID == parsedItem.ID || note.Items[i].ID.String() == target.resource {
			existing = &note.Items[i]
			break
		}
	}

	if existing != nil {
		resultItem := existing
		if parsedItem.Completed != existing.Completed {
			updated, err := h.notes.ToggleItem(r.Context(), userID, target.noteID, existing.ID, parsedItem.Completed)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			resultItem = updated
		}
		w.Header().Set("ETag", quoteETag(itemETag(*resultItem)))
		w.WriteHeader(http.StatusNoContent)
		return
	}

	created, err := h.notes.AddItem(r.Context(), userID, target.noteID, parsedItem.Content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resultItem := created
	if parsedItem.Completed {
		updated, err := h.notes.ToggleItem(r.Context(), userID, target.noteID, created.ID, true)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resultItem = updated
	}
	w.Header().Set("ETag", quoteETag(itemETag(*resultItem)))
	w.WriteHeader(http.StatusCreated)
}

func (h *handler) deleteTask(w http.ResponseWriter, r *http.Request, userID uuid.UUID, target listTarget) {
	if h.notes == nil || target.noteID == uuid.Nil || !target.isItem {
		http.NotFound(w, r)
		return
	}
	itemID, err := uuid.Parse(target.resource)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.notes.DeleteItem(r.Context(), userID, target.noteID, itemID); err != nil {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func itemETag(item notes.NoteItem) string {
	hash := sha256.New()
	hash.Write([]byte(item.ID.String()))
	hash.Write([]byte(item.UpdatedAt.Format(time.RFC3339Nano)))
	if item.Completed {
		hash.Write([]byte{1})
	} else {
		hash.Write([]byte{0})
	}
	hash.Write([]byte(item.Content))
	return hex.EncodeToString(hash.Sum(nil))[:20]
}

func noteToken(note *notes.Note) string {
	hash := sha256.New()
	hash.Write([]byte(note.ID.String()))
	hash.Write([]byte(note.UpdatedAt.Format(time.RFC3339Nano)))
	for _, item := range note.Items {
		hash.Write([]byte(itemETag(item)))
	}
	return hex.EncodeToString(hash.Sum(nil))[:20]
}
