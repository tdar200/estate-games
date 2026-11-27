// Plays the events between two states on the phone board, in order: dice settle in the board
// centre, the token hops square by square (follow-cam on small phones), the landing square glows,
// money flies between the plates of the player strip, cards flip, SOLD stamps, houses drop, jail
// bars slam. phone.js races this against a time budget and skips it when the page is hidden, so
// it can never hold the screen hostage. Sounds go through ctx.sfx (audio.js decides: your own
// events at full volume, other players' quieter or silent). A drawn card stays up long enough to
// read (audio.js cardReadMs) unless the queue falls behind.

import { diceHtml } from "../shared/dice.js";
import { escapeHtml, money, playerById, upper } from "../shared/format.js";
import * as motion from "../shared/motion.js";
import { cardHoldMs, sfxForEvent } from "./audio.js";
import { dotHtml, shortName } from "./cues.js";

const e = escapeHtml;

export const ANIMATED = new Set(["dice", "move", "pay", "card", "buy", "build", "jail", "bankrupt"]);

export function animatable(events) {
  return events.filter((event) => ANIMATED.has(event.type) && !(event.type === "dice" && event.order));
}

function sfx(ctx, event, extra) {
  const hit = sfxForEvent(event, ctx.viewerId, extra);
  if (hit) ctx.sfx?.(hit.name, hit.opts, hit.gain);
}

// Wait `ms`, but stop early when the playback is cancelled or the queue falls behind.
async function hold(ctx, ms) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (ctx.cancelled?.() || ctx.behind?.()) return;
    await motion.wait(Math.min(150, end - Date.now()));
  }
}

// A beat between effects at the chosen pace (SETTINGS: animations normal or fast).
function pause(ctx, ms) {
  return motion.wait(Math.round(ms * (ctx.pace || 1)));
}

function withProp(state, index, prop) {
  return { ...state, properties: { ...state.properties, [index]: prop } };
}

function centreOf(rect) {
  return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
}

// A money chip that flies from one screen rect to another (viewport coordinates, fixed layer).
async function flyChip(layer, fromRect, toRect, text, tone) {
  if (!layer || !fromRect || !toRect || motion.prefersReducedMotion()) return;
  const chip = document.createElement("span");
  chip.className = `fly-chip fly-chip--${tone}`;
  chip.textContent = text;
  layer.append(chip);
  const a = centreOf(fromRect);
  const b = centreOf(toRect);
  const lift = Math.max(a.y, b.y) + 42;
  const at = (p) => `translate(${Math.round(p.x)}px, ${Math.round(p.y)}px) translate(-50%, -50%)`;
  const animation = chip.animate([
    { transform: `${at(a)} scale(0.6)`, opacity: 0 },
    { transform: `${at(a)} scale(1)`, opacity: 1, offset: 0.12 },
    { transform: `${at({ x: (a.x + b.x) / 2, y: lift })} scale(1.08)`, offset: 0.55 },
    { transform: `${at(b)} scale(0.9)`, opacity: 1, offset: 0.9 },
    { transform: `${at(b)} scale(0.6)`, opacity: 0 },
  ], { duration: 900, easing: "ease-in-out", fill: "forwards" });
  await animation.finished.catch(() => {});
  chip.remove();
}

function flash(el, tone) {
  if (!el) return;
  el.classList.remove("is-flash-pos", "is-flash-neg");
  void el.offsetWidth;
  el.classList.add(`is-flash-${tone}`);
  setTimeout(() => el.classList.remove(`is-flash-${tone}`), 1100);
}

// Count a plate's cash by `delta` and remember what it shows.
function tickPlate(ctx, playerId, delta) {
  if (!playerId || !delta) return;
  const from = ctx.cash.get(playerId);
  if (from === undefined) return;
  const to = from + delta;
  ctx.cash.set(playerId, to);
  const node = ctx.plate(playerId)?.querySelector("[data-cash-for]");
  if (node) motion.tickNumber(node, from, to);
}

