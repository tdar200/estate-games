# Poker: game API

No-Limit Texas Hold'em for the Estate party server. This file describes what travels over the wire: the
actions a phone or the table screen sends, the state every viewer receives, the animation events, and the
protocol for an external bot. The rules engine is `internal/poker/holdem`; the server code is
`internal/server/poker.go` and `internal/server/poker_adapter.go`.

Play money only. Chips have no value and cannot be bought.

Source of truth:

- `internal/poker/holdem`: the rules engine. `Table` is a game session and `Hand` is one hand.
- `internal/poker/bots`: the Easy and Normal bots, plus the external-bot bridge.
- `web/fixtures/poker/poker-*.json`: real states in every phase, from `go run ./cmd/poker-fixtures`.

## 1. Transport

Poker uses Estate's room API (Estate `docs/API.md`, "Game types"):

- **Create:** `POST /api/games {name, tokenKind?, gameType: "poker"}` from a phone (it becomes the host
  player), or `POST /api/tables {gameType: "poker"}` from the PC table screen. `gameType` is `"estate"`
  (the default) or `"poker"`; anything else is `400 {"error":"unknown game type"}`. The classic
  `POST /api/rooms` always makes an Estate game.
- **Join:** `POST /api/rooms/{code}/join` as for Estate. In a running poker game the engine decides: with
  `settings.lateJoin` on and a free seat the newcomer is dealt in from the next hand.
- **Phones only:** `POST /api/rooms/{code}/seats` (controller or keyboard seats on the PC) answers
  `400 {"error":"poker needs a phone per player"}`. The table screen is a viewer: it sees face-up cards only.
- **Act:** `POST /api/rooms/{code}/action` with `{token, action}`. The table screen may send the host's
  actions `start`, `settings` and `deal` with `{tableToken, action}`.
- **Live updates:** `GET /ws/rooms/{code}?token=` — events (§5), then each viewer's own state.
- **Computer seats:** `POST /api/rooms/{code}/ai {token, difficulty}`; poker offers the levels in
  `state.aiLevels` (§3.9: `["easy","normal"]`), other levels are refused.
- **End Game** (`POST /api/rooms/{code}/end`), **rematch** (same game type and settings), **reclaim**,
  room settings (`computerSpeed` scales the computers' pause) and the games list work as for Estate.
- `POST /api/rooms/{code}/trade-quote` is Estate-only (400 in a poker room).

On a rule error, `POST …/action` answers `400 {"error": "<message>"}`. The messages are plain English and
safe to show, e.g. `"it is not your turn"` or `"that bet size is not allowed: raise to between 40 and 1500"`.

## 2. Actions

`action` is a JSON object. `to` is always the total a player's bet becomes on this street (the "raise to"
amount), never the increment.

| action | who | when | effect |
|---|---|---|---|
| `{"type":"start"}` | host | lobby, 2+ players | Builds the blind schedule and deals hand 1. |
| `{"type":"settings","settings":{…}}` | host | lobby | Partial update of §2.1; unknown keys are ignored. |
| `{"type":"fold"}` | player to act | `phase=betting` | Not allowed when checking is free (`you.legal.fold=false`). |
| `{"type":"check"}` | player to act | betting, nothing to call | |
| `{"type":"call"}` | player to act | betting | Calls `you.legal.call`; with nothing to call it checks. Calling for less than the bet puts the player all-in. |
| `{"type":"bet","to":N}` / `{"type":"raise","to":N}` | player to act | betting, `you.legal.raise` set | `N` must be in `[raise.min, raise.max]`. The two names mean the same thing. |
| `{"type":"allin"}` | player to act | betting | Raises all-in if raising is allowed, otherwise calls with everything. |
| `{"type":"show"}` | a player dealt into the last hand | after the hand (`you.canShow`) | Turns their hole cards face up for everyone. |
| `{"type":"sitOut"}` | any seated player | any time | Not dealt in from the next hand. A hand in progress is still theirs to finish. |
| `{"type":"sitIn"}` | sitting-out player | any time | Dealt in from the next hand; resumes a paused table. |
| `{"type":"rebuy"}` | busted player | `you.canRebuy` (tournament: before `you.rebuyUntil`; cash: any time) | Back in with `settings.startingStack` chips; `boughtIn` grows by the same. |
| `{"type":"declineRebuy"}` | busted player | as above | Out now and placed (§6). In a cash game: out of the game for good. |
| `{"type":"deal"}` | host | `showdown`, `handOver` or `paused` | Deals the next hand now instead of waiting for `nextHandAt`. |

