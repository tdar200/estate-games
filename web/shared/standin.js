// Stand-in, I'M BACK, idle time and the host's Players controls (docs/API.md §3c, UX-02 and
// UX-07). Pure: the screens pass the server's clock ("now", Unix ms) in.

import { inputOf, playerById } from "./format.js";
import { auctionActors, isTimed } from "./auction-clock.js";

// Idle shows once the game has waited this long on someone (the server's own idle stand-in starts
// at 2 minutes; before 30 s a wait is just someone thinking).
export const IDLE_SHOW_MS = 30_000;

// A person (not a computer) still in the game.
export function isPerson(player) {
  return Boolean(player) && inputOf(player) !== "ai" && !player.bankrupt;
}

// I'M BACK shows while the stand-in plays for this person.
export function showBack(player) {
  return isPerson(player) && Boolean(player.autopilot);
}

// The while-away list shows once the seat was handed back, until the person's next command.
export function showAwayList(player) {
  return isPerson(player) && !player.autopilot && Number(player.standIn?.until) > 0;
}

// The log entries since the stand-in took over: after standIn.logId; everything when logId is ""
// or no longer in the log (it keeps its newest 120 entries only).
export function awayEntries(state, player) {
  const mark = player?.standIn;
  if (!mark) return [];
  const log = state?.log || [];
  const at = mark.logId ? log.findIndex((entry) => entry.id === mark.logId) : -1;
  return log.slice(at + 1);
}

// The same entries as short lines for that person's phone: their own name becomes "you" words
// ("Sam rolled 3 + 4 = 7" → "Rolled 3 + 4 = 7", "Sam's turn" → "Your turn"); others keep theirs.
// The newest `limit`, oldest first, and how many earlier ones were left out.
export function awayLines(state, player, { limit = 8 } = {}) {
  const entries = awayEntries(state, player);
  const name = player?.name || "";
  const lines = entries.map((entry) => {
    const text = String(entry.text || "");
    let line = text;
    let mine = false;
    if (name && text.startsWith(`${name}'s `)) {
      line = `Your ${text.slice(name.length + 3)}`;
      mine = true;
    } else if (name && text.startsWith(`${name} `)) {
      const rest = text.slice(name.length + 1);
      line = rest.charAt(0).toUpperCase() + rest.slice(1);
      mine = true;
    }
    return { id: entry.id, text: line, mine, at: entry.at };
  });
  const shown = lines.slice(-limit);
  return { lines: shown, more: lines.length - shown.length };
}

// Cash then (standIn.cash) and now: the change while away, or null without a mark.
export function awayCash(player) {
  const then = Number(player?.standIn?.cash);
  if (!player?.standIn || !Number.isFinite(then)) return null;
  return { then, now: player.cash, change: player.cash - then };
}

// Who the game is waiting on: the person to act. A timed auction never waits on anyone.
export function waitedOnIds(state) {
  if (!state || state.status !== "playing") return [];
  switch (state.phase) {
    case "determining_order": return state.openingOrder?.pendingIds || [];
    case "auction": return isTimed(state.auction) ? [] : auctionActors(state);
    case "trade": return [state.trade?.targetId].filter(Boolean);
    case "loan_offer": return [state.loanOffer?.targetId].filter(Boolean);
    case "card_confirmation": return [state.pendingCard?.playerId].filter(Boolean);
    case "debt": return [state.debt?.payerId].filter(Boolean);
    case "awaiting_roll":
    case "awaiting_purchase":
    case "turn_complete": return [state.currentPlayerId].filter(Boolean);
    default: return [];
  }
}

// When the game last moved: the newest log entry's time (the game clock, Unix ms), or null.
export function lastMoveAt(state) {
  const log = state?.log || [];
  for (let index = log.length - 1; index >= 0; index -= 1) {
    const at = Number(log[index]?.at);
    if (Number.isFinite(at) && at > 0) return at;
  }
  return null;
}

// How long the game has waited on this person (ms), from data the screen really has: the time of
// the newest log entry while the game waits on them. Null when it isn't waiting on them, they are
// a computer, the stand-in already plays for them, or the log has no times.
export function idleMs(state, playerId, now) {
  const player = playerById(state, playerId);
  if (!isPerson(player) || player.autopilot || !waitedOnIds(state).includes(playerId)) return null;
  const since = lastMoveAt(state);
  if (since === null || !Number.isFinite(now)) return null;
  return Math.max(0, now - since);
}

// Idle long enough to say so (IDLE m:ss).
export function idleShown(state, playerId, now) {
  const ms = idleMs(state, playerId, now);
  return ms !== null && ms >= IDLE_SHOW_MS ? ms : null;
}

// How long the stand-in has played for them (ms since standIn.since), or null.
export function standInMs(player, now) {
  const since = Number(player?.standIn?.since);
  if (!player?.autopilot || !Number.isFinite(since) || since <= 0 || !Number.isFinite(now)) return null;
  return Math.max(0, now - since);
}

// "1:40", "12:05".
export function minutesText(ms) {
  const seconds = Math.max(0, Math.floor((Number(ms) || 0) / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

// What the host may do to this player now (docs §3c "Host controls mid-game"): Computer plays and
// Skip turn for a person still in, Skip only while the game waits on them or it is their turn;
// Remove for anyone still in, except during the roll for turn order.
export function hostControls(state, playerId) {
  const player = playerById(state, playerId);
  const none = { standin: false, skip: false, remove: false };
  if (!player || player.bankrupt || state?.status !== "playing") return none;
  const person = isPerson(player);
  const waited = state.currentPlayerId === playerId || waitedOnIds(state).includes(playerId) || auctionActors(state).includes(playerId);
  return {
    standin: person && !player.autopilot,
    skip: person && waited && !player.skipped,
    remove: state.phase !== "determining_order",
  };
}

// What a removal gives back to the bank: deeds, buildings and cash.
export function removalSummary(state, playerId) {
  const player = playerById(state, playerId);
  if (!player) return null;
  const deeds = Object.entries(state.properties || {}).filter(([, prop]) => prop?.ownerId === playerId);
  const houses = deeds.reduce((sum, [, prop]) => sum + (prop.houses || 0), 0);
  return { deeds: deeds.length, houses, cash: player.cash };
}

// The host-control request a screen sends: {path, body} (POST /api/rooms/{code}/{path}).
export function hostRequest(kind, code, token, playerId) {
  if (!["standin", "skip", "remove"].includes(kind)) throw new Error(`Unknown host control: ${kind}`);
  return { method: "POST", path: `/api/rooms/${encodeURIComponent(code)}/${kind}`, body: { token, playerId } };
}
