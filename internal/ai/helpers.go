package ai

import (
	"strconv"
	"strings"
)

// Re-implementations of the src/game.js helpers that ai.js imports
// (aiTradeOnCooldown, auctionCurrentPlayerId, bankSupply), on the view types.

// auctionCurrentPlayerID mirrors auctionCurrentPlayerId(game).
//
// Like the JS function it MUTATES g.Auction.TurnPos: it moves turnPos to the
// seat that is actually due (skipping the current high bidder). The engine
// performs the same normalisation at the start of every bid/pass and every
// publicState, so the mutation never changes game behaviour, but it is
// reproduced here so a caller that keeps the snapshot sees exactly what JS
// leaves behind after chooseAiAction. "" means null.
func auctionCurrentPlayerID(g *Game) string {
	a := g.Auction
	if a == nil || len(a.ActiveIDs) == 0 {
		return ""
	}
	n := len(a.ActiveIDs)
	if n == 1 && a.BidderID == a.ActiveIDs[0] {
		return ""
	}
	for offset := 0; offset < n; offset++ {
		pos := (a.TurnPos + offset) % n
		if pos < 0 {
			// JS: activeIds[negative] is undefined, which differs from bidderId,
			// so turnPos is stored and undefined returned. Unreachable in play.
			a.TurnPos = pos
			return ""
		}
		candidate := a.ActiveIDs[pos]
		if candidate != a.BidderID {
			a.TurnPos = pos
			return candidate
		}
	}
	return ""
}

// openAuctionBidder reports whether playerID may bid or pass in a timed
// auction (engine checkOpenBidder): still in, and not holding the top bid.
func openAuctionBidder(a *Auction, playerID string) bool {
	if playerID == "" || a.BidderID == playerID {
		return false
	}
	for _, id := range a.ActiveIDs {
		if id == playerID {
			return true
		}
	}
	return false
}

// bankSupply mirrors bankSupply(game): 32 houses and 12 hotels minus those on
// the board.
func bankSupply(g *Game) Supply {
	usedHouses, usedHotels := 0, 0
	for _, state := range g.Properties {
		if state == nil {
			continue
		}
		if state.Houses == 5 {
			usedHotels++
		} else {
			usedHouses += state.Houses
		}
	}
	return Supply{Houses: 32 - usedHouses, Hotels: 12 - usedHotels}
}

// tradeGoalKeys mirrors tradeGoalKeys(propertyIndexes) in game.js.
func tradeGoalKeys(indexes []int) []string {
	var goals []string
	seen := map[string]bool{}
	for _, index := range indexes {
		var goal string
		s := spaceAt(index)
		switch {
		case s != nil && s.kind == typeProperty:
			goal = "property:" + groupNames[s.group]
		case s != nil && s.kind == typeRailroad:
			goal = "railroad"
		case s != nil && s.kind == typeUtility:
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

// aiTradeOnCooldown mirrors aiTradeOnCooldown(game, proposerId, targetId,
// propertyIndexes): true while a rejected goal is still cooling down.
func aiTradeOnCooldown(g *Game, proposerID, targetID string, indexes []int) bool {
	completedTurns := g.TurnCounts[proposerID] // missing -> 0, like `|| 0`
	for _, goal := range tradeGoalKeys(indexes) {
		var key strings.Builder
		key.WriteString(proposerID)
		key.WriteByte('|')
		key.WriteString(targetID)
		key.WriteByte('|')
		key.WriteString(goal)
		if completedTurns < g.AITradeCooldowns[key.String()] {
			return true
		}
	}
	return false
}
