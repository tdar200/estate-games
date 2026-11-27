package ai_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
)

// Tests for the AI's house-rule play (loans.go, rules.go), driving the real
// engine: the AI sees json.Marshal(*engine.Game) decoded into ai.Game (aiView)
// and its actions go back through their JSON form, exactly as on the server.

const loanTestClock = 1_700_000_000_000

// Board indexes used below.
const (
	northRR        = 5  // North Station Railroad
	pineRoad       = 16 // orange
	harborStreet   = 18 // orange
	centralRoad    = 19 // orange
	goldenLeafRoad = 21 // red
	museumRoad     = 23 // red
	galaxyAvenue   = 24 // red
	westRR         = 15
	southRR        = 25
	skylineRoad    = 37 // dark blue, $350
)

var orange = []int{pineRoad, harborStreet, centralRoad}

func loansOn() engine.RulesUpdate {
	on := true
	return engine.RulesUpdate{Loans: &on}
}

// seqRNG yields values in order, then 0.5.
func seqRNG(values ...float64) engine.RNG {
	next := 0
	return engine.RNGFunc(func() float64 {
		if next < len(values) {
			next++
			return values[next-1]
		}
		return 0.5
	})
}

// diceRNG makes the next roll come up a + b.
func diceRNG(a, b int) engine.RNG {
	return seqRNG((float64(a)-0.5)/6, (float64(b)-0.5)/6)
}

func mustDo(t *testing.T, g *engine.Game, playerID string, action engine.Action, rng engine.RNG) {
	t.Helper()
	if err := engine.PerformAction(g, playerID, action, rng); err != nil {
		t.Fatalf("%s: PerformAction(%v): %v", playerID, action, err)
	}
}

// performAI sends an AI action to the engine through its JSON form.
func performAI(g *engine.Game, playerID string, a *ai.Action, rng engine.RNG) error {
	if a == nil {
		return errors.New("no action")
	}
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	var action engine.Action
	if err := json.Unmarshal(data, &action); err != nil {
		return err
	}
	return engine.PerformAction(g, playerID, action, rng)
}

func applyAI(t *testing.T, g *engine.Game, playerID string, a *ai.Action, rng engine.RNG) {
	t.Helper()
	if err := performAI(g, playerID, a, rng); err != nil {
		data, _ := json.Marshal(a)
		t.Fatalf("%s: engine refused AI action %s: %v", playerID, data, err)
	}
}

func decide(t *testing.T, g *engine.Game, playerID string) *ai.Action {
	t.Helper()
	return ai.ChooseAction(aiView(t, g), playerID)
}

func actionJSON(a *ai.Action) string {
	data, _ := json.Marshal(a)
	return string(data)
}

// newRulesGame seats the players ("ai:" prefix for computers), sets the house
// rules as the host and plays the opening order so seats keep their order and
// seat 0 is on turn.
func newRulesGame(t *testing.T, rules engine.RulesUpdate, names ...string) (*engine.Game, []*engine.Player) {
	t.Helper()
	g := engine.CreateGame("LOANS")
	g.Clock = func() int64 { return loanTestClock }
	g.NewID = engine.SequentialIDs()
	players := make([]*engine.Player, len(names))
	for i, name := range names {
		kind := engine.KindHuman
		if rest, isAI := strings.CutPrefix(name, "ai:"); isAI {
			name, kind = rest, engine.KindAI
		}
		player, err := engine.AddPlayer(g, name, kind)
		if err != nil {
			t.Fatal(err)
		}
		players[i] = player
	}
	mustDo(t, g, g.HostID, engine.Action{Type: engine.ActionSetRules, Rules: &rules}, nil)
	mustDo(t, g, g.HostID, engine.Action{Type: engine.ActionStart}, seqRNG())
	for i, player := range players {
		mustDo(t, g, player.ID, engine.Action{Type: engine.ActionRollForOrder}, diceRNG(6, 6-i))
	}
	engine.CompleteOpeningOrder(g, *g.OpeningOrder.CountdownEndsAt)
	for i, player := range players {
		if g.Players[i] != player {
			t.Fatalf("seat %d is %s, want %s", i, g.Players[i].Name, player.Name)
		}
	}
	return g, players
}

func give(g *engine.Game, owner *engine.Player, indexes ...int) {
	for _, index := range indexes {
		g.Properties[index].OwnerID = owner.ID
	}
}

