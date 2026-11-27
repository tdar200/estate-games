package hardai

import (
	"testing"

	"github.com/tdar200/estate-games/internal/engine"
)

// Debt fixes shared by every level (their debt moves are the heuristic's,
// internal/ai) and Hard's own mortgage plans (raiseCash).

var orangeSpaces = []int{16, 18, 19}

// diceOf makes the next roll come up a + b.
func diceOf(a, b int) engine.RNG {
	values := []float64{(float64(a) - 0.5) / 6, (float64(b) - 0.5) / 6}
	return engine.RNGFunc(func() float64 {
		if len(values) == 0 {
			return 0.5
		}
		v := values[0]
		values = values[1:]
		return v
	})
}

// startedGame is a two-seat game past the opening order, with the first
// player to move on turn.
func startedGame(t *testing.T, rules *engine.RulesUpdate) (*engine.Game, *engine.Player, *engine.Player) {
	t.Helper()
	rng := newSplitMix(7)
	g, err := NewGameWithRules([]string{"A", "B"}, rules, rng)
	if err != nil {
		t.Fatal(err)
	}
	d := &Driver{RNG: rng}
	for g.Phase == engine.PhaseDeterminingOrder || g.Phase == engine.PhaseOrderCountdown {
		if err := d.Step(g); err != nil {
			t.Fatal(err)
		}
	}
	me := engine.CurrentPlayer(g)
	if g.Phase != engine.PhaseAwaitingRoll || me == nil {
		t.Fatalf("phase %s after the opening order", g.Phase)
	}
	other := g.Players[0]
	if other == me {
		other = g.Players[1]
	}
	return g, me, other
}

// landOnLuxuryTax rolls me from Century Avenue onto Luxury Tax ($100) with
// $50 in hand.
func landOnLuxuryTax(t *testing.T, g *engine.Game, me *engine.Player) {
	t.Helper()
	me.Cash, me.Position = 50, 34
	if err := engine.PerformAction(g, me.ID, engine.Action{Type: engine.ActionRoll}, diceOf(1, 3)); err != nil {
		t.Fatal(err)
	}
	if g.Phase != engine.PhaseDebt || g.Debt == nil || g.Debt.PayerID != me.ID || g.Debt.Amount != 100 {
		t.Fatalf("expected a $100 debt, phase %s debt %+v", g.Phase, g.Debt)
	}
}

// N2: every level sells a hotel when the bank has fewer than four houses,
// instead of going bankrupt with hotels on the board.
func TestLevelsSellHotelsInHouseShortage(t *testing.T) {
	g, me, other := startedGame(t, nil)
	for _, index := range orangeSpaces {
		g.Properties[index].OwnerID, g.Properties[index].Houses = me.ID, 5
	}
	// The other seat's red, yellow and green spaces hold 30 houses: the
	// bank has 2 left.
	need := 30
	for _, index := range []int{21, 23, 24, 26, 27, 29, 31, 32, 34} {
		g.Properties[index].OwnerID = other.ID
		g.Properties[index].Houses = min(4, need)
		need -= g.Properties[index].Houses
	}
	if houses := engine.BankSupply(g).Houses; houses != 2 {
		t.Fatalf("bank has %d houses, want 2", houses)
	}
	landOnLuxuryTax(t, g, me)

	for _, level := range []Level{Easy, Normal, Hard, Expert, Grandmaster} {
		t.Run(level.String(), func(t *testing.T) {
			sim := Clone(g)
			bot := NewBot(level, 1, Options{Budget: -1, Workers: 1})
			a, ok := bot.Choose(sim, me.ID)
			if !ok || a.Type != engine.ActionSellBuilding || sim.Properties[a.SpaceIndex].Houses != 5 {
				t.Fatalf("chose %v (%v), want to sell a hotel", a, ok)
			}
			if err := engine.PerformAction(sim, me.ID, a, nil); err != nil {
				t.Fatalf("engine refused %v: %v", a, err)
			}
			if a, ok = bot.Choose(sim, me.ID); !ok || a.Type != engine.ActionPayDebt {
				t.Fatalf("then chose %v (%v), want pay_debt", a, ok)
			}
			if err := engine.PerformAction(sim, me.ID, a, nil); err != nil {
				t.Fatalf("engine refused %v: %v", a, err)
			}
			if p := sim.PlayerByID(me.ID); p.Bankrupt || sim.Debt != nil || p.Cash != 50+150-100 {
				t.Fatalf("bankrupt %v, debt %+v, cash %d", p.Bankrupt, sim.Debt, p.Cash)
			}
		})
	}
}

// N3: Hard's mortgage plans (mortgage-then-buy, mortgage-to-build) never
// name a deed pledged on a loan, which the engine refuses to mortgage.
func TestRaiseCashSkipsPledgedDeeds(t *testing.T) {
	g, me, other := startedGame(t, loansOnly())
	const north, west, south = 5, 15, 25
	for _, index := range []int{north, west, south} {
		g.Properties[index].OwnerID = me.ID
	}
	all := raiseCash(g, me.ID, -1, -1)
	if len(all) != 3 {
		t.Fatalf("unpledged: %v, want all three railroads", all)
	}
	first := all[0].SpaceIndex

	// Pledge the first choice on a loan from the other seat.
	if err := engine.PerformAction(g, me.ID, engine.Action{Type: engine.ActionOfferLoan, Direction: engine.LoanBorrow,
		TargetID: other.ID, Principal: 100, Repay: 120, DueTurns: 10, Collateral: []int{first}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := engine.PerformAction(g, other.ID, engine.Action{Type: engine.ActionAcceptLoan}, nil); err != nil {
		t.Fatal(err)
	}
	if !pledged(g, first) {
		t.Fatalf("%s should be pledged", engine.Board[first].Name)
	}
	for _, need := range []int{-1, 100, 200} {
		plan := raiseCash(g, me.ID, need, -1)
		if len(plan) == 0 {
			t.Fatalf("need %d: no plan, want the two unpledged railroads", need)
		}
		for _, a := range plan {
			if a.SpaceIndex == first {
				t.Fatalf("need %d: plan %v mortgages pledged %s", need, plan, engine.Board[first].Name)
			}
			if err := engine.PerformAction(Clone(g), me.ID, a, nil); err != nil {
				t.Fatalf("need %d: engine refused %v: %v", need, a, err)
			}
		}
	}
	// $300 needs all three; with one pledged it cannot be raised.
	if plan := raiseCash(g, me.ID, 300, -1); plan != nil {
		t.Fatalf("need 300: %v, want nil (only $200 is mortgageable)", plan)
	}
}
