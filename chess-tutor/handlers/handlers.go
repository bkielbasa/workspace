package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"chess-tutor/models"
	"chess-tutor/services"

	"github.com/notnil/chess"
)

type Handler struct {
	Analyzer  *services.Analyzer
	Templates *template.Template
	mu        sync.RWMutex
	states    map[string]*PlayState
}

type playEval struct {
	MoveNo         int
	SAN            string
	Classification string
	BestSan        string
	BestLineSan    string
}

type PlayState struct {
	FEN         string
	PGN         string
	Moves       []string
	PlayerColor string
	GameOver    bool
	Result      string
	GameID      int64
	PlayerEvals []playEval
	Skill       int
	Depth       int
}

type costlyRow struct {
	models.MoveStat
	AnalysisID int64
}

func New(analyzer *services.Analyzer) *Handler {
	funcMap := template.FuncMap{
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := 0; i < n; i++ {
				s[i] = i
			}
			return s
		},
		"add": func(nums ...int) int {
			total := 0
			for _, n := range nums {
				total += n
			}
			return total
		},
		"sub":  func(a, b int) int { return a - b },
		"mul":  func(a, b int) int { return a * b },
		"mulf": func(a, b float64) float64 { return a * b },
		"addf": func(nums ...float64) float64 {
			total := 0.0
			for _, n := range nums {
				total += n
			}
			return total
		},
		"join": func(sep string, items []string) string { return strings.Join(items, sep) },
		"div": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"safe": func(s string) template.HTML { return template.HTML(s) },
		"title": func(s string) string {
			if s == "" {
				return ""
			}
			return strings.ToUpper(s[:1]) + s[1:]
		},
		"truncate": func(s string, max int) string {
			if len(s) <= max {
				return s
			}
			return s[:max] + "..."
		},
		"fmtEval": func(cp float64, mate int) string {
			if mate > 0 {
				return fmt.Sprintf("M%d", mate)
			}
			if mate < 0 {
				return fmt.Sprintf("-M%d", -mate)
			}
			ev := cp / 100.0
			if ev >= 0 {
				return fmt.Sprintf("+%.2f", ev)
			}
			return fmt.Sprintf("%.2f", ev)
		},
		"pl": func(cp float64) string {
			return fmt.Sprintf("%.2f", cp/100.0)
		},
		"json": func(v interface{}) (string, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
		"md": func(v interface{}) template.HTML {
			switch t := v.(type) {
			case string:
				return template.HTML(services.RenderMarkdown(t))
			case template.HTML:
				return template.HTML(services.RenderMarkdown(string(t)))
			}
			return ""
		},
		"practiceURL": func(mode string, gameID int64, theme string, index int) string {
			var parts []string
			if mode != "" && mode != "daily" {
				parts = append(parts, "mode="+mode)
			}
			if mode == "game" && gameID > 0 {
				parts = append(parts, "game="+strconv.FormatInt(gameID, 10))
			}
			if mode == "theme" && theme != "" {
				parts = append(parts, "theme="+url.QueryEscape(theme))
			}
			if index >= 0 {
				parts = append(parts, "index="+strconv.Itoa(index))
			}
			if len(parts) == 0 {
				return "/practice"
			}
			return "/practice?" + strings.Join(parts, "&")
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseGlob("templates/*.html")
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}

	return &Handler{
		Analyzer:  analyzer,
		Templates: tmpl,
		states:    make(map[string]*PlayState),
	}
}

const sessionCookie = "chess_tutor_session"

func sessionID(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err == nil && c.Value != "" {
		return c.Value
	}
	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Intn(1000000))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		MaxAge:   86400 * 365,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return id
}

func (h *Handler) getPlayState(w http.ResponseWriter, r *http.Request) *PlayState {
	sid := sessionID(w, r)
	h.mu.RLock()
	ps, ok := h.states[sid]
	h.mu.RUnlock()
	if !ok {
		h.mu.Lock()
		ps = &PlayState{}
		h.states[sid] = ps
		h.mu.Unlock()
	}
	return ps
}

func (h *Handler) resetPlayState(w http.ResponseWriter, r *http.Request) *PlayState {
	sid := sessionID(w, r)
	h.mu.Lock()
	defer h.mu.Unlock()
	ps := &PlayState{}
	h.states[sid] = ps
	return ps
}

func (h *Handler) render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err := h.Templates.ExecuteTemplate(w, name, data)
	if err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "Internal error: "+err.Error(), 500)
	}
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	gameCount, _ := models.GetGameCount()
	analyzedCount, _ := models.GetAnalyzedGameCount()
	blunders, mistakes, inaccuracies, totalGames, _ := services.GetOverallStatsSummary()

	hasAnthropic := os.Getenv("ANTHROPIC_API_KEY") != ""

	data := map[string]interface{}{
		"GameCount":     gameCount,
		"AnalyzedCount": analyzedCount,
		"Blunders":      blunders,
		"Mistakes":      mistakes,
		"Inaccuracies":  inaccuracies,
		"TotalGames":    totalGames,
		"HasAnthropic":  hasAnthropic,
	}

	h.render(w, "index.html", data)
}

func (h *Handler) Games(w http.ResponseWriter, r *http.Request) {
	games, err := models.GetGames(100, 0)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	data := map[string]interface{}{
		"Games": games,
	}

	if r.Header.Get("HX-Request") != "" {
		h.render(w, "games_list", data)
	} else {
		h.render(w, "games.html", data)
	}
}

