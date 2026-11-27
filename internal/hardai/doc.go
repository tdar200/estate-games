// Package hardai provides computer opponents of five strengths for Estate
// and a fast driver that plays bot-vs-bot games (used by cmd/arena).
//
// # Levels
//
//   - Normal: the heuristic AI of internal/ai, unchanged (HeuristicBot).
//   - Easy: the heuristic with seeded, deliberate mistakes: it caps some
//     auctions well below value, declines some purchases, skips builds and
//     trade ideas, misplays jail and accepts some slightly bad trades
//     (EasyBot). Every mistake is bounded: it never lets its own colour
//     group go, and a trade mistake is decided once per offer and turn, at
//     most a little under value, never completes the proposer's group, and
//     happens at most once per proposer and game.
//   - Hard: Monte Carlo rollouts on top of the heuristic (HardBot).
//   - Expert: Hard's search with a self-play-tuned heuristic (ai.Params,
//     expert_params.json) as baseline and rollout policy, short rollouts
//     scored by a learned value network (internal/valuenet), and extra trade
//     ideas screened by that network (nettrades.go); see expert.go and
//     docs/ai/EXPERT.md. The network reads 6 players: in a 7- or 8-player
//     game Expert searches like Hard until at most 6 are still in
//     (HardBot.searchFor, valuenet.Fits).
//   - Grandmaster: Expert without the ladder handicaps (no cap on deals per
//     partner, up to 600 rounds per decision, 2.5 s safety cap), with trade
//     answers built to resist people (deeper rollouts, a confident-gain bar,
//     a higher one for handing over a colour group, part of the proposer's
//     gain counted against the deal, answers seeded by the deeds not the
//     cash and remembered within a round), opponents modelled by a mix of
//     styles, offers to people that look fair at list price, and its own
//     8-slot network when one is shipped (internal/valuenet2); see
//     grandmaster.go and docs/ai/GRANDMASTER.md.
//
// NewBot(level, seed, opts) returns a Bot for a seat. A Bot answers
// Choose(g, playerID) with the action that seat takes now; it never mutates
// g. Bots keep per-seat state (plans, caches): one bot per seat, not shared
// between goroutines.
//
// # How Hard decides
//
// For the decisions that matter it lists a few candidate plans, always
// including the heuristic's own choice:
//
//   - buy, decline, or mortgage-then-buy a property;
//   - an auction ceiling: "win the space at price X" for a few X against
//     "let the others have it"; the break-even price is interpolated, and
//     Hard then raises by the minimum while the bid is within it;
//   - accept or reject a trade addressed to it (loan offers are answered by
//     the heuristic, which knows the loan rules);
//   - in jail: roll, pay bail or use a card;
//   - before rolling and at the end of its turn: building plans (next tier,
//     three houses, all-in, all-in keeping a cushion, spread over several
//     groups, mortgaging spare holdings to reach three houses), lifting
//     mortgages, trades that complete one of its colour groups (cash, or cash
//     plus a spare property, full-set swaps first), buying an opponent's
//     railroad or utility, selling a spare holding above its price (never one
//     that completes the buyer's group), or simply rolling / ending the turn.
//     Proposals are priced with ai.QuoteTrade, the AI owner's own acceptance
//     test, so a computer owner accepts them; the rollouts decide whether
//     the deal is actually good for Hard.
//
// Each candidate is applied to a copy of the game (Clone, a hand-written deep
// copy; TestCloneMatchesJSON checks it equals the JSON round trip) and played
// forward Options.Horizon rounds with the heuristic for every seat. Each
// rollout scores 1 for a win, 0 for bankruptcy, otherwise the player's share
// of the table's net worth. All candidates of a round share the round's seed:
// the same reshuffled decks (the real order is hidden information) and the
// same per-seat dice streams (common random numbers). Rounds run in parallel
// on Options.Workers goroutines until Options.Rollouts rounds, the time
// budget, or a clear verdict (clearly worse candidates are pruned); an
// alternative replaces the heuristic's choice only if its paired advantage
// exceeds Options.Confidence standard errors. Routine moves (rolling,
// confirming a card, paying a debt, ending a turn with nothing to do) are
// the heuristic's.
//
// The round count (Options.Rollouts: Hard 12, Expert 200) sets a level's
// strength; the time budget (250 ms) is only a safety cap. With a negative
// Options.Budget (no time limit) a Hard bot is deterministic for a given
// seed, whatever the number of workers; with a budget that binds (a badly
// overloaded machine) the number of rollouts, hence occasionally the
// decision, depends on the machine.
//
// # Trades: no spam, no farming
//
// Hard, Expert and Easy remember their own offers (trademem.go): a refused
// deal and similar ones rest for a while, a target that keeps refusing
// rests, a refusal costs the next turn's offer, and Hard and Expert close
// at most Options.MaxDeals deals per partner and game. Hard and Expert
// answer a repeated offer the same way (answerTrade).
//
// # Rollout policy and speed
//
// internal/ai's trade-proposal search costs about a millisecond per call, so
// in rollouts it is replaced by a light proposer that offers cash for the one
// space completing a colour group, priced with ai.QuoteTrade (the AI target's
// own acceptance test). Everything else in a rollout is the heuristic's own
// decision; a handful of states where the heuristic has exactly one move are
// answered without calling it (TestOnlyMoveMatchesHeuristic). The AI view is
// built directly from the engine state instead of a JSON round trip
// (TestViewMatchesJSON; a reflection guard falls back to JSON if internal/ai
// ever reads a key the builder does not fill).
//
// # Driver
//
// Driver plays a game with one Bot per seat, asking NextActor who must act:
// the order roller, the trade or loan-offer target, the auction bidder due,
// otherwise the current player (src/rooms.js nextAiAction). If a bot has no
// action or the engine refuses it, the heuristic's action is played and the
// refusal is counted in Driver.Rejected. PlayMatch sets up and plays a whole
// game; MatchResult adjudicates games stopped by the turn cap on net worth.
package hardai
