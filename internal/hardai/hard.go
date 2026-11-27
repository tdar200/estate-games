package hardai

import (
	"math"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/valuenet"
	"github.com/tdar200/estate-games/internal/valuenet2"
)

// HardBot is the Monte Carlo player. For each decision that matters it
// enumerates a few candidate plans (always including the heuristic's own
// choice), plays every candidate forward in many simulated futures with the
// rollout policy (sim.go) and keeps the plan with the best average outcome.
// Every candidate of a round sees the same dice and decks (common random
// numbers), so differences between candidates are measured, not drowned in
// luck. An alternative replaces the heuristic's choice only when its paired
// advantage clears Options.Confidence standard errors.
//
// Routine moves (rolling, confirming a card, paying a debt, ending a turn
// with nothing to do) are the heuristic's.
//
// A HardBot is not safe for concurrent use: give every seat its own.
type HardBot struct {
	opts    Options
	seed    uint64
	h       *HeuristicBot
	workers []*simWorker
	count   uint64    // Monte Carlo decisions made (seeds)
	started time.Time // start of the current Choose (the budget counts from here)

	// plan: the rest of the chosen candidate, valid while the game is the
	// one it was made for and nothing else has acted since.
	plan        []engine.Action
	planGame    string // game code
	planVersion int
	planPlayer  string

	// per-turn bookkeeping
	turnRef      turnKey
	rollMC       int
	completeMC   int
	lastWasTrade bool

	// auction ceiling cache
	auctionRef  auctionKey
	auctionCeil int

	vn  *valuenet.Evaluator  // static evaluations (Expert trade ideas), lazily made
	vn2 *valuenet2.Evaluator // the same with Options.Net2 (Grandmaster)

	seedOverride uint64 // if not 0, the seed of the next Monte Carlo decision

	mem     *tradeMemory            // its own offers (anti-spam, no farming)
	refused map[uint64]refusedOffer // trade shape -> the last offer it refused

	// Grandmaster only (grandmaster.go).
	gmMemo   map[uint64]*gmAnswer // trade shape -> remembered answer
	override *search              // if set, the search setting of the next Monte Carlo decision

	// Prices for people (quote.go).
	quotes     map[uint64]quoteEntry // trade shape -> its price this round
	counters   map[string]int        // deal -> counter-offers made this turn
	countersAt int                   // the turn (totalTurns) counters counts
}

// refusedOffer is the cash of an offer the bot refused, and when.
type refusedOffer struct {
	offer, request int
	turn           int // total turns of the game
}

// refusalMemory is how long, in rounds, a refused offer stays refused: the
// same deeds for no more cash are refused again without a new search (a new
// search with fresh dice would accept a bad offer now and then, so repeating
// it would eventually pay).
const refusalMemory = 3

type auctionKey struct {
	turn  turnKey
	space int
	turns int
}

// NewHardBot returns a Monte Carlo bot.
func NewHardBot(seed int64, opts Options) *HardBot {
	opts = opts.withDefaults()
	b := &HardBot{opts: opts, seed: uint64(seed), h: NewTunedBot(opts.Params), mem: newTradeMemory(opts.MaxDeals)}
	for i := 0; i < opts.Workers; i++ {
		b.workers = append(b.workers, newSimWorker(opts))
	}
	return b
}

// Choose implements Bot.
func (b *HardBot) Choose(g *engine.Game, playerID string) (engine.Action, bool) {
	start := time.Now()
	b.started = start
	info := DecisionInfo{PlayerID: playerID, Phase: g.Phase}
	b.mem.observe(g, playerID)
	action, ok := b.choose(g, playerID, &info)
	if ok && action.Type == engine.ActionOfferTrade {
		b.mem.record(g, playerID, action)
	}
	info.Duration = time.Since(start)
	info.Action = action.Type
	if b.opts.OnDecision != nil {
		b.opts.OnDecision(info)
	}
	return action, ok
}

func (b *HardBot) choose(g *engine.Game, me string, info *DecisionInfo) (engine.Action, bool) {
	if a, ok := b.nextPlanned(g, me); ok {
		info.Planned = true
		return a, true
	}
	if key := turnOf(g); key != b.turnRef {
		b.turnRef = key
		b.rollMC, b.completeMC, b.lastWasTrade = 0, 0, false
	}
	hRaw := b.h.choose(g, me)
	hAct, hOK := toEngineAction(hRaw)
	if !hOK && g.Phase == engine.PhaseLoanOffer && g.LoanOffer != nil && g.LoanOffer.TargetID == me {
		hAct, hOK = engine.Action{Type: engine.ActionRejectLoan}, true
	}
	if !hOK {
		return hAct, false
	}
	if hAct.Type == engine.ActionOfferTrade && !b.mem.allowed(g, me, hAct) {
		// The heuristic's offer is held back (refused just now, or the
		// partner's deals are used up): its move without it is the baseline.
		if alt, ok := b.h.chooseNoTrade(g, me); ok {
			hAct = alt
		}
	}
	switch g.Phase {
	case engine.PhaseTrade:
		if g.Trade != nil && g.Trade.TargetID == me && b.opts.Disable&DecideTrade == 0 {
			if p := g.PlayerByID(g.Trade.ProposerID); p != nil && p.Kind == engine.KindHuman && !b.opts.NoHumanPrices {
				return b.answerHuman(g, me, info)
			}
			if b.opts.GM != nil {
				return b.answerTradeGM(g, me, hAct, info)
			}
			return b.answerTrade(g, me, hAct, info)
		}
	case engine.PhaseLoanOffer:
		// Loan answers are the heuristic's (internal/ai knows the loan
		// rules: collateral, due dates, repayment capacity).
		return hAct, true
	case engine.PhaseAuction:
		if b.opts.Disable&DecideAuction == 0 {
			return b.decideAuction(g, me, hAct, info)
		}
	case engine.PhaseAwaitingPurchase:
		if cur := engine.CurrentPlayer(g); cur != nil && cur.ID == me && b.opts.Disable&DecidePurchase == 0 {
			return b.decidePurchase(g, me, hAct, info)
		}
	case engine.PhaseAwaitingRoll:
		if cur := engine.CurrentPlayer(g); cur != nil && cur.ID == me && b.opts.Disable&DecideRoll == 0 {
			return b.decideRoll(g, me, hAct, info)
		}
	case engine.PhaseTurnComplete:
		if cur := engine.CurrentPlayer(g); cur != nil && cur.ID == me && b.opts.Disable&DecideComplete == 0 {
			return b.decideComplete(g, me, hAct, info)
		}
	}
	return hAct, true
}

