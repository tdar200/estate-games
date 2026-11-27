package quiz

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// The question packs (docs/QUIZ-API.md §7) are compiled into the binary from
// packs/*.json. They live here and never under web/, which is served to
// browsers, because a pack holds every answer.
//
//go:embed packs
var embedded embed.FS

// Topics a category or final may have. A board's six categories all have
// different topics, so a board is never three geography categories.
var Topics = []string{
	"geography", "science", "space", "nature", "history", "people", "sport", "music",
	"screen", "books", "art", "food", "words", "numbers", "inventions",
}

// Licences a pack may have. Anything but our own questions or CC0 needs an
// attribution line, which the game shows in its credits.
var licences = []string{"own", "CC0", "CC BY-SA 4.0"}

func needsAttribution(licence string) bool { return licence != "own" && licence != "CC0" }

// Pack limits (§7). The lengths are what the table screen and a 360 px phone
// can show at their minimum font sizes.
const (
	maxClueText      = 140
	maxChoice        = 32
	maxTitle         = 24
	cluesPerCategory = 5
	wrongChoices     = 3
)

// Pack is one question pack file.
type Pack struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Version     int        `json:"version"`
	License     string     `json:"license"`
	Attribution string     `json:"attribution"`
	Categories  []Category `json:"categories"`
	Finals      []Final    `json:"finals"`
}

// Category is a column of the board: five clues on one theme, tier 1
// (easiest, the cheapest row) to tier 5.
type Category struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Topic string `json:"topic"`
	Clues []Clue `json:"clues"`

	credit string // the pack's attribution ("" for own and CC0 packs)
}

// Clue is one question with its right answer and three wrong choices.
type Clue struct {
	Tier   int      `json:"tier"`
	Text   string   `json:"text"`
	Answer string   `json:"answer"`
	Wrong  []string `json:"wrong"`
	Src    string   `json:"src"` // where the fact was checked
}

// Final is a final-round question: a category title shown before the bets,
// then the clue.
type Final struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Topic    string   `json:"topic"`
	Text     string   `json:"text"`
	Answer   string   `json:"answer"`
	Wrong    []string `json:"wrong"`
	Src      string   `json:"src"`

	credit string
}

// Library is every category and final the dealer may use, from all the packs
// it was loaded from, in file order.
type Library struct {
	Categories []*Category
	Finals     []*Final
}

var (
	defaultOnce sync.Once
	defaultLib  *Library
	defaultErr  error
)

// DefaultLibrary is the embedded packs, loaded and checked once. An error
// means a pack breaks the §7 rules (the pack test names every problem).
func DefaultLibrary() (*Library, error) {
	defaultOnce.Do(func() {
		sub, err := fs.Sub(embedded, "packs")
		if err != nil {
			defaultErr = err
			return
		}
		defaultLib, defaultErr = LoadLibrary(sub)
	})
	return defaultLib, defaultErr
}

// LoadLibrary reads every *.json pack at the root of fsys and checks them
// together (ids must be unique across packs). The error lists every rule
// broken, so a pack author can fix them all in one go.
func LoadLibrary(fsys fs.FS) (*Library, error) {
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	var packs []Pack
	var files, problems []string
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		var p Pack
		if err := json.Unmarshal(data, &p); err != nil {
			problems = append(problems, fmt.Sprintf("%s: not valid JSON: %v", name, err))
			continue
		}
		problems = append(problems, p.check(path.Base(name))...)
		packs, files = append(packs, p), append(files, name)
	}
	problems = append(problems, duplicateIDs(files, packs)...)
	if len(problems) > 0 {
		return nil, errors.New("question packs: " + strings.Join(problems, "; "))
	}
	lib := &Library{}
	for i := range packs {
		p := &packs[i]
		credit := ""
		if needsAttribution(p.License) {
			credit = p.Attribution
		}
		for j := range p.Categories {
			c := &p.Categories[j]
			c.credit = credit
			slices.SortFunc(c.Clues, func(a, b Clue) int { return a.Tier - b.Tier })
			lib.Categories = append(lib.Categories, c)
		}
		for j := range p.Finals {
			f := &p.Finals[j]
			f.credit = credit
			lib.Finals = append(lib.Finals, f)
		}
	}
	return lib, nil
}

