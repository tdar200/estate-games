package quiz

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

// TestBotGamesFinish plays 10,000 games of computers only (1–8 of them, any
// levels, any settings) and checks that every game finishes and that every
// score is exactly what its events add up to: each board clue's right and
// wrong, and each final reveal.
func TestBotGamesFinish(t *testing.T) {
	games := 10000
	if testing.Short() {
		games = 1000
	}
	lib := testLibrary(t)
	var failures atomic.Int32
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				if err := playBotGame(lib, n); err != nil && failures.Add(1) <= 5 {
					t.Errorf("game %d: %v", n, err)
				}
			}
		}()
	}
	for n := range games {
		jobs <- n
	}
	close(jobs)
	wg.Wait()
}

func playBotGame(lib *Library, n int) error {
	var seed [32]byte
	binary.LittleEndian.PutUint64(seed[:], uint64(n)+1)
	c := &clock{now: start}
	g := NewGame("SIM", Options{Seed: &seed, Clock: c.read, Library: lib})
	r := mrand.New(mrand.NewPCG(uint64(n), 7))
	for i := range 1 + r.IntN(MaxPlayers) {
		if _, err := g.AddComputer(fmt.Sprintf("Bot %d", i+1), Levels[r.IntN(len(Levels))]); err != nil {
			return err
		}
	}
	settings, _ := json.Marshal(map[string]any{"type": "settings", "settings": Settings{
		Length:        []string{LengthQuick, LengthFull}[r.IntN(2)],
		ReadSpeed:     []string{SpeedSlow, SpeedNormal, SpeedFast}[r.IntN(3)],
		AnswerSeconds: []int{6, 8, 12}[r.IntN(3)],
	}})
	if err := g.PerformAction(TableHost, settings); err != nil {
		return err
	}
	if err := g.PerformAction(TableHost, json.RawMessage(`{"type":"start"}`)); err != nil {
		return err
	}
	want := map[string]int{}
	var seq int64
	for steps := 0; g.Status == StatusPlaying; steps++ {
		at := g.NextDeadline()
		switch {
		case steps > 20000:
			return fmt.Errorf("still playing after %d steps (phase %s)", steps, g.Phase)
		case at == 0:
			return fmt.Errorf("nothing timed in phase %s", g.Phase)
		case at < c.now:
			return fmt.Errorf("deadline %d is before the clock %d in phase %s", at, c.now, g.Phase)
		}
		c.now = at
		g.Advance(at)
		for _, e := range g.EventsSince(seq) {
			seq = e.Seq
			switch e.Type {
			case EvRight:
				want[e.PlayerID] += e.Value
			case EvWrong:
				want[e.PlayerID] -= e.Value
			case EvFinalReveal:
				if *e.Correct {
					want[e.PlayerID] += *e.Bet
				} else {
					want[e.PlayerID] -= *e.Bet
				}
			}
		}
	}
	return checkFinished(g, want)
}

// checkFinished checks a game that played to its end: scores match the
// events, every clue was played, everyone's final was revealed, and places
// and winners follow the scores.
func checkFinished(g *Game, want map[string]int) error {
	if g.Phase != PhaseFinished || g.Round != finalRound {
		return fmt.Errorf("ended in phase %s, round %d", g.Phase, g.Round)
	}
	for _, p := range g.Players {
		if p.Score != want[p.ID] {
			return fmt.Errorf("%s scored %d, events say %d", p.ID, p.Score, want[p.ID])
		}
	}
	for _, b := range g.Boards {
		for _, bc := range b.Categories {
			for _, cell := range bc.Cells {
				if !cell.Played {
					return fmt.Errorf("round %d %s row %d was never played", b.Round, bc.Title, cell.Row)
				}
			}
		}
	}
	if g.Final.Revealed != len(g.Players) || len(g.Final.Results) != len(g.Players) {
		return fmt.Errorf("final revealed %d of %d", g.Final.Revealed, len(g.Players))
	}
	top := g.Players[0].Score
	for _, p := range g.Players {
		top = max(top, p.Score)
	}
	for _, p := range g.Players {
		higher := 0
		for _, q := range g.Players {
			if q.Score > p.Score {
				higher++
			}
		}
		if p.Place != higher+1 {
			return fmt.Errorf("%s has %d points and place %d, want %d", p.ID, p.Score, p.Place, higher+1)
		}
		if won := p.Score == top; won != containsID(g.WinnerIDs, p.ID) {
			return fmt.Errorf("%s with %d points: winner %v, winners %v", p.ID, p.Score, won, g.WinnerIDs)
		}
	}
	return nil
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
