package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/poker/bots"
	"github.com/tdar200/estate-games/internal/poker/holdem"
)

// fastTiming makes poker's pauses short enough to play many hands quickly.
var fastTiming = holdem.Timing{RunoutStepMs: 15, HandOverMs: 15, ShowdownMs: 15, RebuyMs: 30_000}

// withPoker adds the poker game type. The room timer re-checks every few
// milliseconds so tests that move the fake clock see the engine react
// without waiting for the production tick.
func withPoker(timing *holdem.Timing) harnessOption {
	return func(c *Config) {
		c.Engines = map[string]Engine{GamePoker: PokerEngine{Timing: timing}}
		c.pokerTick = 5 * time.Millisecond
	}
}

// pokerGame creates a poker game from a phone (its host player).
func (h *harness) pokerGame(name string, d device) seat {
	h.t.Helper()
	r := h.post("/api/games", map[string]any{"name": name, "gameType": "poker"}, d.opt())
	expectStatus(h.t, r, http.StatusCreated)
	return seat{r.str("code"), r.str("token"), r.str("playerId")}
}

func (h *harness) pokerJoin(code, name string, d device) seat {
	h.t.Helper()
	r := h.post("/api/rooms/"+code+"/join", map[string]any{"name": name}, d.opt())
	expectStatus(h.t, r, http.StatusOK)
	return seat{r.str("code"), r.str("token"), r.str("playerId")}
}

func (h *harness) pokerAct(code, token string, action string) response {
	h.t.Helper()
	return h.act(code, token, json.RawMessage(action))
}

// withTable runs fn on a room's poker table under the server lock.
func (h *harness) withTable(code string, fn func(*holdem.Table)) {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	fn(pokerOf(h.srv.rooms[code]).t)
}

// recorder keeps every WebSocket frame one viewer receives.
type recorder struct {
	mu     sync.Mutex
	frames []recFrame
	last   map[string]any // latest state
	sock   *socket
	closed chan struct{}
}

type recFrame struct {
	at  time.Time
	raw []byte
}

func (h *harness) record(code, token string) *recorder {
	h.t.Helper()
	rec := &recorder{sock: h.dial("/ws/rooms/"+code+"?token="+token, nil), closed: make(chan struct{})}
	go func() {
		defer close(rec.closed)
		for {
			_, data, err := rec.sock.conn.Read(context.Background())
			if err != nil {
				return
			}
			var msg map[string]any
			_ = json.Unmarshal(data, &msg)
			rec.mu.Lock()
			rec.frames = append(rec.frames, recFrame{time.Now(), data})
			if msg["type"] == "state" {
				rec.last, _ = msg["state"].(map[string]any)
			}
			rec.mu.Unlock()
		}
	}()
	return rec
}

func (rec *recorder) state() map[string]any {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.last
}

func (rec *recorder) snapshot() []recFrame {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]recFrame(nil), rec.frames...)
}

// events returns every event received so far.
func (rec *recorder) events() []map[string]any {
	var out []map[string]any
	for _, f := range rec.snapshot() {
		var msg map[string]any
		if json.Unmarshal(f.raw, &msg) == nil && msg["type"] == "event" {
			out = append(out, msg["event"].(map[string]any))
		}
	}
	return out
}

