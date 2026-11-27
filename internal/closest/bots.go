package closest

import (
	"math"
	"slices"
)

// Computer players (docs/CLOSEST-API.md §6) live in the engine and draw from
// the game's seeded random stream, so a game replays exactly from its seed
// or snapshot. Guessing is the one place a computer reads the answer: its
// guess is the answer plus an error drawn for its level, which is what
// "knowing roughly" means for a bot. Betting never reads the answer: it
// works only from what every player can see (the slots, the guesses, the
// odds) and the computer's own guess.

// Computer levels.
const (
	LevelEasy   = "easy"
	LevelNormal = "normal"
	LevelHard   = "hard"
	// LevelPerson is the modelled average person cmd/closest-arena measures
	// the levels against (§6). Rooms never offer it.
	LevelPerson = "person"
)

// Levels are the computer levels rooms offer, easiest first.
var Levels = []string{LevelEasy, LevelNormal, LevelHard}

// Computers' pauses (§6): a guess 4–12 s after the question opens, a bet 3–8
// s after the bets open, and always computerMarginMs before the deadline.
const (
	computerGuessMinMs = 4000
	computerGuessMaxMs = 12000
	computerBetMinMs   = 3000
	computerBetMaxMs   = 8000
)

const (
	sharedWeight    = 0.6 // z = 0.6·zs + 0.8·zi: every computer shares part of its error
	ownWeight       = 0.8
	wildFactor      = 3   // a wild guess has three times the width
	beliefDraws     = 400 // samples of the truth a computer's bet is worked out from
	hardSplitBelow  = 0.25
	normalStakeOver = 0.5
	hardStakeEdge   = 0.3 // Hard stakes when p_win × (odds + 1) − 1 is over this
)

// profile is how a level guesses and bets.
type profile struct {
	width float64 // the spread of its guesses, in units of the question's spread
	wild  float64 // chance a guess is wild (three times the width)
	knows float64 // chance it knows the answer exactly
	// oneFigure is the chance a plain guess is rounded to one significant
	// figure (else two); yearStep and yearChance round a year guess to a
	// multiple of yearStep that often (0: never).
	oneFigure  float64
	yearStep   int
	yearChance float64
	// mix is the weight of its own guess in the centre of its belief about
	// the answer (the rest is the table's median guess).
	mix   float64
	style string // how it bets: LevelEasy, LevelNormal or LevelHard
}

// profiles are tuned with cmd/closest-arena (§6 records the measured win
// shares).
var profiles = map[string]profile{
	LevelEasy:   {width: 1.6, wild: .08, knows: .03, oneFigure: 1.0 / 3, yearStep: 10, yearChance: .5, style: LevelEasy},
	LevelNormal: {width: 1.2, wild: .05, knows: .06, oneFigure: 1.0 / 3, yearStep: 5, yearChance: .25, mix: .75, style: LevelNormal},
	LevelHard:   {width: 0.9, wild: .03, knows: .10, mix: .5, style: LevelHard},
	// The modelled average person: a little wider than Normal's first
	// draft, betting and staking as Normal does.
	LevelPerson: {width: 1.1, wild: .05, knows: .06, oneFigure: 1.0 / 3, yearStep: 5, yearChance: .25, mix: .75, style: LevelNormal},
}

// planGuesses decides, as a question opens, what each computer will guess
// and when. One error is drawn for the question and shared by every
// computer (a table tends to be wrong together); each adds its own.
func (g *Game) planGuesses(now int64) {
	cur := g.Cur
	zs := g.rng.NormFloat64()
	latest := g.PhaseEndsAt - computerMarginMs
	for _, id := range cur.Playing {
		p := g.Player(id)
		if p == nil || p.Left || !p.computer() {
			continue
		}
		value := g.computerGuess(p, cur.Question, zs)
		at := min(now+g.between(computerGuessMinMs, computerGuessMaxMs), max(now, latest))
		p.Plan = &Plan{At: at, Value: value}
	}
}

// computerGuess draws a computer's guess (§6): the answer with an error of
// its level's width, sometimes wild, sometimes exact, rounded as a person
// would type it and kept inside the question's range.
func (g *Game) computerGuess(p *Player, q *Question, zs float64) int {
	prof := profiles[p.Level]
	zi, wild, knows, rounding := g.rng.NormFloat64(), g.rng.Float64(), g.rng.Float64(), g.rng.Float64()
	if knows < prof.knows {
		return q.Answer
	}
	w := prof.width
	if wild < prof.wild {
		w *= wildFactor
	}
	z := sharedWeight*zs + ownWeight*zi
	var v float64
	if q.Format == FormatYear {
		v = float64(q.Answer) + z*w*q.Spread
		if prof.yearStep > 0 && rounding < prof.yearChance {
			v = math.Round(v/float64(prof.yearStep)) * float64(prof.yearStep)
		}
	} else {
		v = float64(q.Answer) * math.Exp(z*w*math.Log1p(q.Spread/100))
		figures := 2
		if rounding < prof.oneFigure {
			figures = 1
		}
		v = roundFigures(v, figures)
		// Past the end of the range, the nearest round number inside it
		// (9,900,000 rather than 9,999,999).
		if v > float64(q.Max) {
			v = math.Max(float64(q.Min), floorFigures(float64(q.Max), 2))
		} else if v < float64(q.Min) {
			v = math.Min(float64(q.Max), -floorFigures(-float64(q.Min), 2))
		}
	}
	return min(max(int(math.Round(v)), q.Min), q.Max)
}

