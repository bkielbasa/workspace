# Notes & Lists Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a self-hosted Google Keep and Google Tasks replacement (Notes & Lists) for families, featuring a responsive Keep-style web app, real-time checklist collaboration via SSE, and native mobile sync via CalDAV VTODO.

**Architecture:** A dedicated `internal/notes` domain service backed by PostgreSQL relational tables (`notes`, `note_items`, `note_tags`), an RFC 5545 VTODO serializer/parser in `internal/format/vtodo`, CalDAV `/cal/lists/` task collections in `internal/caldav`, an in-memory pub/sub SSE event broker in `internal/notes`, and a server-rendered HTMX web interface at `/notes`.

**Tech Stack:** Go 1.22, PostgreSQL, HTML5/CSS, HTMX, Server-Sent Events (SSE), CalDAV / RFC 5545 iCalendar (VTODO).

**Spec:** `docs/superpowers/specs/2026-09-18-notes-and-lists-design.md`

## Global Constraints
- Target Go version: 1.22+
- Database: PostgreSQL with SQL migrations in `migrations/`
- All database IDs use UUIDs (`github.com/google/uuid`)
- CSRF protection: All state-changing web mutations require `_csrf` form token or `X-CSRF-Token` header
- Authentication: Session cookies for web UI (`RequireAuth`), Basic Auth with password/app passwords for CalDAV (`deviceAuth`)
- Privacy default: Notes and lists are private to the creator unless `is_family_shared = TRUE`
- Code formatting: Standard `gofmt` and idiomatic error wrapping

---

### Task 1: Database Migration for Notes, Checklist Items, and Tags

**Files:**
- Create: `migrations/026_notes.up.sql`
- Create: `migrations/026_notes.down.sql`

**Interfaces:**
- Consumes: `users` table from migration `001_users.up.sql`
- Produces: `notes`, `note_items`, `note_tags`, `notes_tags` database tables and indexes

- [ ] **Step 1: Write down migration file**

Create `migrations/026_notes.down.sql`:
```sql
DROP TABLE IF EXISTS notes_tags;
DROP TABLE IF EXISTS note_tags;
DROP TABLE IF EXISTS note_items;
DROP TABLE IF EXISTS notes;
```

- [ ] **Step 2: Write up migration file**

Create `migrations/026_notes.up.sql`:
```sql
CREATE TABLE IF NOT EXISTS notes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    kind VARCHAR(16) NOT NULL DEFAULT 'note', -- 'note' or 'list'
    color VARCHAR(24) NOT NULL DEFAULT 'default',
    is_pinned BOOLEAN NOT NULL DEFAULT FALSE,
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    is_family_shared BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notes_user_lookup ON notes(user_id, is_archived, is_pinned DESC, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_notes_family_shared ON notes(is_family_shared, is_archived, is_pinned DESC, updated_at DESC) WHERE is_family_shared = TRUE;

CREATE TABLE IF NOT EXISTS note_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    note_id UUID NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    completed_at TIMESTAMPTZ NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_note_items_note ON note_items(note_id, sort_order ASC, created_at ASC);

CREATE TABLE IF NOT EXISTS note_tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(60) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, name)
);

CREATE TABLE IF NOT EXISTS notes_tags (
    note_id UUID NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES note_tags(id) ON DELETE CASCADE,
    PRIMARY KEY(note_id, tag_id)
);
```

- [ ] **Step 3: Verify migration syntax**

Run: `git status` to verify migration files are positioned correctly in `migrations/`.

- [ ] **Step 4: Commit**

```bash
git add migrations/026_notes.up.sql migrations/026_notes.down.sql
git commit -m "feat(db): add migration for notes, items, and tags"
```

---

### Task 2: Domain Types & In-Memory Event Broker

**Files:**
- Create: `internal/notes/model.go`
- Create: `internal/notes/broker.go`
- Create: `internal/notes/broker_test.go`

**Interfaces:**
- Produces: `Note`, `NoteItem`, `NoteTag`, `Event`, `Broker`
- Consumes: `github.com/google/uuid`

- [ ] **Step 1: Write the failing test for Broker**

Create `internal/notes/broker_test.go`:
```go
package notes

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testEvent() Event {
	return Event{
		Type:      "item_toggled",
		NoteID:    uuid.New(),
		ItemID:    uuid.New(),
		Completed: true,
		UserID:    uuid.New(),
	}
}

func TestBrokerPublishSubscribe(t *testing.T) {
	broker := NewBroker()
	ch := broker.Subscribe()
	defer broker.Unsubscribe(ch)

	ev := testEvent()
	broker.Publish(ev)

	select {
	case received := <-ch:
		if received.NoteID != ev.NoteID || received.Completed != ev.Completed {
			t.Fatalf("expected %+v, got %+v", ev, received)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestBrokerUnsubscribe(t *testing.T) {
	broker := NewBroker()
	ch := broker.Subscribe()
	broker.Unsubscribe(ch)

	ev := testEvent()
	broker.Publish(ev)

	select {
	case received, ok := <-ch:
		if ok {
			t.Fatalf("unexpected event on unsubscribed channel: %+v", received)
		}
	default:
		// success: nothing pending
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notes/...`  
Expected: FAIL with compilation errors (`undefined: NewBroker`, `undefined: Event`).

