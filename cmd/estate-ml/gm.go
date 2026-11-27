package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/hardai"
	"github.com/tdar200/estate-games/internal/valuenet"
	"github.com/tdar200/estate-games/internal/valuenet2"
)

// gm: Grandmaster's value network (internal/valuenet2: 8 player slots,
// FeatureVersion 2) and its AlphaZero-style improvement loop.
//
// Each generation k has three stages, each resumable:
//
//  1. data: league games (data/gm<k>/shard-*.bin, valuenet2 records). Every
//     game draws its player count (2-8) and house rules, and each seat's
//     player from a league: Grandmaster with the current best network,
//     Expert, Hard, the tuned heuristic, Normal, the three caricature styles
//     Grandmaster's rollouts use, and Easy. At sampled turn starts the
//     position is recorded with the game's winner, and a share of them also
//     get a search-improved target (hardai.SearchValue: short rollouts scored
//     by the current best network).
//  2. train: a network trained on the converted Expert datasets (-v1-sets;
//     all of them in generation 1, a fraction later) and the league data of
//     the last -window generations; generation 1 starts from random weights,
//     later ones from the best network. The target mixes the winner with the
//     search target (-target-mix).
//  3. gate: Grandmaster with the new network against Grandmaster with the
//     best one (generation 1: with Expert's network), with two Expert seats,
//     seats rotated. The new network is accepted only if it wins clearly
//     more often (one-sided z >= -gate-z over the games either of them won).
//
// State: gm/state.json. Status for the dashboard: status/gm.json (stage,
// progress, history), status/run.json, status/events.jsonl, the ledger.

type gmConfig struct {
	Gens            int     `json:"gens"`
	Games           int     `json:"games"`
	Shard           int     `json:"shard"`
	Sample          float64 `json:"sample"`
	TargetShare     float64 `json:"targetShare"`
	TargetRollouts  int     `json:"targetRollouts"`
	TargetRounds    int     `json:"targetRounds"`
	V1Sets          string  `json:"v1Sets"`
	V1Fraction      float64 `json:"v1Fraction"`
	Window          int     `json:"window"`
	Epochs          int     `json:"epochs"`
	EpochsLater     int     `json:"epochsLater"`
	H1              int     `json:"h1"`
	H2              int     `json:"h2"`
	Batch           int     `json:"batch"`
	LR              float64 `json:"lr"`
	LRFinal         float64 `json:"lrFinal"`
	WD              float64 `json:"wd"`
	Dropout         float64 `json:"dropout"`
	TargetMix       float64 `json:"targetMix"`
	GateGames       int     `json:"gateGames"`
	GateZ           float64 `json:"gateZ"`
	GMRollouts      int     `json:"gmRollouts"`
	GMTradeRollouts int     `json:"gmTradeRollouts"`
	GateRollouts    int     `json:"gateRollouts"`
	Workers         int     `json:"workers"`
	Seed            uint64  `json:"seed"`
	MaxTurns        int     `json:"maxTurns"`
	ValEvery        int     `json:"valEvery"`
	CkptMinutes     int     `json:"ckptMinutes"`
}

type gmGen struct {
	Gen        int     `json:"gen"`
	Games      int64   `json:"games"`
	Positions  int64   `json:"positions"`
	Targets    int64   `json:"targets"`
	TrainN     int     `json:"trainPositions"`
	ValN       int     `json:"valPositions"`
	ValLoss    float64 `json:"valLoss"`
	ValAcc     float64 `json:"valAccuracy"`
	BestLoss   float64 `json:"bestValLoss,omitempty"` // the previous best network on the same validation set (0: none)
	GateGames  int     `json:"gateGames"`
	NewWins    int     `json:"newWins"`
	OldWins    int     `json:"oldWins"`
	Share      float64 `json:"share"` // new / (new + old)
	ShareLo    float64 `json:"shareLo"`
	ShareHi    float64 `json:"shareHi"`
	Z          float64 `json:"z"`
	Accepted   bool    `json:"accepted"`
	Net        string  `json:"net"`
	StartedAt  int64   `json:"startedAt"`
	FinishedAt int64   `json:"finishedAt"`
}

