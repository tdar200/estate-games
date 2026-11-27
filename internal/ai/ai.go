package ai

import (
	"math"
	"sort"
)

// ChooseAction mirrors chooseAiAction(game, playerId): the action the AI
// player playerID takes now, or nil when JS returns null (not an AI, not its
// turn, or nothing to do in this phase).
//
// The decision is a pure function of g except for one documented side effect
// copied from JS: during an auction g.Auction.TurnPos is normalised to the seat
// that is due (see auctionCurrentPlayerID).
func ChooseAction(g *Game, playerID string) *Action {
	return ChooseActionParams(g, playerID, nil)
}

// ChooseActionParams is ChooseAction for an AI playing with knobs p (nil or
// DefaultParams: exactly ChooseAction).
func ChooseActionParams(g *Game, playerID string, p *Params) *Action {
	if g == nil {
		return nil
	}
	c := newCtxParams(g, p)
	player := c.findPlayer(playerID)
	if player == nil || player.Kind != "ai" || player.Bankrupt {
		return nil
	}

	switch g.Phase {
	case "card_confirmation":
		if g.PendingCard == nil || g.PendingCard.PlayerID != playerID {
			return nil
		}
		return &Action{Type: ActionConfirmCard}
	case "trade":
		if g.Trade == nil || g.Trade.TargetID != playerID {
			return nil
		}
		if c.acceptsTrade(g.Trade, playerID) {
			return &Action{Type: ActionAcceptTrade}
		}
		return &Action{Type: ActionRejectTrade}
	case "auction":
		if g.Auction != nil && g.Auction.ClockMs > 0 {
			// Timed open auction (not in game.js): anyone still in who
			// does not hold the top bid may bid or pass at any moment.
			if !openAuctionBidder(g.Auction, playerID) {
				return nil
			}
		} else if auctionCurrentPlayerID(g) != playerID || playerID == "" {
			return nil
		}
		s := spaceAt(g.Auction.SpaceIndex)
		if s == nil || !c.hasProp[s.index] {
			// JS would throw reading BOARD[index].type / properties; the engine
			// never auctions a non-ownable square.
			return &Action{Type: ActionPassAuction}
		}
		ceiling := c.auctionCeiling(player, s)
		bid := g.Auction.CurrentBid
		nextBid := max(bid+10, int(math.Ceil(float64(bid+1)/10))*10)
		if float64(nextBid) <= ceiling && nextBid <= player.Cash {
			return &Action{Type: ActionBid, Amount: nextBid}
		}
		return &Action{Type: ActionPassAuction}
	case "loan_offer": // house rule "loans": only the offer's target answers
		return c.chooseLoanResponse(player)
	}

	if g.CurrentIndex < 0 || g.CurrentIndex >= len(g.Players) || g.Players[g.CurrentIndex].ID != playerID {
		return nil
	}

	switch g.Phase {
	case "debt":
		if a := c.chooseLoanRequest(player); a != nil { // house rule "loans"
			return a
		}
		return c.chooseDebtAction(player)
	case "awaiting_purchase":
		var s *space
		if g.PendingPurchase != nil {
			s = spaceAt(*g.PendingPurchase)
			if s != nil && !c.hasProp[s.index] {
				s = nil // JS would throw; never produced by the engine
			}
		}
		if c.shouldBuy(player, s) || c.shouldBuyWithoutAuction(player, s) {
			return &Action{Type: ActionBuy}
		}
		if a := c.chooseMortgageForPurchase(player, s); a != nil {
			return a
		}
		return &Action{Type: ActionDecline}
	case "awaiting_roll":
		if a := c.chooseAiTrade(player); a != nil {
			return a
		}
		if a := c.chooseUnmortgage(player); a != nil {
			return a
		}
		if a := c.chooseUnblockingRepayment(player); a != nil { // house rule "loans"
			return a
		}
		if a := c.chooseBuild(player); a != nil {
			return a
		}
		if a := c.chooseJailAction(player); a != nil {
			return a
		}
		return &Action{Type: ActionRoll}
	case "turn_complete":
		if a := c.chooseAiTrade(player); a != nil {
			return a
		}
		if a := c.chooseUnmortgage(player); a != nil {
			return a
		}
		if a := c.chooseUnblockingRepayment(player); a != nil { // house rule "loans"
			return a
		}
		if a := c.chooseBuild(player); a != nil {
			return a
		}
		if a := c.chooseDueRepayment(player); a != nil { // house rule "loans"
			return a
		}
		return &Action{Type: ActionEndTurn}
	}
	return nil
}

