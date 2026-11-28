package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/gamerecord"
	"github.com/tdar200/estate-games/internal/quiz"
)

// quizTestLibrary is the engine's small test pack set (14 categories over 13
// topics, 3 finals): server tests stay put while the real packs grow.
var quizTestLibrary = sync.OnceValues(func() (*quiz.Library, error) {
	return quiz.LoadLibrary(os.DirFS(filepath.Join("..", "quiz", "testdata", "packs")))
})

// withQuiz adds the quiz game type with a seeded deal and seeded computers,
// the test packs, and the room timer's sleeps capped at 5 ms so the room
// follows the harness clock closely.
func withQuiz(t *testing.T, seed byte) harnessOption {
	t.Helper()
	lib, err := quizTestLibrary()
	if err != nil {
		t.Fatal(err)
	}
	return func(c *Config) {
		s := [32]byte{seed, 31: 0x51}
		if c.Engines == nil {
			c.Engines = map[string]Engine{}
		}
		c.Engines[GameQuiz] = QuizEngine{Seed: &s, Library: lib}
		c.pokerTick = 5 * time.Millisecond
	}
}

func (h *harness) quizGame(name string, d device) seat {
	h.t.Helper()
	r := h.post("/api/games", map[string]any{"name": name, "gameType": "quiz"}, d.opt())
	expectStatus(h.t, r, http.StatusCreated)
	return seat{r.str("code"), r.str("token"), r.str("playerId")}
}

func (h *harness) quizJoin(code, name string, d device) seat {
	h.t.Helper()
	r := h.post("/api/rooms/"+code+"/join", map[string]any{"name": name}, d.opt())
	expectStatus(h.t, r, http.StatusOK)
	return seat{r.str("code"), r.str("token"), r.str("playerId")}
}

// withQuizGame runs fn on a quiz room's game under the server lock.
func (h *harness) withQuizGame(code string, fn func(g *quiz.Game)) {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	fn(quizOf(h.srv.rooms[code]).g)
}

// quizRun moves the harness clock from deadline to deadline, running the
// room's timer at each, until done holds. The people's phones count as
// connected throughout (they would be polling), so the room never pauses
// for being empty.
func (h *harness) quizRun(code string, done func(g *quiz.Game) bool) {
	h.t.Helper()
	for range 20000 {
		h.srv.mu.Lock()
		r := h.srv.rooms[code]
		g := quizOf(r).g
		if done(g) {
			h.srv.mu.Unlock()
			return
		}
		deadline, phase := g.NextDeadline(), g.Phase
		if deadline == 0 {
			h.srv.mu.Unlock()
			h.t.Fatalf("the quiz waits on no timer in phase %s", phase)
		}
		if wait := time.UnixMilli(deadline).Sub(h.srv.now()); wait > 0 {
			h.clock.Advance(wait)
		}
		now := h.srv.now()
		for _, sess := range r.order {
			sess.lastSeen = now
		}
		h.srv.scheduleAI(r)
		h.srv.mu.Unlock()
	}
	h.t.Fatal("the quiz never got there")
}

func (h *harness) quizTo(code, phase string) {
	h.t.Helper()
	h.quizRun(code, func(g *quiz.Game) bool { return g.Phase == phase })
}

// quizCell is an open cell of the current board, a gold square or not.
func quizCell(g *quiz.Game, gold bool) (category, row int) {
	for c, cat := range g.Boards[g.Round-1].Categories {
		for r, cell := range cat.Cells {
			if !cell.Played && cell.Gold == gold {
				return c, r
			}
		}
	}
	return -1, -1
}

