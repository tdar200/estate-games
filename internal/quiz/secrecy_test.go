package quiz

import (
	"encoding/json"
	"fmt"
	mrand "math/rand/v2"
	"slices"

	"strings"
	"testing"
	"unicode"
)

// TestSecrecy plays whole games of people (a random driver) and computers
// and checks, after every step, the state each viewer gets: the table, a
// spectator and every player (§3: never the right answer before the reveal,
// unfound gold squares, unpicked clues, the final clue before its reading,
// another player's choice or final bet). It also checks that the events
// carry nothing secret and that the scores add up at the end.
func TestSecrecy(t *testing.T) {
	games := 16
	if testing.Short() {
		games = 4
	}
	for n := range games {
		t.Run(fmt.Sprint("game ", n), func(t *testing.T) {
			t.Parallel()
			secretGame(t, n)
		})
	}
}

// secretGame plays game n of TestSecrecy: three people and two computers,
// Full games with fast reading and 6 s answers every other game.
func secretGame(t *testing.T, n int) {
	r := mrand.New(mrand.NewPCG(uint64(n), 99))
	c := &clock{now: start}
	g := NewGame("SECRET", Options{Seed: seedOf(byte(n)), Clock: c.read, Library: testLibrary(t)})
	var people []string
	for _, name := range []string{"Ann", "Ben", "Cat"} {
		people = append(people, addHuman(t, g, name))
	}
	addComputer(t, g, "Robo", Levels[n%3])
	addComputer(t, g, "Hal", LevelHard)
	if n%2 == 1 {
		mustAct(t, g, people[0], `{"type":"settings","settings":{"length":"full","readSpeed":"fast","answerSeconds":6}}`)
	}
	mustAct(t, g, people[0], `{"type":"start"}`)
	d := &driver{g: g, r: r, people: people}
	want := map[string]int{}
	var seq int64
	logSeq := 0
	for steps := 0; g.Status == StatusPlaying; steps++ {
		if steps > 50000 {
			t.Fatalf("stuck in phase %s", g.Phase)
		}
		d.step(c)
		checkViews(t, g)
		logSeq = checkLog(t, g, logSeq)
		for _, e := range g.EventsSince(seq) {
			seq = e.Seq
			checkEvent(t, e)
			switch e.Type {
			case EvRight:
				want[e.PlayerID] += e.Value
			case EvWrong:
				want[e.PlayerID] -= e.Value
			case EvFinalReveal:
				if *e.Correct {
					want[e.PlayerID] += *e.Bet
				} else {
					want[e.PlayerID] -= *e.Bet
				}
			}
		}
	}
	if err := checkFinished(g, want); err != nil {
		t.Fatal(err)
	}
	checkViews(t, g)
}

// driver plays the people at random: picks, gold bets, answers (right about
// half the time, sometimes none) and final bets, each at a random moment
// before its timer.
type driver struct {
	g      *Game
	r      *mrand.Rand
	people []string
	plans  []plan
	seen   map[string]bool
}

type plan struct {
	at     int64
	ctx    string
	player string
	action string
}

// context names the decision the current phase asks for ("" for none); a
// plan made in one context is dropped when the game moves to another.
func (d *driver) context() string {
	g := d.g
	switch g.Phase {
	case PhasePick, PhaseBet:
		return fmt.Sprintf("%s/%d", g.Phase, g.ClueSeq)
	case PhaseRead, PhaseAnswer:
		return fmt.Sprintf("answer/%d", g.ClueSeq)
	case PhaseFinalBet:
		return "finalBet"
	case PhaseFinalRead, PhaseFinalAnswer:
		return "finalAnswer"
	}
	return ""
}

func (d *driver) step(c *clock) {
	g, ctx := d.g, d.context()
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	if ctx != "" && !d.seen[ctx] {
		d.seen[ctx] = true
		d.plan(ctx, c.now)
	}
	d.plans = slices.DeleteFunc(d.plans, func(p plan) bool { return p.ctx != ctx })
	slices.SortFunc(d.plans, func(a, b plan) int { return int(a.at - b.at) })
	if next := g.NextDeadline(); len(d.plans) > 0 && d.plans[0].at <= next {
		p := d.plans[0]
		d.plans = d.plans[1:]
		c.now = max(c.now, p.at)
		_ = act(g, p.player, "%s", p.action) // a plan overtaken by events is refused, as on a phone
		return
	}
	c.now = max(c.now, g.NextDeadline())
	g.Advance(c.now)
}

