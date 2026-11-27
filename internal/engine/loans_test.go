package engine

import (
	"fmt"
	"testing"
)

// Tests for player-to-player loans (loans.go). Helpers live in rules_test.go
// and game_test.go.

const phaseError = "This action is not available during the current phase"

// loanGame starts a game with the loans rule; by default the players are
// Alpha (on turn), Sam and Cleo.
func loanGame(t *testing.T, names ...string) (*Game, []*Player) {
	t.Helper()
	if len(names) == 0 {
		names = []string{"Alpha", "Sam", "Cleo"}
	}
	return rulesGame(t, RulesUpdate{Loans: boolPtr(true)}, names...)
}

func lendAction(target *Player, principal, repay, dueTurns int, collateral ...int) Action {
	return Action{Type: ActionOfferLoan, Direction: LoanLend, TargetID: target.ID,
		Principal: principal, Repay: repay, DueTurns: dueTurns, Collateral: collateral}
}

func borrowAction(target *Player, principal, repay, dueTurns int, collateral ...int) Action {
	action := lendAction(target, principal, repay, dueTurns, collateral...)
	action.Direction = LoanBorrow
	return action
}

// lendAndAccept has the current player lend to borrower and returns the loan.
func lendAndAccept(t *testing.T, g *Game, borrower *Player, principal, repay, dueTurns int, collateral ...int) *Loan {
	t.Helper()
	act(t, g, CurrentPlayer(g).ID, lendAction(borrower, principal, repay, dueTurns, collateral...), nil)
	act(t, g, borrower.ID, Action{Type: ActionAcceptLoan}, nil)
	return g.Loans[len(g.Loans)-1]
}

// skipTurn ends the current player's turn without rolling.
func skipTurn(t *testing.T, g *Game) {
	t.Helper()
	g.Phase = PhaseTurnComplete
	act(t, g, CurrentPlayer(g).ID, Action{Type: ActionEndTurn}, nil)
}

// skipTo ends turns until player is on turn (at least one).
func skipTo(t *testing.T, g *Game, player *Player) {
	t.Helper()
	for range len(g.Players) {
		skipTurn(t, g)
		if CurrentPlayer(g) == player {
			return
		}
	}
	t.Fatalf("%s never got the turn", player.Name)
}

func TestLoanLendOfferAccepted(t *testing.T) {
	g, p := loanGame(t)
	alpha, sam, cleo := p[0], p[1], p[2]

	act(t, g, alpha.ID, lendAction(sam, 200, 240, 3), nil)
	equal(t, "phase", g.Phase, PhaseLoanOffer)
	offer := g.LoanOffer
	equal(t, "offer", *offer, LoanOffer{
		ID: offer.ID, Direction: LoanLend, ProposerID: alpha.ID, TargetID: sam.ID,
		LenderID: alpha.ID, BorrowerID: sam.ID, Principal: 200, Repay: 240, DueTurns: 3,
		Collateral: []int{}, ReturnPhase: PhaseAwaitingRoll, CreatedAt: testClock,
	})
	equal(t, "log", lastLog(g), "Alpha offered Sam a $200 loan")
	equal(t, "no cash moved", [2]int{alpha.Cash, sam.Cash}, [2]int{1500, 1500})
	equal(t, "current player", CurrentPlayer(g), alpha)

	public := jsonObject(t, PublicState(g, cleo.ID, Viewer{}))
	equal(t, "public offer direction", public["loanOffer"].(map[string]any)["direction"], any("lend"))
	equal(t, "public offer target", public["loanOffer"].(map[string]any)["targetId"], any(sam.ID))
	equal(t, "public currentPlayerId", public["currentPlayerId"], any(alpha.ID))

	actFails(t, g, alpha.ID, Action{Type: ActionRoll}, constant(0), phaseError)
	actFails(t, g, alpha.ID, Action{Type: ActionAcceptLoan}, nil, "Only the recipient can accept this loan offer")
	actFails(t, g, cleo.ID, Action{Type: ActionAcceptLoan}, nil, "Only the recipient can accept this loan offer")
	actFails(t, g, alpha.ID, Action{Type: ActionOfferTrade, TargetID: sam.ID,
		Offer: &TradeBundle{Cash: 1}, Request: &TradeBundle{Cash: 1}}, nil, phaseError)

	act(t, g, sam.ID, Action{Type: ActionAcceptLoan}, nil)
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
	equal(t, "offer cleared", g.LoanOffer, (*LoanOffer)(nil))
	equal(t, "cash", [2]int{alpha.Cash, sam.Cash}, [2]int{1300, 1700})
	loan := g.Loans[0]
	equal(t, "loan", *loan, Loan{
		ID: loan.ID, LenderID: alpha.ID, BorrowerID: sam.ID, Principal: 200, Repay: 240,
		DueTurn: 3, Collateral: []int{}, Status: LoanActive, CreatedAt: testClock,
	})
	equal(t, "log", lastLog(g), "Alpha lent Sam $200; Sam repays $240 by turn 3")
	equal(t, "active", ActiveLoans(g), []*Loan{loan})
	equal(t, "find", FindLoan(g, loan.ID), loan)

	object := jsonObject(t, g)
	if _, present := object["loanOffer"]; present {
		t.Fatal("loanOffer should be omitted once answered")
	}
	loans := object["loans"].([]any)
	equal(t, "json loan", loans[0], any(map[string]any{
		"id": loan.ID, "lenderId": alpha.ID, "borrowerId": sam.ID, "principal": 200.0, "repay": 240.0,
		"dueTurn": 3.0, "collateral": []any{}, "status": "active", "createdAt": float64(testClock),
	}))
	equal(t, "public loans", len(jsonObject(t, PublicState(g, "", Viewer{Role: "spectator"}))["loans"].([]any)), 1)

	// Play continues; an offer from turn_complete returns there.
	act(t, g, alpha.ID, Action{Type: ActionRoll}, sequence(0, 0.4)) // Stamp Duty
	equal(t, "phase", g.Phase, PhaseTurnComplete)
	act(t, g, alpha.ID, lendAction(cleo, 100, 100, 2), nil)
	equal(t, "return phase", g.LoanOffer.ReturnPhase, PhaseTurnComplete)
	act(t, g, cleo.ID, Action{Type: ActionAcceptLoan}, nil)
	equal(t, "phase", g.Phase, PhaseTurnComplete)
	equal(t, "cleo due", g.Loans[1].DueTurn, 2)
	act(t, g, alpha.ID, Action{Type: ActionEndTurn}, nil)
}