// ChooseDebtAction is how the AI gets out of its own debt without asking for
// a loan: pay_debt when its cash covers the debt, otherwise the best
// sell_building or mortgage (the same choice ChooseAction makes once a loan
// is out of the question), and bankrupt only when nothing is left to sell or
// mortgage. It is for a caller that cannot send the loan request ChooseAction
// may make first under the house rule "loans" (offer_loan), such as the
// server's stand-in for an away person, or whose request was refused.
//
// nil unless g is in the debt phase with playerID on turn and owing the
// debt. Unlike ChooseAction it does not need the seat to be marked "ai".
func ChooseDebtAction(g *Game, playerID string) *Action {
	if g == nil || g.Phase != "debt" || g.Debt == nil || g.Debt.PayerID != playerID {
		return nil
	}
	if g.CurrentIndex < 0 || g.CurrentIndex >= len(g.Players) || g.Players[g.CurrentIndex].ID != playerID {
		return nil
	}
	c := newCtxParams(g, nil)
	player := c.findPlayer(playerID)
	if player == nil || player.Bankrupt {
		return nil
	}
	return c.chooseDebtAction(player)
}

// ------------------------------------------------------------- buying ----

func (c *ctx) shouldBuy(p *Player, s *space) bool {
	if s == nil || p.Cash < s.price {
		return false
	}
	return c.scaledCeiling(p, s, c.p.BuyScale) >= float64(s.price)
}

func (c *ctx) completesGroup(playerID string, g int) bool {
	missing := 0
	for _, s := range groupSpaces[g] {
		if c.props[s.index].OwnerID != playerID {
			missing++
		}
	}
	return missing == 1
}

// auctionCeiling mirrors auctionCeiling(game, player, space): the most the AI
// would pay. It is an integer-valued float64 (or NaN/±Inf exactly where JS
// would produce them).
func (c *ctx) auctionCeiling(p *Player, s *space) float64 {
	return c.scaledCeiling(p, s, c.p.AuctionScale)
}

// scaledCeiling is auctionCeiling with the value scaled by a knob (exactly
// auctionCeiling at 1).
func (c *ctx) scaledCeiling(p *Player, s *space, scale float64) float64 {
	value, hardCap, reserve := c.auctionValue(p, s)
	if scale != 1 {
		value = mul(value, scale)
	}
	return math.Max(0, math.Min(math.Min(math.Floor(value), hardCap), float64(p.Cash-reserve)))
}

// auctionValue is the part of auctionCeiling before the cash cap: the AI's
// value of the space, the hard cap on what it pays, and the cash reserve it
// keeps (the ceiling is min(floor(value), hardCap, cash - reserve), >= 0).
func (c *ctx) auctionValue(p *Player, s *space) (value, hardCap float64, reserve int) {
	ownedOfType := 0
	for i := 0; i < boardSize; i++ {
		if board[i].kind == s.kind && c.hasProp[i] && c.props[i].OwnerID == p.ID {
			ownedOfType++
		}
	}
	value = mul(float64(s.price), c.p.BaseValue)
	completionGroup := noGroup
	hardCap = math.Inf(1)
	opponents := float64(max(1, c.active-1))

	switch {
	case s.group != noGroup:
		groupSize := len(groupSpaces[s.group])
		ownedInGroup := c.groupOwnedCount(p.ID, s.group)
		if ownedInGroup == groupSize-1 {
			completionGroup = s.group
			value = mul(float64(s.price), c.p.GroupComplete[s.group])
			tier := c.reachableGroupTier(p, s.group, p.Cash-s.price)
			value += mul(mul(mul(groupStageEV[s.group][tier], buildabilityWeights[tier]), opponents), c.p.EVCashFactor)
		} else if ownedInGroup > 0 {
			value = mul(float64(s.price), c.p.GroupPartial[s.group])
		}
	case s.kind == typeRailroad:
		nextCount := min(4, ownedOfType+1)
		value = float64(s.price) + mul(mul(evAt(railroadEV[:], nextCount)-evAt(railroadEV[:], ownedOfType), opponents), c.p.RailroadEV)
		if nextCount == 4 && c.hasStrongGroupWaitingToBuild(p.ID) {
			hardCap = mul(900, c.p.RailroadCapScale)
		} else {
			hardCap = mul(railroadHardCaps[nextCount], c.p.RailroadCapScale)
		}
	case s.kind == typeUtility:
		nextCount := min(2, ownedOfType+1)
		value = mul(float64(s.price), c.p.UtilityBase) + mul(mul(evAt(utilityEV[:], nextCount)-evAt(utilityEV[:], ownedOfType), opponents), c.p.UtilityEV)
	}

	value += c.auctionDenialValue(p, s)
	reserve = c.adaptiveReserve(p, completionGroup)
	return value, hardCap, reserve
}

