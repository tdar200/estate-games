// The BOARD tab: what the centre of the board ("stage") shows, one short status line under the
// board ("YOUR TURN", "MAYA'S TURN"), the squares to shade or mark, and the decision buttons — for
// every phone role (your turn, someone else's turn, remote players, spectators). What just happened
// plays on the board itself (hops, landing glow, money chips); there is no history list. Pure:
// returns HTML strings and plain data.

import { diceHtml } from "../shared/dice.js";
import { GROUP_COLORS, GROUP_INK, colourWord, escapeHtml, inputOf, money, ordinal, parseLog, playerById, upper } from "../shared/format.js";
import {
  BOARD_CONSTANTS, CHANCE_EFFECTS, activeLoans, bidCap, buildOptions, cardPreview, fullSets, groupIndexes, loanDue, nextRollHeat, nextRollRisk, openingRows, openingTie, ownedBy, ownsGroup,
  propState, purchaseInsight, rentFor, rentLabel, rentMultiple, rulesOf,
} from "../shared/insights.js";
import { clockFraction, clockText, goingOnce, isTimed, msLeft } from "../shared/auction-clock.js";
import { awayCash, awayLines, minutesText, showAwayList, standInMs } from "../shared/standin.js";
import { dotHtml, shortName, turnCues } from "./cues.js";
import { icon } from "./icons.js";
import { btn } from "./parts.js";
import { plannedTrade } from "./trade.js";

const e = escapeHtml;
const { RAILROADS, UTILITIES } = BOARD_CONSTANTS;

function me(state) {
  return playerById(state, state?.viewerId);
}

// The server's clock for a pure render (Unix ms), or null when there is none: the socket's synced
// clock live (ui.serverNow, set by phone.js), else the fixture's frozen `now` in a demo. Without
// one, nothing that counts (IDLE m:ss, the auction clock) is drawn from a guess.
export function nowOf(state, ui = {}) {
  const live = ui?.serverNow;
  if (live !== null && live !== undefined && Number.isFinite(Number(live))) return Number(live);
  const now = Number(state?.now);
  return Number.isFinite(now) && now > 0 ? now : null;
}

// ---------------------------------------------------------------------------------------------
// Stage pieces

export function eventHtml({ who = "", big = "", sub = "", tone = "", dot = "" } = {}) {
  return `<div class="pev${tone ? ` pev--${tone}` : ""}">
    ${who ? `<div class="pev__who">${dot}${e(who)}</div>` : ""}
    ${big ? `<div class="pev__big">${e(big)}</div>` : ""}
    ${sub ? `<div class="pev__sub">${sub}</div>` : ""}
  </div>`;
}

const BLANK_DIE = '<div class="die die--st die--blank" aria-hidden="true"><b>?</b></div>';

export function diceStageHtml(dice, { doubles = false, count = 0, blank = false, label = "" } = {}) {
  if (blank || !Array.isArray(dice)) return `<div class="pdice" data-stage-dice aria-hidden="true"><div class="dice-pair">${BLANK_DIE}${BLANK_DIE}</div></div>`;
  const total = dice[0] + dice[1];
  const pips = doubles ? `<span class="pdice__count" aria-label="Doubles ${count} of 3">${[1, 2, 3].map((n) => `<i class="${n <= count ? "is-on" : ""}"></i>`).join("")}</span>` : "";
  return `<div class="pdice" data-stage-dice>${diceHtml(dice, { className: "die--st", rotations: [-8, 7] })}<span class="pdice__total" aria-label="Total ${total}">${total}</span></div>
    ${doubles ? `<div class="pbadge pbadge--gold">DOUBLES ${pips}</div>` : ""}${label ? `<div class="pdice__label">${e(label)}</div>` : ""}`;
}

function groupSquares(state, space) {
  if (space.type === "property") return groupIndexes(state, space.group);
  if (space.type === "railroad") return RAILROADS;
  if (space.type === "utility") return UTILITIES;
  return [];
}

// Set pips: one box per square of the set, with the owner's chip in it; the square in play outlined.
export function setPipsHtml(state, index) {
  const space = state.board[index];
  const squares = groupSquares(state, space);
  if (!squares.length) return "";
  const colour = space.type === "property" ? GROUP_COLORS[space.group] : "#5B6470";
  return `<div class="pips" aria-hidden="true">${squares.map((square) => {
    const owner = playerById(state, propState(state, square)?.ownerId);
    return `<span class="pip${square === index ? " is-this" : ""}" style="background:${colour}">${owner ? dotHtml(state, owner.id, { size: 12 }) : square === index ? "<b>?</b>" : ""}</span>`;
  }).join("")}</div>`;
}

function rentRows(state, index) {
  const space = state.board[index];
  const prop = propState(state, index);
  if (space.type === "property") {
    const now = prop?.ownerId ? rentLabel(state, index) : money(space.rents[0]);
    return `<span>Rent</span><b>${e(now)}</b>`;
  }
  if (space.type === "railroad") return `<span>Rent</span><b>${prop?.ownerId ? e(rentLabel(state, index)) : "$25–$200"}</b>`;
  if (space.type === "utility") return "<span>Rent</span><b>4× / 10× dice</b>";
  return "";
}

// Which district photo a deed card shows (tabletop.css): the colour set, a station, or the
// Power Station / Reservoir.
export function photoKey(space) {
  if (!space) return "";
  if (space.type === "property") return space.group;
  if (space.type === "railroad") return "station";
  if (space.type === "utility") return /reservoir|water/i.test(space.name) ? "water" : "power";
  return "";
}

