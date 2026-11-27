package ai_test

import (
	"slices"
	"testing"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
)

// Tests for the AI's way out of a debt: hotels in a house shortage, pledged
// deeds, and the debt move without a loan request (ChooseDebtAction). The
// engine plays by its fixed rules here (not game.js's legacy mode).

// Cal's colour groups, used to drain the bank's houses.
var calSpaces = []int{21, 23, 24, 26, 27, 29, 31, 32, 34} // red, yellow, green

// shortageDebtGame gives the AI Bea hotels on orange, $50 and nothing else,
// fills Cal's red, yellow and green spaces with houses until the bank has
// bankHouses (< 4) left, then rolls Bea onto Luxury Tax ($100 to the bank).
func shortageDebtGame(t *testing.T, rules engine.RulesUpdate, bankHouses int) (*engine.Game, *engine.Player) {
	t.Helper()
	g, p := newRulesGame(t, rules, "ai:Bea", "ai:Cal")
	bea, cal := p[0], p[1]
	give(g, bea, orange...)
	build(g, 5, orange...)
	give(g, cal, calSpaces...)
	for need, i := 32-bankHouses, 0; need > 0; i++ {
		g.Properties[calSpaces[i]].Houses = min(4, need)
		need -= min(4, need)
	}
	bea.Cash, bea.Position = 50, 34
	mustDo(t, g, bea.ID, engine.Action{Type: engine.ActionRoll}, diceRNG(1, 3))
	if g.Phase != engine.PhaseDebt || g.Debt == nil || g.Debt.Amount != 100 {
		t.Fatalf("expected a $100 debt, phase %s debt %+v", g.Phase, g.Debt)
	}
	if supply := engine.BankSupply(g); supply.Houses != bankHouses {
		t.Fatalf("bank has %d houses, want %d", supply.Houses, bankHouses)
	}
	return g, bea
}

// payDebtWith applies choose's moves for player until the debt is paid,
// checking that each is one of allowed and that the engine takes it.
func payDebtWith(t *testing.T, g *engine.Game, player *engine.Player, choose func(*ai.Game, string) *ai.Action, allowed ...string) []*ai.Action {
	t.Helper()
	var moves []*ai.Action
	for range 30 {
		a := choose(aiView(t, g), player.ID)
		if a == nil || !slices.Contains(allowed, a.Type) {
			t.Fatalf("%s chose %s in debt, want one of %v", player.Name, actionJSON(a), allowed)
		}
		applyAI(t, g, player.ID, a, nil)
		moves = append(moves, a)
		if a.Type == ai.ActionPayDebt {
			if g.Debt != nil || player.Bankrupt {
				t.Fatalf("after paying: debt %+v, bankrupt %v", g.Debt, player.Bankrupt)
			}
			return moves
		}
	}
	t.Fatalf("%s did not pay the debt in 30 moves", player.Name)
	return nil
}

// N2: with fewer than four houses in the bank, a hotel can still be sold (the
// engine breaks it down to the houses left and sells the rest). The AI used
// to skip such hotels and go bankrupt with them on the board.
func TestAISellsHotelsInHouseShortage(t *testing.T) {
	t.Run("game.js rule: bankrupt", func(t *testing.T) {
		// The parity tests keep ai.js's choice; this is the bug.
		g, bea := shortageDebtGame(t, engine.RulesUpdate{}, 2)
		if a := decide(t, g, bea.ID); a == nil || a.Type != ai.ActionBankrupt {
			t.Fatalf("with game.js's rule: got %s, want bankrupt", actionJSON(a))
		}
	})

	defer ai.UseEngineHotelRule()()
	for _, tc := range []struct {
		name       string
		rules      engine.RulesUpdate
		bankHouses int
	}{
		{"two houses left", engine.RulesUpdate{}, 2},
		{"no houses left", engine.RulesUpdate{}, 0},
		{"three houses left, loans on", loansOn(), 3}, // no lender: Cal never rescues a set
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, bea := shortageDebtGame(t, tc.rules, tc.bankHouses)
			moves := payDebtWith(t, g, bea, ai.ChooseAction, ai.ActionSellBuilding, ai.ActionPayDebt)
			sold := moves[0]
			if sold.Type != ai.ActionSellBuilding || !slices.Contains(orange, sold.SpaceIndex) {
				t.Fatalf("first move %s, want a hotel sale on orange", actionJSON(sold))
			}
			if len(moves) != 2 {
				t.Fatalf("moves %d, want one sale then pay_debt", len(moves))
			}
			if houses := g.Properties[sold.SpaceIndex].Houses; houses != tc.bankHouses {
				t.Fatalf("the hotel came down to %d houses, want %d", houses, tc.bankHouses)
			}
			// $50 + (5 - houses left) levels at half the $100 build cost - $100.
			if want := 50 + (5-tc.bankHouses)*50 - 100; bea.Cash != want {
				t.Fatalf("cash %d, want %d", bea.Cash, want)
			}
		})
	}
}

