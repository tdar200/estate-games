package hardai

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/valuenet2"
)

// gmTestOptions keep Grandmaster cheap and deterministic in tests.
func gmTestOptions() Options {
	return Options{Budget: -1, Workers: 2, Rollouts: 16, MinRounds: 2,
		GM: &GrandmasterOptions{TradeRollouts: 24, TradeHorizon: 1}}
}

func TestGrandmasterLevel(t *testing.T) {
	l, err := ParseLevel("GrandMaster")
	if err != nil || l != Grandmaster || l.String() != "grandmaster" {
		t.Fatalf("ParseLevel = %v, %v", l, err)
	}
	b := NewBot(Grandmaster, 1, Options{}).(*HardBot)
	if b.opts.GM == nil || b.opts.Net == nil || b.opts.Eval != EvalValueNet {
		t.Fatal("grandmaster options not filled")
	}
	if b.opts.Rollouts != gmRollouts || b.opts.MaxDeals >= 0 || len(b.opts.Styles) == 0 || b.opts.Budget != gmBudget {
		t.Fatalf("grandmaster defaults: rollouts %d, max deals %d, styles %d, budget %v", b.opts.Rollouts, b.opts.MaxDeals, len(b.opts.Styles), b.opts.Budget)
	}
	if b.opts.GM.TradeRollouts != gmDefaults.TradeRollouts || b.opts.GM.Z != gmDefaults.Z {
		t.Fatalf("trade settings %+v", *b.opts.GM)
	}
}

// Grandmaster never makes a move the engine refuses, from 2 to 8 players,
// with classic rules and house rules, and its games are fixed by the seed.
func TestGrandmasterNeverIllegal(t *testing.T) {
	cases := []struct {
		levels []Level
		rules  *engine.RulesUpdate
	}{
		{[]Level{Grandmaster, Expert}, nil},
		{[]Level{Grandmaster, Expert, Hard, Normal}, nil},
		{[]Level{Grandmaster, Normal, Grandmaster}, loansOnly()},
		{[]Level{Grandmaster, Easy, Normal, Hard, Normal, Expert, Normal, Grandmaster}, allRules()},
	}
	if testing.Short() {
		cases = cases[:2]
	}
	for i, c := range cases {
		res, err := PlayMatch(MatchConfig{Levels: c.levels, Seed: int64(500 + i), Options: gmTestOptions(), MaxTurns: 160, Rules: c.rules})
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		for _, s := range res.Seats {
			if s.Level == Grandmaster && s.Rejected != 0 {
				t.Fatalf("case %d: %d Grandmaster actions refused", i, s.Rejected)
			}
		}
	}
	play := func(workers int) []byte {
		o := gmTestOptions()
		o.Workers = workers
		res, err := PlayMatch(MatchConfig{Levels: []Level{Grandmaster, Expert, Normal}, Seed: 61, Options: o, MaxTurns: 100})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(res.Game)
		return data
	}
	if a, b := play(1), play(3); !bytes.Equal(a, b) {
		t.Fatal("the number of workers changed a Grandmaster game")
	}
}

// Probing does not pay: a refused deal stays refused for any cash up to the
// refused amount, and nudging the cash replays the same rollouts, so the
// verdict moves with the cash only.
func TestGrandmasterAnswersAreStable(t *testing.T) {
	spots := tradeSpots(t, 4, func(*engine.Game, string, string, int) bool { return true })
	for _, s := range spots {
		b := NewGrandmasterBot(7, gmTestOptions())
		price := engine.Board[s.deed].Price
		ask := func(cash int) bool {
			g := Clone(s.g)
			offer := tradeAction(s.target, cash, nil, 0, []int{s.deed})
			if err := engine.PerformAction(g, s.proposer, offer, nil); err != nil {
				t.Fatalf("offer: %v", err)
			}
			a, ok := b.Choose(g, s.target)
			return ok && a.Type == engine.ActionAcceptTrade
		}
		cash := max(10, price/4)
		if cash > s.g.PlayerByID(s.proposer).Cash {
			continue
		}
		first := ask(cash)
		for k := 0; k < 5; k++ {
			if got := ask(cash); got != first {
				t.Fatalf("the same offer got %v then %v", first, got)
			}
		}
		if !first {
			for k := 1; k <= 5 && cash-k > 0; k++ {
				if ask(cash - k) {
					t.Fatalf("accepted $%d after refusing $%d", cash-k, cash)
				}
			}
		}
	}
}

