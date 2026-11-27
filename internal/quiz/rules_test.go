package quiz

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLobby(t *testing.T) {
	g, _ := newGame(t, 1)
	expectErr(t, act(g, TableHost, `{"type":"start"}`), "add a player first")
	_, err := g.AddPlayer("   ", KindHuman)
	expectErr(t, err, "a name is required")
	_, err = g.AddPlayer("Rex", "robot")
	expectErr(t, err, `kind must be "human" or "ai"`)

	bot := addComputer(t, g, "Robo", LevelHard)
	if g.HostID != "" {
		t.Fatal("a computer became the host")
	}
	ann := addHuman(t, g, "Ann")
	ben := addHuman(t, g, "Ben")
	if g.HostID != ann {
		t.Fatalf("host = %q, want the first person %q", g.HostID, ann)
	}
	if p := g.Player(ann); p.Latency != UnknownLatency || !p.Online {
		t.Fatalf("a new person has latency %d, online %v; want 250 and online", p.Latency, p.Online)
	}

	expectErr(t, act(g, ben, `{"type":"start"}`), "only the host can start the game")
	expectErr(t, act(g, ben, `{"type":"settings","settings":{"length":"full"}}`), "only the host can change the rules")
	expectErr(t, act(g, ann, `{"type":"settings","settings":{"length":"long"}}`), `length must be "quick" or "full"`)
	expectErr(t, act(g, ann, `{"type":"settings","settings":{"readSpeed":"turbo"}}`), `readSpeed must be "slow", "normal" or "fast"`)
	expectErr(t, act(g, ann, `{"type":"settings","settings":{"answerSeconds":7}}`), "answerSeconds must be 6, 8 or 12")
	expectErr(t, act(g, ann, `{"type":"settings","settings":"fast"}`), "invalid settings")
	// A partial update keeps the other settings; unknown keys are ignored.
	mustAct(t, g, TableHost, `{"type":"settings","settings":{"length":"full","surprise":1}}`)
	if want := (Settings{Length: LengthFull, ReadSpeed: SpeedNormal, AnswerSeconds: 8}); g.Settings != want {
		t.Fatalf("settings = %+v, want %+v", g.Settings, want)
	}

	expectErr(t, act(g, TableHost, `{"type":"pick","category":0,"row":0}`), "the table screen can only start the game or change the rules")
	expectErr(t, act(g, ann, `{"type":"pick","category":0,"row":0}`), "the game is not in progress")
	expectErr(t, act(g, ann, `{"type":"answer","clue":1,"choice":0}`), "the game is not in progress")
	expectErr(t, act(g, ann, `{"type":"dance"}`), `unknown action "dance"`)
	expectErr(t, act(g, ann, `nonsense`), "invalid action format")
	expectErr(t, act(g, "p99", `{"type":"start"}`), "you are not playing in this game")

	if err := g.SetLevel(bot, LevelEasy); err != nil || g.Player(bot).Level != LevelEasy {
		t.Fatalf("SetLevel: %v, level %q", err, g.Player(bot).Level)
	}
	expectErr(t, g.SetLevel(ann, LevelEasy), "no such computer player")
	_, err = g.AddComputer("Odd", "genius")
	expectErr(t, err, `unknown level "genius"`)

	for len(g.Players) < MaxPlayers {
		addComputer(t, g, "Filler", LevelNormal)
	}
	_, err = g.AddPlayer("Late", KindHuman)
	expectErr(t, err, "the table is full")

	expectErr(t, g.RemovePlayer(ben, ann), "only the host can remove other players")
	if err := g.RemovePlayer(ann, ann); err != nil {
		t.Fatal(err)
	}
	if len(g.Players) != MaxPlayers-1 || g.HostID != ben {
		t.Fatalf("after the host left the lobby: %d seats, host %q; want %d and %q", len(g.Players), g.HostID, MaxPlayers-1, ben)
	}

	startGame(t, g)
	_, err = g.AddPlayer("Late", KindHuman)
	expectErr(t, err, "the game has already started")
	expectErr(t, act(g, ben, `{"type":"settings","settings":{"length":"quick"}}`), "the rules can only change in the lobby")
	expectErr(t, act(g, ben, `{"type":"start"}`), "the game has already started")
	expectErr(t, g.SetLevel(bot, LevelHard), "the game has already started")
}