func (d *driver) plan(ctx string, now int64) {
	g, r := d.g, d.r
	add := func(at int64, player, format string, args ...any) {
		d.plans = append(d.plans, plan{at: at, ctx: ctx, player: player, action: fmt.Sprintf(format, args...)})
	}
	within := func(from, span int64) int64 { return from + 1 + r.Int64N(span) }
	switch g.Phase {
	case PhasePick:
		if p := g.Player(g.ChooserID); p.computer() || r.IntN(8) == 0 {
			return // a computer, or a person who lets the timer pick
		}
		var cells [][2]int
		for ci, bc := range g.board().Categories {
			for ri, cell := range bc.Cells {
				if cell.open() {
					cells = append(cells, [2]int{ci, ri})
				}
			}
		}
		x := cells[r.IntN(len(cells))]
		add(within(now, pickMs-500), g.ChooserID, `{"type":"pick","category":%d,"row":%d}`, x[0], x[1])
	case PhaseBet:
		if p := g.Player(g.Bet.PlayerID); !p.computer() && r.IntN(6) != 0 {
			add(within(now, betMs-500), p.ID, `{"type":"bet","amount":%d}`, g.Bet.Min+r.IntN(g.Bet.Max-g.Bet.Min+1))
		}
	case PhaseRead:
		q := g.Clue.Q
		for _, id := range d.people {
			if slices.Contains(g.Clue.Eligible, id) && r.IntN(6) != 0 {
				d.answer(add, id, q, within(q.OpenAt, q.ClosesAt-q.OpenAt))
			}
		}
	case PhaseFinalBet:
		for _, id := range d.people {
			if r.IntN(8) != 0 {
				add(within(now, finalBetMs-500), id, `{"type":"bet","amount":%d}`, r.IntN(finalMaxBet(g.Player(id))+1))
			}
		}
	case PhaseFinalRead:
		q := g.Final.Q
		for _, id := range d.people {
			if r.IntN(8) != 0 {
				d.answer(add, id, q, within(q.OpenAt, finalAnswerMs))
			}
		}
	}
}

func (d *driver) answer(add func(int64, string, string, ...any), id string, q *Question, at int64) {
	choice := q.Answer
	if d.r.IntN(2) == 0 {
		choice = d.r.IntN(4)
	}
	seq := d.g.ClueSeq
	add(at, id, `{"type":"answer","clue":%d,"choice":%d,"ms":%d}`, seq, choice, max(0, at-q.OpenAt-125))
}

// keysAllowedAt says where a key that can carry a secret may appear in a
// view (path segments joined by "/", list indexes as "#").
var keysAllowedAt = map[string][]string{
	"choice":      {"you/choice", "clue/results/#/choice", "final/results/#/choice"},
	"finalChoice": {"you/finalChoice"},
	"bet":         {"bet", "you/bet", "final/results/#/bet"},
	"amount":      {"bet/amount"},
	"answerIndex": {"clue/answerIndex", "final/answerIndex"},
	"clue":        {"clue"},
}

// keysNeverInView are snapshot fields with secrets (answers, plans, taps,
// the dealt boards, bets) that no view may contain anywhere.
var keysNeverInView = []string{"answer", "taps", "plan", "decideAt", "finder", "boards", "finalClue", "rng",
	"bets", "strong", "weak", "reported", "events", "question"}

func checkViews(t *testing.T, g *Game) {
	t.Helper()
	viewers := []struct {
		id string
		v  Viewer
	}{{"", Viewer{Role: "table"}}, {"", Viewer{Role: "spectator", Name: "Sam", SpectatorID: "s1"}}}
	for _, p := range g.Players {
		viewers = append(viewers, struct {
			id string
			v  Viewer
		}{p.ID, Viewer{Role: "player", Name: p.Name}})
	}
	for _, vw := range viewers {
		data, err := g.PublicState(vw.id, vw.v)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkView(g, vw.id, data); err != nil {
			t.Fatalf("phase %s, viewer %q (%s): %v\n%s", g.Phase, vw.id, vw.v.Role, err, data)
		}
	}
}

