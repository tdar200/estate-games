package hardai

import "github.com/tdar200/estate-games/internal/engine"

// Clone returns a deep copy of g. The copy shares no mutable memory with g,
// preserves nil-versus-empty for every slice and map (so its JSON encoding is
// byte-identical to g's), and keeps g's Clock and NewID functions. It is much
// faster than a JSON round trip; TestCloneMatchesJSON checks the two agree.
func Clone(g *engine.Game) *engine.Game {
	return cloneGame(g, false)
}

// cloneGame is Clone; withoutLog leaves the log empty (with spare capacity),
// which simulations use because nothing reads the log.
func cloneGame(g *engine.Game, withoutLog bool) *engine.Game {
	if g == nil {
		return nil
	}
	out := *g // copies every scalar, Settings.DiceMode, Clock, NewID

	if g.Players != nil {
		players := make([]engine.Player, len(g.Players))
		out.Players = make([]*engine.Player, len(g.Players))
		for i, p := range g.Players {
			if p == nil {
				continue
			}
			players[i] = *p
			out.Players[i] = &players[i]
		}
	}
	if g.Properties != nil {
		states := make([]engine.PropertyState, len(g.Properties))
		out.Properties = make(map[int]*engine.PropertyState, len(g.Properties))
		i := 0
		for index, state := range g.Properties {
			if state == nil {
				out.Properties[index] = nil
				continue
			}
			states[i] = *state
			out.Properties[index] = &states[i]
			i++
		}
	}
	if g.Turn != nil {
		turn := *g.Turn
		turn.Dice = cloneInts(g.Turn.Dice)
		if g.Turn.Movements != nil {
			turn.Movements = make([]*engine.Movement, len(g.Turn.Movements))
			for i, m := range g.Turn.Movements {
				turn.Movements[i] = cloneMovement(m)
			}
		}
		out.Turn = &turn
	}
	if g.PendingPurchase != nil {
		v := *g.PendingPurchase
		out.PendingPurchase = &v
	}
	if g.Auction != nil {
		a := *g.Auction
		a.ActiveIDs = cloneStrings(g.Auction.ActiveIDs)
		a.PassedIDs = cloneStrings(g.Auction.PassedIDs)
		a.Bids = cloneIntMap(g.Auction.Bids)
		out.Auction = &a
	}
	if g.Debt != nil {
		d := *g.Debt
		if g.Debt.After != nil {
			after := *g.Debt.After
			after.Dice = cloneInts(g.Debt.After.Dice)
			d.After = &after
		}
		out.Debt = &d
	}
	if g.LastCard != nil {
		c := *g.LastCard
		out.LastCard = &c
	}
	if g.PendingCard != nil {
		c := *g.PendingCard
		out.PendingCard = &c
	}
	if g.LastLanding != nil {
		l := *g.LastLanding
		out.LastLanding = &l
	}
	if g.LastBankruptcy != nil {
		b := *g.LastBankruptcy
		b.Properties = cloneInts(g.LastBankruptcy.Properties)
		out.LastBankruptcy = &b
	}
	out.LastMove = cloneMovement(g.LastMove)
	if g.Trade != nil {
		t := *g.Trade
		t.Offer.Properties = cloneInts(g.Trade.Offer.Properties)
		t.Request.Properties = cloneInts(g.Trade.Request.Properties)
		out.Trade = &t
	}
	out.AITradeCooldowns = cloneIntMap(g.AITradeCooldowns)
	out.TurnCounts = cloneIntMap(g.TurnCounts)
	if g.OpeningOrder != nil {
		o := *g.OpeningOrder
		o.PendingIDs = cloneStrings(g.OpeningOrder.PendingIDs)
		o.Order = cloneStrings(g.OpeningOrder.Order)
		if g.OpeningOrder.CountdownEndsAt != nil {
			v := *g.OpeningOrder.CountdownEndsAt
			o.CountdownEndsAt = &v
		}
		if g.OpeningOrder.Entries != nil {
			o.Entries = make([]*engine.OpeningEntry, len(g.OpeningOrder.Entries))
			for i, e := range g.OpeningOrder.Entries {
				if e == nil {
					continue
				}
				entry := *e
				if e.Rolls != nil {
					entry.Rolls = make([]engine.OpeningRoll, len(e.Rolls))
					for j, r := range e.Rolls {
						entry.Rolls[j] = r
						entry.Rolls[j].Dice = cloneInts(r.Dice)
					}
				}
				o.Entries[i] = &entry
			}
		}
		out.OpeningOrder = &o
	}
	if g.TurnSummary != nil {
		s := *g.TurnSummary
		if g.TurnSummary.Players != nil {
			s.Players = append(make([]engine.TurnSummaryEntry, 0, len(g.TurnSummary.Players)), g.TurnSummary.Players...)
		}
		out.TurnSummary = &s
	}
	if g.TurnSummaryBaselines != nil {
		out.TurnSummaryBaselines = make(map[string]map[string]engine.PlayerSpot, len(g.TurnSummaryBaselines))
		for id, spots := range g.TurnSummaryBaselines {
			if spots == nil {
				out.TurnSummaryBaselines[id] = nil
				continue
			}
			copied := make(map[string]engine.PlayerSpot, len(spots))
			for k, v := range spots {
				copied[k] = v
			}
			out.TurnSummaryBaselines[id] = copied
		}
	}
	if g.Settings.Rules.Auctions != nil {
		v := *g.Settings.Rules.Auctions
		out.Settings.Rules.Auctions = &v
	}
	out.Decks.Chance = cloneStrings(g.Decks.Chance)
	out.Decks.Chest = cloneStrings(g.Decks.Chest)
	switch {
	case withoutLog:
		out.Log = make([]engine.LogEntry, 0, logCapacity)
	case g.Log != nil:
		out.Log = append(make([]engine.LogEntry, 0, len(g.Log)), g.Log...)
	}
	if g.Loans != nil {
		loans := make([]engine.Loan, len(g.Loans))
		out.Loans = make([]*engine.Loan, len(g.Loans))
		for i, l := range g.Loans {
			if l == nil {
				continue
			}
			loans[i] = *l
			loans[i].Collateral = cloneInts(l.Collateral)
			out.Loans[i] = &loans[i]
		}
	}
	if g.LoanOffer != nil {
		o := *g.LoanOffer
		o.Collateral = cloneInts(g.LoanOffer.Collateral)
		out.LoanOffer = &o
	}
	return &out
}

// logCapacity is the log capacity of simulation clones; the driver empties
// the log before it reaches it, so appends never reallocate (see trimLog).
const logCapacity = 128

func cloneMovement(m *engine.Movement) *engine.Movement {
	if m == nil {
		return nil
	}
	c := *m
	c.Path = cloneInts(m.Path)
	return &c
}

func cloneInts(s []int) []int {
	if s == nil {
		return nil
	}
	return append(make([]int, 0, len(s)), s...)
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append(make([]string, 0, len(s)), s...)
}

func cloneIntMap(m map[string]int) map[string]int {
	if m == nil {
		return nil
	}
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
