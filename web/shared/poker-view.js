// Poker table markup shared by the phone (/play) and the table screen (/). Pure: state in, HTML
// strings out, so every fixture can be rendered and checked in Node. Styles: web/shared/poker.css.
//
// The table is an oval felt in a fixed design box that scales with its container (CSS container
// units, poker.css): 360 × 600 units in portrait, 1000 × 560 in landscape. Seats sit around the rail
// by position name (b = bottom centre, then clockwise: bl, l, tl, t, tr, r, br); the viewer is always
// at the bottom. Everything that changes during a hand (bets, labels, cards, the timer ring) is keyed
// with data-key so shared/morph.js updates it in place instead of rebuilding it, and the order of
// the nodes never changes during a hand: morph moves a node it finds later in the list, and a moved
// node restarts its CSS animations (cards re-deal, flags pop again: a flash). So every seat always
// has its hole-card slot, its flag slot and its bet slot (empty and hidden when there is nothing),
// bets and seats live in separate containers, and the board comes before the pots.
//
// Wire format: docs/POKER-API.md. Optional fields (settings.actionSeconds, players[].timeBank,
// actionTimeBank, you.strength, settings.style, aiLevels) are read defensively: missing → hidden.

import { GAME_TITLE as CLOSEST_TITLE } from "./closest-name.js";
import { escapeHtml, ordinal, upper } from "./format.js";
import { formatChips, oddsLabel, parseCard } from "./poker.js";
import { TOKEN_COLORS, inkOn, playerColor, tokenGlyphSvg, tokenSvg } from "./tokens.js";

const e = escapeHtml;

// "$1,500". Play money: chips have no value.
export function chips(n) {
  return `$${formatChips(Math.max(0, Math.round(Number(n) || 0)))}`;
}

// ---------------------------------------------------------------------------------------------
// Cards

const SUIT_PATHS = {
  s: "M12 1.5C9.2 5.4 3 9.1 3 13.7c0 2.8 2.2 4.8 4.7 4.8 1.6 0 2.9-.7 3.6-1.8-.3 2.3-1.2 3.9-2.8 5.3h7c-1.6-1.4-2.5-3-2.8-5.3.7 1.1 2 1.8 3.6 1.8 2.5 0 4.7-2 4.7-4.8C21 9.1 14.8 5.4 12 1.5z",
  h: "M12 21.3C10.4 19.8 2.5 14 2.5 8.3 2.5 5.2 4.8 3 7.6 3c1.9 0 3.5 1 4.4 2.6C12.9 4 14.5 3 16.4 3c2.8 0 5.1 2.2 5.1 5.3 0 5.7-7.9 11.5-9.5 13z",
  d: "M12 1.5 20.2 12 12 22.5 3.8 12z",
  c: "M12 2a4.6 4.6 0 0 0-4.4 6 4.6 4.6 0 1 0 2.9 7.9c-.3 2.4-1.2 4.2-2.9 5.6h8.8c-1.7-1.4-2.6-3.2-2.9-5.6A4.6 4.6 0 1 0 16.4 8 4.6 4.6 0 0 0 12 2z",
};

export function suitSvg(suit) {
  return `<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="${SUIT_PATHS[suit] || SUIT_PATHS.s}"/></svg>`;
}

// One playing card. code "Ah" (face up), "back" (face down) or null (an empty board slot).
// opts: cls (extra classes), key (data-key), small (rank over suit, no big pip).
export function cardHtml(code, { cls = "", key = "", small = false, decor = false } = {}) {
  const keyAttr = key ? ` data-key="${e(key)}"` : "";
  if (!code) return `<span class="pc pc--slot ${cls}"${keyAttr} aria-hidden="true"></span>`;
  if (code === "back") return `<span class="pc pc--back ${cls}"${keyAttr} role="img" aria-label="Face-down card"></span>`;
  const card = parseCard(code);
  if (!card) return `<span class="pc pc--slot ${cls}"${keyAttr} aria-hidden="true"></span>`;
  const ten = card.label === "10" ? " pc__rank--10" : "";
  const a11y = decor ? "aria-hidden=\"true\"" : `role="img" aria-label="${e(card.label)} of ${card.suitName}"`;
  return `<span class="pc pc--${card.suit}${small ? " pc--sm" : ""} ${cls}"${keyAttr} ${a11y}><span class="pc__rank${ten}">${e(card.label)}</span><span class="pc__s">${suitSvg(card.suit)}</span><span class="pc__pip">${suitSvg(card.suit)}</span></span>`;
}

// ---------------------------------------------------------------------------------------------
// Chips (the round tokens on the felt, not avatars)

const CHIP_TONES = ["red", "navy", "gold", "ink", "ivory", "green"];
const PILE_AT = [[0, 0], [16, -5], [7, 9], [23, 8], [-5, 11], [12, -13]];

