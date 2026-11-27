// Renders every fixture through the pure table views and phone screens and checks the output:
// no exceptions, no "undefined"/"NaN"/"[object Object]" leaking into the HTML, expected phone
// routes, and buttons that carry valid action JSON. Run: node web/tests/render.check.mjs

import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { diffEvents } from "../shared/events.js";
import { renderPhone, route } from "../phone/screens.js";
import * as views from "../table/views.js";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "fixtures");
const load = (name) => JSON.parse(readFileSync(join(dir, `${name}.json`), "utf8"));
const names = readdirSync(dir).filter((file) => file.endsWith(".json") && file !== "index.json").map((file) => file.replace(/\.json$/, ""));
const stateFixtures = names.filter((name) => load(name).state);

const BAD = [/undefined/, /\bNaN\b/, /\[object /, />null</, /\$-/, /Infinity/];

function clean(html, label) {
  for (const pattern of BAD) {
    const match = html.match(pattern);
    if (match) {
      const at = html.indexOf(match[0]);
      assert.fail(`${label}: found ${pattern} near: ${html.slice(Math.max(0, at - 80), at + 60)}`);
    }
  }
  for (const attr of html.matchAll(/data-act='([^']*)'/g)) {
    const action = JSON.parse(attr[1]);
    assert.equal(typeof action.type, "string", `${label}: action without type`);
  }
}

function asTable(state) {
  if (state.viewerRole === "table") return state;
  let pad = 0;
  const localSeats = state.players
    .filter((player) => player.input === "controller" || player.input === "keyboard")
    .map((player) => ({ seatId: `seat_${player.id}`, playerId: player.id, input: player.input, pad: player.input === "controller" ? pad++ : null }));
  return { ...state, viewerRole: "table", viewerId: null, localSeats };
}

const tableCtx = { server: load("server").server, padNames: { 0: "Xbox controller" }, pads: [{ index: 0, name: "Xbox controller", seated: true }], status: "live", demo: true };

test("table views render every fixture", () => {
  for (const name of stateFixtures) {
    const state = asTable(load(name).state);
    const label = `table/${name}`;
    if (state.status === "lobby") {
      clean(views.joinHtml(state, tableCtx), `${label} join`);
      continue;
    }
    clean(views.platesHtml(state, tableCtx), `${label} plates`);
    const model = views.panelModel(state, tableCtx);
    assert.ok(model.verb, `${label}: the now card says what is happening`);
    clean(views.panelHtml(model), `${label} panel`);
    clean(views.panelHtml(model, { busy: true }), `${label} busy panel`);
    clean(views.slotHtml(state, tableCtx), `${label} slot`);
    clean(views.tableCodeHtml(state.code), `${label} code`);
    for (const seat of state.localSeats || []) {
      clean(views.propertyOverlayHtml(state, seat, tableCtx), `${label} property overlay`);
      const partner = state.players.find((player) => player.id !== seat.playerId && !player.bankrupt);
      clean(views.tradeOverlayHtml(state, seat, { targetId: partner?.id, offer: [], request: [], offerCash: 50, requestCash: 0, quote: "" }, tableCtx), `${label} trade overlay`);
      clean(views.tradeOverlayHtml(state, seat, views.newTradeDraft(), tableCtx), `${label} trade overlay (fresh)`);
      clean(views.loanOverlayHtml(state, seat, { targetId: partner?.id, principal: 200, repay: 240, dueTurns: 6, collateral: [] }, tableCtx), `${label} loan overlay`);
    }
    if (state.status === "finished") clean(views.victoryOverlayHtml(state, tableCtx), `${label} victory`);
  }
});

test("table: controller seat gets action rows when it must act", () => {
  const pad = asTable(load("pad-turn").state);
  const model = views.panelModel(pad, tableCtx);
  const actions = model.rows.filter((row) => row.act || row.cmd);
  assert.ok(actions.length >= 3, "roll, trade and property rows");
  assert.equal(actions[0].act.action.type, "roll");
  assert.equal(actions[0].glyph, "A");
  assert.equal(model.verb, "ROLL");
  const debt = asTable(load("pad-debt").state);
  const debtRows = views.panelModel(debt, tableCtx).rows.filter((row) => row.act || row.cmd);
  assert.ok(debtRows.some((row) => row.cmd === "bankrupt"));
  const phone = asTable(load("phone-turn").state);
  const phoneModel = views.panelModel(phone, tableCtx);
  assert.equal(phoneModel.wait, "phone", "a phone icon with waiting dots, not a sentence");
  assert.equal(phoneModel.verb, "BUY?");
  assert.equal(phoneModel.rows.filter((row) => row.act).length, 0, "no buttons for phone players");
  assert.match(views.slotHtml(phone, tableCtx), /FOR SALE/); // deed for sale in the board centre
});

