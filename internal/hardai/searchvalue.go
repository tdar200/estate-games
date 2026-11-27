package hardai

import (
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/valuenet"
	"github.com/tdar200/estate-games/internal/valuenet2"
)

// SearchValueOptions configure SearchValue. Zero fields take the defaults.
type SearchValueOptions struct {
	Rollouts int            // rollouts (default 32)
	Rounds   int            // rounds of play per rollout (default 2)
	Net      *valuenet.Net  // Expert's network, used when Net2 is nil and the game fits it
	Net2     *valuenet2.Net // Grandmaster's network (any game fits)
	Seed     uint64
}

// SearchValue estimates every player's probability of winning g (index =
// g.Players; bankrupt players 0) by short rollouts, every seat playing a
// style drawn from Grandmaster's opponent mix, scored by the value network
// at their end (1 or 0 for a finished game; net-worth shares when no network
// reads the game). It is a search-improved target for training the network
// (estate-ml gm): the network's own estimate, a few rounds of real dice
// further on.
func SearchValue(g *engine.Game, o SearchValueOptions) []float64 {
	if o.Rollouts <= 0 {
		o.Rollouts = 32
	}
	if o.Rounds <= 0 {
		o.Rounds = 2
	}
	opts := Options{Params: ExpertParams(), Styles: OpponentStyles(), Eval: EvalValueNet, Net: o.Net, Net2: o.Net2}
	w := newSimWorker(opts)
	out := make([]float64, len(g.Players))
	root := cloneGame(g, true)
	horizon := o.Rounds * max(1, activeCount(g))
	var pos valuenet.Position
	var pos2 valuenet2.Position
	for r := 0; r < o.Rollouts; r++ {
		sim := cloneGame(root, true)
		w.prepareSim(sim, mix(o.Seed, uint64(r), 0x5ea7c4))
		w.policy.me = ""
		w.run(sim, horizon)
		switch {
		case sim.Status == engine.StatusFinished:
			for j, p := range sim.Players {
				if p.ID == sim.WinnerID {
					out[j]++
				}
			}
		case w.vn2 != nil:
			valuenet2.FillFromGame(&pos2, sim)
			probs := w.vn2.WinProbs(&pos2)
			for j, p := range sim.Players {
				if s := valuenet2.SlotOf(sim, p.ID); s >= 0 {
					out[j] += probs[s]
				}
			}
		case w.vn != nil && valuenet.Fits(sim):
			valuenet.FillFromGame(&pos, sim)
			probs := w.vn.WinProbs(&pos)
			for j, p := range sim.Players {
				if s := valuenet.SlotOf(sim, p.ID); s >= 0 {
					out[j] += probs[s]
				}
			}
		default:
			for j, p := range sim.Players {
				out[j] += score(sim, p.ID, EvalNetWorth)
			}
		}
	}
	for j := range out {
		out[j] /= float64(o.Rollouts)
	}
	return out
}
