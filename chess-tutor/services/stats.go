package services

import (
	"encoding/json"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"

	"chess-tutor/models"

	"github.com/notnil/chess"
)

type StatsConfig struct {
	BlunderCP       float64
	MistakeCP       float64
	InaccuracyCP    float64
	GoodCP          float64
	OpeningMaxPlies int
	EndgameMaterial float64
}

var defaultStatsConfig = StatsConfig{
	BlunderCP:       300,
	MistakeCP:       150,
	InaccuracyCP:    50,
	GoodCP:          20,
	OpeningMaxPlies: 16,
	EndgameMaterial: 14,
}

func SetStatsConfig(c StatsConfig) {
	if c.BlunderCP > 0 {
		defaultStatsConfig.BlunderCP = c.BlunderCP
	}
	if c.MistakeCP > 0 {
		defaultStatsConfig.MistakeCP = c.MistakeCP
	}
	if c.InaccuracyCP > 0 {
		defaultStatsConfig.InaccuracyCP = c.InaccuracyCP
	}
	if c.GoodCP > 0 {
		defaultStatsConfig.GoodCP = c.GoodCP
	}
	if c.OpeningMaxPlies > 0 {
		defaultStatsConfig.OpeningMaxPlies = c.OpeningMaxPlies
	}
	if c.EndgameMaterial > 0 {
		defaultStatsConfig.EndgameMaterial = c.EndgameMaterial
	}
}

func GetStatsConfig() StatsConfig {
	return defaultStatsConfig
}

func ClassifyCPL(cpl float64) string {
	cfg := defaultStatsConfig
	switch {
	case cpl >= cfg.BlunderCP:
		return "blunder"
	case cpl >= cfg.MistakeCP:
		return "mistake"
	case cpl >= cfg.InaccuracyCP:
		return "inaccuracy"
	case cpl >= cfg.GoodCP:
		return "good"
	default:
		return "excellent"
	}
}

func PieceValue(p chess.Piece) float64 {
	switch p.Type() {
	case chess.Queen:
		return 9
	case chess.Rook:
		return 5
	case chess.Bishop, chess.Knight:
		return 3
	default:
		return 0
	}
}

func MaterialTotal(fen string) float64 {
	fields := strings.Fields(fen)
	if len(fields) == 0 {
		return 0
	}
	total := 0.0
	for _, ch := range fields[0] {
		switch ch {
		case 'q', 'Q':
			total += 9
		case 'r', 'R':
			total += 5
		case 'b', 'B', 'n', 'N':
			total += 3
		}
	}
	return total
}

func DetectPhase(fen string, moveNumber int) string {
	ply := (moveNumber - 1) * 2
	cfg := defaultStatsConfig
	if ply < cfg.OpeningMaxPlies {
		return "opening"
	}
	material := MaterialTotal(fen)
	if material <= cfg.EndgameMaterial {
		return "endgame"
	}
	return "middlegame"
}

func DetectPattern(san string) string {
	san = strings.TrimSpace(san)
	switch {
	case strings.HasSuffix(san, "#"):
		return "checkmate"
	case strings.HasSuffix(san, "+"):
		return "check"
	case strings.Contains(san, "="):
		return "promotion"
	case strings.HasPrefix(san, "O-"):
		return "castle"
	case strings.Contains(san, "x"):
		return "capture"
	default:
		return "quiet"
	}
}

func IsTacticalPattern(pattern string) bool {
	switch pattern {
	case "check", "capture", "promotion", "checkmate", "fork", "pin", "skewer":
		return true
	}
	return false
}

func pieceValue(t chess.PieceType) int {
	switch t {
	case chess.Queen:
		return 9
	case chess.Rook:
		return 5
	case chess.Bishop, chess.Knight:
		return 3
	case chess.Pawn:
		return 1
	default:
		return 0
	}
}

