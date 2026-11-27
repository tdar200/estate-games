package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/hardai"
)

// grandmasterHarness serves real games with a cheap Grandmaster (the real
// level with few rollouts) for "grandmaster" seats and the production bots
// for the others.
func grandmasterHarness(t *testing.T) *harness {
	return newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
		c.Bots = func(level string, seed int64) Bot {
			if level == "grandmaster" {
				return EngineBotAdapter(hardai.NewGrandmasterBot(seed, hardai.Options{Budget: -1, Workers: 4, Rollouts: 12,
					GM: &hardai.GrandmasterOptions{TradeRollouts: 400, TradeHorizon: 1}}))
			}
			return RealBots(level, seed)
		}
	})
}

func TestRealBotsGrandmaster(t *testing.T) {
	for _, level := range []string{"hard", "expert", "grandmaster"} {
		if b, ok := RealBots(level, 1).(*engineBot); !ok || !b.quotesOwnPrice() {
			t.Fatalf("%s should be a hardai bot that names its own price", level)
		}
	}
	if RealBots("easy", 1).(*engineBot).quotesOwnPrice() {
		t.Fatal("easy must keep the Normal heuristic's quote")
	}
	if _, ok := RealBots("normal", 1).(*engineBot); ok {
		t.Fatal("normal is the heuristic itself")
	}
}

// A Grandmaster seat names its own trade price (never the Normal
// heuristic's), and an offer at that price is accepted.
func TestGrandmasterQuotesItsOwnPrice(t *testing.T) {
	h := grandmasterHarness(t)
	host := h.createRoom("Ann")
	r := h.post("/api/rooms/"+host.code+"/ai", map[string]any{"token": host.token, "difficulty": "grandmaster"})
	expectStatus(t, r, 200)
	gmID := players(r.body)[1]["id"].(string)
	expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "start"}), 200)
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
	// Give the computer a railroad it has no use for and Ann plenty of cash.
	h.srv.mu.Lock()
	rm := h.srv.rooms[host.code]
	g := rm.game.(*engineGame).g
	g.Properties[5].OwnerID = gmID
	g.PlayerByID(host.playerID).Cash = 3000
	h.srv.commit(rm)
	h.srv.mu.Unlock()

	q := h.post("/api/rooms/"+host.code+"/trade-quote", map[string]any{"token": host.token, "targetId": gmID, "requestProperties": []int{5}})
	if q.status == 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(q.raw, &e)
		if !strings.HasPrefix(e.Error, "No deal") {
			t.Fatalf("quote refused: %s", q.raw)
		}
		t.Skip("the cheap Grandmaster would not sell at any price here")
	}
	expectStatus(t, q, 200)
	var quote struct {
		OfferCash   int    `json:"offerCash"`
		RequestCash int    `json:"requestCash"`
		TargetID    string `json:"targetId"`
	}
	if err := json.Unmarshal(q.raw, &quote); err != nil || quote.OfferCash <= 0 || quote.TargetID != gmID {
		t.Fatalf("quote %s", q.raw)
	}
	h.srv.mu.Lock()
	recorded := len(rm.quotes) == 1 && rm.quotes[0].net == quote.OfferCash
	h.srv.mu.Unlock()
	if !recorded {
		t.Fatalf("the server must remember Grandmaster's own price ($%d)", quote.OfferCash)
	}
	offer := map[string]any{"type": "offer_trade", "targetId": gmID,
		"offer": map[string]any{"cash": quote.OfferCash, "properties": []int{}}, "request": map[string]any{"cash": 0, "properties": []int{5}}}
	expectStatus(t, h.act(host.code, host.token, offer), 200)
	waitFor(t, "the computer's answer", 15*time.Second, func() bool { return h.view(host.code).Phase != "trade" })
	h.srv.mu.Lock()
	owner := rm.game.(*engineGame).g.Properties[5].OwnerID
	h.srv.mu.Unlock()
	if owner != host.playerID {
		t.Fatal("the offer at Grandmaster's own price must be accepted")
	}
}

// currentPlayerID is the id of the player whose turn it is ("" if none).
func (v *gameView) currentPlayerID() string {
	if v == nil || v.CurrentIndex < 0 || v.CurrentIndex >= len(v.Players) {
		return ""
	}
	return v.Players[v.CurrentIndex].ID
}
