package server

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

type table struct {
	code, token string
}

func (h *harness) createTable() table {
	h.t.Helper()
	r := h.post("/api/tables", map[string]any{})
	expectStatus(h.t, r, http.StatusCreated)
	if got := strings.Join(keyOrder(h.t, r.raw), ","); got != "code,tableToken,state" {
		h.t.Fatalf("table keys %s", got)
	}
	return table{r.str("code"), r.str("tableToken")}
}

func (h *harness) addSeat(tb table, input, name, token string, pad any) response {
	h.t.Helper()
	body := map[string]any{"tableToken": tb.token, "input": input, "name": name, "tokenKind": token}
	if pad != nil {
		body["pad"] = pad
	}
	return h.post("/api/rooms/"+tb.code+"/seats", body)
}

func TestTableSeatsAndHostPlayer(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	st := h.state(tb.code, tb.token)
	if st["viewerRole"] != "table" || st["viewerIsHost"] != true || st["viewerId"] != nil || st["viewerName"] != "Table" {
		t.Fatalf("table viewer: %v %v %v %v", st["viewerRole"], st["viewerIsHost"], st["viewerId"], st["viewerName"])
	}
	if seats, ok := st["localSeats"].([]any); !ok || len(seats) != 0 {
		t.Fatalf("localSeats %v", st["localSeats"])
	}

	pad := h.addSeat(tb, "controller", "Maya", "lighthouse", 0)
	expectStatus(t, pad, http.StatusCreated)
	padSeat := pad.str("seatId")
	if !strings.HasPrefix(padSeat, "seat_") {
		t.Fatalf("seatId %q", padSeat)
	}
	expectError(t, h.addSeat(tb, "controller", "Dup", "key", 0), 400, msgPadTaken)
	expectError(t, h.addSeat(tb, "phone", "Nope", "key", nil), 400, msgSeatInput)
	kb := h.addSeat(tb, "keyboard", "Kim", "lighthouse", nil) // token taken: gets another
	expectStatus(t, kb, http.StatusCreated)
	kbState := kb.body["state"].(map[string]any)
	seats := kbState["localSeats"].([]any)
	if len(seats) != 2 || dig(seats, "0", "input") != "controller" || dig(seats, "0", "pad") != float64(0) || dig(seats, "1", "pad") != nil {
		t.Fatalf("localSeats %v", seats)
	}
	padPlayer := dig(seats, "0", "playerId").(string)
	kbPlayer := dig(seats, "1", "playerId").(string)
	if p := playerByID(kbState, padPlayer); p["input"] != "controller" || p["tokenKind"] != "lighthouse" || p["online"] != true {
		t.Fatalf("pad player %v", p)
	}
	if p := playerByID(kbState, kbPlayer); p["input"] != "keyboard" || p["tokenKind"] != "rocket" {
		t.Fatalf("keyboard player %v", p)
	}

	// First phone player becomes the host player; the second does not.
	alice := h.device("Alice", "umbrella", "198.51.100.1")
	bob := h.device("Bob", "teapot", "198.51.100.2")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice", "tokenKind": "umbrella", "deviceId": alice.id}, alice.opt())
	expectStatus(t, a, 200)
	aState := a.body["state"].(map[string]any)
	if aState["viewerIsHost"] != true || aState["hostId"] != a.str("playerId") {
		t.Fatalf("first phone is not host: host=%v hostId=%v", aState["viewerIsHost"], aState["hostId"])
	}
	if p := playerByID(aState, a.str("playerId")); p["input"] != "phone" || p["tokenKind"] != "umbrella" {
		t.Fatalf("phone player %v", p)
	}
	b := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Bob"}, bob.opt())
	if dig(b.body, "state", "viewerIsHost") != false {
		t.Fatal("second phone must not be host")
	}
	if p := playerByID(b.body["state"].(map[string]any), b.str("playerId")); p["tokenKind"] != "teapot" {
		t.Fatalf("device token not used: %v", p)
	}
	// Host player has lobby rights.
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": a.str("token")}), 200)
	expectError(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": b.str("token")}), 400, msgOnlyHost)
	expectError(t, h.addSeat(table{tb.code, a.str("token")}, "keyboard", "X", "", nil), 403, msgTableOnly)

	// Device ids and secrets never appear in states.
	for _, raw := range [][]byte{a.raw, b.raw, h.get("/api/rooms/" + tb.code + "/state?token=" + tb.token).raw} {
		if strings.Contains(string(raw), alice.id) || strings.Contains(string(raw), bob.secret) || strings.Contains(string(raw), "deviceId") {
			t.Fatal("state leaks device identity")
		}
	}

	// The table acts only for its seats.
	expectError(t, h.act(tb.code, tb.token, map[string]any{"type": "roll"}), 400, msgTableNoSeat)
	expectError(t, h.act(tb.code, tb.token, map[string]any{"type": "roll"}, map[string]any{"seatId": "seat_nope"}), 400, msgSeatMissing)
	r := h.act(tb.code, tb.token, map[string]any{"type": "start"})
	expectStatus(t, r, 200)
	if r.body["phase"] != "determining_order" {
		t.Fatalf("table start: phase %v", r.body["phase"])
	}
	expectError(t, h.addSeat(tb, "keyboard", "Late", "", nil), 400, msgSeatLobby)
	r = h.act(tb.code, tb.token, map[string]any{"type": "roll_for_order"}, map[string]any{"seatId": padSeat})
	expectStatus(t, r, 200)
	if logs := r.body["log"].([]any); !strings.Contains(dig(logs, itoa(len(logs)-1), "text").(string), "Maya rolled") {
		t.Fatalf("seat action did not act as Maya: %v", logs[len(logs)-1])
	}
	expectError(t, h.act(tb.code, tb.token, map[string]any{"type": "roll_for_order"}, map[string]any{"seatId": padSeat}), 400,
		"You have already rolled for order this round")
}

func TestRemovingHostPlayerPromotesNextPhone(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	d1, d2 := h.device("One", "", "198.51.100.11"), h.device("Two", "", "198.51.100.12")
	one := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "One"}, d1.opt())
	two := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Two"}, d2.opt())
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/remove", map[string]any{"token": tb.token, "playerId": one.str("playerId")}), 200)
	st := h.state(tb.code, two.str("token"))
	if st["viewerIsHost"] != true || st["hostId"] != two.str("playerId") {
		t.Fatalf("host not promoted: %v %v", st["viewerIsHost"], st["hostId"])
	}
}