// answerTrade decides a trade offered to the bot by Monte Carlo. The search
// is seeded by the offer (its deeds, cash and the turn), so the same offer in
// the same turn gets the same rollouts; and an offer of the same deeds for no
// more cash than one it refused in the last refusalMemory rounds is refused
// without a search. Repeating a bad offer no longer buys fresh dice.
func (b *HardBot) answerTrade(g *engine.Game, me string, hAct engine.Action, info *DecisionInfo) (engine.Action, bool) {
	t := g.Trade
	shape := tradeShape(t)
	now := totalTurns(g)
	reject := engine.Action{Type: engine.ActionRejectTrade}
	if b.refused == nil {
		b.refused = map[uint64]refusedOffer{}
	}
	if r, ok := b.refused[shape]; ok && now >= r.turn && now-r.turn <= refusalMemory*max(1, activeCount(g)) &&
		t.Offer.Cash <= r.offer && t.Request.Cash >= r.request {
		info.Chosen = "refused before"
		info.Heuristic = hAct.Type == engine.ActionRejectTrade
		return reject, true
	}
	b.seedOverride = mix(b.seed, shape, uint64(t.Offer.Cash), uint64(t.Request.Cash), uint64(now), 0x7ade)
	a, ok := b.decide(g, me, info, []candidate{
		binary(engine.ActionAcceptTrade, hAct),
		binary(engine.ActionRejectTrade, hAct),
	})
	b.seedOverride = 0
	if ok && a.Type == engine.ActionRejectTrade {
		b.refused[shape] = refusedOffer{offer: t.Offer.Cash, request: t.Request.Cash, turn: now}
	}
	return a, ok
}

// nextPlanned pops the next action of the current plan if it still applies.
func (b *HardBot) nextPlanned(g *engine.Game, me string) (engine.Action, bool) {
	if len(b.plan) == 0 || b.planGame != g.Code || b.planVersion != g.Version || b.planPlayer != me {
		b.plan = nil
		return engine.Action{}, false
	}
	a := b.plan[0]
	b.plan = b.plan[1:]
	b.planVersion = g.Version + 1 // PerformAction bumps the version once
	return a, true
}

func (b *HardBot) setPlan(g *engine.Game, me string, actions []engine.Action) {
	b.plan = append([]engine.Action(nil), actions...)
	b.planGame = g.Code
	b.planVersion = g.Version + 1
	b.planPlayer = me
}

// candidate is one plan to evaluate: a sequence of actions by the deciding
// player, or a custom transition (an auction won at a given price).
type candidate struct {
	name      string
	actions   []engine.Action
	apply     func(sim *engine.Game, me string) bool
	heuristic bool // the heuristic's own choice (the baseline)
}

func binary(actionType string, h engine.Action) candidate {
	return candidate{name: actionType, actions: []engine.Action{{Type: actionType}}, heuristic: h.Type == actionType}
}

func single(name string, a engine.Action) candidate {
	return candidate{name: name, actions: []engine.Action{a}}
}

// decide runs Monte Carlo over cands and returns the first action of the
// winner, keeping the rest as the plan.
func (b *HardBot) decide(g *engine.Game, me string, info *DecisionInfo, cands []candidate) (engine.Action, bool) {
	cands = dedupe(cands)
	hIdx := -1
	for i, c := range cands {
		if c.heuristic {
			hIdx = i
			break
		}
	}
	if len(cands) == 1 {
		return b.commit(g, me, cands[0], info)
	}
	if len(cands) > b.opts.MaxCandidates {
		// Keep the heuristic choice and the first alternatives (generators
		// put their strongest ideas first).
		kept := make([]candidate, 0, b.opts.MaxCandidates)
		if hIdx >= 0 {
			kept = append(kept, cands[hIdx])
		}
		for i, c := range cands {
			if i != hIdx && len(kept) < b.opts.MaxCandidates {
				kept = append(kept, c)
			}
		}
		cands = kept
		hIdx = 0
		if !cands[0].heuristic {
			hIdx = -1
		}
	}
	res := b.monteCarlo(g, me, cands, hIdx)
	info.MonteCarlo = true
	info.Rounds = res.rounds
	info.Rollouts = res.rollouts
	info.Overhead = res.start.Sub(b.started)
	info.Candidates = len(cands)
	return b.commit(g, me, cands[res.pick(hIdx, b.opts.MinRounds)], info)
}

func (b *HardBot) commit(g *engine.Game, me string, c candidate, info *DecisionInfo) (engine.Action, bool) {
	info.Chosen = c.name
	info.Heuristic = c.heuristic
	info.Kind = kindOf(c)
	if len(c.actions) == 0 {
		return engine.Action{}, false
	}
	b.setPlan(g, me, c.actions[1:])
	if c.actions[0].Type == engine.ActionOfferTrade {
		b.lastWasTrade = true
	}
	return c.actions[0], true
}

func dedupe(cands []candidate) []candidate {
	out := cands[:0]
	for _, c := range cands {
		dup := -1
		if c.apply == nil {
			for i, o := range out {
				if o.apply == nil && sameActions(o.actions, c.actions) {
					dup = i
					break
				}
			}
		}
		if dup >= 0 {
			if c.heuristic {
				out[dup].heuristic = true
				out[dup].name = c.name
			}
			continue
		}
		out = append(out, c)
	}
	return out
}

func sameActions(a, b []engine.Action) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].String() != b[i].String() {
			return false
		}
	}
	return true
}

// ------------------------------------------------------------ Monte Carlo

type mcResult struct {
	rounds   int
	rollouts int
	start    time.Time // when the first rollout began
	n        int
	scores   [][]float64 // [round][candidate]; NaN = candidate could not be applied
	active   []bool
	search   search // the settings it ran with
}

