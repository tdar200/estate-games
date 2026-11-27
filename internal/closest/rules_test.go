package closest

import (
	"slices"
	"testing"
)

func TestOddsLadder(t *testing.T) {
	want := map[int][]int{
		1: {2},
		2: {2, 2},
		3: {3, 2, 3},
		4: {3, 2, 2, 3},
		5: {4, 3, 2, 3, 4},
		7: {5, 4, 3, 2, 3, 4, 5},
		8: {5, 4, 3, 2, 2, 3, 4, 5},
	}
	for k, odds := range want {
		var got []int
		for i := range k {
			got = append(got, Odds(i, k))
		}
		if !slices.Equal(got, odds) {
			t.Errorf("%d slots: odds %v, want %v", k, got, odds)
		}
	}
}

func TestJudge(t *testing.T) {
	slots := lineUp([]Guess{{PlayerID: "a", Value: 120}, {PlayerID: "b", Value: 52}, {PlayerID: "c", Value: 90}, {PlayerID: "d", Value: 76}})
	cases := []struct {
		answer          int
		winners, second []int
	}{
		{88, []int{2}, []int{1}},    // 90 off by 2; 76 off by 12
		{83, []int{1, 2}, []int{}},  // 76 and 90 both off by 7: two winners, no second
		{10, []int{0}, []int{1}},    // below everyone
		{999, []int{3}, []int{2}},   // above everyone
		{105, []int{2, 3}, []int{}}, // 90 and 120 both off by 15
		{52, []int{0}, []int{1}},    // exact
	}
	for _, c := range cases {
		w, s := judge(slots, c.answer)
		if !slices.Equal(w, c.winners) || !slices.Equal(s, c.second) {
			t.Errorf("answer %d: winners %v second %v, want %v %v", c.answer, w, s, c.winners, c.second)
		}
	}
	// Two slots equally second: both are second.
	slots = lineUp([]Guess{{PlayerID: "a", Value: 100}, {PlayerID: "b", Value: 90}, {PlayerID: "c", Value: 110}})
	if w, s := judge(slots, 100); !slices.Equal(w, []int{1}) || !slices.Equal(s, []int{0, 2}) {
		t.Errorf("winners %v second %v, want [1] [0 2]", w, s)
	}
}

func TestLineUp(t *testing.T) {
	slots := lineUp([]Guess{{PlayerID: "a", Value: 60}, {PlayerID: "b", Value: 40}, {PlayerID: "c", Value: 60}})
	if len(slots) != 2 || slots[0].Value != 40 || slots[1].Value != 60 || !slices.Equal(slots[1].OwnerIDs, []string{"a", "c"}) {
		t.Fatalf("slots %+v", slots)
	}
}

