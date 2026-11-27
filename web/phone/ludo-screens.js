// Ludo screens for the phone (/play): lobby, the board, final places and the Ludo sheets. Pure:
// state + view in, HTML strings out (the same slots as screens.js renderPhone: header, banner,
// main, nav). phone.js picks these when state.game === "ludo"; ludo-app.js wires them.
//
// The game screen is one column that fits a 360–390 px phone without scrolling:
//   top bar   turn number, whose turn, MORE
//   board     the whole board, square, edge to edge; your movable pieces glow (tap one)
//   players   a strip of every player: token, pieces home, the die on whoever is rolling
//   action    your turn: a big ROLL; with a choice, one big button per piece you can move (the
//             pieces on the board are small to tap); otherwise whose turn it is

import { aiLevelLabel, escapeHtml, ordinal, upper } from "../shared/format.js";
import { boardSvg } from "../shared/ludo-board.js";
import { centerDieHtml, distinctMoves, forcedMove, homePips, howToPlayHtml, isLudoState, lastNews, playerOf, ruleChipsHtml, standingsHtml, turnLine, turnPlayer } from "../shared/ludo-view.js";
import { gamePickHtml, gtypeTag } from "../shared/poker-view.js";
import { inkOn, playerColor, playerTokenSvg } from "../shared/tokens.js";
import { icon } from "./icons.js";
import { btn, buttonsHtml } from "./parts.js";

const e = escapeHtml;
const GOLD = ["#F2C14E", "#15191E"];

export const isLudo = isLudoState;

export function mePlayer(state) {
  return playerOf(state, state?.viewerId);
}

// Which Ludo screen: ld-lobby, ld-invite, ld-table or ld-result.
export function ludoRoute(state, view = {}) {
  if (!state) return "ld-table";
  if (view.inviteOpen) return "ld-invite";
  if (state.status === "lobby") return "ld-lobby";
  if (state.status === "finished" && !view.ld?.seeBoard) return "ld-result";
  return "ld-table";
}

export function aiLevelsOf(state) {
  return Array.isArray(state?.aiLevels) && state.aiLevels.length ? state.aiLevels : ["easy", "normal", "hard"];
}

function logoBar(state, view) {
  const watch = view.session?.role === "spectator";
  return `<div class="bar bar--logo"><span class="bar__logo">ESTATE</span>${gtypeTag("ludo")}${state?.code ? `<span class="bar__table">${watch ? "Watching" : "Table"} <strong>${e(state.code)}</strong></span>` : ""}<button type="button" class="bar__gear" data-cmd="settings" aria-label="Settings">${icon("gear", { size: 20 })}</button></div>`;
}

// ---------------------------------------------------------------------------------------------
// Lobby

