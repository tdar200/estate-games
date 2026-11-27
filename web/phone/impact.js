// What a trade does, for both sides, so nobody has to remember the cards: cash, net worth and rent
// before → after, sets completed or broken, plain verdicts ("Completes Alpha's RED set"), and a
// small map of the board with the deal's squares. Pure. Uses the shared insights.tradeImpact (adapted
// below); the local version is only a fallback for older shared builds.

import * as insights from "../shared/insights.js";
import { GROUP_COLORS, colourWord, escapeHtml, money, playerById, possessive, upper } from "../shared/format.js";
import { playerColor } from "../shared/tokens.js";

const e = escapeHtml;
const { groupIndexes, netWorth, ownsGroup, propState, rentFor } = insights;

// The state after the swap (ownership and cash only; buildings and mortgages travel with deeds).
export function stateAfter(state, trade) {
  const properties = { ...state.properties };
  for (const index of trade.offer?.properties || []) properties[index] = { ...propState(state, index), ownerId: trade.targetId };
  for (const index of trade.request?.properties || []) properties[index] = { ...propState(state, index), ownerId: trade.proposerId };
  const give = trade.offer?.cash || 0;
  const get = trade.request?.cash || 0;
  const players = state.players.map((player) => {
    if (player.id === trade.proposerId) return { ...player, cash: player.cash - give + get };
    if (player.id === trade.targetId) return { ...player, cash: player.cash + give - get };
    return player;
  });
  return { ...state, properties, players };
}

// Rent the player's deeds charge right now (streets at their current level, an average 7 for
// utilities): a rough "earning power".
export function rentPower(state, playerId) {
  let total = 0;
  for (const [index, prop] of Object.entries(state.properties || {})) {
    if (prop?.ownerId !== playerId) continue;
    total += rentFor(state, Number(index)) || 0;
  }
  return total;
}

function groupsOf(state) {
  return [...new Set((state.board || []).filter((space) => space.type === "property").map((space) => space.group))];
}

// { verdicts: [{ text, tone }], sides: [{ playerId, you, name, cash, worth, rent, setsGained, setsLost }] }
// tone is from the viewer's point of view: "good", "bad" or "info".
export function tradeImpactLocal(state, trade, viewerId) {
  const after = stateAfter(state, trade);
  const sides = [trade.proposerId, trade.targetId].map((playerId) => {
    const player = playerById(state, playerId);
    const setsGained = [];
    const setsLost = [];
    for (const group of groupsOf(state)) {
      const before = ownsGroup(state, playerId, group);
      const later = ownsGroup(after, playerId, group);
      if (!before && later) setsGained.push(group);
      if (before && !later) setsLost.push(group);
    }
    return {
      playerId,
      you: playerId === viewerId,
      name: player?.name || "",
      cash: [player?.cash ?? 0, playerById(after, playerId)?.cash ?? 0],
      worth: [netWorth(state, playerId), netWorth(after, playerId)],
      rent: [rentPower(state, playerId), rentPower(after, playerId)],
      setsGained,
      setsLost,
    };
  });
  const verdicts = [];
  const whose = (side) => (side.you ? "your" : `${possessive(side.name)}`);
  for (const side of sides) {
    for (const group of side.setsGained) verdicts.push({ text: `Completes ${whose(side)} ${upper(colourWord(group))} set`, tone: side.you ? "good" : "bad", group });
    for (const group of side.setsLost) verdicts.push({ text: `Breaks ${whose(side)} ${upper(colourWord(group))} set`, tone: side.you ? "bad" : "good", group });
  }
  if (!verdicts.length) {
    const me = sides.find((side) => side.you);
    if (me) {
      const change = me.worth[1] - me.worth[0];
      verdicts.push({ text: change === 0 ? "Even on paper: no set changes hands" : `${change > 0 ? "You gain" : "You lose"} ${money(Math.abs(change))} of value on paper`, tone: change > 0 ? "good" : change < 0 ? "bad" : "info" });
    } else verdicts.push({ text: "No set changes hands", tone: "info" });
  }
  return { verdicts, sides };
}

