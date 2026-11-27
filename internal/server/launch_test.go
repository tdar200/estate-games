package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
)

// Launch blockers found in the review of 27 Nov 2026: a crash in a computer's
// move froze the table; a panic in socket or sweep code left the server
// locked; the 4-digit approval code could be guessed; an idle connected
// person stalled the game; the stand-in bankrupted an away player who could
// still pay.

// ---- 1. A failed computer step is retried, then replaced by the safe move ----------------

// onceCrashBot crashes on the first decision any seat asks it for (crashed
// is shared), then plays like the Normal heuristic.
type onceCrashBot struct{ crashed *atomic.Bool }

func (b onceCrashBot) ChooseAction(game Game, gameJSON []byte, playerID string) (json.RawMessage, error) {
	if b.crashed.CompareAndSwap(false, true) {
		panic("one-off bot bug")
	}
	return RealAI{}.ChooseAction(game, gameJSON, playerID)
}

// onceCrashThinker is onceCrashBot for a bot that thinks off the lock
// (thinkAsync), like Hard and Grandmaster.
type onceCrashThinker struct{ crashed *atomic.Bool }

func (b onceCrashThinker) ChooseAction(game Game, gameJSON []byte, playerID string) (json.RawMessage, error) {
	return RealAI{}.ChooseAction(game, gameJSON, playerID)
}

func (b onceCrashThinker) decide(_ *engine.Game, gameJSON []byte, playerID string, _ func(*engine.Game, engine.Action) bool) (json.RawMessage, uint64, error) {
	if b.crashed.CompareAndSwap(false, true) {
		panic("one-off bot bug")
	}
	action, err := RealAI{}.ChooseAction(nil, gameJSON, playerID)
	return action, 0, err
}

func (onceCrashThinker) discard(uint64) {}

// botTable starts a table game of two computers made by bots, with the table
// screen connected (so the computers play on their own).
func botTable(t *testing.T, bots BotFactory, opts ...harnessOption) (*harness, table, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	base := func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.Bots = bots
		c.AIDelay = 2 * time.Millisecond
		c.Logger = log.New(logs, "", 0)
	}
	h := newHarness(t, append([]harnessOption{base}, opts...)...)
	tb := h.createTable()
	for range 2 {
		expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), http.StatusOK)
	}
	h.dial("/ws/rooms/"+tb.code+"?token="+tb.token, nil).next(2 * time.Second)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	return h, tb, logs
}

// A computer whose move crashes once is asked again after a pause and the
// game goes on; before, nothing ever woke it and the table froze.
func TestComputerCrashOnceGameGoesOn(t *testing.T) {
	for _, thinks := range []bool{false, true} {
		t.Run(map[bool]string{false: "decides on the lock", true: "thinks off the lock"}[thinks], func(t *testing.T) {
			var crashed atomic.Bool
			h, tb, logs := botTable(t, func(string, int64) Bot {
				if thinks {
					return onceCrashThinker{&crashed}
				}
				return onceCrashBot{&crashed}
			})
			waitFor(t, "the computer's crash", 15*time.Second, crashed.Load)
			start := h.view(tb.code).turnNumber()
			waitFor(t, "the computers to play on after the crash", 30*time.Second, func() bool {
				v := h.view(tb.code)
				return v.turnNumber() >= start+3 || v.Status == "finished"
			})
			if !strings.Contains(logs.String(), "one-off bot bug") || !strings.Contains(logs.String(), "no move from the computer") {
				t.Fatalf("the crash and the retry are not in the log:\n%s", logs.String())
			}
		})
	}
}

// A computer that crashes on every decision gets the safe move after the
// retry, so the game still goes on.
func TestComputerAlwaysCrashingGetsSafeMove(t *testing.T) {
	h, tb, logs := botTable(t, func(string, int64) Bot { return panicBot{} })
	waitFor(t, "turns played by the safe move", 30*time.Second, func() bool {
		v := h.view(tb.code)
		return v.turnNumber() >= 4 || v.Status == "finished"
	})
	if !strings.Contains(logs.String(), "(2 in a row)") {
		t.Fatalf("no second failure before the safe move:\n%s", logs.String())
	}
}

