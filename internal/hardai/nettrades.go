package hardai

import (
	"sort"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/valuenet"
	"github.com/tdar200/estate-games/internal/valuenet2"
)

// Trade ideas screened by the value network (Expert).
//
// Hard proposes only a few hand-picked kinds of trade (completing one of its
// colour groups, buying railroads and utilities, selling spare holdings).
// With a value network the bot can look at many more ideas: every
// tradeable space of every computer opponent, for cash or for one of its
// own spare holdings, and every sale of a spare holding. Each idea's
// structure is first scored statically (the network's change in its win
// probability with the space at list price); the best ones are priced with
// ai.QuoteTrade (the computer owner's own acceptance test) and kept if they
// still raise its win probability at that price. The survivors join the
// candidate plans, so the rollouts have the final say.

const (
	netTradeScreen   = 8     // ideas priced with QuoteTrade
	netTradeKeep     = 3     // ideas passed to the search
	netTradeMinGain  = 0.004 // minimum static gain in win probability
	netTradeMaxChips = 3     // own holdings tried as part payment per request
)

type tradeIdea struct {
	target  string
	ti      int // target's valuenet slot
	offer   []int
	request []int
	gain    float64 // static gain at the screening price
}

// netTradeCandidates returns the best value-network trade ideas.
func (b *HardBot) netTradeCandidates(g *engine.Game, me string) []candidate {
	if b.opts.Net == nil || b.opts.NoNetTrades || g.Turn == nil || g.Turn.AITradeAttempted || g.Trade != nil ||
		b.opts.Disable&DecideProposals != 0 {
		return nil
	}
	slotOf, value, ok := b.staticTradeValue(g, me)
	if !ok {
		return nil
	}
	me0 := g.PlayerByID(me)

	spare := spareHoldings(g, me, -1)
	var ideas []tradeIdea
	for _, t := range g.Players {
		if t.ID == me || t.Bankrupt || t.Kind != engine.KindAI {
			continue
		}
		ti := slotOf(t.ID)
		if ti < 0 {
			continue
		}
		chips := swapFirst(g, t.ID, spare)
		if len(chips) > netTradeMaxChips {
			chips = chips[:netTradeMaxChips]
		}
		for _, index := range engine.OwnableIndexes {
			st := g.Properties[index]
			if st.OwnerID != t.ID || pledged(g, index) || engine.AITradeOnCooldown(g, me, t.ID, []int{index}) {
				continue
			}
			if grp := spaceGroup[index]; grp >= 0 && groupHasBuildings(g, grp) {
				continue
			}
			price := engine.Board[index].Price
			idea := tradeIdea{target: t.ID, ti: ti, request: []int{index}}
			idea.gain = value(&idea, price)
			ideas = append(ideas, idea)
			for _, chip := range chips {
				swap := tradeIdea{target: t.ID, ti: ti, offer: []int{chip}, request: []int{index}}
				swap.gain = value(&swap, price-engine.Board[chip].Price)
				ideas = append(ideas, swap)
			}
		}
		for _, chip := range spare {
			if engine.AITradeOnCooldown(g, me, t.ID, []int{chip}) {
				continue
			}
			sale := tradeIdea{target: t.ID, ti: ti, offer: []int{chip}}
			sale.gain = value(&sale, -engine.Board[chip].Price)
			ideas = append(ideas, sale)
		}
	}
	sort.SliceStable(ideas, func(i, j int) bool { return ideas[i].gain > ideas[j].gain })
	view := b.h.vb.build(g)
	if view == nil {
		return nil
	}
	markAI(view, me)
	type priced struct {
		c    candidate
		gain float64
	}
	var out []priced
	screen, keep := netTradeScreen, netTradeKeep
	if b.opts.NetTradeScreen > 0 {
		screen = b.opts.NetTradeScreen
	}
	if b.opts.NetTradeKeep > 0 {
		keep = b.opts.NetTradeKeep
	}
	for i := 0; i < len(ideas) && i < screen; i++ {
		idea := &ideas[i]
		if idea.gain < netTradeMinGain {
			break
		}
		q, err := ai.QuoteTrade(view, me, ai.NewQuoteRequest(idea.target, idea.offer, idea.request))
		if err != nil {
			continue
		}
		pay := q.OfferCash - q.RequestCash
		if q.OfferCash > me0.Cash || (len(idea.request) > 0 && len(idea.offer) == 0 && q.OfferCash <= 0) {
			continue
		}
		gain := value(idea, pay)
		if gain < netTradeMinGain {
			continue
		}
		name := "net trade"
		for _, index := range idea.offer {
			name += " give " + engine.Board[index].Name
		}
		for _, index := range idea.request {
			name += " get " + engine.Board[index].Name
		}
		out = append(out, priced{single(name, tradeAction(idea.target, q.OfferCash, idea.offer, q.RequestCash, idea.request)), gain})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].gain > out[j].gain })
	var cands []candidate
	for i := 0; i < len(out) && i < keep; i++ {
		cands = append(cands, out[i].c)
	}
	return cands
}