// check lists every §7 rule the pack breaks (ids across packs are checked by
// duplicateIDs).
func (p *Pack) check(file string) []string {
	var out []string
	bad := func(where, format string, args ...any) {
		out = append(out, file+" "+where+": "+fmt.Sprintf(format, args...))
	}
	if strings.TrimSpace(p.ID) == "" {
		bad("pack", "id missing")
	}
	if !slices.Contains(licences, p.License) {
		bad("pack", "licence %q is not own, CC0 or CC BY-SA 4.0", p.License)
	} else if needsAttribution(p.License) && strings.TrimSpace(p.Attribution) == "" {
		bad("pack", "licence %q needs an attribution line", p.License)
	}
	for i, c := range p.Categories {
		where := c.ID
		if where == "" {
			where = fmt.Sprintf("category %d", i)
		}
		if strings.TrimSpace(c.ID) == "" {
			bad(where, "id missing")
		}
		if msg := titleProblem(c.Title); msg != "" {
			bad(where, "%s", msg)
		}
		if !slices.Contains(Topics, c.Topic) {
			bad(where, "topic %q is not in the list", c.Topic)
		}
		if len(c.Clues) != cluesPerCategory {
			bad(where, "has %d clues, not 5", len(c.Clues))
		}
		tiers := map[int]bool{}
		for _, clue := range c.Clues {
			tiers[clue.Tier] = true
			for _, msg := range questionProblems(clue.Text, clue.Answer, clue.Wrong, clue.Src) {
				bad(fmt.Sprintf("%s tier %d", where, clue.Tier), "%s", msg)
			}
		}
		for t := 1; t <= cluesPerCategory; t++ {
			if !tiers[t] {
				bad(where, "has no tier %d clue (tiers 1 to 5, once each)", t)
			}
		}
	}
	for i, f := range p.Finals {
		where := f.ID
		if where == "" {
			where = fmt.Sprintf("final %d", i)
		}
		if strings.TrimSpace(f.ID) == "" {
			bad(where, "id missing")
		}
		if msg := titleProblem(f.Category); msg != "" {
			bad(where, "category %s", msg)
		}
		if !slices.Contains(Topics, f.Topic) {
			bad(where, "topic %q is not in the list", f.Topic)
		}
		for _, msg := range questionProblems(f.Text, f.Answer, f.Wrong, f.Src) {
			bad(where, "%s", msg)
		}
	}
	return out
}

// titleProblem checks a category title: at most 24 characters, upper case.
func titleProblem(title string) string {
	switch {
	case strings.TrimSpace(title) == "":
		return "title missing"
	case utf8.RuneCountInString(title) > maxTitle:
		return fmt.Sprintf("title %q is longer than %d characters", title, maxTitle)
	case title != strings.ToUpper(title):
		return fmt.Sprintf("title %q is not upper case", title)
	}
	return ""
}

// questionProblems checks a clue's text, its four choices and its source.
func questionProblems(text, answer string, wrong []string, src string) []string {
	var out []string
	if strings.TrimSpace(text) == "" {
		out = append(out, "text missing")
	} else if n := utf8.RuneCountInString(text); n > maxClueText {
		out = append(out, fmt.Sprintf("text is %d characters (at most %d)", n, maxClueText))
	}
	if len(wrong) != wrongChoices {
		out = append(out, fmt.Sprintf("has %d wrong choices, not 3", len(wrong)))
	}
	seen := map[string]bool{}
	for _, choice := range append([]string{answer}, wrong...) {
		if strings.TrimSpace(choice) == "" {
			out = append(out, "a choice is empty")
			continue
		}
		if n := utf8.RuneCountInString(choice); n > maxChoice {
			out = append(out, fmt.Sprintf("choice %q is %d characters (at most %d)", choice, n, maxChoice))
		}
		key := choiceKey(choice)
		if seen[key] {
			out = append(out, fmt.Sprintf("choice %q appears twice", choice))
		}
		seen[key] = true
	}
	if strings.TrimSpace(src) == "" {
		out = append(out, "src missing (where the fact was checked)")
	}
	return out
}

// choiceKey compares choices ignoring case and spaces, so "New York" and
// "new york" or "NewYork" count as the same choice.
func choiceKey(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), "")
}

// duplicateIDs reports category and final ids used more than once across all
// packs (the room's memory of played categories is by id).
func duplicateIDs(files []string, packs []Pack) []string {
	var out []string
	where := map[string]string{}
	claim := func(id, file string) {
		if id == "" {
			return
		}
		if first, ok := where[id]; ok {
			out = append(out, fmt.Sprintf("%s: id %q is already used in %s", file, id, first))
			return
		}
		where[id] = file
	}
	for i, p := range packs {
		file := files[i]
		for _, c := range p.Categories {
			claim(c.ID, file)
		}
		for _, f := range p.Finals {
			claim(f.ID, file)
		}
	}
	return out
}
