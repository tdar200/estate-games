package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/hardai"
)

// tune: separable CMA-ES over the heuristic's knobs (ai.Knobs), by self-play.
//
// Every generation samples -pop candidates. Each plays -seeds deals in each
// of the four seats against a field of three baseline bots: the same seeds
// for every candidate of the generation (common random numbers) and every
// seat equally often. Fitness is the win rate. Up to -switch generations the
// field is three Normal bots; after that it is one Normal bot and two copies
// of the champion (the distribution mean at the switch), so the strategy
// keeps improving against itself rather than only exploiting Normal. Every
// -validate generations the mean is measured against 3 x Normal with more
// games; the best validated mean is the result (tune/best.json).

type tuneConfig struct {
	Gens          int     `json:"gens"`
	Pop           int     `json:"pop"`
	Seeds         int     `json:"seeds"` // deals per candidate (x4 seats = games)
	Workers       int     `json:"workers"`
	Sigma         float64 `json:"sigma"`
	RunSeed       uint64  `json:"runSeed"`
	Switch        int     `json:"switch"`
	Validate      int     `json:"validate"`
	ValidateSeeds int     `json:"validateSeeds"`
	MaxTurns      int     `json:"maxTurns"`
}

type validation struct {
	Gen     int       `json:"gen"`
	X       []float64 `json:"x"`
	WinRate float64   `json:"winRate"`
	Games   int       `json:"games"`
}

type tuneState struct {
	Config        tuneConfig   `json:"config"`
	CMA           *sepCMA      `json:"cma"`
	Champion      []float64    `json:"champion,omitempty"` // field for stage 2 (normalised)
	BestEver      float64      `json:"bestEver"`
	BestEverX     []float64    `json:"bestEverX"`
	Validations   []validation `json:"validations"`
	BestValidated *validation  `json:"bestValidated,omitempty"`
	Stage2        *stage2Best  `json:"stage2,omitempty"` // selection once the champion is in the field
	StartedAt     int64        `json:"startedAt"`
}

// stage2Best is the selected strategy of stage 2: the best average of its
// win rates against 3 x Normal (VN) and against Normal + 2 x champion (VC),
// on the fixed validation deals.
type stage2Best struct {
	Gen   int       `json:"gen"`
	X     []float64 `json:"x"`
	VN    float64   `json:"vn"`
	VC    float64   `json:"vc"`
	Score float64   `json:"score"`
}

