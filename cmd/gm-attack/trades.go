package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"sort"
	"strings"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/hardai"
)

// structure is a deal shape: the deeds each side gives and the cash ladder
// the attacker walks (net cash it pays, most favourable to it first). It
// stops at the first offer the target accepts, as a person would.
type structure struct {
	kind       string
	offer      []int // attacker's deeds
	request    []int // target's deeds
	ladder     []int
	completesA bool // the attacker gets a colour group
	completesT bool // the target gets a colour group
}

// judged is one deal the target accepted (or would be made to accept), with
// the oracle's verdict.
type judged struct {
	Pos        int    `json:"pos"`
	Source     int    `json:"source"`
	Stage      string `json:"stage"`
	Players    int    `json:"players"`
	Target     string `json:"target"`
	Attacker   string `json:"attacker"`
	Path       string `json:"path"` // answer (the bot's search), quote (the server's quote), probe (cash +$1..+$10)
	Kind       string `json:"kind"`
	Offer      []int  `json:"offer"`
	Request    []int  `json:"request"`
	Net        int    `json:"net"`      // cash the attacker pays (negative: receives)
	ListGain   int    `json:"listGain"` // list value the attacker gets minus what it gives (deeds and cash)
	CompletesA bool   `json:"completesAttacker"`
	CompletesT bool   `json:"completesTarget"`
	Tries      int    `json:"tries"` // offers of this shape made, this one included
	// LevelAccepts: the level's own answer to the quoted deal (quote path),
	// had the server not accepted it for the computer.
	LevelAccepts bool    `json:"levelAccepts"`
	Weight       float64 `json:"weight"` // 1 / sampling probability
	Delta        float64 `json:"delta"`  // target's win probability, accept minus reject (oracle)
	SE           float64 `json:"se"`
	AttGain      float64 `json:"attackerGain"` // attacker's win probability, accept minus reject
	PTarget      float64 `json:"pTarget"`      // target's win probability if it rejects
	Text         string  `json:"text"`
}

func (j *judged) describe(pos position) {
	g := pos.g
	nameOf := func(id string) string {
		if p := g.PlayerByID(id); p != nil {
			return p.Name
		}
		return id
	}
	cash := ""
	switch {
	case j.Net > 0:
		cash = fmt.Sprintf(" + $%d", j.Net)
	case j.Net < 0:
		cash = fmt.Sprintf(" for $%d", -j.Net)
	}
	gives := names(j.Offer)
	if len(j.Offer) == 0 && j.Net > 0 {
		gives = fmt.Sprintf("$%d", j.Net)
		cash = ""
	}
	j.Text = fmt.Sprintf("[%s, %d players, turn %d] human %s gives %s%s, gets %s from %s", j.Stage, j.Players,
		totalTurns(g), nameOf(j.Attacker), gives, cash, names(j.Request), nameOf(j.Target))
	if j.CompletesA {
		j.Text += " (completes the human's colour group)"
	}
	if j.CompletesT {
		j.Text += " (completes the computer's colour group)"
	}
}

func totalTurns(g *engine.Game) int {
	n := 0
	for _, c := range g.TurnCounts {
		n += c
	}
	return n
}

// structures lists the deal shapes the attacker tries with target t.
func structures(g *engine.Game, a, t string) []structure {
	A, T := g.PlayerByID(a), g.PlayerByID(t)
	aDeeds, tDeeds := tradeable(g, a), tradeable(g, t)
	var out []structure
	add := func(s structure, fractions []float64, base, shift int) {
		seen := map[int]bool{}
		for _, f := range fractions {
			net := round10(f*float64(base)) + shift
			if net > A.Cash || -net > T.Cash || (net == 0 && (len(s.offer) == 0 || len(s.request) == 0)) {
				continue
			}
			if net <= 0 && len(s.offer) == 0 || net >= 0 && len(s.request) == 0 {
				continue // both sides must give something of value
			}
			if !seen[net] {
				seen[net] = true
				s.ladder = append(s.ladder, net)
			}
		}
		if len(s.ladder) > 0 {
			s.completesA = completes(g, a, s.request, s.offer)
			s.completesT = completes(g, t, s.offer, s.request)
			out = append(out, s)
		}
	}
	buyFractions := []float64{0.3, 0.5, 0.7, 0.85, 1, 1.15, 1.3, 1.5, 1.75, 2, 2.5, 3}
	for _, d := range tDeeds {
		add(structure{kind: "buy", request: []int{d}}, buyFractions, engine.Board[d].Price, 0)
	}
	for _, x := range aDeeds {
		// Selling: the attacker asks the most first.
		add(structure{kind: "sell", offer: []int{x}}, []float64{-3, -2.5, -2, -1.75, -1.5, -1.25, -1, -0.8}, engine.Board[x].Price, 0)
	}
	for _, x := range aDeeds {
		for _, d := range tDeeds {
			diff := engine.Board[d].Price - engine.Board[x].Price
			add(structure{kind: "swap", offer: []int{x}, request: []int{d}}, []float64{-250, -150, -75, 0, 75, 150, 250}, 1, diff)
		}
	}
	// Two of the target's deeds of one group (or two railroads/utilities).
	for i := 0; i < len(tDeeds); i++ {
		for j := i + 1; j < len(tDeeds); j++ {
			d1, d2 := tDeeds[i], tDeeds[j]
			if !sameSet(d1, d2) {
				continue
			}
			add(structure{kind: "buy2", request: []int{d1, d2}}, []float64{0.4, 0.6, 0.8, 1, 1.25, 1.5, 2, 2.5}, price([]int{d1, d2}), 0)
		}
	}
	// Two of the attacker's deeds for one of the target's that completes the
	// attacker's group.
	for _, d := range tDeeds {
		if !completes(g, a, []int{d}, nil) {
			continue
		}
		for i := 0; i < len(aDeeds); i++ {
			for j := i + 1; j < len(aDeeds); j++ {
				gives := []int{aDeeds[i], aDeeds[j]}
				diff := engine.Board[d].Price - price(gives)
				add(structure{kind: "give2", offer: gives, request: []int{d}}, []float64{-150, -50, 0, 50, 150}, 1, diff)
			}
		}
	}
	return out
}

