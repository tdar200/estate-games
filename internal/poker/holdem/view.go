package holdem

import (
	"encoding/json"
	"math"
	"sort"
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

// View is the state one viewer may see. Hole cards are hidden except the
// viewer's own and those turned face up (all-in run-out, showdown, or a
// voluntary show). PublicState is View as JSON; bots receive the same View,
// so they cannot see more than a player could.
type View struct {
	Game           string         `json:"game"` // always "poker"
	Code           string         `json:"code"`
	Status         string         `json:"status"` // lobby | playing | finished
	Phase          string         `json:"phase"`  // lobby | betting | runout | showdown | handOver | paused | finished
	HostID         string         `json:"hostId"`
	Version        int64          `json:"version"`
	Now            int64          `json:"now"` // server clock (Unix ms) for countdowns
	Settings       Settings       `json:"settings"`
	SeatLimit      int            `json:"seatLimit"` // most seats settings.maxPlayers may be set to (8)
	Level          LevelView      `json:"level"`
	Schedule       []Level        `json:"schedule"` // every blind level (a preview from the settings while in the lobby)
	HandNumber     int            `json:"handNumber"`
	Street         string         `json:"street"` // preflop | flop | turn | river; "" outside a hand
	Board          []string       `json:"board"`
	Pots           []PotView      `json:"pots"`       // pots gathered so far (not the bets in front of players)
	TotalPot       int            `json:"totalPot"`   // every chip in the middle, bets included
	CurrentBet     int            `json:"currentBet"` // the bet to match this street
	ButtonSeat     int            `json:"buttonSeat"` // -1 before the first hand
	SmallBlindSeat int            `json:"smallBlindSeat"`
	BigBlindSeat   int            `json:"bigBlindSeat"`
	ActorID        string         `json:"actorId"`
	ActionDeadline int64          `json:"actionDeadline"` // end of the actor's turn timer (time bank included once it runs), 0 = none
	ActionTimeBank bool           `json:"actionTimeBank"` // the actor's turn timer ran out and their time bank is running
	NextHandAt     int64          `json:"nextHandAt"`     // when the next hand is dealt, 0 = n/a
	Players        []PlayerView   `json:"players"`
	Actions        []ActionView   `json:"actions"` // this hand, oldest first
	Result         *ResultView    `json:"result"`  // set once the hand is over
	You            *YouView       `json:"you"`     // set when the viewer is a seated player
	WinnerID       string         `json:"winnerId"`
	Standings      []StandingView `json:"standings"` // set when the game is finished
	Log            []LogEntry     `json:"log"`
	LastEventSeq   int64          `json:"lastEventSeq"`

	ViewerID     string      `json:"viewerId"`
	ViewerRole   string      `json:"viewerRole"`
	ViewerName   string      `json:"viewerName"`
	SpectatorID  string      `json:"spectatorId"`
	ViewerIsHost bool        `json:"viewerIsHost"`
	Spectators   []Spectator `json:"spectators"`
}

// LevelView is the blind level and what comes next.
type LevelView struct {
	Index      int    `json:"index"` // 0-based
	SmallBlind int    `json:"smallBlind"`
	BigBlind   int    `json:"bigBlind"`
	Ante       int    `json:"ante"`
	EndsAt     int64  `json:"endsAt"` // 0 = no further increase
	Next       *Level `json:"next"`
}

// PotView is a pot in the middle.
type PotView struct {
	Amount      int      `json:"amount"`
	EligibleIDs []string `json:"eligibleIds"`
}

// PlayerView is one seat.
type PlayerView struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Kind       string      `json:"kind"` // human | ai
	Seat       int         `json:"seat"`
	Stack      int         `json:"stack"`  // chips behind
	Bet        int         `json:"bet"`    // in front of the player this street
	Status     string      `json:"status"` // waiting | active | folded | allIn | sittingOut | busted | out | left
	InHand     bool        `json:"inHand"` // dealt into the current hand
	HasCards   bool        `json:"hasCards"`
	Cards      []string    `json:"cards"` // null unless visible to this viewer
	Shown      bool        `json:"shown"` // face up for everyone
	HandName   string      `json:"handName,omitempty"`
	IsButton   bool        `json:"isButton"`
	IsSB       bool        `json:"isSmallBlind"`
	IsBB       bool        `json:"isBigBlind"`
	LastAction *ActionView `json:"lastAction"`
	// Odds: win and tie chances during an all-in run-out (phase "runout"),
	// null otherwise.
	Odds       *Odds `json:"odds"`
	SitOutNext bool  `json:"sitOutNext,omitempty"`
	RebuyUntil int64 `json:"rebuyUntil,omitempty"`
	Rebuys     int   `json:"rebuys"`
	Place      int   `json:"place,omitempty"`
	// TimeBank is the seconds left in the player's time bank (while it runs
	// for the actor it still shows what they had when it started).
	TimeBank int `json:"timeBank"`
	BoughtIn int `json:"boughtIn"` // starting stack (cash buy-in) plus rebuys
}

