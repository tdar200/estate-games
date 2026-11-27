package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestTraceParity replays games recorded from the JS engine by
// scripts/trace.js (testdata/traces/*.json) and checks, after every step,
// that the Go engine returns the same error, consumes the same RNG floats,
// writes the same log texts and reaches the same canonical state snapshot.

const traceDir = "../../testdata/traces"

type traceFile struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Code        string         `json:"code"`
	Clock       int64          `json:"clock"`
	Initial     map[string]any `json:"initial"`
	Steps       []traceStep    `json:"steps"`
	Finished    bool           `json:"finished"`
	StepCount   int            `json:"stepCount"`
}

type traceStep struct {
	Op     string          `json:"op"`
	Error  *string         `json:"error"`
	Floats []float64       `json:"floats"`
	Logs   []string        `json:"logs"`
	Delta  any             `json:"delta"`
	Result json.RawMessage `json:"result"`

	// action
	Actor  *string         `json:"actor"`
	Seat   int             `json:"seat"`
	Action json.RawMessage `json:"action"`
	// addPlayer / toPlayer
	Name   string  `json:"name"`
	Kind   *string `json:"kind"`
	IsHost bool    `json:"isHost"`
	// removePlayer
	Requester *string `json:"requester"`
	Target    string  `json:"target"`
	// toSpectator
	PlayerID    string `json:"playerId"`
	SpectatorID string `json:"spectatorId"`
	// completeOpeningOrder
	Now int64 `json:"now"`
	// publicState
	ViewerID *string     `json:"viewerId"`
	Viewer   traceViewer `json:"viewer"`
	Full     bool        `json:"full"`
	// mutate: direct state edits (paths of object keys / array indexes)
	Set []traceSet `json:"set"`

	// Raw is JSON.parse(JSON.stringify(game)) after the step, recorded at a
	// few distinct state shapes per trace (ids, timestamps and log included).
	Raw json.RawMessage `json:"raw"`
}

type traceSet struct {
	Path  []any           `json:"path"`
	Value json.RawMessage `json:"value"`
}

type traceViewer struct {
	Role        string      `json:"role"`
	Name        string      `json:"name"`
	SpectatorID string      `json:"spectatorId"`
	IsHost      bool        `json:"isHost"`
	Spectators  []Spectator `json:"spectators"`
}

// replayRNG returns the recorded floats in order; requests beyond the
// recording return 0.5 and are counted so the mismatch is reported.
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

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func TestTraceParity(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(traceDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no traces in %s; run node scripts/trace.js", traceDir)
	}
	totalSteps := 0
	for _, file := range files {
		trace := loadTrace(t, file)
		totalSteps += len(trace.Steps)
		t.Run(trace.Name, func(t *testing.T) {
			t.Parallel()
			replayTrace(t, trace, false)
		})
	}
	t.Logf("replayed %d traces, %d steps", len(files), totalSteps)
}

