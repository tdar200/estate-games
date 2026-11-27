package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/hardai"
)

// ------------------------------------------------------------------- speed

func cmdSpeed(args []string) {
	fs := flag.NewFlagSet("speed", flag.ExitOnError)
	parallel := fs.Int("parallel", 10, "goroutines for the source games")
	quotes := fs.Int("quotes", 0, "only time Grandmaster's own price (QuoteDeal, production settings) on up to this many deal shapes per position and player")
	workers := fs.Int("workers", 10, "-quotes: cores per quote (the server uses all of them)")
	games := fs.Int("games", 2, "source games")
	fs.Parse(args)
	positions := collectPositions(sourceConfig{level: hardai.Expert, games: *games, seed: 99, parallel: *parallel, perStage: 1, newBot: defaultNewBot})
	fmt.Printf("%d positions\n", len(positions))
	if *quotes > 0 {
		timeQuotes(positions, *quotes, *workers)
		return
	}
	for _, p := range positions {
		var target string
		for _, pl := range p.g.Players {
			if pl.ID != p.attacker && !pl.Bankrupt {
				target = pl.ID
				break
			}
		}
		ss := structures(p.g, p.attacker, target)
		bot := levelBot(hardai.Expert, 1)
		start := time.Now()
		calls := 0
		for _, s := range ss {
			for _, net := range s.ladder[:min(3, len(s.ladder))] {
				answer(p.g, p.attacker, offerAction(target, s.offer, s.request, net), bot)
				calls++
			}
		}
		perAnswer := time.Since(start) / time.Duration(max(1, calls))
		fmt.Printf("%s %d players: %d shapes; expert answer %v each (%d calls)\n", p.stage, len(p.g.Players), len(ss), perAnswer, calls)
		if len(ss) == 0 {
			continue
		}
		g := hardai.Clone(p.g)
		s := ss[0]
		engine.PerformAction(g, p.attacker, offerAction(target, s.offer, s.request, s.ladder[0]), nil)
		for _, rounds := range []int{1, 3, 6, 12, -1} {
			o := hardai.NewOracle(hardai.OracleOptions{Rollouts: 64, Rounds: rounds, Workers: 1, Seed: 5})
			start := time.Now()
			res := o.Evaluate(g, target, []hardai.OracleOption{
				{Actions: []engine.Action{{Type: engine.ActionRejectTrade}}},
				{Actions: []engine.Action{{Type: engine.ActionAcceptTrade}}},
			})
			fmt.Printf("   oracle rounds %3d: %v per 64 paired rollouts; diff %+.3f se %.3f (se at 512: %.3f)\n", rounds,
				time.Since(start).Round(time.Millisecond), res.Diff[1], res.SE[1], res.SE[1]/math.Sqrt(8))
		}
	}
}

// timeQuotes times Grandmaster's QuoteDeal as the server runs it: a fresh
// production bot per position (2.5 s cap, the given cores), the attacker
// asking every other player about up to n deal shapes.
func timeQuotes(positions []position, n, workers int) {
	var times []time.Duration
	priced := 0
	for i, p := range positions {
		bot := hardai.NewGrandmasterBot(int64(i+1), hardai.Options{Workers: workers})
		for _, pl := range p.g.Players {
			if pl.ID == p.attacker || pl.Bankrupt {
				continue
			}
			ss := structures(p.g, p.attacker, pl.ID)
			for _, s := range ss[:min(n, len(ss))] {
				start := time.Now()
				_, ok := bot.QuoteDeal(hardai.Clone(p.g), pl.ID, p.attacker, s.offer, s.request)
				times = append(times, time.Since(start))
				if ok {
					priced++
				}
			}
		}
	}
	if len(times) == 0 {
		fmt.Println("no quotes")
		return
	}
	sort.Slice(times, func(a, b int) bool { return times[a] < times[b] })
	q := func(f float64) time.Duration {
		return times[min(len(times)-1, int(f*float64(len(times))))].Round(time.Millisecond)
	}
	fmt.Printf("Grandmaster quotes on %d cores: %d asked, %d priced; p50 %v, p90 %v, p95 %v, max %v\n",
		workers, len(times), priced, q(0.5), q(0.9), q(0.95), times[len(times)-1].Round(time.Millisecond))
}

