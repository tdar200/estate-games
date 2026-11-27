package ai

import (
	"errors"
	"math"
	"sort"
)

// AcceptsTrade mirrors aiAcceptsTrade(game, playerId): whether playerID would
// accept the pending g.Trade (false when there is none). Like JS it does not
// check that playerID is the trade's target.
func AcceptsTrade(g *Game, playerID string) bool {
	return AcceptsTradeParams(g, playerID, nil)
}

// AcceptsTradeParams is AcceptsTrade for an AI playing with knobs p (nil =
// DefaultParams).
func AcceptsTradeParams(g *Game, playerID string, p *Params) bool {
	if g == nil || g.Trade == nil {
		return false
	}
	return newCtxParams(g, p).acceptsTrade(g.Trade, playerID)
}

func (c *ctx) acceptsTrade(t *Trade, playerID string) bool {
	tv := tradeView{
		proposerID: t.ProposerID,
		offer:      bundle{cash: t.Offer.Cash, properties: t.Offer.Properties},
		request:    bundle{cash: t.Request.Cash, properties: t.Request.Properties},
	}
	// JS throws (TypeError) evaluating a trade that names a square without
	// property state; the engine never creates one. Treat it as a rejection.
	for _, list := range [2][]int{tv.offer.properties, tv.request.properties} {
		for _, index := range list {
			if index < 0 || index >= boardSize || !c.hasProp[index] {
				return false
			}
			if c.pledged[index] {
				return false // house rule: the engine refuses to move collateral
			}
		}
	}
	return c.evaluateTrade(&tv, playerID).accepts
}

// bundle and tradeView are the AI's scratch trade objects ({proposerId,
// offer:{cash,properties}, request:{cash,properties}}).
type bundle struct {
	cash       int
	properties []int
}

type tradeView struct {
	proposerID string
	offer      bundle
	request    bundle
}

type tradeSide int

const (
	sideOffer tradeSide = iota
	sideRequest
)

func (t *tradeView) setCash(side tradeSide, cash int) {
	if side == sideOffer {
		t.offer.cash = cash
	} else {
		t.request.cash = cash
	}
}

type evaluation struct {
	accepts           bool
	targetGain        float64
	proposerGain      float64
	targetCashAfter   int
	proposerCashAfter int
}

