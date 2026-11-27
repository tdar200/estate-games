package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func keyOrder(t *testing.T, raw []byte) []string {
	t.Helper()
	fields, err := splitObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(fields))
	for i, f := range fields {
		keys[i] = f.key
	}
	return keys
}

func TestCompatCreateJoinWatchState(t *testing.T) {
	h := newHarness(t)
	r := h.post("/api/rooms", `{"name":"  Ann\u0007  "}`)
	expectStatus(t, r, http.StatusCreated)
	if got := strings.Join(keyOrder(t, r.raw), ","); got != "code,token,playerId,state" {
		t.Fatalf("create keys %s", got)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content type %q", ct)
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "SAMEORIGIN",
		"Cache-Control":           "no-store",
		"Content-Security-Policy": classicCSP,
	} {
		if got := r.header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	code, token, hostID := r.str("code"), r.str("token"), r.str("playerId")
	if len(code) != 5 || strings.Trim(code, roomAlphabet) != "" {
		t.Fatalf("bad code %q", code)
	}
	state := dig(r.body, "state").(map[string]any)
	if state["viewerRole"] != "player" || state["viewerIsHost"] != true || state["viewerId"] != hostID || state["viewerName"] != "Ann" {
		t.Fatalf("viewer fields wrong: %v %v %v %v", state["viewerRole"], state["viewerIsHost"], state["viewerId"], state["viewerName"])
	}
	host := playerByID(state, hostID)
	if host["name"] != "Ann" || host["input"] != "classic" || host["tokenKind"] != "rocket" || host["online"] != true {
		t.Fatalf("host player %v", host)
	}
	if _, ok := state["localSeats"]; ok {
		t.Fatal("localSeats must only be sent to the table")
	}
	stateRaw, _ := json.Marshal(state)
	_ = stateRaw

	// Lowercase codes are accepted (requireRoom upper-cases).
	j := h.post("/api/rooms/"+strings.ToLower(code)+"/join", map[string]any{"name": "Bea"})
	expectStatus(t, j, http.StatusOK)
	if got := strings.Join(keyOrder(t, j.raw), ","); got != "code,token,playerId,state" {
		t.Fatalf("join keys %s", got)
	}
	if j.str("code") != code {
		t.Fatalf("join code %q", j.str("code"))
	}

	w := h.post("/api/rooms/"+code+"/watch", map[string]any{"name": "Cat"})
	expectStatus(t, w, http.StatusOK)
	if got := strings.Join(keyOrder(t, w.raw), ","); got != "code,token,spectatorId,state" {
		t.Fatalf("watch keys %s", got)
	}
	if !strings.HasPrefix(w.str("spectatorId"), "spectator_") || w.str("state", "viewerRole") != "spectator" {
		t.Fatalf("watch result %s", w.raw)
	}

	st := h.get("/api/rooms/" + code + "/state?token=" + token)
	expectStatus(t, st, http.StatusOK)
	spectators := st.body["spectators"].([]any)
	if len(spectators) != 1 || dig(spectators, "0", "name") != "Cat" {
		t.Fatalf("spectators %v", spectators)
	}
	// Top-level key order matches publicState, additive fields last.
	keys := keyOrder(t, st.raw)
	if keys[0] != "code" || keys[3] != "hostId" || keys[4] != "players" || keys[len(keys)-3] != "spectators" || keys[len(keys)-2] != "turnCounts" || keys[len(keys)-1] != "turnNumber" {
		t.Fatalf("state key order %v", keys)
	}
	if len(players(st.body)) != 2 {
		t.Fatalf("players %v", st.body["players"])
	}
}

func TestCompatErrors(t *testing.T) {
	h := newHarness(t)
	host := h.createRoom("Ann")
	cases := []struct {
		name   string
		method string
		path   string
		body   any
		status int
		msg    string
	}{
		{"unknown endpoint", "GET", "/api/nope", nil, 404, "API endpoint not found"},
		{"wrong method", "GET", "/api/rooms", nil, 404, "API endpoint not found"},
		{"bad code chars", "POST", "/api/rooms/AB-CD/join", `{"name":"x"}`, 404, "API endpoint not found"},
		{"invalid json", "POST", "/api/rooms", `{"name":`, 400, "Invalid JSON"},
		{"whitespace body", "POST", "/api/rooms", `  `, 400, "Invalid JSON"},
		{"null body", "POST", "/api/rooms", `null`, 400, "Cannot read properties of null (reading 'name')"},
		{"null body token", "POST", "/api/rooms/" + host.code + "/action", `null`, 400, "Cannot read properties of null (reading 'token')"},
		{"empty name", "POST", "/api/rooms", `{"name":"   "}`, 400, "Enter a player name"},
		{"array body", "POST", "/api/rooms", `[1,2]`, 400, "Enter a player name"},
		{"too large", "POST", "/api/rooms", `{"name":"` + strings.Repeat("a", 32_800) + `"}`, 413, "Request body is too large"},
		{"missing room", "POST", "/api/rooms/ZZZZZ/join", `{"name":"x"}`, 400, "The room does not exist or the server restarted"},
		{"bad token", "GET", "/api/rooms/" + host.code + "/state?token=nope", nil, 400, "Your session is invalid; please join the room again"},
		{"no token", "GET", "/api/rooms/" + host.code + "/state", nil, 400, "Your session is invalid; please join the room again"},
		{"no action", "POST", "/api/rooms/" + host.code + "/action", map[string]any{"token": host.token}, 400, "Invalid action format"},
		{"unknown action", "POST", "/api/rooms/" + host.code + "/action", map[string]any{"token": host.token, "action": map[string]any{"type": "fly"}}, 400, "The game has not started"},
		{"bad role", "POST", "/api/rooms/" + host.code + "/mode", map[string]any{"token": host.token, "role": "ghost"}, 400, "Invalid participation role"},
		{"start alone", "POST", "/api/rooms/" + host.code + "/action", map[string]any{"token": host.token, "action": map[string]any{"type": "start"}}, 400, "At least 2 players are required"},
		{"remove unknown", "POST", "/api/rooms/" + host.code + "/remove", map[string]any{"token": host.token, "playerId": 42}, 400, "Player not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := h.do(c.method, c.path, c.body, fromIP("10.0.0."+c.name[:1]))
			expectError(t, r, c.status, c.msg)
			if got := strings.Join(keyOrder(t, r.raw), ","); got != "error" {
				t.Fatalf("error body keys %s", got)
			}
		})
	}

	// Joining after the start fails with the Node message.
	h.join(host.code, "Bea")
	expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "start"}), 200)
	expectError(t, h.post("/api/rooms/"+host.code+"/join", map[string]any{"name": "Cy"}), 400,
		"The game has already started; joining in progress is not supported")
	expectError(t, h.post("/api/rooms/"+host.code+"/mode", map[string]any{"token": host.token, "role": "spectator"}), 400,
		"Participation can only be changed before the game starts")

	// OPTIONS short-circuits with 204 everywhere.
	if r := h.do(http.MethodOptions, "/api/rooms", nil); r.status != http.StatusNoContent {
		t.Fatalf("OPTIONS status %d", r.status)
	}
}