// A small pile of chips, more for bigger amounts (decorative, aria-hidden).
export function pileHtml(amount, { size = 16, max = 4, seed = 0 } = {}) {
  const n = Math.max(1, Math.min(max, amount >= 1000 ? 4 : amount >= 300 ? 3 : amount >= 60 ? 2 : 1));
  const pts = PILE_AT.slice(0, n).map(([x, y]) => [x * size / 30, y * size / 30]);
  const minX = Math.min(...pts.map((p) => p[0]));
  const minY = Math.min(...pts.map((p) => p[1]));
  const w = Math.max(...pts.map((p) => p[0])) - minX + size;
  const h = Math.max(...pts.map((p) => p[1])) - minY + size;
  const body = pts.map(([x, y], i) => `<span class="ch ch--${CHIP_TONES[(i + seed) % CHIP_TONES.length]}" style="left:${(x - minX).toFixed(1)}em;top:${(y - minY).toFixed(1)}em;width:${size}em;height:${size}em"></span>`).join("");
  return `<span class="pile" style="width:${w.toFixed(1)}em;height:${h.toFixed(1)}em" aria-hidden="true">${body}</span>`;
}

// ---------------------------------------------------------------------------------------------
// Game type: tags and the ESTATE / POKER / LUDO / QUIZ picker

const ICON = {
  cards: '<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><rect x="1.5" y="3.5" width="7.5" height="10.5" rx="1.3" transform="rotate(-10 5 9)" fill="none" stroke="currentColor" stroke-width="1.5"/><rect x="6.5" y="2" width="7.5" height="10.5" rx="1.3" fill="currentColor"/></svg>',
  board: '<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><rect x="1.5" y="1.5" width="13" height="13" rx="1" fill="none" stroke="currentColor" stroke-width="1.6"/><rect x="4.5" y="4.5" width="7" height="7" fill="none" stroke="currentColor" stroke-width="1.2"/></svg>',
  ludo: '<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><path d="M6 1.5h4v4.5h4.5v4H10v4.5H6V10H1.5V6H6z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/><circle cx="8" cy="8" r="1.6" fill="currentColor"/></svg>',
  quiz: '<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><rect x="1.5" y="1.5" width="13" height="13" rx="2.2" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M5.9 6.1a2.1 2.1 0 1 1 3 1.9c-.6.3-.9.7-.9 1.4v.4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/><circle cx="8" cy="11.6" r="1" fill="currentColor"/></svg>',
  closest: '<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><path d="M1.5 12.5h13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><rect x="2" y="7" width="3" height="3.6" rx=".6" fill="none" stroke="currentColor" stroke-width="1.2"/><rect x="6.5" y="7" width="3" height="3.6" rx=".6" fill="currentColor"/><rect x="11" y="7" width="3" height="3.6" rx=".6" fill="none" stroke="currentColor" stroke-width="1.2"/><path d="M8 1.5v3.2M6.3 3.2 8 4.9l1.7-1.7" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  tick: '<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><path d="M2.5 8.5 6.2 12 13.5 4" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/></svg>',
  bot: '<svg viewBox="0 0 16 16" aria-hidden="true" focusable="false" fill="currentColor"><circle cx="8" cy="1.6" r="1.1"/><path d="M8 1.2v2.3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><rect x="2.5" y="4" width="11" height="9" rx="2.4"/><circle cx="5.9" cy="8.2" r="1.3" fill="#15191E"/><circle cx="10.1" cy="8.2" r="1.3" fill="#15191E"/></svg>',
};
export const POKER_ICONS = ICON;

// Every game type, with the name its tile and tag show (Near Thing: closest-view.js GAME_TITLE).
export const GAME_TYPES = Object.freeze(["estate", "poker", "ludo", "quiz", "closest"]);
const GAME_LABELS = Object.freeze({ estate: "ESTATE", poker: "POKER", ludo: "LUDO", quiz: "QUIZ", closest: CLOSEST_TITLE });

export function gameTypeOf(stateOrSummary) {
  const value = stateOrSummary?.gameType || stateOrSummary?.game;
  return GAME_TYPES.includes(value) ? value : "estate";
}

// "NEAR THING" for "closest": the name on a tile, a tag or a "SWITCH TO …" confirm.
export function gameLabel(gameType) {
  return GAME_LABELS[gameType] || GAME_LABELS.estate;
}

// ESTATE / POKER / LUDO / QUIZ / NEAR THING tag for lists and headers.
export function gtypeTag(gameType) {
  if (gameType === "ludo") return `<span class="gtype gtype--ludo">${ICON.ludo}LUDO</span>`;
  if (gameType === "quiz") return `<span class="gtype gtype--quiz">${ICON.quiz}QUIZ</span>`;
  if (gameType === "closest") return `<span class="gtype gtype--closest">${ICON.closest}${GAME_LABELS.closest}</span>`;
  return gameType === "poker"
    ? `<span class="gtype gtype--poker">${ICON.cards}POKER</span>`
    : `<span class="gtype">${ICON.board}ESTATE</span>`;
}

