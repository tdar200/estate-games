// Trade for the phone, on one page (round-2 review §6): partner chips on top (none preselected),
// YOU GIVE and YOU GET side by side with compact deed tiles and a cash stepper under each, and a
// sticky footer with one live effect line, the value bar and SEND. A computer's price is a small
// optional chip ("ASK $82") that changes the deal only when tapped. The page never restores an old
// draft: a trade saved for your turn is a card at the top. LOAN is a segment of the same page,
// set with a stepper and chips (amount, +%, due). Also the incoming-offer review. Pure.

import { GROUP_COLORS, GROUP_INK, colourWord, counterChanges, escapeHtml, inputOf, lastReply, money, playerById, quoteState, upper } from "../shared/format.js";
import * as insights from "../shared/insights.js";
import { dealBalance, groupIndexes, loanSecuredBy, ownedBy, ownsGroup, pledgeable, propState, rentLabel, rulesOf, tradeEffects, tradeable } from "../shared/insights.js";
import { shortName } from "./cues.js";
import { icon } from "./icons.js";
import { btn, buttonsHtml } from "./parts.js";
import { impactHtml, miniMapHtml, tradeImpact, verdictsHtml } from "./impact.js";
import { discHtml } from "./strip.js";

const e = escapeHtml;
export const LOAN_RATES = [0.1, 0.2, 0.5];
export const LOAN_DUES = [3, 6, 10];

export function emptyDraft() {
  return { targetId: null, offer: [], request: [], offerCash: 0, requestCash: 0, quote: null, planned: false };
}

export function normaliseDraft(draft) {
  const base = { ...emptyDraft(), ...(draft || {}) };
  delete base.step;
  if (typeof base.quote === "string") base.quote = base.quote ? { text: base.quote } : null;
  return base;
}

// Deeds a player can put in a deal right now (no buildings in the set, not pledged for a loan).
export function tradeableNow(state, playerId) {
  return tradeable(state, playerId).filter((index) => !loanSecuredBy(state, index));
}

// Drop anything that no longer belongs to the right player, so the counts the screen shows and the
// offer that gets sent are the same thing.
export function cleanDraft(state, draft, viewerId) {
  const d = normaliseDraft(draft);
  const target = playerById(state, d.targetId);
  const live = target && !target.bankrupt && target.id !== viewerId;
  const mine = new Set(tradeableNow(state, viewerId));
  const theirs = new Set(live ? tradeableNow(state, target.id) : []);
  const offer = d.offer.filter((index) => mine.has(index));
  const request = d.request.filter((index) => theirs.has(index));
  const dropped = d.offer.length - offer.length + d.request.length - request.length;
  const me = playerById(state, viewerId);
  return {
    draft: {
      ...d,
      targetId: live ? d.targetId : null,
      offer,
      request,
      offerCash: Math.max(0, Math.min(Math.round(d.offerCash || 0), me?.cash ?? 0)),
      requestCash: live ? Math.max(0, Math.min(Math.round(d.requestCash || 0), target?.cash ?? 0)) : 0,
    },
    dropped,
  };
}

export function giveCount(draft) {
  return draft.offer.length + (draft.offerCash > 0 ? 1 : 0);
}

export function getCount(draft) {
  return draft.request.length + (draft.requestCash > 0 ? 1 : 0);
}

// Both sides must hold something, and at least one deed must move (no cash-for-cash).
export function tradeValid(draft) {
  return Boolean(draft.targetId) && giveCount(draft) > 0 && getCount(draft) > 0 && draft.offer.length + draft.request.length > 0;
}

// Why SEND is greyed, in two or three words (null when the deal can go).
export function tradeBlocker(draft) {
  if (!draft.targetId) return "pick a partner";
  if (!giveCount(draft) && !getCount(draft)) return "tap some deeds";
  if (!giveCount(draft)) return "add to YOU GIVE";
  if (!getCount(draft)) return "add to YOU GET";
  if (!draft.offer.length && !draft.request.length) return "a deed must move";
  return null;
}

export function isMyTradeTurn(state, viewerId) {
  return state?.currentPlayerId === viewerId && ["awaiting_roll", "turn_complete"].includes(state.phase);
}

