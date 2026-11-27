package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/closest"
)

// closestFixtureNames are the screens' fixtures (docs/CLOSEST-API.md §8).
var closestFixtureNames = []string{
	"closest-lobby-table", "closest-lobby-phone", "closest-intro-table", "closest-guess-table",
	"closest-guess-phone", "closest-guess-year-phone", "closest-guessed-phone", "closest-lineup-table",
	"closest-lineup-phone", "closest-bet-table", "closest-bet-phone", "closest-bet-8-phone",
	"closest-betin-phone", "closest-chips-table", "closest-reveal-table", "closest-reveal-phone",
	"closest-reveal-tie-table", "closest-reveal-nobody-table", "closest-reveal-oneslot-table",
	"closest-last-guess-table", "closest-finished-table", "closest-finished-phone",
	"closest-lineup-big-table", "closest-chips-pile-table",
}

// TestClosestFixtures builds the Near Thing screens' demo and test
// states (web/fixtures/closest) with the real server: every state is what
// stateFor sends that viewer. The questions are the engine's made-up test
// questions, never the real packs, because fixtures are served publicly.
// Two games are played on a clock that moves only when the test moves it,
// with a seeded deal and seeded computers, every person's guess and bet
// scripted, so the files come out the same every time (except the room
// codes):
//
//   - Game A, a Quick game at a PC table: Maya (hosting from her phone), Kim,
//     Sam and Jun on theirs, and two computers (Hard, Normal). Question 2 is
//     the full walk through one question, 3 a year with a tie, 4 nobody
//     guesses, 5 one slot, 7 the last (double) question.
//   - Game B, eight phones: the worst cases (eight 7-digit slots, eight
//     slots on a phone with a stake to set, every chip on one slot).
//
// To rewrite them:
//
//	ESTATE_CLOSEST_FIXTURES=web/fixtures/closest go test ./internal/server -run TestClosestFixtures
//
// (from the repository root). Without the variable the scenes are only built
// and checked: each one's phase and viewer, and that no server-only field or
// early secret reaches a client.
func TestClosestFixtures(t *testing.T) {
	clock := &fixtureClock{}
	clock.set(time.Date(2026, 11, 22, 19, 0, 0, 0, time.UTC).UnixMilli())
	type entry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Viewer      string `json:"viewer"`
	}
	var index []entry
	out := os.Getenv("ESTATE_CLOSEST_FIXTURES")
	if out != "" && !filepath.IsAbs(out) {
		// go test runs in the package folder; the documented path is from
		// the repository root.
		out = filepath.Join("..", "..", out)
	}
	if out != "" {
		if err := os.MkdirAll(out, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, description, viewer, phase string, state json.RawMessage) {
		t.Helper()
		var probe map[string]any
		if err := json.Unmarshal(state, &probe); err != nil || probe["game"] != "closest" {
			t.Fatalf("%s: not a closest state", name)
		}
		role, who := "player", viewer
		if viewer == "table" {
			role, who = "table", ""
		}
		if probe["phase"] != phase || probe["viewerRole"] != role || (role == "player" && probe["viewerName"] != who) {
			t.Fatalf("%s: phase %v for %v %v, want %s for %s", name, probe["phase"], probe["viewerRole"], probe["viewerName"], phase, viewer)
		}
		bad := []string{`"rng"`, `"plan"`, `"played":[`, `"spread"`, `"defining"`, `"guesses"`, `"bets"`, `"afterMs"`, `"auto"`, `"questions"`, `"current"`, `"openedAt"`}
		if phase != closest.PhaseReveal && phase != closest.PhaseFinished {
			bad = append(bad, `"answer":`, `"fact"`, `"src"`, `"results"`)
		}
		for _, b := range bad {
			if strings.Contains(string(state), b) {
				t.Fatalf("%s: server-only or early field %s in a client state", name, b)
			}
		}
		index = append(index, entry{name, description, viewer})
		if out == "" {
			return
		}
		doc, _ := json.MarshalIndent(map[string]any{"state": json.RawMessage(state), "viewer": viewer, "meta": map[string]any{"description": description}}, "", "  ")
		if err := os.WriteFile(filepath.Join(out, name+".json"), append(doc, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	closestFixtureGameA(t, clock, write)
	clock.set(time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC).UnixMilli())
	closestFixtureGameB(t, clock, write)

	var names []string
	for _, e := range index {
		names = append(names, e.Name)
	}
	slices.Sort(names)
	want := slices.Sorted(slices.Values(closestFixtureNames))
	if !slices.Equal(names, want) {
		t.Fatalf("fixtures %v, want %v", names, want)
	}
	if out != "" {
		doc, _ := json.MarshalIndent(index, "", "  ")
		if err := os.WriteFile(filepath.Join(out, "index.json"), append(doc, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d closest fixtures to %s", len(index), out)
	}
}

type closestWrite func(name, description, viewer, phase string, state json.RawMessage)

// closestFixtureLibrary is the made-up test questions with these ids, in
// this order: a fixture game is dealt exactly these (Quick takes 7).
func closestFixtureLibrary(t *testing.T, ids ...string) *closest.Library {
	t.Helper()
	all, err := closestTestLibrary()
	if err != nil {
		t.Fatal(err)
	}
	lib := &closest.Library{}
	for _, id := range ids {
		i := slices.IndexFunc(all.Questions, func(q *closest.Question) bool { return q.ID == id })
		if i < 0 {
			t.Fatalf("no test question %s", id)
		}
		lib.Questions = append(lib.Questions, all.Questions[i])
	}
	return lib
}

// closestFixtureRoom is a fixture game's room: a PC table and the people on
// their phones.
type closestFixtureRoom struct {
	t      *testing.T
	h      *harness
	code   string
	tokens map[string]string // "table" and each person's name
	ids    map[string]string // each person's player id
}

func newClosestFixtureRoom(t *testing.T, clock *fixtureClock, lib *closest.Library, seed [32]byte, people, tokenKinds []string, ip int) *closestFixtureRoom {
	t.Helper()
	h := newHarness(t, func(c *Config) {
		c.Clock = clock.Now
		c.Engines = map[string]Engine{GameClosest: ClosestEngine{Seed: &seed, Library: lib}}
	})
	tb := h.post("/api/tables", map[string]any{"gameType": "closest"})
	expectStatus(t, tb, http.StatusCreated)
	fr := &closestFixtureRoom{t: t, h: h, code: tb.str("code"), tokens: map[string]string{"table": tb.str("tableToken")}, ids: map[string]string{}}
	for i, name := range people {
		d := h.device(name, tokenKinds[i], fmt.Sprintf("198.51.100.%d", ip+i))
		res := h.post("/api/rooms/"+fr.code+"/join", map[string]any{"name": name}, d.opt())
		expectStatus(t, res, http.StatusOK)
		fr.tokens[name], fr.ids[name] = res.str("token"), res.str("playerId")
	}
	return fr
}

// stage runs fn under the lock with the room's timer stopped (the phones
// are polling), publishes the result, and returns each viewer's state.
func (fr *closestFixtureRoom) stage(fn func(), viewers ...string) []json.RawMessage {
	fr.t.Helper()
	s := fr.h.srv
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rooms[fr.code]
	r.stopTimer()
	if fn != nil {
		fn()
	}
	now := s.now()
	for _, sess := range r.order {
		sess.lastSeen = now
	}
	s.commit(r)
	r.stopTimer()
	var states []json.RawMessage
	for _, viewer := range viewers {
		st, err := s.stateFor(r, r.sessions[fr.tokens[viewer]])
		if err != nil {
			fr.t.Fatal(err)
		}
		states = append(states, st)
	}
	return states
}

func (fr *closestFixtureRoom) game() *closest.Game { return closestOf(fr.h.srv.rooms[fr.code]).g }

// closestSim plays a fixture game on the engine, under the server lock and
// on the fixture clock: the computers play themselves (unless a scene plans
// their guess), the people as the scenes script them.
type closestSim struct {
	t         *testing.T
	g         *closest.Game
	clock     *fixtureClock
	betSeq    int   // the question whose bets opened at betOpened
	betOpened int64 // when its bets opened
}

func (s *closestSim) now() int64 { return s.clock.ms.Load() }

func (s *closestSim) do(id string, c closest.Command) {
	s.t.Helper()
	if err := s.g.Do(id, c); err != nil {
		s.t.Fatalf("%s %s: %v", s.g.Player(id).Name, c.Type, err)
	}
}

// guess has a person guess at this moment.
func (s *closestSim) guess(id string, value int) {
	s.t.Helper()
	seq, v := s.g.Cur.Seq, float64(value)
	s.do(id, closest.Command{Type: "guess", Q: &seq, Value: &v})
}

// bet has a person put chips on the slots holding guesses a and b (values),
// with the stake on a's slot.
func (s *closestSim) bet(id string, a, b, stake int) {
	s.t.Helper()
	seq, sa, sb := s.g.Cur.Seq, s.slot(a), s.slot(b)
	s.do(id, closest.Command{Type: "bet", Q: &seq, Chips: []int{sa, sb}, Stake: &stake, StakeSlot: &sa})
}

// slot is the index of the slot holding a guess of value.
func (s *closestSim) slot(value int) int {
	s.t.Helper()
	i := slices.IndexFunc(s.g.Cur.Slots, func(sl closest.Slot) bool { return sl.Value == value })
	if i < 0 {
		s.t.Fatalf("no slot %d", value)
	}
	return i
}

// plan sets a computer's next move: its guess (value) or its bet (value
// unused) at ms after the phase began.
func (s *closestSim) plan(id string, ms int64, value int) {
	s.g.Player(id).Plan = &closest.Plan{At: s.opened() + ms, Value: value}
}

// opened is when the current phase began (its timer minus its length, for
// the guess and bet phases; the scenes only time things there).
func (s *closestSim) opened() int64 {
	switch s.g.Phase {
	case closest.PhaseGuess:
		return s.g.Cur.OpenedAt
	case closest.PhaseBet:
		return s.betOpened
	}
	return s.now()
}

// runTo moves the clock to at, running engine steps as they fall due.
func (s *closestSim) runTo(at int64) {
	s.t.Helper()
	for due := s.g.NextDeadline(); due != 0 && due <= at; due = s.g.NextDeadline() {
		s.clock.set(max(due, s.now()))
		s.g.Advance(s.now())
		s.noteBetOpen()
	}
	s.clock.set(max(at, s.now()))
}

// after moves the clock to ms after the current guess or bet phase began.
func (s *closestSim) after(ms int64) { s.runTo(s.opened() + ms) }

// waitFor runs the game on, with the people doing nothing more, until cond
// holds.
func (s *closestSim) waitFor(cond func() bool) {
	s.t.Helper()
	for i := 0; !cond(); i++ {
		due := s.g.NextDeadline()
		if i > 10000 || due == 0 {
			s.t.Fatalf("the fixture game is stuck in phase %s", s.g.Phase)
		}
		s.runTo(due)
	}
}

func (s *closestSim) phase(phase string) {
	s.t.Helper()
	s.waitFor(func() bool { return s.g.Phase == phase })
}

// noteBetOpen remembers when the bets opened (the engine keeps only the
// deadline, which the hurry-up moves).
func (s *closestSim) noteBetOpen() {
	if s.g.Phase == closest.PhaseBet && s.betSeq != s.g.Cur.Seq {
		s.betSeq, s.betOpened = s.g.Cur.Seq, s.now()
	}
}

// play plays a question through: the people guess as given (an absent
// person does not guess), the computers as they planned; then each person
// who guessed bets both chips on the slot of whichever guess bets names.
func (s *closestSim) play(guesses map[string]int, bets map[string]int) {
	s.t.Helper()
	s.phase(closest.PhaseGuess)
	s.after(2000)
	for _, id := range s.order(guesses) {
		s.guess(id, guesses[id])
	}
	s.waitFor(func() bool { return s.g.Phase == closest.PhaseBet || s.g.Phase == closest.PhaseReveal })
	if s.g.Phase == closest.PhaseBet {
		s.after(2500)
		for _, id := range s.order(bets) {
			s.bet(id, bets[id], bets[id], 0)
		}
	}
	s.phase(closest.PhaseReveal)
}

// order is the people in a scene's map in seat order.
func (s *closestSim) order(m map[string]int) []string {
	var ids []string
	for _, p := range s.g.Players {
		if _, ok := m[p.ID]; ok {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

func secondsLeft(g *closest.Game, now int64) int {
	return int(math.Round(float64(g.PhaseEndsAt-now) / 1000))
}

// closestFixtureGameA is the walk through a Quick game at a PC table.
func closestFixtureGameA(t *testing.T, clock *fixtureClock, write closestWrite) {
	order := []string{"made-up-1/01", "made-up-1/07", "made-up-1/02", "made-up-2/03", "made-up-1/04", "made-up-2/06", "made-up-1/12"}
	lib := closestFixtureLibrary(t, order...)
	fr := newClosestFixtureRoom(t, clock, lib, [32]byte{22, 11, 31: 0xC1}, []string{"Maya", "Kim", "Sam", "Jun"}, []string{"lighthouse", "rocket", "teapot", "bicycle"}, 61)
	h, code, tokens := fr.h, fr.code, fr.tokens
	for _, level := range []string{"hard", "normal"} {
		expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": tokens["table"], "difficulty": level}), http.StatusOK)
	}
	maya, kim, sam, jun := fr.ids["Maya"], fr.ids["Kim"], fr.ids["Sam"], fr.ids["Jun"]

	st := fr.stage(nil, "table", "Maya")
	write("closest-lobby-table", "Lobby on the table screen: Maya's phone (host), three more phones and two computers (Hard, Normal); Quick game, 30 s to guess, 25 s to bet", "table", closest.PhaseLobby, st[0])
	write("closest-lobby-phone", "Lobby on Maya's phone: she hosts, the setting chips and START", "Maya", closest.PhaseLobby, st[1])

	expectStatus(t, h.act(code, tokens["Maya"], map[string]any{"type": "start"}), http.StatusOK)
	var g *closest.Game
	var sim *closestSim
	var alpha, orca string
	st = fr.stage(func() {
		g = fr.game()
		sim = &closestSim{t: t, g: g, clock: clock}
		// The deal is shuffled; put the questions in the order the scenes want.
		slices.SortFunc(g.Questions, func(a, b *closest.Question) int { return slices.Index(order, a.ID) - slices.Index(order, b.ID) })
		for _, p := range g.Players {
			if p.Kind == closest.KindAI && alpha == "" {
				alpha = p.ID
			} else if p.Kind == closest.KindAI {
				orca = p.ID
			}
		}
	}, "table")
	write("closest-intro-table", "The game starts: 7 questions, the last one pays double", "table", closest.PhaseIntro, st[0])
	name := func(id string) string { return g.Player(id).Name }

	// ---- Question 1, played through: everyone scores something, so the
	// stakes are open on question 2.
	fr.stage(func() {
		sim.play(map[string]int{maya: 150, kim: 120, sam: 200, jun: 90}, map[string]int{maya: 150, kim: 150, sam: 150, jun: 150})
		sim.phase(closest.PhaseGuess)
	})
	if g.Player(maya).Score < 3 {
		t.Fatalf("Maya should have points to stake: %d", g.Player(maya).Score)
	}

	// ---- Question 2, the walk through. Kim guesses after 3 s, Alpha (Hard)
	// after 5.2 s, Sam after 6.5 s; Orca (Normal) will at 9.1 s.
	st = fr.stage(func() {
		sim.plan(alpha, 5200, 3000)
		sim.plan(orca, 9100, 1800)
		sim.after(3000)
		sim.guess(kim, 2500)
		sim.after(6500)
		sim.guess(sam, 4800)
		sim.after(8000)
	}, "table", "Maya")
	q := g.Cur.Question
	write("closest-guess-table", fmt.Sprintf("Question 2: %q; Kim, %s and Sam have guessed (who, not what), %d s left", q.Text, name(alpha), secondsLeft(g, sim.now())), "table", closest.PhaseGuess, st[0])
	write("closest-guess-phone", fmt.Sprintf("Maya's phone with the number pad open (%s to %s %s)", thousands(q.Min), thousands(q.Max), q.Unit), "Maya", closest.PhaseGuess, st[1])

	st = fr.stage(func() {
		sim.after(11000)
		sim.guess(maya, 3600)
	}, "Maya")
	write("closest-guessed-phone", fmt.Sprintf("Maya has locked in 3,600 (one change left); only Jun is still to guess, so the timer has dropped to %d s", secondsLeft(g, sim.now())), "Maya", closest.PhaseGuess, st[0])

	st = fr.stage(func() {
		sim.after(14000)
		sim.guess(jun, 12000)
	}, "table", "Maya")
	write("closest-lineup-table", "Everyone has guessed: the six guesses line up low to high as slots, paying 4 3 2 2 3 4", "table", closest.PhaseLineup, st[0])
	write("closest-lineup-phone", "Maya's phone during the line-up: her guess, 3,600, is the fourth slot", "Maya", closest.PhaseLineup, st[1])

	// The bets: Kim at 2 s (one chip on 3,000 with 2 points staked, one on
	// his own), Orca (Normal) at 4.5 s, Sam at 5 s; Alpha (Hard) will at 9 s.
	st = fr.stage(func() {
		sim.phase(closest.PhaseBet)
		sim.plan(orca, 4500, 0)
		sim.plan(alpha, 9000, 0)
		sim.after(2000)
		sim.bet(kim, 3000, 2500, min(2, g.Player(kim).Score))
		sim.after(5000)
		sim.bet(sam, 4800, 3000, 0)
		sim.after(6000)
	}, "table", "Maya")
	you := g.View(maya, closest.Viewer{Role: "player"}).You
	write("closest-bet-table", fmt.Sprintf("The bets: Kim, %s and Sam are in (where, secret), %d s left", name(orca), secondsLeft(g, sim.now())), "table", closest.PhaseBet, st[0])
	write("closest-bet-phone", fmt.Sprintf("Maya's phone: six slots, both chips start on her own guess, a stake of 0 to %d", you.StakeMax), "Maya", closest.PhaseBet, st[1])

	st = fr.stage(func() {
		sim.after(10000)
		sim.bet(maya, 3000, 3600, min(2, g.Player(maya).Score))
	}, "Maya")
	write("closest-betin-phone", fmt.Sprintf("Maya has bet: a chip on 3,000 with 2 points staked, a chip on her own 3,600; only Jun is left, %d s", secondsLeft(g, sim.now())), "Maya", closest.PhaseBet, st[0])

	st = fr.stage(func() {
		sim.after(13000)
		sim.bet(jun, 12000, 12000, 0)
	}, "table")
	write("closest-chips-table", "Every chip lands at once: one disc per player in their colour, ×2 for both chips, stakes shown", "table", closest.PhaseChips, st[0])

	st = fr.stage(func() { sim.phase(closest.PhaseReveal) }, "table", "Maya")
	var mayaDelta int
	for _, r := range g.Cur.Results {
		if r.PlayerID == maya {
			mayaDelta = r.Delta
		}
	}
	write("closest-reveal-table", fmt.Sprintf("The answer is %s %s: %s's 3,000 is closest (off by 100) and Maya's 3,600 second; the payouts and the fact", thousands(q.Answer), q.Unit, name(alpha)), "table", closest.PhaseReveal, st[0])
	write("closest-reveal-phone", fmt.Sprintf("Maya's phone at the reveal: second closest, her stake won, +%d", mayaDelta), "Maya", closest.PhaseReveal, st[1])

	// ---- Question 3, a year: Maya's pad (no separators), then a tie.
	st = fr.stage(func() {
		sim.phase(closest.PhaseGuess)
		sim.plan(alpha, 4100, 1800)
		sim.plan(orca, 7300, 1700)
		sim.after(2500)
		sim.guess(kim, 1792)
		sim.after(3000)
	}, "Maya")
	write("closest-guess-year-phone", fmt.Sprintf("Maya's pad on a year question: %q (no thousands separator, no unit)", g.Cur.Question.Text), "Maya", closest.PhaseGuess, st[0])
	st = fr.stage(func() {
		sim.guess(maya, 1780)
		sim.after(5000)
		sim.guess(sam, 1750)
		sim.guess(jun, 1820)
		sim.phase(closest.PhaseBet)
		sim.after(3000)
		sim.bet(maya, 1780, 1780, 1)
		sim.bet(kim, 1792, 1800, 0)
		sim.bet(sam, 1780, 1792, 0)
		sim.bet(jun, 1820, 1792, 0)
		sim.phase(closest.PhaseReveal)
	}, "table")
	if len(g.Cur.Winners) != 2 {
		t.Fatalf("question 3 should be a tie: winners %v", g.Cur.Winners)
	}
	write("closest-reveal-tie-table", fmt.Sprintf("A tie: the answer is %d, and Maya's 1780 and Kim's 1792 are both off by 6, so both win (no second place)", g.Cur.Question.Answer), "table", closest.PhaseReveal, st[0])

	// ---- Question 4: nobody guesses.
	st = fr.stage(func() {
		sim.phase(closest.PhaseGuess)
		g.Player(alpha).Plan, g.Player(orca).Plan = nil, nil
		sim.phase(closest.PhaseReveal)
	}, "table")
	if !g.Cur.Nobody {
		t.Fatal("question 4 should have no guesses")
	}
	write("closest-reveal-nobody-table", "Nobody guessed: straight to the answer and the fact, no points", "table", closest.PhaseReveal, st[0])

	// ---- Question 5: Kim and Orca both guess 400, nobody else guesses: one
	// slot, no betting, it wins.
	st = fr.stage(func() {
		sim.phase(closest.PhaseGuess)
		g.Player(alpha).Plan = nil
		sim.plan(orca, 6200, 400)
		sim.after(3500)
		sim.guess(kim, 400)
		sim.phase(closest.PhaseReveal)
	}, "table")
	if len(g.Cur.Slots) != 1 {
		t.Fatalf("question 5 should have one slot: %v", g.Cur.Slots)
	}
	write("closest-reveal-oneslot-table", fmt.Sprintf("One slot (Kim and %s both guessed 400): no betting, and it wins", name(orca)), "table", closest.PhaseReveal, st[0])

	// ---- Question 6, played through; question 7, the last, pays double.
	st = fr.stage(func() {
		sim.play(map[string]int{maya: 21000, kim: 15000, sam: 30000, jun: 9000}, map[string]int{maya: 21000, kim: 15000, sam: 21000, jun: 21000})
		sim.phase(closest.PhaseGuess)
		sim.after(4000)
		sim.guess(sam, 60000)
		sim.after(7000)
	}, "table")
	write("closest-last-guess-table", fmt.Sprintf("The last question, paying double: %q; %d s left", g.Cur.Question.Text, secondsLeft(g, sim.now())), "table", closest.PhaseGuess, st[0])

	st = fr.stage(func() {
		sim.after(9000)
		sim.guess(maya, 45000)
		sim.guess(kim, 38000)
		sim.guess(jun, 80000)
		sim.phase(closest.PhaseBet)
		sim.after(3000)
		sim.bet(maya, 45000, 38000, min(3, g.Player(maya).Score))
		sim.bet(kim, 38000, 45000, 0)
		sim.bet(sam, 45000, 45000, 0)
		sim.bet(jun, 80000, 45000, 0)
		sim.waitFor(func() bool { return g.Status == closest.StatusFinished })
	}, "table", "Maya")
	var winners []string
	for _, id := range g.WinnerIDs {
		winners = append(winners, name(id))
	}
	verdict := strings.Join(winners, " and ") + " wins"
	if len(winners) > 1 {
		verdict = strings.Join(winners, " and ") + " share first place"
	}
	write("closest-finished-table", fmt.Sprintf("Game over: %s on %d; PLAY AGAIN and NEW TABLE", verdict, g.Player(g.WinnerIDs[0]).Score), "table", closest.PhaseFinished, st[0])
	write("closest-finished-phone", fmt.Sprintf("Maya's phone at the end: place %d of 6", g.Player(maya).Place), "Maya", closest.PhaseFinished, st[1])
}

// closestFixtureGameB is eight phones: the worst cases for the screens.
func closestFixtureGameB(t *testing.T, clock *fixtureClock, write closestWrite) {
	order := []string{"made-up-1/10", "made-up-1/03", "made-up-1/05", "made-up-1/08", "made-up-1/11", "made-up-1/14", "made-up-1/15"}
	lib := closestFixtureLibrary(t, order...)
	people := []string{"Ava", "Ben", "Cleo", "Dev", "Eli", "Fay", "Gus", "Hal"}
	fr := newClosestFixtureRoom(t, clock, lib, [32]byte{22, 11, 2, 31: 0xC1}, people, tokenKinds, 71)
	h, code, tokens := fr.h, fr.code, fr.tokens
	expectStatus(t, h.act(code, tokens["table"], map[string]any{"type": "start"}), http.StatusOK)
	var g *closest.Game
	var sim *closestSim
	ids := make([]string, len(people))
	for i, p := range people {
		ids[i] = fr.ids[p]
	}

	// Question 1: eight different 7-digit guesses.
	big := []int{1_200_000, 2_500_000, 3_750_000, 4_100_000, 5_600_000, 6_800_000, 8_250_000, 9_900_000}
	st := fr.stage(func() {
		g = fr.game()
		sim = &closestSim{t: t, g: g, clock: clock}
		slices.SortFunc(g.Questions, func(a, b *closest.Question) int { return slices.Index(order, a.ID) - slices.Index(order, b.ID) })
		sim.phase(closest.PhaseGuess)
		for i, id := range ids {
			sim.after(int64(2000 + 900*i))
			sim.guess(id, big[i])
		}
	}, "table")
	write("closest-lineup-big-table", "The widest line: eight different 7-digit guesses, 1,200,000 to 9,900,000, paying 5 4 3 2 2 3 4 5", "table", closest.PhaseLineup, st[0])

	// Everyone bets both chips on the lowest slot, which wins: everyone has
	// points to stake on question 2.
	fr.stage(func() {
		sim.phase(closest.PhaseBet)
		sim.after(3000)
		for _, id := range ids {
			sim.bet(id, big[0], big[0], 0)
		}
		sim.phase(closest.PhaseReveal)
	})

	// Question 2: eight slots on a phone, with a stake to set.
	guesses := []int{900, 2000, 3500, 4800, 5000, 7500, 12000, 40000}
	st = fr.stage(func() {
		sim.phase(closest.PhaseGuess)
		for i, id := range ids {
			sim.after(int64(2500 + 800*i))
			sim.guess(id, guesses[i])
		}
		sim.phase(closest.PhaseBet)
		sim.after(1500)
	}, "Ava")
	write("closest-bet-8-phone", fmt.Sprintf("Ava's phone with eight slots to bet on and a stake of 0 to %d", g.View(ids[0], closest.Viewer{Role: "player"}).You.StakeMax), "Ava", closest.PhaseBet, st[0])

	// Every chip on Eli's 5,000, every stake on it too.
	st = fr.stage(func() {
		for i, id := range ids {
			sim.after(int64(3000 + 700*i))
			sim.bet(id, 5000, 5000, 1+i%3)
		}
	}, "table")
	write("closest-chips-pile-table", "The biggest pile: all eight players put both chips and a stake (1 to 3) on Eli's 5,000", "table", closest.PhaseChips, st[0])
}
