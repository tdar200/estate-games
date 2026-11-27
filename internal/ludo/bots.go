package ludo

import (
	"errors"
	"fmt"
	mrand "math/rand/v2"
)

// Computer players. A bot decides from the View its seat gets (Ludo hides
// nothing, so that is the whole game) and answers with the same command a
// phone sends: {"type":"roll"} or {"type":"move","piece":N}. Rolling is never
// a decision; choosing the piece is.
//
//   - Easy plays a random legal move.
//   - Normal scores each move with rules of thumb a decent player uses:
//     capture, get a piece home or into its home column, bring a piece out on
//     a 6, step onto a safe square, run from danger, and don't stop just in
//     front of an opponent.
//   - Hard looks ahead: for each move it plays out the extra roll a 6, a
//     capture or a piece home brings, then every roll of the next opponent
//     (who answers as Normal would), and scores the positions with a
//     weighted evaluation tuned by self-play against Normal (cmd/ludo-arena
//     -tune). It decides in well under a millisecond.

// Bot decides for one seat. Decide is called when the View says the seat is
// to roll or to move (View.You.ToRoll or ToMove).
type Bot interface {
	Decide(v *View) (Command, error)
}

// Difficulty names.
const (
	Easy   = "easy"
	Normal = "normal"
	Hard   = "hard"
)

// Levels are the computer levels Ludo offers, weakest first.
var Levels = []string{Easy, Normal, Hard}

