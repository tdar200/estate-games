package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Parity tests against fixtures recorded from src/ai.js by
// scripts/ai-fixtures.js (testdata/ai/*.json).

const fixtureDir = "../../testdata/ai"

type fixtureFile struct {
	Kind     string            `json:"kind"`
	Count    int               `json:"count"`
	Fixtures []json.RawMessage `json:"fixtures"`
}

type chooseFixture struct {
	ID           string          `json:"id"`
	Source       string          `json:"source"`
	Phase        string          `json:"phase"`
	PlayerID     string          `json:"playerId"`
	Game         json.RawMessage `json:"game"`
	Expected     json.RawMessage `json:"expected"`
	TurnPosAfter *int            `json:"turnPosAfter"`
}

type acceptsFixture struct {
	ID       string          `json:"id"`
	Source   string          `json:"source"`
	PlayerID string          `json:"playerId"`
	Game     json.RawMessage `json:"game"`
	Expected bool            `json:"expected"`
}

type quoteCase struct {
	ProposerID string          `json:"proposerId"`
	Request    json.RawMessage `json:"request"`
	Expected   json.RawMessage `json:"expected"`
	Error      *string         `json:"error"`
}

type quoteFixture struct {
	ID     string          `json:"id"`
	Source string          `json:"source"`
	Game   json.RawMessage `json:"game"`
	Cases  []quoteCase     `json:"cases"`
}

func loadFixtureFiles(t *testing.T) map[string]fixtureFile {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixtures in %s; run `node scripts/ai-fixtures.js`", fixtureDir)
	}
	files := map[string]fixtureFile{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var file fixtureFile
		if err := json.Unmarshal(data, &file); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if file.Count != len(file.Fixtures) {
			t.Fatalf("%s: header says %d fixtures, file has %d", path, file.Count, len(file.Fixtures))
		}
		files[filepath.Base(path)] = file
	}
	return files
}

// canonical decodes JSON into generic values so key order does not matter.
func canonical(t *testing.T, data []byte) any {
	t.Helper()
	var v any
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return v
}

func decodeGame(t *testing.T, id string, raw json.RawMessage) *Game {
	t.Helper()
	var g Game
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("%s: decode game: %v", id, err)
	}
	return &g
}

type tally struct {
	mu     sync.Mutex
	counts map[string]int
}

func (s *tally) add(key string) {
	s.mu.Lock()
	s.counts[key]++
	s.mu.Unlock()
}

func TestFixtureParity(t *testing.T) {
	files := loadFixtureFiles(t)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	stats := &tally{counts: map[string]int{}}
	t.Run("files", func(t *testing.T) {
		for _, name := range names {
			file := files[name]
			t.Run(strings.TrimSuffix(name, ".json"), func(t *testing.T) {
				t.Parallel()
				switch file.Kind {
				case "choose":
					checkChoose(t, file.Fixtures, stats)
				case "accepts":
					checkAccepts(t, file.Fixtures, stats)
				case "quote":
					checkQuotes(t, file.Fixtures, stats)
				default:
					t.Fatalf("unknown fixture kind %q", file.Kind)
				}
			})
		}
	})

	keys := make([]string, 0, len(stats.counts))
	total := 0
	for key, n := range stats.counts {
		keys = append(keys, key)
		total += n
	}
	sort.Strings(keys)
	var summary strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&summary, "\n  %-46s %6d", key, stats.counts[key])
	}
	t.Logf("checked %d fixture decisions:%s", total, summary.String())
}

const maxReported = 25

func checkChoose(t *testing.T, fixtures []json.RawMessage, stats *tally) {
	failures := 0
	for _, raw := range fixtures {
		var f chooseFixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		g := decodeGame(t, f.ID, f.Game)
		got := ChooseAction(g, f.PlayerID)
		gotJSON := []byte("null")
		if got != nil {
			var err error
			if gotJSON, err = json.Marshal(got); err != nil {
				t.Fatal(err)
			}
		}
		want := canonical(t, f.Expected)
		kind := "null"
		if m, ok := want.(map[string]any); ok {
			kind, _ = m["type"].(string)
		}
		stats.add("choose " + f.Source + " " + kind)
		mismatch := !reflect.DeepEqual(canonical(t, gotJSON), want)
		if !mismatch && f.TurnPosAfter != nil && (g.Auction == nil || g.Auction.TurnPos != *f.TurnPosAfter) {
			mismatch = true
			gotJSON = fmt.Appendf(gotJSON, " (turnPos after: %v, want %d)", turnPos(g), *f.TurnPosAfter)
		}
		if mismatch {
			failures++
			if failures <= maxReported {
				t.Errorf("choose %s [%s, %s, player %s]:\n   go: %s\n   js: %s", f.ID, f.Source, f.Phase, f.PlayerID, gotJSON, f.Expected)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d choose fixtures mismatched", failures, len(fixtures))
	}
}

func turnPos(g *Game) any {
	if g.Auction == nil {
		return nil
	}
	return g.Auction.TurnPos
}

func checkAccepts(t *testing.T, fixtures []json.RawMessage, stats *tally) {
	failures := 0
	for _, raw := range fixtures {
		var f acceptsFixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		g := decodeGame(t, f.ID, f.Game)
		got := AcceptsTrade(g, f.PlayerID)
		stats.add(fmt.Sprintf("accepts %s %v", f.Source, f.Expected))
		if got != f.Expected {
			failures++
			if failures <= maxReported {
				t.Errorf("accepts %s [%s, player %s]: go %v, js %v", f.ID, f.Source, f.PlayerID, got, f.Expected)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d accepts fixtures mismatched", failures, len(fixtures))
	}
}

func checkQuotes(t *testing.T, fixtures []json.RawMessage, stats *tally) {
	failures, cases := 0, 0
	for _, raw := range fixtures {
		var f quoteFixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		for i, c := range f.Cases {
			cases++
			g := decodeGame(t, f.ID, f.Game)
			var req QuoteRequest
			if err := json.Unmarshal(c.Request, &req); err != nil {
				t.Fatalf("%s case %d: decode request %s: %v", f.ID, i, c.Request, err)
			}
			quote, err := QuoteTrade(g, c.ProposerID, req)
			var got, want string
			if err != nil {
				got = "error: " + err.Error()
			} else {
				data, _ := json.Marshal(quote)
				got = string(data)
			}
			if c.Error != nil {
				want = "error: " + *c.Error
				stats.add("quote error")
			} else {
				data, _ := json.Marshal(canonical(t, c.Expected))
				want = string(data)
				var q Quote
				_ = json.Unmarshal(c.Expected, &q)
				switch {
				case q.OfferCash > 0:
					stats.add("quote ok offerCash>0")
				case q.RequestCash > 0:
					stats.add("quote ok requestCash>0")
				default:
					stats.add("quote ok no cash")
				}
				if err == nil {
					data, _ := json.Marshal(canonical(t, []byte(got)))
					got = string(data)
				}
			}
			if got != want {
				failures++
				if failures <= maxReported {
					t.Errorf("quote %s case %d [%s, proposer %s, request %s]:\n   go: %s\n   js: %s", f.ID, i, f.Source, c.ProposerID, c.Request, got, want)
				}
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d quote cases mismatched", failures, cases)
	}
}
