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
		t.Fatalf("expected ErrNotFound for non-owner private note, got: %v", err)
	}

	// 3. Alice makes note family shared
	note.IsFamilyShared = true
	_, err = svc.UpdateNote(ctx, userAlice, false, *note)
	if err != nil {
		t.Fatalf("failed to update note: %v", err)
	}

	// 4. Bob can now view and add items to the shared note
	got, err := svc.GetNote(ctx, userBob, note.ID)
	if err != nil {
		t.Fatalf("expected Bob to view family shared note, got: %v", err)
	}
	if got.Title != "Secret Diary" {
		t.Fatalf("expected title match, got %s", got.Title)
	}

	// 5. Bob tries to delete Alice's note -> should be ErrForbidden (only owner or admin can delete)
	err = svc.DeleteNote(ctx, userBob, false, note.ID)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for non-owner deleting note, got: %v", err)
	}
}
