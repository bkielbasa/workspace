package caldav

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/format/vtodo"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

type dummyAuth struct {
	user *identity.User
}

func (d *dummyAuth) Authenticate(ctx context.Context, email, password string) (*identity.User, error) {
	if email == d.user.Email && password == "secret" {
		return d.user, nil
	}
	return nil, fmt.Errorf("invalid auth")
}

type dummyCalendar struct{}

func (d *dummyCalendar) List(ctx context.Context, userID uuid.UUID) ([]calendar.Event, error) {
	return nil, nil
}
func (d *dummyCalendar) Put(ctx context.Context, event calendar.Event) (*calendar.Event, error) {
	return &event, nil
}
func (d *dummyCalendar) GetByResource(ctx context.Context, userID uuid.UUID, resource string) (*calendar.Event, error) {
	return nil, nil
}
func (d *dummyCalendar) DeleteByResource(ctx context.Context, userID uuid.UUID, resource string) error {
	return nil
}

type mockNotesService struct {
	notes map[uuid.UUID]*notes.Note
}

func newMockNotesService() *mockNotesService {
	return &mockNotesService{
		notes: make(map[uuid.UUID]*notes.Note),
	}
}

func (m *mockNotesService) ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]notes.Note, error) {
	var result []notes.Note
	for _, n := range m.notes {
		if n.IsArchived == archived {
			result = append(result, *n)
		}
	}
	return result, nil
}

func (m *mockNotesService) GetNote(ctx context.Context, userID uuid.UUID, id uuid.UUID) (*notes.Note, error) {
	n, ok := m.notes[id]
	if !ok {
		return nil, fmt.Errorf("note not found: %s", id)
	}
	cp := *n
	cp.Items = make([]notes.NoteItem, len(n.Items))
	copy(cp.Items, n.Items)
	return &cp, nil
}

func (m *mockNotesService) AddItem(ctx context.Context, userID uuid.UUID, noteID uuid.UUID, content string) (*notes.NoteItem, error) {
	return m.AddItemWithID(ctx, userID, noteID, uuid.Nil, content)
}

func (m *mockNotesService) AddItemWithID(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, content string) (*notes.NoteItem, error) {
	n, ok := m.notes[noteID]
	if !ok {
		return nil, fmt.Errorf("note not found: %s", noteID)
	}
	now := time.Now().UTC()
	if itemID == uuid.Nil {
		itemID = uuid.New()
	}
	item := notes.NoteItem{
		ID:        itemID,
		NoteID:    noteID,
		Content:   content,
		Completed: false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	n.Items = append(n.Items, item)
	n.UpdatedAt = now
	return &item, nil
}

func (m *mockNotesService) UpdateItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, content string, completed bool) (*notes.NoteItem, error) {
	n, ok := m.notes[noteID]
	if !ok {
		return nil, fmt.Errorf("note not found: %s", noteID)
	}
	for i := range n.Items {
		if n.Items[i].ID == itemID {
			now := time.Now().UTC()
			n.Items[i].Content = content
			n.Items[i].Completed = completed
			if completed {
				n.Items[i].CompletedAt = &now
			} else {
				n.Items[i].CompletedAt = nil
			}
			n.Items[i].UpdatedAt = now
			n.UpdatedAt = now
			return &n.Items[i], nil
		}
	}
	return nil, fmt.Errorf("item not found: %s", itemID)
}

func (m *mockNotesService) ToggleItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, completed bool) (*notes.NoteItem, error) {
	n, ok := m.notes[noteID]
	if !ok {
		return nil, fmt.Errorf("note not found: %s", noteID)
	}
	for i := range n.Items {
		if n.Items[i].ID == itemID {
			now := time.Now().UTC()
			n.Items[i].Completed = completed
			if completed {
				n.Items[i].CompletedAt = &now
			} else {
				n.Items[i].CompletedAt = nil
			}
			n.Items[i].UpdatedAt = now
			n.UpdatedAt = now
			return &n.Items[i], nil
		}
	}
	return nil, fmt.Errorf("item not found: %s", itemID)
}

func (m *mockNotesService) DeleteItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID) error {
	n, ok := m.notes[noteID]
	if !ok {
		return fmt.Errorf("note not found: %s", noteID)
	}
	idx := -1
	for i := range n.Items {
		if n.Items[i].ID == itemID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("item not found: %s", itemID)
	}
	n.Items = append(n.Items[:idx], n.Items[idx+1:]...)
	n.UpdatedAt = time.Now().UTC()
	return nil
}

