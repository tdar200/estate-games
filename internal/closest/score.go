package closest

import (
	"slices"
	"sort"
)

// Scoring (docs/CLOSEST-API.md §3.3, §4.1).
const (
	ownerWin     = 3 // the owner of a winning slot
	ownerExact   = 5 // ... when it is the answer exactly
	ownerSecond  = 1 // the owner of a second-closest slot
	secondChip   = 1 // a chip on a second-closest slot
	maxOdds      = 5
	middleOdds   = 2
	maxStake     = 3
	chipsPerBet  = 2
	resultWin    = "win"
	resultSecond = "second"
)

// Slot is one distinct guess on the line, low to high. Equal guesses share
// a slot; OwnerIDs are in guess order.
type Slot struct {
	Value    int      `json:"value"`
	OwnerIDs []string `json:"ownerIds"`
	Odds     int      `json:"odds"`
}

// Odds is what a chip on slot i of k pays when it wins: 2 in the middle,
// one more for each step outward, at most 5 (min(5, 2 + ⌊|i − (k−1)/2|⌋)).
func Odds(i, k int) int {
	d := 2*i - (k - 1)
	if d < 0 {
		d = -d
	}
	return min(maxOdds, middleOdds+d/2)
}

// lineUp makes the slots from the guesses (in guess order).
func lineUp(guesses []Guess) []Slot {
	var slots []Slot
	for _, gs := range guesses {
		i := slices.IndexFunc(slots, func(s Slot) bool { return s.Value == gs.Value })
		if i < 0 {
			slots = append(slots, Slot{Value: gs.Value})
			i = len(slots) - 1
		}
		slots[i].OwnerIDs = append(slots[i].OwnerIDs, gs.PlayerID)
	}
	sort.SliceStable(slots, func(a, b int) bool { return slots[a].Value < slots[b].Value })
	for i := range slots {
		slots[i].Odds = Odds(i, len(slots))
	}
	return slots
}

// judge finds the winning slots (the smallest distance to the answer, ties
// all win) and, only when exactly one slot wins, the second-closest slots
// (the next smallest distance).
func judge(slots []Slot, answer int) (winners, second []int) {
	winners, second = []int{}, []int{}
	if len(slots) == 0 {
		return winners, second
	}
	best, next := -1, -1
	for _, s := range slots {
		d := distance(s.Value, answer)
		switch {
		case best < 0 || d < best:
			best, next = d, best
		case d > best && (next < 0 || d < next):
			next = d
		}
	}
	for i, s := range slots {
		if distance(s.Value, answer) == best {
			winners = append(winners, i)
		}
	}
	if len(winners) == 1 && next >= 0 {
		for i, s := range slots {
			if distance(s.Value, answer) == next {
				second = append(second, i)
			}
		}
	}
	return winners, second
}

func distance(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// Result is one player's points on a question.
type Result struct {
	PlayerID string `json:"playerId"`
	Guess    *int   `json:"guess"` // null when they did not guess
	Slot     *int   `json:"slot"`
	// Bonus is the owner bonus; ChipPoints what their free chips paid;
	// StakePoints what the stake won (or, negative, lost).
	Bonus       int `json:"bonus"`
	ChipPoints  int `json:"chipPoints"`
	StakePoints int `json:"stakePoints"`
	Delta       int `json:"delta"`
	Before      int `json:"before"`
	After       int `json:"after"`
}

// payout is what one player scores on a judged question (§4.1). m is 2 on
// the last question. active: they are still playing (a player who left
// scores nothing more). It returns the bonus, the chips' points and the
// stake's points.
func payout(slots []Slot, winners, second []int, answer, m int, playerID string, bet *Bet, active bool) (bonus, chips, stake int, closest bool) {
	if !active {
		return 0, 0, 0, false
	}
	won := func(i int) bool { return slices.Contains(winners, i) }
	sec := func(i int) bool { return slices.Contains(second, i) }
	for i, s := range slots {
		if !slices.Contains(s.OwnerIDs, playerID) {
			continue
		}
		switch {
		case won(i) && s.Value == answer:
			bonus, closest = ownerExact*m, true
		case won(i):
			bonus, closest = ownerWin*m, true
		case sec(i):
			bonus = ownerSecond * m
		}
	}
	if bet == nil {
		return bonus, 0, 0, closest
	}
	for _, c := range bet.Chips {
		switch {
		case won(c):
			chips += slots[c].Odds * m
		case sec(c):
			chips += secondChip * m
		}
	}
	if bet.Stake > 0 && bet.StakeSlot != nil {
		if won(*bet.StakeSlot) {
			stake = bet.Stake * slots[*bet.StakeSlot].Odds // never doubled
		} else {
			stake = -bet.Stake
		}
	}
	return bonus, chips, stake, closest
}