// ---- 2. A panic under the server lock never leaves it held ---------------------------------

func lockFree(t *testing.T, h *harness) {
	t.Helper()
	waitFor(t, "the server lock to be free", 5*time.Second, func() bool {
		if h.srv.mu.TryLock() {
			h.srv.mu.Unlock()
			return true
		}
		return false
	})
}

// breakGame makes every snapshot and state of the room panic (as a bug in
// state code would) until the returned function restores it.
func breakGame(h *harness, code string) (r *room, restore func()) {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	r = h.srv.rooms[code]
	game := r.game
	r.game = nil
	return r, func() {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		r.game = game
	}
}

func TestRoomSocketOpenPanicReleasesLock(t *testing.T) {
	logs := &syncBuffer{}
	h := newHarness(t, func(c *Config) { c.Logger = log.New(logs, "", 0) })
	host := h.createRoom("Ann")
	r, restore := breakGame(h, host.code)
	sock := h.dial("/ws/rooms/"+host.code+"?token="+host.token, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := sock.conn.Read(ctx); err == nil {
		t.Fatal("the socket opened on a broken game")
	}
	lockFree(t, h)
	restore()
	h.srv.mu.Lock()
	conns, open := len(r.conns), r.sessions[host.token].conns
	h.srv.mu.Unlock()
	if conns != 0 || open != 0 {
		t.Fatalf("the refused socket is still counted: %d sockets, session %d", conns, open)
	}
	if !strings.Contains(logs.String(), "panic in opening a room socket") {
		t.Fatalf("panic not logged:\n%s", logs.String())
	}
	expectStatus(t, h.get("/api/rooms/"+host.code+"/state?token="+host.token), http.StatusOK)
}

func TestRoomSocketClosePanicReleasesLock(t *testing.T) {
	logs := &syncBuffer{}
	h := newHarness(t, func(c *Config) { c.Logger = log.New(logs, "", 0) })
	host := h.createRoom("Ann")
	sock := h.dial("/ws/rooms/"+host.code+"?token="+host.token, nil)
	sock.next(2 * time.Second)
	_, restore := breakGame(h, host.code)
	sock.conn.CloseNow() // Ann goes offline: the presence change commits and panics
	waitFor(t, "the close to be handled", 5*time.Second, func() bool {
		return strings.Contains(logs.String(), "panic in closing a room socket")
	})
	lockFree(t, h)
	restore()
	expectStatus(t, h.get("/api/rooms/"+host.code+"/state?token="+host.token), http.StatusOK)
}

func TestDeviceSocketOpenPanicReleasesLock(t *testing.T) {
	logs := &syncBuffer{}
	h := newHarness(t, func(c *Config) { c.Logger = log.New(logs, "", 0) })
	ann := h.device("Ann", "key", "198.51.100.120")
	h.srv.mu.Lock()
	friends := h.srv.deviceConns
	h.srv.deviceConns = nil // registering the socket now panics
	h.srv.mu.Unlock()
	header := http.Header{}
	header.Set(deviceHeader, ann.id+":"+ann.secret)
	sock := h.dial("/ws/device", header)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := sock.conn.Read(ctx); err == nil {
		t.Fatal("the socket opened although registering it failed")
	}
	lockFree(t, h)
	h.srv.mu.Lock()
	h.srv.deviceConns = friends
	h.srv.mu.Unlock()
	if !strings.Contains(logs.String(), "panic in opening a device socket") {
		t.Fatalf("panic not logged:\n%s", logs.String())
	}
}

// A crash in the sweep is logged and confined to its room: the lock is free,
// the other rooms are still swept, and the sweep loop (and the process) live.
func TestSweepCrashIsContained(t *testing.T) {
	logs := &syncBuffer{}
	h := newHarness(t, func(c *Config) { c.Logger = log.New(logs, "", 0) })
	broken := h.createRoom("Ann")
	other := h.createRoom("Bea")
	_, restore := breakGame(h, broken.code)
	h.srv.mu.Lock()
	h.srv.rooms[broken.code].presenceSig = "stale" // both rooms commit on the sweep
	h.srv.rooms[other.code].presenceSig = "stale"
	h.srv.mu.Unlock()
	last := h.srv.now()
	if h.srv.sweepOnce(&last) {
		t.Fatal("the sweep stopped")
	}
	lockFree(t, h)
	restore()
	h.srv.mu.Lock()
	sig := h.srv.rooms[other.code].presenceSig
	h.srv.mu.Unlock()
	if sig == "stale" {
		t.Fatal("a crash in one room stopped the sweep of the others")
	}
	if !strings.Contains(logs.String(), "sweep crashed") {
		t.Fatalf("crash not logged:\n%s", logs.String())
	}
	expectStatus(t, h.get("/api/rooms/"+broken.code+"/state?token="+broken.token), http.StatusOK)
}

// ---- 3. Wrong approval codes are capped per game, over every request ----------------------

func TestWrongCodesCappedPerGameForSeats(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "key", "198.51.100.130")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	expectStatus(t, a, http.StatusOK)
	h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token})
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second)
	h.state(tb.code, tb.token) // the table polls; Alice is away
	seat := a.str("playerId")
	reclaim := func(d device, ip, code string) response {
		body := map[string]any{"playerId": seat}
		if code != "" {
			body["approvalCode"] = code
		}
		return h.post("/api/rooms/"+tb.code+"/reclaim", body, d.opt(), fromIP(ip))
	}

	// Strangers on new devices and new addresses use up the game's budget.
	var eve device
	var ip string
	for i := 0; i < maxRoomWrongCodes; i++ {
		if i%maxClaimAttempts == 0 {
			ip = fmt.Sprintf("203.0.113.%d", 140+i)
			eve = h.device("Eve", "rocket", ip)
			expectStatus(t, reclaim(eve, ip, ""), http.StatusAccepted)
		}
		expectError(t, reclaim(eve, ip, "zzzz"), http.StatusForbidden, msgClaimWrongCode)
	}

	// Now even the right code is refused, whoever types it.
	mallory := h.device("Mallory", "teapot", "203.0.113.160")
	ask := reclaim(mallory, "203.0.113.160", "")
	expectStatus(t, ask, http.StatusAccepted)
	code := ""
	for _, c := range stateClaims(h.state(tb.code, tb.token)) {
		if c["id"] == ask.str("claim", "id") {
			code, _ = c["code"].(string)
		}
	}
	if code == "" {
		t.Fatal("the table doesn't show the request")
	}
	expectError(t, reclaim(mallory, "203.0.113.160", code), http.StatusForbidden, msgClaimCodesShut)
	if !strings.Contains(strings.ToLower(msgClaimCodesShut), "code") {
		t.Fatal("the asking screen shows a refusal inline only if it mentions the code")
	}

	// The prompt still works: the table lets Mallory in.
	expectStatus(t, h.decide(tb.code, tb.token, ask.str("claim", "id"), "approve"), http.StatusOK)
	in := reclaim(mallory, "203.0.113.160", "")
	expectStatus(t, in, http.StatusOK)
	if in.str("token") == "" {
		t.Fatalf("approved device got no session: %s", in.raw)
	}

	// Once the window has passed, codes are checked again.
	h.clock.Advance(wrongCodeWindow)
	h.state(tb.code, tb.token)
	nia := h.device("Nia", "key", "203.0.113.170")
	ask = reclaim(nia, "203.0.113.170", "")
	expectStatus(t, ask, http.StatusAccepted)
	code = ""
	for _, c := range stateClaims(h.state(tb.code, tb.token)) {
		if c["id"] == ask.str("claim", "id") {
			code, _ = c["code"].(string)
		}
	}
	expectStatus(t, reclaim(nia, "203.0.113.170", code), http.StatusOK)
}

