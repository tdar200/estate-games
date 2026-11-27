# EXPERT: a computer player trained by self-play

EXPERT is the fourth computer level (after Easy, Normal and Hard). It plays with
the same Monte Carlo search as Hard (when built: the same time budget, about
250 ms per key decision on all cores; since November 2026 a fixed number of
rollout rounds, see "Difficulty ladder and fair play"), but with two
ingredients learned from 1,071,995 self-play games:

1. **Tuned strategy knobs.** The heuristic AI (`internal/ai`, a port of
   `src/ai.js`) now reads its constants from `ai.Params`. CMA-ES tuned the 67
   knobs by self-play. EXPERT uses the tuned heuristic as its default move and
   as its own policy inside rollouts. The tuned knobs ship in
   `internal/hardai/expert_params.json`.
2. **A value network.** A small neural network (`internal/valuenet`, pure Go,
   embedded as `model.bin`) estimates each player's probability of winning from
   a position. Hard plays every rollout 16 rounds forward and scores it by
   net-worth share. EXPERT plays rollouts only one round forward
   and scores them with the network. So it gets several times more rollouts in
   the same time, and each one is scored by a far better estimate of who wins.
   The network also suggests trades: EXPERT screens many more trade ideas than
   Hard by how much each would raise its win probability.

It sees only public information. Rollouts reshuffle the unseen decks and roll
their own dice, as Hard's do, and the network reads only the public board.

**Results in one paragraph.** At 100 ms per decision, EXPERT wins **88.4%** of four-player games against three Normal bots, where Hard wins 65.9%. Against three Hard bots it wins **49.5%**, while each Hard bot wins about 17%. At a mixed table (EXPERT, Hard, Normal, Easy) it wins **70.8%**. In a Bradley–Terry fit over every exam game this gives **Elo 2016**, against Hard 1822, Normal 1500 and Easy 1355. Results with the loans house rule are about the same. Every number comes from cmd/arena, with seats rotated and Wilson 95% intervals (see Results).

**Since November 2026** (fair-play fixes and fixed rollout counts, see "Difficulty ladder and fair play"): the ladder is Easy 1333, Normal 1500, Hard 1721, EXPERT 1903, even steps of about 170–220 Elo. EXPERT wins 76% against three Normal bots and 52% against three Hard bots, and closes at most 2 deals it proposes per partner and game.

## How to play it

* In the lobby, the computer-level chip cycles EASY → NORMAL → HARD → EXPERT
  (PC table and phone).
* API: `POST /api/rooms/{code}/ai {token, difficulty:"expert"}` (docs/API.md).
* Go: `hardai.NewBot(hardai.Expert, seed, hardai.Options{})` or
  `hardai.ParseLevel("expert")`. `server.RealBots("expert", seed)` maps it in
  the server.
