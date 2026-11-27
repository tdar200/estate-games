package holdem

import (
	"reflect"
	"strings"
	"testing"
)

// stackedDeck builds a deck that deals the given hole cards (one string per
// player, in hand order) and board, then the remaining cards.
func stackedDeck(t testing.TB, holes []string, board string) []Card {
	t.Helper()
	n := len(holes)
	hole := make([][]Card, n)
	for i, s := range holes {
		hole[i] = MustCards(s)
	}
	var deck []Card
	used := map[Card]bool{}
	push := func(c Card) {
		if used[c] {
			t.Fatalf("card %s used twice", CardString(c))
		}
		used[c] = true
		deck = append(deck, c)
	}
	for pass := 0; pass < 2; pass++ {
		for i := 0; i < n; i++ {
			push(hole[i][pass])
		}
	}
	b := MustCards(board)
	spare := func() Card {
		for _, c := range NewDeck() {
			if !used[c] && !contains(b, c) {
				return c
			}
		}
		panic("no spare card")
	}
	if len(b) >= 3 {
		push(spare()) // burn
		push(b[0])
		push(b[1])
		push(b[2])
	}
	if len(b) >= 4 {
		push(spare())
		push(b[3])
	}
	if len(b) >= 5 {
		push(spare())
		push(b[4])
	}
	for _, c := range NewDeck() {
		if !used[c] {
			push(c)
		}
	}
	return deck
}

func contains(cs []Card, c Card) bool {
	for _, x := range cs {
		if x == c {
			return true
		}
	}
	return false
}

