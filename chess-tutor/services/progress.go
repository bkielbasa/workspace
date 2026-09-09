package services

import (
	"database/sql"
	"math"
	"sort"

	"chess-tutor/models"
)

type ProgressConfig struct {
	RollingWindow int
	FlagPctDelta  float64
}

var defaultProgressConfig = ProgressConfig{
	RollingWindow: 10,
	FlagPctDelta:  15.0,
}

func SetProgressConfig(c ProgressConfig) {
	if c.RollingWindow > 0 {
		defaultProgressConfig.RollingWindow = c.RollingWindow
	}
	if c.FlagPctDelta > 0 {
		defaultProgressConfig.FlagPctDelta = c.FlagPctDelta
	}
}

type RollingSummary struct {
	Games               int
	Window              int
	AvgAccuracy         float64
	AvgCPL              float64
	BlundersPerGame     float64
	MistakesPerGame     float64
	InaccuraciesPerGame float64
	ErrorRate           float64
	MissedTacticsPer    float64
	WinRate             float64
}

type TimeClassSummary struct {
	TimeClass   string
	Games       int
	AvgAccuracy float64
	AvgCPL      float64
	BlundersPer float64
	MistakesPer float64
	InaccPer    float64
	WinRate     float64
}

type PhaseTrend struct {
	Phase         string
	Games         int
	AvgCPL        float64
	ErrorsPerGame float64
}

type PatternTrend struct {
	Pattern string
	Phase   string
	Count   int
	AvgCPL  float64
}

type OpeningTrend struct {
	ECO       string
	Name      string
	Games     int
	AvgCPL    float64
	AvgAcc    float64
	ErrorsPer float64
}

type SignificanceRow struct {
	Metric    string
	Current   float64
	Prior     float64
	DeltaPct  float64
	Flagged   bool
	Direction string
}

type FocusItem struct {
	Pattern string
	Times   int
	AvgCPL  float64
	Worst   float64
	Tip     string
}

type ProgressReport struct {
	Summary          RollingSummary
	PriorSummary     RollingSummary
	TimeClassStats   []TimeClassSummary
	PhaseTrends      []PhaseTrend
	PatternTrends    []PatternTrend
	OpeningTrends    []OpeningTrend
	Significance     []SignificanceRow
	RecommendedFocus []FocusItem
	Window           int
	HasEnoughGames   bool
}

func pctDelta(cur, prev float64) float64 {
	if prev == 0 {
		if cur == 0 {
			return 0
		}
		return 100
	}
	return (cur - prev) / math.Abs(prev) * 100
}

func significanceFlag(cur, prev float64) (float64, bool) {
	delta := pctDelta(cur, prev)
	return delta, math.Abs(delta) >= defaultProgressConfig.FlagPctDelta
}

func listFromGameStats(stats []models.GameStat) []models.GameStat {
	sort.SliceStable(stats, func(i, j int) bool {
		return stats[i].PlayedAt.Before(stats[j].PlayedAt)
	})
	return stats
}

func summarize(stats []models.GameStat) RollingSummary {
	var s RollingSummary
	s.Games = len(stats)
	if s.Games == 0 {
		return s
	}
	var acc, cpl, blun, mis, ina, miss float64
	for _, g := range stats {
		acc += g.Accuracy
		cpl += g.AvgCPL
		blun += float64(g.Blunders)
		mis += float64(g.Mistakes)
		ina += float64(g.Inaccuracies)
		miss += float64(g.MissedTactics)
		if g.Result == "win" {
			s.WinRate += 100
		} else if g.Result == "draw" {
			s.WinRate += 50
		}
	}
	n := float64(s.Games)
	s.AvgAccuracy = acc / n
	s.AvgCPL = cpl / n
	s.BlundersPerGame = blun / n
	s.MistakesPerGame = mis / n
	s.InaccuraciesPerGame = ina / n
	s.MissedTacticsPer = miss / n
	totalErrors := blun + mis + ina
	s.ErrorRate = totalErrors / n
	s.WinRate = s.WinRate / n
	return s
}