func build(g *engine.Game, houses int, indexes ...int) {
	for _, index := range indexes {
		g.Properties[index].Houses = houses
	}
}

// endTurn ends the current player's turn without rolling.
func endTurn(t *testing.T, g *engine.Game) {
	t.Helper()
	g.Phase = engine.PhaseTurnComplete
	mustDo(t, g, engine.CurrentPlayer(g).ID, engine.Action{Type: engine.ActionEndTurn}, nil)
}

func borrowOffer(target *engine.Player, principal, repay, dueTurns int, collateral ...int) engine.Action {
	return engine.Action{Type: engine.ActionOfferLoan, Direction: engine.LoanBorrow, TargetID: target.ID,
		Principal: principal, Repay: repay, DueTurns: dueTurns, Collateral: collateral}
}

func lendOffer(target *engine.Player, principal, repay, dueTurns int, collateral ...int) engine.Action {
	action := borrowOffer(target, principal, repay, dueTurns, collateral...)
	action.Direction = engine.LoanLend
	return action
}

// answerLoan checks the AI's answer to the pending offer, that AcceptsLoan
// agrees and that the engine takes the answer.
func answerLoan(t *testing.T, g *engine.Game, ai1 *engine.Player, accept bool) {
	t.Helper()
	want := ai.ActionRejectLoan
	if accept {
		want = ai.ActionAcceptLoan
	}
	got := decide(t, g, ai1.ID)
	if got == nil || got.Type != want {
		t.Fatalf("%s answered %s, want %s", ai1.Name, actionJSON(got), want)
	}
	if ai.AcceptsLoan(aiView(t, g), ai1.ID) != accept {
		t.Fatalf("AcceptsLoan disagrees with ChooseAction")
	}
	for _, other := range g.Players {
		if other.ID != ai1.ID && other.Kind == engine.KindAI {
			if a := decide(t, g, other.ID); a != nil {
				t.Fatalf("%s is not the target but chose %s", other.Name, actionJSON(a))
			}
		}
	}
	returnPhase := g.LoanOffer.ReturnPhase
	applyAI(t, g, ai1.ID, got, nil)
	if g.Phase != returnPhase || g.LoanOffer != nil {
		t.Fatalf("phase %s after the answer, want %s", g.Phase, returnPhase)
	}
}

func TestAILenderAnswersLoanRequests(t *testing.T) {
	cases := []struct {
		name                     string
		setup                    func(g *engine.Game, hana *engine.Player)
		principal, repay, dueTrn int
		collateral               []int
		accept                   bool
	}{
		{name: "fair request", principal: 200, repay: 240, dueTrn: 4, accept: true},
		{name: "fair long request", principal: 300, repay: 390, dueTrn: 10, accept: true},
		{name: "stingy rate", principal: 200, repay: 205, dueTrn: 4},
		{name: "rate too low for a long term", principal: 200, repay: 230, dueTrn: 10},
		{
			name:  "collateral lowers the rate",
			setup: func(g *engine.Game, hana *engine.Player) { give(g, hana, northRR, westRR, southRR) },
			// 8%: below the unsecured 10%, above the fully secured 5%.
			principal: 200, repay: 216, dueTrn: 4, collateral: []int{northRR, westRR, southRR}, accept: true,
		},
		{
			name:      "same rate unsecured",
			setup:     func(g *engine.Game, hana *engine.Player) { give(g, hana, northRR, westRR, southRR) },
			principal: 200, repay: 216, dueTrn: 4,
		},
		{
			name:      "borrower could not repay",
			setup:     func(g *engine.Game, hana *engine.Player) { hana.Cash = 50 },
			principal: 400, repay: 480, dueTrn: 4,
		},
		{name: "would break the lender's reserve", principal: 1400, repay: 1680, dueTrn: 4},
		{
			name:      "leader would build on a full set",
			setup:     func(g *engine.Game, hana *engine.Player) { give(g, hana, orange...) },
			principal: 600, repay: 720, dueTrn: 5,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, p := newRulesGame(t, loansOn(), "Hana", "ai:Lena", "ai:Otto")
			hana, lena := p[0], p[1]
			if tc.setup != nil {
				tc.setup(g, hana)
			}
			mustDo(t, g, hana.ID, borrowOffer(lena, tc.principal, tc.repay, tc.dueTrn, tc.collateral...), nil)
			hanaCash, lenaCash := hana.Cash, lena.Cash
			answerLoan(t, g, lena, tc.accept)
			if !tc.accept {
				if len(g.Loans) != 0 || hana.Cash != hanaCash {
					t.Fatalf("a declined offer moved money or made a loan")
				}
				return
			}
			if len(g.Loans) != 1 || g.Loans[0].Status != engine.LoanActive || g.Loans[0].LenderID != lena.ID {
				t.Fatalf("loans after accepting: %+v", g.Loans)
			}
			if hana.Cash != hanaCash+tc.principal || lena.Cash != lenaCash-tc.principal {
				t.Fatalf("cash %d/%d, want %d/%d", hana.Cash, lena.Cash, hanaCash+tc.principal, lenaCash-tc.principal)
			}
		})
	}
}

