package engine

import (
	"errors"
	"strconv"
	"strings"
)

// tradeSettlementResult is the cash outcome of a trade (tradeSettlement).
type tradeSettlementResult struct {
	proposerFee  int
	targetFee    int
	proposerCash int
	targetCash   int
}

func offerTrade(g *Game, proposerID string, action *Action) error {
	proposer, err := assertCurrent(g, proposerID)
	if err != nil {
		return err
	}
	if err := assertPhase(g, PhaseAwaitingRoll, PhaseTurnComplete); err != nil {
		return err
	}
	if g.Trade != nil {
		return errors.New("Another trade is already pending")
	}
	targetID, targetOK := action.targetID()
	var target *Player
	for _, player := range g.Players {
		if targetOK && player.ID == targetID && !player.Bankrupt && player.ID != proposer.ID {
			target = player
			break
		}
	}
	if target == nil {
		return errors.New("Invalid trade target")
	}

	// Same evaluation order as the JS object literal: id, then offer, then request.
	id := g.uid("trade")
	offer, err := normalizeTradeBundle(action.bundle("offer"))
	if err != nil {
		return err
	}
	request, err := normalizeTradeBundle(action.bundle("request"))
	if err != nil {
		return err
	}
	trade := &Trade{
		ID:          id,
		ProposerID:  proposer.ID,
		TargetID:    target.ID,
		Offer:       offer,
		Request:     request,
		ReturnPhase: g.Phase,
		CreatedAt:   g.now(),
	}
	if _, err := validateTrade(g, trade); err != nil {
		noteRefusedAITrade(g, proposer, err) // house rule: loans
		return err
	}
	if proposer.Kind == KindAI && g.Turn != nil {
		g.Turn.AITradeAttempted = true
	}
	g.Trade = trade
	g.Phase = PhaseTrade
	addLog(g, proposer.Name+" proposed a trade to "+target.Name)
	return nil
}

func normalizeTradeBundle(bundle rawBundle) (TradeBundle, error) {
	cash := bundle.cash
	if !isInteger(cash) || cash < 0 || cash > 1_000_000 {
		return TradeBundle{}, errors.New("Invalid trade cash amount")
	}
	properties := []int{}
	seen := map[int]bool{}
	for _, value := range bundle.properties {
		index, ok := boardIndex(value)
		if !ok || !IsOwnable(Board[index].Type) {
			return TradeBundle{}, errors.New("Invalid trade properties")
		}
		if !seen[index] {
			seen[index] = true
			properties = append(properties, index)
		}
	}
	return TradeBundle{Cash: int(cash), Properties: properties}, nil
}

func validateTrade(g *Game, trade *Trade) (tradeSettlementResult, error) {
	var proposer, target *Player
	for _, player := range g.Players {
		if proposer == nil && player.ID == trade.ProposerID && !player.Bankrupt {
			proposer = player
		}
		if target == nil && player.ID == trade.TargetID && !player.Bankrupt {
			target = player
		}
	}
	if proposer == nil || target == nil || proposer.ID == target.ID {
		return tradeSettlementResult{}, errors.New("The trading players are invalid")
	}
	if !hasTradeValue(trade.Offer) || !hasTradeValue(trade.Request) {
		return tradeSettlementResult{}, errors.New("Both sides must offer cash or property")
	}
	if err := validateTradeBundle(g, proposer, trade.Offer); err != nil {
		return tradeSettlementResult{}, err
	}
	if err := validateTradeBundle(g, target, trade.Request); err != nil {
		return tradeSettlementResult{}, err
	}
	if proposer.Cash < trade.Offer.Cash {
		return tradeSettlementResult{}, errors.New(proposer.Name + " does not have enough cash")
	}
	if target.Cash < trade.Request.Cash {
		return tradeSettlementResult{}, errors.New(target.Name + " does not have enough cash")
	}

	settlement := tradeSettlement(g, trade)
	if settlement.proposerCash < 0 {
		return tradeSettlementResult{}, errors.New(proposer.Name + " cannot pay the mortgage transfer fee")
	}
	if settlement.targetCash < 0 {
		return tradeSettlementResult{}, errors.New(target.Name + " cannot pay the mortgage transfer fee")
	}
	return settlement, nil
}

func hasTradeValue(bundle TradeBundle) bool {
	return bundle.Cash > 0 || len(bundle.Properties) > 0
}

