package closest

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// The question packs (docs/CLOSEST-API.md §7) are compiled into the binary
// from packs/*.json. They live here and never under web/, which is served to
// browsers, because a pack holds every answer.
//
//go:embed packs
var embedded embed.FS

// Topics a question may have: the quiz's fifteen.
var Topics = []string{
	"geography", "science", "space", "nature", "history", "people", "sport", "music",
	"screen", "books", "art", "food", "words", "numbers", "inventions",
}

// Question formats.
const (
	FormatPlain = "plain" // thousands separators: 12,500
	FormatYear  = "year"  // no separator: 1969
)

// Licences a pack may have. Anything but our own questions or CC0 needs an
// attribution line, which the game shows in its credits.
var licences = []string{"own", "CC0", "CC BY-SA 4.0"}

func needsAttribution(licence string) bool { return licence != "own" && licence != "CC0" }

// Pack limits (§7). The lengths are what the table screen and a 360 px phone
// can show at their minimum font sizes.
const (
	maxText    = 120
	maxFact    = 140
	maxUnit    = 16
	minAnswer  = 10
	maxAnswer  = 999_999
	maxGuess   = 9_999_999 // the most a question's max may be (7 digits on the pad)
	minYear    = 1000
	maxYear    = 2025
	minSpreadY = 1   // years
	maxSpreadY = 300 // years
	minSpreadP = 5   // % of the answer
	maxSpreadP = 300 // % of the answer
)

// Pack is one question pack file.
type Pack struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Version     int        `json:"version"`
	License     string     `json:"license"`
	Attribution string     `json:"attribution"`
	Questions   []Question `json:"questions"`
}

// Question is one question with its whole-number answer.
type Question struct {
	ID     string `json:"id"`
	Topic  string `json:"topic"`
	Text   string `json:"text"`
	Answer int    `json:"answer"`
	Unit   string `json:"unit"`
	Format string `json:"format"`
	Min    int    `json:"min"`
	Max    int    `json:"max"`
	// Spread is how far a typical guess is off: years for a year question, a
	// percentage of the answer for a plain one. The computers guess with it.
	Spread   float64  `json:"spread"`
	Fact     string   `json:"fact"`
	Src      []string `json:"src"`
	Defining bool     `json:"defining,omitempty"`

	credit string // the pack's attribution ("" for own and CC0 packs)
}

// Library is every question the dealer may use, from all the packs it was
// loaded from, in file order.
type Library struct {
	Questions []*Question
}

var (
	defaultOnce sync.Once
	defaultLib  *Library
	defaultErr  error
)

// DefaultLibrary is the embedded packs, loaded and checked once. An error
// means a pack breaks the §7 rules (the pack test names every problem). With
// no pack files at all it is an empty library: a game then says why it
// cannot start.
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

// rawQuestion reads numbers as written, so a pack with 1911.5 is reported as
// "not a whole number" instead of failing to load.
type rawQuestion struct {
	ID       string   `json:"id"`
	Topic    string   `json:"topic"`
	Text     string   `json:"text"`
	Answer   *float64 `json:"answer"`
	Unit     *string  `json:"unit"`
	Format   string   `json:"format"`
	Min      *float64 `json:"min"`
	Max      *float64 `json:"max"`
	Spread   *float64 `json:"spread"`
	Fact     string   `json:"fact"`
	Src      []string `json:"src"`
	Defining bool     `json:"defining"`
}

type rawPack struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Version     int           `json:"version"`
	License     string        `json:"license"`
	Attribution string        `json:"attribution"`
	Questions   []rawQuestion `json:"questions"`
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
	var problems []string
	where := map[string]string{} // question id → file
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		var raw rawPack
		if err := json.Unmarshal(data, &raw); err != nil {
			problems = append(problems, fmt.Sprintf("%s: not valid JSON: %v", name, err))
			continue
		}
		file := path.Base(name)
		p, bad := raw.check(file)
		problems = append(problems, bad...)
		for _, q := range p.Questions {
			if q.ID == "" {
				continue
			}
			if first, ok := where[q.ID]; ok {
				problems = append(problems, fmt.Sprintf("%s: id %q is already used in %s", file, q.ID, first))
				continue
			}
			where[q.ID] = file
		}
		packs = append(packs, p)
	}
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
		for j := range p.Questions {
			q := &p.Questions[j]
			q.credit = credit
			lib.Questions = append(lib.Questions, q)
		}
	}
	return lib, nil
}

// check lists every §7 rule the pack breaks (ids across packs are checked by
// LoadLibrary) and returns the pack as the game uses it.
func (raw *rawPack) check(file string) (Pack, []string) {
	var out []string
	bad := func(where, format string, args ...any) {
		out = append(out, file+" "+where+": "+fmt.Sprintf(format, args...))
	}
	p := Pack{ID: raw.ID, Title: raw.Title, Version: raw.Version, License: raw.License, Attribution: raw.Attribution}
	if strings.TrimSpace(p.ID) == "" {
		bad("pack", "id missing")
	}
	if !slices.Contains(licences, p.License) {
		bad("pack", "licence %q is not own, CC0 or CC BY-SA 4.0", p.License)
	} else if needsAttribution(p.License) && strings.TrimSpace(p.Attribution) == "" {
		bad("pack", "licence %q needs an attribution line", p.License)
	}
	if len(raw.Questions) == 0 {
		bad("pack", "has no questions")
	}
	years := 0
	seen := map[string]bool{}
	for i, rq := range raw.Questions {
		where := rq.ID
		if strings.TrimSpace(where) == "" {
			where = fmt.Sprintf("question %d", i)
			bad(where, "id missing")
		} else if seen[rq.ID] {
			bad(where, "id used twice in this pack")
		}
		seen[rq.ID] = true
		q, problems := rq.question()
		for _, msg := range problems {
			bad(where, "%s", msg)
		}
		if q.Format == FormatYear {
			years++
		}
		p.Questions = append(p.Questions, q)
	}
	if n := len(raw.Questions); n > 0 && years*3 > n {
		bad("pack", "%d of its %d questions are years (at most a third)", years, n)
	}
	return p, out
}

