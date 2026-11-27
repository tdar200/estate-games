// UX round (docs/API.md §3c), the shared rules both screens use: the timed auction's countdown and
// quick bids, idle time, the host's controls and their requests, the while-away list, RENT ×2 /
// ×10 and the debt checklist. Run: node --test web/tests/ux-shared.check.mjs

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { GOING_ONCE_MS, QUICK_STEPS, auctionActors, canBid, clockFraction, clockText, goingOnce, isTimed, msLeft, quickBids, screenNow, secondsLeft } from "../shared/auction-clock.js";
import { debtChecklist, toggleDebtRow } from "../shared/debt-checklist.js";
import { rentMultiple } from "../shared/insights.js";
import { IDLE_SHOW_MS, awayCash, awayLines, hostControls, hostRequest, idleMs, idleShown, lastMoveAt, minutesText, removalSummary, showAwayList, showBack, standInMs, waitedOnIds } from "../shared/standin.js";
import { EstateClient } from "../shared/client.js";
import { composeFixture } from "../table/demo.js";

const here = dirname(fileURLToPath(import.meta.url));
const load = async (name) => JSON.parse(readFileSync(join(here, "..", "fixtures", `${name}.json`), "utf8"));
const compose = async (name) => (await composeFixture(name, load)).state;
const byName = (state, name) => state.players.find((player) => player.name === name);

const NOW = 1793560000000;
const clock = (left, clockMs = 8000) => ({ clockMs, endsAt: NOW + left, currentBid: 180, activeIds: [], bids: {} });

test("countdown: ms left from endsAt and the server clock, clamped to the clock's length", () => {
  assert.equal(msLeft(clock(5400), NOW), 5400);
  assert.equal(msLeft(clock(5400), NOW + 6000), 0, "never below 0");
  assert.equal(msLeft(clock(12000), NOW), 8000, "a screen behind the server never shows more than the clock holds");
  assert.equal(msLeft({ currentBid: 10 }, NOW), 0, "turn-order auctions have no clock");
  assert.equal(secondsLeft(5400), 6);
  assert.equal(secondsLeft(400), 1);
  assert.equal(secondsLeft(0), 0);
  assert.equal(clockText(5400), "0:06");
  assert.equal(clockText(30000), "0:30");
  assert.equal(clockFraction(clock(4000), NOW), 0.5);
  assert.equal(clockFraction(clock(4000, 20000), NOW), 0.2, "the length comes from clockMs, not a fixed 8 s");
});

test("countdown: GOING ONCE at 3 s or less, not at 0", () => {
  assert.equal(GOING_ONCE_MS, 3000);
  assert.equal(goingOnce(clock(3001), NOW), false);
  assert.equal(goingOnce(clock(3000), NOW), true);
  assert.equal(goingOnce(clock(1), NOW), true);
  assert.equal(goingOnce(clock(0), NOW), false, "closed, not going");
});

test("countdown: server clock offset, the demo's frozen now", () => {
  const offset = { now: () => NOW + 1234 };
  assert.equal(screenNow({ now: NOW }, { demo: false, clock: offset }), NOW + 1234, "live: the socket's synced clock");
  assert.equal(screenNow({ now: NOW }, { demo: true, clock: offset }), NOW, "demo: the fixture's now");
});

test("timed auction: anyone in but the leader may bid; turn order keeps its one bidder", async () => {
  const state = await compose("t-auction-timed");
  assert.ok(isTimed(state));
  assert.equal(state.auction.currentPlayerId, null);
  const sam = byName(state, "Sam").id;
  const maya = byName(state, "Maya").id;
  assert.equal(canBid(state, sam), false, "the leader waits");
  assert.equal(canBid(state, maya), true);
  assert.ok(!auctionActors(state).includes(sam));
  assert.deepEqual(waitedOnIds(state), [], "a timed auction never waits on anyone (no idle)");

  const turnOrder = (await composeFixture("table-auction", load)).state;
  assert.equal(isTimed(turnOrder), false);
  const bidder = turnOrder.auction.currentPlayerId;
  assert.equal(canBid(turnOrder, bidder), true);
  for (const player of turnOrder.players) if (player.id !== bidder) assert.equal(canBid(turnOrder, player.id), false);
});

