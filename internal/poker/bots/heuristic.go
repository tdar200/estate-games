package bots

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"sort"

	"github.com/tdar200/estate-games/internal/poker/holdem"
)

// Profile tunes the heuristic bot.
type Profile struct {
	Name       string
	Iterations int  // Monte Carlo deals per decision
	UseRanges  bool // model opponents from their actions (else any two cards)
	// OpenRange is the preflop percentile opened from early, middle and late
	// position.
	OpenRange [3]float64
	CallSlack float64 // calls when equity ≥ pot odds − CallSlack
	ValueBet  float64 // heads-up equity needed to bet for value
	RaiseEq   float64 // heads-up equity needed to raise a bet
	Bluff     float64 // chance to bluff when checked to in position
	CBet      float64 // chance to continuation-bet the flop heads-up as the preflop raiser
	Passive   float64 // chance to only call with a raising hand
	Mistake   float64 // chance of a deliberate random (but legal) decision
	Rebuy     bool
}

// NormalProfile plays a solid, slightly cautious game.
var NormalProfile = Profile{
	Name:       Normal,
	Iterations: 400,
	UseRanges:  true,
	OpenRange:  [3]float64{0.14, 0.22, 0.38},
	CallSlack:  0,
	ValueBet:   0.60,
	RaiseEq:    0.76,
	Bluff:      0.12,
	CBet:       0.55,
	Passive:    0.15,
	Rebuy:      true,
}

// EasyProfile plays too many hands, calls too much, rarely raises, ignores
// what opponents' bets mean, and makes a deliberate random mistake on about
// one decision in ten.
var EasyProfile = Profile{
	Name:       Easy,
	Iterations: 120,
	UseRanges:  false,
	OpenRange:  [3]float64{0.30, 0.40, 0.55},
	CallSlack:  0.12,
	ValueBet:   0.70,
	RaiseEq:    0.88,
	Bluff:      0.04,
	CBet:       0.25,
	Passive:    0.5,
	Mistake:    0.10,
	Rebuy:      true,
}

// Heuristic is the Easy/Normal bot: hand strength by Monte Carlo equity
// against plausible ranges, pot odds, position and stack depth, with
// occasional bluffs.
type Heuristic struct {
	p   Profile
	rng *rand.Rand
}

// NewHeuristic creates a bot; the seed makes every choice reproducible.
func NewHeuristic(p Profile, seed uint64) *Heuristic {
	return &Heuristic{p: p, rng: rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15))}
}

// situation is what the bot works out from the View.
type situation struct {
	v          *holdem.View
	l          *holdem.LegalView
	hole       [2]holdem.Card
	board      []holdem.Card
	bb         int
	pot        int // every chip in the middle, bets included
	toCall     int
	stack      int
	myBet      int
	opps       []Range
	nOpp       int
	inPosition bool
	behind     int // players still to act after us preflop
	raises     int // preflop bets/raises so far
	limpers    int
	aggressor  bool // we made the last preflop raise
	effBB      float64
	street     string
}

// Decide implements Bot.
func (b *Heuristic) Decide(ctx context.Context, v *holdem.View) (holdem.Command, error) {
	y := v.You
	if y == nil {
		return holdem.Command{}, errors.New("not a seated player")
	}
	if !y.ToAct {
		if y.CanRebuy {
			if b.p.Rebuy {
				return holdem.Command{Type: "rebuy"}, nil
			}
			return holdem.Command{Type: "declineRebuy"}, nil
		}
		return holdem.Command{}, errors.New("nothing to decide")
	}
	s, err := b.read(v)
	if err != nil {
		return holdem.Command{}, err
	}
	if b.rng.Float64() < b.p.Mistake {
		return b.mistake(s), nil
	}
	if s.street == "preflop" {
		return b.preflop(s), nil
	}
	return b.postflop(s), nil
}

