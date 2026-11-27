package bots

import (
	"math/rand/v2"

	"github.com/tdar200/estate-games/internal/poker/holdem"
	"github.com/tdar200/estate-games/internal/poker/holdem/eval"
)

// Range is a rough model of an opponent's hole cards, built from what they
// did this hand.
type Range struct {
	// Top keeps hands within this preflop percentile (1 = any two cards).
	// Hands outside it are still drawn now and then (Leak), because people
	// do not play ranges exactly.
	Top  float64
	Leak float64
	// Bets and raises after the flop (Aggression) and calls of a bet after
	// the flop (Calls): the more they put in, the more likely a made hand.
	Aggression int
	Calls      int
}

// AnyTwo is a random hand.
var AnyTwo = Range{Top: 1}

// accept reports whether hole cards fit the range given the visible board.
func (r Range) accept(rng *rand.Rand, a, b holdem.Card, board []holdem.Card) bool {
	if r.Top < 1 && PreflopPercentile(a, b) > r.Top && rng.Float64() >= r.Leak {
		return false
	}
	if len(board) < 3 || (r.Aggression == 0 && r.Calls == 0) {
		return true
	}
	cat := holdem.CategoryOf(scoreN(a, b, board))
	var p float64
	switch {
	case cat >= holdem.TwoPair:
		p = 1
	case cat == holdem.OnePair:
		p = 0.75
		if r.Aggression >= 2 {
			p = 0.45
		}
	default: // draws and bluffs
		p = 0.5
		if r.Aggression == 1 {
			p = 0.25
		} else if r.Aggression >= 2 {
			p = 0.1
		}
	}
	return rng.Float64() < p
}

// scoreN evaluates two hole cards with a 3–5 card board.
func scoreN(a, b holdem.Card, board []holdem.Card) int {
	switch len(board) {
	case 3:
		return eval.HandValue(a, b, board[0], board[1], board[2])
	case 4:
		_, s := eval.BestFiveOfSix(a, b, board[0], board[1], board[2], board[3])
		return s
	default:
		return holdem.Eval7([7]holdem.Card{a, b, board[0], board[1], board[2], board[3], board[4]})
	}
}

// Equity estimates the share of the pot hole cards win against opponents
// holding the given ranges, by Monte Carlo over iters random deals.
func Equity(rng *rand.Rand, hole [2]holdem.Card, board []holdem.Card, opps []Range, iters int) float64 {
	if len(opps) == 0 {
		return 1
	}
	dead := map[holdem.Card]bool{hole[0]: true, hole[1]: true}
	for _, c := range board {
		dead[c] = true
	}
	pool := make([]holdem.Card, 0, 52)
	for _, c := range holdem.NewDeck() {
		if !dead[c] {
			pool = append(pool, c)
		}
	}
	work := make([]holdem.Card, len(pool))
	oppHole := make([][2]holdem.Card, len(opps))
	var full [5]holdem.Card
	copy(full[:], board)
	total := 0.0
	for it := 0; it < iters; it++ {
		copy(work, pool)
		k := 0
		draw := func() holdem.Card {
			j := k + rng.IntN(len(work)-k)
			work[k], work[j] = work[j], work[k]
			k++
			return work[k-1]
		}
		for o, r := range opps {
			for try := 0; ; try++ {
				a, b := draw(), draw()
				if try >= 30 || r.accept(rng, a, b, board) {
					oppHole[o] = [2]holdem.Card{a, b}
					break
				}
				k -= 2 // put them back
			}
		}
		for i := len(board); i < 5; i++ {
			full[i] = draw()
		}
		me := holdem.Eval7([7]holdem.Card{hole[0], hole[1], full[0], full[1], full[2], full[3], full[4]})
		best, ties := me, 1
		lost := false
		for _, oh := range oppHole {
			s := holdem.Eval7([7]holdem.Card{oh[0], oh[1], full[0], full[1], full[2], full[3], full[4]})
			if s < best {
				lost = true
				break
			}
			if s == best {
				ties++
			}
		}
		if !lost {
			total += 1 / float64(ties)
		}
	}
	return total / float64(iters)
}
