package models

import (
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if err := InitDB(":memory:"); err != nil {
		panic(err)
	}
	m.Run()
}

func TestInsertAndGetGame(t *testing.T) {
	g := &Game{
		ChessComID:  "tutor-test-unique-1",
		White:       "You",
		Black:       "Tutor",
		Result:      "win",
		Termination: "tutor",
		TimeClass:   "tutor",
		PGN:         "[Event \"tutor\"]\n1. e4 e5 *",
		PlayedAt:    time.Now(),
		Username:    "You",
		WhiteElo:    1200,
	}
	id, err := InsertGame(g)
	if err != nil {
		t.Fatalf("InsertGame: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero game id")
	}
	got, err := GetGame(id)
	if err != nil {
		t.Fatalf("GetGame: %v", err)
	}
	if got.White != "You" || got.Black != "Tutor" || got.Result != "win" {
		t.Fatalf("game roundtrip mismatch: %+v", got)
	}

	// INSERT OR IGNORE must dedupe by chesscom_id.
	dup, err := InsertGame(g)
	if err != nil {
		t.Fatalf("duplicate InsertGame: %v", err)
	}
	if dup != id {
		t.Fatalf("expected dedupe to return original id, got %d vs %d", dup, id)
	}
}

func TestReplaceAndGetMoveStats(t *testing.T) {
	g := &Game{ChessComID: "tutor-test-stats", White: "You", Black: "Tutor", Result: "loss", TimeClass: "tutor", PlayedAt: time.Now()}
	id, err := InsertGame(g)
	if err != nil {
		t.Fatalf("InsertGame: %v", err)
	}

	stats := []MoveStat{
		{GameID: id, MoveNumber: 1, Side: "white", SAN: "e4", CPL: 10, Classification: "good", Phase: "opening", Pattern: "quiet", IsStudent: true},
		{GameID: id, MoveNumber: 1, Side: "black", SAN: "e5", CPL: 300, Classification: "blunder", Phase: "opening", Pattern: "capture", MissedTactic: true, IsStudent: false},
	}
	if err := ReplaceMoveStats(id, stats); err != nil {
		t.Fatalf("ReplaceMoveStats: %v", err)
	}
	got, err := GetMoveStats(id)
	if err != nil {
		t.Fatalf("GetMoveStats: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 stats, got %d", len(got))
	}
	if got[1].Classification != "blunder" || !got[1].MissedTactic || !got[1].IsStudent == false {
		t.Fatalf("stat roundtrip mismatch: %+v", got[1])
	}

	// Replacing again must overwrite, not accumulate.
	if err := ReplaceMoveStats(id, stats[:1]); err != nil {
		t.Fatalf("ReplaceMoveStats: %v", err)
	}
	got, err = GetMoveStats(id)
	if err != nil {
		t.Fatalf("GetMoveStats: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 stat after replace, got %d", len(got))
	}
}

func TestSettings(t *testing.T) {
	if err := SetSetting("test_key", "test_value"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	val, err := GetSetting("test_key")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if val != "test_value" {
		t.Fatalf("expected test_value, got %q", val)
	}
	missing, err := GetSetting("no_such_key")
	if err != nil {
		t.Fatalf("GetSetting missing: %v", err)
	}
	if missing != "" {
		t.Fatalf("expected empty for missing key, got %q", missing)
	}
}