// TestWorkedExample is §4.1's example: guesses 52, 76, 90, 120 (odds 3 2 2
// 3), answer 88. 90 wins, 76 is second; a player with both chips on 90 and
// a stake of 3 on it scores 2 + 2 + 6 = 10.
func TestWorkedExample(t *testing.T) {
	g, c, ids := fourPeople(t, 1, madeUp("ex/88", 88))
	ann, ben, cat, dan := ids[0], ids[1], ids[2], ids[3]
	guess(t, g, ann, 52)
	guess(t, g, ben, 76)
	guess(t, g, cat, 90)
	guess(t, g, dan, 120)
	if g.Phase != PhaseLineup {
		t.Fatalf("phase %s after every guess, want lineup", g.Phase)
	}
	var odds []int
	for _, s := range g.Cur.Slots {
		odds = append(odds, s.Odds)
	}
	if !slices.Equal(odds, []int{3, 2, 2, 3}) {
		t.Fatalf("odds %v", odds)
	}
	runToPhase(t, g, c, PhaseBet)
	g.Player(ann).Score = 3
	if v := viewOf(t, g, ann); v.You.StakeMax != 3 || !v.You.CanBet {
		t.Fatalf("you %+v", v.You)
	}
	bet(t, g, ann, 2, 2, 3, 2)
	bet(t, g, ben, 1, 1, 0, 0)
	bet(t, g, cat, 2, 3, 0, 0)
	bet(t, g, dan, 1, 2, 0, 0)
	if g.Phase != PhaseChips {
		t.Fatalf("phase %s after every bet, want chips", g.Phase)
	}
	runToPhase(t, g, c, PhaseReveal)
	if !slices.Equal(g.Cur.Winners, []int{2}) || !slices.Equal(g.Cur.Second, []int{1}) {
		t.Fatalf("winners %v second %v", g.Cur.Winners, g.Cur.Second)
	}
	want := map[string][4]int{ // bonus, chips, stake, delta
		ann: {0, 4, 6, 10},
		ben: {1, 2, 0, 3},
		cat: {3, 2, 0, 5},
		dan: {0, 3, 0, 3},
	}
	for id, w := range want {
		r := resultOf(t, g, id)
		if got := [4]int{r.Bonus, r.ChipPoints, r.StakePoints, r.Delta}; got != w {
			t.Errorf("%s: bonus, chips, stake, delta = %v, want %v", g.Player(id).Name, got, w)
		}
	}
	if g.Player(ann).Score != 13 || g.Player(cat).Closest != 1 || g.Player(ben).Closest != 0 {
		t.Fatalf("ann %d, cat closest %d", g.Player(ann).Score, g.Player(cat).Closest)
	}
	v := viewOf(t, g, "")
	q := v.Question
	if q.Answer == nil || *q.Answer != 88 || q.Fact == nil || len(q.Src) != 2 || len(q.Results) != 4 {
		t.Fatalf("reveal view %+v", q)
	}
	if s := q.Slots[2]; *s.Result != resultWin || *s.Off != 2 || len(s.Chips) != 3 {
		t.Fatalf("slot 2 %+v", s)
	}
	if s := q.Slots[1]; *s.Result != resultSecond || *s.Off != 12 {
		t.Fatalf("slot 1 %+v", s)
	}
	if s := q.Slots[0]; *s.Result != "" {
		t.Fatalf("slot 0 %+v", s)
	}
	if chips := q.Slots[2].Chips; chips[0] != (ChipView{PlayerID: ann, Count: 2, Stake: 3}) {
		t.Fatalf("Ann's chips %+v", chips[0])
	}
}

// TestLastQuestionDouble: the last question pays everything ×2 but the stake.
func TestLastQuestionDouble(t *testing.T) {
	g, c := newGame(t, 2)
	ann, ben := addHuman(t, g, "Ann"), addHuman(t, g, "Ben")
	addHuman(t, g, "Cat")
	mustAct(t, g, ann, `{"type":"settings","settings":{"fill":false}}`)
	startGame(t, g)
	setQuestion(g, 7, madeUp("ex/last", 88))
	runUntil(t, g, c, func() bool { return g.QuestionNo == 7 && g.Phase == PhaseGuess })
	if !g.Cur.Double || !viewOf(t, g, "").Question.Double {
		t.Fatal("the last question is not double")
	}
	guess(t, g, ann, 88)
	guess(t, g, ben, 50)
	runToPhase(t, g, c, PhaseBet)
	g.Player(ann).Score = 3
	bet(t, g, ann, 1, 1, 2, 1)
	bet(t, g, ben, 0, 1, 0, 0)
	runToPhase(t, g, c, PhaseReveal)
	if r := resultOf(t, g, ann); r.Bonus != 10 || r.ChipPoints != 8 || r.StakePoints != 4 || r.Delta != 22 {
		t.Fatalf("Ann %+v", r)
	}
	if r := resultOf(t, g, ben); r.Bonus != 2 || r.ChipPoints != 6 || r.Delta != 8 {
		t.Fatalf("Ben %+v", r)
	}
	runToPhase(t, g, c, PhaseFinished)
	if g.Status != StatusFinished || !slices.Equal(g.WinnerIDs, []string{ann}) {
		t.Fatalf("%s winners %v", g, g.WinnerIDs)
	}
	if v := viewOf(t, g, ann); v.Question == nil || v.Question.Answer == nil || len(v.Standings) != 3 || v.Standings[0].ID != ann {
		t.Fatalf("finished view %+v", v)
	}
}

