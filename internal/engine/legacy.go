package engine

import "math"

// Estate fixed some rules that src/game.js got wrong (review of 3 Nov 2026:
// B1, B6, B10–B13, B15, and the 6-player cap). The Go engine is still checked
// step by step against games recorded from game.js (trace_test.go), so a game
// can be put in legacy mode, where it keeps game.js's behaviour for exactly
// those rules. Only the trace parity tests do that: legacyRules is unexported,
// is not part of the game JSON, and every real game plays by the fixed rules.

// legacyMaxPlayers is game.js's seat limit.
const legacyMaxPlayers = 6

func (g *Game) maxPlayers() int {
	if g.legacyRules {
		return legacyMaxPlayers
	}
	return MaxPlayers
}

// unmortgageCost is UnmortgageCost, or game.js's float version in legacy mode
// (Math.ceil((price / 2) * 1.1), $1 too much on some deeds).
func (g *Game) unmortgageCost(price int) int {
	if g.legacyRules {
		return int(math.Ceil(float64(price) / 2 * 1.1))
	}
	return UnmortgageCost(price)
}

// transferFee is MortgageTransferFee, or game.js's float version in legacy
// mode (Math.ceil((price / 2) * 0.1)).
func (g *Game) transferFee(price int) int {
	if g.legacyRules {
		return int(math.Ceil(float64(price) / 2 * 0.1))
	}
	return MortgageTransferFee(price)
}

// UseGameJSRules puts g in legacy mode (above). It is for tests in other
// packages that replay recordings of src/game.js (internal/ai's
// TestEngineTraceDecisions); real games never call it. game.js had only the
// turn-order auction, so the timed auction a new game starts with is
// switched off (the game stores no rules, like game.js's).
func (g *Game) UseGameJSRules() {
	g.legacyRules = true
	g.Settings.Rules.AuctionClock = 0
}