func TestReadingTime(t *testing.T) {
	g, _ := newGame(t, 1)
	cases := []struct {
		words int
		speed string
		want  int64
	}{
		{2, SpeedNormal, 3000},  // 2100 is below the floor
		{10, SpeedNormal, 4500}, // 1500 + 3000
		{30, SpeedNormal, 8000}, // capped
		{10, SpeedSlow, 6300},   // × 1.4
		{10, SpeedFast, 3375},   // × 0.75
		{2, SpeedFast, 2250},    // the floor is scaled too
	}
	for _, tc := range cases {
		g.Settings.ReadSpeed = tc.speed
		if got := g.readMs(strings.Repeat("word ", tc.words)); got != tc.want {
			t.Errorf("%d words at %s: %d ms, want %d", tc.words, tc.speed, got, tc.want)
		}
	}
}

// TestPhaseWalk follows a board clue through every phase with its timers,
// and checks scoring and control for a right answer, no answer and only
// wrong answers.
func TestPhaseWalk(t *testing.T) {
	g, c := newGame(t, 2)
	ann, ben := addHuman(t, g, "Ann"), addHuman(t, g, "Ben")
	startGame(t, g)
	want := func(phase string, endsAt int64) {
		t.Helper()
		if g.Phase != phase || g.PhaseEndsAt != endsAt {
			t.Fatalf("phase %s ending %d, want %s ending %d", g.Phase, g.PhaseEndsAt, phase, endsAt)
		}
	}
	if g.Round != 1 || g.Status != StatusPlaying || len(g.Boards) != 1 {
		t.Fatalf("round %d, status %s, %d boards", g.Round, g.Status, len(g.Boards))
	}
	want(PhaseIntro, start+introMs+6*introPerTitleMs)
	tick(g, c)
	want(PhasePick, c.now+pickMs)

	g.ChooserID = ann
	expectErr(t, act(g, ben, `{"type":"pick","category":0,"row":0}`), "it is not your pick")
	expectErr(t, act(g, ann, `{"type":"pick","category":6,"row":0}`), "pick a category from 0 to 5 and a row from 0 to 4")
	expectErr(t, act(g, ann, `{"type":"pick","row":0}`), "pick a category from 0 to 5 and a row from 0 to 4")
	expectErr(t, act(g, ann, `{"type":"bet","amount":5}`), "there is nothing to bet on now")
	cat, row := firstOpen(g, false)
	pickCell(t, g, cat, row)
	expectErr(t, act(g, ann, `{"type":"pick","category":%d,"row":%d}`, cat, row), "it is not time to pick a clue")

	q := g.Clue.Q
	words := len(strings.Fields(q.Text))
	readMs := int64(min(max(1500+300*words, 3000), 8000))
	if q.ReadAt != c.now || q.OpenAt != c.now+readMs || q.ClosesAt != q.OpenAt+8000 {
		t.Fatalf("read at %d, open %d, close %d; want %d, %d, %d", q.ReadAt, q.OpenAt, q.ClosesAt, c.now, c.now+readMs, c.now+readMs+8000)
	}
	want(PhaseRead, q.OpenAt)
	if len(g.Clue.Eligible) != 2 {
		t.Fatalf("eligible %v, want both players", g.Clue.Eligible)
	}
	tick(g, c)
	want(PhaseAnswer, q.ClosesAt)

	value, right, wrong := g.Clue.Value, q.Answer, wrongChoice(q)
	c.now = q.OpenAt + 1000
	if err := answerNow(g, ann, wrong, 900); err != nil {
		t.Fatal(err)
	}
	expectErr(t, answerNow(g, ann, right, 950), "you have already answered")
	expectErr(t, act(g, ben, `{"type":"answer","clue":%d,"choice":4,"ms":5}`, g.Clue.Seq), "choose one of the four answers")
	expectErr(t, act(g, ben, `{"type":"answer","choice":1}`), "say which clue and which answer")
	expectErr(t, act(g, ben, `{"type":"answer","clue":%d,"choice":1}`, g.Clue.Seq+1), "that clue is over")
	c.now = q.OpenAt + 1300
	if err := answerNow(g, ben, right, 1200); err != nil {
		t.Fatal(err)
	}
	// Everyone has tapped and one tap is right: judged 100 ms after the last
	// tap (nobody left to wait for).
	if g.Phase != PhaseAnswer || q.DecideAt != c.now+minWaitMs {
		t.Fatalf("phase %s, decide at %d; want answer, %d", g.Phase, q.DecideAt, c.now+minWaitMs)
	}
	tick(g, c)
	want(PhaseReveal, c.now+revealMs)
	if a, b := g.Player(ann), g.Player(ben); a.Score != -value || b.Score != value || a.Wrong != 1 || b.Correct != 1 {
		t.Fatalf("scores %d (%d wrong) and %d (%d right); want %d and %d", a.Score, a.Wrong, b.Score, b.Correct, -value, value)
	}
	if g.ChooserID != ben || g.Clue.WinnerID != ben {
		t.Fatalf("chooser %q, winner %q; want the winner %q", g.ChooserID, g.Clue.WinnerID, ben)
	}
	expectErr(t, answerNow(g, ben, right, 1), "that clue is over")
	tick(g, c)
	want(PhasePick, c.now+pickMs)

	// Nobody answers: the choices close, the engine waits the grace for taps
	// on their way (two phones at 250 ms: 300 ms), and the chooser keeps
	// control.
	cat, row = firstOpen(g, false)
	pickCell(t, g, cat, row)
	runToPhase(t, g, c, PhaseAnswer)
	q = g.Clue.Q
	tick(g, c)
	if g.Phase != PhaseAnswer || q.DecideAt != q.ClosesAt+300 {
		t.Fatalf("after the close: phase %s, decide at %d; want answer, %d", g.Phase, q.DecideAt, q.ClosesAt+300)
	}
	tick(g, c)
	want(PhaseReveal, c.now+revealLongMs)
	if g.ChooserID != ben || g.Clue.WinnerID != "" || g.Player(ann).Score != -value || g.Player(ben).Score != value {
		t.Fatalf("no answer changed something: chooser %q, winner %q", g.ChooserID, g.Clue.WinnerID)
	}
	tick(g, c)

	// Both wrong: judged as soon as the last one taps; both lose.
	cat, row = firstOpen(g, false)
	pickCell(t, g, cat, row)
	runToPhase(t, g, c, PhaseAnswer)
	q, v2 := g.Clue.Q, g.Clue.Value
	c.now = q.OpenAt + 2000
	if err := answerNow(g, ann, wrongChoice(q), 1900); err != nil {
		t.Fatal(err)
	}
	c.now = q.OpenAt + 2500
	if err := answerNow(g, ben, (q.Answer+2)%4, 2400); err != nil {
		t.Fatal(err)
	}
	want(PhaseReveal, c.now+revealLongMs)
	if a, b := g.Player(ann), g.Player(ben); a.Score != -value-v2 || b.Score != value-v2 {
		t.Fatalf("scores %d and %d; want %d and %d", a.Score, b.Score, -value-v2, value-v2)
	}
	if g.ChooserID != ben {
		t.Fatalf("chooser %q, want %q to keep control", g.ChooserID, ben)
	}
}