function lobbyScreen(state, view) {
  const me = mePlayer(state);
  const host = Boolean(me) && (state.hostId === me.id || state.viewerIsHost);
  const seats = state.players.filter((player) => player.status !== "left");
  const list = seats.map((player) => {
    const ai = player.kind === "ai" || player.input === "ai";
    const level = ai ? `<span class="seat__level seat__level--${e(player.difficulty || "normal")}">${e(upper(aiLevelLabel(player.difficulty || "normal")))}</span>` : "";
    // Keyboard and controller seats play on the PC.
    const where = !ai && (player.input === "keyboard" || player.input === "controller") ? '<span class="lseat__tag">ON PC</span>' : !ai && player.online === false ? '<span class="lseat__tag">AWAY</span>' : "";
    return `<li class="lseat${player.id === me?.id ? " is-me" : ""}" data-key="ls-${e(player.id)}">
      ${playerTokenSvg(state, player, { size: 32 })}
      <span class="lseat__who"><span class="lseat__name">${e(player.id === me?.id ? "YOU" : upper(player.name))}</span>${level}</span>${where}
      ${host && player.id !== me?.id ? `<button type="button" class="lseat__remove" data-cmd="remove" data-arg="${e(player.id)}" aria-label="Remove ${e(player.name)}">×</button>` : ""}
    </li>`;
  }).join("");
  const levels = aiLevelsOf(state);
  const level = view.ld?.aiLevel && levels.includes(view.ld.aiLevel) ? view.ld.aiLevel : levels.includes("normal") ? "normal" : levels[0];
  const levelChip = host && levels.length > 1 ? `<button type="button" class="rchip" data-cmd="ld-ai-level" aria-label="New computer players: ${e(aiLevelLabel(level))}. Tap to change"><span>COMPUTER</span><b>${e(upper(aiLevelLabel(level)))}</b></button>` : "";
  const max = Math.min(6, state.seatLimit || 6);
  const ready = seats.length;
  const hostPlayer = playerOf(state, state.hostId);
  const buttons = host
    ? [
      btn(ready >= 2 ? `START · ${ready} PLAYERS` : "START", ready >= 2 ? "" : "needs 2 players", "primary", { act: { type: "start" }, disabled: ready < 2 }),
      btn("+ COMPUTER", ready >= max ? "table is full" : "", "ghost", { cmd: "ld-add-ai", disabled: ready >= max }),
    ]
    : [btn(hostPlayer ? `WAITING FOR ${upper(hostPlayer.name)}` : "WAITING FOR THE HOST", "", "ghost", { disabled: true })];
  const codeCard = `<section class="codecard" aria-label="Table code">
      <div class="codecard__code"><span>CODE</span><strong>${e(state.code)}</strong></div>
      <div class="codecard__btns"><button type="button" class="small-btn is-gold" data-cmd="share">INVITE</button><button type="button" class="small-btn" data-cmd="copy">${view.copied ? "COPIED" : "COPY LINK"}</button><button type="button" class="small-btn is-wa" data-cmd="whatsapp">WHATSAPP</button></div>
      ${view.tableOnline === false && host ? `<p class="codecard__pc">${icon("screen", { size: 13 })}On a PC: open it and pick this game</p>` : ""}
    </section>`;
  const arms = state.board?.arms || 4;
  const picker = host ? gamePickHtml("ludo", { cmd: "ld-switch-game", subtitles: false }) : `<div class="pk-gamechosen">${gtypeTag("ludo")}<span>Ludo · ${arms} arms · up to ${max} players</span></div>`;
  const preview = `<div class="ldp-preview ld-board" data-arms="${arms}">${boardSvg(state, { label: `Board preview: ${arms} arms` })}</div>`;
  const chipsRow = [{ html: `${icon("deal", { size: 14 })} FRIENDS`, cmd: "open-invite" }, { html: "HOW TO PLAY", cmd: "ld-help" }, { html: "LEAVE", cmd: "leave-ask" }];
  const main = `${codeCard}${picker}<ul class="lseats">${list}</ul>${preview}<div class="rchips pk-rchips ld-rchips">${ruleChipsHtml(state, { host })}${levelChip}</div>${buttonsHtml(buttons, chipsRow)}`;
  return { banner: [GOLD, view.session?.role === "spectator" ? "WATCHING" : "IN THE LOBBY"], main, header: logoBar(state, view) };
}

function inviteScreen(state, view) {
  const code = state?.code || "";
  const link = view.inviteLink || "";
  const qr = view.qrUrl
    ? `<img class="qr" src="${e(view.qrUrl)}" width="150" height="150" alt="QR code for the invite link to table ${e(code)}">`
    : '<div class="qr qr--placeholder" role="img" aria-label="QR code unavailable"><span>QR</span></div>';
  const main = `<section class="paper"><div class="paper__head">TABLE ${e(code)}</div><div class="paper__body paper__body--center">${qr}
    <div class="link-box"><span>${e(link.replace(/^https?:\/\//, ""))}</span></div>${view.linkIsLan ? '<div class="note note--ink note--small">Same Wi-Fi only</div>' : ""}</div></section>`;
  const buttons = [btn("SHARE LINK", "", "primary", { cmd: "share" }), btn("WHATSAPP", "", "ghost", { cmd: "whatsapp" }), btn(view.copied ? "COPIED" : "COPY LINK", "", "ghost", { cmd: "copy" }), btn("BACK", "", "ghost", { cmd: "close-invite" })];
  return { banner: null, main: `${main}${buttonsHtml(buttons)}`, header: logoBar(state, view) };
}

// ---------------------------------------------------------------------------------------------
// The board

function topBar(state) {
  const player = turnPlayer(state);
  const color = player ? playerColor(state, player) : "#F2C14E";
  const turn = state.turnNumber && state.status === "playing" ? `<span class="pkbar__hand">TURN <b>${state.turnNumber}</b></span>` : "";
  const who = player ? `<span class="ldp__who" style="--pc:${color};--pc-ink:${inkOn(color)}">${playerTokenSvg(state, player, { size: 22 })}<b>${e(player.id === state.viewerId ? "YOU" : upper(player.name))}</b></span>` : "";
  return `<div class="pkbar ldp-bar">${turn}${who}<button type="button" class="pkbar__more" data-cmd="ld-more" aria-label="More: settings, how to play, leave">${icon("gear", { size: 16 })}<span>MORE</span></button></div>`;
}

