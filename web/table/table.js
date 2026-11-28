// Estate PC table screen: board, players, QR to join, and controller/keyboard seats.
// Renders from server state (docs/API.md); all rules run on the server.

import { Board, deedHtml } from "../shared/board.js";
import { EstateClient, LiveRoom, demoParam, expandReplay, loadFixture, normaliseCode, storage, unreachable } from "../shared/client.js";
import { awaitApproval, showClaimPrompts } from "../shared/claims.js"; // seat-approval hook (server review S1/S2)
import { diffEvents } from "../shared/events.js";
import { AI_LEVELS, cardReadMs, inputOf, money, nextAiLevel, playerById } from "../shared/format.js";
import { ownedBy, rulesOf } from "../shared/insights.js";
import * as motion from "../shared/motion.js";
import * as music from "../shared/music.js";
import { receiptsFor } from "../shared/receipts.js";
import * as sound from "../shared/sound.js";
import { TOKEN_KINDS, TOKEN_LABELS, ensureMetalDefs } from "../shared/tokens.js";
import { soundCues } from "./cues.js";
import { COMPUTER_SPEEDS, loadScreen, normaliseScreen, saveScreen } from "./settings.js";
import { composeFixture, resolveNames } from "./demo.js";
import { CAMERA_EASE, CLOSE_UP_RENT, FOLLOW_BIG_REST_MS, FOLLOW_IN_MS, FOLLOW_OUT_MS, FOLLOW_REST_MS, LAYOUT, cameraPlan, followsHop, hopShots, moveMs, overviewView, panMs, sameView, viewFor, viewTransform, visibleRect } from "./camera.js";
import * as views from "./views.js";
import { inviteText, openWhatsApp } from "../shared/invite.js";
import { deedDetailHtml } from "../shared/deed.js";
import { morph, paint } from "../shared/morph.js";
import { FACES, TABLETOP_FACES, fontsReady } from "../shared/fonts.js";
import { buildPokerScene } from "../shared/poker-scenes.js";
import { createPokerTable, isPokerState, pokerEndConfirmHtml, pokerHelpOverlayHtml, pokerSettingsOverlayHtml } from "./poker-table.js";
import { createLudoTable, isLudoState, ludoEndConfirmHtml, ludoHelpOverlayHtml, ludoSettingsOverlayHtml } from "./ludo-table.js";
import { buildLudoScene } from "../shared/ludo-scenes.js";
import { createQuizTable, isQuizState, quizEndConfirmHtml, quizHelpOverlayHtml, quizSettingsOverlayHtml } from "./quiz-table.js";
import { buildQuizScene, demoClock } from "../shared/quiz-scenes.js";
import { isTimed, tickAuctionClock } from "../shared/auction-clock.js";
import { debtChecklist, toggleDebtRow } from "../shared/debt-checklist.js";
import { isPerson, lastMoveAt, waitedOnIds } from "../shared/standin.js";
import { closestEndConfirmHtml, closestHelpOverlayHtml, closestSettingsOverlayHtml, createClosestTable, isClosestState } from "./closest-table.js";
import { buildClosestScene } from "../shared/closest-scenes.js";
import { GAME_NAME as CLOSEST_NAME } from "../shared/closest-name.js";
import { gameLabel } from "../shared/poker-view.js";
import { installState, promptInstall, startApp } from "../shared/pwa.js";

const SESSION_KEY = "estate.table";
const AI_LEVEL_KEY = "estate.aiLevel";
const STAGE = { width: 1600, height: 900 };

const $ = (selector) => document.querySelector(selector);
const el = {
  viewport: $("#viewport"),
  stage: $("#stage"),
  join: $("#join-screen"),
  game: $("#game-screen"),
  board: $("#board"),
  plates: $("#plates"),
  panel: $("#panel"),
  dock: $("#dock"),
  camera: $("#camera"),
  show: $("#show"),
  diceHud: $("#dice-hud"),
  minimap: $("#minimap"),
  minimapBoard: $("#minimap .minimap__board"),
  minimapView: $("#minimap .minimap__view"),
  plateFx: $("#plate-fx"),
  code: $("#table-code"),
  conn: $("#conn-note"),
  mute: $("#mute"),
  menuBtn: $("#menu-btn"),
  soundMenu: $("#sound-menu"),
  deedView: $("#deed-view"),
  overlay: $("#overlay"),
  gate: $("#gate"),
  stageFx: $("#stage-fx"),
  soundPill: $("#sound-pill"),
  netbar: $("#netbar"),
  toast: $("#toast"),
  loading: $("#loading"),
  poker: $("#poker-screen"),
  ludo: $("#ludo-screen"),
  quiz: $("#quiz-screen"),
  closest: $("#closest-screen"),
};

const board = new Board(el.board);
const ui = {
  client: new EstateClient(),
  live: null,
  session: null,
  server: null,
  demo: null,
  demoPlay: false,
  state: null,
  status: "connecting",
  queue: [],
  pendingEvents: [],
  draining: false,
  busy: false,
  overlay: null,
  gate: null,
  cam: { view: null, timer: null, released: null, holdKey: null, reducedNoted: false },
  games: { list: [], key: "", timer: null },
  panelRows: [],
  bid: null,
  // The debt checklist's ticks, keyed by views.debtKey (a new debt starts from the plan).
  debtPicks: {},
  decisionKey: "",
  pads: new Map(),
  padNames: {},
  qr: { code: null, url: null },
  victoryShownFor: null,
  supportsSeats: true,
  prevCash: new Map(),
  aiLevel: AI_LEVELS.includes(storage.get(AI_LEVEL_KEY)) ? storage.get(AI_LEVEL_KEY) : "hard",
  screen: loadScreen(),
  soundMenu: false,
  offerDismissed: null,
  deedView: null,
  deedReturn: null,
};

// Poker rooms (state.game === "poker"): the table screen is a viewer of the same table the phones
// show (poker-table.js); this file keeps the session, the menus and the sound.
const pokerTable = createPokerTable({
  el,
  ui,
  storage,
  ctx: () => ctx(),
  render: () => render(),
  renderAll: () => render(),
  perform: (action) => perform(null, action),
  addAi: (level) => addComputer(level),
  sound: (name, opts) => sound.play(name, opts),
  reduced: () => reducedMotion(),
});

// Ludo rooms (state.game === "ludo"): the board, the plates and the keyboard and controller seats
// (ludo-table.js); this file keeps the session, the menus and the sound, as for poker.
const ludoTable = createLudoTable({
  el,
  ui,
  storage,
  ctx: () => ctx(),
  render: () => render(),
  perform: (action, seatId = null) => perform(seatId, action),
  addAi: (level) => addComputer(level),
  sound: (name, opts) => sound.play(name, opts),
  reduced: () => reducedMotion(),
  fast: () => ui.screen.speed === "fast",
});

// Quiz rooms (state.game === "quiz"): phones only, so this screen shows the board, the clue and
// the scores and lets the host start and set the game (quiz-table.js). Its clock is the socket's
// synced clock (docs/QUIZ-API.md §1.1): the choices appear here when it reaches clue.openAt.
const quizTable = createQuizTable({
  el,
  ui,
  storage,
  ctx: () => ctx(),
  render: () => render(),
  perform: (action) => perform(null, action),
  addAi: (level) => addComputer(level),
  sound: (name, opts) => sound.play(name, opts),
  reduced: () => reducedMotion(),
  now: () => (ui.demo ? demoClock(ui.state) : ui.live?.clock?.now() ?? Date.now()),
  openHelp: () => openOverlay({ kind: "menu", page: "help" }),
});

// Near Thing rooms (state.game === "closest"): phones only as well (guesses and bets are
// secret); this screen shows the question, the line of guesses, the chips and the answer
// (closest-table.js). The line-up and the reveal play from their phase start on the same clock.
const closestTable = createClosestTable({
  el,
  ui,
  storage,
  ctx: () => ctx(),
  render: () => render(),
  perform: (action) => perform(null, action),
  addAi: (level) => addComputer(level),
  sound: (name, opts) => sound.play(name, opts),
  reduced: () => reducedMotion(),
  // Demos show each phase's settled frame unless asked to play it (&play=1).
  settled: () => reducedMotion() || (ui.demo && !ui.demoPlay),
  now: () => (ui.demo ? demoClock(ui.state) : ui.live?.clock?.now() ?? Date.now()),
  openHelp: () => openOverlay({ kind: "menu", page: "help" }),
});

// The game type of a state (rooms keep theirs for life).
const gameTypeOfState = (state) => (isPokerState(state) ? "poker" : isLudoState(state) ? "ludo" : isQuizState(state) ? "quiz" : isClosestState(state) ? "closest" : "estate");
const GAME_NAMES = { estate: "Estate", poker: "poker", ludo: "Ludo", quiz: "quiz", closest: CLOSEST_NAME };

// ---------------------------------------------------------------------------------------------
// Layout: a fixed 1600 × 900 stage scaled to fit the screen (the approved design size).

function fit() {
  const scale = Math.min(window.innerWidth / STAGE.width, window.innerHeight / STAGE.height);
  const x = (window.innerWidth - STAGE.width * scale) / 2;
  const y = (window.innerHeight - STAGE.height * scale) / 2;
  el.stage.style.transform = `translate(${x}px, ${y}px) scale(${scale})`;
}

// ---------------------------------------------------------------------------------------------
// Context for the pure views

function padList() {
  const seatedPads = new Set((ui.state?.localSeats || []).filter((seat) => seat.input === "controller").map((seat) => seat.pad));
  return Object.entries(ui.padNames).map(([index, name]) => ({ index: Number(index), name, seated: seatedPads.has(Number(index)) }));
}

function serverNow() {
  // A demo's clock is the fixture's `now`; without one, time stands still at its last log entry
  // (nobody has waited, nothing is idle).
  if (ui.demo) return Number(ui.state?.now) > 0 ? demoClock(ui.state) : lastMoveAt(ui.state) ?? demoClock(ui.state);
  return ui.live?.clock?.now() ?? Date.now();
}

function ctx() {
  return {
    server: ui.server,
    status: ui.status,
    demo: Boolean(ui.demo),
    pads: padList(),
    padNames: ui.padNames,
    qrUrl: ui.qr.code === ui.state?.code ? ui.qr.url : null,
    origin: location.origin,
    host: location.host,
    supportsSeats: ui.supportsSeats,
    aiLevel: ui.aiLevel,
    watching: ui.state?.viewerRole !== "table",
    bid: ui.bid,
    // The server's clock (a timed auction's countdown, idle times): the fixture's frozen now in a
    // demo, the socket's synced clock live (docs/API.md §3c).
    now: serverNow(),
    debtPicks: ui.debtPicks,
    games: ui.games.list,
    sound: { ...sound.getSettings(), locked: sound.audioLocked() },
  };
}

// ---------------------------------------------------------------------------------------------
// Rendering

function render() {
  const signature = focusSignature(document.activeElement);
  renderGate();
  const state = ui.state;
  if (!state) {
    restoreFocus(signature);
    return;
  }
  el.loading.hidden = true;
  music.setMood(music.moodFor(state) || "lobby");
  if (isClosestState(state)) {
    el.join.hidden = true;
    el.game.hidden = true;
    el.poker.hidden = true;
    el.ludo.hidden = true;
    el.quiz.hidden = true;
    quizTable.stop();
    el.closest.hidden = false;
    syncGamesPoll(false);
    closestTable.render(state);
    loadQr(state.code);
    renderOverlay();
    restoreFocus(signature);
    return;
  }
  el.closest.hidden = true;
  closestTable.stop();
  if (isQuizState(state)) {
    el.join.hidden = true;
    el.game.hidden = true;
    el.poker.hidden = true;
    el.ludo.hidden = true;
    el.quiz.hidden = false;
    syncGamesPoll(false);
    quizTable.render(state);
    loadQr(state.code);
    renderOverlay();
    restoreFocus(signature);
    return;
  }
  el.quiz.hidden = true;
  quizTable.stop();
  if (isLudoState(state)) {
    el.join.hidden = true;
    el.game.hidden = true;
    el.poker.hidden = true;
    el.ludo.hidden = false;
    syncGamesPoll(false);
    ludoTable.render(state);
    loadQr(state.code);
    renderOverlay();
    restoreFocus(signature);
    return;
  }
  el.ludo.hidden = true;
  if (isPokerState(state)) {
    el.join.hidden = true;
    el.game.hidden = true;
    el.poker.hidden = false;
    syncGamesPoll(false);
    pokerTable.render(state);
    loadQr(state.code);
    renderOverlay();
    restoreFocus(signature);
    return;
  }
  el.poker.hidden = true;
  syncOfferModal(state);
  const lobby = state.status === "lobby";
  el.join.hidden = !lobby;
  el.game.hidden = lobby;
  // ACTIVE GAMES: poll the server's games list while this lobby is on screen.
  syncGamesPoll(lobby && state.viewerRole === "table" && !ui.demo && Boolean(ui.server) && !ui.gate);
  if (lobby) renderJoin(state);
  else renderGame(state);
  renderOverlay();
  refreshDeedView();
  // While a state's animations play, the camera follows them; it settles when they're done.
  if (!lobby && !ui.draining) cameraToState(state);
  restoreFocus(signature);
}

// The "show" slot above the action card: the deed for sale, the bid, the card, the deal.
function setShow(html) {
  if (el.show.dataset.html === html) return;
  el.show.dataset.html = html;
  clearTimeout(ui.showTimer);
  // What was shown (a card that has been read, a deed) fades instead of vanishing.
  if (!html && !el.show.hidden && el.show.innerHTML) {
    el.show.classList.add("is-leaving");
    ui.showTimer = setTimeout(() => {
      el.show.hidden = true;
      el.show.innerHTML = "";
      el.show.classList.remove("is-leaving");
    }, 360);
    return;
  }
  el.show.classList.remove("is-leaving");
  el.show.innerHTML = html;
  el.show.hidden = !html;
  // The deed / card / bid takes the dice's place above the action card.
  if (html) hideDice(true);
}

// ---------------------------------------------------------------------------------------------
// Camera: the board moves inside the stage with one GPU-composited transform (camera.js).
// CAMERA = FOLLOW MOVES (the default): long hops (7+ squares, past PAYDAY, or a card's move) are
// followed at the ZOOM setting (1.6×), panning with the token square by square, with a short rest on the landing square; money moves, cards and every
// decision then play on the whole board (the deed, card or deal shows big beside it).
// CLOSE-UPS ONLY: the whole board, with a short close-up on a big rent. OFF: always the whole
// board. A drawer frames the deeds it is about whatever the setting. Reduced motion: always the
// whole board, no moves (the TV says so once). Hidden tab: no animation, the camera just jumps.

const reducedMotion = () => motion.prefersReducedMotion();
const EASE = CAMERA_EASE;

function applyView(view, ms = 0, ease = EASE) {
  const instant = !ms || document.hidden;
  el.board.style.transition = instant ? "none" : `transform ${ms}ms ${ease}`;
  el.board.style.transform = viewTransform(view);
  ui.cam.view = view;
  // The mini-map shows only while zoomed in, with a gold rectangle for what the camera sees.
  const zoomed = view.z > 1.05 && ui.overlay?.kind !== "trade";
  el.minimap.hidden = !zoomed;
  const scale = (el.minimapBoard.clientWidth || 242) / LAYOUT.board;
  const rect = visibleRect(view);
  const box = el.minimapView.style;
  box.transition = instant ? "none" : `left ${ms}ms ${ease}, top ${ms}ms ${ease}, width ${ms}ms ${ease}, height ${ms}ms ${ease}`;
  box.left = `${(rect.x * scale).toFixed(1)}px`;
  box.top = `${(rect.y * scale).toFixed(1)}px`;
  box.width = `${(rect.w * scale).toFixed(1)}px`;
  box.height = `${(rect.h * scale).toFixed(1)}px`;
}

// The moment a close-up belongs to. No version in it: an unrelated update in the same moment (a
// phone reconnecting) must not restart the close-up or zoom back in after it eased out.
function cameraKey(state) {
  return `${state.code}|${state.phase}|${state.currentPlayerId}|${state.lastLanding?.id || ""}`;
}

// Property drawer: the deed with focus, else all the player's deeds. Loan drawer: the security.
function inspectSquares() {
  const overlay = ui.overlay;
  if (overlay?.kind === "property") {
    const seat = ui.state?.localSeats?.find((candidate) => candidate.seatId === overlay.seatId);
    return seat ? { focus: focusedDeed(), squares: ownedBy(ui.state, seat.playerId) } : null;
  }
  if (overlay?.kind === "loan") return { focus: null, squares: overlay.draft?.collateral || [] };
  return null;
}

// Put the camera where this state wants it. `instant` snaps (an animation was cut short).
function cameraToState(state, { instant = false } = {}) {
  if (!state || state.status === "lobby" || el.game.hidden) return;
  // Trade is a modal over the dimmed board now: the camera stays where the game wants it.
  let plan = cameraPlan(state, { inspect: inspectSquares() });
  if (ui.screen.camera === "off" && plan.frame !== "drawer") plan = { kind: "overview", squares: [], frame: "main", hold: 0 };
  const key = cameraKey(state);
  if (reducedMotion() || (plan.hold && ui.cam.released === key)) plan = { kind: "overview", squares: [], frame: plan.frame, hold: 0 };
  const view = viewFor(plan, (index) => board.rect(index), cameraLayout());
  if (!sameView(ui.cam.view, view)) applyView(view, instant ? 0 : moveMs(ui.cam.view, view, { reduced: reducedMotion(), hidden: document.hidden }));
  el.minimap.hidden = docked() || !(ui.cam.view?.z > 1.05);
  if (!plan.hold) {
    clearTimeout(ui.cam.timer);
    ui.cam.holdKey = null;
  } else if (ui.cam.holdKey !== key) {
    // Rest on the landing square for a moment, then ease back to the whole board.
    clearTimeout(ui.cam.timer);
    ui.cam.holdKey = key;
    ui.cam.timer = setTimeout(() => {
      if (ui.draining || !ui.state || cameraKey(ui.state) !== key) return;
      ui.cam.released = key;
      cameraToState(ui.state);
    }, plan.hold);
  }
}