// The shared insights.tradeImpact ({ proposer, target } sides with cash/worth before and after,
// sets completed/broken, rent changes, blocked deeds, verdict strings), in the phone's shape;
// the local version when it isn't there.
export function tradeImpact(state, trade, viewerId) {
  if (typeof insights.tradeImpact === "function") {
    try {
      const shared = insights.tradeImpact(state, trade, { viewerId });
      if (shared?.proposer && shared?.target) return fromShared(shared, viewerId);
    } catch { /* fall back */ }
  }
  return tradeImpactLocal(state, trade, viewerId);
}

function fromShared(shared, viewerId) {
  const sides = [shared.proposer, shared.target].map((side) => ({
    playerId: side.playerId,
    you: side.playerId === viewerId,
    name: side.name,
    cash: [side.cashBefore, side.cashAfter],
    worth: [side.worthBefore, side.worthAfter],
    rent: null,
    rentChanges: side.rentChanges || [],
    setsGained: (side.setsCompleted || []).map((set) => set.group),
    setsLost: (side.setsBroken || []).map((set) => set.group),
    fee: side.fee || 0,
    blocked: side.blocked || [],
  }));
  const verdicts = [];
  for (const side of [shared.proposer, shared.target]) {
    const you = side.playerId === viewerId;
    for (const text of side.verdicts || []) {
      const completes = /^Completes /.test(text);
      const breaks = /^Breaks /.test(text);
      const group = [...(side.setsCompleted || []), ...(side.setsBroken || [])].find((set) => text.includes(` ${set.name} set`))?.group;
      const tone = completes ? (you ? "good" : "bad") : breaks ? (you ? "bad" : "good") : /can't be traded/.test(text) ? "bad" : "info";
      verdicts.push({ text, tone, group });
    }
  }
  if (!verdicts.length) {
    const me = sides.find((side) => side.you);
    const change = me ? me.worth[1] - me.worth[0] : 0;
    verdicts.push(me && change ? { text: `${change > 0 ? "You gain" : "You lose"} ${money(Math.abs(change))} of value on paper`, tone: change > 0 ? "good" : "bad" } : { text: "No set changes hands", tone: "info" });
  }
  return { verdicts, sides };
}

// ---- HTML -------------------------------------------------------------------------------------

export function verdictsHtml(impact) {
  return `<div class="verdicts">${impact.verdicts.map((verdict) => `<div class="verdict verdict--${verdict.tone}">${verdict.group ? `<i style="background:${GROUP_COLORS[verdict.group]}"></i>` : ""}${e(verdict.text)}</div>`).join("")}</div>`;
}

const arrow = (pair, { signed = false } = {}) => {
  const [before, after] = pair;
  const tone = after > before ? "up" : after < before ? "down" : "same";
  return `<span class="imp__val imp__val--${tone}">${money(before)} → <b>${money(after)}</b>${signed && after !== before ? ` <small>${after > before ? "+" : "−"}${money(Math.abs(after - before))}</small>` : ""}</span>`;
};

// A rent change from the shared impact: dollars for sets and railroads, dice multiples for
// utilities, "—" when nothing is charged.
const rentArrow = (change) => {
  const show = (value) => (!value ? "—" : change.key === "utility" ? `${value}×` : money(value));
  const tone = change.after > change.before ? "up" : change.after < change.before ? "down" : "same";
  return `<span class="imp__val imp__val--${tone}">${show(change.before)} → <b>${show(change.after)}</b></span>`;
};

