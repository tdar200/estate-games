// Quiz screens for the phone (/play): lobby, picking, reading, the four answer buttons, bets, the
// reveal, the final and the places. Pure: state + view in, HTML strings out (the same slots as
// screens.js renderPhone: header, banner, main, nav). phone.js picks these when state.game ===
// "quiz"; quiz-app.js wires them and owns the clock (view.qz.now, view.qz.open).
//
// docs/QUIZ-API.md §9: works from 360 px wide, nothing under 14 px. The answer buttons are four
// full-width rows, A to D top to bottom in fixed places on every phone, each with its letter and
// text (never colour alone), at least 64 px tall. They exist only once the choices are open: before
// openAt the page has no choice text in it at all.

import { aiLevelLabel, escapeHtml, upper } from "../shared/format.js";
import { gamePickHtml, gtypeTag } from "../shared/poker-view.js";
import { activeClue, betPresets, clampBet, readProgress, secondsLeft } from "../shared/quiz-clock.js";
import {
  LETTERS, canAnswerNow, categoryColor, cluesLeft, creditsHtml, fitTitleSize, fmtScore, howToPlayHtml, isQuizState, myChoice, outcomeFor, phaseLine, placeWord, playerOf, rankings, roundSubtitle,
  roundTitle, seatedPlayers, settingChips, settingsLine, signedPoints,
} from "../shared/quiz-view.js";
import { inkOn, playerColor, playerTokenSvg } from "../shared/tokens.js";
import { icon } from "./icons.js";
import { btn, buttonsHtml } from "./parts.js";

const e = escapeHtml;
// "A · B · C" lines wrap only between their parts, never inside one ("ONE / BOARD").
const dotParts = (text) => String(text).split(" · ").map((part, i, all) => `<span class="qzp-part">${e(part)}${i < all.length - 1 ? " ·" : ""}</span>`).join(" ");
const GOLD = ["#F2C14E", "#15191E"];

export const isQuiz = isQuizState;

export function mePlayer(state) {
  return playerOf(state, state?.viewerId);
}

export function aiLevelsOf(state) {
  return Array.isArray(state?.aiLevels) && state.aiLevels.length ? state.aiLevels : ["easy", "normal", "hard"];
}

// The bet being made by the viewer now: {key, kind: "gold" | "final", min, max} or null.
export function betContext(state) {
  const you = state?.you;
  if (!you?.canBet) return null;
  if (state.phase === "bet" && state.bet?.playerId === you.id) return { key: `${state.code}|g${state.clue?.seq ?? 0}`, kind: "gold", min: you.betMin, max: you.betMax };
  if (state.phase === "finalBet") return { key: `${state.code}|f${state.final?.seq ?? 0}`, kind: "final", min: you.betMin, max: you.betMax };
  return null;
}

// The amount on the stepper: the draft for this bet, else HALF.
export function betAmount(state, draft) {
  const bet = betContext(state);
  if (!bet) return 0;
  if (draft && draft.key === bet.key && Number.isFinite(draft.amount)) return clampBet(draft.amount, bet.min, bet.max);
  return betPresets(bet.min, bet.max).half;
}

// The viewer's tap on the active clue: the server's word, or the tap this phone just sent.
export function tappedChoice(state, view = {}) {
  const server = myChoice(state);
  if (server >= 0) return server;
  const clue = activeClue(state);
  const tap = view.qz?.tapped;
  return clue && tap && tap.key === clue.key && Number.isInteger(tap.choice) ? tap.choice : -1;
}

// Which quiz screen (data-screen on #app).
export function quizRoute(state, view = {}) {
  if (!state) return "qz-wait";
  if (view.inviteOpen) return "qz-invite";
  if (state.status === "lobby") return "qz-lobby";
  if (state.status === "finished") return "qz-result";
  const me = mePlayer(state);
  const open = Boolean(view.qz?.open);
  switch (state.phase) {
    case "intro":
      return "qz-intro";
    case "pick":
      return state.you?.canPick ? "qz-pick" : "qz-wait";
    case "gold":
      return "qz-gold";
    case "bet":
      return betContext(state) ? "qz-bet" : "qz-wait";
    case "read":
    case "answer":
    case "finalRead":
    case "finalAnswer": {
      if (!open) return "qz-read";
      if (tappedChoice(state, view) >= 0) return "qz-locked";
      return me && canAnswerNow(state) ? "qz-answer" : "qz-watch";
    }
    case "reveal":
      return "qz-reveal";
    case "roundEnd":
      return "qz-roundend";
    case "finalIntro":
      return "qz-final";
    case "finalBet":
      return betContext(state) ? "qz-bet" : "qz-final";
    case "finalReveal":
      return "qz-final-reveal";
    default:
      return "qz-wait";
  }
}

