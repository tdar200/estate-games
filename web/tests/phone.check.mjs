// Checks for the phone (UX review 1 Nov 2026 + round 2): the BOARD tab for every role (strip, board,
// one status line, buttons; no history list), outcome cues, getting back in ("WHO ARE YOU?" seat
// takeover, seat moved, back to the last table), the one-page trade, and the "one glance" word
// budgets. Pure: renders fixtures through web/phone/*.js in Node. Run: node web/tests/phone.check.mjs

import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { cueHtml, logCues, newCues, turnCues } from "../phone/cues.js";
import { impactHtml, miniMapHtml, setOwnersHtml, stateAfter, tradeImpact, tradeImpactLocal } from "../phone/impact.js";
import { animatable } from "../phone/playback.js";
import { DEFAULT_AUDIO, cardHoldMs, cardReadMs, loadAudio, moodFor, normaliseAudio, saveAudio, sfxForAction, sfxForCue, sfxForEvent } from "../phone/audio.js";
import { DEFAULT_PREFS, leaderNow, loadPrefs, normalisePrefs, paceOf, reducedMotion, savePrefs, worthWithLoans } from "../phone/settings.js";
import * as insights from "../shared/insights.js";
import { netWorth } from "../shared/insights.js";
import { LAYOUTS, boardModeFor, deskGameFrame, hopZoom, layoutFor } from "../phone/layout.js";
import { SCENES, SECTIONS, buildScene } from "../phone/scenarios.js";
import { ambientBoard, audioControlsHtml, finalStandings, settingsHtml, gameAction, humanSeats, joinMode, renderPhone, route } from "../phone/screens.js";
import { REMATCH_GUARD_MS, RESULT_SCREENS, rematchReady, resultShownAt } from "../phone/parts.js";
import { pokerRoute } from "../phone/poker-screens.js";
import { ludoRoute } from "../phone/ludo-screens.js";
import { quizRoute } from "../phone/quiz-screens.js";
import { closestRoute } from "../phone/closest-screens.js";
import { cardTarget, heatSquares } from "../phone/stage.js";
import { stripHtml } from "../phone/strip.js";
import {
  cleanDraft, emptyDraft, loanActionOf, loanComposeScreen, loanRepay, plannedTrade, quoteKey, tradeActionOf, tradeBlocker, tradeScreen, tradeValid, wantsQuote,
} from "../phone/trade.js";
import { TOKEN_COLORS, playerColor } from "../shared/tokens.js";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "fixtures");
const load = (name) => JSON.parse(readFileSync(join(dir, `${name}.json`), "utf8"));
const names = readdirSync(dir).filter((file) => file.endsWith(".json") && file !== "index.json").map((file) => file.replace(/\.json$/, ""));
const stateFixtures = names.filter((name) => load(name).state && load(name).state.viewerRole !== "table");

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

