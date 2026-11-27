// The phone's pages from the UX round (B · Tabletop): the timed auction (UX-03), the host's
// PLAYERS controls and the idle bar (UX-02), the debt checklist and the loan terms card (UX-04),
// and the token picker's grid (UX-08). Pure HTML builders: phone.js wires the commands and keeps
// the clocks ticking ([data-auc-clock], [data-tick-since]).

import { QUICK_STEPS, auctionActors, auctionRoster, canBid, clockFraction, clockText, goingOnce, msLeft, secondsLeft } from "../shared/auction-clock.js";
import { debtChecklist } from "../shared/debt-checklist.js";
import { GROUP_COLORS, colourWord, escapeHtml, inputOf, money, playerById, upper } from "../shared/format.js";
import { activeLoans, loanDue, rulesOf } from "../shared/insights.js";
import { hostControls, idleShown, lastMoveAt, minutesText, removalSummary, waitedOnIds } from "../shared/standin.js";
import { NEW_TOKEN_KINDS, TOKEN_COLORS, TOKEN_KINDS, TOKEN_LABELS, playerTokenSvg, tokenSvg } from "../shared/tokens.js";
import { icon } from "./icons.js";
import { btn, buttonsHtml } from "./parts.js";
import { nowOf, slipHtml } from "./stage.js";
import { discHtml } from "./strip.js";

const e = escapeHtml;
const actAttr = (action) => ` data-act='${JSON.stringify(action)}'`;

function viewer(state) {
  return playerById(state, state?.viewerId);
}

// ---------------------------------------------------------------------------------------------
// UX-03 · Timed auction: a clock, the lot and its top bid, one tap per bid, who else is in.

function rosterHtml(state, { skip = null } = {}) {
  const rows = auctionRoster(state).filter((row) => row.player.id !== skip).map((row) => {
    const value = row.passed ? "PASSED" : row.bid !== null ? money(row.bid) : "—";
    return `<li class="troster__row${row.passed ? " is-passed" : ""}${row.leads ? " is-lead" : ""}">${discHtml(state, row.player, { size: 30 })}<span class="troster__name">${e(upper(row.player.name))}</span><b class="troster__bid">${value}</b></li>`;
  }).join("");
  return rows ? `<ul class="troster" aria-label="Bidders">${rows}</ul>` : "";
}

