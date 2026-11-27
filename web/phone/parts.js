// Small HTML builders shared by the phone screens. Pure.

import { escapeHtml } from "../shared/format.js";

const e = escapeHtml;

export function btn(label, detail, kind, attrs = {}) {
  return { label, detail, kind, ...attrs };
}

function dataAttrs(button) {
  const data = button.act ? ` data-act='${JSON.stringify(button.act)}'` : button.cmd ? ` data-cmd="${e(button.cmd)}"` : "";
  const extra = button.arg !== undefined ? ` data-arg="${e(button.arg)}"` : "";
  // aria: a longer name for a short label (LOAN → "Ask for a loan").
  const aria = button.aria ? ` aria-label="${e(button.aria)}"` : "";
  return `${data}${extra}${aria}${button.disabled ? " disabled" : ""}`;
}

// Full-width stacked buttons (gold primary, ghost secondaries). A detail shows only when it is
// given (we keep them for reasons a button is greyed). `chips` render as one row of small
// secondary actions under them.
export function buttonsHtml(buttons, chips = []) {
  if (!buttons.length && !chips.length) return "";
  const main = buttons.map((button) => `<button type="button" class="btn btn--${button.kind}${button.className ? ` ${button.className}` : ""}"${dataAttrs(button)}><span class="btn__label">${button.html || e(button.label)}</span>${button.detail ? `<span class="btn__detail">${e(button.detail)}</span>` : ""}</button>`).join("");
  const row = chips.length ? `<div class="chipbtns">${chips.map((chip) => `<button type="button" class="chipbtn${chip.gold ? " chipbtn--gold" : ""}${chip.danger ? " chipbtn--danger" : ""}"${dataAttrs(chip)}>${chip.html || e(chip.label)}</button>`).join("")}</div>` : "";
  return `<div class="actions">${main}${row}</div>`;
}

export function tipHtml(tip) {
  if (!tip || !tip.text) return "";
  return `<div class="tip tip--${tip.tone || "info"}">${tip.strong ? `<strong>${e(tip.strong)}</strong> ` : ""}${e(tip.text)}</div>`;
}

export function glassBox(title, inner, extraClass = "") {
  return `<section class="glass ${extraClass}">${title ? `<h2 class="eyebrow">${e(title)}</h2>` : ""}${inner}</section>`;
}

// PLAY AGAIN on the host's result screen (Estate, poker, Ludo, quiz, Near Thing) ignores taps for
// its first REMATCH_GUARD_MS: people are often still tapping where the last turn's button was when
// the game ends, and that tap must not start a new game for everyone. Nothing changes on screen.
export const REMATCH_GUARD_MS = 1500;
export const RESULT_SCREENS = new Set(["result", "pk-result", "ld-result", "qz-result", "cg-result"]);

// When this finished game's result screen first appeared (ms), else null. Call after each render
// with the screen drawn, the state's status and the value so far. Leaving for a tab (MY DEEDS,
// PLAYERS) and coming back keeps the first time; a new game (any other status) forgets it.
export function resultShownAt(screen, status, since, now) {
  if (status !== "finished") return null;
  if (since !== null && since !== undefined) return since;
  return RESULT_SCREENS.has(screen) ? now : null;
}

// Whether a PLAY AGAIN tap counts: the result screen has been up for REMATCH_GUARD_MS.
export function rematchReady(since, now) {
  return since !== null && since !== undefined && now - since >= REMATCH_GUARD_MS;
}
