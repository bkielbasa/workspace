package web

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/contacts"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/mail"
	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

type mockNotesService struct {
	mu     sync.Mutex
	broker *notes.Broker
	notes  map[uuid.UUID]*notes.Note
}

func newMockNotesService() *mockNotesService {
	return &mockNotesService{
		broker: notes.NewBroker(),
		notes:  make(map[uuid.UUID]*notes.Note),
	}
}

func (m *mockNotesService) Broker() *notes.Broker {
	return m.broker
}

func (m *mockNotesService) CreateNote(ctx context.Context, userID uuid.UUID, n notes.Note) (*notes.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := uuid.New()
	now := time.Now().UTC()
	n.ID = id
	n.UserID = userID
	n.CreatedAt = now
	n.UpdatedAt = now
	if n.Color == "" {
		n.Color = "default"
	}
	if n.Kind == "" {
		n.Kind = notes.KindNote
	}
	m.notes[id] = &n

	m.broker.Publish(notes.Event{
		Type:   "note_created",
		NoteID: id,
		UserID: userID,
	})
	return &n, nil
}

func (m *mockNotesService) GetNote(ctx context.Context, userID uuid.UUID, id uuid.UUID) (*notes.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.notes[id]
	if !ok {
		return nil, notes.ErrNotFound
	}
	if n.UserID != userID && !n.IsFamilyShared {
		return nil, notes.ErrNotFound
	}
	cp := *n
	cp.Items = make([]notes.NoteItem, len(n.Items))
	copy(cp.Items, n.Items)
	return &cp, nil
}

func (m *mockNotesService) ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]notes.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []notes.Note
	for _, n := range m.notes {
		if n.IsArchived != archived {
			continue
		}
		if tag != "" {
			hasTag := false
			for _, t := range n.Tags {
				if t == tag {
					hasTag = true
					break
				}
			}
			if !hasTag {
				continue
			}
		}
		if n.UserID == userID || n.IsFamilyShared {
			cp := *n
			cp.Items = make([]notes.NoteItem, len(n.Items))
			copy(cp.Items, n.Items)
			result = append(result, cp)
		}
	}
	return result, nil
}

func (m *mockNotesService) UpdateNote(ctx context.Context, userID uuid.UUID, isAdmin bool, n notes.Note) (*notes.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.notes[n.ID]
	if !ok {
		return nil, notes.ErrNotFound
	}
	if existing.UserID != userID && !isAdmin {
		return nil, notes.ErrForbidden
	}

	n.UpdatedAt = time.Now().UTC()
	m.notes[n.ID] = &n
	m.broker.Publish(notes.Event{
		Type:   "note_updated",
		NoteID: n.ID,
		UserID: userID,
	})
	return &n, nil
}

func (m *mockNotesService) DeleteNote(ctx context.Context, userID uuid.UUID, isAdmin bool, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.notes[id]
	if !ok {
		return notes.ErrNotFound
	}
	if existing.UserID != userID && !isAdmin {
		return notes.ErrForbidden
	}
	delete(m.notes, id)
	m.broker.Publish(notes.Event{
		Type:   "note_deleted",
		NoteID: id,
		UserID: userID,
	})
	return nil
}

func (m *mockNotesService) AddItem(ctx context.Context, userID uuid.UUID, noteID uuid.UUID, content string) (*notes.NoteItem, error) {
	return m.AddItemWithID(ctx, userID, noteID, uuid.New(), content)
}

