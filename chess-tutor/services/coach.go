package services

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"chess-tutor/models"
)

type CoachInput struct {
	GameID       int64              `json:"game_id"`
	Result       string             `json:"result"`
	TimeClass    string             `json:"time_class"`
	OpeningName  string             `json:"opening_name"`
	OpeningECO   string             `json:"opening_eco"`
	Accuracy     float64            `json:"accuracy"`
	AvgCPL       float64            `json:"avg_cpl"`
	Blunders     int                `json:"blunders"`
	Mistakes     int                `json:"mistakes"`
	Inaccuracies int                `json:"inaccuracies"`
	MissedTactics int               `json:"missed_tactics"`
	ThrowWin     bool               `json:"throw_win"`
	Recovered    bool               `json:"recovered"`
	FirstError   int                `json:"first_error_move"`
	PhaseCPL     map[string]float64 `json:"phase_cpl"`
	ClockError   float64            `json:"clock_avg_on_errors"`
	ClockOK      float64            `json:"clock_avg_on_ok"`
	TopCostly    []CostlyMove       `json:"top_costly_moves"`
	Baseline     Baseline           `json:"baseline"`
}

type CostlyMove struct {
	Move       string  `json:"san"`
	MoveNumber int     `json:"move_number"`
	CPL        float64 `json:"cpl"`
	Phase      string  `json:"phase"`
	BestMove   string  `json:"best_move"`
	Pattern    string  `json:"pattern"`
}

type Baseline struct {
	GamesAnalyzed int     `json:"games_analyzed"`
	AvgAccuracy   float64 `json:"avg_accuracy"`
	AvgCPL        float64 `json:"avg_cpl"`
	BlundersPer   float64 `json:"blunders_per_game"`
	MistakesPer   float64 `json:"mistakes_per_game"`
	InaccPer      float64 `json:"inaccuracies_per_game"`
}

func BuildCoachInput(gs *models.GameStat, historical []models.GameStat) CoachInput {
	input := CoachInput{
		GameID:        gs.GameID,
		Result:        gs.Result,
		TimeClass:     gs.TimeClass,
		OpeningName:   gs.OpeningName,
		OpeningECO:    gs.OpeningECO,
		Accuracy:      gs.Accuracy,
		AvgCPL:        gs.AvgCPL,
		Blunders:      gs.Blunders,
		Mistakes:      gs.Mistakes,
		Inaccuracies:  gs.Inaccuracies,
		MissedTactics: gs.MissedTactics,
		ThrowWin:      gs.ThrowWin,
		Recovered:     gs.Recovered,
		FirstError:    gs.FirstMistakeMove,
		PhaseCPL: map[string]float64{
			"opening":     gs.OpeningCPL,
			"middlegame":  gs.MiddlegameCPL,
			"endgame":     gs.EndgameCPL,
		},
		ClockError: gs.ClockErrorAvg,
		ClockOK:    gs.ClockOKAvg,
	}

	costly := TopCostlyMoves(gs.GameID, 5)
	for _, m := range costly {
		if !m.IsStudent {
			continue
		}
		bestMove := ""
		if m.SAN != "" {
			bestMove = m.SAN
		}
		input.TopCostly = append(input.TopCostly, CostlyMove{
			Move:       m.SAN,
			MoveNumber: m.MoveNumber,
			CPL:        m.CPL,
			Phase:      m.Phase,
			BestMove:   bestMove,
			Pattern:    m.Pattern,
		})
	}

	if len(historical) > 0 {
		bs := summarize(historical)
		input.Baseline = Baseline{
			GamesAnalyzed: bs.Games,
			AvgAccuracy:   bs.AvgAccuracy,
			AvgCPL:        bs.AvgCPL,
			BlundersPer:   bs.BlundersPerGame,
			MistakesPer:   bs.MistakesPerGame,
			InaccPer:      bs.InaccuraciesPerGame,
		}
	}

	return input
}