export function deedMiniHtml(state, index, { eyebrow = "", badge = null, sold = false, rows = true } = {}) {
  const space = state.board[index];
  if (!space) return "";
  const head = space.type === "property" ? GROUP_COLORS[space.group] : "#FBFAF4";
  const ink = space.type === "property" ? GROUP_INK[space.group] : "#15191E";
  const glyph = space.type === "railroad" ? icon("rail", { size: 14 }) : space.type === "utility" ? icon("bolt", { size: 14 }) : "";
  return `<div class="pdeed"${sold ? " data-sold" : ""} data-sheet="${index}" data-photo="${photoKey(space)}" role="button" tabindex="0" aria-label="${e(space.name)}: details"><span class="pinfo" aria-hidden="true"></span>
    <div class="pdeed__head" style="background:${head};color:${ink}">${eyebrow ? `<small>${e(eyebrow)}</small>` : ""}${glyph}${e(upper(space.name))}</div>
    ${setPipsHtml(state, index)}
    ${rows ? `<div class="pdeed__rows">${rentRows(state, index)}</div>` : ""}
  </div>${badge ? `<div class="pbadge pbadge--${badge.tone || "ink"}">${e(badge.text)}</div>` : ""}`;
}

export function cardMiniHtml(text, { deck = "chance" } = {}) {
  return `<div class="pcard pcard--${deck === "chest" ? "chest" : "chance"}" data-card><div class="pcard__head">${deck === "chest" ? "POST" : "HEADLINES"}</div><div class="pcard__text">${e(text)}</div></div>`;
}

// Short badge for a square someone may buy: "COMPLETES SET", "BLOCKS MAYA", "2 OF 3"…
export function buyBadge(state, playerId, index) {
  const space = state.board[index];
  if (space.type === "railroad") {
    const mine = RAILROADS.filter((square) => propState(state, square)?.ownerId === playerId).length;
    return { text: `RAILROAD ${mine + 1} OF 4`, tone: mine ? "gold" : "ink" };
  }
  if (space.type === "utility") {
    const mine = UTILITIES.filter((square) => propState(state, square)?.ownerId === playerId).length;
    return { text: mine ? "BOTH UTILITIES" : "UTILITY 1 OF 2", tone: mine ? "gold" : "ink" };
  }
  if (space.type !== "property") return null;
  const insight = purchaseInsight(state, playerId, index);
  const strong = insight?.strong || "";
  if (/^Completes/.test(strong)) return { text: "COMPLETES YOUR SET", tone: "gold" };
  if (/^Blocks/.test(strong)) {
    const rival = groupIndexes(state, space.group).map((square) => propState(state, square)?.ownerId).find((id) => id && id !== playerId);
    return { text: `BLOCKS ${upper(playerById(state, rival)?.name || "A SET")}`, tone: "red" };
  }
  const of = strong.match(/^(\d+) of (\d+)/);
  if (of) return { text: `${of[1]} OF ${of[2]} ${upper(colourWord(space.group))}`, tone: "gold" };
  return null;
}

// ---------------------------------------------------------------------------------------------
// Board data

// Red wash on the squares 2–12 ahead that would cost the player money (and Busted); the
// level (0–1) follows the dice odds, darkest at 7.
export function heatSquares(state, playerId) {
  return nextRollHeat(state, playerId).squares.map((entry) => ({ index: entry.index, level: Math.round(Math.max(0.25, entry.level) * 100) / 100 }));
}

// One compact risk badge for your own turn, only when the worst case is big.
export function riskBadge(state, playerId) {
  const risk = nextRollRisk(state, playerId);
  const top = risk.threats[0];
  if (!top) return null;
  const big = top.max >= 150 || top.max >= risk.cash * 0.25;
  if (!big) return null;
  return { amount: top.max, label: top.label, short: top.max > risk.cash };
}

// The status line under the board: one short phrase, with the dot of whoever it is about.
// tone: "gold" when it's on you, "" otherwise.
export function statusHtml(state, { text, playerId = null, tone = "", glyph = "" }) {
  if (!text) return "";
  const dot = playerId ? dotHtml(state, playerId, { size: 16 }) : glyph ? icon(glyph, { size: 22 }) : "";
  // data-key: an unchanged line stays put across updates; a new line lifts in.
  return `<div class="pstatus${tone ? ` pstatus--${tone}` : ""}" data-key="${e(`${tone}|${playerId || ""}|${text}`)}">${dot}<span>${e(text)}</span></div>`;
}

// Where a pending Headlines card sends the player (null when it doesn't move them).
export function cardTarget(state, playerId, cardId) {
  const effect = CHANCE_EFFECTS[cardId];
  const player = playerById(state, playerId);
  if (!effect || !player) return null;
  const pos = player.position;
  if (effect.type === "moveTo") return effect.target;
  if (effect.type === "nearestRail") return RAILROADS.find((index) => index > pos) ?? RAILROADS[0];
  if (effect.type === "nearestUtility") return UTILITIES.find((index) => index > pos) ?? UTILITIES[0];
  if (effect.type === "move") return (pos + effect.steps + 40) % 40;
  if (effect.type === "jail") return 10;
  return null;
}

function pathBetween(from, to, backwards = false) {
  const path = [];
  if (backwards) {
    for (let index = from; index !== to; index = (index + 39) % 40) path.push(index);
  } else {
    for (let index = from; index !== to; index = (index + 1) % 40) path.push(index);
  }
  path.push(to);
  return path;
}

// ---------------------------------------------------------------------------------------------
// Buttons shared by several screens

function buildChip(state, player) {
  const build = buildOptions(state, player.id).filter((option) => option.affordable && option.bankOk).sort((a, b) => b.delta - a.delta)[0];
  if (!build) return null;
  return { html: `${icon("house")} ${build.hotel ? "HOTEL" : "BUILD"} ${money(build.cost)}`, label: "BUILD", act: { type: "build", spaceIndex: build.index } };
}

function plannedChip(state, ui, { canSend }) {
  const plan = plannedTrade(state, ui.plan);
  if (!plan) return null;
  if (!canSend) return { html: `${icon("deal")} PLAN · ${e(upper(plan.target.name))}`, cmd: "tab", arg: "trade" };
  return { html: `${icon("deal")} SEND TRADE · ${e(upper(plan.target.name))}`, cmd: "trade-send-plan", gold: true };
}

