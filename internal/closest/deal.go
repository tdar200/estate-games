package closest

import (
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"slices"
)

var errNoQuestions = errors.New("there are no questions to ask yet")

// deal chooses a game's questions (§7): Settings.QuestionCount of them, at
// most topicCap from any topic and yearCap year questions, never one the
// room has played while fresh ones remain. played is the room's memory,
// least recently played first; when the fresh questions run low, the least
// recently played come back. The caps give way before a repeat does: a
// third history question is better than one the table remembers.
func (lib *Library) deal(rng *mrand.Rand, s Settings, played []string) ([]*Question, []string, error) {
	n := s.QuestionCount()
	switch {
	case lib == nil || len(lib.Questions) == 0:
		return nil, nil, errNoQuestions
	case len(lib.Questions) < n:
		return nil, nil, fmt.Errorf("the question packs have only %d questions; a game needs %d", len(lib.Questions), n)
	}
	rank := map[string]int{}
	for i, id := range played {
		rank[id] = i
	}
	order := stalestFirst(rng, lib.Questions, func(q *Question) string { return q.ID }, rank)
	var picked []*Question
	topics, years := map[string]int{}, 0
	taken := map[string]bool{}
	take := func(fresh, capped bool) {
		for _, q := range order {
			if len(picked) == n {
				return
			}
			_, old := rank[q.ID]
			if taken[q.ID] || old == fresh {
				continue
			}
			year := q.Format == FormatYear
			if capped && (topics[q.Topic] >= s.topicCap() || (year && years >= s.yearCap())) {
				continue
			}
			taken[q.ID] = true
			picked = append(picked, q)
			topics[q.Topic]++
			if year {
				years++
			}
		}
	}
	take(true, true)
	take(true, false)
	take(false, true)
	take(false, false)
	// The stalest were taken first; shuffle so a question's place says
	// nothing about when the room last saw it.
	rng.Shuffle(len(picked), func(i, j int) { picked[i], picked[j] = picked[j], picked[i] })
	var credits []string
	for _, q := range picked {
		credits = addCredit(credits, q.credit)
	}
	return picked, credits, nil
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