type gmState struct {
	Gen     int      `json:"gen"`
	Stage   string   `json:"stage"` // data, train, gate, done
	Best    string   `json:"best"`  // current best network ("" = Expert's, i.e. Grandmaster v1)
	Current gmGen    `json:"current"`
	History []gmGen  `json:"history"`
	Config  gmConfig `json:"config"`
}

func cmdGM(args []string) {
	fs := flag.NewFlagSet("gm", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "work directory")
	var c gmConfig
	fs.IntVar(&c.Gens, "gens", 4, "generations")
	fs.IntVar(&c.Games, "games", 8000, "league games per generation")
	fs.IntVar(&c.Shard, "shard", 400, "games per data shard")
	fs.Float64Var(&c.Sample, "sample", 0.3, "probability of recording a turn start")
	fs.Float64Var(&c.TargetShare, "target-share", 0.5, "share of recorded positions given a search target")
	fs.IntVar(&c.TargetRollouts, "target-rollouts", 32, "rollouts per search target")
	fs.IntVar(&c.TargetRounds, "target-rounds", 2, "rounds of play per search-target rollout")
	fs.StringVar(&c.V1Sets, "v1-sets", "tune,gen1,gen2", "Expert's datasets to convert and train on (comma-separated; missing ones are skipped)")
	fs.Float64Var(&c.V1Fraction, "v1-fraction", 0.25, "share of the Expert datasets kept after generation 1")
	fs.IntVar(&c.Window, "window", 3, "generations of league data trained on")
	fs.IntVar(&c.Epochs, "epochs", 3, "epochs, generation 1 (from random weights)")
	fs.IntVar(&c.EpochsLater, "epochs-later", 2, "epochs, later generations (from the best network)")
	fs.IntVar(&c.H1, "h1", 256, "first hidden layer")
	fs.IntVar(&c.H2, "h2", 128, "second hidden layer")
	fs.IntVar(&c.Batch, "batch", 1024, "minibatch")
	fs.Float64Var(&c.LR, "lr", 2e-3, "initial learning rate")
	fs.Float64Var(&c.LRFinal, "lr-final", 5e-5, "final learning rate")
	fs.Float64Var(&c.WD, "wd", 0.02, "decoupled weight decay")
	fs.Float64Var(&c.Dropout, "dropout", 0.5, "hidden-unit dropout")
	fs.Float64Var(&c.TargetMix, "target-mix", 0.5, "weight of the search target in the training target")
	fs.IntVar(&c.GateGames, "gate-games", 400, "gate games")
	fs.Float64Var(&c.GateZ, "gate-z", 1.0, "one-sided z the new network needs over the best one")
	fs.IntVar(&c.GMRollouts, "gm-rollouts", 120, "league Grandmaster: rollout rounds per decision (speed)")
	fs.IntVar(&c.GMTradeRollouts, "gm-trade-rollouts", 300, "league Grandmaster: rounds per trade answer")
	fs.IntVar(&c.GateRollouts, "gate-rollouts", 0, "gate Grandmasters: rollout rounds per decision (0 = production)")
	fs.IntVar(&c.Workers, "workers", 20, "games played at once / training goroutines")
	fs.Uint64Var(&c.Seed, "seed", 2026, "seed")
	fs.IntVar(&c.MaxTurns, "maxturns", 1000, "turn cap")
	fs.IntVar(&c.ValEvery, "val-every", 20, "one game in N is held out for validation")
	fs.IntVar(&c.CkptMinutes, "ckpt-minutes", 10, "training: checkpoint inside an epoch at least this often")
	fs.IntVar(&maxTempC, "max-temp", 90, "pause work while the CPU package is at or above this temperature in °C (0 = off)")
	fs.Parse(args)

	statePath := filepath.Join(*dir, "gm", "state.json")
	var st gmState
	if readJSON(statePath, &st) != nil || st.Gen == 0 {
		st = gmState{Gen: 1, Stage: "data"}
		event(*dir, "info", "Grandmaster loop started: %d generations of %d league games", c.Gens, c.Games)
	} else {
		event(*dir, "info", "Grandmaster loop resumed: generation %d, stage %s", st.Gen, st.Stage)
	}
	st.Config = c
	if st.Stage == "done" && st.Gen <= c.Gens {
		st.Stage = "data" // started again with more generations
	}
	save := func() {
		if err := writeJSONAtomic(statePath, st); err != nil {
			fatalf("gm: %v", err)
		}
		writeGMStatus(*dir, &st, "", 0, nil)
	}
	save()
	for st.Gen <= c.Gens {
		if st.Current.Gen != st.Gen {
			st.Current = gmGen{Gen: st.Gen, StartedAt: nowMS()}
		}
		best, err := loadBest(*dir, st.Best)
		if err != nil {
			fatalf("gm: best network %s: %v", st.Best, err)
		}
		switch st.Stage {
		case "data":
			gmData(*dir, &st, best)
			st.Stage = "train"
		case "train":
			gmTrainStage(*dir, &st, best)
			st.Stage = "gate"
		case "gate":
			cand, err := valuenet2.Load(filepath.Join(*dir, "net", fmt.Sprintf("gm%d", st.Gen), "best.bin"))
			if err != nil {
				fatalf("gm: candidate: %v", err)
			}
			gmGate(*dir, &st, cand, best)
			st.Current.FinishedAt = nowMS()
			st.Current.Net = fmt.Sprintf("net/gm%d/best.bin", st.Gen)
			if st.Current.Accepted {
				st.Best = st.Current.Net
				event(*dir, "info", "Grandmaster generation %d accepted: new network wins %.1f%% of the two Grandmasters' wins (z = %.2f)",
					st.Gen, 100*st.Current.Share, st.Current.Z)
			} else {
				event(*dir, "warn", "Grandmaster generation %d rejected: new network wins %.1f%% of the two Grandmasters' wins (z = %.2f); keeping %s",
					st.Gen, 100*st.Current.Share, st.Current.Z, bestLabel(st.Best))
			}
			st.History = append(st.History, st.Current)
			_ = appendJSONL(filepath.Join(*dir, "status", "gm.jsonl"), st.Current)
			st.Gen++
			st.Stage = "data"
		default:
			fatalf("gm: unknown stage %q", st.Stage)
		}
		save()
	}
	st.Stage = "done"
	save()
	event(*dir, "info", "Grandmaster loop finished; best network: %s", bestLabel(st.Best))
}

