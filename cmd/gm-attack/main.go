// Command gm-attack measures how exploitable a computer level is: it plays
// games between bots of that level, takes positions from them, makes one seat
// a human attacker and searches widely for trade offers the level accepts,
// then judges every accepted offer with a slow, strong oracle (many long
// rollouts, hardai.Oracle). It also audits the level's own purchases,
// auction ceilings and trade proposals, and measures the clairvoyant ceiling.
//
//	gm-attack trades      -level expert -games 30 -parallel 10 -out trades.jsonl
//	gm-attack decisions   -level expert -games 30 -parallel 10 -out decisions.jsonl
//	gm-attack noise       -games 6 -parallel 10
//	gm-attack clairvoyant -games 400 -parallel 10 -json clair.json
//	gm-attack recheck     -in trades.jsonl -n 30 -rounds 24 -full-trades
//	gm-attack summary     -in trades.jsonl
//	gm-attack show        -in trades.jsonl -rank 0
//	gm-attack speed
//
// Every answer is the level's real production answer: the bot's Choose on
// the game with the offer pending, with its production options (no time
// limit, so it is reproducible). The "quote" path is what the server does
// when a human asks a computer for its price in the trade screen
// (ai.QuoteTrade, the Normal heuristic's acceptance test) and then offers
// that deal: the server accepts it for the computer, whatever its level
// (internal/server/quotes.go). See docs/ai/GRANDMASTER.md.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/hardai"
	"github.com/tdar200/estate-games/internal/valuenet"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	debug.SetGCPercent(400)
	args := os.Args[2:]
	switch os.Args[1] {
	case "trades":
		cmdTrades(args)
	case "decisions":
		cmdDecisions(args)
	case "noise":
		cmdNoise(args)
	case "clairvoyant":
		cmdClairvoyant(args)
	case "speed":
		cmdSpeed(args)
	case "reanswer":
		cmdReanswer(args)
	case "recheck":
		cmdRecheck(args)
	case "summary":
		cmdSummary(args)
	case "show":
		cmdShow(args)
	case "arenasum":
		cmdArenaSum(args)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gm-attack trades|reanswer|recheck|summary|show|decisions|noise|clairvoyant|speed|arenasum [flags]")
	os.Exit(2)
}

// ---------------------------------------------------------------- positions

// position is a game state at the start of the attacker's turn (it is about
// to roll), taken from a game between bots of the level under attack.
type position struct {
	g        *engine.Game
	source   int    // source game
	stage    string // early, mid, late
	attacker string // the current player, made a human
}

// stageOf classifies a position: early while the bank still holds 4 or more
// deeds, late once 6 or more houses stand, mid in between.
func stageOf(g *engine.Game) string {
	unowned, houses := 0, 0
	for _, index := range engine.OwnableIndexes {
		st := g.Properties[index]
		if st.OwnerID == "" {
			unowned++
		}
		houses += st.Houses
	}
	switch {
	case unowned >= 4:
		return "early"
	case houses >= 6:
		return "late"
	}
	return "mid"
}

// sourceConfig describes the games positions are drawn from.
type sourceConfig struct {
	level    hardai.Level
	games    int
	seed     int64
	parallel int
	perStage int // positions kept per stage and game
	newBot   func(level hardai.Level, seed int64, opts hardai.Options) hardai.Bot
	// audit, if set, records a seat's decisions: hook sees every decision
	// (DecisionInfo), wrap wraps the seat's bot.
	audit func(game, seat int) (hook func(hardai.DecisionInfo), wrap func(hardai.Bot) hardai.Bot)
}

var playerCounts = []int{4, 3, 5, 4, 6, 4}

