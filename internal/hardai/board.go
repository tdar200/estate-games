package hardai

import "github.com/tdar200/estate-games/internal/engine"

// Static board tables (engine.GroupSpaces allocates; these do not).

// groupMembers[g] lists the board indexes of colour group g (engine.GroupOrder).
var groupMembers [][]int

// spaceGroup[i] is the colour group of board index i, or -1.
var spaceGroup [engine.BoardSize]int

var railroadIndexes, utilityIndexes []int

func init() {
	for i := range spaceGroup {
		spaceGroup[i] = -1
	}
	groupMembers = make([][]int, len(engine.GroupOrder))
	for g, key := range engine.GroupOrder {
		for _, s := range engine.Board {
			if s.Group == key {
				groupMembers[g] = append(groupMembers[g], s.Index)
				spaceGroup[s.Index] = g
			}
		}
	}
	for _, s := range engine.Board {
		switch s.Type {
		case engine.SpaceRailroad:
			railroadIndexes = append(railroadIndexes, s.Index)
		case engine.SpaceUtility:
			utilityIndexes = append(utilityIndexes, s.Index)
		}
	}
}

func ownerOf(g *engine.Game, index int) string {
	if st := g.Properties[index]; st != nil {
		return st.OwnerID
	}
	return ""
}

// ownsGroup reports whether playerID owns every space of colour group grp.
func ownsGroup(g *engine.Game, playerID string, grp int) bool {
	for _, index := range groupMembers[grp] {
		if ownerOf(g, index) != playerID {
			return false
		}
	}
	return true
}

func groupHasBuildings(g *engine.Game, grp int) bool {
	for _, index := range groupMembers[grp] {
		if g.Properties[index].Houses > 0 {
			return true
		}
	}
	return false
}

func groupHasMortgage(g *engine.Game, grp int) bool {
	for _, index := range groupMembers[grp] {
		if g.Properties[index].Mortgaged {
			return true
		}
	}
	return false
}

func groupMinHouses(g *engine.Game, grp int) int {
	m := 5
	for _, index := range groupMembers[grp] {
		m = min(m, g.Properties[index].Houses)
	}
	return m
}

// groupPledged reports whether any space of the group secures an active loan
// (which blocks building and trading it).
func groupPledged(g *engine.Game, grp int) bool {
	if len(g.Loans) == 0 {
		return false
	}
	for _, index := range groupMembers[grp] {
		if engine.LoanSecuredBy(g, index) != nil {
			return true
		}
	}
	return false
}

func pledged(g *engine.Game, index int) bool {
	return len(g.Loans) > 0 && engine.LoanSecuredBy(g, index) != nil
}

func playerIndex(g *engine.Game, id string) int {
	for i, p := range g.Players {
		if p.ID == id {
			return i
		}
	}
	return -1
}

func activeCount(g *engine.Game) int {
	n := 0
	for _, p := range g.Players {
		if !p.Bankrupt {
			n++
		}
	}
	return n
}

// supply returns the bank's remaining houses and hotels (engine.BankSupply
// without the map range).
func supply(g *engine.Game) (houses, hotels int) {
	houses, hotels = 32, 12
	for _, grp := range groupMembers {
		for _, index := range grp {
			switch h := g.Properties[index].Houses; h {
			case 5:
				hotels--
			default:
				houses -= h
			}
		}
	}
	return houses, hotels
}

// netWorth is cash plus the cost basis of everything a player owns:
// unmortgaged spaces at price, mortgaged ones at half, buildings at cost.
func netWorth(g *engine.Game, p *engine.Player) float64 {
	if p.Bankrupt {
		return 0
	}
	worth := p.Cash
	for _, index := range engine.OwnableIndexes {
		st := g.Properties[index]
		if st.OwnerID != p.ID {
			continue
		}
		s := &engine.Board[index]
		if st.Mortgaged {
			worth += s.Price / 2
		} else {
			worth += s.Price
		}
		worth += st.Houses * s.BuildCost
	}
	return float64(max(0, worth))
}

// maxSeats sizes the per-seat arrays of the simulations: every seat count
// the engine allows (engine.MaxPlayers, 8), and never fewer than 8.
const maxSeats = max(engine.MaxPlayers, 8)
