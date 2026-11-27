package server

import (
	"net/http"
	"testing"
	"time"
)

// A game can start on a phone: the phone is the host, adds computers, starts,
// and a PC can put the same game on the big screen later.
func TestPhoneHostedGame(t *testing.T) {
	h := newHarness(t)
	ana := h.device("Ana", "key", "198.51.100.71")
	expectError(t, h.post("/api/games", map[string]any{"name": "Ana"}), http.StatusUnauthorized, msgDeviceNeeded)
	g := h.post("/api/games", map[string]any{"name": "Ana", "tokenKind": "key"}, ana.opt())
	expectStatus(t, g, http.StatusCreated)
	code, token := g.str("code"), g.str("token")
	if code == "" || token == "" || g.str("playerId") == "" {
		t.Fatalf("create result %s", g.raw)
	}
	if dig(g.body, "state", "viewerIsHost") != true || dig(g.body, "state", "status") != "lobby" {
		t.Fatalf("creator should host the lobby: %v %v", dig(g.body, "state", "viewerIsHost"), dig(g.body, "state", "status"))
	}
	if p := playerByID(g.body["state"].(map[string]any), g.str("playerId")); p["tokenKind"] != "key" || p["input"] != "phone" || p["color"] != tokenColors["key"] {
		t.Fatalf("host seat %v", p)
	}
	// The host phone runs the lobby.
	expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": "hard"}), http.StatusOK)
	ben := h.device("Ben", "rocket", "198.51.100.72")
	b := h.post("/api/rooms/"+code+"/join", map[string]any{"name": "Ben", "tokenKind": "rocket"}, ben.opt())
	expectStatus(t, b, http.StatusOK)
	if dig(b.body, "state", "viewerIsHost") == true {
		t.Fatal("only the creator hosts")
	}
	expectStatus(t, h.act(code, token, map[string]any{"type": "start"}), http.StatusOK)
	// A PC can show it on the big screen once the host says yes.
	pcDevice := h.device("PC", "", "198.51.100.73")
	ask := h.post("/api/tables/"+code+"/resume", map[string]any{}, pcDevice.opt())
	expectStatus(t, ask, http.StatusAccepted)
	claims := stateClaims(h.state(code, token))
	if len(claims) != 1 || claims[0]["kind"] != "table" {
		t.Fatalf("host sees %v", claims)
	}
	expectStatus(t, h.decide(code, token, ask.str("claim", "id"), "approve"), http.StatusOK)
	pc := h.post("/api/tables/"+code+"/resume", map[string]any{}, pcDevice.opt())
	expectStatus(t, pc, http.StatusOK)
	if dig(pc.body, "state", "viewerRole") != "table" {
		t.Fatalf("PC view %v", dig(pc.body, "state", "viewerRole"))
	}
	// The creator's device gets their seat back from the game itself.
	expectStatus(t, h.post("/api/rooms/"+code+"/rejoin", map[string]any{}, ana.opt()), http.StatusOK)
}

