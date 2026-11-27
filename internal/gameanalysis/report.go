package gameanalysis

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tdar200/estate-games/internal/gamerecord"
)

// pct formats a probability as a percentage; pts a change in percentage
// points with its sign.
func pct(p float64) string { return fmt.Sprintf("%.0f%%", 100*p) }

func pts(d float64) string {
	s := fmt.Sprintf("%+.1f", 100*d)
	if s == "+0.0" || s == "-0.0" {
		s = "0.0"
	}
	return s + " pts"
}

func (a *Analysis) player(id string) *Player {
	for i := range a.Players {
		if a.Players[i].ID == id {
			return &a.Players[i]
		}
	}
	return nil
}

func (a *Analysis) index(id string) int {
	for i := range a.Players {
		if a.Players[i].ID == id {
			return i
		}
	}
	return -1
}

func (p *Player) label() string {
	switch {
	case p.Kind == "ai" && p.Difficulty != "":
		return fmt.Sprintf("%s (computer, %s)", p.Name, strings.ToUpper(p.Difficulty))
	case p.Kind == "ai":
		return p.Name + " (computer)"
	case p.Input != "":
		return fmt.Sprintf("%s (person, %s)", p.Name, p.Input)
	}
	return p.Name + " (person)"
}

func (a *Analysis) name(id string) string {
	if p := a.player(id); p != nil {
		return p.Name
	}
	return id
}

const sparks = "▁▂▃▄▅▆▇█"

// sparkline draws values in [0, 1].
func sparkline(values []float64) string {
	runes := []rune(sparks)
	var b strings.Builder
	for _, v := range values {
		i := int(v * float64(len(runes)))
		i = max(0, min(len(runes)-1, i))
		b.WriteRune(runes[i])
	}
	return b.String()
}

// sampleIndexes picks up to k indexes of 0..n-1, evenly spread, with the
// first and the last.
func sampleIndexes(n, k int) []int {
	var out []int
	for j := 0; j < k && n > 0; j++ {
		i := j * (n - 1) / max(1, k-1)
		if len(out) == 0 || out[len(out)-1] != i {
			out = append(out, i)
		}
	}
	return out
}

func maxAbs(v []float64) float64 {
	m := 0.0
	for _, x := range v {
		m = max(m, abs(x))
	}
	return m
}

func downsample(v []float64, n int) []float64 {
	if len(v) <= n {
		return v
	}
	out := make([]float64, n)
	for i := range out {
		lo, hi := i*len(v)/n, (i+1)*len(v)/n
		sum := 0.0
		for _, x := range v[lo:hi] {
			sum += x
		}
		out[i] = sum / float64(hi-lo)
	}
	return out
}