// RAISE CASH only when short: cash under the worst square of your next roll.
function shortOfCash(state, player) {
  const risk = nextRollRisk(state, player.id);
  return player.cash < (risk.worst?.cost || 0);
}

function endTurnButtons(state, player) {
  const buttons = [btn("END TURN", "", "primary", { act: { type: "end_turn" } })];
  if (shortOfCash(state, player)) buttons.push(btn("RAISE CASH", "", "ghost", { cmd: "tab", arg: "property" }));
  return buttons;
}

const yourTurn = (state) => ({ text: "YOUR TURN", playerId: state.viewerId, tone: "gold" });

// ---------------------------------------------------------------------------------------------
// Screens

function rollBoard(state, ui) {
  const player = me(state);
  const doubles = state.turn?.doublesCount > 0 && Array.isArray(state.turn?.dice);
  // Nothing in the centre before you roll: the ROLL button says it.
  const stage = doubles ? diceStageHtml(state.turn.dice, { doubles: true, count: state.turn.doublesCount }) : "";
  const chips = [buildChip(state, player), plannedChip(state, ui, { canSend: true })].filter(Boolean);
  return {
    stage,
    heat: heatSquares(state, player.id),
    status: doubles ? { text: "DOUBLES · ROLL AGAIN", playerId: player.id, tone: "gold" } : yourTurn(state),
    // The TV's words (UX-01 key map): A ROLL / ROLL AGAIN, X BUILD (a chip here), Y the TRADE tab.
    buttons: [btn(doubles ? "ROLL AGAIN" : "ROLL", "", "primary", { act: { type: "roll" }, className: "btn--roll" })],
    chips,
    extra: glanceHtml(state),
  };
}

function buyBoard(state) {
  const player = me(state);
  const index = state.pendingPurchase;
  const space = state.board[index];
  const can = player.cash >= space.price;
  const buttons = [
    btn(`BUY ${money(space.price)}`, can ? `keep ${money(player.cash - space.price)}` : `need ${money(space.price - player.cash)} more`, "primary", { act: { type: "buy" }, disabled: !can }),
    btn(rulesOf(state).auctions ? "AUCTION" : "PASS", "", "ghost", { act: { type: "decline" } }),
  ];
  if (!can) buttons.push(btn("RAISE CASH", "", "ghost", { cmd: "tab", arg: "property" }));
  return {
    stage: deedMiniHtml(state, index, { eyebrow: "FOR SALE", badge: buyBadge(state, player.id, index) }),
    marks: { target: index },
    status: yourTurn(state),
    buttons,
  };
}

// The clock of a timed auction (UX-03): a pill with m:ss and a bar that drains, both kept up to
// date between states by phone.js (tickAuctionClock on [data-auc-clock]). Without a server clock
// (now null) it shows the clock full.
export function auctionClockHtml(auction, now, { className = "" } = {}) {
  if (!isTimed(auction)) return "";
  const at = Number.isFinite(now) ? now : Number(auction.endsAt) - Number(auction.clockMs);
  const left = msLeft(auction, at);
  return `<div class="aclock${className ? ` ${className}` : ""}${goingOnce(auction, at) ? " is-going" : ""}${left <= 0 ? " is-closed" : ""}" data-auc-clock style="--left:${clockFraction(auction, at).toFixed(3)}" role="timer" aria-label="Time left">
    <span class="aclock__pill">${icon("clock", { size: 20 })}<b data-auc-text>${clockText(left)}</b></span><span class="aclock__bar" aria-hidden="true"><i></i></span></div>`;
}

function auctionStage(state, viewerId, now = null) {
  const auction = state.auction;
  const space = state.board[auction.spaceIndex];
  const leader = playerById(state, auction.bidderId);
  const head = space.type === "property" ? GROUP_COLORS[space.group] : "#13294B";
  const ink = space.type === "property" ? GROUP_INK[space.group] : "#FFFFFF";
  return `<div class="pauc" data-sheet="${auction.spaceIndex}" data-photo="${photoKey(space)}" role="button" tabindex="0" aria-label="${e(space.name)}: details"><span class="pinfo" aria-hidden="true"></span>
    <div class="pauc__head" style="background:${head};color:${ink}"><small>AUCTION · LIST ${money(space.price)}</small>${e(upper(space.name))}</div>
    ${setPipsHtml(state, auction.spaceIndex)}
    <div class="pauc__bid">${money(auction.currentBid)}</div>
    <div class="pauc__lead">${leader ? `${dotHtml(state, leader.id, { size: 14 })}${leader.id === viewerId ? "You lead" : `${e(leader.name)} leads`}` : "No bids yet"}</div>
  </div>${auctionClockHtml(auction, now, { className: "aclock--stage" })}`;
}

export function auctionStatus(state, viewerId) {
  const auction = state.auction;
  // Timed (UX-03): nobody has the turn; the line says who leads.
  if (isTimed(auction)) {
    const leader = playerById(state, auction.bidderId);
    if (viewerId && playerById(state, viewerId) && !auction.activeIds.includes(viewerId)) return { text: "AUCTION · YOU PASSED" };
    if (leader && leader.id === viewerId) return { text: "AUCTION · YOU LEAD", playerId: viewerId, tone: "gold" };
    if (leader) return { text: `AUCTION · ${upper(leader.name)} LEADS`, playerId: leader.id };
    return { text: "AUCTION · NO BIDS" };
  }
  const bidder = playerById(state, auction.currentPlayerId);
  if (viewerId && !auction.activeIds.includes(viewerId)) return { text: "AUCTION · YOU PASSED" };
  if (bidder && bidder.id === viewerId) return { text: "AUCTION · YOUR BID", playerId: viewerId, tone: "gold" };
  if (bidder) return { text: `AUCTION · ${upper(bidder.name)}'S BID`, playerId: bidder.id };
  return { text: "AUCTION" };
}

