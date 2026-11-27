// Near Thing on the phone: wires closest-screens.js (HTML) to the page, and keeps this phone's
// drafts (the digits on the pad, where the two chips and the stake are) until they are sent.
// phone.js owns the session, the socket and the slots; it hands Near Thing states here.
//
// Every state is drawn the moment it arrives (docs/CLOSEST-API.md §8: "states apply at once").
// LOCK IN and CONFIRM show the result on this phone at once and go over the socket (POST when it
// is closed, phone.js send). A refusal puts the draft back and says why, except when the phase
// ended while the tap was on its way ("that question is over", "guesses are closed", "bets are
// closed"): the next state is already coming, so a message would only talk over it.

import { morph } from "../shared/morph.js";
import { scheduleAt } from "../shared/quiz-clock.js";
import {
  betAction, betCheck, betKey, closestSounds, closestTimedSounds, guessAction, guessCheck, isClosestState, padPress, padValue, phaseElapsed, placeChip, setStakeSlot, settingChips, stakeMaxOf, stepStake,
} from "../shared/closest-view.js";
import { aiLevelsOf, betIsIn, closestRoute, currentBet, helpSheetHtml, moreSheetHtml, myGuess, padDigits, renderClosest } from "./closest-screens.js";

const AI_KEY = "estate.closestAiLevel";
const TICK_MS = 250;

// Refusals that only mean the phase moved on: no message (see the top of this file).
export const SILENT_ERRORS = /that question is over|guesses are closed|bets are closed/i;

// Sounds the table screen leads with play quieter on the phone.
const PHONE_GAIN = Object.freeze({ slotPop: 0.5, answerSlide: 0.5, chipsLand: 0.6, othersIn: 0.7 });

export function isSilentError(error) {
  return SILENT_ERRORS.test(String(error || ""));
}

