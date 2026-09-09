package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/notnil/chess"
)

func QueryOpeningLocal(moves []*chess.Move) (eco, name string) {
	if o := getBook().Find(moves); o != nil {
		return o.Code(), o.Title()
	}
	return "", ""
}

type LichessOpeningResponse struct {
	Opening *struct {
		ECO  string `json:"eco"`
		Name string `json:"name"`
	} `json:"opening"`
	Moves []struct {
		UCI          string `json:"uci"`
		SAN          string `json:"san"`
		AverageRating int   `json:"averageRating"`
		White        int    `json:"white"`
		Draws        int    `json:"draws"`
		Black        int    `json:"black"`
		Total        int    `json:"total"`
		Opening      *struct {
			ECO  string `json:"eco"`
			Name string `json:"name"`
		} `json:"opening"`
	} `json:"moves"`
}

type BookMove struct {
	SAN      string
	UCI      string
	Total    int
	WhitePct float64
	DrawPct  float64
	BlackPct float64
}

type OpeningMoveInfo struct {
	SAN      string  `json:"san"`
	UCI      string  `json:"uci"`
	Total    int     `json:"total"`
	WhitePct float64 `json:"white_pct"`
	DrawPct  float64 `json:"draw_pct"`
	BlackPct float64 `json:"black_pct"`
}

func QueryOpeningMoves(fen string) ([]OpeningMoveInfo, error) {
	u := fmt.Sprintf("https://explorer.lichess.ovh/masters?fen=%s&moves=20&topGames=0", url.QueryEscape(fen))

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ChessTutor/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data LichessOpeningResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	var moves []OpeningMoveInfo
	for _, m := range data.Moves {
		moves = append(moves, OpeningMoveInfo{
			SAN:    m.SAN,
			UCI:    m.UCI,
			Total:  m.Total,
			WhitePct:  float64(m.White) / float64(m.Total) * 100,
			DrawPct:   float64(m.Draws) / float64(m.Total) * 100,
			BlackPct:  float64(m.Black) / float64(m.Total) * 100,
		})
	}
	return moves, nil
}

func GetBookMoves(fen string) []BookMove {
	moves, err := QueryOpeningMoves(fen)
	if err != nil || moves == nil {
		return nil
	}
	var result []BookMove
	for _, m := range moves {
		result = append(result, BookMove{
			SAN:      m.SAN,
			UCI:      m.UCI,
			Total:    m.Total,
			WhitePct: m.WhitePct,
			DrawPct:  m.DrawPct,
			BlackPct: m.BlackPct,
		})
	}
	return result
}
