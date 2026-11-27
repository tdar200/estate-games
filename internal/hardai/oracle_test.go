package hardai

import (
	"math"
	"testing"

	"github.com/tdar200/estate-games/internal/engine"
)

// The oracle's paired comparison: an option against itself differs by
// exactly zero (common random numbers), and a real choice gets a finite
// verdict with a standard error.
func TestOracleIsPaired(t *testing.T) {
	spots := tradeSpots(t, 1, func(*engine.Game, string, string, int) bool { return true })
	s := spots[0]
	g := Clone(s.g)
	if err := engine.PerformAction(g, s.proposer, tradeAction(s.target, 10, nil, 0, []int{s.deed}), nil); err != nil {
		t.Fatal(err)
	}
	o := NewOracle(OracleOptions{Rollouts: 24, Rounds: 2, Workers: 3, Seed: 1})
	res := o.Evaluate(g, s.target, []OracleOption{
		{Name: "reject", Actions: []engine.Action{{Type: engine.ActionRejectTrade}}},
		{Name: "reject again", Actions: []engine.Action{{Type: engine.ActionRejectTrade}}},
		{Name: "accept", Actions: []engine.Action{{Type: engine.ActionAcceptTrade}}},
	})
	if res.Diff[1] != 0 || res.SE[1] != 0 {
		t.Fatalf("the same option differs from itself: %v ± %v", res.Diff[1], res.SE[1])
	}
	if math.IsNaN(res.Diff[2]) || math.IsInf(res.SE[2], 0) || res.Mean[2] < 0 || res.Mean[2] > 1 {
		t.Fatalf("verdict %v ± %v (mean %v)", res.Diff[2], res.SE[2], res.Mean[2])
	}
	// Independent of the number of workers.
	o1 := NewOracle(OracleOptions{Rollouts: 24, Rounds: 2, Workers: 1, Seed: 1})
	res1 := o1.Evaluate(g, s.target, []OracleOption{
		{Name: "reject", Actions: []engine.Action{{Type: engine.ActionRejectTrade}}},
		{Name: "reject again", Actions: []engine.Action{{Type: engine.ActionRejectTrade}}},
		{Name: "accept", Actions: []engine.Action{{Type: engine.ActionAcceptTrade}}},
	})
	if res1.Diff[2] != res.Diff[2] {
		t.Fatalf("workers changed the verdict: %v vs %v", res1.Diff[2], res.Diff[2])
	}
}

// The clairvoyant analysis bot plays legal moves.
func TestClairvoyantPlaysLegally(t *testing.T) {
	res, err := PlayMatch(MatchConfig{Levels: []Level{Expert, Normal, Normal}, Seed: 5, MaxTurns: 80,
		Options: Options{Budget: -1, Workers: 1},
		NewBotPeek: func(seat int, level Level, seed int64, peek func() engine.RNG) Bot {
			if seat == 0 {
				return NewClairvoyantBot(seed, Options{Budget: -1, Workers: 1}, peek)
			}
			return NewBot(level, seed, Options{})
		}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Seats[0].Rejected != 0 {
		t.Fatalf("%d refused actions", res.Seats[0].Rejected)
	}
}
