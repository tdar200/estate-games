package holdem

import (
	"math"
	"math/rand/v2"

	"github.com/tdar200/estate-games/internal/poker/holdem/eval"
)

// strengthSamples is the Monte Carlo sample count for you.strength (about
// ±1.3% at one standard error; a few milliseconds even against seven
// opponents).
const strengthSamples = 1500

// strengthEntry caches one player's you.strength: it is recomputed only when
// a new street is dealt or the number of opponents still in changes.
type strengthEntry struct {
	hand, board, opps int
	value             float64
}

// strengthFor returns you.strength for the player at hand index i.
func (t *Table) strengthFor(id string, i int) float64 {
	h := t.Hand
	opps := 0
	for k := 0; k < h.N; k++ {
		if k != i && !h.Folded[k] {
			opps++
		}
	}
	e, ok := t.strength[id]
	if ok && e.hand == t.HandNumber && e.board == len(h.Board) && e.opps == opps {
		return e.value
	}
	seed := uint64(t.HandNumber)<<24 | uint64(i)<<16 | uint64(len(h.Board))<<8 | uint64(opps)
	v := Strength([2]Card{h.Hole[i][0], h.Hole[i][1]}, h.Board, opps, seed)
	e = strengthEntry{hand: t.HandNumber, board: len(h.Board), opps: opps, value: math.Round(v*100) / 100}
	if t.strength == nil {
		t.strength = map[string]strengthEntry{}
	}
	t.strength[id] = e
	return e.value
}

// Strength estimates the chance that hole wins the hand by the river against
// opps opponents holding random cards, with ties shared (two-way tie = ½):
// the board cards still to come are dealt out in every sample, so a strong
// draw counts for what it is worth (the usual meaning of a strength meter).
// On the river it is simply the chance of holding the best hand. It uses
// only the player's own cards, the board and the number of opponents, so it
// reveals nothing hidden. seed makes it deterministic.
func Strength(hole [2]Card, board []Card, opps int, seed uint64) float64 {
	if opps <= 0 {
		return 1
	}
	dead := map[Card]bool{hole[0]: true, hole[1]: true}
	for _, c := range board {
		dead[c] = true
	}
	work := make([]Card, 0, 52)
	for _, c := range NewDeck() {
		if !dead[c] {
			work = append(work, c)
		}
	}
	rng := rand.New(rand.NewPCG(seed, 0x57e6))
	rollout := len(board) < 5
	var full [5]Card
	copy(full[:], board)
	cur := full[:len(board)]
	mine := worstScore
	if !rollout {
		mine = scoreWith(hole[0], hole[1], cur)
	}
	total := 0.0
	for s := 0; s < strengthSamples; s++ {
		k := 0
		draw := func() Card {
			j := k + rng.IntN(len(work)-k)
			work[k], work[j] = work[j], work[k]
			k++
			return work[k-1]
		}
		b := cur
		if rollout {
			for n := len(board); n < 5; n++ {
				full[n] = draw()
			}
			b = full[:]
			mine = scoreWith(hole[0], hole[1], b)
		}
		ties, lost := 1, false
		for o := 0; o < opps; o++ {
			sc := scoreWith(draw(), draw(), b)
			if sc < mine {
				lost = true
				break
			}
			if sc == mine {
				ties++
			}
		}
		if !lost {
			total += 1 / float64(ties)
		}
	}
	return total / strengthSamples
}

// scoreWith scores two hole cards with a three- to five-card board (lower is
// better, as everywhere in this package).
func scoreWith(a, b Card, board []Card) Score {
	switch len(board) {
	case 3:
		return eval.HandValue(a, b, board[0], board[1], board[2])
	case 4:
		c := [6]Card{a, b, board[0], board[1], board[2], board[3]}
		best := worstScore
		for skip := 0; skip < 6; skip++ {
			var f [5]Card
			n := 0
			for j := 0; j < 6; j++ {
				if j != skip {
					f[n] = c[j]
					n++
				}
			}
			best = min(best, eval.HandValue(f[0], f[1], f[2], f[3], f[4]))
		}
		return best
	default:
		return Eval7([7]Card{a, b, board[0], board[1], board[2], board[3], board[4]})
	}
}
