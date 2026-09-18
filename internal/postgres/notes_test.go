package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

// Ensure postgres.NotesRepository implements notes.Repository
func TestNotesRepositoryInterface(t *testing.T) {
	var _ notes.Repository = (*notesRepository)(nil)
}

func TestNotesRepository(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	repo := NewNotesRepository(db)

	// Create test user
	var userID uuid.UUID
	err = db.QueryRowContext(ctx, `
		INSERT INTO users (email, username, password_hash, display_name)
		VALUES ('notes-test@example.com', 'notestest', 'x', 'Notes Test')
		RETURNING id
	`).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID)

	// 1. Create a Note
	note1, err := repo.CreateNote(ctx, notes.Note{
		UserID: userID,
		Title:  "First Note",
		Body:   "Hello from notes repository",
		Kind:   notes.KindNote,
		Color:  "blue",
	})
	if err != nil {
		t.Fatalf("CreateNote failed: %v", err)
	}
	if note1.Title != "First Note" || note1.Body != "Hello from notes repository" || note1.Color != "blue" {
		t.Fatalf("Note values mismatch: %+v", note1)
	}

	// 2. Get the Note
	fetched, err := repo.GetNote(ctx, note1.ID)
	if err != nil {
		t.Fatalf("GetNote failed: %v", err)
	}
	if fetched.ID != note1.ID || fetched.Title != note1.Title {
		t.Fatalf("GetNote mismatch: fetched=%+v, expected=%+v", fetched, note1)
	}

	// 3. Update the Note
	note1.Title = "First Note (Updated)"
	note1.IsPinned = true
	updated, err := repo.UpdateNote(ctx, *note1)
	if err != nil {
		t.Fatalf("UpdateNote failed: %v", err)
	}
	if !updated.IsPinned || updated.Title != "First Note (Updated)" {
		t.Fatalf("UpdateNote mismatch: %+v", updated)
	}

	// 4. Add Items to note (turning it into a checklist/list if we want, or just testing list items)
	item1, err := repo.AddItem(ctx, notes.NoteItem{
		NoteID:    note1.ID,
		Content:   "Task 1",
		SortOrder: 1,
	})
	if err != nil {
		t.Fatalf("AddItem failed: %v", err)
	}
	if item1.Content != "Task 1" || item1.Completed {
		t.Fatalf("Item1 mismatch: %+v", item1)
	}

	item2, err := repo.AddItem(ctx, notes.NoteItem{
		NoteID:    note1.ID,
		Content:   "Task 2",
		SortOrder: 2,
	})
	if err != nil {
		t.Fatalf("AddItem failed: %v", err)
	}

	// 5. Get and List Items
	fetchedItem, err := repo.GetItem(ctx, item1.ID)
	if err != nil {
		t.Fatalf("GetItem failed: %v", err)
	}
	if fetchedItem.Content != "Task 1" {
		t.Fatalf("GetItem mismatch: %+v", fetchedItem)
	}

	items, err := repo.ListItems(ctx, note1.ID)
	if err != nil {
		t.Fatalf("ListItems failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(items))
	}
	if items[0].ID != item1.ID || items[1].ID != item2.ID {
		t.Fatalf("ListItems sort or content mismatch: %+v", items)
	}

	// 6. Toggle Item
	toggled, err := repo.ToggleItem(ctx, item1.ID, true)
	if err != nil {
		t.Fatalf("ToggleItem failed: %v", err)
	}
	if !toggled.Completed || toggled.CompletedAt == nil {
		t.Fatalf("Expected toggled item to be completed: %+v", toggled)
	}

	// 7. Update Item
	item2.Content = "Task 2 (Edited)"
	item2.SortOrder = 0 // move to beginning
	updatedItem, err := repo.UpdateItem(ctx, *item2)
	if err != nil {
		t.Fatalf("UpdateItem failed: %v", err)
	}
	if updatedItem.Content != "Task 2 (Edited)" || updatedItem.SortOrder != 0 {
		t.Fatalf("UpdateItem mismatch: %+v", updatedItem)
	}

	// Re-list and verify ordering
	items, err = repo.ListItems(ctx, note1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].ID != item2.ID || items[1].ID != item1.ID {
		t.Fatalf("Expected item2 to be first due to sort order: %+v", items)
	}

	// 8. Tags operations
	err = repo.SetNoteTags(ctx, note1.ID, userID, []string{"important", "work"})
	if err != nil {
		t.Fatalf("SetNoteTags failed: %v", err)
	}

	// Fetch note again and check tags
	fetched, err = repo.GetNote(ctx, note1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(fetched.Tags) != 2 || fetched.Tags[0] != "important" || fetched.Tags[1] != "work" {
		t.Fatalf("Expected tags [important, work], got: %v", fetched.Tags)
	}

	// List tags for user
	allTags, err := repo.ListTags(ctx, userID)
	if err != nil {
		t.Fatalf("ListTags failed: %v", err)
	}
	if len(allTags) != 2 || allTags[0].Name != "important" || allTags[1].Name != "work" {
		t.Fatalf("Expected user tags, got: %+v", allTags)
	}

	// 9. List notes with and without tag filtering
	notesList, err := repo.ListNotes(ctx, userID, false, "work")
	if err != nil {
		t.Fatalf("ListNotes with tag failed: %v", err)
	}
	if len(notesList) != 1 || notesList[0].ID != note1.ID {
		t.Fatalf("ListNotes expected 1 note, got: %+v", notesList)
	}

	notesListEmpty, err := repo.ListNotes(ctx, userID, false, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if len(notesListEmpty) != 0 {
		t.Fatalf("Expected 0 notes, got: %d", len(notesListEmpty))
	}

	// 10. Delete Item & Delete Note
	err = repo.DeleteItem(ctx, item1.ID)
	if err != nil {
		t.Fatalf("DeleteItem failed: %v", err)
	}
	itemsAfterDelete, err := repo.ListItems(ctx, note1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsAfterDelete) != 1 || itemsAfterDelete[0].ID != item2.ID {
		t.Fatalf("Expected only item2, got: %+v", itemsAfterDelete)
	}

	err = repo.DeleteNote(ctx, note1.ID)
	if err != nil {
		t.Fatalf("DeleteNote failed: %v", err)
	}

	// Get note should fail or return error
	_, err = repo.GetNote(ctx, note1.ID)
	if err == nil {
		t.Fatal("Expected GetNote on deleted note to fail")
	}
}
