# Ludo: game API

Ludo for the Estate party server: the third game type, after Estate and Texas hold'em. This file
describes what travels over the wire: the actions a phone or the table screen sends, the state every
viewer receives, the animation events, and how computer players and the stand-in play. The server code
is `internal/server/ludo.go` and `internal/server/ludo_adapter.go`; the rules engine is `internal/ludo`.

Source of truth:

- `internal/ludo`: the rules engine (`Game`), the board geometry (`board.go`), the house rules
  (`settings.go`) and the Easy, Normal and Hard computer players (`bots.go`).
- `web/fixtures/ludo/ludo-*.json`: real server states in every phase, for both screens, written by
  `ESTATE_LUDO_FIXTURES=web/fixtures/ludo go test ./internal/server -run TestLudoFixtures`.
- `cmd/ludo-arena`: plays the computer players against each other (§8).

## 1. Transport

Ludo uses Estate's room API (`docs/API.md`, "Game types"), exactly as poker does:

- **Create:** `POST /api/games {name, tokenKind?, gameType: "ludo"}` from a phone (it becomes the host
  player), or `POST /api/tables {gameType: "ludo"}` from the PC table screen. The classic
  `POST /api/rooms` always makes an Estate game.
- **Join:** `POST /api/rooms/{code}/join` as for Estate, **in the lobby only** (there is no joining a
  Ludo game in progress; a seat can still move to a new device: reclaim and rejoin work as for Estate).
- **Keyboard and controller seats work** (nothing is secret in Ludo): `POST /api/rooms/{code}/seats`
  `{tableToken, input: "keyboard"|"controller", name, tokenKind, pad?}` as for Estate. The table acts for
  them with `{tableToken, seatId, action}`.
- **Act:** `POST /api/rooms/{code}/action` with `{token, action}`. The table screen may send the host's
  actions `start` and `settings` with `{tableToken, action}` (with nobody seated it is the host itself, so
  a table of computers can be set up and started).
- **Live updates:** `GET /ws/rooms/{code}?token=` — events (§5), then each viewer's own state.
- **Computer seats:** `POST /api/rooms/{code}/ai {token, difficulty}` with a level from `state.aiLevels`
  (`["easy","normal","hard"]`); others are refused ("Choose easy, normal or hard").
- **End Game** (`POST /api/rooms/{code}/end`: players with every piece home keep their places, the rest
  are ranked by how far their pieces got), **rematch** (same players in the same order, same host, same
  rules, same keyboard and controller seats, back in the lobby), room settings (`computerSpeed` scales the
  computers' pause), the games list, friends, invites, presence and persistence across restarts (the
  dice stream is saved, so a restored game rolls exactly what it would have) work as for Estate.
- `POST /api/rooms/{code}/trade-quote` and `/not-selling` are Estate-only (400).
- **Game records:** every Ludo game is recorded as its event stream (docs/GAME-RECORDS.md, "Poker and Ludo"); it is not replayed through the engine.

On a rule error, `POST …/action` answers `400 {"error": "<message>"}` in plain English, e.g.
`"it is not your turn"`, `"roll the die first"`, `"move a piece first"`, `"that piece can't move"`.

## 2. Actions

| action | who | when | effect |
|---|---|---|---|
| `{"type":"start"}` | host | lobby, 2–6 players (2–4 on the 4-arm board) | Seats everyone on an arm, picks who rolls first at random. |
| `{"type":"settings","settings":{…}}` | host | lobby | Partial update of §2.1; unknown keys are ignored. |
| `{"type":"roll"}` | player to act | `phase=roll` | Rolls the die (§6). |
| `{"type":"move","piece":N}` | player to act | `phase=move`, `N` one of `moves[].piece` | Moves piece `N` (§6). |

### 2.1 Settings (`state.settings`): the house rules

Defaults are the Pakistani/Indian style.

