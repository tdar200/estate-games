// Near Thing on the table screen (TV / PC at /): the question big enough to read across a room,
// the guesses lined up low to high as slots, the chips, the answer sliding along the line, and a
// plate per player. docs/CLOSEST-API.md is the contract; §8 sets the floors. A 1600 × 900 stage
// scaled to fit (table.js fit()): the question is ≥ 44 stage px, slot numbers ≥ 36, nothing < 20.
//
// Phones only (guesses and bets are secret): no keyboard or controller seats, so this screen is a
// viewer that also lets the host start the game and change the settings. States apply at once;
// the line-up and the reveal play inside their phase time (CSS delays worked out once per phase
// from phaseEndsAt, so a screen that comes in late joins part-way through).
//
// The line of slots and the reveal are looks the owner is still choosing: they come from
// web/shared/closest-looks.js (lineLook, revealLook). Everything else here is plain.

import { aiLevelLabel, escapeHtml, upper } from "../shared/format.js";
import { morph } from "../shared/morph.js";
import { gamePickHtml, gtypeTag } from "../shared/poker-view.js";
import { scheduleAt, secondsLeft } from "../shared/quiz-clock.js";
import {
  closestSounds, closestTimedSounds, closestWord, creditsHtml, fillNote, fmtScore, gameTitle, howToPlayHtml, isClosestState, isLastQuestion, lineModel, phaseElapsed, phaseLine, placeWord,
  plateDelta, plateTag, playerOf, playingPlayers, questionLabel, rangeText, rankings, revealModel, seatedPlayers, settingChips,
} from "../shared/closest-view.js";
import { lineLook, revealLineHtml, revealLook } from "../shared/closest-looks.js";
import { playerColor, playerTokenSvg } from "../shared/tokens.js";
import { ludoSettingsOverlayHtml } from "./ludo-table.js";
import * as views from "./views.js";

const e = escapeHtml;
const { ICONS } = views;
const AI_KEY = "estate.closestAiLevel";

export { isClosestState };

function qrHtml(state, ctx) {
  const code = state?.code || "·····";
  if (ctx.qrUrl) return `<img class="qr qr--img" src="${e(ctx.qrUrl)}" width="300" height="300" alt="QR code that opens the join page for table ${e(code)}">`;
  return views.qrPlaceholderHtml({ label: `QR code for table ${code}`, note: ctx.demo ? "DEMO" : "" });
}

