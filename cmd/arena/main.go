// Command arena plays bot-vs-bot Estate games and reports how the computer
// levels compare.
//
//	go run ./cmd/arena -games 2000 -seats hard,normal,normal,normal -parallel 6 -seed 1
//	go run ./cmd/arena -games 1000 -seats normal,easy,easy,easy
//	go run ./cmd/arena -games 300 -seats hard,normal,normal,normal -rules loans -json out.json
//
// Seat assignments rotate every game (game i shifts the -seats list by i), so
// every entry plays every lobby position equally often; turn order is then
// rolled as in a real game. A game still running after -maxturns turns is
// adjudicated to the player with the highest net worth (reported
// separately). Win rates come with Wilson 95% confidence intervals; Elo
// ratings are a Bradley-Terry fit of winner-beats-each-loser pairs, with
// Normal anchored at 1500. Decision timing covers every Choose call per
// level and, separately, the Monte Carlo decisions of Hard and Expert (on Windows the
// monotonic clock ticks coarsely, so sub-millisecond decisions read as 0).
//
// Hard and Expert stop a decision after a fixed number of rollout rounds
// (their strength; the server's 250 ms is only a safety cap), so by default
// the arena sets no time limit and its games play exactly as production
// bots, however loaded the machine. -budget adds a wall-clock cap per
// decision (single-threaded with -workers 1, the default: -parallel above
// the number of physical cores then makes games share cores and the bots
// play fewer rollouts within it). -disable leaves chosen decision kinds to the heuristic
// (ablation).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/hardai"
	"github.com/tdar200/estate-games/internal/valuenet"
	"github.com/tdar200/estate-games/internal/valuenet2"
)

type config struct {
	Games      int                       `json:"games"`
	Seats      string                    `json:"seats"`
	Parallel   int                       `json:"parallel"`
	Seed       int64                     `json:"seed"`
	Budget     string                    `json:"budget"`
	Rollouts   int                       `json:"rollouts"`
	Horizon    int                       `json:"horizon"`
	Workers    int                       `json:"workers"`
	Confidence float64                   `json:"confidence"`
	MaxTurns   int                       `json:"maxTurns"`
	Mistakes   float64                   `json:"mistakeRate"`
	Eval       int                       `json:"eval"`
	Rules      string                    `json:"rules,omitempty"`
	Disable    string                    `json:"disable,omitempty"`
	Market     bool                      `json:"market"`
	ExpertNet  string                    `json:"expertNet,omitempty"`
	ExpertPar  string                    `json:"expertParams,omitempty"`
	OppParams  string                    `json:"opponentParams,omitempty"`
	HardParams string                    `json:"hardParams,omitempty"`
	ExpertHor  int                       `json:"expertHorizon,omitempty"`
	ExpertConf float64                   `json:"expertConfidence,omitempty"`
	ExpertCand int                       `json:"expertCandidates,omitempty"`
	ExpertNetT bool                      `json:"expertNetTrades"`
	ExpertNTS  int                       `json:"expertNetTradeScreen,omitempty"`
	ExpertNTK  int                       `json:"expertNetTradeKeep,omitempty"`
	ExpertRol  int                       `json:"expertRollouts,omitempty"`
	MaxDeals   int                       `json:"maxDeals,omitempty"`
	ExpertMD   int                       `json:"expertMaxDeals,omitempty"`
	GMNet      string                    `json:"gmNet,omitempty"`
	GMRol      int                       `json:"gmRollouts,omitempty"`
	GMCand     int                       `json:"gmCandidates,omitempty"`
	GMTrade    hardai.GrandmasterOptions `json:"gm"`
}