// Back to the whole board (a close-up or a followed hop eases out), never a jump.
function wholeBoard(ms = 360) {
  if (docked()) return;
  const view = overviewView();
  if (sameView(ui.cam.view, view)) return;
  clearTimeout(ui.cam.timer);
  ui.cam.holdKey = null;
  applyView(view, reducedMotion() || document.hidden ? 0 : ms);
}

// Before money moves or a card turns over: if the follow camera is still resting on the landing
// square, ease out to the whole board (the notes fly token to token; at 2× one end is off-screen).
async function easeOut() {
  if (!(ui.cam.view?.z > 1.05) || docked()) return;
  wholeBoard(FOLLOW_OUT_MS);
  await motion.wait(Math.round(FOLLOW_OUT_MS * 0.6));
}

// Follow one hop (CAMERA = FOLLOW MOVES): zoom in on the token, then pan with it. Returns the
// camera's view for each square of the path, or null when this hop plays on the whole board.
async function followStart(playerId, path, jail, card = false) {
  if (!followsHop({ mode: ui.screen.camera, reduced: reducedMotion(), hidden: document.hidden, docked: docked(), path, jail, card })) {
    if (path.length > 1 && !jail) noteReducedCamera();
    return null;
  }
  const layout = cameraLayout();
  const points = path.map((square) => board.point(square, 0, 1));
  const token = board.tokenEl(playerId);
  if (token?.dataset.x) points[0] = { x: Number(token.dataset.x), y: Number(token.dataset.y) };
  const shots = hopShots(points, { zoom: layout.focusZoom, layout });
  clearTimeout(ui.cam.timer);
  ui.cam.holdKey = null;
  applyView(shots[0], FOLLOW_IN_MS);
  await motion.wait(FOLLOW_IN_MS - 80);
  return shots;
}

// Reduced motion turns the camera off. When that comes from the computer's own setting (not this
// screen's REDUCED MOTION = ON), say so once, so the owner knows why the board never zooms.
function noteReducedCamera() {
  if (ui.cam.reducedNoted || ui.screen.camera === "off" || ui.screen.motion !== "system" || !reducedMotion()) return;
  ui.cam.reducedNoted = true;
  toast("Camera off: this computer has reduced motion turned on. MENU → SETTINGS → REDUCED MOTION: OFF brings the camera back.", { ms: 9000 });
}

// The camera's layout with this screen's zoom (settings): focus at the chosen zoom, fits scaled
// to match.
function cameraLayout() {
  const zoom = ui.screen.zoom || LAYOUT.focusZoom;
  return { ...LAYOUT, focusZoom: zoom, fitMaxZoom: LAYOUT.fitMaxZoom * (zoom / LAYOUT.focusZoom) };
}

document.addEventListener("visibilitychange", () => {
  if (!document.hidden && ui.state && !ui.draining) cameraToState(ui.state, { instant: true });
});

// ---------------------------------------------------------------------------------------------
// ACTIVE GAMES list in the lobby (GET /api/games every 5 s while the lobby is visible).

function syncGamesPoll(on) {
  if (on && !ui.games.timer) {
    pollGames();
    ui.games.timer = setInterval(pollGames, 5000);
  } else if (!on && ui.games.timer) {
    clearInterval(ui.games.timer);
    ui.games.timer = null;
  }
}

async function pollGames() {
  if (document.hidden || ui.games.busy) return;
  ui.games.busy = true;
  try {
    const result = await ui.client.listGames();
    const list = views.otherGames(result?.games, ui.state?.code);
    // Re-render only when something a row shows has changed.
    const key = JSON.stringify(list.map((game) => [game.code, game.status, game.turn, game.tableOnline, game.openSeats, game.host?.name, (game.players || []).map((player) => [player.tokenKind, player.color, player.online])]));
    if (key !== ui.games.key) {
      ui.games.key = key;
      ui.games.list = list;
      if (ui.state?.status === "lobby" && !el.join.hidden) render();
    }
  } catch {
    // An older server without /api/games: no list, and nothing to say about it.
  } finally {
    ui.games.busy = false;
  }
}

function renderJoin(state) {
  // Keep whatever is being typed into the resume box across re-renders.
  const typed = el.join.querySelector("#resume-code")?.value;
  morph(el.join, views.joinHtml(state, { ...ctx(), resumeCode: typed ?? "" }));
  loadQr(state.code);
}

function loadQr(code) {
  if (ui.demo || !code || ui.qr.code === code || ui.state?.viewerRole !== "table") return;
  ui.qr = { code, url: null };
  const url = ui.client.qrUrl(code);
  const probe = new Image();
  probe.onload = () => {
    if (ui.qr.code !== code) return;
    ui.qr.url = url;
    render();
  };
  probe.src = url;
}

function renderGame(state, { skipBoard = false } = {}) {
  const current = playerById(state, state.currentPlayerId);
  if (!skipBoard) {
    board.update(state, { currentSquare: current && state.status === "playing" ? current.position : null });
    board.placeTokens(state);
    // A timed auction plays on the board's centre: the deed, the clock and who leads (UX-03).
    const stage = views.stageHtml(state, ctx());
    board.setSlot(stage);
    board.center?.classList.toggle("has-stage", Boolean(stage));
    markBoard(state);
    for (const player of state.players) board.tokenEl(player.id)?.classList.toggle("is-turn", player.id === current?.id && state.phase === "awaiting_roll");
  }
  const previousCash = new Map(ui.prevCash);
  morph(el.plates, views.platesHtml(state, ctx()));
  el.plates.dataset.count = String(state.players.length);
  for (const plate of el.plates.querySelectorAll(".plate")) {
    const id = plate.dataset.playerId;
    const amount = plate.querySelector(".plate__amount");
    const to = Number(amount.dataset.cash);
    const from = previousCash.get(id);
    if (from !== undefined && from !== to) motion.tickNumber(amount, from, to);
    ui.prevCash.set(id, to);
  }
  renderPanel(state);
  if (!ui.busy) setShow(views.slotHtml(state, ctx()));
  syncAuctionClock(state);
  syncIdleTick(state);
  const mapHtml = views.minimapHtml(state);
  morph(el.minimapBoard, mapHtml);
  const codeHtml = views.tableCodeHtml(state.code);
  morph(el.code, codeHtml);
  // The connection dot says "fine"; a word appears only when something is wrong.
  // (A demo shows DEMO unless it is previewing a connection state.)
  const demoLabel = ui.demo && !["offline", "connecting"].includes(ui.status);
  el.conn.querySelector(".side__conn-text").textContent = demoLabel ? "DEMO" : ui.status === "offline" ? "OFFLINE" : "";
  el.conn.dataset.status = demoLabel ? "live" : ui.status;
  // A timed auction's buttons change with every bid (new amounts); focus goes back to the first.
  const key = `${state.code}|${state.phase}|${state.currentPlayerId}|${views.actingSeats(state).map((seat) => seat.seatId).join(",")}|${state.auction?.currentPlayerId || ""}|${isTimed(state) ? state.auction.bidderId || "" : ""}`;
  if (key !== ui.decisionKey) {
    ui.decisionKey = key;
    ui.focusPanelNext = true;
  }
}

// A timed auction's clock runs between states: every 200 ms the ring, the seconds and GOING ONCE
// are brought up to date on the server's clock (no re-render). It stops when the auction ends.
function syncAuctionClock(state) {
  const timed = isTimed(state);
  if (!timed) {
    clearInterval(ui.aucTimer);
    ui.aucTimer = null;
    return;
  }
  const tick = () => {
    if (!isTimed(ui.state)) {
      clearInterval(ui.aucTimer);
      ui.aucTimer = null;
      return;
    }
    tickAuctionClock(el.game, ui.state.auction, serverNow());
  };
  tick();
  if (!ui.aucTimer && !ui.demo) ui.aucTimer = setInterval(tick, 200);
}

// IDLE m:ss (plates, PLAYERS) counts up between states: from the log's newest time on the server's
// clock, so once a second the plates (and an open PLAYERS page) are painted again while the game
// waits on a person.
function syncIdleTick(state) {
  const waiting = !ui.demo && state.status === "playing" && isEstateState(state) && waitedOnIds(state).some((id) => isPerson(playerById(state, id)));
  if (!waiting) {
    clearInterval(ui.idleTimer);
    ui.idleTimer = null;
    return;
  }
  if (ui.idleTimer) return;
  ui.idleTimer = setInterval(() => {
    const now = ui.state;
    if (!now || now.status !== "playing" || ui.busy || el.game.hidden) return;
    morph(el.plates, views.platesHtml(now, ctx()));
    if (ui.overlay?.kind === "menu" && ui.overlay.page === "players") renderOverlay();
  }, 1000);
}

// The "now" card. While animations play it stays visible but greyed out and inert (ui.busy): it
// only ever offers actions for the state the board is actually showing. The same while the
// server can't be reached (review F10: A ROLL stayed lit through an outage).
function renderPanel(state) {
  const model = views.panelModel(state, ctx());
  const inert = ui.busy || offline();
  ui.panelRows = inert ? [] : model.rows;
  morph(el.panel, views.panelHtml(model, { busy: inert }));
  el.panel.classList.toggle("is-busy", inert);
  el.panel.classList.toggle("has-rows", model.rows.length > 0);
}

const offline = () => ui.status === "offline";

// Lost the server (review F10: a small OFFLINE chip and a toast were all the TV showed): a bar
// across the top until it's back. Games are saved on the server and come back as they were.
function renderNetbar() {
  const show = offline();
  if (show && !el.netbar.innerHTML) el.netbar.innerHTML = views.netbarHtml();
  el.netbar.hidden = !show;
}

function setBusy(busy) {
  if (ui.busy === busy) return;
  ui.busy = busy;
  if (busy && el.panel.contains(document.activeElement)) document.activeElement.blur();
  if (ui.state && ui.state.status !== "lobby") renderPanel(ui.state);
}

// Next-roll heat (replaces the risk sentence), squares in a trade offer, a card's destination.
// While a trade is being built, the squares in the draft pulse instead (give red, get green).
// Trade, property and loan open as a drawer on the right (the board stays in view); seat setup,
// rules, confirms and the victory screen stay modal.
const DOCK_KINDS = new Set(["property", "loan"]);
const DOCK_MARKS = ["give", "get", "own", "focus"];
const docked = () => DOCK_KINDS.has(ui.overlay?.kind);

function markBoard(state) {
  const marks = views.boardMarks(state);
  const open = docked();
  board.highlightSquares(open ? [] : marks.risk, "risk");
  board.highlightSquares(marks.pick, "pick");
  board.highlightSquares(marks.target, "target");
  if (!open) for (const kind of DOCK_MARKS) board.clearHighlights(kind);
}

// The deed with keyboard/pad/mouse focus in the property drawer (its square is framed).
function focusedDeed() {
  const node = el.dock.contains(document.activeElement) ? document.activeElement.closest?.("[data-deed]") : null;
  const index = node ? Number(node.dataset.deed) : NaN;
  return Number.isInteger(index) ? index : null;
}

function renderDock(state, seat) {
  const overlay = ui.overlay;
  const scroll = [...el.dock.querySelectorAll(".trade-col__deeds, .prop-dock__body, .loan-dock__body")].map((node) => node.scrollTop);
  board.clearHighlights("risk");
  for (const kind of DOCK_MARKS) board.clearHighlights(kind);
  if (overlay.kind === "property") {
    paint(el.dock, views.propertyDockHtml(state, seat, ctx()), `property:${seat.seatId}`);
    board.highlightSquares(ownedBy(state, seat.playerId), "own");
  } else {
    paint(el.dock, views.loanDockHtml(state, seat, overlay.draft, ctx()), `loan:${seat.seatId}`);
    board.highlightSquares(overlay.draft.collateral, "give");
  }
  el.dock.querySelectorAll(".trade-col__deeds, .prop-dock__body, .loan-dock__body").forEach((node, index) => { node.scrollTop = scroll[index] || 0; });
}

function renderOverlay() {
  const overlay = ui.overlay;
  const state = ui.state;
  const open = docked();
  el.overlay.hidden = !overlay || open;
  el.dock.hidden = !open;
  el.game.classList.toggle("is-docked", open);
  if (!open && el.dock.firstChild) {
    paint(el.dock, "", "");
    if (state && state.status !== "lobby") markBoard(state);
  }
  if (!overlay || !state) {
    paint(el.overlay, "", "");
    return;
  }
  const seat = overlay.seatId ? state.localSeats?.find((candidate) => candidate.seatId === overlay.seatId) : null;
  if (((open || overlay.kind === "trade" || overlay.kind === "offer") && !seat) || (overlay.kind === "rules" && state.status !== "lobby")) {
    ui.overlay = null;
    el.overlay.hidden = true;
    el.dock.hidden = true;
    el.game.classList.remove("is-docked");
    return;
  }
  if (open) {
    paint(el.overlay, "", "");
    renderDock(state, seat);
    return;
  }
  el.overlay.classList.toggle("overlay--trade", overlay.kind === "trade" || overlay.kind === "offer");
  // The open dialog is updated in place (morph): a state update while it is open must not rebuild
  // it, or it blinks out and lifts in again. A different dialog (or menu page) starts fresh.
  if (overlay.kind === "trade") {
    const scroll = [...el.overlay.querySelectorAll(".trade-col__deeds")].map((node) => node.scrollTop);
    paint(el.overlay, views.tradeModalHtml(state, seat, overlay.draft, { ...ctx(), peek: overlay.peek }), `trade:${overlay.seatId}`);
    el.overlay.querySelectorAll(".trade-col__deeds").forEach((node, index) => { node.scrollTop = scroll[index] || 0; });
  } else if (overlay.kind === "offer") {
    paint(el.overlay, views.offerModalHtml(state, seat, { peek: overlay.peek }), `offer:${overlay.tradeId}`);
  } else if (overlay.kind === "menu") {
    paint(el.overlay, menuPageHtml(state, overlay.page), `menu:${overlay.page}`);
  } else if (overlay.kind === "seat") {
    const name = el.overlay.querySelector("#seat-name");
    if (name) overlay.draft.name = name.value;
    paint(el.overlay, views.seatOverlayHtml(state, overlay.draft, ctx()), "seat");
  } else if (overlay.kind === "rules") {
    paint(el.overlay, views.rulesOverlayHtml(state), "rules");
  } else if (overlay.kind === "confirm") {
    paint(el.overlay, overlay.html || views.confirmOverlayHtml(overlay), `confirm:${overlay.title || (overlay.html ? "custom" : "")}`);
  } else if (overlay.kind === "victory") {
    if (!el.overlay.querySelector(".victory")) {
      paint(el.overlay, views.victoryOverlayHtml(state, ctx()), "victory");
      sound.play("win");
      motion.confetti(el.overlay.querySelector(".victory__confetti"), { count: 48, height: 900 });
    }
  }
}

function openOverlay(overlay) {
  ui.overlay = overlay;
  ui.focusOverlayNext = true;
  render();
}

function closeOverlay() {
  // A seat's incoming offer put away with Esc / B stays away (ACCEPT / DECLINE are on the card).
  if (ui.overlay?.kind === "offer") ui.offerDismissed = ui.overlay.tradeId;
  ui.overlay = null;
  ui.focusPanelNext = true;
  render();
}

// ---------------------------------------------------------------------------------------------
// Gate: full-screen card for getting back to a table (moved away, open elsewhere, typed code).

function renderGate() {
  const gate = ui.gate;
  el.gate.hidden = !gate;
  if (!gate) {
    morph(el.gate, "");
    return;
  }
  el.loading.hidden = true;
  const typed = el.gate.querySelector("#resume-code")?.value;
  const html = views.gateHtml({ ...gate, code: gate.kind === "resume" && typed !== undefined && !gate.fresh ? typed : gate.code, canCancel: Boolean(ui.state) });
  gate.fresh = false;
  morph(el.gate, html);
}

function showGate(gate) {
  if (ui.overlay?.kind !== "victory") ui.overlay = null;
  ui.gate = { ...gate, fresh: true };
  ui.focusGateNext = true;
  render();
}

function hideGate() {
  ui.gate = null;
  ui.focusPanelNext = true;
  render();
}

async function gateCommand(cmd) {
  const gate = ui.gate;
  if (!gate) return;
  if (cmd === "gate-retry" || cmd === "gate-take") await resumeTable(gate.code, { from: "manual" });
  else if (cmd === "gate-cancel") hideGate();
  else if (cmd === "new-table") await newTable();
}

// ---------------------------------------------------------------------------------------------
// Focus: real buttons, spatial movement for arrows / D-pad, restored across re-renders.

function focusSignature(node) {
  if (!node || node === document.body) return null;
  const attributes = ["id", "data-pkey", "data-row", "data-cmd", "data-game-code", "data-act", "data-toggle", "data-partner", "data-cash-field", "data-token", "data-cash", "data-lender", "data-pledge", "data-loan-step", "data-debt-tick", "data-host", "data-host-pick"];
  for (const name of attributes) {
    const value = node.getAttribute?.(name);
    if (value) return [name, value];
  }
  return null;
}

function scopeEl() {
  if (ui.deedView !== null) return el.deedView;
  if (ui.soundMenu) return el.soundMenu;
  if (ui.gate) return el.gate;
  if (docked()) return el.dock;
  if (ui.overlay) return el.overlay;
  if (ui.state?.status === "lobby") return el.join;
  return el.panel;
}

