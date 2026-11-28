// Unit checks for the pure shared modules (insights.js, format.js) against hand-built states and
// the generated fixtures. Run: node web/tests/shared.check.mjs
// (Named *.check.mjs so `node --test` in the repo root doesn't pick it up.)

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { EstateClient, LiveRoom } from "../shared/client.js";
import * as fmt from "../shared/format.js";
import * as ins from "../shared/insights.js";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (name) => JSON.parse(readFileSync(join(here, "..", "fixtures", `${name}.json`), "utf8"));
const BOARD = fixture("roll").state.board;

// Build a state from an ownership table: { index: [ownerId, houses, mortgaged] }.
function makeState(own, players, extra = {}) {
  const properties = {};
  for (const space of BOARD) {
    if (["property", "railroad", "utility"].includes(space.type)) properties[space.index] = { ownerId: null, mortgaged: false, houses: 0 };
  }
  for (const [index, [ownerId, houses = 0, mortgaged = false]] of Object.entries(own)) properties[index] = { ownerId, houses, mortgaged };
  return { board: BOARD, properties, players, bank: { houses: 32, hotels: 12 }, phase: "awaiting_roll", status: "playing", log: [], ...extra };
}

const P = (id, name, cash, position, extra = {}) => ({ id, name, cash, position, color: "#2F6FE0", inJail: false, bankrupt: false, jailCards: 0, ...extra });

// The design's mid-game: Robin has brown (2 houses each) and orange (3 each); Alpha the red set
// with hotels on Galaxy; Sam two railroads; Maya Power Station.
const designOwn = {
  1: ["t", 2], 3: ["t", 2], 16: ["t", 3], 18: ["t", 3], 19: ["t", 3],
  6: ["a"], 8: ["a"], 9: ["a"], 21: ["a", 4], 23: ["a", 4], 24: ["a", 5],
  5: ["s"], 15: ["s"], 26: ["s"], 12: ["m"],
};
const designPlayers = [P("t", "Robin", 1240, 19), P("a", "Alpha", 860, 7), P("s", "Sam", 410, 33), P("m", "Maya", 1520, 12)];

test("format: money, signs, ordinals, lists", () => {
  assert.equal(fmt.money(1240), "$1,240");
  assert.equal(fmt.money(-260), "−$260");
  assert.equal(fmt.signedMoney(30), "+$30");
  assert.equal(fmt.signedMoney(-1100), "−$1,100");
  assert.equal(fmt.ordinal(1), "1st");
  assert.equal(fmt.ordinal(2), "2nd");
  assert.equal(fmt.ordinal(11), "11th");
  assert.equal(fmt.ordinal(23), "23rd");
  assert.equal(fmt.listNames(["A", "B", "C"]), "A, B and C");
  assert.equal(fmt.escapeHtml('<b a="x">&'), "&lt;b a=&quot;x&quot;&gt;&amp;");
  assert.equal(fmt.pct(8 / 36), "22%");
});

test("format: parseLog turns engine lines into feed items", () => {
  const state = makeState(designOwn, designPlayers);
  const rent = fmt.parseLog({ text: "Sam paid $30 (Begonia Road rent)" }, state);
  assert.equal(rent.kind, "rent");
  assert.equal(rent.text, "Sam paid Robin rent on Begonia Road");
  assert.equal(rent.amount, 30);
  assert.equal(rent.tone, "pos");
  assert.equal(rent.playerId, "s");
  assert.equal(rent.otherId, "t");
  const buy = fmt.parseLog({ text: "Robin bought Coral Street for $260" }, state);
  assert.deepEqual([buy.kind, buy.text, buy.amount, buy.tone], ["buy", "Robin bought Coral Street", -260, "neg"]);
  assert.equal(fmt.parseLog({ text: "Alpha was sent to jail" }, state).kind, "jail");
  assert.equal(fmt.parseLog({ text: "Robin rolled 5 + 3 = 8" }, state).text, "Robin rolled 5 + 3");
  assert.equal(fmt.parseLog({ text: "Maya's turn" }, state).kind, "turn");
  assert.equal(fmt.parseLog({ text: "Robin paid $200 (Stamp Duty)" }, state).amount, -200);
  assert.equal(fmt.parseLog({ text: "Robin landed on Headlines" }, state).kind, "land");
  const payday = fmt.parseLog({ text: "Robin passed PAYDAY and collected $200" }, state);
  assert.deepEqual([payday.kind, payday.amount, payday.text], ["go", 200, "Robin passed PAYDAY"]);
  assert.equal(fmt.parseLog({ text: "Robin used a Jail pass" }, state).text, "Robin used a Jail pass");
  assert.equal(fmt.parseLog({ text: "Robin confirmed the Headlines card" }, state).kind, "confirm");
});

test("format: every log line in the fixtures parses with an actor or a known system kind", () => {
  for (const name of ["roll", "debt", "victory", "trade", "auction", "card", "jail"]) {
    const { state } = fixture(name);
    for (const entry of state.log) {
      const item = fmt.parseLog(entry, state);
      assert.ok(item.text, `empty text for ${entry.text}`);
      if (item.kind === "info") assert.fail(`unparsed log line (${name}): ${entry.text}`);
    }
  }
});

test("insights: rent mirrors the engine", () => {
  const state = makeState(designOwn, designPlayers);
  assert.equal(ins.rentFor(state, 1), 30); // 2 houses
  assert.equal(ins.rentFor(state, 24), 1100); // hotel
  assert.equal(ins.rentFor(state, 6), 12); // full light-blue set, unimproved → 2×
  assert.equal(ins.rentFor(state, 5), 50); // two railroads
  assert.equal(ins.rentLabel(state, 12), "4× DICE");
  assert.equal(ins.rentFor(state, 12, { diceTotal: 9 }), 36);
  assert.equal(ins.rentFor(state, 27), null); // unowned
  const mortgaged = makeState({ ...designOwn, 26: ["s", 0, true] }, designPlayers);
  assert.equal(ins.rentLabel(mortgaged, 26), "MORTGAGED");
  assert.equal(ins.rentLevelLabel(state, 24), "RENT WITH HOTEL");
});