func main() {
	var cfg config
	var budget time.Duration
	var jsonPath string
	var quiet bool
	flag.IntVar(&cfg.Games, "games", 200, "number of games")
	flag.StringVar(&cfg.Seats, "seats", "hard,normal,normal,normal", "comma-separated levels, one per seat (easy, normal, hard, expert)")
	flag.IntVar(&cfg.Parallel, "parallel", runtime.NumCPU(), "games played at once")
	flag.Int64Var(&cfg.Seed, "seed", 1, "base seed (game i uses seed+i)")
	flag.DurationVar(&budget, "budget", 0, "Hard and Expert: time cap per decision (0 = none: the levels' rollout counts decide, as in production on any normal machine)")
	flag.IntVar(&cfg.Rollouts, "rollouts", 0, "Hard: max rollout rounds per decision (0 = default)")
	flag.IntVar(&cfg.Horizon, "horizon", 0, "Hard: rollout length in rounds (0 = default)")
	flag.IntVar(&cfg.Workers, "workers", 1, "Hard: goroutines per decision")
	flag.Float64Var(&cfg.Confidence, "confidence", 0, "Hard: z-score needed to override the heuristic (0 = default)")
	flag.IntVar(&cfg.MaxTurns, "maxturns", 1000, "turn cap per game (then adjudicated on net worth)")
	flag.Float64Var(&cfg.Mistakes, "mistakes", 0, "Easy: mistake-rate multiplier (0 = default)")
	flag.IntVar(&cfg.Eval, "eval", 0, "Hard: rollout evaluation (0 = net-worth share, 1 = strategic worth share)")
	flag.StringVar(&cfg.Rules, "rules", "", "house rules: comma-separated loans, jackpot, noauctions, cash=N (empty = classic)")
	flag.StringVar(&cfg.Disable, "disable", "", "Hard: decision kinds left to the heuristic (ablation): auction,purchase,trade,roll,complete,proposals")
	flag.BoolVar(&cfg.Market, "market", true, "Hard: also propose plain purchases and sales (-market=false to disable)")
	flag.StringVar(&cfg.ExpertNet, "expert-net", "", "Expert: value network file (default: the embedded model)")
	flag.StringVar(&cfg.ExpertPar, "expert-params", "", "Expert: knobs JSON (default: the shipped expert_params.json)")
	flag.StringVar(&cfg.OppParams, "opponent-params", "", "Expert: knobs JSON its rollouts play the other seats with (default: Normal)")
	flag.StringVar(&cfg.HardParams, "hard-params", "", "Hard: knobs JSON for its baseline and its own seat in rollouts (ablation; default: Normal's)")
	flag.IntVar(&cfg.ExpertHor, "expert-horizon", 0, "Expert: rollout rounds before the value network scores (0 = its default; -horizon is Hard's)")
	flag.Float64Var(&cfg.ExpertConf, "expert-confidence", 0, "Expert: z-score to override its baseline (0 = default; -confidence is Hard's)")
	flag.IntVar(&cfg.ExpertCand, "expert-candidates", 0, "Expert: max plans per decision (0 = its default)")
	flag.BoolVar(&cfg.ExpertNetT, "expert-nettrades", true, "Expert: propose the trade ideas its value network screens (-expert-nettrades=false to disable)")
	flag.IntVar(&cfg.ExpertNTS, "expert-nettrade-screen", 0, "Expert: trade ideas priced per decision (0 = default)")
	flag.IntVar(&cfg.ExpertNTK, "expert-nettrade-keep", 0, "Expert: trade ideas passed to the search (0 = default)")
	flag.IntVar(&cfg.ExpertRol, "expert-rollouts", 0, "Expert: max rollout rounds per decision (0 = its default; -rollouts is Hard's)")
	flag.IntVar(&cfg.MaxDeals, "max-deals", 0, "Hard: deals it proposes that one partner accepts, per game (0 = default, -1 = no cap)")
	flag.IntVar(&cfg.ExpertMD, "expert-max-deals", 0, "Expert: the same (0 = default; -max-deals is Hard's)")
	flag.StringVar(&cfg.GMNet, "gm-net", "", "Grandmaster: its network file (internal/valuenet2; default: the shipped one, else Expert's)")
	flag.IntVar(&cfg.GMRol, "gm-rollouts", 0, "Grandmaster: max rollout rounds per ordinary decision (0 = default)")
	flag.IntVar(&cfg.GMCand, "gm-candidates", 0, "Grandmaster: max plans per decision (0 = default)")
	flag.IntVar(&cfg.GMTrade.TradeRollouts, "gm-trade-rollouts", 0, "Grandmaster: rollout rounds per trade answer (0 = default)")
	flag.IntVar(&cfg.GMTrade.TradeHorizon, "gm-trade-horizon", 0, "Grandmaster: rounds of play per trade-answer rollout (0 = default)")
	flag.Float64Var(&cfg.GMTrade.Margin, "gm-margin", 0, "Grandmaster: win-probability gain needed to accept a deal (0 = default)")
	flag.Float64Var(&cfg.GMTrade.Z, "gm-z", 0, "Grandmaster: standard errors on top of the margin (0 = default)")
	flag.Float64Var(&cfg.GMTrade.GroupMargin, "gm-group-margin", 0, "Grandmaster: margin when the deal gives the proposer a colour group (0 = default)")
	flag.Float64Var(&cfg.GMTrade.Rival, "gm-rival", 0, "Grandmaster: weight of the proposer's gain (0 = default, negative = none)")
	flag.Float64Var(&cfg.GMTrade.RivalLeader, "gm-rival-leader", 0, "Grandmaster: ... when the proposer is the strongest rival (0 = default, negative = none)")
	flag.BoolVar(&cfg.GMTrade.NoStyles, "gm-nostyles", false, "Grandmaster: rollouts model opponents with the default heuristic only")
	flag.BoolVar(&cfg.GMTrade.NoHumanOffers, "gm-nohuman", false, "Grandmaster: no offers to human players")
	flag.BoolVar(&cfg.GMTrade.SharpProposer, "gm-sharp", false, "Grandmaster: trade answers play the proposer with the tuned heuristic")
	flag.StringVar(&jsonPath, "json", "", "also write the report as JSON to this file")
	flag.BoolVar(&quiet, "quiet", false, "no progress output")
	flag.IntVar(&maxTemp, "max-temp", 0, "pause new games while the CPU package is at or above this temperature in °C (Linux; 0 = off)")
	var combine string
	var bootstrap int
	flag.StringVar(&combine, "combine", "", "instead of playing: fit one Elo ladder over these comma-separated -json reports")
	flag.IntVar(&bootstrap, "bootstrap", 400, "-combine: bootstrap resamples for the 95% intervals")
	flag.Parse()
	if combine != "" {
		if err := combineReports(os.Stdout, strings.Split(combine, ","), bootstrap); err != nil {
			fmt.Fprintln(os.Stderr, "arena:", err)
			os.Exit(1)
		}
		return
	}
	// Simulations allocate briskly; trading memory for less GC work speeds
	// them up noticeably.
	debug.SetGCPercent(400)

	levels, err := parseSeats(cfg.Seats)
	if err == nil {
		matchRules, err = parseRules(cfg.Rules)
	}
	var disabled hardai.Decisions
	if err == nil {
		disabled, err = hardai.ParseDecisions(cfg.Disable)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "arena:", err)
		os.Exit(2)
	}
	if cfg.Games < 1 || cfg.Parallel < 1 {
		fmt.Fprintln(os.Stderr, "arena: -games and -parallel must be positive")
		os.Exit(2)
	}
	cfg.Budget = budget.String()
	opts := hardai.Options{
		Budget:     budget,
		Rollouts:   cfg.Rollouts,
		Horizon:    cfg.Horizon,
		Workers:    cfg.Workers,
		Confidence: cfg.Confidence,
		// MistakeRate 0 is the default; the flag passes through.
		MistakeRate:    cfg.Mistakes,
		Eval:           cfg.Eval,
		Disable:        disabled,
		NoMarketTrades: !cfg.Market,
		MaxDeals:       cfg.MaxDeals,
	}
	if err := loadExpertOptions(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "arena:", err)
		os.Exit(2)
	}
	if budget == 0 {
		opts.Budget = -1
	}
	rep := run(cfg, levels, opts, quiet)
	rep.print(os.Stdout)
	if jsonPath != "" {
		data, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(jsonPath, append(data, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "arena:", err)
			os.Exit(1)
		}
	}
	if rep.Errors > 0 {
		os.Exit(1)
	}
}

