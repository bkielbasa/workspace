package postgres

import (
	"context"
	"database/sql"
	"os"
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
