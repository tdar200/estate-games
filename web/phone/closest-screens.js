// Near Thing screens for the phone (/play): lobby, the number pad, the line-up, the bet, the
// chips, the reveal and the places. Pure: state + view in, HTML strings out (the same slots as
// screens.js renderPhone: header, banner, main, nav). phone.js picks these when state.game ===
// "closest"; closest-app.js wires them and keeps the drafts (view.cg).
//
// docs/CLOSEST-API.md §8: from 360 × 740, nothing under 14 px, touch targets ≥ 44 px. The number
// pad and the bet screen are looks the owner is still choosing: they come from
// web/shared/closest-looks.js (padLook, betLook); the rest here is plain.

import { aiLevelLabel, escapeHtml, upper } from "../shared/format.js";
import { gamePickHtml, gtypeTag } from "../shared/poker-view.js";
import { secondsLeft } from "../shared/quiz-clock.js";
import {
  betDraft, betKey, betModel, breakdown, closestWord, creditsHtml, fillNote, fmtScore, formatNumber, gameTitle, howToPlayHtml, isClosestState, isLastQuestion, numberWords, padModel, phaseLine,
  placeWord, playerOf, playingPlayers, rangeText, rankings, resultOf, revealModel, seatedPlayers, settingChips, settingsLine, signedPoints, slotsOf, withUnit,
} from "../shared/closest-view.js";
import { betLook, padLook, revealLineHtml } from "../shared/closest-looks.js";
import { inkOn, playerColor, playerTokenSvg } from "../shared/tokens.js";
import { icon } from "./icons.js";
import { btn, buttonsHtml } from "./parts.js";

const e = escapeHtml;
const dotParts = (text) => String(text).split(" · ").map((part, i, all) => `<span class="cgp-part">${e(part)}${i < all.length - 1 ? " ·" : ""}</span>`).join(" ");
const GOLD = ["#F2C14E", "#15191E"];

export const isClosest = isClosestState;

export function mePlayer(state) {
  return playerOf(state, state?.viewerId);
}

export function aiLevelsOf(state) {
  return Array.isArray(state?.aiLevels) && state.aiLevels.length ? state.aiLevels : ["easy", "normal", "hard"];
}

// The viewer's guess this question: the server's word, else the one this phone is sending.
export function myGuess(state, view = {}) {
  const you = state?.you;
  if (!you) return null;
  if (you.guess !== null && you.guess !== undefined) return you.guess;
  const sent = view.cg?.sent;
  return sent && sent.seq === state.question?.seq ? sent.value : null;
}

// The pad's digits for this question (the draft is dropped when the question changes).
export function padDigits(state, view = {}) {
  const pad = view.cg?.pad;
  return pad && pad.seq === state?.question?.seq ? pad.digits : "";
}

// The bet being drafted on this phone, normalised for this question.
export function currentBet(state, view = {}) {
  return betDraft(state, view.cg?.bet);
}

// Bet in: the server says so, or this phone has just sent it.
export function betIsIn(state, view = {}) {
  if (state?.you?.chips) return true;
  return Boolean(view.cg?.betSent) && view.cg.betSent === betKey(state);
}

// Which screen (data-screen on #app).
export function closestRoute(state, view = {}) {
  if (!state) return "cg-wait";
  if (view.inviteOpen) return "cg-invite";
  if (state.status === "lobby") return "cg-lobby";
  if (state.status === "finished") return "cg-result";
  const you = state.you;
  switch (state.phase) {
    case "intro":
      return "cg-intro";
    case "guess": {
      if (!you) return "cg-watch";
      const guess = myGuess(state, view);
      const changing = view.cg?.changing === state.question?.seq;
      if (you.canGuess && (guess === null || changing)) return "cg-guess";
      return guess !== null ? "cg-guessed" : "cg-watch";
    }
    case "lineup":
      return "cg-lineup";
    case "bet":
      if (!you) return "cg-watch";
      if (betIsIn(state, view)) return "cg-betin";
      return you.canBet ? "cg-bet" : "cg-watch";
    case "chips":
      return "cg-chips";
    case "reveal":
      return "cg-reveal";
    default:
      return "cg-wait";
  }
}

// ---------------------------------------------------------------------------------------------
// Bars