func num(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func strs(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// legalAction picks a move from you.legal: mostly check/call, sometimes a
// raise, all-in or fold.
func legalAction(rng *rand.Rand, you map[string]any) string {
	legal, _ := you["legal"].(map[string]any)
	if legal == nil {
		return ""
	}
	raise, _ := legal["raise"].(map[string]any)
	switch k := rng.IntN(20); {
	case k < 2 && legal["fold"] == true:
		return `{"type":"fold"}`
	case k < 4 && raise != nil:
		return fmt.Sprintf(`{"type":"raise","to":%d}`, num(raise["min"]))
	case k < 5:
		return `{"type":"allin"}`
	}
	if legal["check"] == true {
		return `{"type":"check"}`
	}
	return `{"type":"call"}`
}

// ---------------------------------------------------------------------------

// Game types: POST /api/games and /api/tables take gameType; unknown types
// are refused; the classic API stays Estate; poker rooms are phones only and
// offer only the computer levels that exist.
func TestPokerGameTypes(t *testing.T) {
	h := newHarness(t, withPoker(nil))
	alice := h.device("Alice", "rocket", "198.51.100.81")
	res := h.post("/api/games", map[string]any{"name": "Alice", "gameType": "poker"}, alice.opt())
	expectStatus(t, res, http.StatusCreated)
	st := res.body["state"].(map[string]any)
	if st["game"] != "poker" || st["gameType"] != "poker" || st["viewerRole"] != "player" || dig(st, "you", "isHost") != true {
		t.Fatalf("host's poker lobby: game %v type %v role %v you %v", st["game"], st["gameType"], st["viewerRole"], st["you"])
	}
	if got := strings.Join(strs(st["aiLevels"]), ","); got != "easy,normal" {
		t.Fatalf("aiLevels %q", got)
	}
	if dig(st, "settings", "actionSeconds") != float64(20) || dig(st, "settings", "timeBankSeconds") != float64(30) || dig(st, "settings", "style") != "tournament" {
		t.Fatalf("settings %v", st["settings"])
	}
	code, token := res.str("code"), res.str("token")
	if p := playerByID(st, res.str("playerId")); p["input"] != "phone" || p["tokenKind"] != "rocket" || p["online"] != true || p["timeBank"] != float64(30) {
		t.Fatalf("host player %v", p)
	}

	// The games list and the summary carry the type.
	list := h.get("/api/games", alice.opt())
	expectStatus(t, list, http.StatusOK)
	if dig(list.body, "games", "0", "gameType") != "poker" || dig(list.body, "games", "0", "rules", "style") != "tournament" {
		t.Fatalf("games list %s", list.raw)
	}
	sum := h.get("/api/rooms/" + code + "/summary")
	if sum.str("gameType") != "poker" || dig(sum.body, "openSeats") != float64(7) || dig(sum.body, "turn") != float64(0) {
		t.Fatalf("summary %s", sum.raw)
	}

	// Unknown types are refused.
	bob := h.device("Bob", "teapot", "198.51.100.82")
	expectError(t, h.post("/api/games", map[string]any{"name": "Bob", "gameType": "chess"}, bob.opt()), http.StatusBadRequest, "unknown game type")
	expectError(t, h.post("/api/games", map[string]any{"name": "Bob", "gameType": 7}, bob.opt()), http.StatusBadRequest, "unknown game type")
	expectError(t, h.post("/api/tables", map[string]any{"gameType": "chess"}), http.StatusBadRequest, "unknown game type")

	// A PC table for poker: a viewer, no local seats.
	tb := h.post("/api/tables", map[string]any{"gameType": "poker"})
	expectStatus(t, tb, http.StatusCreated)
	if dig(tb.body, "state", "game") != "poker" || dig(tb.body, "state", "viewerRole") != "table" || dig(tb.body, "state", "you") != nil {
		t.Fatalf("poker table %s", tb.raw)
	}
	if seats, ok := dig(tb.body, "state", "localSeats").([]any); !ok || len(seats) != 0 {
		t.Fatalf("localSeats %v", dig(tb.body, "state", "localSeats"))
	}
	expectError(t, h.addSeat(table{tb.str("code"), tb.str("tableToken")}, "keyboard", "Kim", "key", nil), http.StatusBadRequest, "poker needs a phone per player")
	expectError(t, h.addSeat(table{tb.str("code"), tb.str("tableToken")}, "controller", "Pat", "key", 0), http.StatusBadRequest, "poker needs a phone per player")

	// Estate stays the default, and party states say so.
	est := h.post("/api/tables", map[string]any{})
	expectStatus(t, est, http.StatusCreated)
	if dig(est.body, "state", "game") != nil || dig(est.body, "state", "gameType") != "estate" || len(strs(dig(est.body, "state", "aiLevels"))) != 5 {
		t.Fatalf("estate table %v %v", dig(est.body, "state", "gameType"), dig(est.body, "state", "aiLevels"))
	}
	// The classic Node-compatible API is Estate-only and unchanged.
	classic := h.post("/api/rooms", map[string]any{"name": "Cat", "gameType": "poker"})
	expectStatus(t, classic, http.StatusCreated)
	if s := classic.body["state"].(map[string]any); s["game"] != nil || s["gameType"] != nil || s["aiLevels"] != nil || s["board"] == nil {
		t.Fatalf("classic room is not plain Estate: %s", classic.raw)
	}

	// Computer seats: only the levels poker has.
	expectError(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": "hard"}), http.StatusBadRequest, "Choose easy or normal")
	ai := h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": "easy"})
	expectStatus(t, ai, http.StatusOK)
	var aiPlayer map[string]any
	for _, p := range players(ai.body) {
		if p["kind"] == "ai" {
			aiPlayer = p
		}
	}
	if aiPlayer == nil || aiPlayer["input"] != "ai" || aiPlayer["difficulty"] != "easy" {
		t.Fatalf("computer seat %v", aiPlayer)
	}
	h.srv.mu.Lock()
	m := h.srv.rooms[code].meta[aiPlayer["id"].(string)]
	hasBot := m.pokerBot != nil && m.bot == nil
	h.srv.mu.Unlock()
	if !hasBot {
		t.Fatal("poker computer seats get a poker bot")
	}

	// Estate-only routes.
	expectError(t, h.post("/api/rooms/"+code+"/trade-quote", map[string]any{"token": token, "targetId": aiPlayer["id"]}), http.StatusBadRequest, msgPokerNoQuotes)

	// Without poker configured, the type is unknown.
	h2 := newHarness(t)
	d2 := h2.device("Dee", "key", "198.51.100.83")
	expectError(t, h2.post("/api/games", map[string]any{"name": "Dee", "gameType": "poker"}, d2.opt()), http.StatusBadRequest, "unknown game type")
}

// A whole multi-hand game over WebSockets: two phones, a computer, the table
// screen and a spectator. Every frame is scanned: no hole card ever reaches
// anyone but its owner before it is turned face up, hand strength goes only
// to its owner, and the computer moves after the computers' pause.
func TestPokerPrivateCardsOverSockets(t *testing.T) {
	timing := fastTiming
	// The pause is checked from frame arrival times, which a busy machine can bunch
	// together, so it is long enough to leave room: fail only under half of it.
	const aiPause = 120 * time.Millisecond
	h := newHarness(t, withPoker(&timing), func(c *Config) { c.AIDelay = aiPause })
	tbr := h.post("/api/tables", map[string]any{"gameType": "poker"})
	expectStatus(t, tbr, http.StatusCreated)
	tb := table{tbr.str("code"), tbr.str("tableToken")}
	ann := h.pokerJoin(tb.code, "Ann", h.device("Ann", "rocket", "198.51.100.91"))
	ben := h.pokerJoin(tb.code, "Ben", h.device("Ben", "teapot", "198.51.100.92"))
	ai := h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": "normal"})
	expectStatus(t, ai, http.StatusOK)
	aiID := ""
	for _, p := range players(ai.body) {
		if p["kind"] == "ai" {
			aiID = p["id"].(string)
		}
	}
	watch := h.post("/api/rooms/"+tb.code+"/watch", map[string]any{"name": "Viv"})
	expectStatus(t, watch, http.StatusOK)

	recs := map[string]*recorder{
		"":           h.record(tb.code, tb.token),
		ann.playerID: h.record(tb.code, ann.token),
		ben.playerID: h.record(tb.code, ben.token),
		"spectator":  h.record(tb.code, watch.str("token")),
	}
	tokens := map[string]string{ann.playerID: ann.token, ben.playerID: ben.token}
	// The table acts as host: smaller stacks for more all-ins and side pots.
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "settings", "settings": map[string]any{"startingStack": 600, "blindSchedule": "off"}}), http.StatusOK)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)

	rng := rand.New(rand.NewPCG(11, 12))
	acted := map[string]float64{}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st := recs[""].state()
		if st != nil && (st["status"] == "finished" || num(st["handNumber"]) > 12) {
			break
		}
		for id, token := range tokens {
			ps := recs[id].state()
			you, _ := ps["you"].(map[string]any)
			if you == nil || you["toAct"] != true || acted[id] == ps["version"].(float64) {
				continue
			}
			acted[id] = ps["version"].(float64)
			h.pokerAct(tb.code, token, legalAction(rng, you)) // a stale state may be refused; that's fine
		}
		time.Sleep(3 * time.Millisecond)
	}
	final := recs[""].state()
	if final["status"] != "finished" && num(final["handNumber"]) < 6 {
		t.Fatalf("only %v hands played (phase %v)", final["handNumber"], final["phase"])
	}

	// Ground truth for the people's cards: what each phone saw as its own.
	own := map[int]map[string][]string{} // hand → player → cards
	for id := range tokens {
		for _, f := range recs[id].snapshot() {
			var msg map[string]any
			_ = json.Unmarshal(f.raw, &msg)
			st, _ := msg["state"].(map[string]any)
			if you, _ := st["you"].(map[string]any); you != nil && len(strs(you["cards"])) == 2 {
				hand := num(st["handNumber"])
				if own[hand] == nil {
					own[hand] = map[string][]string{}
				}
				own[hand][id] = strs(you["cards"])
			}
		}
	}
	shownSeen, strengthSeen := 0, 0
	for viewer, rec := range recs {
		viewerID := viewer
		if viewer == "spectator" {
			viewerID = ""
		}
		s, st := scanPokerFrames(t, viewer, viewerID, rec.snapshot(), own)
		shownSeen += s
		strengthSeen += st
	}
	if shownSeen == 0 {
		t.Fatal("no hand was ever shown: the scan proved nothing")
	}
	if strengthSeen == 0 {
		t.Fatal("phones never got their hand strength")
	}

	// The computer played, and never faster than the computers' pause.
	var becameActor time.Time
	aiActions := 0
	for _, f := range recs[""].snapshot() {
		var msg map[string]any
		_ = json.Unmarshal(f.raw, &msg)
		if msg["type"] == "state" {
			if st := msg["state"].(map[string]any); st["actorId"] == aiID && becameActor.IsZero() {
				becameActor = f.at
			}
			continue
		}
		ev, _ := msg["event"].(map[string]any)
		if ev["type"] == "action" && ev["playerId"] == aiID {
			aiActions++
			if !becameActor.IsZero() && f.at.Sub(becameActor) < aiPause/2 {
				t.Fatalf("the computer acted %v after its turn began (pause %v)", f.at.Sub(becameActor), aiPause)
			}
			becameActor = time.Time{}
		}
		if ev["type"] == "action" || ev["type"] == "deal" || ev["type"] == "handStart" {
			if ev["playerId"] != aiID {
				becameActor = time.Time{}
			}
		}
	}
	if aiActions == 0 {
		t.Fatal("the computer never acted")
	}
}

