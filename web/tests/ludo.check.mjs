// Ludo screens: the board geometry for four and six arms (every cell where the server says, no two
// squares on top of each other, neighbours next to each other), every Ludo fixture rendered for the
// phone and the table screen (no undefined/NaN, a piece drawn for every piece, the right ones
// glowing), the house-rule chips, whose-turn wording, equivalent moves counted once, the keyboard
// and controller seats, the ESTATE / POKER / LUDO picker, and the sounds. Then, when Playwright and
// Edge are available, every fixture in a real browser at 390 × 844 and 360 × 740 (phone) and
// 1600 × 900 (table): nothing off screen, ROLL and the move buttons reachable, the board whole.
// Run: node --test web/tests/ludo.check.mjs

import assert from "node:assert/strict";
import { createServer } from "node:http";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { ARM_SQUARES, armOwners, areaColor, boardSvg, cellPoint, geometry, movableSet, piecePoints, startSquare, starSquare } from "../shared/ludo-board.js";
import { distinctMoves, forcedMove, howToPlayHtml, isLudoState, ludoRuleChips, ludoSounds, plateHtml, standingsOf, turnLine } from "../shared/ludo-view.js";
import { gamePickHtml, gameTypeOf, gtypeTag } from "../shared/poker-view.js";
import { helpSheetHtml, ludoRoute, moreSheetHtml, moveLabel, renderLudo } from "../phone/ludo-screens.js";
import { renderPhone } from "../phone/screens.js";
import { PLATE_SIDES, createLudoTable, ludoEndConfirmHtml, ludoGameHtml, ludoHelpOverlayHtml, ludoLobbyHtml, ludoSettingsOverlayHtml, seatHint } from "../table/ludo-table.js";

const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, "..");
const dir = join(webRoot, "fixtures", "ludo");
const load = (name) => JSON.parse(readFileSync(join(dir, `${name}.json`), "utf8"));
const names = readdirSync(dir).filter((file) => file.endsWith(".json") && file !== "index.json").map((file) => file.replace(/\.json$/, ""));
const phoneNames = names.filter((name) => name.endsWith("-phone"));
const tableNames = names.filter((name) => name.endsWith("-table"));
const clone = (value) => JSON.parse(JSON.stringify(value));