function focusables(root) {
  return [...root.querySelectorAll("button:not([disabled]), input:not([disabled]), [tabindex=\"0\"]")].filter((node) => node.getClientRects().length > 0);
}

// Focus without letting the browser scroll the scaled stage; scroll only the box the node is in.
function focusNode(node) {
  if (!node) return;
  node.focus({ preventScroll: true });
  if (node instanceof HTMLInputElement && node.type === "text") node.setSelectionRange(node.value.length, node.value.length);
  const box = node.closest(".trade-col__deeds, .dialog__body--scroll, .games__list, .prop-dock__body, .loan-dock__body");
  if (!box) return;
  const top = node.getBoundingClientRect().top - box.getBoundingClientRect().top;
  const scale = box.getBoundingClientRect().height / (box.clientHeight || 1) || 1;
  const y = top / scale + box.scrollTop;
  const height = node.offsetHeight;
  if (y < box.scrollTop) box.scrollTop = Math.max(0, y - 10);
  else if (y + height > box.scrollTop + box.clientHeight) box.scrollTop = y + height - box.clientHeight + 10;
}

function restoreFocus(signature) {
  const scope = scopeEl();
  if (ui.focusGateNext && ui.gate) {
    ui.focusGateNext = false;
    focusNode(el.gate.querySelector("#resume-code") || el.gate.querySelector(".big-btn--primary") || focusables(el.gate)[0]);
    return;
  }
  if (ui.focusOverlayNext && ui.overlay) {
    ui.focusOverlayNext = false;
    if (ui.overlay.kind === "trade") {
      focusNode(el.overlay.querySelector("[data-partner]") || focusables(el.overlay)[0]);
      return;
    }
    if (ui.overlay.kind === "loan") {
      focusNode(el.dock.querySelector("[data-lender]") || focusables(el.dock)[0]);
      return;
    }
    if (ui.overlay.kind === "property") {
      // The first thing you can do, else the first deed.
      focusNode(el.dock.querySelector(".pdeed__act:not([disabled])") || el.dock.querySelector(".pdeed") || focusables(el.dock)[0]);
      return;
    }
    const seatFocus = ui.overlay.kind === "seat" ? (ui.overlay.draft.input === "controller" ? el.overlay.querySelector(".token-choice.is-picked") : el.overlay.querySelector("#seat-name")) : null;
    const body = el.overlay.querySelector(".dialog__body");
    const first = seatFocus || el.overlay.querySelector("[data-autofocus]:not([disabled])") || (body && focusables(body)[0]) || focusables(el.overlay)[0];
    focusNode(first);
    if (first?.id === "seat-name" && ui.overlay.draft.autoName) first.select();
    return;
  }
  if (signature) {
    const [name, value] = signature;
    const match = [...scope.querySelectorAll(`[${name}]`)].find((node) => node.getAttribute(name) === value);
    // A pending "focus the panel" never applies while an overlay or the gate has the keys.
    if (match && !match.disabled && (!ui.focusPanelNext || ui.overlay || ui.gate)) {
      if (document.activeElement !== match) focusNode(match);
      return;
    }
    // A property action that changed (MORTGAGE ↔ UNMORTGAGE) or went grey: stay on its deed.
    if (name === "data-pkey" && ui.overlay?.kind === "property") {
      const card = el.dock.querySelector(`[data-pkey="${value.split(":")[0]}:card"]`);
      if (card) {
        focusNode(card);
        return;
      }
    }
  }
  if (ui.focusPanelNext && !ui.overlay && !ui.gate && !ui.busy) {
    ui.focusPanelNext = false;
    const first = el.panel.querySelector(".panel__row--action");
    if (first && ui.state?.status !== "lobby") first.focus({ preventScroll: true });
  }
}

// Trade dock: zones, not geometry. Partners ←/→; in YOU GIVE / YOU GET ↑/↓ walk the deeds then the
// cash (top → partners, bottom → the foot) and ←/→ switch column on the same row; the foot ←/→.
function tradeNav(direction) {
  const root = el.overlay;
  const zone = (name) => [...root.querySelectorAll(`[data-nav="${name}"]`)].filter((node) => !node.disabled);
  const overlay = ui.overlay;
  const current = document.activeElement;
  const at = root.contains(current) ? current.dataset.nav : null;
  const partner = () => zone("partners").find((node) => node.classList.contains("is-on")) || zone("partners")[0];
  const go = (node) => {
    if (node) focusNode(node);
    return Boolean(node);
  };
  if (!at) return go(partner() || focusables(root)[0]);
  const list = zone(at);
  const index = list.indexOf(current);
  const step = direction === "right" || direction === "down" ? 1 : -1;
  if (at === "partners") {
    if (direction === "left" || direction === "right") return go(list[index + step]);
    if (direction === "down") return go(zone(overlay.lastCol || "offer")[0] || zone("offer")[0] || zone("foot").at(-1));
    return false;
  }
  if (at === "offer" || at === "request") {
    overlay.lastCol = at;
    if (direction === "up") return go(list[index - 1] || partner());
    if (direction === "down") return go(list[index + 1] || zone("foot").at(-1));
    const towards = direction === "right" ? "request" : "offer";
    if (towards === at) return false;
    const other = zone(towards);
    if (!other.length) return go(partner());
    const deeds = (nodes) => nodes.filter((node) => !node.dataset.cashField);
    const cash = (nodes) => nodes.find((node) => node.dataset.cashField);
    const target = current.dataset.cashField ? cash(other) : deeds(other)[Math.min(deeds(list).indexOf(current), deeds(other).length - 1)];
    return go(target || cash(other) || other[0]);
  }
  if (at === "foot") {
    if (direction === "left" || direction === "right") return go(list[index + step]);
    if (direction === "up") return go(zone(overlay.lastCol || "offer").at(-1) || partner());
  }
  return false;
}

function moveFocus(direction) {
  if (ui.overlay?.kind === "trade" && !ui.gate && ui.deedView === null) {
    tradeNav(direction);
    return;
  }
  const items = focusables(scopeEl());
  if (!items.length) return;
  const current = document.activeElement;
  if (!items.includes(current)) {
    focusNode(items[0]);
    return;
  }
  const from = current.getBoundingClientRect();
  const cx = from.left + from.width / 2;
  const cy = from.top + from.height / 2;
  // Up/down stays inside a trade column (YOU GIVE / YOU GET) while it has somewhere to go.
  const column = current.closest("[data-col]");
  const vertical = direction === "up" || direction === "down";
  let best = null;
  let bestScore = Infinity;
  for (const item of items) {
    if (item === current) continue;
    const rect = item.getBoundingClientRect();
    const dx = rect.left + rect.width / 2 - cx;
    const dy = rect.top + rect.height / 2 - cy;
    const primary = { left: -dx, right: dx, up: -dy, down: dy }[direction];
    if (primary <= 2) continue;
    const secondary = vertical ? Math.abs(dx) : Math.abs(dy);
    const otherColumn = vertical && column && item.closest("[data-col]") !== column ? 2000 : 0;
    const score = primary + secondary * 2.2 + otherColumn;
    if (score < bestScore) {
      bestScore = score;
      best = item;
    }
  }
  focusNode(best || current);
}

function activateFocused() {
  const active = document.activeElement;
  if (active && scopeEl().contains(active) && active.matches("button:not([disabled])")) {
    active.click();
    return true;
  }
  // A deed card in the property drawer: A / Enter goes to its first action.
  const action = active?.matches?.(".pdeed") ? active.querySelector(".pdeed__act:not([disabled])") : null;
  if (action) {
    focusNode(action);
    return true;
  }
  return false;
}

function setInputMode(mode) {
  if (document.body.dataset.input !== mode) document.body.dataset.input = mode;
}

// ---------------------------------------------------------------------------------------------
// Actions

function toast(message, { error = false, ms = 0 } = {}) {
  el.toast.textContent = message;
  el.toast.hidden = false;
  el.toast.classList.toggle("is-error", error);
  clearTimeout(ui.toastTimer);
  ui.toastTimer = setTimeout(() => { el.toast.hidden = true; }, ms || (error ? 4200 : 2600));
}

function describeAction(action) {
  const extra = action.amount ? ` ${money(action.amount)}` : action.spaceIndex !== undefined ? ` · ${ui.state?.board?.[action.spaceIndex]?.name || action.spaceIndex}` : "";
  return `${action.type.replaceAll("_", " ")}${extra}`;
}

// Engine errors in plain words. Stale input (a press meant for the previous phase) is dropped.
const STALE = /not available during the current phase|not your turn|cannot respond to this|already rolled|has not started|no longer active/i;
const PLAIN = [
  [/Not enough cash|cannot exceed your cash/i, "Not enough cash"],
  [/must exceed the current bid/i, "Bid higher than the current bid"],
  [/complete color group/i, "Own the whole colour set first"],
  [/Sell every building/i, "Sell the houses on that set first"],
  [/bank has no (houses|hotels)/i, "The bank is out of buildings"],
  [/no longer belongs|belongs to/i, "Those deeds changed hands; check the offer"],
];

function plainError(message) {
  const text = String(message || "");
  if (STALE.test(text)) return null;
  for (const [pattern, plain] of PLAIN) if (pattern.test(text)) return plain;
  return text;
}

// This screen's table session ended because another screen took the table (docs/API.md "Getting
// back in"): its token stops working and its socket gets "This session moved to another device".
const MOVED = /moved to another device|session is invalid/i;

async function perform(seatId, action) {
  if (ui.busy) return false;
  if (offline()) {
    toast("Not connected to the server yet. The game is safe: try again when it's back.", { error: true });
    return false;
  }
  sound.play("tap");
  if (ui.demo) {
    console.info("[demo] would POST /action", JSON.stringify({ seatId, action }));
    toast(`Demo · would send: ${describeAction(action)}${seatId ? ` (seat ${seatId})` : ""}`);
    return true;
  }
  try {
    const state = await ui.client.act(ui.session.code, ui.session.token, action, seatId);
    ui.live?.accept(state);
    return true;
  } catch (error) {
    if (MOVED.test(error.message || "")) {
      sessionEnded(error);
      return false;
    }
    // The server didn't answer: say so within a second or two, not after the full grace.
    if (unreachable(error)) ui.live?.actionFailed();
    const message = plainError(error.message);
    if (message) toast(message, { error: true });
    else render();
    return false;
  }
}

async function runRow(row) {
  if (!row || ui.busy) return;
  if (row.act) {
    await perform(row.act.seatId, row.act.action);
    return;
  }
  const seat = ui.state?.localSeats?.find((candidate) => candidate.seatId === row.seatId);
  switch (row.cmd) {
    case "trade":
      openTrade(seat);
      break;
    case "loan":
      openLoan(seat);
      break;
    case "property":
      openOverlay({ kind: "property", seatId: seat.seatId });
      break;
    case "bankrupt": {
      askBankrupt(seat);
      break;
    }
    case "apply-plan":
      await applyPlan(seat, row.plan);
      break;
    case "new-table":
      await newTable();
      break;
    case "rematch":
      askRematch();
      break;
    default:
      break;
  }
}

// Tick or untick a row of the debt checklist (UX-04). PAY lights up when the ticks cover the debt.
function toggleDebtTick(key) {
  const state = ui.state;
  if (state?.phase !== "debt" || !state.debt) return;
  const id = views.debtKey(state);
  const list = debtChecklist(state, state.debt.payerId, ui.debtPicks[id] || null);
  ui.debtPicks = { [id]: toggleDebtRow(list, key) };
  sound.play("tap");
  render();
}

async function applyPlan(seat, plan) {
  for (const action of plan.actions) {
    const ok = await perform(seat.seatId, action);
    if (!ok || ui.demo) break;
  }
}

// Ask for a loan in debt: the richest player lends what's short, repaid +20% in 6 turns.
function openLoan(seat) {
  if (!seat) return;
  const me = playerById(ui.state, seat.playerId);
  const short = Math.max(10, (ui.state.debt?.amount || 0) - me.cash);
  const lender = ui.state.players.filter((player) => player.id !== me.id && !player.bankrupt).sort((a, b) => b.cash - a.cash)[0];
  const principal = Math.ceil(short / 10) * 10;
  openOverlay({ kind: "loan", seatId: seat.seatId, draft: { targetId: lender?.id || null, principal, repay: Math.ceil((principal * 1.2) / 10) * 10, dueTurns: 6, collateral: [] } });
}

function seatForPad(index) {
  return ui.state?.localSeats?.find((seat) => seat.input === "controller" && seat.pad === index) || null;
}

// Declare bankruptcy (resign): a person may at any time in play (0.10.1); confirm on the page
// first. Owing another player in the debt phase, everything goes to them; otherwise to the bank.
function askBankrupt(seat) {
  const state = ui.state;
  if (!seat || !views.canResign(state, seat.playerId)) return;
  openOverlay({
    kind: "confirm", danger: true, seatId: null,
    title: "DECLARE BANKRUPTCY?",
    text: views.resignText(state, seat.playerId),
    confirm: "DECLARE BANKRUPTCY",
    onConfirm: () => perform(seat.seatId, { type: "bankrupt" }),
  });
}

// Trade opens on one page with nobody and nothing selected. The draft dies with the overlay.
function openTrade(seat) {
  if (!seat) return;
  openOverlay({ kind: "trade", seatId: seat.seatId, draft: views.newTradeDraft() });
}

function openSeatSetup(input, pad = null) {
  const state = ui.state;
  if (!state || state.status !== "lobby" || ui.state.viewerRole !== "table") return;
  if (state.players.length >= views.maxPlayers(state)) {
    toast("Every seat is taken.");
    return;
  }
  const taken = new Set(state.players.map((player) => player.tokenKind).filter(Boolean));
  const tokenKind = TOKEN_KINDS.find((kind) => !taken.has(kind)) || TOKEN_KINDS[0];
  // Controllers can't type: the name defaults to the token ("Rocket") and follows the token pick.
  openOverlay({ kind: "seat", draft: { input, pad, name: TOKEN_LABELS[tokenKind], autoName: true, tokenKind } });
}

async function confirmSeat() {
  const draft = ui.overlay?.draft;
  if (!draft) return;
  const name = (el.overlay.querySelector("#seat-name")?.value || draft.name).trim().slice(0, 12) || draft.name;
  if (ui.demo) {
    console.info("[demo] would POST /seats", JSON.stringify({ input: draft.input, name, tokenKind: draft.tokenKind, pad: draft.pad }));
    toast(`Demo · would seat ${name} (${draft.input})`);
    closeOverlay();
    return;
  }
  try {
    const result = await ui.client.addSeat(ui.session.code, ui.session.token, { input: draft.input, name, tokenKind: draft.tokenKind, pad: draft.pad });
    sound.play("accept");
    closeOverlay();
    ui.live?.accept(result.state);
  } catch (error) {
    if (error.status === 404) ui.supportsSeats = false;
    toast(error.message, { error: true });
  }
}

// The join link for this table (the public short link when the server has one) and its message.
function inviteMessage() {
  return inviteText(gameTypeOfState(ui.state), ui.state?.code || "", views.joinUrl(ui.state, ctx()));
}

// WHATSAPP under the QR code (0.11.1): WhatsApp Desktop or Web opens with the message ready to send.
function inviteWhatsApp() {
  if (ui.demo) return toast("Demo · would open WhatsApp with the join link");
  sound.play("tap");
  openWhatsApp(inviteMessage());
  return undefined;
}

// COPY LINK under the QR code. localhost counts as a secure page, so the clipboard API works; a
// hidden text field is the fallback (the screen itself is not selectable).
async function inviteCopy() {
  const text = inviteMessage();
  let ok = false;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      ok = true;
    }
  } catch {
    ok = false;
  }
  if (!ok) {
    const field = document.createElement("textarea");
    field.value = text;
    field.setAttribute("readonly", "");
    field.style.cssText = "position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;";
    document.body.append(field);
    field.select();
    try { ok = document.execCommand("copy"); } catch { ok = false; }
    field.remove();
  }
  toast(ok ? "Link copied: paste it into WhatsApp or any chat" : "Couldn't copy: use the link under the QR code", { error: !ok });
}

async function lobbyCommand(cmd, arg) {
  if (cmd === "pick-game") return pickGame(arg);
  if (String(cmd).startsWith("pk-")) return pokerTable.command(cmd, arg);
  if (String(cmd).startsWith("ld-")) return ludoTable.command(cmd, arg);
  if (String(cmd).startsWith("qz-")) return quizTable.command(cmd, arg);
  if (String(cmd).startsWith("cg-")) return closestTable.command(cmd, arg);
  if (cmd === "invite-whatsapp") return inviteWhatsApp();
  if (cmd === "invite-copy") return inviteCopy();
  if (cmd === "rematch") return askRematch();
  if (cmd === "new-table") return askNewTable();
  if (cmd === "remove-player") return askRemovePlayer(arg);
  if (cmd === "seat-keyboard") return openSeatSetup("keyboard");
  if (cmd === "rules") return openOverlay({ kind: "rules" });
  if (cmd === "start") return perform(null, { type: "start" });
  if (cmd === "ai-level") {
    ui.aiLevel = nextAiLevel(ui.aiLevel);
    storage.set(AI_LEVEL_KEY, ui.aiLevel);
    render();
    return undefined;
  }
  if (cmd === "add-ai") return addComputer(ui.aiLevel);
  return undefined;
}

// NEW TABLE from the lobby (header chip or MENU): start over with a new table and code. Asks first
// when people have sat down: they stay at the old code (still in ACTIVE GAMES) and scan the new one.
function askNewTable() {
  const gameType = gameTypeOfState(ui.state);
  const people = (ui.state?.players || []).filter((player) => player.status !== "left" && inputOf(player) !== "ai");
  if (ui.state?.status !== "lobby" || !people.length) return newTable(gameType);
  return openOverlay({
    kind: "confirm", seatId: null,
    title: "START A NEW TABLE?",
    text: "This screen gets a new table with a new code. People who joined this one scan the new code to join again.",
    confirm: "NEW TABLE",
    cancel: "STAY HERE",
    onConfirm: () => newTable(gameType),
  });
}