// evaluateTrade mirrors evaluateAiTrade(game, trade, playerId) where playerId
// is the AI deciding whether to accept.
func (c *ctx) evaluateTrade(t *tradeView, playerID string) evaluation {
	target := c.findPlayer(playerID)
	proposer := c.findPlayer(t.proposerID)
	if target == nil || proposer == nil {
		return evaluation{}
	}

	targetBefore := c.ownedSet(target.ID)
	proposerBefore := c.ownedSet(proposer.ID)
	targetAfter := targetBefore.clone()
	proposerAfter := proposerBefore.clone()
	for _, index := range t.request.properties {
		targetAfter.del(index)
		proposerAfter.add(index)
	}
	for _, index := range t.offer.properties {
		proposerAfter.del(index)
		targetAfter.add(index)
	}

	targetCashAfter := target.Cash - t.request.cash + t.offer.cash - c.mortgageFee(t.offer.properties)
	proposerCashAfter := proposer.Cash - t.offer.cash + t.request.cash - c.mortgageFee(t.request.properties)
	targetLiquidityFloor := max(roundInt(c.p.LiquidityMin), int(math.Floor(mul(float64(c.adaptiveReserve(target, noGroup)), c.p.LiquidityFactor))))
	if targetCashAfter < targetLiquidityFloor || proposerCashAfter < 0 {
		return evaluation{targetCashAfter: targetCashAfter, proposerCashAfter: proposerCashAfter}
	}

	beforeHoldings := newHoldings(holding{target.ID, targetBefore}, holding{proposer.ID, proposerBefore})
	afterHoldings := newHoldings(holding{target.ID, targetAfter}, holding{proposer.ID, proposerAfter})
	targetBeforeValue := c.strategicPositionValue(target.ID, targetBefore, target.Cash, beforeHoldings)
	targetAfterValue := c.strategicPositionValue(target.ID, targetAfter, targetCashAfter, afterHoldings)
	proposerBeforeValue := c.strategicPositionValue(proposer.ID, proposerBefore, proposer.Cash, beforeHoldings)
	proposerAfterValue := c.strategicPositionValue(proposer.ID, proposerAfter, proposerCashAfter, afterHoldings)
	targetGain := targetAfterValue - targetBeforeValue
	proposerGain := proposerAfterValue - proposerBeforeValue

	activeCount := c.active
	proposerIsLeader := proposer.ID == c.currentLeaderID()
	leaderPremium := 0.0
	if proposerIsLeader {
		leaderPremium = mul(mul(weightOr(&leaderWeights, activeCount), c.p.LeaderScale), c.p.LeaderPremium)
	}
	fairnessShare := c.p.FairnessShare
	if activeCount == 2 {
		fairnessShare = c.p.FairnessShare2P
	}
	fairnessShare += leaderPremium
	createsThreeHouseThreat := c.tradeCreatesThreeHouseThreat(proposer, proposerBefore, proposerAfter, proposer.Cash, proposerCashAfter)
	if createsThreeHouseThreat && activeCount == 2 {
		fairnessShare = math.Max(fairnessShare, c.p.ThreatShare2P)
	} else if createsThreeHouseThreat && proposerIsLeader {
		fairnessShare = math.Max(fairnessShare, c.p.ThreatShareLeader)
	}
	demand := math.Max(0, mul(proposerGain, fairnessShare))
	if c.p.AcceptMargin != 0 {
		demand += c.p.AcceptMargin
	}
	return evaluation{
		accepts:           targetGain >= demand,
		targetGain:        targetGain,
		proposerGain:      proposerGain,
		targetCashAfter:   targetCashAfter,
		proposerCashAfter: proposerCashAfter,
	}
}

func (c *ctx) tradeCreatesThreeHouseThreat(proposer *Player, before, after *indexSet, beforeCash, afterCash int) bool {
	for _, g := range strongDevelopmentGroups {
		if !c.canBuildGroupToTier(proposer, g, before, beforeCash, 3) && c.canBuildGroupToTier(proposer, g, after, afterCash, 3) {
			return true
		}
	}
	return false
}

// canBuildGroupToTier mirrors canBuildGroupToTier(game, player, group,
// holdings, cash, targetTier).
func (c *ctx) canBuildGroupToTier(p *Player, g int, holdings *indexSet, cash, targetTier int) bool {
	spaces := groupSpaces[g]
	for _, s := range spaces {
		if !holdings.contains(s.index) {
			return false
		}
	}
	if c.groupHasMortgage(g) {
		return false
	}
	currentTier := c.groupMinHouses(g)
	if c.pledgedGroup[g] && currentTier < targetTier {
		return false // house rule: collateral blocks building
	}
	if currentTier >= targetTier {
		return true
	}
	requiredHouses := 0
	for _, s := range spaces {
		requiredHouses += max(0, targetTier-c.props[s.index].Houses)
	}
	if c.bankSupply().Houses < requiredHouses {
		return false
	}
	buildCost := requiredHouses * spaces[0].buildCost
	return cash-buildCost >= c.adaptiveReserve(p, g)
}

// findHighestAcceptedCash mirrors findHighestAcceptedCash(...): binary search
// for the most cash on `side` that playerID still accepts.
func (c *ctx) findHighestAcceptedCash(t *tradeView, playerID string, side tradeSide, maximum int) int {
	low, high := 0, max(0, maximum)
	for low < high {
		middle := (low + high + 1) / 2 // Math.ceil((low + high) / 2), both >= 0
		t.setCash(side, middle)
		if c.evaluateTrade(t, playerID).accepts {
			low = middle
		} else {
			high = middle - 1
		}
	}
	t.setCash(side, 0)
	return low
}