func validateTradeBundle(g *Game, owner *Player, bundle TradeBundle) error {
	for _, index := range bundle.Properties {
		space := spaceAt(index)
		state := g.Properties[index]
		if state == nil || state.OwnerID != owner.ID {
			name := "Property"
			if space != nil && space.Name != "" {
				name = space.Name
			}
			return errors.New(name + " no longer belongs to " + owner.Name)
		}
		if err := collateralTradeCheck(g, index); err != nil { // house rule: loans
			return err
		}
		if space.Type == SpaceProperty {
			for _, item := range GroupSpaces(space.Group) {
				if g.Properties[item.Index].Houses > 0 {
					return errors.New(Groups[space.Group].Name + " has buildings; sell them all first")
				}
			}
		}
	}
	return nil
}

func tradeSettlement(g *Game, trade *Trade) tradeSettlementResult {
	proposer := g.PlayerByID(trade.ProposerID)
	target := g.PlayerByID(trade.TargetID)
	proposerFee := mortgageTransferFee(g, trade.Request.Properties)
	targetFee := mortgageTransferFee(g, trade.Offer.Properties)
	return tradeSettlementResult{
		proposerFee:  proposerFee,
		targetFee:    targetFee,
		proposerCash: proposer.Cash - trade.Offer.Cash + trade.Request.Cash - proposerFee,
		targetCash:   target.Cash - trade.Request.Cash + trade.Offer.Cash - targetFee,
	}
}

// MortgageTransferFee is the 10% fee to receive a mortgaged property: a tenth
// of the mortgage value (half the price), rounded up. Integer arithmetic: the
// float version of game.js, Math.ceil((price / 2) * 0.1), charges $1 too much
// whenever the product is not exact ($12 instead of $11 for a $220 deed).
func MortgageTransferFee(price int) int {
	return ceilDiv(price, 20) // ceil((price/2) / 10)
}

// UnmortgageCost is the mortgage value plus 10%, rounded up ($110 for a $200
// railroad). game.js computed Math.ceil((price / 2) * 1.1) in floats and asked
// $1 too much for deeds priced 100, 180, 200, 220 and 400.
func UnmortgageCost(price int) int {
	return ceilDiv(price*11, 20) // ceil((price/2) * 11/10)
}

// ceilDiv is ceil(a / b) for a >= 0, b > 0.
func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}

func mortgageTransferFee(g *Game, propertyIndexes []int) int {
	total := 0
	for _, index := range propertyIndexes {
		if g.Properties[index].Mortgaged {
			total += g.transferFee(Board[index].Price)
		}
	}
	return total
}

func acceptTrade(g *Game, playerID string) error {
	if err := assertPhase(g, PhaseTrade); err != nil {
		return err
	}
	if g.Trade == nil || g.Trade.TargetID != playerID {
		return errors.New("Only the recipient can accept this trade")
	}
	trade := g.Trade
	settlement, err := validateTrade(g, trade)
	if err != nil {
		return err
	}
	proposer := g.PlayerByID(trade.ProposerID)
	target := g.PlayerByID(trade.TargetID)

	proposer.Cash = settlement.proposerCash
	target.Cash = settlement.targetCash
	for _, index := range trade.Offer.Properties {
		g.Properties[index].OwnerID = target.ID
	}
	for _, index := range trade.Request.Properties {
		g.Properties[index].OwnerID = proposer.ID
	}

	g.Trade = nil
	g.Phase = trade.ReturnPhase
	addLog(g, target.Name+" accepted "+proposer.Name+"'s trade")
	if settlement.proposerFee > 0 {
		addLog(g, proposer.Name+" paid a "+money(settlement.proposerFee)+" mortgage transfer fee")
	}
	if settlement.targetFee > 0 {
		addLog(g, target.Name+" paid a "+money(settlement.targetFee)+" mortgage transfer fee")
	}
	return nil
}

func rejectTrade(g *Game, playerID string, action *Action) error {
	if err := assertPhase(g, PhaseTrade); err != nil {
		return err
	}
	trade := g.Trade
	if trade == nil || (playerID != trade.ProposerID && playerID != trade.TargetID) {
		return errors.New("The current player cannot respond to this trade")
	}
	actor := g.PlayerByID(playerID)
	cancelled := playerID == trade.ProposerID
	if !cancelled && trade.CounterTo == "" {
		// (Declining a computer's counter-offer is no refusal of its idea:
		// it only answered.)
		recordAITradeRejection(g, trade)
	}
	g.Trade = nil
	g.Phase = trade.ReturnPhase
	verb := "declined"
	if cancelled {
		verb = "cancelled"
	}
	addLog(g, actor.Name+" "+verb+" the trade")
	if reason := tradeReason(actor, action); reason != "" {
		addLog(g, actor.Name+": "+reason)
	}
	return nil
}

