# Contributing to Estate

Thanks for helping. Bug reports, ideas, fixes and new screens are all welcome.

For anything bigger than a small fix, please open an issue first so we can agree on the approach
before you spend time on it.

## What you need

- [Go](https://go.dev/dl/) 1.27.3 or newer (see `go.mod`)
- [Node.js](https://nodejs.org/) 18 or newer, for the web checks and the upstream Node server's tests
- Optional: Playwright with Chrome or Edge, for the browser test

## Build and run

```sh
go build ./cmd/estate
./estate serve --no-open --addr 127.0.0.1:3210 --data ../estate-dev-data
```

Open `http://localhost:3210/` for the TV and `http://localhost:3210/play` for a phone. A throwaway
`--data` folder outside the repo keeps test games out of your normal data folder and out of git.

The binary embeds `web/` when it is built, so **rebuild and restart after changing web files**.
For quick work on a screen, the demos need no game at all: `/?demo=<name>` on the TV (names in
`web/fixtures/t-index.json`) and `/play?demo=scene&s=<id>` on the phone (ids in
`web/phone/scenarios.js`).

## Tests

```sh
gofmt -l .        # lists files that need gofmt -w; CI fails if any
go vet ./...
go test ./...
node --test web/tests/*.check.mjs
node --test test/
```

- `go test ./...` is slow, about 10 minutes. `internal/server` and `internal/hardai` take the
  longest. While you work, run just the package you changed, for example
  `go test ./internal/engine`.
- A test that fails only now and then is a bug too: please open an issue with the output.
- `web/tests/*.check.mjs` runs the web checks in Node, with no browser. On Windows, run it from Git
  Bash, or use Node 21 or newer, which expands the pattern itself.
- `test/` holds the tests for the upstream Node server (`server.js`, `src/`).
- The browser test is optional and skips itself if Playwright is missing:

  ```sh
  PLAYWRIGHT=/path/to/node_modules/playwright BROWSER=chrome node --test web/tests/screens.browser.mjs
  ```

CI (`.github/workflows/ci.yml`) runs these checks on every pull request.

## How the repo is laid out

- `cmd/`: the `estate` server, plus tools: bot arenas for each game, game record tools
  (`estate-replay`, `estate-whatif`), AI training (`estate-ml`, `mldash`, `gm-attack`) and
  `poker-fixtures`.
- `internal/`: the Go code: `engine` (Estate rules), `ai`, `hardai`, `valuenet`, `valuenet2`
  (Estate computer players), `poker`, `ludo`, `quiz`, `closest` (the other games), `server` (HTTP,
  WebSockets, rooms), `gamerecord` and `gameanalysis`.
- `web/`: everything the binary serves: `table/` (TV), `phone/`, `shared/`, `fixtures/` (demo
  states) and `tests/` (the web checks).
- `public/`: the upstream browser client, served at `/classic/`.
- `src/`, `server.js`, `test/`: the upstream Node server and JavaScript engine, kept as the
  reference the Go engine is tested against.
- `testdata/`: games and AI decisions recorded from the JavaScript engine, for the Go parity tests.
- `scripts/`: fixture and trace generators, question pack validators and the app icon drawer.
  `scripts/ml/` (with `deploy/install-dashboards.sh`) is the author's AI training set-up; it is not
  needed to play.
- `tools/`: a test-only cross-check of the poker rules against PokerKit.
- `deploy/`: install script, systemd unit, Tailscale Funnel scripts and `deploy/aws`.
- `docs/`: the API and game docs, and the README screenshots in `docs/assets/`.
- `embed.go`: embeds `web/` and `public/` into the binary.

## House rules

- **LF line endings everywhere.** `.gitattributes` enforces it. Make sure your editor doesn't turn
  files into CRLF.
- **The web has no build step.** Pages are plain ES modules and CSS, served as they are. Please
  don't add a bundler, a framework or npm dependencies.
- **Rules live on the server.** Clients only send what the player wants to do and draw the state
  they get back. Never trust a client with money, cards or dice.
- **Fixtures come from the real engines.** Regenerate them rather than editing the JSON by hand:
  - Estate base fixtures: `node scripts/fixtures.js`
  - Poker: `go run ./cmd/poker-fixtures`
  - Ludo: `ESTATE_LUDO_FIXTURES=web/fixtures/ludo go test ./internal/server -run TestLudoFixtures`
  - Quiz: `ESTATE_QUIZ_FIXTURES=web/fixtures/quiz go test ./internal/server -run TestQuizFixtures`
  - Near Thing: `ESTATE_CLOSEST_FIXTURES=web/fixtures/closest go test ./internal/server -run TestClosestFixtures`
  - Go parity data: `node scripts/trace.js` and `node scripts/ai-fixtures.js`

  The TV demo screens (`web/fixtures/t-*.json`) build on a base fixture with `extends` and a small
  patch. See the comment at the top of `web/table/demo.js`.
- **Every new screen state gets a demo and a check.** Add a TV fixture (`web/fixtures/t-*.json`
  plus a row in `t-index.json`) or a phone scene (`web/phone/scenarios.js`, or the game's
  `web/shared/*-scenes.js`), and a check in `web/tests`.
- **Don't weaken tests.** If a test fails, fix the code. Don't skip, loosen or delete a test to get
  green. If you think a test is wrong, say why in the pull request.
- **Keep the docs in step.** The files in `docs/` are the contract between the engines, the server
  and the screens. If you change what goes over the wire, update them in the same pull request.
- **Keep names original.** Don't use the names, wording or look of commercial board games or TV
  shows.
- **Go code** is formatted with `gofmt` and must pass `go vet`.

## Pull requests

1. Fork the repo and create a branch from `main`.
2. Make your change, with tests, and keep it focused on one thing.
3. Run the tests above.
4. Open a pull request to `main` and fill in the template. Screenshots help for anything visible.
5. CI must pass. A maintainer will review it, and may ask for changes before merging.

By contributing, you agree that your work is released under the [MIT license](LICENSE).
