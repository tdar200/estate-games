package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/closest"
	"github.com/tdar200/estate-games/internal/gamerecord"
)

// closestTestLibrary is the engine's made-up test packs (24 questions):
// server tests never depend on the real packs.
var closestTestLibrary = sync.OnceValues(func() (*closest.Library, error) {
	return closest.LoadLibrary(os.DirFS(filepath.Join("..", "closest", "testdata", "packs")))
})

// withClosest adds the closest game type with a seeded deal and seeded
// computers, the test packs, and the room timer's sleeps capped at 5 ms so
// the room follows the harness clock closely.
func withClosest(t *testing.T, seed byte) harnessOption {
	t.Helper()
	lib, err := closestTestLibrary()
	if err != nil {
		t.Fatal(err)
	}
	return func(c *Config) {
		s := [32]byte{seed, 31: 0xC1}
		if c.Engines == nil {
			c.Engines = map[string]Engine{}
		}
		c.Engines[GameClosest] = ClosestEngine{Seed: &s, Library: lib}
		c.pokerTick = 5 * time.Millisecond
	}
}

func (h *harness) closestGame(name string, d device) seat {
	h.t.Helper()
	r := h.post("/api/games", map[string]any{"name": name, "gameType": "closest"}, d.opt())
	expectStatus(h.t, r, http.StatusCreated)
	return seat{r.str("code"), r.str("token"), r.str("playerId")}
}

// withClosestGame runs fn on a closest room's game under the server lock.
func (h *harness) withClosestGame(code string, fn func(g *closest.Game)) {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	fn(closestOf(h.srv.rooms[code]).g)
}

// closestRun moves the harness clock from deadline to deadline, running the
// room's timer at each, until done holds. Every phone counts as connected
// (it would be polling) except the away players', so the room never pauses
// for being empty and the engine hears who is away.
func (h *harness) closestRun(code string, done func(g *closest.Game) bool, away ...string) {
	h.t.Helper()
	for range 20000 {
		h.srv.mu.Lock()
		r := h.srv.rooms[code]
		g := closestOf(r).g
		if done(g) {
			h.srv.mu.Unlock()
			return
		}
		deadline, phase := g.NextDeadline(), g.Phase
		if deadline == 0 {
			h.srv.mu.Unlock()
			h.t.Fatalf("the game waits on no timer in phase %s", phase)
		}
		if wait := time.UnixMilli(deadline).Sub(h.srv.now()); wait > 0 {
			h.clock.Advance(wait)
		}
		now := h.srv.now()
		for _, sess := range r.order {
			if !slices.Contains(away, sess.playerID) {
				sess.lastSeen = now
			}
		}
		h.srv.scheduleAI(r)
		h.srv.mu.Unlock()
	}
	h.t.Fatal("the game never got there")
}

func (h *harness) closestTo(code, phase string, away ...string) {
	h.t.Helper()
	h.closestRun(code, func(g *closest.Game) bool { return g.Phase == phase }, away...)
}

// closestShown formats n as the question shows it.
func closestShown(n int, format string) string {
	if format == closest.FormatYear {
		return strconv.Itoa(n)
	}
	return thousands(n)
}