func (h *Handler) DeleteGameAnalysis(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 400)
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}
	models.DeleteMoveAnalyses(id)
	models.DeleteGameAnalysis(id)
	models.MarkGameUnanalyzed(id)
	models.TakeMetricSnapshot()
	http.Redirect(w, r, "/games/"+idStr, 303)
}

func (h *Handler) ImportGames(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 400)
		return
	}

	username := r.FormValue("username")
	monthsStr := r.FormValue("months")
	if username == "" {
		http.Error(w, "username required", 400)
		return
	}

	months := 3
	if monthsStr != "" {
		if m, err := strconv.Atoi(monthsStr); err == nil && m > 0 && m <= 12 {
			months = m
		}
	}

	count, err := services.ImportGamesFromChessCom(username, months)
	if err != nil {
		h.render(w, "games_list", map[string]interface{}{
			"Error": fmt.Sprintf("Import error: %v", err),
		})
		return
	}

	games, _ := models.GetGames(100, 0)
	h.render(w, "games_list", map[string]interface{}{
		"Games":    games,
		"Imported": count,
		"Username": username,
	})
}

func (h *Handler) GameDetail(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	game, err := models.GetGame(id)
	if err != nil {
		http.Error(w, "game not found", 404)
		return
	}

	analysis, _ := models.GetGameAnalysis(id)
	moves, _ := models.GetMoveAnalyses(id)
	gameStat, _ := models.GetGameStat(id)
	moveStats, _ := models.GetMoveStats(id)

	var costlyMoves []costlyRow
	analysisByPos := map[string]int64{}
	for _, m := range moves {
		analysisByPos[fmt.Sprintf("%d|%s", m.MoveNumber, m.Side)] = m.ID
	}
	for _, ms := range moveStats {
		if !ms.IsStudent {
			continue
		}
		costlyMoves = append(costlyMoves, costlyRow{
			MoveStat:   ms,
			AnalysisID: analysisByPos[fmt.Sprintf("%d|%s", ms.MoveNumber, ms.Side)],
		})
	}
	sort.SliceStable(costlyMoves, func(i, j int) bool {
		return costlyMoves[i].CPL > costlyMoves[j].CPL
	})
	if len(costlyMoves) > 5 {
		costlyMoves = costlyMoves[:5]
	}

	boardFEN := "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"
	boardClass := ""
	boardMove := 0
	boardSAN := ""
	boardSide := ""
	boardEval := 0.0
	var bookMoves []services.BookMove
	if len(moves) > 0 {
		last := moves[len(moves)-1]
		boardFEN = last.FEN
		boardClass = last.Classification
		boardMove = last.MoveNumber
		boardSAN = last.SAN
		boardSide = last.Side
		boardEval = last.EvalAfter
		bookMoves = services.GetBookMoves(boardFEN)
	}

	data := map[string]interface{}{
		"Game":        game,
		"Analysis":    analysis,
		"Moves":       moves,
		"GameStat":    gameStat,
		"MoveStats":   moveStats,
		"BoardFEN":    boardFEN,
		"BoardClass":  boardClass,
		"BoardMove":   boardMove,
		"BoardSAN":    boardSAN,
		"BoardSide":   boardSide,
		"BoardEval":   boardEval / 100,
		"BookMoves":   bookMoves,
		"CostlyMoves": costlyMoves,
	}

	h.render(w, "game_detail.html", data)
}

func (h *Handler) BoardAtMove(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	moveID := r.PathValue("moveId")

	gid, err := strconv.ParseInt(gameID, 10, 64)
	if err != nil {
		http.Error(w, "invalid game id", 400)
		return
	}
	mid, err := strconv.ParseInt(moveID, 10, 64)
	if err != nil {
		http.Error(w, "invalid move id", 400)
		return
	}

	moveAnalyses, _ := models.GetMoveAnalyses(gid)
	var target *models.MoveAnalysis
	for _, m := range moveAnalyses {
		if m.ID == mid {
			target = &m
			break
		}
	}
	if target == nil {
		http.Error(w, "move not found", 404)
		return
	}

	bookMoves := services.GetBookMoves(target.FEN)

	data := map[string]interface{}{
		"FEN":            target.FEN,
		"MoveNum":        target.MoveNumber,
		"Side":           target.Side,
		"Classification": target.Classification,
		"SAN":            target.SAN,
		"EvalAfter":      target.EvalAfter / 100,
		"EvalDiff":       target.EvalDiff / 100,
		"BestMove":       target.BestMove,
		"BestLine":       target.BestLine,
		"BookMoves":      bookMoves,
		"ExploreMode":    false,
	}
	h.render(w, "board_partial.html", data)
}