export function timedAuctionScreen(state, ui = {}) {
  const auction = state.auction;
  const viewerId = state.viewerId;
  const player = viewer(state);
  const now = nowOf(state, ui);
  // No server clock (a test, a fixture without `now`): the clock shows full.
  const at = Number.isFinite(now) ? now : Number(auction.endsAt) - Number(auction.clockMs);
  const left = msLeft(auction, at);
  const going = goingOnce(auction, at);
  const leader = playerById(state, auction.bidderId);
  const leads = Boolean(leader) && leader.id === viewerId;
  const inIt = Boolean(player) && (auction.activeIds || []).includes(viewerId);
  const current = Number(auction.currentBid) || 0;
  const top = `<div class="tauc__top"><h2 class="tauc__title">${icon("hammer", { size: 30 })}AUCTION</h2><span class="tauc__pill" role="timer" aria-label="Time left">${icon("clock", { size: 22 })}<b data-auc-text>${clockText(left)}</b></span></div>
    <div class="tauc__bar" aria-hidden="true"><i></i></div>`;
  const lot = `<section class="tauc__lot">${slipHtml(state, auction.spaceIndex, { className: "slip--lot" })}
    <div class="tauc__bid"><small>TOP BID</small><b class="tauc__amount${leads ? " is-mine" : ""}">${money(current)}</b>
      <span class="tauc__who">${leader ? `${discHtml(state, leader, { size: 28 })}${leads ? "YOU" : e(upper(leader.name))}` : "NO BIDS"}</span></div></section>`;
  let body;
  if (inIt && leads) {
    const others = auctionActors(state).map((id) => playerById(state, id)).filter(Boolean).map((other) => upper(other.name));
    body = `<div class="tstamp tstamp--lead" aria-hidden="true">YOU LEAD</div>
      <div class="tauc__going" role="status"><span>${icon("clock", { size: 30 })}GOING ONCE</span><small>SOLD IN <b data-auc-secs>${secondsLeft(left)}</b></small></div>
      <div class="tbids"><button type="button" class="tbid tbid--main" disabled><b>BID ${money(current + QUICK_STEPS[0])}</b></button></div>
      ${others.length ? `<p class="tauc__note">${e(others.join(" · "))} CAN STILL BID</p>` : ""}`;
  } else if (inIt) {
    const can = canBid(state, viewerId);
    const bid = (step, main) => {
      const amount = current + step;
      const off = !can || amount > player.cash;
      return `<button type="button" class="tbid${main ? " tbid--main" : ""}"${actAttr({ type: "bid", amount })}${off ? " disabled" : ""} aria-label="Bid ${money(amount)}"><b>${main ? "BID " : ""}${money(amount)}</b><small>+${money(step)}</small></button>`;
    };
    body = `<div class="tbids">${bid(QUICK_STEPS[0], true)}<div class="tbids__row">${QUICK_STEPS.slice(1).map((step) => bid(step, false)).join("")}</div>
        <button type="button" class="tbid tbid--pass"${actAttr({ type: "pass_auction" })}><b>PASS</b></button></div>
      <div class="tauc__notes"><span>ONE TAP = BID</span><span>CASH ${money(player.cash)}</span></div>
      ${rosterHtml(state, { skip: viewerId })}`;
  } else {
    body = `<div class="tstamp tstamp--passed" aria-hidden="true">PASSED</div>${rosterHtml(state, { skip: viewerId })}`;
  }
  const main = `<section class="tauc${going ? " is-going" : ""}${left <= 0 ? " is-closed" : ""}" data-auc-clock style="--left:${clockFraction(auction, at).toFixed(3)}" aria-label="Auction">${top}${lot}${body}</section>`;
  return { banner: null, main, nav: false };
}

// ---------------------------------------------------------------------------------------------
// UX-02 · Host controls: IDLE m:ss on the board and in MORE, PLAYERS with COMPUTER PLAYS · SKIP
// TURN · REMOVE (POST /api/rooms/{code}/standin | skip | remove).

export function isHostViewer(state) {
  const player = viewer(state);
  return Boolean(player) && state.status === "playing" && (state.viewerIsHost === true || state.hostId === player.id);
}

// The person the game has waited on for 30 s or more (not you), for the host: {player, ms, since}.
export function idleTarget(state, ui = {}) {
  if (!isHostViewer(state)) return null;
  const now = nowOf(state, ui);
  if (now === null) return null;
  for (const id of waitedOnIds(state)) {
    if (id === state.viewerId) continue;
    const ms = idleShown(state, id, now);
    if (ms !== null) return { player: playerById(state, id), ms, since: lastMoveAt(state) };
  }
  return null;
}

const tickAttrs = (since, prefix) => ` data-tick-since="${Number(since)}" data-tick-prefix="${e(prefix)}"`;

export function idleBarHtml(state, ui = {}) {
  const idle = idleTarget(state, ui);
  if (!idle) return "";
  const prefix = `${upper(idle.player.name)} IDLE `;
  return `<div class="idlebar" role="status">${icon("clock", { size: 26 })}<b${tickAttrs(idle.since, prefix)}>${e(prefix)}${minutesText(idle.ms)}</b><button type="button" class="idlebar__btn" data-cmd="host-players" data-arg="${e(idle.player.id)}">PLAYERS</button></div>`;
}

const DEVICE_WORDS = { phone: "phone", classic: "phone", ai: "computer", controller: "controller", keyboard: "keyboard" };