// One chip per player: token, pieces home, and the die beside whoever is rolling.
function strip(state) {
  return `<div class="ldp-strip">${(state.players || []).map((player) => {
    const turn = state.turnId === player.id && state.status === "playing";
    const color = playerColor(state, player);
    const word = player.status === "left" ? "LEFT" : player.place && player.status === "finished" ? upper(ordinal(player.place)) : player.autopilot ? "AWAY" : "";
    return `<span class="ldp-chip${turn ? " is-turn" : ""}${player.status === "left" ? " is-left" : ""}" data-key="ldc-${e(player.id)}" style="--pc:${color}">${playerTokenSvg(state, player, { size: 22 })}<span class="ldp-chip__name">${e(player.id === state.viewerId ? "YOU" : upper(player.name))}</span>${homePips(state, player)}${word ? `<small>${e(word)}</small>` : ""}</span>`;
  }).join("")}</div>`;
}

// What a move does, in two words, for its big button.
export function moveLabel(move) {
  if (move.capture) return { label: "CAPTURE", sub: move.capture > 1 ? `${move.capture} PIECES` : "SEND HOME" };
  if (move.home) return { label: "HOME", sub: "ALL THE WAY" };
  if (move.out) return { label: "OUT", sub: "ONTO START" };
  if (move.to?.zone === "column") return { label: "UP THE COLUMN", sub: move.safe ? "SAFE" : "" };
  return { label: `+${move.path?.length || 0}`, sub: move.safe ? "TO A SAFE STAR" : "" };
}

function actionArea(state, view) {
  const me = mePlayer(state);
  const you = state.you || {};
  const line = e(turnLine(state, { viewerId: state.viewerId }));
  if (!me) return `<div class="ldp-act ldp-act--idle"><span class="ldp-act__line">WATCHING · ${line}</span><button type="button" class="pkbtn pkbtn--ghost pkbtn--small" data-cmd="leave-ask">LEAVE</button></div>`;
  if (state.status === "finished") return `<div class="ldp-act ldp-act--idle"><span class="ldp-act__line">GAME OVER</span><button type="button" class="pkbtn pkbtn--gold pkbtn--small" data-cmd="ld-see-result">FINAL PLACES</button></div>`;
  if (me.status === "finished") return `<div class="ldp-act ldp-act--idle"><span class="ldp-act__line">YOU FINISHED ${e(upper(ordinal(me.place)))} · ${line}</span></div>`;
  const color = playerColor(state, me);
  if (you.toRoll) {
    const again = state.diceBy === me.id && state.rollSeq > 0;
    return `<div class="ldp-act ldp-act--roll" data-key="act-roll" style="--pc:${color};--pc-ink:${inkOn(color)}"><button type="button" class="ldp-roll" data-ld="roll" ${view.busy ? "disabled" : ""}><span class="ldp-roll__die" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i></span><span class="ldp-roll__label">${again ? "ROLL AGAIN" : "ROLL"}</span></button></div>`;
  }
  if (you.toMove) {
    const moves = distinctMoves(state.moves);
    if (forcedMove(state)) {
      return `<div class="ldp-act ldp-act--idle" data-key="act-auto"><span class="ldp-act__line">YOU ROLLED ${state.dice === 6 ? "A 6" : `A ${state.dice}`} · MOVING…</span></div>`;
    }
    const buttons = moves.map((move) => {
      const what = moveLabel(move);
      return `<button type="button" class="ldp-move${view.ld?.focus === move.piece ? " is-focus" : ""}" data-ld="move" data-piece="${move.piece}" style="--pc:${color};--pc-ink:${inkOn(color)}"><span class="ldp-move__n">${move.piece + 1}</span><span class="ldp-move__label">${e(what.label)}</span>${what.sub ? `<span class="ldp-move__sub">${e(what.sub)}</span>` : ""}</button>`;
    }).join("");
    return `<div class="ldp-act ldp-act--move" data-key="act-move"><span class="ldp-act__hint">YOU ROLLED ${state.dice === 6 ? "A 6" : `A ${state.dice}`} · TAP A GLOWING PIECE</span><div class="ldp-act__row ldp-act__row--${Math.min(4, moves.length)}">${buttons}</div></div>`;
  }
  const news = lastNews(state);
  return `<div class="ldp-act ldp-act--idle" data-key="act-wait"><span class="ldp-act__line">${line}</span>${news ? `<span class="ldp-act__news">${e(news)}</span>` : ""}</div>`;
}

