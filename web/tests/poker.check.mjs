// Poker screens: every poker fixture rendered for the phone and the table screen (no leaks of
// undefined/NaN, no hidden card on the table screen), the action bar, raise presets and slider
// clamping, the pre-action rules, the hand strength meter, the rule chips, the ESTATE / POKER
// picker and game tags, and the sounds. Then, when Playwright and a browser are available, every
// poker scene at 390 × 844 in a real browser: eight seats fit with no overlaps and nothing spills
// off the screen. Run: node web/tests/poker.check.mjs

import assert from "node:assert/strict";
import { createServer } from "node:http";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { clampRaise } from "../shared/poker.js";
import { POKER_SCENES, buildPokerScene, eightSeats } from "../shared/poker-scenes.js";
import { actTimer, gamePickHtml, gameTypeOf, gtypeTag, handRanksHtml, rankOf, seatOrder, shortHandName, tableModel, winningCards } from "../shared/poker-view.js";
import {
  DEFAULT_POKER_PREFS, aiLevelsOf, canPreAct, fourColour, makePreAction, nextLevel, normalisePokerPrefs, pokerSounds, preActionOptions, preActionToSend,
  raiseModel, reconcilePreAction, ruleChips, sliderAmount, stepAmount, strengthSteps, toCallOf, turnButtons,
} from "../phone/poker-play.js";
import { allInSheetHtml, helpSheetHtml, moreSheetHtml, pokerRoute, pokerSettingsRows, renderPoker } from "../phone/poker-screens.js";
import { renderPhone, settingsHtml } from "../phone/screens.js";
import { pokerGameHtml, pokerLobbyHtml, pokerSettingsOverlayHtml, tvLine } from "../table/poker-table.js";
import * as views from "../table/views.js";

const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, "..");
const dir = join(webRoot, "fixtures", "poker");
const load = (name) => JSON.parse(readFileSync(join(dir, `${name}.json`), "utf8"));
const names = readdirSync(dir).filter((file) => file.endsWith(".json") && file !== "index.json").map((file) => file.replace(/\.json$/, ""));
const phoneNames = names.filter((name) => name.endsWith("-phone"));
const tableNames = names.filter((name) => name.endsWith("-table"));
const clone = (value) => JSON.parse(JSON.stringify(value));