func TestWrongCodesCappedPerGameForTableScreen(t *testing.T) {
	h := newHarness(t)
	pc := h.device("PC", "", "198.51.100.180")
	made := h.post("/api/tables", map[string]any{}, pc.opt(), fromIP("198.51.100.180"))
	expectStatus(t, made, http.StatusCreated)
	code := made.str("code")
	alice := h.device("Alice", "key", "198.51.100.181")
	a := h.post("/api/rooms/"+code+"/join", map[string]any{"name": "Alice"}, alice.opt(), fromIP("198.51.100.181"))
	expectStatus(t, a, http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second)
	h.state(code, a.str("token")) // the table screen is gone; Alice (the host) is here
	resume := func(d device, ip, approval string) response {
		body := map[string]any{}
		if approval != "" {
			body["approvalCode"] = approval
		}
		return h.post("/api/tables/"+code+"/resume", body, d.opt(), fromIP(ip))
	}

	var tv device
	var ip string
	for i := 0; i < maxRoomWrongCodes; i++ {
		if i%maxClaimAttempts == 0 {
			ip = fmt.Sprintf("203.0.113.%d", 190+i)
			tv = h.device("TV", "", ip)
			expectStatus(t, resume(tv, ip, ""), http.StatusAccepted)
		}
		expectError(t, resume(tv, ip, "zzzz"), http.StatusForbidden, msgClaimWrongCode)
	}

	screen := h.device("Screen", "", "203.0.113.210")
	ask := resume(screen, "203.0.113.210", "")
	expectStatus(t, ask, http.StatusAccepted)
	right := ""
	for _, c := range stateClaims(h.state(code, a.str("token"))) {
		if c["id"] == ask.str("claim", "id") {
			right, _ = c["code"].(string)
		}
	}
	if right == "" {
		t.Fatal("the host doesn't see the request")
	}
	expectError(t, resume(screen, "203.0.113.210", right), http.StatusForbidden, msgClaimCodesShut)
	expectStatus(t, h.decide(code, a.str("token"), ask.str("claim", "id"), "approve"), http.StatusOK)
	got := resume(screen, "203.0.113.210", "")
	expectStatus(t, got, http.StatusOK)
	if dig(got.body, "state", "viewerRole") != "table" {
		t.Fatalf("resume result %s", got.raw)
	}
}

