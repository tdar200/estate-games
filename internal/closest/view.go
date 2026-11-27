package closest

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

// View is the state one viewer gets (docs/CLOSEST-API.md §3). It is built
// from scratch for each viewer and holds only what that viewer may see:
// never another player's guess before the line-up, another player's chips
// or stake before the chips are shown, the answer, fact or sources before
// the reveal, or a question not yet asked. A viewer's own guess and bet are
// theirs to see (in You).
type View struct {
	Game          string        `json:"game"` // always "closest"
	Title         string        `json:"title"`
	Code          string        `json:"code"`
	Status        string        `json:"status"` // lobby | playing | finished
	HostID        string        `json:"hostId"`
	Version       int64         `json:"version"`
	Phase         string        `json:"phase"`
	Now           int64         `json:"now"`         // server clock (Unix ms)
	PhaseEndsAt   int64         `json:"phaseEndsAt"` // when the phase ends by timer; 0 when none
	Settings      Settings      `json:"settings"`
	SeatLimit     int           `json:"seatLimit"`
	QuestionNo    int           `json:"questionNo"`
	QuestionCount int           `json:"questionCount"`
	Question      *QuestionView `json:"question"`
	Players       []PlayerView  `json:"players"`
	You           *YouView      `json:"you"`
	// Standings ranks everyone once the game is over (null before).
	Standings    []StandingView `json:"standings"`
	WinnerIDs    []string       `json:"winnerIds"`
	Credits      []string       `json:"credits"` // attribution lines of the packs this game asks from
	Log          []LogEntry     `json:"log"`
	LastEventSeq int64          `json:"lastEventSeq"`

	ViewerID     string      `json:"viewerId"`
	ViewerRole   string      `json:"viewerRole"`
	ViewerName   string      `json:"viewerName"`
	SpectatorID  string      `json:"spectatorId"`
	ViewerIsHost bool        `json:"viewerIsHost"`
	Spectators   []Spectator `json:"spectators"`
}

// QuestionView is the question being played (§3.2). The fields fill in
// phase by phase: the question itself from the guessing, who has guessed
// while guesses are open, the slots from the line-up, who has bet while
// bets are open, the chips once shown, and the answer and results only in
// the reveal.
type QuestionView struct {
	Seq     int          `json:"seq"`
	No      int          `json:"no"`
	Double  bool         `json:"double"`
	Text    string       `json:"text,omitzero"`
	Unit    *string      `json:"unit,omitempty"`
	Format  string       `json:"format,omitzero"`
	Min     *int         `json:"min,omitempty"`
	Max     *int         `json:"max,omitempty"`
	Topic   string       `json:"topic,omitzero"`
	Guessed []string     `json:"guessed,omitzero"`
	Slots   []SlotView   `json:"slots,omitzero"`
	BetsIn  []string     `json:"betsIn,omitzero"`
	Answer  *int         `json:"answer,omitempty"`
	Fact    *string      `json:"fact,omitempty"`
	Src     []string     `json:"src,omitzero"`
	Results []ResultView `json:"results,omitzero"`
	Nobody  *bool        `json:"nobody,omitempty"`
}

// SlotView is one slot on the line (§3.3).
type SlotView struct {
	Index    int        `json:"index"`
	Value    int        `json:"value"`
	OwnerIDs []string   `json:"ownerIds"`
	Odds     int        `json:"odds"`
	Chips    []ChipView `json:"chips,omitzero"`   // from the chips phase
	Off      *int       `json:"off,omitempty"`    // |value − answer|, in the reveal
	Result   *string    `json:"result,omitempty"` // "win", "second" or "", in the reveal
}

// ChipView is one player's chips on a slot: Count 1 or 2; Stake is their
// stake when it went on this slot.
type ChipView struct {
	PlayerID string `json:"playerId"`
	Count    int    `json:"count"`
	Stake    int    `json:"stake"`
}

// ResultView is one player's line in the reveal (§3.2).
type ResultView = Result

// PlayerView is one seat (§3.4).
type PlayerView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Status  string `json:"status"` // waiting (lobby) | playing | left
	Score   int    `json:"score"`
	Closest int    `json:"closest"`
	Place   int    `json:"place,omitempty"` // when finished
	// Guessed / BetIn: done in the current phase (no values).
	Guessed bool `json:"guessed"`
	BetIn   bool `json:"betIn"`
}

// YouView is a seated player's own panel (§3.5).
type YouView struct {
	ID          string  `json:"id"`
	CanGuess    bool    `json:"canGuess"`
	Guess       *int    `json:"guess"`
	ChangesLeft int     `json:"changesLeft"`
	CanBet      bool    `json:"canBet"`
	Chips       *[2]int `json:"chips"`
	Stake       int     `json:"stake"`
	StakeSlot   *int    `json:"stakeSlot"`
	StakeMax    int     `json:"stakeMax"`
}