func TestAutoPick(t *testing.T) {
	g, c := newGame(t, 3)
	ann := addHuman(t, g, "Ann")
	startGame(t, g)
	runToPhase(t, g, c, PhasePick)
	// No pick yet: the cheapest cell, leftmost (row 0 is never gold).
	tick(g, c)
	if g.Clue.Category != 0 || g.Clue.Row != 0 {
		t.Fatalf("first auto-pick (%d, %d), want (0, 0)", g.Clue.Category, g.Clue.Row)
	}
	if last := g.Log[len(g.Log)-2].Text; last != "Time is up: Ann gets the next clue" {
		t.Fatalf("log %q", last)
	}
	runToPhase(t, g, c, PhasePick)
	pickCell(t, g, 3, 0)
	runToPhase(t, g, c, PhasePick)
	// The cheapest open cell in the last picked category.
	tick(g, c)
	if g.Clue.Category != 3 || g.Clue.Row != 1 {
		t.Fatalf("auto-pick (%d, %d), want (3, 1) in the last category", g.Clue.Category, g.Clue.Row)
	}
	runToPhase(t, g, c, PhasePick)
	// The last category is used up: the cheapest open cell anywhere.
	for _, cell := range g.board().Categories[3].Cells {
		cell.Played = true
	}
	tick(g, c)
	if g.Clue.Category != 1 || g.Clue.Row != 0 || g.ChooserID != ann {
		t.Fatalf("auto-pick (%d, %d), want (1, 0)", g.Clue.Category, g.Clue.Row)
	}
}

