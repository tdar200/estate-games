package holdem

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

type fakeClock struct{ now int64 }

func (c *fakeClock) Now() int64 { return c.now }

func newTestTable(t *testing.T, names ...string) (*Table, *fakeClock, []string) {
	t.Helper()
	clock := &fakeClock{now: 1_700_000_000_000}
	seed := [32]byte{1, 2, 3}
	tm := Timing{} // no pauses
	tb := NewTable("TEST", Options{Seed: &seed, Clock: clock.Now, Timing: &tm})
	var ids []string
	for _, n := range names {
		kind := KindHuman
		if strings.HasPrefix(n, "Bot") {
			kind = KindAI
		}
		id, err := tb.AddPlayer(n, kind)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return tb, clock, ids
}

func do(t *testing.T, tb *Table, id string, js string) {
	t.Helper()
	if err := tb.PerformAction(id, json.RawMessage(js)); err != nil {
		t.Fatalf("%s %s: %v", id, js, err)
	}
}

// playToEnd plays checks/calls until the hand is over.
func playToEnd(t *testing.T, tb *Table) {
	t.Helper()
	for guard := 0; tb.Phase == PhaseBetting || tb.Phase == PhaseRunout; guard++ {
		if guard > 200 {
			t.Fatal("hand did not finish")
		}
		if tb.Phase == PhaseRunout {
			tb.Advance(tb.Now())
			continue
		}
		w := tb.Waiting()
		do(t, tb, w[0].PlayerID, string(tb.SafeCall(w[0].PlayerID)))
	}
}

// SafeCall is a test helper: check, else call.
func (t *Table) SafeCall(id string) json.RawMessage {
	if t.Hand.Legal().CanCheck {
		return json.RawMessage(`{"type":"check"}`)
	}
	return json.RawMessage(`{"type":"call"}`)
}

func TestLobbyStartAndButtonRotation(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann", "Ben", "Cat")
	if tb.HostID != ids[0] {
		t.Fatalf("host %q", tb.HostID)
	}
	if err := tb.PerformAction(ids[1], json.RawMessage(`{"type":"start"}`)); err == nil {
		t.Fatal("only the host can start")
	}
	do(t, tb, ids[0], `{"type":"settings","settings":{"startingStack":2000,"blindSchedule":"off"}}`)
	do(t, tb, ids[0], `{"type":"start"}`)
	if tb.Status != StatusPlaying || tb.Phase != PhaseBetting || tb.HandNumber != 1 {
		t.Fatalf("status %s phase %s hand %d", tb.Status, tb.Phase, tb.HandNumber)
	}
	var buttons []int
	for k := 0; k < 4; k++ {
		buttons = append(buttons, tb.ButtonSeat)
		playToEnd(t, tb)
		tb.Advance(tb.Now())
	}
	for k := 1; k < len(buttons); k++ {
		if buttons[k] != (buttons[k-1]+1)%3 {
			t.Fatalf("button seats %v should rotate clockwise", buttons)
		}
	}
	// chips are conserved
	total := 0
	for _, p := range tb.Players {
		total += p.Stack
	}
	if h := tb.Hand; h != nil && !h.Done {
		total = 0
		for i := range h.Stacks {
			total += h.Stacks[i] + h.Contrib[i]
		}
	}
	if total != 6000 {
		t.Fatalf("total chips %d", total)
	}
}

func TestPublicStateHidesHoleCards(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann", "Ben", "Cat")
	do(t, tb, ids[0], `{"type":"start"}`)
	check := func(viewer string, wantOwn bool) {
		t.Helper()
		raw, err := tb.PublicState(viewer, Viewer{Role: "player"})
		if err != nil {
			t.Fatal(err)
		}
		var v View
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		for _, p := range v.Players {
			if p.ID == viewer {
				if wantOwn && len(p.Cards) != 2 {
					t.Fatalf("viewer %s cannot see own cards", viewer)
				}
				continue
			}
			if p.Cards != nil {
				t.Fatalf("viewer %q sees %s's cards %v", viewer, p.ID, p.Cards)
			}
		}
		// No hidden card may appear anywhere in the JSON.
		for i, id := range tb.HandIDs {
			if id == viewer || tb.Hand.Shown[i] {
				continue
			}
			for _, c := range tb.Hand.Hole[i] {
				if strings.Contains(string(raw), `"`+CardString(c)+`"`) && !strings.Contains(strings.Join(cardStrings(tb.Hand.Board), ","), CardString(c)) {
					t.Fatalf("hidden card %s of %s leaks to %q", CardString(c), id, viewer)
				}
			}
		}
		if wantOwn && (v.You == nil || len(v.You.Cards) != 2 || v.You.HandName == "") {
			t.Fatalf("you panel %+v", v.You)
		}
	}
	check(ids[0], true)
	check(ids[1], true)
	check("", false) // the table screen
	// The deck never leaves the server.
	raw, _ := tb.PublicState("", Viewer{Role: "table"})
	if strings.Contains(string(raw), `"deck"`) {
		t.Fatal("deck in public state")
	}
}

func TestShowdownRevealsWinners(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann", "Ben")
	do(t, tb, ids[0], `{"type":"start"}`)
	// Everybody all-in: both hands are tabled during the run-out.
	w := tb.Waiting()[0].PlayerID
	do(t, tb, w, `{"type":"allin"}`)
	w = tb.Waiting()[0].PlayerID
	do(t, tb, w, `{"type":"call"}`)
	if tb.Phase != PhaseRunout {
		t.Fatalf("phase %s", tb.Phase)
	}
	var v View
	raw, _ := tb.PublicState("", Viewer{Role: "table"})
	json.Unmarshal(raw, &v)
	for _, p := range v.Players {
		if len(p.Cards) != 2 || !p.Shown {
			t.Fatalf("all-in hands should be face up: %+v", p)
		}
	}
	for tb.Phase == PhaseRunout {
		tb.Advance(tb.Now())
	}
	if tb.Phase != PhaseShowdown && tb.Status != StatusFinished {
		t.Fatalf("phase %s", tb.Phase)
	}
	raw, _ = tb.PublicState("", Viewer{Role: "table"})
	json.Unmarshal(raw, &v)
	if v.Result == nil || !v.Result.Showdown || len(v.Result.Pots) == 0 || v.Result.Pots[0].Winners[0].HandName == "" {
		t.Fatalf("result %+v", v.Result)
	}
}

func TestBlindLevelsRise(t *testing.T) {
	tb, clock, ids := newTestTable(t, "Ann", "Ben", "Cat")
	do(t, tb, ids[0], `{"type":"settings","settings":{"targetMinutes":30}}`)
	do(t, tb, ids[0], `{"type":"start"}`)
	if len(tb.Levels) < 5 || tb.Levels[0].Minutes == 0 {
		t.Fatalf("levels %+v", tb.Levels)
	}
	for i := 1; i < len(tb.Levels); i++ {
		if tb.Levels[i].BigBlind <= tb.Levels[i-1].BigBlind || tb.Levels[i].SmallBlind*2 != tb.Levels[i].BigBlind {
			t.Fatalf("levels not increasing: %+v", tb.Levels)
		}
	}
	playToEnd(t, tb)
	clock.now += int64(tb.Levels[0].Minutes)*60_000 + 1
	tb.Advance(clock.now)
	if tb.LevelIndex != 1 || tb.Hand.BigBlind != tb.Levels[1].BigBlind {
		t.Fatalf("level %d, hand blind %d", tb.LevelIndex, tb.Hand.BigBlind)
	}
}

func TestScheduleFinishesNearTarget(t *testing.T) {
	s := DefaultSettings()
	for _, minutes := range []int{15, 30, 60, 120, 240} {
		s.TargetMinutes = minutes
		lv := BuildSchedule(s, 6)
		elapsed, reached := 0, -1
		for _, l := range lv {
			if l.BigBlind*20 >= 6*s.StartingStack && reached < 0 {
				reached = elapsed
			}
			elapsed += l.Minutes
		}
		if reached < minutes*6/10 || reached > minutes*14/10 {
			t.Errorf("target %d min: big blind reaches 1/20 of the chips after %d min (%+v)", minutes, reached, lv)
		}
	}
}

func TestBustRebuyAndPlaces(t *testing.T) {
	tb, clock, ids := newTestTable(t, "Ann", "Ben", "Cat")
	tb.Timing.RebuyMs = 10_000
	do(t, tb, ids[0], `{"type":"settings","settings":{"rebuys":1,"blindSchedule":"off"}}`)
	do(t, tb, ids[0], `{"type":"start"}`)
	// Play all-in hands until somebody busts.
	for guard := 0; guard < 50; guard++ {
		busted := false
		for _, p := range tb.Players {
			busted = busted || p.Busted
		}
		if busted {
			break
		}
		for tb.Phase == PhaseBetting {
			w := tb.Waiting()[0].PlayerID
			do(t, tb, w, `{"type":"allin"}`)
		}
		for tb.Phase == PhaseRunout {
			tb.Advance(tb.Now())
		}
		tb.Advance(tb.Now())
	}
	var bust *Player
	for _, p := range tb.Players {
		if p.Busted {
			bust = p
		}
	}
	if bust == nil {
		t.Fatal("nobody busted")
	}
	raw, _ := tb.PublicState(bust.ID, Viewer{Role: "player"})
	var v View
	json.Unmarshal(raw, &v)
	if !v.You.CanRebuy {
		t.Fatalf("busted player should be offered a rebuy: %+v", v.You)
	}
	do(t, tb, bust.ID, `{"type":"rebuy"}`)
	if bust.Stack != tb.Settings.StartingStack || bust.Rebuys != 1 {
		t.Fatalf("after rebuy %+v", bust)
	}
	// No rebuys left: the next bust is final, and an expired offer too.
	for guard := 0; guard < 200 && tb.Status == StatusPlaying; guard++ {
		for tb.Phase == PhaseBetting {
			w := tb.Waiting()[0].PlayerID
			do(t, tb, w, `{"type":"allin"}`)
		}
		for tb.Phase == PhaseRunout {
			tb.Advance(tb.Now())
		}
		clock.now += 20_000
		tb.Advance(clock.now)
	}
	if tb.Status != StatusFinished || tb.WinnerID == "" {
		t.Fatalf("status %s winner %q", tb.Status, tb.WinnerID)
	}
	places := map[int]bool{}
	for _, p := range tb.Players {
		places[p.Place] = true
	}
	if !places[1] || !places[2] || !places[3] {
		for _, l := range tb.Log {
			t.Log(l.Text)
		}
		t.Fatalf("places %+v", tb.standings())
	}
	raw, _ = tb.PublicState("", Viewer{Role: "table"})
	json.Unmarshal(raw, &v)
	if v.Phase != PhaseFinished || len(v.Standings) != 3 || v.Standings[0].ID != tb.WinnerID {
		t.Fatalf("standings %+v", v.Standings)
	}
}

func TestSitOutAndBackIn(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann", "Ben", "Cat")
	do(t, tb, ids[0], `{"type":"start"}`)
	do(t, tb, ids[2], `{"type":"sitOut"}`)
	playToEnd(t, tb)
	tb.Advance(tb.Now())
	if tb.handIndex(ids[2]) >= 0 {
		t.Fatal("a sitting-out player was dealt in")
	}
	if len(tb.HandIDs) != 2 {
		t.Fatalf("hand ids %v", tb.HandIDs)
	}
	do(t, tb, ids[2], `{"type":"sitIn"}`)
	playToEnd(t, tb)
	tb.Advance(tb.Now())
	if tb.handIndex(ids[2]) < 0 {
		t.Fatal("player should be dealt back in")
	}
}

func TestPausesWhenOnlyOneCanPlay(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann", "Ben")
	do(t, tb, ids[0], `{"type":"start"}`)
	do(t, tb, ids[1], `{"type":"sitOut"}`)
	playToEnd(t, tb)
	tb.Advance(tb.Now())
	if tb.Phase != PhasePaused {
		t.Fatalf("phase %s", tb.Phase)
	}
	do(t, tb, ids[1], `{"type":"sitIn"}`)
	if tb.Phase != PhaseBetting {
		t.Fatalf("phase after sit-in %s", tb.Phase)
	}
}

func TestShotClock(t *testing.T) {
	tb, clock, ids := newTestTable(t, "Ann", "Ben", "Cat")
	do(t, tb, ids[0], `{"type":"settings","settings":{"actionSeconds":20,"timeBankSeconds":0}}`)
	do(t, tb, ids[0], `{"type":"start"}`)
	actor := tb.Waiting()[0]
	if actor.Deadline != clock.now+20_000 || tb.NextDeadline() != actor.Deadline {
		t.Fatalf("deadline %d next %d", actor.Deadline, tb.NextDeadline())
	}
	clock.now += 20_000
	if !tb.Advance(clock.now) {
		t.Fatal("timeout should change the state")
	}
	i := -1
	for k, id := range tb.HandIDs {
		if id == actor.PlayerID {
			i = k
		}
	}
	if !tb.Hand.Folded[i] {
		t.Fatal("timed-out player facing a bet should fold")
	}
}

func TestSnapshotRestoreMidHand(t *testing.T) {
	tb, clock, ids := newTestTable(t, "Ann", "Ben", "Cat", "Bot Dee")
	do(t, tb, ids[0], `{"type":"start"}`)
	w := tb.Waiting()[0].PlayerID
	do(t, tb, w, `{"type":"raise","to":60}`)
	snap, err := tb.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	tm := Timing{}
	tb2, err := Restore(snap, Options{Clock: clock.Now, Timing: &tm})
	if err != nil {
		t.Fatal(err)
	}
	a1, _ := tb.PublicState(ids[1], Viewer{Role: "player"})
	a2, _ := tb2.PublicState(ids[1], Viewer{Role: "player"})
	if string(a1) != string(a2) {
		t.Fatalf("restored view differs\n%s\n%s", a1, a2)
	}
	// Both continue identically, including future shuffles.
	for k := 0; k < 3; k++ {
		for _, x := range []*Table{tb, tb2} {
			playToEnd(t, x)
			x.Advance(x.Now())
		}
	}
	s1, _ := tb.Snapshot()
	s2, _ := tb2.Snapshot()
	if string(s1) != string(s2) {
		t.Fatal("tables diverged after restore")
	}
}

func TestEndGameRanksByChips(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann", "Ben", "Cat")
	do(t, tb, ids[0], `{"type":"start"}`)
	w := tb.Waiting()[0].PlayerID
	do(t, tb, w, `{"type":"raise","to":100}`)
	if _, err := tb.EndGame(); err != nil {
		t.Fatal(err)
	}
	for _, p := range tb.Players {
		if p.Stack != 1500 {
			t.Fatalf("hand in progress should be refunded: %+v", p)
		}
	}
	if tb.Status != StatusFinished || tb.WinnerID != "" {
		t.Fatalf("all tied: status %s winner %q", tb.Status, tb.WinnerID)
	}
}

func TestLateJoinAndLeave(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann", "Ben")
	do(t, tb, ids[0], `{"type":"start"}`)
	cid, err := tb.AddPlayer("Cat", KindHuman)
	if err != nil {
		t.Fatal(err)
	}
	if tb.handIndex(cid) >= 0 {
		t.Fatal("late joiner dealt into the running hand")
	}
	playToEnd(t, tb)
	tb.Advance(tb.Now())
	if tb.handIndex(cid) < 0 {
		t.Fatal("late joiner should be dealt in next hand")
	}
	if err := tb.RemovePlayer(cid, cid); err != nil {
		t.Fatal(err)
	}
	if p := tb.Player(cid); !p.Left || p.Place != 3 {
		t.Fatalf("left player %+v", p)
	}
}

func TestDeterministicShuffles(t *testing.T) {
	deal := func() string {
		tb, _, ids := newTestTable(t, "Ann", "Ben", "Cat")
		do(t, tb, ids[0], `{"type":"start"}`)
		return CardsString(tb.Hand.Deck)
	}
	if deal() != deal() {
		t.Fatal("same seed should give the same deck")
	}
	// Different seeds differ.
	a := ShuffledDeck(rand.New(rand.NewPCG(1, 1)))
	b := ShuffledDeck(rand.New(rand.NewPCG(1, 2)))
	if CardsString(a) == CardsString(b) {
		t.Fatal("different seeds gave the same deck")
	}
}

// TestRandomTableSessions plays whole games with random actions and checks
// the table invariants: chips conserved, everyone sees only what they may.
func TestRandomTableSessions(t *testing.T) {
	for game := 0; game < 30; game++ {
		tb, clock, ids := newTestTable(t, "Ann", "Ben", "Cat", "Bot Dee", "Bot Eve")
		tb.Timing = Timing{RunoutStepMs: 1000, HandOverMs: 2000, ShowdownMs: 3000, RebuyMs: 5000}
		rng := rand.New(rand.NewPCG(uint64(game), 3))
		do(t, tb, ids[0], fmt.Sprintf(`{"type":"settings","settings":{"rebuys":%d,"targetMinutes":15,"actionSeconds":30}}`, game%3))
		do(t, tb, ids[0], `{"type":"start"}`)
		total := 5 * 1500
		for step := 0; step < 5000 && tb.Status == StatusPlaying; step++ {
			if tb.Phase == PhasePaused {
				// Players who timed out were sat out; bring them back.
				for _, p := range tb.Players {
					if p.SittingOut && p.active() {
						do(t, tb, p.ID, `{"type":"sitIn"}`)
					}
				}
			}
			pend := tb.Waiting()
			if len(pend) > 0 && rng.IntN(10) > 0 {
				w := pend[rng.IntN(len(pend))]
				if w.Kind == "rebuy" {
					if rng.IntN(2) == 0 {
						do(t, tb, w.PlayerID, `{"type":"rebuy"}`)
						total += 1500
					} else {
						do(t, tb, w.PlayerID, `{"type":"declineRebuy"}`)
					}
					continue
				}
				a := randomAction(rng, tb.Hand.Legal())
				js, _ := json.Marshal(Command{Type: string(a.Type), To: a.To})
				do(t, tb, w.PlayerID, string(js))
			} else if next := tb.NextDeadline(); next > 0 {
				clock.now = max(clock.now, next)
				tb.Advance(clock.now)
			}
			sum := 0
			for _, p := range tb.Players {
				if i := tb.handIndex(p.ID); i >= 0 {
					sum += tb.Hand.Stacks[i]
					if !tb.Hand.Done {
						sum += tb.Hand.Contrib[i]
					}
				} else if !p.Left {
					sum += p.Stack
				}
			}
			if sum != total {
				t.Fatalf("game %d step %d: chips %d != %d", game, step, sum, total)
			}
		}
		if tb.Status != StatusFinished {
			t.Fatalf("game %d did not finish (phase %s, hand %d)", game, tb.Phase, tb.HandNumber)
		}
	}
}

// 0.10.2: with no person seated nobody holds HostID, so the table screen sends
// host commands as TableHost: it may change the settings and start a table of
// computers only, and nothing else.
func TestTableHostStartsComputersOnly(t *testing.T) {
	tb, _, _ := newTestTable(t, "Bot A", "Bot B")
	if tb.HostID != "" {
		t.Fatalf("no person seated, so no host; got %q", tb.HostID)
	}
	if err := tb.PerformAction(TableHost, json.RawMessage(`{"type":"fold"}`)); err == nil {
		t.Fatal("the table screen must not bet for anyone")
	}
	if err := tb.PerformAction("nobody", json.RawMessage(`{"type":"start"}`)); err == nil {
		t.Fatal("an unknown requester is still refused")
	}
	raw, err := json.Marshal(tb.Settings)
	if err != nil {
		t.Fatal(err)
	}
	do(t, tb, TableHost, `{"type":"settings","settings":`+string(raw)+`}`)
	do(t, tb, TableHost, `{"type":"start"}`)
	if tb.Status != StatusPlaying {
		t.Fatalf("status %s, want playing", tb.Status)
	}
	// A seated person who is not the host still can't start or change the settings.
	tb2, _, ids := newTestTable(t, "Ana", "Ben")
	if err := tb2.PerformAction(ids[1], json.RawMessage(`{"type":"start"}`)); err == nil {
		t.Fatal("only the host may start")
	}
}