// matchRules are the house rules of every game (-rules).
var matchRules *engine.RulesUpdate

// parseRules reads -rules: a comma-separated list of loans, jackpot,
// noauctions and cash=N (empty = classic rules).
func parseRules(s string) (*engine.RulesUpdate, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "classic" {
		return nil, nil
	}
	on, off := true, false
	r := &engine.RulesUpdate{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "loans":
			r.Loans = &on
		case part == "jackpot":
			r.FreeParkingJackpot = &on
		case part == "noauctions":
			r.Auctions = &off
		case strings.HasPrefix(part, "cash="):
			cash, err := strconv.Atoi(strings.TrimPrefix(part, "cash="))
			if err != nil {
				return nil, fmt.Errorf("bad starting cash in %q", part)
			}
			r.StartingCash = &cash
		default:
			return nil, fmt.Errorf("unknown house rule %q (want loans, jackpot, noauctions, cash=N)", part)
		}
	}
	return r, nil
}

func parseSeats(s string) ([]hardai.Level, error) {
	var levels []hardai.Level
	for _, part := range strings.Split(s, ",") {
		level, err := hardai.ParseLevel(part)
		if err != nil {
			return nil, err
		}
		levels = append(levels, level)
	}
	if len(levels) < 2 || len(levels) > engine.MaxPlayers {
		return nil, fmt.Errorf("need 2 to %d seats, got %d", engine.MaxPlayers, len(levels))
	}
	return levels, nil
}

// ------------------------------------------------------------------ run

// gameRecord is one game's outcome, per -seats entry.
type gameRecord struct {
	err         error
	turns       int
	steps       int
	adjudicated bool
	won         []bool
	bankrupt    []bool
	rejected    []int
	loans       int
	winner      int // -seats entry of the winner (-1: none)
	trades      tradeTally
}

// tradeTally counts one game's trade offers (every seat is a bot here).
type tradeTally struct {
	offers, accepted, rejected int
	maxSameDeal                int                     // most times one deal (proposer, target, deeds) was offered
	sameDealBy                 map[hardai.Level]int    // ... per proposer level
	acceptedBy                 map[[2]hardai.Level]int // accepted trades by proposer and target level
	deeds                      map[hardai.Level]int    // deeds held at the end, per level
	seats                      map[hardai.Level]int    // seats per level (for averages)
}

// tradeWatcher builds a tradeTally from the driver's OnAction calls.
type tradeWatcher struct {
	tally    tradeTally
	deals    map[string]int
	proposer string
	target   string
}

func levelOfPlayer(g *engine.Game, id string) hardai.Level {
	if p := g.PlayerByID(id); p != nil {
		if name, _, ok := strings.Cut(p.Name, "-"); ok {
			if l, err := hardai.ParseLevel(name); err == nil {
				return l
			}
		}
	}
	return hardai.Normal
}

func (w *tradeWatcher) onAction(g *engine.Game, playerID string, a engine.Action) {
	switch a.Type {
	case engine.ActionOfferTrade:
		w.tally.offers++
		w.proposer, w.target = playerID, a.TargetID
		key := playerID + "|" + a.TargetID
		for _, b := range []*engine.TradeBundle{a.Offer, a.Request} {
			key += "|"
			if b != nil {
				props := append([]int(nil), b.Properties...)
				sort.Ints(props)
				for _, p := range props {
					key += strconv.Itoa(p) + ","
				}
			}
		}
		w.deals[key]++
		w.tally.maxSameDeal = max(w.tally.maxSameDeal, w.deals[key])
		l := levelOfPlayer(g, playerID)
		w.tally.sameDealBy[l] = max(w.tally.sameDealBy[l], w.deals[key])
	case engine.ActionAcceptTrade:
		w.tally.accepted++
		w.tally.acceptedBy[[2]hardai.Level{levelOfPlayer(g, w.proposer), levelOfPlayer(g, w.target)}]++
	case engine.ActionRejectTrade:
		w.tally.rejected++
	}
}