// LUDO's tile: a small cross board in four of the player colours, a piece on it.
const LUDO_ART = '<svg class="art-ludo__svg" viewBox="0 0 30 30" aria-hidden="true" focusable="false"><rect x="1" y="1" width="28" height="28" rx="3" fill="#FBFAF4" stroke="#15191E" stroke-width="1.6"/><rect x="2.6" y="2.6" width="9.4" height="9.4" rx="1.6" fill="#0E8C8C"/><rect x="18" y="2.6" width="9.4" height="9.4" rx="1.6" fill="#7A1838"/><rect x="2.6" y="18" width="9.4" height="9.4" rx="1.6" fill="#8A5CF5"/><rect x="18" y="18" width="9.4" height="9.4" rx="1.6" fill="#6F7A1A"/><path d="M12 15 15 12 18 15 15 18z" fill="#F2C14E" stroke="#15191E" stroke-width=".9"/><circle cx="7.3" cy="7.3" r="2.2" fill="#FBFAF4"/><circle cx="22.7" cy="22.7" r="2.2" fill="#FBFAF4"/></svg>';

// QUIZ's tile: a corner of the board, three category colour bars over value cells, one gold.
const QUIZ_ART = '<svg class="art-quiz__svg" viewBox="0 0 30 30" aria-hidden="true" focusable="false"><rect x="1" y="1" width="28" height="28" rx="3" fill="#13294B" stroke="#15191E" stroke-width="1.6"/><rect x="3" y="3" width="7.4" height="4" rx=".8" fill="#E8232A"/><rect x="11.3" y="3" width="7.4" height="4" rx=".8" fill="#1FAE57"/><rect x="19.6" y="3" width="7.4" height="4" rx=".8" fill="#1166B4"/><g fill="#FBFAF4"><rect x="3" y="8.6" width="7.4" height="5.4" rx=".8"/><rect x="11.3" y="8.6" width="7.4" height="5.4" rx=".8"/><rect x="19.6" y="8.6" width="7.4" height="5.4" rx=".8"/><rect x="3" y="15.4" width="7.4" height="5.4" rx=".8"/><rect x="19.6" y="15.4" width="7.4" height="5.4" rx=".8"/><rect x="3" y="22.2" width="7.4" height="4.8" rx=".8"/><rect x="11.3" y="22.2" width="7.4" height="4.8" rx=".8"/><rect x="19.6" y="22.2" width="7.4" height="4.8" rx=".8"/></g><rect x="11.3" y="15.4" width="7.4" height="5.4" rx=".8" fill="#F2C14E"/><path d="m15 16.2.75 1.55 1.7.2-1.25 1.15.33 1.68L15 19.95l-1.53.83.33-1.68-1.25-1.15 1.7-.2z" fill="#15191E"/></svg>';

// NEAR THING's tile: guesses lined up as evenly spaced slots, a gold marker dropping onto the
// closest, and a chip under it.
const CLOSEST_ART = '<svg class="art-closest__svg" viewBox="0 0 30 30" aria-hidden="true" focusable="false"><rect x="1" y="1" width="28" height="28" rx="3" fill="#14304A" stroke="#15191E" stroke-width="1.6"/><path d="M4 22.5h22" stroke="#FBFAF4" stroke-width="1.4" stroke-linecap="round" opacity=".55"/><g fill="#FBFAF4" stroke="#15191E" stroke-width=".8"><rect x="3.4" y="14" width="6.2" height="6.6" rx="1"/><rect x="19.4" y="14" width="6.2" height="6.6" rx="1"/></g><rect x="11.4" y="14" width="6.2" height="6.6" rx="1" fill="#F2C14E" stroke="#15191E" stroke-width=".8"/><path d="M14.5 4.2v6.2m-2.4-2.6 2.4 2.6 2.4-2.6" fill="none" stroke="#F2C14E" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/><circle cx="14.5" cy="25.6" r="2.3" fill="#E8232A" stroke="#15191E" stroke-width=".8"/><circle cx="14.5" cy="25.6" r="1" fill="#FBFAF4"/></svg>';

function gameArt(kind) {
  if (kind === "closest") return `<span class="gtile__art art-closest" aria-hidden="true">${CLOSEST_ART}</span>`;
  if (kind === "quiz") return `<span class="gtile__art art-quiz" aria-hidden="true">${QUIZ_ART}</span>`;
  if (kind === "ludo") return `<span class="gtile__art art-ludo" aria-hidden="true">${LUDO_ART}</span>`;
  if (kind === "poker") return `<span class="gtile__art art-cards" aria-hidden="true">${cardHtml("Kh", { decor: true })}${cardHtml("As", { decor: true })}<span class="ch ch--gold"></span></span>`;
  return '<span class="gtile__art art-board" aria-hidden="true"><span class="art-board__sq"><i></i><i></i><i></i><i></i></span></span>';
}

