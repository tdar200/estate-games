package gamerecord

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/hardai"
)

// livePlay plays a game the way the server does (random ids, the real clock,
// the default RNG) and records it with this package's capture functions.
type livePlay struct {
	g    *engine.Game
	rec  *Record
	bots map[string]hardai.Bot
	rng  *rand.Rand
}

func newLivePlay(t *testing.T, kinds []string, diceMode string, rules string) *livePlay {
	t.Helper()
	g := engine.CreateGame("TEST1")
	lp := &livePlay{g: g, bots: map[string]hardai.Bot{}, rng: rand.New(rand.NewPCG(1, 2))}
	for i, kind := range kinds {
		p, err := engine.AddPlayer(g, []string{"Ann", "Bob", "Cy", "Dee", "Eve", "Fay"}[i], kind)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			lp.bots[p.ID] = hardai.NewBot(hardai.Easy, 7, hardai.Options{})
		} else {
			lp.bots[p.ID] = hardai.NewHeuristicBot(false)
		}
	}
	if g.HostID == "" {
		g.HostID = g.Players[0].ID
	}
	if diceMode != "" {
		if err := engine.PerformAction(g, g.HostID, mustAction(t, `{"type":"set_dice_mode","mode":"`+diceMode+`"}`), nil); err != nil {
			t.Fatal(err)
		}
	}
	if rules != "" {
		if err := engine.PerformAction(g, g.HostID, mustAction(t, `{"type":"set_rules","rules":`+rules+`}`), nil); err != nil {
			t.Fatal(err)
		}
	}
	initial, _ := json.Marshal(g)
	lp.rec = &Record{Format: Format, FormatVersion: FormatVersion, Header: Header{ID: "test", Code: g.Code, GameType: "estate"}, Initial: initial}
	lp.apply(t, g.HostID, `{"type":"start"}`, SourcePlayer)
	return lp
}