// collectPositions plays cfg.games games of cfg.level bots (3-6 players)
// and keeps up to perStage turn-start positions per stage and game.
func collectPositions(cfg sourceConfig) []position {
	out := make([][]position, cfg.games)
	var next atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	var done atomic.Int64
	for w := 0; w < cfg.parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= cfg.games {
					return
				}
				thermalWait()
				n := playerCounts[i%len(playerCounts)]
				levels := make([]hardai.Level, n)
				for s := range levels {
					levels[s] = cfg.level
				}
				var snaps []*engine.Game
				var last *engine.Turn
				res, err := hardai.PlayMatch(hardai.MatchConfig{
					Levels: levels, Seed: cfg.seed + int64(i), MaxTurns: 1000,
					NewBot: func(seat int, level hardai.Level, seed int64) hardai.Bot {
						opts := hardai.Options{Budget: -1, Workers: 1}
						if cfg.audit == nil {
							return cfg.newBot(level, seed, opts)
						}
						hook, wrap := cfg.audit(i, seat)
						opts.OnDecision = hook
						return wrap(cfg.newBot(level, seed, opts))
					},
					OnAction: func(g *engine.Game, _ string, _ engine.Action) {
						if g.Turn == last {
							return
						}
						last = g.Turn
						if g.Status == engine.StatusPlaying && g.Phase == engine.PhaseAwaitingRoll && g.Turn != nil {
							if p := engine.CurrentPlayer(g); p != nil && !p.InJail && !p.Bankrupt {
								snaps = append(snaps, hardai.Clone(g))
							}
						}
					},
				})
				if err != nil {
					fmt.Fprintf(os.Stderr, "game %d: %v\n", i, err)
					continue
				}
				_ = res
				rng := rand.New(rand.NewPCG(uint64(cfg.seed), uint64(i)))
				byStage := map[string][]*engine.Game{}
				for _, g := range snaps {
					s := stageOf(g)
					byStage[s] = append(byStage[s], g)
				}
				var kept []position
				for _, s := range []string{"early", "mid", "late"} {
					list := byStage[s]
					rng.Shuffle(len(list), func(a, b int) { list[a], list[b] = list[b], list[a] })
					for k := 0; k < len(list) && k < cfg.perStage; k++ {
						g := list[k]
						cur := engine.CurrentPlayer(g)
						cur.Kind = engine.KindHuman
						kept = append(kept, position{g: g, source: i, stage: s, attacker: cur.ID})
					}
				}
				out[i] = kept
				d := done.Add(1)
				fmt.Fprintf(os.Stderr, "\rpositions: %d/%d games (%s)", d, cfg.games, time.Since(start).Round(time.Second))
			}
		}()
	}
	wg.Wait()
	fmt.Fprintln(os.Stderr)
	var all []position
	for _, list := range out {
		all = append(all, list...)
	}
	return all
}

func defaultNewBot(level hardai.Level, seed int64, opts hardai.Options) hardai.Bot {
	return hardai.NewBot(level, seed, opts)
}

// ------------------------------------------------------------------ helpers

func groupOf(index int) string { return engine.Board[index].Group }

// tradeable lists owner's deeds that may change hands (not collateral, no
// buildings in the colour group).
func tradeable(g *engine.Game, owner string) []int {
	var out []int
	for _, index := range engine.OwnableIndexes {
		st := g.Properties[index]
		if st.OwnerID != owner || (len(g.Loans) > 0 && engine.LoanSecuredBy(g, index) != nil) {
			continue
		}
		if grp := groupOf(index); grp != "" {
			built := false
			for _, s := range engine.GroupSpaces(grp) {
				built = built || g.Properties[s.Index].Houses > 0
			}
			if built {
				continue
			}
		}
		out = append(out, index)
	}
	return out
}

// completes reports whether moving deeds `gets` to player id (and `gives`
// away from it) gives id a colour group it does not own now.
func completes(g *engine.Game, id string, gets, gives []int) bool {
	owned := map[int]bool{}
	for _, index := range engine.OwnableIndexes {
		if g.Properties[index].OwnerID == id {
			owned[index] = true
		}
	}
	before := groupsOf(owned)
	for _, i := range gives {
		delete(owned, i)
	}
	for _, i := range gets {
		owned[i] = true
	}
	after := groupsOf(owned)
	for grp := range after {
		if !before[grp] {
			return true
		}
	}
	return false
}

func groupsOf(owned map[int]bool) map[string]bool {
	out := map[string]bool{}
	for _, key := range engine.GroupOrder {
		all := true
		for _, s := range engine.GroupSpaces(key) {
			all = all && owned[s.Index]
		}
		if all {
			out[key] = true
		}
	}
	return out
}

func price(indexes []int) int {
	total := 0
	for _, i := range indexes {
		total += engine.Board[i].Price
	}
	return total
}

