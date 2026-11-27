// Poker on the phone: the pure rules of the controls. No DOM, so web/tests/poker.check.mjs runs it
// in Node. poker-app.js wires it to the page; poker-screens.js draws it.
//
//   pre-actions   CHECK/FOLD · CHECK · CALL $x · CALL ANY, ticked while someone else acts and sent
//                 when your turn comes if still valid (CALL $x clears when the price changes, CHECK
//                 when a bet comes in, everything on a new hand)
//   raise panel   amount, slider steps, MIN · ½ POT · POT · ALL IN presets (clampRaise keeps it legal)
//   prefs         this phone's CARD COLOURS (follow the table / 2 / 4), HAND NAMES, HAND STRENGTH
//   sounds        which effect a state change makes on this phone

import { clampRaise } from "../shared/poker.js";
import { chips } from "../shared/poker-view.js";

// ---------------------------------------------------------------------------------------------
// Preferences (saved apart from settings.js, whose shape the Estate tests pin)

export const POKER_PREFS_KEY = "estate.pokerPrefs";
export const DEFAULT_POKER_PREFS = Object.freeze({ cards: "table", handNames: true, strength: true });
const CARD_MODES = ["table", "2", "4"];

export function normalisePokerPrefs(raw) {
  const value = raw && typeof raw === "object" ? raw : {};
  return {
    cards: CARD_MODES.includes(String(value.cards)) ? String(value.cards) : DEFAULT_POKER_PREFS.cards,
    handNames: typeof value.handNames === "boolean" ? value.handNames : DEFAULT_POKER_PREFS.handNames,
    strength: typeof value.strength === "boolean" ? value.strength : DEFAULT_POKER_PREFS.strength,
  };
}

export function loadPokerPrefs(store) {
  try {
    return normalisePokerPrefs(store?.get(POKER_PREFS_KEY));
  } catch {
    return { ...DEFAULT_POKER_PREFS };
  }
}

export function savePokerPrefs(store, prefs) {
  const clean = normalisePokerPrefs(prefs);
  try { store?.set(POKER_PREFS_KEY, clean); } catch { /* private mode: lasts this visit */ }
  return clean;
}

// Four-colour cards on this phone: its own choice, else the table's setting.
export function fourColour(prefs, state) {
  if (prefs?.cards === "4") return true;
  if (prefs?.cards === "2") return false;
  return Boolean(state?.settings?.fourColorDeck);
}

// ---------------------------------------------------------------------------------------------
// Who you are at the table

export function mePlayer(state) {
  return state?.players?.find((player) => player.id === state.viewerId) || null;
}

// Chips you'd put in to call right now (capped at your stack: a short call is all-in).
export function toCallOf(state) {
  const me = mePlayer(state);
  if (!me) return 0;
  const owe = Math.max(0, (state.currentBet || 0) - (me.bet || 0));
  return Math.min(owe, me.stack || 0);
}

// In this hand, still able to act, and someone else is acting: pre-actions apply.
export function canPreAct(state) {
  const me = mePlayer(state);
  if (!me || state?.phase !== "betting" || state.status !== "playing") return false;
  if (state.you?.toAct) return false;
  return me.status === "active" && me.inHand !== false && me.hasCards !== false && (me.stack || 0) > 0;
}

// The pre-action toggles: [CHECK/FOLD] [CHECK | CALL $x] [CALL ANY].
export function preActionOptions(state) {
  if (!canPreAct(state)) return [];
  const owe = toCallOf(state);
  const middle = owe > 0 ? { kind: "call", label: `CALL ${chips(owe)}`, amount: owe } : { kind: "check", label: "CHECK", amount: 0 };
  return [
    { kind: "checkfold", label: "CHECK/FOLD", amount: 0 },
    middle,
    { kind: "callany", label: "CALL ANY", amount: 0 },
  ];
}

// A ticked pre-action, made for this state: { kind, amount, hand }.
export function makePreAction(state, kind) {
  const option = preActionOptions(state).find((candidate) => candidate.kind === kind);
  if (!option) return null;
  return { kind: option.kind, amount: option.amount, hand: state.handNumber || 0 };
}