function auctionBoard(state) {
  const player = me(state);
  const auction = state.auction;
  const space = state.board[auction.spaceIndex];
  const mine = auction.currentPlayerId === player.id;
  const out = !auction.activeIds.includes(player.id);
  const steps = [{ step: 10 }, { step: 50 }];
  if (auction.currentBid < space.price) steps.push({ to: space.price, label: "LIST" });
  else steps.push({ step: 100 });
  const bids = steps.map(({ step, to, label }) => {
    const amount = to ?? auction.currentBid + step;
    const disabled = !mine || amount > player.cash || amount <= auction.currentBid;
    return `<button type="button" class="bid" data-act='${JSON.stringify({ type: "bid", amount })}' ${disabled ? "disabled" : ""}><span class="bid__step">${label || `+$${step}`}</span><span class="bid__to">${money(amount)}</span></button>`;
  }).join("");
  const cap = bidCap(state, player.id);
  const max = Math.max(player.cash, 1);
  const safe = Math.max(0, Math.min(100, (cap / max) * 100));
  const now = Math.max(0, Math.min(100, (auction.currentBid / max) * 100));
  const bar = out ? "" : `<div class="bidbar" aria-label="Safe to bid up to ${money(cap)}"><span class="bidbar__safe" style="width:${safe.toFixed(1)}%"></span><span class="bidbar__now" style="left:${now.toFixed(1)}%"></span></div>`;
  return {
    stage: auctionStage(state, player.id),
    marks: { target: auction.spaceIndex },
    status: auctionStatus(state, player.id),
    extra: out ? "" : `<div class="bids">${bids}</div>${bar}`,
    buttons: out ? [] : [btn("PASS", "", "danger", { act: { type: "pass_auction" }, disabled: !mine })],
  };
}

// ---- Rent ×2 and ×10 (UX-05): a "nearest" card set the rent --------------------------------------

// A small deed slip: the name on its colour band, an optional foot (owner, rent). Tapping it opens
// the deed (data-sheet), so it never holds a button.
export function slipHtml(state, index, { foot = "", className = "" } = {}) {
  const space = state.board[index];
  if (!space) return "";
  const head = space.type === "property" ? GROUP_COLORS[space.group] : "#4A4238";
  const ink = space.type === "property" ? GROUP_INK[space.group] : "#FFFFFF";
  return `<div class="slip${className ? ` ${className}` : ""}" data-sheet="${index}" role="button" tabindex="0" aria-label="${e(space.name)}: details"><span class="slip__head" style="background:${head};color:${ink}">${e(upper(space.name.replace(/ Railroad$/, "")))}</span>${foot ? `<span class="slip__foot">${foot}</span>` : ""}</div>`;
}

// The card that set the rent ("Engineering works", "Heatwave"): the words before its colon.
function rentCardTitle(state) {
  const text = String(state?.lastCard?.text || "");
  const at = text.indexOf(":");
  return at > 0 ? text.slice(0, at).trim() : "";
}

function rentWho(landing, name) {
  const multiple = rentMultiple(landing);
  return multiple ? `RENT ×${multiple.multiplier} · ${name}` : `RENT · ${name}`;
}

// The sum behind a multiplied rent, under the status line: the deed, the stamp and the card, then
// $25 × 2 = $50 (a station) or the new roll and 7 × 10 = $70 (a utility). "" for a plain rent.
export function rentMultipleHtml(state, landing) {
  const multiple = rentMultiple(landing);
  if (!multiple || !landing) return "";
  const space = state.board[landing.spaceIndex];
  const owner = playerById(state, landing.recipientId);
  const title = rentCardTitle(state);
  const sum = (left) => `<div class="rentx__sum" aria-label="${e(`${left} times ${multiple.multiplier} is ${money(multiple.amount)}`)}">${left}<i>×</i><b class="rentx__mult">${multiple.multiplier}</b><i>=</i><span class="rentx__total">${money(multiple.amount)}</span></div>`;
  const eyebrow = `<h2 class="tteyebrow tteyebrow--red">RENT ×${multiple.multiplier}</h2>`;
  if (multiple.dice) {
    const total = multiple.dice[0] + multiple.dice[1];
    const who = [owner ? upper(owner.name) : "", title ? upper(title) : ""].filter(Boolean).join(" · ");
    return `${eyebrow}<section class="rentx rentx--dice" aria-label="${e(`Rent ${multiple.multiplier} times a new roll`)}">
      <div class="rentx__head"><span class="rentx__photo" data-photo="${photoKey(space)}" data-sheet="${landing.spaceIndex}" aria-hidden="true"></span><div class="rentx__who"><b>${e(upper(space.name))}</b><small>${owner ? dotHtml(state, owner.id, { size: 22 }) : ""}${e(who)}</small></div><span class="rentx__stamp" aria-hidden="true">×${multiple.multiplier}</span></div>
      <div class="rentx__roll"><span class="rentx__label">NEW ROLL</span>${diceHtml(multiple.dice, { className: "die--st", rotations: [-6, 5] })}<b>= ${total}</b></div>
      ${sum(`<b class="rentx__base rentx__base--plain">${total}</b>`)}
    </section>`;
  }
  const foot = `${owner ? dotHtml(state, owner.id, { size: 22 }) : ""}<b>${money(multiple.base)}</b>`;
  return `${eyebrow}<section class="rentx">
      <div class="rentx__head">${slipHtml(state, landing.spaceIndex, { foot })}<div class="rentx__why"><span class="rentx__stamp" aria-hidden="true">RENT ×${multiple.multiplier}</span>${title ? `<small>${e(upper(title))}</small>` : ""}</div></div>
      ${sum(`<span class="rentx__base">${money(multiple.base)}</span>`)}
    </section>`;
}

function rentBoard(state) {
  const player = me(state);
  const landing = state.lastLanding;
  const space = state.board[landing.spaceIndex];
  const owner = playerById(state, landing.recipientId);
  return {
    stage: eventHtml({ who: rentWho(landing, upper(shortName(space.name))), big: money(-landing.amount), tone: "red", sub: owner ? `${dotHtml(state, owner.id, { size: 14 })}to ${e(owner.name)}` : "" }),
    marks: { target: landing.spaceIndex },
    status: yourTurn(state),
    bigRent: landing.amount >= 500,
    // The rent is already paid when this shows, so the key stays END TURN (the board's PAY $50 is
    // the TV's debt-style wording; the engine has no "pay rent" step).
    extra: rentMultipleHtml(state, landing),
    buttons: endTurnButtons(state, player),
  };
}