func (m *mockNotesService) AddItemWithID(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, content string) (*notes.NoteItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.notes[noteID]
	if !ok {
		return nil, notes.ErrNotFound
	}
	if n.UserID != userID && !n.IsFamilyShared {
		return nil, notes.ErrNotFound
	}

	now := time.Now().UTC()
	item := notes.NoteItem{
		ID:        itemID,
		NoteID:    noteID,
		Content:   content,
		Completed: false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	n.Items = append(n.Items, item)
	m.broker.Publish(notes.Event{
		Type:      "item_added",
		NoteID:    noteID,
		ItemID:    item.ID,
		Completed: false,
		UserID:    userID,
	})
	return &item, nil
}

func (m *mockNotesService) UpdateItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, content string, completed bool) (*notes.NoteItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.notes[noteID]
	if !ok {
		return nil, notes.ErrNotFound
	}
	for i := range n.Items {
		if n.Items[i].ID == itemID {
			n.Items[i].Content = content
			n.Items[i].Completed = completed
			n.Items[i].UpdatedAt = time.Now().UTC()
			return &n.Items[i], nil
		}
	}
	return nil, notes.ErrNotFound
}

func (m *mockNotesService) ToggleItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, completed bool) (*notes.NoteItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.notes[noteID]
	if !ok {
		return nil, notes.ErrNotFound
	}
	for i := range n.Items {
		if n.Items[i].ID == itemID {
			n.Items[i].Completed = completed
			n.Items[i].UpdatedAt = time.Now().UTC()
			m.broker.Publish(notes.Event{
				Type:      "item_toggled",
				NoteID:    noteID,
				ItemID:    itemID,
				Completed: completed,
				UserID:    userID,
			})
			return &n.Items[i], nil
		}
	}
	return nil, notes.ErrNotFound
}

func (m *mockNotesService) DeleteItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.notes[noteID]
	if !ok {
		return notes.ErrNotFound
	}
	for i := range n.Items {
		if n.Items[i].ID == itemID {
			n.Items = append(n.Items[:i], n.Items[i+1:]...)
			m.broker.Publish(notes.Event{
				Type:   "item_deleted",
				NoteID: noteID,
				ItemID: itemID,
				UserID: userID,
			})
			return nil
		}
	}
	return notes.ErrNotFound
}

type notesTestSessionService struct {
	userID uuid.UUID
}

func (s notesTestSessionService) Create(context.Context, uuid.UUID, time.Duration) (*identity.Session, error) {
	return &identity.Session{UserID: s.userID, Token: "test-session-token"}, nil
}
func (s notesTestSessionService) GetByToken(context.Context, string) (*identity.Session, error) {
	return &identity.Session{UserID: s.userID, Token: "test-session-token"}, nil
}
func (s notesTestSessionService) Delete(context.Context, string) error {
	return nil
}

type notesTestUserService struct {
	user *identity.User
}

func (u notesTestUserService) Authenticate(context.Context, string, string) (*identity.User, error) {
	return u.user, nil
}
func (u notesTestUserService) Get(context.Context, uuid.UUID) (*identity.User, error) {
	return u.user, nil
}
func (u notesTestUserService) Update(context.Context, uuid.UUID, string, bool) error {
	return nil
}
func (u notesTestUserService) ChangePassword(context.Context, uuid.UUID, string) error {
	return nil
}
func (u notesTestUserService) List(context.Context) ([]identity.User, error) {
	return []identity.User{*u.user}, nil
}
func (u notesTestUserService) Delete(context.Context, uuid.UUID) error {
	return nil
}
func (u notesTestUserService) SetUsername(ctx context.Context, id uuid.UUID, username string) error {
	return nil
}

type dummyContactsService struct{}