func TestAIBorrowerAnswersLoanOffers(t *testing.T) {
	ownsOrange := func(g *engine.Game, bo *engine.Player) {
		give(g, bo, orange...)
		bo.Cash = 300
	}
	cases := []struct {
		name                     string
		setup                    func(g *engine.Game, bo *engine.Player)
		principal, repay, dueTrn int
		collateral               []int
		accept                   bool
	}{
		// $300 cash reaches no house tier on orange, $1100 reaches three houses.
		{name: "cash for three houses on orange", setup: ownsOrange, principal: 800, repay: 920, dueTrn: 5, accept: true},
		{name: "too expensive", setup: ownsOrange, principal: 800, repay: 1300, dueTrn: 5},
		{name: "term too short", setup: ownsOrange, principal: 800, repay: 880, dueTrn: 2},
		{name: "collateral would block the build", setup: ownsOrange, principal: 800, repay: 920, dueTrn: 5, collateral: []int{pineRoad}},
		{name: "no use for the cash", principal: 500, repay: 550, dueTrn: 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, p := newRulesGame(t, loansOn(), "Hana", "ai:Bo")
			hana, bo := p[0], p[1]
			if tc.setup != nil {
				tc.setup(g, bo)
			}
			mustDo(t, g, hana.ID, lendOffer(bo, tc.principal, tc.repay, tc.dueTrn, tc.collateral...), nil)
			answerLoan(t, g, bo, tc.accept)
			if !tc.accept {
				return
			}
			// On its turn the AI spends the loan on the set it borrowed for.
			endTurn(t, g)
			a := decide(t, g, bo.ID)
			if a == nil || a.Type != ai.ActionBuild || !slices.Contains(orange, a.SpaceIndex) {
				t.Fatalf("after borrowing the AI chose %s, want a build on orange", actionJSON(a))
			}
			applyAI(t, g, bo.ID, a, nil)
		})
	}
}

// debtGame puts the AI Bea on turn with hotels on orange and $50, then rolls
// her onto Luxury Tax ($100 to the bank): a debt she can only pay by selling
// hotels.
func debtGame(t *testing.T, names ...string) (*engine.Game, []*engine.Player) {
	t.Helper()
	g, p := newRulesGame(t, loansOn(), names...)
	bea := p[0]
	give(g, bea, orange...)
	build(g, 5, orange...)
	bea.Cash = 50
	bea.Position = 34
	mustDo(t, g, bea.ID, engine.Action{Type: engine.ActionRoll}, diceRNG(1, 3))
	if g.Phase != engine.PhaseDebt || g.Debt == nil || g.Debt.Amount != 100 {
		t.Fatalf("expected a $100 debt, phase %s debt %+v", g.Phase, g.Debt)
	}
	return g, p
}

func hotelsKept(g *engine.Game) bool {
	for _, index := range orange {
		if g.Properties[index].Houses != 5 {
			return false
		}
	}
	return true
}

