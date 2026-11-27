// The quiz on the table screen (TV / PC at /): the board of six categories, each clue big enough
// to read across a room, the four choices the moment they open, and a plate per player along the
// bottom. docs/QUIZ-API.md is the contract; §9 sets the sizes. This is a 1600 × 900 stage scaled to
// fit (table.js fit()), so at 1080p every px here is 1.2 screen px: the clue is 54 (65 on screen),
// the choices 36 (43), board values 56 (67), category titles 26 (31), and nothing is under 20 (24).
//
// The quiz is phones only (final bets are secret): no keyboard or controller seats, so this screen
// is a viewer that also lets the host start the game and change the settings. States apply at once:
// no animation queue may hold back a clue (the 7 Nov review found the TV 1.7 s behind on queues).
// Entrances are CSS only, and the choices appear when the synced clock reaches clue.openAt.

import { aiLevelLabel, escapeHtml, upper } from "../shared/format.js";
import { morph } from "../shared/morph.js";
import { gamePickHtml, gtypeTag } from "../shared/poker-view.js";
import { activeClue, choicesOpen, readProgress, scheduleAt, secondsLeft } from "../shared/quiz-clock.js";
import {
  LETTERS, categoryColor, cluesLeft, creditsHtml, fitTitleSize, fmtScore, howToPlayHtml, isQuizState, outcomeFor, phaseLine, placeWord, playerOf, quizSounds, rankings, roundSubtitle, roundTitle, seatedPlayers,
  settingChips, signedPoints,
} from "../shared/quiz-view.js";
import { playerColor, playerTokenSvg } from "../shared/tokens.js";
import { ludoSettingsOverlayHtml } from "./ludo-table.js";
import * as views from "./views.js";

const e = escapeHtml;
const { ICONS } = views;
const AI_KEY = "estate.quizAiLevel";

export { isQuizState };

// How long the intro shows the round title before the first category, and between categories
// (docs §4: 3 s + 0.6 s per category).
const INTRO_FIRST_MS = 1200;
const INTRO_STEP_MS = 600;

function qrHtml(state, ctx) {
  const code = state?.code || "·····";
  if (ctx.qrUrl) return `<img class="qr qr--img" src="${e(ctx.qrUrl)}" width="300" height="300" alt="QR code that opens the join page for table ${e(code)}">`;
  return views.qrPlaceholderHtml({ label: `QR code for table ${code}`, note: ctx.demo ? "DEMO" : "" });
}

function toolsHtml(ctx, { conn = true } = {}) {
  return `<span class="pk-tv__tools">${conn ? `<span class="side__conn" data-status="${e(ctx.status || "live")}"><span class="dot" aria-hidden="true"></span><span class="side__conn-text">${ctx.status === "offline" ? "OFFLINE" : ctx.demo ? "DEMO" : ""}</span></span>` : ""}<button class="icon-btn" type="button" data-cmd="qz-help" aria-label="How to play" title="How to play">${ICONS.help}</button>${views.soundButtonHtml(ctx.sound)}<button class="icon-btn menu-btn" type="button" data-cmd="menu" aria-haspopup="dialog" aria-label="Menu" title="Menu: settings, how to play, leave">${ICONS.menu}</button></span>`;
}

function levelOf(state, wanted) {
  const levels = Array.isArray(state?.aiLevels) && state.aiLevels.length ? state.aiLevels : ["easy", "normal", "hard"];
  return { levels, level: levels.includes(wanted) ? wanted : levels.includes("normal") ? "normal" : levels[0] };
}

function deviceWord(player) {
  if (player.kind === "ai" || player.input === "ai") return upper(aiLevelLabel(player.difficulty || "normal"));
  return player.online === false ? "AWAY" : "PHONE";
}

// ---------------------------------------------------------------------------------------------
// Lobby

