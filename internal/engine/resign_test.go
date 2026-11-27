package engine

import "testing"

// A person may declare bankruptcy (resign) at any time in play, with cash:
// everything goes to the bank and their turn passes on.
func TestResignWithCashOnOwnTurn(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, b := p[0], p[1]
	g.Properties[1].OwnerID, g.Properties[3].OwnerID = a.ID, a.ID
	g.Properties[3].Houses, g.Properties[1].Houses = 1, 1
	g.Properties[5].OwnerID, g.Properties[5].Mortgaged = a.ID, true
	a.JailCards = 1
	equal(t, "a's turn", CurrentPlayer(g).ID, a.ID)
	act(t, g, a.ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "out", a.Bankrupt, true)
	equal(t, "cash", a.Cash, 0)
	for _, i := range []int{1, 3, 5} {
		st := g.Properties[i]
		equal(t, "back to the bank", [3]any{st.OwnerID, st.Houses, st.Mortgaged}, [3]any{"", 0, false})
	}
	equal(t, "jail card returned", a.JailCards, 0)
	equal(t, "turn passed", CurrentPlayer(g).ID, b.ID)
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
	equal(t, "to the bank", g.LastBankruptcy.CreditorID, "")
	if !hasLog(g, "A declared bankruptcy; assets returned to the bank") {
		t.Fatal("no bankruptcy log line")
	}
	actFails(t, g, a.ID, Action{Type: ActionBankrupt}, nil, "already out")
}

// During another player's turn: the turn stays with them.
func TestResignDuringAnotherTurn(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, c := p[0], p[2]
	act(t, g, c.ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "c out", c.Bankrupt, true)
	equal(t, "still a's turn", [2]string{CurrentPlayer(g).ID, g.Phase}, [2]string{a.ID, PhaseAwaitingRoll})
	// The last one standing wins.
	act(t, g, p[1].ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "finished", [3]string{g.Status, g.Phase, g.WinnerID}, [3]string{StatusFinished, PhaseFinished, a.ID})
}

// Mid-auction: the person leaves it (a leading bid of theirs is withdrawn);
// if it was their landing, the turn passes on when the auction ends.
func TestResignMidAuction(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, b, c := p[0], p[1], p[2]
	a.Position = 36
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 2)) // Crown Avenue (39), unowned
	equal(t, "buy?", g.Phase, PhaseAwaitingPurchase)
	act(t, g, a.ID, Action{Type: ActionDecline}, nil)
	equal(t, "auction", g.Phase, PhaseAuction)
	// B bids first, then C resigns (not the leader), then A (the lander) resigns.
	act(t, g, b.ID, Action{Type: ActionBid, Amount: 100}, nil)
	act(t, g, c.ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "still an auction", g.Phase, PhaseAuction)
	equal(t, "bidders", g.Auction.ActiveIDs, []string{b.ID, a.ID})
	act(t, g, a.ID, Action{Type: ActionBankrupt}, nil)
	// B alone with the lead: B wins, and with A and C out the game is over.
	equal(t, "winner", [2]string{g.Status, g.WinnerID}, [2]string{StatusFinished, b.ID})
}

func TestResignWithdrawsLeadingBid(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C", "D")
	a, b, c, d := p[0], p[1], p[2], p[3]
	a.Position = 36
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 2))
	act(t, g, a.ID, Action{Type: ActionDecline}, nil)
	act(t, g, b.ID, Action{Type: ActionBid, Amount: 100}, nil)
	act(t, g, b.ID, Action{Type: ActionBankrupt}, nil) // B leads, then resigns
	equal(t, "bid withdrawn", [2]any{g.Auction.BidderID, g.Auction.CurrentBid}, [2]any{"", 0})
	act(t, g, c.ID, Action{Type: ActionBid, Amount: 10}, nil)
	act(t, g, d.ID, Action{Type: ActionPassAuction}, nil)
	act(t, g, a.ID, Action{Type: ActionPassAuction}, nil)
	equal(t, "c won", g.Properties[39].OwnerID, c.ID)
	equal(t, "a's turn goes on", [2]string{CurrentPlayer(g).ID, g.Phase}, [2]string{a.ID, PhaseTurnComplete})
}

