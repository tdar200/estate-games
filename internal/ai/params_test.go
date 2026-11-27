package ai

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParamsVectorRoundTrip(t *testing.T) {
	if n := len(Knobs); n < 30 || n > 80 {
		t.Fatalf("%d knobs, want 30-80", n)
	}
	seen := map[string]bool{}
	for _, k := range Knobs {
		if seen[k.Name] {
			t.Fatalf("duplicate knob %q", k.Name)
		}
		seen[k.Name] = true
		if k.Default < k.Min || k.Default > k.Max {
			t.Errorf("knob %s: default %v outside [%v, %v]", k.Name, k.Default, k.Min, k.Max)
		}
	}
	p, err := ParamsFromVector(DefaultParams.Vector())
	if err != nil {
		t.Fatal(err)
	}
	if p != DefaultParams {
		t.Fatal("vector round trip changed the defaults")
	}
	data, err := json.Marshal(DefaultParams)
	if err != nil {
		t.Fatal(err)
	}
	var q Params
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatal(err)
	}
	if q != DefaultParams {
		t.Fatal("JSON round trip changed the defaults")
	}
	if err := json.Unmarshal([]byte(`{"noSuchKnob": 1}`), &q); err == nil {
		t.Fatal("unknown knob accepted")
	}
}

// TestParamsDefaultParity replays every choose fixture through
// ChooseActionParams with a copy of the defaults, and the trade search with
// the opponent model forced onto its own context (the path a tuned AI takes),
// and checks both decide exactly like ChooseAction.
func TestParamsDefaultParity(t *testing.T) {
	files := loadFixtureFiles(t)
	copyParams := DefaultParams
	checked, trades := 0, 0
	for _, file := range files {
		if file.Kind != "choose" {
			continue
		}
		for _, raw := range file.Fixtures {
			var f chooseFixture
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			want := ChooseAction(decodeGame(t, f.ID, f.Game), f.PlayerID)
			got := ChooseActionParams(decodeGame(t, f.ID, f.Game), f.PlayerID, &copyParams)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("%s: ChooseActionParams(copy of defaults) = %+v, ChooseAction = %+v", f.ID, got, want)
			}
			checked++
			if f.Phase != "awaiting_roll" && f.Phase != "turn_complete" {
				continue
			}
			g := decodeGame(t, f.ID, f.Game)
			base := newCtx(g)
			player := base.findPlayer(f.PlayerID)
			if player == nil || g.Turn == nil || g.Turn.AITradeAttempted {
				continue
			}
			split := newCtxParams(g, &copyParams)
			split.opp = newCtxParams(g, &DefaultParams)
			if split.opp == split {
				t.Fatal("opponent context not split")
			}
			wantTrade := base.chooseAiTrade(player)
			gotTrade := split.chooseAiTrade(player)
			if !reflect.DeepEqual(wantTrade, gotTrade) {
				t.Fatalf("%s: split opponent model proposes %+v, default %+v", f.ID, gotTrade, wantTrade)
			}
			trades++
		}
	}
	if checked == 0 || trades == 0 {
		t.Fatalf("checked %d decisions and %d trade searches", checked, trades)
	}
	t.Logf("checked %d decisions and %d trade searches", checked, trades)
}

// TestParamsChangeDecisions checks the knobs are wired: shifted knobs change
// some decisions and never panic.
func TestParamsChangeDecisions(t *testing.T) {
	files := loadFixtureFiles(t)
	v := DefaultParams.Vector()
	for i, k := range Knobs {
		v[i] = k.Min + 0.8*(k.Max-k.Min)
	}
	p, err := ParamsFromVector(v)
	if err != nil {
		t.Fatal(err)
	}
	changed, total := 0, 0
	for _, file := range files {
		if file.Kind != "choose" {
			continue
		}
		for _, raw := range file.Fixtures {
			var f chooseFixture
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			want := ChooseAction(decodeGame(t, f.ID, f.Game), f.PlayerID)
			got := ChooseActionParams(decodeGame(t, f.ID, f.Game), f.PlayerID, &p)
			total++
			if !reflect.DeepEqual(want, got) {
				changed++
			}
		}
	}
	if changed == 0 {
		t.Fatalf("no decision of %d changed with shifted knobs", total)
	}
	t.Logf("%d of %d decisions changed", changed, total)
}