- [ ] **Step 3: Implement domain models and Broker**

Create `internal/notes/model.go`:
```go
package notes

import (
	"time"

	"github.com/google/uuid"
)

type Kind string

const (
	KindNote Kind = "note"
	KindList Kind = "list"
)

type Note struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	Kind           Kind       `json:"kind"`
	Color          string     `json:"color"`
	IsPinned       bool       `json:"is_pinned"`
	IsArchived     bool       `json:"is_archived"`
	IsFamilyShared bool       `json:"is_family_shared"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Items          []NoteItem `json:"items,omitempty"`
	Tags           []string   `json:"tags,omitempty"`
}

type NoteItem struct {
	ID          uuid.UUID  `json:"id"`
	NoteID      uuid.UUID  `json:"note_id"`
	Content     string     `json:"content"`
	Completed   bool       `json:"completed"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	SortOrder   int        `json:"sort_order"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type NoteTag struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Event struct {
	Type      string    `json:"type"`
	NoteID    uuid.UUID `json:"note_id"`
	ItemID    uuid.UUID `json:"item_id,omitempty"`
	Completed bool      `json:"completed,omitempty"`
	UserID    uuid.UUID `json:"user_id"`
}
```

Create `internal/notes/broker.go`:
```go
package notes

import (
	"sync"
)

type Broker struct {
	mu          sync.RWMutex
	subscribers map[chan Event]struct{}
}

func NewBroker() *Broker {
	return &Broker{
		subscribers: make(map[chan Event]struct{}),
	}
}

func (b *Broker) Subscribe() chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan Event, 64)
	b.subscribers[ch] = struct{}{}
	return ch
}

func (b *Broker) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.subscribers[ch]; ok {
		delete(b.subscribers, ch)
		close(ch)
	}
}