// TestRoundsAndControl plays a Full game: round 1 to its end, the round end,
// and round 2 at double values with two gold squares, first picked by the
// lowest score.
func TestRoundsAndControl(t *testing.T) {
	g, c := newGame(t, 4)
	ann, ben := addHuman(t, g, "Ann"), addHuman(t, g, "Ben")
	mustAct(t, g, ann, `{"type":"settings","settings":{"length":"full"}}`)
	startGame(t, g)
	if len(g.Boards) != 2 {
		t.Fatalf("%d boards, want 2", len(g.Boards))
	}
	runToPhase(t, g, c, PhasePick)
	g.ChooserID = ben
	pickCell(t, g, 0, 0)
	runToPhase(t, g, c, PhaseAnswer)
	c.now = g.Clue.Q.OpenAt + 1500
	if err := answerNow(g, ann, g.Clue.Q.Answer, 1400); err != nil {
		t.Fatal(err)
	}
	// Ann won the clue, so Ann picks next; nobody answers anything else, so
	// Ann keeps control and finds the gold square (bet timer: 5, no answer:
	// -5).
	runToPhase(t, g, c, PhasePick)
	if g.ChooserID != ann {
		t.Fatalf("chooser %q, want the winner %q", g.ChooserID, ann)
	}
	runToPhase(t, g, c, PhaseRoundEnd)
	if g.PhaseEndsAt != c.now+roundEndMs || g.Clue != nil {
		t.Fatalf("round end ends %d (want %d), clue %v", g.PhaseEndsAt, c.now+roundEndMs, g.Clue)
	}
	if a, b := g.Player(ann).Score, g.Player(ben).Score; a != 100-goldMinBet || b != 0 {
		t.Fatalf("after round 1: %d and %d, want %d and 0", a, b, 100-goldMinBet)
	}
	tick(g, c)
	if g.Phase != PhaseIntro || g.Round != 2 || g.ChooserID != ben {
		t.Fatalf("phase %s round %d chooser %q; want intro, round 2, the lowest score %q", g.Phase, g.Round, g.ChooserID, ben)
	}
	golds := map[int]bool{}
	for ci, bc := range g.board().Categories {
		for r, cell := range bc.Cells {
			if cell.Value != (r+1)*200 || cell.Played {
				t.Fatalf("round 2 cell (%d, %d): value %d, played %v", ci, r, cell.Value, cell.Played)
			}
			if cell.Gold {
				if golds[ci] {
					t.Fatal("two gold squares in one category")
				}
				golds[ci] = true
			}
		}
	}
	if len(golds) != 2 {
		t.Fatalf("%d gold squares in round 2, want 2", len(golds))
	}
	runToPhase(t, g, c, PhaseFinalIntro)
	if g.Round != finalRound || g.Final == nil || g.board() != nil {
		t.Fatalf("final: round %d", g.Round)
	}
}

// goldGame seats Ann and Ben, gives Ann a score and control, and has her
// pick the gold square; it returns in the bet phase.
func goldGame(t *testing.T, annScore int) (*Game, *clock, string, string) {
	t.Helper()
	g, c := newGame(t, 5)
	ann, ben := addHuman(t, g, "Ann"), addHuman(t, g, "Ben")
	startGame(t, g)
	runToPhase(t, g, c, PhasePick)
	g.ChooserID = ann
	g.Player(ann).Score = annScore
	cat, row := firstOpen(g, true)
	pickCell(t, g, cat, row)
	if g.Phase != PhaseGold || g.PhaseEndsAt != c.now+goldMs || !g.Clue.Gold || g.Clue.Finder != ann {
		t.Fatalf("after picking the gold square: phase %s ends %d, clue %+v", g.Phase, g.PhaseEndsAt, g.Clue)
	}
	expectErr(t, answerNow(g, ann, 0, 100), "answers are not open yet")
	expectErr(t, act(g, ann, `{"type":"bet","amount":100}`), "there is nothing to bet on now")
	tick(g, c)
	if g.Phase != PhaseBet || g.PhaseEndsAt != c.now+betMs {
		t.Fatalf("phase %s ends %d, want bet ending %d", g.Phase, g.PhaseEndsAt, c.now+betMs)
	}
	return g, c, ann, ben
}

