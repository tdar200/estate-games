package server

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func summaryPlayerNamed(t *testing.T, summary response, name string) map[string]any {
	t.Helper()
	for _, raw := range summary.body["players"].([]any) {
		p := raw.(map[string]any)
		if p["name"] == name {
			return p
		}
	}
	t.Fatalf("no player %q in summary %v", name, summary.body["players"])
	return nil
}

// stateClaims returns state.claims (the requests this viewer may answer).
func stateClaims(state map[string]any) []map[string]any {
	list, _ := state["claims"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, raw := range list {
		out = append(out, raw.(map[string]any))
	}
	return out
}

func (h *harness) reclaim(code, playerID string, d device, extra map[string]any) response {
	h.t.Helper()
	body := map[string]any{"playerId": playerID}
	for k, v := range extra {
		body[k] = v
	}
	return h.post("/api/rooms/"+code+"/reclaim", body, d.opt())
}

func (h *harness) decide(code, token, claimID, decision string) response {
	h.t.Helper()
	return h.post("/api/rooms/"+code+"/claims/"+claimID, map[string]any{"token": token, "decision": decision})
}

// A player who closed the game can take their seat back from any other
// browser once the old one is gone and the game says yes; the old browser's
// session ends.
func TestReclaimSeatFromNewDevice(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "key", "198.51.100.21")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice", "tokenKind": "key"}, alice.opt())
	expectStatus(t, a, http.StatusOK)
	h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token})
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)

	laptop := h.device("Alice", "key", "198.51.100.22")
	// Still connected: refused.
	expectError(t, h.reclaim(tb.code, a.str("playerId"), laptop, nil), http.StatusConflict, msgSeatOnline)
	sum := h.get("/api/rooms/" + tb.code + "/summary")
	if p := summaryPlayerNamed(t, sum, "Alice"); p["online"] != true || p["reclaimable"] != false || p["id"] != a.str("playerId") {
		t.Fatalf("connected seat in summary: %v", p)
	}

	h.clock.Advance(onlineWindow + time.Second)
	h.state(tb.code, tb.token) // the table polls; only Alice went quiet
	sum = h.get("/api/rooms/" + tb.code + "/summary")
	if p := summaryPlayerNamed(t, sum, "Alice"); p["online"] != false || p["reclaimable"] != true || p["color"] != tokenColors["key"] {
		t.Fatalf("offline seat in summary: %v", p)
	}
	for _, raw := range sum.body["players"].([]any) {
		if p := raw.(map[string]any); p["input"] == "ai" && p["reclaimable"] != false {
			t.Fatalf("computer seats are never reclaimable: %v", p)
		}
	}
	if sum.body["tableOnline"] != true {
		t.Fatalf("tableOnline = %v", sum.body["tableOnline"])
	}

	// The new browser asks; the table sees the request with its code.
	ask := h.reclaim(tb.code, a.str("playerId"), laptop, nil)
	expectStatus(t, ask, http.StatusAccepted)
	claimID := ask.str("claim", "id")
	if ask.body["pending"] != true || claimID == "" || ask.str("claim", "status") != "pending" || ask.str("claim", "playerId") != a.str("playerId") {
		t.Fatalf("pending answer %s", ask.raw)
	}
	if strings.Contains(string(ask.raw), "code\":\"") && dig(ask.body, "claim", "code") != nil {
		t.Fatal("the asking device must not be told the approval code")
	}
	claims := stateClaims(h.state(tb.code, tb.token))
	if len(claims) != 1 || claims[0]["id"] != claimID || claims[0]["kind"] != "seat" || claims[0]["playerName"] != "Alice" ||
		claims[0]["deviceName"] != "Alice" || len(claims[0]["code"].(string)) != 4 {
		t.Fatalf("table's claims %v", claims)
	}
	// Asking again is the same request, not a second one.
	expectStatus(t, h.reclaim(tb.code, a.str("playerId"), laptop, nil), http.StatusAccepted)
	if claims := stateClaims(h.state(tb.code, tb.token)); len(claims) != 1 {
		t.Fatalf("repeat ask made %d requests", len(claims))
	}
	status := h.get("/api/rooms/"+tb.code+"/claims/"+claimID, laptop.opt())
	expectStatus(t, status, http.StatusOK)
	if status.str("status") != "pending" {
		t.Fatalf("status %s", status.raw)
	}
	// Other devices can't read someone else's request.
	expectError(t, h.get("/api/rooms/"+tb.code+"/claims/"+claimID, alice.opt()), http.StatusNotFound, msgClaimMissing)

	// The table approves; the laptop collects its seat.
	decided := h.decide(tb.code, tb.token, claimID, "approve")
	expectStatus(t, decided, http.StatusOK)
	if len(stateClaims(decided.body)) != 0 {
		t.Fatalf("an answered request stays in state: %v", decided.body["claims"])
	}
	if got := h.get("/api/rooms/"+tb.code+"/claims/"+claimID, laptop.opt()); got.str("status") != "approved" {
		t.Fatalf("status after approval %s", got.raw)
	}
	got := h.reclaim(tb.code, a.str("playerId"), laptop, nil)
	expectStatus(t, got, http.StatusOK)
	if got.str("playerId") != a.str("playerId") || got.str("token") == "" || got.str("token") == a.str("token") {
		t.Fatalf("reclaim result %v", got.body)
	}
	if dig(got.body, "state", "viewerId") != a.str("playerId") {
		t.Fatalf("reclaimed state is for %v", dig(got.body, "state", "viewerId"))
	}
	// The old browser's session is over, and its device no longer holds the seat.
	expectError(t, h.get("/api/rooms/"+tb.code+"/state?token="+a.str("token")), http.StatusBadRequest, msgSessionInvalid)
	expectError(t, h.post("/api/rooms/"+tb.code+"/rejoin", map[string]any{}, alice.opt()), http.StatusNotFound, msgNoSeat)
	// The new device can come back by itself from now on.
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/rejoin", map[string]any{}, laptop.opt()), http.StatusOK)
	// And it plays: the seat is a phone seat that is online again.
	if p := summaryPlayerNamed(t, h.get("/api/rooms/"+tb.code+"/summary"), "Alice"); p["online"] != true || p["input"] != "phone" {
		t.Fatalf("after reclaim: %v", p)
	}
}

