package server

import (
	"net/http"
	"testing"
	"time"
)

// When a player walks away mid-game, the computer plays for them after a
// short wait, so the table never freezes; when they come back they play again.
func TestComputerStandsInForAwayPlayer(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
		c.AutopilotDelay = 50 * time.Millisecond
	})
	tb := h.createTable()
	alice := h.device("Alice", "key", "198.51.100.61")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice", "tokenKind": "key"}, alice.opt())
	expectStatus(t, a, http.StatusOK)
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), http.StatusOK)
	sock := h.dial("/ws/rooms/"+tb.code+"?token="+a.str("token"), nil)
	sock.next(2 * time.Second)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)

	// The computer rolls for the order; then the game waits only on Alice.
	waitFor(t, "the game to wait on Alice", 10*time.Second, func() bool {
		ids, _ := actorsToMove(h.view(tb.code))
		return len(ids) == 1 && ids[0] == a.str("playerId")
	})
	time.Sleep(50 * time.Millisecond) // nothing is scheduled any more
	// Alice closes her phone. Nothing else happens: the disconnect alone must
	// start the stand-in countdown.
	sock.conn.CloseNow()
	h.srv.mu.Lock()
	r := h.srv.rooms[tb.code]
	h.srv.mu.Unlock()

	sawAutopilot := false
	waitFor(t, "the computer to play several turns for Alice", 20*time.Second, func() bool {
		h.srv.mu.Lock()
		if m := r.meta[a.str("playerId")]; m != nil && m.autopilot {
			sawAutopilot = true
		}
		h.srv.mu.Unlock()
		return h.view(tb.code).turnNumber() >= 6 || h.view(tb.code).Status == "finished"
	})
	if !sawAutopilot {
		t.Fatal("autopilot flag never set")
	}
	st := h.state(tb.code, tb.token)
	if p := playerByID(st, a.str("playerId")); p["autopilot"] != true || p["online"] != false {
		t.Fatalf("away player in state: %v", p)
	}
	if h.view(tb.code).Status == "finished" {
		return
	}

	// Alice comes back: the stand-in stops and the game waits for her again.
	h.state(tb.code, a.str("token"))
	h.srv.mu.Lock()
	h.srv.commit(r)
	h.srv.scheduleAI(r)
	if m := r.meta[a.str("playerId")]; m.autopilot {
		h.srv.mu.Unlock()
		t.Fatal("autopilot still on after Alice came back")
	}
	h.srv.mu.Unlock()
	waitFor(t, "a decision for Alice", 20*time.Second, func() bool {
		view := h.view(tb.code)
		ids, _ := actorsToMove(view)
		return len(ids) == 1 && ids[0] == a.str("playerId")
	})
	before := h.view(tb.code).Version
	for i := 0; i < 10; i++ {
		h.state(tb.code, a.str("token")) // she stays online
		time.Sleep(20 * time.Millisecond)
	}
	if after := h.view(tb.code).Version; after != before {
		t.Fatalf("the game moved on without Alice (version %d → %d)", before, after)
	}
}

func TestCardReadTime(t *testing.T) {
	short := cardReadTime("Advance to GO")
	long := cardReadTime("Advance to the nearest utility; if owned, pay ten times the dice total")
	if short != cardReadMin {
		t.Fatalf("short card: %v, want the minimum %v", short, cardReadMin)
	}
	if long <= short || long > cardReadMax {
		t.Fatalf("long card: %v", long)
	}
	s := &Server{aiDelay: defaultAIDelay}
	if got := s.cardHold("Advance to GO"); got != cardLead+cardReadMin {
		t.Fatalf("hold at the normal pace: %v", got)
	}
	s.aiDelay = defaultAIDelay / 10
	if got := s.cardHold("Advance to GO"); got != (cardLead+cardReadMin)/10 {
		t.Fatalf("hold scales with the computer delay: %v", got)
	}
}
