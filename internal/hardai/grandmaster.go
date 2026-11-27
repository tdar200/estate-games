package hardai

import (
	"math"
	"sort"
	"time"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/valuenet"
	"github.com/tdar200/estate-games/internal/valuenet2"
)

// Grandmaster level.
//
// Grandmaster is Expert (the same candidate plans, the tuned heuristic and
// the value network) without the ladder handicaps, with more thinking on the
// decisions that matter and a trade policy built to resist people:
//
//   - Trade answers are judged by many deeper rollouts (TradeRollouts rounds
//     of TradeHorizon rounds of play each) and accepted only for a confident
//     gain: the mean gain in its win probability must clear a margin plus z
//     standard errors, a larger one when the deal gives the proposer a
//     colour group or breaks up one of its own. In games of 3 or more it
//     also counts part of the proposer's own gain against the deal (more
//     when the proposer is, or would become, the strongest rival), so it
//     does not make kings.
//   - Probing does not pay: a trade answer's rollouts are seeded by the deeds
//     and the round, not the cash, so re-offering with a dollar more replays
//     the same futures; and within a round an answer is remembered as a cash
//     threshold (a refused deal is refused again for no more cash, an
//     accepted one accepted again for no less).
//   - Rollouts model each opponent with a style drawn from a mix (Normal,
//     tuned, aggressive trader, cash hoarder, over-builder) instead of
//     assuming everyone plays the default heuristic.
//   - Every Monte Carlo decision may use up to Rollouts rounds (Expert: 200)
//     within a 2.5 s safety budget; most still settle early. Auction
//     ceilings look AuctionHorizon rounds ahead (Expert: one).
//   - No cap on deals per partner (Expert: 2); the anti-spam memory stays.
//   - It screens more trade ideas, and offers people deals that look fair at
//     list price (a premium in cash, or an even swap) when they still raise
//     its own win probability.

// GrandmasterOptions are Grandmaster's own settings (Options.GM). Zero
// fields take the defaults (gmDefaults).
type GrandmasterOptions struct {
	TradeRollouts  int     // round cap of a trade answer
	TradeHorizon   int     // rounds of play before the network scores a trade answer's rollout
	AuctionHorizon int     // the same for an auction ceiling
	Margin         float64 // win-probability gain needed to accept a deal ...
	Z              float64 // ... plus this many standard errors
	GroupMargin    float64 // the same when the deal gives the proposer a colour group, or breaks up one of mine
	GroupZ         float64
	Rival          float64 // weight of the proposer's win-probability gain (3+ players)
	RivalLeader    float64 // ... when the proposer is or becomes the strongest rival
	NoStyles       bool    // rollouts model opponents with the default heuristic only (as Expert)
	NoHumanOffers  bool    // never propose to human players
	NoMemo         bool    // no answer memory (for tests of the search alone)
	ExpertNet      bool    // use Expert's network even when a Grandmaster network is shipped
	SharpProposer  bool    // trade answers and quotes play the proposer with the tuned heuristic, whatever style was drawn
}

var gmDefaults = GrandmasterOptions{
	TradeRollouts:  600,
	TradeHorizon:   8,
	AuctionHorizon: 3,
	Margin:         0.004,
	Z:              2,
	GroupMargin:    0.02,
	GroupZ:         2.5,
	Rival:          0.15,
	RivalLeader:    0.4,
}

