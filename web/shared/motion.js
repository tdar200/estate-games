// Reusable animations from the approved Motion design. Every function returns a Promise that
// resolves when the motion has settled, and collapses to (almost) nothing when the user prefers
// reduced motion.

import { diceHtml } from "./dice.js";
import { escapeHtml, money } from "./format.js";

// "system" follows the OS setting; "reduce" / "full" override it (a per-screen setting).
let motionOverride = "system";

export function setMotionOverride(mode) {
  motionOverride = mode === "reduce" || mode === "full" ? mode : "system";
}

export function prefersReducedMotion() {
  if (motionOverride === "reduce") return true;
  if (motionOverride === "full") return false;
  return Boolean(globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches);
}

// "fast" animations: hops and chips run quicker (card reading time never changes).
let speedFactor = 1;

export function setAnimationSpeed(speed) {
  speedFactor = speed === "fast" ? 0.55 : 1;
}

export function animationSpeed() {
  return speedFactor;
}

export function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function quick(ms) {
  return prefersReducedMotion() ? Math.min(ms, 60) : ms;
}

// Add a one-shot CSS class (keyframes in tokens.css / board.css) and resolve when it ends.
export function playClass(el, className, ms) {
  if (!el) return Promise.resolve();
  el.classList.remove(className);
  void el.offsetWidth;
  el.classList.add(className);
  return wait(quick(ms)).then(() => el.classList.remove(className));
}

function animate(el, keyframes, options) {
  if (!el?.animate) return Promise.resolve();
  const duration = quick(options.duration);
  const animation = el.animate(keyframes, { fill: "forwards", ...options, duration });
  return animation.finished.catch(() => {}).then(() => animation);
}

// DICE THROW: thrown across the board, bounce, settle (0.9 s). Leaves the dice in place.
export async function throwDice(container, dice, { size = "" } = {}) {
  if (!container || !Array.isArray(dice)) return;
  container.innerHTML = diceHtml(dice, { className: size });
  const faces = [...container.querySelectorAll(".die")];
  faces.forEach((face, index) => {
    face.style.animation = prefersReducedMotion() ? "none" : `diceIn 0.9s ${index * 0.12}s ease-out both`;
  });
  await wait(quick(1020));
}

// TOKEN MOVES: lifts and sets down square by square (0.12 s each). `points` in the layer's
// coordinates; `place(point)` returns the CSS transform for a token centred on that point.
export async function hopToken(el, points, { stepMs = 120, place, onStep } = {}) {
  if (!el || points.length < 2) return;
  if (prefersReducedMotion()) {
    el.style.transform = place(points[points.length - 1]);
    return;
  }
  for (let index = 1; index < points.length; index += 1) {
    const from = place(points[index - 1]);
    const to = place(points[index]);
    const lift = place({ x: (points[index - 1].x + points[index].x) / 2, y: (points[index - 1].y + points[index].y) / 2 - 22 });
    const last = index === points.length - 1;
    const animation = el.animate([{ transform: from }, { transform: lift, offset: 0.45 }, { transform: to }], {
      duration: last ? stepMs * 1.6 : stepMs, easing: "ease-in-out", fill: "forwards",
    });
    await animation.finished.catch(() => {});
    el.style.transform = to;
    animation.cancel();
    onStep?.(index, last);
  }
}

const NOTE_COLORS = { 500: "#F3DFA2", 100: "#CDE3F5", 50: "#E6D3F0", 20: "#CFEADF", 10: "#F6E7D3", 5: "#DCD3F2", 1: "#F6F3EA" };

// Break an amount into a handful of note values; bigger amounts fly more notes.
export function notesFor(amount) {
  const values = [];
  let left = Math.max(0, Math.round(amount));
  for (const value of [500, 100, 50, 20, 10, 5, 1]) {
    while (left >= value && values.length < 8) {
      values.push(value);
      left -= value;
    }
  }
  return values.length ? values : [1];
}

