package services

import (
	"fmt"
	"log"
	"strings"

	"chess-tutor/models"

	"github.com/notnil/chess"
)

func uciLineToSAN(fen, line string, maxPly int) string {
	_, sans := replayLine(fen, line, maxPly)
	return sans
}

func replayLine(fen, line string, maxPly int) ([]string, string) {
	if line == "" {
		return nil, ""
	}
	game, err := newGameFromFEN(fen)
	if err != nil {
		return nil, ""
	}
	var fens []string
	var sans []string
	for i, tok := range strings.Fields(line) {
		if i >= maxPly {
			break
		}
		pos := game.Position()
		mv, err := chess.UCINotation{}.Decode(pos, tok)
		if err != nil {
			break
		}
		sans = append(sans, chess.AlgebraicNotation{}.Encode(pos, mv))
		if err := game.Move(mv); err != nil {
			break
		}
		fens = append(fens, game.Position().String())
	}
	return fens, strings.Join(sans, " ")
}

func lineFENsFromUCI(fen, uciLine string, maxPly int) []string {
	fens, _ := replayLine(fen, uciLine, maxPly)
	return fens
}

func evalTextFromWhite(fen string, cp float64, mate int) string {
	sideToMove := chess.White
	parts := strings.Fields(fen)
	if len(parts) > 1 && parts[1] == "b" {
		sideToMove = chess.Black
	}
	if mate != 0 {
		if sideToMove == chess.Black {
			mate = -mate
		}
		if mate > 0 {
			return fmt.Sprintf("M%d", mate)
		}
		return fmt.Sprintf("-M%d", -mate)
	}
	ev := cp
	if sideToMove == chess.Black {
		ev = -ev
	}
	ev /= 100
	if ev >= 0 {
		return fmt.Sprintf("+%.2f", ev)
	}
	return fmt.Sprintf("%.2f", ev)
}

func uciFirst(line string) string {
	for _, tok := range strings.Fields(line) {
		return tok
	}
	return ""
}

func (a *Analyzer) consistentBestLine(item *PracticeItem) string {
	if uciFirst(item.BestLine) == item.BestUCI {
		return item.BestLine
	}
	if a.Engine != nil {
		multi, err := a.Engine.EvaluateMultiPV(item.FEN, GetAnalysisDepth(), 3)
		if err == nil {
			for _, l := range multi {
				if l.BestMove == item.BestUCI {
					return l.BestLine
				}
			}
		}
		if len(multi) > 0 {
			return multi[0].BestLine
		}
	}
	return item.BestLine
}

func (a *Analyzer) ExplainPracticeMove(item *PracticeItem, userUCI, userSAN string) (string, string, []string) {
	cached, err := models.GetPracticeExplanation(item.GameID, item.MoveNumber, item.Side, userUCI)
	if err == nil && cached != nil && cached.Explanation != "" {
		uciLine := cached.BestLineUCI
		if uciLine == "" {
			uciLine = a.consistentBestLine(item)
		}
		lineFENs, bestLineSAN := replayLine(item.FEN, uciLine, 8)
		return cached.Explanation, bestLineSAN, lineFENs
	}

	uciLine := a.consistentBestLine(item)
	lineFENs, bestLineSAN := replayLine(item.FEN, uciLine, 8)
	bestEvalStr := evalTextFromWhite(item.FEN, item.BestCP, item.BestMate)

	userEvalStr := ""
	userLineSAN := ""
	if item.BestUCI == userUCI {
		userEvalStr = evalTextFromWhite(item.FEN, item.BestCP, item.BestMate)
	} else {
		for _, t := range item.Top {
			if t.UCI == userUCI {
				userEvalStr = evalTextFromWhite(item.FEN, t.CP, t.Mate)
				break
			}
		}
		if userEvalStr == "" && a.Engine != nil {
			game, err := newGameFromFEN(item.FEN)
			if err != nil {
				log.Printf("explain fen parse: %v", err)
			} else {
				pos := game.Position()
				mv, err := chess.UCINotation{}.Decode(pos, userUCI)
				if err != nil {
					log.Printf("explain move decode: %v", err)
				} else if err := game.Move(mv); err != nil {
					log.Printf("explain move apply: %v", err)
				} else {
					fenAfterUser := game.Position().String()
					res, err := a.Engine.Evaluate(fenAfterUser, GetAnalysisDepth())
					if err == nil && res != nil {
						userEvalStr = evalTextFromWhite(fenAfterUser, res.Centipawns, res.MateIn)
						userLineSAN = uciLineToSAN(fenAfterUser, res.BestLine, 6)
					}
				}
			}
		}
	}

	explanation := a.llmMoveExplanation(item, userUCI, userSAN, bestEvalStr, userEvalStr, userLineSAN, bestLineSAN)

	_ = models.SavePracticeExplanation(&models.PracticeExplanation{
		GameID:      item.GameID,
		MoveNumber:  item.MoveNumber,
		Side:        item.Side,
		UserUCI:     userUCI,
		BestUCI:     item.BestUCI,
		Explanation: explanation,
		BestLine:    bestLineSAN,
		BestLineUCI: uciLine,
	})
	return explanation, bestLineSAN, lineFENs
}

