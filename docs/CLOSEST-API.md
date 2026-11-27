# Near Thing: game API

The fifth game type on the Estate party server, after Estate, Texas hold'em, Ludo and the quiz. It is a
chill, slow-paced party quiz: everyone guesses a number, the guesses become a row of betting slots, and
everyone bets on whose guess is closest. Its name is **Near Thing** (the code and the wire call it
`"closest"`). Keep the display name in one constant on each side (`closest.Title` in Go, `GAME_NAME`
in `web/shared/closest-name.js`) so a rename is one line each.

Keep names, words and looks original (see [CONTRIBUTING](../CONTRIBUTING.md)), including in text that
reaches a browser; `web/tests/closest.check.mjs` checks the screens for a short list of words to avoid.

This file is the contract between the rules engine (`internal/closest`), the server
(`internal/server/closest.go`, `closest_adapter.go`) and the screens (`web/table/closest-table.js`,
`web/phone/closest-app.js`, `closest-screens.js`, `web/shared/closest-*.js`, `web/shared/closest.css`).
Designed in November 2026 from a written plan and a review with a scoring simulation.

## 0. The game in one paragraph

Each question has a whole-number answer ("In what year did the first person reach the South Pole?").
Everyone types a guess on their phone's number pad. The table screen lines the guesses up low to high as
**slots**; the middle slots pay 2× and each step outward pays one more, up to 5×. Everyone places
**2 free chips** on any slots, in secret, and may **stake 1–3 of their points** on one of them. The
chips are shown, then the answer slides along the line. The **closest guess** (over or under) wins: its
owner scores +3 (+5 if exact) and chips on it pay its odds. The **second closest** also pays: its owner
+1, chips on it 1 each. The last question pays double. Quick is 7 questions (~8–11 min), Full is 12.

## 1. Transport

As for the quiz (`docs/QUIZ-API.md` §1) unless stated:

- **Create:** `POST /api/games {name, tokenKind?, gameType: "closest"}` (phone, becomes host player) or
  `POST /api/tables {gameType: "closest"}` (PC table screen).
- **Join:** lobby only. Reclaim and rejoin work mid-game.
- **Phones only.** Guesses and bets are secret, so `POST /seats` answers **400**
  `"Near Thing needs a phone per player"` (built from `closest.Title`). The table screen is a viewer
  and may send the host actions `start` and `settings` with `{tableToken, action}`.
- **Act:** `POST /api/rooms/{code}/action {token, action}` or over the room WebSocket
  (`docs/QUIZ-API.md` §1.1, `{"type":"action",…}` → `actionResult`).
- **No clock sync and no latency feed.** Nothing here is timed to the millisecond; phones and the table
  ping at the normal rate. The engine is told who is online (for §4 timeouts).
- **Computer seats:** `POST /api/rooms/{code}/ai {token, difficulty}`, levels `["easy","normal","hard"]`.
- **End Game, rematch, room settings, games list, friends, invites, presence, persistence:** as for the
  quiz. Rematch keeps players, order, host and settings, and does not repeat questions this room has
  already played (the room remembers played question ids, persisted with the room).
- **No stand-in.** `players[].autopilot` is always false.
- **Game records:** recorded as the event stream (like the quiz). The `reveal` event carries every guess
  and every bet (§5), so real play can re-tune the odds and the bots later.

## 2. Actions

| action | who | when | effect |
|---|---|---|---|
| `{"type":"start"}` | host | lobby, 1–8 players | Fills computers (§2.1 `fill`), deals the questions (§7), starts the intro. |
| `{"type":"settings","settings":{…}}` | host | lobby | Partial update of §2.1; unknown keys ignored; bad values refused. |
| `{"type":"guess","q":S,"value":N}` | playing player | `phase=guess` | `S` = `question.seq`; `N` a whole number within `question.min`–`question.max`. A second `guess` replaces the first (one change allowed). |
| `{"type":"bet","q":S,"chips":[A,B],"stake":N,"stakeSlot":T}` | playing player | `phase=bet` | `A`, `B` slot indexes (may be equal); `N` 0–`you.stakeMax`; `T` must be `A` or `B` when `N > 0` (ignored when 0). Once. |

Errors (`400 {"error": …}`), plain English: `"guesses are closed"`, `"guess between 10 and 9,000"`
(numbers formatted as the question shows them: a year question says `"guess between 1000 and 2025"`),
`"you have already changed your guess"`,
`"bets are closed"`, `"you have already bet"`, `"pick two slots"`, `"stake between 0 and 3"`,
`"put the stake on one of your chips"`, `"that question is over"` (stale `q`). Starting with no question
packs answers `"there are no questions to ask yet"`; with too few, `"the question packs have only N
questions; a game needs M"`.