func cmdTune(args []string) {
	fs := flag.NewFlagSet("tune", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "work directory")
	var cfg tuneConfig
	fs.IntVar(&cfg.Gens, "gens", 80, "generations")
	fs.IntVar(&cfg.Pop, "pop", 24, "candidates per generation")
	fs.IntVar(&cfg.Seeds, "seeds", 100, "deals per candidate (each played in all 4 seats)")
	fs.IntVar(&cfg.Workers, "workers", 24, "games played at once")
	fs.Float64Var(&cfg.Sigma, "sigma", 0.12, "initial step size (fraction of each knob's range)")
	fs.Uint64Var(&cfg.RunSeed, "seed", 2026, "run seed")
	fs.IntVar(&cfg.Switch, "switch", 45, "generation from which the field includes the champion (0 = never)")
	fs.IntVar(&cfg.Validate, "validate", 5, "validate the mean every N generations")
	fs.IntVar(&cfg.ValidateSeeds, "validate-seeds", 500, "deals per validation (x4 games)")
	fs.IntVar(&cfg.MaxTurns, "maxturns", 1000, "turn cap per game")
	record := fs.Float64("record", 0.1, "share of turn starts recorded as value-network data (data/tune/)")
	fs.Parse(args)

	statePath := filepath.Join(*dir, "tune", "state.json")
	var st tuneState
	resumed := false
	if data, err := os.ReadFile(statePath); err == nil {
		if err := json.Unmarshal(data, &st); err != nil {
			fatalf("tune: bad checkpoint %s: %v", statePath, err)
		}
		st.CMA.init()
		// Keep the original run's search settings; allow more generations or workers.
		gens, workers := cfg.Gens, cfg.Workers
		cfg = st.Config
		cfg.Gens, cfg.Workers = max(gens, cfg.Gens), workers
		st.Config = cfg
		resumed = true
	} else {
		st = tuneState{Config: cfg, CMA: newSepCMA(normalise(ai.DefaultParams.Vector()), cfg.Sigma, cfg.Pop), StartedAt: nowMS()}
	}
	rep := newReporter(*dir, "tune", "tune")
	defer rep.close()
	if resumed {
		event(*dir, "info", "Tuning resumed from checkpoint at generation %d", st.CMA.Gen)
	} else {
		event(*dir, "info", "Tuning started: %d knobs, population %d, %d games per candidate, %d generations",
			len(ai.Knobs), cfg.Pop, 4*cfg.Seeds, cfg.Gens)
	}
	writeParamsStatus(*dir, bestX(&st))
	normal := ai.DefaultParams
	genStart := time.Now()
	resumeAt, resumeGen := time.Now(), st.CMA.Gen
	for st.CMA.Gen < cfg.Gens {
		gen := st.CMA.Gen
		if cfg.Switch > 0 && gen >= cfg.Switch && st.Champion == nil {
			st.Champion = bestX(&st)
			event(*dir, "info", "Generation %d: the field now includes the champion (best validated strategy so far)", gen)
			if st.BestValidated != nil {
				champ := paramsOf(st.Champion)
				rep.set(fmt.Sprintf("Validating champion · gen %d/%d", gen, cfg.Gens), "Measuring the champion against Normal + 2 x champion", nil)
				w, n := playCandidates([]ai.Params{champ}, [3]*ai.Params{&normal, &champ, &champ}, validationSeeds(cfg), cfg.Workers, cfg.MaxTurns, nil, func(int) { rep.addGames(1) })
				vc := float64(w[0]) / float64(n[0])
				st.Stage2 = &stage2Best{Gen: st.BestValidated.Gen, X: st.Champion, VN: st.BestValidated.WinRate, VC: vc, Score: (st.BestValidated.WinRate + vc) / 2}
				event(*dir, "info", "Champion (gen %d): %.1f%% vs 3 x Normal, %.1f%% vs Normal + 2 x champion", st.Stage2.Gen, 100*st.Stage2.VN, 100*vc)
				if err := writeJSONAtomic(statePath, &st); err != nil {
					fatalf("tune: checkpoint: %v", err)
				}
			}
		}
		field := [3]*ai.Params{&normal, &normal, &normal}
		fieldName := "3 x Normal"
		if st.Champion != nil {
			champ := paramsOf(st.Champion)
			field = [3]*ai.Params{&normal, &champ, &champ}
			fieldName = "Normal + 2 x champion"
		}
		rng := rand.New(rand.NewPCG(cfg.RunSeed, uint64(gen)))
		xs := st.CMA.sample(rng)
		cands := make([]ai.Params, len(xs))
		for i, x := range xs {
			cands[i] = paramsOf(x)
		}
		seeds := make([]int64, cfg.Seeds)
		base := int64(mixSeed(cfg.RunSeed, uint64(gen), 77) >> 2)
		for i := range seeds {
			seeds[i] = base + int64(i)
		}
		total := len(cands) * len(seeds) * 4
		label := fmt.Sprintf("Tuning strategy · gen %d/%d", gen+1, cfg.Gens)
		genStart = time.Now()
		var perGen time.Duration // average generation (with validations) since this run started
		if gen > resumeGen {
			perGen = time.Since(resumeAt) / time.Duration(gen-resumeGen)
		}
		eta := func(doneFrac float64) *int64 {
			if perGen <= 0 {
				return nil
			}
			return etaPtr(perGen.Seconds() * (float64(cfg.Gens-gen) - doneFrac))
		}
		rep.set(label, fmt.Sprintf("Evaluating %d candidates against %s (%d games)", len(cands), fieldName, total), eta(0))
		rec := &recorder{gen: gen, sample: *record}
		wins, games := playCandidates(cands, field, seeds, cfg.Workers, cfg.MaxTurns, rec, func(done int) {
			rep.addGames(1)
			if done%200 == 0 {
				rep.set(label, fmt.Sprintf("Evaluating %d candidates against %s: %d/%d games", len(cands), fieldName, done, total), eta(float64(done)/float64(total)))
			}
		})
		if n, err := rec.flush(tuneShardPath(*dir, gen, "g")); err != nil {
			logf("tune: recording positions: %v", err)
		} else {
			logf("gen %d: recorded %d positions", gen+1, n)
		}
		fit := make([]float64, len(cands))
		best, sum := 0, 0.0
		for i := range fit {
			fit[i] = float64(wins[i]) / float64(max(1, games[i]))
			sum += fit[i]
			if fit[i] > fit[best] {
				best = i
			}
		}
		mean := sum / float64(len(fit))
		st.CMA.update(xs, fit)
		if fit[best] > st.BestEver {
			if st.BestEver > 0 {
				event(*dir, "info", "Generation %d: new best candidate %.1f%% (+%.1f pts)", gen+1, 100*fit[best], 100*(fit[best]-st.BestEver))
			}
			st.BestEver = fit[best]
			st.BestEverX = append([]float64(nil), xs[best]...)
		}
		_ = appendJSONL(filepath.Join(*dir, "status", "tune.jsonl"), map[string]any{
			"gen": gen + 1, "bestFitness": fit[best], "meanFitness": mean, "bestEverFitness": st.BestEver,
			"baseline": 0.25, "sigma": st.CMA.Sigma, "games": total, "at": nowMS(), "field": fieldName,
		})
		logf("gen %d: best %.4f mean %.4f sigma %.4f (%s, %s)", gen+1, fit[best], mean, st.CMA.Sigma, fieldName, time.Since(genStart).Round(time.Second))
		if cfg.Validate > 0 && (st.CMA.Gen%cfg.Validate == 0 || st.CMA.Gen == cfg.Gens) {
			x := append([]float64(nil), st.CMA.Mean...)
			p := paramsOf(x)
			vseeds := validationSeeds(cfg)
			vtotal := len(vseeds) * 4
			rep.set(fmt.Sprintf("Validating mean · gen %d/%d", st.CMA.Gen, cfg.Gens), fmt.Sprintf("Measuring the current strategy against 3 x Normal (%d games)", vtotal), nil)
			vrec := &recorder{gen: st.CMA.Gen, sample: *record, candOffset: 63}
			w, n := playCandidates([]ai.Params{p}, [3]*ai.Params{&normal, &normal, &normal}, vseeds, cfg.Workers, cfg.MaxTurns, vrec, func(int) { rep.addGames(1) })
			if _, err := vrec.flush(tuneShardPath(*dir, st.CMA.Gen, "v")); err != nil {
				logf("tune: recording positions: %v", err)
			}
			v := validation{Gen: st.CMA.Gen, X: x, WinRate: float64(w[0]) / float64(n[0]), Games: n[0]}
			st.Validations = append(st.Validations, v)
			lo, hi := wilson(w[0], n[0])
			stage1Best := bestValidatedRate(&st)
			newBestVN := st.BestValidated == nil || v.WinRate > st.BestValidated.WinRate
			if newBestVN {
				st.BestValidated = &v
			}
			record := map[string]any{"gen": v.Gen, "winRate": v.WinRate, "games": v.Games, "ci95": []float64{lo, hi}, "at": nowMS()}
			selected := false
			if st.Stage2 == nil {
				// Stage 1: the best win rate against 3 x Normal.
				selected = newBestVN
				if selected {
					event(*dir, "info", "Validation at generation %d: %.1f%% vs 3 x Normal (95%% CI %.1f-%.1f, %d games), new best", v.Gen, 100*v.WinRate, 100*lo, 100*hi, v.Games)
				} else {
					event(*dir, "info", "Validation at generation %d: %.1f%% vs 3 x Normal (95%% CI %.1f-%.1f); best stays %.1f%% (gen %d)", v.Gen, 100*v.WinRate, 100*lo, 100*hi, 100*st.BestValidated.WinRate, st.BestValidated.Gen)
				}
			} else {
				// Stage 2: also against the champion field; select on the
				// average, never giving up more than 1.5 points against Normal.
				champ := paramsOf(st.Champion)
				rep.set(fmt.Sprintf("Validating mean · gen %d/%d", st.CMA.Gen, cfg.Gens), "Measuring the current strategy against Normal + 2 x champion", nil)
				wc, nc := playCandidates([]ai.Params{p}, [3]*ai.Params{&normal, &champ, &champ}, vseeds, cfg.Workers, cfg.MaxTurns, nil, func(int) { rep.addGames(1) })
				vc := float64(wc[0]) / float64(nc[0])
				score := (v.WinRate + vc) / 2
				record["vsChampion"], record["score"] = vc, score
				selected = score > st.Stage2.Score && v.WinRate >= stage1Best-0.015
				if selected {
					st.Stage2 = &stage2Best{Gen: v.Gen, X: x, VN: v.WinRate, VC: vc, Score: score}
				}
				tail := fmt.Sprintf("; best stays gen %d (score %.1f)", st.Stage2.Gen, 100*st.Stage2.Score)
				if selected {
					tail = ", new best"
				}
				event(*dir, "info", "Validation at generation %d: %.1f%% vs 3 x Normal, %.1f%% vs Normal + 2 x champion (score %.1f)%s",
					v.Gen, 100*v.WinRate, 100*vc, 100*score, tail)
			}
			if selected {
				writeParamsStatus(*dir, x)
				if err := writeJSONAtomic(filepath.Join(*dir, "tune", "best.json"), p); err != nil {
					logf("tune: %v", err)
				}
			}
			_ = appendJSONL(filepath.Join(*dir, "tune", "validations.jsonl"), record)
		}
		if err := writeJSONAtomic(filepath.Join(*dir, "tune", fmt.Sprintf("mean-gen%03d.json", st.CMA.Gen)), paramsOf(st.CMA.Mean)); err != nil {
			logf("tune: %v", err)
		}
		if err := writeJSONAtomic(statePath, &st); err != nil {
			fatalf("tune: checkpoint: %v", err)
		}
	}
	rep.set("Tuning finished", fmt.Sprintf("Best validated strategy: %.1f%% vs 3 x Normal (gen %d)", 100*bestValidatedRate(&st), bestValidatedGen(&st)), nil)
	if st.Stage2 != nil {
		event(*dir, "info", "Selected strategy: gen %d, %.1f%% vs 3 x Normal, %.1f%% vs Normal + 2 x champion", st.Stage2.Gen, 100*st.Stage2.VN, 100*st.Stage2.VC)
	}
	event(*dir, "info", "Tuning finished after %d generations", st.CMA.Gen)
}

