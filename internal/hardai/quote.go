package hardai

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
)

// Prices for people.
//
// Hard, Expert and Grandmaster name their own price for a deal a person
// drafts with them (Quote: the trade screen's price, which the server
// honours), and they answer a person's offer by that same price: an offer at
// or above it is accepted; below it, the computer replies with a
// counter-offer at its price (counter_trade), at most maxCounters times per
// deal and turn, and then says no with its price. When no cash price works
// it may counter with a different bundle (one of the person's deeds added,
// screened by the value network), or it says why not ("No deal: that would
// give you a full set."). The price is remembered for the round, so the
// trade screen, the answer and the counter always agree, and a dollar more
// does not buy fresh dice.
//
// Bots answer each other as before (answerTrade, answerTradeGM): this path
// is only for offers made by a human player.

// Quote is a computer's own price for a deal: the proposer gives the offer
// deeds plus Net cash and gets the request deeds.
type Quote struct {
	Net    int    // net cash the proposer pays (negative: the computer pays)
	OK     bool   // a price works
	Short  bool   // not OK because every price it would take is more cash than the proposer has
	Reason string // when not OK: why not, in the computer's words
}

type quoteEntry struct {
	round int
	fp    string
	q     Quote
}

const (
	quoteBudget   = 2500 * time.Millisecond // a price may think this long (server: all cores)
	quoteRefine   = 3                       // prices tried between the last refused and the first accepted grid price
	maxCounters   = 2                       // counter-offers per deal and turn
	maxQuoteMemo  = 256
	altScreen     = 3 // alternative bundles priced when no cash price works
	altMinGainNet = 0.004
)

// QuotesOwnPrice reports whether the bot names its own trade price (all of
// Hard, Expert and Grandmaster do; the server asks Quote instead of the
// Normal heuristic's quote).
func (b *HardBot) QuotesOwnPrice() bool { return true }

// QuoteDeal is the bot's own price for a deal the current player (proposer)
// is drafting with it on its own turn: the net cash it wants (negative: the
// most it would pay). ok is false when no price works (Quote has the
// reason).
func (b *HardBot) QuoteDeal(g *engine.Game, me, proposer string, offer, request []int) (net int, ok bool) {
	q, valid := b.QuoteFor(g, me, proposer, offer, request)
	return q.Net, valid && q.OK
}

// QuoteFor is QuoteDeal with the whole answer; valid is false when the
// position is not one where proposer can draft a deal with me.
func (b *HardBot) QuoteFor(g *engine.Game, me, proposer string, offer, request []int) (q Quote, valid bool) {
	cur := engine.CurrentPlayer(g)
	if cur == nil || cur.ID != proposer || g.Trade != nil ||
		(g.Phase != engine.PhaseAwaitingRoll && g.Phase != engine.PhaseTurnComplete) {
		return Quote{}, false
	}
	return b.Quote(g, me, proposer, offer, request), true
}

// Quote prices a deal on g, the position before it is offered (no trade
// pending): proposer gives offer and gets request from me.
func (b *HardBot) Quote(g *engine.Game, me, proposer string, offer, request []int) Quote {
	t := &engine.Trade{ProposerID: proposer, TargetID: me,
		Offer: engine.TradeBundle{Properties: offer}, Request: engine.TradeBundle{Properties: request}}
	round := totalTurns(g) / max(1, activeCount(g))
	shape := tradeShape(t)
	fp := dealFingerprint(g, t) + cashKey(g, proposer, me)
	if e, ok := b.quotes[shape]; ok && e.round == round && e.fp == fp {
		return e.q
	}
	q := b.priceDeal(g, me, proposer, t, round)
	if b.quotes == nil || len(b.quotes) >= maxQuoteMemo {
		b.quotes = map[uint64]quoteEntry{}
	}
	b.quotes[shape] = quoteEntry{round: round, fp: fp, q: q}
	return q
}

func cashKey(g *engine.Game, ids ...string) string {
	var sb strings.Builder
	for _, id := range ids {
		if p := g.PlayerByID(id); p != nil {
			fmt.Fprintf(&sb, "|%d", p.Cash)
		}
	}
	return sb.String()
}