func TestLoanBorrowRequestOnOwnTurn(t *testing.T) {
	g, p := loanGame(t)
	alpha, sam := p[0], p[1]
	g.Properties[1].OwnerID = alpha.ID

	act(t, g, alpha.ID, borrowAction(sam, 300, 330, 1, 1), nil)
	offer := g.LoanOffer
	equal(t, "offer", *offer, LoanOffer{
		ID: offer.ID, Direction: LoanBorrow, ProposerID: alpha.ID, TargetID: sam.ID,
		LenderID: sam.ID, BorrowerID: alpha.ID, Principal: 300, Repay: 330, DueTurns: 1,
		Collateral: []int{1}, ReturnPhase: PhaseAwaitingRoll, CreatedAt: testClock,
	})
	equal(t, "log", lastLog(g), "Alpha asked Sam for a $300 loan")
	act(t, g, sam.ID, Action{Type: ActionAcceptLoan}, nil)
	equal(t, "cash", [2]int{alpha.Cash, sam.Cash}, [2]int{1800, 1200})
	assertLog(t, g, "Sam lent Alpha $300; Alpha repays $330 by turn 1")
	equal(t, "log", lastLog(g), "Begonia Road secures the loan")
	equal(t, "secured", LoanSecuredBy(g, 1), g.Loans[0])

	// dueTurns 1 asked on your own turn: due when your next turn starts.
	skipTo(t, g, alpha)
	equal(t, "status", g.Loans[0].Status, LoanRepaid)
	equal(t, "cash", [2]int{alpha.Cash, sam.Cash}, [2]int{1470, 1530})
	equal(t, "log", lastLog(g), "Alpha repaid Sam $330 (loan due)")
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
	equal(t, "released", LoanSecuredBy(g, 1), (*Loan)(nil))
}

func TestLoanRequestDuringDebt(t *testing.T) {
	g, p := loanGame(t)
	alpha, sam, cleo := p[0], p[1], p[2]
	g.Properties[39].OwnerID = sam.ID
	alpha.Cash = 20
	alpha.Position = 33
	act(t, g, alpha.ID, Action{Type: ActionRoll}, constant(0.34)) // 3 + 3 -> Crown Avenue, rent 50
	equal(t, "phase", g.Phase, PhaseDebt)

	actFails(t, g, alpha.ID, lendAction(cleo, 100, 150, 2), nil, phaseError)
	actFails(t, g, sam.ID, borrowAction(cleo, 100, 150, 2), nil, "It is not your turn yet")

	// In the debt phase the direction may be left out: it is a request.
	raw := fmt.Sprintf(`{"type":"offer_loan","targetId":%q,"principal":100,"repay":"150","dueTurns":2}`, cleo.ID)
	act(t, g, alpha.ID, decodeAction(t, raw), nil)
	equal(t, "phase", g.Phase, PhaseLoanOffer)
	equal(t, "direction", g.LoanOffer.Direction, LoanBorrow)
	equal(t, "lender", g.LoanOffer.LenderID, cleo.ID)
	equal(t, "borrower", g.LoanOffer.BorrowerID, alpha.ID)
	equal(t, "return", g.LoanOffer.ReturnPhase, PhaseDebt)
	equal(t, "debt kept", g.Debt.Amount, 50)
	equal(t, "log", lastLog(g), "Alpha asked Cleo for a $100 loan")
	actFails(t, g, alpha.ID, Action{Type: ActionPayDebt}, nil, phaseError)
	actFails(t, g, alpha.ID, Action{Type: ActionMortgage, SpaceIndex: 39}, nil, phaseError)

	act(t, g, cleo.ID, Action{Type: ActionAcceptLoan}, nil)
	equal(t, "back in debt", g.Phase, PhaseDebt)
	equal(t, "cash", [2]int{alpha.Cash, cleo.Cash}, [2]int{120, 1400})
	equal(t, "due", g.Loans[0].DueTurn, 2)

	// The debtor still presses pay_debt.
	act(t, g, alpha.ID, Action{Type: ActionPayDebt}, nil)
	equal(t, "cash", [2]int{alpha.Cash, sam.Cash}, [2]int{70, 1550})
	equal(t, "phase after doubles", g.Phase, PhaseAwaitingRoll)
	equal(t, "debt", g.Debt, (*Debt)(nil))
}

