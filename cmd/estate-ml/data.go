package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/hardai"
	"github.com/tdar200/estate-games/internal/valuenet"
)

// data: play self-play games and record positions for the value network.
//
// Games are played in shards of -shard games (data/<set>/shard-NNNNN.bin, a
// sequence of valuenet records written atomically once the shard is done);
// a restart skips the shards already on disk. Each game draws its player
// count (2-6, mostly 4), its house rules (mostly classic) and each seat's bot
// (mostly the tuned heuristic, also Normal and noisy tuned variants, for
// variety). At the start of each turn the position is recorded with
// probability -sample; the label is the game's winner (or the net-worth
// leader of a game stopped by the turn cap).

func cmdData(args []string) {
	fs := flag.NewFlagSet("data", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "work directory")
	set := fs.String("set", "gen1", "dataset name (data/<set>/)")
	games := fs.Int("games", 150000, "games to play in total")
	shard := fs.Int("shard", 2000, "games per shard")
	workers := fs.Int("workers", 24, "games played at once")
	paramsPath := fs.String("params", "", "tuned knobs JSON (default: tune/best.json)")
	sample := fs.Float64("sample", 0.2, "probability of recording a turn start")
	seed := fs.Uint64("seed", 7, "seed")
	maxTurns := fs.Int("maxturns", 1000, "turn cap")
	expert := fs.Float64("expert", 0, "share of seats played by the EXPERT bot (needs -budget)")
	budget := fs.Duration("budget", 20*time.Millisecond, "EXPERT decision budget (with -expert)")
	netPath := fs.String("net", "", "EXPERT value network (with -expert; default: the embedded model)")
	expertHorizon := fs.Int("expert-horizon", 0, "EXPERT rollout rounds (0 = default)")
	fs.Parse(args)

	if *paramsPath == "" {
		*paramsPath = filepath.Join(*dir, "tune", "best.json")
	}
	tuned := ai.DefaultParams
	if data, err := os.ReadFile(*paramsPath); err == nil {
		if err := json.Unmarshal(data, &tuned); err != nil {
			fatalf("data: %s: %v", *paramsPath, err)
		}
	} else {
		logf("data: no tuned knobs at %s; using the defaults", *paramsPath)
	}
	outDir := filepath.Join(*dir, "data", *set)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("data: %v", err)
	}
	shards := (*games + *shard - 1) / *shard
	rep := newReporter(*dir, "data", "data-"+*set)
	defer rep.close()
	var positions, bytes atomic.Int64
	var gamesDone atomic.Int64
	// Count what is already on disk.
	for k := 0; k < shards; k++ {
		if st, err := os.Stat(shardPath(outDir, k)); err == nil {
			bytes.Add(st.Size())
			positions.Add(st.Size() / valuenet.RecordSize)
			gamesDone.Add(int64(*shard))
		}
	}
	writeData := func() {
		_ = writeJSONAtomic(filepath.Join(*dir, "status", "data.json"), map[string]any{
			"positions": positions.Load(), "games": gamesDone.Load(), "bytes": bytes.Load(), "updatedAt": nowMS(), "set": *set,
		})
	}
	rep.mu.Lock()
	rep.extras = writeData
	rep.mu.Unlock()
	if gamesDone.Load() > 0 {
		event(*dir, "info", "Data generation (%s) resumed: %d games, %d positions already on disk", *set, gamesDone.Load(), positions.Load())
	} else {
		event(*dir, "info", "Data generation (%s) started: %d games, sampling %.0f%% of turn starts", *set, *games, 100**sample)
	}
	start := time.Now()
	startGames := gamesDone.Load()
	opts := hardai.Options{Budget: *budget, Workers: 1, Params: &tuned, Horizon: *expertHorizon}
	if *netPath != "" {
		net, err := valuenet.Load(*netPath)
		if err != nil {
			fatalf("data: %v", err)
		}
		opts.Net = net
	}
	for k := 0; k < shards; k++ {
		path := shardPath(outDir, k)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		n := min(*shard, *games-k**shard)
		recs := make([][]valuenet.Record, n)
		var next atomic.Int64
		var wg sync.WaitGroup
		for w := 0; w < *workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(next.Add(1) - 1)
					if i >= n {
						return
					}
					id := uint32(k**shard + i)
					recs[i] = playRecorded(id, *seed, &tuned, *sample, *maxTurns, *expert, opts)
					rep.addGames(1)
					d := gamesDone.Add(1)
					if d%100 == 0 {
						rate := float64(d-startGames) / time.Since(start).Seconds()
						rep.set(fmt.Sprintf("Generating positions · %s", *set),
							fmt.Sprintf("%d/%d games, %d positions", d, *games, positions.Load()),
							etaPtr(float64(int64(*games)-d)/rate))
					}
				}
			}()
		}
		wg.Wait()
		f, err := os.Create(path + ".tmp")
		if err != nil {
			fatalf("data: %v", err)
		}
		bw := bufio.NewWriterSize(f, 1<<20)
		buf := make([]byte, valuenet.RecordSize)
		count := 0
		for _, game := range recs {
			for _, r := range game {
				r.Encode(buf)
				bw.Write(buf)
				count++
			}
		}
		if err := bw.Flush(); err != nil {
			fatalf("data: %v", err)
		}
		f.Close()
		if err := os.Rename(path+".tmp", path); err != nil {
			fatalf("data: %v", err)
		}
		positions.Add(int64(count))
		bytes.Add(int64(count * valuenet.RecordSize))
		writeData()
		logf("shard %d: %d games, %d positions", k, n, count)
	}
	rep.set("Data generation finished", fmt.Sprintf("%d games, %d positions in %s", gamesDone.Load(), positions.Load(), *set), nil)
	event(*dir, "info", "Data generation (%s) finished: %d games, %d positions (%.0f MB)", *set, gamesDone.Load(), positions.Load(), float64(bytes.Load())/1e6)
}