// timings collects decision durations per level (all decisions) and the
// Hard bot's Monte Carlo decisions.
type timings struct {
	mu        sync.Mutex
	all       map[hardai.Level][]time.Duration
	mc        []time.Duration
	mcRounds  []int
	mcKept    int                              // Monte Carlo decisions that kept the heuristic's choice
	mcLevel   map[hardai.Level][]time.Duration // Monte Carlo decisions per level
	mcRoundsL map[hardai.Level][]int
	byPhase   map[string]int
	overrides map[string]int
}

func (t *timings) add(level hardai.Level, d time.Duration) {
	t.mu.Lock()
	t.all[level] = append(t.all[level], d)
	t.mu.Unlock()
}

func (t *timings) addMC(level hardai.Level, info hardai.DecisionInfo) {
	t.mu.Lock()
	t.mc = append(t.mc, info.Duration)
	t.mcLevel[level] = append(t.mcLevel[level], info.Duration)
	t.mcRoundsL[level] = append(t.mcRoundsL[level], info.Rounds)
	t.mcRounds = append(t.mcRounds, info.Rounds)
	if info.Heuristic {
		t.mcKept++
	}
	t.byPhase[info.Phase]++
	if !info.Heuristic && info.Kind != "" {
		t.overrides[info.Phase+": "+info.Kind]++
	}
	t.mu.Unlock()
}

// timedBot measures every Choose call.
type timedBot struct {
	bot   hardai.Bot
	level hardai.Level
	t     *timings
}

func (b timedBot) Choose(g *engine.Game, playerID string) (engine.Action, bool) {
	start := time.Now()
	a, ok := b.bot.Choose(g, playerID)
	b.t.add(b.level, time.Since(start))
	return a, ok
}

func run(cfg config, levels []hardai.Level, opts hardai.Options, quiet bool) *report {
	n := len(levels)
	records := make([]gameRecord, cfg.Games)
	tm := &timings{all: map[hardai.Level][]time.Duration{}, byPhase: map[string]int{}, overrides: map[string]int{},
		mcLevel: map[hardai.Level][]time.Duration{}, mcRoundsL: map[hardai.Level][]int{}}
	mcOpts := opts
	start := time.Now()
	var next, done atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < cfg.Parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= cfg.Games {
					return
				}
				thermalWait()
				records[i] = playOne(cfg, levels, mcOpts, i, tm)
				if d := done.Add(1); !quiet && (d%max(1, int64(cfg.Games)/20) == 0 || d == int64(cfg.Games)) {
					fmt.Fprintf(os.Stderr, "\r%d/%d games (%s)", d, cfg.Games, time.Since(start).Round(time.Second))
				}
			}
		}()
	}
	wg.Wait()
	if !quiet {
		fmt.Fprintln(os.Stderr)
	}
	return buildReport(cfg, levels, records, tm, time.Since(start), n)
}

func playOne(cfg config, levels []hardai.Level, opts hardai.Options, i int, tm *timings) gameRecord {
	n := len(levels)
	rot := i % n
	// Lobby seat s is played by -seats entry (s+rot)%n.
	seatLevels := make([]hardai.Level, n)
	entryOf := make([]int, n)
	for s := 0; s < n; s++ {
		entryOf[s] = (s + rot) % n
		seatLevels[s] = levels[entryOf[s]]
	}
	watch := &tradeWatcher{deals: map[string]int{}}
	watch.tally.acceptedBy = map[[2]hardai.Level]int{}
	watch.tally.sameDealBy = map[hardai.Level]int{}
	res, err := hardai.PlayMatch(hardai.MatchConfig{
		Levels:   seatLevels,
		Seed:     cfg.Seed + int64(i),
		Rules:    matchRules,
		Options:  opts,
		MaxTurns: cfg.MaxTurns,
		OnAction: watch.onAction,
		NewBot: func(seat int, level hardai.Level, seed int64) hardai.Bot {
			o := opts
			switch level {
			case hardai.Expert:
				o.Params, o.Net, o.OpponentParams = expertParams, expertNet, opponentParams
				o.Horizon, o.Confidence, o.MaxCandidates = cfg.ExpertHor, cfg.ExpertConf, cfg.ExpertCand
				o.Rollouts = cfg.ExpertRol
				o.MaxDeals = cfg.ExpertMD
				o.NoNetTrades = !cfg.ExpertNetT
				o.NetTradeScreen, o.NetTradeKeep = cfg.ExpertNTS, cfg.ExpertNTK
			case hardai.Grandmaster:
				o.Params, o.Net = expertParams, expertNet
				o.Rollouts, o.MaxCandidates = cfg.GMRol, cfg.GMCand
				o.Net2 = gmNet
				gm := cfg.GMTrade
				o.GM = &gm
			case hardai.Hard:
				o.Params = hardParams
			}
			o.OnDecision = func(info hardai.DecisionInfo) {
				if info.MonteCarlo {
					tm.addMC(level, info)
				}
			}
			return timedBot{bot: hardai.NewBot(level, seed, o), level: level, t: tm}
		},
	})
	rec := gameRecord{err: err, won: make([]bool, n), bankrupt: make([]bool, n), rejected: make([]int, n), winner: -1}
	if err != nil {
		return rec
	}
	rec.turns, rec.steps, rec.adjudicated = res.Turns, res.Steps, res.Adjudicated
	rec.loans = len(res.Game.Loans)
	rec.trades = watch.tally
	rec.trades.deeds = map[hardai.Level]int{}
	rec.trades.seats = map[hardai.Level]int{}
	for s, seat := range res.Seats {
		e := entryOf[s]
		rec.won[e] = seat.Won
		if seat.Won {
			rec.winner = e
		}
		rec.bankrupt[e] = seat.Bankrupt
		rec.rejected[e] = seat.Rejected
		rec.trades.seats[seat.Level]++
		for _, index := range engine.OwnableIndexes {
			if st := res.Game.Properties[index]; st != nil && st.OwnerID == seat.PlayerID {
				rec.trades.deeds[seat.Level]++
			}
		}
	}
	return rec
}

