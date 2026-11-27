package quiz

import (
	"slices"
	"testing"
)

// TestClaimedLatencyIsCapped: the phone reports its own round trip, so a
// doctored phone claiming 2 s and "ms": 0 must not have its taps count as
// instant (uncapped, any tap in the first ~3 s would). The bound trusts at
// most trustLatencyMs: a tap arriving at +1900 counts as at least
// 1900 − 100 − 200 − 150 = 1450, and one at +3100 as 2650.
func TestClaimedLatencyIsCapped(t *testing.T) {
	for _, arrive := range []int64{1900, 3100} {
		g, c, ids := fairClue(t, 2000)
		runToPhase(t, g, c, PhaseAnswer)
		q := g.Clue.Q
		c.now = q.OpenAt + arrive
		if err := answerNow(g, ids[0], q.Answer, 0); err != nil {
			t.Fatal(err)
		}
		want := int(arrive) - trustLatencyMs/2 - trustLatencyMs - trustSlackMs
		if eff := q.tapOf(ids[0]).Effective; eff != want {
			t.Fatalf("claimed 2 s latency, arriving at +%d: counts as %d ms, want %d", arrive, eff, want)
		}
	}
}

// TestClaimedSlownessIsNotFree: a wrong guess sent at once with a huge ms
// must not count as later than the winner (which would make it a free late
// tap). It counts as at most the time since the choices opened.
func TestClaimedSlownessIsNotFree(t *testing.T) {
	g, c, ids := fairClue(t, 50, 50)
	guesser, winner := ids[0], ids[1]
	runToPhase(t, g, c, PhaseAnswer)
	q := g.Clue.Q
	value := g.Clue.Value
	c.now = q.OpenAt + 900
	if err := answerNow(g, guesser, wrongChoice(q), 7999); err != nil {
		t.Fatal(err)
	}
	c.now = q.OpenAt + 2025
	if err := answerNow(g, winner, q.Answer, 2000); err != nil {
		t.Fatal(err)
	}
	runToPhase(t, g, c, PhaseReveal)
	tap := q.tapOf(guesser)
	if tap.Late || tap.Delta != -value || g.Player(guesser).Score != -value {
		t.Fatalf("wrong guess claiming 7999 ms: late %v, delta %d, score %d; want it to lose %d",
			tap.Late, tap.Delta, g.Player(guesser).Score, value)
	}
	if tap.Effective > 900+earlyMs {
		t.Fatalf("counted as %d ms, more than it took to arrive", tap.Effective)
	}
}

// TestGoldNoAnswerCountsWrong: a gold-square finder who never answers loses
// the bet and it counts as a wrong answer (as an unanswered final does).
func TestGoldNoAnswerCountsWrong(t *testing.T) {
	g, c := newGame(t, 11)
	ann := addHuman(t, g, "Ann")
	startGame(t, g)
	runToPhase(t, g, c, PhasePick)
	cat, row := firstOpen(g, true)
	if cat < 0 {
		t.Fatal("no gold square on the board")
	}
	pickCell(t, g, cat, row)
	runToPhase(t, g, c, PhaseBet)
	mustAct(t, g, ann, `{"type":"bet","amount":100}`)
	runToPhase(t, g, c, PhaseReveal)
	if p := g.Player(ann); p.Score != -100 || p.Wrong != 1 || p.Correct != 0 {
		t.Fatalf("score %d, right %d, wrong %d; want -100, 0, 1", p.Score, p.Correct, p.Wrong)
	}
}

// TestLateFinalRevealStaysOneByOne: a timer that fires long after the final
// reveal was due (a paused room, a sleeping PC) reveals one player, then
// times the next from now, so the table still shows them one by one.
func TestLateFinalRevealStaysOneByOne(t *testing.T) {
	g, c, _ := finalGame(t, 100, 200, 300)
	runToPhase(t, g, c, PhaseFinalReveal)
	c.now += 3_600_000
	g.Advance(c.now)
	if g.Status != StatusPlaying || g.Final.Revealed != 1 {
		t.Fatalf("after a 1 h pause: status %s, %d revealed; want one revealed and still playing", g.Status, g.Final.Revealed)
	}
	if g.Final.RevealAt != c.now+finalRevealStepMs || g.PhaseEndsAt != c.now+2*finalRevealStepMs+finalRevealEndMs {
		t.Fatalf("next reveal %d, end %d; want both timed from now", g.Final.RevealAt-c.now, g.PhaseEndsAt-c.now)
	}
	runUntil(t, g, c, func() bool { return g.Status != StatusPlaying })
}

// TestLeaveAfterTheEndWasDue: leaving when the game's end is already due
// finishes it once, with the winner it earned.
func TestLeaveAfterTheEndWasDue(t *testing.T) {
	g, c, ids := finalGame(t, 300)
	ann := ids[0]
	runToPhase(t, g, c, PhaseFinalReveal)
	runUntil(t, g, c, func() bool { return g.Final.Revealed == len(g.Final.Order) })
	c.now = g.PhaseEndsAt + 10_000
	seq := g.EventSeq
	if err := g.RemovePlayer(ann, ann); err != nil {
		t.Fatal(err)
	}
	overs := 0
	for _, e := range g.EventsSince(seq) {
		if e.Type == EvGameOver {
			overs++
		}
	}
	if g.Status == StatusPlaying || overs != 1 || !slices.Equal(g.WinnerIDs, []string{ann}) {
		t.Fatalf("status %s, %d gameOver events, winners %v; want finished once, won by %s", g.Status, overs, g.WinnerIDs, ann)
	}
}

// TestRoundTwoAutoPick: the pick timer's "last category" belongs to round 1's
// board, so round 2's first timed-out pick takes the cheapest cell, leftmost.
func TestRoundTwoAutoPick(t *testing.T) {
	g, c := newGame(t, 3)
	addHuman(t, g, "Ann")
	mustAct(t, g, g.HostID, `{"type":"settings","settings":{"length":"full"}}`)
	startGame(t, g)
	runUntil(t, g, c, func() bool { return g.Round == 2 && g.Phase == PhasePick })
	tick(g, c)
	if g.Clue.Category != 0 || g.Clue.Row != 0 {
		t.Fatalf("round 2 auto-pick (%d, %d), want (0, 0)", g.Clue.Category, g.Clue.Row)
	}
}