// What the game waits for from this player, in two or three words.
export function doingWords(state, id) {
  switch (state?.phase) {
    case "determining_order": return state.openingOrder?.pendingIds?.includes(id) ? "to roll" : "";
    case "auction": return auctionActors(state).includes(id) ? "auction" : "";
    case "trade": return state.trade?.targetId === id ? "trade offer" : "";
    case "loan_offer": return state.loanOffer?.targetId === id ? "loan offer" : "";
    case "card_confirmation": return state.pendingCard?.playerId === id ? "card" : "";
    case "debt": return state.debt?.payerId === id ? "in debt" : "";
    case "awaiting_roll": return state.currentPlayerId === id ? "to roll" : "";
    case "awaiting_purchase": return state.currentPlayerId === id ? "to buy" : "";
    case "turn_complete": return state.currentPlayerId === id ? "to end turn" : "";
    default: return "";
  }
}

function hostTag(state, player, now) {
  if (player.bankrupt) return ["out", player.left ? "REMOVED" : "OUT"];
  if (inputOf(player) === "ai") return ["ai", "COMPUTER"];
  if (player.autopilot) return ["bot", "COMPUTER PLAYS"];
  const ms = now === null ? null : idleShown(state, player.id, now);
  if (ms !== null) return ["idle", `IDLE ${minutesText(ms)}`, `${tickAttrs(lastMoveAt(state), "IDLE ")}`];
  if (player.skipped) return ["skip", "SKIPPING"];
  if (player.online === false) return ["away", "AWAY"];
  return ["on", "ONLINE"];
}

export function hostPlayersHtml(state, ui = {}) {
  if (!isHostViewer(state)) return "";
  const now = nowOf(state, ui);
  // You first, then the others in seat order (as on the board).
  const players = [...(state.players || [])].sort((a, b) => Number(b.id === state.viewerId) - Number(a.id === state.viewerId));
  const rows = players.map((player) => {
    const self = player.id === state.viewerId;
    const doing = self ? "host" : doingWords(state, player.id);
    const sub = [DEVICE_WORDS[inputOf(player)] || "phone", doing].filter(Boolean).join(" · ");
    const [tone, tag, tick = ""] = hostTag(state, player, now);
    const inner = `${discHtml(state, player, { size: 46 })}<span class="hrow__who"><b>${self ? "YOU" : e(upper(player.name))}</b><small>${e(sub)}</small></span><span class="htag htag--${tone}"${tick}>${e(tag)}</span>`;
    const controls = self || player.bankrupt ? { standin: false, skip: false, remove: false } : hostControls(state, player.id);
    const any = controls.standin || controls.skip || controls.remove;
    const open = any && ui.hostOpen === player.id;
    const head = any
      ? `<button type="button" class="hrow__head" data-cmd="host-open" data-arg="${e(player.id)}" aria-expanded="${open}">${inner}</button>`
      : `<div class="hrow__head">${inner}</div>`;
    const acts = open
      ? `<div class="hrow__acts">${controls.standin ? `<button type="button" class="btn btn--primary hbtn hbtn--bot" data-cmd="host-standin" data-arg="${e(player.id)}"><span class="btn__label">${icon("bot", { size: 24 })} COMPUTER PLAYS</span></button>` : ""}
        <div class="hrow__pair">${controls.skip ? `<button type="button" class="btn btn--ghost hbtn" data-cmd="host-skip" data-arg="${e(player.id)}"><span class="btn__label">SKIP TURN</span></button>` : ""}${controls.remove ? `<button type="button" class="btn btn--danger hbtn" data-cmd="host-remove-ask" data-arg="${e(player.id)}"><span class="btn__label">REMOVE</span></button>` : ""}</div></div>`
      : "";
    return `<li class="hrow${open ? " is-open" : ""}${player.bankrupt ? " is-out" : ""}">${head}${acts}</li>`;
  }).join("");
  return `<h2 class="hhead">${icon("crown", { size: 24 })}PLAYERS</h2><ul class="hrows">${rows}</ul>`;
}