// --------------------------------------------------------------- report

type seatReport struct {
	Entry       int     `json:"entry"`
	Level       string  `json:"level"`
	Wins        int     `json:"wins"`
	Outright    int     `json:"outrightWins"`
	Adjudicated int     `json:"adjudicatedWins"`
	WinRate     float64 `json:"winRate"`
	CILow       float64 `json:"ci95Low"`
	CIHigh      float64 `json:"ci95High"`
	Bankrupt    int     `json:"bankruptcies"`
	Rejected    int     `json:"rejectedActions"`
}

type timingReport struct {
	Level     string  `json:"level"`
	Decisions int     `json:"decisions"`
	P50ms     float64 `json:"p50ms"`
	P95ms     float64 `json:"p95ms"`
	MaxMs     float64 `json:"maxMs"`
	AvgRounds float64 `json:"avgRounds,omitempty"` // Monte Carlo rows: rollout rounds per decision
}

// pairReport counts games won by a level in which a seat of another level
// lost (the data of the Elo fit).
type pairReport struct {
	Winner string `json:"winner"`
	Loser  string `json:"loser"`
	Count  int    `json:"count"`
}

type report struct {
	Config         config             `json:"config"`
	Games          int                `json:"games"`
	Errors         int                `json:"errors"`
	FirstError     string             `json:"firstError,omitempty"`
	Finished       int                `json:"finished"`
	Adjudicated    int                `json:"adjudicated"`
	AvgTurns       float64            `json:"avgTurns"`
	AvgRounds      float64            `json:"avgRounds"`
	AvgSteps       float64            `json:"avgSteps"`
	AvgLoans       float64            `json:"avgLoans,omitempty"`
	Seats          []seatReport       `json:"seats"`
	Elo            map[string]float64 `json:"elo"`
	Timing         []timingReport     `json:"timing"`
	MCOverrides    map[string]int     `json:"mcOverrides,omitempty"` // "phase: kind" of choices that replaced the heuristic's
	MonteCarlo     *timingReport      `json:"monteCarlo,omitempty"`
	MCAvgRounds    float64            `json:"mcAvgRounds,omitempty"`
	MCKeptHeur     float64            `json:"mcKeptHeuristicShare,omitempty"`
	MCPerGame      float64            `json:"mcDecisionsPerGame,omitempty"`
	MCByPhase      map[string]int     `json:"mcByPhase,omitempty"`
	MCByLevel      []timingReport     `json:"monteCarloByLevel,omitempty"`
	Pairs          []pairReport       `json:"pairs,omitempty"`
	Trades         tradeReport        `json:"trades"`
	Winners        []int              `json:"winners"` // per game: the winning -seats entry (-1: error)
	WallSeconds    float64            `json:"wallSeconds"`
	GamesPerMinute float64            `json:"gamesPerMinute"`
}

// tradeReport summarises the trade offers (all between bots here).
type tradeReport struct {
	OffersPerGame   float64            `json:"offersPerGame"`
	AcceptedPerGame float64            `json:"acceptedPerGame"`
	AcceptRate      float64            `json:"acceptRate"`
	SameDealAvg     float64            `json:"sameDealAvg"` // per game, the most times one deal went to the same target; averaged
	SameDealMax     int                `json:"sameDealMax"` // ... its maximum over games
	SameDealByLevel map[string]int     `json:"sameDealMaxByProposer,omitempty"`
	AcceptedByPair  map[string]float64 `json:"acceptedByPairPerGame,omitempty"`
	DeedsAtEnd      map[string]float64 `json:"deedsAtEndPerSeat,omitempty"`
}