// scanPokerFrames checks every frame one viewer received; viewerID is "" for
// the table screen and spectators. It returns how many states showed a hand
// face up and how many carried the viewer's hand strength.
func scanPokerFrames(t *testing.T, who, viewerID string, frames []recFrame, own map[int]map[string][]string) (shown, strength int) {
	t.Helper()
	seenLog := map[string]bool{}
	for k, f := range frames {
		var msg map[string]any
		if err := json.Unmarshal(f.raw, &msg); err != nil {
			t.Fatalf("%s frame %d: %v", who, k, err)
		}
		switch msg["type"] {
		case "event":
			ev := msg["event"].(map[string]any)
			cards := strs(ev["cards"])
			if len(cards) > 0 && ev["type"] != "deal" && ev["type"] != "reveal" {
				t.Fatalf("%s: event %v carries cards", who, ev)
			}
			allowed := map[string]bool{}
			for _, c := range cards {
				allowed[c] = true
			}
			for _, c := range cardTokens(f.raw) {
				if !allowed[c] {
					t.Fatalf("%s: card %s in event %s", who, c, f.raw)
				}
			}
			continue
		case "state":
		default:
			continue
		}
		st := msg["state"].(map[string]any)
		if (st["viewerId"] == nil && viewerID != "") || (st["viewerId"] != nil && st["viewerId"] != viewerID) {
			t.Fatalf("%s got a state for %v", who, st["viewerId"])
		}
		n := strings.Count(string(f.raw), `"strength"`)
		you, _ := st["you"].(map[string]any)
		if viewerID == "" && (you != nil || n > 0) {
			t.Fatalf("%s (no seat) got a player panel or a strength: %s", who, f.raw)
		}
		if n > 1 {
			t.Fatalf("%s: %d strength fields", who, n)
		}
		if you != nil && you["strength"] != nil {
			strength++
		}
		allowed := map[string]bool{}
		add := func(cs []string) {
			for _, c := range cs {
				allowed[c] = true
			}
		}
		add(strs(st["board"]))
		if you != nil {
			if you["id"] != viewerID {
				t.Fatalf("%s got someone else's panel", who)
			}
			add(strs(you["cards"]))
		}
		shownIDs := map[string]bool{}
		names := map[string]string{}
		for _, p := range players(st) {
			names[p["name"].(string)] = p["id"].(string)
			cards := strs(p["cards"])
			if len(cards) == 0 {
				continue
			}
			if p["id"] != viewerID && p["shown"] != true {
				t.Fatalf("%s sees %s's face-down cards %v", who, p["name"], cards)
			}
			if p["shown"] == true {
				shownIDs[p["id"].(string)] = true
				if p["id"] != viewerID {
					shown++
				}
			}
			add(cards)
		}
		if res, _ := st["result"].(map[string]any); res != nil {
			for _, raw := range res["hands"].([]any) {
				hd := raw.(map[string]any)
				if hd["mucked"] == true && hd["cards"] != nil {
					t.Fatalf("%s: a mucked hand's cards %v", who, hd["cards"])
				}
				add(strs(hd["cards"]))
			}
		}
		// Everything except the (historical) log must be explained by the
		// board, the viewer's own cards and face-up hands.
		withoutLog := map[string]any{}
		for k, v := range st {
			if k != "log" {
				withoutLog[k] = v
			}
		}
		data, _ := json.Marshal(withoutLog)
		for _, c := range cardTokens(data) {
			if !allowed[c] {
				t.Fatalf("%s: card %s leaks in state (phase %v, hand %v)", who, c, st["phase"], st["handNumber"])
			}
		}
		// Ground truth: no other person's cards for this hand, unless shown.
		for id, cards := range own[num(st["handNumber"])] {
			if id == viewerID || shownIDs[id] {
				continue
			}
			for _, c := range cards {
				for _, got := range cardTokens(data) {
					if got == c {
						t.Fatalf("%s sees %s's hidden %s", who, id, c)
					}
				}
			}
		}
		// New log lines name cards only for the board or a hand turned up now.
		logs, _ := st["log"].([]any)
		for _, raw := range logs {
			line := raw.(map[string]any)
			id, text := line["id"].(string), line["text"].(string)
			if seenLog[id] {
				continue
			}
			seenLog[id] = true
			cards := cardTokens([]byte(text))
			if len(cards) == 0 {
				continue
			}
			switch {
			case strings.HasPrefix(text, "Flop: "), strings.HasPrefix(text, "Turn: "), strings.HasPrefix(text, "River: "):
			case strings.Contains(text, " shows "):
				name := strings.SplitN(text, " shows ", 2)[0]
				if pid := names[name]; !shownIDs[pid] {
					t.Fatalf("%s: log %q but %s's cards are not face up", who, text, name)
				}
			default:
				t.Fatalf("%s: log line with cards %q", who, text)
			}
		}
	}
	return shown, strength
}