func TestAIAsksForLoanInDebt(t *testing.T) {
	t.Run("asks a non-leading AI and keeps its hotels", func(t *testing.T) {
		g, p := debtGame(t, "ai:Bea", "ai:Cal", "ai:Dee")
		bea, cal, dee := p[0], p[1], p[2]
		cal.Cash = 3000 // Cal leads, so Bea prefers Dee
		a := decide(t, g, bea.ID)
		want := ai.Action{Type: ai.ActionOfferLoan, TargetID: dee.ID, Loan: &ai.LoanTerms{
			Direction: ai.LoanBorrow, Principal: 50, Repay: 60, DueTurns: 4, Collateral: []int{},
		}}
		if actionJSON(a) != actionJSON(&want) {
			t.Fatalf("got %s, want %s", actionJSON(a), actionJSON(&want))
		}
		applyAI(t, g, bea.ID, a, nil)
		answerLoan(t, g, dee, true)
		a = decide(t, g, bea.ID)
		if a == nil || a.Type != ai.ActionPayDebt {
			t.Fatalf("after the loan Bea chose %s, want pay_debt", actionJSON(a))
		}
		applyAI(t, g, bea.ID, a, nil)
		if !hotelsKept(g) || g.Debt != nil || len(engine.ActiveLoans(g)) != 1 {
			t.Fatalf("hotels %v, debt %+v, loans %+v", hotelsKept(g), g.Debt, g.Loans)
		}
	})

	t.Run("falls back to selling when the human lender declines", func(t *testing.T) {
		g, p := debtGame(t, "ai:Bea", "ai:Cal", "Hal")
		bea, cal, hal := p[0], p[1], p[2]
		cal.Cash = 20 // too poor to lend: only Hal can
		a := decide(t, g, bea.ID)
		if a == nil || a.Type != ai.ActionOfferLoan || a.TargetID != hal.ID {
			t.Fatalf("got %s, want a loan request to Hal", actionJSON(a))
		}
		applyAI(t, g, bea.ID, a, nil)
		mustDo(t, g, hal.ID, engine.Action{Type: engine.ActionRejectLoan}, nil)
		for range 20 {
			a = decide(t, g, bea.ID)
			if a == nil || a.Type == ai.ActionOfferLoan {
				t.Fatalf("after the refusal Bea chose %s", actionJSON(a))
			}
			applyAI(t, g, bea.ID, a, nil)
			if a.Type == ai.ActionPayDebt {
				break
			}
			if a.Type != ai.ActionSellBuilding {
				t.Fatalf("Bea chose %s, want sell_building", actionJSON(a))
			}
		}
		if g.Debt != nil || hotelsKept(g) || len(g.Loans) != 0 {
			t.Fatalf("debt %+v, hotels kept %v, loans %+v", g.Debt, hotelsKept(g), g.Loans)
		}
	})

	t.Run("offers collateral to a hesitant human lender", func(t *testing.T) {
		g, p := newRulesGame(t, loansOn(), "ai:Bea", "ai:Cal", "Hal")
		bea, cal, hal := p[0], p[1], p[2]
		give(g, bea, orange...)
		build(g, 5, orange...)
		give(g, bea, northRR)
		bea.Cash, cal.Cash = 50, 20
		mustDo(t, g, bea.ID, engine.Action{Type: engine.ActionRoll}, diceRNG(1, 3)) // Income Tax, $200
		// $50 cash + $100 from the railroad cannot cover $200 without selling
		// hotels. Bea leads, so a cautious lender would refuse an unsecured
		// rescue: she pledges the railroad.
		a := decide(t, g, bea.ID)
		want := ai.Action{Type: ai.ActionOfferLoan, TargetID: hal.ID, Loan: &ai.LoanTerms{
			Direction: ai.LoanBorrow, Principal: 150, Repay: 180, DueTurns: 4, Collateral: []int{northRR},
		}}
		if actionJSON(a) != actionJSON(&want) {
			t.Fatalf("got %s, want %s", actionJSON(a), actionJSON(&want))
		}
		applyAI(t, g, bea.ID, a, nil)
		mustDo(t, g, hal.ID, engine.Action{Type: engine.ActionAcceptLoan}, nil)
		if engine.LoanSecuredBy(g, northRR) == nil {
			t.Fatal("the railroad should secure the loan")
		}
		if a = decide(t, g, bea.ID); a == nil || a.Type != ai.ActionPayDebt {
			t.Fatalf("after the loan Bea chose %s, want pay_debt", actionJSON(a))
		}
		applyAI(t, g, bea.ID, a, nil)
		if !hotelsKept(g) {
			t.Fatal("hotels sold despite the loan")
		}
	})

	t.Run("no willing lender: sells at once", func(t *testing.T) {
		// Two players: the only opponent never rescues a set from being sold.
		g, p := debtGame(t, "ai:Bea", "ai:Cal")
		a := decide(t, g, p[0].ID)
		if a == nil || a.Type != ai.ActionSellBuilding {
			t.Fatalf("got %s, want sell_building", actionJSON(a))
		}
	})

	t.Run("mortgages instead when that covers the debt", func(t *testing.T) {
		g, p := newRulesGame(t, loansOn(), "ai:Bea", "ai:Cal", "ai:Dee")
		bea := p[0]
		give(g, bea, orange...)
		build(g, 5, orange...)
		give(g, bea, northRR)
		bea.Cash, bea.Position = 50, 34
		p[1].Cash = 3000
		mustDo(t, g, bea.ID, engine.Action{Type: engine.ActionRoll}, diceRNG(1, 3))
		a := decide(t, g, bea.ID)
		if a == nil || a.Type != ai.ActionMortgage || a.SpaceIndex != northRR {
			t.Fatalf("got %s, want to mortgage the railroad", actionJSON(a))
		}
	})
}

