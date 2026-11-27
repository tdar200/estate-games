package quiz

import (
	"math"
	mrand "math/rand/v2"
	"slices"
	"testing"
)

// phone is a fake phone for the fairness tests: Latency is its round trip.
type phone struct {
	id      string
	latency int
}

// tapPlan is one tap on its way: the person's true reaction and when the
// tap reaches the server.
type tapPlan struct {
	p        phone
	reaction float64
	arrival  int64
	ms       int
}

// TestFairAcrossLatencies: three phones at 5, 60 and 250 ms round trips,
// whose people react with the same distribution, each win about a third of
// many clues, and the fastest person wins every one.
func TestFairAcrossLatencies(t *testing.T) {
	const clues = 3000
	r := mrand.New(mrand.NewPCG(3, 4))
	wins := map[string]int{}
	fastestWon, played := 0, 0
	for game := 0; played < clues; game++ {
		g, c := newGame(t, byte(game))
		phones := []phone{{addHuman(t, g, "Fast"), 5}, {addHuman(t, g, "Mid"), 60}, {addHuman(t, g, "Slow"), 250}}
		for _, p := range phones {
			g.SetLatency(p.id, p.latency, true)
		}
		startGame(t, g)
		for g.Status == StatusPlaying && played < clues {
			runUntil(t, g, c, func() bool { return g.Phase == PhasePick || g.Status != StatusPlaying })
			if g.Status != StatusPlaying {
				break
			}
			cat, row := firstOpen(g, false)
			if cat < 0 {
				g.EndGame() // only the gold square is left
				break
			}
			pickCell(t, g, cat, row)
			runToPhase(t, g, c, PhaseAnswer)
			q, seq := g.Clue.Q, g.Clue.Seq
			var taps []tapPlan
			for _, p := range phones {
				reaction := max(300, 1500+300*r.NormFloat64())
				syncErr := (r.Float64()*2 - 1) * float64(p.latency) / 4 // the phone's clock is off by up to a quarter of its round trip
				oneWay := float64(p.latency) / 2 * (0.8 + 0.4*r.Float64())
				arrival := q.OpenAt + int64(math.Round(syncErr+reaction+oneWay))
				taps = append(taps, tapPlan{p, reaction, arrival, int(math.Round(reaction))})
			}
			slices.SortFunc(taps, func(a, b tapPlan) int { return int(a.arrival - b.arrival) })
			for _, tp := range taps {
				c.now = tp.arrival
				g.Advance(c.now)
				if g.Phase != PhaseAnswer || g.Clue.Seq != seq {
					// Judged already: fine only if this person was slower
					// than the winner (their tap could not have won).
					if w := q.tapOf(g.Clue.WinnerID); w == nil || tp.ms <= w.Effective {
						t.Fatalf("an honest %d ms tap from a %d ms phone arrived after the clue was judged", tp.ms, tp.p.latency)
					}
					continue
				}
				if err := answerNow(g, tp.p.id, q.Answer, tp.ms); err != nil {
					t.Fatal(err)
				}
			}
			runUntil(t, g, c, func() bool { return g.Clue.Decided })
			fastest := slices.MinFunc(taps, func(a, b tapPlan) int { return a.ms - b.ms })
			if g.Clue.WinnerID == fastest.p.id {
				fastestWon++
			}
			wins[g.Clue.WinnerID]++
			played++
		}
	}
	names := map[string]string{"p1": "5 ms", "p2": "60 ms", "p3": "250 ms"}
	for id, name := range names {
		share := float64(wins[id]) / clues
		t.Logf("%s phone won %.1f%%", name, 100*share)
		if math.Abs(share-1.0/3) > 0.03 {
			t.Errorf("the %s phone won %.1f%% of %d clues, want 33.3%% ± 3", name, 100*share, clues)
		}
	}
	// Ties to the millisecond go by arrival, so allow for a handful.
	if fastestWon < clues*99/100 {
		t.Errorf("the fastest person won %d of %d clues", fastestWon, clues)
	}
}

// fairClue seats players with the given latencies and opens a board clue.
func fairClue(t *testing.T, latencies ...int) (*Game, *clock, []string) {
	t.Helper()
	g, c := newGame(t, 30)
	var ids []string
	for i, l := range latencies {
		id := addHuman(t, g, string(rune('A'+i)))
		g.SetLatency(id, l, true)
		ids = append(ids, id)
	}
	startGame(t, g)
	runToPhase(t, g, c, PhasePick)
	cat, row := firstOpen(g, false)
	pickCell(t, g, cat, row)
	return g, c, ids
}

// TestDoctoredClient: a phone that always claims "ms": 1 gains at most its
// latency + 150 ms over the truth.
func TestDoctoredClient(t *testing.T) {
	for _, latency := range []int{5, 60, 250} {
		for _, reaction := range []int64{300, 900, 2500} {
			g, c, ids := fairClue(t, latency, 250)
			runToPhase(t, g, c, PhaseAnswer)
			q := g.Clue.Q
			c.now = q.OpenAt + reaction + int64(latency/2)
			if err := answerNow(g, ids[0], q.Answer, 1); err != nil {
				t.Fatal(err)
			}
			eff := int64(q.tapOf(ids[0]).Effective)
			if gain := reaction - eff; gain > int64(latency)+trustSlackMs {
				t.Errorf("latency %d, reaction %d: counted as %d ms, a gain of %d (at most %d)", latency, reaction, eff, gain, latency+trustSlackMs)
			}
		}
	}
}