// The deal fingerprint covers the deeds traded and their colour groups.
func TestDealFingerprint(t *testing.T) {
	spots := tradeSpots(t, 1, func(*engine.Game, string, string, int) bool { return true })
	g := Clone(spots[0].g)
	tr := &engine.Trade{ProposerID: spots[0].proposer, TargetID: spots[0].target,
		Request: engine.TradeBundle{Properties: []int{spots[0].deed}}}
	before := dealFingerprint(g, tr)
	grp := spaceGroup[spots[0].deed]
	other := spots[0].deed
	if grp >= 0 {
		for _, i := range groupMembers[grp] {
			if i != spots[0].deed {
				other = i
			}
		}
	}
	g.Properties[other].Mortgaged = !g.Properties[other].Mortgaged
	if dealFingerprint(g, tr) == before {
		t.Fatal("a mortgage in the deed's group did not change the fingerprint")
	}
}

// Opponent styles are drawn per rollout and the deciding seat keeps its own
// knobs.
func TestOpponentStyles(t *testing.T) {
	styles := OpponentStyles()
	if len(styles) != 5 {
		t.Fatalf("%d styles", len(styles))
	}
	g, err := NewGame([]string{"A", "B", "C", "D"}, newSplitMix(3))
	if err != nil {
		t.Fatal(err)
	}
	var p rolloutPolicy
	p.styles = styles
	p.mine = ExpertParams()
	p.me = g.Players[0].ID
	seen := map[string]bool{}
	for seed := uint64(0); seed < 200; seed++ {
		p.drawStyles(g, seed)
		if p.paramsOf(p.me) != p.mine {
			t.Fatal("the deciding seat must play its own knobs")
		}
		for _, s := range styles {
			if p.paramsOf(g.Players[1].ID) == s.Params {
				seen[s.Name] = true
			}
		}
	}
	if len(seen) != len(styles) {
		t.Fatalf("styles drawn for one seat in 200 rollouts: %v", seen)
	}
}

// Grandmaster keeps its word: offering the price it quoted is accepted.
func TestGrandmasterQuoteIsHonoured(t *testing.T) {
	spots := tradeSpots(t, 6, func(*engine.Game, string, string, int) bool { return true })
	quoted := 0
	for _, s := range spots {
		b := NewGrandmasterBot(9, gmTestOptions())
		net, ok := b.QuoteDeal(s.g, s.target, s.proposer, nil, []int{s.deed})
		if !ok || net > s.g.PlayerByID(s.proposer).Cash {
			continue
		}
		quoted++
		g := Clone(s.g)
		if err := engine.PerformAction(g, s.proposer, tradeAction(s.target, net, nil, 0, []int{s.deed}), nil); err != nil {
			t.Fatalf("offer: %v", err)
		}
		if a, ok := b.Choose(g, s.target); !ok || a.Type != engine.ActionAcceptTrade {
			t.Fatalf("quoted $%d for %s, then refused it", net, engine.Board[s.deed].Name)
		}
	}
	if quoted == 0 {
		t.Skip("no spot got a quote")
	}
}

