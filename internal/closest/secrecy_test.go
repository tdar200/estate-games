package closest

import (
	"encoding/json"
	"fmt"
	mrand "math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// TestSecrecy plays whole games of people (a random driver) and computers
// and checks, after every step, the state each viewer gets (the table, a
// spectator and every player) in every phase: never another player's guess
// before the line-up, another player's chips or stake before the chips are
// shown, the answer, fact or sources before the reveal, or a question not
// yet asked (§3). It also checks that the events and the log give nothing
// away and that the scores add up.
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

// secretGame plays game n of TestSecrecy: three people and two computers
// (every fourth game people only, so questions where nobody guesses or
// everyone guesses the same come up); every other game is Full with the
// short timers; every third game one person leaves partway.
func secretGame(t *testing.T, n int) *Game {
	r := mrand.New(mrand.NewPCG(uint64(n), 7))
	c := &clock{now: start}
	g := NewGame("SECRET", Options{Seed: seedOf(byte(n)), Clock: c.read, Library: testLibrary(t)})
	var people []string
	for _, name := range []string{"Ann", "Ben", "Cat"} {
		people = append(people, addHuman(t, g, name))
	}
	if n%4 != 2 {
		addComputer(t, g, "Robo", Levels[n%3])
		addComputer(t, g, "Hal", LevelHard)
	}
	settings := `{"fill":false}`
	if n%2 == 1 {
		settings = `{"fill":false,"length":"full","guessSeconds":20,"betSeconds":15}`
	}
	mustAct(t, g, people[0], `{"type":"settings","settings":%s}`, settings)
	mustAct(t, g, people[0], `{"type":"start"}`)
	d := &driver{g: g, r: r, people: people, leaver: -1}
	if n%3 == 0 {
		d.leaver, d.leaveAt = 1+r.IntN(2), start+int64(30_000+r.IntN(240_000))
	}
	var seq int64
	logSeq := 0
	revealed := map[int]bool{}
	for steps := 0; g.Status == StatusPlaying; steps++ {
		if steps > 20000 {
			t.Fatalf("stuck: %s", g)
		}
		d.step(c)
		checkViews(t, g)
		logSeq = checkLog(t, g, logSeq)
		for _, e := range g.EventsSince(seq) {
			seq = e.Seq
			checkEvent(t, g, e)
		}
		if cur := g.Cur; cur != nil && cur.Revealed && !revealed[cur.Seq] {
			revealed[cur.Seq] = true
			checkResults(t, g)
		}
	}
	checkViews(t, g)
	if len(revealed) != len(g.Questions) {
		t.Fatalf("%d of %d questions revealed", len(revealed), len(g.Questions))
	}
	if len(g.WinnerIDs) == 0 {
		t.Fatal("nobody won")
	}
	return g
}

// driver plays the people at random: a guess (sometimes none, sometimes
// changed once) and a bet (sometimes none, sometimes a stake) at random
// moments before each timer; away now and then; one may leave.
type driver struct {
	g       *Game
	r       *mrand.Rand
	people  []string
	plans   []plan
	seen    map[string]bool
	leaver  int // index into people, -1 for none
	leaveAt int64
}

type plan struct {
	at     int64
	ctx    string
	player string
	action string
}

func (d *driver) context() string {
	switch d.g.Phase {
	case PhaseGuess, PhaseBet:
		return fmt.Sprintf("%s/%d", d.g.Phase, d.g.QuestionSeq)
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
	slices.SortStableFunc(d.plans, func(a, b plan) int { return int(a.at - b.at) })
	next := g.NextDeadline()
	if d.leaver >= 0 && d.leaveAt <= next {
		c.now = max(c.now, d.leaveAt)
		if err := g.RemovePlayer(d.people[d.leaver], d.people[d.leaver]); err != nil {
			panic(err)
		}
		d.leaver = -1
		return
	}
	if len(d.plans) > 0 && d.plans[0].at <= next {
		p := d.plans[0]
		d.plans = d.plans[1:]
		c.now = max(c.now, p.at)
		_ = act(g, p.player, "%s", p.action) // a plan overtaken by events is refused, as on a phone
		return
	}
	c.now = max(c.now, next)
	g.Advance(c.now)
}

func (d *driver) plan(ctx string, now int64) {
	g, r := d.g, d.r
	cur := g.Cur
	add := func(at int64, player, format string, args ...any) {
		d.plans = append(d.plans, plan{at: at, ctx: ctx, player: player, action: fmt.Sprintf(format, args...)})
	}
	within := func(span int64) int64 { return now + 1 + r.Int64N(span) }
	for _, id := range d.people {
		g.SetOnline(id, r.IntN(5) != 0)
	}
	switch g.Phase {
	case PhaseGuess:
		q := cur.Question
		span := g.PhaseEndsAt - now - 200
		mode, same := r.IntN(6), d.value(q) // 0: nobody guesses; 1: everyone guesses the same
		for _, id := range d.people {
			if mode == 1 {
				add(within(span), id, `{"type":"guess","q":%d,"value":%d}`, cur.Seq, same)
				continue
			}
			if mode == 0 || r.IntN(6) == 0 {
				continue
			}
			add(within(span), id, `{"type":"guess","q":%d,"value":%d}`, cur.Seq, d.value(q))
			if r.IntN(4) == 0 {
				add(within(span), id, `{"type":"guess","q":%d,"value":%d}`, cur.Seq, d.value(q))
			}
		}
	case PhaseBet:
		k := len(cur.Slots)
		for _, id := range d.people {
			if r.IntN(6) == 0 {
				continue
			}
			a, b := r.IntN(k), r.IntN(k)
			stake := r.IntN(stakeMax(g.Player(id)) + 1)
			slot := []int{a, b}[r.IntN(2)]
			if r.IntN(10) == 0 {
				stake, slot = 1, k // refused: no such slot among the chips
			}
			add(within(g.PhaseEndsAt-now-200), id, `{"type":"bet","q":%d,"chips":[%d,%d],"stake":%d,"stakeSlot":%d}`, cur.Seq, a, b, stake, slot)
		}
	}
}

// value is a person's guess: near the answer, now and then exact, now and
// then the same as someone else's.
func (d *driver) value(q *Question) int {
	r := d.r
	switch r.IntN(8) {
	case 0:
		return q.Answer
	case 1:
		if gs := d.g.Cur.Guesses; len(gs) > 0 {
			return gs[r.IntN(len(gs))].Value
		}
	}
	f := 0.5 + r.Float64()*1.5
	return min(max(int(float64(q.Answer)*f), q.Min), q.Max)
}

// keysAllowedAt says where a key that can carry a secret may appear in a
// view (path segments joined by "/", list indexes as "#").
var keysAllowedAt = map[string][]string{
	"guess":     {"you/guess", "question/results/#/guess"},
	"chips":     {"you/chips", "question/slots/#/chips"},
	"stake":     {"you/stake", "question/slots/#/chips/#/stake"},
	"stakeSlot": {"you/stakeSlot"},
	"value":     {"question/slots/#/value"},
	"answer":    {"question/answer"},
	"fact":      {"question/fact"},
	"src":       {"question/src"},
	"results":   {"question/results"},
	"off":       {"question/slots/#/off"},
	"result":    {"question/slots/#/result"},
	"nobody":    {"question/nobody"},
}

// keysNeverInView are snapshot fields with secrets (the dealt questions,
// every guess and bet, computers' plans, the random stream) that no view may
// contain anywhere.
var keysNeverInView = []string{"questions", "current", "guesses", "bets", "plan", "rng", "played", "events",
	"spread", "defining", "afterMs", "auto", "playing", "openedAt", "revealed", "winners", "second"}

func checkViews(t *testing.T, g *Game) {
	t.Helper()
	type viewer struct {
		id string
		v  Viewer
	}
	viewers := []viewer{{"", Viewer{Role: "table"}}, {"", Viewer{Role: "spectator", Name: "Sam", SpectatorID: "s1"}}}
	for _, p := range g.Players {
		viewers = append(viewers, viewer{p.ID, Viewer{Role: "player", Name: p.Name}})
	}
	for _, vw := range viewers {
		data, err := g.PublicState(vw.id, vw.v)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkView(g, vw.id, data); err != nil {
			t.Fatalf("%s, viewer %q (%s): %v\n%s", g, vw.id, vw.v.Role, err, data)
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
	var v View
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	cur := g.shownQuestion()
	// Question texts: only the one being played (or, after the game, the
	// last one revealed); facts and sources only in its reveal.
	for _, q := range g.Questions {
		current := cur != nil && cur.Question == q
		if strs[q.Text] && !current {
			return fmt.Errorf("the question %q is visible", q.Text)
		}
		if strs[q.Fact] && !(current && cur.Revealed) {
			return fmt.Errorf("the fact %q is visible", q.Fact)
		}
		for _, s := range q.Src {
			if strs[s] && !(current && cur.Revealed) {
				return fmt.Errorf("the source %q is visible", s)
			}
		}
	}
	if cur == nil {
		if v.Question != nil {
			return fmt.Errorf("a question with nothing to show")
		}
		return checkYou(g, viewerID, v)
	}
	qv := v.Question
	if qv == nil || qv.Seq != cur.Seq {
		return fmt.Errorf("question %+v, want seq %d", qv, cur.Seq)
	}
	lined := g.Phase != PhaseGuess
	shown := g.Phase == PhaseChips || cur.Revealed
	if !lined && qv.Slots != nil {
		return fmt.Errorf("slots before the line-up")
	}
	if lined && len(qv.Slots) != len(cur.Slots) {
		return fmt.Errorf("%d slots shown, %d on the line", len(qv.Slots), len(cur.Slots))
	}
	for _, s := range qv.Slots {
		if !shown && s.Chips != nil {
			return fmt.Errorf("chips before they are shown")
		}
		if !cur.Revealed && (s.Off != nil || s.Result != nil) {
			return fmt.Errorf("a slot's result before the reveal")
		}
	}
	if !cur.Revealed && (qv.Answer != nil || qv.Fact != nil || qv.Src != nil || qv.Results != nil || qv.Nobody != nil) {
		return fmt.Errorf("the answer or results before the reveal")
	}
	if cur.Revealed && (qv.Answer == nil || *qv.Answer != cur.Question.Answer || len(qv.Results) != len(cur.Playing)) {
		return fmt.Errorf("the reveal shows %v and %d results", qv.Answer, len(qv.Results))
	}
	if (qv.Guessed != nil) != (g.Phase == PhaseGuess) || (qv.BetsIn != nil) != (g.Phase == PhaseBet) {
		return fmt.Errorf("guessed %v / betsIn %v in phase %s", qv.Guessed, qv.BetsIn, g.Phase)
	}
	if g.Phase == PhaseGuess && len(qv.Guessed) != len(cur.Guesses) {
		return fmt.Errorf("guessed %v", qv.Guessed)
	}
	return checkYou(g, viewerID, v)
}

// checkYou: a player sees exactly their own guess and bet; nobody else gets
// a you.
func checkYou(g *Game, viewerID string, v View) error {
	if viewerID == "" {
		if v.You != nil {
			return fmt.Errorf("a you for a viewer without a seat")
		}
		return nil
	}
	if v.You == nil || v.You.ID != viewerID {
		return fmt.Errorf("you %+v", v.You)
	}
	cur := g.shownQuestion()
	var own *Guess
	var ownBet *Bet
	if cur != nil {
		own, ownBet = cur.guessOf(viewerID), cur.betOf(viewerID)
	}
	if (v.You.Guess == nil) != (own == nil) || (own != nil && *v.You.Guess != own.Value) {
		return fmt.Errorf("you.guess %v, own guess %+v", v.You.Guess, own)
	}
	if (v.You.Chips == nil) != (ownBet == nil) || (ownBet != nil && (*v.You.Chips != ownBet.Chips || v.You.Stake != ownBet.Stake)) {
		return fmt.Errorf("you.chips %v, own bet %+v", v.You.Chips, ownBet)
	}
	if p := g.Player(viewerID); v.You.StakeMax != min(3, p.Score) {
		return fmt.Errorf("stakeMax %d with %d points", v.You.StakeMax, p.Score)
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

// checkEvent: events go to every client, so only the reveal may carry
// guesses, bets or the answer, and it must carry all of them.
func checkEvent(t *testing.T, g *Game, e Event) {
	t.Helper()
	if e.Type != EvReveal {
		if e.Guesses != nil || e.Bets != nil || e.Answer != nil || e.Winners != nil || e.QuestionID != "" {
			t.Fatalf("event %+v carries a secret", e)
		}
		return
	}
	cur := g.Cur
	if e.Q != cur.Seq || *e.Answer != cur.Question.Answer || e.QuestionID != cur.Question.ID ||
		len(e.Guesses) != len(cur.Guesses) || len(e.Bets) != len(cur.Bets) {
		t.Fatalf("reveal %+v for question %d", e, cur.Seq)
	}
}

// checkResults: each result is the bonus, chips and stake, applied to the
// score with the floor at 0, and matches the player's score now.
func checkResults(t *testing.T, g *Game) {
	t.Helper()
	for _, r := range g.Cur.Results {
		p := g.Player(r.PlayerID)
		if r.After != max(0, r.Before+r.Bonus+r.ChipPoints+r.StakePoints) || r.Delta != r.After-r.Before || p.Score != r.After {
			t.Fatalf("result %+v, score %d", r, p.Score)
		}
		if p.Left && r.Delta != 0 {
			t.Fatalf("%s left but scored %+v", p.Name, r)
		}
	}
}

// checkLog checks the log lines added since logSeq (the log goes to every
// viewer): no question text or fact, and no answer before its reveal. It
// returns the last line's number.
func checkLog(t *testing.T, g *Game, logSeq int) int {
	t.Helper()
	var secrets []string
	for _, q := range g.Questions {
		secrets = append(secrets, q.Text, q.Fact)
	}
	if cur := g.Cur; cur != nil && !cur.Revealed {
		secrets = append(secrets, "answer", formatNumber(cur.Question.Answer, cur.Question.Format))
	}
	for _, e := range g.Log[max(0, len(g.Log)-(g.LogSeq-logSeq)):] {
		for _, s := range secrets {
			if strings.Contains(e.Text, s) {
				t.Fatalf("%s: the log line %q gives away %q", g, e.Text, s)
			}
		}
	}
	return g.LogSeq
}