func bestLabel(path string) string {
	if path == "" {
		return "Expert's network (none of the new ones passed)"
	}
	return path
}

// loadBest loads the best network (nil = Grandmaster on Expert's network).
func loadBest(dir, rel string) (*valuenet2.Net, error) {
	if rel == "" {
		return nil, nil
	}
	path := rel
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, rel)
	}
	return valuenet2.Load(path)
}

// writeGMStatus writes status/gm.json for the dashboard.
// gmStatusMu serialises status writes and updates of the current generation.
var gmStatusMu sync.Mutex

func writeGMStatus(dir string, st *gmState, label string, progress float64, eta *int64) {
	activity := label
	if activity == "" {
		activity = fmt.Sprintf("Grandmaster network, generation %d of %d: %s", st.Gen, st.Config.Gens, st.Stage)
	}
	setGMProgress(dir, "Phase 2: Grandmaster network self-play loop", activity, eta)
	_ = writeJSONAtomic(filepath.Join(dir, "status", "gm.json"), map[string]any{
		"gen": st.Gen, "gens": st.Config.Gens, "stage": st.Stage, "label": label, "progress": progress, "etaSec": eta,
		"best": bestLabel(st.Best), "current": st.Current, "history": st.History, "updatedAt": nowMS(),
		"config": st.Config,
	})
}

// ------------------------------------------------------------------- data