func TestLoanRejectAndCancel(t *testing.T) {
	g, p := loanGame(t)
	alpha, sam, cleo := p[0], p[1], p[2]

	actFails(t, g, sam.ID, Action{Type: ActionAcceptLoan}, nil, phaseError)
	actFails(t, g, sam.ID, Action{Type: ActionRejectLoan}, nil, phaseError)

	act(t, g, alpha.ID, lendAction(sam, 200, 240, 3), nil)
	actFails(t, g, cleo.ID, Action{Type: ActionRejectLoan}, nil, "The current player cannot respond to this loan offer")
	act(t, g, sam.ID, Action{Type: ActionRejectLoan}, nil)
	equal(t, "log", lastLog(g), "Sam declined the loan offer")
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
	equal(t, "offer", g.LoanOffer, (*LoanOffer)(nil))
	equal(t, "loans", len(g.Loans), 0)
	equal(t, "cash", [2]int{alpha.Cash, sam.Cash}, [2]int{1500, 1500})

	act(t, g, alpha.ID, lendAction(sam, 200, 240, 3), nil)
	act(t, g, alpha.ID, Action{Type: ActionRejectLoan}, nil)
	equal(t, "log", lastLog(g), "Alpha cancelled the loan offer")
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)

	// Declining a request made in debt returns to the debt; bankruptcy stays possible.
	g.Properties[39].OwnerID = sam.ID
	alpha.Cash = 20
	alpha.Position = 33
	act(t, g, alpha.ID, Action{Type: ActionRoll}, constant(0.34))
	act(t, g, alpha.ID, borrowAction(cleo, 100, 100, 1), nil)
	act(t, g, cleo.ID, Action{Type: ActionRejectLoan}, nil)
	equal(t, "phase", g.Phase, PhaseDebt)
	equal(t, "debt", g.Debt.PayerID, alpha.ID)
	act(t, g, alpha.ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "bankrupt", alpha.Bankrupt, true)
	equal(t, "turn", CurrentPlayer(g), sam)
}

