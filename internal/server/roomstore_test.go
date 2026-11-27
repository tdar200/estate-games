package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// restartableHarness runs the real engine with a data directory, so a second
// harness on the same directory is the same server after a restart.
func restartableHarness(t *testing.T, dir string, clock *testClock) *harness {
	return newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
		c.DataDir = dir
		c.SaveDelay = 10 * time.Millisecond
		if clock != nil {
			c.Clock = clock.Now
		}
	})
}

// A restart (an update, a reboot) keeps every game: same code, same seats,
// and the phones' and table's tokens keep working.
func TestGamesSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	h := restartableHarness(t, dir, nil)
	tb := h.createTable()
	alice := h.device("Alice", "key", "198.51.100.51")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice", "tokenKind": "key"}, alice.opt())
	expectStatus(t, a, http.StatusOK)
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": "hard"}), http.StatusOK)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "set_rules", "rules": map[string]any{"loans": true}}), http.StatusOK)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	// Wait for a still moment, Alice's own turn to roll, so the computer
	// cannot move between reading the state and the restart.
	alicesRoll := func(view *gameView) bool {
		return view.Status == "playing" && view.Phase == "awaiting_roll" && view.Players[view.CurrentIndex].ID == a.str("playerId")
	}
	waitFor(t, "Alice's turn to roll", 30*time.Second, func() bool {
		view := h.view(tb.code)
		if alicesRoll(view) {
			return true
		}
		if view.OpeningOrder != nil && slices.Contains(view.OpeningOrder.PendingIDs, a.str("playerId")) {
			// Alice rolls for the order, again after a tie.
			h.act(tb.code, a.str("token"), map[string]any{"type": "roll_for_order"})
		} else if ids, _ := actorsToMove(view); slices.Contains(ids, a.str("playerId")) {
			// She answers auctions and offers on the computer's turn.
			h.act(tb.code, a.str("token"), aliceMove(view.Phase))
		}
		return false
	})
	before := h.state(tb.code, a.str("token"))
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, roomsFileName)); err != nil {
		t.Fatalf("no saved games: %v", err)
	}

	h2 := restartableHarness(t, dir, nil)
	after := h2.state(tb.code, a.str("token"))
	for _, key := range []string{"code", "status", "phase", "currentPlayerId", "viewerId", "rules"} {
		if dig(after, key) == nil && dig(before, key) != nil || !jsonEqual(dig(after, key), dig(before, key)) {
			t.Fatalf("%s changed across the restart: %v → %v", key, dig(before, key), dig(after, key))
		}
	}
	if len(players(after)) != len(players(before)) {
		t.Fatalf("players changed: %v → %v", players(before), players(after))
	}
	for _, p := range players(after) {
		if p["input"] == "ai" && p["difficulty"] != "hard" {
			t.Fatalf("computer level lost: %v", p)
		}
	}
	// The table's token still works, the phone can still rejoin by device, and
	// play goes on (the computer and the phone take turns).
	expectStatus(t, h2.get("/api/rooms/"+tb.code+"/state?token="+tb.token), http.StatusOK)
	expectStatus(t, h2.post("/api/rooms/"+tb.code+"/rejoin", map[string]any{}, alice.opt()), http.StatusOK)
	start := h2.view(tb.code).turnNumber()
	for i := 0; i < 400 && h2.view(tb.code).turnNumber() < start+2 && h2.view(tb.code).Status == "playing"; i++ {
		view := h2.view(tb.code)
		// Alice answers whenever she is the one to act: her own turn, and
		// also auctions or offers on the computer's turn.
		if ids, _ := actorsToMove(view); slices.Contains(ids, a.str("playerId")) {
			h2.act(tb.code, a.str("token"), aliceMove(view.Phase))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := h2.view(tb.code).turnNumber(); got < start+2 && h2.view(tb.code).Status == "playing" {
		t.Fatalf("play did not continue after the restart (turn %d → %d, phase %s)", start, got, h2.view(tb.code).Phase)
	}
}

func aliceMove(phase string) map[string]any {
	switch phase {
	case "awaiting_roll":
		return map[string]any{"type": "roll"}
	case "awaiting_purchase":
		return map[string]any{"type": "decline"}
	case "auction":
		return map[string]any{"type": "pass_auction"}
	case "debt":
		return map[string]any{"type": "bankrupt"}
	case "trade":
		return map[string]any{"type": "reject_trade"}
	case "loan_offer":
		return map[string]any{"type": "reject_loan"}
	case "card_confirmation":
		return map[string]any{"type": "confirm_card"}
	}
	return map[string]any{"type": "end_turn"}
}

// Games idle past the room lifetime are not brought back.
func TestStaleGamesNotRestored(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{}
	h := restartableHarness(t, dir, clock)
	tb := h.createTable()
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	clock.Advance(roomTTL + time.Minute)
	h2 := restartableHarness(t, dir, clock)
	expectError(t, h2.get("/api/rooms/"+tb.code+"/summary"), http.StatusNotFound, msgRoomMissing)
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// A table left open overnight with nobody moving is still in use: it comes
// back after a restart even though its last game action was hours ago.
func TestOpenTableSurvivesLongIdleRestart(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{}
	h := restartableHarness(t, dir, clock)
	tb := h.createTable()
	sock := h.dial("/ws/rooms/"+tb.code+"?token="+tb.token, nil)
	sock.next(2 * time.Second)
	clock.Advance(roomTTL + time.Hour) // the screen sat open all night
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	h2 := restartableHarness(t, dir, clock)
	expectStatus(t, h2.get("/api/rooms/"+tb.code+"/state?token="+tb.token), http.StatusOK)
}