func sameSet(a, b int) bool {
	sa, sb := engine.Board[a], engine.Board[b]
	if sa.Group != "" {
		return sa.Group == sb.Group
	}
	return sa.Type == sb.Type
}

type pairTask struct {
	pos    int
	target string
}

func cmdTrades(args []string) {
	fs := flag.NewFlagSet("trades", flag.ExitOnError)
	levelName := fs.String("level", "expert", "level under attack (its bots play the source games and answer)")
	games := fs.Int("games", 24, "source games")
	perStage := fs.Int("per-stage", 1, "positions per stage and source game")
	seed := fs.Int64("seed", 20261103, "seed")
	parallel := fs.Int("parallel", 10, "goroutines")
	rollouts := fs.Int("rollouts", 384, "oracle: paired rollouts per verdict")
	rounds := fs.Int("rounds", 6, "oracle: rollout length in rounds (negative: whole game)")
	sample := fs.Int("sample", 6, "per position and target: accepted non-completing deals judged (completing ones: all)")
	probe := fs.Bool("probe", true, "also probe refused group-completing deals with +$1..+$10")
	out := fs.String("out", "", "write every judged deal (JSON lines)")
	cross := fs.Int("crosscheck", 0, "re-judge the N worst and N random deals with whole-game rollouts")
	summaryPath := fs.String("summary", "", "grandmaster.json to record the exploit rates in")
	summaryPrefix := fs.String("summary-prefix", "", "result id prefix (x = Expert, g = Grandmaster)")
	crossRollouts := fs.Int("crosscheck-rollouts", 256, "whole-game rollouts per re-judged deal")
	crossFull := fs.Bool("crosscheck-full-trades", false, "cross-check rollouts play the heuristic's full trade search")
	crossRounds := fs.Int("crosscheck-rounds", -1, "cross-check rollout length in rounds (negative: whole game)")
	srcLevel := fs.String("source", "", "level of the bots that play the source games (default: -level)")
	fs.BoolVar(&ownQuoteAll, "ownquote-all", true, "Grandmaster: ask its own price for every deal shape (false: group-completing shapes only)")
	var gmo hardai.GrandmasterOptions
	fs.IntVar(&gmo.TradeHorizon, "gm-trade-horizon", 0, "Grandmaster variant: rounds per trade-answer rollout (0 = default)")
	fs.IntVar(&gmo.TradeRollouts, "gm-trade-rollouts", 0, "Grandmaster variant: rounds per trade answer (0 = default)")
	fs.BoolVar(&gmo.NoStyles, "gm-nostyles", false, "Grandmaster variant: no opponent style mix")
	fs.BoolVar(&gmo.SharpProposer, "gm-sharp", false, "Grandmaster variant: trade answers play the proposer with the tuned heuristic")
	fs.Float64Var(&gmo.Margin, "gm-margin", 0, "Grandmaster variant: accept margin (0 = default)")
	fs.Parse(args)
	level, err := hardai.ParseLevel(*levelName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	src := level
	if gmo != (hardai.GrandmasterOptions{}) {
		gmOverride = &gmo
	}
	if *srcLevel != "" {
		if src, err = hardai.ParseLevel(*srcLevel); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	positions := collectPositions(sourceConfig{level: src, games: *games, seed: *seed, parallel: *parallel, perStage: *perStage, newBot: defaultNewBot})
	fmt.Fprintf(os.Stderr, "%d positions\n", len(positions))
	items, stats := attackTrades(positions, level, *seed, *parallel, *sample, *probe)
	judge(positions, items, *rollouts, *rounds, *seed, *parallel)
	printTradeReport(items, stats, *levelName)
	printAnswerQuality(items)
	writeJSONL(*out, items) // before the cross-check, which can take long
	if *summaryPath != "" && *summaryPrefix != "" {
		recordExploit(*summaryPath, *summaryPrefix, *levelName, items, stats)
	}
	if *cross > 0 {
		crossCheck(positions, items, *cross, *crossRollouts, *crossRounds, *crossFull, *seed, *parallel)
	}
	writeJSONL(*out, items)
}

// attackStats counts what the attack tried.
type attackStats struct {
	pairs, structures, offers, accepted, quoted int
	byKind                                      map[string][2]int // structures tried, accepted
	pairsWithAccept                             int
	completing, completingAccepted              int
	probed, probeFlipped                        int
}

// attackTrades runs the attack and returns the deals to judge.
func attackTrades(positions []position, level hardai.Level, seed int64, parallel, sample int, probe bool) ([]*judged, attackStats) {
	var tasks []pairTask
	for i, p := range positions {
		for _, pl := range p.g.Players {
			if pl.ID != p.attacker && !pl.Bankrupt {
				tasks = append(tasks, pairTask{i, pl.ID})
			}
		}
	}
	// Biggest pairs first (most deal shapes, then most players), so the
	// slowest ones do not run alone at the end.
	size := map[pairTask]int{}
	for _, t := range tasks {
		g := positions[t.pos].g
		size[t] = len(structures(g, positions[t.pos].attacker, t.target))*16 + len(g.Players)
	}
	sort.SliceStable(tasks, func(a, b int) bool { return size[tasks[a]] > size[tasks[b]] })
	results := make([][]*judged, len(tasks))
	stats := make([]attackStats, len(tasks))
	parallelDo(len(tasks), parallel, "attack", func(k int) {
		task := tasks[k]
		pos := positions[task.pos]
		g := pos.g
		st := &stats[k]
		st.pairs = 1
		st.byKind = map[string][2]int{}
		bot := levelBot(level, int64(hashMix(uint64(seed), uint64(task.pos), uint64(len(task.target)), uint64(task.target[len(task.target)-1]))))
		rng := rand.New(rand.NewPCG(uint64(seed), uint64(k)))
		var accepted, others []*judged
		for _, s := range structures(g, pos.attacker, task.target) {
			st.structures++
			kc := st.byKind[s.kind]
			kc[0]++
			if s.completesA {
				st.completing++
			}
			lastRefused := math.MinInt
			got, acc := 0, false
			tries := 0
			for _, net := range s.ladder {
				ok, valid := answer(g, pos.attacker, offerAction(task.target, s.offer, s.request, net), bot)
				if !valid {
					continue
				}
				tries++
				st.offers++
				if ok {
					got, acc = net, true
					break
				}
				lastRefused = net
			}
			mk := func(path string, net, tries int) *judged {
				j := &judged{Pos: task.pos, Source: pos.source, Stage: pos.stage, Players: len(g.Players), Target: task.target,
					Attacker: pos.attacker, Path: path, Kind: s.kind, Offer: s.offer, Request: s.request, Net: net,
					ListGain: price(s.request) - price(s.offer) - net, CompletesA: s.completesA, CompletesT: s.completesT,
					Tries: tries, Weight: 1}
				j.describe(pos)
				return j
			}
			if acc {
				st.accepted++
				kc[1]++
				if s.completesA {
					st.completingAccepted++
				}
				j := mk("answer", got, tries)
				if s.completesA {
					accepted = append(accepted, j)
				} else {
					others = append(others, j)
				}
			} else if probe && s.completesA && lastRefused != math.MinInt {
				// Fresh dice for a dollar more: the answer's rollouts are
				// seeded by the cash, so nudging it buys a new verdict.
				st.probed++
				for k := 1; k <= 10; k++ {
					net := lastRefused + k // the ladder rises in the target's favour
					if net == 0 {
						continue
					}
					ok, valid := answer(g, pos.attacker, offerAction(task.target, s.offer, s.request, net), bot)
					if !valid {
						break
					}
					st.offers++
					if ok {
						st.probeFlipped++
						accepted = append(accepted, mk("probe", net, tries+k))
						break
					}
				}
			}
			st.byKind[s.kind] = kc
			// The server's quote for this shape (the trade screen's ASK chip).
			if net, ok := quote(g, pos.attacker, task.target, s.offer, s.request); ok {
				st.quoted++
				j := mk("quote", net, 1)
				j.LevelAccepts, _ = answer(g, pos.attacker, offerAction(task.target, s.offer, s.request, net), bot)
				if s.completesA {
					accepted = append(accepted, j)
				} else {
					others = append(others, j)
				}
			}
			// A Grandmaster's own price (what the server would honour if it
			// asked the computer instead of the Normal heuristic).
			if hb, ok := bot.(*hardai.HardBot); ok && level == hardai.Grandmaster && (s.completesA || ownQuoteAll) {
				if net, ok := hb.QuoteDeal(g, task.target, pos.attacker, s.offer, s.request); ok {
					j := mk("ownquote", net, 1)
					if s.completesA {
						accepted = append(accepted, j)
					} else {
						others = append(others, j)
					}
				}
			}
		}
		if len(accepted) > 0 || len(others) > 0 {
			st.pairsWithAccept = 1
		}
		// Judge every group-completing deal, and a sample of the others per path.
		results[k] = append(results[k], accepted...)
		byPath := map[string][]*judged{}
		for _, j := range others {
			byPath[j.Path] = append(byPath[j.Path], j)
		}
		for _, path := range []string{"answer", "quote", "ownquote"} {
			list := byPath[path]
			rng.Shuffle(len(list), func(a, b int) { list[a], list[b] = list[b], list[a] })
			keep := min(sample, len(list))
			for _, j := range list[:keep] {
				j.Weight = float64(len(list)) / float64(keep)
				results[k] = append(results[k], j)
			}
		}
	})
	var all []*judged
	var total attackStats
	total.byKind = map[string][2]int{}
	for k := range results {
		all = append(all, results[k]...)
		s := stats[k]
		total.pairs += s.pairs
		total.structures += s.structures
		total.offers += s.offers
		total.accepted += s.accepted
		total.quoted += s.quoted
		total.pairsWithAccept += s.pairsWithAccept
		total.completing += s.completing
		total.completingAccepted += s.completingAccepted
		total.probed += s.probed
		total.probeFlipped += s.probeFlipped
		for kind, v := range s.byKind {
			t := total.byKind[kind]
			total.byKind[kind] = [2]int{t[0] + v[0], t[1] + v[1]}
		}
	}
	return all, total
}

func hashMix(values ...uint64) uint64 {
	h := uint64(0x9e3779b97f4a7c15)
	for _, v := range values {
		h ^= v + 0x9e3779b97f4a7c15 + (h << 6) + (h >> 2)
		h *= 0xff51afd7ed558ccd
		h ^= h >> 33
	}
	return h
}

// judge fills in the oracle's verdict for every deal: the target's win
// probability if it accepts minus if it rejects.
func judge(positions []position, items []*judged, rollouts, rounds int, seed int64, parallel int) {
	judgeWith(positions, items, hardai.OracleOptions{Rollouts: rollouts, Rounds: rounds, Seed: uint64(seed) ^ 0x0ac1e}, parallel)
}

// judgeWith is judge with any oracle settings.
func judgeWith(positions []position, items []*judged, oo hardai.OracleOptions, parallel int) {
	oracles := make(chan *hardai.Oracle, parallel)
	for i := 0; i < parallel; i++ {
		oo.Workers = 1
		oracles <- hardai.NewOracle(oo)
	}
	parallelDo(len(items), parallel, "oracle", func(i int) {
		it := items[i]
		o := <-oracles
		defer func() { oracles <- o }()
		g := hardai.Clone(positions[it.Pos].g)
		if err := engine.PerformAction(g, it.Attacker, offerAction(it.Target, it.Offer, it.Request, it.Net), nil); err != nil {
			it.Delta = math.NaN()
			return
		}
		res := o.Evaluate(g, it.Target, []hardai.OracleOption{
			{Name: "reject", Actions: []engine.Action{{Type: engine.ActionRejectTrade}}},
			{Name: "accept", Actions: []engine.Action{{Type: engine.ActionAcceptTrade}}},
		})
		it.Delta, it.SE = res.Diff[1], res.SE[1]
		it.PTarget = res.Mean[0]
		for j, p := range g.Players {
			if p.ID == it.Attacker {
				it.AttGain = res.Others[1][j] - res.Others[0][j]
			}
		}
	})
}

// printTradeReport summarises the attack.
func printTradeReport(items []*judged, st attackStats, level string) {
	fmt.Printf("\n== Trade answers of %s: %d position/target pairs, %d deal shapes, %d offers made\n", level, st.pairs, st.structures, st.offers)
	fmt.Printf("shapes accepted at some price on the ladder: %d of %d (%.1f%%); pairs with an accepted deal: %d\n",
		st.accepted, st.structures, 100*float64(st.accepted)/float64(max(1, st.structures)), st.pairsWithAccept)
	kinds := make([]string, 0, len(st.byKind))
	for k := range st.byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		v := st.byKind[k]
		fmt.Printf("  %-6s %5d shapes, %5d accepted\n", k, v[0], v[1])
	}
	fmt.Printf("group-completing shapes: %d, accepted %d; refused ones probed with +$1..+$10: %d, flipped to accepted: %d\n",
		st.completing, st.completingAccepted, st.probed, st.probeFlipped)
	fmt.Printf("server quotes obtained: %d\n\n", st.quoted)
	for _, path := range []string{"answer", "probe", "quote", "ownquote"} {
		for _, comp := range []int{-1, 1, 0} {
			var w, bad2, bad5, se, gain float64
			n, nb := 0, 0
			for _, it := range items {
				if it.Path != path || math.IsNaN(it.Delta) {
					continue
				}
				if comp == 1 && !it.CompletesA || comp == 0 && it.CompletesA {
					continue
				}
				n++
				w += it.Weight
				se += it.SE
				gain += it.Weight * it.Delta
				if it.Delta <= -0.02 {
					bad2 += it.Weight
					nb++
				}
				if it.Delta <= -0.05 {
					bad5 += it.Weight
				}
			}
			if n == 0 {
				continue
			}
			label := map[int]string{-1: "all", 1: "completing the human's group", 0: "not completing"}[comp]
			_, lo, hi := wilson(nb, n)
			fmt.Printf("%-7s %-30s judged %5d  cost >=2pp: %5.1f%% (unweighted 95%% CI %.1f-%.1f)  >=5pp: %5.1f%%  mean change %+.2fpp  oracle SE %.2fpp\n",
				path, label, n, 100*bad2/w, 100*lo, 100*hi, 100*bad5/w, 100*gain/w, 100*se/float64(n))
		}
	}
	// How often a position offers the human at least one such deal.
	for _, path := range []string{"answer", "probe", "quote", "ownquote"} {
		pairs := map[[2]any]bool{}
		bad := map[[2]any]bool{}
		for _, it := range items {
			if it.Path != path && !(path == "probe" && it.Path == "answer") {
				continue
			}
			key := [2]any{it.Pos, it.Target}
			pairs[key] = true
			if it.Delta <= -0.02 {
				bad[key] = true
			}
		}
		if len(pairs) > 0 {
			label := map[string]string{"answer": "ladder answers", "probe": "ladder + probing", "quote": "server quotes", "ownquote": "its own quotes"}[path]
			p, lo, hi := wilson(len(bad), st.pairs)
			fmt.Printf("position/target pairs where %-16s give the human a deal costing the computer >=2pp: %d of %d, %s\n", label, len(bad), st.pairs, pct(p, lo, hi))
		}
	}
	// Worst cases.
	var worst []*judged
	for _, it := range items {
		if !math.IsNaN(it.Delta) {
			worst = append(worst, it)
		}
	}
	sort.Slice(worst, func(i, j int) bool { return worst[i].Delta < worst[j].Delta })
	fmt.Println("\nworst accepted deals (target's win probability change, oracle):")
	for i := 0; i < len(worst) && i < 12; i++ {
		it := worst[i]
		fmt.Printf("  %+6.1fpp ±%.1f  human %+5.1fpp  %-6s %s (tries %d)\n", 100*it.Delta, 100*it.SE, 100*it.AttGain, it.Path, it.Text, it.Tries)
	}
	_ = strings.Join
}

// crossCheck re-judges the n worst deals and n random others with whole-game
// rollouts (no network at the end) and prints how the two oracles agree.
func crossCheck(positions []position, items []*judged, n, rollouts, rounds int, fullTrades bool, seed int64, parallel int) {
	var valid []*judged
	for _, it := range items {
		if !math.IsNaN(it.Delta) {
			valid = append(valid, it)
		}
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].Delta < valid[j].Delta })
	pick := map[*judged]bool{}
	for i := 0; i < len(valid) && i < n; i++ {
		pick[valid[i]] = true
	}
	rng := rand.New(rand.NewPCG(uint64(seed), 0xc055))
	perm := rng.Perm(len(valid))
	for _, k := range perm {
		if len(pick) >= 2*n {
			break
		}
		pick[valid[k]] = true
	}
	var a, c []*judged
	for _, it := range valid {
		if pick[it] {
			a = append(a, it)
			cp := *it
			c = append(c, &cp)
		}
	}
	judgeWith(positions, c, hardai.OracleOptions{Rollouts: rollouts, Rounds: rounds, Seed: uint64(seed) ^ 0xf011, FullTrades: fullTrades}, parallel)
	label := "whole game"
	if rounds > 0 {
		label = fmt.Sprintf("%d-round", rounds)
	}
	if fullTrades {
		label += ", full trades"
	}
	fmt.Printf("\ncross-check of %d deals (%d worst + random) with %d %s rollouts:\n", len(a), min(n, len(valid)), rollouts, label)
	compareOracles(label, a, c)
	worstN := min(n, len(a))
	agree := 0
	for i := 0; i < worstN; i++ {
		// a is in Delta order, so its first entries are the worst ones.
		if c[i].Delta <= -0.02 {
			agree++
		}
	}
	fmt.Printf("of the %d worst deals, %d also cost >= 2pp by the %s oracle\n", worstN, agree, label)
}