// ---------------------------------------------------------------------------------------------
// Bars

function logoBar(state, view) {
  const watch = view.session?.role === "spectator";
  return `<div class="bar bar--logo qzp-logo"><span class="bar__logo">ESTATE</span>${gtypeTag("quiz")}${state?.code ? `<span class="bar__table">${watch ? "Watching" : "Table"} <strong>${e(state.code)}</strong></span>` : ""}<button type="button" class="bar__gear" data-cmd="settings" aria-label="Settings">${icon("gear", { size: 20 })}</button></div>`;
}

function topBar(state, view) {
  const me = mePlayer(state);
  const color = me ? playerColor(state, me) : "#F2C14E";
  const who = me
    ? `<span class="qzp-me" style="--pc:${color};--pc-ink:${inkOn(color)}">${playerTokenSvg(state, me, { size: 26 })}<b class="qzp-me__score${me.score < 0 ? " is-neg" : ""}">${fmtScore(me.score)}</b></span>`
    : '<span class="qzp-watching">WATCHING</span>';
  const left = state.board ? cluesLeft(state.board) : 0;
  const round = `<span class="qzp-round"><span>${e(roundTitle(state))}</span>${state.board && Number(state.round) < 3 ? `<span>${left} LEFT</span>` : ""}</span>`;
  return `<div class="pkbar qzp-top">${who}${round}<button type="button" class="pkbar__more qzp-more" data-cmd="qz-more" aria-label="More: settings, how to play, leave">${icon("gear", { size: 16 })}<span>MORE</span></button></div>`;
}

// The line over a game screen: what is happening, with a timer when there is one.
function headLine(state, view, text = phaseLine(state, { viewerId: state.viewerId })) {
  const now = view.qz?.now ?? Date.now();
  const timed = ["pick", "bet", "finalBet"].includes(state.phase) && state.phaseEndsAt > 0;
  const seconds = timed ? secondsLeft(state.phaseEndsAt, now) : 0;
  return `<div class="qzp-head"><span class="qzp-head__line">${e(text)}</span>${timed ? `<span class="qzp-timer${seconds <= 3 ? " is-low" : ""}" aria-label="${seconds} seconds left">${seconds} S</span>` : ""}</div>`;
}

// The final's category on its gold card: sized to fit the narrowest phone (360 px wide: the card's
// text box is about 300 px), at most two lines.
const FINAL_TITLE_BOX = Object.freeze({ width: 300, height: 70, max: 30, min: 18, spacing: 0.06, lineHeight: 1.15 });

function categoryTag(clue) {
  if (!clue) return "";
  const color = clue.final ? { bg: "#F2C14E", ink: "#15191E" } : categoryColor(clue.categoryIndex);
  return `<span class="qzp-tag" style="--cat:${color.bg};--cat-ink:${color.ink}"><i aria-hidden="true"></i><span>${e(clue.final ? `FINAL · ${clue.category}` : clue.category)}</span></span>`;
}

function clueMeta(state, clue) {
  const bet = !clue.final && clue.gold ? state.bet : null;
  const value = clue.final ? "" : `<span class="qzp-value">${bet?.amount ? `BET ${fmtScore(bet.amount)}` : fmtScore(clue.value)}</span>`;
  return `<div class="qzp-meta">${categoryTag(clue)}${value}</div>`;
}

// ---------------------------------------------------------------------------------------------
// Lobby