// ActionView is one hand action.
type ActionView struct {
	PlayerID string `json:"playerId"`
	Street   string `json:"street"`
	Type     string `json:"type"` // ante | smallBlind | bigBlind | fold | check | call | bet | raise
	Amount   int    `json:"amount"`
	To       int    `json:"to"`
	AllIn    bool   `json:"allIn,omitempty"`
}

// ResultView describes a finished hand.
type ResultView struct {
	HandNumber int             `json:"handNumber"`
	Showdown   bool            `json:"showdown"`
	Board      []string        `json:"board"`
	Pots       []PotResultView `json:"pots"`
	Hands      []ShownHandView `json:"hands"` // players still in at showdown, in show order
	Uncalled   []UncalledView  `json:"uncalled"`
	Cancelled  bool            `json:"cancelled,omitempty"` // called off by "end game"
}

// PotResultView is a pot and who won it.
type PotResultView struct {
	Amount      int          `json:"amount"`
	EligibleIDs []string     `json:"eligibleIds"`
	Winners     []WinnerView `json:"winners"`
}

// WinnerView is a share of a pot.
type WinnerView struct {
	ID        string   `json:"id"`
	Amount    int      `json:"amount"`
	HandName  string   `json:"handName,omitempty"`  // only when the hand was shown
	BestCards []string `json:"bestCards,omitempty"` // the five cards, only when shown
}

// ShownHandView is a hand at showdown; Cards is null when mucked.
type ShownHandView struct {
	ID        string   `json:"id"`
	Cards     []string `json:"cards"`
	HandName  string   `json:"handName,omitempty"`
	BestCards []string `json:"bestCards,omitempty"`
	Mucked    bool     `json:"mucked"`
}

// UncalledView is a bet returned to its owner.
type UncalledView struct {
	ID     string `json:"id"`
	Amount int    `json:"amount"`
}

// YouView is the viewer's private panel.
type YouView struct {
	ID       string   `json:"id"`
	Seat     int      `json:"seat"`
	Cards    []string `json:"cards"`
	HandName string   `json:"handName"` // best hand now, or a preflop description
	// Strength is the chance (0–1, 2 decimals) that your hand is the best
	// one right now against the opponents still in, assuming random cards
	// for them; before the flop, your share of the pot all-in against
	// random hands. Null when you are not in a hand still being bet (not
	// dealt in, folded, the run-out, or the hand is over), and for computer
	// seats (no screen shows it). See strength.go.
	Strength   *float64   `json:"strength"`
	ToAct      bool       `json:"toAct"`
	Legal      *LegalView `json:"legal"` // set when ToAct
	CanShow    bool       `json:"canShow"`
	CanRebuy   bool       `json:"canRebuy"`
	RebuyUntil int64      `json:"rebuyUntil,omitempty"`
	SittingOut bool       `json:"sittingOut"`
	SitOutNext bool       `json:"sitOutNext"`
	IsHost     bool       `json:"isHost"`
}

// LegalView lists the viewer's options when it is their turn.
type LegalView struct {
	Fold  bool       `json:"fold"`
	Check bool       `json:"check"`
	Call  int        `json:"call"` // chips to call; 0 when checking (or calling is not possible)
	Raise *RaiseView `json:"raise"`
	AllIn int        `json:"allIn"` // the to-amount of going all-in (a call if raising is closed)
}

// RaiseView is the legal bet/raise range plus suggested sizes.
type RaiseView struct {
	IsBet   bool         `json:"isBet"` // opening bet rather than a raise
	Min     int          `json:"min"`   // raise-to amounts (total street bet)
	Max     int          `json:"max"`
	Presets []PresetView `json:"presets"`
}

// PresetView is a suggested bet size.
type PresetView struct {
	Label string `json:"label"` // "Min", "½ pot", "¾ pot", "Pot", "All-in"
	To    int    `json:"to"`
}