function toolsHtml(ctx, { conn = true } = {}) {
  return `<span class="pk-tv__tools">${conn ? `<span class="side__conn" data-status="${e(ctx.status || "live")}"><span class="dot" aria-hidden="true"></span><span class="side__conn-text">${ctx.status === "offline" ? "OFFLINE" : ctx.demo ? "DEMO" : ""}</span></span>` : ""}<button class="icon-btn" type="button" data-cmd="cg-help" aria-label="How to play" title="How to play">${ICONS.help}</button>${views.soundButtonHtml(ctx.sound)}<button class="icon-btn menu-btn" type="button" data-cmd="menu" aria-haspopup="dialog" aria-label="Menu: settings, how to play, leave" title="Menu: settings, how to play, leave">${ICONS.menu}</button></span>`;
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

export function closestLobbyHtml(state, ctx = {}) {
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
      seats.push(`<li class="qz-seat" data-key="cgs-${e(player.id)}" style="--pc:${playerColor(state, player)}">${playerTokenSvg(state, player, { size: 56, label: `${player.name}'s token` })}<b>${e(upper(player.name))}</b><span class="qz-seat__dev">${e(deviceWord(player))}</span>${host ? views.removeSeatButtonHtml(player) : ""}</li>`);
    } else if (index === players.length) {
      seats.push(`<li class="qz-seat is-open is-hint"><span class="qz-seat__phone" aria-hidden="true">${ICONS.phone}</span><small>SCAN TO JOIN</small></li>`);
    } else {
      seats.push('<li class="qz-seat is-open"><span class="qz-seat__plus" aria-hidden="true">+</span><span class="sr-only">Open seat</span></li>');
    }
  }
  const chips = settingChips(state).map((chip) => {
    const inner = `<span>${e(chip.name)}</span><b>${e(chip.label)}</b>${chip.detail ? `<small>${e(chip.detail)}</small>` : ""}`;
    return host
      ? `<button type="button" class="qz-chip${chip.custom ? " is-custom" : ""}" data-cmd="cg-setting" data-arg="${e(chip.key)}" aria-label="${e(chip.name)}: ${e(chip.label)}. Change">${inner}</button>`
      : `<span class="qz-chip">${inner}</span>`;
  }).join("");
  const { levels, level } = levelOf(state, ctx.aiLevel);
  const ready = players.length;
  const fill = fillNote(state);
  const foot = host ? `<div class="pk-tv__foot qz-tv__foot">
      <span class="qz-count">${ready} / ${max}</span>
      <span class="ai-pick"><button class="chip-btn" type="button" data-cmd="cg-add-ai" ${ready >= max ? "disabled" : ""} aria-label="Add a ${e(aiLevelLabel(level))} computer player">${ICONS.cpu}<span>ADD COMPUTER</span></button>${levels.length > 1 ? `<button class="chip-btn chip-btn--level" type="button" data-cmd="cg-ai-level" aria-label="Computer level: ${e(aiLevelLabel(level))}. Change">${e(upper(aiLevelLabel(level)))} <span aria-hidden="true">⇄</span></button>` : ""}</span>
      <button class="ready-btn" type="button" data-cmd="start" ${ready >= 1 ? "" : "disabled"} aria-label="${ready >= 1 ? `Start ${e(gameTitle(state))} with ${ready} ${ready === 1 ? "player" : "players"}` : "Waiting for a player"}">START ${ICONS.arrow}</button>
    </div>` : `<div class="pk-tv__foot qz-tv__foot"><span class="qz-count">${ready} / ${max}</span><span class="qz-note">Waiting for the host to start</span></div>`;
  return `<header class="pk-tv__head qz-head"><span class="join__brand">ESTATE</span>${gtypeTag("closest")}${watching ? "" : views.resumeFormHtml({ code: ctx.resumeCode || "", error: ctx.resumeError || "" })}${watching ? "" : views.newTableButtonHtml()}${toolsHtml(ctx, { conn: false })}</header>
    <div class="pk-tv__lobby qz-tv__lobby cg-tv__lobby">
      <section class="qr-card on-light" aria-label="Scan to join">
        <div class="qr-card__head">${watching ? "WATCHING" : "SCAN TO JOIN"}</div>
        <div class="qr-card__body">${qrHtml(state, ctx)}
          <div class="qr-card__code"><span class="qr-card__code-value" aria-label="Table code ${e(code.split("").join(" "))}">${e(code)}</span></div>
          <div class="qr-card__foot">${reach}<span class="qr-card__link">${e(link)}</span></div>${watching ? "" : views.inviteButtonsHtml()}
        </div>
      </section>
      <div class="pk-tv__right qz-tv__right">
        ${watching ? "" : gamePickHtml("closest", { cmd: "pick-game", subtitles: false })}
        <ul class="qz-seats">${seats.join("")}</ul>
        <p class="qz-phones">${ICONS.phone}<span><b>Everyone plays on their own phone.</b> Guesses and bets are secret, so there are no keyboard or controller seats.${fill ? ` <b class="cg-fill">${e(fill)}.</b>` : ""}</span></p>
        <div class="qz-chips cg-chips">${chips}</div>
        ${foot}
      </div>
    </div>`;
}

// ---------------------------------------------------------------------------------------------
// Pieces of the game screen

function headHtml(state, ctx) {
  const label = questionLabel(state, { short: true });
  const round = `<span class="qz-head__round cg-head__round"><b>${e(gameTitle(state))}</b>${label ? `<small>${e(label)}</small>` : ""}</span>`;
  return `<header class="pk-tv__head qz-head"><span class="table-code">${views.tableCodeHtml(state.code)}</span>${round}
    <span class="qz-line" data-key="line-${e(state.phase)}">${revealLineHtml(state, phaseLine(state, { short: true }), ctx.elapsed ?? Infinity)}</span>${toolsHtml(ctx)}</header>`;
}