func setupTestEnv() (*identity.User, *mockNotesService, http.Handler) {
	testUser := &identity.User{
		ID:    uuid.New(),
		Email: "alice@example.com",
	}
	auth := &dummyAuth{user: testUser}
	notesMock := newMockNotesService()
	h := NewWithTasks(&dummyCalendar{}, notesMock, auth)
	return testUser, notesMock, h
}

func TestCalDAVListsPROPFIND(t *testing.T) {
	_, _, h := setupTestEnv()

	req := httptest.NewRequest("PROPFIND", "/cal/", strings.NewReader(`<?xml version="1.0" encoding="utf-8" ?><D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/></D:prop></D:propfind>`))
	req.SetBasicAuth("alice@example.com", "secret")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)
	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 MultiStatus, got %d", w.Code)
	}
}

func TestCalDAVListsDiscovery(t *testing.T) {
	user, notesMock, h := setupTestEnv()

	listID := uuid.New()
	noteID := uuid.New()
	now := time.Now().UTC()

	notesMock.notes[listID] = &notes.Note{
		ID:        listID,
		UserID:    user.ID,
		Title:     "Groceries",
		Kind:      notes.KindList,
		CreatedAt: now,
		UpdatedAt: now,
	}
	notesMock.notes[noteID] = &notes.Note{
		ID:        noteID,
		UserID:    user.ID,
		Title:     "Work Ideas",
		Kind:      notes.KindNote, // should not be listed as task collection
		CreatedAt: now,
		UpdatedAt: now,
	}

	// 1. PROPFIND on /cal/
	req := httptest.NewRequest("PROPFIND", "/cal/", nil)
	req.SetBasicAuth("alice@example.com", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, fmt.Sprintf("/cal/lists/%s/", listID)) {
		t.Fatalf("expected calendar-home-set to contain list %s, body:\n%s", listID, body)
	}
	if strings.Contains(body, fmt.Sprintf("/cal/lists/%s/", noteID)) {
		t.Fatalf("calendar-home-set should not contain text note %s", noteID)
	}

	// 2. PROPFIND on /cal/{user}/
	req = httptest.NewRequest("PROPFIND", fmt.Sprintf("/cal/%s/", user.ID), nil)
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", w.Code)
	}
	body = w.Body.String()
	if !strings.Contains(body, fmt.Sprintf("/cal/lists/%s/", listID)) {
		t.Fatalf("expected calendar-home-set to contain list %s", listID)
	}

	// 3. PROPFIND on /cal/lists/ with Depth: 1
	req = httptest.NewRequest("PROPFIND", "/cal/lists/", nil)
	req.Header.Set("Depth", "1")
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", w.Code)
	}
	body = w.Body.String()
	if !strings.Contains(body, fmt.Sprintf("/cal/lists/%s/", listID)) {
		t.Fatalf("expected /cal/lists/ to contain list %s", listID)
	}
	if !strings.Contains(body, `<C:comp name="VTODO"/>`) {
		t.Fatalf("expected supported-calendar-component-set with VTODO, got:\n%s", body)
	}
	if !strings.Contains(body, "Groceries") {
		t.Fatalf("expected displayname Groceries, got:\n%s", body)
	}
}