func TestAIRespectsPledgedCollateral(t *testing.T) {
	t.Run("never builds on a pledged group", func(t *testing.T) {
		g, p := newRulesGame(t, loansOn(), "ai:Ava", "Hal")
		ava, hal := p[0], p[1]
		give(g, ava, orange...)
		ava.Cash = 400
		if a := decide(t, g, ava.ID); a == nil || a.Type != ai.ActionBuild {
			t.Fatalf("baseline: got %s, want a build on orange", actionJSON(a))
		}
		// Ava pledges Pine Road for a loan she cannot repay early.
		mustDo(t, g, ava.ID, borrowOffer(hal, 300, 900, 10, pineRoad), nil)
		mustDo(t, g, hal.ID, engine.Action{Type: engine.ActionAcceptLoan}, nil)
		ava.Cash = 400
		a := decide(t, g, ava.ID)
		if a == nil || a.Type != ai.ActionRoll {
			t.Fatalf("with Pine Road pledged: got %s, want roll", actionJSON(a))
		}
		applyAI(t, g, ava.ID, a, diceRNG(3, 4))
	})

	t.Run("repays early to unblock building", func(t *testing.T) {
		g, p := newRulesGame(t, loansOn(), "ai:Ava", "Hal")
		ava, hal := p[0], p[1]
		give(g, ava, orange...)
		ava.Cash = 2000
		mustDo(t, g, ava.ID, borrowOffer(hal, 100, 120, 10, pineRoad), nil)
		mustDo(t, g, hal.ID, engine.Action{Type: engine.ActionAcceptLoan}, nil)
		loan := g.Loans[0]
		a := decide(t, g, ava.ID)
		if a == nil || a.Type != ai.ActionRepayLoan || a.LoanID != loan.ID {
			t.Fatalf("got %s, want repay_loan %s", actionJSON(a), loan.ID)
		}
		applyAI(t, g, ava.ID, a, nil)
		if loan.Status != engine.LoanRepaid {
			t.Fatalf("loan status %s", loan.Status)
		}
		if a = decide(t, g, ava.ID); a == nil || a.Type != ai.ActionBuild || !slices.Contains(orange, a.SpaceIndex) {
			t.Fatalf("after repaying: got %s, want a build on orange", actionJSON(a))
		}
		applyAI(t, g, ava.ID, a, nil)
	})

	// tradeGame: Hal (on turn), Ava (AI) and Cy. Ava holds two orange
	// properties and Galaxy Avenue, Hal holds Central Road and two reds:
	// Ava's natural trade gives Galaxy Avenue for Central Road. Ava is short
	// of cash, so the offer has to carry a property.
	tradeGame := func(t *testing.T) (*engine.Game, *engine.Player, *engine.Player, *engine.Player) {
		g, p := newRulesGame(t, loansOn(), "Hal", "ai:Ava", "Cy")
		hal, ava, cy := p[0], p[1], p[2]
		give(g, ava, pineRoad, harborStreet, galaxyAvenue)
		give(g, hal, centralRoad, goldenLeafRoad, museumRoad)
		ava.Cash = 260
		return g, hal, ava, cy
	}
	pledgedIn := func(a *ai.Action, index int) bool {
		return a != nil && a.Type == ai.ActionOfferTrade &&
			(slices.Contains(a.Offer.Properties, index) || slices.Contains(a.Request.Properties, index))
	}

	g, _, ava, _ := tradeGame(t)
	endTurn(t, g)
	baseline := decide(t, g, ava.ID)
	if baseline == nil || baseline.Type != ai.ActionOfferTrade ||
		!slices.Contains(baseline.Offer.Properties, galaxyAvenue) || !slices.Contains(baseline.Request.Properties, centralRoad) {
		t.Fatalf("baseline trade: got %s, want Galaxy Avenue for Central Road", actionJSON(baseline))
	}

	t.Run("never offers its own pledged property", func(t *testing.T) {
		g, _, ava, cy := tradeGame(t)
		endTurn(t, g)
		mustDo(t, g, ava.ID, borrowOffer(cy, 10, 12, 5, galaxyAvenue), nil)
		mustDo(t, g, cy.ID, engine.Action{Type: engine.ActionAcceptLoan}, nil)
		a := decide(t, g, ava.ID)
		if pledgedIn(a, galaxyAvenue) {
			t.Fatalf("offered pledged Galaxy Avenue: %s", actionJSON(a))
		}
		applyAI(t, g, ava.ID, a, diceRNG(3, 4))
	})

	t.Run("never asks for a pledged property", func(t *testing.T) {
		g, hal, ava, cy := tradeGame(t)
		mustDo(t, g, hal.ID, borrowOffer(cy, 10, 12, 5, centralRoad), nil)
		mustDo(t, g, cy.ID, engine.Action{Type: engine.ActionAcceptLoan}, nil)
		_, err := ai.QuoteTrade(aiView(t, g), hal.ID, ai.NewQuoteRequest(ava.ID, []int{centralRoad}, nil))
		if err == nil || err.Error() != "Central Road is security for a loan" {
			t.Fatalf("quote for pledged Central Road: %v", err)
		}
		endTurn(t, g)
		a := decide(t, g, ava.ID)
		if pledgedIn(a, centralRoad) {
			t.Fatalf("asked for pledged Central Road: %s", actionJSON(a))
		}
		applyAI(t, g, ava.ID, a, diceRNG(3, 4))
	})
}