async function playPay(ctx, event) {
  const { board } = ctx;
  const fromPlate = event.from ? ctx.plate(event.from) : null;
  const toPlate = event.to ? ctx.plate(event.to) : null;
  const boardRect = board.root.getBoundingClientRect();
  const bankRect = { left: boardRect.left + boardRect.width / 2 - 1, top: boardRect.top + boardRect.height / 2 - 1, width: 2, height: 2 };
  if (event.reason === "go") {
    sfx(ctx, event);
    const point = board.tokenPoint(event.to);
    if (point) motion.riseChip(board.fx, point, event.amount, { tone: "pos" });
    flash(toPlate, "pos");
    tickPlate(ctx, event.to, event.amount);
    if (event.to === ctx.viewerId) ctx.buzz(20);
    await pause(ctx, 650);
    return;
  }
  const big = event.amount >= 500;
  sfx(ctx, event);
  if (big) {
    motion.bigRentHit(board.root);
    if (event.from === ctx.viewerId) ctx.buzz([80, 60, 120]);
  }
  const fromPoint = event.from ? board.tokenPoint(event.from) : null;
  if (fromPoint) motion.riseChip(board.fx, fromPoint, -event.amount, { tone: "neg" });
  flash(fromPlate, "neg");
  tickPlate(ctx, event.from, -event.amount);
  await flyChip(ctx.layer, fromPlate?.getBoundingClientRect() || bankRect, toPlate?.getBoundingClientRect() || bankRect, money(event.amount), event.to ? "pay" : "bank");
  if (toPlate) {
    flash(toPlate, "pos");
    tickPlate(ctx, event.to, event.amount);
    if (event.to === ctx.viewerId) ctx.buzz(20);
  }
  await pause(ctx, 250);
}

async function playDice(ctx, next, event) {
  const { board } = ctx;
  sfx(ctx, event);
  const player = playerById(next, event.playerId);
  const name = event.playerId === ctx.viewerId ? "YOU" : upper(player?.name || "");
  board.setStage(`<div class="pev pev--gold"><div class="pev__who">${dotHtml(next, event.playerId, { size: 16 })}${e(name)} ${event.playerId === ctx.viewerId ? "ROLL" : "ROLLS"}</div></div><div class="pdice" data-roll></div>`);
  const box = board.stageEl()?.querySelector("[data-roll]");
  board.dirtyStage();
  await motion.throwDice(box, event.dice, { size: "die--st" });
  const total = event.dice[0] + event.dice[1];
  box?.insertAdjacentHTML("beforeend", `<span class="pdice__total is-pop">${total}</span>`);
  if (event.dice[0] === event.dice[1]) box?.insertAdjacentHTML("afterend", '<div class="pbadge pbadge--gold is-pop">DOUBLES</div>');
  if (event.playerId === ctx.viewerId) ctx.buzz(15);
  await pause(ctx, 380);
}

async function playMove(ctx, event, shown) {
  const mine = event.playerId === ctx.viewerId;
  const path = event.path || [];
  if (path.length < 2) return;
  ctx.positions.set(event.playerId, path[path.length - 1]);
  const stepMs = Math.round((event.jail ? 420 : path.length > 14 ? 85 : 120) * (ctx.pace || 1));
  await ctx.board.hop(event.playerId, path, {
    stepMs,
    // Only your own hop zooms in: everyone else's plays on the whole board (review U7: the
    // board kept zooming during other players' turns and the overview was lost).
    follow: ctx.follow !== false && mine,
    jail: Boolean(event.jail),
    zoom: ctx.zoom,
    onStep: (index, last) => {
      sfx(ctx, event, { step: { last } });
      if (mine) ctx.buzz(last ? 30 : 8);
    },
  });
  // Fan the tokens out again now that the mover shares a square (the hop lands it dead centre).
  ctx.board.board.placeTokens(shown, { positions: ctx.positions, animate: true });
}

// The card flips over in the centre and stays there until it has been readable (for everyone's
// card, so remote players can read it too). A card waiting to be confirmed stays on the stage
// anyway, so it only gets a short beat here.
async function playCard(ctx, next, event) {
  const stage = ctx.board.stageEl();
  sfx(ctx, event);
  if (!stage) return;
  stage.innerHTML = '<div class="pflip" data-flip></div>';
  ctx.board.dirtyStage();
  const started = Date.now();
  await motion.flipCard(stage.querySelector("[data-flip]"), { deck: event.deck, text: event.text });
  const readMs = cardHoldMs(event, next);
  await hold(ctx, readMs ? Math.max(0, readMs - (Date.now() - started)) : 600);
}

async function playBuy(ctx, next, event, shown) {
  const { board } = ctx;
  sfx(ctx, event);
  const updated = withProp(shown, event.spaceIndex, next.properties[event.spaceIndex]);
  board.board.update(updated, { currentSquare: null });
  const space = next.board[event.spaceIndex];
  const who = event.playerId === ctx.viewerId ? "YOURS" : upper(playerById(next, event.playerId)?.name || "");
  const stage = board.stageEl();
  if (stage) {
    stage.innerHTML = `<div class="pev"><div class="pev__who">${dotHtml(next, event.playerId, { size: 16 })}${e(who)}</div><div class="pev__big">${e(upper(shortName(space.name)))}</div><div class="pev__sub">${money(event.price)}</div><span class="pstamp">SOLD</span></div>`;
    board.dirtyStage();
  }
  const cell = board.cell(event.spaceIndex);
  cell?.classList.add("is-pop");
  setTimeout(() => cell?.classList.remove("is-pop"), 900);
  await pause(ctx, motion.prefersReducedMotion() ? 200 : 1000);
  return updated;
}