// priceDeal searches a grid of prices with common rollouts, then refines
// between the last price refused and the first accepted.
func (b *HardBot) priceDeal(g *engine.Game, me, proposer string, t *engine.Trade, round int) Quote {
	pp, mp := g.PlayerByID(proposer), g.PlayerByID(me)
	if pp == nil || mp == nil || pp.Bankrupt || mp.Bankrupt || len(t.Offer.Properties)+len(t.Request.Properties) == 0 {
		return Quote{Reason: "No deal."}
	}
	offer, request := t.Offer.Properties, t.Request.Properties
	base := 0
	for _, i := range request {
		base += engine.Board[i].Price
	}
	for _, i := range offer {
		base -= engine.Board[i].Price
	}
	scale := float64(max(abs(base), 50))
	fractions := []float64{0.25, 0.5, 0.75, 1, 1.5, 2, 3, 4, 6}
	if base < 0 {
		fractions = []float64{-1, -0.75, -0.5, -0.25, 0, 0.25, 0.5}
	}
	short := false
	valid := func(c int) bool {
		if c > pp.Cash {
			short = true
			return false
		}
		return -c <= mp.Cash && !(c <= 0 && len(offer) == 0) && !(c >= 0 && len(request) == 0)
	}
	var grid []int
	for _, f := range fractions {
		c := int(math.Ceil(f*scale/10)) * 10
		if valid(c) && (len(grid) == 0 || c > grid[len(grid)-1]) {
			grid = append(grid, c)
		}
	}
	reason := func() string { return noDealReason(g, me, proposer, t, short) }
	if len(grid) == 0 {
		return Quote{Short: short, Reason: reason()}
	}
	undo := b.quoteSearch(g, me, t, round)
	defer undo()
	k := b.firstAccepted(g, me, proposer, t, grid)
	if k < 0 {
		return Quote{Short: short, Reason: reason()}
	}
	price := grid[k]
	if k > 0 {
		lo, hi := grid[k-1], grid[k]
		var mids []int
		for i := 1; i <= quoteRefine; i++ {
			c := int(math.Ceil(float64(lo+(hi-lo)*i/(quoteRefine+1))/10)) * 10
			if c > lo && c < hi && valid(c) && (len(mids) == 0 || c > mids[len(mids)-1]) {
				mids = append(mids, c)
			}
		}
		if len(mids) > 0 {
			if j := b.firstAccepted(g, me, proposer, t, mids); j >= 0 {
				price = mids[j]
			}
		}
	}
	return Quote{Net: price, OK: true}
}

// quoteSearch sets up the search a price uses (the level's own trade
// answer: Grandmaster's deep trade rollouts with its rival weighting, Hard's
// and Expert's ordinary decision search), seeded by the deeds and the round
// (not the cash); the returned func undoes it.
func (b *HardBot) quoteSearch(g *engine.Game, me string, t *engine.Trade, round int) func() {
	b.seedOverride = mix(b.seed, tradeShape(t), uint64(round), 0x6a7e)
	savedBudget := b.opts.Budget
	if b.opts.Budget > 0 && b.opts.Budget < quoteBudget {
		b.opts.Budget = quoteBudget
	}
	if gm := b.opts.GM; gm != nil {
		b.override = &search{horizon: gm.TradeHorizon, confidence: b.opts.Confidence, rollouts: gm.TradeRollouts}
		rival := 0.0
		if activeCount(g) >= 3 {
			rival = gm.Rival
			if b.strongestAfter(g, t, me) {
				rival = gm.RivalLeader
			}
		}
		for _, w := range b.workers {
			w.rival, w.rivalWeight = t.ProposerID, rival
		}
		b.sharpen(t.ProposerID)
	}
	return func() {
		b.seedOverride, b.override = 0, nil
		b.opts.Budget = savedBudget
		for _, w := range b.workers {
			w.rival, w.rivalWeight = "", 0
		}
		b.sharpen("")
	}
}

// firstAccepted runs one search over "no deal" and the deal at each of
// prices, and returns the index of the lowest price the bot would take (-1:
// none). Grandmaster takes a deal when its gain clears its margin plus Z
// standard errors (more when the proposer would get a colour group); Hard
// and Expert when it clears their search's own confidence bar, the one that
// lets a search overrule the heuristic.
func (b *HardBot) firstAccepted(g *engine.Game, me, proposer string, t *engine.Trade, prices []int) int {
	offer, request := t.Offer.Properties, t.Request.Properties
	cands := []candidate{{name: "no deal", apply: func(*engine.Game, string) bool { return true }, heuristic: true}}
	for _, c := range prices {
		c := c
		cands = append(cands, candidate{name: "deal at " + itoa(c), apply: func(sim *engine.Game, me string) bool {
			a := tradeAction(me, max(0, c), offer, max(0, -c), request)
			if engine.PerformAction(sim, proposer, a, nil) != nil {
				return false
			}
			return engine.PerformAction(sim, me, engine.Action{Type: engine.ActionAcceptTrade}, nil) == nil
		}})
	}
	b.started = time.Now()
	res := b.monteCarloNoPrune(g, me, cands)
	margin, z := 0.0, res.search.confidence
	if gm := b.opts.GM; gm != nil {
		margin, z = gm.Margin, gm.Z
		if gmCompletes(g, proposer, request, offer) || gmBreaks(g, me, request) {
			margin, z = gm.GroupMargin, gm.GroupZ
		}
	}
	for k := range prices {
		mean, se, n := res.pairStats(k+1, 0)
		if n >= min(b.opts.MinRounds, res.rounds) && n > 0 && !math.IsNaN(mean) && mean > 0 && mean > margin+z*se {
			return k
		}
	}
	return -1
}

