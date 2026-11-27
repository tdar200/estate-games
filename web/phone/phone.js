// Estate phone: the board (every role), each player's money, deeds and buttons. Renders server
// state through the pure screens in screens.js; all rules run on the server (docs/API.md).
//
// State arrival works like the PC table: new states are queued, and each one first plays its
// events on the phone board (dice → hops with a follow camera → landing glow → money) and only
// then renders the result and the decision. Animations run against a time budget and are skipped
// when the page is hidden, so a backgrounded phone can never freeze the queue.

import * as boardShared from "../shared/board.js";
import * as deedShared from "../shared/deed.js";
import { DeviceSocket, EstateClient, LiveRoom, demoParam, expandReplay, loadFixture, normaliseCode, storage, unreachable } from "../shared/client.js";
import { awaitApproval, showClaimPrompts } from "../shared/claims.js"; // seat-approval hook (server review S1)
import { morph } from "../shared/morph.js";
import { inviteLine, inviteText, openWhatsApp } from "../shared/invite.js";
import { fontsReady } from "../shared/fonts.js";
import { diffEvents } from "../shared/events.js";
import { AI_LEVELS, escapeHtml, money, nextAiLevel, playerById, possessive, upper } from "../shared/format.js";
import {
  canManage, canMortgage, debtPlan, finalScore, housePayback, loanSecuredBy, mortgageValue, netWorth, nextBuild, nextSale, propState, rentLabel,
  rulesOf, suggestLoan, unmortgageCost,
} from "../shared/insights.js";
import * as motion from "../shared/motion.js";
import * as sound from "../shared/sound.js";
import { TOKEN_COLORS, TOKEN_KINDS, canonicalKind, ensureMetalDefs, playerColor } from "../shared/tokens.js";
import { PhoneBoard } from "./board-view.js";
import { boardModeFor, deskGameFrame, hopZoom, layoutFor } from "./layout.js";
import { buildScene, sceneById, sceneFixtures } from "./scenarios.js";
import { cueHtml, dotHtml, newCues } from "./cues.js";
import { icon } from "./icons.js";
import { animatable, playEvents, yourTurnCue } from "./playback.js";
import { FALLBACK_SOUNDS, cardHoldMs, loadAudio, moodFor, saveAudio, sfxForAction, sfxForCue, sfxForEvent } from "./audio.js";
import { gameAction, howToPlayHtml, joinMode, me, myDecision, renderPhone, settingsHtml } from "./screens.js";
import { setOwnersHtml } from "./impact.js";
import { leaderNow, loadPrefs, paceOf, reducedMotion, savePrefs } from "./settings.js";
import { cleanDraft, emptyDraft, loanActionOf, plannedTrade, quoteKey, tradeActionOf, tradeValid, wantsQuote } from "./trade.js";
import { deedActionButtons } from "./deedsheet.js";
import { rematchReady, resultShownAt } from "./parts.js";
import { createPokerApp } from "./poker-app.js";
import { isPoker, pokerSettingsRows } from "./poker-screens.js";
import { buildPokerScene } from "../shared/poker-scenes.js";
import { createLudoApp } from "./ludo-app.js";
import { isLudo } from "./ludo-screens.js";
import { buildLudoScene } from "../shared/ludo-scenes.js";
import { createQuizApp } from "./quiz-app.js";
import { isQuiz } from "./quiz-screens.js";
import { buildQuizScene, demoClock } from "../shared/quiz-scenes.js";
import { quizMood } from "../shared/quiz-view.js";
import { createClosestApp } from "./closest-app.js";
import { isClosest } from "./closest-screens.js";
import { buildClosestScene } from "../shared/closest-scenes.js";
import { closestMood } from "../shared/closest-view.js";
import { GAME_NAME as CLOSEST_NAME } from "../shared/closest-name.js";
import { gameLabel } from "../shared/poker-view.js";
import { installState, installSteps, promptInstall, startApp } from "../shared/pwa.js";
import { isTimed, tickAuctionClock } from "../shared/auction-clock.js";
import { toggleDebtRow } from "../shared/debt-checklist.js";
import { minutesText, showAwayList } from "../shared/standin.js";
import { debtKey, debtListFor, idleTarget, removeSheetHtml } from "./ux-screens.js";

const SESSION_KEY = "estate.phone";
const PROFILE_KEY = "estate.profile";
const AI_LEVEL_KEY = "estate.aiLevel";
// The last table this browser sat at (or watched): offered as "BACK TO TABLE …" from anywhere.
const LAST_TABLE_KEY = "estate.lastTable";
// Set once this phone has seen HOW TO PLAY (shown by itself on the first Estate table it joins).
const HOW_SEEN_KEY = "estate.howToSeen";
const INSTALL_HIDDEN_KEY = "estate.installHidden";
const SUMMARY_POLL_MS = 4000;
const GAMES_POLL_MS = 5000;
const $ = (selector) => document.querySelector(selector);
const el = {
  app: $("#app"), header: $("#header"), banner: $("#banner"), main: $("#main"), nav: $("#nav"),
  boardview: $("#boardview"), board: $("#board"), status: $("#status"),
  fx: $("#fx"), cues: $("#cues"), sheet: $("#sheet"), toast: $("#toast"),
};

const profile = storage.get(PROFILE_KEY) || {};
const ui = {
  client: new EstateClient(),
  live: null,
  device: null,
  session: null,
  state: null,
  server: null,
  demo: null,
  tab: "turn",
  inviteOpen: false,
  invitedView: false,
  remote: false,
  palette: { ...TOKEN_COLORS },
  aiLevel: AI_LEVELS.includes(storage.get(AI_LEVEL_KEY)) ? storage.get(AI_LEVEL_KEY) : "hard",
  join: { code: "", name: profile.name || "", tokenKind: canonicalKind(profile.tokenKind) || null, summary: null, error: "", busy: false, lockedCode: false, mode: null, note: "", reclaim: null, loading: false, missing: false },
  lastTable: storage.get(LAST_TABLE_KEY),
  // ACTIVE GAMES on the home screen (GET /api/games); null until the first answer.
  games: { list: null, error: "" },
  // Whether a PC table shows this lobby (from the room summary); undefined until known.
  tableOnline: undefined,
  trade: emptyDraft(),
  loanDraft: null,
  // A trade saved for your turn. Kept apart from the trade page's draft, which always opens empty.
  plan: null,
  autopilotSeen: false,
  // Names in the order this phone saw players go bankrupt (the engine log is capped).
  outSeen: [],
  friends: { list: null, error: "", sent: {} },
  invite: null,
  sheet: null,
  copied: false,
  qrUrl: null,
  muted: sound.isMuted(),
  // Effects on, music off (the PC usually plays it), one volume; saved on this phone.
  audio: loadAudio(storage),
  // Vibration, animation speed, follow camera, reduced motion (settings.js); saved on this device.
  prefs: loadPrefs(storage),
  musicMood: null,
  canShake: false,
  decision: null,
  lastScreen: null,
  // When this finished game's result screen first appeared: PLAY AGAIN waits a moment (parts.js).
  resultShownAt: null,
  pendingEvents: [],
  queue: [],
  draining: false,
  playing: false,
  acting: false,
  skip: null,
  shownCash: new Map(),
};

const pboard = new PhoneBoard(el.board, { onSquareTap: (index) => openSheet({ kind: "square", index }) });

// Poker (state.game === "poker"): its own screens and controls (poker-app.js); this file keeps the
// session, the socket, the sheets and the shared commands.
const poker = createPokerApp({
  el,
  ui,
  storage,
  render: () => render(),
  act: (action) => act(action),
  sfx: (name, opts, gain) => sfx(name, opts, gain),
  buzz: (pattern) => buzz(pattern),
  openSheet: (sheet) => openSheet(sheet),
  closeSheet: () => closeSheet(),
  renderSheet: () => renderSheet(),
  command: (cmd, arg) => command(cmd, arg),
  addAi: (level) => call("POST /ai", () => ui.client.addAi(ui.session.code, ui.session.token, level)),
});

// Ludo (state.game === "ludo"): its own screens, controls and animations (ludo-app.js), the same
// way as poker.
const ludo = createLudoApp({
  el,
  ui,
  storage,
  render: () => render(),
  act: (action) => act(action),
  sfx: (name, opts, gain) => sfx(name, opts, gain),
  buzz: (pattern) => buzz(pattern),
  openSheet: (sheet) => openSheet(sheet),
  closeSheet: () => closeSheet(),
  renderSheet: () => renderSheet(),
  command: (cmd, arg) => command(cmd, arg),
  addAi: (level) => call("POST /ai", () => ui.client.addAi(ui.session.code, ui.session.token, level)),
});

// The quiz (state.game === "quiz"): its own screens and the buzzer (quiz-app.js). Its taps go over
// the room socket (sendQuick) and its clock is the socket's synced clock (docs/QUIZ-API.md §1.1, §6).
const quiz = createQuizApp({
  el,
  ui,
  storage,
  render: () => render(),
  act: (action) => act(action),
  send: (action) => sendQuick(action),
  serverNow: () => (ui.demo ? demoClock(ui.state) : ui.live?.clock?.now() ?? Date.now()),
  sfx: (name, opts, gain) => sfx(name, opts, gain),
  buzz: (pattern) => buzz(pattern),
  toast: (message, opts) => toast(message, opts),
  openSheet: (sheet) => openSheet(sheet),
  closeSheet: () => closeSheet(),
  renderSheet: () => renderSheet(),
  command: (cmd, arg) => command(cmd, arg),
  addAi: (level) => call("POST /ai", () => ui.client.addAi(ui.session.code, ui.session.token, level)),
});

// Near Thing (state.game === "closest"): its own screens, the number pad and the chips
// (closest-app.js). Guesses and bets go over the room socket too (sendQuick).
const closest = createClosestApp({
  el,
  ui,
  storage,
  render: () => render(),
  act: (action) => act(action),
  send: (action) => sendQuick(action),
  serverNow: () => (ui.demo ? demoClock(ui.state) : ui.live?.clock?.now() ?? Date.now()),
  sfx: (name, opts, gain) => sfx(name, opts, gain),
  buzz: (pattern) => buzz(pattern),
  toast: (message, opts) => toast(message, opts),
  openSheet: (sheet) => openSheet(sheet),
  closeSheet: () => closeSheet(),
  renderSheet: () => renderSheet(),
  command: (cmd, arg) => command(cmd, arg),
  addAi: (level) => call("POST /ai", () => ui.client.addAi(ui.session.code, ui.session.token, level)),
});

// The game types this phone can start or switch to.
const GAME_TYPES = ["estate", "poker", "ludo", "quiz", "closest"];
const gameTypeOrEstate = (value) => (GAME_TYPES.includes(value) ? value : "estate");
const gameTypeOfState = (state) => (isPoker(state) ? "poker" : isLudo(state) ? "ludo" : isQuiz(state) ? "quiz" : isClosest(state) ? "closest" : "estate");

// ---------------------------------------------------------------------------------------------
// Rendering

// The server's clock (Unix ms) for the screens' clocks and IDLE m:ss: the socket's synced clock
// live; in a demo the fixture's frozen `now` (null without one, so nothing counts from a guess).
function serverNow() {
  if (ui.demo) return Number(ui.state?.now) > 0 ? Number(ui.state.now) : null;
  return ui.live?.clock?.now?.() ?? Date.now();
}

function view() {
  return {
    session: ui.session,
    serverNow: serverNow(),
    hostOpen: ui.hostOpen,
    debtPicks: ui.debtPicks,
    tab: ui.tab,
    inviteOpen: ui.inviteOpen,
    invitedView: ui.invitedView,
    remote: ui.remote,
    palette: ui.palette,
    aiLevel: ui.aiLevel,
    join: ui.join,
    lastTable: ui.lastTable,
    games: ui.games,
    tableOnline: ui.tableOnline,
    trade: ui.trade,
    plan: ui.plan,
    outSeen: ui.outSeen,
    loanDraft: ui.loanDraft,
    friends: ui.friends,
    invite: ui.invite,
    copied: ui.copied,
    qrUrl: ui.qrUrl,
    inviteLink: inviteLink(),
    linkIsLan: !ui.server?.publicURL && (!ui.demo || ui.demo === "scene"),
    muted: ui.muted,
    audio: ui.audio,
    layout: document.documentElement.dataset.layout || "phone",
    tradeInfo: ui.tradeInfo,
    canShake: ui.canShake,
    install: installState(),
    installHidden: Boolean(storage.get(INSTALL_HIDDEN_KEY)),
    now: Date.now(),
  };
}

function inviteLink() {
  const code = ui.state?.code || ui.session?.code || "";
  const base = ui.server?.publicURL || ui.server?.lanURL || location.origin;
  return `${base.replace(/\/$/, "")}/j/${code}`;
}

function setBusy() {
  if (ui.playing || ui.acting) el.app.dataset.busy = "1";
  else delete el.app.dataset.busy;
}

function render() {
  const screen = drawScreen();
  ui.resultShownAt = resultShownAt(screen, ui.state?.status, ui.resultShownAt, Date.now());
  return screen;
}

