package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type AnthropicClient struct {
	apiKey  string
	model   string
	baseURL string
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func NewAnthropicClient() *AnthropicClient {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-sonnet-4-5-20250929"
	}
	if apiKey == "" {
		return nil
	}
	return &AnthropicClient{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://api.anthropic.com/v1",
	}
}

func (c *AnthropicClient) Chat(system string, messages []anthropicMessage) (string, error) {
	req := anthropicRequest{
		Model:     c.model,
		MaxTokens: 2000,
		System:    system,
		Messages:  messages,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var result anthropicResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("anthropic error: %s", result.Error.Message)
	}

	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty response")
	}

	return result.Content[0].Text, nil
}

func (c *AnthropicClient) GetGameFeedback(gamePGN string, moves []MoveEval, openings, baseline string, isPlayGame bool) (string, error) {
	system := `You are an expert chess tutor (2500+ FIDE). Your role is to provide clear, encouraging, and actionable feedback to a student. Be honest about mistakes but always frame them as learning opportunities. Use chess notation and explain concepts in simple terms. Keep responses concise: the per-game analysis must stay under 140 words total.`

	var prompt string
	if isPlayGame {
		prompt = fmt.Sprintf(`The student just played a move in a training game. Here's the position and move analysis:

%s

Move Analysis:
- Move played: %s
- Evaluation before: %.2f
- Evaluation after: %.2f
- Classification: %s
- Best move was: %s
- Best continuation: %s

%s

Provide VERY BRIEF feedback (2-3 sentences max) about this specific move. If it was a good move, explain why. If it was a mistake, explain the better option concisely.`,
			gamePGN,
			moves[len(moves)-1].San,
			moves[len(moves)-1].EvalBefore,
			moves[len(moves)-1].EvalAfter,
			moves[len(moves)-1].Classification,
			moves[len(moves)-1].BestMove,
			moves[len(moves)-1].BestLine,
			getClassificationAdvice(moves[len(moves)-1].Classification))
	} else {
		var moveDetails []string
		for i, m := range moves {
			moveDetails = append(moveDetails, formatMoveInfo(i, m))
		}

		prompt = fmt.Sprintf(`Here is a chess game played by a student (PGN):

%s

Opening: %s

Recent baseline (this student's average over their previous games): %s

Here is the analysis of each move with Stockfish evaluation (centipawns) and classification:

%s

Write SHORT feedback, at most 140 words total, with these three short parts (no headings, no markdown tables):
1. SUMMARY — one sentence on how the game went.
2. KEY MISTAKES — the 1-3 most important mistakes, one short line each, naming the actual move and the better one.
3. TAKEAWAY — one specific thing to focus on next session.

Briefly compare this game to the baseline only if it was clearly better or worse than usual. Be encouraging but honest.`,
			gamePGN, openings, baseline, strings.Join(moveDetails, "\n"))
	}

	return c.Chat(system, []anthropicMessage{{Role: "user", Content: prompt}})
}

func (c *AnthropicClient) GetOverallFeedback(statsJSON OpeningStatsJSON, blunders, mistakes, inaccuracies int) (string, error) {
	system := `You are an expert chess tutor (2500+ FIDE). Your role is to provide clear, encouraging, and actionable feedback.`

	statsStr, _ := json.Marshal(statsJSON)

	prompt := fmt.Sprintf(`Here is a chess student's overall statistics from their analyzed games:

Opening Performance:
%s

Total errors across all games:
- Blunders: %d
- Mistakes: %d
- Inaccuracies: %d

Please provide:
1. OPENING ANALYSIS: Which openings does the student play well? Which need work? Suggest specific openings to study.
2. PATTERN ANALYSIS: Based on the error distribution, what seems to be the student's main weakness (tactics, positional play, endgames, openings)?
3. STUDY PLAN: A concrete 4-week study plan with specific topics and resources
4. RECOMMENDATION: The single most important thing to focus on right now

Format as markdown with clear sections. Be encouraging but honest and specific.`,
		string(statsStr), blunders, mistakes, inaccuracies)

	return c.Chat(system, []anthropicMessage{{Role: "user", Content: prompt}})
}

func (c *AnthropicClient) GetOpeningRecommendations(statsJSON string) (string, error) {
	system := `You are an expert chess opening tutor (2500+ FIDE). Provide specific opening recommendations.`

	prompt := fmt.Sprintf(`Based on this student's opening statistics from their chess.com games:

%s

Please provide:
1. Which 2-3 openings they should focus on learning first
2. For each recommended opening: key ideas, typical pawn structures, and 1-2 main variations to know
3. Which openings they should stop playing (if any)
4. A simple repertoire suggestion for White and Black

Be specific with ECO codes and move orders. Keep it practical for a club player.`,
		statsJSON)

	return c.Chat(system, []anthropicMessage{{Role: "user", Content: prompt}})
}

type OpeningStatsJSON struct {
	Openings  []OpeningsStat  `json:"openings"`
}

type OpeningsStat struct {
	Name     string  `json:"name"`
	ECO      string  `json:"eco"`
	Games    int     `json:"games"`
	ScorePct float64 `json:"score_pct"`
}

func getClassificationAdvice(c string) string {
	switch c {
	case "blunder":
		return "Focus on this type of position in tactics training."
	case "mistake":
		return "Look for better positional or tactical options."
	case "inaccuracy":
		return "A good move can be improved. Study similar positions."
	default:
		return ""
	}
}

// countHelper is used to number moves in formatted output
var moveCount int
func ResetMoveCount() { moveCount = 0 }

func formatMoveInfo(idx int, m MoveEval) string {
	return fmt.Sprintf("%d. %s -> %.1f (was %.1f, diff %.1f) %s [best: %s]",
		idx+1, m.San, m.EvalAfter, m.EvalBefore, m.EvalDiff, m.Classification, m.BestMove)
}