// StandingView is a final ranking row.
type StandingView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Place    int    `json:"place"`
	Stack    int    `json:"stack"`
	BoughtIn int    `json:"boughtIn"`
	Net      int    `json:"net"` // chips won: stack − boughtIn (what a cash game ranks by)
}

// PublicState returns the View as JSON.
func (t *Table) PublicState(viewerID string, v Viewer) ([]byte, error) {
	return json.Marshal(t.View(viewerID, v))
}

// View builds what viewerID may see ("" for the shared table screen or a
// spectator).
func (t *Table) View(viewerID string, v Viewer) *View {
	h := t.Hand
	lv := t.Level()
	out := &View{
		Game:           "poker",
		Code:           t.Code,
		Status:         t.Status,
		Phase:          t.Phase,
		HostID:         t.HostID,
		Version:        t.Version,
		Now:            t.Now(),
		Settings:       t.Settings,
		SeatLimit:      t.SeatLimit,
		Level:          LevelView{Index: t.LevelIndex, SmallBlind: lv.SmallBlind, BigBlind: lv.BigBlind, Ante: lv.Ante, EndsAt: t.LevelEndsAt},
		HandNumber:     t.HandNumber,
		Board:          []string{},
		Pots:           []PotView{},
		ButtonSeat:     t.ButtonSeat,
		SmallBlindSeat: -1,
		BigBlindSeat:   -1,
		NextHandAt:     0,
		Players:        []PlayerView{},
		Actions:        []ActionView{},
		WinnerID:       t.WinnerID,
		Log:            append([]LogEntry{}, t.Log...),
		LastEventSeq:   t.EventSeq,
		ViewerID:       viewerID,
		ViewerRole:     v.Role,
		ViewerName:     v.Name,
		SpectatorID:    v.SpectatorID,
		ViewerIsHost:   v.IsHost,
		Spectators:     v.Spectators,
	}
	if out.Spectators == nil {
		out.Spectators = []Spectator{}
	}
	out.Schedule = t.Levels
	if t.Status == StatusLobby {
		out.Schedule = BuildSchedule(t.Settings, t.seated())
		if len(out.Schedule) > 0 {
			out.Level.EndsAt = 0
			if len(out.Schedule) > 1 {
				next := out.Schedule[1]
				out.Level.Next = &next
			}
		}
	}
	if out.Schedule == nil {
		out.Schedule = []Level{}
	}
	if t.LevelIndex+1 < len(t.Levels) {
		next := t.Levels[t.LevelIndex+1]
		out.Level.Next = &next
	}
	if t.Phase == PhaseShowdown || t.Phase == PhaseHandOver || t.Phase == PhasePaused {
		out.NextHandAt = t.NextHandAt
	}
	inHand := h != nil && t.Status != StatusLobby
	if inHand {
		out.Board = cardStrings(h.Board)
		if !h.Done {
			out.Street = h.Street.String()
			out.CurrentBet = h.MaxBet()
			out.TotalPot = h.Pot()
			for _, p := range h.collectedPots() {
				out.Pots = append(out.Pots, PotView{Amount: p.Amount, EligibleIDs: t.ids(p.Eligible)})
			}
			if i := h.Actor(); i >= 0 && t.Phase == PhaseBetting {
				out.ActorID = t.HandIDs[i]
				out.ActionDeadline = t.Deadline
				out.ActionTimeBank = t.OnBank
			}
		}
		sb, bb := h.blindSeats()
		if p := t.Player(t.HandIDs[sb]); p != nil {
			out.SmallBlindSeat = p.Seat
		}
		if p := t.Player(t.HandIDs[bb]); p != nil {
			out.BigBlindSeat = p.Seat
		}
		for _, r := range h.Log {
			out.Actions = append(out.Actions, ActionView{PlayerID: t.HandIDs[r.Player], Street: r.Street.String(), Type: string(r.Type), Amount: r.Amount, To: r.To, AllIn: r.AllIn})
		}
		out.Result = t.resultView()
	}
	for _, p := range t.Players {
		out.Players = append(out.Players, t.playerView(p, viewerID, out))
	}
	sort.SliceStable(out.Players, func(a, b int) bool { return out.Players[a].Seat < out.Players[b].Seat })
	if p := t.Player(viewerID); p != nil {
		out.You = t.youView(p, out)
	}
	if t.Status == StatusFinished {
		out.Standings = t.standings()
	}
	return out
}

