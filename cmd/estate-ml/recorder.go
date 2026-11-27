package main

import (
	"bufio"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/hardai"
	"github.com/tdar200/estate-games/internal/valuenet"
)

// recorder samples positions from games played for another purpose (the
// tuner's games) and writes them as a value-network dataset shard: the
// games are played anyway, so their positions come for free. A nil
// recorder records nothing.
type recorder struct {
	gen        int
	sample     float64
	candOffset int
	mu         sync.Mutex
	recs       []valuenet.Record
}

// recordedGame collects the sampled positions of one game.
type recordedGame struct {
	r    *recorder
	id   uint32
	rng  *rand.Rand
	recs []valuenet.Record
	last *engine.Turn
}

// game returns the collector for the game of candidate cand, deal seed and
// seat (nil when r is nil).
func (r *recorder) game(cand, seed, seat int) *recordedGame {
	if r == nil {
		return nil
	}
	id := uint32(((r.gen*64+cand+r.candOffset)*1024+seed)*4 + seat)
	return &recordedGame{r: r, id: id, rng: rand.New(rand.NewPCG(uint64(id), 0x5a3e))}
}

// onAction samples the position at the start of each new turn.
func (rg *recordedGame) onAction() func(g *engine.Game, playerID string, a engine.Action) {
	if rg == nil {
		return nil
	}
	return func(g *engine.Game, _ string, _ engine.Action) {
		if g.Turn == rg.last {
			return
		}
		rg.last = g.Turn
		if g.Status == engine.StatusPlaying && g.Phase == engine.PhaseAwaitingRoll && g.Turn != nil && rg.rng.Float64() < rg.r.sample {
			rg.recs = append(rg.recs, valuenet.Record{Game: rg.id, Winner: -1, Pos: valuenet.FromGame(g)})
		}
	}
}

// finish labels the game's positions with its winner and hands them over.
func (rg *recordedGame) finish(res hardai.MatchResult) {
	if rg == nil || len(rg.recs) == 0 || res.Game == nil {
		return
	}
	winnerID := ""
	for _, s := range res.Seats {
		if s.Won {
			winnerID = s.PlayerID
		}
	}
	winner := -1
	for i, p := range res.Game.Players {
		if p.ID == winnerID {
			winner = i
		}
	}
	if winner < 0 {
		return
	}
	for i := range rg.recs {
		rg.recs[i].Winner = int8(winner)
	}
	rg.r.mu.Lock()
	rg.r.recs = append(rg.r.recs, rg.recs...)
	rg.r.mu.Unlock()
}

// flush writes the collected positions to path (atomically) and returns the
// count.
func (r *recorder) flush(path string) (int, error) {
	if r == nil {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(path + ".tmp")
	if err != nil {
		return 0, err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	buf := make([]byte, valuenet.RecordSize)
	for i := range r.recs {
		r.recs[i].Encode(buf)
		if _, err := bw.Write(buf); err != nil {
			f.Close()
			return 0, err
		}
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	n := len(r.recs)
	r.recs = nil
	return n, os.Rename(path+".tmp", path)
}

func tuneShardPath(dir string, gen int, kind string) string {
	return filepath.Join(dir, "data", "tune", fmt.Sprintf("shard-%s%03d.bin", kind, gen))
}