// findLowestAcceptedCash mirrors findLowestAcceptedCash(...); ok=false is JS null.
func (c *ctx) findLowestAcceptedCash(t *tradeView, playerID string, side tradeSide, maximum int) (cash int, ok bool) {
	low, high := 0, max(0, maximum)
	t.setCash(side, high)
	if !c.evaluateTrade(t, playerID).accepts {
		t.setCash(side, 0)
		return 0, false
	}
	for low < high {
		middle := (low + high) / 2 // Math.floor, both >= 0
		t.setCash(side, middle)
		if c.evaluateTrade(t, playerID).accepts {
			high = middle
		} else {
			low = middle + 1
		}
	}
	t.setCash(side, 0)
	return low, true
}

// ---------------------------------------------------------- proposals ----

// tradeablePropertyIndexes mirrors tradeablePropertyIndexes(game, playerId).
func (c *ctx) tradeablePropertyIndexes(playerID string) []int {
	var out []int
	for i := 0; i < boardSize; i++ {
		if !c.hasProp[i] || c.props[i].OwnerID != playerID {
			continue
		}
		s := &board[i]
		if s.kind == typeProperty && c.groupHasBuildings(s.group) {
			continue
		}
		if c.pledged[i] {
			continue // house rule: collateral cannot be traded
		}
		out = append(out, i)
	}
	return out
}

func (c *ctx) tradeChipScore(playerID string, index int) int {
	s := &board[index]
	typePriority := 2
	switch s.kind {
	case typeUtility:
		typePriority = 0
	case typeRailroad:
		typePriority = 1
	}
	return typePriority*100_000 + c.assetStrategicTier(playerID, s)*10_000 + s.price
}

// tradeOfferBundles mirrors tradeOfferBundles: the empty bundle, then every
// single chip, then every pair, from the 12 cheapest chips.
func (c *ctx) tradeOfferBundles(proposerID string, indexes []int) [][]int {
	type chip struct{ index, score int }
	chips := make([]chip, len(indexes))
	for i, index := range indexes {
		chips[i] = chip{index, c.tradeChipScore(proposerID, index)}
	}
	sort.SliceStable(chips, func(i, j int) bool { return chips[i].score-chips[j].score < 0 })
	if len(chips) > maxTradeChips {
		chips = chips[:maxTradeChips]
	}
	bundles := [][]int{{}}
	for _, ch := range chips {
		bundles = append(bundles, []int{ch.index})
	}
	for left := 0; left < len(chips); left++ {
		for right := left + 1; right < len(chips); right++ {
			bundles = append(bundles, []int{chips[left].index, chips[right].index})
		}
	}
	return bundles
}

type proposal struct {
	targetID string
	offer    TradeBundle
	request  TradeBundle
	score    float64
}

// chooseAiTrade mirrors chooseAiTrade(game, proposer).
func (c *ctx) chooseAiTrade(proposer *Player) *Action {
	if c.g.Turn != nil && c.g.Turn.AITradeAttempted {
		return nil
	}
	proposerAssets := c.tradeablePropertyIndexes(proposer.ID)
	offerBundles := c.tradeOfferBundles(proposer.ID, proposerAssets)
	var candidates []proposal

	for _, target := range c.g.Players {
		if target.ID == proposer.ID || target.Bankrupt {
			continue
		}
		for _, requestIndex := range c.tradeablePropertyIndexes(target.ID) {
			request := []int{requestIndex}
			for _, offerProperties := range offerBundles {
				if p, ok := c.proposalCandidate(proposer, target, offerProperties, request); ok {
					candidates = append(candidates, p)
				}
			}
		}
	}

	// .sort((a, b) => b.score - a.score || a.offer.cash - b.offer.cash)
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := &candidates[i], &candidates[j]
		if d := b.score - a.score; d != 0 && !math.IsNaN(d) {
			return d < 0
		}
		return a.offer.Cash-b.offer.Cash < 0
	})
	if len(candidates) == 0 {
		return nil
	}
	best := candidates[0]
	return &Action{
		Type:     ActionOfferTrade,
		TargetID: best.targetID,
		Offer:    &best.offer,
		Request:  &best.request,
	}
}