function boughtThisTurn(state, player) {
  const log = state.log || [];
  for (let index = log.length - 1; index >= 0; index -= 1) {
    const text = log[index].text;
    if (text === `${player.name}'s turn`) return null;
    if (text.startsWith(`${player.name} bought `) || text.startsWith(`${player.name} won `)) {
      const name = text.slice(player.name.length).replace(/^ (?:bought|won) (.+) for \$\d+$/, "$1");
      return state.board.find((space) => space.name === name) || null;
    }
  }
  return null;
}

// What the landing square did, as a stage card (used on your own "done" screen and when watching).
function landingStage(state, playerId, viewerId) {
  const landing = state.lastLanding?.playerId === playerId ? state.lastLanding : null;
  const player = playerById(state, playerId);
  const youText = playerId === viewerId;
  const bought = player ? boughtThisTurn(state, player) : null;
  if (bought) return deedMiniHtml(state, bought.index, { eyebrow: youText ? "YOURS NOW" : `${upper(player.name)} BOUGHT`, sold: true, rows: false });
  if (!landing) return "";
  const space = state.board[landing.spaceIndex];
  const name = upper(shortName(space.name));
  switch (landing.kind) {
    case "rent": {
      const owner = playerById(state, landing.recipientId);
      return eventHtml({ who: rentWho(landing, name), big: youText ? money(-landing.amount) : money(landing.amount), tone: "red", sub: owner ? `${dotHtml(state, playerId, { size: 14 })}→ ${dotHtml(state, owner.id, { size: 14 })}` : "" });
    }
    case "tax": return eventHtml({ who: name, big: money(-landing.amount), tone: "red" });
    case "jail": return eventHtml({ who: youText ? "SENT TO JAIL" : `${upper(player?.name || "")} · JAIL`, big: "JAIL", tone: "orange" });
    case "chance":
    case "chest": return state.lastCard ? cardMiniHtml(state.lastCard.text, { deck: state.lastCard.deck || landing.kind }) : eventHtml({ who: name });
    case "own": return eventHtml({ who: name, big: youText ? "YOURS" : "THEIR OWN" });
    case "mortgaged": return eventHtml({ who: name, big: "MORTGAGED" });
    case "freeParking": return eventHtml({ who: "CITY PARK", big: landing.amount ? `+${money(landing.amount)}` : "FREE REST", tone: landing.amount ? "green" : "" });
    case "purchase": {
      // Sold at auction since: say who has it now, never a stale FOR SALE.
      const owner = playerById(state, propState(state, landing.spaceIndex)?.ownerId);
      if (owner) return deedMiniHtml(state, landing.spaceIndex, { eyebrow: owner.id === viewerId ? "YOURS NOW" : `SOLD · ${upper(owner.name)}`, sold: true, rows: false });
      return deedMiniHtml(state, landing.spaceIndex, { eyebrow: "FOR SALE", rows: false });
    }
    default: break;
  }
  if (space.type === "go") return eventHtml({ who: "PAYDAY", big: "+$200", tone: "green" });
  if (space.type === "parking") return eventHtml({ who: "CITY PARK", big: "FREE REST" });
  if (space.type === "jail") return eventHtml({ who: "JAIL", big: "JUST PASSING" });
  return eventHtml({ big: name });
}

function doneBoard(state, ui) {
  const player = me(state);
  const buttons = endTurnButtons(state, player);
  const chips = [buildChip(state, player), plannedChip(state, ui, { canSend: true })].filter(Boolean);
  const stage = landingStage(state, player.id, player.id) || diceStageHtml(state.turn?.dice || null, { blank: !state.turn?.dice });
  return {
    stage,
    marks: { target: player.position },
    status: yourTurn(state),
    buttons,
    chips,
  };
}

function jailReason(state, viewerId) {
  const cue = turnCues(state, viewerId, { limit: 10 }).reverse().find((item) => item.kind === "jail" && item.reason !== undefined);
  return cue?.reason || "";
}

function jailStageHtml(state, player, { title, sub = "" }) {
  return `<div class="pjail" data-jail>${dotHtml(state, player.id, { size: 34 })}<div class="jail-bars"><span></span><span></span><span></span><span></span></div></div>
    ${eventHtml({ who: title, big: sub })}`;
}

function jailBoard(state) {
  const player = me(state);
  const turnOver = state.phase === "turn_complete";
  const attempt = Math.min(3, player.jailTurns + 1);
  const reason = jailReason(state, player.id);
  const stage = jailStageHtml(state, player, { title: turnOver ? "SENT TO JAIL" : "IN JAIL", sub: turnOver ? (reason ? upper(reason) : "") : `TRY ${attempt} OF 3` });
  const buttons = turnOver
    ? [btn("END TURN", "", "primary", { act: { type: "end_turn" } })]
    // The TV's words and order (UX-01): A ROLL, X PAY $50, Y USE PASS.
    : [
      btn("ROLL", "", "primary", { act: { type: "roll" } }),
      btn(`PAY ${money(50)}`, player.cash >= 50 ? "" : "not enough cash", "ghost", { act: { type: "pay_bail" }, disabled: player.cash < 50 }),
      ...(player.jailCards ? [btn("USE PASS", player.jailCards > 1 ? `×${player.jailCards}` : "", "ghost", { act: { type: "use_jail_card" } })] : []),
    ];
  return {
    stage,
    heat: turnOver ? null : heatSquares(state, player.id),
    status: { text: "YOUR TURN · IN JAIL", playerId: player.id, tone: "gold" },
    buttons,
  };
}

