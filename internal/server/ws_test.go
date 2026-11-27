package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRoomSocketPushesEventsThenState(t *testing.T) {
	h := newHarness(t)
	h.engine.dice = [][2]int{{6, 5}, {1, 2}, {3, 4}}
	host := h.createRoom("Ann")
	guest := h.join(host.code, "Bea")
	sock := h.dial("/ws/rooms/"+host.code+"?token="+guest.token, nil)
	first := sock.next(2 * time.Second)
	if first["type"] != "state" || dig(first, "state", "viewerId") != guest.playerID {
		t.Fatalf("initial message %v", first)
	}

	expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "start"}), 200)
	msgs := sock.until(2*time.Second, func(m map[string]any) bool {
		return m["type"] == "state" && dig(m, "state", "phase") == "determining_order"
	})
	if dig(msgs[len(msgs)-1], "state", "viewerId") != guest.playerID {
		t.Fatal("socket state must be rendered for the socket's viewer")
	}
	h.act(host.code, host.token, map[string]any{"type": "roll_for_order"})
	msgs = sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "state" })
	if len(msgs) < 2 || msgs[0]["type"] != "event" || dig(msgs[0], "event", "type") != "dice" || dig(msgs[0], "event", "order") != true {
		t.Fatalf("expected order dice event before state, got %v", msgs)
	}
	h.act(host.code, guest.token, map[string]any{"type": "roll_for_order"})
	// The countdown completes on a timer and is pushed without any request.
	sock.until(3*time.Second, func(m map[string]any) bool {
		return m["type"] == "state" && dig(m, "state", "phase") == "awaiting_roll"
	})
	st := h.state(host.code, host.token)
	currentToken := host.token
	if st["currentPlayerId"] == guest.playerID {
		currentToken = guest.token
	}
	expectStatus(t, h.act(host.code, currentToken, map[string]any{"type": "roll"}), 200)
	msgs = sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "state" })
	var kinds []string
	for _, m := range msgs {
		if m["type"] == "event" {
			kinds = append(kinds, dig(m, "event", "type").(string))
		}
	}
	if strings.Join(kinds, ",") != "dice,move" {
		t.Fatalf("roll events %v", kinds)
	}
	move := msgs[1]["event"].(map[string]any)
	if path := move["path"].([]any); len(path) != 8 || path[0] != float64(0) || path[7] != float64(7) {
		t.Fatalf("move path %v", move["path"])
	}

	// Pings are answered and keep the socket alive.
	sock.send(map[string]any{"type": "ping"})
	sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "pong" })
}

func TestRoomSocketRejectsBadSession(t *testing.T) {
	h := newHarness(t)
	host := h.createRoom("Ann")
	sock := h.dial("/ws/rooms/"+host.code+"?token=wrong", nil)
	msg := sock.next(2 * time.Second)
	if msg["type"] != "error" || msg["error"] != msgSessionInvalid {
		t.Fatalf("message %v", msg)
	}
	missing := h.dial("/ws/rooms/ZZZZZ?token=x", nil)
	if msg := missing.next(2 * time.Second); msg["error"] != msgRoomMissing {
		t.Fatalf("message %v", msg)
	}
	device := h.dial("/ws/device", nil)
	if msg := device.next(2 * time.Second); msg["type"] != "error" {
		t.Fatalf("device socket without credential: %v", msg)
	}
}

func TestRemovedPlayerSocketGetsError(t *testing.T) {
	h := newHarness(t)
	host := h.createRoom("Ann")
	guest := h.join(host.code, "Bea")
	sock := h.dial("/ws/rooms/"+host.code+"?token="+guest.token, nil)
	sock.next(2 * time.Second)
	h.post("/api/rooms/"+host.code+"/remove", map[string]any{"token": host.token, "playerId": guest.playerID})
	sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "error" && m["error"] == msgSessionInvalid })
}