* Arena: `go run ./cmd/arena -seats expert,hard,hard,hard -games 1000` (no time
  limit by default: the levels' round counts decide, as in production), then
  `go run ./cmd/arena -combine a.json,b.json,...` for one Elo ladder over several
  `-json` reports.

## Method

### 1. The knobs (`internal/ai/params.go`)

Every tunable constant of the heuristic is a field of `ai.Params`.
`ai.DefaultParams` reproduces `src/ai.js` exactly. The JS-fixture parity tests,
the engine trace cross-check, the hardai tests and the server tests all run
unchanged, and `TestParamsDefaultParity` replays every fixture through the
parameterised entry point. `ai.Knobs` lists them with a range for the tuner:

67 knobs in seven groups: value (29: the multipliers for complete and partial colour groups, railroad and utility values, the EV-to-cash factor, denial and leader weights, the portfolio value of houses and mortgaged spaces), reserve (9: the cash kept back at each stage of the game and its adjustments), auction/buy (2), build (6: ordering weights and the reserve kept when building), mortgage (8: reserves for lifting mortgages, which holdings to mortgage and which houses to sell), jail (3) and trade (10: reserves, the minimum gain of a proposal, the fairness share demanded, liquidity floors).

<details><summary>All 67 knobs: default and tuned value (bold = moved by at least 5% of its range)</summary>

| group | knob | default | tuned | range |
|---|---|---:|---:|---|
| value | `complete.brown` | 1.25 | **1.87** | 0.8–4 |
| value | `complete.lightBlue` | 1.65 | **2.21** | 0.8–4 |
| value | `complete.pink` | 1.90 | **2.11** | 0.8–4 |
| value | `complete.orange` | 2.25 | **2.42** | 0.8–4 |
| value | `complete.red` | 2.10 | 2.07 | 0.8–4 |
| value | `complete.yellow` | 1.90 | **1.36** | 0.8–4 |
| value | `complete.green` | 1.60 | **0.95** | 0.8–4 |
| value | `complete.darkBlue` | 1.35 | **1.67** | 0.8–4 |
| value | `partial.brown` | 1.05 | **0.94** | 0.7–2.2 |
| value | `partial.lightBlue` | 1.15 | **1.67** | 0.7–2.2 |
| value | `partial.pink` | 1.20 | **1.34** | 0.7–2.2 |
| value | `partial.orange` | 1.30 | **1.67** | 0.7–2.2 |
| value | `partial.red` | 1.25 | **1.07** | 0.7–2.2 |
| value | `partial.yellow` | 1.20 | 1.20 | 0.7–2.2 |
| value | `partial.green` | 1.10 | **0.83** | 0.7–2.2 |
| value | `partial.darkBlue` | 1.05 | **1.28** | 0.7–2.2 |
| value | `baseValue` | 1.10 | **0.96** | 0.6–1.8 |
| value | `evCashFactor` | 3.00 | 3.18 | 1–7 |
| value | `railroadEV` | 18 | **7.23** | 4–45 |
| value | `railroadCapScale` | 1.00 | 1.03 | 0.4–2.2 |
| value | `utilityBase` | 0.85 | 0.90 | 0.3–1.5 |
| value | `utilityEV` | 12 | **21** | 2–35 |
| value | `denialScale` | 1.00 | 0.88 | 0–2.5 |
| value | `leaderScale` | 1.00 | 1.00 | 0–2.5 |
| value | `houseValue` | 0.70 | **0.35** | 0.2–1.3 |
| value | `mortgagedValue` | 0.55 | 0.57 | 0.2–1 |
| value | `railroadPortfolio` | 1.00 | 1.07 | 0.4–2 |
| value | `utilityPortfolio` | 1.00 | **0.49** | 0.4–2 |
| value | `acquisitionOpen` | 8.00 | 8.00 | 2–20 |
| reserve | `reserve.acquisition` | 1.00 | **0.80** | 0.1–2.5 |
| reserve | `reserve.formation` | 1.00 | **0.71** | 0.1–2.5 |
| reserve | `reserve.development` | 1.00 | **0.23** | 0.1–2.5 |
| reserve | `reserve.endgame` | 1.00 | **1.32** | 0.1–2.5 |
| reserve | `reserveHostileDev` | 150 | **10** | 0–450 |
| reserve | `reserveLuxury` | 250 | **186** | 0–700 |
| reserve | `reserveMin` | 80 | **45** | 0–300 |
| reserve | `completeOrange` | 140 | **114** | 0–400 |
| reserve | `completeStrong` | 100 | **167** | 0–400 |
| auction | `auctionScale` | 1.00 | 1.02 | 0.5–1.7 |
| auction | `buyScale` | 1.00 | **1.19** | 0.5–1.7 |
| build | `buildTierLow` | 1.15 | **1.70** | 0.2–4 |
| build | `buildTierThree` | 1.80 | 1.71 | 0.2–5 |
| build | `buildTierHigh` | 0.78 | 0.69 | 0.1–4 |
| build | `shortageBonus` | 1.18 | **1.34** | 0.6–2.5 |
| build | `hotelPenalty` | 0.45 | **1.02** | 0.05–2 |
| build | `buildReserve` | 1.00 | **0.09** | 0–2.5 |
| mortgage | `unmortgageLow` | 250 | 292 | -100–900 |
| mortgage | `unmortgageMid` | 100 | 135 | -150–700 |
| mortgage | `unmortgageHigh` | 0.00 | **96** | -200–500 |
| mortgage | `liquidateStrong` | 45 | **8.86** | 0–200 |
| mortgage | `liquidateWeak` | 20 | **35** | 0–120 |
| mortgage | `liquidateNear` | 12 | 9.78 | 0–80 |
| mortgage | `sellThreePenalty` | 3.00 | **4.09** | 0.5–10 |
| mortgage | `sellHighPenalty` | 1.35 | **2.47** | 0.3–5 |
| jail | `jailOpenSpaces` | 8.00 | **14** | 1–24 |
| jail | `jailHitFraction` | 0.60 | 0.69 | 0.05–2 |
| jail | `jailIncomeRatio` | 1.25 | **0.59** | 0.3–4 |
| trade | `tradeReserve` | 220 | **331** | 0–700 |
| trade | `minTradeGain` | 12 | 3.80 | 0–250 |
| trade | `fairnessShare` | 0.30 | **0.48** | 0–1.2 |
| trade | `fairnessShare2P` | 0.40 | **0.17** | 0–1.5 |
| trade | `leaderPremium` | 0.25 | **0.46** | 0–1.2 |
| trade | `threatShare2P` | 0.90 | **1.38** | 0.2–2 |
| trade | `threatShareLeader` | 0.75 | **1.10** | 0.2–2 |
| trade | `liquidityFactor` | 0.70 | **0.49** | 0–2 |
| trade | `liquidityMin` | 180 | 167 | 0–600 |
| trade | `acceptMargin` | 0.00 | 6.31 | -100–300 |

</details>

When EXPERT prices a trade it proposes, it models the target's acceptance with
the *default* knobs, because it assumes the others play the default heuristic.
It values the trade for itself with its own knobs. Answers to trades use its
own knobs. Loan decisions keep the default constants, since loans are a house
rule and the tuning games use classic rules.

### 2. Tuning by self-play (`estate-ml tune`)

* **Optimiser:** separable CMA-ES (Ros & Hansen 2008), which uses a diagonal
  covariance. The method is well founded, and with 67 noisy dimensions and
  about 2,000 evaluations a full covariance cannot be learned anyway. It searches
  in normalised coordinates (each knob scaled to its range), with σ₀ = 0.08,
  population 24 and 80 generations, starting from the defaults.
* **Fitness:** win rate over 400 four-player games per candidate. Each
  candidate plays 100 deals, each in all four seats. All candidates of a
  generation share the same deals (common random numbers), and turn order is
  rolled as in a real game. Games still running at 1,000 turns are adjudicated
  on net worth.
* **Field:** for generations 1–40 the three opponents are Normal bots. From
  generation 41 the field is one Normal bot plus two copies of the
  *champion*, the best validated strategy so far, so the strategy keeps
  improving against strong play instead of only exploiting Normal.
* **Selection:** every 5 generations the distribution mean is validated on
  2,000 fixed games against 3×Normal, and in stage 2 also on 2,000 against
  the champion field. In stage 1 the mean with the best win rate is kept. In
  stage 2 the best average of the two win rates is kept, provided it gives up
  no more than 1.5 points against Normal. The result is `tune/best.json`.
* **Hard-lite in the field:** rejected. One Monte Carlo seat makes a game
  roughly 10–50× slower, which would have cut the tuning to a few
  generations. The champion field supplies the strong-opponent pressure
  instead, and Hard is measured at the end in the arena.

**Tuning curve.** The run lasted 70 generations: 40 against 3×Normal and 30 against the champion field. It was stopped there because stage 2 had levelled off, and the remaining game budget went to value-network data. Fitness is the win rate of a candidate (25% = as good as the field):

| generation | field | best candidate | mean of the population | σ |
|---:|---|---:|---:|---:|
| 1 | 3×Normal | 31.5% | 22.4% | 0.076 |
| 5 | 3×Normal | 45.5% | 36.9% | 0.069 |
| 10 | 3×Normal | 40.5% | 36.2% | 0.070 |
| 20 | 3×Normal | 49.3% | 43.6% | 0.073 |
| 30 | 3×Normal | 54.8% | 50.9% | 0.067 |
| 40 | 3×Normal | 55.8% | 51.7% | 0.062 |
| 45 | Normal + 2×champion | 32.8% | 28.9% | 0.058 |
| 55 | Normal + 2×champion | 35.5% | 32.2% | 0.052 |
| 70 | Normal + 2×champion | 37.5% | 33.3% | 0.053 |

Validation of the distribution mean (2,000 fixed games each; 95% CI about ±2.2 points):

| generation | vs 3×Normal | vs Normal + 2×champion | selection score |
|---:|---:|---:|---:|
| 0 (defaults = Normal) | 25% | n/a | n/a |
| 5 | 39.1% | n/a | n/a |
| 10 | 42.8% | n/a | n/a |
| 20 | 49.1% | n/a | n/a |
| 30 | 50.2% | n/a | n/a |
| 40 (champion) | 54.4% | 29.6% | 42.0 |
| 45 | 54.9% | 31.4% | 43.1 |
| 50 | 46.9% | 30.8% | 38.8 |
| 55 | 54.4% | 32.5% | 43.4 |
| **60 (selected)** | **53.4%** | **33.9%** | **43.6** |
| 65 | 50.4% | 33.8% | 42.1 |
| 70 | 50.5% | 33.5% | 42.0 |

So the tuned heuristic alone, with no search, wins **53.4%** of 4-player games against three default heuristics (Normal), where an equal player would win 25%. What it learned, roughly:

* **Spend on buildings, keep less cash.** It keeps 23% of the default reserve once groups are being developed and 9% of it when building. It barely adds to the reserve when an opponent has three houses (+10 instead of +150), and it puts the hotel penalty aside.
* **Value colour groups differently.** It prizes light blue, orange, brown and dark blue more and green and yellow less, for complete and partial groups alike. It values railroads lower and utilities higher in auctions.
* **Trade harder.** As a target it demands a bigger share of the proposer's gain (fairness 0.30 → 0.48; 0.75/0.90 → 1.10/1.38 against three-house threats), and it keeps more cash after proposing (220 → 331).
* **Jail:** it stays in jail longer once the board is bought up (it leaves early only while 14 or more spaces are unowned, instead of 8).

### 3. The value network (`internal/valuenet`)

* **Data:** positions at the start of a turn, sampled from self-play games.
  The label is the game's winner (the net-worth leader if the turn cap
  stopped the game). There are two sources:
  * every tuning game (10% of turn starts; four players, classic rules,
    candidate strategies against Normal or the champion);
  * a dedicated run with the tuned knobs (`estate-ml data`, 30% of turn
    starts): 2–6 players (mostly 4), house rules mixed in (loans, jackpot,
    no auctions), and seats played by the tuned bot, Normal, or a noisy tuned
    variant for variety.

  This came to 8.8M positions from 730,000 tuning games and 13.3M from 300,000 dedicated games, about 44 per game.
