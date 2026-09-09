package services

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"chess-tutor/models"

	"github.com/notnil/chess"
)

const startFEN = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"

type PracticeMove struct {
	UCI  string  `json:"move"`
	CP   float64 `json:"cp"`
	Mate int     `json:"mate"`
}

type PracticeItem struct {
	Index           int
	GameID          int64
	MoveNumber      int
	Side            string
	PlayedSAN       string
	FEN             string
	CPL             float64
	Classification  string
	Phase           string
	Pattern         string
	MissedTactic    bool
	Top             []PracticeMove
	BestUCI         string
	BestSAN         string
	BestCP          float64
	BestMate        int
	BestLine        string
	RevealFEN       string
	AttemptCorrect  int
	Attempts        int
	LastPracticedAt time.Time
	PlayedAt        time.Time
	GameWhite       string
	GameBlack       string
	GameResult      string
	PlayerSide      string
	Hints           []string
}

type PracticeCheckResult struct {
	Item            *PracticeItem
	UserUCI         string
	UserSAN         string
	Verdict         string
	UserCP          float64
	UserMate        int
	Explanation     string
	ExplanationLine string
	LineFENs        []string
}

type ThemeStat struct {
	Pattern  string
	Count    int
	TotalCPL float64
}

// PracticeQuery describes a practice queue. Mode is one of
// "game", "daily", "theme" or "opponent".
type PracticeQuery struct {
	Mode   string
	GameID int64
	Theme  string
}

func practiceKey(moveNumber int, side string) string {
	return fmt.Sprintf("%d|%s", moveNumber, side)
}

func oppositeSide(side string) string {
	if side == "white" {
		return "black"
	}
	return "white"
}

func setIndices(items []PracticeItem) {
	for i := range items {
		items[i].Index = i
	}
}

// BuildPracticeQueue builds the item list for a given practice mode.
//   - "game":     one game's student errors, worst first (classic blunder review)
//   - "daily":    every analyzed game's student errors, ordered by spaced repetition
//   - "theme":    like daily but restricted to one pattern
//   - "opponent": every analyzed game's opponent errors, worst first (find the punishing move)
func BuildPracticeQueue(q PracticeQuery) ([]PracticeItem, error) {
	switch q.Mode {
	case "game":
		if q.GameID <= 0 {
			return nil, errors.New("game mode requires a game id")
		}
		items, err := buildGameItems(q.GameID, true)
		if err != nil {
			return nil, err
		}
		sortByCPL(items)
		setIndices(items)
		return items, nil
	case "opponent":
		items, err := buildAllItems(false)
		if err != nil {
			return nil, err
		}
		sortByCPL(items)
		setIndices(items)
		return items, nil
	case "theme":
		items, err := buildAllItems(true)
		if err != nil {
			return nil, err
		}
		filtered := items[:0]
		for _, it := range items {
			if it.Pattern == q.Theme {
				filtered = append(filtered, it)
			}
		}
		sortBySR(filtered)
		setIndices(filtered)
		return filtered, nil
	default:
		items, err := buildAllItems(true)
		if err != nil {
			return nil, err
		}
		sortBySR(items)
		setIndices(items)
		return items, nil
	}
}

func sortByCPL(items []PracticeItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CPL != items[j].CPL {
			return items[i].CPL > items[j].CPL
		}
		if !items[i].PlayedAt.Equal(items[j].PlayedAt) {
			return items[i].PlayedAt.After(items[j].PlayedAt)
		}
		return items[i].GameID < items[j].GameID
	})
}

func sortBySR(items []PracticeItem) {
	sort.SliceStable(items, func(i, j int) bool {
		si := spacedRepetitionScore(&items[i])
		sj := spacedRepetitionScore(&items[j])
		if si != sj {
			return si > sj
		}
		if !items[i].PlayedAt.Equal(items[j].PlayedAt) {
			return items[i].PlayedAt.After(items[j].PlayedAt)
		}
		return items[i].CPL > items[j].CPL
	})
}

// spacedRepetitionScore ranks items for the daily review. Higher = should
// be reviewed sooner. Never-practiced errors are hot; recently-mastered ones
// cool off; repeated failures stay at the top of the queue.
func spacedRepetitionScore(it *PracticeItem) float64 {
	score := it.CPL / 100.0
	if it.MissedTactic {
		score += 1.0
	}
	if it.Attempts == 0 {
		score += 3.0
	} else {
		days := time.Since(it.LastPracticedAt).Hours() / 24.0
		switch it.AttemptCorrect {
		case 1: // best move
			score -= 2.0
			if days < 1 {
				score -= 3.0
			} else if days < 7 {
				score -= 1.5
			}
		case 2: // good but not best
			score -= 1.0
			if days < 1 {
				score -= 2.0
			}
		default: // wrong
			score += 3.0 + 1.5*float64(it.Attempts)
			if days < 1 {
				score += 1.0
			}
		}
	}
	daysSinceGame := time.Since(it.PlayedAt).Hours() / 24.0
	if daysSinceGame > 0 {
		score -= daysSinceGame / 30.0
	}
	return score
}