function drawScreen() {
  const active = document.activeElement;
  const focusId = active?.id || null;
  const focusAttr = !focusId && active ? ["data-tab", "data-cmd", "data-act", "data-toggle", "data-partner", "data-token", "data-pk", "data-pre", "data-pk-slider", "data-qz", "data-cg"].map((name) => [name, active.getAttribute?.(name)]).find(([, value]) => value !== null && value !== undefined) : null;
  const selection = active && "selectionStart" in active ? [active.selectionStart, active.selectionEnd] : null;
  if (isClosest(ui.state) && ui.session) {
    const screen = closest.render(view());
    restoreFocus(focusId, focusAttr, selection);
    if (ui.sheet) renderSheet();
    setBusy();
    if (screen === "cg-lobby") watchLobby();
    if (screen !== ui.lastScreen) {
      if (ui.lastScreen) window.scrollTo({ top: 0 });
      ui.lastScreen = screen;
    }
    return screen;
  }
  if (isQuiz(ui.state) && ui.session) {
    const screen = quiz.render(view());
    restoreFocus(focusId, focusAttr, selection);
    if (ui.sheet) renderSheet();
    setBusy();
    if (screen === "qz-lobby") watchLobby();
    if (screen !== ui.lastScreen) {
      if (ui.lastScreen) window.scrollTo({ top: 0 });
      ui.lastScreen = screen;
    }
    return screen;
  }
  if (isLudo(ui.state) && ui.session) {
    const screen = ludo.render(view());
    restoreFocus(focusId, focusAttr, selection);
    if (ui.sheet) renderSheet();
    setBusy();
    if (screen === "ld-lobby") watchLobby();
    if (screen !== ui.lastScreen) {
      if (ui.lastScreen && screen !== "ld-table") window.scrollTo({ top: 0 });
      ui.lastScreen = screen;
    }
    return screen;
  }
  if (isPoker(ui.state) && ui.session) {
    const screen = poker.render(view());
    restoreFocus(focusId, focusAttr, selection);
    if (ui.sheet) renderSheet();
    setBusy();
    if (screen === "pk-lobby") watchLobby();
    if (screen !== ui.lastScreen) {
      if (ui.lastScreen && screen !== "pk-table") window.scrollTo({ top: 0 });
      ui.lastScreen = screen;
    }
    return screen;
  }
  delete el.app.dataset.game;
  const out = renderPhone(ui.state, view());
  // While a playback runs, the strip and board belong to it; only the rest follows the tab.
  // Every slot is updated in place (shared/morph.js): rebuilding it on each state replayed its
  // entrance animations and restarted its rings, which read as flashes on every update.
  if (!ui.playing) {
    morph(el.header, out.header);
    tickPlates();
  }
  morph(el.banner, out.banner);
  morph(el.main, out.main);
  morph(el.nav, out.nav);
  el.app.dataset.screen = out.screen;
  el.app.dataset.view = out.board ? "board" : "page";
  // Desk layout: in a game the board stays on the left whatever the tab shows on the right.
  const layout = document.documentElement.dataset.layout;
  const deskGame = layout === "desk" && deskGameFrame(out.screen, ui.state) && Boolean(out.board || out.ambient);
  // Tablet and PC: trade opens as a large modal over the dimmed board (the phone keeps a page).
  const tradeModal = layout !== "phone" && ["trade", "loanDraft"].includes(out.screen) && Boolean(out.ambient);
  el.app.dataset.modal = tradeModal ? "trade" : "";
  const boardData = out.board || (deskGame || tradeModal ? out.ambient : null);
  el.app.dataset.frame = deskGame ? "game" : "page";
  el.boardview.hidden = !boardData;
  el.status.hidden = !boardData;
  if (boardData && !ui.playing) {
    fitBoardMode();
    pboard.show(ui.state);
    pboard.apply(boardData);
    morph(el.status, boardData.status || "");
  }
  if (out.screen !== ui.lastScreen) {
    if (ui.lastScreen) window.scrollTo({ top: 0 });
    ui.lastScreen = out.screen;
  }
  restoreFocus(focusId, focusAttr, selection);
  if (ui.sheet) renderSheet();
  setBusy();
  if (out.screen === "trade") maybeQuote();
  if (out.screen === "join") {
    watchSummary();
    if (joinMode(ui.join) === "home") watchGames();
  }
  if (out.screen === "lobby") watchLobby();
  return out.screen;
}

// After a render: focus the same control again (its node may have been replaced).
function restoreFocus(focusId, focusAttr, selection) {
  let target = focusId ? document.getElementById(focusId) : null;
  if (!target && focusAttr) target = [...document.querySelectorAll(`[${focusAttr[0]}]`)].find((node) => node.getAttribute(focusAttr[0]) === focusAttr[1]);
  if (target && target !== document.body && target !== document.activeElement) {
    target.focus({ preventScroll: true });
    if (selection && "setSelectionRange" in target && target.type === "text") {
      try { target.setSelectionRange(selection[0], selection[1]); } catch { /* number inputs */ }
    }
  }
}

// Compact board on small screens, the full board (names, prices) once it is FULL_BOARD_MIN wide.
function fitBoardMode() {
  const px = el.board.clientWidth;
  if (px > 0) pboard.setMode(boardModeFor(px));
}

// html[data-layout]: phone, tablet or desk (layout.js). Re-render when it changes.
function applyLayout() {
  const layout = layoutFor(window.innerWidth, window.innerHeight);
  if (document.documentElement.dataset.layout === layout) return false;
  document.documentElement.dataset.layout = layout;
  return true;
}

function applyMotion() {
  // The shared motion module honours the override and the pace too (its own effects follow them).
  motion.setMotionOverride?.(ui.prefs.motion === "auto" ? "system" : ui.prefs.motion);
  motion.setAnimationSpeed?.(ui.prefs.speed);
  const still = reducedMotion(ui.prefs, motion.prefersReducedMotion());
  if (still) document.documentElement.dataset.motion = "reduce";
  else delete document.documentElement.dataset.motion;
}

function installLayout() {
  applyLayout();
  applyMotion();
  let timer = null;
  const onResize = () => {
    clearTimeout(timer);
    timer = setTimeout(() => {
      applyLayout();
      if (!ui.playing && (ui.state || !ui.session)) render();
    }, 120);
  };
  window.addEventListener("resize", onResize);
  window.addEventListener("orientationchange", onResize);
}

// Plate cash counts from what it showed last to the new value, flashing green or red.
function tickPlates() {
  for (const node of el.header.querySelectorAll("[data-cash-for]")) {
    const id = node.dataset.cashFor;
    const value = Number(node.dataset.cash);
    const from = ui.shownCash.get(id);
    ui.shownCash.set(id, value);
    if (from === undefined || from === value) continue;
    node.textContent = money(from);
    motion.tickNumber(node, from, value);
    const plate = node.closest(".plate");
    plate?.classList.add(value > from ? "is-flash-pos" : "is-flash-neg");
    setTimeout(() => plate?.classList.remove("is-flash-pos", "is-flash-neg"), 1100);
  }
}

function toast(message, { error = false } = {}) {
  el.toast.textContent = message;
  el.toast.hidden = false;
  el.toast.classList.toggle("is-error", error);
  clearTimeout(ui.toastTimer);
  if (!ui.demoHold) ui.toastTimer = setTimeout(() => { el.toast.hidden = true; }, error ? 4200 : 2400);
}

// One short outcome chip that slides in under the strip and fades ("Maya accepted your trade",
// "SOLD Skyline → Alpha"). A newer one replaces it; there is never a list.
function showCue(html) {
  const node = document.createElement("div");
  node.className = "cue-toast";
  node.innerHTML = html;
  el.cues.replaceChildren(node);
  if (ui.demoHold) return;
  setTimeout(() => node.classList.add("is-leaving"), 2400);
  setTimeout(() => node.remove(), 2900);
}

// ---------------------------------------------------------------------------------------------
// Sound and music

// One effect at this phone's volume. gain < 1 for other players' events (quieter, so a room of
// phones doesn't all play the same sound). Older sound.js builds without play() use what they have.
function sfx(name, opts = {}, gain = 1) {
  // The last few effects, for QA (window.__phone.ui.sfxLog).
  (ui.sfxLog ||= []).push({ name, gain: Math.round(gain * 100) / 100, at: Date.now() });
  if (ui.sfxLog.length > 60) ui.sfxLog.shift();
  if (ui.muted || !ui.audio.effects || gain <= 0) return;
  // The shared engine applies the device's effects volume itself: pass only the relative level.
  const volume = Math.max(0, Math.min(1, SHARED_SETTINGS ? gain : ui.audio.volume * gain));
  if (typeof sound.play === "function") {
    try { sound.play(name, { ...opts, volume }); } catch { /* never let a sound break the game */ }
    return;
  }
  if (gain < 0.5) return;
  const fallback = sound[FALLBACK_SOUNDS[name]];
  if (typeof fallback === "function") fallback(name === "land" || name === "yourTurn" ? 2 : undefined);
}

// web/shared/music.js arrives with the table agent's sound work; until then music is a no-op.
let music = null;
import("../shared/music.js").then((module) => {
  music = module;
  applyAudio();
}).catch(() => { music = null; });

// The shared engine (web/shared/sound.js) keeps one "estate.sound" per browser, which a table open
// in another tab of the same browser shares. So the phone's one volume is that device volume
// (effects at the volume, music at the shared balance: 0.8 → effects 0.8, music 0.45, exactly
// the shared defaults), and the phone writes the shared on/off flags only to turn something ON.
// The phone's own "off" (music is off by default here) just means this page plays nothing, so it
// never silences the table's music.
const SHARED_SETTINGS = typeof sound.getSettings === "function" && typeof sound.setEffectsVolume === "function";
const MUSIC_BALANCE = (sound.DEFAULT_SETTINGS?.music ?? 0.45) / (sound.DEFAULT_SETTINGS?.effects ?? 0.8);
// On load the phone adopts the device's volume and anything switched off on the device, and writes
// nothing; it pushes only what its user changes later.
if (SHARED_SETTINGS) {
  try {
    const device = sound.getSettings();
    ui.audio = { ...ui.audio, volume: device.effects, effects: ui.audio.effects && device.effectsOn, music: ui.audio.music && device.musicOn };
    ui.audioApplied = `${ui.audio.effects}|${ui.audio.music}|${ui.audio.volume}`;
  } catch { /* keep the phone's own */ }
}

function pushSharedAudio() {
  if (!SHARED_SETTINGS) return;
  const key = `${ui.audio.effects}|${ui.audio.music}|${ui.audio.volume}`;
  if (key === ui.audioApplied) return;
  ui.audioApplied = key;
  try {
    const now = sound.getSettings();
    const musicVolume = Math.round(ui.audio.volume * MUSIC_BALANCE * 100) / 100;
    if (now.effects !== ui.audio.volume) sound.setEffectsVolume(ui.audio.volume);
    if (Math.abs(now.music - musicVolume) > 0.005) sound.setMusicVolume?.(musicVolume);
    if (ui.audio.effects && !now.effectsOn) sound.setEffectsEnabled?.(true);
    if (ui.audio.music && !now.musicOn) sound.setMusicEnabled?.(true);
  } catch { /* storage blocked: the engine keeps its defaults for this visit */ }
}

// Push the settings to the engines, and start, change or stop the music for the current game.
function applyAudio() {
  pushSharedAudio();
  if (!music) return;
  const on = ui.audio.music && !ui.muted && !ui.demo;
  try {
    if (!SHARED_SETTINGS) {
      music.setVolume?.(ui.audio.volume);
      music.setEnabled?.(on);
    }
    const mood = on ? (isQuiz(ui.state) ? quizMood(ui.state) : isClosest(ui.state) ? closestMood(ui.state) : typeof music.moodFor === "function" ? music.moodFor(ui.state) : moodFor(ui.state)) || "lobby" : null;
    if (!mood) {
      if (ui.musicMood) music.stop?.();
      ui.musicMood = null;
      return;
    }
    if (!ui.musicMood) music.start?.(mood);
    else if (mood !== ui.musicMood) music.setMood?.(mood);
    ui.musicMood = mood;
  } catch {
    ui.musicMood = null;
  }
}

function setAudio(patch) {
  ui.audio = saveAudio(storage, { ...ui.audio, ...patch });
  applyAudio();
}

// ---------------------------------------------------------------------------------------------
// State arrival: queue → play events on the board → render

function decisionKey(state) {
  const decision = myDecision(state);
  return decision ? `${decision}|${state.phase}|${state.currentPlayerId}|${state.auction?.currentBid ?? ""}` : null;
}

function applyState(next) {
  // [seat-approval hook] the host (or, when the host is away, the other players) answers requests
  // to move a seat or the table to a new device (state.claims; docs/API.md "Getting back in").
  if (!ui.demo) showClaimPrompts(next, { client: ui.client, code: ui.session?.code, token: ui.session?.token });
  // Events are derived from the state diff (the server derives its WS events the same way), so a
  // late WS event can never be attached to the wrong state.
  ui.pendingEvents.length = 0;
  ui.queue.push(next);
  if (!ui.draining) drain();
}

const ANIMATION_BUDGET_BASE_MS = 4000;
const ANIMATION_BUDGET_PER_EVENT_MS = 2500;

function boardShowing() {
  return !el.boardview.hidden && (el.app.dataset.view === "board" || el.app.dataset.frame === "game");
}

function plateEl(id) {
  return [...el.header.querySelectorAll("[data-plate]")].find((node) => node.dataset.plate === id) || null;
}

// Vibration only works after the user has touched the page; skip it before that.
function buzz(pattern) {
  if (!ui.prefs.vibration) return;
  if (navigator.userActivation && !navigator.userActivation.hasBeenActive) return;
  navigator.vibrate?.(pattern);
}

async function drain() {
  ui.draining = true;
  try {
    while (ui.queue.length) {
      const next = ui.queue.shift();
      const prev = ui.state;
      const behind = ui.queue.length > 2;
      const live = prev && next && prev.code === next.code && prev.status === "playing" && next.status !== "lobby";
      const still = reducedMotion(ui.prefs, motion.prefersReducedMotion());
      let readHold = 0;
      if (live && !behind && !document.hidden && boardShowing()) {
        const events = animatable(diffEvents(prev, next));
        if (events.length && !still) await play(prev, next, events);
        else if (still) readHold = events.filter((event) => event.type === "card").reduce((sum, event) => sum + cardHoldMs(event, next), 0);
      }
      commit(prev, next);
      // Without animations the card is shown at once: keep it there long enough to read.
      for (const end = Date.now() + readHold; Date.now() < end && ui.queue.length <= 2 && !document.hidden;) await motion.wait(150);
    }
  } finally {
    ui.draining = false;
    setBusy();
  }
}

async function play(prev, next, events) {
  ui.playing = true;
  setBusy();
  let cancelled = false;
  let timedOut = false;
  const skip = new Promise((resolve) => { ui.skip = resolve; });
  const ctx = {
    board: pboard,
    layer: el.fx,
    plate: plateEl,
    cash: new Map(prev.players.map((player) => [player.id, ui.shownCash.get(player.id) ?? player.cash])),
    viewerId: next.viewerId,
    buzz,
    zoom: hopZoom(el.board.clientWidth),
    cancelled: () => cancelled,
    behind: () => ui.queue.length > 2,
    sfx,
    pace: paceOf(ui.prefs),
    // Tablet and PC boards are big enough to follow a hop without zooming: the whole board stays
    // in view. The small phone board zooms in on the hop and eases back out after it lands.
    follow: ui.prefs.follow && boardModeFor(el.board.clientWidth) === "compact",
  };
  // Animations wait on browser frames. A hidden or frozen tab produces none, so never let them
  // hold the phone hostage: after a budget (or when the page is hidden), jump to the new state.
  // A drawn card gets its reading time on top (audio.js cardHoldMs).
  const reading = events.filter((event) => event.type === "card").reduce((sum, event) => sum + cardHoldMs(event, next), 0);
  const budget = ANIMATION_BUDGET_BASE_MS + events.length * ANIMATION_BUDGET_PER_EVENT_MS + reading;
  try {
    await Promise.race([
      playEvents(ctx, prev, next, events),
      motion.wait(budget).then(() => { timedOut = true; }),
      skip.then(() => { timedOut = true; }),
    ]);
  } catch (error) {
    console.warn("Animation skipped:", error);
  }
  cancelled = true;
  ui.skip = null;
  if (timedOut) pboard.cancelEffects();
  for (const [id, value] of ctx.cash) ui.shownCash.set(id, value);
  pboard.dirtyStage();
  ui.playing = false;
}

document.addEventListener("visibilitychange", () => {
  if (document.hidden) ui.skip?.();
});