// ---- 4. A connected person who stops playing gets the stand-in -----------------------------

// idleGame starts a table game of Alice (who stays connected on her phone)
// and one computer; it returns once the game waits on Alice alone.
func idleGame(t *testing.T) (h *harness, tb table, alice seat, r *room) {
	t.Helper()
	h = newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
	})
	tb = h.createTable()
	phone := h.device("Alice", "key", "198.51.100.220")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice", "tokenKind": "key"}, phone.opt())
	expectStatus(t, a, http.StatusOK)
	alice = seat{tb.code, a.str("token"), a.str("playerId")}
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), http.StatusOK)
	h.dial("/ws/rooms/"+tb.code+"?token="+alice.token, nil).next(2 * time.Second)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	waitFor(t, "the game to wait on Alice", 10*time.Second, func() bool {
		ids, _ := actorsToMove(h.view(tb.code))
		return len(ids) == 1 && ids[0] == alice.playerID
	})
	h.srv.mu.Lock()
	r = h.srv.rooms[tb.code]
	h.srv.mu.Unlock()
	return h, tb, alice, r
}

func autopilotOn(h *harness, r *room, playerID string) bool {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	m := r.meta[playerID]
	return m != nil && m.autopilot
}

// idleDue reports whether the stand-in would take over an idle person now.
// It only reads their idle clock, so it never switches the stand-in on.
func idleDue(h *harness, r *room, playerID string) bool {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	since, ok := r.idleFrom[playerID]
	return ok && !h.srv.now().Before(since.Add(idleStandInDelay))
}