const BAD = [/undefined/, /\bNaN\b/, /\[object /, />null</, /Infinity/];
function clean(html, label) {
  for (const pattern of BAD) {
    const match = String(html).match(pattern);
    if (match) {
      const at = html.indexOf(match[0]);
      assert.fail(`${label}: found ${pattern} near: ${html.slice(Math.max(0, at - 80), at + 60)}`);
    }
  }
}
const tableCtx = { server: null, demo: true, status: "live", sound: {}, origin: "http://localhost" };
const view = (state, extra = {}) => ({ session: { code: state.code, role: "player" }, ld: {}, ...extra });

// ---------------------------------------------------------------------------------------------
// Geometry

test("geometry: the cross and the six-arm board have every cell, none on top of another, neighbours touching", () => {
  for (const arms of [4, 6]) {
    const geom = geometry(arms);
    const counts = {};
    for (const cell of geom.cells.values()) counts[cell.kind] = (counts[cell.kind] || 0) + 1;
    assert.deepEqual(counts, { ring: arms * ARM_SQUARES, column: arms * 5, home: arms, yard: arms * 4 }, `${arms} arms`);
    // No two squares (ring or column) closer than a square's width.
    const squares = [...geom.cells.entries()].filter(([, cell]) => cell.kind === "ring" || cell.kind === "column");
    for (let i = 0; i < squares.length; i += 1) {
      for (let j = i + 1; j < squares.length; j += 1) {
        const d = Math.hypot(squares[i][1].x - squares[j][1].x, squares[i][1].y - squares[j][1].y);
        assert.ok(d > 0.99, `${arms} arms: ${squares[i][0]} and ${squares[j][0]} overlap (${d.toFixed(3)})`);
      }
    }
    // Ring neighbours are next to each other (a step, or a diagonal at the inner corners).
    const ring = arms * ARM_SQUARES;
    for (let i = 0; i < ring; i += 1) {
      const a = cellPoint(geom, `r${i}`);
      const b = cellPoint(geom, `r${(i + 1) % ring}`);
      const d = Math.hypot(a.x - b.x, a.y - b.y);
      assert.ok(d > 0.99 && d < 1.5, `${arms} arms: r${i} → r${(i + 1) % ring} is ${d.toFixed(3)} apart`);
    }
    // Each arm: the tip's neighbour in is column square 1; column squares run to the centre.
    for (let arm = 0; arm < arms; arm += 1) {
      const tip = cellPoint(geom, `r${arm * ARM_SQUARES + 6}`);
      const first = cellPoint(geom, `c${arm}.1`);
      assert.ok(Math.abs(Math.hypot(tip.x - first.x, tip.y - first.y) - 1) < 0.01, `arm ${arm}: tip next to column 1`);
      const last = cellPoint(geom, `c${arm}.5`);
      assert.ok(Math.hypot(last.x, last.y) < Math.hypot(first.x, first.y), `arm ${arm}: the column runs inwards`);
      assert.ok(geom.cells.get(`r${startSquare(arm)}`).local === 8 && geom.cells.get(`r${starSquare(arm)}`).local === 3);
    }
    // Everything fits on the board.
    for (const cell of geom.cells.values()) assert.ok(Math.abs(cell.x) < geom.extent && Math.abs(cell.y) < geom.extent, `${arms} arms: a cell outside the board`);
  }
  assert.equal(cellPoint(geometry(4), "r99"), null);
  assert.equal(geometry(5).arms, 4, "only 4 or 6 arms");
});

test("geometry: play runs clockwise, and yards sit clockwise of their arm", () => {
  const geom = geometry(4);
  // Arm 0 points down: its start square is left of centre, its yard bottom left; arm 1's yard top left.
  const start = cellPoint(geom, `r${startSquare(0)}`);
  assert.ok(start.x < 0 && start.y > 0);
  assert.ok(geom.yards[0].x < 0 && geom.yards[0].y > 0, "arm 0's yard is bottom left");
  assert.ok(geom.yards[1].x < 0 && geom.yards[1].y < 0, "arm 1's yard is top left");
  assert.ok(geom.yards[2].x > 0 && geom.yards[2].y < 0 && geom.yards[3].x > 0 && geom.yards[3].y > 0);
  // From the start square a piece goes up (towards the centre) along the left column.
  const next = cellPoint(geom, `r${startSquare(0) + 1}`);
  assert.ok(next.y < start.y && Math.abs(next.x - start.x) < 0.01);
  // The plates follow the yards on the table screen.
  assert.deepEqual(PLATE_SIDES[4], { left: [1, 0], right: [2, 3] });
  assert.deepEqual(PLATE_SIDES[6].left, [2, 1, 0]);
});

test("colours: very light player colours are toned down on the ivory board", () => {
  assert.notEqual(areaColor("#F5F3EC"), "#F5F3EC", "a near-white colour gets a warm grey");
  assert.equal(areaColor("#7A1838"), "#7A1838");
});

// ---------------------------------------------------------------------------------------------
// Every fixture

test("fixtures: real server states for both boards, every phase, both screens", () => {
  assert.ok(phoneNames.length >= 7 && tableNames.length >= 7, `${phoneNames.length} phone, ${tableNames.length} table fixtures`);
  const index = JSON.parse(readFileSync(join(dir, "index.json"), "utf8"));
  assert.equal(index.length, names.length, "index.json lists every fixture");
  const phases = new Set(names.map((name) => load(name).state.phase));
  for (const phase of ["lobby", "roll", "move", "finished"]) assert.ok(phases.has(phase), `a ${phase} fixture`);
  assert.ok(names.some((name) => load(name).state.board.arms === 6), "a six-arm fixture");
  for (const name of names) {
    const state = load(name).state;
    assert.ok(isLudoState(state) && state.gameType === "ludo", name);
    const geom = geometry(state.board.arms);
    for (const player of state.players) for (const piece of player.pieces) assert.ok(geom.cells.has(piece.cell), `${name}: ${piece.cell}`);
    for (const move of state.moves || []) for (const cell of [move.from, move.cell, ...move.path]) assert.ok(geom.cells.has(cell), `${name}: move cell ${cell}`);
    for (const cell of state.lastMove?.path || []) assert.ok(geom.cells.has(cell), `${name}: last move ${cell}`);
    assert.equal(JSON.stringify(state).includes('"rng"'), false, `${name}: the dice stream never reaches a screen`);
  }
});

test("board: a piece for every piece, keyed for morph; the right pieces glow on each screen", () => {
  for (const name of names) {
    const state = load(name).state;
    const svg = boardSvg(state, { showAll: true });
    clean(svg, name);
    const want = state.players.filter((player) => player.status !== "left").reduce((n, player) => n + player.pieces.length, 0);
    const keys = [...svg.matchAll(/data-key="(lp-[^"]+)"/g)].map((match) => match[1]);
    assert.equal(keys.length, want, `${name}: pieces drawn`);
    assert.equal(new Set(keys).size, keys.length, `${name}: unique piece keys`);
    assert.equal(piecePoints(state).length, want);
  }
  const move = load("ludo-move-phone").state;
  assert.equal(movableSet(move, { viewerId: move.viewerId }).size, move.moves.length, "your pieces glow on your phone");
  const other = { ...clone(move), viewerId: move.players.find((player) => player.id !== move.turnId).id };
  assert.equal(movableSet(other, { viewerId: other.viewerId }).size, 0, "nobody else's on a phone");
  assert.equal(movableSet(move, { showAll: true }).size, move.moves.length, "the table shows everybody's");
  const glowing = (boardSvg(move, { viewerId: move.viewerId }).match(/class="lp[^"]*is-movable/g) || []).length;
  assert.equal(glowing, move.moves.length);
  // A focused piece shows where it lands.
  assert.match(boardSvg(move, { showAll: true, local: [move.turnId], focus: move.moves[0].piece }), /lb-target/);
  assert.equal(armOwners(move).filter(Boolean).length, move.players.length);
});

