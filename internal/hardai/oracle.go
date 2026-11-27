package hardai

import (
	"math"
	"sync"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/valuenet"
)

// Oracle is a slow, strong judge of a decision, for analysis (cmd/gm-attack),
// not a bot: it compares options by many long rollouts. Every option of a
// rollout sees the same reshuffled decks and the same per-seat dice
// (common random numbers), so the paired difference between two options
// measures the decision, not the luck. Every seat plays the same policy (the
// tuned heuristic by default), and a rollout that stops before the game ends
// is scored by the value network (or net-worth share without one).
//
// It sees only public information, like the bots: the unseen deck order is
// reshuffled for every rollout.
type Oracle struct {
	opts    OracleOptions
	workers []*simWorker
}

// OracleOptions configure an Oracle. Zero fields take the defaults.
type OracleOptions struct {
	Rollouts int           // paired rollouts per evaluation (default 512)
	Rounds   int           // rollout length in rounds of play (0 = 6; negative = to the end of the game)
	Net      *valuenet.Net // scores unfinished rollouts (nil = the embedded model; see NoNet)
	NoNet    bool          // score unfinished rollouts by net-worth share instead
	Params   *ai.Params    // knobs of every seat in rollouts (nil = Expert's tuned knobs)
	Others   *ai.Params    // if set, knobs of every seat but the judged one
	Workers  int           // goroutines (default GOMAXPROCS)
	Seed     uint64        // rollout seeds derive from it (independent oracles: different seeds)
	// FullTrades plays the heuristic's full trade search in rollouts instead
	// of the light proposer of the bots' rollouts (several times slower).
	FullTrades bool
	// MaxTurns caps whole-game rollouts at this many turns past the position
	// (0 = until 1,500 turns in all); a capped rollout is scored by the network.
	MaxTurns int
}

// NewOracle returns an oracle.
func NewOracle(o OracleOptions) *Oracle {
	if o.Rollouts <= 0 {
		o.Rollouts = 512
	}
	if o.Rounds == 0 {
		o.Rounds = 6
	}
	if o.Params == nil {
		o.Params = ExpertParams()
	}
	if o.Others == nil {
		o.Others = o.Params
	}
	if o.Net == nil && !o.NoNet {
		if net, err := valuenet.Default(); err == nil {
			o.Net = net
		}
	}
	if o.NoNet {
		o.Net = nil
	}
	if o.Workers <= 0 {
		o.Workers = defaultWorkers()
	}
	or := &Oracle{opts: o}
	for i := 0; i < o.Workers; i++ {
		w := newSimWorker(Options{Params: o.Params, OpponentParams: o.Others, Eval: EvalValueNet, Net: o.Net})
		if o.Net == nil {
			w.eval = EvalNetWorth
		}
		w.policy.fullTrades = o.FullTrades
		w.maxTurns = o.MaxTurns
		or.workers = append(or.workers, w)
	}
	return or
}

// OracleOption is one option to judge: actions the judged player plays now,
// or a custom transition (Apply), applied to a copy of the game.
type OracleOption struct {
	Name    string
	Actions []engine.Action
	Apply   func(sim *engine.Game, me string) bool
}

// OracleResult holds the judged player's average outcome per option (its
// win probability: 1 or 0 for finished rollouts, the network's estimate
// otherwise) and every option's paired difference from option 0 with its
// standard error.
type OracleResult struct {
	Mean []float64
	Diff []float64 // Mean[i] - Mean[0], paired
	SE   []float64 // standard error of Diff[i]
	N    []int     // rollouts where both option i and option 0 applied
	// Others[i][j]: average win probability of player j (index in g.Players)
	// under option i, so callers can see who gains from a deal.
	Others [][]float64
}