func TestRateLimits(t *testing.T) {
	h := newHarness(t)
	// Entry tier: 30 per minute per IP (joins count too; rooms are also
	// capped per creator, see TestRoomQuotas).
	for i := 0; i < 30; i++ {
		expectStatus(t, h.post("/api/rooms/ZZZZZ/watch", map[string]any{"name": "P"}, fromIP("203.0.113.5")), 400)
	}
	r := h.post("/api/rooms", map[string]any{"name": "P"}, fromIP("203.0.113.5"))
	expectError(t, r, 429, "Too many requests; please try again shortly")
	if r.header.Get("Retry-After") != "60" {
		t.Fatalf("Retry-After %q", r.header.Get("Retry-After"))
	}
	// Other IPs and other tiers are unaffected.
	room := h.createRoom("Q", fromIP("203.0.113.6"))
	// State tier: 600 per minute per address, whatever token is sent (made-up
	// tokens must not each get a fresh allowance).
	for i := 0; i < 600; i++ {
		token := room.token
		if i%2 == 1 {
			token = fmt.Sprintf("made-up-%d", i)
		}
		h.get("/api/rooms/"+room.code+"/state?token="+token, fromIP("203.0.113.6"))
	}
	expectStatus(t, h.get("/api/rooms/"+room.code+"/state?token="+room.token, fromIP("203.0.113.6")), 429)
	// Action tier: 180 per minute shared per IP.
	for i := 0; i < 180; i++ {
		expectStatus(t, h.get("/api/server", fromIP("203.0.113.7")), 200)
	}
	expectStatus(t, h.get("/api/server", fromIP("203.0.113.7")), 429)
	// A minute later the window resets.
	h.clock.Advance(61e9)
	expectStatus(t, h.get("/api/server", fromIP("203.0.113.7")), 200)
}

