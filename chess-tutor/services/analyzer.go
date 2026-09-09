package services

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"chess-tutor/models"

	"github.com/notnil/chess"
	"github.com/notnil/chess/opening"
)

type Analyzer struct {
	Engine    *StockfishEngine
	Anthropic *AnthropicClient
	sem       chan struct{}
}

func NewAnalyzer(engine *StockfishEngine, anthropic *AnthropicClient) *Analyzer {
	return &Analyzer{
		Engine:    engine,
		Anthropic: anthropic,
		sem:       make(chan struct{}, 3),
	}
}

func (a *Analyzer) AnalyzeGame(game *models.Game, username string) error {
	a.sem <- struct{}{}
	defer func() { <-a.sem }()

	log.Printf("Analyzing game %d (%s vs %s)", game.ID, game.White, game.Black)

	pgnOpt, err := chess.PGN(strings.NewReader(game.PGN))
	if err != nil {
		return fmt.Errorf("parse pgn: %w", err)
	}
	gameObj := chess.NewGame(pgnOpt)

	moves := gameObj.Moves()
	if len(moves) == 0 {
		return fmt.Errorf("no moves in game")
	}

	models.DeleteMoveAnalyses(game.ID)
	models.DeleteGameAnalysis(game.ID)

	tempGame := chess.NewGame()
	var moveEvals []MoveEval

	totalDiffWhite := 0.0
	totalDiffBlack := 0.0
	whiteMoveCount := 0
	blackMoveCount := 0

	var blunders, mistakes, inaccuracies, good, excellent int
	var bookMovesWhite, bookMovesBlack int
	var movesSoFar []*chess.Move

	studentUser := game.Username
	if studentUser == "" {
		studentUser = username
	}
	if studentUser == "" {
		if stored, err := models.GetSetting("chesscom_username"); err == nil {
			studentUser = stored
		}
	}
	playerSide := "white"
	if strings.EqualFold(studentUser, game.Black) || (len(game.Black) >= 4 && strings.HasPrefix(strings.ToLower(studentUser), strings.ToLower(game.Black[:4]))) {
		playerSide = "black"
	}

	var studentBlunders, studentMistakes, studentInaccuracies, studentBookMoves int

	for i, move := range moves {
		side := "white"
		if i%2 == 1 {
			side = "black"
		}

		fenBefore := tempGame.Position().String()

		evalBefore := 0.0
		bestMoveBefore := ""
		bestEval := 0.0
		topMovesJSON := ""
		multi, err := a.Engine.EvaluateMultiPV(fenBefore, depthConfig, 3)
		if err != nil {
			log.Printf("MultiPV before move %d: %v", i+1, err)
		} else if len(multi) > 0 {
			evalBefore = multi[0].Centipawns
			bestMoveBefore = multi[0].BestMove
			bestEval = -multi[0].Centipawns
			topMovesJSON = SerializeTopMoves(multi)
		}

		san := chess.AlgebraicNotation{}.Encode(tempGame.Position(), move)

		err = tempGame.Move(move)
		if err != nil {
			return fmt.Errorf("apply move %s: %w", move.String(), err)
		}

		movesSoFar = append(movesSoFar, move)

		var isBook bool
		if i < 30 {
			isBook = isBookMoveLocal(movesSoFar)
		}

		fenAfter := tempGame.Position().String()

		evalAfter := 0.0
		bestMove := ""
		bestLine := ""
		res, err := a.Engine.Evaluate(fenAfter, depthConfig)
		if err != nil {
			log.Printf("Eval after move %d: %v", i+1, err)
		} else {
			evalAfter = res.Centipawns
			bestMove = res.BestMove
			bestLine = res.BestLine
		}

		evalDiff := -evalAfter - evalBefore

		isBlunder := false
		if bestMoveBefore != "" {
			blunderDiff := evalAfter - bestEval
			if blunderDiff > 150.0 {
				isBlunder = true
			}
		}

		classification := ClassifyMoveMaterial(evalDiff/100.0, 0, isBlunder, MaterialTotal(fenAfter))
		switch classification {
		case "blunder":
			blunders++
		case "mistake":
			mistakes++
		case "inaccuracy":
			inaccuracies++
		case "good":
			good++
		case "excellent":
			excellent++
		}

		if side == playerSide {
			switch classification {
			case "blunder":
				studentBlunders++
			case "mistake":
				studentMistakes++
			case "inaccuracy":
				studentInaccuracies++
			}
		}

		if side == "white" {
			totalDiffWhite += math.Abs(evalDiff)
			whiteMoveCount++
			if isBook {
				bookMovesWhite++
			}
		} else {
			totalDiffBlack += math.Abs(evalDiff)
			blackMoveCount++
			if isBook {
				bookMovesBlack++
			}
		}

		if side == playerSide && isBook {
			studentBookMoves++
		}

		ma := &models.MoveAnalysis{
			GameID:         game.ID,
			MoveNumber:     i/2 + 1,
			Side:           side,
			SAN:            san,
			FEN:            fenAfter,
			EvalBefore:     evalBefore,
			EvalAfter:      evalAfter,
			EvalDiff:       evalDiff,
			Classification: classification,
			BestMove:       bestMove,
			BestLine:       bestLine,
			IsBook:         isBook,
			BestEval:       bestEval,
			TopMoves:       topMovesJSON,
			PlayedUCI:      move.String(),
		}
		if err := models.SaveMoveAnalysis(ma); err != nil {
			log.Printf("Save move analysis: %v", err)
		}

		moveEvals = append(moveEvals, MoveEval{
			San:            san,
			EvalBefore:     evalBefore,
			EvalAfter:      evalAfter,
			EvalDiff:       evalDiff,
			BestMove:       bestMove,
			BestLine:       bestLine,
			Classification: classification,
		})
	}

	accuracyWhite := calculateAccuracy(totalDiffWhite, whiteMoveCount)
	accuracyBlack := calculateAccuracy(totalDiffBlack, blackMoveCount)

	studentAccuracy := accuracyWhite
	if playerSide == "black" {
		studentAccuracy = accuracyBlack
	}
	studentElo := game.WhiteElo
	opponentElo := game.BlackElo
	if playerSide == "black" {
		studentElo = game.BlackElo
		opponentElo = game.WhiteElo
	}
	if studentElo <= 0 {
		studentElo = parsePGNInt(game.PGN, "WhiteElo")
		if playerSide == "black" {
			studentElo = parsePGNInt(game.PGN, "BlackElo")
		}
	}
	if opponentElo <= 0 {
		opponentElo = parsePGNInt(game.PGN, "BlackElo")
		if playerSide == "black" {
			opponentElo = parsePGNInt(game.PGN, "WhiteElo")
		}
	}
	if studentElo <= 0 {
		studentElo = opponentElo
	}
	profile := StudentProfile{
		Elo:          studentElo,
		Accuracy:     studentAccuracy,
		Blunders:     studentBlunders,
		Mistakes:     studentMistakes,
		Inaccuracies: studentInaccuracies,
		BookMoves:    studentBookMoves,
	}

	openingName := ""
	openingECO := ""
	if game.OpeningName != "" {
		openingName = game.OpeningName
		openingECO = game.OpeningECO
	} else {
		eco, name := QueryOpeningLocal(gameObj.Moves())
		if name != "" {
			openingECO = eco
			openingName = name
			models.UpdateGameOpening(game.ID, eco, name)
		}
	}
	openingDisplay := openingName
	if openingECO != "" {
		openingDisplay = fmt.Sprintf("%s (%s)", openingName, openingECO)
	}

	feedback := a.gameFeedbackText(game, moveEvals, openingDisplay, profile, historyBaselineText(game.ID), blunders, mistakes, inaccuracies, bookMovesWhite, bookMovesBlack)

	ga := &models.GameAnalysis{
		GameID:         game.ID,
		TotalMoves:     len(moves),
		AccuracyWhite:  accuracyWhite,
		AccuracyBlack:  accuracyBlack,
		Blunders:       blunders,
		Mistakes:       mistakes,
		Inaccuracies:   inaccuracies,
		GoodMoves:      good,
		ExcellentMoves: excellent,
		BookMovesWhite: bookMovesWhite,
		BookMovesBlack: bookMovesBlack,
		Feedback:       feedback,
	}
	if err := models.SaveGameAnalysis(ga); err != nil {
		return fmt.Errorf("save analysis: %w", err)
	}

	rawMoves, err := models.GetMoveAnalyses(game.ID)
	if err != nil {
		log.Printf("load move analyses for stats: %v", err)
	}

	gs, err := ComputeAndSaveStats(game, rawMoves, playerSide)
	if err != nil {
		log.Printf("compute stats: %v", err)
	} else {
		allStats, _ := models.GetGameStats()
		var historical []models.GameStat
		for _, s := range allStats {
			if s.GameID != game.ID {
				historical = append(historical, s)
			}
		}
		input := BuildCoachInput(gs, historical)
		coach := a.GenerateCoachFeedback(input)
		if coach != "" {
			if err := models.SaveCoachFeedback(game.ID, coach); err != nil {
				log.Printf("save coach feedback: %v", err)
			}
		}
	}

	if err := models.MarkGameAnalyzed(game.ID); err != nil {
		return fmt.Errorf("mark analyzed: %w", err)
	}

	if err := models.TakeMetricSnapshot(); err != nil {
		log.Printf("metric snapshot: %v", err)
	}

	return nil
}