### 2.1 Settings (`state.settings`)

| key | default | values | meaning |
|---|---|---|---|
| `length` | `"quick"` | `quick`, `full` | 7 or 12 questions. |
| `guessSeconds` | `30` | `20`, `30`, `45` | Time to guess. |
| `betSeconds` | `25` | `15`, `25`, `35` | Time to bet. |
| `fill` | `true` | `true`, `false` | At `start`, add Normal computers until there are 4 players (named and given tokens as computer seats added in the lobby are; a rematch keeps them as ordinary computer seats). Two lone players get two slots that both pay 2×, which makes betting meaningless (review §2.2). The lobby says so: "2 computers will join so there are 4 guesses". |

## 3. State

Every viewer gets the same public game; `you`, the viewer fields and **the viewer's own secrets** (their
guess before the line-up, their bet before the chips are shown) differ.

**Never in any state before its reveal, for any viewer (table, player, spectator):** another player's
guess (before `lineup`); another player's chips or stake (before `chips`); the answer, the fact and the
sources (before `reveal`); any question not yet asked. A named server test checks every phase for every
viewer, like `internal/quiz/secrecy_test.go`.

### 3.1 Top level

| field | type | notes |
|---|---|---|
| `game` | `"closest"` | Clients pick the screen by this. |
| `title` | string | `closest.Title` (the display name). |
| `code`, `status`, `hostId`, `version` | | `status`: `lobby` · `playing` · `finished`. |
| `phase` | string | §4. |
| `now` | ms | Server clock. |
| `phaseEndsAt` | ms | When the current phase ends by timer (0 when none). |
| `settings` | §2.1 | |
| `seatLimit` | int | 8. |
| `questionNo` | int | 0 in the lobby and intro, then 1…`questionCount`. |
| `questionCount` | int | 7 or 12. |
| `question` | §3.2 | The current question, else null. When finished, the last question if it was revealed (null after END GAME cut a question short). |
| `players` | §3.4[] | Join order. |
| `you` | §3.5 | A seated player's own state, else null. |
| `standings` | []? | When finished: `{id, name, place, score, closest, left?}` by place; null before. |
| `winnerIds` | id[] | When finished: everyone in 1st place. |
| `credits` | string[] | Attribution lines for the packs in use. |
| `log`, `lastEventSeq` | | Plain-English lines; the last engine event. |
| viewer fields | | `viewerId`, `viewerRole`, `viewerName`, `spectatorId`, `viewerIsHost`, `spectators`, as for the quiz. |

Server-added fields as for the quiz: `gameType: "closest"`, `aiLevels`, `roomSettings`, `claims`, and per
player `input`, `tokenKind`, `color`, `online`, `difficulty`, `autopilot` (false).

### 3.2 `question`