func buildAllItems(includeStudent bool) ([]PracticeItem, error) {
	ids, err := models.GetAnalyzedGameIDs()
	if err != nil {
		return nil, err
	}
	var all []PracticeItem
	for _, id := range ids {
		items, err := buildGameItems(id, includeStudent)
		if err != nil {
			log.Printf("practice items for game %d: %v", id, err)
			continue
		}
		all = append(all, items...)
	}
	return all, nil
}

func buildGameItems(gameID int64, includeStudent bool) ([]PracticeItem, error) {
	game, err := models.GetGame(gameID)
	if err != nil {
		return nil, err
	}
	return buildGameItemsFrom(game, includeStudent)
}

func buildGameItemsFrom(game *models.Game, includeStudent bool) ([]PracticeItem, error) {
	moves, err := models.GetMoveAnalyses(game.ID)
	if err != nil {
		return nil, err
	}
	stats, err := models.GetMoveStats(game.ID)
	if err != nil {
		return nil, err
	}
	statMap := make(map[string]*models.MoveStat, len(stats))
	for i := range stats {
		statMap[practiceKey(stats[i].MoveNumber, stats[i].Side)] = &stats[i]
	}

	attempts, err := models.GetPracticeAttempts(game.ID)
	if err != nil {
		log.Printf("get practice attempts: %v", err)
	}
	attemptMap := make(map[string]*models.PracticeAttempt, len(attempts))
	for i := range attempts {
		attemptMap[practiceKey(attempts[i].MoveNumber, attempts[i].Side)] = &attempts[i]
	}

	var items []PracticeItem
	fenBefore := startFEN
	for i, m := range moves {
		if i > 0 {
			fenBefore = moves[i-1].FEN
		}
		st, ok := statMap[practiceKey(m.MoveNumber, m.Side)]
		if !ok || st.IsStudent != includeStudent {
			continue
		}
		if st.Classification != "blunder" && st.Classification != "mistake" && st.Classification != "inaccuracy" {
			continue
		}
		top := parseTopMoves(m.TopMoves)
		if len(top) == 0 {
			continue
		}

		item := PracticeItem{
			GameID:         game.ID,
			MoveNumber:     m.MoveNumber,
			Side:           m.Side,
			PlayedSAN:      m.SAN,
			FEN:            fenBefore,
			CPL:            st.CPL,
			Classification: st.Classification,
			Phase:          st.Phase,
			Pattern:        st.Pattern,
			MissedTactic:   st.MissedTactic,
			AttemptCorrect: -1,
			PlayedAt:       game.PlayedAt,
			GameWhite:      game.White,
			GameBlack:      game.Black,
			GameResult:     game.Result,
			PlayerSide:     oppositeSide(m.Side),
		}
		if includeStudent {
			item.PlayerSide = m.Side
		}
		if i > 0 {
			item.BestLine = moves[i-1].BestLine
		}
		for _, t := range top {
			item.Top = append(item.Top, PracticeMove{UCI: t.Move, CP: t.Centipawns, Mate: t.MateIn})
		}
		best := item.Top[0]
		item.BestUCI = best.UCI
		item.BestCP = best.CP
		item.BestMate = best.Mate

		// If the engine's own #1 move at this position is the move the
		// student actually played, it was misclassified as an error
		// (inconsistent eval data) and is not worth drilling.
		if m.PlayedUCI != "" && m.PlayedUCI == item.BestUCI {
			continue
		}

		game, err := newGameFromFEN(fenBefore)
		if err != nil {
			continue
		}
		pos := game.Position()
		mv, err := chess.UCINotation{}.Decode(pos, best.UCI)
		if err != nil {
			continue
		}
		item.BestSAN = chess.AlgebraicNotation{}.Encode(pos, mv)
		if err := game.Move(mv); err == nil {
			item.RevealFEN = game.Position().String()
		}

		if a, ok := attemptMap[practiceKey(m.MoveNumber, m.Side)]; ok {
			item.AttemptCorrect = a.Correct
			item.Attempts = a.Attempts
			item.LastPracticedAt = a.LastPracticedAt
		}
		item.Hints = computeHints(&item)
		items = append(items, item)
	}

	return items, nil
}