func TestGoldSquare(t *testing.T) {
	t.Run("right wins the bet", func(t *testing.T) {
		g, c, ann, ben := goldGame(t, 900)
		if b := g.Bet; b.Min != 5 || b.Max != 900 {
			t.Fatalf("bet range %d–%d, want 5–900 (the finder's score is above 500)", b.Min, b.Max)
		}
		expectErr(t, act(g, ben, `{"type":"bet","amount":100}`), "only the gold square finder bets on it")
		expectErr(t, act(g, ann, `{"type":"bet","amount":4}`), "bet between 5 and 900")
		expectErr(t, act(g, ann, `{"type":"bet","amount":901}`), "bet between 5 and 900")
		expectErr(t, act(g, ann, `{"type":"bet"}`), "bet between 5 and 900")
		mustAct(t, g, ann, `{"type":"bet","amount":900}`)
		if g.Phase != PhaseRead || len(g.Clue.Eligible) != 1 || g.Clue.Eligible[0] != ann {
			t.Fatalf("phase %s, eligible %v; want read with only the finder", g.Phase, g.Clue.Eligible)
		}
		runToPhase(t, g, c, PhaseAnswer)
		q := g.Clue.Q
		c.now = q.OpenAt + 500
		expectErr(t, answerNow(g, ben, q.Answer, 400), "only the gold square finder answers this one")
		if err := answerNow(g, ann, q.Answer, 400); err != nil {
			t.Fatal(err)
		}
		tick(g, c)
		if g.Phase != PhaseReveal || g.PhaseEndsAt != c.now+revealLongMs || g.Player(ann).Score != 1800 {
			t.Fatalf("phase %s ends %d, score %d; want reveal for 4.5 s and 1800", g.Phase, g.PhaseEndsAt, g.Player(ann).Score)
		}
	})
	t.Run("wrong loses the bet", func(t *testing.T) {
		g, c, ann, _ := goldGame(t, 0)
		if g.Bet.Max != 500 {
			t.Fatalf("bet max %d, want the round's top value 500", g.Bet.Max)
		}
		mustAct(t, g, ann, `{"type":"bet","amount":300}`)
		runToPhase(t, g, c, PhaseAnswer)
		c.now = g.Clue.Q.OpenAt + 700
		if err := answerNow(g, ann, wrongChoice(g.Clue.Q), 600); err != nil {
			t.Fatal(err)
		}
		if g.Phase != PhaseReveal || g.Player(ann).Score != -300 {
			t.Fatalf("phase %s, score %d; want reveal and -300", g.Phase, g.Player(ann).Score)
		}
	})
	t.Run("no answer loses the bet", func(t *testing.T) {
		g, c, ann, ben := goldGame(t, 200)
		mustAct(t, g, ann, `{"type":"bet","amount":250}`)
		runToPhase(t, g, c, PhaseReveal)
		if g.Player(ann).Score != -50 || g.Player(ben).Score != 0 {
			t.Fatalf("scores %d and %d, want -50 and 0", g.Player(ann).Score, g.Player(ben).Score)
		}
		if last := g.Events[len(g.Events)-1]; last.Type != EvReveal {
			t.Fatalf("last event %s, want reveal", last.Type)
		}
	})
	t.Run("the bet timer bets the minimum", func(t *testing.T) {
		g, c, ann, _ := goldGame(t, 0)
		tick(g, c)
		if g.Phase != PhaseRead || !g.Bet.Placed || g.Bet.Amount != goldMinBet {
			t.Fatalf("phase %s, bet %+v; want read with the minimum bet", g.Phase, g.Bet)
		}
		if g.Player(ann).Score != 0 {
			t.Fatal("the bet changed the score before the answer")
		}
	})
}

// finalGame plays three people through the board without answering and
// returns in the final intro with the scores set.
func finalGame(t *testing.T, scores ...int) (*Game, *clock, []string) {
	t.Helper()
	g, c := newGame(t, 6)
	var ids []string
	for _, name := range []string{"Ann", "Ben", "Cat"}[:len(scores)] {
		ids = append(ids, addHuman(t, g, name))
	}
	startGame(t, g)
	runToPhase(t, g, c, PhaseFinalIntro)
	if g.PhaseEndsAt != c.now+finalIntroMs || g.Final.Category == "" || g.Final.Q != nil {
		t.Fatalf("final intro ends %d (want %d), final %+v", g.PhaseEndsAt, c.now+finalIntroMs, g.Final)
	}
	for i, id := range ids {
		g.Player(id).Score = scores[i]
	}
	return g, c, ids
}

