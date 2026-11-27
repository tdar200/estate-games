// Player strip: one plate per player (token disc in the player's colour, cash, name or "You"), a
// gold TURN tab on the current player, NEXT on whoever follows, and a pulsing ring on whoever must
// act right now (bidder, trade target, debtor, order roller). Offline players are dimmed with a
// dashed edge; a robot badge shows when the computer is playing for someone (players[].autopilot).
// Replaces the single-player header in game. Pure.

import { escapeHtml, money, playerById } from "../shared/format.js";
import { nextPlayerId } from "../shared/insights.js";
import { inkOn, playerColor, tokenKindOf, tokenSvg } from "../shared/tokens.js";
import { icon } from "./icons.js";

const e = escapeHtml;

// Who must act now (a player id), beyond the current player.
export function actorId(state) {
  if (!state || state.status !== "playing") return null;
  switch (state.phase) {
    case "auction": return state.auction?.currentPlayerId || null;
    case "trade": return state.trade?.targetId || null;
    case "loan_offer": return state.loanOffer?.targetId || null;
    case "debt": return state.debt?.payerId || null;
    case "card_confirmation": return state.pendingCard?.playerId || null;
    default: return state.currentPlayerId || null;
  }
}

export function discHtml(state, player, { size = 22, className = "" } = {}) {
  if (!player) return "";
  const color = playerColor(state, player);
  return `<span class="disc ${className}" style="--disc:${e(color)};--disc-ink:${inkOn(color)};width:${size}px;height:${size}px">${tokenSvg(tokenKindOf(state, player), color, { size: Math.round(size * 0.9) })}</span>`;
}

// `deltas`: optional map playerId → cash change since the viewer's last turn (shown as badges).
export function stripHtml(state, { viewerId = null, deltas = null } = {}) {
  const players = state?.players || [];
  if (!players.length) return "";
  const current = state.status === "playing" ? state.currentPlayerId : null;
  const next = state.status === "playing" && state.phase !== "determining_order" && state.phase !== "order_countdown" ? nextPlayerId(state) : null;
  const actor = actorId(state);
  const ordering = state.phase === "determining_order" ? new Set(state.openingOrder?.pendingIds || []) : null;
  const plates = players.map((player) => {
    const me = player.id === viewerId;
    const turn = player.id === current && !ordering;
    const acting = ordering ? ordering.has(player.id) : player.id === actor && !player.bankrupt;
    const flag = player.bankrupt ? "OUT" : turn ? "TURN" : player.id === next && !ordering ? "NEXT" : "";
    const delta = deltas?.[player.id];
    const offline = player.online === false && player.input !== "ai" && !player.bankrupt;
    const auto = Boolean(player.autopilot) && !player.bankrupt;
    const label = `${me ? "You" : player.name}: ${money(player.cash)}${turn ? ", taking their turn" : ""}${acting && !turn ? ", must act" : ""}${player.bankrupt ? ", out of the game" : ""}${auto ? ", the computer is playing for them" : offline ? ", offline" : ""}`;
    const classes = ["plate", turn ? "is-turn" : "", me ? "is-me" : "", acting ? "is-act" : "", player.bankrupt ? "is-out" : "", player.inJail ? "is-jail" : "", offline ? "is-offline" : "", auto ? "is-auto" : ""].filter(Boolean).join(" ");
    return `<div class="${classes}" role="listitem" data-plate="${e(player.id)}" style="--plate-color:${e(playerColor(state, player))}" aria-label="${e(label)}">
      ${flag ? `<span class="plate__flag plate__flag--${flag.toLowerCase()}">${flag}</span>` : ""}
      ${discHtml(state, player, { size: 22, className: "plate__disc" })}${auto ? `<span class="plate__bot" aria-hidden="true">${icon("bot", { size: 11 })}</span>` : ""}
      <span class="plate__text"><span class="plate__cash" data-cash-for="${e(player.id)}" data-cash="${player.cash}">${money(player.cash)}</span><span class="plate__name">${me ? "You" : e(player.name)}</span></span>
      ${delta ? `<span class="plate__delta plate__delta--${delta > 0 ? "pos" : "neg"}">${delta > 0 ? "+" : "−"}${money(Math.abs(delta))}</span>` : ""}
    </div>`;
  }).join("");
  return `<div class="strip strip--n${Math.min(8, players.length)}" role="list" aria-label="Players">${plates}</div>`;
}

// Cash changes since the viewer's previous turn, from the server's turn summary.
export function sinceDeltas(state, viewerId) {
  const summary = state?.turnSummary;
  if (!summary || summary.playerId !== viewerId || state.currentPlayerId !== viewerId) return null;
  if (state.turn?.dice) return null;
  const out = {};
  for (const row of summary.players || []) if (row.cashDelta) out[row.playerId] = row.cashDelta;
  return Object.keys(out).length ? out : null;
}

export function playerName(state, id, viewerId) {
  if (id && id === viewerId) return "You";
  return playerById(state, id)?.name || "";
}
