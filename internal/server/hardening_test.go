package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Regression tests for the independent review of 3 Nov 2026 (server side).

// B16: a quoted price no longer binds the computer once the deeds it covers
// (or their colour group) changed: mortgaged, built on, set completed.
func TestQuoteVoidAfterDeedsChange(t *testing.T) {
	players := []playerView{{ID: "me", Kind: "human"}, {ID: "bot", Kind: "ai"}}
	board := []boardSpace{{Index: 1, Type: "property", Group: "brown"}, {Index: 3, Type: "property", Group: "brown"},
		{Index: 5, Type: "railroad"}, {Index: 15, Type: "railroad"}, {Index: 6, Type: "property", Group: "lightBlue"}}
	props := func(owner3, owner15 string, mortgaged1 bool) map[string]propertyView {
		return map[string]propertyView{"1": {OwnerID: "bot", Mortgaged: mortgaged1}, "3": {OwnerID: owner3},
			"5": {OwnerID: "bot"}, "15": {OwnerID: owner15}, "6": {OwnerID: "me"}}
	}
	r := &room{meta: map[string]*playerMeta{}, board: board}
	r.last = &gameView{Status: "playing", Phase: "awaiting_roll", Players: players, Properties: props("", "", false)}
	r.rememberQuote("me", []byte(`{"targetId":"bot","offerProperties":[6],"requestProperties":[1]}`), []byte(`{"offerCash":50,"requestCash":0,"targetId":"bot"}`))
	r.rememberQuote("me", []byte(`{"targetId":"bot","offerProperties":[],"requestProperties":[5]}`), []byte(`{"offerCash":120,"requestCash":0,"targetId":"bot"}`))
	trade := func(p map[string]propertyView, offer, request []int, cash int) *gameView {
		return &gameView{Status: "playing", Phase: "trade", Players: players, Properties: p, Trade: &tradeView{
			ProposerID: "me", TargetID: "bot", Offer: tradeBundleView{Cash: cash, Properties: offer}, Request: tradeBundleView{Properties: request}}}
	}
	if !r.quotedTrade(trade(props("", "", false), []int{6}, []int{1}, 50), "bot") {
		t.Fatal("unchanged deeds: the quote holds")
	}
	if r.quotedTrade(trade(props("", "", true), []int{6}, []int{1}, 50), "bot") {
		t.Fatal("the deed was mortgaged since the quote")
	}
	if r.quotedTrade(trade(props("me", "", false), []int{6}, []int{1}, 50), "bot") {
		t.Fatal("the asker completed more of the group since the quote")
	}
	if !r.quotedTrade(trade(props("", "", false), nil, []int{5}, 120), "bot") {
		t.Fatal("railroad quote holds")
	}
	if r.quotedTrade(trade(props("", "me", false), nil, []int{5}, 120), "bot") {
		t.Fatal("railroad holdings changed since the quote")
	}
}

// B5: in "choose dice" mode the stand-in rolls with dice for an away person.
func TestWithChosenDice(t *testing.T) {
	view := &gameView{Players: []playerView{{ID: "p", Kind: "human"}, {ID: "c", Kind: "ai"}}, Settings: json.RawMessage(`{"diceMode":"choice"}`)}
	got := withChosenDice(view, "p", json.RawMessage(`{"type":"roll"}`))
	var roll struct {
		Type string `json:"type"`
		Dice []int  `json:"dice"`
	}
	if err := json.Unmarshal(got, &roll); err != nil || roll.Type != "roll" || len(roll.Dice) != 2 ||
		roll.Dice[0] < 1 || roll.Dice[0] > 6 || roll.Dice[1] < 1 || roll.Dice[1] > 6 {
		t.Fatalf("person in choice mode: %s", got)
	}
	for _, c := range []struct {
		who, action string
		settings    string
	}{{"c", `{"type":"roll"}`, `{"diceMode":"choice"}`}, {"p", `{"type":"roll"}`, `{"diceMode":"random"}`},
		{"p", `{"type":"end_turn"}`, `{"diceMode":"choice"}`}, {"p", `{"type":"roll","dice":[2,2]}`, `{"diceMode":"choice"}`}} {
		v := *view
		v.Settings = json.RawMessage(c.settings)
		if out := withChosenDice(&v, c.who, json.RawMessage(c.action)); string(out) != c.action {
			t.Fatalf("%s %s %s changed to %s", c.who, c.action, c.settings, out)
		}
	}
}

