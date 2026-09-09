package services

import (
	"fmt"
	"sort"
)

type Suggestion struct {
	Title  string
	Detail string
}

type StudentProfile struct {
	Elo          int
	Accuracy     float64
	Blunders     int
	Mistakes     int
	Inaccuracies int
	BookMoves    int
}

type Tip struct {
	Title  string
	Detail string
	Impact int
	Tags   []string
}

type eloBand struct {
	Min  int
	Max  int
	Tips []Tip
}

var eloBands = []eloBand{
	{
		Min: 0, Max: 399,
		Tips: []Tip{
			{Title: "Ask: 'What is my opponent threatening?'", Detail: "Before every move, check checks, captures, and threats from the other side. This alone prevents most hanging-piece blunders.", Impact: 5, Tags: []string{"blunder"}},
			{Title: "Look for hanging pieces", Detail: "Scan for loose pieces on both sides. Yours are targets for your opponent; theirs are free material for you.", Impact: 5, Tags: []string{"blunder", "tactics"}},
			{Title: "Develop knights before bishops", Detail: "Knights have fewer available squares early on, so bring them out first, toward the center.", Impact: 3, Tags: []string{"opening"}},
			{Title: "Castle early", Detail: "Get your king to safety quickly, ideally before move 8, and connect your rooks.", Impact: 4, Tags: []string{"opening"}},
			{Title: "Don't bring the queen out too soon", Detail: "An early queen becomes a target for enemy pieces. Develop minor pieces and castle first.", Impact: 3, Tags: []string{"opening"}},
			{Title: "Win a free piece safely, then it's done", Detail: "If you can capture a hanging piece without consequence, do it now instead of waiting for the 'perfect' moment.", Impact: 4, Tags: []string{"tactics"}},
			{Title: "Learn basic checkmates", Detail: "Practice K+Q vs K and K+R vs K until they are automatic. These endgames decide many games at this level.", Impact: 4, Tags: []string{"endgame"}},
			{Title: "Check every capture before making it", Detail: "Recapturing is often best, but always look one move ahead before you take to avoid walking into a fork.", Impact: 4, Tags: []string{"blunder", "tactics"}},
		},
	},
	{
		Min: 400, Max: 799,
		Tips: []Tip{
			{Title: "Run CCT before every move", Detail: "Check Checks, Captures, and Threats from both sides before choosing your move. It is the #1 blunder killer.", Impact: 5, Tags: []string{"tactics", "blunder"}},
			{Title: "Do tactical puzzles daily", Detail: "10-20 puzzles a day build pattern recognition for forks, pins, and skewers.", Impact: 5, Tags: []string{"tactics"}},
			{Title: "Count attackers and defenders", Detail: "Before trading, count how many pieces attack the square and how many defend it. Take only when you have more attackers.", Impact: 4, Tags: []string{"tactics", "calculation"}},
			{Title: "Learn forks, pins, skewers, and discovered attacks", Detail: "These four patterns are the bread and butter of this rating range. Drill them in puzzles until they pop out automatically.", Impact: 5, Tags: []string{"tactics"}},
			{Title: "Don't attack before developing", Detail: "Finish development first. Attacking with undeveloped pieces lets your opponent defend easily and counterattack.", Impact: 3, Tags: []string{"opening"}},
			{Title: "Improve your worst piece", Detail: "Every move should improve your least active piece. Rooks want open files, knights want outposts, bishops want open diagonals.", Impact: 4, Tags: []string{"positional"}},
			{Title: "Trade when ahead in material", Detail: "Exchanging pieces when you are up material simplifies the game and brings you closer to the win.", Impact: 3, Tags: []string{"strategy"}},
			{Title: "Use a blunder check", Detail: "Before moving, ask: does this hang a piece, allow mate, or lose the exchange? Re-check every candidate move.", Impact: 4, Tags: []string{"blunder"}},
		},
	},
	{
		Min: 800, Max: 1199,
		Tips: []Tip{
			{Title: "Give every move a purpose", Detail: "Aimless moves let your opponent seize the initiative. Ask what each move accomplishes before playing it.", Impact: 5, Tags: []string{"positional"}},
			{Title: "Improve your worst piece first", Detail: "Find your least active piece and give it a better square. Repeatedly activating pieces is the heart of positional play.", Impact: 4, Tags: []string{"positional"}},
			{Title: "Open files for your rooks", Detail: "A rook on a semi-open or open file dominates. Trade your opponent's rook away or control the file first.", Impact: 4, Tags: []string{"positional"}},
			{Title: "Centralize your pieces", Detail: "Pieces in the center control more squares. Put knights and bishops on strong central outposts.", Impact: 3, Tags: []string{"positional"}},
			{Title: "Don't attack without enough attackers", Detail: "Count before you attack. If you have fewer pieces committed than your opponent's defenders, the attack fails.", Impact: 4, Tags: []string{"strategy"}},
			{Title: "Learn opposition and king activity", Detail: "The king is a fighting piece in the endgame. Learn opposition and use your king actively to convert won endgames.", Impact: 4, Tags: []string{"endgame"}},
			{Title: "Knights love outposts", Detail: "A knight on a protected central square, especially deep in enemy territory, is worth a small fortune.", Impact: 3, Tags: []string{"positional"}},
			{Title: "Watch for bad bishops", Detail: "A bishop blocked by your own pawns is a 'bad bishop'. Avoid exchanging a good bishop for your opponent's bad one.", Impact: 3, Tags: []string{"positional"}},
		},
	},
	{
		Min: 1200, Max: 1599,
		Tips: []Tip{
			{Title: "Calculate forcing moves first", Detail: "Checks, captures, and threats limit your opponent's choices. Start calculations with these to prune the tree.", Impact: 5, Tags: []string{"calculation"}},
			{Title: "Don't stop after one good move", Detail: "Finding a good move is not enough. Look for a better one. Compare at least two candidate moves.", Impact: 4, Tags: []string{"calculation"}},
			{Title: "Compare candidate moves", Detail: "List 2-3 reasonable moves and calculate each to the same depth before deciding. This catches many one-move traps.", Impact: 4, Tags: []string{"calculation"}},
			{Title: "Respect pawn structure", Detail: "Pawn moves are permanent. Doubled, isolated, or backward pawns are long-term weaknesses that opponents will exploit.", Impact: 4, Tags: []string{"positional"}},
			{Title: "Learn typical plans, not just openings", Detail: "Know the standard middlegame ideas for your openings instead of memorizing deep theory without purpose.", Impact: 3, Tags: []string{"opening", "strategy"}},
			{Title: "Coordinate pieces before attacking", Detail: "Attack with all your pieces working together. Uncoordinated pieces lead to blunders on the attack.", Impact: 4, Tags: []string{"strategy"}},
			{Title: "Use your king in the endgame", Detail: "Improve king activity before grabbing pawns. An active king is often worth a pawn.", Impact: 3, Tags: []string{"endgame"}},
			{Title: "Exchange bad pieces for good ones", Detail: "Trade your passive piece for your opponent's active one, even at slight material cost.", Impact: 3, Tags: []string{"strategy"}},
		},
	},
	{
		Min: 1600, Max: 1999,
		Tips: []Tip{
			{Title: "Identify imbalances before choosing a plan", Detail: "Material, pawn structure, piece activity, king safety, and space decide your plan. Name the imbalance first.", Impact: 5, Tags: []string{"strategy"}},
			{Title: "Weak squares matter more than material", Detail: "A permanent hole in the opponent's camp can outweigh a pawn. Occupy it before they can defend it.", Impact: 4, Tags: []string{"strategy"}},
			{Title: "Don't rush pawn breaks", Detail: "A pawn break changes the structure forever. Only advance when it improves your pieces or damages theirs.", Impact: 3, Tags: []string{"strategy"}},
			{Title: "Restrict opponent counterplay first", Detail: "Before starting your own plan, check for the opponent's freeing moves and take them away.", Impact: 4, Tags: []string{"strategy"}},
			{Title: "Convert advantages patiently", Detail: "When ahead, trade pieces, simplify, and avoid risk. There is no need to win quickly; win safely.", Impact: 4, Tags: []string{"strategy"}},
			{Title: "Use prophylaxis", Detail: "Ask 'what does my opponent want?' on every move and prevent it before executing your own plan.", Impact: 4, Tags: []string{"strategy"}},
			{Title: "Study model games", Detail: "Play through games of strong players in your openings to absorb how they convert plans into wins.", Impact: 3, Tags: []string{"strategy"}},
			{Title: "Let calculation support strategy", Detail: "Strategy gives you the right plan; calculation executes it. Don't calculate aimlessly without a plan.", Impact: 3, Tags: []string{"calculation"}},
		},
	},
	{
		Min: 2000, Max: 2400,
		Tips: []Tip{
			{Title: "Improve evaluation accuracy", Detail: "Prefer a dynamic, complex position over an equal material count. Learn to judge activity and initiative precisely.", Impact: 5, Tags: []string{"calculation"}},
			{Title: "Understand dynamic vs static advantages", Detail: "Static advantages (structure, bishop pair) need conversion; dynamic ones (initiative) must be used now or they evaporate.", Impact: 4, Tags: []string{"strategy"}},
			{Title: "Know when initiative outweighs material", Detail: "A lasting initiative can be worth more than a pawn. Learn when to give material to keep the initiative rolling.", Impact: 4, Tags: []string{"strategy"}},
			{Title: "Convert technical endings flawlessly", Detail: "Master the small endgames: king activity, opposition, and precise pawn races decide many master games.", Impact: 4, Tags: []string{"endgame"}},
			{Title: "Optimize time management", Detail: "Save time on the opening to spend on critical middlegame positions. Use your opponent's time to plan.", Impact: 3, Tags: []string{"psychology"}},
			{Title: "Build opening repertoires around middlegames", Detail: "Choose openings whose middlegame plans you understand and enjoy, not just lines with good statistics.", Impact: 3, Tags: []string{"opening"}},
			{Title: "Eliminate calculation bias", Detail: "Do not over-favor the first move you like. Verify with the opponent's best defense and your own worst-case replies.", Impact: 3, Tags: []string{"calculation"}},
		},
	},
}