// Keep a ticked pre-action only while it still means what it said. Returns the pre-action or null.
export function reconcilePreAction(pre, state) {
  if (!pre) return null;
  if (!state || state.status !== "playing" || (state.handNumber || 0) !== pre.hand) return null;
  const me = mePlayer(state);
  if (!me || me.status !== "active") return null;
  if (state.you?.toAct) return pre; // decided by preActionToSend
  if (state.phase !== "betting") return pre.kind === "call" ? null : pre;
  const owe = toCallOf(state);
  if (pre.kind === "check" && owe > 0) return null;
  if (pre.kind === "call" && owe !== pre.amount) return null;
  return pre;
}

// Your turn has come: the action a ticked pre-action sends now, or null (then it is dropped).
export function preActionToSend(pre, state) {
  const legal = state?.you?.toAct ? state.you.legal : null;
  if (!pre || !legal || (state.handNumber || 0) !== pre.hand) return null;
  switch (pre.kind) {
    case "checkfold":
      if (legal.check) return { type: "check" };
      return legal.fold ? { type: "fold" } : null;
    case "check":
      return legal.check ? { type: "check" } : null;
    case "call":
      return !legal.check && legal.call === pre.amount ? { type: "call" } : null;
    case "callany":
      if (legal.check) return { type: "check" };
      return legal.call > 0 ? { type: "call" } : null;
    default:
      return null;
  }
}

// ---------------------------------------------------------------------------------------------
// Your turn: the three buttons and the raise panel

// FOLD · CHECK or CALL $x · BET / RAISE (or ALL IN when a raise is all you can do).
export function turnButtons(state) {
  const legal = state?.you?.toAct ? state.you.legal : null;
  if (!legal) return [];
  const me = mePlayer(state);
  const buttons = [];
  buttons.push({ kind: "fold", label: "FOLD", action: { type: "fold" }, disabled: !legal.fold });
  if (legal.check) buttons.push({ kind: "check", label: "CHECK", action: { type: "check" } });
  else {
    const allIn = me && legal.call >= (me.stack || 0);
    buttons.push({ kind: "call", label: allIn ? "ALL IN" : "CALL", amount: legal.call, action: { type: "call" }, allIn });
  }
  if (legal.raise) {
    const r = legal.raise;
    if (r.min >= r.max) buttons.push({ kind: "allin", label: "ALL IN", amount: r.max, action: { type: "allin" }, allIn: true, confirm: true });
    else buttons.push({ kind: "raise", label: r.isBet ? "BET" : "RAISE", amount: null, open: true });
  }
  return buttons;
}

const PRESET_NAMES = [["Min", "MIN"], ["½ pot", "½ POT"], ["Pot", "POT"], ["All-in", "ALL IN"]];

// The raise panel for a chosen amount `to` (null = the minimum).
export function raiseModel(state, to = null) {
  const r = state?.you?.toAct ? state.you.legal?.raise : null;
  if (!r) return null;
  const value = clampRaise(state, to ?? r.min);
  const step = Math.max(1, state.level?.bigBlind || state.settings?.bigBlind || 1);
  const presets = PRESET_NAMES.map(([source, label]) => {
    let amount = null;
    if (source === "Min") amount = r.min;
    else if (source === "All-in") amount = r.max;
    else amount = r.presets?.find((preset) => preset.label === source)?.to ?? null;
    if (amount !== null) amount = clampRaise(state, amount);
    const off = amount === null || (source !== "All-in" && source !== "Min" && amount >= r.max);
    return { label, to: off ? null : amount, on: !off && amount === value, allIn: source === "All-in" };
  });
  const allIn = value >= r.max;
  return {
    verb: r.isBet ? "BET" : "RAISE TO",
    isBet: Boolean(r.isBet),
    min: r.min,
    max: r.max,
    to: value,
    step,
    allIn,
    presets,
    confirmLabel: allIn ? `ALL IN ${chips(value)}` : `${r.isBet ? "BET" : "RAISE TO"} ${chips(value)}`,
    action: allIn ? { type: "allin" } : { type: r.isBet ? "bet" : "raise", to: value },
  };
}

