package quiz

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// The phase machine (docs/QUIZ-API.md §4). Every timer lives in the game:
// NextDeadline says when the next step is due and Advance runs it. Steps are
// phase timers, computers' moves and taps, and judging a clue.

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
// anything changed (the server then publishes the new state). A tap keeps
// its own arrival time; a phase that starts late starts from now, so a
// server that slept (a restart) never rushes through the game.
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
		return g.PhaseEndsAt, func(_, now int64) { g.enterPick(now) }
	case PhasePick:
		return g.PhaseEndsAt, func(_, now int64) { g.autoPick(now) }
	case PhaseGold:
		return g.PhaseEndsAt, func(_, now int64) { g.enterBet(now) }
	case PhaseBet:
		return g.PhaseEndsAt, func(_, now int64) { g.betTimeout(now) }
	case PhaseRead:
		return g.Clue.Q.OpenAt, func(_, now int64) { g.openClue(now) }
	case PhaseAnswer:
		return g.judgeTimer(g.Clue.Q, g.judgeClue)
	case PhaseReveal:
		return g.PhaseEndsAt, func(_, now int64) { g.afterReveal(now) }
	case PhaseRoundEnd:
		return g.PhaseEndsAt, func(_, now int64) { g.afterRound(now) }
	case PhaseFinalIntro:
		return g.PhaseEndsAt, func(_, now int64) { g.enterFinalBet(now) }
	case PhaseFinalBet:
		return g.PhaseEndsAt, func(_, now int64) { g.finalBetTimeout(now) }
	case PhaseFinalRead:
		return g.Final.Q.OpenAt, func(_, now int64) { g.openFinal(now) }
	case PhaseFinalAnswer:
		return g.judgeTimer(g.Final.Q, g.startFinalReveal)
	case PhaseFinalReveal:
		if g.Final.Revealed < len(g.Final.Order) {
			return g.Final.RevealAt, func(_, now int64) { g.revealNext(now) }
		}
		return g.PhaseEndsAt, func(_, now int64) { g.finish(now) }
	}
	return 0, nil
}

// judgeTimer is an answering phase's timer: judge at DecideAt once it is
// known; until then, when the choices close, allow the grace for taps still
// on their way (§6.4).
func (g *Game) judgeTimer(q *Question, judge func(now int64)) (int64, step) {
	if q.DecideAt > 0 {
		return q.DecideAt, func(_, now int64) { judge(now) }
	}
	return q.ClosesAt, func(_, _ int64) { q.DecideAt = q.ClosesAt + g.wait() }
}

