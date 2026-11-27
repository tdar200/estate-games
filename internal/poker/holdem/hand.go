// Portions of this file (side-pot construction and showdown reveal order)
// are derived from github.com/alexclewontin/riverboat (game.go, views.go),
// Copyright (c) 2020, Alex Lewontin, BSD-2-Clause; see LICENSE-riverboat.

package holdem

import (
	"errors"
	"fmt"
	"sort"
)

// Street is the betting round.
type Street int

const (
	Preflop Street = iota
	Flop
	Turn
	River
)

var streetNames = [...]string{"preflop", "flop", "turn", "river"}

func (s Street) String() string {
	if s >= 0 && int(s) < len(streetNames) {
		return streetNames[s]
	}
	return "unknown"
}

// ActionType names a player action or a forced bet.
type ActionType string

const (
	ActFold  ActionType = "fold"
	ActCheck ActionType = "check"
	ActCall  ActionType = "call"
	ActBet   ActionType = "bet"   // first wager of a street (To = amount)
	ActRaise ActionType = "raise" // raise to To
	ActAllIn ActionType = "allin" // as much as the rules allow: raise all-in or call

	// Forced bets, recorded in the hand log only.
	ActAnte       ActionType = "ante"
	ActSmallBlind ActionType = "smallBlind"
	ActBigBlind   ActionType = "bigBlind"
)

// Action is a player's decision. For bet/raise, To is the total the player's
// bet on this street becomes ("raise to"), not the increment.
type Action struct {
	Type ActionType `json:"type"`
	To   int        `json:"to,omitempty"`
}

// ActionRecord is one entry of the hand log.
type ActionRecord struct {
	Street Street     `json:"street"`
	Player int        `json:"player"`
	Type   ActionType `json:"type"`
	Amount int        `json:"amount"`          // chips moved from stack into the pot by this action
	To     int        `json:"to"`              // the player's street bet afterwards
	AllIn  bool       `json:"allIn,omitempty"` // the action left the player with no chips
	Forced bool       `json:"forced,omitempty"`
}

// Legal describes what the player to act may do.
type Legal struct {
	Player     int  // hand index of the actor, -1 if nobody
	CanFold    bool // false when checking is free
	CanCheck   bool
	CallAmount int  // chips a call adds (capped by the stack); 0 when checking
	CanRaise   bool // a bet or raise is allowed
	MinTo      int  // smallest legal bet/raise-to (an all-in for less is allowed when it is the whole stack)
	MaxTo      int  // largest (all-in)
	IsBet      bool // the wager would open the street (no bet to call)
	CurrentBet int  // the highest street bet
}

// Uncalled is a bet returned to its owner because nobody matched it.
type Uncalled struct {
	Player int `json:"player"`
	Amount int `json:"amount"`
}

// Pot is a main or side pot at showdown.
type Pot struct {
	Amount   int   `json:"amount"`
	Eligible []int `json:"eligible"` // live players who can win it, by hand index
	Winners  []int `json:"winners"`
	Shares   []int `json:"shares"` // chips each winner receives, parallel to Winners
}

// HandResult is filled in when the hand ends.
type HandResult struct {
	Showdown    bool       `json:"showdown"` // cards were compared (false: everyone else folded)
	Pots        []Pot      `json:"pots"`
	Won         []int      `json:"won"`    // total chips awarded per player
	Scores      []Score    `json:"scores"` // per player; worstScore for folded players
	Names       []string   `json:"names"`  // hand names of live players at showdown
	Best        []cardList `json:"best"`   // best five cards of live players at showdown
	Mucked      []bool     `json:"mucked"` // live at showdown but not shown (auto-muck)
	FirstToShow int        `json:"firstToShow"`
}

// HandConfig starts a hand. Players are given in hand order: index 0 sits
// directly left of the button and the last index is the button (as in
// PokerKit). Heads-up, index 1 is the button and posts the small blind.
type HandConfig struct {
	SmallBlind int
	BigBlind   int
	Ante       int
	Stacks     []int
	// Deck is dealt from the front: two passes of hole cards starting at
	// index 0, then burn+3, burn+1, burn+1. It must hold 52 distinct cards.
	Deck []Card
	// PauseRunout makes an all-in run-out wait for DealNext between streets,
	// so a table can reveal the board slowly. Otherwise it completes at once.
	PauseRunout bool
}