function commit(prev, next) {
  if (isClosest(next)) {
    ui.state = next;
    ui.decision = null;
    closest.commit(prev, next);
    applyAudio();
    return;
  }
  if (isQuiz(next)) {
    ui.state = next;
    ui.decision = null;
    quiz.commit(prev, next);
    applyAudio();
    return;
  }
  if (isLudo(next)) {
    ui.state = next;
    ui.decision = null;
    autopilotNote(next);
    ludo.commit(prev, next);
    applyAudio();
    return;
  }
  if (isPoker(next)) {
    ui.state = next;
    ui.decision = null;
    autopilotNote(next);
    poker.commit(prev, next);
    applyAudio();
    return;
  }
  ui.state = next;
  // Ticks on the debt checklist belong to one debt; a new one starts from the plan again.
  if (ui.debtPicks && ui.debtPicks.key !== debtKey(next)) ui.debtPicks = null;
  const key = decisionKey(next);
  const myTurnPhases = ["awaiting_roll", "awaiting_purchase", "turn_complete"];
  const prevDecision = ui.decision?.split("|")[0];
  let turnStarted = false;
  if (key && key !== ui.decision) {
    const nowDecision = key.split("|")[0];
    const stayInPlace = ["property", "trade"].includes(ui.tab) && myTurnPhases.includes(prevDecision) && myTurnPhases.includes(nowDecision);
    if (prev && !stayInPlace && !ui.loanDraft) {
      leaveTradeTab("turn");
      ui.tab = "turn";
      ui.inviteOpen = false;
    }
    if (prev && !prevDecision) buzz(60);
    turnStarted = Boolean(prev) && nowDecision === "awaiting_roll" && prev.currentPlayerId !== next.viewerId;
  }
  ui.decision = key;
  if (ui.loanDraft && next.status !== "playing") ui.loanDraft = null;
  tradeHygiene(prev, next);
  autopilotNote(next);
  noteOuts(prev, next);
  const screen = render();
  afterCommit(prev, next, screen, turnStarted);
  applyAudio();
}

// Keep the trade draft honest: it clears when your turn ends and drops deeds that changed hands.
// A plan saved for your turn is separate; say when it is ready, or drop it when it went stale.
function tradeHygiene(prev, next) {
  const viewerId = next.viewerId;
  if (!viewerId) return;
  const wasMine = prev?.currentPlayerId === viewerId;
  const isMine = next.currentPlayerId === viewerId;
  if (prev && wasMine && !isMine) ui.trade = emptyDraft();
  if (ui.trade.targetId) ui.trade = { ...cleanDraft(next, ui.trade, viewerId).draft, quote: ui.trade.quote };
  if (prev && !wasMine && isMine && ui.plan) {
    const plan = plannedTrade(next, ui.plan);
    if (plan) toast(`Your trade for ${plan.target.name} is ready: tap SEND TRADE`);
    else {
      ui.plan = null;
      toast("Your planned trade is out of date: deeds changed hands");
    }
  }
}

function noteOuts(prev, next) {
  if (next.status === "lobby" || (prev && prev.code !== next.code)) ui.outSeen = [];
  for (const player of next.players || []) {
    const before = prev?.players?.find((candidate) => candidate.id === player.id);
    if (player.bankrupt && before && !before.bankrupt && !ui.outSeen.includes(player.name)) ui.outSeen.push(player.name);
  }
}

// The computer played for this phone's player while it was away (players[].autopilot): say so
// once, when the phone is back in charge.
function autopilotNote(next) {
  const mine = me(next);
  if (!mine) return;
  if (mine.autopilot) ui.autopilotSeen = true;
  else if (ui.autopilotSeen) {
    ui.autopilotSeen = false;
    // YOU'RE BACK (UX-07) lists what happened on the board itself; the toast is for the rest.
    if (!showAwayList(mine)) toast("The computer played for you");
  }
}

function afterCommit(prev, next, screen, turnStarted) {
  if (!prev) return;
  const viewerId = next.viewerId;
  // Outcome chips for results that happen away from the board (or that matter to you).
  const showingBoard = boardShowing();
  for (const cue of newCues(prev, next, viewerId)) {
    const mine = Boolean(viewerId) && (cue.playerId === viewerId || cue.otherId === viewerId);
    const always = ["bankrupt", "win"].includes(cue.kind);
    if (!cue.important || (!always && !mine)) continue;
    if (showingBoard && !always && !["auction", "trade", "loan"].includes(cue.kind)) continue;
    if (cue.kind === "trade" && cue.result === "accepted" && cue.otherId === viewerId) buzz(40);
    showCue(cueHtml(cue, next));
  }
  // Offers to you, deals you are in, loans, an auction you won (whether or not a chip shows).
  for (const cue of newCues(prev, next, viewerId)) {
    const hit = sfxForCue(cue, viewerId);
    if (hit) sfx(hit.name, hit.opts, hit.gain);
  }
  if (turnStarted) {
    if (showingBoard) yourTurnCue(pboard);
    sfx("yourTurn");
  }
  // Sounds and effects for things the board playback doesn't show.
  for (const event of diffEvents(prev, next)) {
    if (event.type === "dice" && event.order && event.playerId === viewerId) sfx("dice");
    else if (event.type === "build" && event.playerId === viewerId && !showingBoard) {
      const hit = sfxForEvent(event, viewerId);
      if (hit) sfx(hit.name, hit.opts, hit.gain);
    } else if (event.type === "win") {
      const hit = sfxForEvent(event, viewerId);
      if (hit) sfx(hit.name, hit.opts, hit.gain);
      motion.confetti(el.fx, { count: 40, height: window.innerHeight });
    }
  }
}

// ---------------------------------------------------------------------------------------------
// Actions

function demoLog(what, detail = {}) {
  console.info(`[demo] would send ${what}`, JSON.stringify(detail));
  toast(`Demo · would send: ${what}${detail.type ? ` ${detail.type.replaceAll("_", " ")}` : ""}`);
}

async function act(action) {
  // Lost the table (review F10): the buttons are dimmed; a tap that still gets through says why.
  if (!ui.demo && ui.live?.status === "offline") {
    toast("Not connected to the table. Your game is safe: try again when it's back.", { error: true });
    return false;
  }
  sfx("tap", {}, 0.5);
  if (ui.demo) {
    demoLog("POST /action", action);
    return true;
  }
  ui.acting = true;
  setBusy();
  try {
    const state = await ui.client.act(ui.session.code, ui.session.token, action);
    const hit = sfxForAction(action);
    if (hit) sfx(hit.name, hit.opts, hit.gain);
    ui.live?.accept(state);
    return true;
  } catch (error) {
    if (sessionGone(error)) endSession(error);
    else toast(friendlyError(error.message), { error: true });
    // The server didn't answer: show RECONNECTING within a second or two.
    if (unreachable(error)) ui.live?.actionFailed();
    return false;
  } finally {
    ui.acting = false;
    setBusy();
  }
}

// Quiz taps (answers, picks, bets) go over the room socket when it is open, so no new HTTP request
// stands between a tap and the server (docs/QUIZ-API.md §1.1); POST otherwise. Never toasts:
// returns {ok, error} for quiz-app.js to decide (an "already answered" is not worth saying).
async function sendQuick(action) {
  if (ui.demo) {
    demoLog("socket action", action);
    return { ok: true };
  }
  if (ui.live?.status === "offline") return { ok: false, error: "Not connected to the table. Your game is safe: try again when it's back." };
  try {
    const sent = ui.live?.send?.(action);
    if (sent) await sent;
    else ui.live?.accept(await ui.client.act(ui.session.code, ui.session.token, action));
    return { ok: true };
  } catch (error) {
    if (sessionGone(error)) {
      endSession(error);
      return { ok: false, error: "" };
    }
    if (unreachable(error)) ui.live?.actionFailed();
    return { ok: false, error: friendlyError(error.message) };
  }
}

// The seat moved to another phone (or the table closed): this session is over.
function sessionGone(error) {
  return /session is invalid|moved to another device|does not exist|removed you/i.test(String(error?.message || ""));
}

// ---- Host controls (UX-02): COMPUTER PLAYS · SKIP TURN · REMOVE from MORE → PLAYERS ----------
const HOST_WORDS = {
  standin: (name) => `let the computer play for ${name}`,
  skip: (name) => `skip ${possessive(name)} turn`,
  remove: (name) => `remove ${name}`,
};

async function hostSend(kind, playerId) {
  const player = playerById(ui.state, playerId);
  if (!player || !HOST_WORDS[kind]) return;
  sfx("tap", {}, 0.5);
  if (ui.demo) {
    toast(`Demo · would ${HOST_WORDS[kind](player.name)}`);
    return;
  }
  try {
    const state = await ui.client.hostControl(ui.session.code, ui.session.token, kind, playerId);
    if (kind === "remove" && ui.hostOpen === playerId) ui.hostOpen = null;
    if (state?.version !== undefined) ui.live?.accept(state);
    else render();
  } catch (error) {
    if (sessionGone(error)) endSession(error);
    else toast(friendlyError(error.message), { error: true });
    if (unreachable(error)) ui.live?.actionFailed();
  }
}

// Clocks that run between states (UX-02, UX-03, UX-07): the timed auction's clock, IDLE m:ss and
// the stand-in's time, every 250 ms; a re-render when the idle bar should come or go.
function tickClocks() {
  const state = ui.state;
  if (!state || !ui.session || isPoker(state) || isLudo(state) || isQuiz(state) || isClosest(state)) {
    ui.idleKey = "";
    return;
  }
  const now = serverNow();
  if (now === null) return;
  if (state.phase === "auction" && isTimed(state.auction)) tickAuctionClock(el.app, state.auction, now);
  for (const node of el.app.querySelectorAll("[data-tick-since]")) {
    const since = Number(node.dataset.tickSince);
    if (Number.isFinite(since) && since > 0) node.textContent = `${node.dataset.tickPrefix || ""}${minutesText(now - since)}`;
  }
  const key = ui.playing ? ui.idleKey : idleTarget(state, { serverNow: now })?.player.id || "";
  if (ui.idleKey !== undefined && key !== ui.idleKey) {
    ui.idleKey = key;
    render();
  }
  ui.idleKey = key;
}

// Engine errors are written for developers; say what they mean for the player.
function friendlyError(message) {
  const text = String(message || "");
  if (/not available during the current phase/i.test(text)) return "Hang on: the board is still moving.";
  if (/not your turn/i.test(text)) return "It's not your turn for that.";
  return text;
}

async function call(label, fn) {
  if (ui.demo) {
    demoLog(label);
    return null;
  }
  try {
    const state = await fn();
    if (state?.version !== undefined) ui.live?.accept(state);
    return state;
  } catch (error) {
    toast(error.message, { error: true });
    return null;
  }
}

function saveProfile() {
  storage.set(PROFILE_KEY, { name: ui.join.name, tokenKind: ui.join.tokenKind });
}

async function joinTable() {
  const code = normaliseCode(ui.join.code);
  const name = ui.join.name.trim().slice(0, 12);
  if (code.length !== 5) return failJoin("Enter the 5-character table code from the PC.");
  if (!name) return failJoin("Enter your name.");
  if (!ui.join.tokenKind) return failJoin("Pick a token.");
  saveProfile();
  if (ui.demo) {
    demoLog("POST /join", { code, name, tokenKind: ui.join.tokenKind });
    return undefined;
  }
  ui.join.busy = true;
  ui.join.error = "";
  render();
  try {
    await ui.client.ensureDevice({ name, tokenKind: ui.join.tokenKind });
    const result = await ui.client.join(code, { name, tokenKind: ui.join.tokenKind });
    ui.client.updateDevice({ name, tokenKind: ui.join.tokenKind });
    startSession({ code: result.code, token: result.token, playerId: result.playerId, role: "player" }, result.state);
  } catch (error) {
    failJoin(error.message);
  } finally {
    ui.join.busy = false;
  }
  return undefined;
}

function failJoin(message) {
  ui.join.error = message;
  ui.join.busy = false;
  render();
}

function rememberTable(code) {
  ui.lastTable = { code, at: Date.now() };
  storage.set(LAST_TABLE_KEY, ui.lastTable);
}

function forgetTable(code) {
  if (ui.lastTable?.code !== code) return;
  ui.lastTable = null;
  storage.set(LAST_TABLE_KEY, null);
}

// A clean join view for `code` (kept when a session ends, so getting back is one tap).
function resetJoin(code, extra = {}) {
  ui.join = { ...ui.join, code, lockedCode: Boolean(code), summary: null, error: "", busy: false, mode: null, note: "", reclaim: null, loading: Boolean(code), missing: false, ...extra };
}

// Take an offline seat over from this browser (docs/API.md "Getting back in"). The old phone's
// session ends; if it is still connected the server says 409 and we offer a retry.
async function reclaimSeat(playerId) {
  const code = normaliseCode(ui.join.code);
  if (ui.demo) return demoLog("POST /reclaim", { code, playerId });
  if (!playerId || ui.join.busy) return undefined;
  ui.join.busy = true;
  ui.join.error = "";
  ui.join.reclaim = { playerId };
  render();
  try {
    await ui.client.ensureDevice({});
    let result;
    try {
      result = await ui.client.reclaim(code, playerId);
    } catch (error) {
      // This browser already holds a live seat here: that one is yours, go back to it.
      if (error.status === 400 && /already has a seat/i.test(error.message)) result = await ui.client.rejoin(code);
      else throw error;
    }
    // [seat-approval hook] a new device waits for the host's or table's yes (or their code).
    if (result?.pending) result = await awaitApproval(ui.client, code, result, (approvalCode) => ui.client.reclaim(code, playerId, approvalCode));
    if (!result) {
      ui.join.busy = false;
      ui.join.reclaim = null;
      render();
      return undefined;
    }
    ui.join.busy = false;
    startSession({ code: result.code, token: result.token, playerId: result.playerId, role: "player" }, result.state);
  } catch (error) {
    ui.join.busy = false;
    ui.join.reclaim = { playerId, status: error.status, error: error.status === 404 ? "This server can't move seats yet. Update it." : error.message };
    render();
    loadSummary(code, { quiet: true });
  }
  return undefined;
}

// Same-device rejoin: true when this browser already holds a seat at `code`.
async function tryRejoin(code) {
  if (!ui.server || !ui.client.deviceHeader()) return false;
  try {
    const result = await ui.client.rejoin(code);
    startSession({ code: result.code, token: result.token, playerId: result.playerId, role: "player" }, result.state);
    return true;
  } catch {
    return false;
  }
}

async function backToTable(code) {
  if (ui.demo) return demoLog("back to table", { code });
  resetJoin(code);
  history.replaceState(null, "", `/j/${code}`);
  render();
  if (await tryRejoin(code)) return undefined;
  await loadSummary(code);
  return undefined;
}