func (o GrandmasterOptions) withDefaults() GrandmasterOptions {
	d := gmDefaults
	if o.TradeRollouts > 0 {
		d.TradeRollouts = o.TradeRollouts
	}
	if o.TradeHorizon != 0 {
		d.TradeHorizon = o.TradeHorizon
	}
	if o.AuctionHorizon != 0 {
		d.AuctionHorizon = o.AuctionHorizon
	}
	if o.Margin != 0 {
		d.Margin = o.Margin
	}
	if o.Z != 0 {
		d.Z = o.Z
	}
	if o.GroupMargin != 0 {
		d.GroupMargin = o.GroupMargin
	}
	if o.GroupZ != 0 {
		d.GroupZ = o.GroupZ
	}
	if o.Rival != 0 {
		d.Rival = max(0, o.Rival)
	}
	if o.RivalLeader != 0 {
		d.RivalLeader = max(0, o.RivalLeader)
	}
	d.NoStyles, d.NoHumanOffers, d.NoMemo, d.ExpertNet, d.SharpProposer = o.NoStyles, o.NoHumanOffers, o.NoMemo, o.ExpertNet, o.SharpProposer
	return d
}

// Grandmaster defaults for the shared Options.
const (
	gmRollouts       = 600                     // round cap of an ordinary Monte Carlo decision
	gmCandidates     = 20                      // plans per decision
	gmBudget         = 2500 * time.Millisecond // safety cap per decision (server: 24 cores)
	gmNetTradeScreen = 16
	gmNetTradeKeep   = 5
	gmMemoRounds     = 1  // an answer is remembered for the rest of this round
	gmWideRollouts   = 64 // round cap while the network cannot read the game (7-8 players still in): 16-round rollouts
)

// NewGrandmasterBot returns a Grandmaster bot. Zero Options fields take the
// Grandmaster defaults.
func NewGrandmasterBot(seed int64, opts Options) *HardBot {
	gm := GrandmasterOptions{}
	if opts.GM != nil {
		gm = *opts.GM
	}
	gm = gm.withDefaults()
	opts.GM = &gm
	if opts.Rollouts == 0 {
		opts.Rollouts = gmRollouts
	}
	if opts.MaxCandidates == 0 {
		opts.MaxCandidates = gmCandidates
	}
	if opts.Budget == 0 {
		opts.Budget = gmBudget
	}
	if opts.MaxDeals == 0 {
		opts.MaxDeals = -1
	}
	if opts.NetTradeScreen == 0 {
		opts.NetTradeScreen = gmNetTradeScreen
	}
	if opts.NetTradeKeep == 0 {
		opts.NetTradeKeep = gmNetTradeKeep
	}
	if !gm.NoStyles && opts.Styles == nil {
		opts.Styles = OpponentStyles()
	}
	if opts.Net2 == nil && !gm.ExpertNet {
		if net, err := valuenet2.Default(); err == nil {
			opts.Net2 = net
		}
	}
	return NewExpertBot(seed, opts)
}

// ---------------------------------------------------------- opponent styles

// OpponentStyle is a way of playing the rollouts model an opponent with.
type OpponentStyle struct {
	Name   string
	Weight float64
	Params *ai.Params
}

// OpponentStyles is Grandmaster's mix of opponent models: the Normal
// heuristic, the tuned one, and three caricatures of how people play (an
// aggressive trader, a cash hoarder, an over-builder).
func OpponentStyles() []OpponentStyle {
	normal := ai.DefaultParams
	tuned := *ExpertParams()
	trader := tuned
	trader.MinTradeGain = 0
	trader.FairnessShare, trader.FairnessShare2P = 0.12, 0.08
	trader.TradeReserve = 120
	trader.LiquidityMin, trader.LiquidityFactor = 90, 0.3
	trader.AcceptMargin = -15
	hoarder := normal
	hoarder.ReserveScale = [4]float64{1.8, 1.8, 1.8, 1.8}
	hoarder.ReserveMin = 220
	hoarder.BuildReserve = 2
	hoarder.BuyScale = 0.85
	builder := tuned
	builder.BuildReserve = 0
	builder.ReserveScale[2] = 0.1
	builder.ReserveMin = 0
	builder.ReserveHostileDev = 0
	builder.HotelPenalty = 0.05
	return []OpponentStyle{
		{"normal", 0.3, &normal},
		{"tuned", 0.3, &tuned},
		{"trader", 0.14, &trader},
		{"hoarder", 0.13, &hoarder},
		{"builder", 0.13, &builder},
	}
}