func gmData(dir string, st *gmState, best *valuenet2.Net) {
	c := st.Config
	set := fmt.Sprintf("gm%d", st.Gen)
	outDir := filepath.Join(dir, "data", set)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("gm: %v", err)
	}
	rep := newReporter(dir, "gm", "gm-data-"+set)
	defer rep.close()
	shards := (c.Games + c.Shard - 1) / c.Shard
	var games, positions, targets atomic.Int64
	for k := 0; k < shards; k++ {
		if data, err := os.ReadFile(gmShardPath(outDir, k)); err == nil {
			games.Add(int64(min(c.Shard, c.Games-k*c.Shard)))
			for o := 0; o+valuenet2.RecordSize <= len(data); o += valuenet2.RecordSize {
				positions.Add(1)
				if data[o+12]&valuenet2.FlagTarget != 0 {
					targets.Add(1)
				}
			}
		}
	}
	start := time.Now()
	startGames := games.Load()
	v1, _ := valuenet.Default()
	for k := 0; k < shards; k++ {
		path := gmShardPath(outDir, k)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		n := min(c.Shard, c.Games-k*c.Shard)
		recs := make([][]valuenet2.Record, n)
		var next atomic.Int64
		var wg sync.WaitGroup
		for w := 0; w < c.Workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(next.Add(1) - 1)
					if i >= n {
						return
					}
					thermalWait(dir)
					id := uint32(k*c.Shard + i)
					var nt int64
					recs[i], nt = playLeague(id, uint64(st.Gen)*1_000_003+c.Seed, c, best, v1)
					targets.Add(nt)
					rep.addGames(1)
					d := games.Add(1)
					rate := float64(d-startGames) / time.Since(start).Seconds()
					eta := etaPtr(float64(int64(c.Games)-d) / rate)
					label := fmt.Sprintf("Grandmaster generation %d/%d · league games", st.Gen, c.Gens)
					msg := fmt.Sprintf("%d/%d games, %d positions", d, c.Games, positions.Load())
					rep.set(label, msg, eta)
					if d%10 == 0 {
						gmStatusMu.Lock()
						st.Current.Games, st.Current.Positions, st.Current.Targets = d, positions.Load(), targets.Load()
						writeGMStatus(dir, st, label+": "+msg, float64(d)/float64(c.Games), eta)
						gmStatusMu.Unlock()
					}
				}
			}()
		}
		wg.Wait()
		count, err := writeV2Shard(path, recs)
		if err != nil {
			fatalf("gm: %v", err)
		}
		positions.Add(int64(count))
		logf("gm %s shard %d: %d games, %d positions", set, k, n, count)
	}
	st.Current.Games, st.Current.Positions, st.Current.Targets = games.Load(), positions.Load(), targets.Load()
	event(dir, "info", "Grandmaster generation %d: %d league games, %d positions (%d with search targets)", st.Gen,
		games.Load(), positions.Load(), targets.Load())
}

func gmShardPath(dir string, k int) string {
	return filepath.Join(dir, fmt.Sprintf("shard-%05d.bin", k))
}