func (dummyContactsService) List(context.Context, uuid.UUID) ([]contacts.Contact, error) {
	return nil, nil
}
func (dummyContactsService) Get(context.Context, uuid.UUID, uuid.UUID) (contacts.Contact, error) {
	return contacts.Contact{}, nil
}
func (dummyContactsService) PutStructured(context.Context, uuid.UUID, *uuid.UUID, contacts.Contact) (*contacts.Contact, error) {
	return nil, nil
}
func (dummyContactsService) DeleteByID(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

type dummyCalendarService struct{}

func (dummyCalendarService) Get(context.Context, uuid.UUID, uuid.UUID) (*calendar.Event, error) {
	return nil, nil
}
func (dummyCalendarService) GetByUID(context.Context, uuid.UUID, string) (*calendar.Event, error) {
	return nil, nil
}
func (dummyCalendarService) List(context.Context, uuid.UUID) ([]calendar.Event, error) {
	return nil, nil
}
func (dummyCalendarService) Put(context.Context, calendar.Event) (*calendar.Event, error) {
	return nil, nil
}
func (dummyCalendarService) Delete(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

type dummyMailService struct{}

func (dummyMailService) EnsureDefaultMailboxes(context.Context, uuid.UUID) error {
	return nil
}
func (dummyMailService) ListMailboxes(context.Context, uuid.UUID) ([]mail.MailboxInfo, error) {
	return nil, nil
}
func (dummyMailService) GetMailbox(context.Context, uuid.UUID, string) (*mail.Mailbox, error) {
	return nil, nil
}
func (dummyMailService) ListMessages(context.Context, uuid.UUID, int, int) ([]mail.Message, error) {
	return nil, nil
}
func (dummyMailService) SearchMessages(context.Context, uuid.UUID, string) ([]mail.Message, error) {
	return nil, nil
}
func (dummyMailService) GetMessage(context.Context, uuid.UUID, uuid.UUID) (*mail.Message, *mail.Mailbox, error) {
	return nil, nil, nil
}
func (dummyMailService) UpdateFlags(context.Context, uuid.UUID, bool, bool, bool, bool, bool) error {
	return nil
}
func (dummyMailService) DeleteMessage(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
func (dummyMailService) SendMessage(context.Context, *identity.User, string, string, string) (*mail.Message, error) {
	return nil, nil
}
func (dummyMailService) SendMessageWithAttachments(context.Context, *identity.User, string, string, string, []mail.Attachment) (*mail.Message, error) {
	return nil, nil
}
func (dummyMailService) SendInvite(context.Context, *identity.User, string, string, string, string, string) (*mail.Message, error) {
	return nil, nil
}

func setupNotesTestServer(t *testing.T, user *identity.User, notesSvc notesService) (*Server, *http.ServeMux) {
	t.Helper()
	files := os.DirFS("../..")
	if _, err := fs.Stat(files, "web/templates/notes.html"); err != nil {
		t.Fatalf("missing template notes.html: %v", err)
	}

	sessions := notesTestSessionService{userID: user.ID}
	users := notesTestUserService{user: user}

	srv, err := New(files, dummyContactsService{}, dummyCalendarService{}, dummyMailService{}, sessions, users, false)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	srv.SetNotes(notesSvc)

	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	return srv, mux
}

func TestNotesPageRequiresAuth(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/notes", nil)
	w := httptest.NewRecorder()

	handler := s.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusFound && w.Code != http.StatusUnauthorized {
		t.Fatalf("expected redirect to login or 401, got %d", w.Code)
	}
}

func TestNotesCreateRequiresCSRF(t *testing.T) {
	user := &identity.User{
		ID:      uuid.New(),
		Email:   "alice@example.com",
		Enabled: true,
	}
	notesSvc := newMockNotesService()
	_, mux := setupNotesTestServer(t, user, notesSvc)

	// Missing CSRF token
	req := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader("title=Hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for missing CSRF, got %d", w.Code)
	}

	// Mismatched CSRF token
	req = httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader("title=Hello&_csrf=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "token-123"})
	w = httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for mismatched CSRF, got %d", w.Code)
	}
}

func TestNotesPageRendersNotes(t *testing.T) {
	user := &identity.User{
		ID:      uuid.New(),
		Email:   "alice@example.com",
		Enabled: true,
	}
	notesSvc := newMockNotesService()
	_, err := notesSvc.CreateNote(context.Background(), user.ID, notes.Note{
		Title: "Groceries",
		Body:  "Milk, Bread, Eggs",
		Kind:  notes.KindNote,
		Color: "mint",
	})
	if err != nil {
		t.Fatalf("failed to create note: %v", err)
	}

	_, mux := setupNotesTestServer(t, user, notesSvc)

	req := httptest.NewRequest(http.MethodGet, "/notes", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "token-123"})
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Groceries") {
		t.Errorf("expected page to contain 'Groceries'")
	}
	if !strings.Contains(body, "Milk, Bread, Eggs") {
		t.Errorf("expected page to contain 'Milk, Bread, Eggs'")
	}
	if !strings.Contains(body, "note-color-mint") {
		t.Errorf("expected page to contain 'note-color-mint'")
	}
}

