package postgres

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/bklimczak/workspace/internal/calendar"
	"github.com/google/uuid"
)

// TestSchedulingColumnsRoundtrip exercises attendees/sequence against a real
// database. It runs only when TEST_DATABASE_URL is set.
func TestSchedulingColumnsRoundtrip(t *testing.T) {
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
	repo := NewCalendarRepository(db)
	userID := uuid.New()

	saved, err := repo.Put(ctx, calendar.Event{
		UserID:    userID,
		Title:     "Sched",
		Resource:  "sched-" + uuid.NewString(),
		UID:       "sched-uid-1",
		Attendees: []string{"a@example.com", "b@example.com"},
		Sequence:  4,
		ETag:      "e1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Attendees) != 2 || saved.Sequence != 4 {
		t.Fatalf("roundtrip mismatch: %+v", saved)
	}
	got, err := repo.GetByUID(ctx, userID, "sched-uid-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Attendees) != 2 || got.Sequence != 4 {
		t.Fatalf("lookup mismatch: %+v", got)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM events WHERE user_id = $1`, userID); err != nil {
		t.Fatal(err)
	}
}

// TestGetForUserRepairsSeparator stores a message the way the old ingest
// did (headers glued to the body) and requires the detail-page read path
// to hand back a parseable message.
func TestGetForUserRepairsSeparator(t *testing.T) {
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

	var userID uuid.UUID
	err = db.QueryRowContext(ctx, `
		INSERT INTO users (email, username, password_hash, display_name)
		VALUES ('repair-test@example.com', 'repairtest', 'x', 'Repair Test')
		RETURNING id
	`).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)

	var boxID uuid.UUID
	err = db.QueryRowContext(ctx, `
		INSERT INTO mailboxes (user_id, name) VALUES ($1, 'INBOX') RETURNING id
	`, userID).Scan(&boxID)
	if err != nil {
		t.Fatal(err)
	}

	corrupt := "From: a@b.c\r\n" +
		"To: repair-test@example.com\r\n" +
		"Subject: Broken\r\n" +
		"Content-Type: multipart/mixed; boundary=\"rb1\"\r\n" +
		"--rb1\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"hello\r\n" +
		"--rb1--\r\n"
	var msgID uuid.UUID
	err = db.QueryRowContext(ctx, `
		INSERT INTO messages (mailbox_id, message_id, sender, subject, in_reply_to, references_header, raw_message, mime_type, charset)
		VALUES ($1, '<repair-test@localhost>', 'a@b.c', 'Broken', '', '', $2, '', '') RETURNING id
	`, boxID, corrupt).Scan(&msgID)
	if err != nil {
		t.Fatal(err)
	}

	repo := NewMessageRepository(db)
	got, _, err := repo.GetForUser(ctx, userID, msgID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.RawMessage, "boundary=\"rb1\"\r\n\r\n--rb1") {
		t.Fatalf("separator not repaired:\n%q", got.RawMessage[:160])
	}
}