const BAD = [/undefined/, /\bNaN\b/, /\[object /, />null</, /\$-/, /Infinity/];
function clean(html, label) {
  for (const pattern of BAD) {
    const match = String(html).match(pattern);
    if (match) {
      const at = html.indexOf(match[0]);
      assert.fail(`${label}: found ${pattern} near: ${html.slice(Math.max(0, at - 80), at + 60)}`);
    }
  }
  for (const attr of String(html).matchAll(/data-act='([^']*)'/g)) assert.equal(typeof JSON.parse(attr[1]).type, "string", `${label}: action without type`);
}

const view = (extra = {}) => ({ session: { code: "KQ7TD", role: "player" }, pk: {}, pkPrefs: { ...DEFAULT_POKER_PREFS }, now: 0, ...extra });
const at = (state) => view({ now: state.now });
const SUIT = { clubs: "c", diamonds: "d", hearts: "h", spades: "s" };
// Face-up cards drawn in some HTML, as codes ("Ah", "Td").
function faceUps(html) {
  const out = [];
  for (const match of String(html).matchAll(/aria-label="(10|[2-9AJQK]) of (clubs|diamonds|hearts|spades)"/g)) out.push(`${match[1] === "10" ? "T" : match[1]}${SUIT[match[2]]}`);
  return out;
}
const tableCtx = { server: null, demo: true, status: "live", sound: {}, origin: "http://localhost" };

// ---------------------------------------------------------------------------------------------
// Rendering every fixture

test("phone: every poker fixture renders (screen, slots, no undefined/NaN), and the sheets too", () => {
  assert.ok(phoneNames.length >= 15, `phone fixtures: ${phoneNames.length}`);
  for (const name of phoneNames) {
    const state = load(name).state;
    assert.equal(state.game, "poker", name);
    const out = renderPoker(state, at(state));
    for (const slot of ["header", "banner", "main"]) clean(out[slot], `${name} ${slot}`);
    const expected = state.status === "lobby" ? "pk-lobby" : state.status === "finished" ? "pk-result" : "pk-table";
    assert.equal(out.screen, expected, name);
    clean(moreSheetHtml(state, at(state)), `${name} more`);
    clean(helpSheetHtml(state, at(state)), `${name} help`);
    if (state.you?.legal?.raise) clean(allInSheetHtml(state, state.you.legal.raise.max), `${name} all-in`);
    // As a spectator watching on a phone.
    const watching = { ...state, viewerId: null, viewerRole: "spectator", you: null };
    clean(renderPoker(watching, view({ now: state.now, session: { code: state.code, role: "spectator" } })).main, `${name} spectator`);
  }
});

test("phone: the viewer sits at the bottom with their own cards face up and big", () => {
  for (const name of phoneNames) {
    const state = load(name).state;
    if (state.status !== "playing" || !state.you?.cards?.length) continue;
    const order = seatOrder(state);
    assert.equal(order[0].player.id, state.viewerId, `${name}: viewer first`);
    assert.equal(order[0].pos, "b", `${name}: viewer bottom-centre`);
    const html = renderPoker(state, at(state)).main;
    assert.match(html, /pk-hole--mine/, `${name}: own cards drawn big`);
    for (const code of state.you.cards) assert.ok(faceUps(html).includes(code), `${name}: own card ${code} face up`);
  }
});

test("table screen: every fixture renders, and never shows a card that isn't face up", () => {
  assert.ok(tableNames.length >= 15, `table fixtures: ${tableNames.length}`);
  for (const name of tableNames) {
    const state = load(name).state;
    assert.equal(state.viewerRole, "table", name);
    assert.equal(state.you, null, `${name}: no private panel`);
    const html = state.status === "lobby" ? pokerLobbyHtml(state, tableCtx) : pokerGameHtml(state, { ...tableCtx, now: state.now });
    clean(html, name);
    const allowed = new Set([...(state.board || []), ...(state.result?.board || []), ...(state.result?.hands || []).flatMap((hand) => hand.cards || []), ...state.players.flatMap((player) => player.cards || [])]);
    // How to play shows example cards; only the table itself counts here.
    for (const code of faceUps(html)) assert.ok(allowed.has(code), `${name}: shows ${code}, which isn't face up`);
    // The phone's private cards from the same scene never reach the table screen.
    const phone = names.includes(name.replace(/-table$/, "-phone")) ? load(name.replace(/-table$/, "-phone")).state : null;
    for (const code of phone?.you?.cards || []) {
      if (!allowed.has(code)) assert.ok(!faceUps(html).includes(code), `${name}: leaks ${code}`);
    }
  }
});

test("table screen: hidden hands are card backs; the 8-seat table has all eight seats", () => {
  const state = load("poker-flop8-table").state;
  const html = pokerGameHtml(state, { ...tableCtx, now: state.now });
  const holding = state.players.filter((player) => player.hasCards && !player.cards).length;
  assert.equal((html.match(/pc pc--back/g) || []).length, holding * 2, "two backs per hidden hand");
  assert.equal((html.match(/class="pk-seat[ "]/g) || []).length, 8);
  assert.match(html, /data-shape="landscape"/);
  assert.match(tvLine(state, state.now), /'S TURN/);
});

test("scenes: every derived demo scene builds and renders", async () => {
  for (const scene of POKER_SCENES) {
    const { state, pk } = await buildPokerScene(scene.name, async (name) => load(name));
    const html = state.viewerRole === "table" ? pokerGameHtml(state, { ...tableCtx, now: state.now }) : renderPoker(state, view({ now: state.now, pk })).main;
    clean(html, scene.name);
  }
  const eight = eightSeats(load("poker-flop-phone").state);
  assert.equal(eight.players.length, 8);
  assert.deepEqual(seatOrder(eight).map((entry) => entry.pos), ["b", "bl", "l", "tl", "t", "tr", "r", "br"]);
});

// ---------------------------------------------------------------------------------------------
// Your turn: buttons, raise panel, slider

test("action bar: FOLD · CALL $x · RAISE when facing a bet; CHECK · BET when it's free", () => {
  const flop = load("poker-flop-phone").state;
  assert.deepEqual(turnButtons(flop).map((button) => button.kind), ["fold", "call", "raise"]);
  assert.equal(turnButtons(flop)[1].amount, 67);
  const html = renderPoker(flop, at(flop)).main;
  assert.match(html, /data-pk="fold"/);
  assert.match(html, /CALL<\/span><span class="pkbtn__sub">\$67/);
  assert.match(html, /data-pk="raise-open"/);

  const free = clone(flop);
  free.you.legal = { fold: false, check: true, call: 0, raise: { isBet: true, min: 20, max: 1455, presets: [{ label: "Min", to: 20 }, { label: "½ pot", to: 135 }, { label: "Pot", to: 269 }, { label: "All-in", to: 1455 }] }, allIn: 1455 };
  const buttons = turnButtons(free);
  assert.deepEqual(buttons.map((button) => button.kind), ["fold", "check", "raise"]);
  assert.equal(buttons[0].disabled, true, "no folding when checking is free");
  assert.equal(buttons[2].label, "BET");

  // Calling would put you all in: the call reads ALL IN and asks first.
  const short = clone(flop);
  short.players.find((player) => player.id === short.viewerId).stack = 50;
  short.you.legal = { fold: true, check: false, call: 50, raise: null, allIn: 50 };
  const call = turnButtons(short).find((button) => button.kind === "call");
  assert.equal(call.allIn, true);
  assert.match(renderPoker(short, at(short)).main, /data-pk="callallin"/);
  // Not your turn: no buttons.
  assert.deepEqual(turnButtons(load("poker-flop8-phone").state), []);
});

test("raise panel: MIN · ½ POT · POT · ALL IN presets, amounts kept legal, ALL IN at the top", () => {
  const flop = load("poker-flop-phone").state;
  const model = raiseModel(flop, null);
  assert.equal(model.to, 134);
  assert.equal(model.verb, "RAISE TO");
  assert.deepEqual(model.presets.map((preset) => [preset.label, preset.to]), [["MIN", 134], ["½ POT", 235], ["POT", 403], ["ALL IN", 1455]]);
  assert.equal(model.presets[0].on, true);
  assert.deepEqual(model.action, { type: "raise", to: 134 });
  const top = raiseModel(flop, 99999);
  assert.equal(top.to, 1455);
  assert.equal(top.allIn, true);
  assert.deepEqual(top.action, { type: "allin" });
  assert.equal(top.confirmLabel, "ALL IN $1,455");
  assert.equal(raiseModel(flop, 5).to, 134, "never below the minimum");
  // Slider: whole small blinds between the ends, the ends exact.
  assert.equal(sliderAmount(flop, 0), 134);
  assert.equal(sliderAmount(flop, 134), 134);
  assert.equal(sliderAmount(flop, 403.4), 400);
  assert.equal(sliderAmount(flop, 1454), 1450);
  assert.equal(sliderAmount(flop, 1455), 1455);
  assert.equal(sliderAmount(flop, 2000), 1455);
  assert.equal(stepAmount(flop, 134, 1), 154, "+ one big blind");
  assert.equal(stepAmount(flop, 134, -1), 134, "never below the minimum");
  assert.equal(clampRaise(flop, 1e9), 1455);
  // A river raise where only MIN and ALL IN exist: the pot presets are off.
  const river = load("poker-river-phone").state;
  const presets = raiseModel(river, null).presets;
  assert.equal(presets.find((preset) => preset.label === "POT").to, null);
  // The panel draws the slider with the legal range and the chosen amount.
  const html = renderPoker(flop, view({ now: flop.now, pk: { raiseOpen: true, raiseTo: 235 } })).main;
  assert.match(html, /data-pk-slider min="134" max="1455" step="1" value="235"/);
  assert.match(html, /RAISE TO<\/span><span class="pkbtn__sub" data-raise-sub>\$235/);
  assert.match(html, /class="pkp[^"]*is-raising/);
});

