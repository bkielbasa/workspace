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
	"sort"
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
	server.SetPhotoTags(newMemPhotoLabels())
	server.SetPhotoAlbums(newMemAlbums())
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

func (m *memPhotoLabels) Set(_ context.Context, _ uuid.UUID, path string, tags []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.labels {
		if strings.HasPrefix(k, path+"\x00") {
			delete(m.labels, k)
		}
	}
	for _, t := range tags {
		m.labels[path+"\x00"+t] = t
	}
	return nil
}

func (m *memPhotoLabels) ByPhoto(_ context.Context, _ uuid.UUID) (map[string][]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]string{}
	seen := map[string]bool{}
	for k, t := range m.labels {
		path := k[:strings.Index(k, "\x00")]
		if !seen[k] {
			seen[k] = true
			out[path] = append(out[path], t)
		}
	}
	for _, v := range out {
		sort.Strings(v)
	}
	return out, nil
}

func (m *memPhotoLabels) All(_ context.Context, _ uuid.UUID) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, t := range m.labels {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out, nil
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

func TestGalleryModalAndTags(t *testing.T) {
	userID := uuid.New()
	store := newMemFiles()
	mux := photoTestServer(t, userID, store, memPhotoAuth{})

	store.files["2026-09/beach.jpg"] = &memFile{data: []byte("fake-image-bytes"), mod: time.Now()}
	store.files["2026-09/dune.jpg"] = &memFile{data: []byte("fake-image-bytes"), mod: time.Now()}

	// Gallery carries the modal triggers plus the modal shell.
	req := httptest.NewRequest(http.MethodGet, "/gallery", nil)
	photoCookies(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /gallery = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="gallery-open"`, `data-path="2026-09/beach.jpg"`, `data-kind="image"`,
		`id="photo-modal"`, `data-tag-form`, `/static/js/gallery.js`, "Sharing",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("gallery missing %q", want)
		}
	}

	// Tag one photo; the same tag is suggested for the other.
	form := url.Values{"_csrf": {"test-csrf-token"}, "path": {"2026-09/beach.jpg"}, "tags": {"tatra, sunrise, Tatra "}}
	req = httptest.NewRequest(http.MethodPost, "/gallery/tags?format=json", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tag save = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"tags":["tatra","sunrise"]`) {
		t.Errorf("tag JSON normalizes poorly: %s", body)
	}

	// Gallery shows chips and suggests the tag everywhere.
	req = httptest.NewRequest(http.MethodGet, "/gallery", nil)
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body = rec.Body.String()
	for _, want := range []string{`data-tags="sunrise,tatra"`, `<option value="tatra">`, `<option value="sunrise">`} {
		if !strings.Contains(body, want) {
			t.Errorf("gallery missing %q", want)
		}
	}

	// Reuse the same tag on the second photo.
	form = url.Values{"_csrf": {"test-csrf-token"}, "path": {"2026-09/dune.jpg"}, "tags": {"tatra"}}
	req = httptest.NewRequest(http.MethodPost, "/gallery/tags?format=json", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tag reuse = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/gallery", nil)
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if body := rec.Body.String(); !strings.Contains(body, `data-path="2026-09/dune.jpg" data-name="dune.jpg" data-kind="image" data-tags="tatra"`) {
		t.Errorf("reused tag not rendered on second photo")
	}

	// Empty set clears.
	form = url.Values{"_csrf": {"test-csrf-token"}, "path": {"2026-09/beach.jpg"}, "tags": {""}}
	req = httptest.NewRequest(http.MethodPost, "/gallery/tags?format=json", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tag clear = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/gallery", nil)
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if body := rec.Body.String(); strings.Contains(body, `data-path="2026-09/beach.jpg" data-name="beach.jpg" data-kind="image" data-tags=""`) == false {
		t.Errorf("cleared tags still rendered")
	}
}

type memAlbum struct {
	id    uuid.UUID
	name  string
	items map[string]bool
}

type memAlbums struct {
	mu     sync.Mutex
	albums map[uuid.UUID]*memAlbum
}

func newMemAlbums() *memAlbums {
	return &memAlbums{albums: map[uuid.UUID]*memAlbum{}}
}

func (m *memAlbums) Create(_ context.Context, _ uuid.UUID, name string) (*identity.PhotoAlbum, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.albums {
		if a.name == name {
			return &identity.PhotoAlbum{ID: a.id, Name: a.name}, nil
		}
	}
	a := &memAlbum{id: uuid.New(), name: name, items: map[string]bool{}}
	m.albums[a.id] = a
	return &identity.PhotoAlbum{ID: a.id, Name: a.name}, nil
}

func (m *memAlbums) List(_ context.Context, _ uuid.UUID) ([]identity.PhotoAlbum, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []identity.PhotoAlbum
	for _, a := range m.albums {
		out = append(out, identity.PhotoAlbum{ID: a.id, Name: a.name, Count: len(a.items)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *memAlbums) Delete(_ context.Context, _ uuid.UUID, albumID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.albums, albumID)
	return nil
}

func (m *memAlbums) Add(_ context.Context, _ uuid.UUID, albumID uuid.UUID, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.albums[albumID]; ok {
		a.items[path] = true
	}
	return nil
}

func (m *memAlbums) Remove(_ context.Context, _ uuid.UUID, albumID uuid.UUID, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.albums[albumID]; ok {
		delete(a.items, path)
	}
	return nil
}

func (m *memAlbums) Paths(_ context.Context, _ uuid.UUID, albumID uuid.UUID) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	if a, ok := m.albums[albumID]; ok {
		for p := range a.items {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *memAlbums) Memberships(_ context.Context, _ uuid.UUID) (map[string][]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]uuid.UUID{}
	for _, a := range m.albums {
		for p := range a.items {
			out[p] = append(out[p], a.id)
		}
	}
	return out, nil
}

func TestGalleryAlbums(t *testing.T) {
	userID := uuid.New()
	store := newMemFiles()
	mux := photoTestServer(t, userID, store, memPhotoAuth{})

	store.files["2026-09/beach.jpg"] = &memFile{data: []byte("x"), mod: time.Now()}
	store.files["2026-09/dune.jpg"] = &memFile{data: []byte("x"), mod: time.Now()}

	post := func(target string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		photoCookies(req)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	get := func(target string) string {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		photoCookies(req)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", target, rec.Code)
		}
		return rec.Body.String()
	}

	// Create lands on the new album's filtered view.
	rec := post("/gallery/albums", url.Values{"_csrf": {"test-csrf-token"}, "name": {"Holidays"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("album create = %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/gallery?album=") {
		t.Fatalf("album redirect = %q", loc)
	}
	albumID := strings.TrimPrefix(loc, "/gallery?album=")

	// Sidebar lists the album; empty album shows the nothing-matches state.
	body := get("/gallery")
	for _, want := range []string{"Holidays", `name="name"`, "data-album-checks", "Tags"} {
		if !strings.Contains(body, want) {
			t.Errorf("gallery missing %q", want)
		}
	}
	if body := get(loc); !strings.Contains(body, "Nothing matches") {
		t.Errorf("empty album should show the nothing-matches state")
	}

	// Toggle the photo in via JSON, like the modal does.
	rec = post("/gallery/albums/toggle?format=json", url.Values{
		"_csrf": {"test-csrf-token"}, "album_id": {albumID}, "path": {"2026-09/beach.jpg"}, "add": {"1"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"in_album":true`) {
		t.Fatalf("album toggle = %d %s", rec.Code, rec.Body.String())
	}

	// Filtered view shows only the member; modal state carries the album.
	body = get(loc)
	if !strings.Contains(body, "beach.jpg") || strings.Contains(body, "dune.jpg") {
		t.Errorf("album filter shows wrong set")
	}
	if !strings.Contains(body, "data-albums=\""+albumID+`"`) {
		t.Errorf("modal album state missing")
	}

	// Toggle out again empties the album.
	rec = post("/gallery/albums/toggle?format=json", url.Values{
		"_csrf": {"test-csrf-token"}, "album_id": {albumID}, "path": {"2026-09/beach.jpg"}, "add": {"0"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"in_album":false`) {
		t.Fatalf("album untoggle = %d %s", rec.Code, rec.Body.String())
	}
	if body := get(loc); !strings.Contains(body, "Nothing matches") {
		t.Errorf("emptied album should show the nothing-matches state")
	}
}

func TestGalleryFragments(t *testing.T) {
	userID := uuid.New()
	store := newMemFiles()
	mux := photoTestServer(t, userID, store, memPhotoAuth{})

	store.files["2026-09/beach.jpg"] = &memFile{data: []byte("x"), mod: time.Now()}

	hxGet := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("HX-Request", "true")
		photoCookies(req)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// Content fragment: grid yes, full-page chrome no.
	rec := hxGet("/gallery/content")
	if rec.Code != http.StatusOK {
		t.Fatalf("content = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="gallery-content"`) || !strings.Contains(body, "beach.jpg") {
		t.Errorf("fragment missing grid")
	}
	for _, chrome := range []string{"nav-brand", "<html", "photo-modal"} {
		if strings.Contains(body, chrome) {
			t.Errorf("fragment leaks full-page chrome %q", chrome)
		}
	}

	// HX album create returns the fragment with the push URL, no redirect.
	form := url.Values{"_csrf": {"test-csrf-token"}, "name": {"HX Album"}}
	req := httptest.NewRequest(http.MethodPost, "/gallery/albums", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("hx create = %d", rec.Code)
	}
	if push := rec.Header().Get("HX-Push-Url"); !strings.HasPrefix(push, "/gallery?album=") {
		t.Errorf("hx create push = %q", push)
	}
	if !strings.Contains(rec.Body.String(), "HX Album") {
		t.Errorf("hx create fragment missing the album")
	}

	// HX upload returns the fragment showing the new photo, no redirect.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("_csrf", "test-csrf-token")
	fw, _ := w.CreateFormFile("files", "hx.jpg")
	_, _ = fw.Write([]byte("fake-image-bytes"))
	_ = w.Close()
	req = httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	photoCookies(req)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("hx upload = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "hx.jpg") || !strings.Contains(body, `id="gallery-content"`) {
		t.Errorf("hx upload fragment missing the photo")
	}
}

func TestGalleryShowsNestedUploads(t *testing.T) {
	userID := uuid.New()
	store := newMemFiles()
	mux := photoTestServer(t, userID, store, memPhotoAuth{})

	// PhotoSync-style nesting plus a preview cache that must stay hidden.
	store.files["2026-09/Telefon Bartka/Favorites/IMG_6980.JPG"] = &memFile{data: []byte("x"), mod: time.Now()}
	store.files["2026-09/2026-09/.previews/IMG_6980.jpg"] = &memFile{data: []byte("x"), mod: time.Now()}

	req := httptest.NewRequest(http.MethodGet, "/gallery", nil)
	photoCookies(req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /gallery = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "IMG_6980.JPG") {
		t.Errorf("nested upload not shown")
	}
	if strings.Contains(body, ".previews") {
		t.Errorf("preview cache leaked into the gallery")
	}
}
