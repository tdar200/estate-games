package gamerecord

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/tdar200/estate-games/internal/engine"
)

// playToCounter plays until a person can offer a computer a deal, has them
// offer it and the computer counter it with a reason, as Hard and up do.
func playToCounter(t *testing.T, reason string) *livePlay {
	t.Helper()
	lp := newLivePlay(t, []string{"human", "ai"}, "", "")
	g := lp.g
	var human, bot *engine.Player
	for _, p := range g.Players {
		if p.Kind == engine.KindAI {
			bot = p
		} else {
			human = p
		}
	}
	for i := 0; i < 3000 && lp.step(t); i++ {
		cur := engine.CurrentPlayer(g)
		if g.Trade != nil || cur == nil || cur.ID != human.ID {
			continue
		}
		if g.Phase != engine.PhaseAwaitingRoll && g.Phase != engine.PhaseTurnComplete {
			continue
		}
		// The counter asks for $300, which the engine refuses if the person can't pay it
		// (the dice are random, so some games get here poorer).
		if human.Cash < 300 {
			continue
		}
		for space, ps := range g.Properties {
			if ps.OwnerID != bot.ID {
				continue
			}
			offer := fmt.Sprintf(`{"type":"offer_trade","targetId":%q,"offer":{"cash":1,"properties":[]},"request":{"cash":0,"properties":[%d]}}`, bot.ID, space)
			if lp.apply(t, human.ID, offer, SourcePlayer) != nil {
				continue
			}
			counter := fmt.Sprintf(`{"type":"counter_trade","reason":%q,"offer":{"cash":0,"properties":[%d]},"request":{"cash":300,"properties":[]}}`, reason, space)
			if err := lp.apply(t, bot.ID, counter, SourceAI); err != nil {
				t.Fatalf("counter refused: %v", err)
			}
			return lp
		}
	}
	t.Fatal("never reached a counter-offer")
	return nil
}

// The engine logs a computer's reason, so a record that dropped it stopped
// replaying at the counter-offer (0.10.1 to 0.11.1).
func TestCounterWithReasonReplays(t *testing.T) {
	lp := playToCounter(t, "I'd do it for $300.")
	if _, err := Verify(lp.rec); err != nil {
		t.Fatal(err)
	}
}

// Every key of engine.Action must survive CleanAction: a missing one is only
// noticed when a game that uses it fails to replay.
func TestActionKeysCoverEngineAction(t *testing.T) {
	action := reflect.TypeOf(engine.Action{})
	for i := range action.NumField() {
		field := action.Field(i)
		if !field.IsExported() {
			continue
		}
		key, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if !actionKeys[key] {
			t.Errorf("engine.Action.%s (%q) is dropped from records: add it to actionKeys", field.Name, key)
		}
	}
}

// A record that lost a reason comes back only with the right text: the
// recorded state hash rejects anything else.
func TestRepairReasons(t *testing.T) {
	const reason = "I'd do it for $300."
	lp := playToCounter(t, reason)
	last := &lp.rec.Steps[len(lp.rec.Steps)-1]
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(last.Action, &fields); err != nil || fields["reason"] == nil {
		t.Fatalf("no reason in %s", last.Action)
	}
	delete(fields, "reason")
	last.Action, _ = json.Marshal(fields)
	if _, err := Verify(lp.rec); err == nil {
		t.Fatal("a record without its reason must not replay")
	}
	wrong := func(*engine.Game, *Step) []string { return []string{"No deal."} }
	if n, err := RepairReasons(lp.rec, wrong); err == nil || n != 0 {
		t.Fatalf("a wrong guess was accepted: %d, %v", n, err)
	}
	if strings.Contains(string(last.Action), "reason") {
		t.Fatalf("a failed repair must leave the step alone: %s", last.Action)
	}
	right := func(*engine.Game, *Step) []string { return []string{"No deal.", reason} }
	n, err := RepairReasons(lp.rec, right)
	if err != nil || n != 1 {
		t.Fatalf("repaired %d, %v", n, err)
	}
	if _, err := Verify(lp.rec); err != nil {
		t.Fatal(err)
	}
}

// A record that stops replaying still reports how the game really ended, not
// where the replay stopped.
func TestUnreplayableRecordKeepsItsRealEnd(t *testing.T) {
	lp := newLivePlay(t, []string{"ai", "ai"}, "", "")
	lp.play(t, 20000)
	if lp.g.WinnerID == "" {
		t.Fatal("the game did not finish")
	}
	final, err := json.Marshal(lp.g)
	if err != nil {
		t.Fatal(err)
	}
	lp.rec.Final = final
	lp.rec.Steps[len(lp.rec.Steps)/2].Hash = "0000000000000000"
	Annotate(lp.rec, EndFinished)
	if lp.rec.Verified {
		t.Fatal("the record must not verify")
	}
	if lp.rec.Outcome.WinnerID != lp.g.WinnerID {
		t.Fatalf("winner %q, want %q", lp.rec.Outcome.WinnerID, lp.g.WinnerID)
	}
	if got := lp.rec.Outcome.Standings[0]; got.PlayerID != lp.g.WinnerID || got.Place != 1 {
		t.Fatalf("first place %+v, winner %q", got, lp.g.WinnerID)
	}
}