// Evaluate plays Rollouts paired rollouts of every option for player me
// from g (which it does not modify).
func (o *Oracle) Evaluate(g *engine.Game, me string, options []OracleOption) OracleResult {
	n := len(options)
	N := o.opts.Rollouts
	scores := make([][]float64, N)
	others := make([][][]float64, N)
	root := cloneGame(g, true)
	horizon := math.MaxInt32
	if o.opts.Rounds > 0 {
		horizon = o.opts.Rounds * max(1, activeCount(g))
	}
	useNet := o.opts.Net != nil && valuenet.Fits(g)
	cands := make([]candidate, n)
	for i, opt := range options {
		cands[i] = candidate{name: opt.Name, actions: opt.Actions, apply: opt.Apply}
	}
	seed := mix(o.opts.Seed, uint64(g.Version), hashString(me), hashString(g.Code))
	var wg sync.WaitGroup
	var next int
	var mu sync.Mutex
	for w := range o.workers {
		wg.Add(1)
		go func(w *simWorker) {
			defer wg.Done()
			for {
				mu.Lock()
				r := next
				next++
				mu.Unlock()
				if r >= N {
					return
				}
				row := make([]float64, n)
				orow := make([][]float64, n)
				for i := range cands {
					row[i], orow[i] = w.oracleRollout(root, me, cands[i], mix(seed, uint64(r)), horizon, useNet)
				}
				scores[r] = row
				others[r] = orow
			}
		}(o.workers[w])
	}
	wg.Wait()
	res := OracleResult{Mean: make([]float64, n), Diff: make([]float64, n), SE: make([]float64, n), N: make([]int, n),
		Others: make([][]float64, n)}
	players := len(g.Players)
	for i := 0; i < n; i++ {
		var sum, dsum, dsq float64
		count, dn := 0, 0
		res.Others[i] = make([]float64, players)
		for r := 0; r < N; r++ {
			v := scores[r][i]
			if math.IsNaN(v) {
				continue
			}
			sum += v
			count++
			for j := 0; j < players && j < len(others[r][i]); j++ {
				res.Others[i][j] += others[r][i][j]
			}
			base := scores[r][0]
			if math.IsNaN(base) {
				continue
			}
			d := v - base
			dsum += d
			dsq += d * d
			dn++
		}
		if count > 0 {
			res.Mean[i] = sum / float64(count)
			for j := range res.Others[i] {
				res.Others[i][j] /= float64(count)
			}
		} else {
			res.Mean[i] = math.NaN()
		}
		res.N[i] = dn
		if dn > 1 {
			mean := dsum / float64(dn)
			res.Diff[i] = mean
			res.SE[i] = math.Sqrt(math.Max(0, (dsq-dsum*dsum/float64(dn))/float64(dn-1)) / float64(dn))
		} else {
			res.Diff[i], res.SE[i] = math.NaN(), math.Inf(1)
		}
	}
	return res
}

// oracleRollout is rollout that also reports every player's outcome.
func (w *simWorker) oracleRollout(root *engine.Game, me string, c candidate, seed uint64, horizon int, useNet bool) (float64, []float64) {
	sim := cloneGame(root, true)
	w.prepareSim(sim, seed)
	w.policy.me = me
	if !w.applyCandidate(sim, me, c) {
		return math.NaN(), nil
	}
	w.runLong(sim, horizon, w.maxTurns)
	all := make([]float64, len(sim.Players))
	if sim.Status == engine.StatusFinished {
		for j, p := range sim.Players {
			if p.ID == sim.WinnerID {
				all[j] = 1
			}
		}
	} else if useNet && w.vn != nil && valuenet.Fits(sim) {
		var pos valuenet.Position
		valuenet.FillFromGame(&pos, sim)
		probs := w.vn.WinProbs(&pos)
		for j, p := range sim.Players {
			if s := valuenet.SlotOf(sim, p.ID); s >= 0 {
				all[j] = probs[s]
			}
		}
	} else {
		for j, p := range sim.Players {
			all[j] = score(sim, p.ID, EvalNetWorth)
		}
	}
	mine := 0.0
	for j, p := range sim.Players {
		if p.ID == me {
			mine = all[j]
		}
	}
	return mine, all
}

// runLong is run without the step cap of short rollouts (a whole game can
// take several thousand actions).
func (w *simWorker) runLong(sim *engine.Game, horizonTurns, maxTurns int) {
	if horizonTurns < math.MaxInt32 {
		w.run(sim, horizonTurns)
		return
	}
	if maxTurns > 0 {
		w.run(sim, maxTurns)
		return
	}
	for k := 0; k < 25 && sim.Status == engine.StatusPlaying; k++ {
		before := totalTurns(sim)
		w.run(sim, math.MaxInt32)
		if totalTurns(sim) == before {
			return // stuck
		}
		if totalTurns(sim) > 1500 {
			return // a stalemate; scored where it stands
		}
	}
}

// WinAuction ends the simulated auction in sim with me buying the space at
// price (for oracle options).
func WinAuction(sim *engine.Game, me string, price int) bool { return winAuction(sim, me, price) }