// B5 end to end: an away player in "choose dice" mode no longer stalls the game.
func TestChoiceDiceStandInKeepsPlaying(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
		c.AutopilotDelay = 30 * time.Millisecond
	})
	tb := h.createTable()
	alice := h.device("Alice", "key", "198.51.100.101")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	expectStatus(t, a, http.StatusOK)
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), http.StatusOK)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "set_dice_mode", "mode": "choice"}), http.StatusOK)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second) // Alice's phone goes to sleep
	waitFor(t, "the stand-in to play Alice's turns", 20*time.Second, func() bool {
		h.state(tb.code, tb.token) // the table stays on
		v := h.view(tb.code)
		return v.TurnCounts[a.str("playerId")] >= 2 || v.Status == "finished"
	})
}

// B8: at "fast" the computers still leave a drawn card up for its full
// reading time; only the dice-and-hop lead gets shorter.
func TestCardHoldNeverShorterThanReading(t *testing.T) {
	s := &Server{aiDelay: defaultAIDelay}
	text := "Advance to the nearest utility; if owned, pay ten times the dice total"
	if got := s.cardHoldAt(text, computerSpeeds["fast"]); got < cardReadTime(text) {
		t.Fatalf("fast hold %v is shorter than the reading time %v", got, cardReadTime(text))
	}
	if got := s.cardHoldAt(text, 1); got != cardLead+cardReadTime(text) {
		t.Fatalf("normal hold %v", got)
	}
	if slow, normal := s.cardHoldAt(text, computerSpeeds["slow"]), s.cardHoldAt(text, 1); slow <= normal {
		t.Fatalf("slow hold %v should be longer than %v", slow, normal)
	}
}

// S4/S7: rooms full of computers stop when no person is connected, and play
// again when someone comes back.
func TestComputersWaitWhenNobodyIsConnected(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
		c.AutopilotDelay = 20 * time.Millisecond
	})
	ana := h.device("Ana", "key", "198.51.100.111")
	g := h.post("/api/games", map[string]any{"name": "Ana"}, ana.opt())
	expectStatus(t, g, http.StatusCreated)
	code := g.str("code")
	for i := 0; i < 2; i++ {
		expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": g.str("token"), "difficulty": "easy"}), http.StatusOK)
	}
	expectStatus(t, h.act(code, g.str("token"), map[string]any{"type": "start"}), http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second) // Ana leaves; the computers (and her stand-in) play on for a while
	start := h.view(code).Version
	waitFor(t, "play to go on for a while", 10*time.Second, func() bool { return h.view(code).Version > start+10 })

	h.clock.Advance(botIdleGrace)
	waitFor(t, "the computers to stop", 10*time.Second, func() bool {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		return h.srv.rooms[code].aiPaused
	})
	paused := h.view(code).Version
	time.Sleep(300 * time.Millisecond)
	if v := h.view(code).Version; v != paused {
		t.Fatalf("computers kept playing an empty room: version %d → %d", paused, v)
	}
	// Ana is back: they play again.
	h.state(code, g.str("token"))
	h.srv.mu.Lock()
	stillPaused := h.srv.rooms[code].aiPaused
	h.srv.mu.Unlock()
	if stillPaused {
		t.Fatal("computers still waiting after Ana came back")
	}
}