func TestAIRepaysLoanDueNextTurn(t *testing.T) {
	g, p := newRulesGame(t, loansOn(), "ai:Ava", "Hal")
	ava, hal := p[0], p[1]
	mustDo(t, g, ava.ID, borrowOffer(hal, 100, 120, 1), nil)
	mustDo(t, g, hal.ID, engine.Action{Type: engine.ActionAcceptLoan}, nil)
	g.Phase = engine.PhaseTurnComplete
	a := decide(t, g, ava.ID)
	if a == nil || a.Type != ai.ActionRepayLoan || a.LoanID != g.Loans[0].ID {
		t.Fatalf("got %s, want repay_loan", actionJSON(a))
	}
	applyAI(t, g, ava.ID, a, nil)
	if g.Loans[0].Status != engine.LoanRepaid {
		t.Fatalf("loan status %s", g.Loans[0].Status)
	}
	if a = decide(t, g, ava.ID); a == nil || a.Type != ai.ActionEndTurn {
		t.Fatalf("then: got %s, want end_turn", actionJSON(a))
	}
}

func TestAIBuysWhenAuctionsAreOff(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name  string
		rules engine.RulesUpdate
		want  string
	}{
		// $500 cash, Skyline Road costs $350: the auction ceiling is
		// $500 - $180 reserve = $320, so with auctions the AI declines and bids.
		{"default rules", engine.RulesUpdate{}, ai.ActionDecline},
		{"auctions off", engine.RulesUpdate{Auctions: &off}, ai.ActionBuy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, p := newRulesGame(t, tc.rules, "ai:Ava", "Hal")
			ava := p[0]
			ava.Cash, ava.Position = 500, 33
			mustDo(t, g, ava.ID, engine.Action{Type: engine.ActionRoll}, diceRNG(1, 3))
			if g.Phase != engine.PhaseAwaitingPurchase || *g.PendingPurchase != skylineRoad {
				t.Fatalf("phase %s", g.Phase)
			}
			a := decide(t, g, ava.ID)
			if a == nil || a.Type != tc.want {
				t.Fatalf("got %s, want %s", actionJSON(a), tc.want)
			}
			applyAI(t, g, ava.ID, a, nil)
		})
	}
}

// ---------------------------------------------------------------- soak ----

type soakStats struct {
	games, finished, turns, decisions int
	requests, accepted, rejected      int
	earlyRepays, jackpots             int
	loans                             map[string]int // final loan status -> count
	defaults                          map[string]int // how defaulted loans ended -> count
	actions                           map[string]int
}