func TestLoanOfferValidation(t *testing.T) {
	const (
		amountError = "The loan amount must be a whole number from $1 to $10000"
		repayError  = "Repayment must be a whole number from $200 to $600"
		termError   = "The loan term must be 1 to 20 turns"
	)
	type players = []*Player // Alpha (on turn), Sam, Cleo, Robo (AI)
	raw := func(format string, args ...any) func(p players) Action {
		return func(p players) Action {
			var action Action
			if err := action.UnmarshalJSON(fmt.Appendf(nil, format, args...)); err != nil {
				panic(err)
			}
			return action
		}
	}
	lendSam := func(principal, repay, dueTurns int, collateral ...int) func(p players) Action {
		return func(p players) Action { return lendAction(p[1], principal, repay, dueTurns, collateral...) }
	}
	cases := []struct {
		name   string
		setup  func(g *Game, p players)
		actor  int
		action func(p players) Action
		want   string // "" = accepted
	}{
		{"valid", nil, 0, lendSam(200, 240, 3), ""},
		{"valid maxima", func(g *Game, p players) { p[0].Cash = 10_000 }, 0, lendSam(10_000, 30_000, 20), ""},
		{"not your turn", nil, 1, func(p players) Action { return lendAction(p[2], 200, 240, 3) }, "It is not your turn yet"},
		{"no direction", nil, 0, func(p players) Action {
			action := lendAction(p[1], 200, 240, 3)
			action.Direction = ""
			return action
		}, "Choose whether to lend or borrow"},
		{"unknown direction", nil, 0, func(p players) Action {
			action := lendAction(p[1], 200, 240, 3)
			action.Direction = "gift"
			return action
		}, "Choose whether to lend or borrow"},
		{"direction not a string", nil, 0, raw(`{"type":"offer_loan","direction":5,"targetId":"x","principal":1,"repay":1,"dueTurns":1}`), "Choose whether to lend or borrow"},
		{"purchase phase", func(g *Game, p players) { g.Phase = PhaseAwaitingPurchase }, 0, lendSam(200, 240, 3), phaseError},
		{"lend while in debt", func(g *Game, p players) {
			g.Phase, g.Debt = PhaseDebt, &Debt{PayerID: p[0].ID, Amount: 5000, Reason: "test"}
		}, 0, lendSam(200, 240, 3), phaseError},
		{"request during another's debt", func(g *Game, p players) {
			g.Phase, g.Debt = PhaseDebt, &Debt{PayerID: p[1].ID, Amount: 5000, Reason: "test"}
		}, 0, func(p players) Action { return borrowAction(p[2], 200, 240, 3) }, "Only the player in debt can ask for a loan right now"},
		{"pending offer", func(g *Game, p players) {
			if err := PerformAction(g, p[0].ID, lendAction(p[2], 10, 10, 1), nil); err != nil {
				panic(err)
			}
		}, 0, lendSam(200, 240, 3), phaseError},
		{"self", nil, 0, func(p players) Action { return lendAction(p[0], 200, 240, 3) }, "Invalid loan partner"},
		{"unknown target", nil, 0, raw(`{"type":"offer_loan","direction":"lend","targetId":"nobody","principal":200,"repay":240,"dueTurns":3}`), "Invalid loan partner"},
		{"target not a string", nil, 0, raw(`{"type":"offer_loan","direction":"lend","targetId":7,"principal":200,"repay":240,"dueTurns":3}`), "Invalid loan partner"},
		{"bankrupt target", func(g *Game, p players) { p[1].Bankrupt = true }, 0, lendSam(200, 240, 3), "Invalid loan partner"},
		{"ai target", nil, 0, func(p players) Action { return lendAction(p[3], 200, 240, 3) }, ""}, // the server answers for AIs
		{"principal zero", nil, 0, lendSam(0, 0, 3), amountError},
		{"principal too big", func(g *Game, p players) { p[0].Cash = 20_000 }, 0, lendSam(10_001, 10_001, 3), amountError},
		{"principal fraction", nil, 0, func(p players) Action {
			return raw(`{"type":"offer_loan","direction":"lend","targetId":%q,"principal":1.5,"repay":2,"dueTurns":3}`, p[1].ID)(p)
		}, amountError},
		{"principal text", nil, 0, func(p players) Action {
			return raw(`{"type":"offer_loan","direction":"lend","targetId":%q,"principal":"lots","repay":2,"dueTurns":3}`, p[1].ID)(p)
		}, amountError},
		{"principal missing", nil, 0, func(p players) Action {
			return raw(`{"type":"offer_loan","direction":"lend","targetId":%q,"repay":2,"dueTurns":3}`, p[1].ID)(p)
		}, amountError},
		{"principal numeric string", nil, 0, func(p players) Action {
			return raw(`{"type":"offer_loan","direction":"lend","targetId":%q,"principal":"200","repay":240,"dueTurns":"3"}`, p[1].ID)(p)
		}, ""},
		{"repay below principal", nil, 0, lendSam(200, 199, 3), repayError},
		{"repay above triple", nil, 0, lendSam(200, 601, 3), repayError},
		{"repay missing", nil, 0, func(p players) Action {
			return raw(`{"type":"offer_loan","direction":"lend","targetId":%q,"principal":200,"dueTurns":3}`, p[1].ID)(p)
		}, repayError},
		{"term zero", nil, 0, lendSam(200, 240, 0), termError},
		{"term too long", nil, 0, lendSam(200, 240, 21), termError},
		{"collateral not a list", nil, 0, func(p players) Action {
			return raw(`{"type":"offer_loan","direction":"lend","targetId":%q,"principal":200,"repay":240,"dueTurns":3,"collateral":26}`, p[1].ID)(p)
		}, "Invalid loan collateral"},
		{"collateral not ownable", nil, 0, lendSam(200, 240, 3, 4), "Invalid loan collateral"},
		{"collateral off board", nil, 0, lendSam(200, 240, 3, 40), "Invalid loan collateral"},
		{"collateral null", nil, 0, func(p players) Action {
			return raw(`{"type":"offer_loan","direction":"lend","targetId":%q,"principal":200,"repay":240,"dueTurns":3,"collateral":null}`, p[1].ID)(p)
		}, ""},
		{"collateral not the borrower's", nil, 0, lendSam(200, 240, 3, 26), "Sea Breeze Road does not belong to Sam"},
		{"request collateral not the borrower's", func(g *Game, p players) { g.Properties[26].OwnerID = p[1].ID }, 0,
			func(p players) Action { return borrowAction(p[1], 200, 240, 3, 26) }, "Sea Breeze Road does not belong to Alpha"},
		{"collateral group has buildings", func(g *Game, p players) {
			for _, index := range []int{26, 27, 29} {
				g.Properties[index].OwnerID = p[1].ID
			}
			g.Properties[27].Houses = 1
		}, 0, lendSam(200, 240, 3, 26), "Sunshine District has buildings; sell them all first"},
		{"railroad collateral", func(g *Game, p players) { g.Properties[5].OwnerID = p[1].ID }, 0, lendSam(200, 240, 3, 5, 5), ""},
		{"collateral already pledged", func(g *Game, p players) {
			g.Properties[26].OwnerID = p[1].ID
			g.Loans = []*Loan{{ID: "loan_old", LenderID: p[2].ID, BorrowerID: p[1].ID, Principal: 1, Repay: 1,
				Collateral: []int{26}, Status: LoanActive}}
		}, 0, lendSam(200, 240, 3, 26), "Sea Breeze Road is already security for a loan"},
		{"pair has an active loan", func(g *Game, p players) {
			g.Loans = []*Loan{{ID: "loan_old", LenderID: p[1].ID, BorrowerID: p[0].ID, Principal: 1, Repay: 1,
				Collateral: []int{}, Status: LoanActive}}
		}, 0, lendSam(200, 240, 3), "Alpha and Sam already have an active loan"},
		{"pair loan closed", func(g *Game, p players) {
			g.Loans = []*Loan{{ID: "loan_old", LenderID: p[1].ID, BorrowerID: p[0].ID, Principal: 1, Repay: 1,
				Collateral: []int{}, Status: LoanRepaid}}
		}, 0, lendSam(200, 240, 3), ""},
		{"lender short of cash", func(g *Game, p players) { p[0].Cash = 150 }, 0, lendSam(200, 240, 3), "Alpha does not have enough cash"},
		{"asked lender short of cash", nil, 0, func(p players) Action { return borrowAction(p[1], 2000, 2000, 3) }, "Sam does not have enough cash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, p := loanGame(t, "Alpha", "Sam", "Cleo", "ai:Robo")
			if tc.setup != nil {
				tc.setup(g, p)
			}
			phase, version := g.Phase, g.Version
			err := PerformAction(g, p[tc.actor].ID, tc.action(p), nil)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error %q", err)
				}
				equal(t, "phase", g.Phase, PhaseLoanOffer)
				// The offer stays valid when accepted straight away.
				act(t, g, g.LoanOffer.TargetID, Action{Type: ActionAcceptLoan}, nil)
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			equal(t, "phase unchanged", g.Phase, phase)
			equal(t, "version unchanged", g.Version, version)
		})
	}

	t.Run("lender spent the cash before accepting", func(t *testing.T) {
		g, p := loanGame(t)
		act(t, g, p[0].ID, lendAction(p[1], 200, 240, 3), nil)
		p[0].Cash = 100 // e.g. changed by the server between steps
		actFails(t, g, p[1].ID, Action{Type: ActionAcceptLoan}, nil, "Alpha does not have enough cash")
		equal(t, "still pending", g.Phase, PhaseLoanOffer)
	})
}