// Cash, net worth and rent before → after for both sides.
export function impactHtml(state, impact) {
  const cols = impact.sides.map((side) => {
    const player = playerById(state, side.playerId);
    // A set made says what it can charge once built up (review U8: "rent $12 → $24" alone hid it).
    const top = (group) => Math.max(0, ...groupIndexes(state, group).map((index) => state.board[index]?.rents?.[5] || 0));
    const sets = [...side.setsGained.map((group) => `<span class="imp__set" style="--set:${GROUP_COLORS[group]}">+ ${e(upper(colourWord(group)))}${top(group) ? ` <small>up to ${money(top(group))}</small>` : ""}</span>`), ...side.setsLost.map((group) => `<span class="imp__set is-lost" style="--set:${GROUP_COLORS[group]}">− ${e(upper(colourWord(group)))}</span>`)].join("");
    return `<div class="imp__side${side.you ? " is-you" : ""}">
      <div class="imp__who"><span class="imp__dot" style="background:${playerColor(state, player)}"></span>${side.you ? "YOU" : e(upper(side.name))}</div>
      <div class="imp__row"><span>Cash</span>${arrow(side.cash)}</div>
      <div class="imp__row"><span>Net worth</span>${arrow(side.worth, { signed: true })}</div>
      ${side.rent ? `<div class="imp__row"><span>Rent</span>${arrow(side.rent)}</div>` : (side.rentChanges || []).map((change) => `<div class="imp__row"><span>${e(change.label)} rent</span>${rentArrow(change)}</div>`).join("")}
      ${sets ? `<div class="imp__sets">${sets}</div>` : ""}
    </div>`;
  }).join("");
  return `<div class="impact" aria-label="What this deal does">${cols}</div>`;
}

// Grid position on an 11 × 11 board (PAYDAY bottom-right, as the real board).
function cell(index) {
  if (index <= 10) return [11, 11 - index];
  if (index < 20) return [11 - (index - 10), 1];
  if (index <= 30) return [1, index - 19];
  return [index - 29, 11];
}

// The whole board in miniature: colour bars, owners as coloured fills, the deal's squares outlined
// (red: you give, green: you get).
export function miniMapHtml(state, { give = [], get = [] } = {}) {
  const giving = new Set(give);
  const getting = new Set(get);
  const cells = (state.board || []).map((space, index) => {
    const [row, col] = cell(index);
    const prop = propState(state, index);
    const owner = playerById(state, prop?.ownerId);
    const bar = space.type === "property" ? GROUP_COLORS[space.group] : "";
    const mark = giving.has(index) ? " is-give" : getting.has(index) ? " is-get" : "";
    const fill = owner ? `--own:${playerColor(state, owner)};` : "";
    return `<span class="mm__cell${owner ? " is-owned" : ""}${mark}" style="grid-area:${row}/${col};${fill}${bar ? `--bar:${bar};` : ""}" title="${e(space.name)}">${bar ? "<i></i>" : ""}</span>`;
  }).join("");
  return `<div class="minimap" role="img" aria-label="Board: the deal's squares outlined">${cells}<span class="mm__key"><b class="is-give"></b>give <b class="is-get"></b>get</span></div>`;
}

// The members of a deed's set and who owns each (for the deed detail).
export function setOwnersHtml(state, index, viewerId) {
  const space = state.board[index];
  const members = space.type === "property" ? groupIndexes(state, space.group)
    : (state.board || []).filter((candidate) => candidate.type === space.type).map((candidate) => candidate.index);
  if (members.length < 2) return "";
  const rows = members.map((member) => {
    const owner = playerById(state, propState(state, member)?.ownerId);
    const who = owner ? (owner.id === viewerId ? "You" : owner.name) : "For sale";
    return `<li class="setown${member === index ? " is-this" : ""}"><span class="setown__name">${e(state.board[member].name)}</span><span class="setown__owner">${owner ? `<span class="imp__dot" style="background:${playerColor(state, owner)}"></span>` : ""}${e(who)}</span></li>`;
  }).join("");
  return `<ul class="setowns" aria-label="Who owns the set">${rows}</ul>`;
}