func (b *Heuristic) read(v *holdem.View) (*situation, error) {
	y := v.You
	if len(y.Cards) != 2 || y.Legal == nil {
		return nil, errors.New("no cards or options")
	}
	s := &situation{v: v, l: y.Legal, bb: max(v.Level.BigBlind, 1), pot: v.TotalPot, street: v.Street}
	for k, c := range y.Cards {
		card, err := holdem.ParseCard(c)
		if err != nil {
			return nil, err
		}
		s.hole[k] = card
	}
	for _, c := range v.Board {
		card, err := holdem.ParseCard(c)
		if err != nil {
			return nil, err
		}
		s.board = append(s.board, card)
	}
	s.toCall = y.Legal.Call
	// Hand order: clockwise from the seat after the button; button last.
	var order []holdem.PlayerView
	for _, p := range v.Players {
		if p.InHand {
			order = append(order, p)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		ki, kj := order[i].Seat, order[j].Seat
		if ki <= v.ButtonSeat {
			ki += 100
		}
		if kj <= v.ButtonSeat {
			kj += 100
		}
		return ki < kj
	})
	hero := -1
	for i, p := range order {
		if p.ID == y.ID {
			hero = i
			s.stack, s.myBet = p.Stack, p.Bet
		}
	}
	if hero < 0 {
		return nil, errors.New("not in the hand")
	}
	// Preflop order starts after the big blind (heads-up: with the button).
	n := len(order)
	pre := make([]int, 0, n)
	for k := 0; k < n; k++ {
		start := 2
		if n == 2 {
			start = 1
		}
		pre = append(pre, (start+k)%n)
	}
	for k, i := range pre {
		if i == hero {
			for _, j := range pre[k+1:] {
				if order[j].Status != "folded" {
					s.behind++
				}
			}
		}
	}
	// Who is still in, and what did they do?
	lastLive := -1
	maxOpp := 0
	for i, p := range order {
		if p.Status == "folded" {
			continue
		}
		lastLive = i
		if i != hero {
			maxOpp = max(maxOpp, p.Stack+p.Bet)
		}
	}
	s.inPosition = lastLive == hero
	s.effBB = float64(min(s.stack+s.myBet, maxOpp)) / float64(s.bb)
	type history struct{ preRaise, preRaiseOverRaise, preCall, preCheck, aggr, calls int }
	hist := map[string]*history{}
	raisesSoFar := 0
	lastRaiser := ""
	for _, a := range v.Actions {
		h := hist[a.PlayerID]
		if h == nil {
			h = &history{}
			hist[a.PlayerID] = h
		}
		if a.Street == "preflop" {
			switch a.Type {
			case "bet", "raise":
				if raisesSoFar > 0 {
					h.preRaiseOverRaise++
				}
				h.preRaise++
				raisesSoFar++
				lastRaiser = a.PlayerID
			case "call":
				if raisesSoFar == 0 {
					s.limpers++
				}
				h.preCall++
			case "check":
				h.preCheck++
			}
			continue
		}
		switch a.Type {
		case "bet", "raise":
			h.aggr++
		case "call":
			h.calls++
		}
	}
	s.raises = raisesSoFar
	s.aggressor = lastRaiser == y.ID
	for i, p := range order {
		if i == hero || p.Status == "folded" {
			continue
		}
		s.nOpp++
		if !b.p.UseRanges {
			s.opps = append(s.opps, AnyTwo)
			continue
		}
		h := hist[p.ID]
		if h == nil {
			h = &history{}
		}
		r := Range{Top: 1, Leak: 0.05, Aggression: h.aggr, Calls: h.calls}
		switch {
		case h.preRaiseOverRaise > 0:
			r.Top = 0.08
		case h.preRaise > 0:
			r.Top = 0.25
		case h.preCall > 0 && raisesSoFar > 0:
			r.Top = 0.32
		case h.preCall > 0:
			r.Top = 0.6
		}
		// Short stacks shove and call wider.
		if total := p.Stack + p.Bet; total < 15*s.bb && r.Top < 1 {
			r.Top = math.Min(1, r.Top*1.6)
		}
		s.opps = append(s.opps, r)
	}
	return s, nil
}

func (b *Heuristic) equity(s *situation) float64 {
	return Equity(b.rng, s.hole, s.board, s.opps, b.p.Iterations)
}

// ---------------------------------------------------------------- actions

func (b *Heuristic) checkOrFold(s *situation) holdem.Command {
	if s.l.Check {
		return holdem.Command{Type: "check"}
	}
	return holdem.Command{Type: "fold"}
}

func (b *Heuristic) callOrCheck(s *situation) holdem.Command {
	if s.l.Check {
		return holdem.Command{Type: "check"}
	}
	return holdem.Command{Type: "call"}
}

// raiseTo bets or raises to about `to`, rounded and clamped to the legal
// range; near-all-in sizes become all-in. Falls back to calling.
func (b *Heuristic) raiseTo(s *situation, to int) holdem.Command {
	r := s.l.Raise
	if r == nil {
		return b.callOrCheck(s)
	}
	unit := max(1, s.bb/2)
	to = (to + unit/2) / unit * unit
	to = min(max(to, r.Min), r.Max)
	if float64(to) >= 0.7*float64(r.Max) {
		to = r.Max
	}
	kind := "raise"
	if r.IsBet {
		kind = "bet"
	}
	return holdem.Command{Type: kind, To: to}
}

func (b *Heuristic) allIn(s *situation) holdem.Command {
	if s.l.Raise != nil {
		return holdem.Command{Type: "allin"}
	}
	return b.callOrCheck(s)
}