function plateHtml(state, player) {
  const color = playerColor(state, player);
  const { tag, tone } = plateTag(state, player);
  const delta = plateDelta(state, player.id);
  const deltaTone = delta.startsWith("+") ? "win" : "lose";
  const classes = ["qz-plate", tone === "locked" ? "is-locked" : "", tone === "left" ? "is-left" : "", tone === "away" ? "is-dim" : ""].filter(Boolean).join(" ");
  const check = tone === "locked" ? `<span class="qz-plate__check" aria-hidden="true">${ICONS.check}</span>` : "";
  return `<li class="${classes}" data-key="cgp-${e(player.id)}" style="--pc:${color}">
    <span class="qz-plate__top">${playerTokenSvg(state, player, { size: 40 })}<b class="qz-plate__name">${e(upper(player.name))}</b>${check}</span>
    <span class="qz-plate__bottom"><span class="qz-plate__score">${fmtScore(player.score)}</span>${delta ? `<span class="qz-plate__delta is-${deltaTone}">${e(delta)}</span>` : ""}</span>${tag ? `<span class="qz-plate__tag">${e(tag)}</span>` : ""}
  </li>`;
}

function platesHtml(state) {
  const players = state.players || [];
  return `<ol class="qz-plates cg-plates" style="--n:${Math.max(4, players.length)}" aria-label="Scores">${players.map((player) => plateHtml(state, player)).join("")}</ol>`;
}

// The question: big while guessing, compact over the line after that.
function questionHtml(state, { compact = false } = {}) {
  const question = state.question || {};
  const long = String(question.text || "").length > 90;
  const range = rangeText(question);
  const meta = `<div class="cg-q__meta"><span class="cg-q__tag${isLastQuestion(state) ? " is-double" : ""}">${e(questionLabel(state))}</span>${!compact && range ? `<span class="cg-q__range">Answer ${e(range)}</span>` : ""}</div>`;
  return `<section class="cg-q${compact ? " is-compact" : ""}" data-key="q-${e(question.seq ?? 0)}" aria-label="Question">${meta}<p class="cg-q__text${long ? " is-long" : ""}">${e(question.text || "")}</p></section>`;
}

// Who is done (dots, never what) and the seconds left, under the question or the line.
function doneHtml(state, ctx, { ids, word }) {
  const total = playingPlayers(state).length;
  const dots = ids.map((id) => playerOf(state, id)).filter(Boolean).map((player) => `<span class="qz-dot" data-key="dot-${e(player.id)}" style="--pc:${playerColor(state, player)}">${playerTokenSvg(state, player, { size: 40, label: `${player.name}: ${word.toLowerCase()}` })}</span>`).join("");
  const ends = Number(state.phaseEndsAt) || 0;
  const seconds = ends ? secondsLeft(ends, ctx.now) : 0;
  const span = Math.max(1, (Number(state.settings?.[state.phase === "bet" ? "betSeconds" : "guessSeconds"]) || 30) * 1000);
  const fraction = ends ? Math.max(0, Math.min(1, (ends - ctx.now) / span)) : 0;
  return `<div class="cg-done">
      <span class="qz-answered"><span class="qz-answered__label cg-done__label">${e(word)} <b>${ids.length}</b>/${total}</span><span class="qz-dots">${dots}</span></span>
      ${ends ? `<span class="qz-count-down cg-count${seconds <= 5 ? " is-low" : ""}" aria-label="${seconds} seconds left"><span class="qz-bar qz-bar--time" aria-hidden="true"><i style="transform:scaleX(${fraction.toFixed(3)})"></i></span><b>${seconds}</b></span>` : ""}
    </div>`;
}

function introHtml(state) {
  const count = Number(state.questionCount) || 0;
  return `<div class="qz-intro cg-intro"><div class="qz-intro__title">${e(gameTitle(state))}</div><div class="qz-intro__sub">${count ? `${count} QUESTIONS · ` : ""}GUESS, THEN BACK THE CLOSEST</div><p class="cg-intro__double">The last question pays double.</p></div>`;
}