func computeHints(it *PracticeItem) []string {
	concept := ""
	switch {
	case it.MissedTactic:
		concept = "Look for a forcing sequence — a check, capture, or threat that wins material or resolves a tactic."
	case it.Classification == "blunder":
		concept = "Your move was a serious error. Find the move that keeps or wins the best position."
	default:
		concept = "A better move is available. Ask yourself: is a piece hanging, or is there a tactical shot?"
	}
	if it.Pattern != "" && it.Pattern != "quiet" {
		concept += fmt.Sprintf(" The best move involves a %s.", it.Pattern)
	}
	return []string{
		concept,
		fmt.Sprintf("The best move is made with your %s.", pieceNameAtSource(it.FEN, it.BestUCI)),
		fmt.Sprintf("The best move is %s.", it.BestSAN),
	}
}

func pieceNameAtSource(fen, uci string) string {
	if len(uci) < 2 {
		return "piece"
	}
	game, err := newGameFromFEN(fen)
	if err != nil {
		return "piece"
	}
	sq := chess.NewSquare(chess.File(int(uci[0]-'a')), chess.Rank(int(uci[1]-'1')))
	switch game.Position().Board().Piece(sq).Type() {
	case chess.Pawn:
		return "pawn"
	case chess.Knight:
		return "knight"
	case chess.Bishop:
		return "bishop"
	case chess.Rook:
		return "rook"
	case chess.Queen:
		return "queen"
	case chess.King:
		return "king"
	}
	return "piece"
}

// PracticeThemes returns the tactical patterns that appear among the
// student's errors across all analyzed games, most frequent first.
func PracticeThemes() []ThemeStat {
	counts, err := models.GetPracticeThemes()
	if err != nil {
		log.Printf("practice themes: %v", err)
		return nil
	}
	themes := make([]ThemeStat, 0, len(counts))
	for _, c := range counts {
		themes = append(themes, ThemeStat{Pattern: c.Pattern, Count: c.Count, TotalCPL: c.TotalCPL})
	}
	return themes
}

// DefaultTheme picks a sensible starting pattern for theme drills,
// preferring concrete tactical motifs over generic ones.
func DefaultTheme() string {
	for _, t := range PracticeThemes() {
		switch t.Pattern {
		case "capture", "check", "promotion", "checkmate", "fork", "pin", "skewer":
			return t.Pattern
		}
	}
	if themes := PracticeThemes(); len(themes) > 0 {
		return themes[0].Pattern
	}
	return ""
}

func CheckPracticeAnswer(q PracticeQuery, index int, fen, uci string) (*PracticeCheckResult, error) {
	items, err := BuildPracticeQueue(q)
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(items) {
		return nil, errors.New("invalid practice index")
	}
	item := items[index]
	if item.FEN != fen {
		return nil, errors.New("position does not match this practice item")
	}

	game, err := newGameFromFEN(fen)
	if err != nil {
		return nil, err
	}
	pos := game.Position()
	mv, err := chess.UCINotation{}.Decode(pos, uci)
	if err != nil {
		return nil, err
	}
	userSAN := chess.AlgebraicNotation{}.Encode(pos, mv)
	if err := game.Move(mv); err != nil {
		return nil, errors.New("illegal move")
	}

	res := &PracticeCheckResult{
		Item:    &item,
		UserUCI: uci,
		UserSAN: userSAN,
	}

	correct := 0
	switch {
	case item.BestUCI == uci:
		res.Verdict = "best"
		correct = 1
	case practiceTopMovesContain(item.Top, uci, 3):
		res.Verdict = "good"
		correct = 2
	default:
		res.Verdict = "wrong"
	}
	for _, t := range item.Top {
		if t.UCI == uci {
			res.UserCP = t.CP
			res.UserMate = t.Mate
		}
	}

	item.AttemptCorrect = correct
	res.Item = &item

	if err := models.SavePracticeAttempt(&models.PracticeAttempt{
		GameID:     item.GameID,
		MoveNumber: item.MoveNumber,
		Side:       item.Side,
		FenBefore:  item.FEN,
		BestUCI:    item.BestUCI,
		UserUCI:    uci,
		Correct:    correct,
	}); err != nil {
		log.Printf("save practice attempt: %v", err)
	}
	return res, nil
}

func newGameFromFEN(fen string) (*chess.Game, error) {
	fenOpt, err := chess.FEN(fen)
	if err != nil {
		return nil, err
	}
	return chess.NewGame(fenOpt), nil
}

func practiceTopMovesContain(top []PracticeMove, uci string, n int) bool {
	for i, t := range top {
		if i >= n {
			break
		}
		if t.UCI == uci {
			return true
		}
	}
	return false
}

func NormalizeMode(mode string) string {
	switch mode {
	case "game", "theme", "opponent":
		return mode
	default:
		return "daily"
	}
}

func DescribeSide(side string) string {
	if side == "black" {
		return "Black"
	}
	return "White"
}

func DescribeResult(result string) string {
	switch strings.ToLower(result) {
	case "win":
		return "Win"
	case "loss":
		return "Loss"
	case "draw":
		return "Draw"
	}
	return result
}
