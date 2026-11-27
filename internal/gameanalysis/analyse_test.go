package gameanalysis

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
	"github.com/tdar200/estate-games/internal/hardai"
)

// shortGame records a game of a person (played by the heuristic) and two
// computers, ended by the host after maxTurns turns.
func shortGame(t *testing.T, maxTurns int) *gamerecord.Record {
	t.Helper()
	g := engine.CreateGame("SHORT")
	bots := map[string]hardai.Bot{}
	var players []gamerecord.Player
	for i, seat := range []struct{ name, kind, level string }{{"Robin", engine.KindHuman, ""}, {"Alpha", engine.KindAI, "easy"}, {"Blue", engine.KindAI, "normal"}} {
		p, err := engine.AddPlayer(g, seat.name, seat.kind)
		if err != nil {
			t.Fatal(err)
		}
		bots[p.ID] = hardai.NewHeuristicBot(false)
		if seat.level == "easy" {
			bots[p.ID] = hardai.NewBot(hardai.Easy, 3, hardai.Options{})
		}
		players = append(players, gamerecord.Player{ID: p.ID, Seat: i, Name: p.Name, Kind: p.Kind, Difficulty: seat.level})
	}
	rec, err := gamerecord.NewRecorder(g, gamerecord.Header{ID: "short", Players: players})
	if err != nil {
		t.Fatal(err)
	}
	if err := rec.Action(g.HostID, json.RawMessage(`{"type":"start"}`), gamerecord.SourcePlayer); err != nil {
		t.Fatal(err)
	}
	for steps := 0; g.Status == engine.StatusPlaying && steps < 20000; steps++ {
		if gamerecord.TurnNumber(g) > maxTurns {
			if err := rec.End(""); err != nil {
				t.Fatal(err)
			}
			break
		}
		if g.Phase == engine.PhaseOrderCountdown {
			rec.Order(g.Now())
			continue
		}
		id, orderRoll := hardai.NextActor(g)
		if orderRoll {
			if err := rec.Do(id, engine.Action{Type: engine.ActionRollForOrder}, gamerecord.SourceAI); err != nil {
				t.Fatal(err)
			}
			continue
		}
		source := gamerecord.SourceAI
		if g.PlayerByID(id).Kind == engine.KindHuman {
			source = gamerecord.SourcePlayer
		}
		action, ok := bots[id].Choose(g, id)
		if !ok || rec.Do(id, action, source) != nil {
			action, _ = hardai.NewHeuristicBot(false).Choose(g, id)
			if err := rec.Do(id, action, source); err != nil {
				t.Fatalf("%s: %v", action, err)
			}
		}
	}
	reason := gamerecord.EndFinished
	if n := len(rec.Rec.Steps); rec.Rec.Steps[n-1].Kind == gamerecord.StepEnd {
		reason = gamerecord.EndEnded
	}
	out := rec.Finish(reason)
	if !out.Verified {
		t.Fatalf("record does not replay: %s", out.VerifyError)
	}
	return out
}

func TestAnalyseShortGame(t *testing.T) {
	rec := shortGame(t, 25)
	a, err := Analyse(rec, Options{Rollouts: 4, Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Players) != 3 || len(a.Decisions) < 20 || len(a.Curve) < 20 || a.Turns < 20 {
		t.Fatalf("players %d decisions %d curve %d turns %d", len(a.Players), len(a.Decisions), len(a.Curve), a.Turns)
	}
	if a.WinnerID == "" || a.Reason != gamerecord.EndEnded {
		t.Fatalf("winner %q reason %q", a.WinnerID, a.Reason)
	}
	// The curve: probabilities that add up, from even chances to the winner.
	for _, pt := range a.Curve {
		sum := 0.0
		for _, p := range pt.P {
			if p < 0 || p > 1 {
				t.Fatalf("probability %v at step %d", p, pt.Step)
			}
			sum += p
		}
		if math.Abs(sum-1) > 0.01 {
			t.Fatalf("probabilities sum to %v at step %d", sum, pt.Step)
		}
	}
	last := a.Curve[len(a.Curve)-1]
	if last.Step != len(rec.Steps) || last.P[a.index(a.WinnerID)] != 1 {
		t.Fatalf("curve ends at step %d with %v", last.Step, last.P)
	}
	kinds := map[string]string{}
	for _, p := range rec.Players {
		kinds[p.ID] = p.Kind
	}
	for _, d := range a.Decisions {
		who := a.index(d.PlayerID)
		if len(d.Before) != 3 || len(d.After) != 3 || math.Abs(d.After[who]-d.Before[who]-d.Delta) > 0.001 || d.Text == "" {
			t.Fatalf("decision %+v", d)
		}
	}
	for _, h := range a.Blunders {
		if kinds[h.PlayerID] != engine.KindAI || h.Delta >= 0 {
			t.Fatalf("blunder %+v", h)
		}
	}
	for _, h := range a.Best {
		if kinds[h.PlayerID] != engine.KindHuman || h.Delta <= 0 {
			t.Fatalf("best move %+v", h)
		}
	}
	md := a.Markdown(rec)
	for _, want := range []string{"# How the game was won: SHORT", "## Players", "## Win probability over the game", "## Turning points", "Robin (person)", "## How this was worked out"} {
		if !strings.Contains(md, want) {
			t.Fatalf("report lacks %q:\n%s", want, md)
		}
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var back Analysis
	if err := json.Unmarshal(data, &back); err != nil || len(back.Decisions) != len(a.Decisions) {
		t.Fatalf("JSON round trip: %v", err)
	}
}

// The value network alone is the fast path, and a record that does not
// replay is refused.
func TestAnalyseNetOnlyAndBrokenRecord(t *testing.T) {
	rec := shortGame(t, 12)
	a, err := Analyse(rec, Options{NewOracle: func() (Oracle, error) { return NewNetOracle(nil) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Decisions) == 0 || !strings.Contains(a.Oracle, "value network") {
		t.Fatalf("net-only analysis: %d decisions, oracle %q", len(a.Decisions), a.Oracle)
	}
	rec.Steps[len(rec.Steps)/2].Hash = "0123456789abcdef"
	if _, err := Analyse(rec, Options{NewOracle: func() (Oracle, error) { return NewNetOracle(nil) }}); err == nil || !strings.Contains(err.Error(), "does not replay") {
		t.Fatalf("broken record: %v", err)
	}
}