func TestLoansDisabledByDefault(t *testing.T) {
	g, first, second := startedGame(t)
	for _, action := range []Action{
		lendAction(second, 200, 240, 3),
		{Type: ActionRepayLoan, LoanID: "loan_x"},
	} {
		actFails(t, g, first.ID, action, nil, "Loans are not enabled in this game")
	}
	actFails(t, g, second.ID, Action{Type: ActionAcceptLoan}, nil, phaseError)
	actFails(t, g, second.ID, Action{Type: ActionRejectLoan}, nil, phaseError)
	assertNoHouseRuleKeys(t, "game", jsonObject(t, g))

	lobby := newTestGame("LOBBY")
	host := mustAdd(t, lobby, "Host", "")
	actFails(t, lobby, host.ID, lendAction(host, 1, 1, 1), nil, "The game has not started")
}

func TestLoanDueAutoRepay(t *testing.T) {
	g, p := loanGame(t)
	alpha, sam := p[0], p[1]
	loan := lendAndAccept(t, g, sam, 200, 240, 1) // Sam has 0 turns: due at 1
	equal(t, "due", loan.DueTurn, 1)

	skipTurn(t, g) // Sam's first turn: not due yet
	equal(t, "turn", CurrentPlayer(g), sam)
	equal(t, "status", loan.Status, LoanActive)
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)

	skipTo(t, g, sam) // Sam has completed 1 turn
	equal(t, "status", loan.Status, LoanRepaid)
	equal(t, "cash", [2]int{alpha.Cash, sam.Cash}, [2]int{1540, 1460})
	equal(t, "logs", [2]string{g.Log[len(g.Log)-2].Text, lastLog(g)}, [2]string{"Sam's turn", "Sam repaid Alpha $240 (loan due)"})
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
	equal(t, "active", len(ActiveLoans(g)), 0)
}

func TestLoanDueCollateralDefault(t *testing.T) {
	for _, tc := range []struct {
		collateral []int
		log        string
	}{
		{[]int{26}, "Sam missed the loan deadline; Sea Breeze Road passes to Alpha"},
		{[]int{26, 27, 5}, "Sam missed the loan deadline; Sea Breeze Road, Coral Street and North Station Railroad pass to Alpha"},
	} {
		t.Run(fmt.Sprint(tc.collateral), func(t *testing.T) {
			g, p := loanGame(t)
			alpha, sam := p[0], p[1]
			for _, index := range []int{26, 27, 5} {
				g.Properties[index].OwnerID = sam.ID
			}
			g.Properties[26].Mortgaged = true
			loan := lendAndAccept(t, g, sam, 200, 400, 1, tc.collateral...)
			skipTo(t, g, sam)
			sam.Cash = 100 // spent it all
			skipTo(t, g, sam)

			equal(t, "status", loan.Status, LoanDefaulted)
			equal(t, "log", lastLog(g), tc.log)
			for _, index := range tc.collateral {
				equal(t, Board[index].Name+" owner", g.Properties[index].OwnerID, alpha.ID)
			}
			equal(t, "mortgage kept", g.Properties[26].Mortgaged, true)
			if len(tc.collateral) == 1 {
				equal(t, "unpledged stays", g.Properties[27].OwnerID, sam.ID)
			}
			equal(t, "cash", [2]int{alpha.Cash, sam.Cash}, [2]int{1300, 100})
			equal(t, "phase", g.Phase, PhaseAwaitingRoll)
			equal(t, "no debt", g.Debt, (*Debt)(nil))
			equal(t, "released", LoanSecuredBy(g, 26), (*Loan)(nil))
		})
	}
}

// overdueLoanGame leaves Sam at the start of a turn with an overdue $300
// loan from Alpha (no collateral) and only $100 cash, owning Crown Avenue.
func overdueLoanGame(t *testing.T) (*Game, []*Player, *Loan) {
	t.Helper()
	g, p := loanGame(t)
	sam := p[1]
	g.Properties[39].OwnerID = sam.ID
	loan := lendAndAccept(t, g, sam, 200, 300, 1)
	skipTo(t, g, sam)
	sam.Cash = 100
	skipTo(t, g, sam)
	return g, p, loan
}