// The lander resigns mid-auction and the others finish it; then the turn
// passes on.
func TestResignLanderMidAuctionTurnPasses(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C", "D")
	a, b, c, d := p[0], p[1], p[2], p[3]
	a.Position = 36
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 2))
	act(t, g, a.ID, Action{Type: ActionDecline}, nil)
	act(t, g, a.ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "auction goes on", g.Phase, PhaseAuction)
	act(t, g, b.ID, Action{Type: ActionBid, Amount: 50}, nil)
	act(t, g, c.ID, Action{Type: ActionPassAuction}, nil)
	act(t, g, d.ID, Action{Type: ActionPassAuction}, nil)
	equal(t, "b won", g.Properties[39].OwnerID, b.ID)
	equal(t, "b's turn", [2]string{CurrentPlayer(g).ID, g.Phase}, [2]string{b.ID, PhaseAwaitingRoll})
}

// A pending trade the person is part of is cancelled, whoever proposed it.
func TestResignWithPendingTrade(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, b := p[0], p[1]
	g.Properties[39].OwnerID = b.ID
	act(t, g, a.ID, Action{Type: ActionOfferTrade, TargetID: b.ID, Offer: &TradeBundle{Cash: 300}, Request: &TradeBundle{Properties: []int{39}}}, nil)
	equal(t, "trade", g.Phase, PhaseTrade)
	act(t, g, b.ID, Action{Type: ActionBankrupt}, nil) // the target resigns
	equal(t, "cancelled", [3]any{g.Trade == nil, g.Phase, CurrentPlayer(g).ID}, [3]any{true, PhaseAwaitingRoll, a.ID})
	equal(t, "39 to the bank", g.Properties[39].OwnerID, "")

	g2, p2 := seatedGame(t, "A", "B", "C")
	g2.Properties[39].OwnerID = p2[1].ID
	act(t, g2, p2[0].ID, Action{Type: ActionOfferTrade, TargetID: p2[1].ID, Offer: &TradeBundle{Cash: 300}, Request: &TradeBundle{Properties: []int{39}}}, nil)
	act(t, g2, p2[0].ID, Action{Type: ActionBankrupt}, nil) // the proposer, on turn, resigns
	equal(t, "cancelled, turn passed", [3]any{g2.Trade == nil, g2.Phase, CurrentPlayer(g2).ID}, [3]any{true, PhaseAwaitingRoll, p2[1].ID})
}

// In a debt to another player the creditor gets everything (the existing
// rule), even with enough cash; computers keep the old rule.
func TestResignInDebtGoesToCreditor(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, b := p[0], p[1]
	g.Properties[39].OwnerID = b.ID
	g.Properties[31].OwnerID = a.ID
	a.Cash = 20
	a.Position = 36
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 2)) // rent $50 to B
	equal(t, "debt", g.Phase, PhaseDebt)
	act(t, g, a.ID, Action{Type: ActionMortgage, SpaceIndex: 31}, nil) // now cash covers it
	act(t, g, a.ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "to b", [3]any{a.Bankrupt, g.Properties[31].OwnerID, g.LastBankruptcy.CreditorID}, [3]any{true, b.ID, b.ID})
	equal(t, "b got the cash", b.Cash, 1500+20+150)
	equal(t, "turn passed", CurrentPlayer(g).ID, b.ID)
}

// A debt to the bank: everything goes to the bank, as before.
func TestResignInBankDebt(t *testing.T) {
	g, p := seatedGame(t, "A", "B")
	a := p[0]
	a.Cash = 100
	a.Position = 1
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 2)) // Stamp Duty $200
	equal(t, "debt", g.Phase, PhaseDebt)
	act(t, g, a.ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "bank", [3]any{a.Bankrupt, g.LastBankruptcy.CreditorID, g.Status}, [3]any{true, "", StatusFinished})
}

// Card confirmation and a purchase on their own landing are dropped.
func TestResignWithPendingPurchase(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, b := p[0], p[1]
	a.Position = 36
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 2))
	equal(t, "buy?", g.Phase, PhaseAwaitingPurchase)
	act(t, g, a.ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "nothing pending", [3]any{g.PendingPurchase == nil, g.Properties[39].OwnerID, CurrentPlayer(g).ID}, [3]any{true, "", b.ID})
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
}

// Not before the turn order is set.
func TestResignWaitsForTurnOrder(t *testing.T) {
	g := newTestGame("ORDER")
	first := mustAdd(t, g, "Player A", "")
	mustAdd(t, g, "Player B", "")
	mustAdd(t, g, "Player C", "")
	act(t, g, first.ID, Action{Type: ActionStart}, constant(0.5))
	actFails(t, g, first.ID, Action{Type: ActionBankrupt}, nil, "turn order")
}

// Computers still go bankrupt only in their own debt phase.
func TestComputerBankruptcyUnchanged(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	p[1].Kind = KindAI
	actFails(t, g, p[1].ID, Action{Type: ActionBankrupt}, nil, "current phase")
}
