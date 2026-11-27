// Poker on the table screen (TV / PC at /): a big viewer of the same table the phones show, with
// face-up cards only, the QR code to join, and no controller or keyboard seats (poker is phones
// only). In the lobby: the ESTATE / POKER picker, the seats, the rule chips (the table is the host)
// and START. table.js hands poker states here and keeps the session, menus and sound.

import { escapeHtml, ordinal, upper, aiLevelLabel } from "../shared/format.js";
import { morph } from "../shared/morph.js";
import { serverClock } from "../shared/poker.js";
import { createPokerFx } from "../shared/poker-fx.js";
import {
  actTimer, blindsPillHtml, chips, clockText, gamePickHtml, gtypeTag, handRanksHtml, howStepsHtml, tableHtml, tableModel,
} from "../shared/poker-view.js";
import { fourColour, loadPokerPrefs, nextLevel, pokerSounds, ruleChips, savePokerPrefs } from "../phone/poker-play.js";
import { playerTokenSvg } from "../shared/tokens.js";
import * as views from "./views.js";

const e = escapeHtml;
const { ICONS } = views;
const AI_KEY = "estate.pokerAiLevel";

export function isPokerState(state) {
  return state?.game === "poker";
}

// What the table shows under the action: whose turn, or what comes next.
export function tvLine(state, now = Date.now()) {
  if (!state || state.status !== "playing") return "";
  const actor = state.players?.find((player) => player.id === state.actorId);
  if (state.phase === "betting" && actor) return `${upper(actor.name)}'S TURN`;
  if (state.phase === "runout") return "ALL IN · CARDS COMING";
  if (state.phase === "paused") return "WAITING FOR PLAYERS";
  if ((state.phase === "showdown" || state.phase === "handOver") && state.nextHandAt > now) return `NEXT HAND IN <span data-next-clock>${Math.ceil((state.nextHandAt - now) / 1000)}</span>`;
  return "";
}

function qrHtml(state, ctx, size = "big") {
  const code = state?.code || "·····";
  if (ctx.qrUrl) return `<img class="qr qr--img" src="${e(ctx.qrUrl)}" width="${size === "big" ? 300 : 96}" height="${size === "big" ? 300 : 96}" alt="QR code that opens the join page for table ${e(code)}">`;
  return views.qrPlaceholderHtml({ label: `QR code for table ${code}`, note: ctx.demo && size === "big" ? "DEMO" : "" });
}

function headHtml(state, ctx, { lobby = false } = {}) {
  const right = `<span class="pk-tv__tools">${lobby ? "" : `<span class="side__conn" data-status="${e(ctx.status || "live")}"><span class="dot" aria-hidden="true"></span><span class="side__conn-text">${ctx.status === "offline" ? "OFFLINE" : ctx.demo ? "DEMO" : ""}</span></span>`}${views.soundButtonHtml(ctx.sound)}<button class="icon-btn menu-btn" type="button" data-cmd="menu" aria-haspopup="dialog" aria-label="Menu" title="Menu: settings, how to play, leave">${ICONS.menu}</button></span>`;
  if (lobby) {
    return `<header class="pk-tv__head"><span class="join__brand">ESTATE</span>${gtypeTag("poker")}${ctx.watching ? "" : views.resumeFormHtml({ code: ctx.resumeCode || "", error: ctx.resumeError || "" })}${ctx.watching ? "" : views.newTableButtonHtml()}${right}</header>`;
  }
  const hand = state.handNumber ? `<span class="pk-tv__hand">HAND <b>${state.handNumber}</b></span>` : "";
  return `<header class="pk-tv__head"><span class="table-code">${views.tableCodeHtml(state.code)}</span>${blindsPillHtml(state, { now: ctx.now })}${hand}${right}</header>`;
}

