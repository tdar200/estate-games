// Command estate-whatif replays a recorded game and, at every trade offer a
// person made, asks each computer level how it would have answered the same
// offer in the same position. It shows whether a stronger level would have
// refused the deals a person won with, and (with -quotes, the default) what
// each level's own price is for the deal (the trade screen's ASK chip since
// 0.10.1) and what it answers at the price that was offered: accept, a
// counter-offer at its price, or no with its reason.
//
//	go run ./cmd/estate-whatif [-seeds 5] [-levels expert,grandmaster] [-quotes=false] game.json.gz
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
	"github.com/tdar200/estate-games/internal/hardai"
)

func main() {
	seeds := flag.Int("seeds", 5, "answers per level, each with its own seed")
	levels := flag.String("levels", "expert,grandmaster", "levels to ask")
	all := flag.Bool("all", false, "also ask about offers that were refused")
	quotes := flag.Bool("quotes", true, "also show each level's own price for the deal and its answer by that price (Hard, Expert, Grandmaster)")
	workers := flag.Int("workers", 8, "cores per answer")
	only := flag.String("steps", "", "only these offer steps (comma-separated record step numbers)")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: estate-whatif [-seeds N] [-levels a,b] [-quotes=false] game.json.gz")
		os.Exit(2)
	}
	rec, err := gamerecord.Read(flag.Arg(0))
	must(err)
	names := map[string]string{}
	human := map[string]bool{}
	for _, p := range rec.Players {
		names[p.ID] = p.Name
		human[p.ID] = p.Kind == "human"
	}
	var asked []hardai.Level
	for _, s := range strings.Split(*levels, ",") {
		l, err := hardai.ParseLevel(strings.TrimSpace(s))
		must(err)
		asked = append(asked, l)
	}
	cfg := config{levels: asked, seeds: *seeds, all: *all, quotes: *quotes, workers: *workers, names: names, steps: map[int]bool{}}
	for _, s := range strings.Split(*only, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			cfg.steps[n] = true
		}
	}

	r, err := gamerecord.NewReplayer(rec)
	must(err)
	for !r.Done() {
		st := &rec.Steps[r.Next()]
		if st.Kind == gamerecord.StepAction && human[st.By] && len(st.Action) > 0 {
			var a engine.Action
			if json.Unmarshal(st.Action, &a) == nil && a.Type == engine.ActionOfferTrade {
				report(r.G, st, a, cfg, rec)
			}
		}
		if _, err := r.Step(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			must(err)
		}
	}
}

type config struct {
	levels  []hardai.Level
	seeds   int
	all     bool
	quotes  bool
	workers int
	names   map[string]string
	steps   map[int]bool // empty: all
}

// report asks every level about one offer, at the position just before it.
func report(pos *engine.Game, st *gamerecord.Step, a engine.Action, cfg config, rec *gamerecord.Record) {
	actual := answerInRecord(rec, st.N, a.TargetID)
	if (!cfg.all && actual != "accepted") || (len(cfg.steps) > 0 && !cfg.steps[st.N]) {
		return
	}
	paid := cashOf(a.Offer) - cashOf(a.Request)
	cash := 0
	if p := pos.PlayerByID(st.By); p != nil {
		cash = p.Cash
	}
	fmt.Printf("\nstep %d: %s asks %s for %s, giving %s  (actually %s; net cash paid %s; %s had %s)\n", st.N, cfg.names[st.By], cfg.names[a.TargetID],
		bundle(a.Request), bundle(a.Offer), actual, signed(paid), cfg.names[st.By], signed(cash))
	if net, ok := normalQuote(pos, st.By, a.TargetID, propsOf(a.Offer), propsOf(a.Request)); ok {
		fmt.Printf("  %-12s the Normal heuristic's quote (the ASK chip before 0.10.1, honoured for every level): %s\n", "", signed(net))
	} else {
		fmt.Printf("  %-12s the Normal heuristic's quote: none\n", "")
	}
	for _, l := range cfg.levels {
		// Before 0.10.1: the bot's search answers the offer like a bot's.
		yes := 0
		for s := 0; s < cfg.seeds; s++ {
			g := hardai.Clone(pos)
			if err := engine.PerformAction(g, st.By, a, nil); err != nil {
				fmt.Printf("  %-12s engine refused the offer: %v\n", l, err)
				break
			}
			bot := hardai.NewBot(l, int64(1000+s), hardai.Options{Budget: -1, Workers: cfg.workers, NoHumanPrices: true})
			if ch, ok := bot.Choose(g, a.TargetID); ok && ch.Type == engine.ActionAcceptTrade {
				yes++
			}
		}
		fmt.Printf("  %-12s search answer (before 0.10.1): accepts %d of %d\n", l, yes, cfg.seeds)
		if !cfg.quotes || (l != hardai.Hard && l != hardai.Expert && l != hardai.Grandmaster) {
			continue
		}
		// Since 0.10.1: its own price, and its answer by it.
		var prices []string
		answers := map[string]int{}
		var sample string
		for s := 0; s < cfg.seeds; s++ {
			bot := hardai.NewBot(l, int64(1000+s), hardai.Options{Budget: -1, Workers: cfg.workers}).(*hardai.HardBot)
			q := bot.Quote(hardai.Clone(pos), a.TargetID, st.By, propsOf(a.Offer), propsOf(a.Request))
			if q.OK {
				prices = append(prices, signed(q.Net))
			} else {
				prices = append(prices, "none")
			}
			g := hardai.Clone(pos)
			if err := engine.PerformAction(g, st.By, a, nil); err != nil {
				break
			}
			ch, _ := bot.Choose(g, a.TargetID)
			key := describeAnswer(ch, cfg.names)
			answers[key]++
			if sample == "" {
				sample = key
			}
		}
		fmt.Printf("  %-12s its price (net cash from %s): %s\n", "", cfg.names[st.By], strings.Join(prices, ", "))
		keys := make([]string, 0, len(answers))
		for k := range answers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %-12s answer to the offer as made (%d of %d): %s\n", "", answers[k], cfg.seeds, k)
		}
	}
}