| key | default | meaning |
|---|---|---|
| `pieces` | `4` | Pieces each: 2, 3 or 4 (fewer is quicker). |
| `board` | `"auto"` | `"auto"`: the classic 4-arm cross for 2–4 players, the 6-arm board for 5–6. `"4"`: the cross (refuses a 5th player). `"6"`: six arms even for 2–4 (longer laps, more room). |
| `anyRollOut` | `false` | Any roll brings a piece out, not only a 6 (kids mode). |
| `homeBonus` | `true` | A piece reaching home gives another roll. |
| `safeSquares` | `true` | Start squares and star squares are safe: no captures there, colours may share them. |
| `threeSixes` | `true` | A third 6 in a row ends the turn and isn't played (the first two moves stand). |
| `captureFirst` | `false` | A player's pieces may only turn into their home column once that player has captured a piece; until then they go round again. |
| `blocks` | `false` | Two or more of one player's pieces on a ring square form a block: nobody else may pass or land there. Without it pieces never block and a capture takes every piece on the square. |
| `playOn` | `true` | After the first player gets every piece home, play on for 2nd, 3rd… Off: the game ends at the first winner and the rest are ranked by how far their pieces got. |

Fixed rules: a 6 brings a piece out (unless `anyRollOut`), a 6 or a capture gives another roll, a 6
that can't be used still rolls again, and a piece needs the exact number to reach home.

**Why the cross for up to four:** it is the board everyone knows and the most readable on a TV (15
squares across); the six-arm board is used only when five or six play, unless the host chooses it.

## 3. State

Ludo hides nothing, so every viewer gets the same game; only `you` and the viewer fields differ.

### 3.1 Top level

| field | type | notes |
|---|---|---|
| `game` | `"ludo"` | Clients pick the screen by this. |
| `code`, `status`, `hostId`, `version` | | `status`: `lobby` · `playing` · `finished`. |
| `phase` | string | `lobby` · `roll` · `move` · `finished`. |
| `now` | ms | The server clock. |
| `settings` | §2.1 | |
| `seatLimit` | int | 6, or 4 when the host chose the 4-arm board. |
| `board` | §3.2 | |
| `turnId` | id | The player to act ("" outside play). |
| `turnNumber` | int | Counts turns (bonus rolls belong to the same turn). |
| `dice`, `diceBy` | int, id | The last roll and who rolled it (0 / "" before the first). |
| `rollSeq` | int | Rolls so far: a new value means a new roll (animate the die even for the same number). |
| `sixes` | int | 6s in a row in this turn. |
| `moves` | §3.4[] | The legal moves for `dice` while `phase=move` (public: the table screen highlights them). |
| `lastMove` | §3.5 | The most recent move, or null. |
| `players` | §3.3[] | Join order, which is also turn order (clockwise). |
| `you` | §3.6 | Set for a seated player's own state, else null. |
| `winnerId` | id | Set when finished. |
| `standings` | []? | When finished: `{id, name, place, home, progress, captures, left?}` in place order; null before. |
| `log`, `lastEventSeq` | | Plain-English lines; the last engine event. |
| `viewerId`, `viewerRole`, `viewerName`, `spectatorId`, `viewerIsHost`, `spectators` | | As for Estate. |

### 3.2 `board`

`{arms, ring, column, stepsHome, starts[], safe[]}`: 4 or 6 arms; `ring` = 13 per arm; `column` = 5
home-column squares; `stepsHome` = steps from a start square to home (56 on the cross, 82 on six arms);
`starts[a]` = arm `a`'s start square; `safe` = the safe ring squares (empty when `safeSquares` is off).

Every arm is three columns of six squares. Ring square `i` belongs to arm `floor(i/13)`; within the arm,
squares 0–5 run outwards up the first column, 6 is the tip, 7–12 run back in down the second column. An
arm's start square is its local 8, its star its local 3 (eight steps after the previous arm's start).
A piece goes round from its start square to the tip of its own arm (`ring-2` steps), turns up the
middle column (5 squares) and reaches home: `stepsHome` steps. The yard sits clockwise of its arm.
Clients draw both boards from this numbering (`web/shared/ludo-board.js`).

