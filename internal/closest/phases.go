package closest

import (
	"fmt"
	"slices"
	"strings"
)

// The phase machine (docs/CLOSEST-API.md §4). Every timer lives in the game:
// NextDeadline says when the next step is due and Advance runs it. Steps are
// phase timers and computers' moves.

// NextDeadline is the earliest time Advance has work to do, or 0 when
// nothing is timed (lobby, finished).
func (g *Game) NextDeadline() int64 {
	at, run := g.nextStep()
	if run == nil {
		return 0
	}
	return at
}

// Advance runs every step due at now, in time order, and reports whether
// anything changed (the server then publishes the new state). A phase that
// starts late starts from now, so a server that slept (a restart) never
// rushes through the game.
func (g *Game) Advance(now int64) bool {
	before := g.Version
	g.advance(now)
	return g.Version != before
}

func (g *Game) advance(now int64) {
	for guard := 0; guard < maxSteps; guard++ {
		at, run := g.nextStep()
		if run == nil || at > now {
			return
		}
		run(at, now)
		g.Version++
	}
}

// step is one timed transition: at is when it was due, now when it runs.
type step func(at, now int64)

// nextStep is the earliest due step: a computer's move, else (on a tie too)
// the phase timer.
func (g *Game) nextStep() (int64, step) {
	if g.Status != StatusPlaying {
		return 0, nil
	}
	var at int64
	var run step
	for _, p := range g.Players {
		if p.Plan != nil && (run == nil || p.Plan.At < at) {
			at, run = p.Plan.At, g.computerStep(p)
		}
	}
	if t, timer := g.timer(); timer != nil && (run == nil || t < at) {
		at, run = t, timer
	}
	return at, run
}

// timer is the current phase's own timed step.
func (g *Game) timer() (int64, step) {
	switch g.Phase {
	case PhaseIntro:
		return g.PhaseEndsAt, func(_, now int64) { g.openQuestion(1, now) }
	case PhaseGuess:
		return g.PhaseEndsAt, func(_, now int64) { g.closeGuesses(now) }
	case PhaseLineup:
		return g.PhaseEndsAt, func(_, now int64) { g.afterLineup(now) }
	case PhaseBet:
		return g.PhaseEndsAt, func(_, now int64) { g.betTimeout(now) }
	case PhaseChips:
		return g.PhaseEndsAt, func(_, now int64) { g.reveal(now) }
	case PhaseReveal:
		return g.PhaseEndsAt, func(_, now int64) { g.afterReveal(now) }
	}
	return 0, nil
}

// computerStep plays a computer's planned move.
func (g *Game) computerStep(p *Player) step {
	return func(_, now int64) {
		plan := p.Plan
		p.Plan = nil
		switch g.Phase {
		case PhaseGuess:
			if g.Cur.guessOf(p.ID) == nil {
				g.placeGuess(p, plan.Value, now)
			}
		case PhaseBet:
			if g.Cur.betOf(p.ID) == nil {
				g.placeBet(g.computerBet(p), now)
			}
		}
	}
}

// setPhase moves to a phase. Computers' plans belong to the phase they were
// made in, so they go.
func (g *Game) setPhase(phase string, endsAt int64) {
	g.Phase, g.PhaseEndsAt = phase, endsAt
	for _, p := range g.Players {
		p.Plan = nil
	}
}

// waitingOn counts the players still playing who have not acted in the
// current phase (guessed or bet), and how many are playing.
func (g *Game) waitingOn() (waiting, playing int) {
	for _, p := range g.playing() {
		if !slices.Contains(g.Cur.Playing, p.ID) {
			continue
		}
		playing++
		switch g.Phase {
		case PhaseGuess:
			if g.Cur.guessOf(p.ID) == nil {
				waiting++
			}
		case PhaseBet:
			if g.Cur.betOf(p.ID) == nil {
				waiting++
			}
		}
	}
	return waiting, playing
}