func TestFinal(t *testing.T) {
	g, c, ids := finalGame(t, -300, 1200, 400)
	ann, ben, cat := ids[0], ids[1], ids[2]
	expectErr(t, act(g, ann, `{"type":"bet","amount":10}`), "there is nothing to bet on now")
	tick(g, c)
	if g.Phase != PhaseFinalBet || g.PhaseEndsAt != c.now+finalBetMs {
		t.Fatalf("phase %s ends %d, want finalBet ending %d", g.Phase, g.PhaseEndsAt, c.now+finalBetMs)
	}
	for id, max := range map[string]int{ann: 500, ben: 1200, cat: 500} {
		you := g.View(id, Viewer{Role: "player"}).You
		if !you.CanBet || you.BetMin != 0 || you.BetMax != max || you.Bet != nil {
			t.Fatalf("%s: %+v; want a bet of 0–%d (a score below 500 may still bet 500)", id, you, max)
		}
	}
	expectErr(t, act(g, ann, `{"type":"bet","amount":501}`), "bet between 0 and 500")
	expectErr(t, act(g, ben, `{"type":"bet","amount":-1}`), "bet between 0 and 1200")
	expectErr(t, answerNow(g, ann, 0, 100), "answers are not open yet")
	mustAct(t, g, ann, `{"type":"bet","amount":500}`)
	expectErr(t, act(g, ann, `{"type":"bet","amount":100}`), "you have already bet")
	mustAct(t, g, ben, `{"type":"bet","amount":1200}`)
	if v := g.View(ann, Viewer{Role: "player"}); *v.You.Bet != 500 || v.You.CanBet || len(v.Final.BetsIn) != 2 {
		t.Fatalf("Ann's view: you %+v, bets in %v", v.You, v.Final.BetsIn)
	}
	// Cat never bets: the timer bets 0 for her.
	tick(g, c)
	if bet, ok := g.Final.betOf(cat); g.Phase != PhaseFinalRead || !ok || bet != 0 {
		t.Fatalf("phase %s, Cat's bet %d (%v); want finalRead and 0", g.Phase, bet, ok)
	}
	q := g.Final.Q
	if q.Tier != finalTier || q.ClosesAt != q.OpenAt+finalAnswerMs {
		t.Fatalf("final question tier %d, closes %d after opening", q.Tier, q.ClosesAt-q.OpenAt)
	}
	tick(g, c)
	if g.Phase != PhaseFinalAnswer || g.PhaseEndsAt != q.ClosesAt {
		t.Fatalf("phase %s, want finalAnswer", g.Phase)
	}
	c.now = q.OpenAt + 4000
	if err := answerNow(g, ann, q.Answer, 3900); err != nil {
		t.Fatal(err)
	}
	expectErr(t, answerNow(g, ann, q.Answer, 3900), "you have already answered")
	if err := answerNow(g, ben, wrongChoice(q), 3950); err != nil {
		t.Fatal(err)
	}
	if v := g.View(ann, Viewer{Role: "player"}); v.You.FinalChoice != q.Answer || v.Final.AnswerIndex != nil || len(v.Final.AnswersIn) != 2 {
		t.Fatalf("Ann's view before the reveal: you %+v, final %+v", v.You, v.Final)
	}
	// Cat does not answer: the reveal waits for the close and the grace.
	tick(g, c)
	tick(g, c)
	if g.Phase != PhaseFinalReveal || c.now != q.ClosesAt+300 {
		t.Fatalf("phase %s at %d, want finalReveal at %d", g.Phase, c.now, q.ClosesAt+300)
	}
	if order := strings.Join(g.Final.Order, ","); order != ann+","+cat+","+ben {
		t.Fatalf("reveal order %s, want lowest score first", order)
	}
	wantAfter := []int{200, 400, 0}
	for i := range 3 {
		tick(g, c)
		r := g.Final.Results[i]
		if r.After != wantAfter[i] || g.Final.Revealed != i+1 {
			t.Fatalf("reveal %d: %+v, want %d points", i, r, wantAfter[i])
		}
	}
	if r := g.Final.Results[1]; r.Choice != -1 || r.Correct || r.Bet != 0 {
		t.Fatalf("Cat's result %+v: no answer counts as wrong", r)
	}
	tick(g, c)
	if g.Status != StatusFinished || g.Phase != PhaseFinished {
		t.Fatalf("status %s phase %s, want finished", g.Status, g.Phase)
	}
	places := map[string]int{cat: 1, ann: 2, ben: 3}
	for id, place := range places {
		if g.Player(id).Place != place {
			t.Fatalf("%s place %d, want %d", id, g.Player(id).Place, place)
		}
	}
	if len(g.WinnerIDs) != 1 || g.WinnerIDs[0] != cat {
		t.Fatalf("winners %v, want [%s]", g.WinnerIDs, cat)
	}
	v := g.View("", Viewer{Role: "table"})
	if len(v.Standings) != 3 || v.Standings[0].ID != cat || v.Standings[2].ID != ben || v.Final.Revealed == nil || *v.Final.Revealed != 3 {
		t.Fatalf("standings %+v", v.Standings)
	}
	if g.NextDeadline() != 0 {
		t.Fatal("a finished game still has a timer")
	}
}

func TestFinalTies(t *testing.T) {
	g, c, ids := finalGame(t, 300, 300)
	tick(g, c)
	for _, id := range ids {
		mustAct(t, g, id, `{"type":"bet","amount":0}`)
	}
	runToPhase(t, g, c, PhaseFinished)
	if len(g.WinnerIDs) != 2 || g.Player(ids[0]).Place != 1 || g.Player(ids[1]).Place != 1 {
		t.Fatalf("winners %v: a tie shares first place", g.WinnerIDs)
	}
}