**Cells** name where a piece is: `"r12"` ring square, `"c2.4"` arm 2's home-column square 4 (5 is next
to home), `"h2"` arm 2's home, `"y2.1"` arm 2's yard spot for piece 1.

### 3.3 `players[]`

`{id, name, kind, arm, status, pieces[], home, out, progress, captures, lost, sixes, place?, canGoHome,
isTurn}` plus the server's fields (§3.7). `status`: `waiting` (lobby) · `playing` · `finished` (every
piece home) · `left`. `home`/`out`: pieces home / on the board. `progress`: 0–100, how far the pieces
have come in all. `captures`: pieces of others this player sent back; `lost`: own pieces sent back.
`canGoHome`: false while `captureFirst` still holds their pieces back.

`pieces[]`: `{index, zone, step, cell, square, left, safe}` — `zone` `yard|track|column|home`; `step` on
the track is squares from the start square (0 = the start square), in the column 1–5; `square` the ring
square (−1 off the ring); `left` the steps still to go; `safe` true where it can't be captured.

### 3.4 `moves[]`

`{piece, from, to: {zone, step}, cell, path[], out?, home?, capture?, safe?}`: `from`/`cell` the cells
before and after; `path` every cell it passes, one hop each, the destination last; `out` it comes out of
the yard; `home` it reaches home; `capture` how many pieces it sends back; `safe` it ends where it can't
be captured. Several pieces coming out of the yard are the same choice, and so are two pieces stacked on
one square: when every move is the same, the server plays it for a person too (§7).

### 3.5 `lastMove`

`{seq, playerId, piece, roll, from, cell, path[], captured[], out, home, again}`; `captured[]`:
`{playerId, piece, cell, yard}` (where it was caught and the yard spot it went back to); `again`: the
move earned another roll. Clients compare `seq` to animate each move once.

### 3.6 `you`

`{id, toRoll, toMove, movable[], place?}`: `movable` are the pieces you may move now.

### 3.7 Fields Estate's server adds

`players[]`: `input` (`phone|keyboard|controller|ai`), `tokenKind`, `color` (the token's colour,
`web/shared/palette.json`), `online`, `difficulty` (computers), `autopilot` (the stand-in plays for them).
Top level: `gameType: "ludo"`, `aiLevels`, `roomSettings`, `claims`, and `localSeats` for the table.

## 4. Phases

`lobby` → `roll` ⇄ `move` → `finished`. After a roll: no legal move → the turn passes (or, on a 6, the
same player rolls again); otherwise `move`. After a move: a 6, a capture or (with `homeBonus`) a piece
home → `roll` again for the same player; otherwise the next player still playing, clockwise.

## 5. Events

Sent on the WebSocket just before the state they belong to (`{"type":"event","event":{…}}`), each with
a `seq`: `join`, `leave`, `start` (who goes first), `turn`, `roll` (`value`, `bonus` on a 6), `noMove`
(`bonus`: rolls again anyway), `threeSixes`, `move` (`piece`, `path`, `cell`), `capture` (`victimId`,
`piece`, `cell`), `home`, `again`, `finish` (`place`), `gameOver` (the winner). The screens animate from
the states themselves (`rollSeq`, `lastMove.seq`), so a missed event never desynchronises them.

## 6. Rules in order

1. **Roll.** A 6 counts towards three in a row. With `threeSixes`, a third 6 ends the turn unplayed.
2. **Moves.** A piece in the yard comes out onto its start square on a 6 (any roll with `anyRollOut`)
   unless another player's block stands there. A piece on the track moves round; if it may go home
   (always, or after a capture with `captureFirst`) and the move crosses its arm's tip, it turns up its
   column; past home is not allowed (the exact number is needed). A block of another player can't be
   passed or landed on. A piece in the column moves up it, exactly to home.
3. **Landing.** On a ring square that isn't safe, every piece of every other player there goes back to
   its yard (with `blocks`, there are never two of one player there to land on).
