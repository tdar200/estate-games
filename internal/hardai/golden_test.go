package hardai

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"

	"github.com/tdar200/estate-games/internal/engine"
)

// The Grandmaster level shares code with the others (hard.go, sim.go). Easy,
// Normal, Hard and Expert are tuned as a ladder, so they must play exactly as
// before it was added: these games, with the levels' production settings
// (no time limit, so they are deterministic), must end in the same states as
// when the hashes were recorded (3 November 2026, before Grandmaster).
//
// Re-recorded on 27 November 2026 for two reasons. (1) The hashes cover the
// log, so the board and card rename re-recorded all four: in the old wording
// "mixed" and "experts" still give their 3 November hashes (83d55e8948d4,
// 50607320d7a5). (2) A bug fix in internal/ai: in debt during a house
// shortage (bank under four houses) the heuristic now sells a hotel instead
// of going bankrupt with it. Neither real game reaches a shortage before it
// diverges; Hard and Expert rollouts (every seat played by the heuristic, 16
// rounds ahead) do, so two Monte Carlo choices moved: "loans" first differs at
// action 418 (a Hard bot ends its turn instead of offering a trade), "seven"
// at action 320 (an Expert bot rolls before making the same offer). With that
// fix off and the old wording, the old hashes (a773c258cb87, fea898d3bf20)
// come back exactly. The other fixes of that day (no mortgaging pledged
// deeds) never arise in these games.
//
// Re-recorded again on 27 November 2026 for the timed open auction: new
// games default to auctionClock 8, so their settings carry
// "rules":{"auctionClock":8} and every auction is open bidding against the
// clock. The driver's fixed clock never runs an auction's clock out, so the
// auctions end when all but the leader have passed; the levels' bidding
// limits are unchanged, but who bids when (and so the prices) differ. With
// the default put back to 0 (turn-order auctions) all four old hashes
// (8092762da680, 506eed4ca910, 839a7f514f68, e79b7626ef3f) come back
// exactly.
func TestLevelsUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("plays full-strength Expert games")
	}
	loans := true
	cases := []struct {
		name   string
		levels []Level
		seed   int64
		turns  int
		rules  *engine.RulesUpdate
		want   string
	}{
		{"mixed", []Level{Expert, Hard, Normal, Easy}, 11, 160, nil, "71f062ac4a63"},
		{"experts", []Level{Expert, Expert, Expert}, 12, 120, nil, "e93083caddb8"},
		{"loans", []Level{Hard, Expert, Easy, Normal, Hard}, 13, 120, &engine.RulesUpdate{Loans: &loans}, "d7ee5e4778f0"},
		{"seven", []Level{Expert, Normal, Hard, Easy, Normal, Expert, Normal}, 14, 140, nil, "2670878fa729"},
	}
	var wg sync.WaitGroup
	got := make([]string, len(cases))
	errs := make([]error, len(cases))
	for i, c := range cases {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := PlayMatch(MatchConfig{Levels: c.levels, Seed: c.seed, MaxTurns: c.turns, Rules: c.rules,
				Options: Options{Budget: -1, Workers: 2}})
			if err != nil {
				errs[i] = err
				return
			}
			data, _ := json.Marshal(res.Game)
			sum := sha256.Sum256(data)
			got[i] = hex.EncodeToString(sum[:])[:12]
		}(i)
	}
	wg.Wait()
	for i, c := range cases {
		if errs[i] != nil {
			t.Fatalf("%s: %v", c.name, errs[i])
		}
		if got[i] != c.want {
			t.Errorf("%s: final state hash %s, want %s (a shared change altered how the levels play)", c.name, got[i], c.want)
		}
	}
}
