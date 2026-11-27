// Ludo on the table screen (TV / PC at /): the board big in the middle, a plate for every player
// beside their yard, the last roll on a big die in the centre, and whose turn it is across the top.
// Nothing is secret in Ludo, so keyboard and controller seats play here like Estate's:
//   keyboard   Enter or Space rolls; ← → (or ↑ ↓) pick a piece, Enter moves it; 1–4 move a piece
//   controller A rolls; the D-pad picks a piece, A moves it
// In the lobby: the game picker, the seats (× removes one), ADD COMPUTER with its level, ADD
// KEYBOARD, the house-rule chips, a preview of the board the seated players will get, and START.
// table.js hands Ludo states here and keeps the session, the menus and the sound.

import { aiLevelLabel, escapeHtml, upper } from "../shared/format.js";
import { boardSvg } from "../shared/ludo-board.js";
import { createLudoFx } from "../shared/ludo-fx.js";
import {
  actingSeat, centerDieHtml, distinctMoves, forcedMove, howToPlayHtml, isLudoState, ludoRuleChips, ludoSounds, playerOf, plateHtml, ruleChipsHtml, standingsHtml, turnLine, turnPlayer,
} from "../shared/ludo-view.js";
import { morph } from "../shared/morph.js";
import { gamePickHtml, gtypeTag } from "../shared/poker-view.js";
import { playerColor, playerTokenSvg } from "../shared/tokens.js";
import * as views from "./views.js";

const e = escapeHtml;
const { ICONS } = views;
const AI_KEY = "estate.ludoAiLevel";

export { isLudoState };

// Which side of the board each arm's plate goes on, top to bottom (each yard sits clockwise of
// its arm: on the cross, arm 0's yard is bottom left, arm 1's top left…).
export const PLATE_SIDES = {
  4: { left: [1, 0], right: [2, 3] },
  6: { left: [2, 1, 0], right: [3, 4, 5] },
};

function qrHtml(state, ctx) {
  const code = state?.code || "·····";
  if (ctx.qrUrl) return `<img class="qr qr--img" src="${e(ctx.qrUrl)}" width="300" height="300" alt="QR code that opens the join page for table ${e(code)}">`;
  return views.qrPlaceholderHtml({ label: `QR code for table ${code}`, note: ctx.demo ? "DEMO" : "" });
}

function toolsHtml(ctx, { conn = true } = {}) {
  return `<span class="pk-tv__tools">${conn ? `<span class="side__conn" data-status="${e(ctx.status || "live")}"><span class="dot" aria-hidden="true"></span><span class="side__conn-text">${ctx.status === "offline" ? "OFFLINE" : ctx.demo ? "DEMO" : ""}</span></span>` : ""}${views.soundButtonHtml(ctx.sound)}<button class="icon-btn menu-btn" type="button" data-cmd="menu" aria-haspopup="dialog" aria-label="Menu" title="Menu: settings, how to play, leave">${ICONS.menu}</button></span>`;
}

function deviceWord(state, player) {
  if (player.kind === "ai" || player.input === "ai") return upper(aiLevelLabel(player.difficulty || "normal"));
  const seat = (state.localSeats || []).find((candidate) => candidate.playerId === player.id);
  if (seat) return seat.input === "controller" ? `CONTROLLER${Number.isInteger(seat.pad) ? ` ${seat.pad + 1}` : ""}` : "KEYBOARD";
  return player.online === false ? "AWAY" : "PHONE";
}

// ---------------------------------------------------------------------------------------------
// Lobby