func TestPresenceOnlineFlag(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	d := h.device("Alice", "rocket", "198.51.100.71")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, d.opt())
	tableSock := h.dial("/ws/rooms/"+tb.code+"?token="+tb.token, nil)
	first := tableSock.next(2 * time.Second)
	if dig(first, "state", "viewerRole") != "table" || dig(first, "state", "localSeats") == nil {
		t.Fatalf("table socket state %v", first)
	}
	// Alice stops polling and has no socket: after the window she shows offline.
	h.clock.Advance(time.Minute)
	msgs := tableSock.until(8*time.Second, func(m map[string]any) bool {
		p := playerByID(dig(m, "state").(map[string]any), a.str("playerId"))
		return m["type"] == "state" && p != nil && p["online"] == false
	})
	offline := msgs[len(msgs)-1]
	// Her phone reconnects: online again, with a bumped version.
	phone := h.dial("/ws/rooms/"+tb.code+"?token="+a.str("token"), nil)
	phone.next(2 * time.Second)
	msgs = tableSock.until(3*time.Second, func(m map[string]any) bool {
		p := playerByID(dig(m, "state").(map[string]any), a.str("playerId"))
		return m["type"] == "state" && p != nil && p["online"] == true
	})
	online := msgs[len(msgs)-1]
	if dig(online, "state", "version").(float64) <= dig(offline, "state", "version").(float64) {
		t.Fatal("presence change must bump the version so clients re-render")
	}
}

func TestAIPlaysAndCountdownCompletes(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	for i := 0; i < 2; i++ {
		expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), 200)
	}
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), 200)
	waitFor(t, "AI turns", 5*time.Second, func() bool {
		st := h.state(tb.code, tb.token)
		counts := 0
		for _, entry := range st["log"].([]any) {
			if strings.HasSuffix(dig(entry, "text").(string), "'s turn") {
				counts++
			}
		}
		return counts >= 3
	})
}

// A ping may carry the client's clock (t) and its best round trip (rtt): the
// pong echoes t and adds the server's clock, so phones sync to the server
// (docs/QUIZ-API.md §1.1). Room and device sockets alike, for every game
// type; a bare ping (older clients) still gets a pong, without t.
func TestSocketPongCarriesTheServerClock(t *testing.T) {
	h := newHarness(t)
	host := h.createRoom("Ann")
	d := h.device("Bea", "rocket", "198.51.100.81")
	sockets := map[string]*socket{
		"room":   h.dial("/ws/rooms/"+host.code+"?token="+host.token, nil),
		"device": h.dial("/ws/device", http.Header{deviceHeader: {d.id + ":" + d.secret}}),
	}
	for kind, sock := range sockets {
		before := h.srv.now().UnixMilli()
		sock.send(map[string]any{"type": "ping", "t": 1795430029100.25, "rtt": 42})
		msgs := sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "pong" })
		pong := msgs[len(msgs)-1]
		now, _ := pong["now"].(float64)
		if pong["t"] != 1795430029100.25 || int64(now) < before || int64(now) > h.srv.now().UnixMilli() || len(pong) != 3 {
			t.Fatalf("%s socket: pong %v", kind, pong)
		}
		for _, ping := range []map[string]any{{"type": "ping"}, {"type": "ping", "t": "soon"}} {
			sock.send(ping)
			msgs = sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "pong" })
			if pong := msgs[len(msgs)-1]; pong["now"] == nil || pong["t"] != nil {
				t.Fatalf("%s socket: %v answered with %v", kind, ping, pong)
			}
		}
	}
	// The room socket's rtt counts towards Ann's latency; the lowest of the
	// last eight wins, and an rtt that is not a number is ignored.
	room := sockets["room"]
	for _, rtt := range []any{90, 35.4, "fast", -5, 70} {
		room.send(map[string]any{"type": "ping", "t": 1, "rtt": rtt})
		room.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "pong" })
	}
	h.srv.mu.Lock()
	latency := h.srv.rooms[host.code].playerLatency(host.playerID)
	h.srv.mu.Unlock()
	if latency != 35 {
		t.Fatalf("latency %d, want 35", latency)
	}
	for range rttWindow {
		room.send(map[string]any{"type": "ping", "rtt": 80})
		room.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "pong" })
	}
	h.srv.mu.Lock()
	latency = h.srv.rooms[host.code].playerLatency(host.playerID)
	h.srv.mu.Unlock()
	if latency != 80 {
		t.Fatalf("latency %d after eight newer pings, want 80", latency)
	}
}

