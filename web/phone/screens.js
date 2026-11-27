// Pure screen builders for the phone. `route()` picks a screen from the state and UI tab;
// `renderPhone()` returns HTML strings for header (player strip in game), banner, main and nav,
// plus `board` data for the BOARD tab (stage, status line, shaded squares) that phone.js applies to
// the persistent board. No DOM access, so every fixture can be rendered and checked in Node.

import { diceHtml, dieHtml } from "../shared/dice.js";
import {
  GROUP_COLORS, GROUP_INK, aiLevelLabel, colourWord, escapeHtml, inputOf, money, ordinal, playerById, possessive, upper,
} from "../shared/format.js";
import {
  activeLoans, bankruptcyOrder, canManage, hasOutOrder, debtPlan, finalScore, groupIndexes, loanDue, loanSecuredBy, loanTerms, netWorth, nextBuild, offerParties, ownedBy, propState,
  rentFor, rentLabel, rulesOf, standings, unmortgageCost,
} from "../shared/insights.js";
import { MAX_PLAYERS, TOKEN_COLORS, playerColor, playerTokenSvg, tokenSvg } from "../shared/tokens.js";
import { icon } from "./icons.js";
import { canOfferInstall, installSteps } from "../shared/pwa.js";
import { btn, buttonsHtml, glassBox, tipHtml } from "./parts.js";
import { auctionStatus, boardScreen, isBoardScreen, statusHtml } from "./stage.js";
import { canBid, isTimed } from "../shared/auction-clock.js";
import { showBack } from "../shared/standin.js";
import { debtChecklistScreen, hostPlayersHtml, idleBarHtml, loanCardsHtml, timedAuctionScreen, tokenFieldHtml } from "./ux-screens.js";
import { shortName } from "./cues.js";
import { discHtml, sinceDeltas, stripHtml } from "./strip.js";
import { isMyTradeTurn, loanComposeScreen, plannedTrade, tradeScreen } from "./trade.js";
import { gamePickHtml, gameTypeOf, gtypeTag } from "../shared/poker-view.js";

const e = escapeHtml;
const GOLD = ["#F2C14E", "#15191E"];

export const TABS = ["turn", "property", "trade", "friends"];
export const WATCH_TABS = ["turn", "players", "friends"];
const TAB_LABELS = { turn: "BOARD", property: "MY DEEDS", trade: "TRADE", friends: "MORE", players: "PLAYERS" };
// A line icon over each tab's label (the Tabletop nav, tabletop.css).
const navIcon = (paths) => `<svg class="nav__icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${paths}</svg>`;
const NAV_ICONS = {
  turn: navIcon('<rect x="3" y="3" width="18" height="18" rx="2"/><rect x="8" y="8" width="8" height="8"/>'),
  property: navIcon('<rect x="4" y="5" width="12" height="16" rx="2"/><path d="M8 3h10a2 2 0 0 1 2 2v13"/>'),
  trade: navIcon('<path d="M4 8h14l-4-4M20 16H6l4 4"/>'),
  friends: navIcon('<path d="M5 12h.01M12 12h.01M19 12h.01"/>'),
  players: navIcon('<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0M16 4.6a3.5 3.5 0 0 1 0 6.8M18 14.3a6.5 6.5 0 0 1 3.5 5.7"/>'),
};

// ---------------------------------------------------------------------------------------------
// Routing

export function me(state) {
  return playerById(state, state?.viewerId);
}

export function myDecision(state) {
  const player = me(state);
  if (!state || !player || player.bankrupt || state.status !== "playing") return null;
  switch (state.phase) {
    case "determining_order": return state.openingOrder?.pendingIds.includes(player.id) ? "order" : null;
    // Timed (UX-03): anyone still in except the leader; turn order: whose bid it is.
    case "auction": return canBid(state, player.id) ? "auction" : null;
    case "trade": return state.trade?.targetId === player.id ? "trade" : null;
    case "loan_offer": return state.loanOffer?.targetId === player.id ? "loan" : null;
    case "card_confirmation": return state.pendingCard?.playerId === player.id ? "card" : null;
    case "debt": return state.debt?.payerId === player.id ? "debt" : null;
    case "awaiting_roll":
    case "awaiting_purchase":
    case "turn_complete": return state.currentPlayerId === player.id ? state.phase : null;
    default: return null;
  }
}

// Spectators and players who are out get BOARD · PLAYERS · MORE.
export function tabsFor(state) {
  const player = me(state);
  return !player || player.bankrupt ? WATCH_TABS : TABS;
}

export function route(state, ui = {}) {
  if (!ui.session) return ui.invitedView ? "invited" : "join";
  if (!state) return "join";
  if (ui.loanDraft && state.status === "playing") return "loanDraft";
  if (state.status === "lobby") return ui.inviteOpen ? "invite" : "lobby";
  if (state.status === "finished" && ui.tab !== "property" && ui.tab !== "players") return "result";
  const tabs = tabsFor(state);
  const tab = tabs.includes(ui.tab) ? ui.tab : "turn";
  if (tab === "property") return "property";
  if (tab === "players") return "players";
  if (tab === "trade") return "trade";
  if (tab === "friends") return ui.inviteOpen ? "invite" : "friends";
  const player = me(state);
  if (!player || player.bankrupt) return "waiting";
  // The computer plays for you (UX-07): COMPUTER PLAYING with I'M BACK, whatever the phase.
  if (showBack(player)) return "standin";
  if (state.phase === "determining_order" || state.phase === "order_countdown") return "order";
  if (state.phase === "auction") return isTimed(state) ? "timedAuction" : "auction";
  if (state.phase === "trade" && (state.trade?.targetId === player.id || state.trade?.proposerId === player.id)) return "trade";
  if (state.phase === "loan_offer" && (state.loanOffer?.targetId === player.id || state.loanOffer?.proposerId === player.id)) return "loan";
  if (state.phase === "debt" && state.debt?.payerId === player.id) return "debt";
  if (state.phase === "card_confirmation" && state.pendingCard?.playerId === player.id) return "card";
  if (state.currentPlayerId !== player.id) return "waiting";
  if (state.phase === "awaiting_purchase") return "buy";
  if (state.phase === "awaiting_roll") return player.inJail ? "jail" : "roll";
  if (state.phase === "turn_complete") {
    if (player.inJail) return "jail";
    if (state.lastLanding?.kind === "rent" && state.lastLanding.playerId === player.id) return "rent";
    return "done";
  }
  return "waiting";
}

// ---------------------------------------------------------------------------------------------
// Header, banner, nav

