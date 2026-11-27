package hardai

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
)

// Bot chooses the next action of a seat. Choose is called only when
// NextActor(g) names playerID; ok=false means the bot has nothing to do (the
// driver then falls back to the heuristic). Choose must not mutate g.
type Bot interface {
	Choose(g *engine.Game, playerID string) (engine.Action, bool)
}

// NextActor returns the player who must act now, mirroring nextAiAction in
// src/rooms.js (and nextAIPlayer in internal/server) without the "is it an
// AI" filter: the pending opening-order roller, the trade or loan-offer
// target, the auction bidder due (in a timed auction, where anyone but the
// leader may bid, the first of them from TurnPos: engine.AuctionBidderIDs),
// otherwise the current player. orderRoll is
// true when that player must roll for turn order. "" means nobody can act
// (game over, or the order countdown is running: see Step).
func NextActor(g *engine.Game) (playerID string, orderRoll bool) {
	if g.Status != engine.StatusPlaying {
		return "", false
	}
	switch g.Phase {
	case engine.PhaseDeterminingOrder:
		if g.OpeningOrder == nil || len(g.OpeningOrder.PendingIDs) == 0 {
			return "", false
		}
		return g.OpeningOrder.PendingIDs[0], true
	case engine.PhaseOrderCountdown:
		return "", false
	case engine.PhaseTrade:
		if g.Trade == nil {
			return "", false
		}
		return g.Trade.TargetID, false
	case engine.PhaseLoanOffer:
		if g.LoanOffer == nil {
			return "", false
		}
		return g.LoanOffer.TargetID, false
	case engine.PhaseAuction:
		a := g.Auction
		if a == nil || len(a.ActiveIDs) == 0 || (len(a.ActiveIDs) == 1 && a.BidderID == a.ActiveIDs[0]) {
			return "", false
		}
		for offset := range a.ActiveIDs {
			pos := (a.TurnPos + offset) % len(a.ActiveIDs)
			if a.ActiveIDs[pos] != a.BidderID {
				return a.ActiveIDs[pos], false
			}
		}
		return "", false
	}
	if p := engine.CurrentPlayer(g); p != nil {
		return p.ID, false
	}
	return "", false
}

// HeuristicBot plays with internal/ai (the "Normal" level). It builds the
// AI's view directly from the engine state (equal to the documented JSON
// round trip, see view.go) and, like the server, answers a loan offer the AI
// has no opinion on with reject_loan. It is not safe for concurrent use.
type HeuristicBot struct {
	vb     viewBuilder
	params *ai.Params // knobs (nil = ai.DefaultParams, the Normal level)
}

// NewHeuristicBot returns a heuristic bot. forceJSON makes it use the JSON
// round trip for every decision (slow; for checking the fast path).
func NewHeuristicBot(forceJSON bool) *HeuristicBot {
	return &HeuristicBot{vb: viewBuilder{forceJSON: forceJSON}}
}

// NewTunedBot returns a heuristic bot playing with knobs p (nil = defaults).
func NewTunedBot(p *ai.Params) *HeuristicBot {
	return &HeuristicBot{params: p}
}

// Choose implements Bot.
func (h *HeuristicBot) Choose(g *engine.Game, playerID string) (engine.Action, bool) {
	a := h.choose(g, playerID)
	if action, ok := toEngineAction(a); ok {
		return action, true
	}
	if g.Phase == engine.PhaseLoanOffer && g.LoanOffer != nil && g.LoanOffer.TargetID == playerID {
		return engine.Action{Type: engine.ActionRejectLoan}, true
	}
	return engine.Action{}, false
}

// choose returns the raw ai decision (nil when the AI has none).
func (h *HeuristicBot) choose(g *engine.Game, playerID string) *ai.Action {
	view := h.vb.build(g)
	if view == nil {
		return nil
	}
	for _, p := range view.Players {
		if p.ID == playerID {
			p.Kind = engine.KindAI // the AI only plays seats marked "ai"
		}
	}
	return ai.ChooseActionParams(view, playerID, h.params)
}

// Seat binds a bot to a player id.
type Seat struct {
	PlayerID string
	Bot      Bot
}

// Driver plays games to completion with one Bot per seat.
type Driver struct {
	Seats    []Seat
	Fallback Bot        // used when a seat's bot has no action or the engine rejects it; nil = a HeuristicBot
	RNG      engine.RNG // dice; nil = engine.DefaultRNG
	MaxSteps int        // cap on actions (0 = 200000)
	MaxTurns int        // cap on turns started (0 = no cap)
	TrimLog  bool       // empty the game log as it fills (nothing reads it; saves copying)

	// Rejected counts, per player id, actions a seat's bot chose that the
	// engine refused (the fallback's action was played instead).
	Rejected map[string]int
	// OnAction, if set, is called after every applied action.
	OnAction func(g *engine.Game, playerID string, a engine.Action)
}

// Result summarises a played game.
type Result struct {
	Finished bool   // somebody won by bankrupting everyone else
	WinnerID string // "" unless Finished
	Steps    int    // actions applied
	Turns    int    // turns started
}

// ErrStalled is returned when nobody can act in a game that is not over.
var ErrStalled = errors.New("hardai: game stalled")

