package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type CalDAV struct {
	cal   *Calendar
	users *Users
}

func (c *CalDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/.well-known/caldav" {
		http.Redirect(w, r, "/cal/", http.StatusMovedPermanently)
		return
	}
	// See the CardDAV handler: clients read the DAV header from an OPTIONS
	// response to decide whether the collection is usable at all.
	if r.Method == "OPTIONS" {
		w.Header().Set("DAV", "1, 2, 3, calendar-access")
		w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, PROPFIND, REPORT")
		w.WriteHeader(http.StatusOK)
		return
	}

	user, pass, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", "Basic realm=caldav")
		w.WriteHeader(401)
		return
	}

	u, err := c.users.Authenticate(context.Background(), user, pass)
	if err != nil {
		w.WriteHeader(403)
		return
	}

	switch r.Method {
	case "PROPFIND":
		c.list(w, r, u.ID)
	case "REPORT":
		c.list(w, r, u.ID)
	case "PUT":
		c.put(w, r, u.ID)
	case "GET":
		c.get(w, r, u.ID)
	case "DELETE":
		c.delete(w, r, u.ID)
	default:
		w.WriteHeader(405)
	}
}

func (c *CalDAV) list(w http.ResponseWriter, r *http.Request, uid uuid.UUID) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(207)

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
</d:multistatus>`, uid, uid, uid)
		return
	}

	// The principal, which clients read before they can find the calendar.
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
</d:multistatus>`, uid, uid, uid)
		return
	}

	if strings.HasSuffix(r.URL.Path, "/default/") {
		events, _ := c.cal.List(r.Context(), uid)
		etags := make([]string, 0, len(events))
		for _, e := range events {
			etags = append(etags, e.ETag)
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
  </d:response>`, uid, token, token)

		if r.Header.Get("Depth") == "1" {
			for _, e := range events {
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
  </d:response>`, uid, e.Href(), e.ETag)
			}
		}

		fmt.Fprint(w, `
</d:multistatus>`)
		return
	}

	events, _ := c.cal.List(r.Context(), uid)

	fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">`)
	for _, e := range events {
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
  </d:response>`, uid, e.Href(), e.ETag)
	}
	fmt.Fprint(w, `
</d:multistatus>`)
}

func (c *CalDAV) put(w http.ResponseWriter, r *http.Request, uid uuid.UUID) {
	body, _ := io.ReadAll(r.Body)
	raw := string(body)
	title, start, end, icalUID := parseICS(raw)

	// The client addresses the event by the resource name in the URL and
	// expects to read it back at that same href; keying on it (not a fresh
	// random id) is what makes the create/update round trip.
	resource := eventResourceFromPath(r.URL.Path)
	if resource == "" {
		http.Error(w, "bad event path", http.StatusBadRequest)
		return
	}

	e, err := c.cal.PutICS(r.Context(), uid, resource, raw, icalUID, title, start, end)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("ETag", e.ETag)
	w.WriteHeader(201)
}

func (c *CalDAV) get(w http.ResponseWriter, r *http.Request, uid uuid.UUID) {
	resource := eventResourceFromPath(r.URL.Path)
	if resource == "" {
		http.NotFound(w, r)
		return
	}
	e, err := c.cal.GetByResource(r.Context(), uid, resource)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("ETag", e.ETag)
	// A CalDAV client's own body is served back verbatim; web-created events
	// have none, so serialise one on the fly.
	if strings.TrimSpace(e.ICS) != "" {
		fmt.Fprint(w, e.ICS)
		return
	}
	fmt.Fprint(w, icsEvent(*e))
}

func (c *CalDAV) delete(w http.ResponseWriter, r *http.Request, uid uuid.UUID) {
	resource := eventResourceFromPath(r.URL.Path)
	if resource == "" {
		http.NotFound(w, r)
		return
	}
	if err := c.cal.DeleteByResource(r.Context(), uid, resource); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}

// eventResourceFromPath extracts the event resource name from
// /cal/{uid}/default/{resource}.ics. The name is client-chosen and need not be
// a UUID.
func eventResourceFromPath(path string) string {
	last := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		last = path[i+1:]
	}
	return strings.TrimSuffix(last, ".ics")
}

// icsEvent serializes an event as a full iCalendar object. Clients (iOS,
// DAVx5) drop VEVENTs that have no UID or DTSTAMP, and require the envelope
// to be versioned, so the minimal object below is enough to be importable.
func icsEvent(e Event) string {
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\n")
	b.WriteString("VERSION:2.0\n")
	b.WriteString("PRODID:-//Workspace//Workspace//EN\n")
	b.WriteString("CALSCALE:GREGORIAN\n")
	b.WriteString("BEGIN:VEVENT\n")
	b.WriteString("UID:" + e.ID.String() + "\n")
	b.WriteString("DTSTAMP:" + e.UpdatedAt.UTC().Format("20060102T150405Z") + "\n")
	b.WriteString("DTSTART:" + e.StartsAt.UTC().Format("20060102T150405Z") + "\n")
	b.WriteString("DTEND:" + e.EndsAt.UTC().Format("20060102T150405Z") + "\n")
	b.WriteString("SUMMARY:" + escapeVCardValue(e.Title) + "\n")
	b.WriteString("END:VEVENT\n")
	b.WriteString("END:VCALENDAR\n")
	return b.String()
}

// parseICS pulls a best-effort title/start/end/UID out of an iCalendar body for
// the web calendar's columns. The raw body is what CalDAV serves back, so this
// only needs to be good enough to sort and label events, and must never fail:
// unknown or timezone-qualified times simply yield a zero time (stored NULL).
func parseICS(raw string) (title string, start, end time.Time, uid string) {
	for _, line := range unfoldICS(raw) {
		name, params, value := splitICSLine(line)
		switch strings.ToUpper(name) {
		case "SUMMARY":
			title = unescapeICSText(value)
		case "UID":
			uid = strings.TrimSpace(value)
		case "DTSTART":
			start = parseICSTime(params, value)
		case "DTEND":
			end = parseICSTime(params, value)
		}
	}
	return
}

// unfoldICS splits an iCalendar object into logical lines, joining RFC 5545
// folded continuations (a line starting with a space or tab) and trimming CRLF.
func unfoldICS(raw string) []string {
	var lines []string
	var buf string
	for _, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		if line != "" && (line[0] == ' ' || line[0] == '\t') && buf != "" {
			buf += line[1:]
			continue
		}
		if buf != "" {
			lines = append(lines, buf)
		}
		buf = line
	}
	if buf != "" {
		lines = append(lines, buf)
	}
	return lines
}

// splitICSLine breaks "NAME;PARAM=v:VALUE" into its parts.
func splitICSLine(line string) (name, params, value string) {
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return line, "", ""
	}
	head, value := line[:colon], line[colon+1:]
	if semi := strings.IndexByte(head, ';'); semi >= 0 {
		return head[:semi], head[semi+1:], value
	}
	return head, "", value
}

// parseICSTime handles the common DTSTART/DTEND forms: UTC (Z), floating local
// date-time, and all-day dates (VALUE=DATE). Timezone-qualified values are read
// as their wall-clock time; exact zone math is unnecessary because the raw ICS
// is served back unchanged.
func parseICSTime(params, value string) time.Time {
	value = strings.TrimSpace(value)
	if strings.Contains(strings.ToUpper(params), "VALUE=DATE") || len(value) == 8 {
		if t, err := time.Parse("20060102", value); err == nil {
			return t
		}
	}
	for _, layout := range []string{"20060102T150405Z", "20060102T150405"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func unescapeICSText(v string) string {
	v = strings.ReplaceAll(v, `\n`, "\n")
	v = strings.ReplaceAll(v, `\N`, "\n")
	v = strings.ReplaceAll(v, `\,`, ",")
	v = strings.ReplaceAll(v, `\;`, ";")
	v = strings.ReplaceAll(v, `\\`, `\`)
	return strings.TrimSpace(v)
}