var universalTips = []Tip{
	{Title: "What changed after my opponent's last move?", Detail: "Check the new threats the last move created before deciding anything. This is the foundation of blunder prevention.", Impact: 5, Tags: []string{"blunder"}},
	{Title: "Candidate moves first", Detail: "Write (in your head) 2-3 candidate moves before calculating any of them deeply.", Impact: 4, Tags: []string{"calculation"}},
	{Title: "Checks → Captures → Threats", Detail: "Look at forcing moves in this order. It is the quickest way to spot tactics and avoid falling for them.", Impact: 5, Tags: []string{"tactics", "blunder"}},
	{Title: "LPDO: Loose Pieces Drop Off", Detail: "Every move, find the loose pieces on both sides. Loose pieces are the source of most tactical blows.", Impact: 5, Tags: []string{"blunder", "tactics"}},
	{Title: "Worst piece first", Detail: "Improve your worst-placed piece on every move. This simple rule lifts the quality of your whole position.", Impact: 4, Tags: []string{"positional"}},
	{Title: "Don't attack with fewer pieces", Detail: "Count your attackers and the defenders before launching anything. Under-supported attacks collapse.", Impact: 3, Tags: []string{"strategy"}},
	{Title: "Don't calculate everything", Detail: "Calculate only what matters. Long variations should be justified by forcing moves and clear goals.", Impact: 3, Tags: []string{"psychology"}},
	{Title: "Trade according to your advantage", Detail: "When ahead, trade pieces and aim for the endgame. When behind, keep pieces on the board and create chaos.", Impact: 4, Tags: []string{"strategy"}},
	{Title: "King safety before attack", Detail: "Never start a kingside attack while your own king is still in the center or vulnerable.", Impact: 3, Tags: []string{"strategy"}},
	{Title: "Every pawn move creates weaknesses", Detail: "Pawns cannot move backward. Before pushing one, consider which squares it permanently abandons.", Impact: 3, Tags: []string{"positional"}},
}