// cardTokens finds card codes ("As", "Td") standing alone in text.
func cardTokens(data []byte) []string {
	isRank := func(b byte) bool { return strings.IndexByte("23456789TJQKA", b) >= 0 }
	isSuit := func(b byte) bool { return strings.IndexByte("cdhs", b) >= 0 }
	alnum := func(b byte) bool {
		return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	var out []string
	for i := 0; i+1 < len(data); i++ {
		if !isRank(data[i]) || !isSuit(data[i+1]) {
			continue
		}
		if (i > 0 && alnum(data[i-1])) || (i+2 < len(data) && alnum(data[i+2])) {
			continue
		}
		out = append(out, string(data[i:i+2]))
	}
	return out
}

// The engine's timers run on the server clock: the default 20-second turn
// timer, then the 30-second time bank, then the timeout fold; the next hand
// and the all-in run-out follow — all from moving the fake clock, without
// the test touching the engine.
func TestPokerTimersFollowTheClock(t *testing.T) {
	h := newHarness(t, withPoker(nil))
	host := h.pokerGame("Ann", h.device("Ann", "rocket", "198.51.100.101"))
	ben := h.pokerJoin(host.code, "Ben", h.device("Ben", "teapot", "198.51.100.102"))
	recA := h.record(host.code, host.token)
	recB := h.record(host.code, ben.token)
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"start"}`), http.StatusOK)

	waitState := func(what string, cond func(map[string]any) bool) map[string]any {
		t.Helper()
		var st map[string]any
		waitFor(t, what, 5*time.Second, func() bool {
			st = recA.state()
			return st != nil && cond(st)
		})
		return st
	}
	st := waitState("hand 1", func(s map[string]any) bool { return num(s["handNumber"]) == 1 && s["actorId"] != "" })
	actor := st["actorId"].(string)
	deadline := int64(st["actionDeadline"].(float64))
	if left := deadline - int64(st["now"].(float64)); left < 19_000 || left > 20_000 || st["actionTimeBank"] != false {
		t.Fatalf("turn timer: %d ms left, bank %v", left, st["actionTimeBank"])
	}
	for _, p := range players(st) {
		if p["timeBank"] != float64(30) {
			t.Fatalf("time bank at the start %v", p)
		}
	}

	// Nothing happens before the timer runs out.
	h.clock.Advance(19 * time.Second)
	time.Sleep(60 * time.Millisecond)
	if st := recA.state(); st["actorId"] != actor || st["actionTimeBank"] != false {
		t.Fatalf("moved early: actor %v bank %v", st["actorId"], st["actionTimeBank"])
	}
	// The timer runs out: the bank runs on.
	h.clock.Advance(1100 * time.Millisecond)
	st = waitState("the time bank", func(s map[string]any) bool { return s["actionTimeBank"] == true })
	if st["actorId"] != actor || int64(st["actionDeadline"].(float64)) != deadline+30_000 {
		t.Fatalf("bank: actor %v deadline %v (want %d)", st["actorId"], st["actionDeadline"], deadline+30_000)
	}
	sawBank := false
	for _, ev := range recB.events() {
		sawBank = sawBank || (ev["type"] == "timeBank" && ev["playerId"] == actor && ev["amount"] == float64(30))
	}
	if !sawBank {
		t.Fatal("no timeBank event")
	}
	// The bank runs out: heads-up preflop the small blind can't check, so
	// the timeout folds and the hand is over.
	h.clock.Advance(30 * time.Second)
	st = waitState("the timeout", func(s map[string]any) bool { return s["phase"] == "handOver" })
	if p := playerByID(st, actor); p["timeBank"] != float64(0) || p["status"] != "folded" {
		t.Fatalf("timed-out player %v", p)
	}
	// The next hand comes at nextHandAt (2.5 s).
	h.clock.Advance(3 * time.Second)
	st = waitState("hand 2", func(s map[string]any) bool { return num(s["handNumber"]) == 2 && s["phase"] == "betting" })

	// Hand 2: all-in and call, then the board runs out street by street.
	for guard := 0; guard < 4 && st["phase"] == "betting"; guard++ {
		next := st["actorId"].(string)
		token := host.token
		if next == ben.playerID {
			token = ben.token
		}
		expectStatus(t, h.pokerAct(host.code, token, `{"type":"allin"}`), http.StatusOK)
		st = waitState("the next decision", func(s map[string]any) bool { return s["actorId"] != next || s["phase"] != "betting" })
	}
	if st["phase"] != "runout" || len(strs(st["board"])) != 0 {
		t.Fatalf("after the all-in: phase %v board %v", st["phase"], st["board"])
	}
	for _, want := range []int{3, 4, 5} {
		h.clock.Advance(1900 * time.Millisecond)
		waitState(fmt.Sprintf("%d board cards", want), func(s map[string]any) bool { return len(strs(s["board"])) == want })
	}
	waitState("the showdown", func(s map[string]any) bool { return s["phase"] == "showdown" || s["status"] == "finished" })
}

// An away player's stand-in only checks or folds, and stops when they are
// back; they stay dealt in.
func TestPokerStandInChecksOrFolds(t *testing.T) {
	timing := fastTiming
	h := newHarness(t, withPoker(&timing), func(c *Config) { c.AutopilotDelay = 60 * time.Millisecond })
	host := h.pokerGame("Ann", h.device("Ann", "rocket", "198.51.100.111"))
	ben := h.pokerJoin(host.code, "Ben", h.device("Ben", "teapot", "198.51.100.112"))
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"settings","settings":{"actionSeconds":0}}`), http.StatusOK)
	recA := h.record(host.code, host.token)
	recB := h.record(host.code, ben.token)
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"start"}`), http.StatusOK)
	waitFor(t, "hand 1", 2*time.Second, func() bool { return recA.state() != nil && num(recA.state()["handNumber"]) == 1 })
	recA.sock.conn.CloseNow() // Ann walks away

	// Ben raises every third hand (so Ann's stand-in faces bets) and
	// otherwise checks or calls (so it gets free checks too).
	acted := 0.0
	standIn := func() (checks, folds, other int) {
		for _, ev := range recB.events() {
			if ev["playerId"] != host.playerID || ev["type"] != "action" {
				continue
			}
			switch ev["action"] {
			case "check":
				checks++
			case "fold":
				folds++
			default:
				other++
			}
		}
		return
	}
	sawAutopilot := false
	waitFor(t, "the stand-in to check and fold for Ann", 10*time.Second, func() bool {
		st := recB.state()
		if p := playerByID(st, host.playerID); p != nil && p["autopilot"] == true {
			sawAutopilot = true
		}
		if you, _ := st["you"].(map[string]any); you != nil && you["toAct"] == true && st["version"].(float64) != acted {
			acted = st["version"].(float64)
			legal := you["legal"].(map[string]any)
			action := `{"type":"check"}`
			if raise, _ := legal["raise"].(map[string]any); raise != nil && num(st["handNumber"])%3 == 0 {
				action = fmt.Sprintf(`{"type":"raise","to":%d}`, num(raise["min"]))
			} else if legal["check"] != true {
				action = `{"type":"call"}`
			}
			h.pokerAct(host.code, ben.token, action)
		}
		checks, folds, _ := standIn()
		return checks >= 1 && folds >= 1 || recB.state()["status"] == "finished"
	})
	if !sawAutopilot {
		t.Fatal("players[].autopilot never showed")
	}
	if _, _, other := standIn(); other > 0 {
		t.Fatalf("the stand-in did more than check or fold for Ann (%d other actions)", other)
	}
	if st := recB.state(); st["status"] == "finished" {
		for _, l := range st["log"].([]any) {
			t.Log(l.(map[string]any)["text"])
		}
		t.Fatal("the game ended; the stand-in must not lose Ann's chips by calling")
	}

	// Ann is back: the stand-in stops and the game waits for her.
	recA = h.record(host.code, host.token)
	waitFor(t, "Ann's seat handed back", 2*time.Second, func() bool {
		p := playerByID(recA.state(), host.playerID)
		return p != nil && p["autopilot"] == nil && p["online"] == true
	})
	waitFor(t, "a decision for Ann", 10*time.Second, func() bool {
		st := recB.state()
		if you, _ := st["you"].(map[string]any); you != nil && you["toAct"] == true && st["version"].(float64) != acted {
			acted = st["version"].(float64)
			legal := you["legal"].(map[string]any)
			if legal["check"] == true {
				h.pokerAct(host.code, ben.token, `{"type":"check"}`)
			} else {
				h.pokerAct(host.code, ben.token, `{"type":"call"}`)
			}
		}
		return recA.state()["actorId"] == host.playerID
	})
	before := recA.state()["version"]
	time.Sleep(300 * time.Millisecond)
	if after := recA.state(); after["version"] != before || after["actorId"] != host.playerID {
		t.Fatalf("the game moved on without Ann (version %v → %v)", before, after["version"])
	}
}

// A poker seat moves to a new device in the middle of a hand, with the same
// hole cards.
func TestPokerReclaimMidHand(t *testing.T) {
	h := newHarness(t, withPoker(nil))
	tbr := h.post("/api/tables", map[string]any{"gameType": "poker"})
	tb := table{tbr.str("code"), tbr.str("tableToken")}
	alice := h.device("Alice", "key", "198.51.100.121")
	a := h.pokerJoin(tb.code, "Alice", alice)
	b := h.pokerJoin(tb.code, "Bob", h.device("Bob", "teapot", "198.51.100.122"))
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "settings", "settings": map[string]any{"actionSeconds": 0}}), http.StatusOK)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	before := h.state(tb.code, a.token)
	cards := strs(dig(before, "you", "cards"))
	if len(cards) != 2 {
		t.Fatalf("Alice's cards %v", cards)
	}

	h.clock.Advance(onlineWindow + time.Second)
	h.state(tb.code, tb.token) // the table and Bob are still here
	h.state(tb.code, b.token)
	laptop := h.device("Alice", "key", "198.51.100.123")
	ask := h.reclaim(tb.code, a.playerID, laptop, nil)
	expectStatus(t, ask, http.StatusAccepted)
	expectStatus(t, h.decide(tb.code, tb.token, ask.str("claim", "id"), "approve"), http.StatusOK)
	got := h.reclaim(tb.code, a.playerID, laptop, nil)
	expectStatus(t, got, http.StatusOK)
	st := got.body["state"].(map[string]any)
	if st["viewerId"] != a.playerID || strings.Join(strs(dig(st, "you", "cards")), " ") != strings.Join(cards, " ") || st["handNumber"] != before["handNumber"] {
		t.Fatalf("reclaimed seat: viewer %v cards %v hand %v", st["viewerId"], dig(st, "you", "cards"), st["handNumber"])
	}
	expectError(t, h.get("/api/rooms/"+tb.code+"/state?token="+a.token), http.StatusBadRequest, msgSessionInvalid)
	// The new device plays the seat.
	token := got.str("token")
	for guard := 0; guard < 6; guard++ {
		cur := h.state(tb.code, token)
		if dig(cur, "you", "toAct") == true {
			expectStatus(t, h.pokerAct(tb.code, token, `{"type":"call"}`), http.StatusOK)
			return
		}
		expectStatus(t, h.pokerAct(tb.code, b.token, `{"type":"call"}`), http.StatusOK)
	}
	t.Fatal("Alice's new device never got a turn")
}

// Restart in the middle of a hand: the same hand, the same cards to come,
// and no event is sent twice.
func TestPokerRestartMidHand(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, withPoker(nil), func(c *Config) { c.DataDir = dir; c.SaveDelay = 10 * time.Millisecond })
	alice := h.device("Alice", "rocket", "198.51.100.131")
	host := h.pokerGame("Alice", alice)
	ben := h.pokerJoin(host.code, "Ben", h.device("Ben", "teapot", "198.51.100.132"))
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"start"}`), http.StatusOK)
	// One action, then restart.
	st := h.state(host.code, host.token)
	actorToken := host.token
	if st["actorId"] == ben.playerID {
		actorToken = ben.token
	}
	expectStatus(t, h.pokerAct(host.code, actorToken, `{"type":"call"}`), http.StatusOK)
	before := h.state(host.code, host.token)
	var saved []byte
	var savedSeq int64
	h.srv.mu.Lock()
	r := h.srv.rooms[host.code]
	saved, savedSeq = append([]byte(nil), r.lastJSON...), r.eventSeq
	h.srv.mu.Unlock()
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(dir, roomsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file), `"gameType":"poker"`) || !strings.Contains(string(file), fmt.Sprintf(`"eventSeq":%d`, savedSeq)) {
		t.Fatalf("rooms.json lacks the game type or event sequence")
	}

	h2 := newHarness(t, withPoker(nil), func(c *Config) { c.DataDir = dir })
	after := h2.state(host.code, host.token)
	for _, key := range []string{"game", "status", "phase", "handNumber", "actorId", "board", "pots", "totalPot", "currentBet", "buttonSeat"} {
		if fmt.Sprint(before[key]) != fmt.Sprint(after[key]) {
			t.Fatalf("%s changed across the restart: %v → %v", key, before[key], after[key])
		}
	}
	if fmt.Sprint(dig(before, "you", "cards")) != fmt.Sprint(dig(after, "you", "cards")) || fmt.Sprint(before["players"]) == "" {
		t.Fatalf("hole cards changed: %v → %v", dig(before, "you", "cards"), dig(after, "you", "cards"))
	}
	// Events already sent are not sent again.
	rec := h2.record(host.code, host.token)
	waitFor(t, "the first state", 2*time.Second, func() bool { return rec.state() != nil })
	time.Sleep(50 * time.Millisecond)
	for _, ev := range rec.events() {
		if int64(ev["seq"].(float64)) <= savedSeq {
			t.Fatalf("event %v replayed after the restart", ev)
		}
	}

	// The same cards come next: the saved deck predicts the flop.
	predict, err := holdem.Restore(saved, holdem.Options{Timing: &holdem.Timing{}})
	if err != nil {
		t.Fatal(err)
	}
	for guard := 0; guard < 20 && len(predict.Hand.Board) < 3; guard++ {
		w := predict.Waiting()
		if len(w) == 0 {
			break
		}
		act := `{"type":"call"}`
		if predict.Hand.Legal().CanCheck {
			act = `{"type":"check"}`
		}
		if err := predict.PerformAction(w[0].PlayerID, json.RawMessage(act)); err != nil {
			t.Fatal(err)
		}
	}
	tokens := map[string]string{host.playerID: host.token, ben.playerID: ben.token}
	var flop []string
	for guard := 0; guard < 20; guard++ {
		cur := h2.state(host.code, host.token)
		if b := strs(cur["board"]); len(b) >= 3 {
			flop = b[:3]
			break
		}
		id, _ := cur["actorId"].(string)
		if id == "" {
			t.Fatalf("no actor before the flop: %v", cur["phase"])
		}
		act := `{"type":"call"}`
		if you := h2.state(host.code, tokens[id])["you"].(map[string]any); dig(you, "legal", "check") == true {
			act = `{"type":"check"}`
		}
		expectStatus(t, h2.pokerAct(host.code, tokens[id], act), http.StatusOK)
	}
	want := []string{}
	for _, c := range predict.Hand.Board[:3] {
		want = append(want, holdem.CardString(c))
	}
	if strings.Join(flop, " ") != strings.Join(want, " ") {
		t.Fatalf("flop after the restart %v, saved deck says %v", flop, want)
	}
}

