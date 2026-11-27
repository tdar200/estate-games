# GRANDMASTER: the top computer level

GRANDMASTER is the fifth computer level, after EXPERT. It is built to win as
often as the dice allow and to resist a strong person, who wins property-trading games with
trades. (Dice mean no player is unbeatable: see "The ceiling".)

**Results in one paragraph** (November 2026, GRANDMASTER v1: search and
logic on EXPERT's network). Against three EXPERT bots it wins **37.3%**
(34.6–40.1) of 4-player games, where an equal player wins 25% and a
clairvoyant EXPERT that sees the future dice wins 49.8%; 30.3% against four
(20% equal); 57.5% against three HARD and 84.2% against three NORMAL
(shipped EXPERT: 51.5% and 76.3%); Elo about 1994 against EXPERT's 1890
(Normal = 1500). A trade attacker that tries every deal shape at every price
gets a deal costing it 2 points of win probability or more in 40% of
positions (EXPERT: 51%), and through the trade screen's price, which is now
its own, in 37% (EXPERT, bound to the Normal heuristic's price: 65%); the
worst such deal costs it 27 points (EXPERT: 79). Most of its edge over
EXPERT bots comes from modelling opponents as a mix of styles. Its searched
decisions take under 0.1 s at the 95th percentile on the server's 24 cores,
its trade price about 0.3–0.5 s.

Status: the level is in place (hardai, the server's bot factory and quote,
both lobby pickers) and not yet deployed. Its own network (Phase 2) is
training on the training machine: generation 1 passed its gate narrowly (54% of
the decisive games against GRANDMASTER on EXPERT's network, z = 1.34);
none is shipped yet, so it plays with EXPERT's network.

## How to play it

* In the lobby, the computer-level chip cycles EASY → NORMAL → HARD → EXPERT
  → GRANDMASTER (PC table and phone).
* API: `POST /api/rooms/{code}/ai {token, difficulty:"grandmaster"}`. Its
  trade-screen price comes from Grandmaster itself (the chip says THINKING
  while it works it out, a moment on the server), and an offer at that price
  is accepted. It is never bound to the Normal heuristic's price.
* Since 0.10.1 Hard and Expert price deals the same way (`internal/hardai/quote.go`),
  and all three answer a person's offer by their price: at or above it,
  accepted; below it, a counter-offer at exactly that price (twice per deal
  and turn); with no price, the reason ("No deal: that would give you a full
  set.") or, for Expert and Grandmaster, a counter with one of the person's
  deeds added. The trade screen reads "ACCEPTS AT $650 · $348 MORE NEEDED",
  "WILL ACCEPT" or "WON'T TRADE AT ANY PRICE". Computers' own offers to
  people follow the server's rules (`internal/server/asks.go`, docs/API.md).
* Go: `hardai.NewBot(hardai.Grandmaster, seed, hardai.Options{})`;
  `server.RealBots("grandmaster", seed)` in the server.
* Arena: `go run ./cmd/arena -seats grandmaster,expert,expert,expert`.

## Why a new level

A person beat four EXPERT bots easily. EXPERT had only ever been measured
against other bots, so the first step was to measure how a person can exploit
it ("Phase 0" below), and only then to build GRANDMASTER against what was
found.

## Measuring how a person can exploit a level (`cmd/gm-attack`)

* **Positions** come from games between bots of the level under attack (3 to
  6 players): one early (the bank still holds 4 or more deeds), one mid and
  one late (6 or more houses built) turn start per game. The player about to
  roll becomes the human attacker.
* **The attack.** Against every other player it tries every deal shape:
  buying one deed, selling one, swapping one for one, buying two of a set,
  and two of its deeds for the one that completes its colour group. For each
  shape it walks a ladder of prices, most favourable to itself first, and
  stops at the first offer the computer accepts, as a person would. Every
  answer is the bot's real production answer (`Choose` on the game with the
  offer pending, production settings, no time limit). It also asks for the
  server's quote for every shape (the trade screen's ASK chip), which the
  server then accepts for the computer whatever its level, and it probes
  refused group-completing deals with $1 to $10 more.
* **The oracle** judges every accepted deal: the computer's win probability
  if it accepts minus if it refuses, from 512 paired rollouts (the same dice
  and decks for both answers), each 8 rounds of play with every seat on the
  tuned heuristic, then the value network. A sample is re-judged with
  whole-game rollouts (no network) as a cross-check.
* **Own decisions**: purchases, auction ceilings and its own accepted
  proposals, judged the same way.
* **The ceiling**: a clairvoyant bot (`hardai.NewClairvoyantBot`, analysis
  only) that runs Expert's search with the real future dice and cards.

## What the attack found on EXPERT (Phase 0)

Positions: 24 games between Expert bots (3 to 6 players), one early, mid and
late turn start each where the game reached it: 62 positions, 188
position/target pairs. The attacker
tried 8,023 deal shapes and made 38,561 offers. The figures below are from
the run on the PC that judged every deal (12,385 verdicts); a first run on
the laptop judged all group-completing deals but only a sample of the
others, and found the same rates per deal but fewer bad pairs.

**1. The trade screen's price is a promise the search never checks.** When a
person drafts a trade, the screen shows the computer's price (the ASK chip),
which the server gets from the Normal heuristic (`ai.QuoteTrade`), and the
server then accepts that deal for the computer whatever its level. So
EXPERT's search never sees these offers. Of the 6,848 quoted deals, 25.7%
(24.6-26.7) cost the computer 2 points or more of win probability (30.9% of
those that complete the person's colour group; 46.9% of one-deed purchases
in the laptop run), and 14.4% cost 5 points or more. In 122 of 188
position/target pairs (64.9%, 57.8-71.4) at least one quoted deal costs 2
points or more. EXPERT's own answer would have refused more than three
quarters of those bad deals (it accepts 22.4% of them).

**2. EXPERT's own answers.** Walking a price ladder and stopping at the first
yes, the attacker got a deal accepted in 68.5% of shapes (5,494). 18.5%
(17.5-19.6) of the deals EXPERT accepted cost it 2 points or more (26.4% of
the 888 group-completing ones; 17.0% of the others), 10.2% cost 5 points or
more; in 96 of 188 pairs (51.1%, 44.0-58.1) at least one did. (Laptop
run, by stage: mid game 28.8%, early 22.7%, late 5.1%.)

**3. Probing pays a little.** EXPERT seeds its answer by the cash offered, so
a dollar more buys fresh dice. Of 572 group-completing deals refused at every
price on the ladder, 43 (7.5%) were accepted for $1 to $10 more, and 30% of
those cost it 2 points or more.

**4. Its other decisions are sound, except auctions.** Purchases: 3% of
decisions cost 2 points or more. Its own trade proposals, when accepted, gain
it 7.9 points on average and none cost it 2 points (they cost the partner
7.1). Auctions: in 35% of auctions its ceiling is far from where the oracle
puts the break-even (an upper bound: it counts the best price above its
ceiling, or the loss of winning at its own ceiling).

**5. The oracle and its noise.** One verdict (512 paired rollouts of 8
rounds, then the network) has a noise of about 1.4 points (two independent
seeds disagree by 2.0 points RMS; the reported standard error, 1.0, is a
little optimistic). Longer rollouts that keep the full heuristic trading
(24 rounds) agree with it on the 2-point line for 87% of deals (52 of 60).
Whole-game rollouts with only the bots' light trading drift towards "the
computer gains" (they rarely swap deeds), but even they confirm all 40 of the
worst deals found as costing 2 points or more (and judge them worse).

**6. The ceiling.** A clairvoyant Expert (the same search, but its rollouts
use the real future dice and deck order) wins 49.8% (44.9-54.6) of 4-player
games against three Expert bots, where an equal player wins 25%. No honest
bot can expect to come near that.

The worst deals were of two kinds: a deed that gives a person a colour group
(Skyline Road, completing dark blue, for two cheap deeds and $158 at a
3-player table: -56 points for the computer), or a deed from a group the
computer was about to complete (it held two yellows and $1,507 while the
third sat with a cash-poor player; selling one yellow for $364 cost it 83
points, because in the rollouts it usually bought the third and built).
The network alone rated that computer at 8%: it reads the board, not the
deals about to happen, which is why short rollouts beat it as a judge.

## Method

GRANDMASTER is EXPERT (`docs/ai/EXPERT.md`: the tuned heuristic, the Monte
Carlo search over candidate plans, the value network) with these changes
(`internal/hardai/grandmaster.go`):

* **Trade answers it can defend.** An offer is judged by 600 rounds of
  paired rollouts, each eight rounds of play long before the value network
  scores it (EXPERT: at most 200 rounds of one round each). It accepts only
  if the gain in its win probability clears a margin of 0.4 points plus two
  standard errors, and 2 points plus 2.5 standard errors when the deal gives
  the proposer a colour group or breaks up one of its own. With three or
  more players still in, part of the proposer's own gain counts against the
  deal: 15%, or 40% when the proposer is, or would become, the strongest
  rival (no kingmaking).
* **Probing does not pay.** EXPERT's answer is seeded by the cash offered,
  so a person who offers a dollar more gets fresh dice and, now and then, a
  yes to a bad deal. GRANDMASTER seeds an answer by the deeds and the round
  only: a dollar more replays the same futures. Within a round it remembers
  each deal's answer as a cash threshold (refused for this much, so refused
  for less; accepted for this much, so accepted for more), as long as the
  deeds involved and their colour groups are unchanged.
* **Its own price.** `QuoteDeal` names the least it would accept for a deal
  from one search over a grid of prices with the same rollouts, and
  remembers it, so the offer at that price is accepted.
* **A mix of opponents.** EXPERT's rollouts assume every other player is
  the Normal heuristic. GRANDMASTER draws each other player's style per
  rollout: Normal (30%), the tuned heuristic (30%), an aggressive trader, a
  cash hoarder and an over-builder (about 13% each).
* **More thinking on the big decisions.** Every Monte Carlo decision may use
  up to 600 rounds (EXPERT 200) and 20 candidate plans (EXPERT 14); most
  still settle early. Auction ceilings are searched three rounds deep
  (EXPERT: one). The safety cap is 2.5 s per decision
  (EXPERT 250 ms). Routine moves stay instant.
* **Better proposals.** No cap on deals per partner (EXPERT: 2); the
  anti-spam memory stays. It prices 16 network-screened trade ideas and
  keeps 5 for the search (EXPERT 8 and 3), and it offers people deals that
  look fair at list price (a 30% premium in cash, 80% for the deed that
  completes its group, or an even swap) when they still raise its win
  probability.
* **2 to 8 players.** With EXPERT's network (6 player slots) a game of 7 or
  8 players is searched like Hard until enough players are out, with more
  rounds than EXPERT gets. Its own network (`internal/valuenet2`, 8 slots)
  reads every game from the first turn.

## Results (Phase 1: GRANDMASTER v1 on EXPERT's network)

### The same attack on GRANDMASTER

The same 62 positions and 188 position/target pairs (EXPERT's source games),
the same deal shapes and price ladders, every accepted deal judged by the
same oracle (15,791 verdicts for GRANDMASTER, 12,385 for EXPERT). Rates per
deal have 95% intervals over deals; rates per pair, Wilson intervals.

| | EXPERT | GRANDMASTER |
|---|---:|---:|
| shapes accepted at some price on the ladder | 68.5% | 51.2% |
| group-completing shapes accepted (of 1,465) | 888 | 241 |
| ladder deals it accepts that cost it ≥ 2 points | 18.5% (17.5–19.6) of 5,494 | **12.4%** (11.4–13.4) of 4,109 |
| ... of those completing the person's group | 26.4% (23.6–29.3) | **8.3%** (5.4–12.5) |
| ... that cost it ≥ 5 points | 10.2% | **5.8%** |
| pairs where the ladder finds such a deal | 96 (51.1%, 44.0–58.1) | **75 (39.9%, 33.2–47.0)** |
| refused group deals that $1–$10 more flips | 43 of 572 (7.5%), 30% of them bad | 44 of 1,219 (3.6%), 18% of them bad |
| the trade screen's price: deals costing it ≥ 2 points | Normal quote: 25.7% (24.6–26.7) of 6,848 | **its own price: 10.2%** (9.3–11.1) of 4,790 |
| ... completing the person's group | 30.9% | **6.0%** (4.4–8.1) |
| pairs where the trade screen's price is such a deal | 122 (64.9%, 57.8–71.4) | **70 (37.2%, 30.6–44.3)** |
| its own answer to the Normal quote's bad deals | accepts 22.4% | accepts 10.2% (8.9–11.8) |
| worst deal a person can get | −79 points (ladder) | −27 points (ladder or its price) |

What this says:

* **The trade screen is no longer the big hole.** With its own price in
  place of the Normal quote, the share of positions where the price on the
  screen is a bad deal falls from 65% to 37%, and the share of bad quoted
  deals from 26% to 10%. Group-completing quotes are rarely bad (6%).
* **Group deals are well defended.** It accepts a quarter as many
  group-completing deals as EXPERT, and the ones it accepts gain it 21
  points on average by the oracle. Probing works half as often.
* **The catastrophes are gone.** EXPERT's worst ladder deals cost it 75-79
  points (selling a yellow it was about to complete). GRANDMASTER's worst
  cost 27 (a green, mid game at a 6-player table, for a pink and $310;
  the person gains 46 points).
* **Ordinary deals are still exploitable.** One in eight ordinary deals it
  accepts on a ladder costs it 2 points or more, mostly purchases (25% of
  accepted one-deed sales to the person) and swaps (13%), mostly mid game
  (18%; late game 2%). A position offers at least one such deal 40% of the
  time. The attacker tries every shape at every price and keeps the first
  yes, so it finds exactly the deals whose verdict GRANDMASTER's own
  rollouts get wrong. Most are small: 5.8% cost 5 points or more.
* Of the 12 worst deals in the GRANDMASTER run, all are Normal-quote deals:
  what the server would have given away had it kept binding GRANDMASTER to
  the Normal quote. None is reachable now.

The earlier laptop runs agree: on the first 6 source games the 8-round
trade horizon (now the default) left 28% of pairs with a bad ladder deal,
the first 3-round version 35%, EXPERT 39%.

**Tried and not adopted: assume the proposer plays well**
(`GrandmasterOptions.SharpProposer`, `-gm-sharp`). Its rollouts draw a
style for every player, so the person proposing a deal is often played by
a weak style. With the proposer always played by the tuned heuristic,
GRANDMASTER would refuse 42% of the bad ladder deals it had accepted (and
8% of the good ones; `gm-attack reanswer`). But the full attack, which
adapts, found almost as many: 11.1% (10.2–12.1) of ladder deals cost it 2
points or more (12.4% without), 4.4% cost 5 or more (5.8%), and the same
share of pairs (76 against 75). It accepts more group-completing deals
(429 against 241), more of them bad (48 against 20). Its own price was a
little safer (9.4% bad, 66 pairs against 70), and its strength the same
(39.2%, 35.3–43.1, on the same 600 seeds as 38.0% without). Not clearly
better, so the default stays off.

### Strength (`cmd/arena`)

Seats rotated every game, turn order rolled, no time limit (the round counts
decide, as in production), one core per decision, Wilson 95% intervals.
Games on the training machine (22 at a time, in chunks of 60), plus 600 on the
laptop for the first row. EXPERT's figures for comparison are the shipped
EXPERT's, from `docs/ai/EXPERT.md` ("Difficulty ladder and fair play").

| matchup | games | GRANDMASTER wins | equal share | EXPERT in its seat |
|---|---:|---:|---:|---:|
| vs 3 EXPERT (4 players) | 1,200 | **37.3%** (34.6–40.1) | 25% | 25% |
| vs 4 EXPERT (5 players) | 600 | **30.3%** (26.8–34.1) | 20% | 20% |
| vs 3 HARD | 480 | **57.5%** (53.0–61.8) | 25% | 51.5% (44.6–58.3), 200 games |
| vs 3 NORMAL | 480 | **84.2%** (80.6–87.2) | 25% | 76.3% (71.2–80.8), 300 games |
| mixed: GRANDMASTER, EXPERT, HARD, NORMAL | 600 | **49.5%** (45.5–53.5) | 25% | (EXPERT 28.5%, HARD 19.2%, NORMAL 2.8%) |

One Bradley–Terry fit over the 2,760 PC games (Normal = 1500, 95% bootstrap
intervals): HARD 1787 (1735–1840), EXPERT 1890 (1844–1942), **GRANDMASTER
1994 (1950–2041)**, about 100 Elo above EXPERT, the same step as from HARD
to EXPERT. (EXPERT.md puts EXPERT at 1903 on its own ladder.)

**Where the strength comes from: the opponent model.** The same 480 seeds
against three EXPERTs, with and without the opponent style mix in its
rollouts (`-gm-nostyles`; everything else unchanged): **40.4%** (36.1–44.9)
with it, **26.7%** (22.9–30.8) without, which is EXPERT's own level. So
nearly all of GRANDMASTER's edge over EXPERT bots comes from modelling the
other players as a mix of styles instead of all as the Normal heuristic;
the deeper trade answers, wider searches and deeper auctions make it safer
to trade with, not stronger against bots. One of the styles is the tuned
heuristic that EXPERT's own rollouts play, so against EXPERT bots the mix
is partly a model of the real opponents. Whether it helps as much against
people is untested.

How it wins: it closes more deals and ends with more deeds. Against three
EXPERTs, 2.3 of its proposals are accepted per game (EXPERT's to it: 1.0)
and it holds 10.3 deeds at the end (an EXPERT 5.6). Against three NORMAL
bots it closes 11.5 deals a game and ends with 22 deeds: NORMAL accepts
anything its heuristic likes, and GRANDMASTER has no cap on deals per
partner. A person will not accept that many. Its anti-spam memory holds:
the worst repeat of one deal to one player was 4 or 5 times in a game
(EXPERT 4, HARD and NORMAL 3).

### Thinking time

One core per decision (the arena games above; every decision, routine moves
included): GRANDMASTER's median decision is instant, its 95th percentile
about 0.28 s (4 players; 0.43 s with 5) and its slowest 2.3 s (3.0 s with
5 players); EXPERT's are 0.05 s and 0.6 s. The searched decisions alone
(trade answers, auctions, turn plans) are about five times EXPERT's.

As the server runs it (every core on one decision, 2.5 s cap), on the Linux
PC (24 cores; 8 games against three EXPERTs, 1,715 GRANDMASTER decisions):
its searched decisions take 8 ms at the median, 89 ms at the 95th
percentile and 215 ms at most (EXPERT on the same cores: 2, 23 and 42 ms).
Its own trade price (`QuoteDeal`, what the trade screen waits for while
the chip says THINKING) took 0.33 s at the median, 0.48 s at the 95th
percentile and 0.52 s at most over 67 quotes on 22 cores (laptop, 10 cores:
0.58 s, 1.27 s, 1.27 s). The server runs at most two computer thoughts at
a time (`maxThinking`), quotes included, so a busy server adds waiting.

## Reproduce

```sh
# Phase 0 on EXPERT (seed 20261103 by default): trade attack + whole-game cross-check
go run ./cmd/gm-attack trades -level expert -games 24 -parallel 10 -rollouts 512 -rounds 8 \
    -crosscheck 40 -crosscheck-rollouts 512 -out expert_trades.jsonl
go run ./cmd/gm-attack summary -in expert_trades.jsonl -sources 12   # by stage, kind, players
go run ./cmd/gm-attack decisions -level expert -games 12 -per-game 8 -parallel 10 -rollouts 512 -rounds 8
go run ./cmd/gm-attack noise -games 6 -parallel 10 -rollouts 512 -rounds 8 -mid 24 -full 512
go run ./cmd/gm-attack noise -games 6 -parallel 10 -rollouts 512 -rounds 8 -skip-pair -mid 24 \
    -mid-full-trades -mid-rollouts 256 -items 60
go run ./cmd/gm-attack clairvoyant -games 400 -parallel 10
# Phase 1: both attacks with every accepted deal judged (-sample 1000000), on
# the same positions (Expert source games); -summary records the rates
go run ./cmd/gm-attack trades -level expert -games 24 -parallel 22 -rollouts 512 -rounds 8 \
    -sample 1000000 -out expert.jsonl
go run ./cmd/gm-attack trades -level grandmaster -source expert -games 24 -parallel 22 -rollouts 512 \
    -rounds 8 -sample 1000000 -out gm.jsonl
# strength (no time limit: the round counts decide, as in production); the PC
# ran each in chunks of 60 games with consecutive seeds (scripts: see below)
go run ./cmd/arena -seats grandmaster,expert,expert,expert -games 600 -parallel 22 -seed 30000000
go run ./cmd/arena -seats grandmaster,expert,expert,expert,expert -games 600 -parallel 22 -seed 31000000
go run ./cmd/arena -seats grandmaster,expert,hard,normal -games 600 -parallel 22 -seed 32000000
go run ./cmd/arena -seats grandmaster,hard,hard,hard -games 480 -parallel 22 -seed 33000000
go run ./cmd/arena -seats grandmaster,normal,normal,normal -games 480 -parallel 22 -seed 34000000
# A/B: no opponent style mix; the proposer played by the tuned heuristic
go run ./cmd/arena -seats grandmaster,expert,expert,expert -games 480 -parallel 22 -seed 30000000 -gm-nostyles
go run ./cmd/arena -seats grandmaster,expert,expert,expert -games 600 -parallel 22 -seed 30000000 -gm-sharp
go run ./cmd/gm-attack reanswer -in gm.jsonl -parallel 8 -gm-sharp      # quick screen of a variant
# timing as the server runs it (all cores per decision, 2.5 s cap), and its own price
go run ./cmd/arena -seats grandmaster,expert,expert,expert -games 8 -parallel 1 -workers 24 -budget 2.5s -seed 31
go run ./cmd/gm-attack speed -games 4 -parallel 8 -quotes 3 -workers 22
```

## Its own network (value net v2) and the self-play loop

`internal/valuenet2` is EXPERT's network (`internal/valuenet`, unchanged:
EXPERT keeps its embedded model) with:

* **8 player slots** (FeatureVersion 2), so every game the engine allows,
  2 to 8 players, is read from the first turn;
* three more inputs per player: the cash it could raise (mortgaging every
  deed and selling its buildings at half), the dearest rent it could land
  on, and how many deeds it owns; owner one-hots of 9 (bank + 8);
* a larger default size, 888 → 256 → 128 → 1 (EXPERT: 690 → 128 → 64 → 1);
  an evaluation of a 4-player position costs about twice EXPERT's;
* training records that may carry a **search-improved target** next to the
  winner: every player's win probability after short rollouts from the
  position, scored by the current best network (`hardai.SearchValue`). The
  training target mixes the two (`-target-mix`, default ½);
* EXPERT's datasets convert to it (`valuenet2.FromV1`), so the 22M positions
  recorded for EXPERT train it too.

`estate-ml gm` runs the loop, one generation at a time, each stage
resumable:

1. **League games** (`data/gm<k>`): each game draws 2-8 players (mostly 4)
   and house rules, and every seat from a league: Grandmaster with the
   current best network (30%, fewer rollouts for speed), Expert (20%), Hard
   (8%), the tuned heuristic (12%), Normal (12%), the trader, hoarder and
   over-builder styles (12%) and Easy (6%). 30% of turn starts are recorded,
   half of them with a search target.
2. **Training** (`net/gm<k>`): EXPERT's converted datasets (all of them in
   generation 1, a quarter later) plus the league data of the last three
   generations; generation 1 from random weights, later ones fine-tuned from
   the best network. The epoch with the lowest validation loss is kept.
3. **Gate**: Grandmaster with the new network against Grandmaster with the
   best one, plus two Experts, seats rotated (400 games by default). The new
   network replaces the best only if it wins clearly more of the games the
   two of them win (one-sided z ≥ 1).

State: `gm/state.json`; dashboard: `status/gm.json` (the GRANDMASTER card of
`mldash`), `status/gm.jsonl` (one line per generation), the usual
`status/run.json`, `events.jsonl`, `train.jsonl` and the ledger.

### The PC run (started 4 November 2026, 01:15)

`estate-ml gm -dir ~/estate-ml/gm -gens 4 -games 8000 -workers 22`, under
`nice -n 10`, started by `scripts/ml/gm-queue.sh` after the Phase 1
measurements:

| step | per generation | cores | generation 1, measured |
|---|---|---:|---|
| league games (8,000; 30% of turn starts recorded, half with a search target) | data/gm<k> | 22 | 382,888 positions (191,559 with a search target) in 2 h 48 min, about 1.05 games/s when alone on the PC |
| training (generation 1: EXPERT's converted positions + league data, 3 epochs; later: a quarter of them + the last 3 generations, 2 epochs) | net/gm<k> | 22 | 22.1M training and 1.17M validation positions; under 4 min per epoch (about 100,000 positions/s) |
| gate (400 games: GM new vs GM best vs 2 Expert) | gm/gate-<k>.jsonl | 22 | 10 min |

Generation 1's network: validation log loss 0.707 (uniform 1.230), the
winner picked 69.2%, Brier 0.109, after 3 epochs (0.710, 0.713, 0.707).
These are on its own validation set, so they do not compare directly with
EXPERT's network (0.707 on EXPERT's data); the gate decides. **It passed
its gate:** in 400 games of GRANDMASTER with it, GRANDMASTER with EXPERT's
network and two EXPERTs, the new network's GRANDMASTER won 146 and the
old one's 124: 54.1% (48.1–59.9) of their wins, z = 1.34. The gate's bar
(z ≥ 1) is lenient on purpose, so the loop can climb; before a network is
shipped in `internal/valuenet2/models`, a larger arena against three
EXPERTs (compare 37.3% with EXPERT's network) should confirm it.

Memory: the training step holds about 19 GB (EXPERT's 22.9M positions read
and converted in memory); disk: about 60 MB of league data per generation.
Generation 1 took 3 h 10 min (its league games shared the PC with other
measurements for an hour); the 4 should take about 10-11 hours.

### Running the loop on the training machine

As run in November 2026, everything lives under `~/estate-ml/gm/` (`bin`,
`arena`, `attack`, `data`, `net`, `logs`), with `gm/status`, `gm/ledger` and
`gm/data/{tune,gen1,gen2}` symlinked to EXPERT's `~/estate-ml/status`,
`ledger` and datasets. `scripts/ml/gm-queue.sh` runs the Phase 1
measurements and then the loop (22 games or workers at a time, `nice -n 10`,
pausing above 90°C); `scripts/ml/gm.sh` runs only the loop, from
`~/estate-ml`.

```sh
# build (any machine) and copy to the training machine
GOOS=linux GOARCH=amd64 go build -trimpath -o estate-ml ./cmd/estate-ml
GOOS=linux GOARCH=amd64 go build -trimpath -o arena ./cmd/arena
GOOS=linux GOARCH=amd64 go build -trimpath -o gm-attack ./cmd/gm-attack
GOOS=linux GOARCH=amd64 go build -trimpath -o mldash ./cmd/mldash
scp estate-ml arena gm-attack scripts/ml/gm-queue.sh <training-host>:~/estate-ml/gm/bin/
scp mldash <training-host>:~/estate-ml/bin/
# on the training machine: start detached; the same command resumes after a stop or a reboot
setsid nohup sh ~/estate-ml/gm/bin/gm-queue.sh > ~/estate-ml/gm/logs/queue.log 2>&1 < /dev/null &
# watch: the dashboard on :3299 (GRANDMASTER card: status/grandmaster.json
# and status/gm.json), or
tail -f ~/estate-ml/gm/logs/gm.log
# stop: kill gm-queue.sh and the arena / gm-attack / estate-ml gm process;
# nothing is lost but the arena chunk, shard, epoch or gate game in progress
```

Ship a generation's network that passed its gate:
`cp ~/estate-ml/gm/net/gm<k>/best.bin internal/valuenet2/models/gm.bin`, then
`go test ./...` (Grandmaster picks it up through `valuenet2.Default`).

## What is still open, and what is a guess

* **People are not bots.** The attacker plays every deal shape on a price
  ladder; a person plays fewer, but chooses them better. Rates are per deal
  the bot accepted, and per position (does the position offer at least one
  bad deal), not per game: how many games a person wins with them is not
  measured. Game records (docs/GAME-RECORDS.md) will show what people do.
* **The oracle is a model too.** It plays the tuned heuristic for everyone.
  Its verdicts agree with longer rollouts (full heuristic trading, 24
  rounds) on 87% of deals, and every worst case survived a whole-game
  re-judging, but the 2-point line is a convention, and a verdict has about
  1.4 points of noise, so deals near the line are uncertain.
* **The trade screen's quote for HARD and EXPERT is still the Normal
  heuristic's.** For a Grandmaster seat the server now asks Grandmaster for
  its price and honours that; HARD and EXPERT still honour the Normal
  heuristic's price, which is their biggest hole (finding 1).
* **Grandmaster's own judgement is still exploitable on ordinary deals.**
  Deeper rollouts fixed group deals and probing, but one in eight ordinary
  deals it accepts on a ladder still costs it 2 points by the oracle (12.6%),
  and 40% of positions offer one: the attacker selects exactly the deals
  its estimate gets wrong. Part of the gap may be the opponent model: its
  rollouts play the proposer with a random style (often weaker than the
  oracle's tuned heuristic), so it can underrate what the proposer does
  with the deed.
* **Opponent styles, margins and the rival weight were chosen, not tuned.**
