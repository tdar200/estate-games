// Behaviour checks for the PC table views: one-glance layout (no history feed, tight word budget),
// the "now" card, the one-page trade (no preselection, only each side's own deeds, locked deeds
// greyed, stepper cash, the computer's price never applied by itself, cash-for-cash blocked), the
// busy card, the lobby's resume box and the getting-back-in gate. Run: node web/tests/table.check.mjs

import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { cellPosition } from "../shared/board.js";
import { cardPreview, ownedBy, tradeable } from "../shared/insights.js";
import * as camera from "../table/camera.js";
import * as views from "../table/views.js";

const here = dirname(fileURLToPath(import.meta.url));
const dir = join(here, "..", "fixtures");
const load = (name) => JSON.parse(readFileSync(join(dir, `${name}.json`), "utf8"));
const server = load("server").server;
const ctx = { server, padNames: { 0: "Xbox controller" }, pads: [{ index: 0, name: "Xbox controller", seated: true }], status: "live", demo: true };

function asTable(state) {
  if (state.viewerRole === "table") return state;
  let pad = 0;
  const localSeats = state.players
    .filter((player) => player.input === "controller" || player.input === "keyboard")
    .map((player) => ({ seatId: `seat_${player.id}`, playerId: player.id, input: player.input, pad: player.input === "controller" ? pad++ : null }));
  return { ...state, viewerRole: "table", viewerId: null, localSeats };
}