// ------------------------------------------------------------------- noise

// cmdNoise measures the oracle's own noise: the same deals judged by two
// oracles with independent rollout seeds, and the agreement of short
// (network-scored) and whole-game rollouts.
func cmdNoise(args []string) {
	fs := flag.NewFlagSet("noise", flag.ExitOnError)
	games := fs.Int("games", 4, "source games")
	parallel := fs.Int("parallel", 10, "goroutines")
	rollouts := fs.Int("rollouts", 384, "oracle rollouts")
	rounds := fs.Int("rounds", 6, "oracle rollout rounds")
	full := fs.Int("full", 0, "also judge with this many whole-game rollouts (0 = skip)")
	fullTrades := fs.Bool("full-trades", false, "whole-game rollouts play the heuristic's full trade search")
	fullMax := fs.Int("full-maxturns", 0, "cap whole-game rollouts at this many turns past the position (0 = none)")
	mid := fs.Int("mid", 0, "also judge with rollouts of this many rounds (and the network), same rollout count")
	midFull := fs.Bool("mid-full-trades", false, "the -mid oracle plays the heuristic's full trade search")
	midRollouts := fs.Int("mid-rollouts", 512, "rollouts of the -mid oracle")
	skipFirst := fs.Bool("skip-pair", false, "skip the second short oracle (noise estimate)")
	seed := fs.Int64("seed", 4242, "seed of the source games")
	maxItems := fs.Int("items", 120, "deals judged")
	fs.Parse(args)
	positions := collectPositions(sourceConfig{level: hardai.Expert, games: *games, seed: *seed, parallel: *parallel, perStage: 1, newBot: defaultNewBot})
	// Deals: the server's quote for every shape (cheap to obtain, varied).
	var items []*judged
	for i, p := range positions {
		for _, pl := range p.g.Players {
			if pl.ID == p.attacker || pl.Bankrupt {
				continue
			}
			for _, s := range structures(p.g, p.attacker, pl.ID) {
				if net, ok := quote(p.g, p.attacker, pl.ID, s.offer, s.request); ok {
					items = append(items, &judged{Pos: i, Target: pl.ID, Attacker: p.attacker, Offer: s.offer, Request: s.request, Net: net, CompletesA: s.completesA})
				}
			}
		}
	}
	// Keep a spread: every completing deal, then others.
	sort.SliceStable(items, func(i, j int) bool { return items[i].CompletesA && !items[j].CompletesA })
	if len(items) > *maxItems {
		items = items[:*maxItems]
	}
	fmt.Printf("%d deals\n", len(items))
	copyItems := func() []*judged {
		out := make([]*judged, len(items))
		for i, it := range items {
			c := *it
			out[i] = &c
		}
		return out
	}
	a, b := copyItems(), copyItems()
	judge(positions, a, *rollouts, *rounds, 1, *parallel)
	if !*skipFirst {
		judge(positions, b, *rollouts, *rounds, 2, *parallel)
	}
	var d2, se2 float64
	n := 0
	for i := range a {
		if *skipFirst {
			break
		}
		if math.IsNaN(a[i].Delta) || math.IsNaN(b[i].Delta) {
			continue
		}
		d := a[i].Delta - b[i].Delta
		d2 += d * d
		se2 += a[i].SE*a[i].SE + b[i].SE*b[i].SE
		n++
	}
	fmt.Printf("two independent oracles (%d rollouts, %d rounds): RMS difference %.2fpp, so one verdict's noise is %.2fpp (SD); mean reported SE %.2fpp\n",
		*rollouts, *rounds, 100*math.Sqrt(d2/float64(n)), 100*math.Sqrt(d2/float64(n)/2), 100*math.Sqrt(se2/float64(2*n)))
	if *mid > 0 {
		m := copyItems()
		judgeWith(positions, m, hardai.OracleOptions{Rollouts: *midRollouts, Rounds: *mid, Seed: 4 ^ 0x0ac1e, FullTrades: *midFull}, *parallel)
		fmt.Printf("%d-round oracle against the %d-round one:\n", *mid, *rounds)
		compareOracles(fmt.Sprintf("%d rounds", *mid), a, m)
	}
	if *full > 0 {
		c := copyItems()
		judgeWith(positions, c, hardai.OracleOptions{Rollouts: *full, Rounds: -1, Seed: 3, FullTrades: *fullTrades, MaxTurns: *fullMax}, *parallel)
		var sx, sy, sxx, syy, sxy float64
		agree, both := 0, 0
		for i := range a {
			x, y := (a[i].Delta+b[i].Delta)/2, c[i].Delta
			if math.IsNaN(x) || math.IsNaN(y) {
				continue
			}
			sx += x
			sy += y
			sxx += x * x
			syy += y * y
			sxy += x * y
			both++
			if (x <= -0.02) == (y <= -0.02) {
				agree++
			}
		}
		nf := float64(both)
		r := (sxy - sx*sy/nf) / math.Sqrt((sxx-sx*sx/nf)*(syy-sy*sy/nf))
		var fse float64
		for _, it := range c {
			fse += it.SE
		}
		fmt.Printf("whole-game oracle (%d rollouts): correlation with the %d-round oracle r = %.2f; mean SE %.2fpp; agree on the 2pp line %d/%d; mean %.2fpp vs %.2fpp\n",
			*full, *rounds, r, 100*fse/float64(len(c)), agree, both, 100*sy/nf, 100*sx/nf)
	}
}

