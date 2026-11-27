// UX round on the TV (docs/API.md §3c, boards UX-01…UX-08): the timed auction's quick bids and
// clock, the debt checklist's PAY, RENT ×2 / ×10, the host's PLAYERS page and REMOVE confirm, the
// stand-in card and COMPUTER tag, the CONTROLS key map, the NEW kite and PLAY AGAIN?.
// Run: node --test web/tests/ux-table.check.mjs

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { composeFixture } from "../table/demo.js";
import * as views from "../table/views.js";

const here = dirname(fileURLToPath(import.meta.url));
const load = async (name) => JSON.parse(readFileSync(join(here, "..", "fixtures", `${name}.json`), "utf8"));
const server = (await load("server")).server;
const source = readFileSync(join(here, "..", "table", "table.js"), "utf8");
const NOW = 1793560000000;

async function scene(name) {
  const fixture = await composeFixture(name, load);
  const state = fixture.state;
  const open = fixture.ui.open || {};
  const ctx = { server, now: Number(state.now) || NOW, demo: true };
  if (open.kind === "ticks") ctx.debtPicks = { [views.debtKey(state)]: open.ticks };
  return { state, ctx, open };
}
const text = (html) => String(html).replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();
const byName = (state, name) => state.players.find((player) => player.name === name);
const rowsOf = (model) => model.rows.map((row) => ({ button: row.button, key: row.key, label: row.label, detail: row.detail, action: row.act?.action || null }));

test("timed auction: A / X / Y bid +$10 / +$50 / +$100 over the top bid, B passes", async () => {
  const { state, ctx } = await scene("t-auction-timed");
  const model = views.panelModel(state, ctx);
  assert.equal(model.timed, true);
  assert.deepEqual(rowsOf(model), [
    { button: "A", key: "A", label: "$190", detail: "+$10", action: { type: "bid", amount: 190 } },
    { button: "X", key: "X", label: "$230", detail: "+$50", action: { type: "bid", amount: 230 } },
    { button: "Y", key: "Y", label: "$280", detail: "+$100", action: { type: "bid", amount: 280 } },
    { button: "B", key: "B", label: "Pass", detail: "", action: { type: "pass_auction" } },
  ]);
  const keyboard = await scene("t-auction-timed-keyboard");
  assert.deepEqual(rowsOf(views.panelModel(keyboard.state, keyboard.ctx)).map((row) => row.key), ["A", "X", "Y", "B"], "a keyboard seat presses the same letters");
});

test("timed auction: a quick bid the seat can't afford is gone", async () => {
  const { state, ctx } = await scene("t-auction-timed-going");
  assert.equal(byName(state, "Maya").cash, 250);
  assert.deepEqual(rowsOf(views.panelModel(state, ctx)).map((row) => row.detail), ["+$10", "+$50", ""], "no +$100 ($280 > $250)");
});

