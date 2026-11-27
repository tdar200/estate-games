// The actions on a deed's sheet (tap any property on the phone): the buttons for
// web/shared/deedactions.js, each greyed with its one-line reason when the engine wouldn't take
// it. TRADE and ASK stay live off-turn: the composer saves the deal for your turn. Pure, so the
// checks can run it.

import { escapeHtml } from "../shared/format.js";
import { deedActions } from "../shared/deedactions.js";

const e = escapeHtml;

export function deedActionButtons(state, viewerId, index) {
  return deedActions(state, viewerId, index, { planTrades: true }).actions.map((action) => {
    const data = action.act ? `data-act='${JSON.stringify(action.act)}'` : `data-cmd="${e(action.cmd)}" data-arg="${e(action.arg)}"`;
    const kind = action.tone === "primary" && !action.disabled ? "primary" : "ghost";
    const label = `${action.label}${action.detail ? ` · ${action.detail}` : ""}`;
    const button = `<button type="button" class="btn btn--${kind}${action.on ? " is-on" : ""}" ${data} data-deed-action="${e(action.id)}"${action.disabled ? " disabled" : ""}${action.on !== undefined ? ` aria-pressed="${action.on}"` : ""}><span class="btn__label">${e(label)}</span></button>`;
    return action.disabled && action.reason ? `${button}<p class="sheet__why" data-why="${e(action.id)}">${e(action.reason)}</p>` : button;
  });
}