// proposalCandidate mirrors aiProposalCandidate(...); ok=false is JS null.
func (c *ctx) proposalCandidate(proposer, target *Player, offerProperties, requestProperties []int) (proposal, bool) {
	if !c.requestedPropertiesCompleteGroups(proposer.ID, offerProperties, requestProperties) {
		return proposal{}, false
	}
	if aiTradeOnCooldown(c.g, proposer.ID, target.ID, requestProperties) {
		return proposal{}, false
	}
	incomingFee := c.mortgageFee(requestProperties)
	proposerReserve := max(roundInt(c.p.TradeReserve), c.adaptiveReserve(proposer, noGroup))
	maximumPayment := min(maxTradeCash, proposer.Cash-incomingFee-proposerReserve)
	if maximumPayment < 0 {
		return proposal{}, false
	}

	t := tradeView{
		proposerID: proposer.ID,
		offer:      bundle{properties: offerProperties},
		request:    bundle{properties: requestProperties},
	}
	// The target's acceptance is modelled with the default knobs.
	opp := c.oppCtx()
	offerCash, ok := opp.findLowestAcceptedCash(&t, target.ID, sideOffer, maximumPayment)
	if !ok || (offerCash == 0 && len(offerProperties) == 0) {
		return proposal{}, false
	}
	t.offer.cash = offerCash
	ev := opp.evaluateTrade(&t, target.ID)
	if !ev.accepts {
		return proposal{}, false
	}
	gain := ev.proposerGain
	if opp != c {
		gain = c.proposerGain(&t, target.ID)
	}
	if gain < c.p.MinTradeGain {
		return proposal{}, false
	}
	if ev.proposerCashAfter < proposerReserve {
		return proposal{}, false
	}
	return proposal{
		targetID: target.ID,
		offer:    TradeBundle{Cash: offerCash, Properties: append([]int{}, offerProperties...)},
		request:  TradeBundle{Cash: 0, Properties: append([]int{}, requestProperties...)},
		score:    gain,
	}, true
}

// proposerGain is the proposer's gain from t valued with c's knobs (the
// proposerGain of evaluateTrade, without its acceptance checks).
func (c *ctx) proposerGain(t *tradeView, targetID string) float64 {
	target := c.findPlayer(targetID)
	proposer := c.findPlayer(t.proposerID)
	if target == nil || proposer == nil {
		return 0
	}
	targetBefore := c.ownedSet(target.ID)
	proposerBefore := c.ownedSet(proposer.ID)
	targetAfter := targetBefore.clone()
	proposerAfter := proposerBefore.clone()
	for _, index := range t.request.properties {
		targetAfter.del(index)
		proposerAfter.add(index)
	}
	for _, index := range t.offer.properties {
		proposerAfter.del(index)
		targetAfter.add(index)
	}
	proposerCashAfter := proposer.Cash - t.offer.cash + t.request.cash - c.mortgageFee(t.request.properties)
	beforeHoldings := newHoldings(holding{target.ID, targetBefore}, holding{proposer.ID, proposerBefore})
	afterHoldings := newHoldings(holding{target.ID, targetAfter}, holding{proposer.ID, proposerAfter})
	return c.strategicPositionValue(proposer.ID, proposerAfter, proposerCashAfter, afterHoldings) -
		c.strategicPositionValue(proposer.ID, proposerBefore, proposer.Cash, beforeHoldings)
}

// requestedPropertiesCompleteGroups mirrors the JS helper: every requested
// colour property must complete its group for the proposer after the trade.
func (c *ctx) requestedPropertiesCompleteGroups(proposerID string, offerProperties, requestProperties []int) bool {
	var requested []*space
	for _, index := range requestProperties {
		if s := spaceAt(index); s != nil && s.kind == typeProperty {
			requested = append(requested, s)
		}
	}
	if len(requested) == 0 {
		return true
	}
	holdings := c.ownedSet(proposerID)
	for _, index := range offerProperties {
		holdings.del(index)
	}
	for _, index := range requestProperties {
		holdings.add(index)
	}
	for _, s := range requested {
		for _, candidate := range groupSpaces[s.group] {
			if !holdings.contains(candidate.index) {
				return false
			}
		}
	}
	return true
}