function tableScreen(state, view) {
  const arms = Number(state.board?.arms) === 6 ? 6 : 4;
  const board = boardSvg(state, { viewerId: state.viewerId ?? null, focus: view.ld?.focus ?? null, label: `Ludo board: ${turnLine(state, { viewerId: state.viewerId })}` });
  const main = `<div class="ldp" data-arms="${arms}">
    <div class="ldp__board ld-board" data-arms="${arms}">${board}${centerDieHtml(state)}</div>
    ${strip(state)}
    ${actionArea(state, view)}
  </div>`;
  return { banner: null, main, header: topBar(state) };
}

// ---------------------------------------------------------------------------------------------
// Final places

function resultScreen(state, view) {
  const me = mePlayer(state);
  const winner = playerOf(state, state.winnerId);
  const won = Boolean(winner && me && winner.id === me.id);
  const title = won ? "EVERY PIECE HOME FIRST" : winner ? `${e(upper(winner.name))} WINS` : "GAME OVER";
  const main = `<div class="medal-wrap" data-medal><div class="medal${won ? "" : " medal--silver"}"><span class="medal__eyebrow">${won ? "WINNER" : "FINISHED"}</span><span class="medal__place">${e(upper(me?.place ? ordinal(me.place) : ""))}</span></div></div>
    <div class="big-title">${title}</div>
    <div class="paper lfinal lfinal--phone">${standingsHtml(state, { viewerId: state.viewerId, size: 22 })}</div>`;
  const host = Boolean(me) && state.hostId === me.id;
  const buttons = host ? [btn("PLAY AGAIN", "", "primary", { cmd: "rematch" })] : [btn("WAITING FOR A REMATCH", "the host starts it", "ghost", { disabled: true })];
  buttons.push(btn("LEAVE TABLE", "", "ghost", { cmd: "leave" }));
  return { banner: null, main: `${main}${buttonsHtml(buttons, [{ label: "SEE THE BOARD", cmd: "ld-see-board" }])}`, header: logoBar(state, view) };
}

// ---------------------------------------------------------------------------------------------
// Entry

export function renderLudo(state, view = {}) {
  const screen = ludoRoute(state, view);
  const built = screen === "ld-lobby" ? lobbyScreen(state, view)
    : screen === "ld-invite" ? inviteScreen(state, view)
      : screen === "ld-result" ? resultScreen(state, view)
        : tableScreen(state, view);
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
  const items = [sheetBtn("INVITE PEOPLE", 'data-sheet-cmd="ld-invite"', "primary")];
  items.push(sheetBtn("SETTINGS", 'data-sheet-cmd="ld-settings"', "ghost", { html: `${icon("gear", { size: 16 })} SETTINGS` }));
  items.push(sheetBtn("HOW TO PLAY", 'data-sheet-cmd="ld-help"', "ghost"));
  if (me) items.push(sheetBtn("LEAVE TABLE", 'data-sheet-cmd="ld-leave"', "ghost"));
  if (host && state.status === "playing") items.push(sheetBtn("END GAME FOR EVERYONE", 'data-sheet-cmd="ld-end"', "danger"));
  items.push(sheetBtn("CLOSE", 'data-sheet-cmd="close"', "plain"));
  return `<div class="sheet__body"><h2 class="sheet__title">MORE</h2>
    <div class="tableline"><span>TABLE</span><strong>${e(state.code || view.session?.code || "")}</strong>${gtypeTag("ludo")}</div>
    <div class="rchips pk-rchips ld-rchips">${ruleChipsHtml(state)}</div>
    <div class="actions">${items.join("")}</div></div>`;
}

export function helpSheetHtml(state, { back = true } = {}) {
  return `<div class="sheet__body ld-help"><h2 class="sheet__title">HOW TO PLAY LUDO</h2>${howToPlayHtml(state)}
    <p class="sheet__text">On your turn tap <b>ROLL</b>, then tap one of your glowing pieces (or its big button). With only one move, it moves by itself.</p>
    <div class="actions">${back ? sheetBtn("BACK", 'data-sheet-cmd="ld-more"', "ghost") : ""}${sheetBtn("CLOSE", 'data-sheet-cmd="close"', "plain")}</div></div>`;
}