func buildReport(cfg config, levels []hardai.Level, records []gameRecord, tm *timings, wall time.Duration, n int) *report {
	rep := &report{Config: cfg, Games: len(records), WallSeconds: wall.Seconds()}
	rep.GamesPerMinute = float64(len(records)) / wall.Minutes()
	seats := make([]seatReport, n)
	for e := range seats {
		seats[e] = seatReport{Entry: e + 1, Level: levels[e].String()}
	}
	// Pairwise wins between levels for the Elo fit.
	pair := map[[2]hardai.Level]float64{}
	played := 0
	var turns, steps, loans float64
	var offers, accepted, sameDeal float64
	acceptedBy := map[string]float64{}
	deeds, deedSeats := map[string]float64{}, map[string]float64{}
	for _, r := range records {
		rep.Winners = append(rep.Winners, r.winner)
		if r.err != nil {
			rep.Errors++
			if rep.FirstError == "" {
				rep.FirstError = r.err.Error()
			}
			continue
		}
		played++
		offers += float64(r.trades.offers)
		accepted += float64(r.trades.accepted)
		sameDeal += float64(r.trades.maxSameDeal)
		rep.Trades.SameDealMax = max(rep.Trades.SameDealMax, r.trades.maxSameDeal)
		if rep.Trades.SameDealByLevel == nil {
			rep.Trades.SameDealByLevel = map[string]int{}
		}
		for l, v := range r.trades.sameDealBy {
			rep.Trades.SameDealByLevel[l.String()] = max(rep.Trades.SameDealByLevel[l.String()], v)
		}
		for k, v := range r.trades.acceptedBy {
			acceptedBy[k[0].String()+">"+k[1].String()] += float64(v)
		}
		for l, v := range r.trades.deeds {
			deeds[l.String()] += float64(v)
		}
		for l, v := range r.trades.seats {
			deedSeats[l.String()] += float64(v)
		}
		turns += float64(r.turns)
		steps += float64(r.steps)
		loans += float64(r.loans)
		if r.adjudicated {
			rep.Adjudicated++
		} else {
			rep.Finished++
		}
		winner := -1
		for e := 0; e < n; e++ {
			if r.won[e] {
				winner = e
				seats[e].Wins++
				if r.adjudicated {
					seats[e].Adjudicated++
				} else {
					seats[e].Outright++
				}
			}
			if r.bankrupt[e] {
				seats[e].Bankrupt++
			}
			seats[e].Rejected += r.rejected[e]
		}
		if winner >= 0 {
			for e := 0; e < n; e++ {
				if e != winner && levels[e] != levels[winner] {
					pair[[2]hardai.Level{levels[winner], levels[e]}]++
				}
			}
		}
	}
	if played > 0 {
		rep.AvgTurns = turns / float64(played)
		rep.AvgRounds = rep.AvgTurns / float64(n)
		rep.AvgSteps = steps / float64(played)
		rep.AvgLoans = loans / float64(played)
		rep.Trades.OffersPerGame = offers / float64(played)
		rep.Trades.AcceptedPerGame = accepted / float64(played)
		if offers > 0 {
			rep.Trades.AcceptRate = accepted / offers
		}
		rep.Trades.SameDealAvg = sameDeal / float64(played)
		rep.Trades.AcceptedByPair = map[string]float64{}
		for k, v := range acceptedBy {
			rep.Trades.AcceptedByPair[k] = v / float64(played)
		}
		rep.Trades.DeedsAtEnd = map[string]float64{}
		for k, n := range deedSeats {
			rep.Trades.DeedsAtEnd[k] = deeds[k] / n
		}
	}
	for e := range seats {
		seats[e].WinRate, seats[e].CILow, seats[e].CIHigh = wilson(seats[e].Wins, played)
	}
	rep.Seats = seats
	rep.Elo = eloFit(levels, pair)

	present := map[hardai.Level]bool{}
	for _, l := range levels {
		present[l] = true
	}
	for _, l := range []hardai.Level{hardai.Easy, hardai.Normal, hardai.Hard, hardai.Expert, hardai.Grandmaster} {
		if present[l] {
			rep.Timing = append(rep.Timing, summarize(l.String()+" (all)", tm.all[l]))
		}
	}
	for _, l := range []hardai.Level{hardai.Hard, hardai.Expert, hardai.Grandmaster} {
		if ds := tm.mcLevel[l]; len(ds) > 0 {
			tr := summarize(l.String()+" (Monte Carlo)", ds)
			rounds := 0
			for _, r := range tm.mcRoundsL[l] {
				rounds += r
			}
			tr.AvgRounds = float64(rounds) / float64(len(ds))
			rep.MCByLevel = append(rep.MCByLevel, tr)
		}
	}
	for k, v := range pair {
		rep.Pairs = append(rep.Pairs, pairReport{Winner: k[0].String(), Loser: k[1].String(), Count: int(v)})
	}
	sort.Slice(rep.Pairs, func(i, j int) bool {
		if rep.Pairs[i].Winner != rep.Pairs[j].Winner {
			return rep.Pairs[i].Winner < rep.Pairs[j].Winner
		}
		return rep.Pairs[i].Loser < rep.Pairs[j].Loser
	})
	if len(tm.mc) > 0 {
		mc := summarize("all Monte Carlo", tm.mc)
		rep.MonteCarlo = &mc
		total := 0
		for _, r := range tm.mcRounds {
			total += r
		}
		rep.MCAvgRounds = float64(total) / float64(len(tm.mcRounds))
		rep.MCKeptHeur = float64(tm.mcKept) / float64(len(tm.mc))
		hardSeats := 0
		for _, l := range levels {
			if l == hardai.Hard || l == hardai.Expert || l == hardai.Grandmaster {
				hardSeats++
			}
		}
		if played > 0 && hardSeats > 0 {
			rep.MCPerGame = float64(len(tm.mc)) / float64(played*hardSeats)
		}
		rep.MCByPhase = tm.byPhase
		rep.MCOverrides = tm.overrides
	}
	return rep
}