func TestRejoinMidGame(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "rocket", "198.51.100.21")
	bob := h.device("Bob", "teapot", "198.51.100.22")
	stranger := h.device("Eve", "key", "198.51.100.23")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Bob"}, bob.opt())
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), 200)

	r := h.post("/api/rooms/"+tb.code+"/rejoin", map[string]any{}, alice.opt())
	expectStatus(t, r, 200)
	if got := strings.Join(keyOrder(t, r.raw), ","); got != "code,token,playerId,state" {
		t.Fatalf("rejoin keys %s", got)
	}
	if r.str("playerId") != a.str("playerId") || r.str("token") == a.str("token") {
		t.Fatalf("rejoin returned %s", r.raw[:120])
	}
	if dig(r.body, "state", "viewerIsHost") != true {
		t.Fatal("rejoined host player lost host rights")
	}
	expectStatus(t, h.act(tb.code, r.str("token"), map[string]any{"type": "roll_for_order"}), 200)
	// Joining again with the same device mid-game also returns the seat.
	again := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	expectStatus(t, again, 200)
	if again.str("playerId") != a.str("playerId") {
		t.Fatal("join with a seated device should rejoin")
	}
	expectError(t, h.post("/api/rooms/"+tb.code+"/rejoin", map[string]any{}, stranger.opt()), 404, msgNoSeat)
	expectError(t, h.post("/api/rooms/"+tb.code+"/rejoin", map[string]any{}), 401, msgDeviceNeeded)
	expectError(t, h.post("/api/rooms/"+tb.code+"/rejoin", map[string]any{}, asDevice(alice.id, strings.Repeat("x", 43))), 401, msgDeviceInvalid)
}

