package main

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "strings"

    "github.com/google/uuid"
)

type CardDAV struct {
    contacts *Contacts
}

func (c *CardDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if r.URL.Path == "/.well-known/carddav" {
        http.Redirect(w, r, "/dav/", http.StatusMovedPermanently)
        return
    }
    user, pass, ok := r.BasicAuth()
    if !ok {
        w.Header().Set("WWW-Authenticate", "Basic realm=carddav")
        w.WriteHeader(401)
        return
    }

    u, err := c.contactsUser(r.Context(), user, pass)
    if err != nil {
        w.WriteHeader(403)
        return
    }

    userID := u.ID

    switch r.Method {
    case "PROPFIND":
        c.handleList(w, r, userID)
    case "REPORT":
        c.handleReport(w, r, userID)
    case "PUT":
        c.handlePut(w, r, userID)
    case "DELETE":
        c.handleDelete(w, r, userID)
    case "GET":
        c.handleGet(w, r, userID)
    default:
        w.WriteHeader(405)
    }
}

func (c *CardDAV) handleList(w http.ResponseWriter, r *http.Request, uid uuid.UUID) {
    w.Header().Set("Content-Type", "application/xml; charset=utf-8")
    w.WriteHeader(207)

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
</d:multistatus>`, uid, uid, uid)
        return
    }

    // The principal. Clients read the root, then the principal it names, and
    // only then the address book, so this step cannot be skipped.
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
</d:multistatus>`, uid, uid, uid)
        return
    }

    if strings.HasSuffix(r.URL.Path, "/contacts/") {
        fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">
  <d:response>
    <d:href>/dav/%s/contacts/</d:href>
    <d:propstat>
      <d:prop>
        <d:resourcetype>
          <d:collection/>
          <card:addressbook/>
        </d:resourcetype>
        <d:displayname>Contacts</d:displayname>
        <d:sync-token>token-%s</d:sync-token>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>`, uid, uid)

        // Depth: 1 asks for the members of the collection as well, which is
        // how a client discovers the cards it needs to fetch.
        if r.Header.Get("Depth") == "1" {
            list, _ := c.contacts.List(r.Context(), uid)
            for _, ct := range list {
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
  </d:response>`, uid, ct.ID, ct.ETag)
            }
        }

        fmt.Fprint(w, `
</d:multistatus>`)
        return
    }

    list, _ := c.contacts.List(r.Context(), uid)

    fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">`)
    for _, ct := range list {
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
  </d:response>`, uid, ct.ID, ct.ETag)
    }
    fmt.Fprint(w, `
</d:multistatus>`)
}

func (c *CardDAV) handleReport(w http.ResponseWriter, r *http.Request, uid uuid.UUID) {
    // very minimal sync: return all (no sync-token diff yet)
    c.handleList(w, r, uid)
}

func (c *CardDAV) handlePut(w http.ResponseWriter, r *http.Request, uid uuid.UUID) {
    body, _ := io.ReadAll(r.Body)
    email, name := parseVCard(string(body))

    ct, err := c.contacts.Upsert(r.Context(), uid, email, name)
    if err != nil {
        http.Error(w, err.Error(), 500)
        return
    }

    w.Header().Set("ETag", ct.ETag)
    w.WriteHeader(201)
}

func (c *CardDAV) handleDelete(w http.ResponseWriter, r *http.Request, uid uuid.UUID) {
    // naive: delete by id via lookup not implemented → fallback by email param
    email := r.URL.Query().Get("email")
    _ = c.contacts.Delete(r.Context(), uid, email)
    w.WriteHeader(204)
}

func (c *CardDAV) handleGet(w http.ResponseWriter, r *http.Request, uid uuid.UUID) {
    parts := strings.Split(r.URL.Path, "/")
    id := parts[len(parts)-1]

    list, _ := c.contacts.List(r.Context(), uid)
    for _, ct := range list {
        if ct.ID.String()+".vcf" == id {
            w.Header().Set("Content-Type", "text/vcard")
            fmt.Fprintf(w, "BEGIN:VCARD\nVERSION:3.0\nFN:%s\nEMAIL:%s\nEND:VCARD\n", ct.Name, ct.Email)
            return
        }
    }
    http.NotFound(w, r)
}

func parseVCard(v string) (email, name string) {
    for _, line := range strings.Split(v, "\n") {
        if strings.HasPrefix(line, "EMAIL:") {
            email = strings.TrimSpace(strings.TrimPrefix(line, "EMAIL:"))
        }
        if strings.HasPrefix(line, "FN:") {
            name = strings.TrimSpace(strings.TrimPrefix(line, "FN:"))
        }
    }
    return
}

func (c *CardDAV) contactsUser(ctx context.Context, email, password string) (*User, error) {
    u := &Users{db: c.contacts.db}
    return u.Authenticate(ctx, email, password)
}