// End Game ranks a tournament by chips; rematch keeps the game type and the
// settings. A cash game: fixed blinds, rebuy whenever, End Game ranks by
// chips won.
func TestPokerEndGameRematchAndCash(t *testing.T) {
	timing := fastTiming
	h := newHarness(t, withPoker(&timing))
	host := h.pokerGame("Ann", h.device("Ann", "rocket", "198.51.100.141"))
	ben := h.pokerJoin(host.code, "Ben", h.device("Ben", "teapot", "198.51.100.142"))
	h.record(host.code, host.token)
	h.record(host.code, ben.token)
	tokens := map[string]string{host.playerID: host.token, ben.playerID: ben.token}
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"settings","settings":{"startingStack":2000,"actionSeconds":30,"timeBankSeconds":60,"blindSchedule":"off"}}`), http.StatusOK)
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"start"}`), http.StatusOK)
	// The first player raises, the other folds.
	st := h.state(host.code, host.token)
	first := st["actorId"].(string)
	expectStatus(t, h.pokerAct(host.code, tokens[first], `{"type":"raise","to":200}`), http.StatusOK)
	second := h.state(host.code, host.token)["actorId"].(string)
	expectStatus(t, h.pokerAct(host.code, tokens[second], `{"type":"fold"}`), http.StatusOK)
	oldVersion := h.state(host.code, host.token)["version"].(float64)
	ended := h.post("/api/rooms/"+host.code+"/end", map[string]any{"token": host.token})
	expectStatus(t, ended, http.StatusOK)
	est := ended.body
	standings := est["standings"].([]any)
	top := standings[0].(map[string]any)
	if est["status"] != "finished" || top["id"] != first || top["place"] != float64(1) || est["winnerId"] != first ||
		top["stack"].(float64) <= standings[1].(map[string]any)["stack"].(float64) {
		t.Fatalf("End Game ranks by chips: %v winner %v", standings, est["winnerId"])
	}

	re := h.post("/api/rooms/"+host.code+"/rematch", map[string]any{"token": host.token})
	expectStatus(t, re, http.StatusOK)
	rs := re.body
	if rs["game"] != "poker" || rs["gameType"] != "poker" || rs["status"] != "lobby" || rs["version"].(float64) <= oldVersion {
		t.Fatalf("rematch: game %v status %v version %v", rs["game"], rs["status"], rs["version"])
	}
	if dig(rs, "settings", "startingStack") != float64(2000) || dig(rs, "settings", "actionSeconds") != float64(30) ||
		dig(rs, "settings", "timeBankSeconds") != float64(60) || dig(rs, "settings", "blindSchedule") != "off" {
		t.Fatalf("rematch settings %v", rs["settings"])
	}
	if len(players(rs)) != 2 || dig(rs, "you", "isHost") != true {
		t.Fatalf("rematch seats %v", rs["players"])
	}
	host.playerID = rs["viewerId"].(string)
	ben.playerID = h.state(host.code, ben.token)["viewerId"].(string)
	tokens = map[string]string{host.playerID: host.token, ben.playerID: ben.token}

	// Cash: all-in until somebody busts.
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"settings","settings":{"style":"cash","startingStack":1000}}`), http.StatusOK)
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"start"}`), http.StatusOK)
	busted := ""
	waitFor(t, "a bust", 10*time.Second, func() bool {
		st := h.state(host.code, host.token)
		for _, p := range players(st) {
			if p["status"] == "busted" {
				busted = p["id"].(string)
				return true
			}
		}
		if id, _ := st["actorId"].(string); id != "" {
			h.pokerAct(host.code, tokens[id], `{"type":"allin"}`)
		}
		return false
	})
	// Hours later the busted player still may rebuy, and the game goes on.
	h.clock.Advance(3 * time.Hour)
	time.Sleep(50 * time.Millisecond)
	bs := h.state(host.code, tokens[busted])
	if bs["status"] != "playing" || dig(bs, "you", "canRebuy") != true || dig(bs, "you", "rebuyUntil") != nil || dig(bs, "level", "bigBlind") != float64(20) {
		t.Fatalf("cash bust: status %v you %v level %v", bs["status"], bs["you"], bs["level"])
	}
	expectStatus(t, h.pokerAct(host.code, tokens[busted], `{"type":"rebuy"}`), http.StatusOK)
	if p := playerByID(h.state(host.code, host.token), busted); p["boughtIn"] != float64(2000) {
		t.Fatalf("after the rebuy %v", p)
	}
	ended = h.post("/api/rooms/"+host.code+"/end", map[string]any{"token": host.token})
	expectStatus(t, ended, http.StatusOK)
	net := 0
	prev := 1 << 30
	for _, raw := range ended.body["standings"].([]any) {
		s := raw.(map[string]any)
		n := num(s["net"])
		if n > prev || n != num(s["stack"])-num(s["boughtIn"]) {
			t.Fatalf("cash standings not by chips won: %v", ended.body["standings"])
		}
		prev, net = n, net+n
	}
	if net != 0 {
		t.Fatalf("chips won sum to %d", net)
	}
}