function headerHtml(state, screen, ui) {
  const inGame = state && (state.status === "playing" || state.status === "finished") && screen !== "join" && screen !== "invited";
  if (inGame) {
    const deltas = screen === "roll" ? sinceDeltas(state, state.viewerId) : null;
    return stripHtml(state, { viewerId: state.viewerId, deltas });
  }
  const code = state?.code || ui.join?.code;
  const watch = ui.session?.role === "spectator";
  return `<div class="bar bar--logo"><span class="bar__logo">ESTATE</span>${code ? `<span class="bar__table">${watch ? "Watching" : "Table"} <strong>${e(code)}</strong></span>` : ""}<button type="button" class="bar__gear" data-cmd="settings" aria-label="Settings">${icon("gear", { size: 20 })}</button></div>`;
}

function bannerHtml(banner) {
  if (!banner) return "";
  // data-key: the same banner is kept across updates (no replayed lift-in); new words animate in.
  return `<div class="banner" data-key="${e(banner[1])}" style="background:${banner[0][0]};color:${banner[0][1]}">${e(banner[1])}</div>`;
}

function navHtml(state, screen, ui) {
  const tabs = tabsFor(state);
  const tab = tabs.includes(ui.tab) ? ui.tab : "turn";
  const decision = Boolean(myDecision(state));
  const planReady = Boolean(plannedTrade(state, ui.plan)) && isMyTradeTurn(state, state?.viewerId);
  return `<nav class="nav nav--${tabs.length}" aria-label="Sections">${tabs.map((name) => {
    const active = tab === name;
    const dot = !active && ((name === "turn" && decision) || (name === "trade" && planReady));
    return `<button type="button" class="nav__btn${active ? " is-active" : ""}" data-tab="${name}" aria-pressed="${active}" aria-label="${TAB_LABELS[name]}${dot ? " (needs you)" : ""}">
      ${NAV_ICONS[name] || ""}<span class="nav__label">${TAB_LABELS[name]}</span><span class="nav__bar" aria-hidden="true"></span>${dot ? '<span class="nav__dot" aria-hidden="true"></span>' : ""}
    </button>`;
  }).join("")}</nav>`;
}

// ---------------------------------------------------------------------------------------------
// Getting in: table code, "WHO ARE YOU?" (take your seat back from any phone), new player form

// Human seats from the room summary (computers can't be taken over, so they aren't listed).
export function humanSeats(summary) {
  return (summary?.players || []).filter((player) => inputOf(player) !== "ai");
}

// Which join view to show: "home" (no table yet: NEW GAME, ACTIVE GAMES, join with code), "create"
// (name + token for a new phone-hosted game), "who" (pick your seat) or "new" (name + token).
export function joinMode(join = {}) {
  if (join.mode === "create") return "create";
  const summary = join.summary;
  if (!summary) return normalisedCode(join.code).length === 5 && !join.loading && !join.missing ? "new" : "home";
  const started = summary.status && summary.status !== "lobby";
  if (join.mode === "new" && !started) return "new";
  if (started) return "who";
  return humanSeats(summary).some((player) => player.reclaimable) ? "who" : "new";
}

function normalisedCode(code) {
  return String(code || "").toUpperCase().replace(/[^A-Z0-9]/g, "");
}

function seatState(player) {
  if (player.reclaimable) return "";
  if (player.input === "controller" || player.input === "keyboard") return "at the PC";
  if (player.online) return "playing";
  if (player.reclaimable === undefined) return "";
  return "out";
}

function seatTile(player, join) {
  const color = player.color || TOKEN_COLORS[player.tokenKind] || "#8A929C";
  const token = tokenSvg(player.tokenKind || "rocket", color, { size: 40 });
  const name = `<span class="seat__name">${e(upper(player.name))}</span>`;
  const busy = join.busy && join.reclaim?.playerId === player.id;
  if (player.reclaimable && player.id) {
    return `<button type="button" class="seat" data-cmd="reclaim" data-arg="${e(player.id)}" style="--seat:${e(color)}" aria-label="I'm ${e(player.name)}"${join.busy ? " disabled" : ""}>${token}${name}${busy ? '<span class="seat__state">…</span>' : ""}</button>`;
  }
  const state = seatState(player);
  return `<div class="seat is-off" style="--seat:${e(color)}" aria-label="${e(player.name)}${state ? `, ${state}` : ""}">${token}${name}${state ? `<span class="seat__state">${e(state)}</span>` : ""}</div>`;
}

function reclaimNote(join, summary) {
  const reclaim = join.reclaim;
  if (!reclaim?.error) return "";
  const seat = (summary?.players || []).find((player) => player.id === reclaim.playerId);
  if (reclaim.status === 409) {
    return `<div class="tip tip--warn tip--row" role="alert"><span><strong>${e(upper(seat?.name || "That seat"))} IS STILL ON.</strong> Close it on the other phone, or wait 20 s.</span>
      <button type="button" class="small-btn is-gold" data-cmd="reclaim" data-arg="${e(reclaim.playerId)}"${join.busy ? " disabled" : ""}>RETRY</button></div>`;
  }
  return `<div class="tip tip--warn" role="alert">${e(reclaim.error)}</div>`;
}

// What tapping a game in ACTIVE GAMES does: REJOIN your own seat, JOIN a lobby with room (or a
// started game with an offline seat, which opens WHO ARE YOU?), otherwise WATCH.
export function gameAction(game) {
  if (game.mine) return { label: "REJOIN", kind: "rejoin" };
  if (game.status === "lobby" && game.openSeats > 0) return { label: "JOIN", kind: "join" };
  if (game.status !== "lobby" && game.status !== "finished" && (game.players || []).some((player) => player.reclaimable)) return { label: "JOIN", kind: "seats" };
  return { label: "WATCH", kind: "watch" };
}

function gameChip(game) {
  if (game.status === "lobby") {
    const seats = (game.players || []).length;
    return { text: `LOBBY · ${seats}/${seats + (game.openSeats || 0)}`, tone: "lobby" };
  }
  if (game.status === "finished") return { text: "FINISHED", tone: "done" };
  // Poker tables count hands (the summary's turn is the hand number there); Estate and Ludo turns.
  if (gameTypeOf(game) === "poker") return { text: game.turn ? `HAND ${game.turn}` : "STARTING", tone: "play" };
  return { text: game.turn ? `TURN ${game.turn}` : "STARTING", tone: "play" };
}

