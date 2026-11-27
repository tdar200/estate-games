// Near Thing screens (docs/CLOSEST-API.md §2, §3, §4, §8): the 24 fixtures of §8 in the §3 shape,
// the helpers the four looks stand on (numbers and words, the pad, the odds, the bet, plate tags,
// the reveal breakdown, phase timing, the sounds), the phone routes and its quiet errors, the
// picker's fifth tile, and every fixture and scene rendered on the table screen and the phone with
// nothing broken in the markup. Then, when Playwright and Edge are available, every table fixture
// and scene at 1600 × 900 (stage scale 1: nothing under 20 px, the question ≥ 44, slot numbers ≥ 36,
// nothing off screen or spilling) and every phone one at 360 × 740 (nothing under 14 px, nothing
// wider than the screen, touch targets ≥ 44 px, bet rows ≥ 56 px, the keys and LOCK IN on screen).
// Set CLOSEST_SHOTS=<folder> to save a screenshot of each.
// Run: node --test web/tests/closest.check.mjs

import assert from "node:assert/strict";
import { createServer } from "node:http";
import { existsSync, mkdirSync, readFileSync, readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import {
  CLOSEST_SOUNDS, GAME_TITLE, PAD_KEYS, PAD_MAX_DIGITS, answerPosition, betAction, betCheck, betDraft, betKey, betModel, betStart, breakdown, closestMood, closestSounds,
  closestTimedSounds, fillNote, formatNumber, guessAction, guessCheck, isClosestState, lineModel, lineupDelays, lineupMs, middleSlot, numberWords, oddsFor, oddsLabel, oddsLadder,
  offText, ownSlot, padDisabled, padModel, padPress, padValue, phaseDuration, phaseElapsed, phaseLine, placeChip, plateDelta, plateTag, questionLabel, rangeText, revealModel,
  revealSteps, riskText, setStake, setStakeSlot, settingChips, settingsLine, signedPoints, stakeMaxOf, stakeWin, stepStake, withUnit,
} from "../shared/closest-view.js";
import { CLOSEST_SCENES, buildClosestScene, demoClock, settle } from "../shared/closest-scenes.js";
import { betLook, lineIsTight, lineLook, padLook, revealLineHtml, revealLook } from "../shared/closest-looks.js";
import { CLOSEST_SCREENS, closestRoute, helpSheetHtml, moreSheetHtml, renderClosest } from "../phone/closest-screens.js";
import { SILENT_ERRORS, isSilentError } from "../phone/closest-app.js";
import { closestEndConfirmHtml, closestGameHtml, closestHelpOverlayHtml, closestLobbyHtml } from "../table/closest-table.js";
import { GAME_TYPES, gameLabel, gamePickHtml, gameTypeOf, gtypeTag } from "../shared/poker-view.js";
import { SOUND_NAMES } from "../shared/sound.js";
import { LiveRoom } from "../shared/client.js";

const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, "..");
const dir = join(webRoot, "fixtures", "closest");
const loadFixture = async (name) => JSON.parse(readFileSync(join(dir, `${name}.json`), "utf8"));
const fixtureNames = readdirSync(dir).filter((file) => file.endsWith(".json") && file !== "index.json").map((file) => file.replace(/\.json$/, ""));
const sceneNames = Object.keys(CLOSEST_SCENES);
const allNames = [...fixtureNames, ...sceneNames];
const scene = async (name) => (await buildClosestScene(name, loadFixture)).state;
const fixture = async (name) => (await loadFixture(name)).state;