test("timed auction: the clock counts down to endsAt; GOING ONCE at 3 s", async () => {
  const running = await scene("t-auction-timed");
  const stage = views.stageHtml(running.state, running.ctx);
  assert.match(text(stage), /\b6 SECONDS\b/, "5.4 s left reads 6");
  assert.doesNotMatch(stage, /auc-clock[^"]*is-going/);
  const going = await scene("t-auction-timed-going");
  const late = views.stageHtml(going.state, going.ctx);
  assert.match(text(late), /\b3 SECONDS\b/);
  assert.match(late, /auc-clock[^"]*is-going/);
  assert.match(text(late), /TOP BID \$180 SAM/);
});

test("timed auction: YOU LEAD waits greyed; a seat that passed has no buttons", async () => {
  const lead = await scene("t-auction-timed-lead");
  const model = views.panelModel(lead.state, lead.ctx);
  assert.match(text(views.panelHtml(model)), /YOU LEAD/);
  assert.ok(model.rows.length > 0 && model.rows.every((row) => !row.act), "the leader's buttons wait");
  const passed = await scene("t-auction-timed-passed");
  assert.deepEqual(views.panelModel(passed.state, passed.ctx).rows, []);
  assert.match(text(views.stageHtml(passed.state, passed.ctx)), /MAYA PASSED/);
});

test("debt: PAY lights up once the ticks cover it, else it shows what's still to find", async () => {
  const covered = await scene("pad-debt");
  const pay = views.panelModel(covered.state, covered.ctx).rows[0];
  assert.equal(pay.cmd, "apply-plan");
  assert.ok(!pay.off);
  assert.equal(pay.plan.actions.at(-1).type, "pay_debt");
  const short = await scene("t-debt-checklist-short");
  const off = views.panelModel(short.state, short.ctx).rows[0];
  assert.equal(off.off, true);
  assert.equal(off.act, null);
  assert.equal(off.detail, "Find $22");
  assert.match(text(views.stageHtml(short.state, short.ctx)), /STILL TO FIND \$22/);
  const list = views.debtListHtml(short.state, views.tableDebtList(short.state, short.ctx));
  assert.ok((list.match(/data-debt-tick=/g) || []).length <= views.DEBT_ROWS, "a short checklist (every deed is still in DEEDS)");
});

test("rent ×2 / ×10: the tag, the sum and the receipt from base, multiplier and dice", async () => {
  const x2 = await scene("t-rent-x2");
  const stage2 = text(views.stageHtml(x2.state, x2.ctx));
  assert.match(stage2, /RENT ×2/);
  assert.match(stage2, /\$25 × 2 = \$50/);
  const receipt2 = text(views.panelHtml(views.panelModel(x2.state, x2.ctx)));
  assert.match(receipt2, /PAID SAM \$50/);
  const x10 = await scene("t-rent-x10");
  const stage10 = views.stageHtml(x10.state, x10.ctx);
  assert.match(text(stage10), /RENT ×10/);
  assert.match(stage10, /⚂/);
  assert.match(stage10, /⚃/);
  assert.match(text(stage10), /× 10 = \$70/);
  assert.match(text(views.panelHtml(views.panelModel(x10.state, x10.ctx))), /Roll 3 \+ 4/);
  const plain = structuredClone(x2.state);
  delete plain.lastLanding.multiplier;
  assert.doesNotMatch(views.stageHtml(plain, x2.ctx), /rent-tag/, "plain rent: no tag");
  const other = structuredClone(x2.state);
  other.currentPlayerId = byName(other, "Sam").id;
  assert.equal(views.rentMultipleNow(other), null, "only on the payer's own turn");
});

test("host: IDLE m:ss on the tray, from the log's time only", async () => {
  const { state, ctx } = await scene("t-host-idle");
  assert.match(text(views.platesHtml(state, ctx)), /IDLE 1:40/);
  assert.doesNotMatch(text(views.platesHtml(state, { ...ctx, now: NOW - 100000 + 20000 })), /IDLE/, "20 s is not idle");
});

test("host: PLAYERS lists everyone; the open row's COMPUTER PLAYS / SKIP TURN / REMOVE", async () => {
  const { state, ctx } = await scene("t-host-players");
  const sam = byName(state, "Sam");
  const alpha = byName(state, "Alpha");
  const html = views.playersOverlayHtml(state, { now: ctx.now, open: sam.id });
  assert.match(text(html), /SAM phone · to roll IDLE 1:40/);
  for (const kind of ["standin", "skip", "remove"]) assert.match(html, new RegExp(`data-host="${kind}" data-arg="${sam.id}"`));
  assert.match(html, /data-host="standin"[^>]*data-autofocus/, "focus starts on COMPUTER PLAYS, never REMOVE");
  assert.match(text(html), /ALPHA computer COMPUTER/);
  const robot = views.playersOverlayHtml(state, { now: ctx.now, open: alpha.id });
  assert.doesNotMatch(robot, /data-host="(standin|skip)"/, "a computer can only be removed");
  assert.match(robot, new RegExp(`data-host="remove" data-arg="${alpha.id}"`));
  assert.equal(views.defaultHostPick(state, ctx.now), sam.id, "opens on the idle player");
  const menu = text(views.menuOverlayHtml(state, { controls: true, host: true }));
  assert.match(menu, /CONTROLS PLAYERS HOST/);
  assert.doesNotMatch(text(views.menuOverlayHtml(state, {})), /PLAYERS/);
});

test("host: REMOVE asks first, says where everything goes, focus on KEEP", async () => {
  const { state, ctx } = await scene("t-host-remove");
  const html = views.removeConfirmHtml(state, byName(state, "Sam").id, ctx);
  assert.match(text(html), /REMOVE SAM\? IDLE 1:40 · TO ROLL DEEDS ×7 BANK HOUSES ×6 BANK CASH \$1,145 BANK SEAT CLOSED REMOVE SAM KEEP/);
  assert.match(html, /data-cmd="close" data-autofocus>KEEP/);
  assert.doesNotMatch(html, /data-cmd="confirm"[^>]*data-autofocus/);
});

test("host: requests go through the client with the table's token; REMOVE only after its confirm", () => {
  const send = source.match(/async function sendHost\([^)]*\) \{[\s\S]*?\n\}/)?.[0] || "";
  assert.match(send, /ui\.client\.hostControl\(ui\.session\.code, ui\.session\.token, kind, playerId\)/);
  const action = source.match(/function hostAction\([^)]*\) \{[\s\S]*?\n\}/)?.[0] || "";
  assert.match(action, /canHost\(\)/);
  assert.match(action, /kind === "remove"[\s\S]*?kind: "confirm"[\s\S]*?onConfirm: \(\) => sendHost\("remove"/);
  assert.match(source, /function canHost\(\) \{\s*return ui\.state\?\.viewerRole === "table"/, "the table screen is the host");
});

test("stand-in: COMPUTER on the tray and the card (does / never / back)", async () => {
  const { state, ctx } = await scene("t-standin-computer");
  assert.match(text(views.platesHtml(state, ctx)), /COMPUTER/);
  const panel = text(views.panelHtml(views.panelModel(state, ctx)));
  assert.match(panel, /COMPUTER PLAYS FOR SAM DOES ROLL BUY BID PAY NEVER TRADE LOAN BUILD BACK ANY BUTTON · SAM'S PHONE/);
  const idle = await scene("t-host-idle");
  assert.doesNotMatch(text(views.panelHtml(views.panelModel(idle.state, idle.ctx))), /COMPUTER PLAYS/);
});

test("controls: one key map, the same words and order on pad, keyboard and phone", () => {
  const html = views.controlsOverlayHtml();
  const rows = [...html.matchAll(/<tr><td class="controls__pad">[\s\S]*?<\/tr>/g)].map((match) => text(match[0]));
  assert.deepEqual(rows.map((row) => row.match(/\b(MAIN|SECOND|THIRD|BACK|DEEDS|MENU)\b/)[0]), ["MAIN", "SECOND", "THIRD", "BACK", "DEEDS", "MENU"]);
  assert.deepEqual(views.KEY_MAP.map((entry) => entry.button), ["A", "X", "Y", "B", "VIEW", "START"]);
  assert.deepEqual(views.KEY_MAP.map((entry) => entry.keys.join("+")), ["A+↵", "X", "Y", "B+Esc", "V", "M"]);
  assert.match(rows[3], /B ESC PASS/);
  assert.match(html, /data-cmd="close" data-autofocus/);
});

test("tokens: taken ones greyed with their owner, the kite marked NEW", async () => {
  const { state } = await scene("t-seat-kite");
  const html = views.seatOverlayHtml(state, { input: "keyboard", tokenKind: "kite", name: "Kite" }, { server });
  const kite = html.match(/<button[^>]*data-token="kite"[\s\S]*?<\/button>/)?.[0] || "";
  assert.match(kite, /token-choice__new">NEW</);
  assert.equal((html.match(/token-choice__new/g) || []).length, 1, "only the kite is new");
  assert.match(html, /class="token-choice is-taken" data-token="bicycle"[^>]*disabled/);
  assert.match(text(html), /BICYCLE SAM/);
  assert.match(text(html), /SEATS 4 \/ 8/);
});

test("play again: asks first, the same seats' tokens, focus on NOT YET", async () => {
  const ask = source.match(/function askRematch\(\) \{[\s\S]*?\n\}/)?.[0] || "";
  assert.match(ask, /tokens: views\.rematchTokensHtml\(ui\.state\)/);
  const { state } = await scene("t-victory-again");
  const html = views.confirmOverlayHtml({ title: "PLAY AGAIN?", text: "SAME PLAYERS · NEW GAME", confirm: "PLAY AGAIN", cancel: "NOT YET", tokens: views.rematchTokensHtml(state), icon: "resume" });
  assert.equal((html.match(/<svg[^>]*class="[^"]*token-svg/g) || []).length, state.players.length);
  assert.match(html, /data-cmd="close" data-autofocus>NOT YET/);
  assert.doesNotMatch(html, /data-cmd="confirm"[^>]*data-autofocus/);
});