func newSoakStats() soakStats {
	return soakStats{loans: map[string]int{}, defaults: map[string]int{}, actions: map[string]int{}}
}

// Outcomes of a defaulted loan (soakStats.defaults).
const (
	defaultWrittenOff = "written off: borrower bankrupt to someone else"
	defaultToLender   = "borrower bankrupt to the lender: assets passed"
	defaultCollateral = "deadline missed: collateral passed"
	defaultDebtPaid   = "deadline missed: overdue debt paid in full"
)

// trackLoans classifies loans that defaulted since the last call. statuses
// holds each loan's last seen status; overdue holds the loan whose overdue
// debt is pending, if any.
func trackLoans(g *engine.Game, stats *soakStats, statuses map[string]string, overdue *string) {
	if *overdue != "" && (g.Debt == nil || g.Debt.LoanID != *overdue) {
		loan := engine.FindLoan(g, *overdue)
		if g.PlayerByID(loan.BorrowerID).Bankrupt {
			stats.defaults[defaultToLender]++
		} else {
			stats.defaults[defaultDebtPaid]++
		}
		*overdue = ""
	}
	for _, loan := range g.Loans {
		if statuses[loan.ID] == loan.Status {
			continue
		}
		statuses[loan.ID] = loan.Status
		if loan.Status != engine.LoanDefaulted {
			continue
		}
		lb := g.LastBankruptcy
		switch {
		case g.PlayerByID(loan.BorrowerID).Bankrupt && lb != nil && lb.PlayerID == loan.BorrowerID && lb.CreditorID == loan.LenderID:
			stats.defaults[defaultToLender]++
		case g.PlayerByID(loan.BorrowerID).Bankrupt:
			stats.defaults[defaultWrittenOff]++
		case g.Debt != nil && g.Debt.LoanID == loan.ID:
			*overdue = loan.ID
		default:
			stats.defaults[defaultCollateral]++
		}
	}
}

func (s *soakStats) add(o soakStats) {
	s.games += o.games
	s.finished += o.finished
	s.turns += o.turns
	s.decisions += o.decisions
	s.requests += o.requests
	s.accepted += o.accepted
	s.rejected += o.rejected
	s.earlyRepays += o.earlyRepays
	s.jackpots += o.jackpots
	for k, v := range o.loans {
		s.loans[k] += v
	}
	for k, v := range o.defaults {
		s.defaults[k] += v
	}
	for k, v := range o.actions {
		s.actions[k] += v
	}
}

// soakActor is the seat the game waits on (rooms.go nextAiAction).
func soakActor(g *engine.Game) string {
	if g.Phase == engine.PhaseLoanOffer && g.LoanOffer != nil {
		return g.LoanOffer.TargetID
	}
	return nextActorID(g)
}

const (
	soakTurnCap    = 600  // completed turns per game before giving up (not an error)
	soakStallLimit = 3000 // AI actions without a completed turn: an infinite loop
)

