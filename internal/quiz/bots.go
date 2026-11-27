package quiz

import (
	"math"
	"slices"
)

// Computer players (docs/QUIZ-API.md §8) live in the engine and draw from
// the game's seeded random stream, so a game replays exactly from its seed
// or snapshot. They see no more than a player could, with one exception
// that is the point of a quiz bot: whether it "knows" a clue is a dice roll
// against its level's table, and when it knows, it taps the right choice.

// Computer levels.
const (
	LevelEasy   = "easy"
	LevelNormal = "normal"
	LevelHard   = "hard"
	// LevelPerson is the modelled average person cmd/quiz-arena measures the
	// levels against (§8). Rooms never offer it.
	LevelPerson = "person"
)

// Levels are the computer levels rooms offer, easiest first.
var Levels = []string{LevelEasy, LevelNormal, LevelHard}

// Computers' pauses before a move, in ms (a person needs a moment too, and
// an instant move from a computer looks wrong on the big screen).
const (
	computerPickMinMs     = 1500
	computerPickMaxMs     = 3000
	computerBetMinMs      = 1600 // "after about 2 s"
	computerBetMaxMs      = 2400
	computerFinalBetMinMs = 2000
	computerFinalBetMaxMs = 5000
)

const (
	finalTier   = 4    // the final counts as a tier-4 clue for a computer's chance of knowing it
	topicShift  = 0.12 // a strong topic adds this to the chance of knowing, a weak one takes it off
	minKnow     = 0.02
	maxKnow     = 0.98
	minTapMs    = 700 // nobody reads four choices and taps faster
	guessLateMs = 800 // a guess comes this much later than a knowing tap
	finalSims   = 2000
	priorChance = 0.6 // a final bettor's guess at anyone's chance of being right, before their record
	priorWeight = 5   // worth this many answers
)

// profile is how a level answers, picks and bets.
type profile struct {
	knows [5]float64 // chance of knowing a clue, by tier 1–5
	guess float64    // chance of tapping a random choice when it doesn't know
	tapMs float64    // mean time to tap when it knows
	tapSD float64    // spread of that time (normal distribution)
	style string     // how it picks and bets: LevelEasy, LevelNormal or LevelHard
}

// profiles are tuned with cmd/quiz-arena (§8 records the measured win
// shares). Speed decides a clue that both players know, so Normal and Hard
// are only a little quicker and better read than the person: the first
// draft of the table (Hard .96–.60 at 1400 ms) let the person win 1 game in
// 100 against Hard.
var profiles = map[string]profile{
	LevelEasy:   {knows: [5]float64{.80, .65, .50, .35, .20}, guess: .35, tapMs: 2600, tapSD: 700, style: LevelEasy},
	LevelNormal: {knows: [5]float64{.86, .74, .60, .47, .32}, guess: .15, tapMs: 2100, tapSD: 600, style: LevelNormal},
	LevelHard:   {knows: [5]float64{.88, .76, .63, .50, .35}, guess: .10, tapMs: 2000, tapSD: 500, style: LevelHard},
	// The person picks and bets as Normal does: cheapest first, middling bets.
	LevelPerson: {knows: [5]float64{.85, .72, .58, .45, .30}, guess: .20, tapMs: 2200, tapSD: 700, style: LevelNormal},
}

// drawTopics gives a computer its two strong and two weak topics for a game.
func (g *Game) drawTopics() (strong, weak []string) {
	order := g.rng.Perm(len(Topics))
	for _, i := range order[:2] {
		strong = append(strong, Topics[i])
	}
	for _, i := range order[2:4] {
		weak = append(weak, Topics[i])
	}
	return strong, weak
}

// knowChance is a computer's chance of knowing a clue of this topic and tier.
func (p *Player) knowChance(topic string, tier int) float64 {
	k := profiles[p.Level].knows[tier-1]
	if slices.Contains(p.Strong, topic) {
		k += topicShift
	}
	if slices.Contains(p.Weak, topic) {
		k -= topicShift
	}
	return min(max(k, minKnow), maxKnow)
}

// planTaps decides, as the choices open, what each computer among ids will
// tap and when (§6.5). always: it answers even when it doesn't know (a gold
// square or the final, where silence costs as much as a wrong answer).
func (g *Game) planTaps(q *Question, ids []string, always bool) {
	for _, id := range ids {
		p := g.Player(id)
		if p == nil || p.Left || !p.computer() {
			continue
		}
		if choice, ms, ok := g.computerAnswer(p, q, always); ok {
			p.Plan = &Plan{At: q.OpenAt + int64(ms), Choice: choice, Ms: ms}
		}
	}
}

// computerAnswer draws a computer's answer: the right choice when it knows,
// else perhaps a random guess, a little later.
func (g *Game) computerAnswer(p *Player, q *Question, always bool) (choice, ms int, ok bool) {
	prof := profiles[p.Level]
	tap := func(extra float64) int {
		return max(minTapMs, int(math.Round(prof.tapMs+prof.tapSD*g.rng.NormFloat64()+extra)))
	}
	if g.rng.Float64() < p.knowChance(q.Topic, q.Tier) {
		return q.Answer, tap(0), true
	}
	if always || g.rng.Float64() < prof.guess {
		return g.rng.IntN(len(q.Choices)), tap(guessLateMs), true
	}
	return 0, 0, false
}

// boardCell is a cell's place on the board.
type boardCell struct{ cat, row int }