func writeV2Shard(path string, recs [][]valuenet2.Record) (int, error) {
	f, err := os.Create(path + ".tmp")
	if err != nil {
		return 0, err
	}
	bw := bufio.NewWriterSize(f, 1<<20)
	buf := make([]byte, valuenet2.RecordSize)
	count := 0
	for _, game := range recs {
		for i := range game {
			game[i].Encode(buf)
			if _, err := bw.Write(buf); err != nil {
				f.Close()
				return 0, err
			}
			count++
		}
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	return count, os.Rename(path+".tmp", path)
}

// League seats and their weights.
var leagueSeats = []struct {
	name   string
	weight float64
}{
	{"grandmaster", 0.30}, {"expert", 0.20}, {"hard", 0.08}, {"tuned", 0.12}, {"normal", 0.12}, {"style", 0.12}, {"easy", 0.06},
}

var leagueCounts = []struct {
	n      int
	weight float64
}{{2, 0.08}, {3, 0.13}, {4, 0.32}, {5, 0.14}, {6, 0.13}, {7, 0.10}, {8, 0.10}}

// playLeague plays league game id and returns its recorded positions and
// how many of them have a search target.
func playLeague(id uint32, seed uint64, c gmConfig, best *valuenet2.Net, v1 *valuenet.Net) ([]valuenet2.Record, int64) {
	rng := rand.New(rand.NewPCG(seed, uint64(id)))
	dice := dataRNG{rand.New(rand.NewPCG(seed^0xd1ce, uint64(id)))}
	u := rng.Float64()
	n := 4
	for _, pc := range leagueCounts {
		if u < pc.weight {
			n = pc.n
			break
		}
		u -= pc.weight
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
		logf("league game %d: %v", id, err)
		return nil, 0
	}
	styles := hardai.OpponentStyles()
	d := &hardai.Driver{RNG: dice, TrimLog: true, MaxTurns: c.MaxTurns}
	for _, p := range g.Players {
		bseed := int64(rng.Uint64() >> 1)
		u := rng.Float64()
		kind := leagueSeats[len(leagueSeats)-1].name
		for _, s := range leagueSeats {
			if u < s.weight {
				kind = s.name
				break
			}
			u -= s.weight
		}
		var bot hardai.Bot
		opts := hardai.Options{Budget: -1, Workers: 1}
		switch kind {
		case "grandmaster":
			opts.Rollouts, opts.Net2 = c.GMRollouts, best
			opts.GM = &hardai.GrandmasterOptions{TradeRollouts: c.GMTradeRollouts, ExpertNet: best == nil}
			bot = hardai.NewGrandmasterBot(bseed, opts)
		case "expert":
			bot = hardai.NewBot(hardai.Expert, bseed, opts)
		case "hard":
			bot = hardai.NewBot(hardai.Hard, bseed, opts)
		case "tuned":
			bot = hardai.NewTunedBot(hardai.ExpertParams())
		case "normal":
			bot = hardai.NewHeuristicBot(false)
		case "style":
			bot = hardai.NewTunedBot(styles[2+rng.IntN(3)].Params)
		default:
			bot = hardai.NewBot(hardai.Easy, bseed, opts)
		}
		d.Seats = append(d.Seats, hardai.Seat{PlayerID: p.ID, Bot: bot})
	}
	var out []valuenet2.Record
	var withTarget int64
	var lastTurn *engine.Turn
	turns, steps := 0, 0
	for steps < 200000 && g.Status == engine.StatusPlaying && turns < c.MaxTurns {
		if g.Turn != lastTurn {
			lastTurn = g.Turn
			if g.Phase == engine.PhaseAwaitingRoll && g.Turn != nil {
				turns++
				if rng.Float64() < c.Sample {
					r := valuenet2.Record{Game: id, Winner: -1, Pos: valuenet2.FromGame(g)}
					if rng.Float64() < c.TargetShare {
						probs := hardai.SearchValue(g, hardai.SearchValueOptions{Rollouts: c.TargetRollouts, Rounds: c.TargetRounds,
							Net: v1, Net2: best, Seed: rng.Uint64()})
						r.SetTarget(probs)
						withTarget++
					}
					out = append(out, r)
				}
			}
		}
		if err := d.Step(g); err != nil {
			logf("league game %d: %v", id, err)
			return nil, 0
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
		bestWorth := -1
		for i, p := range g.Players {
			if w := engine.NetWorth(g, p.ID); !p.Bankrupt && w > bestWorth {
				bestWorth, winner = w, i
			}
		}
	}
	if winner < 0 {
		return nil, 0
	}
	for i := range out {
		out[i].Winner = int8(winner)
	}
	return out, withTarget
}

// ------------------------------------------------------------------ train

// loadV2Records reads the league sets (valuenet2 shards) and Expert's sets
// (valuenet shards, converted), keeping a v1Keep share of the latter's games.
func loadV2Records(dir string, v2Sets, v1Sets []string, v1Keep float64) ([]valuenet2.Record, int, error) {
	var out []valuenet2.Record
	for _, set := range v2Sets {
		paths, _ := filepath.Glob(filepath.Join(dir, "data", set, "shard-*.bin"))
		sort.Strings(paths)
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, 0, err
			}
			tag := uint32(setHash(p)) << 24
			for o := 0; o+valuenet2.RecordSize <= len(data); o += valuenet2.RecordSize {
				var r valuenet2.Record
				if err := r.Decode(data[o : o+valuenet2.RecordSize]); err != nil {
					return nil, 0, fmt.Errorf("%s: %v", p, err)
				}
				r.Game ^= tag
				out = append(out, r)
			}
		}
	}
	v1n := 0
	if len(v1Sets) > 0 && v1Keep > 0 {
		recs, err := loadRecords(dir, v1Sets)
		if err != nil {
			return nil, 0, err
		}
		for i := range recs {
			if v1Keep < 1 && float64(mixSeed(uint64(recs[i].Game), 0x71)%1000) >= v1Keep*1000 {
				continue
			}
			out = append(out, valuenet2.FromV1(&recs[i]))
			v1n++
		}
	}
	return out, v1n, nil
}

