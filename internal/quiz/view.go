package quiz

import (
	"encoding/json"
	"slices"
)

// Viewer describes who is looking (mirrors the Estate server's Viewer).
// Role is "player", "spectator" or "table" (the shared big screen).
type Viewer struct {
	Role        string
	Name        string
	SpectatorID string
	IsHost      bool
	Spectators  []Spectator
}

// Spectator is one entry of View.Spectators.
type Spectator struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IsHost bool   `json:"isHost"`
}

// View is the state one viewer gets (docs/QUIZ-API.md §3). It is built from
// scratch for each viewer and holds only what that viewer may see: never
// the right answer before the reveal, unfound gold squares, clues not yet
// picked, another player's choice or another player's final bet. A viewer's
// own choice and final bet are theirs to see (in You).
type View struct {
	Game        string   `json:"game"` // always "quiz"
	Code        string   `json:"code"`
	Status      string   `json:"status"` // lobby | playing | finished
	HostID      string   `json:"hostId"`
	Version     int64    `json:"version"`
	Phase       string   `json:"phase"`
	Now         int64    `json:"now"`         // server clock (Unix ms)
	PhaseEndsAt int64    `json:"phaseEndsAt"` // when the phase ends by timer; 0 when none
	Settings    Settings `json:"settings"`
	SeatLimit   int      `json:"seatLimit"`
	// Round is 0 in the lobby, the board round, or 3 in the final; Rounds is
	// how many boards this game plays.
	Round     int          `json:"round"`
	Rounds    int          `json:"rounds"`
	Board     *BoardView   `json:"board"`
	ChooserID string       `json:"chooserId"`
	Clue      *ClueView    `json:"clue"`
	Bet       *BetView     `json:"bet"`
	Final     *FinalView   `json:"final"`
	Players   []PlayerView `json:"players"`
	You       *YouView     `json:"you"`
	// Standings ranks everyone once the game is over (null before).
	Standings    []StandingView `json:"standings"`
	WinnerIDs    []string       `json:"winnerIds"`
	Credits      []string       `json:"credits"` // attribution lines of the packs this game deals from
	Log          []LogEntry     `json:"log"`
	LastEventSeq int64          `json:"lastEventSeq"`

	ViewerID     string      `json:"viewerId"`
	ViewerRole   string      `json:"viewerRole"`
	ViewerName   string      `json:"viewerName"`
	SpectatorID  string      `json:"spectatorId"`
	ViewerIsHost bool        `json:"viewerIsHost"`
	Spectators   []Spectator `json:"spectators"`
}

// BoardView is the current board without its clues.
type BoardView struct {
	Round      int            `json:"round"`
	Categories []CategoryView `json:"categories"`
}

// CategoryView is one column of BoardView.
type CategoryView struct {
	Index int        `json:"index"`
	Title string     `json:"title"`
	Topic string     `json:"topic"`
	Cells []CellView `json:"cells"`
}

// CellView is one cell: Gold only once a gold square has been picked.
type CellView struct {
	Row    int  `json:"row"`
	Value  int  `json:"value"`
	Played bool `json:"played"`
	Gold   bool `json:"gold,omitempty"`
}

// ClueView is the clue being played. The fields fill in phase by phase: the
// clue and its choices from the reading (sent early so every phone can show
// the choices at OpenAt exactly), who has answered once the choices open,
// the right answer and the results only in the reveal.
type ClueView struct {
	Seq           int          `json:"seq"`
	Category      string       `json:"category"`
	CategoryIndex int          `json:"categoryIndex"`
	Row           int          `json:"row"`
	Value         int          `json:"value"`
	Gold          bool         `json:"gold"`
	Text          string       `json:"text,omitzero"`
	ReadAt        int64        `json:"readAt,omitzero"`
	OpenAt        int64        `json:"openAt,omitzero"`
	ClosesAt      int64        `json:"closesAt,omitzero"`
	Choices       []string     `json:"choices,omitzero"`
	Eligible      []string     `json:"eligible,omitzero"`
	Answered      []string     `json:"answered,omitzero"`
	AnswerIndex   *int         `json:"answerIndex,omitempty"`
	Results       []ResultView `json:"results,omitzero"`
	WinnerID      *string      `json:"winnerId,omitempty"`
}

