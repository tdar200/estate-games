package quiz

import (
	"errors"
	mrand "math/rand/v2"
	"slices"
)

// Board shape (docs/QUIZ-API.md §0, §7).
const (
	boardCategories = 6
	boardRows       = 5
)

// goldRowWeights are the chances of a gold square sitting in each row: never
// in the two cheapest rows, where finding one would be worth little.
var goldRowWeights = [boardRows]int{0, 0, 2, 3, 3}

// Board is one dealt board. It holds every clue and the gold squares, so it
// travels only in the snapshot; View builds the public board from it.
type Board struct {
	Round      int              `json:"round"`
	Categories []*BoardCategory `json:"categories"`
}

// BoardCategory is a column of a dealt board.
type BoardCategory struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Topic string  `json:"topic"`
	Cells []*Cell `json:"cells"` // row 0 (tier 1, cheapest) first
}

// Cell is one clue on a board.
type Cell struct {
	Row    int  `json:"row"`
	Value  int  `json:"value"`
	Clue   Clue `json:"clue"`
	Gold   bool `json:"gold"`
	Played bool `json:"played"`
}

// open reports whether the cell can still be picked.
func (c *Cell) open() bool { return !c.Played }

// dealt is what a new game plays: a board per round and the final, plus the
// credit lines of the packs they came from.
type dealt struct {
	boards  []*Board
	final   *Final
	credits []string
}

var (
	errFewTopics = errors.New("the question packs need categories in at least 6 different topics")
	errNoFinals  = errors.New("the question packs have no final questions")
)

// deal picks the boards and the final for a game of rounds boards (§7).
// played is the room's memory of category and final ids, least recently
// played first: anything not in it comes first, then the least recently
// played, so a rematch never repeats a category while fresh ones remain.
func (lib *Library) deal(rng *mrand.Rand, rounds int, played []string) (dealt, error) {
	rank := map[string]int{}
	for i, id := range played {
		rank[id] = i
	}
	var d dealt
	used := map[string]bool{}
	for round := 1; round <= rounds; round++ {
		cats, err := lib.pickCategories(rng, used, rank)
		if err != nil {
			return dealt{}, err
		}
		for _, c := range cats {
			used[c.ID] = true
			d.credits = addCredit(d.credits, c.credit)
		}
		d.boards = append(d.boards, newBoard(rng, round, cats))
	}
	if len(lib.Finals) == 0 {
		return dealt{}, errNoFinals
	}
	finals := stalestFirst(rng, lib.Finals, func(f *Final) string { return f.ID }, rank)
	d.final = finals[0]
	d.credits = addCredit(d.credits, d.final.credit)
	return d, nil
}

// pickCategories chooses one board's six categories, each from a different
// topic, stalest first. Categories already on this game's other board are
// used only when the packs have no other way to fill six topics.
func (lib *Library) pickCategories(rng *mrand.Rand, used map[string]bool, rank map[string]int) ([]*Category, error) {
	var fresh, again []*Category
	for _, c := range lib.Categories {
		if used[c.ID] {
			again = append(again, c)
		} else {
			fresh = append(fresh, c)
		}
	}
	id := func(c *Category) string { return c.ID }
	order := append(stalestFirst(rng, fresh, id, rank), stalestFirst(rng, again, id, rank)...)
	var cats []*Category
	topics := map[string]bool{}
	for _, c := range order {
		if len(cats) < boardCategories && !topics[c.Topic] {
			topics[c.Topic] = true
			cats = append(cats, c)
		}
	}
	if len(cats) < boardCategories {
		return nil, errFewTopics
	}
	// The stalest categories were taken first; shuffle so a category's place
	// on the board says nothing about when the room last saw it.
	rng.Shuffle(len(cats), func(i, j int) { cats[i], cats[j] = cats[j], cats[i] })
	return cats, nil
}

// stalestFirst orders items randomly, then puts those the room has never
// played first and the rest from least to most recently played.
func stalestFirst[T any](rng *mrand.Rand, items []T, id func(T) string, rank map[string]int) []T {
	out := slices.Clone(items)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	age := func(item T) int {
		if r, ok := rank[id(item)]; ok {
			return r
		}
		return -1
	}
	slices.SortStableFunc(out, func(a, b T) int { return age(a) - age(b) })
	return out
}

// newBoard lays out a board: values by row and round, and the round's gold
// squares (1 on the first board, 2 in different categories on the second).
func newBoard(rng *mrand.Rand, round int, cats []*Category) *Board {
	b := &Board{Round: round}
	for _, c := range cats {
		bc := &BoardCategory{ID: c.ID, Title: c.Title, Topic: c.Topic}
		for row, clue := range c.Clues {
			bc.Cells = append(bc.Cells, &Cell{Row: row, Value: cellValue(round, row), Clue: clue})
		}
		b.Categories = append(b.Categories, bc)
	}
	gold := 1
	if round > 1 {
		gold = 2
	}
	for _, ci := range rng.Perm(len(cats))[:gold] {
		b.Categories[ci].Cells[goldRow(rng)].Gold = true
	}
	return b
}

// goldRow draws a gold square's row by goldRowWeights.
func goldRow(rng *mrand.Rand) int {
	total := 0
	for _, w := range goldRowWeights {
		total += w
	}
	n := rng.IntN(total)
	for row, w := range goldRowWeights {
		if n < w {
			return row
		}
		n -= w
	}
	return boardRows - 1
}

// cellValue is a cell's points: 100–500 on the first board, 200–1000 on the
// second.
func cellValue(round, row int) int { return (row + 1) * 100 * round }

// topValue is the dearest cell of a round's board (the floor of a gold
// square's top bet).
func topValue(round int) int { return cellValue(round, boardRows-1) }

// addCredit appends a pack's attribution line once.
func addCredit(credits []string, credit string) []string {
	if credit == "" || slices.Contains(credits, credit) {
		return credits
	}
	return append(credits, credit)
}

// remember records ids as just played: each moves to the end of the room's
// list (most recent last), so the list stays one entry per id.
func remember(played []string, ids ...string) []string {
	out := slices.DeleteFunc(slices.Clone(played), func(id string) bool { return slices.Contains(ids, id) })
	return append(out, ids...)
}