func (t *Table) ids(idx []int) []string {
	out := make([]string, 0, len(idx))
	for _, i := range idx {
		out = append(out, t.HandIDs[i])
	}
	return out
}

func (t *Table) visible(i int, viewerID string) bool {
	h := t.Hand
	return h.Shown[i] || (viewerID != "" && t.HandIDs[i] == viewerID)
}

func (t *Table) playerView(p *Player, viewerID string, v *View) PlayerView {
	pv := PlayerView{ID: p.ID, Name: p.Name, Kind: p.Kind, Seat: p.Seat, Stack: p.Stack, Rebuys: p.Rebuys, Place: p.Place, SitOutNext: p.SitOutNext,
		TimeBank: p.TimeBank, BoughtIn: p.BoughtIn}
	if p.Busted {
		pv.RebuyUntil = p.RebuyUntil
	}
	h := t.Hand
	i := t.handIndex(p.ID)
	if t.Status == StatusLobby {
		i = -1
	}
	if i >= 0 {
		pv.InHand = true
		pv.Stack = h.Stacks[i]
		pv.Bet = h.Bets[i]
		pv.HasCards = !h.Folded[i]
		pv.Shown = h.Shown[i]
		if t.visible(i, viewerID) {
			pv.Cards = cardStrings(h.Hole[i])
			if pv.Shown && len(h.Board) >= 3 {
				_, _, pv.HandName = DescribeCards(append([]Card{h.Hole[i][0], h.Hole[i][1]}, h.Board...))
			} else if pv.Shown {
				pv.HandName = DescribeHole(h.Hole[i][0], h.Hole[i][1])
			}
		}
		if t.Odds != nil && i < len(t.Odds) && !h.Folded[i] && !h.Done {
			o := Odds{Win: round4(t.Odds[i].Win), Tie: round4(t.Odds[i].Tie)}
			pv.Odds = &o
		}
		pv.IsButton = i == h.N-1
		sb, bb := h.blindSeats()
		pv.IsSB, pv.IsBB = i == sb, i == bb
		for k := len(h.Log) - 1; k >= 0; k-- {
			if r := h.Log[k]; r.Player == i && r.Type != ActAnte {
				pv.LastAction = &ActionView{PlayerID: p.ID, Street: r.Street.String(), Type: string(r.Type), Amount: r.Amount, To: r.To, AllIn: r.AllIn}
				break
			}
		}
	}
	switch {
	case p.Left:
		pv.Status = "left"
	case p.Out:
		pv.Status = "out"
	case p.Busted:
		pv.Status = "busted"
	case i >= 0 && h.Folded[i]:
		pv.Status = "folded"
	case i >= 0 && h.Stacks[i] == 0 && !h.Done:
		pv.Status = "allIn"
	case i >= 0 && !h.Done:
		pv.Status = "active"
	case p.SittingOut:
		pv.Status = "sittingOut"
	case i >= 0:
		pv.Status = "active"
	default:
		pv.Status = "waiting"
	}
	if t.Status == StatusLobby && p.SitOutNext {
		pv.Status = "sittingOut"
	}
	return pv
}

func (t *Table) youView(p *Player, v *View) *YouView {
	y := &YouView{ID: p.ID, Seat: p.Seat, SittingOut: p.SittingOut, SitOutNext: p.SitOutNext, IsHost: p.ID == t.HostID}
	h := t.Hand
	i := t.handIndex(p.ID)
	if t.Status == StatusLobby {
		i = -1
	}
	if i >= 0 {
		y.Cards = cardStrings(h.Hole[i])
		if len(h.Board) >= 3 {
			_, _, y.HandName = DescribeCards(append([]Card{h.Hole[i][0], h.Hole[i][1]}, h.Board...))
		} else {
			y.HandName = DescribeHole(h.Hole[i][0], h.Hole[i][1])
		}
		if h.Done && !h.Shown[i] && h.Result != nil {
			y.CanShow = true
		}
		if t.Phase == PhaseBetting && h.Actor() == i {
			y.ToAct = true
			y.Legal = t.legalView(h)
		}
		if t.Phase == PhaseBetting && !h.Done && !h.Folded[i] && p.Kind == KindHuman {
			s := t.strengthFor(p.ID, i)
			y.Strength = &s
		}
	}
	if p.Busted {
		y.CanRebuy = t.canRebuy(p)
		y.RebuyUntil = p.RebuyUntil
	}
	return y
}