// RENT CHANGES HANDS: notes fly in an arc from payer to owner (1.2 s).
export async function flyNotes(layer, from, to, amount) {
  if (!layer) return;
  if (prefersReducedMotion()) return;
  const notes = notesFor(amount);
  const runs = notes.map((value, index) => {
    const note = document.createElement("div");
    note.className = "bank-note";
    note.style.background = NOTE_COLORS[value];
    note.textContent = `$${value}`;
    layer.append(note);
    const dx = to.x - from.x;
    const dy = to.y - from.y;
    const start = `translate(${from.x - 20}px, ${from.y - 11}px)`;
    const peak = `translate(${from.x - 20 + dx / 2}px, ${from.y - 11 + dy / 2 - 70}px)`;
    const end = `translate(${to.x - 20}px, ${to.y - 11}px)`;
    const animation = note.animate([
      { transform: `${start} rotate(-20deg) scale(0.7)`, opacity: 0 },
      { opacity: 1, offset: 0.1 },
      { transform: `${peak} rotate(14deg) scale(1)`, offset: 0.5 },
      { opacity: 1, offset: 0.9 },
      { transform: `${end} rotate(-6deg) scale(0.8)`, opacity: 0 },
    ], { duration: 900, delay: index * 120, easing: "ease-in-out", fill: "forwards" });
    return animation.finished.catch(() => {}).then(() => note.remove());
  });
  await Promise.all(runs);
}

// "+$550" / "−$1,100" chip that rises over a point.
export function riseChip(layer, point, amount, { tone = null } = {}) {
  if (!layer || !amount) return Promise.resolve();
  const chip = document.createElement("span");
  const positive = tone ? tone === "pos" : amount > 0;
  chip.className = `rise-chip ${positive ? "rise-chip--pos" : "rise-chip--neg"}`;
  chip.textContent = positive ? `+${money(Math.abs(amount))}` : money(-Math.abs(amount));
  chip.style.left = `${point.x}px`;
  chip.style.top = `${point.y - 40}px`;
  chip.style.marginLeft = "-28px";
  layer.append(chip);
  return wait(quick(1800)).then(() => chip.remove());
}

// CARD DRAWN: lifts off the deck and flips to face the table (0.6 s). Returns the card element.
export async function flipCard(container, { deck = "chance", text = "" } = {}) {
  if (!container) return null;
  const chest = deck === "chest";
  container.innerHTML = `<div class="flip-card${chest ? " flip-card--chest" : ""}">
    <div class="flip-card__inner">
      <div class="flip-card__back">${chest ? "POST" : "HEADLINES"}</div>
      <div class="flip-card__front"><span class="flip-card__eyebrow">${chest ? "POST" : "HEADLINES"}</span><span class="flip-card__text">${escapeHtml(text)}</span></div>
    </div>
  </div>`;
  const inner = container.querySelector(".flip-card__inner");
  if (prefersReducedMotion()) {
    inner.style.transform = "rotateY(180deg)";
    return container.firstElementChild;
  }
  await animate(inner, [
    { transform: "translateY(0) rotateY(0deg)" },
    { transform: "translateY(-14px) rotateY(0deg)", offset: 0.3 },
    { transform: "translateY(0) rotateY(180deg)" },
  ], { duration: 650, easing: "ease-in-out" });
  return container.firstElementChild;
}

// PROPERTY BOUGHT: ink SOLD stamp over an element (0.5 s), removed after `holdMs`.
export async function stampSold(target, { holdMs = 900, label = "SOLD" } = {}) {
  if (!target) return;
  const stamp = document.createElement("span");
  stamp.className = "sold-stamp";
  stamp.textContent = label;
  target.append(stamp);
  await wait(quick(600 + holdMs));
  stamp.remove();
}

// Small stamp sized for a board square.
export async function stampCell(cell) {
  if (!cell) return;
  const stamp = document.createElement("span");
  stamp.className = "sold-stamp sold-stamp--cell";
  stamp.textContent = "SOLD";
  cell.append(stamp);
  await wait(quick(1100));
  stamp.remove();
}

