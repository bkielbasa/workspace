package web_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/files"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

type memFile struct {
	isDir bool
	data  []byte
	mod   time.Time
}

type memFiles struct {
	files map[string]*memFile
}

func newMemFiles() *memFiles {
	return &memFiles{files: map[string]*memFile{"": {isDir: true, mod: time.Now()}}}
}

func (m *memFiles) EnsureUserRoot(_ string) error { return nil }

func (m *memFiles) Stat(_ string, name string) (files.File, error) {
	f, ok := m.files[name]
	if !ok {
		// Implicit parents exist when anything lives beneath them.
		for p := range m.files {
			if strings.HasPrefix(p, name+"/") {
				return files.File{Name: baseName(name), IsDir: true, ModTime: time.Now()}, nil
			}
		}
		return files.File{}, files.ErrNotFound
	}
	return files.File{Name: baseName(name), IsDir: f.isDir, Size: int64(len(f.data)), ModTime: f.mod}, nil
}

func baseName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func (m *memFiles) ListDir(_ string, name string) ([]files.File, error) {
	if dir, ok := m.files[name]; ok && !dir.isDir {
		return nil, files.ErrNotFound
	}
	known := false
	if _, ok := m.files[name]; ok {
		known = true
	} else {
		for p := range m.files {
			if strings.HasPrefix(p, name+"/") {
				known = true
				break
			}
		}
	}
	if !known && name != "" {
		return nil, files.ErrNotFound
	}
	var out []files.File
	prefix := name
	if prefix != "" {
		prefix += "/"
	}
	seen := map[string]bool{}
	for p := range m.files {
		if !strings.HasPrefix(p, prefix) || p == name {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		top := rest
		if i := strings.Index(rest, "/"); i >= 0 {
			top = rest[:i]
		}
		if seen[top] {
			continue
		}
		seen[top] = true
		full := prefix + top
		fullFile := m.files[full]
		var size int64
		isDir := true
		if fullFile != nil {
			isDir = fullFile.isDir
			size = int64(len(fullFile.data))
		}
		out = append(out, files.File{Name: top, IsDir: isDir, Size: size, ModTime: time.Now()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

type memReadCloser struct {
	*bytes.Reader
}

func (m memReadCloser) Close() error { return nil }

func (m *memFiles) Open(_ string, name string) (io.ReadSeekCloser, files.File, error) {
	f, ok := m.files[name]
	if !ok {
		return nil, files.File{}, files.ErrNotFound
	}
	if f.isDir {
		return nil, files.File{}, files.ErrIsDir
	}
	info, _ := m.Stat("", name)
	return memReadCloser{bytes.NewReader(f.data)}, info, nil
}

func (m *memFiles) Write(_ string, name string, data io.Reader, _ int64) error {
	b, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	m.files[name] = &memFile{data: b, mod: time.Now()}
	return nil
}

func (m *memFiles) Mkdir(_ string, name string) error {
	if _, ok := m.files[name]; ok {
		return files.ErrExists
	}
	parent := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		parent = name[:i]
	} else {
		parent = ""
	}
	if _, ok := m.files[parent]; !ok {
		return files.ErrConflict
	}
	m.files[name] = &memFile{isDir: true, mod: time.Now()}
	return nil
}

func (m *memFiles) Move(_ string, from, to string, overwrite bool) error {
	f, ok := m.files[from]
	if !ok {
		return files.ErrNotFound
	}
	if _, exists := m.files[to]; exists && !overwrite {
		return files.ErrExists
	}
	m.files[to] = f
	delete(m.files, from)
	for p, child := range m.files {
		if strings.HasPrefix(p, from+"/") {
			m.files[to+strings.TrimPrefix(p, from)] = child
			delete(m.files, p)
		}
	}
	return nil
}

func (m *memFiles) Remove(_ string, name string) error {
	if _, ok := m.files[name]; !ok {
		return files.ErrNotFound
	}
	for p := range m.files {
		if p == name || strings.HasPrefix(p, name+"/") {
			delete(m.files, p)
		}
	}
	return nil
}

func driveTestServer(t *testing.T, userID uuid.UUID, store *memFiles) (*http.ServeMux, string) {
	t.Helper()
	filesFS := os.DirFS("../..")
	server, err := web.New(filesFS, contactService{}, calendarService{}, &mailServiceStub{}, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetFiles(store)
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	return mux, "valid-session"
}

func drivePost(t *testing.T, mux *http.ServeMux, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf-token"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func driveGet(t *testing.T, mux *http.ServeMux, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestDriveEmptyAndMkdir(t *testing.T) {
	userID := uuid.New()
	store := newMemFiles()
	mux, _ := driveTestServer(t, userID, store)

	rec := driveGet(t, mux, "/drive")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /drive = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "This folder is empty") || !strings.Contains(body, "Upload") {
		t.Errorf("empty state broken")
	}

	form := url.Values{"_csrf": {"test-csrf-token"}, "path": {""}, "name": {"docs"}}
	if rec := drivePost(t, mux, "/drive/mkdir", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("mkdir = %d", rec.Code)
	}
	rec = driveGet(t, mux, "/drive")
	if body := rec.Body.String(); !strings.Contains(body, "docs") {
		t.Errorf("folder missing from listing")
	}

	// Missing parent is an error redirect, not a crash.
	form.Set("name", "nope/deep")
	if rec := drivePost(t, mux, "/drive/mkdir", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("deep mkdir = %d", rec.Code)
	} else if loc := rec.Header().Get("Location"); !strings.Contains(loc, "error=mkdir") {
		t.Errorf("expected mkdir error redirect, got %q", loc)
	}
}

func TestDriveUploadDownloadDelete(t *testing.T) {
	userID := uuid.New()
	store := newMemFiles()
	mux, _ := driveTestServer(t, userID, store)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("_csrf", "test-csrf-token")
	_ = w.WriteField("path", "docs")
	fw, err := w.CreateFormFile("files", "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("drive contents"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/drive/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf-token"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload = %d. Body: %s", rec.Code, rec.Body.String())
	}

	rec = driveGet(t, mux, "/drive/download?path=docs/hello.txt")
	if rec.Code != http.StatusOK || rec.Body.String() != "drive contents" {
		t.Fatalf("download = %d %q", rec.Code, rec.Body.String())
	}

	// Breadcrumb + file row render.
	rec = driveGet(t, mux, "/drive?path=docs")
	if body := rec.Body.String(); !strings.Contains(body, "hello.txt") || !strings.Contains(body, "Files") {
		t.Errorf("listing broken")
	}

	// Rename via move.
	form := url.Values{"_csrf": {"test-csrf-token"}, "path": {"docs/hello.txt"}, "new_name": {"hi.txt"}}
	if rec := drivePost(t, mux, "/drive/rename", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("rename = %d", rec.Code)
	}
	rec = driveGet(t, mux, "/drive/download?path=docs/hi.txt")
	if rec.Body.String() != "drive contents" {
		t.Errorf("renamed download = %q", rec.Body.String())
	}

	// Delete back to empty.
	form = url.Values{"_csrf": {"test-csrf-token"}, "path": {"docs/hi.txt"}}
	if rec := drivePost(t, mux, "/drive/delete", form); rec.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rec.Code)
	}
	rec = driveGet(t, mux, "/drive/download?path=docs/hi.txt")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted download = %d", rec.Code)
	}
}

func TestDriveTraversalSafe(t *testing.T) {
	userID := uuid.New()
	store := newMemFiles()
	mux, _ := driveTestServer(t, userID, store)

	rec := driveGet(t, mux, "/drive?path=../..")
	if rec.Code != http.StatusOK {
		t.Fatalf("traversal = %d, want root listing", rec.Code)
	}
	form := url.Values{"_csrf": {"test-csrf-token"}, "path": {"docs/../../evil.txt"}}
	_ = drivePost(t, mux, "/drive/delete", form)
	if _, ok := store.files["evil.txt"]; ok {
		t.Errorf("traversal delete escaped")
	}
}
