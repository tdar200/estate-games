package hardai

import (
	_ "embed"
	"encoding/json"
	"sync"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/valuenet"
)

// Expert level.
//
// The Expert bot is a HardBot (the same candidate plans and the same
// common-random-number Monte Carlo search) with two learned ingredients,
// both produced by self-play (cmd/estate-ml, docs/ai/EXPERT.md):
//
//   - tuned knobs (expert_params.json, ai.Params found by CMA-ES): the
//     heuristic it starts from (its baseline choice, the plans it expands)
//     and the policy its own seat follows in rollouts. The other seats are
//     modelled by the default heuristic (Options.OpponentParams);
//   - a value network (valuenet.Default, embedded): rollouts stop after a
//     few rounds (expertHorizon instead of Hard's 16) and are scored by the
//     network's probability that the bot wins the game, instead of a
//     net-worth share. Shorter rollouts mean several times more of them in
//     the same time, and the network's estimate is far less noisy than the
//     state of one random future.
//
// Like Hard, it never looks at hidden information: rollouts reshuffle the
// unseen decks and roll their own dice, and the network reads only the
// public board.

//go:embed expert_params.json
var expertParamsJSON []byte

var (
	expertOnce   sync.Once
	expertParams ai.Params
)

// ExpertParams returns the tuned knobs the Expert bot ships with.
func ExpertParams() *ai.Params {
	expertOnce.Do(func() {
		expertParams = ai.DefaultParams
		if err := json.Unmarshal(expertParamsJSON, &expertParams); err != nil {
			panic("hardai: bad expert_params.json: " + err.Error())
		}
	})
	p := expertParams
	return &p
}

// Expert defaults (used when the Options fields are zero).
const (
	expertHorizon    = 1   // rounds per rollout before the network scores it (A/B: 0-2 beat 3-6)
	expertCandidates = 14  // plans per decision
	expertRollouts   = 200 // round cap (most decisions settle well before it); sets its strength with the budget only a safety cap
	expertConfidence = 0.5 // paired z-score to override the baseline (Hard: 1); its estimates are less noisy
)

// NewExpertBot returns an Expert bot: a HardBot with the tuned knobs and
// the value network (see above). Zero Options fields take the Expert
// defaults; a nil Options.Net uses the embedded model.
func NewExpertBot(seed int64, opts Options) *HardBot {
	if opts.Params == nil {
		opts.Params = ExpertParams()
	}
	if opts.Net == nil {
		if net, err := valuenet.Default(); err == nil {
			opts.Net = net
		}
	}
	if opts.Net != nil {
		opts.Eval = EvalValueNet
	}
	if opts.Horizon == 0 {
		opts.Horizon = expertHorizon
	}
	if opts.MaxCandidates == 0 {
		opts.MaxCandidates = expertCandidates
	}
	if opts.Rollouts == 0 {
		opts.Rollouts = expertRollouts
	}
	if opts.Confidence == 0 {
		opts.Confidence = expertConfidence
	}
	return NewHardBot(seed, opts)
}