// turnList writes "turns 51, 56 and 61" (at most 8, then "and N more").
func turnList(turns []int) string {
	if len(turns) == 1 {
		return fmt.Sprintf("turn %d", turns[0])
	}
	var parts []string
	for i, t := range turns {
		if i == 8 {
			parts = append(parts, fmt.Sprintf("%d more", len(turns)-8))
			break
		}
		parts = append(parts, fmt.Sprint(t))
	}
	return "turns " + strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// Markdown renders the report "How the game was won".
func (a *Analysis) Markdown(rec *gamerecord.Record) string {
	var b strings.Builder
	started := time.UnixMilli(rec.StartedAt)
	fmt.Fprintf(&b, "# How the game was won: %s, %s\n\n", a.Code, started.Format("Mon 2 Jan 2006 15:04"))
	b.WriteString(a.story(rec))
	b.WriteString("\n\n")

	b.WriteString("## Players\n\n")
	b.WriteString("Win probability at the start, at its peak and at the end. Each decision is scored against what the Expert policy would have done in the same position (see the end of the report); \"worse\" and \"better\" count the decisions that changed the player's chances by 3 points or more that way, and \"net\" adds up every score.\n\n")
	b.WriteString("| Place | Player | Start | Peak | End | Decisions | Worse | Better | Net vs policy |\n|---:|---|---:|---:|---:|---:|---:|---:|---:|\n")
	players := append([]Player(nil), a.Players...)
	sort.SliceStable(players, func(i, j int) bool {
		pi, pj := players[i].Place, players[j].Place
		if pi == 0 {
			pi = 99
		}
		if pj == 0 {
			pj = 99
		}
		return pi < pj
	})
	for _, p := range players {
		place := "-"
		if p.Place > 0 {
			place = fmt.Sprint(p.Place)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d | %d | %d | %s |\n", place, p.label(), pct(p.Start), pct(p.Peak), pct(p.End), p.Decisions, p.Worse, p.Better, pts(p.VsPolicy))
	}

	b.WriteString("\n## Win probability over the game\n\n")
	if len(a.Curve) > 0 {
		b.WriteString("At the start of every turn, low to high:\n\n```\n")
		width := 0
		for _, p := range a.Players {
			width = max(width, len([]rune(p.Name)))
		}
		for pi, p := range a.Players {
			values := make([]float64, len(a.Curve))
			for i, pt := range a.Curve {
				values[i] = pt.P[pi]
			}
			fmt.Fprintf(&b, "%-*s %s %s\n", width, p.Name, sparkline(downsample(values, 120)), pct(p.End))
		}
		b.WriteString("```\n\n| Turn |")
		for _, p := range a.Players {
			fmt.Fprintf(&b, " %s |", p.Name)
		}
		b.WriteString("\n|---:|")
		for range a.Players {
			b.WriteString("---:|")
		}
		b.WriteString("\n")
		for _, i := range sampleIndexes(len(a.Curve), 12) {
			pt := a.Curve[i]
			label := fmt.Sprint(pt.Turn)
			if pt.Step == a.Steps {
				label = "end"
			}
			fmt.Fprintf(&b, "| %s |", label)
			for pi := range a.Players {
				fmt.Fprintf(&b, " %s |", pct(pt.P[pi]))
			}
			b.WriteString("\n")
		}
	}

	if len(a.TurningPoints) > 0 {
		focus := a.name(a.WinnerID)
		if a.WinnerID == "" {
			focus = "the leader"
		}
		fmt.Fprintf(&b, "\n## Turning points\n\nThe turns that moved %s's chances most:\n\n", focus)
		for _, s := range a.TurningPoints {
			kind := map[string]string{"luck": "dice and cards", "end": "the game was ended", "decision": "mostly a decision by " + a.name(s.PlayerID)}[s.Kind]
			fmt.Fprintf(&b, "- **Turn %d** (%s, %s): %s\n", s.Turn, pts(s.Delta), kind, s.Text)
		}
	}

	if len(a.Best) > 0 {
		b.WriteString("\n## Best moves by people\n\nDecisions that gained the most against what the Expert policy would have done in their place:\n\n")
		highlightTable(&b, a, a.Best)
	}
	if len(a.Blunders) > 0 {
		b.WriteString("\n## The computers' biggest blunders\n\nDecisions that cost the computer the most against what the Expert policy would have done:\n\n")
		highlightTable(&b, a, a.Blunders)
		a.worstByKind(&b)
	}

	if len(a.Trades) > 0 {
		b.WriteString("\n## Trades\n\nEach side's change in win probability from just before the offer to just after the answer.\n\n| Turn | Offer | Answer | Proposer | Target |\n|---:|---|---|---:|---:|\n")
		for _, t := range a.Trades {
			fmt.Fprintf(&b, "| %d | %s | %s | %s %s | %s %s |\n", t.Turn, t.Text, t.Answer, a.name(t.ProposerID), pts(t.ProposerDelta), a.name(t.TargetID), pts(t.TargetDelta))
		}
	}

	if len(a.Auctions) > 0 {
		b.WriteString("\n## Auctions\n\nThe auctions that moved anyone's chances most, from the decline that opened them to the last bid:\n\n| Turn | Space (price) | Won by | Paid | Bids | Buyer's change |\n|---:|---|---|---:|---:|---:|\n")
		list := append([]Auction(nil), a.Auctions...)
		sort.SliceStable(list, func(i, j int) bool { return maxAbs(list[i].Deltas) > maxAbs(list[j].Deltas) })
		if len(list) > 10 {
			list = list[:10]
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].StartStep < list[j].StartStep })
		for _, au := range list {
			winner, change := "nobody", ""
			if au.WinnerID != "" {
				winner = a.name(au.WinnerID)
				change = pts(au.Deltas[a.index(au.WinnerID)])
			}
			bids := 0
			for _, bid := range au.Bids {
				if !bid.Pass {
					bids++
				}
			}
			fmt.Fprintf(&b, "| %d | %s (%s) | %s | %s | %d | %s |\n", au.Turn, au.Space, money(au.Price), winner, money(au.Paid), bids, change)
		}
	}

	b.WriteString("\n## How this was worked out\n\n")
	fmt.Fprintf(&b, "The record (%s, %d steps) was replayed through the rules engine and checked step by step. Win probabilities come from the %s: %d estimates (every turn start, and before and after every decision), in %.1f s.\n\n", a.Record, a.Steps, a.Oracle, a.Positions, float64(a.ElapsedMs)/1000)
	b.WriteString("A decision's score is the decider's win probability just after it minus just before it, both looked at to the same later turn. The position before a decision is played out by the Expert policy, so the score says how much better or worse the move was than the policy's own move (shown as \"Expert policy instead\"). The policy is a strong heuristic, not perfect play: a move it would not make can still be right, and repeated small differences (not building where it would build) add up. Rollouts make no trade offers of their own (the policy still answers offers), so a trade offer is scored against not trading. Scores within a couple of points of zero are noise.\n")
	if a.Quick {
		b.WriteString("\nThis is a quick analysis: few, short rollouts, so the numbers are rough. Run it without -quick for the full one.\n")
	}
	return b.String()
}

