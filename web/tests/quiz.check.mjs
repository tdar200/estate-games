// Quiz screens (docs/QUIZ-API.md §1.1, §3, §6, §9): the fixtures in the §3 shape, every fixture and
// demo scene rendered on the table screen and the phone (no undefined / NaN), the right answer never
// on screen before the reveal, the choices hidden until openAt and always A–D in the server's order,
// bets held within betMin–betMax, the clock sync (the lowest round trip wins), the tap timing, the
// socket actions and the sounds. Then, when Playwright and Edge are available, every fixture and
// scene in a real browser: the table at 1920 × 1080 (text floors at 1080p: clue ≥ 48 px, choices
// ≥ 36, board values ≥ 56, category titles ≥ 28, nothing under 24) and the phone at 390 × 844 and
// 360 × 740 (nothing under 14 px, answer buttons ≥ 64 px tall and full width, nothing off screen).
// Set QUIZ_SHOTS=<folder> to save a screenshot of each.
// Run: node --test web/tests/quiz.check.mjs

import assert from "node:assert/strict";
import { createServer } from "node:http";
import { existsSync, mkdirSync, readFileSync, readdirSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, extname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import {
  activeClue, betPresets, betStep, bestSample, choicesOpen, clampBet, clockSample, createClockSync, msUntilOpen, notePaint, pingInterval, pingMessage,
  readProgress, scheduleAt, secondsLeft, stepBet, tapMs,
} from "../shared/quiz-clock.js";
import { LETTERS, canAnswerNow, categoryColor, fmtScore, isQuizState, outcomeFor, phaseLine, playerOf, quizMood, quizSounds, rankings, settingChips, signedPoints } from "../shared/quiz-view.js";
import { QUIZ_SCENES, buildQuizScene, demoClock } from "../shared/quiz-scenes.js";
import { betAmount, betContext, helpSheetHtml, moreSheetHtml, quizRoute, renderQuiz } from "../phone/quiz-screens.js";
import { quizEndConfirmHtml, quizGameHtml, quizHelpOverlayHtml, quizLobbyHtml } from "../table/quiz-table.js";
import { gamePickHtml, gameTypeOf, gtypeTag } from "../shared/poker-view.js";
import { LiveRoom } from "../shared/client.js";

const here = dirname(fileURLToPath(import.meta.url));
const webRoot = resolve(here, "..");
const dir = join(webRoot, "fixtures", "quiz");
const loadFixture = async (name) => JSON.parse(readFileSync(join(dir, `${name}.json`), "utf8"));
const fixtureNames = readdirSync(dir).filter((file) => file.endsWith(".json") && file !== "index.json").map((file) => file.replace(/\.json$/, ""));
const sceneNames = Object.keys(QUIZ_SCENES);
const allNames = [...fixtureNames, ...sceneNames];
const scene = async (name) => (await buildQuizScene(name, loadFixture)).state;

// The fixture list of docs §9.
const DOC_FIXTURES = [
  "quiz-lobby-table", "quiz-lobby-phone", "quiz-intro-table", "quiz-pick-table", "quiz-pick-phone", "quiz-pick-other-phone", "quiz-read-table",
  "quiz-read-phone", "quiz-answer-table", "quiz-answer-phone", "quiz-answered-phone", "quiz-reveal-table", "quiz-reveal-phone", "quiz-gold-table",
  "quiz-bet-table", "quiz-bet-phone", "quiz-roundend-table", "quiz-finalbet-table", "quiz-finalbet-phone", "quiz-finalanswer-table",
  "quiz-finalanswer-phone", "quiz-finalreveal-table", "quiz-finished-table", "quiz-finished-phone",
];
const REVEALED = new Set(["reveal", "finalReveal", "finished"]);

// What each screen shows for a state, on the state's own clock (as the demos do).
function tableHtml(state, now = demoClock(state)) {
  if (state.status === "lobby") return quizLobbyHtml(state, { now, qrUrl: "", aiLevel: "normal" });
  return quizGameHtml(state, { now, introElapsed: 1e6 });
}
function phoneView(state, now = demoClock(state), extra = {}) {
  return { qz: { now, open: choicesOpen(state, now), tapped: null, betDraft: null, aiLevel: null, fresh: "", ...extra }, session: { code: state.code, role: state.viewerId ? "player" : "spectator", playerId: state.viewerId || null } };
}
function phoneHtml(state, now, extra) {
  const out = renderQuiz(state, phoneView(state, now, extra));
  return { ...out, html: `${out.header}${out.banner}${out.main}` };
}
const asSpectator = (state) => ({ ...state, viewerId: null, viewerRole: "spectator", viewerName: "Guest", you: null });
const asTable = (state) => ({ ...state, viewerId: "", viewerRole: "table", viewerName: "Table", you: null });
const text = (html) => html.replace(/<[^>]+>/g, " ").replace(/&amp;/g, "&").replace(/&#39;/g, "'").replace(/&quot;/g, '"').replace(/\s+/g, " ");
const BAD = /undefined|NaN|\[object |null(?![-\w])/;

// ---------------------------------------------------------------------------------------------
// Fixtures

test("fixtures: the 24 of docs §9, each a quiz state in the §3 shape, no secrets before their reveal", async () => {
  for (const name of DOC_FIXTURES) assert.ok(fixtureNames.includes(name), `missing fixture ${name}`);
  const index = JSON.parse(readFileSync(join(dir, "index.json"), "utf8"));
  assert.deepEqual(index.map((entry) => entry.name).sort(), [...fixtureNames].sort(), "index.json lists every fixture");
  for (const name of fixtureNames) {
    const { state } = await loadFixture(name);
    assert.equal(state.game, "quiz", name);
    assert.ok(isQuizState(state), name);
    for (const key of ["code", "status", "hostId", "version", "phase", "now", "phaseEndsAt", "settings", "seatLimit", "round", "rounds", "board", "chooserId", "clue", "bet", "final", "players", "you", "standings", "winnerIds", "credits", "log", "lastEventSeq"]) {
      assert.ok(key in state, `${name}: ${key}`);
    }
    for (const key of ["length", "readSpeed", "answerSeconds"]) assert.ok(key in state.settings, `${name}: settings.${key}`);
    for (const player of state.players) {
      for (const key of ["id", "name", "kind", "status", "score", "correct", "wrong", "isChooser", "lockedIn"]) assert.ok(key in player, `${name}: players[].${key}`);
    }
    if (name.endsWith("-table")) {
      assert.equal(state.viewerRole, "table", name);
      assert.equal(state.you, null, name);
    } else {
      assert.equal(state.viewerRole, "player", name);
      for (const key of ["id", "canPick", "canAnswer", "choice", "canBet", "betMin", "betMax", "bet", "finalChoice", "latencyMs"]) assert.ok(key in state.you, `${name}: you.${key}`);
    }
    if (state.board) {
      assert.equal(state.board.categories.length, 6, name);
      for (const category of state.board.categories) {
        assert.equal(category.cells.length, 5, name);
        for (const cell of category.cells) if (cell.gold) assert.ok(cell.played, `${name}: an unfound gold square is in the state`);
      }
    }
    if (!REVEALED.has(state.phase)) {
      assert.ok(!("answerIndex" in (state.clue || {})), `${name}: the clue's answer before the reveal`);
      assert.ok(!("answerIndex" in (state.final || {})), `${name}: the final's answer before the reveal`);
    }
    if (state.clue?.text) assert.equal(state.clue.choices.length, 4, name);
  }
});

// ---------------------------------------------------------------------------------------------
// Every fixture and scene on both screens

test("render: every fixture and scene on its screen, and table states on a spectator's phone", async () => {
  for (const name of allNames) {
    const state = await scene(name);
    if (name.endsWith("-table")) {
      const html = tableHtml(state);
      assert.ok(html.length > 200, `${name}: table html`);
      assert.doesNotMatch(text(html), BAD, `${name}: table text`);
      assert.doesNotMatch(html, /="undefined"|="NaN"|scaleX\(NaN\)/, `${name}: table attributes`);
      const watch = phoneHtml(asSpectator(state));
      assert.match(watch.screen, /^qz-/, `${name}: spectator screen`);
      assert.doesNotMatch(text(watch.html), BAD, `${name}: spectator phone text`);
    } else {
      const out = phoneHtml(state);
      assert.match(out.screen, /^qz-/, name);
      assert.ok(out.main.length > 50, `${name}: phone main`);
      assert.doesNotMatch(text(out.html), BAD, `${name}: phone text`);
      assert.doesNotMatch(out.html, /="undefined"|="NaN"|scaleX\(NaN\)/, `${name}: phone attributes`);
      // The table screen shows the same moment.
      assert.doesNotMatch(text(tableHtml(asTable(state))), BAD, `${name}: as the table`);
    }
  }
  // The help (with credits), MORE and END GAME sheets and overlays. The credits list carries each of
  // the state's attribution lines (docs §3.1 credits), or our own line when the packs need none.
  const { state } = await loadFixture("quiz-answer-table");
  const OWN_CREDIT = "Questions written and checked for Estate.";
  const withCredits = { ...state, credits: ["Clues adapted from Wikipedia, CC BY-SA 4.0", "Open Trivia <DB> & friends"] };
  for (const candidate of [state, withCredits, { ...state, credits: [] }, { ...state, credits: ["", "  "] }]) {
    const lines = (candidate.credits || []).filter((line) => String(line || "").trim());
    const expected = lines.length ? lines.map((line) => escape(line)) : [OWN_CREDIT];
    for (const [where, html] of [["table help", quizHelpOverlayHtml(candidate)], ["phone help", helpSheetHtml(candidate)]]) {
      assert.match(html, /class="qhow"[\s\S]*class="qcredits"/, where);
      const list = /<ul class="qcredits">([\s\S]*?)<\/ul>/.exec(html)?.[1] || "";
      const items = [...list.matchAll(/<li>([\s\S]*?)<\/li>/g)].map((match) => match[1]);
      assert.deepEqual(items, expected, `${where}: credits ${JSON.stringify(candidate.credits)}`);
    }
  }
  assert.match(quizEndConfirmHtml(state), /END/);
  assert.doesNotMatch(text(moreSheetHtml((await loadFixture("quiz-answer-phone")).state, phoneView(state))), BAD);
});

test("phone routes: each fixture lands on its screen", async () => {
  const expect = {
    "quiz-lobby-phone": "qz-lobby", "quiz-pick-phone": "qz-pick", "quiz-pick-other-phone": "qz-wait", "quiz-read-phone": "qz-read", "quiz-answer-phone": "qz-answer",
    "quiz-answered-phone": "qz-locked", "quiz-reveal-phone": "qz-reveal", "quiz-bet-phone": "qz-bet", "quiz-finalbet-phone": "qz-bet", "quiz-finalanswer-phone": "qz-answer",
    "quiz-finished-phone": "qz-result", "quiz-intro-phone": "qz-intro", "quiz-gold-phone": "qz-gold", "quiz-roundend-phone": "qz-roundend", "quiz-finalintro-phone": "qz-final",
    "quiz-finalread-phone": "qz-read", "quiz-finalanswered-phone": "qz-locked", "quiz-finalreveal-phone": "qz-final-reveal", "quiz-read-open-phone": "qz-answer",
  };
  for (const [name, screen] of Object.entries(expect)) assert.equal(phoneHtml(await scene(name)).screen, screen, name);
  // "NAME is picking" on everyone else's phone: the chooser's name, whoever the fixture has picking.
  const other = await scene("quiz-pick-other-phone");
  const chooser = playerOf(other, other.chooserId);
  assert.ok(chooser && chooser.id !== other.viewerId, "someone else has control");
  assert.ok(text(phoneHtml(other).main).toUpperCase().includes(`${chooser.name.toUpperCase()} IS PICKING`), `"${chooser.name} is picking"`);
  // And on the chooser's own phone, YOUR PICK.
  const mine = await scene("quiz-pick-phone");
  assert.equal(mine.chooserId, mine.viewerId);
  assert.match(text(phoneHtml(mine).html), /YOUR PICK/);
  // A spectator never gets answer buttons.
  assert.doesNotMatch(phoneHtml(asSpectator(await scene("quiz-answer-phone"))).html, /data-qz="answer"/);
});

// ---------------------------------------------------------------------------------------------
// Secrets and timing

test("the right answer is never marked before the reveal, even if a state carried it", async () => {
  const MARKS = /is-right|qz-choice__tick|qzp-answer\b|qz-freveal__answer|RIGHT ANSWER/;
  for (const name of allNames) {
    const state = await scene(name);
    if (REVEALED.has(state.phase)) continue;
    const leaky = { ...state, clue: state.clue ? { ...state.clue, answerIndex: 1, winnerId: "p1" } : state.clue, final: state.final ? { ...state.final, answerIndex: 2 } : state.final };
    for (const candidate of [state, leaky]) {
      assert.doesNotMatch(tableHtml(asTable(candidate)), MARKS, `${name}: table`);
      if (candidate.you) assert.doesNotMatch(phoneHtml(candidate).html, MARKS, `${name}: phone`);
      assert.doesNotMatch(phoneHtml(asSpectator(candidate)).html, MARKS, `${name}: spectator`);
    }
  }
  // At the reveal it is: the right choice on both screens.
  const reveal = await scene("quiz-reveal-table");
  const right = reveal.clue.answerIndex;
  assert.ok(text(/<li class="qz-choice is-right">([\s\S]*?)<\/li>/.exec(tableHtml(reveal))?.[1] || "").includes(`${LETTERS[right]} ${reveal.clue.choices[right]}`), "table: the right choice marked");
  const revealPhone = await scene("quiz-reveal-phone");
  assert.ok(text(/class="qzp-answer"([\s\S]*?)<\/div>/.exec(phoneHtml(revealPhone).html)?.[1] || "").includes(`${LETTERS[revealPhone.clue.answerIndex]} ${revealPhone.clue.choices[revealPhone.clue.answerIndex]}`), "phone: THE ANSWER");
});

test("the choices stay hidden until openAt by the synced clock, then show A–D in the server's order", async () => {
  for (const name of ["quiz-read-table", "quiz-read-phone", "quiz-finalread-table", "quiz-finalread-phone"]) {
    const state = await scene(name);
    const clue = activeClue(state);
    assert.ok(clue && clue.openAt > state.now, `${name}: read before openAt`);
    const words = clue.choices;
    for (const now of [clue.readAt, state.now, clue.openAt - 1]) {
      assert.equal(choicesOpen(state, now), false, `${name} @${now - clue.openAt}`);
      assert.ok(msUntilOpen(state, now) > 0);
      const tableMarkup = tableHtml(asTable(state), now);
      assert.match(tableMarkup, /class="qz-choices is-waiting"[^>]*aria-hidden="true"/, `${name}: four empty places while reading`);
      const table = text(tableMarkup);
      const phone = state.you ?phoneHtml(state, now) : phoneHtml({ ...state, viewerId: state.hostId, viewerRole: "player", you: { id: state.hostId, choice: -1, finalChoice: -1, canAnswer: false } }, now);
      for (const word of words) {
        assert.ok(!table.includes(word), `${name}: "${word}" on the table before openAt`);
        assert.ok(!text(phone.html).includes(word), `${name}: "${word}" on the phone before openAt`);
      }
      assert.doesNotMatch(phone.html, /data-qz="answer"/, `${name}: nothing to tap before openAt`);
      assert.equal(phone.screen, "qz-read");
    }
    // At openAt (still in the read phase: the answer state may not have arrived yet) they show.
    assert.equal(choicesOpen(state, clue.openAt), true);
    assert.equal(msUntilOpen(state, clue.openAt), 0);
    const table = tableHtml(asTable(state), clue.openAt);
    const order = [...table.matchAll(/class="qz-choice__letter">([A-D])<\/span><span class="qz-choice__text">([^<]*)</g)].map((match) => [match[1], match[2]]);
    assert.deepEqual(order, LETTERS.map((letter, index) => [letter, escape(words[index])]), `${name}: table A–D`);
    if (state.you) {
      const phone = phoneHtml(state, clue.openAt);
      assert.equal(phone.screen, "qz-answer", name);
      assertButtons(phone.html, words, name);
    }
  }
  // The answer phase shows them whatever the clock says (the server only enters it at openAt).
  const answer = await scene("quiz-answer-phone");
  assert.equal(choicesOpen(answer, 0), true);
  assertButtons(phoneHtml(answer, 0).html, answer.clue.choices, "answer phase, slow clock");
  assertButtons(phoneHtml(await scene("quiz-finalanswer-phone")).html, (await scene("quiz-finalanswer-phone")).final.choices, "final");
});

const reEscape = (value) => String(value).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

function escape(value) {
  return String(value).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

// Four buttons, A to D, the server's order, the letter and the text in each.
function assertButtons(html, choices, label) {
  const buttons = [...html.matchAll(/<button[^>]*class="qzp-choice"[^>]*data-choice="(\d)"[^>]*>\s*<span class="qzp-choice__letter">([A-D])<\/span><span class="qzp-choice__text">([^<]*)<\/span>/g)];
  assert.equal(buttons.length, 4, `${label}: four buttons`);
  buttons.forEach((match, index) => {
    assert.equal(Number(match[1]), index, `${label}: data-choice order`);
    assert.equal(match[2], LETTERS[index], `${label}: letter`);
    assert.equal(match[3], escape(choices[index]), `${label}: text`);
  });
}

test("locked in and the reveal keep the choices where they were; one tap per clue", async () => {
  const answered = await scene("quiz-answered-phone");
  const locked = phoneHtml(answered);
  assert.equal(locked.screen, "qz-locked");
  assert.ok(answered.you.choice >= 0, "the fixture's viewer has tapped");
  assert.ok(text(locked.main).includes(`${LETTERS[answered.you.choice]} ${answered.clue.choices[answered.you.choice]}`), "locked in shows your choice");
  assert.doesNotMatch(locked.html, /data-qz="answer"/, "no second tap");
  // The phone's own tap locks at once, before the server's state says so.
  const open = await scene("quiz-answer-phone");
  const key = activeClue(open).key;
  assert.equal(phoneHtml(open, undefined, { tapped: { key, choice: 3 } }).screen, "qz-locked");
  assert.equal(phoneHtml(open, undefined, { tapped: { key: "other|c1", choice: 3 } }).screen, "qz-answer", "a tap on another clue does not lock this one");
  assert.equal(canAnswerNow({ ...open, you: { ...open.you, choice: 2 } }), false);
  // The reveal, read from the state's own results (docs §3.3): the winner, who lost, late taps, who
  // didn't tap. Then made-up reveals from the same fixture so every outcome is always exercised,
  // gold squares included (a finder who never taps has no results row but loses the bet).
  const reveal = await scene("quiz-reveal-table");
  assertReveal(reveal, "fixture");
  assertReveal(await scene("quiz-reveal-phone"), "phone fixture");
  for (const [label, variant] of revealVariants(reveal)) assertReveal(variant, label);
});

// What each player should get from a revealed clue, from the results alone (§3.3, §4 scoring).
function expectedOutcome(state, id) {
  const clue = state.clue;
  const row = (clue.results || []).find((result) => result.playerId === id);
  if (row) {
    if (row.late) return { outcome: "late", delta: 0 };
    if (row.playerId === clue.winnerId) return { outcome: "won", delta: row.delta };
    return { outcome: "lost", delta: row.delta };
  }
  const left = playerOf(state, id)?.status === "left";
  if (clue.gold && state.bet?.playerId === id && !clue.winnerId && !left) return { outcome: "lost", delta: -state.bet.amount, noAnswer: true };
  return { outcome: (clue.eligible || []).includes(id) ? "missed" : "none", delta: 0 };
}

function assertReveal(state, label) {
  const clue = state.clue;
  const table = tableHtml(asTable(state));
  const foot = /<div class="qz-clue__foot qz-clue__foot--reveal">([\s\S]*?)<\/div>/.exec(table)?.[1] || "";
  const chips = new Map([...foot.matchAll(/<span class="qz-res is-(\w+)"[^>]*>[\s\S]*?<b>([^<]*)<\/b><span>([^<]*)<\/span><\/span>/g)].map((match) => [match[2], { outcome: match[1], word: match[3] }]));
  for (const player of state.players) {
    const want = expectedOutcome(state, player.id);
    const got = outcomeFor(state, player.id);
    const who = `${label}: ${player.name}`;
    assert.equal(got.outcome, want.outcome, `${who}: outcome`);
    assert.equal(got.delta, want.delta, `${who}: delta`);
    if (want.delta) assert.ok(want.outcome === "won" ? want.delta > 0 : want.delta < 0, `${who}: the sign of the change`);
    // The table: a chip in the reveal line for everyone whose score moved or who tapped late, none
    // for who didn't tap; the plate shows the same change.
    const chip = chips.get(player.name.toUpperCase());
    const plate = new RegExp(`data-key="qzp-${player.id}"[\\s\\S]*?</li>`).exec(table)?.[0] || "";
    if (want.outcome === "won" || want.outcome === "lost") {
      assert.deepEqual(chip, { outcome: want.outcome, word: signedPoints(want.delta) }, `${who}: reveal chip`);
      assert.match(plate, new RegExp(`qz-plate__delta[^>]*>${reEscape(signedPoints(want.delta))}<`), `${who}: plate change`);
      assert.match(plate, new RegExp(`qz-plate__tag">${want.outcome === "won" ? "FIRST" : want.noAnswer ? "NO ANSWER" : "WRONG"}<`), `${who}: plate tag`);
    } else if (want.outcome === "late") {
      assert.equal(chip?.outcome, "late", `${who}: late chip`);
      assert.match(chip.word, /LATE/, `${who}: late chip`);
      assert.doesNotMatch(plate, /qz-plate__delta/, `${who}: no change on a late tap`);
    } else {
      assert.equal(chip, undefined, `${who}: no chip without a tap`);
      assert.doesNotMatch(plate, /qz-plate__delta/, `${who}: no change`);
    }
    // Their own phone says the same.
    if (player.status === "left") continue;
    const own = { ...state, viewerId: player.id, viewerRole: "player", viewerName: player.name, you: { id: player.id, canPick: false, canAnswer: false, choice: (clue.results || []).find((row) => row.playerId === player.id)?.choice ?? -1, canBet: false, betMin: 0, betMax: 0, bet: null, finalChoice: -1, latencyMs: 250 } };
    const card = text(/<div class="qzp-result[^"]*"[^>]*>([\s\S]*?)<\/div>/.exec(phoneHtml(own).main)?.[1] || "").trim();
    const words = {
      won: `RIGHT ${signedPoints(want.delta)}`,
      lost: `${want.noAnswer ? "NO ANSWER" : "WRONG"} ${signedPoints(want.delta)}`,
      late: /BUT LATE NO CHANGE/,
      missed: "NO ANSWER NO CHANGE",
    }[want.outcome];
    if (words instanceof RegExp) assert.match(card, words, `${who}: phone card`);
    else if (words) assert.equal(card, words, `${who}: phone card`);
    // Watching someone else's gold square: what the finder got.
    if (clue.gold && want.outcome === "none") {
      const finder = expectedOutcome(state, state.bet.playerId);
      assert.ok(card.endsWith(finder.delta ? signedPoints(finder.delta) : "NO CHANGE"), `${who}: the finder's change on a watcher's phone (${card})`);
    }
    // And the reveal sound: right for a win, wrong for a loss.
    const before = { ...own, phase: "answer", clue: { ...clue, answerIndex: undefined, results: undefined, winnerId: undefined } };
    const sounds = quizSounds(before, own, { viewerId: player.id }).map((hit) => hit.name);
    if (want.outcome === "won") assert.ok(sounds.includes("quizRight"), `${who}: right sound`);
    if (want.outcome === "lost") assert.ok(sounds.includes("quizWrong") && !sounds.includes("quizRight"), `${who}: wrong sound`);
  }
  // The lead line when nobody won.
  if (!clue.winnerId) {
    const lead = clue.gold ? ((clue.results || []).length ? "WRONG ON A GOLD SQUARE" : "NO ANSWER ON A GOLD SQUARE") : (clue.results || []).length ? "NOBODY RIGHT IN TIME" : "NOBODY ANSWERED";
    assert.ok(text(foot).includes(lead), `${label}: "${lead}"`);
  }
}

// Reveals judged as the engine judges them (internal/quiz judgeClue): taps in time order; the first
// right tap wins the stake; every tap at or after it is late (no change); a wrong tap before it
// loses the stake. A gold square has one eligible player, the finder, and the bet is the stake.
function judge(base, taps, { gold = null } = {}) {
  const clue = base.clue;
  const answer = clue.answerIndex;
  const stake = gold ? gold.amount : clue.value;
  const first = taps.findIndex((tap) => tap.correct);
  const results = taps.map((tap, index) => ({
    playerId: tap.id,
    choice: tap.correct ? answer : (answer + 1 + index) % 4 === answer ? (answer + 2) % 4 : (answer + 1 + index) % 4,
    correct: tap.correct,
    delta: index === first ? stake : first >= 0 && index > first ? 0 : -stake,
    ms: 900 + index * 350,
    late: first >= 0 && index > first,
  }));
  const winnerId = first >= 0 ? taps[first].id : "";
  return {
    ...base,
    chooserId: winnerId || base.chooserId,
    bet: gold ? { playerId: gold.playerId, min: 5, max: Math.max(500, playerOf(base, gold.playerId)?.score || 0), amount: gold.amount } : null,
    clue: { ...clue, gold: Boolean(gold), eligible: gold ? [gold.playerId] : clue.eligible, answered: taps.map((tap) => tap.id), results, winnerId },
    players: base.players.map((player) => ({ ...player, isChooser: player.id === (winnerId || base.chooserId), status: gold?.left && player.id === gold.playerId ? "left" : player.status })),
  };
}

function revealVariants(base) {
  const ids = (base.clue.eligible || []).filter((id) => playerOf(base, id)?.status !== "left");
  assert.ok(ids.length >= 4, "the reveal fixture has at least four players to judge");
  const finder = ids[0];
  const amount = Math.max(5, Math.round(Math.max(500, playerOf(base, finder).score) / 2));
  const variants = [
    ["lost, won, late right, late wrong, missed", judge(base, [{ id: ids[0], correct: false }, { id: ids[1], correct: true }, { id: ids[2], correct: true }, { id: ids[3], correct: false }])],
    ["nobody right in time", judge(base, [{ id: ids[0], correct: false }, { id: ids[1], correct: false }])],
    ["nobody answered", judge(base, [])],
    ["gold: right", judge(base, [{ id: finder, correct: true }], { gold: { playerId: finder, amount } })],
    ["gold: wrong", judge(base, [{ id: finder, correct: false }], { gold: { playerId: finder, amount } })],
    ["gold: no answer", judge(base, [], { gold: { playerId: finder, amount } })],
    ["gold: no answer, finder left", judge(base, [], { gold: { playerId: finder, amount, left: true } })],
  ];
  // Between them every outcome comes up, including the gold finder's loss without a tap.
  const seen = new Set(variants.flatMap(([, state]) => state.players.map((player) => expectedOutcome(state, player.id).outcome + (expectedOutcome(state, player.id).noAnswer ? "+noAnswer" : ""))));
  for (const outcome of ["won", "lost", "late", "missed", "none", "lost+noAnswer"]) assert.ok(seen.has(outcome), `variants cover ${outcome}`);
  return variants;
}

test("the table's answered dots say who, never what", async () => {
  const state = await scene("quiz-answer-table");
  const html = tableHtml(state);
  for (const id of state.clue.answered) assert.match(html, new RegExp(`class="qz-dot" data-key="dot-${id}"`));
  assert.equal((html.match(/class="qz-dot"/g) || []).length, state.clue.answered.length);
  assert.doesNotMatch(html, /data-choice=|qz-choice__who/, "no hint of anyone's choice before the reveal");
  assert.match(html, /qz-count-down/);
});

// ---------------------------------------------------------------------------------------------
// Bets

test("bets: the stepper, MIN / HALF / MAX and BET stay within betMin–betMax", async () => {
  assert.equal(clampBet(2000, 5, 1200), 1200);
  assert.equal(clampBet(-3, 5, 1200), 5);
  assert.equal(clampBet(12.6, 0, 100), 13);
  assert.equal(clampBet("x", 5, 900), 5);
  assert.equal(clampBet(50, 0, 0), 0);
  assert.equal(betStep(5, 300), 10);
  assert.equal(betStep(5, 1200), 50);
  assert.equal(betStep(0, 2100), 100);
  assert.equal(stepBet(5, 1, 5, 1200), 50);
  assert.equal(stepBet(50, -1, 5, 1200), 5);
  assert.equal(stepBet(1190, 1, 5, 1200), 1200);
  assert.equal(stepBet(1200, 1, 5, 1200), 1200);
  assert.equal(stepBet(0, -1, 0, 2100), 0);
  assert.deepEqual(betPresets(5, 1200), { min: 5, half: 600, max: 1200 });
  assert.deepEqual(betPresets(0, 2100), { min: 0, half: 1050, max: 2100 });
  assert.deepEqual(betPresets(5, 8), { min: 5, half: 5, max: 8 });
  for (let min = 0; min <= 20; min += 5) {
    for (const max of [min, min + 7, 500, 1301, 2100]) {
      const presets = betPresets(min, max);
      for (const value of Object.values(presets)) assert.ok(value >= min && value <= Math.max(min, max), `preset ${value} in ${min}–${max}`);
      let value = min;
      for (let press = 0; press < 60; press += 1) {
        value = stepBet(value, 1, min, max);
        assert.ok(value >= min && value <= Math.max(min, max));
      }
      assert.equal(value, Math.max(min, max));
    }
  }
  for (const name of ["quiz-bet-phone", "quiz-finalbet-phone"]) {
    const state = await scene(name);
    const bet = betContext(state);
    assert.equal(bet.min, state.you.betMin, name);
    assert.equal(bet.max, state.you.betMax, name);
    assert.equal(betAmount(state, null), betPresets(bet.min, bet.max).half, `${name}: starts at HALF`);
    assert.equal(betAmount(state, { key: bet.key, amount: 1e9 }), bet.max, `${name}: a draft is clamped`);
    assert.equal(betAmount(state, { key: "elsewhere", amount: 7 }), betPresets(bet.min, bet.max).half, `${name}: another bet's draft is ignored`);
    const html = phoneHtml(state).html;
    for (const label of ["MIN", "HALF", "MAX"]) assert.match(html, new RegExp(`<span>${label}</span>`), `${name}: ${label}`);
    const amounts = [...html.matchAll(/data-amount="(-?\d+)"/g)].map((match) => Number(match[1]));
    assert.ok(amounts.length >= 4, name);
    for (const amount of amounts) assert.ok(amount >= bet.min && amount <= bet.max, `${name}: ${amount} out of range`);
    const atMax = phoneHtml(state, undefined, { betDraft: { key: bet.key, amount: bet.max } }).html;
    assert.match(atMax, /data-dir="1"[^>]*disabled/, `${name}: + stops at the max`);
    const atMin = phoneHtml(state, undefined, { betDraft: { key: bet.key, amount: bet.min } }).html;
    assert.match(atMin, /data-dir="-1"[^>]*disabled/, `${name}: − stops at the min`);
  }
  // Nobody else gets a stepper; the table shows the range, not a control.
  assert.equal(betContext(await scene("quiz-finalintro-phone")), null);
  // The range is the state's bet.min–bet.max, with thousands separators.
  const betTable = await scene("quiz-bet-table");
  assert.ok(text(tableHtml(betTable)).includes(`${fmtScore(betTable.bet.min)} TO ${fmtScore(betTable.bet.max)}`), "the bet range from the state");
  assert.ok(text(tableHtml({ ...betTable, bet: { ...betTable.bet, min: 5, max: 1200 } })).includes("5 TO 1,200"), "1,200 with a separator");
});

// ---------------------------------------------------------------------------------------------
// Clock sync and tap timing

test("clock: offset = now + rtt/2 − received; the lowest round trip of the last 8 wins", () => {
  assert.deepEqual(clockSample(1000, 5000, 1100), { rtt: 100, offset: 3950 });
  let local = 10_000;
  const clock = createClockSync({ clientNow: () => local });
  assert.equal(clock.synced(), false);
  clock.seen(50_000, 10_000);
  assert.equal(clock.offset(), 40_000, "before any pong a state's now is the fallback");
  // Server is 40 000 ms ahead. Sample 1: 200 ms round trip, server answered 150 ms in (asymmetric).
  clock.pong({ type: "pong", t: 10_000, now: 50_150 }, 10_200);
  assert.equal(clock.offset(), 40_050);
  // Sample 2: a 20 ms round trip, symmetric: exact.
  clock.pong({ type: "pong", t: 11_000, now: 51_010 }, 11_020);
  assert.equal(clock.offset(), 40_000);
  assert.equal(clock.rtt(), 20);
  // Slower samples never replace it.
  clock.pong({ type: "pong", t: 12_000, now: 52_400 }, 12_500);
  assert.equal(clock.offset(), 40_000);
  local = 20_000;
  assert.equal(clock.now(), 60_000);
  // Bad pongs are ignored: no t (a bare ping's pong), negative or absurd round trips.
  assert.equal(clock.pong({ type: "pong", now: 1 }), null);
  assert.equal(clock.pong({ type: "pong", t: 30_000, now: 1 }, 29_000), null);
  assert.equal(clock.pong({ type: "pong", t: 0, now: 1 }, 90_000), null);
  assert.equal(clock.samples.length, 3);
  // Only the last 8 count: the 20 ms sample ages out.
  for (let index = 0; index < 8; index += 1) clock.pong({ type: "pong", t: 13_000 + index * 1000, now: 53_060 + index * 1000 }, 13_100 + index * 1000);
  assert.equal(clock.samples.length, 8);
  assert.equal(clock.rtt(), 100);
  assert.equal(clock.offset(), 40_010);
  assert.equal(bestSample([{ rtt: 30, offset: 1 }, { rtt: 30, offset: 2 }, { rtt: Number.NaN, offset: 3 }]).offset, 1, "ties keep the first");
  // Each ping carries t and the best rtt; the cadence is 2 s while a quiz plays.
  assert.deepEqual(pingMessage(clock, 777), { type: "ping", t: 777, rtt: 100 });
  assert.deepEqual(pingMessage(createClockSync(), 5), { type: "ping", t: 5 });
  assert.equal(pingInterval({ game: "quiz", status: "playing" }), 2000);
  assert.equal(pingInterval({ game: "quiz", status: "lobby" }), 15000);
  assert.equal(pingInterval({ game: "ludo", status: "playing" }), 15000);
  clock.reset();
  assert.equal(clock.synced(), false);
});

test("timing: ms = tap.timeStamp − paint time; the choices open on schedule, late states at once", () => {
  assert.equal(tapMs(1834.6, 1000.2), 834);
  assert.equal(tapMs(1000, 1200), 0, "never negative");
  assert.equal(tapMs(1500, null, 1000), 500, "no paint time yet: from when they were shown");
  assert.equal(tapMs(Number.NaN, 1000), null, "unmeasurable: sent without ms, never as 0");
  assert.equal(tapMs(1500, null, null), null);
  // The paint time is the frame after the choices are un-hidden.
  const frames = [];
  let painted = null;
  notePaint((at) => { painted = at; }, { raf: (fn) => frames.push(fn), clock: () => 4242 });
  assert.equal(painted, null);
  frames.shift()();
  assert.equal(painted, 4242);
  // scheduleAt: a fake clock and timers. setTimeout firing early re-arms for the rest.
  let now = 1000;
  const timers = [];
  const opts = { now: () => now, setTimer: (fn, ms) => timers.push({ fn, ms }) && timers.length, clearTimer: () => {} };
  let fired = 0;
  scheduleAt(1500, () => { fired += 1; }, opts);
  assert.equal(fired, 0);
  assert.equal(timers.at(-1).ms, 500);
  now = 1490;
  timers.at(-1).fn();
  assert.equal(fired, 0, "early timer");
  assert.equal(timers.at(-1).ms, 10);
  now = 1500;
  timers.at(-1).fn();
  assert.equal(fired, 1);
  // A state that arrives after openAt: in the same call, no timer.
  const before = timers.length;
  scheduleAt(900, () => { fired += 1; }, opts);
  assert.equal(fired, 2);
  assert.equal(timers.length, before);
  // Cancelled: never fires.
  const cancel = scheduleAt(5000, () => { fired += 1; }, opts);
  cancel();
  now = 6000;
  timers.at(-1).fn();
  assert.equal(fired, 2);
  // Bars and countdowns.
  assert.equal(readProgress({ readAt: 0, openAt: 1000 }, 250), 0.25);
  assert.equal(readProgress({ readAt: 0, openAt: 1000 }, 5000), 1);
  assert.equal(secondsLeft(10_000, 7_001), 3);
  assert.equal(secondsLeft(0, 7_001), 0);
});

test("socket actions: {type:action, action, id}; actionResult settles; errors reject; POST when closed", async () => {
  const room = new LiveRoom({}, { code: "QZ7KM", token: "t", onState() {} });
  assert.equal(room.send({ type: "answer", clue: 18, choice: 1, ms: 834 }), false, "no socket: the caller POSTs");
  const sent = [];
  room.socket = { readyState: WebSocket.OPEN, send: (raw) => sent.push(JSON.parse(raw)) };
  const ok = room.send({ type: "answer", clue: 18, choice: 1, ms: 834 });
  assert.deepEqual(sent[0], { type: "action", action: { type: "answer", clue: 18, choice: 1, ms: 834 }, id: 1 });
  room.settle({ type: "actionResult", id: 1, ok: true });
  assert.equal(await ok, true);
  const refused = room.send({ type: "answer", clue: 18, choice: 2, ms: 900 });
  room.settle({ type: "actionResult", id: 2, ok: false, error: "you have already answered" });
  await assert.rejects(refused, /already answered/);
  const lost = room.send({ type: "bet", amount: 5 });
  room.failPending("The connection dropped.");
  await assert.rejects(lost, /dropped/);
  assert.equal(room.pending.size, 0);
  // A quiz that plays pings every 2 s; the state's now seeds the clock.
  room.socket = null;
  room.accept({ game: "quiz", status: "playing", version: 9, now: Date.now() + 5000 });
  assert.equal(room.pingMs, 2000);
  assert.ok(room.clock.offset() > 4000);
  room.accept({ game: "quiz", status: "finished", version: 10, now: Date.now() });
  assert.equal(room.pingMs, 15000);
  clearInterval(room.pingTimer);
});

// ---------------------------------------------------------------------------------------------
// Lobby, picker, words, sounds

test("lobby: QUIZ tile, seats, ADD COMPUTER + level, setting chips, START; phones only", async () => {
  const tv = tableHtml(await scene("quiz-lobby-table"));
  assert.match(tv, /data-arg="quiz" aria-pressed="true"/, "QUIZ is the picked tile");
  assert.match(tv, /data-cmd="qz-add-ai"[\s\S]*?ADD COMPUTER/);
  assert.match(tv, /data-cmd="qz-ai-level"/);
  for (const key of ["length", "readSpeed", "answerSeconds"]) assert.match(tv, new RegExp(`data-cmd="qz-setting"[^>]*data-arg="${key}`));
  assert.match(tv, /data-cmd="start"/);
  assert.match(text(tv), /phone/i);
  assert.doesNotMatch(tv, /ADD KEYBOARD|data-cmd="add-seat"|press A · Enter/, "no keyboard or controller seats");
  const phone = phoneHtml(await scene("quiz-lobby-phone")).html;
  assert.match(phone, /qz-switch-game/);
  assert.match(phone, /START/);
  assert.match(phone, /data-cmd="qz-setting"/);
  assert.equal(settingChips((await scene("quiz-lobby-table"))).length, 3);
  // The picker has four tiles everywhere; lists tag quiz rooms.
  assert.match(gamePickHtml("quiz"), /gpick--4[\s\S]*data-arg="quiz" aria-pressed="true"/);
  assert.equal(gameTypeOf({ gameType: "quiz" }), "quiz");
  assert.equal(gameTypeOf({ game: "quiz" }), "quiz");
  assert.match(gtypeTag("quiz"), /gtype--quiz[\s\S]*QUIZ/);
});

test("table screen: every phase draws its own body; plates mark the chooser and who is locked in", async () => {
  const bodies = {
    "quiz-intro-table": /qz-intro__cats/, "quiz-pick-table": /qz-board/, "quiz-gold-table": /qz-goldcard/, "quiz-bet-table": /qz-goldcard--bet/,
    "quiz-read-table": /qz-bar--read/, "quiz-answer-table": /qz-choices/, "quiz-reveal-table": /is-reveal/, "quiz-roundend-table": /qz-ranks/,
    "quiz-finalintro-table": /qz-final-intro/, "quiz-finalbet-table": /qz-betsin/, "quiz-finalread-table": /qz-bar--read/, "quiz-finalanswer-table": /qz-choices/,
    "quiz-finalreveal-table": /qz-frs/, "quiz-finished-table": /qz-done[\s\S]*data-cmd="rematch"[\s\S]*data-cmd="new-table"/,
  };
  for (const [name, pattern] of Object.entries(bodies)) assert.match(tableHtml(await scene(name)), pattern, name);
  // The chooser's plate is marked; played cells are empty, open ones show their value.
  const pickState = await scene("quiz-pick-table");
  const pick = tableHtml(pickState);
  assert.match(pick, new RegExp(`class="qz-plate is-chooser" data-key="qzp-${pickState.chooserId}"`), "the chooser's plate");
  assert.equal((pick.match(/class="qz-plate[^"]*\bis-chooser\b/g) || []).length, 1, "one chooser");
  const cells = pickState.board.categories.flatMap((category) => category.cells);
  const values = (html) => (html.match(/class="qz-cell__value"/g) || []).length;
  assert.equal(values(pick), cells.filter((cell) => !cell.played).length, "played cells are empty");
  const playOne = (state) => {
    let done = false;
    const categories = state.board.categories.map((category) => ({ ...category, cells: category.cells.map((cell) => (!done && !cell.played && (done = true) ? { ...cell, played: true } : cell)) }));
    return { ...state, board: { ...state.board, categories } };
  };
  if (cells.some((cell) => !cell.played)) assert.equal(values(tableHtml(playOne(pickState))), values(pick) - 1, "one more played cell, one fewer value");
  // Locked-in plates: exactly who has tapped (docs §3.3 answered), never more.
  const answerState = await scene("quiz-answer-table");
  const answer = tableHtml(answerState);
  const lockedIds = [...answer.matchAll(/class="qz-plate[^"]*\bis-locked\b[^"]*" data-key="qzp-([^"]+)"/g)].map((match) => match[1]);
  assert.deepEqual(lockedIds.sort(), [...answerState.clue.answered].sort(), "locked-in plates");
  // The intro reveals the categories one by one: each later card waits longer.
  const intro = quizGameHtml(await scene("quiz-intro-table"), { now: (await scene("quiz-intro-table")).now, introElapsed: 0 });
  const delays = [...intro.matchAll(/<li style="--d:(-?\d+)ms/g)].map((match) => Number(match[1]));
  assert.equal(delays.length, 6);
  for (let index = 1; index < 6; index += 1) assert.ok(delays[index] > delays[index - 1]);
  // The final reveal goes from the lowest score: before → after for the revealed ones.
  const freveal = await scene("quiz-finalreveal-table");
  const frHtml = tableHtml(freveal);
  const rows = frHtml.match(/class="qz-fr(?: [^"]*)?"/g) || [];
  assert.equal(rows.length, freveal.final.order.length);
  const at = freveal.final.order.map((id) => frHtml.indexOf(`data-key="fr-${id}"`));
  for (let index = 0; index < at.length; index += 1) {
    assert.ok(at[index] >= 0, `row for ${freveal.final.order[index]}`);
    if (index) assert.ok(at[index] > at[index - 1], "rows in the order the server reveals them");
  }
  const finalResults = new Map((freveal.final.results || []).map((result) => [result.playerId, result]));
  const startScores = freveal.final.order.map((id) => finalResults.get(id)?.before ?? playerOf(freveal, id).score);
  for (let index = 1; index < startScores.length; index += 1) assert.ok(startScores[index] >= startScores[index - 1], `lowest score first: ${startScores.join(", ")}`);
  for (const result of freveal.final.results || []) {
    const row = new RegExp(`data-key="fr-${result.playerId}"[\\s\\S]*?</li>`).exec(frHtml)?.[0] || "";
    assert.ok(row.includes(`<s>${fmtScore(result.before)}</s> ${fmtScore(result.after)}`), `${result.playerId}: before → after`);
    assert.ok(row.includes(signedPoints(result.correct ? result.bet : -result.bet)), `${result.playerId}: the bet won or lost`);
  }
  // Finished: places with ties shared.
  const done = await scene("quiz-finished-table");
  assert.equal(rankings(done)[0].player.id, done.winnerIds[0]);
});

test("words and colours: the board's category bars use the property-group colours; names stay original", async () => {
  const seen = new Set([0, 1, 2, 3, 4, 5].map((index) => categoryColor(index).bg));
  assert.equal(seen.size, 6);
  // Names the screens must not show (CONTRIBUTING: keep names original).
  const banned = /jeopardy|daily double|final jeopardy|who wants|millionaire|this is jeopardy/i;
  for (const name of allNames) {
    const state = await scene(name);
    assert.doesNotMatch(tableHtml(state.viewerRole === "table" ? state : asTable(state)), banned, name);
    if (state.you) assert.doesNotMatch(phoneHtml(state).html, banned, name);
  }
  for (const file of ["table/quiz-table.js", "phone/quiz-screens.js", "phone/quiz-app.js", "shared/quiz-view.js", "shared/quiz.css"]) {
    assert.doesNotMatch(readFileSync(join(webRoot, file), "utf8"), banned, file);
  }
  assert.equal(phaseLine(await scene("quiz-pick-table")), phaseLine(await scene("quiz-pick-table")));
});

test("sounds: gold, the reveal for you, others' lock-ins; thinking music through the final", async () => {
  const names = (prev, next, viewerId) => quizSounds(prev, next, { viewerId }).map((hit) => hit.name);
  const pick = await scene("quiz-pick-table");
  const gold = await scene("quiz-gold-table");
  assert.ok(names(pick, gold).includes("quizGold"));
  // The reveal for you: whoever won hears right, whoever lost points hears wrong (from the
  // fixture's own results; every outcome's sound is also checked on made-up reveals above).
  const answered = await scene("quiz-answered-phone");
  const reveal = await scene("quiz-reveal-phone");
  const viewAs = (state, id) => ({ ...state, viewerId: id, you: { ...state.you, id, choice: (state.clue?.results || []).find((row) => row.playerId === id)?.choice ?? -1 } });
  if (reveal.clue.winnerId) assert.ok(names(viewAs(answered, reveal.clue.winnerId), viewAs(reveal, reveal.clue.winnerId), reveal.clue.winnerId).includes("quizRight"), "the winner hears right");
  const loser = (reveal.clue.results || []).find((row) => !row.late && row.delta < 0);
  if (loser) {
    const heard = names(viewAs(answered, loser.playerId), viewAs(reveal, loser.playerId), loser.playerId);
    assert.ok(heard.includes("quizWrong") && !heard.includes("quizRight"), "a wrong tap hears wrong");
  }
  // Others' taps tick; your own does not tick again (it clicked when you tapped).
  const open = await scene("quiz-answer-table");
  const [viewer, someone] = open.players.map((player) => player.id);
  const none = { ...open, clue: { ...open.clue, answered: [] } };
  assert.ok(names(none, { ...none, clue: { ...none.clue, answered: [someone] } }, viewer).includes("quizLock"), "someone else locked in");
  assert.ok(!names(none, { ...none, clue: { ...none.clue, answered: [viewer] } }, viewer).includes("quizLock"), "your own tap clicks on tap, not again");
  assert.equal(quizMood(await scene("quiz-finalbet-table")), "thinking");
  assert.equal(quizMood(await scene("quiz-finalanswer-table")), "thinking");
  assert.equal(quizMood(await scene("quiz-answer-table")), "play");
  const sound = readFileSync(join(webRoot, "shared", "sound.js"), "utf8");
  for (const name of ["quizOpen", "quizLock", "quizRight", "quizWrong", "quizGold"]) assert.match(sound, new RegExp(`"${name}"`), name);
});

test("wiring: both pages load quiz.css; the table has its screen; phones only for seats", () => {
  const table = readFileSync(join(webRoot, "table", "index.html"), "utf8");
  const phone = readFileSync(join(webRoot, "phone", "index.html"), "utf8");
  assert.match(table, /href="\/shared\/quiz\.css"/);
  assert.match(phone, /href="\/shared\/quiz\.css"/);
  assert.match(table, /id="quiz-screen"/);
  const css = readFileSync(join(webRoot, "shared", "quiz.css"), "utf8");
  assert.doesNotMatch(css, /@import|url\(\s*["']?https?:/, "no outside fonts or files (CSP)");
  // Table CSS: nothing under 20 stage px (24 px at 1080p) in the .qz-tv rules.
  for (const block of css.split("/* ============================================================== Phone")[0].matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    const selector = block[1].trim();
    if (!/\.qz-|\.qhow|\.qcredits/.test(selector) || /\.app\b/.test(selector)) continue;
    for (const size of block[2].matchAll(/font-size:\s*(\d+(?:\.\d+)?)px/g)) assert.ok(Number(size[1]) >= 20, `${selector}: ${size[1]}px`);
  }
  // Phone CSS: nothing under 14 px.
  for (const block of css.split("/* ============================================================== Phone")[1].matchAll(/([^{}]+)\{([^}]*)\}/g)) {
    for (const size of block[2].matchAll(/font-size:\s*(\d+(?:\.\d+)?)px/g)) assert.ok(Number(size[1]) >= 14, `${block[1].trim()}: ${size[1]}px`);
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

// Every visible piece of text, its size in screen px (the table stage is scaled), plus the named
// floors; anything off screen or spilling out of its box.
// Not text anyone reads: the picker tiles' card art (a poker "K"/"A") and screen-reader-only labels.
const SKIP_JS = `const SKIP = ".gtile__art, .sr-only";`;

const TABLE_PROBE = `(() => {
  ${SKIP_JS}
  const m =/scale\\(([\\d.]+)\\)/.exec(document.querySelector("#stage")?.style.transform || "");
  const scale = m ? Number(m[1]) : 1;
  const out = [];
  const roots = [document.querySelector("#quiz-screen"), ...document.querySelectorAll(".qz-tvhelp")].filter(Boolean);
  const visible = (el) => { const s = getComputedStyle(el); if (s.visibility === "hidden" || s.display === "none" || Number(s.opacity) === 0) return false; const b = el.getBoundingClientRect(); return b.width > 0 && b.height > 0; };
  for (const root of roots) {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    for (let node; (node = walker.nextNode());) {
      const words = node.textContent.trim();
      if (!words) continue;
      const el = node.parentElement;
      if (!visible(el) || el.closest(SKIP)) continue;
      const size = parseFloat(getComputedStyle(el).fontSize) * scale;
      if (size < 23.9) out.push("text " + size.toFixed(1) + "px: ." + String(el.className).split(" ")[0] + ' "' + words.slice(0, 24) + '"');
    }
  }
  const floors = [[".qz-clue__text", 48], [".qz-choice__text", 36], [".qz-cell__value", 56], [".qz-cat__title", 28]];
  for (const [selector, min] of floors) for (const el of document.querySelectorAll("#quiz-screen " + selector)) {
    if (!visible(el)) continue;
    const size = parseFloat(getComputedStyle(el).fontSize) * scale;
    if (size < min - 0.1) out.push(selector + " " + size.toFixed(1) + "px < " + min);
  }
  for (const el of document.querySelectorAll("#quiz-screen *")) {
    if (!visible(el)) continue;
    const b = el.getBoundingClientRect();
    if (b.right > innerWidth + 1 || b.bottom > innerHeight + 1 || b.left < -1 || b.top < -1) { out.push("off screen: ." + String(el.className).split(" ")[0]); break; }
  }
  // Boxes must hold their content both ways; a line of text only across (a tall glyph may poke
  // a pixel or two over its own line box without anything spilling on screen).
  const boxes = ".qz-clue, .qz-board, .qz-body, .qz-done, .qz-tv__lobby, .qz-choice, .qz-plate, .qz-cell, .qz-fr";
  // Category titles are boxes too: a title too tall for its card is cut off by the card. The phase
  // line in the header must not lose words to its ellipsis.
  const lines = ".qz-clue__text, .qz-choice__text, .qz-cat__title, .qz-plate__name, .qz-line b";
  for (const el of document.querySelectorAll("#quiz-screen :is(" + boxes + ", " + lines + ")")) {
    if (!visible(el)) continue;
    const tall = el.matches(boxes + ", .qz-cat__title") && el.scrollHeight > el.clientHeight + 2;
    if (tall || el.scrollWidth > el.clientWidth + 2) out.push("spills: ." + String(el.className).split(" ")[0] + " " + el.scrollWidth + "×" + el.scrollHeight + " in " + el.clientWidth + "×" + el.clientHeight);
  }
  // "Click or press any key for sound" never covers the quiz: it docks in the header's free room.
  const pill = document.querySelector("#sound-pill");
  const head = document.querySelector("#quiz-screen .pk-tv__head");
  if (pill && visible(pill)) {
    const p = pill.getBoundingClientRect();
    const solid = "button, svg, img, input, .qz-cell, .qz-plate, .qz-cat, .qz-clue, .qz-goldcard, .qz-done, .qz-line, .qz-timer, .table-code, .gtile, .qz-seat, .qr-card";
    for (const el of document.querySelectorAll("#quiz-screen *")) {
      if (!visible(el) || el === head || el.contains(head)) continue;
      const own = [...el.childNodes].some((node) => node.nodeType === 3 && node.textContent.trim());
      if (!own && !el.matches(solid)) continue;
      const b = el.getBoundingClientRect();
      if (b.left < p.right - 1 && b.right > p.left + 1 && b.top < p.bottom - 1 && b.bottom > p.top + 1) { out.push("sound pill covers ." + String(el.className.baseVal ?? el.className).split(" ")[0]); break; }
    }
    if (p.top < -1 || p.right > innerWidth + 1) out.push("sound pill off screen");
  }
  return { problems: out, scale, pill: Boolean(pill && visible(pill)) };
})()`;

const PHONE_PROBE = `(() => {
  ${SKIP_JS}
  const out = [];
  const app = document.querySelector("#app");
  const visible = (el) => { const s = getComputedStyle(el); if (s.visibility === "hidden" || s.display === "none" || Number(s.opacity) === 0) return false; const b = el.getBoundingClientRect(); return b.width > 0 && b.height > 0; };
  const roots = [app, ...[...document.querySelectorAll("dialog[open]")]];
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
  const top = document.querySelector(".qzp-top, .qzp-logo");
  if (top && top.getBoundingClientRect().height < 36) out.push("top bar squashed to " + top.getBoundingClientRect().height.toFixed(0) + "px");
  const main = document.querySelector(".qzp");
  for (const button of document.querySelectorAll(".qzp-choice:not(.is-static)")) {
    const b = button.getBoundingClientRect();
    if (b.height < 64) out.push("answer button " + b.height.toFixed(0) + "px tall");
    if (main && b.width < main.getBoundingClientRect().width - 2) out.push("answer button not full width");
    if (b.bottom > innerHeight + 1) out.push("answer button below the screen");
  }
  for (const el of document.querySelectorAll(".qzp-cell, .qzp-step, .qzp-preset, .qzp-betgo")) {
    const b = el.getBoundingClientRect();
    if (el.tagName === "BUTTON" && b.height < 44) out.push("tap target " + b.height.toFixed(0) + "px: ." + el.className.split(" ")[0]);
  }
  return { problems: out, screen: app?.dataset.screen || "" };
})()`;

test("browser: every quiz fixture and scene on the table (1920 × 1080) and the phone (390 × 844, 360 × 740)", { timeout: 600000 }, async (t) => {
  const browser = await launch();
  if (!browser) {
    t.skip("Playwright or Edge is not available");
    return;
  }
  const shots = process.env.QUIZ_SHOTS || "";
  if (shots) mkdirSync(shots, { recursive: true });
  const server = await serveWeb();
  const base = `http://127.0.0.1:${server.address().port}`;
  const failures = [];
  let pillSeen = false;
  const watchErrors = (page, list) => {
    page.on("pageerror", (error) => list.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error" && !/404|Failed to load resource/.test(message.text())) list.push(message.text());
    });
  };
  try {
    const tableRuns = [...allNames.filter((name) => name.endsWith("-table")).map((name) => [name, ""]), ["quiz-lobby-table", "&open=menu&page=help"], ["quiz-answer-table", "&open=menu&page=help"]];
    const tv = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
    for (const [name, extra] of tableRuns) {
      const page = await tv.newPage();
      const errors = [];
      watchErrors(page, errors);
      await page.goto(`${base}/?demo=${name}${extra}`, { waitUntil: "load" });
      await page.waitForFunction(() => globalThis.estateDemo?.ready?.(), null, { timeout: 15000 });
      await page.waitForTimeout(name.includes("intro") ? 5000 : 1200);
      const report = await page.evaluate(TABLE_PROBE);
      const label = `table ${name}${extra}`;
      if (errors.length) failures.push(`${label}: ${errors.join("; ")}`);
      if (Math.abs(report.scale - 1.2) > 0.01) failures.push(`${label}: stage scale ${report.scale}`);
      pillSeen ||= report.pill;
      for (const problem of report.problems) failures.push(`${label}: ${problem}`);
      if (shots) await page.screenshot({ path: join(shots, `table-${name}${extra ? "-help" : ""}.png`) });
      await page.close();
    }
    await tv.close();
    // The browser starts silent, so the sound pill shows; if it never did, its placement went unchecked.
    if (!pillSeen) failures.push("table: the sound pill never showed, so its placement was not checked");
    const phoneRuns = [
      ...allNames.filter((name) => name.endsWith("-phone")).map((name) => [name, ""]),
      ["quiz-answer-phone", "&as=spectator"], ["quiz-pick-phone", "&as=spectator"], ["quiz-lobby-phone", "&sheet=help"], ["quiz-answer-phone", "&sheet=more"],
    ];
    for (const viewport of [{ width: 390, height: 844 }, { width: 360, height: 740 }]) {
      const context = await browser.newContext({ viewport, deviceScaleFactor: 1, isMobile: true, hasTouch: true });
      for (const [name, extra] of phoneRuns) {
        const page = await context.newPage();
        const errors = [];
        watchErrors(page, errors);
        await page.goto(`${base}/play?demo=${name}${extra}`, { waitUntil: "load" });
        await page.waitForFunction(() => document.querySelector("#app")?.dataset.screen?.startsWith("qz-"), null, { timeout: 15000 });
        await page.waitForTimeout(extra.includes("sheet") ? 900 : 700);
        const report = await page.evaluate(PHONE_PROBE);
        const label = `phone ${viewport.width} ${name}${extra}`;
        if (errors.length) failures.push(`${label}: ${errors.join("; ")}`);
        for (const problem of report.problems) failures.push(`${label}: ${problem}`);
        if (shots) {
          const tag = extra ? `-${extra.replace(/[^a-z]+/gi, "-").replace(/^-|-$/g, "")}` : "";
          await page.screenshot({ path: join(shots, `phone${viewport.width}-${name}${tag}.png`), fullPage: report.screen === "qz-lobby" || report.screen === "qz-result" });
        }
        await page.close();
      }
      await context.close();
    }
  } finally {
    await browser.close();
    server.close();
  }
  assert.deepEqual(failures, []);
});

test("help: the how-to-play text matches the settings (answer seconds)", async () => {
  const state = await scene("quiz-lobby-table");
  assert.match(helpSheetHtml({ ...state, settings: { ...state.settings, answerSeconds: 12 } }), /12 seconds/);
  assert.equal(quizRoute(null), "qz-wait");
});