// Hand is one hand of No-Limit Hold'em. All fields are exported so a hand
// can be snapshotted and restored with encoding/json; mutate it only through
// its methods.
type Hand struct {
	N           int        `json:"n"`
	SmallBlind  int        `json:"smallBlind"`
	BigBlind    int        `json:"bigBlind"`
	Ante        int        `json:"ante"`
	StartStacks []int      `json:"startStacks"`
	Stacks      []int      `json:"stacks"`
	Bets        []int      `json:"bets"`    // this street
	Contrib     []int      `json:"contrib"` // whole hand, antes and blinds included
	Folded      []bool     `json:"folded"`
	Hole        []cardList `json:"hole"`
	Board       cardList   `json:"board"`
	Deck        cardList   `json:"deck"`
	DeckPos     int        `json:"deckPos"`
	Street      Street     `json:"street"`

	// Betting-round state (PokerKit semantics).
	Actors      []int  `json:"actors"`      // queue; Actors[0] is to act
	Acted       []bool `json:"acted"`       // acted since the last full raise
	ShortAllIns []int  `json:"shortAllIns"` // consecutive short all-in raise increments
	RaiseInc    int    `json:"raiseInc"`    // largest bet/raise increment this street
	Aggressor   int    `json:"aggressor"`   // last bettor/raiser this street, -1 if none

	Runout      bool           `json:"runout"` // betting is over; the board is being run out
	PauseRunout bool           `json:"pauseRunout"`
	Done        bool           `json:"done"`
	Shown       []bool         `json:"shown"` // hole cards face up
	Log         []ActionRecord `json:"log"`
	Uncalled    []Uncalled     `json:"uncalled"`
	Result      *HandResult    `json:"result"`
}

// Errors returned by Act.
var (
	ErrHandOver      = errors.New("the hand is over")
	ErrNotYourTurn   = errors.New("it is not your turn")
	ErrCannotFold    = errors.New("nothing to call: check instead of folding")
	ErrCannotCheck   = errors.New("there is a bet to call")
	ErrCannotRaise   = errors.New("raising is not allowed here")
	ErrBadAmount     = errors.New("that bet size is not allowed")
	ErrUnknownAction = errors.New("unknown action")
)

// NewHand posts antes and blinds, deals hole cards and starts preflop betting.
func NewHand(cfg HandConfig) (*Hand, error) {
	n := len(cfg.Stacks)
	if n < 2 || n > 22 {
		return nil, fmt.Errorf("a hand needs 2–22 players, got %d", n)
	}
	if cfg.BigBlind <= 0 || cfg.SmallBlind < 0 || cfg.Ante < 0 || cfg.SmallBlind > cfg.BigBlind {
		return nil, errors.New("invalid blinds or ante")
	}
	for _, s := range cfg.Stacks {
		if s <= 0 {
			return nil, errors.New("every player in a hand needs chips")
		}
	}
	if err := checkDeck(cfg.Deck); err != nil {
		return nil, err
	}
	h := &Hand{
		N:           n,
		SmallBlind:  cfg.SmallBlind,
		BigBlind:    cfg.BigBlind,
		Ante:        cfg.Ante,
		StartStacks: append([]int(nil), cfg.Stacks...),
		Stacks:      append([]int(nil), cfg.Stacks...),
		Bets:        make([]int, n),
		Contrib:     make([]int, n),
		Folded:      make([]bool, n),
		Hole:        make([]cardList, n),
		Deck:        append(cardList(nil), cfg.Deck...),
		Acted:       make([]bool, n),
		Shown:       make([]bool, n),
		Aggressor:   -1,
		PauseRunout: cfg.PauseRunout,
		Uncalled:    []Uncalled{},
		Log:         []ActionRecord{},
		Board:       cardList{},
	}
	// Antes are dead money; then the blinds come out of what is left.
	if h.Ante > 0 {
		for i := 0; i < n; i++ {
			amt := min(h.Ante, h.Stacks[i])
			h.Stacks[i] -= amt
			h.Contrib[i] += amt
			h.Log = append(h.Log, ActionRecord{Street: Preflop, Player: i, Type: ActAnte, Amount: amt, AllIn: h.Stacks[i] == 0, Forced: true})
		}
	}
	sb, bb := h.blindSeats()
	h.post(sb, h.SmallBlind, ActSmallBlind)
	h.post(bb, h.BigBlind, ActBigBlind)
	for pass := 0; pass < 2; pass++ {
		for i := 0; i < n; i++ {
			h.Hole[i] = append(h.Hole[i], h.draw())
		}
	}
	h.beginBetting()
	return h, nil
}