// --------------------------------------------------------------- decisions

// auditItem is one decision of the level's bot in a source game, judged by
// the oracle.
type auditItem struct {
	Kind    string  `json:"kind"` // purchase, auction, proposal
	Source  int     `json:"source"`
	Players int     `json:"players"`
	Turn    int     `json:"turn"`
	Choice  string  `json:"choice"`
	Regret  float64 `json:"regret"` // oracle: best option minus the bot's (win probability)
	SE      float64 `json:"se"`
	Detail  string  `json:"detail"`
	// auction: the bot's ceiling, the oracle's value of winning at each grid price
	Ceiling int       `json:"ceiling,omitempty"`
	Grid    []int     `json:"grid,omitempty"`
	Values  []float64 `json:"values,omitempty"`
	// proposal: proposer's and target's change if accepted
	Gain       float64 `json:"gain,omitempty"`
	TargetGain float64 `json:"targetGain,omitempty"`
	Accepted   bool    `json:"accepted,omitempty"`

	g      *engine.Game
	me     string
	action engine.Action
}

// auditRecorder collects decisions of the level's seats.
type auditRecorder struct {
	mu    sync.Mutex
	items []*auditItem
	limit map[[2]int]int // (game, kind) -> recorded
	max   int
}

type auditBot struct {
	inner hardai.Bot
	rec   *auditRecorder
	game  int
	last  hardai.DecisionInfo
	seen  map[string]bool // auctions already recorded
}

func (b *auditBot) Choose(g *engine.Game, id string) (engine.Action, bool) {
	var before *engine.Game
	kind := ""
	switch g.Phase {
	case engine.PhaseAwaitingPurchase:
		if cur := engine.CurrentPlayer(g); cur != nil && cur.ID == id && g.PendingPurchase != nil && cur.Cash >= engine.Board[*g.PendingPurchase].Price {
			kind = "purchase"
		}
	case engine.PhaseAuction:
		key := fmt.Sprint(totalTurns(g), g.Auction.SpaceIndex)
		if !b.seen[key] {
			b.seen[key] = true
			kind = "auction"
		}
	case engine.PhaseAwaitingRoll, engine.PhaseTurnComplete:
		kind = "proposal?"
	}
	if kind != "" {
		before = hardai.Clone(g)
	}
	b.last = hardai.DecisionInfo{}
	a, ok := b.inner.Choose(g, id)
	if kind == "proposal?" {
		if ok && a.Type == engine.ActionOfferTrade {
			kind = "proposal"
		} else {
			kind = ""
		}
	}
	if kind == "" || !ok {
		return a, ok
	}
	it := &auditItem{Kind: kind, Source: b.game, Players: len(g.Players), Turn: totalTurns(g), Choice: a.Type, g: before, me: id, action: a}
	if kind == "auction" {
		var c int
		if _, err := fmt.Sscanf(b.last.Chosen, "ceiling %d", &c); err == nil {
			it.Ceiling = c
		} else {
			it.Ceiling = -1 // heuristic ceiling (too few rounds): not recorded
		}
	}
	b.rec.add(it)
	return a, ok
}

