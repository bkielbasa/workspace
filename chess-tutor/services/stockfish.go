package services

import (
	"bufio"
	"fmt"
	"log"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type StockfishEngine struct {
	cmd    *exec.Cmd
	stdin  *bufio.Writer
	stdout *bufio.Scanner
	mu     sync.Mutex
}

type EvalResult struct {
	Centipawns float64
	MateIn     int
	BestMove   string
	BestLine   string
}

type MultiPVLine struct {
	Rank       int
	Centipawns float64
	MateIn     int
	BestMove   string
	BestLine   string
}

func NewStockfishEngine(path string) (*StockfishEngine, error) {
	cmd := exec.Command(path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	sf := &StockfishEngine{
		cmd:    cmd,
		stdin:  bufio.NewWriter(stdin),
		stdout: bufio.NewScanner(stdout),
	}

	sf.send("uci")
	sf.waitFor("uciok")
	sf.send("setoption name Threads value 4")
	sf.send("setoption name Hash value 256")
	sf.send("isready")
	sf.waitFor("readyok")

	return sf, nil
}

func (sf *StockfishEngine) send(cmd string) {
	sf.stdin.WriteString(cmd + "\n")
	sf.stdin.Flush()
}

func (sf *StockfishEngine) waitFor(target string) string {
	for sf.stdout.Scan() {
		line := sf.stdout.Text()
		if strings.Contains(line, target) {
			return line
		}
	}
	return ""
}

func (sf *StockfishEngine) search(fen string, depth int) (*EvalResult, error) {
	sf.send(fmt.Sprintf("position fen %s", fen))
	sf.send(fmt.Sprintf("go depth %d", depth))

	var result EvalResult
	reEval := regexp.MustCompile(`info.*?cp (-?\d+)`)
	reMate := regexp.MustCompile(`info.*?mate (-?\d+)`)
	reBest := regexp.MustCompile(`bestmove (\S+)`)
	rePv := regexp.MustCompile(` pv (.+)$`)

	for sf.stdout.Scan() {
		line := sf.stdout.Text()
		line = strings.TrimSpace(line)

		if strings.Contains(line, "bestmove") {
			match := reBest.FindStringSubmatch(line)
			if len(match) > 1 {
				result.BestMove = match[1]
			}
			break
		}

		if strings.Contains(line, "info") {
			if m := reMate.FindStringSubmatch(line); len(m) > 1 {
				mateIn, _ := strconv.Atoi(m[1])
				result.MateIn = mateIn
				if mateIn > 0 {
					result.Centipawns = 1000 - float64(mateIn)
				} else {
					result.Centipawns = -(1000 - float64(-mateIn))
				}
			} else if m := reEval.FindStringSubmatch(line); len(m) > 1 {
				cp, _ := strconv.ParseFloat(m[1], 64)
				result.Centipawns = cp
			}
			if m := rePv.FindStringSubmatch(line); len(m) > 1 {
				result.BestLine = m[1]
			}
		}
	}

	return &result, nil
}

func (sf *StockfishEngine) Evaluate(fen string, depth int) (*EvalResult, error) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	return sf.search(fen, depth)
}

// SearchWithSkill runs a search at the given strength. skill is the UCI
// Skill Level (0 weakest .. 20 strongest). Strength is reset to full
// (skill 20) afterwards so background analysis stays strong.
func (sf *StockfishEngine) SearchWithSkill(fen string, depth, skill int) (*EvalResult, error) {
	sf.mu.Lock()
	defer sf.mu.Unlock()

	if skill >= 0 {
		sf.send(fmt.Sprintf("setoption name Skill Level value %d", skill))
		sf.send("isready")
		sf.waitFor("readyok")
	}
	defer sf.send("setoption name Skill Level value 20")

	return sf.search(fen, depth)
}

func (sf *StockfishEngine) GetBestMove(fen string, depth int) (string, string, error) {
	res, err := sf.Evaluate(fen, depth)
	if err != nil {
		return "", "", err
	}
	return res.BestMove, res.BestLine, nil
}

func (sf *StockfishEngine) EvaluateMultiPV(fen string, depth, numLines int) ([]MultiPVLine, error) {
	if numLines < 1 {
		numLines = 1
	}
	sf.mu.Lock()
	defer sf.mu.Unlock()

	sf.send(fmt.Sprintf("setoption name MultiPV value %d", numLines))
	sf.send("isready")
	sf.waitFor("readyok")
	sf.send(fmt.Sprintf("position fen %s", fen))
	sf.send(fmt.Sprintf("go depth %d", depth))

	reEval := regexp.MustCompile(`multipv\s+(\d+).*?cp (-?\d+)`)
	reMate := regexp.MustCompile(`multipv\s+(\d+).*?mate (-?\d+)`)
	reBest := regexp.MustCompile(`bestmove (\S+)`)
	rePv := regexp.MustCompile(` pv (.+)$`)

	lines := make([]MultiPVLine, 0, numLines)
	bestMove := ""

	for sf.stdout.Scan() {
		line := strings.TrimSpace(sf.stdout.Text())

		if strings.Contains(line, "bestmove") {
			if m := reBest.FindStringSubmatch(line); len(m) > 1 {
				bestMove = m[1]
			}
			break
		}

		if strings.Contains(line, "info") {
			var rank int
			var cp float64
			var mate int
			var ok bool
			if m := reMate.FindStringSubmatch(line); len(m) > 1 {
				rank, _ = strconv.Atoi(m[1])
				mate, _ = strconv.Atoi(m[2])
				if mate > 0 {
					cp = 1000 - float64(mate)
				} else {
					cp = -(1000 - float64(-mate))
				}
				ok = true
			} else if m := reEval.FindStringSubmatch(line); len(m) > 1 {
				rank, _ = strconv.Atoi(m[1])
				cp, _ = strconv.ParseFloat(m[2], 64)
				ok = true
			}
			if ok {
				entry := MultiPVLine{Rank: rank, Centipawns: cp, MateIn: mate}
				if m := rePv.FindStringSubmatch(line); len(m) > 1 {
					entry.BestLine = m[1]
					fields := strings.Fields(m[1])
					if len(fields) > 0 {
						entry.BestMove = fields[0]
					}
				}
				lines = append(lines, entry)
			}
		}
	}

	sf.send("setoption name MultiPV value 1")
	sf.send("isready")
	sf.waitFor("readyok")

	unique := make([]MultiPVLine, 0, len(lines))
	byRank := map[int]MultiPVLine{}
	for _, l := range lines {
		byRank[l.Rank] = l
	}
	seen := map[int]bool{}
	for _, l := range lines {
		if seen[l.Rank] {
			continue
		}
		seen[l.Rank] = true
		if latest, ok := byRank[l.Rank]; ok {
			unique = append(unique, latest)
		}
	}
	if bestMove != "" && len(unique) > 0 && unique[0].BestMove == "" {
		unique[0].BestMove = bestMove
	}
	return unique, nil
}

func (sf *StockfishEngine) Close() {
	sf.send("quit")
	sf.cmd.Wait()
}

func ClassifyMove(evalBefore, evalAfter float64, isBlunder bool) string {
	diff := math.Abs(evalAfter - evalBefore)
	return classify(diff, isBlunder, 1.0)
}

// ClassifyMoveMaterial is like ClassifyMove but scales thresholds by how
// much material is left. In sparse endgames a small eval swing is more
// meaningful, so thresholds tighten; in wild middlegames they loosen.
func ClassifyMoveMaterial(evalBefore, evalAfter float64, isBlunder bool, material float64) string {
	diff := math.Abs(evalAfter - evalBefore)
	factor := 1.0
	switch {
	case material <= 14.0:
		factor = 0.6
	case material >= 40.0:
		factor = 1.3
	}
	return classify(diff, isBlunder, factor)
}

func classify(diff float64, isBlunder bool, factor float64) string {
	if isBlunder {
		return "blunder"
	}
	if diff*factor >= 3.0 {
		return "blunder"
	}
	if diff*factor >= 1.5 {
		return "mistake"
	}
	if diff*factor >= 0.5 {
		return "inaccuracy"
	}
	if diff*factor >= 0.2 {
		return "good"
	}
	return "excellent"
}

// EngineSettingsForDifficulty maps a friendly difficulty label to the UCI
// Skill Level and search depth used by the tutor engine.
func EngineSettingsForDifficulty(label string) (skill, depth int) {
	switch label {
	case "beginner":
		return 1, 4
	case "intermediate":
		return 8, 8
	default:
		return 20, depthConfig
	}
}

type MoveEval struct {
	San            string
	EvalBefore     float64
	EvalAfter      float64
	EvalDiff       float64
	BestMove       string
	BestLine       string
	Classification string
	BestMoveBefore string
	BestLineBefore string
}

var depthConfig = 10

func SetAnalysisDepth(d int) {
	depthConfig = d
}

func GetAnalysisDepth() int {
	return depthConfig
}

func init() {
	log.SetFlags(log.Ltime | log.Lshortfile)
}