func checkDeck(deck []Card) error {
	if len(deck) != 52 {
		return errors.New("the deck must hold 52 cards")
	}
	seen := map[Card]bool{}
	for _, c := range deck {
		if c == 0 || seen[c] {
			return errors.New("the deck has a missing or repeated card")
		}
		seen[c] = true
	}
	return nil
}

// blindSeats returns the hand indices of the small and big blind.
func (h *Hand) blindSeats() (sb, bb int) {
	if h.N == 2 {
		return 1, 0 // heads-up: the button posts the small blind
	}
	return 0, 1
}

func (h *Hand) post(i, blind int, kind ActionType) {
	amt := min(blind, h.Stacks[i])
	h.Stacks[i] -= amt
	h.Bets[i] += amt
	h.Contrib[i] += amt
	h.Log = append(h.Log, ActionRecord{Street: Preflop, Player: i, Type: kind, Amount: amt, To: h.Bets[i], AllIn: h.Stacks[i] == 0, Forced: true})
}

func (h *Hand) draw() Card {
	c := h.Deck[h.DeckPos]
	h.DeckPos++
	return c
}

// MaxBet is the highest bet on the current street.
func (h *Hand) MaxBet() int {
	m := 0
	for _, b := range h.Bets {
		m = max(m, b)
	}
	return m
}

// Pot is every chip committed this hand (collected pots plus street bets).
func (h *Hand) Pot() int {
	t := 0
	for _, c := range h.Contrib {
		t += c
	}
	return t
}

// Live is the number of players who have not folded.
func (h *Hand) Live() int {
	n := 0
	for _, f := range h.Folded {
		if !f {
			n++
		}
	}
	return n
}

func (h *Hand) isBlind(i int) bool {
	sb, bb := h.blindSeats()
	return i == sb || i == bb
}

// effectiveStack is what player i can still lose (PokerKit's definition).
func (h *Hand) effectiveStack(i int) int {
	if h.Folded[i] {
		return 0
	}
	var totals []int
	for j := 0; j < h.N; j++ {
		if !h.Folded[j] {
			totals = append(totals, h.Bets[j]+h.Stacks[j])
		}
	}
	if len(totals) < 2 {
		return 0
	}
	sort.Ints(totals)
	return min(h.Stacks[i], max(0, totals[len(totals)-2]-h.Bets[i]))
}

func (h *Hand) beginBetting() {
	// The opener sits after the largest blind preflop (ties go to the later
	// seat); after the flop every bet is zero, so it is index 0.
	best, bestKey := 0, -1
	for i := 0; i < h.N; i++ {
		key := 0
		if h.Street == Preflop && h.isBlind(i) {
			key = h.Bets[i]
		}
		if key >= bestKey {
			best, bestKey = i, key
		}
	}
	opener := (best + 1) % h.N
	h.Actors = h.Actors[:0]
	for k := 0; k < h.N; k++ {
		i := (opener + k) % h.N
		if h.Folded[i] || h.Stacks[i] == 0 || h.effectiveStack(i) == 0 {
			continue
		}
		h.Actors = append(h.Actors, i)
	}
	h.RaiseInc = 0
	h.ShortAllIns = nil
	h.Aggressor = -1
	for i := range h.Acted {
		h.Acted[i] = false
	}
	if len(h.Actors) == 0 || h.Live() <= 1 || (len(h.Actors) == 1 && h.Bets[h.Actors[0]] >= h.MaxBet()) {
		h.endBetting()
	}
}

// Actor returns the hand index of the player to act, or -1.
func (h *Hand) Actor() int {
	if h.Done || len(h.Actors) == 0 {
		return -1
	}
	return h.Actors[0]
}