// Game type plumbing: created from a phone or a PC table; the games list and
// summary say "closest"; computer levels Easy, Normal and Hard, played by
// the engine; phones only; the table may start and change the rules; fill
// seats Normal computers at the start, with tokens; joining is for the
// lobby; with no question packs the start says why.
func TestClosestGameTypes(t *testing.T) {
	h := newHarness(t, withClosest(t, 1))
	ann := h.device("Ann", "rocket", "198.51.100.231")
	res := h.post("/api/games", map[string]any{"name": "Ann", "gameType": "closest"}, ann.opt())
	expectStatus(t, res, http.StatusCreated)
	st := res.body["state"].(map[string]any)
	code, token, annID := res.str("code"), res.str("token"), res.str("playerId")
	if st["game"] != "closest" || st["gameType"] != "closest" || st["title"] != closest.Title || st["viewerRole"] != "player" || dig(st, "you", "id") != annID || st["hostId"] != annID {
		t.Fatalf("host's lobby: %v %v %v %v %v", st["game"], st["gameType"], st["title"], st["viewerRole"], st["you"])
	}
	if got := strings.Join(strs(st["aiLevels"]), ","); got != "easy,normal,hard" {
		t.Fatalf("aiLevels %q", got)
	}
	if dig(st, "settings", "length") != "quick" || dig(st, "settings", "guessSeconds") != float64(30) || dig(st, "settings", "betSeconds") != float64(25) ||
		dig(st, "settings", "fill") != true || st["seatLimit"] != float64(8) || st["questionCount"] != float64(7) || st["question"] != nil {
		t.Fatalf("settings %v seatLimit %v questionCount %v question %v", st["settings"], st["seatLimit"], st["questionCount"], st["question"])
	}
	p := playerByID(st, annID)
	if autopilot, has := p["autopilot"]; p["input"] != "phone" || p["tokenKind"] != "rocket" || p["online"] != true || p["color"] != "#1EA0FF" || !has || autopilot != false || p["status"] != "waiting" {
		t.Fatalf("host player %v", p)
	}
	list := h.get("/api/games", ann.opt())
	if dig(list.body, "games", "0", "gameType") != "closest" || dig(list.body, "games", "0", "rules", "length") != "quick" {
		t.Fatalf("games list %s", list.raw)
	}
	sum := h.get("/api/rooms/" + code + "/summary")
	if sum.str("gameType") != "closest" || dig(sum.body, "openSeats") != float64(7) {
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
			if p["difficulty"] != "hard" || p["input"] != "ai" || p["online"] != true || p["autopilot"] != false || p["name"] != "Alpha" {
				t.Fatalf("computer seat %v", p)
			}
		}
	}
	h.srv.mu.Lock()
	m := h.srv.rooms[code].meta[aiID]
	noBot := m != nil && m.bot == nil && m.pokerBot == nil && m.ludoBot == nil
	level := closestOf(h.srv.rooms[code]).g.Player(aiID).Level
	h.srv.mu.Unlock()
	if !noBot || level != closest.LevelHard {
		t.Fatalf("closest computers are the engine's (bot set: %v, level %q)", !noBot, level)
	}
	expectError(t, h.post("/api/rooms/"+code+"/trade-quote", map[string]any{"token": token, "targetId": aiID}), http.StatusBadRequest, msgPokerNoQuotes)
	expectError(t, h.act(code, token, map[string]any{"type": "settings", "settings": map[string]any{"guessSeconds": 25}}), http.StatusBadRequest, "guessSeconds must be 20, 30 or 45")

	// A PC table: it hosts until a person is seated, and seats are phones only.
	tb := h.post("/api/tables", map[string]any{"gameType": "closest"})
	expectStatus(t, tb, http.StatusCreated)
	tbl := table{tb.str("code"), tb.str("tableToken")}
	if dig(tb.body, "state", "game") != "closest" || dig(tb.body, "state", "viewerRole") != "table" || dig(tb.body, "state", "you") != nil {
		t.Fatalf("closest table %s", tb.raw)
	}
	expectError(t, h.addSeat(tbl, "keyboard", "Kim", "key", nil), http.StatusBadRequest, msgClosestPhones)
	expectError(t, h.addSeat(tbl, "controller", "Pat", "anchor", 0), http.StatusBadRequest, msgClosestPhones)
	if msgClosestPhones != "Near Thing needs a phone per player" {
		t.Fatalf("phones message %q", msgClosestPhones)
	}
	kim := h.post("/api/rooms/"+tbl.code+"/join", map[string]any{"name": "Kim"}, h.device("Kim", "teapot", "198.51.100.232").opt())
	expectStatus(t, kim, http.StatusOK)
	expectStatus(t, h.post("/api/rooms/"+tbl.code+"/ai", map[string]any{"token": tbl.token, "difficulty": "easy"}), http.StatusOK)
	expectError(t, h.act(tbl.code, tbl.token, map[string]any{"type": "guess", "q": 1, "value": 10}), http.StatusBadRequest, msgTableNoSeat)
	expectStatus(t, h.act(tbl.code, tbl.token, map[string]any{"type": "settings", "settings": map[string]any{"length": "full", "guessSeconds": 45}}), http.StatusOK)
	started := h.act(tbl.code, tbl.token, map[string]any{"type": "start"})
	expectStatus(t, started, http.StatusOK)
	if dig(started.body, "status") != "playing" || dig(started.body, "phase") != closest.PhaseIntro || dig(started.body, "settings", "length") != "full" || dig(started.body, "questionCount") != float64(12) {
		t.Fatalf("table start: %s", started.raw)
	}
	// Fill: two Normal computers joined so there are four players, with
	// tokens of their own and the names a computer seat gets.
	ps := players(started.body)
	tokens := map[string]bool{}
	for _, p := range ps {
		tokens[p["tokenKind"].(string)] = true
	}
	if len(ps) != 4 || len(tokens) != 4 || tokens[""] {
		t.Fatalf("players after the fill: %v", ps)
	}
	for i, name := range []string{"Orca", "Redwood"} {
		p := ps[2+i]
		if p["name"] != name || p["kind"] != "ai" || p["difficulty"] != "normal" || p["input"] != "ai" || p["color"] == nil || p["autopilot"] != false {
			t.Fatalf("filled computer %d: %v", i, p)
		}
	}
	// Joining is for the lobby only.
	late := h.post("/api/rooms/"+tbl.code+"/join", map[string]any{"name": "Lee"}, h.device("Lee", "bicycle", "198.51.100.233").opt())
	expectError(t, late, http.StatusBadRequest, msgAlreadyStarted)

	// Without the game type configured it is unknown; with no packs (the
	// binary's, before any are written) the start says why.
	h2 := newHarness(t)
	d2 := h2.device("Dee", "key", "198.51.100.234")
	expectError(t, h2.post("/api/games", map[string]any{"name": "Dee", "gameType": "closest"}, d2.opt()), http.StatusBadRequest, msgUnknownGameType)
	h3 := newHarness(t, func(c *Config) {
		c.Engines = map[string]Engine{GameClosest: ClosestEngine{Library: &closest.Library{}}}
	})
	d3 := h3.device("Eve", "key", "198.51.100.235")
	eve := h3.closestGame("Eve", d3)
	expectError(t, h3.act(eve.code, eve.token, map[string]any{"type": "start"}), http.StatusBadRequest, "there are no questions to ask yet")
}