func shardPath(dir string, k int) string { return filepath.Join(dir, fmt.Sprintf("shard-%05d.bin", k)) }

// dataRNG is an engine.RNG from math/rand/v2.
type dataRNG struct{ r *rand.Rand }

func (d dataRNG) Float64() float64 { return d.r.Float64() }

// playRecorded plays game id and returns its sampled positions.
func playRecorded(id uint32, seed uint64, tuned *ai.Params, sample float64, maxTurns int, expertShare float64, opts hardai.Options) []valuenet.Record {
	rng := rand.New(rand.NewPCG(seed, uint64(id)))
	dice := dataRNG{rand.New(rand.NewPCG(seed^0xd1ce, uint64(id)))}
	var n int
	switch u := rng.Float64(); {
	case u < 0.60:
		n = 4
	case u < 0.72:
		n = 3
	case u < 0.80:
		n = 2
	case u < 0.90:
		n = 5
	default:
		n = 6
	}
	var rules *engine.RulesUpdate
	on, off := true, false
	switch u := rng.Float64(); {
	case u < 0.75:
	case u < 0.87:
		rules = &engine.RulesUpdate{Loans: &on}
	case u < 0.95:
		rules = &engine.RulesUpdate{FreeParkingJackpot: &on}
	default:
		rules = &engine.RulesUpdate{Auctions: &off}
	}
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("p%d", i+1)
	}
	g, err := hardai.NewGameWithRules(names, rules, dice)
	if err != nil {
		logf("game %d: %v", id, err)
		return nil
	}
	d := &hardai.Driver{RNG: dice, TrimLog: true, MaxTurns: maxTurns}
	for i, p := range g.Players {
		var bot hardai.Bot
		switch u := rng.Float64(); {
		case expertShare > 0 && u < expertShare:
			bot = hardai.NewBot(hardai.Expert, int64(rng.Uint64()>>1), opts)
		case u < expertShare+(1-expertShare)*0.60:
			bot = hardai.NewTunedBot(tuned)
		case u < expertShare+(1-expertShare)*0.85:
			bot = hardai.NewHeuristicBot(false)
		default:
			v := normalise(tuned.Vector())
			for j := range v {
				v[j] = clamp01(v[j] + 0.06*rng.NormFloat64())
			}
			noisy := paramsOf(v)
			bot = hardai.NewTunedBot(&noisy)
		}
		_ = i
		d.Seats = append(d.Seats, hardai.Seat{PlayerID: p.ID, Bot: bot})
	}
	var out []valuenet.Record
	var lastTurn *engine.Turn
	turns, steps := 0, 0
	for steps < 200000 && g.Status == engine.StatusPlaying && turns < maxTurns {
		if g.Turn != lastTurn {
			lastTurn = g.Turn
			if g.Phase == engine.PhaseAwaitingRoll && g.Turn != nil {
				turns++
				if rng.Float64() < sample {
					out = append(out, valuenet.Record{Game: id, Winner: -1, Pos: valuenet.FromGame(g)})
				}
			}
		}
		if err := d.Step(g); err != nil {
			logf("game %d: %v", id, err)
			return nil
		}
		steps++
	}
	winner := -1
	if g.Status == engine.StatusFinished {
		for i, p := range g.Players {
			if p.ID == g.WinnerID {
				winner = i
			}
		}
	} else {
		best := -1
		for i, p := range g.Players {
			if w := engine.NetWorth(g, p.ID); !p.Bankrupt && w > best {
				best, winner = w, i
			}
		}
	}
	if winner < 0 {
		return nil
	}
	for i := range out {
		out[i].Winner = int8(winner)
	}
	return out
}

// loadRecords reads every shard of the given sets.
func loadRecords(dir string, sets []string) ([]valuenet.Record, error) {
	var paths []string
	for _, set := range sets {
		p, _ := filepath.Glob(filepath.Join(dir, "data", set, "shard-*.bin"))
		paths = append(paths, p...)
	}
	sort.Strings(paths)
	var total int64
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil {
			total += st.Size() / valuenet.RecordSize
		}
	}
	out := make([]valuenet.Record, 0, total)
	for si, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		for o := 0; o+valuenet.RecordSize <= len(data); o += valuenet.RecordSize {
			var r valuenet.Record
			if err := r.Decode(data[o : o+valuenet.RecordSize]); err != nil {
				return nil, fmt.Errorf("%s: %v", p, err)
			}
			// Games of different sets share ids; keep them apart.
			r.Game = r.Game ^ uint32(setHash(p))<<24
			out = append(out, r)
		}
		_ = si
	}
	return out, nil
}

func setHash(path string) uint8 {
	dir := filepath.Base(filepath.Dir(path))
	h := uint8(0)
	for i := 0; i < len(dir); i++ {
		h = h*31 + dir[i]
	}
	return h
}