// Legal reports the actor's options.
func (h *Hand) Legal() Legal {
	i := h.Actor()
	l := Legal{Player: i, CurrentBet: h.MaxBet()}
	if i < 0 {
		return l
	}
	maxBet := l.CurrentBet
	toCall := maxBet - h.Bets[i]
	l.CallAmount = min(h.Stacks[i], toCall)
	l.CanCheck = toCall == 0
	l.CanFold = toCall > 0
	l.IsBet = maxBet == 0
	l.CanRaise = h.canRaise(i)
	if l.CanRaise {
		l.MaxTo = h.Stacks[i] + h.Bets[i]
		l.MinTo = min(l.MaxTo, maxBet+max(h.RaiseInc, h.BigBlind))
	}
	return l
}

func (h *Hand) canRaise(i int) bool {
	maxBet := h.MaxBet()
	callAmt := min(h.Stacks[i], maxBet-h.Bets[i])
	if callAmt < h.RaiseInc {
		return false // facing only a short all-in after acting: betting is not reopened
	}
	if len(h.ShortAllIns) > 0 && sum(h.ShortAllIns) < h.RaiseInc && h.Acted[i] {
		return false
	}
	if h.Stacks[i] <= maxBet-h.Bets[i] {
		return false // calling puts the player all-in
	}
	for j := 0; j < h.N; j++ {
		if j != i && !h.Folded[j] && h.Stacks[j]+h.Bets[j] > maxBet {
			return true
		}
	}
	return false // everyone else is all-in or folded: just call
}

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

// Act applies the actor's decision. On error the hand is unchanged.
func (h *Hand) Act(a Action) error {
	if h.Done {
		return ErrHandOver
	}
	i := h.Actor()
	if i < 0 {
		return ErrNotYourTurn
	}
	l := h.Legal()
	switch a.Type {
	case ActFold:
		if !l.CanFold {
			return ErrCannotFold
		}
		h.popActor()
		h.Folded[i] = true
		h.record(i, ActFold, 0)
	case ActCheck, ActCall:
		if a.Type == ActCheck && !l.CanCheck {
			return ErrCannotCheck
		}
		h.popActor()
		h.putIn(i, l.CallAmount)
		kind := ActCall
		if l.CallAmount == 0 {
			kind = ActCheck
		}
		h.record(i, kind, l.CallAmount)
	case ActBet, ActRaise, ActAllIn:
		to := a.To
		if a.Type == ActAllIn {
			if !l.CanRaise {
				// Can't raise: all-in means calling with what is left.
				if l.CallAmount == 0 {
					return ErrCannotRaise
				}
				return h.Act(Action{Type: ActCall})
			}
			to = l.MaxTo
		}
		if !l.CanRaise {
			return ErrCannotRaise
		}
		if to < l.MinTo || to > l.MaxTo {
			return fmt.Errorf("%w: raise to between %d and %d", ErrBadAmount, l.MinTo, l.MaxTo)
		}
		h.raiseTo(i, to, l)
	default:
		return ErrUnknownAction
	}
	if len(h.Actors) == 0 || h.Live() <= 1 {
		h.endBetting()
	}
	return nil
}

func (h *Hand) popActor() int {
	i := h.Actors[0]
	h.Actors = h.Actors[1:]
	h.Acted[i] = true
	return i
}

func (h *Hand) putIn(i, amt int) {
	h.Stacks[i] -= amt
	h.Bets[i] += amt
	h.Contrib[i] += amt
}

func (h *Hand) record(i int, kind ActionType, amt int) {
	h.Log = append(h.Log, ActionRecord{Street: h.Street, Player: i, Type: kind, Amount: amt, To: h.Bets[i], AllIn: h.Stacks[i] == 0 && !h.Folded[i]})
}

func (h *Hand) raiseTo(i, to int, l Legal) {
	maxBet := l.CurrentBet
	h.popActor()
	inc := to - maxBet
	delta := to - h.Bets[i]
	h.putIn(i, delta)
	kind := ActRaise
	if maxBet == 0 {
		kind = ActBet
	}
	h.record(i, kind, delta)
	h.Aggressor = i
	// Everyone else still holding chips must act again.
	h.Actors = h.Actors[:0]
	for k := 1; k < h.N; k++ {
		j := (i + k) % h.N
		if !h.Folded[j] && h.Stacks[j] > 0 {
			h.Actors = append(h.Actors, j)
		}
	}
	if inc >= h.RaiseInc {
		// A full bet or raise reopens the betting for everyone.
		for j := range h.Acted {
			h.Acted[j] = false
		}
		h.Acted[i] = true
	}
	h.RaiseInc = max(h.RaiseInc, inc)
	if h.Stacks[i] > 0 {
		h.ShortAllIns = nil
	} else {
		h.ShortAllIns = append(h.ShortAllIns, inc)
	}
	if sum(h.ShortAllIns) >= h.RaiseInc {
		h.ShortAllIns = nil
	}
}