test("turn timer and time bank: share of time left; the bank runs in blue", () => {
  const flop = load("poker-preflop-phone").state;
  const run = actTimer(flop, flop.now);
  assert.equal(run.id, flop.actorId);
  assert.equal(run.bank, false);
  assert.equal(run.total, flop.settings.actionSeconds);
  assert.ok(run.left > 0 && run.left <= run.total);
  const bank = load("poker-timebank-phone").state;
  const onBank = actTimer(bank, bank.now);
  assert.equal(onBank.bank, true);
  assert.equal(onBank.total, bank.players.find((player) => player.id === bank.actorId).timeBank);
  const html = renderPoker(bank, at(bank)).main;
  assert.match(html, /pk-ring is-bank/);
  assert.match(html, /pk-secs is-bank/);
  assert.equal(actTimer({ ...flop, actionDeadline: 0 }, flop.now), null, "no timer when it's off");
});

// ---------------------------------------------------------------------------------------------
// Pre-actions

test("pre-actions: CHECK/FOLD · CALL $x · CALL ANY while someone else acts", () => {
  const state = load("poker-flop8-phone").state;
  assert.equal(canPreAct(state), true);
  assert.equal(toCallOf(state), 50);
  assert.deepEqual(preActionOptions(state).map((option) => option.label), ["CHECK/FOLD", "CALL $50", "CALL ANY"]);
  const html = renderPoker(state, view({ now: state.now, pk: { pre: { kind: "call", amount: 50, hand: state.handNumber } } })).main;
  assert.match(html, /class="pkpre is-on" data-pre="call" aria-pressed="true"/, "the ticked one shows");
  assert.equal((html.match(/aria-pressed="true"/g) || []).length, 1);
  // Nothing to call: the middle one is CHECK.
  const free = clone(state);
  free.currentBet = 0;
  for (const player of free.players) player.bet = 0;
  assert.deepEqual(preActionOptions(free).map((option) => option.label), ["CHECK/FOLD", "CHECK", "CALL ANY"]);
  // Folded, your turn, or not betting: none.
  assert.deepEqual(preActionOptions(load("poker-flop-phone").state), []);
  assert.deepEqual(preActionOptions(load("poker-showdown-phone").state), []);
});

