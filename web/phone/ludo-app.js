// Ludo on the phone: wires ludo-screens.js (HTML) to the page and plays ludo-fx.js between states.
// phone.js owns the session, the socket and the slots; it hands Ludo states here.
//
// States arrive in order and are shown in order: each new state's animation (the die, a piece
// hopping, a capture) plays on the board still showing the one before, then the new one is drawn.
// So the screen can be a state or two behind ui.state for a second; taps always act on ui.state,
// the server's latest word.

import { morph } from "../shared/morph.js";
import * as motion from "../shared/motion.js";
import { createLudoFx } from "../shared/ludo-fx.js";
import { isLudoState, ludoRuleChips, ludoSounds, playerOf } from "../shared/ludo-view.js";
import { aiLevelsOf, helpSheetHtml, moreSheetHtml, renderLudo } from "./ludo-screens.js";

const AI_KEY = "estate.ludoAiLevel";

export function createLudoApp(deps) {
  const { el, ui, storage } = deps;
  const ld = { aiLevel: storage.get(AI_KEY) || null, seeBoard: false, focus: null };
  let shown = null;
  let animating = false;
  let queue = Promise.resolve();
  const fx = createLudoFx({ sound: (name, opts) => deps.sfx(name, opts, opts?.volume ?? 0.8), speed: () => (document.documentElement.dataset.speed === "fast" ? 0.6 : 1) });

  const active = () => isLudoState(ui.state) && Boolean(ui.session);
  const reduced = () => document.documentElement.dataset.motion === "reduce" || motion.prefersReducedMotion?.();

  function view(base = {}) {
    return { ...base, ld, busy: animating || Boolean(el.app.dataset.busy) };
  }

  // ---- Rendering --------------------------------------------------------------------------

  function render(base) {
    const state = animating && shown ? shown : ui.state;
    const out = renderLudo(state, view(base));
    morph(el.header, out.header);
    morph(el.banner, out.banner);
    morph(el.main, out.main);
    morph(el.nav, out.nav);
    el.app.dataset.game = "ludo";
    el.app.dataset.screen = out.screen;
    el.app.dataset.view = out.screen === "ld-table" ? "ludo" : "page";
    el.app.dataset.frame = out.screen === "ld-table" ? "ludo" : "page";
    el.app.dataset.modal = "";
    el.boardview.hidden = true;
    el.status.hidden = true;
    return out.screen;
  }

  // ---- A new state ------------------------------------------------------------------------

  // Called by phone.js for every Ludo state, in order.
  function commit(prev, next) {
    if (next.status !== "finished") ld.seeBoard = false;
    queue = queue.then(() => show(prev, next)).catch((error) => console.warn("Ludo animation skipped:", error));
    return queue;
  }

  async function show(prev, next) {
    const same = isLudoState(prev) && prev.code === next.code;
    if (!same || document.hidden || ui.state !== next && ui.state?.version > next.version + 2) {
      shown = next;
      deps.render();
      return;
    }
    for (const hit of ludoSounds(prev, next, { viewerId: next.viewerId })) {
      if (["jail", "gavel", "go", "land"].includes(hit.name) && !reduced()) continue; // the animation plays those
      deps.sfx(hit.name, hit.opts, hit.gain);
    }
    if (next.you?.toRoll && !(prev.you?.toRoll)) deps.buzz(50);
    animating = true;
    try {
      await fx.play(el.main.querySelector(".ldp") ? el.main : null, prev, next, () => {
        animating = false;
        shown = next;
        deps.render();
      }, { reduced: reduced() || el.app.dataset.screen !== "ld-table" });
    } finally {
      animating = false;
      shown = next;
    }
    if (next.status === "finished" && prev.status !== "finished" && next.winnerId && next.winnerId === next.viewerId && !reduced()) {
      motion.confetti(el.fx, { count: 40, height: window.innerHeight });
    }
  }

  // ---- Taps -------------------------------------------------------------------------------

  async function move(piece) {
    const s = ui.state;
    if (!s?.you?.toMove || !s.moves?.some((m) => m.piece === piece)) return undefined;
    ld.focus = null;
    return deps.act({ type: "move", piece });
  }

  function onClick(event) {
    if (!active() || el.sheet.contains(event.target)) return;
    const button = event.target.closest("button[data-ld]");
    if (button && !button.disabled) {
      event.preventDefault();
      if (animating) return;
      if (button.dataset.ld === "roll") deps.act({ type: "roll" });
      else if (button.dataset.ld === "move") move(Number(button.dataset.piece));
      return;
    }
    // A glowing piece on the board.
    const piece = event.target.closest?.(".ldp .lp.is-movable");
    if (piece && !animating && piece.dataset.player === ui.state?.viewerId) {
      event.preventDefault();
      move(Number(piece.dataset.piece));
    }
  }

  document.addEventListener("click", onClick);

  // ---- Commands (data-cmd="ld-…") and sheets -------------------------------------------------

  async function command(cmd, arg) {
    const s = ui.state;
    switch (cmd) {
      case "ld-more":
        deps.sfx("tap", {}, 0.5);
        return deps.openSheet({ kind: "ld-more" });
      case "ld-help":
        return deps.openSheet({ kind: "ld-help", back: s?.status !== "lobby" });
      case "ld-rule": {
        const chip = ludoRuleChips(s).find((candidate) => candidate.key === arg);
        if (!chip) return undefined;
        deps.sfx("tap", {}, 0.5);
        return deps.act({ type: "settings", settings: chip.patch });
      }
      case "ld-ai-level": {
        const levels = aiLevelsOf(s);
        const shown = levels.includes(ld.aiLevel) ? ld.aiLevel : levels.includes("normal") ? "normal" : levels[0];
        ld.aiLevel = levels[(levels.indexOf(shown) + 1) % levels.length];
        storage.set(AI_KEY, ld.aiLevel);
        deps.sfx("tap", {}, 0.5);
        return deps.render();
      }
      case "ld-add-ai": {
        const levels = aiLevelsOf(s);
        return deps.addAi(levels.includes(ld.aiLevel) ? ld.aiLevel : levels.includes("normal") ? "normal" : levels[0]);
      }
      case "ld-see-board":
        ld.seeBoard = true;
        return deps.render();
      case "ld-see-result":
        ld.seeBoard = false;
        return deps.render();
      default:
        return undefined;
    }
  }

  function sheetHtml(sheet) {
    const s = ui.state;
    if (sheet.kind === "ld-more") return moreSheetHtml(s, view({ session: ui.session }));
    if (sheet.kind === "ld-help") return helpSheetHtml(s, { back: sheet.back !== false });
    return "";
  }

  async function sheetCommand(cmd) {
    switch (cmd) {
      case "ld-invite":
        deps.closeSheet();
        return deps.command("open-invite");
      case "ld-settings":
        return deps.openSheet({ kind: "settings", back: "ld-more" });
      case "ld-help":
        return deps.openSheet({ kind: "ld-help" });
      case "ld-more":
        return deps.openSheet({ kind: "ld-more" });
      case "ld-leave":
        return deps.command("leave-ask");
      case "ld-end":
        return deps.command("end-game-ask");
      default:
        return undefined;
    }
  }

  // Who is furthest along (for END GAME's confirm).
  function leader() {
    const s = ui.state;
    return [...(s?.players || [])].filter((player) => player.status !== "left").sort((a, b) => (b.home - a.home) || (b.progress - a.progress))[0] || null;
  }

  function setup(extra = {}) {
    Object.assign(ld, extra);
  }

  function reset() {
    ld.seeBoard = false;
    ld.focus = null;
    shown = null;
    animating = false;
    queue = Promise.resolve();
  }

  return { ld, render, commit, command, sheetHtml, sheetCommand, leader, setup, reset, isActive: active, player: (id) => playerOf(ui.state, id) };
}