func TestLoanDueWithoutCollateralRaisesDebt(t *testing.T) {
	g, p, loan := overdueLoanGame(t)
	alpha, sam, cleo := p[0], p[1], p[2]
	equal(t, "phase", g.Phase, PhaseDebt)
	equal(t, "debt", *g.Debt, Debt{
		PayerID: sam.ID, Amount: 300, CreditorID: alpha.ID, Reason: "loan repayment to Alpha",
		After: &DebtAfter{Kind: AfterPhase, Phase: PhaseAwaitingRoll}, LoanID: loan.ID,
	})
	equal(t, "status", loan.Status, LoanDefaulted)
	equal(t, "logs", [2]string{g.Log[len(g.Log)-2].Text, lastLog(g)},
		[2]string{"Sam missed the loan deadline", "Sam must raise $300 to pay loan repayment to Alpha"})
	debtJSON := jsonObject(t, g)["debt"].(map[string]any)
	equal(t, "json loanId", debtJSON["loanId"], any(loan.ID))
	equal(t, "json creditor", debtJSON["creditorId"], any(alpha.ID))
	actFails(t, g, sam.ID, Action{Type: ActionRepayLoan, LoanID: loan.ID}, nil, phaseError)
	// The defaulted loan is closed, so Sam may ask anyone (even Alpha) for a new loan.
	act(t, g, sam.ID, borrowAction(cleo, 300, 300, 5), nil)
	equal(t, "return", g.LoanOffer.ReturnPhase, PhaseDebt)
	act(t, g, sam.ID, Action{Type: ActionRejectLoan}, nil)
	equal(t, "back in debt", g.Phase, PhaseDebt)
	equal(t, "debt kept", g.Debt.LoanID, loan.ID)

	t.Run("raise cash and pay", func(t *testing.T) {
		g, p, _ := overdueLoanGame(t)
		alpha, sam := p[0], p[1]
		act(t, g, sam.ID, Action{Type: ActionMortgage, SpaceIndex: 39}, nil)
		act(t, g, sam.ID, Action{Type: ActionPayDebt}, nil)
		equal(t, "cash", [2]int{alpha.Cash, sam.Cash}, [2]int{1600, 0})
		equal(t, "log", lastLog(g), "Sam paid $300 (loan repayment to Alpha)")
		equal(t, "phase", g.Phase, PhaseAwaitingRoll)
		act(t, g, sam.ID, Action{Type: ActionRoll}, sequence(0, 0.2))
	})

	t.Run("bankrupt to the lender", func(t *testing.T) {
		g, p, loan := overdueLoanGame(t)
		alpha, sam, cleo := p[0], p[1], p[2]
		act(t, g, sam.ID, Action{Type: ActionBankrupt}, nil)
		equal(t, "creditor", g.LastBankruptcy.CreditorID, alpha.ID)
		equal(t, "property", g.Properties[39].OwnerID, alpha.ID)
		equal(t, "cash", alpha.Cash, 1300+100)
		equal(t, "status", loan.Status, LoanDefaulted)
		equal(t, "turn", CurrentPlayer(g), cleo)
		equal(t, "status", g.Status, StatusPlaying)
	})
}

func TestSeveralDueLoansSettleInOrder(t *testing.T) {
	g, p := loanGame(t)
	alpha, sam, cleo := p[0], p[1], p[2]
	fromAlpha := lendAndAccept(t, g, sam, 300, 300, 2) // due at 2
	skipTo(t, g, cleo)                                 // Sam has completed 1 turn
	fromCleo := lendAndAccept(t, g, sam, 100, 100, 1)  // due at 2 as well
	equal(t, "due", [2]int{fromAlpha.DueTurn, fromCleo.DueTurn}, [2]int{2, 2})
	g.Properties[37].OwnerID = sam.ID
	g.Properties[39].OwnerID = sam.ID
	skipTo(t, g, sam) // Sam's second turn: nothing due yet
	equal(t, "not due", [2]string{fromAlpha.Status, fromCleo.Status}, [2]string{LoanActive, LoanActive})
	sam.Cash = 50
	skipTo(t, g, sam) // Sam has completed 2 turns: both due

	equal(t, "debt", g.Debt.LoanID, fromAlpha.ID)
	equal(t, "cleo's loan waits", fromCleo.Status, LoanActive)
	act(t, g, sam.ID, Action{Type: ActionMortgage, SpaceIndex: 39}, nil) // +200
	act(t, g, sam.ID, Action{Type: ActionMortgage, SpaceIndex: 37}, nil) // +175
	act(t, g, sam.ID, Action{Type: ActionPayDebt}, nil)
	equal(t, "cleo repaid", fromCleo.Status, LoanRepaid)
	equal(t, "log", lastLog(g), "Sam repaid Cleo $100 (loan due)")
	equal(t, "cash", [3]int{alpha.Cash, sam.Cash, cleo.Cash}, [3]int{1500, 25, 1500})
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
}