// TestStakeLoses: a stake on any slot but a winner is lost, the second
// closest included, while the chips there still pay.
func TestStakeLoses(t *testing.T) {
	g, c, ids := fourPeople(t, 3, madeUp("ex/100", 100))
	guess(t, g, ids[0], 90)
	guess(t, g, ids[1], 120)
	guess(t, g, ids[2], 300)
	guess(t, g, ids[3], 101)
	runToPhase(t, g, c, PhaseBet)
	g.Player(ids[0]).Score = 1
	expectErr(t, act(g, ids[0], `{"type":"bet","q":%d,"chips":[0,0],"stake":2,"stakeSlot":0}`, g.Cur.Seq), "stake between 0 and 1")
	bet(t, g, ids[0], 0, 0, 1, 0) // 90 is second (101 wins)
	runToPhase(t, g, c, PhaseReveal)
	if r := resultOf(t, g, ids[0]); r.Bonus != 1 || r.ChipPoints != 2 || r.StakePoints != -1 || r.Delta != 2 || r.After != 3 {
		t.Fatalf("Ann %+v", r)
	}
	if r := resultOf(t, g, ids[3]); r.Bonus != 3 {
		t.Fatalf("Dan %+v", r)
	}
}

func TestTieBothWin(t *testing.T) {
	g, c, ids := fourPeople(t, 4, madeUp("ex/tie", 100))
	guess(t, g, ids[0], 90)
	guess(t, g, ids[1], 110)
	guess(t, g, ids[2], 150)
	runToPhase(t, g, c, PhaseBet) // Dan never guesses: the timer closes the guesses
	bet(t, g, ids[3], 0, 1, 0, 0)
	bet(t, g, ids[2], 2, 2, 0, 0)
	runToPhase(t, g, c, PhaseReveal)
	if !slices.Equal(g.Cur.Winners, []int{0, 1}) || len(g.Cur.Second) != 0 {
		t.Fatalf("winners %v second %v", g.Cur.Winners, g.Cur.Second)
	}
	// Ann and Ben never bet: the timer put both their chips on their own
	// slots (3× and 2×).
	for i, want := range []int{3 + 3*2, 3 + 2*2, 0, 3 + 2} {
		if r := resultOf(t, g, ids[i]); r.Delta != want {
			t.Errorf("%s: delta %d, want %d (%+v)", g.Player(ids[i]).Name, r.Delta, want, r)
		}
	}
	if g.Player(ids[0]).Closest != 1 || g.Player(ids[1]).Closest != 1 {
		t.Fatal("both winners count a closest guess")
	}
}

