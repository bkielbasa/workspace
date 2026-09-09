package services

import (
	"testing"

	"chess-tutor/models"
)

func TestParseClockTimes(t *testing.T) {
	pgn := `[Event "Live Chess"]
[White "w"]
[Black "b"]
[TimeControl "600+5"]
1. d4 {[%clk 0:09:48]} 1... d5 {[%clk 0:09:41]} 2. Bf4 {[%clk 0:09:32]} 2... Nc6 {[%clk 0:09:25]} 3. e3 {[%clk 0:09:10]} 3... a6 {[%clk 0:09:05]}
`
	moves := []models.MoveAnalysis{
		{MoveNumber: 1, Side: "white", SAN: "d4"},
		{MoveNumber: 1, Side: "black", SAN: "d5"},
		{MoveNumber: 2, Side: "white", SAN: "Bf4"},
		{MoveNumber: 2, Side: "black", SAN: "Nc6"},
		{MoveNumber: 3, Side: "white", SAN: "e3"},
		{MoveNumber: 3, Side: "black", SAN: "a6"},
	}
	lines := parseClockTimes(pgn, moves, "white")
	if len(lines) != 2 {
		t.Fatalf("expected 2 student non-book clock lines, got %d", len(lines))
	}
	want := float64(9*60 + 48 + 5 - (9*60 + 32))
	if got := lines[0].timeUsed; got > want+0.001 || got < want-0.001 {
		t.Fatalf("time used = %.2f, want ~%.2f", got, want)
	}
}

func TestDetectTacticalPattern(t *testing.T) {
	cases := []struct {
		name string
		fen  string
		uci  string
		want string
	}{
		{
			name: "knight forks king and rook",
			fen:  "r3k3/2N5/8/8/8/8/8/4K3 b - - 0 1",
			uci:  "b5c7",
			want: "fork",
		},
		{
			name: "knight forks rook and queen",
			fen:  "4k3/8/8/8/r3r3/2N5/8/4K3 b - - 0 1",
			uci:  "b1c3",
			want: "fork",
		},
		{
			name: "bishop pins knight to king",
			fen:  "r3k3/8/2n5/1B6/8/8/8/4K3 b - - 0 1",
			uci:  "f1b5",
			want: "pin",
		},
		{
			name: "bishop skewers rook and knight",
			fen:  "4k3/8/4n3/3r4/2B5/8/8/4K3 b - - 0 1",
			uci:  "g2c4",
			want: "skewer",
		},
		{
			name: "quiet move",
			fen:  "rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 1",
			uci:  "e2e4",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectTacticalPattern(tc.fen, tc.uci); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
