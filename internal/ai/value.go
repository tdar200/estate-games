package ai

import (
	"math"
	"sort"
)

// Tuning constants and tables, copied verbatim from src/ai.js.
const (
	aiTradeReserve        = 220
	aiMinTradeGain        = 12
	strategicEVCashFactor = 3.0
	maxTradeChips         = 12
	maxTradeCash          = 1_000_000
)

var railroadHardCaps = [5]float64{0, 240, 450, 750, 1200}

// strongDevelopmentGroups is STRONG_DEVELOPMENT_GROUPS in Set order.
var strongDevelopmentGroups = [4]int{groupOrange, groupRed, groupYellow, groupDarkBlue}

var groupStageEV = [groupCount][6]float64{
	groupBrown:     {0.284, 0.710, 2.130, 6.390, 11.360, 16.560},
	groupLightBlue: {1.010, 2.525, 7.070, 21.209, 31.561, 42.921},
	groupPink:      {1.783, 4.457, 13.371, 38.983, 54.301, 66.857},
	groupOrange:    {2.844, 7.110, 20.033, 54.927, 74.305, 93.683},
	groupRed:       {3.572, 8.929, 25.580, 68.481, 85.164, 101.848},
	groupYellow:    {3.960, 9.900, 29.701, 71.357, 86.655, 101.954},
	groupGreen:     {4.572, 11.704, 35.113, 79.972, 97.133, 112.836},
	groupDarkBlue:  {4.581, 10.003, 29.410, 67.020, 80.523, 94.026},
}

var (
	railroadEV          = [5]float64{0, 0.839, 3.358, 10.073, 26.862}
	utilityEV           = [3]float64{0, 0.948, 4.195}
	buildabilityWeights = [6]float64{0, 0.15, 0.4, 1, 1.1, 1.15}
	blockerGroupWeights = [groupCount]float64{
		groupBrown: 0.25, groupLightBlue: 0.75, groupPink: 0.8, groupOrange: 1.2,
		groupRed: 1.1, groupYellow: 1, groupGreen: 0.8, groupDarkBlue: 0.95,
	}
	denialWeights = [7]float64{0, 0, 1.35, 1, 0.85, 0.7, 0.6}
	leaderWeights = [7]float64{0, 0, 0.75, 0.65, 0.55, 0.45, 0.4}
)

// Strategic phases (keys of RESERVE_SCHEDULE).
const (
	phaseAcquisition = iota
	phaseFormation
	phaseDevelopment
	phaseEndgame
)

var reserveSchedule = [4][7]int{
	phaseAcquisition: {0, 0, 180, 220, 220, 260, 260},
	phaseFormation:   {0, 0, 240, 300, 300, 340, 340},
	phaseDevelopment: {0, 0, 320, 380, 380, 440, 440},
	phaseEndgame:     {0, 0, 420, 500, 500, 580, 580},
}

// mul returns a*b rounded to float64. Every floating-point product in this
// package goes through mul: the explicit conversion forbids the compiler from
// fusing the product into a following addition (FMA, which Go permits on
// arm64, ppc64le, s390x and amd64 with GOAMD64=v3). JS never fuses, so fused
// arithmetic could flip a comparison and break decision parity.
func mul(a, b float64) float64 { return float64(a * b) }

// weightOr mirrors `TABLE[n] || TABLE[6]` (a zero or missing entry falls back
// to the 6-player value).
func weightOr(table *[7]float64, n int) float64 {
	if n >= 0 && n < len(table) && table[n] != 0 {
		return table[n]
	}
	return table[6]
}

// ctx carries one decision's view of the game plus caches of the values that
// depend only on the (unchanging) game. ai.js recomputes these every time;
// caching them cannot change any result because nothing the AI reads is
// mutated during a decision.
type ctx struct {
	g       *Game
	p       *Params // the deciding AI's knobs
	opp     *ctx    // the same game seen with DefaultParams (opponent model); c itself when p is the default
	props   [boardSize]PropertyState
	hasProp [boardSize]bool
	active  int

	phaseDone  bool
	phase      int
	leaderDone bool
	leader     string
	supplyDone bool
	supply     Supply
	reserves   map[reserveKey]int

	// House rules (rules.go, loans.go). All false/empty in default games, so
	// every branch that reads them leaves the ai.js port untouched there.
	loansOn      bool
	pledged      [boardSize]bool  // pledged on an active loan: no trading
	pledgedGroup [groupCount]bool // group holds a pledged property: no building
}

type reserveKey struct {
	playerID string
	group    int
}