test("phone: every fixture renders (screen, slots, sheets), and as a spectator", () => {
  for (const name of phoneNames) {
    const state = load(name).state;
    const out = renderLudo(state, view(state));
    for (const slot of ["header", "banner", "main"]) clean(out[slot], `${name} ${slot}`);
    const expected = state.status === "lobby" ? "ld-lobby" : state.status === "finished" ? "ld-result" : "ld-table";
    assert.equal(out.screen, expected, name);
    clean(moreSheetHtml(state, view(state)), `${name} more`);
    clean(helpSheetHtml(state), `${name} help`);
    const watching = { ...state, viewerId: null, viewerRole: "spectator", you: null };
    clean(renderLudo(watching, view(state, { session: { code: state.code, role: "spectator" } })).main, `${name} spectator`);
  }
  assert.equal(ludoRoute(load("ludo-finished-phone").state, { ld: { seeBoard: true } }), "ld-table");
});

test("phone: ROLL on your roll; one big button per real choice; nothing to choose says MOVING", () => {
  const roll = load("ludo-roll-phone").state;
  assert.match(renderLudo(roll, view(roll)).main, /data-ld="roll"/);
  const move = load("ludo-move-phone").state;
  const html = renderLudo(move, view(move)).main;
  assert.equal((html.match(/data-ld="move"/g) || []).length, distinctMoves(move.moves).length);
  assert.match(html, /HOME/);
  // Three pieces in the yard on a 6, plus one out: two choices (OUT once, and the piece out).
  const six = clone(move);
  six.dice = 6;
  six.moves = [
    { piece: 0, from: "r22", cell: "r28", path: ["r23", "r24", "r25", "r26", "r27", "r28"], to: { zone: "track", step: 20 } },
    { piece: 1, from: "y0.1", cell: "r8", path: ["r8"], out: true, safe: true, to: { zone: "track", step: 0 } },
    { piece: 2, from: "y0.2", cell: "r8", path: ["r8"], out: true, safe: true, to: { zone: "track", step: 0 } },
  ];
  assert.equal(distinctMoves(six.moves).length, 2);
  assert.equal((renderLudo(six, view(six)).main.match(/data-ld="move"/g) || []).length, 2);
  six.moves = six.moves.slice(1);
  assert.ok(forcedMove(six), "every piece coming out is one move");
  assert.match(renderLudo(six, view(six)).main, /MOVING…/);
  assert.doesNotMatch(renderLudo(six, view(six)).main, /data-ld="move"/);
  assert.deepEqual(moveLabel({ capture: 1 }).label, "CAPTURE");
  assert.deepEqual(moveLabel({ out: true }).label, "OUT");
  assert.deepEqual(moveLabel({ path: [1, 2, 3], to: { zone: "track" } }).label, "+3");
  // Someone else's turn: their name, no buttons.
  const waiting = load("ludo-waiting-phone").state;
  const wait = renderLudo(waiting, view(waiting)).main;
  assert.doesNotMatch(wait, /data-ld=/);
  assert.match(wait, /TURN/);
});