func (h *Handler) ExploreMove(w http.ResponseWriter, r *http.Request) {
	fen := r.FormValue("fen")
	moveStr := r.FormValue("move")

	if fen == "" {
		http.Error(w, "fen required", 400)
		return
	}

	fenOpt, err := chess.FEN(fen)
	if err != nil {
		http.Error(w, "invalid fen", 400)
		return
	}
	tempGame := chess.NewGame(fenOpt)
	pos := tempGame.Position()

	newFEN := fen
	side := "white"
	if pos.Turn() == chess.Black {
		side = "black"
	}
	san := ""
	classification := ""
	evalAfter := 0.0
	evalDiff := 0.0
	bestMove := ""
	bestLine := ""
	moveNum := 0

	if moveStr != "" {
		move, err := chess.UCINotation{}.Decode(pos, moveStr)
		if err != nil {
			move, err = chess.AlgebraicNotation{}.Decode(pos, moveStr)
			if err != nil {
				http.Error(w, "invalid move: "+err.Error(), 400)
				return
			}
		}

		san = chess.AlgebraicNotation{}.Encode(pos, move)

		evalRes, _ := h.Analyzer.Engine.Evaluate(fen, services.GetAnalysisDepth())
		evalBefore := 0.0
		if evalRes != nil {
			evalBefore = evalRes.Centipawns
		}

		err = tempGame.Move(move)
		if err != nil {
			http.Error(w, "invalid move", 400)
			return
		}
		newFEN = tempGame.Position().String()

		afterRes, _ := h.Analyzer.Engine.Evaluate(newFEN, services.GetAnalysisDepth())
		if afterRes != nil {
			evalAfter = afterRes.Centipawns
			bestMove = afterRes.BestMove
			bestLine = afterRes.BestLine
		}

		evalDiff = -evalAfter - evalBefore
		classification = services.ClassifyMoveMaterial(evalDiff/100.0, 0, false, services.MaterialTotal(fen))
	} else {
		evalRes, _ := h.Analyzer.Engine.Evaluate(fen, services.GetAnalysisDepth())
		if evalRes != nil {
			evalAfter = evalRes.Centipawns
			bestMove = evalRes.BestMove
			bestLine = evalRes.BestLine
		}
	}

	parts := strings.Split(newFEN, " ")
	if len(parts) >= 6 {
		fmt.Sscanf(parts[5], "%d", &moveNum)
	}

	bookMoves := services.GetBookMoves(newFEN)

	data := map[string]interface{}{
		"FEN":            newFEN,
		"MoveNum":        moveNum,
		"Side":           side,
		"Classification": classification,
		"SAN":            san,
		"EvalAfter":      evalAfter / 100,
		"EvalDiff":       evalDiff / 100,
		"BestMove":       bestMove,
		"BestLine":       bestLine,
		"BookMoves":      bookMoves,
		"ExploreMode":    true,
	}
	h.render(w, "board_partial.html", data)
}

type practiceMainData struct {
	Mode   string
	Game   *models.Game
	Theme  string
	Items  []services.PracticeItem
	Index  int
	Item   *services.PracticeItem
	Total  int
	Solved bool
	Result *services.PracticeCheckResult
	Themes []services.ThemeStat
	Title  string
}

func (h *Handler) practiceQuery(r *http.Request) services.PracticeQuery {
	q := services.PracticeQuery{Mode: services.NormalizeMode(r.URL.Query().Get("mode"))}
	if q.Mode == "game" {
		if id, err := strconv.ParseInt(r.URL.Query().Get("game"), 10, 64); err == nil {
			q.GameID = id
		}
	}
	q.Theme = r.URL.Query().Get("theme")
	return q
}

func (h *Handler) practiceData(q services.PracticeQuery, index int) (*practiceMainData, error) {
	if q.Mode == "theme" && q.Theme == "" {
		q.Theme = services.DefaultTheme()
	}
	items, err := services.BuildPracticeQueue(q)
	if err != nil {
		return nil, err
	}
	if index < 0 {
		index = 0
	}
	if len(items) > 0 && index >= len(items) {
		index = len(items) - 1
	}
	var item *services.PracticeItem
	if len(items) > 0 {
		item = &items[index]
	}
	var game *models.Game
	if item != nil {
		if g, err := models.GetGame(item.GameID); err == nil {
			game = g
		}
	}
	themes := services.PracticeThemes()
	title := practiceTitle(q.Mode)
	return &practiceMainData{
		Mode:   q.Mode,
		Game:   game,
		Theme:  q.Theme,
		Items:  items,
		Index:  index,
		Item:   item,
		Total:  len(items),
		Themes: themes,
		Title:  title,
	}, nil
}

func (h *Handler) Practice(w http.ResponseWriter, r *http.Request) {
	q := h.practiceQuery(r)
	index := 0
	if qs := r.URL.Query().Get("index"); qs != "" {
		if n, err := strconv.Atoi(qs); err == nil {
			index = n
		}
	}
	data, err := h.practiceData(q, index)
	if err != nil {
		http.Error(w, "no practice data", 404)
		return
	}
	if r.Header.Get("HX-Request") != "" {
		h.render(w, "practice_main.html", data)
	} else {
		h.render(w, "practice.html", data)
	}
}

func (h *Handler) GamePracticeRedirect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	http.Redirect(w, r, "/practice?mode=game&game="+id, 303)
}