export function ludoLobbyHtml(state, ctx = {}) {
  const watching = ctx.watching;
  const code = state.code || "·····";
  const link = views.joinUrl(state, ctx).replace(/^https?:\/\//, "");
  const reach = views.joinReach(ctx) === "anywhere"
    ? `<span class="reach reach--anywhere">${ICONS.globe}<b>ANYWHERE</b></span>`
    : `<span class="reach reach--lan">${ICONS.wifi}<b>SAME WI‑FI</b></span>`;
  const players = (state.players || []).filter((player) => player.status !== "left");
  const max = Math.min(6, state.seatLimit || 6);
  const host = !watching && state.viewerRole === "table";
  const freePad = (ctx.pads || []).find((pad) => !pad.seated);
  const seats = [];
  let hinted = false;
  for (let index = 0; index < max; index += 1) {
    const player = players[index];
    if (player) {
      seats.push(`<li class="ld-seat" data-key="lds-${e(player.id)}" style="--pc:${playerColor(state, player)}">${playerTokenSvg(state, player, { size: 64, label: `${player.name}'s token` })}<b>${e(upper(player.name))}</b><span class="seat__level">${e(deviceWord(state, player))}</span>${host ? views.removeSeatButtonHtml(player) : ""}</li>`);
    } else if (!hinted && host) {
      hinted = true;
      seats.push(`<li class="ld-seat is-open is-hint${freePad ? " is-ready" : ""}"><span class="ld-seat__plus" aria-hidden="true">+</span><span class="ld-seat__ways"><span title="Scan the code with a phone">${ICONS.phone}</span><span title="Press A on a controller">${ICONS.pad}<b class="key-cap key-cap--pad">A</b></span><span title="Press Enter on the keyboard">${ICONS.keyboard}<b class="key-cap">↵</b></span></span><small>SCAN · A · ENTER</small></li>`);
    } else {
      seats.push('<li class="ld-seat is-open"><span class="ld-seat__plus" aria-hidden="true">+</span><span class="sr-only">Open seat</span></li>');
    }
  }
  const levels = Array.isArray(state.aiLevels) ? state.aiLevels : [];
  const level = levels.includes(ctx.aiLevel) ? ctx.aiLevel : levels.includes("normal") ? "normal" : levels[0] || "";
  const ready = players.length;
  const foot = host ? `<div class="pk-tv__foot ld-tv__foot">
      <span class="players-box__count">${ready} / ${max}</span>
      <span class="ai-pick"><button class="chip-btn" type="button" data-cmd="ld-add-ai" ${ready >= max ? "disabled" : ""} aria-label="Add a ${e(aiLevelLabel(level))} computer player">${ICONS.cpu}<span>ADD COMPUTER</span></button>${levels.length > 1 ? `<button class="chip-btn chip-btn--level" type="button" data-cmd="ld-ai-level" aria-label="Computer level: ${e(aiLevelLabel(level))}. Change">${e(upper(aiLevelLabel(level)))} <span aria-hidden="true">⇄</span></button>` : ""}</span>
      ${ctx.supportsSeats === false ? "" : `<button class="chip-btn" type="button" data-cmd="seat-keyboard" ${ready >= max ? "disabled" : ""} aria-label="Add a keyboard player">${ICONS.keyboard}<span>ADD KEYBOARD</span></button>`}
      <button class="ready-btn" type="button" data-cmd="start" ${ready >= 2 ? "" : "disabled"} aria-label="${ready >= 2 ? `Start the game with ${ready} players` : "Waiting for at least 2 players"}">START ${ICONS.arrow}</button>
    </div>` : `<div class="pk-tv__foot ld-tv__foot"><span class="players-box__count">${ready} / ${max}</span><span class="pk-tv__note">Waiting for the host to start</span></div>`;
  const arms = state.board?.arms || 4;
  return `<header class="pk-tv__head"><span class="join__brand">ESTATE</span>${gtypeTag("ludo")}${watching ? "" : views.resumeFormHtml({ code: ctx.resumeCode || "", error: ctx.resumeError || "" })}${watching ? "" : views.newTableButtonHtml()}${toolsHtml(ctx, { conn: false })}</header>
    <div class="pk-tv__lobby ld-tv__lobby">
      <section class="qr-card on-light" aria-label="Scan to join">
        <div class="qr-card__head">${watching ? "WATCHING" : "SCAN TO JOIN"}</div>
        <div class="qr-card__body">${qrHtml(state, ctx)}
          <div class="qr-card__code"><span class="qr-card__code-value" aria-label="Table code ${e(code.split("").join(" "))}">${e(code)}</span></div>
          <div class="qr-card__foot">${reach}<span class="qr-card__link">${e(link)}</span></div>${watching ? "" : views.inviteButtonsHtml()}
        </div>
      </section>
      <div class="pk-tv__right">
        ${watching ? "" : gamePickHtml("ludo", { cmd: "pick-game", subtitles: false })}
        <div class="ld-tv__mid">
          <ul class="ld-tv__seats">${seats.join("")}</ul>
          <figure class="ld-tv__preview" aria-label="The board: ${arms} arms">${boardSvg(state, { label: `Board preview: ${arms} arms` })}<figcaption>${arms === 6 ? "SIX ARMS · 5–6 PLAYERS" : "THE CROSS · 2–4 PLAYERS"}</figcaption></figure>
        </div>
        <div class="pk-tv__rules ld-tv__rules">${ruleChipsHtml(state, { host })}</div>
        ${foot}
      </div>
    </div>`;
}

// ---------------------------------------------------------------------------------------------
// The game

function sideHtml(state, arms, side) {
  const owners = new Map((state.players || []).filter((player) => player.status !== "left" || state.status === "finished").map((player) => [player.arm, player]));
  return PLATE_SIDES[arms][side].map((arm) => {
    const player = owners.get(arm);
    if (!player) return `<div class="lplate lplate--empty" data-key="lpl-empty-${arm}" aria-hidden="true"></div>`;
    const seat = (state.localSeats || []).find((candidate) => candidate.playerId === player.id) || null;
    return plateHtml(state, player, { local: seat, levelLabel: aiLevelLabel, size: 58 });
  }).join("");
}

// What the keyboard or controller seat to act can press, under the board.
export function seatHint(state, focus = null) {
  const seat = actingSeat(state);
  if (!seat) return "";
  const pad = seat.input === "controller";
  const key = (text) => `<b class="key-cap${pad ? " key-cap--pad" : ""}">${e(text)}</b>`;
  const player = playerOf(state, seat.playerId);
  const who = `<span class="ld-hint__who">${e(upper(player?.name || ""))}</span>`;
  if (state.phase === "roll") return `<div class="ld-hint" data-key="ld-hint">${who}${key(pad ? "A" : "ENTER")}<span>ROLL</span></div>`;
  if (state.phase === "move" && !forcedMove(state)) {
    return `<div class="ld-hint" data-key="ld-hint">${who}${pad ? key("◀ ▶") : `${key("←")}${key("→")}`}<span>PICK A PIECE</span>${key(pad ? "A" : "ENTER")}<span>MOVE${Number.isInteger(focus) ? ` PIECE ${focus + 1}` : ""}</span></div>`;
  }
  return "";
}

function finalHtml(state, ctx) {
  const winner = playerOf(state, state.winnerId);
  const host = state.viewerRole === "table" && !ctx.watching;
  return `<div class="lfinal-wrap"><div class="lfinal on-light" role="dialog" aria-label="Final places">
    <div class="lfinal__head"><span class="lfinal__eyebrow">FINAL PLACES</span>${winner ? `${e(upper(winner.name))} WINS` : "GAME OVER"}</div>
    ${standingsHtml(state, { size: 40 })}
    ${host ? `<div class="lfinal__btns"><button type="button" class="big-btn big-btn--primary" data-cmd="rematch" data-autofocus>PLAY AGAIN</button><button type="button" class="big-btn" data-cmd="new-table">NEW TABLE</button></div>` : ""}
  </div></div>`;
}

// Only a keyboard or controller seat to act has a picked piece (and its landing square shown).
function actingSeatFocus(state, focus) {
  return actingSeat(state) && state.phase === "move" && Number.isInteger(focus) ? focus : null;
}

export function ludoGameHtml(state, ctx = {}) {
  const arms = Number(state.board?.arms) === 6 ? 6 : 4;
  const local = (state.localSeats || []).map((seat) => seat.playerId);
  const player = turnPlayer(state);
  const color = player ? playerColor(state, player) : "#F2C14E";
  const line = turnLine(state, { local });
  const banner = `<span class="ld-tv__turn${player ? "" : " is-idle"}" style="--pc:${color}">${player ? playerTokenSvg(state, player, { size: 46 }) : ""}<b>${e(line)}</b></span>`;
  const board = boardSvg(state, { local, showAll: true, focus: actingSeatFocus(state, ctx.focus), label: `Ludo board: ${line}` });
  return `<header class="pk-tv__head ld-tv__head"><span class="table-code">${views.tableCodeHtml(state.code)}</span>${banner}${toolsHtml(ctx)}</header>
    <div class="ld-tv__body">
      <aside class="ld-tv__side" aria-label="Players">${sideHtml(state, arms, "left")}</aside>
      <div class="ld-tv__boardwrap"><div class="ld-tv__board ld-board" data-arms="${arms}">${board}${centerDieHtml(state)}</div></div>
      <aside class="ld-tv__side" aria-label="Players">${sideHtml(state, arms, "right")}</aside>
    </div>
    ${seatHint(state, ctx.focus ?? null)}
    ${state.status === "finished" ? finalHtml(state, ctx) : ""}`;
}

export function ludoHelpOverlayHtml(state) {
  const key = (text) => `<b class="key-cap">${e(text)}</b>`;
  return `<div class="dialog dialog--help on-light ld-tvhelp" role="dialog" aria-modal="true" aria-labelledby="help-title">
    <div class="dialog__head" id="help-title">HOW TO PLAY LUDO</div>
    <div class="dialog__body ld-tvhelp__body">${howToPlayHtml(state)}
      <div class="ld-keys"><h3 class="set-head">ON THIS SCREEN</h3>
        <p>Keyboard: ${key("ENTER")} roll · ${key("←")}${key("→")} pick a piece · ${key("ENTER")} move it · ${key("1")}–${key("4")} move that piece</p>
        <p>Controller: ${key("A")} roll · D-pad pick · ${key("A")} move</p>
        <p>Phones: tap ROLL, then tap a glowing piece.</p></div>
    </div>
    <div class="dialog__actions dialog__actions--foot"><button type="button" class="big-btn big-btn--primary" data-cmd="menu-back" data-autofocus>${ICONS.arrowLeft}BACK</button></div>
  </div>`;
}

// SETTINGS for a Ludo table: Estate's, without the board camera (Ludo's board never moves).
export function ludoSettingsOverlayHtml(sound = {}, screen = {}, ctx = {}) {
  const html = views.settingsOverlayHtml(sound, screen, ctx);
  const start = html.indexOf('<h3 class="set-head">CAMERA AND MOTION</h3>');
  const rowStart = html.lastIndexOf('<div class="set-row">', html.indexOf("ANIMATIONS</span>"));
  if (start < 0 || rowStart < start) return html;
  const out = `${html.slice(0, start)}<h3 class="set-head">MOTION</h3>${html.slice(rowStart)}`;
  // Computers' trade offers are Estate's: that row goes.
  const at = out.indexOf("COMPUTER OFFERS TO PEOPLE");
  if (at < 0) return out;
  const from = out.lastIndexOf('<div class="set-row">', at);
  const to = out.indexOf("</div>", at) + "</div>".length;
  return from < 0 || to < at ? out : out.slice(0, from) + out.slice(to);
}

export function ludoEndConfirmHtml(state) {
  const leader = [...(state?.players || [])].filter((player) => player.status !== "left").sort((a, b) => (b.home - a.home) || (b.progress - a.progress))[0];
  const lead = leader ? `<div class="confirm-leader">${playerTokenSvg(state, leader, { size: 34 })}<span class="confirm-leader__name">${e(upper(leader.name))}</span><span class="confirm-leader__worth">${ICONS.crown}${leader.progress}%</span></div>` : "";
  return `<div class="dialog on-light" role="alertdialog" aria-modal="true" aria-labelledby="confirm-title">
    <div class="dialog__head dialog__head--danger" id="confirm-title">END THE GAME NOW?</div>
    <div class="dialog__body"><p class="dialog__lead">The game ends for everyone. Players home keep their places; the rest are ranked by how far their pieces got:</p>${lead}
      <div class="dialog__actions"><button type="button" class="big-btn big-btn--danger" data-cmd="confirm">END GAME</button><button type="button" class="big-btn" data-cmd="close" data-autofocus>KEEP PLAYING</button></div>
    </div></div>`;
}

// ---------------------------------------------------------------------------------------------
// Controller

export function createLudoTable(deps) {
  const { el, ui, storage } = deps;
  let aiLevel = storage.get(AI_KEY) || null;
  let shown = null; // the state on screen (behind ui.state while an animation plays)
  let animating = false;
  let focus = null; // the piece a keyboard or controller seat has picked
  let focusKey = "";
  const fx = createLudoFx({ sound: (name, opts) => deps.sound(name, opts), speed: () => (deps.fast?.() ? 0.6 : 1) });

  function ctx() {
    return { ...deps.ctx(), aiLevel, focus };
  }

  // Keep the picked piece on a movable one (the first, when the decision is new).
  function syncFocus(state) {
    const seat = actingSeat(state);
    const key = seat && state.phase === "move" ? `${state.code}|${state.rollSeq}` : "";
    const choices = distinctMoves(state.moves);
    if (key !== focusKey) {
      focusKey = key;
      focus = key ? choices[0]?.piece ?? null : null;
    }
    if (key && !choices.some((move) => move.piece === focus)) focus = choices[0]?.piece ?? null;
  }

  function draw(state) {
    syncFocus(state);
    const lobby = state.status === "lobby";
    morph(el.ludo, lobby ? ludoLobbyHtml(state, ctx()) : ludoGameHtml(state, ctx()));
    el.ludo.dataset.mode = lobby ? "lobby" : "game";
    el.ludo.dataset.arms = String(state.board?.arms || 4);
  }

  function render(state) {
    if (animating && shown) return draw(shown);
    shown = state;
    return draw(state);
  }

  // A new Ludo state (table.js drain, in order): animate what happened, then show it.
  async function commit(prev, next) {
    const same = isLudoState(prev) && prev.code === next.code && prev.status !== "lobby" && next.status !== "lobby";
    if (!same || document.hidden) {
      shown = next;
      deps.render();
      return;
    }
    for (const hit of ludoSounds(prev, next)) {
      if (["jail", "gavel", "go", "land"].includes(hit.name) && !deps.reduced()) continue; // the animation plays those
      deps.sound(hit.name, { volume: hit.gain });
    }
    animating = true;
    try {
      await fx.play(el.ludo, prev, next, () => {
        animating = false;
        shown = next;
        deps.render();
      }, { reduced: deps.reduced() });
    } finally {
      animating = false;
      shown = next;
    }
  }

  // ---- Acting for the keyboard and controller seats -----------------------------------------

  async function roll(seat) {
    deps.sound("tap");
    return deps.perform({ type: "roll" }, seat.seatId);
  }

  async function move(seat, piece) {
    if (!Number.isInteger(piece)) return undefined;
    return deps.perform({ type: "move", piece }, seat.seatId);
  }

  function cycle(delta) {
    const state = ui.state;
    const moves = distinctMoves(state?.moves);
    if (moves.length < 2) return false;
    const at = Math.max(0, moves.findIndex((m) => m.piece === focus));
    focus = moves[(at + delta + moves.length) % moves.length].piece;
    deps.sound("tap", { volume: 0.5 });
    deps.render();
    return true;
  }

  // A key on the table screen during a game. Returns true when Ludo used it.
  function key(event) {
    const state = ui.state;
    if (!isLudoState(state) || state.status !== "playing" || animating) return false;
    const seat = actingSeat(state);
    if (!seat || seat.input !== "keyboard") return false;
    const k = event.key;
    if (state.phase === "roll" && (k === "Enter" || k === " " || k.toUpperCase() === "R")) {
      roll(seat);
      return true;
    }
    if (state.phase === "move") {
      if (k === "ArrowRight" || k === "ArrowDown") return cycle(1) || true;
      if (k === "ArrowLeft" || k === "ArrowUp") return cycle(-1) || true;
      if (k === "Enter" || k === " ") {
        move(seat, focus);
        return true;
      }
      if (/^[1-4]$/.test(k)) {
        const piece = Number(k) - 1;
        if (state.moves.some((m) => m.piece === piece)) move(seat, piece);
        return true;
      }
    }
    return false;
  }

  // A controller button (or D-pad direction) for pad `index`. Returns true when Ludo used it.
  function pad(index, button) {
    const state = ui.state;
    if (!isLudoState(state) || state.status !== "playing") return false;
    const seat = actingSeat(state);
    if (!seat || seat.input !== "controller" || seat.pad !== index) return false;
    if (animating) return true;
    if (state.phase === "roll" && button === "A") {
      roll(seat);
      return true;
    }
    if (state.phase === "move") {
      if (button === "right" || button === "down") return cycle(1) || true;
      if (button === "left" || button === "up") return cycle(-1) || true;
      if (button === "A") {
        move(seat, focus);
        return true;
      }
    }
    return false;
  }

  // Clicks on the board: a movable piece of a local seat moves (handy with a mouse too).
  function click(target) {
    const state = ui.state;
    const node = target.closest?.(".lp.is-movable");
    if (!node || !isLudoState(state) || animating) return false;
    const seat = actingSeat(state);
    const piece = Number(node.dataset.piece);
    if (!seat || node.dataset.player !== seat.playerId) return false;
    if (focus === piece || forcedMove(state)) move(seat, piece);
    else {
      focus = piece;
      deps.render();
    }
    return true;
  }

  async function command(cmd, arg) {
    const state = ui.state;
    switch (cmd) {
      case "ld-rule": {
        const chip = ludoRuleChips(state).find((candidate) => candidate.key === arg);
        if (!chip) return undefined;
        deps.sound("tap");
        return deps.perform({ type: "settings", settings: chip.patch });
      }
      case "ld-ai-level": {
        const levels = Array.isArray(state?.aiLevels) && state.aiLevels.length ? state.aiLevels : ["easy", "normal", "hard"];
        const shown = levels.includes(aiLevel) ? aiLevel : levels.includes("normal") ? "normal" : levels[0];
        aiLevel = levels[(levels.indexOf(shown) + 1) % levels.length];
        storage.set(AI_KEY, aiLevel);
        deps.sound("tap");
        deps.render();
        return undefined;
      }
      case "ld-add-ai": {
        const levels = Array.isArray(state?.aiLevels) ? state.aiLevels : [];
        return deps.addAi(levels.includes(aiLevel) ? aiLevel : levels.includes("normal") ? "normal" : levels[0] || null);
      }
      default:
        return undefined;
    }
  }

  return { render, commit, command, key, pad, click, busy: () => animating, focus: () => focus };
}