// Five tiles: ESTATE (property board game), POKER (Texas Hold'em), LUDO (race your pieces home),
// QUIZ (four choices, fastest right answer) and NEAR THING (guess the number, back the closest).
// `picked` is the chosen one. The grid keeps its gpick--3 and gpick--4 classes (the earlier rules)
// and adds gpick--5 (closest.css: five across on the table screen; on a phone the fifth tile takes
// the whole last row).
// opts: cmd (data-cmd on each tile; data-arg = the game), disabled, column (art above the name),
// subtitles (show the one-line description).
export function gamePickHtml(picked, { cmd = "pick-game", disabled = false, column = false, subtitles = true, label = "Game" } = {}) {
  const tile = (kind, name, sub) => {
    const on = picked === kind;
    return `<button type="button" class="gtile${column ? " gtile--col" : ""}${on ? " is-on" : ""}" data-cmd="${e(cmd)}" data-arg="${kind}" aria-pressed="${on}"${disabled ? " disabled" : ""}>
      ${gameArt(kind)}<span class="gtile__text"><span class="gtile__name">${name}</span>${subtitles ? `<span class="gtile__sub">${sub}</span>` : ""}</span>
      <span class="gtile__tick" aria-hidden="true">${ICON.tick}</span></button>`;
  };
  return `<div class="gpick gpick--3 gpick--4 gpick--5" role="group" aria-label="${e(label)}">${tile("estate", "ESTATE", "Property board game")}${tile("poker", "POKER", "Texas Hold'em")}${tile("ludo", "LUDO", "Race your pieces home")}${tile("quiz", "QUIZ", "Fastest right answer")}${tile("closest", GAME_LABELS.closest, "Guess, then back the closest")}</div>`;
}

// ---------------------------------------------------------------------------------------------
// Seats around the table

// Position names by number of seats, clockwise from the bottom (the viewer).
export const POSITIONS = {
  1: ["b"],
  2: ["b", "t"],
  3: ["b", "tl", "tr"],
  4: ["b", "l", "t", "r"],
  5: ["b", "bl", "tl", "tr", "br"],
  6: ["b", "bl", "tl", "t", "tr", "br"],
  7: ["b", "bl", "l", "tl", "tr", "r", "br"],
  8: ["b", "bl", "l", "tl", "t", "tr", "r", "br"],
};

// Players who sit at the table (left players are gone), clockwise from the viewer (or from the
// lowest seat on the table screen), each with a position name.
export function seatOrder(state, viewerId = state?.viewerId) {
  const players = (state?.players || []).filter((player) => player.status !== "left").sort((a, b) => a.seat - b.seat);
  const at = Math.max(0, players.findIndex((player) => player.id === viewerId));
  const ordered = players.slice(at).concat(players.slice(0, at));
  const names = POSITIONS[Math.max(1, Math.min(8, ordered.length))] || POSITIONS[8];
  return ordered.slice(0, 8).map((player, index) => ({ player, pos: names[index] }));
}

const ACTION_WORDS = { fold: "FOLD", check: "CHECK", call: "CALL", bet: "BET", raise: "RAISE" };

// The short label over a seat: what they just did this street, or their state.
export function seatLabel(state, player) {
  const result = state?.result;
  if (result && !result.cancelled) {
    const won = (result.pots || []).reduce((sum, pot) => sum + (pot.winners || []).filter((w) => w.id === player.id).reduce((s, w) => s + (w.amount || 0), 0), 0);
    if (won > 0) return { text: `WIN ${chips(won)}`, tone: "win" };
  }
  switch (player.status) {
    case "folded": return player.inHand ? { text: "FOLD", tone: "fold" } : null;
    case "allIn": return { text: "ALL IN", tone: "allin" };
    case "sittingOut": return { text: "SITTING OUT", tone: "away" };
    case "busted": return { text: "OUT OF CHIPS", tone: "out" };
    case "out": return { text: player.place ? `OUT · ${ordinal(player.place).toUpperCase()}` : "OUT", tone: "out" };
    case "waiting": return state?.status === "playing" ? { text: "NEXT HAND", tone: "away" } : null;
    default: break;
  }
  const last = player.lastAction;
  if (!last || !state?.street || last.street !== state.street) return null;
  const word = ACTION_WORDS[last.type];
  if (!word) return null;
  if (last.allIn) return { text: "ALL IN", tone: "allin" };
  if (last.type === "bet" || last.type === "raise") return { text: `${word} ${chips(last.to)}`, tone: "act" };
  return { text: word, tone: last.type === "fold" ? "fold" : "act" };
}

// The five cards that won (union over every pot's winners), or an empty set.
export function winningCards(state) {
  const set = new Set();
  const result = state?.result;
  if (!result?.showdown) return set;
  for (const pot of result.pots || []) for (const winner of pot.winners || []) for (const card of winner.bestCards || []) set.add(card);
  return set;
}

