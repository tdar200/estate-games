package hardai

import (
	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/valuenet"
	"github.com/tdar200/estate-games/internal/valuenet2"
)

// Rollout policy.
//
// Rollouts play every seat with internal/ai, with one change for speed: the
// heuristic's trade-proposal search (chooseAiTrade, about a millisecond per
// call, i.e. ~60% of a heuristic game) is replaced by a light proposer that
// looks only for the trades that shape games, the ones completing a colour
// group, once per turn, and prices them with ai.QuoteTrade (the same
// acceptance test the AI target applies, so such offers are accepted).
// Trade answers, auctions, buying, building, jail and debt are the
// heuristic's own decisions.

// rolloutPolicy is the per-goroutine rollout player.
type rolloutPolicy struct {
	h HeuristicBot

	// Knobs of the heuristic in rollouts: mine for the deciding player's
	// seat, others for every other seat (nil = ai.DefaultParams).
	me     string
	mine   *ai.Params
	others *ai.Params

	// Opponent styles (Grandmaster): with styles set, every rollout draws a
	// style for each seat (drawStyles); seat[i] are the knobs of the player
	// with id seatIDs[i] (the deciding player still plays mine).
	styles  []OpponentStyle
	styled  bool
	seat    [maxSeats]*ai.Params
	seatIDs [maxSeats]string

	// sharp plays the player with this id with sharpParams whatever style
	// was drawn (Grandmaster's trade answers with SharpProposer: the
	// proposer of a deal is assumed to play well).
	sharp       string
	sharpParams *ai.Params

	// fullTrades plays the heuristic's own trade search instead of the light
	// proposer (slow; the oracle's whole-game rollouts only).
	fullTrades bool

	checkedTurn *engine.Turn     // turn whose light trade check is done
	quoteFail   [maxSeats][8]int // proposer turn count until which a failed quote is skipped
	request     [1]int
}

func (p *rolloutPolicy) reset() {
	p.checkedTurn = nil
	p.quoteFail = [maxSeats][8]int{}
}

// choose returns the rollout action of playerID in sim.
func (p *rolloutPolicy) choose(sim *engine.Game, playerID string) (engine.Action, bool) {
	p.h.params = p.paramsOf(playerID)
	if p.fullTrades {
		return p.h.Choose(sim, playerID)
	}
	if (sim.Phase == engine.PhaseAwaitingRoll || sim.Phase == engine.PhaseTurnComplete) &&
		sim.Turn != nil && !sim.Turn.AITradeAttempted {
		if p.checkedTurn != sim.Turn {
			p.checkedTurn = sim.Turn
			if a, ok := p.lightTrade(sim, playerID); ok {
				return a, true
			}
		}
		if a, ok := onlyMove(sim, playerID, true); ok {
			return a, true
		}
		view := p.h.vb.build(sim)
		if view == nil {
			return engine.Action{}, false
		}
		if view.Turn != nil {
			view.Turn.AITradeAttempted = true // skip the expensive search
		}
		markAI(view, playerID)
		return toEngineAction(ai.ChooseActionParams(view, playerID, p.h.params))
	}
	if a, ok := onlyMove(sim, playerID, false); ok {
		return a, true
	}
	return p.h.Choose(sim, playerID)
}