// drawStyles picks every seat's style for one rollout (the same for every
// candidate of a round: common random numbers).
func (p *rolloutPolicy) drawStyles(sim *engine.Game, seed uint64) {
	if len(p.styles) == 0 {
		return
	}
	total := 0.0
	for _, s := range p.styles {
		total += s.Weight
	}
	r := splitMix{state: mix(seed, 0x57)}
	for i := range sim.Players {
		if i >= maxSeats {
			break
		}
		u := r.Float64() * total
		p.seat[i] = p.styles[len(p.styles)-1].Params
		for _, s := range p.styles {
			if u < s.Weight {
				p.seat[i] = s.Params
				break
			}
			u -= s.Weight
		}
	}
	for i := range p.seatIDs {
		p.seatIDs[i] = ""
	}
	for i, pl := range sim.Players {
		if i < maxSeats {
			p.seatIDs[i] = pl.ID
		}
	}
	p.styled = true
}

// --------------------------------------------------------------- answering

// gmAnswer is a remembered trade answer: within its round, a deal of the
// same shape is accepted again for at least acceptAt net cash and refused
// again for at most refuseAt.
type gmAnswer struct {
	round    int
	fp       string
	acceptAt int // lowest net cash to me accepted (math.MaxInt: none)
	refuseAt int // highest net cash to me refused (math.MinInt: none)
}

// answerTradeGM decides a trade offered to a Grandmaster.
func (b *HardBot) answerTradeGM(g *engine.Game, me string, hAct engine.Action, info *DecisionInfo) (engine.Action, bool) {
	gm := b.opts.GM
	t := g.Trade
	accept := engine.Action{Type: engine.ActionAcceptTrade}
	reject := engine.Action{Type: engine.ActionRejectTrade}
	shape := tradeShape(t)
	net := t.Offer.Cash - t.Request.Cash // cash I receive
	round := totalTurns(g) / max(1, activeCount(g))
	fp := dealFingerprint(g, t)
	if b.gmMemo == nil {
		b.gmMemo = map[uint64]*gmAnswer{}
	}
	m := b.gmMemo[shape]
	if m != nil && (m.fp != fp || round-m.round >= gmMemoRounds) {
		m = nil
	}
	if m != nil && !gm.NoMemo {
		switch {
		case net >= m.acceptAt:
			info.Chosen, info.Heuristic = "accepted before", hAct.Type == engine.ActionAcceptTrade
			return accept, true
		case net <= m.refuseAt:
			info.Chosen, info.Heuristic = "refused before", hAct.Type == engine.ActionRejectTrade
			return reject, true
		}
	}
	if m == nil {
		m = &gmAnswer{round: round, fp: fp, acceptAt: math.MaxInt, refuseAt: math.MinInt}
		b.gmMemo[shape] = m
	}
	ok, _ := b.judgeDeal(g, me, info)
	if ok {
		m.acceptAt = min(m.acceptAt, net)
		return accept, true
	}
	m.refuseAt = max(m.refuseAt, net)
	return reject, true
}

