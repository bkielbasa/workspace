package notes

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type mockRepo struct {
	notes map[uuid.UUID]Note
	items map[uuid.UUID]NoteItem
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		notes: make(map[uuid.UUID]Note),
		items: make(map[uuid.UUID]NoteItem),
	}
}

func (m *mockRepo) CreateNote(ctx context.Context, n Note) (*Note, error) {
	if n.ID == uuid.Nil {
		n.ID = uuid.New()
	}
	m.notes[n.ID] = n
	return &n, nil
}
func (m *mockRepo) GetNote(ctx context.Context, id uuid.UUID) (*Note, error) {
	n, ok := m.notes[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &n, nil
}
func (m *mockRepo) ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]Note, error) {
	var res []Note
	for _, n := range m.notes {
		if (n.UserID == userID || n.IsFamilyShared) && n.IsArchived == archived {
			res = append(res, n)
		}
	}
	return res, nil
}
func (m *mockRepo) UpdateNote(ctx context.Context, n Note) (*Note, error) {
	m.notes[n.ID] = n
	return &n, nil
}
func (m *mockRepo) DeleteNote(ctx context.Context, id uuid.UUID) error {
	delete(m.notes, id)
	return nil
}
func (m *mockRepo) AddItem(ctx context.Context, item NoteItem) (*NoteItem, error) {
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	m.items[item.ID] = item
	return &item, nil
}
func (m *mockRepo) GetItem(ctx context.Context, id uuid.UUID) (*NoteItem, error) {
	item, ok := m.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &item, nil
}
func (m *mockRepo) ListItems(ctx context.Context, noteID uuid.UUID) ([]NoteItem, error) {
	var res []NoteItem
	for _, it := range m.items {
		if it.NoteID == noteID {
			res = append(res, it)
		}
	}
	return res, nil
}
func (m *mockRepo) ToggleItem(ctx context.Context, id uuid.UUID, completed bool) (*NoteItem, error) {
	it, ok := m.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	it.Completed = completed
	m.items[id] = it
	return &it, nil
}
func (m *mockRepo) UpdateItem(ctx context.Context, item NoteItem) (*NoteItem, error) {
	m.items[item.ID] = item
	return &item, nil
}
func (m *mockRepo) DeleteItem(ctx context.Context, id uuid.UUID) error {
	delete(m.items, id)
	return nil
}
func (m *mockRepo) ListTags(ctx context.Context, userID uuid.UUID) ([]NoteTag, error) {
	return nil, nil
}
func (m *mockRepo) SetNoteTags(ctx context.Context, noteID uuid.UUID, userID uuid.UUID, tags []string) error {
	return nil
}

func TestServiceAuthorization(t *testing.T) {
	repo := newMockRepo()
	broker := NewBroker()
	svc := NewService(repo, broker)

	userAlice := uuid.New()
	userBob := uuid.New()

	ctx := context.Background()

	// 1. Alice creates a private note
	note, err := svc.CreateNote(ctx, userAlice, Note{
		Title:          "Secret Diary",
		IsFamilyShared: false,
	})
	if err != nil {
		t.Fatalf("unexpected error creating note: %v", err)
	}

	// 2. Bob attempts to get Alice's private note -> should be ErrNotFound (hidden)
	_, err = svc.GetNote(ctx, userBob, note.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-owner note, got: %v", err)
	}

	// 3. Bob attempts to update Alice's note -> should be ErrNotFound
	_, err = svc.UpdateNote(ctx, userBob, false, *note)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-owner updating note, got: %v", err)
	}

	// 4. Bob tries to delete Alice's note -> should be ErrNotFound
	err = svc.DeleteNote(ctx, userBob, false, note.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-owner deleting note, got: %v", err)
	}

	// 5. Admin can delete Alice's note
	err = svc.DeleteNote(ctx, userBob, true, note.ID)
	if err != nil {
		t.Fatalf("expected admin to delete note, got: %v", err)
	}
}

