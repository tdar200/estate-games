package closest

import (
	"fmt"
	mrand "math/rand/v2"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// TestEmbeddedPacks checks every pack the binary ships against the §7 rules
// (LoadLibrary names every problem) and that Quick and Full games can be
// dealt from them. With no packs yet it passes: the library is empty and a
// game says so.
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
	lib, err := DefaultLibrary()
	if err != nil {
		for _, problem := range strings.Split(strings.TrimPrefix(err.Error(), "question packs: "), "; ") {
			t.Error(problem)
		}
		t.FailNow()
	}
	if packs == 0 {
		if len(lib.Questions) != 0 {
			t.Fatal("questions without packs")
		}
		t.Log("no question packs yet: nothing to check, and an empty library is allowed")
		return
	}
	topics, years := map[string]int{}, 0
	for _, q := range lib.Questions {
		topics[q.Topic]++
		if q.Format == FormatYear {
			years++
		}
	}
	t.Logf("%d packs: %d questions over %d topics, %d years", packs, len(lib.Questions), len(topics), years)
	for _, length := range []string{LengthQuick, LengthFull} {
		s := DefaultSettings()
		s.Length = length
		qs, _, err := lib.deal(testRNG(1), s, nil)
		if err != nil {
			t.Errorf("dealing a %s game: %v", length, err)
			continue
		}
		if err := checkCaps(qs, s); err != nil {
			t.Errorf("a %s game from the packs: %v", length, err)
		}
	}
}

// testRNG is a seeded random stream like a game's.
func testRNG(seed byte) *mrand.Rand { return mrand.New(mrand.NewChaCha8(*seedOf(seed))) }

// checkCaps checks a deal against §7: the count, the topic and year caps, no
// question twice.
func checkCaps(qs []*Question, s Settings) error {
	if len(qs) != s.QuestionCount() {
		return fmt.Errorf("%d questions, want %d", len(qs), s.QuestionCount())
	}
	topics, years, ids := map[string]int{}, 0, map[string]bool{}
	for _, q := range qs {
		if ids[q.ID] {
			return fmt.Errorf("%s twice", q.ID)
		}
		ids[q.ID] = true
		topics[q.Topic]++
		if topics[q.Topic] > s.topicCap() {
			return fmt.Errorf("%d questions on %s", topics[q.Topic], q.Topic)
		}
		if q.Format == FormatYear {
			years++
		}
	}
	if years > s.yearCap() {
		return fmt.Errorf("%d year questions", years)
	}
	return nil
}