// judgeDeal runs the trade-answer search on g (a trade addressed to me) and
// reports whether accepting clears Grandmaster's bar, and the estimated gain.
func (b *HardBot) judgeDeal(g *engine.Game, me string, info *DecisionInfo) (bool, float64) {
	gm := b.opts.GM
	t := g.Trade
	round := totalTurns(g) / max(1, activeCount(g))
	proposerGetsGroup := gmCompletes(g, t.ProposerID, t.Request.Properties, t.Offer.Properties) ||
		gmBreaks(g, me, t.Request.Properties)
	margin, z := gm.Margin, gm.Z
	if proposerGetsGroup {
		margin, z = gm.GroupMargin, gm.GroupZ
	}
	// Part of the proposer's gain counts against the deal (3+ players).
	rival := 0.0
	if activeCount(g) >= 3 {
		rival = gm.Rival
		if b.strongestAfter(g, t, me) {
			rival = gm.RivalLeader
		}
	}
	// Seeded by the deeds and the round, not the cash.
	b.seedOverride = mix(b.seed, tradeShape(t), uint64(round), 0x6a7e)
	b.override = &search{horizon: gm.TradeHorizon, confidence: b.opts.Confidence, rollouts: gm.TradeRollouts}
	for _, w := range b.workers {
		w.rival, w.rivalWeight = t.ProposerID, rival
	}
	b.sharpen(t.ProposerID)
	cands := []candidate{
		{name: engine.ActionRejectTrade, actions: []engine.Action{{Type: engine.ActionRejectTrade}}, heuristic: true},
		{name: engine.ActionAcceptTrade, actions: []engine.Action{{Type: engine.ActionAcceptTrade}}},
	}
	res := b.monteCarlo(g, me, cands, 0)
	b.seedOverride, b.override = 0, nil
	for _, w := range b.workers {
		w.rival, w.rivalWeight = "", 0
	}
	b.sharpen("")
	info.MonteCarlo = true
	info.Rounds = res.rounds
	info.Rollouts = res.rollouts
	info.Candidates = 2
	info.Overhead = res.start.Sub(b.started)
	mean, se, n := res.pairStats(1, 0)
	ok := n >= b.opts.MinRounds && !math.IsNaN(mean) && mean > margin+z*se
	if ok {
		info.Chosen = engine.ActionAcceptTrade
	} else {
		info.Chosen = engine.ActionRejectTrade
	}
	info.Kind = "gm answer"
	info.Heuristic = !ok
	return ok, mean
}

// strongestAfter reports whether the proposer of g.Trade is, or after the
// deal would be, the strongest player other than me (by the value network's
// static estimate; false without one or when the game does not fit it).
func (b *HardBot) strongestAfter(g *engine.Game, t *engine.Trade, me string) bool {
	if b.opts.Net == nil || !valuenet.Fits(g) {
		return false
	}
	if b.vn == nil {
		b.vn = valuenet.NewEvaluator(b.opts.Net)
	}
	pi, mi := valuenet.SlotOf(g, t.ProposerID), valuenet.SlotOf(g, me)
	if pi < 0 || mi < 0 {
		return false
	}
	var pos valuenet.Position
	valuenet.FillFromGame(&pos, g)
	top := func(probs [valuenet.MaxSlots]float64) bool {
		for s := 0; s < int(pos.NPlayers); s++ {
			if s != pi && s != mi && probs[s] > probs[pi] {
				return false
			}
		}
		return true
	}
	if top(b.vn.WinProbs(&pos)) {
		return true
	}
	ti := mi
	for _, index := range t.Request.Properties {
		pos.SetOwner(index, pi)
	}
	for _, index := range t.Offer.Properties {
		pos.SetOwner(index, ti)
	}
	pos.Players[pi].Cash += int32(t.Request.Cash - t.Offer.Cash)
	pos.Players[ti].Cash += int32(t.Offer.Cash - t.Request.Cash)
	return top(b.vn.WinProbs(&pos))
}

// gmCompletes reports whether player id, receiving gets and giving gives,
// would own a colour group it does not own now.
func gmCompletes(g *engine.Game, id string, gets, gives []int) bool {
	has := func(index int, after bool) bool {
		if after {
			for _, x := range gives {
				if x == index {
					return false
				}
			}
			for _, x := range gets {
				if x == index {
					return true
				}
			}
		}
		return ownerOf(g, index) == id
	}
	for _, members := range groupMembers {
		before, after := true, true
		for _, index := range members {
			before = before && has(index, false)
			after = after && has(index, true)
		}
		if after && !before {
			return true
		}
	}
	return false
}