test("phone: the lobby has the picker, seats with × for the host, the board preview and the rule chips", () => {
  const lobby = load("ludo-lobby-phone").state;
  const html = renderLudo(lobby, view(lobby)).main;
  assert.match(html, /data-cmd="ld-switch-game" data-arg="estate"/);
  assert.match(html, /data-arg="ludo" aria-pressed="true"/);
  assert.match(html, /data-cmd="ld-rule"/);
  assert.match(html, /ldp-preview/);
  assert.match(html, /\+ COMPUTER/);
  assert.equal((html.match(/data-cmd="remove"/g) || []).length, lobby.players.length - 1, "× on everyone but you");
  const guest = { ...lobby, viewerId: lobby.players.find((player) => player.id !== lobby.hostId && player.kind === "human")?.id || "nobody", viewerIsHost: false };
  const guestHtml = renderLudo(guest, view(guest)).main;
  assert.doesNotMatch(guestHtml, /data-cmd="ld-rule"|ld-switch-game|data-cmd="remove"/);
  const six = load("ludo-lobby6-phone").state;
  assert.equal(six.board.arms, 6);
  assert.match(renderLudo(six, view(six)).main, /data-arms="6"/);
});

test("table screen: every fixture renders; lobby seats, ×, computers, keyboard seat, rules and START", () => {
  for (const name of tableNames) {
    const state = load(name).state;
    const html = state.status === "lobby" ? ludoLobbyHtml(state, tableCtx) : ludoGameHtml(state, tableCtx);
    clean(html, name);
    if (state.status !== "lobby") {
      const arms = state.board.arms;
      assert.equal((html.match(/class="lplate[ "]/g) || []).length, PLATE_SIDES[arms].left.length + PLATE_SIDES[arms].right.length, `${name}: a plate slot per arm`);
    }
  }
  const lobby = load("ludo-lobby-table").state;
  const html = ludoLobbyHtml(lobby, tableCtx);
  assert.match(html, /data-cmd="pick-game" data-arg="poker"/);
  assert.match(html, /data-cmd="ld-add-ai"/);
  assert.match(html, /data-cmd="ld-ai-level"/);
  assert.match(html, /data-cmd="seat-keyboard"/, "keyboard seats: nothing is secret in Ludo");
  assert.match(html, /SCAN · A · ENTER/);
  assert.equal((html.match(/data-cmd="remove-player"/g) || []).length, lobby.players.length);
  assert.match(html, /data-cmd="start"/);
  assert.match(html, /data-cmd="new-table"/);
  assert.match(html, /KEYBOARD/);
  assert.doesNotMatch(ludoLobbyHtml(lobby, { ...tableCtx, watching: true }), /remove-player|data-cmd="new-table"|data-cmd="start"/);
  const finished = load("ludo-finished-table").state;
  const fin = ludoGameHtml(finished, tableCtx);
  assert.match(fin, /PLAY AGAIN/);
  assert.match(fin, /NEW TABLE/);
  assert.match(fin, /FINAL PLACES/);
  clean(ludoHelpOverlayHtml(finished), "help");
  clean(ludoEndConfirmHtml(load("ludo-roll-table").state), "end confirm");
  const settings = ludoSettingsOverlayHtml({}, {}, { computerSpeed: "normal", canComputerSpeed: true });
  assert.doesNotMatch(settings, /CAMERA|COMPUTER OFFERS/);
  assert.match(settings, /COMPUTER SPEED/);
});

test("table screen: the keyboard seat to act sees what to press", () => {
  const move = clone(load("ludo-move-table").state);
  assert.equal(seatHint(move), "", "Maya plays on her phone");
  move.localSeats = [{ seatId: "seat_1", playerId: move.turnId, input: "keyboard", pad: null }];
  assert.match(seatHint(move, move.moves[0].piece), /PICK A PIECE.*MOVE PIECE/);
  move.phase = "roll";
  assert.match(seatHint(move), /ENTER.*ROLL/);
  move.localSeats[0] = { ...move.localSeats[0], input: "controller", pad: 0 };
  assert.match(seatHint(move), />A<.*ROLL/);
});

// A fake page for createLudoTable: no DOM is touched by key/pad/command.
function fakeTable(state) {
  const sent = [];
  const renders = { n: 0 };
  const store = new Map();
  const deps = {
    el: { ludo: null }, ui: { state }, storage: { get: (k) => store.get(k) ?? null, set: (k, v) => store.set(k, v) },
    ctx: () => ({}), render: () => { renders.n += 1; }, perform: async (action, seatId) => { sent.push({ action, seatId }); return true; },
    addAi: async (level) => { sent.push({ addAi: level }); }, sound: () => {}, reduced: () => true,
  };
  return { table: createLudoTable(deps), sent, renders, deps };
}