// DetectTacticalPattern inspects the position after a move (fen) and the
// played move (uci like "g1f3") to label forks, pins and skewers that are not
// visible from the SAN alone.
func DetectTacticalPattern(fen, uci string) string {
	if fen == "" || len(uci) < 4 {
		return ""
	}
	file := int(uci[2] - 'a')
	rank := int(uci[3] - '1')
	if file < 0 || file > 7 || rank < 0 || rank > 7 {
		return ""
	}
	toSq := chess.NewSquare(chess.File(file), chess.Rank(rank))

	fenOpt, err := chess.FEN(fen)
	if err != nil {
		return ""
	}
	game := chess.NewGame(fenOpt)
	pos := game.Position()
	board := pos.Board()
	moved := board.Piece(toSq)
	if moved == chess.NoPiece {
		return ""
	}
	enemyColor := oppositeColor(moved.Color())

	// Pin / skewer: only sliding pieces create them. Checked first because a
	// fork rule would otherwise label an aligned pin/skewer as a fork.
	if moved.Type() == chess.Bishop || moved.Type() == chess.Rook || moved.Type() == chess.Queen {
		if p := detectPinSkewer(board, toSq, moved, enemyColor); p != "" {
			return p
		}
	}

	// Fork: the moved piece simultaneously attacks two or more enemy pieces.
	if moved.Type() == chess.Knight || moved.Type() == chess.Pawn ||
		moved.Type() == chess.Bishop || moved.Type() == chess.Rook || moved.Type() == chess.Queen {
		targets := pseudoAttacks(board, toSq, moved, enemyColor)
		n3 := 0
		nonKingValue := 0
		kingAttacked := false
		for sq := range targets {
			p := board.Piece(sq)
			if p == chess.NoPiece || p.Color() != enemyColor {
				continue
			}
			if p.Type() == chess.King {
				kingAttacked = true
				continue
			}
			v := pieceValue(p.Type())
			nonKingValue += v
			if v >= 3 {
				n3++
			}
		}
		if (n3 >= 2) ||
			(kingAttacked && n3 >= 1) ||
			(nonKingValue >= 5 && len(targets) >= 2) {
			return "fork"
		}
	}
	return ""
}

func detectPinSkewer(board *chess.Board, toSq chess.Square, moved chess.Piece, enemyColor chess.Color) string {
	for _, dir := range rayDirections(moved.Type()) {
		first := chess.NoSquare
		second := chess.NoSquare
		sq := toSq
		for {
			sq = stepSquare(sq, dir)
			if sq == chess.NoSquare {
				break
			}
			if board.Piece(sq) == chess.NoPiece {
				continue
			}
			if first == chess.NoSquare {
				first = sq
			} else {
				second = sq
				break
			}
		}
		if first == chess.NoSquare || second == chess.NoSquare {
			continue
		}
		p1 := board.Piece(first)
		p2 := board.Piece(second)
		if p1.Color() != enemyColor || p2.Color() != enemyColor {
			continue
		}
		v1 := pieceValue(p1.Type())
		v2 := pieceValue(p2.Type())
		if p2.Type() == chess.King {
			return "pin"
		}
		if v1 >= 3 && v2 >= 3 && v1 >= v2 && p1.Type() != chess.King {
			return "skewer"
		}
	}
	return ""
}

func pseudoAttacks(board *chess.Board, from chess.Square, p chess.Piece, enemyColor chess.Color) map[chess.Square]bool {
	targets := map[chess.Square]bool{}
	add := func(sq chess.Square) {
		if sq != chess.NoSquare {
			targets[sq] = true
		}
	}
	ownColor := p.Color()
	blocked := func(sq chess.Square) bool {
		piece := board.Piece(sq)
		return piece != chess.NoPiece && piece.Color() == ownColor
	}
	switch p.Type() {
	case chess.Pawn:
		rankMod := 1
		if ownColor == chess.Black {
			rankMod = -1
		}
		toRank := int(from.Rank()) + rankMod
		add(chess.NewSquare(from.File()-1, chess.Rank(toRank)))
		add(chess.NewSquare(from.File()+1, chess.Rank(toRank)))
	case chess.Knight:
		for _, d := range []rayDir{{1, 2}, {1, -2}, {-1, 2}, {-1, -2}, {2, 1}, {2, -1}, {-2, 1}, {-2, -1}} {
			add(stepSquare(from, d))
		}
	case chess.King:
		for _, d := range []rayDir{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
			add(stepSquare(from, d))
		}
	case chess.Bishop, chess.Rook, chess.Queen:
		for _, dir := range rayDirections(p.Type()) {
			sq := from
			for {
				sq = stepSquare(sq, dir)
				if sq == chess.NoSquare || blocked(sq) {
					break
				}
				add(sq)
				if board.Piece(sq) != chess.NoPiece {
					break
				}
			}
		}
	}
	return targets
}