func mustAction(t *testing.T, raw string) engine.Action {
	t.Helper()
	a, err := DecodeAction(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (lp *livePlay) prevHash() string {
	if n := len(lp.rec.Steps); n > 0 {
		return lp.rec.Steps[n-1].Hash
	}
	return StateHash(lp.g)
}

func (lp *livePlay) push(st Step) {
	st.N = len(lp.rec.Steps)
	lp.rec.Steps = append(lp.rec.Steps, st)
}

func (lp *livePlay) apply(t *testing.T, playerID, raw, source string) error {
	t.Helper()
	st, record, err := PerformAction(lp.g, playerID, json.RawMessage(raw), lp.prevHash())
	if record {
		st.Source = source
		lp.push(st)
	}
	return err
}

// step plays one move; false when nobody can move.
func (lp *livePlay) step(t *testing.T) bool {
	t.Helper()
	g := lp.g
	if g.Status != engine.StatusPlaying {
		return false
	}
	if g.Phase == engine.PhaseOrderCountdown {
		now := g.Now()
		if g.OpeningOrder.CountdownEndsAt != nil {
			now = max(now, *g.OpeningOrder.CountdownEndsAt)
		}
		st, ok := CompleteOpeningOrder(g, now)
		if !ok {
			t.Fatal("countdown did not complete")
		}
		st.Source = SourceServer
		lp.push(st)
		return true
	}
	id, orderRoll := hardai.NextActor(g)
	if id == "" {
		return false
	}
	if orderRoll {
		if err := lp.apply(t, id, `{"type":"roll_for_order"}`, SourceAI); err != nil {
			t.Fatal(err)
		}
		return true
	}
	action, ok := lp.bots[id].Choose(g, id)
	if !ok {
		action, ok = hardai.NewHeuristicBot(false).Choose(g, id)
	}
	if !ok {
		return false
	}
	raw, _ := json.Marshal(action)
	p := g.PlayerByID(id)
	if action.Type == engine.ActionRoll && p.Kind == engine.KindHuman && g.Settings.DiceMode == engine.DiceModeChoice {
		raw, _ = json.Marshal(map[string]any{"type": "roll", "dice": []int{1 + lp.rng.IntN(6), 1 + lp.rng.IntN(6)}, "junk": "secret-token"})
	}
	if err := lp.apply(t, id, string(raw), SourceAI); err != nil {
		fallback, ok := hardai.NewHeuristicBot(false).Choose(g, id)
		if !ok {
			t.Fatalf("no fallback after %s: %v", raw, err)
		}
		raw, _ = json.Marshal(fallback)
		if err := lp.apply(t, id, string(raw), SourceAI); err != nil {
			t.Fatalf("fallback %s refused: %v", raw, err)
		}
	}
	return true
}

func (lp *livePlay) play(t *testing.T, maxSteps int) {
	for i := 0; i < maxSteps && lp.step(t); i++ {
	}
}

func TestRecordedGameReplays(t *testing.T) {
	cases := []struct {
		name, dice, rules string
		kinds             []string
	}{
		{"classic", "", "", []string{"ai", "ai", "ai", "ai"}},
		{"choice dice and loans", "choice", `{"loans":true,"freeParkingJackpot":true}`, []string{"human", "ai", "human", "ai", "ai"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lp := newLivePlay(t, c.kinds, c.dice, c.rules)
			lp.play(t, 6000)
			rec := lp.rec
			if len(rec.Steps) < 200 {
				t.Fatalf("only %d steps", len(rec.Steps))
			}
			g, err := Verify(rec)
			if err != nil {
				t.Fatal(err)
			}
			if StateHash(g) != StateHash(lp.g) {
				t.Fatal("replay ended in a different state")
			}
			for _, st := range rec.Steps {
				if strings.Contains(string(st.Action), "junk") {
					t.Fatalf("unknown key kept: %s", st.Action)
				}
			}
			Annotate(rec, EndExpired)
			if !rec.Verified {
				t.Fatalf("annotate: %s", rec.VerifyError)
			}
			if len(rec.TurnOrder) != len(c.kinds) || rec.Turns < 10 || len(rec.Auctions)+len(rec.Trades) == 0 {
				t.Fatalf("summaries: order %v turns %d auctions %d trades %d", rec.TurnOrder, rec.Turns, len(rec.Auctions), len(rec.Trades))
			}
			if len(rec.Outcome.Standings) != len(c.kinds) || rec.Outcome.Standings[0].Place != 1 {
				t.Fatalf("standings %+v", rec.Outcome.Standings)
			}
			if lp.g.Status == engine.StatusFinished && rec.Outcome.WinnerID != lp.g.WinnerID {
				t.Fatalf("winner %q, game %q", rec.Outcome.WinnerID, lp.g.WinnerID)
			}
		})
	}
}

// Any change to a recorded step is caught at that step.
func TestReplayFailsAtFirstMismatch(t *testing.T) {
	lp := newLivePlay(t, []string{"ai", "ai", "ai"}, "", "")
	lp.play(t, 400)
	firstRoll := -1
	for i, st := range lp.rec.Steps {
		if st.ActionType() == engine.ActionRoll && len(st.RNG) == 2 {
			firstRoll = i
			break
		}
	}
	if firstRoll < 0 {
		t.Fatal("no roll")
	}
	tamper := map[string]func(st *Step){
		"dice": func(st *Step) {
			// Another die face, with the dice field kept consistent.
			st.RNG[0] += 1.0 / 6
			if st.RNG[0] >= 1 {
				st.RNG[0] -= 1
			}
			st.Dice = dicePairs(st.RNG)
		},
		"hash":    func(st *Step) { st.Hash = "0000000000000000" },
		"actor":   func(st *Step) { st.By = "nobody" },
		"ids":     func(st *Step) { st.IDs = st.IDs[1:] },
		"version": func(st *Step) { st.VersionAfter++ },
	}
	for name, change := range tamper {
		t.Run(name, func(t *testing.T) {
			data, _ := json.Marshal(lp.rec)
			var rec Record
			if err := json.Unmarshal(data, &rec); err != nil {
				t.Fatal(err)
			}
			change(&rec.Steps[firstRoll])
			_, err := Verify(&rec)
			var m *Mismatch
			if !errors.As(err, &m) || m.Step != firstRoll {
				t.Fatalf("got %v, want a mismatch at step %d", err, firstRoll)
			}
		})
	}
}

func TestCleanAction(t *testing.T) {
	got := string(CleanAction(json.RawMessage(`{"type":"offer_trade","targetId":"p_1","token":"abc","offer":{"cash":"10","properties":[1],"deviceId":"x"},"request":7}`)))
	want := `{"offer":{"cash":"10","properties":[1]},"request":null,"targetId":"p_1","type":"offer_trade"}`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if got := string(CleanAction(nil)); got != "null" {
		t.Fatalf("empty action: %s", got)
	}
}

// A journal survives a damaged last line, round-trips, and truncates.
func TestJournal(t *testing.T) {
	lp := newLivePlay(t, []string{"ai", "ai"}, "", "")
	lp.play(t, 120)
	path := filepath.Join(t.TempDir(), "g.jsonl")
	var buf []byte
	buf = append(buf, HeaderLine(lp.rec)...)
	for i := range lp.rec.Steps {
		if i == 10 {
			buf = append(buf, StandInLine(lp.g.Players[0].ID, true, 10, 5)...)
		}
		if i == 20 {
			buf = append(buf, StandInLine(lp.g.Players[0].ID, false, 20, 6)...)
		}
		buf = append(buf, StepLine(&lp.rec.Steps[i])...)
	}
	buf = append(buf, `{"type":"step","step":{"n":`...) // cut off by a crash
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := ReadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if j.Damaged != 1 || len(j.Record.Steps) != len(lp.rec.Steps) || len(j.StandIn) != 2 {
		t.Fatalf("damaged %d steps %d standins %d", j.Damaged, len(j.Record.Steps), len(j.StandIn))
	}
	j.Truncate(15)
	if err := os.WriteFile(path, j.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	j2, err := ReadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := j2.Finish()
	if len(rec.Steps) != 15 || len(rec.StandIns) != 1 || rec.StandIns[0].FromStep != 10 || rec.StandIns[0].ToStep != -1 {
		t.Fatalf("after truncate: %d steps, stand-ins %+v", len(rec.Steps), rec.StandIns)
	}
	if _, err := Verify(rec); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "g.json.gz")
	Annotate(rec, EndExpired)
	if err := Write(out, rec); err != nil {
		t.Fatal(err)
	}
	back, err := Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Verified || len(back.Steps) != 15 || back.Outcome.Reason != EndExpired {
		t.Fatalf("read back: verified %v steps %d outcome %+v", back.Verified, len(back.Steps), back.Outcome)
	}
}

func BenchmarkStateHash(b *testing.B) {
	t := &testing.T{}
	lp := newLivePlay(t, []string{"ai", "ai", "ai", "ai", "ai"}, "", "")
	for i := 0; i < 1500 && lp.step(t); i++ {
	}
	data, _ := json.Marshal(lp.g)
	b.Logf("state %d bytes, %d steps", len(data), len(lp.rec.Steps))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		StateHash(lp.g)
	}
}

// The hash ignores the version (the server bumps it for presence changes)
// and nothing else, whatever the players are called.
func TestStateHashIgnoresOnlyVersion(t *testing.T) {
	g := engine.CreateGame("HASH1")
	if _, err := engine.AddPlayer(g, `x","version":7,"{\`, engine.KindHuman); err != nil {
		t.Fatal(err)
	}
	a := StateHash(g)
	g.Version += 41
	if StateHash(g) != a {
		t.Fatal("hash depends on the version")
	}
	g.Players[0].Cash++
	if StateHash(g) == a {
		t.Fatal("hash ignores cash")
	}
	data, _ := json.Marshal(g)
	if start, end := topLevelValue(data, "version"); start < 0 || string(data[start:end]) != fmt.Sprint(g.Version) {
		t.Fatalf("version found at %d:%d: %s", start, end, data[start:end])
	}
}