// The slider's position → an amount: whole small blinds between min and max, both ends exact.
export function sliderAmount(state, raw) {
  const r = state?.you?.legal?.raise;
  if (!r) return null;
  const value = Number(raw);
  if (!(value > r.min)) return r.min;
  if (value >= r.max) return r.max;
  const unit = Math.max(1, state.level?.smallBlind || state.settings?.smallBlind || 1);
  return clampRaise(state, Math.round(value / unit) * unit);
}

// ± one big blind from `to`.
export function stepAmount(state, to, direction) {
  const model = raiseModel(state, to);
  if (!model) return null;
  return clampRaise(state, model.to + direction * model.step);
}

// ---------------------------------------------------------------------------------------------
// Hand strength meter: 5 steps from you.strength (0–1), or null when there is none.

export function strengthSteps(strength) {
  const value = Number(strength);
  if (strength === null || strength === undefined || !Number.isFinite(value)) return null;
  return Math.max(1, Math.min(5, Math.ceil(Math.max(0, Math.min(1, value)) * 5)));
}

export const STRENGTH_WORDS = ["", "WEAK", "FAIR", "GOOD", "STRONG", "MONSTER"];

// ---------------------------------------------------------------------------------------------
// Lobby rule chips (host): each tap moves to the next choice. Options a setting doesn't have yet
// (the engine fields that are still arriving) are hidden.

const BLINDS = [[5, 10], [10, 20], [25, 50], [50, 100], [100, 200]];
const STACKS = [1000, 1500, 2000, 3000, 5000, 10000];
const SPEEDS = [["off", 0, "NEVER"], ["auto", 30, "FAST"], ["auto", 60, "NORMAL"], ["auto", 120, "SLOW"]];
const TIMERS = [0, 10, 20, 30, 60];
const BANKS = [0, 15, 30, 60];
const REBUYS = [0, 1, 2, 3];

export function levelMinutes(targetMinutes) {
  return Math.max(3, Math.min(20, Math.round((Number(targetMinutes) || 60) / 8)));
}

function next(list, at) {
  return list[(at + 1) % list.length];
}

export function ruleChips(state) {
  const s = state?.settings || {};
  const chipsOut = [];
  const cash = s.style === "cash";
  if ("style" in s) chipsOut.push({ key: "style", short: "STYLE", value: cash ? "CASH TABLE" : "TOURNAMENT", patch: { style: cash ? "tournament" : "cash" } });
  const bb = s.bigBlind || 20;
  const stack = s.startingStack || 1500;
  const blinds = BLINDS.filter(([, big]) => big * 10 <= stack);
  const bAt = blinds.findIndex(([, big]) => big === bb);
  const [nsb, nbb] = next(blinds.length ? blinds : BLINDS, bAt);
  chipsOut.push({ key: "blinds", short: "BLINDS", value: `${chips(s.smallBlind || 10)}/${chips(bb)}`, patch: { smallBlind: nsb, bigBlind: nbb } });
  const stacks = STACKS.filter((value) => value >= bb * 10);
  chipsOut.push({ key: "stack", short: cash ? "BUY-IN" : "STACK", value: chips(stack), patch: { startingStack: next(stacks, stacks.indexOf(stack)) } });
  if (!cash) {
    // Cash tables keep their blinds and allow any number of rebuys (POKER-API §2.2).
    const sAt = s.blindSchedule === "off" ? 0 : Math.max(1, SPEEDS.findIndex(([mode, minutes]) => mode === "auto" && minutes === s.targetMinutes));
    const [mode, minutes] = next(SPEEDS, sAt);
    const now = SPEEDS[sAt] || SPEEDS[2];
    const shown = s.blindSchedule === "off" ? "NEVER" : `${levelMinutes(s.targetMinutes)} MIN`;
    chipsOut.push({ key: "speed", short: "BLINDS UP", value: shown, title: now[2], patch: mode === "off" ? { blindSchedule: "off" } : { blindSchedule: "auto", targetMinutes: minutes } });
  }
  const rebuys = s.rebuys || 0;
  if (!cash) chipsOut.push({ key: "rebuys", short: "REBUYS", value: rebuys === 0 ? "OFF" : rebuys === 1 ? "ONCE" : `${rebuys}×`, patch: { rebuys: next(REBUYS, Math.max(0, REBUYS.indexOf(rebuys))) } });
  const timer = s.actionSeconds || 0;
  const tAt = TIMERS.indexOf(timer);
  chipsOut.push({ key: "timer", short: "TURN TIMER", value: timer ? `${timer}s` : "OFF", patch: { actionSeconds: next(TIMERS, tAt < 0 ? 0 : tAt) } });
  if ("timeBankSeconds" in s) {
    const bank = s.timeBankSeconds || 0;
    const kAt = BANKS.indexOf(bank);
    chipsOut.push({ key: "bank", short: "TIME BANK", value: bank ? `${bank}s` : "OFF", patch: { timeBankSeconds: next(BANKS, kAt < 0 ? 0 : kAt) } });
  }
  chipsOut.push({ key: "deck", short: "CARDS", value: s.fourColorDeck ? "4 COLOURS" : "2 COLOURS", patch: { fourColorDeck: !s.fourColorDeck } });
  return chipsOut;
}