// maxCounterRounds is how many counter-offers one chain of offers may have.
const maxCounterRounds = 4

// counterTrade answers the pending trade with other terms (counter_trade):
// its target proposes a new trade back to its proposer, during the same
// turn. action's offer is what the counterer gives, its request what it
// asks for. The counter keeps the original's return phase, so accepting or
// declining it ends the exchange where the original offer would have.
func counterTrade(g *Game, playerID string, action *Action) error {
	if err := assertPhase(g, PhaseTrade); err != nil {
		return err
	}
	original := g.Trade
	if original == nil || original.TargetID != playerID {
		return errors.New("Only the recipient can counter this trade")
	}
	if original.Round >= maxCounterRounds {
		return errors.New("Too many counter-offers")
	}
	offer, err := normalizeTradeBundle(action.bundle("offer"))
	if err != nil {
		return err
	}
	request, err := normalizeTradeBundle(action.bundle("request"))
	if err != nil {
		return err
	}
	counter := &Trade{
		ID:          g.uid("trade"),
		ProposerID:  playerID,
		TargetID:    original.ProposerID,
		Offer:       offer,
		Request:     request,
		ReturnPhase: original.ReturnPhase,
		CreatedAt:   g.now(),
		CounterTo:   original.ID,
		Original:    &TradeTerms{Offer: original.Offer, Request: original.Request},
		Round:       original.Round + 1,
	}
	if _, err := validateTrade(g, counter); err != nil {
		return err
	}
	actor := g.PlayerByID(playerID)
	proposer := g.PlayerByID(original.ProposerID)
	g.Trade = counter
	addLog(g, actor.Name+" countered "+proposer.Name+"'s offer")
	if reason := tradeReason(actor, action); reason != "" {
		addLog(g, actor.Name+": "+reason)
	}
	return nil
}

// tradeReason is a computer's one-line reason for its answer (people say
// theirs in chat), trimmed to one short line.
func tradeReason(actor *Player, action *Action) string {
	if actor == nil || actor.Kind != KindAI || action == nil {
		return ""
	}
	reason := action.Reason
	if action.raw != nil {
		reason, _ = action.raw["reason"].(string)
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) > 120 {
		reason = reason[:120]
	}
	return reason
}

// TradeGoalKeys maps property indexes to de-duplicated cooldown goals:
// "property:<group>", "railroad", "utility" or "asset:<index>".
func TradeGoalKeys(propertyIndexes []int) []string {
	goals := []string{}
	seen := map[string]bool{}
	for _, index := range propertyIndexes {
		var goal string
		space := spaceAt(index)
		switch {
		case space != nil && space.Type == SpaceProperty:
			goal = "property:" + space.Group
		case space != nil && space.Type == SpaceRailroad:
			goal = "railroad"
		case space != nil && space.Type == SpaceUtility:
			goal = "utility"
		default:
			goal = "asset:" + strconv.Itoa(index)
		}
		if !seen[goal] {
			seen[goal] = true
			goals = append(goals, goal)
		}
	}
	return goals
}

func aiTradeCooldownKey(proposerID, targetID, goal string) string {
	return proposerID + "|" + targetID + "|" + goal
}

func recordAITradeRejection(g *Game, trade *Trade) {
	proposer := g.PlayerByID(trade.ProposerID)
	if proposer == nil || proposer.Kind != KindAI {
		return
	}
	if g.AITradeCooldowns == nil {
		g.AITradeCooldowns = map[string]int{}
	}
	completedTurns := g.TurnCounts[proposer.ID]
	goals := trade.Request.Properties
	if len(goals) == 0 && !g.legacyRules {
		// A refused sale (the computer asked only for cash) cools down on the
		// deeds it offered, so the same sale isn't offered to the same player
		// turn after turn (game.js only keyed requested deeds).
		goals = trade.Offer.Properties
	}
	for _, goal := range TradeGoalKeys(goals) {
		g.AITradeCooldowns[aiTradeCooldownKey(proposer.ID, trade.TargetID, goal)] = completedTurns + 4
	}
}

// AITradeOnCooldown reports whether an AI proposer recently had a trade for
// any of these properties' goals rejected by targetID (aiTradeOnCooldown).
func AITradeOnCooldown(g *Game, proposerID, targetID string, propertyIndexes []int) bool {
	completedTurns := g.TurnCounts[proposerID]
	for _, goal := range TradeGoalKeys(propertyIndexes) {
		expiresAt := g.AITradeCooldowns[aiTradeCooldownKey(proposerID, targetID, goal)]
		if completedTurns < expiresAt {
			return true
		}
	}
	return false
}