func bestValidatedRate(st *tuneState) float64 {
	if st.BestValidated == nil {
		return 0
	}
	return st.BestValidated.WinRate
}

func bestValidatedGen(st *tuneState) int {
	if st.BestValidated == nil {
		return 0
	}
	return st.BestValidated.Gen
}

// bestX is the selected strategy: the stage-2 selection, else the best
// validated mean, or the current mean before any validation.
func bestX(st *tuneState) []float64 {
	if st.Stage2 != nil {
		return append([]float64(nil), st.Stage2.X...)
	}
	if st.BestValidated != nil {
		return append([]float64(nil), st.BestValidated.X...)
	}
	return append([]float64(nil), st.CMA.Mean...)
}

func normalise(v []float64) []float64 {
	out := make([]float64, len(v))
	for i, k := range ai.Knobs {
		out[i] = (v[i] - k.Min) / (k.Max - k.Min)
	}
	return out
}

func denormalise(x []float64) []float64 {
	out := make([]float64, len(x))
	for i, k := range ai.Knobs {
		out[i] = k.Min + clamp01(x[i])*(k.Max-k.Min)
	}
	return out
}

func paramsOf(x []float64) ai.Params {
	p, err := ai.ParamsFromVector(denormalise(x))
	if err != nil {
		panic(err)
	}
	return p
}