var (
	bookECOSync sync.Once
	bookECO     *opening.BookECO
)

func getBook() *opening.BookECO {
	bookECOSync.Do(func() {
		bookECO = opening.NewBookECO()
	})
	return bookECO
}

func IsBookMoveLocal(moves []*chess.Move) bool {
	return isBookMoveLocal(moves)
}

func isBookMoveLocal(movesSoFar []*chess.Move) bool {
	if len(movesSoFar) == 0 {
		return false
	}
	book := getBook()
	prefix := movesSoFar[:len(movesSoFar)-1]
	played := movesSoFar[len(movesSoFar)-1]
	nextIdx := len(prefix)
	for _, o := range book.Possible(prefix) {
		om := o.Game().Moves()
		if nextIdx < len(om) && om[nextIdx].String() == played.String() {
			return true
		}
	}
	return false
}

func calculateAccuracy(totalDiff float64, moveCount int) float64 {
	if moveCount == 0 {
		return 0
	}
	avgLoss := totalDiff / float64(moveCount)
	accuracy := 100.0 * math.Exp(-avgLoss*0.006)
	if accuracy > 100 {
		accuracy = 100
	}
	return accuracy
}

func generateLocalFeedback(game *models.Game, moveEvals []MoveEval, blunders, mistakes, inaccuracies, bookWhite, bookBlack int, opening string, profile StudentProfile) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("<h2>Game Analysis: %s vs %s</h2>\n", game.White, game.Black))
	b.WriteString(fmt.Sprintf("<p><strong>Result:</strong> %s | <strong>Opening:</strong> %s</p>\n", game.Result, opening))
	b.WriteString(fmt.Sprintf("<p><strong>Overview:</strong> %d total moves. ", len(moveEvals)))
	b.WriteString(fmt.Sprintf("Blunders: %d, Mistakes: %d, Inaccuracies: %d. ", blunders, mistakes, inaccuracies))
	b.WriteString(fmt.Sprintf("Book moves: %d (White) / %d (Black)</p>\n", bookWhite, bookBlack))
	if profile.Elo > 0 {
		b.WriteString(fmt.Sprintf("<p><strong>Student rating:</strong> %s | <strong>Accuracy:</strong> %.1f%%</p>\n",
			EloLabel(profile), profile.Accuracy))
	}

	b.WriteString("<h3>Critical Moments</h3>\n<ul>\n")
	criticalCount := 0
	for _, m := range moveEvals {
		if (m.Classification == "blunder" || m.Classification == "mistake") && criticalCount < 5 {
			b.WriteString(fmt.Sprintf("<li><strong>%s</strong> (%s): Eval changed from %.1f to %.1f. Best was <strong>%s</strong>. Line: %s</li>\n",
				m.San, m.Classification, m.EvalBefore, m.EvalAfter, m.BestMove, truncateStr(m.BestLine, 60)))
			criticalCount++
		}
	}

	if criticalCount == 0 {
		b.WriteString("<li>No critical mistakes found! Well played.</li>\n")
	}

	b.WriteString("</ul>\n")

	if blunders+mistakes+inaccuracies > 0 {
		b.WriteString("<h3>Advice</h3>\n<ul>\n")
		if blunders > 2 {
			b.WriteString("<li><strong>Tactics:</strong> Focus on basic tactical patterns (forks, pins, skewers). Do 20+ tactics puzzles daily.</li>\n")
		}
		if mistakes > 3 {
			b.WriteString("<li><strong>Positional play:</strong> Study positional concepts like pawn structures, piece activity, and square control.</li>\n")
		}
		if inaccuracies > 5 {
			b.WriteString("<li><strong>Calculation:</strong> Work on calculating deeper. Always look for your opponent's best response.</li>\n")
		}
		if bookWhite+bookBlack < 6 {
			b.WriteString(fmt.Sprintf("<li><strong>Opening prep:</strong> Only %d book moves played. Study your opening lines deeper to reach a playable middlegame.</li>\n", bookWhite+bookBlack))
		}
		b.WriteString("</ul>\n")
	}

	b.WriteString("<h3>Suggestions for You</h3>\n")
	b.WriteString(fmt.Sprintf("<p><strong>%s vs %s</strong> (your rating: %s, accuracy: %.1f%%)</p>\n",
		game.White, game.Black, EloLabel(profile), profile.Accuracy))
	suggestions := BuildSuggestions(profile)
	if len(suggestions) == 0 {
		b.WriteString("<ul><li>Keep playing and analyzing - the sample size is small. Your trends will shape the advice.</li></ul>\n")
	} else {
		b.WriteString("<ul>\n")
		for _, s := range suggestions {
			b.WriteString(fmt.Sprintf("<li><strong>%s.</strong> %s</li>\n", s.Title, s.Detail))
		}
		b.WriteString("</ul>\n")
	}

	return b.String()
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func (a *Analyzer) PlayMove(currentFEN string, moveStr string) (newFEN string, eval *MoveEval, gameOver bool, result string, err error) {
	fenOpt, _ := chess.FEN(currentFEN)
	tempGame := chess.NewGame(fenOpt)

	err = tempGame.MoveStr(moveStr)
	if err != nil {
		return currentFEN, nil, false, "", fmt.Errorf("invalid move: %w", err)
	}

	status := tempGame.Outcome()
	if status != chess.NoOutcome {
		gameOver = true
		switch status {
		case chess.WhiteWon:
			result = "1-0"
		case chess.BlackWon:
			result = "0-1"
		case chess.Draw:
			result = "1/2-1/2"
		}
	}

	fenAfter := tempGame.Position().String()

	res, err := a.Engine.Evaluate(fenAfter, depthConfig)
	if err != nil {
		return fenAfter, nil, gameOver, result, fmt.Errorf("evaluate: %w", err)
	}

	bestRes, _ := a.Engine.Evaluate(currentFEN, depthConfig)

	playerDiff := -res.Centipawns - bestRes.Centipawns
	eval = &MoveEval{
		San:            moveStr,
		EvalBefore:     bestRes.Centipawns,
		EvalAfter:      res.Centipawns,
		EvalDiff:       playerDiff,
		BestMove:       res.BestMove,
		BestLine:       res.BestLine,
		Classification: ClassifyMoveMaterial(playerDiff/100.0, 0, false, MaterialTotal(fenAfter)),
		BestMoveBefore: bestRes.BestMove,
		BestLineBefore: bestRes.BestLine,
	}

	return fenAfter, eval, gameOver, result, nil
}

