package engine

import "errors"

// charge makes payer pay amount to creditorID ("" = bank). If the payer has
// enough cash the payment happens immediately and play resumes per after;
// otherwise the game enters the debt phase. It reports whether it was paid.
func charge(g *Game, payer *Player, amount int, creditorID, reason string, after *DebtAfter) bool {
	roundedAmount := max(0, amount) // amounts are always integers, so Math.round is the identity
	if payer.Cash >= roundedAmount {
		payer.Cash -= roundedAmount
		if creditorID != "" {
			if creditor := g.PlayerByID(creditorID); creditor != nil && !creditor.Bankrupt {
				creditor.Cash += roundedAmount
			}
		}
		payToJackpot(g, creditorID, roundedAmount) // house rule; no-op by default
		suffix := ""
		if reason != "" {
			suffix = " (" + reason + ")"
		}
		addLog(g, payer.Name+" paid "+money(roundedAmount)+suffix)
		resumeAfterDebt(g, payer, after)
		return true
	}
	g.Debt = &Debt{PayerID: payer.ID, Amount: roundedAmount, CreditorID: creditorID, Reason: reason, After: after}
	g.Phase = PhaseDebt
	addLog(g, payer.Name+" must raise "+money(roundedAmount)+" to pay "+reason)
	return false
}

func payDebt(g *Game, playerID string) error {
	if err := assertPhase(g, PhaseDebt); err != nil {
		return err
	}
	if g.Debt == nil || g.Debt.PayerID != playerID {
		return errors.New("This debt belongs to another player")
	}
	player := g.PlayerByID(playerID)
	if player.Cash < g.Debt.Amount {
		return errors.New("Not enough cash; sell buildings or mortgage properties")
	}
	debt := g.Debt
	player.Cash -= debt.Amount
	if debt.CreditorID != "" {
		if creditor := g.PlayerByID(debt.CreditorID); creditor != nil && !creditor.Bankrupt {
			creditor.Cash += debt.Amount
		}
	}
	payToJackpot(g, debt.CreditorID, debt.Amount) // house rule; no-op by default
	g.Debt = nil
	addLog(g, player.Name+" paid "+money(debt.Amount)+" ("+debt.Reason+")")
	resumeAfterDebt(g, player, debt.After)
	loanDebtPaid(g, player, debt) // house rule; no-op unless debt.LoanID is set
	return nil
}

func resumeAfterDebt(g *Game, player *Player, after *DebtAfter) {
	if passTurnOfResigned(g) {
		return // the player whose turn it was resigned meanwhile
	}
	if after == nil || after.Kind == AfterPhase {
		if after != nil && after.Phase != "" {
			g.Phase = after.Phase
		} else {
			g.Phase = postLandingPhase(g)
		}
		return
	}
	switch after.Kind {
	case AfterLeaveJail:
		player.InJail = false
		player.JailTurns = 0
		g.Phase = PhaseAwaitingRoll
		addLog(g, player.Name+" paid bail and left jail")
	case AfterJailMove:
		player.InJail = false
		player.JailTurns = 0
		g.Turn.ExtraRoll = false
		total := after.Dice[0] + after.Dice[1]
		addLog(g, player.Name+" paid bail, left jail, and moved "+itoa(total)+" spaces")
		movePlayer(g, player, total, true)
		resolveLanding(g, player, rentOptions{})
	}
}

// declareBankruptcy handles the bankrupt action. A player in the debt phase
// who cannot pay goes bankrupt to their creditor (or the bank). A person
// (not a computer) may also declare bankruptcy, that is resign, at any time
// in play (0.10.1): in the debt phase they owe, everything goes to the
// creditor as above; otherwise it goes to the bank (resign). Computers keep
// the old rule: only in their own debt phase, and only when they can't pay.
func declareBankruptcy(g *Game, playerID string) error {
	player := g.PlayerByID(playerID)
	owes := g.Debt != nil && g.Debt.PayerID == playerID && (g.Phase == PhaseDebt || g.Phase == PhaseLoanOffer)
	person := player != nil && player.Kind == KindHuman && !g.legacyRules
	if person && !owes {
		return resign(g, player)
	}
	if person && g.Phase == PhaseLoanOffer && g.LoanOffer != nil {
		// Asking for a loan to pay this debt: the request goes, the debt stays.
		o := g.LoanOffer
		g.LoanOffer = nil
		g.Phase = o.ReturnPhase
		addLog(g, player.Name+" cancelled the loan offer")
	}
	if err := assertPhase(g, PhaseDebt); err != nil {
		return err
	}
	if g.Debt == nil || g.Debt.PayerID != playerID {
		return errors.New("Bankruptcy cannot be declared right now")
	}
	if player.Cash >= g.Debt.Amount && !g.legacyRules && !person {
		// A computer able to pay must pay (going bankrupt would just hand
		// everything to the creditor); a person may still resign.
		return errors.New("You have enough cash to pay this debt")
	}
	var creditor *Player
	if g.Debt.CreditorID != "" {
		for _, candidate := range g.Players {
			if candidate.ID == g.Debt.CreditorID && !candidate.Bankrupt {
				creditor = candidate
				break
			}
		}
	}
	goBankrupt(g, player, creditor, true)
	if finishIfOneLeft(g) {
		return nil
	}
	advanceTurn(g)
	return nil
}