// SearchValue gives every player a probability (they sum to 1, bankrupt
// players 0), for any player count, with or without a network.
func TestSearchValue(t *testing.T) {
	for _, n := range []int{2, 4, 7} {
		names := make([]string, n)
		for i := range names {
			names[i] = string(rune('A' + i))
		}
		g, err := NewGame(names, newSplitMix(uint64(n)))
		if err != nil {
			t.Fatal(err)
		}
		d := heuristicDriver(g, newSplitMix(9), 60)
		for step := 0; step < 400 && g.Status == engine.StatusPlaying; step++ {
			if err := d.Step(g); err != nil {
				t.Fatal(err)
			}
		}
		probs := SearchValue(g, SearchValueOptions{Rollouts: 6, Rounds: 1, Seed: 3})
		sum := 0.0
		for i, p := range probs {
			if p < 0 || p > 1 || (g.Players[i].Bankrupt && p != 0) {
				t.Fatalf("%d players: probability %v for player %d", n, p, i)
			}
			sum += p
		}
		if g.Status == engine.StatusPlaying && (sum < 0.999 || sum > 1.001) {
			t.Fatalf("%d players: probabilities sum to %v", n, sum)
		}
	}
}

// Grandmaster's offers to people are well-formed: priced within its cash,
// to a human player, and the engine takes them.
func TestGrandmasterHumanOffers(t *testing.T) {
	spots := tradeSpots(t, 8, func(*engine.Game, string, string, int) bool { return true })
	offers := 0
	for _, s := range spots {
		g := Clone(s.g)
		// The spot's current player becomes the Grandmaster; every other seat a person.
		gmID := s.proposer
		for _, p := range g.Players {
			if p.ID == gmID {
				p.Kind = engine.KindAI
			} else {
				p.Kind = engine.KindHuman
			}
		}
		b := NewGrandmasterBot(5, gmTestOptions())
		b.mem.observe(g, gmID)
		for _, c := range b.humanTradeCandidates(g, gmID) {
			a := c.actions[0]
			if a.Type != engine.ActionOfferTrade || g.PlayerByID(a.TargetID).Kind != engine.KindHuman {
				t.Fatalf("not an offer to a person: %v", a)
			}
			if a.Offer.Cash > g.PlayerByID(gmID).Cash {
				t.Fatalf("offers $%d with $%d", a.Offer.Cash, g.PlayerByID(gmID).Cash)
			}
			sim := Clone(g)
			if err := engine.PerformAction(sim, gmID, a, nil); err != nil {
				t.Fatalf("engine refused %v: %v", a, err)
			}
			offers++
		}
	}
	if offers == 0 {
		t.Log("no human offer in these spots")
	}
}

// With its own 8-slot network, Grandmaster scores rollouts with the network
// from the first turn of an 8-player game (Expert's 6-slot network cannot).
func TestGrandmasterNet2ReadsEightPlayers(t *testing.T) {
	names := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	g, err := NewGame(names, newSplitMix(8))
	if err != nil {
		t.Fatal(err)
	}
	expert := NewExpertBot(1, Options{Budget: -1, Workers: 1})
	if expert.searchFor(g).useNet {
		t.Fatal("Expert's network cannot read 8 players")
	}
	o := gmTestOptions()
	o.Net2 = valuenet2.NewNet(valuenet2.NumFeatures, 8, 4, 1)
	gm := NewGrandmasterBot(1, o)
	if !gm.searchFor(g).useNet || gm.workers[0].vn2 == nil {
		t.Fatal("Grandmaster with its own network should score 8-player rollouts with it")
	}
	res, err := PlayMatch(MatchConfig{Levels: []Level{Grandmaster, Normal, Normal, Normal, Normal, Normal, Normal, Normal}, Seed: 3, MaxTurns: 60,
		NewBot: func(seat int, level Level, seed int64) Bot {
			if level == Grandmaster {
				return NewGrandmasterBot(seed, o)
			}
			return NewBot(level, seed, Options{})
		}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Seats[0].Rejected != 0 {
		t.Fatalf("%d refused actions", res.Seats[0].Rejected)
	}
}
