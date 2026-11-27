package hardai

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/valuenet"
)

// The shipped knobs and model load, and Expert uses them.
func TestExpertDefaults(t *testing.T) {
	p := ExpertParams()
	if p == nil {
		t.Fatal("no expert params")
	}
	for _, k := range ai.Knobs {
		if v := k.Get(p); v < k.Min || v > k.Max {
			t.Fatalf("knob %s = %v outside [%v, %v]", k.Name, v, k.Min, k.Max)
		}
	}
	net, err := valuenet.Default()
	if err != nil {
		t.Fatalf("embedded value network: %v", err)
	}
	b := NewExpertBot(1, Options{})
	if b.opts.Eval != EvalValueNet || b.opts.Net != net || b.opts.Params == nil {
		t.Fatalf("expert options not filled: eval %d, net %v, params %v", b.opts.Eval, b.opts.Net != nil, b.opts.Params != nil)
	}
	if b.opts.Horizon != expertHorizon || b.opts.MaxCandidates != expertCandidates {
		t.Fatalf("expert horizon %d, candidates %d", b.opts.Horizon, b.opts.MaxCandidates)
	}
	if b.workers[0].vn == nil {
		t.Fatal("rollouts are not scored by the value network")
	}
}

// A tuned bot with the default knobs is the Normal bot, move for move.
func TestTunedDefaultsPlayLikeNormal(t *testing.T) {
	play := func(newBot func() Bot) []byte {
		res, err := PlayMatch(MatchConfig{Levels: []Level{Normal, Normal, Normal, Normal}, Seed: 77, MaxTurns: 200,
			NewBot: func(int, Level, int64) Bot { return newBot() }})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(res.Game)
		return data
	}
	normal := play(func() Bot { return NewHeuristicBot(false) })
	defaults := ai.DefaultParams
	if tuned := play(func() Bot { return NewTunedBot(&defaults) }); !bytes.Equal(normal, tuned) {
		t.Fatal("NewTunedBot(defaults) played differently from Normal")
	}
	if tuned := play(func() Bot { return NewTunedBot(nil) }); !bytes.Equal(normal, tuned) {
		t.Fatal("NewTunedBot(nil) played differently from Normal")
	}
}

// Expert seats never make a move the engine refuses, with classic rules,
// every house rule, and loans.
func TestExpertNeverIllegal(t *testing.T) {
	games := 6
	if testing.Short() {
		games = 3
	}
	var mu sync.Mutex
	mc := 0
	opts := soakOptions
	opts.OnDecision = func(d DecisionInfo) {
		if d.MonteCarlo {
			mu.Lock()
			mc++
			mu.Unlock()
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, games)
	rejected := make([]int, games)
	for i := 0; i < games; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			levels := []Level{Expert, Hard, Normal, Expert}[:3+i%2]
			rules := []*engine.RulesUpdate{nil, allRules(), loansOnly()}[i%3]
			res, err := PlayMatch(MatchConfig{Levels: levels, Seed: int64(900 + i), Options: opts, MaxTurns: 300, Rules: rules})
			errs[i] = err
			for _, s := range res.Seats {
				if s.Level == Expert {
					rejected[i] += s.Rejected
				}
			}
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("game %d: %v", i, errs[i])
		}
		if rejected[i] != 0 {
			t.Fatalf("game %d: %d Expert actions refused by the engine", i, rejected[i])
		}
	}
	if mc < 20 {
		t.Fatalf("only %d Monte Carlo decisions exercised", mc)
	}
}

// With no time budget an Expert game is fixed by its seed, whatever the
// number of rollout goroutines.
func TestExpertDeterministic(t *testing.T) {
	play := func(workers int) []byte {
		opts := soakOptions
		opts.Workers = workers
		res, err := PlayMatch(MatchConfig{Levels: []Level{Expert, Normal, Hard}, Seed: 43, Options: opts, MaxTurns: 120})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(res.Game)
		return data
	}
	first := play(1)
	if again := play(1); !bytes.Equal(first, again) {
		t.Fatal("same seed, different games")
	}
	if parallel := play(3); !bytes.Equal(first, parallel) {
		t.Fatal("the number of workers changed the game")
	}
}