// computerPick chooses a clue. Easy: any open cell. Normal: the cheapest cell
// of a random category. Hard: its strong categories first, cheapest first,
// or the dearest rows (where gold squares hide) when behind.
func (g *Game) computerPick(p *Player) (cat, row int) {
	var open []boardCell
	for c, bc := range g.board().Categories {
		for r, cell := range bc.Cells {
			if cell.open() {
				open = append(open, boardCell{c, r})
			}
		}
	}
	switch profiles[p.Level].style {
	case LevelEasy:
		x := open[g.rng.IntN(len(open))]
		return x.cat, x.row
	case LevelHard:
		x := g.hardPick(p, open)
		return x.cat, x.row
	}
	var cats []int
	for _, x := range open {
		if !slices.Contains(cats, x.cat) {
			cats = append(cats, x.cat)
		}
	}
	cat = cats[g.rng.IntN(len(cats))]
	// open lists each category's cells cheapest first.
	i := slices.IndexFunc(open, func(x boardCell) bool { return x.cat == cat })
	return cat, open[i].row
}

func (g *Game) hardPick(p *Player, open []boardCell) boardCell {
	pool := slices.DeleteFunc(slices.Clone(open), func(x boardCell) bool {
		return !slices.Contains(p.Strong, g.board().Categories[x.cat].Topic)
	})
	if len(pool) == 0 {
		pool = open
	}
	behind := g.behind(p)
	best := pool[0]
	for _, x := range pool[1:] {
		if (behind && x.row > best.row) || (!behind && x.row < best.row) {
			best = x
		}
	}
	return best
}

// behind reports whether someone still playing has more points than p.
func (g *Game) behind(p *Player) bool {
	for _, q := range g.playing() {
		if q != p && q.Score > p.Score {
			return true
		}
	}
	return false
}

// computerGoldBet bets on a gold square. Easy: about 20% of the most it may
// bet. Normal: 40–60% (60–80% in a strong topic). Hard: 80–100% in a strong
// topic or when behind, else about half.
func (g *Game) computerGoldBet(p *Player) int {
	b := g.Bet
	strong := slices.Contains(p.Strong, g.board().Categories[g.Clue.Category].Topic)
	lo, hi := 0.4, 0.6
	switch profiles[p.Level].style {
	case LevelEasy:
		lo, hi = 0.15, 0.25
	case LevelHard:
		lo, hi = 0.45, 0.55
		if strong || g.behind(p) {
			lo, hi = 0.8, 1
		}
	default:
		if strong {
			lo, hi = 0.6, 0.8
		}
	}
	frac := lo + (hi-lo)*g.rng.Float64()
	return min(max(int(math.Round(frac*float64(b.Max))), b.Min), b.Max)
}

// computerFinalBet simulates the final: each candidate bet is tried against
// finalSims outcomes, where every player is right with the chance their
// record so far suggests and the others' (secret) bets are drawn at random
// within their limits; the bet that most often finishes first wins. Easy
// then adds big noise and Normal a little; Hard bets the best it found.
func (g *Game) computerFinalBet(p *Player) int {
	top := finalMaxBet(p)
	players := g.playing()
	var others []*Player
	for _, q := range players {
		if q != p {
			others = append(others, q)
		}
	}
	cands := finalCandidates(p, players, top)
	wins := make([]int, len(cands))
	self := recordChance(p)
	for range finalSims {
		best := math.MinInt
		for _, o := range others {
			bet := g.rng.IntN(finalMaxBet(o) + 1)
			if g.rng.Float64() < recordChance(o) {
				best = max(best, o.Score+bet)
			} else {
				best = max(best, o.Score-bet)
			}
		}
		right := g.rng.Float64() < self
		for i, c := range cands {
			score := p.Score - c
			if right {
				score = p.Score + c
			}
			if score >= best {
				wins[i]++
			}
		}
	}
	bet, most := cands[0], wins[0]
	for i, w := range wins {
		if w > most {
			bet, most = cands[i], w
		}
	}
	switch profiles[p.Level].style {
	case LevelEasy:
		if g.rng.IntN(2) == 0 {
			bet += g.rng.IntN(top/2 + 1)
		}
	case LevelNormal:
		bet += int(math.Round((g.rng.Float64()*0.2 - 0.1) * float64(top)))
	}
	return min(max(bet, 0), top)
}

// recordChance is a player's chance of being right, from their record:
// (correct + 0.6 × 5) / (answers + 5).
func recordChance(p *Player) float64 {
	return (float64(p.Correct) + priorChance*priorWeight) / float64(p.Correct+p.Wrong+priorWeight)
}

// finalCandidates are the bets worth simulating: nothing; for the leader,
// the "cover" bet (2 × second − leader + 1, enough to stay ahead of a
// second-placed player who doubles) and the "stay ahead" bet (leader −
// 2 × second − 1, the most that still wins when wrong); for second place,
// the most that keeps them above a third who doubles; and a quarter, half,
// three quarters and all of the limit.
func finalCandidates(p *Player, players []*Player, top int) []int {
	scores := make([]int, 0, len(players))
	for _, q := range players {
		scores = append(scores, q.Score)
	}
	slices.SortFunc(scores, func(a, b int) int { return b - a })
	cands := []int{0}
	add := func(b int) {
		b = min(max(b, 0), top)
		if !slices.Contains(cands, b) {
			cands = append(cands, b)
		}
	}
	if len(scores) >= 2 {
		leader, second := scores[0], scores[1]
		if p.Score == leader {
			add(2*second - leader + 1)
			add(leader - 2*second - 1)
		} else if p.Score == second && len(scores) >= 3 {
			add(second - 2*scores[2] - 1)
		}
	}
	for _, pct := range []int{25, 50, 75, 100} {
		add(top * pct / 100)
	}
	return cands
}