// payFee charges the receiver of a mortgaged space the transfer fee.
func payFee(pos *valuenet.Position, receiver, index int) {
	pos.Players[receiver].Cash -= int32(engine.MortgageTransferFee(engine.Board[index].Price))
}

// staticTradeValue returns the network's static view of trade ideas in g
// for me: slotOf maps a player to its network slot (-1: not read), and
// value is the change in my win probability if an idea happened with
// payToTarget cash from me to the target (-1 if either side could not pay).
// It uses Expert's network, or Grandmaster's own (Options.Net2) when set.
// ok is false when the network does not read me.
func (b *HardBot) staticTradeValue(g *engine.Game, me string) (slotOf func(string) int, value func(*tradeIdea, int) float64, ok bool) {
	if b.opts.Net2 != nil {
		return b.staticTradeValue2(g, me)
	}
	// The network reads at most valuenet.MaxSlots players still in the game;
	// positions use its slots, not g.Players indexes.
	mi := valuenet.SlotOf(g, me)
	if mi < 0 {
		return nil, nil, false
	}
	if b.vn == nil {
		b.vn = valuenet.NewEvaluator(b.opts.Net)
	}
	var base valuenet.Position
	valuenet.FillFromGame(&base, g)
	p0 := b.vn.WinProbs(&base)[mi]
	value = func(idea *tradeIdea, payToTarget int) float64 {
		pos := base
		for _, index := range idea.request {
			pos.SetOwner(index, mi)
			if g.Properties[index].Mortgaged {
				payFee(&pos, mi, index)
			}
		}
		for _, index := range idea.offer {
			pos.SetOwner(index, idea.ti)
			if g.Properties[index].Mortgaged {
				payFee(&pos, idea.ti, index)
			}
		}
		pos.Players[mi].Cash -= int32(payToTarget)
		pos.Players[idea.ti].Cash += int32(payToTarget)
		if pos.Players[mi].Cash < 0 || pos.Players[idea.ti].Cash < 0 {
			return -1
		}
		return b.vn.WinProbs(&pos)[mi] - p0
	}
	return func(id string) int { return valuenet.SlotOf(g, id) }, value, true
}

// staticTradeValue2 is staticTradeValue with Grandmaster's 8-slot network.
func (b *HardBot) staticTradeValue2(g *engine.Game, me string) (func(string) int, func(*tradeIdea, int) float64, bool) {
	mi := valuenet2.SlotOf(g, me)
	if mi < 0 {
		return nil, nil, false
	}
	if b.vn2 == nil {
		b.vn2 = valuenet2.NewEvaluator(b.opts.Net2)
	}
	var base valuenet2.Position
	valuenet2.FillFromGame(&base, g)
	p0 := b.vn2.WinProbs(&base)[mi]
	fee := func(pos *valuenet2.Position, receiver, index int) {
		pos.Players[receiver].Cash -= int32(engine.MortgageTransferFee(engine.Board[index].Price))
	}
	value := func(idea *tradeIdea, payToTarget int) float64 {
		pos := base
		for _, index := range idea.request {
			pos.SetOwner(index, mi)
			if g.Properties[index].Mortgaged {
				fee(&pos, mi, index)
			}
		}
		for _, index := range idea.offer {
			pos.SetOwner(index, idea.ti)
			if g.Properties[index].Mortgaged {
				fee(&pos, idea.ti, index)
			}
		}
		pos.Players[mi].Cash -= int32(payToTarget)
		pos.Players[idea.ti].Cash += int32(payToTarget)
		if pos.Players[mi].Cash < 0 || pos.Players[idea.ti].Cash < 0 {
			return -1
		}
		return b.vn2.WinProbs(&pos)[mi] - p0
	}
	return func(id string) int { return valuenet2.SlotOf(g, id) }, value, true
}