// S3: the games list shows only your own games (seat, table screen, watching
// or invited) and games their host opened to everyone; strangers never see
// which seats could be taken over.
func TestGamesList(t *testing.T) {
	h := newHarness(t)
	ana := h.device("Ana", "key", "198.51.100.81")
	ben := h.device("Ben", "rocket", "198.51.100.82")
	mine := h.post("/api/games", map[string]any{"name": "Ana"}, ana.opt())
	expectStatus(t, mine, http.StatusCreated)
	other := h.post("/api/games", map[string]any{"name": "Ben"}, ben.opt())
	expectStatus(t, other, http.StatusCreated)
	expectStatus(t, h.act(other.str("code"), other.str("token"), map[string]any{"type": "start"}), http.StatusBadRequest) // needs 2 players
	h.createTable()                                                                                                       // a PC table nobody joined

	list := h.get("/api/games", ana.opt())
	expectStatus(t, list, http.StatusOK)
	games := list.body["games"].([]any)
	if len(games) != 1 {
		t.Fatalf("Ana should see only her own game, got %d: %s", len(games), list.raw)
	}
	first := games[0].(map[string]any)
	if first["code"] != mine.str("code") || first["mine"] != true || first["playerId"] != mine.str("playerId") || first["listed"] != false {
		t.Fatalf("own game must be listed and marked: %v", first)
	}
	if dig(first, "host", "name") != "Ana" || first["openSeats"] != float64(7) || first["status"] != "lobby" {
		t.Fatalf("listing %v", first)
	}
	// A stranger (no device, or a fresh one) sees nothing.
	if games := h.get("/api/games").body["games"].([]any); len(games) != 0 {
		t.Fatalf("anonymous list %v", games)
	}
	eve := h.device("Eve", "teapot", "198.51.100.83")
	if games := h.get("/api/games", eve.opt()).body["games"].([]any); len(games) != 0 {
		t.Fatalf("stranger's list %v", games)
	}
	// Only the host can open a game to everyone.
	expectError(t, h.post("/api/rooms/"+mine.str("code")+"/settings", map[string]any{"token": mine.str("token"), "listed": "yes"}), http.StatusBadRequest, msgBadListed)
	set := h.post("/api/rooms/"+other.str("code")+"/settings", map[string]any{"token": other.str("token"), "listed": true})
	expectStatus(t, set, http.StatusOK)
	if dig(set.body, "roomSettings", "listed") != true {
		t.Fatalf("roomSettings %v", set.body["roomSettings"])
	}
	games = h.get("/api/games", eve.opt()).body["games"].([]any)
	if len(games) != 1 || dig(games[0], "code") != other.str("code") || dig(games[0], "mine") != false || dig(games[0], "listed") != true {
		t.Fatalf("stranger should see the open game only: %v", games)
	}
	// Watching makes a game yours to find again.
	expectStatus(t, h.post("/api/rooms/"+mine.str("code")+"/watch", map[string]any{"name": "Eve"}, eve.opt()), http.StatusOK)
	if games := h.get("/api/games", eve.opt()).body["games"].([]any); len(games) != 2 {
		t.Fatalf("watcher's list %v", games)
	}

	// Ben's game goes quiet; strangers never see takeable seats in it.
	h.clock.Advance(onlineWindow + time.Second)
	h.get("/api/rooms/" + mine.str("code") + "/state?token=" + mine.str("token"))
	b2 := h.device("Bea", "bicycle", "198.51.100.84")
	expectStatus(t, h.post("/api/rooms/"+other.str("code")+"/join", map[string]any{"name": "Bea"}, b2.opt()), http.StatusOK)
	h.clock.Advance(onlineWindow + time.Second)
	h.get("/api/rooms/" + other.str("code") + "/state?token=" + other.str("token"))
	for _, raw := range h.get("/api/games", eve.opt()).body["games"].([]any) {
		for _, p := range dig(raw, "players").([]any) {
			if dig(p, "reclaimable") == true && dig(raw, "code") == other.str("code") {
				t.Fatalf("a stranger is shown a takeable seat: %v", p)
			}
		}
	}
}

// 0.10.2: the PC's NEW TABLE leaves the old lobby behind. With only computers
// seated it drops out of the games list once its screen is offline, and it is
// pruned after emptyRoomTTL instead of roomTTL. A lobby with a person stays.
func TestComputerOnlyLobbyHiddenAndPrunedSoon(t *testing.T) {
	h := newHarness(t)
	pc := h.device("PC", "key", "198.51.100.91")
	made := h.post("/api/tables", map[string]any{}, pc.opt())
	expectStatus(t, made, http.StatusCreated)
	code, token := made.str("code"), made.str("tableToken")
	expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": "easy"}), http.StatusOK)
	listed := func() bool {
		for _, g := range h.get("/api/games", pc.opt()).body["games"].([]any) {
			if dig(g, "code") == code {
				return true
			}
		}
		return false
	}
	if !listed() {
		t.Fatalf("the PC's own lobby is listed while its screen is online")
	}
	h.clock.Advance(onlineWindow + time.Second)
	if listed() {
		t.Fatalf("a computer-only lobby whose screen went away must not be listed")
	}
	h.clock.Advance(emptyRoomTTL)
	h.srv.pruneRooms(h.srv.now())
	h.srv.mu.Lock()
	_, kept := h.srv.rooms[code]
	h.srv.mu.Unlock()
	if kept {
		t.Fatalf("a computer-only lobby is pruned after emptyRoomTTL")
	}

	// A lobby with a person in it is kept for the full roomTTL.
	ana := h.device("Ana", "rocket", "198.51.100.92")
	game := h.post("/api/games", map[string]any{"name": "Ana"}, ana.opt())
	expectStatus(t, game, http.StatusCreated)
	expectStatus(t, h.post("/api/rooms/"+game.str("code")+"/ai", map[string]any{"token": game.str("token"), "difficulty": "easy"}), http.StatusOK)
	h.clock.Advance(emptyRoomTTL + onlineWindow + time.Second)
	h.srv.pruneRooms(h.srv.now())
	h.srv.mu.Lock()
	_, kept = h.srv.rooms[game.str("code")]
	h.srv.mu.Unlock()
	if !kept {
		t.Fatalf("a lobby with a person must not be pruned early")
	}
}
