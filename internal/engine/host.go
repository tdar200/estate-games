package engine

import "errors"

// Actions the server issues on its own or on the host's behalf. Clients
// cannot send them: the server refuses both types from a player ("Unknown
// action"), and they are recorded like any other step so saved games replay.
const (
	// ActionCloseAuction ends a timed auction whose clock has run out
	// ({type, now}: now is the server's clock in Unix ms). The leader wins;
	// with no bid the property stays with the bank.
	ActionCloseAuction = "close_auction"
	// ActionRemovePlayer takes a player out of a game in progress
	// ({type, playerId}): their deeds, buildings and cash go to the bank, a
	// debt they owe is written off, and their seat is closed.
	ActionRemovePlayer = "remove_player"
)

// closeAuction is the close_auction action. requesterID must be "" (the
// server); now defaults to the game clock.
func closeAuction(g *Game, requesterID string, now int64) error {
	if requesterID != "" {
		return errors.New("Only the server can close an auction")
	}
	if g.Phase != PhaseAuction || !g.Auction.Timed() {
		return errors.New("There is no timed auction to close")
	}
	if now == 0 {
		now = g.now()
	}
	if now < g.Auction.EndsAt {
		return errors.New("The auction clock has not run out")
	}
	endAuction(g, g.Auction.BidderID)
	return nil
}

// removeFromGame is the remove_player action: the host (requesterID is the
// host's id, or "" for the table screen) takes playerID out of the game. It
// works like a resignation (resign) except that a debt the player owes is
// written off rather than paid to the creditor, so everything they had goes
// to the bank.
func removeFromGame(g *Game, requesterID, playerID string) error {
	if requesterID != "" && requesterID != g.HostID {
		return errors.New("Only the host can remove players")
	}
	if g.Phase == PhaseFinished || g.Status != StatusPlaying {
		return errors.New("The game is not in progress")
	}
	player := g.PlayerByID(playerID)
	if player == nil {
		return errors.New("Player not found")
	}
	if player.Bankrupt {
		return errors.New("That player is already out of the game")
	}
	if g.Phase == PhaseDeterminingOrder || g.Phase == PhaseOrderCountdown {
		return errors.New("Wait until the turn order is set")
	}
	if d := g.Debt; d != nil && d.PayerID == player.ID {
		// Written off: the creditor gets nothing, as when a bank debt is
		// left unpaid. The debtor is the player whose turn it is, and their
		// turn passes on (resign) once they are out.
		g.Debt = nil
		if g.Phase == PhaseDebt {
			g.Phase = PhaseTurnComplete
		}
		if o := g.LoanOffer; o != nil && o.ReturnPhase == PhaseDebt {
			o.ReturnPhase = PhaseTurnComplete
		}
		addLog(g, player.Name+"'s debt of "+money(d.Amount)+" was written off")
	}
	player.Left = true
	return resign(g, player)
}

// playerIDValue returns action.playerId when it is a string.
func (a *Action) playerIDValue() string {
	if a.raw == nil {
		return a.PlayerID
	}
	s, _ := a.raw["playerId"].(string)
	return s
}

// nowValue returns action.now when it is a whole number (0 otherwise).
func (a *Action) nowValue() int64 {
	if a.raw == nil {
		return a.Now
	}
	if v := a.number("now"); isInteger(v) && v > 0 {
		return int64(v)
	}
	return 0
}