// floorFigures rounds v down to that many significant figures (v > 0; for
// v < 0 it rounds toward zero's other side, so -floorFigures(-v) rounds up).
func floorFigures(v float64, figures int) float64 {
	a := math.Abs(v)
	if a < 1 {
		return math.Floor(v)
	}
	digits := int(math.Floor(math.Log10(a))) + 1
	if digits <= figures {
		return math.Floor(v)
	}
	unit := math.Pow(10, float64(digits-figures))
	return math.Floor(v/unit) * unit
}

// roundFigures rounds v to that many significant figures (1,847 → 1,800 at
// two, 2,000 at one).
func roundFigures(v float64, figures int) float64 {
	if v < 1 {
		return math.Round(v)
	}
	digits := int(math.Floor(math.Log10(v))) + 1
	if digits <= figures {
		return math.Round(v)
	}
	unit := math.Pow(10, float64(digits-figures))
	return math.Round(v/unit) * unit
}

// planBets decides, as the bets open, when each computer bets (what it bets
// is worked out at that moment, from the line as everyone sees it).
func (g *Game) planBets(now int64) {
	latest := g.PhaseEndsAt - computerMarginMs
	for _, id := range g.Cur.Playing {
		p := g.Player(id)
		if p == nil || p.Left || !p.computer() {
			continue
		}
		p.Plan = &Plan{At: min(now+g.between(computerBetMinMs, computerBetMaxMs), max(now, latest))}
	}
}

// computerBet places a computer's chips and stake (§6). It never reads the
// answer.
//   - Easy: both chips on its own slot (no guess: the middle slot); never
//     stakes.
//   - Normal: one chip on its own slot, one on its best expected payout
//     (p_win × odds + p_second); no guess: both on that best pick. Stakes 1
//     on the best pick when its win chance is over 0.5.
//   - Hard: both chips on its best expected payout, split (one on the most
//     likely slot) when the best pick's win chance is under 0.25. Stakes on
//     whichever of its chips' slots has the biggest edge p_win × (odds + 1)
//     − 1, when that is over 0.3: 1 point, 2 over 0.6, 3 over 1.
func (g *Game) computerBet(p *Player) Bet {
	cur := g.Cur
	prof := profiles[p.Level]
	own := cur.slotOf(p.ID)
	b := Bet{PlayerID: p.ID}
	if prof.style == LevelEasy {
		s := own
		if s < 0 {
			s = (len(cur.Slots) - 1) / 2
		}
		b.Chips = [2]int{s, s}
		return b
	}
	win, second := g.beliefChances(p, prof)
	best := 0
	payout := func(i int) float64 { return win[i]*float64(cur.Slots[i].Odds) + second[i]*secondChip }
	for i := range cur.Slots {
		if payout(i) > payout(best) {
			best = i
		}
	}
	top := stakeMax(p)
	switch prof.style {
	case LevelHard:
		b.Chips = [2]int{best, best}
		if win[best] < hardSplitBelow {
			likely := 0
			for i := range cur.Slots {
				if win[i] > win[likely] {
					likely = i
				}
			}
			b.Chips[1] = likely
		}
		slot, edge := -1, 0.0
		for _, c := range b.Chips {
			if e := win[c]*float64(cur.Slots[c].Odds+1) - 1; slot < 0 || e > edge {
				slot, edge = c, e
			}
		}
		stake := 0
		switch {
		case edge > 1:
			stake = 3
		case edge > 0.6:
			stake = 2
		case edge > hardStakeEdge:
			stake = 1
		}
		if stake = min(stake, top); stake > 0 {
			b.Stake, b.StakeSlot = stake, &slot
		}
	default:
		first := own
		if first < 0 {
			first = best
		}
		b.Chips = [2]int{first, best}
		if win[best] > normalStakeOver && top >= 1 {
			slot := best
			b.Stake, b.StakeSlot = 1, &slot
		}
	}
	return b
}

// beliefChances is a computer's belief about each slot's chance of winning
// and of coming second: the truth is drawn beliefDraws times around a centre
// made from its own guess and the table's median guess (by its level's mix),
// with its level's width.
func (g *Game) beliefChances(p *Player, prof profile) (win, second []float64) {
	cur := g.Cur
	q := cur.Question // its format and spread only: never its answer
	values := make([]float64, 0, len(cur.Guesses))
	for _, gs := range cur.Guesses {
		values = append(values, float64(gs.Value))
	}
	slices.Sort(values)
	median := values[len(values)/2]
	if len(values)%2 == 0 {
		median = (values[len(values)/2-1] + median) / 2
	}
	centre, mix := median, prof.mix
	if gs := cur.guessOf(p.ID); gs != nil {
		centre = float64(gs.Value)
	} else {
		mix = 0
	}
	year := q.Format == FormatYear
	var width float64
	if year {
		centre = mix*centre + (1-mix)*median
		width = prof.width * q.Spread
	} else {
		centre = math.Exp(mix*math.Log(max(centre, 1)) + (1-mix)*math.Log(max(median, 1)))
		width = prof.width * math.Log1p(q.Spread/100)
	}
	k := len(cur.Slots)
	win, second = make([]float64, k), make([]float64, k)
	dist := make([]float64, k)
	for range beliefDraws {
		z := g.rng.NormFloat64()
		truth := centre + z*width
		if !year {
			truth = centre * math.Exp(z*width)
		}
		best, next := -1, -1
		for i, s := range cur.Slots {
			dist[i] = math.Abs(float64(s.Value) - truth)
			switch {
			case best < 0 || dist[i] < dist[best]:
				best, next = i, best
			case next < 0 || dist[i] < dist[next]:
				next = i
			}
		}
		win[best]++
		if next >= 0 {
			second[next]++
		}
	}
	for i := range win {
		win[i] /= beliefDraws
		second[i] /= beliefDraws
	}
	return win, second
}
