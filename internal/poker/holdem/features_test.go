package holdem

import (
	"encoding/json"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// The turn timer is on by default (20 s) with a 30 s time bank.
func TestDefaultTurnTimerAndTimeBank(t *testing.T) {
	s := DefaultSettings()
	if s.ActionSeconds != 20 || s.TimeBankSeconds != 30 || s.Style != StyleTournament {
		t.Fatalf("defaults %+v", s)
	}
	tb, clock, ids := newTestTable(t, "Ann", "Ben", "Cat")
	do(t, tb, ids[0], `{"type":"start"}`)
	for _, p := range tb.Players {
		if p.TimeBank != 30 {
			t.Fatalf("time bank at the start %+v", p)
		}
	}
	w := tb.Waiting()[0]
	start := clock.now
	if w.Deadline != start+20_000 || tb.NextDeadline() != w.Deadline {
		t.Fatalf("deadline %d, want %d", w.Deadline, start+20_000)
	}
	actor := tb.Player(w.PlayerID)

	// Nothing happens before the timer runs out.
	clock.now = start + 19_999
	if tb.Advance(clock.now) {
		t.Fatal("changed before the timer ran out")
	}
	// The timer runs out: the bank runs on, the actor still decides.
	clock.now = start + 20_000
	if !tb.Advance(clock.now) {
		t.Fatal("the time bank should start")
	}
	v := tb.View(actor.ID, Viewer{Role: "player"})
	if !v.ActionTimeBank || v.ActionDeadline != start+50_000 || v.ActorID != actor.ID || !v.You.ToAct {
		t.Fatalf("on the bank: timeBank %v deadline %d actor %s", v.ActionTimeBank, v.ActionDeadline, v.ActorID)
	}
	if tb.NextDeadline() != start+50_000 {
		t.Fatalf("next deadline %d", tb.NextDeadline())
	}
	ev := tb.Events[len(tb.Events)-1]
	if ev.Type != EvTimeBank || ev.PlayerID != actor.ID || ev.Amount != 30 {
		t.Fatalf("event %+v", ev)
	}
	// The table screen sees the bank running too, but never anyone's cards.
	tv := tb.View("", Viewer{Role: "table"})
	if !tv.ActionTimeBank || tv.You != nil {
		t.Fatalf("table view %+v", tv.You)
	}

	// Acting 7.2 s into the bank uses 8 s of it (whole seconds, rounded up).
	clock.now = start + 27_200
	do(t, tb, actor.ID, string(tb.SafeCall(actor.ID)))
	if actor.TimeBank != 22 {
		t.Fatalf("bank after use %d, want 22", actor.TimeBank)
	}
	if tb.OnBank {
		t.Fatal("the next player starts on their ordinary timer")
	}
	w2 := tb.Waiting()[0]
	if w2.Deadline != clock.now+20_000 {
		t.Fatalf("next actor deadline %d", w2.Deadline)
	}

	// The next player lets the timer and the whole bank run out: check or
	// fold, bank empty.
	next := tb.Player(w2.PlayerID)
	clock.now += 20_000
	tb.Advance(clock.now)
	clock.now += 30_000
	tb.Advance(clock.now)
	if next.TimeBank != 0 || next.Timeouts != 1 {
		t.Fatalf("after timing out: bank %d timeouts %d", next.TimeBank, next.Timeouts)
	}
	if w := tb.Waiting(); len(w) > 0 && w[0].PlayerID == next.ID {
		t.Fatal("still waiting on the timed-out player")
	}
	// With an empty bank the next timeout comes straight after the timer.
	snap, _ := tb.Snapshot()
	if !strings.Contains(string(snap), `"timeBank":0`) {
		t.Fatal("bank not saved")
	}
}

// The time bank refills by 5 s every 10 hands dealt in, up to the setting,
// and survives a snapshot.
func TestTimeBankRefillAndRestore(t *testing.T) {
	tb, clock, ids := newTestTable(t, "Ann", "Ben")
	do(t, tb, ids[0], `{"type":"settings","settings":{"timeBankSeconds":20,"blindSchedule":"off"}}`)
	do(t, tb, ids[0], `{"type":"start"}`)
	ann := tb.Player(ids[0])
	ann.TimeBank = 3 // used most of it
	for hand := tb.HandNumber; hand < 10; hand = tb.HandNumber {
		playToEnd(t, tb)
		tb.Advance(clock.now)
	}
	// Ten hands dealt in (the first counted at the start): +5 s.
	if ann.TimeBank != 8 {
		t.Fatalf("bank after 10 hands %d, want 8", ann.TimeBank)
	}
	for hand := tb.HandNumber; hand < 40; hand = tb.HandNumber {
		playToEnd(t, tb)
		tb.Advance(clock.now)
	}
	if ann.TimeBank != 20 {
		t.Fatalf("bank is capped at the setting: %d", ann.TimeBank)
	}

	// Mid-bank snapshot and restore: same deadline, same outcome.
	w := tb.Waiting()[0]
	clock.now = w.Deadline
	tb.Advance(clock.now)
	if !tb.OnBank {
		t.Fatal("bank should run")
	}
	snap, err := tb.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	tm := Timing{}
	tb2, err := Restore(snap, Options{Clock: clock.Now, Timing: &tm})
	if err != nil {
		t.Fatal(err)
	}
	if !tb2.OnBank || tb2.Deadline != tb.Deadline || tb2.NextDeadline() != tb.NextDeadline() {
		t.Fatalf("restored bank: on %v deadline %d/%d", tb2.OnBank, tb2.Deadline, tb.Deadline)
	}
	clock.now += 5_500
	for _, x := range []*Table{tb, tb2} {
		do(t, x, w.PlayerID, string(x.SafeCall(w.PlayerID)))
	}
	if a, b := tb.Player(w.PlayerID).TimeBank, tb2.Player(w.PlayerID).TimeBank; a != b || a != 14 {
		t.Fatalf("bank after restore %d / %d, want 14", a, b)
	}
}

// Validation of the new settings.
func TestStyleAndTimerSettings(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann", "Ben")
	for _, bad := range []string{`{"style":"sitngo"}`, `{"timeBankSeconds":121}`, `{"timeBankSeconds":-1}`, `{"actionSeconds":5}`} {
		if err := tb.PerformAction(ids[0], json.RawMessage(`{"type":"settings","settings":`+bad+`}`)); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	do(t, tb, ids[0], `{"type":"settings","settings":{"style":"cash","actionSeconds":0,"timeBankSeconds":0}}`)
	v := tb.View(ids[0], Viewer{Role: "player"})
	if v.Settings.Style != StyleCash || len(v.Schedule) != 1 || v.Schedule[0].Minutes != 0 || v.Level.Next != nil {
		t.Fatalf("cash lobby: style %q schedule %+v", v.Settings.Style, v.Schedule)
	}
	// An old save without a style is a tournament.
	snap, _ := tb.Snapshot()
	old := strings.Replace(string(snap), `"style":"cash",`, ``, 1)
	tb2, err := Restore([]byte(old), Options{})
	if err != nil || tb2.Settings.Style != StyleTournament {
		t.Fatalf("restored style %q (%v)", tb2.Settings.Style, err)
	}
}

// Cash games: fixed blinds, rebuy whenever, no expiry, ranked by chips won.
func TestCashGame(t *testing.T) {
	tb, clock, ids := newTestTable(t, "Ann", "Ben", "Cat")
	tb.Timing.RebuyMs = 10_000
	do(t, tb, ids[0], `{"type":"settings","settings":{"style":"cash","targetMinutes":15,"rebuys":0,"startingStack":1000}}`)
	do(t, tb, ids[0], `{"type":"start"}`)
	if len(tb.Levels) != 1 || tb.LevelEndsAt != 0 {
		t.Fatalf("cash blinds must stay fixed: %+v ends %d", tb.Levels, tb.LevelEndsAt)
	}
	for _, p := range tb.Players {
		if p.BoughtIn != 1000 {
			t.Fatalf("bought in %+v", p)
		}
	}
	// Play all-in hands until somebody busts.
	var bust *Player
	for guard := 0; guard < 60 && bust == nil; guard++ {
		for tb.Phase == PhaseBetting {
			w := tb.Waiting()[0]
			if w.Kind != "act" {
				break
			}
			do(t, tb, w.PlayerID, `{"type":"allin"}`)
		}
		for tb.Phase == PhaseRunout {
			tb.Advance(tb.Now())
		}
		for _, p := range tb.Players {
			if p.Busted {
				bust = p
			}
		}
		tb.Advance(tb.Now())
	}
	if bust == nil {
		t.Fatal("nobody busted")
	}
	if bust.RebuyUntil != 0 {
		t.Fatalf("cash rebuys never expire: %d", bust.RebuyUntil)
	}
	// Hours later the offer is still open; the busted player sits out.
	clock.now += int64(3 * time.Hour / time.Millisecond)
	tb.Advance(clock.now)
	if !bust.Busted || bust.Out || tb.handIndex(bust.ID) >= 0 && !tb.Hand.Done {
		t.Fatalf("busted cash player %+v dealt in or out", bust)
	}
	if tb.Status != StatusPlaying {
		t.Fatal("a cash game only ends when the host ends it")
	}
	if len(tb.Levels) != 1 || tb.Level().BigBlind != 20 {
		t.Fatalf("blinds rose in a cash game: %+v", tb.Level())
	}
	v := tb.View(bust.ID, Viewer{Role: "player"})
	if !v.You.CanRebuy || v.You.RebuyUntil != 0 {
		t.Fatalf("cash rebuy offer %+v", v.You)
	}
	found := false
	for _, w := range tb.Waiting() {
		found = found || (w.PlayerID == bust.ID && w.Kind == "rebuy" && w.Deadline == 0)
	}
	if !found {
		t.Fatalf("waiting %+v", tb.Waiting())
	}
	// Rebuy twice over the game (rebuys:0 doesn't limit cash games).
	do(t, tb, bust.ID, `{"type":"rebuy"}`)
	if bust.Stack != 1000 || bust.BoughtIn != 2000 || bust.Rebuys != 1 {
		t.Fatalf("after rebuy %+v", bust)
	}
	// A late joiner buys in too.
	did, err := tb.AddPlayer("Dee", KindHuman)
	if err != nil {
		t.Fatal(err)
	}
	if tb.Player(did).BoughtIn != 1000 {
		t.Fatal("late joiner's buy-in")
	}
	// The host ends the game: ranked by chips won.
	if _, err := tb.EndGame(); err != nil {
		t.Fatal(err)
	}
	st := tb.View("", Viewer{Role: "table"}).Standings
	for k := 1; k < len(st); k++ {
		if st[k].Net > st[k-1].Net || st[k].Net != st[k].Stack-st[k].BoughtIn {
			t.Fatalf("standings not by chips won: %+v", st)
		}
	}
	total := 0
	for _, s := range st {
		total += s.Net
	}
	if total != 0 {
		t.Fatalf("chips won must sum to zero: %+v", st)
	}
	if st[0].Net > st[1].Net && tb.WinnerID != st[0].ID {
		t.Fatalf("winner %s, standings %+v", tb.WinnerID, st)
	}
}

// Random cash sessions: chips are conserved (every chip bought is on the
// table or in the pot), and End Game's results sum to zero.
func TestRandomCashSessions(t *testing.T) {
	for game := 0; game < 6; game++ {
		tb, clock, ids := newTestTable(t, "Ann", "Ben", "Cat", "Bot Dee")
		tb.Timing = Timing{RunoutStepMs: 1000, HandOverMs: 2000, ShowdownMs: 3000, RebuyMs: 5000}
		rng := rand.New(rand.NewPCG(uint64(game), 7))
		do(t, tb, ids[0], `{"type":"settings","settings":{"style":"cash","startingStack":400}}`)
		do(t, tb, ids[0], `{"type":"start"}`)
		for step := 0; step < 1200; step++ {
			pend := tb.Waiting()
			if len(pend) > 0 && rng.IntN(8) > 0 {
				w := pend[rng.IntN(len(pend))]
				if w.Kind == "rebuy" {
					do(t, tb, w.PlayerID, `{"type":"rebuy"}`)
					continue
				}
				a := randomAction(rng, tb.Hand.Legal())
				js, _ := json.Marshal(Command{Type: string(a.Type), To: a.To})
				do(t, tb, w.PlayerID, string(js))
			} else if next := tb.NextDeadline(); next > 0 {
				clock.now = max(clock.now, next)
				tb.Advance(clock.now)
			} else if tb.Phase == PhasePaused {
				for _, p := range tb.Players {
					if p.SittingOut {
						do(t, tb, p.ID, `{"type":"sitIn"}`)
					}
				}
			}
			if tb.Status != StatusPlaying {
				t.Fatalf("game %d: a cash game ended by itself (%s)", game, tb.Phase)
			}
			if tb.Levels[0].BigBlind != 20 || len(tb.Levels) != 1 {
				t.Fatal("blinds changed")
			}
			sum, bought := 0, 0
			for _, p := range tb.Players {
				bought += p.BoughtIn
				// During a hand the hand holds the chips; afterwards the
				// player's stack does (a rebuy may have topped it up).
				if i := tb.handIndex(p.ID); i >= 0 && !tb.Hand.Done {
					sum += tb.Hand.Stacks[i] + tb.Hand.Contrib[i]
				} else {
					sum += p.Stack
				}
			}
			if sum != bought {
				t.Fatalf("game %d step %d: chips %d, bought %d", game, step, sum, bought)
			}
		}
		if _, err := tb.EndGame(); err != nil {
			t.Fatal(err)
		}
		net := 0
		for _, s := range tb.View("", Viewer{Role: "table"}).Standings {
			net += s.Net
		}
		if net != 0 {
			t.Fatalf("game %d: results sum to %d", game, net)
		}
	}
}

// In a cash game a player who leaves cashes out, and the game ends by itself
// once fewer than two players are left in it.
func TestCashGameEndsWhenAlone(t *testing.T) {
	tb, _, ids := newTestTable(t, "Ann", "Ben")
	do(t, tb, ids[0], `{"type":"settings","settings":{"style":"cash"}}`)
	do(t, tb, ids[0], `{"type":"start"}`)
	w := tb.Waiting()[0]
	do(t, tb, w.PlayerID, `{"type":"raise","to":100}`)
	leaver := w.PlayerID
	if err := tb.RemovePlayer(leaver, leaver); err != nil {
		t.Fatal(err)
	}
	if tb.Status != StatusFinished {
		t.Fatalf("status %s", tb.Status)
	}
	// The unmatched 80 of the raise comes back; the 20 the big blind matched
	// is lost.
	p := tb.Player(leaver)
	if p.Stack != 1480 || p.Net() != -20 {
		t.Fatalf("leaver cashes out what is behind: %+v", p)
	}
	other := ids[0]
	if other == leaver {
		other = ids[1]
	}
	if tb.WinnerID != other || tb.Player(other).Place != 1 || p.Place != 2 {
		t.Fatalf("winner %s places %d/%d", tb.WinnerID, tb.Player(other).Place, p.Place)
	}
}

// you.strength: only in the viewer's own panel, cached per street and
// opponent count, null outside a live hand.
func TestHandStrength(t *testing.T) {
	// Computer seats don't get it (no screen shows it).
	bt, _, bids := newTestTable(t, "Ann", "Bot Bo")
	do(t, bt, bids[0], `{"type":"start"}`)
	if v := bt.View(bids[1], Viewer{Role: "player"}); v.You == nil || v.You.Strength != nil {
		t.Fatalf("computer seat's strength %v", v.You)
	}

	tb, _, ids := newTestTable(t, "Ann", "Ben", "Cat", "Dan")
	do(t, tb, ids[0], `{"type":"start"}`)
	for _, id := range ids {
		v := tb.View(id, Viewer{Role: "player"})
		if v.You.Strength == nil || *v.You.Strength <= 0 || *v.You.Strength >= 1 {
			t.Fatalf("preflop strength %v", v.You.Strength)
		}
		if s := *v.You.Strength; s != float64(int(s*100+0.5))/100 {
			t.Fatalf("strength %v not rounded to 2 decimals", s)
		}
		raw, _ := tb.PublicState(id, Viewer{Role: "player"})
		if n := strings.Count(string(raw), `"strength"`); n != 1 {
			t.Fatalf("strength appears %d times in a player's state", n)
		}
	}
	raw, _ := tb.PublicState("", Viewer{Role: "table"})
	if strings.Contains(string(raw), `"strength"`) {
		t.Fatal("the table screen must not get anyone's strength")
	}
	// Cached: same value while nothing changes; recomputed when a player
	// folds (fewer opponents).
	w := tb.Waiting()[0]
	someone := ids[0]
	if someone == w.PlayerID {
		someone = ids[1]
	}
	before := *tb.View(someone, Viewer{Role: "player"}).You.Strength
	if again := *tb.View(someone, Viewer{Role: "player"}).You.Strength; again != before {
		t.Fatal("cached value changed")
	}
	e := tb.strength[someone]
	do(t, tb, w.PlayerID, `{"type":"fold"}`)
	if v := tb.View(w.PlayerID, Viewer{Role: "player"}); v.You.Strength != nil {
		t.Fatal("a folded player has no strength")
	}
	tb.View(someone, Viewer{Role: "player"})
	if e2 := tb.strength[someone]; e2.opps != e.opps-1 {
		t.Fatalf("opponents %d → %d", e.opps, e2.opps)
	}
	playToEnd(t, tb)
	if v := tb.View(someone, Viewer{Role: "player"}); v.You.Strength != nil {
		t.Fatal("strength after the hand is over")
	}

	// Known cases.
	aces := [2]Card{MustCard("As"), MustCard("Ah")}
	if s := Strength(aces, nil, 1, 1); s < 0.82 || s > 0.88 {
		t.Fatalf("aces vs one random hand preflop: %.3f", s)
	}
	if s := Strength(aces, MustCards("Ad Ac 7h 7s 2d"), 3, 1); s < 0.99 {
		t.Fatalf("four aces on the river: %.3f", s)
	}
	low := [2]Card{MustCard("7c"), MustCard("2d")}
	if s := Strength(low, MustCards("As Kh Qs"), 1, 1); s > 0.3 {
		t.Fatalf("seven-high on A-K-Q: %.3f", s)
	}
	// A draw counts the cards still to come: a flush draw on the flop is
	// worth well above nothing, and more than the same cards that missed on
	// the river.
	draw := [2]Card{MustCard("8h"), MustCard("7h")}
	flop := Strength(draw, MustCards("Ah 2h Kc"), 1, 1)
	missed := Strength(draw, MustCards("Ah 2h Kc 4s 9d"), 1, 1)
	if flop < 0.35 || flop <= missed+0.1 {
		t.Fatalf("flush draw on the flop %.3f, missed on the river %.3f", flop, missed)
	}
	t.Logf("flush draw on the flop: %.2f; missed on the river: %.2f", flop, missed)
	// Cost: a few milliseconds at most against seven opponents.
	startT := time.Now()
	for k := 0; k < 5; k++ {
		Strength(low, MustCards("As Kh Qs 9d"), 7, uint64(k))
		Strength(low, nil, 7, uint64(k))
	}
	if per := time.Since(startT) / 10; per > 15*time.Millisecond {
		t.Fatalf("strength takes %v", per)
	} else {
		t.Logf("strength vs 7 opponents: %v per computation", per)
	}
}