// ResultView is one tap in the reveal, in time order. Ms is the time it
// counted as (§6.3); Late: after the winner, so it changed nothing.
type ResultView struct {
	PlayerID string `json:"playerId"`
	Choice   int    `json:"choice"`
	Correct  bool   `json:"correct"`
	Delta    int    `json:"delta"`
	Ms       int    `json:"ms"`
	Late     bool   `json:"late"`
}

// BetView is a gold square's bet: Amount is 0 until placed, then public.
type BetView struct {
	PlayerID string `json:"playerId"`
	Min      int    `json:"min"`
	Max      int    `json:"max"`
	Amount   int    `json:"amount"`
}

// FinalView is the final round, filling in phase by phase like ClueView.
type FinalView struct {
	Seq         int               `json:"seq"`
	Category    string            `json:"category"`
	BetsIn      []string          `json:"betsIn,omitzero"`
	Text        string            `json:"text,omitzero"`
	ReadAt      int64             `json:"readAt,omitzero"`
	OpenAt      int64             `json:"openAt,omitzero"`
	ClosesAt    int64             `json:"closesAt,omitzero"`
	Choices     []string          `json:"choices,omitzero"`
	AnswersIn   []string          `json:"answersIn,omitzero"`
	AnswerIndex *int              `json:"answerIndex,omitempty"`
	Order       []string          `json:"order,omitzero"`
	Revealed    *int              `json:"revealed,omitempty"`
	Results     []FinalResultView `json:"results,omitzero"`
}

// FinalResultView is one revealed final answer (Choice -1: no answer).
type FinalResultView struct {
	PlayerID string `json:"playerId"`
	Bet      int    `json:"bet"`
	Choice   int    `json:"choice"`
	Correct  bool   `json:"correct"`
	Before   int    `json:"before"`
	After    int    `json:"after"`
}

// PlayerView is one seat.
type PlayerView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Status    string `json:"status"` // waiting (lobby) | playing | left
	Score     int    `json:"score"`
	Correct   int    `json:"correct"`
	Wrong     int    `json:"wrong"`
	Place     int    `json:"place,omitempty"` // when finished
	IsChooser bool   `json:"isChooser"`
	// LockedIn: has tapped on the current clue, or bet or answered in the
	// current final step (whether right, and how much, stay secret).
	LockedIn bool `json:"lockedIn"`
}

// YouView is a seated player's own panel.
type YouView struct {
	ID      string `json:"id"`
	CanPick bool   `json:"canPick"`
	// CanAnswer is true from the reading on while the player may still
	// answer; the phone still shows the choices only at OpenAt.
	CanAnswer   bool `json:"canAnswer"`
	Choice      int  `json:"choice"` // own tap on the current clue, -1 none
	CanBet      bool `json:"canBet"`
	BetMin      int  `json:"betMin"`
	BetMax      int  `json:"betMax"`
	Bet         *int `json:"bet"`         // own gold or final bet once placed
	FinalChoice int  `json:"finalChoice"` // own final answer, -1 none
	LatencyMs   int  `json:"latencyMs"`   // the round trip the server believes this phone has
}

// StandingView is one line of the final standings.
type StandingView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Place   int    `json:"place"`
	Score   int    `json:"score"`
	Correct int    `json:"correct"`
	Wrong   int    `json:"wrong"`
	Left    bool   `json:"left,omitempty"`
}

// PublicState is View as JSON.
func (g *Game) PublicState(viewerID string, v Viewer) ([]byte, error) {
	return json.Marshal(g.View(viewerID, v))
}