func (c *ctx) hasStrongGroupWaitingToBuild(playerID string) bool {
	for _, g := range strongDevelopmentGroups {
		if !c.groupOwnedBy(playerID, g) || c.groupHasMortgage(g) || c.pledgedGroup[g] {
			continue
		}
		if c.groupMinHouses(g) < 3 {
			return true
		}
	}
	return false
}

// auctionDenialValue mirrors auctionDenialValue(game, player, space).
func (c *ctx) auctionDenialValue(p *Player, s *space) float64 {
	denialWeight := mul(weightOr(&denialWeights, c.active), c.p.DenialScale)
	leaderID := c.currentLeaderID()
	best := 0.0

	for _, opponent := range c.g.Players {
		if opponent.Bankrupt || opponent.ID == p.ID {
			continue
		}
		threat := 0.0
		if s.group != noGroup && c.completesGroup(opponent.ID, s.group) {
			tier := c.reachableGroupTier(opponent, s.group, opponent.Cash-s.price)
			threat = mul(mul(mul(
				groupStageEV[s.group][max(3, tier)],
				buildabilityWeights[max(1, tier)]),
				c.p.EVCashFactor),
				blockerGroupWeights[s.group])
		} else if s.kind == typeRailroad {
			owned := c.countOwned(railroadSpaces, opponent.ID, false)
			if owned >= 2 {
				threat = mul(evAt(railroadEV[:], min(4, owned+1))-evAt(railroadEV[:], owned), 12)
			}
		}
		if opponent.ID == leaderID {
			threat = mul(threat, 1+mul(weightOr(&leaderWeights, c.active), c.p.LeaderScale))
		}
		best = math.Max(best, mul(threat, denialWeight))
	}
	return best
}

// chooseMortgageForPurchase mirrors chooseMortgageForPurchase(game, player, space).
func (c *ctx) chooseMortgageForPurchase(p *Player, s *space) *Action {
	if s == nil || p.Cash >= s.price || !c.isStrategicPurchase(p.ID, s) {
		return nil
	}
	shortfall := s.price - p.Cash
	candidates := c.mortgageCandidates(p.ID, s.group)
	available := 0
	for _, candidate := range candidates {
		available += candidate.price / 2
	}
	if available < shortfall || len(candidates) == 0 {
		return nil
	}
	return &Action{Type: ActionMortgage, SpaceIndex: candidates[0].index}
}

func (c *ctx) isStrategicPurchase(playerID string, s *space) bool {
	switch {
	case s.group != noGroup:
		return c.completesGroup(playerID, s.group)
	case s.kind == typeRailroad:
		return c.countOwned(railroadSpaces, playerID, false) >= 2
	case s.kind == typeUtility:
		return c.countOwned(utilitySpaces, playerID, false) >= 1
	}
	return false
}

// mortgageCandidates mirrors mortgageCandidates(game, playerId, avoidGroup):
// unmortgaged, building-free holdings sorted with avoidGroup last, then by
// mortgageLiquidationScore descending (stable, like Array.prototype.sort).
// avoidGroup noGroup stands for the JS default null, which matches nothing.
// With the house rule "loans" it also leaves out deeds pledged on an active
// loan, which the engine refuses to mortgage (no JS counterpart; nothing is
// pledged in a default game).
func (c *ctx) mortgageCandidates(playerID string, avoidGroup int) []*space {
	type candidate struct {
		s       *space
		avoided int
		score   float64
	}
	var list []candidate
	for i := 0; i < boardSize; i++ {
		if !c.hasProp[i] {
			continue
		}
		st := &c.props[i]
		s := &board[i]
		if st.OwnerID != playerID || st.Mortgaged || c.pledged[i] {
			continue
		}
		if s.kind == typeProperty && c.groupHasBuildings(s.group) {
			continue
		}
		avoided := 0
		if avoidGroup != noGroup && s.group == avoidGroup {
			avoided = 1
		}
		list = append(list, candidate{s, avoided, c.mortgageLiquidationScore(playerID, s)})
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.avoided != b.avoided {
			return a.avoided-b.avoided < 0
		}
		return b.score-a.score < 0
	})
	out := make([]*space, len(list))
	for i := range list {
		out[i] = list[i].s
	}
	return out
}