// checkView checks one viewer's state against the game it came from.
func checkView(g *Game, viewerID string, data []byte) error {
	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		return err
	}
	strs := map[string]bool{}
	if err := walkKeys(tree, "", strs); err != nil {
		return err
	}
	shown := func(text string) bool { return strs[text] }
	// Clue texts: only the clue being read or answered (its text is part of
	// the reading), and the final's once it is read.
	for _, b := range g.Boards {
		for ci, bc := range b.Categories {
			for _, cell := range bc.Cells {
				current := g.Clue != nil && g.Clue.Q != nil && b == g.board() && g.Clue.Category == ci && g.Clue.Row == cell.Row
				if !current && shown(cell.Clue.Text) {
					return fmt.Errorf("the clue %q is visible", cell.Clue.Text)
				}
			}
		}
	}
	if g.FinalClue != nil && (g.Final == nil || g.Final.Q == nil) && shown(g.FinalClue.Text) {
		return fmt.Errorf("the final clue is visible before its reading")
	}
	var v View
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Board != nil {
		for _, cv := range v.Board.Categories {
			for _, cell := range cv.Cells {
				if cell.Gold && !cell.Played {
					return fmt.Errorf("an unfound gold square is visible")
				}
			}
		}
	}
	if cl := g.Clue; cl != nil && !cl.Decided && v.Clue != nil &&
		(v.Clue.AnswerIndex != nil || v.Clue.Results != nil || v.Clue.WinnerID != nil) {
		return fmt.Errorf("the clue's answer or results before the reveal")
	}
	if f := g.Final; f != nil && v.Final != nil {
		if f.Order == nil && (v.Final.AnswerIndex != nil || v.Final.Results != nil || v.Final.Order != nil) {
			return fmt.Errorf("the final's answer or results before the reveal")
		}
		if len(v.Final.Results) != f.Revealed {
			return fmt.Errorf("%d final results shown, %d revealed", len(v.Final.Results), f.Revealed)
		}
	}
	if v.You != nil {
		if v.You.ID != viewerID {
			return fmt.Errorf("you is %q", v.You.ID)
		}
		own := -1
		if g.Final != nil {
			if amount, ok := g.Final.betOf(viewerID); ok {
				own = amount
			}
		} else if g.Bet != nil && g.Bet.Placed && g.Bet.PlayerID == viewerID {
			own = g.Bet.Amount
		}
		if (v.You.Bet == nil) != (own < 0) || (v.You.Bet != nil && *v.You.Bet != own) {
			return fmt.Errorf("you.bet %v, own bet %d", v.You.Bet, own)
		}
	} else if viewerID != "" {
		return fmt.Errorf("no you for a player")
	}
	return nil
}

// walkKeys checks every key of a view against keysAllowedAt and
// keysNeverInView, and collects every string value into strs.
func walkKeys(node any, path string, strs map[string]bool) error {
	switch n := node.(type) {
	case string:
		strs[n] = true
	case map[string]any:
		for k, child := range n {
			p := k
			if path != "" {
				p = path + "/" + k
			}
			if slices.Contains(keysNeverInView, k) {
				return fmt.Errorf("secret field %s", p)
			}
			if allowed, ok := keysAllowedAt[k]; ok && !slices.Contains(allowed, p) {
				return fmt.Errorf("field %s outside its place", p)
			}
			if err := walkKeys(child, p, strs); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range n {
			if err := walkKeys(child, path+"/#", strs); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkEvent: events go to every client, so none may carry a choice, a
// right/wrong verdict or a final bet before its reveal.
func checkEvent(t *testing.T, e Event) {
	t.Helper()
	switch e.Type {
	case EvTap, EvFinalBet, EvOpen, EvPick, EvGold, EvIntro:
		if e.Correct != nil || e.Bet != nil || e.Amount != 0 || e.Taps != nil || e.Tap != nil {
			t.Fatalf("event %+v carries a secret", e)
		}
	}
}

// checkLog checks the log lines added since logSeq (the log goes to every
// viewer): no clue text, and no answer before its reveal. It returns the
// last line's number.
func checkLog(t *testing.T, g *Game, logSeq int) int {
	t.Helper()
	var secrets []string
	for _, b := range g.Boards {
		for _, bc := range b.Categories {
			for _, cell := range bc.Cells {
				secrets = append(secrets, cell.Clue.Text)
			}
		}
	}
	if g.FinalClue != nil {
		secrets = append(secrets, g.FinalClue.Text)
	}
	// The answer before the reveal (a number-only answer like "6" would
	// match the points in every line, so those are left out).
	answer := func(q *Question) {
		if a := q.Choices[q.Answer]; strings.ContainsFunc(a, unicode.IsLetter) {
			secrets = append(secrets, a)
		}
	}
	if g.Clue != nil && g.Clue.Q != nil && !g.Clue.Decided {
		answer(g.Clue.Q)
	}
	if g.Final != nil && g.Final.Q != nil && g.Final.Order == nil {
		answer(g.Final.Q)
	}
	for _, e := range g.Log[max(0, len(g.Log)-(g.LogSeq-logSeq)):] {
		for _, s := range secrets {
			if strings.Contains(e.Text, s) {
				t.Fatalf("phase %s: the log line %q gives away %q", g.Phase, e.Text, s)
			}
		}
	}
	return g.LogSeq
}