// A question played over both transports: Bob over his room socket, Ann
// over HTTP. Nobody sees another's guess before the line-up or another's
// chips before they are shown; the reveal pays as §4.1 says.
func TestClosestPlayOverHTTPAndSocket(t *testing.T) {
	h := newHarness(t, withClosest(t, 2))
	ann := h.closestGame("Ann", h.device("Ann", "rocket", "198.51.100.241"))
	bob := h.join(ann.code, "Bob", h.device("Bob", "teapot", "198.51.100.242").opt())
	code := ann.code
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "settings", "settings": map[string]any{"fill": false}}), http.StatusOK)
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "start"}), http.StatusOK)

	sock := h.dial("/ws/rooms/"+code+"?token="+bob.token, http.Header{"X-Forwarded-For": {"198.51.100.242"}})
	sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "state" })
	h.closestTo(code, closest.PhaseGuess)
	var seq, answer, lo, hi int
	var format string
	h.withClosestGame(code, func(g *closest.Game) {
		q := g.Cur.Question
		seq, answer, lo, hi, format = g.Cur.Seq, q.Answer, q.Min, q.Max, q.Format
	})
	annGuess, bobGuess := answer-1, answer+(hi-answer)/2

	sock.send(map[string]any{"type": "action", "action": map[string]any{"type": "guess", "q": seq, "value": bobGuess}, "id": 7})
	msgs := sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "actionResult" })
	if result := msgs[len(msgs)-1]; result["id"] != float64(7) || result["ok"] != true {
		t.Fatalf("Bob's guess: %v", result)
	}
	if mine := msgs[len(msgs)-2]; mine["type"] != "state" || dig(mine, "state", "you", "guess") != float64(bobGuess) || dig(mine, "state", "you", "changesLeft") != float64(1) {
		t.Fatalf("Bob's state after his guess: %v", dig(mine, "state", "you"))
	}
	st := h.state(code, ann.token)
	if dig(st, "question", "slots") != nil || dig(st, "you", "guess") != nil || playerByID(st, bob.playerID)["guessed"] != true || dig(st, "question", "answer") != nil {
		t.Fatalf("Ann sees too much, or too little: %v %v", st["question"], st["you"])
	}
	expectError(t, h.act(code, ann.token, map[string]any{"type": "guess", "q": seq, "value": hi + 1}), http.StatusBadRequest,
		"guess between "+closestShown(lo, format)+" and "+closestShown(hi, format))
	expectError(t, h.act(code, ann.token, map[string]any{"type": "guess", "q": seq - 1, "value": annGuess}), http.StatusBadRequest, "that question is over")
	res := h.act(code, ann.token, map[string]any{"type": "guess", "q": seq, "value": annGuess})
	expectStatus(t, res, http.StatusOK)
	slots, _ := dig(res.body, "question", "slots").([]any)
	if res.body["phase"] != closest.PhaseLineup || len(slots) != 2 || dig(res.body, "question", "slots", "0", "value") != float64(annGuess) ||
		dig(res.body, "question", "slots", "1", "ownerIds", "0") != bob.playerID || dig(res.body, "question", "slots", "0", "odds") != float64(2) {
		t.Fatalf("everyone guessed, so the line-up: %v %v", res.body["phase"], res.body["question"])
	}

	h.closestTo(code, closest.PhaseBet)
	sock.send(map[string]any{"type": "action", "action": map[string]any{"type": "bet", "q": seq, "chips": []int{0, 0}, "stake": 0}, "id": 8})
	msgs = sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "actionResult" })
	if result := msgs[len(msgs)-1]; result["id"] != float64(8) || result["ok"] != true {
		t.Fatalf("Bob's bet: %v", result)
	}
	sock.send(map[string]any{"type": "action", "action": map[string]any{"type": "bet", "q": seq, "chips": []int{1, 1}}, "id": 9})
	msgs = sock.until(2*time.Second, func(m map[string]any) bool { return m["type"] == "actionResult" })
	if result := msgs[len(msgs)-1]; result["ok"] != false || result["error"] != "you have already bet" {
		t.Fatalf("Bob's second bet: %v", result)
	}
	st = h.state(code, ann.token)
	if dig(st, "question", "betsIn", "0") != bob.playerID || dig(st, "question", "slots", "0", "chips") != nil {
		t.Fatalf("Ann sees Bob's chips before they are shown: %v", st["question"])
	}
	expectError(t, h.act(code, ann.token, map[string]any{"type": "bet", "q": seq, "chips": []int{0, 1}, "stake": 1, "stakeSlot": 0}), http.StatusBadRequest, "stake between 0 and 0")
	expectError(t, h.act(code, ann.token, map[string]any{"type": "bet", "q": seq, "chips": []int{0}}), http.StatusBadRequest, "pick two slots")
	res = h.act(code, ann.token, map[string]any{"type": "bet", "q": seq, "chips": []int{0, 1}, "stake": 0})
	expectStatus(t, res, http.StatusOK)
	// The chips, in the order they were bet: Bob's two, then Ann's one.
	if res.body["phase"] != closest.PhaseChips || dig(res.body, "question", "slots", "0", "chips", "0", "playerId") != bob.playerID ||
		dig(res.body, "question", "slots", "0", "chips", "0", "count") != float64(2) || dig(res.body, "question", "slots", "1", "chips", "0", "playerId") != ann.playerID {
		t.Fatalf("everyone bet, so the chips: %v %v", res.body["phase"], res.body["question"])
	}

	h.closestTo(code, closest.PhaseReveal)
	st = h.state(code, ann.token)
	results, _ := dig(st, "question", "results").([]any)
	if dig(st, "question", "answer") != float64(answer) || len(results) != 2 || dig(st, "question", "slots", "0", "result") != "win" || dig(st, "question", "slots", "1", "result") != "second" {
		t.Fatalf("reveal: %v", st["question"])
	}
	// Ann: owner of the winner +3, a chip on it 2, a chip on second 1. Bob:
	// owner of second +1, two chips on the winner 2 each.
	want := map[string]float64{ann.playerID: 6, bob.playerID: 5}
	for _, r := range results {
		r := r.(map[string]any)
		if r["delta"] != want[r["playerId"].(string)] {
			t.Fatalf("results %v", results)
		}
	}
	if playerByID(st, ann.playerID)["score"] != float64(6) || playerByID(st, bob.playerID)["score"] != float64(5) {
		t.Fatalf("scores: %v", st["players"])
	}
	// Bob's socket got the events, none with a value before the reveal,
	// and the reveal with every guess and bet.
	msgs = sock.until(2*time.Second, func(m map[string]any) bool {
		return m["type"] == "event" && dig(m, "event", "type") == closest.EvReveal
	})
	reveal := msgs[len(msgs)-1]
	if guesses, _ := dig(reveal, "event", "guesses").([]any); len(guesses) != 2 {
		t.Fatalf("reveal event %v", reveal)
	}
	if bets, _ := dig(reveal, "event", "bets").([]any); len(bets) != 2 {
		t.Fatalf("reveal event %v", reveal)
	}
	for _, m := range msgs[:len(msgs)-1] {
		if m["type"] == "event" && (dig(m, "event", "answer") != nil || dig(m, "event", "guesses") != nil || dig(m, "event", "bets") != nil) {
			t.Fatalf("an event gives something away before the reveal: %v", m)
		}
	}
}