func (h *Handler) PracticeCheck(w http.ResponseWriter, r *http.Request) {
	q := services.PracticeQuery{
		Mode:  services.NormalizeMode(r.FormValue("mode")),
		Theme: r.FormValue("theme"),
	}
	if gid, err := strconv.ParseInt(r.FormValue("game"), 10, 64); err == nil {
		q.GameID = gid
	}
	index, err := strconv.Atoi(r.FormValue("index"))
	if err != nil {
		http.Error(w, "invalid index", 400)
		return
	}
	fen := r.FormValue("fen")
	move := r.FormValue("move")
	if fen == "" || move == "" {
		http.Error(w, "missing fen or move", 400)
		return
	}

	res, err := services.CheckPracticeAnswer(q, index, fen, move)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	res.Explanation, res.ExplanationLine, res.LineFENs = h.Analyzer.ExplainPracticeMove(res.Item, res.UserUCI, res.UserSAN)

	items, err := services.BuildPracticeQueue(q)
	if err != nil {
		http.Error(w, "no practice data", 404)
		return
	}
	newIndex := -1
	for i := range items {
		if items[i].GameID == res.Item.GameID && items[i].MoveNumber == res.Item.MoveNumber && items[i].Side == res.Item.Side {
			items[i].AttemptCorrect = res.Item.AttemptCorrect
			newIndex = i
			break
		}
	}
	if newIndex < 0 {
		newIndex = index
	}
	if newIndex >= len(items) {
		newIndex = len(items) - 1
	}
	data := &practiceMainData{
		Mode:   q.Mode,
		Theme:  q.Theme,
		Items:  items,
		Index:  newIndex,
		Total:  len(items),
		Solved: true,
		Result: res,
		Title:  practiceTitle(q.Mode),
	}
	if len(items) > 0 {
		data.Item = &items[newIndex]
		if g, err := models.GetGame(items[newIndex].GameID); err == nil {
			data.Game = g
		}
	}
	data.Themes = services.PracticeThemes()
	h.render(w, "practice_main.html", data)
}

func practiceTitle(mode string) string {
	switch mode {
	case "game":
		return "Blunder Review"
	case "theme":
		return "Theme Drill"
	case "opponent":
		return "Punish Opponent"
	}
	return "Daily Review"
}

func (h *Handler) AnalyzeGame(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 400)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	game, err := models.GetGame(id)
	if err != nil {
		http.Error(w, "game not found", 404)
		return
	}

	go func() {
		if err := h.Analyzer.AnalyzeGame(game, ""); err != nil {
			log.Printf("analyze game %d: %v", id, err)
		}
	}()

	http.Redirect(w, r, "/games/"+idStr, 303)
}

func (h *Handler) RegenerateCoach(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 400)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	if _, err := models.GetGameAnalysis(id); err != nil {
		http.Error(w, "game not analyzed", 400)
		return
	}

	go func() {
		if err := h.Analyzer.RegenerateCoachAnalysis(id); err != nil {
			log.Printf("regenerate coach for game %d: %v", id, err)
		}
	}()

	http.Redirect(w, r, "/games/"+idStr, 303)
}

func (h *Handler) AnalyzeAllGames(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 400)
		return
	}

	games, err := models.GetUnanalyzedGames()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	for _, game := range games {
		id := game.ID
		go func(g models.Game) {
			if err := h.Analyzer.AnalyzeGame(&g, ""); err != nil {
				log.Printf("analyze game %d: %v", id, err)
			}
		}(game)
	}

	http.Redirect(w, r, "/games", 303)
}

func tutorResultPGN(result string) string {
	switch result {
	case "win":
		return "1-0"
	case "loss":
		return "0-1"
	}
	return "1/2-1/2"
}