async function playBuild(ctx, next, event, shown) {
  const { board } = ctx;
  const updated = withProp(shown, event.spaceIndex, next.properties[event.spaceIndex]);
  board.board.update(updated, { currentSquare: null });
  const houses = board.cell(event.spaceIndex)?.querySelector(".cell__houses");
  const from = event.houses === 5 ? 0 : event.from || 0;
  const count = event.houses === 5 ? 1 : event.houses;
  sfx(ctx, event);
  await motion.dropHouses(houses, from);
  return updated;
}

// Someone went out: a card that holds for a moment ("DEV IS OUT · 8 DEEDS → CARA") while the owner
// chips flip to the creditor, so the hand-over is never skipped by whatever comes next.
async function playBankruptcy(ctx, prev, next, event, shown) {
  const out = playerById(next, event.playerId);
  const creditor = playerById(next, event.creditorId);
  const deeds = Object.values(prev.properties || {}).filter((prop) => prop?.ownerId === event.playerId).length;
  const you = event.playerId === ctx.viewerId;
  const to = creditor ? `${dotHtml(next, creditor.id, { size: 16 })}${creditor.id === ctx.viewerId ? "YOU" : e(upper(creditor.name))}` : "THE BANK";
  const stage = ctx.board.stageEl();
  if (stage) {
    stage.innerHTML = `<div class="pev pev--red pev--out"><div class="pev__who">${dotHtml(next, event.playerId, { size: 16 })}${you ? "YOU ARE" : e(upper(out?.name || ""))+" IS"} OUT</div><div class="pev__big">${deeds ? `${deeds} DEED${deeds === 1 ? "" : "S"} → ` : "ALL → "}${to}</div></div>`;
    ctx.board.dirtyStage();
  }
  const updated = { ...shown, properties: next.properties };
  ctx.board.board.update(updated, { currentSquare: null });
  if (creditor?.id === ctx.viewerId) ctx.buzz([40, 30, 40]);
  await motion.wait(motion.prefersReducedMotion() ? 1200 : 2600);
  return updated;
}

// Plays `events` (already filtered by animatable()) starting from what `prev` shows.
// ctx: { board: PhoneBoard, layer, plate(id), cash: Map, viewerId, buzz(pattern), zoom, cancelled(),
//        behind() (the queue fell behind: cut holds short), sfx(name, opts, gain) }
export async function playEvents(ctx, prev, next, events) {
  let shown = prev;
  ctx.positions = new Map(prev.players.map((player) => [player.id, player.position]));
  ctx.board.show(prev, { animate: false });
  ctx.board.clearMarks();
  for (const event of events) {
    if (ctx.cancelled?.()) return;
    switch (event.type) {
      case "dice": await playDice(ctx, next, event); break;
      case "move":
        // Sent to jail: the token lands in the barred box, so the fan-out after the hop must place
        // it there too (it used to hop into the box, step out to the passing strip, then jump back in).
        if (event.jail) shown = { ...shown, players: shown.players.map((player) => (player.id === event.playerId ? { ...player, inJail: true } : player)) };
        await playMove(ctx, event, shown);
        break;
      case "pay": await playPay(ctx, event); break;
      case "card": await playCard(ctx, next, event); break;
      case "buy": shown = await playBuy(ctx, next, event, shown); break;
      case "build": shown = await playBuild(ctx, next, event, shown); break;
      case "jail":
        sfx(ctx, event);
        if (event.playerId === ctx.viewerId) ctx.buzz([60, 40, 60]);
        await motion.slamBars(ctx.board.tokenEl(event.playerId));
        break;
      case "bankrupt": {
        sfx(ctx, event);
        const token = ctx.board.tokenEl(event.playerId);
        if (token?.animate) await token.animate([{ opacity: 1 }, { opacity: 0 }], { duration: motion.prefersReducedMotion() ? 1 : 700, fill: "forwards" }).finished.catch(() => {});
        shown = await playBankruptcy(ctx, prev, next, event, shown);
        break;
      }
      default:
        break;
    }
  }
}

// Gold "YOUR TURN" stamp over the board. (The board itself is not dimmed any more: a 0.4 s dip
// of the whole board read as a flash on every turn.)
export function yourTurnCue(board) {
  if (!board?.root || motion.prefersReducedMotion()) return;
  const stamp = document.createElement("span");
  stamp.className = "turn-stamp";
  stamp.textContent = "YOUR TURN";
  board.fx?.append(stamp);
  setTimeout(() => stamp.remove(), 1300);
}

export { diceHtml };