// The fixture list of docs §8.
const DOC_FIXTURES = [
  "closest-lobby-table", "closest-lobby-phone", "closest-intro-table", "closest-guess-table", "closest-guess-phone", "closest-guess-year-phone", "closest-guessed-phone",
  "closest-lineup-table", "closest-lineup-phone", "closest-bet-table", "closest-bet-phone", "closest-bet-8-phone", "closest-betin-phone", "closest-chips-table",
  "closest-reveal-table", "closest-reveal-phone", "closest-reveal-tie-table", "closest-reveal-nobody-table", "closest-reveal-oneslot-table", "closest-last-guess-table",
  "closest-finished-table", "closest-finished-phone", "closest-lineup-big-table", "closest-chips-pile-table",
];
const PHASES = new Set(["intro", "guess", "lineup", "bet", "chips", "reveal"]);
const BAD = /undefined|NaN|\[object |null(?![-\w])/;
// Never the original board game's name or its words (task rules).
const BANNED = /\bwits\b|wager|\bmats?\b/i;

// What each screen shows for a state, on the state's own clock (as the demos do), settled.
function tableHtml(state, now = demoClock(state)) {
  if (state.status === "lobby") return closestLobbyHtml(state, { now, qrUrl: "", aiLevel: "normal", demo: true });
  return closestGameHtml(state, { now, elapsed: Infinity, demo: true });
}
function phoneView(state, cg = {}) {
  return { cg: { now: demoClock(state), aiLevel: null, pad: null, padError: "", changing: null, sent: null, sending: false, bet: null, betSent: null, ...cg }, session: { code: state.code, role: state.viewerId ? "player" : "spectator", playerId: state.viewerId || null } };
}
function phoneHtml(state, cg) {
  const out = renderClosest(state, phoneView(state, cg));
  return { ...out, html: `${out.header}${out.banner}${out.main}` };
}
const spectator = (state) => ({ ...state, viewerId: null, viewerRole: "spectator", viewerName: "Guest", you: null });
const textOf = (html) => html.replace(/<style[\s\S]*?<\/style>|<svg[\s\S]*?<\/svg>/g, " ").replace(/<[^>]+>/g, " ").replace(/&[a-z#0-9]+;/gi, " ");

// ---------------------------------------------------------------------------------------------
// Fixtures

test("fixtures: the 24 of docs §8, each a Near Thing state in the §3 shape, no answer before the reveal", async () => {
  for (const name of DOC_FIXTURES) assert.ok(fixtureNames.includes(name), `${name}.json is missing`);
  for (const name of fixtureNames) {
    const state = await fixture(name);
    assert.ok(isClosestState(state), `${name}: game is closest`);
    assert.equal(state.title, "Near Thing", `${name}: title`);
    assert.ok(["lobby", "playing", "finished"].includes(state.status), `${name}: status`);
    assert.ok(Number(state.now) > 0, `${name}: now`);
    assert.ok(Array.isArray(state.players), `${name}: players`);
    if (name.endsWith("-phone")) assert.ok(state.viewerId && state.you, `${name}: a phone fixture has a viewer and you`);
    if (state.status !== "playing") continue;
    assert.ok(PHASES.has(state.phase), `${name}: phase ${state.phase}`);
    const question = state.question;
    if (state.phase === "intro" && !question) continue;
    assert.ok(question && Number.isInteger(question.seq) && question.text, `${name}: question`);
    if (state.phase !== "reveal") assert.equal(question.answer, undefined, `${name}: the answer is hidden until the reveal`);
    for (const slot of question.slots || []) {
      assert.equal(slot.odds, oddsFor(slot.index, question.slots.length), `${name}: slot ${slot.index} odds match the ladder`);
      if (!["chips", "reveal"].includes(state.phase)) assert.equal(slot.chips, undefined, `${name}: chips stay hidden until the chips phase`);
    }
  }
  const big = await fixture("closest-lineup-big-table");
  assert.equal(big.question.slots.length, 8);
  assert.ok(big.question.slots.every((slot) => String(slot.value).length === 7), "eight 7-digit guesses");
  const pile = await fixture("closest-chips-pile-table");
  assert.equal(pile.question.slots.filter((slot) => (slot.chips || []).length).length, 1, "all the chips on one slot");
});

// ---------------------------------------------------------------------------------------------
// Numbers, the pad, the odds

test("numbers: separators for plain numbers, none for years; the unit; the range", () => {
  assert.equal(GAME_TITLE, "NEAR THING");
  assert.equal(formatNumber(3200000), "3,200,000");
  assert.equal(formatNumber(12500), "12,500");
  assert.equal(formatNumber(640), "640");
  assert.equal(formatNumber(0), "0");
  assert.equal(formatNumber(1969, "year"), "1969");
  assert.equal(formatNumber(-1200), "−1,200");
  assert.equal(formatNumber("x"), "");
  assert.equal(withUnit(3600, { unit: "pipes", format: "plain" }), "3,600 pipes");
  assert.equal(withUnit(1786, { unit: "", format: "year" }), "1786");
  assert.equal(rangeText({ min: 100, max: 60000, unit: "pipes", format: "plain" }), "100 to 60,000 pipes");
  assert.equal(rangeText({ min: 1000, max: 2025, format: "year" }), "1000 to 2025");
  assert.equal(rangeText({}), "");
});

test("numbers in words: millions and thousands, small numbers and years as digits, never 1,969", () => {
  assert.equal(numberWords(3200000), "3 million 200 thousand");
  assert.equal(numberWords(12500), "12 thousand 500");
  assert.equal(numberWords(4350000), "4 million 350 thousand");
  assert.equal(numberWords(2000000000), "2 billion");
  assert.equal(numberWords(1001), "1 thousand 1");
  assert.equal(numberWords(640), "640");
  assert.equal(numberWords(1969, "year"), "1969");
  for (const value of [1969, 2025, 1000]) assert.doesNotMatch(numberWords(value, "year"), /,|thousand/);
  assert.equal(numberWords(null), "");
  assert.equal(formatNumber(undefined), "");
});

test("pad: digits, 000, backspace, at most 9 digits, leading zeros dropped", () => {
  assert.equal(PAD_KEYS.length, 12, "a 3 × 4 grid");
  assert.equal(PAD_MAX_DIGITS, 9);
  let digits = "";
  for (const key of ["3", "2", "000", "000"]) digits = padPress(digits, key);
  assert.equal(digits, "32000000");
  assert.equal(padValue(digits), 32000000);
  assert.equal(padPress(digits, "000"), digits, "000 past 9 digits is refused");
  assert.equal(padPress(digits, "5"), "320000005");
  assert.equal(padPress("320000005", "1"), "320000005", "a 10th digit is refused");
  assert.equal(padPress("12", "back"), "1");
  assert.equal(padPress("", "back"), "");
  assert.equal(padPress("", "000"), "", "000 on an empty pad does nothing");
  assert.equal(padPress("0", "000"), "0");
  assert.equal(padPress("0", "7"), "7", "a leading zero goes");
  assert.equal(padPress("", "0"), "0");
  assert.equal(padPress("12", "clear"), "");
  assert.equal(padPress("12", "x"), "12");
  assert.equal(padValue(""), null);
  const off = padDisabled("");
  assert.equal(off.back, true);
  assert.equal(off["000"], true);
  assert.equal(off["1"], false);
  const full = padDisabled("123456789");
  assert.equal(full["1"], true);
  assert.equal(full.back, false);
  assert.equal(padDisabled("1234567")["000"], true, "000 would pass 9 digits");
});

test("guess check: the server's wording outside min–max, with the question's own separators", () => {
  const question = { min: 100, max: 60000, format: "plain" };
  assert.deepEqual(guessCheck(null, question), { ok: false, error: "" }, "an empty pad says nothing");
  assert.deepEqual(guessCheck(99, question), { ok: false, error: "guess between 100 and 60,000" });
  assert.deepEqual(guessCheck(60001, question), { ok: false, error: "guess between 100 and 60,000" });
  assert.deepEqual(guessCheck(100, question), { ok: true, error: "" });
  assert.deepEqual(guessCheck(60000, question), { ok: true, error: "" });
  assert.deepEqual(guessCheck(999, { min: 1000, max: 2025, format: "year" }), { ok: false, error: "guess between 1000 and 2025" });
  assert.deepEqual(guessAction({ question: { seq: 4 } }, "3600"), { type: "guess", q: 4, value: 3600 });
});

test("pad model and look: the draft with separators, its words, the range, 12 keys, LOCK IN only in range", async () => {
  const state = await fixture("closest-guess-phone");
  const empty = padModel(state, { digits: "" });
  assert.equal(empty.canLock, false);
  assert.equal(empty.error, "");
  assert.equal(empty.range, rangeText(state.question));
  const typed = padModel(state, { digits: "3200" });
  assert.equal(typed.display, formatNumber(3200, state.question.format));
  assert.equal(typed.canLock, guessCheck(3200, state.question).ok);
  const short = padModel(state, { digits: "4" });
  assert.equal(short.canLock, false, "under the minimum: no LOCK IN");
  assert.equal(short.error, "", "under the minimum: more digits can fix it, so no error yet");
  const wild = padModel(state, { digits: "999999999" });
  assert.equal(wild.canLock, false);
  assert.match(wild.error, /^guess between /);
  const html = padLook(wild);
  assert.equal((html.match(/data-cg="pad"/g) || []).length, 12);
  assert.match(html, /Out of range: guess between/);
  assert.match(html, /data-cg="lock" disabled/);
  assert.match(padLook(padModel(state, { digits: "", sending: false })), /Type your guess/);
  const year = await fixture("closest-guess-year-phone");
  const yearModel = padModel(year, { digits: "1969" });
  assert.equal(yearModel.display, "1969");
  assert.equal(yearModel.words, "1969");
  assert.doesNotMatch(padLook(yearModel), /1,969/);
  assert.equal(padModel({ ...state, you: { ...state.you, guess: 3000 } }, { digits: "1" }).changing, true);
});

test("odds: the middle pays 2×, each step out one more, up to 5×", () => {
  assert.deepEqual(oddsLadder(1), [2]);
  assert.deepEqual(oddsLadder(2), [2, 2]);
  assert.deepEqual(oddsLadder(3), [3, 2, 3]);
  assert.deepEqual(oddsLadder(6), [4, 3, 2, 2, 3, 4]);
  assert.deepEqual(oddsLadder(7), [5, 4, 3, 2, 3, 4, 5]);
  assert.deepEqual(oddsLadder(8), [5, 4, 3, 2, 2, 3, 4, 5]);
  assert.equal(oddsLabel(3), "3×");
  assert.equal(oddsLabel(0), "");
  assert.equal(middleSlot(6), 2);
  assert.equal(middleSlot(1), 0);
});

// ---------------------------------------------------------------------------------------------
// The bet

test("bet: both chips start on your own guess (the middle without one); a stake 0–stakeMax on a slot holding a chip", async () => {
  const state = await fixture("closest-bet-phone");
  assert.equal(ownSlot(state), 3, "Maya guessed 3,600: slot 3");
  assert.equal(stakeMaxOf(state), 3);
  const start = betStart(state);
  assert.deepEqual(start, { key: betKey(state), chips: [3, 3], stake: 0, stakeSlot: 3 });
  assert.deepEqual(betCheck(start, state), { ok: true, error: "" }, "one tap confirms");
  const noGuess = { ...state, question: { ...state.question, slots: state.question.slots.map((slot) => ({ ...slot, ownerIds: slot.ownerIds.filter((id) => id !== "p1") })) } };
  assert.deepEqual(betStart(noGuess).chips, [2, 2], "no guess: the middle slot");

  let bet = placeChip(start, 1, 5);
  assert.deepEqual(bet.chips, [3, 5]);
  assert.equal(bet.stakeSlot, 3, "the stake stays while a chip is still there");
  bet = placeChip(bet, 0, 1);
  assert.deepEqual(bet.chips, [1, 5]);
  assert.equal(bet.stakeSlot, 1, "the stake follows the moved chip");
  assert.equal(setStakeSlot(bet, 4).stakeSlot, 1, "never on a slot without a chip");
  bet = setStakeSlot(bet, 5);
  assert.equal(bet.stakeSlot, 5);
  assert.equal(setStake(bet, 9, 3).stake, 3);
  assert.equal(setStake(bet, -2, 3).stake, 0);
  bet = stepStake(stepStake(bet, 1, 3), 1, 3);
  assert.equal(bet.stake, 2);
  assert.equal(stepStake(stepStake(bet, 1, 3), 1, 3).stake, 3, "no higher than stakeMax");
  assert.deepEqual(betAction(state, bet), { type: "bet", q: state.question.seq, chips: [1, 5], stake: 2, stakeSlot: 5 });
  assert.deepEqual(betAction(state, start), { type: "bet", q: state.question.seq, chips: [3, 3], stake: 0 }, "no stakeSlot without a stake");

  assert.deepEqual(betCheck({ ...bet, chips: [1] }, state), { ok: false, error: "pick two slots" });
  assert.deepEqual(betCheck({ ...bet, chips: [1, 9] }, state), { ok: false, error: "pick two slots" });
  assert.deepEqual(betCheck({ ...bet, stake: 4 }, state), { ok: false, error: "stake between 0 and 3" });
  assert.deepEqual(betCheck({ ...bet, stakeSlot: 0 }, state), { ok: false, error: "put the stake on one of your chips" });
  assert.equal(riskText(3), "you could lose 3");
  assert.equal(riskText(0), "free chips never lose");
  assert.equal(stakeWin(2, 4), 8);

  // A draft from another question starts again; one out of range is put right.
  assert.deepEqual(betDraft(state, { ...bet, key: "OTHER|1" }), start);
  const poorer = { ...state, you: { ...state.you, stakeMax: 1 } };
  assert.equal(betDraft(poorer, bet).stake, 1);
  assert.equal(stakeMaxOf({ you: { stakeMax: 12 } }), 3);
  assert.equal(stakeMaxOf({ you: { stakeMax: -1 } }), 0);
});

test("bet model and look: a row per slot, both chip buttons, the stake stepper, CONFIRM", async () => {
  const state = await fixture("closest-bet-phone");
  const bet = setStake(placeChip(betStart(state), 1, 0), 2, 3);
  const model = betModel(state, bet);
  assert.equal(model.rows.length, state.question.slots.length);
  assert.equal(model.split, true);
  assert.equal(model.rows[3].mine, true);
  assert.equal(model.rows[3].stakeHere, true);
  assert.equal(model.stakeWins, 2 * state.question.slots[3].odds);
  const html = betLook(model, state);
  assert.equal((html.match(/class="cg-row[" ]/g) || []).length, state.question.slots.length);
  assert.equal((html.match(/data-cg="chip"/g) || []).length, state.question.slots.length * 2);
  assert.equal((html.match(/data-cg="stake-slot"/g) || []).length, 2, "the stake can go on either chip's slot");
  assert.match(html, /data-cg="stake-step" data-dir="-1"/);
  assert.match(html, /you could lose 2/);
  assert.match(html, /data-cg="confirm"(?! disabled)/);
  const free = betLook(betModel({ ...state, you: { ...state.you, stakeMax: 0 } }, null), state);
  assert.match(free, /No points to stake yet/);
  assert.doesNotMatch(free, /data-cg="stake-step"/);
  const eight = await fixture("closest-bet-8-phone");
  assert.equal(betModel(eight, null).rows.length, eight.question.slots.length);
});

// ---------------------------------------------------------------------------------------------
// Plates, the reveal, timing

test("plate tags: GUESSED, BET IN, AWAY; LEFT outranks them; done outranks AWAY", async () => {
  const guess = await fixture("closest-guess-table");
  const someone = guess.players[0];
  assert.deepEqual(plateTag({ ...guess, question: { ...guess.question, guessed: [someone.id] } }, someone), { tag: "GUESSED", tone: "locked" });
  assert.deepEqual(plateTag({ ...guess, question: { ...guess.question, guessed: [] } }, { ...someone, guessed: false, online: false }), { tag: "AWAY", tone: "away" });
  assert.deepEqual(plateTag({ ...guess, question: { ...guess.question, guessed: [someone.id] } }, { ...someone, online: false }), { tag: "GUESSED", tone: "locked" });
  assert.deepEqual(plateTag(guess, { ...someone, status: "left" }), { tag: "LEFT", tone: "left" });
  const bet = await fixture("closest-bet-table");
  const inId = bet.question.betsIn[0];
  assert.deepEqual(plateTag(bet, bet.players.find((player) => player.id === inId)), { tag: "BET IN", tone: "locked" });
  assert.deepEqual(plateTag(bet, { id: "zz", status: "playing", online: true }), { tag: "", tone: "" });
  const reveal = await fixture("closest-reveal-table");
  assert.equal(plateDelta(reveal, "p1"), "+8");
  assert.equal(plateDelta(reveal, "p6"), "−1");
  assert.equal(plateDelta(reveal, "p4"), "");
  assert.equal(signedPoints(0), "±0");
});

test("reveal breakdown: bonus, chips and stake in words; no points; no guess; nobody guessed", async () => {
  const reveal = await fixture("closest-reveal-table");
  const p1 = breakdown(reveal, "p1");
  assert.equal(p1.text, "+1 second closest · +3 chips · +4 stake");
  assert.equal(p1.total, 8);
  assert.equal(breakdown(reveal, "p5").text, "+3 closest");
  assert.equal(breakdown(reveal, "p6").text, "−1 stake");
  assert.equal(breakdown(reveal, "p4").text, "no points");
  assert.equal(breakdown(reveal, "nobody-here").text, "");
  const nobody = await fixture("closest-reveal-nobody-table");
  assert.equal(breakdown(nobody, nobody.players[0].id).text, "nobody guessed");
  assert.equal(phaseLine(nobody), "NOBODY GUESSED");
  assert.equal(phaseLine(reveal), `${reveal.players.find((player) => player.id === "p5").name.toUpperCase()} IS CLOSEST`);
  assert.equal(phaseLine(reveal, { viewerId: "p5" }), "YOU'RE CLOSEST");
  assert.equal(phaseLine(await fixture("closest-reveal-tie-table")), "A TIE: BOTH WIN");
  assert.equal(offText(0), "exact");
  assert.equal(offText(1300), "off by 1,300");
  assert.equal(offText(12, "year"), "off by 12");
  assert.equal(answerPosition([{ value: 10 }, { value: 20 }], 15), 0.5);
  assert.equal(answerPosition([{ value: 10 }, { value: 20 }], 5), -0.5);
  assert.equal(answerPosition([{ value: 10 }, { value: 20 }], 25), 1.5);
  const model = revealModel(reveal, { elapsed: 1000 });
  assert.equal(model.answerText, withUnit(reveal.question.answer, reveal.question));
  assert.equal(model.results.length, reveal.question.results.length);
  assert.equal(model.beats.answer, 300 - 1000, "beats are relative to now");
  assert.match(revealLook(model, reveal), /CLOSEST<\/span>[\s\S]*SECOND<\/span>/);
  assert.match(revealLook(revealModel(nobody), nobody), /Nobody guessed, so nobody scores/);
  // The phase line holds THE ANSWER until the wins beat; nothing to hold when nobody guessed.
  const line = revealLineHtml(reveal, phaseLine(reveal, { short: true }), 1000);
  assert.match(line, /cg-swap__a[^>]*>THE ANSWER<[\s\S]*cg-swap__b">[A-Z ]+ IS CLOSEST</);
  assert.match(line, new RegExp(`--wins:${model.beats.wins}ms`));
  assert.equal(revealLineHtml(nobody, phaseLine(nobody)), "<b>NOBODY GUESSED</b>");
  assert.equal(revealLineHtml({ ...reveal, phase: "chips" }, "CHIPS DOWN"), "<b>CHIPS DOWN</b>");
});

test("settle: the scenes score a question as docs §4.1 does (the reveal fixture again from its chips)", async () => {
  const reveal = await fixture("closest-reveal-table");
  const chips = { ...reveal, phase: "chips", question: { ...reveal.question, answer: undefined, results: undefined, slots: reveal.question.slots.map(({ off, result, ...slot }) => slot) }, players: reveal.players.map((player) => ({ ...player, score: (reveal.question.results.find((row) => row.playerId === player.id) || {}).before ?? player.score })) };
  const settled = settle(chips, reveal.question.answer);
  for (const row of reveal.question.results) {
    const mine = settled.question.results.find((other) => other.playerId === row.playerId);
    assert.deepEqual({ bonus: mine.bonus, chipPoints: mine.chipPoints, stakePoints: mine.stakePoints, delta: mine.delta }, { bonus: row.bonus, chipPoints: row.chipPoints, stakePoints: row.stakePoints, delta: row.delta }, row.playerId);
  }
  assert.deepEqual(settled.question.slots.map((slot) => slot.result || ""), reveal.question.slots.map((slot) => slot.result || ""));
});

test("timing: the line-up clamp, fixed phase lengths, how far in, slot entrances, reveal beats", async () => {
  assert.equal(lineupMs(1), 3000);
  assert.equal(lineupMs(6), 5100);
  assert.equal(lineupMs(8), 6300);
  assert.equal(lineupMs(9), 6500);
  assert.equal(phaseDuration({ phase: "intro" }), 4000);
  assert.equal(phaseDuration({ phase: "chips" }), 3500);
  assert.equal(phaseDuration({ phase: "reveal", question: { no: 2 }, questionCount: 7 }), 8000);
  assert.equal(phaseDuration({ phase: "reveal", question: { no: 7, double: true }, questionCount: 7 }), 9000, "the last reveal is a second longer");
  assert.equal(phaseDuration({ phase: "guess" }), null);
  const lineup = await fixture("closest-lineup-table");
  const duration = phaseDuration(lineup);
  assert.equal(duration, lineupMs(lineup.question.slots.length));
  assert.equal(phaseElapsed(lineup, lineup.phaseEndsAt), duration);
  assert.equal(phaseElapsed(lineup, lineup.phaseEndsAt - duration - 500), 0);
  assert.equal(phaseElapsed({ phase: "guess", phaseEndsAt: 5 }, 1), 0);
  for (const count of [1, 2, 6, 8]) {
    const delays = lineupDelays(count);
    assert.equal(delays.length, count);
    assert.ok(delays.every((delay, index) => index === 0 || delay > delays[index - 1]), `${count}: low to high`);
    assert.ok(delays.at(-1) <= lineupMs(count) - 1200, `${count}: time to read the last one`);
    assert.ok(delays.every((delay, index) => index === 0 || delay - delays[index - 1] <= 600), `${count}: at most 600 ms apart`);
  }
  const steps = revealSteps(8000);
  assert.ok(steps.answer < steps.slide && steps.slide < steps.slideEnd && steps.slideEnd < steps.off && steps.off < steps.wins && steps.wins < steps.payouts && steps.payouts < steps.fact);
  assert.ok(steps.fact < 8000);
  assert.equal(revealSteps(9000).fact, Math.round(steps.fact * 9 / 8));
  const line = lineModel(lineup, { elapsed: 0 });
  assert.ok(line.slots.every((slot) => slot.delay > 0), "a fresh line-up: every slot still to come");
  assert.ok(lineModel(lineup).slots.every((slot) => slot.delay < 0), "settled: every slot in");
});

test("settings, words and the question label", async () => {
  const lobby = await fixture("closest-lobby-table");
  const chips = settingChips(lobby);
  assert.deepEqual(chips.map((chip) => chip.key), ["length", "guessSeconds", "betSeconds", "fill"]);
  assert.match(settingsLine(lobby), /GAME · .* TO GUESS · .* TO BET · COMPUTERS FILL/);
  assert.match(fillNote({ ...lobby, players: lobby.players.slice(0, 1), settings: { ...lobby.settings, fill: true } }), /^3 computers will join so there are 4 guesses$/);
  assert.equal(fillNote({ ...lobby, settings: { ...lobby.settings, fill: false } }), "");
  assert.equal(questionLabel({ question: { no: 2 }, questionCount: 7 }), "QUESTION 2 OF 7");
  assert.equal(questionLabel({ question: { no: 7, double: true }, questionCount: 7 }), "LAST QUESTION · DOUBLE POINTS");
});

// ---------------------------------------------------------------------------------------------
// Sounds

test("sounds: one trigger list of sound.js sounds; phase changes and the clock fire them", async () => {
  for (const [trigger, sound] of Object.entries(CLOSEST_SOUNDS)) assert.ok(SOUND_NAMES.includes(sound.name), `${trigger}: ${sound.name} is a sound.js sound`);
  for (const trigger of ["lockIn", "slotPop", "chipsLand", "answerSlide", "win", "tick"]) assert.ok(CLOSEST_SOUNDS[trigger], `${trigger} (docs §8)`);
  const guess = await fixture("closest-guess-table");
  const intro = await fixture("closest-intro-table");
  assert.deepEqual(closestSounds(intro, guess).map((hit) => hit.trigger), ["question"]);
  const others = { ...guess, question: { ...guess.question, guessed: [...(guess.question.guessed || []), "zz"] } };
  assert.deepEqual(closestSounds(guess, others).map((hit) => hit.trigger), ["othersIn"]);
  assert.deepEqual(closestSounds(guess, others, { viewerId: "zz" }), [], "your own guess plays on the tap, not again");
  const chips = await fixture("closest-chips-table");
  const bet = await fixture("closest-bet-table");
  assert.deepEqual(closestSounds(bet, chips).map((hit) => hit.trigger), ["chipsLand"]);
  const finished = await fixture("closest-finished-table");
  assert.ok(closestSounds({ ...finished, status: "playing", phase: "reveal" }, finished).some((hit) => hit.trigger === "gameOver"));
  assert.deepEqual(closestSounds(null, guess), []);

  const lineup = await fixture("closest-lineup-table");
  const pops = closestTimedSounds(lineup).filter((hit) => hit.trigger === "slotPop");
  assert.equal(pops.length, lineup.question.slots.length);
  assert.deepEqual(pops.map((hit) => hit.opts.step), lineup.question.slots.map((_, index) => index + 1));
  const reveal = await fixture("closest-reveal-table");
  const timed = closestTimedSounds(reveal, { viewerId: "p1" });
  assert.deepEqual(timed.map((hit) => hit.trigger), ["answerSlide", "win"]);
  assert.deepEqual(closestTimedSounds(reveal, { viewerId: "p4" }).map((hit) => hit.trigger), ["answerSlide", "miss"]);
  const ticks = closestTimedSounds(guess).filter((hit) => hit.trigger === "tick");
  assert.equal(ticks.length, 5);
  assert.equal(ticks.at(-1).at, guess.phaseEndsAt - 1000);
  assert.equal(ticks.at(-1).opts.last, true);
  assert.deepEqual(closestTimedSounds(guess, { ticks: false }), [], "no ticks once you are in");
  assert.equal(closestMood(await fixture("closest-lobby-table")), "lobby");
  assert.equal(closestMood(guess), "play");
  assert.equal(closestMood(finished), "victory");
});

// ---------------------------------------------------------------------------------------------
// Phone routes, quiet errors, socket actions

test("phone routes: each phone fixture lands on its screen; spectators watch", async () => {
  const expect = {
    "closest-lobby-phone": "cg-lobby", "closest-guess-phone": "cg-guess", "closest-guess-year-phone": "cg-guess", "closest-guessed-phone": "cg-guessed",
    "closest-lineup-phone": "cg-lineup", "closest-bet-phone": "cg-bet", "closest-bet-8-phone": "cg-bet", "closest-betin-phone": "cg-betin",
    "closest-reveal-phone": "cg-reveal", "closest-finished-phone": "cg-result",
  };
  for (const [name, screen] of Object.entries(expect)) assert.equal(closestRoute(await fixture(name), phoneView(await fixture(name))), screen, name);
  const guess = await fixture("closest-guess-phone");
  assert.equal(closestRoute(spectator(guess), phoneView(spectator(guess))), "cg-watch");
  assert.equal(closestRoute(guess, phoneView(guess, { sent: { seq: guess.question.seq, value: 3000 } })), "cg-guessed", "a sent guess shows at once");
  const other = await fixture("closest-guessed-phone");
  assert.equal(closestRoute(other, phoneView(other, { changing: other.question.seq + 1 })), "cg-guessed", "changing needs this question's seq");
  const guessed = await fixture("closest-guessed-phone");
  assert.equal(closestRoute(guessed, phoneView(guessed, { changing: guessed.question.seq })), guessed.you.canGuess ? "cg-guess" : "cg-guessed");
  const bet = await fixture("closest-bet-phone");
  assert.equal(closestRoute(bet, phoneView(bet, { betSent: betKey(bet) })), "cg-betin");
  assert.equal(closestRoute(null), "cg-wait");
  for (const name of allNames.filter((candidate) => candidate.endsWith("-phone"))) assert.ok(CLOSEST_SCREENS.includes(closestRoute(await scene(name), phoneView(await scene(name)))), name);
});

test("quiet errors: a phase that moved on says nothing; other refusals become toasts", () => {
  for (const error of ["that question is over", "guesses are closed", "bets are closed", "Bets are closed"]) assert.ok(isSilentError(error), error);
  for (const error of ["guess between 100 and 60,000", "you have already changed your guess", "you have already bet", "pick two slots", "stake between 0 and 3", "put the stake on one of your chips"]) assert.equal(isSilentError(error), false, error);
  assert.ok(SILENT_ERRORS instanceof RegExp);
  assert.equal(isSilentError(new Error("that question is over")), true);
});

test("socket actions: guess and bet go as {type:action, action, id}; POST when the socket is closed", async () => {
  const room = new LiveRoom({}, { code: "K8LLP", token: "t", onState() {} });
  assert.equal(room.send({ type: "guess", q: 2, value: 3600 }), false, "no socket: the caller POSTs");
  const sent = [];
  room.socket = { readyState: WebSocket.OPEN, send: (raw) => sent.push(JSON.parse(raw)) };
  const ok = room.send({ type: "guess", q: 2, value: 3600 });
  assert.deepEqual(sent[0], { type: "action", action: { type: "guess", q: 2, value: 3600 }, id: 1 });
  room.settle({ type: "actionResult", id: 1, ok: true });
  assert.equal(await ok, true);
  const refused = room.send({ type: "bet", q: 2, chips: [3, 3], stake: 0 });
  room.settle({ type: "actionResult", id: 2, ok: false, error: "bets are closed" });
  await assert.rejects(refused, /bets are closed/);
  clearInterval(room.pingTimer);
});

// ---------------------------------------------------------------------------------------------
// Rendering

test("render: every fixture and scene on its screen, on a spectator's phone, and no banned words", async () => {
  for (const name of allNames) {
    const state = await scene(name);
    const tv = tableHtml(name.endsWith("-phone") ? spectator(state) : state);
    assert.doesNotMatch(textOf(tv), BAD, `table ${name}`);
    assert.doesNotMatch(tv, /style="[^"]*(undefined|NaN)/, `table ${name}: styles`);
    assert.doesNotMatch(textOf(tv), BANNED, `table ${name}: words`);
    const phone = phoneHtml(state);
    assert.doesNotMatch(textOf(phone.html), BAD, `phone ${name} (${phone.screen})`);
    assert.doesNotMatch(phone.html, /style="[^"]*(undefined|NaN)/, `phone ${name}: styles`);
    assert.doesNotMatch(textOf(phone.html), BANNED, `phone ${name}: words`);
    const watch = phoneHtml(spectator(state));
    assert.doesNotMatch(textOf(watch.html), BAD, `spectator ${name} (${watch.screen})`);
  }
  const lobby = await scene("closest-lobby-phone");
  for (const html of [moreSheetHtml(lobby, phoneView(lobby)), helpSheetHtml(lobby), closestHelpOverlayHtml(lobby), closestEndConfirmHtml(await scene("closest-bet-table"))]) {
    assert.doesNotMatch(textOf(html), BAD);
    assert.doesNotMatch(textOf(html), BANNED);
  }
  assert.match(helpSheetHtml(lobby), /class="qcredits"|qcredits/, "the help has the credits");
});

test("table screen: the question in guess, the line from the line-up, chips from the chips phase, the reveal", async () => {
  const guess = tableHtml(await fixture("closest-guess-table"));
  assert.match(guess, /class="cg-q"/);
  assert.match(guess, /class="cg-q__text[^"]*"/);
  assert.doesNotMatch(guess, /cg-line/);
  const lineup = tableHtml(await fixture("closest-lineup-table"));
  assert.match(lineup, /class="cg-line is-lineup/);
  assert.doesNotMatch(lineup, /cg-chip"/);
  const chips = tableHtml(await fixture("closest-chips-table"));
  assert.match(chips, /class="cg-chip"/);
  const big = await scene("closest-chips-big-table");
  assert.ok(lineIsTight(lineModel(big)), "eight 7-digit slots drop to the 36 px floor");
  assert.equal(lineIsTight(lineModel(await fixture("closest-chips-table"))), false);
  assert.match(lineLook(lineModel(big), big), /cg-line[^"]*is-tight/);
  const reveal = tableHtml(await fixture("closest-reveal-table"));
  assert.match(reveal, /class="cg-reveal/);
  assert.match(reveal, /class="cg-answer"/);
  assert.doesNotMatch(reveal, /qz-plates/, "the results take the plates' place at the reveal");
  assert.match(tableHtml(await fixture("closest-reveal-nobody-table")), /qz-plates/);
  const last = tableHtml(await fixture("closest-last-guess-table"));
  assert.match(last, /DOUBLE/);
  const lobby = tableHtml(await fixture("closest-lobby-table"));
  assert.match(lobby, /data-cmd="cg-setting"/);
  assert.match(lobby, /gpick--5[\s\S]*data-arg="closest" aria-pressed="true"/);
});

test("picker: a fifth tile named from GAME_TITLE; the type tag; the game types", () => {
  assert.deepEqual(GAME_TYPES, ["estate", "poker", "ludo", "quiz", "closest"]);
  assert.equal(gameLabel("closest"), GAME_TITLE);
  assert.equal(gameTypeOf({ game: "closest" }), "closest");
  assert.match(gtypeTag("closest"), /gtype--closest[\s\S]*NEAR THING/);
  const html = gamePickHtml("closest", { cmd: "pick-game" });
  assert.equal((html.match(/class="gtile[ "]/g) || []).length, 5);
  assert.match(html, /gpick--5/);
  assert.match(html, /data-arg="closest"[^>]*aria-pressed="true"/);
  assert.match(gamePickHtml("estate", { cmd: "pick-game" }), /data-arg="closest"[^>]*aria-pressed="false"/);
});

test("wiring: both pages load closest.css; the table has its screen; CSS floors (table ≥ 20, phone ≥ 14); no banned words in the code", () => {
  const table = readFileSync(join(webRoot, "table", "index.html"), "utf8");
  const phone = readFileSync(join(webRoot, "phone", "index.html"), "utf8");
  assert.match(table, /href="\/shared\/closest\.css"/);
  assert.match(phone, /href="\/shared\/closest\.css"/);
  assert.match(table, /id="closest-screen"[^>]*class="pk-screen qz-tv cg-tv"/);
  const css = readFileSync(join(webRoot, "shared", "closest.css"), "utf8");
  assert.doesNotMatch(css, /@import|url\(\s*["']?https?:/, "no outside fonts or files (CSP)");
  const [tableCss, phoneCss] = css.split("/* ============================================================== Phone");
  assert.ok(phoneCss, "closest.css has its Phone section");
  for (const block of tableCss.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    const selector = block[1].trim();
    if (/\.app\b/.test(selector) || !/\.cg-|\.pk-tv__lobby/.test(selector)) continue;
    for (const size of block[2].matchAll(/font-size:\s*(\d+(?:\.\d+)?)px/g)) assert.ok(Number(size[1]) >= 20, `${selector}: ${size[1]}px`);
  }
  assert.match(tableCss, /\.cg-q__text\s*\{[^}]*font-size:\s*(4[4-9]|[5-9]\d)px/, "the question ≥ 44");
  assert.match(tableCss, /\.cg-q\.is-compact \.cg-q__text[^{]*\{[^}]*font-size:\s*44px/, "the compact question is still 44");
  assert.match(tableCss, /\.cg-line\.is-tight \.cg-slot__num\s*\{[^}]*font-size:\s*36px/, "tight slot numbers stay at 36");
  for (const block of phoneCss.matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    for (const size of block[2].matchAll(/font-size:\s*(\d+(?:\.\d+)?)px/g)) assert.ok(Number(size[1]) >= 14, `${block[1].trim()}: ${size[1]}px`);
  }
  for (const file of ["shared/closest-view.js", "shared/closest-looks.js", "shared/closest-scenes.js", "shared/closest.css", "phone/closest-app.js", "phone/closest-screens.js", "table/closest-table.js"]) {
    assert.doesNotMatch(readFileSync(join(webRoot, file), "utf8"), BANNED, file);
  }
});

// ---------------------------------------------------------------------------------------------
// In a real browser

const PW = process.env.PLAYWRIGHT || ""; // node_modules/playwright; these browser checks skip without it (CONTRIBUTING.md)
const TYPES = { ".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8", ".json": "application/json; charset=utf-8", ".svg": "image/svg+xml", ".woff2": "font/woff2" };

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
  try { return await chromium.launch({ channel: "msedge", headless: true }); } catch { return null; }
}

const SKIP_JS = `const SKIP = ".gtile__art, .sr-only";`;

const TABLE_PROBE = `(() => {
  ${SKIP_JS}
  const m = /scale\\(([\\d.]+)\\)/.exec(document.querySelector("#stage")?.style.transform || "");
  const scale = m ? Number(m[1]) : 1;
  const out = [];
  const screen = document.querySelector("#closest-screen");
  const roots = [screen, ...document.querySelectorAll(".cg-tvhelp, .dialog")].filter(Boolean);
  const visible = (el) => { const s = getComputedStyle(el); if (s.visibility === "hidden" || s.display === "none" || Number(s.opacity) === 0) return false; const b = el.getBoundingClientRect(); return b.width > 0 && b.height > 0; };
  if (!screen || !visible(screen)) out.push("the Near Thing screen is not showing");
  for (const root of roots) {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    for (let node; (node = walker.nextNode());) {
      const words = node.textContent.trim();
      if (!words) continue;
      const el = node.parentElement;
      if (!visible(el) || el.closest(SKIP)) continue;
      const size = parseFloat(getComputedStyle(el).fontSize) * scale;
      if (size < 19.9) out.push("text " + size.toFixed(1) + "px: ." + String(el.className).split(" ")[0] + ' "' + words.slice(0, 24) + '"');
    }
  }
  const floors = [[".cg-q__text", 44], [".cg-slot__num", 36]];
  for (const [selector, min] of floors) for (const el of document.querySelectorAll("#closest-screen " + selector)) {
    if (!visible(el)) continue;
    const size = parseFloat(getComputedStyle(el).fontSize) * scale;
    if (size < min - 0.1) out.push(selector + " " + size.toFixed(1) + "px < " + min);
  }
  for (const el of document.querySelectorAll("#closest-screen *")) {
    if (!visible(el)) continue;
    const b = el.getBoundingClientRect();
    if (b.right > innerWidth + 1 || b.bottom > innerHeight + 1 || b.left < -1 || b.top < -1) { out.push("off screen: ." + String(el.className.baseVal ?? el.className).split(" ")[0] + " " + Math.round(b.left) + "," + Math.round(b.top) + " " + Math.round(b.right) + "," + Math.round(b.bottom)); break; }
  }
  // The answer card is sized by its content (never squeezed); only its width can spill.
  const boxes = ".cg-q, .cg-body, .cg-done, .cg-tv__lobby, .cg-slot, .cg-res, .qz-plate, .qz-done";
  const lines = ".cg-q__text, .cg-slot__num, .cg-answer, .cg-answer__num, .qz-plate__name, .qz-line b, .cg-res__name";
  for (const el of document.querySelectorAll("#closest-screen :is(" + boxes + ", " + lines + ")")) {
    if (!visible(el)) continue;
    const tall = el.matches(boxes) && el.scrollHeight > el.clientHeight + 2;
    if (tall || el.scrollWidth > el.clientWidth + 2) out.push("spills: ." + String(el.className).split(" ")[0] + " " + el.scrollWidth + "×" + el.scrollHeight + " in " + el.clientWidth + "×" + el.clientHeight);
  }
  // Slots and results never overlap each other.
  const cards = [...document.querySelectorAll("#closest-screen .cg-slot, #closest-screen .cg-res, #closest-screen .cg-answer, #closest-screen .cg-q, #closest-screen .qz-plate")].filter(visible).map((el) => [el, el.getBoundingClientRect()]);
  for (let i = 0; i < cards.length; i += 1) for (let j = i + 1; j < cards.length; j += 1) {
    const [a, ra] = cards[i];
    const [b, rb] = cards[j];
    if (a.contains(b) || b.contains(a)) continue;
    if (ra.left < rb.right - 2 && ra.right > rb.left + 2 && ra.top < rb.bottom - 2 && ra.bottom > rb.top + 2) { out.push("overlap: ." + a.className.split(" ")[0] + " and ." + b.className.split(" ")[0]); i = cards.length; break; }
  }
  return { problems: out, scale };
})()`;

const PHONE_PROBE = `(() => {
  ${SKIP_JS}
  const out = [];
  const app = document.querySelector("#app");
  const visible = (el) => { const s = getComputedStyle(el); if (s.visibility === "hidden" || s.display === "none" || Number(s.opacity) === 0) return false; const b = el.getBoundingClientRect(); return b.width > 0 && b.height > 0; };
  const roots = [app, ...document.querySelectorAll("dialog[open]")];
  for (const root of roots) {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    for (let node; (node = walker.nextNode());) {
      const words = node.textContent.trim();
      if (!words) continue;
      const el = node.parentElement;
      if (!visible(el) || el.closest(SKIP)) continue;
      const size = parseFloat(getComputedStyle(el).fontSize);
      if (size < 13.9) out.push("text " + size + "px: ." + String(el.className).split(" ")[0] + ' "' + words.slice(0, 24) + '"');
    }
  }
  if (document.documentElement.scrollWidth > innerWidth + 1) out.push("page wider than the screen");
  for (const el of document.querySelectorAll("#app *")) {
    if (!visible(el)) continue;
    const b = el.getBoundingClientRect();
    if (b.right > innerWidth + 1 || b.left < -1) { out.push("off the side: ." + String(el.className.baseVal ?? el.className).split(" ")[0]); break; }
  }
  for (const el of document.querySelectorAll("#app button, dialog[open] button")) {
    if (!visible(el) || el.closest(".gtile")) continue;
    const b = el.getBoundingClientRect();
    if (b.height < 43.5 || b.width < 43.5) out.push("tap target " + b.width.toFixed(0) + "×" + b.height.toFixed(0) + ": ." + String(el.className).split(" ")[0] + " " + (el.textContent.trim().slice(0, 16) || el.getAttribute("aria-label") || ""));
  }
  for (const el of document.querySelectorAll(".cg-row")) if (el.getBoundingClientRect().height < 55.5) out.push("bet row " + el.getBoundingClientRect().height.toFixed(0) + "px");
  // The pad's keys and LOCK IN, and the bet's stake steppers and CONFIRM (kept on screen while the
  // rows scroll under them), with at least three rows still in view above the stake panel.
  for (const el of document.querySelectorAll(".cg-key, .cg-lock, .cg-step, .cg-stakebtn, .cg-confirm")) {
    const b = el.getBoundingClientRect();
    if (b.bottom > innerHeight + 1) out.push("." + [...el.classList].find((name) => name.startsWith("cg-")) + " below the screen");
  }
  const dock = document.querySelector(".cg-bet__dock");
  if (dock) {
    const rows = [...document.querySelectorAll(".cg-row")];
    const seen = rows.filter((row) => row.getBoundingClientRect().bottom <= dock.getBoundingClientRect().top + 12).length;
    if (seen < Math.min(3, rows.length)) out.push("only " + seen + " bet rows in view above the stake panel");
  }
  for (const el of document.querySelectorAll(".cg-row__num, .cgp-slot__num, .cgp-card__num, .cg-pad__num")) if (el.scrollWidth > el.clientWidth + 2) out.push("number cut: ." + el.className + " " + el.textContent);
  return { problems: out, screen: app?.dataset.screen || "" };
})()`;

test("browser: every Near Thing table scene at 1600 × 900 and phone scene at 360 × 740", { timeout: 600000 }, async (t) => {
  const browser = await launch();
  if (!browser) {
    t.skip("Playwright or Edge is not available");
    return;
  }
  const shots = process.env.CLOSEST_SHOTS || "";
  if (shots) mkdirSync(shots, { recursive: true });
  const server = await serveWeb();
  const base = `http://127.0.0.1:${server.address().port}`;
  const failures = [];
  const watchErrors = (page, list) => {
    page.on("pageerror", (error) => list.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error" && !/404|Failed to load resource/.test(message.text())) list.push(message.text());
    });
  };
  try {
    const tableRuns = [
      ...allNames.filter((name) => name.endsWith("-table")).map((name) => [name, ""]),
      ["closest-lobby-table", "&open=menu&page=help"], ["closest-bet-table", "&open=menu&page=help"], ["closest-reveal-table", "&play=1"], ["closest-lineup-big-table", "&play=1"],
    ];
    const tv = await browser.newContext({ viewport: { width: 1600, height: 900 } });
    for (const [name, extra] of tableRuns) {
      const page = await tv.newPage();
      const errors = [];
      watchErrors(page, errors);
      await page.goto(`${base}/?demo=${name}${extra}`, { waitUntil: "load" });
      await page.waitForFunction(() => globalThis.estateDemo?.ready?.(), null, { timeout: 15000 });
      await page.waitForTimeout(extra.includes("play") ? 2500 : 900);
      const report = await page.evaluate(TABLE_PROBE);
      const label = `table ${name}${extra}`;
      if (errors.length) failures.push(`${label}: ${errors.join("; ")}`);
      if (Math.abs(report.scale - 1) > 0.01) failures.push(`${label}: stage scale ${report.scale}`);
      for (const problem of report.problems) failures.push(`${label}: ${problem}`);
      if (shots) await page.screenshot({ path: join(shots, `table-${name}${extra.replace(/[^a-z0-9]+/gi, "-")}.png`) });
      await page.close();
    }
    await tv.close();
    const phoneRuns = [
      ...allNames.filter((name) => name.endsWith("-phone")).map((name) => [name, ""]),
      ["closest-guess-phone", "&pad=999999999"], ["closest-guess-year-phone", "&pad=1969"], ["closest-guessed-phone", "&pad=4"],
      ["closest-bet-phone", "&chips=0,5&stake=3&stakeSlot=5"], ["closest-bet-8-phone", "&chips=0,7&stake=2&stakeSlot=7"],
      ["closest-guess-phone", "&as=spectator"], ["closest-bet-phone", "&as=spectator"], ["closest-lobby-phone", "&sheet=help"], ["closest-bet-phone", "&sheet=more"],
    ];
    const context = await browser.newContext({ viewport: { width: 360, height: 740 }, deviceScaleFactor: 1, isMobile: true, hasTouch: true });
    for (const [name, extra] of phoneRuns) {
      const page = await context.newPage();
      const errors = [];
      watchErrors(page, errors);
      await page.goto(`${base}/play?demo=${name}${extra}`, { waitUntil: "load" });
      await page.waitForFunction(() => document.querySelector("#app")?.dataset.screen?.startsWith("cg-"), null, { timeout: 15000 });
      await page.waitForTimeout(extra.includes("sheet") ? 900 : 600);
      const report = await page.evaluate(PHONE_PROBE);
      const label = `phone ${name}${extra}`;
      if (errors.length) failures.push(`${label}: ${errors.join("; ")}`);
      for (const problem of report.problems) failures.push(`${label}: ${problem}`);
      if (shots) {
        const tag = extra ? `-${extra.replace(/[^a-z0-9]+/gi, "-").replace(/^-|-$/g, "")}` : "";
        await page.screenshot({ path: join(shots, `phone-${name}${tag}.png`), fullPage: true });
      }
      await page.close();
    }
    await context.close();
  } finally {
    await browser.close();
    server.close();
  }
  assert.deepEqual(failures, []);
});
