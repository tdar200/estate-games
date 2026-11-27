package closest

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
)

// start is the fake clock's first reading (any fixed time will do).
const start = int64(1_800_000_000_000)

var (
	testLibOnce sync.Once
	testLib     *Library
	testLibErr  error
)

// testLibrary is the engine's own made-up packs (testdata/packs): 24
// questions over 15 topics, 6 of them years, one pack under CC BY-SA for the
// credits.
func testLibrary(t testing.TB) *Library {
	t.Helper()
	testLibOnce.Do(func() { testLib, testLibErr = LoadLibrary(os.DirFS("testdata/packs")) })
	if testLibErr != nil {
		t.Fatal(testLibErr)
	}
	return testLib
}

// clock is a controlled game clock.
type clock struct{ now int64 }

func (c *clock) read() int64 { return c.now }

func seedOf(n byte) *[32]byte {
	var s [32]byte
	s[0], s[31] = n, 0x3c
	return &s
}

// newGame makes a seeded game on a controlled clock with the test packs.
func newGame(t testing.TB, seed byte) (*Game, *clock) {
	t.Helper()
	c := &clock{now: start}
	return NewGame("TEST", Options{Seed: seedOf(seed), Clock: c.read, Library: testLibrary(t)}), c
}

func addHuman(t testing.TB, g *Game, name string) string {
	t.Helper()
	id, err := g.AddPlayer(name, KindHuman)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func addComputer(t testing.TB, g *Game, name, level string) string {
	t.Helper()
	id, err := g.AddComputer(name, level)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// act sends an action as JSON (so the decoding is tested too).
func act(g *Game, playerID, format string, args ...any) error {
	return g.PerformAction(playerID, json.RawMessage(fmt.Sprintf(format, args...)))
}

func mustAct(t testing.TB, g *Game, playerID, format string, args ...any) {
	t.Helper()
	if err := act(g, playerID, format, args...); err != nil {
		t.Fatalf("%s: %s: %v", playerID, fmt.Sprintf(format, args...), err)
	}
}

func expectErr(t testing.TB, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// tick moves the clock to the next deadline and runs it; false when nothing
// is timed.
func tick(g *Game, c *clock) bool {
	at := g.NextDeadline()
	if at == 0 {
		return false
	}
	c.now = max(c.now, at)
	g.Advance(c.now)
	return true
}

// runUntil steps until cond holds (fatal if the game stops first).
func runUntil(t testing.TB, g *Game, c *clock, cond func() bool) {
	t.Helper()
	for i := 0; !cond(); i++ {
		if i > 5000 || !tick(g, c) {
			t.Fatalf("stuck: %s", g)
		}
	}
}

func runToPhase(t testing.TB, g *Game, c *clock, phase string) {
	t.Helper()
	runUntil(t, g, c, func() bool { return g.Phase == phase })
}

// startGame starts g as its host (or the table when no person sits).
func startGame(t testing.TB, g *Game) {
	t.Helper()
	host := g.HostID
	if host == "" {
		host = TableHost
	}
	mustAct(t, g, host, `{"type":"start"}`)
}

// madeUp is a plain question with the given answer (range ×10 either way).
func madeUp(id string, answer int) *Question {
	return &Question{ID: id, Topic: "numbers", Text: "How many made-up things are there?", Answer: answer,
		Unit: "things", Format: FormatPlain, Min: max(0, answer/10), Max: answer * 10, Spread: 50,
		Fact: "There are " + thousands(answer) + " made-up things.", Src: []string{"https://example.org/a", "https://example.org/b"}}
}

// setQuestion replaces question no (1-based) before it is asked.
func setQuestion(g *Game, no int, q *Question) { g.Questions[no-1] = q }

// guess has a player guess value on the current question.
func guess(t testing.TB, g *Game, playerID string, value int) {
	t.Helper()
	mustAct(t, g, playerID, `{"type":"guess","q":%d,"value":%d}`, g.Cur.Seq, value)
}

// bet has a player place both chips and a stake on the current question.
func bet(t testing.TB, g *Game, playerID string, a, b, stake, stakeSlot int) {
	t.Helper()
	mustAct(t, g, playerID, `{"type":"bet","q":%d,"chips":[%d,%d],"stake":%d,"stakeSlot":%d}`, g.Cur.Seq, a, b, stake, stakeSlot)
}

// resultOf is a player's result on the revealed question.
func resultOf(t testing.TB, g *Game, playerID string) Result {
	t.Helper()
	for _, r := range g.Cur.Results {
		if r.PlayerID == playerID {
			return r
		}
	}
	t.Fatalf("no result for %s", playerID)
	return Result{}
}

// viewOf is a player's (or, with "", the table's) state.
func viewOf(t testing.TB, g *Game, viewerID string) View {
	t.Helper()
	role := "player"
	if viewerID == "" {
		role = "table"
	}
	data, err := g.PublicState(viewerID, Viewer{Role: role})
	if err != nil {
		t.Fatal(err)
	}
	var v View
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// fourPeople seats Ann (host), Ben, Cat and Dan, starts with fill off, puts
// qs in as the first questions and moves to question 1's guesses.
func fourPeople(t testing.TB, seed byte, qs ...*Question) (*Game, *clock, []string) {
	t.Helper()
	g, c := newGame(t, seed)
	var ids []string
	for _, name := range []string{"Ann", "Ben", "Cat", "Dan"} {
		ids = append(ids, addHuman(t, g, name))
	}
	mustAct(t, g, ids[0], `{"type":"settings","settings":{"fill":false}}`)
	startGame(t, g)
	for i, q := range qs {
		setQuestion(g, i+1, q)
	}
	runToPhase(t, g, c, PhaseGuess)
	return g, c, ids
}
