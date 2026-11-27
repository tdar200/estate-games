// Pure HTML builders for the PC table screen. They take a state plus a small context object and
// return strings, so every fixture can be rendered and checked in Node (web/tests/*.check.mjs).
// One glance: players (token, name, cash, deeds), one "now" card (who, what, the buttons), the
// board. No history list: what just happened plays on the board and the plates and then goes.

import { cellPosition, deedHtml } from "../shared/board.js";
import {
  GROUP_COLORS, GROUP_INK, aiLevelLabel, colourWord, counterChanges, deviceLabel, escapeHtml, inputOf, lastReply, money, playerById, possessive, quoteState, signedMoney, spacePhoto, upper,
} from "../shared/format.js";
import {
  buildOptions, canManage, cardPreview, dealBalance, debtPlan, fullSets, groupIndexes, mortgageValue, nextBuild,
  nextRollHeat, nextSale, openingRows, openingTie, ownedBy, ownsGroup, propState, rentLabel, standings, tradeable, unmortgageCost, canMortgage,
  rulesOf, loanTerms, offerParties, pledgeable, loanSecuredBy, netWorth, rentFor, tradeImpact, rentMultiple,
} from "../shared/insights.js";
import { GOING_ONCE_MS, QUICK_STEPS, auctionActors, auctionRoster, canBid, clockFraction, clockText, goingOnce, isTimed, msLeft, quickBids, secondsLeft } from "../shared/auction-clock.js";
import { hostControls, idleShown, isPerson, minutesText, removalSummary, waitedOnIds } from "../shared/standin.js";
import { debtChecklist } from "../shared/debt-checklist.js";
import { NEW_TOKEN_KINDS } from "../shared/tokens.js";
import { deedDetailHtml } from "../shared/deed.js";
import { deedActions } from "../shared/deedactions.js";
import { MAX_PLAYERS, TOKEN_KINDS, TOKEN_LABELS, TOKEN_COLORS, TOKEN_COLOR_NAMES, canonicalKind, inkOn, playerChipHtml, playerColor, playerTokenSvg, tokenSvg } from "../shared/tokens.js";
import { gamePickHtml, gameTypeOf, gtypeTag } from "../shared/poker-view.js";

// Glyphs filled with a player colour need readable text (Rose and Sky take dark text).
const PLAYER_FILLS = new Set(Object.values(TOKEN_COLORS).map((color) => color.toUpperCase()));
// A keyboard key is drawn as a cream key cap (dark letter, a printed edge); pad buttons in their own
// colours (the tabletop look's badges).
function glyphStyle(bg) {
  const fill = bg || "#5B6470";
  if (fill === G.KEY) return `background:${fill};color:#22201C;box-shadow:inset 0 0 0 1.5px #2B2620,0 2px 0 #2B2620`;
  return `background:${fill}${PLAYER_FILLS.has(String(fill).toUpperCase()) ? `;color:${inkOn(fill)};box-shadow:inset 0 0 0 1.5px rgba(14,18,22,.55)` : ""}`;
}

const G = { A: "#1E9E5A", B: "#D94242", X: "#3A6FB5", Y: "#C99A12", MENU: "#5B6470", VIEW: "#5B6470", START: "#5B6470", KEY: "#F6EFDF", OUT: "#8A929C" };

const e = escapeHtml;

// Small inline icons (currentColor). Decorative: the text next to them, or an sr-only label, names them.
const svg = (body) => `<svg class="ico" viewBox="0 0 24 24" aria-hidden="true" focusable="false">${body}</svg>`;
export const ICONS = {
  help: svg('<circle cx="12" cy="12" r="9.5" fill="none" stroke="currentColor" stroke-width="2"/><path d="M9.3 9.4a2.8 2.8 0 1 1 3.9 2.6c-.8.4-1.2 1-1.2 1.9v.6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><circle cx="12" cy="17.4" r="1.3" fill="currentColor"/>'),
  search: svg('<circle cx="10.5" cy="10.5" r="6.5" fill="none" stroke="currentColor" stroke-width="2.2"/><path d="M15.5 15.5L21 21M10.5 7.5v6M7.5 10.5h6" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"/>'),
  pause: svg('<path d="M3 12c3-6 6-6 9 0s6 6 9 0" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"/>'),
  arrowLeft: svg('<path d="M19 12H5M11 6l-6 6 6 6" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>'),
  menu: svg('<path d="M4 7h16M4 12h16M4 17h16" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/>'),
  speaker: svg('<path d="M4 9h4l5-4v14l-5-4H4z" fill="currentColor"/><path d="M16 8.5a5 5 0 0 1 0 7M18.5 6a8.5 8.5 0 0 1 0 12" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>'),
  speakerOff: svg('<path d="M4 9h4l5-4v14l-5-4H4z" fill="currentColor"/><path d="M16 9l5 6M21 9l-5 6" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>'),
  note: svg('<path d="M9 18V6l11-2v12" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><circle cx="6.5" cy="18" r="2.5" fill="currentColor"/><circle cx="17.5" cy="16" r="2.5" fill="currentColor"/>'),
  bell: svg('<path d="M6 16V11a6 6 0 0 1 12 0v5l2 2H4z" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><path d="M10 20a2 2 0 0 0 4 0" fill="none" stroke="currentColor" stroke-width="2"/>'),
  phone: svg('<rect x="6" y="2" width="12" height="20" rx="2.5" fill="none" stroke="currentColor" stroke-width="2"/><circle cx="12" cy="18" r="1.3" fill="currentColor"/>'),
  pad: svg('<path d="M7 7h10a5 5 0 0 1 4.8 6.4l-1.3 4.2a2.6 2.6 0 0 1-4.3 1.1L14 16.5h-4l-2.2 2.2a2.6 2.6 0 0 1-4.3-1.1l-1.3-4.2A5 5 0 0 1 7 7z" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><path d="M8 10v4M6 12h4" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><circle cx="16" cy="11" r="1.2" fill="currentColor"/><circle cx="18" cy="13.4" r="1.2" fill="currentColor"/>'),
  keyboard: svg('<rect x="2" y="6" width="20" height="12" rx="2" fill="none" stroke="currentColor" stroke-width="2"/><path d="M6 10h1M9.5 10h1M13 10h1M16.5 10h1M7 14h10" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>'),
  cpu: svg('<rect x="6" y="6" width="12" height="12" rx="2" fill="none" stroke="currentColor" stroke-width="2"/><rect x="9.5" y="9.5" width="5" height="5" fill="currentColor"/><path d="M9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>'),
  browser: svg('<rect x="2" y="4" width="20" height="16" rx="2" fill="none" stroke="currentColor" stroke-width="2"/><path d="M2 9h20" stroke="currentColor" stroke-width="2"/>'),
  lock: svg('<rect x="5" y="10" width="14" height="11" rx="2" fill="currentColor"/><path d="M8 10V7a4 4 0 0 1 8 0v3" fill="none" stroke="currentColor" stroke-width="2.4"/>'),
  card: svg('<rect x="5" y="3" width="14" height="18" rx="2" fill="none" stroke="currentColor" stroke-width="2"/><path d="M9 9l6 6M15 9l-6 6" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>'),
  chance: svg('<rect x="4" y="2.5" width="16" height="19" rx="2.5" fill="currentColor"/><path d="M9.5 9a2.6 2.6 0 1 1 3.6 2.4c-.8.4-1.1.9-1.1 1.8v.6" fill="none" stroke="#FFFFFF" stroke-width="2.2" stroke-linecap="round"/><circle cx="12" cy="17.4" r="1.3" fill="#FFFFFF"/>'),
  coin: svg('<circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" stroke-width="2"/><path d="M14.5 8.5h-3.2a1.9 1.9 0 0 0 0 3.8h1.4a1.9 1.9 0 0 1 0 3.8H9.5M12 6.5v2M12 16v2" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>'),
  hammer: svg('<path d="M13 4l7 7-3 3-7-7z" fill="currentColor"/><path d="M11.5 9.5L3.5 17.5" stroke="currentColor" stroke-width="3" stroke-linecap="round"/><path d="M3 21h9" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>'),
  house: svg('<path d="M3 11l9-7 9 7v9a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z" fill="currentColor"/>'),
  swap: svg('<path d="M4 8h14l-3-3M20 16H6l3 3" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/>'),
  warn: svg('<path d="M12 3l10 18H2z" fill="currentColor"/><path d="M12 10v5" stroke="#15191E" stroke-width="2.2" stroke-linecap="round"/><circle cx="12" cy="18" r="1.2" fill="#15191E"/>'),
  arrow: svg('<path d="M4 12h14M13 6l6 6-6 6" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>'),
  up: svg('<path d="M12 20V5M6 11l6-6 6 6" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>'),
  check: svg('<path d="M4 12.5l5 5L20 6.5" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>'),
  dice: svg('<rect x="3" y="3" width="18" height="18" rx="4" fill="none" stroke="currentColor" stroke-width="2"/><circle cx="8.5" cy="8.5" r="1.6" fill="currentColor"/><circle cx="15.5" cy="15.5" r="1.6" fill="currentColor"/><circle cx="12" cy="12" r="1.6" fill="currentColor"/>'),
  crown: svg('<path d="M3 8l4.5 4L12 5l4.5 7L21 8l-2 11H5z" fill="currentColor"/>'),
  globe: svg('<circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" stroke-width="2"/><path d="M3 12h18M12 3c3 3.5 3 14.5 0 18M12 3c-3 3.5-3 14.5 0 18" fill="none" stroke="currentColor" stroke-width="1.8"/>'),
  wifi: svg('<path d="M2.5 9a14 14 0 0 1 19 0M5.8 12.6a9 9 0 0 1 12.4 0M9.2 16.2a4 4 0 0 1 5.6 0" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"/><circle cx="12" cy="19.5" r="1.5" fill="currentColor"/>'),
  wifiOff: svg('<path d="M2.5 9a14 14 0 0 1 19 0M5.8 12.6a9 9 0 0 1 12.4 0M9.2 16.2a4 4 0 0 1 5.6 0" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" opacity=".45"/><circle cx="12" cy="19.5" r="1.5" fill="currentColor"/><path d="M4 3l16 18" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/>'),
  camera: svg('<path d="M4 7h3l2-3h6l2 3h3a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V8a1 1 0 0 1 1-1z" fill="none" stroke="currentColor" stroke-width="2"/><circle cx="12" cy="13" r="3.5" fill="none" stroke="currentColor" stroke-width="2"/>'),
  loan: svg('<path d="M3 17h18M6 17V9M10 17V9M14 17V9M18 17V9M2 9l10-5 10 5z" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/>'),
  stamp: svg('<path d="M9 3h6l-1 8h4l1 4H5l1-4h4z" fill="currentColor"/><path d="M4 19h16" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/>'),
  deed: svg('<rect x="5" y="3" width="14" height="18" rx="2" fill="none" stroke="currentColor" stroke-width="2"/><rect x="5" y="3" width="14" height="6" rx="1.5" fill="currentColor"/><path d="M8.5 13h7M8.5 16.5h5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/>'),
  out: svg('<circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" stroke-width="2.2"/><path d="M6 18L18 6" stroke="currentColor" stroke-width="2.2"/>'),
  gear: svg('<path d="M12 8.2a3.8 3.8 0 1 0 0 7.6 3.8 3.8 0 0 0 0-7.6z" fill="none" stroke="currentColor" stroke-width="2.2"/><path d="M12 2.5v3M12 18.5v3M2.5 12h3M18.5 12h3M5.3 5.3l2.1 2.1M16.6 16.6l2.1 2.1M5.3 18.7l2.1-2.1M16.6 7.4l2.1-2.1" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"/>'),
  robot: svg('<rect x="4" y="7" width="16" height="12" rx="3" fill="currentColor"/><circle cx="9" cy="12.5" r="1.7" fill="#15191E"/><circle cx="15" cy="12.5" r="1.7" fill="#15191E"/><path d="M12 3v4M9.5 16h5" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><circle cx="12" cy="3" r="1.4" fill="currentColor"/><path d="M2 12v3M22 12v3" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>'),
  screen: svg('<rect x="2" y="3.5" width="20" height="13" rx="2" fill="none" stroke="currentColor" stroke-width="2"/><path d="M8 21h8M12 16.5V21" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>'),
  resume: svg('<path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/><path d="M19.8 3.8v4.6h-4.6" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/>'),
  clock: svg('<circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" stroke-width="2.2"/><path d="M12 7v5.5l3.5 2" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/>'),
  gamepad: svg('<path d="M7 7h10a5 5 0 0 1 4.8 6.4l-1.3 4.2a2.6 2.6 0 0 1-4.3 1.1L14 16.5h-4l-2.2 2.2a2.6 2.6 0 0 1-4.3-1.1l-1.3-4.2A5 5 0 0 1 7 7z" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><path d="M8 10.5v3M6.5 12h3" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><circle cx="15.5" cy="11" r="1.1" fill="currentColor"/><circle cx="17.5" cy="13.2" r="1.1" fill="currentColor"/>'),
  views: svg('<rect x="3" y="3" width="12" height="12" rx="2" fill="none" stroke="currentColor" stroke-width="2"/><rect x="9" y="9" width="12" height="12" rx="2" fill="none" stroke="currentColor" stroke-width="2"/>'),
};

const DEVICE_ICON = { phone: "phone", controller: "pad", keyboard: "keyboard", ai: "cpu", classic: "browser" };

// A player's round chip with their token's silhouette (initials collide: Ana / Alpha).
function chip(state, player, size = 22) {
  return player ? playerChipHtml(state, player, { size, glyph: true }) : "";
}

export function shortName(name) {
  return String(name || "")
    .replace(/\bRailroad\b/, "RR")
    .replace(/\bStreet\b/, "St")
    .replace(/\bAvenue\b/, "Ave")
    .replace(/\bRoad\b/, "Rd")
    .replace(/\bCompany\b/, "Co");
}

function spaceColour(space) {
  if (space?.type === "property") return GROUP_COLORS[space.group];
  if (space?.type === "railroad") return "#2B323A";
  return "#5B6470";
}

function spacedCode(code) {
  return String(code || "").split("").join(" ");
}

// ---------------------------------------------------------------------------------------------
// Seats and devices

export function seatFor(state, playerId) {
  return state?.localSeats?.find((seat) => seat.playerId === playerId) || null;
}

export function padLabel(ctx, pad) {
  return ctx?.padNames?.[pad] || "Controller";
}

function deviceOf(state, player, ctx) {
  const seat = seatFor(state, player.id);
  const padName = seat && seat.input === "controller" ? padLabel(ctx, seat.pad) : null;
  return deviceLabel(state, player, { padName });
}

// Who must act right now (player ids).
export function actorIds(state) {
  if (!state) return [];
  switch (state.phase) {
    case "determining_order": return state.openingOrder?.pendingIds || [];
    // A timed auction: everyone still in except the leader may bid (docs/API.md §3c).
    case "auction": return auctionActors(state);
    case "trade": return [state.trade?.targetId, state.trade?.proposerId].filter(Boolean);
    case "loan_offer": return [state.loanOffer?.targetId, state.loanOffer?.proposerId].filter(Boolean);
    case "card_confirmation": return [state.pendingCard?.playerId].filter(Boolean);
    case "debt": return [state.debt?.payerId].filter(Boolean);
    case "awaiting_roll":
    case "awaiting_purchase":
    case "turn_complete": return [state.currentPlayerId].filter(Boolean);
    default: return [];
  }
}

// The one player everyone is waiting on (pulsing ring on their plate).
export function waitingOnId(state) {
  switch (state?.phase) {
    // Nobody holds a timed auction up: everyone bids at once on the clock.
    case "auction": return isTimed(state.auction) ? null : state.auction?.currentPlayerId || null;
    case "trade": return state.trade?.targetId || null;
    case "loan_offer": return state.loanOffer?.targetId || null;
    case "debt": return state.debt?.payerId || null;
    default: return null;
  }
}

// A trade between two computers. The TV keeps those quiet (review B6: refused bot-to-bot offers
// took over the action card, the show slot and the camera a dozen times a game): no deal card, no
// "TRADE?", no marked squares, no sound. One that is accepted shows as ✓ DEAL on both plates.
export function botDeal(state, trade = state?.trade) {
  if (!trade) return false;
  const proposer = playerById(state, trade.proposerId);
  const target = playerById(state, trade.targetId);
  return Boolean(proposer && target && inputOf(proposer) === "ai" && inputOf(target) === "ai");
}

// Outcome chips the TV doesn't show: a computer turning down another computer's offer.
export function quietReceipt(receipt, state) {
  if (receipt?.kind !== "declined") return false;
  const a = playerById(state, receipt.playerId);
  const b = playerById(state, receipt.otherId);
  return Boolean(a && b && inputOf(a) === "ai" && inputOf(b) === "ai");
}

// Local seats that need to act now, keyboard seats first.
export function actingSeats(state) {
  const need = actorIds(state);
  const seats = (state?.localSeats || []).filter((seat) => need.includes(seat.playerId));
  return seats.sort((a, b) => Number(a.input !== "keyboard") - Number(b.input !== "keyboard"));
}

// The one key map (UX-01): a keyboard seat sees the same coloured A / X / Y / B and ⧉ as a
// controller (its keys are those letters, and V); a key of its own (Q, S) shows as a key cap.
function glyphFor(seat, button, key) {
  if (seat?.input === "keyboard" && (key !== KEY_FOR[button] || !G[button])) return { glyph: key, glyphBg: G.KEY };
  // Property is the pad's View (⧉) button; Start (☰) opens the menu.
  return { glyph: button === "MENU" || button === "START" ? "☰" : button === "VIEW" ? "⧉" : button, glyphBg: G[button] || G.MENU };
}

// ---------------------------------------------------------------------------------------------
// Join screen (lobby)

function qrPlaceholderCells(seed = 7) {
  const n = 29;
  const cells = [];
  let value = seed;
  const rand = () => { value = (value * 1103515245 + 12345) % 2147483648; return value / 2147483648; };
  const finder = (r, c) => {
    for (const [fr, fc] of [[0, 0], [0, n - 7], [n - 7, 0]]) {
      const y = r - fr;
      const x = c - fc;
      if (y >= -1 && y <= 7 && x >= -1 && x <= 7) {
        if (y < 0 || y > 6 || x < 0 || x > 6) return 0;
        if (y === 0 || y === 6 || x === 0 || x === 6) return 1;
        return y >= 2 && y <= 4 && x >= 2 && x <= 4 ? 1 : 0;
      }
    }
    return -1;
  };
  for (let r = 0; r < n; r += 1) {
    for (let c = 0; c < n; c += 1) {
      let v = finder(r, c);
      if (v === -1) v = r === 6 || c === 6 ? ((r + c) % 2 === 0 ? 1 : 0) : (rand() > 0.52 ? 1 : 0);
      if (r >= 20 && r <= 24 && c >= 20 && c <= 24) v = r === 20 || r === 24 || c === 20 || c === 24 || (r === 22 && c === 22) ? 1 : 0;
      cells.push(v);
    }
  }
  return cells;
}

// Decorative stand-in used in demo mode and when the server has no qr.svg.
export function qrPlaceholderHtml({ label = "QR code unavailable", note = "" } = {}) {
  const cells = qrPlaceholderCells().map((on) => `<span${on ? ' class="on"' : ""}></span>`).join("");
  return `<div class="qr qr--placeholder" role="img" aria-label="${e(label)}"><div class="qr__grid">${cells}</div>${note ? `<span class="qr__note">${e(note)}</span>` : ""}</div>`;
}

export function joinUrl(state, ctx) {
  const base = ctx?.server?.publicURL || ctx?.server?.lanURL || ctx?.origin || "";
  return `${base.replace(/\/$/, "")}/j/${state?.code || ""}`;
}

// Who can use the invite link: anyone (the server has a public URL) or only this network.
export function joinReach(ctx) {
  return ctx?.server?.publicURL ? "anywhere" : "lan";
}

// "RESUME A TABLE": type a 5-letter table code to open that table on this screen (docs/API.md
// "Getting back in"). In the lobby header and on the gate screen.
export function resumeFormHtml({ code = "", error = "", compact = true } = {}) {
  return `<form class="resume-form${compact ? " resume-form--compact" : ""}" data-form="resume" autocomplete="off">
    <label class="resume-form__label" for="resume-code">${ICONS.resume}<span>RESUME A TABLE</span></label>
    <input id="resume-code" class="resume-form__input" name="code" maxlength="5" spellcheck="false" autocapitalize="characters" placeholder="CODE" value="${e(code)}">
    <button type="submit" class="resume-form__go" data-cmd="resume-open" aria-label="Open that table">${ICONS.arrow}</button>
    ${error ? `<span class="resume-form__error" role="alert">${e(error)}</span>` : ""}
  </form>`;
}

// Seats at a table: the server's number when it sends one, else every token (8).
export function maxPlayers(state) {
  const value = Number(state?.maxPlayers);
  return Number.isInteger(value) && value >= 2 ? Math.min(value, MAX_PLAYERS) : MAX_PLAYERS;
}

// × on a lobby seat (the table is the host, so it may remove anyone before the start; 0.10.2):
// a computer goes at once, a person after a confirm (table.js askRemovePlayer).
export function removeSeatButtonHtml(player) {
  return `<button type="button" class="seat__remove" data-cmd="remove-player" data-arg="${e(player.id)}" aria-label="Remove ${e(player.name)}" title="Remove ${e(player.name)}">×</button>`;
}