// S4: one device or one address can't fill the server with rooms; their own
// abandoned rooms make way, and the server machine itself is exempt.
func TestRoomQuotas(t *testing.T) {
	h := newHarness(t)
	d := h.device("Quinn", "key", "203.0.113.80")
	var codes []string
	for i := 0; i < maxRoomsPerDev; i++ {
		g := h.post("/api/games", map[string]any{"name": "Quinn"}, d.opt(), fromIP("203.0.113.80"))
		expectStatus(t, g, http.StatusCreated)
		codes = append(codes, g.str("code"))
	}
	expectError(t, h.post("/api/games", map[string]any{"name": "Quinn"}, d.opt(), fromIP("203.0.113.80")), http.StatusTooManyRequests, msgRoomQuota)
	// Once those lobbies are abandoned, they make way for a new one.
	h.clock.Advance(onlineWindow + time.Second)
	expectStatus(t, h.post("/api/games", map[string]any{"name": "Quinn"}, d.opt(), fromIP("203.0.113.80")), http.StatusCreated)
	h.srv.mu.Lock()
	gone := 0
	for _, code := range codes {
		if h.srv.rooms[code] == nil {
			gone++
		}
	}
	h.srv.mu.Unlock()
	if gone == 0 {
		t.Fatal("no abandoned room was cleared")
	}
	// Per address, without devices.
	for i := 0; i < maxRoomsPerIP; i++ {
		expectStatus(t, h.post("/api/rooms", map[string]any{"name": "P"}, fromIP("203.0.113.81")), http.StatusCreated)
	}
	expectError(t, h.post("/api/rooms", map[string]any{"name": "P"}, fromIP("203.0.113.81")), http.StatusTooManyRequests, msgRoomQuota)
	expectError(t, h.post("/api/tables", map[string]any{}, fromIP("203.0.113.81")), http.StatusTooManyRequests, msgRoomQuota)
	// The server machine itself (the PC table on the host) has no quota.
	for i := 0; i < maxRoomsPerIP+1; i++ {
		expectStatus(t, h.post("/api/tables", map[string]any{}), http.StatusCreated)
	}
}

// S5: made-up state tokens share one allowance per address and don't grow
// the limiter; expired buckets go on the sweep, not per request.
func TestRateLimiterBucketsPerAddress(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 700; i++ {
		h.get(fmt.Sprintf("/api/rooms/AAAAA/state?token=random%d", i), fromIP("203.0.113.90"))
	}
	if n := h.srv.limiter.size(); n > 3 {
		t.Fatalf("limiter grew to %d buckets", n)
	}
	expectStatus(t, h.get("/api/rooms/AAAAA/state?token=x", fromIP("203.0.113.90")), http.StatusTooManyRequests)
	// IPv6 clients are limited per /64.
	if addressKey("2001:db8:1:2:aaaa::1") != addressKey("2001:db8:1:2:bbbb::9") || addressKey("2001:db8:1:3::1") == addressKey("2001:db8:1:2::1") {
		t.Fatal("IPv6 addresses must group by /64")
	}
	l := newRateLimiter()
	now := time.Now()
	l.consume("a", 5, now)
	l.consume("b", 5, now)
	l.prune(now.Add(rateWindow))
	if l.size() != 0 {
		t.Fatalf("prune left %d buckets", l.size())
	}
}

// S8: only the forwarding proxy's own entry of X-Forwarded-For counts, and
// the LAN address is only told to clients on the LAN or tailnet.
func TestClientAddressAndServerInfo(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/server", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.7")
	if got := clientIP(req); got != "198.51.100.7" {
		t.Fatalf("clientIP %q", got)
	}
	req.RemoteAddr = "203.0.113.5:5000"
	if got := clientIP(req); got != "203.0.113.5" {
		t.Fatalf("a remote client can't claim an address: %q", got)
	}
	h := newHarness(t)
	if got := h.get("/api/server", fromIP("203.0.113.9")); got.body["lanURL"] != nil {
		t.Fatalf("public client sees the LAN address: %s", got.raw)
	}
	for _, ip := range []string{"192.168.1.5", "100.101.102.103"} {
		if got := h.get("/api/server", fromIP(ip)); got.body["lanURL"] != "http://192.168.1.20:3210" {
			t.Fatalf("%s: %s", ip, got.raw)
		}
	}
	if got := h.get("/api/server"); got.body["lanURL"] == nil {
		t.Fatal("the server machine itself sees the LAN address")
	}
}

