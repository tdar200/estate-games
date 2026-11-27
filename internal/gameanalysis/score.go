package gameanalysis

import (
	"sort"
	"sync"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
)

// evalKey is one estimate: a position (the game before step pos) looked at to
// turn horizon.
type evalKey struct{ pos, horizon int }

// scoring holds every estimate of a game.
type scoring struct {
	probs   map[evalKey][]float64
	horizon map[int]int // each kept position's own horizon
	pair    map[int]int // each decision's horizon, shared by before and after
}

// own is position i's win probabilities at its own horizon.
func (s *scoring) own(i int) []float64 { return s.probs[evalKey{i, s.horizon[i]}] }

// before and after are decision i's positions, at the decision's horizon.
func (s *scoring) before(i int) []float64 { return s.probs[evalKey{i, s.pair[i]}] }
func (s *scoring) after(i int) []float64  { return s.probs[evalKey{i + 1, s.pair[i]}] }

// score estimates every position that matters, in parallel: each kept
// position at its own horizon (the curve), and both sides of every decision
// and of the host's End game at one shared horizon.
func score(rec *gamerecord.Record, positions map[int]*engine.Game, info []stepInfo, opts Options, first Oracle, index map[string]int) (*scoring, error) {
	s := &scoring{probs: map[evalKey][]float64{}, horizon: map[int]int{}, pair: map[int]int{}}
	var jobs []evalKey
	seen := map[evalKey]bool{}
	add := func(k evalKey) {
		if !seen[k] {
			seen[k] = true
			jobs = append(jobs, k)
		}
	}
	order := make([]int, 0, len(positions))
	for i := range positions {
		order = append(order, i)
	}
	sort.Ints(order)
	for _, i := range order {
		s.horizon[i] = first.Horizon(positions[i])
		add(evalKey{i, s.horizon[i]})
	}
	for i := range info {
		if info[i].decision || info[i].end {
			h := first.Horizon(positions[i+1])
			if info[i].end {
				h = s.horizon[i]
			}
			s.pair[i] = h
			add(evalKey{i, h})
			add(evalKey{i + 1, h})
		}
	}
	sort.Slice(jobs, func(a, b int) bool {
		if jobs[a].pos != jobs[b].pos {
			return jobs[a].pos < jobs[b].pos
		}
		return jobs[a].horizon < jobs[b].horizon
	})

	workers := min(opts.Workers, max(1, len(jobs)))
	oracles := []Oracle{first}
	for len(oracles) < workers {
		o, err := opts.NewOracle()
		if err != nil {
			return nil, err
		}
		oracles = append(oracles, o)
	}
	var mu sync.Mutex
	queue := make(chan evalKey)
	var wg sync.WaitGroup
	done := 0
	players := len(rec.Players)
	for _, o := range oracles {
		wg.Add(1)
		go func(o Oracle) {
			defer wg.Done()
			for k := range queue {
				byID := o.WinProbs(positions[k.pos], k.horizon)
				vec := make([]float64, players)
				for id, p := range byID {
					if j, ok := index[id]; ok {
						vec[j] = p
					}
				}
				mu.Lock()
				s.probs[k] = vec
				done++
				if opts.Progress != nil {
					opts.Progress(done, len(jobs))
				}
				mu.Unlock()
			}
		}(o)
	}
	for _, k := range jobs {
		queue <- k
	}
	close(queue)
	wg.Wait()
	return s, nil
}

// turnStarts are the positions of the curve: the first kept position of each
// turn, and the end.
func turnStarts(order []int, info []stepInfo) []int {
	var out []int
	for k, i := range order {
		if k == 0 || i == len(info) || info[i].turn != info[order[k-1]].turn {
			out = append(out, i)
		}
	}
	return out
}