func TestLeaving(t *testing.T) {
	t.Run("the chooser leaves", func(t *testing.T) {
		g, c := newGame(t, 7)
		ann, ben, cat := addHuman(t, g, "Ann"), addHuman(t, g, "Ben"), addHuman(t, g, "Cat")
		startGame(t, g)
		runToPhase(t, g, c, PhasePick)
		g.ChooserID = ann
		g.Player(ben).Score, g.Player(cat).Score = 100, 300
		c.now += 5000
		if err := g.RemovePlayer(ann, ann); err != nil {
			t.Fatal(err)
		}
		if g.ChooserID != cat || g.PhaseEndsAt != c.now+pickMs {
			t.Fatalf("chooser %q, pick ends %d; want the highest score %q with the whole pick time", g.ChooserID, g.PhaseEndsAt, cat)
		}
		if g.HostID != ben {
			t.Fatalf("host %q, want the next person %q", g.HostID, ben)
		}
		expectErr(t, act(g, ann, `{"type":"pick","category":0,"row":0}`), "you are not playing in this game")
		expectErr(t, g.RemovePlayer(ben, ann), "no such player")
		if v := g.View(ann, Viewer{Role: "player"}); v.Players[0].Status != "left" || v.You.CanPick {
			t.Fatalf("Ann's view after leaving: %+v %+v", v.Players[0], v.You)
		}
	})
	t.Run("the gold square finder leaves", func(t *testing.T) {
		g, c, ann, ben := goldGame(t, 0)
		cell := g.board().Categories[g.Clue.Category].Cells[g.Clue.Row]
		if err := g.RemovePlayer(ann, ann); err != nil {
			t.Fatal(err)
		}
		if g.Phase != PhasePick || g.ChooserID != ben || !cell.Played || g.PhaseEndsAt != c.now+pickMs {
			t.Fatalf("phase %s, chooser %q, cell played %v", g.Phase, g.ChooserID, cell.Played)
		}
	})
	t.Run("the last one to answer leaves", func(t *testing.T) {
		g, c := newGame(t, 8)
		ann, ben := addHuman(t, g, "Ann"), addHuman(t, g, "Ben")
		startGame(t, g)
		runToPhase(t, g, c, PhasePick)
		cat, row := firstOpen(g, false)
		pickCell(t, g, cat, row)
		runToPhase(t, g, c, PhaseAnswer)
		value := g.Clue.Value
		c.now = g.Clue.Q.OpenAt + 1000
		if err := answerNow(g, ann, wrongChoice(g.Clue.Q), 900); err != nil {
			t.Fatal(err)
		}
		if err := g.RemovePlayer(ben, ben); err != nil {
			t.Fatal(err)
		}
		if g.Phase != PhaseReveal || g.Player(ann).Score != -value {
			t.Fatalf("phase %s, score %d; want the clue judged at once", g.Phase, g.Player(ann).Score)
		}
	})
	t.Run("a player leaves before betting in the final", func(t *testing.T) {
		g, c, ids := finalGame(t, 100, 200, 300)
		tick(g, c)
		mustAct(t, g, ids[0], `{"type":"bet","amount":100}`)
		mustAct(t, g, ids[1], `{"type":"bet","amount":100}`)
		if err := g.RemovePlayer(ids[2], ids[2]); err != nil {
			t.Fatal(err)
		}
		if g.Phase != PhaseFinalRead {
			t.Fatalf("phase %s, want finalRead once everyone left has bet", g.Phase)
		}
		runToPhase(t, g, c, PhaseFinished)
		if p := g.Player(ids[2]); p.Place != 3 || p.Score != 300 {
			t.Fatalf("the leaver: place %d, score %d; want last with the score kept", p.Place, p.Score)
		}
		if len(g.Final.Order) != 2 {
			t.Fatalf("reveal order %v, want only the two still playing", g.Final.Order)
		}
	})
	t.Run("everyone leaves", func(t *testing.T) {
		g, c := newGame(t, 9)
		ann := addHuman(t, g, "Ann")
		startGame(t, g)
		runToPhase(t, g, c, PhasePick)
		if err := g.RemovePlayer(ann, ann); err != nil {
			t.Fatal(err)
		}
		if g.Status != StatusFinished || len(g.WinnerIDs) != 0 {
			t.Fatalf("status %s, winners %v", g.Status, g.WinnerIDs)
		}
	})
}

func TestEndGame(t *testing.T) {
	g, c := newGame(t, 10)
	ann, ben := addHuman(t, g, "Ann"), addHuman(t, g, "Ben")
	_, err := g.EndGame()
	expectErr(t, err, "the game is not in progress")
	startGame(t, g)
	runToPhase(t, g, c, PhaseAnswer)
	g.Player(ben).Score = 300
	winner, err := g.EndGame()
	if err != nil || winner != ben {
		t.Fatalf("EndGame = %q, %v; want %q", winner, err, ben)
	}
	if g.Status != StatusFinished || g.Clue != nil || g.Player(ann).Place != 2 || g.NextDeadline() != 0 {
		t.Fatalf("after EndGame: status %s, Ann's place %d", g.Status, g.Player(ann).Place)
	}
	expectErr(t, answerNow(g, ann, 0, 100), "the game is not in progress")
}

