package holdem

import "math/rand/v2"

// Odds is a live player's chance, once the board is complete, of holding
// the best hand alone (Win) or sharing it (Tie).
type Odds struct {
	Win float64 `json:"win"`
	Tie float64 `json:"tie"`
}

// oddsSamples is the Monte Carlo sample count when three or more board
// cards are still to come (about ±0.3% at one standard error); with two or
// fewer to come every board is enumerated exactly.
const oddsSamples = 30000

// Odds computes the win and tie chances of every live player (indexed by
// hand index; folded players get zero) from the live hole cards and the
// board. Folded players' cards are treated as unknown, as they are to the
// people watching, so the numbers reveal nothing about mucked hands. seed
// makes the sampled case deterministic.
func (h *Hand) Odds(seed uint64) []Odds {
	out := make([]Odds, h.N)
	var live []int
	for i := 0; i < h.N; i++ {
		if !h.Folded[i] {
			live = append(live, i)
		}
	}
	if len(live) == 1 {
		out[live[0]].Win = 1
	}
	if len(live) < 2 {
		return out
	}
	known := map[Card]bool{}
	for _, i := range live {
		known[h.Hole[i][0]], known[h.Hole[i][1]] = true, true
	}
	for _, c := range h.Board {
		known[c] = true
	}
	var pool []Card
	for _, c := range NewDeck() {
		if !known[c] {
			pool = append(pool, c)
		}
	}
	var board [5]Card
	copy(board[:], h.Board)
	nb := len(h.Board)
	need := 5 - nb
	wins := make([]float64, h.N)
	ties := make([]float64, h.N)
	scores := make([]Score, len(live))
	total := 0.0
	score := func() {
		best := worstScore
		for k, i := range live {
			scores[k] = Eval7([7]Card{h.Hole[i][0], h.Hole[i][1], board[0], board[1], board[2], board[3], board[4]})
			best = min(best, scores[k])
		}
		n := 0
		for _, s := range scores {
			if s == best {
				n++
			}
		}
		for k, i := range live {
			if scores[k] == best {
				if n == 1 {
					wins[i]++
				} else {
					ties[i]++
				}
			}
		}
		total++
	}
	switch need {
	case 0:
		score()
	case 1:
		for _, a := range pool {
			board[4] = a
			score()
		}
	case 2:
		for x := 0; x < len(pool); x++ {
			for y := x + 1; y < len(pool); y++ {
				board[3], board[4] = pool[x], pool[y]
				score()
			}
		}
	default:
		rng := rand.New(rand.NewPCG(seed, 0x0dd5))
		work := append([]Card(nil), pool...)
		for s := 0; s < oddsSamples; s++ {
			for k := 0; k < need; k++ {
				j := k + rng.IntN(len(work)-k)
				work[k], work[j] = work[j], work[k]
				board[nb+k] = work[k]
			}
			score()
		}
	}
	for _, i := range live {
		out[i] = Odds{Win: wins[i] / total, Tie: ties[i] / total}
	}
	return out
}