func TestRepayLoanEarly(t *testing.T) {
	g, p := loanGame(t)
	alpha, sam, cleo := p[0], p[1], p[2]
	loan := lendAndAccept(t, g, sam, 200, 240, 5)
	repay := Action{Type: ActionRepayLoan, LoanID: loan.ID}

	actFails(t, g, sam.ID, repay, nil, "It is not your turn yet")
	skipTurn(t, g)
	actFails(t, g, sam.ID, Action{Type: ActionRepayLoan, LoanID: "loan_missing"}, nil, "Loan not found")
	actFails(t, g, sam.ID, decodeAction(t, `{"type":"repay_loan"}`), nil, "Loan not found")
	g.Loans = append(g.Loans, &Loan{ID: "loan_other", LenderID: sam.ID, BorrowerID: cleo.ID, Principal: 1, Repay: 1,
		Collateral: []int{}, Status: LoanActive})
	actFails(t, g, sam.ID, Action{Type: ActionRepayLoan, LoanID: "loan_other"}, nil, "Only the borrower can repay this loan")
	g.Loans[len(g.Loans)-1].Status = LoanCancelled
	sam.Cash = 239
	actFails(t, g, sam.ID, repay, nil, "Not enough cash to repay this loan")
	sam.Cash = 1700
	g.Phase, g.Debt = PhaseDebt, &Debt{PayerID: sam.ID, Amount: 5000, Reason: "test"}
	actFails(t, g, sam.ID, repay, nil, phaseError)
	g.Phase, g.Debt = PhaseAwaitingRoll, nil

	act(t, g, sam.ID, decodeAction(t, fmt.Sprintf(`{"type":"repay_loan","loanId":%q}`, loan.ID)), nil)
	equal(t, "status", loan.Status, LoanRepaid)
	equal(t, "cash", [2]int{alpha.Cash, sam.Cash}, [2]int{1540, 1460})
	equal(t, "log", lastLog(g), "Sam repaid Alpha $240")
	actFails(t, g, sam.ID, repay, nil, "This loan is no longer active")

	// A repaid loan no longer blocks the pair.
	act(t, g, sam.ID, lendAction(alpha, 100, 100, 1), nil)
	act(t, g, alpha.ID, Action{Type: ActionRejectLoan}, nil)

	// Repaying works while a purchase is pending; offering a loan does not.
	other := lendAndAccept(t, g, cleo, 50, 60, 5)
	act(t, g, sam.ID, Action{Type: ActionRoll}, sequence(0, 0.2)) // Maple Bridge Street
	equal(t, "phase", g.Phase, PhaseAwaitingPurchase)
	actFails(t, g, sam.ID, lendAction(alpha, 100, 100, 1), nil, phaseError)
	skipTo(t, g, cleo)
	act(t, g, cleo.ID, Action{Type: ActionRoll}, sequence(0, 0.2))
	equal(t, "phase", g.Phase, PhaseAwaitingPurchase)
	act(t, g, cleo.ID, Action{Type: ActionRepayLoan, LoanID: other.ID}, nil)
	equal(t, "other repaid", other.Status, LoanRepaid)
}

func TestCollateralBlocksTradesAndBuilding(t *testing.T) {
	g, p := loanGame(t)
	alpha, sam, cleo := p[0], p[1], p[2]
	for _, index := range []int{26, 27, 29} {
		g.Properties[index].OwnerID = sam.ID
	}
	loan := lendAndAccept(t, g, sam, 200, 240, 5, 26)
	equal(t, "secured", LoanSecuredBy(g, 26), loan)
	equal(t, "not secured", LoanSecuredBy(g, 27), (*Loan)(nil))

	actFails(t, g, alpha.ID, Action{Type: ActionOfferTrade, TargetID: sam.ID,
		Offer: &TradeBundle{Cash: 500}, Request: &TradeBundle{Properties: []int{26}}}, nil, "Sea Breeze Road is security for a loan")
	skipTurn(t, g)
	actFails(t, g, sam.ID, Action{Type: ActionOfferTrade, TargetID: cleo.ID,
		Offer: &TradeBundle{Properties: []int{27, 26}}, Request: &TradeBundle{Cash: 100}}, nil, "Sea Breeze Road is security for a loan")
	actFails(t, g, sam.ID, Action{Type: ActionBuild, SpaceIndex: 29}, nil, "Sea Breeze Road is security for a loan")

	// Unpledged property trades as usual; pledged property can't be mortgaged
	// (it must reach the lender as pledged, review B14).
	act(t, g, sam.ID, Action{Type: ActionOfferTrade, TargetID: cleo.ID,
		Offer: &TradeBundle{Properties: []int{27}}, Request: &TradeBundle{Cash: 100}}, nil)
	act(t, g, cleo.ID, Action{Type: ActionRejectTrade}, nil)
	actFails(t, g, sam.ID, Action{Type: ActionMortgage, SpaceIndex: 26}, nil, "Sea Breeze Road is security for a loan")

	act(t, g, sam.ID, Action{Type: ActionRepayLoan, LoanID: loan.ID}, nil)
	act(t, g, sam.ID, Action{Type: ActionMortgage, SpaceIndex: 26}, nil)
	act(t, g, sam.ID, Action{Type: ActionUnmortgage, SpaceIndex: 26}, nil)
	act(t, g, sam.ID, Action{Type: ActionBuild, SpaceIndex: 29}, nil)
	equal(t, "houses", g.Properties[29].Houses, 1)
	act(t, g, sam.ID, Action{Type: ActionSellBuilding, SpaceIndex: 29}, nil)
	act(t, g, sam.ID, Action{Type: ActionOfferTrade, TargetID: cleo.ID,
		Offer: &TradeBundle{Properties: []int{26}}, Request: &TradeBundle{Cash: 100}}, nil)
	equal(t, "phase", g.Phase, PhaseTrade)
}