func newCtx(g *Game) *ctx { return newCtxParams(g, &DefaultParams) }

// newCtxParams is newCtx for an AI playing with knobs p (nil = defaults).
func newCtxParams(g *Game, p *Params) *ctx {
	if p == nil {
		p = &DefaultParams
	}
	c := &ctx{g: g, p: p, reserves: map[reserveKey]int{}}
	for index, state := range g.Properties {
		if index >= 0 && index < boardSize && state != nil {
			c.props[index] = *state
			c.hasProp[index] = true
		}
	}
	for _, p := range g.Players {
		if !p.Bankrupt {
			c.active++
		}
	}
	c.initHouseRules()
	if p == &DefaultParams || *p == DefaultParams {
		c.opp = c
	}
	return c
}

// oppCtx is the opponent model: the same game valued with DefaultParams (the
// AI assumes the others play the default heuristic when it prices a trade
// proposal against their acceptance test).
func (c *ctx) oppCtx() *ctx {
	if c.opp == nil {
		c.opp = newCtxParams(c.g, &DefaultParams)
	}
	return c.opp
}

// ownerOf mirrors game.properties[index]?.ownerId ("" for null/undefined).
func (c *ctx) ownerOf(index int) string {
	if !c.hasProp[index] {
		return ""
	}
	return c.props[index].OwnerID
}

