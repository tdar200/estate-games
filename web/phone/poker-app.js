// Poker on the phone: wires poker-screens.js (HTML) and poker-play.js (rules of the controls) to
// the page. phone.js owns the session, the socket and the slots; it hands poker states here.
//
// Nothing here rebuilds the screen on a timer. The turn timer is two CSS variables on #app
// (--pk-t, the share of time left, and --pk-s, whole seconds, shown with a CSS counter) and
// data-pk-clock ("low" in the last 5 s, "bank" on the time bank); the blinds and rebuy clocks are
// text nodes. Chips that slide to the pot or the winner fly in the #fx layer, outside the morphed
// slots, so the table itself only ever changes in place.

import { morph } from "../shared/morph.js";
import * as motion from "../shared/motion.js";
import { createPokerFx } from "../shared/poker-fx.js";
import { serverClock } from "../shared/poker.js";
import { actTimer, chips, clockText } from "../shared/poker-view.js";
import { allInSheetHtml, helpSheetHtml, isPoker, moreSheetHtml, rebuyClock, renderPoker } from "./poker-screens.js";
import {
  fourColour, loadPokerPrefs, makePreAction, myTurnSecondsLeft, nextLevel, pokerSounds, preActionToSend, raiseModel, reconcilePreAction, ruleChips,
  savePokerPrefs, sliderAmount, stepAmount, turnButtons,
} from "./poker-play.js";

const AI_KEY = "estate.pokerAiLevel";
const TICK_MS = 200;