function finishedHtml(state, ctx) {
  const rows = rankings(state);
  const winners = (state.winnerIds || []).map((id) => playerOf(state, id)).filter(Boolean);
  const title = winners.length > 1 ? `${winners.map((player) => upper(player.name)).join(" AND ")} SHARE IT` : winners.length ? `${upper(winners[0].name)} WINS` : "GAME OVER";
  const host = state.viewerRole === "table" && !ctx.watching;
  return `<div class="qz-done cg-done-card">
    <div class="qz-done__eyebrow">FINAL SCORES</div>
    <div class="qz-done__title">${e(title)}</div>
    <ol class="qz-done__list">${rows.map((row) => `<li class="qz-done__row${row.place === 1 ? " is-win" : ""}" data-key="cgd-${e(row.player.id)}" style="--pc:${playerColor(state, row.player)}">
      <span class="qz-done__place">${e(placeWord(row.place))}</span>${playerTokenSvg(state, row.player, { size: 48 })}<b class="qz-done__name">${e(upper(row.player.name))}</b>
      <span class="qz-done__how">${e(closestWord(row.closest))}</span><span class="qz-done__score">${fmtScore(row.score)}</span></li>`).join("")}</ol>
    ${host ? `<div class="qz-done__btns"><button type="button" class="big-btn big-btn--primary" data-cmd="rematch" data-autofocus>PLAY AGAIN</button><button type="button" class="big-btn" data-cmd="new-table">NEW TABLE</button></div>` : ""}
  </div>`;
}

export function closestGameHtml(state, ctx = {}) {
  const now = Number.isFinite(ctx.now) ? ctx.now : Date.now();
  const elapsed = ctx.elapsed ?? Infinity;
  const c = { ...ctx, now };
  const phase = state.status === "finished" ? "finished" : state.phase;
  const question = state.question || {};
  let body = "";
  let plates = true;
  switch (phase) {
    case "intro":
      body = introHtml(state);
      break;
    case "guess":
      body = `${questionHtml(state)}${doneHtml(state, c, { ids: question.guessed || [], word: "GUESSED" })}`;
      break;
    case "lineup":
    case "chips":
      body = `${questionHtml(state, { compact: true })}${lineLook(lineModel(state, { elapsed }), state)}`;
      break;
    case "bet":
      body = `${questionHtml(state, { compact: true })}${lineLook(lineModel(state, { elapsed }), state)}${doneHtml(state, c, { ids: question.betsIn || [], word: "BETS IN" })}`;
      break;
    case "reveal":
      // The reveal look draws the question itself, with the answer beside it.
      body = revealLook(revealModel(state, { elapsed }), state);
      plates = Boolean(question.nobody);
      break;
    case "finished":
      body = finishedHtml(state, c);
      plates = false;
      break;
    default:
      body = "";
  }
  return `${headHtml(state, c)}<div class="qz-body cg-body" data-phase="${e(phase)}">${body}</div>${plates ? platesHtml(state) : ""}`;
}

// ---------------------------------------------------------------------------------------------
// Overlays (table.js menu pages)

export function closestHelpOverlayHtml(state) {
  return `<div class="dialog dialog--help on-light qz-tvhelp cg-tvhelp" role="dialog" aria-modal="true" aria-labelledby="help-title">
    <div class="dialog__head" id="help-title">HOW TO PLAY ${e(gameTitle(state))}</div>
    <div class="dialog__body qz-tvhelp__body">${howToPlayHtml(state)}
      <div class="qz-tvhelp__side"><h3 class="set-head">ON THIS SCREEN</h3>
        <p>Everyone plays on their own phone: scan the code to join. This screen shows each question, the guesses lined up, the chips and the answer.</p>
        <h3 class="set-head">QUESTIONS</h3>${creditsHtml(state)}</div>
    </div>
    <div class="dialog__actions dialog__actions--foot"><button type="button" class="big-btn big-btn--primary" data-cmd="menu-back" data-autofocus>${ICONS.arrowLeft}BACK</button></div>
  </div>`;
}

// SETTINGS for a Near Thing table: as for Ludo and the quiz (no board camera, no trade offers).
export function closestSettingsOverlayHtml(sound = {}, screen = {}, ctx = {}) {
  return ludoSettingsOverlayHtml(sound, screen, ctx);
}

export function closestEndConfirmHtml(state) {
  const leader = rankings(state)[0]?.player || null;
  const lead = leader ? `<div class="confirm-leader">${playerTokenSvg(state, leader, { size: 34 })}<span class="confirm-leader__name">${e(upper(leader.name))}</span><span class="confirm-leader__worth">${ICONS.crown}${fmtScore(leader.score)}</span></div>` : "";
  return `<div class="dialog on-light" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title">
    <div class="dialog__head dialog__head--danger" id="confirm-title">END ${e(gameTitle(state))} NOW?</div>
    <div class="dialog__body"><p class="dialog__lead">The game ends for everyone and places go by the scores as they stand:</p>${lead}
      <div class="dialog__actions"><button type="button" class="big-btn big-btn--danger" data-cmd="confirm">END GAME</button><button type="button" class="big-btn" data-cmd="close" data-autofocus>KEEP PLAYING</button></div>
    </div></div>`;
}