// NEW TABLE in the lobby header: start over with a new table and code (0.10.2; it used to be only
// on the finish screen, so a lobby had no way out).
export function newTableButtonHtml() {
  return '<button class="chip-btn join__new" type="button" data-cmd="new-table" title="Start over with a new table and a new code">NEW TABLE</button>';
}

// WHATSAPP and COPY LINK under the QR code on every PC lobby (0.11.1: there was no way to send the
// link from the PC; the link text is not even selectable). table.js lobbyCommand handles both.
export function inviteButtonsHtml() {
  return `<div class="qr-card__share"><button class="chip-btn chip-btn--wa" type="button" data-cmd="invite-whatsapp" title="Send the join link on WhatsApp">${ICONS.phone}<span>WHATSAPP</span></button><button class="chip-btn" type="button" data-cmd="invite-copy" title="Copy the join link">COPY LINK</button></div>`;
}

export function joinHtml(state, ctx = {}) {
  const players = state?.players || [];
  const code = state?.code || "·····";
  const link = joinUrl(state, ctx).replace(/^https?:\/\//, "");
  const watching = state?.viewerRole && state.viewerRole !== "table";
  const anywhere = joinReach(ctx) === "anywhere";
  const qr = ctx.qrUrl
    ? `<img class="qr qr--img" src="${e(ctx.qrUrl)}" width="300" height="300" alt="QR code that opens the join page for table ${e(code)}">`
    : qrPlaceholderHtml({ label: `QR code for table ${code}`, note: ctx.demo ? "DEMO" : "" });

  const freePad = (ctx.pads || []).find((pad) => !pad.seated);
  const ready = players.length;
  const max = maxPlayers(state);
  const canStart = ready >= 2 && !watching;
  const seats = [];
  let hinted = false;
  for (let index = 0; index < max; index += 1) {
    const player = players[index];
    if (player) {
      const host = state.hostId === player.id;
      const input = inputOf(player);
      const device = deviceOf(state, player, ctx);
      seats.push(`<div class="seat${host ? " seat--host" : ""}">
        ${playerTokenSvg(state, player, { size: 74, label: `${player.name}'s token` })}
        <span class="seat__name">${e(upper(player.name))}</span>
        <span class="seat__device" title="${e(device)}">${ICONS[DEVICE_ICON[input] || "browser"]}<span class="sr-only">${e(device)}</span>${input === "ai" && player.difficulty ? `<span class="seat__level seat__level--${e(player.difficulty)}">${e(upper(aiLevelLabel(player.difficulty)))}</span>` : ""}</span>
        ${watching ? "" : removeSeatButtonHtml(player)}
      </div>`);
    } else if (!hinted && !watching) {
      // The first open seat says how to sit down, in words (review 4 Nov #6: it used to be icons
      // with the words for screen readers only): scan, press A on a controller, or Enter.
      // A connected controller that has no seat yet lights it up.
      hinted = true;
      seats.push(`<div class="seat seat--open seat--hint${freePad ? " is-ready" : ""}">
        <span class="seat__plus" aria-hidden="true">+</span>
        <span class="seat__ways" aria-hidden="true"><span class="seat__way" title="Scan the code with a phone">${ICONS.phone}</span><span class="seat__way" title="Press A on a controller">${ICONS.pad}<b class="key-cap key-cap--pad">A</b></span><span class="seat__way" title="Press Enter on the keyboard">${ICONS.keyboard}<b class="key-cap">↵</b></span></span>
        <span class="seat__hint" title="Scan the code with a phone, press A on a controller, or Enter on the keyboard"><b>SCAN TO JOIN</b><small>or press A · Enter</small></span>
      </div>`);
    } else {
      seats.push('<div class="seat seat--open"><span class="seat__plus" aria-hidden="true">+</span><span class="sr-only">Open seat</span></div>');
    }
  }
  const reach = anywhere
    ? `<span class="reach reach--anywhere">${ICONS.globe}<b>ANYWHERE</b></span>`
    : `<span class="reach reach--lan">${ICONS.wifi}<b>SAME WI‑FI</b></span>`;
  const rules = rulesOf(state);
  const custom = rules.loans || rules.freeParkingJackpot || !rules.auctions || rules.startingCash !== 1500;

  return `<header class="join__head">
      <span class="join__brand" id="join-title">ESTATE</span>
      <span class="dot dot--${e(ctx.status || "live")}" aria-hidden="true" title="${ctx.status === "offline" ? "Not connected to the server" : "Connected to the server"}"></span>
      ${watching ? "" : resumeFormHtml({ code: ctx.resumeCode || "", error: ctx.resumeError || "" })}
      ${watching ? "" : newTableButtonHtml()}
      ${soundButtonHtml(ctx.sound)}
      <button class="icon-btn menu-btn" type="button" data-cmd="menu" aria-haspopup="dialog" aria-label="Menu" title="Menu: settings, how to play, leave">${ICONS.menu}</button>
    </header>
    <div class="join__grid">
      <section class="qr-card on-light" aria-label="Scan to join">
        <div class="qr-card__head">${watching ? "WATCHING" : "SCAN TO JOIN"}</div>
        <div class="qr-card__body">
          ${qr}
          <div class="qr-card__code"><span class="qr-card__code-value" aria-label="Table code ${e(spacedCode(code))}">${e(code)}</span></div>
          <div class="qr-card__foot">${reach}<span class="qr-card__link">${e(link)}</span></div>
          ${watching ? "" : inviteButtonsHtml()}
        </div>
      </section>
      <div class="join__right">
      ${watching ? "" : gamePickHtml("estate", { cmd: "pick-game", subtitles: false })}
      ${watching ? "" : gamesHtml(ctx.games, { currentCode: state?.code })}
      <section class="players-box" aria-label="Players">
        <div class="seats seats--${max}">${seats.join("")}</div>
        <div class="players-box__foot">
          <span class="players-box__count">${ready} / ${max}</span>
          ${watching ? "" : `<button class="chip-btn${custom ? " is-custom" : ""}" type="button" data-cmd="rules" aria-label="House rules" title="House rules: auctions, loans, starting cash">${ICONS.gear}RULES</button>
          <span class="ai-pick"><button class="chip-btn" type="button" data-cmd="add-ai" ${ready >= max ? "disabled" : ""} aria-label="Add a ${e(aiLevelLabel(ctx.aiLevel))} computer player" title="Add a computer player">${ICONS.cpu}<span>ADD COMPUTER</span></button><button class="chip-btn chip-btn--level" type="button" data-cmd="ai-level" aria-label="Computer level: ${e(aiLevelLabel(ctx.aiLevel))}. Change" title="How well the next computer plays: press to change">${e(upper(aiLevelLabel(ctx.aiLevel)))} <span aria-hidden="true">⇄</span></button></span>
          ${ctx.supportsSeats === false ? "" : `<button class="chip-btn" type="button" data-cmd="seat-keyboard" ${ready >= max ? "disabled" : ""} aria-label="Add a keyboard player" title="Add a player on this keyboard">${ICONS.keyboard}<span>ADD KEYBOARD</span></button>`}
          <button class="ready-btn" type="button" data-cmd="start" ${canStart ? "" : "disabled"} aria-label="${canStart ? `Start the game with ${ready} players` : "Waiting for at least 2 players"}">START ${ICONS.arrow}</button>`}
        </div>
      </section>
      </div>
    </div>`;
}

// ---------------------------------------------------------------------------------------------
// Sound: one button (lobby header, game corner) opens a small menu: everything on/off, music
// on/off with its volume, effects on/off with theirs. `settings` = sound.getSettings().

// settings.locked: the browser is still holding sound back (sound.audioLocked()): the speaker is
// struck through until someone clicks or presses a key on this screen.
export function soundButtonHtml(settings = {}) {
  const off = settings.muted || (!settings.musicOn && !settings.effectsOn);
  return `<button class="icon-btn sound-btn" type="button" data-cmd="sound" aria-haspopup="dialog" aria-label="${e(soundLabel(settings))}" title="Sound and music">${off || settings.locked ? ICONS.speakerOff : ICONS.speaker}</button>`;
}

export function soundLabel(settings = {}) {
  const off = settings.muted || (!settings.musicOn && !settings.effectsOn);
  if (off) return "Sound (off)";
  return settings.locked ? "Sound (click or press any key to turn it on)" : "Sound";
}

// The "click or press any key for sound" pill (table.js renderSoundPill): while the browser holds
// sound back and the sound is meant to be on.
export function soundPillShown(settings = {}) {
  const off = settings.muted || (!settings.musicOn && !settings.effectsOn);
  return Boolean(settings.locked) && !off;
}

export function soundPillHtml() {
  return `${ICONS.speakerOff}<span>CLICK OR PRESS ANY KEY FOR SOUND</span>`;
}

// The bar across the top of the TV while the server can't be reached (table.js renderNetbar).
export function netbarHtml() {
  return `<span class="netbar__spin" aria-hidden="true"></span><strong>RECONNECTING…</strong><span>The game is safe on the server and carries on when it's back.</span>`;
}

export function soundMenuHtml(settings = {}) {
  const pct = (value) => Math.round((Number(value) || 0) * 100);
  const toggle = (key, icon, label, on, disabled = false) => `<button type="button" class="sound-toggle${on ? " is-on" : ""}" data-sound="${key}" aria-pressed="${on}" ${disabled ? "disabled" : ""}>${ICONS[icon]}<span>${label}</span><b>${on ? "ON" : "OFF"}</b></button>`;
  const slider = (key, label, value, disabled) => `<input class="sound-slider" type="range" min="0" max="100" step="5" value="${pct(value)}" data-volume="${key}" aria-label="${label} volume" ${disabled ? "disabled" : ""}>`;
  const all = !settings.muted;
  return `<div class="sound-menu__card on-light" role="dialog" aria-label="Sound">
    ${toggle("all", all ? "speaker" : "speakerOff", "SOUND", all)}
    <div class="sound-menu__row">${toggle("music", "note", "MUSIC", Boolean(settings.musicOn), !all)}${slider("music", "Music", settings.music, !all || !settings.musicOn)}</div>
    <div class="sound-menu__row">${toggle("effects", "bell", "EFFECTS", Boolean(settings.effectsOn), !all)}${slider("effects", "Effects", settings.effects, !all || !settings.effectsOn)}</div>
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// ACTIVE GAMES (lobby): every other game on this server (GET /api/games), so a game started from a
// phone can be put on this screen without typing its code. One row each: the host's token and
// name, everyone's token discs, LOBBY · 3/6 or TURN 14, and SHOW HERE (only when no table screen
// shows it). Nothing at all when there are no other games.

export function gameStatus(game) {
  const players = game?.players?.length || 0;
  if (game?.status === "lobby") return `LOBBY · ${players}/${players + (game.openSeats || 0)}`;
  if (game?.status === "playing") return Number.isInteger(game.turn) && game.turn > 0 ? `${gameTypeOf(game) === "poker" ? "HAND" : "TURN"} ${game.turn}` : "PLAYING"; // Ludo counts turns too
  return "OVER";
}

export function otherGames(games, currentCode = null) {
  return (games || []).filter((game) => game?.code && game.code !== currentCode && game.status !== "finished");
}

export function gamesHtml(games, { currentCode = null } = {}) {
  const list = otherGames(games, currentCode);
  if (!list.length) return "";
  const rows = list.map((game) => {
    const players = game.players || [];
    const pseudo = { players };
    const host = game.host?.name ? game.host : players[0] || null;
    const hostKind = host?.tokenKind || players.find((player) => player.name === host?.name)?.tokenKind || null;
    const hostToken = hostKind ? tokenSvg(hostKind, host?.color || null, { size: 40 }) : `<span class="game-row__nohost">${ICONS.phone}</span>`;
    const discs = players.map((player) => `<span class="game-row__disc${player.online === false && player.input !== "ai" ? " is-away" : ""}" title="${e(player.name)}">${playerChipHtml(pseudo, player, { size: 26, glyph: true })}</span>`).join("");
    const busy = Boolean(game.tableOnline);
    const action = busy
      ? `<button type="button" class="game-row__go is-busy" disabled aria-label="Table ${e(spacedCode(game.code))} is on a screen">${ICONS.screen}ON A SCREEN</button>`
      : `<button type="button" class="game-row__go" data-game-code="${e(game.code)}" aria-label="Show table ${e(spacedCode(game.code))} on this screen">${ICONS.screen}SHOW HERE</button>`;
    return `<li class="game-row${busy ? " is-busy" : ""}" data-code="${e(game.code)}">
      <span class="game-row__host">${hostToken}</span>
      <span class="game-row__name">${e(upper(host?.name || game.code))}</span>${gtypeTag(gameTypeOf(game))}
      <span class="game-row__discs">${discs}</span>
      <span class="game-row__status${game.status === "playing" ? " is-playing" : ""}">${e(gameStatus(game))}</span>
      ${action}
    </li>`;
  }).join("");
  return `<section class="games on-dark" aria-label="Active games">
    <h2 class="games__title">ACTIVE GAMES${list.length > 3 ? `<span class="games__count">${list.length}</span>` : ""}<small class="games__note">Only your games and public ones show here</small></h2>
    <ul class="games__list${list.length > 3 ? " is-long" : ""}">${rows}</ul>
  </section>`;
}

// House rules from the PC lobby (the table is the host). Each press sends set_rules at once. The
// "choose your dice" mode is deliberately not offered: there is no dice picker (review P0-2).
export const CASH_STEPS = [500, 1000, 1500, 2000, 2500, 3000, 4000, 5000];

export function rulesOverlayHtml(state) {
  const rules = rulesOf(state);
  const toggle = (key, icon, label, on) => `<button type="button" class="rule-row${on ? " is-on" : ""}" data-rule="${key}" aria-pressed="${on}">
      <span class="rule-row__icon">${ICONS[icon]}</span><span class="rule-row__label">${label}</span><span class="rule-row__state">${on ? "ON" : "OFF"}</span>
    </button>`;
  const at = CASH_STEPS.indexOf(rules.startingCash);
  return `<div class="dialog on-light" role="dialog" aria-modal="true" aria-labelledby="rules-title">
    <div class="dialog__head" id="rules-title">${ICONS.gear}HOUSE RULES</div>
    <div class="dialog__body">
      ${toggle("auctions", "hammer", "AUCTIONS", rules.auctions)}
      ${toggle("loans", "loan", "LOANS", rules.loans)}
      ${toggle("freeParkingJackpot", "coin", "CITY PARK JACKPOT", rules.freeParkingJackpot)}
      <div class="rule-row rule-row--cash"><span class="rule-row__icon">${ICONS.coin}</span><span class="rule-row__label">START</span>
        <button type="button" class="step-btn" data-rule-cash="-1" ${at === 0 ? "disabled" : ""} aria-label="Less starting cash">−</button>
        <span class="rule-row__state">${money(rules.startingCash)}</span>
        <button type="button" class="step-btn" data-rule-cash="1" ${at === CASH_STEPS.length - 1 ? "disabled" : ""} aria-label="More starting cash">+</button>
      </div>
      <div class="dialog__actions"><button type="button" class="big-btn big-btn--primary" data-cmd="close" data-autofocus>DONE</button></div>
    </div>
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// Gate: full-screen messages for getting back to a table (moved away, open elsewhere, typed code).
// kind: "moved" | "busy" | "resume". Buttons carry data-cmd (table.js gateCommand).

export function gateHtml(gate = {}) {
  const code = gate.code || "";
  const codeHtml = code ? `<div class="gate__code"><span class="gate__code-label">TABLE</span><span class="gate__code-value" aria-label="Table code ${e(spacedCode(code))}">${e(code)}</span></div>` : "";
  const button = (cmd, label, primary = false) => `<button type="button" class="big-btn${primary ? " big-btn--primary" : ""}" data-cmd="${cmd}">${label}</button>`;
  let icon = ICONS.screen;
  let title = "";
  let body = "";
  let actions = "";
  if (gate.kind === "moved") {
    title = "THIS TABLE MOVED TO ANOTHER SCREEN";
    body = codeHtml;
    actions = button("gate-take", `${ICONS.resume}TAKE IT BACK`, true) + button("new-table", "NEW TABLE");
  } else if (gate.kind === "busy") {
    title = "OPEN ON ANOTHER SCREEN";
    body = `${codeHtml}<p class="gate__note">Close it there · or wait 20 s</p>`;
    actions = button("gate-retry", `${ICONS.resume}RETRY`, true) + (gate.canCancel ? button("gate-cancel", "CANCEL") : button("new-table", "NEW TABLE"));
  } else {
    icon = ICONS.resume;
    title = "RESUME A TABLE";
    body = resumeFormHtml({ code, error: gate.error || "", compact: false });
    actions = `${gate.canCancel ? button("gate-cancel", "CANCEL") : ""}${button("new-table", "NEW TABLE")}`;
  }
  const error = gate.kind !== "resume" && gate.kind !== "busy" && gate.error ? `<p class="gate__error" role="alert">${e(gate.error)}</p>` : "";
  return `<div class="gate__card on-light" role="alertdialog" aria-modal="true" aria-labelledby="gate-title" data-kind="${e(gate.kind || "resume")}">
    <div class="gate__icon">${icon}</div>
    <h1 class="gate__title" id="gate-title">${e(title)}</h1>
    ${body}
    ${error}
    <div class="gate__actions">${actions}</div>
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// Game screen: header code, plates

// The table code in the header, big enough to read and type on another screen.
export function tableCodeHtml(code) {
  if (!code) return "";
  return `<span class="table-code__label">TABLE</span><span class="table-code__value" aria-label="Table code ${e(spacedCode(code))}">${e(code)}</span>`;
}

// One plate per player: token, name, cash, TURN, deeds owned (full sets as colour bars) and the
// few states that matter at a glance (jail, a loan, offline).
export function platesHtml(state, ctx = {}) {
  const auction = state.phase === "auction";
  const waiting = waitingOnId(state);
  return state.players.map((player) => {
    const isTurn = !auction && player.id === state.currentPlayerId && state.status === "playing";
    const winner = state.status === "finished" && state.winnerId === player.id;
    let flag = "";
    if (player.bankrupt) flag = '<span class="flag flag--out">OUT</span>';
    else if (winner) flag = `<span class="flag flag--turn flag--icon">${ICONS.crown}<span class="sr-only">Winner</span></span>`;
    else if (isTurn) flag = '<span class="flag flag--turn">TURN</span>';
    const deeds = ownedBy(state, player.id).length;
    const sets = fullSets(state, player.id).map((group) => `<span class="plate__set" style="background:${GROUP_COLORS[group]}" title="Full ${colourWord(group)} set"></span>`).join("");
    const input = inputOf(player);
    const device = deviceOf(state, player, ctx);
    const meta = [];
    if (deeds) meta.push(`<span class="meta meta--deeds" title="${deeds} deed${deeds === 1 ? "" : "s"}">${ICONS.deed}<b>${deeds}</b><span class="sr-only"> deeds</span></span>`);
    if (sets) meta.push(`<span class="plate__sets">${sets}</span>`);
    if (player.inJail) meta.push(`<span class="meta meta--jail" title="In jail">${ICONS.lock}<span class="sr-only">In jail</span></span>`);
    // Away: a phone/browser seat that dropped off. The server may let the computer play for them
    // (players[].autopilot); a missing field means no.
    const offline = player.online === false && input !== "ai" && !player.bankrupt;
    const autopilot = Boolean(player.autopilot) && !player.bankrupt;
    if (offline) meta.push(`<span class="meta meta--off" title="Offline">${ICONS.wifiOff}<span class="sr-only">Offline</span></span>`);
    // A computer's level (review U5: it showed only in the lobby).
    if (input === "ai" && player.difficulty && !player.bankrupt) meta.push(`<span class="meta meta--level meta--${e(player.difficulty)}">${e(upper(aiLevelLabel(player.difficulty)))}</span>`);
    const bot = autopilot ? `<span class="plate__bot" title="The computer is playing for ${e(player.name)}">${ICONS.robot}<span class="sr-only">Computer playing</span></span>` : "";
    // Tabs beside TURN (UX-02, UX-04, UX-07): the computer plays for a person, the game has waited
    // on them a while (from the log's times), or they owe money now.
    const tags = [];
    if (!player.bankrupt && state.status === "playing") {
      if (autopilot && isPerson(player)) tags.push('<span class="plate__tag plate__tag--bot">COMPUTER</span>');
      else if (player.skipped) tags.push('<span class="plate__tag plate__tag--bot">SKIPPING</span>');
      const idle = idleShown(state, player.id, ctx.now ?? Date.now());
      if (idle !== null && !autopilot) tags.push(`<span class="plate__tag plate__tag--idle">IDLE ${minutesText(idle)}</span>`);
      if (state.phase === "debt" && state.debt?.payerId === player.id) tags.push(`<span class="plate__tag plate__tag--owes">OWES ${money(state.debt.amount)}</span>`);
    }
    // Compact plate for the left rail: token, name over cash, a row of markers; TURN as a tab.
    return `<div class="plate${isTurn ? " is-turn" : ""}${waiting === player.id ? " is-waiting" : ""}${player.bankrupt ? " is-out" : ""}${offline ? " is-offline" : ""}${autopilot ? " is-autopilot" : ""}" data-player-id="${e(player.id)}" data-key="${e(player.id)}" style="--pc:${playerColor(state, player)}">
      <span class="plate__token">${playerTokenSvg(state, player, { size: 76 })}${bot}</span>
      <div class="plate__main">
        <span class="plate__name">${e(upper(player.name))}</span><span class="sr-only">${e(device)}</span>
        <div class="plate__amount" data-cash="${player.cash}">${money(player.cash)}</div>
        ${meta.length ? `<div class="plate__meta">${meta.join("")}</div>` : ""}
      </div>
      ${tags.length ? `<span class="plate__tags${flag && !player.bankrupt ? " plate__tags--flag" : ""}">${tags.join("")}</span>` : ""}
      ${flag}
    </div>`;
  }).join("");
}

// ---------------------------------------------------------------------------------------------
// The "now" card: whose turn and what is happening or must be decided, in big type with icons.

function actionRow(seat, button, key, label, detail, act, extra = {}) {
  return { ...glyphFor(seat, button, key), label, detail, act, button, key, ...extra };
}

function bidAmount(state, ctx, player) {
  const current = state.auction?.currentBid || 0;
  const minimum = current + 1;
  const suggested = ctx?.bid && ctx.bid.key === `${state.auction?.spaceIndex}:${current}` ? ctx.bid.amount : Math.max(minimum, Math.ceil((current + 10) / 10) * 10);
  return Math.max(minimum, Math.min(suggested, player?.cash ?? suggested));
}

function sentenceCase(text) {
  return String(text || "").toLowerCase().replace(/^\w/, (c) => c.toUpperCase());
}

// The one key map (UX-01): the same buttons, the same words and the same order on a controller, the
// keyboard and the phones. Pad A, or A / Enter on the keyboard: the main action. X: the second.
// Y: the third. B, or B / Esc: back (pass, not yet, close). View ⧉ / V: my deeds. Menu ☰ / M: the
// menu. A phone shows the same buttons top to bottom in this order.
export const KEY_MAP = [
  { button: "A", keys: ["A", "↵"], name: "MAIN", does: "Roll · Buy · Pay · Bid · End turn", phone: "BUY $240", tone: "gold" },
  { button: "X", keys: ["X"], name: "SECOND", does: "Auction · Build · Pay $50", phone: "AUCTION", tone: "cream" },
  { button: "Y", keys: ["Y"], name: "THIRD", does: "Trade · Use pass", phone: "TRADE TAB", tone: "tab", icon: "swap" },
  { button: "B", keys: ["B", "Esc"], name: "BACK", does: "Pass · Not yet · Close", phone: "PASS", tone: "danger" },
  { button: "VIEW", keys: ["V"], name: "DEEDS", does: "My deeds", phone: "DEEDS TAB", tone: "tab", icon: "views" },
  { button: "START", keys: ["M"], name: "MENU", does: "Sound · Rules · End game", phone: "MORE TAB", tone: "tab", icon: "menu" },
];

// The keyboard letter for each pad button.
export const KEY_FOR = { A: "A", X: "X", Y: "Y", B: "B", VIEW: "V", START: "M" };

// Which pad button a timed auction's quick bid sits on: +$10 A, +$50 X, +$100 Y.
const QUICK_BUTTON = { 10: "A", 50: "X", 100: "Y" };

// Buttons for one local (keyboard/controller) seat, in the key map's order. Labels are one or two
// words: the who and the why are already at the top of the card. The keyboard key is the pad
// button's letter (row.key), except bankruptcy, which keeps its own Q so no back or pass key can
// ever press it.
function seatRows(state, seat, ctx, { named = false } = {}) {
  const player = playerById(state, seat.playerId);
  const rows = [];
  const act = (action) => ({ seatId: seat.seatId, action });
  const add = (button, label, detail, action, extra = {}) => rows.push(actionRow(seat, button, extra.key || KEY_FOR[button], label, detail, action, extra));
  if (!player) return rows;
  const who = named ? ` · ${player.name}` : "";
  const deeds = () => add("VIEW", "Deeds", "", null, { cmd: "property", icon: "deed" });
  switch (state.phase) {
    case "determining_order":
      add("A", `Roll${who}`, "", act({ type: "roll_for_order" }), { icon: "dice" });
      break;
    case "auction": {
      if (isTimed(state.auction)) {
        // Open bidding on the clock: one press bids +$10 / +$50 / +$100 over the top bid; a step
        // this player can't afford isn't offered.
        if (!canBid(state, player.id)) break;
        for (const bid of quickBids(state, player.id)) {
          add(QUICK_BUTTON[bid.step], `${money(bid.amount)}${who}`, `+$${bid.step}`, act({ type: "bid", amount: bid.amount }), { bid: bid.amount, quick: bid.step });
        }
        add("B", "Pass", "", act({ type: "pass_auction" }));
        break;
      }
      const amount = bidAmount(state, ctx, player);
      const can = amount <= player.cash;
      add("A", `Bid ${money(amount)}${who}`, "", can ? act({ type: "bid", amount }) : null, { bid: amount, icon: "hammer" });
      add("B", "Pass", "", act({ type: "pass_auction" }));
      break;
    }
    case "trade":
      if (state.trade?.targetId === player.id) {
        add("A", `Accept${who}`, "", act({ type: "accept_trade" }), { icon: "check" });
        add("B", "Decline", "", act({ type: "reject_trade" }));
      } else if (state.trade?.proposerId === player.id) {
        add("B", `Cancel offer${who}`, "", act({ type: "reject_trade" }));
      }
      break;
    case "loan_offer": {
      const offer = state.loanOffer;
      if (offer?.targetId === player.id) {
        const lending = offerParties(offer).lenderId === player.id;
        add("A", `${lending ? "Lend" : "Take"} ${money(offer.principal)}${who}`, "", act({ type: "accept_loan" }), { icon: "check" });
        add("B", "Decline", "", act({ type: "reject_loan" }));
      } else if (offer?.proposerId === player.id) {
        add("B", `Cancel offer${who}`, "", act({ type: "reject_loan" }));
      }
      break;
    }
    case "card_confirmation": {
      const preview = cardPreview(state, player.id, state.pendingCard?.cardId);
      // "Move to west station RR": street words shortened so the button never cuts the name off.
      const label = sentenceCase(preview.action.label).replace(/\brailroad\b/i, "RR").replace(/\bstreet\b/i, "St").replace(/\bavenue\b/i, "Ave").replace(/\broad\b/i, "Rd");
      add("A", label, "", act({ type: "confirm_card" }));
      break;
    }
    case "debt": {
      // PAY lights up once cash plus the ticked checklist rows cover the debt (UX-04); pressing it
      // runs the ticked sells and mortgages, then pays.
      const owed = state.debt.amount;
      const list = debtChecklist(state, player.id, ctx?.debtPicks?.[debtKey(state)] || null);
      if (player.cash >= owed) add("A", `Pay ${money(owed)}`, "", act({ type: "pay_debt" }), { icon: "coin" });
      else if (list.covered) add("A", `Pay ${money(owed)}`, "", null, { cmd: "apply-plan", plan: list, icon: "coin" });
      else add("A", `Pay ${money(owed)}`, `Find ${money(list.toFind)}`, null, { icon: "coin", off: true });
      if (player.cash < owed && rulesOf(state).loans) add("X", "Loan", "", null, { cmd: "loan", icon: "loan" });
      add("Y", "Go bankrupt", "", null, { cmd: "bankrupt", icon: "out", key: "Q" });
      break;
    }
    case "awaiting_roll":
      if (player.inJail) {
        add("A", "Roll", "", act({ type: "roll" }), { icon: "dice" });
        add("X", `Pay ${money(50)}`, "", player.cash >= 50 ? act({ type: "pay_bail" }) : null, { icon: "lock" });
        if (player.jailCards > 0) add("Y", "Use pass", `×${player.jailCards}`, act({ type: "use_jail_card" }), { icon: "card" });
      } else {
        add("A", state.turn?.doublesCount ? "Roll again" : "Roll", "", act({ type: "roll" }), { icon: "dice" });
        const build = buildOptions(state, player.id).filter((option) => option.affordable && option.bankOk).sort((a, b) => b.delta - a.delta)[0];
        if (build) add("X", "Build", money(build.cost), act({ type: "build", spaceIndex: build.index }), { icon: "house" });
        add("Y", "Trade", "", null, { cmd: "trade", icon: "swap" });
      }
      deeds();
      break;
    case "awaiting_purchase": {
      const space = state.board[state.pendingPurchase];
      const can = player.cash >= space.price;
      add("A", `Buy ${money(space.price)}`, "", can ? act({ type: "buy" }) : null, { icon: "stamp" });
      if (rulesOf(state).auctions) add("X", "Auction", "", act({ type: "decline" }), { icon: "hammer" });
      else add("B", "Pass", "", act({ type: "decline" }));
      deeds();
      break;
    }
    case "turn_complete":
      add("A", "End turn", "", act({ type: "end_turn" }), { icon: "check" });
      add("Y", "Trade", "", null, { cmd: "trade", icon: "swap" });
      deeds();
      break;
    default:
      break;
  }
  return rows.map((row) => ({ ...row, seatId: seat.seatId, playerId: seat.playerId }));
}

// The quick bids a seat that leads would have, greyed: the card keeps its size while it leads.
function leaderRows(state, seat, player) {
  const current = Number(state.auction?.currentBid) || 0;
  const rows = QUICK_STEPS.filter((step) => current + step <= player.cash)
    .map((step) => actionRow(seat, QUICK_BUTTON[step], KEY_FOR[QUICK_BUTTON[step]], money(current + step), `+$${step}`, null, { quick: step }));
  rows.push(actionRow(seat, "B", "B", "Pass", "", null));
  return rows.map((row) => ({ ...row, seatId: seat.seatId, playerId: seat.playerId }));
}

// Which debt the checklist's ticks belong to: a new debt (or a new amount) starts from the plan.
export function debtKey(state) {
  const debt = state?.debt;
  return debt ? `${debt.payerId}:${debt.creditorId || "bank"}:${debt.amount}` : "";
}

// The two sides of a trade (proposer gives / target gives).
export function dealSides(state, trade) {
  return {
    give: { player: playerById(state, trade.proposerId), cash: trade.offer?.cash || 0, properties: trade.offer?.properties || [] },
    get: { player: playerById(state, trade.targetId), cash: trade.request?.cash || 0, properties: trade.request?.properties || [] },
  };
}

function trayItems(state, side) {
  const items = side.properties.map((index) => {
    const space = state.board[index];
    return `<span class="tray-deed" data-deed-info="${index}" style="--gc:${spaceColour(space)}">${e(shortName(space?.name || ""))}</span>`;
  });
  if (side.cash) items.push(`<span class="tray-cash">${money(side.cash)}</span>`);
  return items.join("");
}

// A person's offer on the TV, readable from the sofa (review U4): who gives what, by name, and the
// set it would make.
export function dealSlotHtml(state, trade) {
  const { give, get } = dealSides(state, trade);
  const side = (pile) => `<div class="slot-deal__side">
      <span class="slot-deal__who">${chip(state, pile.player, 30)}<b>${e(upper(pile.player?.name || ""))}</b><small>GIVES</small></span>
      <div class="deal__items">${trayItems(state, pile) || '<span class="tray-none">NOTHING</span>'}</div>
    </div>`;
  const sets = setChanges(state, trade).filter((change) => change.gain);
  return `<div class="slot-deal" aria-label="Trade offer">
    <span class="slot-deal__head">${ICONS.swap}TRADE OFFER</span>
    ${side(give)}${side(get)}
    ${sets.length ? `<div class="slot-deal__sets">${setPills(state, sets)}</div>` : ""}
  </div>`;
}

// Colour sets that change hands in a trade: [{ playerId, group, gain }].
export function setChanges(state, trade) {
  const after = { ...state, properties: { ...state.properties } };
  for (const index of trade.offer?.properties || []) after.properties[index] = { ...propState(state, index), ownerId: trade.targetId };
  for (const index of trade.request?.properties || []) after.properties[index] = { ...propState(state, index), ownerId: trade.proposerId };
  const groups = [...new Set((state.board || []).filter((space) => space.type === "property").map((space) => space.group))];
  const changes = [];
  for (const playerId of [trade.proposerId, trade.targetId]) {
    for (const group of groups) {
      const before = ownsGroup(state, playerId, group);
      const later = ownsGroup(after, playerId, group);
      if (before !== later) changes.push({ playerId, group, gain: later });
    }
  }
  return changes;
}

// A set changing hands as a pill: whose chip, the colour, + or − SET. With `youId` it is toned for
// that player (green: good for you), otherwise green means "gains a set".
function setPills(state, changes, youId = null) {
  return changes.map((change) => {
    const good = youId ? (change.playerId === youId) === change.gain : change.gain;
    const who = playerById(state, change.playerId);
    return `<span class="set-pill set-pill--${good ? "good" : "warn"}" title="${e(`${who?.name || ""} ${change.gain ? "gets" : "loses"} the ${colourWord(change.group)} set`)}">${chip(state, who, 22)}<span class="set-pill__swatch" style="background:${GROUP_COLORS[change.group]}"></span><b>${change.gain ? "+" : "−"}SET</b></span>`;
  }).join("");
}

// What the current player's landing did, as one short line for the action card at END TURN:
// "PAID $80 → (owner)", "TAX $200", "BOUGHT $400", "IN JAIL". Empty when nothing worth saying.
export function landingLine(state) {
  const landing = state?.lastLanding;
  if (!landing || landing.playerId !== state.currentPlayerId) return null;
  const recipient = playerById(state, landing.recipientId);
  switch (landing.kind) {
    case "rent":
      return { verb: `PAID ${money(landing.amount)}`, icon: "coin", html: recipient ? `${ICONS.arrow}${chip(state, recipient, 30)}` : "" };
    case "tax":
      return { verb: `TAX ${money(landing.amount)}`, icon: "coin", html: "" };
    case "purchase":
      return propState(state, landing.spaceIndex)?.ownerId === landing.playerId ? { verb: `BOUGHT ${money(landing.amount)}`, icon: "stamp", html: "" } : null;
    case "goToJail":
      return { verb: "IN JAIL", icon: "lock", html: "" };
    default:
      return null;
  }
}

// The action card's model: { band, actorHtml, name, verb, verbIcon, line, wait, badges, chips,
// legend, rows }. `verb` + `line` make the one line under the name ("BID? $60 👑(T)"). Rows with
// `act`/`cmd` are buttons for local seats; everything else is a chip, a number or an icon.
export function panelModel(state, ctx = {}) {
  const model = nowModel(state, ctx);
  const standIn = state?.phase === "finished" ? null : standInPlayer(state);
  if (standIn) {
    model.standin = standInCardHtml(state, standIn);
    model.wait = "robot";
  }
  // A keyboard seat with buttons: its keys beside the pad letters (UX-01, KEYBOARD SEAT).
  const keyboard = (model.rows || []).some((row) => row.seatId && (state.localSeats || []).find((seat) => seat.seatId === row.seatId)?.input === "keyboard");
  return keyboard ? { ...model, keymap: true } : model;
}

function nowModel(state, ctx = {}) {
  const current = playerById(state, state.currentPlayerId);
  const local = actingSeats(state);
  const rowsFor = (seats, options) => seats.flatMap((seat) => seatRows(state, seat, ctx, options));
  const base = { band: "#F2C14E", actorHtml: "", name: "", verb: "", verbIcon: "", line: "", wait: "", big: "", badges: [], chips: "", legend: [], rows: [] };
  const actor = (player) => ({ actorHtml: player ? playerTokenSvg(state, player, { size: 64 }) : "", name: player ? upper(player.name) : "" });
  // Someone off this screen (phone, computer, browser) is deciding: their device icon and dots.
  // An offline player shows the robot when the computer stands in for them, else the dropped link.
  const waitFor = (player) => {
    if (!player || seatFor(state, player.id)) return "";
    if (player.autopilot) return "robot";
    if (player.online === false && inputOf(player) !== "ai") return "wifiOff";
    return DEVICE_ICON[inputOf(player)] || "browser";
  };
  const amount = (value) => `<b class="now-amt">${money(value)}</b>`;

  if (state.phase === "finished") {
    const winner = playerById(state, state.winnerId);
    const chips = standings(state).map((entry) => `<span class="now-chip${entry.player.bankrupt ? " is-out" : ""}"><span class="now-chip__place">${e(entry.placeLabel.toUpperCase())}</span>${chip(state, entry.player, 24)}<b>${entry.player.bankrupt ? "OUT" : money(entry.worth)}</b></span>`).join("");
    return {
      ...base, ...actor(winner), verb: "WINS", verbIcon: "crown", chips,
      rows: ctx.watching ? [] : [{ glyph: "↵", glyphBg: G.KEY, label: "Play again", detail: "", cmd: "rematch", icon: "resume" }],
    };
  }
  if (state.phase === "determining_order" || state.phase === "order_countdown") {
    const order = state.openingOrder;
    const settled = state.phase === "order_countdown";
    const chips = settled
      ? (order?.order || []).map((id, index) => `<span class="now-chip"><span class="now-chip__place">${index + 1}</span>${chip(state, playerById(state, id), 24)}</span>`).join("")
      // First rolls for everyone; the tied players' tie-break rolls after theirs ("2 · 5").
      : openingRows(state).map((row) => `<span class="now-chip${row.pending ? " is-pending" : ""}${row.tied ? " is-tied" : ""}">${chip(state, playerById(state, row.playerId), 24)}<b>${e(row.shown)}</b></span>`).join("");
    const tie = settled ? null : openingTie(state);
    const verb = settled ? "ORDER" : tie ? (tie.totals.length === 1 ? `TIED ON ${tie.totals[0]}` : "TIE-BREAK") : "HIGH ROLL STARTS";
    return { ...base, verb, verbIcon: "dice", chips, rows: rowsFor(local, { named: local.length > 1 }) };
  }
  if (state.phase === "auction" && isTimed(state.auction)) {
    // Timed open bidding (UX-03): the deed, the clock and who leads are on the board's centre
    // (stageHtml); the card has the bidder, the clock's two pills, the quick bids and CASH / MAX.
    const auction = state.auction;
    const inSeats = (state.localSeats || []).filter((seat) => auction.activeIds.includes(seat.playerId));
    const one = inSeats.length === 1 ? playerById(state, inSeats[0].playerId) : null;
    const leader = playerById(state, auction.bidderId);
    const leads = one && one.id === auction.bidderId;
    let rows = rowsFor(local, { named: local.length > 1 });
    // A seat here that leads keeps its buttons, greyed: it can't bid on itself or pass.
    if (!rows.length && leads) {
      rows = leaderRows(state, inSeats[0], one).map((row) => ({ ...row, act: null, cmd: null, button: row.button || true }));
    }
    const who = one ? actor(one) : leader && !inSeats.length ? actor(leader) : { actorHtml: "", name: "" };
    return {
      ...base, ...who, band: "#E97A1F",
      verb: leads ? "YOU LEAD" : one ? "BID" : leader && !inSeats.length ? "LEADS" : "AUCTION",
      verbIcon: leads ? "crown" : "hammer",
      line: one || (leader && !inSeats.length) ? "" : amount(auction.currentBid),
      pills: ["OPEN BIDDING", `${Math.round(auction.clockMs / 1000)} S CLOCK`],
      rows,
      foot: one ? { cash: one.cash, max: one.cash } : null,
      timed: true,
    };
  }
  if (state.phase === "auction") {
    const auction = state.auction;
    const space = state.board[auction.spaceIndex];
    const bidder = playerById(state, auction.currentPlayerId);
    const leader = playerById(state, auction.bidderId);
    const lastBids = new Map();
    for (const entry of state.log || []) {
      const match = entry.text.match(/^(.+) bid \$(\d+)$/);
      if (match) {
        const player = state.players.find((candidate) => candidate.name === match[1]);
        if (player) lastBids.set(player.id, Number(match[2]));
      }
      if (entry.text === `${space.name} entered an open auction`) lastBids.clear();
    }
    const chips = state.players.filter((player) => !player.bankrupt).map((player) => {
      const active = auction.activeIds.includes(player.id);
      const leads = player.id === auction.bidderId;
      const bid = leads ? auction.currentBid : lastBids.get(player.id);
      return `<span class="now-chip${active ? "" : " is-out"}${leads ? " is-lead" : ""}${player.id === auction.currentPlayerId ? " is-pending" : ""}">${leads ? ICONS.crown : ""}${chip(state, player, 24)}<b>${active ? (bid ? money(bid) : "–") : "OUT"}</b></span>`;
    }).join("");
    const pads = (state.localSeats || []).some((seat) => seat.input === "controller" && auction.activeIds.includes(seat.playerId));
    const keys = (state.localSeats || []).some((seat) => seat.input === "keyboard" && auction.activeIds.includes(seat.playerId));
    // A seat here still in the auction keeps its BID / PASS rows (greyed) while someone else bids,
    // so the card doesn't shrink and grow with every bid (it jumped up and down twice a second
    // in a fast auction against a computer).
    const waiting = local.length ? null : (state.localSeats || []).find((seat) => auction.activeIds.includes(seat.playerId));
    const rows = local.length ? rowsFor(local) : waiting
      ? seatRows({ ...state, auction: { ...auction, currentPlayerId: waiting.playerId } }, waiting, ctx).map((row) => ({ ...row, act: null, cmd: null, button: row.button || true }))
      : [];
    const legend = [];
    if (pads && rows.length) legend.push({ glyph: "RB", bg: G.MENU, label: "+$10" }, { glyph: "RT", bg: G.MENU, label: "+$50" });
    if (keys && rows.length) legend.push({ glyph: "+/−", bg: G.KEY, label: "$10" });
    return {
      ...base, ...actor(bidder), band: "#E97A1F", verb: "BID?", verbIcon: "hammer", wait: waitFor(bidder),
      line: `${amount(auction.currentBid)}${leader ? `${ICONS.crown}${chip(state, leader, 26)}` : ""}`,
      chips, legend, rows,
    };
  }
  if (state.phase === "loan_offer" && state.loanOffer) {
    const offer = state.loanOffer;
    const parties = offerParties(offer);
    const target = playerById(state, offer.targetId);
    return {
      ...base, ...actor(target), band: "#A97E1E", verb: "LOAN?", verbIcon: "loan", wait: waitFor(target),
      line: `${chip(state, playerById(state, parties.lenderId), 26)}${amount(offer.principal)}${ICONS.arrow}${chip(state, playerById(state, parties.borrowerId), 26)}<span class="now-sub">${ICONS.resume}${money(offer.repay)}</span>`,
      rows: rowsFor(local),
    };
  }
  if (state.phase === "trade" && state.trade && !botDeal(state)) {
    const target = playerById(state, state.trade.targetId);
    const proposer = playerById(state, state.trade.proposerId);
    return {
      ...base, ...actor(target), verb: "TRADE?", verbIcon: "swap", wait: waitFor(target),
      line: `${chip(state, proposer, 26)}<span class="now-swap">${ICONS.swap}</span>${chip(state, target, 26)}`,
      chips: setPills(state, setChanges(state, state.trade)),
      rows: rowsFor(local),
    };
  }
  if (!current) return { ...base, verb: "…" };

  const model = { ...base, ...actor(current), band: playerColor(state, current) || "#F2C14E", wait: waitFor(current), rows: rowsFor(local) };
  switch (state.phase) {
    case "awaiting_roll": {
      model.verb = current.inJail ? "IN JAIL" : state.turn?.doublesCount ? "DOUBLES · ROLL AGAIN" : "ROLL";
      model.verbIcon = current.inJail ? "lock" : "dice";
      if (current.inJail) model.badges.push({ icon: "lock", text: `${Math.min(3, current.jailTurns + 1)}/3`, tone: "jail" });
      // Doubles: another roll; the dots count them (a third double in a row goes to jail).
      if (state.turn?.doublesCount) model.badges.push({ icon: "dice", text: `DOUBLES ${"●".repeat(state.turn.doublesCount)}${"○".repeat(Math.max(0, 3 - state.turn.doublesCount))}`, tone: "gold" });
      const heat = nextRollHeat(state, current.id);
      // The most this roll could cost (rent or tax within reach), in plain words (review U3).
      if (heat.alert) model.badges.push({ icon: "warn", text: `MAY PAY ${money(heat.alert)}`, tone: heat.alert > current.cash ? "danger" : "warn" });
      break;
    }
    case "awaiting_purchase": {
      const space = state.board[state.pendingPurchase];
      model.verb = "BUY?";
      model.verbIcon = "stamp";
      model.band = spaceColour(space);
      model.line = space ? amount(space.price) : "";
      if (space && current.cash < space.price) model.badges.push({ icon: "warn", text: `SHORT ${money(space.price - current.cash)}`, tone: "danger" });
      break;
    }
    case "card_confirmation": {
      const chest = state.pendingCard?.deck === "chest";
      model.verb = chest ? "POST" : "HEADLINES";
      model.verbIcon = chest ? "card" : "chance";
      model.band = chest ? "#2F6FE0" : "#F08A2B";
      break;
    }
    case "debt": {
      const creditor = playerById(state, state.debt.creditorId);
      const short = Math.max(0, state.debt.amount - current.cash);
      model.verb = "OWES";
      model.verbIcon = "warn";
      model.band = "#E8232A";
      model.line = `${amount(state.debt.amount)}${ICONS.arrow}${creditor ? chip(state, creditor, 26) : '<span class="now-bank">BANK</span>'}`;
      if (short > 0) model.badges.push({ icon: "warn", text: `SHORT ${money(short)}`, tone: "danger" });
      // A seat here ticks what to mortgage and sell; PAY lights up once that covers it.
      model.checklist = debtListHtml(state, tableDebtList(state, ctx));
      // PAY takes the whole row, as drawn (UX-04); LOAN and GO BANKRUPT share the next.
      model.wideFirst = true;
      break;
    }
    case "turn_complete": {
      const landed = landingLine(state);
      model.verb = landed?.verb || "END TURN";
      model.verbIcon = landed?.icon || "check";
      model.line = landed?.html || "";
      // A card's double (or ten-times) rent: the sum is on the board's centre, the receipt here.
      model.receipt = rentReceiptHtml(state);
      break;
    }
    default:
      break;
  }
  return model;
}

// The action card (bottom-right). `busy` (animations still playing): the buttons stay in place
// but greyed out and inert, so nothing that belongs to the previous state can be pressed. Buttons
// sit two to a row; with an odd count the first (the main action) takes the whole row.
export function panelHtml(model, { busy = false } = {}) {
  const odd = model.rows.length % 2 === 1;
  // A label (with its detail) too long for half a row ("Roll · Robin", "Raise cash +$180",
  // "Use pass ×2") takes a whole row.
  const long = (row) => String(row.label).length + String(row.detail || "").length > 9;
  const rows = model.rows.map((row, index) => {
    const icon = row.icon && ICONS[row.icon] ? `<span class="panel__icon">${ICONS[row.icon]}</span>` : "";
    const live = Boolean(row.act || row.cmd);
    const wide = model.timed || long(row) || (index === 0 && (odd || model.wideFirst)) ? " panel__row--wide" : "";
    const inner = `<span class="glyph" style="${glyphStyle(row.glyphBg || G.MENU)}">${busy && live ? "…" : e(row.glyph)}</span>${icon}<span class="panel__label">${e(row.label)}</span>${row.detail ? `<span class="panel__detail">${e(row.detail)}</span>` : ""}`;
    if (live && !busy) return `<button class="panel__row panel__row--action${wide}" type="button" data-row="${index}">${inner}</button>`;
    if (row.button || busy) return `<div class="panel__row panel__row--disabled${wide}" aria-disabled="true">${inner}</div>`;
    return `<div class="panel__row${wide}">${inner}</div>`;
  }).join("");
  const badges = (model.badges || []).map((badge) => `<span class="badge badge--${badge.tone || "plain"}">${badge.icon && ICONS[badge.icon] ? ICONS[badge.icon] : ""}${e(badge.text)}</span>`).join("");
  const busyBadge = busy ? `<span class="now__wait now__wait--busy" role="status">${ICONS.dice}<span class="busy-dots" aria-label="Moving"><i></i><i></i><i></i></span></span>` : "";
  const legend = (model.legend || []).map((item) => `<span class="legend__item"><span class="glyph glyph--sm" style="${glyphStyle(item.bg)}">${e(item.glyph)}</span>${e(item.label)}</span>`).join("");
  const pills = (model.pills || []).map((text) => `<span class="now-pill">${e(text)}</span>`).join("");
  const foot = model.foot ? `<div class="now__foot"><span>CASH <b>${money(model.foot.cash)}</b></span><span>MAX <b>${money(model.foot.max)}</b></span></div>` : "";
  const wait = model.wait && !busy ? `<span class="now__wait" role="status">${ICONS[model.wait] || ""}<span class="busy-dots" aria-label="Waiting"><i></i><i></i><i></i></span></span>` : "";
  const lead = model.actorHtml ? `<span class="now__token">${model.actorHtml}</span>` : `<span class="now__icon">${ICONS[model.verbIcon] || ""}</span>`;
  const verbIcon = model.actorHtml && ICONS[model.verbIcon] ? ICONS[model.verbIcon] : "";
  // The name has the whole row (review 4 Nov #5: the busy / waiting chip beside it cut "BLUE WHALE"
  // to "BLUE W…"); the chip ends the verb line instead. A long name gets a smaller size and may
  // take two lines rather than an ellipsis.
  const nameSize = !model.name ? "" : model.name.length > 12 ? " now__name--longer" : model.name.length > 8 ? " now__name--long" : "";
  return `<div class="panel__body now__body">
      <div class="now__head">
        ${lead}
        <div class="now__title">
          ${model.name ? `<h2 class="now__name${nameSize}">${e(model.name)}</h2>` : ""}
          <span class="now__verb">${verbIcon}<span>${e(model.verb)}</span>${model.line || ""}${wait}${busyBadge}</span>
        </div>
      </div>
      ${pills ? `<div class="now__pills">${pills}</div>` : ""}
      ${model.checklist || ""}
      ${model.receipt || ""}
      ${model.standin || ""}
      ${badges ? `<div class="badges">${badges}</div>` : ""}
      ${model.chips ? `<div class="now__chips">${model.chips}</div>` : ""}
      ${legend ? `<div class="legend">${legend}</div>` : ""}
      ${model.keymap ? keymapHtml() : ""}
      ${foot}
      ${rows ? `<div class="panel__rows${busy ? " is-busy" : ""}">${rows}</div>` : ""}
    </div>`;
}

// The KEYBOARD SEAT card: each pad letter and the keys that press it.
export function keymapHtml() {
  const cap = (key) => `<span class="key-cap">${e(key)}</span>`;
  const pad = (button) => `<span class="glyph glyph--sm" style="${glyphStyle(G[button])}">${button === "VIEW" ? "⧉" : button}</span>`;
  const items = KEY_MAP.filter((entry) => entry.button !== "START").map((entry) => `<span class="keymap__item">${pad(entry.button)}=${entry.keys.map(cap).join("")}</span>`).join("");
  return `<div class="keymap"><span class="keymap__title">KEYBOARD SEAT</span><div class="keymap__keys">${items}</div></div>`;
}



// ---------------------------------------------------------------------------------------------
// PLAYERS (UX-02): the host's controls mid-game. The table screen is the host (it sends its
// tableToken). One row per player still in: token, name, device, and ONLINE / IDLE m:ss / COMPUTER;
// the open row has A COMPUTER PLAYS, X SKIP TURN, Y REMOVE (REMOVE asks first).

const DOING = {
  awaiting_roll: "to roll", awaiting_purchase: "to buy", turn_complete: "to end turn", debt: "to pay",
  trade: "to answer", loan_offer: "to answer", card_confirmation: "card", determining_order: "to roll",
};

// What the game waits on this player for ("to roll"), or "".
export function doingWord(state, playerId) {
  return waitedOnIds(state).includes(playerId) ? DOING[state.phase] || "" : "";
}

// The row's tag: [text, tone]. Only from data the screen has: online, autopilot, skipped, the log.
export function hostTag(state, player, now) {
  if (inputOf(player) === "ai") return ["COMPUTER", "bot"];
  if (player.autopilot) return ["COMPUTER", "bot"];
  if (player.skipped) return ["SKIPPING", "bot"];
  const idle = idleShown(state, player.id, now);
  if (idle !== null) return [`IDLE ${minutesText(idle)}`, "idle"];
  if (player.online === false) return ["AWAY", "idle"];
  return ["ONLINE", "on"];
}

// Which row starts open: the person the game waits on longest, else the first person.
export function defaultHostPick(state, now = Date.now()) {
  const inGame = (state?.players || []).filter((player) => !player.bankrupt);
  const waited = inGame.find((player) => isPerson(player) && waitedOnIds(state).includes(player.id) && idleShown(state, player.id, now) !== null);
  return (waited || inGame.find((player) => isPerson(player) && player.id !== state?.hostId) || inGame[0])?.id || null;
}

export function playersOverlayHtml(state, ctx = {}) {
  const now = ctx.now ?? Date.now();
  const open = ctx.open === undefined ? defaultHostPick(state, now) : ctx.open;
  const rows = (state?.players || []).filter((player) => !player.bankrupt).map((player) => {
    const device = { phone: "phone", controller: "controller", keyboard: "keyboard", ai: "computer", classic: "browser" }[inputOf(player)] || "";
    const sub = [device, state.hostId === player.id ? "host" : "", doingWord(state, player.id)].filter(Boolean).join(" · ");
    const [tag, tone] = hostTag(state, player, now);
    const isOpen = player.id === open;
    const can = hostControls(state, player.id);
    const person = isPerson(player);
    const act = (kind, button, icon, label, enabled, extra = "") => `<button type="button" class="host-act host-act--${kind}${extra}" data-host="${kind}" data-arg="${e(player.id)}" ${enabled ? "" : "disabled"}${kind === "standin" && enabled ? " data-autofocus" : ""}><span class="glyph glyph--sm" style="${glyphStyle(G[button])}">${button}</span>${icon ? ICONS[icon] : ""}<span>${label}</span></button>`;
    const actions = isOpen ? `<div class="host-row__acts">${person ? act("standin", "A", "robot", "COMPUTER PLAYS", can.standin, " host-act--main") + act("skip", "X", "", player.skipped ? "SKIPPING" : "SKIP TURN", can.skip) : ""}${act("remove", "Y", "", "REMOVE", can.remove, " host-act--danger")}</div>` : "";
    return `<li class="host-row${isOpen ? " is-open" : ""}">
      <button type="button" class="host-row__pick" data-host-pick="${e(player.id)}" aria-expanded="${isOpen}"${isOpen && !actions.includes("data-autofocus") ? " data-autofocus" : ""}>
        <span class="host-row__token">${playerTokenSvg(state, player, { size: 56 })}</span>
        <span class="host-row__who"><b>${e(upper(player.name))}</b><small>${e(sub)}</small></span>
        <span class="host-tag host-tag--${tone}">${e(tag)}</span>
      </button>
      ${actions}
    </li>`;
  }).join("");
  return `<div class="dialog dialog--players on-light" role="dialog" aria-modal="true" aria-labelledby="players-title">
    <div class="dialog__head" id="players-title">${ICONS.crown}PLAYERS</div>
    <div class="dialog__body"><ul class="host-list">${rows}</ul></div>
    <div class="dialog__legend"><span class="legend__item"><span class="glyph glyph--sm" style="${glyphStyle(G.MENU)}">✛</span>MOVE</span><button type="button" class="legend__item legend__btn" data-cmd="menu-back"><span class="glyph glyph--sm" style="${glyphStyle(G.B)}">B</span>BACK</button></div>
  </div>`;
}

// REMOVE <NAME>? Where everything goes: deeds, houses and cash to the bank, the seat closed.
export function removeConfirmHtml(state, playerId, ctx = {}) {
  const player = playerById(state, playerId);
  if (!player) return confirmOverlayHtml({ title: "REMOVE?", text: "", confirm: "REMOVE", danger: true, cancel: "KEEP" });
  const now = ctx.now ?? Date.now();
  const summary = removalSummary(state, playerId);
  const [tag] = hostTag(state, player, now);
  const sub = [tag === "ONLINE" ? "" : tag, doingWord(state, playerId).toUpperCase()].filter(Boolean).join(" · ");
  const bank = `<b class="remove-row__to">${ICONS.loan}BANK</b>`;
  const row = (what, to) => `<li class="remove-row"><span>${what}</span>${to}</li>`;
  return `<div class="dialog dialog--remove on-light" role="alertdialog" aria-modal="true" aria-labelledby="remove-title">
    <div class="dialog__body">
      <div class="remove-head"><span class="remove-head__token">${playerTokenSvg(state, player, { size: 96 })}</span><div><h2 id="remove-title">REMOVE ${e(upper(player.name))}?</h2>${sub ? `<small>${e(sub)}</small>` : ""}</div></div>
      <ul class="remove-rows">
        ${row(`DEEDS ×${summary.deeds}`, bank)}
        ${row(`HOUSES ×${summary.houses}`, bank)}
        ${row(`CASH ${money(summary.cash)}`, bank)}
        ${row("SEAT", '<b class="remove-row__closed">CLOSED</b>')}
      </ul>
      <div class="dialog__actions dialog__actions--stack"><button type="button" class="big-btn big-btn--danger" data-cmd="confirm">REMOVE ${e(upper(player.name))}</button><button type="button" class="big-btn" data-cmd="close" data-autofocus>KEEP</button></div>
    </div>
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// CONTROLS (UX-01): the one key map, the same words in the same order on the pad, the keyboard and
// the phone.

export function controlsOverlayHtml() {
  const cap = (key) => `<span class="key-cap">${key === "↵" ? "↵" : e(upper(key))}</span>`;
  const pad = (entry) => `<span class="glyph glyph--lg" style="${glyphStyle(G[entry.button])}">${entry.button === "VIEW" ? ICONS.views : entry.button === "START" ? ICONS.menu : entry.button}</span>`;
  const phone = (entry) => `<span class="phone-btn phone-btn--${entry.tone}">${entry.icon && ICONS[entry.icon] ? ICONS[entry.icon] : ""}${e(entry.phone)}</span>`;
  const rows = KEY_MAP.map((entry) => `<tr><td class="controls__pad">${pad(entry)}</td><td class="controls__does"><b>${e(entry.name)}</b><small>${e(entry.does)}</small></td><td class="controls__keys">${entry.keys.map(cap).join("")}</td><td class="controls__phone">${phone(entry)}</td></tr>`).join("");
  return `<div class="dialog dialog--controls on-light" role="dialog" aria-modal="true" aria-labelledby="controls-title">
    <div class="dialog__head" id="controls-title">${ICONS.gamepad}CONTROLS</div>
    <div class="dialog__body controls">
      <table class="controls__table"><thead><tr><th>PAD</th><th>DOES</th><th>KEYBOARD</th><th>PHONE</th></tr></thead><tbody>${rows}</tbody></table>
      <div class="controls__pills"><span class="now-pill">SAME WORDS ON EVERY SCREEN</span><span class="now-pill">PHONE BUTTONS IN THE SAME ORDER</span></div>
    </div>
    <div class="dialog__legend"><span class="legend__item"><span class="glyph glyph--sm" style="${glyphStyle(G.MENU)}">✛</span>MOVE</span><button type="button" class="legend__item legend__btn" data-cmd="close" data-autofocus><span class="glyph glyph--sm" style="${glyphStyle(G.B)}">B</span>CLOSE</button></div>
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// COMPUTER PLAYS FOR <NAME> (UX-07): the stand-in's card on the action card while it plays for the
// person the game waits on. Safe moves only (docs/API.md): roll, buy, bid, pay; never trades, loans
// or builds. Any command from their own seat hands it back.

export function standInPlayer(state) {
  if (!state || state.status !== "playing" || isTimed(state.auction)) return null;
  const ids = [state.currentPlayerId, ...waitedOnIds(state)];
  return ids.map((id) => playerById(state, id)).find((player) => player && isPerson(player) && player.autopilot && !player.bankrupt) || null;
}

export function standInCardHtml(state, player) {
  if (!player) return "";
  const where = { phone: "PHONE", controller: "PAD", keyboard: "KEYBOARD", classic: "BROWSER" }[inputOf(player)] || "PHONE";
  const pill = (text, tone) => `<span class="standin-pill standin-pill--${tone}">${text}</span>`;
  return `<div class="standin-card">
    <div class="standin-card__head"><span class="standin-card__icon">${ICONS.robot}</span><b>COMPUTER PLAYS FOR ${e(upper(player.name))}</b></div>
    <div class="standin-card__row"><span class="standin-card__label">DOES</span>${["ROLL", "BUY", "BID", "PAY"].map((word) => pill(word, "does")).join("")}</div>
    <div class="standin-card__row"><span class="standin-card__label">NEVER</span>${["TRADE", "LOAN", "BUILD"].map((word) => pill(word, "never")).join("")}</div>
    <div class="standin-card__back"><span class="standin-card__label">BACK</span><b>ANY BUTTON · ${e(upper(player.name))}'S ${where}</b></div>
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// RENT ×2 / ×10 (UX-05): rent a "nearest" Headlines card set, as base × multiplier = amount. The
// rent is already paid (the engine pays it on landing): the board's centre shows the sum, the right
// column the receipt.

const DIE_FACES = ["", "⚀", "⚁", "⚂", "⚃", "⚄", "⚅"];

// The landing's multiple when it belongs to the player whose turn it is, else null.
export function rentMultipleNow(state) {
  const landing = state?.lastLanding;
  if (!landing || landing.kind !== "rent" || landing.playerId !== state.currentPlayerId) return null;
  const multiple = rentMultiple(landing);
  return multiple ? { ...multiple, landing, space: state.board[landing.spaceIndex], owner: playerById(state, landing.recipientId) } : null;
}

function cardDeckWord(state) {
  return state.lastCard?.deck === "chest" ? "Post" : "Headlines";
}

export function rentStageHtml(state) {
  const rent = rentMultipleNow(state);
  if (!rent || !rent.space) return "";
  const { space, owner, multiplier, base, dice, amount } = rent;
  const stations = space.type === "railroad" && owner ? ownedBy(state, owner.id).filter((index) => state.board[index]?.type === "railroad").length : 0;
  const sub = [owner ? upper(owner.name) : "", stations ? `${stations} STATION${stations === 1 ? "" : "S"}` : ""].filter(Boolean).join(" · ");
  const baseHtml = dice
    ? `<span class="rent-sum__base rent-sum__base--dice"><span class="rent-sum__dice" aria-hidden="true">${DIE_FACES[dice[0]] || ""}${DIE_FACES[dice[1]] || ""}</span>${base}</span>`
    : `<span class="rent-sum__base">${money(base)}</span>`;
  return `<div class="rent-stage" data-deed-info="${space.index}">
    <div class="rent-stage__top"><span class="rent-tag">RENT ×${multiplier}</span><span class="rent-stage__where"><b>${e(upper(shortName(space.name).replace(/ RR$/, "")))}</b>${sub ? `<small>${e(sub)}</small>` : ""}</span></div>
    <div class="rent-sum">${baseHtml}<span class="rent-sum__op">×</span><span class="rent-sum__mult">${multiplier}</span><span class="rent-sum__op">=</span><span class="rent-sum__total">${money(amount)}</span></div>
  </div>`;
}

// The right column's receipt: what the rent would be, the card's multiple, what was paid.
export function rentReceiptHtml(state) {
  const rent = rentMultipleNow(state);
  if (!rent) return "";
  const { space, owner, multiplier, base, dice, amount } = rent;
  const first = dice ? [`Roll ${dice[0]} + ${dice[1]}`, String(base)] : [`${space?.type === "utility" ? "Utility" : "Station"} rent`, money(base)];
  return `<div class="rent-receipt">
    <div class="rent-receipt__row"><span>${e(first[0])}</span><b>${e(first[1])}</b></div>
    <div class="rent-receipt__row"><span>${cardDeckWord(state)} card</span><b class="is-red">×${multiplier}</b></div>
    <div class="rent-receipt__row rent-receipt__row--total"><span>PAID ${owner ? e(upper(owner.name)) : "BANK"}</span><b>${money(amount)}</b></div>
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// Debt (UX-04): the board's centre has OWE / CASH / STILL TO FIND and a bar; the right column the
// checklist of deeds to mortgage and houses to sell, ticked or not, for a seat at this table.

// The payer's checklist with this table's ticks, or null when the payer isn't seated here.
export function tableDebtList(state, ctx = {}) {
  if (state?.phase !== "debt" || !state.debt) return null;
  if (!seatFor(state, state.debt.payerId)) return null;
  return debtChecklist(state, state.debt.payerId, ctx.debtPicks?.[debtKey(state)] || null);
}

function debtReason(state) {
  const reason = String(state.debt?.reason || "").replace(/\s+rent$/i, "");
  return reason ? upper(reason) : "";
}

export function debtStageHtml(state, ctx = {}) {
  const debt = state.debt;
  const payer = playerById(state, debt.payerId);
  const creditor = playerById(state, debt.creditorId);
  if (!payer) return "";
  const list = tableDebtList(state, ctx);
  const owe = Number(debt.amount) || 0;
  const cash = Math.max(0, payer.cash);
  const raised = list ? list.raised : 0;
  const toFind = list ? list.toFind : Math.max(0, owe - cash);
  const pct = (value) => `${Math.max(0, Math.min(100, owe > 0 ? (value / owe) * 100 : 0)).toFixed(1)}%`;
  const cashPart = Math.min(cash, owe);
  const raisedPart = Math.min(raised, Math.max(0, owe - cashPart));
  const covered = toFind === 0;
  return `<div class="debt-stage" data-debt-stage>
    <div class="debt-stage__head">${playerTokenSvg(state, payer, { size: 64 })}<b>${e(upper(payer.name))} OWES ${creditor ? e(upper(creditor.name)) : "THE BANK"}</b>${creditor ? playerTokenSvg(state, creditor, { size: 64 }) : '<span class="debt-stage__bank">BANK</span>'}</div>
    <div class="debt-stage__nums">
      <div class="debt-num"><span class="debt-num__label">OWE</span><b class="debt-num__amt debt-num__amt--owe">${money(owe)}</b><span class="debt-num__sub">${e(debtReason(state))}</span></div>
      <div class="debt-num"><span class="debt-num__label">CASH</span><b class="debt-num__amt debt-num__amt--cash">${money(cash)}</b><span class="debt-num__sub">IN HAND</span></div>
      <div class="debt-num"><span class="debt-num__label">STILL TO FIND</span><b class="debt-num__amt debt-num__amt--${covered ? "done" : "find"}">${money(toFind)}</b><span class="debt-num__sub">${covered ? "COVERED" : list ? "AFTER PLAN" : "SHORT"}</span></div>
    </div>
    <div class="debt-bar" aria-hidden="true"><i class="debt-bar__cash" style="width:${pct(cashPart)}"></i><i class="debt-bar__raised" style="width:${pct(raisedPart)}"></i></div>
    <div class="debt-stage__key"><span><i class="debt-key debt-key--cash"></i>CASH ${money(cash)}</span>${raised ? `<span><i class="debt-key debt-key--raised"></i>RAISED ${money(raised)}</span>` : ""}<b class="${covered ? "is-done" : ""}">${covered ? "COVERED" : `SHORT ${money(toFind)}`}</b></div>
  </div>`;
}

// How many checklist rows the card shows (the board's four): the ticked ones always, then the
// deeds that raise the most, then the house sales. Every deed is still in DEEDS (View / V).
export const DEBT_ROWS = 4;

export function debtRowsShown(list) {
  if (!list) return [];
  const all = [...list.mortgage, ...list.sell];
  const rank = (row) => (row.ticked ? 0 : row.blocked ? 3 : row.kind === "mortgage" ? 1 : 2);
  const ordered = all.map((row, index) => ({ row, index })).sort((a, b) => rank(a.row) - rank(b.row) || b.row.amount - a.row.amount || a.index - b.index).map((entry) => entry.row);
  const ticked = ordered.filter((row) => row.ticked);
  const shown = new Set([...ticked, ...ordered.filter((row) => !row.ticked)].slice(0, Math.max(DEBT_ROWS, ticked.length)));
  return all.filter((row) => shown.has(row));
}

// The checklist card: MORTGAGE (deeds, rent now → $0) and SELL HOUSES (houses now → after), each
// a button that ticks or unticks; a deed with houses on its set waits until they're sold. Numbers
// and names only: the board's centre has OWE / CASH / STILL TO FIND.
export function debtListHtml(state, list) {
  const rows = debtRowsShown(list);
  if (!rows.length) return "";
  const colour = (group) => GROUP_COLORS[group] || "#5B6470";
  const tick = (row, sub, icon = "") => `<button type="button" class="debt-tick${row.ticked ? " is-on" : ""}" data-debt-tick="${e(row.key)}" aria-pressed="${row.ticked}"${row.blocked ? " disabled" : ""}>
      <span class="debt-tick__box">${row.ticked ? ICONS.check : ""}</span><span class="debt-tick__bar" style="background:${colour(row.group)}"></span>
      <span class="debt-tick__text"><b>${e(upper(row.name))}</b><small>${icon}${e(sub)}</small></span><span class="debt-tick__amt">+${money(row.amount)}</span>
    </button>`;
  const mortgages = rows.filter((row) => row.kind === "mortgage").map((row) => tick({ ...row, name: shortName(row.name) }, row.blocked ? "sell houses first" : `${money(row.rent)} → $0`)).join("");
  const sells = rows.filter((row) => row.kind === "sell").map((row) => tick({ ...row, name: row.name.endsWith(" houses") ? colourWord(row.group) : shortName(row.name) }, `${row.houses} → ${row.after}`, ICONS.house)).join("");
  return `<div class="debt-list on-light">
    ${mortgages ? `<span class="debt-list__title">MORTGAGE</span>${mortgages}` : ""}
    ${sells ? `<span class="debt-list__title">SELL HOUSES</span>${sells}` : ""}
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// The "show" slot above the action card (screen space, so it stays in view however the camera is
// zoomed): the thing being decided, big: the deed for sale, the bid, the card, the deal. The board's
// own centre keeps only the ESTATE logo; dice get their own spot (table.js).

export function slotHtml(state, ctx = {}) {
  // A timed auction plays on the board's centre (stageHtml); the right column has the keys.
  if (isTimed(state)) return "";
  if (state.phase === "auction" && state.auction) {
    const space = state.board[state.auction.spaceIndex];
    const leader = playerById(state, state.auction.bidderId);
    return `<div class="slot-auction" data-deed-info="${state.auction.spaceIndex}"><span class="slot-auction__eyebrow">${ICONS.hammer}${e(upper(space.name))}</span><span class="slot-auction__bid">${money(state.auction.currentBid)}</span><span class="slot-auction__who">${leader ? `${ICONS.crown}${chip(state, leader, 30)}` : `<span class="slot-auction__list">${money(space.price)}</span>`}</span></div>`;
  }
  if (state.phase === "card_confirmation" && state.pendingCard) {
    const chest = state.pendingCard.deck === "chest";
    return `<div class="flip-card flip-card--static${chest ? " flip-card--chest" : ""}"><div class="flip-card__inner" style="transform:rotateY(180deg)"><div class="flip-card__back">${chest ? "POST" : "HEADLINES"}</div><div class="flip-card__front"><span class="flip-card__eyebrow">${chest ? "POST" : "HEADLINES"}</span><span class="flip-card__text">${e(state.pendingCard.text)}</span></div></div></div>`;
  }
  if (state.phase === "awaiting_purchase") {
    return `<div class="slot-deed" data-deed-info="${state.pendingPurchase}">${deedHtml(state, state.pendingPurchase, { eyebrow: "FOR SALE", className: "deed--board" })}</div>`;
  }
  if (state.phase === "trade" && state.trade && !botDeal(state)) return dealSlotHtml(state, state.trade);
  return "";
}

// The board's centre during a timed auction (UX-03): AUCTION, the deed with its photo, the clock
// ring (GOING ONCE at 3 s or less), the top bid and everyone in it. The clock is drawn for
// ctx.now (the server's synced clock); table.js keeps it running with tickAuctionClock.
const DEVICE_WORD = { phone: "PHONE", controller: "PAD", keyboard: "KEYS", ai: "COMPUTER", classic: "BROWSER" };

export function auctionClockHtml(auction, now, { size = "lg" } = {}) {
  const left = msLeft(auction, now);
  const going = goingOnce(auction, now);
  const fraction = clockFraction(auction, now).toFixed(3);
  return `<div class="auc-clock auc-clock--${size}${going ? " is-going" : ""}${left <= 0 ? " is-closed" : ""}" data-auc-clock style="--left:${fraction}">
      <span class="auc-clock__face" role="timer" aria-label="Seconds left">
        <svg class="auc-clock__ring" viewBox="0 0 144 144" aria-hidden="true"><circle class="auc-clock__track" cx="72" cy="72" r="64"/><circle class="auc-clock__run" cx="72" cy="72" r="64" pathLength="100"/></svg>
        <span class="auc-clock__secs" data-auc-secs>${secondsLeft(left)}</span>
        <span class="auc-clock__unit">SECONDS</span>
      </span>
      <span class="auc-going"><span class="auc-going__soon">GOING ONCE AT ${Math.round(GOING_ONCE_MS / 1000)}</span><span class="auc-going__now">GOING ONCE</span></span>
    </div>`;
}

export function stageHtml(state, ctx = {}) {
  if (state?.phase === "debt" && state.debt && state.status === "playing") return debtStageHtml(state, ctx);
  if (state?.phase === "turn_complete" && state.status === "playing") return rentStageHtml(state);
  if (!isTimed(state)) return "";
  const auction = state.auction;
  const space = state.board[auction.spaceIndex];
  const now = ctx.now ?? Date.now();
  const leader = playerById(state, auction.bidderId);
  const photo = spacePhoto(space);
  const band = spaceColour(space);
  const roster = auctionRoster(state).map(({ player, bid, passed, leads }) => {
    const seat = seatFor(state, player.id);
    let tag = DEVICE_WORD[inputOf(player)] || "";
    let tone = "";
    if (leads) [tag, tone] = ["LEADS", "lead"];
    else if (passed) [tag, tone] = ["PASSED", "out"];
    else if (bid) tag = money(bid);
    else if (seat && canBid(state, player.id)) [tag, tone] = ["BIDDING", "bidding"];
    return `<span class="auc-who${passed ? " is-out" : ""}${leads ? " is-lead" : ""}">${playerTokenSvg(state, player, { size: 64 })}<b>${e(upper(player.name))}</b><span class="auc-tag${tone ? ` auc-tag--${tone}` : ""}">${e(tag)}</span></span>`;
  }).join("");
  return `<div class="auc-stage">
    <div class="auc-stage__title">${ICONS.hammer}<span>AUCTION</span></div>
    <div class="auc-stage__mid">
      <div class="auc-deed"${photo ? ` data-photo="${photo}"` : ""} data-deed-info="${auction.spaceIndex}">
        <div class="auc-deed__head" style="background:${band};color:${inkOn(band)}"><span class="auc-deed__eyebrow">DEED</span><span class="auc-deed__name">${e(upper(space.name))}</span></div>
        <span class="auc-deed__list">LIST ${money(space.price)}</span>
      </div>
      ${auctionClockHtml(auction, now)}
    </div>
    <div class="auc-top"><span class="auc-top__label">TOP BID</span><b class="auc-top__amt">${money(auction.currentBid)}</b>${leader ? `<span class="auc-top__who">${playerTokenSvg(state, leader, { size: 56 })}<b>${e(upper(leader.name))}</b></span>` : '<span class="auc-top__who auc-top__who--none">NO BIDS</span>'}</div>
    <div class="auc-roster">${roster}</div>
  </div>`;
}

// The mini-map (top-right while zoomed): the whole board as flat squares, colour bars, owner dots
// and tokens, in a 121 × 121 viewBox (board units × 10: nine squares plus two 1.55 corners).
const TRACK = (n) => (n === 1 ? 0 : n === 11 ? 105.5 : 15.5 + (n - 2) * 10);
const TRACK_SIZE = (n) => (n === 1 || n === 11 ? 15.5 : 10);

export function minimapHtml(state, marks = null) {
  if (!state?.board) return "";
  const cells = [];
  const at = new Map();
  for (const space of state.board) {
    const pos = cellPosition(space.index);
    const x = TRACK(pos.col);
    const y = TRACK(pos.row);
    const w = TRACK_SIZE(pos.col);
    const h = TRACK_SIZE(pos.row);
    at.set(space.index, { x, y, w, h, side: pos.side });
    cells.push(`<rect x="${x}" y="${y}" width="${w}" height="${h}" fill="#FBFAF4" stroke="#15191E" stroke-width="0.5"/>`);
    if (space.type === "property") {
      const strip = { bottom: [x, y, w, 3], top: [x, y + h - 3, w, 3], left: [x + w - 3, y, 3, h], right: [x, y, 3, h] }[pos.side];
      if (strip) cells.push(`<rect x="${strip[0]}" y="${strip[1]}" width="${strip[2]}" height="${strip[3]}" fill="${GROUP_COLORS[space.group]}"/>`);
    }
    const owner = playerById(state, propState(state, space.index)?.ownerId);
    if (owner) cells.push(`<circle cx="${x + w / 2}" cy="${y + h / 2}" r="2.4" fill="${playerColor(state, owner)}" stroke="#15191E" stroke-width="0.6"/>`);
  }
  const tokens = [];
  const crowd = new Map();
  for (const player of state.players || []) {
    if (player.bankrupt || !at.has(player.position)) continue;
    const box = at.get(player.position);
    const n = crowd.get(player.position) || 0;
    crowd.set(player.position, n + 1);
    const cx = box.x + box.w / 2 + ((n % 3) - 1) * 3.4;
    const cy = box.y + box.h / 2 + (Math.floor(n / 3) - 0.5) * 3.4;
    const turn = player.id === state.currentPlayerId;
    tokens.push(`<circle cx="${cx.toFixed(1)}" cy="${cy.toFixed(1)}" r="${turn ? 3.6 : 2.8}" fill="${playerColor(state, player)}" stroke="${turn ? "#F2C14E" : "#15191E"}" stroke-width="${turn ? 1.4 : 0.8}"/>`);
  }
  // A trade's squares: red rings for what you give, green for what you get.
  const rings = [];
  for (const [kind, colour] of [["give", "#D9343A"], ["get", "#1C8A47"]]) {
    for (const index of marks?.[kind] || []) {
      const box = at.get(index);
      if (box) rings.push(`<rect x="${box.x + 0.6}" y="${box.y + 0.6}" width="${box.w - 1.2}" height="${box.h - 1.2}" fill="${colour}" fill-opacity="0.25" stroke="${colour}" stroke-width="1.6"/>`);
    }
  }
  return `<svg class="minimap__svg" viewBox="0 0 121 121" aria-hidden="true" focusable="false"><rect x="15.5" y="15.5" width="90" height="90" fill="#D3E6D2"/>${cells.join("")}${rings.join("")}${tokens.join("")}</svg>`;
}

// Squares the board marks for this state: next-roll heat for the player about to roll (replaces
// the risk sentence), the squares in a trade offer, a card's destination.
export function boardMarks(state) {
  const marks = { risk: [], pick: [], target: [] };
  if (state?.status !== "playing") return marks;
  const current = playerById(state, state.currentPlayerId);
  if (state.phase === "awaiting_roll" && current) {
    marks.risk = nextRollHeat(state, current.id).squares.filter((entry) => entry.cost > 0).map(({ index, level }) => ({ index, level }));
  }
  if (state.phase === "trade" && state.trade && !botDeal(state)) marks.pick = [...(state.trade.offer?.properties || []), ...(state.trade.request?.properties || [])];
  if (state.phase === "card_confirmation" && state.pendingCard) {
    const target = cardPreview(state, state.pendingCard.playerId, state.pendingCard.cardId).target;
    if (Number.isInteger(target)) marks.target = [target];
  }
  return marks;
}

// ---------------------------------------------------------------------------------------------
// Overlays

export function seatOverlayHtml(state, draft, ctx = {}) {
  const taken = new Map((state?.players || []).filter((player) => player.tokenKind).map((player) => [canonicalKind(player.tokenKind), player]));
  const tokens = TOKEN_KINDS.map((kind) => {
    const owner = taken.get(kind);
    const pressed = draft.tokenKind === kind;
    const isNew = NEW_TOKEN_KINDS.includes(kind) && !owner;
    const dot = `<i class="token-choice__dot" style="background:${owner ? playerColor(state, owner) || TOKEN_COLORS[kind] : TOKEN_COLORS[kind]}"></i>`;
    return `<button type="button" class="token-choice${pressed ? " is-picked" : ""}${owner ? " is-taken" : ""}" data-token="${kind}" aria-pressed="${pressed}" ${owner ? "disabled" : ""}>
      ${isNew ? '<span class="token-choice__new">NEW</span>' : ""}
      ${tokenSvg(kind, TOKEN_COLORS[kind], { size: 64 })}
      <span class="token-choice__name">${e(upper(TOKEN_LABELS[kind]))}</span>
      <small class="token-choice__who">${dot}${e(upper(owner ? owner.name : TOKEN_COLOR_NAMES[kind] || ""))}</small>
    </button>`;
  }).join("");
  const seated = (state?.players || []).filter((player) => !player.bankrupt).length;
  const device = draft.input === "keyboard" ? "Keyboard" : padLabel(ctx, draft.pad);
  return `<div class="dialog on-light" role="dialog" aria-modal="true" aria-labelledby="seat-title">
    <div class="dialog__head" id="seat-title">TAKE A SEAT<span class="seat-count">SEATS ${seated} / ${MAX_PLAYERS}</span></div>
    <div class="dialog__body">
      <p class="dialog__lead dialog__lead--icon">${draft.input === "keyboard" ? ICONS.keyboard : ICONS.pad}<span>${e(device)}</span></p>
      <div class="field"><span>Token</span><div class="token-grid">${tokens}</div></div>
      <label class="field"><span>Name (optional)</span><input id="seat-name" type="text" maxlength="12" autocomplete="off" value="${e(draft.name)}"></label>
      <div class="dialog__actions">
        <button type="button" class="big-btn big-btn--primary" data-cmd="seat-confirm">TAKE SEAT</button>
        <button type="button" class="big-btn" data-cmd="close">CANCEL</button>
      </div>
    </div>
  </div>`;
}

function groupBlocks(state, playerId) {
  const owned = ownedBy(state, playerId);
  const blocks = [];
  const groups = [...new Set(owned.map((index) => state.board[index]).filter((space) => space.type === "property").map((space) => space.group))];
  for (const group of groups) blocks.push({ key: group, kind: "property", indexes: owned.filter((index) => state.board[index].group === group), size: groupIndexes(state, group).length });
  const rails = owned.filter((index) => state.board[index].type === "railroad");
  if (rails.length) blocks.push({ key: "railroad", kind: "railroad", indexes: rails, size: 4 });
  const utils = owned.filter((index) => state.board[index].type === "utility");
  if (utils.length) blocks.push({ key: "utility", kind: "utility", indexes: utils, size: 2 });
  return blocks;
}

export { groupBlocks };

// ---------------------------------------------------------------------------------------------
// Property, docked on the right like the trade drawer: deeds as cards grouped by colour set, each
// with its houses, rent, state and the actions that are possible now (or greyed with one word).
// The camera frames the deed that has focus; the player's squares pulse on the board.

function bankHas(state, houses) {
  const bank = state?.bank || {};
  if (houses === 4) return (bank.hotels ?? 12) > 0;
  return (bank.houses ?? 32) > 0;
}

// Pure model of one player's property: { player, manage, cash, worth, groups: [{ key, kind, title,
// colour, ink, owned, size, full, progress, deeds: [{ index, name, houses, hotel, rent, mortgaged,
// pledged, actions: [{ id, label, icon, act, enabled, why }] }] }], totals }.
export function propertyModel(state, playerId) {
  const player = playerById(state, playerId);
  const manage = canManage(state, playerId);
  const inDebt = state?.phase === "debt";
  const groups = groupBlocks(state, playerId).map((block) => {
    const property = block.kind === "property";
    const full = property && block.indexes.length === block.size;
    const all = property ? groupIndexes(state, block.key) : block.indexes;
    const levels = all.map((index) => propState(state, index)?.houses || 0);
    const min = Math.min(...levels);
    const max = Math.max(0, ...levels);
    const anyMortgaged = all.some((index) => propState(state, index)?.mortgaged);
    const anyPledged = all.some((index) => loanSecuredBy(state, index));
    const deeds = block.indexes.map((index) => {
      const space = state.board[index];
      const prop = propState(state, index);
      const houses = prop?.houses || 0;
      const pledged = Boolean(loanSecuredBy(state, index));
      const actions = [];
      if (full) {
        let why = "";
        if (!manage) why = "TURN";
        else if (inDebt) why = "DEBT";
        else if (houses >= 5) why = "MAX";
        else if (anyMortgaged) why = "MORTGAGED";
        else if (anyPledged) why = "PLEDGED";
        else if (houses > min) why = "EVEN";
        else if ((player?.cash ?? 0) < space.buildCost) why = "CASH";
        else if (!bankHas(state, houses)) why = "BANK";
        actions.push({ id: "build", label: `${houses === 4 ? "HOTEL" : "BUILD"} ${money(space.buildCost)}`, icon: "house", act: { type: "build", spaceIndex: index }, enabled: !why, why });
        if (max > 0) {
          let sellWhy = "";
          if (!manage) sellWhy = "TURN";
          else if (houses === 0) sellWhy = "NONE";
          else if (houses < max) sellWhy = "EVEN";
          else if (houses === 5 && (state?.bank?.houses ?? 32) < 4) sellWhy = "BANK";
          actions.push({ id: "sell", label: `SELL +${money(Math.floor(space.buildCost / 2))}`, icon: "coin", act: { type: "sell_building", spaceIndex: index }, enabled: !sellWhy, why: sellWhy });
        }
      }
      if (prop?.mortgaged) {
        const cost = unmortgageCost(space);
        const why = !manage ? "TURN" : inDebt ? "DEBT" : (player?.cash ?? 0) < cost ? "CASH" : "";
        actions.push({ id: "mortgage", label: `UNMORTGAGE ${money(cost)}`, icon: "stamp", act: { type: "unmortgage", spaceIndex: index }, enabled: !why, why });
      } else {
        const why = !manage ? "TURN" : property && max > 0 ? "HOUSES" : canMortgage(state, playerId, index) ? "" : "LOCKED";
        actions.push({ id: "mortgage", label: `MORTGAGE +${money(mortgageValue(space))}`, icon: "stamp", act: { type: "mortgage", spaceIndex: index }, enabled: !why, why });
      }
      return {
        index, name: space.name, colour: spaceColour(space), houses, hotel: houses === 5,
        rent: prop?.mortgaged ? null : rentLabel(state, index), mortgaged: Boolean(prop?.mortgaged), pledged, actions,
      };
    });
    const colour = property ? GROUP_COLORS[block.key] : block.kind === "railroad" ? "#2B323A" : "#5B6470";
    return {
      key: block.key, kind: block.kind,
      title: property ? upper(colourWord(block.key)) : block.kind === "railroad" ? "RAILROADS" : "UTILITIES",
      colour, ink: property ? GROUP_INK[block.key] || "#FFFFFF" : "#FFFFFF",
      owned: block.indexes.length, size: block.size, full, progress: full && property ? "FULL SET" : `${block.indexes.length} OF ${block.size}`, deeds,
    };
  });
  const deeds = groups.flatMap((group) => group.deeds);
  const totals = {
    deeds: deeds.length,
    houses: deeds.reduce((sum, deed) => sum + (deed.hotel ? 0 : deed.houses), 0),
    hotels: deeds.filter((deed) => deed.hotel).length,
    mortgaged: deeds.filter((deed) => deed.mortgaged).length,
  };
  return { player, manage, cash: player?.cash ?? 0, worth: player ? netWorth(state, player.id) : 0, groups, totals };
}

// The order keys and pads walk the drawer in: each card, then its enabled buttons; CLOSE last.
export function propertyFocusOrder(model) {
  const order = [];
  for (const group of model.groups) {
    for (const deed of group.deeds) {
      order.push(`${deed.index}:card`);
      for (const action of deed.actions) if (action.enabled) order.push(`${deed.index}:${action.id}`);
    }
  }
  order.push("close");
  return order;
}

// Why a drawer action is off, as a reason rather than something to press (review 4 Nov #10:
// "MORTGAGE HOUSES" read like a button). Keyed by action id, then the model's one-word code.
const WHY_TEXT = {
  build: { TURN: "ON YOUR TURN", DEBT: "PAY YOUR DEBT FIRST", MAX: "HOTEL BUILT", MORTGAGED: "UNMORTGAGE THE SET FIRST", PLEDGED: "SET IS PLEDGED FOR A LOAN", EVEN: "BUILD EVENLY FIRST", CASH: "NOT ENOUGH CASH", BANK: "BANK HAS NO HOUSES" },
  sell: { TURN: "ON YOUR TURN", NONE: "NO HOUSES TO SELL", EVEN: "SELL EVENLY FIRST", BANK: "BANK HAS NO HOUSES" },
  mortgage: { TURN: "ON YOUR TURN", DEBT: "PAY YOUR DEBT FIRST", CASH: "NOT ENOUGH CASH", HOUSES: "SELL HOUSES TO MORTGAGE", LOCKED: "CAN'T MORTGAGE YET" },
};

export function whyText(action) {
  return WHY_TEXT[action.id]?.[action.why] || action.why;
}

export function propertyDockHtml(state, seat, ctx = {}) {
  const model = propertyModel(state, seat.playerId);
  const player = model.player;
  const card = (deed, ink) => {
    const houses = deed.hotel
      ? '<span class="pdeed__hotel" title="Hotel"></span>'
      : deed.houses ? `<span class="pdeed__houses" title="${deed.houses} house${deed.houses === 1 ? "" : "s"}">${'<span class="pdeed__house"></span>'.repeat(deed.houses)}</span>` : "";
    const tags = [deed.mortgaged ? '<span class="pdeed__tag pdeed__tag--mortgaged">MORTGAGED</span>' : "", deed.pledged ? `<span class="pdeed__tag pdeed__tag--pledged">${ICONS.lock}PLEDGED</span>` : ""].join("");
    const buttons = model.manage ? deed.actions.map((action) => action.enabled
      ? `<button type="button" class="pdeed__act pdeed__act--${action.id}" data-act='${JSON.stringify(action.act)}' data-pkey="${deed.index}:${action.id}" data-deed="${deed.index}">${ICONS[action.icon] || ""}${e(action.label)}</button>`
      : `<span class="pdeed__why pdeed__why--${action.id}" data-deed="${deed.index}" title="${e(action.label)}: ${e(whyText(action).toLowerCase())}">${ICONS.lock}<b>${e(whyText(action))}</b></span>`).join("") : "";
    return `<article class="pdeed${deed.mortgaged ? " is-mortgaged" : ""}" style="--gc:${deed.colour};--gc-ink:${ink}" tabindex="0" data-deed="${deed.index}" data-pkey="${deed.index}:card" aria-label="${e(deed.name)}">
      <div class="pdeed__bar" data-deed-info="${deed.index}"><span class="pdeed__name">${e(upper(shortName(deed.name)))}</span>${houses}</div>
      <div class="pdeed__row"><span class="pdeed__rent">${deed.rent === null ? "—" : `<small>RENT</small>${e(deed.rent)}`}</span>${tags}</div>
      ${buttons ? `<div class="pdeed__acts">${buttons}</div>` : ""}
    </article>`;
  };
  const groups = model.groups.map((group) => {
    const photo = group.deeds[0] ? spacePhoto(state.board[group.deeds[0].index] || {}) : "";
    return `<section class="pgroup" aria-label="${e(group.title)}">
      <header class="pgroup__head" style="background:${group.colour};color:${group.ink}"${photo ? ` data-photo="${photo}"` : ""}><span>${e(group.title)}</span><span class="pgroup__progress${group.full ? " is-full" : ""}">${e(group.progress)}</span></header>
      <div class="pgroup__cards">${group.deeds.map((deed) => card(deed, group.ink)).join("")}</div>
    </section>`;
  }).join("");
  const totals = model.totals;
  return `<div class="prop-dock" role="dialog" aria-labelledby="prop-title">
    <div class="trade-dock__head prop-dock__head">
      <span id="prop-title" class="trade-title">${playerTokenSvg(state, player, { size: 34 })}${e(upper(possessive(player.name)))} PROPERTY</span>
      <span class="trade-me"><span class="prop-dock__money">${ICONS.coin}<b>${money(model.cash)}</b></span><span class="prop-dock__money prop-dock__money--worth" title="Net worth">${ICONS.crown}<b>${money(model.worth)}</b></span></span>
    </div>
    ${model.manage ? "" : `<div class="prop-dock__note">${ICONS.lock}VIEW ONLY</div>`}
    <div class="prop-dock__body">${groups || `<div class="trade-none">${ICONS.deed}NO DEEDS</div>`}</div>
    <div class="trade-foot prop-dock__foot">
      <span class="prop-dock__totals">${ICONS.deed}<b>${totals.deeds}</b>${ICONS.house}<b>${totals.houses}</b>${totals.hotels ? `<span class="pdeed__hotel pdeed__hotel--sm"></span><b>${totals.hotels}</b>` : ""}${totals.mortgaged ? `<span class="pdeed__tag pdeed__tag--mortgaged">${totals.mortgaged} MORTGAGED</span>` : ""}</span>
      <button type="button" class="big-btn" data-cmd="close" data-pkey="close">${padGlyph(seat, "B", "Esc")}CLOSE</button>
    </div>
  </div>`;
}

// The PC used to show property as a modal; the drawer replaced it. Same signature.
export const propertyOverlayHtml = propertyDockHtml;

// ---------------------------------------------------------------------------------------------
// Trade on one page: partner chips on top (none picked), YOU GIVE | YOU GET side by side with
// each player's own deeds as mini-cards and a cash stepper, a value bar, the computer's price as
// an optional button that never edits the deal by itself, SEND / CANCEL. The draft dies with the
// overlay.

export function newTradeDraft() {
  return { targetId: null, offer: [], request: [], offerCash: 0, requestCash: 0, quote: null, quoteKey: "" };
}

// What can actually be sent, from what each side still owns right now. Cash-for-cash is blocked.
export function tradeDraftStatus(state, meId, draft) {
  const target = playerById(state, draft?.targetId);
  const me = playerById(state, meId);
  const open = (playerId) => tradeable(state, playerId).filter((index) => !loanSecuredBy(state, index));
  const mine = me ? open(me.id) : [];
  const theirs = target ? open(target.id) : [];
  const offer = (draft?.offer || []).filter((index) => mine.includes(index));
  const request = (draft?.request || []).filter((index) => theirs.includes(index));
  const offerCash = Math.max(0, Math.min(me?.cash || 0, draft?.offerCash || 0));
  const requestCash = Math.max(0, Math.min(target?.cash || 0, draft?.requestCash || 0));
  const gives = offer.length > 0 || offerCash > 0;
  const gets = request.length > 0 || requestCash > 0;
  const deeds = offer.length + request.length > 0;
  let reason = "";
  if (!target) reason = "PICK A PLAYER";
  else if (!gives) reason = "ADD WHAT YOU GIVE";
  else if (!gets) reason = "ADD WHAT YOU GET";
  else if (!deeds) reason = "ADD A DEED";
  return { target, offer, request, offerCash, requestCash, gives, gets, valid: Boolean(target && gives && gets && deeds), reason, mine, theirs };
}

// Deeds a player owns but can't trade right now, with a one-word reason.
export function lockedDeeds(state, playerId) {
  const open = new Set(tradeable(state, playerId));
  return ownedBy(state, playerId)
    .filter((index) => !open.has(index) || loanSecuredBy(state, index))
    .map((index) => ({ index, why: loanSecuredBy(state, index) ? "PLEDGED" : "HOUSES" }));
}

function groupCount(state, playerId, group) {
  return groupIndexes(state, group).filter((index) => propState(state, index)?.ownerId === playerId).length;
}

// The card would complete `receiverId`'s colour set.
function completesSet(state, receiverId, index) {
  const space = state.board[index];
  if (space?.type !== "property" || !receiverId) return false;
  return groupCount(state, receiverId, space.group) + 1 === groupIndexes(state, space.group).length;
}

// One deed as a row: colour bar, short name, a set badge, the price, a tick when picked. Rows (not
// a grid) so ↑/↓ always means "next deed" for keys and D-pads.
function deedRows(state, indexes, picked, field, { receiverId, warn = false, locked = [] } = {}) {
  const rows = indexes.map((index) => {
    const space = state.board[index];
    const on = picked.includes(index);
    const sets = completesSet(state, receiverId, index);
    const mortgaged = propState(state, index)?.mortgaged;
    const badge = sets ? `<span class="deed-row__badge deed-row__badge--${warn ? "warn" : "good"}">${warn ? "GIVES SET" : "SET"}</span>` : "";
    return `<button type="button" class="deed-row${on ? " is-on" : ""}${mortgaged ? " is-mortgaged" : ""}" data-toggle="${field}:${index}" data-nav="${field}" aria-pressed="${on}" style="--gc:${spaceColour(space)}" title="${e(space?.name || "")}">
      <span class="deed-row__bar" aria-hidden="true"></span><span class="deed-row__name">${e(upper(shortName(space?.name || "")))}</span>${badge}<b class="deed-row__price">${money(space?.price || 0)}</b><span class="deed-row__tick" aria-hidden="true">${ICONS.check}</span>
    </button>`;
  });
  for (const { index, why } of locked) {
    const space = state.board[index];
    rows.push(`<div class="deed-row is-locked" style="--gc:${spaceColour(space)}" aria-disabled="true"><span class="deed-row__bar" aria-hidden="true"></span><span class="deed-row__name">${e(upper(shortName(space?.name || "")))}</span><span class="deed-row__why">${ICONS.lock}${e(why)}</span></div>`);
  }
  return rows.join("");
}

// Cash for one side: the amount is the keyboard/pad stop (+/− or the bumpers change it); the
// ±10 / ±50 buttons are for the mouse.
function stepper(field, value, max, label, { disabled = false, seat = null } = {}) {
  const can = (delta) => !disabled && (delta < 0 ? value > 0 : value < max);
  const button = (delta) => `<button type="button" class="step-btn" tabindex="-1" data-cash="${field}:${delta}" ${can(delta) ? "" : "disabled"} aria-label="${label} ${delta > 0 ? "plus" : "minus"} ${Math.abs(delta)}">${delta > 0 ? "+" : "−"}${Math.abs(delta)}</button>`;
  const keys = seat?.input === "keyboard" ? "+/−" : "RB/LB";
  return `<div class="cash-step${disabled ? " is-off" : ""}">${button(-50)}${button(-10)}<button type="button" class="cash-step__value" data-cash-field="${field}" data-nav="${field}" ${disabled ? "disabled" : ""} aria-label="${label}: ${money(value)}. ${keys} to change" aria-live="polite">${money(value)}</button>${button(10)}${button(50)}</div>`;
}

function valueOf(state, indexes, cash) {
  return indexes.reduce((sum, index) => sum + (propState(state, index)?.mortgaged ? Math.floor(state.board[index].price / 2) : state.board[index].price), 0) + cash;
}

function padGlyph(seat, button, key) {
  const glyph = glyphFor(seat, button, key);
  return `<span class="glyph glyph--sm" style="${glyphStyle(glyph.glyphBg)}">${e(glyph.glyph)}</span>`;
}

// The computer's price for the deeds in the deal (asked when the deeds change; the cash is
// compared here): "ACCEPTS AT $650 · $348 MORE NEEDED" as a button that puts the price in,
// "✓ WILL ACCEPT" once the deal meets it, "WON'T TRADE AT ANY PRICE" (with why), THINKING.
function priceHtml(state, seat, target, status, quote) {
  if (!target || inputOf(target) !== "ai" || !(status.offer.length || status.request.length)) return "";
  const q = quoteState(quote, status.offerCash, status.requestCash);
  if (q.kind === "wait") return `<span class="price price--wait" role="status">${ICONS.cpu}THINKING<span class="busy-dots" aria-hidden="true"><i></i><i></i><i></i></span></span>`;
  if (q.kind === "error") return "";
  if (q.kind === "never" || q.kind === "short") {
    return `<span class="price price--never" role="status" title="${e(q.detail || "")}">${ICONS.cpu}<span class="price__text"><b>${e(q.text)}</b>${q.detail ? `<small class="price__why">${e(upper(q.detail))}</small>` : ""}</span></span>`;
  }
  if (q.kind === "ok" && q.offerCash === status.offerCash && q.requestCash === status.requestCash) {
    return `<span class="price price--done" role="status" title="${e(target.name)}: ${e(q.text.toLowerCase())}">${ICONS.cpu}${ICONS.check}<span class="price__text"><b>WILL ACCEPT</b><small class="price__why">${e(q.text)}</small></span></span>`;
  }
  if (q.kind === "ok") {
    // It would take less from you (or pay you more): one tap puts its exact price in.
    return `<button type="button" class="price-btn price-btn--ok" data-cmd="trade-apply-quote" data-nav="foot" title="Put in ${e(target.name)}'s price">${ICONS.cpu}${ICONS.check}<span class="price__text"><b>WILL ACCEPT</b><small class="price__why">${e(q.text)}</small></span>${padGlyph(seat, "Y", "Y")}</button>`;
  }
  return `<button type="button" class="price-btn" data-cmd="trade-apply-quote" data-nav="foot" title="Put in ${e(target.name)}'s price">${ICONS.cpu}<span class="price__text"><b>${e(q.text)}</b><small class="price__why">${e(q.status)}</small></span>${padGlyph(seat, "Y", "Y")}</button>`;
}

// Trade, docked in the right column (the board stays visible to the room). Zones for keys and
// pads: partners (←/→), YOU GIVE and YOU GET (↑/↓ through deeds then cash, ←/→ switch column),
// the foot (price, CANCEL, SEND). Enter toggles, +/− cash, S sends, Esc closes.
export function tradeDockHtml(state, seat, draft, ctx = {}) {
  const me = playerById(state, seat.playerId);
  const partners = state.players.filter((player) => player.id !== me.id && !player.bankrupt);
  const status = tradeDraftStatus(state, me.id, draft);
  const target = status.target && partners.some((player) => player.id === status.target.id) ? status.target : null;
  const partnerRow = partners.length
    ? partners.map((player) => {
      const on = target?.id === player.id;
      const ai = inputOf(player) === "ai";
      return `<button type="button" class="partner-chip${on ? " is-on" : ""}" data-partner="${e(player.id)}" data-nav="partners" aria-pressed="${on}" style="--pc:${playerColor(state, player)}">
        <span class="partner-chip__token">${playerTokenSvg(state, player, { size: 40 })}${ai ? `<span class="partner-chip__bot" title="Computer">${ICONS.cpu}</span>` : ""}</span>
        <span class="partner-chip__text"><span class="partner-chip__name">${e(upper(player.name))}</span><span class="partner-chip__cash">${money(player.cash)}</span></span>
      </button>`;
    }).join("")
    : `<div class="trade-none">${ICONS.swap}NO ONE</div>`;
  const none = `<div class="trade-none">${ICONS.deed}NONE</div>`;
  const giveRows = deedRows(state, status.mine, status.offer, "offer", { receiverId: target?.id, warn: true, locked: lockedDeeds(state, me.id) });
  const getRows = target ? deedRows(state, status.theirs, status.request, "request", { receiverId: me.id, locked: lockedDeeds(state, target.id) }) : "";
  const pick = `<div class="trade-none trade-none--pick">${ICONS.up}PICK A PLAYER</div>`;
  const give = `<section class="trade-col trade-col--give" data-col="offer" aria-label="You give">
      <header class="trade-col__head">${chip(state, me, 24)}<span>YOU GIVE</span></header>
      <div class="trade-col__deeds">${giveRows || none}</div>
      ${stepper("offer", status.offerCash, me.cash, "Cash you give", { seat })}
    </section>`;
  const get = `<section class="trade-col trade-col--get" data-col="request" aria-label="You get">
      <header class="trade-col__head">${target ? chip(state, target, 24) : ""}<span>YOU GET</span></header>
      <div class="trade-col__deeds">${target ? getRows || none : pick}</div>
      ${stepper("request", status.requestCash, target?.cash || 0, "Cash you get", { disabled: !target, seat })}
    </section>`;
  const giveValue = valueOf(state, status.offer, status.offerCash);
  const getValue = valueOf(state, status.request, status.requestCash);
  const total = Math.max(1, giveValue + getValue);
  const trade = target ? { proposerId: me.id, targetId: target.id, offer: { cash: status.offerCash, properties: status.offer }, request: { cash: status.requestCash, properties: status.request } } : null;
  const pills = trade ? setPills(state, setChanges(state, trade), me.id) : "";
  const empty = !giveValue && !getValue;
  const why = status.valid || !target ? "" : `<span class="trade-why">${e(status.reason)}</span>`;
  const summary = `<div class="trade-sum">
      <span class="trade-sum__give">${money(giveValue)}</span>
      <div class="value-bar${empty ? " is-empty" : ""}" role="img" aria-label="You give ${money(giveValue)}, you get ${money(getValue)}"><span style="flex:${empty ? 1 : (giveValue / total).toFixed(3)}"></span><span style="flex:${empty ? 1 : (getValue / total).toFixed(3)}"></span></div>
      <span class="trade-sum__get">${money(getValue)}</span>
    </div>
    ${pills || why ? `<div class="trade-sum__line">${pills}${why}</div>` : ""}`;
  const price = priceHtml(state, seat, target, status, draft?.quote);
  return `<div class="trade-dock${ctx.modal ? " trade-dock--modal" : ""}" role="dialog"${ctx.modal ? ' aria-modal="true"' : ""} aria-labelledby="trade-title">
    <div class="trade-dock__head"><span id="trade-title" class="trade-title">${ICONS.swap}TRADE</span><span class="trade-me">${chip(state, me, 26)}<span>${e(upper(me.name))}</span><b>${money(me.cash)}</b></span></div>
    <div class="trade-partners" role="group" aria-label="Trade with">${partnerRow}</div>
    <div class="trade-cols">${give}${get}</div>
    ${summary}
    <div class="trade-foot">
      ${price ? `<span class="trade-foot__price">${price}</span>` : ""}
      <button type="button" class="big-btn" data-cmd="close" data-nav="foot">${padGlyph(seat, "B", "Esc")}CANCEL</button>
      <button type="button" class="big-btn big-btn--primary" data-cmd="trade-send" data-nav="foot" ${status.valid ? "" : "disabled"}>SEND${padGlyph(seat, "X", "S")}</button>
    </div>
  </div>`;
}

// The PC used to show trade as a modal; the docked panel replaced it. Same signature.
export const tradeOverlayHtml = tradeDockHtml;

// Ask for a loan (debt, loans house rule), docked on the right like trade and property: who lends
// (partner chips), amount / repay / due as steppers, optional security as deed rows (they pulse on
// the board), the terms in one line, SEND / CLOSE.
export function loanDockHtml(state, seat, draft, ctx = {}) {
  const me = playerById(state, seat.playerId);
  const lenders = state.players.filter((player) => player.id !== me.id && !player.bankrupt);
  const target = playerById(state, draft.targetId) || lenders[0];
  const pledges = pledgeable(state, me.id).map((index) => {
    const on = draft.collateral.includes(index);
    const space = state.board[index];
    return `<button type="button" class="deed-row${on ? " is-on" : ""}" aria-pressed="${on}" data-pledge="${index}" style="--gc:${spaceColour(space)}">
      <span class="deed-row__bar" aria-hidden="true"></span><span class="deed-row__name">${e(upper(shortName(space.name)))}</span><b class="deed-row__price">${money(space.price)}</b><span class="deed-row__tick" aria-hidden="true">${ICONS.check}</span>
    </button>`;
  }).join("");
  const stepRow = (field, label, value, steps) => `<div class="loan-step"><span class="loan-step__label">${label}</span>${steps.map((step) => `<button type="button" class="step-btn" data-loan-step="${field}:${step}">${step > 0 ? "+" : "−"}${Math.abs(step)}</button>`).join("")}<span class="loan-step__value">${value}</span></div>`;
  const offer = { proposerId: me.id, targetId: target?.id, direction: "borrow", principal: draft.principal, repay: draft.repay, dueTurns: draft.dueTurns, collateral: draft.collateral };
  const valid = target && draft.principal > 0 && draft.principal <= target.cash && draft.repay >= draft.principal;
  return `<div class="loan-dock" role="dialog" aria-labelledby="loan-title">
    <div class="trade-dock__head"><span id="loan-title" class="trade-title">${ICONS.loan}LOAN</span><span class="trade-me">${chip(state, me, 26)}<span>${e(upper(me.name))}</span><b>${money(me.cash)}</b></span></div>
    <div class="trade-partners" role="group" aria-label="Lender">${lenders.map((player) => {
      const on = player.id === target?.id;
      return `<button type="button" class="partner-chip${on ? " is-on" : ""}" aria-pressed="${on}" data-lender="${e(player.id)}" style="--pc:${playerColor(state, player)}">
        <span class="partner-chip__token">${playerTokenSvg(state, player, { size: 40 })}</span>
        <span class="partner-chip__text"><span class="partner-chip__name">${e(upper(player.name))}</span><span class="partner-chip__cash">${money(player.cash)}</span></span>
      </button>`;
    }).join("")}</div>
    <div class="loan-dock__body">
      <div class="loan-steps">
        ${stepRow("principal", "GET", money(draft.principal), [-50, -10, 10, 50])}
        ${stepRow("repay", "REPAY", money(draft.repay), [-50, -10, 10, 50])}
        ${stepRow("dueTurns", "TURNS", String(draft.dueTurns), [-1, 1])}
      </div>
      <div class="loan-dock__security"><span class="loan-dock__label">${ICONS.lock}SECURITY</span>${pledges || `<div class="trade-none">${ICONS.deed}NONE</div>`}</div>
      ${target ? `<p class="loan-dock__terms">${e(loanTerms(state, offer, null))}</p>` : ""}
    </div>
    <div class="trade-foot">
      <button type="button" class="big-btn" data-cmd="close">${padGlyph(seat, "B", "Esc")}CLOSE</button>
      <button type="button" class="big-btn big-btn--primary" data-cmd="loan-send" ${valid ? "" : "disabled"}>SEND${padGlyph(seat, "X", "S")}</button>
    </div>
  </div>`;
}

// The PC used to show the loan request as a modal; the drawer replaced it. Same signature.
export const loanOverlayHtml = loanDockHtml;

// `tokens` (optional HTML: who it is about) turns it into the felt-headed card of UX-06, with the
// text as a label under the tokens.
export function confirmOverlayHtml({ title, text, confirm, danger = false, cancel = "KEEP PLAYING", tokens = "", icon = "" }) {
  return `<div class="dialog on-light${tokens ? " dialog--felt" : ""}" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title">
    <div class="dialog__head${danger ? " dialog__head--danger" : ""}" id="confirm-title">${icon && ICONS[icon] ? ICONS[icon] : ""}${e(title)}</div>
    <div class="dialog__body">${tokens ? `<div class="dialog__tokens">${tokens}</div>` : ""}<p class="dialog__lead${tokens ? " dialog__lead--label" : ""}">${e(text)}</p>
      <div class="dialog__actions"><button type="button" class="big-btn${danger ? " big-btn--danger" : " big-btn--primary"}" data-cmd="confirm">${e(confirm)}</button><button type="button" class="big-btn" data-cmd="close" data-autofocus>${e(cancel)}</button></div>
    </div>
  </div>`;
}

// PLAY AGAIN?: the same seats' tokens (a rematch keeps everyone at the table).
export function rematchTokensHtml(state) {
  return (state?.players || []).map((player) => playerTokenSvg(state, player, { size: 76, label: `${player.name}'s token` })).join("");
}

// Focus starts on the standings, not PLAY AGAIN: people are often still pressing Enter / A from
// the last turn when the game ends, and that press must not start a rematch (it asks first too).
export function victoryOverlayHtml(state, ctx = {}) {
  const winner = playerById(state, state.winnerId);
  const rows = standings(state).map((entry) => `<li class="final"><span class="final__place">${entry.placeLabel}</span>${chip(state, entry.player, 22)}<span class="final__name">${e(entry.player.name)}</span><span class="final__how">${entry.player.bankrupt ? "OUT" : `${money(entry.worth)} · ${ICONS.deed}${ownedBy(state, entry.player.id).length}`}</span></li>`).join("");
  return `<div class="victory" role="dialog" aria-modal="true" aria-labelledby="victory-title">
    <div class="victory__confetti" aria-hidden="true"></div>
    <div class="victory__stage">
      <div class="victory__token"><div class="spotlight"></div><div class="react-pulse">${winner ? playerTokenSvg(state, winner, { size: 150, label: `${winner.name}'s token` }) : ""}</div></div>
      <div class="medal"><span class="medal__eyebrow">WINNER</span><span class="medal__place">1ST</span></div>
    </div>
    <h2 class="victory__title" id="victory-title">${e(upper(winner?.name || ""))} OWNS THE TOWN</h2>
    <ol class="victory__list on-light" tabindex="-1" data-autofocus>${rows}</ol>
    <div class="victory__actions">
      ${ctx.watching ? "" : `<button type="button" class="big-btn big-btn--primary" data-cmd="rematch">${ICONS.resume}PLAY AGAIN</button>`}
      <button type="button" class="big-btn big-btn--ghost" data-cmd="close">SEE THE BOARD</button>
      ${ctx.watching ? "" : '<button type="button" class="big-btn big-btn--ghost big-btn--small" data-cmd="new-table">NEW TABLE</button>'}
    </div>
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// MENU (button top-right, Esc, or Start on a pad): RESUME, SETTINGS, HOW TO PLAY, LEAVE TABLE,
// END GAME. Modal, centred over the dimmed board. ctx: { watching, canEnd }.

// Whether a player may declare bankruptcy now: a person still in a game in play, once the turn
// order is set (the engine's rule; computers decide for themselves).
export function canResign(state, playerId) {
  if (state?.game) return false; // poker and Ludo have no bankruptcy
  const player = playerById(state, playerId);
  return Boolean(player && !player.bankrupt && inputOf(player) !== "ai" && state?.status === "playing"
    && !["determining_order", "order_countdown", "finished"].includes(state.phase));
}

// The confirmation's words: where their property goes.
export function resignText(state, playerId) {
  const player = playerById(state, playerId);
  const owes = ["debt", "loan_offer"].includes(state?.phase) && state.debt?.payerId === playerId;
  const creditor = owes ? playerById(state, state.debt?.creditorId) : null;
  return `${player?.name || "This player"} will be out of the game, and their property goes to ${creditor && !creditor.bankrupt ? creditor.name : "the bank"}.`;
}

export function menuOverlayHtml(state, ctx = {}) {
  const playing = state?.status === "playing";
  const item = (cmd, icon, label, { primary = false, danger = false, disabled = false, note = "" } = {}) => `<button type="button" class="menu-item${primary ? " menu-item--primary" : ""}${danger ? " menu-item--danger" : ""}" data-cmd="${cmd}" ${disabled ? "disabled" : ""}${primary ? " data-autofocus" : ""}>${ICONS[icon] || ""}<span>${label}</span>${note ? `<small>${e(note)}</small>` : ""}</button>`;
  const items = [
    item("close", "resume", "RESUME", { primary: true }),
    item("menu-settings", "gear", "SETTINGS"),
    item("menu-help", "help", "HOW TO PLAY"),
  ];
  if (ctx.controls) items.push(item("menu-controls", "gamepad", "CONTROLS"));
  if (ctx.host) items.push(item("menu-players", "crown", "PLAYERS", { note: "HOST" }));
  if (!ctx.watching && state?.status !== "lobby") items.push(item("menu-leave", "out", "LEAVE TABLE"));
  // In the lobby: start over with a new table and code (0.10.2).
  if (!ctx.watching && state?.status === "lobby") items.push(item("menu-new", "screen", "NEW TABLE", { note: "NEW CODE" }));
  // Declare bankruptcy (resign) for a person seated here, at any time in play (0.10.1).
  if (!ctx.watching) {
    for (const seat of (state?.localSeats || []).filter((candidate) => canResign(state, candidate.playerId))) {
      const name = playerById(state, seat.playerId)?.name || "";
      items.push(`<button type="button" class="menu-item menu-item--danger" data-cmd="menu-bankrupt" data-arg="${e(seat.seatId)}">${ICONS.out || ""}<span>DECLARE BANKRUPTCY · ${e(upper(name))}</span></button>`);
    }
  }
  if (!ctx.watching && playing) items.push(item("menu-end", "crown", "END GAME", { danger: true, disabled: ctx.canEnd === false, note: ctx.canEnd === false ? "HOST ONLY" : "" }));
  return `<div class="dialog dialog--menu on-light" role="dialog" aria-modal="true" aria-labelledby="menu-title">
    <div class="dialog__head" id="menu-title">MENU${state?.code ? `<span class="menu-code">TABLE <b>${e(state.code)}</b></span>` : ""}</div>
    <div class="dialog__body menu-list">${items.join("")}<p class="credits-line credits-line--menu">PHOTOS · WIKIMEDIA COMMONS · /shared/photos/CREDITS.txt</p></div>
  </div>`;
}

// SETTINGS. sound = sound.getSettings(); screen = settings.js values; ctx: { computerSpeed,
// canComputerSpeed, fullscreen, fullscreenSupported, systemReduced (the computer asks for reduced
// motion, so the camera is off), install (installState() from shared/pwa.js) }.
export function settingsOverlayHtml(sound = {}, screen = {}, ctx = {}) {
  const pct = (value) => Math.round((Number(value) || 0) * 100);
  const toggle = (key, on, disabled = false) => `<button type="button" class="set-toggle${on ? " is-on" : ""}" data-set="${key}" aria-pressed="${on}" ${disabled ? "disabled" : ""}><span class="set-toggle__knob"></span><b>${on ? "ON" : "OFF"}</b></button>`;
  const choice = (key, options, current, disabled = false) => `<span class="set-choice" role="group">${options.map(([value, label]) => `<button type="button" class="set-choice__opt${String(current) === String(value) ? " is-on" : ""}" data-set="${key}" data-value="${value}" aria-pressed="${String(current) === String(value)}" ${disabled ? "disabled" : ""}>${label}</button>`).join("")}</span>`;
  const slider = (key, label, value, disabled) => `<input class="set-slider" type="range" min="0" max="100" step="5" value="${pct(value)}" data-volume="${key}" aria-label="${label} volume" ${disabled ? "disabled" : ""}>`;
  const row = (icon, label, control, cls = "") => `<div class="set-row${cls}"><span class="set-row__label">${ICONS[icon] || ""}${label}</span><span class="set-row__control">${control}</span></div>`;
  const on = !sound.muted;
  return `<div class="dialog dialog--settings on-light" role="dialog" aria-modal="true" aria-labelledby="settings-title">
    <div class="dialog__head" id="settings-title">${ICONS.gear}SETTINGS</div>
    <div class="dialog__body set-list">
      <h3 class="set-head">SOUND</h3>
      ${row(on ? "speaker" : "speakerOff", "SOUND", toggle("sound", on))}
      ${row("note", "MUSIC", toggle("music", Boolean(sound.musicOn), !on) + slider("music", "Music", sound.music, !on || !sound.musicOn))}
      ${row("bell", "EFFECTS", toggle("effects", Boolean(sound.effectsOn), !on) + slider("effects", "Effects", sound.effects, !on || !sound.effectsOn))}
      <h3 class="set-head">CAMERA AND MOTION</h3>
      ${row("camera", "CAMERA", choice("camera", [["follow", "FOLLOW MOVES"], ["closeups", "CLOSE-UPS ONLY"], ["off", "OFF"]], screen.camera || "follow"), " set-row--wide")}
      ${ctx.systemReduced && screen.camera !== "off" ? `<p class="set-note" role="note">${ICONS.pause}The camera is off: this computer has reduced motion turned on. Set REDUCED MOTION to OFF below to use the camera here.</p>` : ""}
      ${row("search", "ZOOM", choice("zoom", [[1.6, "1.6×"], [2, "2×"], [2.4, "2.4×"]], screen.zoom ?? 2, screen.camera === "off"))}
      ${row("dice", "ANIMATIONS", choice("speed", [["normal", "NORMAL"], ["fast", "FAST"]], screen.speed || "normal"))}
      ${row("pause", "REDUCED MOTION", choice("motion", [["system", "SYSTEM"], ["reduce", "ON"], ["full", "OFF"]], screen.motion || "system"))}
      <h3 class="set-head">TABLE</h3>
      ${row("cpu", "COMPUTER SPEED", choice("computerSpeed", [["slow", "SLOW"], ["normal", "NORMAL"], ["fast", "FAST"]], ctx.computerSpeed || "normal", !ctx.canComputerSpeed))}
      ${row("swap", "COMPUTER OFFERS TO PEOPLE", choice("botOffers", [["normal", "NORMAL"], ["fewer", "FEWER"], ["off", "OFF"]], ctx.botOffers || "normal", !ctx.canComputerSpeed))}
      ${row("screen", "FULL SCREEN", toggle("fullscreen", Boolean(ctx.fullscreen), ctx.fullscreenSupported === false))}
      ${ctx.install === "prompt" ? row("screen", "INSTALL APP", '<span class="set-choice" role="group"><button type="button" class="set-choice__opt" data-set="install">INSTALL</button></span>') : ctx.install === "desktop" ? `<p class="set-note" role="note">${ICONS.screen || ""}To install Estate Table as its own full-screen window, click the install icon at the right of the address bar.</p>` : ""}
      ${ctx.listed === null || ctx.listed === undefined ? "" : row("globe", "PUBLIC GAME", toggle("listed", Boolean(ctx.listed), !ctx.canComputerSpeed))}
    </div>
    <div class="dialog__actions dialog__actions--foot"><button type="button" class="big-btn big-btn--primary" data-cmd="menu-back" data-autofocus>${ICONS.arrowLeft}BACK</button></div>
  </div>`;
}

// HOW TO PLAY: the controls (keyboard key and pad button) and five rule reminders.
export function helpOverlayHtml(state) {
  const rules = rulesOf(state);
  const key = (text) => `<b class="key-cap">${e(text)}</b>`;
  const pad = (button) => `<span class="glyph glyph--sm" style="${glyphStyle(G[button] || G.MENU)}">${e(button === "VIEW" ? "⧉" : button === "START" ? "☰" : button)}</span>`;
  const controls = [
    // The one key map (UX-01, CONTROLS): the pad button's letter on the keyboard too.
    ["Roll · buy · pay · end turn", ["A", "↵"], ["A"]],
    ["Auction · build", ["X"], ["X"]],
    ["Trade", ["Y"], ["Y"]],
    ["Deeds", ["V"], ["VIEW"]],
    ["Bankrupt (in debt)", ["Q"], ["Y"]],
    ["Bid ±$10 · ±$50", ["+", "−", "[", "]"], ["RB", "LB", "RT", "LT"]],
    ["Move · choose", ["←↑↓→", "↵"], ["✛", "A"]],
    ["Back · menu", ["Esc", "M"], ["B", "START"]],
  ].map(([label, keys, buttons]) => `<tr><th>${e(label)}</th><td>${keys.map(key).join(" ")}</td><td>${buttons.map((button) => (button === "✛" ? key("✛") : pad(button))).join(" ")}</td></tr>`).join("");
  const tips = [
    "Pass PAYDAY to collect $200.",
    "Own a whole colour set to build; build evenly across it.",
    rules.auctions ? "A street nobody buys goes to auction." : "A street nobody buys stays with the bank.",
    "Three doubles in a row sends you to jail.",
    "Can't pay? Sell houses or mortgage; the last player standing wins.",
  ].map((tip) => `<li>${e(tip)}</li>`).join("");
  return `<div class="dialog dialog--help on-light" role="dialog" aria-modal="true" aria-labelledby="help-title">
    <div class="dialog__head" id="help-title">HOW TO PLAY</div>
    <div class="dialog__body help">
      <table class="help__keys"><thead><tr><th></th><th>${ICONS.keyboard}KEYS</th><th>${ICONS.pad}PAD</th></tr></thead><tbody>${controls}</tbody></table>
      <ol class="help__rules">${tips}</ol>
    </div>
    <div class="dialog__actions dialog__actions--foot"><span class="credits-line">PHOTOS · WIKIMEDIA COMMONS · /shared/photos/CREDITS.txt</span><button type="button" class="big-btn big-btn--primary" data-cmd="menu-back" data-autofocus>${ICONS.arrowLeft}BACK</button></div>
  </div>`;
}

// END GAME confirm: the richest player still in wins.
export function endGameLeader(state) {
  const top = standings(state).find((entry) => !entry.player.bankrupt) || null;
  return top ? { player: top.player, worth: top.worth } : null;
}

export function endGameConfirmHtml(state) {
  const leader = endGameLeader(state);
  const lead = leader ? `<div class="confirm-leader">${chip(state, leader.player, 34)}<span class="confirm-leader__name">${e(upper(leader.player.name))}</span><span class="confirm-leader__worth">${ICONS.crown}${money(leader.worth)}</span></div>` : "";
  return `<div class="dialog on-light" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title">
    <div class="dialog__head dialog__head--danger" id="confirm-title">END THE GAME NOW?</div>
    <div class="dialog__body"><p class="dialog__lead">The game ends for everyone. The richest player wins, counting loans and any unpaid debt:</p>
      ${lead}
      <div class="dialog__actions"><button type="button" class="big-btn big-btn--danger" data-cmd="confirm">END GAME</button><button type="button" class="big-btn" data-cmd="close" data-autofocus>KEEP PLAYING</button></div>
    </div>
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// Trade, big and central (about 1200 × 760 over the dimmed board): the builder and an incoming
// offer for a seat at this screen. Both show every deed with its rent, each side's holdings by
// colour set, a mini-map with the deal's squares, the focused deed's full card, and what the deal
// does to each side (tradeImpact: cash, net worth, sets, rents).

function dealGroups(state, indexes) {
  const order = [];
  const byKey = new Map();
  for (const index of [...indexes].sort((a, b) => a - b)) {
    const space = state.board[index];
    const key = space?.type === "property" ? space.group : space?.type || "other";
    if (!byKey.has(key)) {
      byKey.set(key, []);
      order.push(key);
    }
    byKey.get(key).push(index);
  }
  return order.map((key) => ({ key, indexes: byKey.get(key) }));
}

function groupHeadHtml(state, key, ownerId) {
  const total = key === "railroad" ? 4 : key === "utility" ? 2 : groupIndexes(state, key).length;
  const owned = key === "railroad" ? [5, 15, 25, 35].filter((index) => propState(state, index)?.ownerId === ownerId).length
    : key === "utility" ? [12, 28].filter((index) => propState(state, index)?.ownerId === ownerId).length
      : groupCount(state, ownerId, key);
  const colour = key === "railroad" ? "#2B323A" : key === "utility" ? "#5B6470" : GROUP_COLORS[key];
  const label = key === "railroad" ? "RAILROADS" : key === "utility" ? "UTILITIES" : colourWord(key).toUpperCase();
  return `<div class="deal-group"><span class="deal-group__swatch" style="background:${colour}"></span><span>${e(label)}</span><small>${owned === total ? "FULL SET" : `${owned} OF ${total}`}</small></div>`;
}

function dealRowHtml(state, index, { field = null, picked = false, receiverId = null, warn = false, nav = null } = {}) {
  const space = state.board[index];
  const prop = propState(state, index);
  const sets = completesSet(state, receiverId, index);
  const rent = rentFor(state, index);
  const rentText = prop?.mortgaged ? "MORTGAGED" : space?.type === "utility" ? `${utilityTimes(state, index)}× DICE` : rent === null ? "" : `RENT ${money(rent)}`;
  const badge = sets ? `<span class="deed-row__badge deed-row__badge--${warn ? "warn" : "good"}">${warn ? "GIVES SET" : "SET"}</span>` : "";
  const inner = `<span class="deed-row__bar" aria-hidden="true"></span><span class="deed-row__name">${e(upper(shortName(space?.name || "")))}</span><span class="deal-row__rent">${e(rentText)}</span>${badge}<b class="deed-row__price">${money(space?.price || 0)}</b>`;
  if (field) {
    return `<button type="button" class="deed-row deal-row${picked ? " is-on" : ""}${prop?.mortgaged ? " is-mortgaged" : ""}" data-toggle="${field}:${index}" data-nav="${field}" data-deed-info="${index}" aria-pressed="${picked}" style="--gc:${spaceColour(space)}" title="${e(space?.name || "")}">${inner}<span class="deed-row__tick" aria-hidden="true">${ICONS.check}</span></button>`;
  }
  return `<div class="deed-row deal-row deed-row--static${prop?.mortgaged ? " is-mortgaged" : ""}" tabindex="0" data-deed-info="${index}"${nav ? ` data-nav="${nav}"` : ""} style="--gc:${spaceColour(space)}" title="${e(space?.name || "")}">${inner}</div>`;
}

function utilityTimes(state, index) {
  const owner = propState(state, index)?.ownerId;
  return [12, 28].filter((candidate) => owner && propState(state, candidate)?.ownerId === owner).length === 2 ? 10 : 4;
}

function holdingsHtml(state, ownerId, indexes, rowOptions, locked = []) {
  const groups = dealGroups(state, [...indexes, ...locked.map((entry) => entry.index)]);
  const lockedMap = new Map(locked.map((entry) => [entry.index, entry.reason]));
  return groups.map((group) => `${groupHeadHtml(state, group.key, ownerId)}${group.indexes.map((index) => {
    if (lockedMap.has(index)) {
      const space = state.board[index];
      return `<div class="deed-row deal-row is-locked" style="--gc:${spaceColour(space)}" aria-disabled="true" data-deed-info="${index}"><span class="deed-row__bar" aria-hidden="true"></span><span class="deed-row__name">${e(upper(shortName(space?.name || "")))}</span><span class="deed-row__why">${ICONS.lock}${e(lockedMap.get(index))}</span></div>`;
    }
    return dealRowHtml(state, index, rowOptions(index));
  }).join("")}`).join("");
}

function impactSideHtml(state, side, you) {
  const player = playerById(state, side.playerId);
  const delta = side.worthAfter - side.worthBefore;
  const lines = [...side.verdicts.slice(0, 3), ...side.rentChanges.slice(0, 3).map((change) => change.text)];
  return `<div class="impact__side${you ? " is-you" : ""}">
    <div class="impact__who">${chip(state, player, 26)}<b>${e(you ? "YOU" : upper(side.name))}</b></div>
    <div class="impact__nums">
      <span class="impact__num"><small>CASH</small>${money(side.cashBefore)} → <b>${money(side.cashAfter)}</b></span>
      <span class="impact__num"><small>NET WORTH</small>${money(side.worthBefore)} → <b class="${delta > 0 ? "is-up" : delta < 0 ? "is-down" : ""}">${money(side.worthAfter)}</b></span>
    </div>
    <ul class="impact__lines">${lines.length ? lines.map((line) => `<li>${e(line)}</li>`).join("") : "<li>No set or rent changes.</li>"}</ul>
  </div>`;
}

function impactHtml(state, trade, meId) {
  if (!trade) return `<div class="impact impact--empty">${ICONS.swap}PICK A PLAYER, THEN DEEDS OR CASH: THIS SHOWS WHAT THE DEAL DOES TO EACH SIDE</div>`;
  const impact = tradeImpact(state, trade, { viewerId: meId });
  const mine = impact.proposer.playerId === meId ? impact.proposer : impact.target;
  const theirs = mine === impact.proposer ? impact.target : impact.proposer;
  return `<div class="impact">${impactSideHtml(state, mine, true)}${impactSideHtml(state, theirs, false)}</div>`;
}

function peekHtml(state, index, viewerId) {
  return Number.isInteger(index) ? deedDetailHtml(state, index, { viewerId, size: "sm" }) : `<div class="trade-peek__hint">${ICONS.deed}<span>Move to a deed to see its full card</span></div>`;
}

export function tradeModalHtml(state, seat, draft, ctx = {}) {
  const me = playerById(state, seat.playerId);
  const partners = state.players.filter((player) => player.id !== me.id && !player.bankrupt);
  const status = tradeDraftStatus(state, me.id, draft);
  const target = status.target && partners.some((player) => player.id === status.target.id) ? status.target : null;
  const partnerRow = partners.length
    ? partners.map((player) => {
      const on = target?.id === player.id;
      const ai = inputOf(player) === "ai";
      return `<button type="button" class="partner-chip${on ? " is-on" : ""}" data-partner="${e(player.id)}" data-nav="partners" aria-pressed="${on}" style="--pc:${playerColor(state, player)}">
        <span class="partner-chip__token">${playerTokenSvg(state, player, { size: 40 })}${ai ? `<span class="partner-chip__bot" title="Computer">${ICONS.cpu}</span>` : ""}</span>
        <span class="partner-chip__text"><span class="partner-chip__name">${e(upper(player.name))}</span><span class="partner-chip__cash">${money(player.cash)} · ${ICONS.deed}${ownedBy(state, player.id).length}</span></span>
      </button>`;
    }).join("")
    : `<div class="trade-none">${ICONS.swap}NO ONE</div>`;
  const giveList = holdingsHtml(state, me.id, status.mine, (index) => ({ field: "offer", picked: status.offer.includes(index), receiverId: target?.id, warn: true }), lockedDeeds(state, me.id));
  const getList = target ? holdingsHtml(state, target.id, status.theirs, (index) => ({ field: "request", picked: status.request.includes(index), receiverId: me.id }), lockedDeeds(state, target.id)) : "";
  const give = `<section class="trade-col trade-col--give" data-col="offer" aria-label="You give">
      <header class="trade-col__head">${chip(state, me, 24)}<span>YOU GIVE</span><small>YOUR DEEDS</small></header>
      <div class="trade-col__deeds">${giveList || `<div class="trade-none">${ICONS.deed}NONE</div>`}</div>
      ${stepper("offer", status.offerCash, me.cash, "Cash you give", { seat })}
    </section>`;
  const get = `<section class="trade-col trade-col--get" data-col="request" aria-label="You get">
      <header class="trade-col__head">${target ? chip(state, target, 24) : ""}<span>YOU GET</span><small>${target ? `${e(upper(possessive(target.name)))} DEEDS` : ""}</small></header>
      <div class="trade-col__deeds">${target ? getList || `<div class="trade-none">${ICONS.deed}NONE</div>` : `<div class="trade-none trade-none--pick">${ICONS.up}PICK A PLAYER</div>`}</div>
      ${stepper("request", status.requestCash, target?.cash || 0, "Cash you get", { disabled: !target, seat })}
    </section>`;
  const trade = target && (status.offer.length || status.request.length || status.offerCash || status.requestCash)
    ? { proposerId: me.id, targetId: target.id, offer: { cash: status.offerCash, properties: status.offer }, request: { cash: status.requestCash, properties: status.request } }
    : null;
  const why = status.valid || !target ? "" : `<span class="trade-why">${e(status.reason)}</span>`;
  const price = priceHtml(state, seat, target, status, draft?.quote);
  const map = minimapHtml(state, { give: status.offer, get: status.request });
  const peek = Number.isInteger(ctx.peek) ? ctx.peek : [...status.request, ...status.offer][0];
  return `<div class="trade-modal"><div class="trade-dock trade-dock--modal trade-builder" role="dialog" aria-modal="true" aria-labelledby="trade-title">
    <div class="trade-dock__head"><span id="trade-title" class="trade-title">${ICONS.swap}TRADE</span><span class="trade-me">${chip(state, me, 26)}<span>${e(upper(me.name))}</span><b>${money(me.cash)}</b></span></div>
    <div class="trade-partners" role="group" aria-label="Trade with">${partnerRow}</div>
    <div class="trade-main">
      <div class="trade-cols">${give}${get}</div>
      <aside class="trade-side"><div class="trade-map" aria-hidden="true">${map}</div><div class="trade-peek" data-peek>${peekHtml(state, peek, me.id)}</div></aside>
    </div>
    ${tradeSumHtml(state, trade, me.id)}
    ${impactHtml(state, trade, me.id)}
    <div class="trade-foot">
      ${price ? `<span class="trade-foot__price">${price}</span>` : ""}${why}
      <button type="button" class="big-btn" data-cmd="close" data-nav="foot">${padGlyph(seat, "B", "Esc")}CANCEL</button>
      <button type="button" class="big-btn big-btn--primary" data-cmd="trade-send" data-nav="foot" ${status.valid ? "" : "disabled"}>SEND${padGlyph(seat, "X", "S")}</button>
    </div>
  </div></div>`;
}

// What the deal is worth to `meId`. A colour set changing hands is what matters, not list prices
// (review U8: a deal completing your set showed a mostly red bar): one line per set made (green
// yours, red theirs) with the most it can charge, list values small. Otherwise the price bar.
export function tradeSumHtml(state, trade, meId) {
  const deal = trade ? dealBalance(state, trade, meId) : { give: 0, get: 0, sets: [] };
  const made = deal.sets.filter((set) => set.gain).sort((a, b) => Number(b.you) - Number(a.you)).slice(0, 2);
  if (made.length) {
    const lines = made.map((set) => `<span class="set-line set-line--${set.you ? "good" : "bad"}"><i style="background:${GROUP_COLORS[set.group]}"></i><span>${set.you ? "YOUR" : e(upper(possessive(set.name)))} ${e(upper(colourWord(set.group)))} SET</span><b>UP TO ${money(set.rent)}</b></span>`).join("");
    return `<div class="trade-sum trade-sum--sets" aria-label="You give ${money(deal.give)}, you get ${money(deal.get)} at list prices">${lines}<span class="trade-sum__list">LIST ${money(deal.give)} ⇄ ${money(deal.get)}</span></div>`;
  }
  const total = Math.max(1, deal.give + deal.get);
  const empty = !deal.give && !deal.get;
  return `<div class="trade-sum">
      <span class="trade-sum__give">${money(deal.give)}</span>
      <div class="value-bar${empty ? " is-empty" : ""}" role="img" aria-label="You give ${money(deal.give)}, you get ${money(deal.get)}"><span style="flex:${empty ? 1 : (deal.give / total).toFixed(3)}"></span><span style="flex:${empty ? 1 : (deal.get / total).toFixed(3)}"></span></div>
      <span class="trade-sum__get">${money(deal.get)}</span>
    </div>`;
}

export function offerModalHtml(state, seat, ctx = {}) {
  const trade = state.trade;
  const me = playerById(state, seat.playerId);
  const from = playerById(state, trade.proposerId);
  const gives = trade.request?.properties || [];
  const gets = trade.offer?.properties || [];
  const rows = (indexes, cash, receiverId) => {
    const list = dealGroups(state, indexes).map((group) => `${groupHeadHtml(state, group.key, propState(state, group.indexes[0])?.ownerId)}${group.indexes.map((index) => dealRowHtml(state, index, { receiverId })).join("")}`).join("");
    const money$ = cash ? `<div class="deed-row deal-row deed-row--cash"><span class="deed-row__name">${ICONS.coin}CASH</span><b class="deed-row__price">${money(cash)}</b></div>` : "";
    return list + money$ || `<div class="trade-none">${ICONS.deed}NOTHING</div>`;
  };
  const peek = Number.isInteger(ctx.peek) ? ctx.peek : [...gets, ...gives][0];
  // A computer's counter-offer answers your own offer: what changed, its words, ACCEPT / CHANGE /
  // WALK AWAY. A computer's own offer for your deeds has NOT SELLING <deed> buttons.
  const counter = Boolean(trade.counterTo) && inputOf(from) === "ai";
  const changes = counter ? counterChanges(state, trade, me.id) : [];
  const reply = counter ? lastReply(state, from.id) : "";
  const title = counter ? `${e(upper(possessive(from?.name || "")))} COUNTER-OFFER` : "TRADE OFFER";
  const replyHtml = counter
    ? `<div class="offer-reply" role="status"><span class="offer-reply__who">${chip(state, from, 22)}${e(upper(from?.name || ""))}</span>${reply ? `<q>${e(reply)}</q>` : ""}${changes.map((c) => `<span class="offer-change offer-change--${c.tone}">${e(c.text)}</span>`).join("")}</div>`
    : "";
  const keep = !counter && inputOf(from) === "ai"
    ? gives.map((index) => `<button type="button" class="big-btn big-btn--quiet" data-cmd="offer-not-selling" data-arg="${index}" title="No computer asks for it again until you turn it back on (its deed card)">${ICONS.lock}NOT SELLING ${e(upper(shortName(state.board[index]?.name || "")))}</button>`).join("")
    : "";
  const foot = counter
    ? `<button type="button" class="big-btn" data-cmd="offer-decline">${padGlyph(seat, "X", "X")}WALK AWAY</button>
      <button type="button" class="big-btn" data-cmd="offer-change">${padGlyph(seat, "Y", "C")}CHANGE</button>
      <button type="button" class="big-btn big-btn--primary" data-cmd="offer-accept" data-autofocus>ACCEPT${padGlyph(seat, "A", "↵")}</button>`
    : `${keep}<button type="button" class="big-btn" data-cmd="offer-decline">${padGlyph(seat, "X", "X")}DECLINE</button>
      <button type="button" class="big-btn big-btn--primary" data-cmd="offer-accept" data-autofocus>ACCEPT${padGlyph(seat, "A", "↵")}</button>`;
  return `<div class="trade-modal"><div class="trade-dock trade-dock--modal trade-offer${counter ? " trade-offer--counter" : ""}" role="dialog" aria-modal="true" aria-labelledby="offer-title">
    <div class="trade-dock__head"><span id="offer-title" class="trade-title">${ICONS.swap}${title}</span><span class="trade-me">${chip(state, from, 30)}<span>${e(upper(from?.name || ""))}</span>${ICONS.arrow}${chip(state, me, 30)}<span>${e(upper(me.name))}</span></span></div>
    ${replyHtml}
    <div class="trade-main">
      <div class="trade-cols">
        <section class="trade-col trade-col--give" aria-label="You give"><header class="trade-col__head">${chip(state, me, 24)}<span>YOU GIVE</span></header><div class="trade-col__deeds">${rows(gives, trade.request?.cash || 0, trade.proposerId)}</div></section>
        <section class="trade-col trade-col--get" aria-label="You get"><header class="trade-col__head">${chip(state, from, 24)}<span>YOU GET</span></header><div class="trade-col__deeds">${rows(gets, trade.offer?.cash || 0, me.id)}</div></section>
      </div>
      <aside class="trade-side"><div class="trade-map" aria-hidden="true">${minimapHtml(state, { give: gives, get: gets })}</div><div class="trade-peek" data-peek>${peekHtml(state, peek, me.id)}</div></aside>
    </div>
    ${tradeSumHtml(state, trade, me.id)}
    ${impactHtml(state, trade, me.id)}
    <div class="trade-foot">
      ${foot}
    </div>
  </div></div>`;
}

// The full deed card as a modal (click any deed, or I / the pad's Y on a focused one).
// Beside it, what the acting seat can do with it (web/shared/deedactions.js, the engine's rules):
// mortgage, unmortgage, build, sell, trade, not selling on your own; ask to buy on someone else's;
// buy the one you're standing on. A greyed action says why in one line.
export function deedViewHtml(state, index, { viewerId = null, seat = null, actor = null } = {}) {
  const who = actor || deedActorSeat(state, index, seat);
  const acts = deedActionsHtml(state, index, who);
  const card = deedDetailHtml(state, index, { viewerId: who?.playerId || viewerId, size: "lg" });
  return `<div class="deed-view__card" role="dialog" aria-modal="true" aria-label="${e(state.board[index]?.name || "Deed")}">
    ${acts ? `<div class="deed-view__row">${card}${acts}</div>` : card}
    <button type="button" class="big-btn deed-view__close" data-cmd="deed-close" data-autofocus>${padGlyph(who || seat || { input: "keyboard" }, "B", "Esc")}CLOSE</button>
  </div>`;
}

// Who acts from a deed's card at this table: the player whose turn it is when they sit here (they
// can act now), else the deed's owner when they sit here, else the seat the card was opened for.
export function deedActorSeat(state, index, fallback = null) {
  const seats = (state?.localSeats || []).filter((candidate) => candidate.playerId);
  const ownerId = propState(state, index)?.ownerId;
  return seats.find((candidate) => candidate.playerId === state.currentPlayerId)
    || seats.find((candidate) => candidate.playerId === ownerId)
    || (fallback?.playerId ? fallback : null);
}

export function deedActionsHtml(state, index, seat) {
  const player = seat?.playerId ? playerById(state, seat.playerId) : null;
  if (!player || inputOf(player) === "ai") return "";
  const { actions } = deedActions(state, player.id, index);
  if (!actions.length) return "";
  const many = (state.localSeats || []).filter((candidate) => candidate.playerId).length > 1;
  const icons = { build: ICONS.house, sell: ICONS.house, mortgage: ICONS.deed, unmortgage: ICONS.deed, trade: ICONS.swap, ask: ICONS.swap, "not-selling": ICONS.lock, buy: ICONS.stamp };
  const buttons = actions.map((action) => {
    const data = action.act ? `data-deed-act="${e(JSON.stringify(action.act))}"` : `data-cmd="${e(action.cmd)}" data-arg="${e(action.arg)}"`;
    const tone = action.tone === "primary" && !action.disabled ? " big-btn--primary" : "";
    const sale = action.id === "not-selling" ? ` deed-view__sale${action.on ? " is-on" : ""}` : "";
    const pressed = action.on !== undefined ? ` aria-pressed="${action.on}"` : "";
    const button = `<button type="button" class="big-btn big-btn--small deed-act${tone}${sale}" ${data} data-seat="${e(seat.seatId)}" data-deed-action="${e(action.id)}"${action.disabled ? " disabled" : ""}${pressed}>${icons[action.id] || ""}<span class="deed-act__label">${e(action.label)}</span>${action.detail ? `<span class="deed-act__detail">${e(action.detail)}</span>` : ""}</button>`;
    return action.disabled && action.reason ? `${button}<p class="deed-act__why" data-why="${e(action.id)}">${e(action.reason)}</p>` : button;
  });
  return `<div class="deed-view__acts">${many ? `<p class="deed-act__for">${e(upper(player.name))}</p>` : ""}${buttons.join("")}</div>`;
}

// On a deed of a person at this table: whether computers may ask for it ("Not selling" on an
// offer turns it off; this turns it back on).
export function notSellingToggleHtml(state, index) {
  const ownerId = propState(state, index)?.ownerId;
  const ownerSeat = (state.localSeats || []).find((candidate) => candidate.playerId === ownerId);
  const owner = playerById(state, ownerId);
  if (!ownerSeat || !owner || inputOf(owner) === "ai" || state.status !== "playing") return "";
  const off = (owner.notSelling || []).includes(index);
  return `<button type="button" class="big-btn deed-view__sale${off ? " is-on" : ""}" data-cmd="deed-not-selling" data-arg="${index}:${off ? "false" : "true"}" data-seat="${e(ownerSeat.seatId)}" aria-pressed="${off}">${ICONS.lock}${off ? "NOT SELLING TO COMPUTERS · SELL AGAIN" : "DON'T SELL TO COMPUTERS"}</button>`;
}

export { signedMoney };