// S6: rejoining from the same browser doesn't pile up sessions, and unused
// spectator sessions are dropped.
func TestSessionsDoNotPileUp(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "key", "198.51.100.121")
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt()), http.StatusOK)
	for i := 0; i < 8; i++ {
		h.clock.Advance(onlineWindow + time.Second)
		expectStatus(t, h.post("/api/rooms/"+tb.code+"/rejoin", map[string]any{}, alice.opt()), http.StatusOK)
	}
	count := func(role string) int {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		n := 0
		for _, sess := range h.srv.rooms[tb.code].order {
			if sess.role == role {
				n++
			}
		}
		return n
	}
	if n := count("player"); n > 2 {
		t.Fatalf("%d player sessions after repeated rejoins", n)
	}
	for i := 0; i < 10; i++ {
		expectStatus(t, h.post("/api/rooms/"+tb.code+"/watch", map[string]any{"name": "W"}), http.StatusOK)
	}
	h.clock.Advance(staleSession + time.Minute)
	h.state(tb.code, tb.token)
	h.srv.mu.Lock()
	h.srv.pruneRooms(h.srv.now())
	h.srv.mu.Unlock()
	if n := count("spectator"); n != 0 {
		t.Fatalf("%d stale spectator sessions kept", n)
	}
	if n := count("player"); n != 1 {
		t.Fatalf("Alice's last session must stay (it may be her only way back): %d", n)
	}
}

// S6: one address can't hold an unlimited number of sockets.
func TestSocketsPerAddressCapped(t *testing.T) {
	h := newHarness(t)
	h.srv.mu.Lock()
	h.srv.sockets[addressKey("203.0.113.130")] = maxSocketsPerIP
	h.srv.mu.Unlock()
	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+"/ws/device", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.130")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d", res.StatusCode)
	}
}

// B18: a computer that crashes doesn't take the server down.
func TestComputerCrashIsContained(t *testing.T) {
	logs := &syncBuffer{}
	h := newHarness(t, func(c *Config) {
		c.Bots = func(string, int64) Bot { return panicBot{} }
		c.Logger = log.New(logs, "", 0)
	})
	host := h.createRoom("Ann")
	expectStatus(t, h.post("/api/rooms/"+host.code+"/ai", map[string]any{"token": host.token}), http.StatusOK)
	expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "start"}), http.StatusOK)
	waitFor(t, "the computer's turn", 5*time.Second, func() bool {
		v := h.view(host.code)
		if v.Phase == "determining_order" {
			h.act(host.code, host.token, map[string]any{"type": "roll_for_order"})
		}
		if v.Phase == "awaiting_roll" && v.Players[v.CurrentIndex].ID == host.playerID {
			h.act(host.code, host.token, map[string]any{"type": "roll"})
		}
		if v.Phase == "awaiting_purchase" || v.Phase == "turn_complete" {
			if v.Players[v.CurrentIndex].ID == host.playerID {
				h.act(host.code, host.token, map[string]any{"type": "end_turn"})
				h.act(host.code, host.token, map[string]any{"type": "decline"})
			}
		}
		return strings.Contains(logs.String(), "crashed")
	})
	// Still serving.
	expectStatus(t, h.get("/api/rooms/"+host.code+"/state?token="+host.token), http.StatusOK)
	if st := h.state(host.code, host.token); !slices.Contains([]any{"playing"}, st["status"]) {
		t.Fatalf("status %v", st["status"])
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type panicBot struct{}

func (panicBot) ChooseAction(Game, []byte, string) (json.RawMessage, error) { panic("bot bug") }