// ---------------------------------------------------------------------------------------------
// Controller

export function createClosestTable(deps) {
  const { el, ui, storage } = deps;
  let aiLevel = storage.get(AI_KEY) || null;
  let ticker = null;
  let soundKey = "";
  let soundCancels = [];
  let phaseKey = "";
  let elapsedAtStart = Infinity;

  const now = () => deps.now();

  // How far into the phase this screen came in, fixed once per phase (re-renders keep the CSS
  // delays as they were). Settled (reduced motion, or a demo not asked to play): the end frame.
  function elapsedFor(state) {
    const key = `${state.code}|${state.question?.seq ?? 0}|${state.phase}`;
    if (key !== phaseKey) {
      phaseKey = key;
      elapsedAtStart = deps.settled?.() ? Infinity : phaseElapsed(state, now());
    }
    return elapsedAtStart;
  }

  function ctx(state) {
    return { ...deps.ctx(), aiLevel, now: now(), elapsed: elapsedFor(state) };
  }

  function draw(state) {
    const lobby = state.status === "lobby";
    const c = ctx(state);
    morph(el.closest, lobby ? closestLobbyHtml(state, c) : closestGameHtml(state, c));
    el.closest.dataset.mode = lobby ? "lobby" : "game";
    el.closest.dataset.phase = state.status === "finished" ? "finished" : state.phase || "";
    el.closest.dataset.demo = c.demo ? "1" : "";
  }

  // The clock's sounds (slot pops, the slide, the win, the last five seconds' ticks), scheduled
  // once per phase; and a light re-render for the countdown while guessing and betting.
  function syncTimers(state) {
    const demo = Boolean(deps.ctx().demo);
    const key = demo || state.status !== "playing" ? "" : `${state.code}|${state.question?.seq ?? 0}|${state.phase}|${state.phaseEndsAt}`;
    if (key !== soundKey) {
      for (const cancel of soundCancels) cancel();
      soundCancels = [];
      soundKey = key;
      if (key) {
        for (const hit of closestTimedSounds(state)) {
          if (hit.at > now()) soundCancels.push(scheduleAt(hit.at, () => { if (!document.hidden) deps.sound(hit.name, { ...hit.opts, volume: hit.gain }); }, { now }));
        }
      }
    }
    const live = !demo && state.status === "playing" && ["guess", "bet"].includes(state.phase) && state.phaseEndsAt > 0;
    if (live && !ticker) ticker = setInterval(() => { if (isClosestState(ui.state) && !document.hidden) draw(ui.state); }, 250);
    if (!live && ticker) {
      clearInterval(ticker);
      ticker = null;
    }
  }

  function render(state) {
    draw(state);
    syncTimers(state);
  }

  // A new state (table.js drain): sounds, then straight on screen. Nothing waits.
  function commit(prev, next) {
    if (isClosestState(prev) && !document.hidden) {
      for (const hit of closestSounds(prev, next)) deps.sound(hit.name, { ...hit.opts, volume: hit.gain });
    }
    deps.render();
  }

  function stop() {
    for (const cancel of soundCancels) cancel();
    soundCancels = [];
    soundKey = "";
    phaseKey = "";
    clearInterval(ticker);
    ticker = null;
  }

  async function command(cmd, arg) {
    const state = ui.state;
    switch (cmd) {
      case "cg-setting": {
        const chip = settingChips(state).find((candidate) => candidate.key === arg);
        if (!chip || state?.status !== "lobby") return undefined;
        deps.sound("tap");
        return deps.perform({ type: "settings", settings: chip.patch });
      }
      case "cg-ai-level": {
        const { levels, level } = levelOf(state, aiLevel);
        aiLevel = levels[(levels.indexOf(level) + 1) % levels.length];
        storage.set(AI_KEY, aiLevel);
        deps.sound("tap");
        deps.render();
        return undefined;
      }
      case "cg-add-ai": {
        const { level } = levelOf(state, aiLevel);
        return deps.addAi(level);
      }
      case "cg-help":
        return deps.openHelp?.();
      default:
        return undefined;
    }
  }

  return { render, commit, command, stop, busy: () => false };
}