func TestNotesCreateNoteAndChecklist(t *testing.T) {
	user := &identity.User{
		ID:      uuid.New(),
		Email:   "alice@example.com",
		Enabled: true,
	}
	notesSvc := newMockNotesService()
	_, mux := setupNotesTestServer(t, user, notesSvc)

	csrfToken := "token-abc"

	// 1. Standard POST creates a note and redirects
	form := url.Values{
		"_csrf": {csrfToken},
		"title": {"Weekend Plan"},
		"body":  {"Go for a hike and read"},
		"kind":  {"note"},
		"color": {"peach"},
	}
	req := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 See Other redirect, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/notes" {
		t.Fatalf("expected redirect to /notes, got %q", loc)
	}

	// 2. HTMX POST creates a checklist with lines in body and returns notesList partial
	formChecklist := url.Values{
		"_csrf":            {csrfToken},
		"title":            {"Weekly Chores"},
		"body":             {"Dishes\nLaundry\nTrash"},
		"kind":             {"list"},
		"color":            {"storm"},
		"is_family_shared": {"true"},
	}
	req = httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(formChecklist.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
	w = httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for HTMX POST, got %d: %s", w.Code, w.Body.String())
	}
	resBody := w.Body.String()
	if !strings.Contains(resBody, "Weekly Chores") {
		t.Errorf("expected response to contain 'Weekly Chores'")
	}
	if !strings.Contains(resBody, "Dishes") {
		t.Errorf("expected response to contain checklist item 'Dishes'")
	}
	if !strings.Contains(resBody, "Laundry") {
		t.Errorf("expected response to contain checklist item 'Laundry'")
	}
	if !strings.Contains(resBody, "Trash") {
		t.Errorf("expected response to contain checklist item 'Trash'")
	}
	if !strings.Contains(resBody, "Family") {
		t.Errorf("expected response to contain Family badge")
	}
}

func TestNotesTogglePinAndDelete(t *testing.T) {
	user := &identity.User{
		ID:      uuid.New(),
		Email:   "alice@example.com",
		Enabled: true,
	}
	notesSvc := newMockNotesService()
	n, _ := notesSvc.CreateNote(context.Background(), user.ID, notes.Note{
		Title: "Important Note",
		Body:  "Must do",
		Kind:  notes.KindNote,
	})

	_, mux := setupNotesTestServer(t, user, notesSvc)
	csrfToken := "token-pin"

	// Toggle Pin via HTMX
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%s/toggle-pin", n.ID), strings.NewReader("_csrf="+csrfToken))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "note-pinned") {
		t.Errorf("expected pinned note to have 'note-pinned' class")
	}

	// Delete Note via HTMX
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%s/delete", n.ID), strings.NewReader("_csrf="+csrfToken))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
	w = httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Important Note") {
		t.Errorf("deleted note should no longer be present in notes list")
	}
}

