package quiz

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
)

// Fair answering (docs/QUIZ-API.md §6): the fastest person wins, not the
// fastest connection. Every phone shows the choices at the same server
// instant (Question.OpenAt, by synced clock) and reports how long its person
// took (ms). The server trusts that report only as far as the network can
// explain: a tap that arrived A ms after the choices opened, from a player
// with latency L, took about estimate = A − L/2, and it counts as at least
// estimate − L − 150 ms whatever the phone claims. So an honest slow
// connection costs nothing and a doctored phone gains at most L + 150 ms.
// The first correct tap is not judged at once: the engine waits long enough
// for an earlier tap from the slowest phone still able to answer to arrive.

// UnknownLatency is a player's latency in ms until the server measures one
// (the server also reports it for a player whose phone has sent no round
// trip yet).
const UnknownLatency = 250

const (
	earlyMs      = 50   // a tap may arrive this long before the choices open (clocks sync only so well)
	trustSlackMs = 150  // how much faster than the network explains a reported time may be
	maxLatency   = 2000 // latencies are capped here (a phone that slow cannot play anyway)
	// trustLatencyMs caps the latency the timing bound believes. The phone
	// reports its own round trip, so a doctored one could claim 2 s and have
	// any tap in the first 3 s count as instant; past this cap a slow phone
	// is judged by its arrival time like everyone else (§6.3).
	trustLatencyMs = 200
	minWaitMs      = 100    // the wait after a first correct tap: at least
	maxWaitMs      = 400    // and at most (a very slow phone may not hold everyone up longer)
	waitSlackMs    = 50     // added to the slowest latency
	maxReportedMs  = 600000 // a reported time above this is nonsense and counts as missing
)

var (
	errNotOpen  = errors.New("answers are not open yet")
	errClueOver = errors.New("that clue is over")
	errAnswered = errors.New("you have already answered")
)

// answer is the "answer" action: a tap on a board clue or the final.
func (g *Game) answer(p *Player, c Command, now int64) error {
	if g.Status != StatusPlaying {
		return errNotPlaying
	}
	if c.Clue == nil || c.Choice == nil {
		return errors.New("say which clue and which answer")
	}
	if *c.Choice < 0 || *c.Choice > 3 {
		return errors.New("choose one of the four answers")
	}
	if g.Round == finalRound {
		return g.answerFinal(p, *c.Clue, *c.Choice, reportedMs(c.Ms), now)
	}
	cl := g.Clue
	if cl == nil || cl.Seq != *c.Clue {
		return errClueOver
	}
	switch g.Phase {
	case PhaseGold, PhaseBet:
		return errNotOpen
	case PhaseRead:
		if now < cl.Q.OpenAt-earlyMs {
			return errNotOpen
		}
	case PhaseAnswer:
	default:
		return errClueOver
	}
	// Everyone playing may answer a board clue; only a gold square narrows
	// it to its finder.
	if !slices.Contains(cl.Eligible, p.ID) {
		return errors.New("only the gold square finder answers this one")
	}
	if cl.Q.tapOf(p.ID) != nil {
		return errAnswered
	}
	if g.Phase == PhaseRead {
		g.openClue(now) // a tap just before the opening: this phone's clock runs a little ahead
	}
	g.tapClue(p, *c.Choice, reportedMs(c.Ms), now, now)
	return nil
}

func (g *Game) answerFinal(p *Player, seq, choice, reported int, now int64) error {
	f := g.Final
	if f == nil || f.Seq != seq {
		return errClueOver
	}
	switch g.Phase {
	case PhaseFinalIntro, PhaseFinalBet:
		return errNotOpen
	case PhaseFinalRead:
		if now < f.Q.OpenAt-earlyMs {
			return errNotOpen
		}
	case PhaseFinalAnswer:
	default:
		return errClueOver
	}
	if f.Q.tapOf(p.ID) != nil {
		return errAnswered
	}
	if g.Phase == PhaseFinalRead {
		g.openFinal(now)
	}
	g.tapFinal(p, choice, reported, now, now)
	return nil
}

// reportedMs reads the phone's ms: a number from 0 to maxReportedMs,
// rounded; anything else (null included: JSON.stringify writes NaN as null)
// is -1 (missing), and the arrival time is used.
func reportedMs(raw json.RawMessage) int {
	var ms *float64
	if len(raw) == 0 || json.Unmarshal(raw, &ms) != nil || ms == nil || *ms < 0 || *ms > maxReportedMs {
		return -1
	}
	return int(math.Round(*ms))
}

// effectiveMs is the time a tap counts as (§6.3), with L capped at
// trustLatencyMs: estimate = sinceOpen − L/2; the reported ms, but never less
// than estimate − L − 150 and never more than the time since the choices
// opened (plus the early allowance), so claiming to be slow cannot turn a
// wrong guess into a free late tap; the estimate when nothing usable was
// reported; never below 0.
func effectiveMs(reported int, sinceOpen int64, latency int) int {
	l := float64(min(latency, trustLatencyMs))
	estimate := float64(sinceOpen) - l/2
	eff := estimate
	if reported >= 0 {
		eff = min(max(float64(reported), estimate-l-trustSlackMs), float64(sinceOpen+earlyMs))
	}
	return int(math.Round(max(0, eff)))
}

// addTap records a tap with its timing.
func (q *Question) addTap(p *Player, choice, reported int, arrival int64) Tap {
	lat := p.latency()
	t := Tap{PlayerID: p.ID, Choice: choice, Reported: reported, Arrival: arrival, Latency: lat,
		Effective: effectiveMs(reported, arrival-q.OpenAt, lat), Correct: choice == q.Answer}
	q.Taps = append(q.Taps, t)
	return t
}

