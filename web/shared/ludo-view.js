// Ludo markup and rules of the screens, shared by the table screen and the phone. Pure: state in,
// strings and plain objects out (checked in Node by web/tests/ludo.check.mjs). The board itself is
// ludo-board.js; animations are ludo-fx.js; styles web/shared/ludo.css. Wire format:
// docs/LUDO-API.md.

import { dieHtml } from "./dice.js";
import { escapeHtml, ordinal, upper } from "./format.js";
import { inkOn, playerColor, playerTokenSvg } from "./tokens.js";

const e = escapeHtml;

export function isLudoState(state) {
  return state?.game === "ludo";
}

export function playerOf(state, id) {
  return (state?.players || []).find((player) => player.id === id) || null;
}

export function turnPlayer(state) {
  return state?.status === "playing" ? playerOf(state, state.turnId) : null;
}

// The moves that do different things: several pieces coming out of the yard are one choice, and so
// are two pieces stacked on one square. The server plays a move by itself when there is only one.
export function distinctMoves(moves) {
  const seen = new Set();
  return (moves || []).filter((move) => {
    const key = move.out ? "out" : move.from;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

// Nothing to choose: the move about to be played for the player (or null).
export function forcedMove(state) {
  if (state?.phase !== "move") return null;
  const moves = distinctMoves(state.moves);
  return moves.length === 1 ? moves[0] : null;
}

// Local seats (keyboard and controller) of the table screen, by player id.
export function localSeatOf(state, playerId) {
  return (state?.localSeats || []).find((seat) => seat.playerId === playerId) || null;
}

// The local seat whose turn it is (the table acts for it), or null.
export function actingSeat(state) {
  return state?.status === "playing" ? localSeatOf(state, state.turnId) : null;
}

// ---------------------------------------------------------------------------------------------
// House rules as chips (the lobby): each press moves one rule to its next value.

const BOARDS = [["auto", "AUTO"], ["4", "4 ARMS"], ["6", "6 ARMS"]];

export function ludoRuleChips(state) {
  const s = { pieces: 4, board: "auto", anyRollOut: false, homeBonus: true, safeSquares: true, threeSixes: true, captureFirst: false, blocks: false, playOn: true, ...(state?.settings || {}) };
  const seated = (state?.players || []).filter((player) => player.status !== "left").length;
  const boardAt = Math.max(0, BOARDS.findIndex(([value]) => value === s.board));
  let nextBoard = BOARDS[(boardAt + 1) % BOARDS.length][0];
  if (nextBoard === "4" && seated > 4) nextBoard = "6";
  const arms = Number(state?.board?.arms) || 4;
  const onOff = (value) => (value ? "ON" : "OFF");
  return [
    { key: "pieces", short: "PIECES", value: String(s.pieces), patch: { pieces: s.pieces === 2 ? 4 : s.pieces - 1 }, custom: s.pieces !== 4 },
    { key: "board", short: "BOARD", value: s.board === "auto" ? `AUTO · ${arms}` : BOARDS[boardAt][1], patch: { board: nextBoard }, custom: s.board !== "auto" },
    { key: "anyRollOut", short: "COME OUT ON", value: s.anyRollOut ? "ANY ROLL" : "A 6", patch: { anyRollOut: !s.anyRollOut }, custom: s.anyRollOut },
    { key: "safeSquares", short: "SAFE STARS", value: onOff(s.safeSquares), patch: { safeSquares: !s.safeSquares }, custom: !s.safeSquares },
    { key: "threeSixes", short: "THREE 6s", value: s.threeSixes ? "LOSE TURN" : "OFF", patch: { threeSixes: !s.threeSixes }, custom: !s.threeSixes },
    { key: "homeBonus", short: "HOME ROLLS AGAIN", value: onOff(s.homeBonus), patch: { homeBonus: !s.homeBonus }, custom: !s.homeBonus },
    { key: "captureFirst", short: "CAPTURE BEFORE HOME", value: onOff(s.captureFirst), patch: { captureFirst: !s.captureFirst }, custom: s.captureFirst },
    { key: "blocks", short: "BLOCKS", value: onOff(s.blocks), patch: { blocks: !s.blocks }, custom: s.blocks },
    { key: "playOn", short: "FINISH", value: s.playOn ? "PLAY ON" : "FIRST WINS", patch: { playOn: !s.playOn }, custom: !s.playOn },
  ];
}

export function ruleChipsHtml(state, { host = false, cmd = "ld-rule" } = {}) {
  return ludoRuleChips(state).map((chip) => {
    const inner = `<span>${e(chip.short)}</span><b>${e(chip.value)}</b>`;
    const cls = `rchip${chip.custom ? " is-custom" : ""}`;
    if (!host) return `<span class="${cls}">${inner}</span>`;
    return `<button type="button" class="${cls}" data-cmd="${e(cmd)}" data-arg="${e(chip.key)}" aria-label="${e(chip.short)}: ${e(chip.value)}. Change">${inner}</button>`;
  }).join("");
}

// ---------------------------------------------------------------------------------------------
// Whose turn, in words

// The line under the board: "MAYA'S TURN · ROLL", "YOUR TURN · PICK A PIECE", "MAYA ROLLED A 6"…
// viewerId: the phone's player (null on the table screen); local: ids played on this screen.
export function turnLine(state, { viewerId = null, local = [] } = {}) {
  if (!state) return "";
  if (state.status === "lobby") return "WAITING TO START";
  if (state.status === "finished") {
    const winner = playerOf(state, state.winnerId);
    return winner ? `${winner.id === viewerId ? "YOU WIN" : `${upper(winner.name)} WINS`}` : "GAME OVER";
  }
  const player = turnPlayer(state);
  if (!player) return "";
  const you = player.id === viewerId;
  const who = you ? "YOUR TURN" : `${upper(player.name)}'S TURN`;
  const here = you || local.includes(player.id);
  if (player.autopilot) return `${who} · THE COMPUTER PLAYS FOR THEM`;
  if (state.phase === "move") {
    if (here) return `${who} · ${forcedMove(state) ? "MOVING…" : "PICK A PIECE"}`;
    return `${upper(player.name)} ROLLED ${state.dice === 6 ? "A 6" : `A ${state.dice}`}`;
  }
  const again = state.diceBy === player.id && state.rollSeq > 0 && (state.lastMove?.playerId === player.id ? state.lastMove.again : state.dice === 6);
  if (here) return `${who} · ${again ? "ROLL AGAIN" : "ROLL"}`;
  return `${who}${again ? " · ROLLS AGAIN" : ""}`;
}

// What just happened, for a short line on the plates and the phone: "CAPTURED ORCA", "HOME!".
export function lastNews(state) {
  const move = state?.lastMove;
  if (!move) return "";
  const who = playerOf(state, move.playerId);
  if (!who) return "";
  if (move.captured?.length) {
    const victim = playerOf(state, move.captured[0].playerId);
    return `${upper(who.name)} CAPTURED ${victim ? upper(victim.name) : "A PIECE"}`;
  }
  if (move.home) return `${upper(who.name)}: A PIECE HOME`;
  if (move.out) return `${upper(who.name)} BROUGHT A PIECE OUT`;
  return "";
}

// ---------------------------------------------------------------------------------------------
// Plates and the die

export function statusWord(state, player) {
  if (player.status === "left") return "LEFT";
  if (player.place && state.status === "finished") return upper(ordinal(player.place));
  if (player.status === "finished") return `${upper(ordinal(player.place))} · HOME`;
  if (player.autopilot) return "AWAY · COMPUTER";
  if (player.online === false && player.kind !== "ai") return "AWAY";
  return "";
}

// A row of little discs: one per piece, filled when home.
export function homePips(state, player) {
  const color = playerColor(state, player);
  return `<span class="lplate__pips" aria-label="${player.home} of ${(player.pieces || []).length} pieces home">${(player.pieces || []).map((piece) => `<i class="${piece.zone === "home" ? "is-home" : piece.zone === "yard" ? "is-yard" : ""}" style="--pc:${color}"></i>`).join("")}</span>`;
}

// A player's plate: token, name, pieces home, captures, the die when it's their turn.
export function plateHtml(state, player, { local = null, levelLabel = null, size = 48, dice = true } = {}) {
  const color = playerColor(state, player);
  const turn = state.turnId === player.id && state.status === "playing";
  const word = statusWord(state, player);
  const device = local ? (local.input === "controller" ? "CONTROLLER" : "KEYBOARD") : player.kind === "ai" ? (levelLabel ? upper(levelLabel(player.difficulty || "normal")) : "COMPUTER") : "";
  const die = dice && turn && state.dice && state.diceBy === player.id ? `<span class="lplate__die">${dieHtml(state.dice, { className: "die--ld" })}</span>` : "";
  return `<div class="lplate${turn ? " is-turn" : ""}${player.status === "finished" ? " is-done" : ""}${player.status === "left" ? " is-left" : ""}" data-key="lpl-${e(player.id)}" data-player="${e(player.id)}" style="--pc:${color};--pc-ink:${inkOn(color)}">
    <span class="lplate__token">${playerTokenSvg(state, player, { size, label: `${player.name}'s token` })}</span>
    <span class="lplate__text">
      <span class="lplate__name">${e(upper(player.name))}</span>
      <span class="lplate__meta">${homePips(state, player)}${player.captures ? `<span class="lplate__caps" title="Pieces captured">⚔ ${player.captures}</span>` : ""}${device ? `<span class="lplate__dev">${e(device)}</span>` : ""}</span>
      ${word ? `<span class="lplate__word">${e(word)}</span>` : ""}
    </span>
    ${die}
  </div>`;
}

// The big die in the middle of the board (the last roll, in the roller's colour). data-roll
// changes with every roll so ludo-fx.js knows to tumble it.
export function centerDieHtml(state) {
  if (state?.status !== "playing" || !state.dice) return `<div class="lcenter" data-key="lcenter"></div>`;
  const roller = playerOf(state, state.diceBy);
  const color = roller ? playerColor(state, roller) : "#F2C14E";
  return `<div class="lcenter" data-key="lcenter" style="--pc:${color}"><div class="lcenter__die" data-roll="${state.rollSeq}">${dieHtml(state.dice, { className: "die--ldbig" })}</div>${state.sixes >= 2 && state.dice === 6 && state.turnId === state.diceBy ? '<span class="lcenter__warn">TWO 6s</span>' : ""}</div>`;
}

// ---------------------------------------------------------------------------------------------
// Standings

export function standingsOf(state) {
  if (Array.isArray(state?.standings) && state.standings.length) return state.standings;
  return [...(state?.players || [])].sort((a, b) => (a.place || 99) - (b.place || 99) || b.progress - a.progress)
    .map((player, index) => ({ id: player.id, name: player.name, place: player.place || index + 1, home: player.home, progress: player.progress, captures: player.captures, left: player.status === "left" }));
}

export function standingsHtml(state, { viewerId = null, size = 36 } = {}) {
  const pieces = state?.settings?.pieces || 4;
  return `<ol class="lfinal__list">${standingsOf(state).map((row) => {
    const player = playerOf(state, row.id) || row;
    const how = row.left ? "LEFT" : row.home >= pieces ? "ALL HOME" : `${row.home}/${pieces} HOME · ${row.progress}%`;
    return `<li class="lfinal__row${row.place === 1 ? " is-win" : ""}${row.id === viewerId ? " is-you" : ""}"><span class="lfinal__place">${e(ordinal(row.place))}</span>${playerTokenSvg(state, player, { size })}<span class="lfinal__name">${e(row.id === viewerId ? "YOU" : upper(row.name))}</span><span class="lfinal__how">${e(how)}${row.captures ? ` · ⚔ ${row.captures}` : ""}</span></li>`;
  }).join("")}</ol>`;
}

// ---------------------------------------------------------------------------------------------
// How to play (the rules in play, in plain words)

export function howToPlayHtml(state) {
  const s = state?.settings || {};
  const items = [
    s.anyRollOut ? "Any roll brings a piece out of your yard onto your start square." : "Roll a <b>6</b> to bring a piece out of your yard onto your start square.",
    "Pieces go once round the board clockwise, then up your coloured column to home in the middle.",
    `A <b>6</b> or a capture gives you another roll${s.homeBonus === false ? "" : ", and so does getting a piece home"}.`,
    s.threeSixes === false ? "There is no limit on sixes in a row." : "Three 6s in a row lose your turn: the third 6 isn't played.",
    "Land on another player's piece to send it back to their yard.",
    s.safeSquares === false ? "No square is safe." : "Start squares and the <b>star</b> squares are safe: nobody is captured there.",
    "You need the exact number to reach home.",
  ];
  if (s.captureFirst) items.push("Capture at least one piece before yours may go up your column; until then they go round again.");
  if (s.blocks) items.push("Two of your pieces on one square make a <b>block</b>: nobody else can pass or land on it.");
  items.push(s.playOn === false ? "The first player with every piece home wins; the rest are ranked by how far they got." : "First home wins; the rest play on for second, third…");
  return `<ol class="lhow">${items.map((text) => `<li>${text}</li>`).join("")}</ol>`;
}

// ---------------------------------------------------------------------------------------------
// Sounds for a new state (both screens): the die, hops, a capture, a piece home, a finish.

export function ludoSounds(prev, next, { viewerId = null } = {}) {
  const out = [];
  if (!isLudoState(prev) || !isLudoState(next) || prev.code !== next.code) return out;
  if (next.status === "finished" && prev.status !== "finished") return [{ name: "win", opts: {}, gain: 1 }];
  if ((next.rollSeq || 0) > (prev.rollSeq || 0)) out.push({ name: "dice", opts: {}, gain: 0.9 });
  const move = next.lastMove;
  if (move && move.seq !== prev.lastMove?.seq) {
    if (move.captured?.length) out.push({ name: move.captured.some((c) => c.playerId === viewerId) ? "jail" : "gavel", opts: {}, gain: 0.9 });
    if (move.home) out.push({ name: "go", opts: {}, gain: 0.9 });
    else if (move.out) out.push({ name: "land", opts: {}, gain: 0.8 });
  }
  const finished = (next.players || []).find((player) => player.status === "finished" && playerOf(prev, player.id)?.status !== "finished");
  if (finished) out.push({ name: "accept", opts: {}, gain: 1 });
  if (viewerId && next.turnId === viewerId && next.phase === "roll" && (prev.turnId !== viewerId || prev.phase !== "roll")) out.push({ name: "turn", opts: {}, gain: 0.8 });
  return out;
}