export function tradeActionOf(draft) {
  return {
    type: "offer_trade",
    targetId: draft.targetId,
    offer: { cash: Math.max(0, Math.round(draft.offerCash || 0)), properties: [...draft.offer] },
    request: { cash: Math.max(0, Math.round(draft.requestCash || 0)), properties: [...draft.request] },
  };
}

// A saved plan that is still valid ({ target, draft }) or null. `plan` is ui.plan (a draft with
// planned: true), kept apart from the page's draft so the page never reopens pre-filled.
export function plannedTrade(state, plan) {
  if (!plan?.planned || !state?.viewerId) return null;
  const { draft: clean } = cleanDraft(state, plan, state.viewerId);
  const target = playerById(state, clean.targetId);
  if (!target || !tradeValid(clean)) return null;
  return { target, draft: clean };
}

// The deeds a computer price was asked for. A quote only counts while the deeds are the same.
export function quoteKey(draft) {
  return `${draft.targetId || ""}|${[...draft.offer].sort((a, b) => a - b).join(",")}|${[...draft.request].sort((a, b) => a - b).join(",")}`;
}

// Whether to ask the computer for its price (the server quotes only on your own turn).
export function wantsQuote(state, draft) {
  const target = playerById(state, draft.targetId);
  return Boolean(target && inputOf(target) === "ai" && draft.offer.length + draft.request.length > 0 && isMyTradeTurn(state, state.viewerId));
}

// ---- Loans: repay follows the chosen rate, so lowering the amount lowers the repayment. ----

export function loanRate(draft) {
  if (typeof draft?.rate === "number") return draft.rate;
  if (draft?.repay && draft?.principal) {
    const implied = draft.repay / draft.principal - 1;
    return LOAN_RATES.reduce((best, rate) => (Math.abs(rate - implied) < Math.abs(best - implied) ? rate : best), LOAN_RATES[1]);
  }
  return 0.2;
}

export function loanRepay(draft) {
  return Math.max(draft.principal, Math.round(draft.principal * (1 + loanRate(draft))));
}

export function loanActionOf(draft) {
  return { type: "offer_loan", targetId: draft.targetId, direction: draft.direction, principal: draft.principal, repay: loanRepay(draft), dueTurns: draft.dueTurns, collateral: [...(draft.collateral || [])] };
}

// ---------------------------------------------------------------------------------------------
// Pieces

function spaceColour(space) {
  return space.type === "property" ? [GROUP_COLORS[space.group], GROUP_INK[space.group]] : ["#5B6470", "#FFFFFF"];
}

function cardRent(state, index) {
  const prop = propState(state, index);
  const space = state.board[index];
  if (prop?.mortgaged) return "mortgaged";
  if (space.type === "utility") return rentLabel(state, index) || "4× dice";
  return rentLabel(state, index) || money(space.rents?.[0] || 25);
}

// Badge that says what moving this one deed does to sets.
function cardTag(state, index, { fromId, toId, viewerId }) {
  const space = state.board[index];
  if (space.type !== "property" || !toId) return null;
  const group = space.group;
  const colour = upper(colourWord(group));
  const after = { ...state, properties: { ...state.properties, [index]: { ...propState(state, index), ownerId: toId } } };
  if (toId === viewerId && ownsGroup(after, viewerId, group)) return { text: `COMPLETES ${colour}`, tone: "good" };
  if (fromId === viewerId && ownsGroup(after, toId, group)) return { text: `GIVES ${colour} SET`, tone: "bad" };
  if (fromId === viewerId && ownsGroup(state, viewerId, group)) return { text: "BREAKS YOUR SET", tone: "bad" };
  if (toId === viewerId && ownsGroup(state, fromId, group)) return { text: "BREAKS THEIR SET", tone: "good" };
  return null;
}

function cardInner(state, index, { tag = null, on = false, note = "" } = {}) {
  const space = state.board[index];
  const [bg, ink] = spaceColour(space);
  const foot = tag ? `<span class="tcard__tag tcard__tag--${tag.tone}">${e(tag.text)}</span>` : `<span class="tcard__row">${note || e(cardRent(state, index))}</span>`;
  return `<span class="tcard__head" style="background:${bg};color:${ink}">${e(upper(shortName(space.name)))}</span>${foot}${on ? `<span class="tcard__tick" aria-hidden="true">${icon("check", { size: 10 })}</span>` : ""}`;
}