// noDealReason says why no price works, in the computer's words.
func noDealReason(g *engine.Game, me, proposer string, t *engine.Trade, short bool) string {
	switch {
	case short:
		return "No deal: not for any price you can pay."
	case gmCompletes(g, proposer, t.Request.Properties, t.Offer.Properties):
		return "No deal: that would give you a full set."
	case gmBreaks(g, me, t.Request.Properties):
		return "No deal: I won't break up my set."
	}
	return "No deal at any price."
}

// ------------------------------------------------------ answering people

// answerHuman answers a person's offer (the pending trade, addressed to me)
// by the bot's own price: accept at or above it, else a counter-offer at it
// (or another bundle), else no with the reason.
func (b *HardBot) answerHuman(g *engine.Game, me string, info *DecisionInfo) (engine.Action, bool) {
	t := g.Trade
	pre := beforeOffer(g)
	q := b.Quote(pre, me, t.ProposerID, t.Offer.Properties, t.Request.Properties)
	info.MonteCarlo = true
	offered := t.Offer.Cash - t.Request.Cash
	if q.OK && offered >= q.Net {
		info.Chosen = "at its price"
		return engine.Action{Type: engine.ActionAcceptTrade}, true
	}
	key := counterKey(t)
	if now := totalTurns(g); b.counters == nil || b.countersAt != now {
		b.counters, b.countersAt = map[string]int{}, now
	}
	if b.counters[key] >= maxCounters {
		info.Chosen = "no more counters"
		reason := q.Reason
		if q.OK {
			reason = "My price is still " + priceWords(t.Offer.Properties, t.Request.Properties, q.Net) + "."
		}
		return engine.Action{Type: engine.ActionRejectTrade, Reason: reason}, true
	}
	if q.OK {
		b.counters[key]++
		info.Chosen = "counter at its price"
		return counterAction(t.Offer.Properties, t.Request.Properties, q.Net, "I'd do it for "+priceWords(t.Offer.Properties, t.Request.Properties, q.Net)+"."), true
	}
	// No cash price works (or none the person can pay): one of their deeds
	// added may.
	if offer, alt, ok := b.alternative(pre, me, t); ok {
		b.counters[key]++
		info.Chosen = "counter with another deal"
		return counterAction(offer, t.Request.Properties, alt.Net, q.Reason+" I'd do it for "+priceWords(offer, t.Request.Properties, alt.Net)+"."), true
	}
	info.Chosen = "no deal"
	return engine.Action{Type: engine.ActionRejectTrade, Reason: q.Reason}, true
}

// beforeOffer is g as it was before the pending trade was offered.
func beforeOffer(g *engine.Game) *engine.Game {
	pre := Clone(g)
	if t := pre.Trade; t != nil {
		pre.Phase = t.ReturnPhase
		pre.Trade = nil
	}
	return pre
}

// counterKey identifies a deal (who offers which deeds for which) for the
// counter limit.
func counterKey(t *engine.Trade) string {
	o := append([]int(nil), t.Offer.Properties...)
	r := append([]int(nil), t.Request.Properties...)
	sort.Ints(o)
	sort.Ints(r)
	return fmt.Sprint(t.ProposerID, o, r)
}

// counterAction is the bot's counter-offer for a person's deal: the person
// gives offer (deeds) plus net cash and gets request; written from the
// bot's side (counter_trade's offer is what the counterer gives).
func counterAction(offer, request []int, net int, reason string) engine.Action {
	return engine.Action{Type: engine.ActionCounterTrade,
		Offer:   &engine.TradeBundle{Cash: max(0, -net), Properties: append([]int{}, request...)},
		Request: &engine.TradeBundle{Cash: max(0, net), Properties: append([]int{}, offer...)},
		Reason:  reason}
}