// Lobby: QR card on the left; the game picker, seats, rules and START on the right.
export function pokerLobbyHtml(state, ctx = {}) {
  const watching = ctx.watching;
  const code = state.code || "·····";
  const link = views.joinUrl(state, ctx).replace(/^https?:\/\//, "");
  const reach = views.joinReach(ctx) === "anywhere"
    ? `<span class="reach reach--anywhere">${ICONS.globe}<b>ANYWHERE</b></span>`
    : `<span class="reach reach--lan">${ICONS.wifi}<b>SAME WI‑FI</b></span>`;
  const players = (state.players || []).filter((player) => player.status !== "left");
  const max = Math.min(state.seatLimit || 8, state.settings?.maxPlayers || 8);
  const host = !watching && state.viewerRole === "table";
  const seats = [];
  for (let index = 0; index < max; index += 1) {
    const player = players[index];
    if (!player) {
      seats.push(`<li class="pk-tv__seat is-open" aria-label="Open seat"><span aria-hidden="true">+</span></li>`);
      continue;
    }
    const ai = player.kind === "ai" || player.input === "ai";
    // × removes the seat before the start (the table is the host; 0.10.2).
    seats.push(`<li class="pk-tv__seat" data-key="tvs-${e(player.id)}">${playerTokenSvg(state, player, { size: 70, label: `${player.name}'s token` })}<b>${e(upper(player.name))}</b>${ai && player.difficulty ? `<span class="seat__level">${e(upper(aiLevelLabel(player.difficulty)))}</span>` : `<span class="seat__level">${player.online === false ? "AWAY" : "PHONE"}</span>`}${host ? views.removeSeatButtonHtml(player) : ""}</li>`);
  }
  const rules = ruleChips(state).map((chip) => (host
    ? `<button type="button" class="rchip" data-cmd="pk-rule" data-arg="${e(chip.key)}" aria-label="${e(chip.short)}: ${e(chip.value)}. Change"><span>${e(chip.short)}</span><b>${e(chip.value)}</b></button>`
    : `<span class="rchip"><span>${e(chip.short)}</span><b>${e(chip.value)}</b></span>`)).join("");
  const levels = Array.isArray(state.aiLevels) ? state.aiLevels : [];
  const level = levels.includes(ctx.aiLevel) ? ctx.aiLevel : levels[levels.length - 1] || "";
  const ready = players.length;
  const foot = host ? `<div class="pk-tv__foot">
      <span class="players-box__count">${ready} / ${max}</span>
      <span class="pk-tv__note">Poker is played on phones: scan to sit down</span>
      <span class="ai-pick" style="margin-left:auto"><button class="chip-btn" type="button" data-cmd="pk-add-ai" ${ready >= max ? "disabled" : ""} aria-label="Add a computer player">${ICONS.cpu}<span aria-hidden="true">+</span></button>${levels.length > 1 ? `<button class="chip-btn chip-btn--level" type="button" data-cmd="pk-ai-level" aria-label="Computer level: ${e(aiLevelLabel(level))}. Change">${e(upper(aiLevelLabel(level)))} <span aria-hidden="true">⇄</span></button>` : ""}</span>
      <button class="ready-btn" type="button" data-cmd="start" ${ready >= 2 ? "" : "disabled"} aria-label="${ready >= 2 ? `Start the game with ${ready} players` : "Waiting for at least 2 players"}">START ${ICONS.arrow}</button>
    </div>` : `<div class="pk-tv__foot"><span class="players-box__count">${ready} / ${max}</span><span class="pk-tv__note">Waiting for the host to start</span></div>`;
  return `${headHtml(state, ctx, { lobby: true })}
    <div class="pk-tv__lobby">
      <section class="qr-card on-light" aria-label="Scan to join">
        <div class="qr-card__head">${watching ? "WATCHING" : "SCAN TO JOIN"}</div>
        <div class="qr-card__body">${qrHtml(state, ctx)}
          <div class="qr-card__code"><span class="qr-card__code-value" aria-label="Table code ${e(code.split("").join(" "))}">${e(code)}</span></div>
          <div class="qr-card__foot">${reach}<span class="qr-card__link">${e(link)}</span></div>${watching ? "" : views.inviteButtonsHtml()}
        </div>
      </section>
      <div class="pk-tv__right">
        ${watching ? "" : gamePickHtml("poker", { cmd: "pick-game", subtitles: false })}
        <ul class="pk-tv__seats">${seats.join("")}</ul>
        <div class="pk-tv__rules">${rules}</div>
        ${foot}
      </div>
    </div>`;
}

// Final standings (Estate's victory card, with chips).
function finalHtml(state) {
  const standings = Array.isArray(state.standings) && state.standings.length ? state.standings
    : [...state.players].sort((a, b) => (a.place || 99) - (b.place || 99)).map((player, index) => ({ id: player.id, name: player.name, place: player.place || index + 1, stack: player.stack }));
  const winner = state.players.find((player) => player.id === state.winnerId);
  const cash = state.settings?.style === "cash";
  const rows = standings.map((entry) => {
    const player = state.players.find((candidate) => candidate.id === entry.id) || entry;
    const how = cash && Number.isFinite(entry.net) ? `${entry.net >= 0 ? "+" : "−"}${chips(Math.abs(entry.net))}` : chips(entry.stack);
    return `<li class="pk-final__row${entry.id === state.winnerId ? " is-win" : ""}"><span class="pk-final__place">${e(ordinal(entry.place))}</span>${playerTokenSvg(state, player, { size: 40 })}<span class="pk-final__name">${e(upper(entry.name))}</span><b>${how}</b></li>`;
  }).join("");
  const host = state.viewerRole === "table";
  return `<div class="pk-final-wrap"><div class="pk-final on-light" role="dialog" aria-label="Final standings">
    <div class="pk-final__head"><span class="pk-final__eyebrow">FINAL STANDINGS</span>${winner ? `${e(upper(winner.name))} WINS` : "GAME OVER"}</div>
    <ol class="pk-final__list">${rows}</ol>
    ${host ? `<div class="pk-final__btns"><button type="button" class="big-btn big-btn--primary" data-cmd="rematch">PLAY AGAIN</button><button type="button" class="big-btn" data-cmd="new-table">NEW TABLE</button></div>` : ""}
  </div></div>`;
}

// In a game: the header, the big landscape table, the QR to join, whose turn.
export function pokerGameHtml(state, ctx = {}) {
  const model = tableModel(state, { viewerId: null, handNames: ctx.handNames !== false, now: ctx.now });
  const four = ctx.fourColor ?? Boolean(state.settings?.fourColorDeck);
  const line = tvLine(state, ctx.now);
  const join = state.status === "playing" && state.settings?.lateJoin !== false
    ? `<div class="pk-tv__join">${qrHtml(state, ctx, "small")}<span class="pk-tv__join-text"><span>JOIN AT</span><b>${e(state.code)}</b></span></div>` : "";
  return `${headHtml(state, ctx)}
    <div class="pk-tv__body">${tableHtml(model, { fourColor: four, shape: "landscape" })}${join}${line ? `<div class="pk-tv__line">${line}</div>` : ""}${state.status === "finished" ? finalHtml(state) : ""}</div>`;
}

export function pokerHelpOverlayHtml(state, { fourColor = false } = {}) {
  return `<div class="dialog dialog--help on-light pk-tvhelp" role="dialog" aria-modal="true" aria-labelledby="help-title">
    <div class="dialog__head" id="help-title">HOW TO PLAY</div>
    <div class="dialog__body pk-help pk-tvhelp__body">${howStepsHtml()}${handRanksHtml({ fourColor })}</div>
    <div class="dialog__actions dialog__actions--foot"><button type="button" class="big-btn big-btn--primary" data-cmd="menu-back" data-autofocus>${ICONS.arrowLeft}BACK</button></div>
  </div>`;
}

// SETTINGS for a poker table: sound as for Estate; CARD COLOURS and HAND NAMES take the camera rows'
// place; COMPUTER SPEED, FULL SCREEN, PUBLIC GAME as for Estate.
export function pokerSettingsOverlayHtml(sound = {}, prefs = {}, ctx = {}) {
  const html = views.settingsOverlayHtml(sound, {}, ctx);
  const choice = (key, options, current) => `<span class="set-choice" role="group">${options.map(([value, label]) => `<button type="button" class="set-choice__opt${String(current) === String(value) ? " is-on" : ""}" data-set="${key}" data-value="${value}" aria-pressed="${String(current) === String(value)}">${label}</button>`).join("")}</span>`;
  const row = (label, control) => `<div class="set-row"><span class="set-row__label">${ICONS.card}${label}</span><span class="set-row__control">${control}</span></div>`;
  const cards = `<h3 class="set-head">CARDS</h3>
      ${row("CARD COLOURS", choice("pkCards", [["table", "TABLE"], ["2", "2"], ["4", "4"]], prefs.cards || "table"))}
      ${row("HAND NAMES", choice("pkHandNames", [["true", "ON"], ["false", "OFF"]], String(prefs.handNames !== false)))}`;
  // Swap the CAMERA AND MOTION block's camera rows (CLOSE-UPS, ZOOM) for the card rows.
  const start = html.indexOf('<h3 class="set-head">CAMERA AND MOTION</h3>');
  const rowStart = html.lastIndexOf('<div class="set-row">', html.indexOf("ANIMATIONS</span>"));
  if (start < 0 || rowStart < start) return html;
  return `${html.slice(0, start)}${cards}<h3 class="set-head">MOTION</h3>${html.slice(rowStart)}`;
}

export function pokerEndConfirmHtml(state) {
  const ranked = [...(state?.players || [])].filter((player) => !["out", "left"].includes(player.status)).sort((a, b) => (b.stack + (b.bet || 0)) - (a.stack + (a.bet || 0)));
  const top = ranked[0];
  const lead = top ? `<div class="confirm-leader">${playerTokenSvg(state, top, { size: 34 })}<span class="confirm-leader__name">${e(upper(top.name))}</span><span class="confirm-leader__worth">${ICONS.crown}${chips(top.stack + (top.bet || 0))}</span></div>` : "";
  return `<div class="dialog on-light" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title">
    <div class="dialog__head dialog__head--danger" id="confirm-title">END THE GAME NOW?</div>
    <div class="dialog__body"><p class="dialog__lead">The game ends for everyone and the biggest stack wins. A hand in play is called off and its chips go back:</p>${lead}
      <div class="dialog__actions"><button type="button" class="big-btn big-btn--danger" data-cmd="confirm">END GAME</button><button type="button" class="big-btn" data-cmd="close" data-autofocus>KEEP PLAYING</button></div>
    </div></div>`;
}

// ---------------------------------------------------------------------------------------------
// Controller

export function createPokerTable(deps) {
  const { el, ui, storage } = deps;
  const clock = serverClock();
  const fx = createPokerFx(document.body);
  let prefs = loadPokerPrefs(storage);
  let aiLevel = storage.get(AI_KEY) || null;
  let timer = null;
  let frozen = null;
  const now = () => (frozen !== null ? frozen : clock.now());

  function ctx() {
    return { ...deps.ctx(), now: now(), aiLevel, handNames: prefs.handNames !== false, fourColor: fourColour(prefs, ui.state) };
  }

  function render(state) {
    const lobby = state.status === "lobby";
    const html = lobby ? pokerLobbyHtml(state, ctx()) : pokerGameHtml(state, ctx());
    morph(el.poker, html);
    el.poker.dataset.mode = lobby ? "lobby" : "game";
    tick();
    if (!timer) timer = setInterval(() => (isPokerState(ui.state) ? tick() : stop()), 250);
  }

  function stop() {
    clearInterval(timer);
    timer = null;
    el.poker.style.removeProperty("--pk-t");
    el.poker.style.removeProperty("--pk-s");
    delete el.poker.dataset.pkClock;
  }

  function tick() {
    const state = ui.state;
    if (!isPokerState(state)) return;
    const t = now();
    const run = actTimer(state, t);
    if (run) {
      el.poker.style.setProperty("--pk-t", Math.max(0, Math.min(1, run.left / run.total)).toFixed(3));
      el.poker.style.setProperty("--pk-s", String(Math.ceil(run.left)));
      const mode = run.bank ? "bank" : run.left <= 5 ? "low" : "";
      if ((el.poker.dataset.pkClock || "") !== mode) {
        if (mode) el.poker.dataset.pkClock = mode;
        else delete el.poker.dataset.pkClock;
      }
    } else {
      el.poker.style.removeProperty("--pk-t");
      delete el.poker.dataset.pkClock;
    }
    const level = el.poker.querySelector("[data-level-clock]");
    if (level && state.level?.endsAt) {
      const text = clockText(state.level.endsAt - t);
      if (level.textContent !== text) level.textContent = text;
    }
    const next = el.poker.querySelector("[data-next-clock]");
    if (next && state.nextHandAt) {
      const text = String(Math.max(0, Math.ceil((state.nextHandAt - t) / 1000)));
      if (next.textContent !== text) next.textContent = text;
    }
  }

  // A new poker state (table.js drain): sounds and moving chips around the in-place update.
  function commit(prev, next) {
    clock.sync(next);
    if (ui.demo && !/[?&]live=1/.test(location.search)) frozen = next.now || Date.now();
    const same = isPokerState(prev) && prev.code === next.code && prev.status !== "lobby" && next.status !== "lobby";
    const before = same && !el.poker.hidden ? fx.snapshot(el.poker) : null;
    deps.render();
    if (!same || document.hidden) return;
    for (const hit of pokerSounds({ ...prev, viewerId: null }, { ...next, viewerId: null })) {
      if (hit.name === "turn") continue;
      deps.sound(hit.name, { ...hit.opts, volume: Math.min(1, hit.gain + 0.4) });
    }
    if (document.documentElement.dataset.motion !== "reduce" && !deps.reduced()) fx.animate(el.poker, prev, next, before);
  }

  async function command(cmd, arg) {
    const state = ui.state;
    switch (cmd) {
      case "pk-rule": {
        const chip = ruleChips(state).find((candidate) => candidate.key === arg);
        if (chip) return deps.perform({ type: "settings", settings: chip.patch });
        return undefined;
      }
      case "pk-ai-level":
        aiLevel = nextLevel(state, aiLevel);
        storage.set(AI_KEY, aiLevel);
        deps.renderAll();
        return undefined;
      case "pk-add-ai": {
        const levels = Array.isArray(state?.aiLevels) ? state.aiLevels : [];
        return deps.addAi(levels.includes(aiLevel) ? aiLevel : levels[levels.length - 1] || null);
      }
      default:
        return undefined;
    }
  }

  // SETTINGS rows of its own (data-set="pkCards" / "pkHandNames").
  function setting(target) {
    const key = target.dataset.set;
    const value = target.dataset.value;
    if (key === "pkCards") prefs = savePokerPrefs(storage, { ...prefs, cards: value });
    else if (key === "pkHandNames") prefs = savePokerPrefs(storage, { ...prefs, handNames: value === "true" });
    else return false;
    deps.renderAll();
    return true;
  }

  return { render, commit, command, setting, prefs: () => prefs, stop };
}