The host may also end the game with `POST /api/rooms/{code}/end` (Estate's End Game). Any hand in
progress is called off and its chips are returned. A tournament then ranks players by chips; a cash game
ranks everyone who sat down (including players who left or gave up) by chips won, `stack − boughtIn`.
With a tie for first there is no single winner (`winnerId` is `""`).

### 2.1 Settings (`state.settings`)

| key | default | range | meaning |
|---|---|---|---|
| `style` | `"tournament"` | `"tournament"`, `"cash"` | §2.2 |
| `maxPlayers` | 8 | 2–`seatLimit` (8) | seats at the table. The engine supports up to 10; the party game caps it at 8 (`state.seatLimit`) |
| `startingStack` | 1500 | ≥ 10 big blinds | chips per player, and per rebuy (the cash buy-in) |
| `smallBlind` / `bigBlind` | 10 / 20 | 1 ≤ SB ≤ BB | first blind level (a cash game's fixed blinds) |
| `ante` | 0 | 0–BB | first-level ante, paid by every player dealt in |
| `blindSchedule` | `"auto"` | `"auto"`, `"off"` | `auto`: blinds rise so the game ends in about `targetMinutes`. Ignored in a cash game |
| `targetMinutes` | 60 | 15–240 | used with `auto` (checked only when `blindSchedule` is `auto`) |
| `antes` | true | | with `auto`, antes of about BB/8 start a third of the way in |
| `rebuys` | 0 | 0–10 | tournament rebuys allowed per player; 0 = off. Ignored in a cash game (unlimited) |
| `rebuyLevels` | 0 | 0–50 | tournament rebuys only during the first N levels; 0 = the whole game |
| `actionSeconds` | **20** | 0 or 10–300 | turn timer per decision; 0 = off. The host screens offer off, 10, 20, 30 and 60 |
| `timeBankSeconds` | **30** | 0–120 | each player's time bank (§4.1); 0 = off |
| `lateJoin` | true | | players may join a running game (dealt in from the next hand) |
| `fourColorDeck` | false | | display hint: draw spades black, hearts red, diamonds blue, clubs green. The table screen follows it; a phone may keep its own preference (`poker.js` `parseCard(code, {fourColor})`) |

`settings` is a partial update: send only the keys that change. Each key is checked on its own range;
settings that the chosen style ignores may keep any valid value, so switching back restores them.

### 2.2 Game styles

| | `tournament` (default) | `cash` |
|---|---|---|
| blinds | rise per `blindSchedule` / `targetMinutes` | fixed at `smallBlind`/`bigBlind`/`ante`; `schedule` has one level, `level.endsAt` is 0 |
| out of chips | rebuy per `rebuys`/`rebuyLevels` within 30 s, otherwise out with a place | `busted`: sits out and may rebuy whenever they like (no deadline, `rebuyUntil` absent) |
| `boughtIn` | `startingStack × (1 + rebuys)` | the same: buy-in plus every rebuy |
| ends | when one player has every chip, or End Game (ranked by chips) | when the host ends it (End Game), or by itself once fewer than two players are left in it (the others left or declined to rebuy); ranked by chips won, `stack − boughtIn` |
| `place` | given as players go out | given to everyone at the end |
| leaving | the player's chips leave with them; placed by when they left | the player cashes out what is behind them; their result counts at the end |

`standings[]` carries `stack`, `boughtIn` and `net` (= `stack − boughtIn`) for both styles.

How the automatic schedule works:

- Every level lasts `targetMinutes/8` minutes, clamped to 3–20 minutes.
- Big blinds follow a geometric curve, rounded to friendly numbers (20, 30, 40, 50, 60, 80, 100, 120, 150,
  200, …), with the small blind exactly half.
- The curve is aimed so that at `targetMinutes` the big blind is about a twentieth of all the chips in play.
  At that point the average heads-up stack is about 10 big blinds, so the game ends soon after.
- Levels keep rising after the target, so every game finishes.
- The whole schedule is in `state.schedule`. In the lobby it is a preview based on the current settings.
- A new level takes effect at the start of the next hand after `level.endsAt`.

## 3. State

Every viewer gets their own state. It is the engine's `holdem.View`, plus the fields Estate's server
adds (§3.9). Hole cards are hidden as described in §3.10. Fixtures for every phase are listed in §7.

### 3.1 Top level

| field | type | notes |
|---|---|---|
| `game` | `"poker"` | lets one client tell game types apart |
| `code`, `hostId`, `version` | | `version` rises with every change; clients drop a state whose version they already have |
| `status` | `lobby` \| `playing` \| `finished` | |
| `phase` | `lobby` \| `betting` \| `runout` \| `showdown` \| `handOver` \| `paused` \| `finished` | §4 |
| `now` | ms | server clock when the state was built; use it to correct countdowns for clock skew |
| `settings` | §2.1 | |
| `seatLimit` | int | the most seats `settings.maxPlayers` may be set to (8) |
| `level` | `{index, smallBlind, bigBlind, ante, endsAt, next}` | `endsAt` = when the blinds go up (0 = never); `next` = the following level or null |
| `schedule` | `[{smallBlind, bigBlind, ante, minutes}]` | `minutes: 0` on the last level (it never ends) |
| `handNumber` | int | 0 before the first hand |
| `street` | `preflop` \| `flop` \| `turn` \| `river` \| `""` | `""` once the hand is over |
| `board` | `["As","Kd","7c"]` | cards are rank + lower-case suit; `T` = ten |
| `pots` | `[{amount, eligibleIds}]` | pots already gathered (bets still in front of players are not included). More than one entry means side pots; `eligibleIds` are the players still able to win each one |
| `totalPot` | int | everything in the middle, including bets in front of players |
| `currentBet` | int | the street bet to match |
| `buttonSeat`, `smallBlindSeat`, `bigBlindSeat` | seat numbers, -1 if none | heads-up, the button is also the small blind |
| `actorId` | player id or `""` | whose turn it is (`phase=betting` only) |
| `actionDeadline` | ms or 0 | when `actorId`'s time runs out: the turn timer, and once that has run out the time bank as well (§4.1) |
| `actionTimeBank` | bool | `actorId`'s turn timer ran out and their time bank is running (`actionDeadline` now covers the bank) |
| `nextHandAt` | ms or 0 | when the next hand is dealt (`showdown`, `handOver`, `paused`) |
| `players` | §3.2 | sorted by seat |
| `actions` | §3.3 | this hand so far, oldest first |
| `result` | §3.4 or null | set once the hand is over |
| `you` | §3.5 or null | set when the viewer is a seated player |
| `winnerId` | id or `""` | once `finished` |
| `standings` | `[{id, name, place, stack, boughtIn, net}]` or null | once `finished`, best place first; `net = stack − boughtIn` (a cash game ranks by it) |
| `log` | `[{id, text}]` | the last 80 lines, e.g. "Sam raises to 60", "Maya wins 1,800 from side pot 1 with Pair of Twos" |
| `lastEventSeq` | int | sequence number of the latest event (§5) |
| `viewerId`, `viewerRole`, `viewerName`, `spectatorId`, `viewerIsHost`, `spectators` | | as in Estate |

### 3.2 `players[]`

| field | notes |
|---|---|
| `id`, `name`, `kind` (`human` \| `ai`), `seat` (0 to `settings.maxPlayers` − 1) | seats go clockwise |
| `stack` | chips behind (not counting `bet`) |
| `bet` | chips in front of the player on this street |
| `status` | `waiting` (seated, dealt in next hand) · `active` · `folded` · `allIn` · `sittingOut` · `busted` (no chips, rebuy offered) · `out` (eliminated) · `left` |
| `inHand` | dealt into the current or last hand |
| `hasCards` | still holds cards (face down unless `cards` is set) |
| `cards` | `["Ah","Kh"]` when visible to this viewer, else null |
| `shown` | the cards are face up for everyone |
| `handName` | label for a face-up hand: "Two pair, Kings and Sevens" once the flop is out; before it "Pocket Sevens" or "Ace-King suited" |
| `odds` | `{win, tie}` (0–1, 4 decimals) during an all-in run-out (`phase: "runout"`), else null. `win` is the chance of the best hand alone, `tie` of sharing it. Recomputed each street: exact once two or fewer cards are to come, otherwise 30,000 seeded samples. Folded players' cards count as unknown, so the numbers reveal nothing about mucked hands. `poker.js` `oddsLabel()` formats them |
| `isButton`, `isSmallBlind`, `isBigBlind` | |
| `lastAction` | `{type, amount, to, street, allIn}` or null: the player's latest action this hand (blinds included, antes excluded). Show it as the "Raise to 60" / "Check" badge only while `lastAction.street === state.street` |
| `sitOutNext` | will sit out from the next hand |
| `rebuyUntil` | ms, while `busted` in a tournament (absent in a cash game: no deadline) |
| `rebuys` | rebuys used |
| `place` | tournament: final place once `out`/`left`; everyone gets one at the end (a cash game places only then) |
| `timeBank` | seconds left in the player's time bank (int). While the bank runs for the actor it still shows what they had when it started; it drops when they act (whole seconds used, rounded up) |
| `boughtIn` | chips the player brought: the starting stack (cash buy-in) plus every rebuy. Chips won so far = `stack − boughtIn` (between hands) |

### 3.3 `actions[]`

`{playerId, street, type, amount, to, allIn}`, where `type` is one of `ante`, `smallBlind`, `bigBlind`,
`fold`, `check`, `call`, `bet` or `raise`. `amount` is the chips this action put in; `to` is the player's
street bet afterwards. A fold the player didn't choose (they left the table) shows as `fold` with
`amount 0`.

### 3.4 `result` (hand over)

```json
{"handNumber": 12, "showdown": true, "board": ["2c","As","9s","6d","4c"],
 "pots": [{"amount": 1800, "eligibleIds": ["p2","p3","p1"],
           "winners": [{"id": "p2", "amount": 1800, "handName": "Two pair, Sixes and Twos",
                        "bestCards": ["6h","6d","2s","2c","As"]}]},
          {"amount": 1800, "eligibleIds": ["p3","p1"], "winners": [ … ]}],
 "hands": [{"id": "p2", "cards": ["6h","2s"], "handName": "…", "bestCards": […], "mucked": false}, …],
 "uncalled": [{"id": "p3", "amount": 900}]}
```

- `pots[0]` is the main pot and later entries are side pots. A split pot has several `winners`.
- `showdown: false` means everyone else folded. No hands are named then, and `hands` is empty.
- `hands` lists the players still in at showdown, in the order they show. A mucked hand has
  `cards: null, mucked: true`.
- `uncalled` lists chips handed back because nobody matched them. It appears both after a fold and at
  showdown.
- `handName` and `bestCards` appear only for hands that were shown. Highlight `bestCards` on the table.
- `cancelled: true` means the hand was called off by End Game.

### 3.5 `you` (the viewer's own panel)

| field | notes |
|---|---|
| `id`, `seat`, `cards` | your hole cards (also while folded) |
| `handName` | your best hand now ("Pair of Aces"), or before the flop "Ace-King suited" / "Pocket Sevens" |
| `strength` | 0–1 (2 decimals) or null: hand strength, below |
| `toAct` | your turn |
| `legal` | when `toAct`: §3.6 |
| `canShow` | the hand is over and your cards are face down: offer "Show" |
| `canRebuy`, `rebuyUntil` | busted with a rebuy available (tournament: until `rebuyUntil`; cash: no deadline, `rebuyUntil` absent) |
| `sittingOut`, `sitOutNext`, `isHost` | |

**`you.strength`** is the chance that your hand wins by the river against the opponents still in the
hand, assuming random cards for them (ties count as a share). The board cards still to come are dealt out in
every sample, so a draw counts for what it is worth (a flush draw on the flop against one opponent ≈ 0.5); on
the river it is the chance your hand is best (pocket aces against one opponent before the flop ≈ 0.85). It is null when you are not in a hand that is still being bet: not dealt in, folded,
during an all-in run-out (show `players[].odds` there) and once the hand is over; computer seats never
get it (no screen shows it, and it keeps their decisions cheap). It is computed from
your own cards, the board and the number of opponents only (1,500 seeded Monte Carlo samples, about
0.5 ms against seven opponents), recomputed when a street is dealt or a player folds, and appears only
in your own `you`: never in `players[]`, never for the table screen or spectators.

### 3.6 `you.legal`

```json
{"fold": true, "check": false, "call": 67,
 "raise": {"isBet": false, "min": 134, "max": 1455,
           "presets": [{"label": "Min", "to": 134}, {"label": "½ pot", "to": 235},
                       {"label": "¾ pot", "to": 319}, {"label": "Pot", "to": 403},
                       {"label": "All-in", "to": 1455}]},
 "allIn": 1455}
```

- `call` is the number of chips a call puts in. It is less than the bet when the call puts you all-in,
  and 0 when you can check.
- `raise` is null when raising isn't allowed:
  - when calling would already put you all-in;
  - when everyone else is all-in;
  - when a short all-in hasn't reopened the betting for you (§6).
- `isBet: true` means this is the first bet of the street. Label the button "Bet", otherwise "Raise to".
- `min` may equal `max`, as an all-in for less than a full raise.
- `presets` already respect the limits, and repeated amounts are dropped.
- Pot-sized presets use the pot after you call (`to = currentBet + pot × fraction`).
- `allIn` is the `to` an all-in reaches. It is a call amount when `raise` is null.

### 3.7 Seats and order

Seats go clockwise and the button moves one live seat clockwise each hand. The first button is drawn at
random.

| | small blind | big blind | first to act preflop | first to act after the flop |
|---|---|---|---|---|
| 3 or more players | next live seat after the button | the seat after that | seat after the big blind | first live seat after the button |
| Heads-up | the button | the other player | the button | the big blind |

### 3.8 Sitting out, busting, joining

- Sitting-out players keep their chips and seat but aren't dealt in, and they don't pay blinds.
- With fewer than two players able to play, the table goes to `phase: paused` until someone sits in,
  rebuys or joins.
- A player who runs out of chips becomes `busted`. In a tournament they may rebuy until `rebuyUntil`
  (30 s) if `settings.rebuys` allows it; otherwise they are `out` with a `place`. In a cash game they sit
  out, busted, until they rebuy (no deadline) or decline.
- A busted player pauses nothing, unless they are the only opponent left.
- **Away players** (the server's stand-in): after 30 s offline the stand-in only checks when checking is
  free and folds otherwise (`Table.SafeAction`). It never bets, calls or decides a rebuy; an open rebuy
  offer simply runs out (a cash game's waits). Away players stay dealt in, so they post their blinds, and
  `players[].autopilot` is true while the stand-in plays for them.
- Late joiners are `waiting` until the next hand.

### 3.9 Fields Estate's server adds

- On every player: `input` (`phone` | `ai`), `tokenKind` (the player's avatar, one of Estate's eight),
  `online`, `difficulty` (computer seats only: `easy` | `normal`), `autopilot` (true while a stand-in plays
  for them; omitted otherwise).
- At top level, after the engine's fields:
  - `gameType: "poker"` (Estate party states carry `gameType: "estate"`; choose the screen by
    `state.game === "poker"` or `state.gameType`);
  - `aiLevels`: the computer levels this room offers, `["easy","normal"]` for poker (Estate rooms:
    `["easy","normal","hard","expert"]`). Show only these. Poker's Hard/Expert come later from the
    trained AI; with the server's `--poker-bot` option set, `"hard"` is offered and plays as that bot;
  - `roomSettings: {computerSpeed, listed}`;
  - `claims` (only when there are requests the viewer may answer, as for Estate);
  - `localSeats: []` on the table screen's own state (always empty in poker).

The fixtures include them, in the same key order.

### 3.10 What each viewer can see

- **Phone (a player):** your own two cards (`you.cards` and your `players[].cards`), and every card
  that is face up.
- **Table screen and spectators:** only face-up cards.
- **Cards go face up:**
  - when betting is over and the board is run out (`phase: runout`), for everyone still in;
  - at showdown, by the show rules (§6);
  - when a player uses `show`.
- **Never sent to any client:** the deck, burn cards and other players' hidden cards.
  - The engine's `Snapshot()` (persistence and the server only) contains them; `PublicState` never does.
  - Tests check this for every fixture and viewer.

## 4. Phases and timing

```
lobby ─start─▶ betting ⇄ (next street) ─┬─▶ showdown ─┐
                  │                      └─▶ handOver ─┤  wait nextHandAt
                  └─(all-in, no more betting)─▶ runout ─▶ showdown
                                                       ▼
                         next hand ◀────────────────────┘   (or paused / finished)
```

| phase | what's happening | what the table screen shows | moves on |
|---|---|---|---|
| `betting` | `actorId` must act | highlight the actor; countdown to `actionDeadline` if set (shown as the time bank while `actionTimeBank`) | an action, or the turn timer and time bank running out (check if free, otherwise fold; two timeouts in a row sit the player out) |
| `runout` | nobody can bet; live hands are face up | deal the next street every 1.8 s; show each hand's `odds` (win chances) and `handName` | automatic |
| `showdown` | `result` with cards | reveal in `result.hands` order, then push each pot to its `winners` | `nextHandAt` (5 s + 1.5 s per extra pot), or host `deal` |
| `handOver` | everyone else folded | push the pot | `nextHandAt` (2.5 s) or host `deal` |
| `paused` | fewer than two can play | waiting message | sit-in, rebuy or join |
| `finished` | `standings`, `winnerId` | final screen | rematch (Estate) |

Timers live in the engine. The server calls `Table.Advance(now)` at `Table.NextDeadline()`
(`internal/server/poker.go`). All times are Unix milliseconds on the server clock; `state.now` tells you
that clock.

### 4.1 Turn timer and time bank

- Each decision has `settings.actionSeconds` (default 20 s; 0 = no timer). `actionDeadline` is when it ends.
- When it runs out and the actor has time in their bank (`players[].timeBank` > 0), the bank starts
  running from that moment: a `timeBank` event (§5) is sent, `actionTimeBank` becomes true and
  `actionDeadline` moves out by the whole bank (`deadline + timeBank × 1000`). The player can still act
  normally.
- Acting while on the bank uses the seconds that ran, rounded up to whole seconds, from `timeBank`.
- When the bank runs out too, `timeBank` is 0 and the timeout plays check (if free) or fold. Two timeouts
  in a row sit the player out from the next hand.
- Every player starts with `settings.timeBankSeconds` (default 30 s; late joiners too). The bank refills
  by **5 s every 10 hands** a player is dealt into, never above the setting.
- The next actor always starts on a fresh turn timer. All of this is deterministic (the engine clock) and
  is saved with the game, so a restart mid-bank keeps the same deadline.

## 5. Events

Over the room WebSocket, events arrive as `{"type":"event","event":{…}}` before the state they lead to,
just as Estate events do. Events are public: they never carry a hidden card. Every event has `seq` and
`handNumber`.

| `type` | fields | use |
|---|---|---|
| `handStart` | `playerId` (button) | shuffle and deal animation |
| `post` | `playerId`, `action` (`ante` \| `smallBlind` \| `bigBlind`), `amount`, `to`, `allIn` | chips to the line |
| `action` | `playerId`, `action` (`fold` \| `check` \| `call` \| `bet` \| `raise`), `amount`, `to`, `allIn`, `street` | chips, sounds, speech bubble |
| `deal` | `street` (`flop` \| `turn` \| `river`), `cards` | board cards; bets sweep into the pot first |
| `reveal` | `playerId`, `cards`, `handName?` | flip a hand face up |
| `muck` | `playerId` | throw a hand away face down |
| `uncalled` | `playerId`, `amount` | chips slide back |
| `win` | `playerId`, `amount`, `pot` (0 = main), `handName?` | push a pot (or part of a split) |
| `bust` | `playerId`, `place?` | no `place`: may rebuy; with `place`: eliminated |
| `rebuy` | `playerId`, `amount` | |
| `timeBank` | `playerId`, `amount` (seconds in the bank) | the turn timer ran out; the time bank is running (§4.1) |
| `levelUp` | `level` `{smallBlind, bigBlind, ante, minutes}` | "Blinds are up" banner |
| `sitOut`, `sitIn`, `join`, `leave` | `playerId` (`leave` may carry `place`) | |
| `gameOver` | `playerId` (winner, may be `""`) | |

Each fixture has an `events` array with the current hand's events, for animation work.

The server sends each event once, in order, before the state it leads to; after a server restart it does
not repeat events that were already sent (it saves the last sequence number with the room).

## 6. Rules

The betting rules follow PokerKit (University of Toronto). They were cross-checked against it on 20,000
random hands with uneven stacks, antes and short blinds, comparing 168,202 decisions; see
`internal/poker/holdem/crosscheck_test.go`.

- **Blinds and antes.**
  - Antes are posted first, then the blinds out of what is left.
  - A player who can't cover a blind or ante posts what they have and is all-in.
  - The amount to call is the largest bet actually posted.
- **Minimum raise.**
  - A raise must increase the bet by at least the largest bet or raise increment so far this street, and
    never by less than the big blind.
  - An all-in for less is always allowed.
- **Short all-in.**
  - An all-in raise smaller than a full raise does not reopen betting for players who already acted.
    They may only call or fold, unless several short all-ins together add up to a full raise.
  - Players who haven't acted yet may still raise.
- **No pointless raises.** When everyone else is all-in you can only call.
- **Side pots.**
  - Built from each player's total contribution, with as many pots as needed.
  - Pots that end up with exactly the same eligible players are merged.
  - Chips nobody matched (an uncalled bet, or blinds and antes an opponent was too short to match) go
    back to their owner.
- **Odd chips.** Each pot is split separately. Odd chips go one each to the winners in seat order,
  starting left of the button.
- **Showdown order.**
  - The last player to bet or raise on the river shows first. If nobody bet the river, the first live
    player left of the button shows first.
  - Later players show only if they win part of a pot or beat or tie the best hand shown so far.
    Everyone else mucks, face down, and may still `show`.
  - Winners are always shown.
  - After an all-in run-out every live hand is already face up.
- **Run-out.** When at most one player can still bet, every live hand turns face up and the board is
  dealt street by street with no betting.
- **Places.**
  - Players are ranked by the hand they lost their last chip in: later is better.
  - Players busting in the same hand rank by the stack they started that hand with: bigger is better.
  - A declined or expired rebuy counts from the hand the player busted in.
- **Fairness.**
  - Every deck is a Fisher-Yates shuffle from a ChaCha8 generator seeded from `crypto/rand`.
  - Tests and simulations inject a fixed seed instead, so games can be replayed exactly.

## 7. Fixtures

The fixtures are in `web/fixtures/poker/`, listed in its `index.json`. They use Estate's fixture format
`{state, viewer, meta}` plus `events`, so `?demo=<name>` works with Estate's `loadFixture`. Each scene comes
in two versions: `-table` (the shared screen) and `-phone` (one player's phone). The states include every
field the server adds (§3.9), in the server's key order.

| name | scene |
|---|---|
| `poker-lobby-*` | lobby with five seats (two computer players), four-colour deck on, 30 s timer; Priya is offline; phone = the host |
| `poker-preflop-*` | a raise in front of Maya, who is to act; 20 s turn timer running; phone = Maya with her options and `you.strength` |
| `poker-flop-*`, `poker-turn-*`, `poker-river-*` | three players in, Maya faces a bet on each street |
| `poker-timebank-*` | flop: Maya's turn timer ran out, her time bank is running (`actionTimeBank`), facing a bet |
| `poker-flop8-*` | eight seats on the flop; someone else is to act after Sam's bet and Maya acts later (pre-action buttons); Lena offline |
| `poker-runout-*` | three uneven stacks all-in preflop; hands face up with win chances (`players[].odds`), flop out; phone = Sam (short stack) |
| `poker-showdown-*` | main pot and side pot won by different players |
| `poker-busted-*` | tournament: players out of chips with a 30-second rebuy offer; phone = Sam with Rebuy |
| `poker-handover-*` | a raise, everyone folds, pot won without a showdown; phone = the winner (may Show) |
| `poker-uncontested-*` | Maya raises and wins uncontested; phone = Maya with `you.canShow` |
| `poker-sitout-*` | hand 2 dealt without Maya, who is sitting out; phone = Maya (`you.sittingOut`) |
| `poker-headsup-*` | two players: the button posts the small blind and acts first preflop; phone = Maya, her turn |
| `poker-cash-lobby-*` | cash-game lobby: fixed blinds 10/20, buy-in 2,000; phone = the host |
| `poker-cash-busted-*` | cash game mid-hand: Sam busted and sits out with Rebuy available (no deadline); Jonah rebought once (`boughtIn` 2,000); phone = Sam |
| `poker-gameover-*` | finished with standings; phone = a player who did not win |

To regenerate the fixtures, run `go run ./cmd/poker-fixtures`. `go test ./cmd/poker-fixtures` fails if
they are stale. The generator also fails if any fixture shows a hidden card or a hand strength to someone
it doesn't belong to.

## 8. External bot protocol

The stronger AI (a separate Rust program built on robopoker) plugs in as a bot without the server knowing
anything about it. It works over two transports:

- **Process:** the server starts the program and writes one JSON request per line to its stdin. The
  program writes one JSON reply per line to stdout, and stderr is free for logging. One process may serve
  every seat; requests are sent one at a time.
- **HTTP:** `POST` the request to a localhost URL (e.g. `http://127.0.0.1:7420/decide`); the response
  body is the reply.

Request:

```json
{"type": "decide", "id": 17, "protocol": 1, "view": { …the seat's state, exactly as its phone sees it… }}
```

Reply:

```json
{"id": 17, "action": {"type": "raise", "to": 600}}
```

Rules for the bot:

- `view` is that seat's `holdem.View` (§3, without the server's extra fields). It holds the seat's own
  cards and nothing hidden. Decide from `view.you.legal`.
- `action` is any action from §2 that the seat may take.
  - When `view.you.toAct` is true, it is a betting action.
  - When `view.you.canRebuy` is true and it isn't the seat's turn, the bot is being asked about a rebuy:
    answer `{"type":"rebuy"}` or `{"type":"declineRebuy"}`.
- Echo `id`; replies with another `id` are ignored, so a late answer can't land on the wrong decision.
- Reply `{"id": 17, "error": "…"}` to give up on a decision.

Fallbacks:

- The server wraps external bots in `bots.WithFallback(external, normalBot)`.
- A crash, a timeout (default 10 s), an error or an illegal answer is replaced by the Normal bot's
  decision.
- If that also fails, the bot checks when it can and folds otherwise.

To try a bot against the built-in ones:

```
go run ./cmd/poker-arena -hands 20000 -seats normal,cmd:./robopoker-bridge
go run ./cmd/poker-arena -hands 20000 -seats normal,http://127.0.0.1:7420/decide
```

The arena prints each seat's big blinds won per 100 hands with a 95% confidence interval (§9).

## 9. Arena

`go run ./cmd/poker-arena` plays bots against each other:

- Every hand starts with all stacks reset to `-stack` big blinds (default 100), cash-game style.
- The button moves every hand.
- It reports bb/100 per seat with a 95% confidence interval: ±1.96 × standard error, from the hand-by-hand
  variance. The "by bot" rows pool seats and are a rough guide only, because seats at one table aren't
  independent.

Flags: `-hands`, `-seats` (2–10 specs: `easy`, `normal`, `cmd:<program args>`, an http URL), `-stack`,
`-bb`, `-ante`, `-seed`, `-workers`, `-json`.

Reference results (seed 1, 100 bb deep):

| matchup | hands | result |
|---|---|---|
| Normal vs Normal, heads-up | 6,000 | +22.4 ± 37.2 bb/100 (zero within noise) |
| Normal vs Easy, heads-up | 6,000 | Normal +295.8 ± 50.4 |
| 3 Normal + 3 Easy, 6-max | 6,000 | Normal seats +90 to +133; Easy seats −79 to −135 |