func TestNotesItemAddToggleDelete(t *testing.T) {
	user := &identity.User{
		ID:      uuid.New(),
		Email:   "alice@example.com",
		Enabled: true,
	}
	notesSvc := newMockNotesService()
	n, _ := notesSvc.CreateNote(context.Background(), user.ID, notes.Note{
		Title: "Project Tasks",
		Kind:  notes.KindList,
	})

	_, mux := setupNotesTestServer(t, user, notesSvc)
	csrfToken := "token-item"

	// 1. Add Item
	addForm := url.Values{
		"_csrf":   {csrfToken},
		"content": {"Write unit tests"},
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%s/items", n.ID), strings.NewReader(addForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK adding item, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Write unit tests") {
		t.Errorf("expected response to contain new item text")
	}

	// Retrieve item ID from mock service
	updated, _ := notesSvc.GetNote(context.Background(), user.ID, n.ID)
	if len(updated.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(updated.Items))
	}
	itemID := updated.Items[0].ID

	// 2. Toggle Item Complete
	toggleForm := url.Values{
		"_csrf":     {csrfToken},
		"completed": {"on"},
	}
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%s/items/%s/toggle", n.ID, itemID), strings.NewReader(toggleForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
	w = httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK toggling item, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "completed") {
		t.Errorf("expected response to have completed style/attribute")
	}

	// 3. Delete Item
	delForm := url.Values{
		"_csrf": {csrfToken},
	}
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%s/items/%s/delete", n.ID, itemID), strings.NewReader(delForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
	w = httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK deleting item, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Write unit tests") {
		t.Errorf("deleted item should no longer be present in card")
	}
}

func TestNotesLiveSSE(t *testing.T) {
	user := &identity.User{
		ID:      uuid.New(),
		Email:   "alice@example.com",
		Enabled: true,
	}
	notesSvc := newMockNotesService()
	_, mux := setupNotesTestServer(t, user, notesSvc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/notes/live", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(w, req)
		close(done)
	}()

	// Give the handler a moment to subscribe
	time.Sleep(50 * time.Millisecond)

	noteID := uuid.New()
	notesSvc.Broker().Publish(notes.Event{
		Type:   "note_updated",
		NoteID: noteID,
		UserID: user.ID,
	})

	// Allow event to be written and flushed
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %q", w.Header().Get("Content-Type"))
	}
	out := w.Body.String()
	if !strings.Contains(out, "event: note_update") {
		t.Errorf("expected event: note_update in SSE body, got:\n%s", out)
	}
	if !strings.Contains(out, noteID.String()) {
		t.Errorf("expected note ID %s in SSE body, got:\n%s", noteID, out)
	}
}

func TestNotesLiveSSEUnauthorized(t *testing.T) {
	user := &identity.User{
		ID:      uuid.New(),
		Email:   "alice@example.com",
		Enabled: true,
	}
	notesSvc := newMockNotesService()
	_, mux := setupNotesTestServer(t, user, notesSvc)

	req := httptest.NewRequest(http.MethodGet, "/notes/live", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", w.Code)
	}
}