// S1: a made-up device can no longer walk into an offline seat (the host's
// included). It only gets a pending request that the table can refuse, and
// guessing the code doesn't work.
func TestStrangerCannotTakeOfflineSeat(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "key", "198.51.100.25")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	expectStatus(t, a, http.StatusOK)
	h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token})
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second)
	h.state(tb.code, tb.token)

	// A well-formed device id the server has never seen (the review's attack).
	eve := device{"dev_madeUpByEve01", strings.Repeat("e", 43)}
	ask := h.reclaim(tb.code, a.str("playerId"), eve, nil)
	expectStatus(t, ask, http.StatusAccepted)
	if ask.str("token") != "" {
		t.Fatal("a stranger got a session")
	}
	// Alice's own session is untouched and she can come back.
	h.state(tb.code, a.str("token"))
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/rejoin", map[string]any{}, alice.opt()), http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second) // and she's away again
	h.state(tb.code, tb.token)

	// Wrong codes: refused, and after a few the request is refused for good.
	for i := 0; i < maxClaimAttempts; i++ {
		expectError(t, h.reclaim(tb.code, a.str("playerId"), eve, map[string]any{"approvalCode": "zzzz"}), http.StatusForbidden, msgClaimWrongCode)
	}
	expectError(t, h.reclaim(tb.code, a.str("playerId"), eve, nil), http.StatusForbidden, msgClaimDenied)
	if claims := stateClaims(h.state(tb.code, tb.token)); len(claims) != 0 {
		t.Fatalf("a refused request is still shown: %v", claims)
	}

	// The table can also say no outright.
	h.clock.Advance(onlineWindow + claimDenyCooldown + time.Second)
	h.state(tb.code, tb.token)
	mallory := h.device("Mallory", "rocket", "198.51.100.26")
	ask = h.reclaim(tb.code, a.str("playerId"), mallory, nil)
	expectStatus(t, ask, http.StatusAccepted)
	// Only the table or the host can answer; Alice's seat owner is offline.
	expectError(t, h.decide(tb.code, "not-a-token", ask.str("claim", "id"), "approve"), http.StatusBadRequest, msgSessionInvalid)
	expectStatus(t, h.decide(tb.code, tb.token, ask.str("claim", "id"), "deny"), http.StatusOK)
	if got := h.get("/api/rooms/"+tb.code+"/claims/"+ask.str("claim", "id"), mallory.opt()); got.str("status") != "denied" {
		t.Fatalf("status after deny %s", got.raw)
	}
	expectError(t, h.reclaim(tb.code, a.str("playerId"), mallory, nil), http.StatusForbidden, msgClaimDenied)
	if p := playerByID(h.state(tb.code, tb.token), a.str("playerId")); p["online"] != false {
		t.Fatalf("seat changed hands: %v", p)
	}
}