// wilson is the Wilson score interval at 95%.
func wilson(k, n int) (p, lo, hi float64) {
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

// eloFit fits Bradley-Terry strengths to the pairwise wins (MM algorithm)
// and maps them to Elo with Normal (or the first level present) at 1500.
func eloFit(levels []hardai.Level, pair map[[2]hardai.Level]float64) map[string]float64 {
	var ls []hardai.Level
	seen := map[hardai.Level]bool{}
	for _, l := range levels {
		if !seen[l] {
			seen[l] = true
			ls = append(ls, l)
		}
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i] < ls[j] })
	out := map[string]float64{}
	if len(ls) == 1 {
		out[ls[0].String()] = 1500
		return out
	}
	strength := make([]float64, len(ls))
	for i := range strength {
		strength[i] = 1
	}
	for iter := 0; iter < 2000; iter++ {
		next := make([]float64, len(ls))
		for i := range ls {
			wins, denom := 0.0, 0.0
			for j := range ls {
				if i == j {
					continue
				}
				wij := pair[[2]hardai.Level{ls[i], ls[j]}]
				wji := pair[[2]hardai.Level{ls[j], ls[i]}]
				wins += wij
				if games := wij + wji; games > 0 {
					denom += games / (strength[i] + strength[j])
				}
			}
			// A half-win prior keeps unbeaten levels finite.
			next[i] = (wins + 0.5) / (denom + 1/(strength[i]+1))
		}
		strength = next
	}
	anchor := 0
	for i, l := range ls {
		if l == hardai.Normal {
			anchor = i
		}
	}
	for i, l := range ls {
		out[l.String()] = 1500 + 400*math.Log10(strength[i]/strength[anchor])
	}
	return out
}