func gmTrainStage(dir string, st *gmState, best *valuenet2.Net) {
	c := st.Config
	out := fmt.Sprintf("gm%d", st.Gen)
	outDir := filepath.Join(dir, "net", out)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("gm: %v", err)
	}
	rep := newReporter(dir, "gm", "gm-train-"+out)
	defer rep.close()
	label := fmt.Sprintf("Grandmaster generation %d/%d · training the network", st.Gen, c.Gens)
	rep.set(label, "loading positions", nil)
	writeGMStatus(dir, st, label+": loading positions", 0, nil)
	var v2Sets []string
	for g := max(1, st.Gen-c.Window+1); g <= st.Gen; g++ {
		v2Sets = append(v2Sets, fmt.Sprintf("gm%d", g))
	}
	var v1Sets []string
	for _, s := range strings.Split(c.V1Sets, ",") {
		if s = strings.TrimSpace(s); s != "" {
			if paths, _ := filepath.Glob(filepath.Join(dir, "data", s, "shard-*.bin")); len(paths) > 0 {
				v1Sets = append(v1Sets, s)
			}
		}
	}
	keep := 1.0
	if st.Gen > 1 {
		keep = c.V1Fraction
	}
	all, v1n, err := loadV2Records(dir, v2Sets, v1Sets, keep)
	if err != nil {
		fatalf("gm: %v", err)
	}
	var trainSet, valSet []valuenet2.Record
	for _, r := range all {
		if mixSeed(uint64(r.Game), 0xa1)%uint64(c.ValEvery) == 0 {
			valSet = append(valSet, r)
		} else {
			trainSet = append(trainSet, r)
		}
	}
	all = nil
	logf("gm train %s: %d training positions (%d converted from %v), %d validation", out, len(trainSet), v1n, v1Sets, len(valSet))
	if len(trainSet) == 0 || len(valSet) == 0 {
		fatalf("gm: no training data")
	}
	epochs := c.Epochs
	if best != nil {
		epochs = c.EpochsLater
	}
	meta := gmTrainMeta{} // BestLoss 0: none yet
	ckpt := filepath.Join(outDir, "ckpt.bin")
	var net *valuenet2.Net
	resumed := false
	if readJSON(filepath.Join(outDir, "epoch.json"), &meta) == nil && (meta.Epoch > 0 || meta.Done > 0) {
		if net, err = valuenet2.Load(ckpt); err != nil {
			fatalf("gm: checkpoint: %v", err)
		}
		resumed = true
	} else if best != nil {
		net = best.Clone()
	} else {
		net = valuenet2.NewNet(valuenet2.NumFeatures, c.H1, c.H2, c.Seed+uint64(st.Gen))
	}
	tr := valuenet2.NewTrainer(net, valuenet2.TrainConfig{Batch: c.Batch, LR: c.LR, WeightDecay: c.WD, Workers: c.Workers,
		Dropout: c.Dropout, TargetMix: c.TargetMix})
	if resumed {
		if err := tr.LoadState(filepath.Join(outDir, "adam.bin")); err != nil {
			fatalf("gm: optimiser state: %v", err)
		}
		event(dir, "info", "Grandmaster generation %d training resumed at epoch %d, %d positions in", st.Gen, meta.Epoch+1, meta.Done)
	}
	if best != nil {
		bm := valuenet2.Evaluate(best, valSet, c.Workers)
		st.Current.BestLoss = bm.LogLoss
	}
	st.Current.TrainN, st.Current.ValN = len(trainSet), len(valSet)
	lr0 := c.LR
	if best != nil {
		lr0 = c.LR / 2 // fine-tuning
	}
	// checkpoint saves the network, the optimiser and the place in the epoch.
	checkpoint := func() {
		if err := net.Save(ckpt); err != nil {
			fatalf("gm: %v", err)
		}
		if err := tr.SaveState(filepath.Join(outDir, "adam.bin")); err != nil {
			fatalf("gm: %v", err)
		}
		if err := writeJSONAtomic(filepath.Join(outDir, "epoch.json"), meta); err != nil {
			fatalf("gm: %v", err)
		}
	}
	for epoch := meta.Epoch; epoch < epochs; epoch++ {
		rate := c.LRFinal + 0.5*(lr0-c.LRFinal)*(1+math.Cos(math.Pi*float64(epoch)/float64(epochs)))
		start := time.Now()
		elabel := fmt.Sprintf("%s, epoch %d/%d", label, epoch+1, epochs)
		// The epoch's order depends only on the epoch, so a resumed epoch
		// skips exactly the positions already trained on.
		perm := valuenet2.Shuffle(len(trainSet), rand.New(rand.NewPCG(c.Seed+uint64(st.Gen), uint64(epoch))))
		skip := min(meta.Done, len(perm))
		last, lastCkpt := time.Now(), time.Now()
		trainLoss := tr.TrainEpoch(trainSet, perm[skip:], rate, func(done int) {
			thermalWait(dir)
			abs := skip + done
			if time.Since(lastCkpt) > time.Duration(c.CkptMinutes)*time.Minute {
				lastCkpt = time.Now()
				meta.Done = abs
				checkpoint()
			}
			if time.Since(last) > 5*time.Second {
				last = time.Now()
				frac := float64(abs) / float64(len(trainSet))
				eta := etaPtr(time.Since(start).Seconds() / max(1e-9, float64(done)/float64(len(trainSet))) * (1 - frac + float64(epochs-epoch-1)))
				msg := fmt.Sprintf("%.0f%% of epoch %d", 100*frac, epoch+1)
				rep.set(elabel, msg, eta)
				writeGMStatus(dir, st, elabel+": "+msg, (float64(epoch)+frac)/float64(epochs), eta)
			}
		})
		m := valuenet2.Evaluate(net, valSet, c.Workers)
		logf("gm %s epoch %d: train loss %.4f, val loss %.4f (uniform %.4f), accuracy %.4f, brier %.4f (%s)",
			out, epoch+1, trainLoss, m.LogLoss, m.Uniform, m.Accuracy, m.Brier, time.Since(start).Round(time.Second))
		_ = appendJSONL(filepath.Join(dir, "status", "train.jsonl"), map[string]any{
			"epoch": epoch + 1, "trainLoss": round4(trainLoss), "valLoss": round4(m.LogLoss), "valAccuracy": round4(m.Accuracy),
			"brier": round4(m.Brier), "lr": rate, "at": nowMS(), "net": out, "uniformLoss": round4(m.Uniform),
		})
		if meta.BestLoss == 0 || m.LogLoss < meta.BestLoss {
			meta.BestLoss, meta.BestEp = m.LogLoss, epoch+1
			if err := net.Save(filepath.Join(outDir, "best.bin")); err != nil {
				fatalf("gm: %v", err)
			}
			_ = writeJSONAtomic(filepath.Join(outDir, "metrics.json"), map[string]any{"epoch": epoch + 1, "validation": m,
				"trainPositions": len(trainSet), "valPositions": len(valSet), "v2Sets": v2Sets, "v1Sets": v1Sets})
			st.Current.ValLoss, st.Current.ValAcc = m.LogLoss, m.Accuracy
		}
		meta.Epoch, meta.Done = epoch+1, 0
		checkpoint()
	}
	if st.Current.ValLoss == 0 {
		// Resumed after the last epoch: read the kept metrics.
		var mf struct {
			Validation valuenet2.Metrics `json:"validation"`
		}
		if readJSON(filepath.Join(outDir, "metrics.json"), &mf) == nil {
			st.Current.ValLoss, st.Current.ValAcc = mf.Validation.LogLoss, mf.Validation.Accuracy
		}
	}
	prev := "no previous network"
	if st.Current.BestLoss > 0 {
		prev = fmt.Sprintf("previous best %.3f", st.Current.BestLoss)
	}
	event(dir, "info", "Grandmaster generation %d network: validation log loss %.3f (%s), winner picked %.1f%%",
		st.Gen, st.Current.ValLoss, prev, 100*st.Current.ValAcc)
}

