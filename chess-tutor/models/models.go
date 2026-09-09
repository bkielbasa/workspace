package models

import (
	"database/sql"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

type Game struct {
	ID           int64     `json:"id"`
	ChessComID   string    `json:"chesscom_id"`
	White        string    `json:"white"`
	Black        string    `json:"black"`
	Result       string    `json:"result"`
	Termination  string    `json:"termination"`
	TimeClass    string    `json:"time_class"`
	PGN          string    `json:"pgn"`
	PlayedAt     time.Time `json:"played_at"`
	DownloadedAt time.Time `json:"downloaded_at"`
	Analyzed     bool      `json:"analyzed"`
	OpeningECO   string    `json:"opening_eco"`
	OpeningName  string    `json:"opening_name"`
	Username     string    `json:"username"`
	WhiteElo     int       `json:"white_elo"`
	BlackElo     int       `json:"black_elo"`
}

type MoveAnalysis struct {
	ID             int64   `json:"id"`
	GameID         int64   `json:"game_id"`
	MoveNumber     int     `json:"move_number"`
	Side           string  `json:"side"`
	SAN            string  `json:"san"`
	FEN            string  `json:"fen"`
	EvalBefore     float64 `json:"eval_before"`
	EvalAfter      float64 `json:"eval_after"`
	EvalDiff       float64 `json:"eval_diff"`
	Classification string  `json:"classification"`
	BestMove       string  `json:"best_move"`
	BestLine       string  `json:"best_line"`
	IsBook         bool    `json:"is_book"`
	BestEval       float64 `json:"best_eval"`
	TopMoves       string  `json:"top_moves"`
	PlayedUCI      string  `json:"played_uci"`
}

type MoveStat struct {
	ID             int64   `json:"id"`
	GameID         int64   `json:"game_id"`
	MoveNumber     int     `json:"move_number"`
	Side           string  `json:"side"`
	SAN            string  `json:"san"`
	CPL            float64 `json:"cpl"`
	Classification string  `json:"classification"`
	Phase          string  `json:"phase"`
	Criticality    float64 `json:"criticality"`
	MissedTactic   bool    `json:"missed_tactic"`
	Top1           bool    `json:"top1"`
	Top3           bool    `json:"top3"`
	Pattern        string  `json:"pattern"`
	IsBook         bool    `json:"is_book"`
	IsStudent      bool    `json:"is_student"`
}

type GameStat struct {
	GameID               int64     `json:"game_id"`
	StudentSide          string    `json:"student_side"`
	AvgCPL               float64   `json:"avg_cpl"`
	OpponentAvgCPL       float64   `json:"opponent_avg_cpl"`
	Accuracy             float64   `json:"accuracy"`
	OpponentAccuracy     float64   `json:"opponent_accuracy"`
	Blunders             int       `json:"blunders"`
	Mistakes             int       `json:"mistakes"`
	Inaccuracies         int       `json:"inaccuracies"`
	OpponentBlunders     int       `json:"opponent_blunders"`
	OpponentMistakes     int       `json:"opponent_mistakes"`
	OpponentInaccuracies int       `json:"opponent_inaccuracies"`
	FirstMistakeMove     int       `json:"first_mistake_move"`
	OpeningCPL           float64   `json:"opening_cpl"`
	MiddlegameCPL        float64   `json:"middlegame_cpl"`
	EndgameCPL           float64   `json:"endgame_cpl"`
	OpeningErrors        int       `json:"opening_errors"`
	MiddlegameErrors     int       `json:"middlegame_errors"`
	EndgameErrors        int       `json:"endgame_errors"`
	MissedTactics        int       `json:"missed_tactics"`
	ThrowWin             bool      `json:"throw_win"`
	Recovered            bool      `json:"recovered"`
	EvalPeak             float64   `json:"eval_peak"`
	EvalValley           float64   `json:"eval_valley"`
	TimeClass            string    `json:"time_class"`
	OpeningECO           string    `json:"opening_eco"`
	OpeningName          string    `json:"opening_name"`
	Result               string    `json:"result"`
	PlayedAt             time.Time `json:"played_at"`
	WhiteElo             int       `json:"white_elo"`
	BlackElo             int       `json:"black_elo"`
	ClockErrorAvg        float64   `json:"clock_error_avg"`
	ClockOKAvg           float64   `json:"clock_ok_avg"`
	ComputedAt           time.Time `json:"computed_at"`
}

type GameAnalysis struct {
	ID             int64     `json:"id"`
	GameID         int64     `json:"game_id"`
	TotalMoves     int       `json:"total_moves"`
	AccuracyWhite  float64   `json:"accuracy_white"`
	AccuracyBlack  float64   `json:"accuracy_black"`
	Blunders       int       `json:"blunders"`
	Mistakes       int       `json:"mistakes"`
	Inaccuracies   int       `json:"inaccuracies"`
	GoodMoves      int       `json:"good_moves"`
	ExcellentMoves int       `json:"excellent_moves"`
	BookMovesWhite int       `json:"book_moves_white"`
	BookMovesBlack int       `json:"book_moves_black"`
	Strengths      string    `json:"strengths"`
	Weaknesses     string    `json:"weaknesses"`
	Feedback       string    `json:"feedback"`
	CoachFeedback  string    `json:"coach_feedback"`
	AnalyzedAt     time.Time `json:"analyzed_at"`
}

type PracticeAttempt struct {
	ID              int64     `json:"id"`
	GameID          int64     `json:"game_id"`
	MoveNumber      int       `json:"move_number"`
	Side            string    `json:"side"`
	FenBefore       string    `json:"fen_before"`
	BestUCI         string    `json:"best_uci"`
	UserUCI         string    `json:"user_uci"`
	Correct         int       `json:"correct"`
	Attempts        int       `json:"attempts"`
	LastPracticedAt time.Time `json:"last_practiced_at"`
	CreatedAt       time.Time `json:"created_at"`
}

type PracticeExplanation struct {
	ID          int64     `json:"id"`
	GameID      int64     `json:"game_id"`
	MoveNumber  int       `json:"move_number"`
	Side        string    `json:"side"`
	UserUCI     string    `json:"user_uci"`
	BestUCI     string    `json:"best_uci"`
	Explanation string    `json:"explanation"`
	BestLine    string    `json:"best_line"`
	BestLineUCI string    `json:"best_line_uci"`
	CreatedAt   time.Time `json:"created_at"`
}

type OpeningStat struct {
	ECO          string  `json:"eco"`
	Name         string  `json:"name"`
	Games        int     `json:"games"`
	Wins         int     `json:"wins"`
	Losses       int     `json:"losses"`
	Draws        int     `json:"draws"`
	ScorePct     float64 `json:"score_pct"`
	AvgBookMoves float64 `json:"avg_book_moves"`
}

type OpeningRecommendation struct {
	Name           string  `json:"name"`
	ECO            string  `json:"eco"`
	Frequency      int     `json:"frequency"`
	ScorePct       float64 `json:"score_pct"`
	Recommendation string  `json:"recommendation"`
}

const schema = `
CREATE TABLE IF NOT EXISTS games (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chesscom_id TEXT UNIQUE,
    white TEXT NOT NULL,
    black TEXT NOT NULL,
    result TEXT NOT NULL,
    termination TEXT,
    time_class TEXT,
    pgn TEXT NOT NULL,
    played_at DATETIME,
    downloaded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    analyzed BOOLEAN DEFAULT 0,
    opening_eco TEXT,
    opening_name TEXT,
    username TEXT,
    white_elo INTEGER DEFAULT 0,
    black_elo INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS move_analysis (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id INTEGER REFERENCES games(id) ON DELETE CASCADE,
    move_number INTEGER NOT NULL,
    side TEXT NOT NULL,
    san TEXT NOT NULL,
    fen TEXT,
    eval_before REAL,
    eval_after REAL,
    eval_diff REAL,
    classification TEXT,
    best_move TEXT,
    best_line TEXT,
    is_book INTEGER DEFAULT 0,
    best_eval REAL,
    top_moves TEXT,
    played_uci TEXT
);

CREATE TABLE IF NOT EXISTS move_stats (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id INTEGER REFERENCES games(id) ON DELETE CASCADE,
    move_number INTEGER NOT NULL,
    side TEXT NOT NULL,
    san TEXT NOT NULL,
    cpl REAL,
    classification TEXT,
    phase TEXT,
    criticality REAL,
    missed_tactic INTEGER DEFAULT 0,
    top1 INTEGER DEFAULT 0,
    top3 INTEGER DEFAULT 0,
    pattern TEXT,
    is_book INTEGER DEFAULT 0,
    is_student INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS game_stats (
    game_id INTEGER PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
    student_side TEXT,
    avg_cpl REAL,
    opponent_avg_cpl REAL,
    accuracy REAL,
    opponent_accuracy REAL,
    blunders INTEGER DEFAULT 0,
    mistakes INTEGER DEFAULT 0,
    inaccuracies INTEGER DEFAULT 0,
    opponent_blunders INTEGER DEFAULT 0,
    opponent_mistakes INTEGER DEFAULT 0,
    opponent_inaccuracies INTEGER DEFAULT 0,
    first_mistake_move INTEGER DEFAULT 0,
    opening_cpl REAL,
    middlegame_cpl REAL,
    endgame_cpl REAL,
    opening_errors INTEGER DEFAULT 0,
    middlegame_errors INTEGER DEFAULT 0,
    endgame_errors INTEGER DEFAULT 0,
    missed_tactics INTEGER DEFAULT 0,
    throw_win INTEGER DEFAULT 0,
    recovered INTEGER DEFAULT 0,
    eval_peak REAL DEFAULT 0,
    eval_valley REAL DEFAULT 0,
    time_class TEXT,
    opening_eco TEXT,
    opening_name TEXT,
    result TEXT,
    played_at DATETIME,
    white_elo INTEGER DEFAULT 0,
    black_elo INTEGER DEFAULT 0,
    clock_error_avg REAL,
    clock_ok_avg REAL,
    computed_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS game_analysis (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id INTEGER REFERENCES games(id) ON DELETE CASCADE UNIQUE,
    total_moves INTEGER,
    accuracy_white REAL,
    accuracy_black REAL,
    blunders INTEGER DEFAULT 0,
    mistakes INTEGER DEFAULT 0,
    inaccuracies INTEGER DEFAULT 0,
    good_moves INTEGER DEFAULT 0,
    excellent_moves INTEGER DEFAULT 0,
    book_moves_white INTEGER DEFAULT 0,
    book_moves_black INTEGER DEFAULT 0,
    strengths TEXT,
    weaknesses TEXT,
    feedback TEXT,
    coach_feedback TEXT,
    analyzed_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS practice_attempts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id INTEGER NOT NULL,
    move_number INTEGER NOT NULL,
    side TEXT NOT NULL,
    fen_before TEXT,
    best_uci TEXT,
    user_uci TEXT,
    correct INTEGER DEFAULT 0,
    attempts INTEGER DEFAULT 0,
    last_practiced_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(game_id, move_number, side)
);

CREATE TABLE IF NOT EXISTS practice_explanations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    game_id INTEGER NOT NULL,
    move_number INTEGER NOT NULL,
    side TEXT NOT NULL,
    user_uci TEXT NOT NULL,
    best_uci TEXT,
    explanation TEXT,
    best_line TEXT,
    best_line_uci TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(game_id, move_number, side, user_uci)
);

CREATE INDEX IF NOT EXISTS idx_games_analyzed ON games(analyzed);
CREATE INDEX IF NOT EXISTS idx_games_played_at ON games(played_at);
CREATE INDEX IF NOT EXISTS idx_move_analysis_game ON move_analysis(game_id);

CREATE TABLE IF NOT EXISTS metric_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    taken_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    total_games INTEGER DEFAULT 0,
    total_analyzed INTEGER DEFAULT 0,
    avg_accuracy REAL DEFAULT 0,
    avg_accuracy_white REAL DEFAULT 0,
    avg_accuracy_black REAL DEFAULT 0,
    total_blunders INTEGER DEFAULT 0,
    total_mistakes INTEGER DEFAULT 0,
    total_inaccuracies INTEGER DEFAULT 0,
    total_errors INTEGER DEFAULT 0,
    total_moves INTEGER DEFAULT 0,
    avg_blunders_per REAL DEFAULT 0,
    avg_mistakes_per REAL DEFAULT 0,
    avg_inaccuracies_per REAL DEFAULT 0,
    error_rate REAL DEFAULT 0,
    win_rate REAL DEFAULT 0,
    wins INTEGER DEFAULT 0,
    losses INTEGER DEFAULT 0,
    draws INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS app_settings (
    key TEXT PRIMARY KEY,
    value TEXT
);
`

func InitDB(dbPath string) error {
	var err error
	DB, err = sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(30000)")
	if err != nil {
		return err
	}
	_, err = DB.Exec(schema)
	if err != nil {
		return err
	}

	DB.Exec(`ALTER TABLE game_analysis ADD COLUMN book_moves_white INTEGER DEFAULT 0`)
	DB.Exec(`ALTER TABLE game_analysis ADD COLUMN book_moves_black INTEGER DEFAULT 0`)
	DB.Exec(`ALTER TABLE move_analysis ADD COLUMN is_book INTEGER DEFAULT 0`)
	DB.Exec(`ALTER TABLE games ADD COLUMN username TEXT`)
	DB.Exec(`ALTER TABLE games ADD COLUMN white_elo INTEGER DEFAULT 0`)
	DB.Exec(`ALTER TABLE games ADD COLUMN black_elo INTEGER DEFAULT 0`)
	DB.Exec(`ALTER TABLE move_analysis ADD COLUMN best_eval REAL`)
	DB.Exec(`ALTER TABLE move_analysis ADD COLUMN top_moves TEXT`)
	DB.Exec(`ALTER TABLE move_analysis ADD COLUMN played_uci TEXT`)
	DB.Exec(`ALTER TABLE game_analysis ADD COLUMN coach_feedback TEXT`)
	DB.Exec(`ALTER TABLE practice_explanations ADD COLUMN best_line_uci TEXT`)
	DB.Exec(`ALTER TABLE practice_attempts ADD COLUMN attempts INTEGER DEFAULT 0`)
	DB.Exec(`ALTER TABLE practice_attempts ADD COLUMN last_practiced_at DATETIME`)

	log.Println("Database initialized at", dbPath)
	return nil
}

func GetSetting(key string) (string, error) {
	var value string
	err := DB.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func SetSetting(key, value string) error {
	_, err := DB.Exec(`INSERT INTO app_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func InsertGame(g *Game) (int64, error) {
	res, err := DB.Exec(`INSERT OR IGNORE INTO games
		(chesscom_id, white, black, result, termination, time_class, pgn, played_at, opening_eco, opening_name, username, white_elo, black_elo)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ChessComID, g.White, g.Black, g.Result, g.Termination, g.TimeClass, g.PGN, g.PlayedAt, g.OpeningECO, g.OpeningName, g.Username, g.WhiteElo, g.BlackElo)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

func GetGames(limit, offset int) ([]Game, error) {
	rows, err := DB.Query(`SELECT id, chesscom_id, white, black, result, termination, time_class,
		pgn, played_at, downloaded_at, analyzed, opening_eco, opening_name, COALESCE(username, ''), COALESCE(white_elo, 0), COALESCE(black_elo, 0)
		FROM games ORDER BY played_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var games []Game
	for rows.Next() {
		var g Game
		err := rows.Scan(&g.ID, &g.ChessComID, &g.White, &g.Black, &g.Result,
			&g.Termination, &g.TimeClass, &g.PGN, &g.PlayedAt, &g.DownloadedAt,
			&g.Analyzed, &g.OpeningECO, &g.OpeningName, &g.Username, &g.WhiteElo, &g.BlackElo)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, nil
}

func GetGame(id int64) (*Game, error) {
	g := &Game{}
	err := DB.QueryRow(`SELECT id, chesscom_id, white, black, result, termination, time_class,
		pgn, played_at, downloaded_at, analyzed, opening_eco, opening_name, COALESCE(username, ''), COALESCE(white_elo, 0), COALESCE(black_elo, 0)
		FROM games WHERE id = ?`, id).Scan(
		&g.ID, &g.ChessComID, &g.White, &g.Black, &g.Result,
		&g.Termination, &g.TimeClass, &g.PGN, &g.PlayedAt, &g.DownloadedAt,
		&g.Analyzed, &g.OpeningECO, &g.OpeningName, &g.Username, &g.WhiteElo, &g.BlackElo)
	if err != nil {
		return nil, err
	}
	return g, nil
}

func GetGameAnalysis(gameID int64) (*GameAnalysis, error) {
	a := &GameAnalysis{}
	err := DB.QueryRow(`SELECT id, game_id, total_moves, accuracy_white, accuracy_black,
		blunders, mistakes, inaccuracies, good_moves, excellent_moves, book_moves_white, book_moves_black, strengths, weaknesses, feedback, coach_feedback, analyzed_at
		FROM game_analysis WHERE game_id = ?`, gameID).Scan(
		&a.ID, &a.GameID, &a.TotalMoves, &a.AccuracyWhite, &a.AccuracyBlack,
		&a.Blunders, &a.Mistakes, &a.Inaccuracies, &a.GoodMoves, &a.ExcellentMoves,
		&a.BookMovesWhite, &a.BookMovesBlack,
		&a.Strengths, &a.Weaknesses, &a.Feedback, &a.CoachFeedback, &a.AnalyzedAt)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func GetMoveAnalyses(gameID int64) ([]MoveAnalysis, error) {
	rows, err := DB.Query(`SELECT id, game_id, move_number, side, san, fen,
		eval_before, eval_after, eval_diff, classification, best_move, best_line, is_book, best_eval, top_moves, played_uci
		FROM move_analysis WHERE game_id = ? ORDER BY move_number, CASE side WHEN 'white' THEN 0 ELSE 1 END`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var moves []MoveAnalysis
	for rows.Next() {
		var m MoveAnalysis
		var isBook int
		var bestEval sql.NullFloat64
		var topMoves sql.NullString
		var playedUCI sql.NullString
		err := rows.Scan(&m.ID, &m.GameID, &m.MoveNumber, &m.Side, &m.SAN, &m.FEN,
			&m.EvalBefore, &m.EvalAfter, &m.EvalDiff, &m.Classification, &m.BestMove, &m.BestLine, &isBook, &bestEval, &topMoves, &playedUCI)
		if err != nil {
			return nil, err
		}
		m.IsBook = isBook == 1
		m.BestEval = bestEval.Float64
		m.TopMoves = topMoves.String
		m.PlayedUCI = playedUCI.String
		moves = append(moves, m)
	}
	return moves, nil
}

func SaveMoveAnalysis(m *MoveAnalysis) error {
	_, err := DB.Exec(`INSERT INTO move_analysis
		(game_id, move_number, side, san, fen, eval_before, eval_after, eval_diff, classification, best_move, best_line, is_book, best_eval, top_moves, played_uci)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.GameID, m.MoveNumber, m.Side, m.SAN, m.FEN, m.EvalBefore, m.EvalAfter, m.EvalDiff, m.Classification, m.BestMove, m.BestLine, m.IsBook, m.BestEval, m.TopMoves, m.PlayedUCI)
	return err
}

func UpdateMoveBookFlag(gameID int64, moveNumber int, side string, isBook bool) error {
	_, err := DB.Exec(`UPDATE move_analysis SET is_book = ? WHERE game_id = ? AND move_number = ? AND side = ?`,
		isBook, gameID, moveNumber, side)
	return err
}

func GetAnalyzedGameIDs() ([]int64, error) {
	rows, err := DB.Query(`SELECT id FROM games WHERE analyzed = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func SaveGameAnalysis(a *GameAnalysis) error {
	_, err := DB.Exec(`INSERT OR REPLACE INTO game_analysis
		(game_id, total_moves, accuracy_white, accuracy_black, blunders, mistakes, inaccuracies, good_moves, excellent_moves, book_moves_white, book_moves_black, strengths, weaknesses, feedback, coach_feedback)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.GameID, a.TotalMoves, a.AccuracyWhite, a.AccuracyBlack, a.Blunders, a.Mistakes, a.Inaccuracies, a.GoodMoves, a.ExcellentMoves,
		a.BookMovesWhite, a.BookMovesBlack,
		a.Strengths, a.Weaknesses, a.Feedback, a.CoachFeedback)
	return err
}

func SaveCoachFeedback(gameID int64, coachFeedback string) error {
	_, err := DB.Exec(`UPDATE game_analysis SET coach_feedback = ? WHERE game_id = ?`, coachFeedback, gameID)
	return err
}

func UpdateGameFeedback(gameID int64, feedback string) error {
	_, err := DB.Exec(`UPDATE game_analysis SET feedback = ?, analyzed_at = CURRENT_TIMESTAMP WHERE game_id = ?`, feedback, gameID)
	return err
}

func SavePracticeAttempt(a *PracticeAttempt) error {
	_, err := DB.Exec(`INSERT INTO practice_attempts (game_id, move_number, side, fen_before, best_uci, user_uci, correct, attempts, last_practiced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(game_id, move_number, side) DO UPDATE SET
			fen_before = excluded.fen_before,
			best_uci = excluded.best_uci,
			user_uci = excluded.user_uci,
			correct = excluded.correct,
			attempts = practice_attempts.attempts + 1,
			last_practiced_at = CURRENT_TIMESTAMP`,
		a.GameID, a.MoveNumber, a.Side, a.FenBefore, a.BestUCI, a.UserUCI, a.Correct)
	return err
}

type PracticeThemeCount struct {
	Pattern  string
	Count    int
	TotalCPL float64
}

func GetPracticeThemes() ([]PracticeThemeCount, error) {
	rows, err := DB.Query(`SELECT COALESCE(pattern, ''), COUNT(*), COALESCE(SUM(cpl), 0)
		FROM move_stats
		WHERE is_student = 1 AND classification IN ('blunder','mistake','inaccuracy') AND pattern != ''
		GROUP BY pattern ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var themes []PracticeThemeCount
	for rows.Next() {
		var t PracticeThemeCount
		if err := rows.Scan(&t.Pattern, &t.Count, &t.TotalCPL); err != nil {
			return nil, err
		}
		themes = append(themes, t)
	}
	return themes, nil
}

func GetPracticeAttempts(gameID int64) ([]PracticeAttempt, error) {
	rows, err := DB.Query(`SELECT id, game_id, move_number, side, COALESCE(fen_before, ''), COALESCE(best_uci, ''), COALESCE(user_uci, ''), correct, COALESCE(attempts, 0), last_practiced_at, created_at
		FROM practice_attempts WHERE game_id = ? ORDER BY id`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attempts []PracticeAttempt
	for rows.Next() {
		var a PracticeAttempt
		var lastPracticed sql.NullTime
		if err := rows.Scan(&a.ID, &a.GameID, &a.MoveNumber, &a.Side, &a.FenBefore, &a.BestUCI, &a.UserUCI, &a.Correct, &a.Attempts, &lastPracticed, &a.CreatedAt); err != nil {
			return nil, err
		}
		if lastPracticed.Valid {
			a.LastPracticedAt = lastPracticed.Time
		}
		attempts = append(attempts, a)
	}
	return attempts, nil
}

func SavePracticeExplanation(e *PracticeExplanation) error {
	_, err := DB.Exec(`INSERT INTO practice_explanations (game_id, move_number, side, user_uci, best_uci, explanation, best_line, best_line_uci)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(game_id, move_number, side, user_uci) DO UPDATE SET
			best_uci = excluded.best_uci,
			explanation = excluded.explanation,
			best_line = excluded.best_line,
			best_line_uci = excluded.best_line_uci,
			created_at = CURRENT_TIMESTAMP`,
		e.GameID, e.MoveNumber, e.Side, e.UserUCI, e.BestUCI, e.Explanation, e.BestLine, e.BestLineUCI)
	return err
}

func GetPracticeExplanation(gameID int64, moveNumber int, side, userUCI string) (*PracticeExplanation, error) {
	e := &PracticeExplanation{}
	err := DB.QueryRow(`SELECT id, game_id, move_number, side, user_uci, COALESCE(best_uci, ''), COALESCE(explanation, ''), COALESCE(best_line, ''), COALESCE(best_line_uci, ''), created_at
		FROM practice_explanations WHERE game_id = ? AND move_number = ? AND side = ? AND user_uci = ?`,
		gameID, moveNumber, side, userUCI).Scan(
		&e.ID, &e.GameID, &e.MoveNumber, &e.Side, &e.UserUCI, &e.BestUCI, &e.Explanation, &e.BestLine, &e.BestLineUCI, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func MarkGameUnanalyzed(gameID int64) error {
	_, err := DB.Exec(`UPDATE games SET analyzed = 0 WHERE id = ?`, gameID)
	return err
}

func MarkGameAnalyzed(gameID int64) error {
	_, err := DB.Exec(`UPDATE games SET analyzed = 1 WHERE id = ?`, gameID)
	return err
}

func GetUnanalyzedGames() ([]Game, error) {
	rows, err := DB.Query(`SELECT id, chesscom_id, white, black, result, termination, time_class,
		pgn, played_at, downloaded_at, analyzed, opening_eco, opening_name
		FROM games WHERE analyzed = 0 ORDER BY played_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var games []Game
	for rows.Next() {
		var g Game
		err := rows.Scan(&g.ID, &g.ChessComID, &g.White, &g.Black, &g.Result,
			&g.Termination, &g.TimeClass, &g.PGN, &g.PlayedAt, &g.DownloadedAt,
			&g.Analyzed, &g.OpeningECO, &g.OpeningName)
		if err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, nil
}

func GetGameCount() (int, error) {
	var count int
	err := DB.QueryRow(`SELECT COUNT(*) FROM games`).Scan(&count)
	return count, err
}

func GetAnalyzedGameCount() (int, error) {
	var count int
	err := DB.QueryRow(`SELECT COUNT(*) FROM games WHERE analyzed = 1`).Scan(&count)
	return count, err
}

func GetOpeningStats() ([]OpeningStat, error) {
	rows, err := DB.Query(`SELECT COALESCE(g.opening_eco, '?'), COALESCE(g.opening_name, 'Unknown'),
		COUNT(*), SUM(CASE WHEN g.result = 'win' THEN 1 ELSE 0 END),
		SUM(CASE WHEN g.result = 'loss' THEN 1 ELSE 0 END),
		SUM(CASE WHEN g.result = 'draw' THEN 1 ELSE 0 END),
		COALESCE(ROUND(AVG(ga.book_moves_white + ga.book_moves_black), 1), 0)
		FROM games g LEFT JOIN game_analysis ga ON ga.game_id = g.id
		WHERE g.analyzed = 1 AND g.opening_eco IS NOT NULL
		GROUP BY g.opening_eco ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []OpeningStat
	for rows.Next() {
		var s OpeningStat
		err := rows.Scan(&s.ECO, &s.Name, &s.Games, &s.Wins, &s.Losses, &s.Draws, &s.AvgBookMoves)
		if err != nil {
			return nil, err
		}
		if s.Games > 0 {
			s.ScorePct = float64(s.Wins*100+s.Draws*50) / float64(s.Games)
		}
		stats = append(stats, s)
	}
	return stats, nil
}

func GetOverallStats() (blunders, mistakes, inaccuracies, totalGames int, err error) {
	err = DB.QueryRow(`SELECT COALESCE(SUM(blunders),0), COALESCE(SUM(mistakes),0), COALESCE(SUM(inaccuracies),0), COUNT(*)
		FROM game_analysis`).Scan(&blunders, &mistakes, &inaccuracies, &totalGames)
	return
}

type DashboardStats struct {
	GameCount     int
	AnalyzedCount int
	Wins          int
	Losses        int
	Draws         int
	WinRate       float64

	AvgAccuracy      float64
	AvgAccuracyWhite float64
	AvgAccuracyBlack float64

	TotalBlunders      int
	TotalMistakes      int
	TotalInaccuracies  int
	TotalErrors        int
	AvgBlundersPer     float64
	AvgMistakesPer     float64
	AvgInaccuraciesPer float64
	AvgErrorsPer       float64
	ErrorRate          float64

	AvgMovesPerGame float64
	AvgBookMoves    float64
	TotalMoves      int

	WinRateWhite float64
	WinRateBlack float64

	RecentAccuracy []float64
	RecentErrors   []int

	BestAccuracy    float64
	BestAccuracyID  int64
	WorstAccuracy   float64
	WorstAccuracyID int64

	OpeningStats   []OpeningStat
	TimeClassStats []TimeClassStat
}

type MetricSnapshot struct {
	ID                 int64
	TakenAt            time.Time
	TotalGames         int
	TotalAnalyzed      int
	AvgAccuracy        float64
	AvgAccuracyWhite   float64
	AvgAccuracyBlack   float64
	TotalBlunders      int
	TotalMistakes      int
	TotalInaccuracies  int
	TotalErrors        int
	TotalMoves         int
	AvgBlundersPer     float64
	AvgMistakesPer     float64
	AvgInaccuraciesPer float64
	ErrorRate          float64
	WinRate            float64
	Wins               int
	Losses             int
	Draws              int
}

func (m MetricSnapshot) AvgErrorsPer() float64 {
	return m.AvgBlundersPer + m.AvgMistakesPer + m.AvgInaccuraciesPer
}

func TakeMetricSnapshot() error {
	s := &DashboardStats{}
	DB.QueryRow(`SELECT COUNT(*) FROM games`).Scan(&s.GameCount)
	DB.QueryRow(`SELECT COUNT(*) FROM games WHERE analyzed = 1`).Scan(&s.AnalyzedCount)
	DB.QueryRow(`SELECT COALESCE(SUM(CASE WHEN result='win' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN result='loss' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN result='draw' THEN 1 ELSE 0 END),0) FROM games`).Scan(&s.Wins, &s.Losses, &s.Draws)
	if s.GameCount > 0 {
		s.WinRate = float64(s.Wins*100+s.Draws*50) / float64(s.GameCount)
	}
	DB.QueryRow(`SELECT COALESCE(AVG(accuracy_white),0), COALESCE(AVG(accuracy_black),0) FROM game_analysis`).Scan(&s.AvgAccuracyWhite, &s.AvgAccuracyBlack)
	s.AvgAccuracy = (s.AvgAccuracyWhite + s.AvgAccuracyBlack) / 2
	DB.QueryRow(`SELECT COALESCE(SUM(blunders),0), COALESCE(SUM(mistakes),0), COALESCE(SUM(inaccuracies),0),
		COALESCE(SUM(total_moves),0), COUNT(*) FROM game_analysis`).Scan(&s.TotalBlunders, &s.TotalMistakes, &s.TotalInaccuracies, &s.TotalMoves, &s.AnalyzedCount)
	s.TotalErrors = s.TotalBlunders + s.TotalMistakes + s.TotalInaccuracies
	if s.AnalyzedCount > 0 {
		s.AvgBlundersPer = float64(s.TotalBlunders) / float64(s.AnalyzedCount)
		s.AvgMistakesPer = float64(s.TotalMistakes) / float64(s.AnalyzedCount)
		s.AvgInaccuraciesPer = float64(s.TotalInaccuracies) / float64(s.AnalyzedCount)
		s.AvgErrorsPer = float64(s.TotalErrors) / float64(s.AnalyzedCount)
		s.AvgMovesPerGame = float64(s.TotalMoves) / float64(s.AnalyzedCount)
	}
	if s.TotalMoves > 0 {
		s.ErrorRate = float64(s.TotalErrors) / float64(s.TotalMoves) * 100
	}

	_, err := DB.Exec(`INSERT INTO metric_snapshots
		(total_games, total_analyzed, avg_accuracy, avg_accuracy_white, avg_accuracy_black,
		total_blunders, total_mistakes, total_inaccuracies, total_errors, total_moves,
		avg_blunders_per, avg_mistakes_per, avg_inaccuracies_per, error_rate,
		win_rate, wins, losses, draws)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.GameCount, s.AnalyzedCount, s.AvgAccuracy, s.AvgAccuracyWhite, s.AvgAccuracyBlack,
		s.TotalBlunders, s.TotalMistakes, s.TotalInaccuracies, s.TotalErrors, s.TotalMoves,
		s.AvgBlundersPer, s.AvgMistakesPer, s.AvgInaccuraciesPer, s.ErrorRate,
		s.WinRate, s.Wins, s.Losses, s.Draws)
	return err
}

func GetMetricSnapshots(limit int) ([]MetricSnapshot, error) {
	rows, err := DB.Query(`SELECT id, taken_at, total_games, total_analyzed,
		avg_accuracy, avg_accuracy_white, avg_accuracy_black,
		total_blunders, total_mistakes, total_inaccuracies, total_errors, total_moves,
		avg_blunders_per, avg_mistakes_per, avg_inaccuracies_per, error_rate,
		win_rate, wins, losses, draws
		FROM metric_snapshots ORDER BY taken_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snapshots []MetricSnapshot
	for rows.Next() {
		var ms MetricSnapshot
		rows.Scan(&ms.ID, &ms.TakenAt, &ms.TotalGames, &ms.TotalAnalyzed,
			&ms.AvgAccuracy, &ms.AvgAccuracyWhite, &ms.AvgAccuracyBlack,
			&ms.TotalBlunders, &ms.TotalMistakes, &ms.TotalInaccuracies, &ms.TotalErrors, &ms.TotalMoves,
			&ms.AvgBlundersPer, &ms.AvgMistakesPer, &ms.AvgInaccuraciesPer, &ms.ErrorRate,
			&ms.WinRate, &ms.Wins, &ms.Losses, &ms.Draws)
		snapshots = append(snapshots, ms)
	}
	return snapshots, nil
}

type TimeClassStat struct {
	TimeClass string
	Games     int
	Wins      int
	Losses    int
	Draws     int
	WinRate   float64
}

func GetDashboardStats() (*DashboardStats, error) {
	s := &DashboardStats{}

	DB.QueryRow(`SELECT COUNT(*) FROM games`).Scan(&s.GameCount)
	DB.QueryRow(`SELECT COUNT(*) FROM games WHERE analyzed = 1`).Scan(&s.AnalyzedCount)
	DB.QueryRow(`SELECT COALESCE(SUM(CASE WHEN result='win' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN result='loss' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN result='draw' THEN 1 ELSE 0 END),0) FROM games`).Scan(&s.Wins, &s.Losses, &s.Draws)
	if s.GameCount > 0 {
		s.WinRate = float64(s.Wins*100+s.Draws*50) / float64(s.GameCount)
	}

	DB.QueryRow(`SELECT COALESCE(AVG(accuracy_white),0), COALESCE(AVG(accuracy_black),0) FROM game_analysis`).Scan(&s.AvgAccuracyWhite, &s.AvgAccuracyBlack)
	s.AvgAccuracy = (s.AvgAccuracyWhite + s.AvgAccuracyBlack) / 2

	DB.QueryRow(`SELECT
		COALESCE(SUM(blunders),0), COALESCE(SUM(mistakes),0), COALESCE(SUM(inaccuracies),0),
		COALESCE(SUM(total_moves),0), COUNT(*),
		COALESCE(AVG(book_moves_white + book_moves_black),0)
		FROM game_analysis`).Scan(&s.TotalBlunders, &s.TotalMistakes, &s.TotalInaccuracies, &s.TotalMoves, &s.AnalyzedCount, &s.AvgBookMoves)
	s.TotalErrors = s.TotalBlunders + s.TotalMistakes + s.TotalInaccuracies
	if s.AnalyzedCount > 0 {
		s.AvgBlundersPer = float64(s.TotalBlunders) / float64(s.AnalyzedCount)
		s.AvgMistakesPer = float64(s.TotalMistakes) / float64(s.AnalyzedCount)
		s.AvgInaccuraciesPer = float64(s.TotalInaccuracies) / float64(s.AnalyzedCount)
		s.AvgErrorsPer = float64(s.TotalErrors) / float64(s.AnalyzedCount)
	}
	if s.TotalMoves > 0 {
		s.ErrorRate = float64(s.TotalErrors) / float64(s.TotalMoves) * 100
		s.AvgMovesPerGame = float64(s.TotalMoves) / float64(s.AnalyzedCount)
	}

	DB.QueryRow(`SELECT COALESCE(SUM(CASE WHEN g.result='win' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN g.result='loss' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN g.result='draw' THEN 1 ELSE 0 END),0)
		FROM games g WHERE g.analyzed = 1 AND g.white = g.white`).Scan(&s.Wins, &s.Losses, &s.Draws)

	rows, err := DB.Query(`SELECT (ga.accuracy_white + ga.accuracy_black) / 2,
		ga.blunders + ga.mistakes + ga.inaccuracies
		FROM game_analysis ga JOIN games g ON g.id = ga.game_id
		WHERE g.analyzed = 1 ORDER BY g.played_at DESC LIMIT 10`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var acc float64
			var errs int
			rows.Scan(&acc, &errs)
			s.RecentAccuracy = append(s.RecentAccuracy, acc)
			s.RecentErrors = append(s.RecentErrors, errs)
		}
	}

	DB.QueryRow(`SELECT COALESCE((ga.accuracy_white + ga.accuracy_black) / 2,0), ga.game_id
		FROM game_analysis ga ORDER BY (ga.accuracy_white + ga.accuracy_black) / 2 DESC LIMIT 1`).Scan(&s.BestAccuracy, &s.BestAccuracyID)
	DB.QueryRow(`SELECT COALESCE((ga.accuracy_white + ga.accuracy_black) / 2,0), ga.game_id
		FROM game_analysis ga ORDER BY (ga.accuracy_white + ga.accuracy_black) / 2 ASC LIMIT 1`).Scan(&s.WorstAccuracy, &s.WorstAccuracyID)

	stats, _ := GetOpeningStats()
	s.OpeningStats = stats

	tcRows, err := DB.Query(`SELECT COALESCE(time_class,'unknown'), COUNT(*),
		SUM(CASE WHEN result='win' THEN 1 ELSE 0 END),
		SUM(CASE WHEN result='loss' THEN 1 ELSE 0 END),
		SUM(CASE WHEN result='draw' THEN 1 ELSE 0 END)
		FROM games WHERE analyzed = 1 GROUP BY time_class ORDER BY COUNT(*) DESC`)
	if err == nil {
		defer tcRows.Close()
		for tcRows.Next() {
			var tc TimeClassStat
			tcRows.Scan(&tc.TimeClass, &tc.Games, &tc.Wins, &tc.Losses, &tc.Draws)
			if tc.Games > 0 {
				tc.WinRate = float64(tc.Wins*100+tc.Draws*50) / float64(tc.Games)
			}
			s.TimeClassStats = append(s.TimeClassStats, tc)
		}
	}

	return s, nil
}

func UpdateGameOpening(gameID int64, eco, name string) error {
	_, err := DB.Exec(`UPDATE games SET opening_eco = ?, opening_name = ? WHERE id = ?`, eco, name, gameID)
	return err
}

func DeleteMoveAnalyses(gameID int64) error {
	_, err := DB.Exec(`DELETE FROM move_analysis WHERE game_id = ?`, gameID)
	return err
}

func DeleteGameAnalysis(gameID int64) error {
	_, err := DB.Exec(`DELETE FROM game_analysis WHERE game_id = ?`, gameID)
	return err
}

func DeleteMoveStats(gameID int64) error {
	_, err := DB.Exec(`DELETE FROM move_stats WHERE game_id = ?`, gameID)
	return err
}

func DeleteGameStat(gameID int64) error {
	_, err := DB.Exec(`DELETE FROM game_stats WHERE game_id = ?`, gameID)
	return err
}

func ReplaceMoveStats(gameID int64, stats []MoveStat) error {
	if err := DeleteMoveStats(gameID); err != nil {
		return err
	}
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO move_stats
		(game_id, move_number, side, san, cpl, classification, phase, criticality, missed_tactic, top1, top3, pattern, is_book, is_student)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, s := range stats {
		missed := 0
		top1 := 0
		top3 := 0
		book := 0
		student := 0
		if s.MissedTactic {
			missed = 1
		}
		if s.Top1 {
			top1 = 1
		}
		if s.Top3 {
			top3 = 1
		}
		if s.IsBook {
			book = 1
		}
		if s.IsStudent {
			student = 1
		}
		if _, err := stmt.Exec(s.GameID, s.MoveNumber, s.Side, s.SAN, s.CPL, s.Classification, s.Phase, s.Criticality, missed, top1, top3, s.Pattern, book, student); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func GetMoveStats(gameID int64) ([]MoveStat, error) {
	rows, err := DB.Query(`SELECT id, game_id, move_number, side, san, cpl, classification, phase, criticality, missed_tactic, top1, top3, pattern, is_book, is_student
		FROM move_stats WHERE game_id = ? ORDER BY move_number, CASE side WHEN 'white' THEN 0 ELSE 1 END`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []MoveStat
	for rows.Next() {
		var s MoveStat
		var missed, top1, top3, book, student int
		err := rows.Scan(&s.ID, &s.GameID, &s.MoveNumber, &s.Side, &s.SAN, &s.CPL, &s.Classification, &s.Phase,
			&s.Criticality, &missed, &top1, &top3, &s.Pattern, &book, &student)
		if err != nil {
			return nil, err
		}
		s.MissedTactic = missed == 1
		s.Top1 = top1 == 1
		s.Top3 = top3 == 1
		s.IsBook = book == 1
		s.IsStudent = student == 1
		stats = append(stats, s)
	}
	return stats, nil
}

func ReplaceGameStat(gs *GameStat) error {
	tw := 0
	rec := 0
	if gs.ThrowWin {
		tw = 1
	}
	if gs.Recovered {
		rec = 1
	}
	_, err := DB.Exec(`INSERT INTO game_stats
		(game_id, student_side, avg_cpl, opponent_avg_cpl, accuracy, opponent_accuracy,
		 blunders, mistakes, inaccuracies, opponent_blunders, opponent_mistakes, opponent_inaccuracies,
		 first_mistake_move, opening_cpl, middlegame_cpl, endgame_cpl,
		 opening_errors, middlegame_errors, endgame_errors, missed_tactics, throw_win, recovered,
		 eval_peak, eval_valley, time_class, opening_eco, opening_name, result, played_at,
		 white_elo, black_elo, clock_error_avg, clock_ok_avg)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(game_id) DO UPDATE SET
			student_side = excluded.student_side, avg_cpl = excluded.avg_cpl, opponent_avg_cpl = excluded.opponent_avg_cpl,
			accuracy = excluded.accuracy, opponent_accuracy = excluded.opponent_accuracy,
			blunders = excluded.blunders, mistakes = excluded.mistakes, inaccuracies = excluded.inaccuracies,
			opponent_blunders = excluded.opponent_blunders, opponent_mistakes = excluded.opponent_mistakes,
			opponent_inaccuracies = excluded.opponent_inaccuracies, first_mistake_move = excluded.first_mistake_move,
			opening_cpl = excluded.opening_cpl, middlegame_cpl = excluded.middlegame_cpl, endgame_cpl = excluded.endgame_cpl,
			opening_errors = excluded.opening_errors, middlegame_errors = excluded.middlegame_errors,
			endgame_errors = excluded.endgame_errors, missed_tactics = excluded.missed_tactics,
			throw_win = excluded.throw_win, recovered = excluded.recovered, eval_peak = excluded.eval_peak,
			eval_valley = excluded.eval_valley, time_class = excluded.time_class, opening_eco = excluded.opening_eco,
			opening_name = excluded.opening_name, result = excluded.result, played_at = excluded.played_at,
			white_elo = excluded.white_elo, black_elo = excluded.black_elo, clock_error_avg = excluded.clock_error_avg,
			clock_ok_avg = excluded.clock_ok_avg, computed_at = CURRENT_TIMESTAMP`,
		gs.GameID, gs.StudentSide, gs.AvgCPL, gs.OpponentAvgCPL, gs.Accuracy, gs.OpponentAccuracy,
		gs.Blunders, gs.Mistakes, gs.Inaccuracies, gs.OpponentBlunders, gs.OpponentMistakes, gs.OpponentInaccuracies,
		gs.FirstMistakeMove, gs.OpeningCPL, gs.MiddlegameCPL, gs.EndgameCPL,
		gs.OpeningErrors, gs.MiddlegameErrors, gs.EndgameErrors, gs.MissedTactics, tw, rec,
		gs.EvalPeak, gs.EvalValley, gs.TimeClass, gs.OpeningECO, gs.OpeningName, gs.Result, gs.PlayedAt,
		gs.WhiteElo, gs.BlackElo, gs.ClockErrorAvg, gs.ClockOKAvg)
	return err
}

func GetGameStat(gameID int64) (*GameStat, error) {
	gs := &GameStat{}
	var tw, rec int
	var playedAt sql.NullTime
	err := DB.QueryRow(`SELECT game_id, student_side, avg_cpl, opponent_avg_cpl, accuracy, opponent_accuracy,
		blunders, mistakes, inaccuracies, opponent_blunders, opponent_mistakes, opponent_inaccuracies,
		first_mistake_move, opening_cpl, middlegame_cpl, endgame_cpl,
		opening_errors, middlegame_errors, endgame_errors, missed_tactics, throw_win, recovered,
		eval_peak, eval_valley, COALESCE(time_class,''), COALESCE(opening_eco,''), COALESCE(opening_name,''),
		COALESCE(result,''), played_at, COALESCE(white_elo,0), COALESCE(black_elo,0),
		COALESCE(clock_error_avg,0), COALESCE(clock_ok_avg,0)
		FROM game_stats WHERE game_id = ?`, gameID).Scan(
		&gs.GameID, &gs.StudentSide, &gs.AvgCPL, &gs.OpponentAvgCPL, &gs.Accuracy, &gs.OpponentAccuracy,
		&gs.Blunders, &gs.Mistakes, &gs.Inaccuracies, &gs.OpponentBlunders, &gs.OpponentMistakes, &gs.OpponentInaccuracies,
		&gs.FirstMistakeMove, &gs.OpeningCPL, &gs.MiddlegameCPL, &gs.EndgameCPL,
		&gs.OpeningErrors, &gs.MiddlegameErrors, &gs.EndgameErrors, &gs.MissedTactics, &tw, &rec,
		&gs.EvalPeak, &gs.EvalValley, &gs.TimeClass, &gs.OpeningECO, &gs.OpeningName,
		&gs.Result, &playedAt, &gs.WhiteElo, &gs.BlackElo, &gs.ClockErrorAvg, &gs.ClockOKAvg)
	if err != nil {
		return nil, err
	}
	gs.ThrowWin = tw == 1
	gs.Recovered = rec == 1
	if playedAt.Valid {
		gs.PlayedAt = playedAt.Time
	}
	return gs, nil
}

func GetGameStats() ([]GameStat, error) {
	rows, err := DB.Query(`SELECT game_id, student_side, avg_cpl, opponent_avg_cpl, accuracy, opponent_accuracy,
		blunders, mistakes, inaccuracies, opponent_blunders, opponent_mistakes, opponent_inaccuracies,
		first_mistake_move, opening_cpl, middlegame_cpl, endgame_cpl,
		opening_errors, middlegame_errors, endgame_errors, missed_tactics, throw_win, recovered,
		eval_peak, eval_valley, COALESCE(time_class,''), COALESCE(opening_eco,''), COALESCE(opening_name,''),
		COALESCE(result,''), played_at, COALESCE(white_elo,0), COALESCE(black_elo,0),
		COALESCE(clock_error_avg,0), COALESCE(clock_ok_avg,0)
		FROM game_stats ORDER BY played_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []GameStat
	for rows.Next() {
		var gs GameStat
		var tw, rec int
		var playedAt sql.NullTime
		err := rows.Scan(&gs.GameID, &gs.StudentSide, &gs.AvgCPL, &gs.OpponentAvgCPL, &gs.Accuracy, &gs.OpponentAccuracy,
			&gs.Blunders, &gs.Mistakes, &gs.Inaccuracies, &gs.OpponentBlunders, &gs.OpponentMistakes, &gs.OpponentInaccuracies,
			&gs.FirstMistakeMove, &gs.OpeningCPL, &gs.MiddlegameCPL, &gs.EndgameCPL,
			&gs.OpeningErrors, &gs.MiddlegameErrors, &gs.EndgameErrors, &gs.MissedTactics, &tw, &rec,
			&gs.EvalPeak, &gs.EvalValley, &gs.TimeClass, &gs.OpeningECO, &gs.OpeningName,
			&gs.Result, &playedAt, &gs.WhiteElo, &gs.BlackElo, &gs.ClockErrorAvg, &gs.ClockOKAvg)
		if err != nil {
			return nil, err
		}
		gs.ThrowWin = tw == 1
		gs.Recovered = rec == 1
		if playedAt.Valid {
			gs.PlayedAt = playedAt.Time
		}
		stats = append(stats, gs)
	}
	return stats, nil
}
