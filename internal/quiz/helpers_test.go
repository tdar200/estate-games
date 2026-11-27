package quiz

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

// testLibrary is the engine's own small pack set (testdata/packs): 14
// categories over 13 topics (two geography categories, so dealing must skip
// a topic), 3 finals, and one CC BY-SA category for the credits.
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
	s[0], s[31] = n, 0x5a
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
			t.Fatalf("stuck in phase %s (status %s)", g.Phase, g.Status)
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

// pickCell has the chooser pick cell (cat, row).
func pickCell(t testing.TB, g *Game, cat, row int) {
	t.Helper()
	mustAct(t, g, g.ChooserID, `{"type":"pick","category":%d,"row":%d}`, cat, row)
}

// firstOpen finds an open cell that is (gold) or is not (!gold) a gold square.
func firstOpen(g *Game, gold bool) (int, int) {
	for r := 0; r < boardRows; r++ {
		for c, bc := range g.board().Categories {
			if cell := bc.Cells[r]; cell.open() && cell.Gold == gold {
				return c, r
			}
		}
	}
	return -1, -1
}

// answerNow taps choice on the current clue (or the final) with reported ms.
func answerNow(g *Game, playerID string, choice, ms int) error {
	seq := 0
	if g.Round == finalRound {
		seq = g.Final.Seq
	} else if g.Clue != nil {
		seq = g.Clue.Seq
	}
	return act(g, playerID, `{"type":"answer","clue":%d,"choice":%d,"ms":%d}`, seq, choice, ms)
}

// question is the open question (board clue or final).
func question(g *Game) *Question {
	if g.Round == finalRound {
		return g.Final.Q
	}
	return g.Clue.Q
}

func wrongChoice(q *Question) int { return (q.Answer + 1) % 4 }

// viewOf is a player's (or, with "", the table's) state as JSON text.
func viewOf(t testing.TB, g *Game, viewerID string) string {
	t.Helper()
	role := "player"
	if viewerID == "" {
		role = "table"
	}
	data, err := g.PublicState(viewerID, Viewer{Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
