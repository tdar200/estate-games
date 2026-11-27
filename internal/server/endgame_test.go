package server

import (
	"net/http"
	"testing"
	"time"
)

// The host (or the PC) can end a game early; the richest player wins and
// everyone gets the finished state. Others can't.
func TestEndGameEarly(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
	})
	ana := h.device("Ana", "key", "198.51.100.91")
	g := h.post("/api/games", map[string]any{"name": "Ana"}, ana.opt())
	expectStatus(t, g, http.StatusCreated)
	code, host := g.str("code"), g.str("token")
	ben := h.device("Ben", "rocket", "198.51.100.92")
	b := h.post("/api/rooms/"+code+"/join", map[string]any{"name": "Ben"}, ben.opt())
	expectStatus(t, b, http.StatusOK)
	expectError(t, h.post("/api/rooms/"+code+"/end", map[string]any{"token": host}), http.StatusBadRequest, msgEndNotPlaying)
	expectStatus(t, h.act(code, host, map[string]any{"type": "start"}), http.StatusOK)

	expectError(t, h.post("/api/rooms/"+code+"/end", map[string]any{"token": b.str("token")}), http.StatusBadRequest, msgOnlyHost)
	end := h.post("/api/rooms/"+code+"/end", map[string]any{"token": host})
	expectStatus(t, end, http.StatusOK)
	if end.str("status") != "finished" || end.str("winnerId") == "" {
		t.Fatalf("after end: status %v winner %v", end.body["status"], end.body["winnerId"])
	}
	if st := h.state(code, b.str("token")); st["status"] != "finished" {
		t.Fatalf("Ben sees %v", st["status"])
	}
	// The host can start a new round straight away.
	expectStatus(t, h.post("/api/rooms/"+code+"/rematch", map[string]any{"token": host}), http.StatusOK)
}

func TestComputerSpeedSetting(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	if st := h.state(tb.code, tb.token); dig(st, "roomSettings", "computerSpeed") != "normal" {
		t.Fatalf("default roomSettings %v", st["roomSettings"])
	}
	expectError(t, h.post("/api/rooms/"+tb.code+"/settings", map[string]any{"token": tb.token, "computerSpeed": "warp"}), http.StatusBadRequest, msgBadSpeed)
	got := h.post("/api/rooms/"+tb.code+"/settings", map[string]any{"token": tb.token, "computerSpeed": "fast"})
	expectStatus(t, got, http.StatusOK)
	if dig(got.body, "roomSettings", "computerSpeed") != "fast" {
		t.Fatalf("after setting: %v", got.body["roomSettings"])
	}
	h.srv.mu.Lock()
	factor := h.srv.rooms[tb.code].speedFactor()
	h.srv.mu.Unlock()
	if factor >= 1 {
		t.Fatalf("fast should shorten the computer pause, factor %v", factor)
	}
	// Classic rooms keep the exact Node state shape: no roomSettings.
	room := h.createRoom("Ann")
	if st := h.state(room.code, room.token); st["roomSettings"] != nil {
		t.Fatal("classic rooms must not gain roomSettings")
	}
}