// The session ended under us: the seat moved to another phone, or the table closed. Go to the
// join screen for the same table (code kept), so taking the seat back is one tap.
async function endSession(error) {
  const code = ui.session?.code || ui.state?.code || "";
  poker.reset();
  ludo.reset();
  quiz.reset();
  closest.reset();
  ui.live?.stop();
  ui.live = null;
  keepAwake(false);
  storage.set(SESSION_KEY, null);
  ui.session = null;
  ui.state = null;
  ui.queue = [];
  ui.decision = null;
  ui.trade = emptyDraft();
  ui.loanDraft = null;
  ui.tab = "turn";
  closeSheet();
  const closed = /does not exist/i.test(String(error?.message || ""));
  resetJoin(closed ? "" : code, { note: closed ? `Table ${code} has closed.` : "Your seat moved to another phone." });
  history.replaceState(null, "", closed || !code ? "/play" : `/j/${code}`);
  if (closed) forgetTable(code);
  render();
  if (closed || !code) return;
  await loadSummary(code);
  if (ui.join.missing) {
    resetJoin("", { note: `Table ${code} has closed.` });
    history.replaceState(null, "", "/play");
    render();
  }
}

function leaveTable() {
  poker.reset();
  ludo.reset();
  quiz.reset();
  closest.reset();
  ui.live?.stop();
  ui.live = null;
  keepAwake(false);
  storage.set(SESSION_KEY, null);
  ui.session = null;
  ui.state = null;
  ui.queue = [];
  ui.decision = null;
  ui.tab = "turn";
  resetJoin("", { loading: false });
  history.replaceState(null, "", "/play");
  render();
}

async function watchTable() {
  const code = normaliseCode(ui.join.code);
  const name = (ui.join.name || "Guest").trim().slice(0, 12) || "Guest";
  if (ui.demo) return demoLog("POST /watch", { code, name });
  try {
    const result = await ui.client.watch(code, name);
    startSession({ code: result.code, token: result.token, spectatorId: result.spectatorId, role: "spectator" }, result.state);
  } catch (error) {
    failJoin(error.message);
  }
  return undefined;
}

async function rejoinTable() {
  const code = normaliseCode(ui.join.code);
  if (ui.demo) return demoLog("POST /rejoin", { code });
  try {
    await ui.client.ensureDevice({});
    const result = await ui.client.rejoin(code);
    startSession({ code: result.code, token: result.token, playerId: result.playerId, role: "player" }, result.state);
  } catch (error) {
    failJoin(error.status === 404 ? "This phone doesn't hold a seat at that table." : error.message);
  }
  return undefined;
}

async function loadSummary(rawCode, { quiet = false } = {}) {
  const code = normaliseCode(rawCode);
  if (ui.demo || code.length !== 5) return;
  if (!quiet) ui.join.loading = true;
  let changed = !quiet;
  try {
    const summary = await ui.client.summary(code);
    if (normaliseCode(ui.join.code) !== code || ui.session) return;
    changed = changed || JSON.stringify(summary) !== JSON.stringify(ui.join.summary);
    ui.join.summary = summary;
    ui.join.missing = false;
    const taken = new Set((summary.players || []).map((player) => player.tokenKind));
    if (!ui.join.tokenKind || taken.has(ui.join.tokenKind)) ui.join.tokenKind = TOKEN_KINDS.find((kind) => !taken.has(kind)) || null;
    // A seat that just went offline: clear the "still connected" note.
    const tried = ui.join.reclaim?.playerId && summary.players?.find((player) => player.id === ui.join.reclaim.playerId);
    if (tried?.reclaimable && ui.join.reclaim.status === 409) ui.join.reclaim = null;
  } catch (error) {
    if (normaliseCode(ui.join.code) !== code || ui.session) return;
    if (quiet && error.status === 0) return;
    ui.join.summary = null;
    // 404 with the server's own message = no such room (the generic one = an old server).
    if (error.status === 404 && error.message !== "Not available on this server") {
      ui.join.missing = true;
      ui.join.error = `No table ${code}. Check the code on the TV.`;
      forgetTable(code);
    }
    changed = true;
  } finally {
    if (!quiet) ui.join.loading = false;
  }
  if (changed) render();
}

// ACTIVE GAMES: every game on this server, refreshed while the home screen is showing.
async function loadGames() {
  if (ui.demo) return;
  try {
    const result = await ui.client.listGames();
    const list = Array.isArray(result?.games) ? result.games : [];
    const changed = JSON.stringify(list) !== JSON.stringify(ui.games.list);
    ui.games = { list, error: "" };
    if (changed && !ui.session) render();
  } catch (error) {
    if (ui.games.list === null || error.status !== 0) {
      ui.games = { list: ui.games.list, error: error.message || "unavailable" };
      if (!ui.session) render();
    }
  }
}

function watchGames() {
  if (ui.gamesPoll || ui.demo) return;
  loadGames();
  ui.gamesPoll = setInterval(() => {
    if (ui.session) {
      clearInterval(ui.gamesPoll);
      ui.gamesPoll = null;
      return;
    }
    if (document.hidden || joinMode(ui.join) !== "home") return;
    loadGames();
  }, GAMES_POLL_MS);
}

// In the lobby: is a PC table showing this game? (If not, the host is told how to put it on one.)
async function loadLobbyTable() {
  const code = ui.state?.code;
  if (ui.demo || !code || ui.state?.status !== "lobby") return;
  try {
    const summary = await ui.client.summary(code);
    const online = Boolean(summary?.tableOnline);
    if (online !== ui.tableOnline) {
      ui.tableOnline = online;
      if (ui.state?.status === "lobby") render();
    }
  } catch {
    // Old servers have no summary: say nothing about a PC.
  }
}

function watchLobby() {
  if (ui.lobbyPoll || ui.demo) return;
  loadLobbyTable();
  ui.lobbyPoll = setInterval(() => {
    if (ui.state?.status !== "lobby") {
      clearInterval(ui.lobbyPoll);
      ui.lobbyPoll = null;
      return;
    }
    if (!document.hidden) loadLobbyTable();
  }, GAMES_POLL_MS);
}

// NEW GAME: this phone hosts a party game (no PC needed); straight into the lobby as host.
async function createGame() {
  const name = ui.join.name.trim().slice(0, 12);
  if (!name) return failJoin("Enter your name.");
  if (!ui.join.tokenKind) return failJoin("Pick a token.");
  saveProfile();
  const gameType = gameTypeOrEstate(ui.join.gameType);
  if (ui.demo) return demoLog("POST /api/games", { name, tokenKind: ui.join.tokenKind, gameType });
  ui.join.busy = true;
  ui.join.error = "";
  render();
  try {
    await ui.client.ensureDevice({ name, tokenKind: ui.join.tokenKind });
    if (!ui.client.deviceHeader()) throw new Error("This server can't host games from a phone yet.");
    // client.js createGame(name, tokenKind, gameType); an older client takes no game type.
    const result = gameType === "estate" || /gameType/.test(String(ui.client.createGame))
      ? await ui.client.createGame(name, ui.join.tokenKind, gameType)
      : await ui.client.request("POST", "/api/games", { name, tokenKind: ui.join.tokenKind, gameType });
    if (gameType !== "estate" && result?.state && result.state.game !== gameType) throw new Error(`This server can't host ${gameType} yet.`);
    ui.client.updateDevice({ name, tokenKind: ui.join.tokenKind });
    startSession({ code: result.code, token: result.token, playerId: result.playerId, role: "player" }, result.state);
    ui.tableOnline = false; // a phone-hosted game has no PC until one resumes it
    render();
  } catch (error) {
    failJoin(error.status === 404 ? "This server can't host games from a phone yet." : error.message);
  }
  return undefined;
}

// The host's lobby picker: the other game means a new table (a new code), since a room keeps its
// game. Alone (or with computers only) it switches at once; with people waiting it asks first.
async function switchGame(gameType) {
  const state = ui.state;
  const want = gameTypeOrEstate(gameType);
  const now = gameTypeOfState(state);
  if (!state || state.status !== "lobby" || want === now) return undefined;
  const mine = state.players?.find((player) => player.id === state.viewerId);
  const people = (state.players || []).filter((player) => player.id !== state.viewerId && player.kind !== "ai" && player.input !== "ai");
  const go = async () => {
    ui.join = { ...ui.join, name: mine?.name || ui.join.name, tokenKind: mine?.tokenKind || ui.join.tokenKind, gameType: want };
    await createGame();
  };
  if (!people.length) return go();
  openConfirm({
    title: `SWITCH TO ${gameLabel(want)}?`,
    text: "That starts a new table with a new code. Everyone here joins again with it.",
    confirm: `NEW ${gameLabel(want)} TABLE`,
    run: go,
  });
  return undefined;
}

// A row in ACTIVE GAMES: REJOIN your seat, JOIN (name + token, or WHO ARE YOU?), or WATCH.
async function openGame(code) {
  const game = ui.games.list?.find((candidate) => candidate.code === code);
  if (!game) return undefined;
  const action = gameAction(game);
  if (ui.demo) return demoLog(`${action.label} ${code}`);
  if (action.kind === "watch") {
    resetJoin(code, { loading: false, summary: game });
    return watchTable();
  }
  if (action.kind === "rejoin" && await tryRejoin(code)) return undefined;
  resetJoin(code, { loading: false, summary: game });
  const taken = new Set((game.players || []).map((player) => player.tokenKind));
  if (!ui.join.tokenKind || taken.has(ui.join.tokenKind)) ui.join.tokenKind = TOKEN_KINDS.find((kind) => !taken.has(kind)) || null;
  history.replaceState(null, "", `/j/${code}`);
  render();
  loadSummary(code, { quiet: true });
  return undefined;
}

// While "WHO ARE YOU?" is showing, refresh the seats so one that goes offline becomes tappable.
function watchSummary() {
  if (ui.summaryPoll || ui.demo) return;
  ui.summaryPoll = setInterval(() => {
    if (ui.session) {
      clearInterval(ui.summaryPoll);
      ui.summaryPoll = null;
      return;
    }
    if (document.hidden || ui.join.busy || joinMode(ui.join) !== "who") return;
    loadSummary(ui.join.code, { quiet: true });
  }, SUMMARY_POLL_MS);
}

// A fresh loan draft: repay follows the rate chip, so lowering the amount lowers the repayment.
function newLoan(direction, targetId) {
  return { direction, targetId, principal: 200, rate: 0.2, dueTurns: 6, collateral: [], locked: false, security: false };
}

// Leaving the trade tab (or switching tabs at all) drops the draft and any loan draft.
function leaveTradeTab(nextTab) {
  if (nextTab !== "trade" || ui.loanDraft) {
    ui.trade = emptyDraft();
    ui.loanDraft = null;
  }
}

function defaultPartner() {
  return ui.state?.players.find((player) => player.id !== ui.state.viewerId && !player.bankrupt)?.id || null;
}

async function sendTrade(draft) {
  const clean = cleanDraft(ui.state, draft, ui.state.viewerId).draft;
  if (!tradeValid(clean)) {
    toast("That offer is out of date: check the deeds again", { error: true });
    ui.trade = { ...clean, planned: false };
    ui.tab = "trade";
    render();
    return;
  }
  // No "sent" toast: a person's answer is awaited on the OFFER SENT card, and a computer's answer
  // arrives at once as a cue chip.
  const ok = await act(tradeActionOf(clean));
  if (ok) {
    ui.trade = emptyDraft();
    if (draft.planned) ui.plan = null;
    ui.tab = "turn";
    render();
  }
}

// A computer partner's price for the deeds in the deal, asked in the background so the trade
// footer can show "ASK $82". It never changes the deal unless the chip is tapped.
function maybeQuote() {
  const state = ui.state;
  if (!state?.viewerId || !ui.session) return;
  const { draft } = cleanDraft(state, ui.trade, state.viewerId);
  if (!wantsQuote(state, draft)) return;
  const key = quoteKey(draft);
  if (ui.trade.quote?.key === key) return;
  ui.trade = { ...ui.trade, quote: { key, loading: true } };
  clearTimeout(ui.quoteTimer);
  ui.quoteTimer = setTimeout(() => fetchQuote(draft, key), ui.demo ? 0 : 400);
}

async function fetchQuote(draft, key) {
  let quote;
  if (ui.demo) quote = { key, offerCash: 80, requestCash: 0 };
  else {
    try {
      const result = await ui.client.tradeQuote(ui.session.code, ui.session.token, { targetId: draft.targetId, offerProperties: draft.offer, requestProperties: draft.request });
      quote = { key, offerCash: result?.offerCash || 0, requestCash: result?.requestCash || 0 };
    } catch (error) {
      quote = { key, error: error.message };
    }
  }
  if (ui.trade.quote?.key !== key) return;
  ui.trade = { ...ui.trade, quote };
  if (el.app.dataset.screen === "trade") render();
}