4. **Again?** A 6, a capture, or a piece home with `homeBonus`: roll again (not after finishing).
5. **Finishing.** A player with every piece home takes the next place. With `playOn` the game goes on
   until one player is left (last place); without it, it ends at once and the rest are ranked by
   progress (then captures). Players who left rank last, the last to leave highest.

## 7. Computers, the stand-in and forced moves

The room's one timer plays (`internal/server/ludo.go`):

- **Computer seats** roll after the computers' pause (about 0.5 s × the room's computer speed) and move
  after a little longer, and always wait until the screens have shown the last roll and hop (dice 0.7 s,
  0.15 s per square, 0.65 s for a capture, 0.8 s for a piece home) so people can follow.
- **Forced moves:** when a person rolled and every legal move is the same (one piece can move, or every
  piece would come out), the server plays it after 0.95 s. People always roll themselves.
- **The stand-in:** when the game has waited `autopilotDelay` (30 s) on an offline person, the computer
  plays for them as Normal would (rolls and moves) until they are back online; `players[].autopilot`
  shows it.

## 8. Computer players and the arena

- **Easy** plays a random legal move.
- **Normal** scores moves by rules of thumb: capture (worth the most), a piece home, into the home
  column, out of the yard on a 6, onto a safe square, away from a piece that can hit it; it avoids
  stopping within reach in front of an opponent.
- **Hard** looks ahead: each move, then the extra roll it may earn, then every roll of the next opponent
  answering as Normal would, scoring positions with weights tuned by self-play (`-tune`).

`go run ./cmd/ludo-arena -games 4000 -seats hard,normal` prints each bot's share of wins (95% CI) and
average place; seats rotate every game. `-rules kids,noSafe,captureFirst,blocks,firstWins,…`,
`-pieces`, `-board` set the house rules. Measured 5 Nov 2026 with the default rules, 4 pieces:

| match | games | wins (95% CI) |
|---|---|---|
| Normal vs Easy | 4000 | Normal 92.8% ± 0.8 |
| Hard vs Easy | 4000 | Hard 94.5% ± 0.7 |
| Hard vs Normal | 4000 | Hard 60.2% ± 1.5 (58.5% before tuning) |
| Hard, Normal, Normal, Normal | 3000 | Hard 34.7% ± 1.7 (a fair share is 25%) |
| Hard, Normal, Easy, Easy | 3000 | Hard 55.3%, Normal 38.7%, each Easy 3.0% |
| Hard and five Normal (six arms) | 2000 | Hard 28.2% ± 2.0 (fair share 16.7%) |
| Hard vs Normal, capture-first and blocks on | 3000 | Hard 51.3% ± 1.8: no real edge; Hard was tuned on the default rules |

## 9. Screens

- **Table screen** (`web/table/ludo-table.js`): the board big in the middle, a plate beside every yard,
  the last roll on a big die in the centre, whose turn across the top. Keyboard seat: Enter rolls,
  ← → pick a piece (its landing square is marked), Enter moves it, 1–4 move that piece. Controller seat:
  A rolls, the D-pad picks, A moves. Lobby: the game picker, seats with ×, ADD COMPUTER with its level,
  ADD KEYBOARD (or Enter / A on a controller), the rule chips, a preview of the board, START. Finish: the
  places, PLAY AGAIN and NEW TABLE.
- **Phone** (`web/phone/ludo-screens.js`, `ludo-app.js`): the whole board, a big ROLL, one big button per
  real choice (or tap a glowing piece), whose turn it is; works from 360 px wide.
- Shared: `web/shared/ludo-board.js` (geometry and SVG), `ludo-view.js` (rule chips, plates, wording,
  sounds), `ludo-fx.js` (the die tumbling, pieces hopping square by square, captures flying home, the
  home burst), `ludo.css`.
- Demos: `/?demo=ludo-move-table`, `/play?demo=ludo-move-phone` (any fixture name).