// The host lost their phone and the table is the only other screen: the
// 4-digit code the table shows lets their new phone in, host rights included.
func TestReclaimHostSeatWithTableCode(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "key", "198.51.100.31")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	expectStatus(t, a, http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second) // everyone is away for a moment
	phone := h.device("Alice", "key", "198.51.100.32")
	ask := h.reclaim(tb.code, a.str("playerId"), phone, nil)
	expectStatus(t, ask, http.StatusAccepted)
	// The table screen comes back and shows the request; Alice reads the code.
	claims := stateClaims(h.state(tb.code, tb.token))
	if len(claims) != 1 {
		t.Fatalf("table claims %v", claims)
	}
	got := h.reclaim(tb.code, a.str("playerId"), phone, map[string]any{"approvalCode": claims[0]["code"]})
	expectStatus(t, got, http.StatusOK)
	if dig(got.body, "state", "viewerIsHost") != true {
		t.Fatalf("host rights lost: %v", dig(got.body, "state", "viewerIsHost"))
	}
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": got.str("token")}), http.StatusOK)
	if claims := stateClaims(h.state(tb.code, tb.token)); len(claims) != 0 {
		t.Fatalf("used request still shown: %v", claims)
	}
}

// Without a table screen, when the host is the one away, the other players
// at the game see the request and can let the host's new phone in.
func TestReclaimApprovedByOtherPlayerWhenHostAway(t *testing.T) {
	h := newHarness(t)
	ana := h.device("Ana", "key", "198.51.100.35")
	g := h.post("/api/games", map[string]any{"name": "Ana"}, ana.opt())
	expectStatus(t, g, http.StatusCreated)
	code := g.str("code")
	ben := h.device("Ben", "rocket", "198.51.100.36")
	b := h.post("/api/rooms/"+code+"/join", map[string]any{"name": "Ben"}, ben.opt())
	expectStatus(t, b, http.StatusOK)
	// While the host is online, only the host sees requests.
	cat := h.device("Cat", "teapot", "198.51.100.37")
	c := h.post("/api/rooms/"+code+"/join", map[string]any{"name": "Cat"}, cat.opt())
	expectStatus(t, c, http.StatusOK)
	expectStatus(t, h.act(code, g.str("token"), map[string]any{"type": "start"}), http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second)
	h.state(code, g.str("token"))
	h.state(code, b.str("token"))
	newCat := h.device("Cat", "teapot", "198.51.100.38")
	askCat := h.reclaim(code, c.str("playerId"), newCat, nil)
	expectStatus(t, askCat, http.StatusAccepted)
	if len(stateClaims(h.state(code, b.str("token")))) != 0 {
		t.Fatal("Ben sees requests although the host is online")
	}
	if len(stateClaims(h.state(code, g.str("token")))) != 1 {
		t.Fatal("the host doesn't see the request")
	}

	// Now Ana's phone dies; Ben is the only one connected.
	h.clock.Advance(onlineWindow + time.Second)
	h.state(code, b.str("token"))
	newAna := h.device("Ana", "key", "198.51.100.39")
	ask := h.reclaim(code, g.str("playerId"), newAna, nil)
	expectStatus(t, ask, http.StatusAccepted)
	claims := stateClaims(h.state(code, b.str("token")))
	var forAna map[string]any
	for _, cl := range claims {
		if cl["playerId"] == g.str("playerId") {
			forAna = cl
		}
	}
	if forAna == nil {
		t.Fatalf("Ben can't see Ana's request: %v", claims)
	}
	expectStatus(t, h.decide(code, b.str("token"), ask.str("claim", "id"), "approve"), http.StatusOK)
	got := h.reclaim(code, g.str("playerId"), newAna, nil)
	expectStatus(t, got, http.StatusOK)
	if dig(got.body, "state", "viewerIsHost") != true {
		t.Fatal("Ana lost host rights")
	}
}