// TestGameJSONMatchesJS checks that json.Marshal(game) is the same document
// as JSON.stringify(game) in JS — every key, nested shape and value,
// including ids, timestamps and the log — at the raw captures recorded by
// scripts/trace.js (mid-game states with auctions, trades, debts of every
// continuation kind, pending cards, bankruptcies, turn summaries...).
func TestGameJSONMatchesJS(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(traceDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	captures := 0
	for _, file := range files {
		trace := loadTrace(t, file)
		count := 0
		for _, step := range trace.Steps {
			if step.Raw != nil {
				count++
			}
		}
		if count == 0 {
			continue
		}
		captures += count
		t.Run(trace.Name, func(t *testing.T) {
			t.Parallel()
			replayTrace(t, trace, true)
		})
	}
	if captures == 0 {
		t.Fatal("no raw game captures found; run node scripts/trace.js")
	}
	t.Logf("compared %d raw game documents", captures)
}

func loadTrace(t *testing.T, file string) *traceFile {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var trace traceFile
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	if trace.StepCount != len(trace.Steps) {
		t.Fatalf("%s: stepCount %d but %d steps", file, trace.StepCount, len(trace.Steps))
	}
	return &trace
}

// replayTrace replays a trace, failing at the first divergence. With
// checkRaw it also compares the full game JSON at raw captures and stops
// after the last one.
func replayTrace(t *testing.T, trace *traceFile, checkRaw bool) {
	lastRaw := -1
	for index, step := range trace.Steps {
		if step.Raw != nil {
			lastRaw = index
		}
	}
	g := CreateGame(trace.Code)
	g.UseGameJSRules() // the recordings follow game.js, including the rules Estate fixed (legacy.go)
	clock := trace.Clock
	g.Clock = func() int64 { return clock }
	g.NewID = SequentialIDs()

	expected := any(trace.Initial)
	got, err := canonicalGame(g)
	if err != nil {
		t.Fatal(err)
	}
	if diffs := diffValues("", expected, got); len(diffs) > 0 {
		t.Fatalf("initial snapshot differs:\n  %s", strings.Join(diffs, "\n  "))
	}

	for index, step := range trace.Steps {
		describe := func() string {
			switch step.Op {
			case "action":
				return fmt.Sprintf("step %d: action %s by %q (seat %d)", index, string(step.Action), deref(step.Actor), step.Seat)
			default:
				data, _ := json.Marshal(step)
				return fmt.Sprintf("step %d: %s %s", index, step.Op, compact(data))
			}
		}
		lastLogID := ""
		if len(g.Log) > 0 {
			lastLogID = g.Log[len(g.Log)-1].ID
		}
		rng := &replayRNG{values: step.Floats}

		result, stepErr := runStep(g, step, rng)

		var problems []string
		gotError := ""
		if stepErr != nil {
			gotError = stepErr.Error()
		}
		if gotError != deref(step.Error) {
			problems = append(problems, fmt.Sprintf("error: want %q, got %q", deref(step.Error), gotError))
		}
		if rng.used != len(step.Floats) {
			problems = append(problems, fmt.Sprintf("rng: JS consumed %d floats, Go requested %d", len(step.Floats), rng.used))
		}
		if step.Result != nil {
			var want, have any
			_ = json.Unmarshal(step.Result, &want)
			if result != nil {
				data, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				_ = json.Unmarshal(data, &have)
			}
			for _, d := range diffValues("result", want, have) {
				problems = append(problems, d)
			}
		}
		gotLogs := newLogTexts(g, lastLogID)
		if !reflect.DeepEqual(gotLogs, nonNil(step.Logs)) {
			problems = append(problems, fmt.Sprintf("logs:\n      want %q\n      got  %q", step.Logs, gotLogs))
		}
		if step.Delta != nil {
			expected = applyPatch(expected, step.Delta)
		}
		snapshot, err := canonicalGame(g)
		if err != nil {
			t.Fatal(err)
		}
		problems = append(problems, diffValues("state", expected, snapshot)...)
		if checkRaw && step.Raw != nil {
			var want any
			if err := json.Unmarshal(step.Raw, &want); err != nil {
				t.Fatal(err)
			}
			have, err := toJSONValue(g)
			if err != nil {
				t.Fatal(err)
			}
			problems = append(problems, diffShapes("shape", want, any(have))...)
			problems = append(problems, diffValues("json", want, any(have))...)
		}
		if len(problems) > 0 {
			if len(problems) > 25 {
				problems = append(problems[:25], fmt.Sprintf("... and %d more", len(problems)-25))
			}
			t.Fatalf("%s\n  %s", describe(), strings.Join(problems, "\n  "))
		}
		if checkRaw && index == lastRaw {
			return
		}
	}
	if trace.Finished != (g.Status == StatusFinished) {
		t.Fatalf("finished: want %v, got status %q", trace.Finished, g.Status)
	}
}

// runStep performs one recorded operation and returns its result value (for
// steps that record one) and error.
func runStep(g *Game, step traceStep, rng RNG) (any, error) {
	switch step.Op {
	case "action":
		var action Action
		if err := json.Unmarshal(step.Action, &action); err != nil {
			return nil, fmt.Errorf("decode action: %w", err)
		}
		return nil, PerformAction(g, deref(step.Actor), action, rng)
	case "addPlayer":
		player, err := AddPlayer(g, step.Name, deref(step.Kind))
		if err != nil {
			return nil, err
		}
		return player.ID, nil
	case "removePlayer":
		return nil, RemovePlayer(g, deref(step.Requester), step.Target)
	case "toSpectator":
		return nil, SwitchPlayerToSpectator(g, step.PlayerID, step.SpectatorID)
	case "toPlayer":
		player, err := SwitchSpectatorToPlayer(g, step.Name, step.IsHost)
		if err != nil {
			return nil, err
		}
		return player.ID, nil
	case "completeOpeningOrder":
		return CompleteOpeningOrder(g, step.Now), nil
	case "mutate":
		return nil, mutateGame(g, step.Set)
	case "publicState":
		viewer := Viewer{
			Role:        step.Viewer.Role,
			Name:        step.Viewer.Name,
			SpectatorID: step.Viewer.SpectatorID,
			IsHost:      step.Viewer.IsHost,
			Spectators:  step.Viewer.Spectators,
		}
		view, err := canonicalPublic(PublicState(g, deref(step.ViewerID), viewer), step.Full)
		return view, err
	}
	return nil, fmt.Errorf("unknown trace op %q", step.Op)
}

// mutateGame applies JSON-path edits by round-tripping the game through its
// JSON form, which also exercises json.Unmarshal of every state type.
func mutateGame(g *Game, sets []traceSet) error {
	data, err := json.Marshal(g)
	if err != nil {
		return err
	}
	root, err := decodeNumbers(data)
	if err != nil {
		return err
	}
	for _, edit := range sets {
		value, err := decodeNumbers(edit.Value)
		if err != nil {
			return err
		}
		target := root
		for depth, key := range edit.Path {
			last := depth == len(edit.Path)-1
			switch container := target.(type) {
			case map[string]any:
				name := fmt.Sprint(key)
				if last {
					container[name] = value
				} else {
					target = container[name]
				}
			case []any:
				position := int(key.(float64))
				if last {
					container[position] = value
				} else {
					target = container[position]
				}
			default:
				return fmt.Errorf("mutate: cannot index %T with %v", target, key)
			}
		}
	}
	data, err = json.Marshal(root)
	if err != nil {
		return err
	}
	fresh := &Game{}
	if err := json.Unmarshal(data, fresh); err != nil {
		return fmt.Errorf("mutate: unmarshal game: %w", err)
	}
	fresh.Clock, fresh.NewID, fresh.legacyRules = g.Clock, g.NewID, g.legacyRules
	*g = *fresh
	return nil
}

func decodeNumbers(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	err := decoder.Decode(&value)
	return value, err
}

// diffShapes compares JSON documents structurally: same key sets and the
// same kind (null, bool, number, string, array, object) at every path.
func diffShapes(path string, want, got any) []string {
	kind := func(value any) string {
		switch value.(type) {
		case nil:
			return "null"
		case bool:
			return "bool"
		case float64:
			return "number"
		case string:
			return "string"
		case []any:
			return "array"
		case map[string]any:
			return "object"
		}
		return fmt.Sprintf("%T", value)
	}
	if kind(want) != kind(got) {
		return []string{fmt.Sprintf("%s: JS has %s, Go has %s", path, kind(want), kind(got))}
	}
	var out []string
	switch w := want.(type) {
	case map[string]any:
		g := got.(map[string]any)
		for key := range w {
			if _, ok := g[key]; !ok {
				out = append(out, fmt.Sprintf("%s.%s: key missing in Go", path, key))
			}
		}
		for key := range g {
			if _, ok := w[key]; !ok {
				out = append(out, fmt.Sprintf("%s.%s: extra key in Go", path, key))
			}
		}
		for key, value := range w {
			if other, ok := g[key]; ok {
				out = append(out, diffShapes(path+"."+key, value, other)...)
			}
		}
	case []any:
		g := got.([]any)
		for i := 0; i < len(w) && i < len(g); i++ {
			out = append(out, diffShapes(fmt.Sprintf("%s[%d]", path, i), w[i], g[i])...)
		}
	}
	sort.Strings(out)
	return out
}

func newLogTexts(g *Game, lastLogID string) []string {
	start := 0
	if lastLogID != "" {
		for index, entry := range g.Log {
			if entry.ID == lastLogID {
				start = index + 1
				break
			}
		}
	}
	texts := []string{}
	for _, entry := range g.Log[start:] {
		texts = append(texts, entry.Text)
	}
	return texts
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// ---- canonical snapshots (mirror canonicalGame/canonicalPublic in trace.js)

var strippedKeys = map[string]bool{"id": true, "at": true, "createdAt": true}

func strip(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = strip(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if !strippedKeys[key] {
				out[key] = strip(item)
			}
		}
		return out
	}
	return value
}

// goOnlyKeys are Estate additions to the game JSON that game.js does not have
// (path of object keys, then the key). They are removed from the Go side
// before it is compared with a JS recording; review_fixes_test.go covers them.
var goOnlyKeys = []struct {
	path []string
	key  string
}{
	{[]string{"openingOrder"}, "rollingIds"}, // who rolls this round (review B3)
	{[]string{"openingOrder"}, "ties"},       // why they roll again (review B3)
}

func toJSONValue(value any) (map[string]any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	for _, extra := range goOnlyKeys {
		object := out
		for _, key := range extra.path {
			object, _ = object[key].(map[string]any)
		}
		if object != nil {
			delete(object, extra.key)
		}
	}
	return out, nil
}

func canonicalGame(g *Game) (map[string]any, error) {
	raw, err := toJSONValue(g)
	if err != nil {
		return nil, err
	}
	players, _ := raw["players"].([]any)
	delete(raw, "players")
	delete(raw, "log")
	out := strip(raw).(map[string]any)
	indexed := make(map[string]any, len(players))
	for index, player := range players {
		indexed[strconv.Itoa(index)] = player
	}
	out["players"] = indexed
	return out, nil
}

func canonicalPublic(view *PublicGameState, full bool) (map[string]any, error) {
	raw, err := toJSONValue(view)
	if err != nil {
		return nil, err
	}
	players := raw["players"]
	delete(raw, "players")
	logTexts := []any{}
	for _, entry := range raw["log"].([]any) {
		logTexts = append(logTexts, entry.(map[string]any)["text"])
	}
	delete(raw, "log")
	if !full {
		delete(raw, "board")
		delete(raw, "groups")
	}
	out := strip(raw).(map[string]any)
	out["players"] = players
	out["logTexts"] = logTexts
	return out, nil
}

// applyPatch mirrors diff() in trace.js: objects merge recursively,
// {"$del": true} removes a key, anything else replaces the value.
func applyPatch(target, patch any) any {
	patchObject, patchIsObject := patch.(map[string]any)
	targetObject, targetIsObject := target.(map[string]any)
	if !patchIsObject || !targetIsObject {
		return patch
	}
	out := make(map[string]any, len(targetObject))
	for key, value := range targetObject {
		out[key] = value
	}
	for key, value := range patchObject {
		if isDeletion(value) {
			delete(out, key)
			continue
		}
		out[key] = applyPatch(out[key], value)
	}
	return out
}

func isDeletion(value any) bool {
	object, ok := value.(map[string]any)
	return ok && len(object) == 1 && object["$del"] == true
}

// diffValues lists field-level differences between want and got.
func diffValues(path string, want, got any) []string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: want %s, got %s", path, show(want), show(got))}
		}
		keys := map[string]bool{}
		for key := range w {
			keys[key] = true
		}
		for key := range g {
			keys[key] = true
		}
		sorted := make([]string, 0, len(keys))
		for key := range keys {
			sorted = append(sorted, key)
		}
		sort.Strings(sorted)
		var out []string
		for _, key := range sorted {
			wv, inWant := w[key]
			gv, inGot := g[key]
			child := path + "." + key
			switch {
			case !inWant:
				out = append(out, fmt.Sprintf("%s: unexpected key, got %s", child, show(gv)))
			case !inGot:
				out = append(out, fmt.Sprintf("%s: missing key, want %s", child, show(wv)))
			default:
				out = append(out, diffValues(child, wv, gv)...)
			}
		}
		return out
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return []string{fmt.Sprintf("%s: want %s, got %s", path, show(want), show(got))}
		}
		var out []string
		for i := range w {
			out = append(out, diffValues(fmt.Sprintf("%s[%d]", path, i), w[i], g[i])...)
		}
		return out
	case float64:
		if g, ok := got.(float64); ok && (g == w || (math.IsNaN(g) && math.IsNaN(w))) {
			return nil
		}
	default:
		if reflect.DeepEqual(want, got) {
			return nil
		}
	}
	return []string{fmt.Sprintf("%s: want %s, got %s", path, show(want), show(got))}
}

func show(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return compact(data)
}

func compact(data []byte) string {
	text := string(data)
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}
