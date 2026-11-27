package holdem

import (
	"encoding/json"
	"math"
	"testing"
)

func TestOddsKnownMatchups(t *testing.T) {
	// Aces against kings all-in preflop: about 82% / 18%.
	h := mustHand(t, HandConfig{SmallBlind: 10, BigBlind: 20, Stacks: []int{1000, 1000},
		Deck: stackedDeck(t, []string{"AsAd", "KhKc"}, "2c7d9hJs3c"), PauseRunout: true})
	act(t, h, 1, Action{Type: ActAllIn})
	act(t, h, 0, call)
	o := h.Odds(1)
	if math.Abs(o[0].Win-0.82) > 0.015 || math.Abs(o[1].Win-0.175) > 0.015 || o[0].Tie > 0.01 {
		t.Fatalf("AA vs KK %+v", o)
	}
	// On the flop it is exact: enumerate the 990 turn/river pairs.
	h.DealNext()
	o = h.Odds(1)
	if s := o[0].Win + o[1].Win + o[0].Tie; math.Abs(s-1) > 1e-9 {
		t.Fatalf("shares should add up to 1: %+v", o)
	}
	// River: the result is known.
	h.DealNext()
	h.DealNext()
	if !h.Done {
		t.Fatal("hand should be over")
	}
}

func TestOddsIgnoreFoldedCards(t *testing.T) {
	odds := func(foldedHole string) []Odds {
		h := mustHand(t, HandConfig{SmallBlind: 10, BigBlind: 20, Stacks: []int{1000, 1000, 1000},
			Deck: stackedDeck(t, []string{"AsAd", "KhKc", foldedHole}, ""), PauseRunout: true})
		act(t, h, 2, fold)
		act(t, h, 0, Action{Type: ActAllIn})
		act(t, h, 1, call)
		return h.Odds(7)
	}
	a, b := odds("Ac2d"), odds("7h8h")
	if a[0] != b[0] || a[1] != b[1] || a[2] != (Odds{}) {
		t.Fatalf("folded cards changed the odds: %+v vs %+v", a, b)
	}
}

func TestTableShowsOddsOnlyDuringRunout(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann", "Ben")
	do(t, tb, ids[0], `{"type":"start"}`)
	odds := func() []*Odds {
		var v View
		raw, _ := tb.PublicState("", Viewer{Role: "table"})
		json.Unmarshal(raw, &v)
		var out []*Odds
		for _, p := range v.Players {
			out = append(out, p.Odds)
		}
		return out
	}
	for _, o := range odds() {
		if o != nil {
			t.Fatal("no odds while betting")
		}
	}
	do(t, tb, tb.Waiting()[0].PlayerID, `{"type":"allin"}`)
	do(t, tb, tb.Waiting()[0].PlayerID, `{"type":"call"}`)
	if tb.Phase != PhaseRunout {
		t.Fatalf("phase %s", tb.Phase)
	}
	o := odds()
	if o[0] == nil || o[1] == nil || math.Abs(o[0].Win+o[1].Win+o[0].Tie-1) > 0.001 {
		t.Fatalf("odds during run-out %+v %+v", o[0], o[1])
	}
	for tb.Phase == PhaseRunout {
		tb.Advance(tb.Now())
	}
	for _, o := range odds() {
		if o != nil {
			t.Fatal("no odds after the hand")
		}
	}
}

func TestSeatLimit(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann")
	if tb.SeatLimit != DefaultSeatLimit || tb.Settings.MaxPlayers != 8 {
		t.Fatalf("limit %d max %d", tb.SeatLimit, tb.Settings.MaxPlayers)
	}
	if err := tb.PerformAction(ids[0], json.RawMessage(`{"type":"settings","settings":{"maxPlayers":9}}`)); err == nil {
		t.Fatal("9 seats should be refused with the default limit")
	}
	for k := 0; k < 7; k++ {
		if _, err := tb.AddPlayer("P", KindHuman); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tb.AddPlayer("Ninth", KindHuman); err == nil {
		t.Fatal("the ninth player should not fit")
	}
	// The engine itself supports ten seats.
	seed := [32]byte{}
	big := NewTable("BIG", Options{Seed: &seed, SeatLimit: MaxSeats})
	id, _ := big.AddPlayer("Host", KindHuman)
	if err := big.PerformAction(id, json.RawMessage(`{"type":"settings","settings":{"maxPlayers":10}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestStandInNeverDecidesRebuys(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann", "Ben")
	tb.Timing.RebuyMs = 60_000
	do(t, tb, ids[0], `{"type":"settings","settings":{"rebuys":1,"blindSchedule":"off"}}`)
	do(t, tb, ids[0], `{"type":"start"}`)
	for guard := 0; guard < 20 && len(tb.Waiting()) > 0 && tb.Waiting()[0].Kind == "act"; guard++ {
		do(t, tb, tb.Waiting()[0].PlayerID, `{"type":"allin"}`)
		for tb.Phase == PhaseRunout {
			tb.Advance(tb.Now())
		}
	}
	w := tb.Waiting()
	if len(w) != 1 || w[0].Kind != "rebuy" {
		t.Fatalf("waiting %+v", w)
	}
	if a := tb.SafeAction(w[0].PlayerID); a != nil {
		t.Fatalf("stand-in decided a rebuy: %s", a)
	}
}