// × on a lobby seat (POST /remove with the table token; the table is the host). A computer goes at
// once; a person is asked about first, since a phone player is sent back to the start.
function askRemovePlayer(playerId) {
  const player = playerById(ui.state, playerId);
  if (!player || ui.state?.status !== "lobby") return undefined;
  if (inputOf(player) === "ai") return removePlayer(playerId);
  return openOverlay({
    kind: "confirm", seatId: null,
    title: `REMOVE ${String(player.name || "THIS PLAYER").toUpperCase()}?`,
    text: "They leave this table. They can join again by scanning the code.",
    confirm: "REMOVE",
    cancel: "KEEP THEM",
    danger: true,
    onConfirm: () => removePlayer(playerId),
  });
}

async function removePlayer(playerId) {
  if (ui.demo) {
    toast("Demo · would remove that player");
    return;
  }
  try {
    ui.live?.accept(await ui.client.removePlayer(ui.session.code, ui.session.token, playerId));
    sound.play("tap");
  } catch (error) {
    toast(error.message, { error: true });
  }
}

async function addComputer(level) {
  if (ui.demo) {
    console.info("[demo] would POST /ai", level);
    toast("Demo · would add a computer player");
    return;
  }
  try {
    ui.live?.accept(await ui.client.addAi(ui.session.code, ui.session.token, level));
  } catch (error) {
    toast(error.message, { error: true });
  }
}

// The ESTATE / POKER / LUDO / QUIZ / NEAR THING picker on the start screen. A room keeps its game, so another game is a new
// table with a new code: at once while nobody has sat down, otherwise after a confirm.
function pickGame(gameType) {
  const want = ["poker", "ludo", "quiz", "closest"].includes(gameType) ? gameType : "estate";
  const now = gameTypeOfState(ui.state);
  if (!ui.state || ui.state.status !== "lobby" || want === now) return undefined;
  sound.play("tap");
  const people = (ui.state.players || []).filter((player) => player.status !== "left" && inputOf(player) !== "ai");
  if (!people.length) return newTable(want);
  return openOverlay({
    kind: "confirm", seatId: null,
    title: `SWITCH TO ${gameLabel(want)}?`,
    text: "That opens a new table with a new code. Everyone here joins again by scanning it.",
    confirm: `NEW ${gameLabel(want)} TABLE`,
    onConfirm: () => newTable(want),
  });
}

// PLAY AGAIN (victory screen, the card's row, the other games' final screens) asks first: it
// starts a new game for everyone, and people are often still pressing Enter / A from the last
// turn when the game ends. Focus starts on the cancel button, so stray presses never confirm.
function askRematch() {
  return openOverlay({
    kind: "confirm", seatId: null,
    title: "PLAY AGAIN?",
    text: "SAME PLAYERS · NEW GAME",
    tokens: views.rematchTokensHtml(ui.state),
    icon: "resume",
    confirm: "PLAY AGAIN",
    cancel: "NOT YET",
    onConfirm: rematch,
  });
}

// Same code, same seats, a fresh game in the lobby (POST /rematch with the table token).
async function rematch() {
  if (ui.demo) {
    toast("Demo · would start a rematch");
    return;
  }
  try {
    const state = await ui.client.request("POST", `/api/rooms/${encodeURIComponent(ui.session.code)}/rematch`, { token: ui.session.token });
    ui.overlay = null;
    ui.victoryShownFor = null;
    if (state?.version !== undefined) ui.live?.accept(state);
    sound.play("accept");
  } catch (error) {
    if (error.status === 404 && /not available/i.test(error.message)) await newTable();
    else toast(error.message, { error: true });
  }
}

// House rules from the lobby: each press sends the whole rule set at once.
async function setRule(target) {
  const rules = rulesOf(ui.state);
  if (target.dataset.rule) rules[target.dataset.rule] = !rules[target.dataset.rule];
  else {
    const steps = views.CASH_STEPS;
    const at = Math.max(0, steps.indexOf(rules.startingCash));
    rules.startingCash = steps[Math.max(0, Math.min(steps.length - 1, at + Number(target.dataset.ruleCash)))];
  }
  sound.play("tap");
  await perform(null, { type: "set_rules", rules });
}

// POST /api/tables {gameType} (client.js createTable(gameType); older clients take no argument).
async function createTableOf(gameType, replaces) {
  if (gameType === "estate" || /gameType/.test(String(ui.client.createTable))) return ui.client.createTable(gameType, replaces);
  await ui.client.ensureDevice({});
  return ui.client.request("POST", "/api/tables", { gameType });
}

async function newTable(gameType = "estate") {
  if (ui.demo) {
    toast(`Demo · would create a new ${GAME_NAMES[gameType] || "Estate"} table`);
    return;
  }
  // One at a time: presses while a table is on its way would each make another.
  if (ui.creatingTable) return;
  ui.creatingTable = true;
  const session = ui.session;
  const replacing = session?.watch ? null : session;
  // The server closes the empty table being replaced. Stop its connection first: a closing socket
  // falls back to polling, and that poll of the closed room logged a 400 on every game switch.
  const old = replacing ? ui.live : null;
  old?.stop();
  try {
    const result = await createTableOf(gameType, replacing);
    if (gameType !== "estate" && result?.state && gameTypeOfState(result.state) !== gameType) toast(`This server can't host ${GAME_NAMES[gameType]} yet.`, { error: true });
    ui.gate = null;
    startSession({ code: result.code, token: result.tableToken }, result.state);
  } catch (error) {
    toast(error.message, { error: true });
    // No new table: carry on with the old one (if it was closed after all, its connection ends it).
    if (old && ui.live === old) old.start();
  } finally {
    ui.creatingTable = false;
    // The old table closed but no new one came: start again, as for any closed table.
    const fatal = ui.fatalWhileCreating;
    ui.fatalWhileCreating = null;
    if (fatal && ui.live?.closedByServer) sessionEnded(fatal);
  }
}

function adjustBid(seat, delta) {
  const state = ui.state;
  if (state?.phase !== "auction" || state.auction?.currentPlayerId !== seat.playerId) return;
  const player = playerById(state, seat.playerId);
  const current = state.auction.currentBid;
  const key = `${state.auction.spaceIndex}:${current}`;
  const base = ui.bid?.key === key ? ui.bid.amount : Math.max(current + 1, Math.ceil((current + 10) / 10) * 10);
  const amount = Math.max(current + 1, Math.min(player.cash, base + delta));
  ui.bid = { key, amount };
  sound.play("tap");
  render();
}

// ---------------------------------------------------------------------------------------------
// Overlay clicks

el.overlay.addEventListener("click", async (event) => {
  const target = event.target.closest("button");
  if (!target || !ui.overlay) return;
  const overlay = ui.overlay;
  const cmd = target.dataset.cmd;
  if (cmd === "close") return closeOverlay();
  if (overlay.kind === "menu" || cmd?.startsWith("menu-")) return menuClick(overlay, target);
  if (cmd === "offer-accept" || cmd === "offer-decline") return answerOffer(overlay, cmd === "offer-accept");
  if (cmd === "offer-change") return changeCounter(overlay);
  if (cmd === "offer-not-selling") return notSelling(overlay, Number(target.dataset.arg));
  if (cmd === "seat-confirm") return confirmSeat();
  if (cmd === "confirm") {
    const run = overlay.onConfirm;
    closeOverlay();
    return run?.();
  }
  if (cmd === "new-table") return newTable();
  if (cmd === "rematch") return askRematch();
  if (overlay.kind === "rules" && (target.dataset.rule || target.dataset.ruleCash)) return setRule(target);
  if (target.dataset.token && overlay.kind === "seat") {
    const typed = el.overlay.querySelector("#seat-name")?.value;
    if (typed !== undefined && typed !== overlay.draft.name) overlay.draft.autoName = false;
    overlay.draft.tokenKind = target.dataset.token;
    if (overlay.draft.autoName) {
      overlay.draft.name = TOKEN_LABELS[target.dataset.token];
      const input = el.overlay.querySelector("#seat-name");
      if (input) input.value = overlay.draft.name;
    }
    return render();
  }
  if (target.dataset.act && overlay.kind === "property") {
    return perform(overlay.seatId, JSON.parse(target.dataset.act));
  }
  if (overlay.kind === "trade") return tradeClick(overlay, target);
  if (overlay.kind === "loan") return loanClick(overlay, target);
  return undefined;
});

// The seat name starts as the token's name: the first keystroke replaces it instead of appending.
el.overlay.addEventListener("focusin", (event) => {
  if (event.target.id === "seat-name" && ui.overlay?.draft?.autoName) event.target.select();
});

el.overlay.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && event.target.id === "seat-name") {
    event.preventDefault();
    confirmSeat();
  }
});

async function loanClick(overlay, target) {
  const draft = overlay.draft;
  if (target.dataset.lender) draft.targetId = target.dataset.lender;
  else if (target.dataset.pledge !== undefined) {
    const value = Number(target.dataset.pledge);
    draft.collateral = draft.collateral.includes(value) ? draft.collateral.filter((item) => item !== value) : [...draft.collateral, value];
  } else if (target.dataset.loanStep) {
    const [field, delta] = target.dataset.loanStep.split(":");
    const min = field === "dueTurns" ? 1 : 10;
    draft[field] = Math.max(min, draft[field] + Number(delta));
    if (field === "principal" && draft.repay < draft.principal) draft.repay = draft.principal;
    if (field === "repay" && draft.repay < draft.principal) draft.repay = draft.principal;
  } else if (target.dataset.cmd === "loan-send") {
    const action = { type: "offer_loan", targetId: draft.targetId, direction: "borrow", principal: draft.principal, repay: draft.repay, dueTurns: draft.dueTurns, collateral: draft.collateral };
    const ok = await perform(overlay.seatId, action);
    if (ok) closeOverlay();
    return;
  }
  render();
}

// ---------------------------------------------------------------------------------------------
// Trade (one page)

function tradeSeat(overlay) {
  return ui.state?.localSeats?.find((candidate) => candidate.seatId === overlay.seatId) || null;
}

function tradeStatus(overlay) {
  return views.tradeDraftStatus(ui.state, tradeSeat(overlay)?.playerId, overlay.draft);
}

// Which side's cash the bumpers / +− keys change: the column that has focus, else what you give.
function focusedTradeField() {
  const col = document.activeElement?.closest?.("[data-col]")?.dataset.col || ui.overlay?.lastCol;
  return col === "request" ? "request" : "offer";
}

function tradeCash(overlay, field, delta) {
  const draft = overlay.draft;
  const seat = tradeSeat(overlay);
  if (field === "request" && !draft.targetId) return;
  const key = field === "offer" ? "offerCash" : "requestCash";
  const owner = field === "offer" ? playerById(ui.state, seat?.playerId) : playerById(ui.state, draft.targetId);
  draft[key] = Math.max(0, Math.min(owner?.cash || 0, draft[key] + delta));
  sound.play("tap");
  render();
}

// A computer partner's price for the deeds in the deal, fetched quietly whenever they change. It
// only shows as a button; the cash changes only when someone presses it.
function refreshQuote(overlay) {
  const draft = overlay.draft;
  const seat = tradeSeat(overlay);
  const status = tradeStatus(overlay);
  const ai = status.target && inputOf(status.target) === "ai";
  if (!seat || !ai || !(status.offer.length || status.request.length)) {
    draft.quote = null;
    draft.quoteKey = "";
    return;
  }
  const key = `${status.target.id}|${status.offer.join(",")}|${status.request.join(",")}`;
  if (draft.quoteKey === key) return;
  draft.quoteKey = key;
  draft.quote = { loading: true };
  clearTimeout(ui.quoteTimer);
  ui.quoteTimer = setTimeout(async () => {
    let quote;
    if (ui.demo) quote = { offerCash: 80, requestCash: 0 };
    else {
      try {
        const result = await ui.client.tradeQuote(ui.session.code, ui.session.token, { targetId: status.target.id, offerProperties: status.offer, requestProperties: status.request }, seat.seatId);
        quote = { offerCash: result.offerCash || 0, requestCash: result.requestCash || 0 };
      } catch (error) {
        quote = { error: plainError(error.message) || "No price" };
      }
    }
    if (ui.overlay?.draft !== draft || draft.quoteKey !== key) return;
    draft.quote = quote;
    render();
  }, 250);
}

function applyQuote(overlay) {
  const draft = overlay.draft;
  const quote = draft.quote;
  if (!quote || quote.loading || quote.error) return;
  draft.offerCash = quote.offerCash || 0;
  draft.requestCash = quote.requestCash || 0;
  sound.play("tap");
  render();
}

async function sendTrade(overlay) {
  const draft = overlay.draft;
  const seat = tradeSeat(overlay);
  const status = tradeStatus(overlay);
  if (!seat || !status.valid) return;
  const action = { type: "offer_trade", targetId: draft.targetId, offer: { cash: status.offerCash, properties: status.offer }, request: { cash: status.requestCash, properties: status.request } };
  const ok = await perform(seat.seatId, action);
  if (ok && ui.overlay === overlay) closeOverlay();
}

async function tradeClick(overlay, target) {
  const draft = overlay.draft;
  if (!tradeSeat(overlay)) return;
  if (target.dataset.partner) {
    // A new partner keeps what you give; what you get was theirs, so it starts empty.
    if (draft.targetId !== target.dataset.partner) Object.assign(draft, { targetId: target.dataset.partner, request: [], requestCash: 0, quote: null, quoteKey: "" });
    sound.play("tap");
  } else if (target.dataset.toggle) {
    const [field, index] = target.dataset.toggle.split(":");
    const value = Number(index);
    draft[field] = draft[field].includes(value) ? draft[field].filter((item) => item !== value) : [...draft[field], value];
    sound.play("tap");
  } else if (target.dataset.cash) {
    const [field, delta] = target.dataset.cash.split(":");
    tradeCash(overlay, field, Number(delta));
    return;
  } else if (target.dataset.cmd === "trade-apply-quote") {
    applyQuote(overlay);
    return;
  } else if (target.dataset.cmd === "trade-send") {
    await sendTrade(overlay);
    return;
  }
  refreshQuote(overlay);
  render();
}

// ---------------------------------------------------------------------------------------------
// Panel, lobby and gate clicks

el.panel.addEventListener("click", (event) => {
  const tick = event.target.closest("[data-debt-tick]");
  if (tick && !tick.disabled) {
    toggleDebtTick(tick.dataset.debtTick);
    return;
  }
  const button = event.target.closest("[data-row]");
  if (button && !ui.busy) runRow(ui.panelRows[Number(button.dataset.row)]);
});

el.join.addEventListener("click", (event) => {
  const game = event.target.closest("[data-game-code]");
  if (game) {
    // SHOW HERE: this screen becomes that game's table (its lobby or mid-game).
    sound.play("tap");
    resumeTable(game.dataset.gameCode, { from: "manual" });
    return;
  }
  const button = event.target.closest("[data-cmd]");
  if (button?.dataset.cmd === "sound") {
    toggleSoundMenu();
    return;
  }
  if (button?.dataset.cmd === "menu") {
    openMenu();
    return;
  }
  if (button && button.dataset.cmd !== "resume-open") lobbyCommand(button.dataset.cmd, button.dataset.arg);
});

el.quiz.addEventListener("click", (event) => {
  const button = event.target.closest("[data-cmd]");
  if (!button || button.disabled) return undefined;
  const cmd = button.dataset.cmd;
  if (cmd === "sound") return toggleSoundMenu();
  if (cmd === "menu") return openMenu();
  if (cmd === "start") return perform(null, { type: "start" });
  if (cmd === "resume-open") return undefined;
  return lobbyCommand(cmd, button.dataset.arg);
});

el.closest.addEventListener("click", (event) => {
  const button = event.target.closest("[data-cmd]");
  if (!button || button.disabled) return undefined;
  const cmd = button.dataset.cmd;
  if (cmd === "sound") return toggleSoundMenu();
  if (cmd === "menu") return openMenu();
  if (cmd === "start") return perform(null, { type: "start" });
  if (cmd === "resume-open") return undefined;
  return lobbyCommand(cmd, button.dataset.arg);
});

el.ludo.addEventListener("click", (event) => {
  if (ludoTable.click(event.target)) return undefined;
  const button = event.target.closest("[data-cmd]");
  if (!button || button.disabled) return undefined;
  const cmd = button.dataset.cmd;
  if (cmd === "sound") return toggleSoundMenu();
  if (cmd === "menu") return openMenu();
  if (cmd === "start") return perform(null, { type: "start" });
  if (cmd === "resume-open") return undefined;
  return lobbyCommand(cmd, button.dataset.arg);
});

el.poker.addEventListener("click", (event) => {
  const button = event.target.closest("[data-cmd]");
  if (!button || button.disabled) return;
  const cmd = button.dataset.cmd;
  if (cmd === "sound") return toggleSoundMenu();
  if (cmd === "menu") return openMenu();
  if (cmd === "start") return perform(null, { type: "start" });
  if (cmd === "resume-open") return undefined;
  return lobbyCommand(cmd, button.dataset.arg);
});

el.gate.addEventListener("click", (event) => {
  const button = event.target.closest("[data-cmd]");
  if (button && button.dataset.cmd !== "resume-open") gateCommand(button.dataset.cmd);
});