test("keyboard and controller seats: roll, pick a piece, move it", async () => {
  const state = clone(load("ludo-move-table").state);
  state.localSeats = [{ seatId: "seat_k", playerId: state.turnId, input: "keyboard", pad: null }];
  state.phase = "roll";
  const { table, sent } = fakeTable(state);
  const key = (k) => table.key({ key: k });
  assert.equal(key("Enter"), true);
  assert.deepEqual(sent.pop(), { action: { type: "roll" }, seatId: "seat_k" });
  state.phase = "move";
  table.render = table.render; // (focus follows the moves on render; key() keeps its own)
  assert.equal(key("ArrowRight"), true);
  const picked = table.focus();
  assert.ok(state.moves.some((move) => move.piece === picked));
  assert.equal(key("Enter"), true);
  assert.deepEqual(sent.pop(), { action: { type: "move", piece: picked }, seatId: "seat_k" });
  assert.equal(key(String(state.moves[2].piece + 1)), true);
  assert.deepEqual(sent.pop().action, { type: "move", piece: state.moves[2].piece });
  assert.equal(table.pad(0, "A"), false, "no controller seat here");
  // A controller seat: its own pad only.
  state.localSeats = [{ seatId: "seat_p", playerId: state.turnId, input: "controller", pad: 1 }];
  state.phase = "roll";
  assert.equal(table.pad(0, "A"), false, "another pad");
  assert.equal(table.pad(1, "A"), true);
  assert.deepEqual(sent.pop(), { action: { type: "roll" }, seatId: "seat_p" });
  assert.equal(key("Enter"), false, "the keyboard doesn't act for a controller seat");
  // Nobody local: nothing happens.
  state.localSeats = [];
  assert.equal(key("Enter"), false);
});

test("lobby commands: rule chips cycle, the computer level steps from NORMAL, ADD COMPUTER", async () => {
  const lobby = clone(load("ludo-lobby-table").state);
  const chips = ludoRuleChips(lobby);
  assert.equal(chips.length, 9);
  const byKey = Object.fromEntries(chips.map((chip) => [chip.key, chip]));
  assert.deepEqual(byKey.pieces.patch, { pieces: 3 });
  assert.deepEqual(byKey.board.patch, { board: "4" });
  assert.deepEqual(byKey.anyRollOut.patch, { anyRollOut: true });
  assert.deepEqual(byKey.safeSquares.patch, { safeSquares: false });
  assert.deepEqual(byKey.threeSixes.patch, { threeSixes: false });
  assert.deepEqual(byKey.homeBonus.patch, { homeBonus: false });
  assert.deepEqual(byKey.captureFirst.patch, { captureFirst: true });
  assert.deepEqual(byKey.blocks.patch, { blocks: true });
  assert.deepEqual(byKey.playOn.patch, { playOn: false });
  assert.equal(chips.filter((chip) => chip.custom).length, 0, "defaults are not marked");
  assert.deepEqual(ludoRuleChips({ ...lobby, settings: { ...lobby.settings, pieces: 2 } })[0].patch, { pieces: 4 });
  // Five seated: the board chip skips the cross.
  const five = { ...lobby, players: [...lobby.players, { ...lobby.players[0], id: "p9" }], settings: { ...lobby.settings, board: "auto" } };
  assert.deepEqual(ludoRuleChips(five).find((chip) => chip.key === "board").patch, { board: "6" });
  const { table, sent } = fakeTable(lobby);
  await table.command("ld-rule", "blocks");
  assert.deepEqual(sent.pop().action, { type: "settings", settings: { blocks: true } });
  await table.command("ld-ai-level");
  await table.command("ld-add-ai");
  assert.deepEqual(sent.pop(), { addAi: "hard" }, "NORMAL → HARD");
});