func (b *Heuristic) preflop(s *situation) holdem.Command {
	pct := PreflopPercentile(s.hole[0], s.hole[1])
	late, mid := 2, 4
	pos := 0
	switch {
	case s.behind <= late:
		pos = 2
	case s.behind <= mid:
		pos = 1
	}
	open := b.p.OpenRange[pos]
	if s.nOpp <= 2 {
		open = math.Min(0.75, open*1.4) // short-handed: play more hands
	}
	// Short stacks: shove or fold.
	if s.effBB <= 12 {
		if s.raises == 0 {
			push := math.Min(0.75, open+0.03*(12-s.effBB))
			if pct <= push {
				return b.allIn(s)
			}
			return b.checkOrFold(s)
		}
		e := b.equity(s)
		need := float64(s.toCall) / float64(s.pot+s.toCall)
		if e >= need+0.03-b.p.CallSlack {
			return b.allIn(s)
		}
		return b.checkOrFold(s)
	}
	if s.raises == 0 {
		if s.toCall == 0 { // big blind option
			if pct <= open/3 {
				return b.raiseTo(s, 3*s.bb+s.limpers*s.bb)
			}
			return holdem.Command{Type: "check"}
		}
		if pct <= open {
			size := 5 * s.bb / 2
			if s.nOpp == 1 && s.l.Call*2 == s.bb { // small blind vs big blind
				size = 3 * s.bb
			}
			return b.raiseTo(s, size+s.limpers*s.bb)
		}
		// Complete the small blind or over-limp with playable hands.
		if s.toCall*2 <= s.bb && pct <= math.Min(0.8, open*2) {
			return holdem.Command{Type: "call"}
		}
		if s.limpers > 0 && pct <= open*1.4 {
			return holdem.Command{Type: "call"}
		}
		return b.checkOrFold(s)
	}
	// Facing a raise.
	e := b.equity(s)
	need := float64(s.toCall) / float64(s.pot+s.toCall)
	if (e >= 0.58 || pct <= 0.03) && b.rng.Float64() >= b.p.Passive {
		return b.raiseTo(s, 3*s.v.CurrentBet+s.myBet)
	}
	implied := 0.0
	if s.effBB > 40 && (holdem.Rank(s.hole[0]) == holdem.Rank(s.hole[1]) || holdem.Suit(s.hole[0]) == holdem.Suit(s.hole[1])) {
		implied = 0.05
	}
	if e >= need+0.04-implied-b.p.CallSlack {
		return b.callOrCheck(s)
	}
	return b.checkOrFold(s)
}

func (b *Heuristic) postflop(s *situation) holdem.Command {
	e := b.equity(s)
	adj := 0.07 * float64(s.nOpp-1)
	pot := s.pot
	if s.toCall == 0 {
		if e >= b.p.ValueBet+adj {
			frac := 0.5 + 0.35*(e-b.p.ValueBet)/(1-b.p.ValueBet) + 0.1*(b.rng.Float64()-0.5)
			return b.raiseTo(s, int(float64(pot)*frac))
		}
		bluff := false
		if s.aggressor && s.street == "flop" && s.nOpp == 1 && b.rng.Float64() < b.p.CBet {
			bluff = true
		} else if s.inPosition && s.nOpp <= 2 && b.rng.Float64() < b.p.Bluff {
			bluff = true
		}
		if bluff && e < b.p.ValueBet {
			return b.raiseTo(s, int(float64(pot)*(0.35+0.25*b.rng.Float64())))
		}
		return holdem.Command{Type: "check"}
	}
	need := float64(s.toCall) / float64(pot+s.toCall)
	if e >= b.p.RaiseEq+adj && s.l.Raise != nil && b.rng.Float64() >= b.p.Passive {
		return b.raiseTo(s, 3*s.v.CurrentBet)
	}
	implied := 0.02
	if s.street == "river" {
		implied = -0.02
	}
	if e >= need-implied-b.p.CallSlack {
		// Little behind after calling: commit instead.
		if s.stack-s.toCall < (pot+s.toCall)/4 && e >= need+0.1 {
			return b.allIn(s)
		}
		return holdem.Command{Type: "call"}
	}
	if s.inPosition && s.street != "river" && s.l.Raise != nil && b.rng.Float64() < b.p.Bluff/4 {
		return b.raiseTo(s, s.l.Raise.Min)
	}
	return b.checkOrFold(s)
}

// mistake is a deliberate, random but legal decision (Easy).
func (b *Heuristic) mistake(s *situation) holdem.Command {
	r := b.rng.Float64()
	switch {
	case r < 0.45:
		return b.callOrCheck(s)
	case r < 0.75 && s.l.Raise != nil:
		return b.raiseTo(s, s.l.Raise.Min)
	case r < 0.85 && s.l.Raise != nil && s.l.Raise.Max <= 20*s.bb:
		return b.allIn(s)
	case s.l.Fold:
		return holdem.Command{Type: "fold"}
	default:
		return b.callOrCheck(s)
	}
}