func oppositeColor(c chess.Color) chess.Color {
	if c == chess.White {
		return chess.Black
	}
	return chess.White
}

type rayDir struct{ df, dr int }

func rayDirections(t chess.PieceType) []rayDir {
	switch t {
	case chess.Bishop:
		return []rayDir{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
	case chess.Rook:
		return []rayDir{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	case chess.Queen:
		return []rayDir{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}, {1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	}
	return nil
}

func stepSquare(sq chess.Square, d rayDir) chess.Square {
	f := int(sq.File()) + d.df
	r := int(sq.Rank()) + d.dr
	if f < 0 || f > 7 || r < 0 || r > 7 {
		return chess.NoSquare
	}
	return chess.NewSquare(chess.File(f), chess.Rank(r))
}

type rawTopMove struct {
	Move       string  `json:"move"`
	Centipawns float64 `json:"cp"`
	MateIn     int     `json:"mate"`
}

func parseTopMoves(jsonStr string) []rawTopMove {
	if jsonStr == "" {
		return nil
	}
	var moves []rawTopMove
	if err := json.Unmarshal([]byte(jsonStr), &moves); err != nil {
		log.Printf("parse top_moves json: %v", err)
		return nil
	}
	return moves
}

func SerializeTopMoves(lines []MultiPVLine) string {
	if len(lines) == 0 {
		return ""
	}
	moves := make([]rawTopMove, 0, len(lines))
	for _, l := range lines {
		moves = append(moves, rawTopMove{
			Move:       l.BestMove,
			Centipawns: l.Centipawns,
			MateIn:     l.MateIn,
		})
	}
	jsonBytes, err := json.Marshal(moves)
	if err != nil {
		return ""
	}
	return string(jsonBytes)
}

func cplForMove(m *models.MoveAnalysis) float64 {
	if m.BestEval != 0 || m.EvalAfter != 0 {
		cpl := math.Abs(m.EvalAfter - m.BestEval)
		if cpl > 0 {
			return cpl
		}
	}
	return math.Abs(m.EvalDiff)
}

func criticalityForMove(m *models.MoveAnalysis) float64 {
	top := parseTopMoves(m.TopMoves)
	if len(top) < 2 {
		return 0
	}
	best := top[0].Centipawns
	if top[0].MateIn != 0 {
		if top[0].MateIn > 0 {
			best = 1000 - float64(top[0].MateIn)
		} else {
			best = -(1000 - float64(-top[0].MateIn))
		}
	}
	second := top[1].Centipawns
	if top[1].MateIn != 0 {
		if top[1].MateIn > 0 {
			second = 1000 - float64(top[1].MateIn)
		} else {
			second = -(1000 - float64(-top[1].MateIn))
		}
	}
	return math.Abs(best - second)
}

func topMovesContain(top []rawTopMove, uci string, n int) bool {
	for i, t := range top {
		if i >= n {
			break
		}
		if t.Move == uci {
			return true
		}
	}
	return false
}

func ComputeMoveStats(game *models.Game, moves []models.MoveAnalysis, studentSide string) ([]models.MoveStat, error) {
	stats := make([]models.MoveStat, 0, len(moves))
	for _, m := range moves {
		top := parseTopMoves(m.TopMoves)
		cpl := cplForMove(&m)
		pattern := DetectPattern(m.SAN)
		if m.PlayedUCI != "" && len(top) > 0 && top[0].Move == m.PlayedUCI {
			pattern = DetectPattern(m.BestMove)
		}
		if tactical := DetectTacticalPattern(m.FEN, m.PlayedUCI); tactical != "" {
			pattern = tactical
		}

		ms := models.MoveStat{
			GameID:         game.ID,
			MoveNumber:     m.MoveNumber,
			Side:           m.Side,
			SAN:            m.SAN,
			CPL:            cpl,
			Classification: ClassifyCPL(cpl),
			Phase:          DetectPhase(m.FEN, m.MoveNumber),
			Criticality:    criticalityForMove(&m),
			MissedTactic:   IsTacticalPattern(pattern) && (ClassifyCPL(cpl) != "excellent" && ClassifyCPL(cpl) != "good"),
			Top1:           m.PlayedUCI != "" && len(top) > 0 && top[0].Move == m.PlayedUCI,
			Top3:           m.PlayedUCI != "" && topMovesContain(top, m.PlayedUCI, 3),
			Pattern:        pattern,
			IsBook:         m.IsBook,
			IsStudent:      m.Side == studentSide,
		}
		stats = append(stats, ms)
	}
	return stats, nil
}

type clockLine struct {
	timeUsed  float64
	errorMove bool
	valid     bool
}

func parseClockTimes(pgn string, moveEvals []models.MoveAnalysis, studentSide string) []clockLine {
	increment := 0.0
	for _, line := range strings.Split(pgn, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[TimeControl") {
			if idx := strings.Index(line, "+"); idx != -1 {
				digits := ""
				for _, ch := range line[idx+1:] {
					if ch >= '0' && ch <= '9' {
						digits += string(ch)
					} else if len(digits) > 0 {
						break
					}
				}
				if v, err := strconv.ParseFloat(digits, 64); err == nil {
					increment = v
				}
			}
			break
		}
	}

	clocks := make([]float64, 0)
	for _, line := range strings.Split(pgn, "\n") {
		rest := line
		found := false
		for {
			idx := strings.Index(rest, "[%clk ")
			if idx == -1 {
				break
			}
			found = true
			rest = rest[idx+6:]
			end := strings.Index(rest, "]")
			if end == -1 {
				break
			}
			token := rest[:end]
			rest = rest[end+1:]
			parts := strings.Split(token, ":")
			if len(parts) == 3 {
				h, _ := strconv.ParseFloat(parts[0], 64)
				m, _ := strconv.ParseFloat(parts[1], 64)
				s, _ := strconv.ParseFloat(parts[2], 64)
				clocks = append(clocks, h*3600+m*60+s)
			} else if len(parts) == 2 {
				m, _ := strconv.ParseFloat(parts[0], 64)
				s, _ := strconv.ParseFloat(parts[1], 64)
				clocks = append(clocks, m*60+s)
			}
		}
		if found {
			break
		}
	}

	if len(clocks) == 0 {
		return nil
	}

	lines := make([]clockLine, 0, len(moveEvals))
	for i, m := range moveEvals {
		if m.IsBook || m.Side != studentSide {
			continue
		}
		if i < 2 || i >= len(clocks) {
			continue
		}
		prev := clocks[i-2]
		cur := clocks[i]
		used := prev - cur + increment
		if used < 0 || used > 600 {
			continue
		}
		cl := clockLine{timeUsed: used, valid: true}
		if cplForMove(&m) >= defaultStatsConfig.InaccuracyCP {
			cl.errorMove = true
		}
		lines = append(lines, cl)
	}
	return lines
}

func avgClock(lines []clockLine, errorMove bool) (float64, bool) {
	var sum float64
	count := 0
	for _, l := range lines {
		if l.valid && l.errorMove == errorMove {
			sum += l.timeUsed
			count++
		}
	}
	if count == 0 {
		return 0, false
	}
	return sum / float64(count), true
}

func accuracyFromCPL(avgCPL float64) float64 {
	if avgCPL <= 0 {
		return 0
	}
	accuracy := 100.0 * math.Exp(-avgCPL*0.0006)
	if accuracy > 100 {
		accuracy = 100
	}
	return accuracy
}

func ComputeGameStats(game *models.Game, moves []models.MoveAnalysis, studentSide string) (*models.GameStat, error) {
	moveStats, err := ComputeMoveStats(game, moves, studentSide)
	if err != nil {
		return nil, err
	}

	gs := &models.GameStat{
		GameID:           game.ID,
		StudentSide:      studentSide,
		TimeClass:        game.TimeClass,
		OpeningECO:       game.OpeningECO,
		OpeningName:      game.OpeningName,
		Result:           game.Result,
		PlayedAt:         game.PlayedAt,
		WhiteElo:         game.WhiteElo,
		BlackElo:         game.BlackElo,
		FirstMistakeMove: 0,
	}

	var studentCPL, opponentCPL float64
	var studentN, opponentN int

	var openingCPL, midCPL, endCPL float64
	var openingN, midN, endN int
	var openingErrs, midErrs, endErrs int

	var evalPeak, evalValley float64
	haveEval := false

	for _, ms := range moveStats {
		if ms.Side == studentSide {
			studentCPL += ms.CPL
			studentN++
			switch ms.Phase {
			case "opening":
				openingCPL += ms.CPL
				openingN++
				if ms.Classification == "inaccuracy" || ms.Classification == "mistake" || ms.Classification == "blunder" {
					openingErrs++
				}
			case "middlegame":
				midCPL += ms.CPL
				midN++
				if ms.Classification == "inaccuracy" || ms.Classification == "mistake" || ms.Classification == "blunder" {
					midErrs++
				}
			case "endgame":
				endCPL += ms.CPL
				endN++
				if ms.Classification == "inaccuracy" || ms.Classification == "mistake" || ms.Classification == "blunder" {
					endErrs++
				}
			}

			switch ms.Classification {
			case "blunder":
				gs.Blunders++
			case "mistake":
				gs.Mistakes++
			case "inaccuracy":
				gs.Inaccuracies++
			}
			if gs.FirstMistakeMove == 0 && (ms.Classification == "inaccuracy" || ms.Classification == "mistake" || ms.Classification == "blunder") {
				gs.FirstMistakeMove = ms.MoveNumber
			}
			if ms.MissedTactic {
				gs.MissedTactics++
			}
		} else {
			opponentCPL += ms.CPL
			opponentN++
			switch ms.Classification {
			case "blunder":
				gs.OpponentBlunders++
			case "mistake":
				gs.OpponentMistakes++
			case "inaccuracy":
				gs.OpponentInaccuracies++
			}
		}
	}

	if studentN > 0 {
		gs.AvgCPL = studentCPL / float64(studentN)
		gs.Accuracy = accuracyFromCPL(gs.AvgCPL)
	}
	if opponentN > 0 {
		gs.OpponentAvgCPL = opponentCPL / float64(opponentN)
		gs.OpponentAccuracy = accuracyFromCPL(gs.OpponentAvgCPL)
	}
	if openingN > 0 {
		gs.OpeningCPL = openingCPL / float64(openingN)
	}
	if midN > 0 {
		gs.MiddlegameCPL = midCPL / float64(midN)
	}
	if endN > 0 {
		gs.EndgameCPL = endCPL / float64(endN)
	}
	gs.OpeningErrors = openingErrs
	gs.MiddlegameErrors = midErrs
	gs.EndgameErrors = endErrs

	for _, m := range moves {
		if m.Side != studentSide {
			continue
		}
		val := m.EvalBefore
		if !haveEval {
			evalPeak = val
			evalValley = val
			haveEval = true
			continue
		}
		if val > evalPeak {
			evalPeak = val
		}
		if val < evalValley {
			evalValley = val
		}
	}
	gs.EvalPeak = evalPeak / 100.0
	gs.EvalValley = evalValley / 100.0

	if gs.EvalPeak >= 3.0 && game.Result == "loss" {
		gs.ThrowWin = true
	}
	if gs.EvalValley <= -3.0 && game.Result == "win" {
		gs.Recovered = true
	}

	if clockLines := parseClockTimes(game.PGN, moves, studentSide); len(clockLines) > 0 {
		if avg, ok := avgClock(clockLines, true); ok {
			gs.ClockErrorAvg = avg
		}
		if avg, ok := avgClock(clockLines, false); ok {
			gs.ClockOKAvg = avg
		}
	}

	return gs, nil
}

func ComputeAndSaveStats(game *models.Game, moves []models.MoveAnalysis, studentSide string) (*models.GameStat, error) {
	moveStats, err := ComputeMoveStats(game, moves, studentSide)
	if err != nil {
		return nil, err
	}
	if err := models.ReplaceMoveStats(game.ID, moveStats); err != nil {
		return nil, err
	}
	gs, err := ComputeGameStats(game, moves, studentSide)
	if err != nil {
		return nil, err
	}
	if err := models.ReplaceGameStat(gs); err != nil {
		return nil, err
	}
	return gs, nil
}

func TopCostlyMoves(gameID int64, n int) []models.MoveStat {
	stats, err := models.GetMoveStats(gameID)
	if err != nil {
		return nil
	}
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].CPL > stats[j].CPL
	})
	if len(stats) > n {
		stats = stats[:n]
	}
	return stats
}
