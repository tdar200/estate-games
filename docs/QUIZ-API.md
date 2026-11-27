# Quiz: game API

The quiz for the Estate party server: the fourth game type, after Estate, Texas hold'em and Ludo. It is
a category-board quiz in the style of a TV general-knowledge show, with our own name, look, sound and
questions. The picker tile says **QUIZ** (a name is still to be chosen; never use the names of real
shows, their round names or their look anywhere, including text that reaches a browser).

This file is the contract between the rules engine (`internal/quiz`), the server
(`internal/server/quiz.go`, `quiz_adapter.go`) and the screens (`web/table/quiz-table.js`,
`web/phone/quiz-app.js`, `quiz-screens.js`, `web/shared/quiz-*.js`), designed in November 2026.

## 0. The game in one paragraph

A board of 6 categories × 5 clues (100–500 points; a Full game adds a second board at 200–1000). The
player in control picks a clue on their phone. Everyone reads the clue on the big screen and their
phone; when the reading bar ends, four choices appear on **every** phone at the same moment. The
**first correct tap** wins the clue's points and control. A **wrong tap** loses the points and locks
that player out of the clue. Hidden **gold squares** let the player who finds them bet before seeing
the clue, alone. A **final round** has everyone bet secretly and answer one last clue. Highest score wins.

## 1. Transport

As for Ludo (`docs/LUDO-API.md` §1) unless stated:

- **Create:** `POST /api/games {name, tokenKind?, gameType: "quiz"}` (phone, becomes host player) or
  `POST /api/tables {gameType: "quiz"}` (PC table screen).
- **Join:** `POST /api/rooms/{code}/join`, **lobby only**. Reclaim and rejoin work mid-game.
- **Phones only.** Final bets are secret, so `POST /seats` (keyboard/controller) answers **400**
  `"the quiz needs a phone per player"`. The table screen is a viewer and may send the host actions
  `start` and `settings` with `{tableToken, action}`.
- **Act:** `POST /api/rooms/{code}/action {token, action}` **or** over the room WebSocket (§1.1).
- **Computer seats:** `POST /api/rooms/{code}/ai {token, difficulty}`, levels `["easy","normal","hard"]`.
- **End Game, rematch, room settings, games list, friends, invites, presence, persistence:** as for
  Ludo. Rematch keeps players, order, host and settings, and **does not repeat categories** this room
  has already played (the room remembers played category ids, persisted with the room).
- **No stand-in.** An away person simply does not answer (no penalty); an away chooser is covered by
  the pick timer. `players[].autopilot` is always false for quiz rooms.
- `trade-quote`, `not-selling`: Estate-only (400).
- **Game records:** quiz games are recorded as their event stream (like poker and Ludo), including
  every tap with its timing (§6), for tuning bots and difficulty later.

### 1.1 WebSocket additions (all game types; old clients keep working)

- **Ping with clock sync.** Client → `{"type":"ping","t":<client Date.now()>,"rtt":<ms>?}`. Server →
  `{"type":"pong","t":<echo of t>,"now":<server ms>}`. A bare `{"type":"ping"}` still gets a pong
  (with `now`, without `t`). `rtt` is the client's best (lowest) round trip of its last 8 samples.
- **Clock offset on the client:** for each pong, `rtt = receiveTime − t`,
  `offset = now + rtt/2 − receiveTime`; keep the offset of the lowest-`rtt` sample among the last 8.
  Synced server time = `Date.now() + offset`. While a quiz room is `playing`, the phone and the table
  screen ping every **2 s** (15 s otherwise).
- **The server keeps each player's latency:** the minimum `rtt` the player's socket reported over its
  last 8 pings; unknown (no socket, or no `rtt` yet) counts as **250 ms**. The quiz engine is told each
  player's latency and whether they are online (§6).
- **Actions over the socket.** Client → `{"type":"action","action":{…},"id":<client counter>}`
  (plus `seatId` where `POST …/action` takes one: a table's local seats in other games).
  The server runs it exactly like `POST …/action` with the socket's own token (same checks, same
  per-address rate limit), and answers `{"type":"actionResult","id":…,"ok":true}` or
  `{"type":"actionResult","id":…,"ok":false,"error":"<message>"}` (`id` is null when none was sent).
  (Not `"type":"error"`: clients treat that as a fatal session error.) The new state still arrives as
  a normal `state` message, before the `actionResult`. Clients fall back to `POST` when the socket is
  not open.