function lobbyScreen(state, view) {
  const me = mePlayer(state);
  const host = Boolean(me) && (state.hostId === me.id || state.viewerIsHost);
  const seats = seatedPlayers(state);
  const list = seats.map((player) => {
    const ai = player.kind === "ai" || player.input === "ai";
    const level = ai ? `<span class="qzp-seat__level">${e(upper(aiLevelLabel(player.difficulty || "normal")))}</span>` : player.online === false ? '<span class="qzp-seat__level">AWAY</span>' : "";
    return `<li class="qzp-seat${player.id === me?.id ? " is-me" : ""}" data-key="qs-${e(player.id)}" style="--pc:${playerColor(state, player)}">
      ${playerTokenSvg(state, player, { size: 30 })}<span class="qzp-seat__who"><span class="qzp-seat__name">${e(player.id === me?.id ? "YOU" : upper(player.name))}</span>${level}</span>
      ${host && player.id !== me?.id ? `<button type="button" class="qzp-seat__remove" data-cmd="remove" data-arg="${e(player.id)}" aria-label="Remove ${e(player.name)}">×</button>` : ""}
    </li>`;
  }).join("");
  const levels = aiLevelsOf(state);
  const level = view.qz?.aiLevel && levels.includes(view.qz.aiLevel) ? view.qz.aiLevel : levels.includes("normal") ? "normal" : levels[0];
  const chips = settingChips(state).map((chip) => (host
    ? `<button type="button" class="qzp-chip${chip.custom ? " is-custom" : ""}" data-cmd="qz-setting" data-arg="${e(chip.key)}" aria-label="${e(chip.name)}: ${e(chip.label)}. Tap to change"><span>${e(chip.name)}</span><b>${e(chip.label)}</b></button>`
    : `<span class="qzp-chip"><span>${e(chip.name)}</span><b>${e(chip.label)}</b></span>`)).join("");
  const levelChip = host && levels.length > 1 ? `<button type="button" class="qzp-chip" data-cmd="qz-ai-level" aria-label="New computer players: ${e(aiLevelLabel(level))}. Tap to change"><span>COMPUTER</span><b>${e(upper(aiLevelLabel(level)))}</b></button>` : "";
  const max = Math.min(8, state.seatLimit || 8);
  const ready = seats.length;
  const hostPlayer = playerOf(state, state.hostId);
  const buttons = host
    ? [
      btn(`START · ${ready} ${ready === 1 ? "PLAYER" : "PLAYERS"}`, "", "primary", { act: { type: "start" }, disabled: ready < 1 }),
      btn("+ COMPUTER", ready >= max ? "table is full" : "", "ghost", { cmd: "qz-add-ai", disabled: ready >= max }),
    ]
    : [btn(hostPlayer ? `WAITING FOR ${upper(hostPlayer.name)}` : "WAITING FOR THE HOST", "", "ghost", { disabled: true })];
  const codeCard = `<section class="codecard qzp-code" aria-label="Table code">
      <div class="codecard__code"><span>CODE</span><strong>${e(state.code)}</strong></div>
      <div class="codecard__btns"><button type="button" class="small-btn is-gold" data-cmd="share">INVITE</button><button type="button" class="small-btn" data-cmd="copy">${view.copied ? "COPIED" : "COPY LINK"}</button><button type="button" class="small-btn is-wa" data-cmd="whatsapp">WHATSAPP</button></div>
    </section>`;
  const picker = host ? gamePickHtml("quiz", { cmd: "qz-switch-game", subtitles: false }) : `<div class="pk-gamechosen qzp-chosen">${gtypeTag("quiz")}<span>${e(settingsLine(state))}</span></div>`;
  const note = `<p class="qzp-note">${icon("phone", { size: 16 })}<span>Everyone plays on their own phone: share the code.</span></p>`;
  const chipsRow = [{ html: `${icon("deal", { size: 14 })} FRIENDS`, cmd: "open-invite" }, { html: "HOW TO PLAY", cmd: "qz-help" }, { html: "LEAVE", cmd: "leave-ask" }];
  const main = `${codeCard}${picker}<ul class="qzp-seats">${list}</ul>${note}<div class="qzp-chips">${chips}${levelChip}</div>${buttonsHtml(buttons, chipsRow)}`;
  return { banner: [GOLD, view.session?.role === "spectator" ? "WATCHING" : "IN THE LOBBY"], main, header: logoBar(state, view) };
}

function inviteScreen(state, view) {
  const code = state?.code || "";
  const link = view.inviteLink || "";
  const qr = view.qrUrl
    ? `<img class="qr" src="${e(view.qrUrl)}" width="150" height="150" alt="QR code for the invite link to table ${e(code)}">`
    : '<div class="qr qr--placeholder" role="img" aria-label="QR code unavailable"><span>QR</span></div>';
  const main = `<section class="paper"><div class="paper__head">TABLE ${e(code)}</div><div class="paper__body paper__body--center">${qr}
    <div class="link-box"><span>${e(link.replace(/^https?:\/\//, ""))}</span></div>${view.linkIsLan ? '<div class="note note--ink">Same Wi-Fi only</div>' : ""}</div></section>`;
  const buttons = [btn("SHARE LINK", "", "primary", { cmd: "share" }), btn("WHATSAPP", "", "ghost", { cmd: "whatsapp" }), btn(view.copied ? "COPIED" : "COPY LINK", "", "ghost", { cmd: "copy" }), btn("BACK", "", "ghost", { cmd: "close-invite" })];
  return { banner: null, main: `${main}${buttonsHtml(buttons)}`, header: logoBar(state, view) };
}