// hurry is the hurry-up: when everyone playing but one has acted (with at
// least two playing) and more than 10 s remain, the timer drops to 10 s,
// and a computer still to act moves its plan inside the new time.
func (g *Game) hurry(now int64) {
	waiting, playing := g.waitingOn()
	if playing < 2 || waiting != 1 || g.PhaseEndsAt-now <= hurryMs {
		return
	}
	g.PhaseEndsAt = now + hurryMs
	for _, p := range g.Players {
		if p.Plan != nil {
			p.Plan.At = min(p.Plan.At, max(now, g.PhaseEndsAt-computerMarginMs))
		}
	}
}

// ---------------------------------------------------------------- a question

// openQuestion asks question no: everyone playing may guess until the timer;
// the computers decide now what they will guess, and when.
func (g *Game) openQuestion(no int, now int64) {
	q := g.Questions[no-1]
	g.QuestionNo = no
	g.QuestionSeq++
	g.Cur = &Current{Seq: g.QuestionSeq, No: no, Double: no == len(g.Questions), Question: q, OpenedAt: now,
		Playing: g.playingIDs(), Guesses: []Guess{}, Bets: []Bet{}}
	g.History = remember(g.History, q.ID)
	g.setPhase(PhaseGuess, now+int64(g.Settings.GuessSeconds)*1000)
	g.emit(Event{Type: EvQuestion, Q: g.Cur.Seq, No: no})
	if g.Cur.Double {
		g.logf("Question %d of %d: the last one, and it pays double", no, len(g.Questions))
	} else {
		g.logf("Question %d of %d", no, len(g.Questions))
	}
	g.planGuesses(now)
}

// checkGuesses closes the guesses when everyone playing has guessed, else
// applies the hurry-up.
func (g *Game) checkGuesses(now int64) {
	if waiting, _ := g.waitingOn(); waiting == 0 {
		g.closeGuesses(now)
		return
	}
	g.hurry(now)
}

// closeGuesses lines the guesses up as slots (a player who has left keeps
// theirs on the line); with nobody guessing, the answer is shown at once.
func (g *Game) closeGuesses(now int64) {
	cur := g.Cur
	if len(cur.Guesses) == 0 {
		cur.Nobody = true
		g.logf("Nobody guessed")
		g.reveal(now)
		return
	}
	cur.Slots = lineUp(cur.Guesses)
	ms := min(max(lineupBaseMs+lineupPerSlotMs*len(cur.Slots), lineupMinMs), lineupMaxMs)
	g.setPhase(PhaseLineup, now+int64(ms))
	g.emit(Event{Type: EvLineup, Slots: len(cur.Slots)})
}

// afterLineup opens the bets, or with a single slot (one guesser, or
// everyone the same) shows the answer: there is nothing to bet on.
func (g *Game) afterLineup(now int64) {
	if len(g.Cur.Slots) == 1 {
		g.reveal(now)
		return
	}
	g.setPhase(PhaseBet, now+int64(g.Settings.BetSeconds)*1000)
	g.planBets(now)
	g.checkBets(now) // nobody left to bet (everyone playing has gone)
}

// checkBets shows the chips when everyone playing has bet, else applies the
// hurry-up.
func (g *Game) checkBets(now int64) {
	if waiting, _ := g.waitingOn(); waiting == 0 {
		g.showChips(now)
		return
	}
	g.hurry(now)
}

// betTimeout is the bet timer: a player who guessed and is online gets both
// chips on their own slot with no stake (the phone starts there, so one tap
// would have confirmed it); anyone else gets no chips.
func (g *Game) betTimeout(now int64) {
	cur := g.Cur
	for _, p := range g.playing() {
		if !slices.Contains(cur.Playing, p.ID) || cur.betOf(p.ID) != nil {
			continue
		}
		if slot := cur.slotOf(p.ID); slot >= 0 && p.Online {
			cur.Bets = append(cur.Bets, Bet{PlayerID: p.ID, Chips: [2]int{slot, slot}, Auto: true})
		}
	}
	g.showChips(now)
}