func buildTutorPGN(moves []string, playerColor, result string) string {
	white, black := "You", "Tutor"
	if playerColor == "black" {
		white, black = "Tutor", "You"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[Event \"Tutor game\"]\n[Site \"Chess Tutor\"]\n[Date \"%s\"]\n[White \"%s\"]\n[Black \"%s\"]\n[Result \"%s\"]\n\n",
		time.Now().Format("2006.01.02"), white, black, tutorResultPGN(result))
	for i := 0; i < len(moves); i += 2 {
		fmt.Fprintf(&b, "%d.", i/2+1)
		if i < len(moves) {
			fmt.Fprintf(&b, " %s", moves[i])
		}
		if i+1 < len(moves) {
			fmt.Fprintf(&b, " %s", moves[i+1])
		}
		b.WriteString(" ")
	}
	b.WriteString(tutorResultPGN(result))
	return b.String()
}

func (h *Handler) saveTutorGame(ps *PlayState) int64 {
	if ps == nil || len(ps.Moves) == 0 {
		return 0
	}
	result := ps.Result
	if result == "" {
		result = "loss"
	}
	pgn := buildTutorPGN(ps.Moves, ps.PlayerColor, result)
	white, black := "You", "Tutor"
	if ps.PlayerColor == "black" {
		white, black = "Tutor", "You"
	}
	game := &models.Game{
		ChessComID:  fmt.Sprintf("tutor-%d", time.Now().UnixNano()),
		White:       white,
		Black:       black,
		Result:      result,
		Termination: "tutor",
		TimeClass:   "tutor",
		PGN:         pgn,
		PlayedAt:    time.Now(),
		Username:    "You",
	}
	id, err := models.InsertGame(game)
	if err != nil {
		log.Printf("save tutor game: %v", err)
		return 0
	}
	game.ID = id
	ps.GameID = id
	go h.Analyzer.AnalyzeGame(game, "You")
	return id
}

func buildMistakeSummary(ps *PlayState) ([]map[string]string, int, int, int) {
	var items []map[string]string
	var blunders, mistakes, inaccuracies int
	for _, e := range ps.PlayerEvals {
		switch e.Classification {
		case "blunder":
			blunders++
		case "mistake":
			mistakes++
		case "inaccuracy":
			inaccuracies++
		}
		if e.Classification == "blunder" || e.Classification == "mistake" || e.Classification == "inaccuracy" {
			items = append(items, map[string]string{
				"MoveNo":         strconv.Itoa(e.MoveNo),
				"SAN":            e.SAN,
				"Classification": e.Classification,
				"BestSan":        e.BestSan,
				"BestLineSan":    e.BestLineSan,
			})
		}
	}
	return items, blunders, mistakes, inaccuracies
}

func (h *Handler) Play(w http.ResponseWriter, r *http.Request) {
	sessionID(w, r)
	data := map[string]interface{}{
		"FEN":         "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"PlayerColor": "white",
		"GameActive":  false,
	}
	h.render(w, "play.html", data)
}

func (h *Handler) PlayMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 400)
		return
	}
	ps := h.getPlayState(w, r)
	if ps.GameOver {
		http.Error(w, "No active game", 400)
		return
	}

	moveStr := r.FormValue("move")
	if moveStr == "" {
		http.Error(w, "move required", 400)
		return
	}

	oldFEN := ps.FEN
	newFEN, eval, gameOver, result, err := h.Analyzer.PlayMove(oldFEN, moveStr)
	if err != nil {
		h.render(w, "play_board.html", map[string]interface{}{
			"FEN":         ps.FEN,
			"PlayerColor": ps.PlayerColor,
			"Status":      fmt.Sprintf("Invalid move: %v", err),
			"GameActive":  !ps.GameOver,
			"Moves":       ps.Moves,
		})
		return
	}

	ps.FEN = newFEN
	ps.Moves = append(ps.Moves, eval.San)
	ps.GameOver = gameOver
	if gameOver {
		ps.Result = result
	}

	lastMove := services.SanToUCI(oldFEN, eval.San)
	movesList := strings.Join(ps.Moves, " ")
	status := fmt.Sprintf("Move: %s | Eval: %.2f | %s", eval.San, eval.EvalAfter/100, eval.Classification)

	preBestSan := services.UCItoSAN(oldFEN, eval.BestMoveBefore)
	preBestLineSan := uciLineToSAN(oldFEN, eval.BestLineBefore)
	ps.PlayerEvals = append(ps.PlayerEvals, playEval{
		MoveNo:         (len(ps.Moves) + 1) / 2,
		SAN:            eval.San,
		Classification: eval.Classification,
		BestSan:        preBestSan,
		BestLineSan:    preBestLineSan,
	})

	var feedbackHTML string
	if eval.Classification == "blunder" || eval.Classification == "mistake" || eval.Classification == "inaccuracy" {
		feedbackHTML = fmt.Sprintf(`<div class="feedback %s"><strong>%s!</strong> Best was <strong>%s</strong> (%s)</div>`,
			eval.Classification, strings.Title(eval.Classification), preBestSan, preBestLineSan)
	}
	if ps.GameOver {
		status = fmt.Sprintf("Game Over: %s", result)
	}

	data := map[string]interface{}{
		"FEN":            ps.FEN,
		"PlayerColor":    ps.PlayerColor,
		"Status":         status,
		"GameActive":     !ps.GameOver,
		"Moves":          ps.Moves,
		"FeedbackHTML":   template.HTML(feedbackHTML),
		"Classification": eval.Classification,
		"EvalAfter":      eval.EvalAfter,
		"BestMove":       eval.BestMove,
		"BestLine":       eval.BestLine,
		"PGN":            movesList,
		"LastMove":       lastMove,
		"Skill":          ps.Skill,
	}
	if ps.GameOver {
		gameID := h.saveTutorGame(ps)
		items, blunders, mistakes, inaccuracies := buildMistakeSummary(ps)
		data["MistakeSummary"] = items
		data["BlunderCount"] = blunders
		data["MistakeCount"] = mistakes
		data["InaccuracyCount"] = inaccuracies
		data["SavedGameID"] = gameID
	}
	h.render(w, "play_board.html", data)
}

func (h *Handler) PlayEngineMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 400)
		return
	}
	ps := h.getPlayState(w, r)
	if ps.GameOver {
		http.Error(w, "No active game", 400)
		return
	}

	fenOpt, _ := chessFEN(ps.FEN)
	if g, ok := fenOpt.(*chess.Game); ok && g.Position().Turn() == turnForColor(ps.PlayerColor) {
		h.render(w, "play_board.html", map[string]interface{}{
			"FEN":         ps.FEN,
			"PlayerColor": ps.PlayerColor,
			"Status":      "It's your move!",
			"GameActive":  true,
			"Moves":       ps.Moves,
		})
		return
	}

	beforeFEN := ps.FEN
	fenAfter, san, err := h.Analyzer.GetEngineMoveAt(ps.FEN, ps.Depth, ps.Skill)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	ps.FEN = fenAfter
	ps.Moves = append(ps.Moves, san)

	status := fmt.Sprintf("Tutor plays: %s", san)
	fenOptAfter, _ := chessFEN(ps.FEN)
	gameOver := false
	if g, ok := fenOptAfter.(*chess.Game); ok && g.Outcome() != chess.NoOutcome {
		gameOver = true
		ps.Result = tutorGameResult(g.Outcome(), ps.PlayerColor)
		status = fmt.Sprintf("Game Over: %s", g.Outcome())
	}
	ps.GameOver = gameOver

	data := map[string]interface{}{
		"FEN":         ps.FEN,
		"PlayerColor": ps.PlayerColor,
		"Status":      status,
		"GameActive":  !ps.GameOver,
		"Moves":       ps.Moves,
		"LastMove":    services.SanToUCI(beforeFEN, san),
	}
	if ps.GameOver {
		gameID := h.saveTutorGame(ps)
		items, blunders, mistakes, inaccuracies := buildMistakeSummary(ps)
		data["MistakeSummary"] = items
		data["BlunderCount"] = blunders
		data["MistakeCount"] = mistakes
		data["InaccuracyCount"] = inaccuracies
		data["SavedGameID"] = gameID
	}
	h.render(w, "play_board.html", data)
}