func selectBand(elo int) *eloBand {
	if elo <= 0 {
		return &eloBands[1]
	}
	for i := range eloBands {
		if elo >= eloBands[i].Min && elo <= eloBands[i].Max {
			return &eloBands[i]
		}
	}
	return &eloBands[len(eloBands)-1]
}

func BuildSuggestions(profile StudentProfile) []Suggestion {
	band := selectBand(profile.Elo)

	weaknesses := map[string]int{}
	if profile.Blunders >= 2 {
		weaknesses["blunder"] += 2
	}
	if profile.Blunders >= 4 {
		weaknesses["blunder"]++
	}
	if profile.Mistakes >= 2 {
		weaknesses["calculation"]++
		weaknesses["strategy"]++
	}
	if profile.Inaccuracies >= 3 {
		weaknesses["positional"] += 2
	}
	if profile.BookMoves < 6 {
		weaknesses["opening"] += 2
	}
	if profile.Accuracy < 70 && profile.Blunders >= 1 {
		weaknesses["tactics"]++
	}

	scoreTip := func(t Tip) int {
		s := t.Impact * 2
		for _, tag := range t.Tags {
			if w, ok := weaknesses[tag]; ok {
				s += w * 3
			}
		}
		return s
	}

	scored := make([]Tip, len(band.Tips))
	copy(scored, band.Tips)
	sort.SliceStable(scored, func(i, j int) bool {
		return scoreTip(scored[i]) > scoreTip(scored[j])
	})

	var suggestions []Suggestion
	used := map[string]bool{}

	add := func(t Tip) {
		if len(suggestions) >= 5 {
			return
		}
		if used[t.Title] {
			return
		}
		used[t.Title] = true
		suggestions = append(suggestions, Suggestion{Title: t.Title, Detail: t.Detail})
	}

	for _, t := range scored {
		add(t)
	}

	if len(suggestions) < 5 {
		sortedUniversal := make([]Tip, len(universalTips))
		copy(sortedUniversal, universalTips)
		sort.SliceStable(sortedUniversal, func(i, j int) bool {
			return scoreTip(sortedUniversal[i]) > scoreTip(sortedUniversal[j])
		})
		for _, t := range sortedUniversal {
			if len(suggestions) >= 5 {
				break
			}
			add(t)
		}
	}

	return suggestions
}

func EloLabel(profile StudentProfile) string {
	if profile.Elo <= 0 {
		return "unknown rating"
	}
	return fmt.Sprintf("~%d", profile.Elo)
}