test("whose turn: plain words for the table, your phone and a keyboard seat", () => {
  const roll = load("ludo-roll-table").state;
  const maya = roll.players.find((player) => player.id === roll.turnId);
  assert.match(turnLine(roll), new RegExp(`${maya.name.toUpperCase()}'S TURN`));
  assert.match(turnLine(roll, { viewerId: maya.id }), /YOUR TURN · ROLL/);
  const move = load("ludo-move-table").state;
  assert.match(turnLine(move), /ROLLED A 5/);
  assert.match(turnLine(move, { local: [move.turnId] }), /PICK A PIECE/);
  assert.match(turnLine(load("ludo-finished-table").state), /WINS/);
  assert.equal(turnLine({ ...roll, status: "lobby" }), "WAITING TO START");
  const capture = load("ludo-capture-table").state;
  assert.ok(capture.lastMove.captured.length > 0 && capture.lastMove.again);
  assert.match(turnLine(capture, { viewerId: capture.turnId }), /ROLL AGAIN/);
  const fin = load("ludo-finished-table").state;
  const standings = standingsOf(fin);
  assert.deepEqual(standings.map((row) => row.place), standings.map((_, i) => i + 1));
  clean(plateHtml(roll, roll.players[0]), "plate");
  assert.match(howToPlayHtml(roll), /exact number/);
  assert.match(howToPlayHtml({ settings: { blocks: true, captureFirst: true, anyRollOut: true } }), /block.*/s);
});

test("picker: ESTATE / POKER / LUDO everywhere a game is chosen; lists tag Ludo games", () => {
  const pick = gamePickHtml("ludo");
  assert.match(pick, /data-arg="ludo" aria-pressed="true"/);
  assert.match(pick, /data-arg="estate" aria-pressed="false"/);
  assert.match(pick, /data-arg="poker" aria-pressed="false"/);
  assert.match(pick, /LUDO/);
  assert.equal(gameTypeOf({ gameType: "ludo" }), "ludo");
  assert.equal(gameTypeOf({ game: "ludo" }), "ludo");
  assert.match(gtypeTag("ludo"), /gtype--ludo/);
  // The phone's NEW GAME.
  const join = { mode: "create", name: "Ana", tokenKind: "rocket", gameType: "ludo", code: "", summary: null };
  const create = renderPhone(null, { session: null, join, palette: {} });
  assert.match(create.main, /data-cmd="pick-game" data-arg="ludo" aria-pressed="true"/);
  // The phone's home list.
  const games = [{ code: "LLLLL", status: "playing", gameType: "ludo", turn: 12, host: { name: "Sam", tokenKind: "teapot" }, players: [{ name: "Sam", tokenKind: "teapot" }] }];
  const home = renderPhone(null, { session: null, join: { code: "" }, games: { list: games }, palette: {} }).main;
  assert.match(home, /gtype--ludo/);
  assert.match(home, /TURN 12/);
  // Estate's lobby on the phone and the table screen offer LUDO too.
  const estateLobby = JSON.parse(readFileSync(join_(webRoot, "fixtures", "lobby.json"), "utf8")).state;
  const lobbyOut = renderPhone(estateLobby, { session: { code: estateLobby.code }, tab: "turn", trade: {}, friends: { list: [], sent: {} } });
  if (estateLobby.hostId === estateLobby.viewerId) assert.match(lobbyOut.main, /data-cmd="switch-game" data-arg="ludo"/);
});
const join_ = (...parts) => join(...parts);

test("sounds: the die, a finish, the win, your turn; the animation plays hops, captures and home", () => {
  const before = load("ludo-roll-phone").state;
  const rolled = clone(before);
  rolled.rollSeq += 1;
  assert.deepEqual(ludoSounds(before, rolled, { viewerId: before.viewerId }).map((hit) => hit.name), ["dice"]);
  const captured = load("ludo-capture-phone").state;
  const prev = clone(captured);
  prev.lastMove = { ...prev.lastMove, seq: prev.lastMove.seq - 1 };
  const names = ludoSounds(prev, captured, { viewerId: "someone" }).map((hit) => hit.name);
  assert.ok(names.includes("gavel"), names.join());
  const done = load("ludo-finished-phone").state;
  const almost = { ...clone(done), status: "playing" };
  assert.deepEqual(ludoSounds(almost, done).map((hit) => hit.name), ["win"]);
  const wait = load("ludo-waiting-phone").state;
  const mine = { ...clone(wait), turnId: wait.viewerId, phase: "roll" };
  assert.ok(ludoSounds(wait, mine, { viewerId: wait.viewerId }).some((hit) => hit.name === "turn"));
  assert.deepEqual(ludoSounds(null, wait), []);
});

// ---------------------------------------------------------------------------------------------
// In a real browser