// PlayUndo takes back the player's last move and the tutor's reply so the
// player can try again from the previous position.
func (h *Handler) PlayUndo(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 400)
		return
	}
	ps := h.getPlayState(w, r)
	if ps.GameOver || len(ps.Moves) == 0 {
		http.Error(w, "Nothing to undo", 400)
		return
	}

	if ps.PlayerEvals != nil && len(ps.PlayerEvals) > 0 {
		ps.PlayerEvals = ps.PlayerEvals[:len(ps.PlayerEvals)-1]
	}
	removed := 1
	if len(ps.Moves) > 1 {
		removed = 2
	}
	ps.Moves = ps.Moves[:len(ps.Moves)-removed]
	ps.GameOver = false
	ps.Result = ""

	fenOpt, _ := chess.FEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	tempGame := chess.NewGame(fenOpt)
	for _, san := range ps.Moves {
		if err := tempGame.MoveStr(san); err != nil {
			http.Error(w, "cannot rebuild position", 500)
			return
		}
	}
	ps.FEN = tempGame.Position().String()

	var lastMove string
	if len(ps.Moves) > 0 {
		beforeFEN := startFENFor(ps.Moves[:len(ps.Moves)-1])
		lastMove = services.SanToUCI(beforeFEN, ps.Moves[len(ps.Moves)-1])
	}

	h.render(w, "play_board.html", map[string]interface{}{
		"FEN":         ps.FEN,
		"PlayerColor": ps.PlayerColor,
		"Status":      "Move taken back — try again.",
		"GameActive":  true,
		"Moves":       ps.Moves,
		"LastMove":    lastMove,
	})
}

// PlayHint returns the engine's recommended move (and brief line) for the
// current position so the player can request help on their turn.
func (h *Handler) PlayHint(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 400)
		return
	}
	ps := h.getPlayState(w, r)
	if ps.GameOver {
		http.Error(w, "No active game", 400)
		return
	}

	fenOpt, _ := chessFEN(ps.FEN)
	if g, ok := fenOpt.(*chess.Game); !ok || g.Position().Turn() != turnForColor(ps.PlayerColor) {
		http.Error(w, "It's not your turn", 400)
		return
	}

	res, err := h.Analyzer.Engine.Evaluate(ps.FEN, ps.Depth)
	if err != nil || res.BestMove == "" {
		http.Error(w, "hint unavailable", 500)
		return
	}
	bestSAN := services.UCItoSAN(ps.FEN, res.BestMove)
	bestLineSAN := uciLineToSAN(ps.FEN, res.BestLine)
	line := bestLineSAN
	if line == "" {
		line = bestSAN
	}

	var lastMove string
	if len(ps.Moves) > 0 {
		lastMove = services.SanToUCI(startFENFor(ps.Moves[:len(ps.Moves)-1]), ps.Moves[len(ps.Moves)-1])
	}

	h.render(w, "play_board.html", map[string]interface{}{
		"FEN":         ps.FEN,
		"PlayerColor": ps.PlayerColor,
		"Status":      fmt.Sprintf("Hint: consider %s", bestSAN),
		"GameActive":  true,
		"Moves":       ps.Moves,
		"LastMove":    lastMove,
		"HintSAN":     bestSAN,
		"HintLineSAN": line,
		"Skill":       ps.Skill,
	})
}

func startFENFor(moves []string) string {
	fenOpt, _ := chess.FEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	tempGame := chess.NewGame(fenOpt)
	for _, san := range moves {
		if err := tempGame.MoveStr(san); err != nil {
			return ""
		}
	}
	return tempGame.Position().String()
}

func tutorGameResult(outcome chess.Outcome, playerColor string) string {
	switch outcome {
	case chess.WhiteWon:
		if playerColor == "white" {
			return "win"
		}
		return "loss"
	case chess.BlackWon:
		if playerColor == "black" {
			return "win"
		}
		return "loss"
	}
	return "draw"
}

func (h *Handler) PlayNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 400)
		return
	}

	color := r.FormValue("color")
	if color == "" {
		color = "white"
	}
	difficulty := r.FormValue("difficulty")
	if difficulty == "" {
		difficulty = "advanced"
	}
	skill, depth := services.EngineSettingsForDifficulty(difficulty)

	ps := h.resetPlayState(w, r)
	ps.FEN = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"
	ps.Moves = []string{}
	ps.PlayerColor = color
	ps.PlayerEvals = []playEval{}
	ps.Skill = skill
	ps.Depth = depth

	engineMoved := false
	lastMove := ""
	status := "Game started! Make your move."
	if color == "black" {
		startFEN := ps.FEN
		fenAfter, san, err := h.Analyzer.GetEngineMoveAt(ps.FEN, ps.Depth, ps.Skill)
		if err == nil && fenAfter != "" {
			ps.FEN = fenAfter
			ps.Moves = append(ps.Moves, san)
			engineMoved = true
			lastMove = services.SanToUCI(startFEN, san)
			status = fmt.Sprintf("Tutor plays: %s — your turn.", san)
		}
	}

	h.render(w, "play_board.html", map[string]interface{}{
		"FEN":         ps.FEN,
		"PlayerColor": color,
		"Status":      status,
		"GameActive":  true,
		"Moves":       ps.Moves,
		"EngineMoved": engineMoved,
		"LastMove":    lastMove,
		"Skill":       ps.Skill,
	})
}