func TestSummary(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "umbrella", "198.51.100.31")
	h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	h.addSeat(tb, "keyboard", "Kim", "key", nil)
	r := h.get("/api/rooms/" + strings.ToLower(tb.code) + "/summary")
	expectStatus(t, r, 200)
	if r.str("code") != tb.code || r.str("status") != "lobby" || r.body["openSeats"] != float64(6) || r.body["turn"] != float64(0) {
		t.Fatalf("summary %s", r.raw)
	}
	if r.str("host", "name") != "Alice" || r.str("host", "tokenKind") != "umbrella" {
		t.Fatalf("summary host %v", r.body["host"])
	}
	ps := r.body["players"].([]any)
	if len(ps) != 2 || dig(ps, "1", "input") != "keyboard" || dig(ps, "1", "tokenKind") != "key" || dig(ps, "0", "id") == nil || dig(ps, "1", "reclaimable") != false {
		t.Fatalf("summary players %v", ps)
	}
	if dig(r.body, "rules", "diceMode") != "random" {
		t.Fatalf("rules %v", r.body["rules"])
	}
	expectError(t, h.get("/api/rooms/ZZZZZ/summary"), 404, msgRoomMissing)
}

func TestDeviceProfile(t *testing.T) {
	h := newHarness(t)
	d := h.device("Priya", "umbrella", "198.51.100.41")
	if !strings.HasPrefix(d.id, "dev_") || len(d.secret) < 32 {
		t.Fatalf("device %v", d)
	}
	r := h.do(http.MethodPatch, "/api/device", map[string]any{"name": "Pri", "tokenKind": "key", "color": "#7b4fd6"}, d.opt())
	expectStatus(t, r, 200)
	if r.str("name") != "Pri" || r.str("tokenKind") != "key" || r.str("color") != "#7B4FD6" || r.str("deviceId") != d.id {
		t.Fatalf("profile %s", r.raw)
	}
	if strings.Contains(string(r.raw), d.secret) {
		t.Fatal("profile echoes the secret")
	}
	expectError(t, h.do(http.MethodPatch, "/api/device", map[string]any{"tokenKind": "boot"}, d.opt()), 400, "Choose one of the eight tokens")
	expectError(t, h.do(http.MethodPatch, "/api/device", map[string]any{"name": ""}, d.opt()), 400, "Enter a player name")
	expectError(t, h.do(http.MethodPatch, "/api/device", map[string]any{"name": "X"}), 401, msgDeviceNeeded)
	expectError(t, h.do(http.MethodPatch, "/api/device", map[string]any{"name": "X"}, asDevice(d.id, strings.Repeat("y", 43))), 401, msgDeviceInvalid)
	g := h.get("/api/device", d.opt())
	expectStatus(t, g, 200)
	if g.str("name") != "Pri" || g.body["recent"] == nil {
		t.Fatalf("GET device %s", g.raw)
	}
	// An unknown but well-formed device is adopted (data file lost).
	adopted := h.get("/api/device", asDevice("dev_restored0001", strings.Repeat("z", 43)))
	expectStatus(t, adopted, 200)
}