// Game type plumbing: created from a phone or a PC table; the games list and
// summary say "quiz"; computer levels Easy, Normal and Hard, played by the
// engine; phones only (no keyboard or controller seats); joining is for the
// lobby; Estate-only routes refuse.
func TestQuizGameTypes(t *testing.T) {
	h := newHarness(t, withQuiz(t, 1))
	ann := h.device("Ann", "rocket", "198.51.100.151")
	res := h.post("/api/games", map[string]any{"name": "Ann", "gameType": "quiz"}, ann.opt())
	expectStatus(t, res, http.StatusCreated)
	st := res.body["state"].(map[string]any)
	code, token, annID := res.str("code"), res.str("token"), res.str("playerId")
	if st["game"] != "quiz" || st["gameType"] != "quiz" || st["viewerRole"] != "player" || dig(st, "you", "id") != annID || st["hostId"] != annID {
		t.Fatalf("host's quiz lobby: %v %v %v %v", st["game"], st["gameType"], st["viewerRole"], st["you"])
	}
	if got := strings.Join(strs(st["aiLevels"]), ","); got != "easy,normal,hard" {
		t.Fatalf("aiLevels %q", got)
	}
	if dig(st, "settings", "length") != "quick" || dig(st, "settings", "answerSeconds") != float64(8) || st["seatLimit"] != float64(8) || dig(st, "you", "latencyMs") != float64(quiz.UnknownLatency) {
		t.Fatalf("settings %v seatLimit %v you %v", st["settings"], st["seatLimit"], st["you"])
	}
	p := playerByID(st, annID)
	if autopilot, has := p["autopilot"]; p["input"] != "phone" || p["tokenKind"] != "rocket" || p["online"] != true || p["color"] != "#1EA0FF" || !has || autopilot != false || p["status"] != "waiting" {
		t.Fatalf("host player %v", p)
	}
	list := h.get("/api/games", ann.opt())
	if dig(list.body, "games", "0", "gameType") != "quiz" || dig(list.body, "games", "0", "rules", "length") != "quick" {
		t.Fatalf("games list %s", list.raw)
	}
	sum := h.get("/api/rooms/" + code + "/summary")
	if sum.str("gameType") != "quiz" || dig(sum.body, "openSeats") != float64(7) {
		t.Fatalf("summary %s", sum.raw)
	}

	// Computers: the three levels; the engine plays them (no server bot).
	expectError(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": "grandmaster"}), http.StatusBadRequest, "Choose easy, normal or hard")
	ai := h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": "hard"})
	expectStatus(t, ai, http.StatusOK)
	var aiID string
	for _, p := range players(ai.body) {
		if p["kind"] == "ai" {
			aiID = p["id"].(string)
			if p["difficulty"] != "hard" || p["input"] != "ai" || p["online"] != true || p["autopilot"] != false {
				t.Fatalf("computer seat %v", p)
			}
		}
	}
	h.srv.mu.Lock()
	m := h.srv.rooms[code].meta[aiID]
	noBot := m != nil && m.bot == nil && m.pokerBot == nil && m.ludoBot == nil
	level := quizOf(h.srv.rooms[code]).g.Player(aiID).Level
	h.srv.mu.Unlock()
	if !noBot || level != quiz.LevelHard {
		t.Fatalf("quiz computers are the engine's (bot set: %v, level %q)", !noBot, level)
	}
	expectError(t, h.post("/api/rooms/"+code+"/trade-quote", map[string]any{"token": token, "targetId": aiID}), http.StatusBadRequest, msgPokerNoQuotes)

	// A PC table: it hosts until a person is seated, and seats are phones only.
	tb := h.post("/api/tables", map[string]any{"gameType": "quiz"})
	expectStatus(t, tb, http.StatusCreated)
	tbl := table{tb.str("code"), tb.str("tableToken")}
	if dig(tb.body, "state", "game") != "quiz" || dig(tb.body, "state", "viewerRole") != "table" || dig(tb.body, "state", "you") != nil {
		t.Fatalf("quiz table %s", tb.raw)
	}
	expectError(t, h.addSeat(tbl, "keyboard", "Kim", "key", nil), http.StatusBadRequest, msgQuizPhones)
	expectError(t, h.addSeat(tbl, "controller", "Pat", "anchor", 0), http.StatusBadRequest, msgQuizPhones)
	for _, level := range []string{"easy", "normal"} {
		expectStatus(t, h.post("/api/rooms/"+tbl.code+"/ai", map[string]any{"token": tbl.token, "difficulty": level}), http.StatusOK)
	}
	expectError(t, h.act(tbl.code, tbl.token, map[string]any{"type": "pick", "category": 0, "row": 0}), http.StatusBadRequest, msgTableNoSeat)
	expectStatus(t, h.act(tbl.code, tbl.token, map[string]any{"type": "settings", "settings": map[string]any{"length": "full", "readSpeed": "fast"}}), http.StatusOK)
	started := h.act(tbl.code, tbl.token, map[string]any{"type": "start"})
	expectStatus(t, started, http.StatusOK)
	if dig(started.body, "status") != "playing" || dig(started.body, "phase") != "intro" || dig(started.body, "settings", "length") != "full" || dig(started.body, "rounds") != float64(2) {
		t.Fatalf("table start: %s", started.raw)
	}
	// Joining is for the lobby only.
	late := h.post("/api/rooms/"+tbl.code+"/join", map[string]any{"name": "Lee"}, h.device("Lee", "teapot", "198.51.100.152").opt())
	expectError(t, late, http.StatusBadRequest, msgAlreadyStarted)

	// Without the quiz configured the type is unknown.
	h2 := newHarness(t)
	d2 := h2.device("Dee", "key", "198.51.100.153")
	expectError(t, h2.post("/api/games", map[string]any{"name": "Dee", "gameType": "quiz"}, d2.opt()), http.StatusBadRequest, msgUnknownGameType)
}

// A clue played over both transports: Bob over his room socket (with clock
// sync and his latency), Ann over HTTP. The fair buzzer judges by the time
// each person took: Ann's faster wrong tap loses the value, Bob wins it.
func TestQuizPlayOverHTTPAndSocket(t *testing.T) {
	h := newHarness(t, withQuiz(t, 2))
	ann := h.quizGame("Ann", h.device("Ann", "rocket", "198.51.100.161"))
	bob := h.quizJoin(ann.code, "Bob", h.device("Bob", "teapot", "198.51.100.162"))
	code := ann.code
	tokens := map[string]string{ann.playerID: ann.token, bob.playerID: bob.token}
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "start"}), http.StatusOK)

	sock := h.dial("/ws/rooms/"+code+"?token="+bob.token, http.Header{"X-Forwarded-For": {"198.51.100.162"}})
	sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "state" })
	sock.send(map[string]any{"type": "ping", "t": 1000, "rtt": 40})
	msgs := sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "pong" })
	if pong := msgs[len(msgs)-1]; pong["t"] != float64(1000) || pong["now"] == nil {
		t.Fatalf("pong %v", pong)
	}

	h.quizTo(code, quiz.PhasePick)
	var chooser string
	var cat, row int
	h.withQuizGame(code, func(g *quiz.Game) {
		chooser = g.ChooserID
		cat, row = quizCell(g, false)
	})
	other := ann.playerID
	if chooser == ann.playerID {
		other = bob.playerID
	}
	expectError(t, h.act(code, tokens[other], map[string]any{"type": "pick", "category": cat, "row": row}), http.StatusBadRequest, "it is not your pick")
	expectStatus(t, h.act(code, tokens[chooser], map[string]any{"type": "pick", "category": cat, "row": row}), http.StatusOK)
	h.quizTo(code, quiz.PhaseAnswer)
	var seq, right, value int
	var openAt int64
	h.withQuizGame(code, func(g *quiz.Game) {
		seq, right, value, openAt = g.Clue.Seq, g.Clue.Q.Answer, g.Clue.Value, g.Clue.Q.OpenAt
	})
	wrong := (right + 1) % 4
	// Bob's 900 ms tap reaches the server 920 ms after the choices opened
	// (half his 40 ms round trip later); a tap never counts as later than it
	// arrived.
	h.clock.Advance(time.UnixMilli(openAt + 920).Sub(h.srv.now()))

	// Bob answers over his socket; "_" fields are not the client's to send.
	action := map[string]any{"type": "answer", "clue": seq, "choice": right, "ms": 900, "_arrival": 1}
	sock.send(map[string]any{"type": "action", "action": action, "id": 7})
	msgs = sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "actionResult" })
	if result := msgs[len(msgs)-1]; result["id"] != float64(7) || result["ok"] != true {
		t.Fatalf("Bob's answer: %v", result)
	}
	mine := msgs[len(msgs)-2]
	if mine["type"] != "state" || dig(mine, "state", "you", "choice") != float64(right) || dig(mine, "state", "you", "latencyMs") != float64(40) {
		t.Fatalf("Bob's state after his tap: %v", dig(mine, "state", "you"))
	}
	sock.send(map[string]any{"type": "action", "action": action, "id": 8})
	msgs = sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "actionResult" })
	if result := msgs[len(msgs)-1]; result["id"] != float64(8) || result["ok"] != false || result["error"] != "you have already answered" {
		t.Fatalf("Bob's second answer: %v", result)
	}

	// Ann has no socket: her latency is unknown (250 ms). Her wrong tap took
	// her 600 ms, faster than Bob's right one though it arrived after his, so
	// it costs her the value.
	res := h.act(code, ann.token, map[string]any{"type": "answer", "clue": seq, "choice": wrong, "ms": 600})
	expectStatus(t, res, http.StatusOK)
	if dig(res.body, "you", "latencyMs") != float64(quiz.UnknownLatency) || dig(res.body, "you", "choice") != float64(wrong) {
		t.Fatalf("Ann's state: %v", dig(res.body, "you"))
	}
	if dig(res.body, "clue", "answerIndex") != nil || strings.Contains(string(res.raw), fmt.Sprintf(`"choice":%d,"correct"`, right)) {
		t.Fatal("the answer is out before the reveal")
	}
	h.quizTo(code, quiz.PhaseReveal)
	st := h.state(code, ann.token)
	results, _ := dig(st, "clue", "results").([]any)
	if dig(st, "clue", "winnerId") != bob.playerID || dig(st, "clue", "answerIndex") != float64(right) || len(results) != 2 {
		t.Fatalf("reveal: %v", st["clue"])
	}
	first, second := results[0].(map[string]any), results[1].(map[string]any)
	if first["playerId"] != ann.playerID || first["ms"] != float64(600) || first["delta"] != float64(-value) ||
		second["playerId"] != bob.playerID || second["ms"] != float64(900) || second["delta"] != float64(value) {
		t.Fatalf("results in time order: %v", results)
	}
	if playerByID(st, bob.playerID)["score"] != float64(value) || playerByID(st, ann.playerID)["score"] != float64(-value) {
		t.Fatalf("scores: %v", st["players"])
	}
	if st["chooserId"] != bob.playerID {
		t.Fatal("the winner takes control")
	}
}

