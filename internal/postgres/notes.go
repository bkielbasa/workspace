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

	tags, err := r.getNoteTags(ctx, res.ID)
	if err != nil {
		return nil, fmt.Errorf("get note tags: %w", err)
	}
	res.Tags = tags

	return &res, nil
}

func (r *notesRepository) ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]notes.Note, error) {
	query := `
		SELECT DISTINCT n.id, n.user_id, n.title, n.body, n.kind, n.color,
		       n.is_pinned, n.is_archived, n.is_family_shared, n.created_at, n.updated_at
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list notes iterate: %w", err)
	}

	if len(result) == 0 {
		return result, nil
	}

	noteIDs := make([]uuid.UUID, len(result))
	noteIndexMap := make(map[uuid.UUID]int, len(result))
	for i, n := range result {
		noteIDs[i] = n.ID
		noteIndexMap[n.ID] = i
	}

	itemsQuery := `
		SELECT id, note_id, content, completed, completed_at, sort_order, created_at, updated_at
		FROM note_items
		WHERE note_id = ANY($1)
		ORDER BY sort_order ASC, created_at ASC
	`
	itemRows, err := r.db.QueryContext(ctx, itemsQuery, noteIDs)
	if err != nil {
		return nil, fmt.Errorf("list notes items: %w", err)
	}
	defer itemRows.Close()

	for itemRows.Next() {
		var item notes.NoteItem
		if err := itemRows.Scan(
			&item.ID, &item.NoteID, &item.Content, &item.Completed, &item.CompletedAt, &item.SortOrder, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan note item: %w", err)
		}
		if idx, ok := noteIndexMap[item.NoteID]; ok {
			result[idx].Items = append(result[idx].Items, item)
		}
	}
	if err := itemRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate note items: %w", err)
	}

	tagsQuery := `
		SELECT nt.note_id, t.name
		FROM note_tags t
		JOIN notes_tags nt ON t.id = nt.tag_id
		WHERE nt.note_id = ANY($1)
		ORDER BY t.name ASC
	`
	tagRows, err := r.db.QueryContext(ctx, tagsQuery, noteIDs)
	if err != nil {
		return nil, fmt.Errorf("list notes tags: %w", err)
	}
	defer tagRows.Close()

	for tagRows.Next() {
		var noteID uuid.UUID
		var tagName string
		if err := tagRows.Scan(&noteID, &tagName); err != nil {
			return nil, fmt.Errorf("scan note tag: %w", err)
		}
		if idx, ok := noteIndexMap[noteID]; ok {
			result[idx].Tags = append(result[idx].Tags, tagName)
		}
	}
	if err := tagRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate note tags: %w", err)
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
	err := r.db.QueryRowContext(ctx, "DELETE FROM note_items WHERE id = $1 RETURNING note_id", id).Scan(&noteID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil // item wasn't present, no-op
		}
		return err
	}
	if noteID != uuid.Nil {
		_, _ = r.db.ExecContext(ctx, "UPDATE notes SET updated_at = $1 WHERE id = $2", time.Now(), noteID)
	}
	return nil
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

// getNoteTags is an internal helper to retrieve tags associated with a specific note.
func (r *notesRepository) getNoteTags(ctx context.Context, noteID uuid.UUID) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.name FROM note_tags t
		JOIN notes_tags nt ON t.id = nt.tag_id
		WHERE nt.note_id = $1
		ORDER BY t.name ASC
	`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tags = append(tags, name)
	}
	return tags, nil
}