func TestFriendsInvitesAndPresence(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "rocket", "198.51.100.51")
	bob := h.device("Bob", "teapot", "198.51.100.52")
	carol := h.device("Carol", "key", "198.51.100.53")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Bob"}, bob.opt())

	// Not friends before the game starts.
	f := h.get("/api/friends", alice.opt())
	expectStatus(t, f, 200)
	if len(f.list) != 0 {
		t.Fatalf("friends before start %s", f.raw)
	}
	expectStatus(t, h.act(tb.code, a.str("token"), map[string]any{"type": "start"}), 200)

	f = h.get("/api/friends", alice.opt())
	if len(f.list) != 1 {
		t.Fatalf("friends after start %s", f.raw)
	}
	friend := f.list[0].(map[string]any)
	if friend["deviceId"] != bob.id || friend["name"] != "Bob" || friend["tokenKind"] != "teapot" || friend["status"] != "in_game" || friend["code"] != tb.code {
		t.Fatalf("friend entry %v", friend)
	}
	if friend["lastPlayedAt"] == nil || strings.Contains(string(f.raw), bob.secret) {
		t.Fatalf("friend entry %s", f.raw)
	}

	// Bob's device socket gets his friends list, then invites from Alice.
	bobSock := h.dial("/ws/device?device="+bob.id+":"+bob.secret, nil)
	first := bobSock.next(2 * time.Second)
	if first["type"] != "friends" || dig(first, "friends", "0", "deviceId") != alice.id {
		t.Fatalf("first device message %v", first)
	}
	inv := h.post("/api/invites", map[string]any{"toDeviceId": bob.id, "code": tb.code}, alice.opt())
	expectStatus(t, inv, 200)
	if inv.body["sent"] != true {
		t.Fatalf("invite %s", inv.raw)
	}
	msg := bobSock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "invite" })
	invite := msg[len(msg)-1]["invite"].(map[string]any)
	if dig(invite, "from", "name") != "Alice" || dig(invite, "from", "tokenKind") != "rocket" || invite["code"] != tb.code || invite["playersWaiting"] != float64(2) {
		t.Fatalf("invite %v", invite)
	}
	if _, leaked := dig(invite, "from").(map[string]any)["deviceId"]; leaked {
		t.Fatal("invite leaks device id")
	}
	// Invites only go to friends and are rate limited per pair.
	expectError(t, h.post("/api/invites", map[string]any{"toDeviceId": carol.id, "code": tb.code}, alice.opt()), 403,
		"You can only invite people you have played with")
	expectStatus(t, h.post("/api/invites", map[string]any{"toDeviceId": bob.id, "code": tb.code}, alice.opt()), 200)
	expectStatus(t, h.post("/api/invites", map[string]any{"toDeviceId": bob.id, "code": tb.code}, alice.opt()), 200)
	expectError(t, h.post("/api/invites", map[string]any{"toDeviceId": bob.id, "code": tb.code}, alice.opt()), 429,
		"Too many invites; please wait a moment")
	expectError(t, h.post("/api/invites", map[string]any{"toDeviceId": bob.id, "code": tb.code}), 401, msgDeviceNeeded)

	// Presence: Alice's device socket pushes a fresh list to Bob.
	aliceSock := h.dial("/ws/device", http.Header{deviceHeader: {alice.id + ":" + alice.secret}})
	aliceSock.next(2 * time.Second)
	// Once seats go quiet, statuses fall back to online / offline.
	h.clock.Advance(2 * time.Minute)
	h.srv.mu.Lock()
	for _, r := range h.srv.rooms {
		h.srv.commit(r)
	}
	h.srv.mu.Unlock()
	f = h.get("/api/friends", bob.opt())
	if status := dig(f.list, "0", "status"); status != "in_game" {
		// Alice's device socket is open and she holds a seat: in_game.
		t.Fatalf("alice status %v", status)
	}
	aliceSock.conn.CloseNow()
	msgs := bobSock.until(3*time.Second, func(m map[string]any) bool {
		return m["type"] == "friends" && dig(m, "friends", "0", "status") == "offline"
	})
	if len(msgs) == 0 {
		t.Fatal("no offline push")
	}
	f = h.get("/api/friends", alice.opt())
	if dig(f.list, "0", "status") != "in_game" {
		t.Fatalf("bob (device socket open, seated) status %v", dig(f.list, "0", "status"))
	}
}

