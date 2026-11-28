# Adding a game

Estate runs five games on one server. A sixth follows the same pattern. There is no plug-in
framework: a new game is its own rules package plus a few small hooks in the shared server and
screen code. This page lists every place you will touch.

Near Thing (`closest` in the code) is the newest and smallest game, so it is the best one to copy.
Read [CLOSEST-API.md](CLOSEST-API.md) first, then keep its files open next to yours.

Before you start, open an issue with the idea (use the "Idea" template). A short description of
the rules and what the TV and the phones show saves a lot of rework.

## 1. The rules: `internal/<game>/`

A plain Go package with no HTTP in it. Near Thing's is `internal/closest`:

- `game.go`, `phases.go`, `score.go`, `settings.go`: the game state, its phases, the actions players
  can take, and the rules that check them. Everything that matters (money, cards, dice, scores)
  is decided here, never on a phone.
- `view.go`: what each viewer is allowed to see. The TV and every phone get their own view, so a
  player never receives someone else's secret. `secrecy_test.go` checks it.
- `bots.go`: the computer players.
- `rules_test.go`, `sim_test.go`: rules tests, and many simulated games to catch stuck states.

## 2. The server: `internal/server/`

- `<game>_adapter.go`: wraps your game in the `Engine` and `Game` interfaces from `engine.go`.
  Copy `closest_adapter.go`.
- `<game>.go`: the room side: timers, sending state to the TV and phones, and the game record.
  Copy `closest.go` (`commitClosest`, `closestStateFor`).
- `gameinfo.go`: add a `Game<Name>` constant next to `GameClosest`. Your game reports a `GameInfo`
  so the shared code (lobby lists, rejoin, friends, End Game, rematch) can handle it without
  reading your state.
- `rooms.go` and `state.go`: add your game where they branch on `isClosest()`.
- `roomstore.go`: saving and restoring an open game across a restart.
- `tablerecord.go` and `internal/gamerecord/table.go`: the finished game's record
  ([GAME-RECORDS.md](GAME-RECORDS.md)).
- `cmd/estate/main.go`: register the engine in `Config.Engines`.

## 3. The screens: `web/`

No build step: plain ES modules and CSS (see House rules in [CONTRIBUTING.md](../CONTRIBUTING.md)).

- `web/shared/<game>-name.js`: the name players see.
- `web/shared/<game>-view.js`, `<game>.css`: drawing shared by the TV and the phone.
- `web/shared/<game>-scenes.js`: demo scenes, so every screen state can be seen without playing.
- `web/table/<game>-table.js`: the TV. Hook it into `web/table/table.js` (import, screen element,
  routing on `state.game`) and add its `<main>` and stylesheet to `web/table/index.html`.
- `web/phone/<game>-app.js`, `<game>-screens.js`: the phone. Hook it into `web/phone/phone.js` and
  `web/phone/index.html`.
- Small shared spots: the game list in `web/phone/screens.js` (NEW GAME), the game's tile and tag
  in `web/shared/poker-view.js`, its name in invites (`web/shared/invite.js`) and its music mood
  (`web/shared/music.js`).

## 4. Tests and docs

- `internal/server/<game>_test.go`: rooms, joining, reconnecting, timers, records.
- `internal/server/<game>_fixtures_test.go`: writes demo states from the real engine into
  `web/fixtures/<game>/`. Never edit fixtures by hand.
- `web/tests/<game>.check.mjs`: checks for every screen state.
- `docs/<GAME>-API.md`: the actions and the state your game sends. Add it to "Game types" in
  [API.md](API.md) and to the list in the README.

## Checks while you work

The full test run takes about 10 minutes. While you work, run just your parts:

```sh
go test ./internal/<game> ./internal/server -run '<Name>'
node web/tests/<game>.check.mjs
```

Run everything in [CONTRIBUTING.md](../CONTRIBUTING.md#tests) before you open the pull request.