func (r *auditRecorder) add(it *auditItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := [2]int{it.Source, map[string]int{"purchase": 0, "auction": 1, "proposal": 2}[it.Kind]}
	if r.limit[key] >= r.max {
		return
	}
	r.limit[key]++
	r.items = append(r.items, it)
}

func cmdDecisions(args []string) {
	fs := flag.NewFlagSet("decisions", flag.ExitOnError)
	levelName := fs.String("level", "expert", "level audited")
	games := fs.Int("games", 12, "source games")
	perGame := fs.Int("per-game", 8, "decisions of each kind kept per game")
	seed := fs.Int64("seed", 777, "seed")
	parallel := fs.Int("parallel", 10, "goroutines")
	rollouts := fs.Int("rollouts", 384, "oracle rollouts")
	rounds := fs.Int("rounds", 6, "oracle rollout rounds")
	out := fs.String("out", "", "JSON lines output")
	fs.Parse(args)
	level, err := hardai.ParseLevel(*levelName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	rec := &auditRecorder{limit: map[[2]int]int{}, max: *perGame}
	collectPositions(sourceConfig{level: level, games: *games, seed: *seed, parallel: *parallel, perStage: 0, newBot: defaultNewBot,
		audit: func(game, seat int) (func(hardai.DecisionInfo), func(hardai.Bot) hardai.Bot) {
			ab := &auditBot{rec: rec, game: game, seen: map[string]bool{}}
			return func(info hardai.DecisionInfo) {
					if info.MonteCarlo {
						ab.last = info
					}
				}, func(inner hardai.Bot) hardai.Bot {
					ab.inner = inner
					return ab
				}
		}})
	items := rec.items
	fmt.Fprintf(os.Stderr, "%d decisions recorded\n", len(items))
	oracles := make(chan *hardai.Oracle, *parallel)
	for i := 0; i < *parallel; i++ {
		oracles <- hardai.NewOracle(hardai.OracleOptions{Rollouts: *rollouts, Rounds: *rounds, Workers: 1, Seed: uint64(*seed)})
	}
	parallelDo(len(items), *parallel, "oracle", func(i int) {
		o := <-oracles
		defer func() { oracles <- o }()
		auditOne(o, items[i])
	})
	printAudit(items, *levelName)
	writeJSONL(*out, items)
}

func auditOne(o *hardai.Oracle, it *auditItem) {
	g := it.g
	switch it.Kind {
	case "purchase":
		res := o.Evaluate(g, it.me, []hardai.OracleOption{
			{Name: "buy", Actions: []engine.Action{{Type: engine.ActionBuy}}},
			{Name: "decline", Actions: []engine.Action{{Type: engine.ActionDecline}}},
		})
		idx := 0
		if it.Choice == engine.ActionDecline {
			idx = 1
		}
		best := math.Max(res.Mean[0], res.Mean[1])
		it.Regret = best - res.Mean[idx]
		it.SE = res.SE[1]
		it.Detail = fmt.Sprintf("%s %s ($%d): buy - decline = %+.1fpp", it.Choice, engine.Board[*g.PendingPurchase].Name,
			engine.Board[*g.PendingPurchase].Price, 100*res.Diff[1]*-1)
	case "auction":
		a := g.Auction
		p := g.PlayerByID(it.me)
		listPrice := engine.Board[a.SpaceIndex].Price
		var grid []int
		for _, f := range []float64{0.25, 0.5, 0.75, 1, 1.25, 1.5, 2, 2.5} {
			x := round10(f * float64(listPrice))
			if x <= p.Cash && x > a.CurrentBid {
				grid = append(grid, x)
			}
		}
		if it.Ceiling > 0 && it.Ceiling <= p.Cash && !containsInt(grid, it.Ceiling) {
			grid = append(grid, it.Ceiling)
			sort.Ints(grid)
		}
		opts := []hardai.OracleOption{{Name: "pass", Actions: []engine.Action{{Type: engine.ActionPassAuction}}}}
		for _, x := range grid {
			x := x
			opts = append(opts, hardai.OracleOption{Name: fmt.Sprint(x), Apply: func(sim *engine.Game, me string) bool {
				return hardai.WinAuction(sim, me, x)
			}})
		}
		res := o.Evaluate(g, it.me, opts)
		it.Grid = grid
		it.Values = res.Diff[1:]
		// Overbid: winning at its own ceiling loses >= 2pp against passing.
		// Underbid: some price above its ceiling is still worth >= 2pp.
		it.Regret = 0
		for k, x := range grid {
			v := res.Diff[k+1]
			if it.Ceiling >= 0 && x == it.Ceiling && v < 0 {
				it.Regret = math.Max(it.Regret, -v)
			}
			if it.Ceiling >= 0 && x > it.Ceiling && v > 0 {
				it.Regret = math.Max(it.Regret, v)
			}
		}
		it.SE = 0
		for _, se := range res.SE[1:] {
			it.SE = math.Max(it.SE, se)
		}
		it.Detail = fmt.Sprintf("%s ($%d): ceiling $%d", engine.Board[a.SpaceIndex].Name, listPrice, it.Ceiling)
	case "proposal":
		t := it.action
		res := o.Evaluate(g, it.me, []hardai.OracleOption{
			{Name: "no offer", Apply: func(sim *engine.Game, me string) bool { return true }},
			{Name: "deal", Apply: func(sim *engine.Game, me string) bool {
				if engine.PerformAction(sim, me, t, nil) != nil {
					return false
				}
				return engine.PerformAction(sim, t.TargetID, engine.Action{Type: engine.ActionAcceptTrade}, nil) == nil
			}},
		})
		it.Gain = res.Diff[1]
		it.SE = res.SE[1]
		it.Regret = math.Max(0, -res.Diff[1])
		for j, p := range g.Players {
			if p.ID == t.TargetID {
				it.TargetGain = res.Others[1][j] - res.Others[0][j]
			}
		}
		it.Detail = fmt.Sprintf("gives %s + $%d, gets %s + $%d", names(t.Offer.Properties), t.Offer.Cash, names(t.Request.Properties), t.Request.Cash)
	}
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func printAudit(items []*auditItem, level string) {
	fmt.Printf("\n== Own decisions of %s (oracle regret: best option minus its choice)\n", level)
	for _, kind := range []string{"purchase", "auction", "proposal"} {
		n, bad, sum, se := 0, 0, 0.0, 0.0
		for _, it := range items {
			if it.Kind != kind || (kind == "auction" && it.Ceiling < 0) {
				continue
			}
			n++
			sum += it.Regret
			se += it.SE
			if it.Regret >= 0.02 {
				bad++
			}
		}
		if n == 0 {
			continue
		}
		p, lo, hi := wilson(bad, n)
		fmt.Printf("%-9s %4d judged; regret >= 2pp: %s; mean regret %.2fpp; mean oracle SE %.2fpp\n", kind, n, pct(p, lo, hi), 100*sum/float64(n), 100*se/float64(n))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Regret > items[j].Regret })
	fmt.Println("worst:")
	for i := 0; i < len(items) && i < 10; i++ {
		it := items[i]
		extra := ""
		if it.Kind == "proposal" {
			extra = fmt.Sprintf(" (its gain %+.1fpp, target %+.1fpp)", 100*it.Gain, 100*it.TargetGain)
		}
		fmt.Printf("  regret %5.1fpp ±%.1f  %-8s %d players turn %d: %s%s\n", 100*it.Regret, 100*it.SE, it.Kind, it.Players, it.Turn, it.Detail, extra)
	}
}

// ------------------------------------------------------------- clairvoyant

func cmdClairvoyant(args []string) {
	fs := flag.NewFlagSet("clairvoyant", flag.ExitOnError)
	games := fs.Int("games", 200, "games")
	seats := fs.String("opponents", "expert,expert,expert", "the other seats")
	parallel := fs.Int("parallel", 10, "games at once")
	seed := fs.Int64("seed", 20261103, "seed")
	horizon := fs.Int("horizon", 0, "clairvoyant rollout rounds (0 = default)")
	tunedOpp := fs.Bool("tuned-opponents", true, "model the other seats with Expert's tuned knobs")
	honest := fs.Bool("honest", false, "control: the same seat plays Expert (no peeking)")
	jsonPath := fs.String("json", "", "write the result")
	fs.Parse(args)
	var opp []hardai.Level
	for _, s := range splitComma(*seats) {
		l, err := hardai.ParseLevel(s)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		opp = append(opp, l)
	}
	n := len(opp) + 1
	var wins, played atomic.Int64
	var turns atomic.Int64
	start := time.Now()
	parallelDo(*games, *parallel, "clairvoyant", func(i int) {
		seat := i % n
		levels := make([]hardai.Level, 0, n)
		for s, k := 0, 0; s < n; s++ {
			if s == seat {
				levels = append(levels, hardai.Expert)
			} else {
				levels = append(levels, opp[k])
				k++
			}
		}
		res, err := hardai.PlayMatch(hardai.MatchConfig{Levels: levels, Seed: *seed + int64(i), MaxTurns: 1000,
			NewBotPeek: func(s int, level hardai.Level, bseed int64, peek func() engine.RNG) hardai.Bot {
				opts := hardai.Options{Budget: -1, Workers: 1}
				if s == seat && !*honest {
					opts.Horizon = *horizon
					if *tunedOpp {
						opts.OpponentParams = hardai.ExpertParams()
					}
					return hardai.NewClairvoyantBot(bseed, opts, peek)
				}
				return hardai.NewBot(level, bseed, opts)
			}})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		played.Add(1)
		turns.Add(int64(res.Turns))
		if res.Seats[seat].Won {
			wins.Add(1)
		}
	})
	p, lo, hi := wilson(int(wins.Load()), int(played.Load()))
	label := "clairvoyant"
	if *honest {
		label = "expert (control)"
	}
	fmt.Printf("%s vs %s: %d games, wins %s; equal share %.1f%%; avg %.0f turns; %s\n", label, *seats, played.Load(), pct(p, lo, hi),
		100/float64(n), float64(turns.Load())/float64(max(1, played.Load())), time.Since(start).Round(time.Second))
	if *jsonPath != "" {
		data, _ := json.MarshalIndent(map[string]any{"label": label, "opponents": *seats, "games": played.Load(), "wins": wins.Load(),
			"winRate": p, "ci95Low": lo, "ci95High": hi, "seed": *seed}, "", "  ")
		os.WriteFile(*jsonPath, data, 0o644)
	}
}

func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}