test("insights: 2d6 landing odds", () => {
  const odds = ins.landingOdds(38);
  assert.equal(odds.length, 11);
  assert.ok(Math.abs(odds.reduce((sum, outcome) => sum + outcome.p, 0) - 1) < 1e-12);
  assert.equal(odds[0].square, 0); // 38 + 2 wraps to GO
  assert.equal(odds.find((outcome) => outcome.total === 7).p, 6 / 36);
  assert.ok(Math.abs(ins.probabilityOfReaching(19, [21, 23, 24]) - 8 / 36) < 1e-12);
});

test("insights: risk line matches the design copy", () => {
  const state = makeState(designOwn, designPlayers);
  const line = ins.riskLine(state, "t");
  assert.equal(line.tone, "warn");
  assert.equal(line.text, "22% chance of landing on Alpha's brown set (rent up to $1,100). You can cover it.");
  assert.deepEqual(line.squares.sort((a, b) => a - b), [21, 23, 24]);

  const jailed = makeState(designOwn, [P("t", "Robin", 1240, 10, { inJail: true }), ...designPlayers.slice(1)]);
  const jailLine = ins.riskLine(jailed, "t", { jail: true });
  assert.equal(jailLine.tone, "ok");
  assert.match(jailLine.text, /^Staying in is safe right now: (6|8)% chance of /);

  const poor = makeState(designOwn, [P("t", "Robin", 300, 19), ...designPlayers.slice(1)]);
  assert.match(ins.riskLine(poor, "t").text, /That's more than your \$300 cash\.$/);

  const quiet = makeState({ 31: ["m"] }, [P("t", "Robin", 1240, 24), ...designPlayers.slice(1)]);
  assert.equal(ins.riskLine(quiet, "t").text, "Next roll looks safe: nothing ahead charges more than $26. Busted is 14% likely.");
});

test("insights: least-rent-loss debt plan reproduces the design plan", () => {
  const state = makeState(designOwn, [P("t", "Robin", 640, 24), ...designPlayers.slice(1)]);
  const plan = ins.debtPlan(state, "t", 460);
  assert.equal(plan.total, 460);
  assert.ok(plan.exact && plan.enough);
  assert.deepEqual(plan.steps.map((step) => step.label), [
    "Sell all 4 light blue houses",
    "Mortgage both light blue streets",
    "Sell 2 houses from each green street",
  ]);
  assert.deepEqual(plan.steps.map((step) => step.amount), [100, 60, 300]);
  assert.equal(plan.steps[0].cost, "Light blue rent drops to $4 and $8");
  assert.equal(plan.steps[1].cost, "No light blue rent until you pay back $66");
  assert.equal(plan.steps[2].cost, "Green rent drops to $70–80");
  // Actions are valid in order: houses go evenly, mortgages only after the group is bare.
  let sim = structuredClone(state);
  for (const action of plan.actions) {
    const prop = sim.properties[action.spaceIndex];
    const space = BOARD[action.spaceIndex];
    if (action.type === "sell_building") {
      const levels = BOARD.filter((candidate) => candidate.group === space.group).map((candidate) => sim.properties[candidate.index].houses);
      assert.equal(prop.houses, Math.max(...levels), "even selling");
      prop.houses -= 1;
    } else {
      assert.ok(BOARD.filter((candidate) => candidate.group === space.group).every((candidate) => sim.properties[candidate.index].houses === 0), "no buildings before mortgage");
      prop.mortgaged = true;
    }
  }
});

test("insights: debt plan reports a shortfall when assets cannot cover the debt", () => {
  const state = makeState({ 1: ["t"] }, [P("t", "Robin", 10, 24), P("a", "Alpha", 900, 7)]);
  const plan = ins.debtPlan(state, "t", 500);
  assert.equal(plan.enough, false);
  assert.equal(plan.total, 30);
  assert.equal(plan.short, 470);
});

test("insights: debt plan on a real fixture is valid and enough when possible", () => {
  const { state } = fixture("debt");
  const me = state.players.find((player) => player.id === state.viewerId);
  const needed = state.debt.amount - me.cash;
  const plan = ins.debtPlan(state, me.id, needed);
  assert.ok(plan.steps.length > 0);
  assert.equal(plan.total, plan.steps.reduce((sum, step) => sum + step.amount, 0));
  if (ins.liquidationValue(state, me.id) >= needed) assert.ok(plan.enough);
});

test("insights: next house and payback", () => {
  const state = makeState(designOwn, designPlayers);
  const orange = ins.nextBuild(state, "t", "orange");
  assert.equal(orange.cost, 100);
  assert.equal(orange.delta, 200);
  assert.equal(orange.landings, 1);
  const brown = ins.housePayback(state, 3);
  assert.deepEqual([brown.rentNow, brown.rentNext, brown.delta, brown.landings], [60, 180, 120, 1]);
  assert.equal(ins.nextBuild(state, "t", "red"), null);
  const unimproved = makeState({ 37: ["t"], 39: ["t"] }, designPlayers);
  const blue = ins.nextBuild(unimproved, "t", "darkBlue");
  assert.equal(blue.index, 37); // Skyline: $70 (full set) → $175 beats Crown: $100 → $200
  assert.equal(blue.rentNow, 70);
  assert.equal(blue.delta, 105);
});

test("insights: purchase insight", () => {
  const state = makeState({ ...designOwn, 26: ["s"] }, designPlayers);
  const block = ins.purchaseInsight(state, "t", 27);
  assert.equal(block.strong, "Blocks a set.");
  assert.equal(block.text, "Sam owns Sea Breeze Road; without Coral Street, Sam can never complete orange.");
  const complete = ins.purchaseInsight(makeState({ 37: ["t"] }, designPlayers), "t", 39);
  assert.equal(complete.strong, "Completes your set.");
  assert.match(ins.purchaseInsight(state, "t", 25).strong, /Railroad 1 of 4/);
});

test("insights: trade effects", () => {
  const state = makeState(designOwn, designPlayers);
  const trade = { proposerId: "t", targetId: "a", offer: { cash: 300, properties: [] }, request: { cash: 0, properties: [9] } };
  const effects = ins.tradeEffects(state, trade, "t");
  assert.equal(effects[0].strong, "Breaks Alpha's pink set,");
  assert.equal(effects[0].text, "so Alpha can't build there any more.");
  const completing = ins.tradeEffects(makeState({ 37: ["t"], 39: ["a"] }, designPlayers), { proposerId: "t", targetId: "a", offer: { cash: 200, properties: [] }, request: { cash: 0, properties: [39] } }, "t");
  assert.equal(completing[0].strong, "Completes your red set,");
  assert.deepEqual(ins.tradeable(state, "t"), []); // every Robin street group has houses
});

test("insights: Headlines card preview", () => {
  const state = makeState({ 39: ["m"], 37: ["m"] }, [P("t", "Robin", 1240, 22), ...designPlayers.slice(1)], { turn: { dice: [1, 2] } });
  const preview = ins.cardPreview(state, "t", "chance-crown");
  assert.equal(preview.action.label, "MOVE TO CROWN AVENUE");
  assert.equal(preview.action.detail, "17 squares");
  assert.equal(preview.notes[0].v, "17 squares to Crown Avenue. You don't pass PAYDAY.");
  assert.equal(preview.notes[1].v, "Maya owns it: you pay $100.");
  assert.equal(preview.notes[2].v, "You pass PAYDAY and collect $200.");
  assert.equal(ins.cardPreview(state, "t", "chance-fine").action.label, "PAY $15");
  assert.equal(ins.cardPreview(state, "t", "chance-repairs").action.label, "NOTHING TO PAY");
});

test("insights: standings and turn order helpers", () => {
  const state = makeState(designOwn, designPlayers, { currentIndex: 1 });
  const table = ins.standings(state);
  assert.equal(table.length, 4);
  assert.equal(table[0].place, 1);
  assert.equal(ins.turnsUntil(state, "s"), 1);
  assert.equal(ins.nextPlayerId(state), "s");
  assert.ok(ins.netWorth(state, "t") > 1240);
});

test("format: since-your-last-turn items from a real fixture", () => {
  const { state } = fixture("roll");
  const items = fmt.sinceItems(state, state.viewerId, 3);
  assert.ok(Array.isArray(items));
  for (const item of items) assert.ok(item.text.length > 0);
  const feed = fmt.feedItems(state, { limit: 4 });
  assert.equal(feed.length, 4);
  assert.ok(feed.every((item) => item.kind !== "land"));
});

test("receipts: loan lines as the server writes them count the first amount", async () => {
  const { receiptsFor } = await import("../shared/receipts.js");
  const prev = fixture("active-loan").state;
  const line = (text) => ({ ...prev, log: [...prev.log, { id: `x_${text.length}`, text }] });
  const lent = receiptsFor(prev, line("Alpha lent Robin $300; Robin repays $360 by turn 14"));
  const byName = (list, name) => list.find((receipt) => receipt.playerId === prev.players.find((player) => player.name === name).id);
  assert.equal(byName(lent, "Alpha").amount, -300);
  assert.equal(byName(lent, "Robin").amount, 300);
  const repaid = receiptsFor(prev, line("Robin repaid Alpha $480 (loan due)"));
  assert.equal(byName(repaid, "Robin").amount, -480);
  assert.equal(byName(repaid, "Alpha").label, "REPAID");
});

// ---------------------------------------------------------------------------------------------
// Sound, music, card read time, trade impact, deed cards

const memoryStorage = () => {
  const data = new Map();
  return { getItem: (key) => (data.has(key) ? data.get(key) : null), setItem: (key, value) => data.set(key, String(value)), removeItem: (key) => data.delete(key), data };
};

test("sound: the documented names, aliases, and play() is silent (false) before a gesture", async () => {
  const sound = await import("../shared/sound.js");
  for (const name of ["dice", "hop", "land", "go", "coins", "buy", "gavel", "bid", "house", "hotel", "mortgage", "card", "jail", "offer", "accept", "decline", "loan", "bankrupt", "win", "turn", "tap"]) {
    assert.ok(sound.SOUND_NAMES.includes(name), name);
  }
  assert.equal(new Set(sound.SOUND_NAMES).size, sound.SOUND_NAMES.length);
  assert.deepEqual(Object.keys(sound.ALIASES).sort(), ["build", "deal", "declined", "step", "yourTurn"]);
  for (const target of Object.values(sound.ALIASES)) assert.ok(sound.SOUND_NAMES.includes(target));
  assert.equal(sound.play("dice"), false, "no AudioContext in Node: ignored, no throw");
  assert.equal(sound.play("nope"), false);
  for (const name of ["dice", "tap", "coins", "stamp", "pop", "thud", "fanfare", "snap", "click", "isMuted", "setMuted", "toggleMuted", "onMuteChange", "unlockAudio", "installAudioUnlock"]) assert.equal(typeof sound[name], "function", name);
});

test("sound: volumes and on/off persist in localStorage, clamped, with safe defaults", async () => {
  const store = memoryStorage();
  globalThis.localStorage = store;
  try {
    const sound = await import(`../shared/sound.js?settings=${Date.now()}`);
    assert.deepEqual(sound.loadSettings(), { ...sound.DEFAULT_SETTINGS, muted: false });
    sound.setEffectsVolume(0.3);
    sound.setMusicVolume(4);
    sound.setMusicEnabled(false);
    sound.setMuted(true);
    const saved = JSON.parse(store.getItem("estate.sound"));
    assert.deepEqual(saved, { effects: 0.3, music: 1, effectsOn: true, musicOn: false });
    assert.equal(store.getItem("estate.muted"), "1");
    assert.deepEqual(sound.loadSettings(), { effects: 0.3, music: 1, effectsOn: true, musicOn: false, muted: true });
    store.setItem("estate.sound", "{not json");
    assert.equal(sound.loadSettings().effects, sound.DEFAULT_SETTINGS.effects, "broken JSON gives the defaults");
    const seen = [];
    const off = sound.onSettingsChange((settings) => seen.push(settings.effects));
    sound.setEffectsVolume(0.5);
    off();
    assert.deepEqual(seen, [0.5]);
  } finally {
    delete globalThis.localStorage;
  }
});

test("music: moods by game phase", async () => {
  const music = await import("../shared/music.js");
  // "thinking" (0.12, the quiz): the final round, from its category to its last answer.
  assert.deepEqual(music.MOODS, ["lobby", "play", "tension", "victory", "thinking"]);
  assert.equal(music.moodFor({ game: "quiz", status: "playing", phase: "finalBet" }), "thinking");
  assert.equal(music.moodFor({ game: "quiz", status: "playing", phase: "answer" }), "play");
  const base = fixture("roll").state;
  assert.equal(music.moodFor(null), null);
  assert.equal(music.moodFor({ ...base, status: "lobby" }), "lobby");
  assert.equal(music.moodFor(base), "play");
  assert.equal(music.moodFor({ ...base, phase: "auction" }), "tension");
  assert.equal(music.moodFor({ ...base, phase: "debt" }), "tension");
  assert.equal(music.moodFor({ ...base, phase: "turn_complete", lastLanding: { kind: "rent", amount: 950 } }), "tension", "a big rent");
  assert.equal(music.moodFor({ ...base, phase: "turn_complete", lastLanding: { kind: "rent", amount: 80 } }), "play");
  assert.equal(music.moodFor({ ...base, status: "finished" }), "victory");
  for (const name of ["start", "setMood", "stop", "setEnabled", "isEnabled", "setVolume"]) assert.equal(typeof music[name], "function", name);
});

test("cards: read time is max(3.5 s, 2 s + 55 ms a character), at most 9 s", () => {
  assert.equal(fmt.cardReadMs(""), 3500);
  assert.equal(fmt.cardReadMs("x".repeat(20)), 3500, "short cards get the floor");
  assert.equal(fmt.cardReadMs("x".repeat(40)), 4200);
  assert.equal(fmt.cardReadMs("Strike called off: go to the nearest railroad; if owned, pay double rent"), 2000 + 55 * 72);
  assert.equal(fmt.cardReadMs("x".repeat(400)), 9000);
});

test("tradeImpact: cash, net worth, sets made and broken, rent changes, blocked deeds, verdicts", () => {
  const state = structuredClone(fixture("pad-turn").state);
  const id = (name) => state.players.find((player) => player.name === name).id;
  state.properties[16] = { ownerId: id("Maya"), houses: 0, mortgaged: false };
  const trade = { proposerId: id("Maya"), targetId: id("Robin"), offer: { cash: 50, properties: [21] }, request: { cash: 0, properties: [19] } };
  const impact = ins.tradeImpact(state, trade, { viewerId: id("Maya") });
  assert.equal(impact.proposer.cashAfter, impact.proposer.cashBefore - 50);
  assert.equal(impact.target.cashAfter, impact.target.cashBefore + 50);
  assert.deepEqual(impact.proposer.setsCompleted.map((set) => set.group), ["orange"]);
  assert.ok(impact.proposer.verdicts.includes("Completes your GREEN set"));
  const orange = impact.proposer.rentChanges.find((change) => change.key === "orange");
  assert.ok(orange && orange.after > orange.before, "orange rent goes up");
  assert.match(orange.text, /^Green rent \$\d+ → \$\d+$/);
  assert.equal(impact.proposer.worthAfter, ins.netWorth(impact.after, id("Maya")));
  // Breaking a set: Robin gives away a green street.
  const breaks = ins.tradeImpact(state, { proposerId: id("Robin"), targetId: id("Sam"), offer: { cash: 0, properties: [31] }, request: { cash: 100, properties: [] } });
  assert.ok(breaks.proposer.setsBroken.some((set) => set.group === "green"));
  assert.ok(breaks.proposer.verdicts.some((line) => /^Breaks Robin.s DARK BLUE set$/.test(line)));
  // Railroads change hands: counts before and after.
  const rails = ins.tradeImpact(state, { proposerId: id("Sam"), targetId: id("Maya"), offer: { cash: 10, properties: [] }, request: { cash: 0, properties: [5] } });
  assert.deepEqual(rails.proposer.railroads, { before: 0, after: 1 });
  assert.deepEqual(rails.target.railroads, { before: 2, after: 1 });
  // Blocked: a street on a set with houses.
  state.properties[31].houses = 1;
  const blocked = ins.tradeImpact(state, { proposerId: id("Robin"), targetId: id("Sam"), offer: { cash: 0, properties: [32] }, request: { cash: 0, properties: [] } });
  assert.deepEqual(blocked.proposer.blocked, [{ index: 32, reason: "HOUSES" }]);
  assert.ok(blocked.proposer.verdicts.some((line) => /can.t be traded/.test(line)));
});

test("deed card: rent table with the level that applies now, costs, owner, set line; other squares explained", async () => {
  const { deedDetailHtml, rentLevel } = await import("../shared/deed.js");
  const state = structuredClone(fixture("pad-turn").state);
  const robin = state.players.find((player) => player.name === "Robin").id;
  for (const index of [31, 32, 34]) state.properties[index].houses = 2;
  assert.equal(rentLevel(state, 32), 3, "two houses");
  const html = deedDetailHtml(state, 32, { viewerId: robin });
  assert.match(html, /FOREST AVENUE/);
  assert.equal((html.match(/deed-card__row is-now/g) || []).length, 1);
  assert.match(html, /is-now"><span>With 2 houses<\/span>/);
  assert.match(html, />YOU</, "the viewer's own deed");
  assert.match(html, /Mortgage/);
  assert.match(html, /Unmortgage/);
  assert.equal((html.match(/deed-card__member/g) || []).length, 3, "the three green streets");
  const rail = deedDetailHtml(state, 5);
  assert.match(rail, /is-now"><span>2 railroads/, "Maya owns two");
  assert.match(deedDetailHtml(state, 12), /4× the dice/);
  assert.match(deedDetailHtml(state, 0), /Collect \$200/);
  assert.match(deedDetailHtml(state, 4), /Pay \$200/);
  assert.match(deedDetailHtml(state, 30), /Busted: go straight to jail/);
  state.properties[5].mortgaged = true;
  assert.match(deedDetailHtml(state, 5), /MORTGAGED/);
  for (const space of state.board) assert.doesNotMatch(deedDetailHtml(state, space.index), /undefined|NaN/, `square ${space.index}`);
});

test("format: computer levels cycle easy → normal → hard → expert → grandmaster", () => {
  assert.deepEqual(fmt.AI_LEVELS, ["easy", "normal", "hard", "expert", "grandmaster"]);
  assert.equal(fmt.nextAiLevel("hard"), "expert", "EXPERT comes after HARD");
  assert.equal(fmt.nextAiLevel("expert"), "grandmaster", "GRANDMASTER comes after EXPERT");
  assert.equal(fmt.nextAiLevel("grandmaster"), "easy", "and the cycle wraps");
  assert.equal(fmt.aiLevelLabel("expert"), "Expert");
  assert.equal(fmt.aiLevelLabel("grandmaster"), "Grandmaster");
  assert.equal(fmt.deviceLabel({}, { id: "x", kind: "ai", difficulty: "expert" }), "Computer · Expert");
  assert.equal(fmt.deviceLabel({}, { id: "x", kind: "ai", difficulty: "grandmaster" }), "Computer · Grandmaster");
});

test("opening order: first rolls for everyone, tie-break rolls only for the tied (review B3)", () => {
  const players = [P("m", "Maya", 1500, 0), P("b", "Blue Whale", 1500, 0), P("p", "Priya", 1500, 0)];
  const entries = [
    { playerId: "m", rolls: [{ round: 1, dice: [1, 1], total: 2 }] },
    { playerId: "b", rolls: [{ round: 1, dice: [1, 1], total: 2 }, { round: 2, dice: [1, 2], total: 3 }] },
    { playerId: "p", rolls: [{ round: 1, dice: [3, 4], total: 7 }] },
  ];
  const base = { status: "playing", phase: "determining_order", players, openingOrder: { round: 2, pendingIds: ["m"], entries, order: [] } };
  for (const state of [base, { ...base, openingOrder: { ...base.openingOrder, rollingIds: ["m", "b"], ties: [{ round: 1, total: 2, playerIds: ["m", "b"] }] } }]) {
    const rows = Object.fromEntries(ins.openingRows(state).map((row) => [row.playerId, row]));
    assert.equal(rows.m.shown, "2 · RE-ROLL", "Maya: her 2, then her tie-break to come");
    assert.ok(rows.m.pending && rows.m.tied);
    assert.equal(rows.m.tieTotal, 2);
    assert.equal(rows.b.shown, "2 · 3", "Blue Whale: round 1 and the tie-break, not just the latest roll");
    assert.ok(rows.b.tied && !rows.b.pending);
    assert.equal(rows.p.shown, "7");
    assert.ok(!rows.p.tied && !rows.p.pending, "Priya is not in the tie");
    const tie = ins.openingTie(state);
    assert.deepEqual(tie.ids, ["m", "b"]);
    assert.deepEqual(tie.totals, [2]);
  }
  const first = { ...base, openingOrder: { round: 1, pendingIds: ["p"], entries: entries.map((entry) => ({ ...entry, rolls: entry.rolls.slice(0, 1) })).map((entry) => (entry.playerId === "p" ? { ...entry, rolls: [] } : entry)), order: [] } };
  assert.equal(ins.openingTie(first), null, "round 1: no tie-break");
  assert.deepEqual(ins.openingRows(first).map((row) => row.shown), ["2", "2", "…"]);
});

test("deal balance: a deal that completes your set is good for you, whatever the list prices (review U8)", () => {
  const players = [P("a", "Ana", 1500, 0), P("b", "Ravi", 1500, 0)];
  const state = makeState({ 1: ["a"], 3: ["b"], 39: ["a"] }, players);
  // Ravi gives Maple Bridge ($60) for Ana's Crown Avenue ($400): list prices favour Ravi...
  const trade = { proposerId: "b", targetId: "a", offer: { cash: 0, properties: [3] }, request: { cash: 0, properties: [39] } };
  const ana = ins.dealBalance(state, trade, "a");
  assert.equal(ana.give, 400);
  assert.equal(ana.get, 60);
  assert.equal(ana.tone, "good", "...but Ana completes BROWN");
  assert.deepEqual(ana.sets.map((set) => [set.group, set.gain, set.you, set.rent]), [["brown", true, true, 450]]);
  assert.equal(ins.dealBalance(state, trade, "b").tone, "bad", "for Ravi it hands over a set");
  const even = ins.dealBalance(state, { ...trade, offer: { cash: 400, properties: [] }, request: { cash: 0, properties: [39] } }, "a");
  assert.equal(even.tone, "even", "no set: list prices decide");
  assert.deepEqual(even.sets, []);
});

test("end game: the preview counts a pending debt like the server (review B4)", async () => {
  const players = [P("a", "Ana", 3700, 0), P("b", "Ben", 2550, 0)];
  const state = makeState({}, players, { phase: "debt", debt: { payerId: "a", creditorId: "b", amount: 2000 } });
  assert.equal(ins.finalScore(state, "a"), 1700);
  assert.equal(ins.finalScore(state, "b"), 4550);
  assert.equal(ins.standings(state)[0].player.id, "b", "Ben would win, not Ana");
  const views = await import("../table/views.js");
  assert.equal(views.endGameLeader(state).player.id, "b");
  const { leaderNow } = await import("../phone/settings.js");
  assert.equal(leaderNow(state, ins.netWorth, ins.finalScore).player.id, "b");
  const loans = { ...makeState({}, players), loans: [{ status: "active", lenderId: "b", borrowerId: "a", repay: 600 }] };
  assert.equal(ins.finalScore(loans, "a"), 3100, "a loan owed counts against");
  assert.equal(ins.finalScore(loans, "b"), 3150);
});

test("seat approval card: whose seat, the 4-digit code, LET IN / NO", async () => {
  const { claimCardHtml, claimSubject } = await import("../shared/claims.js");
  const state = { players: [{ id: "p1", name: "Maya", tokenKind: "anchor" }] };
  const claim = { id: "c1", kind: "seat", playerId: "p1", playerName: "Maya", deviceName: "Pixel", code: "4812" };
  assert.equal(claimSubject(claim), "MAYA'S SEAT");
  assert.equal(claimSubject({ kind: "seat", playerName: "James" }), "JAMES' SEAT");
  assert.equal(claimSubject({ kind: "table" }), "THE TABLE SCREEN");
  const html = claimCardHtml(state, claim);
  assert.equal((html.match(/<span>\d<\/span>/g) || []).length, 4, "one box per digit");
  assert.match(html, /data-claim="c1" data-decision="approve">LET IN</);
  assert.match(html, /data-claim="c1" data-decision="deny">NO</);
  assert.match(html, /pchip--glyph/, "the seat's token");
  assert.doesNotMatch(claimCardHtml(state, { ...claim, deviceName: "<b>x</b>" }), /<b>x<\/b>/, "names are escaped");
});

test("format: a computer's price against the draft's cash (0.10.1)", () => {
  assert.equal(fmt.quoteState(null).kind, "wait");
  assert.equal(fmt.quoteState({ loading: true }).text, "THINKING");
  const need = fmt.quoteState({ offerCash: 650, requestCash: 0 }, 302, 0);
  assert.equal(need.text, "ACCEPTS AT $650");
  assert.equal(need.status, "$348 MORE NEEDED");
  assert.equal(fmt.quoteState({ offerCash: 650 }, 650, 0).status, "WILL ACCEPT");
  assert.equal(fmt.quoteState({ offerCash: 650 }, 700, 0).kind, "ok", "more than its price is accepted too");
  const pays = fmt.quoteState({ offerCash: 0, requestCash: 60 }, 0, 80);
  assert.equal(pays.text, "PAYS YOU UP TO $60");
  assert.equal(pays.status, "$20 MORE NEEDED");
  const never = fmt.quoteState({ error: "No deal: that would give you a full set." });
  assert.deepEqual([never.kind, never.text, never.detail], ["never", "WON'T TRADE AT ANY PRICE", "That would give you a full set"]);
  assert.equal(fmt.quoteState({ error: "No deal at any price." }).detail, "");
  assert.equal(fmt.quoteState({ error: "The AI is not willing to buy the selected properties" }).kind, "never");
  assert.equal(fmt.quoteState({ error: "No deal: not for any price you can pay." }).kind, "short");
  assert.equal(fmt.quoteState({ error: "The game moved on; ask again" }).kind, "error");
});

test("format: a counter-offer's changes, the computer's words, and bot-only trades out of the feed", () => {
  const players = [P("t", "Robin", 1000, 0), { ...P("a", "Alpha", 1000, 0), kind: "ai", input: "ai" }, { ...P("b", "Blue", 1000, 0), kind: "ai", input: "ai" }];
  const trade = { proposerId: "a", targetId: "t", offer: { cash: 0, properties: [39] }, request: { cash: 650, properties: [28] },
    original: { offer: { cash: 302, properties: [28] }, request: { cash: 0, properties: [39] } } };
  assert.deepEqual(fmt.counterChanges({ board: BOARD }, trade, "t"), [{ text: "+$348", tone: "bad" }]);
  const extra = { ...trade, request: { cash: 200, properties: [28, 21] } };
  assert.deepEqual(fmt.counterChanges({ board: BOARD }, extra, "t").map((c) => c.text), ["−$102", `+ ${BOARD[21].name} from you`]);
  const state = makeState({}, players, { log: [{ text: "Alpha: I'd do it for $650." }] });
  assert.equal(fmt.lastReply(state, "a"), "I'd do it for $650.");
  assert.equal(fmt.parseLog({ text: "Alpha countered Robin's offer" }, state).kind, "trade");
  const log = [{ text: "Robin's turn" }, { text: "Robin rolled 3 + 4 = 7" }, { text: "Alpha's turn" },
    { text: "Alpha proposed a trade to Blue" }, { text: "Blue declined the trade" },
    { text: "Alpha proposed a trade to Blue" }, { text: "Blue accepted Alpha's trade" }, { text: "Robin's turn" }];
  const since = fmt.sinceItems({ ...state, log, currentPlayerId: "t" }, "t", 5).map((item) => item.text);
  assert.ok(since.some((text) => /accepted Alpha's trade/.test(text)), "a deal between computers still shows");
  assert.ok(!since.some((text) => /offered|declined/.test(text)), "their offers and refusals don't");
});

test("game screens: text isn't selectable (drag or long-press), inputs and the invite link are", () => {
  // The rules live in the shared base stylesheet, which the table (TV/PC, poker) and the phone
  // (phone, tablet, desktop, poker) both load first. web/tests/screens.browser.mjs checks the
  // computed values in a real browser.
  const css = readFileSync(join(here, "..", "shared", "tokens.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
  const rule = (selector) => {
    const at = css.indexOf(`\n${selector} {`);
    assert.ok(at >= 0, `rule for ${selector}`);
    return css.slice(at, css.indexOf("}", at));
  };
  const body = rule("body");
  for (const declaration of ["-webkit-user-select: none", "user-select: none", "-webkit-touch-callout: none", "-webkit-tap-highlight-color: transparent"]) assert.ok(body.includes(declaration), `body: ${declaration}`);
  const typing = rule('input, textarea, [contenteditable]:not([contenteditable="false"]), .selectable');
  assert.match(typing, /-webkit-user-select: text;\s*user-select: text/, "inputs, textareas and editable text stay selectable");
  assert.match(css, /button, a, input, select, textarea, label, summary, \[role="button"\][^{]*\{ touch-action: manipulation; \}/, "double tap never zooms or selects on a control");
  for (const page of ["table", "phone"]) {
    const html = readFileSync(join(here, "..", page, "index.html"), "utf8");
    assert.match(html, /href="\/shared\/tokens\.css"/, `${page} loads the base stylesheet`);
  }
  const phone = readFileSync(join(here, "..", "phone", "phone.css"), "utf8");
  assert.match(phone, /\.link-box span \{[^}]*-webkit-user-select: all; user-select: all;/, "the invite link can be selected and copied by hand");
});

// ---- 0.10.1: a deed's context actions (web/shared/deedactions.js) ----

test("deed actions: your own mortgaged deed → UNMORTGAGE with interest; build greyed until the set is clear", async () => {
  const { deedActions, unmortgagePrice } = await import("../shared/deedactions.js");
  assert.deepEqual([60, 100, 200, 350, 400].map(unmortgagePrice), [33, 55, 110, 193, 220], "the engine's UnmortgageCost (whole numbers)");
  const players = [P("t", "Robin", 500, 0), { ...P("a", "Alpha", 900, 0), kind: "ai", input: "ai" }];
  const state = makeState({ 1: ["t", 0, true], 3: ["t", 0, false] }, players, { currentPlayerId: "t" });
  const out = deedActions(state, "t", 1);
  assert.equal(out.mine, true);
  const byId = Object.fromEntries(out.actions.map((action) => [action.id, action]));
  assert.deepEqual(Object.keys(byId), ["build", "unmortgage", "trade", "not-selling"]);
  assert.equal(byId.unmortgage.disabled, false);
  assert.deepEqual(byId.unmortgage.act, { type: "unmortgage", spaceIndex: 1 });
  assert.equal(byId.unmortgage.detail, "−$33 with interest");
  assert.equal(byId.build.disabled, true);
  assert.match(byId.build.reason, /^Unmortgage the light blue set first$/);
  assert.equal(byId.trade.disabled, false);
  assert.equal(byId.trade.cmd, "deed-trade");
  // Too little cash, in debt, and not your turn each grey UNMORTGAGE with the engine's reason.
  const poor = { ...state, players: [{ ...players[0], cash: 20 }, players[1]] };
  assert.equal(deedActions(poor, "t", 1).actions.find((a) => a.id === "unmortgage").reason, "Costs $33; you have $20");
  const debt = { ...state, phase: "debt", debt: { payerId: "t", amount: 600 } };
  assert.equal(deedActions(debt, "t", 1).actions.find((a) => a.id === "unmortgage").reason, "Not while you owe money");
  const theirTurn = { ...state, currentPlayerId: "a" };
  assert.equal(deedActions(theirTurn, "t", 1).actions.find((a) => a.id === "unmortgage").reason, "On your turn");
  // The unmortgaged one beside it: MORTGAGE +half the price.
  const other = deedActions(state, "t", 3).actions.find((a) => a.id === "mortgage");
  assert.deepEqual([other.disabled, other.detail, other.act.type], [false, "+$30", "mortgage"]);
});

test("deed actions: a built set → build and sell evenly, no mortgage or trade until the houses go", async () => {
  const { deedActions } = await import("../shared/deedactions.js");
  const players = [P("t", "Robin", 500, 0), P("b", "Bea", 500, 0)];
  const state = makeState({ 1: ["t", 1], 3: ["t", 2] }, players, { currentPlayerId: "t" });
  const high = Object.fromEntries(deedActions(state, "t", 3).actions.map((a) => [a.id, a]));
  assert.equal(high.build.reason, `Build evenly: next on ${BOARD[1].name}`);
  assert.deepEqual([high.sell.disabled, high.sell.detail, high.sell.act.type], [false, "+$25", "sell_building"]);
  assert.equal(high.mortgage.reason, "Sell the buildings on the light blue set first");
  assert.equal(high.trade.reason, "Sell the buildings on the light blue set first");
  const low = Object.fromEntries(deedActions(state, "t", 1).actions.map((a) => [a.id, a]));
  assert.deepEqual([low.build.disabled, low.build.label, low.build.detail], [false, "BUILD A HOUSE", "−$50"]);
  assert.equal(low.sell.reason, `Sell evenly: first from ${BOARD[3].name}`);
  // Four on each: a hotel next, unless the bank has none; a hotel sells as SELL THE HOTEL.
  const four = makeState({ 1: ["t", 4], 3: ["t", 4] }, players, { currentPlayerId: "t", bank: { houses: 24, hotels: 0 } });
  const hotel = deedActions(four, "t", 1).actions.find((a) => a.id === "build");
  assert.deepEqual([hotel.label, hotel.reason], ["BUILD A HOTEL", "The bank has no hotels left"]);
  const top = makeState({ 1: ["t", 5], 3: ["t", 5] }, players, { currentPlayerId: "t" });
  assert.deepEqual(deedActions(top, "t", 1).actions.map((a) => a.id), ["sell", "mortgage", "trade", "not-selling"]);
  assert.equal(deedActions(top, "t", 1).actions[0].label, "SELL THE HOTEL");
  // Not owning the whole set: BUILD greyed with why.
  const half = makeState({ 1: ["t", 0] }, players, { currentPlayerId: "t" });
  assert.equal(deedActions(half, "t", 1).actions.find((a) => a.id === "build").reason, "Own all of the light blue set to build");
});

test("deed actions: someone else's deed → ASK TO BUY…; unowned → BUY only while you stand on it", async () => {
  const { deedActions } = await import("../shared/deedactions.js");
  const players = [P("t", "Robin", 500, 0), { ...P("a", "Alpha", 900, 0), kind: "ai", input: "ai" }];
  const state = makeState({ 39: ["a", 0] }, players, { currentPlayerId: "t" });
  const theirs = deedActions(state, "t", 39);
  assert.equal(theirs.mine, false);
  assert.deepEqual(theirs.actions.map((a) => [a.id, a.label, a.detail, a.cmd, a.arg, a.disabled]), [["ask", "ASK TO BUY…", "Alpha", "deed-ask", "39", false]]);
  // Off-turn: the table greys it (trades go on your turn); the phone saves it for your turn.
  const later = { ...state, currentPlayerId: "a" };
  assert.equal(deedActions(later, "t", 39).actions[0].reason, "Trade on your turn");
  assert.equal(deedActions(later, "t", 39, { planTrades: true }).actions[0].disabled, false);
  assert.equal(deedActions({ ...state, phase: "trade", trade: { proposerId: "t", targetId: "a" } }, "t", 39).actions[0].reason, "Wait for the pending trade");
  const built = makeState({ 37: ["a", 1], 39: ["a", 1] }, players, { currentPlayerId: "t" });
  assert.equal(deedActions(built, "t", 39).actions[0].reason, "It has buildings: Alpha would have to sell them first");
  // Unowned: info only, unless you're on it with the choice to make.
  assert.deepEqual(deedActions(state, "t", 37).actions, []);
  const landing = { ...state, phase: "awaiting_purchase", pendingPurchase: 37 };
  assert.deepEqual(deedActions(landing, "t", 37).actions.map((a) => [a.id, a.detail, a.disabled, a.act.type]), [["buy", "$350", false, "buy"]]);
  assert.deepEqual(deedActions(landing, "t", 34).actions, [], "not the square you're on");
  assert.deepEqual(deedActions(landing, "a", 37).actions, [], "not your landing");
  const short = { ...landing, players: [{ ...players[0], cash: 100 }, players[1]] };
  assert.equal(deedActions(short, "t", 37).actions[0].reason, "Costs $350; you have $100");
  // A computer's own deeds offer nothing to a spectator, and squares that aren't deeds have none.
  assert.deepEqual(deedActions(state, null, 39).actions, []);
  assert.deepEqual(deedActions(state, "t", 0).actions, []);
});

// 0.11.1: inviting friends on WhatsApp ("there is no way to send link to friend on whatsapp").
test("invite: the message names the game and table, and the WhatsApp link carries it encoded", async () => {
  const { inviteLine, inviteText, whatsappUrl, openWhatsApp } = await import("../shared/invite.js");
  assert.equal(inviteLine("ludo", "ABCDE"), "Come and play Ludo with me! Table ABCDE");
  assert.equal(inviteLine("unknown", ""), "Come and play Estate with me!");
  const text = inviteText("poker", "XY12Z", "https://estate.example.net/j/XY12Z");
  assert.match(text, /poker.*XY12Z: https:\/\/estate\.example\.net\/j\/XY12Z$/);
  const url = whatsappUrl(text);
  assert.ok(url.startsWith("https://wa.me/?text="));
  assert.equal(decodeURIComponent(url.slice("https://wa.me/?text=".length)), text);
  const opened = [];
  openWhatsApp(text, { open: (u, t) => { opened.push([u, t]); return {}; }, location: {} });
  assert.deepEqual(opened, [[url, "_blank"]]);
  const blocked = { open: () => null, location: { href: "" } };
  openWhatsApp(text, blocked);
  assert.equal(blocked.location.href, url, "a blocked pop-up falls back to this tab");
});

test("a new table names the one it replaces, and only when there is one", async () => {
  const sent = [];
  const client = { ensureDevice: async () => {}, request: async (method, path, body) => sent.push([method, path, body]) };
  await EstateClient.prototype.createTable.call(client, "poker", { code: "ABCDE", token: "secret" });
  await EstateClient.prototype.createTable.call(client, "ludo");
  await EstateClient.prototype.createTable.call(client, "estate", { code: "ABCDE" });
  assert.deepEqual(sent, [
    ["POST", "/api/tables", { gameType: "poker", replaceCode: "ABCDE", replaceToken: "secret" }],
    ["POST", "/api/tables", { gameType: "ludo" }],
    ["POST", "/api/tables", { gameType: "estate" }],
  ]);
});

test("room socket: a change's events go with its state; a state already here drops them (no replayed move)", () => {
  const seen = [];
  const room = new LiveRoom({}, {
    code: "AB12C",
    token: "t",
    onState: (state) => seen.push(`state ${state.version}`),
    onEvent: (event) => seen.push(`event ${event.type}`),
  });
  const now = Date.now();
  // A keyboard seat's action: the POST's reply (version 16) gets here before the socket does.
  room.accept({ version: 16, now });
  // Then the socket: the move's events and the same state, which is dropped with them.
  room.receive({ type: "event", event: { type: "dice", playerId: "p1", dice: [1, 2] } });
  room.receive({ type: "event", event: { type: "move", playerId: "p1", path: [2, 3, 4, 5] } });
  room.receive({ type: "state", state: { version: 16, now } });
  // The next change (END TURN): only its own events come with it, not the move again.
  room.receive({ type: "event", event: { type: "buy", playerId: "p1", spaceIndex: 5 } });
  room.receive({ type: "state", state: { version: 17, now } });
  assert.deepEqual(seen, ["state 16", "event buy", "state 17"]);
  // The socket first, then the reply: the events play once and the reply is dropped.
  room.receive({ type: "event", event: { type: "move", playerId: "p2", path: [7, 8] } });
  room.receive({ type: "state", state: { version: 18, now } });
  room.accept({ version: 18, now });
  assert.deepEqual(seen.slice(3), ["event move", "state 18"]);
  clearInterval(room.pingTimer);
});

test("room socket: events left by a duplicate, a closed socket or a replaced socket never reach a later state", () => {
  const seen = [];
  const room = new LiveRoom({}, {
    code: "AB12C",
    token: "t",
    onState: (state) => seen.push(`state ${state.version}`),
    onEvent: (event) => seen.push(`event ${event.type}`),
  });
  const now = Date.now();
  room.accept({ version: 20, now });
  // Part of a change's events, then the POST's reply, then the socket's copy of that state.
  room.receive({ type: "event", event: { type: "dice", playerId: "p1", dice: [3, 3] } });
  room.accept({ version: 21, now });
  room.receive({ type: "event", event: { type: "move", playerId: "p1", path: [4, 5, 6, 7, 8, 9, 10] } });
  room.receive({ type: "state", state: { version: 21, now } });
  room.accept({ version: 22, now });
  assert.deepEqual(seen, ["state 20", "state 21", "state 22"]);
  // A socket that closes between a change's events and its state: the next state carries nothing.
  const socket = { readyState: 1, close() {} };
  room.socket = socket;
  room.receive({ type: "event", event: { type: "move", playerId: "p2", path: [1, 2] } });
  room.stopped = true; // no retry timers in a test
  room.dropSocket(socket);
  room.accept({ version: 23, now });
  assert.deepEqual(seen.slice(3), ["state 23"]);
  // Events with no state (the server failed to build one) go with the next state, once.
  room.receive({ type: "event", event: { type: "pay", from: "p1", to: null, amount: 100 } });
  room.receive({ type: "state", state: { version: 24, now } });
  room.receive({ type: "state", state: { version: 25, now } });
  assert.deepEqual(seen.slice(4), ["event pay", "state 24", "state 25"]);
  clearInterval(room.pingTimer);
  clearInterval(room.watchTimer);
  clearTimeout(room.offlineTimer);
});