async function command(cmd, arg) {
  const state = ui.state;
  if (String(cmd).startsWith("pk-") && cmd !== "pk-switch-game") return poker.command(cmd, arg);
  if (String(cmd).startsWith("ld-") && cmd !== "ld-switch-game") return ludo.command(cmd, arg);
  if (String(cmd).startsWith("qz-") && cmd !== "qz-switch-game") return quiz.command(cmd, arg);
  if (String(cmd).startsWith("cg-") && cmd !== "cg-switch-game") return closest.command(cmd, arg);
  switch (cmd) {
    case "pick-game":
      ui.join.gameType = gameTypeOrEstate(arg);
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    case "pk-switch-game":
    case "ld-switch-game":
    case "qz-switch-game":
    case "cg-switch-game":
    case "switch-game": return switchGame(arg);
    case "join": return joinTable();
    case "watch": return watchTable();
    case "rejoin": return rejoinTable();
    case "reclaim": return reclaimSeat(arg);
    case "new-game": {
      ui.join = { ...ui.join, mode: "create", code: "", summary: null, error: "", note: "", lockedCode: false, missing: false };
      if (!ui.join.tokenKind) ui.join.tokenKind = TOKEN_KINDS[0];
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    }
    case "create-game": return createGame();
    case "game-open": return openGame(arg);
    case "home":
      resetJoin("", { loading: false });
      history.replaceState(null, "", "/play");
      render();
      return undefined;
    case "back-to-table": return backToTable(arg);
    case "join-new":
    case "join-who":
      ui.join.mode = cmd === "join-new" ? "new" : null;
      ui.join.error = "";
      ui.join.reclaim = null;
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    case "invite-accept":
      ui.invitedView = false;
      ui.remote = true;
      render();
      return undefined;
    case "tab":
      ui.tab = arg;
      ui.inviteOpen = false;
      leaveTradeTab(arg);
      render();
      return undefined;
    case "add-ai": return call("POST /ai", () => ui.client.addAi(ui.session.code, ui.session.token, ui.aiLevel));
    case "ai-level":
      ui.aiLevel = nextAiLevel(ui.aiLevel);
      storage.set(AI_LEVEL_KEY, ui.aiLevel);
      render();
      return undefined;
    case "remove": return call("POST /remove", () => ui.client.removePlayer(ui.session.code, ui.session.token, arg));
    case "dice-mode": return act({ type: "set_dice_mode", mode: state.settings?.diceMode === "choice" ? "random" : "choice" });
    case "rule": {
      const rules = rulesOf(state);
      if (arg === "startingCash") {
        const options = [1500, 2000, 2500, 1000];
        rules.startingCash = options[(options.indexOf(rules.startingCash) + 1) % options.length];
      } else rules[arg] = !rules[arg];
      return act({ type: "set_rules", rules });
    }
    // ---- Host controls (UX-02) ----
    case "host-open":
      ui.hostOpen = ui.hostOpen === arg ? null : arg;
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    case "host-players":
      ui.hostOpen = arg || null;
      ui.tab = "friends";
      ui.inviteOpen = false;
      leaveTradeTab("friends");
      render();
      return undefined;
    case "host-standin": return hostSend("standin", arg);
    case "host-skip": return hostSend("skip", arg);
    case "host-remove-ask":
      openSheet({ kind: "remove", playerId: arg, run: () => hostSend("remove", arg) });
      return undefined;
    // ---- Debt checklist (UX-04): tick rows, then PAY runs the ticked sells and mortgages ----
    case "debt-tick": {
      if (state?.phase !== "debt") return undefined;
      const list = debtListFor(state, view());
      ui.debtPicks = { key: debtKey(state), picked: toggleDebtRow(list, arg) };
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    }
    case "debt-pay": {
      if (state?.phase !== "debt") return undefined;
      for (const action of debtListFor(state, view()).actions) {
        const ok = await act(action);
        if (!ok || ui.demo) break;
      }
      return undefined;
    }
    case "apply-plan": {
      const player = me(state);
      const plan = debtPlan(state, player.id, Math.max(0, state.debt.amount - player.cash));
      for (const action of plan.actions) {
        const ok = await act(action);
        if (!ok || ui.demo) break;
      }
      return undefined;
    }
    case "bankrupt": {
      // At any time in play (0.10.1). Owing another player in the debt phase, they get it all.
      const owes = ["debt", "loan_offer"].includes(state?.phase) && state.debt?.payerId === state.viewerId;
      const creditor = owes ? playerById(state, state.debt?.creditorId) : null;
      openConfirm({
        title: "DECLARE BANKRUPTCY?",
        text: `You'll be out of the game, and your property goes to ${creditor && !creditor.bankrupt ? creditor.name : "the bank"}. This can't be undone.`,
        confirm: "DECLARE BANKRUPTCY",
        run: () => act({ type: "bankrupt" }),
      });
      return undefined;
    }
    // ---- Trade (one page) ----
    case "trade-partner":
      // A new partner starts a new deal; tapping the same one keeps it.
      if (ui.trade.targetId !== arg) ui.trade = { ...emptyDraft(), targetId: arg };
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    case "trade-close":
      ui.trade = emptyDraft();
      ui.loanDraft = null;
      ui.tab = "turn";
      render();
      return undefined;
    case "trade-mode": {
      // TRADE | LOAN: same partner, nothing else carried over.
      const targetId = ui.loanDraft?.targetId || ui.trade.targetId || null;
      ui.trade = { ...emptyDraft(), targetId: arg === "trade" ? targetId : null };
      ui.loanDraft = arg === "loan" ? newLoan("lend", targetId) : null;
      ui.tab = "trade";
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    }
    case "trade-cash": {
      const [field, delta] = arg.split(":");
      const key = field === "offer" ? "offerCash" : "requestCash";
      const max = field === "offer" ? me(state)?.cash ?? 0 : playerById(state, ui.trade.targetId)?.cash ?? 0;
      ui.trade = { ...ui.trade, [key]: Math.max(0, Math.min(max, (ui.trade[key] || 0) + Number(delta))) };
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    }
    case "trade-send": return sendTrade(ui.trade);
    case "trade-send-plan": {
      const plan = plannedTrade(state, ui.plan);
      if (!plan) {
        toast("Your planned trade is out of date", { error: true });
        ui.plan = null;
        render();
        return undefined;
      }
      return sendTrade({ ...plan.draft, planned: true });
    }
    case "trade-plan":
      ui.plan = { ...cleanDraft(state, ui.trade, state.viewerId).draft, quote: null, planned: true };
      ui.trade = emptyDraft();
      toast("Saved. Send it on your turn.");
      ui.tab = "turn";
      render();
      return undefined;
    case "trade-plan-discard":
      ui.plan = null;
      render();
      return undefined;
    case "trade-info":
      // An incoming offer opens WHAT THIS DEAL DOES by default (review U8), so flip what is shown.
      ui.tradeInfo = !el.main.querySelector(".tinfo.is-open");
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    case "trade-clear":
      ui.trade = { ...emptyDraft(), targetId: ui.trade.targetId };
      render();
      return undefined;
    case "trade-quote-use": {
      // The computer's price goes in: your cash and its cash, as "offerCash:requestCash".
      const [offerCash, requestCash] = arg.split(":").map(Number);
      ui.trade = { ...ui.trade, offerCash: offerCash || 0, requestCash: requestCash || 0 };
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    }
    case "trade-counter-change": {
      // Decline the computer's counter and open its terms in the trade page to edit and send.
      const trade = state?.trade;
      if (!trade?.counterTo || trade.targetId !== state.viewerId) return undefined;
      const draft = { ...emptyDraft(), targetId: trade.proposerId,
        offer: [...(trade.request?.properties || [])], offerCash: trade.request?.cash || 0,
        request: [...(trade.offer?.properties || [])], requestCash: trade.offer?.cash || 0 };
      if (!(await act({ type: "reject_trade" }))) return undefined;
      ui.trade = draft;
      maybeQuote();
      render();
      return undefined;
    }
    case "trade-not-selling": {
      // No computer asks for this deed again until its deed card turns it back on; the offer is
      // declined.
      const index = Number(arg);
      await call("POST /not-selling", () => ui.client.notSelling(ui.session.code, ui.session.token, index, true));
      return act({ type: "reject_trade" });
    }
    case "deed-not-selling": {
      const [index, on] = String(arg).split(":");
      await call("POST /not-selling", () => ui.client.notSelling(ui.session.code, ui.session.token, Number(index), on === "true"));
      render();
      return undefined;
    }
    case "deed-trade":
    case "deed-ask": {
      // From a deed's sheet: the trade page with this deed on your side (TRADE THIS…), or asked of
      // its owner (ASK TO BUY…, so the computer's price chip shows). Off-turn it saves for your turn.
      const index = Number(arg);
      const others = state.players.filter((player) => player.id !== state.viewerId && !player.bankrupt);
      ui.trade = cmd === "deed-ask"
        ? { ...emptyDraft(), targetId: propState(state, index)?.ownerId || null, request: [index] }
        : { ...emptyDraft(), targetId: others.length === 1 ? others[0].id : null, offer: [index] };
      ui.loanDraft = null;
      ui.tab = "trade";
      closeSheet();
      sfx("tap", {}, 0.5);
      maybeQuote();
      render();
      return undefined;
    }
    // ---- Loans ----
    case "loan-ask": {
      const player = me(state);
      const short = Math.max(0, state.debt.amount - player.cash);
      const lender = [...state.players].filter((candidate) => candidate.id !== player.id && !candidate.bankrupt).sort((a, b) => b.cash - a.cash)[0];
      ui.loanDraft = { ...newLoan("borrow", lender?.id || null), principal: suggestLoan(short).principal, locked: true };
      render();
      return undefined;
    }
    case "loan-new":
      ui.loanDraft = newLoan("lend", arg || ui.trade.targetId || null);
      ui.trade = emptyDraft();
      ui.tab = "trade";
      render();
      return undefined;
    case "loan-amount": {
      const draft = ui.loanDraft;
      if (!draft) return undefined;
      const lender = draft.direction === "borrow" ? playerById(state, draft.targetId) : me(state);
      const max = Math.max(10, lender?.cash ?? 10);
      draft.principal = Math.max(10, Math.min(max, draft.principal + Number(String(arg).split(":")[1])));
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    }
    case "loan-rate":
    case "loan-due":
      if (!ui.loanDraft) return undefined;
      if (cmd === "loan-rate") ui.loanDraft.rate = Number(arg);
      else ui.loanDraft.dueTurns = Number(arg);
      sfx("tap", {}, 0.5);
      render();
      return undefined;
    case "loan-security":
      if (ui.loanDraft) ui.loanDraft.security = !ui.loanDraft.security;
      render();
      return undefined;
    case "loan-cancel":
      ui.loanDraft = null;
      render();
      return undefined;
    case "loan-send": {
      const draft = ui.loanDraft;
      const ok = await act(loanActionOf(draft));
      if (ok && !ui.demo) {
        ui.loanDraft = null;
        render();
      }
      return undefined;
    }
    case "leave-ask":
      openConfirm(state?.status === "lobby" ? {
        title: "LEAVE THE LOBBY?",
        text: "Your seat stays until the host removes it. Come back any time with BACK TO TABLE.",
        confirm: "LEAVE",
        run: () => command("leave"),
      } : {
        title: "LEAVE THE TABLE?",
        text: "Your seat stays yours: come back with BACK TO TABLE on this phone, or tap your name from any phone.",
        confirm: "LEAVE TABLE",
        run: () => command("leave"),
      });
      return undefined;
    case "end-game-ask": {
      if (isClosest(state)) {
        const top = closest.leader();
        const who = top ? (top.id === state.viewerId ? "you" : top.name) : "";
        openConfirm({
          title: "END THE GAME FOR EVERYONE?",
          text: `${CLOSEST_NAME} stops now and places go by the scores as they stand${top ? `: right now ${who} lead${top.id === state.viewerId ? "" : "s"} with ${Number(top.score).toLocaleString("en-GB")}` : ""}.`,
          confirm: "END GAME",
          run: () => command("end-game"),
        });
        return undefined;
      }
      if (isQuiz(state)) {
        const top = quiz.leader();
        const who = top ? (top.id === state.viewerId ? "you" : top.name) : "";
        openConfirm({
          title: "END THE GAME FOR EVERYONE?",
          text: `The quiz stops now and places go by the scores as they stand${top ? `: right now ${who} lead${top.id === state.viewerId ? "" : "s"} with ${Number(top.score).toLocaleString("en-GB")}` : ""}.`,
          confirm: "END GAME",
          run: () => command("end-game"),
        });
        return undefined;
      }
      if (isLudo(state)) {
        const top = ludo.leader();
        const who = top ? (top.id === state.viewerId ? "you" : top.name) : "";
        openConfirm({
          title: "END THE GAME FOR EVERYONE?",
          text: `The game stops now. Players with every piece home keep their places; the rest are ranked by how far their pieces got${top ? `: right now ${who} lead${top.id === state.viewerId ? "" : "s"}, ${top.progress}% of the way` : ""}.`,
          confirm: "END GAME",
          run: () => command("end-game"),
        });
        return undefined;
      }
      if (isPoker(state)) {
        const top = poker.leader();
        const who = top ? (top.id === state.viewerId ? "you" : top.name) : "";
        openConfirm({
          title: "END THE GAME FOR EVERYONE?",
          text: `The game stops now and the biggest stack wins${top ? `: right now that's ${who}, with ${Number(top.stack + (top.bet || 0)).toLocaleString("en-US")}` : ""}. A hand in play is called off and its chips go back.`,
          confirm: "END GAME",
          run: () => command("end-game"),
        });
        return undefined;
      }
      const leader = leaderNow(state, netWorth, finalScore);
      const name = leader ? (leader.player.id === state.viewerId ? "you" : leader.player.name) : "";
      openConfirm({
        title: "END THE GAME FOR EVERYONE?",
        text: `The game stops now and the richest player wins${leader ? `: right now that's ${name}, worth ${money(leader.worth)}` : ""}.`,
        confirm: "END GAME",
        run: () => command("end-game"),
      });
      return undefined;
    }
    case "end-game":
      if (typeof ui.client.endGame !== "function") return toast("This server can't end games yet.", { error: true });
      return call("POST /end", () => ui.client.endGame(ui.session.code, ui.session.token));
    case "settings":
    case "audio-sheet":
      openSheet({ kind: "settings" });
      return undefined;
    case "install-app":
      if (installState() !== "prompt") {
        openSheet({ kind: "install", back: ui.sheet?.kind === "settings" ? "settings" : null });
        return undefined;
      }
      promptInstall().then((added) => {
        if (added) toast("Estate is on your home screen.");
        render();
      });
      return undefined;
    case "install-hide":
      storage.set(INSTALL_HIDDEN_KEY, true);
      toast("Hidden. SETTINGS still has HOME SCREEN.");
      render();
      return undefined;
    case "how-to-play":
      storage.set(HOW_SEEN_KEY, true);
      openSheet({ kind: "help" });
      return undefined;
    case "pref": {
      const [key, raw] = String(arg).split(":");
      const value = raw === "true" ? true : raw === "false" ? false : raw;
      ui.prefs = savePrefs(storage, { ...ui.prefs, [key]: value });
      applyMotion();
      sfx("tap", {}, 0.6);
      render();
      return undefined;
    }
    case "room-listed": {
      const listed = String(arg).split(":")[1] === "true";
      if (ui.demo) return demoLog("POST /settings", { listed });
      if (typeof ui.client.roomSettings !== "function") return toast("This server can't change that yet.", { error: true });
      return call("POST /settings", () => ui.client.roomSettings(ui.session.code, ui.session.token, { listed }));
    }
    case "computer-speed": {
      const speed = String(arg).split(":")[1];
      if (ui.demo) return demoLog("POST /settings", { computerSpeed: speed });
      if (typeof ui.client.roomSettings !== "function") return toast("This server can't change that yet.", { error: true });
      return call("POST /settings", () => ui.client.roomSettings(ui.session.code, ui.session.token, { computerSpeed: speed }));
    }
    case "play-again":
    case "leave":
      if (ui.demo) return demoLog("leave the table");
      leaveTable();
      return undefined;
    case "rematch":
      // A tap left over from the last turn (the result screen only just came up) does nothing.
      if (!rematchReady(ui.resultShownAt, Date.now())) return undefined;
      return call("POST /rematch", () => ui.client.rematch(ui.session.code, ui.session.token));
    case "open-invite":
      ui.inviteOpen = true;
      loadQr();
      if (ui.friends.list === null) loadFriends();
      render();
      return undefined;
    case "close-invite":
      ui.inviteOpen = false;
      render();
      return undefined;
    case "share": return share(arg);
    case "whatsapp": return whatsapp();
    case "copy": return copyLink();
    case "friend-invite": {
      if (ui.demo) {
        ui.friends.sent[arg] = true;
        demoLog("POST /api/invites", { toDeviceId: arg, code: state?.code });
        render();
        return undefined;
      }
      try {
        await ui.client.invite(arg, ui.session.code);
        ui.friends.sent[arg] = true;
        const friend = ui.friends.list?.find((candidate) => candidate.deviceId === arg);
        toast(`Invite sent to ${friend?.name || "your friend"}`);
      } catch (error) {
        toast(error.message, { error: true });
      }
      render();
      return undefined;
    }
    case "friend-watch": {
      const friend = ui.friends.list?.find((candidate) => candidate.deviceId === arg);
      if (friend?.code) location.href = `/j/${encodeURIComponent(friend.code)}?watch=1`;
      return undefined;
    }
    case "audio":
      if (arg === "muted") {
        sound.toggleMuted();
        ui.muted = sound.isMuted();
      } else if (arg === "music" || arg === "effects") setAudio({ [arg]: !ui.audio[arg] });
      applyAudio();
      if (arg !== "muted" || !ui.muted) sfx("tap", {}, 0.6);
      render();
      return undefined;
    case "audio-sheet":
      openSheet({ kind: "audio" });
      return undefined;
    case "mute":
      sound.toggleMuted();
      ui.muted = sound.isMuted();
      applyAudio();
      render();
      return undefined;
    default:
      return undefined;
  }
}