// REMOVE SAM?: what goes back to the bank, and the seat closes.
export function removeSheetHtml(state, playerId, ui = {}) {
  const player = playerById(state, playerId);
  if (!player) return "";
  const summary = removalSummary(state, playerId) || { deeds: 0, houses: 0, cash: 0 };
  const now = nowOf(state, ui);
  const idle = now === null ? null : idleShown(state, playerId, now);
  const sub = [idle !== null ? `IDLE ${minutesText(idle)}` : "", upper(doingWords(state, playerId))].filter(Boolean).join(" · ");
  const bank = `<b>${icon("bank", { size: 22 })}BANK</b>`;
  return `<div class="sheet__body rsheet">
    <div class="rsheet__head"><span class="rsheet__token">${playerTokenSvg(state, player, { size: 76 })}</span><div><h2 class="sheet__title">REMOVE ${e(upper(player.name))}?</h2>${sub ? `<p class="rsheet__sub">${e(sub)}</p>` : ""}</div></div>
    <ul class="rsheet__rows">
      <li><span>DEEDS ×${summary.deeds}</span>${bank}</li>
      <li><span>HOUSES ×${summary.houses}</span>${bank}</li>
      <li><span>CASH ${money(summary.cash)}</span>${bank}</li>
      <li><span>SEAT</span><b class="is-red">CLOSED</b></li>
    </ul>
    <div class="actions"><button type="button" class="btn btn--danger" data-sheet-cmd="confirm"><span class="btn__label">REMOVE ${e(upper(player.name))}</span></button><button type="button" class="btn btn--ghost" data-sheet-cmd="close"><span class="btn__label">KEEP</span></button></div>
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// UX-04 · Debt: OWE / CASH / TO FIND, tick what to mortgage or sell, PAY once it's covered.

// Which debt a set of ticks belongs to: a new debt starts from the suggested plan again.
export function debtKey(state) {
  const debt = state?.debt;
  return debt ? `${debt.payerId}:${debt.creditorId || "bank"}:${debt.amount}` : "";
}

export function debtPicked(state, ui = {}) {
  return ui.debtPicks && ui.debtPicks.key === debtKey(state) ? ui.debtPicks.picked : null;
}

export function debtListFor(state, ui = {}) {
  return debtChecklist(state, state.viewerId, debtPicked(state, ui));
}

// The rows the phone shows (as on the TV, the board's four): the ticked ones always, then the deeds
// that raise the most, then house sales, then deeds waiting on houses. Every deed is still in MY DEEDS.
export const DEBT_ROWS = 4;
export function debtRowsShown(list) {
  const all = [...list.mortgage, ...list.sell];
  const rank = (row) => (row.ticked ? 0 : row.blocked ? 3 : row.kind === "mortgage" ? 1 : 2);
  const ordered = all.map((row, index) => ({ row, index })).sort((a, b) => rank(a.row) - rank(b.row) || b.row.amount - a.row.amount || a.index - b.index).map((entry) => entry.row);
  const ticked = ordered.filter((row) => row.ticked);
  const shown = new Set([...ticked, ...ordered.filter((row) => !row.ticked)].slice(0, Math.max(DEBT_ROWS, ticked.length)));
  return all.filter((row) => shown.has(row));
}
const shortDeed = (name) => String(name || "").replace(/\bRailroad\b/, "RR").replace(/\bStreet\b/, "St").replace(/\bAvenue\b/, "Ave").replace(/\bRoad\b/, "Rd").replace(/\bCompany\b/, "Co");

function debtRowHtml(row, sub) {
  const colour = GROUP_COLORS[row.group] || "#4A4238";
  const attrs = row.blocked ? " disabled" : ` data-cmd="debt-tick" data-arg="${e(row.key)}"`;
  return `<li><button type="button" class="drow${row.ticked ? " is-on" : ""}${row.blocked ? " is-blocked" : ""}"${attrs} aria-pressed="${row.ticked}">
    <span class="drow__box" aria-hidden="true">${row.ticked ? icon("check", { size: 18 }) : row.blocked ? icon("lock", { size: 14 }) : ""}</span><i class="drow__bar" style="background:${colour}" aria-hidden="true"></i>
    <span class="drow__name"><b>${e(upper(row.name))}</b>${sub ? `<small>${sub}</small>` : ""}</span><b class="drow__amt">+${money(row.amount)}</b></button></li>`;
}

export function debtChecklistScreen(state, ui = {}) {
  const player = viewer(state);
  const debt = state.debt;
  const list = debtListFor(state, ui);
  const creditor = playerById(state, debt.creditorId);
  const reason = String(debt.reason || "").replace(/\s*rent$/i, "").trim();
  const title = `YOU OWE ${creditor ? upper(creditor.name) : "THE BANK"}${reason ? ` · ${upper(reason)}` : ""}`;
  const pct = (value) => (list.owe > 0 ? Math.max(0, Math.min(100, (value / list.owe) * 100)) : 0);
  const cashPct = pct(Math.min(list.cash, list.owe));
  const raisedPct = Math.min(100 - cashPct, pct(list.raised));
  const card = `<section class="dcard" aria-label="Debt">
    <div class="dcard__nums"><div><small>OWE</small><b class="is-red">${money(list.owe)}</b></div><div><small>CASH</small><b>${money(list.cash)}</b></div><div><small>TO FIND</small><b class="${list.toFind ? "is-red" : "is-green"}">${money(list.toFind)}</b></div></div>
    <div class="dcard__bar" aria-hidden="true"><i class="is-cash" style="width:${cashPct.toFixed(1)}%"></i><i class="is-raised" style="width:${raisedPct.toFixed(1)}%"></i></div></section>`;
  // Labels, not sentences: short names; "$rent → $0" and "houses → after" only while 3 rows or fewer
  // fit (the one-glance word budget); a deed waiting on houses shows its lock.
  const shown = debtRowsShown(list);
  const subs = shown.length <= 3;
  const mortgageRows = shown.filter((row) => row.kind === "mortgage");
  const mortgages = mortgageRows.map((row) => debtRowHtml({ ...row, name: shortDeed(row.name) }, !subs ? "" : row.blocked ? "houses first" : `${money(row.rent)} → ${money(0)}`)).join("");
  const sells = shown.filter((row) => row.kind === "sell").map((row) => debtRowHtml({ ...row, name: row.name.endsWith(" houses") ? colourWord(row.group) : shortDeed(row.name) }, subs ? `${icon("house", { size: 14 })}${row.houses} → ${row.after}` : "")).join("");
  const rows = `<section class="dlist">
    ${mortgageRows.length ? `<h3 class="dlist__head">MORTGAGE</h3><ul>${mortgages}</ul>` : ""}
    ${sells ? `<h3 class="dlist__head">SELL HOUSES</h3><ul>${sells}</ul>` : list.sell.length ? "" : '<h3 class="dlist__head">SELL HOUSES</h3><p class="dlist__none">NONE BUILT</p>'}</section>`;
  const canPay = player.cash >= list.owe;
  const pay = canPay
    ? btn(`PAY ${money(list.owe)}`, "", "primary", { act: { type: "pay_debt" } })
    : list.covered
      ? btn(`PAY ${money(list.owe)}`, "", "primary", { cmd: "debt-pay" })
      : btn(`PAY ${money(list.owe)}`, `FIND ${money(list.toFind)}`, "primary", { disabled: true });
  const more = [
    ...(!canPay && rulesOf(state).loans ? [btn("LOAN", "", "ghost", { cmd: "loan-ask", aria: "ASK FOR A LOAN" })] : []),
    btn("GO BANKRUPT", "", "danger", { cmd: "bankrupt" }),
  ];
  const buttons = buttonsHtml([pay]).replace('class="actions"', 'class="actions actions--debt"');
  const pair = buttonsHtml(more).replace('class="actions"', `class="actions actions--pair${more.length === 1 ? " is-one" : ""}"`);
  return { banner: null, main: `<h2 class="ttitle ttitle--debt">${e(title)}</h2>${card}${rows}${buttons}${pair}`, nav: false };
}

// ---------------------------------------------------------------------------------------------
// UX-04 · MY LOANS: the terms of each loan you owe, on one card, and REPAY.

export function loanCardsHtml(state, player) {
  const loans = activeLoans(state, player.id).filter((loan) => loan.borrowerId === player.id);
  if (!loans.length) return "";
  const myTurn = state.currentPlayerId === player.id && ["awaiting_roll", "turn_complete"].includes(state.phase);
  const cards = loans.map((loan) => {
    const lender = playerById(state, loan.lenderId);
    const name = upper(lender?.name || "");
    const due = loanDue(state, loan);
    const counts = state.turnCounts;
    const left = counts && typeof counts[player.id] === "number" && typeof loan.dueTurn === "number" ? loan.dueTurn - counts[player.id] : null;
    const pips = left !== null && left >= 1 && left <= 6 ? `<div class="lcard__pips"><span>TURNS LEFT</span>${Array.from({ length: left }, () => "<i></i>").join("")}</div>` : "";
    const security = (loan.collateral || []).filter((index) => state.board[index]);
    const securityHtml = security.length
      ? `<div class="lcard__sec"><span class="lcard__key">SECURITY</span><div class="lcard__slips">${security.map((index) => slipHtml(state, index, { foot: `${discHtml(state, player, { size: 22 })}` })).join("")}</div></div>
        <p class="lcard__warn">${icon("lock", { size: 20 })}MISSED → ${e(security.map((index) => upper(state.board[index].name)).join(", "))} ${security.length === 1 ? "GOES" : "GO"} TO ${e(name)}</p>`
      : "";
    const afford = player.cash >= loan.repay;
    const label = `You owe ${lender?.name || ""} ${money(loan.repay)}`;
    return `<section class="lcard" aria-label="${e(label)}">
      <div class="lcard__head">${lender ? discHtml(state, lender, { size: 46 }) : ""}<b class="lcard__from">FROM ${e(name)}</b>${due ? `<span class="lcard__due">${e(upper(due))}</span>` : ""}</div>
      <dl class="lcard__terms"><div><dt>Borrowed</dt><dd>${money(loan.principal)}</dd></div><div><dt>Interest</dt><dd>+${money(loan.repay - loan.principal)}</dd></div><div class="is-total"><dt>REPAY</dt><dd>${money(loan.repay)}</dd></div></dl>
      ${pips}${securityHtml}
    </section>
    <div class="lafter"><span>AFTER REPAY</span><b>${money(player.cash)} → ${money(player.cash - loan.repay)}</b></div>
    ${buttonsHtml([btn(`REPAY ${money(loan.repay)}`, myTurn ? (afford ? "" : `need ${money(loan.repay - player.cash)} more`) : "", "primary", { act: { type: "repay_loan", loanId: loan.id }, disabled: !myTurn || !afford })])}`;
  }).join("");
  return `<h2 class="ttitle">MY LOANS</h2>${cards}`;
}

// ---------------------------------------------------------------------------------------------
// UX-08 · Tokens: 4 × 2, taken ones greyed with the owner's name, n OF 8 FREE, the new one marked.

export function tokenFieldHtml({ pickedKind = null, takenBy = new Map(), palette = null } = {}) {
  const free = TOKEN_KINDS.filter((kind) => !takenBy.has(kind)).length;
  const tiles = TOKEN_KINDS.map((kind) => {
    const owner = takenBy.get(kind);
    const picked = !owner && pickedKind === kind;
    const color = palette?.[kind] || TOKEN_COLORS[kind];
    const fresh = NEW_TOKEN_KINDS.includes(kind) && !owner;
    const label = owner ? `${TOKEN_LABELS[kind]}, taken by ${owner}` : `${TOKEN_LABELS[kind]}${fresh ? ", new" : ""}`;
    return `<button type="button" class="token-pick${picked ? " is-picked" : ""}${owner ? " is-taken" : ""}" data-token="${kind}" aria-pressed="${picked}" aria-label="${e(label)}" style="--pick:${e(color)}" ${owner ? "disabled" : ""}>
    ${fresh ? '<span class="token-pick__new" aria-hidden="true">NEW</span>' : ""}${tokenSvg(kind, color, { size: 56 })}<span>${e(upper(owner || TOKEN_LABELS[kind]))}</span></button>`;
  }).join("");
  return `<div class="field field--tokens"><div class="field__row"><span class="field__label">Token</span><span class="field__count">${free} OF ${TOKEN_KINDS.length} FREE</span></div><div class="token-grid token-grid--4">${tiles}</div></div>`;
}