- The server strips any client-sent field whose name starts with `_` from an action before it reaches
  the engine. The engine gets each tap's arrival time and the player's latency from the server
  directly, never from fields of the action.

## 2. Actions

| action | who | when | effect |
|---|---|---|---|
| `{"type":"start"}` | host | lobby, 1–8 players (people or computers) | Deals the boards (§7) and starts the intro. |
| `{"type":"settings","settings":{…}}` | host | lobby | Partial update of §2.1; unknown keys ignored; bad values refused. |
| `{"type":"pick","category":C,"row":R}` | chooser | `phase=pick` | `C` 0–5, `R` 0–4 (row 0 = cheapest), an open cell. |
| `{"type":"bet","amount":N}` | gold finder, or anyone in the final | `phase=bet` / `finalBet` | Integer within `you.betMin`–`you.betMax`. Once (final: once each). |
| `{"type":"answer","clue":S,"choice":K,"ms":M}` | eligible player | `phase=answer` / `finalAnswer` | `S` = `clue.seq` (or `final.seq`), `K` 0–3, `M` = milliseconds from the choices appearing on that phone to the tap (§6). Once per clue. |

Errors (`400 {"error": …}`), plain English: `"it is not your pick"`, `"that clue has been played"`,
`"bet between 5 and 900"`, `"you have already answered"`, `"answers are not open yet"`,
`"that clue is over"`, `"only the gold square finder answers this one"`. (A wrong tap locks you out
because each player taps once per clue: a second tap is `"you have already answered"`.)

### 2.1 Settings (`state.settings`)

| key | default | values | meaning |
|---|---|---|---|
| `length` | `"quick"` | `quick`, `full` | Quick: one board + final (~12 min). Full: two boards + final (~22 min). |
| `readSpeed` | `"normal"` | `slow`, `normal`, `fast` | Scales reading time ×1.4 / ×1.0 / ×0.75. |
| `answerSeconds` | `8` | `6`, `8`, `12` | How long the choices stay open on a board clue. |

## 3. State

Every viewer gets the same public game; `you`, the viewer fields and **the viewer's own secrets**
(their choice before the reveal, their final bet before the final reveal) differ.

**Never in any state before its reveal:** the right answer's index (or text) of the current clue or
the final; where unfound gold squares are; any clue not yet picked; another player's choice; another
player's final bet. A named server test checks every phase's state for each viewer (table, each
player, spectator). Choices are shuffled on the server per clue.

### 3.1 Top level

