package hardai

import (
	"fmt"

	"github.com/tdar200/estate-games/internal/engine"
)

// MatchConfig describes one bot-vs-bot game.
type MatchConfig struct {
	Levels   []Level             // one per seat, in lobby order (turn order is then rolled)
	Seed     int64               // dice, decks and every bot's seed derive from it
	Options  Options             // for the Hard and Easy bots
	MaxTurns int                 // cap on turns (0 = 1000); a capped game is adjudicated on net worth
	Rules    *engine.RulesUpdate // house rules (nil = classic)
	// NewBot, if set, replaces NewBot (for custom bots in tests).
	NewBot func(seat int, level Level, seed int64) Bot
	// NewBotPeek, if set, replaces NewBot and also gets peek, which returns a
	// copy of the game's dice generator as it stands (for the clairvoyant
	// analysis bot only: real bots never see future dice).
	NewBotPeek func(seat int, level Level, seed int64, peek func() engine.RNG) Bot
	// OnAction, if set, is called after every applied action (Driver.OnAction).
	OnAction func(g *engine.Game, playerID string, a engine.Action)
}

// SeatResult is the outcome for one configured seat.
type SeatResult struct {
	PlayerID string
	Level    Level
	Won      bool    // outright winner, or the net-worth leader of a capped game
	Bankrupt bool    // went bankrupt
	NetWorth float64 // at the end
	Rejected int     // actions the engine refused (the heuristic's was played)
}

// MatchResult is the outcome of PlayMatch.
type MatchResult struct {
	Result
	Adjudicated bool // the turn cap ended the game; the winner is the net-worth leader
	Seats       []SeatResult
	Game        *engine.Game // final state
}

// PlayMatch plays one game to completion (or the turn cap).
func PlayMatch(cfg MatchConfig) (MatchResult, error) {
	if len(cfg.Levels) < 2 || len(cfg.Levels) > engine.MaxPlayers {
		return MatchResult{}, fmt.Errorf("hardai: a match needs 2 to %d seats", engine.MaxPlayers)
	}
	rng := newSplitMix(mix(uint64(cfg.Seed), 0xd1ce))
	names := make([]string, len(cfg.Levels))
	for i, level := range cfg.Levels {
		names[i] = level.String() + "-" + itoa(i+1)
	}
	g, err := NewGameWithRules(names, cfg.Rules, rng)
	if err != nil {
		return MatchResult{}, err
	}
	ids := make([]string, len(g.Players)) // lobby order, before the order roll
	for i, p := range g.Players {
		ids[i] = p.ID
	}
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 1000
	}
	d := &Driver{RNG: rng, TrimLog: true, MaxTurns: maxTurns, OnAction: cfg.OnAction}
	for i, level := range cfg.Levels {
		seed := int64(mix(uint64(cfg.Seed), uint64(i)+100))
		var bot Bot
		if cfg.NewBotPeek != nil {
			bot = cfg.NewBotPeek(i, level, seed, func() engine.RNG { c := *rng; return &c })
		} else if cfg.NewBot != nil {
			bot = cfg.NewBot(i, level, seed)
		} else {
			bot = NewBot(level, seed, cfg.Options)
		}
		d.Seats = append(d.Seats, Seat{PlayerID: ids[i], Bot: bot})
	}
	res, err := d.Play(g)
	out := MatchResult{Result: res, Game: g}
	if err != nil {
		return out, err
	}
	winner := res.WinnerID
	if !res.Finished {
		out.Adjudicated = true
		best := -1.0
		for _, p := range g.Players {
			if w := netWorth(g, p); !p.Bankrupt && w > best {
				best, winner = w, p.ID
			}
		}
	}
	for i, id := range ids {
		p := g.PlayerByID(id)
		out.Seats = append(out.Seats, SeatResult{
			PlayerID: id,
			Level:    cfg.Levels[i],
			Won:      id == winner,
			Bankrupt: p.Bankrupt,
			NetWorth: netWorth(g, p),
			Rejected: d.Rejected[id],
		})
	}
	return out, nil
}