// An action sent over the room socket runs as POST …/action with the
// socket's token. The answer is an actionResult echoing the client's id,
// after the new state; a refused action is ok:false with the plain-English
// error, never an "error" message (clients end the session on those).
func TestSocketActions(t *testing.T) {
	h := newHarness(t)
	host := h.createRoom("Ann")
	guest := h.join(host.code, "Bea")
	sock := h.dial("/ws/rooms/"+host.code+"?token="+host.token, nil)
	sock.next(2 * time.Second)
	sock.send(map[string]any{"type": "action", "action": map[string]any{"type": "start"}, "id": 7})
	msgs := sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "actionResult" })
	result := msgs[len(msgs)-1]
	if result["id"] != float64(7) || result["ok"] != true || result["error"] != nil {
		t.Fatalf("actionResult %v", result)
	}
	if len(msgs) < 2 || msgs[len(msgs)-2]["type"] != "state" || dig(msgs[len(msgs)-2], "state", "phase") != "determining_order" {
		t.Fatalf("the new state comes before the result: %v", msgs)
	}
	guestSock := h.dial("/ws/rooms/"+host.code+"?token="+guest.token, nil)
	guestSock.next(2 * time.Second)
	want := h.act(host.code, guest.token, map[string]any{"type": "start"}).str("error")
	guestSock.send(map[string]any{"type": "action", "action": map[string]any{"type": "start"}, "id": "b-1"})
	guestSock.send(map[string]any{"type": "action"})
	for _, id := range []any{"b-1", nil} {
		msgs = guestSock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "actionResult" })
		result = msgs[len(msgs)-1]
		if result["id"] != id || result["ok"] != false || result["error"] == "" || (id != nil && result["error"] != want) {
			t.Fatalf("refused action: %v (POST says %q)", result, want)
		}
		if _, has := result["id"]; !has {
			t.Fatal("an actionResult always has an id (null when the client sent none)")
		}
		for _, m := range msgs {
			if m["type"] == "error" {
				t.Fatalf("a refused action must not end the session: %v", m)
			}
		}
	}
	guestSock.send(map[string]any{"type": "ping"})
	guestSock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "pong" })
}

// Socket actions and POST …/action from one address share the "action"
// rate limit (180 a minute), so the socket is no way round it.
func TestSocketActionsShareTheActionLimit(t *testing.T) {
	h := newHarness(t)
	host := h.createRoom("Ann")
	const ip = "198.51.100.91"
	sock := h.dial("/ws/rooms/"+host.code+"?token="+host.token, http.Header{"X-Forwarded-For": {ip}})
	sock.next(2 * time.Second)
	// Batches of 40, each read before the next: a socket with 64 unsent messages is
	// closed as too slow, and 181 unread results can get there on a busy machine.
	for start := 0; start < 181; start += 40 {
		end := min(start+40, 181)
		for i := start; i < end; i++ {
			sock.send(map[string]any{"type": "action", "action": map[string]any{"type": "no_such_action"}, "id": i})
		}
		for i := start; i < end; i++ {
			msgs := sock.until(5*time.Second, func(m map[string]any) bool { return m["type"] == "actionResult" })
			result := msgs[len(msgs)-1]
			limited := result["error"] == msgTooManyRequests
			if result["id"] != float64(i) || result["ok"] != false || limited != (i == 180) {
				t.Fatalf("action %d: %v", i, result)
			}
		}
	}
	res := h.post("/api/rooms/"+host.code+"/action", map[string]any{"token": host.token, "action": map[string]any{"type": "start"}}, fromIP(ip))
	expectError(t, res, http.StatusTooManyRequests, msgTooManyRequests)
}

// Fields whose names start with "_" are the server's to add: a client's are
// dropped from an action before the engine sees it.
func TestActionPrivateFieldsDropped(t *testing.T) {
	cases := map[string]string{
		`{"type":"answer","_arrival":1,"choice":2,"_rtt":{"a":1}}`: `{"type":"answer","choice":2}`,
		`{"type":"roll"}`: `{"type":"roll"}`,
		`{"_only":true}`:  `{}`,
		`"roll"`:          `"roll"`,
		`{"type":`:        `{"type":`,
		``:                ``,
	}
	for in, want := range cases {
		if got := string(withoutPrivateFields(json.RawMessage(in))); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}