// goBankrupt hands player's assets to creditor (nil: the bank) and takes
// them out of the game. clearDebt drops the pending debt (theirs).
func goBankrupt(g *Game, player *Player, creditor *Player, clearDebt bool) {
	creditorID := ""
	if creditor != nil {
		creditorID = creditor.ID
	}
	transferred := []int{}
	for _, index := range OwnableIndexes {
		if g.Properties[index].OwnerID == player.ID {
			transferred = append(transferred, index)
		}
	}

	for _, index := range transferred {
		state := g.Properties[index]
		space := &Board[index]
		if space.Type == SpaceProperty && state.Houses > 0 {
			player.Cash += (space.BuildCost * state.Houses) / 2
			state.Houses = 0
		}
		state.OwnerID = creditorID
		if creditor == nil {
			state.Mortgaged = false
		}
	}
	if creditor != nil {
		creditor.Cash += player.Cash
	}
	if player.JailCards > 0 && !g.legacyRules {
		// Jail pass cards go to the creditor, or back to the decks.
		if creditor != nil {
			creditor.JailCards += player.JailCards
		} else {
			for range player.JailCards {
				returnJailCard(g)
			}
		}
		player.JailCards = 0
	}
	player.Cash = 0
	player.Bankrupt = true
	player.InJail = false
	if clearDebt {
		g.Debt = nil
	}
	g.LastBankruptcy = &Bankruptcy{
		ID:         g.uid("bankruptcy"),
		PlayerID:   player.ID,
		CreditorID: creditorID,
		Properties: transferred,
		At:         g.now(),
	}
	if player.Left {
		addLog(g, player.Name+" left the game; assets returned to the bank")
	} else if creditor != nil {
		addLog(g, player.Name+" declared bankruptcy; assets transferred to "+creditor.Name)
	} else {
		addLog(g, player.Name+" declared bankruptcy; assets returned to the bank")
	}
	closeLoansOnBankruptcy(g, player) // house rule; no-op without loans
}

// finishIfOneLeft ends the game when one player is left in it.
func finishIfOneLeft(g *Game) bool {
	remaining := activePlayers(g)
	if len(remaining) != 1 {
		return false
	}
	completeTurnSummary(g)
	g.Status = StatusFinished
	g.Phase = PhaseFinished
	g.WinnerID = remaining[0].ID
	addLog(g, remaining[0].Name+" wins the game")
	return true
}

// resign takes a person out of the game at any time in play: whatever they
// are part of is cancelled (a trade or loan offer) or left (an auction),
// their assets go to the bank, and if it was their turn it passes on.
func resign(g *Game, player *Player) error {
	if player.Bankrupt {
		return errors.New("You are already out of the game")
	}
	if g.Phase == PhaseDeterminingOrder || g.Phase == PhaseOrderCountdown {
		return errors.New("Wait until the turn order is set")
	}
	if g.Phase == PhaseFinished || g.Status != StatusPlaying {
		return errors.New("The game is not in progress")
	}
	current := CurrentPlayer(g)
	mine := current != nil && current.ID == player.ID
	if t := g.Trade; t != nil && (t.ProposerID == player.ID || t.TargetID == player.ID) {
		g.Trade = nil
		if g.Phase == PhaseTrade {
			g.Phase = t.ReturnPhase
		}
		addLog(g, player.Name+" cancelled the trade")
	}
	if o := g.LoanOffer; o != nil && (o.ProposerID == player.ID || o.TargetID == player.ID) {
		g.LoanOffer = nil
		if g.Phase == PhaseLoanOffer {
			g.Phase = o.ReturnPhase
		}
		addLog(g, player.Name+" cancelled the loan offer")
	}
	if mine {
		// Their own landing: no purchase, no card left to confirm.
		g.PendingPurchase = nil
		g.PendingCard = nil
	}
	goBankrupt(g, player, nil, false)
	if finishIfOneLeft(g) {
		g.Auction, g.Trade, g.LoanOffer, g.Debt = nil, nil, nil, nil
		return nil
	}
	if g.Phase == PhaseAuction && g.Auction != nil {
		leaveAuction(g, player)
	}
	// Their turn passes on, unless an auction or another player's debt is
	// still to finish (they hand the turn on when they end).
	if cur := CurrentPlayer(g); cur != nil && cur.ID == player.ID && g.Phase != PhaseAuction && g.Phase != PhaseDebt {
		advanceTurn(g)
	}
	return nil
}

// leaveAuction takes a player out of the auction in progress; a leading bid
// of theirs is withdrawn.
func leaveAuction(g *Game, player *Player) {
	a := g.Auction
	for i, id := range a.ActiveIDs {
		if id == player.ID {
			a.ActiveIDs = append(a.ActiveIDs[:i:i], a.ActiveIDs[i+1:]...)
			if i < a.TurnPos {
				a.TurnPos--
			}
			break
		}
	}
	if len(a.ActiveIDs) > 0 {
		a.TurnPos %= len(a.ActiveIDs)
	}
	if a.BidderID == player.ID {
		a.BidderID, a.CurrentBid = "", 0
		addLog(g, player.Name+"'s bid was withdrawn")
		if a.Timed() {
			// The others get a full clock to bid again.
			a.EndsAt = g.now() + int64(a.ClockMs)
		}
	}
	if a.Bids != nil {
		delete(a.Bids, player.ID)
		if len(a.Bids) == 0 {
			a.Bids = nil
		}
	}
	settleAuctionIfReady(g)
}

// passTurnOfResigned hands the turn on when the player whose turn it is
// resigned while an auction or another player's debt was still open (resign).
// Only a person's resignation leaves a bankrupt current player, so games
// without one are unchanged.
func passTurnOfResigned(g *Game) bool {
	cur := CurrentPlayer(g)
	if cur == nil || !cur.Bankrupt || g.Status != StatusPlaying {
		return false
	}
	advanceTurn(g)
	return true
}
