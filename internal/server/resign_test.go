package server

import (
	"net/http"
	"testing"
	"time"
)

// A person may declare bankruptcy at any time in play (0.10.1): on their
// own turn, during a computer's turn, and in the middle of an auction; the
// computers play on.
func TestPersonMayResignAnyTime(t *testing.T) {
	cases := []struct {
		name string
		when func(v *gameView, ann string) bool
		prep func(h *harness, host seat, v *gameView) // a move of Ann's on the way
	}{
		{"own turn", func(v *gameView, ann string) bool {
			return v.Phase == "awaiting_roll" && v.currentPlayerID() == ann
		}, nil},
		{"a computer's turn", func(v *gameView, ann string) bool {
			return v.currentPlayerID() != ann && v.currentPlayerID() != "" && v.Phase != "determining_order" && v.Phase != "order_countdown"
		}, nil},
		{"mid-auction", func(v *gameView, ann string) bool {
			return v.Phase == "auction" && v.Auction != nil
		}, func(h *harness, host seat, v *gameView) {
			if v.Phase == "awaiting_purchase" && v.currentPlayerID() == host.playerID {
				h.act(host.code, host.token, map[string]any{"type": "decline"}) // start an auction
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := cheapHarness(t)
			host := h.createRoom("Ann")
			for i := 0; i < 2; i++ {
				expectStatus(t, h.post("/api/rooms/"+host.code+"/ai", map[string]any{"token": host.token, "difficulty": "hard"}), http.StatusOK)
			}
			expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "start"}), http.StatusOK)
			ann := person{playerID: host.playerID, token: host.token, ip: "198.51.100.61"}
			var at *gameView
			waitFor(t, c.name, 90*time.Second, func() bool {
				v := h.view(host.code)
				if c.when(v, ann.playerID) {
					at = v
					return true
				}
				if c.prep != nil {
					c.prep(h, host, v)
				}
				h.personMove(host.code, ann, v)
				return false
			})
			r := h.act(host.code, host.token, map[string]any{"type": "bankrupt"})
			if r.status != http.StatusOK {
				t.Fatalf("bankrupt in %s (%s): %d %s", c.name, at.Phase, r.status, r.raw)
			}
			v := h.view(host.code)
			if p := v.player(ann.playerID); p == nil || !p.Bankrupt {
				t.Fatal("Ann is still in the game")
			}
			// The computers play on (or one of them has won).
			waitFor(t, "the game to move on", 20*time.Second, func() bool {
				w := h.view(host.code)
				return w.Status == "finished" || w.Version > v.Version+3
			})
		})
	}
}
