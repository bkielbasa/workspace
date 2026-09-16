package files

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/obs"
	"github.com/google/uuid"
)

// prefix is the mount point; it must match the mux registration in main.go.
const prefix = "/files/"

type authenticator interface {
	Authenticate(ctx context.Context, email, password string) (*identity.User, error)
}

type handler struct {
	store *Store
	users authenticator
}

// New returns a WebDAV (RFC 4918, class 1) handler over the store.
func New(store *Store, users authenticator) http.Handler {
	return &handler{store: store, users: users}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sw := &statusWriter{ResponseWriter: w}
	defer func() {
		obs.Log(r.Context(), slog.LevelInfo, "files request",
			"method", r.Method,
			"path", r.URL.Path,
			"depth", strings.TrimSpace(r.Header.Get("Depth")),
			"user_agent", r.Header.Get("User-Agent"),
			"content_type", r.Header.Get("Content-Type"),
			"status", sw.status,
			"bytes", sw.bytes,
		)
	}()
	w = sw

	if r.Method == http.MethodOptions {
		// Class 1 only: collections + properties, no locking. Claiming
		// "2" makes strict clients probe LOCK support that 405s here.
		w.Header().Set("DAV", "1")
		w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, MKCOL, MOVE, COPY, PROPFIND")
		w.WriteHeader(http.StatusOK)
		return
	}

	email, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", "Basic realm=files")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	user, err := h.users.Authenticate(r.Context(), email, password)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// The user's tree springs into existence on first authenticated use,
	// so fresh accounts answer PROPFIND on / instead of 404.
	if err := h.store.EnsureUserRoot(user.ID); err != nil {
		writeErr(w, err)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, prefix)
	// Also serve without trailing slash on the root: "/files" -> "".
	if r.URL.Path == "/files" {
		name = ""
	}

	switch r.Method {
	case "PROPFIND":
		h.propfind(w, r, user.ID, name)
	case http.MethodGet:
		h.get(w, r, user.ID, name)
	case http.MethodHead:
		h.head(w, r, user.ID, name)
	case http.MethodPut:
		h.put(w, r, user.ID, name)
	case http.MethodDelete:
		h.delete(w, r, user.ID, name)
	case "MKCOL":
		h.mkcol(w, r, user.ID, name)
	case "MOVE":
		h.move(w, r, user.ID, name)
	case "COPY":
		h.copy(w, r, user.ID, name)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// statusWriter records the response status and byte count for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// href builds the absolute server path for a virtual path.
func href(name string) string {
	clean := path.Clean("/" + name)
	if clean == "/" {
		return prefix
	}
	u := url.URL{Path: prefix + strings.TrimPrefix(clean, "/")}
	escaped := u.EscapedPath()
	if strings.HasSuffix(name, "/") && !strings.HasSuffix(escaped, "/") {
		escaped += "/"
	}
	return escaped
}

func writeErr(w http.ResponseWriter, err error) {
	switch err {
	case ErrNotFound:
		http.Error(w, "not found", http.StatusNotFound)
	case ErrExists:
		http.Error(w, "already exists", http.StatusConflict)
	case ErrConflict:
		http.Error(w, "conflict", http.StatusConflict)
	case ErrTooLarge:
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
	case ErrQuotaExceeded:
		http.Error(w, "insufficient storage", http.StatusInsufficientStorage)
	case ErrOutsideRoot:
		http.Error(w, "invalid path", http.StatusBadRequest)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (h *handler) propfind(w http.ResponseWriter, r *http.Request, userID uuid.UUID, name string) {
	// The body only names requested properties (never credentials); logging
	// it shows exactly what a picky client validates.
	rawBody, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	obs.Log(r.Context(), slog.LevelInfo, "files propfind body",
		"path", r.URL.Path,
		"body", strings.TrimSpace(string(rawBody)),
	)
	req := parsePropfind(rawBody)
	depth := strings.TrimSpace(r.Header.Get("Depth"))
	if depth == "" {
		depth = "infinity"
	}	// Class-1 clients only need 0 and 1; infinity is folded to 1.
	if depth != "0" && depth != "1" && depth != "infinity" {
		http.Error(w, "bad depth", http.StatusBadRequest)
		return
	}

	f, err := h.store.Stat(userID, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Bare 404: some clients try to XML-parse any PROPFIND body,
			// and a text error here surfaces as a confusing client error.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeErr(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprint(w, `<?xml version="1.0" encoding="utf-8"?>`+"\n"+`<d:multistatus xmlns:d="DAV:">`)
	used, total, limited, _ := h.store.Quota(userID)
	writeResponse(w, href(dirName(name, f.IsDir)), f, req, used, total, limited)

	if (depth == "1" || depth == "infinity") && f.IsDir {
		children, err := h.store.ListDir(userID, name)
		if err != nil {
			fmt.Fprint(w, "\n</d:multistatus>")
			return
		}
		for _, child := range children {
			writeResponse(w, href(joinName(name, child.Name, child.IsDir)), child, req, used, total, limited)
		}
	}
	fmt.Fprint(w, "\n</d:multistatus>")
}

// dirName returns the virtual path with a trailing slash for collections.
func dirName(name string, isDir bool) string {
	if !isDir {
		return name
	}
	if !strings.HasSuffix(name, "/") {
		return name + "/"
	}
	return name
}

func joinName(parent, child string, isDir bool) string {
	return dirName(path.Join(parent, child), isDir)
}

// propRequest is the parsed PROPFIND body: either "everything" (empty,
// unparseable, allprop, or propname bodies) or an explicit property list.
type propRequest struct {
	all   bool
	names []string
}

type propfindChild struct {
	XMLName xml.Name
}

type propfindBody struct {
	XMLName xml.Name
	Allprop *struct{} `xml:"allprop"`
	Prop    *struct {
		Children []propfindChild `xml:",any"`
	} `xml:"prop"`
	Include *struct {
		Children []propfindChild `xml:",any"`
	} `xml:"include"`
	PropName *struct{} `xml:"propname"`
}

func parsePropfind(body []byte) propRequest {
	if len(bytes.TrimSpace(body)) == 0 {
		return propRequest{all: true}
	}
	var req propfindBody
	if err := xml.Unmarshal(body, &req); err != nil {
		return propRequest{all: true}
	}
	if req.Allprop != nil || req.PropName != nil {
		return propRequest{all: true}
	}
	var names []string
	seen := map[string]bool{}
	add := func(children []propfindChild) {
		for _, c := range children {
			local := c.XMLName.Local
			if local == "" || seen[local] {
				continue
			}
			seen[local] = true
			names = append(names, local)
		}
	}
	if req.Prop != nil {
		add(req.Prop.Children)
	}
	if req.Include != nil {
		add(req.Include.Children)
	}
	if len(names) == 0 {
		return propRequest{all: true}
	}
	return propRequest{names: names}
}

func writeResponse(w io.Writer, href string, f File, req propRequest, used, total int64, limited bool) {
	names := req.names
	if req.all {
		names = []string{
			"displayname", "resourcetype", "getcontenttype", "getcontentlength",
			"getetag", "getlastmodified", "creationdate", "supportedlock",
			"quota-used-bytes", "quota-available-bytes",
		}
	}
	var ok, missing []string
	for _, name := range names {
		if _, found := propValue(href, f, name, used, total, limited); found {
			ok = append(ok, name)
		} else {
			missing = append(missing, name)
		}
	}
	fmt.Fprintf(w, `
  <d:response>
    <d:href>%s</d:href>
    <d:propstat>
      <d:prop>`, escapeXML(href))
	for _, name := range ok {
		value, _ := propValue(href, f, name, used, total, limited)
		fmt.Fprintf(w, `
        <%s>%s</%s>`, davTag(name), value, davTag(name))
	}
	fmt.Fprint(w, `
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>`)
	if len(missing) > 0 {
		fmt.Fprint(w, `
    <d:propstat>
      <d:prop>`)
		for _, name := range missing {
			fmt.Fprintf(w, `
        <%s/>`, davTag(name))
		}
		fmt.Fprint(w, `
      </d:prop>
      <d:status>HTTP/1.1 404 Not Found</d:status>
    </d:propstat>`)
	}
	fmt.Fprint(w, `
  </d:response>`)
}

// davTag renders a property name with the DAV: prefix. Well-known CardDAV/
// CalDAV properties keep their own prefixes when requested.
func davTag(name string) string {
	if strings.Contains(name, ":") {
		return name
	}
	return "d:" + name
}

// propValue renders one property. A collection has no content length, so
// getcontentlength reports missing there per RFC 4918.
func propValue(href string, f File, name string, used, total int64, limited bool) (string, bool) {
	switch name {
	case "displayname":
		return escapeXML(displayName(href, f)), true
	case "resourcetype":
		if f.IsDir {
			return "<d:collection/>", true
		}
		return "", true
	case "getcontenttype":
		return escapeXML(f.ContentType()), true
	case "getcontentlength":
		if f.IsDir {
			return "", false
		}
		return fmt.Sprintf("%d", f.Size), true
	case "getetag":
		return escapeXML(f.ETag()), true
	case "getlastmodified":
		return f.ModTime.UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT"), true
	case "creationdate":
		return f.ModTime.UTC().Format("2006-01-02T15:04:05Z"), true
	case "supportedlock":
		return "", true
	case "quota-used-bytes":
		if !f.IsDir {
			return "", false
		}
		return fmt.Sprintf("%d", used), true
	case "quota-available-bytes":
		if !f.IsDir || !limited {
			return "", false
		}
		avail := total - used
		if avail < 0 {
			avail = 0
		}
		return fmt.Sprintf("%d", avail), true
	}
	return "", false
}

func displayName(href string, f File) string {
	if f.Name != "" {
		return f.Name
	}
	return strings.Trim(href, "/")
}

func escapeXML(s string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

func (h *handler) get(w http.ResponseWriter, r *http.Request, userID uuid.UUID, name string) {
	f, info, err := h.store.Open(userID, name)
	if err != nil {
		if errors.Is(err, ErrIsDir) {
			http.Error(w, "is a collection", http.StatusNotFound)
			return
		}
		writeErr(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("ETag", info.ETag())
	http.ServeContent(w, r, info.Name, info.ModTime, f)
}

func (h *handler) head(w http.ResponseWriter, r *http.Request, userID uuid.UUID, name string) {
	info, err := h.store.Stat(userID, name)
	if err != nil {
		writeErr(w, err)
		return
	}
	if info.IsDir {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", info.ContentType())
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size))
	w.Header().Set("ETag", info.ETag())
	w.Header().Set("Last-Modified", info.ModTime.UTC().Format(time.RFC1123))
	w.WriteHeader(http.StatusOK)
}

func (h *handler) put(w http.ResponseWriter, r *http.Request, userID uuid.UUID, name string) {
	if strings.HasSuffix(name, "/") && name != "" {
		http.Error(w, "cannot write a collection", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	limit := int64(1 << 62)
	if h.store.maxFileSize > 0 {
		limit = h.store.maxFileSize + 1
	}
	limited := io.LimitReader(r.Body, limit)
	existed := true
	if _, err := h.store.Stat(userID, name); err != nil {
		existed = false
	}
	if err := h.store.Write(userID, name, limited, r.ContentLength); err != nil {
		if err == ErrTooLarge {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		writeErr(w, err)
		return
	}
	if existed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request, userID uuid.UUID, name string) {
	if name == "" {
		http.Error(w, "cannot delete root", http.StatusForbidden)
		return
	}
	if err := h.store.Remove(userID, name); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) mkcol(w http.ResponseWriter, r *http.Request, userID uuid.UUID, name string) {
	if name == "" {
		http.Error(w, "already exists", http.StatusMethodNotAllowed)
		return
	}
	if r.ContentLength != 0 {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		if len(bytesTrim(body)) != 0 {
			http.Error(w, "extended MKCOL unsupported", http.StatusUnsupportedMediaType)
			return
		}
	}
	if err := h.store.Mkdir(userID, name); err != nil {
		if err == ErrExists {
			http.Error(w, "already exists", http.StatusMethodNotAllowed)
			return
		}
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

func (h *handler) move(w http.ResponseWriter, r *http.Request, userID uuid.UUID, name string) {
	dst, ok := h.destination(r)
	if !ok {
		http.Error(w, "bad destination", http.StatusBadRequest)
		return
	}
	overwrite := !strings.EqualFold(strings.TrimSpace(r.Header.Get("Overwrite")), "F")
	if err := h.store.Move(userID, name, dst, overwrite); err != nil {
		if err == ErrExists {
			http.Error(w, "destination exists", http.StatusPreconditionFailed)
			return
		}
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) copy(w http.ResponseWriter, r *http.Request, userID uuid.UUID, name string) {
	dst, ok := h.destination(r)
	if !ok {
		http.Error(w, "bad destination", http.StatusBadRequest)
		return
	}
	overwrite := !strings.EqualFold(strings.TrimSpace(r.Header.Get("Overwrite")), "F")
	if err := h.store.Copy(userID, name, dst, overwrite); err != nil {
		if err == ErrExists {
			http.Error(w, "destination exists", http.StatusPreconditionFailed)
			return
		}
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// destination resolves the Destination header to a virtual path. Only
// same-tree destinations are supported.
func (h *handler) destination(r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.Header.Get("Destination"))
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	p := u.Path
	if p == "" {
		p = raw
	}
	if !strings.HasPrefix(p, prefix) && p != "/files" {
		return "", false
	}
	dst := strings.TrimPrefix(p, prefix)
	if p == "/files" {
		dst = ""
	}
	if dst == "" {
		return "", false
	}
	return dst, true
}
