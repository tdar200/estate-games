package gameanalysis

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
	"github.com/tdar200/estate-games/internal/hardai"
)

// Options configure Analyse. Zero fields take the defaults.
type Options struct {
	// Quick uses 24 rollouts per position instead of 300: about a tenth of
	// the time, noisier.
	Quick bool
	// Rollouts per position (default 300; Quick: 24) and Turns per rollout:
	// how many turn starts ahead a position is looked at, 0 (or negative)
	// for one full round, a turn for every player still in. Shorter
	// horizons are faster but read the value network mid-swing: 1 (the
	// rest of the turn) overstates what a move changes several times over.
	Rollouts int
	Turns    int
	// Trades lets the rollout policy propose trades (several times slower;
	// see RolloutOracle.Trades).
	Trades bool
	// Workers evaluating positions at once (default min(4, NumCPU)).
	Workers int
	// Seed for the rollouts (default 1).
	Seed uint64
	// NewOracle makes one oracle per worker; nil uses a RolloutOracle with
	// the settings above.
	NewOracle func() (Oracle, error)
	// Progress, if set, is called as positions are evaluated.
	Progress func(done, total int)
	// Top is how many blunders and best moves to list (default 10).
	Top int
}

func (o Options) withDefaults() Options {
	if o.Rollouts == 0 {
		o.Rollouts = 300
		if o.Quick {
			o.Rollouts = 24
		}
	}
	if o.Turns < 0 {
		o.Turns = 0 // RolloutOracle: one full round
	}
	if o.Workers <= 0 {
		o.Workers = min(4, runtime.NumCPU())
	}
	if o.Seed == 0 {
		o.Seed = 1
	}
	if o.Top <= 0 {
		o.Top = 10
	}
	if o.NewOracle == nil {
		rollouts, turns, seed, trades := o.Rollouts, o.Turns, o.Seed, o.Trades
		o.NewOracle = func() (Oracle, error) {
			oracle, err := NewRolloutOracle(rollouts, turns, seed, nil)
			if oracle != nil {
				oracle.Trades = trades
			}
			return oracle, err
		}
	}
	return o
}