// describeAnswer renders a bot's answer to a person's offer.
func describeAnswer(a engine.Action, names map[string]string) string {
	switch a.Type {
	case engine.ActionAcceptTrade:
		return "accepts"
	case engine.ActionRejectTrade:
		if a.Reason != "" {
			return fmt.Sprintf("declines: %q", a.Reason)
		}
		return "declines"
	case engine.ActionCounterTrade:
		return fmt.Sprintf("counter-offer: it gives %s, it asks %s (%q)", bundle(a.Offer), bundle(a.Request), a.Reason)
	}
	return a.Type
}

// answerInRecord is what the target actually answered: the target's next
// accept, reject or counter step after the offer.
func answerInRecord(rec *gamerecord.Record, from int, target string) string {
	for i := from + 1; i < len(rec.Steps) && i < from+40; i++ {
		st := rec.Steps[i]
		if st.By != target || len(st.Action) == 0 {
			continue
		}
		var a engine.Action
		if json.Unmarshal(st.Action, &a) != nil {
			continue
		}
		switch a.Type {
		case engine.ActionAcceptTrade:
			return "accepted"
		case engine.ActionRejectTrade:
			return "rejected"
		case engine.ActionCounterTrade:
			return "countered"
		}
	}
	return "unknown"
}

func cashOf(b *engine.TradeBundle) int {
	if b == nil {
		return 0
	}
	return b.Cash
}

func propsOf(b *engine.TradeBundle) []int {
	if b == nil {
		return nil
	}
	return b.Properties
}

func signed(v int) string {
	if v < 0 {
		return fmt.Sprintf("-$%d", -v)
	}
	return fmt.Sprintf("$%d", v)
}

// bundle names a trade side: the deeds and the cash.
func bundle(b *engine.TradeBundle) string {
	if b == nil {
		return "nothing"
	}
	var parts []string
	for _, i := range b.Properties {
		parts = append(parts, engine.Board[i].Name)
	}
	if b.Cash > 0 {
		parts = append(parts, fmt.Sprintf("$%d", b.Cash))
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, " + ")
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// normalQuote is the Normal heuristic's price for the deal (what the server
// quoted and honoured for every level before 0.10.1): the net cash the
// proposer pays.
func normalQuote(pos *engine.Game, proposer, target string, offer, request []int) (int, bool) {
	data, err := json.Marshal(pos)
	if err != nil {
		return 0, false
	}
	var view ai.Game
	if err := json.Unmarshal(data, &view); err != nil {
		return 0, false
	}
	q, err := ai.QuoteTrade(&view, proposer, ai.NewQuoteRequest(target, offer, request))
	if err != nil {
		return 0, false
	}
	return q.OfferCash - q.RequestCash, true
}