// Words a person actually reads: tags, SVG and attributes stripped, entities decoded.
export function visibleWords(html) {
  const text = String(html || "")
    .replace(/<svg[\s\S]*?<\/svg>/g, " ")
    .replace(/<[^>]+>/g, " ")
    .replace(/&#0?39;/g, "'").replace(/&amp;/g, "&").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, "\"");
  return text.split(/\s+/).filter((word) => /[A-Za-z0-9$]/.test(word));
}

const ui = (extra = {}) => ({ session: { code: "NXGNT" }, tab: "turn", trade: emptyDraft(), friends: { list: [], sent: {} }, ...extra });
const asSpectator = (state) => ({ ...state, viewerId: null, viewerRole: "spectator" });
const textOf = (out) => [out.header, out.banner, out.main, out.nav, out.board?.stage, out.board?.status].join("\n");
const byName = (state, name) => state.players.find((player) => player.name === name);
const withLog = (state, lines) => ({ ...state, log: [...state.log, ...lines.map((text, index) => ({ id: `log_test_${index}`, text, at: 0 }))] });
const mineOf = (state, id) => Object.entries(state.properties).filter(([, prop]) => prop.ownerId === id).map(([index]) => Number(index));

// ---- Board tab ---------------------------------------------------------------------------------

test("board tab renders for every fixture, as the seated player and as a spectator, without leaks", () => {
  for (const name of stateFixtures) {
    const { state } = load(name);
    for (const [label, view] of [["player", state], ["spectator", asSpectator(state)]]) {
      const out = renderPhone(view, ui());
      for (const part of ["header", "banner", "main", "nav"]) clean(out[part], `${name}/${label}/${part}`);
      if (out.board) {
        clean(out.board.stage, `${name}/${label}/stage`);
        clean(out.board.status, `${name}/${label}/status`);
        assert.equal(typeof out.board.stage, "string", `${name}/${label}: stage html`);
        if (out.screen !== "roll" && out.screen !== "waiting") assert.ok(out.board.stage.length > 20, `${name}/${label}: the stage shows the decision`);
        assert.match(out.board.status, /class="pstatus/, `${name}/${label}: one status line`);
        assert.equal(out.board.ticker, undefined, `${name}/${label}: no ticker list under the board`);
      }
    }
  }
});

test("board tab: your turn is strip, board (blank dice, risk wash), YOUR TURN and one ROLL button", () => {
  const { state } = load("roll");
  const out = renderPhone(state, ui());
  assert.equal(out.screen, "roll");
  assert.equal(out.board.stage, "", "nothing in the centre before you roll: no YOUR TURN card, no '?' dice");
  assert.match(out.board.status, /pstatus--gold/);
  assert.match(out.board.status, />YOUR TURN</);
  assert.ok(Array.isArray(out.board.heat) && out.board.heat.length > 0, "squares ahead that cost money are shaded");
  for (const square of out.board.heat) assert.ok(square.level > 0 && square.level <= 1);
  assert.match(out.main, /"type":"roll"/);
  assert.doesNotMatch(out.main, /btn__detail/, "no sub-label under ROLL DICE");
  assert.doesNotMatch(out.main, /data-arg="trade"/, "TRADE lives in the nav, not twice");
  assert.match(out.header, /class="strip strip--n4"/);
  assert.match(out.header, /plate is-turn is-me/);
  assert.match(out.header, /plate__flag--next/);
  assert.match(out.nav, />BOARD</);
  assert.match(out.nav, />MY DEEDS</);
  assert.match(out.nav, />MORE</);
});

test("board tab: someone else's turn, a spectator and a player who is out all see the board and a status line", () => {
  const { state } = load("waiting");
  const current = state.players.find((player) => player.id === state.currentPlayerId);
  const player = renderPhone(state, ui());
  assert.equal(player.screen, "waiting");
  assert.match(player.board.status, new RegExp(`${current.name.toUpperCase()}&#039;S TURN`));
  assert.doesNotMatch(player.main, /PLAN A TRADE|for your turn/, "no extra buttons while you wait");

  const spectator = renderPhone(asSpectator(state), ui());
  assert.equal(spectator.screen, "waiting");
  assert.ok(spectator.board, "spectators get the board");
  assert.match(spectator.header, /class="strip/, "spectators get the player strip");
  assert.doesNotMatch(spectator.main, /data-act=/, "spectators get no decision buttons");
  assert.match(spectator.nav, />PLAYERS</);
  assert.doesNotMatch(spectator.nav, /MY DEEDS|>TRADE</);
  const players = renderPhone(asSpectator(state), ui({ tab: "players" }));
  assert.equal(players.screen, "players");
  assert.equal((players.main.match(/<li class="pl-row/g) || []).length, state.players.length);

  const me = state.players.find((candidate) => candidate.id === state.viewerId);
  const out = { ...state, players: state.players.map((candidate) => (candidate.id === me.id ? { ...candidate, bankrupt: true, cash: 0 } : candidate)) };
  const bankrupt = renderPhone(out, ui());
  assert.ok(bankrupt.board);
  assert.match(bankrupt.board.status, /YOU&#039;RE OUT/);
  assert.match(bankrupt.nav, />PLAYERS</);
});

test("board tab: decisions keep their buttons under the board", () => {
  const cases = { buy: /"type":"buy"/, auction: /"type":"pass_auction"/, rent: /"type":"end_turn"/, "turn-done": /"type":"end_turn"/, jail: /"type":"pay_bail"/, card: /"type":"confirm_card"/, doubles: /"type":"roll"/, order: /roll_for_order|YOU ROLLED|WAITING/ };
  for (const [name, pattern] of Object.entries(cases)) {
    const out = renderPhone(load(name).state, ui());
    assert.ok(out.board, `${name}: board shown`);
    assert.match(out.main, pattern, `${name}: decision button`);
  }
  const buy = renderPhone(load("buy").state, ui());
  assert.match(buy.board.stage, /FOR SALE/);
  assert.match(buy.board.stage, /class="pips"/, "set pips show who owns the rest of the set");
  const auction = renderPhone(load("auction").state, ui());
  assert.match(auction.board.status, /AUCTION · YOUR BID/);
  assert.match(auction.main, /class="bidbar"/, "safe bid zone instead of a sentence");
  const card = load("card").state;
  const cardOut = renderPhone(card, ui());
  assert.equal(cardOut.board.marks.target, cardTarget(card, card.viewerId, card.pendingCard.cardId));
  assert.ok(cardOut.board.marks.path.length > 1, "the card's path is drawn on the board");
});

test("copy: no history list, no filler sub-labels, no probability sentences", () => {
  const filler = /everyone&#039;s in|everyone's in|nothing changes|Nothing changes|everyone bids|or shake|Next: |swap now|leave this auction|then roll|1 in 6|you keep \$|What just happened|Look at both piles|Tap the deeds/;
  for (const name of stateFixtures) {
    const out = renderPhone(load(name).state, ui());
    assert.doesNotMatch(textOf(out), /Next roll looks safe|looks safe|Staying in is safe/, `${name}: no probability sentence`);
    assert.doesNotMatch(textOf(out), filler, `${name}: no filler sub-labels`);
    assert.doesNotMatch(textOf(out), /class="ticker|class="cue /, `${name}: no cue list on the screen`);
  }
  const done = renderPhone(load("turn-done").state, ui());
  assert.doesNotMatch(done.main, /RAISE CASH/, "RAISE CASH only when you're short");
  const doubles = renderPhone(load("doubles").state, ui());
  assert.match(doubles.board.stage, /DOUBLES/);
  assert.match(doubles.board.stage, /pdice__count/, "doubles counter toward jail");
  // A greyed button still says why.
  const buy = load("buy").state;
  const poor = { ...buy, players: buy.players.map((player) => (player.id === buy.viewerId ? { ...player, cash: 5 } : player)) };
  assert.match(renderPhone(poor, ui()).main, /need \$\d+ more/);
});

// "One glance": visible words on the main states stay within a small budget.
// Plates (cash + name per player) and the nav are about 17 of these words on every in-game screen.
// Trade carries more on purpose (owner: "I dont have all the information"): set progress, a verdict.
// Waiting and roll carry the at-a-glance panel (review 4 Nov #8): you, a danger chip, the last move.
const BUDGETS = { lobby: 45, waiting: 36, roll: 36, buy: 32, auction: 42, debt: 42, trade: 90, tradeIncoming: 50 };
function wordsOn(out) {
  return visibleWords([out.header, out.banner, out.main, out.nav, out.board?.stage || "", out.board?.status || ""].join(" ")).length;
}
test("one glance: visible words per main state stay within budget", () => {
  const counts = {
    lobby: wordsOn(renderPhone(load("lobby").state, ui())),
    waiting: wordsOn(renderPhone(load("waiting").state, ui())),
    roll: wordsOn(renderPhone(load("roll").state, ui())),
    buy: wordsOn(renderPhone(load("buy").state, ui())),
    auction: wordsOn(renderPhone(load("auction").state, ui())),
    debt: wordsOn(renderPhone(load("debt").state, ui())),
    tradeIncoming: wordsOn(renderPhone(load("trade").state, ui())),
  };
  const roll = load("roll").state;
  const alpha = byName(roll, "Alpha");
  counts.trade = wordsOn(renderPhone(roll, ui({ tab: "trade", trade: { ...emptyDraft(), targetId: alpha.id, offer: [19], request: [13] } })));
  for (const [state, budget] of Object.entries(BUDGETS)) assert.ok(counts[state] <= budget, `${state}: ${counts[state]} words > ${budget}`);
});

test("strip: one plate per player in the player's colour, TURN and NEXT flags", () => {
  const { state } = load("roll");
  const html = stripHtml(state, { viewerId: state.viewerId });
  assert.equal((html.match(/data-plate=/g) || []).length, state.players.length);
  for (const player of state.players) assert.ok(html.includes(`--plate-color:${playerColor(state, player)}`), `${player.name} plate colour`);
  assert.equal((html.match(/plate__flag--turn/g) || []).length, 1);
  const six = { ...state, players: [...state.players, { ...state.players[0], id: "x5", name: "Eve" }, { ...state.players[1], id: "x6", name: "Zed" }] };
  assert.match(stripHtml(six, { viewerId: state.viewerId }), /strip--n6/);
});

test("cues: tax, PAYDAY, rent, auction, trade results and jail reasons become short chips", () => {
  const { state } = load("roll");
  const me = byName(state, "Robin");
  const tax = logCues(withLog(state, ["Robin's turn", "Robin rolled 1 + 3 = 4", "Robin landed on Stamp Duty", "Robin paid $200 (Stamp Duty)"]), me.id);
  const taxCue = tax.find((cue) => cue.text === "Stamp Duty");
  assert.ok(taxCue, "Stamp Duty cue");
  assert.equal(taxCue.amount, -200);
  assert.equal(taxCue.tone, "neg");
  const go = logCues(withLog(state, ["Robin passed PAYDAY and collected $200"]), me.id).pop();
  assert.equal(go.text, "PAYDAY");
  assert.equal(go.amount, 200);
  const jackpot = logCues(withLog(state, ["Robin collected the $140 City Park jackpot"]), me.id).pop();
  assert.equal(jackpot.text, "City Park");
  assert.equal(jackpot.amount, 140);
  assert.match(logCues(withLog(state, ["Robin used a Jail pass"]), me.id).pop().text, /out of jail/);
  const rent = logCues(withLog(state, ["Robin paid $26 (Golden Leaf Road rent)"]), me.id).pop();
  assert.equal(rent.kind, "rent");
  assert.equal(rent.tone, "neg");
  const others = logCues(withLog(state, ["Sam paid $26 (Golden Leaf Road rent)"]), me.id).pop();
  assert.equal(others.tone, "", "someone else's rent is neutral, not +$");
  const auction = logCues(withLog(state, ["Alpha won Skyline Road for $140"]), me.id).pop();
  assert.equal(auction.kind, "auction");
  assert.match(auction.text, /SOLD Skyline Rd → Alpha/);
  const accepted = logCues(withLog(state, ["Sam accepted Robin's trade"]), me.id).pop();
  assert.match(accepted.text, /Sam accepted your trade/);
  assert.equal(accepted.important, true);
  const jails = [
    [["Maya's turn", "Maya rolled 4 + 4 = 8", "Maya landed on Busted", "Maya was sent to jail"], "Busted"],
    [["Maya's turn", "Maya rolled 2 + 2 = 4", "Maya rolled doubles three times in a row", "Maya was sent to jail"], "3 doubles"],
    [["Maya's turn", "Maya landed on Headlines", "Maya drew: Caught speeding on camera: off to jail", "Maya was sent to jail"], "card"],
  ];
  for (const [lines, reason] of jails) {
    const cue = logCues(withLog(state, lines), me.id).pop();
    assert.equal(cue.kind, "jail");
    assert.equal(cue.reason, reason);
  }
  const next = withLog(state, ["Alpha won Skyline Road for $140", "Sam declined the trade"]);
  assert.deepEqual(newCues(state, next, me.id).map((cue) => cue.kind), ["auction", "trade"]);
  const turn = turnCues(withLog(state, ["Robin rolled 1 + 3 = 4", "Robin landed on Stamp Duty", "Robin paid $200 (Stamp Duty)"]), me.id);
  assert.equal(turn.length, 1, "quiet roll/land lines are not cues");
});

test("playback: order rolls aren't board animations", () => {
  const events = [{ type: "dice", dice: [1, 2], order: true }, { type: "dice", dice: [3, 4] }, { type: "move", path: [1, 2] }, { type: "auction" }];
  assert.deepEqual(animatable(events).map((event) => event.type), ["dice", "move"]);
});

test("heat: only squares that cost money (and Busted) are shaded", () => {
  const { state } = load("roll");
  const me = byName(state, "Robin");
  for (const square of heatSquares(state, me.id)) {
    const space = state.board[square.index];
    const prop = state.properties[square.index];
    assert.ok(space.type === "tax" || space.type === "goToJail" || (prop?.ownerId && prop.ownerId !== me.id), `${space.name} costs money`);
  }
});

// ---- Getting back in ---------------------------------------------------------------------------

// A room summary as the server sends it (docs/API.md "Getting back in").
function summaryOf(state, { offline = [], status = state.status } = {}) {
  return {
    code: state.code,
    status,
    host: { name: "Robin" },
    players: state.players.map((player) => {
      const pc = player.input === "controller" || player.input === "keyboard";
      const off = offline.includes(player.name);
      return {
        id: player.id, name: player.name, tokenKind: player.tokenKind, color: playerColor(state, player), input: player.input,
        online: player.input === "ai" ? true : !off, reclaimable: off && !pc && player.input !== "ai" && !player.bankrupt,
      };
    }),
    openSeats: 0,
  };
}
const joinUi = (join, extra = {}) => ({ session: null, join: { name: "", tokenKind: null, ...join }, ...extra });

test("who are you: a started game lists the human seats; offline ones are tappable, online ones say playing", () => {
  const { state } = load("waiting");
  const summary = summaryOf(state, { offline: ["Robin"] });
  const out = renderPhone(null, joinUi({ code: state.code, lockedCode: true, summary }));
  assert.equal(out.screen, "join");
  assert.equal(joinMode({ code: state.code, summary }), "who");
  assert.match(out.main, /WHO ARE YOU\?/);
  const robin = byName(state, "Robin");
  const sam = byName(state, "Sam");
  const alpha = byName(state, "Alpha");
  assert.match(out.main, new RegExp(`data-cmd="reclaim" data-arg="${robin.id}"`), "your offline seat is one tap");
  assert.doesNotMatch(out.main, new RegExp(`data-arg="${sam.id}"`), "an online seat can't be tapped");
  assert.match(out.main, /seat is-off[^>]*>[\s\S]*?SAM[\s\S]*?playing/, "online seats are greyed with 'playing'");
  assert.doesNotMatch(out.main, new RegExp(`>${alpha.name.toUpperCase()}<`), "computers are not seats you can take");
  assert.equal(humanSeats(summary).length, state.players.filter((player) => player.input !== "ai").length);
  assert.match(out.main, /at the PC/, "the PC's controller seat says where it is");
  assert.match(out.main, /data-cmd="watch"/);
  assert.match(out.main, /JUST WATCH/);
  assert.doesNotMatch(out.main, /JOIN AS NEW PLAYER/, "no new seats once the game started");
  assert.doesNotMatch(out.main, /id="p-name"/, "no form to fill in");
});

test("who are you: in the lobby, offline seats plus JOIN AS NEW PLAYER; with none offline, straight to the form", () => {
  const { state } = load("lobby");
  const withOffline = renderPhone(null, joinUi({ code: state.code, lockedCode: true, summary: summaryOf(state, { offline: ["Sam"] }) }));
  assert.match(withOffline.main, /WHO ARE YOU\?/);
  assert.match(withOffline.main, /data-cmd="join-new"[^>]*>[\s\S]*?JOIN AS NEW PLAYER/);
  assert.match(withOffline.main, /JUST WATCH/);
  const form = renderPhone(null, joinUi({ code: state.code, lockedCode: true, summary: summaryOf(state, { offline: ["Sam"] }), mode: "new" }));
  assert.match(form.main, /id="p-name"/);
  assert.match(form.main, /data-cmd="join-who"/, "a way back to the seats");
  const allOnline = renderPhone(null, joinUi({ code: state.code, lockedCode: true, summary: summaryOf(state) }));
  assert.match(allOnline.main, /NEW PLAYER/);
  assert.match(allOnline.main, /id="p-name"/);
  assert.doesNotMatch(allOnline.main, /WHO ARE YOU/);
});

test("who are you: 'still connected' shows how to fix it and a retry; a moved seat says so", () => {
  const { state } = load("waiting");
  const sam = byName(state, "Sam");
  const summary = summaryOf(state, { offline: ["Robin"] });
  const conflict = renderPhone(null, joinUi({ code: state.code, summary, reclaim: { playerId: sam.id, status: 409, error: "That seat is still connected." } }));
  assert.match(conflict.main, /Close it on the other phone, or wait 20 s/);
  assert.match(conflict.main, new RegExp(`data-cmd="reclaim" data-arg="${sam.id}"[^>]*>RETRY`));
  const moved = renderPhone(null, joinUi({ code: state.code, summary, note: "Your seat moved to another phone." }));
  assert.match(moved.main, /Your seat moved to another phone/);
  assert.match(moved.main, /WHO ARE YOU\?/, "taking it back is one tap away");
  const other = renderPhone(null, joinUi({ code: state.code, summary, reclaim: { playerId: sam.id, status: 400, error: "That seat can't be taken over" } }));
  assert.match(other.main, /can&#039;t be taken over/);
});

test("getting back: no code offers the last table; an old server falls back to this phone's seat", () => {
  const { state } = load("waiting");
  const blank = renderPhone(null, joinUi({ code: "" }, { lastTable: { code: "LW262" } }));
  assert.equal(blank.screen, "join");
  assert.match(blank.main, /data-cmd="back-to-table" data-arg="LW262"[^>]*>[\s\S]*?BACK TO TABLE LW262/);
  assert.match(blank.main, /id="p-code"/);
  const fresh = renderPhone(null, joinUi({ code: "" }));
  assert.doesNotMatch(fresh.main, /BACK TO TABLE/);
  // A summary without seat ids (old server): the grid can't be tapped, GET MY SEAT BACK can.
  const old = { ...summaryOf(state), players: state.players.map((player) => ({ name: player.name, tokenKind: player.tokenKind, input: player.input })) };
  const out = renderPhone(null, joinUi({ code: state.code, summary: old }));
  assert.match(out.main, /GET MY SEAT BACK/);
  assert.doesNotMatch(out.main, /data-cmd="reclaim"/);
});

test("join form: remote invitees get no Wi-Fi tips; the picker shows the shared palette", () => {
  const lobby = load("lobby").state;
  const join = { code: lobby.code, name: "Ravi", tokenKind: "umbrella", summary: { status: "lobby", players: [] } };
  const remote = renderPhone(null, { session: null, remote: true, join });
  assert.equal(remote.screen, "join");
  assert.doesNotMatch(remote.main, /Wi-Fi/);
  for (const [kind, color] of Object.entries(TOKEN_COLORS)) assert.match(remote.main, new RegExp(`data-token="${kind}"[^>]*--pick:${color}`, "i"));
});

test("game over: the host can play again at the same table; everyone can leave (and come back)", () => {
  const victory = load("victory").state;
  const host = renderPhone(victory, ui());
  assert.equal(host.screen, "result");
  assert.match(host.main, /data-cmd="rematch"/);
  assert.match(host.main, /data-cmd="leave"/);
  const guest = renderPhone({ ...victory, hostId: "someone-else" }, ui());
  assert.doesNotMatch(guest.main, /data-cmd="rematch"/);
  assert.match(guest.main, /LEAVE TABLE/);
});

test("game over: a tap left over from the last turn can't start a rematch (PLAY AGAIN waits 1.5 s)", () => {
  assert.equal(REMATCH_GUARD_MS, 1500);
  // Every game's result screen (the ones with PLAY AGAIN) arms the guard.
  const finished = { status: "finished" };
  for (const screen of [route(load("victory").state, ui()), pokerRoute(finished), ludoRoute(finished), quizRoute(finished), closestRoute(finished)]) {
    assert.ok(RESULT_SCREENS.has(screen), `${screen} arms the guard`);
  }
  const at = resultShownAt("result", "finished", null, 10_000);
  assert.equal(at, 10_000);
  assert.equal(rematchReady(at, 10_000), false, "a tap as the screen appears is ignored");
  assert.equal(rematchReady(at, 11_499), false);
  assert.equal(rematchReady(at, 11_500), true, "after 1.5 s PLAY AGAIN works");
  assert.equal(rematchReady(null, 99_999), false, "no result screen yet: ignored");
  // Not armed before the result screen; a tab away (MY DEEDS, PLAYERS) and back keeps the first time.
  assert.equal(resultShownAt("roll", "playing", null, 9_000), null);
  assert.equal(resultShownAt("property", "finished", null, 9_500), null);
  assert.equal(resultShownAt("players", "finished", at, 12_000), at);
  assert.equal(resultShownAt("result", "finished", at, 13_000), at);
  // A new game forgets it, so the next game's result screen waits again.
  assert.equal(resultShownAt("lobby", "lobby", at, 20_000), null);
  // The phone's PLAY AGAIN goes through the guard before it posts /rematch.
  const source = readFileSync(join(here, "..", "phone", "phone.js"), "utf8");
  assert.match(source, /case "rematch":\s*(?:\/\/[^\n]*\s*)*if \(!rematchReady\(ui\.resultShownAt, Date\.now\(\)\)\) return undefined;\s*return call\("POST \/rematch"/);
  assert.match(source, /ui\.resultShownAt = resultShownAt\(screen, ui\.state\?\.status, ui\.resultShownAt, Date\.now\(\)\)/);
});

test("lobby, MY DEEDS and MORE are short", () => {
  const lobby = renderPhone(load("lobby-rules").state, ui());
  assert.match(lobby.main, /class="rchips"/);
  assert.match(lobby.main, /aria-label="Loans between players: /);
  assert.doesNotMatch(lobby.main, /everyone|change in rules|TABLE RULES/);
  const deeds = renderPhone(load("roll").state, ui({ tab: "property" }));
  assert.doesNotMatch(deeds.main, /trade for the set|View only until your turn|blocks /);
  const more = renderPhone(load("waiting").state, ui({ tab: "friends" }));
  assert.match(more.main, /class="tableline"/, "the table code, to get back in later");
  assert.doesNotMatch(more.main, /People you&#039;ve played with/);
});

// ---- Trade, one page ---------------------------------------------------------------------------

test("trade: one page, no partner preselected, nothing in the deal, deed areas greyed until a partner is picked", () => {
  const { state } = load("roll");
  const me = byName(state, "Robin");
  const out = renderPhone(state, ui({ tab: "trade" }));
  assert.equal(out.screen, "trade");
  assert.doesNotMatch(out.main, /tchip is-on|tcard is-on|aria-pressed="true"/, "nothing highlighted on arrival");
  assert.equal((out.main.match(/data-cmd="trade-partner"/g) || []).length, state.players.length - 1);
  assert.doesNotMatch(out.main, /data-toggle=/, "deeds can't be tapped before a partner is picked");
  assert.equal((out.main.match(/class="tside is-off"/g) || []).length, 2, "both sides greyed");
  for (const index of mineOf(state, me.id)) assert.match(out.main, new RegExp(`aria-label="${state.board[index].name}"`), "your deeds are visible");
  assert.match(out.main, /pick a partner/);
  assert.match(out.main, /data-cmd="trade-send"[^>]*disabled/);
  assert.doesNotMatch(out.main, /trade-step|tsteps/, "no wizard steps");
});

test("trade: with a partner, YOU GIVE has only your deeds and YOU GET only theirs, both with cash steppers", () => {
  const { state } = load("roll");
  const me = byName(state, "Robin");
  const sam = byName(state, "Sam");
  const out = renderPhone(state, ui({ tab: "trade", trade: { ...emptyDraft(), targetId: sam.id } }));
  const offers = [...out.main.matchAll(/data-toggle="offer:(\d+)"/g)].map((match) => Number(match[1])).sort((a, b) => a - b);
  const requests = [...out.main.matchAll(/data-toggle="request:(\d+)"/g)].map((match) => Number(match[1])).sort((a, b) => a - b);
  assert.deepEqual(offers, mineOf(state, me.id).sort((a, b) => a - b));
  assert.deepEqual(requests, mineOf(state, sam.id).sort((a, b) => a - b));
  assert.match(out.main, /YOU GIVE[\s\S]*YOU GET/);
  assert.match(out.main, /data-cmd="trade-cash" data-arg="offer:50"/);
  assert.match(out.main, /data-cmd="trade-cash" data-arg="request:50"/);
  assert.doesNotMatch(out.main, /type="number"/, "no $0 text box");
  assert.doesNotMatch(out.main, /tcard is-on/);
  assert.match(out.main, /class="tdock"[\s\S]*class="tsum"/, "sticky footer with the value bar");
  assert.match(out.main, /add to YOU GIVE|tap some deeds/, "SEND says why it waits");
  const picked = renderPhone(state, ui({ tab: "trade", trade: { ...emptyDraft(), targetId: sam.id, offer: [19] } }));
  assert.match(picked.main, /tcard is-on/);
  assert.match(picked.main, /tcard__tick/);
});

test("trade: a player who owns nothing sees 'none' and only the cash stepper; locked deeds say why", () => {
  const { state } = load("roll");
  const me = byName(state, "Robin");
  const sam = byName(state, "Sam");
  const properties = Object.fromEntries(Object.entries(state.properties).map(([index, prop]) => [index, prop.ownerId === me.id ? { ...prop, ownerId: null } : prop]));
  const out = renderPhone({ ...state, properties }, ui({ tab: "trade", trade: { ...emptyDraft(), targetId: sam.id } }));
  assert.match(out.main, /aria-label="YOU GIVE">[\s\S]*?<div class="tnone">no deeds<\/div>/);
  assert.doesNotMatch(out.main, /data-toggle="offer:/);
  assert.match(out.main, /trade-cash/);
  // Houses on Sam's set lock its deeds.
  const built = { ...state, properties: { ...state.properties, 3: { ...state.properties[3], houses: 1 } } };
  const locked = renderPhone(built, ui({ tab: "trade", trade: { ...emptyDraft(), targetId: sam.id } }));
  assert.match(locked.main, /tcard is-locked[^>]*sell houses first/);
});

test("trade: the computer's price is an optional ASK chip that changes the deal only when tapped", () => {
  const { state } = load("roll");
  const alpha = byName(state, "Alpha");
  const sam = byName(state, "Sam");
  const draft = { ...emptyDraft(), targetId: alpha.id, offer: [19], request: [13] };
  assert.equal(wantsQuote(state, draft), true);
  assert.equal(wantsQuote(state, { ...draft, targetId: sam.id }), false, "people don't quote");
  const out = renderPhone(state, ui({ tab: "trade", trade: { ...draft, quote: { key: quoteKey(draft), offerCash: 82, requestCash: 0 } } }));
  assert.match(out.main, /data-cmd="trade-quote-use" data-arg="82:0"[^>]*>[\s\S]*?ACCEPTS AT \$82[\s\S]*?\$82 MORE NEEDED/);
  assert.match(out.main, /aria-label="You give \$\d+, you get/, "value bar");
  assert.equal(tradeActionOf(cleanDraft(state, draft, state.viewerId).draft).offer.cash, 0, "the quote is not in the deal");
  const part = renderPhone(state, ui({ tab: "trade", trade: { ...draft, offerCash: 50, quote: { key: quoteKey(draft), offerCash: 82, requestCash: 0 } } }));
  assert.match(part.main, /\$32 MORE NEEDED/, "your cash is compared with the last price, on the phone");
  const stale = renderPhone(state, ui({ tab: "trade", trade: { ...draft, offer: [39], quote: { key: quoteKey(draft), offerCash: 82, requestCash: 0 } } }));
  assert.doesNotMatch(stale.main, /ACCEPTS AT \$82/, "a price for other deeds is not shown");
  const applied = renderPhone(state, ui({ tab: "trade", trade: { ...draft, offerCash: 82, quote: { key: quoteKey(draft), offerCash: 82, requestCash: 0 } } }));
  assert.doesNotMatch(applied.main, /data-cmd="trade-quote-use"/);
  assert.match(applied.main, /tquote--ok[^>]*disabled>[\s\S]*?WILL ACCEPT/);
  assert.match(applied.main, /data-cmd="trade-send"(?![^>]*disabled)/);
  const never = renderPhone(state, ui({ tab: "trade", trade: { ...draft, quote: { key: quoteKey(draft), error: "No deal: that would give you a full set." } } }));
  assert.match(never.main, /tquote--never[\s\S]*?WON(?:'|&#039;)T TRADE[\s\S]*?THAT WOULD GIVE YOU A FULL SET/);
  assert.match(never.main, /data-cmd="trade-send"(?![^>]*disabled)/, "you may still send it (it will be declined, with a reason)");
  // While the computer thinks (Grandmaster prices deals itself, which takes a moment) the chip waits.
  const thinking = renderPhone(state, ui({ tab: "trade", trade: { ...draft, quote: { key: quoteKey(draft), loading: true } } }));
  assert.match(thinking.main, /THINKING …/);
  assert.match(thinking.main, /data-cmd="trade-send"(?![^>]*disabled)/, "and the deal can still be sent");
});

test("trade: send on your turn, save for your turn otherwise", () => {
  const { state } = load("roll");
  const alpha = byName(state, "Alpha");
  const draft = { ...emptyDraft(), targetId: alpha.id, offer: [19], request: [13] };
  const send = renderPhone(state, ui({ tab: "trade", trade: draft }));
  assert.match(send.main, /SEND TO ALPHA/);
  assert.deepEqual(tradeActionOf(draft), { type: "offer_trade", targetId: alpha.id, offer: { cash: 0, properties: [19] }, request: { cash: 0, properties: [13] } });
  const notMine = { ...state, currentPlayerId: alpha.id };
  const plan = renderPhone(notMine, ui({ tab: "trade", trade: draft }));
  assert.match(plan.main, /data-cmd="trade-plan"[^>]*>[\s\S]*?SAVE FOR MY TURN/);
  // A saved plan is a card at the top; the page itself opens empty (never a restored draft).
  const plannedUi = { tab: "trade", trade: emptyDraft(), plan: { ...draft, planned: true } };
  const saved = renderPhone(notMine, ui(plannedUi));
  assert.match(saved.main, /class="tplan"[\s\S]*PLANNED · ALPHA/);
  assert.match(saved.main, /data-cmd="trade-plan-discard"/);
  assert.doesNotMatch(saved.main, /data-cmd="trade-send-plan"/, "not sendable before your turn");
  assert.doesNotMatch(saved.main, /tcard is-on|tchip is-on/, "the page itself is empty");
  assert.match(renderPhone(state, ui(plannedUi)).main, /data-cmd="trade-send-plan"/, "sendable on your turn");
  const waiting = renderPhone(notMine, ui({ plan: { ...draft, planned: true } }));
  assert.match(waiting.main, /PLAN · ALPHA/, "a saved plan shows as one chip while you wait");
});

test("trade rules: cash-for-cash is blocked; deeds that changed hands leave the deal; stale plans clear", () => {
  const { state } = load("roll");
  const me = byName(state, "Robin");
  const sam = byName(state, "Sam");
  assert.equal(tradeValid({ ...emptyDraft(), targetId: sam.id, offerCash: 100, requestCash: 50 }), false);
  assert.equal(tradeBlocker({ ...emptyDraft(), targetId: sam.id, offerCash: 100, requestCash: 50 }), "a deed must move");
  assert.equal(tradeBlocker(emptyDraft()), "pick a partner");
  const samDeed = mineOf(state, sam.id)[0];
  const myDeed = mineOf(state, me.id)[0];
  const draft = { ...emptyDraft(), targetId: sam.id, offer: [myDeed], request: [samDeed], planned: true };
  assert.ok(plannedTrade(state, draft), "a valid plan is offered on your turn");
  const moved = { ...state, properties: { ...state.properties, [myDeed]: { ...state.properties[myDeed], ownerId: sam.id } } };
  const cleaned = cleanDraft(moved, draft, me.id);
  assert.equal(cleaned.dropped, 1);
  assert.deepEqual(cleaned.draft.offer, []);
  assert.equal(plannedTrade(moved, draft), null, "a plan whose deed left your hands is not sendable");
  const gone = { ...state, players: state.players.map((player) => (player.id === sam.id ? { ...player, bankrupt: true } : player)) };
  assert.equal(cleanDraft(gone, draft, me.id).draft.targetId, null, "a bankrupt partner leaves the deal");
});

test("incoming trade: the target reviews two piles and answers; no $0 lines, no filler", () => {
  const { state } = load("trade");
  assert.equal(route(state, { session: {}, tab: "turn" }), "trade");
  const out = tradeScreen(state, ui());
  assert.match(out.main, /OFFERS/);
  assert.match(out.main, /YOU GIVE[\s\S]*YOU GET/);
  assert.match(out.main, /"type":"accept_trade"/);
  assert.match(out.main, /"type":"reject_trade"/);
  assert.doesNotMatch(out.main, />\$0</);
  assert.doesNotMatch(out.main, /btn__detail|Look at both piles/);
  const sent = tradeScreen(load("trade-sent").state, ui());
  assert.match(sent.main, /OFFER SENT/);
  assert.match(sent.main, /CANCEL OFFER/);
});

test("incoming trade: a computer's counter-offer is its reply, with what changed; ACCEPT / CHANGE / WALK AWAY", () => {
  const { state: base } = load("roll");
  const me = byName(base, "Robin");
  const alpha = byName(base, "Alpha");
  const state = structuredClone(base);
  state.properties[39] = { ownerId: alpha.id, houses: 0, mortgaged: false };
  state.properties[28] = { ownerId: me.id, houses: 0, mortgaged: false };
  state.phase = "trade";
  state.trade = { id: "trade_9", proposerId: alpha.id, targetId: me.id, offer: { cash: 0, properties: [39] }, request: { cash: 650, properties: [28] },
    returnPhase: "awaiting_roll", counterTo: "trade_8", counterRound: 1, original: { offer: { cash: 302, properties: [28] }, request: { cash: 0, properties: [39] } } };
  state.log = [...state.log, { id: "c1", text: "Alpha countered Robin's offer" }, { id: "c2", text: "Alpha: I'd do it for $650 + Reservoir." }];
  const out = tradeScreen(state, ui());
  assert.match(out.main, /ALPHA(?:'|&#039;)S COUNTER-OFFER/);
  assert.match(out.main, /I(?:'|&#039;)d do it for \$650 \+ Reservoir/);
  assert.match(out.main, /tchange--bad">\+\$348</);
  assert.match(out.main, /"type":"accept_trade"/);
  assert.match(out.main, /data-cmd="trade-counter-change"/);
  assert.match(out.main, /WALK AWAY/);
  assert.doesNotMatch(out.main, /trade-not-selling/);
  // A computer's own offer for your deed: NOT SELLING <deed>.
  const ask = { ...state, trade: { ...state.trade, counterTo: undefined, original: undefined, offer: { cash: 300, properties: [] }, request: { cash: 0, properties: [28] } } };
  const plain = tradeScreen(ask, ui());
  assert.match(plain.main, /data-cmd="trade-not-selling" data-arg="28"[^>]*>[\s\S]*?NOT SELLING RESERVOIR/);
});

// ---- Round 2 (docs/review/round2/UX-REVIEW-2.md) ----------------------------------------------

test("round 2 · trade: two columns side by side, one live effect line, the value bar and SEND in the footer", () => {
  // The "trade" fixture: Central Road (19) gives Maya her orange set.
  const pending = load("trade").state;
  const state = { ...pending, phase: "awaiting_roll", trade: null, currentPlayerId: pending.viewerId };
  const maya = byName(state, "Maya");
  const out = renderPhone(state, ui({ tab: "trade", trade: { ...emptyDraft(), targetId: maya.id, offer: [19], request: [23] } }));
  assert.match(out.main, /class="tcols"><section class="tside[^"]*" aria-label="YOU GIVE">[\s\S]*aria-label="YOU GET"/);
  assert.equal((out.main.match(/class="tcash"/g) || []).length, 2, "a cash stepper under each column");
  const dock = out.main.slice(out.main.indexOf('class="tdock"'));
  assert.equal((dock.match(/class="teffect[ "]/g) || []).length, 1, "one effect line");
  assert.match(dock, /class="tsum[ "]/, "the value line");
  assert.match(dock, /tset tset--bad[\s\S]*MAYA'S GREEN SET/, "giving Maya her set is red (review U8)");
  assert.match(dock, /data-cmd="trade-send"/);
});

test("round 2 · trade: computers carry a robot badge; the price is only asked on your turn", () => {
  const { state } = load("roll");
  const alpha = byName(state, "Alpha");
  const out = renderPhone(state, ui({ tab: "trade" }));
  assert.match(out.main, new RegExp(`data-arg="${alpha.id}"[^>]*>[\\s\\S]*?tchip__bot`));
  const draft = { ...emptyDraft(), targetId: alpha.id, offer: [19] };
  assert.equal(wantsQuote({ ...state, currentPlayerId: byName(state, "Sam").id }, draft), false);
});

test("round 2 · loan is a segment of the trade page: stepper and chips, repay follows the rate", () => {
  const state = load("active-loan").state;
  const sam = byName(state, "Sam");
  const draft = { direction: "lend", targetId: sam.id, principal: 200, rate: 0.2, dueTurns: 6, collateral: [] };
  const out = loanComposeScreen(state, ui({ tab: "trade", loanDraft: draft }));
  clean(out.main, "loan compose");
  assert.match(out.main, /data-cmd="trade-mode" data-arg="trade"/);
  assert.match(out.main, /data-cmd="loan-amount" data-arg="principal:-50"/);
  assert.match(out.main, /data-cmd="loan-rate" data-arg="0.2" aria-pressed="true"/);
  assert.match(out.main, /data-cmd="loan-due" data-arg="6" aria-pressed="true"/);
  assert.doesNotMatch(out.main, /type="number"|type="range"/, "no free-text fields");
  assert.match(out.main, /BACK <b>\$240<\/b>/);
  // P1-7: lowering the amount lowers the repayment.
  assert.equal(loanRepay({ ...draft, principal: 100 }), 120);
  assert.deepEqual(loanActionOf({ ...draft, principal: 100 }), { type: "offer_loan", targetId: sam.id, direction: "lend", principal: 100, repay: 120, dueTurns: 6, collateral: [] });
  // An old draft with only repay set keeps the nearest rate.
  assert.equal(loanRepay({ principal: 100, repay: 240, dueTurns: 6 }), 150);
  // In debt you can only borrow.
  const locked = loanComposeScreen(state, ui({ loanDraft: { ...draft, direction: "borrow", locked: true } }));
  assert.doesNotMatch(locked.main, /I LEND/);
});

test("round 2 · incoming loan: one sentence and the two amounts, no table repeating it", () => {
  const out = renderPhone(load("loan-offer").state, { session: {}, tab: "turn" });
  assert.match(out.main, /You get \$300 now and owe Alpha \$360 within 6 turns\./);
  assert.doesNotMatch(out.main, /class="rule"/);
  assert.equal(out.banner, "", "no LOAN OFFER pill over the heading");
});

test("round 2 · lobby has no Dice: Choose rule (no dice picker exists)", () => {
  const lobby = renderPhone(load("lobby-rules").state, ui());
  assert.doesNotMatch(lobby.main, /dice-mode|DICE|CHOOSE/);
});

test("round 2 · MORE mid-game: table code, rules, invite, sound and LEAVE TABLE; no friends list", () => {
  const friends = load("friends").friends;
  const more = renderPhone(load("waiting").state, ui({ tab: "friends", friends: { list: friends, sent: {} } }));
  assert.match(more.main, /class="tableline"/);
  assert.match(more.main, /class="rchips"/);
  assert.match(more.main, /data-cmd="leave-ask"[^>]*>[\s\S]*?LEAVE TABLE/);
  assert.match(more.main, /INVITE PEOPLE/);
  assert.doesNotMatch(more.main, /FRIENDS|PRIYA/, "no friends list mid-game");
  // Friends to invite live on the INVITE screen (from MORE, or from the lobby).
  const invite = renderPhone(load("waiting").state, ui({ tab: "friends", inviteOpen: true, friends: { list: friends, sent: {} } }));
  assert.match(invite.main, /PRIYA[\s\S]*data-cmd="friend-invite"/);
  assert.doesNotMatch(invite.main, /AISHA/, "only friends who are online now");
  const lobby = renderPhone(load("lobby").state, ui({ inviteOpen: true, friends: { list: friends, sent: {} } }));
  assert.equal(lobby.screen, "invite");
  assert.equal(lobby.nav, "", "no game tabs in the lobby");
  assert.match(renderPhone(load("lobby").state, ui()).main, /data-cmd="open-invite"/);
});

test("round 2 · game over: places by net worth, and deeds only for someone who has some", () => {
  const defeat = load("defeat").state;
  const out = renderPhone(defeat, ui());
  const winner = defeat.players.find((player) => !player.bankrupt);
  const worth = Object.entries(defeat.properties).filter(([, prop]) => prop.ownerId === winner.id).length;
  assert.ok(worth >= 0);
  assert.doesNotMatch(out.main, new RegExp(`final__how">\\${winner.cash.toLocaleString("en-US")}<`), "net worth, not cash");
  const me = defeat.players.find((player) => player.id === defeat.viewerId);
  if (me.bankrupt) assert.doesNotMatch(out.main, /MY DEEDS/, "a bankrupt player owns nothing to see");
});

test("round 2 · a street sold at auction never shows FOR SALE afterwards", () => {
  const { state } = load("waiting");
  const current = state.players.find((player) => player.id === state.currentPlayerId);
  const free = Number(Object.keys(state.board).find((index) => state.board[index].type === "property" && !state.properties[index]?.ownerId));
  const sold = { ...state, phase: "turn_complete", lastLanding: { playerId: current.id, spaceIndex: free, kind: "purchase" }, properties: { ...state.properties, [free]: { ownerId: byName(state, "Sam").id, houses: 0, mortgaged: false } }, log: [] };
  const out = renderPhone(sold, ui());
  assert.doesNotMatch(out.board.stage, /FOR SALE/);
  assert.match(out.board.stage, /SOLD · SAM/);
});

test("round 2 · strip: a robot badge while the computer plays for someone; offline seats look offline", () => {
  const { state } = load("waiting");
  const sam = byName(state, "Sam");
  const players = state.players.map((player) => (player.id === sam.id ? { ...player, autopilot: true, online: false } : player));
  const html = stripHtml({ ...state, players }, { viewerId: state.viewerId });
  assert.match(html, new RegExp(`plate[^"]*is-offline[^"]*is-auto" role="listitem" data-plate="${sam.id}"`));
  assert.match(html, /class="plate__bot"/);
  assert.doesNotMatch(stripHtml(state, { viewerId: state.viewerId }), /plate__bot/, "a missing autopilot field is false");
});

test("round 2 · BUY says what you keep; the buy card has no hotel row", () => {
  const out = renderPhone(load("buy").state, ui());
  assert.match(out.main, /btn__detail">keep \$/);
  assert.doesNotMatch(out.board.stage, /Hotel/);
});

test("round 2 · final places: winner first, then the last one out; players older than the log stay below", () => {
  const { state } = load("victory");
  const [a, b, c, d] = state.players;
  const finished = {
    ...state,
    winnerId: a.id,
    players: [{ ...a, bankrupt: false }, { ...b, bankrupt: true, cash: 0 }, { ...c, bankrupt: true, cash: 0 }, { ...d, bankrupt: true, cash: 0 }],
    // Only c's bankruptcy is still in the (capped) log.
    log: [{ id: "l1", text: `${c.name} declared bankruptcy; assets transferred to ${a.name}`, at: 0 }],
  };
  const order = finalStandings(finished, [d.name, b.name]).map((entry) => entry.player.name);
  assert.deepEqual(order, [a.name, c.name, b.name, d.name], "c went out last; b after d (seen on this phone)");
  assert.equal(finalStandings(finished)[1].player.name, c.name, "without the phone's memory the logged one still ranks above the older ones");
});

// ---- Phone-hosted games: home (NEW GAME, ACTIVE GAMES), create form, lobby code card ----------

const demoGames = JSON.parse(readFileSync(join(here, "..", "phone", "demo-games.json"), "utf8")).games;
const homeUi = (extra = {}) => ({ session: null, join: { code: "", name: "", tokenKind: null }, games: { list: demoGames, error: "" }, ...extra });

test("home: NEW GAME first, then ACTIVE GAMES with one action per game, and join with code", () => {
  const out = renderPhone(null, homeUi());
  assert.equal(out.screen, "join");
  assert.equal(joinMode({ code: "" }), "home");
  clean(out.main, "home");
  assert.ok(out.main.indexOf('data-cmd="new-game"') < out.main.indexOf("ACTIVE GAMES"), "NEW GAME comes first");
  assert.equal((out.main.match(/<li class="grow/g) || []).length, demoGames.length);
  const actionOf = (code) => out.main.match(new RegExp(`data-cmd="game-open" data-arg="${code}"[^>]*>([A-Z]+)<`))?.[1];
  assert.equal(actionOf("TLUER"), "REJOIN", "your own game");
  assert.equal(actionOf("FP85S"), "JOIN", "a lobby with open seats");
  assert.equal(actionOf("KQ7RT"), "JOIN", "a started game with an offline seat opens WHO ARE YOU?");
  assert.equal(actionOf("X5PAE"), "WATCH");
  assert.match(out.main, /LOBBY · 3\/6/);
  assert.match(out.main, /TURN 14/);
  assert.equal((out.main.match(/class="gpc"/g) || []).length, demoGames.filter((game) => game.tableOnline).length, "PC icon only when a table screen is on");
  assert.match(out.main, /class="grow is-mine"/);
  assert.match(out.main, /id="p-code"/, "join with code stays, small");
  assert.ok(visibleWords(out.main).length <= 70, `home: ${visibleWords(out.main).length} words`);
});

test("home: empty list says so; BACK TO TABLE only when the list doesn't already show that game", () => {
  const empty = renderPhone(null, homeUi({ games: { list: [], error: "" } }));
  assert.match(empty.main, /No games yet · start one/);
  assert.match(empty.main, /data-cmd="new-game"/);
  const listed = renderPhone(null, homeUi({ lastTable: { code: "TLUER" } }));
  assert.doesNotMatch(listed.main, /BACK TO TABLE/, "the list covers it");
  const gone = renderPhone(null, homeUi({ lastTable: { code: "ZZZZZ" } }));
  assert.match(gone.main, /BACK TO TABLE ZZZZZ/);
  const oldServer = renderPhone(null, homeUi({ games: { list: null, error: "Not available on this server" }, lastTable: { code: "TLUER" } }));
  assert.match(oldServer.main, /BACK TO TABLE TLUER/, "no list: the remembered table is still offered");
});

test("game actions: rejoin, join a lobby, take a seat in a started game, or watch", () => {
  const [mine, lobby, seats, full] = demoGames;
  assert.deepEqual([mine, lobby, seats, full].map((game) => gameAction(game).kind), ["rejoin", "join", "seats", "watch"]);
  assert.equal(gameAction({ ...lobby, openSeats: 0 }).kind, "watch", "a full lobby can only be watched");
  assert.equal(gameAction({ ...seats, status: "finished" }).kind, "watch");
  // Opening a started game with an offline seat lands on WHO ARE YOU?.
  assert.equal(joinMode({ code: seats.code, summary: seats }), "who");
  assert.equal(joinMode({ code: lobby.code, summary: lobby }), "new");
});

test("new game: the name and token form without a code; every token free; CREATE GAME", () => {
  const out = renderPhone(null, { session: null, join: { mode: "create", code: "", name: "Ana", tokenKind: "rocket", summary: demoGames[0] }, games: { list: demoGames } });
  assert.equal(joinMode({ mode: "create" }), "create");
  assert.match(out.main, /NEW GAME/);
  assert.match(out.main, /data-cmd="create-game"[^>]*>[\s\S]*?CREATE GAME/);
  assert.doesNotMatch(out.main, /id="p-code"/);
  assert.doesNotMatch(out.main, /data-token="[a-z]+"[^>]*disabled/, "nobody has a token yet in a new game");
  assert.match(out.main, /data-cmd="home"/, "a way back");
});

test("host lobby without a PC: the code big, INVITE and COPY, and how to show it on a PC", () => {
  const lobby = load("lobby").state;
  const noPc = renderPhone(lobby, ui({ tableOnline: false }));
  assert.match(noPc.main, new RegExp(`class="codecard"[\\s\\S]*?>${lobby.code}<`));
  assert.match(noPc.main, /data-cmd="share"[^>]*>INVITE/);
  assert.match(noPc.main, /data-cmd="copy"/);
  assert.match(noPc.main, /On a PC: open it and pick this game/);
  assert.doesNotMatch(noPc.main, /class="qr/, "no QR on the host's own phone");
  const withPc = renderPhone(lobby, ui({ tableOnline: true }));
  assert.doesNotMatch(withPc.main, /On a PC:/);
  const guest = renderPhone({ ...lobby, hostId: "someone-else", viewerIsHost: false }, ui({ tableOnline: false }));
  assert.doesNotMatch(guest.main, /On a PC:/, "only the host is told");
  assert.match(guest.main, /class="codecard"/, "everyone can invite");
});

// ---- Responsive layouts (layout.js): phone, tablet, desk ---------------------------------------

test("layout: phone under 700 px; tablet portrait; desk (two panes) for landscape from 700 px", () => {
  const cases = [
    [360, 760, "phone"], [390, 844, "phone"], [430, 932, "phone"], [699, 1000, "phone"],
    [768, 1024, "tablet"], [820, 1180, "tablet"], [1024, 1366, "tablet"],
    [1024, 768, "desk"], [1180, 820, "desk"], [1366, 768, "desk"], [1440, 900, "desk"], [1920, 1080, "desk"], [844, 390, "desk"],
  ];
  for (const [width, height, layout] of cases) assert.equal(layoutFor(width, height), layout, `${width}×${height}`);
  assert.deepEqual(LAYOUTS, ["phone", "tablet", "desk"]);
});

test("layout: compact board on small screens, the full board from 560 px; hop zoom eases on big boards", () => {
  assert.equal(boardModeFor(370), "compact");
  assert.equal(boardModeFor(559), "compact");
  assert.equal(boardModeFor(560), "full");
  assert.equal(boardModeFor(868), "full");
  assert.ok(hopZoom(370) > hopZoom(560) && hopZoom(560) > hopZoom(868), "the camera stays on the token at every size, closer on small boards");
  assert.ok(hopZoom(868) > 1);
});

test("desk: the board stays on the left in a game whatever the tab; not in the lobby or the ways in", () => {
  const roll = load("roll").state;
  assert.equal(deskGameFrame("roll", roll), true);
  assert.equal(deskGameFrame("trade", roll), true);
  assert.equal(deskGameFrame("property", roll), true);
  assert.equal(deskGameFrame("result", load("victory").state), true);
  assert.equal(deskGameFrame("lobby", load("lobby").state), false);
  assert.equal(deskGameFrame("join", null), false);
  // Tab pages carry an ambient board: the BOARD tab's stage and status, without its buttons.
  const deeds = renderPhone(roll, ui({ tab: "property" }));
  assert.equal(deeds.board, null);
  assert.match(deeds.ambient.status, />YOUR TURN</);
  assert.ok(Array.isArray(deeds.ambient.heat), "the risk wash stays on the board");
  const waiting = renderPhone(load("waiting").state, ui({ tab: "friends" }));
  assert.match(waiting.ambient.status, /&#039;S TURN/);
  assert.ok(waiting.ambient.stage.length > 20, "the stage still shows what just happened");
  const debt = renderPhone(load("debt").state, ui());
  assert.match(debt.ambient.status, /RAISE CASH/);
  const over = renderPhone(load("victory").state, ui());
  assert.match(over.ambient.status, /WINS/);
  assert.equal(renderPhone(load("lobby").state, ui()).ambient, null);
  assert.equal(ambientBoard(roll, { tab: "turn" }), null, "no session, no board");
});

test("at a glance (review 4 Nov #8): you, a loan due, a danger chip and the last move fill the wait, no sentences", () => {
  const roll = renderPhone(load("roll").state, ui());
  assert.match(roll.main, /class="glance"/, "before you roll");
  assert.match(roll.main, /glance__me[\s\S]*glance__cash">\$[\d,]+<[\s\S]*DEEDS?/, "your cash and deeds");
  const waiting = renderPhone(load("waiting").state, ui());
  assert.match(waiting.main, /class="glance"[\s\S]*glance__last[\s\S]*LAST/, "while you wait: the last move");
  assert.doesNotMatch(waiting.main, /looks safe|chance of|SINCE YOUR TURN|<ul/, "no probability sentence, no history list");
  const loan = renderPhone(load("active-loan").state, ui());
  if (/glance/.test(loan.main)) assert.match(loan.main, /You owe \$\d/, "a loan you owe shows");
  const auction = renderPhone(load("auction").state, ui());
  assert.doesNotMatch(auction.main, /class="glance"/, "an auction keeps its bid tiles");
});

test("desk panel: the right-hand panel stays within the one-glance budgets", () => {
  // Panel = tabs + player rows + status line + the tab's content (the board is on the left).
  const panelWords = (out) => visibleWords([out.nav, out.header, (out.board || out.ambient)?.status || "", out.banner, out.main].join(" ")).length;
  const budgets = { roll: 36, waiting: 36, buy: 32, auction: 42, debt: 45, trade: 50 }; // roll / waiting: the at-a-glance panel
  for (const [name, budget] of Object.entries(budgets)) {
    const words = panelWords(renderPhone(load(name).state, ui()));
    assert.ok(words <= budget, `${name}: ${words} words in the panel > ${budget}`);
  }
  const roll = load("roll").state;
  const alpha = byName(roll, "Alpha");
  const trade = panelWords(renderPhone(roll, ui({ tab: "trade", trade: { ...emptyDraft(), targetId: alpha.id, offer: [19], request: [13] } })));
  assert.ok(trade <= 95, `trade panel: ${trade} words`); // set progress and a verdict, on purpose
});

// ---- Every screen (scenarios.js): each scene renders, without leaks ----------------------------

test("scenes: every screen in the inventory renders from its fixture without leaks", () => {
  assert.equal(new Set(SCENES.map((scene) => scene.id)).size, SCENES.length, "ids are unique");
  const friends = load("friends").friends;
  const fixtures = Object.fromEntries(names.map((name) => [name, load(name)]));
  for (const scene of SCENES) {
    assert.ok(SECTIONS.includes(scene.section), `${scene.id}: section`);
    assert.ok(scene.title, `${scene.id}: title`);
    if (scene.boot || scene.replay !== undefined) continue;
    const { state, ui: extra } = buildScene(scene, fixtures);
    const view = {
      session: scene.session === false ? null : { code: state.code },
      tab: scene.tab || "turn",
      trade: extra.trade || emptyDraft(),
      plan: extra.plan || null,
      loanDraft: extra.loanDraft || null,
      join: { name: "", tokenKind: "rocket", ...(extra.join || {}) },
      games: extra.games === "demo" ? { list: demoGames, error: "" } : extra.games,
      friends: { list: extra.friends === "fixture" ? friends : [], sent: {} },
      inviteOpen: Boolean(extra.inviteOpen),
      invitedView: Boolean(extra.invitedView),
      tableOnline: extra.tableOnline,
      lastTable: extra.lastTable || null,
      muted: Boolean(extra.muted),
      tradeInfo: Boolean(extra.tradeInfo),
      inviteLink: "https://example.test/j/NXGNT",
      now: Date.UTC(2026, 10, 2),
    };
    const out = renderPhone(scene.session === false ? null : state, view);
    for (const part of ["header", "banner", "main", "nav"]) clean(out[part], `${scene.id}/${part}`);
    if (out.board) clean(`${out.board.stage}${out.board.status}`, `${scene.id}/board`);
    assert.ok(out.main.length > 40 || out.board, `${scene.id}: shows something`);
  }
  for (const section of SECTIONS) assert.ok(SCENES.some((scene) => scene.section === section), `${section} has screens`);
});

// ---- Sound, music and card reading time (audio.js) ---------------------------------------------

test("cards stay readable: max(3.5 s, 2 s + 55 ms a character), at most 9 s; a card to confirm waits for you", () => {
  assert.equal(cardReadMs("Off to jail"), 3500, "short cards get 3.5 s");
  assert.equal(cardReadMs("x".repeat(40)), 4200);
  assert.equal(cardReadMs("x".repeat(100)), 7500);
  assert.equal(cardReadMs("x".repeat(400)), 9000, "never more than 9 s");
  assert.equal(cardReadMs(""), 3500);
  const card = { type: "card", deck: "chance", text: "Strike called off: go to the nearest railroad; if owned, pay double rent", playerId: "p1" };
  assert.equal(cardHoldMs(card, { phase: "turn_complete" }), cardReadMs(card.text));
  assert.equal(cardHoldMs(card, { phase: "card_confirmation", pendingCard: { playerId: "p1" } }), 0, "it stays on the stage until confirmed");
});

test("sounds: your own events at full volume; other players' quieter or silent", () => {
  const me = "me";
  const other = "other";
  const hit = (event, extra) => sfxForEvent(event, me, extra);
  assert.deepEqual(hit({ type: "dice", playerId: me }), { name: "dice", opts: {}, gain: 1 });
  assert.ok(hit({ type: "dice", playerId: other }).gain < 0.5, "someone else's dice are quiet");
  assert.equal(hit({ type: "move", playerId: me }, { step: { last: false } }).name, "step");
  assert.equal(hit({ type: "move", playerId: me }, { step: { last: true } }).name, "land");
  assert.equal(hit({ type: "move", playerId: other }, { step: { last: true } }), null, "other tokens hop silently");
  assert.deepEqual(hit({ type: "pay", from: null, to: me, amount: 200, reason: "go" }), { name: "go", opts: { amount: 200 }, gain: 1 });
  const rent = hit({ type: "pay", from: me, to: other, amount: 26 });
  assert.equal(rent.name, "coins");
  assert.equal(rent.opts.amount, 26, "coins scale with the amount");
  assert.equal(rent.opts.out, true);
  assert.equal(hit({ type: "pay", from: other, to: me, amount: 80 }).opts.out, false, "money in");
  assert.equal(hit({ type: "pay", from: other, to: "third", amount: 80 }), null, "other people's money is silent");
  assert.equal(hit({ type: "buy", playerId: me, price: 200 }).gain, 1);
  assert.ok(hit({ type: "buy", playerId: other, price: 200 }).gain < 0.5);
  assert.equal(hit({ type: "build", playerId: me, houses: 5 }).name, "hotel");
  assert.equal(hit({ type: "build", playerId: me, houses: 2 }).name, "build");
  assert.equal(hit({ type: "build", playerId: other, houses: 2 }), null);
  assert.equal(hit({ type: "card", playerId: me }).name, "card");
  assert.equal(hit({ type: "jail", playerId: me }).gain, 1);
  assert.equal(hit({ type: "bankrupt", playerId: other, creditorId: me }).gain, 1, "deeds coming to you");
  assert.ok(hit({ type: "bankrupt", playerId: other, creditorId: "third" }).gain < 0.5);
  assert.equal(hit({ type: "win", playerId: me }).gain, 1);
  assert.ok(hit({ type: "win", playerId: other }).gain < 1, "only the winner's phone plays the win at full volume");
  assert.ok(sfxForEvent({ type: "dice", playerId: me }, null).gain < 0.5, "spectators hear everything quietly");
});

test("sounds: offers to you, deals you are in, loans, an auction you won; your own money buttons", () => {
  const me = "me";
  assert.equal(sfxForCue({ kind: "trade", playerId: "sam", otherId: me }, me).name, "offer");
  assert.equal(sfxForCue({ kind: "trade", result: "accepted", playerId: "sam", otherId: me }, me).name, "deal");
  assert.equal(sfxForCue({ kind: "trade", result: "declined", playerId: "sam", otherId: me }, me).name, "declined");
  assert.equal(sfxForCue({ kind: "trade", result: "declined", playerId: me, otherId: "sam" }, me), null, "no sound for your own DECLINE");
  assert.equal(sfxForCue({ kind: "trade", playerId: "sam", otherId: "maya" }, me), null, "other people's offers are silent");
  assert.equal(sfxForCue({ kind: "loan", playerId: "sam", otherId: me, amount: 200 }, me).name, "loan");
  assert.equal(sfxForCue({ kind: "auction", playerId: me }, me).name, "gavel");
  assert.equal(sfxForCue({ kind: "auction", playerId: "sam" }, me), null);
  assert.equal(sfxForAction({ type: "mortgage", spaceIndex: 3 }).name, "mortgage");
  assert.equal(sfxForAction({ type: "bid", amount: 70 }).name, "bid");
  assert.equal(sfxForAction({ type: "end_turn" }), null);
});

test("music: off by default on a phone; moods follow the game", () => {
  assert.equal(DEFAULT_AUDIO.music, false);
  assert.equal(moodFor(load("lobby").state), "lobby");
  assert.equal(moodFor(load("roll").state), "play");
  assert.equal(moodFor(load("auction").state), "tension");
  assert.equal(moodFor(load("debt").state), "tension");
  const rent = load("rent").state;
  assert.equal(moodFor({ ...rent, lastLanding: { ...rent.lastLanding, amount: 950 } }), "tension", "a big rent");
  assert.equal(moodFor(load("victory").state), "victory");
  assert.equal(moodFor(null), null);
});

test("audio settings: defaults, clamped volume, saved and read back", () => {
  assert.deepEqual(normaliseAudio(null), { music: false, effects: true, volume: 0.8 });
  assert.deepEqual(normaliseAudio({ music: true, effects: "yes", volume: 7 }), { music: true, effects: true, volume: 1 });
  assert.equal(normaliseAudio({ volume: -1 }).volume, 0);
  const memory = new Map();
  const store = { get: (key) => memory.get(key) ?? null, set: (key, value) => memory.set(key, value) };
  assert.deepEqual(loadAudio(store), DEFAULT_AUDIO);
  saveAudio(store, { music: true, effects: false, volume: 0.35 });
  assert.deepEqual(loadAudio(store), { music: true, effects: false, volume: 0.35 });
  assert.deepEqual(loadAudio({ get: () => { throw new Error("blocked"); } }), DEFAULT_AUDIO, "private mode falls back to defaults");
});

test("SETTINGS: sound (SOUND, MUSIC, EFFECTS, one volume), this device, and the host's computer speed", () => {
  const html = settingsHtml({ audio: { music: true, effects: false, volume: 0.5 }, muted: false, prefs: { vibration: false, speed: "fast", follow: true, motion: "auto" }, host: true, computerSpeed: "normal" });
  assert.match(html, /data-cmd="audio" data-arg="muted" aria-pressed="true">SOUND <b>ON/);
  assert.match(html, /data-arg="music" aria-pressed="true">MUSIC <b>ON/);
  assert.match(html, /data-arg="effects" aria-pressed="false">EFFECTS <b>OFF/);
  assert.equal((html.match(/data-audio-volume/g) || []).length, 1, "one volume slider");
  assert.match(html, /value="50" data-audio-volume/);
  assert.match(html, /data-arg="vibration:false" aria-pressed="true"/);
  assert.match(html, /data-arg="speed:fast" aria-pressed="true"/);
  assert.match(html, /data-arg="follow:true" aria-pressed="true"/);
  assert.match(html, /data-arg="motion:auto" aria-pressed="true"/);
  assert.match(html, /data-cmd="computer-speed" data-arg="speed:normal" aria-pressed="true"/);
  assert.doesNotMatch(settingsHtml({ host: false, computerSpeed: "normal" }), /computer-speed/, "only the host sets the computers' speed");
  assert.doesNotMatch(settingsHtml({ host: true, computerSpeed: null }), /computer-speed/, "only party rooms have it");
  assert.match(audioControlsHtml(undefined, false), /MUSIC <b>OFF/, "music off unless turned on");
});

test("SETTINGS and quitting: a gear on home and lobby; MORE has SETTINGS, LEAVE, and END GAME for the host", () => {
  const home = renderPhone(null, { session: null, join: { code: "" }, games: { list: [] } });
  assert.match(home.header, /class="bar__gear" data-cmd="settings"/);
  const lobby = renderPhone(load("lobby").state, ui());
  assert.match(lobby.header, /data-cmd="settings"/);
  assert.match(lobby.main, /data-cmd="leave-ask"[^>]*>LEAVE</, "a clear way out of the lobby");
  const waiting = load("waiting").state;
  const host = renderPhone(waiting, ui({ tab: "friends" }));
  assert.match(host.main, /data-cmd="settings"/);
  assert.match(host.main, /data-cmd="leave-ask"[^>]*>[\s\S]*?LEAVE TABLE/);
  assert.match(host.main, /data-cmd="end-game-ask"[^>]*>[\s\S]*?END GAME FOR EVERYONE/);
  assert.doesNotMatch(host.main, /data-audio-volume/, "sound lives in SETTINGS now");
  const guest = renderPhone({ ...waiting, hostId: byName(waiting, "Sam").id }, ui({ tab: "friends" }));
  assert.doesNotMatch(guest.main, /END GAME/, "only the host can end the game");
  assert.match(guest.main, /LEAVE TABLE/);
});

test("END GAME: the leader is the richest player by net worth, loans included", () => {
  const state = load("active-loan").state;
  const leader = leaderNow(state, netWorth);
  const worths = state.players.filter((player) => !player.bankrupt).map((player) => worthWithLoans(state, player.id, netWorth));
  assert.equal(leader.worth, Math.max(...worths));
  const me = state.players.find((player) => player.id === state.viewerId);
  // Robin owes Alpha $480 and is owed $240 by Sam: net −$240 against plain net worth.
  assert.equal(worthWithLoans(state, me.id, netWorth), netWorth(state, me.id) - 480 + 240);
});

test("prefs: defaults, saved and read back, pace and reduced motion", () => {
  assert.deepEqual(normalisePrefs(null), { vibration: true, speed: "normal", follow: true, motion: "auto" });
  assert.deepEqual(normalisePrefs({ vibration: false, speed: "turbo", follow: "no", motion: "full" }), { vibration: false, speed: "normal", follow: true, motion: "full" });
  const memory = new Map();
  const store = { get: (key) => memory.get(key) ?? null, set: (key, value) => memory.set(key, value) };
  savePrefs(store, { vibration: false, speed: "fast", follow: false, motion: "reduce" });
  assert.deepEqual(loadPrefs(store), { vibration: false, speed: "fast", follow: false, motion: "reduce" });
  assert.deepEqual(loadPrefs({ get: () => { throw new Error("blocked"); } }), DEFAULT_PREFS);
  assert.equal(paceOf({ speed: "fast" }), 0.55);
  assert.equal(paceOf({ speed: "normal" }), 1);
  assert.equal(reducedMotion({ motion: "auto" }, true), true, "follows the system");
  assert.equal(reducedMotion({ motion: "auto" }, false), false);
  assert.equal(reducedMotion({ motion: "reduce" }, false), true, "override on");
  assert.equal(reducedMotion({ motion: "full" }, true), false, "override off");
  // Fast animations never shorten card reading.
  assert.equal(cardReadMs("x".repeat(60)), 5300);
});

// ---- Trade with all the information; tap any deed for details (impact.js) ----------------------

test("impact: cash, net worth and rent before → after for both sides, sets completed, plain verdicts", () => {
  const state = load("trade").state; // Maya offers Museum Road + $200 for your Central Road
  const impact = tradeImpactLocal(state, state.trade, state.viewerId);
  const maya = impact.sides.find((side) => side.name === "Maya");
  const me = impact.sides.find((side) => side.you);
  assert.ok(maya && me, "both sides");
  assert.deepEqual(maya.setsGained, ["orange"], "Central Road completes Maya's orange set");
  assert.equal(me.cash[1] - me.cash[0], 200, "you get $200");
  assert.equal(maya.cash[1] - maya.cash[0], -200);
  assert.match(impact.verdicts[0].text, /^Completes Maya's GREEN set$/);
  assert.equal(impact.verdicts[0].tone, "bad", "bad news for you");
  assert.ok(maya.rent[1] > maya.rent[0], "a full set doubles Maya's rents");
  const after = stateAfter(state, state.trade);
  for (const index of state.trade.offer.properties) assert.equal(after.properties[index].ownerId, state.trade.targetId);
  for (const index of state.trade.request.properties) assert.equal(after.properties[index].ownerId, state.trade.proposerId);
  assert.equal(tradeImpact(state, state.trade, state.viewerId).verdicts.length, impact.verdicts.length, "the shared version when present, else this one");
});

test("impact: the phone uses the shared insights.tradeImpact (rent by set, blocked deeds) in its own shape", () => {
  const state = load("trade").state;
  const shared = insights.tradeImpact(state, state.trade, { viewerId: state.viewerId });
  const impact = tradeImpact(state, state.trade, state.viewerId);
  const maya = impact.sides.find((side) => side.name === "Maya");
  assert.deepEqual(maya.worth, [shared.proposer.worthBefore, shared.proposer.worthAfter], "worth straight from the shared impact");
  assert.equal(maya.rent, null, "rent comes as per-set changes");
  assert.ok(maya.rentChanges.some((change) => change.key === "orange" && change.after > change.before), "ORANGE rent goes up for Maya");
  assert.deepEqual(impact.verdicts[0], { text: "Completes Maya's GREEN set", tone: "bad", group: "orange" });
  const html = impactHtml(state, impact);
  assert.ok(html.includes('GREEN rent</span><span class="imp__val imp__val--up">$14 → <b>$32</b>'));
  assert.ok(html.includes('BROWN rent</span><span class="imp__val imp__val--down">$18 → <b>—</b>'), "no rent shows as a dash");
  // Utilities are dice multiples, not dollars.
  const util = impactHtml(state, { verdicts: [], sides: [{ ...maya, rentChanges: [{ key: "utility", label: "UTILITIES", before: 4, after: 10 }], setsGained: [], setsLost: [] }] });
  assert.ok(util.includes("4× → <b>10×</b>"));
});

test("impact: the map outlines what you give (red) and get (green); a deed sheet lists who owns its set", () => {
  const state = load("trade").state;
  const map = miniMapHtml(state, { give: [19], get: [23] });
  assert.equal((map.match(/mm__cell[^"]*is-give/g) || []).length, 1);
  assert.equal((map.match(/mm__cell[^"]*is-get/g) || []).length, 1);
  assert.equal((map.match(/class="mm__cell/g) || []).length, 40, "all 40 squares");
  const owners = setOwnersHtml(state, 19, state.viewerId);
  assert.equal((owners.match(/<li class="setown/g) || []).length, 3, "the orange set has three streets");
  assert.match(owners, /class="setown is-this"/);
  assert.match(owners, />You</);
  assert.match(setOwnersHtml(state, 5, state.viewerId), /North Station|Railroad/, "railroads list the other railroads");
});

test("trade with all the information: groups with set progress, an i on every deed, the deal's impact", () => {
  const { state } = load("roll");
  const alpha = byName(state, "Alpha");
  const draft = { ...emptyDraft(), targetId: alpha.id, offer: [19], request: [13, 14] };
  const folded = renderPhone(state, ui({ tab: "trade", trade: draft }));
  assert.match(folded.main, /class="tgroup"[^>]*><i><\/i>GREEN<b>1\/3<\/b>/, "your holdings by set, with progress");
  assert.match(folded.main, /class="tgroup"[^>]*><i><\/i>YELLOW<b>2\/3<\/b>/, "theirs too");
  assert.equal((folded.main.match(/class="tcard__info" data-sheet="\d+"/g) || []).length, 2 + 5, "an i on every deed in both columns");
  assert.match(folded.main, /class="teffect [^"]*teffect--fold" data-cmd="trade-info" aria-expanded="false">[\s\S]*?WHY ▾/, "folded on the phone: the footer verdict opens it");
  assert.doesNotMatch(folded.main, /class="tinfo/, "no extra row while folded");
  assert.doesNotMatch(folded.main, /class="impact"/);
  const open = renderPhone(state, ui({ tab: "trade", trade: draft, tradeInfo: true }));
  assert.match(open.main, /class="impact"[\s\S]*class="minimap"/);
  const wide = renderPhone(state, ui({ tab: "trade", trade: draft, layout: "desk" }));
  assert.match(wide.main, /class="impact"/, "always open on tablet and PC");
  assert.doesNotMatch(wide.main, /data-cmd="trade-info"/);
  const deskEmpty = renderPhone(state, ui({ tab: "trade", trade: { ...emptyDraft(), targetId: alpha.id }, layout: "desk" }));
  assert.match(deskEmpty.main, /class="tinfo tinfo--empty"/, "on PC the third column is there before anything is picked (no jump)");
  assert.doesNotMatch(renderPhone(state, ui({ tab: "trade", trade: { ...emptyDraft(), targetId: alpha.id } })).main, /class="tinfo/, "not on the phone");
  const incoming = renderPhone(load("trade").state, ui({ layout: "tablet" }));
  assert.match(incoming.main, /class="verdicts"[\s\S]*Completes Maya&#039;s GREEN set/, "verdicts first");
  assert.match(incoming.main, /class="tcard tcard--static" data-sheet="19"/, "cards in the offer open their details");
  assert.match(incoming.main, /class="impact"[\s\S]*class="minimap"/);
});

test("tap any deed: board stage cards and cue chips carry the deed", () => {
  const buy = renderPhone(load("buy").state, ui());
  assert.match(buy.board.stage, /class="pdeed" data-sheet="\d+"/);
  assert.match(renderPhone(load("auction").state, ui()).board.stage, /class="pauc" data-sheet="\d+"/);
  const { state } = load("roll");
  assert.match(cueHtml({ kind: "auction", icon: "hammer", text: "SOLD", tone: "neg", amount: -100, spaceIndex: 19 }, state), /data-sheet="19"/);
  assert.doesNotMatch(cueHtml({ kind: "trade", icon: "deal", text: "Sam accepted", tone: "pos", amount: null, spaceIndex: null }, state), /data-sheet/);
  assert.match(renderPhone(state, ui({ tab: "property" })).main, /class="dtile[^"]*" data-sheet="19"/, "MY DEEDS tiles");
});

test("board centre: an empty stage clears what an animation left there (review B7)", async () => {
  const { PhoneBoard } = await import("../phone/board-view.js");
  const { Board } = await import("../shared/board.js");
  const slot = { dataset: { html: "" }, innerHTML: "" };
  const fake = { stageHtml: "", board: { slot, setStage(html) { Board.prototype.setSlot.call(this, html); } } };
  PhoneBoard.prototype.setStage.call(fake, "");
  // A computer's dice played straight into the slot, then the turn passed (nothing on the stage).
  slot.innerHTML = '<div class="pev">ALPHA ROLLS 6</div>';
  PhoneBoard.prototype.dirtyStage.call(fake);
  PhoneBoard.prototype.setStage.call(fake, "");
  assert.equal(slot.innerHTML, "", "the old ALPHA ROLLS 6 is gone");
});

test("opening order on the phone: only the tied players are told to roll again (review B3)", async () => {
  const { orderStatusText } = await import("../phone/stage.js");
  const P = (id, name) => ({ id, name, cash: 1500, position: 0, inJail: false, bankrupt: false, tokenKind: { m: "rocket", b: "umbrella", p: "bicycle" }[id] });
  const state = {
    status: "playing", phase: "determining_order", players: [P("m", "Maya"), P("b", "Blue Whale"), P("p", "Priya")],
    openingOrder: { round: 2, pendingIds: ["m"], rollingIds: ["m", "b"], ties: [{ round: 1, total: 2, playerIds: ["m", "b"] }], order: [], entries: [
      { playerId: "m", rolls: [{ round: 1, total: 2 }] }, { playerId: "b", rolls: [{ round: 1, total: 2 }, { round: 2, total: 3 }] }, { playerId: "p", rolls: [{ round: 1, total: 7 }] },
    ] },
  };
  assert.equal(orderStatusText(state, "m"), "TIED ON 2 · ROLL AGAIN");
  assert.equal(orderStatusText(state, "b"), "TIE-BREAK · WAITING");
  assert.equal(orderStatusText(state, "p"), "TIE-BREAK · MAYA & BLUE WHALE");
  assert.equal(orderStatusText({ ...state, openingOrder: { ...state.openingOrder, round: 1, rollingIds: [], ties: [] } }, "p"), "ROLL FOR ORDER · HIGHEST STARTS");
});

test("eight players: the strip goes to two rows of four; the lobby takes eight", () => {
  const { state } = load("roll");
  const kinds = ["rocket", "teapot", "bicycle", "lighthouse", "key", "umbrella", "anchor", "kite"];
  const eight = { ...state, players: kinds.map((tokenKind, index) => ({ ...state.players[index % state.players.length], id: index === 0 ? state.viewerId : `p${index}`, name: `P${index}`, tokenKind })) };
  const strip = stripHtml(eight, { viewerId: state.viewerId });
  assert.match(strip, /strip--n8/);
  assert.equal((strip.match(/role="listitem"/g) || []).length, 8);
  const lobby = load("lobby").state;
  const host = { ...lobby, hostId: lobby.viewerId };
  const seven = { ...host, players: kinds.slice(0, 7).map((tokenKind, index) => ({ ...lobby.players[0], id: index === 0 ? lobby.viewerId : `p${index}`, name: `P${index}`, tokenKind })) };
  assert.doesNotMatch(renderPhone(seven, ui()).main, /data-cmd="add-ai" disabled/, "seven: room for one more");
  const full = { ...seven, players: [...seven.players, { ...seven.players[1], id: "p7", tokenKind: "kite" }] };
  assert.match(renderPhone(full, ui()).main, /data-cmd="add-ai" disabled/, "eight: full");
});

test("bankruptcy at any time (0.10.1): MORE has DECLARE BANKRUPTCY in play, not in the lobby or once out", () => {
  const { state } = load("roll");
  const more = renderPhone(state, ui({ tab: "friends" }));
  assert.match(more.main, /data-cmd="bankrupt"[^>]*>[^]*?DECLARE BANKRUPTCY/);
  const lobby = renderPhone(load("lobby").state, ui({ tab: "friends" }));
  assert.match(renderPhone(load("waiting").state, ui({ tab: "friends" })).main, /DECLARE BANKRUPTCY/, "during another player's turn too");
  assert.doesNotMatch(lobby.main, /DECLARE BANKRUPTCY/);
  const out = { ...state, players: state.players.map((player) => (player.id === state.viewerId ? { ...player, bankrupt: true } : player)) };
  assert.doesNotMatch(renderPhone(out, ui({ tab: "friends" })).main, /DECLARE BANKRUPTCY/);
});

// ---- 0.10.1: tap any property for what you can do with it (the deed sheet) ----

test("deed sheet: your mortgaged deed → UNMORTGAGE; built → SELL; theirs → ASK TO BUY…; greyed ones say why", async () => {
  const { deedActionButtons } = await import("../phone/deedsheet.js");
  const base = structuredClone(JSON.parse(readFileSync(join(here, "..", "fixtures", "roll.json"), "utf8")).state);
  const me = base.players.find((player) => player.name === "Robin");
  const alpha = base.players.find((player) => player.name === "Alpha");
  const at = (patch, extra = {}) => {
    const state = structuredClone(base);
    for (const [index, prop] of Object.entries(patch)) state.properties[index] = { houses: 0, mortgaged: false, ...prop };
    return { ...state, viewerId: me.id, currentPlayerId: me.id, phase: "awaiting_roll", trade: null, ...extra };
  };
  const mortgaged = at({ 37: { ownerId: me.id }, 39: { ownerId: me.id, mortgaged: true } });
  const html = deedActionButtons(mortgaged, me.id, 39).join("");
  assert.match(html, /data-act='\{"type":"unmortgage","spaceIndex":39\}' data-deed-action="unmortgage"><span class="btn__label">UNMORTGAGE · −\$220 with interest/);
  assert.match(html, /data-deed-action="build" disabled>[\s\S]*?<p class="sheet__why" data-why="build">Unmortgage the red set first<\/p>/);
  assert.match(html, /data-cmd="deed-trade" data-arg="39" data-deed-action="trade">/);
  const built = deedActionButtons(at({ 37: { ownerId: me.id, houses: 1 }, 39: { ownerId: me.id, houses: 2 } }), me.id, 39).join("");
  assert.match(built, /data-act='\{"type":"sell_building","spaceIndex":39\}'[^>]*>[\s\S]*?SELL A HOUSE · \+\$100/);
  assert.match(built, /data-why="mortgage">Sell the buildings on the red set first/);
  const theirs = deedActionButtons(at({}), me.id, 13).join("");
  assert.equal(at({}).properties[13].ownerId, alpha.id);
  assert.match(theirs, /data-cmd="deed-ask" data-arg="13" data-deed-action="ask"><span class="btn__label">ASK TO BUY… · Alpha/);
  // Off your turn: management waits (greyed with why), but ASK stays live: the trade page saves it for your turn.
  const later = at({}, { currentPlayerId: alpha.id });
  assert.doesNotMatch(deedActionButtons(later, me.id, 13).join(""), /disabled/);
  assert.match(deedActionButtons(later, me.id, 19).join(""), /data-deed-action="mortgage" disabled>[\s\S]*?data-why="mortgage">On your turn/);
  // Unowned: nothing, unless you're standing on it with the choice.
  assert.deepEqual(deedActionButtons(at({}), me.id, 1), []);
  assert.match(deedActionButtons(at({}, { phase: "awaiting_purchase", pendingPurchase: 1 }), me.id, 1).join(""), /data-act='\{"type":"buy"\}'[^>]*><span class="btn__label">BUY · \$60/);
});

test("deed sheet: phone.js wires TRADE THIS… and ASK TO BUY… to a pre-filled trade page (source check)", () => {
  const src = readFileSync(join(here, "..", "phone", "phone.js"), "utf8");
  assert.match(src, /const buttons = deedActionButtons\(state, player\?\.id, index\);/);
  assert.match(src, /case "deed-ask": \{[\s\S]{0,700}targetId: propState\(state, index\)\?\.ownerId \|\| null, request: \[index\][\s\S]{0,300}ui\.tab = "trade";[\s\S]{0,200}maybeQuote\(\);/);
  assert.match(src, /offer: \[index\] \}/);
});