// ---------------------------------------------------------------------------------------------
// The board (picking, and watching someone pick)

function boardRows(state, { pick = false } = {}) {
  return (state.board?.categories || []).map((category) => {
    const color = categoryColor(category.index);
    const cells = (category.cells || []).map((cell) => {
      const label = cell.played ? `${category.title} ${cell.value}: played` : `${category.title} for ${cell.value}`;
      const mark = cell.played ? (cell.gold ? '<span class="qzp-cell__gold" aria-hidden="true">★</span>' : "") : `<span>${cell.value}</span>`;
      if (pick && !cell.played) return `<button type="button" class="qzp-cell" data-qz="pick" data-cat="${category.index}" data-row="${cell.row}" aria-label="${e(label)}">${mark}</button>`;
      return `<span class="qzp-cell${cell.played ? " is-played" : ""}${cell.gold ? " is-gold" : ""}" role="img" aria-label="${e(label)}">${mark}</span>`;
    }).join("");
    return `<div class="qzp-row" style="--cat:${color.bg};--cat-ink:${color.ink}"><span class="qzp-row__title"><i aria-hidden="true"></i>${e(category.title)}</span><div class="qzp-row__cells">${cells}</div></div>`;
  }).join("");
}

function pickScreen(state, view) {
  const main = `<div class="qzp qzp--pick">${headLine(state, view, "YOUR PICK")}<div class="qzp-board is-live">${boardRows(state, { pick: true })}</div>
    <p class="qzp-hint">Bigger numbers, harder clues. Gold squares hide somewhere.</p></div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function waitScreen(state, view) {
  const line = phaseLine(state, { viewerId: state.viewerId });
  let extra = "";
  if (state.phase === "bet" && state.bet) {
    const finder = playerOf(state, state.bet.playerId);
    extra = `<div class="qzp-card qzp-card--gold"><span class="qzp-card__eyebrow">GOLD SQUARE · ${e(state.clue?.category || "")}</span><b>${e(upper(finder?.name || ""))} BETS ${fmtScore(state.bet.min)} TO ${fmtScore(state.bet.max)}</b><span>Only they answer this one.</span></div>`;
  }
  const board = state.board ? `<div class="qzp-board">${boardRows(state)}</div>` : "";
  const main = `<div class="qzp qzp--wait">${headLine(state, view, line)}${extra}${board}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function introScreen(state, view) {
  const cats = (state.board?.categories || []).map((category) => {
    const color = categoryColor(category.index);
    return `<li class="qzp-intro__cat" style="--cat:${color.bg};--cat-ink:${color.ink}"><i aria-hidden="true"></i>${e(category.title)}</li>`;
  }).join("");
  const main = `<div class="qzp qzp--intro"><div class="qzp-big">${e(roundTitle(state))}</div><div class="qzp-sub">${dotParts(roundSubtitle(state))}</div><ol class="qzp-intro">${cats}</ol></div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function goldScreen(state, view) {
  const finderId = state.bet?.playerId || state.chooserId;
  const finder = playerOf(state, finderId);
  const mine = finderId && finderId === state.viewerId;
  const main = `<div class="qzp qzp--gold"><div class="qzp-goldcard"><span class="qzp-goldcard__star" aria-hidden="true">★</span><b>GOLD SQUARE</b>
    <span>${mine ? "You found it! You bet first, then answer it alone." : `${e(upper(finder?.name || ""))} found it, under ${e(state.clue?.category || "")}.`}</span></div></div>`;
  return { banner: null, main, header: topBar(state, view) };
}

// ---------------------------------------------------------------------------------------------
// Bets: the stepper with MIN, HALF and MAX

function betScreen(state, view) {
  const bet = betContext(state);
  const amount = betAmount(state, view.qz?.betDraft);
  const presets = betPresets(bet.min, bet.max);
  const now = view.qz?.now ?? Date.now();
  const seconds = state.phaseEndsAt > 0 ? secondsLeft(state.phaseEndsAt, now) : 0;
  const gold = bet.kind === "gold";
  const title = gold ? `GOLD SQUARE · ${state.clue?.category || ""}` : `THE FINAL · ${state.final?.category || ""}`;
  const lead = gold ? "Bet before you see the clue. Right wins it, wrong loses it." : "Your bet is secret. Right adds it, wrong takes it away.";
  const preset = (label, value) => `<button type="button" class="qzp-preset${amount === value ? " is-on" : ""}" data-qz="bet-set" data-amount="${value}" aria-pressed="${amount === value}"><span>${label}</span><b>${fmtScore(value)}</b></button>`;
  const main = `<div class="qzp qzp--bet">${headLine(state, view, gold ? "YOUR BET" : "YOUR FINAL BET")}
    <div class="qzp-betbox${gold ? " is-gold" : ""}">
      <span class="qzp-betbox__title">${e(title)}</span>
      <p class="qzp-betbox__lead">${e(lead)}</p>
      <div class="qzp-stepper" role="group" aria-label="Bet amount">
        <button type="button" class="qzp-step" data-qz="bet-step" data-dir="-1" aria-label="Bet less" ${amount <= bet.min ? "disabled" : ""}>−</button>
        <output class="qzp-amount" aria-live="polite">${fmtScore(amount)}</output>
        <button type="button" class="qzp-step" data-qz="bet-step" data-dir="1" aria-label="Bet more" ${amount >= bet.max ? "disabled" : ""}>+</button>
      </div>
      <div class="qzp-presets">${preset("MIN", presets.min)}${preset("HALF", presets.half)}${preset("MAX", presets.max)}</div>
      <button type="button" class="btn btn--primary qzp-betgo" data-qz="bet-go" data-amount="${amount}" ${view.busy ? "disabled" : ""}><span class="btn__label">BET ${fmtScore(amount)}</span></button>
      <span class="qzp-betbox__range">${fmtScore(bet.min)} to ${fmtScore(bet.max)}${seconds ? ` · ${seconds} s left` : ""}</span>
    </div></div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function finalScreen(state, view) {
  const final = state.final || {};
  const people = seatedPlayers(state).filter((player) => player.status === "playing");
  const betsIn = final.betsIn || [];
  const mine = state.you && betsIn.includes(state.you.id);
  const status = state.phase === "finalBet"
    ? `<p class="qzp-final__status">${mine ? `Your bet is in${Number.isFinite(state.you?.bet) ? ` (${fmtScore(state.you.bet)}, only you can see it)` : ""}. ` : ""}${betsIn.length} of ${people.length} bets in.</p>`
    : '<p class="qzp-final__status">Everyone bets in secret, then answers one last clue.</p>';
  const main = `<div class="qzp qzp--final">${headLine(state, view)}<div class="qzp-finalcard"><span class="qzp-finalcard__eyebrow">THE FINAL</span><b style="font-size:${fitTitleSize(final.category, FINAL_TITLE_BOX)}px">${e(final.category || "")}</b></div>${status}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

// ---------------------------------------------------------------------------------------------
// The clue: reading, answering, locked in, watching

function clueBlock(state, clue, { compact = false } = {}) {
  return `${clueMeta(state, clue)}<p class="qzp-clue${compact ? " is-compact" : ""}${String(clue.text || "").length > 100 ? " is-long" : ""}">${e(clue.text)}</p>`;
}

function readScreen(state, view) {
  const clue = activeClue(state);
  const now = view.qz?.now ?? Date.now();
  const progress = readProgress(clue, now);
  const left = Math.max(0, Math.ceil(((clue.openAt || 0) - now) / 1000));
  const eligible = clue.final ? Boolean(state.you) : (clue.eligible || []).includes(state.viewerId);
  const gold = !clue.final && clue.gold ? playerOf(state, state.bet?.playerId) : null;
  const note = !state.you ? "Watching: the answers appear when the bar runs out."
    : !eligible && gold ? `Gold square: only ${upper(gold.name)} answers this one.`
      : `Get ready: four answers appear here in ${left} s.`;
  const main = `<div class="qzp qzp--read">${clueBlock(state, clue)}
    <div class="qzp-bar" role="progressbar" aria-label="Reading time" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${Math.round(progress * 100)}"><i style="transform:scaleX(${progress.toFixed(3)})"></i></div>
    <p class="qzp-hint">${e(note)}</p>
    <div class="qzp-slots" aria-hidden="true">${LETTERS.map((letter) => `<span class="qzp-slot"><span class="qzp-choice__letter">${letter}</span></span>`).join("")}</div></div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function timeBar(clue, now) {
  const span = Math.max(1, (clue.closesAt || 0) - (clue.openAt || 0));
  const fraction = Math.max(0, Math.min(1, ((clue.closesAt || 0) - now) / span));
  const seconds = secondsLeft(clue.closesAt, now);
  return `<div class="qzp-time${seconds <= 3 ? " is-low" : ""}"><span class="qzp-bar qzp-bar--time" aria-hidden="true"><i style="transform:scaleX(${fraction.toFixed(3)})"></i></span><b aria-label="${seconds} seconds left">${seconds}</b></div>`;
}

function answerScreen(state, view) {
  const clue = activeClue(state);
  const now = view.qz?.now ?? Date.now();
  const fresh = view.qz?.fresh === clue.key;
  const buttons = (clue.choices || []).map((choice, index) => `<button type="button" class="qzp-choice" data-qz="answer" data-choice="${index}" aria-label="${LETTERS[index]}: ${e(choice)}"><span class="qzp-choice__letter">${LETTERS[index]}</span><span class="qzp-choice__text">${e(choice)}</span></button>`).join("");
  const main = `<div class="qzp qzp--answer">${clueBlock(state, clue, { compact: true })}${timeBar(clue, now)}
    <div class="qzp-choices${fresh ? " is-fresh" : ""}" data-key="choices-${e(clue.key)}" role="group" aria-label="Answers">${buttons}</div></div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function lockedScreen(state, view) {
  const clue = activeClue(state);
  const now = view.qz?.now ?? Date.now();
  const choice = tappedChoice(state, view);
  const ids = clue.final ? state.final?.answersIn || [] : clue.answered || [];
  const others = ids.filter((id) => id !== state.viewerId).map((id) => playerOf(state, id)).filter(Boolean);
  const wager = clue.final && Number.isFinite(state.you?.bet) ? `<span class="qzp-locked__bet">Your bet: ${fmtScore(state.you.bet)}</span>` : "";
  const main = `<div class="qzp qzp--locked">${clueBlock(state, clue, { compact: true })}${timeBar(clue, now)}
    <div class="qzp-locked" data-key="locked-${e(clue.key)}"><span class="qzp-locked__eyebrow">LOCKED IN</span>
      <span class="qzp-locked__pick"><span class="qzp-choice__letter">${LETTERS[choice] || "?"}</span><b>${e(clue.choices?.[choice] || "")}</b></span>${wager}
      <span class="qzp-locked__wait">Wait for the reveal.</span></div>
    ${others.length ? `<div class="qzp-others"><span>ALSO IN</span>${others.map((player) => playerTokenSvg(state, player, { size: 28, label: player.name })).join("")}</div>` : ""}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function watchScreen(state, view) {
  const clue = activeClue(state);
  const now = view.qz?.now ?? Date.now();
  const gold = !clue.final && clue.gold ? playerOf(state, state.bet?.playerId) : null;
  const why = !state.you ? "WATCHING" : gold ? `ONLY ${upper(gold.name)} ANSWERS` : "YOU'RE OUT OF THIS ONE";
  const list = (clue.choices || []).map((choice, index) => `<li class="qzp-choice is-static"><span class="qzp-choice__letter">${LETTERS[index]}</span><span class="qzp-choice__text">${e(choice)}</span></li>`).join("");
  const main = `<div class="qzp qzp--watch">${clueBlock(state, clue, { compact: true })}${timeBar(clue, now)}<p class="qzp-why">${e(why)}</p><ol class="qzp-choices is-static">${list}</ol></div>`;
  return { banner: null, main, header: topBar(state, view) };
}

// ---------------------------------------------------------------------------------------------
// The reveal

function revealScreen(state, view) {
  const clue = state.clue || {};
  const me = mePlayer(state);
  const right = Number.isInteger(clue.answerIndex) ? clue.answerIndex : -1;
  const winner = playerOf(state, clue.winnerId);
  let card = "";
  if (me) {
    const { result, outcome, delta, noAnswer } = outcomeFor(state, me.id);
    const words = {
      won: ["RIGHT", signedPoints(delta), "is-right"],
      right: ["RIGHT", "±0", "is-late"],
      lost: [noAnswer ? "NO ANSWER" : "WRONG", signedPoints(delta), "is-wrong"],
      late: [result?.correct ? "RIGHT, BUT LATE" : "WRONG, BUT LATE", "NO CHANGE", "is-late"],
      missed: ["NO ANSWER", "NO CHANGE", "is-late"],
      none: ["", "", ""],
    }[outcome];
    if (words[0]) card = `<div class="qzp-result ${words[2]}" data-key="res-${e(clue.seq)}"><b>${e(words[0])}</b><span>${e(words[1])}</span></div>`;
    if (clue.gold && outcome === "none") {
      // Watching someone else's gold square: what the finder got.
      const finder = outcomeFor(state, state.bet?.playerId);
      card = `<div class="qzp-result is-late"><b>${e(winner ? `${upper(winner.name)} GOT IT` : "GOLD SQUARE MISSED")}</b><span>${e(finder.delta ? signedPoints(finder.delta) : "NO CHANGE")}</span></div>`;
    }
  }
  const answer = right >= 0 ? `<div class="qzp-answer"><span class="qzp-answer__label">THE ANSWER</span><span class="qzp-answer__pick"><span class="qzp-choice__letter">${LETTERS[right]}</span><b>${e(clue.choices?.[right] || "")}</b></span></div>` : "";
  const who = winner && winner.id !== me?.id ? `<p class="qzp-winner" style="--pc:${playerColor(state, winner)}">${playerTokenSvg(state, winner, { size: 26 })}<b>${e(upper(winner.name))}</b> wins ${fmtScore(Math.abs(clue.results?.find((row) => row.playerId === winner.id)?.delta || clue.value))} and picks next</p>` : !winner ? `<p class="qzp-winner">${e(phaseLine(state))}${state.chooserId === state.viewerId ? ": you keep control" : ""}</p>` : '<p class="qzp-winner">You pick next.</p>';
  const main = `<div class="qzp qzp--reveal">${clueMeta(state, { ...clue, final: false })}${card}${answer}${who}<p class="qzp-clue is-compact is-dim">${e(clue.text || "")}</p></div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function ranksList(state, { viewerId = null } = {}) {
  return `<ol class="qzp-ranks">${rankings(state).map((row) => `<li class="qzp-rank${row.player.id === viewerId ? " is-me" : ""}${row.place === 1 ? " is-win" : ""}" data-key="qr-${e(row.player.id)}" style="--pc:${playerColor(state, row.player)}">
    <span class="qzp-rank__place">${e(placeWord(row.place))}</span>${playerTokenSvg(state, row.player, { size: 28 })}<b class="qzp-rank__name">${e(row.player.id === viewerId ? "YOU" : upper(row.player.name))}</b><span class="qzp-rank__score${row.score < 0 ? " is-neg" : ""}">${fmtScore(row.score)}</span></li>`).join("")}</ol>`;
}

function roundEndScreen(state, view) {
  const next = Number(state.round) < Number(state.rounds || 1) ? "Round 2 next: double values." : "The final is next.";
  const main = `<div class="qzp qzp--roundend"><div class="qzp-big">${e(roundTitle(state))} COMPLETE</div><div class="qzp-sub">${e(next)}</div>${ranksList(state, { viewerId: state.viewerId })}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function finalRevealScreen(state, view) {
  const final = state.final || {};
  const mine = (final.results || []).find((row) => row.playerId === state.viewerId);
  const right = Number.isInteger(final.answerIndex) ? final.answerIndex : -1;
  const card = mine
    ? `<div class="qzp-result ${mine.correct ? "is-right" : "is-wrong"}"><b>${mine.correct ? "RIGHT" : "WRONG"}</b><span>${e(signedPoints(mine.correct ? mine.bet : -mine.bet))} · ${fmtScore(mine.before)} → ${fmtScore(mine.after)}</span></div>`
    : state.you ? '<div class="qzp-result is-late"><b>COMING UP</b><span>Lowest score first</span></div>' : "";
  const rows = (final.results || []).map((row) => {
    const player = playerOf(state, row.playerId);
    if (!player) return "";
    return `<li class="qzp-fr ${row.correct ? "is-right" : "is-wrong"}" data-key="qf-${e(player.id)}" style="--pc:${playerColor(state, player)}">${playerTokenSvg(state, player, { size: 26 })}<b>${e(player.id === state.viewerId ? "YOU" : upper(player.name))}</b><span class="qzp-fr__mark" aria-label="${row.correct ? "right" : "wrong"}">${row.correct ? "✓" : "✗"}</span><span class="qzp-fr__score">${fmtScore(row.after)}</span></li>`;
  }).join("");
  const answer = right >= 0 ? `<div class="qzp-answer"><span class="qzp-answer__label">THE ANSWER</span><span class="qzp-answer__pick"><span class="qzp-choice__letter">${LETTERS[right]}</span><b>${e(final.choices?.[right] || "")}</b></span></div>` : "";
  const main = `<div class="qzp qzp--freveal">${headLine(state, view)}${card}${answer}<ol class="qzp-frs">${rows}</ol></div>`;
  return { banner: null, main, header: topBar(state, view) };
}

// ---------------------------------------------------------------------------------------------
// Final places

function resultScreen(state, view) {
  const me = mePlayer(state);
  const won = Boolean(me && (state.winnerIds || []).includes(me.id));
  const winners = (state.winnerIds || []).map((id) => playerOf(state, id)).filter(Boolean);
  const title = won ? (winners.length > 1 ? "A SHARED WIN" : "YOU WIN THE QUIZ") : winners.length ? `${upper(winners.map((player) => player.name).join(" and "))} ${winners.length > 1 ? "SHARE IT" : "WINS"}` : "GAME OVER";
  const place = rankings(state).find((row) => row.player.id === me?.id)?.place;
  const medal = me ? `<div class="medal-wrap" data-medal><div class="medal${won ? "" : " medal--silver"}"><span class="medal__eyebrow">${won ? "WINNER" : "FINISHED"}</span><span class="medal__place">${e(place ? placeWord(place) : "")}</span></div></div>` : "";
  const main = `${medal}<div class="big-title qzp-title">${e(title)}</div><div class="qzp-endlist">${ranksList(state, { viewerId: state.viewerId })}</div>`;
  const host = Boolean(me) && state.hostId === me.id;
  const buttons = host ? [btn("PLAY AGAIN", "", "primary", { cmd: "rematch" })] : [btn("WAITING FOR A REMATCH", "the host starts it", "ghost", { disabled: true })];
  buttons.push(btn("LEAVE TABLE", "", "ghost", { cmd: "leave" }));
  return { banner: null, main: `${main}${buttonsHtml(buttons, [{ label: "HOW TO PLAY", cmd: "qz-help" }])}`, header: logoBar(state, view) };
}

// ---------------------------------------------------------------------------------------------
// Entry

export function renderQuiz(state, view = {}) {
  const screen = quizRoute(state, view);
  const builders = {
    "qz-lobby": lobbyScreen, "qz-invite": inviteScreen, "qz-result": resultScreen, "qz-intro": introScreen, "qz-pick": pickScreen, "qz-wait": waitScreen, "qz-gold": goldScreen,
    "qz-bet": betScreen, "qz-read": readScreen, "qz-answer": answerScreen, "qz-locked": lockedScreen, "qz-watch": watchScreen, "qz-reveal": revealScreen, "qz-roundend": roundEndScreen,
    "qz-final": finalScreen, "qz-final-reveal": finalRevealScreen,
  };
  const built = state ? (builders[screen] || waitScreen)(state, view) : { banner: null, main: "", header: "" };
  const banner = built.banner ? `<div class="banner" data-key="${e(built.banner[1])}" style="background:${built.banner[0][0]};color:${built.banner[0][1]}">${e(built.banner[1])}</div>` : "";
  return { screen, header: built.header, banner, main: built.main, nav: "" };
}

// ---------------------------------------------------------------------------------------------
// Sheets (phone.js renders them in its <dialog>)

function sheetBtn(label, attrs, kind = "ghost", { html = "" } = {}) {
  return `<button type="button" class="btn btn--${kind}" ${attrs}><span class="btn__label">${html || e(label)}</span></button>`;
}

export function moreSheetHtml(state, view = {}) {
  const me = mePlayer(state);
  const host = Boolean(me) && state.hostId === me.id;
  const items = [sheetBtn("INVITE PEOPLE", 'data-sheet-cmd="qz-invite"', "primary")];
  items.push(sheetBtn("SETTINGS", 'data-sheet-cmd="qz-settings"', "ghost", { html: `${icon("gear", { size: 16 })} SETTINGS` }));
  items.push(sheetBtn("HOW TO PLAY", 'data-sheet-cmd="qz-help"', "ghost"));
  if (me) items.push(sheetBtn("LEAVE TABLE", 'data-sheet-cmd="qz-leave"', "ghost"));
  if (host && state.status === "playing") items.push(sheetBtn("END GAME FOR EVERYONE", 'data-sheet-cmd="qz-end"', "danger"));
  items.push(sheetBtn("CLOSE", 'data-sheet-cmd="close"', "plain"));
  return `<div class="sheet__body qzp-sheet"><h2 class="sheet__title">MORE</h2>
    <div class="tableline"><span>TABLE</span><strong>${e(state.code || view.session?.code || "")}</strong>${gtypeTag("quiz")}</div>
    <p class="sheet__text">${dotParts(settingsLine(state))}</p>
    <div class="actions">${items.join("")}</div></div>`;
}

export function helpSheetHtml(state, { back = true } = {}) {
  return `<div class="sheet__body qzp-sheet qzp-help"><h2 class="sheet__title">HOW TO PLAY</h2>${howToPlayHtml(state)}
    <h3 class="qzp-help__head">QUESTIONS</h3>${creditsHtml(state)}
    <div class="actions">${back ? sheetBtn("BACK", 'data-sheet-cmd="qz-more"', "ghost") : ""}${sheetBtn("CLOSE", 'data-sheet-cmd="close"', "plain")}</div></div>`;
}