// onlyMove returns the heuristic's action without asking it, in the common
// states where its rules leave a single move (checked against ai.ChooseAction
// by TestOnlyMoveMatchesHeuristic). With trade proposals handled by the
// light proposer: confirm a drawn card; pay a debt the cash covers; and roll
// or end the turn when the player is not in jail, has no mortgage to lift
// and no complete colour group to build on. Only for default rules, where
// the heuristic's behaviour is known.
func onlyMove(sim *engine.Game, playerID string, tradesHandled bool) (engine.Action, bool) {
	if !defaultRules(sim) {
		return engine.Action{}, false
	}
	switch sim.Phase {
	case engine.PhaseCardConfirmation:
		if sim.PendingCard != nil && sim.PendingCard.PlayerID == playerID {
			return engine.Action{Type: engine.ActionConfirmCard}, true
		}
	case engine.PhaseDebt:
		if sim.Debt != nil && sim.Debt.PayerID == playerID {
			if p := sim.PlayerByID(playerID); p != nil && p.Cash >= sim.Debt.Amount {
				return engine.Action{Type: engine.ActionPayDebt}, true
			}
		}
	case engine.PhaseAwaitingRoll, engine.PhaseTurnComplete:
		p := engine.CurrentPlayer(sim)
		if p == nil || p.ID != playerID || p.Bankrupt || (p.InJail && sim.Phase == engine.PhaseAwaitingRoll) {
			return engine.Action{}, false
		}
		if !tradesHandled && (sim.Turn == nil || !sim.Turn.AITradeAttempted) {
			return engine.Action{}, false // the heuristic may still look for a trade
		}
		for _, index := range engine.OwnableIndexes {
			if st := sim.Properties[index]; st.OwnerID == playerID && st.Mortgaged {
				return engine.Action{}, false
			}
		}
		for grp := range groupMembers {
			if ownsGroup(sim, playerID, grp) {
				return engine.Action{}, false
			}
		}
		if sim.Phase == engine.PhaseAwaitingRoll {
			return engine.Action{Type: engine.ActionRoll}, true
		}
		return engine.Action{Type: engine.ActionEndTurn}, true
	}
	return engine.Action{}, false
}

func markAI(view *ai.Game, playerID string) {
	for _, pl := range view.Players {
		if pl.ID == playerID {
			pl.Kind = engine.KindAI
		}
	}
}

// lightTradeReserve mirrors the AI's minimum cash after proposing a trade.
const lightTradeReserve = 220

// lightTrade proposes a cash offer for the one space that would complete a
// colour group for the current player, if the owner accepts a price the
// proposer can afford.
func (p *rolloutPolicy) lightTrade(sim *engine.Game, proposerID string) (engine.Action, bool) {
	pi := playerIndex(sim, proposerID)
	if pi < 0 || pi >= maxSeats {
		return engine.Action{}, false
	}
	proposer := sim.Players[pi]
	if proposer.Cash <= lightTradeReserve {
		return engine.Action{}, false
	}
	turns := sim.TurnCounts[proposerID]
	for grp := len(groupMembers) - 1; grp >= 0; grp-- {
		if p.quoteFail[pi][grp] > turns {
			continue
		}
		missing, ownerID := -1, ""
		ok := true
		for _, index := range groupMembers[grp] {
			owner := ownerOf(sim, index)
			if owner == proposerID {
				continue
			}
			if missing >= 0 || owner == "" {
				ok = false
				break
			}
			missing, ownerID = index, owner
		}
		if !ok || missing < 0 || groupHasBuildings(sim, grp) || pledged(sim, missing) {
			continue
		}
		if engine.AITradeOnCooldown(sim, proposerID, ownerID, []int{missing}) {
			continue
		}
		view := p.h.vb.build(sim)
		if view == nil {
			return engine.Action{}, false
		}
		markAI(view, proposerID)
		markAI(view, ownerID)
		// One evaluation first: acceptance only grows with the cash offered,
		// so if the most the proposer can pay is refused, no quote helps.
		fee := 0
		if sim.Properties[missing].Mortgaged {
			fee = engine.MortgageTransferFee(engine.Board[missing].Price)
		}
		maxPay := proposer.Cash - lightTradeReserve - fee
		if maxPay <= 0 {
			p.quoteFail[pi][grp] = turns + 4
			continue
		}
		p.request[0] = missing
		view.Trade = &ai.Trade{
			ProposerID: proposerID, TargetID: ownerID,
			Offer:   ai.TradeBundle{Cash: maxPay, Properties: p.request[:0]},
			Request: ai.TradeBundle{Properties: p.request[:1]},
		}
		accepted := ai.AcceptsTradeParams(view, ownerID, p.paramsOf(ownerID))
		view.Trade = nil
		if !accepted {
			p.quoteFail[pi][grp] = turns + 4
			continue
		}
		quote, err := ai.QuoteTrade(view, proposerID, ai.NewQuoteRequest(ownerID, nil, []int{missing}))
		if err != nil || quote.OfferCash <= 0 || proposer.Cash-quote.OfferCash < lightTradeReserve {
			p.quoteFail[pi][grp] = turns + 4
			continue
		}
		return engine.Action{
			Type:     engine.ActionOfferTrade,
			TargetID: ownerID,
			Offer:    &engine.TradeBundle{Cash: quote.OfferCash, Properties: []int{}},
			Request:  &engine.TradeBundle{Properties: []int{missing}},
		}, true
	}
	return engine.Action{}, false
}