// fill computes the curve, the decisions and their scores, each player's
// summary, the trades and the auctions.
func (a *Analysis) fill(rec *gamerecord.Record, s *scoring, curve []int, info []stepInfo, turns []int, times []int64, index map[string]int, names map[string]string) {
	for _, i := range curve {
		p := s.own(i)
		a.Curve = append(a.Curve, Point{Step: i, Turn: turns[i], At: times[i], P: round(p)})
		for pi := range a.Players {
			a.Players[pi].Peak = max(a.Players[pi].Peak, p[pi])
		}
	}
	first, last := s.own(curve[0]), s.own(curve[len(curve)-1])
	for pi := range a.Players {
		a.Players[pi].Start, a.Players[pi].End = first[pi], last[pi]
	}
	for i := range info {
		if !info[i].decision {
			continue
		}
		st := &rec.Steps[i]
		who := index[st.By]
		before, after := s.before(i), s.after(i)
		d := after[who] - before[who]
		a.Decisions = append(a.Decisions, Decision{
			Step: i, Turn: info[i].turn, PlayerID: st.By, Source: sourceOf(st), Type: st.ActionType(),
			Category: decisionTypes[st.ActionType()], Text: info[i].text, Policy: info[i].policy, ThinkMs: st.ThinkMs,
			Before: round(before), After: round(after), Delta: d,
		})
		p := &a.Players[who]
		p.Decisions++
		p.VsPolicy += d
		switch {
		case d <= -notable:
			p.Worse++
		case d >= notable:
			p.Better++
		}
	}

	// change is every player's change from the position before step from
	// (at that decision's horizon) to the position after step to.
	change := func(from, to int) []float64 {
		p, q := s.before(from), s.after(to)
		if p == nil || q == nil {
			return nil
		}
		out := make([]float64, len(p))
		for i := range p {
			out[i] = round1(q[i] - p[i])
		}
		return out
	}
	for _, t := range rec.Trades {
		tr := Trade{TradeSummary: t, Text: tradeText(t, names)}
		end := t.Step
		if t.AnswerStep > 0 {
			end = t.AnswerStep
		}
		if d := change(t.Step, end); d != nil {
			tr.ProposerDelta, tr.TargetDelta = d[index[t.ProposerID]], d[index[t.TargetID]]
		}
		a.Trades = append(a.Trades, tr)
	}
	for _, au := range rec.Auctions {
		if au.EndStep < 0 {
			continue
		}
		// From after the decline that opened it to after its last bid.
		p, q := s.after(au.StartStep), s.after(au.EndStep)
		if p == nil || q == nil {
			continue
		}
		d := make([]float64, len(p))
		for i := range p {
			d[i] = round1(q[i] - p[i])
		}
		a.Auctions = append(a.Auctions, Auction{AuctionSummary: au, Deltas: d})
	}
}

// turningPoints finds the turns that changed the winner's (or, with no
// winner, the leader's) chances most, on the curve, and says why: the turn's
// decision that moved them most when it explains much of the change (its own
// score, against the policy, has the same sign and at least 40% of the
// size), the host's End game, or else the dice and cards.
func (a *Analysis) turningPoints(rec *gamerecord.Record, s *scoring, curve []int, info []stepInfo, index map[string]int, names map[string]string) []Swing {
	focus := a.WinnerID
	if focus == "" && len(a.Players) > 0 {
		best := 0
		for i, p := range a.Players {
			if p.End > a.Players[best].End {
				best = i
			}
		}
		focus = a.Players[best].ID
	}
	fi, ok := index[focus]
	if !ok {
		return nil
	}
	var swings []Swing
	for k := 0; k+1 < len(curve); k++ {
		from, to := curve[k], curve[k+1]
		delta := s.own(to)[fi] - s.own(from)[fi]
		sw := Swing{FromStep: from, ToStep: to, Turn: info[from].turn, Delta: delta, Kind: "luck", Text: luckText(info[from:to], names)}
		top, topDelta := -1, 0.0
		for i := from; i < to; i++ {
			if info[i].end {
				top, topDelta = i, delta
				break
			}
			if !info[i].decision {
				continue
			}
			if d := s.after(i)[fi] - s.before(i)[fi]; abs(d) > abs(topDelta) {
				top, topDelta = i, d
			}
		}
		if top >= 0 && topDelta*delta > 0 && abs(topDelta) >= 0.4*abs(delta) {
			sw.Kind, sw.PlayerID, sw.Text = "decision", rec.Steps[top].By, info[top].text
			if info[top].end {
				sw.Kind, sw.PlayerID = "end", ""
			}
		}
		swings = append(swings, sw)
	}
	sort.SliceStable(swings, func(i, j int) bool { return abs(swings[i].Delta) > abs(swings[j].Delta) })
	if len(swings) > 8 {
		swings = swings[:8]
	}
	sort.SliceStable(swings, func(i, j int) bool { return swings[i].FromStep < swings[j].FromStep })
	for i := range swings {
		swings[i].Delta = round1(swings[i].Delta)
	}
	return swings
}
