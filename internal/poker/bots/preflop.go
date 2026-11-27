package bots

import (
	"math"
	"sort"

	"github.com/tdar200/estate-games/internal/poker/holdem"
)

// Preflop strength: every starting hand gets a percentile (0 = aces, 1 =
// the worst hand) from the Chen formula, weighted by how many combinations
// each hand class has.

var preflopPctTable [13][13][2]float64 // [high][low][suited]

func chen(hi, lo int, suited bool) float64 {
	base := func(r int) float64 {
		switch r {
		case 12:
			return 10
		case 11:
			return 8
		case 10:
			return 7
		case 9:
			return 6
		default:
			return float64(r+2) / 2
		}
	}
	if hi == lo {
		return math.Max(5, 2*base(hi))
	}
	s := base(hi)
	if suited {
		s += 2
	}
	gap := hi - lo - 1
	switch {
	case gap == 1:
		s--
	case gap == 2:
		s -= 2
	case gap == 3:
		s -= 4
	case gap >= 4:
		s -= 5
	}
	if gap <= 1 && hi < 10 {
		s++
	}
	return math.Ceil(s)
}

func init() {
	type class struct {
		hi, lo int
		suited bool
		score  float64
		combos int
	}
	var classes []class
	for hi := 0; hi < 13; hi++ {
		for lo := 0; lo <= hi; lo++ {
			if hi == lo {
				classes = append(classes, class{hi, lo, false, chen(hi, lo, false), 6})
				continue
			}
			classes = append(classes, class{hi, lo, true, chen(hi, lo, true), 4})
			classes = append(classes, class{hi, lo, false, chen(hi, lo, false), 12})
		}
	}
	sort.SliceStable(classes, func(a, b int) bool {
		x, y := classes[a], classes[b]
		if x.score != y.score {
			return x.score > y.score
		}
		if x.hi != y.hi {
			return x.hi > y.hi
		}
		if x.lo != y.lo {
			return x.lo > y.lo
		}
		return x.suited && !y.suited
	})
	cum := 0
	for _, c := range classes {
		cum += c.combos
		s := 0
		if c.suited {
			s = 1
		}
		preflopPctTable[c.hi][c.lo][s] = float64(cum) / 1326
	}
}

// PreflopPercentile ranks two hole cards: 0.005 for aces … 1.0 for the
// weakest hand.
func PreflopPercentile(a, b holdem.Card) float64 {
	ra, rb := holdem.Rank(a), holdem.Rank(b)
	if ra < rb {
		ra, rb = rb, ra
	}
	s := 0
	if ra != rb && holdem.Suit(a) == holdem.Suit(b) {
		s = 1
	}
	return preflopPctTable[ra][rb][s]
}
