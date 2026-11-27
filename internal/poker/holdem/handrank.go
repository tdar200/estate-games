package holdem

import (
	"fmt"
	"sort"

	"github.com/tdar200/estate-games/internal/poker/holdem/eval"
)

// Score is a hand value from the riverboat evaluator: 1 (royal flush) …
// 7462 (7-5-4-3-2 offsuit). Lower is better; equal scores split.
type Score = int

// worstScore is larger than any real hand.
const worstScore = 8000

var combos7 [21][5]int

func init() {
	n := 0
	for a := 0; a < 7; a++ {
		for b := a + 1; b < 7; b++ {
			// the five cards are the seven minus a and b
			k := 0
			for i := 0; i < 7; i++ {
				if i != a && i != b {
					combos7[n][k] = i
					k++
				}
			}
			n++
		}
	}
}

// Eval7 scores the best five of seven cards (21 lookups).
func Eval7(c [7]Card) Score {
	best := worstScore
	for _, cb := range combos7 {
		if s := eval.HandValue(c[cb[0]], c[cb[1]], c[cb[2]], c[cb[3]], c[cb[4]]); s < best {
			best = s
		}
	}
	return best
}

// EvalBest scores the best five-card hand from 5–7 cards and returns those
// five cards, ordered for display (see describe).
func EvalBest(cards []Card) (Score, []Card) {
	n := len(cards)
	if n < 5 {
		return worstScore, nil
	}
	best := worstScore
	var bestIdx [5]int
	var idx [5]int
	var rec func(start, depth int)
	rec = func(start, depth int) {
		if depth == 5 {
			s := eval.HandValue(cards[idx[0]], cards[idx[1]], cards[idx[2]], cards[idx[3]], cards[idx[4]])
			if s < best {
				best = s
				bestIdx = idx
			}
			return
		}
		for i := start; i <= n-(5-depth); i++ {
			idx[depth] = i
			rec(i+1, depth+1)
		}
	}
	rec(0, 0)
	five := make([]Card, 5)
	for i, j := range bestIdx {
		five[i] = cards[j]
	}
	return best, orderFive(five, best)
}

// Category is the hand class.
type Category int

const (
	HighCard Category = iota
	OnePair
	TwoPair
	ThreeOfAKind
	Straight
	Flush
	FullHouse
	FourOfAKind
	StraightFlush
)

var categoryNames = [...]string{"High card", "Pair", "Two pair", "Three of a kind", "Straight", "Flush", "Full house", "Four of a kind", "Straight flush"}

func (c Category) String() string { return categoryNames[c] }

// CategoryOf maps a score to its class (Cactus Kev boundaries).
func CategoryOf(s Score) Category {
	switch {
	case s <= 10:
		return StraightFlush
	case s <= 166:
		return FourOfAKind
	case s <= 322:
		return FullHouse
	case s <= 1599:
		return Flush
	case s <= 1609:
		return Straight
	case s <= 2467:
		return ThreeOfAKind
	case s <= 3325:
		return TwoPair
	case s <= 6185:
		return OnePair
	default:
		return HighCard
	}
}

var rankNames = [13]string{"Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine", "Ten", "Jack", "Queen", "King", "Ace"}
var rankPlurals = [13]string{"Twos", "Threes", "Fours", "Fives", "Sixes", "Sevens", "Eights", "Nines", "Tens", "Jacks", "Queens", "Kings", "Aces"}

// orderFive sorts five cards for display: grouped ranks first (quads, trips,
// pairs), then kickers high to low; straights high to low with a wheel's ace last.
func orderFive(five []Card, s Score) []Card {
	count := map[int]int{}
	for _, c := range five {
		count[Rank(c)]++
	}
	out := append([]Card(nil), five...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := Rank(out[i]), Rank(out[j])
		if count[ri] != count[rj] {
			return count[ri] > count[rj]
		}
		if ri != rj {
			return ri > rj
		}
		return Suit(out[i]) > Suit(out[j])
	})
	cat := CategoryOf(s)
	if (cat == Straight || cat == StraightFlush) && Rank(out[0]) == 12 && Rank(out[1]) == 3 {
		out = append(out[1:], out[0]) // 5-4-3-2-A
	}
	return out
}

// Describe names a scored hand from its ordered best five, e.g. "Two pair,
// Kings and Sevens" or "Straight, Ten high".
func Describe(s Score, five []Card) string {
	if len(five) != 5 {
		return ""
	}
	r := func(i int) int { return Rank(five[i]) }
	switch CategoryOf(s) {
	case StraightFlush:
		if r(0) == 12 {
			return "Royal flush"
		}
		return "Straight flush, " + rankNames[r(0)] + " high"
	case FourOfAKind:
		return "Four of a kind, " + rankPlurals[r(0)]
	case FullHouse:
		return "Full house, " + rankPlurals[r(0)] + " full of " + rankPlurals[r(3)]
	case Flush:
		return "Flush, " + rankNames[r(0)] + " high"
	case Straight:
		return "Straight, " + rankNames[r(0)] + " high"
	case ThreeOfAKind:
		return "Three of a kind, " + rankPlurals[r(0)]
	case TwoPair:
		return "Two pair, " + rankPlurals[r(0)] + " and " + rankPlurals[r(2)]
	case OnePair:
		return "Pair of " + rankPlurals[r(0)]
	default:
		return rankNames[r(0)] + " high"
	}
}

// DescribeCards evaluates 5–7 cards and names the best hand.
func DescribeCards(cards []Card) (Score, []Card, string) {
	s, five := EvalBest(cards)
	if five == nil {
		return s, nil, ""
	}
	return s, five, Describe(s, five)
}

// DescribeHole names two hole cards before the flop, e.g. "Pocket Aces",
// "Ace-King suited", "Nine-Four offsuit".
func DescribeHole(a, b Card) string {
	ra, rb := Rank(a), Rank(b)
	if ra < rb {
		ra, rb = rb, ra
	}
	if ra == rb {
		return "Pocket " + rankPlurals[ra]
	}
	kind := "offsuit"
	if Suit(a) == Suit(b) {
		kind = "suited"
	}
	return fmt.Sprintf("%s-%s %s", rankNames[ra], rankNames[rb], kind)
}