// The room's timer wakes the engine when its next step is due, with nobody
// sending anything: the intro ends into the first pick on its own.
func TestQuizTimerWakesTheGame(t *testing.T) {
	h := newHarness(t, withQuiz(t, 3))
	ann := h.quizGame("Ann", h.device("Ann", "rocket", "198.51.100.171"))
	expectStatus(t, h.post("/api/rooms/"+ann.code+"/ai", map[string]any{"token": ann.token, "difficulty": "easy"}), http.StatusOK)
	rec := h.record(ann.code, ann.token)
	expectStatus(t, h.act(ann.code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	var introEnds int64
	h.withQuizGame(ann.code, func(g *quiz.Game) { introEnds = g.PhaseEndsAt })
	h.srv.mu.Lock()
	armed := h.srv.rooms[ann.code].timer != nil
	h.srv.mu.Unlock()
	if !armed {
		t.Fatal("no timer for the intro")
	}
	h.clock.Advance(time.UnixMilli(introEnds).Sub(h.srv.now()))
	waitFor(t, "the first pick", 2*time.Second, func() bool {
		st := rec.state()
		return st != nil && st["phase"] == quiz.PhasePick
	})
}

// END GAME places by the scores so far. PLAY AGAIN keeps the players (the
// computer at its level), the host and the rules, and deals categories this
// room has not played; the room's memory survives a restart.
func TestQuizEndGameAndRematch(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, withQuiz(t, 4), func(c *Config) { c.DataDir = dir; c.SaveDelay = 10 * time.Millisecond })
	ann := h.quizGame("Ann", h.device("Ann", "rocket", "198.51.100.181"))
	bob := h.quizJoin(ann.code, "Bob", h.device("Bob", "teapot", "198.51.100.182"))
	code := ann.code
	expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": ann.token, "difficulty": "easy"}), http.StatusOK)
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "settings", "settings": map[string]any{"readSpeed": "slow", "answerSeconds": 12}}), http.StatusOK)
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	firstBoard := quizBoardIDs(h, code)
	h.quizTo(code, quiz.PhasePick)
	h.withQuizGame(code, func(g *quiz.Game) { g.Player(bob.playerID).Score = 300 })
	expectError(t, h.post("/api/rooms/"+code+"/rematch", map[string]any{"token": ann.token}), http.StatusBadRequest, "The game is not finished yet")
	expectError(t, h.post("/api/rooms/"+code+"/end", map[string]any{"token": bob.token}), http.StatusBadRequest, msgOnlyHost)
	end := h.post("/api/rooms/"+code+"/end", map[string]any{"token": ann.token})
	expectStatus(t, end, http.StatusOK)
	if end.body["status"] != "finished" || dig(end.body, "winnerIds", "0") != bob.playerID || dig(end.body, "standings", "0", "id") != bob.playerID {
		t.Fatalf("end game: %v %v", end.body["winnerIds"], end.body["standings"])
	}
	before := end.body["version"].(float64)

	again := h.post("/api/rooms/"+code+"/rematch", map[string]any{"token": ann.token})
	expectStatus(t, again, http.StatusOK)
	st := again.body
	ps := players(st)
	if st["status"] != "lobby" || st["version"].(float64) <= before || len(ps) != 3 || ps[0]["name"] != "Ann" || ps[1]["name"] != "Bob" || ps[2]["kind"] != "ai" || ps[2]["difficulty"] != "easy" {
		t.Fatalf("rematch lobby: %v %v %v", st["status"], st["version"], ps)
	}
	if st["hostId"] != dig(st, "you", "id") || dig(st, "settings", "readSpeed") != "slow" || dig(st, "settings", "answerSeconds") != float64(12) || ps[0]["score"] != float64(0) {
		t.Fatalf("rematch keeps host and rules: host %v you %v settings %v", st["hostId"], st["you"], st["settings"])
	}
	if bobState := h.state(code, bob.token); dig(bobState, "you", "id") != ps[1]["id"] {
		t.Fatal("Bob's session follows him to his new seat")
	}
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	h2 := newHarness(t, withQuiz(t, 4), func(c *Config) { c.DataDir = dir })
	expectStatus(t, h2.act(code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	secondBoard := quizBoardIDs(h2, code)
	for _, id := range secondBoard {
		if slices.Contains(firstBoard, id) {
			t.Fatalf("category %s dealt again: %v then %v", id, firstBoard, secondBoard)
		}
	}
}

// quizBoardIDs are the category ids of a quiz room's first board.
func quizBoardIDs(h *harness, code string) (ids []string) {
	h.withQuizGame(code, func(g *quiz.Game) {
		for _, c := range g.Boards[0].Categories {
			ids = append(ids, c.ID)
		}
	})
	return ids
}

// A restart mid-clue brings the game back exactly (the clue, its shuffled
// choices, the scores) without replaying old events, and play goes on.
func TestQuizRestartMidGame(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, withQuiz(t, 5), func(c *Config) { c.DataDir = dir; c.SaveDelay = 10 * time.Millisecond })
	ann := h.quizGame("Ann", h.device("Ann", "rocket", "198.51.100.191"))
	bob := h.quizJoin(ann.code, "Bob", h.device("Bob", "teapot", "198.51.100.192"))
	code := ann.code
	tokens := map[string]string{ann.playerID: ann.token, bob.playerID: bob.token}
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	h.quizTo(code, quiz.PhasePick)
	var chooser string
	var cat, row int
	h.withQuizGame(code, func(g *quiz.Game) {
		chooser = g.ChooserID
		cat, row = quizCell(g, false)
	})
	expectStatus(t, h.act(code, tokens[chooser], map[string]any{"type": "pick", "category": cat, "row": row}), http.StatusOK)
	h.quizTo(code, quiz.PhaseRead)
	before := h.state(code, ann.token)
	var savedSeq int64
	h.srv.mu.Lock()
	savedSeq = h.srv.rooms[code].eventSeq
	h.srv.mu.Unlock()
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(dir, roomsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file), `"gameType":"quiz"`) || !strings.Contains(string(file), fmt.Sprintf(`"eventSeq":%d`, savedSeq)) || !strings.Contains(string(file), `"played":[`) {
		t.Fatal("rooms.json lacks the game type, the event sequence or the played categories")
	}
	h2 := newHarness(t, withQuiz(t, 99), func(c *Config) { c.DataDir = dir }) // another seed: the saved stream wins
	after := h2.state(code, ann.token)
	for _, key := range []string{"game", "status", "phase", "round", "board", "chooserId", "clue"} {
		if fmt.Sprint(before[key]) != fmt.Sprint(after[key]) {
			t.Fatalf("%s changed across the restart: %v → %v", key, before[key], after[key])
		}
	}
	rec := h2.record(code, ann.token)
	waitFor(t, "the first state", 2*time.Second, func() bool { return rec.state() != nil })
	h2.quizTo(code, quiz.PhaseAnswer)
	var seq, right int
	h2.withQuizGame(code, func(g *quiz.Game) { seq, right = g.Clue.Seq, g.Clue.Q.Answer })
	expectStatus(t, h2.act(code, bob.token, map[string]any{"type": "answer", "clue": seq, "choice": right, "ms": 1500}), http.StatusOK)
	h2.quizTo(code, quiz.PhaseReveal)
	if st := h2.state(code, ann.token); dig(st, "clue", "winnerId") != bob.playerID {
		t.Fatalf("the restored clue plays on: %v", st["clue"])
	}
	waitFor(t, "the reveal on the socket", 2*time.Second, func() bool {
		st := rec.state()
		return st != nil && st["phase"] == quiz.PhaseReveal
	})
	events := rec.events()
	if len(events) == 0 {
		t.Fatal("no events after the restart")
	}
	for _, ev := range events {
		if int64(ev["seq"].(float64)) <= savedSeq {
			t.Fatalf("event %v replayed after the restart", ev)
		}
	}
}