func TestEarlyAndInvalidTaps(t *testing.T) {
	g, c, ids := fairClue(t, 250, 250)
	q := g.Clue.Q
	c.now = q.OpenAt - earlyMs - 1
	expectErr(t, answerNow(g, ids[0], q.Answer, 0), "answers are not open yet")
	// Within 50 ms of the opening (this phone's clock runs a little ahead):
	// accepted, and the choices open for everyone.
	c.now = q.OpenAt - earlyMs
	if err := answerNow(g, ids[0], q.Answer, 0); err != nil {
		t.Fatal(err)
	}
	if g.Phase != PhaseAnswer || q.tapOf(ids[0]).Effective != 0 {
		t.Fatalf("phase %s, tap %+v", g.Phase, q.tapOf(ids[0]))
	}

	// Missing or nonsense ms: the arrival estimate (arrival − L/2).
	for _, ms := range []string{``, `,"ms":-5`, `,"ms":"fast"`, `,"ms":9999999`, `,"ms":null`} {
		g, c, ids := fairClue(t, 100, 250)
		runToPhase(t, g, c, PhaseAnswer)
		q := g.Clue.Q
		c.now = q.OpenAt + 1000
		mustAct(t, g, ids[0], `{"type":"answer","clue":%d,"choice":%d%s}`, g.Clue.Seq, q.Answer, ms)
		if tap := q.tapOf(ids[0]); tap.Reported != -1 || tap.Effective != 950 {
			t.Errorf("ms %q: reported %d, effective %d; want -1 and 950", ms, tap.Reported, tap.Effective)
		}
	}
}

// TestDecidingWait: after the first correct tap the engine waits for the
// slowest phone still able to answer (clamped to 100–400 ms), so a faster
// person on a slower connection still wins; offline phones are not waited
// for.
func TestDecidingWait(t *testing.T) {
	cases := []struct {
		latencies []int
		online    []bool
		want      int64
	}{
		{[]int{5, 5}, []bool{true, true}, 100},
		{[]int{5, 250}, []bool{true, true}, 300},
		{[]int{5, 1000}, []bool{true, true}, 400},
		{[]int{5, 1000}, []bool{true, false}, 100},
	}
	for _, tc := range cases {
		g, c, ids := fairClue(t, tc.latencies...)
		for i, id := range ids {
			g.SetLatency(id, tc.latencies[i], tc.online[i])
		}
		runToPhase(t, g, c, PhaseAnswer)
		q := g.Clue.Q
		c.now = q.OpenAt + 1000
		if err := answerNow(g, ids[0], q.Answer, 998); err != nil {
			t.Fatal(err)
		}
		if got := q.DecideAt - c.now; got != tc.want {
			t.Errorf("latencies %v online %v: wait %d, want %d", tc.latencies, tc.online, got, tc.want)
		}
	}

	// The slow phone's person was faster: their tap arrives 75 ms after the
	// fast phone's, inside the wait, and wins.
	g, c, ids := fairClue(t, 5, 250)
	runToPhase(t, g, c, PhaseAnswer)
	q := g.Clue.Q
	c.now = q.OpenAt + 1003
	if err := answerNow(g, ids[0], q.Answer, 1000); err != nil {
		t.Fatal(err)
	}
	c.now = q.OpenAt + 950 + 125
	if err := answerNow(g, ids[1], q.Answer, 950); err != nil {
		t.Fatal(err)
	}
	runUntil(t, g, c, func() bool { return g.Clue.Decided })
	if g.Clue.WinnerID != ids[1] {
		t.Fatalf("winner %s, want the faster person on the slower phone", g.Clue.WinnerID)
	}
}

// TestLateTaps: taps that arrive within the wait but count after the
// winner's are marked late and change nothing, right or wrong; a faster
// wrong tap loses; a tap arriving after the judging is refused.
func TestLateTaps(t *testing.T) {
	g, c, ids := fairClue(t, 250, 250, 250, 250, 250)
	runToPhase(t, g, c, PhaseAnswer)
	q, value := g.Clue.Q, g.Clue.Value
	taps := []struct {
		id     string
		choice int
		ms     int
	}{ // in arrival order (each arrives ms + 125 after the opening)
		{ids[3], wrongChoice(q), 600},
		{ids[0], q.Answer, 1000}, // first right: judged 300 ms after it arrives
		{ids[1], wrongChoice(q), 1100},
		{ids[2], q.Answer, 1250},
	}
	for _, tp := range taps {
		c.now = q.OpenAt + int64(tp.ms) + 125
		if err := answerNow(g, tp.id, tp.choice, tp.ms); err != nil {
			t.Fatal(err)
		}
	}
	runToPhase(t, g, c, PhaseReveal)
	expectErr(t, answerNow(g, ids[4], q.Answer, 1300), "that clue is over")
	want := map[string]int{ids[0]: value, ids[1]: 0, ids[2]: 0, ids[3]: -value}
	for id, score := range want {
		if g.Player(id).Score != score {
			t.Errorf("%s scored %d, want %d", id, g.Player(id).Score, score)
		}
	}
	v := g.View("", Viewer{Role: "table"})
	var order []string
	for _, r := range v.Clue.Results {
		order = append(order, r.PlayerID)
		if late := r.PlayerID == ids[1] || r.PlayerID == ids[2]; r.Late != late {
			t.Errorf("%s late %v, want %v", r.PlayerID, r.Late, late)
		}
	}
	if !slices.Equal(order, []string{ids[3], ids[0], ids[1], ids[2]}) {
		t.Errorf("results in order %v, want time order", order)
	}
	if *v.Clue.WinnerID != ids[0] || *v.Clue.AnswerIndex != q.Answer {
		t.Errorf("winner %s, answer %d", *v.Clue.WinnerID, *v.Clue.AnswerIndex)
	}
}