// worstByKind lists the computers' worst decision of each kind.
func (a *Analysis) worstByKind(b *strings.Builder) {
	type worst struct {
		h     Highlight
		n     int
		total float64
	}
	cats := map[string]*worst{}
	for _, d := range a.Decisions {
		p := a.player(d.PlayerID)
		if p == nil || p.Kind != "ai" || d.Delta > -notable || d.Source == gamerecord.SourceStandIn {
			continue
		}
		who := a.index(d.PlayerID)
		w := cats[d.Category]
		if w == nil {
			w = &worst{}
			cats[d.Category] = w
		}
		w.n++
		w.total += d.Delta
		if w.n == 1 || d.Delta < w.h.Delta {
			w.h = Highlight{Turn: d.Turn, PlayerID: d.PlayerID, Text: d.Text, Policy: d.Policy, Delta: d.Delta, Before: d.Before[who], After: d.After[who]}
		}
	}
	if len(cats) == 0 {
		return
	}
	var names []string
	for c := range cats {
		names = append(names, c)
	}
	sort.Strings(names)
	b.WriteString("\nThe worst of each kind (computers' decisions costing 3 points or more):\n\n")
	for _, c := range names {
		w := cats[c]
		instead := ""
		if w.h.Policy != "" {
			instead = " (the Expert policy: " + w.h.Policy + ")"
		}
		fmt.Fprintf(b, "- **%s** (%d, %s in all): turn %d, %s%s, %s\n", c, w.n, pts(w.total), w.h.Turn, w.h.Text, instead, pts(w.h.Delta))
	}
}

