package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestProbeWebtestList(t *testing.T) {
	db, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	userID := uuid.MustParse("8f19c63e-07c5-4051-9996-adc3f8e0ff40")
	mb, err := NewMailboxRepository(db).GetByName(ctx, userID, "INBOX")
	if err != nil {
		t.Fatalf("getbox: %v", err)
	}
	t.Logf("box=%s", mb.ID)
	msgs, err := NewMessageRepository(db).List(ctx, mb.ID, 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	t.Logf("count=%d", len(msgs))
}