export function winnersOf(state) {
  const result = state?.result;
  const map = new Map();
  for (const pot of result?.pots || []) {
    for (const winner of pot.winners || []) {
      const entry = map.get(winner.id) || { id: winner.id, amount: 0, handName: winner.handName || "" };
      entry.amount += winner.amount || 0;
      if (!entry.handName && winner.handName) entry.handName = winner.handName;
      map.set(winner.id, entry);
    }
  }
  return [...map.values()].sort((a, b) => b.amount - a.amount);
}

// Cards a seat shows: face-up codes when known, backs while they hold hidden cards, else none.
function seatCards(state, player, viewerId) {
  if (player.id === viewerId && state?.you?.cards?.length) return { cards: state.you.cards, faceUp: true, mine: true };
  if (player.cards?.length) return { cards: player.cards, faceUp: true, mine: false };
  const shown = state?.result?.hands?.find((hand) => hand.id === player.id && hand.cards?.length);
  if (shown) return { cards: shown.cards, faceUp: true, mine: false };
  if (player.hasCards) return { cards: ["back", "back"], faceUp: false, mine: false };
  return { cards: [], faceUp: false, mine: false };
}

function isAway(player) {
  return player.kind !== "ai" && player.input !== "ai" && (player.autopilot || player.online === false);
}

// Timer for the player to act: { id, left (s), total (s), bank (on the time bank), deadline }.
// `now` is the server-clock time (poker.js serverClock().now()).
export function actTimer(state, now = Date.now()) {
  if (state?.phase !== "betting" || !state.actorId || !state.actionDeadline) return null;
  const actor = state.players?.find((player) => player.id === state.actorId);
  const bank = Boolean(state.actionTimeBank);
  // On the bank, actionDeadline covers the bank, and players[].timeBank still shows what the actor
  // had when it started (docs/POKER-API.md §4.1).
  const base = Number(state.settings?.actionSeconds) || 20;
  let total = bank ? Number(actor?.timeBank) || Number(state.settings?.timeBankSeconds) || 30 : base;
  const left = Math.max(0, (state.actionDeadline - now) / 1000);
  if (left > total) total = Math.ceil(left);
  return { id: state.actorId, left, total, bank, deadline: state.actionDeadline };
}

// Everything the table shows, from the state. opts: viewerId, handNames (show hand names at
// seats), now (server-clock ms, for the timer's first paint).
export function tableModel(state, { viewerId = state?.viewerId ?? null, handNames = true, now = Date.now() } = {}) {
  const order = seatOrder(state, viewerId);
  const winners = winnersOf(state);
  const winIds = new Set(winners.map((w) => w.id));
  const best = winningCards(state);
  const timer = actTimer(state, now);
  const hand = state?.handNumber || 0;
  const over = Boolean(state?.result) && ["showdown", "handOver", "paused", "finished"].includes(state?.phase);
  const seats = order.map(({ player, pos }) => {
    const { cards, faceUp, mine } = seatCards(state, player, viewerId);
    const folded = player.status === "folded";
    const odds = state?.phase === "runout" ? oddsLabel(player.odds) : null;
    return {
      id: player.id,
      pos,
      name: player.name,
      color: playerColor(state, player),
      tokenKind: player.tokenKind,
      viewer: player.id === viewerId,
      stack: player.stack,
      bet: player.bet || 0,
      status: player.status,
      folded,
      allIn: player.status === "allIn",
      out: player.status === "out" || player.status === "busted",
      sittingOut: player.status === "sittingOut",
      ai: player.kind === "ai" || player.input === "ai",
      away: isAway(player),
      dealer: Boolean(player.isButton),
      sb: Boolean(player.isSmallBlind),
      bb: Boolean(player.isBigBlind),
      label: seatLabel(state, player),
      acting: state?.phase === "betting" && state.actorId === player.id,
      bank: Boolean(timer?.bank) && timer.id === player.id,
      cards,
      faceUp,
      mine,
      handName: handNames && faceUp && (!mine || over || state?.phase === "runout") ? player.handName || state?.result?.hands?.find((h) => h.id === player.id)?.handName || "" : "",
      odds,
      win: winIds.has(player.id),
      best: over && winIds.has(player.id) ? best : null,
      hand,
    };
  });
  const board = (state?.board?.length ? state.board : state?.result?.board) || [];
  return { state, seats, board, best, winners, timer, over, hand, viewerId };
}

// ---------------------------------------------------------------------------------------------
// HTML

function seatMarkersHtml(seat) {
  const marks = [];
  if (seat.dealer) marks.push('<span class="pk-btn pk-btn--d" title="Dealer">D</span>');
  if (seat.sb) marks.push('<span class="pk-btn pk-btn--sb" title="Small blind">SB</span>');
  if (seat.bb) marks.push('<span class="pk-btn pk-btn--bb" title="Big blind">BB</span>');
  return marks.length ? `<span class="pk-marks">${marks.join("")}</span>` : "";
}