func writeParamsStatus(dir string, x []float64) {
	v := denormalise(x)
	type entry struct {
		Name    string  `json:"name"`
		Value   float64 `json:"value"`
		Default float64 `json:"default"`
		Min     float64 `json:"min"`
		Max     float64 `json:"max"`
		Group   string  `json:"group"`
	}
	list := make([]entry, len(ai.Knobs))
	for i, k := range ai.Knobs {
		list[i] = entry{k.Name, round4(v[i]), k.Default, k.Min, k.Max, k.Group}
	}
	if err := writeJSONAtomic(filepath.Join(dir, "status", "params.json"), map[string]any{"updatedAt": nowMS(), "params": list}); err != nil {
		logf("status: %v", err)
	}
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// playCandidates plays every candidate on every seed in all four seats
// against the field, on workers goroutines, and returns wins and games per
// candidate.
func playCandidates(cands []ai.Params, field [3]*ai.Params, seeds []int64, workers, maxTurns int, rec *recorder, onGame func(done int)) (wins, games []int) {
	type job struct{ cand, seed, seat int }
	jobs := make(chan job, 256)
	wins = make([]int, len(cands))
	games = make([]int, len(cands))
	var mu sync.Mutex
	var done atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				won, err := playSeat(&cands[j.cand], field, seeds[j.seed], j.seat, maxTurns, rec.game(j.cand, j.seed, j.seat))
				mu.Lock()
				if err == nil {
					games[j.cand]++
					if won {
						wins[j.cand]++
					}
				}
				mu.Unlock()
				if err != nil {
					logf("game error (seed %d): %v", seeds[j.seed], err)
				}
				onGame(int(done.Add(1)))
			}
		}()
	}
	// Interleave candidates so a partial generation is balanced.
	for s := range seeds {
		for seat := 0; seat < 4; seat++ {
			for c := range cands {
				jobs <- job{c, s, seat}
			}
		}
	}
	close(jobs)
	wg.Wait()
	return wins, games
}

