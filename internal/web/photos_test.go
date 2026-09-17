package web_test

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/web"
	"github.com/google/uuid"
)

type memPhotoAuth struct {
	user *identity.User
	err  error
}

func (m memPhotoAuth) Authenticate(context.Context, string, string) (*identity.User, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.user, nil
}

type fakeConverter struct {
	called int
	err    error
}

func (f *fakeConverter) Convert(src, dst string) error {
	f.called++
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(dst, []byte("fake-jpeg"), 0o644)
}

func (f *fakeConverter) Available() bool { return f.err == nil }

func photoTestServer(t *testing.T, userID uuid.UUID, store *memFiles, auth memPhotoAuth) *http.ServeMux {
	t.Helper()
	filesFS := os.DirFS("../..")
	server, err := web.New(filesFS, contactService{}, calendarService{}, &mailServiceStub{}, testSessionService{userID: userID}, testUserService{userID: userID}, false)
	if err != nil {
		t.Fatal(err)
	}
	server.SetPhotos(store, auth)
	server.SetPhotoLabels(newMemPhotoLabels())
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	return mux
}

type memPhotoLabels struct {
	mu     sync.Mutex
	labels map[string]string
}

func newMemPhotoLabels() *memPhotoLabels {
	return &memPhotoLabels{labels: map[string]string{}}
}

func (m *memPhotoLabels) Get(_ context.Context, _ uuid.UUID, path string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.labels[path], nil
}

func (m *memPhotoLabels) Set(_ context.Context, _ uuid.UUID, path, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if label == "" {
		delete(m.labels, path)
		return nil
	}
	m.labels[path] = label
	return nil
}

var errAuthTest = errors.New("bad credentials")

func photoCookies(req *http.Request) {
	req.AddCookie(&http.Cookie{Name: "session", Value: "valid-session"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "test-csrf-token"})
}

func TestGalleryEmptyAndUpload(t *testing.T) {
	userID := uuid.New()
	store := newMemFiles()
	mux := photoTestServer(t, userID, store, memPhotoAuth{})

	req := httptest.NewRequest(http.MethodGet, "/gallery", nil)
	photoCookies(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /gallery = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "No photos yet") {
		t.Errorf("empty state missing")
	}

	// Upload a photo via session+CSRF (gallery form path).
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("_csrf", "test-csrf-token")
	fw, _ := w.CreateFormFile("file", "beach.jpg")
	_, _ = fw.Write([]byte("fake-image-bytes"))
	_ = w.Close()
	req = httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("upload = %d. Body: %s", rec.Code, rec.Body.String())
	}

	// Gallery groups by month with the file rendered.
	month := time.Now().UTC().Format("2006-01")
	req = httptest.NewRequest(http.MethodGet, "/gallery", nil)
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, month) || !strings.Contains(body, "beach.jpg") {
		t.Errorf("gallery missing month/file")
	}

	// Non-media is rejected without failing siblings.
	var buf2 bytes.Buffer
	w2 := multipart.NewWriter(&buf2)
	_ = w2.WriteField("_csrf", "test-csrf-token")
	fw2, _ := w2.CreateFormFile("file", "notes.txt")
	_, _ = fw2.Write([]byte("not a photo"))
	_ = w2.Close()
	req = httptest.NewRequest(http.MethodPost, "/api/upload?format=json", &buf2)
	req.Header.Set("Content-Type", w2.FormDataContentType())
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("json upload = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "not a photo") {
		t.Errorf("rejection not reported: %s", body)
	}
}

func TestUploadBasicAuthJSON(t *testing.T) {
	userID := uuid.New()
	store := newMemFiles()
	user := &identity.User{ID: userID, Email: "photo@example.com"}
	mux := photoTestServer(t, userID, store, memPhotoAuth{user: user})

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("upload", "pic.png")
	_, _ = fw.Write([]byte("fake-png-bytes"))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/upload?format=json", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.SetBasicAuth("photo@example.com", "device-secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("basic upload = %d. Body: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "pic.png") || !strings.Contains(body, "uploaded") {
		t.Errorf("json response wrong: %s", body)
	}

	// Bad credentials are rejected.
	mux2 := photoTestServer(t, userID, store, memPhotoAuth{err: errAuthTest})
	req2 := httptest.NewRequest(http.MethodPost, "/api/upload?format=json", &buf)
	req2.Header.Set("Content-Type", w.FormDataContentType())
	req2.SetBasicAuth("photo@example.com", "wrong")
	rec2 := httptest.NewRecorder()
	mux2.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("bad auth = %d", rec2.Code)
	}
}

func TestGalleryDelete(t *testing.T) {
	userID := uuid.New()
	store := newMemFiles()
	mux := photoTestServer(t, userID, store, memPhotoAuth{})

	// Seed via stub directly, delete via the route.
	home := "test"
	_ = home
	store.files["2026-01/old.jpg"] = &memFile{data: []byte("x"), mod: time.Now()}
	form := url.Values{"_csrf": {"test-csrf-token"}, "path": {"2026-01/old.jpg"}}
	req := httptest.NewRequest(http.MethodPost, "/gallery/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	photoCookies(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rec.Code)
	}
	if _, ok := store.files["2026-01/old.jpg"]; ok {
		t.Errorf("file not deleted")
	}
}

func TestPhotoDetailAndLabel(t *testing.T) {
	userID := uuid.New()
	store := newMemFiles()
	mux := photoTestServer(t, userID, store, memPhotoAuth{})

	store.files["2026-09/beach.jpg"] = &memFile{data: []byte("fake-image-bytes"), mod: time.Now()}

	// Detail page renders the large view, metadata and label form.
	req := httptest.NewRequest(http.MethodGet, "/gallery/photo?path=2026-09%2Fbeach.jpg", nil)
	photoCookies(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET detail = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Download original", "Sharing", "Save label", "/gallery/file?path=2026-09%2fbeach.jpg"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail missing %q", want)
		}
	}

	// Missing file is a 404, not a 500.
	req = httptest.NewRequest(http.MethodGet, "/gallery/photo?path=2026-09%2Fnope.jpg", nil)
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing detail = %d, want 404", rec.Code)
	}

	// Save a label, see it on the detail page, clear it again.
	form := url.Values{"_csrf": {"test-csrf-token"}, "path": {"2026-09/beach.jpg"}, "label": {"Tatra sunrise"}}
	req = httptest.NewRequest(http.MethodPost, "/gallery/label", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("label save = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "/gallery/photo?path=") {
		t.Errorf("label redirect = %q", loc)
	}

	req = httptest.NewRequest(http.MethodGet, "/gallery/photo?path=2026-09%2Fbeach.jpg", nil)
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, "Tatra sunrise") {
		t.Errorf("saved label not shown")
	}

	form = url.Values{"_csrf": {"test-csrf-token"}, "path": {"2026-09/beach.jpg"}, "label": {""}}
	req = httptest.NewRequest(http.MethodPost, "/gallery/label", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("label clear = %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/gallery/photo?path=2026-09%2Fbeach.jpg", nil)
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if body := rec.Body.String(); strings.Contains(body, "Tatra sunrise") {
		t.Errorf("cleared label still shown")
	}
}