func TestServiceSecurityConstraints(t *testing.T) {
	repo := newMockRepo()
	broker := NewBroker()
	svc := NewService(repo, broker)

	userAlice := uuid.New()
	userBob := uuid.New()

	ctx := context.Background()

	// 1. Alice creates a private note
	noteAlice, err := svc.CreateNote(ctx, userAlice, Note{
		Title: "Alice's Secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2. Bob attempts to delete Alice's private note -> should return ErrNotFound (information leak prevention)
	err = svc.DeleteNote(ctx, userBob, false, noteAlice.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unauthorized delete of private note, got: %v", err)
	}

	// 3. Bob attempts to update Alice's note -> should return ErrNotFound
	hijackedNote := *noteAlice
	hijackedNote.UserID = userBob
	hijackedNote.Title = "Hijacked Title"
	_, err = svc.UpdateNote(ctx, userBob, false, hijackedNote)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-owner update, got: %v", err)
	}

	// 4. Admin updates Alice's note -> verify ownership is preserved
	adminUser := uuid.New()
	updated, err := svc.UpdateNote(ctx, adminUser, true, hijackedNote)
	if err != nil {
		t.Fatalf("unexpected error on admin update: %v", err)
	}
	if updated.UserID != userAlice {
		t.Fatalf("expected note owner to remain Alice, but changed to: %s", updated.UserID)
	}

	// 5. Test item verification belonging to note
	item, err := svc.AddItem(ctx, userAlice, noteAlice.ID, "Buy Milk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Bob tries to toggle this item on a completely different note (or a fake note)
	fakeNote, err := svc.CreateNote(ctx, userBob, Note{Title: "Bob's Note"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ToggleItem with incorrect note ID -> should return ErrNotFound
	_, err = svc.ToggleItem(ctx, userBob, fakeNote.ID, item.ID, true)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound when toggling item belonging to another note, got: %v", err)
	}

	// DeleteItem with incorrect note ID -> should return ErrNotFound
	err = svc.DeleteItem(ctx, userBob, fakeNote.ID, item.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound when deleting item belonging to another note, got: %v", err)
	}
}

func TestServiceAddItemWithIDAndUpdatedItem(t *testing.T) {
	repo := newMockRepo()
	broker := NewBroker()
	svc := NewService(repo, broker)

	userID := uuid.New()
	ctx := context.Background()

	note, err := svc.CreateNote(ctx, userID, Note{
		Title: "Checklist",
		Kind:  KindList,
	})
	if err != nil {
		t.Fatalf("unexpected error creating note: %v", err)
	}

	customID := uuid.New()
	item, err := svc.AddItemWithID(ctx, userID, note.ID, customID, "Item 1")
	if err != nil {
		t.Fatalf("unexpected error adding item with ID: %v", err)
	}
	if item.ID != customID {
		t.Fatalf("expected item ID %s, got %s", customID, item.ID)
	}
	if item.Content != "Item 1" {
		t.Fatalf("expected content 'Item 1', got '%s'", item.Content)
	}

	// UpdateItem content and completion status
	updatedItem, err := svc.UpdateItem(ctx, userID, note.ID, item.ID, "Item 1 Updated", true)
	if err != nil {
		t.Fatalf("unexpected error updating item: %v", err)
	}
	if updatedItem.Content != "Item 1 Updated" {
		t.Fatalf("expected content 'Item 1 Updated', got '%s'", updatedItem.Content)
	}
	if !updatedItem.Completed {
		t.Fatalf("expected item to be completed")
	}
}

func TestServiceEventsUserIDAndFamilyShared(t *testing.T) {
	repo := newMockRepo()
	broker := NewBroker()
	svc := NewService(repo, broker)

	eventsCh := broker.Subscribe()
	defer broker.Unsubscribe(eventsCh)

	alice := uuid.New()
	ctx := context.Background()

	// 1. Create Note
	note, err := svc.CreateNote(ctx, alice, Note{
		Title: "Personal List",
		Kind:  KindList,
	})
	if err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}
	ev := <-eventsCh
	if ev.Type != "note_created" || ev.UserID != alice || ev.IsFamilyShared {
		t.Errorf("note_created: expected alice and shared=false, got %+v", ev)
	}

	// 2. Alice adds an item
	item, err := svc.AddItem(ctx, alice, note.ID, "Apples")
	if err != nil {
		t.Fatalf("AddItem failed: %v", err)
	}
	ev = <-eventsCh
	if ev.Type != "item_added" || ev.UserID != alice || ev.IsFamilyShared {
		t.Errorf("item_added: expected alice and shared=false, got %+v", ev)
	}

	// 3. Alice updates item
	_, err = svc.UpdateItem(ctx, alice, note.ID, item.ID, "Green Apples", false)
	if err != nil {
		t.Fatalf("UpdateItem failed: %v", err)
	}
	ev = <-eventsCh
	if ev.Type != "item_updated" || ev.UserID != alice || ev.IsFamilyShared {
		t.Errorf("item_updated: expected alice and shared=false, got %+v", ev)
	}

	// 4. Alice toggles item
	_, err = svc.ToggleItem(ctx, alice, note.ID, item.ID, true)
	if err != nil {
		t.Fatalf("ToggleItem failed: %v", err)
	}
	ev = <-eventsCh
	if ev.Type != "item_toggled" || ev.UserID != alice || ev.IsFamilyShared {
		t.Errorf("item_toggled: expected alice and shared=false, got %+v", ev)
	}

	// 5. Alice deletes item
	err = svc.DeleteItem(ctx, alice, note.ID, item.ID)
	if err != nil {
		t.Fatalf("DeleteItem failed: %v", err)
	}
	ev = <-eventsCh
	if ev.Type != "item_deleted" || ev.UserID != alice || ev.IsFamilyShared {
		t.Errorf("item_deleted: expected alice and shared=false, got %+v", ev)
	}

	// 6. Alice updates note
	note.Title = "Renamed List"
	_, err = svc.UpdateNote(ctx, alice, false, *note)
	if err != nil {
		t.Fatalf("UpdateNote failed: %v", err)
	}
	ev = <-eventsCh
	if ev.Type != "note_updated" || ev.UserID != alice || ev.IsFamilyShared {
		t.Errorf("note_updated: expected alice and shared=false, got %+v", ev)
	}

	// 7. Alice deletes note
	err = svc.DeleteNote(ctx, alice, false, note.ID)
	if err != nil {
		t.Fatalf("DeleteNote failed: %v", err)
	}
	ev = <-eventsCh
	if ev.Type != "note_deleted" || ev.UserID != alice || ev.IsFamilyShared {
		t.Errorf("note_deleted: expected alice and shared=false, got %+v", ev)
	}
}