// HOUSES BUILT: each new house drops onto the square with a small bounce (0.35 s each).
export async function dropHouses(container, fromIndex = 0) {
  if (!container) return;
  const pieces = [...container.children].slice(fromIndex);
  pieces.forEach((piece, index) => {
    piece.style.animation = prefersReducedMotion() ? "none" : `drop 0.35s ${index * 0.18}s ease-in both`;
  });
  await wait(quick(350 + pieces.length * 180));
  pieces.forEach((piece) => { piece.style.animation = ""; });
}

// BIG RENT HIT: rent over $500 jolts the element with a red pulse.
export function bigRentHit(el) {
  return playClass(el, "is-hit", 900);
}

// JAILED: bars slam down over a token.
export async function slamBars(tokenEl) {
  const bars = tokenEl?.querySelector(".jail-bars");
  if (!bars) return;
  bars.hidden = false;
  await playClass(bars, "is-slamming", 1200);
}

// Token reactions: "bob" (waiting), "bounce" (collecting), "wobble" (paying), "pulse" (winner).
export function react(el, kind) {
  const target = el?.querySelector?.(".board-token__body") || el;
  if (!target) return Promise.resolve();
  const ms = { bounce: 2200, wobble: 2200 }[kind];
  if (!ms) {
    target.classList.add(`react-${kind}`);
    return Promise.resolve();
  }
  return playClass(target, `react-${kind}`, ms);
}

// VICTORY: gold confetti falling through a container.
export function confetti(container, { count = 36, colors = ["#F2C14E", "#FBFAF4", "#F2C14E", "#E8232A", "#2F6FE0", "#1FAE57"], height = null } = {}) {
  if (!container || prefersReducedMotion()) return Promise.resolve();
  const width = container.clientWidth || 400;
  const fall = height || container.clientHeight || 400;
  const pieces = [];
  for (let index = 0; index < count; index += 1) {
    const piece = document.createElement("span");
    piece.className = "confetti-piece";
    piece.style.left = `${Math.round(((index * 97) % 100) / 100 * width)}px`;
    piece.style.background = colors[index % colors.length];
    piece.style.setProperty("--fall", `${fall}px`);
    piece.style.animationDelay = `${((index * 37) % 20) / 10}s`;
    piece.style.animationDuration = `${2.2 + ((index * 13) % 10) / 10}s`;
    container.append(piece);
    pieces.push(piece);
  }
  return wait(5200).then(() => pieces.forEach((piece) => piece.remove()));
}

// Count a number up/down inside an element (cash on plates and phone header).
// One count at a time per element: a newer count takes over, and while it runs the element is
// held (`__morphHold`, web/shared/morph.js) so a re-render can't write the final value over it for
// a frame and make the number flicker.
export function tickNumber(el, from, to, { format = money, ms = 600 } = {}) {
  if (!el) return Promise.resolve();
  const id = (el.__tick || 0) + 1;
  el.__tick = id;
  const write = (text) => {
    const node = el.firstChild;
    if (node && node.nodeType === 3 && !node.nextSibling) {
      if (node.nodeValue !== text) node.nodeValue = text;
    } else el.textContent = text;
  };
  if (from === to || prefersReducedMotion()) {
    el.__morphHold = false;
    write(format(to));
    return Promise.resolve();
  }
  // Show the old value at once (a re-render may just have written the new one), unless an earlier
  // count is still running: then this one carries on from where that one is.
  if (!el.__morphHold) write(format(from));
  el.__morphHold = true;
  const start = performance.now();
  return new Promise((resolve) => {
    const frame = (now) => {
      if (el.__tick !== id) {
        resolve();
        return;
      }
      const t = Math.min(1, (now - start) / ms);
      const eased = 1 - (1 - t) ** 3;
      write(format(Math.round(from + (to - from) * eased)));
      if (t < 1) requestAnimationFrame(frame);
      else {
        el.__morphHold = false;
        resolve();
      }
    };
    requestAnimationFrame(frame);
  });
}