func BuildProgressReport() (*ProgressReport, error) {
	allStats, err := models.GetGameStats()
	if err != nil {
		return nil, err
	}
	chrono := listFromGameStats(allStats)

	report := &ProgressReport{}
	window := defaultProgressConfig.RollingWindow
	report.Window = window

	if len(chrono) == 0 {
		return report, nil
	}

	recent := chrono
	prior := chrono
	if len(chrono) > window {
		recent = chrono[len(chrono)-window:]
		prior = chrono[:len(chrono)-window]
		if len(prior) > window {
			prior = prior[len(prior)-window:]
		}
	}
	report.HasEnoughGames = len(chrono) >= 2

	report.Summary = summarize(recent)
	report.PriorSummary = summarize(prior)

	if report.Summary.AvgAccuracy == 0 && report.Summary.Games == 0 {
		report.Summary.Window = window
	}

	report.TimeClassStats = timeClassStats(recent)
	report.PhaseTrends = phaseTrends(recent)
	report.PatternTrends = patternTrends(recent)
	report.OpeningTrends = openingTrends(recent)
	report.Significance = buildSignificance(report.Summary, report.PriorSummary)
	report.RecommendedFocus = recommendedFocus(recent)
	return report, nil
}

func patternTip(pattern string) string {
	switch pattern {
	case "checkmate":
		return "Study mating patterns: back-rank mates, Anastasia's mate, and checkmating with queen + rook."
	case "capture":
		return "Check every capture and recapture. Verify it does not hang material or walk into a tactic."
	case "check":
		return "Give a check only when it improves your position. Ask: does this force something useful?"
	case "promotion":
		return "When a pawn advances, calculate the promotion square carefully and support it with pieces."
	case "fork":
		return "Look for knight forks and queen forks in every position — loose pieces are fork bait."
	case "pin":
		return "Find moves that pin an enemy piece to its king or queen. Target pieces that defend other pieces."
	case "skewer":
		return "Rooks and bishops shine in open files and diagonals. Set up skewers against king and queen."
	default:
		return "Review the missed tactic and try to spot the same idea in your next games."
	}
}