func (g *Game) showChips(now int64) {
	g.setPhase(PhaseChips, now+chipsMs)
	g.emit(Event{Type: EvChips})
}

// reveal shows the answer and pays out (§4.1).
func (g *Game) reveal(now int64) {
	cur := g.Cur
	q := cur.Question
	m := 1
	if cur.Double {
		m = 2
	}
	cur.Winners, cur.Second = judge(cur.Slots, q.Answer)
	cur.Revealed = true
	cur.Results = []Result{}
	for _, id := range cur.Playing {
		p := g.Player(id)
		r := Result{PlayerID: id, Before: p.Score}
		if gs := cur.guessOf(id); gs != nil {
			value, slot := gs.Value, cur.slotOf(id)
			r.Guess, r.Slot = &value, &slot
		}
		bonus, chips, stake, closest := payout(cur.Slots, cur.Winners, cur.Second, q.Answer, m, id, cur.betOf(id), !p.Left)
		r.Bonus, r.ChipPoints, r.StakePoints = bonus, chips, stake
		p.Score = max(0, p.Score+bonus+chips+stake)
		if closest {
			p.Closest++
		}
		r.After = p.Score
		r.Delta = r.After - r.Before
		cur.Results = append(cur.Results, r)
	}
	ms := int64(revealMs)
	if cur.Double {
		ms = revealLastMs
	}
	g.setPhase(PhaseReveal, now+ms)
	answer := q.Answer
	e := Event{Type: EvReveal, Q: cur.Seq, QuestionID: q.ID, Answer: &answer, Winners: slices.Clone(cur.Winners),
		Second: slices.Clone(cur.Second), Nobody: cur.Nobody, Guesses: slices.Clone(cur.Guesses), Bets: slices.Clone(cur.Bets)}
	g.emit(e)
	g.logReveal()
}

func (g *Game) logReveal() {
	cur := g.Cur
	q := cur.Question
	answer := formatNumber(q.Answer, q.Format)
	if q.Unit != "" {
		answer += " " + q.Unit
	}
	if cur.Nobody {
		g.logf("The answer was %s", answer)
		return
	}
	var names []string
	for _, i := range cur.Winners {
		for _, id := range cur.Slots[i].OwnerIDs {
			names = append(names, g.Player(id).Name)
		}
	}
	verb := "is closest"
	if len(names) > 1 {
		verb = "are closest"
	}
	if cur.Slots[cur.Winners[0]].Value == q.Answer {
		verb = "got it exactly"
	}
	g.logf("The answer is %s: %s %s", answer, strings.Join(names, " and "), verb)
}

// afterReveal asks the next question, or ends the game after the last.
func (g *Game) afterReveal(now int64) {
	if g.QuestionNo < len(g.Questions) {
		g.openQuestion(g.QuestionNo+1, now)
		return
	}
	g.finish(now)
}

// ---------------------------------------------------------------- leaving

// afterLeave keeps the game moving when a player leaves mid-game. When no
// person is still playing, the game ends on the scores so far (computers do
// not play on alone); otherwise whatever was waiting on them stops waiting.
func (g *Game) afterLeave(p *Player, now int64) {
	people := false
	for _, q := range g.playing() {
		if !q.computer() {
			people = true
		}
	}
	if len(g.playing()) == 0 || (!p.computer() && !people) {
		g.logf("Nobody is left to play")
		g.finish(now)
		return
	}
	switch g.Phase {
	case PhaseGuess:
		g.checkGuesses(now)
	case PhaseBet:
		g.checkBets(now)
	}
}

// String is a one-line description for test failures.
func (g *Game) String() string {
	return fmt.Sprintf("closest %s: %s/%s question %d of %d", g.Code, g.Status, g.Phase, g.QuestionNo, len(g.Questions))
}