// simWorker owns everything one goroutine needs to run rollouts.
type simWorker struct {
	policy  rolloutPolicy
	streams [maxSeats]splitMix
	deck    splitMix
	eval    int                  // Options.Eval
	vn      *valuenet.Evaluator  // EvalValueNet
	vn2     *valuenet2.Evaluator // EvalValueNet with Options.Net2 (Grandmaster)

	// Clairvoyant analysis bots only (clairvoyant.go): peek returns a copy
	// of the real game's dice generator; rollouts then keep the real deck
	// order and roll every seat's dice from that copy (shared).
	peek   func() engine.RNG
	shared engine.RNG

	// Grandmaster's trade answers: rollouts score my win probability minus
	// rivalWeight times rival's (the proposer's).
	maxTurns    int // oracle only: whole-game rollout cap (OracleOptions.MaxTurns)
	rival       string
	rivalWeight float64
	pos         valuenet.Position
	pos2        valuenet2.Position
}

func newSimWorker(opts Options) *simWorker {
	w := &simWorker{eval: opts.Eval}
	w.policy.mine, w.policy.others = opts.Params, opts.OpponentParams
	w.policy.styles = opts.Styles
	if opts.Eval == EvalValueNet && opts.Net != nil {
		w.vn = valuenet.NewEvaluator(opts.Net)
	}
	if opts.Eval == EvalValueNet && opts.Net2 != nil {
		w.vn2 = valuenet2.NewEvaluator(opts.Net2)
	}
	return w
}

// score is the rollout value of sim for playerID: the value network's win
// probability when useNet (the decision's root fits the network, and so
// does every later state: players only drop out), otherwise the net-worth
// share (or the strategic worth share with EvalStrategic).
func (w *simWorker) score(sim *engine.Game, playerID string, useNet bool) float64 {
	if useNet && w.vn2 != nil {
		return w.vn2.WinProb(sim, playerID)
	}
	if useNet && w.vn != nil {
		return w.vn.WinProb(sim, playerID)
	}
	eval := w.eval
	if eval == EvalValueNet {
		eval = EvalNetWorth
	}
	return score(sim, playerID, eval)
}

// prepareSim turns a clone into a simulation game: every seat is played by
// the AI (so human seats are modelled by the heuristic), ids and clock are
// cheap, and the unseen deck order is reshuffled from seed (the bot must
// not peek at the real order). Every candidate of a round gets the same seed,
// hence the same decks and dice: common random numbers.
func (w *simWorker) prepareSim(sim *engine.Game, seed uint64) {
	for _, p := range sim.Players {
		p.Kind = engine.KindAI
	}
	sim.Clock = fixedClock
	sim.NewID = newSimIDs("s")
	if w.peek != nil {
		w.shared = w.peek()
		w.policy.reset()
		return
	}
	w.deck = splitMix{state: mix(seed, 0xdec)}
	shuffleStrings(sim.Decks.Chance, &w.deck)
	shuffleStrings(sim.Decks.Chest, &w.deck)
	for i := range w.streams {
		w.streams[i] = splitMix{state: mix(seed, uint64(i)+1)}
	}
	w.policy.reset()
	if len(w.policy.styles) > 0 {
		w.policy.drawStyles(sim, seed)
	}
}

func shuffleStrings(s []string, rng *splitMix) {
	for i := len(s) - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		s[i], s[j] = s[j], s[i]
	}
}

// maxRolloutSteps bounds a rollout regardless of its turn horizon.
const maxRolloutSteps = 4000