// What the card's destination means, as one short badge under the card.
function cardOutcome(state, player, target, effect) {
  const prop = propState(state, target);
  const space = state.board[target];
  if (prop && !prop.ownerId) return `<div class="pbadge pbadge--ink">${icon("house", { size: 11 })} FOR SALE ${money(space.price)}</div>`;
  if (prop?.ownerId && prop.ownerId !== player.id) {
    const options = effect?.type === "nearestRail" ? { railroadMultiplier: 2 } : effect?.type === "nearestUtility" ? { utilityMultiplier: 10 } : {};
    const rent = rentFor(state, target, options);
    if (rent) return `<div class="pbadge pbadge--red">${dotHtml(state, prop.ownerId, { size: 12 })} RENT ${money(rent)}</div>`;
  }
  if (target < player.position && effect?.type !== "move" && effect?.type !== "jail" && target !== 0) return '<div class="pbadge pbadge--green">PASS PAYDAY +$200</div>';
  return "";
}

function cardBoard(state) {
  const player = me(state);
  const pending = state.pendingCard;
  const preview = cardPreview(state, player.id, pending.cardId);
  const target = cardTarget(state, player.id, pending.cardId);
  const effect = CHANCE_EFFECTS[pending.cardId];
  let path = null;
  let badge = "";
  if (target !== null && target !== player.position) {
    path = effect?.type === "jail" ? [player.position, target] : pathBetween(player.position, target, effect?.type === "move");
    badge = cardOutcome(state, player, target, effect);
  }
  return {
    stage: `${cardMiniHtml(pending.text, { deck: "chance" })}${badge}`,
    marks: target !== null ? { target, path } : null,
    status: yourTurn(state),
    buttons: [btn(preview.action.label, "", "primary", { act: { type: "confirm_card" } })],
  };
}

function orderStage(state, viewerId) {
  const order = state.openingOrder;
  const settled = state.phase === "order_countdown";
  if (settled) {
    const rows = (order?.order || []).map((id, index) => `<li>${dotHtml(state, id, { size: 14 })}<span>${ordinal(index + 1)}</span><b>${e(id === viewerId ? "You" : playerById(state, id)?.name || "")}</b></li>`).join("");
    return `<ol class="porder">${rows}</ol>`;
  }
  // Everyone's first roll, then the tie-break rolls of the players who tied ("2 · 5"); the tied
  // rows are marked, so a tie is visible to everyone and nobody else is told to roll.
  const rows = openingRows(state).map((row) => {
    const player = playerById(state, row.playerId);
    return `<li class="${row.tied ? "is-tied" : ""}">${dotHtml(state, row.playerId, { size: 14 })}<b>${e(row.playerId === viewerId ? "You" : player?.name || "")}</b><span>${e(row.shown)}</span></li>`;
  }).join("");
  return `<ol class="porder">${rows}</ol>`;
}

// The order line under the board: who rolls, and why.
export function orderStatusText(state, viewerId) {
  if (state.phase === "order_countdown") return "TURN ORDER SET";
  const tie = openingTie(state);
  if (!tie) return "ROLL FOR ORDER · HIGHEST STARTS";
  const mine = openingRows(state).find((row) => row.playerId === viewerId);
  if (mine?.pending) return mine.tieTotal !== null ? `TIED ON ${mine.tieTotal} · ROLL AGAIN` : "TIE-BREAK · ROLL AGAIN";
  if (mine?.tied) return "TIE-BREAK · WAITING";
  const names = tie.ids.map((id) => upper(playerById(state, id)?.name || ""));
  return names.length === 2 ? `TIE-BREAK · ${names[0]} & ${names[1]}` : `TIE-BREAK · ${names.length} PLAYERS`;
}

function orderBoard(state) {
  const player = me(state);
  const order = state.openingOrder;
  const settled = state.phase === "order_countdown";
  const pending = !settled && order?.pendingIds.includes(player?.id);
  const mine = openingRows(state).find((row) => row.playerId === player?.id);
  const buttons = settled || !player ? [] : pending
    ? [btn(order.round > 1 ? "ROLL AGAIN" : "ROLL FOR ORDER", "", "primary", { act: { type: "roll_for_order" } })]
    : [btn(mine?.first !== null && mine?.first !== undefined ? `YOU ROLLED ${mine.shown}` : "WAITING", "", "ghost", { disabled: true })];
  return { stage: orderStage(state, player?.id), status: { text: orderStatusText(state, player?.id), tone: pending ? "gold" : "" }, buttons };
}

function botTrade(state) {
  const from = playerById(state, state.trade?.proposerId);
  const to = playerById(state, state.trade?.targetId);
  return Boolean(from && to && inputOf(from) === "ai" && inputOf(to) === "ai");
}

// Someone else's turn, a spectator, or a player who is out.
function watchStage(state, viewerId, now = null) {
  const current = playerById(state, state.currentPlayerId);
  if (!current) return "";
  const name = upper(current.name);
  const dot = dotHtml(state, current.id, { size: 16 });
  const dice = state.turn?.playerId === current.id ? state.turn?.dice : null;
  switch (state.phase) {
    case "awaiting_roll":
      if (current.inJail) return jailStageHtml(state, current, { title: "IN JAIL", sub: `TRY ${Math.min(3, (current.jailTurns || 0) + 1)} OF 3` });
      if (dice && state.turn.doublesCount > 0) return diceStageHtml(dice, { doubles: dice[0] === dice[1], count: state.turn.doublesCount || 0 });
      return "";
    case "awaiting_purchase":
      return `${deedMiniHtml(state, state.pendingPurchase, { eyebrow: "FOR SALE", rows: false })}<div class="pbadge pbadge--ink">${dot}${e(name)} DECIDING…</div>`;
    case "card_confirmation":
      return cardMiniHtml(state.pendingCard?.text || "", { deck: "chance" });
    case "auction":
      return auctionStage(state, viewerId, now);
    case "debt": {
      const payer = playerById(state, state.debt?.payerId);
      return eventHtml({ who: upper(payer?.name || ""), big: `OWES ${money(state.debt.amount)}`, tone: "red", dot: dotHtml(state, payer?.id, { size: 16 }) });
    }
    case "trade": {
      // Two computers haggling: nothing on the board (an accepted deal shows in the deeds).
      if (botTrade(state)) return landingStage(state, current.id, viewerId) || (dice ? diceStageHtml(dice) : "");
      const from = playerById(state, state.trade?.proposerId);
      const to = playerById(state, state.trade?.targetId);
      return eventHtml({ who: "TRADE", big: `${upper(from?.name || "")} ⇄ ${upper(to?.name || "")}` });
    }
    case "loan_offer": {
      const offer = state.loanOffer;
      return eventHtml({ who: "LOAN", big: money(offer?.principal || 0), sub: `${dotHtml(state, offer?.proposerId, { size: 14 })}→ ${dotHtml(state, offer?.targetId, { size: 14 })}` });
    }
    case "turn_complete":
    default:
      return landingStage(state, current.id, viewerId) || (dice ? diceStageHtml(dice) : eventHtml({ who: name, big: "TURN DONE", dot }));
  }
}