func summarize(label string, ds []time.Duration) timingReport {
	tr := timingReport{Level: label, Decisions: len(ds)}
	if len(ds) == 0 {
		return tr
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	q := func(p float64) float64 {
		idx := int(math.Ceil(p*float64(len(sorted)))) - 1
		return float64(sorted[max(0, min(len(sorted)-1, idx))].Nanoseconds()) / 1e6
	}
	tr.P50ms, tr.P95ms, tr.MaxMs = q(0.5), q(0.95), q(1)
	return tr
}

func (r *report) print(out *os.File) {
	c := r.Config
	rules := c.Rules
	if rules == "" {
		rules = "classic"
	}
	fmt.Fprintf(out, "Estate arena: %d games, seats %s, rules %s, seed %d, budget %s, parallel %d, workers/decision %d\n",
		r.Games, c.Seats, rules, c.Seed, c.Budget, c.Parallel, c.Workers)
	fmt.Fprintf(out, "finished %d, adjudicated at %d turns %d, errors %d; avg %.1f turns (%.1f rounds), %.0f actions; %.1fs wall (%.1f games/min)\n\n",
		r.Finished, c.MaxTurns, r.Adjudicated, r.Errors, r.AvgTurns, r.AvgRounds, r.AvgSteps, r.WallSeconds, r.GamesPerMinute)
	if r.AvgLoans > 0 {
		fmt.Fprintf(out, "loans accepted: %.2f per game\n\n", r.AvgLoans)
	}
	if r.FirstError != "" {
		fmt.Fprintf(out, "first error: %s\n\n", r.FirstError)
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "seat\tlevel\twins\twin %\t95% CI\toutright\tadjudicated\tbankrupt\trejected\t")
	for _, s := range r.Seats {
		fmt.Fprintf(w, "%d\t%s\t%d\t%.1f\t%.1f-%.1f\t%d\t%d\t%d\t%d\t\n", s.Entry, s.Level, s.Wins, 100*s.WinRate,
			100*s.CILow, 100*s.CIHigh, s.Outright, s.Adjudicated, s.Bankrupt, s.Rejected)
	}
	w.Flush()
	fmt.Fprintln(out)
	var names []string
	for name := range r.Elo {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprint(out, "Elo (Bradley-Terry, normal = 1500):")
	for _, name := range names {
		fmt.Fprintf(out, "  %s %.0f", name, r.Elo[name])
	}
	fmt.Fprint(out, "\n\n")
	t := r.Trades
	fmt.Fprintf(out, "trades: %.1f offers per game, %.1f accepted (%.0f%%); one deal to one target at most %.1f times per game on average (worst game %d)\n",
		t.OffersPerGame, t.AcceptedPerGame, 100*t.AcceptRate, t.SameDealAvg, t.SameDealMax)
	if len(t.SameDealByLevel) > 0 {
		fmt.Fprint(out, "worst repeats of one deal, by proposer:")
		for _, k := range sortedKeys(toFloat(t.SameDealByLevel)) {
			fmt.Fprintf(out, "  %s %d", k, t.SameDealByLevel[k])
		}
		fmt.Fprintln(out)
	}
	if len(t.AcceptedByPair) > 0 {
		fmt.Fprint(out, "accepted per game, proposer>target:")
		for _, k := range sortedKeys(t.AcceptedByPair) {
			fmt.Fprintf(out, "  %s %.2f", k, t.AcceptedByPair[k])
		}
		fmt.Fprintln(out)
	}
	if len(t.DeedsAtEnd) > 0 {
		fmt.Fprint(out, "deeds held at the end, per seat:")
		for _, k := range sortedKeys(t.DeedsAtEnd) {
			fmt.Fprintf(out, "  %s %.1f", k, t.DeedsAtEnd[k])
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out)
	w = tabwriter.NewWriter(out, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "decision timing\tcount\tp50 ms\tp95 ms\tmax ms\t")
	rows := append(append([]timingReport(nil), r.Timing...), r.MCByLevel...)
	for _, t := range rows {
		fmt.Fprintf(w, "%s\t%d\t%.3f\t%.3f\t%.1f\t\n", t.Level, t.Decisions, t.P50ms, t.P95ms, t.MaxMs)
	}
	w.Flush()
	if r.MonteCarlo != nil {
		var phases []string
		for p, k := range r.MCByPhase {
			phases = append(phases, fmt.Sprintf("%s %d", p, k))
		}
		sort.Strings(phases)
		fmt.Fprintf(out, "\nMonte Carlo: %.1f decisions per Hard seat per game, %.1f rounds each on average, heuristic choice kept %.0f%%; by phase: %s\n",
			r.MCPerGame, r.MCAvgRounds, 100*r.MCKeptHeur, strings.Join(phases, ", "))
		printOverrides(out, r.MCOverrides)
	}
}

// printOverrides lists the most frequent choices Hard made instead of the
// heuristic's.
func printOverrides(out *os.File, overrides map[string]int) {
	if len(overrides) == 0 {
		return
	}
	type entry struct {
		key   string
		count int
	}
	var list []entry
	for k, v := range overrides {
		list = append(list, entry{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].count != list[j].count {
			return list[i].count > list[j].count
		}
		return list[i].key < list[j].key
	})
	fmt.Fprintln(out, "Choices that overrode the heuristic (most frequent):")
	for i, e := range list {
		if i == 12 {
			break
		}
		fmt.Fprintf(out, "  %6d  %s\n", e.count, e.key)
	}
}

func toFloat(m map[string]int) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = float64(v)
	}
	return out
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ------------------------------------------------------------- -combine

// combinedGame is one game of a -combine input: the level of every seat and
// the winning seat.
type combinedGame struct {
	levels []hardai.Level
	winner int
}

// combineReports fits one Bradley-Terry ladder over several -json reports
// (winner beats each loser of another level, as in a single run), with 95%
// intervals from a bootstrap that resamples the games of every report.
func combineReports(out *os.File, paths []string, resamples int) error {
	var files [][]combinedGame
	var all []hardai.Level
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var rep report
		if err := json.Unmarshal(data, &rep); err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		levels, err := parseSeats(rep.Config.Seats)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		if len(rep.Winners) == 0 {
			return fmt.Errorf("%s: no per-game winners (made by an older arena)", path)
		}
		var games []combinedGame
		for _, w := range rep.Winners {
			if w >= 0 && w < len(levels) {
				games = append(games, combinedGame{levels: levels, winner: w})
			}
		}
		files = append(files, games)
		all = append(all, levels...)
		fmt.Fprintf(out, "%-40s %5d games  seats %s\n", path, len(games), rep.Config.Seats)
	}
	fit := func(sample [][]combinedGame) map[string]float64 {
		pair := map[[2]hardai.Level]float64{}
		for _, games := range sample {
			for _, g := range games {
				for e, l := range g.levels {
					if e != g.winner && l != g.levels[g.winner] {
						pair[[2]hardai.Level{g.levels[g.winner], l}]++
					}
				}
			}
		}
		return eloFit(all, pair)
	}
	point := fit(files)
	rng := uint64(0x9e3779b97f4a7c15)
	next := func(n int) int {
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return int(rng % uint64(n))
	}
	boot := map[string][]float64{}
	for b := 0; b < resamples; b++ {
		sample := make([][]combinedGame, len(files))
		for f, games := range files {
			sample[f] = make([]combinedGame, len(games))
			for i := range games {
				sample[f][i] = games[next(len(games))]
			}
		}
		for k, v := range fit(sample) {
			boot[k] = append(boot[k], v)
		}
	}
	fmt.Fprintf(out, "\nElo ladder (Bradley-Terry over all games, normal = 1500; 95%% bootstrap interval, %d resamples):\n", resamples)
	names := make([]string, 0, len(point))
	for k := range point {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool { return point[names[i]] < point[names[j]] })
	prev := math.NaN()
	for _, k := range names {
		vs := boot[k]
		sort.Float64s(vs)
		lo, hi := point[k], point[k]
		if len(vs) > 0 {
			lo, hi = vs[int(0.025*float64(len(vs)))], vs[min(len(vs)-1, int(0.975*float64(len(vs))))]
		}
		step := ""
		if !math.IsNaN(prev) {
			step = fmt.Sprintf("  (+%.0f)", point[k]-prev)
		}
		fmt.Fprintf(out, "  %-7s %5.0f  [%4.0f, %4.0f]%s\n", k, point[k], lo, hi, step)
		prev = point[k]
	}
	return nil
}

// Expert overrides (-expert-net, -expert-params, -opponent-params).
var (
	expertNet      *valuenet.Net
	gmNet          *valuenet2.Net
	expertParams   *ai.Params
	opponentParams *ai.Params
	hardParams     *ai.Params
)

func loadExpertOptions(cfg config) error {
	if cfg.GMNet != "" {
		net, err := valuenet2.Load(cfg.GMNet)
		if err != nil {
			return err
		}
		gmNet = net
	}
	if cfg.ExpertNet != "" {
		net, err := valuenet.Load(cfg.ExpertNet)
		if err != nil {
			return err
		}
		expertNet = net
	}
	load := func(path string) (*ai.Params, error) {
		if path == "" {
			return nil, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		p := ai.DefaultParams
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("%s: %v", path, err)
		}
		return &p, nil
	}
	var err error
	if expertParams, err = load(cfg.ExpertPar); err != nil {
		return err
	}
	if opponentParams, err = load(cfg.OppParams); err != nil {
		return err
	}
	hardParams, err = load(cfg.HardParams)
	return err
}