// WHATSAPP on the invite screens (0.11.1): WhatsApp opens with the join link ready to send. Works on
// the same Wi-Fi too, where the phone's share sheet (navigator.share) needs HTTPS and is missing.
function whatsapp() {
  const code = ui.state?.code || ui.session?.code || "";
  if (ui.demo) { toast("Demo · would open WhatsApp with the join link"); return undefined; }
  openWhatsApp(inviteText(gameTypeOfState(ui.state), code, inviteLink()));
  return undefined;
}

async function share() {
  const url = inviteLink();
  const data = { title: "Estate", text: inviteLine(gameTypeOfState(ui.state), ui.state?.code || ui.session?.code || ""), url };
  if (navigator.share) {
    try {
      await navigator.share(data);
      return;
    } catch (error) {
      if (error?.name === "AbortError") return;
    }
  }
  await copyLink();
}

// The clipboard API needs HTTPS; on the same Wi-Fi (plain http://192.168…) it is missing, so copy
// through a hidden text field instead (the page itself is not selectable).
function copyByField(text) {
  const field = document.createElement("textarea");
  field.value = text;
  field.setAttribute("readonly", "");
  field.style.cssText = "position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;";
  document.body.append(field);
  field.select();
  field.setSelectionRange(0, text.length);
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  field.remove();
  return ok;
}

async function copyLink() {
  const url = inviteLink();
  let ok = false;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(url);
      ok = true;
    }
  } catch {
    ok = false;
  }
  if (ok || copyByField(url)) {
    ui.copied = true;
    toast("Link copied");
    render();
    setTimeout(() => { ui.copied = false; render(); }, 2000);
  } else {
    const box = el.main.querySelector(".link-box span");
    if (box) {
      const range = document.createRange();
      range.selectNodeContents(box);
      const selection = window.getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
    }
    toast("Press and hold the link to copy it");
  }
}

function loadQr() {
  const code = ui.state?.code;
  if (ui.demo || !code || !ui.server || ui.qrUrl) return;
  const url = ui.client.qrUrl(code);
  const probe = new Image();
  probe.onload = () => {
    ui.qrUrl = url;
    render();
  };
  probe.src = url;
}

async function loadFriends() {
  if (ui.demo) {
    try {
      const fixture = await loadFixture("friends");
      ui.friends.list = fixture.friends;
    } catch {
      ui.friends.list = [];
    }
    render();
    return;
  }
  if (!ui.server || !ui.client.deviceHeader()) {
    ui.friends.list = [];
    ui.friends.error = "Friends show up here after you play with someone on this device.";
    render();
    return;
  }
  try {
    ui.friends.list = await ui.client.friends();
  } catch (error) {
    ui.friends.list = [];
    ui.friends.error = error.status === 404 ? "Friends need the party server." : error.message;
  }
  render();
}

// The token picker shows the shared player palette, so the colour you pick is the colour you play.
async function loadPalette() {
  try {
    const response = await fetch("/shared/palette.json", { cache: "no-store" });
    if (!response.ok) return;
    const file = await response.json();
    for (const [kind, color] of Object.entries(file?.tokens || {})) {
      if (TOKEN_KINDS.includes(kind) && /^#[0-9a-f]{6}$/i.test(color)) ui.palette[kind] = color;
    }
    if (!ui.session) render();
  } catch {
    // Keep the built-in palette (tokens.js TOKEN_COLORS is its twin).
  }
}

// ---------------------------------------------------------------------------------------------
// Sheets (any square on the board, your deeds, confirmations)

function openSheet(sheet) {
  if (!ui.state && sheet.kind !== "confirm" && sheet.kind !== "settings" && sheet.kind !== "install") return;
  ui.sheet = sheet;
  renderSheet();
  if (!el.sheet.open) el.sheet.showModal?.();
  // Confirmations focus the safe choice (KEEP PLAYING), never the destructive one.
  // (Poker sheets can be long: focus without scrolling them to the bottom.)
  (el.sheet.querySelector('[data-sheet-cmd="close"]') && (sheet.kind === "confirm" || sheet.kind === "remove") ? el.sheet.querySelector('[data-sheet-cmd="close"]') : el.sheet.querySelector("button"))?.focus(String(sheet.kind).startsWith("pk-") ? { preventScroll: true } : undefined);
  if (String(sheet.kind).startsWith("pk-")) el.sheet.scrollTop = 0;
}

function closeSheet() {
  ui.sheet = null;
  if (el.sheet.open) el.sheet.close();
}

function openConfirm({ title, text, confirm, run }) {
  openSheet({ kind: "confirm", title, text, confirm, run });
}

function sheetButton(label, attrs, kind = "ghost", disabled = false) {
  return `<button type="button" class="btn btn--${kind}" ${attrs}${disabled ? " disabled" : ""}><span class="btn__label">${escapeHtml(label)}</span></button>`;
}

const SQUARE_NOTES = {
  go: ["PAYDAY", "Collect $200 every time you pass."],
  jail: ["JAIL", "Just passing, unless you were sent here."],
  parking: ["CITY PARK", "Free rest. Nothing happens here."],
  goToJail: ["BUSTED", "Straight to jail. No $200."],
  chance: ["HEADLINES", "Draw a Headlines card."],
  chest: ["POST", "Draw a Post card."],
};

function standingHere(state, index) {
  const here = state.players.filter((player) => !player.bankrupt && player.position === index);
  if (!here.length) return "";
  return `<div class="sheet__here">${here.map((player) => `<span class="sheet__who">${dotHtml(state, player.id, { size: 18 })}${escapeHtml(player.id === state.viewerId ? "You" : player.name)}${player.inJail && index === 10 ? " · in jail" : ""}</span>`).join("")}</div>`;
}

// The full title deed: the shared deedDetailHtml (web/shared/board.js) when it exists; until then
// the rent card, the owner line and who owns each member of the set.
function deedDetail(state, index, owner, prop, space, ownerLine) {
  const shared = typeof deedShared.deedDetailHtml === "function" ? deedShared.deedDetailHtml : boardShared.deedDetailHtml;
  if (typeof shared === "function") {
    try { return `<div class="deed-detail">${shared(state, index, { viewerId: state.viewerId, size: document.documentElement.dataset.layout === "phone" ? "md" : "lg" })}</div>`; } catch { /* fall back */ }
  }
  return `<div class="deed-wrap">${boardShared.deedHtml(state, index, { eyebrow: "", rows: "full", nowLevel: space.type === "property" && owner ? prop.houses : null })}</div>${ownerLine}${setOwnersHtml(state, index, state.viewerId)}`;
}

function renderSheet() {
  const sheet = ui.sheet;
  const state = ui.state;
  if (!sheet) return;
  if (String(sheet.kind).startsWith("pk-")) {
    morph(el.sheet, poker.sheetHtml(sheet));
    return;
  }
  if (String(sheet.kind).startsWith("ld-")) {
    morph(el.sheet, ludo.sheetHtml(sheet));
    return;
  }
  if (String(sheet.kind).startsWith("qz-")) {
    morph(el.sheet, quiz.sheetHtml(sheet));
    return;
  }
  if (String(sheet.kind).startsWith("cg-")) {
    morph(el.sheet, closest.sheetHtml(sheet));
    return;
  }
  if (sheet.kind === "settings") {
    const host = Boolean(state && ui.session && state.hostId && state.hostId === state.viewerId);
    const computerSpeed = state?.roomSettings ? state.roomSettings.computerSpeed || "normal" : null;
    // PUBLIC GAME: the host can list the game for everyone on the server (off by default).
    const listed = state?.roomSettings && "listed" in state.roomSettings ? Boolean(state.roomSettings.listed) : null;
    const pokerRows = isPoker(state) ? pokerSettingsRows(poker.pk.prefs) : "";
    const back = sheet.back ? sheetButton("BACK", `data-sheet-cmd="${sheet.back}"`, "ghost") : "";
    morph(el.sheet, `<div class="sheet__body"><h2 class="sheet__title">SETTINGS</h2>${settingsHtml({ audio: ui.audio, muted: ui.muted, prefs: ui.prefs, host, computerSpeed, listed, poker: pokerRows, camera: !isLudo(state) && !isQuiz(state) && !isClosest(state), app: installState(), publicUrl: ui.server?.publicURL || "" })}<div class="actions">${back}${sheetButton("CLOSE", 'data-sheet-cmd="close"', "plain")}</div></div>`);
    return;
  }
  if (sheet.kind === "install") {
    // ADD TO HOME SCREEN when the browser hasn't offered an install: the steps for this browser.
    const state = installState();
    const text = installSteps(state, { publicUrl: ui.server?.publicURL || "" }) || "This browser can't add Estate to the home screen. Try Chrome on Android or Safari on an iPhone.";
    const back = sheet.back ? sheetButton("BACK", `data-sheet-cmd="${sheet.back}"`, "ghost") : "";
    morph(el.sheet, `<div class="sheet__body"><h2 class="sheet__title">ADD TO HOME SCREEN</h2><p class="sheet__text">${escapeHtml(text)}</p><p class="sheet__text">Estate then opens full screen from its own icon, like an app.</p><div class="actions">${back}${sheetButton("CLOSE", 'data-sheet-cmd="close"', "plain")}</div></div>`);
    return;
  }
  if (sheet.kind === "help") {
    morph(el.sheet, `<div class="sheet__body"><h2 class="sheet__title">HOW TO PLAY</h2>${howToPlayHtml(state)}<div class="actions">${sheetButton(sheet.first ? "LET'S PLAY" : "CLOSE", 'data-sheet-cmd="close"', sheet.first ? "primary" : "plain")}</div></div>`);
    return;
  }
  if (sheet.kind === "remove") {
    // REMOVE SAM? (UX-02): what goes back to the bank; KEEP is the safe choice.
    const html = state ? removeSheetHtml(state, sheet.playerId, view()) : "";
    if (!html) {
      closeSheet();
      return;
    }
    morph(el.sheet, html);
    return;
  }
  if (sheet.kind === "confirm") {
    morph(el.sheet, `<div class="sheet__body"><h2 class="sheet__title">${escapeHtml(sheet.title)}</h2><p class="sheet__text">${escapeHtml(sheet.text)}</p>
      <div class="actions">${sheetButton(sheet.confirm, 'data-sheet-cmd="confirm"', "danger")}${sheetButton("KEEP PLAYING", 'data-sheet-cmd="close"', "plain")}</div></div>`);
    return;
  }
  const index = sheet.index;
  const space = state?.board?.[index];
  if (!space) {
    closeSheet();
    return;
  }
  const prop = propState(state, index);
  const close = sheetButton("CLOSE", 'data-sheet-cmd="close"', "plain");
  if (!prop) {
    const [title, text] = space.type === "tax" ? [upper(space.name), `Pay ${money(space.amount)} to the bank.`] : SQUARE_NOTES[space.type] || [upper(space.name), ""];
    morph(el.sheet, `<div class="sheet__body"><h2 class="sheet__title">${escapeHtml(title)}</h2>${text ? `<p class="sheet__text sheet__text--center">${escapeHtml(text)}</p>` : ""}${standingHere(state, index)}<div class="actions">${close}</div></div>`);
    return;
  }
  const player = me(state);
  const owner = playerById(state, prop.ownerId);
  const mine = Boolean(player && owner && owner.id === player.id);
  // What you can do with it (web/shared/deedactions.js, the engine's rules): yours — build, sell,
  // mortgage or unmortgage, trade it, not selling; someone else's — ask to buy it; unowned — buy it
  // while you're on it. A greyed action says why.
  const buttons = deedActionButtons(state, player?.id, index);
  // The next house's payback only once you could build (the whole set, none mortgaged).
  const payback = mine && space.type === "property" && nextBuild(state, player.id, space.group) ? housePayback(state, index) : null;
  const pledge = loanSecuredBy(state, index);
  const ownerLine = owner
    ? `<div class="sheet__owner">${dotHtml(state, owner.id, { size: 20 })}<span>${mine ? "Yours" : `${escapeHtml(owner.name)}'s`}</span><b>${prop.mortgaged ? "mortgaged · no rent" : `rent ${escapeHtml(rentLabel(state, index))}`}</b></div>`
    : `<div class="sheet__owner sheet__owner--free">${icon("house")}<span>For sale</span><b>${money(space.price)}</b></div>`;
  // Even building and "on your turn" are each greyed button's own reason now (deedactions.js).
  const notes = [
    payback && payback.delta > 0 ? `Next ${payback.hotel ? "hotel" : "house"}: +${money(payback.delta)} a landing · pays back in ${payback.landings}` : "",
    pledge ? `Security for ${pledge.lenderId === player?.id ? "a loan you made" : `${possessive(playerById(state, pledge.lenderId)?.name || "a")} loan`}` : "",
  ].filter(Boolean).map((text) => `<p class="sheet__note">${escapeHtml(text)}</p>`).join("");
  morph(el.sheet, `<div class="sheet__body">
    ${deedDetail(state, index, owner, prop, space, ownerLine)}${standingHere(state, index)}${notes}
    <div class="actions">${buttons.join("")}${close}</div>
  </div>`);
}

el.sheet.addEventListener("click", async (event) => {
  // The sheet handles its own buttons; a re-render below detaches the clicked button, so the
  // document handler must never see this click.
  event.stopPropagation();
  if (event.target === el.sheet) {
    closeSheet();
    return;
  }
  const button = event.target.closest("button");
  if (!button) return;
  if (button.dataset.sheetCmd === "close") closeSheet();
  else if (String(button.dataset.sheetCmd || "").startsWith("pk-")) await poker.sheetCommand(button.dataset.sheetCmd);
  else if (String(button.dataset.sheetCmd || "").startsWith("ld-")) await ludo.sheetCommand(button.dataset.sheetCmd);
  else if (String(button.dataset.sheetCmd || "").startsWith("qz-")) await quiz.sheetCommand(button.dataset.sheetCmd);
  else if (String(button.dataset.sheetCmd || "").startsWith("cg-")) await closest.sheetCommand(button.dataset.sheetCmd);
  else if (button.dataset.sheetCmd === "confirm") {
    const run = ui.sheet?.run;
    closeSheet();
    await run?.();
  } else if (button.dataset.act) {
    await act(JSON.parse(button.dataset.act));
  } else if (button.dataset.cmd) {
    await command(button.dataset.cmd, button.dataset.arg);
  }
});
el.sheet.addEventListener("close", () => { ui.sheet = null; });

// ---------------------------------------------------------------------------------------------
// Events

document.addEventListener("click", (event) => {
  // Tap any deed for its details: the i on a trade tile (inside a toggle button), a card on the
  // board stage, a cue chip, a deed in an offer, a MY DEEDS tile.
  const deed = event.target.closest("[data-sheet]");
  if (deed && !el.sheet.contains(deed) && ui.state) {
    event.preventDefault();
    event.stopPropagation();
    sfx("tap", {}, 0.5);
    openSheet({ kind: "square", index: Number(deed.dataset.sheet) });
    return;
  }
  const button = event.target.closest("button");
  if (!button || button.disabled || el.sheet.contains(button)) return;
  sound.unlockAudio();
  const data = button.dataset;
  // Decision buttons wait while the board is still moving (stale input used to hit the engine).
  if (el.app.dataset.busy && (data.act || data.cmd === "trade-send-plan") && el.main.contains(button)) return;
  if (data.tab) {
    const wasFriends = ui.tab === "friends";
    ui.tab = data.tab;
    if (data.tab === "friends") {
      ui.inviteOpen = wasFriends ? false : ui.inviteOpen;
      if (ui.friends.list === null) loadFriends();
    } else ui.inviteOpen = false;
    leaveTradeTab(data.tab);
    sfx("tap", {}, 0.5);
    render();
    return;
  }
  if (data.act) {
    act(JSON.parse(data.act));
    return;
  }
  if (data.cmd) {
    command(data.cmd, data.arg);
    return;
  }
  if (data.token) {
    ui.join.tokenKind = data.token;
    sfx("tap", {}, 0.5);
    render();
    return;
  }
  if (data.toggle) {
    const [field, index] = data.toggle.split(":");
    const value = Number(index);
    const list = ui.trade[field] || [];
    ui.trade = { ...ui.trade, [field]: list.includes(value) ? list.filter((item) => item !== value) : [...list, value], planned: false };
    sfx("tap", {}, 0.5);
    render();
    return;
  }
  if (data.sheet !== undefined) {
    openSheet({ kind: "square", index: Number(data.sheet) });
    return;
  }
  if (data.loanPartner && ui.loanDraft) {
    ui.loanDraft.targetId = data.loanPartner;
    if (ui.loanDraft.direction === "lend") ui.loanDraft.collateral = [];
    render();
    return;
  }
  if (data.loanDir && ui.loanDraft && !(ui.loanDraft.locked && data.loanDir === "lend")) {
    ui.loanDraft.direction = data.loanDir;
    ui.loanDraft.collateral = [];
    render();
    return;
  }
  if (data.loanPledge !== undefined && ui.loanDraft) {
    const value = Number(data.loanPledge);
    const list = ui.loanDraft.collateral;
    ui.loanDraft.collateral = list.includes(value) ? list.filter((item) => item !== value) : [...list, value];
    render();
  }
});

document.addEventListener("input", (event) => {
  const target = event.target;
  if (target.id === "p-name" || target.id === "inv-name") ui.join.name = target.value;
  else if (target.id === "p-code") {
    const code = normaliseCode(target.value);
    if (target.value !== code) target.value = code;
    const changed = code !== ui.join.code;
    ui.join.code = code;
    clearTimeout(ui.summaryTimer);
    if (changed && (ui.join.summary || ui.join.missing || ui.join.error)) {
      Object.assign(ui.join, { summary: null, missing: false, error: "", mode: null, reclaim: null });
      if (code.length < 5) render();
    }
    if (code.length === 5) {
      ui.join.loading = true;
      ui.summaryTimer = setTimeout(async () => {
        if (await tryRejoin(code)) return;
        loadSummary(code);
      }, 300);
    }
  } else if (target.dataset.audioVolume !== undefined) {
    ui.audio = { ...ui.audio, volume: Math.max(0, Math.min(1, Number(target.value) / 100)) };
    applyAudio();
  } else if (target.dataset.loanField && ui.loanDraft) {
    const field = target.dataset.loanField;
    ui.loanDraft[field] = Math.max(field === "dueTurns" ? 1 : 0, Math.round(Number(target.value) || 0));
    if (field === "principal") {
      const label = el.main.querySelector("[data-loan-amount]");
      if (label) label.textContent = money(ui.loanDraft.principal);
      if (ui.loanDraft.repay < ui.loanDraft.principal) ui.loanDraft.repay = suggestLoan(ui.loanDraft.principal).repay;
    }
  }
});

document.addEventListener("change", (event) => {
  if (event.target.dataset.audioVolume !== undefined) {
    setAudio({ volume: ui.audio.volume });
    sfx("tap", {}, 0.8);
  }
  if (event.target.dataset.loanField) render();
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && el.app.dataset.modal === "trade" && !el.sheet.open && ui.state?.phase !== "trade") {
    event.preventDefault();
    command(ui.loanDraft ? "loan-cancel" : "trade-close");
    return;
  }
  if (event.key !== "Enter") return;
  if (event.target.id === "p-name") {
    event.preventDefault();
    if (joinMode(ui.join) === "create") createGame();
    else joinTable();
  } else if (event.target.id === "p-code") {
    event.preventDefault();
    event.target.blur();
  }
});