// mortgageLiquidationScore mirrors mortgageLiquidationScore(game, playerId, space).
func (c *ctx) mortgageLiquidationScore(playerID string, s *space) float64 {
	cashRaised := float64(s.price / 2)
	opponents := float64(max(1, c.active-1))
	strategicLoss := 1.0
	switch {
	case s.kind == typeRailroad:
		owned := c.countOwned(railroadSpaces, playerID, true)
		strategicLoss += mul(mul(evAt(railroadEV[:], owned)-evAt(railroadEV[:], max(0, owned-1)), opponents), c.p.EVCashFactor)
	case s.kind == typeUtility:
		owned := c.countOwned(utilitySpaces, playerID, true)
		strategicLoss += mul(mul(evAt(utilityEV[:], owned)-evAt(utilityEV[:], max(0, owned-1)), opponents), c.p.EVCashFactor)
	case s.group != noGroup:
		size := len(groupSpaces[s.group])
		owned := c.groupOwnedCount(playerID, s.group)
		if owned == size {
			strategicLoss += mul(mul(groupStageEV[s.group][0], opponents), c.p.EVCashFactor)
			switch s.group {
			case groupOrange, groupRed, groupYellow, groupDarkBlue:
				strategicLoss += c.p.LiquidateStrong
			default:
				strategicLoss += c.p.LiquidateWeak
			}
		} else if owned == size-1 {
			strategicLoss += c.p.LiquidateNear
		}
	}
	return cashRaised / strategicLoss
}

// assetStrategicTier mirrors assetStrategicTier(game, playerId, space).
func (c *ctx) assetStrategicTier(playerID string, s *space) int {
	switch {
	case s.group != noGroup:
		size := len(groupSpaces[s.group])
		owned := c.groupOwnedCount(playerID, s.group)
		if owned == size {
			return 4
		}
		if owned == size-1 {
			return 2
		}
		return 1
	case s.kind == typeRailroad:
		owned := c.countOwned(railroadSpaces, playerID, false)
		if owned >= 3 {
			return 3
		}
		if owned >= 2 {
			return 2
		}
		return 1
	case s.kind == typeUtility:
		if c.countOwned(utilitySpaces, playerID, false) == 2 {
			return 3
		}
		return 1
	}
	return 0
}

// ---------------------------------------------------------- buildings ----

