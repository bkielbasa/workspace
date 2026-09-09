package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ChessComPlayer struct {
	Username string `json:"username"`
	Rating   int    `json:"rating"`
	Result   string `json:"result"`
}

type ChessComGame struct {
	URL       string         `json:"url"`
	PGN       string         `json:"pgn"`
	TimeClass string         `json:"time_class"`
	EndTime   int64          `json:"end_time"`
	Rated     bool           `json:"rated"`
	White     ChessComPlayer `json:"white"`
	Black     ChessComPlayer `json:"black"`
}

type ChessComGamesResponse struct {
	Games []ChessComGame `json:"games"`
}

func FetchChessComGames(username string, months int) ([]ChessComGame, error) {
	var allGames []ChessComGame
	now := time.Now()

	for i := 0; i < months; i++ {
		t := now.AddDate(0, -i, 0)
		url := fmt.Sprintf("https://api.chess.com/pub/player/%s/games/%d/%02d", username, t.Year(), t.Month())

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("request: %w", err)
		}
		req.Header.Set("User-Agent", "ChessTutor/1.0")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", url, err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", url, err)
		}

		if resp.StatusCode == 404 {
			continue
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("chess.com API %d for %s", resp.StatusCode, url)
		}

		var data ChessComGamesResponse
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, fmt.Errorf("parse %s: %w", url, err)
		}

		allGames = append(allGames, data.Games...)
	}

	return allGames, nil
}