// tapRecord is a tap as records keep it.
func (q *Question) tapRecord(t Tap) TapRecord {
	r := TapRecord{PlayerID: t.PlayerID, Choice: t.Choice, Correct: t.Correct, Arrival: int(t.Arrival - q.OpenAt),
		Latency: t.Latency, Effective: t.Effective}
	if t.Reported >= 0 {
		ms := t.Reported
		r.Ms = &ms
	}
	return r
}

// tapClue takes a tap on the board clue (arrival is when it reached the
// server; for a computer, when its planned tap is due).
func (g *Game) tapClue(p *Player, choice, reported int, arrival, now int64) {
	q := g.Clue.Q
	first := !q.hasCorrect()
	t := q.addTap(p, choice, reported, arrival)
	g.emit(Event{Type: EvTap, PlayerID: p.ID})
	if t.Correct && first {
		q.DecideAt = arrival + g.wait()
	}
	g.checkClueAllIn(arrival, now)
}

// checkClueAllIn judges early once nobody else can answer: at once with no
// correct tap, or within the short wait after the last tap with one (§6.4).
func (g *Game) checkClueAllIn(at, now int64) {
	q := g.Clue.Q
	if len(g.remaining()) > 0 {
		return
	}
	if q.hasCorrect() {
		q.DecideAt = min(q.DecideAt, at+g.wait())
		return
	}
	g.judgeClue(now)
}

func (g *Game) tapFinal(p *Player, choice, reported int, arrival, now int64) {
	g.Final.Q.addTap(p, choice, reported, arrival)
	g.emit(Event{Type: EvTap, PlayerID: p.ID})
	g.checkFinalAllIn(now)
}

// checkFinalAllIn starts the reveal once everyone playing has answered.
func (g *Game) checkFinalAllIn(now int64) {
	if len(g.remaining()) == 0 {
		g.startFinalReveal(now)
	}
}

// remaining is who may still answer the open question: players still
// playing who are eligible and have not tapped. A computer decides when the
// choices open, so one with no planned tap will not answer and is not
// waited for.
func (g *Game) remaining() []*Player {
	var q *Question
	var eligible []string
	if g.Round == finalRound {
		q, eligible = g.Final.Q, g.playingIDs()
	} else {
		q, eligible = g.Clue.Q, g.Clue.Eligible
	}
	var out []*Player
	for _, id := range eligible {
		p := g.Player(id)
		if p == nil || p.Left || q.tapOf(id) != nil || (p.computer() && p.Plan == nil) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// wait is how long the engine waits after a first correct tap (and after
// the close, for taps on their way): clamp(Lmax + 50, 100, 400) ms, Lmax the
// highest latency among those who may still answer and are online.
func (g *Game) wait() int64 {
	lmax := 0
	for _, p := range g.remaining() {
		if p.Online {
			lmax = max(lmax, p.latency())
		}
	}
	return int64(min(max(lmax+waitSlackMs, minWaitMs), maxWaitMs))
}

// judgeClue decides the clue (§6.4): the correct tap with the smallest
// effective time wins the value (a gold square: the bet) and control; wrong
// taps faster than it lose the value; taps after it are late and change
// nothing. With no correct tap every wrong tap loses, and a gold square's
// finder loses the bet even without answering.
func (g *Game) judgeClue(now int64) {
	c := g.Clue
	q := c.Q
	c.Decided = true
	stake := c.Value
	if c.Gold {
		stake = g.Bet.Amount
	}
	slices.SortStableFunc(q.Taps, func(a, b Tap) int {
		if a.Effective != b.Effective {
			return a.Effective - b.Effective
		}
		if a.Arrival != b.Arrival {
			return int(a.Arrival - b.Arrival)
		}
		return g.seat(a.PlayerID) - g.seat(b.PlayerID)
	})
	winner := slices.IndexFunc(q.Taps, func(t Tap) bool { return t.Correct })
	for i := range q.Taps {
		t := &q.Taps[i]
		p := g.Player(t.PlayerID)
		switch {
		case i == winner:
			t.Delta = stake
			p.Correct++
			g.emit(Event{Type: EvRight, PlayerID: p.ID, Value: stake})
		case winner >= 0 && t.Effective >= q.Taps[winner].Effective:
			t.Late = true
		default:
			t.Delta = -stake
			p.Wrong++
			g.emit(Event{Type: EvWrong, PlayerID: p.ID, Value: stake})
			g.logf("%s loses %d", p.Name, stake)
		}
		p.Score += t.Delta
	}
	answer := q.Choices[q.Answer]
	if winner >= 0 {
		w := g.Player(q.Taps[winner].PlayerID)
		c.WinnerID = w.ID
		if !w.Left {
			g.ChooserID = w.ID
		}
		g.logf("%s is right: %s, +%d", w.Name, answer, stake)
	} else {
		if f := g.Player(c.Finder); c.Gold && len(q.Taps) == 0 && !f.Left {
			f.Score -= stake
			f.Wrong++
			g.emit(Event{Type: EvWrong, PlayerID: f.ID, Value: stake})
			g.logf("%s did not answer and loses %d", f.Name, stake)
		}
		g.emit(Event{Type: EvNoAnswer})
		g.logf("Nobody got it: the answer was %s", answer)
	}
	records := make([]TapRecord, len(q.Taps))
	for i, t := range q.Taps {
		records[i] = q.tapRecord(t)
	}
	g.emit(Event{Type: EvReveal, Clue: c.Seq, Taps: records})
	wait := int64(revealMs)
	if c.Gold || winner < 0 {
		wait = revealLongMs
	}
	g.setPhase(PhaseReveal, now+wait)
}
