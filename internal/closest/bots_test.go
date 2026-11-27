package closest

import (
	"bytes"
	"math"
	"slices"
	"testing"
)

// TestBotBetsIgnoreAnswer: computers never read the answer when betting.
// Each game is snapshotted as the bets open and restored twice, once with
// the answer moved; the bets (every level's, the timer's too) come out the
// same.
func TestBotBetsIgnoreAnswer(t *testing.T) {
	compared := 0
	for seed := range byte(60) {
		c := &clock{now: start}
		g := NewGame("BOTS", Options{Seed: seedOf(seed), Clock: c.read, Library: testLibrary(t)})
		addHuman(t, g, "Ann")
		addComputer(t, g, "Easy", LevelEasy)
		addComputer(t, g, "Normal", LevelNormal)
		addComputer(t, g, "Hard", LevelHard)
		addComputer(t, g, "Hard 2", LevelHard)
		mustAct(t, g, g.HostID, `{"type":"settings","settings":{"fill":false}}`)
		startGame(t, g)
		for g.Status == StatusPlaying {
			runUntil(t, g, c, func() bool { return g.Phase == PhaseBet || g.Status != StatusPlaying })
			if g.Status != StatusPlaying {
				break
			}
			for _, p := range g.Players {
				if p.computer() {
					p.Score = int(seed) % 5 // so stakes come into it
				}
			}
			snap, err := g.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			bets := func(answer func(q *Question) int) []Bet {
				cc := &clock{now: c.now}
				h, err := Restore(snap, Options{Clock: cc.read, Library: testLibrary(t)})
				if err != nil {
					t.Fatal(err)
				}
				q := h.Cur.Question
				q.Answer = answer(q)
				runUntil(t, h, cc, func() bool { return h.Phase != PhaseBet })
				return h.Cur.Bets
			}
			same := bets(func(q *Question) int { return q.Answer })
			moved := bets(func(q *Question) int {
				if q.Answer-q.Min > q.Max-q.Answer {
					return q.Min + 1
				}
				return q.Max - 1
			})
			if !slices.EqualFunc(same, moved, betsEqual) {
				t.Fatalf("seed %d question %d: bets changed with the answer:\n%+v\n%+v", seed, g.QuestionNo, same, moved)
			}
			compared++
			runUntil(t, g, c, func() bool { return g.Phase != PhaseBet })
		}
	}
	if compared < 200 {
		t.Fatalf("only %d bet phases compared", compared)
	}
}

func betsEqual(a, b Bet) bool {
	return a.PlayerID == b.PlayerID && a.Chips == b.Chips && a.Stake == b.Stake && a.Auto == b.Auto &&
		(a.StakeSlot == nil) == (b.StakeSlot == nil) && (a.StakeSlot == nil || *a.StakeSlot == *b.StakeSlot)
}

// TestComputerGuesses: guesses stay in range, look typed (plain ones have at
// most two significant figures), and Hard guesses closer than Normal, Normal
// closer than Easy.
func TestComputerGuesses(t *testing.T) {
	g, _ := newGame(t, 30)
	lib := testLibrary(t)
	errs := map[string][]float64{}
	exact := map[string]int{}
	for _, level := range Levels {
		p := &Player{ID: "x", Kind: KindAI, Level: level}
		for i := range 4000 {
			q := lib.Questions[i%len(lib.Questions)]
			v := g.computerGuess(p, q, g.rng.NormFloat64())
			if v < q.Min || v > q.Max {
				t.Fatalf("%s guessed %d for %s (range %d–%d)", level, v, q.ID, q.Min, q.Max)
			}
			if v == q.Answer {
				exact[level]++
			}
			if q.Format == FormatYear {
				errs[level] = append(errs[level], math.Abs(float64(v-q.Answer))/q.Spread)
				continue
			}
			if v != q.Answer && significant(v) > 2 {
				t.Fatalf("%s guessed %d (more than two significant figures)", level, v)
			}
			errs[level] = append(errs[level], math.Abs(math.Log(float64(max(v, 1))/float64(q.Answer)))/math.Log1p(q.Spread/100))
		}
	}
	median := func(xs []float64) float64 {
		s := slices.Clone(xs)
		slices.Sort(s)
		return s[len(s)/2]
	}
	e, n, h := median(errs[LevelEasy]), median(errs[LevelNormal]), median(errs[LevelHard])
	t.Logf("median error in spreads: easy %.2f, normal %.2f, hard %.2f; exact %v", e, n, h, exact)
	if !(h < n && n < e) {
		t.Fatalf("levels out of order: easy %.2f, normal %.2f, hard %.2f", e, n, h)
	}
	if !(exact[LevelHard] > exact[LevelEasy]) {
		t.Fatalf("exact guesses %v", exact)
	}
}