// priceWords is what the person gives at a price: "$650 + Reservoir", or
// "Reservoir, and I pay you $40".
func priceWords(offer, request []int, net int) string {
	var parts []string
	if net > 0 {
		parts = append(parts, "$"+itoa(net))
	}
	for _, i := range offer {
		parts = append(parts, engine.Board[i].Name)
	}
	s := strings.Join(parts, " + ")
	if net < 0 {
		if s == "" {
			return "nothing, and I pay you $" + itoa(-net)
		}
		s += ", and I pay you $" + itoa(-net)
	}
	if s == "" {
		s = "nothing more"
	}
	return s
}

// alternative looks for another bundle the bot would take when no cash
// price works for the person's deal: the same deeds for the bot to give, and
// one more of the person's deeds to get. Ideas are screened by the value
// network (the bot's static gain, as for its own trade ideas) and the best
// few priced by Quote; the cheapest for the person, deed at list price plus
// cash, wins. Without a network there is no alternative.
func (b *HardBot) alternative(pre *engine.Game, me string, t *engine.Trade) (offer []int, q Quote, ok bool) {
	if b.opts.Net == nil && b.opts.Net2 == nil {
		return nil, Quote{}, false
	}
	slotOf, value, ready := b.staticTradeValue(pre, me)
	if !ready {
		return nil, Quote{}, false
	}
	pi := slotOf(t.ProposerID)
	if pi < 0 {
		return nil, Quote{}, false
	}
	in := map[int]bool{}
	for _, i := range t.Offer.Properties {
		in[i] = true
	}
	// Not a deed of the colour group the person is asking for: swapping one
	// of those for another is no answer to what they want.
	for _, i := range t.Request.Properties {
		if grp := spaceGroup[i]; grp >= 0 {
			for _, j := range groupMembers[grp] {
				in[j] = true
			}
		}
	}
	plain := tradeIdea{target: t.ProposerID, ti: pi, offer: t.Request.Properties, request: t.Offer.Properties}
	floor := value(&plain, 0)
	type idea struct {
		deed int
		gain float64
	}
	var ideas []idea
	for _, index := range engine.OwnableIndexes {
		st := pre.Properties[index]
		if st == nil || st.OwnerID != t.ProposerID || in[index] || pledged(pre, index) {
			continue
		}
		if grp := spaceGroup[index]; grp >= 0 && groupHasBuildings(pre, grp) {
			continue
		}
		with := plain
		with.request = append(append([]int{}, t.Offer.Properties...), index)
		if gain := value(&with, 0); gain > floor+altMinGainNet {
			ideas = append(ideas, idea{index, gain})
		}
	}
	sort.SliceStable(ideas, func(i, j int) bool { return ideas[i].gain > ideas[j].gain })
	best, bestCost := -1, math.MaxInt
	for i := 0; i < len(ideas) && i < altScreen; i++ {
		with := append(append([]int{}, t.Offer.Properties...), ideas[i].deed)
		aq := b.Quote(pre, me, t.ProposerID, with, t.Request.Properties)
		if !aq.OK {
			continue
		}
		if cost := aq.Net + engine.Board[ideas[i].deed].Price; cost < bestCost {
			best, bestCost, q = ideas[i].deed, cost, aq
		}
	}
	if best < 0 {
		return nil, Quote{}, false
	}
	return append(append([]int{}, t.Offer.Properties...), best), q, true
}

// noDealReasons is everything a computer can say instead of a price.
var noDealReasons = []string{
	"No deal: not for any price you can pay.",
	"No deal: that would give you a full set.",
	"No deal: I won't break up my set.",
	"No deal at any price.",
	"No deal.",
}

// CounterReasons lists what answerHuman can say with a counter-offer where
// the person gives offer plus net cash and gets request. Tools use it to
// restore the reasons older game records lost (gamerecord.RepairReasons).
func CounterReasons(offer, request []int, net int) []string {
	words := "I'd do it for " + priceWords(offer, request, net) + "."
	out := []string{words}
	for _, no := range noDealReasons {
		out = append(out, no+" "+words)
	}
	return out
}

// RefusalReasons lists what answerHuman can say when it refuses a deal
// outright, trying each of nets as the price it would have named.
func RefusalReasons(offer, request, nets []int) []string {
	out := append([]string{}, noDealReasons...)
	for _, net := range nets {
		out = append(out, "My price is still "+priceWords(offer, request, net)+".")
	}
	return out
}