// compareOracles prints how two oracles' verdicts differ by deal kind.
func compareOracles(label string, a, c []*judged) {
	type acc struct {
		n, agree int
		sa, sc   float64
	}
	groups := map[string]*acc{}
	for i := range a {
		x, y := a[i].Delta, c[i].Delta
		if math.IsNaN(x) || math.IsNaN(y) {
			continue
		}
		key := "not completing"
		if a[i].CompletesA {
			key = "completing the human's group"
		}
		for _, k := range []string{"all", key} {
			g := groups[k]
			if g == nil {
				g = &acc{}
				groups[k] = g
			}
			g.n++
			g.sa += x
			g.sc += y
			if (x <= -0.02) == (y <= -0.02) {
				g.agree++
			}
		}
	}
	for _, k := range []string{"all", "completing the human's group", "not completing"} {
		if g := groups[k]; g != nil {
			fmt.Printf("  %-30s n=%3d  mean %+.2fpp (short) vs %+.2fpp (%s); agree on the 2pp line %d/%d\n", k, g.n,
				100*g.sa/float64(g.n), 100*g.sc/float64(g.n), label, g.agree, g.n)
		}
	}
	idx := make([]int, len(a))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool {
		return math.Abs(a[idx[i]].Delta-c[idx[i]].Delta) > math.Abs(a[idx[j]].Delta-c[idx[j]].Delta)
	})
	fmt.Println("  largest disagreements:")
	for _, i := range idx[:min(8, len(idx))] {
		fmt.Printf("   short %+5.1fpp  %s %+5.1fpp ±%.1f  net $%d  completesA %v completesT %v: gives %s gets %s\n", 100*a[i].Delta, label,
			100*c[i].Delta, 100*c[i].SE, a[i].Net, a[i].CompletesA, a[i].CompletesT, names(a[i].Offer), names(a[i].Request))
	}
}