// playSeat plays one 4-player game: p in lobby seat `seat`, the field in the
// others (in order after it).
func playSeat(p *ai.Params, field [3]*ai.Params, seed int64, seat, maxTurns int, rg *recordedGame) (bool, error) {
	res, err := hardai.PlayMatch(hardai.MatchConfig{
		OnAction: rg.onAction(),
		Levels:   []hardai.Level{hardai.Normal, hardai.Normal, hardai.Normal, hardai.Normal},
		Seed:     seed,
		MaxTurns: maxTurns,
		NewBot: func(s int, _ hardai.Level, _ int64) hardai.Bot {
			if s == seat {
				return hardai.NewTunedBot(p)
			}
			return hardai.NewTunedBot(field[(s-seat+3)%4])
		},
	})
	if err != nil {
		return false, err
	}
	rg.finish(res)
	return res.Seats[seat].Won, nil
}

// wilson is the Wilson score interval at 95%.
func wilson(k, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 0
	}
	const z = 1.959964
	p := float64(k) / float64(n)
	nf := float64(n)
	denom := 1 + z*z/nf
	center := (p + z*z/(2*nf)) / denom
	half := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / denom
	return math.Max(0, center-half), math.Min(1, center+half)
}

func mixSeed(values ...uint64) uint64 {
	h := uint64(0x6a09e667f3bcc909)
	for _, v := range values {
		h ^= v + 0x9e3779b97f4a7c15 + (h << 6) + (h >> 2)
		h = (h ^ (h >> 33)) * 0xff51afd7ed558ccd
		h = (h ^ (h >> 33)) * 0xc4ceb93fe53b5c4f
		h ^= h >> 33
	}
	return h
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// validationSeeds are the fixed deals of every validation.
func validationSeeds(cfg tuneConfig) []int64 {
	seeds := make([]int64, cfg.ValidateSeeds)
	base := int64(mixSeed(cfg.RunSeed, 999, 0) >> 2)
	for i := range seeds {
		seeds[i] = base + int64(i)
	}
	return seeds
}
