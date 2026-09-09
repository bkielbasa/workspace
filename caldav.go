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
        fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">
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
        <d:sync-token>token-%s</d:sync-token>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, uid, uid)

        if r.Header.Get("Depth") == "1" {
            events, _ := c.cal.List(r.Context(), uid)
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
  </d:response>`, uid, e.ID, e.ETag)
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
  </d:response>`, uid, e.ID, e.ETag)
    }
    fmt.Fprint(w, `
</d:multistatus>`)
}

func (c *CalDAV) put(w http.ResponseWriter, r *http.Request, uid uuid.UUID) {
    body, _ := io.ReadAll(r.Body)
    title, start, end := parseICS(string(body))

    e, err := c.cal.Upsert(r.Context(), uid, title, start, end)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    w.Header().Set("ETag", e.ETag)
    w.WriteHeader(201)
}

func (c *CalDAV) get(w http.ResponseWriter, r *http.Request, uid uuid.UUID) {
    parts := strings.Split(r.URL.Path, "/")
    id := parts[len(parts)-1]

    events, _ := c.cal.List(r.Context(), uid)
    for _, e := range events {
        if e.ID.String()+".ics" == id {
            w.Header().Set("Content-Type", "text/calendar")
            fmt.Fprintf(w, "BEGIN:VCALENDAR\nBEGIN:VEVENT\nSUMMARY:%s\nDTSTART:%s\nDTEND:%s\nEND:VEVENT\nEND:VCALENDAR\n",
                e.Title,
                e.StartsAt.UTC().Format("20060102T150405Z"),
                e.EndsAt.UTC().Format("20060102T150405Z"),
            )
            return
        }
    }
    http.NotFound(w, r)
}

func parseICS(raw string) (title string, start, end time.Time) {
    for _, l := range strings.Split(raw, "\n") {
        if strings.HasPrefix(l, "SUMMARY:") {
            title = strings.TrimPrefix(l, "SUMMARY:")
        }
        if strings.HasPrefix(l, "DTSTART:") {
            start, _ = time.Parse("20060102T150405Z", strings.TrimPrefix(l, "DTSTART:"))
        }
        if strings.HasPrefix(l, "DTEND:") {
            end, _ = time.Parse("20060102T150405Z", strings.TrimPrefix(l, "DTEND:"))
        }
    }
    return
}

func parseUUID(s string) uuid.UUID {
    id, _ := uuid.Parse(s)
    return id
}