// StandingView is one line of the final standings.
type StandingView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Place   int    `json:"place"`
	Score   int    `json:"score"`
	Closest int    `json:"closest"`
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
	count := g.Settings.QuestionCount()
	if g.Status != StatusLobby {
		count = len(g.Questions)
	}
	view := View{
		Game: "closest", Title: Title, Code: g.Code, Status: g.Status, HostID: g.HostID, Version: g.Version,
		Phase: g.Phase, Now: g.Now(), PhaseEndsAt: g.PhaseEndsAt, Settings: g.Settings, SeatLimit: g.SeatLimit(),
		QuestionNo: g.QuestionNo, QuestionCount: count, Question: g.questionView(),
		Players: make([]PlayerView, 0, len(g.Players)), WinnerIDs: slices.Clone(g.WinnerIDs),
		Credits: slices.Clone(g.Credits), Log: slices.Clone(g.Log), LastEventSeq: g.EventSeq,
		ViewerID: viewerID, ViewerRole: v.Role, ViewerName: v.Name, SpectatorID: v.SpectatorID,
		ViewerIsHost: v.IsHost, Spectators: spectators,
	}
	for _, p := range g.Players {
		pv := PlayerView{ID: p.ID, Name: p.Name, Kind: p.Kind, Status: g.playerStatus(p), Score: p.Score, Closest: p.Closest}
		if g.Status == StatusFinished {
			pv.Place = p.Place
		}
		if cur := g.shownQuestion(); cur != nil && g.Status == StatusPlaying {
			pv.Guessed = g.Phase == PhaseGuess && cur.guessOf(p.ID) != nil
			pv.BetIn = g.Phase == PhaseBet && cur.betOf(p.ID) != nil
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

// shownQuestion is the question the views show: the one being played, and
// after the game the last one if it was revealed (a game ended early
// mid-question shows none: its answer was never revealed).
func (g *Game) shownQuestion() *Current {
	cur := g.Cur
	switch {
	case cur == nil:
		return nil
	case g.Status == StatusPlaying:
		return cur
	case g.Status == StatusFinished && cur.Revealed:
		return cur
	}
	return nil
}

func (g *Game) questionView() *QuestionView {
	cur := g.shownQuestion()
	if cur == nil {
		return nil
	}
	q := cur.Question
	unit, lo, hi := q.Unit, q.Min, q.Max
	qv := &QuestionView{Seq: cur.Seq, No: cur.No, Double: cur.Double, Text: q.Text, Unit: &unit, Format: q.Format,
		Min: &lo, Max: &hi, Topic: q.Topic}
	if g.Phase == PhaseGuess {
		qv.Guessed = make([]string, 0, len(cur.Guesses))
		for _, gs := range cur.Guesses {
			qv.Guessed = append(qv.Guessed, gs.PlayerID)
		}
	}
	if g.Phase == PhaseBet {
		qv.BetsIn = make([]string, 0, len(cur.Bets))
		for _, b := range cur.Bets {
			qv.BetsIn = append(qv.BetsIn, b.PlayerID)
		}
	}
	chipsShown := g.Phase == PhaseChips || cur.Revealed
	if len(cur.Slots) > 0 {
		qv.Slots = make([]SlotView, 0, len(cur.Slots))
		for i, s := range cur.Slots {
			sv := SlotView{Index: i, Value: s.Value, OwnerIDs: slices.Clone(s.OwnerIDs), Odds: s.Odds}
			if chipsShown {
				sv.Chips = chipsOn(cur.Bets, i)
			}
			if cur.Revealed {
				off, result := distance(s.Value, q.Answer), ""
				if slices.Contains(cur.Winners, i) {
					result = resultWin
				} else if slices.Contains(cur.Second, i) {
					result = resultSecond
				}
				sv.Off, sv.Result = &off, &result
			}
			qv.Slots = append(qv.Slots, sv)
		}
	}
	if cur.Revealed {
		answer, fact, nobody := q.Answer, q.Fact, cur.Nobody
		qv.Answer, qv.Fact, qv.Src, qv.Nobody = &answer, &fact, slices.Clone(q.Src), &nobody
		qv.Results = slices.Clone(cur.Results)
	}
	return qv
}

// chipsOn lists each player's chips on slot i, in betting order.
func chipsOn(bets []Bet, i int) []ChipView {
	out := []ChipView{}
	for _, b := range bets {
		count := 0
		for _, c := range b.Chips {
			if c == i {
				count++
			}
		}
		if count == 0 {
			continue
		}
		cv := ChipView{PlayerID: b.PlayerID, Count: count}
		if b.StakeSlot != nil && *b.StakeSlot == i {
			cv.Stake = b.Stake
		}
		out = append(out, cv)
	}
	return out
}

// youView is a player's own panel. Their own guess and bet stay theirs to
// see after they leave and after the game; what they may do only while they
// play.
func (g *Game) youView(p *Player) *YouView {
	you := &YouView{ID: p.ID, ChangesLeft: 1, StakeMax: stakeMax(p)}
	active := !p.Left && g.Status == StatusPlaying
	cur := g.shownQuestion()
	if cur == nil {
		return you
	}
	playing := active && slices.Contains(cur.Playing, p.ID)
	if gs := cur.guessOf(p.ID); gs != nil {
		value := gs.Value
		you.Guess = &value
		if gs.Changed {
			you.ChangesLeft = 0
		}
	}
	you.CanGuess = playing && g.Phase == PhaseGuess && you.ChangesLeft > 0
	if b := cur.betOf(p.ID); b != nil {
		chips := b.Chips
		you.Chips, you.Stake = &chips, b.Stake
		if b.StakeSlot != nil {
			slot := *b.StakeSlot
			you.StakeSlot = &slot
		}
	} else {
		you.CanBet = playing && g.Phase == PhaseBet
	}
	return you
}

// standings lists everyone by place (ties share a place), then seat.
func (g *Game) standings() []StandingView {
	out := make([]StandingView, 0, len(g.Players))
	for _, p := range g.Players {
		out = append(out, StandingView{ID: p.ID, Name: p.Name, Place: p.Place, Score: p.Score, Closest: p.Closest, Left: p.Left})
	}
	slices.SortStableFunc(out, func(a, b StandingView) int { return a.Place - b.Place })
	return out
}