func TestNotesLiveSSEFiltering(t *testing.T) {
	userAlice := &identity.User{
		ID:      uuid.New(),
		Email:   "alice@example.com",
		Enabled: true,
	}
	userBob := &identity.User{
		ID:      uuid.New(),
		Email:   "bob@example.com",
		Enabled: true,
	}
	notesSvc := newMockNotesService()
	_, mux := setupNotesTestServer(t, userAlice, notesSvc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/notes/live", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		mux.ServeHTTP(w, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	// 1. Bob's private event -> should NOT be received by Alice
	bobPrivateID := uuid.New()
	notesSvc.Broker().Publish(notes.Event{
		Type:           "note_created",
		NoteID:         bobPrivateID,
		UserID:         userBob.ID,
		IsFamilyShared: false,
	})

	// 2. Bob's family-shared event -> SHOULD be received by Alice
	bobSharedID := uuid.New()
	notesSvc.Broker().Publish(notes.Event{
		Type:           "note_created",
		NoteID:         bobSharedID,
		UserID:         userBob.ID,
		IsFamilyShared: true,
	})

	// 3. Alice's own private event -> SHOULD be received by Alice
	alicePrivateID := uuid.New()
	notesSvc.Broker().Publish(notes.Event{
		Type:           "note_created",
		NoteID:         alicePrivateID,
		UserID:         userAlice.ID,
		IsFamilyShared: false,
	})

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	out := w.Body.String()
	if strings.Contains(out, bobPrivateID.String()) {
		t.Errorf("Alice received Bob's private note event!")
	}
	if !strings.Contains(out, bobSharedID.String()) {
		t.Errorf("Alice did not receive Bob's family shared note event")
	}
	if !strings.Contains(out, alicePrivateID.String()) {
		t.Errorf("Alice did not receive her own note event")
	}
}

func TestNotesPageFiltering(t *testing.T) {
	user := &identity.User{
		ID:      uuid.New(),
		Email:   "alice@example.com",
		Enabled: true,
	}
	notesSvc := newMockNotesService()

	// 1. Active note with tag "work"
	note1, _ := notesSvc.CreateNote(context.Background(), user.ID, notes.Note{
		Title: "Work Note",
		Tags:  []string{"work"},
	})
	// 2. Active note with tag "home"
	notesSvc.CreateNote(context.Background(), user.ID, notes.Note{
		Title: "Home Note",
		Tags:  []string{"home"},
	})
	// 3. Archived note
	n3, _ := notesSvc.CreateNote(context.Background(), user.ID, notes.Note{
		Title: "Archived Note",
	})
	n3.IsArchived = true

	_, mux := setupNotesTestServer(t, user, notesSvc)

	// Filter by tag=work
	req := httptest.NewRequest(http.MethodGet, "/notes?tag=work", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Work Note") {
		t.Errorf("expected Work Note in response")
	}
	if strings.Contains(w.Body.String(), "Home Note") {
		t.Errorf("Home Note should not be present when filtered by tag=work")
	}

	// Filter by archived=true
	req = httptest.NewRequest(http.MethodGet, "/notes?archived=true", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Archived Note") {
		t.Errorf("expected Archived Note in response")
	}
	if strings.Contains(w.Body.String(), "Work Note") {
		t.Errorf("Work Note should not be present when filtered by archived=true")
	}
	_ = note1
}

func TestNotesDetail(t *testing.T) {
	user := &identity.User{
		ID:      uuid.New(),
		Email:   "alice@example.com",
		Enabled: true,
	}
	notesSvc := newMockNotesService()
	n, _ := notesSvc.CreateNote(context.Background(), user.ID, notes.Note{
		Title: "Secret Note",
		Body:  "Only for Alice",
	})
	_, mux := setupNotesTestServer(t, user, notesSvc)

	// Valid detail request
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/notes/%s", n.ID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Secret Note") {
		t.Errorf("expected detail to render note title")
	}

	// Non-existent note returns 404
	randomID := uuid.New()
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/notes/%s", randomID), nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	w = httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %d", w.Code)
	}
}

func TestNotesUpdateAndShare(t *testing.T) {
	user := &identity.User{
		ID:      uuid.New(),
		Email:   "alice@example.com",
		Enabled: true,
	}
	notesSvc := newMockNotesService()
	n, _ := notesSvc.CreateNote(context.Background(), user.ID, notes.Note{
		Title: "Draft Note",
		Body:  "Draft Body",
	})
	_, mux := setupNotesTestServer(t, user, notesSvc)
	csrfToken := "token-update"

	// 1. Update note via HTMX
	updateForm := url.Values{
		"_csrf": {csrfToken},
		"title": {"Final Note"},
		"color": {"fog"},
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%s", n.ID), strings.NewReader(updateForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Final Note") {
		t.Errorf("expected updated title 'Final Note'")
	}
	if !strings.Contains(w.Body.String(), "note-color-fog") {
		t.Errorf("expected updated color class 'note-color-fog'")
	}

	// 2. Share note with family via HTMX
	shareForm := url.Values{
		"_csrf": {csrfToken},
	}
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/notes/%s/share", n.ID), strings.NewReader(shareForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: "session", Value: "test-session-token"})
	req.AddCookie(&http.Cookie{Name: "csrf", Value: csrfToken})
	w = httptest.NewRecorder()

	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Family") {
		t.Errorf("expected family badge after sharing")
	}
}