func (c *ctx) findPlayer(id string) *Player {
	for _, p := range c.g.Players {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (c *ctx) bankSupply() Supply {
	if !c.supplyDone {
		c.supply = bankSupply(c.g)
		c.supplyDone = true
	}
	return c.supply
}

// ---------------------------------------------------------------- sets ----

// indexSet is a JS Set of board indexes: membership plus insertion order.
// Iteration order matters because several values are float sums taken in
// set order.
type indexSet struct {
	order []int
	has   [boardSize]bool
}

func (s *indexSet) add(index int) {
	if index < 0 || index >= boardSize || s.has[index] {
		return
	}
	s.has[index] = true
	s.order = append(s.order, index)
}

func (s *indexSet) del(index int) {
	if index < 0 || index >= boardSize || !s.has[index] {
		return
	}
	s.has[index] = false
	for i, v := range s.order {
		if v == index {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

func (s *indexSet) contains(index int) bool {
	return index >= 0 && index < boardSize && s.has[index]
}

func (s *indexSet) clone() *indexSet {
	out := &indexSet{has: s.has}
	out.order = append(make([]int, 0, len(s.order)+4), s.order...)
	return out
}

// ownedSet mirrors ownedPropertySet(game, playerId) (board order).
func (c *ctx) ownedSet(playerID string) *indexSet {
	s := &indexSet{}
	for i := 0; i < boardSize; i++ {
		if c.hasProp[i] && c.props[i].OwnerID == playerID {
			s.add(i)
		}
	}
	return s
}

// holding is one entry of the holdingsOverrides Map (insertion ordered).
type holding struct {
	id  string
	set *indexSet
}

// newHoldings mirrors new Map([[id, set], ...]): a repeated key keeps its first
// position and takes the last value.
func newHoldings(entries ...holding) []holding {
	var out []holding
	for _, e := range entries {
		replaced := false
		for i := range out {
			if out[i].id == e.id {
				out[i].set = e.set
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, e)
		}
	}
	return out
}

// hypotheticalOwnerID mirrors hypotheticalOwnerId(game, spaceIndex, overrides).
func (c *ctx) hypotheticalOwnerID(index int, overrides []holding) string {
	for _, h := range overrides {
		if h.set.contains(index) {
			return h.id
		}
	}
	original := c.ownerOf(index)
	if original == "" {
		return ""
	}
	for _, h := range overrides {
		if h.id == original {
			return ""
		}
	}
	return original
}

// ----------------------------------------------------- game-wide values ----

// currentLeaderID mirrors currentLeaderId(game); "" means null.
func (c *ctx) currentLeaderID() string {
	if c.leaderDone {
		return c.leader
	}
	type rankedPlayer struct {
		id    string
		value float64
	}
	var ranked []rankedPlayer
	for _, p := range c.g.Players {
		if p.Bankrupt {
			continue
		}
		ranked = append(ranked, rankedPlayer{p.ID, float64(p.Cash) + c.portfolioValue(c.ownedSet(p.ID))})
	}
	// .sort((left, right) => right.value - left.value)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[j].value-ranked[i].value < 0 })
	leader := ""
	switch {
	case len(ranked) == 0:
	case len(ranked) == 1:
		leader = ranked[0].id
	default:
		first, second := ranked[0].value, ranked[1].value
		if first >= second+100 || first >= mul(second, 1.08) {
			leader = ranked[0].id
		}
	}
	c.leader, c.leaderDone = leader, true
	return leader
}

// strategicPhase mirrors strategicPhase(game).
func (c *ctx) strategicPhase() int {
	if c.phaseDone {
		return c.phase
	}
	unowned := 0
	for i := 0; i < boardSize; i++ {
		// game.properties[index]?.ownerId === null
		if c.hasProp[i] && c.props[i].OwnerID == "" {
			unowned++
		}
	}
	phase := phaseFormation
	switch {
	case float64(unowned) >= c.p.AcquisitionOpen:
		phase = phaseAcquisition
	case c.active <= 2 && unowned <= 3:
		phase = phaseEndgame
	default:
		for _, p := range c.g.Players {
			if !p.Bankrupt && c.playerHasDevelopedGroup(p.ID, 3) {
				phase = phaseDevelopment
				break
			}
		}
	}
	c.phase, c.phaseDone = phase, true
	return phase
}

// groupOwnedBy reports whether every space of group g is owned by playerID.
func (c *ctx) groupOwnedBy(playerID string, g int) bool {
	for _, s := range groupSpaces[g] {
		if c.props[s.index].OwnerID != playerID {
			return false
		}
	}
	return true
}

func (c *ctx) groupHasMortgage(g int) bool {
	for _, s := range groupSpaces[g] {
		if c.props[s.index].Mortgaged {
			return true
		}
	}
	return false
}

func (c *ctx) groupHasBuildings(g int) bool {
	for _, s := range groupSpaces[g] {
		if c.props[s.index].Houses > 0 {
			return true
		}
	}
	return false
}

func (c *ctx) groupMinHouses(g int) int {
	minimum := math.MaxInt
	for _, s := range groupSpaces[g] {
		minimum = min(minimum, c.props[s.index].Houses)
	}
	return minimum
}

func (c *ctx) groupMaxHouses(g int) int {
	maximum := math.MinInt
	for _, s := range groupSpaces[g] {
		maximum = max(maximum, c.props[s.index].Houses)
	}
	return maximum
}

func (c *ctx) groupOwnedCount(playerID string, g int) int {
	n := 0
	for _, s := range groupSpaces[g] {
		if c.props[s.index].OwnerID == playerID {
			n++
		}
	}
	return n
}

// countOwned counts spaces of the list owned by playerID (optionally only the
// unmortgaged ones).
func (c *ctx) countOwned(spaces []*space, playerID string, unmortgagedOnly bool) int {
	n := 0
	for _, s := range spaces {
		st := &c.props[s.index]
		if st.OwnerID == playerID && (!unmortgagedOnly || !st.Mortgaged) {
			n++
		}
	}
	return n
}

func (c *ctx) playerHasDevelopedGroup(playerID string, minimumTier int) bool {
	for g := 0; g < groupCount; g++ {
		if c.groupOwnedBy(playerID, g) && c.groupMinHouses(g) >= minimumTier {
			return true
		}
	}
	return false
}

func (c *ctx) hasHostileDevelopedGroup(playerID string, minimumTier int) bool {
	for _, p := range c.g.Players {
		if !p.Bankrupt && p.ID != playerID && c.playerHasDevelopedGroup(p.ID, minimumTier) {
			return true
		}
	}
	return false
}

func (c *ctx) hasHostileLuxuryHotel(playerID string) bool {
	for _, p := range c.g.Players {
		if p.Bankrupt || p.ID == playerID {
			continue
		}
		for _, g := range [2]int{groupGreen, groupDarkBlue} {
			if !c.groupOwnedBy(p.ID, g) {
				continue
			}
			for _, s := range groupSpaces[g] {
				if c.props[s.index].Houses == 5 {
					return true
				}
			}
		}
	}
	return false
}

// liquidationValue mirrors liquidationValue(game, playerId).
func (c *ctx) liquidationValue(playerID string) int {
	value := 0
	for i := 0; i < boardSize; i++ {
		if !c.hasProp[i] || c.props[i].OwnerID != playerID {
			continue
		}
		st := &c.props[i]
		s := &board[i]
		if !st.Mortgaged && st.Houses == 0 {
			value += s.price / 2
		}
		if s.kind == typeProperty && st.Houses > 0 {
			value += s.buildCost * st.Houses / 2
		}
	}
	return value
}

// adaptiveReserve mirrors adaptiveReserve(game, player, {completionGroup}).
// completionGroup is noGroup when the option is absent.
func (c *ctx) adaptiveReserve(p *Player, completionGroup int) int {
	key := reserveKey{p.ID, completionGroup}
	if v, ok := c.reserves[key]; ok {
		return v
	}
	activeCount := max(2, c.active)
	stage := c.strategicPhase()
	reserve := scaleInt(reserveSchedule[stage][min(6, activeCount)], c.p.ReserveScale[stage])
	if c.hasHostileDevelopedGroup(p.ID, 3) {
		reserve += roundInt(c.p.ReserveHostileDev)
	}
	if c.hasHostileLuxuryHotel(p.ID) && c.liquidationValue(p.ID) < 500 {
		reserve += roundInt(c.p.ReserveLuxury)
	}
	switch completionGroup {
	case groupOrange:
		reserve -= roundInt(c.p.CompleteOrange)
	case groupRed, groupYellow, groupDarkBlue:
		reserve -= roundInt(c.p.CompleteStrong)
	}
	reserve = max(roundInt(c.p.ReserveMin), reserve)
	reserve += c.loanCommitment(p.ID, "") // house rule: cash kept for loans due soon (0 without loans)
	c.reserves[key] = reserve
	return reserve
}

// reachableGroupTier mirrors reachableGroupTier(game, player, group, cash).
func (c *ctx) reachableGroupTier(p *Player, g int, cashAfterAcquisition int) int {
	spaces := groupSpaces[g]
	currentTier := 0
	if c.groupOwnedBy(p.ID, g) {
		currentTier = c.groupMinHouses(g)
	}
	reserve := c.adaptiveReserve(p, g)
	fullTierCost := len(spaces) * spaces[0].buildCost
	return min(5, currentTier+max(0, cashAfterAcquisition-reserve)/fullTierCost)
}

// -------------------------------------------------------- position value ----

// portfolioValue mirrors portfolioValue(game, indexes).
func (c *ctx) portfolioValue(set *indexSet) float64 {
	var groupCounts [groupCount]int
	railroads, utilities := 0, 0
	for _, index := range set.order {
		s := &board[index]
		if s.group != noGroup {
			groupCounts[s.group]++
		}
		if s.kind == typeRailroad {
			railroads++
		}
		if s.kind == typeUtility {
			utilities++
		}
	}

	total, railroadBase, utilityBase := 0.0, 0.0, 0.0
	for _, index := range set.order {
		s := &board[index]
		st := &c.props[index]
		factor := 1.0
		if st.Mortgaged {
			factor = c.p.MortgagedValue
		}
		value := mul(float64(s.price), factor)
		switch {
		case s.group != noGroup:
			groupSize := len(groupSpaces[s.group])
			owned := groupCounts[s.group]
			if owned == groupSize {
				value = mul(value, c.p.GroupComplete[s.group])
			} else if owned == groupSize-1 {
				value = mul(value, c.p.GroupPartial[s.group])
			}
			value += mul(float64(st.Houses*s.buildCost), c.p.HouseValue)
		case s.kind == typeRailroad:
			railroadBase += value
			continue
		case s.kind == typeUtility:
			utilityBase += value
			continue
		}
		total += value
	}
	railroadTotals := [5]float64{0, 180, 430, 820, 1500}
	utilityTotals := [3]float64{0, 130, 360}
	if railroads > 0 {
		total += mul(railroadBase, mul(railroadTotals[railroads], c.p.RailroadPortfolio)/float64(railroads*200))
	}
	if utilities > 0 {
		total += mul(utilityBase, mul(utilityTotals[utilities], c.p.UtilityPortfolio)/float64(utilities*150))
	}
	return total
}

// strategicPositionValue mirrors strategicPositionValue(...).
func (c *ctx) strategicPositionValue(playerID string, set *indexSet, cash int, overrides []holding) float64 {
	return float64(cash) +
		c.portfolioValue(set) +
		c.developmentValue(playerID, set, cash) +
		c.blockerValue(playerID, set, overrides)
}

// developmentValue mirrors developmentValue(game, playerId, indexes, cash).
func (c *ctx) developmentValue(playerID string, set *indexSet, cash int) float64 {
	opponents := float64(max(1, c.active-1))
	reserve := 300
	if p := c.findPlayer(playerID); p != nil {
		reserve = c.adaptiveReserve(p, noGroup)
	}
	currentIncomeValue := 0.0
	bestBuildOption := 0.0

	for g := 0; g < groupCount; g++ {
		spaces := groupSpaces[g]
		all := true
		for _, s := range spaces {
			if !set.contains(s.index) {
				all = false
				break
			}
		}
		if !all || c.groupHasMortgage(g) {
			continue
		}
		currentTier := c.groupMinHouses(g)
		stageValues := &groupStageEV[g]
		currentIncomeValue += mul(mul(stageValues[currentTier], opponents), c.p.EVCashFactor)
		if c.pledgedGroup[g] {
			continue // house rule: collateral blocks building, so no build option
		}

		buildCost := spaces[0].buildCost * len(spaces)
		reachableTier := min(5, currentTier+max(0, cash-reserve)/max(1, buildCost))
		optionValue := mul(mul(mul(mul(
			stageValues[reachableTier]-stageValues[currentTier],
			opponents),
			c.p.EVCashFactor),
			buildabilityWeights[reachableTier]),
			blockerGroupWeights[g])
		bestBuildOption = math.Max(bestBuildOption, optionValue)
	}

	railroads, utilities := 0, 0
	for _, index := range set.order {
		switch board[index].kind {
		case typeRailroad:
			railroads++
		case typeUtility:
			utilities++
		}
	}
	currentIncomeValue += mul(mul(evAt(railroadEV[:], railroads)+evAt(utilityEV[:], utilities), opponents), c.p.EVCashFactor)
	return currentIncomeValue + bestBuildOption
}

// evAt mirrors TABLE[n] where an out-of-range read is undefined (NaN in maths).
func evAt(table []float64, n int) float64 {
	if n < 0 || n >= len(table) {
		return math.NaN()
	}
	return table[n]
}

// blockerValue mirrors blockerValue(game, playerId, indexes, overrides).
func (c *ctx) blockerValue(playerID string, set *indexSet, overrides []holding) float64 {
	denialWeight := mul(weightOr(&denialWeights, c.active), c.p.DenialScale)
	leaderID := c.currentLeaderID()
	players := c.g.Players
	counted := make([]bool, len(players)*groupCount)
	value := 0.0

	for _, index := range set.order {
		s := &board[index]
		if s.group == noGroup {
			continue
		}
		for oi, opponent := range players {
			if opponent.Bankrupt || opponent.ID == playerID {
				continue
			}
			key := oi*groupCount + s.group
			if counted[key] {
				continue
			}
			blocksCompletion := true
			for _, candidate := range groupSpaces[s.group] {
				var ok bool
				if candidate.index == index {
					ok = set.contains(candidate.index)
				} else {
					ok = c.hypotheticalOwnerID(candidate.index, overrides) == opponent.ID
				}
				if !ok {
					blocksCompletion = false
					break
				}
			}
			if !blocksCompletion {
				continue
			}
			counted[key] = true
			leaderMultiplier := 1.0
			if opponent.ID == leaderID {
				leaderMultiplier = 1 + mul(weightOr(&leaderWeights, c.active), c.p.LeaderScale)
			}
			value += mul(mul(mul(mul(
				groupStageEV[s.group][3],
				denialWeight),
				leaderMultiplier),
				c.p.EVCashFactor),
				blockerGroupWeights[s.group])
		}
	}
	return value
}

// mortgageFee mirrors mortgageFee(game, indexes) (the 10% transfer fee).
func (c *ctx) mortgageFee(indexes []int) int {
	sum := 0
	for _, index := range indexes {
		if c.props[index].Mortgaged {
			sum += transferFee(board[index].price)
		}
	}
	return sum
}

// gameJSMaths makes the AI price lifting a mortgage and the transfer fee of a
// mortgaged deed with src/game.js's floating-point formulas
// (Math.ceil((price / 2) * 1.1) and Math.ceil((price / 2) * 0.1)), which
// overcharge $1 on some prices. The engine fixed that (integer maths, review
// of 3 Nov 2026, B12) and real games play by the fixed rules, so the AI does
// too. Only this package's tests set it: they replay decisions recorded from
// the JS AI on game.js rules (the engine's legacy mode).
var gameJSMaths = false

// unmortgageCost is what lifting the mortgage of a deed of this price costs:
// the mortgage value plus 10%, rounded up (engine.UnmortgageCost).
func unmortgageCost(price int) int {
	if gameJSMaths {
		return int(math.Ceil(mul(float64(price)/2, 1.1)))
	}
	return (price*11 + 19) / 20
}

// transferFee is the 10% fee for receiving a mortgaged deed of this price,
// rounded up (engine.MortgageTransferFee).
func transferFee(price int) int {
	if gameJSMaths {
		return int(math.Ceil(mul(float64(price)/2, 0.1)))
	}
	return (price + 19) / 20
}