func TestActionErrors(t *testing.T) {
	g, c := newGame(t, 5)
	ann, ben := addHuman(t, g, "Ann"), addHuman(t, g, "Ben")
	expectErr(t, act(g, ben, `{"type":"start"}`), "only the host can start the game")
	expectErr(t, act(g, ann, `{"type":"settings","settings":{"guessSeconds":10}}`), "guessSeconds must be 20, 30 or 45")
	expectErr(t, act(g, ben, `{"type":"settings","settings":{"fill":false}}`), "only the host can change the rules")
	expectErr(t, act(g, ann, `{"type":"guess","q":1,"value":5}`), "the game is not in progress")
	expectErr(t, act(g, ann, `{"type":"dance"}`), `unknown action "dance"`)
	expectErr(t, act(g, ann, `nonsense`), "invalid action format")
	mustAct(t, g, TableHost, `{"type":"settings","settings":{"fill":false,"length":"full","unknown":1}}`)
	if g.Settings.Length != LengthFull || g.Settings.Fill || g.Settings.GuessSeconds != 30 {
		t.Fatalf("settings %+v", g.Settings)
	}
	startGame(t, g)
	expectErr(t, act(g, ann, `{"type":"settings","settings":{"fill":true}}`), "the rules can only change in the lobby")
	expectErr(t, act(g, TableHost, `{"type":"guess","value":5}`), "the table screen can only start the game or change the rules")
	setQuestion(g, 1, madeUp("ex/err", 880))
	setQuestion(g, 2, &Question{ID: "ex/year", Topic: "history", Text: "In what made-up year?", Answer: 1900, Format: FormatYear,
		Min: 1000, Max: 2025, Spread: 40, Fact: "Made up.", Src: []string{"https://example.org/y", "https://example.org/z"}})
	runToPhase(t, g, c, PhaseGuess)
	seq := g.Cur.Seq
	expectErr(t, act(g, ann, `{"type":"guess","q":%d,"value":80}`, seq), "guess between 88 and 8,800")
	expectErr(t, act(g, ann, `{"type":"guess","q":%d,"value":120.5}`, seq), "guess between 88 and 8,800")
	expectErr(t, act(g, ann, `{"type":"guess","q":%d}`, seq), "guess between 88 and 8,800")
	expectErr(t, act(g, ann, `{"type":"guess","q":%d,"value":100}`, seq+1), "that question is over")
	expectErr(t, act(g, ann, `{"type":"bet","q":%d,"chips":[0,0]}`, seq), "bets are closed")
	mustAct(t, g, ann, `{"type":"guess","value":100}`) // q may be left out
	mustAct(t, g, ann, `{"type":"guess","q":%d,"value":200}`, seq)
	expectErr(t, act(g, ann, `{"type":"guess","q":%d,"value":300}`, seq), "you have already changed your guess")
	if v := viewOf(t, g, ann); *v.You.Guess != 200 || v.You.ChangesLeft != 0 || v.You.CanGuess {
		t.Fatalf("you %+v", v.You)
	}
	guess(t, g, ben, 150)
	runToPhase(t, g, c, PhaseBet)
	expectErr(t, act(g, ann, `{"type":"guess","q":%d,"value":300}`, seq), "guesses are closed")
	expectErr(t, act(g, ann, `{"type":"bet","q":%d,"chips":[0]}`, seq), "pick two slots")
	expectErr(t, act(g, ann, `{"type":"bet","q":%d,"chips":[0,2]}`, seq), "pick two slots")
	expectErr(t, act(g, ann, `{"type":"bet","q":%d,"chips":[0,1],"stake":1,"stakeSlot":1}`, seq), "stake between 0 and 0")
	g.Player(ann).Score = 2
	expectErr(t, act(g, ann, `{"type":"bet","q":%d,"chips":[0,0],"stake":1,"stakeSlot":1}`, seq), "put the stake on one of your chips")
	expectErr(t, act(g, ann, `{"type":"bet","q":%d,"chips":[0,0],"stake":1}`, seq), "put the stake on one of your chips")
	mustAct(t, g, ann, `{"type":"bet","q":%d,"chips":[0,1],"stake":0,"stakeSlot":7}`, seq) // ignored when 0
	expectErr(t, act(g, ann, `{"type":"bet","q":%d,"chips":[0,1]}`, seq), "you have already bet")
	if b := g.Cur.betOf(ann); b.StakeSlot != nil {
		t.Fatalf("stake slot %v with no stake", *b.StakeSlot)
	}
	runUntil(t, g, c, func() bool { return g.QuestionNo == 2 && g.Phase == PhaseGuess })
	expectErr(t, act(g, ann, `{"type":"guess","q":%d,"value":999}`, g.Cur.Seq), "guess between 1000 and 2025")
}

func TestHurryUp(t *testing.T) {
	g, c, ids := fourPeople(t, 6)
	ends := g.PhaseEndsAt
	c.now += 1000
	guess(t, g, ids[0], g.Cur.Question.Min)
	guess(t, g, ids[1], g.Cur.Question.Min)
	if g.PhaseEndsAt != ends {
		t.Fatal("the timer moved with two still to guess")
	}
	c.now += 1000
	guess(t, g, ids[2], g.Cur.Question.Min)
	if g.PhaseEndsAt != c.now+hurryMs {
		t.Fatalf("timer ends in %d ms, want %d", g.PhaseEndsAt-c.now, hurryMs)
	}
	runToPhase(t, g, c, PhaseLineup)
	if len(g.Cur.Guesses) != 3 {
		t.Fatal("Dan guessed?")
	}

	// Within 10 s of the end nothing moves; with two playing, one guess is
	// enough; alone, never.
	g, c = newGame(t, 7)
	ann, ben := addHuman(t, g, "Ann"), addHuman(t, g, "Ben")
	mustAct(t, g, ann, `{"type":"settings","settings":{"fill":false}}`)
	startGame(t, g)
	runToPhase(t, g, c, PhaseGuess)
	c.now = g.PhaseEndsAt - 5000
	ends = g.PhaseEndsAt
	guess(t, g, ann, g.Cur.Question.Min)
	if g.PhaseEndsAt != ends {
		t.Fatal("hurried with 5 s left")
	}
	runUntil(t, g, c, func() bool { return g.QuestionNo == 2 && g.Phase == PhaseGuess })
	guess(t, g, ben, g.Cur.Question.Min)
	if g.PhaseEndsAt != c.now+hurryMs {
		t.Fatal("no hurry-up with one of two in")
	}

	g, c = newGame(t, 8)
	solo := addHuman(t, g, "Solo")
	mustAct(t, g, solo, `{"type":"settings","settings":{"fill":false}}`)
	startGame(t, g)
	runToPhase(t, g, c, PhaseGuess)
	if g.PhaseEndsAt != c.now+30000 {
		t.Fatal("a lone player was hurried")
	}
}