function holeHtml(seat, { fourColor = false } = {}) {
  if (!seat.cards.length) return `<span class="pk-hole is-empty" data-key="hole-${e(seat.id)}" aria-hidden="true"></span>`;
  const cls = ["pk-hole"];
  if (seat.faceUp) cls.push("pk-hole--up");
  if (seat.mine) cls.push("pk-hole--mine");
  if (seat.folded) cls.push("is-folded");
  const cards = seat.cards.map((code, index) => {
    const win = seat.best?.size && code !== "back" && seat.best.has(code) ? " is-win" : "";
    const dim = seat.best?.size && seat.faceUp && !win ? " is-dim" : "";
    return cardHtml(code, { cls: `${win}${dim}`, key: `h${seat.hand}-${index}-${code === "back" ? "b" : "f"}`, small: !seat.mine });
  }).join("");
  const stamp = seat.mine && seat.folded ? '<span class="pk-hole__stamp">FOLDED</span>' : "";
  return `<span class="${cls.join(" ")}${fourColor ? " pk-four" : ""}" data-key="hole-${e(seat.id)}">${cards}${stamp}</span>`;
}

function avatarHtml(seat) {
  const ring = seat.acting ? `<span class="pk-ring${seat.bank ? " is-bank" : ""}" aria-hidden="true"></span><span class="pk-secs${seat.bank ? " is-bank" : ""}" aria-hidden="true"></span>` : "";
  const bot = seat.away ? `<span class="plate__bot pk-away" title="Away: checks or folds for them">${ICON.bot}</span>` : "";
  return `<span class="pk-av" style="--pc:${e(seat.color)}">${tokenSvg(seat.tokenKind || "rocket", seat.color, { size: 40 })}${ring}${bot}${seatMarkersHtml(seat)}</span>`;
}

// A hand name short enough for a seat: the full name, or its rank ("Two pair, Sixes and Twos" →
// "Two pair") when the full one is long. Both are sent; poker.css shows the one that fits.
export function shortHandName(name) {
  const text = String(name || "");
  if (text.length <= 15) return text;
  const at = rankOf(text);
  return at >= 0 ? HAND_RANKS[at].name : text.split(",")[0];
}

function seatHtml(seat, opts) {
  const cls = ["pk-seat"];
  if (seat.viewer) cls.push("is-you");
  if (seat.acting) cls.push("is-turn");
  if (seat.folded) cls.push("is-folded");
  if (seat.out) cls.push("is-out");
  if (seat.sittingOut) cls.push("is-sitout");
  if (seat.away) cls.push("is-away");
  if (seat.allIn) cls.push("is-allin");
  if (seat.win) cls.push("is-win");
  if (seat.faceUp && !seat.mine) cls.push("has-up");
  // Keyed by its words: a new action pops in, the same one stays put.
  const label = seat.label
    ? `<span class="pk-flag pk-flag--${seat.label.tone}" data-key="flag-${e(seat.label.tone)}-${e(seat.label.text)}">${e(seat.label.text)}</span>`
    : '<span class="pk-flag is-off" data-key="flag-none" aria-hidden="true"></span>';
  const name = seat.viewer ? "YOU" : upper(seat.name);
  const ai = seat.ai ? `<span class="pk-ai" title="Computer">${ICON.bot}</span>` : "";
  const stack = seat.status === "busted" || seat.status === "out" ? "—" : chips(seat.stack);
  const short = shortHandName(seat.handName);
  const hand = seat.handName ? `<span class="pk-hand${seat.win ? " is-win" : ""}${short !== seat.handName && seat.handName.length > 18 ? " pk-hand--long" : ""}"><span class="pk-hand__full">${e(upper(seat.handName))}</span>${short !== seat.handName ? `<span class="pk-hand__short">${e(upper(short))}</span>` : ""}</span>` : "";
  const odds = seat.odds ? `<span class="pk-odds" title="Chance to win">${e(seat.odds.win)}${seat.odds.tie ? `<small> · TIE ${e(seat.odds.tie)}</small>` : ""}</span>` : "";
  return `<div class="${cls.join(" ")}" data-key="seat-${e(seat.id)}" data-pos="${seat.pos}" data-player="${e(seat.id)}">
    ${holeHtml(seat, opts)}
    <span class="pk-seat__body">${avatarHtml(seat)}${label}
      <span class="pk-plate"><span class="pk-name${name.length > 10 ? " pk-name--longer" : name.length > 7 ? " pk-name--long" : ""}"><span class="pk-name__text">${e(name)}</span>${ai}</span><span class="pk-stack">${e(stack)}</span></span></span>
    ${odds || hand ? `<span class="pk-info">${odds}${hand}</span>` : ""}
  </div>`;
}

