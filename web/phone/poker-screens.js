// Poker screens for the phone (/play): lobby, the table, final standings and the poker sheets.
// Pure: state + view in, HTML strings out (same slots as screens.js renderPhone: header, banner,
// main, nav). phone.js picks these when state.game === "poker".
//
// The game screen is one column that fits a 390 × 844 phone without scrolling:
//   top bar   blinds pill (with the time until they go up), hand number, MORE
//   table     the whole oval with every seat; you at the bottom with your two cards big
//   me bar    your hand name and a 5-step strength meter · SIT OUT NEXT HAND
//   actions   your turn: FOLD · CHECK/CALL · BET/RAISE; otherwise the pre-action toggles, SHOW
//             CARDS, I'M BACK, REBUY… (fixed height: the table never resizes under you)
// The raise panel and the out-of-chips card float over the bottom of the table.

import { aiLevelLabel, escapeHtml, ordinal, playerById, upper } from "../shared/format.js";
import { playerTokenSvg } from "../shared/tokens.js";
import {
  POKER_ICONS, blindsPillHtml, cardHtml, chips, gamePickHtml, gtypeTag, handRanksHtml, howStepsHtml, tableHtml, tableModel,
} from "../shared/poker-view.js";
import {
  STRENGTH_WORDS, aiLevelsOf, fourColour, mePlayer, preActionOptions, raiseModel, ruleChips, strengthSteps, toCallOf, turnButtons,
} from "./poker-play.js";
import { icon } from "./icons.js";
import { btn, buttonsHtml, tipHtml } from "./parts.js";

const e = escapeHtml;
const GOLD = ["#F2C14E", "#15191E"];

export function isPoker(state) {
  return state?.game === "poker";
}

// Which poker screen: pk-lobby, pk-invite, pk-table or pk-result.
export function pokerRoute(state, view = {}) {
  if (!state) return "pk-table";
  if (view.inviteOpen) return "pk-invite";
  if (state.status === "lobby") return "pk-lobby";
  if (state.status === "finished" && !view.pk?.seeTable) return "pk-result";
  return "pk-table";
}

// ---------------------------------------------------------------------------------------------
// Lobby

function logoBar(state, view) {
  const watch = view.session?.role === "spectator";
  return `<div class="bar bar--logo"><span class="bar__logo">ESTATE</span>${gtypeTag("poker")}${state?.code ? `<span class="bar__table">${watch ? "Watching" : "Table"} <strong>${e(state.code)}</strong></span>` : ""}<button type="button" class="bar__gear" data-cmd="settings" aria-label="Settings">${icon("gear", { size: 20 })}</button></div>`;
}