// UCItoSAN converts a UCI move string (e.g. "e2e4", "e7e8q") to algebraic
// notation for the given position. Returns the input unchanged on failure.
func UCItoSAN(fen, uci string) string {
	fenOpt, err := chess.FEN(fen)
	if err != nil {
		return uci
	}
	tempGame := chess.NewGame(fenOpt, chess.UseNotation(chess.UCINotation{}))
	if err := tempGame.MoveStr(uci); err != nil {
		return uci
	}
	positions := tempGame.Positions()
	moves := tempGame.Moves()
	if len(moves) == 0 || len(positions) < 2 {
		return uci
	}
	return chess.AlgebraicNotation{}.Encode(positions[len(positions)-2], moves[len(moves)-1])
}

// SanToUCI converts an algebraic move string (e.g. "Nf3", "O-O") for the given
// position into UCI "fromto" form (e.g. "g1f3"). Returns "" on failure.
func SanToUCI(fen, san string) string {
	fenOpt, err := chess.FEN(fen)
	if err != nil {
		return ""
	}
	tempGame := chess.NewGame(fenOpt)
	if err := tempGame.MoveStr(san); err != nil {
		return ""
	}
	moves := tempGame.Moves()
	if len(moves) == 0 {
		return ""
	}
	last := moves[len(moves)-1]
	return last.S1().String() + last.S2().String()
}

