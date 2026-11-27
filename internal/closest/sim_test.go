package closest

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	mrand "math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// TestBotGamesFinish plays games of computers only at 1, 2, 4 and 8 seats
// (any levels, any settings, fill on and off) and checks that every game
// finishes with every question revealed, and that every score is exactly
// what the reveal events add up to: a game record carries enough to replay
// the scoring.
func TestBotGamesFinish(t *testing.T) {
	per := 500
	if testing.Short() {
		per = 50
	}
	lib := testLibrary(t)
	var failures atomic.Int32
	type job struct{ seats, n int }
	jobs := make(chan job)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if err := playBotGame(lib, j.seats, j.n); err != nil && failures.Add(1) <= 5 {
					t.Errorf("%d seats, game %d: %v", j.seats, j.n, err)
				}
			}
		}()
	}
	for _, seats := range []int{1, 2, 4, 8} {
		for n := range per {
			jobs <- job{seats, n}
		}
	}
	close(jobs)
	wg.Wait()
}

func playBotGame(lib *Library, seats, n int) error {
	var seed [32]byte
	binary.LittleEndian.PutUint64(seed[:], uint64(seats*100000+n)+1)
	c := &clock{now: start}
	g := NewGame("SIM", Options{Seed: &seed, Clock: c.read, Library: lib})
	r := mrand.New(mrand.NewPCG(uint64(seats), uint64(n)))
	for i := range seats {
		if _, err := g.AddComputer(fmt.Sprintf("Bot %d", i+1), Levels[r.IntN(len(Levels))]); err != nil {
			return err
		}
	}
	settings, _ := json.Marshal(map[string]any{"type": "settings", "settings": Settings{
		Length:       []string{LengthQuick, LengthFull}[r.IntN(2)],
		GuessSeconds: []int{20, 30, 45}[r.IntN(3)],
		BetSeconds:   []int{15, 25, 35}[r.IntN(3)],
		Fill:         n%2 == 0,
	}})
	if err := g.PerformAction(TableHost, settings); err != nil {
		return err
	}
	if err := g.PerformAction(TableHost, json.RawMessage(`{"type":"start"}`)); err != nil {
		return err
	}
	want := map[string]int{}
	closest := map[string]int{}
	reveals := 0
	var seq int64
	for steps := 0; g.Status == StatusPlaying; steps++ {
		at := g.NextDeadline()
		switch {
		case steps > 5000:
			return fmt.Errorf("still playing after %d steps: %s", steps, g)
		case at == 0:
			return fmt.Errorf("nothing timed: %s", g)
		case at < c.now:
			return fmt.Errorf("deadline %d is before the clock %d: %s", at, c.now, g)
		}
		c.now = at
		g.Advance(at)
		for _, e := range g.EventsSince(seq) {
			seq = e.Seq
			if e.Type != EvReveal {
				continue
			}
			reveals++
			m := 1
			if reveals == len(g.Questions) {
				m = 2
			}
			slots := lineUp(e.Guesses)
			winners, second := judge(slots, *e.Answer)
			for _, p := range g.Players {
				var b *Bet
				for i := range e.Bets {
					if e.Bets[i].PlayerID == p.ID {
						b = &e.Bets[i]
					}
				}
				bonus, chips, stake, won := payout(slots, winners, second, *e.Answer, m, p.ID, b, true)
				want[p.ID] = max(0, want[p.ID]+bonus+chips+stake)
				if won {
					closest[p.ID]++
				}
			}
		}
	}
	wantPlayers := max(seats, 0)
	if n%2 == 0 {
		wantPlayers = max(seats, FillTo)
	}
	switch {
	case g.Status != StatusFinished:
		return fmt.Errorf("not finished: %s", g)
	case reveals != len(g.Questions):
		return fmt.Errorf("%d of %d questions revealed", reveals, len(g.Questions))
	case len(g.Players) != wantPlayers:
		return fmt.Errorf("%d players, want %d", len(g.Players), wantPlayers)
	case len(g.WinnerIDs) == 0:
		return fmt.Errorf("nobody won")
	}
	for _, p := range g.Players {
		if p.Score != want[p.ID] || p.Closest != closest[p.ID] {
			return fmt.Errorf("%s scored %d (%d closest); the events say %d (%d)", p.Name, p.Score, p.Closest, want[p.ID], closest[p.ID])
		}
	}
	return nil
}