// Shake to roll (Android and other browsers that expose motion without a permission prompt).
function installShake() {
  if (typeof DeviceMotionEvent === "undefined" || typeof DeviceMotionEvent.requestPermission === "function") return;
  ui.canShake = true;
  let lastPeak = 0;
  let lastRoll = 0;
  window.addEventListener("devicemotion", (event) => {
    const a = event.accelerationIncludingGravity || event.acceleration;
    if (!a || el.app.dataset.screen !== "roll" || el.app.dataset.busy) return;
    const force = Math.hypot(a.x || 0, a.y || 0, a.z || 0);
    const now = Date.now();
    if (force > 24) {
      if (now - lastPeak < 600 && now - lastRoll > 2500) {
        lastRoll = now;
        act({ type: "roll" });
      }
      lastPeak = now;
    }
  });
}

// ---------------------------------------------------------------------------------------------
// Sessions

function startSession(session, initialState) {
  ui.live?.stop();
  ui.session = session;
  storage.set(SESSION_KEY, session);
  rememberTable(session.code);
  ui.tableOnline = undefined;
  clearInterval(ui.summaryPoll);
  ui.summaryPoll = null;
  ui.join = { ...ui.join, busy: false, error: "", note: "", reclaim: null, mode: null };
  ui.state = null;
  ui.queue = [];
  ui.decision = null;
  ui.tab = "turn";
  ui.qrUrl = null;
  ui.trade = emptyDraft();
  ui.inviteOpen = false;
  poker.reset();
  ludo.reset();
  quiz.reset();
  closest.reset();
  history.replaceState(null, "", `/j/${session.code}`);
  const live = new LiveRoom(ui.client, {
    code: session.code,
    token: session.token,
    onState: applyState,
    onEvent: () => {},
    onStatus: (status) => {
      el.app.dataset.connection = status;
      if (status === "offline") toast("Lost the table. Retrying…", { error: true });
    },
    // The seat moved to another phone (its WebSocket says so, or the token stops working), or the
    // table closed: show the join screen for the same table.
    onFatal: (error) => {
      if (ui.live === live) endSession(error);
    },
  });
  ui.live = live;
  if (initialState) live.accept(initialState);
  live.start();
  keepAwake(true);
  if (session.role === "player") setTimeout(showHowToOnce, 700);
}

// The first Estate table this phone joins opens HOW TO PLAY by itself, once (it stays in MORE).
function showHowToOnce() {
  if (ui.demo || storage.get(HOW_SEEN_KEY) || !ui.state || isPoker(ui.state) || isLudo(ui.state) || isQuiz(ui.state) || isClosest(ui.state) || ui.sheet) return;
  storage.set(HOW_SEEN_KEY, true);
  openSheet({ kind: "help", first: true });
}

// Keep the phone's screen on while it is at a table: a phone that sleeps between turns suspends
// its connection and has to reconnect when it wakes (the screen wake lock needs HTTPS; elsewhere
// this quietly does nothing). The browser drops the lock when the page is hidden, so take it again
// whenever the page comes back.
let wakeLock = null;
let wantAwake = false;
async function keepAwake(on) {
  wantAwake = on;
  try {
    if (on && !wakeLock && navigator.wakeLock && !document.hidden) {
      const lock = await navigator.wakeLock.request("screen");
      wakeLock = lock;
      lock.addEventListener?.("release", () => {
        if (wakeLock === lock) wakeLock = null;
      });
      if (!wantAwake) await lock.release();
    } else if (!on && wakeLock) {
      const lock = wakeLock;
      wakeLock = null;
      await lock.release();
    }
  } catch {
    wakeLock = null;
  }
}
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && wantAwake) keepAwake(true);
});

function codeFromUrl() {
  const match = location.pathname.match(/\/j\/([A-Za-z0-9]{5})/);
  return normaliseCode(match?.[1] || new URLSearchParams(location.search).get("code") || "");
}

async function startLive() {
  const code = codeFromUrl();
  const params = new URLSearchParams(location.search);
  ui.server = await ui.client.server();
  ui.join.code = code;
  ui.join.lockedCode = Boolean(code);
  const publicOrigin = ui.server?.publicURL ? new URL(ui.server.publicURL).origin : null;
  const lanOrigin = ui.server?.lanURL ? new URL(ui.server.lanURL).origin : null;
  ui.remote = params.has("invite") || Boolean(publicOrigin && publicOrigin === location.origin && publicOrigin !== lanOrigin);
  const saved = storage.get(SESSION_KEY);
  let movedAway = false;
  if (saved?.code && saved?.token && (!code || saved.code === code)) {
    try {
      const state = await ui.client.state(saved.code, saved.token);
      startSession(saved, state);
      startDeviceSocket();
      return;
    } catch (error) {
      storage.set(SESSION_KEY, null);
      movedAway = saved.role === "player" && /session is invalid|moved/i.test(error.message || "");
    }
  }
  // Same-device rejoin: this browser already holds a seat at the table.
  if (code && !params.has("watch") && await tryRejoin(code)) {
    startDeviceSocket();
    return;
  }
  if (code && ui.server) {
    ui.join.loading = true;
    await loadSummary(code);
    if (movedAway && ui.join.summary) ui.join.note = "Your seat moved to another phone.";
    const fresh = !humanSeatsHaveMe(ui.join.summary);
    ui.invitedView = params.has("invite") || (ui.remote && ui.join.summary?.status === "lobby" && fresh);
  } else if (!code && ui.lastTable?.code && ui.server) {
    // "BACK TO TABLE …" only while that table still exists.
    try {
      await ui.client.summary(ui.lastTable.code);
      if (movedAway) ui.join.note = "Your seat moved to another phone.";
    } catch (error) {
      if (error.status === 404 && error.message !== "Not available on this server") forgetTable(ui.lastTable.code);
    }
  }
  if (!ui.join.tokenKind) ui.join.tokenKind = TOKEN_KINDS[0];
  render();
  startDeviceSocket();
}

// Whether a lobby summary has a seat that could be this person's (an offline human seat).
function humanSeatsHaveMe(summary) {
  return Boolean(summary?.players?.some((player) => player.reclaimable));
}

function startDeviceSocket() {
  if (ui.deviceSocket) return;
  ui.deviceSocket = new DeviceSocket(ui.client, {
    onInvite: (invite) => {
      ui.invite = invite;
      if (!ui.session) {
        ui.join.code = invite.code;
        ui.join.lockedCode = true;
        ui.invitedView = true;
        ui.remote = true;
        loadSummary(invite.code);
      } else toast(`${invite.from?.name || "A friend"} invited you to table ${invite.code}`);
      buzz(120);
    },
    onFriends: (friends) => {
      ui.friends.list = friends;
      if (ui.tab === "friends") render();
    },
  });
  ui.deviceSocket.start();
}

// ?demo=<fixture> renders a real engine state; a few names show screens that need no game state.
// ?demo=replay steps through the 36-state replay as Robin's phone (hops, chips, cues); add
// &as=spectator to watch it as a spectator, &step=<ms> to change the pace, &from=<n> to start
// later, &manual=1 to step with window.__replay.next(). Trade: &trade=1&partner=Sam
// &pick=19,39-3,6 (your deeds - theirs) &cash=50 &quote=80 &planned=1 &loan=1 (the LOAN segment);
// &nodeeds=1 empties your hand.
// Getting back in: who (started game, your seat offline), moved (after the seat moved away),
// who-409 (that seat is still connected), who-lobby, code (no code; "BACK TO TABLE").
// Phone-hosted games: home, home-empty, new-game, host-lobby, guest-lobby. &sheet=<square> opens a
// deed sheet.
const DEMO_SCREENS = {
  join: { fixture: "lobby", session: false },
  invited: { fixture: "lobby", session: false, invited: true },
  who: { fixture: "waiting", session: false, who: true },
  moved: { fixture: "waiting", session: false, who: true, note: "Your seat moved to another phone." },
  "who-409": { fixture: "waiting", session: false, who: true, conflict: true },
  "who-lobby": { fixture: "lobby", session: false, who: true },
  code: { fixture: "lobby", session: false, code: true },
  home: { fixture: "lobby", session: false, code: true, games: "demo-games" },
  "home-empty": { fixture: "lobby", session: false, code: true, games: [] },
  "new-game": { fixture: "lobby", session: false, code: true, create: true },
  "host-lobby": { fixture: "lobby", noPc: true },
  "guest-lobby": { fixture: "lobby", noPc: true, guest: true },
  friends: { fixture: "waiting", tab: "friends" },
  invite: { fixture: "waiting", tab: "friends", inviteOpen: true },
  property: { fixture: "jail", tab: "property" },
  "trade-compose": { fixture: "roll", tab: "trade" },
  spectator: { fixture: "waiting", spectator: true },
  players: { fixture: "waiting", spectator: true, tab: "players" },
};

// ?demo=scene&s=<id>: one screen from scenarios.js (state, view, then one action).
async function startScene(id) {
  const scene = sceneById(id);
  if (!scene) throw new Error(`Unknown scene "${id}"`);
  ui.demoHold = true;
  document.title = `Estate · ${scene.title}`;
  if (scene.boot) {
    morph(el.main, '<p class="loading">Finding your table…</p>');
    return;
  }
  if (scene.replay !== undefined) {
    await startReplay(new URLSearchParams(`manual=1&from=${scene.replay}`));
    setTimeout(() => window.__replay.next(), 700);
    return;
  }
  const fixtures = {};
  for (const name of sceneFixtures(scene)) fixtures[name] = await loadFixture(name);
  const { state, ui: extra } = buildScene(scene, fixtures);
  if (extra.server) ui.server = extra.server;
  if (extra.games === "demo") ui.games = { list: (await (await fetch("/phone/demo-games.json", { cache: "no-store" })).json()).games, error: "" };
  else if (extra.games) ui.games = extra.games;
  if (extra.friends === "fixture") ui.friends.list = (await loadFixture("friends")).friends;
  for (const name of extra.sent || []) {
    const friend = ui.friends.list?.find((candidate) => candidate.name === name);
    if (friend) ui.friends.sent[friend.deviceId] = true;
  }
  for (const key of ["lastTable", "trade", "plan", "loanDraft", "inviteOpen", "tableOnline", "remote", "invitedView", "copied", "outSeen", "muted", "audio", "prefs", "tradeInfo", "hostOpen", "debtPicks"]) {
    if (key in extra) ui[key] = extra[key];
  }
  ui.tab = scene.tab || "turn";
  if (scene.session === false) {
    ui.session = null;
    ui.join = { ...ui.join, code: "", name: "", summary: null, lockedCode: false, mode: null, error: "", note: "", busy: false, reclaim: null, missing: false, loading: false, tokenKind: "rocket", ...(extra.join || {}) };
    render();
  } else {
    ui.session = { code: state.code, token: "demo", playerId: state.viewerId, role: state.viewerId ? "player" : "spectator" };
    applyState(state);
  }
  if (scene.then) setTimeout(() => runSceneAction(scene.then, state, fixtures[scene.fixture || "roll"]), 500);
}

