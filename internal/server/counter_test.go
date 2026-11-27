package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// untilMyTurn plays host's moves (buying, declining offers) until host is to
// roll on its own turn.
func untilMyTurn(t *testing.T, h *harness, host seat) {
	t.Helper()
	waitFor(t, "Ann's turn", 60*time.Second, func() bool {
		view := h.view(host.code)
		if view.Phase == "determining_order" && view.OpeningOrder != nil {
			for _, id := range view.OpeningOrder.PendingIDs {
				if id == host.playerID {
					h.act(host.code, host.token, map[string]any{"type": "roll_for_order"})
				}
			}
		}
		mine := view.currentPlayerID() == host.playerID
		switch {
		case view.Phase == "awaiting_roll" && mine:
			return true
		case view.Phase == "turn_complete" && mine:
			h.act(host.code, host.token, map[string]any{"type": "end_turn"})
		case view.Phase == "awaiting_purchase" && mine:
			h.act(host.code, host.token, map[string]any{"type": "buy"})
		case view.Phase == "card_confirmation" && mine:
			h.act(host.code, host.token, map[string]any{"type": "confirm_card"})
		case view.Phase == "debt" && mine:
			h.act(host.code, host.token, map[string]any{"type": "pay_debt"})
		case view.Phase == "auction" && view.Auction != nil && view.Auction.BidderID != host.playerID:
			h.act(host.code, host.token, map[string]any{"type": "pass_auction"})
		case view.Phase == "trade" && view.Trade != nil && view.Trade.TargetID == host.playerID:
			h.act(host.code, host.token, map[string]any{"type": "reject_trade"})
		}
		return false
	})
}

type quoteReply struct {
	OfferCash   int    `json:"offerCash"`
	RequestCash int    `json:"requestCash"`
	TargetID    string `json:"targetId"`
	Error       string `json:"error"`
}

// Hard and Expert name their own price (never the Normal heuristic's), honour
// it, and answer an offer below it with a counter-offer at exactly that
// price: twice, then a plain no with the price. Accepting the counter
// settles the deal at the price.
func TestComputersCounterAtTheirPrice(t *testing.T) {
	for _, level := range []string{"hard", "expert"} {
		t.Run(level, func(t *testing.T) {
			h := cheapHarness(t)
			host := h.createRoom("Ann")
			r := h.post("/api/rooms/"+host.code+"/ai", map[string]any{"token": host.token, "difficulty": level})
			expectStatus(t, r, http.StatusOK)
			botID := players(r.body)[1]["id"].(string)
			expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "start"}), http.StatusOK)
			untilMyTurn(t, h, host)
			// The computer owns two railroads; Ann has plenty of cash.
			h.srv.mu.Lock()
			rm := h.srv.rooms[host.code]
			g := rm.game.(*engineGame).g
			for _, i := range []int{5, 15} {
				g.Properties[i].OwnerID, g.Properties[i].Mortgaged, g.Properties[i].Houses = botID, false, 0
			}
			g.PlayerByID(host.playerID).Cash = 3000
			h.srv.commit(rm)
			h.srv.mu.Unlock()

			price := func(deed int) int {
				t.Helper()
				q := h.post("/api/rooms/"+host.code+"/trade-quote", map[string]any{"token": host.token, "targetId": botID, "requestProperties": []int{deed}})
				var got quoteReply
				_ = json.Unmarshal(q.raw, &got)
				if q.status != http.StatusOK {
					if strings.HasPrefix(got.Error, "No deal") {
						t.Skipf("the cheap %s would not sell a railroad: %s", level, got.Error)
					}
					t.Fatalf("quote: %s", q.raw)
				}
				if got.OfferCash <= 20 || got.TargetID != botID {
					t.Fatalf("quote %s", q.raw)
				}
				// The server remembers the computer's own price, the one its
				// own search names (not the Normal heuristic's).
				h.srv.mu.Lock()
				defer h.srv.mu.Unlock()
				last := rm.quotes[len(rm.quotes)-1]
				gc, _ := gameCopy(rm.game)
				own := rm.meta[botID].bot.(*engineBot).quoteDeal(gc, botID, host.playerID, nil, []int{deed})
				if last.net != got.OfferCash || !own.OK || own.Net != got.OfferCash {
					t.Fatalf("quoted $%d, remembered $%d, the computer's own price %+v", got.OfferCash, last.net, own)
				}
				return got.OfferCash
			}
			offer := func(deed, cash int) {
				t.Helper()
				expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "offer_trade", "targetId": botID,
					"offer": map[string]any{"cash": cash, "properties": []int{}}, "request": map[string]any{"cash": 0, "properties": []int{deed}}}), http.StatusOK)
			}
			// answer waits for the computer's answer: the counter (if any).
			answer := func() *tradeView {
				t.Helper()
				var counter *tradeView
				waitFor(t, "the computer's answer", 20*time.Second, func() bool {
					v := h.view(host.code)
					if v.Phase == "trade" && v.Trade != nil && v.Trade.ProposerID == botID {
						counter = v.Trade
						return true
					}
					return v.Phase != "trade"
				})
				return counter
			}

			// Below the price: a counter at the price; accepting it settles there.
			p5 := price(5)
			offer(5, p5-10)
			c := answer()
			if c == nil || c.CounterTo == "" || c.TargetID != host.playerID || c.Request.Cash != p5 || !slices.Equal(c.Offer.Properties, []int{5}) {
				t.Fatalf("want a counter of $%d for the railroad, got %+v", p5, c)
			}
			cash := h.view(host.code).player(host.playerID).Cash
			expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "accept_trade"}), http.StatusOK)
			v := h.view(host.code)
			if v.Properties["5"].OwnerID != host.playerID || v.player(host.playerID).Cash != cash-p5 {
				t.Fatalf("the accepted counter did not settle at $%d", p5)
			}

			// Two counters per deal, then no; at the price, yes.
			p15 := price(15)
			for round := 1; round <= 3; round++ {
				offer(15, p15-10)
				c := answer()
				if round <= 2 {
					if c == nil || c.Request.Cash != p15 {
						t.Fatalf("round %d: want a counter at $%d, got %+v", round, p15, c)
					}
					expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "reject_trade"}), http.StatusOK)
					continue
				}
				if c != nil {
					t.Fatalf("a third counter: %+v", c)
				}
				log := h.state(host.code, host.token)["log"].([]any)
				if text := log[len(log)-1].(map[string]any)["text"].(string); !strings.Contains(text, "My price is still $") {
					t.Fatalf("the last word: %q", text)
				}
			}
			offer(15, p15)
			if answer() != nil || h.view(host.code).Properties["15"].OwnerID != host.playerID {
				t.Fatalf("an offer at its price ($%d) was not accepted", p15)
			}
		})
	}
}