* **Features** (690 inputs, from the evaluated player's point of view, with
  opponents numbered in turn order after it): for each seat, whether it is
  present and active, cash, net worth, jail state and cards, whose turn it is,
  complete groups, buildings, expected rent collected per opponent roll (from
  the board's stationary landing probabilities), expected rent paid on its next
  roll, railroads, utilities, loans, mortgages, and its square (one-hot). For
  each ownable space: owner (bank or seat), houses, hotel, mortgaged. For each
  colour group: my share, the best opponent share, who completed it, its
  building level. Global inputs: turn count, houses and hotels left, players
  left, house rules and the jackpot pot.
* **Network:** 690 → 128 → 64 → 1, ReLU, about 97k weights. It produces one
  score per active player, and a softmax over the players gives each one's win
  probability, so they always sum to 1 and any player count works. The first
  layer is evaluated sparsely, which takes about 40 µs per position for four
  players.
* **Training** (`estate-ml train`, pure Go, no dependencies): softmax cross
  entropy of the actual winner, Adam with decoupled weight decay, minibatches
  of 1,024 split over 24 goroutines, and a cosine learning-rate decay. One game
  in 20 is held out for validation (split by game, so no game is on both
  sides). The epoch with the lowest validation loss is kept. The gradients are
  checked against finite differences in `TestGradient`.

**Value network results.** The first network (`net/gen1`) is 690-128-64-1 and was trained on 21.0M positions: 8.8M from the tuning games and 13.3M from 300,000 dedicated games, 1.49 GB in all. It is measured below on 660,876 positions from held-out dedicated games, which no training game shares a deal with. Log loss is the cross entropy of the actual winner; "chance" means guessing every active player equally.

| held-out positions | n | log loss | chance | most-likely player won |
|---|---:|---:|---:|---:|
| **all** | 660,876 | **0.749** | 1.235 | **67.7%** |
| turns 0–39 | 176,630 | 1.203 | 1.360 | 44.3% |
| turns 40–99 | 255,495 | 0.717 | 1.296 | 70.0% |
| turns 100–199 | 189,872 | 0.410 | 1.065 | 83.1% |
| turns 200–399 | 30,275 | 0.466 | 0.983 | 83.3% |
| 2 players | 36,565 | 0.424 | 0.693 | 77.3% |
| 4 players | 394,765 | 0.742 | 1.219 | 67.2% |
| 6 players | 89,053 | 0.932 | 1.565 | 65.4% |

Brier score 0.113 (per player). It is well calibrated: within each tenth of predicted probability the observed win rate is within about 3 points (0.0–0.1 → 3.3%, 0.2–0.3 → 25.0%, 0.5–0.6 → 52.5%, 0.8–0.9 → 82.4%, 0.9–1.0 → 96.3%), and only slightly overconfident at the top.

Overfitting was the main difficulty. Positions of one game share its label, so a few epochs are enough for the net to memorise individual games. Validation loss was lowest after the first epoch and rose after that, so the first epoch's weights are kept. The tuning games make this worse, because every deal is replayed 96 times in a generation (24 candidates × 4 seats). A validation set drawn from them overstates the quality: it gave 0.713, against 0.749 on independent games, so every figure above uses independent games only.

### 4. The EXPERT bot (`internal/hardai/expert.go`)

`NewExpertBot` is a `HardBot` with these settings:

* the baseline choice and the plans it expands come from the tuned heuristic;
* in rollouts its own seat plays the tuned heuristic and the other seats play
  the default heuristic (`Options.OpponentParams`);
* rollouts stop after one round and are scored by the value
  network's probability that EXPERT wins (1 or 0 if the game ended);
* it overrides its baseline when the paired advantage exceeds z = 0.5 standard
  errors (Hard requires 1), because its estimates are much less noisy;
* it evaluates up to 14 candidate plans per decision (Hard evaluates 10).
  When this was written it had a cap of 1,000 rollout rounds (Hard's was
  200), so the time budget rather than the cap ended its decisions; since
  November 2026 both levels stop at a fixed number of rounds (Expert 200,
  Hard 12) and the 250 ms budget is only a safety cap (see "Difficulty
  ladder" below);
* **trade ideas screened by the value network** (`nettrades.go`). Hard only
  proposes a few hand-picked kinds of trade: completing its own colour group,
  buying a railroad or utility, or selling a spare holding. EXPERT also looks
  at every tradeable space of every computer opponent, for cash or for one of
  its own spare holdings, and at every sale of a spare holding. It scores each
  idea statically (the network's change in its win probability at list price),
  prices the best 8 with `ai.QuoteTrade` (the owner's own acceptance test),
  and passes the 3 that still raise its win probability at that price to the
  search as extra candidates. The rollouts make the final call.

The search is otherwise Hard's: candidate plans for purchases, auction
ceilings (win-at-price grid, break-even interpolated), trade answers, jail,
building, mortgages and trade proposals; common random numbers across
candidates; pruning; and a paired-z test before overriding the baseline. The
time budget is the same, so production decisions stay within about 250 ms on
all cores.

**Choosing the search settings (A/B).** Each arm is EXPERT (gen1 network) against 3×Hard: 600 games on the same deals, 50 ms per decision, one core per decision. The baseline was horizon 3 and z = 1; each row changes one thing.

| arm | EXPERT wins | 95% CI | decision p50 |
|---|---:|---|---:|
| horizon 0 (finish the current turn, then the network) | 54.2% | 50.2–58.1 | 2 ms |
| horizon 1 | 52.8% | 48.8–56.8 | 8 ms |
| horizon 2 | 53.5% | 49.5–57.5 | 21 ms |
| **horizon 3 (baseline)** | **48.8%** | 44.9–52.8 | 44 ms |
| horizon 6 | 48.7% | 44.7–52.7 | 50 ms |
| override at z = 0.5 instead of 1 | 54.0% | 50.0–57.9 | 50 ms |
| rollouts model the opponents with the tuned knobs | 39.5% | 35.7–43.5 | 50 ms |
| no network-screened trade ideas | 42.2% | 38.3–46.2 | 25 ms |

The network is a better judge of a position than a few more rounds of heuristic play: horizons 0–2 beat 3–6 by about 5 points. The default is **horizon 1** (tied with 0 and 2, and it still averages over a round of the opponents' dice), with **z = 0.5**, network trade ideas on, and opponents modelled by the default heuristic. The opponents here are Hard bots, which play the default heuristic. Modelling them with EXPERT's own tuned knobs costs 9 points.

### 5. Iteration: EXPERT in the loop (round 2)

1. **Data:** 20,000 more games in which each seat was EXPERT with probability ½ (gen1 network, 10 ms per decision). The other seats were the tuned bot, Normal, or a noisy tuned variant. This gave 0.8M positions from stronger play (`data/gen2`).
2. **Retraining:** from scratch on all 23M positions (tuning, gen1, gen2) with **dropout 0.5** on both hidden layers. Dropout stopped the memorisation: validation loss now keeps falling every epoch, whereas without dropout it rose after the first.
3. **Comparison**, on held-out independent games (gen1 and gen2) and in play (EXPERT with the final settings against 3×Hard, 600 games on the same deals, 50 ms):

| network | held-out log loss | winner picked | Brier | EXPERT vs 3×Hard |
|---|---:|---:|---:|---:|
| gen1 (no dropout, 1 epoch) | 0.750 | 67.5% | 0.113 | 57.8% (53.8–61.7) |
| gen2 (dropout 0.5, 3 epochs) | 0.733 | 67.9% | 0.112 | **62.8%** (58.9–66.6) |
| gen2b (same data, 8 epochs) | 0.734 | 67.9% | 0.112 | n/a |

The gen2 network ships (`internal/valuenet/model.bin`). Training longer (gen2b) gave the same held-out loss, so its A/B was cancelled. Round 2 added **+5.0 points** against 3×Hard, more than the change in log loss suggests: positions from EXPERT's own games teach the network about the positions EXPERT actually reaches.

## Results

All numbers come from `cmd/arena`, run through `estate-ml eval` on the training machine (a Linux box with 18+ cores). Settings: 100 ms per decision, one core per decision, 18 games at once, seats rotated every game, the same deals for every matchup (seed 20261102), turn order rolled as in a real game. Games were capped at 1,000 turns, and none reached the cap. Intervals are Wilson 95%. Elo is a Bradley–Terry fit of winner-beats-loser pairs over all the exam games of a rule set, with Normal = 1500.

| matchup | rules | budget | games | first seat wins | 95% CI | Elo (first seat) | decision p50 / p95 |
|---|---|---|---:|---:|---|---:|---|
| HARD vs 3×NORMAL | classic | 100ms | 2500 | 65.9% | 64.0–67.8% | 1822 | 100 / 102 ms |
| EXPERT vs 3×NORMAL | classic | 100ms | 2500 | 88.4% | 87.0–89.6% | 2016 | 10 / 100 ms |
| EXPERT vs 3×HARD | classic | 100ms | 2500 | 49.5% | 47.6–51.5% | 2016 | 7 / 100 ms |
| Mixed: EXPERT, HARD, NORMAL, EASY | classic | 100ms | 1500 | 70.8% | 68.4–73.0% | 2016 | 9 / 100 ms |
| Ablation: HARD + tuned knobs vs 3×NORMAL | classic | 100ms | 1000 | 67.2% | 64.2–70.0% | (not rated) | 100 / 102 ms |
| HARD vs 3×NORMAL (loans) | loans | 100ms | 1000 | 65.0% | 62.0–67.9% | 1818 | 100 / 102 ms |
| EXPERT vs 3×NORMAL (loans) | loans | 100ms | 1000 | 88.1% | 85.9–90.0% | 2019 | 12 / 100 ms |
| EXPERT vs 3×HARD (loans) | loans | 100ms | 800 | 51.1% | 47.7–54.6% | 2019 | 8 / 100 ms |
| Mixed (loans) | loans | 100ms | 800 | 71.0% | 67.8–74.0% | 2019 | 11 / 100 ms |

**Elo**, classic rules: **EXPERT 2016**, Hard 1822, Normal 1500, Easy 1355. With loans: EXPERT 2019, Hard 1818, Normal 1500, Easy 1325. EXPERT is about **+195 Elo** above Hard and +515 above Normal. At the mixed table the other seats won Hard 24.6%, Normal 3.1%, Easy 1.5%. Against 3×Hard each Hard bot won about 17%.

**Ablation (what each part adds, against 3×Normal, 25% = equal):**

| player | wins vs 3×Normal |
|---|---:|
| Normal (default heuristic) | 25% (by symmetry) |
| tuned heuristic alone, no search | 53.4% (tuning validation, 2,000 games) |
| Hard: search + default heuristic | 65.9% |
| Hard search + tuned knobs, no network | 67.2% |
| **EXPERT**: search + tuned knobs + value network + network trade ideas | **88.4%** |

Inside the search, the tuned knobs add little (+1.3 points, within the noise). The value network and its trade ideas carry most of EXPERT's advantage.

**Decision timing.** In the exam (100 ms budget, one core) EXPERT's Monte Carlo decisions take p50 7–12 ms and p95 100 ms. With the network's low-variance estimates most decisions settle long before the budget. Hard uses its whole budget (p50 100 ms, p95 102 ms). A production-like run (250 ms budget, 18 cores per decision as in the server, 8 games) gave:

| | Monte Carlo decisions | p50 | p95 | max |
|---|---:|---:|---:|---:|
| EXPERT | 540 | 2.7 ms | 75 ms | 131 ms |
| Hard | 532 | 54 ms | 105 ms | 138 ms |

All other decisions (rolling, paying and so on) take well under a millisecond. So EXPERT stays well within the production budget of about 250 ms.

**Caveats.** All opponents are computer players built on the same heuristic, so EXPERT's edge over humans is unmeasured. Its rollouts model the other seats as the default heuristic, which is exactly right against Normal and Hard. Against 3×Hard it won 62.8% at 50 ms (the A/B) but 49.5% at 100 ms, because Hard improves more with time than EXPERT, whose decisions mostly settle early. At the full production budget the gap may narrow further. The PC was shared with the owner's own jobs during the runs, which affects time-budgeted bots equally within a run.

## Difficulty ladder and fair play (November 2026)

The independent review of 3 November 2026 (`docs/review/INDEPENDENT-REVIEW-2026-11-03.md`,
§4 and bugs B2, B6, B17) found the levels badly spaced and two of them
exploitable. What changed:

**Easy: bounded mistakes, no trade exploit (B2).** Easy used to accept any
trade the heuristic refused with a 30% coin drawn afresh for every offer, so a
$1 bid for its dearest deed worked in about 3 tries. Now (`levels.go`):

* a trade mistake is decided once per offer and turn: the coin is keyed on the
  proposer, the deeds on both sides and the turn, not on the cash or the game
  version, so re-offering the same deeds (with any cash) in the same turn gets
  the same answer;
* it only accepts a deal the heuristic would take with 25% of what Easy gives
  (deeds at list price, mortgaged ones at half, plus cash; at most $200) added
  to its side in cash: a little under value, never a pittance;
* never one that completes the proposer's colour group or breaks up its own;
* at most one such mistake per proposer and game;
* its other mistakes are bounded too: it never underbids for, or passes on, a
  space that completes its own colour group, never skips building two turns
  running, and pays bail by mistake only with $150 left over.

The bounds made Easy stronger (Normal's wins against three Easy bots fell from
33.5% to 31.8%), so its mistake rates went up by half, which puts it a full step
under Normal. `TestEasyRefusesLowballs` replays the review's attack (20
positions, 30 tries each, a new turn every other try): the old bot sold Crown
Avenue for $1 in the first position; the new one never sells.

**Hard and Expert answer a repeated offer the same way (B17).** A trade
answer's rollouts are seeded by the offer (its deeds, cash and the turn), and
an offer of the same deeds for no more cash than one refused in the last 3
rounds is refused without a new search.

**No trade spam, no farming (B6).** Every Hard, Expert and Easy bot keeps a
memory of its own offers (`trademem.go`):

* a refused deal, and any deal asking the same target for the same deeds (for
  a sale: offering the same deeds), rests for 8 of its turns, twice as long
  each time the same target refuses the same deed again (up to 64);
* two refusals from one target within 6 turns rest every offer to it for 4;
* after a refusal it makes no offer on its next turn (on top of the engine's
  one offer per turn), so at most one refused offer every other turn;
* Hard and Expert close at most 2 deals per partner and game that they
  proposed (`Options.MaxDeals`), so they cannot strip one opponent of its
  deeds. (In the review's game Expert made three trades with Easy, which ended
  with no deeds.)

The engine now also cools a refused sale down on the deeds offered, and Hard's
market sales respect it. Normal (the stateless heuristic) is held back by the
engine's cooldown only.

**Fixed rollout counts.** Hard used its whole 250 ms budget, so its strength
depended on the machine and its load (the review's 50 ms runs put it 130 Elo
lower than the 100 ms exam). Hard and Expert now stop a decision after a fixed
number of rollout rounds, Hard 12 and Expert 200 (most Expert decisions settle
much earlier), and the 250 ms budget is only a safety cap that a normal machine
never reaches. The arena plays them with no time limit by default, so arena
games and production games are the same. The counts are the dial that spaces
the ladder.

### The new ladder

Measured on a 6-core laptop with `cmd/arena`: seats rotated, seed
20261103 for every matchup, one core per decision, Wilson 95% intervals, and
one Bradley–Terry fit over all five matchups (`arena -combine`, 95% intervals
from 400 bootstrap resamples of the games). **Before** is the code as reviewed,
at the exam's 100 ms per decision; **after** is the shipped defaults with no
time limit (exactly what production plays).
To reproduce one row: `go run ./cmd/arena -games 300 -seats hard,normal,normal,normal
-seed 20261103 -parallel 6 -json h.json` (the old code: add `-budget 100ms`), then
`go run ./cmd/arena -combine e.json,h.json,x.json,m.json,xh.json` for the ladder.

| first seat wins | before | after |
|---|---:|---:|
| Normal vs 3×Easy (800 games) | 33.5% (30.3–36.8) | **46.2%** (42.8–49.7) |
| Hard vs 3×Normal (300) | 65.0% (59.4–70.2) | **53.3%** (47.7–58.9) |
| Expert vs 3×Normal (300) | 91.3% (87.6–94.0) | **76.3%** (71.2–80.8) |
| Mixed: Expert, Hard, Normal, Easy (300) | 69.7% (64.2–74.6) | **65.7%** (60.1–70.8) |
| Expert vs 3×Hard (200) | 56.5% (49.6–63.2) | **51.5%** (44.6–58.3) |

| Elo (Normal = 1500) | before | step | after | step |
|---|---:|---:|---:|---:|
| Easy | 1424 (1402–1449) | | **1333** (1310–1355) | |
| Normal | 1500 | +76 | 1500 | **+167** |
| Hard | 1821 (1786–1860) | +321 | **1721** (1693–1755) | **+221** |
| Expert | 2044 (2008–2086) | +223 | **1903** (1874–1935) | **+182** |

The steps went from 76 / 321 / 223 to 167 / 221 / 182. Expert is still the
strongest by a clear margin (it beats three Hard bots as often as all three
together, and wins 66% at the mixed table). It lost most against Normal, which
it used to farm: it closed 11.1 deals per game with three Normal bots, now 5.6.
Hard's count was chosen from a sweep against 3×Normal (300 games each): 8
rounds 54.0%, 12 rounds 53.3%, 16 rounds 62.3%, 24 rounds 62.0%. 12 gives the
most even steps. With every core of the laptop per decision and the 250 ms cap
(as the server runs them; 8 games), Hard's Monte Carlo decisions took p50 12 ms,
p95 21 ms, max 41 ms, and Expert's p50 4 ms, p95 43 ms, max 90 ms, well under
the cap. Expert with a cap of 3 deals per partner won 82.3% against 3×Normal
(7.6 deals per game), so the cap stays at 2.

Trade offers (all between bots here) before → after:

| matchup | offers per game | accepted | one deal to one target, mean of the per-game maximum |
|---|---:|---:|---:|
| Expert vs 3×Hard | 38.9 → 26.5 | 47% → 52% | 3.8 → 1.7 (worst game 15 → 3) |
| Mixed | 22.0 → 15.5 | 70% → 72% | 2.3 → 1.4 (worst game 13 → 5) |
| Expert vs 3×Normal | 16.0 → 12.3 | 89% → 78% | 1.2 → 1.4 (worst game 3 → 7) |

At the mixed table Expert closed 2.92 deals per game with Easy before and
1.76 after.

Every bot with a memory now offers one deal to one target at most 2–3 times in
a game (the worst games above); the repeats left come from Normal, which only
has the engine's 4-turn cooldown (up to 7 times in one game). An earlier version without the doubling rest still
let Hard ask Expert for the same deed every 8 turns through a 190-turn game (19
times).

### More than six players

Rooms take up to 8 players, but the value network's input has 6 player slots
(`valuenet.MaxSlots`, deliberately no longer `engine.MaxPlayers`; the embedded
model has 690 inputs). `valuenet.Seats` maps a game to the slots: one to one
for up to 6 players; for 7 or 8, the bankrupt players are left out (they own
nothing and never act again) once at most 6 are still in, and otherwise the
game does not fit (`valuenet.Fits`). While it does not fit, Expert searches
like Hard (16-round rollouts scored by net-worth share, z = 1, Hard's round
count) and skips the network's trade ideas; it switches to the network as soon
as the game fits, and every later position of a rollout fits too (players only
drop out). Easy, Normal and Hard need nothing special; `TestEightPlayerGames`
plays 7- and 8-player games with every level, and `TestExpertNetworkSeats`
checks the mapping and the switch.

**Future work:** retrain the value network with 8 player slots (new features,
`FeatureVersion` 2, and self-play data with 7 and 8 players), so Expert can use
it from the first turn of an 8-player game.
Such a network is now coded for the GRANDMASTER level (`internal/valuenet2` and
`estate-ml gm`, see `docs/ai/GRANDMASTER.md`); Expert keeps its own, unchanged.

## Games played

Every job records its games in `~/estate-ml/ledger/` (summed into `status/run.json`):

| job | games |
|---|---:|
| tuning (70 generations × 9,600, validations and the champion check) | 729,685 |
| dataset gen1 (tuned knobs; includes the 2,702 games of a shard replayed after the PC restarted) | 302,702 |
| dataset gen2 (EXPERT in the loop) | 20,000 |
| A/B tests (10 arms × 600) | 6,000 |
| exam (9 matchups) and production timing | 13,608 |
| **total** | **1,071,995** |

The PC restarted unexpectedly once, at about 20:17 on 2 November, during dataset generation. The journal shows no shutdown, panic, OOM or thermal event; earlier boots had ended with clean shutdowns. The job resumed from its shard checkpoints. From then on every job ran on 18 of the 24 cores, and the CPU package temperature was logged (`status/temps.jsonl`, `cpuTempC` in `run.json`). It stayed at 50–70 °C under load, with critical at 105 °C.

## Reproduce

All jobs run on the training machine under `~/estate-ml/` (binaries in `bin/`, logs
in `logs/`). Each job checkpoints and resumes when started again with the same
arguments. Status files for the dashboard are written to `status/`.

```sh
# build (on any machine) and copy to the training machine
GOOS=linux GOARCH=amd64 go build -trimpath -o estate-ml ./cmd/estate-ml
GOOS=linux GOARCH=amd64 go build -trimpath -o arena ./cmd/arena
scp estate-ml arena <training-host>:~/estate-ml/bin/

cd ~/estate-ml   # every command below runs here; -dir defaults to ~/estate-ml
# 1. tune the knobs (resumable: tune/state.json; result tune/best.json; positions -> data/tune)
bin/launch.sh tune tune -gens 70 -pop 24 -seeds 100 -sigma 0.08 -switch 40 -validate 5 -validate-seeds 500 -record 0.1 -workers 18
# 2. dedicated positions with the tuned knobs (data/gen1, resumable by shard)
nice -n 10 bin/estate-ml data -set gen1 -games 300000 -sample 0.3 -workers 18 -params tune/best.json
# 3. first value network (keep the best epoch: net/gen1/best.bin)
nice -n 10 bin/estate-ml train -sets tune,gen1 -out gen1 -epochs 8 -wd 0.02 -workers 18
# 4. A/B the search settings (EXPERT vs 3 x Hard, 50 ms) -> eval/ab.json
bin/estate-ml eval -file eval/ab.json -name "AB horizon 1" -- -games 600 -seats expert,hard,hard,hard -budget 50ms -parallel 18 -seed 777 -expert-net net/gen1/best.bin -expert-params tune/best.json -expert-horizon 1
# 5. round 2: EXPERT-in-the-loop positions, retrain with dropout, compare (bin/pipeline3.sh 1 -expert-confidence 0.5)
nice -n 10 bin/estate-ml data -set gen2 -games 20000 -sample 0.3 -workers 18 -params tune/best.json -expert 0.5 -budget 10ms -net net/gen1/best.bin -expert-horizon 1
nice -n 10 bin/estate-ml train -sets tune,gen1,gen2 -out gen2 -epochs 3 -dropout 0.5 -wd 0.02 -workers 18
bin/estate-ml evalnet -sets gen1,gen2 net/gen1/best.bin net/gen2/best.bin
# 6. ship: copy the results into the repo, run the tests, rebuild
cp tune/best.json    <repo>/internal/hardai/expert_params.json
cp net/gen2/best.bin <repo>/internal/valuenet/model.bin
# 7. the exam: every matchup through cmd/arena (status/eval.json, eval/*.txt)
bin/evalsuite.sh 100ms 2500 1000   # the published exam trimmed the slow tables to 800-1,500 games (see Results)
```

The helper scripts (`launch.sh`, `detach.sh`, `evalsuite.sh`, `pipeline3.sh`, `templog.sh`) are in `scripts/ml/`; copy them to `~/estate-ml/bin/`. `estate-ml knobs` lists the knobs. `arena -expert-net F -expert-params P`
evaluates another network or knob set without rebuilding, and `-hard-params P`
gives Hard tuned knobs (used for the ablation).

### Training dashboard

The dashboard (`cmd/mldash`) reads `~/estate-ml/status/*` and serves on port
3299 (`http://<training machine>:3299` on the LAN). To start it:

```sh
cd ~/estate-ml && nohup nice -n 15 bin/mldash -addr :3299 -dir ~/estate-ml > logs/mldash.log 2>&1 < /dev/null &
```
