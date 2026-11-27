// The quiz on the phone: wires quiz-screens.js (HTML) to the page, and owns the buzzer.
// phone.js owns the session, the socket and the slots; it hands quiz states here.
//
// No animation queue: every state is drawn the moment it arrives (docs/QUIZ-API.md §9). The four
// answer buttons appear when the synced clock (client.js LiveRoom.clock) reaches clue.openAt, the
// frame after they are painted is noted, and a tap sends ms = event.timeStamp − paintTime (§6). One
// tap per clue: the phone locks at once and shows LOCKED IN before the server has answered.

import { morph } from "../shared/morph.js";
import { activeClue, choicesOpen, notePaint, scheduleAt, stepBet, tapMs } from "../shared/quiz-clock.js";
import { canAnswerNow, isQuizState, quizSounds, settingChips } from "../shared/quiz-view.js";
import { aiLevelsOf, betAmount, betContext, helpSheetHtml, moreSheetHtml, quizRoute, renderQuiz } from "./quiz-screens.js";

const AI_KEY = "estate.quizAiLevel";
const TICK_MS = 250;

export function createQuizApp(deps) {
  const { el, ui, storage } = deps;
  const qz = {
    aiLevel: storage.get(AI_KEY) || null,
    tapped: null, // {key, choice}: the tap this phone sent for the clue `key`
    betDraft: null, // {key, amount}
    betSent: "", // the bet key already sent
    picking: false,
    fresh: "", // the clue key whose choices just appeared (a flash on the buttons)
    now: 0,
    open: false,
  };
  const paint = { key: "", shownAt: null, paintAt: null };
  let openCancel = null;
  let openKey = "";
  let tickKey = "";
  let tickCancels = [];
  let ticker = null;
  let chimed = "";
  let freshTimer = null;

  const active = () => isQuizState(ui.state) && Boolean(ui.session);
  const demo = () => Boolean(ui.demo);
  const now = () => deps.serverNow();

  function view(base = {}) {
    const state = ui.state;
    qz.now = now();
    qz.open = choicesOpen(state, qz.now);
    return { ...base, qz, busy: Boolean(el.app.dataset.busy) };
  }

  // ---- Rendering --------------------------------------------------------------------------

  function render(base) {
    const state = ui.state;
    const v = view(base);
    const out = renderQuiz(state, v);
    morph(el.header, out.header);
    morph(el.banner, out.banner);
    morph(el.main, out.main);
    morph(el.nav, out.nav);
    el.app.dataset.game = "quiz";
    el.app.dataset.screen = out.screen;
    el.app.dataset.view = "page";
    el.app.dataset.frame = "page";
    el.app.dataset.modal = "";
    el.boardview.hidden = true;
    el.status.hidden = true;
    afterDraw(state, out.screen);
    syncTimers(state);
    return out.screen;
  }

  // The answer buttons just went on screen: note the paint, flash them, chime and buzz.
  function afterDraw(state, screen) {
    const clue = activeClue(state);
    const open = Boolean(clue) && choicesOpen(state, qz.now);
    if (open && clue.key !== chimed && !["reveal", "finalReveal"].includes(state.phase)) {
      chimed = clue.key;
      const mine = screen === "qz-answer";
      deps.sfx("quizOpen", {}, mine ? 0.9 : 0.5);
      if (mine) deps.buzz(40);
      if (mine && !demo()) {
        qz.fresh = clue.key;
        clearTimeout(freshTimer);
        freshTimer = setTimeout(() => { qz.fresh = ""; if (active()) deps.render(); }, 700);
      }
    }
    if (screen === "qz-answer" && paint.key !== clue.key) {
      paint.key = clue.key;
      paint.shownAt = globalThis.performance?.now?.() ?? null;
      paint.paintAt = null;
      const key = clue.key;
      notePaint((at) => { if (paint.key === key) paint.paintAt = at; });
    }
  }

  // Timers on the synced clock: show the choices at openAt, the last three seconds' ticks (while
  // this phone can still answer), and a light re-render for the bars and seconds.
  function syncTimers(state) {
    const clue = activeClue(state);
    const waiting = clue && !demo() && ["read", "finalRead"].includes(state.phase) && !choicesOpen(state, now()) ? clue.key : "";
    if (waiting !== openKey) {
      openCancel?.();
      openCancel = null;
      openKey = waiting;
      if (waiting) openCancel = scheduleAt(clue.openAt, () => { openCancel = null; if (active()) deps.render(); }, { now });
    }
    const mayAnswer = clue && !demo() && ["read", "answer", "finalRead", "finalAnswer"].includes(state.phase) && clue.closesAt > 0 && state.you && canAnswerNow(state) && !tapFor(clue);
    const ticking = mayAnswer ? clue.key : "";
    if (ticking !== tickKey) {
      for (const cancel of tickCancels) cancel();
      tickCancels = [];
      tickKey = ticking;
      if (ticking) {
        for (const before of [3000, 2000, 1000]) {
          if (clue.closesAt - before > now()) tickCancels.push(scheduleAt(clue.closesAt - before, () => deps.sfx("tick", { last: before === 1000 }, 0.7), { now }));
        }
      }
    }
    const live = !demo() && state?.status === "playing" && (state.phaseEndsAt > 0 || clue);
    if (live && !ticker) ticker = setInterval(() => { if (active() && !document.hidden && !ui.sheet) deps.render(); }, TICK_MS);
    if (!live && ticker) {
      clearInterval(ticker);
      ticker = null;
    }
  }

  function tapFor(clue) {
    return qz.tapped && qz.tapped.key === clue.key ? qz.tapped : null;
  }

  // ---- A new state ------------------------------------------------------------------------

  function commit(prev, next) {
    if (isQuizState(prev) && prev.code === next.code && !document.hidden) {
      for (const hit of quizSounds(prev, next, { viewerId: next.viewerId })) deps.sfx(hit.name, hit.opts, hit.gain);
      if (next.you?.canPick && !prev.you?.canPick) deps.buzz(50);
      if (next.you?.canBet && !prev.you?.canBet) deps.buzz([40, 60, 40]);
    }
    if (next.phase !== "pick") qz.picking = false;
    const bet = betContext(next);
    if (!bet) qz.betSent = "";
    deps.render();
  }

  // ---- Taps -------------------------------------------------------------------------------

  // One tap per clue (docs §2): lock at once, show LOCKED IN, send over the socket. A refusal
  // because the answers were not open yet on the server (a clock a few ms ahead) is sent again just
  // after openAt; any other failure unlocks the buttons.
  async function answer(choice, event) {
    const state = ui.state;
    const clue = activeClue(state);
    if (!clue || tapFor(clue) || !choicesOpen(state, now()) || !canAnswerNow(state) || !state.you) return;
    if (!Number.isInteger(choice) || choice < 0 || choice > 3) return;
    const ms = tapMs(event?.timeStamp, paint.key === clue.key ? paint.paintAt : null, paint.key === clue.key ? paint.shownAt : null);
    qz.tapped = { key: clue.key, choice };
    deps.sfx("quizLock", {}, 0.8);
    deps.buzz(25);
    deps.render();
    const action = ms === null ? { type: "answer", clue: clue.seq, choice } : { type: "answer", clue: clue.seq, choice, ms };
    let result = await deps.send(action);
    if (!result.ok && /not open yet/i.test(result.error || "") && !demo()) {
      await new Promise((resolve) => scheduleAt((clue.openAt || 0) + 60, resolve, { now }));
      result = await deps.send(action);
    }
    if (result.ok || /already/i.test(result.error || "")) return;
    if (qz.tapped?.key === clue.key) qz.tapped = null;
    // "That clue is over": the tap crossed the close in the network and the reveal is on its way,
    // so an error would only talk over it.
    if (result.error && !/clue is over/i.test(result.error)) deps.toast(result.error, { error: true });
    if (active()) deps.render();
  }

  async function pick(category, row) {
    const state = ui.state;
    if (!state?.you?.canPick || qz.picking) return;
    const cell = state.board?.categories?.[category]?.cells?.find((candidate) => candidate.row === row);
    if (!cell || cell.played) return;
    qz.picking = true;
    deps.sfx("tap", {}, 0.5);
    const result = await deps.send({ type: "pick", category, row });
    if (!result.ok) {
      qz.picking = false;
      if (result.error) deps.toast(result.error, { error: true });
    }
  }

  function betStepBy(direction) {
    const state = ui.state;
    const bet = betContext(state);
    if (!bet) return;
    qz.betDraft = { key: bet.key, amount: stepBet(betAmount(state, qz.betDraft), direction, bet.min, bet.max) };
    deps.sfx("tap", {}, 0.4);
    deps.render();
  }

  function betSet(amount) {
    const bet = betContext(ui.state);
    if (!bet) return;
    qz.betDraft = { key: bet.key, amount };
    deps.sfx("tap", {}, 0.4);
    deps.render();
  }

  async function betGo() {
    const state = ui.state;
    const bet = betContext(state);
    if (!bet || qz.betSent === bet.key) return;
    const amount = betAmount(state, qz.betDraft);
    qz.betSent = bet.key;
    deps.sfx("chips", {}, 0.7);
    const result = await deps.send({ type: "bet", amount });
    if (!result.ok) {
      qz.betSent = "";
      if (result.error) deps.toast(result.error, { error: true });
    }
  }

  // Answers go on pointerdown (the earliest moment, so the timing is the person's); a click from a
  // keyboard or screen reader still answers. Everything else on click.
  function onPointer(event) {
    if (!active() || event.button > 0 || el.sheet.contains(event.target)) return;
    const button = event.target.closest?.('button[data-qz="answer"]');
    if (!button || button.disabled) return;
    event.preventDefault();
    answer(Number(button.dataset.choice), event);
  }

  function onClick(event) {
    if (!active() || el.sheet.contains(event.target)) return;
    const button = event.target.closest?.("button[data-qz]");
    if (!button || button.disabled) return;
    event.preventDefault();
    switch (button.dataset.qz) {
      case "answer":
        answer(Number(button.dataset.choice), event);
        break;
      case "pick":
        pick(Number(button.dataset.cat), Number(button.dataset.row));
        break;
      case "bet-step":
        betStepBy(Number(button.dataset.dir) || 0);
        break;
      case "bet-set":
        betSet(Number(button.dataset.amount));
        break;
      case "bet-go":
        betGo();
        break;
      default:
    }
  }

  document.addEventListener("pointerdown", onPointer);
  document.addEventListener("click", onClick);

  // ---- Commands (data-cmd="qz-…") and sheets -------------------------------------------------

  function levelNow(state) {
    const levels = aiLevelsOf(state);
    return levels.includes(qz.aiLevel) ? qz.aiLevel : levels.includes("normal") ? "normal" : levels[0];
  }

  async function command(cmd, arg) {
    const s = ui.state;
    switch (cmd) {
      case "qz-more":
        deps.sfx("tap", {}, 0.5);
        return deps.openSheet({ kind: "qz-more" });
      case "qz-help":
        return deps.openSheet({ kind: "qz-help", back: s?.status !== "lobby" });
      case "qz-setting": {
        const chip = settingChips(s).find((candidate) => candidate.key === arg);
        if (!chip || s?.status !== "lobby") return undefined;
        deps.sfx("tap", {}, 0.5);
        return deps.act({ type: "settings", settings: chip.patch });
      }
      case "qz-ai-level": {
        const levels = aiLevelsOf(s);
        qz.aiLevel = levels[(levels.indexOf(levelNow(s)) + 1) % levels.length];
        storage.set(AI_KEY, qz.aiLevel);
        deps.sfx("tap", {}, 0.5);
        return deps.render();
      }
      case "qz-add-ai":
        return deps.addAi(levelNow(s));
      default:
        return undefined;
    }
  }

  function sheetHtml(sheet) {
    const s = ui.state;
    if (sheet.kind === "qz-more") return moreSheetHtml(s, view({ session: ui.session }));
    if (sheet.kind === "qz-help") return helpSheetHtml(s, { back: sheet.back !== false });
    return "";
  }

  async function sheetCommand(cmd) {
    switch (cmd) {
      case "qz-invite":
        deps.closeSheet();
        return deps.command("open-invite");
      case "qz-settings":
        return deps.openSheet({ kind: "settings", back: "qz-more" });
      case "qz-help":
        return deps.openSheet({ kind: "qz-help" });
      case "qz-more":
        return deps.openSheet({ kind: "qz-more" });
      case "qz-leave":
        return deps.command("leave-ask");
      case "qz-end":
        return deps.command("end-game-ask");
      default:
        return undefined;
    }
  }

  // The top score (for END GAME's confirm).
  function leader() {
    const s = ui.state;
    return [...(s?.players || [])].filter((player) => player.status !== "left").sort((a, b) => b.score - a.score)[0] || null;
  }

  function setup(extra = {}) {
    Object.assign(qz, extra);
  }

  function reset() {
    openCancel?.();
    for (const cancel of tickCancels) cancel();
    clearInterval(ticker);
    clearTimeout(freshTimer);
    openCancel = null;
    tickCancels = [];
    ticker = null;
    openKey = "";
    tickKey = "";
    chimed = "";
    paint.key = "";
    Object.assign(qz, { tapped: null, betDraft: null, betSent: "", picking: false, fresh: "" });
  }

  return { qz, render, commit, command, sheetHtml, sheetCommand, leader, setup, reset, isActive: active, route: (state) => quizRoute(state, view()) };
}
