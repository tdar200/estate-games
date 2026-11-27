# Estate server API

One Go program (`estate`) serves everything: the PC table screen, the phone app, the
classic client, the JSON API and live updates. All game rules run on the server; clients
only send intentions and render state.

Errors: non-2xx with `{"error": "<English message>"}`. Bodies are JSON. Codes are
case-insensitive 5-character room codes (`ABCDEFGHJKLMNPQRSTUVWXYZ23456789`).

## 1. Compatibility API (same as the Node server)

Kept byte-compatible so `public/app.js` (served at `/classic/`) keeps working.

| Method | Path | Body | Returns |
|---|---|---|---|
| POST | `/api/rooms` | `{name}` | `{code, token, playerId, state}`; creator is host player |
| POST | `/api/rooms/{code}/join` | `{name}` | `{code, token, playerId, state}` (lobby only) |
| POST | `/api/rooms/{code}/watch` | `{name}` | `{code, token, spectatorId, state}` |
| GET | `/api/rooms/{code}/state?token=` | — | `state` (publicState for that viewer) |
| POST | `/api/rooms/{code}/mode` | `{token, role}` | `state` (player ⇄ spectator in lobby) |
| POST | `/api/rooms/{code}/action` | `{token, action}` | `state` |
| POST | `/api/rooms/{code}/trade-quote` | `{token, targetId, offerProperties, requestProperties}` | `{offerCash, requestCash, targetId}` |
| POST | `/api/rooms/{code}/ai` | `{token}` | `state` (host adds AI) |
| POST | `/api/rooms/{code}/remove` | `{token, playerId}` | `state` (host removes player) |

`action` objects are exactly the engine's (`{type:"roll"}`, `{type:"buy"}`, `{type:"bid", amount}`,
`{type:"build", spaceIndex}`, `{type:"offer_trade", targetId, offer:{cash,properties}, request:{…}}` …).
`state` is the engine's `publicState` JSON, unchanged, plus the additive fields in §3.

Limits: up to 8 players per game, 24 spectators, 100 rooms in all, rate limits per client address
(the server is on a public link; see "Limits and overload protection" in §3).

## 2. Party mode and friends (new)

### Devices (identity without accounts)
- `POST /api/device` `{name?, tokenKind?}` → `{deviceId, deviceSecret}`. Called once; the client
  keeps both in `localStorage`. Every later request may send header
  `X-Estate-Device: <deviceId>:<deviceSecret>`.
- `PATCH /api/device` `{name?, tokenKind?, color?}` → profile. Remembers name/token between games.

### Tables (PC main screen)
- `POST /api/tables` `{gameType?, replaceCode?, replaceToken?}` → `{code, tableToken, state}` (see "Game types"). With the code and table token of the table this screen is leaving, that table is closed if it is an empty lobby nobody else is watching, so switching games leaves nothing behind. Creates a room **with no player seat**. The
  `tableToken` session has role `"table"`: sees the full board, has host rights in the lobby
  (start, add AI, rules, remove), and acts for its local seats. It never acts for phone players.
- `POST /api/rooms/{code}/seats` `{tableToken, input:"controller"|"keyboard", name, tokenKind, pad?}`
  → `{seatId, state}`. Adds a player controlled from the PC (gamepad index `pad` or keyboard).
- `POST /api/rooms/{code}/action` `{token: tableToken, seatId, action}` → acts for that local seat.

### Phone players
- `POST /api/rooms/{code}/join` also accepts `{name, tokenKind, deviceId}`; the player records
  `input:"phone"` and the device. The first phone player in a room created by a table becomes the
  **host player** (same lobby rights as the table).
- `POST /api/rooms/{code}/rejoin` (device header) → `{code, token, playerId, state}`: returns the
  seat this device already holds (works mid-game). "Scan again to get your seat back."
- `GET /api/rooms/{code}/summary` (no auth) → `{code, status, phase, turn, host:{name,tokenKind,color},
  players:[{name,tokenKind,color,input}], openSeats, rules}` for the invite landing page.

### Live updates (replaces polling for new clients)
- WebSocket `GET /ws/rooms/{code}?token=` → server sends
  - `{"type":"state","state":{…}}` after every change (full publicState for that viewer);
  - `{"type":"event","event":{…}}` just before the matching state, for animations:
    `dice{playerId,dice}`, `move{playerId,path}`, `pay{from,to|null,amount,reason}`,
    `card{deck,text,playerId}`, `buy{playerId,spaceIndex,price}`, `build{playerId,spaceIndex,houses}`,
    `auction{spaceIndex,bid,bidderId}`, `jail{playerId}`, `bankrupt{playerId,creditorId}`,
    `left{playerId}` (the host removed them, §3c), `win{playerId}`. Events are derived on the
    server from the engine's step. A rent `pay` (`reason:"rent"`) also carries `spaceIndex`, and
    `base`, `multiplier` (and `dice`) when a "nearest" card set the rent (§3c).
  - Clients send `{"type":"ping","t":<client Date.now()>?,"rtt":<ms>?}` and get
    `{"type":"pong","t":<t echoed>,"now":<server ms>}` (the device socket too); a bare ping gets a pong
    without `t`, and older clients that ignore `now` keep working. `rtt` is the client's lowest round
    trip of its last 8 pings: the server keeps the lowest the socket reported over its last 8 pings
    (a negative or non-number rtt is ignored) as that player's latency, which the quiz's fair buzzer uses
    (QUIZ-API.md §1.1, §6).
  - Actions may also go over the socket, for every game type: `{"type":"action","action":{…},
    "id":<client counter>?,"seatId"?}` runs exactly as `POST …/action` with the socket's token (same
    checks, errors and per-address rate limit: socket actions share the 180/min bucket) and is
    answered `{"type":"actionResult","id":<id or null>,"ok":true}` or `{…,"ok":false,"error":"…"}`,
    after the new `state`. Never `{"type":"error"}`, which clients treat as the end of the session.
    `POST …/action` stays and is the fallback when the socket is not open.
  - Before any action reaches an engine (POST or socket), the server drops every top-level field of
    the action object whose name starts with `_`: those are the server's own.