// TestPackRules: the made-up test pack passes; a broken pack is reported
// rule by rule.
func TestPackRules(t *testing.T) {
	lib := testLibrary(t)
	if len(lib.Questions) != 24 {
		t.Fatalf("%d test questions", len(lib.Questions))
	}
	question := func(id, extra string) string {
		base := map[string]string{
			"id": `"` + id + `"`, "topic": `"art"`, "text": `"How many?"`, "answer": `500`, "unit": `"things"`,
			"format": `"plain"`, "min": `10`, "max": `9000`, "spread": `50`, "fact": `"A fact."`,
			"src": `["https://example.org/1","https://example.org/2"]`,
		}
		for _, kv := range strings.Split(extra, ";") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				if v == "-" {
					delete(base, k)
				} else {
					base[k] = v
				}
			}
		}
		var parts []string
		for k, v := range base {
			parts = append(parts, fmt.Sprintf("%q:%s", k, v))
		}
		slices.Sort(parts)
		return "{" + strings.Join(parts, ",") + "}"
	}
	pack := func(id, license, attribution string, questions ...string) string {
		return fmt.Sprintf(`{"id":%q,"title":"T","version":1,"license":%q,"attribution":%q,"questions":[%s]}`,
			id, license, attribution, strings.Join(questions, ","))
	}
	good := pack("a", "own", "", question("a/1", ""), question("a/2", ""), question("a/3", `format="year";answer=1900;min=1000;max=2025;unit=""`))
	broken := pack("b", "CC BY-SA 4.0", "",
		question("b/1", `text="`+strings.Repeat("x", 121)+`?"`),
		question("b/2", `text="No question mark";fact="`+strings.Repeat("y", 141)+`"`),
		question("b/3", `answer=500.5;topic="cooking"`),
		question("b/4", `answer=5;min=0;max=50`),
		question("b/5", `min=600`),
		question("b/6", `max=800`),
		question("b/7", `format="year";answer=1900;min=1000;max=2025;spread=500`),
		question("b/8", `format="year";answer=900;min=100;max=2025`),
		question("b/9", `src=["https://example.org/1"]`),
		question("b/10", `src=["ftp://example.org/1"];defining=true`),
		question("b/11", `unit=-;spread=1;max=99999999`),
		question("b/12", `format="fraction"`),
		question("b/1", ``),
	)
	fsys := fstest.MapFS{
		"a.json":    {Data: []byte(good)},
		"b.json":    {Data: []byte(broken)},
		"c.json":    {Data: []byte(pack("c", "MIT", "", question("a/1", "")))},
		"d.json":    {Data: []byte(`{"id":`)},
		"e.json":    {Data: []byte(pack("e", "own", "", question("e/1", `format="year";answer=1900;min=1000;max=2025;unit=""`)))},
		"f.json":    {Data: []byte(pack("f", "own", ""))},
		"notes.txt": {Data: []byte("ignored")},
	}
	_, err := LoadLibrary(fsys)
	if err == nil {
		t.Fatal("bad packs loaded")
	}
	for _, want := range []string{
		`b.json pack: licence "CC BY-SA 4.0" needs an attribution line`,
		`b.json b/1: text is 122 characters (at most 120)`,
		`b.json b/2: text does not end with ?`,
		`b.json b/2: fact is 141 characters (at most 140)`,
		`b.json b/3: answer 500.5 is not a whole number`,
		`b.json b/3: topic "cooking" is not in the list`,
		`b.json b/4: answer 5 is not between 10 and 999999`,
		`b.json b/5: min 600 < answer 500 < max 9000 does not hold`,
		`b.json b/6: range 10–800 is too narrow for answer 500`,
		`b.json b/7: spread must be 1 to 300 (years) for a year question`,
		`b.json b/7: a year question has no unit`,
		`b.json b/8: year answer 900 is not between 1000 and 2025`,
		`b.json b/9: has 1 sources (at least 2 that agree, or 1 with "defining": true)`,
		`b.json b/10: source "ftp://example.org/1" is not a web address`,
		`b.json b/10: a defining question needs its source`,
		`b.json b/11: unit missing ("" for none)`,
		`b.json b/11: spread must be 5 to 300 (% of the answer) for a plain question`,
		`b.json b/11: max 99999999 is above 9999999`,
		`b.json b/12: format "fraction" is not plain or year`,
		`b.json b/1: id used twice in this pack`,
		`c.json pack: licence "MIT" is not own, CC0 or CC BY-SA 4.0`,
		`c.json: id "a/1" is already used in a.json`,
		`d.json: not valid JSON`,
		`e.json pack: 1 of its 1 questions are years (at most a third)`,
		`f.json pack: has no questions`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing problem %q", want)
		}
	}
	if strings.Contains(err.Error(), "a.json ") || strings.Contains(err.Error(), "notes.txt") {
		t.Errorf("a good pack or a non-pack file was reported: %v", err)
	}
	small, err := LoadLibrary(fstest.MapFS{"a.json": {Data: []byte(good)}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = small.deal(testRNG(1), DefaultSettings(), nil)
	expectErr(t, err, "the question packs have only 3 questions; a game needs 7")
	empty, err := LoadLibrary(fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = empty.deal(testRNG(1), DefaultSettings(), nil)
	expectErr(t, err, "there are no questions to ask yet")
}

// TestDealing: every deal keeps the caps, and credits name only packs the
// game asks from.
func TestDealing(t *testing.T) {
	lib := testLibrary(t)
	sawCredit := false
	for seed := range byte(200) {
		s := DefaultSettings()
		if seed%2 == 1 {
			s.Length = LengthFull
		}
		qs, credits, err := lib.deal(testRNG(seed), s, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkCaps(qs, s); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		fromCC := slices.ContainsFunc(qs, func(q *Question) bool { return strings.HasPrefix(q.ID, "made-up-2/") })
		if fromCC != (len(credits) == 1) || (fromCC && credits[0] != "Made-up test questions 2, CC BY-SA 4.0") {
			t.Fatalf("seed %d: credits %v", seed, credits)
		}
		sawCredit = sawCredit || fromCC
	}
	if !sawCredit {
		t.Fatal("the CC BY-SA pack was never dealt")
	}
}

// TestRematchNoRepeat plays games one after another in a room: no question
// comes back while the packs have fresh ones, then the least recently played
// come back first.
func TestRematchNoRepeat(t *testing.T) {
	lib := testLibrary(t) // 24 questions: three Quick games are fresh, the fourth repeats 4
	var played []string
	var games [][]string
	for n := range 4 {
		c := &clock{now: start}
		g := NewGame("ROOM", Options{Seed: seedOf(byte(40 + n)), Clock: c.read, Library: lib})
		g.SetPlayed(played)
		addComputer(t, g, "Robo", LevelHard)
		mustAct(t, g, TableHost, `{"type":"settings","settings":{"fill":false}}`)
		startGame(t, g)
		runToPhase(t, g, c, PhaseFinished)
		var ids []string
		for _, q := range g.Questions {
			ids = append(ids, q.ID)
		}
		games = append(games, ids)
		played = g.Played()
		g.SetPlayed(nil) // a finished game ignores it
		if len(g.Played()) != len(played) {
			t.Fatal("SetPlayed changed a finished game")
		}
	}
	seen := map[string]int{}
	for n, ids := range games[:3] {
		for _, id := range ids {
			if first, ok := seen[id]; ok {
				t.Fatalf("game %d repeated %s from game %d", n+1, id, first+1)
			}
			seen[id] = n
		}
	}
	repeats := 0
	for _, id := range games[3] {
		if n, ok := seen[id]; ok {
			repeats++
			if n != 0 {
				t.Fatalf("game 4 repeated %s from game %d before the stalest", id, n+1)
			}
		}
	}
	if repeats != 4 {
		t.Fatalf("game 4 repeated %d questions, want 4 (only 3 fresh)", repeats)
	}
	if len(played) != 24 {
		t.Fatalf("the room remembers %d questions", len(played))
	}
}

func TestRemember(t *testing.T) {
	got := remember([]string{"a", "b", "c"}, "b", "d")
	if !slices.Equal(got, []string{"a", "c", "b", "d"}) {
		t.Fatal(got)
	}
}