function deedCard(state, index, { field, on, enabled, fromId, toId, viewerId }) {
  const space = state.board[index];
  const tag = enabled ? cardTag(state, index, { fromId, toId, viewerId }) : null;
  const inner = cardInner(state, index, { tag, on });
  const info = `<span class="tcard__info" data-sheet="${index}" role="button" tabindex="0" aria-label="${e(space.name)}: details"></span>`;
  if (!enabled) return `<span class="tcard is-off" aria-label="${e(space.name)}">${inner}${info}</span>`;
  return `<button type="button" class="tcard${on ? " is-on" : ""}" data-toggle="${field}:${index}" aria-pressed="${on}" aria-label="${e(space.name)}${on ? ", in the deal" : ""}">${inner}${info}</button>`;
}

function lockedCard(state, index) {
  const space = state.board[index];
  const reason = typeof insights.untradeable === "function" ? insights.untradeable(state, propState(state, index)?.ownerId).find((entry) => entry.index === index)?.reason : null;
  const loan = reason ? reason === "PLEDGED" : Boolean(loanSecuredBy(state, index));
  return `<span class="tcard is-locked" aria-label="${e(space.name)}: ${loan ? "loan security" : "sell houses first"}">${cardInner(state, index, { note: `${icon(loan ? "lock" : "house", { size: 10 })} ${loan ? "loan" : "houses"}` })}${`<span class="tcard__info" data-sheet="${index}" role="button" tabindex="0" aria-label="${e(space.name)}: details"></span>`}</span>`;
}

// Amount on top, four steps under it (fits a 150 px column).
function cashStepper(cmd, field, value, max, enabled, { min = 0 } = {}) {
  const step = (delta, label) => {
    const next = Math.max(min, Math.min(max, value + delta));
    return `<button type="button" class="tstep" data-cmd="${cmd}" data-arg="${field}:${delta}" ${!enabled || next === value ? "disabled" : ""} aria-label="${delta > 0 ? "Add" : "Remove"} $${Math.abs(delta)}">${label}</button>`;
  };
  return `<div class="tcash"><b class="tcash__amt" aria-live="polite">${icon("coin", { size: 14 })}${money(value)}</b><div class="tcash__steps">${step(-50, "−50")}${step(-10, "−10")}${step(10, "+10")}${step(50, "+50")}</div></div>`;
}

function sideHtml(state, { label, owner, field, picked, cash, cashMax, enabled, fromId, toId, viewerId, emptyText }) {
  const free = owner ? tradeableNow(state, owner.id) : [];
  const freeSet = new Set(free);
  const locked = owner ? ownedBy(state, owner.id).filter((index) => !freeSet.has(index)) : [];
  const lockedSet = new Set(locked);
  const all = owner ? ownedBy(state, owner.id) : [];
  const cards = setGroups(state, all).map(({ label, colour, members, size }) => {
    const head = `<span class="tgroup" style="--g:${colour}"><i></i>${e(label)}<b>${members.length}/${size}</b></span>`;
    const tiles = members.map((index) => (lockedSet.has(index) ? lockedCard(state, index) : deedCard(state, index, { field, on: picked.includes(index), enabled, fromId, toId, viewerId }))).join("");
    return head + tiles;
  }).join("");
  const body = cards ? `<div class="tgrid">${cards}</div>` : `<div class="tnone">${e(emptyText)}</div>`;
  return `<section class="tside${enabled ? "" : " is-off"}" aria-label="${e(label)}">
    <div class="tside__head"><span>${e(label)}</span>${cards ? '<small class="tside__note">RENT</small>' : ""}</div>
    ${body}
    ${cashStepper("trade-cash", field, cash, cashMax, enabled)}
  </section>`;
}

// A player's deeds in board order, grouped by colour set (then railroads, utilities), with how
// much of each set they hold.
function setGroups(state, indexes) {
  const groups = new Map();
  for (const index of [...indexes].sort((a, b) => a - b)) {
    const space = state.board[index];
    const key = space.type === "property" ? space.group : space.type;
    if (!groups.has(key)) {
      const size = space.type === "property" ? groupIndexes(state, space.group).length : space.type === "railroad" ? 4 : 2;
      const label = space.type === "property" ? upper(colourWord(space.group)) : space.type === "railroad" ? "RAILROADS" : "UTILITIES";
      groups.set(key, { label, colour: spaceColour(space)[0], members: [], size });
    }
    groups.get(key).members.push(index);
  }
  return [...groups.values()];
}