// significant counts n's significant figures (1,800 has 2).
func significant(n int) int {
	for n > 0 && n%10 == 0 {
		n /= 10
	}
	digits := 0
	for ; n > 0; n /= 10 {
		digits++
	}
	return digits
}

// TestComputerTiming: computers guess 4–12 s in and bet 3–8 s in, and
// never later than 2 s before the timer.
func TestComputerTiming(t *testing.T) {
	g, c := newGame(t, 31)
	addHuman(t, g, "Ann")
	for range 7 {
		addComputer(t, g, "Bot", LevelNormal)
	}
	mustAct(t, g, g.HostID, `{"type":"settings","settings":{"guessSeconds":20,"betSeconds":15}}`)
	startGame(t, g)
	for range 5 {
		runToPhase(t, g, c, PhaseGuess)
		opened := c.now
		for _, p := range g.Players {
			if p.computer() && (p.Plan == nil || p.Plan.At < opened+4000 || p.Plan.At > min(opened+12000, g.PhaseEndsAt-2000)) {
				t.Fatalf("guess plan %+v, opened %d", p.Plan, opened)
			}
		}
		runToPhase(t, g, c, PhaseBet)
		opened = c.now
		for _, p := range g.Players {
			if p.computer() && (p.Plan == nil || p.Plan.At < opened+3000 || p.Plan.At > opened+8000) {
				t.Fatalf("bet plan %+v, opened %d", p.Plan, opened)
			}
		}
		runToPhase(t, g, c, PhaseReveal)
	}
}

// TestSnapshotRestore: a game restored mid-bet plays on exactly as the
// original does, computers included.
func TestSnapshotRestore(t *testing.T) {
	g, c := newGame(t, 32)
	ann := addHuman(t, g, "Ann")
	addComputer(t, g, "Easy", LevelEasy)
	addComputer(t, g, "Hard", LevelHard)
	startGame(t, g) // fill adds one Normal
	runToPhase(t, g, c, PhaseGuess)
	guess(t, g, ann, g.Cur.Question.Min)
	runToPhase(t, g, c, PhaseBet)
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	c2 := &clock{now: c.now}
	h, err := Restore(snap, Options{Clock: c2.read, Library: testLibrary(t)})
	if err != nil {
		t.Fatal(err)
	}
	if h.Cur.Question != h.Questions[h.Cur.No-1] {
		t.Fatal("the current question is a copy")
	}
	for g.Status == StatusPlaying {
		at := g.NextDeadline()
		if h.NextDeadline() != at {
			t.Fatalf("deadlines %d and %d", at, h.NextDeadline())
		}
		c.now, c2.now = at, at
		g.Advance(at)
		h.Advance(at)
	}
	a, _ := g.Snapshot()
	b, _ := h.Snapshot()
	if !bytes.Equal(a, b) {
		t.Fatal("the restored game played differently")
	}
	if _, err := Restore([]byte(`{"status":"playing","phase":"bet","settings":{"length":"quick","guessSeconds":30,"betSeconds":25}}`), Options{}); err == nil {
		t.Fatal("restored a game in the bets with no question")
	}
	if _, err := Restore([]byte(`{"settings":{"length":"long"}}`), Options{}); err == nil {
		t.Fatal("restored bad settings")
	}
}