// The "RESUME A TABLE" box (lobby header and gate): open that table on this screen.
document.addEventListener("submit", (event) => {
  const form = event.target.closest?.('[data-form="resume"]');
  if (!form) return;
  event.preventDefault();
  const code = normaliseCode(form.querySelector("input")?.value);
  if (code.length !== 5) {
    toast("Type the 5-letter table code", { error: true });
    return;
  }
  if (ui.demo) {
    toast(`Demo · would resume table ${code}`);
    return;
  }
  if (code === ui.session?.code && ui.state) {
    if (ui.gate) hideGate();
    return;
  }
  resumeTable(code, { from: "manual" });
});

el.dock.addEventListener("click", (event) => {
  const target = event.target.closest("button");
  const overlay = ui.overlay;
  if (!target || !docked()) return;
  if (target.dataset.cmd === "close") closeOverlay();
  else if (overlay.kind === "loan") loanClick(overlay, target);
  else if (overlay.kind === "property" && target.dataset.act) perform(overlay.seatId, JSON.parse(target.dataset.act));
});

// Property drawer: whichever deed has focus (keys, pad or a click) is framed on the board.
el.dock.addEventListener("focusin", () => {
  if (ui.overlay?.kind !== "property" || !ui.state) return;
  const deed = focusedDeed();
  board.highlightSquares(deed === null ? [] : [deed], "focus");
  if (!ui.draining) cameraToState(ui.state);
});

el.mute.addEventListener("click", () => toggleSoundMenu());
el.menuBtn?.addEventListener("click", () => openMenu());

// ---------------------------------------------------------------------------------------------
// Sound button and its quick menu (sound on/off, music, effects), the MENU (resume, settings, how
// to play, leave, end game) and this screen's settings.

function renderMute() {
  const settings = { ...sound.getSettings(), locked: sound.audioLocked() };
  const off = settings.muted || (!settings.musicOn && !settings.effectsOn);
  el.mute.setAttribute("aria-pressed", String(off));
  el.mute.setAttribute("aria-haspopup", "dialog");
  el.mute.setAttribute("aria-label", views.soundLabel(settings));
  el.mute.classList.add("sound-btn");
  el.mute.innerHTML = off || settings.locked ? views.ICONS.speakerOff : views.ICONS.speaker;
  if (el.menuBtn) el.menuBtn.innerHTML = views.ICONS.menu;
  const lobbyButton = el.join.querySelector(".join__head .sound-btn");
  if (lobbyButton && lobbyButton !== document.activeElement) lobbyButton.outerHTML = views.soundButtonHtml(settings);
  renderSoundPill(settings);
}

// The browser keeps this screen silent until someone clicks or presses a key on it (review F1: a
// game started from the phones stayed silent all evening, music included). Until then a gold pill
// under the sound button says so; the first click or key press anywhere unlocks the sound
// (sound.installAudioUnlock) and the pill goes. Not shown when the sound is turned off anyway.
function renderSoundPill(settings = { ...sound.getSettings(), locked: sound.audioLocked() }) {
  const show = views.soundPillShown(settings);
  if (show && !el.soundPill.innerHTML) el.soundPill.innerHTML = views.soundPillHtml();
  el.soundPill.hidden = !show;
  el.stage.classList.toggle("has-sound-pill", show);
}

function renderSoundMenu() {
  el.soundMenu.hidden = !ui.soundMenu;
  el.soundMenu.classList.toggle("is-lobby", !ui.state || ui.state.status === "lobby");
  if (!ui.soundMenu) {
    morph(el.soundMenu, "");
    return;
  }
  const active = document.activeElement;
  const keep = el.soundMenu.contains(active) ? (active.dataset.sound ? `[data-sound="${active.dataset.sound}"]` : active.dataset.volume ? `[data-volume="${active.dataset.volume}"]` : null) : null;
  morph(el.soundMenu, views.soundMenuHtml(sound.getSettings()));
  if (keep) el.soundMenu.querySelector(keep)?.focus({ preventScroll: true });
}

function toggleSoundMenu(open = !ui.soundMenu) {
  ui.soundMenu = open;
  renderSoundMenu();
  if (open) el.soundMenu.querySelector("[data-sound]")?.focus({ preventScroll: true });
}

function soundToggle(which) {
  const settings = sound.getSettings();
  if (which === "all" || which === "sound") sound.setMuted(!settings.muted);
  else if (which === "music") music.setEnabled(!settings.musicOn);
  else if (which === "effects") sound.setEffectsEnabled(!settings.effectsOn);
  sound.play("tap");
}

// Range inputs (sound menu and SETTINGS): change the volume without redrawing under the pointer.
function volumeInput(target) {
  const value = Number(target.value) / 100;
  ui.sliding = true;
  try {
    if (target.dataset.volume === "music") music.setVolume(value);
    else sound.setEffectsVolume(value);
  } finally {
    ui.sliding = false;
  }
}

for (const root of [el.soundMenu, el.overlay]) {
  root.addEventListener("input", (event) => {
    if (event.target.dataset?.volume) volumeInput(event.target);
  });
  root.addEventListener("change", (event) => {
    if (event.target.dataset?.volume === "effects") sound.play("coins", { amount: 20 });
  });
}

el.soundMenu.addEventListener("click", (event) => {
  const button = event.target.closest("[data-sound]");
  if (button) soundToggle(button.dataset.sound);
});

document.addEventListener("pointerdown", (event) => {
  if (ui.soundMenu && !el.soundMenu.contains(event.target) && !event.target.closest?.(".sound-btn")) toggleSoundMenu(false);
});

sound.onSettingsChange(() => {
  renderMute();
  if (ui.sliding) return;
  if (ui.soundMenu) renderSoundMenu();
  if (ui.overlay?.kind === "menu" && ui.overlay.page === "settings") render();
});

function openMenu() {
  if (!ui.state && !ui.demo) return;
  if (ui.soundMenu) toggleSoundMenu(false);
  sound.play("tap");
  openOverlay({ kind: "menu", page: "main" });
}

function showMenuPage(page) {
  ui.overlay = { kind: "menu", page };
  ui.focusOverlayNext = true;
  render();
}

function isEstateState(state) {
  return Boolean(state) && !isLudoState(state) && !isPokerState(state) && !isQuizState(state) && !isClosestState(state);
}

// The host's Players controls (UX-02): the table screen is the host of its room (it sends its
// tableToken); an Estate game in play. The server checks too.
function canHost() {
  return ui.state?.viewerRole === "table" && ui.state?.status === "playing" && isEstateState(ui.state);
}

// The table (not a watcher) can end a game in play; the server checks too.
function canEndGame() {
  return ui.state?.viewerRole === "table" && ui.state?.status === "playing";
}

function settingsCtx() {
  return {
    computerSpeed: ui.state?.roomSettings?.computerSpeed || "normal",
    botOffers: ui.state?.roomSettings?.botOffers || "normal",
    listed: ui.state?.roomSettings && "listed" in ui.state.roomSettings ? Boolean(ui.state.roomSettings.listed) : null,
    canComputerSpeed: ui.state?.viewerRole === "table" && (Boolean(ui.state?.roomSettings) || Boolean(ui.demo)),
    fullscreen: Boolean(document.fullscreenElement),
    fullscreenSupported: Boolean(document.documentElement.requestFullscreen),
    install: installState(),
    // The computer itself asks for reduced motion (REDUCED MOTION = SYSTEM): the camera is off.
    systemReduced: ui.screen.motion === "system" && Boolean(globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches),
  };
}

function menuPageHtml(state, page) {
  if (isClosestState(state) && page === "settings") return closestSettingsOverlayHtml(sound.getSettings(), ui.screen, settingsCtx());
  if (isClosestState(state) && page === "help") return closestHelpOverlayHtml(state);
  if (isQuizState(state) && page === "settings") return quizSettingsOverlayHtml(sound.getSettings(), ui.screen, settingsCtx());
  if (isQuizState(state) && page === "help") return quizHelpOverlayHtml(state);
  if (isLudoState(state) && page === "settings") return ludoSettingsOverlayHtml(sound.getSettings(), ui.screen, settingsCtx());
  if (isLudoState(state) && page === "help") return ludoHelpOverlayHtml(state);
  if (isPokerState(state) && page === "settings") return pokerSettingsOverlayHtml(sound.getSettings(), pokerTable.prefs(), { ...settingsCtx(), ...ui.screen });
  if (isPokerState(state) && page === "help") return pokerHelpOverlayHtml(state, { fourColor: Boolean(state.settings?.fourColorDeck) });
  if (page === "settings") return views.settingsOverlayHtml(sound.getSettings(), ui.screen, settingsCtx());
  if (page === "help") return views.helpOverlayHtml(state);
  if (page === "controls") return views.controlsOverlayHtml();
  if (page === "players" && canHost()) return views.playersOverlayHtml(state, { now: serverNow(), open: ui.overlay?.hostPick });
  return views.menuOverlayHtml(state, { watching: state.viewerRole !== "table", canEnd: canEndGame(), controls: isEstateState(state), host: canHost() });
}

function applyScreenSettings() {
  motion.setMotionOverride(ui.screen.motion);
  motion.setAnimationSpeed(ui.screen.speed);
}

function setScreen(change) {
  ui.screen = saveScreen({ ...ui.screen, ...change });
  applyScreenSettings();
  if (ui.state && ui.state.status !== "lobby") cameraToState(ui.state);
}

async function setComputerSpeed(value) {
  if (!COMPUTER_SPEEDS.includes(value)) return;
  if (ui.demo) {
    toast(`Demo · would set computer speed: ${value}`);
    return;
  }
  try {
    const state = await ui.client.roomSettings(ui.session.code, ui.session.token, { computerSpeed: value });
    if (state?.version !== undefined) ui.live?.accept(state);
  } catch (error) {
    toast(plainError(error.message) || error.message, { error: true });
  }
}

// COMPUTER OFFERS TO PEOPLE: normal (the server's rules), fewer, or off (POST /settings).
async function setBotOffers(value) {
  if (!["normal", "fewer", "off"].includes(value)) return;
  if (ui.demo) {
    toast(`Demo · would set computer offers to people: ${value}`);
    return;
  }
  try {
    const state = await ui.client.roomSettings(ui.session.code, ui.session.token, { botOffers: value });
    if (state?.version !== undefined) ui.live?.accept(state);
  } catch (error) {
    toast(plainError(error.message) || error.message, { error: true });
  }
}

// PUBLIC GAME: list this game for everyone on the server (off by default; POST /settings).
async function setRoomListed(listed) {
  if (ui.demo) {
    toast(`Demo · would set public game: ${listed ? "on" : "off"}`);
    return;
  }
  try {
    const state = await ui.client.roomSettings(ui.session.code, ui.session.token, { listed });
    if (state?.version !== undefined) ui.live?.accept(state);
  } catch (error) {
    toast(plainError(error.message) || error.message, { error: true });
  }
}

function toggleFullscreen() {
  if (document.fullscreenElement) document.exitFullscreen?.().catch(() => {});
  else document.documentElement.requestFullscreen?.().catch(() => toast("Full screen isn't available here", { error: true }));
}

document.addEventListener("fullscreenchange", () => {
  if (ui.overlay?.kind === "menu" && ui.overlay.page === "settings") render();
});

function settingClick(target) {
  const value = target.dataset.value;
  switch (target.dataset.set) {
    case "sound":
    case "music":
    case "effects":
      soundToggle(target.dataset.set);
      return;
    case "camera": setScreen({ camera: value }); break;
    case "zoom": setScreen({ zoom: Number(value) }); break;
    case "speed": setScreen({ speed: value }); break;
    case "motion": setScreen({ motion: value }); break;
    case "computerSpeed": setComputerSpeed(value); break;
    case "botOffers": setBotOffers(value); break;
    case "listed": setRoomListed(!ui.state?.roomSettings?.listed); break;
    case "fullscreen": toggleFullscreen(); break;
    case "install": promptInstall().then(() => render()); break;
    default: return;
  }
  sound.play("tap");
  render();
}

async function menuClick(overlay, target) {
  const cmd = target.dataset.cmd;
  if (cmd === "menu-settings") return showMenuPage("settings");
  if (cmd === "menu-help") return showMenuPage("help");
  if (cmd === "menu-back") return showMenuPage("main");
  if (cmd === "menu-controls") return showMenuPage("controls");
  if (cmd === "menu-players") return showMenuPage("players");
  if (target.dataset.hostPick) {
    ui.overlay.hostPick = target.dataset.hostPick;
    sound.play("tap");
    return render();
  }
  if (target.dataset.host) return hostAction(target.dataset.host, target.dataset.arg);
  if (cmd === "menu-new") {
    closeOverlay();
    return askNewTable();
  }
  if (cmd === "menu-bankrupt") return askBankrupt(ui.state?.localSeats?.find((seat) => seat.seatId === target.dataset.arg));
  if (cmd === "menu-leave") {
    return openOverlay({
      kind: "confirm", seatId: null,
      title: "LEAVE THIS TABLE?",
      text: "This screen goes back to the start. The game stays on the server: show it again from ACTIVE GAMES (SHOW HERE) or with its code.",
      confirm: "LEAVE TABLE",
      onConfirm: leaveTable,
    });
  }
  if (cmd === "menu-end") {
    if (!canEndGame()) return undefined;
    return openOverlay({ kind: "confirm", seatId: null, html: isClosestState(ui.state) ? closestEndConfirmHtml(ui.state) : isQuizState(ui.state) ? quizEndConfirmHtml(ui.state) : isLudoState(ui.state) ? ludoEndConfirmHtml(ui.state) : isPokerState(ui.state) ? pokerEndConfirmHtml(ui.state) : views.endGameConfirmHtml(ui.state), onConfirm: endGame });
  }
  if (String(target.dataset.set || "").startsWith("pk") && pokerTable.setting(target)) return undefined;
  if (target.dataset.set) return settingClick(target);
  return undefined;
}

// Players (UX-02): Computer plays and Skip turn go at once; Remove asks first (deeds, houses and
// cash go to the bank, the seat closes).
function hostAction(kind, playerId) {
  if (!canHost() || !playerId) return undefined;
  if (kind === "remove") {
    return openOverlay({ kind: "confirm", seatId: null, html: views.removeConfirmHtml(ui.state, playerId, { now: serverNow() }), onConfirm: () => sendHost("remove", playerId) });
  }
  return sendHost(kind, playerId);
}

async function sendHost(kind, playerId) {
  const name = ui.state?.players?.find((player) => player.id === playerId)?.name || "";
  if (ui.demo) {
    toast(`Demo · would send ${kind} for ${name}`);
    return;
  }
  try {
    const state = await ui.client.hostControl(ui.session.code, ui.session.token, kind, playerId);
    if (state?.version !== undefined) ui.live?.accept(state);
    sound.play("tap");
  } catch (error) {
    toast(error.message, { error: true });
  }
}

// A / X / Y on the Players page press the open row's COMPUTER PLAYS / SKIP TURN / REMOVE.
function hostKey(button) {
  if (ui.overlay?.kind !== "menu" || ui.overlay.page !== "players") return false;
  const kind = { A: "standin", X: "skip", Y: "remove" }[button];
  const node = kind ? el.overlay.querySelector(`.host-row.is-open [data-host="${kind}"]:not([disabled])`) : null;
  if (!node) return false;
  node.click();
  return true;
}

// Leave: forget this screen's table (no auto-reopen) and start fresh; the game carries on.
async function leaveTable() {
  if (ui.demo) {
    toast("Demo · would leave this table (the game stays on the server)");
    return;
  }
  ui.live?.stop();
  ui.live = null;
  storage.set(SESSION_KEY, null);
  ui.victoryShownFor = null;
  await newTable();
}

async function endGame() {
  if (ui.demo) {
    toast("Demo · would end the game (the richest player wins)");
    return;
  }
  try {
    const state = await ui.client.endGame(ui.session.code, ui.session.token);
    if (state?.version !== undefined) ui.live?.accept(state);
  } catch (error) {
    toast(plainError(error.message) || error.message, { error: true });
  }
}

// An offer to a seat at this screen opens big and central by itself (ACCEPT / DECLINE). Esc or B
// puts it away for this offer; the action card still has ACCEPT / DECLINE.
function syncOfferModal(state) {
  const trade = state?.phase === "trade" ? state.trade : null;
  const seat = trade && state.viewerRole === "table" ? state.localSeats?.find((candidate) => candidate.playerId === trade.targetId) : null;
  if (ui.overlay?.kind === "offer" && (!seat || ui.overlay.tradeId !== trade.id)) ui.overlay = null;
  if (seat && !ui.overlay && !ui.gate && ui.offerDismissed !== trade.id) {
    ui.overlay = { kind: "offer", seatId: seat.seatId, tradeId: trade.id };
    ui.focusOverlayNext = true;
  }
}

async function answerOffer(overlay, accept) {
  const seatId = overlay?.seatId;
  if (!seatId) return;
  const ok = await perform(seatId, { type: accept ? "accept_trade" : "reject_trade" });
  if (ok && ui.overlay === overlay) {
    ui.offerDismissed = overlay.tradeId;
    closeOverlay();
  }
}

// CHANGE on a computer's counter-offer: decline it and open the trade builder with its terms, to
// edit and send back.
async function changeCounter(overlay) {
  const seatId = overlay?.seatId;
  const trade = ui.state?.trade;
  const seat = ui.state?.localSeats?.find((candidate) => candidate.seatId === seatId);
  if (!seatId || !trade?.counterTo || !seat) return;
  const draft = { ...views.newTradeDraft(), targetId: trade.proposerId,
    offer: [...(trade.request?.properties || [])], offerCash: trade.request?.cash || 0,
    request: [...(trade.offer?.properties || [])], requestCash: trade.offer?.cash || 0 };
  const ok = await perform(seatId, { type: "reject_trade" });
  if (!ok) return;
  ui.offerDismissed = overlay.tradeId;
  openOverlay({ kind: "trade", seatId, draft });
  refreshQuote(ui.overlay);
  render();
}

