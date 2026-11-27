package quiz

import (
	"fmt"
	mrand "math/rand/v2"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmbeddedPacks checks every pack the binary ships against the §7 rules
// (LoadLibrary names every problem) and that a game can be dealt from them.
// The packs belong to their authors; this test only reports.
func TestEmbeddedPacks(t *testing.T) {
	names, err := embedded.ReadDir("packs")
	if err != nil {
		t.Fatal(err)
	}
	packs := 0
	for _, e := range names {
		if strings.HasSuffix(e.Name(), ".json") {
			packs++
		}
	}
	if packs == 0 {
		t.Skip("no question packs yet")
	}
	lib, err := DefaultLibrary()
	if err != nil {
		for _, problem := range strings.Split(strings.TrimPrefix(err.Error(), "question packs: "), "; ") {
			t.Error(problem)
		}
		t.FailNow()
	}
	topics := map[string]int{}
	for _, c := range lib.Categories {
		topics[c.Topic]++
	}
	t.Logf("%d packs: %d categories over %d topics, %d finals", packs, len(lib.Categories), len(topics), len(lib.Finals))
	for _, rounds := range []int{1, 2} {
		if _, err := lib.deal(testRNG(1), rounds, nil); err != nil {
			t.Errorf("dealing %d rounds: %v", rounds, err)
		}
	}
}

// testRNG is a seeded random stream like a game's.
func testRNG(seed byte) *mrand.Rand { return mrand.New(mrand.NewChaCha8(*seedOf(seed))) }

func TestPackRules(t *testing.T) {
	clue := func(tier int, text, answer string, wrong ...string) string {
		w := `"` + strings.Join(wrong, `","`) + `"`
		return fmt.Sprintf(`{"tier":%d,"text":%q,"answer":%q,"wrong":[%s],"src":"https://example.org"}`, tier, text, answer, w)
	}
	good := []string{}
	for tier := 1; tier <= 5; tier++ {
		good = append(good, clue(tier, "Which one?", "A", "B", "C", "D"))
	}
	pack := func(id, license, attribution, title, topic string, clues []string) string {
		return fmt.Sprintf(`{"id":%q,"title":"T","version":1,"license":%q,"attribution":%q,
			"categories":[{"id":%q,"title":%q,"topic":%q,"clues":[%s]}],
			"finals":[{"id":%q,"category":"FINAL","topic":"art","text":"Who?","answer":"A","wrong":["B","C","D"],"src":"https://example.org"}]}`,
			id, license, attribution, id+"/c", title, topic, strings.Join(clues, ","), id+"/f")
	}
	bad := slices.Clone(good)
	bad[1] = clue(1, strings.Repeat("x", 141), "Same", "same", " SAME ", strings.Repeat("y", 33))
	bad = bad[:4]
	fsys := fstest.MapFS{
		"a.json":    {Data: []byte(pack("a", "own", "", "GOOD", "art", good))},
		"b.json":    {Data: []byte(pack("b", "CC BY-SA 4.0", "", "Lower case", "cooking", bad))},
		"c.json":    {Data: []byte(pack("a", "MIT", "", "FAR TOO LONG A TITLE FOR A BOARD", "art", good))},
		"d.json":    {Data: []byte(`{"id":`)},
		"notes.txt": {Data: []byte("ignored")},
	}
	_, err := LoadLibrary(fsys)
	if err == nil {
		t.Fatal("bad packs loaded")
	}
	for _, want := range []string{
		`b.json pack: licence "CC BY-SA 4.0" needs an attribution line`,
		`b.json b/c: title "Lower case" is not upper case`,
		`b.json b/c: topic "cooking" is not in the list`,
		`b.json b/c: has 4 clues, not 5`,
		`b.json b/c tier 1: text is 141 characters (at most 140)`,
		`b.json b/c tier 1: choice "same" appears twice`,
		`b.json b/c tier 1: choice " SAME " appears twice`,
		`b.json b/c tier 1: choice "yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy" is 33 characters (at most 32)`,
		`b.json b/c: has no tier 2 clue (tiers 1 to 5, once each)`,
		`b.json b/c: has no tier 5 clue (tiers 1 to 5, once each)`,
		`c.json pack: licence "MIT" is not own, CC0 or CC BY-SA 4.0`,
		`c.json a/c: title "FAR TOO LONG A TITLE FOR A BOARD" is longer than 24 characters`,
		`c.json: id "a/c" is already used in a.json`,
		`c.json: id "a/f" is already used in a.json`,
		`d.json: not valid JSON`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing problem %q", want)
		}
	}
	if strings.Contains(err.Error(), "a.json ") || strings.Contains(err.Error(), "notes.txt") {
		t.Errorf("a good pack or a non-pack file was reported: %v", err)
	}

	lib, err := LoadLibrary(fstest.MapFS{"a.json": {Data: []byte(pack("a", "own", "", "GOOD", "art", good))}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = lib.deal(testRNG(1), 1, nil)
	expectErr(t, err, "the question packs need categories in at least 6 different topics")
	empty, _ := LoadLibrary(fstest.MapFS{})
	_, err = empty.deal(testRNG(1), 1, nil)
	expectErr(t, err, "the question packs need categories in at least 6 different topics")
}

func TestDealing(t *testing.T) {
	lib := testLibrary(t)
	goldRows := map[int]int{}
	sawCredit := 0
	for seed := range byte(200) {
		d, err := lib.deal(testRNG(seed), 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, b := range d.boards {
			topics := map[string]bool{}
			gold := map[int]bool{}
			for ci, bc := range b.Categories {
				if topics[bc.Topic] || seen[bc.ID] {
					t.Fatalf("seed %d round %d: topic %s or category %s twice", seed, b.Round, bc.Topic, bc.ID)
				}
				topics[bc.Topic], seen[bc.ID] = true, true
				for r, cell := range bc.Cells {
					if cell.Clue.Tier != r+1 || cell.Value != (r+1)*100*b.Round {
						t.Fatalf("row %d holds tier %d worth %d", r, cell.Clue.Tier, cell.Value)
					}
					if cell.Gold {
						gold[ci] = true
						goldRows[r]++
					}
				}
			}
			if len(b.Categories) != 6 || len(gold) != b.Round {
				t.Fatalf("round %d: %d categories, gold squares in %d categories", b.Round, len(b.Categories), len(gold))
			}
		}
		if slices.Contains(d.credits, "Test inventions adapted from an example source (CC BY-SA 4.0, https://creativecommons.org/licenses/by-sa/4.0/), changed") {
			sawCredit++
			found := false
			for _, b := range d.boards {
				for _, bc := range b.Categories {
					found = found || bc.ID == "test-2/inventions"
				}
			}
			if !found {
				t.Fatal("a credit for a pack the game does not use")
			}
		} else if len(d.credits) != 0 {
			t.Fatalf("credits %v", d.credits)
		}
	}
	if goldRows[0]+goldRows[1] != 0 || goldRows[2] == 0 || goldRows[3] == 0 || goldRows[4] == 0 {
		t.Fatalf("gold squares by row %v: want rows 2–4 only", goldRows)
	}
	if sawCredit == 0 {
		t.Fatal("the CC BY-SA category was never dealt")
	}
}

// TestRematchNoRepeat plays games one after another in a room: no category
// or final comes back while the packs have fresh ones, and then the least
// recently played come back first.
func TestRematchNoRepeat(t *testing.T) {
	lib := testLibrary(t) // 14 categories, 3 finals
	var played []string
	var boards [][]string
	var finals []string
	for n := range 4 {
		c := &clock{now: start}
		g := NewGame("ROOM", Options{Seed: seedOf(byte(40 + n)), Clock: c.read, Library: lib})
		g.SetPlayed(played)
		addComputer(t, g, "Robo", LevelHard)
		startGame(t, g)
		runToPhase(t, g, c, PhaseFinished)
		var ids []string
		for _, bc := range g.Boards[0].Categories {
			ids = append(ids, bc.ID)
		}
		boards, finals = append(boards, ids), append(finals, g.FinalClue.ID)
		played = g.Played()
		g.SetPlayed(nil) // a finished game ignores it
		if len(g.Played()) != len(played) {
			t.Fatal("SetPlayed changed a finished game")
		}
	}
	for _, id := range boards[1] {
		if slices.Contains(boards[0], id) {
			t.Fatalf("game 2 repeated %s while fresh categories remained", id)
		}
	}
	// Game 3: only two fresh categories remain; the other four come back
	// from game 1, the least recently played.
	repeats := 0
	for _, id := range boards[2] {
		switch {
		case slices.Contains(boards[1], id):
			t.Fatalf("game 3 repeated %s from game 2 before game 1's", id)
		case slices.Contains(boards[0], id):
			repeats++
		}
	}
	if repeats > 4 {
		t.Fatalf("game 3 repeated %d categories, want at most 4", repeats)
	}
	if finals[0] == finals[1] || finals[1] == finals[2] || finals[0] == finals[2] || finals[3] != finals[0] {
		t.Fatalf("finals %v: want three different, then the stalest again", finals)
	}
	if len(played) != 14+3 {
		t.Fatalf("the room remembers %d ids, want every category and final once", len(played))
	}
}