// pairStats returns the mean and standard error of score(i) - score(j)
// over the rounds where both are valid.
func (r *mcResult) pairStats(i, j int) (mean, se float64, n int) {
	var sum, sumSq float64
	for _, row := range r.scores[:r.rounds] {
		a, c := row[i], row[j]
		if math.IsNaN(a) || math.IsNaN(c) {
			continue
		}
		d := a - c
		sum += d
		sumSq += d * d
		n++
	}
	if n == 0 {
		return 0, math.Inf(1), 0
	}
	mean = sum / float64(n)
	if n > 1 {
		variance := (sumSq - sum*sum/float64(n)) / float64(n-1)
		se = math.Sqrt(math.Max(variance, 0) / float64(n))
	} else {
		se = math.Inf(1)
	}
	return mean, se, n
}

func (r *mcResult) mean(i int) (float64, int) {
	var sum float64
	n := 0
	for _, row := range r.scores[:r.rounds] {
		if !math.IsNaN(row[i]) {
			sum += row[i]
			n++
		}
	}
	if n == 0 {
		return math.Inf(-1), 0
	}
	return sum / float64(n), n
}

// leader is the active candidate with the best mean score.
func (r *mcResult) leader() int {
	best, bestMean := -1, math.Inf(-1)
	for i := 0; i < r.n; i++ {
		if !r.active[i] {
			continue
		}
		if m, n := r.mean(i); n > 0 && m > bestMean {
			best, bestMean = i, m
		}
	}
	return best
}

// pick chooses the final candidate: the leader if it beats the heuristic
// choice with enough confidence, otherwise the heuristic choice.
func (r *mcResult) pick(hIdx, minRounds int) int {
	best := r.leader()
	if best < 0 {
		if hIdx >= 0 {
			return hIdx
		}
		return 0
	}
	if hIdx < 0 || best == hIdx {
		return best
	}
	if _, n := r.mean(hIdx); n == 0 {
		return best // the heuristic's plan could not be applied
	}
	if r.rounds < minRounds {
		return hIdx
	}
	mean, se, _ := r.pairStats(best, hIdx)
	if mean > r.search.confidence*se && mean > 0 {
		return best
	}
	return hIdx
}

// search is the Monte Carlo setting of one decision.
type search struct {
	useNet     bool    // score rollouts with the value network
	horizon    int     // rollout length in rounds (Options.Horizon)
	confidence float64 // paired z-score to override the baseline
	rollouts   int     // round cap
}

// searchFor returns the setting for a decision in g: the bot's options,
// except that a value-network bot (Expert) facing a game the network cannot
// read (more than valuenet.MaxSlots players still in, e.g. 7 or 8 seats)
// searches like Hard: full-length rollouts scored by net-worth share, with
// Hard's override threshold and round cap. Once enough players are
// bankrupt the network takes over again.
func (b *HardBot) searchFor(g *engine.Game) search {
	s := search{horizon: b.opts.Horizon, confidence: b.opts.Confidence, rollouts: b.opts.Rollouts}
	if b.override != nil {
		s = *b.override
	}
	if b.opts.Eval != EvalValueNet || b.opts.Net == nil {
		return s
	}
	if valuenet.Fits(g) || b.opts.Net2 != nil {
		s.useNet = true
		return s
	}
	capRounds := hardRollouts
	if b.opts.GM != nil {
		capRounds = gmWideRollouts // Grandmaster keeps more of its thinking
	}
	s.horizon, s.confidence, s.rollouts = hardHorizon, hardConfidence, min(s.rollouts, capRounds)
	return s
}

// monteCarlo plays rounds of rollouts (one per candidate per round, all with
// the round's seed) until the rollout cap, the time budget, or a clear
// verdict.
func (b *HardBot) monteCarlo(g *engine.Game, me string, cands []candidate, hIdx int) *mcResult {
	b.count++
	decisionSeed := mix(b.seed, b.count, uint64(g.Version), hashString(me))
	if b.seedOverride != 0 {
		decisionSeed = b.seedOverride
	}
	s := b.searchFor(g)
	n := len(cands)
	res := &mcResult{n: n, scores: make([][]float64, s.rollouts), active: make([]bool, n), search: s}
	for i := range res.active {
		res.active[i] = true
	}
	res.start = time.Now()
	root := cloneGame(g, true)
	horizon := max(0, s.horizon) * max(1, activeCount(g))
	var deadline time.Time
	if b.opts.Budget > 0 {
		deadline = b.started.Add(b.opts.Budget)
	}
	workers := len(b.workers)
	var wg sync.WaitGroup
	for res.rounds < s.rollouts {
		if !deadline.IsZero() && res.rounds >= 1 && time.Now().After(deadline) {
			break
		}
		// A batch is mcBatch rounds; its rollouts (round x candidate) are
		// spread over the workers. Results depend only on the seed and the
		// batch boundaries, never on the number of workers.
		count := min(mcBatch, s.rollouts-res.rounds)
		var tasks []mcTask
		for k := 0; k < count; k++ {
			round := res.rounds + k
			res.scores[round] = make([]float64, n)
			for i := range cands {
				if !res.active[i] {
					res.scores[round][i] = math.NaN()
					continue
				}
				tasks = append(tasks, mcTask{seed: mix(decisionSeed, uint64(round)), round: round, cand: i, out: &res.scores[round][i]})
			}
		}
		// Past the deadline, remaining rollouts are skipped; a round with a
		// skipped rollout is discarded (unless it is the very first).
		run := func(w int, t *mcTask) {
			if !deadline.IsZero() && t.round > 0 && time.Now().After(deadline) {
				t.skipped = true
				return
			}
			*t.out = b.workers[w].rollout(root, me, cands[t.cand], t.seed, horizon, s.useNet)
		}
		if workers == 1 || len(tasks) == 1 {
			for k := range tasks {
				run(0, &tasks[k])
			}
		} else {
			for w := 0; w < min(workers, len(tasks)); w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					for k := w; k < len(tasks); k += workers {
						run(w, &tasks[k])
					}
				}(w)
			}
			wg.Wait()
		}
		complete := count
		for _, t := range tasks {
			if t.skipped {
				complete = min(complete, t.round-res.rounds)
			}
		}
		res.rounds += complete
		for _, t := range tasks {
			if !t.skipped && t.round < res.rounds {
				res.rollouts++
			}
		}
		if complete < count || b.settled(res, hIdx) {
			break
		}
	}
	return res
}

