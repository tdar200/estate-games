# Game records

Every Estate game the server hosts is kept as a **record**: the game as it stood
in the lobby, then every change to it in order, with the dice, cards, ids and
times it used, so that it replays exactly through the rules engine. Records are
for looking back at a game (how was it won?) and for finding what people do that
the computers get wrong. They stay on the server's disk; no endpoint serves them.

- `internal/gamerecord`: the format, the replay checker and a recorder for tools.
- `internal/server/recorder.go`: how the server records its games.
- `internal/gameanalysis` and `cmd/estate-replay`: checking and analysing records.

## Where records are kept

```
<data>/games/2026-11-03-1942-4AUCC.json.gz    finished games (gzipped JSON)
<data>/games/live/2026-11-03-2015-K7PQX.jsonl games being played (one JSON line per event)
```

`<data>` is the server's `--data` directory (`~/.local/share/estate` on the PC).
A record is named after its start, in the server's local time, and the room code:
`YYYY-MM-DD-HHMM-<code>`; a second game started in the same room in the same
minute gets `-2`. Without a data directory nothing is recorded.

**What is recorded.** Every Estate game, in PC table rooms, phone-hosted games and
classic rooms, from the moment the host starts it, and every poker, Ludo, quiz and
closest game (see "Poker and Ludo, and the quiz"). Lobby changes before the start are not steps; the record
starts from the lobby as it was when the game started.

**When a record is finished.** A record is written to `<id>.json.gz` as soon as
its game ends, whichever way it ends (`outcome.reason`):

| reason | the game ended because |
|---|---|
| `finished` | everyone but the winner went bankrupt |
| `ended` | the host ended the game early (the richest player wins) |
| `abandoned` | the room was closed while the game was running (a creator over their room quota) |
| `expired` | nobody used the room for 6 hours, or the room did not come back after a restart |
| `reset` | the room went back to the lobby while the record was still open (should not happen: a rematch needs a finished game, whose record is already written) |

A rematch therefore never loses a game: the finished game's record was written
when it ended, before the room went back to the lobby.

**Retention.** The newest 500 records or 200 MB, whichever is reached first
(newest by start time, then by when they were written). Older ones are deleted
after each new record.

**Privacy.** A record holds what the game itself holds (player names and ids,
the board, the log) plus each seat's kind, computer level, input (phone,
keyboard, controller, classic) and token. It never holds session tokens, device
ids or secrets, or IP addresses. Actions are stored with only the keys the engine
reads, so nothing else a client puts in an action is kept.

## Poker and Ludo, and the quiz

A poker, Ludo, quiz or closest game is kept in the same place, under the same names and limits,
but holds something simpler: format `estate-table-record`, written by
`internal/server/tablerecord.go`.

- `initial`: the game when the record starts, and `final`: how it ended.
- `events`: every engine event in order, exactly as the screens got it, with the
  time it was recorded.
- `holes`: every player's hole cards for every poker hand, **shown or not**.
- `players`, `standIns` and `outcome` (reason, winner, places, poker chips).
- Quiz: every tap with its timing, for tuning the computers and the clue tiers
  (QUIZ-API.md §6). Each board clue's `reveal` event carries `taps`, and each
  player's `finalReveal` event their final answer as `tap`, as
  `{playerId, choice, correct, ms?, arrival, latency, effective}`: `ms` what the
  phone reported (absent when it sent nothing usable), `arrival` when the tap
  reached the server after the choices opened, `latency` the player's latency
  then (0 for a computer) and `effective` the time the tap counted as. The
  quiz's `initial` and `final` are the engine's own snapshots, so they hold every
  dealt clue's answer, the gold squares and the bets. There are no stand-ins.