func (t *Table) legalView(h *Hand) *LegalView {
	l := h.Legal()
	i := l.Player
	lv := &LegalView{Fold: l.CanFold, Check: l.CanCheck, Call: l.CallAmount}
	lv.AllIn = h.Bets[i] + h.Stacks[i]
	if !l.CanRaise {
		lv.AllIn = h.Bets[i] + l.CallAmount
	}
	if l.CanRaise {
		r := &RaiseView{IsBet: l.IsBet, Min: l.MinTo, Max: l.MaxTo}
		pot := h.Pot() + (l.CurrentBet - h.Bets[i]) // the pot after calling
		add := func(label string, to int) {
			if to < l.MinTo || to > l.MaxTo {
				return
			}
			for _, p := range r.Presets {
				if p.To == to {
					return
				}
			}
			r.Presets = append(r.Presets, PresetView{Label: label, To: to})
		}
		add("Min", l.MinTo)
		for _, f := range []struct {
			label    string
			num, den int
		}{{"½ pot", 1, 2}, {"¾ pot", 3, 4}, {"Pot", 1, 1}} {
			add(f.label, l.CurrentBet+pot*f.num/f.den)
		}
		add("All-in", l.MaxTo)
		r.Presets = append([]PresetView{}, r.Presets...)
		lv.Raise = r
	}
	return lv
}

func (t *Table) resultView() *ResultView {
	h := t.Hand
	if !h.Done {
		return nil
	}
	rv := &ResultView{HandNumber: t.HandNumber, Board: cardStrings(h.Board), Pots: []PotResultView{}, Hands: []ShownHandView{}, Uncalled: []UncalledView{}}
	if h.Result == nil {
		rv.Cancelled = true
		return rv
	}
	res := h.Result
	rv.Showdown = res.Showdown
	for _, u := range h.Uncalled {
		rv.Uncalled = append(rv.Uncalled, UncalledView{ID: t.HandIDs[u.Player], Amount: u.Amount})
	}
	for _, pot := range res.Pots {
		pr := PotResultView{Amount: pot.Amount, EligibleIDs: t.ids(pot.Eligible), Winners: []WinnerView{}}
		for k, w := range pot.Winners {
			wv := WinnerView{ID: t.HandIDs[w], Amount: pot.Shares[k]}
			if res.Showdown && h.Shown[w] {
				wv.HandName = res.Names[w]
				wv.BestCards = cardStrings(res.Best[w])
			}
			pr.Winners = append(pr.Winners, wv)
		}
		rv.Pots = append(rv.Pots, pr)
	}
	if res.Showdown {
		var live []int
		for i := 0; i < h.N; i++ {
			if !h.Folded[i] {
				live = append(live, i)
			}
		}
		start := 0
		for k, i := range live {
			if i == res.FirstToShow {
				start = k
			}
		}
		for k := range live {
			i := live[(start+k)%len(live)]
			sh := ShownHandView{ID: t.HandIDs[i], Mucked: !h.Shown[i]}
			if h.Shown[i] {
				sh.Cards = cardStrings(h.Hole[i])
				sh.HandName = res.Names[i]
				sh.BestCards = cardStrings(res.Best[i])
			}
			rv.Hands = append(rv.Hands, sh)
		}
	}
	return rv
}

func (t *Table) standings() []StandingView {
	var out []StandingView
	for _, p := range t.Players {
		place := p.Place
		if place == 0 {
			place = len(t.Players)
		}
		out = append(out, StandingView{ID: p.ID, Name: p.Name, Place: place, Stack: p.Stack, BoughtIn: p.BoughtIn, Net: p.Net()})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Place != out[b].Place {
			return out[a].Place < out[b].Place
		}
		if t.Settings.Cash() {
			return out[a].Net > out[b].Net
		}
		return out[a].Stack > out[b].Stack
	})
	return out
}

// collectedPots are the pots formed from chips already gathered into the
// middle (this street's bets excluded), with their live eligible players.
func (h *Hand) collectedPots() []Pot {
	saved := h.Contrib
	gathered := make([]int, h.N)
	for i := range gathered {
		gathered[i] = h.Contrib[i] - h.Bets[i]
	}
	h.Contrib = gathered
	pots := h.buildPots()
	h.Contrib = saved
	return pots
}

func round4(x float64) float64 { return math.Round(x*10000) / 10000 }