function waitingBoard(state, ui) {
  const player = me(state);
  const spectator = !player;
  const out = player?.bankrupt;
  const current = playerById(state, state.currentPlayerId);
  const chips = [];
  if (!spectator && !out) {
    const plan = plannedChip(state, ui, { canSend: false });
    if (plan) chips.push(plan);
  }
  let status = current ? { text: `${upper(current.name)}'S TURN`, playerId: current.id } : { text: "WAITING" };
  if (state.phase === "auction") status = auctionStatus(state, player?.id || null);
  if (out) status = { ...status, text: `YOU'RE OUT · ${status.text}` };
  return {
    stage: watchStage(state, player?.id || null, nowOf(state, ui)),
    marks: state.phase === "awaiting_purchase" ? { target: state.pendingPurchase } : state.phase === "auction" ? { target: state.auction?.spaceIndex } : null,
    status,
    buttons: [],
    chips,
    // An auction you can bid in shows its bid tiles instead (auctionBoard).
    extra: state.phase === "auction" ? "" : glanceHtml(state),
  };
}

// ---------------------------------------------------------------------------------------------
// At a glance, in the space between the status line and the buttons while you wait or before you
// roll (review 4 Nov #8: about 440 px of empty felt on a 390 × 844 phone). Visual, a few words,
// no history list and no probability sentence (the one-glance rules): you (cash, deeds, full sets,
// buildings), a loan coming due, a chip when your next roll could cost a lot (the heat on the
// board shows where), and the latest move at the table in one line.

const FEED_KINDS = new Set(["buy", "rent", "build", "trade", "bankrupt", "jail"]);

// The latest move worth a line: a sale, a rent, a build, a deal, someone going out or to jail.
function lastMove(state) {
  const log = state?.log || [];
  for (let index = log.length - 1; index >= 0; index -= 1) {
    const item = parseLog(log[index], state);
    if (!FEED_KINDS.has(item.kind)) continue;
    if (item.kind === "trade" && !/ accepted /.test(item.text)) continue;
    const mine = item.kind === "rent" && (item.playerId === state.viewerId ? -1 : item.otherId === state.viewerId ? 1 : 0);
    return { text: item.text, amount: mine ? mine * item.amount : 0 };
  }
  return null;
}

export function glanceHtml(state) {
  if (!state || state.status !== "playing") return "";
  const player = me(state);
  const playing = Boolean(player) && !player.bankrupt;
  const parts = [];
  if (playing) {
    const deeds = ownedBy(state, player.id);
    const sets = fullSets(state, player.id);
    const buildings = deeds.reduce((sum, index) => sum + (propState(state, index)?.houses || 0), 0);
    const setChips = sets.map((group) => `<i class="glance__set" style="background:${GROUP_COLORS[group]}" title="${e(colourWord(group))} set"></i>`).join("");
    const stats = [
      `<span class="glance__stat">${icon("card", { size: 14 })}<b>${deeds.length}</b>${deeds.length === 1 ? "DEED" : "DEEDS"}</span>`,
      sets.length ? `<span class="glance__stat">${setChips}<b>${sets.length}</b>${sets.length === 1 ? "SET" : "SETS"}</span>` : "",
      buildings ? `<span class="glance__stat">${icon("house", { size: 14 })}<b>${buildings}</b></span>` : "",
    ].join("");
    parts.push(`<div class="glance__me" data-key="glance-me"><span class="glance__label">YOU</span><span class="glance__cash">${money(player.cash)}</span>${stats}</div>`);
    // Set progress: one chip per colour you hold (2/3), railroads as one; full sets first.
    const groups = new Map();
    for (const index of deeds) {
      const space = state.board[index];
      const key = space.type === "property" ? space.group : space.type;
      if (!groups.has(key)) groups.set(key, { key, kind: space.type, owned: 0, size: space.type === "property" ? groupIndexes(state, space.group).length : space.type === "railroad" ? RAILROADS.length : UTILITIES.length });
      groups.get(key).owned += 1;
    }
    const progress = [...groups.values()].sort((a, b) => b.owned / b.size - a.owned / a.size).map((group) => {
      const colour = group.kind === "property" ? GROUP_COLORS[group.key] : "#2B323A";
      const ink = group.kind === "property" ? GROUP_INK[group.key] : "#FFFFFF";
      const label = group.kind === "property" ? colourWord(group.key) : group.kind === "railroad" ? "Railroads" : "Utilities";
      const glyph = group.kind === "railroad" ? icon("rail", { size: 12 }) : group.kind === "utility" ? icon("bolt", { size: 12 }) : "";
      return `<span class="glance__group${group.owned === group.size ? " is-full" : ""}" style="background:${colour};color:${ink}" title="${e(label)}: ${group.owned} of ${group.size}">${glyph}${group.owned}/${group.size}</span>`;
    }).join("");
    if (progress) parts.push(`<div class="glance__groups" data-key="glance-groups">${progress}</div>`);
    const loan = activeLoans(state, player.id).find((entry) => entry.borrowerId === player.id);
    if (loan) {
      const due = loanDue(state, loan);
      parts.push(`<div class="glance__line glance__line--warn" data-key="glance-loan">${icon("loan", { size: 14 })}<span>You owe ${money(loan.repay)}${due ? ` · ${e(due)}` : ""}</span></div>`);
    }
    const heat = player.inJail ? null : nextRollHeat(state, player.id);
    if (heat?.alert) parts.push(`<div class="glance__line glance__line--warn" data-key="glance-risk">${icon("warn", { size: 14 })}<span>NEXT ROLL · UP TO ${money(heat.alert)} AHEAD</span></div>`);
  }
  const last = lastMove(state);
  if (last) {
    parts.push(`<div class="glance__last" data-key="glance-last"><span class="glance__label">LAST</span><span class="glance__text">${e(last.text)}</span>${last.amount ? `<b class="${last.amount > 0 ? "is-pos" : "is-neg"}">${last.amount > 0 ? "+" : "−"}${money(Math.abs(last.amount))}</b>` : ""}</div>`);
  }
  return parts.length ? `<section class="glance" aria-label="At a glance">${parts.join("")}</section>` : "";
}