export function createClosestApp(deps) {
  const { el, ui, storage } = deps;
  const cg = {
    aiLevel: storage.get(AI_KEY) || null,
    pad: null, // {seq, digits}
    padError: "",
    changing: null, // the question seq whose guess is being changed (the pad is open again)
    sent: null, // {seq, value}: the guess this phone sent and the server hasn't confirmed yet
    sending: false,
    bet: null, // the bet draft (closest-view.js betDraft shape)
    betSent: "", // the betKey this phone sent
    now: 0,
    phaseKey: "",
    elapsed: Infinity, // how far into the phase this phone came in, fixed once per phase (CSS delays)
  };
  let soundKey = "";
  let soundCancels = [];
  let ticker = null;

  const active = () => isClosestState(ui.state) && Boolean(ui.session);
  const demo = () => Boolean(ui.demo);
  const now = () => deps.serverNow();

  function view(base = {}) {
    cg.now = now();
    const state = ui.state;
    const key = state ? `${state.code}|${state.question?.seq ?? 0}|${state.phase}|${state.status}` : "";
    if (key !== cg.phaseKey) {
      cg.phaseKey = key;
      cg.elapsed = demo() || !state ? Infinity : phaseElapsed(state, cg.now); // demos: the end frame
    }
    return { ...base, cg, busy: Boolean(el.app.dataset.busy) };
  }

  // ---- Rendering --------------------------------------------------------------------------

  function render(base) {
    const state = ui.state;
    const out = renderClosest(state, view(base));
    morph(el.header, out.header);
    morph(el.banner, out.banner);
    morph(el.main, out.main);
    morph(el.nav, out.nav);
    el.app.dataset.game = "closest";
    el.app.dataset.screen = out.screen;
    el.app.dataset.view = "page";
    el.app.dataset.frame = "page";
    el.app.dataset.modal = "";
    el.boardview.hidden = true;
    el.status.hidden = true;
    syncTimers(state, out.screen);
    return out.screen;
  }

  // Must this phone still act in this phase? (Its last five seconds tick only then.)
  function mustAct(state, screen) {
    return screen === "cg-guess" || screen === "cg-bet";
  }

  // The clock's sounds for this phase, scheduled once (cancelled when the phase changes), and a
  // light re-render for the seconds while guessing and betting.
  function syncTimers(state, screen) {
    const playing = !demo() && state?.status === "playing" && state.phaseEndsAt > 0;
    const ticks = mustAct(state, screen);
    const key = playing ? `${state.code}|${state.question?.seq ?? 0}|${state.phase}|${state.phaseEndsAt}|${ticks}` : "";
    if (key !== soundKey) {
      for (const cancel of soundCancels) cancel();
      soundCancels = [];
      soundKey = key;
      if (key) {
        for (const hit of closestTimedSounds(state, { viewerId: state.viewerId, ticks })) {
          if (hit.at <= now()) continue;
          const gain = hit.gain * (PHONE_GAIN[hit.trigger] ?? 1);
          soundCancels.push(scheduleAt(hit.at, () => { if (active() && !document.hidden) deps.sfx(hit.name, hit.opts, gain); }, { now }));
        }
      }
    }
    const live = playing && ["guess", "bet"].includes(state.phase);
    if (live && !ticker) ticker = setInterval(() => { if (active() && !document.hidden && !ui.sheet) deps.render(); }, TICK_MS);
    if (!live && ticker) {
      clearInterval(ticker);
      ticker = null;
    }
  }

  // ---- A new state ------------------------------------------------------------------------

  function commit(prev, next) {
    if (isClosestState(prev) && prev.code === next.code && !document.hidden) {
      for (const hit of closestSounds(prev, next, { viewerId: next.viewerId })) deps.sfx(hit.name, hit.opts, hit.gain * (PHONE_GAIN[hit.trigger] ?? 1));
      if (next.you?.canGuess && next.phase === "guess" && (prev.phase !== "guess" || prev.question?.seq !== next.question?.seq)) deps.buzz(50);
      if (next.you?.canBet && !prev.you?.canBet) deps.buzz([40, 60, 40]);
    }
    const seq = next?.question?.seq;
    // The server has the guess (or the question moved on): the pending one is done.
    if (cg.sent && (cg.sent.seq !== seq || (next.you?.guess !== null && next.you?.guess !== undefined))) cg.sent = null;
    if (cg.changing !== null && (cg.changing !== seq || next.phase !== "guess" || !next.you?.canGuess)) cg.changing = null;
    if (cg.betSent && cg.betSent !== betKey(next)) cg.betSent = "";
    if (next.phase !== "guess") cg.padError = "";
    deps.render();
  }

  // ---- Taps -------------------------------------------------------------------------------

  function refused(result) {
    if (result.error && !isSilentError(result.error)) deps.toast(result.error, { error: true });
  }

  function press(key) {
    const state = ui.state;
    const seq = state?.question?.seq;
    if (state?.phase !== "guess" || !state.you?.canGuess) return;
    const digits = padDigits(state, view());
    const next = padPress(digits, key);
    if (next === digits) return;
    cg.pad = { seq, digits: next };
    cg.padError = "";
    deps.sfx("tap", {}, 0.35);
    deps.render();
  }

  async function lock() {
    const state = ui.state;
    const question = state?.question;
    if (state?.phase !== "guess" || !state.you?.canGuess || cg.sending || !question) return;
    const value = padValue(padDigits(state, view()));
    const check = guessCheck(value, question);
    if (!check.ok) {
      cg.padError = check.error;
      deps.render();
      return;
    }
    cg.sent = { seq: question.seq, value };
    cg.changing = null;
    cg.sending = true;
    deps.sfx("quizLock", {}, 0.8);
    deps.buzz(25);
    deps.render();
    const result = await deps.send(guessAction(state, value));
    cg.sending = false;
    if (!result.ok) {
      if (cg.sent?.seq === question.seq) cg.sent = null;
      if (/guess between/i.test(result.error || "")) cg.padError = result.error;
      else refused(result);
    }
    if (active()) deps.render();
  }

  function startChange() {
    const state = ui.state;
    const seq = state?.question?.seq;
    if (state?.phase !== "guess" || !state.you?.canGuess || !(Number(state.you.changesLeft) > 0)) return;
    cg.changing = seq;
    cg.pad = { seq, digits: "" };
    cg.padError = "";
    deps.sfx("tap", {}, 0.4);
    deps.render();
  }

  function keepGuess() {
    cg.changing = null;
    deps.sfx("tap", {}, 0.4);
    deps.render();
  }

  function editBet(change) {
    const state = ui.state;
    if (state?.phase !== "bet" || !state.you?.canBet || betIsIn(state, view())) return;
    const before = currentBet(state, view());
    const after = change(before, state);
    if (after === before) return;
    cg.bet = after;
    deps.sfx("tap", {}, 0.4);
    deps.render();
  }

  async function confirm() {
    const state = ui.state;
    if (state?.phase !== "bet" || !state.you?.canBet || betIsIn(state, view())) return;
    const bet = currentBet(state, view());
    const check = betCheck(bet, state);
    if (!check.ok) {
      deps.toast(check.error, { error: true });
      return;
    }
    const key = betKey(state);
    cg.betSent = key;
    deps.sfx("quizLock", {}, 0.8);
    deps.buzz(25);
    deps.render();
    const result = await deps.send(betAction(state, bet));
    if (!result.ok && !/already bet/i.test(result.error || "")) {
      if (cg.betSent === key) cg.betSent = "";
      refused(result);
    }
    if (active()) deps.render();
  }

  function onClick(event) {
    if (!active() || el.sheet.contains(event.target)) return;
    const button = event.target.closest?.("button[data-cg]");
    if (!button || button.disabled) return;
    event.preventDefault();
    const slot = Number(button.dataset.slot);
    switch (button.dataset.cg) {
      case "pad":
        press(button.dataset.pad);
        break;
      case "lock":
        lock();
        break;
      case "change":
        startChange();
        break;
      case "keep":
        keepGuess();
        break;
      case "chip":
        editBet((bet) => placeChip(bet, Number(button.dataset.which), slot));
        break;
      case "stake-step":
        editBet((bet, state) => stepStake(bet, Number(button.dataset.dir) || 0, stakeMaxOf(state)));
        break;
      case "stake-slot":
        editBet((bet) => setStakeSlot(bet, slot));
        break;
      case "confirm":
        confirm();
        break;
      default:
    }
  }

  // A keyboard works the pad too (a phone with one, or testing on a computer): digits, Backspace,
  // Enter. Never while a sheet is open or an input has the focus.
  function onKey(event) {
    if (!active() || ui.sheet || event.ctrlKey || event.metaKey || event.altKey) return;
    if (event.target?.closest?.("input, textarea, select, [contenteditable]")) return;
    if (el.app.dataset.screen !== "cg-guess") return;
    if (/^[0-9]$/.test(event.key)) press(event.key);
    else if (event.key === "Backspace") press("back");
    else if (event.key === "Enter") lock();
    else return;
    event.preventDefault();
  }

  document.addEventListener("click", onClick);
  document.addEventListener("keydown", onKey);

  // ---- Commands (data-cmd="cg-…") and sheets -------------------------------------------------

  function levelNow(state) {
    const levels = aiLevelsOf(state);
    return levels.includes(cg.aiLevel) ? cg.aiLevel : levels.includes("normal") ? "normal" : levels[0];
  }

  async function command(cmd, arg) {
    const s = ui.state;
    switch (cmd) {
      case "cg-more":
        deps.sfx("tap", {}, 0.5);
        return deps.openSheet({ kind: "cg-more" });
      case "cg-help":
        return deps.openSheet({ kind: "cg-help", back: s?.status !== "lobby" });
      case "cg-setting": {
        const chip = settingChips(s).find((candidate) => candidate.key === arg);
        if (!chip || s?.status !== "lobby") return undefined;
        deps.sfx("tap", {}, 0.5);
        return deps.act({ type: "settings", settings: chip.patch });
      }
      case "cg-ai-level": {
        const levels = aiLevelsOf(s);
        cg.aiLevel = levels[(levels.indexOf(levelNow(s)) + 1) % levels.length];
        storage.set(AI_KEY, cg.aiLevel);
        deps.sfx("tap", {}, 0.5);
        return deps.render();
      }
      case "cg-add-ai":
        return deps.addAi(levelNow(s));
      default:
        return undefined;
    }
  }

  function sheetHtml(sheet) {
    const s = ui.state;
    if (sheet.kind === "cg-more") return moreSheetHtml(s, view({ session: ui.session }));
    if (sheet.kind === "cg-help") return helpSheetHtml(s, { back: sheet.back !== false });
    return "";
  }

  async function sheetCommand(cmd) {
    switch (cmd) {
      case "cg-invite":
        deps.closeSheet();
        return deps.command("open-invite");
      case "cg-settings":
        return deps.openSheet({ kind: "settings", back: "cg-more" });
      case "cg-help":
        return deps.openSheet({ kind: "cg-help" });
      case "cg-more":
        return deps.openSheet({ kind: "cg-more" });
      case "cg-leave":
        return deps.command("leave-ask");
      case "cg-end":
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
    Object.assign(cg, extra);
  }

  function reset() {
    for (const cancel of soundCancels) cancel();
    clearInterval(ticker);
    soundCancels = [];
    soundKey = "";
    ticker = null;
    Object.assign(cg, { pad: null, padError: "", changing: null, sent: null, sending: false, bet: null, betSent: "" });
  }

  return { cg, render, commit, command, sheetHtml, sheetCommand, leader, setup, reset, isActive: active, route: (state) => closestRoute(state, view()), myGuess: (state) => myGuess(state, view()) };
}
