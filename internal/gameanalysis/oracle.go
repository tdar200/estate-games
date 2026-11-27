// Package gameanalysis explains a recorded Estate game (internal/gamerecord):
// for every decision it estimates each player's probability of winning before
// and after, flags the computers' worst moves and the people's best, and tells
// how the game was won (docs/GAME-RECORDS.md, "Analysing a game").
//
// It uses internal/hardai and internal/valuenet read-only, through their
// public APIs.
package gameanalysis

import (
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
	"github.com/tdar200/estate-games/internal/hardai"
	"github.com/tdar200/estate-games/internal/valuenet"
)

// Oracle estimates every player's probability of winning from a position.
// An Oracle is used by one goroutine at a time; Analyse makes one per worker
// with Options.NewOracle.
type Oracle interface {
	// Name describes the oracle for the report.
	Name() string
	// Horizon is the turn number (gamerecord.TurnNumber) that an estimate
	// of g looks ahead to by default.
	Horizon(g *engine.Game) int
	// WinProbs returns each player's probability of winning g, by player
	// id, looking ahead to the start of turn horizon (an oracle that does
	// not look ahead ignores it). Players still in sum to 1; bankrupt
	// players get 0. A finished game gives its winner 1.
	//
	// The positions before and after a decision are scored to the same
	// horizon, so that both are read at the same point of the game: the
	// value network is not indifferent to whose turn it is.
	WinProbs(g *engine.Game, horizon int) map[string]float64
}

// NetOracle reads a position with the value network alone (the Expert bot's
// network, internal/valuenet): about 40 µs per player. The network was
// trained on positions at the start of a turn, so mid-turn positions (a
// pending purchase, an auction) are read crudely. Games the network cannot
// read (more than six players still in) are scored by net-worth share.
type NetOracle struct {
	ev  *valuenet.Evaluator
	pos valuenet.Position
}

// NewNetOracle returns an oracle on net (nil: the embedded model).
func NewNetOracle(net *valuenet.Net) (*NetOracle, error) {
	if net == nil {
		var err error
		if net, err = valuenet.Default(); err != nil {
			return nil, fmt.Errorf("value network: %w", err)
		}
	}
	return &NetOracle{ev: valuenet.NewEvaluator(net)}, nil
}

// Name implements Oracle.
func (o *NetOracle) Name() string { return "value network alone" }

// Horizon implements Oracle: the network does not look ahead.
func (o *NetOracle) Horizon(g *engine.Game) int { return gamerecord.TurnNumber(g) }

// WinProbs implements Oracle.
func (o *NetOracle) WinProbs(g *engine.Game, _ int) map[string]float64 {
	if out, done := settled(g); done {
		return out
	}
	if !valuenet.Fits(g) {
		return netWorthShare(g)
	}
	valuenet.FillFromGame(&o.pos, g)
	probs := o.ev.WinProbs(&o.pos)
	out := make(map[string]float64, len(g.Players))
	for _, p := range g.Players {
		if slot := valuenet.SlotOf(g, p.ID); slot >= 0 && !p.Bankrupt {
			out[p.ID] = probs[slot]
		} else {
			out[p.ID] = 0
		}
	}
	return out
}

// settled scores a finished game.
func settled(g *engine.Game) (map[string]float64, bool) {
	if g.Status == engine.StatusFinished && g.WinnerID != "" {
		out := make(map[string]float64, len(g.Players))
		for _, p := range g.Players {
			out[p.ID] = 0
		}
		out[g.WinnerID] = 1
		return out, true
	}
	return nil, false
}

// netWorthShare is each player's share of the total net worth of the players
// still in (a crude estimate for positions the network cannot read).
func netWorthShare(g *engine.Game) map[string]float64 {
	out := make(map[string]float64, len(g.Players))
	total := 0.0
	for _, p := range g.Players {
		if !p.Bankrupt {
			total += math.Max(0, float64(engine.NetWorth(g, p.ID)))
		}
	}
	for _, p := range g.Players {
		if p.Bankrupt {
			out[p.ID] = 0
		} else if total > 0 {
			out[p.ID] = math.Max(0, float64(engine.NetWorth(g, p.ID))) / total
		}
	}
	return out
}

