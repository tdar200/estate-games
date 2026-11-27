package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tdar200/estate-games/internal/hardai"
	"github.com/tdar200/estate-games/internal/valuenet"
	"github.com/tdar200/estate-games/internal/valuenet2"
)

// The training stage reads league shards (valuenet2 records) and Expert's
// datasets (valuenet records, converted), keeping a share of the latter.
func TestLoadV2Records(t *testing.T) {
	dir := t.TempDir()
	// A started game to record positions from.
	game, err := hardai.NewGameWithRules([]string{"A", "B", "C", "D"}, nil, fixedRNG(0.3))
	if err != nil {
		t.Fatal(err)
	}
	// Expert's dataset: 50 records of 25 games.
	v1Dir := filepath.Join(dir, "data", "gen1")
	if err := os.MkdirAll(v1Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, valuenet.RecordSize)
	var v1 []byte
	for i := 0; i < 50; i++ {
		r := valuenet.Record{Game: uint32(i / 2), Winner: 1, Pos: valuenet.FromGame(game)}
		r.Encode(buf)
		v1 = append(v1, buf...)
	}
	if err := os.WriteFile(filepath.Join(v1Dir, "shard-00000.bin"), v1, 0o644); err != nil {
		t.Fatal(err)
	}
	// League data: 30 records with search targets.
	v2Dir := filepath.Join(dir, "data", "gm1")
	if err := os.MkdirAll(v2Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	recs := make([][]valuenet2.Record, 1)
	for i := 0; i < 30; i++ {
		r := valuenet2.Record{Game: uint32(i), Winner: 2, Pos: valuenet2.FromGame(game)}
		r.SetTarget([]float64{0.1, 0.2, 0.3, 0.4})
		recs[0] = append(recs[0], r)
	}
	if n, err := writeV2Shard(gmShardPath(v2Dir, 0), recs); err != nil || n != 30 {
		t.Fatalf("writeV2Shard: %d, %v", n, err)
	}
	all, v1n, err := loadV2Records(dir, []string{"gm1"}, []string{"gen1"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 80 || v1n != 50 {
		t.Fatalf("loaded %d records (%d converted), want 80 (50)", len(all), v1n)
	}
	targets := 0
	for _, r := range all {
		if _, ok := r.TargetProbs(); ok {
			targets++
		}
		if r.Pos.NPlayers != 4 {
			t.Fatalf("record with %d players", r.Pos.NPlayers)
		}
	}
	if targets != 30 {
		t.Fatalf("%d records with targets, want 30", targets)
	}
	// A quarter of Expert's games.
	_, v1n, err = loadV2Records(dir, nil, []string{"gen1"}, 0.25)
	if err != nil {
		t.Fatal(err)
	}
	if v1n == 0 || v1n >= 50 {
		t.Fatalf("kept %d of 50 converted records with a 0.25 share", v1n)
	}
}

// fixedRNG is an engine.RNG returning one value (for a deterministic test game).
type fixedRNG float64

func (f fixedRNG) Float64() float64 { return float64(f) }

// A league game records positions labelled with its winner, some of them with
// a search target, for any player count it draws.
func TestPlayLeague(t *testing.T) {
	c := gmConfig{Sample: 0.5, TargetShare: 0.5, TargetRollouts: 4, TargetRounds: 1, GMRollouts: 8, GMTradeRollouts: 8, MaxTurns: 80}
	v1, _ := valuenet.Default()
	got, withTargets := 0, int64(0)
	for id := uint32(0); id < 3; id++ {
		recs, nt := playLeague(id, 99, c, nil, v1)
		withTargets += nt
		for _, r := range recs {
			if r.Winner < 0 || int(r.Winner) >= int(r.Pos.NPlayers) {
				t.Fatalf("game %d: winner %d of %d players", id, r.Winner, r.Pos.NPlayers)
			}
		}
		got += len(recs)
	}
	if got == 0 || withTargets == 0 {
		t.Fatalf("%d positions, %d with targets", got, withTargets)
	}
}