func TestFriendResultsRecorded(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "rocket", "198.51.100.61")
	bob := h.device("Bob", "teapot", "198.51.100.62")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	b := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Bob"}, bob.opt())
	h.act(tb.code, a.str("token"), map[string]any{"type": "start"})
	h.act(tb.code, a.str("token"), map[string]any{"type": "roll_for_order"})
	h.act(tb.code, b.str("token"), map[string]any{"type": "roll_for_order"})
	waitFor(t, "first turn", 2*time.Second, func() bool {
		return h.state(tb.code, a.str("token"))["phase"] == "awaiting_roll"
	})
	st := h.state(tb.code, a.str("token"))
	current := st["currentPlayerId"].(string)
	loserToken := a.str("token")
	if current == b.str("playerId") {
		loserToken = b.str("token")
	}
	expectStatus(t, h.act(tb.code, loserToken, map[string]any{"type": "bankrupt"}), 200)
	f := h.get("/api/friends", alice.opt())
	result := dig(f.list, "0", "lastResult")
	if result != "won" && result != "lost" {
		t.Fatalf("lastResult %v", result)
	}
	g := h.get("/api/device", bob.opt())
	if dig(g.body, "recent", "0", "code") != tb.code || dig(g.body, "recent", "0", "result") == nil {
		t.Fatalf("recent %s", g.raw)
	}
}

func TestAIDifficultyAndTableQuote(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	r := h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"tableToken": tb.token, "difficulty": "hard"})
	expectStatus(t, r, 200)
	r = h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token})
	ps := players(r.body)
	if ps[0]["difficulty"] != "hard" || ps[1]["difficulty"] != "normal" {
		t.Fatalf("difficulties %v %v", ps[0]["difficulty"], ps[1]["difficulty"])
	}
	expectError(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": "brutal"}), 400, "Choose easy, normal, hard, expert or grandmaster")
	seat := h.addSeat(tb, "keyboard", "Kim", "", nil)
	if _, ok := playerByID(seat.body["state"].(map[string]any), dig(seat.body, "state", "localSeats", "0", "playerId").(string))["difficulty"]; ok {
		t.Fatal("humans have no difficulty")
	}
	q := h.post("/api/rooms/"+tb.code+"/trade-quote", map[string]any{"tableToken": tb.token, "seatId": seat.str("seatId"), "targetId": ps[0]["id"]})
	expectStatus(t, q, 200)
	if q.str("targetId") != ps[0]["id"] {
		t.Fatalf("table quote %s", q.raw)
	}
	expectError(t, h.post("/api/rooms/"+tb.code+"/trade-quote", map[string]any{"tableToken": tb.token, "targetId": ps[0]["id"]}), 400, msgTableNoSeat)
	st := h.state(tb.code, tb.token)
	if st["turnNumber"] != float64(1) || dig(st, "turnCounts", ps[0]["id"].(string)) != float64(0) {
		t.Fatalf("turn fields %v %v", st["turnNumber"], st["turnCounts"])
	}
}

func TestBotFactoryPerSeat(t *testing.T) {
	var levels []string
	h := newHarness(t, func(c *Config) {
		c.Bots = func(level string, seed int64) Bot {
			levels = append(levels, level)
			return &SimpleAI{}
		}
	})
	tb := h.createTable()
	h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": "easy"})
	h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token})
	if strings.Join(levels, ",") != "easy,normal" {
		t.Fatalf("bots created for %v", levels)
	}
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), 200)
	waitFor(t, "bots to play", 5*time.Second, func() bool { return h.view(tb.code).Phase != "determining_order" })
}