| field | when | notes |
|---|---|---|
| `seq` | always | New number per question; actions quote it. |
| `no`, `double` | always | Its number; `double` true on the last question (everything but stakes pays ×2). |
| `text` | from `guess` | ≤ 120 characters. |
| `unit` | from `guess` | Shown beside the pad and on every slot ("" for none; years have none). |
| `format` | from `guess` | `plain` (thousands separators: 12,500) or `year` (no separator: 1969). |
| `min`, `max` | from `guess` | Allowed guesses. |
| `topic` | from `guess` | §7. |
| `guessed` | `guess` | Ids who have guessed (no values). |
| `slots` | from `lineup` | §3.3, low to high. |
| `betsIn` | `bet` | Ids who have bet (nothing else). |
| `answer`, `fact`, `src` | `reveal` | The answer, a one-line fact (≤ 140 characters), the source URLs. |
| `results` | `reveal` | `[{playerId, guess, slot, bonus, chipPoints, stakePoints, delta, before, after}]`, every player playing this question (`guess`/`slot` null when they didn't guess). |
| `nobody` | `reveal` | true when nobody guessed. |

### 3.3 `slots[]`

`{index, value, ownerIds, odds}` from `lineup`; plus `chips: [{playerId, count, stake}]` from `chips`
(count 1 or 2; `stake` 0–3, set on the slot the stake went on; in bet order; `[]` on a slot nobody chose);
plus `off` (|value − answer|) and
`result` (`"win"`, `"second"` or `""`) from `reveal`.

- Equal guesses share one slot; `ownerIds` in guess order.
- **Odds ladder** for `k` slots, slot `i` (0 = lowest): `odds = min(5, 2 + floor(|i − (k−1)/2|))`.
  So 1 slot: 2; 2: 2 2; 4: 3 2 2 3; 5: 4 3 2 3 4; 8: 5 4 3 2 2 3 4 5.

### 3.4 `players[]`

`{id, name, kind, status, score, closest, place?, guessed, betIn}` plus server fields. `status`:
`waiting` · `playing` · `left`. `closest`: questions where they owned a winning slot (the tie-break).
`guessed` / `betIn`: done in the current phase (no values). Scores never go below 0.

### 3.5 `you`

`{id, canGuess, guess, changesLeft, canBet, chips, stake, stakeSlot, stakeMax}`: `canGuess` true in `guess`
while you may still guess or change it; `guess` your guess this question or null; `changesLeft` 1 before your first change, then 0; `chips` `[A,B]` once bet, else null;
`stakeMax` = min(3, your score) (0 when your score is 0: no stake offered). Your own guess and bet stay
visible to you after you leave and after the game.

## 4. Phases and timers

`lobby` → `intro` → (`guess` → `lineup` → `bet` → `chips` → `reveal`) × questions → `finished`.

| phase | ends | time |
|---|---|---|
| `intro` | timer | 4 s (title, length, "the last question pays double") |
| `guess` | every playing player has guessed; or timer | `guessSeconds`. **Hurry-up:** when at least 2 are playing, all but one have guessed and more than 10 s remain, the timer drops to 10 s (a computer still to act then acts at least 2 s before the new end). |
| `lineup` | timer | `clamp(1500 + 600 × slots, 3000, 6500)` ms; screens bring the slots in one at a time, low to high, over the phase |
| `bet` | every playing player has bet; or timer | `betSeconds`, with the same hurry-up |
| `chips` | timer | 3.5 s (every chip lands at once) |
| `reveal` | timer | 8 s (9 s on the last question): the answer marker slides in, "off by N" on each slot, then the wins and payouts, and the fact |

**Skips.** Nobody guessed: `guess` → `reveal` (answer and fact, `nobody: true`, no points). Exactly one
slot (one guesser, or everyone the same): `guess` → `lineup` → `reveal` (no betting; the slot wins).

**Timeouts.** No guess: no slot, but they still bet. No bet: if they guessed this question and are
online, both chips go on their own slot (stake 0); otherwise their chips are not placed. (The phone
starts with both chips on your own guess, so one tap confirms.)

**Leaving.** A player who leaves mid-question keeps their slot on the line for that question (others may
have bet on it and are paid as usual) but gets no owner bonus and no `closest`, and their chips and stake
are void (no payout, no loss); they are out from the next question, and are placed below everyone who
stayed. When no person is still playing, the game ends on current scores (computers do not play on alone).

**Late timers.** A timer that falls due while nothing runs it (nobody connected, so the room waits, or a
restart) runs when the room wakes, and the next phase starts from then: the game never races through
phases to catch up.

## 4.1 Scoring

For each question, `m` = 2 on the last question, else 1. `d(slot) = |value − answer|`.

- **Winning slots:** those with the smallest `d` (two slots equally close both win).
- **Second-closest slots:** those with the next smallest `d`, **only when exactly one slot wins**.
- **Owner bonus:** each owner (still playing) of a winning slot `+3 × m`, or `+5 × m` when `d = 0`;
  each owner of a second-closest slot `+1 × m`.
- **Free chips:** never lost. Each chip on a winning slot pays `odds × m`; on a second-closest slot `1 × m`.
- **Stake:** `s` (1–3, at most your score when you bet) on one slot holding one of your chips. If that
  slot wins: `+s × odds` (**not** doubled). Otherwise (second-closest included): `−s`.
- **Final places:** score; then most `closest`; still level = shared place.

Example: guesses 52, 76, 90, 120 → odds 3 2 2 3. Answer 88: 90 wins (off by 2): its owner +3, chips on
it pay 2 each; 76 is second (off by 12): its owner +1, chips on it pay 1 each. A player with both chips
on 90 and a stake of 3 on it scores 2 + 2 + 6 = 10.

## 5. Events

Sent on the WebSocket before their state, each with its own event `seq` (so the question's seq is `q`):
`join`, `leave` (`playerId`), `start`, `question` (`q`, `no`), `guessed` (`playerId`; no value), `lineup`
(`q`, `slots`: count), `betIn` (`playerId`), `chips` (`q`), `reveal` (`q`, `questionId`, `answer`,
`winners` and `second`: slot indexes, `nobody`, and for the record `guesses: [{playerId, value, afterMs,
changed}]` in guess order and `bets: [{playerId, chips, stake, stakeSlot, auto}]`, where `afterMs` is the
time from the question opening to the final guess and `auto` marks chips the bet timer placed),
`gameOver` (`winnerIds`). No event carries a guess, a bet or the answer before `reveal`.

## 6. Computer players

Computers live in the engine (seeded, reproducible). They never read the answer when **betting**; a
test checks that betting gives the same result with the answer changed.

**Guessing.** Each level has a width `w` (tuned with the arena, below): Easy 1.6, Normal 1.2, Hard 0.9. Per question, draw one
**shared** error `zs ~ N(0,1)` that all computers use, and per computer `zi ~ N(0,1)`; `z = 0.6·zs + 0.8·zi`.
- `plain`: `guess = answer × exp(z × w × ln(1 + spread/100))`.
- `year`: `guess = answer + z × w × spread`.
- **Wild:** with chance Easy .08, Normal .05, Hard .03, `w` is tripled for that guess.
- **Knows it:** with chance Easy .03, Normal .06, Hard .10, the guess is the answer.
- **Human-looking:** `plain` rounds to 2 significant figures (Easy and Normal: 1 significant figure
  a third of the time); `year` rounds to a multiple of 10 (Easy, half the time) or 5 (Normal, a quarter
  of the time). A `plain` guess past the range becomes the round number just inside it (9,900,000, not
  9,999,999); then clamp to `min`–`max`.

**Betting** (belief: the truth is spread around a centre made from the computer's own guess and the
table's median guess, 75/25 for Normal and 50/50 for Hard (the median alone when it did not guess), with
its width; win and second chances per slot from 400 samples):
- **Easy:** both chips on its own slot (no guess: the middle slot). Never stakes.
- **Normal:** one chip on its own slot, one on its best expected payout (`p_win × odds + p_second`).
  Stakes 1 when its best pick's win chance is over 0.5.
- **Hard:** both chips on its best expected payout, but splits (one on the most likely slot) when the
  best pick's win chance is under 0.25. Stakes on whichever of its chips' slots has the bigger edge
  `p_win × (odds + 1) − 1`: 1 point over 0.3, 2 over 0.6, 3 over 1 (never more than its score).

**Timing:** guesses 4–12 s after the question opens, bets 3–8 s after the bet opens, always at least 2 s
before the deadline (the hurry-up pulls a late plan in).

**Tuning** (`go run ./cmd/closest-arena`, copied from `cmd/quiz-arena`): a modelled average person
should win about 25–35% of 4-player Quick games against three Normal computers (a fair share is 25%),
and Hard should beat Normal clearly. The person is the arena-only level `person` (never offered in
rooms): width 1.1, rounding, betting and staking as Normal. Its width is fixed, so tuning a computer
never moves the yardstick.

Measured 22 Nov 2026 on the real packs (`go run ./cmd/closest-arena`, 175 questions, 4000 games per
line, seed 1, one A against three B, a shared first place split). The first run, on the 24 made-up test
questions, gave the same picture (person v Normal 29.1%, Hard v Normal 53.5%).

| A v 3 × B | Quick: A wins | 95% | A's average score v B's |
|---|---|---|---|
| person v normal | **29.8%** | ±1.4 | 27.8 v 25.4 |
| hard v normal | **54.3%** | ±1.5 | 37.8 v 23.1 |
| easy v normal | 10.9% | ±1.0 | 19.3 v 28.2 |
| person v hard | 11.0% | ±1.0 | 20.2 v 30.6 |
| person v easy | 54.1% | ±1.5 | 35.5 v 22.2 |

Computer-only Quick games take 3.9 minutes on average (Full 6.6): computers act within seconds, so the
timers rarely run out; with people the timers set the pace.

## 7. Questions and dealing

**Packs** live in `internal/closest/packs/*.json` (embedded in the binary, **never under `web/`**).
`internal/closest/packs/README.md` documents the format:

```json
{
  "id": "closest-1",
  "title": "Near Thing 1",
  "version": 1,
  "license": "own",
  "attribution": "",
  "questions": [
    {
      "id": "closest-1/001",
      "topic": "history",
      "text": "In what year did the first person reach the South Pole?",
      "answer": 1911,
      "unit": "",
      "format": "year",
      "min": 1000,
      "max": 2025,
      "spread": 40,
      "fact": "Roald Amundsen's team reached it on 14 December 1911.",
      "src": ["https://…", "https://…"]
    }
  ]
}
```

Rules, checked by a Go test over every pack (and `scripts/closest-pack/validate.mjs`):
- `license`: `own` or `CC0` (`attribution` needed for anything else, as for the quiz).
- `text` ≤ 120 characters, ends with `?`; `fact` ≤ 140 characters; `unit` ≤ 16 characters.
- `answer` a whole number, 10 ≤ answer ≤ 999,999 (2–6 digits; ask "in millions" or "in thousands"
  instead of bigger numbers, with the unit saying so). `format` `plain` or `year`; a `year` answer is
  1000–2025 and its `unit` is "".
- `min` < `answer` < `max`, `min` ≥ 0, `max` ≤ 9,999,999; the range is wide enough that honest wrong
  guesses fit (about ×10 either side for `plain`, but never silly). Checked for `plain`: `min` ≤
  `answer` / 2 and `max` ≥ 2 × `answer`.
- `spread`: `year` 1–300 (typical error in years); `plain` 5–300 (typical error, % of the answer).
- `src`: at least 2 independent sources that agree, or 1 if it is the defining source and the
  question has `"defining": true`.
- `topic` one of the quiz's 15: `geography`, `science`, `space`, `nature`, `history`, `people`,
  `sport`, `music`, `screen`, `books`, `art`, `food`, `words`, `numbers`, `inventions`.
- Ids unique across all packs. `year` questions at most a third of each pack.

**Good questions:** nobody knows it exactly, everyone can reason toward it; honest guesses within about
10× of each other; fixed by history or by definition. **Bad:** facts people know exactly (piano keys,
bones, chess squares, and the number answers already in the board quiz packs); answers under 10;
astronomical numbers; anything that drifts (populations, prices, records, "tallest", "most"); disputed
or re-measured values (river lengths, mountain heights, counts of an author's works); trick wording; a
unit missing from the question.

**Dealing:** at `start`, `questionCount` questions, at most 2 from any topic in a Quick game (3 in
Full), at most 2 `year` questions in Quick (4 in Full), never a question this room has played (when the
pool runs low, the least recently played come back). A question counts as played once it is asked (one
dealt but cut off by END GAME is not), and the room's list is saved with it.

## 8. Screens

Designs come first (2–3 options each for the pad, the line, the bet screen and the reveal);
the screens follow the pick. Fixed either way:

- Estate's look (Oswald and Inter, `web/shared/tokens.*`), the quiz's plates and timer styles; keep it
  original (see CONTRIBUTING).
- **Table screen** on the 1600 × 900 stage (×1.2 at 1080p): question text ≥ 44 stage px, slot numbers
  ≥ 36, nothing under 20. Up to 8 slots across, each with its owners' tokens, odds, and later the chips
  (one disc per player in their colour, ×2 badge for both chips, stake shown) and "off by N". Slots are
  **evenly spaced**, never a true-scale number line. States apply at once; animations run within the
  phase time.
- **Phone**, from 360 × 740, nothing under 14 px: our own number pad (0–9, ⌫, 000, no system keyboard),
  live separators (none for years), the unit and the number in words above the keys ("3 million 200
  thousand"), numbers outside `min`–`max` refused, LOCK IN as a separate big button; the bet screen with
  every slot as a row ≥ 56 px, both chips starting on your own guess, the stake stepper (0–3, "you could
  lose 3") and CONFIRM.
- **Plate tags:** GUESSED, BET IN, AWAY.
- **Sound:** lock-in tick, slot pop, chips land, answer slide, win, the last 5 s ticking; our own music.
- **Fixtures** (`web/fixtures/closest/`, written by `ESTATE_CLOSEST_FIXTURES=web/fixtures/closest go test
  ./internal/server -run TestClosestFixtures`): `closest-lobby-table`, `closest-lobby-phone`,
  `closest-intro-table`, `closest-guess-table`, `closest-guess-phone`, `closest-guess-year-phone`,
  `closest-guessed-phone`, `closest-lineup-table`, `closest-lineup-phone`, `closest-bet-table`,
  `closest-bet-phone`, `closest-bet-8-phone`, `closest-betin-phone`, `closest-chips-table`,
  `closest-reveal-table`, `closest-reveal-phone`, `closest-reveal-tie-table`,
  `closest-reveal-nobody-table`, `closest-reveal-oneslot-table`, `closest-last-guess-table`,
  `closest-finished-table`, `closest-finished-phone`, and the worst cases `closest-lineup-big-table`
  (8 different 7-digit guesses), `closest-chips-pile-table` (all 8 players on one slot, stakes on).
  Fixtures must not hold real pack questions (they are served publicly): use made-up questions.