test("pre-actions: CALL $x clears when the price changes, CHECK when a bet comes in, all on a new hand", () => {
  const base = load("poker-flop8-phone").state;
  const call = makePreAction(base, "call");
  assert.deepEqual(call, { kind: "call", amount: 50, hand: base.handNumber });
  assert.deepEqual(reconcilePreAction(call, base), call, "same price: kept");
  const raised = clone(base);
  raised.currentBet = 150;
  assert.equal(reconcilePreAction(call, raised), null, "price changed: cleared");

  const free = clone(base);
  free.currentBet = 0;
  for (const player of free.players) player.bet = 0;
  const check = makePreAction(free, "check");
  assert.ok(check);
  assert.equal(reconcilePreAction(check, base), null, "a bet came in: CHECK cleared");
  const checkFold = makePreAction(free, "checkfold");
  assert.ok(reconcilePreAction(checkFold, base), "CHECK/FOLD survives a bet");
  const any = makePreAction(base, "callany");
  assert.ok(reconcilePreAction(any, raised), "CALL ANY survives a raise");
  const next = clone(base);
  next.handNumber += 1;
  for (const pre of [call, checkFold, any]) assert.equal(reconcilePreAction(pre, next), null, `${pre.kind}: new hand clears it`);
  const folded = clone(base);
  folded.players.find((player) => player.id === folded.viewerId).status = "folded";
  assert.equal(reconcilePreAction(any, folded), null, "folded: nothing to pre-act");
});

test("pre-actions: what is sent when your turn comes", () => {
  const turn = load("poker-flop-phone").state; // facing 67, check not allowed
  const hand = turn.handNumber;
  assert.deepEqual(preActionToSend({ kind: "checkfold", hand }, turn), { type: "fold" });
  assert.deepEqual(preActionToSend({ kind: "callany", hand }, turn), { type: "call" });
  assert.deepEqual(preActionToSend({ kind: "call", amount: 67, hand }, turn), { type: "call" });
  assert.equal(preActionToSend({ kind: "call", amount: 50, hand }, turn), null, "price changed");
  assert.equal(preActionToSend({ kind: "check", hand }, turn), null, "a bet is in");
  const free = clone(turn);
  free.you.legal = { ...free.you.legal, check: true, fold: false, call: 0 };
  assert.deepEqual(preActionToSend({ kind: "checkfold", hand }, free), { type: "check" });
  assert.deepEqual(preActionToSend({ kind: "check", hand }, free), { type: "check" });
  assert.deepEqual(preActionToSend({ kind: "callany", hand }, free), { type: "check" });
  assert.equal(preActionToSend({ kind: "callany", hand: hand - 1 }, turn), null, "an old hand's choice is never sent");
  assert.equal(preActionToSend({ kind: "callany", hand }, load("poker-flop8-phone").state), null, "not your turn");
});

// ---------------------------------------------------------------------------------------------
// Me bar: hand name and strength