func TestIdleConnectedPlayerGetsStandIn(t *testing.T) {
	h, tb, alice, r := idleGame(t)
	before := h.view(tb.code).Version

	// Connected but doing nothing: nothing happens for two minutes.
	h.clock.Advance(idleStandInDelay - 10*time.Second)
	if idleDue(h, r, alice.playerID) {
		t.Fatal("the stand-in is due before two minutes")
	}
	time.Sleep(50 * time.Millisecond)
	if h.view(tb.code).Version != before || autopilotOn(h, r, alice.playerID) {
		t.Fatal("the game moved on early")
	}

	// After two minutes the stand-in plays for her, exactly as when away.
	h.clock.Advance(11 * time.Second)
	waitFor(t, "the stand-in to play for idle Alice", 15*time.Second, func() bool {
		ids, _ := actorsToMove(h.view(tb.code))
		return autopilotOn(h, r, alice.playerID) && (len(ids) != 1 || ids[0] != alice.playerID)
	})
	p := playerByID(h.state(tb.code, tb.token), alice.playerID)
	if p["autopilot"] != true || p["online"] != true {
		t.Fatalf("idle player in state: %v", p)
	}

	// Any command from her seat (even a refused one) hands it back.
	h.act(tb.code, alice.token, map[string]any{"type": "not_a_move"})
	if autopilotOn(h, r, alice.playerID) {
		t.Fatal("still standing in after Alice's command")
	}
	if p := playerByID(h.state(tb.code, tb.token), alice.playerID); p["autopilot"] == true {
		t.Fatalf("state still shows the stand-in: %v", p)
	}
	waitFor(t, "a decision for Alice", 20*time.Second, func() bool {
		v := h.view(tb.code)
		ids, _ := actorsToMove(v)
		return v.Status != "playing" || (len(ids) == 1 && ids[0] == alice.playerID)
	})
	if h.view(tb.code).Status != "playing" {
		return
	}
	waiting := h.view(tb.code).Version
	time.Sleep(200 * time.Millisecond)
	if h.view(tb.code).Version != waiting {
		t.Fatal("the stand-in played on after handing the seat back")
	}

	// Each command restarts her two minutes.
	h.clock.Advance(idleStandInDelay - 20*time.Second)
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/not-selling", map[string]any{"token": alice.token, "spaceIndex": 39, "on": true}), http.StatusOK)
	h.clock.Advance(30 * time.Second)
	if idleDue(h, r, alice.playerID) {
		t.Fatal("a command didn't restart the idle clock")
	}
	h.clock.Advance(idleStandInDelay)
	if !idleDue(h, r, alice.playerID) {
		t.Fatal("the idle clock stopped")
	}
}

// A keyboard seat counts as connected while the table screen is, so it gets
// the same idle stand-in, and its commands (from the table, with its seatId)
// hand it back.
func TestIdleKeyboardSeatGetsStandIn(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
	})
	tb := h.createTable()
	pad := h.addSeat(tb, "keyboard", "Kim", "key", nil)
	expectStatus(t, pad, http.StatusCreated)
	kim := dig(pad.body, "state", "localSeats", "0", "playerId").(string)
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), http.StatusOK)
	h.dial("/ws/rooms/"+tb.code+"?token="+tb.token, nil).next(2 * time.Second)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	waitFor(t, "the game to wait on Kim", 10*time.Second, func() bool {
		ids, _ := actorsToMove(h.view(tb.code))
		return len(ids) == 1 && ids[0] == kim
	})
	h.srv.mu.Lock()
	r := h.srv.rooms[tb.code]
	h.srv.mu.Unlock()
	h.clock.Advance(idleStandInDelay + time.Second)
	waitFor(t, "the stand-in to play for Kim", 15*time.Second, func() bool { return autopilotOn(h, r, kim) })
	if p := playerByID(h.state(tb.code, tb.token), kim); p["autopilot"] != true || p["online"] != true {
		t.Fatalf("idle keyboard seat in state: %v", p)
	}
	h.act(tb.code, tb.token, map[string]any{"type": "not_a_move"}, map[string]any{"seatId": pad.str("seatId")})
	if autopilotOn(h, r, kim) {
		t.Fatal("a command from the keyboard seat didn't hand it back")
	}
}

// ---- 5. The stand-in and computers raise cash before going bankrupt ------------------------