func (a *Analyzer) GetEngineMove(fen string) (string, string, error) {
	return a.GetEngineMoveAt(fen, depthConfig, 20)
}

// GetEngineMoveAt returns the engine's best move at the given depth and
// strength. skill is the UCI Skill Level (0 weakest .. 20 strongest).
func (a *Analyzer) GetEngineMoveAt(fen string, depth, skill int) (string, string, error) {
	bestMove := ""
	if skill >= 0 {
		res, serr := a.Engine.SearchWithSkill(fen, depth, skill)
		if serr != nil {
			return "", "", serr
		}
		bestMove = res.BestMove
	} else {
		bm, _, gerr := a.Engine.GetBestMove(fen, depth)
		if gerr != nil {
			return "", "", gerr
		}
		bestMove = bm
	}
	if bestMove == "" || bestMove == "0000" {
		return "", "", fmt.Errorf("engine returned no move")
	}

	fenOpt, _ := chess.FEN(fen)
	tempGame := chess.NewGame(fenOpt, chess.UseNotation(chess.UCINotation{}))
	if err := tempGame.MoveStr(bestMove); err != nil {
		return "", "", fmt.Errorf("engine invalid move: %w", err)
	}

	san := UCItoSAN(fen, bestMove)
	return tempGame.Position().String(), san, nil
}