// NewBot returns a built-in bot; seed makes its random choices reproducible.
func NewBot(difficulty string, seed uint64) (Bot, error) {
	rng := mrand.New(mrand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	switch difficulty {
	case Easy:
		return &easyBot{rng: rng}, nil
	case Normal, "":
		return &normalBot{rng: rng}, nil
	case Hard:
		return &hardBot{rng: rng, w: HardWeights}, nil
	}
	return nil, fmt.Errorf("unknown bot difficulty %q", difficulty)
}

// chooser picks a move in a position; the arena calls it directly on games
// (no View needed), the server through Decide.
type chooser interface {
	choose(pos *position, moves []Move) int
}

// Choose is the bot's move for the player to act in a game (the arena and
// tests; the server goes through Decide).
func Choose(b Bot, g *Game) (Command, error) {
	if g.Current() == nil {
		return Command{}, errors.New("nobody is to act")
	}
	if g.Phase == PhaseRoll {
		return Command{Type: "roll"}, nil
	}
	c, ok := b.(chooser)
	if !ok {
		return b.Decide(g.View(g.Current().ID, Viewer{Role: "player"}))
	}
	pos := &position{r: g.rules(), players: g.Players, me: g.Turn, roll: g.Dice}
	return MoveCommand(g.Moves[c.choose(pos, g.Moves)].Piece), nil
}

func decide(c chooser, v *View) (Command, error) {
	if v == nil || v.You == nil {
		return Command{}, errors.New("not a seated player")
	}
	if v.You.ToRoll {
		return Command{Type: "roll"}, nil
	}
	if !v.You.ToMove || len(v.Moves) == 0 {
		return Command{}, errors.New("nothing to decide")
	}
	pos, err := positionOf(v)
	if err != nil {
		return Command{}, err
	}
	return MoveCommand(v.Moves[c.choose(pos, v.Moves)].Piece), nil
}

// Validate checks a bot's command against the View.
func Validate(v *View, c Command) error {
	if v == nil || v.You == nil {
		return errors.New("not a seated player")
	}
	switch c.Type {
	case "roll":
		if !v.You.ToRoll {
			return errors.New("not this seat's roll")
		}
		return nil
	case "move":
		if !v.You.ToMove || c.Piece == nil {
			return errors.New("not this seat's move")
		}
		for _, m := range v.Moves {
			if m.Piece == *c.Piece {
				return nil
			}
		}
		return errors.New("that piece can't move")
	}
	return fmt.Errorf("unknown action %q", c.Type)
}

// Safe is the fallback decision: roll, or move the first movable piece.
func Safe(v *View) (Command, bool) {
	if v == nil || v.You == nil {
		return Command{}, false
	}
	if v.You.ToRoll {
		return Command{Type: "roll"}, true
	}
	if v.You.ToMove && len(v.Moves) > 0 {
		return MoveCommand(v.Moves[0].Piece), true
	}
	return Command{}, false
}

// ---------------------------------------------------------------- positions

// position is a game as the bots search it: the rules, every player's
// pieces and whose turn it is. Searches work on clones.
type position struct {
	r       rules
	players []*Player
	me      int // the player to act
	roll    int // the number they rolled (the moves being chosen are for it)
}

// positionOf rebuilds the position a View describes.
func positionOf(v *View) (*position, error) {
	pos := &position{r: rules{b: NewBoard(v.Board.Arms), s: v.Settings}, me: -1, roll: v.Dice}
	for i, pv := range v.Players {
		p := &Player{ID: pv.ID, Arm: pv.Arm, Captures: pv.Captures, Left: pv.Status == "left", Place: pv.Place, Pieces: make([]Piece, len(pv.Pieces))}
		for j, pc := range pv.Pieces {
			p.Pieces[j] = Piece{Zone: pc.Zone, Step: pc.Step}
		}
		if pv.Status == "playing" {
			p.Place = 0
		}
		pos.players = append(pos.players, p)
		if pv.ID == v.TurnID {
			pos.me = i
		}
	}
	if pos.me < 0 {
		return nil, errors.New("nobody is to act")
	}
	return pos, nil
}

func clonePlayers(ps []*Player) []*Player {
	out := make([]*Player, len(ps))
	for i, p := range ps {
		q := *p
		q.Pieces = append([]Piece(nil), p.Pieces...)
		out[i] = &q
	}
	return out
}

// threat is the chance that a piece of player q standing on pc is captured
// before q's next turn: every other player still playing gets one roll
// (plus the extra roll after a 6) at it. Pieces behind it within reach,
// and pieces waiting in a yard whose start square it stands on or just
// past, count; a piece that would turn into its own home column first
// doesn't.
func (pos *position) threat(q int, pc Piece) float64 {
	r := pos.r
	p := pos.players[q]
	if r.safeAt(p, pc) {
		return 0
	}
	square := r.b.Square(p.Arm, pc.Step)
	survive := 1.0
	for o, opp := range pos.players {
		if o == q || !opp.active() {
			continue
		}
		var reach [13]bool // reach[d]: some piece of opp lands here with a total of d
		yard := false
		for _, op := range opp.Pieces {
			switch op.Zone {
			case ZoneYard:
				yard = true
			case ZoneTrack:
				d := (square - r.b.Square(opp.Arm, op.Step) + r.b.Ring) % r.b.Ring
				if d < 1 || d > 12 {
					continue
				}
				if r.mayEnter(opp) && op.Step <= r.b.LastStep() && op.Step+d > r.b.LastStep() {
					continue // turns into its home column first
				}
				reach[d] = true
			}
		}
		if yard {
			// Out on a 6 (or any roll in kids mode), then the extra roll.
			out := r.b.Start(opp.Arm)
			d := (square - out + r.b.Ring) % r.b.Ring
			if d == 0 && !r.s.SafeSquares {
				reach[6] = true
			} else if d >= 1 && d <= 6 && d+6 <= 12 {
				reach[d+6] = true
			}
		}
		p := 0.0
		for d := 1; d <= 6; d++ {
			if reach[d] {
				p += 1.0 / 6
			}
		}
		for d := 7; d <= 12; d++ {
			if reach[d] {
				p += 1.0 / 36
			}
		}
		survive *= 1 - min(p, 1)
	}
	return 1 - survive
}

// ---------------------------------------------------------------- Easy

type easyBot struct{ rng *mrand.Rand }

func (b *easyBot) Decide(v *View) (Command, error) { return decide(b, v) }
func (b *easyBot) choose(_ *position, moves []Move) int {
	return b.rng.IntN(len(moves))
}

// ---------------------------------------------------------------- Normal

type normalBot struct{ rng *mrand.Rand }

func (b *normalBot) Decide(v *View) (Command, error) { return decide(b, v) }

func (b *normalBot) choose(pos *position, moves []Move) int {
	best, bestScore := 0, -1e18
	for i, m := range moves {
		s := normalScore(pos, m) + b.rng.Float64()*0.01 // a hair of noise breaks ties
		if s > bestScore {
			best, bestScore = i, s
		}
	}
	return best
}

// normalScore is Normal's rule-of-thumb value of a move.
func normalScore(pos *position, m Move) float64 {
	r := pos.r
	me := pos.players[pos.me]
	from := me.Pieces[m.Piece]
	stepsHome := float64(r.b.StepsHome())
	advanced := float64(r.b.Travelled(from)) / stepsHome // 0 in the yard, 1 nearly home
	s := 0.0
	if m.Capture > 0 {
		s += 100 * float64(m.Capture)
	}
	switch {
	case m.Home:
		s += 60
	case m.To.Zone == ZoneColumn && from.Zone == ZoneTrack:
		s += 30
	case m.Out:
		s += 40
	}
	danger := pos.threat(pos.me, from)
	if danger > 0 && (m.Safe || m.To.Zone != ZoneTrack) {
		s += 35 * danger * (1 + advanced) // escapes
	}
	if m.Safe && m.To.Zone == ZoneTrack && !m.Out {
		s += 15
	}
	if m.To.Zone == ZoneTrack && !m.Safe {
		after := clonePlayers(pos.players)
		r.apply(after, pos.me, m.Piece, m.To, nil)
		risk := (&position{r: r, players: after, me: pos.me}).threat(pos.me, m.To)
		s -= 50 * risk * (1 + advanced)
		if from.Zone == ZoneTrack && danger == 0 && risk > 0 {
			s -= 10 // leaves a quiet square for a risky one
		}
	}
	// Otherwise: push the piece that has furthest to go (spreads the risk) a
	// little, and the one nearly home a little more.
	s += 4*advanced + float64(len(m.Path))*0.2
	return s
}

// ---------------------------------------------------------------- Hard

// Weights are Hard's evaluation weights (cmd/ludo-arena -tune searches them).
type Weights struct {
	Step    float64 `json:"step"`    // each step a piece has travelled
	Out     float64 `json:"out"`     // a piece out of the yard
	Column  float64 `json:"column"`  // a piece safe in its home column
	Home    float64 `json:"home"`    // a piece home
	Risk    float64 `json:"risk"`    // multiplies chance-of-capture × what the piece would lose
	Opp     float64 `json:"opp"`     // weight of the opponents' evaluation against your own
	Leader  float64 `json:"leader"`  // extra weight on the strongest opponent
	Spread  float64 `json:"spread"`  // pieces out on the board (more choice later)
	Blocker float64 `json:"blocker"` // a block of your own (blocks rule only)
}

// HardWeights are the tuned weights (cmd/ludo-arena -tune, 30 rounds of 800
// games against Normal: 58.5% → 60.2% wins over 4000 two-player games; the defaults
// before tuning were Step 1, Out 10, Column 8, Home 12, Risk 1, Opp 0.5,
// Leader 0.3, Spread 1, Blocker 3).
var HardWeights = Weights{Step: 1, Out: 14.9, Column: 2.15, Home: 3.1, Risk: 0.62, Opp: 0.72, Leader: 1.48, Spread: 1.6, Blocker: 8.5}

type hardBot struct {
	rng *mrand.Rand
	w   Weights
}

// NewHard returns a Hard bot with given weights (tuning and tests).
func NewHard(w Weights, seed uint64) Bot {
	return &hardBot{rng: mrand.New(mrand.NewPCG(seed, seed^0x51)), w: w}
}

func (b *hardBot) Decide(v *View) (Command, error) { return decide(b, v) }

func (b *hardBot) choose(pos *position, moves []Move) int {
	if len(moves) == 1 {
		return 0
	}
	best, bestScore := 0, -1e18
	for i, m := range moves {
		s := b.moveValue(pos, m) + b.rng.Float64()*1e-6
		if s > bestScore {
			best, bestScore = i, s
		}
	}
	return best
}

// moveValue: apply the move; if it earns another roll, average over that
// roll the best follow-up; then let the next opponent answer every roll.
func (b *hardBot) moveValue(pos *position, m Move) float64 {
	r := pos.r
	after := clonePlayers(pos.players)
	hits := r.apply(after, pos.me, m.Piece, m.To, nil)
	next := &position{r: r, players: after, me: pos.me}
	again := pos.roll == 6 || m.Home && r.s.HomeBonus || len(hits) > 0
	if again && !after[pos.me].allHome() {
		// An extra roll: the best follow-up for each number, then the reply.
		total := 0.0
		for roll := 1; roll <= 6; roll++ {
			moves := r.legal(after, pos.me, roll)
			if len(moves) == 0 {
				total += b.replied(next)
				continue
			}
			bestHere := -1e18
			for _, m2 := range moves {
				p2 := clonePlayers(after)
				r.apply(p2, pos.me, m2.Piece, m2.To, nil)
				v := b.replied(&position{r: r, players: p2, me: pos.me})
				bestHere = max(bestHere, v)
			}
			total += bestHere
		}
		return total / 6
	}
	return b.replied(next)
}

// replied is the position's value for pos.me after the next opponent in
// turn order answers each roll with Normal's choice.
func (b *hardBot) replied(pos *position) float64 {
	n := len(pos.players)
	opp := -1
	for k := 1; k < n; k++ {
		i := (pos.me + k) % n
		if pos.players[i].active() {
			opp = i
			break
		}
	}
	if opp < 0 {
		return b.eval(pos)
	}
	total := 0.0
	for roll := 1; roll <= 6; roll++ {
		moves := pos.r.legal(pos.players, opp, roll)
		if len(moves) == 0 {
			total += b.eval(pos)
			continue
		}
		at := &position{r: pos.r, players: pos.players, me: opp}
		pick, pickScore := 0, -1e18
		for i, m := range moves {
			if s := normalScore(at, m); s > pickScore {
				pick, pickScore = i, s
			}
		}
		after := clonePlayers(pos.players)
		pos.r.apply(after, opp, moves[pick].Piece, moves[pick].To, nil)
		total += b.eval(&position{r: pos.r, players: after, me: pos.me})
	}
	return total / 6
}

// eval is the position's value for pos.me: their pieces' worth minus the
// opponents' (the strongest weighing a little more).
func (b *hardBot) eval(pos *position) float64 {
	mine := b.worth(pos, pos.me)
	sum, strongest, count := 0.0, -1e18, 0
	for i, p := range pos.players {
		if i == pos.me || !p.active() {
			continue
		}
		v := b.worth(pos, i)
		sum += v
		strongest = max(strongest, v)
		count++
	}
	if count == 0 {
		return mine
	}
	return mine - b.w.Opp*sum/float64(count) - b.w.Leader*strongest
}

// worth is what one player's pieces are worth: steps travelled plus bonuses
// for being out, safe in the column or home, minus each piece's chance of
// being captured times what it would lose.
func (b *hardBot) worth(pos *position, q int) float64 {
	r := pos.r
	p := pos.players[q]
	w := b.w
	total := 0.0
	onBoard := 0
	for _, pc := range p.Pieces {
		switch pc.Zone {
		case ZoneYard:
			continue
		case ZoneHome:
			total += w.Step*float64(r.b.StepsHome()) + w.Home + w.Out
			continue
		case ZoneColumn:
			total += w.Step*float64(r.b.Travelled(pc)) + w.Column + w.Out
			continue
		}
		onBoard++
		value := w.Step*float64(r.b.Travelled(pc)) + w.Out
		total += value - w.Risk*pos.threat(q, pc)*value
	}
	total += w.Spread * float64(min(onBoard, 2))
	if r.s.Blocks {
		seen := map[int]int{}
		for _, pc := range p.Pieces {
			if pc.Zone == ZoneTrack {
				seen[r.b.Square(p.Arm, pc.Step)]++
			}
		}
		for _, n := range seen {
			if n >= 2 {
				total += w.Blocker
			}
		}
	}
	return total
}
