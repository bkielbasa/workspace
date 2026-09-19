package caldav

import (
	"context"
	"errors"
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

type mockAuth struct {
	user *identity.User
}

func (m *mockAuth) Authenticate(ctx context.Context, email, password string) (*identity.User, error) {
	if m.user != nil {
		return m.user, nil
	}
	return nil, fmt.Errorf("invalid auth")
}

type dummyAuth = mockAuth

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

func newMockCalendarService() calendarService {
	return &dummyCalendar{}
}

type mockNotesService struct {
	notes map[uuid.UUID]*notes.Note
}

func newMockNotesService() *mockNotesService {
	return &mockNotesService{
		notes: make(map[uuid.UUID]*notes.Note),
	}
}

func (m *mockNotesService) CreateNote(ctx context.Context, userID uuid.UUID, n notes.Note) (*notes.Note, error) {
	if n.ID == uuid.Nil {
		n.ID = uuid.New()
	}
	n.UserID = userID
	now := time.Now().UTC()
	n.CreatedAt = now
	n.UpdatedAt = now
	cp := n
	m.notes[n.ID] = &cp
	ret := cp
	return &ret, nil
}

func (m *mockNotesService) UpdateNote(ctx context.Context, userID uuid.UUID, isAdmin bool, n notes.Note) (*notes.Note, error) {
	existing, ok := m.notes[n.ID]
	if !ok {
		return nil, notes.ErrNotFound
	}
	if existing.UserID != userID && !isAdmin {
		return nil, notes.ErrNotFound
	}
	n.UserID = existing.UserID
	n.UpdatedAt = time.Now().UTC()
	cp := n
	m.notes[n.ID] = &cp
	ret := cp
	return &ret, nil
}

func (m *mockNotesService) DeleteNote(ctx context.Context, userID uuid.UUID, isAdmin bool, id uuid.UUID) error {
	existing, ok := m.notes[id]
	if !ok {
		return notes.ErrNotFound
	}
	if existing.UserID != userID && !isAdmin {
		return notes.ErrNotFound
	}
	delete(m.notes, id)
	return nil
}

func (m *mockNotesService) ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]notes.Note, error) {
	var result []notes.Note
	for _, n := range m.notes {
		if n.UserID == userID && n.IsArchived == archived {
			result = append(result, *n)
		}
	}
	return result, nil
}