func (a *Analyzer) AnalyzeMoveQuality(fen, moveStr string) (*MoveEval, error) {
	fenOpt, _ := chess.FEN(fen)
	tempGame := chess.NewGame(fenOpt)

	resBefore, err := a.Engine.Evaluate(fen, depthConfig)
	if err != nil {
		return nil, fmt.Errorf("eval before: %w", err)
	}

	err = tempGame.MoveStr(moveStr)
	if err != nil {
		return nil, fmt.Errorf("invalid move: %w", err)
	}

	fenAfter := tempGame.Position().String()

	resAfter, err := a.Engine.Evaluate(fenAfter, depthConfig)
	if err != nil {
		return nil, fmt.Errorf("eval after: %w", err)
	}

	resBest, err := a.Engine.Evaluate(fen, depthConfig+4)
	if err != nil {
		return nil, fmt.Errorf("eval best: %w", err)
	}

	evalDiff := -resAfter.Centipawns - resBefore.Centipawns
	isBlunder := false
	if resBest.BestMove != "" && resBest.BestMove != moveStr {
		bestFenOpt, _ := chess.FEN(fen)
		bestGame := chess.NewGame(bestFenOpt)
		if err := bestGame.MoveStr(resBest.BestMove); err == nil {
			bestFEN := bestGame.Position().String()
			bestRes, _ := a.Engine.Evaluate(bestFEN, depthConfig)
			if bestRes != nil {
				blunderDiff := resAfter.Centipawns - bestRes.Centipawns
				if blunderDiff > 150.0 {
					isBlunder = true
				}
			}
		}
	}

	classification := ClassifyMoveMaterial(evalDiff/100.0, 0, isBlunder, MaterialTotal(fenAfter))

	return &MoveEval{
		San:            moveStr,
		EvalBefore:     resBefore.Centipawns,
		EvalAfter:      resAfter.Centipawns,
		EvalDiff:       evalDiff,
		BestMove:       resBest.BestMove,
		BestLine:       resBest.BestLine,
		Classification: classification,
	}, nil
}

func GetOverallOpeningStats() ([]models.OpeningStat, error) {
	return models.GetOpeningStats()
}

func GetOverallStatsSummary() (int, int, int, int, error) {
	blunders, mistakes, inaccuracies, totalGames, err := models.GetOverallStats()
	return blunders, mistakes, inaccuracies, totalGames, err
}

