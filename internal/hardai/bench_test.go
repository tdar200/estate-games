package hardai

import (
	"os"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
)

// Benchmarks and diagnostics. The diagnostics only log; they run when
// HARDAI_PROBE is set:
//
//	HARDAI_PROBE=1 go test ./internal/hardai -run Probe -v

func probe(t *testing.T) {
	t.Helper()
	if os.Getenv("HARDAI_PROBE") == "" {
		t.Skip("diagnostic; set HARDAI_PROBE=1")
	}
}

func heuristicDriver(g *engine.Game, rng engine.RNG, maxTurns int) *Driver {
	d := &Driver{RNG: rng, TrimLog: true, MaxTurns: maxTurns}
	h := NewHeuristicBot(false)
	for _, p := range g.Players {
		d.Seats = append(d.Seats, Seat{PlayerID: p.ID, Bot: h})
	}
	return d
}

// BenchmarkHeuristicGame plays whole 4-seat Normal games.
func BenchmarkHeuristicGame(b *testing.B) {
	for i := 0; i < b.N; i++ {
		rng := newSplitMix(uint64(i))
		g, _ := NewGame([]string{"A", "B", "C", "D"}, rng)
		heuristicDriver(g, rng, 2000).Play(g)
	}
}

// BenchmarkRollout is one 16-round rollout from a mid-game state.
func BenchmarkRollout(b *testing.B) {
	rng := newSplitMix(3)
	g, _ := NewGame([]string{"A", "B", "C", "D"}, rng)
	heuristicDriver(g, rng, 40).Play(g)
	w := &simWorker{}
	root := cloneGame(g, true)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sim := cloneGame(root, true)
		w.prepareSim(sim, uint64(i))
		w.run(sim, 64)
	}
}

// BenchmarkClone is Clone of a mid-game state.
func BenchmarkClone(b *testing.B) {
	rng := newSplitMix(3)
	g, _ := NewGame([]string{"A", "B", "C", "D"}, rng)
	heuristicDriver(g, rng, 40).Play(g)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Clone(g)
	}
}

// BenchmarkHardGame plays Hard vs 3 Normal with 16 rounds per decision.
func BenchmarkHardGame(b *testing.B) {
	for i := 0; i < b.N; i++ {
		PlayMatch(MatchConfig{Levels: []Level{Hard, Normal, Normal, Normal}, Seed: int64(i), Options: Options{Budget: -1, Rollouts: 16, Workers: 1}})
	}
}

// TestDecisionProbe prints where a Hard decision's time goes.
func TestDecisionProbe(t *testing.T) {
	probe(t)
	for _, horizon := range []int{8, 16} {
		var n, rounds, rollouts int
		var overhead, total time.Duration
		opts := Options{Budget: 100 * time.Millisecond, Workers: 1, Horizon: horizon, OnDecision: func(d DecisionInfo) {
			if d.MonteCarlo {
				n++
				rounds += d.Rounds
				rollouts += d.Rollouts
				overhead += d.Overhead
				total += d.Duration
			}
		}}
		if _, err := PlayMatch(MatchConfig{Levels: []Level{Hard, Normal, Normal, Normal}, Seed: 5, Options: opts, MaxTurns: 120}); err != nil {
			t.Fatal(err)
		}
		t.Logf("horizon %d: %d MC decisions, avg %v (overhead %v), %.1f rounds, %.1f rollouts, %v per rollout",
			horizon, n, total/time.Duration(n), overhead/time.Duration(n), float64(rounds)/float64(n), float64(rollouts)/float64(n),
			(total-overhead)/time.Duration(max(1, rollouts)))
	}
}

// TestTradeProbe lists the trades Normal players make.
func TestTradeProbe(t *testing.T) {
	probe(t)
	kinds := map[string]int{}
	for i := 0; i < 10; i++ {
		rng := newSplitMix(uint64(i))
		g, _ := NewGame([]string{"A", "B", "C", "D"}, rng)
		d := heuristicDriver(g, rng, 2000)
		d.OnAction = func(g *engine.Game, id string, a engine.Action) {
			if a.Type != engine.ActionOfferTrade {
				return
			}
			k := ""
			for _, idx := range a.Request.Properties {
				k += engine.Board[idx].Type + ":" + engine.Board[idx].Group + " "
			}
			k += "<- "
			for _, idx := range a.Offer.Properties {
				k += engine.Board[idx].Type + ":" + engine.Board[idx].Group + " "
			}
			if a.Offer.Cash > 0 {
				k += "+cash"
			}
			kinds[k]++
		}
		if _, err := d.Play(g); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range kinds {
		t.Logf("%4d %s", v, k)
	}
}