// ---------------------------------------------------------------------------------------------
// Stand-in (UX-07): COMPUTER PLAYING with I'M BACK while it plays for you; YOU'RE BACK with what
// happened once it handed the seat back (players[].standIn, docs/API.md §3c).

// The while-away lines as rows, newest last: "Rolled 3 + 4 = 7" and "Landed on City Park" read as
// one row (Rolled 3 + 4 · City Park); turn markers ("Maya's turn") are left out.
function awayRows(state, player, limit = 3) {
  const lines = awayLines(state, player, { limit: 40 }).lines.map((line) => line.text).filter((text) => !/^(Your|.+'s) turn$/.test(text));
  const rows = [];
  for (let index = 0; index < lines.length; index += 1) {
    const roll = lines[index].match(/^Rolled (\d+ \+ \d+)(?: = \d+)?$/);
    const landed = lines[index + 1]?.match(/^Landed on (.+)$/);
    if (roll && landed) {
      rows.push({ text: `Rolled ${roll[1]}`, end: landed[1] });
      index += 1;
    } else rows.push({ text: lines[index], end: "" });
  }
  return rows.slice(-limit);
}

function awayListHtml(rows, cash = null) {
  const items = rows.map((row) => `<li><span>${e(row.text)}</span>${row.end ? `<b>${e(row.end)}</b>` : ""}</li>`);
  if (cash) items.push(`<li class="away__cash"><span>CASH</span><b class="${cash.change > 0 ? "is-pos" : cash.change < 0 ? "is-neg" : ""}">${cash.change > 0 ? "+" : cash.change < 0 ? "−" : ""}${money(Math.abs(cash.change))}</b></li>`);
  return items.length ? `<ul class="away">${items.join("")}</ul>` : "";
}

const STANDIN_BY = { away: "AWAY", idle: "IDLE", host: "BY HOST" };

function standinCardHtml(state, player, now) {
  const label = STANDIN_BY[player.standIn?.by] || "IDLE";
  const ms = standInMs(player, now);
  const since = Number(player.standIn?.since);
  const time = ms !== null
    ? `<small data-tick-since="${since}" data-tick-prefix="${label} ">${label} ${minutesText(ms)}</small>`
    : `<small>${label}</small>`;
  return `<section class="standin" aria-label="The computer is playing for you">
    <div class="standin__head"><span class="standin__bot">${icon("bot", { size: 30 })}</span><div class="standin__title"><b>PLAYING FOR YOU</b>${time}</div></div>
    ${awayListHtml(awayRows(state, player, 3))}</section>`;
}

// YOU'RE BACK: shown on the board in place of the at-a-glance card until your next command; ×
// sends {type:"back"}, which only dismisses it.
export function awayCardHtml(state, player) {
  return `<section class="awaycard" aria-label="While you were away">
    <div class="awaycard__head">${icon("check", { size: 22 })}<b>YOU'RE BACK</b><span class="awaycard__pill">WHILE AWAY</span><button type="button" class="awaycard__x" data-act='{"type":"back"}' aria-label="Dismiss">${icon("cross", { size: 14 })}</button></div>
    ${awayListHtml(awayRows(state, player, 3), awayCash(player))}</section>`;
}

function standinBoard(state, ui) {
  const player = me(state);
  const now = nowOf(state, ui);
  return {
    stage: watchStage(state, player.id, now),
    marks: state.phase === "awaiting_purchase" ? { target: state.pendingPurchase } : state.phase === "auction" ? { target: state.auction?.spaceIndex } : null,
    status: { text: "COMPUTER PLAYING", glyph: "bot" },
    extra: standinCardHtml(state, player, now),
    buttons: [btn("I'M BACK", "", "primary", { act: { type: "back" }, html: `${icon("person", { size: 26 })} I'M BACK` })],
  };
}

const BOARD_SCREENS = { roll: rollBoard, buy: buyBoard, auction: auctionBoard, rent: rentBoard, done: doneBoard, jail: jailBoard, card: cardBoard, order: orderBoard, waiting: waitingBoard, standin: standinBoard };

export function isBoardScreen(screen) {
  return Boolean(BOARD_SCREENS[screen]);
}

export function boardScreen(screen, state, ui) {
  const built = { heat: null, marks: null, extra: "", chips: [], buttons: [], status: null, ...BOARD_SCREENS[screen](state, ui) };
  // Back from the stand-in: YOU'RE BACK takes the at-a-glance card's place.
  const player = me(state);
  if (screen !== "standin" && showAwayList(player)) {
    const glance = glanceHtml(state);
    built.extra = `${awayCardHtml(state, player)}${built.extra === glance ? "" : built.extra}`;
  }
  return built;
}


export { ownsGroup };