// A solo game against computers has nobody to ask: only the network the seat
// was played from may move it.
func TestReclaimSoloGameNeedsSameNetwork(t *testing.T) {
	h := newHarness(t)
	solo := h.device("Sol", "key", "198.51.100.41")
	g := h.post("/api/games", map[string]any{"name": "Sol"}, solo.opt(), fromIP("203.0.113.40"))
	expectStatus(t, g, http.StatusCreated)
	expectStatus(t, h.post("/api/rooms/"+g.str("code")+"/ai", map[string]any{"token": g.str("token")}), http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second)
	stranger := h.device("X", "rocket", "203.0.113.99")
	expectError(t, h.post("/api/rooms/"+g.str("code")+"/reclaim", map[string]any{"playerId": g.str("playerId")}, stranger.opt(), fromIP("203.0.113.99")),
		http.StatusForbidden, msgClaimNoApprover)
	laptop := h.device("Sol", "key", "203.0.113.40")
	got := h.post("/api/rooms/"+g.str("code")+"/reclaim", map[string]any{"playerId": g.str("playerId")}, laptop.opt(), fromIP("203.0.113.40"))
	expectStatus(t, got, http.StatusOK)
}

func TestReclaimRefusals(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	alice := h.device("Alice", "key", "198.51.100.41")
	bob := h.device("Bob", "rocket", "198.51.100.42")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	b := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Bob"}, bob.opt())
	pad := h.addSeat(tb, "controller", "Maya", "lighthouse", 0)
	ai := h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token})
	expectStatus(t, ai, http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second)

	stranger := h.device("Eve", "teapot", "198.51.100.43")
	padPlayer := dig(pad.body, "state", "localSeats", "0", "playerId").(string)
	expectError(t, h.post("/api/rooms/"+tb.code+"/reclaim", map[string]any{"playerId": padPlayer}, stranger.opt()), http.StatusBadRequest, msgSeatNotOwnable)
	for _, p := range players(ai.body) {
		if p["input"] == "ai" {
			expectError(t, h.post("/api/rooms/"+tb.code+"/reclaim", map[string]any{"playerId": p["id"]}, stranger.opt()), http.StatusBadRequest, msgSeatNotOwnable)
		}
	}
	expectError(t, h.post("/api/rooms/"+tb.code+"/reclaim", map[string]any{"playerId": "player_nope"}, stranger.opt()), http.StatusBadRequest, msgSeatUnknown)
	// Bob's phone can't also take Alice's seat.
	expectError(t, h.post("/api/rooms/"+tb.code+"/reclaim", map[string]any{"playerId": a.str("playerId")}, bob.opt()), http.StatusBadRequest, msgDeviceHasSeat)
	// Reclaiming your own seat is just a rejoin, with no approval.
	again := h.post("/api/rooms/"+tb.code+"/reclaim", map[string]any{"playerId": b.str("playerId")}, bob.opt())
	expectStatus(t, again, http.StatusOK)
	if again.str("playerId") != b.str("playerId") {
		t.Fatalf("own seat: %v", again.body)
	}
	// A device is required.
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/reclaim", map[string]any{"playerId": a.str("playerId")}), http.StatusUnauthorized)
	// Requests per room are capped.
	for i := 0; i < maxClaimsPerRoom; i++ {
		d := h.device("Crowd", "key", "198.51.100.50")
		expectStatus(t, h.post("/api/rooms/"+tb.code+"/reclaim", map[string]any{"playerId": a.str("playerId")}, d.opt(), fromIP("198.51.100.50")), http.StatusAccepted)
	}
	d := h.device("Crowd", "key", "198.51.100.51")
	expectError(t, h.post("/api/rooms/"+tb.code+"/reclaim", map[string]any{"playerId": a.str("playerId")}, d.opt(), fromIP("198.51.100.51")), http.StatusTooManyRequests, msgClaimsFull)
}