test("quick bids: currentBid + step, the ones the player can't afford hidden", async () => {
  const state = await compose("t-auction-timed");
  const maya = byName(state, "Maya");
  assert.deepEqual(QUICK_STEPS, [10, 50, 100]);
  assert.deepEqual(quickBids(state, maya.id), [{ step: 10, amount: 190 }, { step: 50, amount: 230 }, { step: 100, amount: 280 }]);
  const poor = await compose("t-auction-timed-going");
  assert.equal(byName(poor, "Maya").cash, 250);
  assert.deepEqual(quickBids(poor, maya.id).map((bid) => bid.step), [10, 50], "+$100 ($280) is over $250");
  const broke = structuredClone(state);
  byName(broke, "Maya").cash = 185;
  assert.deepEqual(quickBids(broke, maya.id), [], "not even +$10: only PASS is left");
  const lead = await compose("t-auction-timed-lead");
  assert.deepEqual(quickBids(lead, maya.id), [], "the leader has no quick bids");
  const passed = await compose("t-auction-timed-passed");
  assert.deepEqual(quickBids(passed, maya.id), [], "a player who passed is out");
});

test("idle: only from real data (the log's newest time while the game waits on a person)", async () => {
  const state = await compose("t-host-idle");
  const sam = byName(state, "Sam");
  assert.equal(lastMoveAt(state), NOW - 100000);
  assert.equal(idleMs(state, sam.id, NOW), 100000);
  assert.equal(idleShown(state, sam.id, NOW), 100000);
  assert.equal(minutesText(idleShown(state, sam.id, NOW)), "1:40");
  assert.equal(idleShown(state, sam.id, NOW - 100000 + IDLE_SHOW_MS - 1), null, "under 30 s is just thinking");
  assert.equal(idleMs(state, byName(state, "Maya").id, NOW), null, "not waited on");
  assert.equal(idleMs(state, byName(state, "Alpha").id, NOW), null, "a computer is never idle");
  const noTimes = structuredClone(state);
  for (const entry of noTimes.log) delete entry.at;
  assert.equal(idleMs(noTimes, sam.id, NOW), null, "no times in the log: no idle shown, never a guess");
  const standIn = await compose("t-standin-computer");
  assert.equal(idleMs(standIn, byName(standIn, "Sam").id, NOW), null, "the computer already plays for them");
  assert.equal(standInMs(byName(standIn, "Sam"), NOW), 40000);
});

test("host controls: what each player offers, and the requests they send", async () => {
  const state = await compose("t-host-idle");
  const sam = byName(state, "Sam");
  assert.deepEqual(hostControls(state, sam.id), { standin: true, skip: true, remove: true });
  assert.deepEqual(hostControls(state, byName(state, "Alpha").id), { standin: false, skip: false, remove: true }, "a computer: remove only");
  assert.deepEqual(hostControls(state, byName(state, "Maya").id), { standin: true, skip: false, remove: true }, "skip only while the game waits on them");
  const standIn = await compose("t-standin-computer");
  assert.equal(hostControls(standIn, byName(standIn, "Sam").id).standin, false, "already played by the computer");
  const lobby = structuredClone(state);
  lobby.status = "lobby";
  assert.deepEqual(hostControls(lobby, sam.id), { standin: false, skip: false, remove: false });

  for (const kind of ["standin", "skip", "remove"]) {
    assert.deepEqual(hostRequest(kind, "NX GNT", "tok", sam.id), { method: "POST", path: `/api/rooms/NX%20GNT/${kind}`, body: { token: "tok", playerId: sam.id } });
  }
  assert.throws(() => hostRequest("kick", "NXGNT", "tok", sam.id));

  const remove = await compose("t-host-remove");
  assert.deepEqual(removalSummary(remove, byName(remove, "Sam").id), { deeds: 7, houses: 6, cash: 1145 });
});

test("host controls: the client posts the request with the table token", async () => {
  const sent = [];
  const fake = Object.create(EstateClient.prototype);
  fake.request = (method, path, body) => {
    sent.push({ method, path, body });
    return Promise.resolve({ ok: true });
  };
  await fake.hostControl("NXGNT", "table-token", "skip", "player_1");
  assert.deepEqual(sent, [{ method: "POST", path: "/api/rooms/NXGNT/skip", body: { token: "table-token", playerId: "player_1" } }]);
});

