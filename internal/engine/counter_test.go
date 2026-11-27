package engine

import "testing"

// A counter-offer replaces the pending trade with the target's terms, sent
// back to the proposer during the proposer's turn; accepting it settles those
// terms, and it carries the original for the clients to compare.
func TestCounterTrade(t *testing.T) {
	g, first, second := startedGame(t)
	second.Kind = KindAI
	g.Properties[39].OwnerID = second.ID
	g.Properties[28].OwnerID = first.ID
	offer := Action{Type: ActionOfferTrade, TargetID: second.ID,
		Offer: &TradeBundle{Cash: 302, Properties: []int{28}}, Request: &TradeBundle{Properties: []int{39}}}
	act(t, g, first.ID, offer, nil)
	original := g.Trade.ID

	actFails(t, g, first.ID, Action{Type: ActionCounterTrade, Offer: &TradeBundle{Cash: 10}}, nil, "Only the recipient")
	counter := Action{Type: ActionCounterTrade,
		Offer: &TradeBundle{Properties: []int{39}}, Request: &TradeBundle{Cash: 650, Properties: []int{28}},
		Reason: "I'd do it for $650 + Reservoir."}
	act(t, g, second.ID, counter, nil)
	tr := g.Trade
	equal(t, "phase", g.Phase, PhaseTrade)
	equal(t, "proposer", tr.ProposerID, second.ID)
	equal(t, "target", tr.TargetID, first.ID)
	equal(t, "counterTo", tr.CounterTo, original)
	equal(t, "round", tr.Round, 1)
	equal(t, "original", *tr.Original, TradeTerms{Offer: TradeBundle{Cash: 302, Properties: []int{28}}, Request: TradeBundle{Properties: []int{39}}})
	equal(t, "return phase", tr.ReturnPhase, PhaseAwaitingRoll)
	n := len(g.Log)
	equal(t, "log", [2]string{g.Log[n-2].Text, g.Log[n-1].Text}, [2]string{"Player B countered Player A's offer", "Player B: I'd do it for $650 + Reservoir."})

	act(t, g, first.ID, Action{Type: ActionAcceptTrade}, nil)
	equal(t, "owner 39", g.Properties[39].OwnerID, first.ID)
	equal(t, "owner 28", g.Properties[28].OwnerID, second.ID)
	equal(t, "first cash", first.Cash, 1500-650)
	equal(t, "second cash", second.Cash, 1500+650)
	equal(t, "phase after", g.Phase, PhaseAwaitingRoll)
}

// Declining a computer's counter is no refusal of its own idea (no trade
// cooldown); a reason is logged only for a computer; the chain is bounded.
func TestCounterTradeDeclineAndLimits(t *testing.T) {
	g, first, second := startedGame(t)
	second.Kind = KindAI
	g.Properties[39].OwnerID = second.ID
	offer := Action{Type: ActionOfferTrade, TargetID: second.ID,
		Offer: &TradeBundle{Cash: 100}, Request: &TradeBundle{Properties: []int{39}}}
	act(t, g, first.ID, offer, nil)
	act(t, g, second.ID, Action{Type: ActionCounterTrade, Offer: &TradeBundle{Properties: []int{39}}, Request: &TradeBundle{Cash: 400}}, nil)
	act(t, g, first.ID, Action{Type: ActionRejectTrade, Reason: "people say theirs in chat"}, nil)
	equal(t, "cooldowns", len(g.AITradeCooldowns), 0)
	equal(t, "last log", g.Log[len(g.Log)-1].Text, "Player A declined the trade")

	act(t, g, first.ID, offer, nil)
	act(t, g, second.ID, Action{Type: ActionRejectTrade, Reason: "No deal: that would give you a full set."}, nil)
	equal(t, "reason", g.Log[len(g.Log)-1].Text, "Player B: No deal: that would give you a full set.")

	// Counters too expensive for the other side are refused like any trade.
	act(t, g, first.ID, offer, nil)
	actFails(t, g, second.ID, Action{Type: ActionCounterTrade, Offer: &TradeBundle{Properties: []int{39}}, Request: &TradeBundle{Cash: 5000}}, nil, "enough cash")
	// A chain of counters ends.
	for round := 1; round <= maxCounterRounds; round++ {
		who := second.ID
		if round%2 == 0 {
			who = first.ID
		}
		give, ask := &TradeBundle{Properties: []int{39}}, &TradeBundle{Cash: 100 + round}
		if who == first.ID {
			give, ask = &TradeBundle{Cash: 100 + round}, &TradeBundle{Properties: []int{39}}
		}
		act(t, g, who, Action{Type: ActionCounterTrade, Offer: give, Request: ask}, nil)
	}
	actFails(t, g, second.ID, Action{Type: ActionCounterTrade, Offer: &TradeBundle{Properties: []int{39}}, Request: &TradeBundle{Cash: 200}}, nil, "Too many")
}