// printAnswerQuality: on the server-quoted deals (judged whatever the
// answer), how often the level's own answer takes good and bad deals.
func printAnswerQuality(items []*judged) {
	type row struct{ n, acc int }
	var good, bad, mid row
	for _, it := range items {
		if it.Path != "quote" || math.IsNaN(it.Delta) {
			continue
		}
		r := &mid
		switch {
		case it.Delta >= 0.02:
			r = &good
		case it.Delta <= -0.02:
			r = &bad
		}
		r.n++
		if it.LevelAccepts {
			r.acc++
		}
	}
	fmt.Println("\nthe level's own answer to the quoted deals (were the server not to accept them for it):")
	for _, x := range []struct {
		label string
		r     row
	}{{"good for it (oracle >= +2pp)", good}, {"about even", mid}, {"bad for it (oracle <= -2pp)", bad}} {
		p, lo, hi := wilson(x.r.acc, x.r.n)
		fmt.Printf("  %-30s %5d deals, accepted %s\n", x.label, x.r.n, pct(p, lo, hi))
	}
}

// cmdRecheck re-judges a trades run's deals (its JSON lines output) with
// another oracle: the positions are played again from the same seed.
func cmdRecheck(args []string) {
	fs := flag.NewFlagSet("recheck", flag.ExitOnError)
	in := fs.String("in", "", "JSON lines written by trades -out")
	srcName := fs.String("source", "expert", "level of the source games (as in the trades run)")
	games := fs.Int("games", 24, "source games (as in the trades run)")
	perStage := fs.Int("per-stage", 1, "positions per stage (as in the trades run)")
	seed := fs.Int64("seed", 20261103, "seed (as in the trades run)")
	parallel := fs.Int("parallel", 10, "goroutines")
	n := fs.Int("n", 30, "re-judge the n worst deals and n random others")
	rollouts := fs.Int("rollouts", 256, "rollouts per deal")
	rounds := fs.Int("rounds", 24, "rollout length in rounds (negative: whole game)")
	full := fs.Bool("full-trades", true, "rollouts play the heuristic's full trade search")
	fs.Parse(args)
	src, err := hardai.ParseLevel(*srcName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var items []*judged
	for _, line := range strings.Split(string(data), "\n") {
		var j judged
		if json.Unmarshal([]byte(line), &j) == nil && j.Attacker != "" {
			items = append(items, &j)
		}
	}
	positions := collectPositions(sourceConfig{level: src, games: *games, seed: *seed, parallel: *parallel, perStage: *perStage, newBot: defaultNewBot})
	fmt.Printf("%d deals, %d positions\n", len(items), len(positions))
	crossCheck(positions, items, *n, *rollouts, *rounds, *full, *seed, *parallel)
}

// cmdReanswer asks a Grandmaster variant (the -gm-* flags) again about the
// deals a trades run found and judged, and reports how often it accepts the
// bad ones (oracle <= -2pp) and the good ones. A quick screen for a change
// to the answer: the full measure is a new trades run, since a variant that
// refuses one rung of a ladder may accept the next.
func cmdReanswer(args []string) {
	fs := flag.NewFlagSet("reanswer", flag.ExitOnError)
	in := fs.String("in", "", "JSON lines written by trades -out (a grandmaster run)")
	srcName := fs.String("source", "expert", "level of the source games (as in the trades run)")
	games := fs.Int("games", 24, "source games (as in the trades run)")
	perStage := fs.Int("per-stage", 1, "positions per stage (as in the trades run)")
	seed := fs.Int64("seed", 20261103, "seed (as in the trades run)")
	parallel := fs.Int("parallel", 10, "goroutines")
	paths := fs.String("paths", "answer", "paths to ask again (comma-separated: answer, ownquote, probe)")
	goodShare := fs.Float64("good", 0.25, "share of the deals that are not bad to ask again (all bad ones are)")
	var gmo hardai.GrandmasterOptions
	fs.IntVar(&gmo.TradeHorizon, "gm-trade-horizon", 0, "Grandmaster variant: rounds per trade-answer rollout (0 = default)")
	fs.IntVar(&gmo.TradeRollouts, "gm-trade-rollouts", 0, "Grandmaster variant: rounds per trade answer (0 = default)")
	fs.BoolVar(&gmo.NoStyles, "gm-nostyles", false, "Grandmaster variant: no opponent style mix")
	fs.BoolVar(&gmo.SharpProposer, "gm-sharp", false, "Grandmaster variant: trade answers play the proposer with the tuned heuristic")
	fs.Float64Var(&gmo.Margin, "gm-margin", 0, "Grandmaster variant: accept margin (0 = default)")
	fs.Float64Var(&gmo.Rival, "gm-rival", 0, "Grandmaster variant: weight of the proposer's gain (0 = default)")
	fs.Float64Var(&gmo.RivalLeader, "gm-rival-leader", 0, "Grandmaster variant: ... when it is the strongest rival (0 = default)")
	fs.Parse(args)
	gmOverride = &gmo
	src, err := hardai.ParseLevel(*srcName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	want := map[string]bool{}
	for _, p := range strings.Split(*paths, ",") {
		want[strings.TrimSpace(p)] = true
	}
	var items []*judged
	for i, line := range strings.Split(string(data), "\n") {
		var j judged
		if json.Unmarshal([]byte(line), &j) != nil || j.Attacker == "" || !want[j.Path] || math.IsNaN(j.Delta) {
			continue
		}
		if j.Delta > -0.02 && float64(hashMix(uint64(*seed), uint64(i))%1000) >= 1000**goodShare {
			continue
		}
		items = append(items, &j)
	}
	positions := collectPositions(sourceConfig{level: src, games: *games, seed: *seed, parallel: *parallel, perStage: *perStage, newBot: defaultNewBot})
	fmt.Fprintf(os.Stderr, "%d deals to ask again, %d positions\n", len(items), len(positions))
	accepted := make([]bool, len(items))
	valid := make([]bool, len(items))
	parallelDo(len(items), *parallel, "reanswer", func(i int) {
		it := items[i]
		pos := positions[it.Pos]
		bot := levelBot(hardai.Grandmaster, int64(hashMix(uint64(*seed), uint64(it.Pos), uint64(len(it.Target)), uint64(it.Target[len(it.Target)-1]))))
		accepted[i], valid[i] = answer(pos.g, it.Attacker, offerAction(it.Target, it.Offer, it.Request, it.Net), bot)
	})
	type row struct{ n, yes int }
	rows := map[string]*row{}
	add := func(key string, yes bool) {
		r := rows[key]
		if r == nil {
			r = &row{}
			rows[key] = r
		}
		r.n++
		if yes {
			r.yes++
		}
	}
	for i, it := range items {
		if !valid[i] {
			continue
		}
		class := "even (-2..+2pp)"
		switch {
		case it.Delta <= -0.05:
			class = "bad, >= 5pp"
		case it.Delta <= -0.02:
			class = "bad, 2-5pp"
		case it.Delta >= 0.02:
			class = "good (>= +2pp)"
		}
		add(it.Path+" "+class, accepted[i])
		if it.Delta <= -0.02 {
			add(it.Path+" bad, all", accepted[i])
		}
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("variant %+v\n", gmo)
	for _, k := range keys {
		r := rows[k]
		p, lo, hi := wilson(r.yes, r.n)
		fmt.Printf("  %-28s asked %5d, still accepted %5.1f%% (%.1f-%.1f)\n", k, r.n, 100*p, 100*lo, 100*hi)
	}
}

// cmdSummary breaks a trades run's judged deals down by path, stage and
// deal kind.
func cmdSummary(args []string) {
	fs := flag.NewFlagSet("summary", flag.ExitOnError)
	in := fs.String("in", "", "JSON lines written by trades -out")
	sources := fs.Int("sources", 0, "only deals from the first N source games (0 = all)")
	fs.Parse(args)
	data, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	type acc struct{ n, bad int }
	rows := map[string]*acc{}
	type wacc struct {
		w, bad float64
		n      int
	}
	weighted := map[string]*wacc{}
	pairs, badPairs := map[string]map[string]bool{}, map[string]map[string]bool{}
	add := func(key string, bad bool) {
		a := rows[key]
		if a == nil {
			a = &acc{}
			rows[key] = a
		}
		a.n++
		if bad {
			a.bad++
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		var j judged
		if json.Unmarshal([]byte(line), &j) != nil || j.Attacker == "" || math.IsNaN(j.Delta) || (*sources > 0 && j.Source >= *sources) {
			continue
		}
		bad := j.Delta <= -0.02
		wkey := j.Path
		if j.CompletesA {
			wkey += " completing"
		}
		for _, k := range []string{j.Path + " (all)", wkey} {
			w := weighted[k]
			if w == nil {
				w = &wacc{}
				weighted[k] = w
			}
			w.w += j.Weight
			w.n++
			if bad {
				w.bad += j.Weight
			}
		}
		pair := fmt.Sprint(j.Pos, j.Target)
		if pairs[j.Path] == nil {
			pairs[j.Path], badPairs[j.Path] = map[string]bool{}, map[string]bool{}
		}
		pairs[j.Path][pair] = true
		if bad {
			badPairs[j.Path][pair] = true
		}
		comp := "other"
		if j.CompletesA {
			comp = "completing"
		}
		add(j.Path+" | stage "+j.Stage, bad)
		add(j.Path+" | kind "+j.Kind, bad)
		add(j.Path+" | "+comp, bad)
		add(j.Path+" | players "+fmt.Sprint(j.Players), bad)
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := rows[k]
		p, lo, hi := wilson(a.bad, a.n)
		fmt.Printf("%-36s %5d judged, cost >= 2pp: %s\n", k, a.n, pct(p, lo, hi))
	}
	fmt.Println("\nweighted (sampled deals count for the ones not judged):")
	wkeys := make([]string, 0, len(weighted))
	for k := range weighted {
		wkeys = append(wkeys, k)
	}
	sort.Strings(wkeys)
	for _, k := range wkeys {
		w := weighted[k]
		fmt.Printf("%-28s %5d judged, cost >= 2pp: %5.1f%%\n", k, w.n, 100*w.bad/w.w)
	}
	fmt.Println("\nposition/target pairs with at least one deal costing >= 2pp (of the pairs with a judged deal):")
	for _, path := range []string{"answer", "probe", "quote", "ownquote"} {
		if len(pairs[path]) > 0 {
			p, lo, hi := wilson(len(badPairs[path]), len(pairs[path]))
			fmt.Printf("%-9s %d of %d, %s\n", path, len(badPairs[path]), len(pairs[path]), pct(p, lo, hi))
		}
	}
}

// cmdShow prints the position behind the k-th worst judged deal of a trades
// run: who owns what, cash, houses and the network's win probabilities.
func cmdShow(args []string) {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	in := fs.String("in", "", "JSON lines written by trades -out")
	rank := fs.Int("rank", 0, "0 = the worst deal")
	games := fs.Int("games", 24, "source games (as in the trades run)")
	seed := fs.Int64("seed", 20261103, "seed (as in the trades run)")
	parallel := fs.Int("parallel", 10, "goroutines")
	fs.Parse(args)
	data, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var items []*judged
	for _, line := range strings.Split(string(data), "\n") {
		var j judged
		if json.Unmarshal([]byte(line), &j) == nil && j.Attacker != "" && !math.IsNaN(j.Delta) {
			items = append(items, &j)
		}
	}
	sort.Slice(items, func(a, b int) bool { return items[a].Delta < items[b].Delta })
	it := items[*rank]
	positions := collectPositions(sourceConfig{level: hardai.Expert, games: *games, seed: *seed, parallel: *parallel, perStage: 1, newBot: defaultNewBot})
	g := positions[it.Pos].g
	fmt.Printf("%s\noracle %+.1fpp ±%.1f, the computer's win probability if it refuses %.1f%%\n\n", it.Text, 100*it.Delta, 100*it.SE, 100*it.PTarget)
	net, _ := valuenetDefault()
	probs := winProbs(net, g)
	for i, p := range g.Players {
		var owned []string
		for _, index := range engine.OwnableIndexes {
			st := g.Properties[index]
			if st.OwnerID != p.ID {
				continue
			}
			label := engine.Board[index].Name
			if grp := engine.Board[index].Group; grp != "" {
				label += "/" + grp
			}
			if st.Houses > 0 {
				label += fmt.Sprintf("[%dh]", st.Houses)
			}
			if st.Mortgaged {
				label += "(m)"
			}
			owned = append(owned, label)
		}
		fmt.Printf("%-10s %-6s cash $%-5d net %5.1f%% bankrupt=%v at %d: %s\n", p.Name, p.Kind, p.Cash, 100*probs[i], p.Bankrupt, p.Position, strings.Join(owned, ", "))
	}
}

// ownQuoteAll: ask a Grandmaster's own price for every deal shape (-ownquote-all).
var ownQuoteAll bool

// recordExploit writes the exploit rates into the summary file: the share of
// deals the level accepted on the price ladder that cost it at least 2pp
// (<prefix>-exploit), the share of position/target pairs with at least one
// (<prefix>-exploit-pairs), and the same via the trade screen's quote
// (<prefix>-chip-pairs): the Normal heuristic's price, or for Grandmaster its
// own price, which is what the server honours for it. Counts are of judged
// deals: run with a -sample large enough to judge every accepted deal (and
// -ownquote-all for Grandmaster).
func recordExploit(path, prefix, level string, items []*judged, st attackStats) {
	bad, n := 0, 0
	pairsBad, chipBad := map[string]bool{}, map[string]bool{}
	chipPath, chipLabel := "quote", "the trade screen's Normal quote"
	if level == "grandmaster" || level == "gm" {
		chipPath, chipLabel = "ownquote", "the trade screen quote (its own price)"
	}
	for _, it := range items {
		if math.IsNaN(it.Delta) {
			continue
		}
		key := fmt.Sprint(it.Pos, it.Target)
		switch it.Path {
		case "answer":
			n++
			if it.Delta <= -0.02 {
				bad++
				pairsBad[key] = true
			}
		case chipPath:
			if it.Delta <= -0.02 {
				chipBad[key] = true
			}
		}
	}
	name := strings.ToUpper(level[:1]) + level[1:]
	mergeSummary(path,
		resultOf(prefix+"-exploit", name+": deals it accepts on a price ladder that cost it >= 2pp", bad, n),
		resultOf(prefix+"-exploit-pairs", name+": positions (per opponent) with such a deal", len(pairsBad), st.pairs),
		resultOf(prefix+"-chip-pairs", name+": positions with a bad deal via "+chipLabel, len(chipBad), st.pairs))
}
