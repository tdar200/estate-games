package gamerecord

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/tdar200/estate-games/internal/engine"
)

func TestLegacyText(t *testing.T) {
	cases := map[string]string{
		"Ann landed on PAYDAY":                     "Ann landed on GO",
		"Ann landed on Headlines":                  "Ann landed on Chance",
		"Ann landed on Post":                       "Ann landed on Community Chest",
		"Ann landed on Jail":                       "Ann landed on Jail / Just Visiting",
		"Ann landed on Busted":                     "Ann landed on Go to Jail",
		"Ann landed on Reservoir":                  "Ann landed on Water Works",
		"Ann bought Power Station for $150":        "Ann bought Electric Company for $150",
		"Ann paid $200 (Stamp Duty)":               "Ann paid $200 (Income Tax)",
		"Council Tax":                              "Luxury Tax",
		"Reservoir rent":                           "Water Works rent",
		"Ann passed PAYDAY and collected $200":     "Ann passed GO and collected $200",
		"Ann used a Jail pass":                     "Ann used a Get Out of Jail Free card",
		"Ann confirmed the Headlines card":         "Ann confirmed the Chance card",
		"Ann collected the $350 City Park jackpot": "Ann collected the $350 Free Parking jackpot",
		"No Jail pass available":                   "No Get Out of Jail Free card available",
		"Rules updated: loans on, City Park jackpot on, auctions off, starting cash $2000": "Rules updated: loans on, Free Parking jackpot on, auctions off, starting cash $2000",
		// Text that was already old, or never changed, stays as it is.
		"Ann landed on Jail / Just Visiting": "Ann landed on Jail / Just Visiting",
		"Ann landed on Crown Avenue":         "Ann landed on Crown Avenue",
		"Ann rolled 3 + 4 = 7":               "Ann rolled 3 + 4 = 7",
	}
	for _, c := range engine.ChanceCards {
		cases["Ann drew: "+c.Text] = "Ann drew: " + legacyCardTexts[c.ID]
	}
	for _, c := range engine.ChestCards {
		cases[c.Text] = legacyCardTexts[c.ID]
	}
	for now, want := range cases {
		if got := legacyText(now); got != want {
			t.Errorf("legacyText(%q) = %q, want %q", now, got, want)
		}
	}
	if len(legacyCardTexts) != len(engine.ChanceCards)+len(engine.ChestCards) {
		t.Fatalf("%d old card texts for %d cards", len(legacyCardTexts), len(engine.ChanceCards)+len(engine.ChestCards))
	}
}

// oldWording rewrites rec as if it had been recorded before the rename up to
// step k: the initial state and those steps' hashes and errors in the old
// wording. It returns the replayed game after step k-1, in the old wording,
// and the old log lines the steps wrote.
func oldWording(t *testing.T, rec *Record, k int) (*engine.Game, string) {
	t.Helper()
	data, _ := json.Marshal(rec)
	var prefix Record
	if err := json.Unmarshal(data, &prefix); err != nil {
		t.Fatal(err)
	}
	prefix.Steps = prefix.Steps[:k]
	r, err := NewReplayer(&prefix)
	if err != nil {
		t.Fatal(err)
	}
	inOldWords := func(g *engine.Game) *engine.Game {
		if old := legacyGame(g); old != nil {
			return old
		}
		return g
	}
	initial, _ := json.Marshal(inOldWords(r.G))
	rec.Initial = initial
	var lines strings.Builder
	lastID := ""
	for {
		st, err := r.Step()
		if err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		old := inOldWords(r.G)
		rec.Steps[st.N].Hash = StateHash(old)
		rec.Steps[st.N].Error = legacyText(st.Error)
		for i := len(old.Log) - 1; i >= 0 && old.Log[i].ID != lastID; i-- {
			lines.WriteString(old.Log[i].Text + "\n")
		}
		if n := len(old.Log); n > 0 {
			lastID = old.Log[n-1].ID
		}
	}
	return inOldWords(r.G), lines.String()
}

// A game recorded before the rename still replays, and so does one saved
// before it and finished after it (old lines first, then new ones).
func TestRecordFromBeforeTheRenameReplays(t *testing.T) {
	t.Run("all before", func(t *testing.T) {
		lp := newLivePlay(t, []string{"ai", "ai", "ai", "ai"}, "", `{"freeParkingJackpot":true}`)
		lp.play(t, 3000)
		rec := lp.rec
		final, lines := oldWording(t, rec, len(rec.Steps))
		if !strings.Contains(string(rec.Initial), "Free Parking jackpot") {
			t.Fatal("the initial state is not in the old wording")
		}
		g, err := Verify(rec)
		if err != nil {
			t.Fatal(err)
		}
		if StateHash(g) != StateHash(final) {
			t.Fatal("replay ended in a different state")
		}
		for _, words := range []string{"landed on Chance", "landed on Community Chest", "passed GO and collected", "Free Parking jackpot", "(Income Tax)"} {
			if !strings.Contains(lines, words) {
				t.Errorf("the game never logged %q: the test does not cover it", words)
			}
		}
		// A changed hash is still caught.
		rec.Steps[len(rec.Steps)/2].Hash = "0000000000000000"
		var m *Mismatch
		if _, err := Verify(rec); !errors.As(err, &m) || m.Step != len(rec.Steps)/2 {
			t.Fatalf("got %v, want a mismatch at step %d", err, len(rec.Steps)/2)
		}
	})
	t.Run("saved before, finished after", func(t *testing.T) {
		lp := newLivePlay(t, []string{"ai", "ai", "ai", "ai"}, "", "")
		lp.play(t, 250)
		k := len(lp.rec.Steps)
		// The deploy: the saved game (old wording) is loaded by the new engine.
		lp.g, _ = oldWording(t, lp.rec, k)
		lp.play(t, 3000)
		if len(lp.rec.Steps) < k+50 {
			t.Fatalf("only %d steps after the rename", len(lp.rec.Steps)-k)
		}
		g, err := Verify(lp.rec)
		if err != nil {
			t.Fatal(err)
		}
		if StateHash(g) != StateHash(lp.g) {
			t.Fatal("replay ended in a different state")
		}
		Annotate(lp.rec, EndExpired)
		if !lp.rec.Verified {
			t.Fatalf("annotate: %s", lp.rec.VerifyError)
		}
	})
}
