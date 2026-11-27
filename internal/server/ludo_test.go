package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/ludo"
)

// withLudo adds the Ludo game type with fixed dice and the screens' waits
// shrunk, so whole games play out in a test.
func withLudo(seed byte) harnessOption {
	return func(c *Config) {
		s := [32]byte{seed}
		if c.Engines == nil {
			c.Engines = map[string]Engine{}
		}
		c.Engines[GameLudo] = LudoEngine{Seed: &s}
		c.pokerTick = 5 * time.Millisecond
		c.ludoPace = 0.002
	}
}

func (h *harness) ludoGame(name string, d device) seat {
	h.t.Helper()
	r := h.post("/api/games", map[string]any{"name": name, "gameType": "ludo"}, d.opt())
	expectStatus(h.t, r, http.StatusCreated)
	return seat{r.str("code"), r.str("token"), r.str("playerId")}
}

func (h *harness) withLudoGame(code string, fn func(*ludo.Game)) {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	fn(ludoOf(h.srv.rooms[code]).g)
}

// Game type plumbing: created from a phone or a PC table; the games list
// and summary say "ludo"; computer levels Easy, Normal and Hard; keyboard and
// controller seats allowed (nothing is secret); Estate-only routes refuse.
func TestLudoGameTypes(t *testing.T) {
	h := newHarness(t, withLudo(1))
	alice := h.device("Alice", "rocket", "198.51.100.181")
	res := h.post("/api/games", map[string]any{"name": "Alice", "gameType": "ludo"}, alice.opt())
	expectStatus(t, res, http.StatusCreated)
	st := res.body["state"].(map[string]any)
	if st["game"] != "ludo" || st["gameType"] != "ludo" || st["viewerRole"] != "player" || dig(st, "you", "id") != res.str("playerId") {
		t.Fatalf("host's Ludo lobby: %v %v %v %v", st["game"], st["gameType"], st["viewerRole"], st["you"])
	}
	if got := strings.Join(strs(st["aiLevels"]), ","); got != "easy,normal,hard" {
		t.Fatalf("aiLevels %q", got)
	}
	if dig(st, "settings", "pieces") != float64(4) || dig(st, "settings", "safeSquares") != true || dig(st, "board", "arms") != float64(4) || dig(st, "seatLimit") != float64(6) {
		t.Fatalf("settings %v board %v", st["settings"], st["board"])
	}
	code, token := res.str("code"), res.str("token")
	if p := playerByID(st, res.str("playerId")); p["input"] != "phone" || p["tokenKind"] != "rocket" || p["online"] != true || p["color"] != "#1EA0FF" || len(p["pieces"].([]any)) != 4 {
		t.Fatalf("host player %v", p)
	}
	list := h.get("/api/games", alice.opt())
	if dig(list.body, "games", "0", "gameType") != "ludo" || dig(list.body, "games", "0", "rules", "pieces") != float64(4) {
		t.Fatalf("games list %s", list.raw)
	}
	sum := h.get("/api/rooms/" + code + "/summary")
	if sum.str("gameType") != "ludo" || dig(sum.body, "openSeats") != float64(5) {
		t.Fatalf("summary %s", sum.raw)
	}
	// Computers: the three Ludo levels, each seat with its own Ludo bot.
	expectError(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": "grandmaster"}), http.StatusBadRequest, "Choose easy, normal or hard")
	ai := h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": "hard"})
	expectStatus(t, ai, http.StatusOK)
	var aiID string
	for _, p := range players(ai.body) {
		if p["kind"] == "ai" {
			aiID = p["id"].(string)
			if p["difficulty"] != "hard" || p["input"] != "ai" {
				t.Fatalf("computer seat %v", p)
			}
		}
	}
	h.srv.mu.Lock()
	m := h.srv.rooms[code].meta[aiID]
	ok := m != nil && m.ludoBot != nil && m.bot == nil && m.pokerBot == nil
	h.srv.mu.Unlock()
	if !ok {
		t.Fatal("Ludo computer seats get a Ludo bot")
	}
	expectError(t, h.post("/api/rooms/"+code+"/trade-quote", map[string]any{"token": token, "targetId": aiID}), http.StatusBadRequest, msgPokerNoQuotes)
	expectError(t, h.post("/api/rooms/"+code+"/not-selling", map[string]any{"token": token, "spaceIndex": 1, "on": true}), http.StatusBadRequest, msgPokerNoQuotes)

	// A PC table: the table is the host, and keyboard and controller seats work.
	tb := h.post("/api/tables", map[string]any{"gameType": "ludo"})
	expectStatus(t, tb, http.StatusCreated)
	tbl := table{tb.str("code"), tb.str("tableToken")}
	if dig(tb.body, "state", "game") != "ludo" || dig(tb.body, "state", "viewerRole") != "table" || dig(tb.body, "state", "you") != nil {
		t.Fatalf("ludo table %s", tb.raw)
	}
	kb := h.addSeat(tbl, "keyboard", "Kim", "key", nil)
	expectStatus(t, kb, http.StatusCreated)
	pad := h.addSeat(tbl, "controller", "Pat", "anchor", 0)
	expectStatus(t, pad, http.StatusCreated)
	st = h.state(tbl.code, tbl.token)
	if seats := st["localSeats"].([]any); len(seats) != 2 {
		t.Fatalf("local seats %v", seats)
	}
	// The table changes the rules and starts (no person holds the host seat on a PC table).
	expectStatus(t, h.act(tbl.code, tbl.token, map[string]any{"type": "settings", "settings": map[string]any{"pieces": 2, "blocks": true}}), http.StatusOK)
	started := h.act(tbl.code, tbl.token, map[string]any{"type": "start"})
	expectStatus(t, started, http.StatusOK)
	if dig(started.body, "status") != "playing" || dig(started.body, "settings", "pieces") != float64(2) {
		t.Fatalf("table start: %s", started.raw)
	}
	expectError(t, h.act(tbl.code, tbl.token, map[string]any{"type": "roll"}), http.StatusBadRequest, msgTableNoSeat)
	// Whoever's turn it is, the table acts for that local seat.
	turn := dig(started.body, "turnId").(string)
	seatID := kb.str("seatId")
	if turn != playerByID(started.body, turn)["id"] {
		t.Fatal("turn id")
	}
	for _, s := range st["localSeats"].([]any) {
		if s.(map[string]any)["playerId"] == turn {
			seatID = s.(map[string]any)["seatId"].(string)
		}
	}
	rolled := h.act(tbl.code, tbl.token, map[string]any{"type": "roll"}, map[string]any{"seatId": seatID})
	expectStatus(t, rolled, http.StatusOK)
	if dig(rolled.body, "rollSeq") != float64(1) {
		t.Fatalf("the keyboard seat rolled: %s", rolled.raw)
	}
	// Without Ludo configured the type is unknown, and Estate stays the default.
	h2 := newHarness(t)
	d2 := h2.device("Dee", "key", "198.51.100.183")
	expectError(t, h2.post("/api/games", map[string]any{"name": "Dee", "gameType": "ludo"}, d2.opt()), http.StatusBadRequest, "unknown game type")
	est := h.post("/api/tables", map[string]any{})
	if dig(est.body, "state", "game") != nil || dig(est.body, "state", "gameType") != "estate" {
		t.Fatal("estate stays the default")
	}
}

// A table of four computers plays a whole game to the end over WebSockets,
// every one of its events and states arriving in order; then a rematch keeps
// the players, the rules and the code.
func TestLudoComputersPlayAWholeGame(t *testing.T) {
	h := newHarness(t, withLudo(2))
	tb := h.post("/api/tables", map[string]any{"gameType": "ludo"})
	code, token := tb.str("code"), tb.str("tableToken")
	for _, level := range []string{"easy", "normal", "hard", "normal"} {
		expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": level}), http.StatusOK)
	}
	expectStatus(t, h.act(code, token, map[string]any{"type": "settings", "settings": map[string]any{"pieces": 2}}), http.StatusOK)
	rec := h.record(code, token) // a person (the table screen) is watching, so computers play
	expectStatus(t, h.act(code, token, map[string]any{"type": "start"}), http.StatusOK)
	waitFor(t, "the computers to finish the game", 60*time.Second, func() bool {
		st := rec.state()
		return st != nil && st["status"] == "finished"
	})
	st := rec.state()
	standings := st["standings"].([]any)
	if len(standings) != 4 || dig(st, "winnerId") != standings[0].(map[string]any)["id"] {
		t.Fatalf("standings %v", standings)
	}
	kinds := map[string]int{}
	lastSeq := 0.0
	for _, ev := range rec.events() {
		kinds[ev["type"].(string)]++
		if ev["seq"].(float64) <= lastSeq {
			t.Fatalf("event %v out of order", ev)
		}
		lastSeq = ev["seq"].(float64)
	}
	for _, kind := range []string{ludo.EvStart, ludo.EvRoll, ludo.EvMove, ludo.EvTurn, ludo.EvFinish, ludo.EvGameOver} {
		if kinds[kind] == 0 {
			t.Fatalf("no %q events: %v", kind, kinds)
		}
	}
	// Rematch: same code, same four players and rules, back in the lobby.
	again := h.post("/api/rooms/"+code+"/rematch", map[string]any{"token": token})
	expectStatus(t, again, http.StatusOK)
	if dig(again.body, "status") != "lobby" || len(players(again.body)) != 4 || dig(again.body, "settings", "pieces") != float64(2) || dig(again.body, "code") != code {
		t.Fatalf("rematch %s", again.raw)
	}
	if v := dig(again.body, "version").(float64); v <= st["version"].(float64) {
		t.Fatalf("the rematch lobby must have a newer version (%v after %v)", v, st["version"])
	}
	for _, p := range players(again.body) {
		if p["input"] != "ai" || p["difficulty"] == nil || len(p["pieces"].([]any)) != 2 {
			t.Fatalf("rematch seat %v", p)
		}
	}
}

// People roll for themselves; when they have nothing to choose (one piece
// can move, or every piece would come out of the yard) the server moves for
// them after the dice land; with a real choice it waits.
func TestLudoForcedMovesAndRealChoices(t *testing.T) {
	h := newHarness(t, withLudo(3))
	ann := h.ludoGame("Ann", h.device("Ann", "rocket", "198.51.100.191"))
	bob := h.post("/api/rooms/"+ann.code+"/join", map[string]any{"name": "Bob"}, h.device("Bob", "teapot", "198.51.100.193").opt())
	expectStatus(t, bob, http.StatusOK)
	bobToken, bobID := bob.str("token"), bob.str("playerId")
	recA := h.record(ann.code, ann.token)
	recB := h.record(ann.code, bobToken)
	expectStatus(t, h.act(ann.code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	tokens := map[string]string{ann.playerID: ann.token, bobID: bobToken}
	// Make it Ann's turn with two pieces out on different squares and one in the yard.
	h.withLudoGame(ann.code, func(g *ludo.Game) {
		g.Turn = 0
		g.Players[0].Pieces[0] = ludo.Piece{Zone: ludo.ZoneTrack, Step: 3}
		g.Players[0].Pieces[1] = ludo.Piece{Zone: ludo.ZoneTrack, Step: 10}
		g.QueueDice(4)
	})
	expectStatus(t, h.act(ann.code, tokens[ann.playerID], map[string]any{"type": "roll"}), http.StatusOK)
	time.Sleep(150 * time.Millisecond)
	if st := h.state(ann.code, ann.token); st["phase"] != "move" || dig(st, "you", "toMove") != true || len(st["moves"].([]any)) != 2 {
		t.Fatalf("a real choice waits for Ann: phase %v moves %v", st["phase"], st["moves"])
	}
	expectStatus(t, h.act(ann.code, ann.token, map[string]any{"type": "move", "piece": 1}), http.StatusOK)
	// Bob: only one piece can move → the server moves it.
	h.withLudoGame(ann.code, func(g *ludo.Game) {
		g.Players[1].Pieces[0] = ludo.Piece{Zone: ludo.ZoneTrack, Step: 5}
		g.QueueDice(3)
	})
	expectStatus(t, h.act(ann.code, bobToken, map[string]any{"type": "roll"}), http.StatusOK)
	waitFor(t, "Bob's only move played for him", 3*time.Second, func() bool {
		st := recB.state()
		return st != nil && dig(st, "lastMove", "playerId") == bobID && st["turnId"] == ann.playerID
	})
	if recA.state()["phase"] != "roll" {
		t.Fatal("after Bob's forced move it's Ann's roll")
	}
	// Errors are plain English.
	expectError(t, h.act(ann.code, bobToken, map[string]any{"type": "roll"}), http.StatusBadRequest, "it is not your turn")
	expectError(t, h.act(ann.code, ann.token, map[string]any{"type": "move", "piece": 0}), http.StatusBadRequest, "roll the die first")
}

// An away player's turn: after the stand-in delay the computer plays for them
// (rolls and moves) until they are back.
func TestLudoStandInPlaysForAwayPlayers(t *testing.T) {
	h := newHarness(t, withLudo(4), func(c *Config) { c.AutopilotDelay = 60 * time.Millisecond })
	ann := h.ludoGame("Ann", h.device("Ann", "rocket", "198.51.100.201"))
	bob := h.post("/api/rooms/"+ann.code+"/join", map[string]any{"name": "Bob"}, h.device("Bob", "teapot", "198.51.100.202").opt())
	bobToken, bobID := bob.str("token"), bob.str("playerId")
	recA := h.record(ann.code, ann.token)
	recB := h.record(ann.code, bobToken)
	expectStatus(t, h.act(ann.code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	h.withLudoGame(ann.code, func(g *ludo.Game) { g.Turn = 1 }) // Bob's turn
	h.srv.mu.Lock()
	h.srv.commit(h.srv.rooms[ann.code])
	h.srv.mu.Unlock()
	recB.sock.conn.CloseNow() // Bob walks away
	sawAutopilot := false
	waitFor(t, "the stand-in to roll for Bob", 10*time.Second, func() bool {
		st := recA.state()
		if p := playerByID(st, bobID); p != nil && p["autopilot"] == true {
			sawAutopilot = true
		}
		if dig(st, "you", "toRoll") == true {
			h.act(ann.code, ann.token, map[string]any{"type": "roll"})
		} else if dig(st, "you", "toMove") == true {
			moves := st["moves"].([]any)
			h.act(ann.code, ann.token, map[string]any{"type": "move", "piece": moves[0].(map[string]any)["piece"]})
		}
		return sawAutopilot && st["diceBy"] == bobID
	})
	// Bob is back: the stand-in stops and the game waits for his roll.
	recB = h.record(ann.code, bobToken)
	waitFor(t, "Bob's seat handed back", 3*time.Second, func() bool {
		p := playerByID(recB.state(), bobID)
		return p != nil && p["autopilot"] == nil && p["online"] == true
	})
	waitFor(t, "Bob's turn", 10*time.Second, func() bool {
		st := recA.state()
		if dig(st, "you", "toRoll") == true {
			h.act(ann.code, ann.token, map[string]any{"type": "roll"})
		} else if dig(st, "you", "toMove") == true {
			moves := st["moves"].([]any)
			h.act(ann.code, ann.token, map[string]any{"type": "move", "piece": moves[0].(map[string]any)["piece"]})
		}
		return st["turnId"] == bobID && st["phase"] == "roll"
	})
	before := recB.state()["version"]
	time.Sleep(250 * time.Millisecond)
	if after := recB.state(); after["version"] != before || after["turnId"] != bobID {
		t.Fatalf("the game rolled without Bob (version %v → %v)", before, after["version"])
	}
}

// A restart in the middle of a game: rooms.json holds the type, the seats and
// the dice stream; the restored game rolls exactly what the old one would
// have, and old events are not sent again.
func TestLudoRestartMidGame(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, withLudo(5), func(c *Config) { c.DataDir = dir; c.SaveDelay = 10 * time.Millisecond })
	ann := h.ludoGame("Ann", h.device("Ann", "rocket", "198.51.100.211"))
	bob := h.post("/api/rooms/"+ann.code+"/join", map[string]any{"name": "Bob"}, h.device("Bob", "teapot", "198.51.100.212").opt())
	tokens := map[string]string{ann.playerID: ann.token, bob.str("playerId"): bob.str("token")}
	expectStatus(t, h.act(ann.code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	turn := h.state(ann.code, ann.token)["turnId"].(string)
	// A 3 with every piece in the yard: no move, the turn passes, nothing pending.
	h.withLudoGame(ann.code, func(g *ludo.Game) { g.QueueDice(3) })
	expectStatus(t, h.act(ann.code, tokens[turn], map[string]any{"type": "roll"}), http.StatusOK)
	before := h.state(ann.code, ann.token)
	var saved []byte
	var savedSeq int64
	h.srv.mu.Lock()
	r := h.srv.rooms[ann.code]
	saved, savedSeq = append([]byte(nil), r.lastJSON...), r.eventSeq
	h.srv.mu.Unlock()
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(dir, roomsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file), `"gameType":"ludo"`) || !strings.Contains(string(file), fmt.Sprintf(`"eventSeq":%d`, savedSeq)) {
		t.Fatal("rooms.json lacks the game type or the event sequence")
	}
	h2 := newHarness(t, withLudo(99), func(c *Config) { c.DataDir = dir }) // another seed: the saved stream wins
	after := h2.state(ann.code, ann.token)
	for _, st := range []map[string]any{before, after} {
		for _, p := range players(st) {
			delete(p, "online") // sessions come back offline until their phones reconnect
		}
	}
	for _, key := range []string{"game", "status", "phase", "turnId", "dice", "rollSeq", "moves", "players"} {
		if fmt.Sprint(before[key]) != fmt.Sprint(after[key]) {
			t.Fatalf("%s changed across the restart: %v → %v", key, before[key], after[key])
		}
	}
	rec := h2.record(ann.code, ann.token)
	waitFor(t, "the first state", 2*time.Second, func() bool { return rec.state() != nil })
	time.Sleep(50 * time.Millisecond)
	for _, ev := range rec.events() {
		if int64(ev["seq"].(float64)) <= savedSeq {
			t.Fatalf("event %v replayed after the restart", ev)
		}
	}
	// The next rolls are the saved stream's: a copy restored from the saved
	// snapshot predicts them (it plays the same moves, the server's forced
	// moves included).
	predict, err := ludo.Restore(saved, ludo.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12 && predict.Status == ludo.StatusPlaying; i++ {
		id := predict.Current().ID
		if predict.Phase == ludo.PhaseMove {
			if piece, forced := predict.ForcedMove(); forced {
				if err := predict.Do(id, ludo.MoveCommand(piece)); err != nil {
					t.Fatal(err)
				}
				waitFor(t, "the server's forced move", 3*time.Second, func() bool {
					return dig(h2.state(ann.code, ann.token), "lastMove", "seq") == float64(predict.MoveSeq)
				})
				continue
			}
		}
		action := ludo.Command{Type: "roll"}
		if predict.Phase == ludo.PhaseMove {
			action = ludo.MoveCommand(predict.Moves[0].Piece)
		}
		raw, _ := json.Marshal(action)
		expectStatus(t, h2.act(ann.code, tokens[id], json.RawMessage(raw)), http.StatusOK)
		if err := predict.PerformAction(id, raw); err != nil {
			t.Fatalf("prediction: %v", err)
		}
		if got := h2.state(ann.code, ann.token)["dice"]; got != float64(predict.Dice) {
			t.Fatalf("roll %d after the restart: %v, the saved stream says %d", i, got, predict.Dice)
		}
	}
}

// END GAME ranks by how far the pieces got.
func TestLudoEndGame(t *testing.T) {
	h := newHarness(t, withLudo(6))
	ann := h.ludoGame("Ann", h.device("Ann", "rocket", "198.51.100.221"))
	h.post("/api/rooms/"+ann.code+"/join", map[string]any{"name": "Bob"}, h.device("Bob", "teapot", "198.51.100.222").opt())
	expectStatus(t, h.act(ann.code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	h.withLudoGame(ann.code, func(g *ludo.Game) {
		g.Players[1].Pieces[0] = ludo.Piece{Zone: ludo.ZoneColumn, Step: 3}
	})
	res := h.post("/api/rooms/"+ann.code+"/end", map[string]any{"token": ann.token})
	expectStatus(t, res, http.StatusOK)
	if dig(res.body, "status") != "finished" || dig(res.body, "winnerId") == ann.playerID {
		t.Fatalf("Bob's piece is further: he wins (%v)", dig(res.body, "winnerId"))
	}
}
