package ai_test

import (
	"testing"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
)

// In a timed open auction a computer may bid whenever it does not lead,
// with the same ceiling as in a turn-order auction; while it leads it waits.
func TestAIBidsInTimedAuction(t *testing.T) {
	clock0 := 0
	for _, tc := range []struct {
		name  string
		rules engine.RulesUpdate
		timed bool
	}{
		{"timed (new games)", engine.RulesUpdate{}, true},
		{"turn order", engine.RulesUpdate{AuctionClock: &clock0}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// $500 cash, Skyline Road costs $350: the AI declines and its
			// auction ceiling is between $210 and $400.
			g, p := newRulesGame(t, tc.rules, "ai:Ava", "Hal", "Ivy")
			ava, hal, ivy := p[0], p[1], p[2]
			ava.Cash, ava.Position = 500, 33
			mustDo(t, g, ava.ID, engine.Action{Type: engine.ActionRoll}, diceRNG(1, 3))
			applyAI(t, g, ava.ID, decide(t, g, ava.ID), nil) // decline
			if g.Phase != engine.PhaseAuction || g.Auction.Timed() != tc.timed {
				t.Fatalf("phase %s, timed %v", g.Phase, g.Auction.Timed())
			}
			// Hal is due first in turn order; in a timed auction Ava need not wait.
			a := decide(t, g, ava.ID)
			if !tc.timed {
				if a != nil {
					t.Fatalf("out of turn: got %s", actionJSON(a))
				}
				return
			}
			if a == nil || a.Type != ai.ActionBid || a.Amount != 10 {
				t.Fatalf("opening bid: got %s", actionJSON(a))
			}
			applyAI(t, g, ava.ID, a, nil)
			if a := decide(t, g, ava.ID); a != nil {
				t.Fatalf("while leading: got %s", actionJSON(a))
			}
			mustDo(t, g, ivy.ID, engine.Action{Type: engine.ActionBid, Amount: 200}, nil)
			if a := decide(t, g, ava.ID); a == nil || a.Type != ai.ActionBid || a.Amount != 210 {
				t.Fatalf("outbid: got %s", actionJSON(a))
			}
			mustDo(t, g, hal.ID, engine.Action{Type: engine.ActionBid, Amount: 400}, nil)
			if a := decide(t, g, ava.ID); a == nil || a.Type != ai.ActionPassAuction {
				t.Fatalf("over the ceiling: got %s", actionJSON(a))
			}
		})
	}
}
