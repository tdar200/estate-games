package hardai

import "github.com/tdar200/estate-games/internal/engine"

// Clairvoyant bot (analysis only, never offered in a game).
//
// It is Expert with one unfair advantage: it sees the future. Its rollouts
// keep the real order of the Chance and Community Chest decks and roll the
// dice from a copy of the game's own dice generator, so for every candidate
// plan it sees exactly the dice and cards that are coming. The other seats
// are still modelled by the heuristic (it cannot know how they will decide),
// so the future it sees drifts from the real one once another player plays
// differently. Its win rate against honest bots is an upper bound on what any
// honest bot can hope for from the same seat (cmd/gm-attack clairvoyant).

// Clairvoyant defaults.
const (
	clairvoyantHorizon = 3 // rounds of known dice before the network scores the position
)

// NewClairvoyantBot returns the clairvoyant analysis bot. peek must return
// a fresh copy of the game's dice generator in its current state (see
// MatchConfig.NewBotPeek). Every rollout of a decision is the same known
// future, so two rounds settle it (their spread is zero).
func NewClairvoyantBot(seed int64, opts Options, peek func() engine.RNG) *HardBot {
	opts.Rollouts = 2
	opts.MinRounds = 2
	if opts.Horizon == 0 {
		opts.Horizon = clairvoyantHorizon
	}
	if opts.MaxCandidates == 0 {
		opts.MaxCandidates = 24
	}
	b := NewExpertBot(seed, opts)
	for _, w := range b.workers {
		w.peek = peek
	}
	return b
}
