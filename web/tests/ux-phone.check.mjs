// UX round, phone side (boards UX-01..08 · Phone): the timed auction page, the host's idle bar,
// PLAYERS and REMOVE sheet, the debt checklist and loan terms card, rent ×2 / ×10, the stand-in
// cards, the token grid and the button words. Renders the ux-* scenes (web/phone/scenarios.js)
// through web/phone/*.js in Node. The shared helpers have their own checks (ux-shared.check.mjs).
// Run: node --test web/tests/ux-phone.check.mjs

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { SCENES, buildScene } from "../phone/scenarios.js";
import { renderPhone, route } from "../phone/screens.js";
import { DEBT_ROWS, debtRowsShown, removeSheetHtml, tokenFieldHtml } from "../phone/ux-screens.js";
import { debtChecklist } from "../shared/debt-checklist.js";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "fixtures");
const load = (name) => JSON.parse(readFileSync(join(dir, `${name}.json`), "utf8"));
const byName = (state, name) => state.players.find((player) => player.name === name);

// Words a person reads (tags and SVG stripped, entities decoded).
const text = (html) => String(html || "").replace(/<svg[\s\S]*?<\/svg>/g, " ").replace(/<[^>]+>/g, " ")
  .replace(/&#0?39;/g, "'").replace(/&amp;/g, "&").replace(/&quot;/g, "\"").replace(/\s+/g, " ").trim();
const acts = (html) => [...String(html).matchAll(/data-act='([^']*)'/g)].map((match) => JSON.parse(match[1]));
const BAD = [/undefined/, /\bNaN\b/, /\[object /, />null</, /\$-/, /Infinity/];
const clean = (html, label) => { for (const pattern of BAD) assert.doesNotMatch(String(html), pattern, `${label}: ${pattern}`); };

// A scene's state and the view phone.js would build for it (demo: the clock is state.now).
function scene(id, extra = {}) {
  const found = SCENES.find((entry) => entry.id === id);
  assert.ok(found, `scene ${id}`);
  const { state, ui } = buildScene(found, { [found.fixture || "roll"]: load(found.fixture || "roll") });
  const view = {
    session: found.session === false ? null : { code: state.code }, tab: found.tab || "turn",
    trade: { targetId: null, offer: [], request: [], offerCash: 0, requestCash: 0 }, friends: { list: [], sent: {} },
    join: { name: "", tokenKind: "rocket", ...(ui.join || {}) }, serverNow: state.now, ...ui, ...extra,
  };
  if (ui.join) view.join = { name: "", tokenKind: "rocket", ...ui.join };
  const out = renderPhone(found.session === false ? null : state, view);
  for (const part of ["header", "banner", "main", "nav"]) clean(out[part], `${id}/${part}`);
  return { state, view, out, main: out.main, all: `${out.banner || ""}${out.main}` };
}

const UX_SCENES = ["ux-tokens", "ux-standin", "ux-youre-back", "ux-host-idle-board", "ux-auction-timed", "ux-auction-timed-lead", "ux-auction-timed-passed",
  "ux-debt-none", "ux-rent-x2", "ux-rent-x10", "ux-loan-terms", "ux-host-players", "ux-host-remove"];

test("ux scenes: each renders without leaks, on a fixed game clock", () => {
  for (const id of UX_SCENES) {
    const { state, out } = scene(id);
    assert.ok(out.main.length > 40 || out.board, `${id}: shows something`);
    if (!["ux-tokens", "ux-debt-none", "ux-loan-terms"].includes(id)) assert.ok(state.now > 0, `${id}: state.now is set`);
  }
  const lead = scene("ux-auction-timed-lead").state;
  assert.equal(lead.auction.endsAt - lead.now, 2000, "the lead scene is in GOING ONCE");
  assert.equal(scene("ux-auction-timed").state.auction.endsAt - lead.now, 6000);
});

// ---- UX-03 Timed auction ----------------------------------------------------------------------

test("UX-03 timed auction: its own page with the clock, one tap per bid, PASS; the turn-order auction is unchanged", () => {
  const bidding = scene("ux-auction-timed");
  assert.equal(route(bidding.state, bidding.view), "timedAuction");
  assert.ok(!bidding.out.nav, "nav hidden, as on the board");
  assert.match(bidding.main, /class="tauc"[^>]*data-auc-clock/, "the clock node tickAuctionClock updates");
  assert.match(bidding.main, /data-auc-text>0:06</);
  const bids = acts(bidding.main);
  assert.deepEqual(bids.filter((act) => act.type === "bid").map((act) => act.amount), [190, 230, 280], "+$10 / +$50 / +$100");
  assert.ok(bids.some((act) => act.type === "pass_auction"), "PASS");
  assert.match(text(bidding.main), /TOP BID \$180 SAM/);
  assert.match(text(bidding.main), /ONE TAP = BID CASH \$1,020/);
  assert.match(text(bidding.main), /MAYA PASSED/);
  assert.match(text(bidding.out.ambient?.status || ""), /AUCTION · SAM LEADS/);

  const lead = scene("ux-auction-timed-lead");
  assert.match(lead.main, /class="tauc is-going"/, "under 3 s: GOING ONCE");
  assert.match(text(lead.main), /YOU LEAD GOING ONCE SOLD IN 2/);
  assert.match(lead.main, /class="tbid tbid--main"[^>]*disabled/, "the leader can't bid against themself");
  assert.match(text(lead.main), /SAM · ALPHA CAN STILL BID/);

  const passed = scene("ux-auction-timed-passed");
  assert.match(text(passed.main), /PASSED/);
  assert.equal(acts(passed.main).filter((act) => act.type === "bid" || act.type === "pass_auction").length, 0, "no bids once passed");

  const turnOrder = load("auction").state;
  assert.equal(route(turnOrder, { session: { code: turnOrder.code }, tab: "turn" }), "auction");
  assert.match(renderPhone(turnOrder, { session: { code: turnOrder.code }, tab: "turn", friends: { list: [], sent: {} } }).main, /class="bidbar"/);
});

// ---- UX-02 Host controls ----------------------------------------------------------------------

test("UX-02 idle bar: the host sees SAM IDLE m:ss with PLAYERS on board screens, from 30 s; guests never", () => {
  const board = scene("ux-host-idle-board");
  assert.match(text(board.out.banner), /^SAM IDLE 1:40 PLAYERS$/);
  assert.match(board.out.banner, /data-tick-since="\d+"/, "ticks every second");
  assert.match(board.out.banner, /data-cmd="host-players" data-arg="player_000000000005"/);
  const early = scene("ux-host-idle-board", { serverNow: board.state.now - 75000 });
  assert.equal(text(early.out.banner), "", "25 s is not idle yet");
  const guestState = { ...board.state, viewerIsHost: false, hostId: byName(board.state, "Sam").id };
  const guest = renderPhone(guestState, { ...board.view });
  assert.equal(text(guest.banner), "", "only the host");
  const noClock = renderPhone({ ...board.state, now: 0 }, { ...board.view, serverNow: null });
  assert.equal(text(noClock.banner), "", "no clock, no idle time");
});

test("UX-02 PLAYERS in MORE: you first, tags, and COMPUTER PLAYS / SKIP TURN / REMOVE for the open row", () => {
  const { main, state } = scene("ux-host-players");
  const sam = byName(state, "Sam").id;
  const words = text(main);
  assert.match(words, /PLAYERS YOU phone · host ONLINE SAM phone · to roll IDLE 1:40 COMPUTER PLAYS SKIP TURN REMOVE ALPHA computer COMPUTER MAYA controller ONLINE/);
  for (const cmd of ["host-standin", "host-skip", "host-remove-ask"]) assert.match(main, new RegExp(`data-cmd="${cmd}" data-arg="${sam}"`), cmd);
  assert.match(main, /class="hrow is-open"/);
  const closed = scene("ux-host-players", { hostOpen: null }).main;
  assert.doesNotMatch(closed, /host-standin/, "controls only for the open row");
  assert.match(closed, new RegExp(`data-cmd="host-open" data-arg="${sam}"`));
  const guest = renderPhone({ ...state, viewerIsHost: false, hostId: sam }, { session: { code: state.code }, tab: "friends", friends: { list: [], sent: {} }, serverNow: state.now });
  assert.doesNotMatch(guest.main, /class="hrows"/, "guests have no host controls");
});

test("UX-02 REMOVE sheet: what goes back to the bank, the seat closes, REMOVE / KEEP", () => {
  const { state } = scene("ux-host-remove");
  const sam = byName(state, "Sam").id;
  const sheet = removeSheetHtml(state, sam, { serverNow: state.now });
  clean(sheet, "remove sheet");
  const words = text(sheet);
  assert.match(words, /REMOVE SAM\? IDLE 1:40 · TO ROLL/);
  assert.match(words, /DEEDS ×\d+ BANK HOUSES ×6 BANK CASH \$1,085 BANK SEAT CLOSED/);
  assert.match(sheet, /data-sheet-cmd="confirm"[^>]*>[\s\S]*REMOVE SAM/);
  assert.match(sheet, /data-sheet-cmd="close"/);
  assert.match(SCENES.find((entry) => entry.id === "ux-host-remove").then.remove, /^Sam$/);
});

test("UX-02 phone.js: host requests go through client.hostControl; demos toast Demo · would …; a removed phone leaves", () => {
  const source = readFileSync(join(here, "..", "phone", "phone.js"), "utf8");
  assert.match(source, /ui\.client\.hostControl\(/);
  assert.match(source, /Demo · would /);
  assert.match(source, /removed you/, "the host removed you ends the session");
  assert.match(source, /setInterval\(tickClocks, 250\)/, "the clocks tick without a new state");
});

// ---- UX-04 Debt checklist and loan terms ------------------------------------------------------

test("UX-04 debt: OWE / CASH / TO FIND, at most four short-named rows to tick, PAY by the plan", () => {
  const debt = load("debt").state;
  const out = renderPhone(debt, { session: { code: debt.code }, tab: "turn", friends: { list: [], sent: {} } });
  assert.ok(!out.nav, "nav hidden, as on the board");
  const words = text(out.main);
  assert.match(words, /^YOU OWE MAYA · PINE ROAD OWE \$550 CASH \$391 TO FIND \$0 MORTGAGE/);
  assert.match(words, /GARDEN AVE \+\$70 MUSEUM RD \+\$110/, "short names (Ave, Rd)");
  const rows = [...out.main.matchAll(/class="drow(?: is-[a-z]+)*"/g)];
  assert.ok(rows.length <= DEBT_ROWS, `${rows.length} rows`);
  assert.equal(rows.length, debtRowsShown(debtChecklist(debt, debt.viewerId)).length);
  assert.match(out.main, /data-cmd="debt-tick" data-arg="mortgage:\d+"/);
  assert.match(out.main, /class="drow is-blocked" disabled/, "a deed waiting on houses is locked");
  assert.match(out.main, /data-cmd="debt-pay"/, "the plan covers it: PAY sends the plan, then pay_debt");
  assert.match(words, /GO BANKRUPT$/);

  const none = scene("ux-debt-none");
  assert.match(text(none.main), /TO FIND \$159/);
  assert.match(none.main, /PAY \$550[\s\S]*FIND \$159[\s\S]*disabled|disabled[\s\S]*PAY \$550/, "nothing ticked: PAY greyed with what is missing");
  assert.doesNotMatch(none.main, /data-cmd="debt-pay"/);

  const rich = { ...debt, players: debt.players.map((player) => (player.id === debt.viewerId ? { ...player, cash: 600 } : player)) };
  assert.ok(acts(renderPhone(rich, { session: { code: debt.code }, tab: "turn", friends: { list: [], sent: {} } }).main).some((act) => act.type === "pay_debt"), "enough cash: PAY is pay_debt");

  const loans = load("debt-loans").state;
  const withLoans = renderPhone(loans, { session: { code: loans.code }, tab: "turn", friends: { list: [], sent: {} } }).main;
  assert.match(withLoans, /data-cmd="loan-ask" aria-label="ASK FOR A LOAN"/);
});

test("UX-04 MY LOANS: the terms on one card (borrowed, interest, repay), due, security and AFTER REPAY", () => {
  const { main } = scene("ux-loan-terms");
  const words = text(main);
  assert.match(words, /MY LOANS FROM ALPHA DUE IN 2 TURNS Borrowed \$400 Interest \+\$80 REPAY \$480 TURNS LEFT SECURITY/);
  assert.match(words, /MISSED → CENTRAL ROAD GOES TO ALPHA AFTER REPAY \$1,020 → \$540 REPAY \$480/);
  assert.match(main, /aria-label="You owe Alpha \$480"/);
  assert.equal((main.match(/<div class="lcard__pips">[\s\S]*?<\/div>/)?.[0].match(/<i><\/i>/g) || []).length, 2, "two turns left");
  assert.ok(acts(main).some((act) => act.type === "repay_loan"), "REPAY");
  assert.doesNotMatch(main, /data-act='\{"type":"repay_loan"[^']*'[^>]*disabled/, "your turn and enough cash: REPAY is live");
});

// ---- UX-05 Rent ×2 / ×10 ----------------------------------------------------------------------

test("UX-05 rent ×2 / ×10: the stamp, the card, the sum; plain rent stays plain", () => {
  const x2 = text(scene("ux-rent-x2").main);
  assert.match(x2, /^RENT ×2 WEST STATION/);
  assert.match(x2, /\$25 × 2 = \$50/);
  assert.match(x2, /STRIKE CALLED OFF/);
  const x10 = scene("ux-rent-x10");
  assert.match(text(x10.main), /RENT ×10 RESERVOIR/);
  assert.match(text(x10.main), /NEW ROLL = 7 7 × 10 = \$70/);
  assert.match(x10.main, /class="rentx rentx--dice"/);
  assert.match(text(x10.out.board.stage), /RENT ×10 · RESERVOIR/);
  const plain = load("rent").state;
  assert.doesNotMatch(renderPhone(plain, { session: { code: plain.code }, tab: "turn", friends: { list: [], sent: {} } }).main, /class="rentx/);
});

// ---- UX-07 Stand-in ---------------------------------------------------------------------------

test("UX-07 stand-in: COMPUTER PLAYING with what it did and I'M BACK; then YOU'RE BACK with a dismiss", () => {
  const standin = scene("ux-standin");
  assert.equal(route(standin.state, standin.view), "standin");
  assert.match(text(standin.out.board.status), /COMPUTER PLAYING/);
  assert.match(text(standin.main), /^PLAYING FOR YOU IDLE 2:00 Rolled 3 \+ 4 City Park/);
  assert.match(standin.main, /data-act='\{"type":"back"\}'[^>]*>[\s\S]*I'M BACK|I&#039;M BACK/);
  assert.ok(acts(standin.main).every((act) => act.type === "back"), "only I'M BACK while the computer plays");

  const back = scene("ux-youre-back");
  assert.equal(route(back.state, back.view), "roll", "your seat again");
  assert.match(text(back.main), /YOU'RE BACK WHILE AWAY Rolled 3 \+ 4 City Park/);
  assert.doesNotMatch(text(back.main), /Your turn|Robin's turn/, "turn lines are left out");
  assert.match(back.main, /class="awaycard"[\s\S]*data-act='\{"type":"back"\}'/, "× dismisses (and tells the server)");
  assert.doesNotMatch(back.main, /class="glance"/, "the card takes the glance's place");
});

// ---- UX-08 Tokens -----------------------------------------------------------------------------

test("UX-08 tokens: n OF 8 FREE, 4 × 2 tiles, taken ones greyed with the owner's name, the kite marked NEW", () => {
  const { main } = scene("ux-tokens");
  assert.match(text(main), /Token 4 OF 8 FREE ROBIN ALPHA SAM MAYA KEY UMBRELLA ANCHOR NEW KITE/);
  assert.match(main, /class="token-grid token-grid--4"/);
  assert.equal((main.match(/class="token-pick[^"]*is-taken[^"]*"[^>]*disabled/g) || []).length, 4, "four taken, disabled");
  assert.match(main, /class="token-pick is-picked"[^>]*data-token="kite"|data-token="kite"[^>]*aria-pressed="true"/);
  const fresh = tokenFieldHtml({ pickedKind: "rocket", takenBy: new Map(), palette: null });
  assert.match(text(fresh), /8 OF 8 FREE/);
  assert.doesNotMatch(fresh, /disabled/, "nothing disabled in a new game");
  const kiteTaken = tokenFieldHtml({ pickedKind: "rocket", takenBy: new Map([["kite", "Kai"]]), palette: null });
  assert.doesNotMatch(kiteTaken, /token-pick__new/, "no NEW badge on a taken kite");
});

// ---- UX-01 Button words in key-map order ------------------------------------------------------

test("UX-01 words: ROLL; in jail ROLL · PAY $50 · USE PASS", () => {
  const roll = load("roll").state;
  const rollOut = renderPhone(roll, { session: { code: roll.code }, tab: "turn", friends: { list: [], sent: {} } });
  assert.match(text(`${rollOut.main}${rollOut.board?.stage || ""}`), /\bROLL\b/);
  const jail = load("jail").state;
  const jailOut = renderPhone(jail, { session: { code: jail.code }, tab: "turn", friends: { list: [], sent: {} } });
  const words = text(jailOut.main);
  assert.match(words, /ROLL[\s\S]*PAY \$50[\s\S]*USE PASS/, "A · B · X order");
});