// The room's timer wakes the engine when its next step is due, with nobody
// sending anything: the intro ends into the first question on its own.
func TestClosestTimerWakesTheGame(t *testing.T) {
	h := newHarness(t, withClosest(t, 3))
	ann := h.closestGame("Ann", h.device("Ann", "rocket", "198.51.100.251"))
	expectStatus(t, h.post("/api/rooms/"+ann.code+"/ai", map[string]any{"token": ann.token, "difficulty": "easy"}), http.StatusOK)
	rec := h.record(ann.code, ann.token)
	expectStatus(t, h.act(ann.code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	var introEnds int64
	h.withClosestGame(ann.code, func(g *closest.Game) { introEnds = g.PhaseEndsAt })
	h.srv.mu.Lock()
	armed := h.srv.rooms[ann.code].timer != nil
	h.srv.mu.Unlock()
	if !armed {
		t.Fatal("no timer for the intro")
	}
	h.clock.Advance(time.UnixMilli(introEnds).Sub(h.srv.now()))
	waitFor(t, "the first question", 2*time.Second, func() bool {
		st := rec.state()
		return st != nil && st["phase"] == closest.PhaseGuess && dig(st, "question", "text") != nil
	})
}

// When the bets close, a player who guessed but did not bet has both chips
// put on their own slot only if their phone is connected: the server tells
// the engine who is online.
func TestClosestBetTimeoutsFollowPresence(t *testing.T) {
	h := newHarness(t, withClosest(t, 4))
	ann := h.closestGame("Ann", h.device("Ann", "rocket", "198.51.100.161"))
	bob := h.join(ann.code, "Bob", h.device("Bob", "teapot", "198.51.100.162").opt())
	code := ann.code
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "settings", "settings": map[string]any{"fill": false}}), http.StatusOK)
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	h.closestTo(code, closest.PhaseGuess)
	var seq, answer, hi int
	h.withClosestGame(code, func(g *closest.Game) { seq, answer, hi = g.Cur.Seq, g.Cur.Question.Answer, g.Cur.Question.Max })
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "guess", "q": seq, "value": answer - 1}), http.StatusOK)
	expectStatus(t, h.act(code, bob.token, map[string]any{"type": "guess", "q": seq, "value": answer + (hi-answer)/2}), http.StatusOK)
	// Bob's phone goes to sleep; Ann's stays connected. Neither bets.
	h.closestTo(code, closest.PhaseChips, bob.playerID)
	st := h.state(code, ann.token)
	if playerByID(st, bob.playerID)["online"] != false {
		t.Fatal("Bob should be away")
	}
	chips, _ := dig(st, "question", "slots", "0", "chips").([]any)
	none, _ := dig(st, "question", "slots", "1", "chips").([]any)
	if len(chips) != 1 || dig(st, "question", "slots", "0", "chips", "0", "playerId") != ann.playerID || dig(st, "question", "slots", "0", "chips", "0", "count") != float64(2) ||
		none == nil || len(none) != 0 {
		t.Fatalf("chips after the bet timer: %v", st["question"])
	}
	if dig(st, "you", "chips", "0") != float64(0) || dig(st, "you", "chips", "1") != float64(0) || dig(st, "you", "stake") != float64(0) {
		t.Fatalf("Ann's own panel: %v", st["you"])
	}
}

