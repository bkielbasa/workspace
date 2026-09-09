package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"chess-tutor/handlers"
	"chess-tutor/models"
	"chess-tutor/services"
)

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "chess_tutor.db"
	}

	if err := models.InitDB(dbPath); err != nil {
		log.Fatalf("database: %v", err)
	}

	stockfishPath := os.Getenv("STOCKFISH_PATH")
	if stockfishPath == "" {
		stockfishPath = "/usr/games/stockfish"
	}

	engine, err := services.NewStockfishEngine(stockfishPath)
	if err != nil {
		log.Fatalf("stockfish: %v", err)
	}
	defer engine.Close()
	log.Printf("Stockfish engine initialized")

	if depth := os.Getenv("ANALYSIS_DEPTH"); depth != "" {
		d := 0
		if _, err := fmt.Sscanf(depth, "%d", &d); err == nil && d > 0 {
			services.SetAnalysisDepth(d)
			log.Printf("Analysis depth set to %d", d)
		}
	}

	anthropic := services.NewAnthropicClient()
	if anthropic != nil {
		log.Printf("Anthropic client initialized")
	} else {
		log.Printf("ANTHROPIC_API_KEY not set - running without AI tutor")
	}

	analyzer := services.NewAnalyzer(engine, anthropic)
	h := handlers.New(analyzer)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /", h.Index)
	mux.HandleFunc("GET /games", h.Games)
	mux.HandleFunc("POST /games/import", h.ImportGames)
	mux.HandleFunc("GET /games/{id}", h.GameDetail)
	mux.HandleFunc("GET /games/{id}/board/{moveId}", h.BoardAtMove)
	mux.HandleFunc("POST /games/{id}/explore", h.ExploreMove)
	mux.HandleFunc("GET /games/{id}/practice", h.GamePracticeRedirect)
	mux.HandleFunc("GET /practice", h.Practice)
	mux.HandleFunc("POST /practice/check", h.PracticeCheck)
	mux.HandleFunc("POST /games/{id}/analyze", h.AnalyzeGame)
	mux.HandleFunc("POST /games/{id}/coach", h.RegenerateCoach)
	mux.HandleFunc("POST /games/{id}/delete-analysis", h.DeleteGameAnalysis)
	mux.HandleFunc("POST /games/analyze-all", h.AnalyzeAllGames)
	mux.HandleFunc("GET /play", h.Play)
	mux.HandleFunc("POST /play/new", h.PlayNew)
	mux.HandleFunc("POST /play/move", h.PlayMove)
	mux.HandleFunc("POST /play/engine", h.PlayEngineMove)
	mux.HandleFunc("POST /play/undo", h.PlayUndo)
	mux.HandleFunc("POST /play/hint", h.PlayHint)
	mux.HandleFunc("POST /play/resign", h.PlayResign)
	mux.HandleFunc("GET /feedback", h.Feedback)
	mux.HandleFunc("GET /progress", h.Progress)
	mux.HandleFunc("GET /openings", h.Openings)

	staticDir := filepath.Join(".", "static")
	fs := http.FileServer(http.Dir(staticDir))
	mux.Handle("GET /static/", http.StripPrefix("/static/", fs))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting chess tutor on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