// allInBot always goes all-in and always rebuys. It also checks that it is
// only ever shown its own seat's view: no one else's face-down cards.
type allInBot struct {
	mu       sync.Mutex
	views    int
	problems []string
}

func (b *allInBot) Decide(_ context.Context, v *holdem.View) (holdem.Command, error) {
	b.mu.Lock()
	b.views++
	if v.You == nil || v.ViewerID != v.You.ID {
		b.problems = append(b.problems, "not a seat's own view")
	}
	for _, p := range v.Players {
		if p.Cards != nil && p.ID != v.ViewerID && !p.Shown {
			b.problems = append(b.problems, "saw "+p.Name+"'s face-down cards")
		}
	}
	b.mu.Unlock()
	if v.You.CanRebuy && !v.You.ToAct {
		return holdem.Command{Type: "rebuy"}, nil
	}
	return holdem.Command{Type: "allin"}, nil
}

// Rebuys: the computer is asked about its rebuy and takes it; a person who
// doesn't answer loses the offer when it expires; the game then finishes.
func TestPokerRebuysAndExpiry(t *testing.T) {
	// Pick a deal where the computer busts first and Ann later: replay the
	// same seats, actions and seed in the engine.
	var seed *[32]byte
	for k := 0; k < 500 && seed == nil; k++ {
		s := [32]byte{byte(k), byte(k >> 8), 42}
		tb := holdem.NewTable("SIM", holdem.Options{Seed: &s, Clock: func() int64 { return 1 }, Timing: &holdem.Timing{RebuyMs: 1000}})
		ann, _ := tb.AddPlayer("Ann", holdem.KindHuman)
		bot, _ := tb.AddPlayer("Alpha", holdem.KindAI)
		_ = tb.PerformAction(ann, json.RawMessage(`{"type":"settings","settings":{"rebuys":1,"blindSchedule":"off"}}`))
		_ = tb.PerformAction(ann, json.RawMessage(`{"type":"start"}`))
		var order []string
		for guard := 0; guard < 200 && tb.Status == holdem.StatusPlaying; guard++ {
			w := tb.Waiting()
			if len(w) == 0 {
				tb.Advance(1)
				continue
			}
			switch {
			case w[0].Kind == "act":
				_ = tb.PerformAction(w[0].PlayerID, json.RawMessage(`{"type":"allin"}`))
			case w[0].PlayerID == bot:
				order = append(order, "bot")
				_ = tb.PerformAction(bot, json.RawMessage(`{"type":"rebuy"}`))
			default:
				order = append(order, "ann")
				_ = tb.PerformAction(ann, json.RawMessage(`{"type":"declineRebuy"}`))
			}
		}
		if strings.Join(order, ",") == "bot,ann" && tb.WinnerID == bot {
			seed = &s
		}
	}
	if seed == nil {
		t.Fatal("no seed found")
	}
	timing := holdem.Timing{RunoutStepMs: 10, HandOverMs: 10, ShowdownMs: 10, RebuyMs: 250}
	bot := &allInBot{}
	h := newHarness(t, func(c *Config) {
		c.Engines = map[string]Engine{GamePoker: PokerEngine{Timing: &timing, Seed: seed}}
		c.PokerBots = func(string, int64) bots.Bot { return bot }
		c.pokerTick = 5 * time.Millisecond
	})
	host := h.pokerGame("Ann", h.device("Ann", "rocket", "198.51.100.151"))
	expectStatus(t, h.post("/api/rooms/"+host.code+"/ai", map[string]any{"token": host.token}), http.StatusOK)
	rec := h.record(host.code, host.token)
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"settings","settings":{"rebuys":1,"blindSchedule":"off"}}`), http.StatusOK)
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"start"}`), http.StatusOK)
	acted := 0.0
	waitFor(t, "the game to finish", 15*time.Second, func() bool {
		st := rec.state()
		if st == nil {
			return false
		}
		if you, _ := st["you"].(map[string]any); you != nil && you["toAct"] == true && st["version"].(float64) != acted {
			acted = st["version"].(float64)
			h.pokerAct(host.code, host.token, `{"type":"allin"}`)
		}
		return st["status"] == "finished"
	})
	st := rec.state()
	botID := ""
	for _, p := range players(st) {
		if p["kind"] == "ai" {
			botID = p["id"].(string)
		}
	}
	rebought, annOut := false, false
	for _, ev := range rec.events() {
		rebought = rebought || (ev["type"] == "rebuy" && ev["playerId"] == botID)
		annOut = annOut || (ev["type"] == "bust" && ev["playerId"] == host.playerID && ev["place"] == float64(2))
	}
	if !rebought || !annOut || st["winnerId"] != botID {
		t.Fatalf("rebought %v, Ann's offer expired %v, winner %v", rebought, annOut, st["winnerId"])
	}
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if bot.views == 0 || len(bot.problems) > 0 {
		t.Fatalf("bot saw %d views; problems %v", bot.views, bot.problems)
	}
}