// END GAME places by the scores so far. PLAY AGAIN keeps the players (the
// computers the fill added among them, at their level), the host and the
// rules, and never asks a question this room has asked; the room's memory
// survives a restart.
func TestClosestEndGameAndRematch(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, withClosest(t, 5), func(c *Config) { c.DataDir = dir; c.SaveDelay = 10 * time.Millisecond })
	ann := h.closestGame("Ann", h.device("Ann", "rocket", "198.51.100.171"))
	bob := h.join(ann.code, "Bob", h.device("Bob", "teapot", "198.51.100.172").opt())
	code := ann.code
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "settings", "settings": map[string]any{"guessSeconds": 45, "betSeconds": 35}}), http.StatusOK)
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	h.closestRun(code, func(g *closest.Game) bool { return g.QuestionNo == 3 && g.Phase == closest.PhaseGuess })
	var asked []string
	h.withClosestGame(code, func(g *closest.Game) {
		asked = g.Played()
		g.Player(bob.playerID).Score = 30
	})
	if len(asked) != 3 {
		t.Fatalf("asked %v", asked)
	}
	expectError(t, h.post("/api/rooms/"+code+"/rematch", map[string]any{"token": ann.token}), http.StatusBadRequest, "The game is not finished yet")
	expectError(t, h.post("/api/rooms/"+code+"/end", map[string]any{"token": bob.token}), http.StatusBadRequest, msgOnlyHost)
	end := h.post("/api/rooms/"+code+"/end", map[string]any{"token": ann.token})
	expectStatus(t, end, http.StatusOK)
	if end.body["status"] != "finished" || dig(end.body, "winnerIds", "0") != bob.playerID || dig(end.body, "standings", "0", "id") != bob.playerID || end.body["question"] != nil {
		t.Fatalf("end game: %v %v %v", end.body["winnerIds"], end.body["standings"], end.body["question"])
	}
	before := end.body["version"].(float64)

	again := h.post("/api/rooms/"+code+"/rematch", map[string]any{"token": ann.token})
	expectStatus(t, again, http.StatusOK)
	st := again.body
	ps := players(st)
	if st["status"] != "lobby" || st["version"].(float64) <= before || len(ps) != 4 || ps[0]["name"] != "Ann" || ps[1]["name"] != "Bob" ||
		ps[2]["kind"] != "ai" || ps[2]["difficulty"] != "normal" || ps[3]["kind"] != "ai" || ps[3]["tokenKind"] == "" {
		t.Fatalf("rematch lobby: %v %v %v", st["status"], st["version"], ps)
	}
	if st["hostId"] != dig(st, "you", "id") || dig(st, "settings", "guessSeconds") != float64(45) || dig(st, "settings", "betSeconds") != float64(35) || ps[1]["score"] != float64(0) {
		t.Fatalf("rematch keeps host and rules: host %v you %v settings %v", st["hostId"], st["you"], st["settings"])
	}
	if bobState := h.state(code, bob.token); dig(bobState, "you", "id") != ps[1]["id"] {
		t.Fatal("Bob's session follows him to his new seat")
	}
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(dir, roomsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file), `"gameType":"closest"`) || !strings.Contains(string(file), `"played":["`+asked[0]) {
		t.Fatal("rooms.json lacks the game type or the played questions")
	}
	h2 := newHarness(t, withClosest(t, 5), func(c *Config) { c.DataDir = dir })
	expectStatus(t, h2.act(code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	var dealt []string
	h2.withClosestGame(code, func(g *closest.Game) {
		for _, q := range g.Questions {
			dealt = append(dealt, q.ID)
		}
	})
	for _, id := range dealt {
		if slices.Contains(asked, id) {
			t.Fatalf("question %s asked again: %v then %v", id, asked, dealt)
		}
	}
}

// A restart mid-bet brings the game back exactly (the question, the line,
// the bets so far) without replaying old events, and play goes on.
func TestClosestRestartMidGame(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, withClosest(t, 6), func(c *Config) { c.DataDir = dir; c.SaveDelay = 10 * time.Millisecond })
	ann := h.closestGame("Ann", h.device("Ann", "rocket", "198.51.100.181"))
	bob := h.join(ann.code, "Bob", h.device("Bob", "teapot", "198.51.100.182").opt())
	code := ann.code
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "settings", "settings": map[string]any{"fill": false}}), http.StatusOK)
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	h.closestTo(code, closest.PhaseGuess)
	var seq, answer, hi int
	h.withClosestGame(code, func(g *closest.Game) { seq, answer, hi = g.Cur.Seq, g.Cur.Question.Answer, g.Cur.Question.Max })
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "guess", "q": seq, "value": answer + (hi-answer)/3}), http.StatusOK)
	expectStatus(t, h.act(code, bob.token, map[string]any{"type": "guess", "q": seq, "value": answer - 1}), http.StatusOK)
	h.closestTo(code, closest.PhaseBet)
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "bet", "q": seq, "chips": []int{0, 1}}), http.StatusOK)
	before := h.state(code, ann.token)
	h.srv.mu.Lock()
	savedSeq := h.srv.rooms[code].eventSeq
	h.srv.mu.Unlock()
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(dir, roomsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file), `"gameType":"closest"`) || !strings.Contains(string(file), fmt.Sprintf(`"eventSeq":%d`, savedSeq)) || !strings.Contains(string(file), `"played":[`) {
		t.Fatal("rooms.json lacks the game type, the event sequence or the played questions")
	}
	h2 := newHarness(t, withClosest(t, 99), func(c *Config) { c.DataDir = dir }) // another seed: the saved stream wins
	after := h2.state(code, ann.token)
	for _, key := range []string{"game", "status", "phase", "phaseEndsAt", "questionNo", "question", "you"} {
		if fmt.Sprint(before[key]) != fmt.Sprint(after[key]) {
			t.Fatalf("%s changed across the restart: %v → %v", key, before[key], after[key])
		}
	}
	rec := h2.record(code, ann.token)
	waitFor(t, "the first state", 2*time.Second, func() bool { return rec.state() != nil })
	expectStatus(t, h2.act(code, bob.token, map[string]any{"type": "bet", "q": seq, "chips": []int{0, 0}, "stake": 0}), http.StatusOK)
	h2.closestTo(code, closest.PhaseReveal)
	st := h2.state(code, ann.token)
	if dig(st, "question", "answer") != float64(answer) || playerByID(st, bob.playerID)["score"] != float64(3+4) || playerByID(st, ann.playerID)["score"] != float64(1+2+1) {
		t.Fatalf("the restored question plays on: %v %v", st["question"], st["players"])
	}
	waitFor(t, "the reveal on the socket", 2*time.Second, func() bool {
		st := rec.state()
		return st != nil && st["phase"] == closest.PhaseReveal
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

// A closest game is recorded as its event stream: every reveal event
// carries every guess (with its timing) and every bet, for re-tuning the
// odds and the computers later. The computers the fill added are in the
// record's players.
func TestClosestRecorded(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, withClosest(t, 7), func(c *Config) { c.DataDir = dir; c.SaveDelay = 10 * time.Millisecond })
	ann := h.closestGame("Ann", h.device("Ann", "rocket", "198.51.100.191"))
	code := ann.code
	for _, level := range []string{"normal", "hard"} {
		expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": ann.token, "difficulty": level}), http.StatusOK)
	}
	expectStatus(t, h.act(code, ann.token, map[string]any{"type": "start"}), http.StatusOK)
	guesses, bets := 0, 0
	for {
		h.closestRun(code, func(g *closest.Game) bool {
			if g.Status == closest.StatusFinished {
				return true
			}
			you := g.View(ann.playerID, closest.Viewer{Role: "player"}).You
			return you != nil && ((you.CanGuess && you.Guess == nil) || you.CanBet)
		})
		var finished, canGuess bool
		var seq, value int
		h.withClosestGame(code, func(g *closest.Game) {
			finished = g.Status == closest.StatusFinished
			if !finished {
				seq, value = g.Cur.Seq, g.Cur.Question.Answer+1
				canGuess = g.Phase == closest.PhaseGuess
			}
		})
		if finished {
			break
		}
		if canGuess {
			h.clock.Advance(1500 * time.Millisecond) // she takes 1.5 s, before any computer guesses
			expectStatus(t, h.act(code, ann.token, map[string]any{"type": "guess", "q": seq, "value": value}), http.StatusOK)
			guesses++
		} else {
			expectStatus(t, h.act(code, ann.token, map[string]any{"type": "bet", "q": seq, "chips": []int{0, 0}}), http.StatusOK)
			bets++
		}
	}
	names := finishedRecords(t, h, dir)
	if len(names) != 1 {
		t.Fatalf("records %v", names)
	}
	rec := readTableRecord(t, dir, names[0])
	if rec.GameType != "closest" || rec.Outcome.Reason != gamerecord.EndFinished || rec.Outcome.WinnerID == "" || len(rec.Players) != 4 {
		t.Fatalf("%s %+v players %d", rec.GameType, rec.Outcome, len(rec.Players))
	}
	if filled := rec.Players[3]; filled.Kind != "ai" || filled.Difficulty != "normal" || filled.Input != "ai" || filled.TokenKind == "" {
		t.Fatalf("the filled computer as recorded: %+v", filled)
	}
	var reveals, annGuesses, annBets int
	for _, e := range rec.Events {
		var ev closest.Event
		if err := json.Unmarshal(e.Event, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type != closest.EvReveal {
			continue
		}
		reveals++
		if ev.Answer == nil || ev.QuestionID == "" || len(ev.Guesses) == 0 {
			t.Fatalf("reveal event %+v", ev)
		}
		for _, g := range ev.Guesses {
			if g.PlayerID == ann.playerID {
				annGuesses++
				if g.Value != *ev.Answer+1 || g.AfterMs < 1500 || g.AfterMs > 4000 || g.Changed {
					t.Fatalf("Ann's guess as recorded: %+v", g)
				}
			}
		}
		for _, b := range ev.Bets {
			if b.PlayerID == ann.playerID {
				annBets++
				if b.Chips != [2]int{0, 0} || b.Stake != 0 || b.Auto {
					t.Fatalf("Ann's bet as recorded: %+v", b)
				}
			}
		}
	}
	if reveals != 7 || annGuesses != guesses || annBets != bets || guesses != 7 || bets == 0 {
		t.Fatalf("%d reveals; Ann's guesses %d of %d, bets %d of %d", reveals, annGuesses, guesses, annBets, bets)
	}
}