// One bet slot per seat, hidden while there is no bet (showing it again starts its slide-in).
function betHtml(seat) {
  if (!seat.bet) return `<div class="pk-bet is-off" data-key="bet-${e(seat.id)}" data-pos="${seat.pos}" data-player="${e(seat.id)}" aria-hidden="true"></div>`;
  return `<div class="pk-bet${seat.allIn ? " is-allin" : ""}" data-key="bet-${e(seat.id)}" data-pos="${seat.pos}" data-player="${e(seat.id)}">${pileHtml(seat.bet, { size: 15 })}<span class="pk-bet__amt">${chips(seat.bet)}</span></div>`;
}

// Pots in the middle: one POT (everything in the middle), or MAIN + SIDE pots once there are side
// pots. After the hand: who won what.
export function potsHtml(model) {
  const state = model.state;
  if (model.over && model.winners.length) {
    const names = (id) => (id === model.viewerId ? "YOU" : upper(state.players.find((player) => player.id === id)?.name || ""));
    const result = state.result;
    const pots = (result.pots || []).filter((pot) => pot.winners?.length);
    const rows = pots.map((pot, index) => {
      const who = pot.winners.map((w) => names(w.id)).join(" + ");
      const amount = pot.winners.reduce((sum, w) => sum + (w.amount || 0), 0);
      const hand = pot.winners[0]?.handName;
      const potName = pots.length > 1 ? (index === 0 ? "MAIN · " : pots.length > 2 ? `SIDE ${index} · ` : "SIDE · ") : "";
      return `<div class="pk-won${index ? " pk-won--side" : ""}" data-key="won-${index}"><span class="pk-won__line"><span class="pk-won__pot">${potName}</span>${e(who)} ${pot.winners.length > 1 ? "SPLIT" : model.viewerId && pot.winners[0].id === model.viewerId ? "WIN" : "WINS"} <b>${chips(amount)}</b></span>${hand && index === 0 ? `<span class="pk-won__hand">${e(upper(hand))}</span>` : ""}</div>`;
    }).join("");
    return `<div class="pk-pots pk-pots--won" data-key="pots-won-${model.hand}">${rows}</div>`;
  }
  const pots = state?.pots || [];
  const total = state?.totalPot || 0;
  if (pots.length > 1) {
    const items = pots.map((pot, index) => `<span class="pk-pot${index ? " pk-pot--side" : ""}" data-key="pot-${index}">${pileHtml(pot.amount, { size: 14, seed: index })}<span class="pk-pot__text"><span class="pk-pot__label">${index ? `SIDE${pots.length > 2 ? ` ${index}` : ""}` : "MAIN"}</span><span class="pk-pot__amt">${chips(pot.amount)}</span></span></span>`).join("");
    return `<div class="pk-pots" data-key="pots">${items}</div>`;
  }
  if (!total) return '<div class="pk-pots" data-key="pots"></div>';
  return `<div class="pk-pots" data-key="pots"><span class="pk-pot" data-key="pot-0">${pileHtml(total, { size: 16 })}<span class="pk-pot__text"><span class="pk-pot__label">POT</span><span class="pk-pot__amt">${chips(total)}</span></span></span></div>`;
}

export function boardHtml(model, { fourColor = false } = {}) {
  const cards = [];
  for (let index = 0; index < 5; index += 1) {
    const code = model.board[index];
    if (!code) {
      cards.push(cardHtml(null, { key: `slot-${index}` }));
      continue;
    }
    const win = model.over && model.best.size && model.best.has(code) ? " is-win" : "";
    const dim = model.over && model.best.size && !win ? " is-dim" : "";
    cards.push(cardHtml(code, { cls: `${win}${dim}`, key: `b${model.hand}-${index}-${code}` }));
  }
  return `<div class="pk-board${fourColor ? " pk-four" : ""}" data-key="board">${cards.join("")}</div>`;
}

// The whole oval with seats, bets, pots and board. opts: fourColor, mark (the word on the felt),
// shape ("auto" | "portrait" | "landscape"), extra (HTML placed on the stage, e.g. a callout).
export function tableHtml(model, { fourColor = false, mark = "ESTATE", shape = "auto", extra = "" } = {}) {
  const seats = model.seats.map((seat) => seatHtml(seat, { fourColor })).join("");
  const bets = model.seats.map(betHtml).join("");
  const count = model.seats.length;
  return `<div class="pk-table" data-shape="${shape}" data-count="${count}">
    <div class="pk-stage${fourColor ? " pk-four" : ""}">
      <div class="pk-rail" aria-hidden="true"><div class="pk-felt"><span class="pk-mark">${e(mark)}</span></div></div>
      <div class="pk-centre">${boardHtml(model, { fourColor })}${potsHtml(model)}</div>
      <div class="pk-bets">${bets}</div>
      <div class="pk-seats">${seats}</div>${extra}
      <div class="pk-fx" aria-hidden="true"></div>
    </div>
  </div>`;
}

// ---------------------------------------------------------------------------------------------
// Blinds pill and level countdown

// "6:40" until the blinds go up (or "" when they never do).
export function clockText(ms) {
  if (!(ms > 0)) return "";
  const total = Math.ceil(ms / 1000);
  const minutes = Math.floor(total / 60);
  const seconds = String(total % 60).padStart(2, "0");
  return `${minutes}:${seconds}`;
}