test("hand strength meter: 5 steps from you.strength; hidden when off or null", () => {
  assert.equal(strengthSteps(null), null);
  assert.equal(strengthSteps(undefined), null);
  assert.equal(strengthSteps(0), 1);
  assert.equal(strengthSteps(0.42), 3);
  assert.equal(strengthSteps(0.85), 5);
  assert.equal(strengthSteps(1), 5);
  // you.strength = the chance to win by the river (POKER-API §3.5); the meter shows it in 5 steps.
  const state = load("poker-flop8-phone").state;
  assert.equal(typeof state.you.strength, "number");
  const on = renderPoker(state, at(state)).main;
  assert.match(on, new RegExp(`pkme__meter pkme__meter--${strengthSteps(state.you.strength)}`));
  assert.match(on, /aria-label="Hand strength [1-5] of 5/);
  assert.doesNotMatch(on, /ahead/i);
  assert.match(on, /pkme__hand">TWO PAIR, EIGHTS AND THREES</);
  const off = renderPoker(state, view({ now: state.now, pkPrefs: { ...DEFAULT_POKER_PREFS, strength: false } })).main;
  assert.doesNotMatch(off, /pkme__meter/);
  const noNames = renderPoker(state, view({ now: state.now, pkPrefs: { ...DEFAULT_POKER_PREFS, handNames: false } })).main;
  assert.doesNotMatch(noNames, /pkme__hand/);
  const none = load("poker-uncontested-phone").state; // strength null once the hand is over
  assert.doesNotMatch(renderPoker(none, at(none)).main, /pkme__meter/);
});

test("prefs: card colours follow the table unless this phone says 2 or 4", () => {
  assert.deepEqual(normalisePokerPrefs(null), DEFAULT_POKER_PREFS);
  assert.deepEqual(normalisePokerPrefs({ cards: "9", handNames: "x", strength: false }), { cards: "table", handNames: true, strength: false });
  const lobby = load("poker-lobby-phone").state; // fourColorDeck on
  assert.equal(fourColour({ cards: "table" }, lobby), true);
  assert.equal(fourColour({ cards: "2" }, lobby), false);
  assert.equal(fourColour({ cards: "4" }, { settings: {} }), true);
  const rows = pokerSettingsRows({ cards: "4", handNames: false, strength: true });
  assert.match(rows, /CARD COLOURS/);
  assert.match(rows, /data-arg="cards:4" aria-pressed="true"/);
  assert.match(rows, /data-arg="handNames:false" aria-pressed="true"/);
  const sheet = settingsHtml({ poker: rows });
  assert.doesNotMatch(sheet, />CAMERA</, "poker swaps the camera row for the card rows");
  assert.match(sheet, /HAND STRENGTH/);
  assert.match(settingsHtml({}), />CAMERA<.*FOLLOW MOVES/, "Estate unchanged");
  const tv = pokerSettingsOverlayHtml({}, { cards: "table", handNames: true }, {});
  assert.match(tv, /CARD COLOURS/);
  assert.doesNotMatch(tv, /CLOSE-UPS/);
});

// ---------------------------------------------------------------------------------------------
// Show, sit out, busted, results

test("show or muck, sit out, I'M BACK, rebuy (tournament and cash), final standings", () => {
  const won = load("poker-uncontested-phone").state;
  assert.match(renderPoker(won, at(won)).main, /data-pk="show">SHOW CARDS/);
  const sitout = load("poker-sitout-phone").state;
  assert.match(renderPoker(sitout, at(sitout)).main, /data-pk="sitin">I'M BACK/);
  const flop8 = load("poker-flop8-phone").state;
  assert.match(renderPoker(flop8, at(flop8)).main, /data-cmd="pk-sit-next" aria-pressed="false"/);
  const busted = load("poker-busted-phone").state;
  const bust = renderPoker(busted, at(busted)).main;
  assert.match(bust, /data-pk="rebuy"/);
  assert.match(bust, /data-rebuy-clock/, "tournament rebuy has a clock");
  assert.match(bust, /1 LEFT/);
  const cash = load("poker-cash-busted-phone").state;
  const cashHtml = renderPoker(cash, at(cash)).main;
  assert.match(cashHtml, /data-pk="rebuy"/);
  assert.doesNotMatch(cashHtml, /data-rebuy-clock/, "cash rebuy: no deadline");
  assert.doesNotMatch(cashHtml, /LEFT</, "cash rebuy: no limit");
  const over = load("poker-gameover-phone").state;
  const result = renderPoker(over, at(over));
  assert.equal(result.screen, "pk-result");
  assert.match(result.main, /SAM WINS/);
  assert.match(result.main, /\$4,500/);
  assert.equal(pokerRoute(over, { pk: { seeTable: true } }), "pk-table");
});

test("showdown: winning five lifted, the rest dimmed, hand names at the seats, pots to winners", () => {
  const state = load("poker-showdown-phone").state;
  const best = winningCards(state);
  assert.ok(best.size >= 5);
  const html = renderPoker(state, at(state)).main;
  assert.match(html, /pc pc--\w+  is-win/);
  assert.match(html, /is-dim/);
  assert.match(html, /MAIN · /);
  assert.match(html, /SIDE · /);
  assert.match(html, /pk-hand/);
  const model = tableModel(state);
  assert.ok(model.seats.some((seat) => seat.win));
  assert.equal(shortHandName("Two pair, Sixes and Twos"), "Two pair");
  assert.equal(shortHandName("Pair of Twos"), "Pair of Twos");
  assert.equal(rankOf("Pocket Sevens"), 8);
  // An uncontested win dims nothing.
  assert.doesNotMatch(renderPoker(load("poker-uncontested-phone").state, at(load("poker-uncontested-phone").state)).main, /is-dim/);
  // Run-out: win chances at the seats.
  const runout = load("poker-runout-phone").state;
  assert.match(renderPoker(runout, at(runout)).main, /pk-odds/);
});

// ---------------------------------------------------------------------------------------------
// Lobby, picker, games list

test("lobby: host rule chips (style, blinds, stack, blinds up, rebuys, timer, time bank, cards, computer); cash hides what it ignores", () => {
  const lobby = load("poker-lobby-phone").state;
  const keys = ruleChips(lobby).map((chip) => chip.key);
  assert.deepEqual(keys, ["style", "blinds", "stack", "speed", "rebuys", "timer", "bank", "deck"]);
  for (const chip of ruleChips(lobby)) {
    assert.equal(typeof chip.patch, "object", chip.key);
    if (chip.patch.startingStack) assert.ok(chip.patch.startingStack >= 10 * lobby.settings.bigBlind, "stack ≥ 10 big blinds");
  }
  assert.deepEqual(ruleChips(lobby).find((chip) => chip.key === "timer").patch, { actionSeconds: lobby.settings.actionSeconds === 30 ? 60 : 30 });
  const cash = load("poker-cash-lobby-phone").state;
  const cashKeys = ruleChips(cash).map((chip) => chip.key);
  assert.ok(!cashKeys.includes("speed") && !cashKeys.includes("rebuys"));
  assert.equal(ruleChips(cash).find((chip) => chip.key === "stack").short, "BUY-IN");
  const html = renderPoker(lobby, at(lobby)).main;
  assert.match(html, /data-cmd="pk-rule" data-arg="style"/);
  assert.match(html, /data-cmd="pk-switch-game" data-arg="estate"/, "host picker");
  assert.match(html, /data-cmd="pk-ai-level"/);
  assert.deepEqual(aiLevelsOf(lobby), ["easy", "normal"]);
  assert.equal(nextLevel(lobby, "easy"), "normal");
  assert.equal(nextLevel(lobby, "expert"), "normal", "only the levels the room offers");
  // A guest sees the same chips, not as buttons, and no picker.
  const guest = { ...lobby, viewerId: "p2", viewerIsHost: false };
  const guestHtml = renderPoker(guest, at(guest)).main;
  assert.doesNotMatch(guestHtml, /data-cmd="pk-rule"/);
  assert.doesNotMatch(guestHtml, /pk-switch-game/);
  assert.match(guestHtml, /WAITING FOR MAYA/);
});

test("picker: NEW GAME offers ESTATE / POKER; Estate's lobby lets the host switch; lists tag the game", () => {
  assert.match(gamePickHtml("poker"), /data-arg="poker" aria-pressed="true"/);
  assert.match(gamePickHtml("poker"), /data-arg="estate" aria-pressed="false"/);
  const join = { mode: "create", name: "Ana", tokenKind: "rocket", gameType: "poker", code: "", summary: null };
  const create = renderPhone(null, { session: null, join, palette: {} });
  assert.match(create.main, /data-cmd="pick-game" data-arg="poker" aria-pressed="true"/);
  assert.match(create.main, /CREATE GAME/);
  const estateLobby = JSON.parse(readFileSync(join_(webRoot, "fixtures", "lobby.json"), "utf8")).state;
  const lobbyOut = renderPhone(estateLobby, { session: { code: estateLobby.code }, tab: "turn", trade: {}, friends: { list: [], sent: {} } });
  if (estateLobby.hostId === estateLobby.viewerId) assert.match(lobbyOut.main, /data-cmd="switch-game" data-arg="poker"/);
  const games = [
    { code: "AAAAA", status: "playing", gameType: "poker", turn: 42, host: { name: "Sam", tokenKind: "teapot" }, players: [{ name: "Sam", tokenKind: "teapot" }] },
    { code: "BBBBB", status: "playing", gameType: "estate", turn: 7, host: { name: "Maya", tokenKind: "rocket" }, players: [{ name: "Maya", tokenKind: "rocket" }] },
  ];
  const home = renderPhone(null, { session: null, join: { code: "" }, games: { list: games }, palette: {} }).main;
  assert.match(home, /gtype--poker/);
  assert.match(home, /HAND 42/);
  assert.match(home, /TURN 7/);
  assert.equal(gameTypeOf({ gameType: "poker" }), "poker");
  assert.equal(gameTypeOf({}), "estate");
  assert.match(gtypeTag("estate"), /ESTATE/);
  // The table screen's start screen has the picker too, and its lobby for poker.
  const tvLobby = load("poker-lobby-table").state;
  const tv = pokerLobbyHtml(tvLobby, tableCtx);
  assert.match(tv, /data-cmd="pick-game" data-arg="estate"/);
  assert.match(tv, /data-cmd="pk-rule"/);
  assert.doesNotMatch(tv, /seat-keyboard|controller/i, "poker is phones only");
  // 0.10.2: the table (host) can take anyone off before the start, and start over with a new table.
  const seated = (tvLobby.players || []).filter((player) => player.status !== "left");
  if (tvLobby.viewerRole === "table" && seated.length) assert.equal((tv.match(/data-cmd="remove-player"/g) || []).length, seated.length);
  assert.match(tv, /data-cmd="new-table"/);
  assert.doesNotMatch(pokerLobbyHtml(tvLobby, { ...tableCtx, watching: true }), /remove-player|data-cmd="new-table"/);
  const estateTv =views.joinHtml({ ...estateLobby, viewerRole: "table", viewerId: null }, tableCtx);
  assert.match(estateTv, /data-cmd="pick-game" data-arg="poker"/);
});
const join_ = (...parts) => join(...parts);

test("how to play: ten hand ranks with example cards, yours marked", () => {
  const html = handRanksHtml({ yours: "Two pair, Kings and Sevens" });
  assert.equal((html.match(/<li/g) || []).length, 10);
  assert.match(html, /class="is-you"><span class="ranks__n">8/);
  assert.match(helpSheetHtml(load("poker-flop8-phone").state, view()), /BEST HANDS FIRST/);
});

test("sounds: deal, chips, check knock, fold, your turn, pot won", () => {
  const before = load("poker-flop8-phone").state;
  const after = clone(before);
  after.version += 1;
  after.actions = [...before.actions, { playerId: "p3", street: "flop", type: "fold", amount: 0, to: 0 }, { playerId: "p4", street: "flop", type: "call", amount: 50, to: 50 }];
  after.you = { ...after.you, toAct: true, legal: { fold: true, check: false, call: 50, raise: null, allIn: 50 } };
  const names = pokerSounds(before, after).map((hit) => hit.name);
  assert.deepEqual(names, ["fold", "chips", "turn"]);
  const next = clone(after);
  next.handNumber += 1;
  next.board = [];
  assert.equal(pokerSounds(after, next)[0].name, "dealCard");
  const show = load("poker-showdown-phone").state;
  const pre = clone(show);
  pre.result = null;
  const won = pokerSounds(pre, show).find((hit) => hit.name === "potWin");
  assert.ok(won && won.opts.mine, "Maya wins the side pot");
  assert.deepEqual(pokerSounds(null, show), []);
});

test("sound engine: the poker effects exist (synthesised) next to Estate's", async () => {
  const sound = await import("../shared/sound.js");
  for (const name of ["dealCard", "chips", "knock", "fold", "tick", "potWin"]) assert.ok(sound.SOUND_NAMES.includes(name), name);
  assert.equal(sound.play("chips"), false, "silent before a gesture, no throw");
});

// ---------------------------------------------------------------------------------------------
// In a browser at 390 × 844: everything fits (needs Playwright + Edge/Chrome; skipped otherwise)

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
  const { chromium } = createRequire(import.meta.url)(PW);
  for (const channel of ["msedge", "chrome"]) {
    try { return await chromium.launch({ channel, headless: true }); } catch { /* try the next */ }
  }
  return null;
}

// Rectangles that must not overlap: seat plates with each other, with face-up board cards and with
// bets; bets with board cards. Everything inside the table and the screen.
const LAYOUT_PROBE = `(() => {
  const r = (n) => { const b = n.getBoundingClientRect(); return { x: b.left, y: b.top, w: b.width, h: b.height, r: b.right, b: b.bottom }; };
  const hit = (a, b, pad = 1) => a.x + pad < b.r && b.x + pad < a.r && a.y + pad < b.b && b.y + pad < a.b;
  const name = (n) => n.closest("[data-player]")?.dataset.player || n.className;
  const plates = [...document.querySelectorAll(".pk-seat .pk-plate")];
  const bets = [...document.querySelectorAll(".pk-bet:not(.is-off)")];
  const cards = [...document.querySelectorAll(".pk-board .pc:not(.pc--slot)")];
  const table = document.querySelector(".pk-table");
  const out = [];
  for (let i = 0; i < plates.length; i += 1) for (let j = i + 1; j < plates.length; j += 1) if (hit(r(plates[i]), r(plates[j]))) out.push("plates " + name(plates[i]) + " / " + name(plates[j]));
  for (const p of plates) for (const c of cards) if (hit(r(p), r(c))) out.push("plate " + name(p) + " / board card");
  for (const p of plates) for (const b of bets) if (hit(r(p), r(b))) out.push("plate " + name(p) + " / bet " + name(b));
  for (const b of bets) for (const c of cards) if (hit(r(b), r(c))) out.push("bet " + name(b) + " / board card");
  if (table) {
    const t = r(table);
    for (const s of [...document.querySelectorAll(".pk-seat .pk-plate, .pk-bet:not(.is-off), .pk-board")]) { const x = r(s); if (x.x < t.x - 1 || x.r > t.r + 1 || x.y < t.y - 1 || x.b > t.b + 1) out.push("outside the table: " + name(s)); }
  }
  if (document.documentElement.scrollWidth > innerWidth + 1) out.push("page wider than the screen");
  const act = document.querySelector(".pkact");
  if (act && r(act).b > innerHeight + 1) out.push("buttons below the screen");
  const seats = document.querySelectorAll(".pk-seat").length;
  const small = [...document.querySelectorAll(".pk-name, .pk-stack, .pkbtn__label, .pkpre__label")].filter((n) => parseFloat(getComputedStyle(n).fontSize) < 9.5).length;
  if (small) out.push(small + " labels under 9.5px");
  return { problems: out, seats, screen: document.querySelector("#app")?.dataset.screen };
})()`;

test("browser: every poker phone fixture and scene fits 390 × 844 with no overlaps", { timeout: 240000 }, async (t) => {
  const browser = await launch();
  if (!browser) {
    t.skip("Playwright or a browser is not available");
    return;
  }
  const server = await serveWeb();
  const base = `http://127.0.0.1:${server.address().port}`;
  const failures = [];
  try {
    const context = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1, isMobile: true, hasTouch: true });
    const list = [...phoneNames, ...POKER_SCENES.filter((scene) => !scene.base.endsWith("-table")).map((scene) => scene.name)];
    for (const name of list) {
      const page = await context.newPage();
      const errors = [];
      page.on("pageerror", (error) => errors.push(error.message));
      await page.goto(`${base}/play?demo=${name}`, { waitUntil: "load" });
      await page.waitForFunction(() => document.querySelector("#app")?.dataset.screen?.startsWith("pk-"), null, { timeout: 15000 });
      await page.waitForTimeout(500);
      const report = await page.evaluate(LAYOUT_PROBE);
      if (errors.length) failures.push(`${name}: ${errors.join("; ")}`);
      for (const problem of report.problems) failures.push(`${name}: ${problem}`);
      if (["poker-flop8-phone", "poker-8-pre", "poker-8-turn", "poker-8-raise"].includes(name) && report.seats !== 8) failures.push(`${name}: ${report.seats} seats drawn, not 8`);
      await page.close();
    }
    // The table screen at 1600 × 900: the same checks.
    const tv = await browser.newContext({ viewport: { width: 1600, height: 900 } });
    for (const name of [...tableNames.filter((n) => !n.includes("lobby")), "poker-8-table"]) {
      const page = await tv.newPage();
      await page.goto(`${base}/?demo=${name}`, { waitUntil: "load" });
      await page.waitForFunction(() => globalThis.estateDemo?.ready?.(), null, { timeout: 15000 });
      await page.waitForTimeout(400);
      const report = await page.evaluate(LAYOUT_PROBE);
      for (const problem of report.problems) failures.push(`table ${name}: ${problem}`);
      await page.close();
    }
  } finally {
    await browser.close();
    server.close();
  }
  assert.deepEqual(failures, []);
});