func highlightTable(b *strings.Builder, a *Analysis, list []Highlight) {
	b.WriteString("| Turn | Who | Move | Expert policy instead | Before | After | Change |\n|---:|---|---|---|---:|---:|---:|\n")
	for _, h := range list {
		policy := h.Policy
		if policy == "" {
			policy = "the same"
		}
		turn, change := fmt.Sprint(h.Turn), pts(h.Delta)
		if h.Count > 1 {
			turn = fmt.Sprintf("%d (×%d)", h.Turn, h.Count)
			change = fmt.Sprintf("%s (%s in all, %s)", pts(h.Delta), pts(h.Total), turnList(h.Turns))
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s |\n", turn, a.name(h.PlayerID), h.Text, policy, pct(h.Before), pct(h.After), change)
	}
}

// story is the opening paragraph.
func (a *Analysis) story(rec *gamerecord.Record) string {
	var b strings.Builder
	var others []string
	for _, p := range a.Players {
		if p.ID != a.WinnerID {
			others = append(others, p.label())
		}
	}
	duration := ""
	if rec.EndedAt > rec.StartedAt {
		d := time.Duration(rec.EndedAt-rec.StartedAt) * time.Millisecond
		duration = fmt.Sprintf(" in %s", d.Round(time.Minute))
	}
	w := a.player(a.WinnerID)
	switch {
	case w == nil && a.Reason != "":
		fmt.Fprintf(&b, "The game had no winner (%s) after %d turns%s. Players: %s.", reasonText(a.Reason), a.Turns, duration, strings.Join(others, ", "))
		return b.String()
	case w == nil:
		fmt.Fprintf(&b, "The game is unfinished after %d turns. Players: %s.", a.Turns, strings.Join(others, ", "))
		return b.String()
	}
	how := "by bankrupting everyone else"
	if a.Reason == gamerecord.EndEnded {
		how = "when the host ended the game, as the richest player"
	}
	fmt.Fprintf(&b, "**%s** won %s after %d turns%s, against %s.", w.label(), how, a.Turns, duration, strings.Join(others, ", "))
	wi := a.index(w.ID)
	// When did the winner take the lead for good?
	lead := -1
	for i, pt := range a.Curve {
		best := true
		for pi, p := range pt.P {
			if pi != wi && p >= pt.P[wi] {
				best = false
			}
		}
		if !best {
			lead = -1
		} else if lead < 0 {
			lead = i
		}
	}
	fmt.Fprintf(&b, " %s's chances went from %s at the start to %s", w.Name, pct(w.Start), pct(w.End))
	if lead >= 0 && lead < len(a.Curve)-1 {
		fmt.Fprintf(&b, "; they were the favourite from turn %d on", a.Curve[lead].Turn)
	}
	b.WriteString(".")
	var luck, decisions []string
	for _, s := range a.TurningPoints {
		switch s.Kind {
		case "luck":
			luck = append(luck, fmt.Sprintf("turn %d (%s)", s.Turn, pts(s.Delta)))
		case "decision":
			decisions = append(decisions, fmt.Sprintf("turn %d (%s, %s)", s.Turn, a.name(s.PlayerID), pts(s.Delta)))
		}
	}
	if len(a.TurningPoints) > 0 {
		fmt.Fprintf(&b, " Of the %d turns that moved their chances most, %d were mostly dice and cards", len(a.TurningPoints), len(luck))
		if len(decisions) > 0 {
			fmt.Fprintf(&b, " and %d mostly decisions: %s", len(decisions), strings.Join(decisions, ", "))
		}
		b.WriteString(".")
	}
	fmt.Fprintf(&b, " Against the Expert policy, %s's %d decisions scored %s net (%d clearly better, %d clearly worse).", w.Name, w.Decisions, pts(w.VsPolicy), w.Better, w.Worse)
	if len(a.Blunders) > 0 {
		bl := a.Blunders[0]
		fmt.Fprintf(&b, " The costliest computer decision: turn %d, %s (%s for %s).", bl.Turn, bl.Text, pts(bl.Delta), a.name(bl.PlayerID))
	}
	return b.String()
}

func reasonText(reason string) string {
	switch reason {
	case gamerecord.EndAbandoned:
		return "the room was closed while it was being played"
	case gamerecord.EndExpired:
		return "nobody came back to finish it"
	case gamerecord.EndReset:
		return "the room went back to the lobby"
	}
	return reason
}