func TestLenderBankruptcyCancelsLoans(t *testing.T) {
	g, p := loanGame(t)
	alpha, sam, cleo := p[0], p[1], p[2]
	g.Properties[26].OwnerID = sam.ID
	toSam := lendAndAccept(t, g, sam, 200, 240, 5, 26)
	skipTo(t, g, cleo)
	fromCleo := lendAndAccept(t, g, alpha, 100, 150, 5)
	skipTurn(t, g) // Alpha's turn

	alpha.Cash = 50
	alpha.Position = 35
	act(t, g, alpha.ID, Action{Type: ActionRoll}, sequence(0, 0.2)) // Council Tax, $100
	equal(t, "phase", g.Phase, PhaseDebt)
	act(t, g, alpha.ID, Action{Type: ActionBankrupt}, nil)

	equal(t, "lender's loan", toSam.Status, LoanCancelled)
	equal(t, "borrower's loan", fromCleo.Status, LoanDefaulted)
	assertLog(t, g, "Alpha's loan to Sam was cancelled; Sam keeps the money")
	assertLog(t, g, "Alpha's loan from Cleo was written off")
	equal(t, "sam keeps", [2]any{sam.Cash, g.Properties[26].OwnerID}, [2]any{1700, sam.ID})
	equal(t, "cleo not paid", cleo.Cash, 1400)
	equal(t, "released", LoanSecuredBy(g, 26), (*Loan)(nil))
	equal(t, "turn", CurrentPlayer(g), sam)
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)

	// Nothing is collected when the cancelled loan would have been due.
	for range 6 {
		skipTurn(t, g)
	}
	equal(t, "sam cash", sam.Cash, 1700)
}

func TestBorrowerBankruptcyWritesOffLoan(t *testing.T) {
	g, p := loanGame(t)
	alpha, sam, cleo := p[0], p[1], p[2]
	g.Properties[26].OwnerID = sam.ID
	loan := lendAndAccept(t, g, sam, 200, 240, 5, 26)
	skipTurn(t, g)

	g.Properties[37].OwnerID = cleo.ID
	g.Properties[39].OwnerID = cleo.ID
	g.Properties[39].Houses = 5 // hotel: $2000
	sam.Position = 33
	act(t, g, sam.ID, Action{Type: ActionRoll}, constant(0.34))
	equal(t, "phase", g.Phase, PhaseDebt)
	actFails(t, g, sam.ID, borrowAction(alpha, 300, 300, 5), nil, "Alpha and Sam already have an active loan")
	act(t, g, sam.ID, Action{Type: ActionBankrupt}, nil)

	equal(t, "status", loan.Status, LoanDefaulted)
	assertLog(t, g, "Sam's loan from Alpha was written off")
	equal(t, "collateral goes to the creditor", g.Properties[26].OwnerID, cleo.ID)
	equal(t, "lender unpaid", alpha.Cash, 1300)
	equal(t, "creditor", g.LastBankruptcy.CreditorID, cleo.ID)
}

func TestLoanActionJSON(t *testing.T) {
	encoded, err := lendAction(&Player{ID: "player_1"}, 200, 240, 3, 26).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "offer", string(encoded),
		`{"collateral":[26],"direction":"lend","dueTurns":3,"principal":200,"repay":240,"targetId":"player_1","type":"offer_loan"}`)
	encoded, _ = Action{Type: ActionRepayLoan, LoanID: "loan_1"}.MarshalJSON()
	equal(t, "repay", string(encoded), `{"loanId":"loan_1","type":"repay_loan"}`)
	encoded, _ = Action{Type: ActionAcceptLoan}.MarshalJSON()
	equal(t, "accept", string(encoded), `{"type":"accept_loan"}`)

	decoded := decodeAction(t, `{"type":"offer_loan","direction":"borrow","targetId":"p","principal":"200","repay":240,"dueTurns":3,"collateral":[26,"27",99]}`)
	equal(t, "typed", [6]any{decoded.Direction, decoded.TargetID, decoded.Principal, decoded.Repay, decoded.DueTurns, decoded.Collateral},
		[6]any{"borrow", "p", 200, 240, 3, []int{26, 27}})
	equal(t, "typed loanId", decodeAction(t, `{"type":"repay_loan","loanId":"loan_9"}`).LoanID, "loan_9")
}

func TestRefusedAITradeForCollateralCountsAsAttempt(t *testing.T) {
	g, p := loanGame(t, "Alpha", "ai:Robo", "Sam")
	alpha, robo, sam := p[0], p[1], p[2]
	g.Properties[26].OwnerID = sam.ID
	lendAndAccept(t, g, sam, 200, 240, 5, 26)
	skipTurn(t, g) // Robo's turn

	// Other refusals keep the JS behaviour (the AI may try another trade).
	actFails(t, g, robo.ID, Action{Type: ActionOfferTrade, TargetID: sam.ID,
		Offer: &TradeBundle{Cash: 5000}, Request: &TradeBundle{Properties: []int{27}}}, nil, "Coral Street no longer belongs to Sam")
	equal(t, "attempt after other error", g.Turn.AITradeAttempted, false)

	actFails(t, g, robo.ID, Action{Type: ActionOfferTrade, TargetID: sam.ID,
		Offer: &TradeBundle{Cash: 500}, Request: &TradeBundle{Properties: []int{26}}}, nil, "Sea Breeze Road is security for a loan")
	equal(t, "attempt after collateral refusal", g.Turn.AITradeAttempted, true)

	// Humans are unaffected.
	skipTo(t, g, alpha)
	actFails(t, g, alpha.ID, Action{Type: ActionOfferTrade, TargetID: sam.ID,
		Offer: &TradeBundle{Cash: 500}, Request: &TradeBundle{Properties: []int{26}}}, nil, "Sea Breeze Road is security for a loan")
	equal(t, "human", g.Turn.AITradeAttempted, false)
}