| field | type | notes |
|---|---|---|
| `game` | `"quiz"` | Clients pick the screen by this. |
| `code`, `status`, `hostId`, `version` | | `status`: `lobby` · `playing` · `finished`. |
| `phase` | string | §4. |
| `now` | ms | Server clock (clients still sync by ping, §1.1). |
| `phaseEndsAt` | ms | When the current phase ends by timer (0 when it waits on nobody's timer). |
| `settings` | §2.1 | |
| `seatLimit` | int | 8. |
| `round` | int | 0 lobby, 1, 2 (Full only), 3 = final. |
| `rounds` | int | Board rounds in this game: 1 (quick) or 2 (full). |
| `board` | §3.2 | The current board (null in lobby and during the final). |
| `chooserId` | id | Who has control (picks next). |
| `clue` | §3.3 | The clue being played on the board, else null. |
| `bet` | §3.4 | The gold square bet, while one is being made or played, else null. |
| `final` | §3.5 | The final round, from `finalIntro` on, else null. |
| `players` | §3.6[] | Join order. |
| `you` | §3.7 | A seated player's own state, else null. |
| `standings` | []? | When finished: `{id, name, place, score, correct, wrong, left?}` by place (ties share a place); null before. |
| `winnerIds` | id[] | When finished: everyone in 1st place. |
| `credits` | string[] | Attribution lines for the question packs this game uses (show them in help/credits). |
| `log`, `lastEventSeq` | | Plain-English lines; the last engine event. |
| viewer fields | | `viewerId`, `viewerRole`, `viewerName`, `spectatorId`, `viewerIsHost`, `spectators`, as for Ludo. |

Server-added fields as for Ludo (`docs/LUDO-API.md` §3.7): `gameType: "quiz"`, `aiLevels`,
`roomSettings`, `claims`, and per player `input`, `tokenKind`, `color`, `online`, `difficulty`,
`autopilot` (always false).

### 3.2 `board`

`{round, categories: [{index, title, topic, cells: [{row, value, played, gold?}]}]}` — 6 categories,
5 cells each, row 0 cheapest. `value` = (row+1)×100 in round 1, ×200 in round 2. `played` true once
picked. `gold: true` appears **only on a played cell** that was a gold square.

### 3.3 `clue`

| field | when | notes |
|---|---|---|
| `seq` | always | New number per clue played; actions quote it. |
| `category`, `categoryIndex`, `row`, `value` | always | `value` is the board value (for a gold square, the bet decides the stake: see `bet`). |
| `gold` | always | true for a gold square clue. |
| `text` | from `read` | The clue (≤ 140 characters). |
| `readAt`, `openAt`, `closesAt` | from `read` | Server ms: reading started; choices appear; choices close (`openAt + answerSeconds`). Clients show the choices at `openAt` **by synced clock**, not when a state arrives. |
| `choices` | from `read` | 4 strings (≤ 32 characters each), already shuffled. Sent with the reading so every phone can show them at `openAt` exactly; clients must keep them hidden until then. |
| `eligible` | from `read` | Player ids who may answer (everyone playing, or only the gold finder). |
| `answered` | from `open` | Ids who have tapped (no choices shown). |
| `answerIndex` | `reveal` only | The right choice. |
| `results` | `reveal` only | `[{playerId, choice, correct, delta, ms, late}]` for everyone who tapped, in time order; `ms` is the time the tap counted as (`effective`, §6.3); `late`: tapped after the winner (no score change). |
| `winnerId` | `reveal` only | Who won it ("" when nobody did). |

### 3.4 `bet` (gold square)

`{playerId, min, max, amount}`: `min` 5; `max` = the larger of the finder's score and the round's top
value (500 / 1000); `amount` 0 until placed, then public (everyone sees the bet, as on TV).

### 3.5 `final`

| field | when | notes |
|---|---|---|
| `seq`, `category` | from `finalIntro` | |
| `betsIn` | from `finalBet` | Ids who have bet (amounts secret). |
| `text`, `readAt`, `openAt`, `closesAt`, `choices` | from `finalRead` | As §3.3; 20 s to answer. |
| `answersIn` | from `finalAnswer` | Ids who have answered. |
| `answerIndex` | `finalReveal` | |
| `order` | `finalReveal` | Player ids in reveal order: lowest score first. |
| `revealed` | `finalReveal` | How many of `order` are revealed so far. |
| `results` | `finalReveal` | `[{playerId, bet, choice, correct, before, after}]` for the revealed players only. |

Everyone playing takes part, whatever their score. Bet range 0 to the larger of their score and 500.
No bet by the deadline = 0; no answer = wrong.

### 3.6 `players[]`

`{id, name, kind, status, score, correct, wrong, place?, isChooser, lockedIn}` plus server fields.
`status`: `waiting` (lobby) · `playing` · `left`. `lockedIn`: has tapped on the current clue, or bet /
answered in the current final step (no secrets). Scores may go below zero.

### 3.7 `you`

`{id, canPick, canAnswer, choice, canBet, betMin, betMax, bet, finalChoice, latencyMs}`: `canAnswer`
true from the reading on while you may still answer (the choices still show only at `openAt`);
`choice` your tap on the current clue (−1 none); `bet` your bet (gold or final) once placed, else null;
`finalChoice` your final answer (−1 none); `latencyMs` what the server believes your round trip is.
Your own choice and bets stay visible to you after you leave and after the game.

## 4. Phases and timers

`lobby` → `intro` → (`pick` → [`gold` → `bet`] → `read` → `answer` → `reveal`)×30 → `roundEnd` →
(Full: `intro` round 2 → … → `roundEnd`) → `finalIntro` → `finalBet` → `finalRead` → `finalAnswer` →
`finalReveal` → `finished`.

| phase | ends | default time |
|---|---|---|
| `intro` | timer | 3 s + 0.6 s per category title (round title, then the 6 titles one by one) |
| `pick` | chooser picks; timer auto-picks | 12 s; auto-pick = cheapest open cell in the last picked category of this board, else the cheapest open cell, leftmost |
| `gold` | timer | 2.5 s (the gold square reveal) |
| `bet` | finder bets; timer bets `min` | 15 s; computers bet after ~2 s |
| `read` | timer | `clamp(1500 + 300 × words, 3000, 8000)` ms × read speed; then the choices open at `openAt` |
| `answer` | first correct tap is decided; or every eligible player has tapped; or `closesAt` + grace | `answerSeconds` (§6 for deciding and grace) |
| `reveal` | timer | 3.5 s (4.5 s after a gold square or when nobody answered) |
| `roundEnd` | timer | 5 s (scores) |
| `finalIntro` | timer | 4 s (the category) |
| `finalBet` | everyone playing has bet; timer bets 0 for the rest | 30 s |
| `finalRead` | timer | as `read` |
| `finalAnswer` | everyone has answered; or `closesAt` + grace | 20 s from `openAt` (the grace as §6.4) |
| `finalReveal` | timer | 3 s per player, then 2 s (each next reveal is timed from the last one shown, so a late timer never reveals several at once) |

**Control.** Round 1: a random player picks first. Round 2: the lowest score picks first (ties: earliest
seat). After a clue, control goes to whoever won it; if nobody did, the chooser keeps it. A chooser
who leaves hands control to the highest score still playing.

**Scoring.** Board clue: right first = +value; a wrong tap counted against you = −value (§6). Gold
square: right = +bet, wrong or no answer = −bet; nobody else may answer it. Final: right = +bet,
wrong or no answer = −bet.

## 5. Events

Sent on the WebSocket before their state, each with a `seq` (screens animate from states, so a missed
event never desynchronises them): `join`, `leave`, `start`, `intro` (`round`; 3 for the final's
category), `pick` (`playerId`, `category`, `row`, `value`), `gold` (`playerId`), `bet` (`playerId`,
`amount`), `open` (`clue`: the clue's or the final's `seq`), `tap` (`playerId`; no choice), `right`
(`playerId`, `value`), `wrong` (`playerId`, `value`), `noAnswer`, `reveal` (`clue`, `taps`: every
tap's record, §6.6, when a board clue is judged), `roundEnd` (`round`), `finalBet` (`playerId`; no
amount), `finalReveal` (`playerId`, `correct`, `bet`, and `tap`, their answer's record, when they
answered), `gameOver` (`winnerIds`). `right`/`wrong` `value` is the stake (a gold square: the bet).

## 6. Fair answering (the buzzer)

The aim: the fastest **person** wins, not the fastest connection.

1. **Choices appear at one instant.** `clue.openAt` is in the state from the start of reading. Every
   phone and the table show the choices when their synced clock reaches `openAt`. If a state arrives
   after `openAt`, show them at once.
2. **The phone measures the person.** When the choices are painted (the `requestAnimationFrame` after
   un-hiding them), the phone notes that time (`performance.now()`); on a tap it sends
   `ms = event.timeStamp − paintTime` (rounded). Taps before the choices show are impossible (nothing
   to tap); a request arriving more than 50 ms before `openAt` is refused ("answers are not open yet").
3. **The server bounds what it trusts.** On arrival at server time `A`, with the player's latency
   `L` (§1.1) capped at 200 ms: `estimate = A − openAt − L/2`;
   `effective = min(max(ms, estimate − L − 150), A − openAt + 50)`, never below 0. A missing or
   invalid `ms` (null included) uses `estimate`. The phone reports its own round trip, so the cap
   keeps a doctored phone's gain to at most `L/2 + L + 150` ≤ 450 ms over its arrival time
   (uncapped, claiming 2 s would make any tap in the first 3 s count as instant); an honest phone
   is unaffected up to a one-way delay of about 450 ms. The upper bound stops a phone claiming a
   huge `ms` so a wrong guess counts as later than the winner (a free `late` tap). A phone that
   cannot measure (no paint time) sends no `ms`, never 0.
4. **Deciding the winner.** When the first **correct** tap arrives (server time `A₁`), the engine waits
   `wait = clamp(Lmax + 50, 100, 400)` ms, `Lmax` = the highest latency among players who may still
   answer and are online. At `A₁ + wait`: the winner is the correct tap with the smallest `effective`.
   Wrong taps with `effective` below the winner's lose the value; wrong or right taps after it are
   `late` (no change). If nobody is right by `closesAt + wait`, every wrong tap loses the value.
   If every eligible player has tapped before that, decide at once (after `wait` from the last tap if
   a correct tap is among them).
5. **Computers** tap through the same path: the engine draws each computer's choice and `ms` when the
   choices open (§8) and treats the tap as arriving at `openAt + ms` with latency 0.
6. **Records** keep for each tap: player, choice, reported `ms`, arrival − `openAt`, latency, effective.

## 7. Questions and dealing

**Packs** live in `internal/quiz/packs/*.json` (embedded in the binary, **never under `web/`**, which is
served publicly). `internal/quiz/packs/README.md` documents the format below.

```json
{
  "id": "core-1",
  "title": "General knowledge 1",
  "version": 1,
  "license": "own",
  "attribution": "",
  "categories": [
    {
      "id": "core-1/planets",
      "title": "THE PLANETS",
      "topic": "space",
      "clues": [
        {"tier": 1, "text": "…", "answer": "…", "wrong": ["…", "…", "…"], "src": "https://…"}
      ]
    }
  ],
  "finals": [
    {"id": "core-1/final-01", "category": "…", "topic": "…", "text": "…", "answer": "…", "wrong": ["…", "…", "…"], "src": "https://…"}
  ]
}
```

Rules, checked by a Go test over every pack:
- `license`: `own`, `CC0` or `CC BY-SA 4.0`. Any licence other than `own`/`CC0` needs `attribution`
  (it goes into `state.credits` whenever that pack is used). CC BY-SA material stays in its own pack
  file, never mixed into an `own` pack.
- Each category has exactly 5 clues with tiers 1, 2, 3, 4, 5 (tier 1 easiest = row 0).
- `text` ≤ 140 characters; `answer` and each `wrong` ≤ 32 characters; 3 `wrong`, all different from
  the answer and each other (case- and space-insensitive); category `title` ≤ 24 characters, upper case.
- `topic` one of: `geography`, `science`, `space`, `nature`, `history`, `people`, `sport`, `music`,
  `screen`, `books`, `art`, `food`, `words`, `numbers`, `inventions`.
- Ids unique across all packs. `src` present (where the fact was checked).

**Dealing** (at `start` and for round 2): 6 categories per board, each from a different topic, none the
room has played before (when the pool runs out, the least recently played come back); the final from
the finals pool, also not repeated. Round 1 has 1 gold square, round 2 has 2 in different categories;
gold squares sit in rows 2–4 (weights 2 : 3 : 3).

## 8. Computer players

Computers live in the engine (seeded, reproducible). Per level, and per clue tier `t` (1–5):

| | Easy | Normal | Hard |
|---|---|---|---|
| knows it, by tier 1→5 | .80 .65 .50 .35 .20 | .86 .74 .60 .47 .32 | .88 .76 .63 .50 .35 |
| guesses when it doesn't know | .35 | .15 | .10 |
| tap time when it knows (ms) | 2600 ± 700 | 2100 ± 600 | 2000 ± 500 |

(Tuned with the arena below. Speed decides a clue that both players know, so Normal and Hard are only a
little quicker and better read than the modelled person: the first draft, Hard at .96–.60 and
1400 ms, let the person win about 1 game in 100 against it.)

- Each computer gets 2 strong topics (+0.12) and 2 weak topics (−0.12) per game, random, clamped to 0.02–0.98.
- A guess is a random choice and comes 800 ms later than a knowing tap; tap times never below 700 ms.
- **Picking:** Easy any open cell; Normal the cheapest cell of a random category; Hard its strong
  categories first, cheapest first, and higher rows (gold hunting) when behind. Picks after 1.5–3 s.
- **Gold bets:** Easy ~20% of max; Normal 40–60% of max (more in a strong topic); Hard 80–100% of max
  in a strong topic or when behind, else ~50%.
- **Final bets:** simulate. Estimate each player's chance from their record so far (prior 0.6, weight 5
  answers); try 8 candidate bets (0, the "cover" bet `2×second − leader + 1` for a leader, the "stay
  ahead" bet `leader − 2×second − 1`, 25/50/75/100% of max, the bet that keeps second place over third);
  2,000 simulated outcomes each; take the bet with the best chance of finishing first. Easy adds big
  noise (random 0–50% of max half the time), Normal small noise, Hard none.
- **Final answer:** knows with the tier-4 chance (topic-adjusted), else guesses; always answers.
- Targets, checked with `go run ./cmd/quiz-arena`: a modelled average person (knows .85 .72 .58 .45 .30,
  guesses .2, taps 2200 ± 700 ms) wins about 40–50% of 1-v-1 Quick games against Normal and 25–30%
  against Hard; Hard beats Normal clearly. Measured on 22 Nov 2026 (4,000 Quick games per matchup,
  seed 1; ± is the 95% confidence interval):

  | matchup | wins (a tie counts half) | average score |
  |---|---|---|
  | person v Easy | 74.7% ± 1.3 | 3,393 v 1,633 |
  | person v Normal | 43.1% ± 1.5 | 3,037 v 3,493 |
  | person v Hard | 27.9% ± 1.4 | 2,812 v 3,911 |
  | Hard v Normal | 65.0% ± 1.5 | 3,777 v 3,240 |

## 9. Screens

Both screens use Estate's look (Oswald and Inter, `web/shared/tokens.*`), never a TV show's look (no
blue screen with yellow money). Category headers carry Estate's property-group colour bars.

- **Table screen** (`web/table/quiz-table.js`), at 1080p: clue text ≥ 48 px, choices ≥ 36 px, board
  values ≥ 56 px, category titles ≥ 28 px, nothing under 24 px. States apply at once (no queued
  animations that delay the clue; the 7 Nov review found the TV 1.7 s behind on Ludo-style queues).
  Screens: lobby (picker tile QUIZ, seats, ADD COMPUTER + level, setting chips, START), intro, board
  with score plates (chooser marked), clue with reading bar, choices with "answered" dots (who, not
  what) and a countdown, reveal (right choice, who won, who lost points), gold square reveal and bet,
  round end, final (category, bets in, clue, choices, reveal one by one from the lowest score),
  finished (places, PLAY AGAIN, NEW TABLE), help with credits.
- **Phone** (`web/phone/quiz-screens.js`, `quiz-app.js`), from 360 px wide, nothing under 14 px:
  join/lobby; pick (the board grid, big cells) or "X is picking"; reading (clue text + bar); answer:
  four full-width buttons A–D in fixed positions with the letter and the text (not colour alone),
  ≥ 64 px tall; locked in; reveal (right/wrong, points); gold bet / final bet (stepper + MIN, HALF,
  MAX); final answer; finished. A visual cue when the choices open (vibrate where available; iPhones
  have no vibrate, so the visual cue is the main one).
- **Sound:** an open chime, a lock-in tick, right, wrong, gold, the last 3 s ticking, and our own
  "thinking" music for the final (from `web/shared/music.js`).
- **Fixtures** (`web/fixtures/quiz/`, written by `ESTATE_QUIZ_FIXTURES=web/fixtures/quiz go test
  ./internal/server -run TestQuizFixtures`): `quiz-lobby-table`, `quiz-lobby-phone`, `quiz-intro-table`,
  `quiz-pick-table`, `quiz-pick-phone` (chooser), `quiz-pick-other-phone`, `quiz-read-table`,
  `quiz-read-phone`, `quiz-answer-table`, `quiz-answer-phone`, `quiz-answered-phone`,
  `quiz-reveal-table`, `quiz-reveal-phone`, `quiz-gold-table`, `quiz-bet-table`, `quiz-bet-phone`,
  `quiz-roundend-table`, `quiz-finalbet-table`, `quiz-finalbet-phone`, `quiz-finalanswer-table`,
  `quiz-finalanswer-phone`, `quiz-finalreveal-table`, `quiz-finished-table`, `quiz-finished-phone`.
  Demos: `/?demo=quiz-answer-table`, `/play?demo=quiz-answer-phone` (any fixture name).