// NOT SELLING <deed> on a computer's offer: no computer asks for it again until the deed card
// turns it back on; the offer is declined.
async function notSelling(overlay, index) {
  const seatId = overlay?.seatId;
  if (!seatId || !Number.isInteger(index)) return;
  if (ui.demo) toast("Demo · would stop computers asking for this deed");
  else {
    try {
      const state = await ui.client.notSelling(ui.session.code, ui.session.token, index, true, seatId);
      if (state?.version !== undefined) ui.live?.accept(state);
    } catch (error) {
      toast(plainError(error.message) || error.message, { error: true });
      return;
    }
  }
  await answerOffer(overlay, false);
}

// ---------------------------------------------------------------------------------------------
// Keyboard

function rowsFor(predicate) {
  return ui.panelRows.filter((row) => (row.act || row.cmd) && predicate(row));
}

function runFirst(rows) {
  if (!rows.length) return false;
  runRow(rows[0]);
  return true;
}

function defaultAction() {
  const state = ui.state;
  if (!state) return;
  if (state.status === "lobby") {
    if (isPokerState(state) || isQuizState(state) || isClosestState(state)) return; // poker, the quiz and Near Thing are phones only: no keyboard or controller seats
    openSeatSetup("keyboard");
    return;
  }
  runFirst(rowsFor(() => true));
}

const CASH_KEYS = { "+": 10, "=": 10, "-": -10, _: -10, "]": 50, "[": -50 };