// dealFingerprint describes the state a trade answer depends on: the deeds
// traded and every deed of their colour groups (owner, houses, mortgage).
// A remembered answer only stands while it is unchanged.
func dealFingerprint(g *engine.Game, t *engine.Trade) string {
	seen := map[int]bool{}
	var out []byte
	addGroup := func(index int) {
		list := []int{index}
		if grp := spaceGroup[index]; grp >= 0 {
			list = groupMembers[grp]
		}
		for _, i := range list {
			if seen[i] {
				continue
			}
			seen[i] = true
			st := g.Properties[i]
			out = append(out, byte(i), byte(st.Houses))
			if st.Mortgaged {
				out = append(out, 'm')
			}
			out = append(out, st.OwnerID...)
			out = append(out, ';')
		}
	}
	for _, list := range [][]int{t.Offer.Properties, t.Request.Properties} {
		sorted := append([]int(nil), list...)
		sort.Ints(sorted)
		for _, index := range sorted {
			addGroup(index)
		}
	}
	return string(out)
}

// ------------------------------------------------------------ human offers

// humanTradeCandidates are offers to human players that look fair at list
// price: buying one of their deeds for a premium in cash, or an even swap of
// deeds (each side's list value within $40, the difference settled in cash),
// screened by the value network like the network trade ideas. The rollouts
// judge them as if accepted (a person does not answer like the heuristic),
// and the trade memory rests a refused one as usual.
func (b *HardBot) humanTradeCandidates(g *engine.Game, me string) []candidate {
	if b.opts.GM == nil || b.opts.GM.NoHumanOffers || b.opts.Net == nil || g.Turn == nil || g.Trade != nil ||
		b.opts.Disable&DecideProposals != 0 || b.mem.paused(g, me) {
		return nil
	}
	slotOf, staticValue, ok := b.staticTradeValue(g, me)
	if !ok {
		return nil
	}
	meP := g.PlayerByID(me)
	type idea struct {
		a    engine.Action
		gain float64
		name string
	}
	var ideas []idea
	value := func(ti int, offer, request []int, pay int) float64 {
		return staticValue(&tradeIdea{ti: ti, offer: offer, request: request}, pay)
	}
	spare := spareHoldings(g, me, -1)
	for _, t := range g.Players {
		if t.ID == me || t.Bankrupt || t.Kind == engine.KindAI {
			continue
		}
		ti := slotOf(t.ID)
		if ti < 0 {
			continue
		}
		for _, index := range engine.OwnableIndexes {
			st := g.Properties[index]
			if st.OwnerID != t.ID || pledged(g, index) {
				continue
			}
			if grp := spaceGroup[index]; grp >= 0 && groupHasBuildings(g, grp) {
				continue
			}
			price := engine.Board[index].Price
			if st.Mortgaged {
				price /= 2
			}
			// A person sells for a clear premium; more for the deed that
			// completes my group.
			premium := 1.3
			if completesFor(g, me, index) {
				premium = 1.8
			}
			pay := int(math.Ceil(float64(price)*premium/10)) * 10
			if pay <= meP.Cash-150 {
				if gain := value(ti, nil, []int{index}, pay); gain >= netTradeMinGain {
					ideas = append(ideas, idea{tradeAction(t.ID, pay, nil, 0, []int{index}), gain, "human buy " + engine.Board[index].Name})
				}
			}
			for _, chip := range spare {
				diff := engine.Board[index].Price - engine.Board[chip].Price
				if diff > 40 || diff < -40 && !completesFor(g, t.ID, chip) {
					continue
				}
				pay := max(0, diff+20)
				if pay > meP.Cash-150 {
					continue
				}
				if gain := value(ti, []int{chip}, []int{index}, pay); gain >= netTradeMinGain {
					ideas = append(ideas, idea{tradeAction(t.ID, pay, []int{chip}, 0, []int{index}), gain,
						"human swap " + engine.Board[chip].Name + " for " + engine.Board[index].Name})
				}
			}
		}
	}
	sort.SliceStable(ideas, func(i, j int) bool { return ideas[i].gain > ideas[j].gain })
	var out []candidate
	for _, id := range ideas {
		if len(out) >= 3 {
			break
		}
		if engine.AITradeOnCooldown(g, me, id.a.TargetID, id.a.Request.Properties) || !b.mem.allowed(g, me, id.a) {
			continue
		}
		// Never hand a person a colour group unless it gets one too (a
		// full-set swap): its answers hold the same line.
		if gmCompletes(g, id.a.TargetID, id.a.Offer.Properties, id.a.Request.Properties) &&
			!gmCompletes(g, me, id.a.Request.Properties, id.a.Offer.Properties) {
			continue
		}
		a := id.a
		out = append(out, candidate{name: id.name, actions: []engine.Action{a}, apply: func(sim *engine.Game, me string) bool {
			// Judged as accepted: the offer, then the person's yes.
			pi := playerIndex(sim, me)
			if pi < 0 || engine.PerformAction(sim, me, a, nil) != nil {
				return false
			}
			return engine.PerformAction(sim, a.TargetID, engine.Action{Type: engine.ActionAcceptTrade}, nil) == nil
		}})
	}
	return out
}

