package hardai

import (
	"strings"
	"testing"

	"github.com/tdar200/estate-games/internal/engine"
)

func quoteTestOptions(level Level) Options {
	switch level {
	case Grandmaster:
		return gmTestOptions()
	case Expert:
		return Options{Budget: -1, Workers: 2, Rollouts: 40, MinRounds: 2}
	}
	return Options{Budget: -1, Workers: 2}
}

// People get the computer's own price. An offer at it is accepted; below it
// the computer counters at exactly that price (never lower), twice per deal
// and turn, then says no with its price; the counter, once accepted, settles
// at that price.
func TestHumanOffersAnsweredByPrice(t *testing.T) {
	spots := tradeSpots(t, 16, func(*engine.Game, string, string, int) bool { return true })
	for _, level := range []Level{Hard, Expert, Grandmaster} {
		checked := 0
		for i, s := range spots {
			if checked == 2 {
				break
			}
			b := NewBot(level, int64(40+i), quoteTestOptions(level)).(*HardBot)
			q := b.Quote(s.g, s.target, s.proposer, nil, []int{s.deed})
			if !q.OK || q.Net <= 20 {
				continue
			}
			checked++
			offer := func(g *engine.Game, cash int) {
				t.Helper()
				if err := engine.PerformAction(g, s.proposer, tradeAction(s.target, cash, nil, 0, []int{s.deed}), nil); err != nil {
					t.Fatalf("%v: offer $%d: %v", level, cash, err)
				}
			}
			if net, ok := b.QuoteDeal(s.g, s.target, s.proposer, nil, []int{s.deed}); !ok || net != q.Net {
				t.Fatalf("%v: QuoteDeal %d %v, Quote %d", level, net, ok, q.Net)
			}
			g := Clone(s.g)
			offer(g, q.Net)
			if a, _ := b.Choose(g, s.target); a.Type != engine.ActionAcceptTrade {
				t.Fatalf("%v: offered its price $%d for %s, answered %v", level, q.Net, engine.Board[s.deed].Name, a)
			}
			for round := 1; round <= maxCounters+1; round++ {
				g = Clone(s.g)
				offer(g, q.Net-10)
				a, _ := b.Choose(g, s.target)
				if round > maxCounters {
					if a.Type != engine.ActionRejectTrade || !strings.HasPrefix(a.Reason, "My price is still $") {
						t.Fatalf("%v: counter %d: want a plain no with its price, got %v", level, round, a)
					}
					break
				}
				if a.Type != engine.ActionCounterTrade || a.Request == nil || a.Request.Cash != q.Net || a.Offer == nil ||
					len(a.Offer.Properties) != 1 || a.Offer.Properties[0] != s.deed || a.Offer.Cash != 0 {
					t.Fatalf("%v: counter %d: want %s for $%d, got %v", level, round, engine.Board[s.deed].Name, q.Net, a)
				}
				if err := engine.PerformAction(g, s.target, a, nil); err != nil {
					t.Fatalf("%v: the engine refused the counter: %v", level, err)
				}
				cash := g.PlayerByID(s.proposer).Cash
				if g.Properties[s.deed].Mortgaged {
					cash -= engine.MortgageTransferFee(engine.Board[s.deed].Price)
				}
				if err := engine.PerformAction(g, s.proposer, engine.Action{Type: engine.ActionAcceptTrade}, nil); err != nil {
					t.Fatalf("%v: accepting the counter: %v", level, err)
				}
				if ownerOf(g, s.deed) != s.proposer || g.PlayerByID(s.proposer).Cash != cash-q.Net {
					t.Fatalf("%v: the accepted counter did not settle at $%d", level, q.Net)
				}
			}
		}
		if checked == 0 {
			t.Fatalf("%v: no spot got a price", level)
		}
	}
}

// Bots answer bots as before: a computer proposer gets the search's answer,
// never a counter-offer.
func TestBotOffersGetNoCounters(t *testing.T) {
	spots := tradeSpots(t, 4, func(*engine.Game, string, string, int) bool { return true })
	for i, s := range spots {
		g := Clone(s.g)
		g.PlayerByID(s.proposer).Kind = engine.KindAI
		if err := engine.PerformAction(g, s.proposer, tradeAction(s.target, 1, nil, 0, []int{s.deed}), nil); err != nil {
			t.Fatal(err)
		}
		b := NewBot(Expert, int64(i), quoteTestOptions(Expert)).(*HardBot)
		if a, _ := b.Choose(g, s.target); a.Type == engine.ActionCounterTrade || a.Reason != "" {
			t.Fatalf("a computer's offer got %v", a)
		}
	}
}

// Why no price works, in words.
func TestNoDealReasons(t *testing.T) {
	spots := tradeSpots(t, 20, func(g *engine.Game, proposer, target string, deed int) bool { return spaceGroup[deed] >= 0 })
	s := spots[0]
	g := Clone(s.g)
	grp := spaceGroup[s.deed]
	for _, index := range groupMembers[grp] {
		if index != s.deed {
			g.Properties[index].OwnerID = s.proposer
		}
	}
	tr := &engine.Trade{ProposerID: s.proposer, TargetID: s.target, Request: engine.TradeBundle{Properties: []int{s.deed}}}
	if r := noDealReason(g, s.target, s.proposer, tr, false); r != "No deal: that would give you a full set." {
		t.Fatalf("completing the person's set: %q", r)
	}
	if r := noDealReason(g, s.target, s.proposer, tr, true); r != "No deal: not for any price you can pay." {
		t.Fatalf("short of cash: %q", r)
	}
	for _, index := range groupMembers[grp] {
		g.Properties[index].OwnerID = s.target
	}
	if r := noDealReason(g, s.target, s.proposer, tr, false); r != "No deal: I won't break up my set." {
		t.Fatalf("breaking its set: %q", r)
	}
	if w := priceWords([]int{28}, []int{39}, 650); w != "$650 + Reservoir" {
		t.Fatalf("priceWords: %q", w)
	}
	if w := priceWords([]int{28}, []int{39}, -40); w != "Reservoir, and I pay you $40" {
		t.Fatalf("priceWords: %q", w)
	}
}