// View builds the state viewerID (a player id, or "" for the table and
// spectators) may see.
func (g *Game) View(viewerID string, v Viewer) View {
	spectators := v.Spectators
	if spectators == nil {
		spectators = []Spectator{}
	}
	rounds := g.Settings.Rounds()
	if g.Status != StatusLobby {
		rounds = len(g.Boards)
	}
	view := View{
		Game: "quiz", Code: g.Code, Status: g.Status, HostID: g.HostID, Version: g.Version, Phase: g.Phase,
		Now: g.Now(), PhaseEndsAt: g.PhaseEndsAt, Settings: g.Settings, SeatLimit: g.SeatLimit(),
		Round: g.Round, Rounds: rounds, Board: g.boardView(), ChooserID: g.ChooserID,
		Clue: g.clueView(), Bet: g.betView(), Final: g.finalView(),
		Players: make([]PlayerView, 0, len(g.Players)), WinnerIDs: slices.Clone(g.WinnerIDs),
		Credits: slices.Clone(g.Credits), Log: slices.Clone(g.Log), LastEventSeq: g.EventSeq,
		ViewerID: viewerID, ViewerRole: v.Role, ViewerName: v.Name, SpectatorID: v.SpectatorID,
		ViewerIsHost: v.IsHost, Spectators: spectators,
	}
	for _, p := range g.Players {
		pv := PlayerView{ID: p.ID, Name: p.Name, Kind: p.Kind, Status: g.playerStatus(p), Score: p.Score,
			Correct: p.Correct, Wrong: p.Wrong, IsChooser: g.Status == StatusPlaying && p.ID == g.ChooserID,
			LockedIn: g.lockedIn(p)}
		if g.Status == StatusFinished {
			pv.Place = p.Place
		}
		view.Players = append(view.Players, pv)
		if p.ID == viewerID {
			view.You = g.youView(p)
		}
	}
	if g.Status == StatusFinished {
		view.Standings = g.standings()
	}
	return view
}

func (g *Game) playerStatus(p *Player) string {
	switch {
	case g.Status == StatusLobby:
		return "waiting"
	case p.Left:
		return "left"
	}
	return "playing"
}

// lockedIn: tapped on the current clue, or bet or answered in the current
// final step.
func (g *Game) lockedIn(p *Player) bool {
	switch g.Phase {
	case PhaseAnswer, PhaseReveal:
		return g.Clue != nil && g.Clue.Q != nil && g.Clue.Q.tapOf(p.ID) != nil
	case PhaseFinalBet:
		_, ok := g.Final.betOf(p.ID)
		return ok
	case PhaseFinalAnswer:
		return g.Final.Q.tapOf(p.ID) != nil
	}
	return false
}

func (g *Game) boardView() *BoardView {
	b := g.board()
	if b == nil || g.Status != StatusPlaying {
		return nil
	}
	bv := &BoardView{Round: b.Round, Categories: make([]CategoryView, 0, len(b.Categories))}
	for i, bc := range b.Categories {
		cv := CategoryView{Index: i, Title: bc.Title, Topic: bc.Topic, Cells: make([]CellView, 0, len(bc.Cells))}
		for _, cell := range bc.Cells {
			cv.Cells = append(cv.Cells, CellView{Row: cell.Row, Value: cell.Value, Played: cell.Played, Gold: cell.Played && cell.Gold})
		}
		bv.Categories = append(bv.Categories, cv)
	}
	return bv
}

func (g *Game) clueView() *ClueView {
	c := g.Clue
	if c == nil || g.Status != StatusPlaying {
		return nil
	}
	cv := &ClueView{Seq: c.Seq, Category: g.board().Categories[c.Category].Title, CategoryIndex: c.Category,
		Row: c.Row, Value: c.Value, Gold: c.Gold}
	q := c.Q
	if q == nil {
		return cv
	}
	cv.Text, cv.ReadAt, cv.OpenAt, cv.ClosesAt = q.Text, q.ReadAt, q.OpenAt, q.ClosesAt
	cv.Choices, cv.Eligible = slices.Clone(q.Choices), slices.Clone(c.Eligible)
	if g.Phase != PhaseRead {
		cv.Answered = tappers(q)
	}
	if c.Decided {
		answer, winner := q.Answer, c.WinnerID
		cv.AnswerIndex, cv.WinnerID = &answer, &winner
		cv.Results = make([]ResultView, 0, len(q.Taps))
		for _, t := range q.Taps {
			cv.Results = append(cv.Results, ResultView{PlayerID: t.PlayerID, Choice: t.Choice, Correct: t.Correct,
				Delta: t.Delta, Ms: t.Effective, Late: t.Late})
		}
	}
	return cv
}