// A quiz game is recorded as its event stream: every judged clue's reveal
// event carries each tap's timing (reported ms, arrival, latency, effective)
// for tuning the computers and the clue tiers later.
func TestQuizRecorded(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, withQuiz(t, 6), func(c *Config) { c.DataDir = dir; c.SaveDelay = 10 * time.Millisecond })
	ann := h.quizGame("Ann", h.device("Ann", "rocket", "198.51.100.201"))
	code := ann.code
	for _, level := range []string{"normal", "hard"} {
		expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": ann.token, "difficulty": level}), http.StatusOK)
	}
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	// Ann taps on the first clue she may answer, the moment it opens (so 40 ms:
	// a tap counts as at most the time since the opening, plus 50 ms).
	for tapped := false; !tapped; {
		h.quizTo(code, quiz.PhaseAnswer)
		var seq, right int
		var eligible bool
		h.withQuizGame(code, func(g *quiz.Game) {
			seq, right, eligible = g.Clue.Seq, g.Clue.Q.Answer, slices.Contains(g.Clue.Eligible, ann.playerID)
		})
		if eligible {
			expectStatus(t, h.act(code, ann.token, map[string]any{"type": "answer", "clue": seq, "choice": right, "ms": 40}), http.StatusOK)
			tapped = true
		}
		h.quizRun(code, func(g *quiz.Game) bool { return g.Phase != quiz.PhaseAnswer })
	}
	h.quizRun(code, func(g *quiz.Game) bool { return g.Status == quiz.StatusFinished })
	names := finishedRecords(t, h, dir)
	if len(names) != 1 {
		t.Fatalf("records %v", names)
	}
	rec := readTableRecord(t, dir, names[0])
	if rec.GameType != "quiz" || rec.Outcome.Reason != gamerecord.EndFinished || rec.Outcome.WinnerID == "" || len(rec.Players) != 3 {
		t.Fatalf("%s %+v players %d", rec.GameType, rec.Outcome, len(rec.Players))
	}
	var reveals, annTaps, finals int
	for _, e := range rec.Events {
		var ev quiz.Event
		if err := json.Unmarshal(e.Event, &ev); err != nil {
			t.Fatal(err)
		}
		switch ev.Type {
		case quiz.EvReveal:
			reveals++
			for _, tap := range ev.Taps {
				if tap.PlayerID == ann.playerID {
					annTaps++
					if tap.Ms == nil || *tap.Ms != 40 || tap.Latency != quiz.UnknownLatency || tap.Effective != 40 || !tap.Correct {
						t.Fatalf("Ann's tap as recorded: %+v", tap)
					}
				} else if tap.Latency != 0 || tap.Ms == nil || tap.Effective != *tap.Ms {
					t.Fatalf("a computer's tap as recorded: %+v", tap)
				}
			}
		case quiz.EvFinalReveal:
			finals++
		}
	}
	if reveals != 30 || annTaps != 1 || finals != 3 {
		t.Fatalf("%d reveals, %d taps by Ann, %d final reveals", reveals, annTaps, finals)
	}
}