// ForceFold folds player i out of turn (they left the table). It is a no-op
// if the player already folded or the hand is over.
func (h *Hand) ForceFold(i int) {
	if h.Done || i < 0 || i >= h.N || h.Folded[i] {
		return
	}
	h.Folded[i] = true
	h.Acted[i] = true
	for k, j := range h.Actors {
		if j == i {
			h.Actors = append(h.Actors[:k:k], h.Actors[k+1:]...)
			break
		}
	}
	h.Log = append(h.Log, ActionRecord{Street: h.Street, Player: i, Type: ActFold, Forced: true, To: h.Bets[i]})
	if h.Runout {
		if h.Live() <= 1 {
			h.finish()
		}
		return
	}
	if len(h.Actors) == 0 || h.Live() <= 1 {
		h.endBetting()
	}
}

// returnUncalled gives back the part of the top bet that nobody matched.
func (h *Hand) returnUncalled() {
	top, second, who := 0, 0, -1
	for i, b := range h.Bets {
		if b > top {
			second, top, who = top, b, i
		} else if b > second {
			second = b
		}
	}
	if who >= 0 && top > second {
		amt := top - second
		h.Bets[who] -= amt
		h.Contrib[who] -= amt
		h.Stacks[who] += amt
		h.Uncalled = append(h.Uncalled, Uncalled{Player: who, Amount: amt})
	}
}

func (h *Hand) endBetting() {
	h.Actors = h.Actors[:0]
	h.returnUncalled()
	for i := range h.Bets {
		h.Bets[i] = 0
	}
	if h.Live() <= 1 || h.Street == River {
		h.finish()
		return
	}
	canAct := 0
	for i := 0; i < h.N; i++ {
		if !h.Folded[i] && h.Stacks[i] > 0 {
			canAct++
		}
	}
	if canAct <= 1 && !h.Runout {
		// Nobody can bet any more: table every live hand and run the board out.
		h.Runout = true
		for i := 0; i < h.N; i++ {
			if !h.Folded[i] {
				h.Shown[i] = true
			}
		}
	}
	if h.Runout && h.PauseRunout {
		return
	}
	h.dealStreet()
}

// NeedsDeal is true while a paused all-in run-out waits for DealNext.
func (h *Hand) NeedsDeal() bool { return h.Runout && !h.Done && h.PauseRunout }

// DealNext deals the next street of a paused run-out.
func (h *Hand) DealNext() error {
	if !h.NeedsDeal() {
		return errors.New("nothing to deal")
	}
	h.dealStreet()
	return nil
}

func (h *Hand) dealStreet() {
	h.Street++
	h.draw() // burn
	count := 1
	if h.Street == Flop {
		count = 3
	}
	for k := 0; k < count; k++ {
		h.Board = append(h.Board, h.draw())
	}
	if h.Runout {
		if h.Street == River {
			h.finish()
		} else if !h.PauseRunout {
			h.dealStreet()
		}
		return
	}
	h.beginBetting()
}

func (h *Hand) finish() {
	if h.Done {
		return
	}
	h.Done = true
	h.Actors = h.Actors[:0]
	res := &HandResult{
		Won:         make([]int, h.N),
		Scores:      make([]Score, h.N),
		Names:       make([]string, h.N),
		Best:        make([]cardList, h.N),
		Mucked:      make([]bool, h.N),
		FirstToShow: -1,
	}
	h.Result = res
	for i := range res.Scores {
		res.Scores[i] = worstScore
	}
	var live []int
	for i := 0; i < h.N; i++ {
		if !h.Folded[i] {
			live = append(live, i)
		}
	}
	if len(live) == 1 {
		w := live[0]
		total := h.Pot()
		res.Pots = []Pot{{Amount: total, Eligible: []int{w}, Winners: []int{w}, Shares: []int{total}}}
		res.Won[w] = total
		h.Stacks[w] += total
		return
	}
	res.Showdown = true
	h.trimExcess()
	for _, i := range live {
		cards := append([]Card{h.Hole[i][0], h.Hole[i][1]}, h.Board...)
		s, five := EvalBest(cards)
		res.Scores[i] = s
		res.Best[i] = five
		res.Names[i] = Describe(s, five)
	}
	res.Pots = h.buildPots()
	for p := range res.Pots {
		pot := &res.Pots[p]
		best := worstScore
		for _, i := range pot.Eligible {
			best = min(best, res.Scores[i])
		}
		for _, i := range pot.Eligible { // ascending index = clockwise from the button
			if res.Scores[i] == best {
				pot.Winners = append(pot.Winners, i)
			}
		}
		q, r := pot.Amount/len(pot.Winners), pot.Amount%len(pot.Winners)
		pot.Shares = make([]int, len(pot.Winners))
		for k, i := range pot.Winners {
			share := q
			if k < r {
				share++ // odd chips: one each, starting left of the button
			}
			pot.Shares[k] = share
			res.Won[i] += share
			h.Stacks[i] += share
		}
	}
	h.revealAtShowdown(live)
}