// mcBatch is the number of rounds between time checks and pruning.
const mcBatch = 2

type mcTask struct {
	seed    uint64
	round   int
	cand    int
	out     *float64
	skipped bool
}

// settled drops candidates that are clearly worse than the leader and
// reports whether the verdict is already clear.
func (b *HardBot) settled(res *mcResult, hIdx int) bool {
	if res.rounds < b.opts.MinRounds {
		return false
	}
	lead := res.leader()
	if lead < 0 {
		return true
	}
	alive := 0
	for i := 0; i < res.n; i++ {
		if !res.active[i] {
			continue
		}
		if _, n := res.mean(i); n == 0 {
			res.active[i] = false // cannot be applied
			continue
		}
		if i != lead && i != hIdx {
			if mean, se, _ := res.pairStats(i, lead); mean+3*se < 0 {
				res.active[i] = false
				continue
			}
		}
		alive++
	}
	if alive <= 1 {
		return true
	}
	// Two left, one of them the heuristic, and the verdict is clear.
	if alive == 2 && hIdx >= 0 && res.active[hIdx] && lead != hIdx {
		mean, se, _ := res.pairStats(lead, hIdx)
		return mean-3*se > 0 || mean+3*se < 0
	}
	return false
}

// rollout plays candidate c from root with the round's seed and scores it
// (NaN when the candidate cannot be applied).
func (w *simWorker) rollout(root *engine.Game, me string, c candidate, seed uint64, horizon int, useNet bool) float64 {
	sim := cloneGame(root, true)
	w.prepareSim(sim, seed)
	w.policy.me = me
	if !w.applyCandidate(sim, me, c) {
		return math.NaN()
	}
	w.run(sim, horizon)
	if w.rivalWeight != 0 {
		return w.scoreRival(sim, me, useNet)
	}
	return w.score(sim, me, useNet)
}

func (w *simWorker) applyCandidate(sim *engine.Game, me string, c candidate) bool {
	if c.apply != nil {
		return c.apply(sim, me)
	}
	pi := playerIndex(sim, me)
	if pi < 0 || pi >= maxSeats {
		return false
	}
	for _, a := range c.actions {
		if err := engine.PerformAction(sim, me, a, w.rngFor(pi)); err != nil {
			return false
		}
	}
	return true
}

// -------------------------------------------------------------- decisions

func (b *HardBot) decidePurchase(g *engine.Game, me string, hAct engine.Action, info *DecisionInfo) (engine.Action, bool) {
	if g.PendingPurchase == nil {
		return hAct, true
	}
	index := *g.PendingPurchase
	price := engine.Board[index].Price
	p := g.PlayerByID(me)
	cands := []candidate{{name: "heuristic:" + hAct.Type, actions: []engine.Action{hAct}, heuristic: true}}
	if p.Cash >= price {
		cands = append(cands, single("buy", engine.Action{Type: engine.ActionBuy}))
	} else if plan := raiseCash(g, me, price-p.Cash, spaceGroup[index]); plan != nil {
		cands = append(cands, candidate{name: "mortgage+buy", actions: append(plan, engine.Action{Type: engine.ActionBuy})})
	}
	cands = append(cands, single("decline", engine.Action{Type: engine.ActionDecline}))
	return b.decide(g, me, info, cands)
}

func (b *HardBot) decideAuction(g *engine.Game, me string, hAct engine.Action, info *DecisionInfo) (engine.Action, bool) {
	a := g.Auction
	p := g.PlayerByID(me)
	if a == nil || p == nil {
		return hAct, true
	}
	nextBid := max(a.CurrentBid+10, int(math.Ceil(float64(a.CurrentBid+1)/10))*10)
	pass := engine.Action{Type: engine.ActionPassAuction}
	if nextBid > p.Cash {
		return pass, true
	}
	key := auctionKey{turn: turnOf(g), space: a.SpaceIndex, turns: totalTurns(g)}
	if b.auctionRef != key {
		b.auctionRef = key
		if b.opts.GM != nil {
			// Grandmaster looks further ahead for an auction (AuctionHorizon).
			b.override = &search{horizon: b.opts.GM.AuctionHorizon, confidence: b.opts.Confidence, rollouts: b.opts.Rollouts}
		}
		b.auctionCeil = b.auctionCeiling(g, me, nextBid, info)
		b.override = nil
	}
	if nextBid <= b.auctionCeil {
		return engine.Action{Type: engine.ActionBid, Amount: nextBid}, true
	}
	return pass, true
}

// Forget drops the rest of the current plan. Call it when a decision is
// thrown away instead of played (the game moved on while the bot thought).
func (b *HardBot) Forget() { b.plan = nil }

// turnKey identifies a turn by value rather than by the *engine.Turn pointer,
// so a bot keeps its per-turn state when each decision is made on a fresh copy
// of the game (the server clones the game so the bot can think without holding
// its lock). A new turn always changes the player or the completed-turn count.
type turnKey struct {
	code   string
	player string // "" while no turn is in progress
	turns  int    // completed turns of all players
}

func turnOf(g *engine.Game) turnKey {
	key := turnKey{code: g.Code, turns: totalTurns(g)}
	if g.Turn != nil {
		key.player = g.Turn.PlayerID
	}
	return key
}

func totalTurns(g *engine.Game) int {
	n := 0
	for _, c := range g.TurnCounts {
		n += c
	}
	return n
}