// "WHAT THIS DEAL DOES": plain verdicts, before → after for both sides, and the board map. On the
// phone it folds away (ui.tradeInfo); on tablet and PC it is always open (phone.css). In the trade
// builder on the phone the folded entry is the verdict line in the footer (dockToggle), so the
// page keeps one row less and fits the screen.
function dealInfoHtml(state, trade, viewerId, { ui = {}, give = [], get = [], dockToggle = false } = {}) {
  const wide = Boolean(ui.layout) && ui.layout !== "phone";
  const open = wide || Boolean(ui.tradeInfo);
  if (!open && dockToggle) return "";
  const toggle = wide ? "<h3 class=\"tinfo__title\">WHAT THIS DEAL DOES</h3>" : `<button type="button" class="tinfo__toggle" data-cmd="trade-info" aria-expanded="${open}">WHAT THIS DEAL DOES <span aria-hidden="true">${open ? "▴" : "▾"}</span></button>`;
  const body = open ? `<div class="tinfo__body">${impactHtml(state, tradeImpact(state, trade, viewerId))}${miniMapHtml(state, { give, get })}</div>` : "";
  return `<section class="tinfo${open ? " is-open" : ""}">${toggle}${body}</section>`;
}

// What the deal is worth. When a colour set changes hands that is what matters, not list prices
// (review U8: a deal completing your set used to show a mostly red bar): one line per set made,
// green when it is yours, red when it is theirs, with the most it can charge (a hotel), and the
// list values small beside it. Otherwise one compact row: GIVE $300 [red|green] GET $280.
function balanceHtml(state, trade, viewerId) {
  const deal = dealBalance(state, trade, viewerId);
  const made = deal.sets.filter((set) => set.gain).sort((a, b) => Number(b.you) - Number(a.you)).slice(0, 2);
  if (made.length) {
    const lines = made.map((set) => `<span class="tset tset--${set.you ? "good" : "bad"}"><i style="background:${GROUP_COLORS[set.group]}"></i><span>${set.you ? "YOUR" : `${e(upper(set.name))}'S`} ${e(upper(colourWord(set.group)))} SET</span><b>UP TO ${money(set.rent)}</b></span>`).join("");
    return `<div class="tsum tsum--sets tsum--${deal.tone}" aria-label="You give ${money(deal.give)}, you get ${money(deal.get)} at list prices">${lines}<span class="tsum__list">LIST ${money(deal.give)} ⇄ ${money(deal.get)}</span></div>`;
  }
  return valueBarHtml(deal.give, deal.get);
}

function valueBarHtml(giveValue, getValue) {
  const bar = giveValue || getValue
    ? `<span style="flex:${Math.max(giveValue, 1)};background:#E8232A"></span><span style="flex:${Math.max(getValue, 1)};background:#1C8A47"></span>`
    : '<span style="flex:1;background:rgba(244,241,230,0.18)"></span>';
  return `<div class="tsum" aria-label="You give ${money(giveValue)}, you get ${money(getValue)}"><span class="tsum__give">GIVE <b>${money(giveValue)}</b></span><span class="tbal">${bar}</span><span class="tsum__get">GET <b>${money(getValue)}</b></span></div>`;
}

// The computer's price (asked when the deeds change; your cash is compared here): "ACCEPTS AT
// $650 · $348 MORE NEEDED" (tap: the price goes in), "✓ WILL ACCEPT", "WON'T TRADE AT ANY PRICE",
// "THINKING …".
export function quoteChip(draft) {
  const quote = draft.quote;
  if (!quote || quote.key !== quoteKey(draft)) return null;
  const q = quoteState(quote, draft.offerCash, draft.requestCash);
  if (q.kind === "wait") return { html: "THINKING …", disabled: true };
  if (q.kind === "error") return null;
  const two = (top, bottom) => `<span class="tquote__top">${top}</span><span class="tquote__sub">${e(bottom)}</span>`;
  if (q.kind === "never") return { html: two("WON&#039;T TRADE", q.detail ? upper(q.detail) : "AT ANY PRICE"), disabled: true, tone: "never" };
  if (q.kind === "short") return { html: two("TOO DEAR", "MORE THAN YOU HAVE"), disabled: true, tone: "never" };
  const arg = `${q.offerCash}:${q.requestCash}`;
  if (q.kind === "ok" && q.offerCash === draft.offerCash && q.requestCash === draft.requestCash) return { html: two(`${icon("check", { size: 12 })} WILL ACCEPT`, q.text), disabled: true, tone: "ok" };
  if (q.kind === "ok") return { html: two(`${icon("check", { size: 12 })} WILL ACCEPT`, q.text), cmd: "trade-quote-use", arg, tone: "ok" };
  return { html: two(e(q.text), q.status), cmd: "trade-quote-use", arg, gold: true };
}

// The single most important consequence of a deal, coloured good or bad for you.
function effectLine(state, trade, viewerId) {
  const effect = tradeEffects(state, trade, viewerId).find((item) => item.strong !== "No set changes hands.");
  if (!effect) return "";
  const tone = effect.tone === "info" && /goes to \d+ railroads/.test(effect.strong) ? "warn" : effect.tone;
  return `<div class="teffect teffect--${tone === "good" ? "good" : tone === "warn" ? "bad" : "info"}">${icon(tone === "good" ? "house" : tone === "warn" ? "warn" : "coin")}<span>${e(effect.strong.replace(/[.,]\s*$/, ""))}</span></div>`;
}

function pile(state, owner, label, properties, cash) {
  const cards = properties.map((index) => `<button type="button" class="tcard tcard--static" data-sheet="${index}" aria-label="${e(state.board[index].name)}: details">${cardInner(state, index)}<span class="tcard__info" aria-hidden="true"></span></button>`).join("");
  return `<div class="tpile"><div class="tpile__who">${discHtml(state, owner, { size: 18 })}${e(label)}</div>${cards}${cash > 0 ? `<span class="tmoney tmoney--big">${money(cash)}</span>` : ""}${!cards && !(cash > 0) ? '<span class="tpile__none">nothing</span>' : ""}</div>`;
}

function partnerChips(state, player, selectedId, attr) {
  return state.players.filter((candidate) => candidate.id !== player.id && !candidate.bankrupt).map((candidate) => {
    const on = candidate.id === selectedId;
    const bot = inputOf(candidate) === "ai" ? `<span class="tchip__bot" aria-label="computer">${icon("bot", { size: 12 })}</span>` : "";
    const data = attr === "trade" ? `data-cmd="trade-partner" data-arg="${e(candidate.id)}"` : `data-loan-partner="${e(candidate.id)}"`;
    return `<button type="button" class="tchip${on ? " is-on" : ""}" ${data} aria-pressed="${on}">${discHtml(state, candidate, { size: 26 })}<span class="tchip__name">${e(upper(candidate.name))}${bot}</span><span class="tchip__cash">${money(candidate.cash)}</span></button>`;
  }).join("");
}

function topHtml(title, { mode = "trade", loans = false, clear = false } = {}) {
  const seg = loans
    ? `<div class="tseg" role="group" aria-label="Trade or loan"><button type="button" class="tseg__btn${mode === "trade" ? " is-on" : ""}" data-cmd="trade-mode" data-arg="trade" aria-pressed="${mode === "trade"}">TRADE</button><button type="button" class="tseg__btn${mode === "loan" ? " is-on" : ""}" data-cmd="trade-mode" data-arg="loan" aria-pressed="${mode === "loan"}">LOAN</button></div>`
    : `<h2 class="ttitle">${e(title)}</h2>`;
  return `<div class="ttop">
      <button type="button" class="tback" data-cmd="${mode === "loan" ? "loan-cancel" : "trade-close"}" aria-label="Close">${icon("cross", { size: 12 })}</button>
      ${seg}
      ${clear ? '<button type="button" class="tclear" data-cmd="trade-clear">CLEAR</button>' : ""}
    </div>`;
}

// "PLANNED · ALPHA: Central Rd ⇄ Theater St [SEND] [✕]" — a saved trade, never silently restored.
function planCard(state, plan, myTurn) {
  const found = plannedTrade(state, plan);
  if (!found) return "";
  const { target, draft } = found;
  const side = (properties, cash) => [...properties.map((index) => shortName(state.board[index].name)), cash > 0 ? money(cash) : ""].filter(Boolean).join(" + ");
  return `<div class="tplan">
      <div class="tplan__main"><span class="tplan__eyebrow">${discHtml(state, target, { size: 16 })}PLANNED · ${e(upper(target.name))}</span><span class="tplan__deal">${e(side(draft.offer, draft.offerCash))} ⇄ ${e(side(draft.request, draft.requestCash))}</span></div>
      ${myTurn ? '<button type="button" class="small-btn is-gold" data-cmd="trade-send-plan">SEND</button>' : ""}
      <button type="button" class="tplan__x" data-cmd="trade-plan-discard" aria-label="Discard the planned trade">${icon("cross", { size: 12 })}</button>
    </div>`;
}

// ---------------------------------------------------------------------------------------------
// Screens

function pendingScreen(state, player, ui) {
  const pending = state.trade;
  const incoming = pending.targetId === player.id;
  const other = playerById(state, incoming ? pending.proposerId : pending.targetId);
  const youGive = incoming ? pending.request : pending.offer;
  const youGet = incoming ? pending.offer : pending.request;
  const give = { properties: youGive.properties || [], cash: youGive.cash || 0 };
  const get = { properties: youGet.properties || [], cash: youGet.cash || 0 };
  // A computer's counter-offer answers your own offer: its words and what changed, then ACCEPT /
  // CHANGE / WALK AWAY. A computer's own offer for your deeds gets NOT SELLING <deed> chips.
  const fromComputer = incoming && inputOf(other) === "ai";
  const counter = fromComputer && Boolean(pending.counterTo);
  const reply = counter ? lastReply(state, other.id) : "";
  const changes = counter ? counterChanges(state, pending, player.id) : [];
  const title = counter ? `${e(upper(other?.name || ""))}'S COUNTER-OFFER` : incoming ? `${e(upper(other?.name || ""))} OFFERS` : "OFFER SENT";
  const replyHtml = counter ? `<div class="treply" role="status">${reply ? `<q>${e(reply)}</q>` : ""}${changes.map((c) => `<span class="tchange tchange--${c.tone}">${e(c.text)}</span>`).join("")}</div>` : "";
  const main = `<div class="trade trade--review">
      <h2 class="ttitle ttitle--center">${title}</h2>
      ${replyHtml}
      ${verdictsHtml(tradeImpact(state, pending, player.id))}
      <div class="treview">${pile(state, player, "YOU GIVE", give.properties, give.cash)}<div class="tswap" aria-hidden="true">⇄</div>${pile(state, other, "YOU GET", get.properties, get.cash)}</div>
      ${balanceHtml(state, pending, player.id)}
      ${dealInfoHtml(state, pending, player.id, { ui: ui || {}, give: give.properties, get: get.properties })}
    </div>`;
  const buttons = counter
    ? [btn("ACCEPT", "", "primary", { act: { type: "accept_trade" } }), btn("CHANGE", "edit it and send it back", "ghost", { cmd: "trade-counter-change" }), btn("WALK AWAY", "", "ghost", { act: { type: "reject_trade" } })]
    : incoming
      ? [btn("ACCEPT", "", "primary", { act: { type: "accept_trade" } }), btn("DECLINE", "", "ghost", { act: { type: "reject_trade" } })]
      : [btn("CANCEL OFFER", "", "ghost", { act: { type: "reject_trade" } })];
  const keep = fromComputer && !counter
    ? give.properties.map((index) => ({ html: `${icon("lock", { size: 12 })} NOT SELLING ${e(upper(shortName(state.board[index].name)))}`, cmd: "trade-not-selling", arg: String(index) }))
    : [];
  return { banner: null, main: `${main}${buttonsHtml(buttons, keep)}` };
}

function tradeOf(player, target, draft) {
  return { proposerId: player.id, targetId: target.id, offer: { cash: draft.offerCash, properties: draft.offer }, request: { cash: draft.requestCash, properties: draft.request } };
}

export function tradeScreen(state, ui) {
  const player = playerById(state, state?.viewerId);
  if (!player || player.bankrupt) return { banner: null, main: `<div class="tempty">${icon("deal", { size: 28 })}<b>PLAYERS ONLY</b></div>` };
  if (state.phase === "trade" && state.trade && (state.trade.targetId === player.id || state.trade.proposerId === player.id)) return pendingScreen(state, player, ui);
  const { draft, dropped } = cleanDraft(state, ui.trade, player.id);
  const target = playerById(state, draft.targetId);
  const myTurn = isMyTradeTurn(state, player.id);
  const loans = rulesOf(state).loans && myTurn;
  const anything = draft.offer.length + draft.request.length + draft.offerCash + draft.requestCash > 0;
  const chips = partnerChips(state, player, target?.id, "trade");
  if (!chips) return { banner: null, main: `<div class="trade">${topHtml("TRADE")}<div class="tempty">${icon("deal", { size: 28 })}<b>NO ONE TO TRADE WITH</b></div></div>` };
  const give = sideHtml(state, {
    label: "YOU GIVE", owner: player, field: "offer", picked: draft.offer, cash: draft.offerCash, cashMax: player.cash,
    enabled: Boolean(target), fromId: player.id, toId: target?.id, viewerId: player.id, emptyText: "no deeds",
  });
  const get = sideHtml(state, {
    label: "YOU GET", owner: target, field: "request", picked: draft.request, cash: draft.requestCash, cashMax: target?.cash ?? 0,
    enabled: Boolean(target), fromId: target?.id, toId: player.id, viewerId: player.id, emptyText: target ? "no deeds" : "pick a partner",
  });
  const body = `${topHtml("TRADE", { mode: "trade", loans, clear: anything })}
    ${planCard(state, ui.plan, myTurn)}
    <div class="tchips" role="group" aria-label="Trade with">${chips}</div>
    ${dropped ? `<div class="tnote">${icon("warn")} ${dropped} deed${dropped === 1 ? "" : "s"} left the deal</div>` : ""}
    <div class="tcols">${give}${get}</div>
    ${target && draft.offer.length + draft.request.length + draft.offerCash + draft.requestCash ? dealInfoHtml(state, tradeOf(player, target, draft), player.id, { ui, give: draft.offer, get: draft.request, dockToggle: true })
      : target && ui.layout === "desk" ? '<section class="tinfo tinfo--empty"><h3 class="tinfo__title">WHAT THIS DEAL DOES</h3><p class="tinfo__hint">Pick deeds or cash: the effect on both of you shows here.</p></section>' : ""}`;
  const valid = tradeValid(draft);
  const blocker = tradeBlocker(draft);
  const primary = myTurn
    ? btn(target ? `SEND TO ${upper(target.name)}` : "SEND", valid ? "" : blocker, "primary", { cmd: "trade-send", disabled: !valid })
    : btn("SAVE FOR MY TURN", valid ? "" : blocker, "primary", { cmd: "trade-plan", disabled: !valid });
  const chip = target ? quoteChip(draft) : null;
  const trade = target ? { proposerId: player.id, targetId: target.id, offer: { cash: draft.offerCash, properties: draft.offer }, request: { cash: draft.requestCash, properties: draft.request } } : null;
  const verdict = trade && draft.offer.length + draft.request.length ? tradeImpact(state, trade, player.id).verdicts[0] : null;
  // On the phone the verdict line opens WHAT THIS DEAL DOES; on tablet and PC that is always open.
  const fold = !(Boolean(ui.layout) && ui.layout !== "phone");
  const tone = verdict ? (verdict.tone === "good" ? "good" : verdict.tone === "bad" ? "bad" : "info") : "";
  const inner = verdict ? `${icon(verdict.tone === "good" ? "house" : verdict.tone === "bad" ? "warn" : "coin")}<span>${e(verdict.text)}</span>` : "";
  const effect = !verdict ? ""
    : fold ? `<button type="button" class="teffect teffect--${tone} teffect--fold" data-cmd="trade-info" aria-expanded="${Boolean(ui.tradeInfo)}">${inner}<b class="teffect__more">${ui.tradeInfo ? "LESS ▴" : "WHY ▾"}</b></button>`
      : `<div class="teffect teffect--${tone}">${inner}</div>`;
  const dock = `<div class="tdock">${effect}${trade ? balanceHtml(state, trade, player.id) : valueBarHtml(0, 0)}<div class="tdock__row">${chip ? `<button type="button" class="chipbtn tquote${chip.gold ? " chipbtn--gold" : ""}${chip.tone ? ` tquote--${chip.tone}` : ""}"${chip.cmd ? ` data-cmd="${chip.cmd}" data-arg="${e(chip.arg)}"` : ""}${chip.disabled ? " disabled" : ""}>${chip.html}</button>` : ""}${buttonsHtml([primary])}</div></div>`;
  return { banner: null, main: `<div class="trade">${body}</div>${dock}` };
}

// The LOAN segment: partner, I LEND / I BORROW, amount stepper, interest and due chips, security.
export function loanComposeScreen(state, ui) {
  const player = playerById(state, state.viewerId);
  const draft = ui.loanDraft;
  const target = playerById(state, draft.targetId);
  const borrow = draft.direction === "borrow";
  const lenderCash = borrow ? target?.cash ?? 0 : player.cash;
  const repay = loanRepay(draft);
  const rate = loanRate(draft);
  const chip = (cmd, arg, on, label) => `<button type="button" class="lchip${on ? " is-on" : ""}" data-cmd="${cmd}" data-arg="${arg}" aria-pressed="${on}">${label}</button>`;
  const dir = draft.locked
    ? ""
    : `<div class="seg seg--dark" role="group" aria-label="Loan direction"><button type="button" class="seg__btn${borrow ? "" : " is-on"}" data-loan-dir="lend" aria-pressed="${!borrow}">I LEND</button><button type="button" class="seg__btn${borrow ? " is-on" : ""}" data-loan-dir="borrow" aria-pressed="${borrow}">I BORROW</button></div>`;
  const borrowerId = borrow ? player.id : target?.id;
  const pledges = borrowerId ? pledgeable(state, borrowerId) : [];
  const security = draft.security || draft.collateral?.length
    ? `<div class="tgrid">${pledges.map((index) => {
      const on = draft.collateral.includes(index);
      return `<button type="button" class="tcard${on ? " is-on" : ""}" data-loan-pledge="${index}" aria-pressed="${on}">${cardInner(state, index, { on })}</button>`;
    }).join("") || '<span class="tnone">nothing free to pledge</span>'}</div>`
    : `<button type="button" class="tlink" data-cmd="loan-security">+ ADD SECURITY</button>`;
  const enabled = Boolean(target);
  const body = `${topHtml("LOAN", { mode: "loan", loans: !draft.locked })}
    <div class="tchips" role="group" aria-label="Loan with">${partnerChips(state, player, target?.id, "loan")}</div>
    <section class="tside tside--loan${enabled ? "" : " is-off"}" aria-label="Loan terms">
      ${dir}
      <div class="lrow"><span class="lrow__key">AMOUNT</span>${cashStepper("loan-amount", "principal", draft.principal, Math.max(10, lenderCash), enabled, { min: 10 })}</div>
      <div class="lrow"><span class="lrow__key">INTEREST</span><div class="lchips">${LOAN_RATES.map((value) => chip("loan-rate", value, rate === value, `+${Math.round(value * 100)}%`)).join("")}</div></div>
      <div class="lrow"><span class="lrow__key">DUE</span><div class="lchips">${LOAN_DUES.map((value) => chip("loan-due", value, draft.dueTurns === value, `${value} turns`)).join("")}</div></div>
      <div class="lrow lrow--top"><span class="lrow__key">SECURITY</span>${security}</div>
    </section>`;
  const valid = enabled && draft.principal > 0 && draft.principal <= lenderCash && draft.dueTurns >= 1;
  const reason = !target ? "pick a partner" : draft.principal > lenderCash ? `${borrow ? `${target.name} has` : "you have"} ${money(lenderCash)}` : "";
  const summary = `<div class="tsum tsum--loan"><span>${borrow ? "YOU GET" : "YOU LEND"} <b>${money(draft.principal)}</b></span><span class="tsum__arrow">→</span><span>${borrow ? "REPAY" : "BACK"} <b>${money(repay)}</b> · ${draft.dueTurns} turns</span></div>`;
  const send = btn(target ? `SEND TO ${upper(target.name)}` : "SEND", valid ? "" : reason, "primary", { cmd: "loan-send", disabled: !valid });
  return { banner: null, main: `<div class="trade">${body}</div><div class="tdock">${summary}${buttonsHtml([send])}</div>` };
}