func (a *Analyzer) GetTutorFeedback(username string, game *models.Game, moveEval *MoveEval, isPlayGame bool) (string, error) {
	if a.Anthropic == nil {
		msg := fmt.Sprintf("**%s** - %s\nEvaluation dropped from %.1f to %.1f.\nBest move was **%s**: %s",
			moveEval.Classification, moveEval.San,
			moveEval.EvalBefore, moveEval.EvalAfter,
			moveEval.BestMove, moveEval.BestLine)
		return msg, nil
	}

	openingName := ""
	fenLines := strings.Split(game.PGN, "\n")
	for _, line := range fenLines {
		if strings.HasPrefix(line, "[Opening") {
			openingName = strings.Trim(line, `[]"Opening `)
		}
	}

	return a.Anthropic.GetGameFeedback(game.PGN, []MoveEval{*moveEval}, openingName, "", true)
}

func RecentGames(limit int) ([]models.Game, error) {
	return models.GetGames(limit, 0)
}

var ErrorGameNotFound = fmt.Errorf("game not found")

func FindGame(id int64) (*models.Game, error) {
	return models.GetGame(id)
}

func FindGameAnalysis(gameID int64) (*models.GameAnalysis, error) {
	return models.GetGameAnalysis(gameID)
}

func FindMoveAnalyses(gameID int64) ([]models.MoveAnalysis, error) {
	return models.GetMoveAnalyses(gameID)
}

func ImportGamesFromChessCom(username string, months int) (int, error) {
	games, err := FetchChessComGames(username, months)
	if err != nil {
		return 0, fmt.Errorf("fetch: %w", err)
	}

	imported := 0
	for _, cg := range games {
		if cg.PGN == "" {
			continue
		}

		pgnOpt, err := chess.PGN(strings.NewReader(cg.PGN))
		if err != nil {
			continue
		}
		game := chess.NewGame(pgnOpt)

		result := "draw"
		switch game.Outcome() {
		case chess.WhiteWon:
			result = "win"
		case chess.BlackWon:
			result = "loss"
		}

		endTime := time.Unix(cg.EndTime, 0)

		wp := game.GetTagPair("White")
		bp := game.GetTagPair("Black")
		tp := game.GetTagPair("Termination")
		wep := game.GetTagPair("WhiteElo")
		bep := game.GetTagPair("BlackElo")
		white := ""
		black := ""
		term := ""
		whiteElo := 0
		blackElo := 0
		if wp != nil {
			white = wp.Value
		}
		if bp != nil {
			black = bp.Value
		}
		if tp != nil {
			term = tp.Value
		}
		if wep != nil {
			whiteElo = atoiSafe(wep.Value)
		}
		if bep != nil {
			blackElo = atoiSafe(bep.Value)
		}

		mg := &models.Game{
			ChessComID:  cg.URL,
			White:       white,
			Black:       black,
			Result:      result,
			Termination: term,
			TimeClass:   cg.TimeClass,
			PGN:         cg.PGN,
			PlayedAt:    endTime,
			Username:    username,
			WhiteElo:    whiteElo,
			BlackElo:    blackElo,
		}

		if err := models.SetSetting("chesscom_username", username); err != nil {
			log.Printf("save username setting: %v", err)
		}

		_, err = models.InsertGame(mg)
		if err == nil {
			imported++
		}
	}

	return imported, nil
}

func atoiSafe(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func parsePGNInt(pgn, tag string) int {
	for _, line := range strings.Split(pgn, "\n") {
		line = strings.TrimSpace(line)
		prefix := "[" + tag + " \""
		if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, "\"]") {
			val := line[len(prefix) : len(line)-2]
			return atoiSafe(val)
		}
	}
	return 0
}

func GetChessComResult(rawResult string, username string, white string) string {
	if strings.Contains(rawResult, "1-0") {
		if strings.HasPrefix(strings.ToLower(username), strings.ToLower(white[:4])) {
			return "win"
		}
		return "loss"
	}
	if strings.Contains(rawResult, "0-1") {
		if strings.HasPrefix(strings.ToLower(username), strings.ToLower(white[:4])) {
			return "loss"
		}
		return "win"
	}
	return "draw"
}

var ErrNoAnthropicKey = fmt.Errorf("ANTHROPIC_API_KEY not set")

func init() {
	log.SetFlags(log.Ltime | log.Lshortfile)
}