export function quizLobbyHtml(state, ctx = {}) {
  const watching = ctx.watching;
  const code = state.code || "·····";
  const link = views.joinUrl(state, ctx).replace(/^https?:\/\//, "");
  const reach = views.joinReach(ctx) === "anywhere"
    ? `<span class="reach reach--anywhere">${ICONS.globe}<b>ANYWHERE</b></span>`
    : `<span class="reach reach--lan">${ICONS.wifi}<b>SAME WI‑FI</b></span>`;
  const players = seatedPlayers(state);
  const max = Math.min(8, state.seatLimit || 8);
  const host = !watching && state.viewerRole === "table";
  const seats = [];
  for (let index = 0; index < max; index += 1) {
    const player = players[index];
    if (player) {
      seats.push(`<li class="qz-seat" data-key="qzs-${e(player.id)}" style="--pc:${playerColor(state, player)}">${playerTokenSvg(state, player, { size: 56, label: `${player.name}'s token` })}<b>${e(upper(player.name))}</b><span class="qz-seat__dev">${e(deviceWord(player))}</span>${host ? views.removeSeatButtonHtml(player) : ""}</li>`);
    } else if (index === players.length) {
      seats.push(`<li class="qz-seat is-open is-hint"><span class="qz-seat__phone" aria-hidden="true">${ICONS.phone}</span><small>SCAN TO JOIN</small></li>`);
    } else {
      seats.push('<li class="qz-seat is-open"><span class="qz-seat__plus" aria-hidden="true">+</span><span class="sr-only">Open seat</span></li>');
    }
  }
  const chips = settingChips(state).map((chip) => {
    const inner = `<span>${e(chip.name)}</span><b>${e(chip.label)}</b>${chip.detail ? `<small>${e(chip.detail)}</small>` : ""}`;
    return host
      ? `<button type="button" class="qz-chip${chip.custom ? " is-custom" : ""}" data-cmd="qz-setting" data-arg="${e(chip.key)}" aria-label="${e(chip.name)}: ${e(chip.label)}. Change">${inner}</button>`
      : `<span class="qz-chip">${inner}</span>`;
  }).join("");
  const { levels, level } = levelOf(state, ctx.aiLevel);
  const ready = players.length;
  const foot = host ? `<div class="pk-tv__foot qz-tv__foot">
      <span class="qz-count">${ready} / ${max}</span>
      <span class="ai-pick"><button class="chip-btn" type="button" data-cmd="qz-add-ai" ${ready >= max ? "disabled" : ""} aria-label="Add a ${e(aiLevelLabel(level))} computer player">${ICONS.cpu}<span>ADD COMPUTER</span></button>${levels.length > 1 ? `<button class="chip-btn chip-btn--level" type="button" data-cmd="qz-ai-level" aria-label="Computer level: ${e(aiLevelLabel(level))}. Change">${e(upper(aiLevelLabel(level)))} <span aria-hidden="true">⇄</span></button>` : ""}</span>
      <button class="ready-btn" type="button" data-cmd="start" ${ready >= 1 ? "" : "disabled"} aria-label="${ready >= 1 ? `Start the quiz with ${ready} ${ready === 1 ? "player" : "players"}` : "Waiting for a player"}">START ${ICONS.arrow}</button>
    </div>` : `<div class="pk-tv__foot qz-tv__foot"><span class="qz-count">${ready} / ${max}</span><span class="qz-note">Waiting for the host to start</span></div>`;
  return `<header class="pk-tv__head qz-head"><span class="join__brand">ESTATE</span>${gtypeTag("quiz")}${watching ? "" : views.resumeFormHtml({ code: ctx.resumeCode || "", error: ctx.resumeError || "" })}${watching ? "" : views.newTableButtonHtml()}${toolsHtml(ctx, { conn: false })}</header>
    <div class="pk-tv__lobby qz-tv__lobby">
      <section class="qr-card on-light" aria-label="Scan to join">
        <div class="qr-card__head">${watching ? "WATCHING" : "SCAN TO JOIN"}</div>
        <div class="qr-card__body">${qrHtml(state, ctx)}
          <div class="qr-card__code"><span class="qr-card__code-value" aria-label="Table code ${e(code.split("").join(" "))}">${e(code)}</span></div>
          <div class="qr-card__foot">${reach}<span class="qr-card__link">${e(link)}</span></div>${watching ? "" : views.inviteButtonsHtml()}
        </div>
      </section>
      <div class="pk-tv__right qz-tv__right">
        ${watching ? "" : gamePickHtml("quiz", { cmd: "pick-game", subtitles: false })}
        <ul class="qz-seats">${seats.join("")}</ul>
        <p class="qz-phones">${ICONS.phone}<span><b>Everyone plays on their own phone.</b> Bets in the final are secret, so there are no keyboard or controller seats.</span></p>
        <div class="qz-chips">${chips}</div>
        ${foot}
      </div>
    </div>`;
}

// ---------------------------------------------------------------------------------------------
// Pieces of the game screen

function headHtml(state, ctx) {
  const now = ctx.now;
  const final = Number(state.round) >= 3;
  const left = state.board ? cluesLeft(state.board) : 0;
  const round = `<span class="qz-head__round"><b>${e(roundTitle(state))}</b>${state.board && !final ? `<small>${left} ${left === 1 ? "CLUE" : "CLUES"} LEFT</small>` : ""}</span>`;
  const who = ["pick", "bet"].includes(state.phase) ? playerOf(state, state.phase === "bet" ? state.bet?.playerId : state.chooserId) : null;
  const color = who ? playerColor(state, who) : "";
  const line = phaseLine(state);
  const timed = ["pick", "bet", "finalBet"].includes(state.phase) && state.phaseEndsAt > 0;
  const seconds = timed ? secondsLeft(state.phaseEndsAt, now) : 0;
  const timer = timed ? `<span class="qz-timer${seconds <= 3 ? " is-low" : ""}" aria-label="${seconds} seconds left"><b>${seconds}</b><small>S</small></span>` : "";
  return `<header class="pk-tv__head qz-head"><span class="table-code">${views.tableCodeHtml(state.code)}</span>${round}
    <span class="qz-line${who ? " has-who" : ""}" data-key="line-${e(state.phase)}"${who ? ` style="--pc:${color}"` : ""}>${who ? playerTokenSvg(state, who, { size: 44 }) : ""}<b>${e(line)}</b></span>${timer}${toolsHtml(ctx)}</header>`;
}

// One plate per player along the bottom: token, name, score, and what they are doing now.
const FINAL_PHASES = ["finalIntro", "finalBet", "finalRead", "finalAnswer", "finalReveal"];

function plateHtml(state, player, ctx) {
  const color = playerColor(state, player);
  const phase = state.phase;
  let tag = "";
  let tone = "";
  let delta = "";
  if (player.status === "left") tag = "LEFT";
  else if (phase === "reveal") {
    const { result, outcome, delta: change, noAnswer } = outcomeFor(state, player.id);
    if (outcome === "won") { tag = "FIRST"; tone = "win"; delta = signedPoints(change); }
    else if (outcome === "lost") { tag = noAnswer ? "NO ANSWER" : "WRONG"; tone = "lose"; delta = signedPoints(change); }
    else if (outcome === "late") { tag = result?.correct ? "RIGHT · LATE" : "LATE"; tone = "late"; }
    else if (outcome === "right") { tag = "RIGHT"; tone = "late"; }
  } else if (["answer", "read"].includes(phase) && (state.clue?.answered || []).includes(player.id)) { tag = "LOCKED IN"; tone = "locked"; }
  else if (phase === "finalBet" && (state.final?.betsIn || []).includes(player.id)) { tag = "BET IN"; tone = "locked"; }
  else if (["finalAnswer", "finalRead"].includes(phase) && (state.final?.answersIn || []).includes(player.id)) { tag = "LOCKED IN"; tone = "locked"; }
  else if (state.clue?.gold && ["read", "answer"].includes(phase) && !(state.clue.eligible || []).includes(player.id)) { tag = "WATCHING"; tone = "dim"; }
  // Control means nothing in the final (everyone bets and answers), so it is not marked there.
  else if ((player.isChooser || state.chooserId === player.id) && !FINAL_PHASES.includes(phase)) { tag = phase === "pick" ? "PICKING" : "IN CONTROL"; tone = "chooser"; }
  else if (player.online === false) tag = "AWAY";
  const chooser = (player.isChooser || state.chooserId === player.id) && !FINAL_PHASES.includes(phase);
  const classes = [...new Set(["qz-plate", chooser ? "is-chooser" : "", tone ? `is-${tone}` : "", player.status === "left" ? "is-left" : ""].filter(Boolean))].join(" ");
  const check = tone === "locked" ? `<span class="qz-plate__check" aria-hidden="true">${ICONS.check}</span>` : "";
  return `<li class="${classes}" data-key="qzp-${e(player.id)}" style="--pc:${color}">
    <span class="qz-plate__top">${playerTokenSvg(state, player, { size: 40 })}<b class="qz-plate__name">${e(upper(player.name))}</b>${check}</span>
    <span class="qz-plate__bottom"><span class="qz-plate__score${player.score < 0 ? " is-neg" : ""}">${fmtScore(player.score)}</span>${delta ? `<span class="qz-plate__delta is-${tone}">${e(delta)}</span>` : ""}</span>${tag ? `<span class="qz-plate__tag">${e(tag)}</span>` : ""}
  </li>`;
}

function platesHtml(state, ctx) {
  const players = state.players || [];
  return `<ol class="qz-plates" style="--n:${Math.max(4, players.length)}" aria-label="Scores">${players.map((player) => plateHtml(state, player, ctx)).join("")}</ol>`;
}

// Each card's title box in stage px (the card less its border, colour bar and padding: keep in
// step with .qz-col, .qz-cat, .qz-intro__cats and .qz-final-intro in quiz.css), its usual title size
// and the floor: 24 stage px is 28.8 px at 1080p (docs §9: category titles ≥ 28 px).
export const TITLE_BOXES = Object.freeze({
  board: { width: 221, height: 80, max: 26, min: 24 },
  intro: { width: 194, height: 134, max: 34, min: 24 },
  final: { width: 788, height: 164, max: 64, min: 24 },
});

// A deed-card category header: the group colour bar, then the title, sized to fit its card.
function categoryCardHtml(category, { fit = "board", style = "" } = {}) {
  const color = categoryColor(category.index);
  const size = fitTitleSize(category.title, TITLE_BOXES[fit]);
  return `<div class="qz-cat${fit === "board" ? "" : " qz-cat--big"}" style="--cat:${color.bg};--cat-ink:${color.ink}${style}"><span class="qz-cat__bar" aria-hidden="true"></span><span class="qz-cat__title" style="font-size:${size}px">${e(category.title)}</span></div>`;
}

export function boardHtml(state, { picked = null } = {}) {
  const categories = state.board?.categories || [];
  const cols = categories.map((category) => {
    const color = categoryColor(category.index);
    const cells = (category.cells || []).map((cell) => {
      const isPicked = picked && picked.categoryIndex === category.index && picked.row === cell.row;
      const classes = ["qz-cell", cell.played ? "is-played" : "", cell.gold ? "is-gold" : "", isPicked ? "is-picked" : ""].filter(Boolean).join(" ");
      const label = cell.played ? `${category.title} ${cell.value}: played${cell.gold ? ", a gold square" : ""}` : `${category.title} for ${cell.value}`;
      const inner = cell.played ? (cell.gold ? `<span class="qz-cell__gold" aria-hidden="true">★</span>` : "") : `<span class="qz-cell__value">${cell.value}</span>`;
      return `<div class="${classes}" role="gridcell" aria-label="${e(label)}">${isPicked && cell.played && !cell.gold ? `<span class="qz-cell__value">${cell.value}</span>` : inner}</div>`;
    }).join("");
    return `<div class="qz-col" role="row" data-key="col-${category.index}" style="--cat:${color.bg};--cat-ink:${color.ink}">${categoryCardHtml(category)}${cells}</div>`;
  }).join("");
  return `<div class="qz-board" role="grid" aria-label="The board: ${cluesLeft(state.board)} clues left">${cols}</div>`;
}

// The clue panel: category and value, the text, the reading bar, then the four choices (once the
// synced clock passes openAt), who has tapped, and the seconds left.
function clueHtml(state, ctx) {
  const clue = activeClue(state);
  if (!clue) return "";
  const now = ctx.now;
  const open = choicesOpen(state, now);
  const reveal = state.phase === "reveal" || state.phase === "finalReveal";
  const final = clue.final;
  const color = final ? { bg: "#F2C14E", ink: "#15191E" } : categoryColor(clue.categoryIndex);
  const bet = !final && clue.gold && state.bet ? state.bet : null;
  const finder = bet ? playerOf(state, bet.playerId) : null;
  const meta = `<div class="qz-clue__meta">
      <span class="qz-tag" style="--cat:${color.bg};--cat-ink:${color.ink}"><i aria-hidden="true"></i>${e(final ? `THE FINAL · ${clue.category}` : clue.category)}</span>
      ${final ? "" : `<span class="qz-clue__value">${bet?.amount ? `BET ${fmtScore(bet.amount)}` : fmtScore(clue.value)}</span>`}
      ${finder ? `<span class="qz-goldtag"><span aria-hidden="true">★</span> GOLD SQUARE · ONLY ${e(upper(finder.name))} ANSWERS</span>` : ""}
    </div>`;
  const length = String(clue.text || "").length;
  const text = `<p class="qz-clue__text${length > 110 ? " is-long" : ""}">${e(clue.text)}</p>`;
  let bar = "";
  if (!open) {
    const progress = readProgress(clue, now);
    bar = `<div class="qz-bar qz-bar--read" role="progressbar" aria-label="Reading time" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${Math.round(progress * 100)}"><i style="transform:scaleX(${progress.toFixed(3)})"></i></div>`;
  }
  // Before openAt: four empty places (letters only; the choice text is not in the page yet).
  let choices = `<ol class="qz-choices is-waiting" data-key="slots-${e(clue.key)}" aria-hidden="true">${LETTERS.map((letter) => `<li class="qz-choice"><span class="qz-choice__letter">${letter}</span></li>`).join("")}</ol>`;
  if (open) {
    const right = reveal && Number.isInteger(clue.answerIndex) ? clue.answerIndex : -1;
    const pickers = reveal ? choosersByChoice(state, clue) : null;
    choices = `<ol class="qz-choices${reveal ? " is-reveal" : ""}" data-key="choices-${e(clue.key)}">${(clue.choices || []).map((choice, index) => {
      const cls = right === index ? " is-right" : reveal ? " is-wrong" : "";
      const who = pickers?.[index]?.length ? `<span class="qz-choice__who">${pickers[index].map((player) => playerTokenSvg(state, player, { size: 34, label: player.name })).join("")}</span>` : "";
      return `<li class="qz-choice${cls}"><span class="qz-choice__letter">${LETTERS[index]}</span><span class="qz-choice__text">${e(choice)}</span>${right === index ? `<span class="qz-choice__tick" aria-label="right answer">${ICONS.check}</span>` : ""}${who}</li>`;
    }).join("")}</ol>`;
  }
  const foot = footHtml(state, clue, ctx, { open, reveal });
  return `<section class="qz-clue${final ? " is-final" : ""}${finder ? " is-gold" : ""}${open ? " is-open" : ""}" data-key="clue-${e(clue.key)}" style="--cat:${color.bg};--cat-ink:${color.ink}" aria-label="Clue">
    ${meta}${text}${choices}${bar}${foot}</section>`;
}

// Who tapped which choice (shown at the reveal only; docs §3.3 results / §3.5 results).
function choosersByChoice(state, clue) {
  const out = [[], [], [], []];
  for (const result of clue.results || []) {
    const player = playerOf(state, result.playerId);
    if (player && Number.isInteger(result.choice) && out[result.choice]) out[result.choice].push(player);
  }
  return out;
}

function footHtml(state, clue, ctx, { open, reveal }) {
  const now = ctx.now;
  if (state.phase === "reveal") return revealLineHtml(state);
  if (state.phase === "finalReveal") return "";
  const ids = clue.final ? state.final?.answersIn || [] : clue.answered || [];
  const total = clue.final ? seatedPlayers(state).filter((player) => player.status === "playing").length : (clue.eligible || []).length;
  const dots = ids.map((id) => playerOf(state, id)).filter(Boolean).map((player) => `<span class="qz-dot" data-key="dot-${e(player.id)}" style="--pc:${playerColor(state, player)}">${playerTokenSvg(state, player, { size: 40, label: `${player.name} has answered` })}</span>`).join("");
  if (!open) {
    const left = Math.max(0, (clue.openAt || 0) - now);
    return `<div class="qz-clue__foot"><span class="qz-foot__hint">ANSWERS APPEAR ON EVERY PHONE IN <b>${Math.ceil(left / 1000)}</b></span></div>`;
  }
  const seconds = secondsLeft(clue.closesAt, now);
  const span = Math.max(1, (clue.closesAt || 0) - (clue.openAt || 0));
  const fraction = Math.max(0, Math.min(1, ((clue.closesAt || 0) - now) / span));
  return `<div class="qz-clue__foot">
      <span class="qz-answered"><span class="qz-answered__label">ANSWERED <b>${ids.length}</b>/${total}</span><span class="qz-dots">${dots}</span></span>
      <span class="qz-count-down${seconds <= 3 ? " is-low" : ""}" aria-label="${seconds} seconds left"><span class="qz-bar qz-bar--time" aria-hidden="true"><i style="transform:scaleX(${fraction.toFixed(3)})"></i></span><b>${seconds}</b></span>
    </div>`;
}

function revealLineHtml(state) {
  const clue = state.clue || {};
  const chips = [];
  const chip = (player, outcome, word) => `<span class="qz-res is-${outcome}" style="--pc:${playerColor(state, player)}">${playerTokenSvg(state, player, { size: 36 })}<b>${e(upper(player.name))}</b><span>${e(word)}</span></span>`;
  for (const result of clue.results || []) {
    const player = playerOf(state, result.playerId);
    if (!player) continue;
    const { outcome, delta } = outcomeFor(state, player.id);
    const word = outcome === "won" || outcome === "lost" ? signedPoints(delta) : result.correct ? "LATE" : "LATE · NO CHANGE";
    chips.push(chip(player, outcome, word));
  }
  // A gold square's finder who never tapped has no results row but still loses the bet.
  const finder = clue.gold ? playerOf(state, state.bet?.playerId) : null;
  const finderOutcome = finder ? outcomeFor(state, finder.id) : null;
  if (finderOutcome?.noAnswer && finderOutcome.outcome === "lost") chips.push(chip(finder, "lost", signedPoints(finderOutcome.delta)));
  const winner = playerOf(state, clue.winnerId);
  const lead = winner ? "" : `<span class="qz-res__none">${clue.gold ? ((clue.results || []).length ? "WRONG ON A GOLD SQUARE" : "NO ANSWER ON A GOLD SQUARE") : (clue.results || []).length ? "NOBODY RIGHT IN TIME" : "NOBODY ANSWERED"}</span>`;
  return `<div class="qz-clue__foot qz-clue__foot--reveal">${lead}${chips.join("")}</div>`;
}

// ---------------------------------------------------------------------------------------------
// Phase bodies

function introHtml(state, ctx) {
  const categories = state.board?.categories || [];
  const elapsed = Math.max(0, Number(ctx.introElapsed) || 0);
  const cards = categories.map((category, index) => {
    const delay = INTRO_FIRST_MS + INTRO_STEP_MS * index - elapsed;
    return `<li style="--d:${Math.round(delay)}ms">${categoryCardHtml(category, { fit: "intro" })}</li>`;
  }).join("");
  return `<div class="qz-intro"><div class="qz-intro__title">${e(roundTitle(state))}</div><div class="qz-intro__sub">${e(roundSubtitle(state))}</div><ol class="qz-intro__cats">${cards}</ol></div>`;
}

function goldHtml(state) {
  const finder = playerOf(state, state.bet?.playerId || state.chooserId);
  const clue = state.clue || {};
  const color = finder ? playerColor(state, finder) : "#F2C14E";
  return `<div class="qz-over"><div class="qz-goldcard" style="--pc:${color}">
    <span class="qz-goldcard__star" aria-hidden="true">★</span>
    <span class="qz-goldcard__title">GOLD SQUARE</span>
    <span class="qz-goldcard__who">${finder ? playerTokenSvg(state, finder, { size: 56 }) : ""}<b>${e(upper(finder?.name || ""))}</b></span>
    <span class="qz-goldcard__line">found it under ${e(clue.category || "")} for ${fmtScore(clue.value)}</span>
  </div></div>`;
}

function betHtml(state, ctx) {
  const bet = state.bet || {};
  const finder = playerOf(state, bet.playerId);
  const clue = state.clue || {};
  const seconds = secondsLeft(state.phaseEndsAt, ctx.now);
  return `<div class="qz-over"><div class="qz-goldcard qz-goldcard--bet" style="--pc:${finder ? playerColor(state, finder) : "#F2C14E"}">
    <span class="qz-goldcard__title">GOLD SQUARE · ${e(clue.category || "")}</span>
    <span class="qz-goldcard__who">${finder ? playerTokenSvg(state, finder, { size: 56 }) : ""}<b>${e(upper(finder?.name || ""))} IS BETTING</b></span>
    <span class="qz-goldcard__range">${fmtScore(bet.min)} TO ${fmtScore(bet.max)}</span>
    <span class="qz-goldcard__line">Before seeing the clue. Right wins the bet, wrong loses it. Nobody else can answer.</span>
    ${seconds ? `<span class="qz-goldcard__time">${seconds} S</span>` : ""}
  </div></div>`;
}

function roundEndHtml(state) {
  const rows = rankings(state);
  const best = Math.max(1, ...rows.map((row) => Math.abs(row.score)));
  const next = Number(state.round) < Number(state.rounds || 1) ? "ROUND 2 NEXT · DOUBLE VALUES" : "THE FINAL IS NEXT";
  return `<div class="qz-roundend">
    <div class="qz-roundend__title">${e(roundTitle(state))} COMPLETE</div>
    <div class="qz-roundend__sub">${e(next)}</div>
    <ol class="qz-ranks">${rows.map((row) => `<li class="qz-rank" data-key="qzr-${e(row.player.id)}" style="--pc:${playerColor(state, row.player)};--w:${Math.max(0, row.score) / best}">
      <span class="qz-rank__place">${e(placeWord(row.place))}</span>${playerTokenSvg(state, row.player, { size: 44 })}<b class="qz-rank__name">${e(upper(row.player.name))}</b>
      <span class="qz-rank__bar" aria-hidden="true"><i></i></span><span class="qz-rank__score${row.score < 0 ? " is-neg" : ""}">${fmtScore(row.score)}</span></li>`).join("")}</ol>
  </div>`;
}

function finalIntroHtml(state, { bets = false, ctx = {} } = {}) {
  const final = state.final || {};
  const people = seatedPlayers(state).filter((player) => player.status === "playing");
  const betsIn = final.betsIn || [];
  const list = bets ? `<div class="qz-betsin"><span class="qz-betsin__label">BETS IN <b>${betsIn.length}</b>/${people.length}</span><span class="qz-dots">${people.map((player) => {
    const done = betsIn.includes(player.id);
    return `<span class="qz-betsin__who${done ? " is-in" : ""}" data-key="bi-${e(player.id)}" style="--pc:${playerColor(state, player)}">${playerTokenSvg(state, player, { size: 44 })}<b>${e(upper(player.name))}</b>${done ? `<i aria-label="bet in">${ICONS.check}</i>` : ""}</span>`;
  }).join("")}</span></div>` : "";
  return `<div class="qz-final-intro">
    <div class="qz-final-intro__eyebrow">THE FINAL · ONE CLUE FOR EVERYONE</div>
    ${categoryCardHtml({ index: 0, title: final.category || "" }, { fit: "final", style: ";--cat:#F2C14E;--cat-ink:#15191E" })}
    <p class="qz-final-intro__line">${bets ? "Bet in secret on your phone: anything from 0 to your score (at least 500). Right adds it, wrong takes it away." : "Everyone bets in secret, then answers one last clue."}</p>
    ${list}
  </div>`;
}

function finalRevealHtml(state, ctx) {
  const final = state.final || {};
  const order = final.order || [];
  const results = new Map((final.results || []).map((result) => [result.playerId, result]));
  const latest = (final.results || [])[Math.max(0, (final.revealed || 0) - 1)]?.playerId;
  const answer = Number.isInteger(final.answerIndex) ? final.choices?.[final.answerIndex] : "";
  const rows = order.map((id) => {
    const player = playerOf(state, id);
    if (!player) return "";
    const result = results.get(id);
    const color = playerColor(state, player);
    if (!result) return `<li class="qz-fr is-waiting" data-key="fr-${e(id)}" style="--pc:${color}">${playerTokenSvg(state, player, { size: 44 })}<b class="qz-fr__name">${e(upper(player.name))}</b><span class="qz-fr__wait">…</span><span class="qz-fr__score">${fmtScore(player.score)}</span></li>`;
    const choice = Number.isInteger(result.choice) && result.choice >= 0 ? `${LETTERS[result.choice]} · ${final.choices?.[result.choice] || ""}` : "NO ANSWER";
    return `<li class="qz-fr ${result.correct ? "is-right" : "is-wrong"}${id === latest ? " is-latest" : ""}" data-key="fr-${e(id)}" style="--pc:${color}">${playerTokenSvg(state, player, { size: 44 })}<b class="qz-fr__name">${e(upper(player.name))}</b>
      <span class="qz-fr__choice">${e(choice)}</span><span class="qz-fr__mark" aria-label="${result.correct ? "right" : "wrong"}">${result.correct ? "✓" : "✗"}</span>
      <span class="qz-fr__bet">${e(result.correct ? signedPoints(result.bet) : signedPoints(-result.bet))}</span>
      <span class="qz-fr__score"><s>${fmtScore(result.before)}</s> ${fmtScore(result.after)}</span></li>`;
  }).join("");
  // Seven or eight players: slimmer rows and a smaller question, so the leaders (shown last) fit.
  return `<div class="qz-freveal${order.length >= 7 ? " qz-freveal--full" : ""}">
    <div class="qz-freveal__head"><span class="qz-tag" style="--cat:#F2C14E;--cat-ink:#15191E"><i aria-hidden="true"></i>THE FINAL · ${e(final.category || "")}</span><p class="qz-freveal__text">${e(final.text || "")}</p>
      ${answer ? `<span class="qz-freveal__answer"><span class="qz-choice__letter">${LETTERS[final.answerIndex]}</span>${e(answer)}</span>` : ""}</div>
    <ol class="qz-frs">${rows}</ol>
  </div>`;
}

function finishedHtml(state, ctx) {
  const rows = rankings(state);
  const winners = (state.winnerIds || []).map((id) => playerOf(state, id)).filter(Boolean);
  const title = winners.length > 1 ? `${winners.map((player) => upper(player.name)).join(" AND ")} SHARE IT` : winners.length ? `${upper(winners[0].name)} WINS` : "GAME OVER";
  const host = state.viewerRole === "table" && !ctx.watching;
  return `<div class="qz-done">
    <div class="qz-done__eyebrow">FINAL SCORES</div>
    <div class="qz-done__title">${e(title)}</div>
    <ol class="qz-done__list">${rows.map((row) => `<li class="qz-done__row${row.place === 1 ? " is-win" : ""}" data-key="qzd-${e(row.player.id)}" style="--pc:${playerColor(state, row.player)}">
      <span class="qz-done__place">${e(placeWord(row.place))}</span>${playerTokenSvg(state, row.player, { size: 48 })}<b class="qz-done__name">${e(upper(row.player.name))}</b>
      <span class="qz-done__how">${row.correct ?? 0} RIGHT · ${row.wrong ?? 0} WRONG</span><span class="qz-done__score${row.score < 0 ? " is-neg" : ""}">${fmtScore(row.score)}</span></li>`).join("")}</ol>
    ${host ? `<div class="qz-done__btns"><button type="button" class="big-btn big-btn--primary" data-cmd="rematch" data-autofocus>PLAY AGAIN</button><button type="button" class="big-btn" data-cmd="new-table">NEW TABLE</button></div>` : ""}
  </div>`;
}

export function quizGameHtml(state, ctx = {}) {
  const now = Number.isFinite(ctx.now) ? ctx.now : Date.now();
  const c = { ...ctx, now };
  const phase = state.status === "finished" ? "finished" : state.phase;
  let body = "";
  let plates = true;
  switch (phase) {
    case "intro":
      body = introHtml(state, c);
      break;
    case "pick":
      body = boardHtml(state);
      break;
    case "gold":
      body = `${boardHtml(state, { picked: state.clue })}${goldHtml(state)}`;
      break;
    case "bet":
      body = `${boardHtml(state, { picked: state.clue })}${betHtml(state, c)}`;
      break;
    case "read":
    case "answer":
    case "reveal":
    case "finalRead":
    case "finalAnswer":
      body = clueHtml(state, c);
      break;
    case "roundEnd":
      body = roundEndHtml(state);
      plates = false;
      break;
    case "finalIntro":
      body = finalIntroHtml(state, { ctx: c });
      break;
    case "finalBet":
      body = finalIntroHtml(state, { bets: true, ctx: c });
      plates = false;
      break;
    case "finalReveal":
      body = finalRevealHtml(state, c);
      plates = false;
      break;
    case "finished":
      body = finishedHtml(state, c);
      plates = false;
      break;
    default:
      body = state.board ? boardHtml(state) : "";
  }
  return `${headHtml(state, c)}<div class="qz-body" data-phase="${e(phase)}">${body}</div>${plates ? platesHtml(state, c) : ""}`;
}

// ---------------------------------------------------------------------------------------------
// Overlays (table.js menu pages)

export function quizHelpOverlayHtml(state) {
  return `<div class="dialog dialog--help on-light qz-tvhelp" role="dialog" aria-modal="true" aria-labelledby="help-title">
    <div class="dialog__head" id="help-title">HOW TO PLAY THE QUIZ</div>
    <div class="dialog__body qz-tvhelp__body">${howToPlayHtml(state)}
      <div class="qz-tvhelp__side"><h3 class="set-head">ON THIS SCREEN</h3>
        <p>Everyone plays on their own phone: scan the code to join. This screen shows the board, each clue and the scores.</p>
        <h3 class="set-head">QUESTIONS</h3>${creditsHtml(state)}</div>
    </div>
    <div class="dialog__actions dialog__actions--foot"><button type="button" class="big-btn big-btn--primary" data-cmd="menu-back" data-autofocus>${ICONS.arrowLeft}BACK</button></div>
  </div>`;
}

// SETTINGS for a quiz table: as for Ludo (no board camera, no computer trade offers).
export function quizSettingsOverlayHtml(sound = {}, screen = {}, ctx = {}) {
  return ludoSettingsOverlayHtml(sound, screen, ctx);
}

export function quizEndConfirmHtml(state) {
  const leader = rankings(state)[0]?.player || null;
  const lead = leader ? `<div class="confirm-leader">${playerTokenSvg(state, leader, { size: 34 })}<span class="confirm-leader__name">${e(upper(leader.name))}</span><span class="confirm-leader__worth">${ICONS.crown}${fmtScore(leader.score)}</span></div>` : "";
  return `<div class="dialog on-light" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title">
    <div class="dialog__head dialog__head--danger" id="confirm-title">END THE QUIZ NOW?</div>
    <div class="dialog__body"><p class="dialog__lead">The game ends for everyone and places go by the scores as they stand:</p>${lead}
      <div class="dialog__actions"><button type="button" class="big-btn big-btn--danger" data-cmd="confirm">END GAME</button><button type="button" class="big-btn" data-cmd="close" data-autofocus>KEEP PLAYING</button></div>
    </div></div>`;
}

// ---------------------------------------------------------------------------------------------
// Controller

export function createQuizTable(deps) {
  const { el, ui, storage } = deps;
  let aiLevel = storage.get(AI_KEY) || null;
  let ticker = null;
  let cancelOpen = null;
  let openKey = "";
  let chimed = "";
  let tickKey = "";
  let tickCancels = [];
  let introKey = "";
  let introElapsed = 0;

  const now = () => deps.now();

  function ctx(state) {
    return { ...deps.ctx(), aiLevel, now: now(), introElapsed: introOffset(state) };
  }

  // The intro's categories land on the server's schedule: a screen that opens the intro late
  // starts part-way through (fixed once per intro, so re-renders never restart the cascade).
  function introOffset(state) {
    if (state?.phase !== "intro") return 0;
    const key = `${state.code}|${state.round}`;
    if (key !== introKey) {
      introKey = key;
      const total = 3000 + 600 * (state.board?.categories?.length || 6);
      introElapsed = state.phaseEndsAt > 0 ? Math.max(0, now() - (state.phaseEndsAt - total)) : 0;
      if (deps.reduced()) introElapsed = 1e6;
    }
    return introElapsed;
  }

  function draw(state) {
    const lobby = state.status === "lobby";
    const c = ctx(state);
    morph(el.quiz, lobby ? quizLobbyHtml(state, c) : quizGameHtml(state, c));
    el.quiz.dataset.mode = lobby ? "lobby" : "game";
    el.quiz.dataset.phase = state.status === "finished" ? "finished" : state.phase || "";
    el.quiz.dataset.demo = c.demo ? "1" : "";
    const clue = activeClue(state);
    if (clue && choicesOpen(state, c.now) && clue.key !== chimed) {
      chimed = clue.key;
      if (!["reveal", "finalReveal"].includes(state.phase)) deps.sound("quizOpen");
    }
  }

  // Timers that follow the synced clock: show the choices at openAt, tick the last 3 seconds, and
  // refresh the countdowns (only the numbers and bars change, morph leaves the rest alone).
  function syncTimers(state) {
    const clue = activeClue(state);
    const demo = Boolean(deps.ctx().demo);
    const waiting = clue && !demo && ["read", "finalRead"].includes(state.phase) && !choicesOpen(state, now()) ? clue.key : "";
    if (waiting !== openKey) {
      cancelOpen?.();
      cancelOpen = null;
      openKey = waiting;
      if (waiting) cancelOpen = scheduleAt(clue.openAt, () => { cancelOpen = null; if (isQuizState(ui.state)) draw(ui.state); }, { now });
    }
    const ticking = clue && !demo && ["read", "answer", "finalRead", "finalAnswer"].includes(state.phase) && clue.closesAt > 0 ? clue.key : "";
    if (ticking !== tickKey) {
      for (const cancel of tickCancels) cancel();
      tickCancels = [];
      tickKey = ticking;
      if (ticking) {
        for (const before of [3000, 2000, 1000]) {
          if (clue.closesAt - before > now()) tickCancels.push(scheduleAt(clue.closesAt - before, () => deps.sound("tick", { last: before === 1000, volume: 0.8 }), { now }));
        }
      }
    }
    const live = !demo && state.status === "playing" && (state.phaseEndsAt > 0 || clue);
    if (live && !ticker) ticker = setInterval(() => { if (isQuizState(ui.state) && !document.hidden) draw(ui.state); }, 250);
    if (!live && ticker) {
      clearInterval(ticker);
      ticker = null;
    }
  }

  function render(state) {
    draw(state);
    syncTimers(state);
  }

  // A new quiz state (table.js drain): sounds, then straight on screen. Nothing waits.
  function commit(prev, next) {
    if (isQuizState(prev) && !document.hidden) {
      for (const hit of quizSounds(prev, next)) deps.sound(hit.name, { ...hit.opts, volume: hit.gain });
    }
    deps.render();
  }

  function stop() {
    cancelOpen?.();
    for (const cancel of tickCancels) cancel();
    clearInterval(ticker);
    ticker = null;
    cancelOpen = null;
    tickCancels = [];
    openKey = "";
    tickKey = "";
  }

  async function command(cmd, arg) {
    const state = ui.state;
    switch (cmd) {
      case "qz-setting": {
        const chip = settingChips(state).find((candidate) => candidate.key === arg);
        if (!chip || state?.status !== "lobby") return undefined;
        deps.sound("tap");
        return deps.perform({ type: "settings", settings: chip.patch });
      }
      case "qz-ai-level": {
        const { levels, level } = levelOf(state, aiLevel);
        aiLevel = levels[(levels.indexOf(level) + 1) % levels.length];
        storage.set(AI_KEY, aiLevel);
        deps.sound("tap");
        deps.render();
        return undefined;
      }
      case "qz-add-ai": {
        const { level } = levelOf(state, aiLevel);
        return deps.addAi(level);
      }
      case "qz-help":
        return deps.openHelp?.();
      default:
        return undefined;
    }
  }

  return { render, commit, command, stop, busy: () => false };
}
