package engine

import (
	"errors"
	"sort"
	"strings"
)

func rollForOpeningOrder(g *Game, playerID string, rng RNG) error {
	if err := assertPhase(g, PhaseDeterminingOrder); err != nil {
		return err
	}
	player := g.PlayerByID(playerID)
	if player == nil || player.Bankrupt {
		return errors.New("Player not found")
	}
	order := g.OpeningOrder
	if order == nil || !containsString(order.PendingIDs, playerID) {
		return errors.New("You have already rolled for order this round")
	}

	first := rollDie(rng)
	second := rollDie(rng)
	dice := []int{first, second}
	total := first + second
	for _, entry := range order.Entries {
		if entry.PlayerID == playerID {
			entry.Rolls = append(entry.Rolls, OpeningRoll{Round: order.Round, Dice: dice, Total: total})
			break
		}
	}
	order.PendingIDs = withoutString(order.PendingIDs, playerID)
	addLog(g, player.Name+" rolled "+itoa(first)+" + "+itoa(second)+" = "+itoa(total)+" for turn order")

	if len(order.PendingIDs) == 0 {
		resolveOpeningOrderRound(g)
	}
	return nil
}

func resolveOpeningOrderRound(g *Game) {
	order := g.OpeningOrder
	// Group entries by their roll history, keeping first-appearance order
	// like a JS Map.
	var keys []string
	groups := map[string][]string{}
	for _, entry := range order.Entries {
		totals := make([]string, len(entry.Rolls))
		for i, roll := range entry.Rolls {
			totals[i] = itoa(roll.Total)
		}
		key := strings.Join(totals, ":")
		if _, seen := groups[key]; !seen {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], entry.PlayerID)
	}
	tiedIDs := []string{}
	for _, key := range keys {
		if members := groups[key]; len(members) > 1 {
			tiedIDs = append(tiedIDs, members...)
			// Never append in place: game copies (internal/hardai) share this slice.
			order.Ties = append(order.Ties[:len(order.Ties):len(order.Ties)], OpeningTie{
				Round:     order.Round,
				Total:     latestOpeningTotal(order, members[0]),
				PlayerIDs: append([]string{}, members...),
			})
		}
	}
	if len(tiedIDs) > 0 {
		order.Round++
		order.PendingIDs = tiedIDs
		order.RollingIDs = append([]string{}, tiedIDs...)
		var names []string
		for _, id := range tiedIDs {
			if player := g.PlayerByID(id); player != nil && player.Name != "" {
				names = append(names, player.Name)
			}
		}
		addLog(g, strings.Join(names, ", ")+" tied and will roll again in round "+itoa(order.Round))
		return
	}

	entries := append([]*OpeningEntry{}, order.Entries...)
	sort.SliceStable(entries, func(i, j int) bool {
		// JS: sort((left, right) => compareOpeningRolls(right.rolls, left.rolls))
		return compareOpeningRolls(entries[j].Rolls, entries[i].Rolls) < 0
	})
	order.Order = make([]string, len(entries))
	positions := map[string]int{}
	for index, entry := range entries {
		order.Order[index] = entry.PlayerID
		positions[entry.PlayerID] = index
	}
	sort.SliceStable(g.Players, func(i, j int) bool {
		return positions[g.Players[i].ID] < positions[g.Players[j].ID]
	})
	g.CurrentIndex = 0
	g.Phase = PhaseOrderCountdown
	order.Stage = OpeningStageCountdown
	order.RollingIDs = []string{}
	endsAt := g.now() + OpeningOrderCountdownMS
	order.CountdownEndsAt = &endsAt
	names := make([]string, len(g.Players))
	for index, player := range g.Players {
		names[index] = player.Name
	}
	addLog(g, "Turn order: "+strings.Join(names, " → "))
}

func compareOpeningRolls(left, right []OpeningRoll) int {
	length := max(len(left), len(right))
	for index := 0; index < length; index++ {
		var l, r int
		if index < len(left) {
			l = left[index].Total
		}
		if index < len(right) {
			r = right[index].Total
		}
		if difference := l - r; difference != 0 {
			return difference
		}
	}
	return 0
}

// CompleteOpeningOrder ends the turn-order countdown and begins the first
// turn once now (Unix ms) has reached countdownEndsAt. It reports whether the
// game advanced (completeOpeningOrder). Pass g.Now() for "now".
func CompleteOpeningOrder(g *Game, now int64) bool {
	if g.Phase != PhaseOrderCountdown || g.OpeningOrder == nil {
		return false
	}
	// JS: now < null coerces null to 0.
	var endsAt int64
	if g.OpeningOrder.CountdownEndsAt != nil {
		endsAt = *g.OpeningOrder.CountdownEndsAt
	}
	if now < endsAt {
		return false
	}
	g.OpeningOrder.Stage = OpeningStageComplete
	initial := playerSnapshot(g)
	g.TurnSummaryBaselines = map[string]map[string]PlayerSpot{}
	for _, player := range g.Players {
		g.TurnSummaryBaselines[player.ID] = initial
	}
	beginTurn(g)
	touch(g)
	return true
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func withoutString(list []string, value string) []string {
	out := []string{}
	for _, item := range list {
		if item != value {
			out = append(out, item)
		}
	}
	return out
}

// latestOpeningTotal is the total of a player's most recent turn-order roll.
func latestOpeningTotal(order *OpeningOrder, playerID string) int {
	for _, entry := range order.Entries {
		if entry.PlayerID == playerID && len(entry.Rolls) > 0 {
			return entry.Rolls[len(entry.Rolls)-1].Total
		}
	}
	return 0
}