// computerStep plays a computer's planned move.
func (g *Game) computerStep(p *Player) step {
	return func(at, now int64) {
		plan := p.Plan
		p.Plan = nil
		switch g.Phase {
		case PhasePick:
			cat, row := g.computerPick(p)
			g.playCell(p, cat, row, now)
		case PhaseBet:
			g.placeGoldBet(p, g.computerGoldBet(p), now)
		case PhaseAnswer:
			g.tapClue(p, plan.Choice, plan.Ms, at, now)
		case PhaseFinalBet:
			g.placeFinalBet(p, g.computerFinalBet(p), now)
		case PhaseFinalAnswer:
			g.tapFinal(p, plan.Choice, plan.Ms, at, now)
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

// ---------------------------------------------------------------- the board

// startRound shows a board: the intro reads its six titles.
func (g *Game) startRound(round int, now int64) {
	g.Round = round
	g.LastCategory = -1 // the pick timer's "last category" is on the old board
	b := g.board()
	var ids, titles []string
	for _, c := range b.Categories {
		ids, titles = append(ids, c.ID), append(titles, c.Title)
	}
	g.History = remember(g.History, ids...)
	g.Clue, g.Bet = nil, nil
	g.setPhase(PhaseIntro, now+introMs+introPerTitleMs*int64(len(titles)))
	g.emit(Event{Type: EvIntro, Round: round})
	g.logf("Round %d: %s", round, strings.Join(titles, ", "))
}

func (g *Game) enterPick(now int64) {
	g.Clue, g.Bet = nil, nil
	g.setPhase(PhasePick, now+pickMs)
	if p := g.Player(g.ChooserID); p != nil && p.computer() {
		p.Plan = &Plan{At: now + g.between(computerPickMinMs, computerPickMaxMs)}
	}
}

func (g *Game) pick(p *Player, category, row *int, now int64) error {
	switch {
	case g.Status != StatusPlaying:
		return errNotPlaying
	case g.Phase != PhasePick:
		return errors.New("it is not time to pick a clue")
	case p.ID != g.ChooserID:
		return errors.New("it is not your pick")
	case category == nil || row == nil || *category < 0 || *category >= boardCategories || *row < 0 || *row >= boardRows:
		return errors.New("pick a category from 0 to 5 and a row from 0 to 4")
	case !g.board().Categories[*category].Cells[*row].open():
		return errors.New("that clue has been played")
	}
	g.playCell(p, *category, *row, now)
	return nil
}

// autoPick is the pick timer: the cheapest open cell in the last picked
// category, else the cheapest open cell, leftmost.
func (g *Game) autoPick(now int64) {
	p := g.Player(g.ChooserID)
	b := g.board()
	cat, row := -1, -1
	if g.LastCategory >= 0 {
		for r, cell := range b.Categories[g.LastCategory].Cells {
			if cell.open() {
				cat, row = g.LastCategory, r
				break
			}
		}
	}
	for r := 0; cat < 0 && r < boardRows; r++ {
		for c, bc := range b.Categories {
			if bc.Cells[r].open() {
				cat, row = c, r
				break
			}
		}
	}
	g.logf("Time is up: %s gets the next clue", p.Name)
	g.playCell(p, cat, row, now)
}

// playCell plays the picked clue: a gold square first goes to its finder's
// bet; any other clue is read at once.
func (g *Game) playCell(p *Player, cat, row int, now int64) {
	bc := g.board().Categories[cat]
	cell := bc.Cells[row]
	cell.Played = true
	g.LastCategory = cat
	g.ClueSeq++
	g.Clue = &ClueState{Seq: g.ClueSeq, Category: cat, Row: row, Value: cell.Value, Gold: cell.Gold, Eligible: []string{}}
	g.emit(Event{Type: EvPick, PlayerID: p.ID, Category: &cat, Row: &row, Value: cell.Value})
	g.logf("%s picks %s for %d", p.Name, bc.Title, cell.Value)
	if !cell.Gold {
		g.read(now)
		return
	}
	g.Clue.Finder = p.ID
	g.setPhase(PhaseGold, now+goldMs)
	g.emit(Event{Type: EvGold, PlayerID: p.ID})
	g.logf("A gold square! Only %s may answer it", p.Name)
}

func (g *Game) enterBet(now int64) {
	p := g.Player(g.Clue.Finder)
	g.Bet = &GoldBet{PlayerID: p.ID, Min: goldMinBet, Max: max(p.Score, topValue(g.Round))}
	g.setPhase(PhaseBet, now+betMs)
	if p.computer() {
		p.Plan = &Plan{At: now + g.between(computerBetMinMs, computerBetMaxMs)}
	}
}

func (g *Game) bet(p *Player, amount *int, now int64) error {
	if g.Status != StatusPlaying {
		return errNotPlaying
	}
	switch g.Phase {
	case PhaseBet:
		if p.ID != g.Bet.PlayerID {
			return errors.New("only the gold square finder bets on it")
		}
		if err := checkBet(amount, g.Bet.Min, g.Bet.Max); err != nil {
			return err
		}
		g.placeGoldBet(p, *amount, now)
	case PhaseFinalBet:
		if _, ok := g.Final.betOf(p.ID); ok {
			return errors.New("you have already bet")
		}
		if err := checkBet(amount, 0, finalMaxBet(p)); err != nil {
			return err
		}
		g.placeFinalBet(p, *amount, now)
	default:
		return errors.New("there is nothing to bet on now")
	}
	return nil
}

// checkBet refuses a missing bet or one out of range ("bet between 5 and
// 900").
func checkBet(amount *int, lo, hi int) error {
	if amount == nil || *amount < lo || *amount > hi {
		return fmt.Errorf("bet between %d and %d", lo, hi)
	}
	return nil
}

func (g *Game) placeGoldBet(p *Player, amount int, now int64) {
	g.Bet.Amount, g.Bet.Placed = amount, true
	g.emit(Event{Type: EvBet, PlayerID: p.ID, Amount: amount})
	g.logf("%s bets %d", p.Name, amount)
	g.read(now)
}

// betTimeout is the gold bet timer: the smallest bet.
func (g *Game) betTimeout(now int64) {
	p := g.Player(g.Bet.PlayerID)
	g.logf("Time is up for %s's bet", p.Name)
	g.placeGoldBet(p, g.Bet.Min, now)
}

// read starts reading the clue; the choices open when the reading ends.
func (g *Game) read(now int64) {
	c := g.Clue
	clue := g.board().Categories[c.Category].Cells[c.Row].Clue
	topic := g.board().Categories[c.Category].Topic
	c.Q = g.newQuestion(clue.Text, clue.Answer, clue.Wrong, topic, c.Row+1, now, int64(g.Settings.AnswerSeconds)*1000)
	if c.Gold {
		c.Eligible = []string{c.Finder}
	} else {
		c.Eligible = g.playingIDs()
	}
	g.setPhase(PhaseRead, c.Q.OpenAt)
}

// newQuestion shuffles the choices (the right one's place is secret) and
// times the reading.
func (g *Game) newQuestion(text, answer string, wrong []string, topic string, tier int, now, answerMs int64) *Question {
	all := append([]string{answer}, wrong...)
	q := &Question{Text: text, Topic: topic, Tier: tier, ReadAt: now, Taps: []Tap{}}
	for i, k := range g.rng.Perm(len(all)) {
		q.Choices = append(q.Choices, all[k])
		if k == 0 {
			q.Answer = i
		}
	}
	q.OpenAt = now + g.readMs(text)
	q.ClosesAt = q.OpenAt + answerMs
	return q
}

// readMs is how long a clue is read before its choices open:
// clamp(1500 + 300 × words, 3000, 8000) ms, scaled by the reading speed.
func (g *Game) readMs(text string) int64 {
	ms := min(max(1500+300*len(strings.Fields(text)), 3000), 8000)
	return int64(math.Round(float64(ms) * g.Settings.readFactor()))
}

// openClue opens the choices on every phone at once (by synced clock); the
// computers decide now what they will tap, and when.
func (g *Game) openClue(now int64) {
	c := g.Clue
	g.setPhase(PhaseAnswer, c.Q.ClosesAt)
	g.emit(Event{Type: EvOpen, Clue: c.Seq})
	g.planTaps(c.Q, c.Eligible, c.Gold)
	g.checkClueAllIn(now, now)
}

// afterReveal goes back to the board, or ends the round when it is empty.
func (g *Game) afterReveal(now int64) {
	for _, bc := range g.board().Categories {
		if slices.ContainsFunc(bc.Cells, (*Cell).open) {
			g.enterPick(now)
			return
		}
	}
	g.Clue, g.Bet = nil, nil
	g.setPhase(PhaseRoundEnd, now+roundEndMs)
	g.emit(Event{Type: EvRoundEnd, Round: g.Round})
	g.logf("That's the end of round %d", g.Round)
}

// afterRound starts the second board (the lowest score picks first) or the
// final.
func (g *Game) afterRound(now int64) {
	if g.Round < len(g.Boards) {
		g.ChooserID = g.lowestScore()
		g.startRound(g.Round+1, now)
		return
	}
	g.enterFinalIntro(now)
}

// ---------------------------------------------------------------- the final

func (g *Game) enterFinalIntro(now int64) {
	g.Round = finalRound
	g.ClueSeq++
	f := g.FinalClue
	g.Final = &FinalState{Seq: g.ClueSeq, ID: f.ID, Category: f.Category, Bets: []FinalBet{}, Results: []FinalResult{}}
	g.History = remember(g.History, f.ID)
	g.setPhase(PhaseFinalIntro, now+finalIntroMs)
	g.emit(Event{Type: EvIntro, Round: finalRound})
	g.logf("The final: %s", f.Category)
}

func (g *Game) enterFinalBet(now int64) {
	g.setPhase(PhaseFinalBet, now+finalBetMs)
	for _, p := range g.playing() {
		if p.computer() {
			p.Plan = &Plan{At: now + g.between(computerFinalBetMinMs, computerFinalBetMaxMs)}
		}
	}
}

// finalMaxBet is the most a player may bet in the final: their score, or
// finalTopBet when that is more (so a player at zero or below still plays).
func finalMaxBet(p *Player) int { return max(p.Score, finalTopBet) }

func (g *Game) placeFinalBet(p *Player, amount int, now int64) {
	g.Final.Bets = append(g.Final.Bets, FinalBet{PlayerID: p.ID, Amount: amount})
	g.emit(Event{Type: EvFinalBet, PlayerID: p.ID})
	g.logf("%s has bet", p.Name)
	if g.allBet() {
		g.readFinal(now)
	}
}

func (g *Game) allBet() bool {
	for _, p := range g.playing() {
		if _, ok := g.Final.betOf(p.ID); !ok {
			return false
		}
	}
	return true
}

// finalBetTimeout is the final bet timer: no bet counts as 0.
func (g *Game) finalBetTimeout(now int64) {
	for _, p := range g.playing() {
		if _, ok := g.Final.betOf(p.ID); !ok {
			g.Final.Bets = append(g.Final.Bets, FinalBet{PlayerID: p.ID})
			g.logf("Time is up: %s bets nothing", p.Name)
		}
	}
	g.readFinal(now)
}

func (g *Game) readFinal(now int64) {
	f := g.FinalClue
	g.Final.Q = g.newQuestion(f.Text, f.Answer, f.Wrong, f.Topic, finalTier, now, finalAnswerMs)
	g.setPhase(PhaseFinalRead, g.Final.Q.OpenAt)
}

func (g *Game) openFinal(now int64) {
	g.setPhase(PhaseFinalAnswer, g.Final.Q.ClosesAt)
	g.emit(Event{Type: EvOpen, Clue: g.Final.Seq})
	g.planTaps(g.Final.Q, g.playingIDs(), true)
	g.checkFinalAllIn(now)
}

// startFinalReveal shows the right answer, then each player's answer and bet
// one by one, lowest score first.
func (g *Game) startFinalReveal(now int64) {
	f := g.Final
	players := g.playing()
	slices.SortStableFunc(players, func(a, b *Player) int { return a.Score - b.Score })
	f.Order = []string{}
	for _, p := range players {
		f.Order = append(f.Order, p.ID)
	}
	f.Revealed, f.RevealAt = 0, now+finalRevealStepMs
	g.setPhase(PhaseFinalReveal, now+finalRevealStepMs*int64(len(f.Order))+finalRevealEndMs)
	g.logf("The answer is %s", f.Q.Choices[f.Q.Answer])
}

// revealNext reveals the next player's final answer and settles their bet:
// right wins it, wrong or no answer loses it. The next reveal is timed from
// now, so a timer that fires late (a paused room, a sleeping PC) still shows
// the rest one by one instead of all at once.
func (g *Game) revealNext(now int64) {
	f := g.Final
	p := g.Player(f.Order[f.Revealed])
	bet, _ := f.betOf(p.ID)
	r := FinalResult{PlayerID: p.ID, Bet: bet, Choice: -1, Before: p.Score}
	t := f.Q.tapOf(p.ID)
	if t != nil {
		r.Choice, r.Correct = t.Choice, t.Correct
	}
	if r.Correct {
		p.Score += bet
		p.Correct++
	} else {
		p.Score -= bet
		p.Wrong++
	}
	r.After = p.Score
	f.Results = append(f.Results, r)
	f.Revealed++
	f.RevealAt = now + finalRevealStepMs
	g.PhaseEndsAt = now + finalRevealStepMs*int64(len(f.Order)-f.Revealed) + finalRevealEndMs
	e := Event{Type: EvFinalReveal, PlayerID: p.ID, Correct: &r.Correct, Bet: &bet}
	if t != nil {
		rec := f.Q.tapRecord(*t)
		e.Tap = &rec
	}
	g.emit(e)
	verdict := "wrong"
	if r.Correct {
		verdict = "right"
	}
	g.logf("%s was %s and bet %d: %d points", p.Name, verdict, bet, p.Score)
}

// ---------------------------------------------------------------- leaving

// afterLeave keeps the game moving when a player leaves mid-game: control
// passes on, and whatever was waiting on them stops waiting.
func (g *Game) afterLeave(p *Player, now int64) {
	if len(g.playing()) == 0 {
		g.finish(now)
		return
	}
	wasChooser := g.ChooserID == p.ID
	if wasChooser {
		g.ChooserID = g.highestScore()
	}
	switch g.Phase {
	case PhasePick:
		if wasChooser {
			g.enterPick(now) // the new chooser gets the whole pick time
		}
	case PhaseGold, PhaseBet:
		if g.Clue.Finder == p.ID {
			g.logf("The gold square goes unplayed")
			g.afterReveal(now)
		}
	case PhaseAnswer:
		g.checkClueAllIn(now, now)
	case PhaseFinalBet:
		if g.allBet() {
			g.readFinal(now)
		}
	case PhaseFinalAnswer:
		g.checkFinalAllIn(now)
	}
}