// -------------------------------------------------------------- quotes ----

// QuoteTrade mirrors quoteAiTrade(game, proposerId, action): the cash that
// makes the AI accept the proposed property swap. Errors carry the exact JS
// messages.
func QuoteTrade(g *Game, proposerID string, req QuoteRequest) (Quote, error) {
	if g == nil || g.Status != "playing" {
		return Quote{}, errors.New("The game has not started")
	}
	c := newCtx(g)
	var proposer *Player
	for _, p := range g.Players {
		if p.ID == proposerID && !p.Bankrupt {
			proposer = p
			break
		}
	}
	if proposer == nil || g.CurrentIndex < 0 || g.CurrentIndex >= len(g.Players) || g.Players[g.CurrentIndex].ID != proposerID {
		return Quote{}, errors.New("Only the current player can request a quote")
	}
	if g.Phase != "awaiting_roll" && g.Phase != "turn_complete" {
		return Quote{}, errors.New("Trading is not available during this phase")
	}

	var target *Player
	if targetID, ok := req.targetID(); ok {
		for _, p := range g.Players {
			if p.ID == targetID && p.Kind == "ai" && !p.Bankrupt {
				target = p
				break
			}
		}
	}
	if target == nil || target.ID == proposerID {
		return Quote{}, errors.New("Quotes are only available from an active AI player")
	}

	offerProperties, err := normalizeQuoteProperties(req.OfferProperties)
	if err != nil {
		return Quote{}, err
	}
	requestProperties, err := normalizeQuoteProperties(req.RequestProperties)
	if err != nil {
		return Quote{}, err
	}
	if len(offerProperties) == 0 && len(requestProperties) == 0 {
		return Quote{}, errors.New("Select properties to trade first")
	}
	if err := c.validateQuoteProperties(proposer, offerProperties); err != nil {
		return Quote{}, err
	}
	if err := c.validateQuoteProperties(target, requestProperties); err != nil {
		return Quote{}, err
	}

	t := tradeView{
		proposerID: proposerID,
		offer:      bundle{properties: offerProperties},
		request:    bundle{properties: requestProperties},
	}
	zeroCashAccepted := c.evaluateTrade(&t, target.ID).accepts

	if zeroCashAccepted && len(offerProperties) > 0 {
		reserveFloor := max(180, int(math.Floor(mul(float64(c.adaptiveReserve(target, noGroup)), 0.7))))
		maximumPayout := min(maxTradeCash, target.Cash-c.mortgageFee(offerProperties)-reserveFloor)
		requestCash := c.findHighestAcceptedCash(&t, target.ID, sideRequest, maximumPayout)
		if requestCash > 0 || len(requestProperties) > 0 {
			return Quote{OfferCash: 0, RequestCash: requestCash, TargetID: target.ID}, nil
		}
	}

	if len(requestProperties) == 0 {
		return Quote{}, errors.New("The AI is not willing to buy the selected properties")
	}
	maximumPayment := min(maxTradeCash, proposer.Cash-c.mortgageFee(requestProperties))
	offerCash, ok := c.findLowestAcceptedCash(&t, target.ID, sideOffer, maximumPayment)
	if !ok {
		return Quote{}, errors.New("You do not have enough cash to complete this trade with the AI")
	}
	return Quote{OfferCash: offerCash, RequestCash: 0, TargetID: target.ID}, nil
}

// validateQuoteProperties mirrors validateQuoteProperties(game, owner, indexes).
func (c *ctx) validateQuoteProperties(owner *Player, indexes []int) error {
	for _, index := range indexes {
		s := &board[index]
		if !c.hasProp[index] || c.props[index].OwnerID != owner.ID {
			return errors.New(s.name + " no longer belongs to " + owner.Name)
		}
		if c.pledged[index] {
			return errors.New(s.name + " is security for a loan") // engine's message
		}
		if s.kind == typeProperty && c.groupHasBuildings(s.group) {
			return errors.New(s.name + " belongs to a color group with buildings and cannot be traded")
		}
	}
	return nil
}