func (a *Analyzer) GenerateCoachFeedback(input CoachInput) string {
	if a.Anthropic != nil {
		jsonBytes, err := json.Marshal(input)
		if err != nil {
			log.Printf("coach marshal: %v", err)
		} else {
			system := `You are a personal chess coach (USCF 2400+). The student has provided structured statistics from an engine analysis of their last game, plus their historical baseline. 
Write encouraging, specific, human coaching feedback in plain text. Structure:
1. SUMMARY - one or two sentences on how the game went overall.
2. WHAT WENT WELL - 1-2 concrete points.
3. WHAT NEEDS WORK - 1-3 concrete points tied to the actual numbers and moves.
4. ONE DRILL - a single specific exercise or habit for next session.
Be honest but kind. Never invent move lines that aren't given. Use the provided numbers. Keep it under 250 words.`
			fb, err := a.Anthropic.Chat(system, []anthropicMessage{{Role: "user", Content: string(jsonBytes)}})
			if err == nil && fb != "" {
				return fb
			}
			log.Printf("coach LLM fallback: %v", err)
		}
	}
	return heuristicCoachFeedback(input)
}

func heuristicCoachFeedback(in CoachInput) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("**Coach summary** — %s game (%s%s). Accuracy **%.1f%%**, avg CPL **%.0f**.\n\n",
		in.TimeClass, in.OpeningName, ecoSuffix(in.OpeningECO), in.Accuracy, in.AvgCPL))

	if in.ThrowWin {
		b.WriteString("You were winning at some point and let it slip. When you hold an advantage, trade pieces and keep pieces active — avoid speculative tactics.\n\n")
	}
	if in.Recovered {
		b.WriteString("Great fighting spirit: you recovered from a losing position. Staying calm when behind is a real strength.\n\n")
	}

	if in.Blunders > 0 || in.Mistakes > 0 || in.Inaccuracies > 0 {
		b.WriteString(fmt.Sprintf("Errors this game: **%d blunders, %d mistakes, %d inaccuracies**. ",
			in.Blunders, in.Mistakes, in.Inaccuracies))
		if in.FirstError > 0 {
			b.WriteString(fmt.Sprintf("Your first serious error came on move **%d**. ", in.FirstError))
		}
		b.WriteString("\n")
		if in.MissedTactics > 0 {
			b.WriteString(fmt.Sprintf("You missed **%d tactical opportunities** (captures/checks/promotions the engine found). Scan for checks and captures every move before committing.\n", in.MissedTactics))
		}
	}

	for phase, cpl := range in.PhaseCPL {
		if cpl > 150 {
			b.WriteString(fmt.Sprintf("Your **%s** play was costly (avg CPL %.0f). Drill %s patterns before moving on.\n", phase, cpl, phase))
		}
	}

	if len(in.TopCostly) > 0 {
		b.WriteString("\n**Costliest moves to review:**\n")
		for _, m := range in.TopCostly {
			b.WriteString(fmt.Sprintf("- Move %d %s (phase %s, CPL %.0f)\n", m.MoveNumber, m.Move, m.Phase, m.CPL))
		}
	}

	if in.Baseline.GamesAnalyzed > 0 {
		b.WriteString("\n**Versus your baseline:**\n")
		b.WriteString(fmt.Sprintf("- Your average accuracy is %.1f%% over %d games; this game %.1f%%.\n",
			in.Baseline.AvgAccuracy, in.Baseline.GamesAnalyzed, in.Accuracy))
		if in.ClockError > 0 && in.ClockOK > 0 {
			b.WriteString(fmt.Sprintf("- You spent ~%.0fs on error moves vs ~%.0fs on good moves. Slowing down on critical positions may help.\n", in.ClockError, in.ClockOK))
		}
		b.WriteString("\n**Drill for next session:** re-play the top blunders above and find the refutation without the engine; then solve 10 tactics puzzles focused on your worst phase.")
	}

	return b.String()
}

func ecoSuffix(eco string) string {
	if eco == "" {
		return ""
	}
	return " (" + eco + ")"
}

func historyBaselineText(exceptGameID int64) string {
	allStats, err := models.GetGameStats()
	if err != nil {
		return ""
	}
	var hist []models.GameStat
	for _, s := range allStats {
		if s.GameID != exceptGameID {
			hist = append(hist, s)
		}
	}
	if len(hist) == 0 {
		return "no prior games analyzed yet"
	}
	sum := summarize(hist)
	return fmt.Sprintf("%d games | avg accuracy %.1f%% | avg CPL %.0f | blunders %.2f/game | mistakes %.2f/game | inaccuracies %.2f/game | win rate %.0f%%",
		sum.Games, sum.AvgAccuracy, sum.AvgCPL, sum.BlundersPerGame, sum.MistakesPerGame, sum.InaccuraciesPerGame, sum.WinRate)
}