// whole reads a required whole number.
func whole(name string, v *float64, problems *[]string) int {
	switch {
	case v == nil:
		*problems = append(*problems, name+" missing")
		return 0
	case *v != math.Trunc(*v) || math.IsInf(*v, 0):
		*problems = append(*problems, fmt.Sprintf("%s %v is not a whole number", name, *v))
		return 0
	}
	return int(*v)
}

// question checks one question against §7 and converts it.
func (rq *rawQuestion) question() (Question, []string) {
	var out []string
	q := Question{ID: rq.ID, Topic: rq.Topic, Text: rq.Text, Format: rq.Format, Fact: rq.Fact,
		Src: rq.Src, Defining: rq.Defining}
	if rq.Unit != nil {
		q.Unit = *rq.Unit
	} else {
		out = append(out, `unit missing ("" for none)`)
	}
	if rq.Spread != nil {
		q.Spread = *rq.Spread
	}
	switch text := strings.TrimSpace(q.Text); {
	case text == "":
		out = append(out, "text missing")
	case utf8.RuneCountInString(q.Text) > maxText:
		out = append(out, fmt.Sprintf("text is %d characters (at most %d)", utf8.RuneCountInString(q.Text), maxText))
	case !strings.HasSuffix(text, "?"):
		out = append(out, "text does not end with ?")
	}
	if strings.TrimSpace(q.Fact) == "" {
		out = append(out, "fact missing")
	} else if n := utf8.RuneCountInString(q.Fact); n > maxFact {
		out = append(out, fmt.Sprintf("fact is %d characters (at most %d)", n, maxFact))
	}
	if n := utf8.RuneCountInString(q.Unit); n > maxUnit {
		out = append(out, fmt.Sprintf("unit %q is %d characters (at most %d)", q.Unit, n, maxUnit))
	}
	if !slices.Contains(Topics, q.Topic) {
		out = append(out, fmt.Sprintf("topic %q is not in the list", q.Topic))
	}
	q.Answer = whole("answer", rq.Answer, &out)
	q.Min = whole("min", rq.Min, &out)
	q.Max = whole("max", rq.Max, &out)
	if rq.Answer != nil && (q.Answer < minAnswer || q.Answer > maxAnswer) {
		out = append(out, fmt.Sprintf("answer %d is not between %d and %d", q.Answer, minAnswer, maxAnswer))
	}
	if rq.Min != nil && rq.Max != nil && rq.Answer != nil {
		if !(q.Min < q.Answer && q.Answer < q.Max) {
			out = append(out, fmt.Sprintf("min %d < answer %d < max %d does not hold", q.Min, q.Answer, q.Max))
		}
		if q.Min < 0 {
			out = append(out, fmt.Sprintf("min %d is below 0", q.Min))
		}
		if q.Max > maxGuess {
			out = append(out, fmt.Sprintf("max %d is above %d", q.Max, maxGuess))
		}
		if q.Format == FormatPlain && (q.Max < 2*q.Answer || q.Min*2 > q.Answer) {
			out = append(out, fmt.Sprintf("range %d–%d is too narrow for answer %d (min at most half, max at least double)", q.Min, q.Max, q.Answer))
		}
	}
	switch q.Format {
	case FormatPlain:
		if rq.Spread == nil || q.Spread < minSpreadP || q.Spread > maxSpreadP {
			out = append(out, fmt.Sprintf("spread must be %d to %d (%% of the answer) for a plain question", minSpreadP, maxSpreadP))
		}
	case FormatYear:
		if q.Answer < minYear || q.Answer > maxYear {
			out = append(out, fmt.Sprintf("year answer %d is not between %d and %d", q.Answer, minYear, maxYear))
		}
		if q.Unit != "" {
			out = append(out, "a year question has no unit")
		}
		if rq.Spread == nil || q.Spread < minSpreadY || q.Spread > maxSpreadY {
			out = append(out, fmt.Sprintf("spread must be %d to %d (years) for a year question", minSpreadY, maxSpreadY))
		}
	default:
		out = append(out, fmt.Sprintf("format %q is not plain or year", q.Format))
	}
	sources := 0
	for _, s := range q.Src {
		if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
			sources++
		} else {
			out = append(out, fmt.Sprintf("source %q is not a web address", s))
		}
	}
	switch {
	case q.Defining && sources < 1:
		out = append(out, "a defining question needs its source")
	case !q.Defining && sources < 2:
		out = append(out, fmt.Sprintf("has %d sources (at least 2 that agree, or 1 with \"defining\": true)", sources))
	}
	return q, out
}