- Near Thing: every guess and bet, for re-tuning the odds and the computers
  (CLOSEST-API.md §5, §6). Each question's `reveal` event carries `questionId`,
  `answer`, `guesses` as `{playerId, value, afterMs, changed}` (`afterMs` from
  the question opening to the final guess) and `bets` as `{playerId, chips,
  stake, stakeSlot, auto}` (`auto`: placed by the bet timer). `initial` and
  `final` are the engine's snapshots, with every dealt question's answer. There
  are no stand-ins.

These records are for looking back at a game: nothing replays them, so `check`
skips them and `analyse` is Estate-only. Read them with `estate-replay info`, or
`estate-replay hands` for poker. A poker cash game has no winner until the host
ends it.

**Privacy.** Hole cards are in the record, so a poker record shows what everyone
folded. As with Estate records, they stay on the server's disk and no endpoint
serves them; session tokens, device ids and addresses are never written.

**Restarts.** As for Estate: `rooms.json` names the journal, a restart carries on
in it, and if a crash left it ahead of the saved game it is cut back to that game.
A game with no usable journal records the rest of it (`startsMidGame`).

## Records from 0.10.1 to 0.11.1

Those versions dropped `reason` from stored actions, but the engine logs it, so a
game in which a computer answered a person's offer with a reason stops replaying
at that step. `estate-replay repair` restores them: it tries each text the
computer could have said and keeps one only if the replay then reproduces the
step's recorded state hash, which covers the log. The original file is left alone
(the result is `<id>.repaired.json.gz`, or goes to `-out`). `info` reads an
unrepaired record's winner and standings from its final state.

## How the server records (and never slows play)

Under the server lock, the server only *captures* a step: while the engine
applies an action it reads the clock once (every clock read in that step returns
that time), records the ids the engine creates and every random number it draws.
The state hash is computed from the game snapshot the server makes after every
change anyway. The step's line is then handed to a background writer, which
appends it to the game's journal (`live/<id>.jsonl`, synced to disk) and, when the
game ends, replays the whole journal, checks it, writes the `.json.gz` and
removes the journal. All disk work happens off the lock.

**Crashes and restarts.** `rooms.json` names each room's journal (`recordId`). On
start-up the server reads the journal of every restored game and lines it up with
the restored game by state hash:

- the journal has steps the saved game never saw (the server died between the
  two writes): those steps are dropped, so the record matches the game that goes on;
- the journal is behind the saved game (steps lost): a **resync** step holding the
  whole saved game is added, and the record goes on from there (`gaps` lists them);
- a game in play with no journal at all (saved before records existed): a new
  record starts from the game as it is, with `startsMidGame: true`;
- journals no restored room claims are finished with reason `expired`.

The server writes queued journal lines before it saves `rooms.json` when it
shuts down, so after a clean restart a record simply continues.

## The format

A record is one JSON object (gzipped). Field names are stable; new optional
fields may be added. `formatVersion` is 1; readers refuse a newer version.