export function blindsText(state) {
  const level = state?.level || {};
  const sb = level.smallBlind ?? state?.settings?.smallBlind ?? 0;
  const bb = level.bigBlind ?? state?.settings?.bigBlind ?? 0;
  return `${chips(sb)} / ${chips(bb)}`;
}

export function blindsPillHtml(state, { now = Date.now(), compact = false } = {}) {
  const level = state?.level || {};
  const ends = level.endsAt && state?.status === "playing" && state?.settings?.style !== "cash" ? clockText(level.endsAt - now) : "";
  const ante = level.ante ? `<span class="pk-blinds__ante">ANTE ${chips(level.ante)}</span>` : "";
  return `<span class="pk-blinds${compact ? " pk-blinds--compact" : ""}"><span class="pk-blinds__label">BLINDS</span><span class="pk-blinds__value">${e(blindsText(state))}</span>${ante}${ends ? `<span class="pk-blinds__next" title="Blinds go up in"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" stroke-width="2.4"/><path d="M12 7v5.5l3.5 2" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/></svg><span data-level-clock>${ends}</span></span>` : ""}</span>`;
}

// ---------------------------------------------------------------------------------------------
// How to play: hand ranks with example cards, and five short steps

export const HAND_RANKS = [
  { name: "Royal flush", cards: ["As", "Ks", "Qs", "Js", "Ts"], use: 5 },
  { name: "Straight flush", cards: ["9h", "8h", "7h", "6h", "5h"], use: 5 },
  { name: "Four of a kind", cards: ["Qc", "Qd", "Qh", "Qs", "4d"], use: 4 },
  { name: "Full house", cards: ["Kd", "Kh", "Ks", "7c", "7d"], use: 5 },
  { name: "Flush", cards: ["Ad", "Jd", "8d", "6d", "2d"], use: 5 },
  { name: "Straight", cards: ["Tc", "9d", "8s", "7h", "6c"], use: 5 },
  { name: "Three of a kind", cards: ["7s", "7h", "7d", "Kc", "2s"], use: 3 },
  { name: "Two pair", cards: ["Jh", "Js", "4c", "4d", "Ah"], use: 4 },
  { name: "Pair", cards: ["Ts", "Th", "Kd", "6c", "3s"], use: 2 },
  { name: "High card", cards: ["Ac", "Jd", "8h", "5s", "2c"], use: 1 },
];

export const HOW_STEPS = [
  "Everyone gets two cards only they can see.",
  "Five shared cards come out in the middle: three, then one, then one.",
  "Make your best five cards from your two and the five in the middle.",
  "On your turn: FOLD, CHECK or CALL, or BET / RAISE.",
  "Best hand at the end wins the pot, or be the last one in.",
];

// Which rank a hand name belongs to ("Two pair, Kings and Sevens" → "Two pair").
export function rankOf(handName) {
  const text = String(handName || "").toLowerCase();
  if (!text) return -1;
  if (text.startsWith("royal")) return 0;
  if (text.startsWith("straight flush")) return 1;
  if (text.startsWith("four")) return 2;
  if (text.startsWith("full")) return 3;
  if (text.startsWith("flush")) return 4;
  if (text.startsWith("straight")) return 5;
  if (text.startsWith("three")) return 6;
  if (text.startsWith("two pair")) return 7;
  if (text.startsWith("pair") || text.startsWith("pocket")) return 8;
  if (text.includes("high")) return 9;
  return -1;
}

export function handRanksHtml({ yours = "", fourColor = false } = {}) {
  const mine = rankOf(yours);
  const rows = HAND_RANKS.map((rank, index) => {
    const cards = rank.cards.map((code, at) => cardHtml(code, { small: true, cls: at < rank.use ? "" : "is-dim" })).join("");
    return `<li class="${index === mine ? "is-you" : ""}"><span class="ranks__n">${index + 1}</span><span class="ranks__name">${e(upper(rank.name))}${index === mine ? ' <span class="ranks__you">YOU</span>' : ""}</span><span class="pc-row">${cards}</span></li>`;
  }).join("");
  return `<ol class="ranks${fourColor ? " pk-four" : ""}">${rows}</ol>`;
}

export function howStepsHtml() {
  return `<ol class="pk-steps">${HOW_STEPS.map((step) => `<li>${e(step)}</li>`).join("")}</ol>`;
}

// ---------------------------------------------------------------------------------------------
// Small pieces

export function tokenChipHtml(state, player, size = 20) {
  const color = playerColor(state, player) || TOKEN_COLORS[player?.tokenKind] || "#8A929C";
  return `<span class="pchip pchip--glyph" style="--pc:${e(color)};--pc-ink:${inkOn(color)};--pc-size:${size}px" aria-hidden="true">${tokenGlyphSvg(player?.tokenKind || "rocket")}</span>`;
}