// run plays sim forward until horizonTurns more turns have started or the
// game ends. Each seat rolls with its own dice stream, so a seat's dice do
// not depend on what other seats did (better common random numbers).
func (w *simWorker) run(sim *engine.Game, horizonTurns int) {
	turns := 0
	lastTurn := sim.Turn
	failures := 0
	for step := 0; step < maxRolloutSteps && sim.Status == engine.StatusPlaying; step++ {
		trimLog(sim)
		id, orderRoll := NextActor(sim)
		if id == "" {
			if sim.Phase == engine.PhaseOrderCountdown && sim.OpeningOrder != nil && sim.OpeningOrder.CountdownEndsAt != nil {
				engine.CompleteOpeningOrder(sim, *sim.OpeningOrder.CountdownEndsAt)
				continue
			}
			return
		}
		pi := playerIndex(sim, id)
		if pi < 0 || pi >= maxSeats {
			return
		}
		rng := w.rngFor(pi)
		var action engine.Action
		if orderRoll {
			action = engine.Action{Type: engine.ActionRollForOrder}
		} else {
			var ok bool
			action, ok = w.policy.choose(sim, id)
			if !ok {
				return
			}
		}
		if err := engine.PerformAction(sim, id, action, rng); err != nil {
			// Not expected (the heuristic plays legal moves), but a refused
			// action is retried a few times (the engine may have recorded the
			// refusal, e.g. a trade naming loan collateral), then the rollout
			// stops and is scored where it stands.
			failures++
			if failures > 3 {
				return
			}
			continue
		}
		failures = 0
		if sim.Turn != lastTurn {
			lastTurn = sim.Turn
			turns++
			if turns > horizonTurns {
				return
			}
		}
	}
}

// score is the rollout value for playerID: 1 for a win, 0 once bankrupt,
// otherwise the player's share of the table's net worth (with eval
// EvalStrategic, a worth that also prices complete groups' earning power).
func score(sim *engine.Game, playerID string, eval int) float64 {
	if sim.Status == engine.StatusFinished {
		if sim.WinnerID == playerID {
			return 1
		}
		return 0
	}
	var mine, total float64
	for _, p := range sim.Players {
		var w float64
		if eval == EvalStrategic {
			w = strategicWorth(sim, p)
		} else {
			w = netWorth(sim, p)
		}
		total += w
		if p.ID == playerID {
			mine = w
		}
	}
	if total <= 0 {
		return 0
	}
	return mine / total
}

// Rollout evaluations (Options.Eval).
const (
	EvalNetWorth  = 0 // share of net worth at cost (default)
	EvalStrategic = 1 // share of net worth with complete groups and railroads at a premium
	EvalValueNet  = 2 // the value network's win probability (Options.Net)
)

// strategicWorth is netWorth plus a premium for what keeps earning: an
// unmortgaged complete colour group is worth half its price again plus a
// quarter of its buildings, and railroads gain with their count.
func strategicWorth(g *engine.Game, p *engine.Player) float64 {
	if p.Bankrupt {
		return 0
	}
	worth := netWorth(g, p)
	for grp, members := range groupMembers {
		if !ownsGroup(g, p.ID, grp) || groupHasMortgage(g, grp) {
			continue
		}
		for _, index := range members {
			s := &engine.Board[index]
			worth += 0.5*float64(s.Price) + 0.25*float64(g.Properties[index].Houses*s.BuildCost)
		}
	}
	railroads := 0
	for _, index := range railroadIndexes {
		if st := g.Properties[index]; st.OwnerID == p.ID && !st.Mortgaged {
			railroads++
		}
	}
	worth += [5]float64{0, 0, 50, 150, 300}[railroads]
	return worth
}

// paramsOf returns the knobs the rollout heuristic plays playerID with.
func (p *rolloutPolicy) paramsOf(playerID string) *ai.Params {
	if playerID == p.me && p.me != "" {
		return p.mine
	}
	if playerID == p.sharp && p.sharp != "" {
		return p.sharpParams
	}
	if p.styled {
		for i, id := range p.seatIDs {
			if id == playerID {
				return p.seat[i]
			}
		}
	}
	return p.others
}

// rngFor is the dice generator of seat pi in a rollout: its own stream, or
// the clairvoyant's copy of the real one.
func (w *simWorker) rngFor(pi int) engine.RNG {
	if w.shared != nil {
		return w.shared
	}
	return &w.streams[pi]
}
