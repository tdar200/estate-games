package server

import "testing"

// A computer accepts an offer that meets the price it quoted this turn.
func TestComputerHonoursItsQuote(t *testing.T) {
	players := []playerView{{ID: "me", Kind: "human"}, {ID: "bot", Kind: "ai"}, {ID: "pal", Kind: "human"}}
	r := &room{meta: map[string]*playerMeta{}}
	r.last = &gameView{Status: "playing", Phase: "awaiting_roll", Players: players}
	r.rememberQuote("me", []byte(`{"token":"x","targetId":"bot","offerProperties":[3],"requestProperties":[1]}`),
		[]byte(`{"offerCash":82,"requestCash":0,"targetId":"bot"}`))
	r.rememberQuote("me", []byte(`{"targetId":"pal","offerProperties":[3],"requestProperties":[6]}`),
		[]byte(`{"offerCash":0,"requestCash":0,"targetId":"pal"}`))
	if len(r.quotes) != 1 {
		t.Fatalf("quotes for people must not be kept: %d", len(r.quotes))
	}
	trade := func(cash int, offer, request []int) *gameView {
		return &gameView{Status: "playing", Phase: "trade", Players: players, Trade: &tradeView{
			ProposerID: "me", TargetID: "bot",
			Offer:   tradeBundleView{Cash: cash, Properties: offer},
			Request: tradeBundleView{Properties: request},
		}}
	}
	if !r.quotedTrade(trade(82, []int{3}, []int{1}), "bot") {
		t.Fatal("the quoted deal must be accepted")
	}
	if !r.quotedTrade(trade(100, []int{3}, []int{1}), "bot") {
		t.Fatal("a better deal must be accepted too")
	}
	if r.quotedTrade(trade(80, []int{3}, []int{1}), "bot") {
		t.Fatal("less than the quote is up to the computer")
	}
	if r.quotedTrade(trade(82, []int{3}, []int{1, 6}), "bot") {
		t.Fatal("a different deal is up to the computer")
	}
	later := trade(82, []int{3}, []int{1})
	later.TurnCounts = map[string]int{"me": 1}
	if r.quotedTrade(later, "bot") {
		t.Fatal("quotes expire when the turn ends")
	}
}