func TestRematch(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "umbrella", "198.51.100.91")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	pad := h.addSeat(tb, "controller", "Maya", "key", 1)
	h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": "easy"})
	h.act(tb.code, tb.token, map[string]any{"type": "set_dice_mode", "mode": "choice"})
	expectError(t, h.post("/api/rooms/"+tb.code+"/rematch", map[string]any{"token": tb.token}), 400, "The game is not finished yet")
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), 200)
	h.act(tb.code, a.str("token"), map[string]any{"type": "roll_for_order"})
	h.act(tb.code, tb.token, map[string]any{"type": "roll_for_order"}, map[string]any{"seatId": pad.str("seatId")})
	waitFor(t, "first turn", 3*time.Second, func() bool { return h.view(tb.code).Phase == "awaiting_roll" })
	// Everyone but the AI goes bankrupt in turn order.
	for i := 0; i < 20 && h.view(tb.code).Status != "finished"; i++ {
		view := h.view(tb.code)
		current := view.Players[view.CurrentIndex].ID
		switch current {
		case a.str("playerId"):
			h.act(tb.code, a.str("token"), map[string]any{"type": "bankrupt"})
		case dig(pad.body, "state", "localSeats", "0", "playerId"):
			h.act(tb.code, tb.token, map[string]any{"type": "bankrupt"}, map[string]any{"seatId": pad.str("seatId")})
		default:
			time.Sleep(30 * time.Millisecond)
		}
	}
	final := h.view(tb.code)
	if final.Status != "finished" {
		t.Fatalf("game not finished: %s", final.Phase)
	}
	// Bankruptcies are numbered in the order they happened.
	out := map[float64]bool{}
	for _, p := range players(h.state(tb.code, tb.token)) {
		if p["bankrupt"] == true {
			out[p["outOrder"].(float64)] = true
		} else if p["outOrder"] != nil {
			t.Fatalf("winner has an outOrder: %v", p)
		}
	}
	if !out[1] || !out[2] || len(out) != 2 {
		t.Fatalf("outOrder values %v", out)
	}
	sock := h.dial("/ws/rooms/"+tb.code+"?token="+a.str("token"), nil)
	sock.next(2 * time.Second)
	expectError(t, h.post("/api/rooms/"+tb.code+"/rematch", map[string]any{"token": "nope"}), 400, msgSessionInvalid)
	r := h.post("/api/rooms/"+tb.code+"/rematch", map[string]any{"tableToken": tb.token})
	expectStatus(t, r, 200)
	if r.str("code") != tb.code || r.str("status") != "lobby" || r.body["version"].(float64) <= float64(final.Version) {
		t.Fatalf("rematch state %v %v %v", r.body["code"], r.body["status"], r.body["version"])
	}
	ps := players(r.body)
	if len(ps) != 3 || ps[0]["name"] != "Alice" || ps[0]["input"] != "phone" || ps[0]["tokenKind"] != "umbrella" ||
		ps[1]["input"] != "controller" || ps[2]["kind"] != "ai" || ps[2]["difficulty"] != "easy" {
		t.Fatalf("rematch players %v", ps)
	}
	if r.body["hostId"] != ps[0]["id"] || dig(r.body, "settings", "diceMode") != "choice" {
		t.Fatalf("rematch host %v settings %v", r.body["hostId"], r.body["settings"])
	}
	if dig(r.body, "localSeats", "0", "playerId") != ps[1]["id"] {
		t.Fatalf("seat not remapped: %v", r.body["localSeats"])
	}
	// The phone's old token now plays the new seat, and its socket got the lobby.
	st := h.state(tb.code, a.str("token"))
	if st["viewerId"] != ps[0]["id"] || st["viewerIsHost"] != true {
		t.Fatalf("phone after rematch %v %v", st["viewerId"], st["viewerIsHost"])
	}
	sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "state" && dig(m, "state", "status") == "lobby" })
	rj := h.post("/api/rooms/"+tb.code+"/rejoin", map[string]any{}, alice.opt())
	if rj.str("playerId") != ps[0]["id"] {
		t.Fatalf("rejoin after rematch %s", rj.raw)
	}
}
