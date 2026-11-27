package ai_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
)

// TestEngineTraceDecisions is an end-to-end check of the server path.
//
// It replays the JS reference games in testdata/traces (ai-*, mixed-*,
// recorded by scripts/trace.js, where every AI move came from chooseAiAction)
// on the Go engine. Before every AI move it builds the AI's view exactly as the
// server will (json.Marshal(*engine.Game) -> json.Unmarshal into ai.Game),
// asks ai.ChooseAction, and checks that the answer equals the recorded JS
// action. It then decodes that action into engine.Action, as the server will.
// This test imports internal/engine; the ai package itself does not.

const traceDir = "../../testdata/traces"

type traceFile struct {
	Name  string      `json:"name"`
	Code  string      `json:"code"`
	Clock int64       `json:"clock"`
	Steps []traceStep `json:"steps"`
}

type traceStep struct {
	Op          string          `json:"op"`
	Error       *string         `json:"error"`
	Floats      []float64       `json:"floats"`
	Actor       *string         `json:"actor"`
	Action      json.RawMessage `json:"action"`
	Name        string          `json:"name"`
	Kind        *string         `json:"kind"`
	PlayerID    string          `json:"playerId"`
	SpectatorID string          `json:"spectatorId"`
	Now         int64           `json:"now"`
	ViewerID    *string         `json:"viewerId"`
}

type replayRNG struct {
	values []float64
	used   int
}

func (r *replayRNG) Float64() float64 {
	r.used++
	if r.used > len(r.values) {
		return 0.5
	}
	return r.values[r.used-1]
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nextActorID mirrors nextActorId in scripts/trace.js (rooms.js nextAiAction
// without the kind filter).
func nextActorID(g *engine.Game) string {
	if g.Status != engine.StatusPlaying {
		return ""
	}
	switch g.Phase {
	case engine.PhaseDeterminingOrder:
		return "" // AIs roll for order without ai.js
	case engine.PhaseTrade:
		if g.Trade == nil {
			return ""
		}
		return g.Trade.TargetID
	case engine.PhaseAuction:
		a := g.Auction
		if a == nil || (len(a.ActiveIDs) == 1 && a.BidderID == a.ActiveIDs[0]) {
			return ""
		}
		for offset := range a.ActiveIDs {
			pos := (a.TurnPos + offset) % len(a.ActiveIDs)
			if a.ActiveIDs[pos] != a.BidderID {
				return a.ActiveIDs[pos]
			}
		}
		return ""
	}
	if p := engine.CurrentPlayer(g); p != nil {
		return p.ID
	}
	return ""
}

// aiView builds the AI snapshot from an engine game: a JSON round trip.
func aiView(t *testing.T, g *engine.Game) *ai.Game {
	t.Helper()
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var view ai.Game
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatal(err)
	}
	return &view
}

func canonicalJSON(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return v
}

func TestEngineTraceDecisions(t *testing.T) {
	var paths []string
	for _, pattern := range []string{"ai-*.json", "mixed-*.json"} {
		matches, err := filepath.Glob(filepath.Join(traceDir, pattern))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		t.Skip("no engine traces in testdata/traces")
	}
	sort.Strings(paths)
	total := map[string]int{}
	results := make(chan map[string]int, len(paths))
	t.Run("traces", func(t *testing.T) {
		for _, path := range paths {
			t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
				t.Parallel()
				results <- replayTraceDecisions(t, path)
			})
		}
	})
	close(results)
	for counts := range results {
		for key, n := range counts {
			total[key] += n
		}
	}
	keys := make([]string, 0, len(total))
	sum := 0
	for key, n := range total {
		keys = append(keys, key)
		sum += n
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&b, " %s=%d", key, total[key])
	}
	t.Logf("%d AI decisions from %d engine traces matched:%s", sum, len(paths), b.String())
}

func replayTraceDecisions(t *testing.T, path string) map[string]int {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tr traceFile
	if err := json.Unmarshal(data, &tr); err != nil {
		t.Fatal(err)
	}
	g := engine.CreateGame(tr.Code)
	clock := tr.Clock
	g.Clock = func() int64 { return clock }
	g.NewID = engine.SequentialIDs()
	// The recordings follow game.js, including the rules the engine has since
	// fixed (internal/engine/legacy.go): replay them with game.js's rules.
	// Checked on 3 Nov 2026: without this, each trace's first difference is
	// one of those fixes (unmortgage rounding 23 traces, nearest-utility
	// re-roll 9, jail card back to the deck 7, jail cards passed on at
	// bankruptcy 3, the 8-seat cap 1); AI decisions differ only after it.
	g.UseGameJSRules()

	counts := map[string]int{}
	failures := 0
	for index, step := range tr.Steps {
		rng := &replayRNG{values: step.Floats}
		var stepErr error
		switch step.Op {
		case "addPlayer":
			_, stepErr = engine.AddPlayer(g, step.Name, deref(step.Kind))
		case "toSpectator":
			stepErr = engine.SwitchPlayerToSpectator(g, step.PlayerID, step.SpectatorID)
		case "completeOpeningOrder":
			engine.CompleteOpeningOrder(g, step.Now)
		case "publicState":
			engine.PublicState(g, deref(step.ViewerID), engine.Viewer{}) // normalises auction turnPos like JS
		case "action":
			if id := nextActorID(g); id != "" {
				if p := g.PlayerByID(id); p != nil && p.Kind == engine.KindAI {
					if deref(step.Actor) != id {
						t.Fatalf("step %d: trace actor %q, expected AI %q to act", index, deref(step.Actor), id)
					}
					got := ai.ChooseAction(aiView(t, g), id)
					gotJSON := []byte("null")
					if got != nil {
						gotJSON, _ = json.Marshal(got)
					}
					if !reflect.DeepEqual(canonicalJSON(t, gotJSON), canonicalJSON(t, step.Action)) {
						failures++
						if failures <= 10 {
							t.Errorf("step %d [phase %s, player %s]:\n   go: %s\n   js: %s", index, g.Phase, id, gotJSON, step.Action)
						}
					}
					kind := "null"
					if got != nil {
						kind = got.Type
					}
					counts[kind]++
				}
			}
			var action engine.Action
			if err := json.Unmarshal(step.Action, &action); err != nil {
				t.Fatalf("step %d: decode action %s: %v", index, step.Action, err)
			}
			stepErr = engine.PerformAction(g, deref(step.Actor), action, rng)
		default:
			t.Skipf("step %d: trace op %q is not supported by this cross-check", index, step.Op)
		}
		gotErr := ""
		if stepErr != nil {
			gotErr = stepErr.Error()
		}
		if gotErr != deref(step.Error) || rng.used != len(step.Floats) {
			t.Fatalf("step %d (%s): engine replay out of sync (error %q, want %q; rng %d/%d)",
				index, step.Op, gotErr, deref(step.Error), rng.used, len(step.Floats))
		}
	}
	if failures > 0 {
		t.Errorf("%d AI decisions mismatched", failures)
	}
	return counts
}