export function createPokerApp(deps) {
  const { el, ui, storage } = deps;
  const clock = serverClock();
  const pk = {
    pre: null,
    raiseOpen: false,
    raiseTo: null,
    aiLevel: storage.get(AI_KEY) || null,
    seeTable: false,
    bustSeen: null,
    sentKey: null,
    turnKey: undefined,
    lastTick: null,
    frozen: null,
    prefs: loadPokerPrefs(storage),
  };
  let timer = null;
  const fx = createPokerFx(el.fx);

  const state = () => ui.state;
  const active = () => isPoker(ui.state) && Boolean(ui.session);
  const now = () => (pk.frozen !== null ? pk.frozen : clock.now());

  function view(base = {}) {
    return { ...base, pk, pkPrefs: pk.prefs, now: now() };
  }

  // ---- Rendering --------------------------------------------------------------------------

  function render(base) {
    const out = renderPoker(ui.state, view(base));
    morph(el.header, out.header);
    morph(el.banner, out.banner);
    morph(el.main, out.main);
    morph(el.nav, out.nav);
    el.app.dataset.game = "poker";
    el.app.dataset.screen = out.screen;
    el.app.dataset.view = out.screen === "pk-table" ? "poker" : "page";
    el.app.dataset.frame = out.screen === "pk-table" ? "poker" : "page";
    el.app.dataset.modal = "";
    el.boardview.hidden = true;
    el.status.hidden = true;
    tick();
    ensureTicker();
    return out.screen;
  }

  function ensureTicker() {
    if (timer) return;
    timer = setInterval(() => {
      if (!active()) {
        clearInterval(timer);
        timer = null;
        clearClock();
        return;
      }
      tick();
    }, TICK_MS);
  }

  function clearClock() {
    el.app.style.removeProperty("--pk-t");
    el.app.style.removeProperty("--pk-s");
    delete el.app.dataset.pkClock;
  }

  // Every few hundred ms: the ring and seconds of whoever is acting, the blinds clock, the rebuy
  // clock, and a soft tick in the last 5 s of your own turn.
  function tick() {
    const s = state();
    if (!isPoker(s)) return;
    const t = now();
    const run = actTimer(s, t);
    if (run) {
      const share = Math.max(0, Math.min(1, run.left / run.total));
      el.app.style.setProperty("--pk-t", share.toFixed(3));
      el.app.style.setProperty("--pk-s", String(Math.ceil(run.left)));
      const mode = run.bank ? "bank" : run.left <= 5 ? "low" : "";
      if ((el.app.dataset.pkClock || "") !== mode) {
        if (mode) el.app.dataset.pkClock = mode;
        else delete el.app.dataset.pkClock;
      }
    } else clearClock();
    const level = el.header.querySelector("[data-level-clock]");
    if (level && s.level?.endsAt) {
      const text = clockText(s.level.endsAt - t);
      if (level.textContent !== text) level.textContent = text;
    }
    const rebuy = el.main.querySelector("[data-rebuy-clock]");
    if (rebuy && s.you?.rebuyUntil) {
      const text = rebuyClock(s.you.rebuyUntil, t);
      if (rebuy.textContent !== text) rebuy.textContent = text;
    }
    const next = el.main.querySelector("[data-next-clock]");
    if (next && s.nextHandAt) {
      const text = String(Math.max(0, Math.ceil((s.nextHandAt - t) / 1000)));
      if (next.textContent !== text) next.textContent = text;
    }
    const left = myTurnSecondsLeft(s, t);
    if (left !== null && left > 0 && left <= 5 && !ui.demo) {
      const whole = Math.ceil(left);
      if (pk.lastTick !== whole) {
        pk.lastTick = whole;
        deps.sfx("tick", { last: whole <= 1 }, 0.9);
      }
    } else pk.lastTick = null;
  }

  // ---- A new state ------------------------------------------------------------------------

  // Called by phone.js for every poker state, in order. Returns the screen it rendered.
  function commit(prev, next) {
    clock.sync(next);
    // Demos hold the clock still (steady screenshots) unless the URL says &live=1.
    const still = ui.demo && !/[?&]live=1/.test(location.search);
    if (still && pk.frozen === null) pk.frozen = next.now || Date.now();
    else if (still) pk.frozen = Math.max(pk.frozen, next.now || 0);
    const samePrev = isPoker(prev) && prev.code === next.code ? prev : null;
    // A new decision closes the raise panel; leaving your turn drops it.
    const turnKey = next.you?.toAct ? `${next.handNumber}|${next.street}|${(next.actions || []).length}` : null;
    if (pk.turnKey !== undefined && turnKey !== pk.turnKey) {
      pk.raiseOpen = false;
      pk.raiseTo = null;
    }
    pk.turnKey = turnKey;
    if (next.status !== "finished") pk.seeTable = false;
    pk.pre = reconcilePreAction(pk.pre, next);
    const before = samePrev && el.app.dataset.screen === "pk-table" ? fx.snapshot(el.main) : null;
    const screen = deps.render();
    if (samePrev && !document.hidden) {
      for (const hit of pokerSounds(samePrev, next)) deps.sfx(hit.name, hit.opts, hit.gain);
      if (!samePrev.you?.toAct && next.you?.toAct) deps.buzz(60);
      if (!reduced() && el.app.dataset.screen === "pk-table") fx.animate(el.main, samePrev, next, before);
      if (!reduced() && next.status === "finished" && samePrev.status !== "finished" && next.winnerId && next.winnerId === next.viewerId) motion.confetti(el.fx, { count: 40, height: window.innerHeight });
    }
    autoSend(next);
    return screen;
  }

  // Your turn came with a ticked pre-action: send it (once) if it still applies.
  function autoSend(next) {
    if (!next.you?.toAct || !pk.pre) return;
    const key = `${next.handNumber}|${(next.actions || []).length}`;
    const action = preActionToSend(pk.pre, next);
    pk.pre = null;
    if (!action || pk.sentKey === key) {
      if (!action) deps.render();
      return;
    }
    pk.sentKey = key;
    setTimeout(() => {
      const s = state();
      if (!s?.you?.toAct || `${s.handNumber}|${(s.actions || []).length}` !== key) return;
      send(action);
    }, 260);
  }

  async function send(action) {
    pk.raiseOpen = false;
    pk.raiseTo = null;
    return deps.act(action);
  }

  // ---- Chips that move (shared/poker-fx.js) -------------------------------------------------

  function reduced() {
    return document.documentElement.dataset.motion === "reduce";
  }

  // ---- Taps -------------------------------------------------------------------------------

  async function pkButton(button) {
    const s = state();
    const kind = button.dataset.pk;
    if (!s) return;
    switch (kind) {
      case "fold": return send({ type: "fold" });
      case "check": return send({ type: "check" });
      case "call": return send({ type: "call" });
      case "callallin":
        return deps.openSheet({ kind: "pk-allin", amount: s.you?.legal?.call || 0, action: { type: "call" } });
      case "allin": {
        const button2 = turnButtons(s).find((candidate) => candidate.kind === "allin");
        return deps.openSheet({ kind: "pk-allin", amount: button2?.amount || s.you?.legal?.allIn || 0, action: { type: "allin" } });
      }
      case "raise-open":
        pk.raiseOpen = true;
        pk.raiseTo = s.you?.legal?.raise?.min ?? null;
        deps.sfx("tap", {}, 0.5);
        return deps.render();
      case "raise-close":
        pk.raiseOpen = false;
        pk.raiseTo = null;
        return deps.render();
      case "raise-send": {
        const model = raiseModel(s, pk.raiseTo);
        if (!model) return undefined;
        if (model.allIn) return deps.openSheet({ kind: "pk-allin", amount: model.to, action: { type: "allin" } });
        return send(model.action);
      }
      case "preset": {
        const to = Number(button.dataset.to);
        if (!Number.isFinite(to) || !button.dataset.to) return undefined;
        pk.raiseTo = to;
        deps.sfx("tap", {}, 0.5);
        return deps.render();
      }
      case "step":
        pk.raiseTo = stepAmount(s, pk.raiseTo, Number(button.dataset.dir) || 1);
        deps.sfx("tap", {}, 0.4);
        return deps.render();
      case "rebuy": return deps.act({ type: "rebuy" });
      case "decline":
        pk.bustSeen = null;
        return deps.act({ type: "declineRebuy" });
      case "sitin": return deps.act({ type: "sitIn" });
      case "show": return deps.act({ type: "show" });
      case "bust-ok": {
        const me = s.players.find((player) => player.id === s.viewerId);
        pk.bustSeen = `${s.code}|${me?.status}|${me?.rebuys || 0}`;
        return deps.render();
      }
      default: return undefined;
    }
  }

  function preToggle(button) {
    const s = state();
    const kind = button.dataset.pre;
    if (pk.pre?.kind === kind) pk.pre = null;
    else pk.pre = makePreAction(s, kind);
    deps.sfx("tap", {}, 0.5);
    deps.render();
  }

  function onClick(event) {
    if (!active()) return;
    const button = event.target.closest("button");
    if (!button || button.disabled || el.sheet.contains(button)) return;
    if (button.dataset.pk) {
      if (el.app.dataset.busy && ["fold", "check", "call", "raise-send"].includes(button.dataset.pk)) return;
      event.preventDefault();
      pkButton(button);
    } else if (button.dataset.pre) {
      event.preventDefault();
      preToggle(button);
    }
  }

  function onInput(event) {
    if (!active() || !event.target.matches?.("[data-pk-slider]")) return;
    const s = state();
    const to = sliderAmount(s, event.target.value);
    if (to === null || to === pk.raiseTo) return;
    pk.raiseTo = to;
    // Update the two amounts in place while dragging (the full render follows on release).
    const label = chips(to);
    const model = raiseModel(s, to);
    for (const node of el.main.querySelectorAll("[data-raise-amt], [data-raise-sub]")) node.textContent = label;
    const send2 = el.main.querySelector('[data-pk="raise-send"] .pkbtn__label');
    if (send2 && model) send2.textContent = model.allIn ? "ALL IN" : model.isBet ? "BET" : "RAISE TO";
  }

  function onChange(event) {
    if (!active() || !event.target.matches?.("[data-pk-slider]")) return;
    deps.render();
  }

  document.addEventListener("click", onClick);
  document.addEventListener("input", onInput);
  document.addEventListener("change", onChange);

  // ---- Commands (data-cmd="pk-…") and sheets -------------------------------------------------

  async function command(cmd, arg) {
    const s = state();
    switch (cmd) {
      case "pk-more":
        deps.sfx("tap", {}, 0.5);
        return deps.openSheet({ kind: "pk-more" });
      case "pk-rule": {
        const chip = ruleChips(s).find((candidate) => candidate.key === arg);
        if (!chip) return undefined;
        deps.sfx("tap", {}, 0.5);
        return deps.act({ type: "settings", settings: chip.patch });
      }
      case "pk-ai-level": {
        pk.aiLevel = nextLevel(s, pk.aiLevel);
        storage.set(AI_KEY, pk.aiLevel);
        deps.sfx("tap", {}, 0.5);
        return deps.render();
      }
      case "pk-add-ai": {
        const levels = Array.isArray(s?.aiLevels) ? s.aiLevels : [];
        const level = levels.includes(pk.aiLevel) ? pk.aiLevel : levels[levels.length - 1] || null;
        return deps.addAi(level);
      }
      case "pk-sit-next": {
        const me = s?.players?.find((player) => player.id === s.viewerId);
        const on = Boolean(s?.you?.sitOutNext || me?.sitOutNext);
        deps.sfx("tap", {}, 0.5);
        return deps.act({ type: on ? "sitIn" : "sitOut" });
      }
      case "pk-see-table":
        pk.seeTable = true;
        return deps.render();
      case "pk-see-result":
        pk.seeTable = false;
        return deps.render();
      case "pk-pref": {
        const [key, raw] = String(arg).split(":");
        const value = raw === "true" ? true : raw === "false" ? false : raw;
        pk.prefs = savePokerPrefs(storage, { ...pk.prefs, [key]: value });
        deps.sfx("tap", {}, 0.6);
        deps.render();
        if (ui.sheet) deps.renderSheet();
        return undefined;
      }
      default:
        return undefined;
    }
  }

  function sheetHtml(sheet) {
    const s = state();
    if (sheet.kind === "pk-more") return moreSheetHtml(s, view({ session: ui.session }));
    if (sheet.kind === "pk-help") return helpSheetHtml(s, view());
    if (sheet.kind === "pk-allin") return allInSheetHtml(s, sheet.amount);
    return "";
  }

  async function sheetCommand(cmd) {
    const s = state();
    switch (cmd) {
      case "pk-invite":
        deps.closeSheet();
        return deps.command("open-invite");
      case "pk-sit-next":
        deps.closeSheet();
        return command("pk-sit-next");
      case "pk-sitin":
        deps.closeSheet();
        return deps.act({ type: "sitIn" });
      case "pk-settings":
        return deps.openSheet({ kind: "settings", back: "pk-more" });
      case "pk-help":
        return deps.openSheet({ kind: "pk-help" });
      case "pk-more":
        return deps.openSheet({ kind: "pk-more" });
      case "pk-leave":
        return deps.command("leave-ask");
      case "pk-end":
        return deps.command("end-game-ask");
      case "pk-allin-yes": {
        const action = ui.sheet?.action || { type: "allin" };
        deps.closeSheet();
        if (!s?.you?.toAct) return undefined;
        return send(action);
      }
      default:
        return undefined;
    }
  }

  // The biggest stack (for END GAME's confirm).
  function leader() {
    const s = state();
    const ranked = [...(s?.players || [])].filter((player) => !["out", "left"].includes(player.status)).sort((a, b) => (b.stack + (b.bet || 0)) - (a.stack + (a.bet || 0)));
    return ranked[0] || null;
  }

  // Demo / scene setup.
  function setup(extra = {}) {
    Object.assign(pk, extra);
  }

  function reset() {
    pk.pre = null;
    pk.raiseOpen = false;
    pk.raiseTo = null;
    pk.seeTable = false;
    pk.bustSeen = null;
    pk.sentKey = null;
    pk.turnKey = undefined;
    pk.frozen = null;
  }

  return { pk, view, render, commit, command, sheetHtml, sheetCommand, leader, setup, reset, isActive: active, fourColour: () => fourColour(pk.prefs, state()) };
}