// Visible words: strip tags and screen-reader-only text, count tokens with a letter.
function words(html) {
  const text = String(html || "").replace(/<span class="sr-only">[^<]*<\/span>/g, " ").replace(/<[^>]+>/g, " ").replace(/&[a-z#0-9]+;/g, " ");
  return text.split(/\s+/).filter((word) => /[A-Za-z]/.test(word)).length;
}

// Everything on the game screen's side and board centre: brand, table code, plates, now card, slot.
function screenWords(state) {
  return 1 + words(views.tableCodeHtml(state.code)) + words(views.platesHtml(state, ctx)) + words(views.panelHtml(views.panelModel(state, ctx))) + words(views.slotHtml(state, ctx));
}

const padState = asTable(load("pad-turn").state);
const seat = padState.localSeats[0];
const me = padState.players.find((player) => player.id === seat.playerId);
const partner = padState.players.find((player) => player.id !== me.id && !player.bankrupt && ownedBy(padState, player.id).length);
const toggles = (html, field) => [...html.matchAll(new RegExp(`data-toggle="${field}:(\\d+)"`, "g"))].map((match) => Number(match[1]));

test("layout: no history feed or log list anywhere on the table", () => {
  const html = readFileSync(join(here, "..", "table", "index.html"), "utf8");
  assert.doesNotMatch(html, /id="feed"|AROUND THE TABLE/);
  assert.equal(views.feedHtml, undefined, "the feed view is gone");
  for (const name of ["pad-turn", "table-auction", "trade", "pad-debt"]) {
    const state = asTable(load(name).state);
    const html = views.panelHtml(views.panelModel(state, ctx));
    assert.doesNotMatch(html, /AROUND THE TABLE|feed__/);
  }
});

test("word budget: the game screen reads at a glance (≤ 30 words), the lobby ≤ 43", () => {
  for (const name of ["pad-turn", "phone-turn", "buy", "table-auction", "pad-debt", "trade", "card", "jail", "doubles", "rent", "order", "loan-offer"]) {
    const total = screenWords(asTable(load(name).state));
    assert.ok(total <= 30, `${name}: ${total} words`);
  }
  // The lobby grew a little on purpose (review 4 Nov #6, first-time players): the first open seat
  // says SCAN TO JOIN · or press A · Enter, and the chips say ADD COMPUTER / ADD KEYBOARD. The
  // game picker's third tile (LUDO, 0.11.0) is the 37th word. WHATSAPP / COPY LINK under the QR
  // code (0.11.1, a way to send the link) are the 38th to 40th. The picker's
  // fourth tile (QUIZ) is the 41st. The fifth (NEAR THING) is the 42nd and 43rd.
  for (const name of ["table-lobby", "table-empty"]) {
    const html = views.joinHtml(asTable(load(name).state), ctx);
    const total = words(html);
    assert.ok(total <= 43, `${name} lobby: ${total} words`);
    assert.match(html, /class="seat__hint"[^>]*><b>SCAN TO JOIN<\/b><small>or press A · Enter<\/small>/, `${name}: how to sit down, in words`);
    assert.match(html, /data-cmd="add-ai"[^>]*title="[^"]+"[^>]*>[\s\S]*?ADD COMPUTER/, `${name}: the computer chip says what it does`);
    assert.match(html, /data-cmd="ai-level"[^>]*title="[^"]+"/, `${name}: the level chip has a tooltip`);
  }
});

test("plates: token, name, cash, TURN and a deed count; no labels or device words", () => {
  const html = views.platesHtml(padState, ctx);
  assert.doesNotMatch(html, />CASH</, "no CASH label under the amount");
  assert.doesNotMatch(html, />NEXT</);
  assert.equal((html.match(/flag--turn">TURN</g) || []).length, 1, "one TURN flag");
  const deeds = ownedBy(padState, me.id).length;
  assert.match(html, new RegExp(`meta--deeds"[^>]*>[\\s\\S]*?<b>${deeds}</b>`), "deed count on the plate");
  const offline = structuredClone(padState);
  offline.players.find((player) => player.input === "phone").online = false;
  assert.match(views.platesHtml(offline, ctx), /is-offline/);
});

test("now card: who and what in a word or two, with icons instead of sentences", () => {
  const roll = views.panelModel(padState, ctx);
  assert.equal(roll.name, me.name.toUpperCase());
  assert.equal(roll.verb, "ROLL");
  assert.deepEqual(roll.rows.filter((row) => row.act || row.cmd).map((row) => row.label), ["Roll", "Trade", "Deeds"]); // UX-01: one key map, VIEW is DEEDS everywhere
  const phone = views.panelModel(asTable(load("phone-turn").state), ctx);
  assert.equal(phone.wait, "phone");
  assert.equal(phone.rows.length, 0);
  const debt = views.panelModel(asTable(load("pad-debt").state), ctx);
  assert.equal(debt.verb, "OWES");
  assert.ok(debt.badges.some((badge) => /^SHORT \$/.test(badge.text)));
  assert.ok(debt.rows.some((row) => row.cmd === "bankrupt"));
  const auctionState = asTable(load("table-auction").state);
  const auction = views.panelModel(auctionState, ctx);
  assert.equal(auction.verb, "BID?");
  // A seat here that is still in the auction keeps its rows, greyed, while someone else bids: the
  // card keeps its size instead of jumping with every bid (flash hunt, 3 Nov).
  assert.ok(auction.rows.length && auction.rows.every((row) => !row.act && !row.cmd), "rows stay, none live, while someone else bids");
  const padSeat = auctionState.localSeats.find((candidate) => candidate.input === "controller" && auctionState.auction.activeIds.includes(candidate.playerId));
  const padTurn = views.panelModel({ ...auctionState, auction: { ...auctionState.auction, currentPlayerId: padSeat.playerId } }, ctx);
  assert.ok(padTurn.legend.some((item) => item.glyph === "RB"), "controller legend when the pad seat bids");
  assert.deepEqual(padTurn.rows.map((row) => row.act?.action.type), ["bid", "pass_auction"]);
  const names = readdirSync(dir).filter((file) => file.endsWith(".json") && file !== "index.json").map((file) => file.replace(/\.json$/, ""));
  for (const name of names) {
    const data = load(name);
    if (!data.state || data.state.status === "lobby") continue;
    const html = views.panelHtml(views.panelModel(asTable(data.state), ctx));
    assert.doesNotMatch(html, /is deciding|is thinking|ON THEIR PHONE|EVERYONE BIDS|HIGHEST FIRST|Next roll looks safe|chance of landing|not available during the current phase/, `${name}: sentence in the now card`);
    for (const row of views.panelModel(asTable(data.state), ctx).rows) assert.ok(words(row.label) <= 3, `${name}: row "${row.label}" is short`);
  }
});

test("now card: while animations play the rows are inert, with no stale 'Roll' to press", () => {
  const model = views.panelModel(padState, ctx);
  assert.ok(model.rows.some((row) => row.act?.action.type === "roll"));
  const busy = views.panelHtml(model, { busy: true });
  assert.doesNotMatch(busy, /<button/);
  assert.doesNotMatch(busy, /data-row=/);
  assert.match(busy, /aria-disabled="true"/);
  assert.match(busy, /now__wait--busy/);
  assert.match(views.panelHtml(model), /<button class="panel__row panel__row--action"/);
});

test("trade: docked one-page panel; nobody picked, nothing selected; your own deeds only", () => {
  const html = views.tradeDockHtml(padState, seat, views.newTradeDraft(), ctx);
  assert.match(html, /class="trade-dock"/, "a docked panel, not a modal dialog");
  assert.doesNotMatch(html, /aria-modal|dialog--/);
  assert.doesNotMatch(html, /aria-pressed="true"/, "no partner or deed looks picked");
  assert.doesNotMatch(html, /is-on/);
  assert.match(html, /data-partner=/);
  assert.match(html, /YOU GIVE/);
  assert.match(html, /YOU GET/);
  assert.match(html, /PICK A PLAYER/);
  assert.doesNotMatch(html, /data-toggle="request:/, "nobody's deeds to get before a partner is picked");
  assert.doesNotMatch(html, /data-cmd="trade-next"|data-cmd="trade-back"|Step \d of/, "no wizard steps");
  assert.match(html, /data-cmd="trade-send" data-nav="foot" disabled/);
  assert.match(html, /data-cmd="close"/);
  const mine = tradeable(padState, me.id);
  assert.deepEqual(toggles(html, "offer").sort((a, b) => a - b), [...mine].sort((a, b) => a - b), "give side lists exactly my tradable deeds");
  assert.doesNotMatch(html, /<input/, "no $0 text box");
  assert.match(html, /data-cash="offer:50"/);
  assert.match(html, /data-cash="request:50" disabled/, "their cash waits for a partner");
  assert.match(html, /data-cash-field="request" data-nav="request" disabled/);
});

test("trade: every key/pad stop sits in a zone (partners, give, get, foot); ± buttons are mouse-only", () => {
  const status = views.tradeDraftStatus(padState, me.id, { targetId: partner.id });
  const html = views.tradeDockHtml(padState, seat, { ...views.newTradeDraft(), targetId: partner.id, offer: status.mine.slice(0, 1) }, ctx);
  const zones = [...html.matchAll(/data-nav="([a-z]+)"/g)].map((match) => match[1]);
  assert.deepEqual([...new Set(zones)], ["partners", "offer", "request", "foot"]);
  assert.equal(zones.filter((zone) => zone === "offer").length, status.mine.length + 1, "each of my deeds, then my cash");
  assert.equal(zones.filter((zone) => zone === "request").length, status.theirs.length + 1);
  for (const button of html.match(/<button[^>]*class="step-btn"[^>]*>/g)) assert.match(button, /tabindex="-1"/);
  assert.match(html, /SEND<span class="glyph glyph--sm"[^>]*>X</, "a controller seat sends with X");
  const keyboard = asTable(load("pad-turn").state);
  const keySeat = { ...seat, input: "keyboard" };
  assert.match(views.tradeDockHtml(keyboard, keySeat, views.newTradeDraft(), ctx), /SEND<span class="glyph glyph--sm"[^>]*>S</, "a keyboard seat sends with S");
});

test("trade: picking a partner shows only their deeds on the get side; picks show a tick", () => {
  const draft = { ...views.newTradeDraft(), targetId: partner.id };
  const html = views.tradeOverlayHtml(padState, seat, draft, ctx);
  const theirs = toggles(html, "request");
  assert.ok(theirs.length > 0, "fixture: partner owns deeds");
  for (const index of theirs) assert.equal(padState.properties[index]?.ownerId, partner.id, `deed ${index} belongs to the partner`);
  for (const index of toggles(html, "offer")) assert.equal(padState.properties[index]?.ownerId, me.id);
  assert.match(html, new RegExp(`data-partner="${partner.id}" data-nav="partners" aria-pressed="true"`));
  const picked = views.tradeOverlayHtml(padState, seat, { ...draft, offer: [toggles(html, "offer")[0]] }, ctx);
  assert.match(picked, /class="deed-row is-on/);
  assert.match(picked, /ADD WHAT YOU GET/);
});

test("trade: empty sides say NONE; locked deeds are greyed with a short reason", () => {
  const poor = structuredClone(padState);
  for (const prop of Object.values(poor.properties)) if (prop.ownerId === me.id) prop.ownerId = null;
  const html = views.tradeOverlayHtml(poor, seat, { ...views.newTradeDraft(), targetId: partner.id }, ctx);
  assert.match(html, /trade-col--give[\s\S]*?NONE[\s\S]*?trade-col--get/);
  assert.doesNotMatch(html, /data-toggle="offer:/);
  assert.match(html, /data-cash="offer:50"/, "cash is still offered");
  // Houses on a set lock the whole set out of trades.
  const built = structuredClone(padState);
  const index = ownedBy(built, partner.id).find((candidate) => built.board[candidate].type === "property");
  built.properties[index].houses = 1;
  const locked = views.tradeOverlayHtml(built, seat, { ...views.newTradeDraft(), targetId: partner.id }, ctx);
  assert.doesNotMatch(locked, new RegExp(`data-toggle="request:${index}"`));
  assert.match(locked, /deed-row is-locked[^>]*aria-disabled="true"[\s\S]*?HOUSES/);
});

test("trade: cash-for-cash is blocked and deeds that changed hands drop out", () => {
  const status = views.tradeDraftStatus(padState, me.id, { targetId: partner.id, offer: [], request: [], offerCash: 100, requestCash: 50 });
  assert.equal(status.gives && status.gets, true);
  assert.equal(status.valid, false);
  assert.equal(status.reason, "ADD A DEED");
  const theirs = views.tradeDraftStatus(padState, me.id, { targetId: partner.id }).theirs;
  const stale = views.tradeDraftStatus(padState, me.id, { targetId: partner.id, offer: [9999], request: theirs.slice(0, 1), offerCash: 0, requestCash: 0 });
  assert.deepEqual(stale.offer, [], "a deed you no longer own is not counted");
  assert.equal(stale.valid, false);
});

test("trade: the computer's price is a small button; it never edits the cash by itself", () => {
  const ai = padState.players.find((player) => player.input === "ai");
  const theirs = views.tradeDraftStatus(padState, me.id, { targetId: ai.id }).theirs;
  const draft = { ...views.newTradeDraft(), targetId: ai.id, request: theirs.slice(0, 1), quote: { offerCash: 82, requestCash: 0 } };
  const html = views.tradeOverlayHtml(padState, seat, draft, ctx);
  assert.match(html, /data-cmd="trade-apply-quote"[^>]*>[\s\S]*?ACCEPTS AT \$82[\s\S]*?\$82 MORE NEEDED/);
  assert.match(html, /cash-step__value[^>]*>\$0</, "your cash in the deal is still $0");
  const short = views.tradeOverlayHtml(padState, seat, { ...draft, offerCash: 50 }, ctx);
  assert.match(short, /\$32 MORE NEEDED/, "the cash is compared with the last quote on the client");
  const asks = views.tradeOverlayHtml(padState, seat, { ...draft, quote: { offerCash: 0, requestCash: 60 } }, ctx);
  assert.match(asks, /data-cmd="trade-apply-quote"[\s\S]*?WILL ACCEPT[\s\S]*?PAYS YOU UP TO \$60/);
  const applied = views.tradeOverlayHtml(padState, seat, { ...draft, offerCash: 82 }, ctx);
  assert.doesNotMatch(applied, /data-cmd="trade-apply-quote"/);
  assert.match(applied, /price--done[\s\S]*?WILL ACCEPT/);
  const waiting = views.tradeOverlayHtml(padState, seat, { ...draft, quote: { loading: true } }, ctx);
  assert.match(waiting, /price--wait[\s\S]*?THINKING/);
  const never = views.tradeOverlayHtml(padState, seat, { ...draft, quote: { error: "No deal: that would give you a full set." } }, ctx);
  assert.match(never, /price--never[\s\S]*?WON(?:'|&#039;)T TRADE AT ANY PRICE[\s\S]*?THAT WOULD GIVE YOU A FULL SET/);
  assert.doesNotMatch(never, /trade-apply-quote/);
  const normal = views.tradeOverlayHtml(padState, seat, { ...draft, quote: { error: "The AI is not willing to buy the selected properties" } }, ctx);
  assert.match(normal, /WON(?:'|&#039;)T TRADE AT ANY PRICE/, "Easy and Normal's no reads the same");
  const dear = views.tradeOverlayHtml(padState, seat, { ...draft, quote: { error: "No deal: not for any price you can pay." } }, ctx);
  assert.match(dear, /MORE THAN YOU HAVE/);
  const human = views.tradeOverlayHtml(padState, seat, { ...draft, targetId: partner.id, quote: { offerCash: 82 } }, ctx);
  assert.doesNotMatch(human, /trade-apply-quote|price--/, "only computers quote a price");
});

test("trade: a valid deal shows the value bar and one SEND button", () => {
  const status = views.tradeDraftStatus(padState, me.id, { targetId: partner.id });
  const html = views.tradeOverlayHtml(padState, seat, { ...views.newTradeDraft(), targetId: partner.id, offer: status.mine.slice(0, 1), request: status.theirs.slice(0, 1), offerCash: 50 }, ctx);
  assert.match(html, /value-bar/);
  assert.match(html, /data-cmd="trade-send" data-nav="foot" >/);
  assert.equal((html.match(/big-btn--primary/g) || []).length, 1);
  assert.doesNotMatch(html, /trade-why/);
});

test("bankruptcy: never the pass key, and its confirm opens on KEEP PLAYING", () => {
  const debt = views.panelModel(asTable(load("pad-debt").state), ctx);
  const bankrupt = debt.rows.find((row) => row.cmd === "bankrupt");
  assert.equal(bankrupt.key, "Q", "keyboard: Q, not X (X passes an auction)");
  assert.notEqual(bankrupt.button, "B", "controller: not B (back)");
  const html = views.confirmOverlayHtml({ title: "DECLARE BANKRUPTCY?", text: "", confirm: "DECLARE BANKRUPTCY", danger: true });
  assert.match(html, /data-cmd="close" data-autofocus>KEEP PLAYING/);
  assert.doesNotMatch(html, /data-cmd="confirm"[^>]*data-autofocus/);
});

test("lobby: house rules from the PC, without the dice-choice mode", () => {
  const lobby = asTable(load("table-lobby").state);
  assert.match(views.joinHtml(lobby, ctx), /data-cmd="rules"/);
  const html = views.rulesOverlayHtml(lobby);
  for (const rule of ["auctions", "loans", "freeParkingJackpot"]) assert.match(html, new RegExp(`data-rule="${rule}"`));
  assert.match(html, /data-rule-cash="1"/);
  assert.doesNotMatch(html, /dice|DICE|choose|CHOOSE/, "no dice picker exists, so no 'choose dice' rule");
  assert.doesNotMatch(views.joinHtml(lobby, ctx), /class="ways"|PRESS A<|Party table|This PC/, "no join tiles or setup chatter");
});

test("lobby: × removes any seat before the start; NEW TABLE in the header and the MENU (0.10.2)", () => {
  const lobby = asTable(load("table-lobby").state);
  assert.ok(lobby.players.length > 0, "the fixture has seated players");
  const html = views.joinHtml(lobby, ctx);
  const re = (text) => String(text).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  for (const player of lobby.players) assert.match(html, new RegExp(`data-cmd="remove-player" data-arg="${re(player.id)}"`), player.name);
  assert.match(html, /data-cmd="new-table"[^>]*>NEW TABLE</);
  const watching = views.joinHtml({ ...lobby, viewerRole: "spectator" }, ctx);
  assert.doesNotMatch(watching, /remove-player|data-cmd="new-table"/, "a watching screen can't remove anyone or start over");
  assert.match(views.menuOverlayHtml(lobby, {}), /data-cmd="menu-new"[\s\S]*?NEW TABLE/);
  assert.doesNotMatch(views.menuOverlayHtml(lobby, { watching: true }), /menu-new/);
  const playing = asTable(load("trade").state);
  assert.doesNotMatch(views.menuOverlayHtml(playing, {}), /menu-new/, "in a game the way out is LEAVE TABLE");
  assert.match(views.menuOverlayHtml(playing, {}), /data-cmd="menu-leave"/);
  assert.match(views.confirmOverlayHtml({ title: "REMOVE ALI?", text: "", confirm: "REMOVE", cancel: "KEEP THEM", danger: true }), /data-cmd="close" data-autofocus>KEEP THEM/);
  assert.doesNotMatch(views.removeSeatButtonHtml({ id: "p1", name: '<img src=x onerror="1">' }), /<img/, "names are escaped");
});

test("game end: PLAY AGAIN is a rematch; places by net worth; bankrupt players just OUT", () => {
  const state = asTable(load("victory").state);
  const html = views.victoryOverlayHtml(state, ctx);
  assert.match(html, /data-cmd="rematch"[^>]*>[\s\S]*?PLAY AGAIN/);
  assert.match(html, /data-cmd="new-table"/);
  assert.doesNotMatch(html, /out 1st|out 2nd|out \d/i, "no unreliable 'out 1st' labels");
  assert.equal(views.panelModel(state, ctx).rows[0].cmd, "rematch");
  // Enter / A press whatever has focus, and people are still pressing them from the last turn:
  // the winner screen starts on the standings, never on PLAY AGAIN (or any button).
  const first = html.match(/<(\w+)[^>]*\bdata-autofocus\b[^>]*>/);
  assert.ok(first && first[1] === "ol" && /victory__list/.test(first[0]), "focus starts on the standings");
  assert.doesNotMatch(html, /<button[^>]*\bdata-autofocus\b/, "no button is focused on the winner screen");
  // Starting a rematch always asks first: every PLAY AGAIN (winner screen, the card's row, the
  // other games' final screens) opens the confirm, rematch() runs only from its PLAY AGAIN, and
  // the confirm opens on NOT YET so a stray Enter / A cancels.
  const source = readFileSync(join(here, "..", "table", "table.js"), "utf8");
  const handlers = [...source.matchAll(/(?:case "rematch":|cmd === "rematch"\))\s*(?:return )?(\w+)\(/g)].map((match) => match[1]);
  assert.ok(handlers.length >= 3, "the button, the card row and the other games' screens");
  assert.deepEqual([...new Set(handlers)], ["askRematch"], "every PLAY AGAIN asks first");
  assert.equal([...source.matchAll(/(?<!function )\brematch\(/g)].length, 0, "nothing calls rematch() directly");
  const ask = source.match(/function askRematch\(\) \{[\s\S]*?\n\}/)?.[0] || "";
  assert.match(ask, /kind: "confirm"/);
  assert.match(ask, /onConfirm: rematch\b/);
  const confirm = views.confirmOverlayHtml({ title: "PLAY AGAIN?", text: "Same players, new game.", confirm: "PLAY AGAIN", cancel: "NOT YET" });
  assert.match(ask, /cancel: "NOT YET"/);
  assert.match(confirm, /data-cmd="close" data-autofocus>NOT YET/);
  assert.doesNotMatch(confirm, /data-cmd="confirm"[^>]*data-autofocus/);
});

test("plates: offline is loud; the computer playing for someone shows a robot", () => {
  const away = structuredClone(padState);
  const phone = away.players.find((player) => player.input === "phone");
  phone.online = false;
  let html = views.platesHtml(away, ctx);
  assert.match(html, /is-offline/);
  assert.match(html, /meta--off[^>]*title="Offline"/);
  assert.doesNotMatch(html, /plate__bot/, "no robot while autopilot is missing/false");
  phone.autopilot = true;
  html = views.platesHtml(away, ctx);
  assert.match(html, /is-autopilot[\s\S]*?plate__bot/);
});

test("chips show token silhouettes, not initials that collide", () => {
  const html = views.panelHtml(views.panelModel(asTable(load("trade").state), ctx));
  assert.match(html, /pchip pchip--glyph[^>]*><svg class="glyph-svg/);
});

test("join: 'anywhere' only with a public link; a resume box for a 5-letter code", () => {
  const state = asTable(load("table-empty").state);
  const pub = views.joinHtml(state, { ...ctx, server: { lanURL: "http://192.168.1.20:3402", publicURL: "https://estate.example.net" } });
  assert.match(pub, /ANYWHERE/);
  assert.match(pub, /estate\.example\.net\/j\//);
  assert.doesNotMatch(pub, /192\.168\.1\.20:3402\/j\//);
  const lan = views.joinHtml(state, { ...ctx, server: { lanURL: "http://192.168.1.20:3402", publicURL: null } });
  assert.doesNotMatch(lan, /ANYWHERE|anywhere/i);
  assert.match(lan, /SAME WI‑FI/);
  assert.match(lan, /192\.168\.1\.20:3402\/j\//);
  assert.doesNotMatch(lan, /--public-url|Phone camera|Party table/, "no setup hints");
  assert.match(lan, /data-form="resume"/);
  assert.match(lan, /RESUME A TABLE/);
  assert.match(lan, /id="resume-code"[^>]*maxlength="5"/);
  assert.match(lan, /data-cmd="start"/);
  const watching = views.joinHtml({ ...state, viewerRole: "spectator" }, ctx);
  assert.doesNotMatch(watching, /data-form="resume"/);
});

test("gate: moved, busy and resume screens with the right buttons and the code", () => {
  const moved = views.gateHtml({ kind: "moved", code: "NXGNT" });
  assert.match(moved, /THIS TABLE MOVED TO ANOTHER SCREEN/);
  assert.match(moved, /data-cmd="gate-take"[^>]*>[\s\S]*?TAKE IT BACK/);
  assert.match(moved, /data-cmd="new-table"/);
  assert.match(moved, /gate__code-value[^>]*>NXGNT</);
  const busy = views.gateHtml({ kind: "busy", code: "NXGNT" });
  assert.match(busy, /OPEN ON ANOTHER SCREEN/);
  assert.match(busy, /data-cmd="gate-retry"/);
  assert.match(busy, /data-cmd="new-table"/);
  assert.match(views.gateHtml({ kind: "busy", code: "NXGNT", canCancel: true }), /data-cmd="gate-cancel"/);
  const resume = views.gateHtml({ kind: "resume", code: "ABCDE", error: "NO TABLE WITH THAT CODE" });
  assert.match(resume, /data-form="resume"/);
  assert.match(resume, /value="ABCDE"/);
  assert.match(resume, /role="alert">NO TABLE WITH THAT CODE/);
  for (const html of [moved, busy, resume]) assert.ok(words(html) <= 16, `gate: ${words(html)} words`);
});

test("header: the table code is shown big enough to type elsewhere", () => {
  const html = views.tableCodeHtml("NXGNT");
  assert.match(html, /table-code__value[^>]*>NXGNT</);
  assert.match(html, /aria-label="Table code N X G N T"/);
  assert.equal(views.tableCodeHtml(""), "");
});

test("board marks: next-roll heat, trade squares and a card's destination", () => {
  const marks = views.boardMarks(asTable(load("pad-turn").state));
  for (const entry of marks.risk) assert.ok(entry.level > 0 && entry.level <= 1);
  const trade = views.boardMarks(asTable(load("trade").state));
  assert.ok(trade.pick.length > 0, "trade squares are picked out on the board");
  const card = views.boardMarks(asTable(load("card").state));
  assert.equal(card.target.length, 1, "card destination is marked");
});

// ---------------------------------------------------------------------------------------------
// Camera (web/table/camera.js): pure view maths and the per-state plan.

const { LAYOUT } = camera;
// Square rects (board px) for the 860 px board: nine 1-unit squares plus two 1.55-unit corners.
const UNIT = LAYOUT.board / 12.1;
const track = (n) => (n === 1 ? 0 : n === 11 ? 10.55 * UNIT : (1.55 + (n - 2)) * UNIT);
const trackSize = (n) => (n === 1 || n === 11 ? 1.55 * UNIT : UNIT);
const rectOf = (index) => {
  const pos = cellPosition(index);
  return { x: track(pos.col), y: track(pos.row), w: trackSize(pos.col), h: trackSize(pos.row) };
};
const onStage = (view, point) => ({ x: view.x + point.x * view.z, y: view.y + point.y * view.z });
const coversFrame = (view, frame) => {
  const size = LAYOUT.board * view.z;
  if (size <= frame.right - frame.left) return true;
  return view.x <= frame.left + 0.01 && view.x + size >= frame.right - 0.01 && view.y <= frame.top + 0.01 && view.y + size >= frame.bottom - 0.01;
};

test("camera: overview shows the whole board, as big as before, right of the plates", () => {
  const view = camera.overviewView();
  assert.deepEqual(view, { x: 360, y: 20, z: 1 });
  assert.ok(view.x >= 24 + 320, "clear of the plates");
  assert.ok(view.x + LAYOUT.board * view.z <= LAYOUT.stage.w - 24 - 340, "clear of the action card column");
  assert.ok(LAYOUT.board >= 840, "board text stays as readable as the old 840 px board");
  const drawer = camera.overviewView(LAYOUT, LAYOUT.drawerFrame);
  assert.ok(drawer.z < 1);
  assert.ok(drawer.x >= LAYOUT.drawerFrame.left && drawer.x + LAYOUT.board * drawer.z <= LAYOUT.drawerFrame.right + 0.01, "whole board left of the trade drawer");
});

test("camera: focus is 2x, puts every square in the clear part of the stage, never shows past the board edge", () => {
  for (let index = 0; index < 40; index += 1) {
    const rect = rectOf(index);
    const centre = { x: rect.x + rect.w / 2, y: rect.y + rect.h / 2 };
    const view = camera.focusView(centre);
    assert.equal(view.z, 2);
    assert.ok(coversFrame(view, LAYOUT.frame), `square ${index}: no empty space in the frame`);
    const at = onStage(view, centre);
    assert.ok(at.x >= LAYOUT.frame.left && at.x <= LAYOUT.frame.right, `square ${index}: x ${at.x.toFixed(0)} between the plates and the card column`);
    assert.ok(at.y >= 0 && at.y <= LAYOUT.stage.h, `square ${index}: y on stage`);
  }
  // A square in the middle of an edge lands in the middle of the frame.
  const mid = rectOf(25);
  const view = camera.focusView({ x: mid.x + mid.w / 2, y: mid.y + mid.h / 2 });
  const at = onStage(view, { x: mid.x + mid.w / 2, y: mid.y + mid.h / 2 });
  assert.ok(Math.abs(at.x - (LAYOUT.frame.left + LAYOUT.frame.right) / 2) < 1);
});

test("camera: fit frames a set of squares (a trade) inside the frame, also left of the drawer", () => {
  for (const [frameName, squares] of [["main", [1, 3]], ["main", [1, 39]], ["drawer", [6, 8, 9]], ["drawer", [0, 20]]]) {
    const frame = camera.frameOf(frameName);
    const view = camera.viewFor({ kind: "fit", squares, frame: frameName }, rectOf);
    assert.ok(view.z >= LAYOUT.minZoom && view.z <= LAYOUT.fitMaxZoom, `${squares}: zoom ${view.z}`);
    for (const index of squares) {
      const rect = rectOf(index);
      const at = onStage(view, { x: rect.x + rect.w / 2, y: rect.y + rect.h / 2 });
      assert.ok(at.x >= frame.left - 1 && at.x <= frame.right + 1, `${frameName} ${squares}: square ${index} x ${at.x.toFixed(0)}`);
      assert.ok(at.y >= frame.top - 1 && at.y <= frame.bottom + 1);
    }
  }
  const close = camera.viewFor({ kind: "fit", squares: [6, 8, 9], frame: "main" }, rectOf);
  assert.ok(close.z > 1.2, "a few neighbouring squares get a close look");
});

test("camera: the older follow plan (calm: false) frames the square that matters and fits trades", () => {
  const plan = (name, options) => camera.cameraPlan(asTable(load(name).state), { calm: false, ...options });
  assert.equal(plan("pad-turn").kind, "overview", "awaiting a roll: whole board");
  assert.equal(plan("table-lobby").kind, "overview");
  assert.equal(plan("victory").kind, "overview");
  const buy = asTable(load("buy").state);
  assert.deepEqual(camera.cameraPlan(buy, { calm: false }), { kind: "focus", squares: [buy.pendingPurchase], frame: "main", hold: 0 });
  const auction = asTable(load("table-auction").state);
  assert.deepEqual(camera.cameraPlan(auction, { calm: false }).squares, [auction.auction.spaceIndex]);
  const debt = asTable(load("pad-debt").state);
  assert.deepEqual(camera.cameraPlan(debt, { calm: false }).squares, [debt.players.find((player) => player.id === debt.debt.payerId).position]);
  const rent = asTable(load("rent").state);
  const rentPlan = camera.cameraPlan(rent, { calm: false });
  assert.equal(rentPlan.kind, "focus");
  assert.deepEqual(rentPlan.squares, [rent.lastLanding.spaceIndex], "rent: the landing square");
  assert.equal(rentPlan.hold, 4000, "then back to the whole board after about 4 s");
  const card = asTable(load("card").state);
  const target = cardPreview(card, card.pendingCard.playerId, card.pendingCard.cardId).target;
  const cardPlan = camera.cameraPlan(card, { calm: false });
  assert.ok(cardPlan.squares.includes(target), "card: its destination is in view");
  const trade = asTable(load("trade").state);
  const tradePlan = camera.cameraPlan(trade, { calm: false });
  assert.equal(tradePlan.kind, "fit");
  assert.deepEqual([...tradePlan.squares].sort(), [...trade.trade.offer.properties, ...trade.trade.request.properties].sort());
  assert.deepEqual(camera.cameraPlan(padState, { trading: true, draft: [6, 39] }), { kind: "fit", squares: [6, 39], frame: "drawer", hold: 0 });
  assert.deepEqual(camera.cameraPlan(padState, { trading: true, draft: [] }), { kind: "overview", squares: [], frame: "drawer", hold: 0 });
  // Every fixture gives a plan whose view is finite and covers the frame.
  for (const name of readdirSync(dir).filter((file) => file.endsWith(".json") && file !== "index.json").map((file) => file.replace(/\.json$/, ""))) {
    const data = load(name);
    if (!data.state) continue;
    for (const calm of [true, false]) {
      const view = camera.viewFor(camera.cameraPlan(asTable(data.state), { calm }), rectOf);
      assert.ok([view.x, view.y, view.z].every(Number.isFinite), `${name}: finite view`);
      assert.ok(coversFrame(view, LAYOUT.frame), `${name}: no empty space`);
    }
  }
});

test("camera: calm by default (review U1): the whole board, a short close-up only for a big rent", () => {
  const states = readdirSync(dir).filter((file) => file.endsWith(".json") && file !== "index.json").map((file) => load(file.replace(/\.json$/, ""))).filter((data) => data.state).map((data) => asTable(data.state));
  const whole = states.filter((state) => camera.cameraPlan(state).kind === "overview").length;
  assert.ok(whole / states.length >= 0.9, `whole board in ${whole} of ${states.length} fixtures`);
  for (const name of ["buy", "table-auction", "pad-debt", "card", "trade", "pad-turn"]) assert.equal(camera.cameraPlan(asTable(load(name).state)).kind, "overview", name);
  const rent = asTable(load("rent").state);
  const big = { ...rent, phase: "turn_complete", currentPlayerId: rent.lastLanding.playerId, lastLanding: { ...rent.lastLanding, amount: camera.CLOSE_UP_RENT } };
  assert.deepEqual(camera.cameraPlan(big), { kind: "focus", squares: [rent.lastLanding.spaceIndex], frame: "main", hold: camera.CLOSE_UP_MS });
  assert.ok(camera.CLOSE_UP_MS <= 2000, "the close-up is brief");
  assert.equal(camera.cameraPlan({ ...big, lastLanding: { ...big.lastLanding, amount: camera.CLOSE_UP_RENT - 1 } }).kind, "overview", "small rent: whole board");
  assert.equal(camera.cameraPlan({ ...big, phase: "awaiting_roll" }).kind, "overview", "next turn: whole board");
  assert.equal(camera.cameraPlan(padState, { trading: true, draft: [6, 39] }).kind, "fit", "a drawer still frames its deeds");
});

test("camera: mini-map rectangle, move timing, reduced motion and hidden tabs", () => {
  assert.deepEqual(camera.visibleRect(camera.overviewView()), { x: 0, y: 0, w: LAYOUT.board, h: LAYOUT.board });
  const zoomed = camera.visibleRect(camera.focusView({ x: 430, y: 40 }));
  assert.ok(zoomed.w < LAYOUT.board && zoomed.h < LAYOUT.board && zoomed.y === 0);
  const from = camera.overviewView();
  const to = camera.focusView({ x: 100, y: 800 });
  const ms = camera.moveMs(from, to);
  assert.ok(ms >= 260 && ms <= 900, `ease ${ms} ms`);
  assert.equal(camera.moveMs(from, to, { reduced: true }), 0, "reduced motion: no camera moves");
  assert.equal(camera.moveMs(from, to, { hidden: true }), 0, "hidden tab: jump, don't animate");
  assert.equal(camera.moveMs(null, to), 0, "first view: no animation");
  assert.match(camera.viewTransform(to), /^translate\(-?\d+(\.\d)?px, -?\d+(\.\d)?px\) scale\(2\.0000\)$/, "one transform, GPU-friendly");
  assert.ok(camera.sameView(from, { ...from, x: from.x + 0.2 }));
});

test("camera: FOLLOW MOVES zooms in on every hop and pans with the token (review 4 Nov #1)", () => {
  // The hop in the t-hop-mid demo (18 → 25): every square of it is shot at the zoom setting, with
  // the token inside the clear frame; the camera moves along with it, not one fixed close-up.
  const path = [18, 19, 20, 21, 22, 23, 24, 25];
  const centre = (index) => { const r = rectOf(index); return { x: r.x + r.w / 2, y: r.y + r.h / 2 }; };
  for (const zoom of [1.6, 2, 2.4]) {
    const shots = camera.hopShots(path.map(centre), { zoom });
    assert.equal(shots.length, path.length);
    shots.forEach((view, step) => {
      assert.equal(view.z, zoom, `step ${step}: zoomed in`);
      assert.ok(coversFrame(view, LAYOUT.frame), `step ${step}: no empty space`);
      const at = onStage(view, centre(path[step]));
      assert.ok(at.x >= LAYOUT.frame.left && at.x <= LAYOUT.frame.right && at.y >= 0 && at.y <= LAYOUT.stage.h, `zoom ${zoom} step ${step}: token in view`);
    });
    assert.ok(new Set(shots.map((view) => Math.round(view.x))).size > 2, `zoom ${zoom}: the camera pans with the token`);
  }
  // Around a corner too (36 → 4 passes PAYDAY).
  const corner = [36, 37, 38, 39, 0, 1, 2, 3, 4];
  const shots = camera.hopShots(corner.map(centre));
  corner.forEach((index, step) => {
    const at = onStage(shots[step], centre(index));
    assert.ok(at.x >= LAYOUT.frame.left && at.x <= LAYOUT.frame.right && at.y >= 0 && at.y <= LAYOUT.stage.h, `corner step ${step}: token in view`);
  });
  // Who gets followed: FOLLOW MOVES (the default), not CLOSE-UPS ONLY / OFF, reduced motion, a hidden
  // tab, an open drawer, a jump to jail or a path of one square.
  assert.equal(camera.followsHop({ path }), true, "default: follow");
  assert.equal(camera.followsHop({ mode: "follow", path }), true);
  for (const [label, options] of [["close-ups only", { mode: "closeups" }], ["off", { mode: "off" }], ["reduced motion", { reduced: true }], ["hidden tab", { hidden: true }], ["drawer open", { docked: true }], ["jail", { jail: true }]]) {
    assert.equal(camera.followsHop({ path, ...options }), false, label);
  }
  assert.equal(camera.followsHop({ path: [5] }), false, "no hop");
  // Only long hops (review F9): 7+ squares, past PAYDAY, or a card's move. Short ones play on the whole board.
  assert.equal(camera.FOLLOW_MIN_STEPS, 7);
  assert.equal(camera.followsHop({ path: [10, 11, 12, 13, 14, 15, 16] }), false, "six squares");
  assert.equal(camera.followsHop({ path: [10, 11, 12, 13, 14, 15, 16, 17] }), true, "seven squares");
  assert.equal(camera.followsHop({ path: [37, 38, 39, 0, 1] }), true, "past PAYDAY");
  assert.equal(camera.followsHop({ path: [36, 37, 38, 39, 0] }), true, "onto PAYDAY");
  assert.equal(camera.followsHop({ path: [7, 6, 5, 4], card: true }), true, "a card's move (go back 3)");
  assert.equal(camera.followsHop({ path: [7, 6, 5, 4], card: true, mode: "closeups" }), false, "but only under FOLLOW MOVES");
  assert.equal(camera.passesGo([5, 6, 7]), false);
  assert.equal(camera.passesGo([39, 0]), true);
  // The zoom glides: ease-in-out over about two thirds of a second (420 ms ease-out read as a cut).
  assert.equal(camera.CAMERA_EASE, "cubic-bezier(.45,0,.2,1)");
  assert.ok(camera.FOLLOW_IN_MS >= 600 && camera.FOLLOW_IN_MS <= 700 && camera.FOLLOW_OUT_MS >= 550 && camera.FOLLOW_OUT_MS <= 700);
  assert.deepEqual(camera.CAMERA_MODES, ["follow", "closeups", "off"]);
  // The pans keep time with the hop: as long as each step (the landing step is 1.6× as long).
  assert.equal(camera.panMs(120), 120);
  assert.equal(camera.panMs(120, true), 192);
  assert.ok(camera.panMs(10) >= 60, "never a jerk");
  assert.ok(camera.FOLLOW_REST_MS >= 400 && camera.FOLLOW_REST_MS <= 700, "rests about half a second on the landing square");
  assert.ok(camera.FOLLOW_BIG_REST_MS > camera.FOLLOW_REST_MS && camera.FOLLOW_BIG_REST_MS <= camera.CLOSE_UP_MS);
});

test("camera: table.js follows the hop and eases out before money and cards (source check)", () => {
  // The 4 Nov regression: the move handler called wholeBoard() and never zoomed. Keep the wiring.
  const source = readFileSync(join(here, "..", "table", "table.js"), "utf8");
  const move = source.slice(source.indexOf(`case "move": {`), source.indexOf(`case "pay": {`));
  assert.match(move, /followStart\(/, "the move handler starts the follow camera");
  assert.match(move, /applyView\(shots\[index \+ 1\]/, "and pans with each step");
  assert.match(move, /FOLLOW_(BIG_)?REST_MS/, "and rests on the landing square");
  const pay = source.slice(source.indexOf(`case "pay": {`), source.indexOf(`case "buy": {`));
  assert.equal((pay.match(/await easeOut\(\)/g) || []).length, 2, "money and cards play on the whole board");
  assert.match(source, /function noteReducedCamera\(\)/, "reduced motion from the computer is explained once");
});

test("mini-map: every square, owner dots and a token per player", () => {
  const state = asTable(load("pad-turn").state);
  const html = views.minimapHtml(state);
  assert.ok((html.match(/<rect /g) || []).length >= 41, "board centre + 40 squares (+ colour bars)");
  assert.equal((html.match(/stroke="#F2C14E"/g) || []).length, 1, "the current player's token is ringed gold");
  const owned = Object.values(state.properties).filter((prop) => prop.ownerId).length;
  const live = state.players.filter((player) => !player.bankrupt).length;
  assert.equal((html.match(/<circle /g) || []).length, owned + live);
});

test("action card: one line under the name; END TURN says what the landing did", () => {
  const rent = asTable(load("rent").state);
  const line = views.landingLine(rent);
  assert.match(line.verb, /^PAID \$\d+/);
  const model = views.panelModel(rent, ctx);
  assert.match(views.panelHtml(model), /now__verb[\s\S]*PAID \$/);
  const buy = views.panelModel(asTable(load("buy").state), ctx);
  assert.equal(buy.verb, "BUY?");
  assert.match(buy.line, /now-amt">\$\d+/);
  assert.match(views.slotHtml(asTable(load("buy").state), ctx), /FOR SALE/, "the deed sits above the card");
  assert.doesNotMatch(views.slotHtml(asTable(load("pad-turn").state), ctx), /die/, "dice have their own spot, not the show slot");
  const pass = views.panelHtml(views.panelModel(asTable(load("table-auction").state), { ...ctx }));
  const bidding = asTable(load("table-auction").state);
  const padSeat = bidding.localSeats.find((seat) => seat.input === "controller" && bidding.auction.activeIds.includes(seat.playerId));
  const two = views.panelModel({ ...bidding, auction: { ...bidding.auction, currentPlayerId: padSeat.playerId } }, ctx);
  assert.equal(two.rows.length, 2);
  assert.doesNotMatch(views.panelHtml(two), /panel__row--wide/, "BID and PASS side by side");
  assert.doesNotMatch(pass, /panel__row--action/, "someone else bids: no live buttons");
  assert.match(pass, /panel__row--disabled/, "the seat's BID / PASS stay in place, greyed");
  const three = views.panelHtml(views.panelModel(asTable(load("pad-debt").state), ctx));
  assert.match(three, /panel__row--wide/, "odd count: the main action spans the row");
});

// ---------------------------------------------------------------------------------------------
// ACTIVE GAMES in the lobby (GET /api/games entries: the /summary object + mine, online, updatedAt).

const GAMES = [
  { code: "PHN01", status: "lobby", phase: "lobby", turn: null, host: { name: "Priya", tokenKind: "teapot", color: "#7A1838" },
    players: [{ id: "p1", name: "Priya", tokenKind: "teapot", color: "#7A1838", input: "phone", online: true }, { id: "p2", name: "Ravi", tokenKind: "bicycle", color: "#6F7A1A", input: "phone", online: false }, { id: "a1", name: "Alpha", tokenKind: "key", color: "#8A5CF5", input: "ai", online: true }],
    openSeats: 3, tableOnline: false, rules: {}, mine: false, online: true, updatedAt: 1793560000000 },
  { code: "PLY22", status: "playing", phase: "awaiting_roll", turn: 14, host: { name: "Sam", tokenKind: "rocket", color: "#1EA0FF" },
    players: [{ id: "s1", name: "Sam", tokenKind: "rocket", color: "#1EA0FF", input: "phone", online: true }, { id: "m1", name: "Maya", tokenKind: "lighthouse", color: "#0E8C8C", input: "controller", online: true }],
    openSeats: 0, tableOnline: true, rules: {}, mine: false, online: true, updatedAt: 1793560001000 },
  { code: "HERE5", status: "lobby", phase: "lobby", turn: null, host: { name: "Table", tokenKind: null, color: null }, players: [], openSeats: 6, tableOnline: true, rules: {}, mine: false, online: true, updatedAt: 1793560002000 },
  { code: "DONE9", status: "finished", phase: "finished", turn: 80, host: { name: "Eve", tokenKind: "key", color: "#8A5CF5" }, players: [], openSeats: 0, tableOnline: false, rules: {}, mine: false, online: true, updatedAt: 1793560003000 },
];

test("active games: one row per other game, status words, SHOW HERE only when no screen shows it", () => {
  const html = views.gamesHtml(GAMES, { currentCode: "HERE5" });
  assert.match(html, /ACTIVE GAMES/);
  const rows = html.match(/<li class="game-row/g) || [];
  assert.equal(rows.length, 2, "this table and finished games are left out");
  assert.doesNotMatch(html, /HERE5|DONE9/);
  assert.match(html, /data-code="PHN01"[\s\S]*?PRIYA[\s\S]*?LOBBY · 3\/6[\s\S]*?data-game-code="PHN01"[^>]*>[\s\S]*?SHOW HERE/);
  assert.match(html, /data-code="PLY22"[\s\S]*?TURN 14[\s\S]*?disabled[^>]*>[\s\S]*?ON A SCREEN/);
  assert.doesNotMatch(html, /data-game-code="PLY22"/, "a game already on a screen can't be pulled here");
  assert.equal((html.match(/game-row__disc(?!s)/g) || []).length, 5, "a token disc per player");
  assert.match(html, /game-row__disc is-away/, "players who dropped off are dimmed");
  assert.match(html, /pchip--glyph/, "discs show token silhouettes");
  assert.equal(views.gameStatus({ status: "playing", turn: 0, players: [] }), "PLAYING");
});

test("active games: nothing at all when there are no other games; in the lobby beside the seats", () => {
  assert.equal(views.gamesHtml([], {}), "");
  assert.equal(views.gamesHtml([GAMES[2]], { currentCode: "HERE5" }), "", "only this table: no list");
  assert.equal(views.gamesHtml(undefined), "");
  const lobby = asTable(load("table-lobby").state);
  const withGames = views.joinHtml(lobby, { ...ctx, games: GAMES });
  assert.match(withGames, /class="join__right">[\s\S]*class="games[\s\S]*class="players-box"/);
  assert.doesNotMatch(views.joinHtml(lobby, { ...ctx, games: [] }), /class="games/);
  assert.doesNotMatch(views.joinHtml({ ...lobby, viewerRole: "spectator" }, { ...ctx, games: GAMES }), /class="games/, "a watching screen can't take tables");
});

// ---------------------------------------------------------------------------------------------
// Property drawer (replaces the modal list) and the loan drawer.

const brownSet = (state) => state.board.filter((space) => space.type === "property" && space.group === "brown").map((space) => space.index);

test("property drawer: deeds grouped by colour set with progress, every deed the player owns, nothing else", () => {
  const model = views.propertyModel(padState, me.id);
  const owned = ownedBy(padState, me.id);
  const listed = model.groups.flatMap((group) => group.deeds.map((deed) => deed.index));
  assert.deepEqual([...listed].sort((a, b) => a - b), [...owned].sort((a, b) => a - b));
  for (const group of model.groups) {
    assert.match(group.progress, /^(FULL SET|\d OF \d)$/);
    for (const deed of group.deeds) {
      const space = padState.board[deed.index];
      if (group.kind === "property") assert.equal(space.group, group.key, `${space.name} sits in its colour`);
      else assert.equal(space.type, group.kind);
    }
  }
  assert.equal(model.totals.deeds, owned.length);
  const html = views.propertyDockHtml(padState, seat, ctx);
  assert.match(html, /class="prop-dock"/, "a drawer, not a modal list");
  assert.doesNotMatch(html, /aria-modal|dialog--/);
  assert.match(html, new RegExp(`${me.name.toUpperCase()}(&#039;|')S PROPERTY`));
  assert.equal((html.match(/<article class="pdeed/g) || []).length, owned.length, "a card per deed");
  assert.match(html, /data-cmd="close"/);
});

test("property drawer: build and sell follow the even-building rule; mortgage waits for houses to go", () => {
  const state = structuredClone(padState);
  const [a, b] = brownSet(state);
  state.properties[a] = { ...state.properties[a], ownerId: me.id, houses: 1, mortgaged: false };
  state.properties[b] = { ...state.properties[b], ownerId: me.id, houses: 0, mortgaged: false };
  const deedOf = (model, index) => model.groups.flatMap((group) => group.deeds).find((deed) => deed.index === index);
  const actionOf = (deed, id) => deed.actions.find((action) => action.id === id);
  const model = views.propertyModel(state, me.id);
  const brown = model.groups.find((group) => group.key === "brown");
  assert.equal(brown.progress, "FULL SET");
  assert.equal(actionOf(deedOf(model, a), "build").why, "EVEN", "the street with a house waits for the other");
  assert.equal(actionOf(deedOf(model, b), "build").enabled, true);
  assert.equal(actionOf(deedOf(model, a), "sell").enabled, true, "sell from the tallest street");
  assert.equal(actionOf(deedOf(model, b), "sell").why, "NONE");
  assert.equal(actionOf(deedOf(model, a), "mortgage").why, "HOUSES");
  assert.equal(actionOf(deedOf(model, b), "mortgage").why, "HOUSES", "the whole set must be bare");
  // Mortgaged: UNMORTGAGE with its cost; no building on the set while it lasts.
  state.properties[a] = { ...state.properties[a], houses: 0 };
  state.properties[b] = { ...state.properties[b], mortgaged: true };
  const later = views.propertyModel(state, me.id);
  assert.match(actionOf(deedOf(later, b), "mortgage").label, /^UNMORTGAGE \$\d+/);
  assert.equal(actionOf(deedOf(later, a), "build").why, "MORTGAGED");
  assert.equal(actionOf(deedOf(later, a), "mortgage").enabled, true);
  // Not this player's turn: view only, every action greyed with TURN, no buttons in the drawer.
  const other = { ...state, currentPlayerId: partner.id };
  const view = views.propertyModel(other, me.id);
  assert.equal(view.manage, false);
  assert.ok(view.groups.flatMap((group) => group.deeds).flatMap((deed) => deed.actions).every((action) => !action.enabled && action.why === "TURN"));
  const viewHtml = views.propertyDockHtml(other, seat, ctx);
  assert.match(viewHtml, /VIEW ONLY/);
  assert.doesNotMatch(viewHtml, /data-act=/);
  // In debt: raise cash (sell, mortgage), no building.
  const debt = asTable(load("pad-debt").state);
  const debtSeat = debt.localSeats.find((candidate) => candidate.playerId === debt.debt.payerId);
  const debtModel = views.propertyModel(debt, debtSeat.playerId);
  const debtActions = debtModel.groups.flatMap((group) => group.deeds).flatMap((deed) => deed.actions);
  assert.ok(debtActions.filter((action) => action.id === "build").every((action) => !action.enabled));
  assert.ok(debtActions.some((action) => action.enabled && (action.id === "sell" || action.id === "mortgage")), "something can raise cash");
});

test("property drawer: an action that is off says why as a note (not a button), never a focus stop", () => {
  const state = structuredClone(padState);
  const [a, b] = brownSet(state);
  state.properties[a] = { ...state.properties[a], ownerId: me.id, houses: 1 };
  state.properties[b] = { ...state.properties[b], ownerId: me.id, houses: 0 };
  const html = views.propertyDockHtml(state, seat, ctx);
  const notes = html.match(/<span class="pdeed__why[^"]*"[^>]*>[\s\S]*?<\/span>/g) || [];
  assert.ok(notes.length >= 3, "build, sell and mortgage reasons");
  for (const note of notes) {
    assert.match(note, /<b>[A-Z' ]+<\/b>/, "a short upper-case reason");
    assert.doesNotMatch(note, /data-pkey|data-act|<button/, "not a button, not a focus stop");
  }
  // Review 4 Nov #10: reasons, not actions ("MORTGAGE HOUSES", "BUILD EVEN").
  assert.match(html, /<b>BUILD EVENLY FIRST<\/b>/);
  assert.match(html, /<b>SELL HOUSES TO MORTGAGE<\/b>/);
  assert.match(html, /<b>NO HOUSES TO SELL<\/b>/);
  assert.doesNotMatch(html, /pdeed__act[^"]*is-off|<b>(EVEN|HOUSES)<\/b>/);
  assert.equal(views.whyText({ id: "build", why: "CASH" }), "NOT ENOUGH CASH");
  assert.equal(views.whyText({ id: "build", why: "SOMETHING NEW" }), "SOMETHING NEW", "an unknown code shows as it is");
});

test("property drawer: focus order is card then its actions, deed by deed, set by set; CLOSE last", () => {
  const state = structuredClone(padState);
  const [a, b] = brownSet(state);
  state.properties[a] = { ...state.properties[a], ownerId: me.id, houses: 1 };
  state.properties[b] = { ...state.properties[b], ownerId: me.id, houses: 0 };
  const model = views.propertyModel(state, me.id);
  const order = views.propertyFocusOrder(model);
  assert.equal(order.at(-1), "close");
  assert.equal(order[0], `${model.groups[0].deeds[0].index}:card`);
  const html = views.propertyDockHtml(state, seat, ctx);
  const inDom = [...html.matchAll(/data-pkey="([^"]+)"/g)].map((match) => match[1]);
  assert.deepEqual(inDom, order, "the DOM order is the focus order");
  assert.ok(order.includes(`${b}:build`) && !order.includes(`${a}:build`), "only enabled actions are stops");
});

test("camera: the property drawer frames the deed with focus, else all the player's deeds", () => {
  const owned = ownedBy(padState, me.id);
  assert.deepEqual(camera.cameraPlan(padState, { inspect: { focus: owned[0], squares: owned } }), { kind: "fit", squares: [owned[0]], frame: "drawer", hold: 0 });
  assert.deepEqual(camera.cameraPlan(padState, { inspect: { focus: null, squares: owned } }).squares, owned);
  assert.equal(camera.cameraPlan(padState, { inspect: { focus: null, squares: [] } }).kind, "overview");
  const one = camera.viewFor({ kind: "fit", squares: [owned[0]], frame: "drawer" }, rectOf);
  assert.ok(one.z > 1.5, "a single deed gets a close look");
});

test("loan drawer: lenders, steppers, security as deed rows, SEND / CLOSE, docked", () => {
  const debt = asTable(load("pad-debt").state);
  const debtSeat = debt.localSeats.find((candidate) => candidate.playerId === debt.debt.payerId);
  const lender = debt.players.find((player) => player.id !== debtSeat.playerId && !player.bankrupt);
  const html = views.loanDockHtml(debt, debtSeat, { targetId: lender.id, principal: 150, repay: 180, dueTurns: 6, collateral: [] }, ctx);
  assert.match(html, /class="loan-dock"/);
  assert.doesNotMatch(html, /aria-modal|dialog--/);
  assert.match(html, /data-lender=/);
  assert.match(html, /data-loan-step="principal:10"/);
  assert.match(html, /data-loan-step="repay:-50"/);
  assert.match(html, /data-loan-step="dueTurns:1"/);
  assert.match(html, /data-cmd="loan-send"/);
  assert.match(html, /data-cmd="close"/);
});

// ---------------------------------------------------------------------------------------------
// Demo fixtures for every table screen (web/fixtures/t-*.json, listed in t-index.json).

const demo = await import("../table/demo.js");
const tIndex = load("t-index");
const BAD_HTML = [/undefined/, /\bNaN\b/, /\[object /, />null</, /\$-/, /Infinity/];

test("demo fixtures: patches merge by key, players by name, @Name becomes an id, + appends", () => {
  const base = load("pad-turn").state;
  const next = demo.applyPatch(base, { players: { Maya: { cash: 5 }, Sam: null }, "players+": [{ id: "p_x", name: "Kai" }], currentPlayerId: "@Kai", properties: { 16: { ownerId: "@Maya" } }, "log+": [{ text: "Maya passed PAYDAY and collected $200" }] });
  assert.equal(next.players.find((player) => player.name === "Maya").cash, 5);
  assert.ok(!next.players.some((player) => player.name === "Sam"));
  assert.equal(next.currentPlayerId, "p_x");
  assert.equal(next.properties[16].ownerId, base.players.find((player) => player.name === "Maya").id);
  assert.equal(next.properties[16].houses, base.properties[16].houses, "other fields kept");
  assert.equal(next.log.length, base.log.length + 1);
  assert.ok(next.log.at(-1).id, "log entries get ids");
  assert.equal(base.players.length, 4, "the base is not changed");
  const seated = demo.withSeats(base, { Maya: "keyboard", Robin: "controller" });
  assert.deepEqual(seated.localSeats.map((entry) => [entry.input, entry.pad]), [["keyboard", null], ["controller", 0]]);
  assert.equal(seated.viewerRole, "table");
});

test("demo fixtures: every t-index row has its fixture, ids are unique, every fixture composes and renders cleanly", async () => {
  const ids = tIndex.map((row) => row.id);
  assert.equal(new Set(ids).size, ids.length, "unique ids");
  const files = readdirSync(dir).filter((file) => /^t-.*\.json$/.test(file) && file !== "t-index.json").map((file) => file.replace(/\.json$/, ""));
  assert.deepEqual([...files].sort(), tIndex.map((row) => row.demo).sort(), "one fixture per row, none left over");
  for (const row of tIndex) {
    assert.ok(row.title && row.section && row.live, `${row.id}: title, section and how to reach it`);
    const fixture = await demo.composeFixture(row.demo, async (name) => load(name));
    const states = [fixture.state, ...fixture.then.map((step) => step.state)].filter(Boolean);
    if (!states.length) {
      assert.ok(fixture.ui.gate || fixture.ui.loading || fixture.ui.watchPrompt !== undefined, `${row.id}: a stateless screen`);
      continue;
    }
    for (const raw of states) {
      const state = asTable(raw);
      const html = state.status === "lobby"
        ? views.joinHtml(state, { ...ctx, games: fixture.ctx.games || [] })
        : views.platesHtml(state, ctx) + views.panelHtml(views.panelModel(state, ctx)) + views.slotHtml(state, ctx);
      for (const pattern of BAD_HTML) assert.doesNotMatch(html, pattern, `${row.id}: ${pattern}`);
      for (const player of state.players) assert.ok(player.id && !String(player.id).startsWith("@"), `${row.id}: player ids resolved`);
      if (state.status !== "lobby") {
        const view = camera.viewFor(camera.cameraPlan(state), rectOf);
        assert.ok([view.x, view.y, view.z].every(Number.isFinite), `${row.id}: finite camera`);
      }
    }
  }
});

test("now card: a label too long for half a row gets the whole row; card buttons shorten street words", async () => {
  const compose = (name) => demo.composeFixture(name, async (fixture) => load(fixture));
  const order = asTable((await compose("t-order-two-seats")).state);
  const orderHtml = views.panelHtml(views.panelModel(order, ctx));
  assert.equal((orderHtml.match(/panel__row--wide/g) || []).length, 2, "Roll · Robin and Roll · Maya each on their own row");
  const jail = asTable((await compose("t-jail-keyboard")).state);
  const jailModel = views.panelModel(jail, ctx);
  const jailHtml = views.panelHtml(jailModel);
  const cardRow = jailModel.rows.findIndex((row) => row.label === "Use pass");
  assert.match(jailHtml, new RegExp(`panel__row--wide" type="button" data-row="${cardRow}"`), "Use pass ×2 is wide");
  assert.doesNotMatch(jailHtml, /panel__row--wide" type="button" data-row="1"/, "Pay $50 stays half");
  const card = asTable((await compose("t-card-keyboard")).state);
  const label = views.panelModel(card, ctx).rows[0].label;
  assert.match(label, /RR$/, label);
});

// ---------------------------------------------------------------------------------------------
// Sound cues, screen settings, the menu, trade as a modal, deed cards

const { soundCues } = await import("../table/cues.js");
const screenSettings = await import("../table/settings.js");

test("sound cues: auction gavel, offers, deals, loans, mortgages and your turn for a seat here", () => {
  const roll = asTable(load("pad-turn").state);
  const maya = roll.players.find((player) => player.name === "Maya").id;
  const sam = roll.players.find((player) => player.name === "Sam").id;
  const names = (prev, next, receipts = [], local = [maya]) => soundCues(prev, next, receipts, local).map((cue) => cue.name);
  assert.deepEqual(names(roll, { ...roll, phase: "auction" }), ["gavel"]);
  assert.deepEqual(names(roll, { ...roll, phase: "trade" }), ["offer"]);
  assert.deepEqual(names(roll, { ...roll, phase: "loan_offer" }), ["offer"]);
  assert.deepEqual(names(roll, roll, [{ kind: "trade" }, { kind: "trade" }, { kind: "mortgage" }]), ["accept", "mortgage"], "each name once");
  assert.deepEqual(names(roll, roll, [{ kind: "declined" }, { kind: "loan" }, { kind: "auction" }]), ["decline", "loan", "gavel"]);
  const before = { ...roll, currentPlayerId: sam, phase: "turn_complete" };
  assert.deepEqual(names(before, roll), ["turn"], "a seat at this screen starts its turn");
  assert.deepEqual(names(before, roll, [], []), [], "nobody here: no ping");
  assert.deepEqual(names(roll, { ...roll, code: "OTHER" }), [], "another table");
  assert.deepEqual(soundCues(roll, roll, [{ kind: "sell", amount: 50 }], [])[0], { name: "coins", opts: { amount: 50, collect: true } });
});

test("screen settings: camera, zoom, animation speed and motion persist, invalid values fall back", () => {
  const data = new Map();
  const store = { getItem: (key) => data.get(key) ?? null, setItem: (key, value) => data.set(key, value) };
  assert.deepEqual(screenSettings.loadScreen(store), screenSettings.DEFAULT_SCREEN);
  assert.deepEqual(screenSettings.DEFAULT_SCREEN, { camera: "follow", zoom: 1.6, speed: "normal", motion: "system" }, "FOLLOW MOVES at 1.6× by default (review F9)");
  assert.deepEqual(screenSettings.CAMERAS, ["follow", "closeups", "off"]);
  const saved = screenSettings.saveScreen({ camera: "closeups", zoom: 2.4, speed: "fast", motion: "reduce" }, store);
  assert.deepEqual(screenSettings.loadScreen(store), saved);
  assert.equal(screenSettings.normaliseScreen({ camera: "off", zoom: 1.6 }).zoom, 1.6, "1.6× stays when picked under the new setting");
  assert.deepEqual(screenSettings.normaliseScreen({ camera: "spin", zoom: 7, speed: "warp", motion: "maybe" }), screenSettings.DEFAULT_SCREEN);
  // Saved before 0.10.1 ({ follow, zoom: 1.6 by default }): the new camera defaults, the rest kept.
  assert.deepEqual(screenSettings.normaliseScreen({ follow: false, zoom: 1.6, speed: "fast", motion: "full" }), { camera: "follow", zoom: 1.6, speed: "fast", motion: "full" });
  // Saved before version 2 at the old 2× default: the new 1.6× default. Saved since (or picked now): 2× stays.
  const old = (value) => ({ getItem: () => JSON.stringify(value) });
  assert.equal(screenSettings.loadScreen(old({ camera: "follow", zoom: 2, speed: "normal", motion: "system" })).zoom, 1.6);
  assert.equal(screenSettings.loadScreen(old({ camera: "follow", zoom: 2.4 })).zoom, 2.4, "2.4× was picked: it stays");
  assert.equal(screenSettings.loadScreen(old({ camera: "follow", zoom: 2, v: 2 })).zoom, 2, "2× picked under version 2 stays");
  assert.equal(screenSettings.normaliseScreen({ camera: "follow", zoom: 2 }).zoom, 2, "picking 2× in SETTINGS works");
  screenSettings.saveScreen({ camera: "follow", zoom: 2 }, store);
  assert.equal(JSON.parse(data.get(screenSettings.SETTINGS_KEY)).v, screenSettings.SCREEN_VERSION, "saved with the version");
  assert.equal(screenSettings.loadScreen(store).zoom, 2);
  assert.equal(screenSettings.normaliseScreen({ follow: true, zoom: 2.4 }).zoom, 2.4, "a zoom the owner picked stays");
  assert.deepEqual(screenSettings.loadScreen({ getItem: () => { throw new Error("blocked"); } }), screenSettings.DEFAULT_SCREEN, "blocked storage");
  assert.deepEqual(screenSettings.ZOOMS, [1.6, 2, 2.4]);
  assert.deepEqual(screenSettings.COMPUTER_SPEEDS, ["slow", "normal", "fast"]);
});

test("menu: resume, settings, how to play, leave and end game; end game only for the table in a game", () => {
  const menu = views.menuOverlayHtml(padState, { watching: false, canEnd: true });
  for (const cmd of ["close", "menu-settings", "menu-help", "menu-leave", "menu-end"]) assert.match(menu, new RegExp(`data-cmd="${cmd}"`));
  assert.match(views.menuOverlayHtml(padState, { canEnd: false }), /data-cmd="menu-end" disabled/);
  const lobby = asTable(load("table-lobby").state);
  const lobbyMenu = views.menuOverlayHtml(lobby, {});
  assert.doesNotMatch(lobbyMenu, /menu-leave|menu-end/, "nothing to leave or end in the lobby");
  const settings = views.settingsOverlayHtml({ muted: false, music: 0.5, effects: 0.8, musicOn: true, effectsOn: false }, { camera: "follow", zoom: 2.4, speed: "fast", motion: "reduce" }, { computerSpeed: "slow", canComputerSpeed: true, fullscreen: false });
  for (const key of ["sound", "music", "effects", "camera", "zoom", "speed", "motion", "computerSpeed", "fullscreen"]) assert.match(settings, new RegExp(`data-set="${key}"`), key);
  assert.match(settings, /data-set="zoom" data-value="2.4" aria-pressed="true"/);
  assert.match(settings, />CAMERA<[\s\S]*data-set="camera" data-value="follow" aria-pressed="true"[^>]*>FOLLOW MOVES<[\s\S]*>CLOSE-UPS ONLY<[\s\S]*data-value="off"[^>]*>OFF</, "CAMERA: FOLLOW MOVES / CLOSE-UPS ONLY / OFF");
  assert.doesNotMatch(settings, /set-note/, "no reduced-motion note unless the computer asks for it");
  const reducedBySystem = views.settingsOverlayHtml({}, { camera: "follow", zoom: 2, motion: "system" }, { systemReduced: true });
  assert.match(reducedBySystem, /set-note[\s\S]*camera is off: this computer has reduced motion/i, "says why the camera is off");
  assert.match(views.settingsOverlayHtml({}, { camera: "off", zoom: 2 }, {}), /data-set="zoom" data-value="2"[^>]*disabled/, "camera off: zoom is off too");
  assert.match(settings, /data-set="computerSpeed" data-value="slow" aria-pressed="true"/);
  assert.match(settings, /data-volume="effects"[^>]*disabled/, "effects off: its slider is off too");
  const help = views.helpOverlayHtml(padState);
  assert.equal((help.match(/<li>/g) || []).length, 5, "five rule reminders");
  assert.match(help, /Trade/);
  const end = views.endGameConfirmHtml(padState);
  const leader = views.endGameLeader(padState);
  assert.match(end, new RegExp(leader.player.name.toUpperCase()));
  assert.match(end, /data-cmd="confirm"/);
});

test("pads: Property is the View button (⧉); Start opens the menu", () => {
  const rows = views.panelModel(padState, ctx).rows;
  const property = rows.find((row) => row.cmd === "property");
  assert.equal(property.glyph, "⧉");
  assert.equal(property.button, "VIEW");
});

test("trade modal: one page with holdings by set, mini-map, the focused deed's card and the impact on both sides", () => {
  const state = structuredClone(padState);
  const meId = seat.playerId;
  const target = state.players.find((player) => player.name === "Robin");
  state.properties[16] = { ownerId: meId, houses: 0, mortgaged: false };
  const empty = views.tradeModalHtml(state, seat, views.newTradeDraft(), ctx);
  assert.match(empty, /class="trade-modal"/);
  assert.match(empty, /aria-modal="true"/);
  assert.doesNotMatch(empty, /class="deed-row[^"]*is-on/, "nothing picked on open");
  assert.match(empty, /impact--empty/);
  const html = views.tradeModalHtml(state, seat, { ...views.newTradeDraft(), targetId: target.id, offer: [21], request: [19], offerCash: 50 }, { ...ctx, peek: 19 });
  assert.match(html, /data-toggle="request:19"[^>]*aria-pressed="true"/);
  assert.match(html, /class="deal-group"/);
  assert.match(html, /FULL SET|\d OF \d/);
  assert.match(html, /minimap__svg/);
  assert.match(html, /deed-card/, "the focused deed's card");
  assert.match(html, /impact__side is-you/);
  assert.match(html, /Completes your ORANGE set/);
  assert.match(html, /data-cmd="trade-send"(?![^>]*disabled)/);
  assert.doesNotMatch(html, /undefined|NaN/);
});

test("incoming offer: big and central with both sides, the impact for this seat, ACCEPT and DECLINE", () => {
  const raw = load("trade").state;
  const state = { ...asTable(raw), localSeats: [{ seatId: "seat_t", playerId: raw.trade.targetId, input: "keyboard", pad: null }] };
  const html = views.offerModalHtml(state, state.localSeats[0]);
  assert.match(html, /TRADE OFFER/);
  assert.match(html, /data-cmd="offer-accept"/);
  assert.match(html, /data-cmd="offer-decline"/);
  assert.match(html, /impact__side is-you/);
  assert.match(html, /YOU GIVE[\s\S]*YOU GET/);
  assert.match(html, /tabindex="0" data-deed-info="\d+"/, "every deed in the offer can take focus and show its card");
  assert.doesNotMatch(html, /undefined|NaN/);
});

// A computer's counter-offer to a person at this table (0.10.1).
function counterState() {
  const raw = structuredClone(load("trade").state);
  const state = asTable(raw);
  const bot = state.players.find((player) => player.input === "ai" && player.id !== raw.trade.targetId) || state.players.find((player) => player.id !== raw.trade.targetId);
  bot.input = "ai";
  bot.kind = "ai";
  const person = state.players.find((player) => player.id !== bot.id && player.input !== "ai") || state.players.find((player) => player.id !== bot.id);
  person.input = "keyboard";
  person.kind = "human";
  state.properties[39] = { ownerId: bot.id, houses: 0, mortgaged: false };
  state.properties[28] = { ownerId: person.id, houses: 0, mortgaged: false };
  state.phase = "trade";
  state.trade = { id: "trade_9", proposerId: bot.id, targetId: person.id, offer: { cash: 0, properties: [39] }, request: { cash: 650, properties: [28] },
    returnPhase: "awaiting_roll", counterTo: "trade_8", counterRound: 1, original: { offer: { cash: 302, properties: [28] }, request: { cash: 0, properties: [39] } } };
  state.log = [...(state.log || []), { id: "l1", text: `${bot.name} countered ${person.name}'s offer` }, { id: "l2", text: `${bot.name}: I'd do it for $650 + Reservoir.` }];
  state.localSeats = [{ seatId: "seat_p", playerId: person.id, input: "keyboard", pad: null }];
  return { state, bot, person };
}

test("counter-offer: shown as the computer's reply, what changed, ACCEPT / CHANGE / WALK AWAY", () => {
  const { state, bot } = counterState();
  const html = views.offerModalHtml(state, state.localSeats[0]);
  assert.match(html, new RegExp(`${bot.name.toUpperCase()}(?:'|&#039;)S COUNTER-OFFER`));
  assert.match(html, /I(?:'|&#039;)d do it for \$650 \+ Reservoir/);
  assert.match(html, /offer-change--bad">\+\$348</, "the extra cash it asks is highlighted");
  for (const cmd of ["offer-accept", "offer-change", "offer-decline"]) assert.match(html, new RegExp(`data-cmd="${cmd}"`), cmd);
  assert.match(html, /WALK AWAY/);
  assert.doesNotMatch(html, /offer-not-selling/, "a counter answers your own offer");
  assert.doesNotMatch(html, /undefined|NaN/);
});

test("a computer's offer for your deeds: NOT SELLING per deed; the deed card turns it back on", () => {
  const { state, bot, person } = counterState();
  const plain = { ...state, trade: { ...state.trade, counterTo: undefined, original: undefined, counterRound: undefined, offer: { cash: 300, properties: [] }, request: { cash: 0, properties: [28] } } };
  const html = views.offerModalHtml(plain, plain.localSeats[0]);
  assert.match(html, /TRADE OFFER/);
  assert.match(html, /data-cmd="offer-not-selling" data-arg="28"[^>]*>[\s\S]*?NOT SELLING RESERVOIR/);
  const deed = views.deedViewHtml(plain, 28, { viewerId: person.id, seat: plain.localSeats[0] });
  assert.match(deed, /data-cmd="deed-not-selling" data-arg="28:true"[\s\S]*?DON(?:'|&#039;)T SELL TO COMPUTERS/);
  const off = structuredClone(plain);
  off.players.find((player) => player.id === person.id).notSelling = [28];
  assert.match(views.deedViewHtml(off, 28, {}), /data-arg="28:false"[^>]*aria-pressed="true">[\s\S]*?SELL AGAIN/);
  assert.equal(views.notSellingToggleHtml(plain, 39), "", "only a person's own deeds");
  void bot;
});

test("settings: COMPUTER OFFERS TO PEOPLE normal / fewer / off", () => {
  const settings = views.settingsOverlayHtml({}, {}, { botOffers: "fewer", canComputerSpeed: true });
  assert.match(settings, /COMPUTER OFFERS TO PEOPLE/);
  for (const value of ["normal", "fewer", "off"]) assert.match(settings, new RegExp(`data-set="botOffers" data-value="${value}"`));
  assert.match(settings, /data-set="botOffers" data-value="fewer" aria-pressed="true"/);
});

test("deed view: the full card with a close button", () => {
  const html = views.deedViewHtml(padState, 21, { viewerId: seat.playerId, seat });
  assert.match(html, /deed-card--lg/);
  assert.match(html, /data-cmd="deed-close"/);
});

test("bot-to-bot trades stay quiet on the TV; a human's offer still shows (review B6)", async () => {
  const trade = asTable(load("trade").state);
  // Make both sides computers.
  const bots = { ...trade, players: trade.players.map((player) => ([trade.trade.proposerId, trade.trade.targetId].includes(player.id) ? { ...player, input: "ai", kind: "ai", difficulty: "hard" } : player)), localSeats: [] };
  assert.equal(views.botDeal(bots), true);
  assert.equal(views.botDeal({ ...trade, localSeats: [] }), false);
  assert.equal(views.slotHtml(bots, ctx), "", "no deal card");
  assert.doesNotMatch(views.panelHtml(views.panelModel(bots, ctx)), /TRADE\?/, "no TRADE? on the action card");
  assert.deepEqual(views.boardMarks(bots).pick, [], "no marked squares");
  assert.match(views.slotHtml({ ...trade, localSeats: [] }, ctx), /slot-deal/, "a person's offer still shows");
  const { soundCues } = await import("../table/cues.js");
  const before = { ...bots, phase: "awaiting_roll", trade: null };
  assert.ok(!soundCues(before, bots).some((cue) => cue.name === "offer"), "no offer sound");
  const declined = { kind: "declined", playerId: trade.trade.proposerId, otherId: trade.trade.targetId, amount: 0 };
  assert.equal(views.quietReceipt(declined, bots), true, "no ✕ chip");
  assert.equal(views.quietReceipt(declined, trade), false, "a person's refusal still shows");
  assert.ok(!soundCues(before, { ...before, version: 2 }, [declined]).some((cue) => cue.name === "decline"));
});

test("eight players: the lobby has eight seats, four across; the plates carry a computer's level", () => {
  const lobby = asTable(load("table-lobby").state);
  const html = views.joinHtml(lobby, ctx);
  assert.equal((html.match(/class="seat[ "]/g) || []).length, 8, "eight seats");
  assert.match(html, /class="seats seats--8"/);
  assert.match(html, /4 \/ 8/);
  assert.equal(views.maxPlayers(lobby), 8);
  assert.equal(views.maxPlayers({ ...lobby, maxPlayers: 6 }), 6, "the server's number wins");
  const kinds = ["rocket", "teapot", "bicycle", "lighthouse", "key", "umbrella", "anchor", "kite"];
  const full = { ...lobby, players: kinds.map((tokenKind, index) => ({ ...lobby.players[0], id: `p${index}`, name: `P${index}`, tokenKind })) };
  assert.match(views.joinHtml(full, ctx), /data-cmd="add-ai" disabled/, "full at eight");
  const game = asTable(load("pad-turn").state);
  const ai = game.players.find((player) => player.input === "ai" || player.kind === "ai");
  const plates = views.platesHtml({ ...game, players: game.players.map((player) => (player.id === ai.id ? { ...player, input: "ai", difficulty: "expert" } : player)) }, ctx);
  assert.match(plates, /meta--level meta--expert">EXPERT</, "review U5");
  const gmPlates = views.platesHtml({ ...game, players: game.players.map((player) => (player.id === ai.id ? { ...player, input: "ai", difficulty: "grandmaster" } : player)) }, ctx);
  assert.match(gmPlates, /meta--level meta--grandmaster">GRANDMASTER</, "the GRANDMASTER plate");
});

test("bankruptcy at any time (0.10.1): a menu entry per person seated here, confirmed on the page", () => {
  const state = structuredClone(padState);
  const seatPlayer = state.players.find((player) => player.id === seat.playerId);
  const menu = views.menuOverlayHtml(state, { watching: false, canEnd: true });
  assert.match(menu, new RegExp(`data-cmd="menu-bankrupt" data-arg="${seat.seatId}"[^>]*>[^]*?DECLARE BANKRUPTCY · ${seatPlayer.name.toUpperCase()}`));
  assert.equal(views.canResign(state, seat.playerId), true, "with cash, outside the debt phase");
  const ai = state.players.find((player) => player.input === "ai");
  assert.equal(views.canResign(state, ai.id), false, "computers decide for themselves");
  assert.equal(views.canResign({ ...state, phase: "determining_order" }, seat.playerId), false, "not before the turn order");
  assert.doesNotMatch(views.menuOverlayHtml(state, { watching: true }), /menu-bankrupt/, "watchers can't");
  assert.match(views.resignText(state, seat.playerId), new RegExp(`${seatPlayer.name} will be out of the game, and their property goes to the bank[.]`));
  const creditor = state.players.find((player) => player.id !== seat.playerId);
  const debt = { ...state, phase: "debt", debt: { payerId: seat.playerId, creditorId: creditor.id, amount: 50 } };
  assert.match(views.resignText(debt, seat.playerId), new RegExp(`property goes to ${creditor.name}[.]`));
  // The debt card still has its BANKRUPT row (Q).
  assert.ok(views.panelModel(debt, ctx).rows.some((row) => row.cmd === "bankrupt" && row.key === "Q"));
});

// ---- 0.10.1: click any property for what you can do with it ----

function deedTable(own = {}, extra = {}) {
  const state = structuredClone(load("roll").state);
  const robin = state.players.find((player) => player.name === "Robin");
  const maya = state.players.find((player) => player.name === "Maya");
  robin.input = "keyboard";
  for (const [index, [ownerId, houses = 0, mortgaged = false]] of Object.entries(own)) state.properties[index] = { ownerId, houses, mortgaged };
  Object.assign(state, { currentPlayerId: robin.id, phase: "awaiting_roll", trade: null, ...extra });
  return { state: asTable(state), robin, maya };
}

test("deed card: your mortgaged deed shows UNMORTGAGE with interest for your seat; BUILD greyed with why", () => {
  const { state, robin } = deedTable({ 37: [null], 39: [null] });
  state.properties[37] = { ownerId: robin.id, houses: 0, mortgaged: false };
  state.properties[39] = { ownerId: robin.id, houses: 0, mortgaged: true };
  const html = views.deedViewHtml(state, 39, {});
  assert.match(html, /class="deed-view__row"/);
  assert.match(html, /data-deed-act="\{&quot;type&quot;:&quot;unmortgage&quot;,&quot;spaceIndex&quot;:39\}" data-seat="seat_[^"]+" data-deed-action="unmortgage">/);
  assert.match(html, /UNMORTGAGE<\/span><span class="deed-act__detail">−\$220 with interest/);
  assert.match(html, /data-deed-action="build" disabled>[\s\S]*?<p class="deed-act__why" data-why="build">Unmortgage the dark blue set first<\/p>/);
  assert.match(html, /data-cmd="deed-trade" data-arg="39"[^>]*data-deed-action="trade">/);
  assert.match(html, /data-cmd="deed-not-selling" data-arg="39:true"/);
  const seatId = state.localSeats.find((seat) => seat.playerId === robin.id).seatId;
  assert.equal(views.deedActorSeat(state, 39).seatId, seatId);
  assert.doesNotMatch(html, /undefined|NaN/);
});

test("deed card: a built set sells evenly; MORTGAGE and TRADE greyed until the houses go", () => {
  const { state, robin } = deedTable();
  state.properties[37] = { ownerId: robin.id, houses: 1, mortgaged: false };
  state.properties[39] = { ownerId: robin.id, houses: 2, mortgaged: false };
  const html = views.deedViewHtml(state, 39, {});
  assert.match(html, /data-deed-act="\{&quot;type&quot;:&quot;sell_building&quot;,&quot;spaceIndex&quot;:39\}"[^>]*data-deed-action="sell">/);
  assert.match(html, /SELL A HOUSE<\/span><span class="deed-act__detail">\+\$100/);
  assert.match(html, /data-why="build">Build evenly: next on /);
  assert.match(html, /data-why="mortgage">Sell the buildings on the dark blue set first/);
  assert.match(html, /data-why="trade">Sell the buildings on the dark blue set first/);
  const low = views.deedViewHtml(state, 37, {});
  assert.match(low, /class="big-btn big-btn--small deed-act big-btn--primary"[^>]*data-deed-action="build">/, "BUILD live on the low street");
});

test("deed card: someone else's deed → ASK TO BUY…; unowned is info only unless you're on it (BUY)", () => {
  const { state } = deedTable();
  const alpha = state.players.find((player) => player.name === "Alpha");
  assert.equal(state.properties[13].ownerId, alpha.id);
  const ask = views.deedViewHtml(state, 13, {});
  assert.match(ask, /data-cmd="deed-ask" data-arg="13" data-seat="seat_[^"]+" data-deed-action="ask">[\s\S]*?ASK TO BUY…<\/span><span class="deed-act__detail">Alpha/);
  assert.doesNotMatch(ask, /data-deed-action="(mortgage|build|trade)"/);
  const free = views.deedViewHtml(state, 1, {});
  assert.doesNotMatch(free, /deed-view__acts/, "an unowned deed: just the card");
  assert.match(free, /deed-view__close/);
  const landing = deedTable({}, { phase: "awaiting_purchase", pendingPurchase: 1 }).state;
  assert.match(views.deedViewHtml(landing, 1, {}), /data-deed-act="\{&quot;type&quot;:&quot;buy&quot;\}"[^>]*data-deed-action="buy">[\s\S]*?BUY<\/span><span class="deed-act__detail">\$60/);
  assert.doesNotMatch(views.deedViewHtml(landing, 3, {}), /data-deed-action="buy"/);
});

test("deed card: off your turn its actions are greyed with a one-line reason; the owner's seat acts", () => {
  const { state, maya } = deedTable();
  const sam = state.players.find((player) => player.name === "Sam");
  const off = { ...state, currentPlayerId: sam.id };
  const html = views.deedViewHtml(off, 18, {});
  const seat = off.localSeats.find((candidate) => candidate.playerId === maya.id);
  assert.match(html, new RegExp(`data-seat="${seat.seatId}"`), "Maya (at this table) acts on her deed while Sam (a phone) plays");
  assert.match(html, /data-deed-action="mortgage" disabled>[\s\S]*?data-why="mortgage">On your turn</);
  assert.match(html, /data-deed-action="trade" disabled>[\s\S]*?data-why="trade">Trade on your turn</);
  assert.match(html, /<p class="deed-act__for">MAYA<\/p>/, "with two seats here, whose actions they are");
  // A spectator-only table (no seats): just the card.
  assert.doesNotMatch(views.deedViewHtml({ ...off, localSeats: [] }, 18, {}), /deed-view__acts/);
});

test("deed card: table.js sends its actions for the acting seat and opens the composer pre-filled (source check)", () => {
  const src = readFileSync(join(here, "..", "table", "table.js"), "utf8");
  assert.match(src, /closest\("\[data-deed-act\]"\)[\s\S]{0,200}perform\(action\.dataset\.seat, JSON\.parse\(action\.dataset\.deedAct\)\)/);
  assert.match(src, /targetId: ownerId, request: \[index\]/, "ASK TO BUY asks its owner for it");
  assert.match(src, /offer: add\(base\.offer\)/, "TRADE THIS puts it on your side");
  assert.match(src, /refreshQuote\(ui\.overlay\)/);
});

test("lobby: WHATSAPP and COPY LINK under the QR code on the PC, not for a watching screen (0.11.1)", () => {
  const lobby = asTable(load("table-lobby").state);
  const html = views.joinHtml(lobby, ctx);
  assert.match(html, /qr-card__share[\s\S]*?data-cmd="invite-whatsapp"[\s\S]*?WHATSAPP[\s\S]*?data-cmd="invite-copy"/);
  assert.doesNotMatch(views.joinHtml({ ...lobby, viewerRole: "spectator" }, ctx), /invite-whatsapp|invite-copy/);
});

test("sound pill: the TV asks for a click or key press while the browser holds sound back (review F1)", () => {
  const on = { muted: false, musicOn: true, effectsOn: true };
  assert.equal(views.soundPillShown({ ...on, locked: true }), true);
  assert.equal(views.soundPillShown({ ...on, locked: false }), false, "sound already running");
  assert.equal(views.soundPillShown({ ...on, muted: true, locked: true }), false, "sound turned off: nothing to ask for");
  assert.equal(views.soundPillShown({ muted: false, musicOn: false, effectsOn: false, locked: true }), false);
  assert.match(views.soundPillHtml(), /CLICK OR PRESS ANY KEY FOR SOUND/);
  // The speaker is struck through until then, and says why.
  assert.ok(views.soundButtonHtml({ ...on, locked: true }).includes(views.ICONS.speakerOff));
  assert.ok(views.soundButtonHtml({ ...on, locked: false }).includes(views.ICONS.speaker));
  assert.equal(views.soundLabel({ ...on, locked: true }), "Sound (click or press any key to turn it on)");
  assert.equal(views.soundLabel({ ...on, muted: true, locked: true }), "Sound (off)");
  const source = readFileSync(join(here, "..", "table", "table.js"), "utf8");
  assert.match(source, /sound\.prepareAudio\(\);\s*\n\s*sound\.installAudioUnlock\(window\)/, "starts sound at load where the browser allows it");
  assert.match(source, /sound\.onAudioState\(/, "follows the browser's sound state");
  const html = readFileSync(join(here, "..", "table", "index.html"), "utf8");
  assert.match(html, /id="sound-pill"/);
  const platform = readFileSync(join(here, "..", "..", "cmd", "estate", "platform.go"), "utf8");
  assert.match(platform, /--autoplay-policy=no-user-gesture-required/, "the kiosk launch allows sound without a click");
});

test("outage: a bar across the TV and inert action buttons while the server is unreachable (review F10)", async () => {
  const client = await import("../shared/client.js");
  for (const status of [0, 502, 503, 504]) assert.equal(client.unreachable({ status }), true, `${status}`);
  for (const status of [400, 404, 409, 500]) assert.equal(client.unreachable({ status }), false, `${status}`);
  assert.ok(client.OFFLINE_GRACE_MS <= 4000, "says offline within 4 s");
  assert.ok(client.ACTION_GRACE_MS < client.OFFLINE_GRACE_MS, "sooner after a failed action");
  assert.match(views.netbarHtml(), /RECONNECTING…/);
  assert.match(views.netbarHtml(), /game is safe/);
  const source = readFileSync(join(here, "..", "table", "table.js"), "utf8");
  const panel = source.slice(source.indexOf("function renderPanel("), source.indexOf("function renderNetbar("));
  assert.match(panel, /const inert = ui\.busy \|\| offline\(\)/, "the action card is inert while offline");
  const perform = source.slice(source.indexOf("async function perform("), source.indexOf("async function runRow("));
  assert.match(perform, /if \(offline\(\)\)/, "a key or controller press while offline is refused, with a reason");
  assert.match(perform, /unreachable\(error\)\) ui\.live\?\.actionFailed\(\)/);
  const css = readFileSync(join(here, "..", "phone", "phone.css"), "utf8");
  for (const selector of [".main .actions .btn", ".pkact .pkbtn", ".ldp-roll", ".ldp-move"]) {
    assert.ok(css.includes(`.app[data-connection="offline"] ${selector}`), `the phone dims ${selector} while offline`);
  }
});

test("music: louder and brighter on small speakers, new variations each session (review F6)", async () => {
  const sound = await import("../shared/sound.js");
  assert.ok(sound.MUSIC_LEVEL >= 2.5 && sound.MUSIC_LEVEL <= 3.2, "about +12 dB on the old 0.7");
  assert.equal(sound.audioLocked(), false, "no WebAudio (Node): nothing to unlock");
  const source = readFileSync(join(here, "..", "shared", "sound.js"), "utf8");
  assert.match(source, /lowCut\.type = "highpass"/);
  assert.match(source, /air\.type = "highshelf"/);
  const music = readFileSync(join(here, "..", "shared", "music.js"), "utf8");
  assert.match(music, /const SESSION_SEED = Math\.floor\(Math\.random\(\)/, "seeded per session");
  assert.match(music, /remember\(old\);/, "a mood picks up where it left off after an auction");
});

test("fonts: served by Estate, never Google (review F18: a home network without internet lost them)", () => {
  const web = join(here, "..");
  const css = readFileSync(join(web, "shared", "fonts.css"), "utf8");
  const files = [...css.matchAll(/url\("\/shared\/fonts\/([^"]+)"\)/g)].map((m) => m[1]);
  assert.deepEqual(files.sort(), ["Inter-latin.woff2", "Oswald-latin.woff2"]);
  for (const file of files) {
    const data = readFileSync(join(web, "shared", "fonts", file));
    assert.equal(data.toString("latin1", 0, 4), "wOF2", `${file} is a WOFF2 font`);
  }
  assert.match(css, /font-family: "Oswald";[^}]*font-weight: 200 700;/, "Oswald covers 500–700");
  assert.match(css, /font-family: "Inter";[^}]*font-weight: 100 900;/, "Inter covers 400–800");
  for (const page of ["table/index.html", "phone/index.html", "shared/board-demo.html"]) {
    const html = readFileSync(join(web, page), "utf8");
    assert.doesNotMatch(html, /googleapis|gstatic/, `${page} doesn't load Google Fonts`);
    assert.ok(html.includes('<link rel="stylesheet" href="/shared/fonts.css">'), `${page} links fonts.css`);
    for (const file of files) assert.ok(html.includes(`<link rel="preload" href="/shared/fonts/${file}" as="font" type="font/woff2" crossorigin>`), `${page} preloads ${file}`);
  }
});