func (b *Broker) Publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subscribers {
		select {
		case ch <- ev:
		default:
			// slow consumer drop to avoid head-of-line blocking
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notes/... -v`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/notes/model.go internal/notes/broker.go internal/notes/broker_test.go
git commit -m "feat(notes): add domain models and pub/sub event broker"
```

---

### Task 3: PostgreSQL Repository for Notes & Items

**Files:**
- Create: `internal/notes/repository.go`
- Create: `internal/postgres/notes.go`
- Create: `internal/postgres/notes_test.go`

**Interfaces:**
- Consumes: `internal/notes` types, `database/sql`
- Produces: `notes.Repository` interface implementation in `internal/postgres`

- [ ] **Step 1: Define repository interface**

Create `internal/notes/repository.go`:
```go
package notes

import (
	"context"

	"github.com/google/uuid"
)

type Repository interface {
	CreateNote(ctx context.Context, note Note) (*Note, error)
	GetNote(ctx context.Context, id uuid.UUID) (*Note, error)
	ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]Note, error)
	UpdateNote(ctx context.Context, note Note) (*Note, error)
	DeleteNote(ctx context.Context, id uuid.UUID) error

	AddItem(ctx context.Context, item NoteItem) (*NoteItem, error)
	GetItem(ctx context.Context, id uuid.UUID) (*NoteItem, error)
	ListItems(ctx context.Context, noteID uuid.UUID) ([]NoteItem, error)
	ToggleItem(ctx context.Context, id uuid.UUID, completed bool) (*NoteItem, error)
	UpdateItem(ctx context.Context, item NoteItem) (*NoteItem, error)
	DeleteItem(ctx context.Context, id uuid.UUID) error

	ListTags(ctx context.Context, userID uuid.UUID) ([]NoteTag, error)
	SetNoteTags(ctx context.Context, noteID uuid.UUID, userID uuid.UUID, tags []string) error
}
```

- [ ] **Step 2: Write repository tests**

Create `internal/postgres/notes_test.go`:
```go
package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

// Ensure postgres.NotesRepository implements notes.Repository
func TestNotesRepositoryInterface(t *testing.T) {
	var _ notes.Repository = (*notesRepository)(nil)
}
```

- [ ] **Step 3: Implement PostgreSQL repository**

Create `internal/postgres/notes.go`:
```go
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

type notesRepository struct {
	db *sql.DB
}

func NewNotesRepository(db *sql.DB) notes.Repository {
	return &notesRepository{db: db}
}

func (r *notesRepository) CreateNote(ctx context.Context, n notes.Note) (*notes.Note, error) {
	if n.ID == uuid.Nil {
		n.ID = uuid.New()
	}
	now := time.Now()
	n.CreatedAt = now
	n.UpdatedAt = now
	if n.Color == "" {
		n.Color = "default"
	}
	if n.Kind == "" {
		n.Kind = notes.KindNote
	}

	query := `
		INSERT INTO notes (id, user_id, title, body, kind, color, is_pinned, is_archived, is_family_shared, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, user_id, title, body, kind, color, is_pinned, is_archived, is_family_shared, created_at, updated_at
	`
	row := r.db.QueryRowContext(ctx, query,
		n.ID, n.UserID, n.Title, n.Body, string(n.Kind), n.Color, n.IsPinned, n.IsArchived, n.IsFamilyShared, n.CreatedAt, n.UpdatedAt,
	)

	var res notes.Note
	var kindStr string
	if err := row.Scan(
		&res.ID, &res.UserID, &res.Title, &res.Body, &kindStr, &res.Color,
		&res.IsPinned, &res.IsArchived, &res.IsFamilyShared, &res.CreatedAt, &res.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("create note: %w", err)
	}
	res.Kind = notes.Kind(kindStr)
	return &res, nil
}

func (r *notesRepository) GetNote(ctx context.Context, id uuid.UUID) (*notes.Note, error) {
	query := `
		SELECT id, user_id, title, body, kind, color, is_pinned, is_archived, is_family_shared, created_at, updated_at
		FROM notes WHERE id = $1
	`
	row := r.db.QueryRowContext(ctx, query, id)
	var res notes.Note
	var kindStr string
	if err := row.Scan(
		&res.ID, &res.UserID, &res.Title, &res.Body, &kindStr, &res.Color,
		&res.IsPinned, &res.IsArchived, &res.IsFamilyShared, &res.CreatedAt, &res.UpdatedAt,
	); err != nil {
		return nil, err
	}
	res.Kind = notes.Kind(kindStr)

	items, err := r.ListItems(ctx, res.ID)
	if err != nil {
		return nil, err
	}
	res.Items = items
	return &res, nil
}

func (r *notesRepository) ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]notes.Note, error) {
	query := `
		SELECT DISTINCT n.id, n.user_id, n.title, n.body, n.kind, n.color, n.is_pinned, n.is_archived, n.is_family_shared, n.created_at, n.updated_at
		FROM notes n
		LEFT JOIN notes_tags nt ON n.id = nt.note_id
		LEFT JOIN note_tags t ON nt.tag_id = t.id
		WHERE (n.user_id = $1 OR n.is_family_shared = TRUE)
		  AND n.is_archived = $2
		  AND ($3 = '' OR t.name = $3)
		ORDER BY n.is_pinned DESC, n.updated_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query, userID, archived, tag)
	if err != nil {
		return nil, fmt.Errorf("list notes: %w", err)
	}
	defer rows.Close()

	var result []notes.Note
	for rows.Next() {
		var n notes.Note
		var kindStr string
		if err := rows.Scan(
			&n.ID, &n.UserID, &n.Title, &n.Body, &kindStr, &n.Color,
			&n.IsPinned, &n.IsArchived, &n.IsFamilyShared, &n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			return nil, err
		}
		n.Kind = notes.Kind(kindStr)
		result = append(result, n)
	}

	for i := range result {
		items, err := r.ListItems(ctx, result[i].ID)
		if err == nil {
			result[i].Items = items
		}
	}
	return result, nil
}

func (r *notesRepository) UpdateNote(ctx context.Context, n notes.Note) (*notes.Note, error) {
	n.UpdatedAt = time.Now()
	query := `
		UPDATE notes
		SET title = $1, body = $2, color = $3, is_pinned = $4, is_archived = $5, is_family_shared = $6, updated_at = $7
		WHERE id = $8
		RETURNING id, user_id, title, body, kind, color, is_pinned, is_archived, is_family_shared, created_at, updated_at
	`
	row := r.db.QueryRowContext(ctx, query,
		n.Title, n.Body, n.Color, n.IsPinned, n.IsArchived, n.IsFamilyShared, n.UpdatedAt, n.ID,
	)
	var res notes.Note
	var kindStr string
	if err := row.Scan(
		&res.ID, &res.UserID, &res.Title, &res.Body, &kindStr, &res.Color,
		&res.IsPinned, &res.IsArchived, &res.IsFamilyShared, &res.CreatedAt, &res.UpdatedAt,
	); err != nil {
		return nil, err
	}
	res.Kind = notes.Kind(kindStr)
	return &res, nil
}

func (r *notesRepository) DeleteNote(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM notes WHERE id = $1", id)
	return err
}

func (r *notesRepository) AddItem(ctx context.Context, item notes.NoteItem) (*notes.NoteItem, error) {
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	now := time.Now()
	item.CreatedAt = now
	item.UpdatedAt = now

	query := `
		INSERT INTO note_items (id, note_id, content, completed, sort_order, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, note_id, content, completed, completed_at, sort_order, created_at, updated_at
	`
	row := r.db.QueryRowContext(ctx, query,
		item.ID, item.NoteID, item.Content, item.Completed, item.SortOrder, item.CreatedAt, item.UpdatedAt,
	)
	var res notes.NoteItem
	if err := row.Scan(
		&res.ID, &res.NoteID, &res.Content, &res.Completed, &res.CompletedAt, &res.SortOrder, &res.CreatedAt, &res.UpdatedAt,
	); err != nil {
		return nil, err
	}

	_, _ = r.db.ExecContext(ctx, "UPDATE notes SET updated_at = $1 WHERE id = $2", now, item.NoteID)
	return &res, nil
}

func (r *notesRepository) GetItem(ctx context.Context, id uuid.UUID) (*notes.NoteItem, error) {
	query := `
		SELECT id, note_id, content, completed, completed_at, sort_order, created_at, updated_at
		FROM note_items WHERE id = $1
	`
	row := r.db.QueryRowContext(ctx, query, id)
	var res notes.NoteItem
	if err := row.Scan(
		&res.ID, &res.NoteID, &res.Content, &res.Completed, &res.CompletedAt, &res.SortOrder, &res.CreatedAt, &res.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &res, nil
}

func (r *notesRepository) ListItems(ctx context.Context, noteID uuid.UUID) ([]notes.NoteItem, error) {
	query := `
		SELECT id, note_id, content, completed, completed_at, sort_order, created_at, updated_at
		FROM note_items WHERE note_id = $1 ORDER BY sort_order ASC, created_at ASC
	`
	rows, err := r.db.QueryContext(ctx, query, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []notes.NoteItem
	for rows.Next() {
		var item notes.NoteItem
		if err := rows.Scan(
			&item.ID, &item.NoteID, &item.Content, &item.Completed, &item.CompletedAt, &item.SortOrder, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (r *notesRepository) ToggleItem(ctx context.Context, id uuid.UUID, completed bool) (*notes.NoteItem, error) {
	now := time.Now()
	var completedAt *time.Time
	if completed {
		completedAt = &now
	}
	query := `
		UPDATE note_items
		SET completed = $1, completed_at = $2, updated_at = $3
		WHERE id = $4
		RETURNING id, note_id, content, completed, completed_at, sort_order, created_at, updated_at
	`
	row := r.db.QueryRowContext(ctx, query, completed, completedAt, now, id)
	var res notes.NoteItem
	if err := row.Scan(
		&res.ID, &res.NoteID, &res.Content, &res.Completed, &res.CompletedAt, &res.SortOrder, &res.CreatedAt, &res.UpdatedAt,
	); err != nil {
		return nil, err
	}
	_, _ = r.db.ExecContext(ctx, "UPDATE notes SET updated_at = $1 WHERE id = $2", now, res.NoteID)
	return &res, nil
}

func (r *notesRepository) UpdateItem(ctx context.Context, item notes.NoteItem) (*notes.NoteItem, error) {
	now := time.Now()
	query := `
		UPDATE note_items
		SET content = $1, sort_order = $2, updated_at = $3
		WHERE id = $4
		RETURNING id, note_id, content, completed, completed_at, sort_order, created_at, updated_at
	`
	row := r.db.QueryRowContext(ctx, query, item.Content, item.SortOrder, now, item.ID)
	var res notes.NoteItem
	if err := row.Scan(
		&res.ID, &res.NoteID, &res.Content, &res.Completed, &res.CompletedAt, &res.SortOrder, &res.CreatedAt, &res.UpdatedAt,
	); err != nil {
		return nil, err
	}
	_, _ = r.db.ExecContext(ctx, "UPDATE notes SET updated_at = $1 WHERE id = $2", now, res.NoteID)
	return &res, nil
}

func (r *notesRepository) DeleteItem(ctx context.Context, id uuid.UUID) error {
	var noteID uuid.UUID
	_ = r.db.QueryRowContext(ctx, "SELECT note_id FROM note_items WHERE id = $1", id).Scan(&noteID)
	_, err := r.db.ExecContext(ctx, "DELETE FROM note_items WHERE id = $1", id)
	if err == nil && noteID != uuid.Nil {
		_, _ = r.db.ExecContext(ctx, "UPDATE notes SET updated_at = $1 WHERE id = $2", time.Now(), noteID)
	}
	return err
}

func (r *notesRepository) ListTags(ctx context.Context, userID uuid.UUID) ([]notes.NoteTag, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id, user_id, name, created_at FROM note_tags WHERE user_id = $1 ORDER BY name ASC", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []notes.NoteTag
	for rows.Next() {
		var t notes.NoteTag
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.CreatedAt); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

func (r *notesRepository) SetNoteTags(ctx context.Context, noteID uuid.UUID, userID uuid.UUID, tagNames []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM notes_tags WHERE note_id = $1", noteID); err != nil {
		return err
	}

	for _, name := range tagNames {
		if name == "" {
			continue
		}
		var tagID uuid.UUID
		err := tx.QueryRowContext(ctx, `
			INSERT INTO note_tags (user_id, name) VALUES ($1, $2)
			ON CONFLICT (user_id, name) DO UPDATE SET name = EXCLUDED.name
			RETURNING id
		`, userID, name).Scan(&tagID)
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO notes_tags (note_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING", noteID, tagID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Run test to verify repository implementation**

Run: `go test ./internal/postgres -run TestNotesRepositoryInterface -v`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/notes/repository.go internal/postgres/notes.go internal/postgres/notes_test.go
git commit -m "feat(notes): implement PostgreSQL repository for notes and checklist items"
```

---

### Task 4: Notes Service & Access Control Matrix

**Files:**
- Create: `internal/notes/service.go`
- Create: `internal/notes/service_test.go`

**Interfaces:**
- Consumes: `notes.Repository`, `notes.Broker`, `identity.User`
- Produces: `notes.Service` with family access control methods

- [ ] **Step 1: Write failing tests for Service access control**

Create `internal/notes/service_test.go`:
```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notes/... -run TestServiceAuthorization`  
Expected: FAIL (`undefined: NewService`, `undefined: ErrNotFound`, `undefined: ErrForbidden`).

- [ ] **Step 3: Implement Notes Service with access control**

Create `internal/notes/service.go`:
```go
package notes

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrNotFound  = errors.New("note not found")
	ErrForbidden = errors.New("access denied")
)

type Service struct {
	repo   Repository
	broker *Broker
}

func NewService(repo Repository, broker *Broker) *Service {
	return &Service{
		repo:   repo,
		broker: broker,
	}
}

func (s *Service) Broker() *Broker {
	return s.broker
}

func (s *Service) CreateNote(ctx context.Context, userID uuid.UUID, n Note) (*Note, error) {
	n.UserID = userID
	created, err := s.repo.CreateNote(ctx, n)
	if err != nil {
		return nil, err
	}
	s.broker.Publish(Event{
		Type:   "note_created",
		NoteID: created.ID,
		UserID: userID,
	})
	return created, nil
}

func (s *Service) GetNote(ctx context.Context, userID uuid.UUID, id uuid.UUID) (*Note, error) {
	n, err := s.repo.GetNote(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	if n.UserID != userID && !n.IsFamilyShared {
		return nil, ErrNotFound
	}
	return n, nil
}

func (s *Service) ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]Note, error) {
	return s.repo.ListNotes(ctx, userID, archived, tag)
}

func (s *Service) UpdateNote(ctx context.Context, userID uuid.UUID, isAdmin bool, n Note) (*Note, error) {
	existing, err := s.repo.GetNote(ctx, n.ID)
	if err != nil {
		return nil, ErrNotFound
	}

	if existing.UserID != userID && !existing.IsFamilyShared {
		return nil, ErrNotFound
	}

	if existing.IsFamilyShared != n.IsFamilyShared && existing.UserID != userID && !isAdmin {
		return nil, ErrForbidden
	}

	updated, err := s.repo.UpdateNote(ctx, n)
	if err != nil {
		return nil, err
	}
	s.broker.Publish(Event{
		Type:   "note_updated",
		NoteID: updated.ID,
		UserID: userID,
	})
	return updated, nil
}

func (s *Service) DeleteNote(ctx context.Context, userID uuid.UUID, isAdmin bool, id uuid.UUID) error {
	existing, err := s.repo.GetNote(ctx, id)
	if err != nil {
		return ErrNotFound
	}
	if existing.UserID != userID && !isAdmin {
		return ErrForbidden
	}
	if err := s.repo.DeleteNote(ctx, id); err != nil {
		return err
	}
	s.broker.Publish(Event{
		Type:   "note_deleted",
		NoteID: id,
		UserID: userID,
	})
	return nil
}

func (s *Service) AddItem(ctx context.Context, userID uuid.UUID, noteID uuid.UUID, content string) (*NoteItem, error) {
	_, err := s.GetNote(ctx, userID, noteID)
	if err != nil {
		return nil, err
	}

	item, err := s.repo.AddItem(ctx, NoteItem{
		NoteID:  noteID,
		Content: content,
	})
	if err != nil {
		return nil, err
	}
	s.broker.Publish(Event{
		Type:      "item_added",
		NoteID:    noteID,
		ItemID:    item.ID,
		Completed: item.Completed,
		UserID:    userID,
	})
	return item, nil
}

func (s *Service) ToggleItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, completed bool) (*NoteItem, error) {
	_, err := s.GetNote(ctx, userID, noteID)
	if err != nil {
		return nil, err
	}

	item, err := s.repo.ToggleItem(ctx, itemID, completed)
	if err != nil {
		return nil, err
	}
	s.broker.Publish(Event{
		Type:      "item_toggled",
		NoteID:    noteID,
		ItemID:    item.ID,
		Completed: item.Completed,
		UserID:    userID,
	})
	return item, nil
}

func (s *Service) DeleteItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID) error {
	_, err := s.GetNote(ctx, userID, noteID)
	if err != nil {
		return err
	}

	if err := s.repo.DeleteItem(ctx, itemID); err != nil {
		return err
	}
	s.broker.Publish(Event{
		Type:   "item_deleted",
		NoteID: noteID,
		ItemID: itemID,
		UserID: userID,
	})
	return nil
}
```

- [ ] **Step 4: Run tests to verify passing**

Run: `go test ./internal/notes/... -v`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/notes/service.go internal/notes/service_test.go
git commit -m "feat(notes): implement service with authorization matrix and event broadcasting"
```

---

### Task 5: RFC 5545 VTODO Serializer & Parser

**Files:**
- Create: `internal/format/vtodo/vtodo.go`
- Create: `internal/format/vtodo/vtodo_test.go`

**Interfaces:**
- Consumes: `notes.NoteItem`
- Produces: `FormatVTODO(item notes.NoteItem) string`, `ParseVTODO(icsContent string) (*notes.NoteItem, error)`

- [ ] **Step 1: Write roundtrip test for VTODO format and parse**

Create `internal/format/vtodo/vtodo_test.go`:
```go
package vtodo

import (
	"strings"
	"testing"
	"time"

	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

func TestVTODOFormatAndParse(t *testing.T) {
	itemID := uuid.New()
	noteID := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)

	item := notes.NoteItem{
		ID:          itemID,
		NoteID:      noteID,
		Content:     "Buy oat milk & apples",
		Completed:   true,
		CompletedAt: &now,
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now,
	}

	ics := Format(item)
	if !strings.Contains(ics, "BEGIN:VTODO") || !strings.Contains(ics, "STATUS:COMPLETED") {
		t.Fatalf("unexpected formatted VTODO:\n%s", ics)
	}

	parsed, err := Parse(ics)
	if err != nil {
		t.Fatalf("failed to parse formatted VTODO: %v", err)
	}

	if parsed.ID != item.ID {
		t.Errorf("expected ID %v, got %v", item.ID, parsed.ID)
	}
	if parsed.Content != item.Content {
		t.Errorf("expected Content %q, got %q", item.Content, parsed.Content)
	}
	if !parsed.Completed {
		t.Errorf("expected Completed = true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/format/vtodo/...`  
Expected: FAIL (`undefined: Format`, `undefined: Parse`).

- [ ] **Step 3: Implement VTODO serialization and parser**

Create `internal/format/vtodo/vtodo.go`:
```go
package vtodo

import (
	"bufio"
	"fmt"
	"strings"
	"time"

	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

const icsTimeFormat = "20060102T150405Z"

func Format(item notes.NoteItem) string {
	var sb strings.Builder
	sb.WriteString("BEGIN:VCALENDAR\r\n")
	sb.WriteString("VERSION:2.0\r\n")
	sb.WriteString("PRODID:-//Workspace//Notes VTODO 1.0//EN\r\n")
	sb.WriteString("BEGIN:VTODO\r\n")
	sb.WriteString(fmt.Sprintf("UID:%s\r\n", item.ID.String()))
	sb.WriteString(fmt.Sprintf("DTSTAMP:%s\r\n", time.Now().UTC().Format(icsTimeFormat)))
	sb.WriteString(fmt.Sprintf("CREATED:%s\r\n", item.CreatedAt.UTC().Format(icsTimeFormat)))
	sb.WriteString(fmt.Sprintf("LAST-MODIFIED:%s\r\n", item.UpdatedAt.UTC().Format(icsTimeFormat)))
	sb.WriteString(fmt.Sprintf("SUMMARY:%s\r\n", escapeText(item.Content)))

	if item.Completed {
		sb.WriteString("STATUS:COMPLETED\r\n")
		if item.CompletedAt != nil {
			sb.WriteString(fmt.Sprintf("COMPLETED:%s\r\n", item.CompletedAt.UTC().Format(icsTimeFormat)))
		}
	} else {
		sb.WriteString("STATUS:NEEDS-ACTION\r\n")
	}

	sb.WriteString("END:VTODO\r\n")
	sb.WriteString("END:VCALENDAR\r\n")
	return sb.String()
}

func Parse(ics string) (*notes.NoteItem, error) {
	scanner := bufio.NewScanner(strings.NewReader(ics))
	item := &notes.NoteItem{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 2 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		switch {
		case key == "UID":
			if id, err := uuid.Parse(val); err == nil {
				item.ID = id
			}
		case key == "SUMMARY":
			item.Content = unescapeText(val)
		case key == "STATUS":
			if strings.EqualFold(val, "COMPLETED") {
				item.Completed = true
			}
		case key == "COMPLETED":
			if t, err := time.Parse(icsTimeFormat, val); err == nil {
				item.CompletedAt = &t
			}
		}
	}

	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	return item, nil
}

func escapeText(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

func unescapeText(s string) string {
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\,", ",")
	s = strings.ReplaceAll(s, "\\;", ";")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/format/vtodo/... -v`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/format/vtodo/vtodo.go internal/format/vtodo/vtodo_test.go
git commit -m "feat(format): add RFC 5545 VTODO serializer and parser"
```

---

### Task 6: CalDAV Task Collection Integration

**Files:**
- Modify: `internal/caldav/caldav.go`
- Create: `internal/caldav/caldav_tasks_test.go`

**Interfaces:**
- Consumes: `notes.Service`, `identity.DeviceAuth`, `format/vtodo`
- Produces: CalDAV `/cal/lists/{note_id}/` endpoint handler and `VTODO` collection discovery

- [ ] **Step 1: Write integration test for CalDAV task collection discovery and PUT**

Create `internal/caldav/caldav_tasks_test.go`:
```go
package caldav

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/bklimczak/workspace/internal/identity"
	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

type dummyAuth struct {
	user *identity.User
}

func (d *dummyAuth) Authenticate(ctx context.Context, email, password string) (*identity.User, error) {
	return d.user, nil
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

func TestCalDAVListsPROPFIND(t *testing.T) {
	testUser := &identity.User{
		ID:    uuid.New(),
		Email: "alice@example.com",
	}
	auth := &dummyAuth{user: testUser}
	h := NewWithTasks(&dummyCalendar{}, nil, auth)

	req := httptest.NewRequest("PROPFIND", "/cal/", strings.NewReader(`<?xml version="1.0" encoding="utf-8" ?><D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/></D:prop></D:propfind>`))
	req.SetBasicAuth("alice@example.com", "secret")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)
	if w.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 MultiStatus, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/caldav -run TestCalDAVListsPROPFIND`  
Expected: FAIL (`undefined: NewWithTasks`).

- [ ] **Step 3: Extend CalDAV handler with Notes & Lists support**

Modify `internal/caldav/caldav.go`:
Add `notesService` interface:
```go
type notesService interface {
	ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]notes.Note, error)
	GetNote(ctx context.Context, userID uuid.UUID, id uuid.UUID) (*notes.Note, error)
	AddItem(ctx context.Context, userID uuid.UUID, noteID uuid.UUID, content string) (*notes.NoteItem, error)
	ToggleItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, completed bool) (*notes.NoteItem, error)
	DeleteItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID) error
}
```
Update `handler` struct and add `NewWithTasks`:
```go
type handler struct {
	calendar calendarService
	notes    notesService
	users    authenticator
}

func NewWithTasks(cal calendarService, notes notesService, users authenticator) http.Handler {
	return &handler{calendar: cal, notes: notes, users: users}
}
```
In `ServeHTTP`, route requests targeting `/cal/lists/` to handle task collections and task VTODO resources using `internal/format/vtodo`.

- [ ] **Step 4: Run CalDAV tests to verify passing**

Run: `go test ./internal/caldav/... -v`  
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/caldav/caldav.go internal/caldav/caldav_tasks_test.go
git commit -m "feat(caldav): support VTODO collections and Apple Reminders sync"
```

---

### Task 7: Web UI Templates & Keep-Style Board Styles

**Files:**
- Create: `web/templates/notes.html`
- Modify: `web/templates/nav.html:1-19`
- Modify: `web/static/css/app.css` (or relevant workspace CSS file)

**Interfaces:**
- Produces: Google Keep card grid layout, quick-add bar, pastel color classes, checklist strikethroughs, and navigation link

- [ ] **Step 1: Update navigation bar to include Notes**

In `web/templates/nav.html`, insert the Notes link after Calendars:
```html
<a href="/calendars" {{if eq .Section "calendars"}}aria-current="page"{{end}}>Calendars</a>
<a href="/notes" {{if eq .Section "notes"}}aria-current="page"{{end}}>Notes</a>
<a href="/drive" {{if eq .Section "drive"}}aria-current="page"{{end}}>Files</a>
```

- [ ] **Step 2: Create `web/templates/notes.html`**

```html
{{define "content"}}<section class="notes-page" hx-ext="sse" sse-connect="/notes/live">
  <div class="notes-topbar card">
    <form class="notes-quickadd" hx-post="/notes" hx-target="#notes-container" hx-swap="outerHTML">
      <input type="hidden" name="_csrf" value="{{.CSRFToken}}">
      <div class="notes-quickadd-head">
        <input type="text" name="title" placeholder="Title" autocomplete="off" class="notes-input-title">
        <button type="submit" class="btn btn-sm">Save</button>
      </div>
      <textarea name="body" placeholder="Take a note or list item..." rows="2" class="notes-input-body"></textarea>
      <div class="notes-quickadd-actions">
        <label><input type="radio" name="kind" value="note" checked> Note</label>
        <label><input type="radio" name="kind" value="list"> Checklist</label>
        <label class="notes-family-toggle"><input type="checkbox" name="is_family_shared" value="true"> 👨‍👩‍👧‍👦 Share with family</label>
        <select name="color" class="notes-color-select">
          <option value="default">Default</option>
          <option value="coral">Coral</option>
          <option value="peach">Peach</option>
          <option value="sand">Sand</option>
          <option value="mint">Mint</option>
          <option value="sage">Sage</option>
          <option value="fog">Fog</option>
          <option value="storm">Storm</option>
          <option value="blossom">Blossom</option>
          <option value="clay">Clay</option>
        </select>
      </div>
    </form>
  </div>

  <div id="notes-container">
    {{template "notesList" .}}
  </div>
</section>
{{end}}

{{define "notesList"}}
<div id="notes-container" class="notes-grid">
  {{range .Notes}}
  <article class="note-card note-color-{{.Color}}{{if .IsPinned}} note-pinned{{end}}" id="note-{{.ID}}">
    <div class="note-card-head">
      <h3 class="note-card-title">{{if .Title}}{{.Title}}{{else}}Untitled{{end}}</h3>
      <div class="note-card-badges">
        {{if .IsFamilyShared}}<span class="note-badge-family" title="Shared with family">👨‍👩‍👧‍👦 Family</span>{{end}}
      </div>
    </div>

    {{if eq .Kind "list"}}
    <ul class="note-checklist">
      {{range .Items}}
      <li class="note-checkitem{{if .Completed}} completed{{end}}" id="item-{{.ID}}">
        <form class="inline-form" hx-post="/notes/{{.NoteID}}/items/{{.ID}}/toggle" hx-target="#note-{{.NoteID}}" hx-swap="outerHTML">
          <input type="hidden" name="_csrf" value="{{$.CSRFToken}}">
          <input type="checkbox" {{if .Completed}}checked{{end}} onchange="this.form.requestSubmit()">
          <span class="note-item-text">{{.Content}}</span>
        </form>
      </li>
      {{end}}
    </ul>
    <form class="note-additem-form" hx-post="/notes/{{.ID}}/items" hx-target="#note-{{.ID}}" hx-swap="outerHTML">
      <input type="hidden" name="_csrf" value="{{$.CSRFToken}}">
      <input type="text" name="content" placeholder="+ Add item" autocomplete="off" class="note-input-additem">
    </form>
    {{else}}
    <div class="note-card-body">{{.Body}}</div>
    {{end}}

    <div class="note-card-foot">
      <form class="inline-form" hx-post="/notes/{{.ID}}/toggle-pin" hx-target="#notes-container" hx-swap="outerHTML">
        <input type="hidden" name="_csrf" value="{{$.CSRFToken}}">
        <button type="submit" class="btn btn-ghost btn-sm" title="Pin / Unpin">{{if .IsPinned}}📌 Pinned{{else}}📍 Pin{{end}}</button>
      </form>
      <form class="inline-form" hx-post="/notes/{{.ID}}/delete" hx-target="#notes-container" hx-swap="outerHTML" hx-confirm="Delete this note?">
        <input type="hidden" name="_csrf" value="{{$.CSRFToken}}">
        <button type="submit" class="btn btn-ghost btn-sm" title="Delete">&times;</button>
      </form>
    </div>
  </article>
  {{else}}
  <div class="mail-empty-state card">
    <div class="mail-empty-icon">📝</div>
    <h3 class="mail-empty-title">No notes or lists yet</h3>
    <p class="mail-empty-subtext">Add a note or grocery list above. Toggle "Share with family" so everyone sees it!</p>
  </div>
  {{end}}
</div>
{{end}}
```

- [ ] **Step 3: Add CSS styles for note cards and colors**

Add responsive styles, grid columns, pastel color themes, and strikethroughs to the static CSS.

- [ ] **Step 4: Verify template syntax by compiling**

Run: `go build ./...`

- [ ] **Step 5: Commit**

```bash
git add web/templates/notes.html web/templates/nav.html web/static/css/
git commit -m "feat(web): add Keep-style notes and checklist templates and styles"
```

---

### Task 8: Web HTTP Handlers & Live SSE Streaming

**Files:**
- Create: `internal/web/notes_http.go`
- Create: `internal/web/notes_test.go`
- Modify: `internal/web/web_http.go`
- Modify: `internal/web/view.go`

**Interfaces:**
- Consumes: `notes.Service`, `web.Server`
- Produces: HTTP handlers for `/notes`, `/notes/live`, `/notes/{id}/items/...`

- [ ] **Step 1: Write failing web handler tests**

Create `internal/web/notes_test.go`:
```go
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
```

- [ ] **Step 2: Run test to verify it passes auth check**

Run: `go test ./internal/web -run TestNotesPageRequiresAuth -v`  
Expected: PASS

- [ ] **Step 3: Implement `internal/web/notes_http.go`**

Create `internal/web/notes_http.go`:
```go
package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/bklimczak/workspace/internal/notes"
	"github.com/google/uuid"
)

func (s *Server) notesPage(w http.ResponseWriter, r *http.Request) {
	user := s.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	notesList, err := s.notes.ListNotes(r.Context(), user.ID, false, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"Section":   "notes",
		"User":      user,
		"CSRFToken": s.csrfToken(r),
		"Notes":     notesList,
	}
	s.render(w, "notes.html", data)
}

func (s *Server) notesLiveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.notes.Broker().Subscribe()
	defer s.notes.Broker().Unsubscribe(ch)

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: note_update\ndata: {\"note_id\":\"%s\",\"type\":\"%s\"}\n\n", ev.NoteID, ev.Type)
			flusher.Flush()
		}
	}
}
```

- [ ] **Step 4: Register routes in `internal/web/web_http.go`**

In `RegisterRoutes(mux *http.ServeMux)`:
```go
mux.HandleFunc("GET /notes", s.page(s.notesPage))
mux.HandleFunc("GET /notes/live", s.RequireAuth(s.notesLiveSSE))
mux.HandleFunc("POST /notes", s.RequireAuth(s.RequireCSRF(s.notesCreate)))
mux.HandleFunc("POST /notes/{id}/items", s.RequireAuth(s.RequireCSRF(s.notesAddItem)))
mux.HandleFunc("POST /notes/{id}/items/{itemID}/toggle", s.RequireAuth(s.RequireCSRF(s.notesToggleItem)))
mux.HandleFunc("POST /notes/{id}/delete", s.RequireAuth(s.RequireCSRF(s.notesDelete)))
```

- [ ] **Step 5: Run tests to verify**

Run: `go test ./internal/web/... -v`  
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/web/notes_http.go internal/web/notes_test.go internal/web/web_http.go
git commit -m "feat(web): add HTTP handlers and SSE stream for notes and checklists"
```

---

### Task 9: Wiring in `main.go`, Discovery & End-to-End Verification

**Files:**
- Modify: `main.go`
- Modify: `internal/discovery/discovery.go` (if autodiscovery needs task reminder profiles)
- Modify: `README.md`

**Interfaces:**
- Wire `notes.NewService`, `postgres.NewNotesRepository`, `caldav.NewWithTasks`, and `webUI.SetNotes`

- [ ] **Step 1: Wire notes service in `main.go`**

In `main.go`:
1. Initialize repository: `notesRepo := postgres.NewNotesRepository(db)`
2. Initialize service: `notesSvc := notes.NewService(notesRepo, notes.NewBroker())`
3. Pass `notesSvc` to `caldav.NewWithTasks(...)`
4. Register `webUI.SetNotes(notesSvc)`

- [ ] **Step 2: Build and run test suite**

Run: `go test ./...`  
Expected: PASS across all packages.

- [ ] **Step 3: Update README.md with Notes & Lists documentation**

Add **Notes & Lists (Google Keep alternative)** to key features and route documentation in `README.md`.

- [ ] **Step 4: Commit**

```bash
git add main.go README.md
git commit -m "feat: wire Notes & Lists service, CalDAV tasks, and update documentation"
```