// RolloutOracle plays a position forward several times with the Expert bot's
// rollout policy (the self-play-tuned heuristic, hardai.ExpertParams, for
// every seat) and scores where each rollout stops with the value network, as
// the Expert bot does in its own search. Every position is played with the
// same rollout seeds (common random numbers), so the difference between the
// positions before and after a move measures the move more than the dice.
// Like the bots, it sees only public information: each rollout reshuffles the
// unseen decks.
type RolloutOracle struct {
	// Rollouts per position.
	Rollouts int
	// Turns is how far ahead a rollout looks by default: the number of turn
	// starts, where 0 means one full round (a turn for every player still
	// in) and 1 means the rest of the current turn.
	Turns int
	// Trades lets the policy propose trades in rollouts. Off by default:
	// the heuristic's trade search is most of a rollout's cost (like the
	// bots' own light rollouts, trade answers still follow the policy).
	Trades bool
	// Seed makes the rollouts reproducible.
	Seed uint64

	net      *NetOracle
	policy   *hardai.HeuristicBot
	fallback *hardai.HeuristicBot
}

// NewRolloutOracle returns a rollout oracle scoring with net (nil: the
// embedded model).
func NewRolloutOracle(rollouts, turns int, seed uint64, net *valuenet.Net) (*RolloutOracle, error) {
	scorer, err := NewNetOracle(net)
	if err != nil {
		return nil, err
	}
	return &RolloutOracle{
		Rollouts: rollouts, Turns: turns, Seed: seed, net: scorer,
		policy:   hardai.NewTunedBot(hardai.ExpertParams()),
		fallback: hardai.NewHeuristicBot(false),
	}, nil
}

// Name implements Oracle.
func (o *RolloutOracle) Name() string {
	horizon := "one round"
	if o.Turns == 1 {
		horizon = "the rest of the turn"
	} else if o.Turns > 1 {
		horizon = fmt.Sprintf("%d turns", o.Turns)
	}
	trades := ", which makes no trade offers of its own"
	if o.Trades {
		trades = ""
	}
	return fmt.Sprintf("value network after %d rollouts of %s with the Expert policy%s", o.Rollouts, horizon, trades)
}

// Horizon implements Oracle.
func (o *RolloutOracle) Horizon(g *engine.Game) int {
	turns := o.Turns
	if turns <= 0 {
		turns = 0
		for _, p := range g.Players {
			if !p.Bankrupt {
				turns++
			}
		}
	}
	return gamerecord.TurnNumber(g) + max(1, turns)
}

// WinProbs implements Oracle.
func (o *RolloutOracle) WinProbs(g *engine.Game, horizon int) map[string]float64 {
	if out, done := settled(g); done {
		return out
	}
	if o.Rollouts <= 0 {
		return o.net.WinProbs(g, horizon)
	}
	sum := make(map[string]float64, len(g.Players))
	for k := 0; k < o.Rollouts; k++ {
		for id, p := range o.rollout(g, k, horizon) {
			sum[id] += p
		}
	}
	for id := range sum {
		sum[id] /= float64(o.Rollouts)
	}
	return sum
}

// rollout plays one future of g to the first turn start at or after turn
// horizon, and scores it.
func (o *RolloutOracle) rollout(g *engine.Game, k, horizon int) map[string]float64 {
	sim := hardai.Clone(g)
	sim.Log = nil
	rng := rand.New(rand.NewPCG(o.Seed, uint64(k)+1))
	sim.Decks.Chance = reshuffled(sim.Decks.Chance, rng)
	sim.Decks.Chest = reshuffled(sim.Decks.Chest, rng)
	// Rollouts roll every seat's dice ("choose dice" is for people at the
	// table).
	sim.Settings.DiceMode = engine.DiceModeRandom
	seats := make([]hardai.Seat, 0, len(sim.Players))
	for _, p := range sim.Players {
		seats = append(seats, hardai.Seat{PlayerID: p.ID, Bot: o.policy})
	}
	d := hardai.Driver{Seats: seats, Fallback: o.fallback, RNG: rng, TrimLog: true}
	if !o.Trades {
		noTrades(sim)
		d.OnAction = func(g *engine.Game, _ string, _ engine.Action) { noTrades(g) }
	}
	turn := sim.Turn
	for steps := 0; steps < 5000 && sim.Status == engine.StatusPlaying; steps++ {
		if err := d.Step(sim); err != nil {
			break // stalled: score it where it stopped
		}
		if sim.Turn != turn {
			turn = sim.Turn
			if gamerecord.TurnNumber(sim) >= horizon {
				break
			}
		}
	}
	return o.net.WinProbs(sim, horizon)
}

func reshuffled(deck []string, rng *rand.Rand) []string {
	out := append([]string(nil), deck...)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// noTrades uses up the current turn's trade proposal, so the heuristic
// skips its (costly) trade search.
func noTrades(g *engine.Game) {
	if g.Turn != nil {
		g.Turn.AITradeAttempted = true
	}
}
