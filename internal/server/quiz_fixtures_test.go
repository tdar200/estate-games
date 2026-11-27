package server

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/quiz"
)

// quizFixtureNames are the screens' fixtures (docs/QUIZ-API.md §9).
var quizFixtureNames = []string{
	"quiz-lobby-table", "quiz-lobby-phone", "quiz-intro-table", "quiz-pick-table", "quiz-pick-phone",
	"quiz-pick-other-phone", "quiz-read-table", "quiz-read-phone", "quiz-answer-table", "quiz-answer-phone",
	"quiz-answered-phone", "quiz-reveal-table", "quiz-reveal-phone", "quiz-gold-table", "quiz-bet-table",
	"quiz-bet-phone", "quiz-roundend-table", "quiz-finalbet-table", "quiz-finalbet-phone",
	"quiz-finalanswer-table", "quiz-finalanswer-phone", "quiz-finalreveal-table", "quiz-finished-table",
	"quiz-finished-phone",
}

// TestQuizFixtures builds the quiz screens' demo and test states
// (web/fixtures/quiz) with the real server and the real question packs:
// every state is what stateFor sends that viewer. One Quick game at a PC
// table (Maya hosting from her phone, Kim, Sam and Jun on theirs, two
// computers) is played on a clock that moves only when the test moves it,
// with a seeded deal, seeded computers and seeded people, so the files come
// out the same every time (except the room code). To rewrite them:
//
//	ESTATE_QUIZ_FIXTURES=web/fixtures/quiz go test ./internal/server -run TestQuizFixtures
//
// (from the repository root). Without the variable the scenes are only built
// and checked: each one's phase and viewer, and that no server-only field
// reaches a client.
func TestQuizFixtures(t *testing.T) {
	clock := &fixtureClock{}
	clock.set(time.Date(2026, 11, 22, 19, 0, 0, 0, time.UTC).UnixMilli())
	seed := [32]byte{22, 11, 31: 0x51}
	h := newHarness(t, func(c *Config) {
		c.Clock = clock.Now
		c.Engines = map[string]Engine{GameQuiz: QuizEngine{Seed: &seed}}
	})
	type entry struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Viewer      string `json:"viewer"`
	}
	var index []entry
	out := os.Getenv("ESTATE_QUIZ_FIXTURES")
	if out != "" && !filepath.IsAbs(out) {
		// go test runs in the package folder; the documented path is from
		// the repository root.
		out = filepath.Join("..", "..", out)
	}
	write := func(name, description, viewer, phase string, state json.RawMessage) {
		t.Helper()
		var probe map[string]any
		if err := json.Unmarshal(state, &probe); err != nil || probe["game"] != "quiz" {
			t.Fatalf("%s: not a quiz state", name)
		}
		role, who := "player", viewer
		if viewer == "table" {
			role, who = "table", ""
		}
		if probe["phase"] != phase || probe["viewerRole"] != role || (role == "player" && probe["viewerName"] != who) {
			t.Fatalf("%s: phase %v for %v %v, want %s for %s", name, probe["phase"], probe["viewerRole"], probe["viewerName"], phase, viewer)
		}
		for _, bad := range []string{`"rng"`, `"plan"`, `"answer":`, `"wrong":[`, `"src"`, `"taps"`, `"bets"`, `"strong"`, `"decideAt"`, `"played":[`} {
			if strings.Contains(string(state), bad) {
				t.Fatalf("%s: server-only field %s in a client state", name, bad)
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

	// ---- The cast: a PC table; Maya (the host player), Kim, Sam and Jun on their phones; two computers.
	tb := h.post("/api/tables", map[string]any{"gameType": "quiz"})
	expectStatus(t, tb, http.StatusCreated)
	code := tb.str("code")
	tokens := map[string]string{"table": tb.str("tableToken")}
	ids := map[string]string{}
	for i, name := range []string{"Maya", "Kim", "Sam", "Jun"} {
		d := h.device(name, []string{"lighthouse", "rocket", "teapot", "bicycle"}[i], fmt.Sprintf("198.51.100.%d", 61+i))
		res := h.post("/api/rooms/"+code+"/join", map[string]any{"name": name}, d.opt())
		expectStatus(t, res, http.StatusOK)
		tokens[name], ids[name] = res.str("token"), res.str("playerId")
	}
	for _, level := range []string{"hard", "normal"} {
		expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": tokens["table"], "difficulty": level}), http.StatusOK)
	}
	maya, kim, sam, jun := ids["Maya"], ids["Kim"], ids["Sam"], ids["Jun"]

	var g *quiz.Game
	var sim *quizSim
	// stage runs fn under the lock with the room's timer stopped (the phones
	// are polling), publishes the result, and returns each viewer's state.
	stage := func(fn func(), viewers ...string) []json.RawMessage {
		t.Helper()
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		r := h.srv.rooms[code]
		r.stopTimer()
		if fn != nil {
			fn()
		}
		now := h.srv.now()
		for _, sess := range r.order {
			sess.lastSeen = now
		}
		h.srv.commit(r)
		r.stopTimer()
		var states []json.RawMessage
		for _, viewer := range viewers {
			st, err := h.srv.stateFor(r, r.sessions[tokens[viewer]])
			if err != nil {
				t.Fatal(err)
			}
			states = append(states, st)
		}
		return states
	}
	name := func(id string) string { return g.Player(id).Name }

	st := stage(nil, "table", "Maya")
	write("quiz-lobby-table", "Lobby on the table screen: Maya's phone (host), three more phones and two computers (Hard, Normal); Quick game, normal reading, 8 s to answer", "table", quiz.PhaseLobby, st[0])
	write("quiz-lobby-phone", "Lobby on Maya's phone: she hosts, the setting chips and START", "Maya", quiz.PhaseLobby, st[1])

	expectStatus(t, h.act(code, tokens["Maya"], map[string]any{"type": "start"}), http.StatusOK)
	st = stage(func() {
		g = quizOf(h.srv.rooms[code]).g
		sim = newQuizSim(t, g, clock, []string{maya, kim, sam, jun})
	}, "table")
	write("quiz-intro-table", "Round 1 starts: the round title, then the six categories one by one", "table", quiz.PhaseIntro, st[0])

	// ---- The board.
	var chooser string
	st = stage(func() {
		sim.playUntil("someone else's pick", func() bool {
			return g.Phase == quiz.PhasePick && g.ClueSeq >= 3 && g.ChooserID != maya
		})
		chooser = name(g.ChooserID)
	}, "Maya")
	write("quiz-pick-other-phone", "Maya's phone while "+chooser+" picks", "Maya", quiz.PhasePick, st[0])

	st = stage(func() {
		sim.playUntil("Maya's pick", func() bool {
			return g.Phase == quiz.PhasePick && g.ClueSeq >= 16 && g.ChooserID == maya
		})
	}, "table", "Maya")
	write("quiz-pick-table", fmt.Sprintf("Maya has control and is picking: %d of 30 clues played, scores on the plates", g.ClueSeq), "table", quiz.PhasePick, st[0])
	write("quiz-pick-phone", "Maya's phone while she picks: the board as big buttons", "Maya", quiz.PhasePick, st[1])

	var clue *quiz.ClueState
	var title string
	st = stage(func() {
		cat, row := sim.cell(3, false)
		sim.pick(maya, cat, row)
		clue, title = g.Clue, g.Boards[0].Categories[cat].Title
		sim.runTo(clue.Q.ReadAt + (clue.Q.OpenAt-clue.Q.ReadAt)*2/5)
	}, "table", "Maya")
	write("quiz-read-table", fmt.Sprintf("%s for %d: the clue is being read; the choices appear when the bar ends", title, clue.Value), "table", quiz.PhaseRead, st[0])
	write("quiz-read-phone", "Maya's phone during the reading: the clue and the bar, no choices yet", "Maya", quiz.PhaseRead, st[1])

	// The choices open. Alpha (Hard) taps a wrong one after 1.5 s and Sam
	// another after 1.9 s; Maya is right after 2.6 s and Kim after 2.8 s,
	// still on its way when Maya's tap is judged, so his is late. Orca
	// (Normal) does not know it and Jun does not answer.
	q := clue.Q
	alpha, orca := sim.computers[0], sim.computers[1]
	st = stage(func() {
		sim.runTo(q.OpenAt)
		g.Player(alpha).Plan = &quiz.Plan{At: q.OpenAt + 1500, Choice: (q.Answer + 1) % 4, Ms: 1500}
		g.Player(orca).Plan = nil
		sim.plan(sam, (q.Answer+2)%4, 1900)
		sim.plan(maya, q.Answer, 2600)
		sim.plan(kim, q.Answer, 2800)
		sim.runTo(q.OpenAt + 2100)
	}, "table", "Maya")
	left := int(math.Round(float64(q.ClosesAt-clock.ms.Load()) / 1000))
	write("quiz-answer-table", fmt.Sprintf("The four choices are open: %s and Sam have tapped (who, not what), %d s left", name(alpha), left), "table", quiz.PhaseAnswer, st[0])
	write("quiz-answer-phone", "Maya's phone with the four choices open", "Maya", quiz.PhaseAnswer, st[1])

	st = stage(func() { sim.runTo(q.OpenAt + 2600 + quiz.UnknownLatency/2) }, "Maya")
	write("quiz-answered-phone", fmt.Sprintf("Maya has tapped %c: locked in, waiting for the reveal", "ABCD"[q.Answer]), "Maya", quiz.PhaseAnswer, st[0])

	st = stage(func() {
		sim.waitFor(func() bool { return g.Phase == quiz.PhaseReveal })
		if clue.WinnerID != maya {
			t.Fatalf("Maya should win the clue: %+v", clue.Q.Taps)
		}
	}, "table", "Maya")
	write("quiz-reveal-table", fmt.Sprintf("Reveal: %s was right; Maya won +%d, %s and Sam lost %d, Kim was right but late", q.Choices[q.Answer], clue.Value, name(alpha), clue.Value), "table", quiz.PhaseReveal, st[0])
	write("quiz-reveal-phone", fmt.Sprintf("Maya's phone at the reveal: right, +%d", clue.Value), "Maya", quiz.PhaseReveal, st[1])

	// Maya keeps control and finds the gold square.
	st = stage(func() {
		sim.waitFor(func() bool { return g.Phase == quiz.PhasePick })
		cat, row := sim.cell(-1, true)
		sim.pick(maya, cat, row)
		clue, title = g.Clue, g.Boards[0].Categories[cat].Title
	}, "table")
	write("quiz-gold-table", fmt.Sprintf("Maya found the gold square under %s for %d", title, clue.Value), "table", quiz.PhaseGold, st[0])

	st = stage(func() { sim.waitFor(func() bool { return g.Phase == quiz.PhaseBet }) }, "table", "Maya")
	write("quiz-bet-table", fmt.Sprintf("Maya is betting on the gold square (%s to %s) before she sees the clue", thousands(g.Bet.Min), thousands(g.Bet.Max)), "table", quiz.PhaseBet, st[0])
	write("quiz-bet-phone", "Maya's phone: the bet stepper with MIN, HALF and MAX", "Maya", quiz.PhaseBet, st[1])

	st = stage(func() {
		sim.bet(maya, max(g.Bet.Min, g.Bet.Max*3/5/100*100))
		sim.waitFor(func() bool { return g.Phase == quiz.PhaseAnswer })
		sim.plan(maya, g.Clue.Q.Answer, 3100)
		sim.playUntil("the end of the board", func() bool { return g.Phase == quiz.PhaseRoundEnd })
	}, "table")
	write("quiz-roundend-table", "Round 1 is over: the scores, the final next", "table", quiz.PhaseRoundEnd, st[0])

	// ---- The final: Kim and Sam bet at once, the computers as they planned;
	// Maya is still deciding.
	var betMax int
	st = stage(func() {
		sim.playUntil("the final's bets", func() bool { return g.Phase == quiz.PhaseFinalBet })
		sim.bet(kim, sim.you(kim).BetMax/2)
		sim.bet(sam, sim.you(sam).BetMax)
		sim.runTo(clock.ms.Load() + 4000)
		betMax = sim.you(maya).BetMax
	}, "table", "Maya")
	write("quiz-finalbet-table", fmt.Sprintf("The final: %s; %d bets are in (amounts secret)", g.Final.Category, len(g.Final.Bets)), "table", quiz.PhaseFinalBet, st[0])
	write("quiz-finalbet-phone", fmt.Sprintf("Maya's phone: her secret final bet, 0 to %s", thousands(betMax)), "Maya", quiz.PhaseFinalBet, st[1])

	mayaBet := betMax*3/5 + 1
	st = stage(func() {
		sim.bet(maya, mayaBet)
		sim.bet(jun, sim.you(jun).BetMax/3)
		sim.waitFor(func() bool { return g.Phase == quiz.PhaseFinalAnswer })
		q = g.Final.Q
		sim.plan(kim, q.Answer, 4200)
		sim.plan(sam, (q.Answer+3)%4, 6100)
		sim.runTo(q.OpenAt + 6500)
	}, "table", "Maya")
	left = int(math.Round(float64(q.ClosesAt-clock.ms.Load()) / 1000))
	write("quiz-finalanswer-table", fmt.Sprintf("The final clue with its four choices; %d answers are in, %d s left", len(q.Taps), left), "table", quiz.PhaseFinalAnswer, st[0])
	write("quiz-finalanswer-phone", fmt.Sprintf("Maya's phone: the final's four choices (she bet %s)", thousands(mayaBet)), "Maya", quiz.PhaseFinalAnswer, st[1])

	st = stage(func() {
		sim.plan(maya, q.Answer, 8800)
		sim.plan(jun, (q.Answer+1)%4, 10400)
		sim.waitFor(func() bool {
			return g.Phase == quiz.PhaseFinalReveal && g.Final.Revealed >= len(g.Final.Order)-2
		})
	}, "table")
	write("quiz-finalreveal-table", fmt.Sprintf("The final reveal, lowest score first: %d of %d revealed", g.Final.Revealed, len(g.Final.Order)), "table", quiz.PhaseFinalReveal, st[0])

	st = stage(func() { sim.waitFor(func() bool { return g.Status == quiz.StatusFinished }) }, "table", "Maya")
	var winners []string
	for _, id := range g.WinnerIDs {
		winners = append(winners, name(id))
	}
	verdict := strings.Join(winners, " and ") + " wins"
	if len(winners) > 1 {
		verdict = strings.Join(winners, " and ") + " share first place"
	}
	write("quiz-finished-table", fmt.Sprintf("Game over: %s on %s; PLAY AGAIN and NEW TABLE", verdict, thousands(g.Player(g.WinnerIDs[0]).Score)), "table", quiz.PhaseFinished, st[0])
	write("quiz-finished-phone", fmt.Sprintf("Maya's phone at the end: place %d of 6", g.Player(maya).Place), "Maya", quiz.PhaseFinished, st[1])

	var names []string
	for _, e := range index {
		names = append(names, e.Name)
	}
	slices.Sort(names)
	want := slices.Sorted(slices.Values(quizFixtureNames))
	if !slices.Equal(names, want) {
		t.Fatalf("fixtures %v, want %v", names, want)
	}
	if out != "" {
		doc, _ := json.MarshalIndent(index, "", "  ")
		if err := os.WriteFile(filepath.Join(out, "index.json"), append(doc, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d quiz fixtures to %s", len(index), out)
	}
}

// fixtureClock is a server clock that moves only when the test moves it, so
// a fixture's times (now, openAt, phaseEndsAt) come out the same every run.
type fixtureClock struct{ ms atomic.Int64 }

func (c *fixtureClock) Now() time.Time { return time.UnixMilli(c.ms.Load()) }
func (c *fixtureClock) set(ms int64)   { c.ms.Store(ms) }

// quizSim plays a fixture game on the engine, under the server lock and on
// the fixture clock. The computers play themselves (their taps drawn by the
// engine); the people tap and bet as a scene scripts it or, between scenes,
// from a seeded stream: each knows a clue with their own chance and has
// their own speed. Every pick is made here, the computers' too, so the gold
// square waits for its scene.
type quizSim struct {
	t         *testing.T
	g         *quiz.Game
	clock     *fixtureClock
	rng       *rand.Rand
	people    []string // seat order
	computers []string // seat order
	knows     map[string]float64
	speed     map[string]int  // ms before a person can tap at all
	decided   map[string]bool // phase/seq/person: they have made up their mind
	taps      []fixtureTap    // the people's taps on their way
}

// fixtureTap is a person's tap: it reaches the server at at (ms + L/2 after
// the choices opened, L the 250 ms every phone without a socket gets).
type fixtureTap struct {
	player          string
	seq, choice, ms int
	at              int64
}

func newQuizSim(t *testing.T, g *quiz.Game, clock *fixtureClock, people []string) *quizSim {
	s := &quizSim{t: t, g: g, clock: clock, rng: rand.New(rand.NewPCG(22, 11)), people: people,
		knows: map[string]float64{}, speed: map[string]int{}, decided: map[string]bool{}}
	for i, id := range people {
		s.knows[id] = []float64{0.8, 0.6, 0.5, 0.4}[i]
		s.speed[id] = []int{900, 1300, 1500, 1700}[i]
	}
	for _, p := range g.Players {
		if p.Kind == quiz.KindAI {
			s.computers = append(s.computers, p.ID)
		}
	}
	return s
}

func (s *quizSim) now() int64 { return s.clock.ms.Load() }

func (s *quizSim) do(id string, c quiz.Command) {
	s.t.Helper()
	if err := s.g.Do(id, c); err != nil {
		s.t.Fatalf("%s %s: %v", s.g.Player(id).Name, c.Type, err)
	}
}

func (s *quizSim) pick(id string, cat, row int) {
	s.t.Helper()
	s.do(id, quiz.Command{Type: "pick", Category: &cat, Row: &row})
}

func (s *quizSim) bet(id string, amount int) {
	s.t.Helper()
	s.do(id, quiz.Command{Type: "bet", Amount: &amount})
}

// you is a player's own panel (their bet range).
func (s *quizSim) you(id string) *quiz.YouView {
	return s.g.View(id, quiz.Viewer{Role: "player"}).You
}

// question is the question being read or answered (a board clue or the
// final) and its seq.
func (s *quizSim) question() (*quiz.Question, int) {
	switch s.g.Phase {
	case quiz.PhaseRead, quiz.PhaseAnswer:
		return s.g.Clue.Q, s.g.Clue.Seq
	case quiz.PhaseFinalRead, quiz.PhaseFinalAnswer:
		return s.g.Final.Q, s.g.Final.Seq
	}
	return nil, 0
}

// cell is an open cell of the board: in the given row (-1: any), a gold
// square or not; random among those.
func (s *quizSim) cell(row int, gold bool) (int, int) {
	s.t.Helper()
	var open [][2]int
	for c, cat := range s.g.Boards[s.g.Round-1].Categories {
		for r, cell := range cat.Cells {
			if !cell.Played && cell.Gold == gold && (row < 0 || r == row) {
				open = append(open, [2]int{c, r})
			}
		}
	}
	if len(open) == 0 {
		if row >= 0 {
			return s.cell(-1, gold)
		}
		s.t.Fatalf("no open cell (gold %v)", gold)
	}
	at := open[s.rng.IntN(len(open))]
	return at[0], at[1]
}

// plan sends a person's tap on the open question, taking ms.
func (s *quizSim) plan(id string, choice, ms int) {
	q, seq := s.question()
	s.taps = append(s.taps, fixtureTap{player: id, seq: seq, choice: choice, ms: ms, at: q.OpenAt + int64(ms+quiz.UnknownLatency/2)})
}

// answer delivers a tap that has reached the server; one for a question
// that is over is dropped, as the server would refuse it.
func (s *quizSim) answer(tp fixtureTap) {
	s.t.Helper()
	q, seq := s.question()
	if q == nil || seq != tp.seq || (s.g.Phase != quiz.PhaseAnswer && s.g.Phase != quiz.PhaseFinalAnswer) {
		return
	}
	s.do(tp.player, quiz.Command{Type: "answer", Clue: &tp.seq, Choice: &tp.choice, Ms: json.RawMessage(strconv.Itoa(tp.ms))})
}

// next is when something next happens: an engine step or a person's tap.
func (s *quizSim) next() int64 {
	due := s.g.NextDeadline()
	for _, tp := range s.taps {
		if due == 0 || tp.at < due {
			due = tp.at
		}
	}
	return due
}

// runTo moves the clock to at, running engine steps and taps as they fall
// due (a tap first when they fall together).
func (s *quizSim) runTo(at int64) {
	s.t.Helper()
	for due := s.next(); due != 0 && due <= at; due = s.next() {
		s.clock.set(max(due, s.now()))
		if i := slices.IndexFunc(s.taps, func(tp fixtureTap) bool { return tp.at <= due }); i >= 0 {
			tp := s.taps[i]
			s.taps = slices.Delete(s.taps, i, i+1)
			s.answer(tp)
		} else {
			s.g.Advance(s.now())
		}
	}
	s.clock.set(max(at, s.now()))
	s.g.Advance(s.now())
}

// waitFor runs the game on, with the people doing nothing more, until cond
// holds.
func (s *quizSim) waitFor(cond func() bool) {
	s.t.Helper()
	for i := 0; !cond(); i++ {
		due := s.next()
		if i > 10000 || due == 0 {
			s.t.Fatalf("the fixture game is stuck in phase %s", s.g.Phase)
		}
		s.runTo(due)
	}
}

// playUntil plays on, the people too, until cond holds.
func (s *quizSim) playUntil(what string, cond func() bool) {
	s.t.Helper()
	for i := 0; !cond(); i++ {
		if i > 100000 || s.g.Status != quiz.StatusPlaying {
			s.t.Fatalf("the fixture game never reached %s (phase %s)", what, s.g.Phase)
		}
		s.act()
		due := s.next()
		if due == 0 {
			s.t.Fatalf("the fixture game is stuck in phase %s", s.g.Phase)
		}
		s.runTo(due)
	}
}

// act is what the people (and every chooser) do at this moment.
func (s *quizSim) act() {
	s.t.Helper()
	g := s.g
	switch g.Phase {
	case quiz.PhasePick:
		cat, row := s.cell(-1, false)
		s.pick(g.ChooserID, cat, row)
	case quiz.PhaseBet:
		if b := g.Bet; !b.Placed && slices.Contains(s.people, b.PlayerID) {
			s.bet(b.PlayerID, b.Min+s.rng.IntN(b.Max-b.Min+1))
		}
	case quiz.PhaseFinalBet:
		for _, id := range s.people {
			if you := s.you(id); you.CanBet {
				s.bet(id, you.BetMin+s.rng.IntN(you.BetMax-you.BetMin+1))
			}
		}
	case quiz.PhaseAnswer, quiz.PhaseFinalAnswer:
		q, seq := s.question()
		for _, id := range s.people {
			key := fmt.Sprint(g.Phase, "/", seq, "/", id)
			if s.decided[key] || !s.eligible(id) || slices.ContainsFunc(s.taps, func(tp fixtureTap) bool { return tp.player == id && tp.seq == seq }) {
				continue
			}
			s.decided[key] = true
			final := g.Phase == quiz.PhaseFinalAnswer
			if !final && s.rng.Float64() >= 0.8 {
				continue // lets this one go
			}
			choice := q.Answer
			if s.rng.Float64() >= s.knows[id] {
				choice = (q.Answer + 1 + s.rng.IntN(3)) % 4
			}
			ms := s.speed[id] + s.rng.IntN(2500)
			if final {
				ms = 3000 + s.rng.IntN(12000)
			}
			if q.OpenAt+int64(ms+quiz.UnknownLatency/2) < q.ClosesAt {
				s.plan(id, choice, ms)
			}
		}
	}
}

// eligible reports whether a person may still answer the open question.
func (s *quizSim) eligible(id string) bool {
	q, _ := s.question()
	if slices.ContainsFunc(q.Taps, func(t quiz.Tap) bool { return t.PlayerID == id }) {
		return false
	}
	return s.g.Phase == quiz.PhaseFinalAnswer || slices.Contains(s.g.Clue.Eligible, id)
}

// thousands writes n with thousands separators, as the screens do.
func thousands(n int) string {
	if n < 0 {
		return "-" + thousands(-n)
	}
	digits := strconv.Itoa(n)
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
}