func (d *Driver) bot(playerID string) Bot {
	for _, s := range d.Seats {
		if s.PlayerID == playerID {
			return s.Bot
		}
	}
	return nil
}

func (d *Driver) fallback() Bot {
	if d.Fallback == nil {
		d.Fallback = NewHeuristicBot(false)
	}
	return d.Fallback
}

// Play runs g until it finishes or hits a cap. g must be playing (started).
func (d *Driver) Play(g *engine.Game) (Result, error) {
	maxSteps := d.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 200_000
	}
	res := Result{}
	lastTurn := g.Turn
	for res.Steps < maxSteps {
		if g.Status != engine.StatusPlaying {
			break
		}
		if d.MaxTurns > 0 && res.Turns >= d.MaxTurns {
			break
		}
		if err := d.Step(g); err != nil {
			return res, fmt.Errorf("step %d (phase %s): %w", res.Steps, g.Phase, err)
		}
		res.Steps++
		if g.Turn != lastTurn {
			lastTurn = g.Turn
			res.Turns++
		}
	}
	res.Finished = g.Status == engine.StatusFinished
	res.WinnerID = g.WinnerID
	return res, nil
}

// Step applies one action (or completes the turn-order countdown).
func (d *Driver) Step(g *engine.Game) error {
	if d.TrimLog {
		trimLog(g)
	}
	if g.Phase == engine.PhaseOrderCountdown {
		now := g.Now()
		if g.OpeningOrder != nil && g.OpeningOrder.CountdownEndsAt != nil {
			now = max(now, *g.OpeningOrder.CountdownEndsAt)
		}
		if !engine.CompleteOpeningOrder(g, now) {
			return ErrStalled
		}
		return nil
	}
	playerID, orderRoll := NextActor(g)
	if playerID == "" {
		return ErrStalled
	}
	if orderRoll {
		return engine.PerformAction(g, playerID, engine.Action{Type: engine.ActionRollForOrder}, d.RNG)
	}
	bot := d.bot(playerID)
	if bot == nil {
		bot = d.fallback()
	}
	action, ok := bot.Choose(g, playerID)
	if ok {
		err := engine.PerformAction(g, playerID, action, d.RNG)
		if err == nil {
			if d.OnAction != nil {
				d.OnAction(g, playerID, action)
			}
			return nil
		}
		if bot == d.Fallback {
			return fmt.Errorf("%s rejected: %w", action, err)
		}
		if d.Rejected == nil {
			d.Rejected = map[string]int{}
		}
		d.Rejected[playerID]++
	}
	action, ok = d.fallback().Choose(g, playerID)
	if !ok {
		return ErrStalled
	}
	if err := engine.PerformAction(g, playerID, action, d.RNG); err != nil {
		return fmt.Errorf("fallback %s rejected: %w", action, err)
	}
	if d.OnAction != nil {
		d.OnAction(g, playerID, action)
	}
	return nil
}

// trimLog shortens the log to its last logKeep entries before it reaches the
// engine's 120-entry cap (beyond which every append copies the whole log).
// The rules never read the log; the AI only scans its recent tail (to
// remember a loan request during the current debt), and losing that entry
// only makes it more cautious: it never asks twice.
func trimLog(g *engine.Game) {
	if len(g.Log) >= 100 {
		n := copy(g.Log, g.Log[len(g.Log)-logKeep:])
		clear(g.Log[n:])
		g.Log = g.Log[:n]
	}
}

const logKeep = 20

// NewGame creates a started game with one AI seat per name, at the
// turn-order roll (phase determining_order). Ids are deterministic
// ("ai_1", "ai_2", ...), the clock is fixed, and the decks are shuffled
// with rng.
func NewGame(names []string, rng engine.RNG) (*engine.Game, error) {
	return NewGameWithRules(names, nil, rng)
}

// NewGameWithRules is NewGame with house rules applied in the lobby (nil =
// classic rules).
func NewGameWithRules(names []string, rules *engine.RulesUpdate, rng engine.RNG) (*engine.Game, error) {
	g := engine.CreateGame("ARENA")
	g.Clock = fixedClock
	g.NewID = newSimIDs("")
	for _, name := range names {
		if _, err := engine.AddPlayer(g, name, engine.KindAI); err != nil {
			return nil, err
		}
	}
	// AI seats never become host; the host only matters in the lobby.
	g.HostID = g.Players[0].ID
	if rules != nil {
		if err := engine.PerformAction(g, g.HostID, engine.Action{Type: engine.ActionSetRules, Rules: rules}, rng); err != nil {
			return nil, err
		}
	}
	if err := engine.StartGame(g, g.HostID, rng); err != nil {
		return nil, err
	}
	return g, nil
}

// fixedClock is the clock of driver-made games and simulations.
func fixedClock() int64 { return 1_700_000_000_000 }

// newSimIDs returns a cheap id generator: purely cosmetic ids (log lines,
// moves, landings) are constant; ids the rules look up (players, trades,
// loans, offers, bankruptcies) are unique.
func newSimIDs(tag string) func(prefix string) string {
	counter := 0
	return func(prefix string) string {
		switch prefix {
		case "log":
			return "log_0"
		case "move":
			return "move_0"
		case "landing":
			return "landing_0"
		}
		counter++
		return prefix + "_" + tag + strconv.Itoa(counter)
	}
}