// Analysis is the result, also written as JSON (cmd/estate-replay). Win
// probabilities are lists in the order of Players.
type Analysis struct {
	Record   string   `json:"record"`
	Code     string   `json:"code"`
	Oracle   string   `json:"oracle"`
	Quick    bool     `json:"quick"`
	Players  []Player `json:"players"`
	WinnerID string   `json:"winnerId,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Turns    int      `json:"turns"`
	Steps    int      `json:"steps"`
	// Curve is the win probability at the start of every turn and at the
	// end.
	Curve     []Point    `json:"curve"`
	Decisions []Decision `json:"decisions"`
	Trades    []Trade    `json:"trades,omitempty"`
	Auctions  []Auction  `json:"auctions,omitempty"`
	// Blunders are the computers' decisions that cost them the most; Best
	// are the people's decisions that gained them the most (indexes into
	// Decisions, or trade offers accepted by the other side).
	Blunders []Highlight `json:"blunders"`
	Best     []Highlight `json:"best"`
	// TurningPoints are the biggest swings in the winner's chances, from
	// decisions or from the dice and cards.
	TurningPoints []Swing `json:"turningPoints"`
	ElapsedMs     int64   `json:"elapsedMs"`
	Positions     int     `json:"positions"`
}

// Player is one seat with what the analysis found about it.
type Player struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Difficulty string `json:"difficulty,omitempty"`
	Input      string `json:"input,omitempty"`
	Place      int    `json:"place,omitempty"`
	// Start, Peak and End are win probabilities on the curve.
	Start float64 `json:"start"`
	Peak  float64 `json:"peak"`
	End   float64 `json:"end"`
	// Decisions counts their scored decisions; Worse and Better those that
	// changed their win probability by at least 3 points against what the
	// Expert policy would have done, and VsPolicy is the sum of all their
	// decisions' scores. These do not add up to End - Start: every score is
	// measured against the policy's move, not against the game as played.
	Decisions int     `json:"decisions"`
	Worse     int     `json:"worse"`
	Better    int     `json:"better"`
	VsPolicy  float64 `json:"vsPolicy"`
}

// notable is the size of a decision's score worth counting (3 points).
const notable = 0.03

// Point is the win probability before step Step (Step = Steps: the end).
type Point struct {
	Step int       `json:"step"`
	Turn int       `json:"turn"`
	At   int64     `json:"t,omitempty"`
	P    []float64 `json:"p"`
}

// Decision is one choice a player made.
type Decision struct {
	Step     int    `json:"step"`
	Turn     int    `json:"turn"`
	PlayerID string `json:"playerId"`
	Source   string `json:"source"` // player, ai or standin
	Type     string `json:"type"`   // the action type
	Category string `json:"category"`
	Text     string `json:"text"`
	// Policy is what the Expert policy (the tuned heuristic the rollouts
	// play) would have done instead; empty when it would have done the same.
	Policy  string `json:"policy,omitempty"`
	ThinkMs int    `json:"thinkMs,omitempty"`
	// Before and After are every player's win probability around the step;
	// Delta is the decider's change.
	Before []float64 `json:"before"`
	After  []float64 `json:"after"`
	Delta  float64   `json:"delta"`
}

// Trade is a trade offer with what it did to both sides (accepted trades: from
// before the offer to after the answer).
type Trade struct {
	gamerecord.TradeSummary
	Text          string  `json:"text"`
	ProposerDelta float64 `json:"proposerDelta"`
	TargetDelta   float64 `json:"targetDelta"`
}

// Auction is an auction with what it did to each player (from the decline
// that opened it to its last bid or pass).
type Auction struct {
	gamerecord.AuctionSummary
	Deltas []float64 `json:"deltas"`
}

// Highlight points at a decision (or an accepted trade offer) worth a look.
type Highlight struct {
	Step     int     `json:"step"`
	Turn     int     `json:"turn"`
	PlayerID string  `json:"playerId"`
	Category string  `json:"category"`
	Text     string  `json:"text"`
	Policy   string  `json:"policy,omitempty"`
	Delta    float64 `json:"delta"`
	Before   float64 `json:"before"`
	After    float64 `json:"after"`
	// Count is how many times the same move (with the same alternative) was
	// made, on Turns, costing or gaining Total in all; the other fields
	// describe the instance with the biggest score.
	Count int     `json:"count"`
	Turns []int   `json:"turns"`
	Total float64 `json:"total"`
}

// Swing is a big change in the winner's chances over one turn.
type Swing struct {
	FromStep int     `json:"fromStep"`
	ToStep   int     `json:"toStep"`
	Turn     int     `json:"turn"`
	Delta    float64 `json:"delta"`
	// Kind is "decision", "luck" (dice, cards and forced moves) or "end"
	// (the host ended the game).
	Kind     string `json:"kind"`
	PlayerID string `json:"playerId,omitempty"` // who decided (decisions)
	Text     string `json:"text"`
}

// decisionTypes are the actions that are choices (the rest are forced or
// chance: rolling, confirming a card, paying, the turn-order roll). Ending
// the turn is a choice too: not to build, trade or mortgage any more.
var decisionTypes = map[string]string{
	engine.ActionBuy:          "buy",
	engine.ActionDecline:      "decline",
	engine.ActionBid:          "bid",
	engine.ActionPassAuction:  "auction pass",
	engine.ActionOfferTrade:   "trade offer",
	engine.ActionAcceptTrade:  "trade accepted",
	engine.ActionRejectTrade:  "trade refused",
	engine.ActionCounterTrade: "counter-offer",
	engine.ActionBuild:        "build",
	engine.ActionSellBuilding: "sell building",
	engine.ActionMortgage:     "mortgage",
	engine.ActionUnmortgage:   "unmortgage",
	engine.ActionPayBail:      "jail",
	engine.ActionUseJailCard:  "jail",
	engine.ActionBankrupt:     "bankruptcy",
	engine.ActionOfferLoan:    "loan",
	engine.ActionAcceptLoan:   "loan",
	engine.ActionRejectLoan:   "loan",
	engine.ActionRepayLoan:    "loan",
	engine.ActionEndTurn:      "end turn",
}

// stepInfo is what the replay pass keeps of every step.
type stepInfo struct {
	turn     int
	decision bool
	end      bool // the host ended the game
	text     string
	policy   string   // the Expert policy's move instead (decisions; "" = the same)
	events   []string // log lines the step added
}

// Analyse replays rec and scores its decisions. The record must replay
// without a mismatch.
func Analyse(rec *gamerecord.Record, opts Options) (*Analysis, error) {
	began := time.Now()
	opts = opts.withDefaults()
	probe, err := opts.NewOracle()
	if err != nil {
		return nil, err
	}
	a := &Analysis{Record: rec.ID, Code: rec.Code, Oracle: probe.Name(), Quick: opts.Quick, Steps: len(rec.Steps)}
	index := map[string]int{}
	for i, p := range rec.Players {
		index[p.ID] = i
		a.Players = append(a.Players, Player{ID: p.ID, Name: p.Name, Kind: p.Kind, Difficulty: p.Difficulty, Input: p.Input})
	}
	names := map[string]string{}
	for _, p := range rec.Players {
		names[p.ID] = p.Name
	}

	// Pass 1: replay, describe every step, and keep the positions to score.
	r, err := gamerecord.NewReplayer(rec)
	if err != nil {
		return nil, err
	}
	n := len(rec.Steps)
	info := make([]stepInfo, n)
	positions := map[int]*engine.Game{}
	turns := make([]int, n+1)
	times := make([]int64, n+1)
	keep := func(i int, g *engine.Game) {
		if _, ok := positions[i]; !ok {
			c := hardai.Clone(g)
			c.Log = nil
			positions[i] = c
		}
	}
	lastTurn := -1
	policy := hardai.NewTunedBot(hardai.ExpertParams())
	for i := 0; i < n; i++ {
		g := r.G
		turns[i] = gamerecord.TurnNumber(g)
		st := &rec.Steps[i]
		times[i] = st.At
		kind := st.ActionType()
		_, isDecision := decisionTypes[kind]
		isDecision = isDecision && st.Error == "" && (st.Source == "" || st.Source == gamerecord.SourcePlayer || st.Source == gamerecord.SourceAI || st.Source == gamerecord.SourceStandIn)
		isEnd := st.Kind == gamerecord.StepEnd
		info[i] = stepInfo{turn: turns[i], decision: isDecision, end: isEnd, text: describe(g, st, names)}
		if i == 0 || turns[i] != lastTurn || isDecision || isEnd {
			keep(i, g)
		}
		if isDecision {
			info[i].policy = policyMove(policy, g, st, names, opts.Trades, info[i].text)
		}
		lastTurn = turns[i]
		logBefore := len(g.Log)
		lastLogID := ""
		if logBefore > 0 {
			lastLogID = g.Log[logBefore-1].ID
		}
		if _, err := r.Step(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("the record does not replay: %w", err)
		}
		info[i].events = newLogLines(r.G, lastLogID)
		if isDecision || isEnd {
			keep(i+1, r.G)
		}
	}
	final := r.G
	turns[n] = gamerecord.TurnNumber(final)
	if n > 0 {
		times[n] = rec.Steps[n-1].At
	}
	keep(n, final)
	a.Turns = turns[n] - 1
	a.WinnerID = final.WinnerID
	if rec.Outcome != nil {
		a.Reason = rec.Outcome.Reason
		if a.WinnerID == "" {
			a.WinnerID = rec.Outcome.WinnerID
		}
		for _, s := range rec.Outcome.Standings {
			if i, ok := index[s.PlayerID]; ok {
				a.Players[i].Place = s.Place
			}
		}
	}

	// Pass 2: score the positions, in parallel.
	order := make([]int, 0, len(positions))
	for i := range positions {
		order = append(order, i)
	}
	sort.Ints(order)
	s, err := score(rec, positions, info, opts, probe, index)
	if err != nil {
		return nil, err
	}
	a.Positions = len(s.probs)

	// Pass 3: the curve, the decisions, luck and skill.
	curve := turnStarts(order, info)
	a.fill(rec, s, curve, info, turns, times, index, names)
	byStep := map[int]*Decision{}
	for i := range a.Decisions {
		byStep[a.Decisions[i].Step] = &a.Decisions[i]
	}
	a.Blunders, a.Best = highlights(rec, a, byStep, index, names, opts.Top)
	a.TurningPoints = a.turningPoints(rec, s, curve, info, index, names)
	a.ElapsedMs = time.Since(began).Milliseconds()
	return a, nil
}

func sourceOf(st *gamerecord.Step) string {
	if st.Source == "" {
		return gamerecord.SourcePlayer
	}
	return st.Source
}

// newLogLines returns the texts of the log entries after lastID.
func newLogLines(g *engine.Game, lastID string) []string {
	start := 0
	if lastID != "" {
		start = len(g.Log)
		for i := len(g.Log) - 1; i >= 0; i-- {
			if g.Log[i].ID == lastID {
				start = i + 1
				break
			}
		}
	}
	var out []string
	for _, e := range g.Log[start:] {
		out = append(out, e.Text)
	}
	return out
}

// highlights picks the computers' worst decisions and the people's best,
// counting a trade offer the other side accepted as the proposer's decision
// (its effect shows when it is accepted).
func highlights(rec *gamerecord.Record, a *Analysis, byStep map[int]*Decision, index map[string]int, names map[string]string, top int) (blunders, best []Highlight) {
	kindOf := map[string]string{}
	for _, p := range rec.Players {
		kindOf[p.ID] = p.Kind
	}
	var all []Highlight
	for _, d := range a.Decisions {
		if d.Source == gamerecord.SourceStandIn {
			continue // the computer playing for an away person
		}
		who := index[d.PlayerID]
		all = append(all, Highlight{Step: d.Step, Turn: d.Turn, PlayerID: d.PlayerID, Category: d.Category, Text: d.Text, Policy: d.Policy, Delta: d.Delta, Before: d.Before[who], After: d.After[who]})
	}
	for _, t := range a.Trades {
		if t.Answer != "accepted" {
			continue
		}
		d := byStep[t.AnswerStep]
		if d == nil {
			continue
		}
		pi := index[t.ProposerID]
		all = append(all, Highlight{
			Step: t.AnswerStep, Turn: d.Turn, PlayerID: t.ProposerID, Category: "trade offered",
			Text:  names[t.ProposerID] + "'s offer, accepted by " + names[t.TargetID] + ": " + t.Text,
			Delta: d.After[pi] - d.Before[pi], Before: d.Before[pi], After: d.After[pi],
		})
	}
	for _, h := range all {
		switch kindOf[h.PlayerID] {
		case engine.KindAI:
			if h.Delta < 0 {
				blunders = append(blunders, h)
			}
		default:
			if h.Delta > 0 {
				best = append(best, h)
			}
		}
	}
	sort.SliceStable(blunders, func(i, j int) bool { return blunders[i].Delta < blunders[j].Delta })
	sort.SliceStable(best, func(i, j int) bool { return best[i].Delta > best[j].Delta })
	return groupHighlights(blunders, top), groupHighlights(best, top)
}

// groupHighlights folds the same move made again (same player, same move,
// same alternative: "ended the turn" where the policy builds on the same
// space, turn after turn) into its biggest instance, with the count, the
// turns and the total, and keeps the top entries.
func groupHighlights(list []Highlight, top int) []Highlight {
	var out []Highlight
	at := map[string]int{}
	for _, h := range list {
		key := h.PlayerID + "|" + h.Category + "|" + h.Text + "|" + h.Policy
		if j, ok := at[key]; ok {
			out[j].Count++
			out[j].Total += h.Delta
			out[j].Turns = append(out[j].Turns, h.Turn)
			continue
		}
		h.Count, h.Total, h.Turns = 1, h.Delta, []int{h.Turn}
		at[key] = len(out)
		out = append(out, h)
	}
	if len(out) > top {
		out = out[:top]
	}
	for i := range out {
		h := &out[i]
		sort.Ints(h.Turns)
		h.Delta, h.Before, h.After, h.Total = round1(h.Delta), round1(h.Before), round1(h.After), round1(h.Total)
	}
	return out
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// round keeps 4 decimals (0.01 percentage points).
func round(v []float64) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = round1(x)
	}
	return out
}

func round1(x float64) float64 { return float64(int64(x*10000+copysign(0.5, x))) / 10000 }

func copysign(v, sign float64) float64 {
	if sign < 0 {
		return -v
	}
	return v
}