```jsonc
{
  "format": "estate-game-record", "formatVersion": 1,
  "id": "2026-11-03-1942-4AUCC", "code": "4AUCC", "gameType": "estate",
  "appVersion": "0.9.0",
  "startedAt": 1793735000000, "endedAt": 1793739000000,   // Unix ms
  "room": "table",                 // table | phone (phone-hosted) | classic
  "computerSpeed": "normal",
  "diceMode": "random",            // random | choice
  "settings": {"diceMode": "random", "rules": {"loans": true}},  // the engine's settings: house rules
  "players": [                     // seat (lobby) order
    {"id": "player_1a2b…", "seat": 0, "name": "Robin", "kind": "human", "input": "keyboard",
     "tokenKind": "rocket", "color": "#…", "host": true},
    {"id": "ai_3c4d…", "seat": 1, "name": "Alpha", "kind": "ai", "difficulty": "expert", "input": "ai", "tokenKind": "teapot"}
  ],
  "initial": { … },                // the engine's full game JSON just before the start
  "steps": [ … ],                  // every change, in order (below)
  "standIns": [{"playerId": "…", "fromStep": 120, "toStep": 141, "from": …, "to": …}],

  // Filled in when the record is finished (by replaying it):
  "outcome": {"reason": "finished", "winnerId": "…",
              "bankruptcyOrder": ["…", "…"],          // first out first
              "standings": [{"playerId": "…", "place": 1, "cash": 2400, "netWorth": 5300, "score": 5300,
                             "bankrupt": false, "outStep": 0}]},
  "final": { … },                  // the game JSON after the last step
  "verified": true, "verifyError": "",
  "turns": 187, "turnOrder": ["…"],
  "trades":   [{"step": 812, "turn": 61, "proposerId": "…", "targetId": "…",
                "offer": {"cash": 0, "properties": [37]}, "request": {"cash": 300, "properties": []},
                "answer": "accepted", "answerStep": 813, "answeredBy": "ai"}],
  "auctions": [{"startStep": 40, "endStep": 52, "turn": 4, "spaceIndex": 6, "space": "…", "price": 100,
                "bids": [{"step": 41, "playerId": "…", "amount": 10}, {"step": 44, "playerId": "…", "pass": true}],
                "winnerId": "…", "paid": 120}],
  "loans": [ … ],                  // loan offers and answers (house rule)
  "gaps": [ ]                      // resync steps
}
```

### Steps

```jsonc
{
  "n": 57,                     // index in steps
  "kind": "action",            // action | order (turn-order countdown ended) | end (host's End game) | resync
  "t": 1793735123456,          // engine clock during the step (Unix ms)
  "by": "player_1a2b…",        // who acted (the host for end)
  "src": "player",             // player | ai | standin (computer playing for an away person) | server | host
  "via": "keyboard",           // a person's input: phone, keyboard, controller, classic, or table (the table screen as host)
  "fallback": true,            // the engine refused the computer's move; its safe fallback was played
  "thinkMs": 41,               // how long the computer took to choose
  "action": {"type": "bid", "amount": 120},
  "error": "",                 // set when the engine refused the action but it still changed the game
  "now": 0,                    // order: the time passed to CompleteOpeningOrder
  "v": 311, "v2": 312,         // game version before and after (the server also bumps it for presence)
  "rng": [0.4118…, 0.9921…],   // every random number drawn, in order (two per pair of dice, thirty for the start's shuffle)
  "ids": ["log_…", "move_…"],  // every id the engine created, in order
  "dice": [[3, 6]],            // the random dice rolled (pairs), derived from rng; chosen dice are in action
  "cards": [{"deck": "chance", "card": "chance-rail-1"}],   // cards drawn
  "decks": {"chance": […], "chest": […]},                   // start only: the deck order after the shuffle
  "hash": "9f1c2e…",           // state hash after the step
  "state": { … }               // resync only: the whole game
}
```

Refused actions that changed nothing are not recorded. Lobby changes are not
steps.