// trimExcess returns the part of the largest contribution that nobody else
// matched. Street bets are trimmed when each round ends; this also covers
// antes and blinds that opponents were too short to match.
func (h *Hand) trimExcess() {
	top, second, who := 0, 0, -1
	for i, c := range h.Contrib {
		if c > top {
			second, top, who = top, c, i
		} else if c > second {
			second = c
		}
	}
	if who < 0 || top <= second || h.Folded[who] {
		return
	}
	amt := top - second
	h.Contrib[who] -= amt
	h.Stacks[who] += amt
	for k := range h.Uncalled {
		if h.Uncalled[k].Player == who {
			h.Uncalled[k].Amount += amt
			return
		}
	}
	h.Uncalled = append(h.Uncalled, Uncalled{Player: who, Amount: amt})
}

// buildPots splits the contributions into a main pot and side pots. Pots
// with the same eligible players are merged.
func (h *Hand) buildPots() []Pot {
	levels := []int{}
	seen := map[int]bool{}
	for _, c := range h.Contrib {
		if c > 0 && !seen[c] {
			seen[c] = true
			levels = append(levels, c)
		}
	}
	sort.Ints(levels)
	var pots []Pot
	carry := 0
	prev := 0
	for _, level := range levels {
		amt := carry
		for _, c := range h.Contrib {
			amt += min(c, level) - min(c, prev)
		}
		prev = level
		var elig []int
		for i := 0; i < h.N; i++ {
			if !h.Folded[i] && h.Contrib[i] >= level {
				elig = append(elig, i)
			}
		}
		if len(elig) == 0 {
			// Only folded players reached this level (someone left mid-hand):
			// the chips join the pot below.
			if len(pots) > 0 {
				pots[len(pots)-1].Amount += amt
			} else {
				carry = amt
			}
			continue
		}
		carry = 0
		if len(pots) > 0 && equalInts(pots[len(pots)-1].Eligible, elig) {
			pots[len(pots)-1].Amount += amt
			continue
		}
		pots = append(pots, Pot{Amount: amt, Eligible: elig})
	}
	return pots
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// revealAtShowdown applies the show order: the last river aggressor (or the
// first live player left of the button) shows first; later players show
// only if they win something or beat or tie the best hand shown so far.
// Everyone else mucks unless they choose to show (Table "show" action).
func (h *Hand) revealAtShowdown(live []int) {
	res := h.Result
	first := live[0]
	if h.Street == River && h.Aggressor >= 0 && !h.Folded[h.Aggressor] && !h.Runout {
		first = h.Aggressor
	}
	res.FirstToShow = first
	bestShown := worstScore
	start := 0
	for k, i := range live {
		if i == first {
			start = k
		}
	}
	for k := 0; k < len(live); k++ {
		i := live[(start+k)%len(live)]
		if h.Shown[i] || k == 0 || res.Won[i] > 0 || res.Scores[i] <= bestShown {
			h.Shown[i] = true
			bestShown = min(bestShown, res.Scores[i])
		} else {
			res.Mucked[i] = true
		}
	}
}

// Show turns a player's hole cards face up (voluntary show after the hand).
func (h *Hand) Show(i int) {
	if i >= 0 && i < h.N {
		h.Shown[i] = true
		if h.Result != nil && i < len(h.Result.Mucked) {
			h.Result.Mucked[i] = false
		}
	}
}