// Late join: a running poker game deals newcomers in from the next hand when
// lateJoin is on, and refuses them when it is off.
func TestPokerLateJoin(t *testing.T) {
	timing := fastTiming
	h := newHarness(t, withPoker(&timing))
	host := h.pokerGame("Ann", h.device("Ann", "rocket", "198.51.100.161"))
	ben := h.pokerJoin(host.code, "Ben", h.device("Ben", "teapot", "198.51.100.162"))
	h.record(host.code, host.token)
	h.record(host.code, ben.token)
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"settings","settings":{"actionSeconds":0}}`), http.StatusOK)
	expectStatus(t, h.pokerAct(host.code, host.token, `{"type":"start"}`), http.StatusOK)
	cat := h.pokerJoin(host.code, "Cat", h.device("Cat", "key", "198.51.100.163"))
	st := h.state(host.code, cat.token)
	if p := playerByID(st, cat.playerID); p["status"] != "waiting" || p["inHand"] != false || dig(st, "you", "cards") != nil {
		t.Fatalf("late joiner %v", p)
	}
	tokens := map[string]string{host.playerID: host.token, ben.playerID: ben.token, cat.playerID: cat.token}
	waitFor(t, "Cat dealt in", 5*time.Second, func() bool {
		st := h.state(host.code, host.token)
		if id, _ := st["actorId"].(string); id != "" {
			h.pokerAct(host.code, tokens[id], `{"type":"fold"}`)
		}
		return num(st["handNumber"]) >= 2 && playerByID(st, cat.playerID)["inHand"] == true
	})

	other := h.pokerGame("Dee", h.device("Dee", "anchor", "198.51.100.164"))
	h.pokerJoin(other.code, "Eve", h.device("Eve", "bicycle", "198.51.100.165"))
	expectStatus(t, h.pokerAct(other.code, other.token, `{"type":"settings","settings":{"lateJoin":false}}`), http.StatusOK)
	expectStatus(t, h.pokerAct(other.code, other.token, `{"type":"start"}`), http.StatusOK)
	late := h.post("/api/rooms/"+other.code+"/join", map[string]any{"name": "Fay"}, h.device("Fay", "tophat", "198.51.100.166").opt())
	expectError(t, late, http.StatusBadRequest, "joining after the start is turned off")
	if sum := h.get("/api/rooms/" + other.code + "/summary"); dig(sum.body, "openSeats") != float64(0) || num(dig(sum.body, "turn")) != 1 {
		t.Fatalf("summary of a closed running game %s", sum.raw)
	}
}

// 0.10.2: a PC poker table with computers only (no phone yet, so no poker
// host) can change its rules and start; the table still can't bet for anyone.
func TestPokerTableComputersOnly(t *testing.T) {
	h := newHarness(t, withPoker(nil))
	tb := h.post("/api/tables", map[string]any{"gameType": "poker"})
	expectStatus(t, tb, http.StatusCreated)
	code, token := tb.str("code"), tb.str("tableToken")
	for i := 0; i < 2; i++ {
		expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": "easy"}), http.StatusOK)
	}
	settings := dig(h.state(code, token), "settings")
	expectStatus(t, h.act(code, token, map[string]any{"type": "settings", "settings": settings}), http.StatusOK)
	st := h.act(code, token, map[string]any{"type": "start"})
	expectStatus(t, st, http.StatusOK)
	if dig(st.body, "status") != "playing" {
		t.Fatalf("a table of computers must start: %s", st.raw)
	}
	expectStatus(t, h.act(code, token, map[string]any{"type": "fold"}), http.StatusBadRequest)
}