**The state hash** is FNV-1a 64 (16 hex digits) of the engine's game JSON
(`json.Marshal`, the same bytes as the server's snapshot) with the top-level
`version` read as 0. The version is left out because the server bumps it between
steps when people come and go; steps record it in `v` and `v2`.

### Replaying

`gamerecord.Replayer` loads `initial` and applies each step through the engine:
it sets the game version to `v`, the clock to `t`, the id generator to the
recorded `ids` and the random numbers to the recorded `rng`, then checks that the
engine accepted or refused the action exactly as recorded, used every random
number and id, reached version `v2`, drew the recorded cards, rolled the recorded
dice, and produced the recorded `hash`. The first difference stops the replay
with a `*gamerecord.Mismatch` naming the step. Nothing is re-seeded: a record
replays the same on any machine and any engine build that applies the same rules.

## Tools

```
go build -o estate-replay ./cmd/estate-replay

estate-replay check FILE...          # replay records; "ok" or the first mismatch (exit 1)
estate-replay info FILE              # players, outcome, standings, stand-ins, trades
estate-replay hands FILE             # a poker record, hand by hand
estate-replay repair [-out DIR] FILE...   # restore what 0.10.1 to 0.11.1 lost (below)
estate-replay analyse [flags] FILE   # writes <id>.analysis.md and <id>.analysis.json next to FILE
estate-replay demo -seats expert,hard,normal,easy -out game.json.gz   # record a computer game
```

`FILE` is a finished record or a live journal (`live/<id>.jsonl`), so a game
still being played can be checked or analysed too.

In Go: `gamerecord.Read`, `gamerecord.Verify`, `gamerecord.NewReplayer` (step by
step), `gamerecord.Annotate` (summaries), `gamerecord.NewRecorder` (record a game
played directly on an `*engine.Game`, e.g. an arena game), and
`gameanalysis.Analyse`.

## Analysing a game

`estate-replay analyse` goes through a record and, for every decision of every
player (computers and people), estimates every player's probability of winning
just before and just after it. It writes:

- **`<id>.analysis.md`**, "How the game was won": the outcome and a short story,
  each player's start, peak and end chances, the win-probability curve (a
  sparkline per player and a table), the turns that moved the winner's chances
  most and why (a decision, the dice and cards, or the host ending the game), the
  people's best moves, the computers' biggest blunders (and the worst of each
  kind: trades accepted or offered, bids, buys and declines, builds, mortgages,
  ending a turn without building), every trade with what it did to both sides,
  and the auctions that mattered;
- **`<id>.analysis.json`**: the same data for other tools (`gameanalysis.Analysis`):
  `players` (with `start`, `peak`, `end`, `decisions`, `worse`, `better`,
  `vsPolicy`), `curve` (win probabilities at every turn start, in `players`
  order), `decisions` (every decision with `before`, `after`, `delta`, the
  `policy` alternative and `thinkMs`), `trades`, `auctions`, `blunders`, `best`
  and `turningPoints`.

**What a decision's score means.** The oracle (`gameanalysis.Oracle`) estimates
win probabilities; the default, `RolloutOracle`, is the Expert bot's own method
in-process: it plays the position forward a few hundred times with the Expert
bot's rollout policy (the self-play-tuned heuristic, `hardai.ExpertParams`) for
every seat, to one full round ahead, and scores where each rollout stops with the
value network (`internal/valuenet`). Rollouts reshuffle the unseen decks, so the
oracle knows no more than the players did, and every position uses the same
rollout seeds (common random numbers). The positions just before and just after
a decision are both looked at to the same later turn. So a decision's score is
how much better or worse the move was than **what the Expert policy would have
done in its place**; the report shows that alternative ("Expert policy instead").
The policy is strong but not perfect: a move it would not make can still be right.
Scores add up per player (`vsPolicy`), but they are not a split of the player's
change in chances, since each is measured against the policy's move rather than
the game as played. Within a couple of points of zero a score is noise.

Rollouts make no trade offers of their own (the policy still answers offers): the
heuristic's trade search is most of a rollout's cost, and the bots' own rollouts
skip it too. A trade offer is therefore scored against not trading. `-trades`
turns the search on (several times slower).

**Speed.** On the laptop (4 threads, another heavy job running), a 250-turn
five-player game with about 1,300 steps and 1,450 estimates took about 100 s at
the default 300 rollouts and 8 s with `-quick` (24 rollouts, close to the full
numbers). `-net-only` reads positions with the value network alone (about a
second, crude for positions in mid-turn). `-workers` sets the threads (default 4).
A 200-turn game takes well under the few minutes allowed.

## Sizes

A record is about 55 bytes per step gzipped, plus a few kilobytes for the
initial and final states: a 90-turn four-computer game (528 steps) is 33 KB, a
250-turn five-player game (1,283 steps) 71 KB (343 KB unzipped). A journal is
not compressed: about 0.3 KB per step while the game runs. 500 records of long
games come to about 35 MB, well under the 200 MB cap.