- WebSocket `GET /ws/device` (device header or `?device=id:secret`) → presence. While open, the
  device is **online**. Server pushes `{"type":"invite","invite":{from:{name,tokenKind,color},
  code, playersWaiting}}` and `{"type":"friends","friends":[…]}` when a friend's status changes.

### Friends (people you've played with)
- `GET /api/friends` (device header) → `[{deviceId, name, tokenKind, color, status:"online"|"in_game"|
  "offline", code?, turn?, lastPlayedAt, lastResult?}]`. Two devices become friends automatically
  when they sit at the same table.
- `POST /api/invites` `{toDeviceId, code}` (device header) → `{sent:true}`; pushed to the friend's
  open device sockets. Rate-limited.

### Links and QR
- `GET /j/{code}` → the phone app with the code pre-filled (invite links and QR codes point here).
- `GET /api/rooms/{code}/qr.svg` → SVG QR code of `{publicURL or lanURL}/j/{code}`.
- `GET /api/server` → `{lanURL, publicURL, version}`.

## 3. Additive state fields

`state.players[]` gains `input` (`"phone"|"controller"|"keyboard"|"ai"|"classic"`), `tokenKind`
(`rocket|teapot|bicycle|lighthouse|key|umbrella|anchor|kite`), `online` (bool). `state` gains
`viewerRole` `"table"` for the PC and `localSeats:[{seatId,playerId,input,pad}]` for the table viewer.
Never expose `deviceId`/`deviceSecret` of other players.

Games hold up to 8 players. Party tables colour each player by token (`web/shared/palette.json`):
rocket Sky #1EA0FF, teapot Wine #7A1838, bicycle Olive #6F7A1A, lighthouse Teal #0E8C8C, key Violet
#8A5CF5, umbrella Onyx #1E2228, anchor Rose #D88890, kite Slate #727078 (each at least 19 CIEDE2000
from every property-group colour). Classic rooms keep the engine's seat colours (8 of them).

What players see (`state.board[].name`, log lines, card texts): PAYDAY (square 0, type `go`),
Headlines (deck and type `chance`), Post (deck and type `chest`), Stamp Duty and Council Tax (type
`tax`), Jail, City Park (type `parking`, jackpot `freeParking`), Busted (type `goToJail`), Power
Station and Reservoir (utilities), and the Jail pass (`jailCards`, action `use_jail_card`). Types,
keys, card ids and actions keep their names. Log lines read "passed PAYDAY and collected $200",
"used a Jail pass", "confirmed the Headlines card" and "collected the $N City Park jackpot".