test("table: auction panel lists the leader and passed players", () => {
  const state = asTable(load("table-auction").state);
  const model = views.panelModel(state, tableCtx);
  assert.equal(model.verb, "BID?");
  assert.match(model.line, /now-amt">\$60</, "the bid is on the action card's one line");
  assert.match(views.slotHtml(state, tableCtx), /PEARL LANE/, "the square's name is in the show slot above the card");
  assert.match(model.chips, /is-lead/, "the leader's chip is marked");
  assert.match(model.chips, /is-out[^"]*"[\s\S]*?OUT/, "passed players are struck out");
  assert.match(views.slotHtml(state, tableCtx), /slot-auction__bid">\$60</);
});

const PHONE_ROUTES = {
  lobby: "lobby", "lobby-rules": "lobby", order: "order", "order-countdown": "order", roll: "roll", doubles: "roll", buy: "buy",
  auction: "auction", rent: "rent", "turn-done": "done", card: "card", jail: "jail", debt: "debt", "debt-loans": "debt",
  trade: "trade", "trade-sent": "trade", waiting: "waiting", victory: "result", defeat: "result",
  "loan-offer": "loan", "loan-request": "loan", "active-loan": "roll",
};

test("phone routes each fixture to the expected screen", () => {
  for (const [name, expected] of Object.entries(PHONE_ROUTES)) {
    const { state } = load(name);
    assert.equal(route(state, { session: { code: state.code }, tab: "turn" }), expected, name);
  }
});

test("phone renders every fixture on every tab without leaks", () => {
  const friends = load("friends").friends;
  for (const name of stateFixtures) {
    const { state } = load(name);
    if (state.viewerRole === "table") continue;
    for (const tab of ["turn", "property", "trade", "friends"]) {
      const ui = { session: { code: state.code }, tab, trade: { targetId: null, offer: [], request: [], offerCash: 0, requestCash: 0 }, friends: { list: friends, sent: {} }, inviteLink: "https://example.test/j/NXGNT", now: Date.UTC(2026, 10, 1) };
      const out = renderPhone(state, ui);
      for (const part of ["header", "banner", "main", "nav"]) clean(out[part], `phone/${name}/${tab}/${part}`);
    }
    const invite = renderPhone(state, { session: { code: state.code }, tab: "friends", inviteOpen: true, inviteLink: "https://example.test/j/NXGNT" });
    clean(invite.main, `phone/${name}/invite`);
  }
});

test("phone: all 16 approved screens are reachable", () => {
  const seen = new Set();
  const lobby = load("lobby").state;
  seen.add(renderPhone(null, { session: null, join: { code: "NXGNT", name: "Robin", tokenKind: "rocket", summary: { status: "lobby", players: lobby.players } } }).screen);
  seen.add(renderPhone(null, { session: null, invitedView: true, join: { code: "NXGNT", name: "Priya", summary: { code: "NXGNT", status: "lobby", host: lobby.players[0], players: lobby.players, openSeats: 2 } } }).screen);
  for (const name of Object.keys(PHONE_ROUTES)) seen.add(route(load(name).state, { session: {}, tab: "turn" }));
  const waiting = load("waiting").state;
  seen.add(route(waiting, { session: {}, tab: "property" }));
  seen.add(route(waiting, { session: {}, tab: "friends" }));
  seen.add(route(waiting, { session: {}, tab: "friends", inviteOpen: true }));
  for (const screen of ["join", "lobby", "roll", "buy", "auction", "rent", "waiting", "property", "debt", "jail", "card", "trade", "result", "invited", "friends", "invite"]) {
    assert.ok(seen.has(screen), `screen ${screen} reachable`);
  }
});

test("phone: loan screens and rules", () => {
  const offer = load("loan-offer").state;
  const out = renderPhone(offer, { session: {}, tab: "turn" });
  assert.match(out.main, /OFFERS A LOAN/);
  assert.match(out.main, /accept_loan/);
  assert.match(out.main, /You get \$300 now and owe Alpha \$360 within 6 turns\./);
  const request = renderPhone(load("loan-request").state, { session: {}, tab: "turn" });
  assert.match(request.main, /CANCEL REQUEST/);
  const debt = renderPhone(load("debt-loans").state, { session: {}, tab: "turn" });
  assert.match(debt.main, /ASK FOR A LOAN/);
  const plainDebt = renderPhone(load("debt").state, { session: {}, tab: "turn" });
  assert.doesNotMatch(plainDebt.main, /ASK FOR A LOAN/);
  const active = load("active-loan").state;
  const friends = renderPhone(active, { session: {}, tab: "friends", friends: { list: [], sent: {} } });
  assert.match(friends.main, /You owe Alpha \$480/);
  assert.match(friends.main, /Sam owes you \$240/);
  assert.match(friends.main, /repay_loan/);
  const property = renderPhone(active, { session: {}, tab: "property" });
  assert.match(property.main, /security for Alpha&#039;s loan/);
  const draft = renderPhone(active, { session: {}, tab: "trade", loanDraft: { direction: "lend", targetId: active.players.find((p) => p.name === "Sam").id, principal: 200, repay: 240, dueTurns: 6, collateral: [] } });
  assert.equal(draft.screen, "loanDraft");
  clean(draft.main, "phone/loanDraft");
  const lobby = renderPhone(load("lobby-rules").state, { session: {}, tab: "turn" });
  assert.match(lobby.main, /Loans between players/);
  assert.match(lobby.main, /data-cmd="rule"/);
  const table = views.panelModel(asTable(offer), tableCtx);
  assert.equal(table.verb, "LOAN?");
});

test("events: diffs between consecutive replay states produce animation events", () => {
  const replay = load("replay");
  const states = replay.replay.map((state) => ({ ...state, board: replay.board, groups: replay.groups }));
  const kinds = new Set();
  for (let index = 1; index < states.length; index += 1) {
    for (const event of diffEvents(states[index - 1], states[index])) {
      kinds.add(event.type);
      if (event.type === "move") assert.ok(event.path.length >= 2);
      if (event.type === "dice") assert.equal(event.dice.length, 2);
      if (event.type === "pay") assert.ok(event.amount > 0);
    }
  }
  assert.ok(kinds.has("dice") && kinds.has("move"), `saw ${[...kinds].join(", ")}`);
});