// soakGame plays one all-AI game with loans and the Free Parking jackpot.
func soakGame(seed uint64) (soakStats, error) {
	stats := newSoakStats()
	stats.games = 1
	r := rand.New(rand.NewPCG(seed, seed*0x9e3779b97f4a7c15+1))
	rng := engine.RNGFunc(r.Float64)
	g := engine.CreateGame(fmt.Sprintf("S%04d", seed))
	g.Clock = func() int64 { return loanTestClock }
	g.NewID = engine.SequentialIDs()
	seats := 2 + int(seed%5)
	for i := range seats {
		if _, err := engine.AddPlayer(g, fmt.Sprintf("Bot %d", i+1), engine.KindAI); err != nil {
			return stats, err
		}
	}
	on := true
	rules := engine.RulesUpdate{Loans: &on, FreeParkingJackpot: &on}
	if seed%4 == 3 {
		off := false
		rules.Auctions = &off
	}
	if seed%3 == 1 {
		low := 800 // tight money: more debts, more loan requests
		rules.StartingCash = &low
	}
	if err := engine.PerformAction(g, g.HostID, engine.Action{Type: engine.ActionSetRules, Rules: &rules}, nil); err != nil {
		return stats, err
	}
	if err := engine.PerformAction(g, g.HostID, engine.Action{Type: engine.ActionStart}, rng); err != nil {
		return stats, err
	}
	for g.Phase == engine.PhaseDeterminingOrder {
		for _, id := range slices.Clone(g.OpeningOrder.PendingIDs) {
			if err := engine.PerformAction(g, id, engine.Action{Type: engine.ActionRollForOrder}, rng); err != nil {
				return stats, err
			}
		}
	}
	engine.CompleteOpeningOrder(g, *g.OpeningOrder.CountdownEndsAt)

	completedTurns := func() int {
		total := 0
		for _, n := range g.TurnCounts {
			total += n
		}
		return total
	}
	lastTurns, stall := 0, 0
	lastLanding := ""
	statuses, overdue := map[string]string{}, ""
	for g.Status == engine.StatusPlaying && completedTurns() < soakTurnCap {
		actor := soakActor(g)
		if actor == "" {
			return stats, fmt.Errorf("turn %d: nobody to act in phase %s", completedTurns(), g.Phase)
		}
		data, err := json.Marshal(g)
		if err != nil {
			return stats, err
		}
		var view ai.Game
		if err := json.Unmarshal(data, &view); err != nil {
			return stats, err
		}
		phase := g.Phase
		a := ai.ChooseAction(&view, actor)
		if a == nil {
			return stats, fmt.Errorf("turn %d: AI %s has no action in phase %s", completedTurns(), actor, phase)
		}
		stats.decisions++
		stats.actions[a.Type]++
		switch a.Type {
		case ai.ActionOfferLoan:
			stats.requests++
		case ai.ActionAcceptLoan:
			stats.accepted++
		case ai.ActionRejectLoan:
			stats.rejected++
		case ai.ActionRepayLoan:
			stats.earlyRepays++
		}
		if err := performAI(g, actor, a, rng); err != nil {
			return stats, fmt.Errorf("turn %d, phase %s: engine refused %s from %s: %v",
				completedTurns(), phase, actionJSON(a), actor, err)
		}
		trackLoans(g, &stats, statuses, &overdue)
		if l := g.LastLanding; l != nil && l.Kind == engine.LandingFreeParking && l.ID != lastLanding {
			stats.jackpots++
			lastLanding = l.ID
		}
		if turns := completedTurns(); turns != lastTurns {
			lastTurns, stall = turns, 0
		} else if stall++; stall > soakStallLimit {
			return stats, fmt.Errorf("turn %d: %d AI actions without finishing a turn (last %s in %s)",
				turns, stall, actionJSON(a), phase)
		}
	}
	stats.turns = completedTurns()
	if g.Status == engine.StatusFinished {
		stats.finished++
	}
	for _, loan := range g.Loans {
		stats.loans[loan.Status]++
	}
	return stats, nil
}

func TestSoakAIGamesWithLoans(t *testing.T) {
	games := 200
	if testing.Short() {
		games = 40
	}
	type result struct {
		seed  uint64
		stats soakStats
		err   error
	}
	seeds := make(chan uint64)
	results := make(chan result)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seed := range seeds {
				stats, err := soakGame(seed)
				results <- result{seed, stats, err}
			}
		}()
	}
	go func() {
		for seed := 1; seed <= games; seed++ {
			seeds <- uint64(seed)
		}
		close(seeds)
		wg.Wait()
		close(results)
	}()

	total := newSoakStats()
	failures := 0
	for res := range results {
		total.add(res.stats)
		if res.err != nil {
			failures++
			if failures <= 10 {
				t.Errorf("seed %d: %v", res.seed, res.err)
			}
		}
	}
	if total.games != games {
		t.Fatalf("played %d games, want %d", total.games, games)
	}
	t.Logf("%d games (%d finished, %d hit the %d-turn cap), %d turns, %d AI decisions",
		total.games, total.finished, total.games-total.finished, soakTurnCap, total.turns, total.decisions)
	t.Logf("loans: %d requested, %d accepted, %d rejected; final status %v; %d repaid early by the AI; %d jackpots collected",
		total.requests, total.accepted, total.rejected, total.loans, total.earlyRepays, total.jackpots)
	t.Logf("defaulted loans: %v", total.defaults)
	t.Logf("AI actions: %v", total.actions)
	if total.requests == 0 || total.accepted == 0 {
		t.Errorf("the soak never exercised loans (requests %d, accepted %d)", total.requests, total.accepted)
	}
}