func (h *Handler) PlayResign(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 400)
		return
	}
	ps := h.getPlayState(w, r)
	if len(ps.Moves) == 0 {
		http.Redirect(w, r, "/play", 303)
		return
	}
	ps.Result = "loss"
	gameID := h.saveTutorGame(ps)
	h.resetPlayState(w, r)
	if gameID > 0 {
		http.Redirect(w, r, fmt.Sprintf("/games/%d", gameID), 303)
		return
	}
	http.Redirect(w, r, "/play", 303)
}

func (h *Handler) Feedback(w http.ResponseWriter, r *http.Request) {
	stats, err := models.GetDashboardStats()
	if err != nil {
		stats = &models.DashboardStats{}
	}

	var analysisText string
	if h.Analyzer != nil && h.Analyzer.Anthropic != nil && stats.AnalyzedCount > 0 {
		statsJSON := services.OpeningStatsJSON{}
		for _, s := range stats.OpeningStats {
			statsJSON.Openings = append(statsJSON.Openings, services.OpeningsStat{
				Name:     s.Name,
				ECO:      s.ECO,
				Games:    s.Games,
				ScorePct: s.ScorePct,
			})
		}
		analysisText, _ = h.Analyzer.Anthropic.GetOverallFeedback(statsJSON, stats.TotalBlunders, stats.TotalMistakes, stats.TotalInaccuracies)
	} else if stats.AnalyzedCount > 0 {
		var b strings.Builder
		b.WriteString("<h2>Overall Statistics</h2>\n")
		b.WriteString(fmt.Sprintf("<p><strong>Total games analyzed:</strong> %d<br>\n", stats.AnalyzedCount))
		b.WriteString(fmt.Sprintf("<strong>Average accuracy:</strong> %.1f%%<br>\n", stats.AvgAccuracy))
		b.WriteString(fmt.Sprintf("<strong>Total blunders:</strong> %d (avg %.1f/game)<br>\n", stats.TotalBlunders, stats.AvgBlundersPer))
		b.WriteString(fmt.Sprintf("<strong>Total mistakes:</strong> %d (avg %.1f/game)<br>\n", stats.TotalMistakes, stats.AvgMistakesPer))
		b.WriteString(fmt.Sprintf("<strong>Total inaccuracies:</strong> %d (avg %.1f/game)<br>\n", stats.TotalInaccuracies, stats.AvgInaccuraciesPer))
		b.WriteString(fmt.Sprintf("<strong>Avg book moves:</strong> %.1f per game</p>\n", stats.AvgBookMoves))

		b.WriteString("<h3>Opening Performance</h3>\n<table class=\"data-table\">\n<thead><tr><th>Opening</th><th>Games</th><th>Score %</th><th>Avg Book Moves</th></tr></thead>\n<tbody>\n")
		for _, s := range stats.OpeningStats {
			b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%d</td><td>%.0f%%</td><td>%.1f</td></tr>\n", s.Name, s.Games, s.ScorePct, s.AvgBookMoves))
		}
		b.WriteString("</tbody>\n</table>\n")

		b.WriteString("<h3>Recommendations</h3>\n<ul>\n")
		if stats.TotalBlunders > 0 {
			b.WriteString(fmt.Sprintf("<li><strong>Tactics:</strong> You average %.1f blunders per game. Focus on tactics training.</li>\n", stats.AvgBlundersPer))
		}
		if stats.TotalMistakes > 0 {
			b.WriteString(fmt.Sprintf("<li><strong>Positional play:</strong> You average %.1f mistakes per game. Study positional concepts.</li>\n", stats.AvgMistakesPer))
		}
		if len(stats.OpeningStats) > 0 {
			worst := stats.OpeningStats[len(stats.OpeningStats)-1]
			if worst.ScorePct < 50 && worst.Games >= 2 {
				b.WriteString(fmt.Sprintf("<li><strong>Opening:</strong> Consider studying <strong>%s</strong> (%.0f%% score rate).</li>\n", worst.Name, worst.ScorePct))
			}
			for _, s := range stats.OpeningStats {
				if s.AvgBookMoves < 5 && s.Games >= 3 {
					b.WriteString(fmt.Sprintf("<li><strong>Opening prep:</strong> In <strong>%s</strong> you average only %.1f book moves. Study the first 8-10 moves of this opening.</li>\n", s.Name, s.AvgBookMoves))
				}
			}
		}
		b.WriteString("</ul>\n")

		analysisText = b.String()
	} else {
		analysisText = "Import and analyze games to get personalized feedback!"
	}

	snapshots, _ := models.GetMetricSnapshots(50)

	data := map[string]interface{}{
		"Stats":        stats,
		"Snapshots":    snapshots,
		"AnalysisText": template.HTML(analysisText),
	}

	h.render(w, "feedback.html", data)
}

func (h *Handler) Progress(w http.ResponseWriter, r *http.Request) {
	report, err := services.BuildProgressReport()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	recentStats, err := services.GetRecentGameStats(20)
	if err != nil {
		recentStats = nil
	}

	data := map[string]interface{}{
		"Report":      report,
		"RecentStats": recentStats,
	}

	h.render(w, "progress.html", data)
}