func mustHand(t *testing.T, cfg HandConfig) *Hand {
	t.Helper()
	h, err := NewHand(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func act(t *testing.T, h *Hand, who int, a Action) {
	t.Helper()
	if got := h.Actor(); got != who {
		t.Fatalf("actor = %d, want %d (log %+v)", got, who, h.Log)
	}
	if err := h.Act(a); err != nil {
		t.Fatalf("player %d %+v: %v", who, a, err)
	}
}

var (
	fold  = Action{Type: ActFold}
	check = Action{Type: ActCheck}
	call  = Action{Type: ActCall}
)

func raise(to int) Action { return Action{Type: ActRaise, To: to} }

func TestHeadsUpButtonPostsSmallBlindAndActsFirst(t *testing.T) {
	h := mustHand(t, HandConfig{SmallBlind: 10, BigBlind: 20, Stacks: []int{1000, 1000},
		Deck: stackedDeck(t, []string{"2c3d", "AsAh"}, "KcKd7h8s9s")})
	// index 1 is the button
	if h.Bets[1] != 10 || h.Bets[0] != 20 {
		t.Fatalf("bets %v: the button should post the small blind", h.Bets)
	}
	act(t, h, 1, call) // button acts first preflop
	act(t, h, 0, check)
	if h.Street != Flop {
		t.Fatalf("street %v", h.Street)
	}
	act(t, h, 0, check) // big blind acts first after the flop
	act(t, h, 1, check)
	act(t, h, 0, check)
	act(t, h, 1, check)
	act(t, h, 0, check)
	act(t, h, 1, check)
	if !h.Done || h.Result.Won[1] != 40 {
		t.Fatalf("done=%v result %+v", h.Done, h.Result)
	}
}

func TestBlindsAndActionOrderThreeHanded(t *testing.T) {
	h := mustHand(t, HandConfig{SmallBlind: 5, BigBlind: 10, Stacks: []int{500, 500, 500},
		Deck: stackedDeck(t, []string{"2c3d", "4c5d", "AsAh"}, "")})
	if !reflect.DeepEqual(h.Bets, []int{5, 10, 0}) {
		t.Fatalf("bets %v", h.Bets)
	}
	act(t, h, 2, call) // under the gun = button three-handed
	act(t, h, 0, call)
	l := h.Legal()
	if !l.CanCheck || !l.CanRaise || l.MinTo != 20 {
		t.Fatalf("big blind option %+v", l)
	}
	act(t, h, 1, check)
	act(t, h, 0, check) // small blind first after the flop
}

func TestMinRaiseTracksLargestRaise(t *testing.T) {
	h := mustHand(t, HandConfig{SmallBlind: 10, BigBlind: 20, Stacks: []int{1000, 1000, 1000, 1000},
		Deck: NewDeck()})
	l := h.Legal()
	if l.MinTo != 40 || l.MaxTo != 1000 {
		t.Fatalf("opening raise range %d-%d", l.MinTo, l.MaxTo)
	}
	if err := h.Act(raise(30)); err == nil {
		t.Fatal("a raise to 30 is below the minimum")
	}
	act(t, h, 2, raise(70)) // raise of 50
	if l := h.Legal(); l.MinTo != 120 {
		t.Fatalf("min re-raise to %d, want 120", l.MinTo)
	}
	act(t, h, 3, raise(200)) // raise of 130
	if l := h.Legal(); l.MinTo != 330 {
		t.Fatalf("min re-raise to %d, want 330", l.MinTo)
	}
}

func TestShortAllInDoesNotReopenBetting(t *testing.T) {
	// Button raises to 60; the small blind is all-in for 80 (a short raise
	// of 20). The big blind calls. The button already acted and faces only
	// 20 more: they may call or fold but not raise.
	h := mustHand(t, HandConfig{SmallBlind: 10, BigBlind: 20, Stacks: []int{80, 1000, 1000},
		Deck: NewDeck()})
	act(t, h, 2, raise(60))
	act(t, h, 0, Action{Type: ActAllIn})
	if h.Stacks[0] != 0 || h.Bets[0] != 80 {
		t.Fatalf("small blind should be all-in for 80: %v %v", h.Stacks, h.Bets)
	}
	if l := h.Legal(); !l.CanRaise {
		t.Fatalf("the big blind has not acted and may raise: %+v", l)
	}
	act(t, h, 1, call)
	l := h.Legal()
	if l.Player != 2 || l.CanRaise || l.CallAmount != 20 {
		t.Fatalf("button facing a short all-in: %+v", l)
	}
	if err := h.Act(raise(200)); err == nil {
		t.Fatal("raise should be refused")
	}
	act(t, h, 2, call)
	if h.Street != Flop {
		t.Fatalf("street %v", h.Street)
	}
}

func TestTwoShortAllInsAddingToAFullRaiseReopen(t *testing.T) {
	// Player 3 bets 100 on the flop; 4 and 0 go all-in for 150 and 200
	// (two short raises of 50 = one full raise); player 1 calls. Player 3 may
	// raise again.
	h := mustHand(t, HandConfig{SmallBlind: 10, BigBlind: 20, Stacks: []int{220, 1000, 1000, 1000, 170},
		Deck: NewDeck()})
	act(t, h, 2, fold)
	act(t, h, 3, call)
	act(t, h, 4, call)
	act(t, h, 0, call)
	act(t, h, 1, check)
	// flop: stacks 200, 980, -, 980, 150
	act(t, h, 0, check)
	act(t, h, 1, check)
	act(t, h, 3, Action{Type: ActBet, To: 100})
	act(t, h, 4, Action{Type: ActAllIn}) // to 150
	act(t, h, 0, Action{Type: ActAllIn}) // to 200
	act(t, h, 1, call)                   // keeps a live opponent with chips
	if l := h.Legal(); l.Player != 3 || !l.CanRaise || l.MinTo != 300 {
		t.Fatalf("player 3 should be able to re-raise to 300+: %+v", l)
	}
}

func TestSidePotsAndShowdown(t *testing.T) {
	// 0 has AA (100 chips), 1 has KK (300), 2 has QQ (1000); all-in preflop.
	deck := stackedDeck(t, []string{"AsAd", "KsKd", "QsQd"}, "2c3c4h9dJh")
	h := mustHand(t, HandConfig{SmallBlind: 10, BigBlind: 20, Stacks: []int{100, 300, 1000}, Deck: deck})
	act(t, h, 2, Action{Type: ActAllIn})
	act(t, h, 0, call)
	act(t, h, 1, call)
	if !h.Done {
		t.Fatal("hand should run out")
	}
	res := h.Result
	if len(res.Pots) != 2 || res.Pots[0].Amount != 300 || res.Pots[1].Amount != 400 {
		t.Fatalf("pots %+v", res.Pots)
	}
	if !reflect.DeepEqual(res.Pots[0].Winners, []int{0}) || !reflect.DeepEqual(res.Pots[1].Winners, []int{1}) {
		t.Fatalf("winners %+v", res.Pots)
	}
	if !reflect.DeepEqual(h.Stacks, []int{300, 400, 700}) {
		t.Fatalf("stacks %v", h.Stacks)
	}
	if len(h.Uncalled) != 1 || h.Uncalled[0] != (Uncalled{Player: 2, Amount: 700}) {
		t.Fatalf("uncalled %+v", h.Uncalled)
	}
	for i := 0; i < 3; i++ {
		if !h.Shown[i] {
			t.Fatalf("all-in hands are tabled: %v", h.Shown)
		}
	}
	if res.Names[0] != "Pair of Aces" {
		t.Fatalf("name %q", res.Names[0])
	}
}

func TestOddChipGoesLeftOfTheButton(t *testing.T) {
	// Board plays (royal flush); antes make the pot odd. Index 0 is first
	// left of the button and gets the odd chip.
	deck := stackedDeck(t, []string{"2c3d", "2d3c", "4c5d"}, "AsKsQsJsTs")
	h := mustHand(t, HandConfig{SmallBlind: 1, BigBlind: 2, Ante: 1, Stacks: []int{100, 100, 100}, Deck: deck})
	act(t, h, 2, fold)
	act(t, h, 0, call)
	act(t, h, 1, check)
	for i := 0; i < 3; i++ {
		act(t, h, 0, check)
		act(t, h, 1, check)
	}
	pot := h.Result.Pots[0]
	if pot.Amount != 7 || !reflect.DeepEqual(pot.Shares, []int{4, 3}) || pot.Winners[0] != 0 {
		t.Fatalf("pot %+v", pot)
	}
}

func TestOddChipsOneEachThreeWays(t *testing.T) {
	deck := stackedDeck(t, []string{"2c3d", "2d3c", "2h3h", "4c5d"}, "AsKsQsJsTs")
	h := mustHand(t, HandConfig{SmallBlind: 1, BigBlind: 2, Ante: 1, Stacks: []int{100, 100, 100, 100}, Deck: deck})
	act(t, h, 2, call)
	act(t, h, 3, fold)
	act(t, h, 0, call)
	act(t, h, 1, check)
	for i := 0; i < 3; i++ {
		act(t, h, 0, check)
		act(t, h, 1, check)
		act(t, h, 2, check)
	}
	// pot = 4 antes + 3*2 = 10 → 4,3,3
	if got := h.Result.Pots[0].Shares; !reflect.DeepEqual(got, []int{4, 3, 3}) {
		t.Fatalf("shares %v", got)
	}
}

func TestShowdownOrderAndMuck(t *testing.T) {
	// River: 0 checks, 1 bets, 0 calls. 1 (aggressor) shows first with the
	// worse hand; 0 shows because it wins. Three-handed, 2 folded.
	deck := stackedDeck(t, []string{"AsAd", "KhQh", "2c7d"}, "Kd8c4s3h9c")
	h := mustHand(t, HandConfig{SmallBlind: 5, BigBlind: 10, Stacks: []int{1000, 1000, 1000}, Deck: deck})
	act(t, h, 2, fold)
	act(t, h, 0, call)
	act(t, h, 1, check)
	act(t, h, 0, check)
	act(t, h, 1, check)
	act(t, h, 0, check)
	act(t, h, 1, check)
	act(t, h, 0, check)
	act(t, h, 1, Action{Type: ActBet, To: 50})
	act(t, h, 0, call)
	res := h.Result
	if res.FirstToShow != 1 || !h.Shown[1] || !h.Shown[0] || res.Won[0] != 120 {
		t.Fatalf("first %d shown %v won %v", res.FirstToShow, h.Shown, res.Won)
	}

	// Without a river bet the first live player left of the button shows
	// first; a beaten hand after it is mucked.
	deck = stackedDeck(t, []string{"AsAd", "KhQh", "2c7d"}, "Jd8c4s3h9c")
	h = mustHand(t, HandConfig{SmallBlind: 5, BigBlind: 10, Stacks: []int{1000, 1000, 1000}, Deck: deck})
	act(t, h, 2, fold)
	act(t, h, 0, call)
	act(t, h, 1, check)
	for i := 0; i < 3; i++ {
		act(t, h, 0, check)
		act(t, h, 1, check)
	}
	if h.Result.FirstToShow != 0 || !h.Shown[0] || h.Shown[1] || !h.Result.Mucked[1] {
		t.Fatalf("first %d shown %v mucked %v", h.Result.FirstToShow, h.Shown, h.Result.Mucked)
	}
}

func TestPausedRunout(t *testing.T) {
	deck := stackedDeck(t, []string{"AsAd", "KsKd"}, "2c3c4h9dJh")
	h := mustHand(t, HandConfig{SmallBlind: 10, BigBlind: 20, Stacks: []int{500, 500}, Deck: deck, PauseRunout: true})
	act(t, h, 1, Action{Type: ActAllIn})
	act(t, h, 0, call)
	for _, want := range []int{0, 3, 4} {
		if !h.NeedsDeal() || len(h.Board) != want {
			t.Fatalf("board %d needsDeal %v", len(h.Board), h.NeedsDeal())
		}
		if err := h.DealNext(); err != nil {
			t.Fatal(err)
		}
	}
	if !h.Done || h.Result.Won[0] != 1000 {
		t.Fatalf("done %v result %+v", h.Done, h.Result)
	}
}

func TestCannotFoldWhenCheckIsFree(t *testing.T) {
	h := mustHand(t, HandConfig{SmallBlind: 10, BigBlind: 20, Stacks: []int{1000, 1000}, Deck: NewDeck()})
	act(t, h, 1, call)
	if err := h.Act(fold); err != ErrCannotFold {
		t.Fatalf("fold with check available: %v", err)
	}
	if err := h.Act(Action{Type: ActCheck}); err != nil {
		t.Fatal(err)
	}
}

func TestShortBlindsAndAntes(t *testing.T) {
	// Player 1 (big blind) has 15 chips: ante 5, then only 10 of the 20 big
	// blind. The amount to call is the largest bet actually posted.
	h := mustHand(t, HandConfig{SmallBlind: 10, BigBlind: 20, Ante: 5, Stacks: []int{1000, 15, 1000}, Deck: NewDeck()})
	if h.Bets[1] != 10 || h.Stacks[1] != 0 || h.Contrib[1] != 15 {
		t.Fatalf("short big blind: bets %v stacks %v contrib %v", h.Bets, h.Stacks, h.Contrib)
	}
	l := h.Legal()
	if l.Player != 2 || l.CallAmount != 10 || l.MinTo != 30 {
		t.Fatalf("legal %+v", l)
	}
}

func TestDescribe(t *testing.T) {
	cases := map[string]string{
		"AsKsQsJsTs2c3d": "Royal flush",
		"5s4s3s2sAs9c9d": "Straight flush, Five high",
		"9c9d9h9s2c3d4h": "Four of a kind, Nines",
		"KcKdKh7s7c2d3h": "Full house, Kings full of Sevens",
		"Ac9c5c3c2cKdKh": "Flush, Ace high",
		"5d4c3h2sAc9d9h": "Straight, Five high",
		"QcQdQh7s5c2d3h": "Three of a kind, Queens",
		"KcKd7h7s2c3d4h": "Two pair, Kings and Sevens",
		"AcAd9h7s5c3d2h": "Pair of Aces",
		"AcJd9h7s5c3d2h": "Ace high",
	}
	for cards, want := range cases {
		_, five, got := DescribeCards(MustCards(cards))
		if got != want {
			t.Errorf("%s: %q, want %q (%s)", cards, got, want, CardsString(five))
		}
	}
	if got := DescribeHole(MustCard("Kd"), MustCard("As")); got != "Ace-King offsuit" {
		t.Errorf("%q", got)
	}
	if !strings.HasPrefix(DescribeHole(MustCard("7h"), MustCard("7s")), "Pocket Sevens") {
		t.Error("pocket pair")
	}
}

func TestEval7MatchesEvalBest(t *testing.T) {
	deck := NewDeck()
	for k := 0; k+7 <= len(deck); k++ {
		var c [7]Card
		copy(c[:], deck[k:k+7])
		s, _ := EvalBest(c[:])
		if Eval7(c) != s {
			t.Fatalf("mismatch at %d", k)
		}
	}
}