const PW = process.env.PLAYWRIGHT || ""; // node_modules/playwright; these browser checks skip without it (CONTRIBUTING.md)
const TYPES = { ".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".json": "application/json; charset=utf-8", ".svg": "image/svg+xml" };

function serveWeb() {
  const server = createServer((req, res) => {
    const url = new URL(req.url, "http://localhost");
    let file = decodeURIComponent(url.pathname);
    if (file.startsWith("/api/") || file.startsWith("/ws/")) {
      res.writeHead(404, { "Content-Type": "application/json" });
      res.end('{"error":"not here"}');
      return;
    }
    if (file === "/") file = "/table/index.html";
    else if (file === "/play") file = "/phone/index.html";
    const full = resolve(webRoot, `.${file}`);
    if (!full.startsWith(webRoot) || !existsSync(full)) {
      res.writeHead(404);
      res.end();
      return;
    }
    res.writeHead(200, { "Content-Type": TYPES[extname(full)] || "application/octet-stream" });
    res.end(readFileSync(full));
  });
  return new Promise((done) => server.listen(0, "127.0.0.1", () => done(server)));
}

async function launch() {
  if (!PW || !existsSync(PW)) return null;
  let chromium;
  try {
    ({ chromium } = createRequire(import.meta.url)(PW));
  } catch {
    return null;
  }
  for (const channel of ["msedge"]) {
    try { return await chromium.launch({ channel, headless: true }); } catch { /* not installed */ }
  }
  return null;
}

// The board whole and on screen; nothing wider than the screen; ROLL / the move buttons inside the
// screen and big enough to tap; the plates (table) clear of the board.
const PROBE = `(() => {
  const r = (n) => { const b = n.getBoundingClientRect(); return { x: b.left, y: b.top, w: b.width, h: b.height, r: b.right, b: b.bottom }; };
  const out = [];
  const board = document.querySelector(".lboard");
  if (board) { const b = r(board); if (b.x < -1 || b.y < -1 || b.r > innerWidth + 1 || b.b > innerHeight + 1) out.push("board off screen " + JSON.stringify(b)); if (b.w < 200) out.push("board too small " + b.w); }
  if (document.documentElement.scrollWidth > innerWidth + 1) out.push("page wider than the screen");
  for (const n of document.querySelectorAll(".ldp-roll, .ldp-move")) { const b = r(n); if (b.b > innerHeight + 1 || b.r > innerWidth + 1) out.push("button off screen: " + n.className); if (b.h < 44) out.push("button under 44px: " + n.className); }
  const plates = [...document.querySelectorAll(".ld-tv .lplate:not(.lplate--empty)")];
  if (board) for (const p of plates) { const a = r(p), b = r(board); if (a.x + 2 < b.r && b.x + 2 < a.r && a.y + 2 < b.b && b.y + 2 < a.b) out.push("plate over the board: " + p.dataset.player); }
  return { problems: out, screen: document.querySelector("#app")?.dataset.screen || document.querySelector("#ludo-screen")?.dataset.mode };
})()`;

test("browser: every Ludo fixture fits the phone (390 × 844 and 360 × 740) and the table screen (1600 × 900)", { timeout: 240000 }, async (t) => {
  const browser = await launch();
  if (!browser) {
    t.skip("Playwright or Edge is not available");
    return;
  }
  const server = await serveWeb();
  const base = `http://127.0.0.1:${server.address().port}`;
  const failures = [];
  try {
    for (const viewport of [{ width: 390, height: 844 }, { width: 360, height: 740 }]) {
      const context = await browser.newContext({ viewport, deviceScaleFactor: 1, isMobile: true, hasTouch: true });
      for (const name of phoneNames) {
        const page = await context.newPage();
        const errors = [];
        page.on("pageerror", (error) => errors.push(error.message));
        await page.goto(`${base}/play?demo=${name}`, { waitUntil: "load" });
        await page.waitForFunction(() => document.querySelector("#app")?.dataset.screen?.startsWith("ld-"), null, { timeout: 15000 });
        await page.waitForTimeout(300);
        const report = await page.evaluate(PROBE);
        if (errors.length) failures.push(`${name} ${viewport.width}: ${errors.join("; ")}`);
        for (const problem of report.problems) {
          // The lobby is a page that scrolls; only the game screen must fit without scrolling.
          if (report.screen === "ld-lobby" && /off screen/.test(problem)) continue;
          failures.push(`${name} ${viewport.width}: ${problem}`);
        }
        await page.close();
      }
      await context.close();
    }
    const tv = await browser.newContext({ viewport: { width: 1600, height: 900 } });
    for (const name of tableNames) {
      const page = await tv.newPage();
      const errors = [];
      page.on("pageerror", (error) => errors.push(error.message));
      await page.goto(`${base}/?demo=${name}`, { waitUntil: "load" });
      await page.waitForFunction(() => globalThis.estateDemo?.ready?.(), null, { timeout: 15000 });
      await page.waitForTimeout(300);
      const report = await page.evaluate(PROBE);
      if (errors.length) failures.push(`table ${name}: ${errors.join("; ")}`);
      for (const problem of report.problems) failures.push(`table ${name}: ${problem}`);
      await page.close();
    }
  } finally {
    await browser.close();
    server.close();
  }
  assert.deepEqual(failures, []);
});

// The owner, 22 Nov: "the pieces don't stack if they are on top of each other, cannot tell what's
// under". The hops ended held (fill: forwards) in the middle of the square, which outranked the
// board's spread, so two pieces that had moved sat exactly on top of each other. Play real moves
// with the real animations and check every piece is drawn where the board puts it.
test("browser: after a move onto a taken square, every piece is drawn at its own spot (none hidden under another)", { timeout: 120000 }, async (t) => {
  const browser = await launch();
  if (!browser) {
    t.skip("Playwright or Edge is not available");
    return;
  }
  const start = load("ludo-move-table").state;
  const step = (state, playerId, index, path, extra = {}) => {
    const next = clone(state);
    const piece = next.players.find((p) => p.id === playerId).pieces[index];
    const from = piece.cell;
    const cell = path[path.length - 1];
    Object.assign(piece, { zone: "track", cell, square: Number(cell.slice(1)) });
    next.lastMove = { seq: (state.lastMove?.seq || 0) + 1, playerId, piece: index, roll: path.length, from, cell, path, captured: [], out: false, home: false, again: false, ...extra };
    return next;
  };
  // p4's piece walks onto p3's piece on r8 (a safe square: they share it); then p1 moves elsewhere;
  // then p3's piece walks on and p4's is left alone again.
  const one = step(start, "p4", 1, ["r6", "r7", "r8"]);
  const two = step(one, "p1", 0, ["r23", "r24", "r25"]);
  const three = step(two, "p3", 1, ["r9", "r10", "r11"]);
  const four = step(three, "p4", 1, ["r9", "r10", "r11"]);
  const server = await serveWeb();
  const base = `http://127.0.0.1:${server.address().port}`;
  try {
    const page = await browser.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(`${base}/fixtures/ludo/index.json`); // any page from this server, for the imports
    const results = await page.evaluate(async (states) => {
      const { boardSvg } = await import("/shared/ludo-board.js");
      const { createLudoFx } = await import("/shared/ludo-fx.js");
      const { morph } = await import("/shared/morph.js");
      const root = document.body.appendChild(document.createElement("div"));
      const draw = (state) => morph(root, boardSvg(state, { showAll: true }));
      const fx = createLudoFx({ speed: () => 0.35 });
      const check = () => {
        const out = { off: [], together: [], held: 0 };
        const seen = new Map();
        for (const el of root.querySelectorAll(".lboard .lp")) {
          const m = el.transform.baseVal.consolidate()?.matrix;
          const marked = { x: m ? m.e : 0, y: m ? m.f : 0 };
          const css = getComputedStyle(el).transform;
          const drawn = css && css !== "none" ? (({ e, f }) => ({ x: e, y: f }))(new DOMMatrixReadOnly(css)) : marked;
          if (Math.hypot(drawn.x - marked.x, drawn.y - marked.y) > 0.02) out.off.push(`${el.dataset.key}@${el.dataset.cell}`);
          const at = `${drawn.x.toFixed(2)},${drawn.y.toFixed(2)}`;
          seen.set(at, [...(seen.get(at) || []), el.dataset.key]);
          out.held += el.getAnimations().filter((a) => !(a instanceof CSSAnimation) && !(a instanceof CSSTransition)).length;
        }
        for (const keys of seen.values()) if (keys.length > 1) out.together.push(keys.join(" + "));
        return out;
      };
      let shown = states[0];
      draw(shown);
      const results = [];
      for (const next of states.slice(1)) {
        await fx.play(root, shown, next, () => draw(next));
        shown = next;
        await new Promise((done) => setTimeout(done, 400));
        results.push({ move: `${next.lastMove.playerId}:${next.lastMove.piece}->${next.lastMove.cell}`, ...check() });
      }
      return results;
    }, [start, one, two, three, four]);
    assert.deepEqual(errors, []);
    for (const result of results) {
      assert.deepEqual(result.off, [], `${result.move}: drawn away from the board's spot`);
      assert.deepEqual(result.together, [], `${result.move}: drawn exactly on top of each other`);
      assert.equal(result.held, 0, `${result.move}: animations left holding pieces`);
    }
  } finally {
    await browser.close();
    server.close();
  }
});