// scoreRival is the rollout score of a Grandmaster trade answer: my win
// probability minus rivalWeight times the rival's (the proposer's).
func (w *simWorker) scoreRival(sim *engine.Game, me string, useNet bool) float64 {
	if sim.Status == engine.StatusFinished {
		switch sim.WinnerID {
		case me:
			return 1
		case w.rival:
			return -w.rivalWeight
		}
		return 0
	}
	if useNet && w.vn2 != nil {
		mi, ri := valuenet2.SlotOf(sim, me), valuenet2.SlotOf(sim, w.rival)
		if mi < 0 {
			return 0
		}
		valuenet2.FillFromGame(&w.pos2, sim)
		probs := w.vn2.WinProbs(&w.pos2)
		s := probs[mi]
		if ri >= 0 {
			s -= w.rivalWeight * probs[ri]
		}
		return s
	}
	if !useNet || w.vn == nil {
		return w.score(sim, me, useNet) - w.rivalWeight*w.score(sim, w.rival, useNet)
	}
	mi, ri := valuenet.SlotOf(sim, me), valuenet.SlotOf(sim, w.rival)
	if mi < 0 {
		return 0
	}
	valuenet.FillFromGame(&w.pos, sim)
	probs := w.vn.WinProbs(&w.pos)
	s := probs[mi]
	if ri >= 0 {
		s -= w.rivalWeight * probs[ri]
	}
	return s
}

// sharpen makes the rollouts play the proposer of the deal being judged with
// the tuned heuristic (GrandmasterOptions.SharpProposer); "" undoes it.
func (b *HardBot) sharpen(proposer string) {
	if b.opts.GM == nil || !b.opts.GM.SharpProposer {
		return
	}
	var params *ai.Params
	if proposer != "" {
		params = ExpertParams()
	}
	for _, w := range b.workers {
		w.policy.sharp, w.policy.sharpParams = proposer, params
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// monteCarloNoPrune is monteCarlo without early stopping or pruning: every
// candidate gets every round (a price grid is compared candidate by
// candidate against "no deal").
func (b *HardBot) monteCarloNoPrune(g *engine.Game, me string, cands []candidate) *mcResult {
	saved := b.opts.MinRounds
	b.opts.MinRounds = math.MaxInt32 // settled() never stops before the cap
	res := b.monteCarlo(g, me, cands, 0)
	b.opts.MinRounds = saved
	return res
}

// gmBreaks reports whether giving away deeds breaks up a colour group id
// owns whole now.
func gmBreaks(g *engine.Game, id string, gives []int) bool {
	for _, index := range gives {
		if grp := spaceGroup[index]; grp >= 0 && ownsGroup(g, id, grp) {
			return true
		}
	}
	return false
}