`state.openingOrder` (roll for turn order) gains `rollingIds` (who rolls in the current round; it
doesn't shrink as they roll, and is `[]` once the order is set) and `ties` (`[{round, total,
playerIds}]`, one per tie that caused a re-roll). Show each player's round-1 roll, and "roll again"
only to `pendingIds`.

### Additions agreed after the first client build
- `state.turnCounts` (map playerId → completed turns) and `state.turnNumber` (1 + total completed
  turns) are injected by the server so clients can show "Turn 23" and "loan due in N turns".
- `offer_loan` carries `direction:"lend"|"borrow"` (see §3a).
- `trade-quote` accepts `seatId` alongside a `tableToken` for controller/keyboard seats.
- The table's lobby actions (e.g. `{type:"start"}`) are sent with `token: tableToken` and no `seatId`.
- `POST /api/rooms/{code}/rejoin` returns **404** when this device holds no seat in the room.
- `POST /api/rooms/{code}/ai` accepts `{token, difficulty:"easy"|"normal"|"hard"|"expert"|"grandmaster"}`
  (default `normal`; anything else is a 400 "Choose easy, normal, hard, expert or grandmaster"); players gain
  `difficulty` for AI seats. `normal` is `internal/ai`; `easy`, `hard`, `expert` and `grandmaster` are
  `internal/hardai` bots. Hard runs
  Monte Carlo rollouts, about 250 ms per key decision, on a copy of the game outside the server lock.
  Expert uses the same search and time budget with two models learned by self-play and embedded in the
  binary: tuned heuristic knobs (`internal/hardai/expert_params.json`, its baseline and rollout policy)
  and a value network (`internal/valuenet/model.bin`, the probability of winning from a position) that
  scores short rollouts. Like Hard it sees only public information. The clients cycle the lobby's
  computer level easy → normal → hard → expert → grandmaster. Method and measured strength:
  `docs/ai/EXPERT.md`, `docs/ai/GRANDMASTER.md`. Grandmaster thinks up to 2.5 s on a big decision.
- **Prices and counter-offers (0.10.1).** Hard, Expert and Grandmaster name their own trade price:
  `trade-quote` to such a seat answers with the price its own search names (computed outside the server
  lock, so it can take a moment; **400** with a message starting "No deal" when no price works, e.g. "No
  deal: that would give you a full set.", "No deal: not for any price you can pay.", "No deal at any
  price."; "The game moved on; ask again" if the deal's deeds or the turn changed meanwhile). The price
  is remembered for the round and honoured: an offer at or above it is accepted. These levels also
  answer a person's `offer_trade` by that price: at or above it, accepted; below it, a counter-offer at
  exactly the price (the engine action `counter_trade`, see below), at most twice per deal and turn, then a
  plain decline whose log line gives the price ("Alpha: My price is still $650."). When no cash price works,
  Expert and Grandmaster may counter with one of the person's deeds added (screened by the value network,
  priced the same way); otherwise they decline with the reason in the log. Easy and Normal keep the
  Normal heuristic's quote (their own judgement) and don't counter. Offers between computers are
  answered as before.
- **`counter_trade`** `{type:"counter_trade", offer:{cash, properties}, request:{cash, properties}}` by the
  target of the pending trade replaces it with a trade from the target back to the proposer, in the same
  turn: `trade` gains `counterTo` (the answered trade's id), `original` (its `{offer, request}`) and
  `counterRound`; the proposer then sends `accept_trade` (settles the counter's terms) or `reject_trade`.
  A computer's `reject_trade` / `counter_trade` may carry `reason`, logged as "<name>: <reason>". At most 4
  counters per chain ("Too many counter-offers").
- **Computer offers to people.** Every level obeys one set of rules (enforced by the server): a deal a
  person declined is never offered to them again in the same or a worse form, by any computer (the
  person's best declined terms per deed set are a floor; a new offer must beat it by 10% and $20); at
  most 2 such improved offers per deed set, computer and game; one ask per deed per round (any computer);
  never for a deed the person marked not selling. `POST /api/rooms/{code}/not-selling`
  `{token, seatId?, spaceIndex, on:bool}` → `state` marks one of the caller's deeds (players gain
  `notSelling:[spaceIndex…]`; a new game clears it). Computers' counter-offers are part of the person's own
  negotiation and don't count as asks.
- `POST /api/rooms/{code}/rematch` `{token}` (host player or table, after `finished`) → same room code,
  same seats/devices/AI, fresh game in the lobby; every connected client receives the new state.
- WebSocket events are `{"type":"event","event":{"type":"dice",…}}`.
- `GET /api/server` doubles as the party-mode capability check (404 on the old Node server).

### Getting back in (seat takeover and table resume)
A seat or table belongs to a browser (device id in `localStorage`). The **same browser** gets back in by
itself (`/rejoin`, or `/resume` from the browser that was the table). A **new** browser, phone or
private tab can take over a seat or the table once the old one is gone (offline) **and the game says
yes**, so a stranger with the link (or a friend in a private tab) can't walk into someone's seat.

**Who can say yes.** Every request ("claim") appears, with a 4-digit code, in `state.claims` of: the
table screen; the host player; the seat's own player (if they come back); and, while neither the table
nor the host is connected, every other player still in the game. They tap approve or deny, or the
person asking types the code shown there. That is how a host who lost their own phone gets back in:
the code is on the table screen (without a table, any other player sees it). A game where nobody else
could ever answer (a solo game against computers; a table with only keyboard/controller seats) lets a
new device in only from the network address the seat or table screen was last used from.

- `GET /api/rooms/{code}/summary` players gain `id`, `online` (bool) and `reclaimable` (bool: a human
  phone/browser seat, not bankrupt, offline now, so it can be asked for). The summary gains
  `tableOnline` (bool). Computer seats and the PC's keyboard/controller seats are never reclaimable.
- `POST /api/rooms/{code}/reclaim` `{playerId, approvalCode?}` with the device header:
  - this device's own seat → **200** `{code, token, playerId, state}` (a rejoin, no approval);
  - another device's offline seat → **202** `{pending:true, claim:{id, kind:"seat", playerId,
    status:"pending", expiresAt}}`. Asking again returns the same request. Once approved, the same
    call returns **200** `{code, token, playerId, state}`; with the right `approvalCode` it does so at
    once. The seat then moves to this device and the old browser's sessions end (its WebSocket closes
    and its token stops working, so it shows the join screen). The asking device is never told the
    code.
  - Errors: **409** "That seat is still connected. Close the game there first, or wait 20 seconds."
    when the seat is online; **400** "That seat can't be taken over" for computer/PC seats or bankrupt
    players; **400** "This device already has a seat at this table" when the device holds another live
    seat here (use `/rejoin`); **403** "That code doesn't match. …" (after 5 wrong codes the request is
    refused); **403** "The game said no to this request. …" for a minute after a no; **403** "Nobody
    else at this game can let a new device in. …" (solo game, different network); **429** "Too many
    devices are asking to join this game. …" (6 open requests per room). Works in the lobby and
    mid-game. Rate-limited like `/rejoin`.
- `POST /api/tables/{code}/resume` `{approvalCode?}` (device header; required when approval is
  needed) → **200** `{code, tableToken, state}` for the browser that was the table, for the host
  player's own device, and for a table room with no phone players when asked from the network the
  table was last used from; otherwise **202** `{pending:true, claim:{id, kind:"table", playerId:null,
  …}}`, answered by the host player (or, while the host is away, any player). Same `approvalCode` and
  errors as `/reclaim`, plus **409** "This table is open on another screen. Close it there, or wait 20
  seconds." while a table screen is online, **403** "Nobody at this game can let a new table screen in.
  …", and **401** without a device when approval is needed. The web client registers a device before
  `POST /api/tables` and `/resume`. Rate-limited.
- `GET /api/rooms/{code}/claims/{id}` (the asking device's header) → `{id, kind, playerId, status:
  "pending"|"approved"|"denied", expiresAt}`; **404** once it expired, or for another device. Poll it
  (every 2 s is fine) while waiting, then repeat the `/reclaim` or `/resume` call.
- `POST /api/rooms/{code}/claims/{id}` `{token, decision:"approve"|"deny"}` (`token` is a session that
  sees the claim; the table sends its `tableToken`) → `state`. **403** "Only the host or the table
  screen can answer this request"; **404** when it expired. Requests wait 5 minutes; an approval must
  be used within 2 minutes.
- `state.claims` (only for viewers who may answer; omitted when there are none):
  `[{id, kind:"seat"|"table", playerId, playerName, deviceName, code, createdAt, expiresAt}]`. Clients
  show an approve/deny prompt with the code (`web/shared/claims.js` is a minimal one), and on the
  asking device a "waiting for a yes" screen with a code box.
- A player or table client whose session ended this way gets the usual invalid-session error
  ("Your session is invalid; please join the room again") on its next request, and its room
  WebSocket receives `{"type":"error","error":"This session moved to another device"}` and closes
  (`LiveRoom` treats both as fatal). Clients should say the seat moved and show the join screen.
### Ending a game, room settings
- `POST /api/rooms/{code}/end` `{token}` (host player or table, game in play) → `state`: ends the game for
  everyone now. The richest player still in wins, by `engine.FinalScore`: `engine.NetWorth` (cash +
  deeds at price, half if mortgaged, + buildings at cost + loans owed to them − loans they owe) with a
  pending debt settled (the debtor's score minus it, a player creditor's plus it). Pending purchases,
  auctions, debts, cards, trades and loan offers are then dropped. Clients that preview "who would win"
  should count a pending debt the same way. Then `/rematch` works as usual. **400** "The game is not in
  progress" otherwise; **400** "Only the host can perform this action" for other players.
- `POST /api/rooms/{code}/settings` `{token, computerSpeed?:"slow"|"normal"|"fast", listed?:bool,
  botOffers?:"normal"|"fewer"|"off"}` (host player or table) → `state`. Party rooms' state gains
  `roomSettings:{computerSpeed, listed, botOffers}` (defaults `"normal"`, `false`, `"normal"`).
  `computerSpeed` scales the pause before computer moves (×1.7 / ×1 / ×0.4)
  and the dice-and-hop lead before a card. `listed: true` shows the game to everyone in
  `GET /api/games` (**400** "listed must be true or false"). `botOffers` is "Computer trade offers to
  people": `normal` (the rules above), `fewer` (also at most one computer offer per person between two
  of their turns), `off` (none) (**400** "Choose normal, fewer or off").
- Computers wait after a drawn card so people can read it: 2 s for the dice and hop (scaled by the
  speed), then max(3.5 s, 2 s + 55 ms per character), capped at 9 s. That reading time is **never
  shortened** at "fast" (it is ×1.7 at "slow"); clients hold the card on screen for it.

### Phone-hosted games and the games list
- `POST /api/games` `{name, tokenKind?, gameType?}` (device header required) → **201** `{code, token, playerId, state}`: a
  party game started from a phone. The creator is the host player (lobby rights: rules, add computers,
  start, rematch); others join with `/join` as usual. There is no table screen until a PC opens it with
  `POST /api/tables/{code}/resume`, which the host approves (or the host's own device opens it); that
  PC becomes the table.
- `GET /api/games` (device header optional) → `{games:[…]}`: **only the games this device is part of**
  (it holds a seat, is or was the table screen, watches, or was invited with `/api/invites`) **plus the
  games their host opened to everyone** (`roomSettings.listed`). Without a device, only listed games.
  Each entry is the `/summary` object plus `mine` (this device holds a seat), `playerId` (when mine),
  `listed`, `online` (any human or table screen connected) and `updatedAt` (ms). In a listed game you
  are not part of, every `reclaimable` is `false`. Order: your games first, then lobbies, then games in
  play, then finished; newest first within each. Left out: empty lobbies whose table screen is gone,
  and finished games nobody is connected to.
- **Stand-in for away players.** When the game waits on an offline human (phone closed, PC table gone
  for its keyboard/controller seats) for 30 s, the computer plays for them until they are online
  again: safe moves only (roll, buy or decline, bid or pass, pay, end turn, raise cash; it never
  proposes or accepts trades or loans and never builds). In "choose dice" mode it picks random dice
  for them. `state.players[].autopilot` is `true` while it does (omitted otherwise). Nothing to call:
  coming back online hands the seat back. A person who is connected but doesn't answer for 2 minutes
  gets the stand-in too; it stays until a command from their seat. The host can also switch it on
  ("Computer plays"), and `players[].standIn` tells the person what happened meanwhile: see §3c.
- **Nobody connected.** When no person (table screen or human player) has been connected for 2 minutes,
  computers stop playing, so computer-only games don't run forever; they carry on as soon as someone
  connects again.

### Game types
One server hosts **Estate**, **poker** (No-Limit Texas Hold'em; wire format in
[POKER-API.md](POKER-API.md)), **Ludo** (wire format in [LUDO-API.md](LUDO-API.md)), the **quiz**
(wire format in [QUIZ-API.md](QUIZ-API.md)) and **Near Thing** (wire format in
[CLOSEST-API.md](CLOSEST-API.md)). A room's type
is fixed when it is created:
- `POST /api/games` `{name, tokenKind?, gameType?}` and `POST /api/tables` `{gameType?}`: `gameType` is
  `"estate"` (the default), `"poker"`, `"ludo"`, `"quiz"` or `"closest"`; anything else is **400** "unknown game type". The classic
  `POST /api/rooms` always makes an Estate game (the Node-compatible surface is unchanged).
- Party states carry `gameType` (`"estate"` | `"poker"` | `"ludo"` | `"quiz"` | `"closest"`) and `aiLevels`, the computer levels
  the room offers (`["easy","normal","hard","expert","grandmaster"]` for Estate, `["easy","normal"]` for poker,
  `["easy","normal","hard"]` for Ludo, the quiz and Near Thing); `POST /ai` refuses any other level ("Choose easy or normal").
  Poker, Ludo, quiz and closest states also have `game: "poker"` / `"ludo"` / `"quiz"` / `"closest"`; Estate states have no `game` field, so
  clients pick the screen by `state.game`.
- `/summary` and `GET /api/games` entries gain `gameType`. For poker, `turn` is the hand number, `rules`
  are the poker settings, and `openSeats` also counts while the game runs when late joining is on.
  Devices' `recent[]` entries record `gameType`.
- Poker is **phones only**: `POST /seats` answers **400** "poker needs a phone per player"; the table
  screen is a viewer that sees face-up cards only. Its host actions are `start`, `settings` and `deal`.
  `/join` during a poker game is up to the engine (`settings.lateJoin`). `trade-quote` is Estate-only.
- **Ludo** keeps Estate's keyboard and controller seats (nothing is secret); `/join` works in the lobby
  only; its host actions are `start` and `settings`; Ludo games are recorded as their event stream
  (GAME-RECORDS.md, "Poker and Ludo"). Its stand-in plays for an away player as the Normal computer would (rolls and moves).
- The **quiz** is phones only (final bets are secret): `POST /seats` answers **400** "the quiz needs a
  phone per player"; `/join` works in the lobby only; its host actions are `start` and `settings`. It has
  no stand-in (an away player simply does not answer), its computers are played by the engine, a
  rematch deals categories the room has not played yet, and its games are recorded as their event
  stream, every tap with its timing (GAME-RECORDS.md). For a quiz, `turn` is the number of clues played so far.
- **Near Thing** is phones only too (guesses and bets are secret): `POST /seats` answers **400**
  "Near Thing needs a phone per player"; `/join` works in the lobby only; its host actions are `start`
  and `settings`. Like the quiz it has no stand-in, its computers are played by the engine (and the
  `fill` setting adds Normal ones at the start), a rematch asks questions the room has not been asked,
  and its games are recorded as their event stream, every guess and bet (GAME-RECORDS.md). For a closest
  game, `turn` is the number of questions asked so far.
- Shared by all: rooms and codes, QR join, devices, friends, invites, presence, seat reclaim, table
  resume, End Game (poker: ranked by chips, or by chips won in a cash game), rematch (same type and
  settings), computer speed, the room limits and rate limits, and persistence across restarts (a poker
  hand resumes exactly where it was; `rooms.json` entries gain `gameType` and `eventSeq`).
- Poker's stand-in for an away player (30 s, as for Estate) **only checks or folds**; it never bets, calls
  or decides a rebuy, and the player stays dealt in.
- `estate serve --poker-bot cmd:/path/to/bridge` (or an http URL; env `ESTATE_POKER_BOT`) plugs an
  external poker AI into "hard" seats (POKER-API.md §8); without it poker offers Easy and Normal.

### Limits and overload protection
- Rooms: 100 in all. Each client address may have 6 live rooms it created and each device 3 (the server
  machine itself is exempt). At the cap, that creator's own abandoned rooms (nobody connected, and not
  in play or idle for an hour) are deleted first; otherwise **429** "You already have several games open
  on this server. Finish or leave one first." A full server answers **503**.
- A room expires 6 h after a person last used it (computer moves and spectators don't count; an open
  player or table screen does), and 30 minutes after creation if nobody ever sat down.
- Rate limits per client address (IPv6 per /64), never per token: state reads 600/min; entries (create,
  join, watch, tables, games, device, rejoin, reclaim, resume) 30/min; everything else, actions sent
  over a room socket included, 180/min; 60
  WebSocket opens per minute and 40 open WebSockets; 20 new device records per minute. Expired buckets
  are dropped by the 5-second sweep. `X-Forwarded-For` is trusted only from a loopback proxy, and only
  its last entry.
- Sessions: spectator sessions unused for 10 minutes are dropped; a browser that rejoins replaces its own
  unused sessions; rooms keep at most 64 sessions.
- `GET /api/server` reports `lanURL` only to clients on the local network or tailnet (`null` otherwise).
- Trade quotes: a computer honours a price it quoted for the rest of that turn only while the deeds in
  the deal and their colour group (or all railroads/utilities) are unchanged.
- A crash in computer play is logged and retried after 15 s instead of taking the server down; after 10
  rejected computer moves in a row, computers pause 15 s and try again.
- The HTTP server has read (30 s), write (60 s), header (10 s) and idle (120 s) timeouts; WebSockets are
  exempt once open.

## 3a. House rules and loans (engine)

All new fields are **omitted in default games**; treat a missing `rules` as the defaults and a
missing `freeParking` as 0.

- `state.rules` (effective, only when not default): `{loans:false, freeParkingJackpot:false,
  auctions:true, startingCash:1500, auctionClock:0}`. `state.freeParking`: the City Park jackpot pot (players see
  "City Park"; the keys keep their old names). New party games have the timed auction
  (`auctionClock:8`, §3c), so their `state.rules` is present from the start.
- `state.loans[]`: `{id, lenderId, borrowerId, principal, repay, dueTurn, collateral:[spaceIndex],
  status:"active"|"repaid"|"defaulted"|"cancelled", createdAt}` (public to every viewer).
  `dueTurn` counts the borrower's completed turns (`turnCounts`).
- `state.loanOffer`: `{id, direction:"lend"|"borrow", proposerId, targetId, lenderId, borrowerId,
  principal, repay, dueTurns, collateral, returnPhase, createdAt}` with phase `"loan_offer"`.
  `currentPlayerId` stays the proposer; only `targetId` may `accept_loan`; target or proposer may
  `reject_loan`. AI seats decide with `ai.AcceptsLoan` (interest, risk, reserve, leader checks) and may ask for loans in debt.
- `state.debt.loanId` when a debt comes from an overdue loan. `lastLanding.kind:"freeParking"`.

| Action | Who / when | Payload |
|---|---|---|
| `set_rules` | host, lobby | `{rules:{loans?, freeParkingJackpot?, auctions?, startingCash?, auctionClock?}}` (cash 500–5000, step 50; clock 0 or 5–30 s, §3c) |
| `offer_loan` | current player on own turn (`lend` or `borrow`); debtor in `debt` (`borrow`, default) | `{direction, targetId, principal (1–10000), repay (P–3P), dueTurns (1–20), collateral:[…]}` |
| `accept_loan` | `loanOffer.targetId` | `{}` |
| `reject_loan` | target or proposer | `{}` |
| `repay_loan` | borrower, own turn (not in debt) | `{loanId}` |

At the start of a borrower's turn, due loans auto-repay if cash allows; otherwise pledged
collateral passes to the lender, or with no collateral a debt to the lender is created (normal
debt flow → pay or bankrupt to the lender). Pledged properties can't be traded, built on or
mortgaged ("<name> is security for a loan"). Exact error strings live in `internal/engine/loans.go`
and `rules.go`.

## 3b. Rules fixes after the 3 Nov 2026 review (engine)
These differ from the old JS engine (`src/game.js`); its recorded games still replay in a test-only
legacy mode (`internal/engine/legacy.go`).
- `sell_building` on a hotel when the bank has fewer than 4 houses breaks it down to the houses the
  bank has (possibly none) and pays half the build cost per level, so a hotel owner in debt can raise
  cash instead of going bankrupt.
- `bankrupt` is refused while cash covers the debt: "You have enough cash to pay this debt".
- `pay_bail` is refused under $50: "Not enough cash to pay bail".
- Unmortgaging costs the mortgage value plus 10% rounded up, in whole dollars (a $200 railroad: $110);
  the 10% fee for receiving a mortgaged deed is rounded the same way.
- A used Jail pass goes back under its deck; on bankruptcy, held cards pass to a player
  creditor or go back to the decks.
- The "nearest utility" Headlines card (deck `chance`) rolls the dice again for the ten-times payment.
- An AI's refused offer to sell deeds puts those deeds on the same trade cooldown as refused requests.
- Up to 8 players: "Rooms support up to 8 players".

## 3c. Timed auction, host controls, I'M BACK, card rent (27 Nov 2026)
All additive: existing keys, ids and actions are unchanged. Games saved before this change, the
game.js parity traces and classic rooms (`POST /api/rooms`, the `/classic/` client) keep turn-order
auctions and have no host controls; there a mid-game `/remove` still answers "Players cannot be
removed after the game starts".

### Timed open auction (UX-03)
- House rule `auctionClock` (whole seconds): `0` is the old turn-order auction, otherwise 5–30. New
  party games (`POST /api/tables`, `POST /api/games`) start at **8**; classic rooms at 0. The host
  changes it in the lobby with `{type:"set_rules", rules:{auctionClock}}`; anything else is **400**
  "The auction clock must be 0 (turn order) or 5 to 30 seconds". It shows in `state.rules.auctionClock`
  and `state.settings.rules.auctionClock` (so a new game has `settings.rules:{auctionClock:8}`), and
  the "Rules updated: …" log line ends ", auction clock 8 s" while it is on. `/rematch` keeps it.
- With the clock on, `state.auction` gains (none of these appear in a turn-order auction):
  - `clockMs`: the clock's length in ms (`auctionClock` × 1000).
  - `endsAt`: Unix ms (server clock) when the auction closes unless someone bids first. It is set
    when the auction opens and reset to *bid time + clockMs* by every accepted bid.
  - `bids`: `{playerId: amount}`, each bidder's latest (highest) bid; omitted before the first bid.
  - `passedIds`: who dropped out, in order; omitted before the first pass.
  - `currentPlayerId` is `null`: nobody has the turn. `activeIds` is everyone still in, `currentBid`
    and `bidderId` are the leader as before (`bidderId` `null` and `currentBid` 0 before any bid).
    `turnPos` is internal. The top-level `state.currentPlayerId` stays the player whose turn it is.
- Who may act: every player in `activeIds` except the leader (`bidderId`), at any moment, in any order.
  - `{type:"bid", amount}`: a whole number above `currentBid` and no more than the bidder's cash.
    Errors (**400**): "You are not in this auction", "You already have the top bid", "The auction has
    closed" (it arrived at or after `endsAt`; the server's close is on its way), "Your bid must exceed
    the current bid" (also when two bids cross: the second loses), "Your bid cannot exceed your cash".
  - `{type:"pass_auction"}`: drops the player out of this auction. It does not touch the clock.
    Errors: "You are not in this auction", "The top bidder cannot pass".
- How it ends:
  - When everyone but the leader has passed, the leader wins at once.
  - When everyone passes with no bid, the deed stays with the bank, as before.
  - Otherwise the server closes it when the clock runs out: the leader wins and pays `currentBid`;
    with no bid the deed stays with the bank. Log: "<name> won <space> for $N" or "<space> received
    no bids"; the game returns to the turn (`auction:null`).
  - If the leader leaves the auction (bankrupt or removed), their bid is withdrawn ("<name>'s bid was
    withdrawn"), `currentBid` goes back to 0 and the clock restarts at full length.
- For clients:
  - Count down to `endsAt` on the server's clock. Socket pongs carry `now` (server ms), so take
    offset = `now` − (send time + rtt/2) and show `endsAt − (Date.now() + offset)`.
  - Call "going once" when 3000 ms or less are left (`engine.AuctionGoingOnceMs`). This is
    presentation only: the server sends nothing for it.
  - Quick bids +$10 / +$50 / +$100 send `{type:"bid", amount: currentBid + step}`; hide a step the
    player can't afford. Any amount is allowed; the steps are only the screen's shortcut.
  - Each accepted bid still sends the `auction{spaceIndex,bid,bidderId}` event.
- Computers bid in the open auction within their usual limits and move within half of the time left
  on the clock, so they never hold it up. A timed auction never waits on a person: the away (30 s)
  and idle (2 min) stand-in clocks don't run during it. A person the stand-in already plays for gets
  its bids or passes.
- Game records: the server's close is the action `{type:"close_auction", now:<server ms>}`, recorded
  with source `"server"` and no `by`. Clients can't send it (below).

### Host controls mid-game (UX-02)
For an Estate game in play in a party room (a table, or a phone-hosted game). Only the host may use
them: the host player's phone sends its `token`, the table screen its `tableToken`. Each answers
with the caller's `state`, and every client gets the new state over its socket.

| Method | Path | Body | Does |
|---|---|---|---|
| POST | `/api/rooms/{code}/standin` | `{token, playerId}` | **Computer plays**: the stand-in plays for that person from now on until a command from their own seat (I'M BACK or any action). If it was already playing for them because they were offline, it now stays after they come back online too. |
| POST | `/api/rooms/{code}/skip` | `{token, playerId}` | **Skip turn**: the server plays the stand-in's safe moves for that person until the game has moved on from them (it no longer waits on them and it is not their turn). A command from their own seat cancels it. |
| POST | `/api/rooms/{code}/remove` | `{token, playerId}` | **Remove** (in the lobby it removes the seat as before): takes the player, person or computer, out of the game. |

Errors (all **400**, plus the usual session errors):
- Any of the three, from anyone but the host: "Only the host can perform this action".
- `standin`, `skip`: "Host controls are only for Estate party games in play" (lobby, finished, a
  classic room or another game type); "Choose a person still in the game" (unknown id, a computer, or
  a player already out).
- `skip`: "The game is not waiting on that player" (they have no decision pending and it is not their
  turn).
- `remove`: "Player not found", "That player is already out of the game", "Wait until the turn order
  is set" (during the roll for turn order).
- The actions `{type:"remove_player"}` and `{type:"close_auction"}` sent by any client through
  `…/action` or the socket: "Only the server can do that".

What clients see:
- **Computer plays**: `players[].autopilot:true` and `players[].standIn` with `by:"host"` (below).
- **Skip turn**: `players[].skipped:true` while the skip is pending (omitted otherwise). The safe
  moves are: roll when it is their turn (also in jail), decline a purchase, pass an auction, confirm
  a card, turn down a trade or loan offer, end the turn. In debt it pays; short of cash it sells
  buildings and mortgages first, and declares bankruptcy only when nothing is left to raise. It never
  buys, builds, bids or trades. The skipped person's moves are recorded with source `"host"` and
  `via` the host's input (`"table"` for the table screen).
- **Remove**: their deeds and buildings go back to the bank, their cash is gone, and a debt they owe
  is written off ("<name>'s debt of $N was written off"; the creditor gets nothing). Then they are out
  like a bankrupt player: `players[]` shows `bankrupt:true`, `cash:0`, `outOrder` and the new
  `left:true`; `lastBankruptcy` is set with `creditorId:null`; the log says "<name> left the game;
  assets returned to the bank"; and the socket event is `left{playerId}` (not `bankrupt`). If it was
  their turn, the turn passes on; if one player is left, the game finishes. A leading bid of theirs
  in an auction is withdrawn as above. Their seat closes: their sockets get
  `{"type":"error","error":"The host removed you from the game"}` and close, their token stops
  working, and the seat can't be rejoined or taken over. A host who removes themselves passes host
  rights to the earliest remaining phone player. Recorded as the action `{type:"remove_player",
  playerId}` with source `"host"` (`by` is the host player, or none for the table screen).

### I'M BACK and the while-away list (UX-07)
- Action `{type:"back"}` (`POST …/action` or a socket action; the table screen adds `seatId` for its
  keyboard or controller seats) hands the seat back from the stand-in, whatever switched it on, and
  does nothing else: no game step, no log line. Without a stand-in it dismisses the while-away list.
  It always answers with `state`. Any other action from the seat also hands it back, as before, and
  cancels a pending skip.
- `players[].standIn` (Estate, people only; omitted when there is nothing to show):
  `{since, logId, cash, by, until?}`:
  - `since`: when the stand-in took over (Unix ms, the game clock, like `log[].at`).
  - `logId`: the id of the newest log entry at that moment (`""` if none). The while-away list is the
    log entries after it. The log keeps only its newest 120 entries, so if `logId` is gone, start
    from the oldest entry there.
  - `cash`: their cash at that moment, so the screen can show the change.
  - `by`: `"away"` (offline 30 s), `"idle"` (connected but not answering for 2 minutes) or `"host"`
    (the host's Computer plays).
  - `until`: when the seat was handed back. It is absent while the stand-in still plays.
- The mark appears at takeover and stays after the handover (with `until`) until the person's next
  command (`back` or any action). So: show "I'M BACK" while `autopilot` is true, and the while-away
  list while `standIn.until` is set. If the stand-in takes over again before the list is dismissed,
  `since`, `logId` and `cash` stay, so the list covers both spells; `by` is updated and `until`
  cleared. A new game (rematch) starts with no marks.

### RENT ×2 / ×10 (UX-05)
Rent set by a "nearest" Headlines card is shown as a multiple. The amount is the engine's, unchanged.
- The rent `pay` event (`reason:"rent"`) and `state.lastLanding` (`kind:"rent"`) gain:
  - `base` and `multiplier`, with `amount = base × multiplier`.
  - The nearest-railroad cards (pay double rent): `base` is the railroad's usual rent for its
    owner's railroads, and `multiplier` is 2.
  - The nearest-utility card (ten times a fresh roll): `base` is the new roll's total, `multiplier`
    is 10, and `dice:[a, b]` is the roll (also logged as "<name> rolled a + b = n for the utility").
- Ordinary rent, including landing on those squares by dice, has none of these keys.
- The server reads them from the log, so the engine's JSON and the game records are unchanged.

## 4. Pages

| Path | Serves |
|---|---|
| `/` | PC table screen (`web/table/`) |
| `/play`, `/j/{code}` | Phone app (`web/phone/`) |
| `/classic/` | The original client (`public/`) |

## 5. Running

```
estate                     # serve on :3210 and open the table screen fullscreen
estate serve --addr :3210 --public-url https://estate.example.ts.net:8443 --data ~/.local/share/estate
```
Static files are embedded in the binary (`embed`). Device/friend data persists in `--data`
(`estate.json`), and every open game is saved to `rooms.json` there and restored on restart (same codes,
same session tokens; games idle for 6 h are dropped), so updating or rebooting the server doesn't end games.
Every Estate game is also kept as a replayable record in `--data/games/` (`rooms.json` gains `recordId`
per room); no endpoint serves records. See [GAME-RECORDS.md](GAME-RECORDS.md) and `cmd/estate-replay`.