function logoBar(state, view) {
  const watch = view.session?.role === "spectator";
  return `<div class="bar bar--logo cgp-logo"><span class="bar__logo">ESTATE</span>${gtypeTag("closest")}${state?.code ? `<span class="bar__table">${watch ? "Watching" : "Table"} <strong>${e(state.code)}</strong></span>` : ""}<button type="button" class="bar__gear" data-cmd="settings" aria-label="Settings">${icon("gear", { size: 20 })}</button></div>`;
}

function roundText(state) {
  const no = Number(state.question?.no) || 0;
  const count = Number(state.questionCount) || 0;
  if (!no) return gameTitle(state);
  if (isLastQuestion(state)) return "LAST · ×2";
  return count ? `Q ${no} / ${count}` : `Q ${no}`;
}

function topBar(state, view) {
  const me = mePlayer(state);
  const color = me ? playerColor(state, me) : "#F2C14E";
  const who = me
    ? `<span class="cgp-me" style="--pc:${color};--pc-ink:${inkOn(color)}">${playerTokenSvg(state, me, { size: 26 })}<b class="cgp-me__score">${fmtScore(me.score)}</b></span>`
    : '<span class="cgp-watching">WATCHING</span>';
  return `<div class="pkbar cgp-top">${who}<span class="cgp-round">${e(roundText(state))}</span><button type="button" class="pkbar__more cgp-more" data-cmd="cg-more" aria-label="More: settings, how to play, leave">${icon("gear", { size: 16 })}<span>MORE</span></button></div>`;
}

// The line over a game screen: what is happening, with the seconds left while guessing and betting.
function headLine(state, view, text = phaseLine(state, { viewerId: state.viewerId })) {
  const now = view.cg?.now ?? Date.now();
  const timed = ["guess", "bet"].includes(state.phase) && state.phaseEndsAt > 0;
  const seconds = timed ? secondsLeft(state.phaseEndsAt, now) : 0;
  return `<div class="cgp-head"><span class="cgp-head__line">${revealLineHtml(state, text, view.cg?.elapsed ?? Infinity)}</span>${timed ? `<span class="cgp-timer${seconds <= 5 ? " is-low" : ""}" aria-label="${seconds} seconds left">${seconds} S</span>` : ""}</div>`;
}

function questionBlock(state, { compact = false } = {}) {
  const question = state.question || {};
  const long = String(question.text || "").length > 100;
  return `<p class="cgp-q${compact ? " is-compact" : ""}${long ? " is-long" : ""}">${e(question.text || "")}</p>`;
}

// Who is done (tokens, never what) out of everyone playing.
function doneRow(state, ids, word) {
  const people = playingPlayers(state).length;
  const others = (ids || []).filter((id) => id !== state.viewerId).map((id) => playerOf(state, id)).filter(Boolean);
  return `<div class="cgp-done"><span>${e(word)} <b>${(ids || []).length}</b>/${people}</span><span class="cgp-done__dots">${others.map((player) => playerTokenSvg(state, player, { size: 26, label: player.name })).join("")}</span></div>`;
}

// The slots as a plain list (line-up, chips, watching): odds, number, owners, your chips.
function lineList(state, { chips = false } = {}) {
  const question = state.question || {};
  const you = state.you;
  const slots = slotsOf(state);
  if (!slots.length) return "";
  const rows = slots.map((slot) => {
    const mine = Boolean(you) && (slot.ownerIds || []).includes(you.id);
    const owners = (slot.ownerIds || []).map((id) => playerOf(state, id)).filter(Boolean).map((player) => playerTokenSvg(state, player, { size: 22, label: player.name })).join("");
    const mineChips = chips && you?.chips ? you.chips.filter((at) => at === slot.index).length : 0;
    const stake = chips && you?.stake > 0 && you.stakeSlot === slot.index ? ` · STAKE ${you.stake}` : "";
    const all = chips ? (slot.chips || []).reduce((sum, chip) => sum + (Number(chip.count) || 0), 0) : 0;
    return `<li class="cgp-slot${mine ? " is-mine" : ""}${slot.result === "win" ? " is-win" : slot.result === "second" ? " is-second" : ""}" data-key="cgl-${slot.index}">
      <span class="cgp-slot__odds">${slot.odds}×</span><b class="cgp-slot__num">${e(formatNumber(slot.value, question.format))}</b>
      <span class="cgp-slot__who">${mine ? '<span class="cgp-tag">YOURS</span>' : ""}${owners}</span>${chips ? `<span class="cgp-slot__chips">${mineChips ? `<b>YOU ×${mineChips}${e(stake)}</b>` : ""}${all ? `<span>${all} ${all === 1 ? "chip" : "chips"}</span>` : ""}</span>` : ""}
    </li>`;
  }).join("");
  return `<ol class="cgp-line"${question.unit ? ` aria-label="Guesses in ${e(question.unit)}"` : ""}>${rows}</ol>`;
}