func (a *Analyzer) llmMoveExplanation(item *PracticeItem, userUCI, userSAN, bestEvalStr, userEvalStr, userLineSAN, bestLineSAN string) string {
	foundBest := item.BestUCI == userUCI

	if a.Anthropic != nil {
		side := "White"
		if strings.HasPrefix(item.Side, "b") || item.Side == "black" {
			side = "Black"
		}
		var prompt string
		if foundBest {
			prompt = fmt.Sprintf(`Position FEN: %s
Side to move: %s
The student found the best move: %s (evaluation after: %s)
Stockfish main line: %s
Explain in 2-3 sentences why %s is strong and what the resulting idea or plan is.`,
				item.FEN, side, item.BestSAN, bestEvalStr, bestLineSAN, item.BestSAN)
		} else {
			userLineNote := ""
			if userLineSAN != "" {
				userLineNote = fmt.Sprintf("\nLine after the student's move: %s", userLineSAN)
			}
			prompt = fmt.Sprintf(`Position FEN: %s
Side to move: %s
The student played: %s (evaluation after: %s)
The best move is: %s (evaluation after: %s)
Stockfish main line: %s%s
Explain in 2-3 sentences why %s is better than %s. Focus on concrete tactics or positional ideas.`,
				item.FEN, side, userSAN, userEvalStr, item.BestSAN, bestEvalStr, bestLineSAN, userLineNote, item.BestSAN, userSAN)
		}
		system := `You are a chess coach (FIDE 2400+). You explain a single move choice to a club player.
Rules:
- Answer in 2-3 short sentences.
- Only mention moves that appear in the provided Stockfish lines. Never invent variations.
- Ground everything in the given evaluation and line.
- Use SAN notation. Be concrete and encouraging.`
		fb, err := a.Anthropic.Chat(system, []anthropicMessage{{Role: "user", Content: prompt}})
		if err == nil && strings.TrimSpace(fb) != "" {
			return strings.TrimSpace(fb)
		}
		log.Printf("move explanation LLM fallback: %v", err)
	}

	return heuristicMoveExplanation(item, userSAN, bestEvalStr, userEvalStr, userLineSAN, bestLineSAN, foundBest)
}

func heuristicMoveExplanation(item *PracticeItem, userSAN, bestEvalStr, userEvalStr, userLineSAN, bestLineSAN string, foundBest bool) string {
	if foundBest {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("You found the strongest move, %s.", item.BestSAN))
		if bestEvalStr != "" {
			b.WriteString(fmt.Sprintf(" The engine evaluates the position at %s after this move.", bestEvalStr))
		}
		if bestLineSAN != "" {
			b.WriteString(fmt.Sprintf(" Its main line is %s.", bestLineSAN))
		}
		return b.String()
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("You played %s, but the engine prefers %s", userSAN, item.BestSAN))
	if bestEvalStr != "" && userEvalStr != "" {
		b.WriteString(fmt.Sprintf(" (%s after your move vs %s after the best move)", userEvalStr, bestEvalStr))
	}
	if bestLineSAN != "" {
		b.WriteString(fmt.Sprintf(". Stockfish's main line: %s", bestLineSAN))
	}
	if userLineSAN != "" {
		b.WriteString(fmt.Sprintf(" After your move the game might continue %s.", userLineSAN))
	}
	b.WriteString(".")
	return b.String()
}