document.addEventListener("keydown", (event) => {
  setInputMode("keys");
  sound.unlockAudio();
  const state = ui.state;
  const key = event.key;
  const typing = event.target instanceof HTMLInputElement;
  if (key === "Escape") {
    event.preventDefault();
    if (ui.deedView !== null) closeDeedView();
    else if (ui.soundMenu) toggleSoundMenu(false);
    else if (ui.gate && ui.state) hideGate();
    else if (ui.overlay?.kind === "menu" && ui.overlay.page !== "main") showMenuPage("main");
    else if (ui.overlay) closeOverlay();
    // The one key map (UX-01): Esc is B, back (pass, not yet); with nothing to go back from, the menu.
    else if (ui.state && ui.state.status === "playing" && !ui.busy && !isLudoState(ui.state) && !isPokerState(ui.state) && !isQuizState(ui.state) && !isClosestState(ui.state) && runFirst(rowsFor((row) => row.key === "B"))) return;
    else if (ui.state) openMenu();
    else document.activeElement?.blur?.();
    return;
  }
  if (ui.deedView !== null) {
    // Arrows move between the card's actions; Enter / Space presses the focused one (CLOSE too).
    const direction = { ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right" }[key];
    if (direction) {
      event.preventDefault();
      moveFocus(direction);
    } else if (key === "Enter" || key === " ") {
      event.preventDefault();
      if (!activateFocused()) closeDeedView();
    }
    return;
  }
  // I: the full card of the deed with focus (board square, drawer card, trade row…).
  if (!event.ctrlKey && !event.metaKey && !event.altKey && key.toUpperCase() === "I" && !(event.target instanceof HTMLInputElement)) {
    if (openDeedFromFocus()) {
      event.preventDefault();
      return;
    }
  }
  if (ui.overlay?.kind === "menu" && !typing && !event.ctrlKey && !event.metaKey && !event.altKey && ["A", "X", "Y"].includes(key.toUpperCase()) && hostKey(key.toUpperCase())) {
    event.preventDefault();
    return;
  }
  if (ui.overlay?.kind === "offer" && !event.ctrlKey && !event.metaKey && !event.altKey && key.toUpperCase() === "X") {
    event.preventDefault();
    answerOffer(ui.overlay, false);
    return;
  }
  if (ui.gate) {
    if (typing) {
      if (key === "ArrowDown" || key === "ArrowUp") {
        event.preventDefault();
        moveFocus(key === "ArrowDown" ? "down" : "up");
      }
      return;
    }
    const direction = { ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right" }[key];
    if (direction) {
      event.preventDefault();
      moveFocus(direction);
    }
    return;
  }
  // Ludo: the keyboard seat to act rolls, picks a piece and moves it (ludo-table.js).
  if (isLudoState(state) && !ui.overlay && !ui.soundMenu && !typing && !event.ctrlKey && !event.metaKey && !event.altKey && ludoTable.key(event)) {
    event.preventDefault();
    return;
  }
  if (ui.overlay?.kind === "trade" && !typing && !event.ctrlKey && !event.metaKey && !event.altKey) {
    if (key in CASH_KEYS) {
      event.preventDefault();
      tradeCash(ui.overlay, focusedTradeField(), CASH_KEYS[key]);
      return;
    }
    if (key.toUpperCase() === "S") {
      event.preventDefault();
      sendTrade(ui.overlay);
      return;
    }
    if (key.toUpperCase() === "Y") {
      event.preventDefault();
      applyQuote(ui.overlay);
      return;
    }
  }
  if (ui.overlay?.kind === "loan" && !typing && key.toUpperCase() === "S") {
    event.preventDefault();
    el.dock.querySelector('[data-cmd="loan-send"]:not([disabled])')?.click();
    return;
  }
  if (typing) {
    if (key === "ArrowDown" || key === "ArrowUp") {
      event.preventDefault();
      moveFocus(key === "ArrowDown" ? "down" : "up");
    }
    return;
  }
  const direction = { ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right" }[key];
  if (direction) {
    event.preventDefault();
    moveFocus(direction);
    return;
  }
  if (key === "Enter" || key === " ") {
    const active = document.activeElement;
    if (active?.matches?.("button") && scopeEl().contains(active)) return;
    event.preventDefault();
    if (!ui.overlay) defaultAction();
    else activateFocused();
    return;
  }
  if (event.ctrlKey || event.metaKey || event.altKey || ui.overlay || ui.soundMenu || !state) return;
  if (state.status === "lobby") return;
  if (isLudoState(state) || isQuizState(state) || isClosestState(state)) return; // Estate's letter keys (drawers, bankruptcy) don't apply
  const upper = key.toUpperCase();
  if (/^[1-9]$/.test(key)) {
    const rows = rowsFor(() => true);
    const row = ui.panelRows.filter((candidate) => candidate.act || candidate.cmd)[Number(key) - 1];
    if (row) runRow(row);
    else if (!rows.length) toast("Nothing to choose right now.");
    return;
  }
  if (key in CASH_KEYS) {
    const seat = views.actingSeats(state)[0];
    if (seat) adjustBid(seat, CASH_KEYS[key]);
    return;
  }
  // The one key map (UX-01): A main, X second, Y third, B back, V deeds, M menu; Q bankruptcy.
  if (upper === "M") {
    openMenu();
    return;
  }
  if (["A", "X", "Y", "B", "V", "Q"].includes(upper)) {
    const matched = runFirst(rowsFor((row) => row.key === upper));
    if (!matched && upper === "V") {
      const seat = state.localSeats?.find((candidate) => candidate.input === "keyboard") || state.localSeats?.[0];
      if (seat) openOverlay({ kind: "property", seatId: seat.seatId });
    }
    // Q: declare bankruptcy at any time (the keyboard seat), after the confirmation.
    if (!matched && upper === "Q") askBankrupt(state.localSeats?.find((candidate) => candidate.input === "keyboard" && views.canResign(state, candidate.playerId)));
  }
});

document.addEventListener("pointerdown", () => {
  setInputMode("pointer");
  sound.unlockAudio();
}, { passive: true });

// ---------------------------------------------------------------------------------------------
// Gamepads (standard mapping): A confirm, B back/pass, X/Y extra rows, D-pad or stick to move
// focus, Start/Menu property, RB/RT +$10/+$50 (bids, trade cash), LB/LT −.

const BUTTONS = [[0, "A"], [1, "B"], [2, "X"], [3, "Y"], [4, "LB"], [5, "RB"], [6, "LT"], [7, "RT"], [8, "BACK"], [9, "START"]];
const PAD_CASH = { RB: 10, RT: 50, LB: -10, LT: -50 };

function padName(id = "") {
  if (/xbox|xinput|045e/i.test(id)) return "Xbox controller";
  if (/dualsense|dualshock|playstation|054c/i.test(id)) return "PlayStation controller";
  if (/switch|057e/i.test(id)) return "Switch controller";
  return "Controller";
}

function pollPads() {
  const pads = navigator.getGamepads ? navigator.getGamepads() : [];
  const now = performance.now();
  for (const pad of pads) {
    if (!pad) continue;
    if (!ui.padNames[pad.index]) {
      ui.padNames[pad.index] = padName(pad.id);
      if (ui.state?.status === "lobby") render();
    }
    const memory = ui.pads.get(pad.index) || { buttons: [], dir: null, repeatAt: 0 };
    const pressed = (index) => Boolean(pad.buttons[index]?.pressed);
    const axisX = pad.axes[0] || 0;
    const axisY = pad.axes[1] || 0;
    let direction = null;
    if (pressed(12) || axisY < -0.6) direction = "up";
    else if (pressed(13) || axisY > 0.6) direction = "down";
    else if (pressed(14) || axisX < -0.6) direction = "left";
    else if (pressed(15) || axisX > 0.6) direction = "right";
    if (direction && (direction !== memory.dir || now >= memory.repeatAt)) {
      padInput(pad.index, direction);
      memory.repeatAt = now + (direction !== memory.dir ? 380 : 140);
    }
    memory.dir = direction;
    for (const [index, name] of BUTTONS) {
      if (pressed(index) && !memory.buttons[index]) padInput(pad.index, name);
    }
    memory.buttons = pad.buttons.map((button) => button.pressed);
    ui.pads.set(pad.index, memory);
  }
  requestAnimationFrame(pollPads);
}

window.addEventListener("gamepadconnected", (event) => {
  ui.padNames[event.gamepad.index] = padName(event.gamepad.id);
  toast(`${padName(event.gamepad.id)} connected`);
  render();
});

window.addEventListener("gamepaddisconnected", (event) => {
  delete ui.padNames[event.gamepad.index];
  ui.pads.delete(event.gamepad.index);
  render();
});

function padInput(index, button) {
  setInputMode("keys");
  sound.unlockAudio();
  // Ludo: the controller seat to act rolls, picks a piece and moves it (ludo-table.js).
  if (!ui.gate && !ui.overlay && ui.deedView === null && isLudoState(ui.state) && ludoTable.pad(index, button)) return;
  if (["up", "down", "left", "right"].includes(button)) {
    moveFocus(button);
    return;
  }
  if (ui.gate) {
    if (button === "A") activateFocused();
    else if (button === "B" && ui.state) hideGate();
    return;
  }
  const state = ui.state;
  if (!state) return;
  const seat = seatForPad(index);
  if (ui.deedView !== null) {
    // A presses the focused action (or CLOSE); B closes. The D-pad moves (padInput, above).
    if (button === "A" && !activateFocused()) closeDeedView();
    else if (button === "B") closeDeedView();
    return;
  }
  // Y on a focused deed shows its card where Y isn't already taken (drawers, the offer).
  if (button === "Y" && ["property", "loan", "offer"].includes(ui.overlay?.kind) && openDeedFromFocus()) return;
  if (button === "START") {
    if (ui.overlay?.kind === "menu") closeOverlay();
    else if (!ui.overlay || !["seat", "confirm"].includes(ui.overlay.kind)) openMenu();
    return;
  }
  if (ui.overlay) {
    if ((button === "X" || button === "Y") && hostKey(button)) return;
    if (button === "A") activateFocused();
    else if (button === "B" && ui.overlay.kind === "menu" && ui.overlay.page !== "main") showMenuPage("main");
    else if (button === "B") closeOverlay();
    else if (ui.overlay.kind === "offer" && button === "X") answerOffer(ui.overlay, false);
    else if (ui.overlay.kind === "trade") {
      if (button === "X") sendTrade(ui.overlay);
      else if (button === "Y") applyQuote(ui.overlay);
      else if (PAD_CASH[button]) tradeCash(ui.overlay, focusedTradeField(), PAD_CASH[button]);
    } else if (ui.overlay.kind === "loan" && button === "X") el.dock.querySelector('[data-cmd="loan-send"]:not([disabled])')?.click();
    return;
  }
  // Poker, the quiz and Near Thing are phones only: a controller just moves focus and presses buttons.
  if (isPokerState(state) || isQuizState(state) || isClosestState(state)) {
    if (button === "A") activateFocused();
    return;
  }
  // Ludo in play: a pad that isn't to act presses focused buttons only (ludoTable.pad had the rest).
  if (isLudoState(state) && state.status !== "lobby") {
    if (button === "A") activateFocused();
    return;
  }
  if (state.status === "lobby") {
    if (button === "A") {
      if (!activateFocused() && !seat) openSeatSetup("controller", index);
    }
    return;
  }
  if (!seat) {
    if (button === "A") activateFocused();
    return;
  }
  if (ui.busy) return;
  const mine = (row) => row.seatId === seat.seatId;
  const focusedRow = document.activeElement?.dataset?.row !== undefined ? ui.panelRows[Number(document.activeElement.dataset.row)] : null;
  switch (button) {
    case "A":
      if (document.activeElement?.dataset?.debtTick !== undefined && el.panel.contains(document.activeElement)) document.activeElement.click();
      else if (focusedRow && (mine(focusedRow) || !focusedRow.seatId)) runRow(focusedRow);
      else runFirst(rowsFor(mine));
      break;
    case "B":
    case "X":
    case "Y":
      if (!runFirst(rowsFor((row) => mine(row) && row.button === button)) && button === "B") document.activeElement?.blur?.();
      break;
    case "BACK":
      openOverlay({ kind: "property", seatId: seat.seatId });
      break;
    case "RB": case "RT": case "LB": case "LT":
      adjustBid(seat, PAD_CASH[button]);
      break;
    default: break;
  }
}

// ---------------------------------------------------------------------------------------------
// State arrival → animations → settle

function onState(next) {
  // [seat-approval hook] the table answers requests to move a seat (or this screen) to a new
  // device; each shows a 4-digit code for a host who lost their phone (state.claims).
  if (!ui.demo) showClaimPrompts(next, { client: ui.client, code: ui.session?.code, token: ui.session?.token });
  ui.queue.push({ state: next, events: ui.pendingEvents.splice(0) });
  if (!ui.draining) drain();
}

async function drain() {
  ui.draining = true;
  try {
    while (ui.queue.length) {
      const { state: next, events } = ui.queue.shift();
      const prev = ui.state;
      // The quiz never waits: each state goes on screen as it arrives (docs/QUIZ-API.md §9).
      if (isQuizState(next)) {
        ui.state = next;
        if (!ui.queue.length) ui.busy = false;
        quizTable.commit(prev, next);
        continue;
      }
      // Near Thing never waits either (docs/CLOSEST-API.md §8): its line-up and reveal play
      // inside the phase time.
      if (isClosestState(next)) {
        ui.state = next;
        if (!ui.queue.length) ui.busy = false;
        closestTable.commit(prev, next);
        continue;
      }
      if (isLudoState(next)) {
        ui.state = next;
        if (!ui.queue.length) ui.busy = false;
        await ludoTable.commit(prev, next);
        continue;
      }
      if (isPokerState(next)) {
        ui.state = next;
        if (!ui.queue.length) ui.busy = false;
        pokerTable.commit(prev, next);
        continue;
      }
      const behind = ui.queue.length > 2;
      const receipts = { list: receiptsFor(prev, next), shown: new Set() };
      let snap = false;
      if (prev && !behind && !document.hidden && prev.status !== "lobby" && next.status !== "lobby" && !el.game.hidden) {
        const list = events.length ? events : diffEvents(prev, next);
        if (list.length) {
          setBusy(true);
          // Animations wait on browser frames. A minimised or covered window produces none, so
          // never let them hold the table hostage: after a budget, jump to the latest state.
          const budget = ANIMATION_BUDGET_BASE_MS + list.length * ANIMATION_BUDGET_PER_EVENT_MS + list.reduce((ms, event) => ms + (event.type === "card" ? cardReadMs(event.text) : 0), 0);
          let timedOut = false;
          try {
            await Promise.race([
              playEvents(prev, next, list, receipts),
              motion.wait(budget).then(() => { timedOut = true; }),
            ]);
          } catch (error) {
            console.warn("Animation skipped:", error);
          }
          if (timedOut) {
            cancelBoardEffects();
            snap = true;
          }
        }
      }
      ui.state = next;
      if (!ui.queue.length) ui.busy = false;
      if (ui.bid && next.auction && ui.bid.key !== `${next.auction.spaceIndex}:${next.auction.currentBid}`) ui.bid = null;
      if (ui.overlay?.kind === "trade") refreshQuote(ui.overlay);
      render();
      // A skipped or cut-short animation snaps the camera to where this state wants it.
      if (next.status !== "lobby") cameraToState(next, { instant: snap || behind || document.hidden });
      if (next.status !== "lobby") showReceipts(receipts, () => true);
      if (!behind && !document.hidden) {
        const local = (next.localSeats || []).map((seat) => seat.playerId);
        for (const cue of soundCues(prev, next, receipts.list, local)) sound.play(cue.name, cue.opts);
      }
      if (next.status === "finished" && ui.victoryShownFor !== next.code) {
        ui.victoryShownFor = next.code;
        openOverlay({ kind: "victory" });
      }
    }
  } finally {
    ui.draining = false;
    if (ui.busy) setBusy(false);
  }
}

// ---------------------------------------------------------------------------------------------
// Outcome chips on the plates: who paid whom, PAYDAY +$200, tax, auction wins, trades. They are the
// only "what just happened" on the side, and they fade. One at a time per plate, in the empty
// right-hand side of the plate beside the cash (review U2: stacked chips used to sit on the cash,
// the OUT badge and the jail lock); a second outcome waits for the first to be read.

const CHIP_LIFE_MS = 2800;
const CHIP_MIN_MS = 1300;
const chipLanes = new Map(); // playerId → { node, shownAt, queue: [] }

function chipLabel(receipt, state) {
  const other = upperName(playerById(state, receipt.otherId)?.name);
  switch (receipt.kind) {
    case "rent": return receipt.amount < 0 ? `→ ${other}` : `← ${other}`;
    case "trade": return "✓ DEAL";
    case "declined": return "✕ DECLINED";
    case "loan": return receipt.amount < 0 ? `LENT · ${other}` : receipt.label;
    default: return receipt.label;
  }
}

function upperName(name) {
  return String(name || "").toUpperCase();
}

function plateChip(receipt) {
  if (!el.plateFx) return;
  const lane = chipLanes.get(receipt.playerId) || { node: null, shownAt: 0, queue: [] };
  chipLanes.set(receipt.playerId, lane);
  lane.queue.push(receipt);
  if (lane.queue.length === 1) pumpChips(receipt.playerId);
}

function pumpChips(playerId) {
  const lane = chipLanes.get(playerId);
  if (!lane?.queue.length) return;
  const life = motion.animationSpeed();
  const wait = lane.node?.isConnected ? lane.shownAt + CHIP_MIN_MS * life - performance.now() : 0;
  if (wait > 0) {
    clearTimeout(lane.timer);
    lane.timer = setTimeout(() => pumpChips(playerId), wait);
    return;
  }
  const receipt = lane.queue.shift();
  const plate = el.plates.querySelector(`.plate[data-player-id="${CSS.escape(playerId)}"]`);
  lane.node?.remove();
  lane.node = null;
  if (plate) {
    const chip = document.createElement("div");
    const tone = receipt.amount > 0 ? "pos" : receipt.amount < 0 || receipt.kind === "declined" ? "neg" : "info";
    chip.className = `plate-chip plate-chip--${tone}`;
    const label = String(chipLabel(receipt, ui.state)).replace(/[<>&"]/g, "");
    const amount = receipt.amount ? `${receipt.amount > 0 ? "+" : "−"}${money(Math.abs(receipt.amount))}` : "";
    chip.innerHTML = amount ? `<b>${amount}</b><span>${label}</span>` : `<b>${label}</b>`;
    // FAST animations (settings) shorten the chips too.
    chip.style.animationDuration = `${((CHIP_LIFE_MS * life) / 1000).toFixed(2)}s`;
    chip.style.visibility = "hidden";
    el.plateFx.append(chip);
    // Right-aligned inside the plate, level with the cash (below the name), never over it.
    chip.style.right = `${el.plateFx.offsetWidth - (plate.offsetLeft + plate.offsetWidth) + 14}px`;
    chip.style.top = `${plate.offsetTop + Math.max(4, plate.offsetHeight - chip.offsetHeight - 12)}px`;
    chip.style.visibility = "";
    lane.node = chip;
    lane.shownAt = performance.now();
    setTimeout(() => {
      chip.remove();
      if (lane.node === chip) lane.node = null;
    }, CHIP_LIFE_MS * life);
  }
  if (lane.queue.length) pumpChips(playerId);
}

function showReceipts(receipts, match) {
  if (!receipts) return;
  for (const receipt of receipts.list) {
    if (receipts.shown.has(receipt.key) || !match(receipt)) continue;
    receipts.shown.add(receipt.key);
    if (views.quietReceipt(receipt, ui.state)) continue;
    plateChip(receipt);
  }
}

const ANIMATION_BUDGET_BASE_MS = 4000;
const ANIMATION_BUDGET_PER_EVENT_MS = 2500;

// Stop one-shot animations that were cut short, so their `fill: forwards` can't fight the
// positions render() sets. Endless idle loops (token bob, turn glow) are left running.
function cancelBoardEffects() {
  for (const animation of el.board?.getAnimations?.({ subtree: true }) || []) {
    if (animation.effect?.getTiming?.().iterations !== Infinity) animation.cancel();
  }
  board.fx?.replaceChildren?.();
  if (board.slot) board.slot.dataset.html = "(dirty)";
  el.show.dataset.html = "(animated)";
  hideDice(true);
}

// Dice land top-centre of the stage (screen space: the board centre is often off-screen when the
// camera is zoomed) and fade a moment later.
function hideDice(now = false, afterMs = 2600) {
  clearTimeout(ui.diceTimer);
  if (now) {
    el.diceHud.classList.remove("is-shown");
    el.diceHud.innerHTML = "";
    return;
  }
  ui.diceTimer = setTimeout(() => el.diceHud.classList.remove("is-shown"), afterMs);
}

function withProp(state, index, prop) {
  return { ...state, properties: { ...state.properties, [index]: prop } };
}

function tokenPoint(playerId) {
  const token = board.tokenEl(playerId);
  if (!token) return null;
  return { x: Number(token.dataset.x), y: Number(token.dataset.y) - 8 };
}

async function playEvents(prev, next, events, receipts = null) {
  let shown = prev;
  board.update(shown, { currentSquare: null });
  board.clearHighlights();
  for (const event of events) {
    switch (event.type) {
      case "dice":
        sound.play("dice");
        clearTimeout(ui.diceTimer);
        el.diceHud.classList.add("is-shown");
        await motion.throwDice(el.diceHud, event.dice, { size: "die--lg" });
        // A double means another roll: say so on the dice (the turn panel says DOUBLES · ROLL AGAIN).
        const double = Array.isArray(event.dice) && event.dice.length === 2 && event.dice[0] === event.dice[1];
        if (double) el.diceHud.insertAdjacentHTML("beforeend", `<div class="dice-hud__tag">DOUBLES</div>`);
        hideDice(false, double ? 4200 : 2600);
        break;
      case "move": {
        // Square by square with a landing glow (the shared Board#hop the phones use too). FOLLOW
        // MOVES: the camera zooms in on the token (the ZOOM setting) and pans with it, one square
        // per step (linear, in time with the hop), then rests on the landing square; the money, card
        // or decision that comes next eases it back to the whole board (easeOut, cameraToState).
        // Otherwise (and for a jump to jail) the hop plays on the whole board.
        const path = event.path || [];
        const jail = Boolean(event.jail);
        const stepMs = Math.round((jail ? 420 : path.length > 14 ? 85 : 120) * motion.animationSpeed());
        const last = path.length - 1;
        // A move straight after this player's card ("go to …", "go back three spaces") is followed too.
        const before = events.slice(0, events.indexOf(event));
        const card = before.slice(before.map((item) => item.type).lastIndexOf("move") + 1).some((item) => item.type === "card" && item.playerId === event.playerId);
        const shots = await followStart(event.playerId, path, jail, card);
        if (shots) applyView(shots[1], panMs(stepMs, last === 1), "linear");
        else if (path.length > 1) wholeBoard();
        let stepped = false;
        await board.hop(event.playerId, path, {
          jail,
          stepMs,
          onStep: (index, isLast) => {
            stepped = true;
            if (shots && !isLast) applyView(shots[index + 1], panMs(stepMs, index + 1 === last), "linear");
            sound.play(isLast ? "land" : "hop", { step: index });
          },
        });
        // Reduced motion jumps without steps; the landing is still heard.
        if (!stepped && path.length > 1) sound.play("land");
        if (shots) {
          // Rest on the landing square. A big rent rests a little longer and stands in for the
          // close-up (released), so the camera doesn't zoom into the same square twice.
          const landing = next.lastLanding;
          const here = landing?.spaceIndex === path[last] && landing.playerId === event.playerId;
          const big = here && landing.kind === "rent" && landing.amount >= CLOSE_UP_RENT;
          if (here) ui.cam.released = cameraKey(next);
          await motion.wait(big ? FOLLOW_BIG_REST_MS : FOLLOW_REST_MS);
        }
        break;
      }
      case "pay": {
        await easeOut();
        const from = event.from ? tokenPoint(event.from) : null;
        const to = event.to ? tokenPoint(event.to) : null;
        const bank = { x: el.board.clientWidth / 2, y: el.board.clientHeight / 2 };
        showReceipts(receipts, (receipt) => [event.from, event.to].includes(receipt.playerId) && Math.abs(receipt.amount) === event.amount);
        if (event.reason === "go") {
          sound.play("go");
          if (to) motion.riseChip(board.fx, to, event.amount, { tone: "pos" });
          await motion.wait(500);
          break;
        }
        const big = event.amount >= 500;
        sound.play("coins", { amount: event.amount, collect: Boolean(event.to) });
        if (big) {
          sound.thud();
          motion.bigRentHit(el.board);
        }
        if (from) {
          motion.riseChip(board.fx, from, -event.amount, { tone: "neg" });
          motion.react(board.tokenEl(event.from), "wobble");
        }
        if (to) motion.react(board.tokenEl(event.to), "bounce");
        await motion.flyNotes(board.fx, from || bank, to || bank, event.amount);
        if (to) motion.riseChip(board.fx, to, event.amount, { tone: "pos" });
        await motion.wait(300);
        break;
      }
      case "card": {
        await easeOut();
        sound.play("card");
        // The card turns over above the action card, in view however the camera is zoomed, and
        // stays long enough to read (cardReadMs) before anything moves on. A card someone at the
        // table must confirm stays on its own until they do (slotHtml), so it doesn't hold here.
        const shownAt = performance.now();
        clearTimeout(ui.showTimer);
        el.show.classList.remove("is-leaving");
        el.show.hidden = false;
        await motion.flipCard(el.show, { deck: event.deck, text: event.text });
        el.show.dataset.html = "(animated)";
        const pending = next.phase === "card_confirmation" && next.pendingCard?.text === event.text ? playerById(next, next.pendingCard.playerId) : null;
        const waitsForPerson = pending && inputOf(pending) !== "ai";
        await motion.wait(waitsForPerson ? 600 : Math.max(0, shownAt + cardReadMs(event.text) - performance.now()));
        break;
      }
      case "buy": {
        sound.play("buy");
        clearTimeout(ui.showTimer);
        el.show.classList.remove("is-leaving");
        showReceipts(receipts, (receipt) => receipt.playerId === event.playerId && receipt.spaceIndex === event.spaceIndex);
        shown = withProp(shown, event.spaceIndex, next.properties[event.spaceIndex]);
        board.update(shown, { currentSquare: null });
        // SOLD lands on the square (in view: the camera rests there) and on the deed by the card.
        el.show.hidden = false;
        el.show.innerHTML = deedHtml(next, event.spaceIndex, { eyebrow: `${playerById(next, event.playerId)?.name || ""}'S NOW`.toUpperCase(), className: "deed--board" });
        el.show.dataset.html = "(animated)";
        await Promise.all([motion.stampCell(board.cell(event.spaceIndex)), motion.stampSold(el.show.querySelector(".deed"), { holdMs: 700 })]);
        break;
      }
      case "build": {
        shown = withProp(shown, event.spaceIndex, next.properties[event.spaceIndex]);
        board.update(shown, { currentSquare: null });
        const houses = board.cell(event.spaceIndex)?.querySelector(".cell__houses");
        const from = event.houses === 5 ? 0 : event.from || 0;
        for (let step = from; step < (event.houses === 5 ? 1 : event.houses); step += 1) setTimeout(() => sound.play(event.houses === 5 ? "hotel" : "house", { step }), (step - from) * 180);
        await motion.dropHouses(houses, from);
        break;
      }
      case "jail":
        sound.play("jail");
        await motion.slamBars(board.tokenEl(event.playerId));
        break;
      case "bankrupt": {
        sound.play("bankrupt");
        const token = board.tokenEl(event.playerId);
        if (token) await token.animate([{ opacity: 1, transform: token.style.transform }, { opacity: 0, transform: `${token.style.transform} scale(0.6)` }], { duration: motion.prefersReducedMotion() ? 1 : 700, fill: "forwards" }).finished.catch(() => {});
        break;
      }
      case "left": {
        // The host removed them (UX-02): their deeds went back to the bank, so a deed or card
        // still up in the show slot ("KESTREL'S NOW …") is out of date. Clear it and fade the token.
        setShow("");
        const token = board.tokenEl(event.playerId);
        if (token) await token.animate([{ opacity: 1, transform: token.style.transform }, { opacity: 0, transform: `${token.style.transform} scale(0.6)` }], { duration: motion.prefersReducedMotion() ? 1 : 700, fill: "forwards" }).finished.catch(() => {});
        break;
      }
      case "auction":
        sound.play("bid");
        break;
      default:
        break;
    }
  }
}

// ---------------------------------------------------------------------------------------------
// Deed cards: click any deed (board square, drawer card, trade row, the deed for sale or at
// auction) or press I / the pad's Y on a focused one to see its full title deed. Esc / B closes.

function deedIndexOf(node) {
  const host = node?.closest?.("[data-deed-info], [data-deed], [data-toggle], [data-pledge], .cell[data-index]");
  if (!host) return null;
  const raw = host.dataset.deedInfo ?? host.dataset.deed ?? host.dataset.pledge ?? (host.dataset.toggle ? host.dataset.toggle.split(":")[1] : host.dataset.index);
  const index = Number(raw);
  return Number.isInteger(index) && ui.state?.board?.[index] ? index : null;
}

function openDeedFromFocus() {
  const index = deedIndexOf(document.activeElement);
  if (index === null) return false;
  openDeedView(index);
  return true;
}

function viewerSeat() {
  const seats = ui.state?.localSeats || [];
  const overlaySeat = ui.overlay?.seatId ? seats.find((seat) => seat.seatId === ui.overlay.seatId) : null;
  return overlaySeat || seats.find((seat) => seat.input === "keyboard") || seats[0] || null;
}

// Who acts from a deed's card: the seat whose drawer or trade is open, else the player whose turn
// it is when they sit here, else the deed's owner when they sit here (views.deedActorSeat).
function deedActor(index) {
  const seats = ui.state?.localSeats || [];
  const overlaySeat = ui.overlay?.seatId ? seats.find((seat) => seat.seatId === ui.overlay.seatId) : null;
  return overlaySeat?.playerId ? overlaySeat : views.deedActorSeat(ui.state, index, viewerSeat());
}

function deedViewMarkup(index) {
  const actor = deedActor(index);
  return views.deedViewHtml(ui.state, index, { viewerId: actor?.playerId || null, seat: actor, actor });
}

// The open card follows the game (a mortgage, a house, a new owner), keeping focus where it was.
function refreshDeedView() {
  if (ui.deedView === null || !ui.state || el.deedView.hidden) return;
  morph(el.deedView, deedViewMarkup(ui.deedView));
  if (!el.deedView.contains(document.activeElement)) {
    (el.deedView.querySelector(".deed-act:not([disabled])") || el.deedView.querySelector("[data-autofocus]"))?.focus({ preventScroll: true });
  }
}

function openDeedView(index) {
  if (!ui.state?.board?.[index]) return;
  if (ui.soundMenu) toggleSoundMenu(false);
  ui.deedReturn = document.activeElement;
  ui.deedView = index;
  el.deedView.innerHTML = deedViewMarkup(index);
  el.deedView.hidden = false;
  sound.play("card");
  el.deedView.querySelector("[data-autofocus]")?.focus({ preventScroll: true });
}

// TRADE THIS… / ASK TO BUY… on a deed's card: the trade builder with the deed on your side, or
// asked of its owner (a computer's price chip shows at once). A builder already open for the seat
// keeps its deal and gains the deed.
function deedCompose(cmd, index, seatId) {
  const state = ui.state;
  const seat = state?.localSeats?.find((candidate) => candidate.seatId === seatId);
  if (!seat || !state.board[index]) return;
  const ownerId = state.properties?.[index]?.ownerId || null;
  const open = ui.overlay?.kind === "trade" && ui.overlay.seatId === seatId ? ui.overlay : null;
  const base = open ? open.draft : views.newTradeDraft();
  const add = (list) => [...new Set([...(list || []), index])];
  const draft = cmd === "deed-ask"
    ? (base.targetId === ownerId ? { ...base, request: add(base.request) } : { ...views.newTradeDraft(), targetId: ownerId, request: [index] })
    : { ...base, offer: add(base.offer) };
  ui.deedReturn = null;
  closeDeedView();
  if (open) open.draft = draft;
  else {
    ui.overlay = { kind: "trade", seatId, draft };
    ui.focusOverlayNext = true;
  }
  refreshQuote(ui.overlay);
  render();
}

function closeDeedView() {
  ui.deedView = null;
  el.deedView.hidden = true;
  el.deedView.innerHTML = "";
  const back = ui.deedReturn;
  ui.deedReturn = null;
  if (back?.isConnected) back.focus({ preventScroll: true });
}

el.deedView.addEventListener("click", async (event) => {
  const sale = event.target.closest("[data-cmd=deed-not-selling]");
  if (sale) {
    const [index, on] = sale.dataset.arg.split(":");
    if (ui.demo) toast(`Demo · would mark it ${on === "true" ? "not selling" : "for sale"}`);
    else {
      try {
        const state = await ui.client.notSelling(ui.session.code, ui.session.token, Number(index), on === "true", sale.dataset.seat);
        if (state?.version !== undefined) ui.live?.accept(state);
      } catch (error) {
        toast(plainError(error.message) || error.message, { error: true });
      }
    }
    refreshDeedView();
    return;
  }
  // MORTGAGE, UNMORTGAGE, BUILD, SELL, BUY: the engine action for the acting seat; the card stays
  // open and shows the result.
  const action = event.target.closest("[data-deed-act]");
  if (action) {
    if (!action.disabled) await perform(action.dataset.seat, JSON.parse(action.dataset.deedAct));
    refreshDeedView();
    return;
  }
  const compose = event.target.closest("[data-cmd=deed-trade], [data-cmd=deed-ask]");
  if (compose) {
    if (!compose.disabled) deedCompose(compose.dataset.cmd, Number(compose.dataset.arg), compose.dataset.seat);
    return;
  }
  if (event.target === el.deedView || event.target.closest("[data-cmd=deed-close]")) closeDeedView();
});

// Clicks on a deed anywhere open its card; clicks on a trade row still pick it (its card shows
// beside the lists), and the property drawer's buttons still act.
document.addEventListener("click", (event) => {
  if (ui.deedView !== null || event.defaultPrevented) return;
  const target = event.target;
  if (target.closest?.("[data-toggle], button.pdeed__act, .deed-view, .sound-menu")) return;
  const host = target.closest?.("[data-deed-info], .game__board .cell[data-index]");
  if (!host) return;
  const index = deedIndexOf(host);
  if (index !== null) openDeedView(index);
});

// The trade builder and an incoming offer show the focused deed's card beside the lists.
el.overlay.addEventListener("focusin", (event) => {
  const overlay = ui.overlay;
  if (!overlay || (overlay.kind !== "trade" && overlay.kind !== "offer")) return;
  const index = deedIndexOf(event.target);
  if (index === null || overlay.peek === index) return;
  overlay.peek = index;
  const peek = el.overlay.querySelector("[data-peek]");
  if (peek) peek.innerHTML = deedDetailHtml(ui.state, index, { viewerId: viewerSeat()?.playerId || null, size: "sm" });
});
el.overlay.addEventListener("pointerover", (event) => {
  const overlay = ui.overlay;
  if (!overlay || (overlay.kind !== "trade" && overlay.kind !== "offer")) return;
  const index = deedIndexOf(event.target);
  if (index === null || overlay.peek === index) return;
  overlay.peek = index;
  const peek = el.overlay.querySelector("[data-peek]");
  if (peek) peek.innerHTML = deedDetailHtml(ui.state, index, { viewerId: viewerSeat()?.playerId || null, size: "sm" });
});

// ---------------------------------------------------------------------------------------------
// Sessions: live (party server or old Node server) and demo. A table belongs to one screen at a
// time; another screen can take it over with POST /api/tables/{code}/resume (docs/API.md).

function startSession(session, initialState) {
  ui.live?.stop();
  ui.session = session;
  storage.set(SESSION_KEY, session);
  ui.state = null;
  ui.queue = [];
  ui.pendingEvents = [];
  ui.overlay = null;
  ui.gate = null;
  ui.prevCash = new Map();
  ui.decisionKey = "";
  el.loading.hidden = true;
  const live = new LiveRoom(ui.client, {
    code: session.code,
    token: session.token,
    onState,
    onEvent: (event) => ui.pendingEvents.push(event),
    onStatus: (status) => {
      const wasOffline = ui.status === "offline";
      ui.status = status;
      renderNetbar();
      if (ui.state) render();
      if (wasOffline && status !== "offline") toast("Back online.");
    },
    onFatal: (error) => {
      if (ui.live !== live) return;
      // The server closes the empty table this screen is replacing: expected, not the end.
      if (ui.creatingTable) {
        live.closedByServer = true;
        ui.fatalWhileCreating = error;
        return;
      }
      sessionEnded(error);
    },
  });
  ui.live = live;
  if (initialState) live.accept(initialState);
  live.start();
}

// The session is over. Moved to another screen → say so and offer it back; gone → a new table.
function sessionEnded(error) {
  const session = ui.session;
  ui.live?.stop();
  ui.live = null;
  if (session?.code && !session.watch && MOVED.test(error?.message || "")) {
    // Keep the code so a reload asks for this table back instead of quietly starting a new one.
    storage.set(SESSION_KEY, { code: session.code, token: null });
    ui.state = null;
    ui.queue = [];
    el.join.hidden = true;
    el.game.hidden = true;
    showGate({ kind: "moved", code: session.code });
    return;
  }
  storage.set(SESSION_KEY, null);
  toast("That table has closed. Setting up a new one…", { error: true });
  setTimeout(() => location.reload(), 1500);
}

// Open an existing table on this screen. from: "manual" (typed, RETRY, TAKE IT BACK, ?table=)
// shows what went wrong; "saved" (this browser's old table) quietly gives up so a new table opens.
async function resumeTable(rawCode, { from = "manual" } = {}) {
  const code = normaliseCode(rawCode);
  try {
    let result = await ui.client.resumeTable(code);
    // [seat-approval hook] a new screen waits for the host's yes (or the code their phone shows).
    if (result?.pending) result = await awaitApproval(ui.client, code, result, (approvalCode) => ui.client.resumeTable(code, approvalCode));
    if (!result) {
      if (from === "saved") return false;
      showGate({ kind: "resume", code, error: "" });
      return "gate";
    }
    startSession({ code: result.code, token: result.tableToken }, result.state);
    sound.play("accept");
    return true;
  } catch (error) {
    if (error.status === 409) {
      showGate({ kind: "busy", code, error: error.message });
      return "gate";
    }
    if (from === "saved") return false;
    const message = error.status === 404 || /does not exist/i.test(error.message || "") ? "NO TABLE WITH THAT CODE" : error.message;
    if (ui.gate || !ui.state) showGate({ kind: "resume", code, error: message });
    else toast(`${code}: ${message}`, { error: true });
    return "gate";
  }
}

function showWatchPrompt(message = "") {
  el.loading.hidden = false;
  el.loading.innerHTML = `<form class="watch-form on-light" id="watch-form">
    <h1 class="watch-form__title">WATCH A GAME</h1>
    <p>This server only runs the classic rooms. Enter a room code to show that game on this screen.</p>
    ${message ? `<p class="watch-form__error" role="alert">${message}</p>` : ""}
    <label class="field"><span>Room code</span><input id="watch-code" maxlength="5" autocomplete="off" autocapitalize="characters" required></label>
    <button class="big-btn big-btn--primary" type="submit">WATCH</button>
  </form>`;
  const input = $("#watch-code");
  input.focus();
  $("#watch-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      const result = await ui.client.watch(input.value, "Table");
      el.loading.innerHTML = "";
      startSession({ code: result.code, token: result.token, watch: true }, result.state);
    } catch (error) {
      showWatchPrompt(error.message);
    }
  });
}

async function startLive() {
  ui.server = await ui.client.server();
  const params = new URLSearchParams(location.search);
  const wanted = normaliseCode(params.get("table"));
  if (params.has("table")) {
    // ?table=CODE opens that table here; drop it from the address so a reload uses the session.
    params.delete("table");
    history.replaceState(null, "", `${location.pathname}${params.toString() ? `?${params}` : ""}${location.hash}`);
  }
  const saved = storage.get(SESSION_KEY);
  if (saved?.code && saved?.token && (!wanted || wanted === saved.code)) {
    try {
      const state = await ui.client.state(saved.code, saved.token);
      startSession(saved, state);
      return;
    } catch (error) {
      if (error.status === 0) {
        el.loading.textContent = error.message;
        return;
      }
    }
  }
  if (ui.server && wanted) {
    await resumeTable(wanted, { from: "manual" });
    return;
  }
  // This browser's table session is no longer valid: ask for the same table back first.
  if (ui.server && saved?.code && !saved.watch) {
    const resumed = await resumeTable(saved.code, { from: "saved" });
    if (resumed) return;
  }
  storage.set(SESSION_KEY, null);
  const watchCode = params.get("watch");
  if (!ui.server || watchCode) {
    if (watchCode) {
      try {
        const result = await ui.client.watch(watchCode, "Table");
        startSession({ code: result.code, token: result.token, watch: true }, result.state);
        return;
      } catch (error) {
        showWatchPrompt(error.message);
        return;
      }
    }
    showWatchPrompt();
    return;
  }
  try {
    const result = await ui.client.createTable();
    startSession({ code: result.code, token: result.tableToken }, result.state);
  } catch (error) {
    el.loading.textContent = `Couldn't set up a table: ${error.message}`;
  }
}

// Fixtures written for a phone viewer become table views: no viewer, local seats for pads/keys.
function asTableState(state) {
  if (state.viewerRole === "table") return state;
  let pad = 0;
  const localSeats = state.players
    .filter((player) => player.input === "controller" || player.input === "keyboard")
    .map((player) => ({ seatId: `seat_${player.id}`, playerId: player.id, input: player.input, pad: player.input === "controller" ? pad++ : null }));
  return { ...state, viewerRole: "table", viewerId: null, localSeats };
}

// Demo only: screenshot scripts and reviewers reach every screen from fixtures (web/table/demo.js
// explains the fixture format). estateDemo.ready() turns true once a fixture's later states, its
// animations and its UI steps are all done.
function installDemoHooks() {
  globalThis.estateDemo = {
    ready: () => ui.demoReady === true,
    games(list) {
      ui.games.list = views.otherGames(list, ui.state?.code);
      render();
    },
    open(kind) {
      return demoOpen({ kind });
    },
    // QA: push a state as the socket would.
    state(next) {
      onState(next);
    },
  };
}

function demoPlayerId(who) {
  const name = String(who || "").replace(/^@/, "");
  return ui.state?.players?.find((candidate) => candidate.name === name || candidate.id === name)?.id || null;
}

function demoSeat(who) {
  const seats = ui.state?.localSeats || [];
  if (who) {
    const name = String(who).replace(/^@/, "");
    const player = ui.state.players.find((candidate) => candidate.name === name || candidate.id === name);
    return seats.find((seat) => seat.playerId === player?.id) || null;
  }
  return seats.find((seat) => seat.input === "keyboard") || seats[0] || null;
}

function demoOpen(open) {
  const seat = demoSeat(open.seat);
  switch (open.kind) {
    case "trade": openTrade(seat); break;
    case "loan": openLoan(seat); break;
    case "seat": openSeatSetup(open.input || "keyboard", open.pad ?? null); break;
    case "rules": openOverlay({ kind: "rules" }); break;
    case "victory": openOverlay({ kind: "victory" }); break;
    case "rematch": askRematch(); break;
    case "bankrupt": runRow(ui.panelRows.find((row) => row.cmd === "bankrupt" && (!seat || row.seatId === seat.seatId))); break;
    case "menu":
      openMenu();
      if (open.page && open.page !== "main") showMenuPage(open.page);
      if (open.player) {
        ui.overlay.hostPick = demoPlayerId(open.player);
        render();
      }
      break;
    case "remove": hostAction("remove", demoPlayerId(open.player)); break;
    case "ticks":
      ui.debtPicks = { [views.debtKey(ui.state)]: [].concat(open.ticks || []) };
      render();
      return true;
    case "leave": openMenu(); menuClick(ui.overlay, { dataset: { cmd: "menu-leave" } }); break;
    case "end": openMenu(); menuClick(ui.overlay, { dataset: { cmd: "menu-end" } }); break;
    case "sound": toggleSoundMenu(true); break;
    case "deed": openDeedView(open.index); break;
    default:
      if (!seat) return false;
      openOverlay({ kind: open.kind, seatId: seat.seatId });
  }
  const overlay = ui.overlay;
  if (open.draft && overlay?.draft) {
    Object.assign(overlay.draft, resolveNames(open.draft, ui.state.players));
    if (overlay.kind === "seat" && open.draft.name) overlay.draft.autoName = false;
    if (overlay.kind === "trade") {
      if (open.draft.quote) {
        // A fixed price (or "asking"): keep it, don't fetch.
        const status = tradeStatus(overlay);
        overlay.draft.quoteKey = `${status.target?.id}|${status.offer.join(",")}|${status.request.join(",")}`;
      } else refreshQuote(overlay);
    }
    const name = el.overlay.querySelector("#seat-name");
    if (name && open.draft.name) name.value = open.draft.name;
    render();
  }
  return Boolean(overlay);
}

const demoUntil = async (test, ms = 15000) => {
  const end = performance.now() + ms;
  while (!test() && performance.now() < end) await motion.wait(50);
};

async function startDemo(name) {
  ui.demo = name;
  installDemoHooks();
  ui.status = "live";
  document.title = `Estate · Table demo · ${name}`;
  try {
    const server = await loadFixture("server").catch(() => null);
    ui.server = server?.server || null;
    // ?demo=…&gate=moved|busy|resume previews the getting-back-in screens.
    const gate = new URLSearchParams(location.search).get("gate");
    if (["moved", "busy", "resume"].includes(gate)) {
      showGate({ kind: gate, code: "NXGNT" });
      if (name === "gate") {
        ui.demoReady = true;
        return;
      }
    }
    if (/^quiz-/.test(name)) {
      const { state } = await buildQuizScene(name);
      const asTv = state.viewerRole === "table" ? state : { ...state, viewerId: "", viewerRole: "table", viewerName: "Table", you: null };
      onState(asTv);
      await demoUntil(() => ui.state && !ui.draining);
      const params = new URLSearchParams(location.search);
      if (params.get("open")) demoOpen({ kind: params.get("open"), page: params.get("page") || "main" });
      ui.demoReady = true;
      return;
    }
    // Near Thing: each phase's settled frame; &play=1 plays the line-up and the reveal from
    // the phase start on the fixture's clock.
    if (/^closest-/.test(name)) {
      const params = new URLSearchParams(location.search);
      ui.demoPlay = params.get("play") === "1";
      const { state } = await buildClosestScene(name);
      const asTv = state.viewerRole === "table" ? state : { ...state, viewerId: "", viewerRole: "table", viewerName: "Table", you: null };
      onState(asTv);
      await demoUntil(() => ui.state && !ui.draining);
      if (params.get("open")) demoOpen({ kind: params.get("open"), page: params.get("page") || "main" });
      ui.demoReady = true;
      return;
    }
    if (/^ludo-/.test(name)) {
      const { state } = await buildLudoScene(name);
      const asTv = state.viewerRole === "table" ? state : { ...state, viewerId: null, viewerRole: "table", viewerName: "", you: null };
      onState(asTv);
      await demoUntil(() => ui.state && !ui.draining);
      const params = new URLSearchParams(location.search);
      if (params.get("open")) demoOpen({ kind: params.get("open"), page: params.get("page") || "main" });
      ui.demoReady = true;
      return;
    }
    if (/^poker-/.test(name)) {
      const { state } = await buildPokerScene(name);
      const asTv = state.viewerRole === "table" ? state : { ...state, viewerId: null, viewerRole: "table", viewerName: "", you: null, players: state.players.map((player) => (player.shown || ["runout", "showdown"].includes(state.phase) ? player : { ...player, cards: null })) };
      onState(asTv);
      await demoUntil(() => ui.state && !ui.draining);
      const params = new URLSearchParams(location.search);
      if (params.get("open")) demoOpen({ kind: params.get("open"), page: params.get("page") || "main" });
      ui.demoReady = true;
      return;
    }
    const fixture = await composeFixture(name, loadFixture);
    const replay = expandReplay(fixture);
    if (replay) {
      let index = 0;
      const step = () => {
        if (index === 0) ui.state = null;
        onState(asTableState(replay[index]));
        index = (index + 1) % replay.length;
        const wait = index === 0 ? 4000 : 2200;
        setTimeout(function next() {
          if (ui.draining) setTimeout(next, 300);
          else step();
        }, wait);
      };
      step();
      return;
    }
    const context = fixture.ctx || {};
    const steps = fixture.ui || {};
    if (context.pads) ui.padNames = { ...context.pads };
    if (context.games) ui.games.list = context.games;
    if (context.server !== undefined) ui.server = context.server;
    if (context.status) {
      ui.status = context.status;
      renderNetbar();
    }
    if (context.aiLevel) ui.aiLevel = context.aiLevel;
    if (context.screen) {
      ui.screen = normaliseScreen({ ...ui.screen, ...context.screen });
      applyScreenSettings();
    }
    if (steps.loading || steps.watchPrompt !== undefined) {
      if (steps.watchPrompt !== undefined) showWatchPrompt(steps.watchPrompt);
      else el.loading.textContent = steps.loading;
      ui.demoReady = true;
      return;
    }
    const table = (state) => {
      const view = asTableState(state);
      return context.watching ? { ...view, viewerRole: "spectator", localSeats: [] } : view;
    };
    if (fixture.state) {
      onState(table(fixture.state));
      await demoUntil(() => ui.state && !ui.draining);
    }
    for (const step of fixture.then || []) {
      await motion.wait(step.after);
      onState(table(step.state));
      await demoUntil(() => !ui.draining);
    }
    for (const open of [].concat(steps.open || [])) demoOpen(typeof open === "string" ? { kind: open } : open);
    if (steps.close) closeOverlay();
    for (const [selector, value] of Object.entries(steps.type || {})) {
      const input = document.querySelector(selector);
      if (input) input.value = value;
    }
    if (steps.focus) {
      const node = document.querySelector(steps.focus);
      if (node) focusNode(node);
    }
    if (steps.gate) showGate(steps.gate);
    if (steps.toast) toast(steps.toast.text, { error: Boolean(steps.toast.error) });
    ui.demoReady = true;
  } catch (error) {
    el.loading.textContent = error.message;
    ui.demoReady = true;
  }
}

// ---------------------------------------------------------------------------------------------

function init() {
  ensureMetalDefs();
  // Sound starts at once where the browser allows it (kiosk launch), else on the first click or
  // key press; the speaker icon and the sound pill follow the browser's state.
  sound.onAudioState(() => {
    renderMute();
    // The poker and Ludo screens draw their own sound button.
    if (ui.state) render();
  });
  sound.prepareAudio();
  sound.installAudioUnlock(window);
  applyScreenSettings();
  music.start("lobby");
  renderMute();
  fit();
  window.addEventListener("resize", fit);
  requestAnimationFrame(pollPads);
  const demo = demoParam();
  // The first real render waits (briefly) for the fonts, so nothing re-flows in front of the room.
  fontsReady(1200, document, [...FACES, ...TABLETOP_FACES]).then(() => {
    if (demo) startDemo(demo);
    else startLive();
  });
}

// Installable app: over HTTPS the browser offers to install "Estate Table" as its own window.
startApp({ onChange: () => render() });
init();
