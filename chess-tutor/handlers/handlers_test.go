package handlers

import (
	"strings"
	"testing"

	"github.com/notnil/chess"
)

func TestBuildTutorPGN(t *testing.T) {
	pgn := buildTutorPGN([]string{"e4", "e5", "Nf3"}, "white", "loss")
	for _, want := range []string{
		`[White "You"]`,
		`[Black "Tutor"]`,
		`[Result "0-1"]`,
		"1. e4 e5 2. Nf3",
		"0-1",
	} {
		if !strings.Contains(pgn, want) {
			t.Fatalf("PGN missing %q:\n%s", want, pgn)
		}
	}
}

func TestBuildTutorPGNBlackPerspective(t *testing.T) {
	pgn := buildTutorPGN([]string{"e4", "c5"}, "black", "win")
	if !strings.Contains(pgn, `[White "Tutor"]`) || !strings.Contains(pgn, `[Black "You"]`) {
		t.Fatalf("perspective swap failed:\n%s", pgn)
	}
	if !strings.Contains(pgn, "[Result \"1-0\"]") {
		t.Fatalf("expected 1-0 result:\n%s", pgn)
	}
}

func TestStartFENFor(t *testing.T) {
	fen := startFENFor([]string{"e4", "e5", "Nf3"})
	if fen == "" {
		t.Fatal("startFENFor returned empty")
	}
	fenOpt, err := chess.FEN(fen)
	if err != nil {
		t.Fatalf("FEN construction failed: %v", err)
	}
	game := chess.NewGame(fenOpt)
	if game.Position().Turn() != chess.Black {
		t.Fatalf("expected black to move after 1.e4 e5 2.Nf3, got %v", game.Position().Turn())
	}
}

func TestTutorGameResult(t *testing.T) {
	cases := []struct {
		outcome chess.Outcome
		color   string
		want    string
	}{
		{chess.WhiteWon, "white", "win"},
		{chess.WhiteWon, "black", "loss"},
		{chess.BlackWon, "black", "win"},
		{chess.BlackWon, "white", "loss"},
		{chess.NoOutcome, "white", "draw"},
	}
	for _, tc := range cases {
		if got := tutorGameResult(tc.outcome, tc.color); got != tc.want {
			t.Fatalf("tutorGameResult(%v, %q) = %q, want %q", tc.outcome, tc.color, got, tc.want)
		}
	}
}

func TestUciLineToSAN(t *testing.T) {
	start := "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"
	got := uciLineToSAN(start, "e2e4 e7e5 g1f3")
	want := "e4 e5 Nf3"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestTurnForColor(t *testing.T) {
	if turnForColor("black") != chess.Black || turnForColor("white") != chess.White {
		t.Fatal("turnForColor mapping wrong")
	}
}
