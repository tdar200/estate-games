package engine

import "errors"

// Ending a game early (an Estate addition, not in src/game.js): the host can
// stop a game in progress, and the player with the highest net worth wins.

// NetWorth is a player's cash, plus each deed at its price (half when
// mortgaged), plus buildings at cost (a hotel counts as five houses), plus
// active loans they are owed, minus active loans they owe.
func NetWorth(g *Game, playerID string) int {
	p := g.PlayerByID(playerID)
	if p == nil {
		return 0
	}
	worth := p.Cash
	for index, state := range g.Properties {
		if state == nil || state.OwnerID != playerID || index < 0 || index >= len(Board) {
			continue
		}
		space := Board[index]
		if state.Mortgaged {
			worth += space.Price / 2
		} else {
			worth += space.Price
		}
		worth += state.Houses * space.BuildCost
	}
	for _, loan := range g.Loans {
		if loan == nil || loan.Status != "active" {
			continue
		}
		if loan.LenderID == playerID {
			worth += loan.Repay
		}
		if loan.BorrowerID == playerID {
			worth -= loan.Repay
		}
	}
	return worth
}

// EndGame finishes a game in progress at once. The richest player still in
// wins (ties go to the earlier seat), scored by FinalScore so an unpaid debt
// counts against the debtor. Anything pending (a purchase, auction, debt,
// card, trade or loan offer) is then dropped.
func EndGame(g *Game) (*Player, error) {
	if g.Status != StatusPlaying {
		return nil, errors.New("The game is not in progress")
	}
	var winner *Player
	best := 0
	for _, p := range g.Players {
		if p == nil || p.Bankrupt {
			continue
		}
		if worth := FinalScore(g, p.ID); winner == nil || worth > best {
			winner, best = p, worth
		}
	}
	if winner == nil {
		return nil, errors.New("No players are left in the game")
	}
	debtNote := ""
	if debt := g.Debt; debt != nil && debt.Amount > 0 {
		if payer := g.PlayerByID(debt.PayerID); payer != nil && !payer.Bankrupt {
			to := "the bank"
			if creditor := g.PlayerByID(debt.CreditorID); creditor != nil && !creditor.Bankrupt {
				to = creditor.Name
			}
			debtNote = payer.Name + "'s unpaid " + money(debt.Amount) + " to " + to + " was counted"
		}
	}
	g.PendingPurchase = nil
	g.Auction = nil
	g.Debt = nil
	g.PendingCard = nil
	g.Trade = nil
	g.LoanOffer = nil
	completeTurnSummary(g)
	g.Status = StatusFinished
	g.Phase = PhaseFinished
	g.WinnerID = winner.ID
	addLog(g, "The game was ended early")
	if debtNote != "" {
		addLog(g, debtNote)
	}
	addLog(g, winner.Name+" wins the game with "+money(best)+" net worth")
	touch(g)
	return winner, nil
}

// FinalScore is what EndGame ranks players by: NetWorth, settling a pending
// debt as if it were paid (the debtor loses the amount, a player creditor
// gains it). Without a debt it equals NetWorth.
func FinalScore(g *Game, playerID string) int {
	score := NetWorth(g, playerID)
	debt := g.Debt
	if debt == nil || debt.Amount <= 0 {
		return score
	}
	payer := g.PlayerByID(debt.PayerID)
	if payer == nil || payer.Bankrupt {
		return score
	}
	if debt.PayerID == playerID {
		score -= debt.Amount
	}
	if debt.CreditorID == playerID {
		if creditor := g.PlayerByID(playerID); creditor != nil && !creditor.Bankrupt {
			score += debt.Amount
		}
	}
	return score
}