// TestComputers checks the computers' timing: a pick after 1.5–3 s, a gold
// bet after 1.6–2.4 s, final bets after 2–5 s, and taps planned when the
// choices open.
func TestComputers(t *testing.T) {
	for seed := range byte(20) {
		g, c := newGame(t, seed)
		bot := addComputer(t, g, "Robo", LevelNormal)
		addComputer(t, g, "Hal", LevelHard)
		startGame(t, g)
		runToPhase(t, g, c, PhasePick)
		g.ChooserID = bot
		g.enterPick(c.now)
		if d := g.NextDeadline() - c.now; d < computerPickMinMs || d > computerPickMaxMs {
			t.Fatalf("computer picks after %d ms", d)
		}
		g.SetLatency(bot, 900, false)
		if p := g.Player(bot); p.Latency != 0 || !p.Online {
			t.Fatal("SetLatency changed a computer")
		}
		cat, row := firstOpen(g, true)
		g.playCell(g.Player(bot), cat, row, c.now)
		tick(g, c)
		if g.Phase != PhaseBet {
			t.Fatalf("phase %s, want bet", g.Phase)
		}
		if d := g.NextDeadline() - c.now; d < computerBetMinMs || d > computerBetMaxMs {
			t.Fatalf("computer bets after %d ms", d)
		}
		tick(g, c)
		if b := g.Bet; !b.Placed || b.Amount < b.Min || b.Amount > b.Max {
			t.Fatalf("computer bet %+v", b)
		}
		runToPhase(t, g, c, PhaseAnswer)
		// The finder always answers a gold square.
		if p := g.Player(bot).Plan; p == nil || p.At != g.Clue.Q.OpenAt+int64(p.Ms) || p.Ms < minTapMs {
			t.Fatalf("plan %+v", p)
		}
	}
}

func TestSnapshotRestore(t *testing.T) {
	// Pause the same seeded game at several points, restore it, and play
	// both copies to the end: they must finish identically.
	for _, pause := range []string{PhaseRead, PhaseAnswer, PhaseBet, PhaseFinalBet, PhaseFinalAnswer} {
		t.Run(pause, func(t *testing.T) {
			g, c := newGame(t, 11)
			addHuman(t, g, "Ann")
			addComputer(t, g, "Robo", LevelEasy)
			addComputer(t, g, "Hal", LevelHard)
			startGame(t, g)
			runToPhase(t, g, c, pause)
			if pause == PhaseAnswer {
				tick(g, c) // a computer's planned tap is pending
			}
			snap, err := g.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			c2 := &clock{now: c.now}
			r, err := Restore(snap, Options{Clock: c2.read, Library: testLibrary(t)})
			if err != nil {
				t.Fatal(err)
			}
			again, _ := r.Snapshot()
			if string(again) != string(snap) {
				t.Fatal("a restored game snapshots differently")
			}
			runToPhase(t, g, c, PhaseFinished)
			runToPhase(t, r, c2, PhaseFinished)
			a, _ := g.Snapshot()
			b, _ := r.Snapshot()
			if string(a) != string(b) {
				t.Fatal("the restored game played differently")
			}
		})
	}
	t.Run("bad snapshots", func(t *testing.T) {
		if _, err := Restore([]byte("{"), Options{}); err == nil {
			t.Fatal("restored a broken snapshot")
		}
		snap, _ := json.Marshal(map[string]any{"code": "X", "settings": map[string]any{"length": "epic"}})
		_, err := Restore(snap, Options{})
		expectErr(t, err, `saved quiz game: length must be "quick" or "full"`)
	})
}

func TestEvents(t *testing.T) {
	g, c := newGame(t, 12)
	ann := addHuman(t, g, "Ann")
	startGame(t, g)
	runToPhase(t, g, c, PhaseAnswer)
	c.now = g.Clue.Q.OpenAt + 800
	if err := answerNow(g, ann, g.Clue.Q.Answer, 700); err != nil {
		t.Fatal(err)
	}
	runToPhase(t, g, c, PhaseReveal)
	var types []string
	for _, e := range g.EventsSince(0) {
		types = append(types, e.Type)
	}
	want := "join start intro pick open tap right reveal"
	if got := strings.Join(types, " "); !strings.HasPrefix(got, "join start intro pick") || !strings.HasSuffix(got, "open tap right reveal") {
		t.Fatalf("events %q, want %q (a gold square may come between)", got, want)
	}
	last := g.Events[len(g.Events)-1]
	if last.Clue != g.Clue.Seq || len(last.Taps) != 1 || last.Taps[0].Ms == nil || *last.Taps[0].Ms != 700 || last.Taps[0].Arrival != 800 {
		t.Fatalf("reveal event %+v", last)
	}
	if n := len(g.EventsSince(last.Seq)); n != 0 {
		t.Fatalf("%d events after the last", n)
	}
}