func TestHostRulesAndRoles(t *testing.T) {
	h := newHarness(t)
	host := h.createRoom("Ann")
	guest := h.join(host.code, "Bea")

	expectError(t, h.post("/api/rooms/"+host.code+"/ai", map[string]any{"token": guest.token}), 400, "Only the host can perform this action")
	r := h.post("/api/rooms/"+host.code+"/ai", map[string]any{"token": host.token})
	expectStatus(t, r, 200)
	ai := players(r.body)[2]
	if ai["name"] != "Alpha" || ai["kind"] != "ai" || ai["input"] != "ai" || ai["online"] != true || ai["tokenKind"] != "bicycle" {
		t.Fatalf("ai player %v", ai)
	}
	r = h.post("/api/rooms/"+host.code+"/ai", map[string]any{"token": host.token})
	if players(r.body)[3]["name"] != "Orca" {
		t.Fatalf("second ai name %v", players(r.body)[3]["name"])
	}
	// Remove the second AI.
	r = h.post("/api/rooms/"+host.code+"/remove", map[string]any{"token": host.token, "playerId": players(r.body)[3]["id"]})
	expectStatus(t, r, 200)
	if len(players(r.body)) != 3 {
		t.Fatalf("players after remove %d", len(players(r.body)))
	}
	// Removing a human invalidates their session.
	expectStatus(t, h.post("/api/rooms/"+host.code+"/remove", map[string]any{"token": host.token, "playerId": guest.playerID}), 200)
	expectError(t, h.get("/api/rooms/"+host.code+"/state?token="+guest.token), 400, msgSessionInvalid)

	// A spectator cannot act; a host spectator may start.
	watcher := h.post("/api/rooms/"+host.code+"/watch", map[string]any{"name": "Cat"})
	expectError(t, h.act(host.code, watcher.str("token"), map[string]any{"type": "roll"}), 400, "Spectators cannot perform game actions")
	expectError(t, h.post("/api/rooms/"+host.code+"/trade-quote", map[string]any{"token": watcher.str("token")}), 400, "Spectators cannot perform game actions")

	h.join(host.code, "Dan")
	r = h.post("/api/rooms/"+host.code+"/mode", map[string]any{"token": host.token, "role": "spectator"})
	expectStatus(t, r, 200)
	if r.body["viewerRole"] != "spectator" || r.body["viewerIsHost"] != true || r.body["hostId"] != r.body["spectatorId"] {
		t.Fatalf("host spectator state: role=%v host=%v hostId=%v spectatorId=%v", r.body["viewerRole"], r.body["viewerIsHost"], r.body["hostId"], r.body["spectatorId"])
	}
	r = h.act(host.code, host.token, map[string]any{"type": "set_dice_mode", "mode": "choice"})
	expectStatus(t, r, 200)
	if dig(r.body, "settings", "diceMode") != "choice" {
		t.Fatalf("dice mode %v", r.body["settings"])
	}
	expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "start"}), 200)
	expectError(t, h.act(host.code, host.token, map[string]any{"type": "roll"}), 400, "Spectators cannot perform game actions")
}

func TestModeRoundTripKeepsHost(t *testing.T) {
	h := newHarness(t)
	host := h.createRoom("Ann")
	expectStatus(t, h.post("/api/rooms/"+host.code+"/mode", map[string]any{"token": host.token, "role": "spectator"}), 200)
	r := h.post("/api/rooms/"+host.code+"/mode", map[string]any{"token": host.token, "role": "player"})
	expectStatus(t, r, 200)
	if r.body["viewerRole"] != "player" || r.body["hostId"] != r.body["viewerId"] {
		t.Fatalf("back to player: %v %v %v", r.body["viewerRole"], r.body["hostId"], r.body["viewerId"])
	}
	// Same role is a no-op.
	expectStatus(t, h.post("/api/rooms/"+host.code+"/mode", map[string]any{"token": host.token, "role": "player"}), 200)
}

func TestTradeQuoteUsesAI(t *testing.T) {
	h := newHarness(t)
	host := h.createRoom("Ann")
	r := h.post("/api/rooms/"+host.code+"/ai", map[string]any{"token": host.token})
	aiID := players(r.body)[1]["id"].(string)
	q := h.post("/api/rooms/"+host.code+"/trade-quote", map[string]any{"token": host.token, "targetId": aiID, "offerProperties": []int{1}})
	expectStatus(t, q, 200)
	if q.str("targetId") != aiID || dig(q.body, "requestCash") != float64(40) {
		t.Fatalf("quote %s", q.raw)
	}
	expectError(t, h.post("/api/rooms/"+host.code+"/trade-quote", map[string]any{"token": host.token, "targetId": host.playerID}), 400,
		"Quotes are only available from an active AI player")
}

func TestHTMLEscapingMatchesJSONStringify(t *testing.T) {
	h := newHarness(t)
	r := h.post("/api/rooms", map[string]any{"name": "<b>&</b>"})
	expectStatus(t, r, 201)
	if !strings.Contains(string(r.raw), `"name":"<b>&</b>"`) || strings.Contains(string(r.raw), `\u003c`) {
		t.Fatalf("names must not be HTML-escaped: %s", r.raw[:200])
	}
}
