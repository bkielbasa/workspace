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

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/format/ics"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

type calendarService interface {
	List(ctx context.Context, userID uuid.UUID) ([]calendar.Event, error)
	Put(ctx context.Context, event calendar.Event) (*calendar.Event, error)
	GetByResource(ctx context.Context, userID uuid.UUID, resource string) (*calendar.Event, error)
	DeleteByResource(ctx context.Context, userID uuid.UUID, resource string) error
}

type authenticator interface {
	Authenticate(ctx context.Context, email, password string) (*identity.User, error)
}

type handler struct {
	calendar calendarService
	users    authenticator
}

// New returns a CalDAV HTTP handler backed by the supplied application services.
func New(calendar calendarService, users authenticator) http.Handler {
	return &handler{calendar: calendar, users: users}
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
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/cal/</d:href>
    <d:propstat>
      <d:prop>
        <d:current-user-principal>
          <d:href>/cal/%s/</d:href>
        </d:current-user-principal>
        <cal:calendar-home-set>
          <d:href>/cal/%s/default/</d:href>
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
</d:multistatus>`, userID, userID, userID)
		return
	}

	if segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/"); len(segments) == 2 {
		fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">
  <d:response>
    <d:href>/cal/%s/</d:href>
    <d:propstat>
      <d:prop>
        <d:current-user-principal>
          <d:href>/cal/%s/</d:href>
        </d:current-user-principal>
        <cal:calendar-home-set>
          <d:href>/cal/%s/default/</d:href>
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
</d:multistatus>`, userID, userID, userID)
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