// putInDebt makes it playerID's turn, in debt: they own the light blue set
// (6, 8, 9) with two houses on each (six in all), have no cash, and owe the
// bank amount; the loans house rule is on. The game must be past the turn
// order.
func putInDebt(h *harness, code, playerID string, amount int, more func(g *engine.Game)) {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	r := h.srv.rooms[code]
	g := r.game.(*engineGame).g
	g.Settings.Rules.Loans = true
	for i, p := range g.Players {
		if p.ID == playerID {
			g.CurrentIndex = i
			p.Cash = 0
			p.InJail = false
		}
	}
	for _, index := range []int{6, 8, 9} {
		g.Properties[index] = &engine.PropertyState{OwnerID: playerID, Houses: 2}
	}
	g.PendingPurchase, g.Auction, g.Trade, g.LoanOffer, g.PendingCard = nil, nil, nil, nil, nil
	if g.Turn != nil {
		g.Turn.PlayerID = playerID
	}
	g.Phase = engine.PhaseDebt
	g.Debt = &engine.Debt{PayerID: playerID, Amount: amount, Reason: "Stamp Duty",
		After: &engine.DebtAfter{Kind: engine.AfterPhase, Phase: engine.PhaseTurnComplete}}
	// The heuristic asks for a loan once per debt, which it finds in the log.
	g.Log = append(g.Log, engine.LogEntry{ID: "log_debt_test", Text: g.PlayerByID(playerID).Name + " must raise $" + strconv.Itoa(amount) + " to pay Stamp Duty"})
	if more != nil {
		more(g)
	}
	g.Version++
	h.srv.commit(r)
	h.srv.scheduleAI(r)
}

// pastOrder waits for the turn order to be settled (people roll for it via
// the stand-in or themselves).
func pastOrder(t *testing.T, h *harness, code string) {
	t.Helper()
	waitFor(t, "the turn order", 15*time.Second, func() bool {
		switch h.view(code).Phase {
		case "awaiting_roll", "turn_complete", "awaiting_purchase":
			return true
		}
		return false
	})
}

func houses(h *harness, code, playerID string) (total int) {
	for _, prop := range h.view(code).Properties {
		if prop.OwnerID == playerID {
			total += prop.Houses
		}
	}
	return total
}

// An away player with six houses who owes $300, with loans on: the heuristic
// asks for a loan, which the stand-in never does for them, and the stand-in
// used to declare them bankrupt instead. It sells and mortgages, and pays.
func TestStandInSellsBeforeBankruptcy(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
		c.AutopilotDelay = 20 * time.Millisecond
	})
	tb := h.createTable()
	var people []seat
	for i, name := range []string{"Alice", "Bob"} {
		d := h.device(name, "key", fmt.Sprintf("198.51.100.%d", 230+i))
		res := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": name}, d.opt())
		expectStatus(t, res, http.StatusOK)
		people = append(people, seat{tb.code, res.str("token"), res.str("playerId")})
	}
	h.dial("/ws/rooms/"+tb.code+"?token="+tb.token, nil).next(2 * time.Second)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second) // both away: the stand-in plays for them
	h.srv.mu.Lock()
	h.srv.scheduleAI(h.srv.rooms[tb.code])
	h.srv.mu.Unlock()
	pastOrder(t, h, tb.code)

	alice := people[0].playerID
	putInDebt(h, tb.code, alice, 300, nil)
	waitFor(t, "Alice's debt to be settled", 20*time.Second, func() bool {
		v := h.view(tb.code)
		me := v.player(alice)
		return me.Bankrupt || v.Debt == nil || v.Debt.PayerID != alice
	})
	if me := h.view(tb.code).player(alice); me.Bankrupt {
		t.Fatal("the stand-in declared Alice bankrupt although she could pay")
	}
	if n := houses(h, tb.code, alice); n == 6 {
		t.Fatal("no house sold")
	}
}

// pledgeMortgageBot, in debt, insists on mortgaging the deed it pledged for
// a loan (the engine refuses: it is the lender's security); otherwise it
// plays like the Normal heuristic.
type pledgeMortgageBot struct{ index int }