func (m *mockNotesService) GetNote(ctx context.Context, userID uuid.UUID, id uuid.UUID) (*notes.Note, error) {
	n, ok := m.notes[id]
	if !ok {
		return nil, notes.ErrNotFound
	}
	if n.UserID != userID {
		return nil, notes.ErrNotFound
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

	otherListID := uuid.New()
	notesMock.notes[otherListID] = &notes.Note{
		ID:        otherListID,
		UserID:    uuid.New(),
		Title:     "Other User List",
		Kind:      notes.KindList,
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
	expectedHomeSet := fmt.Sprintf("<cal:calendar-home-set><d:href>/cal/%s/</d:href></cal:calendar-home-set>", user.ID)
	if !strings.Contains(body, expectedHomeSet) {
		t.Fatalf("expected calendar-home-set to contain %q, body:\n%s", expectedHomeSet, body)
	}

	// 2. PROPFIND on /cal/{user}/ with Depth: 1
	req = httptest.NewRequest("PROPFIND", fmt.Sprintf("/cal/%s/", user.ID), nil)
	req.Header.Set("Depth", "1")
	req.SetBasicAuth("alice@example.com", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207, got %d", w.Code)
	}
	body = w.Body.String()
	if !strings.Contains(body, fmt.Sprintf("/cal/lists/%s/", listID)) {
		t.Fatalf("expected /cal/%s/ to contain list %s", user.ID, listID)
	}
	if strings.Contains(body, fmt.Sprintf("/cal/lists/%s/", noteID)) {
		t.Fatalf("Depth: 1 should not contain text note %s", noteID)
	}
	if strings.Contains(body, fmt.Sprintf("/cal/lists/%s/", otherListID)) {
		t.Fatalf("Depth: 1 should not contain other user list %s", otherListID)
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

func TestCalDAVHomeSetCollectionDiscovery(t *testing.T) {
	notesSvc := newMockNotesService()
	calSvc := newMockCalendarService()
	userID := uuid.New()
	handler := NewWithTasks(calSvc, notesSvc, &mockAuth{user: &identity.User{ID: userID, Email: "user@example.com"}})

	// Unauthenticated request must return 401 Unauthorized with Basic realm="workspace"
	unauthReq := httptest.NewRequest("PROPFIND", "/cal/", nil)
	unauthW := httptest.NewRecorder()
	handler.ServeHTTP(unauthW, unauthReq)
	if unauthW.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", unauthW.Code)
	}
	if authHdr := unauthW.Header().Get("WWW-Authenticate"); authHdr != `Basic realm="workspace"` {
		t.Fatalf("expected WWW-Authenticate Basic realm=\"workspace\", got %q", authHdr)
	}

	// Create a list note
	listNote, err := notesSvc.CreateNote(context.Background(), userID, notes.Note{
		Title: "Groceries",
		Kind:  notes.KindList,
	})
	if err != nil {
		t.Fatalf("failed to create list note: %v", err)
	}

	// 1. PROPFIND Depth: 0 on /cal/ - check calendar-home-set points to /cal/{userID}/
	req := httptest.NewRequest("PROPFIND", "/cal/", strings.NewReader(`
		<d:propfind xmlns:d="DAV:" xmlns:cal="urn:ietf:params:xml:ns:caldav">
			<d:prop><cal:calendar-home-set/></d:prop>
		</d:propfind>
	`))
	req.Header.Set("Depth", "0")
	req.SetBasicAuth("user@example.com", "pass")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 Multi-Status, got %d", w.Code)
	}
	body := w.Body.String()
	expectedHomeSet := fmt.Sprintf("<cal:calendar-home-set><d:href>/cal/%s/</d:href></cal:calendar-home-set>", userID)
	if !strings.Contains(body, expectedHomeSet) {
		t.Fatalf("expected calendar-home-set to contain %q, got body:\n%s", expectedHomeSet, body)
	}

	// 2. PROPFIND Depth: 1 on /cal/{userID}/ - should return default calendar AND list collections
	req = httptest.NewRequest("PROPFIND", fmt.Sprintf("/cal/%s/", userID), nil)
	req.Header.Set("Depth", "1")
	req.SetBasicAuth("user@example.com", "pass")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 Multi-Status on Depth 1, got %d", w.Code)
	}
	depthBody := w.Body.String()
	defaultCalHref := fmt.Sprintf("<d:href>/cal/%s/default/</d:href>", userID)
	listCalHref := fmt.Sprintf("<d:href>/cal/lists/%s/</d:href>", listNote.ID)
	if !strings.Contains(depthBody, defaultCalHref) {
		t.Errorf("expected Depth: 1 to contain default calendar %q", defaultCalHref)
	}
	if !strings.Contains(depthBody, listCalHref) {
		t.Errorf("expected Depth: 1 to contain list collection %q", listCalHref)
	}
	if !strings.Contains(depthBody, "<C:comp name=\"VTODO\"/>") {
		t.Errorf("expected Depth: 1 to advertise VTODO for list collection")
	}
}

func TestCalDAVMKCalendarAndListDeletion(t *testing.T) {
	notesSvc := newMockNotesService()
	calSvc := newMockCalendarService()
	userID := uuid.New()
	handler := NewWithTasks(calSvc, notesSvc, &mockAuth{user: &identity.User{ID: userID, Email: "user@example.com"}})

	newListID := uuid.New()
	mkBody := `<?xml version="1.0" encoding="utf-8" ?>
	<C:mkcalendar xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
		<D:set>
			<D:prop>
				<D:displayname>Packing List</D:displayname>
				<C:supported-calendar-component-set>
					<C:comp name="VTODO"/>
				</C:supported-calendar-component-set>
			</D:prop>
		</D:set>
	</C:mkcalendar>`

	req := httptest.NewRequest("MKCALENDAR", fmt.Sprintf("/cal/lists/%s/", newListID), strings.NewReader(mkBody))
	req.SetBasicAuth("user@example.com", "pass")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on MKCALENDAR, got %d: %s", w.Code, w.Body.String())
	}

	// Duplicate MKCALENDAR on existing list ID must return 409 Conflict
	dupReq := httptest.NewRequest("MKCALENDAR", fmt.Sprintf("/cal/lists/%s/", newListID), strings.NewReader(mkBody))
	dupReq.SetBasicAuth("user@example.com", "pass")
	dupW := httptest.NewRecorder()
	handler.ServeHTTP(dupW, dupReq)
	if dupW.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on duplicate MKCALENDAR, got %d", dupW.Code)
	}

	// Verify note exists in service
	createdNote, err := notesSvc.GetNote(context.Background(), userID, newListID)
	if err != nil {
		t.Fatalf("expected note to be created with ID %s, got err: %v", newListID, err)
	}
	if createdNote.Title != "Packing List" {
		t.Errorf("expected title 'Packing List', got %q", createdNote.Title)
	}
	if createdNote.Kind != notes.KindList {
		t.Errorf("expected KindList, got %s", createdNote.Kind)
	}

	// Delete collection via DELETE /cal/lists/{id}/
	delReq := httptest.NewRequest("DELETE", fmt.Sprintf("/cal/lists/%s/", newListID), nil)
	delReq.SetBasicAuth("user@example.com", "pass")
	delW := httptest.NewRecorder()
	handler.ServeHTTP(delW, delReq)
	if delW.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content on list DELETE, got %d", delW.Code)
	}

	_, err = notesSvc.GetNote(context.Background(), userID, newListID)
	if !errors.Is(err, notes.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after deletion, got %v", err)
	}
}

func TestCalDAVListCollectionPROPPATCH(t *testing.T) {
	notesSvc := newMockNotesService()
	calSvc := newMockCalendarService()
	userID := uuid.New()
	handler := NewWithTasks(calSvc, notesSvc, &mockAuth{user: &identity.User{ID: userID, Email: "user@example.com"}})

	listNote, err := notesSvc.CreateNote(context.Background(), userID, notes.Note{
		Title: "Old Title",
		Kind:  notes.KindList,
	})
	if err != nil {
		t.Fatalf("failed to create list note: %v", err)
	}

	patchBody := `<?xml version="1.0" encoding="utf-8" ?>
	<D:propertyupdate xmlns:D="DAV:">
		<D:set>
			<D:prop>
				<D:displayname>New Title</D:displayname>
			</D:prop>
		</D:set>
	</D:propertyupdate>`

	req := httptest.NewRequest("PROPPATCH", fmt.Sprintf("/cal/lists/%s/", listNote.ID), strings.NewReader(patchBody))
	req.SetBasicAuth("user@example.com", "pass")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 MultiStatus on PROPPATCH, got %d: %s", w.Code, w.Body.String())
	}

	updated, err := notesSvc.GetNote(context.Background(), userID, listNote.ID)
	if err != nil {
		t.Fatalf("failed to get note: %v", err)
	}
	if updated.Title != "New Title" {
		t.Fatalf("expected title 'New Title', got %q", updated.Title)
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