// N3: a deed pledged on an active loan cannot be mortgaged (engine
// collateralTradeCheck). The AI used to pick it anyway, and the refusal led
// to bankruptcy.
func TestAINeverMortgagesPledgedDeeds(t *testing.T) {
	// pledgedDebtGame: the AI Ava holds deeds; pledge (>= 0) secures a loan
	// from Hal; then Ava, with $50, lands on Luxury Tax ($100). Hal, her only
	// opponent, already lends to her, so she has nobody to ask for a loan.
	pledgedDebtGame := func(t *testing.T, pledge int, deeds ...int) (*engine.Game, *engine.Player) {
		t.Helper()
		g, p := newRulesGame(t, loansOn(), "ai:Ava", "Hal")
		ava, hal := p[0], p[1]
		give(g, ava, deeds...)
		if pledge >= 0 {
			mustDo(t, g, ava.ID, borrowOffer(hal, 100, 120, 10, pledge), nil)
			mustDo(t, g, hal.ID, engine.Action{Type: engine.ActionAcceptLoan}, nil)
			if engine.LoanSecuredBy(g, pledge) == nil {
				t.Fatalf("%s should secure the loan", engine.Board[pledge].Name)
			}
		}
		ava.Cash, ava.Position = 50, 34
		mustDo(t, g, ava.ID, engine.Action{Type: engine.ActionRoll}, diceRNG(1, 3))
		if g.Phase != engine.PhaseDebt || g.Debt == nil || g.Debt.Amount != 100 {
			t.Fatalf("expected a $100 debt, phase %s debt %+v", g.Phase, g.Debt)
		}
		return g, ava
	}
	deeds := []int{northRR, westRR}

	g, ava := pledgedDebtGame(t, -1, deeds...)
	baseline := decide(t, g, ava.ID)
	if baseline == nil || baseline.Type != ai.ActionMortgage || !slices.Contains(deeds, baseline.SpaceIndex) {
		t.Fatalf("baseline: got %s, want to mortgage a railroad", actionJSON(baseline))
	}
	favourite := baseline.SpaceIndex

	t.Run("mortgages another deed", func(t *testing.T) {
		g, ava := pledgedDebtGame(t, favourite, deeds...)
		moves := payDebtWith(t, g, ava, ai.ChooseAction, ai.ActionMortgage, ai.ActionPayDebt)
		if moves[0].SpaceIndex == favourite {
			t.Fatalf("mortgaged pledged %s", engine.Board[favourite].Name)
		}
		if g.Properties[favourite].Mortgaged {
			t.Fatal("the pledged deed is mortgaged")
		}
	})

	t.Run("only pledged deeds left: bankrupt, not a refused mortgage", func(t *testing.T) {
		g, ava := pledgedDebtGame(t, northRR, northRR)
		a := decide(t, g, ava.ID)
		if a == nil || a.Type != ai.ActionBankrupt {
			t.Fatalf("got %s, want bankrupt", actionJSON(a))
		}
		applyAI(t, g, ava.ID, a, nil)
		if !ava.Bankrupt {
			t.Fatal("Ava should be out")
		}
	})
}

// N1: ChooseDebtAction is the debt move without a loan request, for a seat
// that cannot send offer_loan (the server's stand-in for an away person).
func TestChooseDebtActionNeverAsksForALoan(t *testing.T) {
	t.Run("AI seat that would ask for a loan", func(t *testing.T) {
		g, p := debtGame(t, "ai:Bea", "ai:Cal", "ai:Dee")
		bea := p[0]
		p[1].Cash = 3000
		if a := decide(t, g, bea.ID); a == nil || a.Type != ai.ActionOfferLoan {
			t.Fatalf("baseline: ChooseAction chose %s, want offer_loan", actionJSON(a))
		}
		payDebtWith(t, g, bea, ai.ChooseDebtAction, ai.ActionSellBuilding, ai.ActionPayDebt)
		if len(g.Loans) != 0 || g.LoanOffer != nil {
			t.Fatalf("loans %+v, offer %+v", g.Loans, g.LoanOffer)
		}
	})

	t.Run("person's seat, as the stand-in plays it", func(t *testing.T) {
		g, p := debtGame(t, "Bea", "ai:Cal", "ai:Dee")
		bea, cal := p[0], p[1]
		if a := decide(t, g, bea.ID); a != nil {
			t.Fatalf("ChooseAction plays only computer seats, got %s", actionJSON(a))
		}
		if a := ai.ChooseDebtAction(aiView(t, g), cal.ID); a != nil {
			t.Fatalf("Cal owes nothing but got %s", actionJSON(a))
		}
		payDebtWith(t, g, bea, ai.ChooseDebtAction, ai.ActionSellBuilding, ai.ActionPayDebt)
		if a := ai.ChooseDebtAction(aiView(t, g), bea.ID); a != nil {
			t.Fatalf("debt paid but got %s", actionJSON(a))
		}
	})

	t.Run("hotels in a house shortage", func(t *testing.T) {
		defer ai.UseEngineHotelRule()()
		g, bea := shortageDebtGame(t, loansOn(), 1)
		moves := payDebtWith(t, g, bea, ai.ChooseDebtAction, ai.ActionSellBuilding, ai.ActionPayDebt)
		if len(moves) != 2 {
			t.Fatalf("moves %d, want one hotel sale then pay_debt", len(moves))
		}
	})

	t.Run("nothing to sell: bankrupt", func(t *testing.T) {
		g, p := newRulesGame(t, loansOn(), "ai:Bea", "ai:Cal", "ai:Dee")
		bea := p[0]
		bea.Cash, bea.Position = 50, 34
		mustDo(t, g, bea.ID, engine.Action{Type: engine.ActionRoll}, diceRNG(1, 3))
		a := ai.ChooseDebtAction(aiView(t, g), bea.ID)
		if a == nil || a.Type != ai.ActionBankrupt {
			t.Fatalf("got %s, want bankrupt", actionJSON(a))
		}
		applyAI(t, g, bea.ID, a, nil)
	})
}