function runSceneAction(then, state, fixture) {
  if (then.sheet !== undefined) {
    const free = state.board.findIndex((space, index) => space.type === "property" && !state.properties?.[index]?.ownerId);
    openSheet({ kind: "square", index: then.sheet === "free" ? free : then.sheet });
  }
  if (then.confirm && then.confirm !== "end-game") command(then.confirm === "leave" ? "leave-ask" : "bankrupt");
  if (then.audioSheet || then.settings) openSheet({ kind: "settings" });
  if (then.help) openSheet({ kind: "help", first: then.help === "first" });
  if (then.confirm === "end-game") command("end-game-ask");
  if (then.remove) {
    const target = state.players.find((player) => player.name === then.remove);
    if (target) command("host-remove-ask", target.id);
  }
  if (then.offline) el.app.dataset.connection = "offline";
  if (then.toast) toast(then.toast, { error: Boolean(then.error) });
  if (then.cue) showCue(cueHtml({ amount: null, playerId: state.viewerId, otherId: null, ...then.cue }, ui.state));
  if (then.stamp) {
    const stamp = document.createElement("span");
    stamp.className = "turn-stamp is-held";
    stamp.textContent = "YOUR TURN";
    pboard.fx?.append(stamp);
  }
  if (then.next || then.nextFromFixture) {
    const next = then.nextFromFixture ? structuredClone(fixture.state) : then.next(state);
    applyState({ ...next, version: (state.version || 0) + 1 });
  }
}

async function startDemo(name) {
  ui.demo = name;
  document.title = `Estate · Phone demo · ${name}`;
  const params = new URLSearchParams(location.search);
  const special = DEMO_SCREENS[name] || {};
  try {
    const server = await loadFixture("server").catch(() => null);
    ui.server = server?.server || null;
    if (name === "replay") {
      await startReplay(params);
      return;
    }
    if (name === "scene") {
      await startScene(params.get("s"));
      return;
    }
    if (/^poker-/.test(name)) {
      await startPokerDemo(name, params);
      return;
    }
    if (/^ludo-/.test(name)) {
      await startLudoDemo(name, params);
      return;
    }
    if (/^quiz-/.test(name)) {
      await startQuizDemo(name, params);
      return;
    }
    if (/^closest-/.test(name)) {
      await startClosestDemo(name, params);
      return;
    }
    const fixture = await loadFixture(special.fixture || name);
    let state = fixture.state || fixture.replay?.[0];
    if (!state) throw new Error("Fixture has no state");
    if (special.session === false) {
      ui.session = null;
      ui.join.code = state.code;
      ui.join.lockedCode = true;
      ui.join.name = special.invited ? "Priya" : "Robin";
      ui.remote = Boolean(special.invited) || params.has("remote");
      // Summary seats as the server sends them: Robin's phone is offline (reclaimable) in the
      // "who" demos; everyone else is connected.
      const seat = (player) => ({
        id: player.id, name: player.name, tokenKind: player.tokenKind, color: playerColor(state, player), input: player.input,
        online: !(special.who && player.name === "Robin"), reclaimable: Boolean(special.who && player.name === "Robin"),
      });
      const players = special.invited ? state.players.slice(0, 3) : special.who ? state.players : state.players.filter((player) => player.name !== "Robin");
      ui.join.summary = {
        code: state.code,
        status: state.status,
        host: playerById(state, state.hostId),
        players: players.map(seat),
        openSeats: state.status === "lobby" ? 6 - state.players.length : 0,
      };
      if (special.note) ui.join.note = special.note;
      if (special.conflict) {
        const sam = state.players.find((player) => player.name === "Sam");
        ui.join.reclaim = { playerId: sam.id, status: 409, error: "That seat is still connected." };
      }
      if (special.code) {
        ui.join = { ...ui.join, code: "", lockedCode: false, summary: null, mode: special.create ? "create" : null };
        ui.lastTable = { code: state.code, at: Date.now() };
        if (special.games === "demo-games") {
          const fixture = await (await fetch("/phone/demo-games.json", { cache: "no-store" })).json();
          ui.games = { list: fixture.games, error: "" };
          ui.lastTable = { code: "TLUER", at: Date.now() };
        } else ui.games = { list: Array.isArray(special.games) ? special.games : [], error: "" };
        if (special.create) {
          ui.join.name = "Ana";
          ui.join.gameType = gameTypeOrEstate(params.get("game"));
        }
      }
      const taken = new Set((ui.join.summary?.players || []).map((player) => player.tokenKind));
      ui.join.tokenKind = TOKEN_KINDS.find((kind) => !taken.has(kind));
      ui.invitedView = Boolean(special.invited);
      render();
      return;
    }
    if (special.spectator || params.get("as") === "spectator") state = { ...state, viewerId: null, viewerRole: "spectator", viewerName: "Guest" };
    if (special.guest) state = { ...state, hostId: state.players.find((player) => player.id !== state.viewerId && player.input !== "ai")?.id || state.hostId, viewerIsHost: false };
    ui.session = { code: state.code, token: "demo", playerId: state.viewerId, role: state.viewerId ? "player" : "spectator" };
    ui.tab = special.tab || params.get("tab") || "turn";
    ui.inviteOpen = Boolean(special.inviteOpen);
    if (special.noPc) ui.tableOnline = false;
    if (ui.tab === "friends") await loadFriends();
    if (name === "trade-compose" || params.get("trade") || params.get("partner")) {
      const partner = params.get("partner") ? state.players.find((player) => player.name === params.get("partner")) : null;
      ui.tab = "trade";
      ui.trade = { ...emptyDraft(), targetId: partner?.id || null };
      if (params.get("pick")) {
        const [offer, request] = params.get("pick").split("-").map((list) => (list ? list.split(",").map(Number) : []));
        ui.trade = { ...ui.trade, offer: offer || [], request: request || [], offerCash: Number(params.get("cash") || 0) };
      }
      if (params.get("quote")) ui.trade.quote = { key: quoteKey(ui.trade), offerCash: Number(params.get("quote")), requestCash: 0 };
      if (params.get("loan")) {
        ui.loanDraft = newLoan("lend", partner?.id || null);
        ui.trade = emptyDraft();
      }
      if (params.get("planned")) {
        ui.plan = { ...ui.trade, planned: true };
        ui.trade = { ...emptyDraft(), targetId: params.get("keep") ? ui.trade.targetId : null };
      }
    }
    if (params.get("nodeeds") && state.viewerId) {
      const properties = { ...state.properties };
      for (const [index, prop] of Object.entries(properties)) if (prop.ownerId === state.viewerId) properties[index] = { ...prop, ownerId: null, houses: 0 };
      state = { ...state, properties };
    }
    applyState(state);
    // &sheet=<square>: open that square's deed sheet (as a tap on the board would).
    if (params.get("sheet") !== null) setTimeout(() => openSheet({ kind: "square", index: Number(params.get("sheet")) }), 400);
  } catch (error) {
    morph(el.main, `<div class="tip tip--warn">${escapeHtml(error.message)}</div>`);
  }
}

// ?demo=ludo-<fixture> (web/fixtures/ludo): a Ludo state on this phone. &as=spectator watches it;
// &sheet=more|help|settings opens a sheet.
async function startLudoDemo(name, params) {
  let { state } = await buildLudoScene(name.endsWith("-phone") || name.endsWith("-table") ? name : `${name}-phone`);
  if (params.get("as") === "spectator") state = { ...state, viewerId: null, viewerRole: "spectator", viewerName: "Guest", you: null };
  ui.session = { code: state.code, token: "demo", playerId: state.viewerId, role: state.viewerId ? "player" : "spectator" };
  if (params.get("nopc")) ui.tableOnline = false;
  applyState(state);
  const sheet = params.get("sheet");
  if (sheet) setTimeout(() => openSheet({ kind: sheet === "settings" ? "settings" : `ld-${sheet}` }), 300);
}

// ?demo=quiz-<fixture> (web/fixtures/quiz): a quiz state on this phone, on the fixture's own frozen
// clock (so openAt counts as passed when the fixture says so). &as=spectator watches it;
// &sheet=more|help|settings opens a sheet; &bet=<amount> sets the bet stepper.
async function startQuizDemo(name, params) {
  const wanted = name.endsWith("-phone") || name.endsWith("-table") ? name : `${name}-phone`;
  let { state } = await buildQuizScene(wanted).catch(() => buildQuizScene(name));
  if (params.get("as") === "spectator") state = { ...state, viewerId: null, viewerRole: "spectator", viewerName: "Guest", you: null };
  ui.session = { code: state.code, token: "demo", playerId: state.viewerId || null, role: state.viewerId ? "player" : "spectator" };
  if (params.get("bet") && state.you?.canBet) quiz.setup({ betDraft: { key: state.phase === "bet" ? `${state.code}|g${state.clue?.seq ?? 0}` : `${state.code}|f${state.final?.seq ?? 0}`, amount: Number(params.get("bet")) } });
  if (params.get("nopc")) ui.tableOnline = false;
  applyState(state);
  const sheet = params.get("sheet");
  if (sheet) setTimeout(() => openSheet({ kind: sheet === "settings" ? "settings" : `qz-${sheet}` }), 300);
}

// ?demo=closest-<fixture or scene> (web/fixtures/closest, web/shared/closest-scenes.js): a Closest
// Guess state on this phone, on the fixture's frozen clock. &as=spectator watches it;
// &sheet=more|help|settings opens a sheet; &pad=<digits> types on the pad (reopening it when the
// guess is in); &chips=A,B&stake=N&stakeSlot=T sets the bet draft.
async function startClosestDemo(name, params) {
  const wanted = name.endsWith("-phone") || name.endsWith("-table") ? name : `${name}-phone`;
  let { state } = await buildClosestScene(wanted).catch(() => buildClosestScene(name));
  if (params.get("as") === "spectator") state = { ...state, viewerId: null, viewerRole: "spectator", viewerName: "Guest", you: null };
  ui.session = { code: state.code, token: "demo", playerId: state.viewerId || null, role: state.viewerId ? "player" : "spectator" };
  const seq = state.question?.seq;
  if (params.get("pad") !== null) closest.setup({ pad: { seq, digits: String(params.get("pad")).replace(/\D/g, "") }, changing: state.you?.guess !== null && state.you?.guess !== undefined ? seq : null });
  if (params.get("chips")) {
    const chips = params.get("chips").split(",").map(Number);
    closest.setup({ bet: { key: `${state.code}|${seq}`, chips, stake: Number(params.get("stake")) || 0, stakeSlot: params.get("stakeSlot") !== null ? Number(params.get("stakeSlot")) : chips[0] } });
  }
  applyState(state);
  const sheet = params.get("sheet");
  if (sheet) setTimeout(() => openSheet({ kind: sheet === "settings" ? "settings" : `cg-${sheet}` }), 300);
}

// ?demo=poker-<fixture or scene> (web/shared/poker-scenes.js): a poker state on this phone.
// &as=spectator watches it; &sheet=more|help|settings|allin opens a sheet; &pre=checkfold ticks a
// pre-action; &raise=<amount> opens the raise panel there.
async function startPokerDemo(name, params) {
  let { state, pk } = await buildPokerScene(name);
  if (params.get("as") === "spectator") state = { ...state, viewerId: null, viewerRole: "spectator", viewerName: "Guest", you: null, players: state.players.map((player) => (player.shown || state.phase === "runout" || state.phase === "showdown" ? player : { ...player, cards: null })) };
  if (params.get("pre")) pk = { ...pk, pre: { kind: params.get("pre"), amount: Number(params.get("amount") || 0), hand: state.handNumber || 0 } };
  if (params.get("raise")) pk = { ...pk, raiseOpen: true, raiseTo: Number(params.get("raise")) };
  poker.setup(pk);
  ui.session = { code: state.code, token: "demo", playerId: state.viewerId, role: state.viewerId ? "player" : "spectator" };
  if (params.get("nopc")) ui.tableOnline = false;
  applyState(state);
  const sheet = params.get("sheet");
  if (sheet) {
    setTimeout(() => {
      if (sheet === "settings") openSheet({ kind: "settings" });
      else if (sheet === "allin") openSheet({ kind: "pk-allin", amount: state.you?.legal?.raise?.max || state.you?.legal?.allIn || 0, action: { type: "allin" } });
      else openSheet({ kind: `pk-${sheet}` });
    }, 300);
  }
}

async function startReplay(params) {
  const fixture = await loadFixture("replay");
  const states = expandReplay(fixture) || [];
  const viewer = params.get("as") === "spectator" ? null : states[0]?.players.find((player) => player.name === (params.get("as") || "Robin"))?.id || null;
  const asViewer = (state) => ({ ...state, viewerId: viewer, viewerRole: viewer ? "player" : "spectator", viewerName: viewer ? playerById(state, viewer)?.name : "Guest", localSeats: undefined });
  const pace = Math.max(400, Number(params.get("step")) || 2600);
  let index = Math.max(0, Math.min(states.length - 1, Number(params.get("from")) || 0));
  ui.session = { code: states[0].code, token: "demo", playerId: viewer, role: viewer ? "player" : "spectator" };
  ui.tab = "turn";
  const stepOnce = () => {
    applyState(asViewer(states[index]));
    index = (index + 1) % states.length;
  };
  window.__replay = { index: () => index, total: states.length, next: stepOnce };
  stepOnce();
  if (params.get("manual")) return;
  const tick = () => {
    if (!ui.draining) stepOnce();
    setTimeout(tick, ui.draining ? 300 : pace);
  };
  setTimeout(tick, pace);
}

function init() {
  installLayout();
  ensureMetalDefs();
  sound.installAudioUnlock(window);
  installShake();
  loadPalette();
  // QA: ui and the board; in a demo, apply(state) pushes a state as the socket would.
  window.__phone = { ui, pboard, poker, ludo, quiz, closest, apply: (state) => (ui.demo ? applyState(state) : undefined) };
  // Installable app: the service worker (offline screen) and the install offer for SETTINGS.
  startApp({ onChange: () => render() });
  setInterval(tickClocks, 250);
  const demo = demoParam();
  // The first real render waits (briefly) for the fonts, so nothing re-flows once it is up.
  fontsReady().then(() => {
    if (demo) startDemo(demo);
    else startLive();
  });
}

init();