func TestCalDAVListCollectionPROPFIND(t *testing.T) {
	user, notesMock, h := setupTestEnv()

	listID := uuid.New()
	itemID := uuid.New()
	now := time.Now().UTC()

	notesMock.notes[listID] = &notes.Note{
		ID:        listID,
		UserID:    user.ID,
		Title:     "Groceries",
		Kind:      notes.KindList,
		CreatedAt: now,
		UpdatedAt: now,
		Items: []notes.NoteItem{
			{
				ID:        itemID,
				NoteID:    listID,
				Content:   "Milk",
				Completed: false,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	// Depth: 0 on /cal/lists/{listID}/
	req := httptest.NewRequest("PROPFIND", fmt.Sprintf("/cal/lists/%s/", listID), nil)
	req.Header.Set("Depth", "0")
	req.SetBasicAuth("alice@example.com", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<C:comp name="VTODO"/>`) {
		t.Fatalf("expected VTODO component set in body:\n%s", body)
	}
	if !strings.Contains(body, "<cs:getctag>") {
		t.Fatalf("expected getctag in body:\n%s", body)
	}
	if strings.Contains(body, itemID.String()) {
		t.Fatalf("Depth: 0 should not list child items")
	}

	// Depth: 1 on /cal/lists/{listID}/
	req = httptest.NewRequest("PROPFIND", fmt.Sprintf("/cal/lists/%s/", listID), nil)
	req.Header.Set("Depth", "1")
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", w.Code)
	}
	body = w.Body.String()
	if !strings.Contains(body, fmt.Sprintf("/cal/lists/%s/%s.ics", listID, itemID)) {
		t.Fatalf("Depth: 1 should list item %s, body:\n%s", itemID, body)
	}
	if !strings.Contains(body, "<d:getetag>") {
		t.Fatalf("expected getetag in body:\n%s", body)
	}

	// PROPFIND on item directly
	req = httptest.NewRequest("PROPFIND", fmt.Sprintf("/cal/lists/%s/%s.ics", listID, itemID), nil)
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", w.Code)
	}

	// PROPFIND on nonexistent list
	req = httptest.NewRequest("PROPFIND", fmt.Sprintf("/cal/lists/%s/", uuid.New()), nil)
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCalDAVListCollectionREPORT(t *testing.T) {
	user, notesMock, h := setupTestEnv()

	listID := uuid.New()
	itemID := uuid.New()
	now := time.Now().UTC()

	notesMock.notes[listID] = &notes.Note{
		ID:        listID,
		UserID:    user.ID,
		Title:     "Groceries",
		Kind:      notes.KindList,
		CreatedAt: now,
		UpdatedAt: now,
		Items: []notes.NoteItem{
			{
				ID:        itemID,
				NoteID:    listID,
				Content:   "Bread",
				Completed: false,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	// 1. calendar-multiget
	multigetXML := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" ?>
<C:calendar-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop>
    <D:getetag/>
    <C:calendar-data/>
  </D:prop>
  <D:href>/cal/lists/%s/%s.ics</D:href>
</C:calendar-multiget>`, listID, itemID)

	req := httptest.NewRequest("REPORT", fmt.Sprintf("/cal/lists/%s/", listID), strings.NewReader(multigetXML))
	req.SetBasicAuth("alice@example.com", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "BEGIN:VTODO") || !strings.Contains(body, "SUMMARY:Bread") {
		t.Fatalf("expected VTODO with Bread in multiget report, got:\n%s", body)
	}

	// 2. calendar-query
	queryXML := `<?xml version="1.0" encoding="utf-8" ?>
<C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
  <D:prop>
    <D:getetag/>
    <C:calendar-data/>
  </D:prop>
</C:calendar-query>`

	req = httptest.NewRequest("REPORT", fmt.Sprintf("/cal/lists/%s/", listID), strings.NewReader(queryXML))
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", w.Code)
	}
	body = w.Body.String()
	if !strings.Contains(body, "SUMMARY:Bread") {
		t.Fatalf("expected SUMMARY:Bread in calendar-query report, got:\n%s", body)
	}

	// 3. sync-collection
	syncXML := `<?xml version="1.0" encoding="utf-8" ?>
<D:sync-collection xmlns:D="DAV:">
  <D:sync-token>0</D:sync-token>
  <D:prop>
    <D:getetag/>
  </D:prop>
</D:sync-collection>`

	req = httptest.NewRequest("REPORT", fmt.Sprintf("/cal/lists/%s/", listID), strings.NewReader(syncXML))
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", w.Code)
	}
	body = w.Body.String()
	if !strings.Contains(body, "<d:sync-token>") {
		t.Fatalf("expected sync-token in sync-collection response, got:\n%s", body)
	}
}

func TestCalDAVListItemGET(t *testing.T) {
	user, notesMock, h := setupTestEnv()

	listID := uuid.New()
	itemID := uuid.New()
	now := time.Now().UTC()

	notesMock.notes[listID] = &notes.Note{
		ID:        listID,
		UserID:    user.ID,
		Title:     "Groceries",
		Kind:      notes.KindList,
		CreatedAt: now,
		UpdatedAt: now,
		Items: []notes.NoteItem{
			{
				ID:        itemID,
				NoteID:    listID,
				Content:   "Eggs",
				Completed: false,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	// GET existing item
	req := httptest.NewRequest("GET", fmt.Sprintf("/cal/lists/%s/%s.ics", listID, itemID), nil)
	req.SetBasicAuth("alice@example.com", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/calendar") {
		t.Fatalf("expected text/calendar Content-Type, got %s", ct)
	}
	if etag := w.Header().Get("ETag"); etag == "" {
		t.Fatalf("expected ETag header")
	}
	body := w.Body.String()
	if !strings.Contains(body, "BEGIN:VTODO") || !strings.Contains(body, "SUMMARY:Eggs") {
		t.Fatalf("expected VTODO with Eggs, got:\n%s", body)
	}

	// GET nonexistent item
	req = httptest.NewRequest("GET", fmt.Sprintf("/cal/lists/%s/%s.ics", listID, uuid.New()), nil)
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}

	// GET on nonexistent list
	req = httptest.NewRequest("GET", fmt.Sprintf("/cal/lists/%s/%s.ics", uuid.New(), itemID), nil)
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestCalDAVListItemPUT(t *testing.T) {
	user, notesMock, h := setupTestEnv()

	listID := uuid.New()
	now := time.Now().UTC()

	notesMock.notes[listID] = &notes.Note{
		ID:        listID,
		UserID:    user.ID,
		Title:     "Groceries",
		Kind:      notes.KindList,
		CreatedAt: now,
		UpdatedAt: now,
	}

	newItemID := uuid.New()
	vtodoItem := notes.NoteItem{
		ID:        newItemID,
		NoteID:    listID,
		Content:   "Tomatoes",
		Completed: false,
	}
	icsContent := vtodo.Format(vtodoItem)

	// 1. Create item via PUT
	req := httptest.NewRequest("PUT", fmt.Sprintf("/cal/lists/%s/%s.ics", listID, newItemID), strings.NewReader(icsContent))
	req.SetBasicAuth("alice@example.com", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}
	if etag := w.Header().Get("ETag"); etag == "" {
		t.Fatalf("expected ETag header on creation")
	}

	note := notesMock.notes[listID]
	if len(note.Items) != 1 || note.Items[0].Content != "Tomatoes" {
		t.Fatalf("expected 1 item with Tomatoes, got %+v", note.Items)
	}
	if note.Items[0].ID != newItemID {
		t.Fatalf("expected item ID to match client-supplied UUID %s, got %s", newItemID, note.Items[0].ID)
	}

	// 1b. GET newly created item directly using client resource URI
	getReq := httptest.NewRequest("GET", fmt.Sprintf("/cal/lists/%s/%s.ics", listID, newItemID), nil)
	getReq.SetBasicAuth("alice@example.com", "secret")
	getW := httptest.NewRecorder()
	h.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200 OK fetching newly created item, got %d", getW.Code)
	}
	if !strings.Contains(getW.Body.String(), "SUMMARY:Tomatoes") {
		t.Fatalf("expected SUMMARY:Tomatoes in GET response, got: %s", getW.Body.String())
	}

	// 2. Update item content AND completion status via PUT
	updatedItem := notes.NoteItem{
		ID:        newItemID,
		NoteID:    listID,
		Content:   "Organic Tomatoes",
		Completed: true,
	}
	updatedICS := vtodo.Format(updatedItem)

	req = httptest.NewRequest("PUT", fmt.Sprintf("/cal/lists/%s/%s.ics", listID, newItemID), strings.NewReader(updatedICS))
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content, got %d: %s", w.Code, w.Body.String())
	}
	if note.Items[0].Content != "Organic Tomatoes" {
		t.Fatalf("expected updated content 'Organic Tomatoes', got '%s'", note.Items[0].Content)
	}
	if !note.Items[0].Completed {
		t.Fatalf("expected item to be completed after PUT")
	}

	// Verify updated item via GET
	getReq = httptest.NewRequest("GET", fmt.Sprintf("/cal/lists/%s/%s.ics", listID, newItemID), nil)
	getReq.SetBasicAuth("alice@example.com", "secret")
	getW = httptest.NewRecorder()
	h.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200 OK fetching updated item, got %d", getW.Code)
	}
	getBody := getW.Body.String()
	if !strings.Contains(getBody, "SUMMARY:Organic Tomatoes") || !strings.Contains(getBody, "STATUS:COMPLETED") {
		t.Fatalf("expected updated content and completed status in GET body, got:\n%s", getBody)
	}

	// 3. PUT with invalid ICS
	req = httptest.NewRequest("PUT", fmt.Sprintf("/cal/lists/%s/%s.ics", listID, uuid.New()), strings.NewReader("INVALID ICS"))
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", w.Code)
	}
}

func TestCalDAVListItemDELETE(t *testing.T) {
	user, notesMock, h := setupTestEnv()

	listID := uuid.New()
	itemID := uuid.New()
	now := time.Now().UTC()

	notesMock.notes[listID] = &notes.Note{
		ID:        listID,
		UserID:    user.ID,
		Title:     "Groceries",
		Kind:      notes.KindList,
		CreatedAt: now,
		UpdatedAt: now,
		Items: []notes.NoteItem{
			{
				ID:        itemID,
				NoteID:    listID,
				Content:   "Cheese",
				Completed: false,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	// DELETE existing item
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/cal/lists/%s/%s.ics", listID, itemID), nil)
	req.SetBasicAuth("alice@example.com", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content, got %d", w.Code)
	}

	note := notesMock.notes[listID]
	if len(note.Items) != 0 {
		t.Fatalf("expected 0 items after DELETE, got %d", len(note.Items))
	}

	// DELETE again on deleted item -> 404
	req = httptest.NewRequest("DELETE", fmt.Sprintf("/cal/lists/%s/%s.ics", listID, itemID), nil)
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d", w.Code)
	}
}