// TestComputerHurried: a computer still to guess when the hurry-up comes
// moves its guess inside the new time.
func TestComputerHurried(t *testing.T) {
	g, c := newGame(t, 9)
	ann := addHuman(t, g, "Ann")
	bot := addComputer(t, g, "Bot", LevelNormal)
	mustAct(t, g, ann, `{"type":"settings","settings":{"fill":false,"guessSeconds":45}}`)
	startGame(t, g)
	runToPhase(t, g, c, PhaseGuess)
	g.Player(bot).Plan.At = c.now + 40000
	guess(t, g, ann, g.Cur.Question.Min)
	if at := g.Player(bot).Plan.At; at != g.PhaseEndsAt-computerMarginMs {
		t.Fatalf("plan at +%d, deadline +%d", at-c.now, g.PhaseEndsAt-c.now)
	}
	tick(g, c)
	if g.Cur.guessOf(bot) == nil || g.Phase != PhaseLineup {
		t.Fatalf("%s, bot guess %v", g, g.Cur.guessOf(bot))
	}
}

func TestNobodyGuessed(t *testing.T) {
	g, c, ids := fourPeople(t, 10)
	runToPhase(t, g, c, PhaseReveal)
	if !g.Cur.Nobody || len(g.Cur.Results) != 4 || g.Cur.Slots != nil {
		t.Fatalf("current %+v", g.Cur)
	}
	v := viewOf(t, g, ids[0])
	if v.Question.Nobody == nil || !*v.Question.Nobody || v.Question.Answer == nil || v.Question.Slots != nil {
		t.Fatalf("question %+v", v.Question)
	}
	for _, p := range g.Players {
		if p.Score != 0 {
			t.Fatal("points with nobody guessing")
		}
	}
}

// TestOneSlot: with one slot (everyone guessed the same) there is nothing to
// bet on: line-up, then the answer.
func TestOneSlot(t *testing.T) {
	g, c, ids := fourPeople(t, 11, madeUp("ex/one", 500))
	guess(t, g, ids[0], 450)
	guess(t, g, ids[1], 450)
	runToPhase(t, g, c, PhaseLineup)
	if len(g.Cur.Slots) != 1 {
		t.Fatal("not one slot")
	}
	tick(g, c)
	if g.Phase != PhaseReveal || !slices.Equal(g.Cur.Winners, []int{0}) {
		t.Fatalf("%s winners %v", g, g.Cur.Winners)
	}
	if g.Player(ids[0]).Score != 3 || g.Player(ids[1]).Score != 3 || g.Player(ids[2]).Score != 0 {
		t.Fatal("the one slot's owners score 3")
	}
}

// TestBetTimeout: no bet by the timer: a player who guessed and is online
// gets both chips on their own slot, no stake; away or without a guess, no
// chips.
func TestBetTimeout(t *testing.T) {
	g, c, ids := fourPeople(t, 12, madeUp("ex/auto", 100))
	guess(t, g, ids[0], 90)
	guess(t, g, ids[1], 150)
	guess(t, g, ids[2], 300)
	runToPhase(t, g, c, PhaseBet)
	g.Player(ids[0]).Score = 3
	g.SetOnline(ids[1], false)
	runToPhase(t, g, c, PhaseChips)
	if len(g.Cur.Bets) != 2 {
		t.Fatalf("bets %+v", g.Cur.Bets)
	}
	if b := g.Cur.Bets[0]; b.PlayerID != ids[0] || b.Chips != [2]int{0, 0} || b.Stake != 0 || !b.Auto {
		t.Fatalf("auto bet %+v", b)
	}
	if b := g.Cur.Bets[1]; b.PlayerID != ids[2] || b.Chips != [2]int{2, 2} {
		t.Fatalf("auto bet %+v", b)
	}
	runToPhase(t, g, c, PhaseReveal)
	if r := resultOf(t, g, ids[0]); r.Delta != 3+2*3 {
		t.Fatalf("Ann %+v", r)
	}
}