function lobbyScreen(state, view) {
  const me = mePlayer(state);
  const host = Boolean(me) && (state.hostId === me.id || state.viewerIsHost);
  const seats = state.players.filter((player) => player.status !== "left");
  const list = seats.map((player) => {
    const ai = player.kind === "ai" || player.input === "ai";
    const level = ai && player.difficulty ? `<span class="seat__level seat__level--${e(player.difficulty)}">${e(upper(aiLevelLabel(player.difficulty)))}</span>` : "";
    const away = !ai && player.online === false ? '<span class="lseat__tag">AWAY</span>' : "";
    return `<li class="lseat${player.id === me?.id ? " is-me" : ""}" data-key="ls-${e(player.id)}">
      ${playerTokenSvg(state, player, { size: 32 })}
      <span class="lseat__who"><span class="lseat__name">${e(player.id === me?.id ? "YOU" : upper(player.name))}</span>${level}</span>${away}
      ${host && player.id !== me?.id ? `<button type="button" class="lseat__remove" data-cmd="remove" data-arg="${e(player.id)}" aria-label="Remove ${e(player.name)}">×</button>` : ""}
    </li>`;
  }).join("");
  const rules = ruleChips(state).map((chip) => {
    const inner = `<span>${e(chip.short)}</span><b>${e(chip.value)}</b>`;
    if (!host) return `<span class="rchip" aria-label="${e(chip.short)}: ${e(chip.value)}">${inner}</span>`;
    return `<button type="button" class="rchip" data-cmd="pk-rule" data-arg="${e(chip.key)}" aria-label="${e(chip.short)}: ${e(chip.value)}. Tap to change">${inner}</button>`;
  });
  const levels = aiLevelsOf(state);
  const level = view.pk?.aiLevel && levels.includes(view.pk.aiLevel) ? view.pk.aiLevel : levels[levels.length - 1] || null;
  if (host && levels.length > 1) rules.push(`<button type="button" class="rchip" data-cmd="pk-ai-level" aria-label="New computer players: ${e(aiLevelLabel(level))}. Tap to change"><span>COMPUTER</span><b>${e(upper(aiLevelLabel(level)))}</b></button>`);
  const max = Math.min(state.seatLimit || 8, state.settings?.maxPlayers || 8);
  const ready = seats.length;
  const hostPlayer = playerById(state, state.hostId);
  const buttons = host
    ? [
      btn(ready >= 2 ? `START · ${ready} PLAYERS` : "START", ready >= 2 ? "" : "needs 2 players", "primary", { act: { type: "start" }, disabled: ready < 2 }),
      btn("+ COMPUTER", ready >= max ? "table is full" : "", "ghost", { cmd: "pk-add-ai", disabled: ready >= max }),
    ]
    : [btn(hostPlayer ? `WAITING FOR ${upper(hostPlayer.name)}` : "WAITING FOR THE HOST", "", "ghost", { disabled: true })];
  const codeCard = `<section class="codecard" aria-label="Table code">
      <div class="codecard__code"><span>CODE</span><strong>${e(state.code)}</strong></div>
      <div class="codecard__btns"><button type="button" class="small-btn is-gold" data-cmd="share">INVITE</button><button type="button" class="small-btn" data-cmd="copy">${view.copied ? "COPIED" : "COPY LINK"}</button><button type="button" class="small-btn is-wa" data-cmd="whatsapp">WHATSAPP</button></div>
      ${view.tableOnline === false && host ? `<p class="codecard__pc">${icon("screen", { size: 13 })}On a PC: open it and pick this game</p>` : ""}
    </section>`;
  const picker = host ? gamePickHtml("poker", { cmd: "pk-switch-game", subtitles: false }) : `<div class="pk-gamechosen">${gtypeTag("poker")}<span>Texas Hold'em · up to ${max} players</span></div>`;
  const chipsRow = [{ html: `${icon("deal", { size: 14 })} FRIENDS`, cmd: "open-invite" }, { html: "LEAVE", cmd: "leave-ask" }];
  const main = `${codeCard}${picker}<ul class="lseats">${list}</ul><div class="rchips pk-rchips">${rules.join("")}</div>${buttonsHtml(buttons, chipsRow)}`;
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
// The table

function topBar(state, view) {
  const hand = state.handNumber ? `<span class="pkbar__hand">HAND <b>${state.handNumber}</b></span>` : "";
  return `<div class="pkbar">${blindsPillHtml(state, { now: view.now, compact: true })}${hand}<button type="button" class="pkbar__more" data-cmd="pk-more" aria-label="More: settings, how to play, leave">${icon("gear", { size: 16 })}<span>MORE</span></button></div>`;
}

function meterHtml(steps) {
  const bars = [1, 2, 3, 4, 5].map((n) => `<i class="${n <= steps ? "is-on" : ""}"></i>`).join("");
  return `<span class="pkme__meter pkme__meter--${steps}" role="img" aria-label="Hand strength ${steps} of 5: ${STRENGTH_WORDS[steps].toLowerCase()}">${bars}</span>`;
}

function meBar(state, view) {
  const me = mePlayer(state);
  const prefs = view.pkPrefs || {};
  if (!me) return '<div class="pkme pkme--empty"></div>';
  const inHand = Boolean(state.you?.cards?.length) && ["active", "allIn"].includes(me.status) && state.status === "playing";
  const name = prefs.handNames !== false && inHand && state.you?.handName ? `<span class="pkme__hand">${e(upper(state.you.handName))}</span>` : "";
  const steps = prefs.strength !== false && inHand ? strengthSteps(state.you?.strength) : null;
  const meter = steps ? meterHtml(steps) : "";
  const seated = state.status === "playing" && !["out", "busted", "left"].includes(me.status);
  const sitNext = Boolean(state.you?.sitOutNext || me.sitOutNext);
  const sitting = me.status === "sittingOut" || state.you?.sittingOut;
  const toggle = seated && !sitting
    ? `<button type="button" class="pkme__sit${sitNext ? " is-on" : ""}" data-cmd="pk-sit-next" aria-pressed="${sitNext}"><span class="pkme__box" aria-hidden="true">${sitNext ? POKER_ICONS.tick : ""}</span><span>SIT OUT NEXT HAND</span></button>`
    : "";
  return `<div class="pkme">${name}${meter}${toggle}</div>`;
}

function actorName(state, viewerId) {
  const actor = state.players.find((player) => player.id === state.actorId);
  if (!actor) return "";
  return actor.id === viewerId ? "YOUR TURN" : `${upper(actor.name)}'S TURN`;
}

function waitingLine(state, view) {
  const me = mePlayer(state);
  if (state.phase === "paused") return "WAITING FOR PLAYERS";
  if (state.phase === "runout") return "ALL IN · CARDS COMING";
  if (state.phase === "showdown" || state.phase === "handOver") return "NEXT HAND SOON";
  if (me?.status === "waiting") return "YOU'RE IN FROM THE NEXT HAND";
  if (me?.status === "folded") return state.actorId ? `FOLDED · ${actorName(state, me.id)}` : "FOLDED";
  if (me?.status === "allIn") return `ALL IN · ${actorName(state, me.id) || "WAITING"}`;
  return actorName(state, view.session?.playerId || state.viewerId) || "WAITING";
}

// The waiting line as HTML: with a live "NEXT HAND IN 5" between hands (poker-app ticks it).
function lineHtml(state, view) {
  if ((state.phase === "showdown" || state.phase === "handOver") && state.nextHandAt > (view.now || 0)) {
    const left = Math.max(0, Math.ceil((state.nextHandAt - (view.now || Date.now())) / 1000));
    return `NEXT HAND IN <span data-next-clock>${left}</span>`;
  }
  return e(waitingLine(state, view));
}

// The fixed-height action area at the bottom.
function actionArea(state, view) {
  const me = mePlayer(state);
  const you = state.you || {};
  const pk = view.pk || {};
  const timerBar = '<span class="pkact__timer" aria-hidden="true"><i></i></span>';
  if (!me) {
    return `<div class="pkact pkact--idle"><span class="pkact__line">WATCHING · ${lineHtml(state, view)}</span><button type="button" class="pkbtn pkbtn--ghost pkbtn--small" data-cmd="leave-ask">LEAVE</button></div>`;
  }
  if (state.status === "finished") {
    return `<div class="pkact pkact--idle"><span class="pkact__line">GAME OVER</span><button type="button" class="pkbtn pkbtn--gold pkbtn--small" data-cmd="pk-see-result">FINAL STANDINGS</button></div>`;
  }
  if (you.toAct && you.legal) {
    const buttons = turnButtons(state);
    const raise = pk.raiseOpen ? raiseModel(state, pk.raiseTo) : null;
    const html = buttons.map((button) => {
      if (button.kind === "fold") return `<button type="button" class="pkbtn pkbtn--fold" data-pk="fold"${button.disabled ? " disabled" : ""}><span class="pkbtn__label">FOLD</span></button>`;
      if (button.kind === "check") return '<button type="button" class="pkbtn pkbtn--call" data-pk="check"><span class="pkbtn__label">CHECK</span></button>';
      if (button.kind === "call") return `<button type="button" class="pkbtn pkbtn--call${button.allIn ? " is-allin" : ""}" data-pk="${button.allIn ? "callallin" : "call"}"><span class="pkbtn__label">${button.label}</span><span class="pkbtn__sub">${chips(button.amount)}</span></button>`;
      if (button.kind === "allin") return `<button type="button" class="pkbtn pkbtn--raise is-allin" data-pk="allin"><span class="pkbtn__label">ALL IN</span><span class="pkbtn__sub">${chips(button.amount)}</span></button>`;
      if (raise) return `<button type="button" class="pkbtn pkbtn--raise is-open${raise.allIn ? " is-allin" : ""}" data-pk="raise-send"><span class="pkbtn__label">${raise.allIn ? "ALL IN" : raise.isBet ? "BET" : "RAISE TO"}</span><span class="pkbtn__sub" data-raise-sub>${chips(raise.to)}</span></button>`;
      return `<button type="button" class="pkbtn pkbtn--raise" data-pk="raise-open"><span class="pkbtn__label">${button.label}</span><span class="pkbtn__sub">${e(state.you.legal.raise.isBet ? "" : "")}${chips(state.you.legal.raise.min)}+</span></button>`;
    }).join("");
    return `<div class="pkact pkact--turn" data-key="act-turn">${timerBar}<div class="pkact__row pkact__row--${buttons.length}">${html}</div></div>`;
  }
  if (you.canRebuy || me.status === "busted" || me.status === "out") {
    const left = Math.max(0, (state.settings?.rebuys || 0) - (me.rebuys || 0));
    const cash = state.settings?.style === "cash";
    if (you.canRebuy) {
      // Tournament: a 30 s offer and a number of rebuys; cash: any time, no limit.
      const clock = you.rebuyUntil ? ` · <span data-rebuy-clock>${e(rebuyClock(you.rebuyUntil, view.now))}</span>` : "";
      return `<div class="pkact pkact--bust" data-key="act-bust"><div class="pkact__row pkact__row--3">
        <button type="button" class="pkbtn pkbtn--gold" data-pk="rebuy"><span class="pkbtn__label">REBUY</span><span class="pkbtn__sub">${chips(state.settings?.startingStack)}${clock}</span></button>
        <button type="button" class="pkbtn pkbtn--ghost" data-pk="decline"><span class="pkbtn__label">WATCH</span>${cash ? "" : `<span class="pkbtn__sub">${left} LEFT</span>`}</button>
        <button type="button" class="pkbtn pkbtn--ghost" data-cmd="leave-ask"><span class="pkbtn__label">LEAVE</span></button></div></div>`;
    }
    return `<div class="pkact pkact--idle" data-key="act-out"><span class="pkact__line">${me.place ? `YOU FINISHED ${e(ordinal(me.place).toUpperCase())}` : "OUT OF CHIPS"} · WATCHING</span><button type="button" class="pkbtn pkbtn--ghost pkbtn--small" data-cmd="leave-ask">LEAVE</button></div>`;
  }
  if (me.status === "sittingOut" || you.sittingOut) {
    return `<div class="pkact pkact--idle" data-key="act-sit"><span class="pkact__line">YOU'RE SITTING OUT</span><button type="button" class="pkbtn pkbtn--gold pkbtn--small" data-pk="sitin">I'M BACK</button></div>`;
  }
  const options = preActionOptions(state);
  if (options.length) {
    const picked = pk.pre?.kind || "";
    const html = options.map((option) => {
      const on = picked === option.kind;
      return `<button type="button" class="pkpre${on ? " is-on" : ""}" data-pre="${option.kind}" aria-pressed="${on}"><span class="pkpre__box" aria-hidden="true">${on ? POKER_ICONS.tick : ""}</span><span class="pkpre__label">${e(option.label)}</span></button>`;
    }).join("");
    return `<div class="pkact pkact--pre" data-key="act-pre"><span class="pkact__hint">${lineHtml(state, view)}</span><div class="pkact__row pkact__row--3">${html}</div></div>`;
  }
  if (you.canShow) {
    return `<div class="pkact pkact--idle" data-key="act-show"><span class="pkact__line">${lineHtml(state, view)}</span><button type="button" class="pkbtn pkbtn--gold pkbtn--small" data-pk="show">SHOW CARDS</button></div>`;
  }
  return `<div class="pkact pkact--idle" data-key="act-idle"><span class="pkact__line">${lineHtml(state, view)}</span></div>`;
}

export function rebuyClock(until, now) {
  const left = Math.max(0, Math.ceil(((until || 0) - (now || Date.now())) / 1000));
  return `0:${String(Math.min(59, left)).padStart(2, "0")}`;
}

// The raise panel (over the bottom of the table) while it is open.
function raisePanel(state, view) {
  const pk = view.pk || {};
  if (!pk.raiseOpen || !state.you?.toAct) return "";
  const model = raiseModel(state, pk.raiseTo);
  if (!model) return "";
  const presets = model.presets.map((preset) => `<button type="button" class="pkraise__preset${preset.on ? " is-on" : ""}${preset.allIn ? " is-allin" : ""}" data-pk="preset" data-to="${preset.to ?? ""}"${preset.to === null ? " disabled" : ""}>${e(preset.label)}<small>${preset.to === null ? "—" : chips(preset.to)}</small></button>`).join("");
  return `<div class="pkraise" data-key="raise">
    <div class="pkraise__top">
      <button type="button" class="pkraise__step" data-pk="step" data-dir="-1" aria-label="Less"${model.to <= model.min ? " disabled" : ""}>−</button>
      <span class="pkraise__amt"><span class="pkraise__verb">${e(model.verb)}</span><b data-raise-amt>${chips(model.to)}</b></span>
      <button type="button" class="pkraise__step" data-pk="step" data-dir="1" aria-label="More"${model.to >= model.max ? " disabled" : ""}>+</button>
      <button type="button" class="pkraise__close" data-pk="raise-close" aria-label="Close">×</button>
    </div>
    <input type="range" class="pkraise__slider" data-pk-slider min="${model.min}" max="${model.max}" step="1" value="${model.to}" aria-label="Amount">
    <div class="pkraise__presets">${presets}</div>
  </div>`;
}

// Out of chips: the card over the table (rebuy offer or your place).
function bustCard(state, view) {
  const me = mePlayer(state);
  if (!me || view.pk?.bustSeen === `${state.code}|${me.status}|${me.rebuys || 0}`) return "";
  if (!state.you?.canRebuy && me.status !== "out") return "";
  if (state.status !== "playing") return "";
  const left = Math.max(0, (state.settings?.rebuys || 0) - (me.rebuys || 0));
  const cash = state.settings?.style === "cash";
  const title = state.you?.canRebuy ? "OUT OF CHIPS" : "YOU'RE OUT";
  const sub = state.you?.canRebuy
    ? cash ? `Buy back in for ${chips(state.settings?.startingStack)} whenever you like` : `Rebuy for ${chips(state.settings?.startingStack)} · ${left} ${left === 1 ? "rebuy" : "rebuys"} left`
    : me.place ? `You finished ${ordinal(me.place)}` : "";
  return `<div class="pkcard" data-key="bust"><span class="pkcard__title">${title}</span>${sub ? `<span class="pkcard__sub">${e(sub)}</span>` : ""}${state.you?.canRebuy ? "" : '<button type="button" class="pkcard__close" data-pk="bust-ok">OK</button>'}</div>`;
}

function tableScreen(state, view) {
  const prefs = view.pkPrefs || {};
  const four = fourColour(prefs, state);
  const model = tableModel(state, { viewerId: state.viewerId ?? null, handNames: prefs.handNames !== false, now: view.now });
  const table = tableHtml(model, { fourColor: four, extra: "" });
  const overlay = `${raisePanel(state, view)}${bustCard(state, view)}`;
  const raising = Boolean(view.pk?.raiseOpen && state.you?.toAct);
  const main = `<div class="pkp${four ? " pk-four" : ""}${raising ? " is-raising" : ""}" data-hand="${state.handNumber || 0}">
    <div class="pkp__table">${table}<div class="pkp__over">${overlay}</div></div>
    ${meBar(state, view)}
    ${actionArea(state, view)}
  </div>`;
  return { banner: null, main, header: topBar(state, view) };
}

// ---------------------------------------------------------------------------------------------
// Final standings (Estate's victory screen, with chips)

function resultScreen(state, view) {
  const me = mePlayer(state);
  const standings = Array.isArray(state.standings) && state.standings.length
    ? state.standings
    : [...state.players].sort((a, b) => (a.place || 99) - (b.place || 99) || b.stack - a.stack).map((player, index) => ({ id: player.id, name: player.name, place: player.place || index + 1, stack: player.stack }));
  const mine = standings.find((entry) => entry.id === me?.id);
  const winner = playerById(state, state.winnerId);
  const won = Boolean(winner && me && winner.id === me.id);
  const finals = standings.map((entry) => {
    const player = playerById(state, entry.id) || entry;
    const cash = state.settings?.style === "cash" && Number.isFinite(entry.net);
    const how = cash ? `${entry.net >= 0 ? "+" : "−"}${chips(Math.abs(entry.net))}` : chips(entry.stack);
    return `<li class="final"><span class="final__place">${e(ordinal(entry.place))}</span>${playerTokenSvg(state, player, { size: 22 })}<span class="final__name">${entry.id === me?.id ? "You" : e(entry.name)}</span><span class="final__how"><b>${how}</b></span></li>`;
  }).join("");
  const title = won ? "YOU HAVE EVERY CHIP" : winner ? `${e(upper(winner.name))} WINS` : "GAME OVER";
  const main = `<div class="medal-wrap" data-medal><div class="medal${won ? "" : " medal--silver"}"><span class="medal__eyebrow">${won ? "WINNER" : "FINISHED"}</span><span class="medal__place">${e(upper(mine ? ordinal(mine.place) : ""))}</span></div></div>
    <div class="big-title">${title}</div>
    <ol class="paper finals">${finals}</ol>`;
  const host = Boolean(me) && state.hostId === me.id;
  const buttons = host ? [btn("PLAY AGAIN", "", "primary", { cmd: "rematch" })] : [btn("WAITING FOR A REMATCH", "the host starts it", "ghost", { disabled: true })];
  buttons.push(btn("LEAVE TABLE", "", "ghost", { cmd: "leave" }));
  return { banner: null, main: `${main}${buttonsHtml(buttons, [{ label: "SEE THE TABLE", cmd: "pk-see-table" }])}`, header: logoBar(state, view) };
}

// ---------------------------------------------------------------------------------------------
// Entry

export function renderPoker(state, view = {}) {
  const screen = pokerRoute(state, view);
  const built = screen === "pk-lobby" ? lobbyScreen(state, view)
    : screen === "pk-invite" ? inviteScreen(state, view)
      : screen === "pk-result" ? resultScreen(state, view)
        : tableScreen(state, view);
  const banner = built.banner ? `<div class="banner" data-key="${e(built.banner[1])}" style="background:${built.banner[0][0]};color:${built.banner[0][1]}">${e(built.banner[1])}</div>` : "";
  return { screen, header: built.header, banner, main: built.main, nav: "" };
}

// ---------------------------------------------------------------------------------------------
// Sheets (phone.js renders them in its <dialog>)

function sheetBtn(label, attrs, kind = "ghost", { disabled = false, html = "" } = {}) {
  return `<button type="button" class="btn btn--${kind}" ${attrs}${disabled ? " disabled" : ""}><span class="btn__label">${html || e(label)}</span></button>`;
}

// MORE: the table at a glance, then invite, sit out, settings, how to play, leave, end game.
export function moreSheetHtml(state, view = {}) {
  const me = mePlayer(state);
  const host = Boolean(me) && state.hostId === me.id;
  const playing = state.status === "playing";
  const sitNext = Boolean(state.you?.sitOutNext || me?.sitOutNext);
  const sitting = me?.status === "sittingOut";
  const rules = ruleChips(state).map((chip) => `<span class="rchip"><span>${e(chip.short)}</span><b>${e(chip.value)}</b></span>`).join("");
  const items = [sheetBtn("INVITE PEOPLE", 'data-sheet-cmd="pk-invite"', "primary")];
  if (me && playing && !["out", "busted"].includes(me.status)) {
    items.push(sitting ? sheetBtn("I'M BACK", 'data-sheet-cmd="pk-sitin"', "ghost") : sheetBtn(sitNext ? "PLAY NEXT HAND" : "SIT OUT NEXT HAND", 'data-sheet-cmd="pk-sit-next"', "ghost"));
  }
  items.push(sheetBtn("SETTINGS", 'data-sheet-cmd="pk-settings"', "ghost", { html: `${icon("gear", { size: 16 })} SETTINGS` }));
  items.push(sheetBtn("HOW TO PLAY", 'data-sheet-cmd="pk-help"', "ghost"));
  if (me) items.push(sheetBtn("LEAVE TABLE", 'data-sheet-cmd="pk-leave"', "ghost"));
  if (host && playing) items.push(sheetBtn("END GAME FOR EVERYONE", 'data-sheet-cmd="pk-end"', "danger"));
  items.push(sheetBtn("CLOSE", 'data-sheet-cmd="close"', "plain"));
  return `<div class="sheet__body"><h2 class="sheet__title">MORE</h2>
    <div class="tableline"><span>TABLE</span><strong>${e(state.code || view.session?.code || "")}</strong>${gtypeTag("poker")}</div>
    <div class="rchips pk-rchips">${rules}</div>
    <div class="actions">${items.join("")}</div></div>`;
}

export function helpSheetHtml(state, view = {}) {
  const four = fourColour(view.pkPrefs, state);
  return `<div class="sheet__body pk-help"><h2 class="sheet__title">HOW TO PLAY</h2>${howStepsHtml()}
    <h3 class="sgroup">BEST HANDS FIRST</h3>${handRanksHtml({ yours: state?.you?.cards?.length ? state.you.handName : "", fourColor: four })}
    <div class="actions">${sheetBtn("BACK", 'data-sheet-cmd="pk-more"', "ghost")}${sheetBtn("CLOSE", 'data-sheet-cmd="close"', "plain")}</div></div>`;
}

export function allInSheetHtml(state, amount) {
  const me = mePlayer(state);
  const cards = (state.you?.cards || []).map((code) => cardHtml(code)).join("");
  return `<div class="sheet__body pk-allin"><h2 class="sheet__title">GO ALL IN?</h2>
    <div class="pk-allin__cards${fourColour(null, state) ? " pk-four" : ""}">${cards}</div>
    <div class="allin-big">${chips(amount)}</div>
    <p class="sheet__text sheet__text--center">Everything you have${me ? ` (${chips((me.stack || 0) + (me.bet || 0))})` : ""}. You can't take it back.</p>
    <div class="actions">${sheetBtn(`ALL IN ${chips(amount)}`, 'data-sheet-cmd="pk-allin-yes"', "danger")}${sheetBtn("NOT NOW", 'data-sheet-cmd="close"', "plain")}</div></div>`;
}

// SETTINGS rows for poker on this phone (added to the shared settings sheet).
export function pokerSettingsRows(prefs = {}) {
  const row = (label, key, options, current) => `<div class="srow"><span class="srow__label">${label}</span><div class="sseg" role="group" aria-label="${label}">${options.map(([value, text]) => `<button type="button" class="sseg__btn${String(value) === String(current) ? " is-on" : ""}" data-cmd="pk-pref" data-arg="${key}:${value}" aria-pressed="${String(value) === String(current)}">${text}</button>`).join("")}</div></div>`;
  const onOff = [[true, "ON"], [false, "OFF"]];
  return `<h3 class="sgroup">POKER ON THIS PHONE</h3>
    ${row("CARD COLOURS", "cards", [["table", "TABLE"], ["2", "2"], ["4", "4"]], prefs.cards || "table")}
    ${row("HAND NAMES", "handNames", onOff, prefs.handNames !== false)}
    ${row("HAND STRENGTH", "strength", onOff, prefs.strength !== false)}`;
}

export { toCallOf };