// Computer levels the server offers for poker (state.aiLevels); [] when it doesn't say.
export function aiLevelsOf(state) {
  return Array.isArray(state?.aiLevels) ? state.aiLevels.filter((level) => typeof level === "string" && level) : [];
}

export function nextLevel(state, current) {
  const levels = aiLevelsOf(state);
  if (!levels.length) return null;
  const at = levels.indexOf(current);
  return at < 0 ? levels[levels.length - 1] : levels[(at + 1) % levels.length];
}

// ---------------------------------------------------------------------------------------------
// Sounds: what a state change makes this phone play. Yours at full volume, others quieter.

const OTHERS = 0.35;

function newActions(prev, next) {
  if (!prev || !next || prev.code !== next.code || (prev.handNumber || 0) !== (next.handNumber || 0)) return next?.actions || [];
  return (next.actions || []).slice((prev.actions || []).length);
}

export function pokerSounds(prev, next) {
  const out = [];
  if (!prev || !next || next.game !== "poker" || prev.code !== next.code) return out;
  const me = next.viewerId;
  const gain = (id) => (id && id === me ? 1 : OTHERS);
  if ((next.handNumber || 0) > (prev.handNumber || 0) && next.status === "playing") out.push({ name: "dealCard", opts: { count: 2 }, gain: 0.8 });
  const before = (prev.handNumber === next.handNumber ? prev.board?.length : 0) || 0;
  const added = (next.board?.length || 0) - before;
  if (added > 0) out.push({ name: "dealCard", opts: { count: added }, gain: 0.7 });
  for (const action of newActions(prev, next)) {
    if (action.type === "ante" || action.type === "smallBlind" || action.type === "bigBlind") continue;
    if (action.type === "fold") out.push({ name: "fold", opts: {}, gain: gain(action.playerId) });
    else if (action.type === "check") out.push({ name: "knock", opts: {}, gain: gain(action.playerId) });
    else out.push({ name: "chips", opts: { amount: action.amount || 0, allIn: Boolean(action.allIn) }, gain: gain(action.playerId) });
  }
  const ended = !prev.result && next.result && !next.result.cancelled;
  if (ended) {
    const mine = (next.result.pots || []).some((pot) => (pot.winners || []).some((winner) => winner.id === me));
    out.push({ name: "potWin", opts: { mine }, gain: mine ? 1 : 0.5 });
  }
  if (!prev.you?.toAct && next.you?.toAct) out.push({ name: "turn", opts: {}, gain: 1 });
  if (next.status === "finished" && prev.status !== "finished") out.push({ name: "win", opts: {}, gain: next.winnerId === me ? 1 : 0.5 });
  return out;
}

// Seconds left on your own turn (for the tick in the last 5 s), or null when it isn't your turn.
export function myTurnSecondsLeft(state, now) {
  if (!state?.you?.toAct || !state.actionDeadline) return null;
  return Math.max(0, (state.actionDeadline - now) / 1000);
}
