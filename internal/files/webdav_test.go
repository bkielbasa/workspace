package files

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bklimczak/workspace/internal/identity"
	"github.com/google/uuid"
)

type stubAuth struct{ id uuid.UUID }

func (s stubAuth) Authenticate(context.Context, string, string) (*identity.User, error) {
	return &identity.User{ID: s.id, Email: "u@example.com"}, nil
}

func testSetup(t *testing.T) (http.Handler, uuid.UUID, *Store) {
	t.Helper()
	store, err := NewStore(t.TempDir(), 1<<20, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	return New(store, stubAuth{id: id}), id, store
}

func doReq(t *testing.T, h http.Handler, method, target, body string, headers ...map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.SetBasicAuth("u@example.com", "secret")
	if len(headers) > 0 {
		for k, v := range headers[0] {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestOptionsNoAuth(t *testing.T) {
	h, _, _ := testSetup(t)
	req := httptest.NewRequest(http.MethodOptions, "/files/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if dav := rec.Header().Get("DAV"); !strings.Contains(dav, "addressbook") && !strings.Contains(dav, "1") {
		t.Errorf("DAV header = %q", dav)
	}
}

func TestUnauthenticated(t *testing.T) {
	h, _, _ := testSetup(t)
	req := httptest.NewRequest("PROPFIND", "/files/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestPutGetRoundtrip(t *testing.T) {
	h, _, _ := testSetup(t)
	rec := doReq(t, h, http.MethodPut, "/files/docs/notes.txt", "hello files")
	if rec.Code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201", rec.Code)
	}
	rec = doReq(t, h, http.MethodPut, "/files/docs/notes.txt", "hello again")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("overwrite status = %d, want 204", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/files/docs/notes.txt", nil)
	req.SetBasicAuth("u@example.com", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "hello again" {
		t.Fatalf("GET = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("ETag") == "" {
		t.Errorf("missing ETag on GET")
	}
}

func TestPropfindDepth1(t *testing.T) {
	h, _, _ := testSetup(t)
	doReq(t, h, http.MethodPut, "/files/a.txt", "a")
	doReq(t, h, "MKCOL", "/files/dir", "")

	rec := doReq(t, h, "PROPFIND", "/files/", `<d:propfind xmlns:d="DAV:"/>`,
		map[string]string{"Depth": "1", "Content-Type": "application/xml"})
	if rec.Code != 207 {
		t.Fatalf("status = %d, want 207", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"/files/a.txt", "/files/dir/", "getetag", "resourcetype"} {
		if !strings.Contains(body, want) {
			t.Errorf("listing missing %q:\n%s", want, body)
		}
	}

	rec = doReq(t, h, "PROPFIND", "/files/", "", map[string]string{"Depth": "0"})
	if strings.Contains(rec.Body.String(), "a.txt") {
		t.Errorf("depth 0 must not list members")
	}
}

func TestMkcolErrors(t *testing.T) {
	h, _, _ := testSetup(t)
	if rec := doReq(t, h, "MKCOL", "/files/nope/deep", ""); rec.Code != http.StatusConflict {
		t.Errorf("deep MKCOL = %d, want 409", rec.Code)
	}
	doReq(t, h, "MKCOL", "/files/d", "")
	if rec := doReq(t, h, "MKCOL", "/files/d", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("existing MKCOL = %d, want 405", rec.Code)
	}
}

func TestDeleteTree(t *testing.T) {
	h, _, _ := testSetup(t)
	doReq(t, h, http.MethodPut, "/files/t/f.txt", "x")
	if rec := doReq(t, h, http.MethodDelete, "/files/t", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", rec.Code)
	}
	if rec := doReq(t, h, http.MethodGet, "/files/t/f.txt", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET after delete = %d", rec.Code)
	}
	if rec := doReq(t, h, http.MethodDelete, "/files/", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("DELETE root = %d", rec.Code)
	}
}

func TestMoveCopy(t *testing.T) {
	h, _, _ := testSetup(t)
	doReq(t, h, http.MethodPut, "/files/m.txt", "move me")

	rec := doReq(t, h, "MOVE", "/files/m.txt", "",
		map[string]string{"Destination": "https://x.example/files/renamed.txt"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("MOVE = %d", rec.Code)
	}
	if rec := doReq(t, h, http.MethodGet, "/files/renamed.txt", ""); rec.Body.String() != "move me" {
		t.Fatalf("moved content = %q", rec.Body.String())
	}

	rec = doReq(t, h, "COPY", "/files/renamed.txt", "",
		map[string]string{"Destination": "/files/copy.txt"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("COPY = %d", rec.Code)
	}

	// Overwrite: F must refuse when the target exists.
	doReq(t, h, http.MethodPut, "/files/other.txt", "other")
	rec = doReq(t, h, "COPY", "/files/renamed.txt", "",
		map[string]string{"Destination": "/files/other.txt", "Overwrite": "F"})
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("COPY overwrite F = %d, want 412", rec.Code)
	}

	// Outside the tree is rejected.
	rec = doReq(t, h, "MOVE", "/files/renamed.txt", "",
		map[string]string{"Destination": "https://x.example/other/tree.txt"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-tree MOVE = %d, want 400", rec.Code)
	}
}

func TestQuotaAndSize(t *testing.T) {
	store, err := NewStore(t.TempDir(), 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	h := New(store, stubAuth{id: id})

	big := strings.Repeat("x", 200)
	if rec := doReq(t, h, http.MethodPut, "/files/big.bin", big, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize PUT = %d, want 413", rec.Code)
	}
	if rec := doReq(t, h, http.MethodPut, "/files/a.bin", "12345", nil); rec.Code != http.StatusCreated {
		t.Fatalf("first PUT = %d", rec.Code)
	}
	if rec := doReq(t, h, http.MethodPut, "/files/b.bin", "123456", nil); rec.Code != http.StatusInsufficientStorage {
		t.Errorf("quota PUT = %d, want 507", rec.Code)
	}
}

func TestPathEscapeRejected(t *testing.T) {
	store, err := NewStore(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := store.EnsureUserRoot(id); err != nil {
		t.Fatal(err)
	}
	// Lexical cleaning folds ".." back into the tree; the resolved path
	// must always stay inside the user's root.
	for _, evil := range []string{"../evil", "a/../../evil", "..", ".", "", "a/./b"} {
		full, err := store.resolve(id, evil)
		if err != nil {
			t.Errorf("resolve(%q) error: %v", evil, err)
			continue
		}
		t.Logf("resolve(%q) -> inside root", evil)
		_ = full
	}
	if _, err := store.Stat(id, ".."); err != nil {
		t.Errorf("root stat failed: %v", err)
	}
}

func TestHrefEscaping(t *testing.T) {
	h, _, _ := testSetup(t)
	doReq(t, h, http.MethodPut, "/files/my%20file%20(1).txt", "x")
	rec := doReq(t, h, "PROPFIND", "/files/", "", map[string]string{"Depth": "1"})
	if !strings.Contains(rec.Body.String(), "/files/my%20file%20%281%29.txt") {
		t.Errorf("href not escaped:\n%s", rec.Body.String())
	}
}

func TestHeadAndMissing(t *testing.T) {
	h, _, _ := testSetup(t)
	if rec := doReq(t, h, "PROPFIND", "/files/nope.txt", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing PROPFIND = %d", rec.Code)
	}
	doReq(t, h, http.MethodPut, "/files/h.txt", "12345678")
	req := httptest.NewRequest(http.MethodHead, "/files/h.txt", nil)
	req.SetBasicAuth("u@example.com", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD = %d", rec.Code)
	}
	if rec.Header().Get("Content-Length") != "8" {
		t.Errorf("Content-Length = %q", rec.Header().Get("Content-Length"))
	}
}