function gameRow(game) {
  const host = game.host || game.players?.[0] || {};
  const action = gameAction(game);
  const chip = gameChip(game);
  const discs = (game.players || []).map((player) => `<span class="gdisc${player.online === false && player.input !== "ai" ? " is-off" : ""}" style="--disc:${e(player.color || TOKEN_COLORS[player.tokenKind] || "#8A929C")}" title="${e(player.name)}">${tokenSvg(player.tokenKind || "rocket", player.color, { size: 16 })}</span>`).join("");
  const pc = game.tableOnline ? `<span class="gpc" aria-label="on a PC">${icon("screen", { size: 12 })}PC</span>` : "";
  return `<li class="grow${game.mine ? " is-mine" : ""}">
      <span class="grow__host">${tokenSvg(host.tokenKind || "rocket", host.color, { size: 34, label: `${host.name || "Host"}'s token` })}</span>
      <div class="grow__main">
        <div class="grow__title"><b>${e(upper(host.name || game.code))}</b><span class="gchip gchip--${chip.tone}">${e(chip.text)}</span>${pc}</div>
        <div class="grow__discs">${gtypeTag(gameTypeOf(game))}${discs}<span class="grow__code">${e(game.code)}</span></div>
      </div>
      <button type="button" class="small-btn${action.kind === "watch" ? "" : " is-gold"}" data-cmd="game-open" data-arg="${e(game.code)}" aria-label="${action.label} ${e(host.name || "")} ${e(game.code)}">${action.label}</button>
    </li>`;
}

// ADD ESTATE TO YOUR HOME SCREEN on the phone home screen: shown until the app is installed or
// the person hides it (×, remembered on the device). The button opens the browser's install
// dialog when it has offered one, otherwise a sheet with the steps for this browser.
export function installCtaHtml(ui = {}) {
  if (!canOfferInstall(ui.install) || ui.installHidden) return "";
  return `<div class="installcta"><button type="button" class="installcta__btn" data-cmd="install-app">${icon("addhome", { size: 20 })}ADD ESTATE TO YOUR HOME SCREEN</button><button type="button" class="installcta__x" data-cmd="install-hide" aria-label="Hide this">${icon("cross", { size: 16 })}</button></div>`;
}

function homeScreen(ui) {
  const join = ui.join || {};
  const games = ui.games?.list;
  const listed = new Set((games || []).map((game) => game.code));
  // BACK TO TABLE only when the list doesn't already show that game (or the list isn't available).
  const known = Array.isArray(games);
  const last = ui.lastTable?.code && ui.lastTable.code !== normalisedCode(join.code) && (known ? !listed.has(ui.lastTable.code) : Boolean(ui.games?.error) || !ui.games) ? ui.lastTable : null;
  const newGame = buttonsHtml([btn("NEW GAME", "", "primary", { cmd: "new-game" })]).replace('class="actions"', 'class="actions actions--top"');
  const back = last ? buttonsHtml([btn(`BACK TO TABLE ${last.code}`, "", "ghost", { cmd: "back-to-table", arg: last.code })]).replace('class="actions"', 'class="actions actions--top"') : "";
  const list = games === null || games === undefined
    ? '<p class="gempty">Looking for games…</p>'
    : games.length ? `<ul class="glist">${games.map(gameRow).join("")}</ul>` : '<p class="gempty">No games yet · start one</p>';
  // Phone: one column (NEW GAME, list, join with code). Tablet and desk: start on the left, the list
  // on the right (phone.css orders the pieces).
  const main = `<div class="home"><div class="home__start">${join.note ? tipHtml({ tone: "warn", text: join.note }) : ""}${newGame}${back}
    <div class="joincode"><label for="p-code">JOIN WITH CODE</label><input id="p-code" class="input input--code input--small" type="text" inputmode="text" autocomplete="off" autocapitalize="characters" maxlength="5" value="${e(join.code || "")}" placeholder="ABCDE"></div>
    ${join.error ? `<div class="tip tip--warn home__error" role="alert">${e(join.error)}</div>` : ""}${installCtaHtml(ui)}</div>
    <section class="games" aria-label="Active games"><h2 class="eyebrow">ACTIVE GAMES</h2>${list}<p class="gnote">Only your games and public ones show here.</p></section></div>`;
  return { banner: null, main, nav: false };
}

function whoScreen(state, ui) {
  const join = ui.join || {};
  const summary = join.summary;
  const started = summary.status && summary.status !== "lobby";
  const seats = humanSeats(summary);
  const hasIds = seats.some((player) => player.id);
  // Seats you can take first.
  const sorted = [...seats].sort((a, b) => Number(Boolean(b.reclaimable)) - Number(Boolean(a.reclaimable)));
  const grid = seats.length
    ? `<div class="seats">${sorted.map((player) => seatTile(player, join)).join("")}</div>`
    : '<p class="note note--ink">No player seats yet.</p>';
  const main = `${join.note ? tipHtml({ tone: "warn", text: join.note }) : ""}
    <section class="paper">
      <div class="paper__head">WHO ARE YOU?${gameTypeOf(summary) !== "estate" ? ` ${gtypeTag(gameTypeOf(summary))}` : ""}</div>
      <div class="paper__body">${grid}</div>
    </section>
    ${reclaimNote(join, summary)}
    ${join.error ? `<div class="tip tip--warn" role="alert">${e(join.error)}</div>` : ""}`;
  const buttons = [];
  // Old servers have no seat ids: fall back to this device's own seat.
  if (!hasIds && started) buttons.push(btn("GET MY SEAT BACK", "", "primary", { cmd: "rejoin", disabled: join.busy }));
  if (!started) buttons.push(btn("JOIN AS NEW PLAYER", "", "primary", { cmd: "join-new", disabled: join.busy }));
  buttons.push(btn("JUST WATCH", "", "ghost", { cmd: "watch", disabled: join.busy }));
  return { banner: null, main: `${main}${buttonsHtml(buttons)}`, nav: false };
}

function newPlayerScreen(state, ui, { create = false } = {}) {
  const join = ui.join || {};
  const summary = create ? null : join.summary;
  const takenBy = new Map((summary?.players || state?.players || []).filter((player) => player.tokenKind).map((player) => [player.tokenKind, player.name]));
  const codeField = join.lockedCode || create ? "" : `<div class="field"><label for="p-code">Table code</label><input id="p-code" class="input input--code" type="text" inputmode="text" autocomplete="off" autocapitalize="characters" maxlength="5" value="${e(join.code || "")}" placeholder="NXGNT"></div>`;
  const back = create
    ? '<button type="button" class="tlink tlink--light" data-cmd="home">‹ BACK</button>'
    : summary && humanSeats(summary).some((player) => player.reclaimable)
      ? '<button type="button" class="tlink tlink--light" data-cmd="join-who">‹ ALREADY SEATED?</button>' : "";
  const other = !create && gameTypeOf(summary) !== "estate" ? gameTypeOf(summary) : "";
  const picked = ["poker", "ludo", "quiz", "closest"].includes(join.gameType) ? join.gameType : "estate";
  const picker = create ? `<div class="field"><span class="field__label">Game</span>${gamePickHtml(picked, { cmd: "pick-game", column: true })}</div>` : "";
  const main = `${back}<section class="paper">
      <div class="paper__head">${create ? "NEW GAME" : "NEW PLAYER"}${other ? ` ${gtypeTag(other)}` : ""}</div>
      <div class="paper__body">
        ${picker}${codeField}
        <div class="field"><label for="p-name">Name</label><input id="p-name" class="input" type="text" autocomplete="nickname" maxlength="12" value="${e(join.name || "")}" placeholder="Your name"></div>
        ${tokenFieldHtml({ pickedKind: join.tokenKind, takenBy, palette: ui.palette })}
      </div>
    </section>
    ${join.error ? `<div class="tip tip--warn" role="alert">${e(join.error)}</div>` : ""}`;
  const buttons = create
    ? [btn(join.busy ? "CREATING…" : "CREATE GAME", "", "primary", { cmd: "create-game", disabled: join.busy })]
    : [btn(join.busy ? "JOINING…" : "JOIN", "", "primary", { cmd: "join", disabled: join.busy })];
  return { banner: null, main: `${main}${buttonsHtml(buttons)}`, nav: false };
}

function joinScreen(state, ui) {
  const mode = joinMode(ui.join);
  if (mode === "who") return whoScreen(state, ui);
  if (mode === "new") return newPlayerScreen(state, ui);
  if (mode === "create") return newPlayerScreen(state, ui, { create: true });
  return homeScreen(ui);
}

function invitedScreen(state, ui) {
  const join = ui.join || {};
  const summary = join.summary || {};
  const host = summary.host || summary.players?.[0] || null;
  const hostName = host?.name || ui.invite?.from?.name || "A friend";
  const players = summary.players || [];
  const seats = players.map((player) => tokenSvg(player.tokenKind, player.color, { size: 36 })).join("");
  const main = `<section class="paper">
      <div class="paper__head">YOU'RE INVITED${gameTypeOf(summary) !== "estate" ? ` ${gtypeTag(gameTypeOf(summary))}` : ""}</div>
      <div class="paper__body paper__body--center">
        ${tokenSvg(host?.tokenKind || "bicycle", host?.color, { size: 72, label: `${hostName}'s token` })}
        <div class="invite-title">${e(upper(hostName))}'S TABLE</div>
        <div class="invite-seats">${seats}</div>
      </div>
    </section>`;
  const buttons = [
    btn("JOIN", "", "primary", { cmd: "invite-accept" }),
    btn("JUST WATCH", "", "ghost", { cmd: "watch" }),
  ];
  return { banner: null, main: `${main}${buttonsHtml(buttons)}`, nav: false };
}

// ---------------------------------------------------------------------------------------------
// Lobby

function ruleChips(state, isHost, aiLevel) {
  const rules = rulesOf(state);
  const chips = [
    { short: "LOANS", key: "Loans between players", value: rules.loans ? "ON" : "OFF", cmd: "rule", arg: "loans" },
    { short: "AUCTIONS", key: "Auctions", value: rules.auctions ? "ON" : "OFF", cmd: "rule", arg: "auctions" },
    { short: "JACKPOT", key: "City Park jackpot", value: rules.freeParkingJackpot ? "ON" : "OFF", cmd: "rule", arg: "freeParkingJackpot" },
    { short: "CASH", key: "Starting cash", value: money(rules.startingCash), cmd: "rule", arg: "startingCash" },
  ];
  if (isHost) chips.push({ short: "COMPUTER", key: "New computer players", value: upper(aiLevelLabel(aiLevel)), cmd: "ai-level", arg: "" });
  return `<div class="rchips">${chips.map((chip) => {
    const inner = `<span>${chip.short}</span><b>${e(chip.value)}</b>`;
    if (!isHost) return `<span class="rchip" aria-label="${chip.key}: ${e(chip.value)}">${inner}</span>`;
    return `<button type="button" class="rchip" data-cmd="${chip.cmd}" data-arg="${chip.arg}" aria-label="${chip.key}: ${e(chip.value)}. Tap to change">${inner}</button>`;
  }).join("")}</div>`;
}

function lobbyScreen(state, ui = {}) {
  const player = me(state);
  const isHost = state.hostId === player?.id || state.viewerIsHost;
  const list = state.players.map((candidate) => {
    const ai = inputOf(candidate) === "ai";
    return `<li class="lseat${candidate.id === player?.id ? " is-me" : ""}">
      ${playerTokenSvg(state, candidate, { size: 32 })}
      <span class="lseat__name">${e(candidate.id === player?.id ? "YOU" : upper(candidate.name))}</span>
      ${ai ? `<span class="lseat__tag" aria-label="computer, ${e(aiLevelLabel(candidate.difficulty))}">${icon("bot", { size: 12 })}${candidate.difficulty ? `<b>${e(upper(aiLevelLabel(candidate.difficulty)))}</b>` : ""}</span>` : ""}
      ${isHost && candidate.id !== player?.id ? `<button type="button" class="lseat__remove" data-cmd="remove" data-arg="${e(candidate.id)}" aria-label="Remove ${e(candidate.name)}">×</button>` : ""}
    </li>`;
  }).join("");
  const ready = state.players.length;
  const host = playerById(state, state.hostId);
  const buttons = isHost
    ? [
      btn(ready >= 2 ? `START · ${ready} PLAYERS` : "START", ready >= 2 ? "" : "needs 2 players", "primary", { act: { type: "start" }, disabled: ready < 2 }),
      btn("+ COMPUTER", ready >= MAX_PLAYERS ? "table is full" : "", "ghost", { cmd: "add-ai", disabled: ready >= MAX_PLAYERS }),
    ]
    : [btn(host ? `WAITING FOR ${upper(host.name)}` : "WAITING FOR THE HOST", "", "ghost", { disabled: true })];
  const noPc = ui.tableOnline === false;
  const codeCard = `<section class="codecard" aria-label="Table code">
      <div class="codecard__code"><span>CODE</span><strong>${e(state.code)}</strong></div>
      <div class="codecard__btns"><button type="button" class="small-btn is-gold" data-cmd="share">INVITE</button><button type="button" class="small-btn" data-cmd="copy">${ui.copied ? "COPIED" : "COPY LINK"}</button><button type="button" class="small-btn is-wa" data-cmd="whatsapp">WHATSAPP</button></div>
      ${noPc && isHost ? `<p class="codecard__pc">${icon("screen", { size: 13 })}On a PC: open it and pick this game</p>` : ""}
    </section>`;
  // The host picks the game here too: POKER or LUDO starts a new table of that game (phone.js switchGame).
  const picker = isHost && state.viewerRole !== "spectator" ? gamePickHtml("estate", { cmd: "switch-game", subtitles: false }) : "";
  const main = `${codeCard}${picker}<ul class="lseats">${list}</ul>${ruleChips(state, isHost, ui.aiLevel || "hard")}`;
  const chips = [{ html: `${icon("deal", { size: 14 })} FRIENDS`, cmd: "open-invite" }, { html: "HOW TO PLAY", cmd: "how-to-play" }, { html: "LEAVE", cmd: "leave-ask", danger: true }];
  return { banner: [GOLD, state.viewerRole === "spectator" ? "WATCHING" : "IN THE LOBBY"], main: `${main}${buttonsHtml(buttons, chips)}`, nav: false };
}

// ---------------------------------------------------------------------------------------------
// MY DEEDS, PLAYERS

function groupBlocks(state, playerId) {
  const owned = ownedBy(state, playerId);
  const blocks = [];
  const groups = [...new Set(owned.map((index) => state.board[index]).filter((space) => space.type === "property").map((space) => space.group))];
  for (const group of groups) blocks.push({ key: group, kind: "property", indexes: owned.filter((index) => state.board[index].group === group), size: groupIndexes(state, group).length });
  const rails = owned.filter((index) => state.board[index].type === "railroad");
  if (rails.length) blocks.push({ key: "railroad", kind: "railroad", indexes: rails, size: 4 });
  const utils = owned.filter((index) => state.board[index].type === "utility");
  if (utils.length) blocks.push({ key: "utility", kind: "utility", indexes: utils, size: 2 });
  return blocks;
}

function blockTitle(block) {
  if (block.kind === "railroad") return "RAILROADS";
  if (block.kind === "utility") return "UTILITIES";
  return upper(colourWord(block.key));
}

function propertyScreen(state) {
  const player = me(state);
  if (!player) return { banner: null, main: tipHtml({ tone: "info", text: "Spectators don't own property." }) };
  const manage = canManage(state, player.id);
  const inDebt = state.phase === "debt";
  const blocks = groupBlocks(state, player.id).map((block) => {
    const color = block.kind === "property" ? GROUP_COLORS[block.key] : "#13294B";
    const ink = block.kind === "property" ? GROUP_INK[block.key] : "#FFFFFF";
    const full = block.kind === "property" && block.indexes.length === block.size;
    const badge = full ? "FULL SET" : `${block.indexes.length}/${block.size}`;
    const rows = block.indexes.map((index) => {
      const space = state.board[index];
      const prop = propState(state, index);
      const pledge = loanSecuredBy(state, index);
      const pledgeNote = pledge ? `security for ${pledge.lenderId === player.id ? "your loan" : `${possessive(playerById(state, pledge.lenderId)?.name || "a")} loan`}` : "";
      const houses = prop.houses === 5 ? '<span class="prop__hotel">HOTEL</span>' : icon("house", { size: 10 }).repeat(prop.houses || 0);
      const status = prop.mortgaged ? "mortgaged" : prop.houses ? "" : pledgeNote;
      return `<button type="button" class="dtile${pledge ? " is-pledged" : ""}${prop.mortgaged ? " is-mortgaged" : ""}" data-sheet="${index}" aria-label="${e(space.name)}"><span class="dtile__name">${e(upper(shortName(space.name)))}</span><span class="dtile__foot">${houses ? `<span class="prop__houses">${houses}</span>` : ""}${status ? `<span class="dtile__state">${e(status)}</span>` : ""}<b>${prop.mortgaged ? "—" : e(rentLabel(state, index))}</b></span></button>`;
    }).join("");
    // One action per set at most, and only on full sets: BUILD (with what you keep), or UNMORTGAGE
    // first. Mortgage, unmortgage and sell are on each deed's card, one tap away.
    let action = null;
    if (manage && full && !inDebt) {
      const build = nextBuild(state, player.id, block.key);
      const mortgagedOne = block.indexes.find((index) => propState(state, index).mortgaged);
      if (mortgagedOne !== undefined) {
        const cost = unmortgageCost(state.board[mortgagedOne]);
        action = { label: `UNMORTGAGE ${money(cost)}`, act: { type: "unmortgage", spaceIndex: mortgagedOne }, disabled: player.cash < cost, gold: true };
      } else if (build) {
        action = { html: `${icon("house")} ${build.hotel ? "HOTEL" : "BUILD"} ${money(build.cost)}<small>keep ${money(Math.max(0, player.cash - build.cost))}</small>`, act: { type: "build", spaceIndex: build.index }, disabled: !build.affordable || !build.bankOk, gold: true };
      }
    }
    const actions = action ? `<button type="button" class="group__btn${action.gold ? " group__btn--gold" : ""}" data-act='${JSON.stringify(action.act)}'${action.disabled ? " disabled" : ""}>${action.html || e(action.label)}</button>` : "";
    return `<section class="group${block.indexes.length >= 3 ? " group--wide" : ""}" style="--g:${color}">
      <div class="group__head" data-group="${e(block.kind === "property" ? block.key : block.kind)}" style="background:${color};color:${ink}"><span class="group__name">${e(blockTitle(block))}</span><span class="group__badge">${badge}</span></div>
      <div class="dtiles">${rows}</div>
      ${actions ? `<div class="group__actions">${actions}</div>` : ""}
    </section>`;
  }).join("");
  const jailCard = player.jailCards ? `<div class="chip-row"><span class="chip">${icon("unlock")} Jail pass × ${player.jailCards}</span></div>` : "";
  const empty = blocks ? "" : `<div class="tempty">${icon("card", { size: 28 })}<b>NO DEEDS YET</b></div>`;
  return { banner: null, main: `${loansHtml(state, player, { compact: true })}${blocks ? `<div class="groups">${blocks}</div>` : ""}${empty}${jailCard}` };
}

function playersScreen(state) {
  const rows = standings(state).map((entry) => {
    const player = entry.player;
    const owned = ownedBy(state, player.id);
    const swatches = owned.map((index) => {
      const space = state.board[index];
      const color = space.type === "property" ? GROUP_COLORS[space.group] : "#5B6470";
      const prop = propState(state, index);
      return `<span class="swatch${prop.mortgaged ? " is-mortgaged" : ""}" style="background:${color}" title="${e(space.name)}">${prop.houses ? `<b>${prop.houses === 5 ? "H" : prop.houses}</b>` : ""}</span>`;
    }).join("");
    return `<li class="pl-row${player.bankrupt ? " is-out" : ""}">
      ${discHtml(state, player, { size: 34 })}
      <div class="pl-row__main"><div class="pl-row__name">${e(upper(player.name))}${player.id === state.viewerId ? " · YOU" : ""}</div>
        <div class="pl-row__deeds">${swatches}</div></div>
      <div class="pl-row__cash">${player.bankrupt ? "OUT" : money(player.cash)}<small>${entry.placeLabel}</small></div>
    </li>`;
  }).join("");
  return { banner: null, main: `<ul class="pl-list">${rows}</ul>` };
}

// ---------------------------------------------------------------------------------------------
// Debt, result

// UX-04: the debt checklist (ux-screens.js).
function debtScreen(state, ui) {
  return debtChecklistScreen(state, ui);
}

// Final places: the winner, then everyone still in by net worth (what the PC shows), then the
// players who went out, the last one out placed highest. The engine log keeps only the latest
// entries, so the order this phone saw players go out (`outSeen`, names) fills in older ones.
export function finalStandings(state, outSeen = []) {
  const logOrder = bankruptcyOrder(state);
  const order = [...outSeen.filter((name) => !logOrder.includes(name)), ...logOrder];
  const exact = hasOutOrder(state);
  const rank = (player) => (exact ? player.outOrder : order.indexOf(player.name));
  const winnerFirst = (entry) => (entry.player.id === state.winnerId ? 0 : 1);
  return (state.players || [])
    .map((player) => ({ player, worth: finalScore(state, player.id), out: player.bankrupt ? rank(player) : null }))
    .sort((a, b) => winnerFirst(a) - winnerFirst(b) || Number(a.player.bankrupt) - Number(b.player.bankrupt) || (a.player.bankrupt ? b.out - a.out : b.worth - a.worth))
    .map((entry, index) => ({ ...entry, place: index + 1, placeLabel: ordinal(index + 1) }));
}

function resultScreen(state, ui = {}) {
  const player = me(state);
  const table = finalStandings(state, ui.outSeen || []);
  const mine = table.find((entry) => entry.player.id === player?.id);
  const winner = playerById(state, state.winnerId);
  const won = winner && player && winner.id === player.id;
  const finals = table.map((entry) => {
    const how = entry.player.bankrupt ? "out" : money(entry.worth);
    return `<li class="final"><span class="final__place">${entry.placeLabel}</span><span class="final__dot" style="background:${playerColor(state, entry.player)}"></span><span class="final__name">${entry.player.id === player?.id ? "You" : e(entry.player.name)}</span><span class="final__how">${e(how)}</span></li>`;
  }).join("");
  const main = `<div class="medal-wrap" data-medal><div class="medal${won ? "" : " medal--silver"}"><span class="medal__eyebrow">${won ? "WINNER" : "FINISHED"}</span><span class="medal__place">${upper(mine?.placeLabel || "")}</span></div></div>
    <div class="big-title">${won ? "YOU OWN THE TOWN" : `${e(upper(winner?.name || ""))} WINS`}</div>
    <ol class="paper finals">${finals}</ol>`;
  const isHost = Boolean(player) && state.hostId === player.id;
  const buttons = isHost ? [btn("PLAY AGAIN", "", "primary", { cmd: "rematch" })] : [btn("WAITING FOR A REMATCH", "the host starts it", "ghost", { disabled: true })];
  buttons.push(btn("LEAVE TABLE", "", "ghost", { cmd: "leave" }));
  const hasDeeds = Boolean(player) && !player.bankrupt && ownedBy(state, player.id).length > 0;
  const chips = [hasDeeds ? { label: "MY DEEDS", cmd: "tab", arg: "property" } : { label: "PLAYERS", cmd: "tab", arg: "players" }];
  return { banner: null, main: `${main}${buttonsHtml(buttons, chips)}`, nav: false };
}

// ---------------------------------------------------------------------------------------------
// MORE: table code, loans, friends, invite, sound

// Online friends you can invite to this table (on the invite screen, not in MORE mid-game).
function onlineFriendsHtml(state, ui) {
  const friends = ui.friends?.list;
  const sent = ui.friends?.sent || {};
  const rows = (friends || []).filter((friend) => friend.status === "online").slice(0, 6).map((friend) => {
    const action = sent[friend.deviceId] ? { label: "SENT", cls: "is-muted", cmd: "" } : { label: "INVITE", cls: "is-gold", cmd: "friend-invite" };
    return `<li class="person">
      ${tokenSvg(friend.tokenKind, friend.color, { size: 34 })}
      <div class="person__main"><div class="person__name">${e(upper(friend.name))}</div><div class="person__meta"><span class="dot" style="background:#4ADE80"></span>online</div></div>
      <button type="button" class="small-btn ${action.cls}" ${action.cmd ? `data-cmd="${action.cmd}" data-arg="${e(friend.deviceId)}"` : "disabled"}>${action.label}</button>
    </li>`;
  }).join("");
  return rows ? glassBox("FRIENDS ONLINE", `<ul class="people">${rows}</ul>`) : "";
}

function friendsScreen(state, ui) {
  const player = me(state);
  const code = state?.code || ui.session?.code || "";
  const tableLine = code ? `<div class="tableline"><span>TABLE</span><strong>${e(code)}</strong></div>` : "";
  const rules = state ? ruleChips(state, false) : "";
  // The host's PLAYERS (UX-02) and the loans you owe as terms cards (UX-04) come first: PLAYERS
  // on top while someone keeps the table waiting (the idle bar), else your loans on top.
  const idle = state ? idleBarHtml(state, ui) : "";
  const host = state ? hostPlayersHtml(state, ui) : "";
  const mine = player && state ? loanCardsHtml(state, player) : "";
  const main = `${idle}${idle ? host + mine : mine + host}${tableLine}${rules}${player ? loansHtml(state, player, { owedOnly: true }) : ""}`;
  const isHost = Boolean(player) && state?.hostId === player.id && state?.status === "playing";
  // Declare bankruptcy (resign) at any time in play, after the confirmation (0.10.1).
  const canResign = Boolean(player) && !player.bankrupt && state?.status === "playing" && !["determining_order", "order_countdown", "finished"].includes(state.phase);
  const buttons = [
    btn("INVITE PEOPLE", "", "primary", { cmd: "open-invite" }),
    btn("SETTINGS", "", "ghost", { cmd: "settings", html: `${icon("gear", { size: 16 })} SETTINGS` }),
    btn("HOW TO PLAY", "", "ghost", { cmd: "how-to-play" }),
    btn("LEAVE TABLE", "", "ghost", { cmd: "leave-ask" }),
    ...(canResign ? [btn("DECLARE BANKRUPTCY", "", "danger", { cmd: "bankrupt" })] : []),
    ...(isHost ? [btn("END GAME FOR EVERYONE", "", "danger", { cmd: "end-game-ask" })] : []),
  ];
  return { banner: null, main: `${main}${buttonsHtml(buttons)}` };
}


// SOUND (on/off for everything), MUSIC (off by default on phones) and EFFECTS, plus one volume
// slider. Inline in MORE, and in a sheet from the lobby.
export function audioControlsHtml(audio = {}, muted = false) {
  const settings = { music: false, effects: true, volume: 0.8, ...(audio || {}) };
  const chip = (arg, label, on) => `<button type="button" class="achip${on ? " is-on" : ""}" data-cmd="audio" data-arg="${arg}" aria-pressed="${on}">${label} <b>${on ? "ON" : "OFF"}</b></button>`;
  return `<section class="audio" aria-label="Sound">
    <div class="audio__row">${chip("muted", "SOUND", !muted)}${chip("music", "MUSIC", settings.music)}${chip("effects", "EFFECTS", settings.effects)}</div>
    <label class="audio__vol"><span>VOLUME</span><input type="range" class="range" min="0" max="100" step="5" value="${Math.round(settings.volume * 100)}" data-audio-volume aria-label="Volume"></label>
  </section>`;
}

// One labelled row of choices: [label] [A|B|C]; the chosen one is pressed.
function settingRow(label, cmd, key, options, current) {
  const buttons = options.map(([value, text]) => `<button type="button" class="sseg__btn${value === current ? " is-on" : ""}" data-cmd="${cmd}" data-arg="${key}:${value}" aria-pressed="${value === current}">${text}</button>`).join("");
  return `<div class="srow"><span class="srow__label">${label}</span><div class="sseg" role="group" aria-label="${label}">${buttons}</div></div>`;
}

// SETTINGS: sound (SOUND / MUSIC / EFFECTS, volume), then vibration, animation speed, the follow
// camera and reduced motion for this device; for the host, how fast the computers play.
// poker: extra rows for a poker table (CARD COLOURS, HAND NAMES, HAND STRENGTH), which also
// replace CAMERA (there is no board camera in poker). CAMERA uses the TV's words: FOLLOW MOVES
// (the camera follows your own hop on a phone-sized board) or OFF.
// camera: false drops CAMERA too (Ludo: its board never moves).
// app: installState() from shared/pwa.js. HOME SCREEN shows unless the app is installed (or the
// browser can't install): ADD when the browser has offered an install, otherwise the steps for this
// browser (installSteps; publicUrl for the https link when this is the home Wi-Fi address).
export function settingsHtml({ audio, muted = false, prefs = {}, host = false, computerSpeed = null, listed = null, poker = "", camera = true, app = null, publicUrl = "" } = {}) {
  const p = { vibration: true, speed: "normal", follow: true, motion: "auto", ...prefs };
  const onOff = [[true, "ON"], [false, "OFF"]];
  const rows = [
    settingRow("VIBRATION", "pref", "vibration", onOff, p.vibration),
    settingRow("ANIMATIONS", "pref", "speed", [["normal", "NORMAL"], ["fast", "FAST"]], p.speed),
    poker || camera === false ? "" : settingRow("CAMERA", "pref", "follow", [[true, "FOLLOW MOVES"], [false, "OFF"]], p.follow),
    settingRow("REDUCED MOTION", "pref", "motion", [["auto", "AUTO"], ["reduce", "ON"], ["full", "OFF"]], p.motion),
  ];
  const room = host && computerSpeed
    ? `<h3 class="sgroup">THIS TABLE</h3>${settingRow("COMPUTERS", "computer-speed", "speed", [["slow", "SLOW"], ["normal", "NORMAL"], ["fast", "FAST"]], computerSpeed)}${listed === null ? "" : `${settingRow("PUBLIC GAME", "room-listed", "listed", onOff, listed)}<p class="snote">On: anyone on this server sees it in their games list.</p>`}`
    : "";
  const home = !canOfferInstall(app)
    ? ""
    : `<div class="srow"><span class="srow__label">HOME SCREEN</span>${app === "prompt" ? '<div class="sseg" role="group" aria-label="HOME SCREEN"><button type="button" class="sseg__btn" data-cmd="install-app">ADD</button></div>' : ""}</div><p class="snote">${e(installSteps(app, { publicUrl }))}</p>`;
  return `<div class="settings"><h3 class="sgroup">SOUND</h3>${audioControlsHtml(audio, muted)}<h3 class="sgroup">THIS DEVICE</h3>${rows.join("")}${home}${poker || ""}${room}</div>`;
}

// HOW TO PLAY (Estate): five steps, from MORE and once on a phone's first join (review 4 Nov #6;
// poker has its own). The steps follow this table's rules (auctions, loans).
export function howToPlayHtml(state = null) {
  const rules = rulesOf(state);
  const steps = [
    ["dice", "ROLL AND MOVE", "On your turn, tap ROLL. Your token moves round the board; the TV follows it."],
    ["house", "BUY STREETS", rules.auctions ? "Land on a free street: BUY it, or it goes to AUCTION and anyone can bid." : "Land on a free street and BUY it if you want it."],
    ["coin", "COLLECT RENT", "Anyone who lands on your street pays you rent. A whole colour set doubles it; then BUILD houses in MY DEEDS for far more."],
    ["swap", "TRADE", `Swap deeds and cash with anyone in TRADE to finish a set.${rules.loans ? " Players can lend each other money too (MORE)." : ""}`],
    ["out", "DON'T GO BROKE", "Short of cash? Sell houses or MORTGAGE deeds. Can't pay, and you're out. The last player left wins."],
  ];
  const cards = steps.map(([glyph, title, text], index) => `<li class="howto__step"><span class="howto__num">${index + 1}</span><span class="howto__icon">${icon(glyph, { size: 22 })}</span><div><b>${title}</b><p>${e(text)}</p></div></li>`).join("");
  return `<ol class="howto">${cards}</ol><p class="howto__tip">Tap any square on the board to see its deed.</p>`;
}

function inviteScreen(state, ui) {
  const code = state?.code || ui.session?.code || "";
  const link = ui.inviteLink || "";
  const qr = ui.qrUrl
    ? `<img class="qr" src="${e(ui.qrUrl)}" width="150" height="150" alt="QR code for the invite link to table ${e(code)}">`
    : `<div class="qr qr--placeholder" role="img" aria-label="QR code unavailable"><span>QR</span></div>`;
  const main = `<section class="paper">
      <div class="paper__head">TABLE ${e(code)}</div>
      <div class="paper__body paper__body--center">
        ${qr}
        <div class="link-box"><span>${e(link.replace(/^https?:\/\//, ""))}</span></div>
        ${ui.linkIsLan ? '<div class="note note--ink note--small">Same Wi-Fi only</div>' : ""}
      </div>
    </section>
    ${onlineFriendsHtml(state, ui)}`;
  const buttons = [
    btn("SHARE LINK", "", "primary", { cmd: "share" }),
    btn("WHATSAPP", "", "ghost", { cmd: "whatsapp" }),
    btn(ui.copied ? "COPIED" : "COPY LINK", "", "ghost", { cmd: "copy" }),
    btn("BACK", "", "ghost", { cmd: "close-invite" }),
  ];
  // From the lobby there are no game tabs yet.
  return { banner: null, main: `${main}${buttonsHtml(buttons)}`, nav: state?.status === "lobby" ? false : undefined };
}

// ---- Loans (house rule) ----------------------------------------------------------------------

// owedOnly: just the loans others owe you (MORE shows the ones you owe as terms cards).
function loansHtml(state, player, { compact = false, owedOnly = false } = {}) {
  const loans = activeLoans(state, player.id).filter((loan) => !owedOnly || loan.borrowerId !== player.id);
  if (!loans.length) return "";
  const myTurn = state.currentPlayerId === player.id && ["awaiting_roll", "turn_complete"].includes(state.phase);
  const rows = loans.map((loan) => {
    const borrowing = loan.borrowerId === player.id;
    const other = playerById(state, borrowing ? loan.lenderId : loan.borrowerId);
    const security = (loan.collateral || []).map((index) => state.board[index]?.name).filter(Boolean);
    const text = borrowing ? `You owe ${other?.name} ${money(loan.repay)}` : `${other?.name} owes you ${money(loan.repay)}`;
    const meta = [loanDue(state, loan), security.length ? `security: ${security.join(", ")}` : ""].filter(Boolean).join(" · ");
    const repay = borrowing && !compact
      ? `<button type="button" class="small-btn is-gold" data-act='${JSON.stringify({ type: "repay_loan", loanId: loan.id })}' ${myTurn && player.cash >= loan.repay ? "" : "disabled"} aria-label="Repay ${money(loan.repay)} to ${e(other?.name || "")}">REPAY</button>`
      : "";
    return `<li class="person"><span class="loan-dot loan-dot--${borrowing ? "owe" : "owed"}" aria-hidden="true">$</span><div class="person__main"><div class="person__name">${e(text)}</div><div class="person__meta">${e(meta)}</div></div>${repay}</li>`;
  }).join("");
  return glassBox("LOANS", `<ul class="people">${rows}</ul>`);
}

function loanScreen(state) {
  const player = me(state);
  const offer = state.loanOffer;
  const incoming = offer.targetId === player.id;
  const parties = offerParties(offer);
  const other = playerById(state, incoming ? offer.proposerId : offer.targetId);
  const lending = parties.lenderId === player.id;
  const security = (offer.collateral || []).map((index) => state.board[index]?.name).filter(Boolean);
  const title = incoming
    ? (offer.direction === "borrow" ? `${upper(other.name)} ASKS TO BORROW` : `${upper(other.name)} OFFERS A LOAN`)
    : `WAITING FOR ${upper(other.name)}`;
  const sentence = loanTerms(state, offer, player.id).split(/(?<=\.) /)[0];
  const main = `<section class="paper"><div class="paper__head">${e(title)}</div><div class="paper__body paper__body--center">
      <div class="loan-big">${money(offer.principal)} <span>→</span> ${money(offer.repay)}</div>
      <p class="loan-line">${e(sentence)}</p>
      ${security.length ? `<div class="chip-row chip-row--ink">${icon("lock", { size: 12 })} ${e(security.join(", "))}</div>` : ""}
    </div></section>`;
  const lenderCash = playerById(state, parties.lenderId)?.cash ?? 0;
  const buttons = incoming
    ? [
      btn(lending ? `LEND ${money(offer.principal)}` : `TAKE ${money(offer.principal)}`, lending && lenderCash < offer.principal ? "not enough cash" : "", "primary", { act: { type: "accept_loan" }, disabled: lending && lenderCash < offer.principal }),
      btn("DECLINE", "", "ghost", { act: { type: "reject_loan" } }),
    ]
    : [btn("CANCEL REQUEST", "", "ghost", { act: { type: "reject_loan" } })];
  return { banner: null, main: `${main}${buttonsHtml(buttons)}` };
}

const SCREENS = {
  join: joinScreen, invited: invitedScreen, lobby: lobbyScreen, property: propertyScreen, debt: debtScreen, timedAuction: timedAuctionScreen,
  trade: tradeScreen, result: resultScreen, friends: friendsScreen, invite: inviteScreen, players: playersScreen,
  loan: loanScreen, loanDraft: loanComposeScreen,
};

// The board tab's decision area under the status line: extra controls (bid tiles), then buttons.
function boardMain(built) {
  return `${built.extra || ""}${buttonsHtml(built.buttons, built.chips)}`;
}

export function renderPhone(state, ui = {}) {
  const screen = route(state, ui);
  if (isBoardScreen(screen)) {
    const built = boardScreen(screen, state, ui);
    return {
      screen,
      header: headerHtml(state, screen, ui),
      // The host's idle bar (UX-02): SAM IDLE 1:40 · PLAYERS, over the board.
      banner: idleBarHtml(state, ui),
      main: boardMain(built),
      nav: navHtml(state, screen, ui),
      board: { stage: built.stage, status: built.status ? statusHtml(state, built.status) : "", heat: built.heat, marks: built.marks, bigRent: Boolean(built.bigRent) },
    };
  }
  const built = SCREENS[screen](state, ui);
  const showNav = built.nav !== false;
  return {
    screen,
    header: headerHtml(state, screen, ui),
    banner: bannerHtml(built.banner),
    main: built.main,
    nav: showNav ? navHtml(state, screen, ui) : "",
    board: null,
    ambient: ambientBoard(state, ui),
  };
}

// The board behind a page in a game (MY DEEDS, TRADE, MORE, debt, an offer, the result): the
// desk layout keeps it on the left, with what the BOARD tab would show there and a status line.
export function ambientBoard(state, ui = {}) {
  if (!state || !ui.session || state.status === "lobby") return null;
  const turn = route(state, { ...ui, tab: "turn", loanDraft: null, inviteOpen: false });
  if (isBoardScreen(turn)) {
    const built = boardScreen(turn, state, ui);
    return { stage: built.stage, status: built.status ? statusHtml(state, built.status) : "", heat: built.heat, marks: built.marks, bigRent: false };
  }
  const current = playerById(state, state.currentPlayerId);
  const winner = playerById(state, state.winnerId);
  const mine = state.currentPlayerId === state.viewerId;
  const status = state.status === "finished"
    ? { text: winner ? `${upper(winner.name)} WINS` : "GAME OVER", playerId: winner?.id, tone: winner?.id === state.viewerId ? "gold" : "" }
    : turn === "debt" ? { text: "RAISE CASH", playerId: state.viewerId, tone: "gold" }
      : turn === "timedAuction" ? auctionStatus(state, state.viewerId)
      : turn === "trade" ? { text: "TRADE OFFER", playerId: state.trade?.proposerId }
        : turn === "loan" ? { text: "LOAN OFFER", playerId: state.loanOffer?.proposerId }
          : current ? { text: mine ? "YOUR TURN" : `${upper(current.name)}'S TURN`, playerId: current.id, tone: mine ? "gold" : "" } : null;
  return { stage: "", status: status ? statusHtml(state, status) : "", heat: null, marks: null, bigRent: false };
}

export { debtPlan, netWorth, rentFor, dieHtml, diceHtml };