// TestLeaving: a player who leaves keeps their slot on the line for the
// question but scores nothing more (no bonus, chips or stake), ranks below
// everyone who stayed, and is out from the next question.
func TestLeaving(t *testing.T) {
	g, c, ids := fourPeople(t, 13, madeUp("ex/leave", 100))
	ann, ben, cat, dan := ids[0], ids[1], ids[2], ids[3]
	g.Player(ben).Score = 9
	guess(t, g, ben, 100)
	guess(t, g, ann, 60)
	guess(t, g, cat, 200)
	runToPhase(t, g, c, PhaseBet)
	bet(t, g, ben, 1, 1, 3, 1)
	if err := g.RemovePlayer(ben, ben); err != nil {
		t.Fatal(err)
	}
	if !g.Player(ben).Left {
		t.Fatal("Ben did not leave")
	}
	bet(t, g, ann, 1, 1, 0, 0)
	bet(t, g, cat, 1, 2, 0, 0)
	bet(t, g, dan, 0, 1, 0, 0) // the last bet still playing shows the chips
	if g.Phase != PhaseChips {
		t.Fatalf("phase %s", g.Phase)
	}
	runToPhase(t, g, c, PhaseReveal)
	if g.Cur.Slots[1].Value != 100 {
		t.Fatal("Ben's slot left the line")
	}
	if r := resultOf(t, g, ben); r.Delta != 0 || r.Bonus != 0 || r.StakePoints != 0 {
		t.Fatalf("Ben %+v", r)
	}
	if g.Player(ben).Closest != 0 || g.Player(ben).Score != 9 {
		t.Fatal("a player who left scored")
	}
	if r := resultOf(t, g, ann); r.ChipPoints != 4 || r.Bonus != 1 {
		t.Fatalf("Ann %+v", r)
	}
	runToPhase(t, g, c, PhaseGuess)
	if slices.Contains(g.Cur.Playing, ben) || len(g.Cur.Playing) != 3 {
		t.Fatalf("playing %v", g.Cur.Playing)
	}
	expectErr(t, act(g, ben, `{"type":"guess","value":50}`), "you are not playing in this game")
	v := viewOf(t, g, ben)
	if v.You == nil || v.You.CanGuess || v.Players[1].Status != "left" {
		t.Fatalf("Ben's view %+v %+v", v.You, v.Players[1])
	}
	// Everyone else guessed: Dan leaving closes the guesses.
	guess(t, g, ann, g.Cur.Question.Min)
	guess(t, g, cat, g.Cur.Question.Min)
	if err := g.RemovePlayer(ann, dan); err != nil {
		t.Fatal(err)
	}
	if g.Phase != PhaseLineup {
		t.Fatalf("phase %s", g.Phase)
	}
	if _, err := g.EndGame(); err != nil {
		t.Fatal(err)
	}
	if p := g.Player(ben); p.Place != 3 {
		t.Fatalf("Ben (left, 9 points) placed %d, want 3", p.Place)
	}
	// The host leaving passes the host on.
	g2, _, ids2 := fourPeople(t, 20)
	if err := g2.RemovePlayer(ids2[0], ids2[0]); err != nil || g2.HostID != ids2[1] {
		t.Fatalf("err %v host %s", err, g2.HostID)
	}
	expectErr(t, g2.RemovePlayer(ids2[2], ids2[3]), "only the host can remove other players")
}

// TestLastPersonLeaves: computers do not play on alone.
func TestLastPersonLeaves(t *testing.T) {
	g, c := newGame(t, 14)
	ann := addHuman(t, g, "Ann")
	startGame(t, g) // fill: three computers join
	if len(g.Players) != FillTo || g.Players[1].Name != "Computer 1" || g.Players[3].Level != LevelNormal {
		t.Fatalf("players %+v", g.Players)
	}
	runToPhase(t, g, c, PhaseGuess)
	bot := g.Players[1].ID
	if err := g.RemovePlayer(TableHost, bot); err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusPlaying {
		t.Fatal("a computer leaving ended the game")
	}
	if err := g.RemovePlayer(ann, ann); err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusFinished || g.NextDeadline() != 0 {
		t.Fatalf("%s", g)
	}
	if v := viewOf(t, g, ""); v.Question != nil || v.Standings == nil {
		t.Fatalf("an unrevealed question shows after the end: %+v", v.Question)
	}
}