// chooseBuild mirrors chooseBuild(game, player).
func (c *ctx) chooseBuild(p *Player) *Action {
	type candidate struct {
		index int
		score float64
	}
	var candidates []candidate
	supply := c.bankSupply()
	reserve := scaleInt(c.adaptiveReserve(p, noGroup), c.p.BuildReserve)
	protectHouseShortage := supply.Houses <= 8 && c.opponentHasBuildableGroup(p.ID)

	for g := 0; g < groupCount; g++ {
		spaces := groupSpaces[g]
		if !c.groupOwnedBy(p.ID, g) || c.groupHasMortgage(g) {
			continue
		}
		if c.pledgedGroup[g] {
			continue // house rule: the engine refuses to build on a group holding collateral
		}
		minimum := c.groupMinHouses(g)
		if minimum >= 5 {
			continue
		}
		targetTier := minimum + 1
		fullTierCost := len(spaces) * spaces[0].buildCost

		stageValues := &groupStageEV[g]
		stageGain := stageValues[targetTier] - stageValues[minimum]
		tierMultiplier := c.p.BuildTierHigh
		if targetTier == 3 {
			tierMultiplier = c.p.BuildTierThree
		} else if targetTier < 3 {
			tierMultiplier = c.p.BuildTierLow
		}
		shortageBonus := 1.0
		if targetTier < 5 && supply.Houses <= 12 {
			shortageBonus = c.p.ShortageBonus
		}
		hotelReleasePenalty := 1.0
		if targetTier == 5 && protectHouseShortage {
			hotelReleasePenalty = c.p.HotelPenalty
		}
		bundleScore := mul(mul(mul(stageGain/float64(fullTierCost), tierMultiplier), shortageBonus), hotelReleasePenalty)

		for _, s := range spaces {
			st := &c.props[s.index]
			if st.Houses != minimum {
				continue
			}
			canBuySingleBuilding := supply.Houses >= 1
			if targetTier == 5 {
				canBuySingleBuilding = supply.Hotels >= 1
			}
			if !canBuySingleBuilding || p.Cash-s.buildCost < reserve {
				continue
			}
			gain := s.rents[st.Houses+1] - s.rents[st.Houses]
			candidates = append(candidates, candidate{
				index: s.index,
				score: mul(bundleScore, 1000) + float64(gain)/float64(s.buildCost),
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[j].score-candidates[i].score < 0 })
	if len(candidates) == 0 {
		return nil
	}
	return &Action{Type: ActionBuild, SpaceIndex: candidates[0].index}
}

func (c *ctx) opponentHasBuildableGroup(playerID string) bool {
	for _, opponent := range c.g.Players {
		if opponent.Bankrupt || opponent.ID == playerID {
			continue
		}
		for g := 0; g < groupCount; g++ {
			if !c.groupOwnedBy(opponent.ID, g) || c.groupHasMortgage(g) || c.pledgedGroup[g] {
				continue
			}
			if c.groupMinHouses(g) >= 4 {
				continue
			}
			if opponent.Cash-groupSpaces[g][0].buildCost >= c.adaptiveReserve(opponent, noGroup) {
				return true
			}
		}
	}
	return false
}

// chooseUnmortgage mirrors chooseUnmortgage(game, player).
func (c *ctx) chooseUnmortgage(p *Player) *Action {
	type candidate struct {
		index int
		score int
	}
	var candidates []candidate
	for i := 0; i < boardSize; i++ {
		if !c.hasProp[i] || c.props[i].OwnerID != p.ID || !c.props[i].Mortgaged {
			continue
		}
		s := &board[i]
		cost := unmortgageCost(s.price)
		tier := c.assetStrategicTier(p.ID, s)
		baseReserve := c.adaptiveReserve(p, noGroup)
		reserve := baseReserve + roundInt(c.p.UnmortgageLow)
		if tier >= 3 {
			reserve = baseReserve + roundInt(c.p.UnmortgageHigh)
		} else if tier >= 2 {
			reserve = baseReserve + roundInt(c.p.UnmortgageMid)
		}
		if p.Cash-cost >= reserve {
			candidates = append(candidates, candidate{i, tier*1000 + s.price})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[j].score-candidates[i].score < 0 })
	if len(candidates) == 0 {
		return nil
	}
	return &Action{Type: ActionUnmortgage, SpaceIndex: candidates[0].index}
}

// --------------------------------------------------------------- jail ----

func (c *ctx) chooseJailAction(p *Player) *Action {
	if !p.InJail {
		return nil
	}
	if !c.shouldLeaveJail(p) {
		return &Action{Type: ActionRoll}
	}
	if p.JailCards > 0 {
		return &Action{Type: ActionUseJailCard}
	}
	if p.Cash >= 50 {
		return &Action{Type: ActionPayBail}
	}
	return &Action{Type: ActionRoll}
}

func (c *ctx) shouldLeaveJail(p *Player) bool {
	bankOwned := 0
	for i := 0; i < boardSize; i++ {
		if c.hasProp[i] && c.props[i].OwnerID == "" {
			bankOwned++
		}
	}
	if float64(bankOwned) >= c.p.JailOpenSpaces {
		return true
	}

	cushion := p.Cash + c.liquidationValue(p.ID)
	maximumHit := c.maxHostileRent(p.ID)
	if float64(maximumHit) > mul(float64(cushion), c.p.JailHitFraction) {
		return false
	}

	ownIncome := c.playerIncomeEV(p.ID)
	hostileIncome := 0.0
	for _, opponent := range c.g.Players {
		if !opponent.Bankrupt && opponent.ID != p.ID {
			hostileIncome += c.playerIncomeEV(opponent.ID)
		}
	}
	if c.hasHostileDevelopedGroup(p.ID, 3) && hostileIncome > mul(ownIncome, c.p.JailIncomeRatio) {
		return false
	}
	return true
}

func (c *ctx) playerIncomeEV(playerID string) float64 {
	income := 0.0
	for g := 0; g < groupCount; g++ {
		if !c.groupOwnedBy(playerID, g) || c.groupHasMortgage(g) {
			continue
		}
		income += groupStageEV[g][c.groupMinHouses(g)]
	}
	railroads := c.countOwned(railroadSpaces, playerID, true)
	utilities := c.countOwned(utilitySpaces, playerID, true)
	return income + evAt(railroadEV[:], railroads) + evAt(utilityEV[:], utilities)
}

func (c *ctx) maxHostileRent(playerID string) int {
	maximum := 0
	for i := 0; i < boardSize; i++ {
		if !c.hasProp[i] {
			continue
		}
		st := &c.props[i]
		if st.OwnerID == "" || st.OwnerID == playerID || st.Mortgaged {
			continue
		}
		s := &board[i]
		switch s.kind {
		case typeProperty:
			rent := s.rents[st.Houses]
			if st.Houses == 0 && c.groupOwnedBy(st.OwnerID, s.group) {
				rent = s.rents[0] * 2
			}
			maximum = max(maximum, rent)
		case typeRailroad:
			count := c.countOwned(railroadSpaces, st.OwnerID, false)
			maximum = max(maximum, 25<<(count-1))
		case typeUtility:
			rent := 48
			if c.countOwned(utilitySpaces, st.OwnerID, false) == 2 {
				rent = 120
			}
			maximum = max(maximum, rent)
		}
	}
	return maximum
}

// --------------------------------------------------------------- debt ----

// gameJSHotelShortage keeps src/game.js's rule that a hotel cannot be sold
// while the bank has fewer than four houses, so chooseDebtAction skips such
// hotels as ai.js does. The engine fixed that rule (a hotel comes down to the
// houses the bank has and the rest is sold too, engine sellBuilding) and real
// games play by it, so the AI does too. Only this package's tests set it,
// like gameJSMaths: they replay decisions recorded from the JS AI on
// game.js rules (the engine's legacy mode).
var gameJSHotelShortage = false

// chooseDebtAction mirrors chooseDebtAction(game, player), except that a
// hotel is also for sale during a house shortage (see gameJSHotelShortage),
// scored on the cash and rent of breaking it down to the bank's houses.
// Without it a player whose only buildings were hotels went bankrupt late in
// the game instead of selling them.
func (c *ctx) chooseDebtAction(p *Player) *Action {
	d := c.g.Debt
	if d == nil || d.PayerID != p.ID {
		return nil
	}
	if p.Cash >= d.Amount {
		return &Action{Type: ActionPayDebt}
	}

	type option struct {
		action Action
		score  float64
	}
	var options []option
	supply := c.bankSupply()
	for i := 0; i < boardSize; i++ {
		s := &board[i]
		if s.kind != typeProperty {
			continue
		}
		st := &c.props[i]
		if st.OwnerID != p.ID || st.Houses < 1 {
			continue
		}
		if st.Houses != c.groupMaxHouses(s.group) {
			continue
		}
		level := st.Houses
		left := level - 1 // houses left on the space after the sale
		if level == 5 && supply.Houses < 4 {
			if gameJSHotelShortage {
				continue
			}
			// House shortage: the hotel comes down to the houses the bank
			// still has and every other level is sold with it.
			left = supply.Houses
		}
		rentLoss := s.rents[level] - s.rents[left]
		threeHousePenalty := 1.0
		if level == 3 {
			threeHousePenalty = c.p.SellThreePenalty
		} else if level > 3 {
			threeHousePenalty = c.p.SellHighPenalty
		}
		cashRaised := (level - left) * s.buildCost / 2
		options = append(options, option{
			action: Action{Type: ActionSellBuilding, SpaceIndex: s.index},
			score:  float64(cashRaised) / math.Max(1, mul(float64(rentLoss), threeHousePenalty)),
		})
	}
	for _, s := range c.mortgageCandidates(p.ID, noGroup) {
		options = append(options, option{
			action: Action{Type: ActionMortgage, SpaceIndex: s.index},
			score:  c.mortgageLiquidationScore(p.ID, s),
		})
	}
	sort.SliceStable(options, func(i, j int) bool { return options[j].score-options[i].score < 0 })
	if len(options) > 0 {
		best := options[0].action
		return &best
	}
	return &Action{Type: ActionBankrupt}
}