func (a *Analyzer) gameFeedbackText(game *models.Game, moveEvals []MoveEval, openingDisplay string, profile StudentProfile, baseline string, blunders, mistakes, inaccuracies, bookW, bookB int) string {
	if a.Anthropic != nil {
		fb, err := a.Anthropic.GetGameFeedback(game.PGN, moveEvals, openingDisplay, baseline, false)
		if err != nil {
			log.Printf("Anthropic feedback: %v", err)
			return generateLocalFeedback(game, moveEvals, blunders, mistakes, inaccuracies, bookW, bookB, openingDisplay, profile)
		}
		return fb
	}
	return generateLocalFeedback(game, moveEvals, blunders, mistakes, inaccuracies, bookW, bookB, openingDisplay, profile)
}

func (a *Analyzer) RegenerateCoachAnalysis(gameID int64) error {
	game, err := models.GetGame(gameID)
	if err != nil {
		return err
	}
	raw, err := models.GetMoveAnalyses(gameID)
	if err != nil {
		return err
	}

	var moveEvals []MoveEval
	var blunders, mistakes, inaccuracies, bookW, bookB int
	for _, m := range raw {
		moveEvals = append(moveEvals, MoveEval{
			San:            m.SAN,
			EvalBefore:     m.EvalBefore,
			EvalAfter:      m.EvalAfter,
			EvalDiff:       m.EvalDiff,
			BestMove:       m.BestMove,
			BestLine:       m.BestLine,
			Classification: m.Classification,
		})
		switch m.Classification {
		case "blunder":
			blunders++
		case "mistake":
			mistakes++
		case "inaccuracy":
			inaccuracies++
		}
		if m.IsBook {
			if m.Side == "white" {
				bookW++
			} else {
				bookB++
			}
		}
	}

	studentUser := game.Username
	if studentUser == "" {
		studentUser, _ = models.GetSetting("chesscom_username")
	}
	playerSide := "white"
	if len(game.Black) >= 4 && strings.HasPrefix(strings.ToLower(studentUser), strings.ToLower(game.Black[:4])) {
		playerSide = "black"
	}

	var studentBlunders, studentMistakes, studentInaccuracies, studentBookMoves int
	for _, m := range raw {
		if m.Side != playerSide {
			continue
		}
		switch m.Classification {
		case "blunder":
			studentBlunders++
		case "mistake":
			studentMistakes++
		case "inaccuracy":
			studentInaccuracies++
		}
		if m.IsBook {
			studentBookMoves++
		}
	}

	studentAccuracy := 0.0
	if analysis, err := models.GetGameAnalysis(gameID); err == nil {
		studentAccuracy = analysis.AccuracyWhite
		if playerSide == "black" {
			studentAccuracy = analysis.AccuracyBlack
		}
	}
	studentElo := game.WhiteElo
	if playerSide == "black" {
		studentElo = game.BlackElo
	}
	profile := StudentProfile{
		Elo:          studentElo,
		Accuracy:     studentAccuracy,
		Blunders:     studentBlunders,
		Mistakes:     studentMistakes,
		Inaccuracies: studentInaccuracies,
		BookMoves:    studentBookMoves,
	}

	openingDisplay := game.OpeningName
	if game.OpeningECO != "" {
		openingDisplay = fmt.Sprintf("%s (%s)", game.OpeningName, game.OpeningECO)
	}
	baseline := historyBaselineText(gameID)
	feedback := a.gameFeedbackText(game, moveEvals, openingDisplay, profile, baseline, blunders, mistakes, inaccuracies, bookW, bookB)
	if err := models.UpdateGameFeedback(gameID, feedback); err != nil {
		return err
	}

	if gs, err := models.GetGameStat(gameID); err == nil {
		allStats, _ := models.GetGameStats()
		var hist []models.GameStat
		for _, s := range allStats {
			if s.GameID != gameID {
				hist = append(hist, s)
			}
		}
		input := BuildCoachInput(gs, hist)
		coach := a.GenerateCoachFeedback(input)
		if coach != "" {
			if err := models.SaveCoachFeedback(gameID, coach); err != nil {
				return err
			}
		}
	}
	return nil
}