// recommendedFocus ranks tactical patterns the student got wrong by how much
// they cost, so the coach report can point at the most valuable thing to drill.
func recommendedFocus(recent []models.GameStat) []FocusItem {
	type acc struct {
		count         int
		cplSum, worst float64
	}
	grouped := map[string]*acc{}
	for _, g := range recent {
		moves, err := models.GetMoveStats(g.GameID)
		if err != nil {
			continue
		}
		for _, m := range moves {
			if !m.IsStudent || m.Pattern == "" || m.Pattern == "quiet" {
				continue
			}
			if m.Classification != "inaccuracy" && m.Classification != "mistake" && m.Classification != "blunder" {
				continue
			}
			a := grouped[m.Pattern]
			if a == nil {
				a = &acc{}
				grouped[m.Pattern] = a
			}
			a.count++
			a.cplSum += m.CPL
			if m.CPL > a.worst {
				a.worst = m.CPL
			}
		}
	}
	var out []FocusItem
	for pattern, a := range grouped {
		out = append(out, FocusItem{
			Pattern: pattern,
			Times:   a.count,
			AvgCPL:  a.cplSum / float64(a.count),
			Worst:   a.worst,
			Tip:     patternTip(pattern),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].AvgCPL*float64(out[i].Times) > out[j].AvgCPL*float64(out[j].Times)
	})
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func timeClassStats(recent []models.GameStat) []TimeClassSummary {
	grouped := map[string][]models.GameStat{}
	for _, g := range recent {
		tc := g.TimeClass
		if tc == "" {
			tc = "unknown"
		}
		grouped[tc] = append(grouped[tc], g)
	}
	var out []TimeClassSummary
	for tc, games := range grouped {
		s := summarize(games)
		out = append(out, TimeClassSummary{
			TimeClass:   tc,
			Games:       s.Games,
			AvgAccuracy: s.AvgAccuracy,
			AvgCPL:      s.AvgCPL,
			BlundersPer: s.BlundersPerGame,
			MistakesPer: s.MistakesPerGame,
			InaccPer:    s.InaccuraciesPerGame,
			WinRate:     s.WinRate,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Games > out[j].Games })
	return out
}

func phaseTrends(recent []models.GameStat) []PhaseTrend {
	openingCPL, midCPL, endCPL := 0.0, 0.0, 0.0
	openingGames, midGames, endGames := 0, 0, 0
	openingErrs, midErrs, endErrs := 0.0, 0.0, 0.0

	for _, g := range recent {
		if g.OpeningCPL > 0 || g.OpeningErrors > 0 {
			openingCPL += g.OpeningCPL
			openingGames++
			openingErrs += float64(g.OpeningErrors)
		}
		if g.MiddlegameCPL > 0 || g.MiddlegameErrors > 0 {
			midCPL += g.MiddlegameCPL
			midGames++
			midErrs += float64(g.MiddlegameErrors)
		}
		if g.EndgameCPL > 0 || g.EndgameErrors > 0 {
			endCPL += g.EndgameCPL
			endGames++
			endErrs += float64(g.EndgameErrors)
		}
	}

	mk := func(name string, cpl float64, games, errs int) PhaseTrend {
		p := PhaseTrend{Phase: name, Games: games}
		if games > 0 {
			p.AvgCPL = cpl / float64(games)
			p.ErrorsPerGame = float64(errs) / float64(games)
		}
		return p
	}
	out := []PhaseTrend{
		mk("opening", openingCPL, openingGames, int(openingErrs)),
		mk("middlegame", midCPL, midGames, int(midErrs)),
		mk("endgame", endCPL, endGames, int(endErrs)),
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Games > out[j].Games })
	return out
}

func patternTrends(recent []models.GameStat) []PatternTrend {
	type key struct{ pattern, phase string }
	counts := map[key]int{}
	cplSum := map[key]float64{}
	for _, g := range recent {
		moves, err := models.GetMoveStats(g.GameID)
		if err != nil {
			continue
		}
		for _, m := range moves {
			if !m.IsStudent || m.Pattern == "" || m.Pattern == "quiet" {
				continue
			}
			k := key{m.Pattern, m.Phase}
			counts[k]++
			cplSum[k] += m.CPL
		}
	}
	var out []PatternTrend
	for k, c := range counts {
		out = append(out, PatternTrend{
			Pattern: k.pattern,
			Phase:   k.phase,
			Count:   c,
			AvgCPL:  cplSum[k] / float64(c),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func openingTrends(recent []models.GameStat) []OpeningTrend {
	grouped := map[string][]models.GameStat{}
	for _, g := range recent {
		key := g.OpeningECO
		if key == "" {
			key = "unknown"
		}
		grouped[key] = append(grouped[key], g)
	}
	var out []OpeningTrend
	for eco, games := range grouped {
		s := summarize(games)
		name := games[0].OpeningName
		out = append(out, OpeningTrend{
			ECO:       eco,
			Name:      name,
			Games:     s.Games,
			AvgCPL:    s.AvgCPL,
			AvgAcc:    s.AvgAccuracy,
			ErrorsPer: s.ErrorRate,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Games > out[j].Games })
	return out
}

func buildSignificance(cur, prior RollingSummary) []SignificanceRow {
	var rows []SignificanceRow
	add := func(name string, cur, prev float64) {
		delta, flagged := significanceFlag(cur, prev)
		dir := ""
		switch name {
		case "Accuracy", "Win rate":
			if delta > 0 {
				dir = "up"
			} else if delta < 0 {
				dir = "down"
			}
		default:
			if delta < 0 {
				dir = "up"
			} else if delta > 0 {
				dir = "down"
			}
		}
		rows = append(rows, SignificanceRow{
			Metric:    name,
			Current:   cur,
			Prior:     prev,
			DeltaPct:  delta,
			Flagged:   flagged,
			Direction: dir,
		})
	}
	if prior.Games > 0 {
		add("Accuracy", cur.AvgAccuracy, prior.AvgAccuracy)
		add("Avg CPL", cur.AvgCPL, prior.AvgCPL)
		add("Blunders/game", cur.BlundersPerGame, prior.BlundersPerGame)
		add("Mistakes/game", cur.MistakesPerGame, prior.MistakesPerGame)
		add("Inaccuracies/game", cur.InaccuraciesPerGame, prior.InaccuraciesPerGame)
		add("Win rate", cur.WinRate, prior.WinRate)
	}
	return rows
}

func GetRecentGameStats(limit int) ([]models.GameStat, error) {
	all, err := models.GetGameStats()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].PlayedAt.After(all[j].PlayedAt)
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func queryFloat(query string, args ...interface{}) float64 {
	var v sql.NullFloat64
	if err := models.DB.QueryRow(query, args...).Scan(&v); err == nil && v.Valid {
		return v.Float64
	}
	return 0
}