test("I'M BACK and the while-away list", () => {
  const sam = { id: "p1", name: "Sam", input: "phone", cash: 1100, autopilot: true, standIn: { since: NOW - 60000, logId: "l2", cash: 1195, by: "idle" } };
  assert.equal(showBack(sam), true);
  assert.equal(showAwayList(sam), false, "the list waits for the hand-back");
  assert.equal(showBack({ ...sam, input: "ai" }), false, "never for a computer");
  const back = { ...sam, autopilot: false, standIn: { ...sam.standIn, until: NOW } };
  assert.equal(showBack(back), false);
  assert.equal(showAwayList(back), true);
  assert.equal(showAwayList({ ...back, standIn: { ...back.standIn, until: 0 } }), false);

  const log = [
    { id: "l1", text: "Sam's turn", at: NOW - 70000 },
    { id: "l2", text: "Computer plays for Sam", at: NOW - 60000 },
    { id: "l3", text: "Sam rolled 3 + 4 = 7", at: NOW - 50000 },
    { id: "l4", text: "Sam paid $50 rent to Alpha", at: NOW - 40000 },
    { id: "l5", text: "Maya's turn", at: NOW - 30000 },
  ];
  const state = { log, players: [back] };
  const { lines, more } = awayLines(state, back);
  assert.deepEqual(lines.map((line) => line.text), ["Rolled 3 + 4 = 7", "Paid $50 rent to Alpha", "Maya's turn"], "after logId; their own name becomes 'you' words");
  assert.deepEqual(lines.map((line) => line.mine), [true, true, false]);
  assert.equal(more, 0);
  assert.equal(awayLines(state, back, { limit: 2 }).more, 1, "the newest few, and how many were left out");
  const gone = { ...back, standIn: { ...back.standIn, logId: "rolled-away" } };
  assert.equal(awayLines(state, gone).lines.length, 5, "a logId no longer in the log: everything kept");
  const empty = { ...back, standIn: { ...back.standIn, logId: "" } };
  assert.equal(awayLines(state, empty).lines.length, 5, "logId \"\": everything");
  assert.equal(awayLines(state, { ...back, standIn: null }).lines.length, 0);
  assert.equal(awayLines({ log: [{ id: "x", text: "Sam's turn" }] }, { ...empty }).lines[0].text, "Your turn");
  assert.deepEqual(awayCash(back), { then: 1195, now: 1100, change: -95 });
});

test("rent ×2 / ×10: from base, multiplier and dice", async () => {
  assert.deepEqual(rentMultiple({ kind: "rent", base: 25, multiplier: 2, amount: 50 }), { multiplier: 2, base: 25, dice: null, amount: 50 });
  assert.deepEqual(rentMultiple({ kind: "rent", base: 7, multiplier: 10, dice: [3, 4], amount: 70 }), { multiplier: 10, base: 7, dice: [3, 4], amount: 70 });
  assert.equal(rentMultiple({ kind: "rent", base: 25, multiplier: 2 }).amount, 50, "no amount: base × multiplier");
  assert.equal(rentMultiple({ kind: "rent", amount: 50 }), null, "plain rent: no tag");
  assert.equal(rentMultiple({ kind: "rent", base: 25, multiplier: 1 }), null);
  assert.equal(rentMultiple({ kind: "rent", base: 7, multiplier: 10, dice: [3] }).dice, null, "dice need two faces");
  const x2 = await compose("t-rent-x2");
  assert.equal(rentMultiple(x2.lastLanding).multiplier, 2);
});

test("debt checklist: owe, cash, still to find; PAY once the ticks cover it", async () => {
  const state = await compose("pad-debt");
  const payer = state.debt.payerId;
  const list = debtChecklist(state, payer);
  assert.equal(list.owe, state.debt.amount);
  assert.equal(list.short, Math.max(0, list.owe - list.cash));
  assert.equal(list.toFind, Math.max(0, list.owe - list.cash - list.raised));
  assert.equal(list.covered, list.toFind === 0);
  if (list.covered) assert.equal(list.actions.at(-1).type, "pay_debt", "PAY runs the ticked rows, then pays");
  const short = debtChecklist(state, payer, ["mortgage:24"]);
  assert.ok(short.toFind > 0, "one deed ticked isn't enough");
  assert.equal(short.covered, false);
  assert.ok(!short.actions.some((action) => action.type === "pay_debt"), "no PAY while short");
  const first = list.mortgage[0] || list.sell[0];
  assert.ok(first);
  const untick = toggleDebtRow(list, first.key);
  assert.equal(untick.includes(first.key), !list.picked.includes(first.key));
  for (const action of list.actions) assert.ok(["mortgage", "sell_building", "pay_debt"].includes(action.type), "existing actions only");
});