// tappers lists who has answered (not what).
func tappers(q *Question) []string {
	ids := make([]string, 0, len(q.Taps))
	for _, t := range q.Taps {
		ids = append(ids, t.PlayerID)
	}
	return ids
}

func (g *Game) betView() *BetView {
	b := g.Bet
	if b == nil || g.Status != StatusPlaying {
		return nil
	}
	bv := &BetView{PlayerID: b.PlayerID, Min: b.Min, Max: b.Max}
	if b.Placed {
		bv.Amount = b.Amount
	}
	return bv
}

func (g *Game) finalView() *FinalView {
	f := g.Final
	if f == nil {
		return nil
	}
	fv := &FinalView{Seq: f.Seq, Category: f.Category}
	if g.Phase != PhaseFinalIntro {
		fv.BetsIn = make([]string, 0, len(f.Bets))
		for _, b := range f.Bets {
			fv.BetsIn = append(fv.BetsIn, b.PlayerID)
		}
	}
	if q := f.Q; q != nil {
		fv.Text, fv.ReadAt, fv.OpenAt, fv.ClosesAt = q.Text, q.ReadAt, q.OpenAt, q.ClosesAt
		fv.Choices = slices.Clone(q.Choices)
		if g.Phase != PhaseFinalRead {
			fv.AnswersIn = tappers(q)
		}
	}
	if f.Order != nil {
		answer, revealed := f.Q.Answer, f.Revealed
		fv.AnswerIndex, fv.Revealed, fv.Order = &answer, &revealed, slices.Clone(f.Order)
		fv.Results = make([]FinalResultView, 0, len(f.Results))
		for _, r := range f.Results {
			fv.Results = append(fv.Results, FinalResultView(r))
		}
	}
	return fv
}

// youView is a player's own panel. Their own choice and bets stay theirs to
// see after they leave and after the game; what they may do only while they
// play.
func (g *Game) youView(p *Player) *YouView {
	you := &YouView{ID: p.ID, Choice: -1, FinalChoice: -1, LatencyMs: p.latency()}
	active := !p.Left && g.Status == StatusPlaying
	you.CanPick = active && g.Phase == PhasePick && g.ChooserID == p.ID
	if c := g.Clue; c != nil && c.Q != nil {
		t := c.Q.tapOf(p.ID)
		if t != nil {
			you.Choice = t.Choice
		}
		open := g.Phase == PhaseRead || g.Phase == PhaseAnswer
		you.CanAnswer = active && open && t == nil && slices.Contains(c.Eligible, p.ID)
	}
	if b := g.Bet; b != nil && b.PlayerID == p.ID {
		if b.Placed {
			amount := b.Amount
			you.Bet = &amount
		} else if active && g.Phase == PhaseBet {
			you.CanBet, you.BetMin, you.BetMax = true, b.Min, b.Max
		}
	}
	if f := g.Final; f != nil {
		if amount, ok := f.betOf(p.ID); ok {
			you.Bet = &amount
		} else if active && g.Phase == PhaseFinalBet {
			you.CanBet, you.BetMin, you.BetMax = true, 0, finalMaxBet(p)
		}
		if f.Q != nil {
			t := f.Q.tapOf(p.ID)
			if t != nil {
				you.FinalChoice = t.Choice
			}
			you.CanAnswer = active && t == nil && (g.Phase == PhaseFinalRead || g.Phase == PhaseFinalAnswer)
		}
	}
	return you
}

// standings lists everyone by place (ties share a place), then seat.
func (g *Game) standings() []StandingView {
	out := make([]StandingView, 0, len(g.Players))
	for _, p := range g.Players {
		out = append(out, StandingView{ID: p.ID, Name: p.Name, Place: p.Place, Score: p.Score, Correct: p.Correct,
			Wrong: p.Wrong, Left: p.Left})
	}
	slices.SortStableFunc(out, func(a, b StandingView) int { return a.Place - b.Place })
	return out
}