func TestPlaces(t *testing.T) {
	g, _ := newGame(t, 15)
	ids := []string{addHuman(t, g, "A"), addHuman(t, g, "B"), addHuman(t, g, "C"), addHuman(t, g, "D"), addHuman(t, g, "E")}
	mustAct(t, g, ids[0], `{"type":"settings","settings":{"fill":false}}`)
	startGame(t, g)
	set := func(id string, score, closest int, left bool) {
		p := g.Player(id)
		p.Score, p.Closest, p.Left = score, closest, left
	}
	set(ids[0], 10, 1, false)
	set(ids[1], 10, 2, false)
	set(ids[2], 10, 2, false)
	set(ids[3], 4, 0, false)
	set(ids[4], 30, 5, true) // left: below everyone who stayed
	g.place()
	want := []int{3, 1, 1, 4, 5}
	for i, id := range ids {
		if p := g.Player(id); p.Place != want[i] {
			t.Errorf("%s placed %d, want %d", p.Name, p.Place, want[i])
		}
	}
	if !slices.Equal(g.WinnerIDs, []string{ids[1], ids[2]}) {
		t.Fatalf("winners %v", g.WinnerIDs)
	}
}

// TestChangedGuessOrder: a changed guess goes to the back of the guess order
// (slot owners are listed in guess order).
func TestChangedGuessOrder(t *testing.T) {
	g, c, ids := fourPeople(t, 16, madeUp("ex/order", 100))
	guess(t, g, ids[0], 50)
	guess(t, g, ids[1], 60)
	c.now += 3000
	guess(t, g, ids[0], 60)
	runToPhase(t, g, c, PhaseLineup)
	if !slices.Equal(g.Cur.Slots[0].OwnerIDs, []string{ids[1], ids[0]}) {
		t.Fatalf("owners %v", g.Cur.Slots[0].OwnerIDs)
	}
	if gs := g.Cur.guessOf(ids[0]); !gs.Changed || gs.AfterMs != 3000 {
		t.Fatalf("guess %+v", gs)
	}
}

func TestLobby(t *testing.T) {
	g, _ := newGame(t, 17)
	if _, err := g.AddPlayer(" ", KindHuman); err == nil {
		t.Fatal("a blank name was seated")
	}
	ann := addHuman(t, g, "Ann")
	bot := addComputer(t, g, "Bot", LevelEasy)
	if err := g.SetLevel(bot, LevelHard); err != nil || g.Player(bot).Level != LevelHard {
		t.Fatal(err)
	}
	if err := g.SetLevel(bot, "genius"); err == nil {
		t.Fatal("unknown level accepted")
	}
	for range MaxPlayers - 2 {
		addHuman(t, g, "X")
	}
	if _, err := g.AddPlayer("Late", KindHuman); err == nil || err.Error() != "the table is full" {
		t.Fatalf("err %v", err)
	}
	if err := g.RemovePlayer(ann, ann); err != nil || len(g.Players) != MaxPlayers-1 || g.HostID == ann {
		t.Fatalf("err %v, host %s", err, g.HostID)
	}
	v := viewOf(t, g, "")
	if v.Game != "closest" || v.Title != Title || v.QuestionCount != 7 || v.Question != nil || v.SeatLimit != 8 || v.Players[0].Status != "waiting" {
		t.Fatalf("lobby view %+v", v)
	}
	empty, _ := newGame(t, 18)
	expectErr(t, act(empty, TableHost, `{"type":"start"}`), "add a player first")
	noPacks := NewGame("X", Options{Library: &Library{}})
	addHuman(t, noPacks, "Ann")
	expectErr(t, act(noPacks, noPacks.HostID, `{"type":"start"}`), "there are no questions to ask yet")
}

// TestEndGame: ended mid-question, the unrevealed question is not shown.
func TestEndGame(t *testing.T) {
	g, _, ids := fourPeople(t, 19)
	guess(t, g, ids[0], g.Cur.Question.Min)
	g.Player(ids[1]).Score = 4
	winner, err := g.EndGame()
	if err != nil || winner != ids[1] || g.Status != StatusFinished {
		t.Fatalf("winner %s err %v", winner, err)
	}
	if v := viewOf(t, g, ids[0]); v.Question != nil || v.You.Guess != nil || v.Players[1].Place != 1 {
		t.Fatalf("view %+v", v)
	}
	if _, err := g.EndGame(); err == nil {
		t.Fatal("ended twice")
	}
}