func (b pledgeMortgageBot) ChooseAction(game Game, gameJSON []byte, playerID string) (json.RawMessage, error) {
	var v gameView
	if json.Unmarshal(gameJSON, &v) == nil && v.Phase == "debt" && v.Debt != nil && v.Debt.PayerID == playerID {
		return json.RawMessage(fmt.Sprintf(`{"type":"mortgage","spaceIndex":%d}`, b.index)), nil
	}
	return RealAI{}.ChooseAction(game, gameJSON, playerID)
}

// A computer whose mortgage of a pledged deed is refused used to fall back to
// bankruptcy; now it sells its houses (and mortgages free deeds) and pays.
func TestRefusedComputerRaisesCashBeforeBankruptcy(t *testing.T) {
	h, tb, _ := botTable(t, func(string, int64) Bot { return pledgeMortgageBot{index: 5} })
	pastOrder(t, h, tb.code)
	view := h.view(tb.code)
	debtor, lender := view.Players[0].ID, view.Players[1].ID
	putInDebt(h, tb.code, debtor, 300, func(g *engine.Game) {
		g.Properties[5] = &engine.PropertyState{OwnerID: debtor}
		g.Loans = append(g.Loans, &engine.Loan{ID: "loan_test", LenderID: lender, BorrowerID: debtor,
			Principal: 100, Repay: 110, DueTurn: 999, Collateral: []int{5}, Status: "active"})
	})
	waitFor(t, "the computer's debt to be settled", 20*time.Second, func() bool {
		v := h.view(tb.code)
		return v.player(debtor).Bankrupt || v.Debt == nil || v.Debt.PayerID != debtor
	})
	v := h.view(tb.code)
	if v.player(debtor).Bankrupt {
		t.Fatal("the computer went bankrupt although it could sell and mortgage")
	}
	if houses(h, tb.code, debtor) == 6 {
		t.Fatal("no house sold")
	}
	if prop := v.Properties["5"]; prop.Mortgaged {
		t.Fatal("the pledged deed was mortgaged")
	}
}

// raiseCashAction only offers what the engine allows: the tallest building
// first, never a pledged deed, and nothing once all is sold and mortgaged.
func TestRaiseCashActionFollowsTheEngine(t *testing.T) {
	g := engine.CreateGame("TEST")
	a, _ := engine.AddPlayer(g, "Ann", engine.KindHuman)
	b, _ := engine.AddPlayer(g, "Ben", engine.KindHuman)
	g.Status, g.Phase, g.CurrentIndex = engine.StatusPlaying, engine.PhaseDebt, 0
	g.Settings.Rules.Loans = true
	a.Cash = 0
	g.Debt = &engine.Debt{PayerID: a.ID, Amount: 1000}
	g.Properties[6] = &engine.PropertyState{OwnerID: a.ID, Houses: 2}
	g.Properties[8] = &engine.PropertyState{OwnerID: a.ID, Houses: 3}
	g.Properties[9] = &engine.PropertyState{OwnerID: a.ID, Houses: 2}
	g.Properties[5] = &engine.PropertyState{OwnerID: a.ID}
	g.Loans = []*engine.Loan{{ID: "loan_1", LenderID: b.ID, BorrowerID: a.ID, Principal: 100, Repay: 110, DueTurn: 99, Collateral: []int{5}, Status: "active"}}
	game := &engineGame{g: g}

	if got := string(raiseCashAction(game, a.ID, nil)); got != `{"type":"sell_building","spaceIndex":8}` {
		t.Fatalf("first move %s, want the tallest building", got)
	}
	for range 7 {
		next := raiseCashAction(game, a.ID, nil)
		if actionType(next) != engine.ActionSellBuilding {
			t.Fatalf("%s while houses are left", next)
		}
		if err := game.PerformAction(a.ID, next); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		next := raiseCashAction(game, a.ID, nil)
		if actionType(next) != engine.ActionMortgage || strings.Contains(string(next), `"spaceIndex":5`) {
			t.Fatalf("%s: want a free deed mortgaged", next)
		}
		if err := game.PerformAction(a.ID, next); err != nil {
			t.Fatal(err)
		}
	}
	if next := raiseCashAction(game, a.ID, nil); next != nil {
		t.Fatalf("%s with only a pledged deed left", next)
	}
}