// A new table from the same screen (NEW TABLE, or switching to another game
// and back) deals categories that screen has not shown yet: the memory lives
// with the screen's device, not only with the room, and survives a restart.
func TestQuizNewTableRemembersTheScreen(t *testing.T) {
	dir := t.TempDir()
	opts := func(c *Config) { c.DataDir = dir; c.SaveDelay = 10 * time.Millisecond }
	h := newHarness(t, withQuiz(t, 4), opts)
	tv := h.device("TV", "rocket", "198.51.100.191")
	kim := h.device("Kim", "teapot", "198.51.100.192")
	play := func(h *harness) []string {
		t.Helper()
		tb := h.post("/api/tables", map[string]any{"gameType": "quiz"}, tv.opt())
		expectStatus(t, tb, http.StatusCreated)
		code, token := tb.str("code"), tb.str("tableToken")
		expectStatus(t, h.post("/api/rooms/"+code+"/join", map[string]any{"name": "Kim"}, kim.opt()), http.StatusOK)
		expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": "easy"}), http.StatusOK)
		expectStatus(t, h.act(code, token, map[string]any{"type": "start"}), http.StatusOK)
		return quizBoardIDs(h, code)
	}
	first := play(h)
	second := play(h)
	for _, id := range second {
		if slices.Contains(first, id) {
			t.Fatalf("a new table on the same screen dealt %s again: %v then %v", id, first, second)
		}
	}
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	h2 := newHarness(t, withQuiz(t, 4), opts)
	third := play(h2)
	for _, id := range third {
		if slices.Contains(second, id) {
			t.Fatalf("after a restart the screen's last board came back (%s): %v then %v", id, second, third)
		}
	}
}