// ------------------------------------------------------------------- gate

// gmGateGame is one gate game's result (persisted, so the gate resumes).
type gmGateGame struct {
	Index   int  `json:"i"`
	NewWon  bool `json:"new"`
	OldWon  bool `json:"old"`
	Turns   int  `json:"turns"`
	Errored bool `json:"err,omitempty"`
}

func gmGate(dir string, st *gmState, cand, best *valuenet2.Net) {
	c := st.Config
	path := filepath.Join(dir, "gm", fmt.Sprintf("gate-%d.jsonl", st.Gen))
	done := map[int]gmGateGame{}
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			var r gmGateGame
			if json.Unmarshal([]byte(line), &r) == nil {
				done[r.Index] = r
			}
		}
	}
	rep := newReporter(dir, "gm", fmt.Sprintf("gm-gate-%d", st.Gen))
	defer rep.close()
	label := fmt.Sprintf("Grandmaster generation %d/%d · gate (new network against the best)", st.Gen, c.Gens)
	var mu sync.Mutex
	var next atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	startDone := len(done)
	for w := 0; w < c.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= c.GateGames {
					return
				}
				mu.Lock()
				_, ok := done[i]
				mu.Unlock()
				if ok {
					continue
				}
				thermalWait(dir)
				r := gateGame(i, c, cand, best)
				mu.Lock()
				done[i] = r
				_ = appendJSONL(path, r)
				nw, ow := gateTally(done)
				n := len(done)
				rep.addGames(1)
				rate := float64(n-startDone) / time.Since(start).Seconds()
				eta := etaPtr(float64(c.GateGames-n) / rate)
				msg := fmt.Sprintf("%d/%d games: new network won %d, best network won %d", n, c.GateGames, nw, ow)
				rep.set(label, msg, eta)
				writeGMStatus(dir, st, label+": "+msg, float64(n)/float64(c.GateGames), eta)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	nw, ow := gateTally(done)
	st.Current.GateGames, st.Current.NewWins, st.Current.OldWins = len(done), nw, ow
	st.Current.Share, st.Current.ShareLo, st.Current.ShareHi = wilsonShare(nw, nw+ow)
	if nw+ow > 0 {
		st.Current.Z = float64(nw-ow) / math.Sqrt(float64(nw+ow))
	}
	st.Current.Accepted = nw > ow && st.Current.Z >= c.GateZ
}