func names(indexes []int) string {
	var parts []string
	for _, i := range indexes {
		s := engine.Board[i]
		label := s.Name
		if s.Group != "" {
			label += " (" + s.Group + ")"
		}
		parts = append(parts, label)
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, " + ")
}

func round10(x float64) int { return int(math.Round(x/10)) * 10 }

// offerAction is the attacker's offer: net > 0 means the attacker pays net
// cash, net < 0 that the target pays -net.
func offerAction(target string, offer, request []int, net int) engine.Action {
	oc, rc := 0, 0
	if net > 0 {
		oc = net
	} else {
		rc = -net
	}
	return engine.Action{Type: engine.ActionOfferTrade, TargetID: target,
		Offer:   &engine.TradeBundle{Cash: oc, Properties: append([]int{}, offer...)},
		Request: &engine.TradeBundle{Cash: rc, Properties: append([]int{}, request...)}}
}

// answer is the target bot's real answer to the offer (true = accepted).
// ok is false when the engine refuses the offer itself.
func answer(pos *engine.Game, attacker string, offer engine.Action, bot hardai.Bot) (accepted, ok bool) {
	g := hardai.Clone(pos)
	if err := engine.PerformAction(g, attacker, offer, nil); err != nil {
		return false, false
	}
	a, chose := bot.Choose(g, offer.TargetID)
	return chose && a.Type == engine.ActionAcceptTrade, true
}

// quote is the price the server quotes for a deal (ai.QuoteTrade on the
// game's JSON, as RealAI.QuoteTrade does): the net cash the attacker pays.
func quote(pos *engine.Game, attacker, target string, offer, request []int) (int, bool) {
	data, err := json.Marshal(pos)
	if err != nil {
		return 0, false
	}
	var view ai.Game
	if err := json.Unmarshal(data, &view); err != nil {
		return 0, false
	}
	q, err := ai.QuoteTrade(&view, attacker, ai.NewQuoteRequest(target, offer, request))
	if err != nil {
		return 0, false
	}
	return q.OfferCash - q.RequestCash, true
}

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

func pct(p, lo, hi float64) string {
	return fmt.Sprintf("%.1f%% (%.1f-%.1f)", 100*p, 100*lo, 100*hi)
}

// parallelDo runs f(i) for i in [0, n) on workers goroutines.
func parallelDo(n, workers int, label string, f func(i int)) {
	var next, done atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				thermalWait()
				f(i)
				d := done.Add(1)
				if label != "" && (d%max(1, int64(n)/50) == 0 || d == int64(n)) {
					fmt.Fprintf(os.Stderr, "\r%s: %d/%d (%s)", label, d, n, time.Since(start).Round(time.Second))
				}
			}
		}()
	}
	wg.Wait()
	if label != "" {
		fmt.Fprintln(os.Stderr)
	}
}

func writeJSONL(path string, items any) {
	if path == "" {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	enc := json.NewEncoder(w)
	switch list := items.(type) {
	case []*judged:
		for _, it := range list {
			enc.Encode(it)
		}
	case []*auditItem:
		for _, it := range list {
			enc.Encode(it)
		}
	}
}

// levelBot builds the bot under attack for a target seat.
// gmOverride, if set, are the Grandmaster settings the attacked bots use
// (variants; default: the shipped ones).
var gmOverride *hardai.GrandmasterOptions

func levelBot(level hardai.Level, seed int64) hardai.Bot {
	return hardai.NewBot(level, seed, hardai.Options{Budget: -1, Workers: 1, GM: gmOverride})
}

var sortFloat = sort.Float64s

func valuenetDefault() (*valuenet.Net, error) { return valuenet.Default() }

// winProbs is the network's win probability for every player of g (index =
// g.Players; 0 for players it does not read).
func winProbs(net *valuenet.Net, g *engine.Game) []float64 {
	out := make([]float64, len(g.Players))
	if net == nil || !valuenet.Fits(g) {
		return out
	}
	e := valuenet.NewEvaluator(net)
	var pos valuenet.Position
	valuenet.FillFromGame(&pos, g)
	probs := e.WinProbs(&pos)
	for i, p := range g.Players {
		if s := valuenet.SlotOf(g, p.ID); s >= 0 {
			out[i] = probs[s]
		}
	}
	return out
}