// heuristicCeiling finds the highest bid the heuristic would make for the
// auctioned space (it bids the minimum raise while that is within its
// ceiling), by bisection on the current bid of its view.
func (b *HardBot) heuristicCeiling(g *engine.Game, me string) int {
	view := b.h.vb.build(g)
	if view == nil || view.Auction == nil {
		return 0
	}
	markAI(view, me)
	p := g.PlayerByID(me)
	bids := func(current int) bool {
		view.Auction.CurrentBid = current // the AI's ceiling does not depend on it
		a := ai.ChooseActionParams(view, me, b.h.params)
		return a != nil && a.Type == ai.ActionBid
	}
	if !bids(0) {
		return 0
	}
	lo, hi := 0, p.Cash // bids(lo) is true
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if bids(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return max(lo+10, int(math.Ceil(float64(lo+1)/10))*10) // the last bid it makes
}

// auctionCeiling estimates the most the space is worth to me now: the price
// at which "win it at X" stops beating "let the others have it", from
// rollouts at a few prices.
func (b *HardBot) auctionCeiling(g *engine.Game, me string, nextBid int, info *DecisionInfo) int {
	a := g.Auction
	p := g.PlayerByID(me)
	price := engine.Board[a.SpaceIndex].Price
	hc := b.heuristicCeiling(g, me)
	hi := min(p.Cash, max(nextBid+20, price*5/2, hc*3/2))
	var grid []int
	for _, x := range []int{nextBid, nextBid + (hi-nextBid)/3, nextBid + 2*(hi-nextBid)/3, hi} {
		x = min(p.Cash, max(nextBid, x/10*10))
		if len(grid) == 0 || x > grid[len(grid)-1] {
			grid = append(grid, x)
		}
	}
	cands := []candidate{{name: "pass", actions: []engine.Action{{Type: engine.ActionPassAuction}}, heuristic: true}}
	for _, x := range grid {
		x := x
		cands = append(cands, candidate{name: "win@" + itoa(x), apply: func(sim *engine.Game, me string) bool {
			return winAuction(sim, me, x)
		}})
	}
	res := b.monteCarlo(g, me, cands, 0)
	info.MonteCarlo = true
	info.Rounds = res.rounds
	info.Rollouts = res.rollouts
	info.Overhead = res.start.Sub(b.started)
	info.Candidates = len(cands)
	if res.rounds < b.opts.MinRounds {
		info.Chosen = "heuristic ceiling"
		info.Heuristic = true
		return hc
	}
	// Advantage of winning at each grid price over passing.
	diffs := make([]float64, len(grid))
	for i := range grid {
		mean, se, n := res.pairStats(i+1, 0)
		if n == 0 {
			diffs[i] = math.Inf(-1)
			continue
		}
		// Shrink towards "not worth it" by the uncertainty: overpaying for
		// a space on noise is the costly mistake.
		diffs[i] = mean - 0.5*res.search.confidence*se
	}
	ceiling := a.CurrentBid // pass
	for i := range grid {
		if diffs[i] <= 0 {
			if i > 0 && !math.IsInf(diffs[i], 0) {
				// Interpolate the break-even price between grid[i-1] and grid[i].
				prev := diffs[i-1]
				x := float64(grid[i-1]) + float64(grid[i]-grid[i-1])*prev/(prev-diffs[i])
				ceiling = int(x)
			}
			break
		}
		ceiling = grid[i]
	}
	info.Chosen = "ceiling " + itoa(ceiling)
	info.Heuristic = ceiling/10 == hc/10
	switch {
	case info.Heuristic:
		info.Kind = "ceiling = heuristic"
	case ceiling > hc:
		info.Kind = "ceiling above heuristic"
	default:
		info.Kind = "ceiling below heuristic"
	}
	return ceiling
}

// winAuction ends the simulated auction with me buying the space at price
// (endAuction with a winner).
func winAuction(sim *engine.Game, me string, price int) bool {
	a := sim.Auction
	p := sim.PlayerByID(me)
	if a == nil || p == nil || p.Cash < price || sim.Phase != engine.PhaseAuction {
		return false
	}
	p.Cash -= price
	sim.Properties[a.SpaceIndex].OwnerID = me
	sim.Auction = nil
	sim.Phase = a.ReturnPhase
	return true
}

func (b *HardBot) decideRoll(g *engine.Game, me string, hAct engine.Action, info *DecisionInfo) (engine.Action, bool) {
	p := g.PlayerByID(me)
	roll := engine.Action{Type: engine.ActionRoll}
	if b.rollMC > 0 && !b.lastWasTrade {
		return roll, true // management for this roll is done
	}
	if b.rollMC >= 3 {
		return roll, true
	}
	cands := []candidate{{name: "heuristic:" + hAct.Type, actions: b.heuristicPlan(g, me, hAct), heuristic: true}}
	cands = append(cands, single("roll", roll))
	if p.InJail {
		if p.Cash >= 50 {
			cands = append(cands, single("pay_bail", engine.Action{Type: engine.ActionPayBail}))
		}
		if p.JailCards > 0 {
			cands = append(cands, single("use_jail_card", engine.Action{Type: engine.ActionUseJailCard}))
		}
	}
	cands = append(cands, b.proposals(g, me, false)...)
	cands = append(cands, b.proposals(g, me, true)...)
	if cands = dedupe(cands); len(cands) == 1 {
		return hAct, true // the heuristic just rolls and there is nothing else
	}
	b.rollMC++
	b.lastWasTrade = false
	return b.decide(g, me, info, cands)
}

func (b *HardBot) decideComplete(g *engine.Game, me string, hAct engine.Action, info *DecisionInfo) (engine.Action, bool) {
	end := engine.Action{Type: engine.ActionEndTurn}
	if (b.completeMC > 0 && !b.lastWasTrade) || b.completeMC >= 3 {
		return end, true
	}
	cands := []candidate{{name: "heuristic:" + hAct.Type, actions: b.heuristicPlan(g, me, hAct), heuristic: true}}
	cands = append(cands, single("end_turn", end))
	cands = append(cands, b.proposals(g, me, false)...)
	cands = append(cands, buildCandidates(g, me)...)
	cands = append(cands, unmortgageCandidates(g, me)...)
	cands = append(cands, b.proposals(g, me, true)...)
	if cands = dedupe(cands); len(cands) == 1 {
		return hAct, true
	}
	b.completeMC++
	b.lastWasTrade = false
	return b.decide(g, me, info, cands)
}

// proposals are the trades the bot may offer now, less any its trade memory
// holds back: its hand-picked kinds (completing a colour group, market
// purchases and sales) or, with net, the value network's ideas (Expert).
func (b *HardBot) proposals(g *engine.Game, me string, net bool) []candidate {
	if b.mem.paused(g, me) {
		return nil
	}
	var cands []candidate
	if net {
		cands = b.netTradeCandidates(g, me)
		if b.opts.GM != nil {
			cands = append(cands, b.humanTradeCandidates(g, me)...)
		}
	} else {
		cands = append(b.tradeCandidates(g, me), b.marketCandidates(g, me)...)
	}
	return b.mem.filter(g, me, cands)
}

// heuristicPlan expands the heuristic's management move into the whole
// sequence it would play (it builds one house per call), so the candidate is
// "what the heuristic would do this turn". Trades and other moves stay single.
func (b *HardBot) heuristicPlan(g *engine.Game, me string, first engine.Action) []engine.Action {
	plan := []engine.Action{first}
	if !isManagement(first.Type) {
		return plan
	}
	scratch := cloneGame(g, true)
	scratch.NewID = newSimIDs("p")
	if engine.PerformAction(scratch, me, first, nil) != nil {
		return plan
	}
	for len(plan) < 40 {
		view := b.h.vb.build(scratch)
		if view == nil || view.Turn == nil {
			break
		}
		view.Turn.AITradeAttempted = true
		markAI(view, me)
		next, ok := toEngineAction(ai.ChooseActionParams(view, me, b.h.params))
		if !ok || !isManagement(next.Type) {
			break
		}
		if engine.PerformAction(scratch, me, next, nil) != nil {
			break
		}
		plan = append(plan, next)
	}
	return plan
}

func isManagement(t string) bool {
	switch t {
	case engine.ActionBuild, engine.ActionUnmortgage, engine.ActionMortgage, engine.ActionSellBuilding:
		return true
	}
	return false
}

// ------------------------------------------------------------ generators

// buildState tracks houses, cash and the bank's stock while planning builds.
type buildState struct {
	houses     [engine.BoardSize]int
	cash       int
	houseStock int
	hotelStock int
	built      []engine.Action
}

func newBuildState(g *engine.Game, me string) *buildState {
	s := &buildState{cash: g.PlayerByID(me).Cash}
	for _, index := range engine.OwnableIndexes {
		s.houses[index] = g.Properties[index].Houses
	}
	s.houseStock, s.hotelStock = supply(g)
	return s
}

func (s *buildState) clone() *buildState {
	c := *s
	c.built = append([]engine.Action(nil), s.built...)
	return &c
}

func (s *buildState) groupMin(grp int) int {
	m := 5
	for _, index := range groupMembers[grp] {
		m = min(m, s.houses[index])
	}
	return m
}

// buildOne adds a house (or hotel) to group grp the way the engine allows:
// on a space at the group's minimum, the most valuable one first.
func (s *buildState) buildOne(grp int) bool {
	m := s.groupMin(grp)
	if m >= 5 {
		return false
	}
	index := -1
	for _, i := range groupMembers[grp] {
		if s.houses[i] == m {
			index = i // later spaces in a group are the dearer ones
		}
	}
	cost := engine.Board[index].BuildCost
	if s.cash < cost {
		return false
	}
	if m == 4 {
		if s.hotelStock < 1 {
			return false
		}
		s.hotelStock--
		s.houseStock += 4
	} else {
		if s.houseStock < 1 {
			return false
		}
		s.houseStock--
	}
	s.houses[index]++
	s.cash -= cost
	s.built = append(s.built, engine.Action{Type: engine.ActionBuild, SpaceIndex: index})
	return true
}

// buildableGroups are my complete, unmortgaged, unpledged groups below hotels.
func buildableGroups(g *engine.Game, me string) []int {
	var out []int
	for grp := range groupMembers {
		if ownsGroup(g, me, grp) && !groupHasMortgage(g, grp) && !groupPledged(g, grp) && groupMinHouses(g, grp) < 5 {
			out = append(out, grp)
		}
	}
	return out
}

// buildCandidates proposes building plans: per group the next full tier,
// three houses each (where rents jump), everything affordable, and
// everything affordable keeping a cushion; plus spread-out plans over all
// groups, and the same plans funded by mortgaging spare holdings.
func buildCandidates(g *engine.Game, me string) []candidate {
	groups := buildableGroups(g, me)
	if len(groups) == 0 {
		return nil
	}
	var out []candidate
	base := newBuildState(g, me)
	add := func(name string, s *buildState, prefix []engine.Action) {
		if len(s.built) == 0 {
			return
		}
		actions := append(append([]engine.Action(nil), prefix...), s.built...)
		out = append(out, candidate{name: name, actions: actions})
	}
	for _, grp := range groups {
		m := base.groupMin(grp)
		key := engine.GroupOrder[grp]
		tier := base.clone()
		for tier.groupMin(grp) < m+1 && tier.buildOne(grp) {
		}
		add("build "+key+" tier", tier, nil)
		if m+1 < 3 {
			three := base.clone()
			for three.groupMin(grp) < 3 && three.buildOne(grp) {
			}
			add("build "+key+" to 3", three, nil)
		}
		all := base.clone()
		for all.buildOne(grp) {
		}
		add("build "+key+" max", all, nil)
		cushion := base.clone()
		for cushion.cash-engine.Board[groupMembers[grp][0]].BuildCost >= 200 && cushion.buildOne(grp) {
		}
		add("build "+key+" max keep 200", cushion, nil)
	}
	if len(groups) > 1 {
		for _, keep := range []int{0, 200} {
			s := base.clone()
			spread(s, groups, keep)
			add("build spread keep "+itoa(keep), s, nil)
		}
	}
	// Mortgage spare holdings to reach three houses (rents roughly triple
	// from two houses to three).
	for _, grp := range groups {
		m := base.groupMin(grp)
		if m >= 3 {
			continue
		}
		need := 0
		for _, index := range groupMembers[grp] {
			need += max(0, 3-base.houses[index]) * engine.Board[index].BuildCost
		}
		if base.cash >= need {
			continue
		}
		mortgages := raiseCash(g, me, need-base.cash, grp)
		if mortgages == nil {
			mortgages = raiseCash(g, me, -1, grp) // everything spare
		}
		if len(mortgages) == 0 {
			continue
		}
		s := base.clone()
		for _, a := range mortgages {
			s.cash += engine.Board[a.SpaceIndex].Price / 2
		}
		for s.groupMin(grp) < 3 && s.buildOne(grp) {
		}
		if len(s.built) > 0 {
			add("mortgage+build "+engine.GroupOrder[grp]+" to 3", s, mortgages)
		}
	}
	return out
}

// spread builds evenly across groups (lowest level first) while cash stays
// at or above keep.
func spread(s *buildState, groups []int, keep int) {
	for {
		bestGroup, bestLevel := -1, 6
		for _, grp := range groups {
			level := s.groupMin(grp)
			cost := engine.Board[groupMembers[grp][0]].BuildCost
			if level < 5 && s.cash-cost >= keep && level < bestLevel {
				bestGroup, bestLevel = grp, level
			}
		}
		if bestGroup < 0 || !s.buildOne(bestGroup) {
			return
		}
	}
}

// raiseCash returns mortgage actions on my least strategic spare holdings
// (never a complete group, avoidGroup, or a deed pledged on a loan, which
// the engine refuses to mortgage) raising at least need; nil when that is
// impossible. need < 0 means "all of them".
func raiseCash(g *engine.Game, me string, need, avoidGroup int) []engine.Action {
	type option struct {
		index, tier, value int
	}
	var options []option
	for _, index := range engine.OwnableIndexes {
		st := g.Properties[index]
		if st.OwnerID != me || st.Mortgaged || pledged(g, index) {
			continue
		}
		s := &engine.Board[index]
		grp := spaceGroup[index]
		tier := 0
		switch s.Type {
		case engine.SpaceProperty:
			if grp == avoidGroup || ownsGroup(g, me, grp) || groupHasBuildings(g, grp) {
				continue
			}
			owned := 0
			for _, i := range groupMembers[grp] {
				if ownerOf(g, i) == me {
					owned++
				}
			}
			if owned == len(groupMembers[grp])-1 {
				tier = 2
			}
		case engine.SpaceRailroad:
			owned := 0
			for _, i := range railroadIndexes {
				if ownerOf(g, i) == me {
					owned++
				}
			}
			tier = min(3, owned) - 1
		case engine.SpaceUtility:
			owned := 0
			for _, i := range utilityIndexes {
				if ownerOf(g, i) == me {
					owned++
				}
			}
			tier = (owned - 1) * 2
		}
		options = append(options, option{index, tier, s.Price / 2})
	}
	sort.SliceStable(options, func(i, j int) bool {
		if options[i].tier != options[j].tier {
			return options[i].tier < options[j].tier
		}
		return options[i].value > options[j].value
	})
	var out []engine.Action
	raised := 0
	for _, o := range options {
		if need >= 0 && raised >= need {
			break
		}
		out = append(out, engine.Action{Type: engine.ActionMortgage, SpaceIndex: o.index})
		raised += o.value
	}
	if need >= 0 && raised < need {
		return nil
	}
	return out
}

// unmortgageCandidates: lift the mortgages of a complete group (then build
// with what is left), or the single most useful other mortgage.
func unmortgageCandidates(g *engine.Game, me string) []candidate {
	p := g.PlayerByID(me)
	var out []candidate
	for grp := range groupMembers {
		if !ownsGroup(g, me, grp) || !groupHasMortgage(g, grp) || groupPledged(g, grp) {
			continue
		}
		cost := 0
		var actions []engine.Action
		for _, index := range groupMembers[grp] {
			if g.Properties[index].Mortgaged {
				cost += engine.UnmortgageCost(engine.Board[index].Price)
				actions = append(actions, engine.Action{Type: engine.ActionUnmortgage, SpaceIndex: index})
			}
		}
		if cost > p.Cash {
			continue
		}
		key := engine.GroupOrder[grp]
		out = append(out, candidate{name: "unmortgage " + key, actions: actions})
		s := newBuildState(g, me)
		s.cash -= cost
		for s.groupMin(grp) < 3 && s.buildOne(grp) {
		}
		if len(s.built) > 0 {
			out = append(out, candidate{name: "unmortgage+build " + key, actions: append(append([]engine.Action(nil), actions...), s.built...)})
		}
	}
	best, bestValue := -1, 0
	for _, index := range engine.OwnableIndexes {
		st := g.Properties[index]
		if st.OwnerID != me || !st.Mortgaged {
			continue
		}
		grp := spaceGroup[index]
		if grp >= 0 && ownsGroup(g, me, grp) {
			continue // handled above
		}
		cost := engine.UnmortgageCost(engine.Board[index].Price)
		if cost > p.Cash-150 {
			continue
		}
		if v := engine.Board[index].Price; v > bestValue {
			best, bestValue = index, v
		}
	}
	if best >= 0 {
		out = append(out, single("unmortgage "+engine.Board[best].Name, engine.Action{Type: engine.ActionUnmortgage, SpaceIndex: best}))
	}
	return out
}

// tradeCandidates proposes trades that complete one of my colour groups,
// priced with ai.QuoteTrade so an AI owner accepts: cash only, or cash
// plus one spare property the owner may value.
func (b *HardBot) tradeCandidates(g *engine.Game, me string) []candidate {
	if g.Turn == nil || g.Turn.AITradeAttempted || g.Trade != nil || b.opts.Disable&DecideProposals != 0 {
		return nil
	}
	p := g.PlayerByID(me)
	var out []candidate
	var view *ai.Game
	for grp := len(groupMembers) - 1; grp >= 0; grp-- {
		missing, ownerID := -1, ""
		ok := true
		for _, index := range groupMembers[grp] {
			owner := ownerOf(g, index)
			if owner == me {
				continue
			}
			if missing >= 0 || owner == "" {
				ok = false
				break
			}
			missing, ownerID = index, owner
		}
		if !ok || missing < 0 || groupHasBuildings(g, grp) || pledged(g, missing) {
			continue
		}
		owner := g.PlayerByID(ownerID)
		if owner == nil || owner.Kind != engine.KindAI || engine.AITradeOnCooldown(g, me, ownerID, []int{missing}) {
			continue
		}
		if view == nil {
			view = b.h.vb.build(g)
			if view == nil {
				return nil
			}
			markAI(view, me)
		}
		key := engine.GroupOrder[grp]
		request := []int{missing}
		if q, err := ai.QuoteTrade(view, me, ai.NewQuoteRequest(ownerID, nil, request)); err == nil && q.OfferCash > 0 && q.OfferCash <= p.Cash {
			out = append(out, single("trade cash for "+key, tradeAction(ownerID, q.OfferCash, nil, 0, request)))
		}
		// Sweeteners: one spare holding instead of (some of) the cash.
		added := 0
		for _, chip := range swapFirst(g, ownerID, spareHoldings(g, me, grp)) {
			if added >= 2 {
				break
			}
			q, err := ai.QuoteTrade(view, me, ai.NewQuoteRequest(ownerID, []int{chip}, request))
			if err != nil || q.OfferCash > p.Cash {
				continue
			}
			out = append(out, single("trade "+engine.Board[chip].Name+" for "+key,
				tradeAction(ownerID, q.OfferCash, []int{chip}, q.RequestCash, request)))
			added++
		}
	}
	return out
}

func tradeAction(targetID string, offerCash int, offer []int, requestCash int, request []int) engine.Action {
	if offer == nil {
		offer = []int{}
	}
	return engine.Action{
		Type:     engine.ActionOfferTrade,
		TargetID: targetID,
		Offer:    &engine.TradeBundle{Cash: offerCash, Properties: append([]int{}, offer...)},
		Request:  &engine.TradeBundle{Cash: requestCash, Properties: append([]int{}, request...)},
	}
}

// spareHoldings are my tradeable properties outside complete or nearly
// complete groups (and outside avoidGroup), cheapest first.
func spareHoldings(g *engine.Game, me string, avoidGroup int) []int {
	var out []int
	for _, index := range engine.OwnableIndexes {
		st := g.Properties[index]
		if st.OwnerID != me || pledged(g, index) {
			continue
		}
		grp := spaceGroup[index]
		if grp >= 0 {
			if grp == avoidGroup || groupHasBuildings(g, grp) {
				continue
			}
			owned := 0
			for _, i := range groupMembers[grp] {
				if ownerOf(g, i) == me {
					owned++
				}
			}
			if owned >= len(groupMembers[grp])-1 {
				continue
			}
		}
		out = append(out, index)
	}
	sort.SliceStable(out, func(i, j int) bool { return engine.Board[out[i]].Price < engine.Board[out[j]].Price })
	return out
}

func defaultWorkers() int { return runtime.GOMAXPROCS(0) }

// swapFirst moves to the front the chips that would complete one of the
// target's colour groups (the classic full-set-for-full-set swap).
func swapFirst(g *engine.Game, targetID string, chips []int) []int {
	out := make([]int, 0, len(chips))
	for _, c := range chips {
		if completesFor(g, targetID, c) {
			out = append(out, c)
		}
	}
	for _, c := range chips {
		if !completesFor(g, targetID, c) {
			out = append(out, c)
		}
	}
	return out
}

// kindOf names a candidate's kind for statistics: its name without the
// group or property it concerns.
func kindOf(c candidate) string {
	name := c.name
	switch {
	case c.heuristic:
		return "heuristic"
	case strings.HasPrefix(name, "net trade"):
		return "net trade"
	case strings.HasPrefix(name, "trade cash for"):
		return "trade cash"
	case strings.HasPrefix(name, "trade "):
		return "trade property+cash"
	case strings.HasPrefix(name, "unmortgage+build"):
		return "unmortgage+build"
	case strings.HasPrefix(name, "unmortgage "):
		return "unmortgage"
	case strings.HasPrefix(name, "mortgage+build"):
		return "mortgage+build to 3"
	case strings.HasPrefix(name, "build spread"):
		return name
	case strings.HasPrefix(name, "build "):
		fields := strings.Fields(name)
		return "build " + strings.Join(fields[2:], " ")
	}
	return name
}

// marketCandidates (on unless Options.NoMarketTrades) proposes plain purchases and sales
// at the AI owner's own acceptance price: buying an opponent's railroad or
// utility, and selling a spare holding (one that does not complete the
// buyer's colour group) for more than its price.
func (b *HardBot) marketCandidates(g *engine.Game, me string) []candidate {
	if b.opts.NoMarketTrades || g.Turn == nil || g.Turn.AITradeAttempted || g.Trade != nil || b.opts.Disable&DecideProposals != 0 {
		return nil
	}
	p := g.PlayerByID(me)
	view := b.h.vb.build(g)
	if view == nil {
		return nil
	}
	markAI(view, me)
	type deal struct {
		c     candidate
		value int
	}
	var buys, sells []deal
	spare := spareHoldings(g, me, -1)
	for _, t := range g.Players {
		if t.ID == me || t.Bankrupt || t.Kind != engine.KindAI {
			continue
		}
		for _, list := range [][]int{railroadIndexes, utilityIndexes} {
			for _, index := range list {
				if ownerOf(g, index) != t.ID || pledged(g, index) || engine.AITradeOnCooldown(g, me, t.ID, []int{index}) {
					continue
				}
				q, err := ai.QuoteTrade(view, me, ai.NewQuoteRequest(t.ID, nil, []int{index}))
				if err != nil || q.OfferCash <= 0 || p.Cash-q.OfferCash < 150 {
					continue
				}
				buys = append(buys, deal{single("buy "+engine.Board[index].Name, tradeAction(t.ID, q.OfferCash, nil, 0, []int{index})),
					engine.Board[index].Price - q.OfferCash})
			}
		}
		for _, index := range spare {
			// The engine cools a refused sale down on the deeds offered.
			if completesFor(g, t.ID, index) || engine.AITradeOnCooldown(g, me, t.ID, []int{index}) {
				continue
			}
			q, err := ai.QuoteTrade(view, me, ai.NewQuoteRequest(t.ID, []int{index}, nil))
			if err != nil || q.RequestCash <= engine.Board[index].Price {
				continue
			}
			sells = append(sells, deal{single("sell "+engine.Board[index].Name, tradeAction(t.ID, 0, []int{index}, q.RequestCash, nil)),
				q.RequestCash - engine.Board[index].Price})
		}
	}
	best := func(list []deal, n int) []candidate {
		sort.SliceStable(list, func(i, j int) bool { return list[i].value > list[j].value })
		var out []candidate
		for i := 0; i < len(list) && i < n; i++ {
			out = append(out, list[i].c)
		}
		return out
	}
	return append(best(buys, 2), best(sells, 2)...)
}

// completesFor reports whether index would complete targetID's colour group.
func completesFor(g *engine.Game, targetID string, index int) bool {
	grp := spaceGroup[index]
	if grp < 0 {
		return false
	}
	for _, i := range groupMembers[grp] {
		if i != index && ownerOf(g, i) != targetID {
			return false
		}
	}
	return true
}