func (h *Handler) Openings(w http.ResponseWriter, r *http.Request) {
	openingStats, _ := services.GetOverallOpeningStats()

	var recommendations string
	if h.Analyzer != nil && h.Analyzer.Anthropic != nil && len(openingStats) > 0 {
		statsJSON := services.OpeningStatsJSON{}
		for _, s := range openingStats {
			statsJSON.Openings = append(statsJSON.Openings, services.OpeningsStat{
				Name:     s.Name,
				ECO:      s.ECO,
				Games:    s.Games,
				ScorePct: s.ScorePct,
			})
		}
		jsonBytes, _ := json.Marshal(statsJSON)
		rec, err := h.Analyzer.Anthropic.GetOpeningRecommendations(string(jsonBytes))
		if err == nil {
			recommendations = rec
		}
	}

	if recommendations == "" {
		var b strings.Builder
		b.WriteString("<h2>Opening Recommendations</h2>\n")
		if len(openingStats) == 0 {
			b.WriteString("<p>Analyze some games first to get opening recommendations.</p>\n")
		} else {
			b.WriteString("<p>Based on your analyzed games:</p>\n<ul>\n")
			for _, s := range openingStats {
				score := "needs work"
				if s.ScorePct >= 60 {
					score = "doing well"
				} else if s.ScorePct >= 40 {
					score = "average"
				}
				b.WriteString(fmt.Sprintf("<li><strong>%s</strong> (%s): %d games, %.0f%% score - %s</li>\n",
					s.Name, s.ECO, s.Games, s.ScorePct, score))
			}
			b.WriteString("</ul>\n")
		}
		recommendations = b.String()
	}

	data := map[string]interface{}{
		"Stats":           openingStats,
		"Recommendations": template.HTML(recommendations),
	}

	h.render(w, "openings.html", data)
}

func renderBoardFromFEN(fen, perspective, highlightClass string) string {
	game, err := chessFEN(fen)
	if err != nil {
		return "<p>invalid position</p>"
	}
	return renderBoardHTML(game, perspective, highlightClass)
}

func startingBoardHTML() string {
	return renderBoardFromFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", "white", "")
}

func chessFEN(fen string) (interface{}, error) {
	fenOpt, err := chess.FEN(fen)
	if err != nil {
		return nil, err
	}
	return chess.NewGame(fenOpt), nil
}

func turnForColor(color string) chess.Color {
	if color == "black" {
		return chess.Black
	}
	return chess.White
}

func uciLineToSAN(fen, line string) string {
	fenOpt, err := chess.FEN(fen)
	if err != nil || line == "" {
		return line
	}
	tempGame := chess.NewGame(fenOpt)
	parts := strings.Split(line, " ")
	sans := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		move, err := chess.UCINotation{}.Decode(tempGame.Position(), p)
		if err != nil {
			return strings.Join(sans, " ")
		}
		sans = append(sans, chess.AlgebraicNotation{}.Encode(tempGame.Position(), move))
		if err := tempGame.Move(move); err != nil {
			return strings.Join(sans, " ")
		}
	}
	return strings.Join(sans, " ")
}

func renderBoardHTML(game interface{}, perspective string, highlightClass string) string {
	g, ok := game.(*chess.Game)
	if !ok {
		return "error"
	}
	pos := g.Position()
	boardMap := pos.Board()

	var rows []string
	files := []string{"a", "b", "c", "d", "e", "f", "g", "h"}

	startRank, endRank, step := 7, -1, -1
	startFile, endFile, fStep := 0, 8, 1
	if perspective == "black" {
		startRank, endRank, step = 0, 8, 1
		startFile, endFile, fStep = 7, -1, -1
	}

	for rank := startRank; rank != endRank; rank += step {
		row := `<div class="board-row">`
		for fi := startFile; fi != endFile; fi += fStep {
			file := files[fi]
			sq := chess.NewSquare(chess.File(fi), chess.Rank(rank))
			piece := boardMap.Piece(sq)
			symbol := pieceSymbol(piece)
			colorClass := "white-square"
			if (rank+fi)%2 == 1 {
				colorClass = "black-square"
			}
			coord := file + strconv.Itoa(rank+1)
			row += fmt.Sprintf(`<div class="square %s" data-square="%s">%s</div>`, colorClass, coord, symbol)
		}
		row += `</div>`
		rows = append(rows, row)
	}

	return strings.Join(rows, "\n")
}

func pieceSymbol(p chess.Piece) string {
	symbols := map[chess.PieceType]string{
		chess.King:   "♔",
		chess.Queen:  "♕",
		chess.Rook:   "♖",
		chess.Bishop: "♗",
		chess.Knight: "♘",
		chess.Pawn:   "♙",
	}
	sym, ok := symbols[p.Type()]
	if !ok {
		return ""
	}
	if p.Color() == chess.Black {
		switch p.Type() {
		case chess.King:
			sym = "♚"
		case chess.Queen:
			sym = "♛"
		case chess.Rook:
			sym = "♜"
		case chess.Bishop:
			sym = "♝"
		case chess.Knight:
			sym = "♞"
		case chess.Pawn:
			sym = "♟"
		}
	}
	return sym
}

func init() {
	log.SetFlags(log.Ltime | log.Lshortfile)
}
