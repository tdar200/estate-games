package server

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
)

func realHarness(t *testing.T) *harness {
	return newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
	})
}

// TestRealEngineCompatFlow drives the real rules engine through the §1 API.
func TestRealEngineCompatFlow(t *testing.T) {
	h := realHarness(t)
	host := h.createRoom("Ann")
	guest := h.join(host.code, "Bea")
	st := h.state(host.code, host.token)
	raw := h.get("/api/rooms/" + host.code + "/state?token=" + host.token).raw
	keys := keyOrder(t, raw)
	want := "code,status,phase,hostId,players,currentIndex,currentPlayerId,properties,turn,pendingPurchase,auction,debt,winnerId,lastCard,pendingCard,lastLanding,lastBankruptcy,lastMove,trade,openingOrder,turnSummary,settings,bank,log,version,board,groups,viewerId,viewerRole,viewerName,spectatorId,viewerIsHost,spectators,turnCounts,turnNumber"
	if strings.Join(keys, ",") != want {
		t.Fatalf("publicState key order:\n%s\nwant:\n%s", strings.Join(keys, ","), want)
	}
	if st["hostId"] != host.playerID || len(st["board"].([]any)) != 40 {
		t.Fatalf("state %v", st["hostId"])
	}
	expectError(t, h.act(host.code, guest.token, map[string]any{"type": "start"}), 400, "Only the host can start the game")
	expectError(t, h.act(host.code, host.token, map[string]any{"type": "roll"}), 400, "The game has not started")
	expectError(t, h.act(host.code, host.token, "roll"), 400, "Invalid action format")
	r := h.post("/api/rooms/"+host.code+"/ai", map[string]any{"token": host.token})
	expectStatus(t, r, 200)
	aiID := players(r.body)[2]["id"].(string)
	expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "start"}), 200)
	expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "roll_for_order"}), 200)
	expectStatus(t, h.act(host.code, guest.token, map[string]any{"type": "roll_for_order"}), 200)

	// The AI rolls for order by itself; ties re-roll; the 3 s countdown ends.
	var tokens = map[string]string{host.playerID: host.token, guest.playerID: guest.token}
	waitFor(t, "first turn", 15*time.Second, func() bool {
		view := h.view(host.code)
		if view.Phase == "determining_order" && view.OpeningOrder != nil {
			for _, id := range view.OpeningOrder.PendingIDs {
				if token := tokens[id]; token != "" {
					h.act(host.code, token, map[string]any{"type": "roll_for_order"})
				}
			}
		}
		return view.Phase == "awaiting_roll"
	})
	quote := h.post("/api/rooms/"+host.code+"/trade-quote", map[string]any{"token": host.token, "targetId": aiID, "offerProperties": []int{}})
	if quote.status != 200 && quote.status != 400 {
		t.Fatalf("quote status %d", quote.status)
	}
}

// TestRealEngineAIGame lets three real AIs play at a table for a while.
func TestRealEngineAIGame(t *testing.T) {
	h := realHarness(t)
	tb := h.createTable()
	for i := 0; i < 3; i++ {
		expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), 200)
	}
	sock := h.dial("/ws/rooms/"+tb.code+"?token="+tb.token, nil)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), 200)
	seen := map[string]bool{}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && !(seen["dice"] && seen["move"] && seen["buy"]) {
		msg := sock.next(10 * time.Second)
		if msg["type"] == "event" {
			seen[dig(msg, "event", "type").(string)] = true
		}
	}
	if !seen["dice"] || !seen["move"] || !seen["buy"] {
		t.Fatalf("AI game produced events %v", seen)
	}
}

type passBot struct{ calls atomic.Int64 }

func (b *passBot) Choose(g *engine.Game, playerID string) (engine.Action, bool) {
	b.calls.Add(1)
	return engine.Action{}, false
}

// TestEngineBotAdapter plugs an engine-native bot (hardai's interface) into
// a real game; "no opinion" falls back to internal/ai so play continues.
func TestEngineBotAdapter(t *testing.T) {
	bot := &passBot{}
	h := newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
		c.Bots = func(level string, seed int64) Bot { return EngineBotAdapter(bot) }
	})
	tb := h.createTable()
	h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": "hard"})
	h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token})
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), 200)
	waitFor(t, "a few turns", 15*time.Second, func() bool { return h.view(tb.code).turnNumber() >= 3 })
	if bot.calls.Load() == 0 {
		t.Fatal("engine bot was never consulted")
	}
}

// slowBot thinks for a long time, like a Hard decision on a slow machine.
type slowBot struct {
	think    time.Duration
	thinking atomic.Int32
	calls    atomic.Int64
}

func (b *slowBot) Choose(g *engine.Game, playerID string) (engine.Action, bool) {
	b.calls.Add(1)
	b.thinking.Add(1)
	defer b.thinking.Add(-1)
	time.Sleep(b.think)
	return engine.Action{}, false
}

// A computer that thinks for a while must not freeze everyone else: the
// server lets engine bots decide on a copy of the game, outside its lock.
func TestSlowBotDoesNotBlockServer(t *testing.T) {
	bot := &slowBot{think: 400 * time.Millisecond}
	h := newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
		c.Bots = func(level string, seed int64) Bot { return EngineBotAdapter(bot) }
	})
	tb := h.createTable()
	h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": "hard"})
	h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": "hard"})
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), 200)
	waitFor(t, "the bot to start thinking", 10*time.Second, func() bool { return bot.thinking.Load() > 0 })
	start := time.Now()
	expectStatus(t, h.get("/api/rooms/"+tb.code+"/state?token="+tb.token), 200)
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Fatalf("state request took %v while a bot was thinking", took)
	}
	waitFor(t, "the game to move on", 30*time.Second, func() bool { return h.view(tb.code).turnNumber() >= 3 })
}

// The production factory: easy, hard and expert are hardai bots, normal is
// internal/ai.
func TestRealBotsLevels(t *testing.T) {
	if _, ok := RealBots("normal", 1).(RealAI); !ok {
		t.Fatal("normal should be internal/ai")
	}
	for _, level := range []string{"easy", "hard", "expert"} {
		if _, ok := RealBots(level, 1).(thinkingBot); !ok {
			t.Fatalf("%s should be a hardai bot", level)
		}
	}
	if _, ok := RealBots("nonsense", 1).(RealAI); !ok {
		t.Fatal("unknown levels fall back to internal/ai")
	}
}

// Real Expert, Hard and Easy bots play a real game through the server.
func TestHardAndEasyBotsPlay(t *testing.T) {
	if testing.Short() {
		t.Skip("plays real Monte Carlo decisions")
	}
	h := newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
		c.Bots = RealBots
	})
	tb := h.createTable()
	for _, level := range []string{"expert", "hard", "easy", "normal"} {
		expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": level}), 200)
	}
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), 200)
	waitFor(t, "four rounds", 60*time.Second, func() bool { return h.view(tb.code).turnNumber() >= 12 })
	h.srv.mu.Lock()
	failures := h.srv.rooms[tb.code].aiFailures
	h.srv.mu.Unlock()
	if failures != 0 {
		t.Fatalf("%d AI actions were refused", failures)
	}
}