func gateTally(done map[int]gmGateGame) (nw, ow int) {
	for _, r := range done {
		if r.NewWon {
			nw++
		}
		if r.OldWon {
			ow++
		}
	}
	return nw, ow
}

// gateGame plays gate game i: four seats, Grandmaster with the new network,
// Grandmaster with the best one, two Experts, seats rotated.
func gateGame(i int, c gmConfig, cand, best *valuenet2.Net) gmGateGame {
	seats := []string{"new", "old", "expert", "expert"}
	rot := i % 4
	levels := make([]hardai.Level, 4)
	kinds := make([]string, 4)
	for s := range levels {
		kinds[s] = seats[(s+rot)%4]
		levels[s] = hardai.Grandmaster
		if kinds[s] == "expert" {
			levels[s] = hardai.Expert
		}
	}
	res, err := hardai.PlayMatch(hardai.MatchConfig{Levels: levels, Seed: int64(c.Seed)*7919 + int64(i), MaxTurns: c.MaxTurns,
		NewBot: func(seat int, level hardai.Level, seed int64) hardai.Bot {
			opts := hardai.Options{Budget: -1, Workers: 1}
			switch kinds[seat] {
			case "new":
				opts.Net2, opts.Rollouts = cand, c.GateRollouts
				return hardai.NewGrandmasterBot(seed, opts)
			case "old":
				opts.Net2, opts.Rollouts = best, c.GateRollouts
				if best == nil {
					opts.GM = &hardai.GrandmasterOptions{ExpertNet: true}
				}
				return hardai.NewGrandmasterBot(seed, opts)
			}
			return hardai.NewBot(level, seed, opts)
		}})
	if err != nil {
		logf("gate game %d: %v", i, err)
		return gmGateGame{Index: i, Errored: true}
	}
	r := gmGateGame{Index: i, Turns: res.Turns}
	for s, seat := range res.Seats {
		if seat.Won {
			r.NewWon = kinds[s] == "new"
			r.OldWon = kinds[s] == "old"
		}
	}
	return r
}

func wilsonShare(k, n int) (p, lo, hi float64) {
	if n == 0 {
		return 0, 0, 0
	}
	const z = 1.959964
	p = float64(k) / float64(n)
	nf := float64(n)
	denom := 1 + z*z/nf
	center := (p + z*z/(2*nf)) / denom
	half := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / denom
	return p, math.Max(0, center-half), math.Min(1, center+half)
}

// gmTrainMeta is net/gm<k>/epoch.json: epochs finished, positions of the
// current epoch already trained on (a checkpoint inside an epoch), and the
// best validation loss so far.
type gmTrainMeta struct {
	Epoch    int     `json:"epoch"`
	Done     int     `json:"done"`
	BestLoss float64 `json:"bestLoss"`
	BestEp   int     `json:"bestEpoch"`
}