// A table-only room (keyboard/controller seats): the PC screen can be
// reopened in another browser on the same network once the old one is gone.
func TestResumeTable(t *testing.T) {
	h := newHarness(t)
	tb := h.createTable()
	expectError(t, h.post("/api/tables/"+tb.code+"/resume", map[string]any{}), http.StatusConflict, msgTableOnline)
	h.clock.Advance(onlineWindow + time.Second)
	if sum := h.get("/api/rooms/" + tb.code + "/summary"); sum.body["tableOnline"] != false {
		t.Fatalf("tableOnline after the screen went away = %v", sum.body["tableOnline"])
	}
	// S2: a stranger elsewhere can't take it.
	expectError(t, h.post("/api/tables/"+tb.code+"/resume", map[string]any{}, fromIP("203.0.113.66")), http.StatusForbidden, msgTableNoApprover)
	got := h.post("/api/tables/"+strings.ToLower(tb.code)+"/resume", map[string]any{})
	expectStatus(t, got, http.StatusOK)
	if got.str("tableToken") == "" || got.str("code") != tb.code || dig(got.body, "state", "viewerRole") != "table" {
		t.Fatalf("resume result %v", got.body)
	}
	expectError(t, h.get("/api/rooms/"+tb.code+"/state?token="+tb.token), http.StatusBadRequest, msgSessionInvalid)
	expectStatus(t, h.act(tb.code, got.str("tableToken"), map[string]any{"type": "set_dice_mode", "mode": "choice"}), http.StatusOK)
	// Only table rooms have a table screen.
	room := h.createRoom("Ann")
	expectError(t, h.post("/api/tables/"+room.code+"/resume", map[string]any{}), http.StatusBadRequest, msgNoTableScreen)
	expectError(t, h.post("/api/tables/ZZZZZ/resume", map[string]any{}), http.StatusBadRequest, msgRoomMissing)
}

// S2: once phones are at the game, a new browser becomes the table only with
// the host's approval; the browser that was the table, and the host's own
// device, get straight back in.
func TestResumeTableNeedsApproval(t *testing.T) {
	h := newHarness(t)
	pc := h.device("PC", "", "198.51.100.60")
	tbRes := h.post("/api/tables", map[string]any{}, pc.opt(), fromIP("198.51.100.60"))
	expectStatus(t, tbRes, http.StatusCreated)
	code, tableToken := tbRes.str("code"), tbRes.str("tableToken")
	alice := h.device("Alice", "key", "198.51.100.61")
	a := h.post("/api/rooms/"+code+"/join", map[string]any{"name": "Alice"}, alice.opt(), fromIP("198.51.100.61"))
	expectStatus(t, a, http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second)
	h.state(code, a.str("token"))

	// The same browser is back without asking anyone, from anywhere.
	back := h.post("/api/tables/"+code+"/resume", map[string]any{}, pc.opt(), fromIP("198.51.100.99"))
	expectStatus(t, back, http.StatusOK)
	_ = tableToken
	h.clock.Advance(onlineWindow + time.Second)
	h.state(code, a.str("token"))

	// Another browser must ask; without a device it can't even ask.
	expectError(t, h.post("/api/tables/"+code+"/resume", map[string]any{}, fromIP("203.0.113.70")), http.StatusUnauthorized, msgDeviceNeeded)
	tv := h.device("TV", "", "203.0.113.70")
	ask := h.post("/api/tables/"+code+"/resume", map[string]any{}, tv.opt(), fromIP("203.0.113.70"))
	expectStatus(t, ask, http.StatusAccepted)
	if ask.str("claim", "kind") != "table" {
		t.Fatalf("pending answer %s", ask.raw)
	}
	claims := stateClaims(h.state(code, a.str("token")))
	if len(claims) != 1 || claims[0]["kind"] != "table" || claims[0]["playerId"] != nil {
		t.Fatalf("host sees %v", claims)
	}
	expectStatus(t, h.decide(code, a.str("token"), ask.str("claim", "id"), "approve"), http.StatusOK)
	got := h.post("/api/tables/"+code+"/resume", map[string]any{}, tv.opt(), fromIP("203.0.113.70"))
	expectStatus(t, got, http.StatusOK)
	if dig(got.body, "state", "viewerRole") != "table" {
		t.Fatalf("resume result %s", got.raw)
	}
	expectError(t, h.get("/api/rooms/"+code+"/state?token="+back.str("tableToken")), http.StatusBadRequest, msgSessionInvalid)

	// The host's own device can open the table directly.
	h.clock.Advance(onlineWindow + time.Second)
	h.state(code, a.str("token"))
	expectStatus(t, h.post("/api/tables/"+code+"/resume", map[string]any{}, alice.opt(), fromIP("203.0.113.71")), http.StatusOK)
}
