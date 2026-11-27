package engine

import "errors"

func canManage(g *Game, playerID string) (*Player, error) {
	player, err := assertCurrent(g, playerID)
	if err != nil {
		return nil, err
	}
	if g.Phase == PhaseDebt {
		if g.Debt == nil || g.Debt.PayerID != playerID {
			return nil, errors.New("Properties cannot be managed right now")
		}
		return player, nil
	}
	if err := assertPhase(g, PhaseAwaitingRoll, PhaseAwaitingPurchase, PhaseTurnComplete); err != nil {
		return nil, err
	}
	return player, nil
}

func groupLevels(group string, g *Game) (minimum, maximum int) {
	spaces := GroupSpaces(group)
	minimum, maximum = 1<<31-1, -1<<31
	for _, item := range spaces {
		houses := g.Properties[item.Index].Houses
		minimum = min(minimum, houses)
		maximum = max(maximum, houses)
	}
	return minimum, maximum
}

func groupHasBuildings(g *Game, group string) bool {
	for _, item := range GroupSpaces(group) {
		if g.Properties[item.Index].Houses > 0 {
			return true
		}
	}
	return false
}

func build(g *Game, playerID string, spaceIndex int) error {
	player, err := canManage(g, playerID)
	if err != nil {
		return err
	}
	if g.Phase == PhaseDebt {
		return errors.New("Buildings cannot be constructed while in debt")
	}
	space := spaceAt(spaceIndex)
	if space == nil || space.Type != SpaceProperty {
		return errors.New("Buildings cannot be constructed on this space")
	}
	state := g.Properties[spaceIndex]
	if state.OwnerID != player.ID {
		return errors.New("You do not own this property")
	}
	if !ownsGroup(g, player.ID, space.Group) {
		return errors.New("You must own the complete color group before building")
	}
	if err := collateralBuildCheck(g, space); err != nil { // house rule: loans
		return err
	}
	for _, item := range GroupSpaces(space.Group) {
		if g.Properties[item.Index].Mortgaged {
			return errors.New("You cannot build while a property in the group is mortgaged")
		}
	}
	minimum, _ := groupLevels(space.Group, g)
	if state.Houses != minimum {
		return errors.New("Buildings must be distributed evenly across the color group")
	}
	if state.Houses >= 5 {
		return errors.New("This property already has a hotel")
	}
	if player.Cash < space.BuildCost {
		return errors.New("Not enough cash")
	}
	supply := BankSupply(g)
	if state.Houses == 4 && supply.Hotels < 1 {
		return errors.New("The bank has no hotels available")
	}
	if state.Houses < 4 && supply.Houses < 1 {
		return errors.New("The bank has no houses available")
	}
	player.Cash -= space.BuildCost
	state.Houses++
	building := "house"
	if state.Houses == 5 {
		building = "hotel"
	}
	addLog(g, player.Name+" built a "+building+" on "+space.Name)
	return nil
}

func sellBuilding(g *Game, playerID string, spaceIndex int) error {
	player, err := canManage(g, playerID)
	if err != nil {
		return err
	}
	space := spaceAt(spaceIndex)
	if space == nil || space.Type != SpaceProperty {
		return errors.New("This property has no buildings")
	}
	state := g.Properties[spaceIndex]
	if state.OwnerID != player.ID || state.Houses < 1 {
		return errors.New("There are no buildings to sell")
	}
	_, maximum := groupLevels(space.Group, g)
	if state.Houses != maximum {
		return errors.New("Buildings must be sold evenly across the color group")
	}
	if state.Houses == 5 {
		if g.legacyRules && BankSupply(g).Houses < 4 {
			return errors.New("The bank does not have enough houses to break down this hotel")
		}
		if houses := BankSupply(g).Houses; houses < 4 {
			// Housing shortage: a hotel normally becomes four houses, but the
			// bank can only hand over what it has, so the rest is sold too
			// (official rule). Each level removed pays half the build cost.
			levels := 5 - houses
			state.Houses = houses
			player.Cash += levels * space.BuildCost / 2
			addLog(g, player.Name+" sold the hotel on "+space.Name+" for "+money(levels*space.BuildCost/2)+
				": the bank had only "+itoa(houses)+" "+verb(houses, "house", "houses")+" to put back")
			return nil
		}
	}
	state.Houses--
	player.Cash += space.BuildCost / 2
	addLog(g, player.Name+" sold one building on "+space.Name)
	return nil
}

func mortgage(g *Game, playerID string, spaceIndex int) error {
	player, err := canManage(g, playerID)
	if err != nil {
		return err
	}
	space := spaceAt(spaceIndex)
	if space == nil || !IsOwnable(space.Type) {
		return errors.New("This property cannot be mortgaged")
	}
	state := g.Properties[spaceIndex]
	if state.OwnerID != player.ID {
		return errors.New("You do not own this property")
	}
	if state.Mortgaged {
		return errors.New("This property is already mortgaged")
	}
	if space.Type == SpaceProperty && groupHasBuildings(g, space.Group) {
		return errors.New("Sell every building in the color group first")
	}
	if err := collateralTradeCheck(g, spaceIndex); err != nil { // house rule: loans
		return err // pledged collateral must reach the lender as it was pledged
	}
	state.Mortgaged = true
	value := space.Price / 2
	player.Cash += value
	addLog(g, player.Name+" mortgaged "+space.Name+" for "+money(value))
	return nil
}

func unmortgage(g *Game, playerID string, spaceIndex int) error {
	player, err := canManage(g, playerID)
	if err != nil {
		return err
	}
	if g.Phase == PhaseDebt {
		return errors.New("Properties cannot be unmortgaged while in debt")
	}
	space := spaceAt(spaceIndex)
	var state *PropertyState
	if space != nil {
		state = g.Properties[spaceIndex]
	}
	if space == nil || state == nil || state.OwnerID != player.ID || !state.Mortgaged {
		return errors.New("This property cannot be unmortgaged")
	}
	cost := g.unmortgageCost(space.Price)
	if player.Cash < cost {
		return errors.New("Not enough cash")
	}
	player.Cash -= cost
	state.Mortgaged = false
	addLog(g, player.Name+" unmortgaged "+space.Name+" for "+money(cost))
	return nil
}

// BankSupply returns the houses and hotels still available from the bank
// (32 houses, 12 hotels in total).
func BankSupply(g *Game) Supply {
	usedHouses, usedHotels := 0, 0
	for _, state := range g.Properties {
		if state.Houses == 5 {
			usedHotels++
		} else {
			usedHouses += state.Houses
		}
	}
	return Supply{Houses: 32 - usedHouses, Hotels: 12 - usedHotels}
}