// ---------------------------------------------------------------------------------------------
// Lobby

function lobbyScreen(state, view) {
  const me = mePlayer(state);
  const host = Boolean(me) && (state.hostId === me.id || state.viewerIsHost);
  const seats = seatedPlayers(state);
  const list = seats.map((player) => {
    const ai = player.kind === "ai" || player.input === "ai";
    const level = ai ? `<span class="cgp-seat__level">${e(upper(aiLevelLabel(player.difficulty || "normal")))}</span>` : player.online === false ? '<span class="cgp-seat__level">AWAY</span>' : "";
    return `<li class="cgp-seat${player.id === me?.id ? " is-me" : ""}" data-key="cs-${e(player.id)}" style="--pc:${playerColor(state, player)}">
      ${playerTokenSvg(state, player, { size: 30 })}<span class="cgp-seat__who"><span class="cgp-seat__name">${e(player.id === me?.id ? "YOU" : upper(player.name))}</span>${level}</span>
      ${host && player.id !== me?.id ? `<button type="button" class="cgp-seat__remove" data-cmd="remove" data-arg="${e(player.id)}" aria-label="Remove ${e(player.name)}">×</button>` : ""}
    </li>`;
  }).join("");
  const levels = aiLevelsOf(state);
  const level = view.cg?.aiLevel && levels.includes(view.cg.aiLevel) ? view.cg.aiLevel : levels.includes("normal") ? "normal" : levels[0];
  const chips = settingChips(state).map((chip) => (host
    ? `<button type="button" class="cgp-chip${chip.custom ? " is-custom" : ""}" data-cmd="cg-setting" data-arg="${e(chip.key)}" aria-label="${e(chip.name)}: ${e(chip.label)}. Tap to change"><span>${e(chip.name)}</span><b>${e(chip.label)}</b></button>`
    : `<span class="cgp-chip"><span>${e(chip.name)}</span><b>${e(chip.label)}</b></span>`)).join("");
  const levelChip = host && levels.length > 1 ? `<button type="button" class="cgp-chip" data-cmd="cg-ai-level" aria-label="New computer players: ${e(aiLevelLabel(level))}. Tap to change"><span>COMPUTER</span><b>${e(upper(aiLevelLabel(level)))}</b></button>` : "";
  const max = Math.min(8, state.seatLimit || 8);
  const ready = seats.length;
  const hostPlayer = playerOf(state, state.hostId);
  const buttons = host
    ? [
      btn(`START · ${ready} ${ready === 1 ? "PLAYER" : "PLAYERS"}`, "", "primary", { act: { type: "start" }, disabled: ready < 1 }),
      btn("+ COMPUTER", ready >= max ? "table is full" : "", "ghost", { cmd: "cg-add-ai", disabled: ready >= max }),
    ]
    : [btn(hostPlayer ? `WAITING FOR ${upper(hostPlayer.name)}` : "WAITING FOR THE HOST", "", "ghost", { disabled: true })];
  const codeCard = `<section class="codecard cgp-code" aria-label="Table code">
      <div class="codecard__code"><span>CODE</span><strong>${e(state.code)}</strong></div>
      <div class="codecard__btns"><button type="button" class="small-btn is-gold" data-cmd="share">INVITE</button><button type="button" class="small-btn" data-cmd="copy">${view.copied ? "COPIED" : "COPY LINK"}</button><button type="button" class="small-btn is-wa" data-cmd="whatsapp">WHATSAPP</button></div>
    </section>`;
  const picker = host ? gamePickHtml("closest", { cmd: "cg-switch-game", subtitles: false }) : `<div class="pk-gamechosen cgp-chosen">${gtypeTag("closest")}<span>${e(settingsLine(state))}</span></div>`;
  const fill = fillNote(state);
  const note = `<p class="cgp-note">${icon("phone", { size: 16 })}<span>Everyone plays on their own phone: share the code.${fill ? ` ${e(fill)}.` : ""}</span></p>`;
  const chipsRow = [{ html: `${icon("deal", { size: 14 })} FRIENDS`, cmd: "open-invite" }, { html: "HOW TO PLAY", cmd: "cg-help" }, { html: "LEAVE", cmd: "leave-ask" }];
  const main = `${codeCard}${picker}<ul class="cgp-seats">${list}</ul>${note}<div class="cgp-chips">${chips}${levelChip}</div>${buttonsHtml(buttons, chipsRow)}`;
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
// The game

function introScreen(state, view) {
  const count = Number(state.questionCount) || 0;
  const main = `<div class="cgp cgp--intro"><div class="cgp-big">${e(gameTitle(state))}</div>${count ? `<div class="cgp-sub">${count} QUESTIONS</div>` : ""}
    <p class="cgp-text">Guess the number on your phone. Then put two chips on the guesses you think are closest.</p>
    <p class="cgp-text cgp-text--gold">The last question pays double.</p></div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function guessScreen(state, view) {
  const question = state.question || {};
  const changing = view.cg?.changing === question.seq;
  const model = padModel(state, { digits: padDigits(state, view), error: view.cg?.padError || "", sending: Boolean(view.cg?.sending) });
  const keep = changing && model.previous
    ? `<div class="cgp-keep"><span>Now: <b>${e(model.previous)}</b>. You can change it once.</span><button type="button" class="small-btn" data-cg="keep">KEEP IT</button></div>`
    : "";
  const main = `<div class="cgp cgp--guess">${headLine(state, view)}${questionBlock(state, { compact: true })}${keep}${padLook(model)}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function guessedScreen(state, view) {
  const question = state.question || {};
  const you = state.you || {};
  const guess = myGuess(state, view);
  const words = question.format === "year" ? "" : numberWords(guess, question.format);
  const shown = formatNumber(guess, question.format);
  const sending = view.cg?.sent?.seq === question.seq && (you.guess === null || you.guess === undefined);
  const change = you.canGuess && Number(you.changesLeft) > 0 && !sending
    ? `<button type="button" class="btn btn--ghost cgp-change" data-cg="change"><span class="btn__label">CHANGE IT</span><span class="btn__detail">once only</span></button>`
    : `<p class="cgp-hint">${sending ? "Sending…" : "Locked in. Wait for the others."}</p>`;
  const main = `<div class="cgp cgp--guessed">${headLine(state, view, "YOUR GUESS IS IN")}${questionBlock(state, { compact: true })}
    <div class="cgp-card cgp-card--guess" data-key="g-${e(question.seq)}"><span class="cgp-card__eyebrow">YOUR GUESS</span><b class="cgp-card__num">${e(shown)}</b>${question.unit ? `<span class="cgp-card__unit">${e(question.unit)}</span>` : ""}${words && words !== shown ? `<span class="cgp-card__words">${e(words)}</span>` : ""}</div>
    ${change}${doneRow(state, question.guessed, "GUESSED")}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function watchScreen(state, view) {
  const question = state.question || {};
  const why = !state.you ? "You're watching this game." : state.phase === "guess" ? "You join in from the next question." : "No chips for you on this one.";
  const done = state.phase === "guess" ? doneRow(state, question.guessed, "GUESSED") : state.phase === "bet" ? doneRow(state, question.betsIn, "BETS IN") : "";
  const main = `<div class="cgp cgp--watch">${headLine(state, view)}${questionBlock(state, { compact: true })}<p class="cgp-hint">${e(why)}</p>${state.phase === "bet" ? lineList(state) : ""}${done}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function lineupScreen(state, view) {
  const question = state.question || {};
  const one = slotsOf(state).length === 1;
  const unit = question.unit ? ` (${question.unit})` : "";
  const main = `<div class="cgp cgp--lineup">${headLine(state, view)}${questionBlock(state, { compact: true })}
    <p class="cgp-hint">${one ? "Only one guess, so no chips this time: it wins." : `Low to high${e(unit)}. Next: put your two chips on the closest.`}</p>${lineList(state)}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function betScreen(state, view) {
  const model = betModel(state, currentBet(state, view));
  // The look's own header says how to move a chip; its stake panel, what can be lost.
  const main = `<div class="cgp cgp--bet">${headLine(state, view)}${questionBlock(state, { compact: true })}${betLook(model, state)}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function betInScreen(state, view) {
  const question = state.question || {};
  const you = state.you || {};
  const bet = you.chips ? { chips: you.chips, stake: Number(you.stake) || 0, stakeSlot: you.stakeSlot } : currentBet(state, view);
  const slots = slotsOf(state);
  const textAt = (index) => formatNumber(slots.find((slot) => slot.index === index)?.value, question.format);
  const places = bet.chips[0] === bet.chips[1] ? [`Both chips on ${textAt(bet.chips[0])}`] : bet.chips.map((at, i) => `Chip ${i + 1} on ${textAt(at)}`);
  const stake = bet.stake > 0 ? `Stake ${bet.stake} on ${textAt(bet.stakeSlot)}` : "No stake";
  const main = `<div class="cgp cgp--betin">${headLine(state, view, "YOUR CHIPS ARE IN")}${questionBlock(state, { compact: true })}
    <div class="cgp-card" data-key="b-${e(question.seq)}"><span class="cgp-card__eyebrow">YOUR CHIPS</span>${places.map((line) => `<b class="cgp-card__line">${e(line)}</b>`).join("")}<span class="cgp-card__line">${e(stake)}</span></div>
    ${doneRow(state, question.betsIn, "BETS IN")}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function chipsScreen(state, view) {
  const main = `<div class="cgp cgp--chips">${headLine(state, view)}${questionBlock(state, { compact: true })}<p class="cgp-hint">Every chip is down. The answer is next.</p>${lineList(state, { chips: true })}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function revealScreen(state, view) {
  const question = state.question || {};
  const me = mePlayer(state);
  const answer = withUnit(question.answer, question);
  const words = question.format === "year" ? "" : numberWords(question.answer, question.format);
  const answerCard = `<div class="cgp-answer" data-key="a-${e(question.seq)}"><span class="cgp-answer__label">THE ANSWER</span><b class="cgp-answer__num">${e(answer)}</b>${words && words !== formatNumber(question.answer, question.format) ? `<span class="cgp-answer__words">${e(words)}</span>` : ""}</div>`;
  let mine = "";
  if (me && state.you) {
    const row = resultOf(state, me.id);
    const how = breakdown(state, me.id);
    const delta = Number(row?.delta) || 0;
    mine = `<div class="cgp-result${delta > 0 ? " is-up" : delta < 0 ? " is-down" : ""}"><b>${e(delta ? signedPoints(delta) : "NO CHANGE")}</b><span>${e(how.text)}</span>${row ? `<small>${fmtScore(row.before)} → ${fmtScore(row.after)}</small>` : ""}</div>`;
  }
  const rows = (question.results || []).map((row) => {
    const player = playerOf(state, row.playerId);
    if (!player) return "";
    return `<li class="cgp-res" data-key="cr-${e(player.id)}" style="--pc:${playerColor(state, player)}">${playerTokenSvg(state, player, { size: 24 })}<b>${e(player.id === state.viewerId ? "YOU" : upper(player.name))}</b><span class="cgp-res__guess">${row.guess === null || row.guess === undefined ? "no guess" : e(formatNumber(row.guess, question.format))}</span><span class="cgp-res__delta">${e(Number(row.delta) ? signedPoints(row.delta) : "±0")}</span></li>`;
  }).join("");
  // The answer at once; your result at the table's wins beat, everyone's points at its payouts beat.
  const { beats } = revealModel(state, { elapsed: view.cg?.elapsed ?? Infinity });
  const timing = `--wins:${Math.round(beats.wins)}ms;--payouts:${Math.round(beats.payouts)}ms`;
  const main = `<div class="cgp cgp--reveal" style="${timing}">${headLine(state, view)}${answerCard}${mine}${question.fact ? `<p class="cgp-fact">${e(question.fact)}</p>` : ""}${rows ? `<ol class="cgp-ress">${rows}</ol>` : ""}</div>`;
  return { banner: null, main, header: topBar(state, view) };
}

function ranksList(state, { viewerId = null } = {}) {
  return `<ol class="cgp-ranks">${rankings(state).map((row) => `<li class="cgp-rank${row.player.id === viewerId ? " is-me" : ""}${row.place === 1 ? " is-win" : ""}" data-key="cr-${e(row.player.id)}" style="--pc:${playerColor(state, row.player)}">
    <span class="cgp-rank__place">${e(placeWord(row.place))}</span>${playerTokenSvg(state, row.player, { size: 28 })}<span class="cgp-rank__who"><b class="cgp-rank__name">${e(row.player.id === viewerId ? "YOU" : upper(row.player.name))}</b><small>${e(closestWord(row.closest))}</small></span><span class="cgp-rank__score">${fmtScore(row.score)}</span></li>`).join("")}</ol>`;
}

function resultScreen(state, view) {
  const me = mePlayer(state);
  const won = Boolean(me && (state.winnerIds || []).includes(me.id));
  const winners = (state.winnerIds || []).map((id) => playerOf(state, id)).filter(Boolean);
  const title = won ? (winners.length > 1 ? "A SHARED WIN" : "YOU WIN") : winners.length ? `${upper(winners.map((player) => player.name).join(" and "))} ${winners.length > 1 ? "SHARE IT" : "WINS"}` : "GAME OVER";
  const place = rankings(state).find((row) => row.player.id === me?.id)?.place;
  const medal = me ? `<div class="medal-wrap" data-medal><div class="medal${won ? "" : " medal--silver"}"><span class="medal__eyebrow">${won ? "WINNER" : "FINISHED"}</span><span class="medal__place">${e(place ? placeWord(place) : "")}</span></div></div>` : "";
  const main = `${medal}<div class="big-title cgp-title">${e(title)}</div><div class="cgp-endlist">${ranksList(state, { viewerId: state.viewerId })}</div>`;
  const host = Boolean(me) && state.hostId === me.id;
  const buttons = host ? [btn("PLAY AGAIN", "", "primary", { cmd: "rematch" })] : [btn("WAITING FOR A REMATCH", "the host starts it", "ghost", { disabled: true })];
  buttons.push(btn("LEAVE TABLE", "", "ghost", { cmd: "leave" }));
  return { banner: null, main: `${main}${buttonsHtml(buttons, [{ label: "HOW TO PLAY", cmd: "cg-help" }])}`, header: logoBar(state, view) };
}

function waitScreen(state, view) {
  const main = `<div class="cgp cgp--wait">${headLine(state, view)}<p class="cgp-hint">${e(rangeText(state.question || {}) ? "Waiting for the next step." : "Waiting.")}</p></div>`;
  return { banner: null, main, header: state?.status === "playing" ? topBar(state, view) : logoBar(state, view) };
}

// ---------------------------------------------------------------------------------------------
// Entry

const BUILDERS = Object.freeze({
  "cg-lobby": lobbyScreen, "cg-invite": inviteScreen, "cg-intro": introScreen, "cg-guess": guessScreen, "cg-guessed": guessedScreen, "cg-watch": watchScreen,
  "cg-lineup": lineupScreen, "cg-bet": betScreen, "cg-betin": betInScreen, "cg-chips": chipsScreen, "cg-reveal": revealScreen, "cg-result": resultScreen, "cg-wait": waitScreen,
});

export const CLOSEST_SCREENS = Object.freeze(Object.keys(BUILDERS));

export function renderClosest(state, view = {}) {
  const screen = closestRoute(state, view);
  const built = state ? (BUILDERS[screen] || waitScreen)(state, view) : { banner: null, main: "", header: "" };
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
  const items = [sheetBtn("INVITE PEOPLE", 'data-sheet-cmd="cg-invite"', "primary")];
  items.push(sheetBtn("SETTINGS", 'data-sheet-cmd="cg-settings"', "ghost", { html: `${icon("gear", { size: 16 })} SETTINGS` }));
  items.push(sheetBtn("HOW TO PLAY", 'data-sheet-cmd="cg-help"', "ghost"));
  if (me) items.push(sheetBtn("LEAVE TABLE", 'data-sheet-cmd="cg-leave"', "ghost"));
  if (host && state.status === "playing") items.push(sheetBtn("END GAME FOR EVERYONE", 'data-sheet-cmd="cg-end"', "danger"));
  items.push(sheetBtn("CLOSE", 'data-sheet-cmd="close"', "plain"));
  return `<div class="sheet__body cgp-sheet"><h2 class="sheet__title">MORE</h2>
    <div class="tableline"><span>TABLE</span><strong>${e(state.code || view.session?.code || "")}</strong>${gtypeTag("closest")}</div>
    <p class="sheet__text">${dotParts(settingsLine(state))}</p>
    <div class="actions">${items.join("")}</div></div>`;
}

export function helpSheetHtml(state, { back = true } = {}) {
  return `<div class="sheet__body cgp-sheet cgp-help"><h2 class="sheet__title">HOW TO PLAY</h2>${howToPlayHtml(state)}
    <h3 class="cgp-help__head">QUESTIONS</h3>${creditsHtml(state)}
    <div class="actions">${back ? sheetBtn("BACK", 'data-sheet-cmd="cg-more"', "ghost") : ""}${sheetBtn("CLOSE", 'data-sheet-cmd="close"', "plain")}</div></div>`;
}
