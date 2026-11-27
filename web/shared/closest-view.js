// Near Thing: the shared view helpers (docs/CLOSEST-API.md). Pure: state in, strings and plain
// objects out. The table screen (web/table/closest-table.js), the phone (web/phone/closest-*.js) and
// the looks (web/shared/closest-looks.js) all build on these, so every screen says the same thing.
//
// Nothing here decides a look: number formatting and words, the number pad, the odds, the bet
// (chips, stake), plate tags, the reveal breakdown, phase timing and the sound triggers. The looks
// take the models built at the bottom (padModel, lineModel, betModel, revealModel).

import { escapeHtml, ordinal, upper } from "./format.js";
import { creditsHtml as quizCreditsHtml } from "./quiz-view.js";

const e = escapeHtml;

// The display name, in one place (web/shared/closest-name.js, docs §0). The
// server sends its own as state.title; gameTitle() prefers that so a rename shows at once.
export { GAME_NAME, GAME_TITLE } from "./closest-name.js";
import { GAME_TITLE } from "./closest-name.js";

export function gameTitle(state) {
  const title = String(state?.title || "").trim();
  return title ? upper(title) : GAME_TITLE;
}

export function isClosestState(state) {
  return state?.game === "closest";
}

export function playerOf(state, id) {
  return (state?.players || []).find((player) => player.id === id) || null;
}

// Everyone still at the table (join order).
export function seatedPlayers(state) {
  return (state?.players || []).filter((player) => player.status !== "left");
}

// Playing this question (not waiting in the lobby, not gone).
export function playingPlayers(state) {
  return (state?.players || []).filter((player) => player.status === "playing");
}

const MINUS = "−";

// ---------------------------------------------------------------------------------------------
// Numbers: as the question shows them (docs §3.2 format). plain: 12,500 · year: 1969.

const blank = (value) => value === null || value === undefined || value === "";

export function formatNumber(value, format = "plain") {
  if (blank(value)) return "";
  const number = Math.round(Number(value));
  if (!Number.isFinite(number)) return "";
  const digits = String(Math.abs(number));
  const body = format === "year" ? digits : digits.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return number < 0 ? `${MINUS}${body}` : body;
}

// "3,600 pipes" · "1786" (years have no unit).
export function withUnit(value, question = {}) {
  const text = formatNumber(value, question.format);
  const unit = String(question.unit || "").trim();
  return unit && text ? `${text} ${unit}` : text;
}

// "100 to 60,000 pipes" · "1000 to 2025".
export function rangeText(question = {}) {
  if (!Number.isFinite(Number(question.min)) || !Number.isFinite(Number(question.max))) return "";
  return `${formatNumber(question.min, question.format)} to ${withUnit(question.max, question)}`;
}

// The number in words above the keys (docs §8): 3200000 → "3 million 200 thousand", 12500 →
// "12 thousand 500", 640 → "640". Years read as written (1969 → "1969", never "1,969" or "1
// thousand 969"); so do numbers under 1,000.
const GROUPS = [[1e9, "billion"], [1e6, "million"], [1e3, "thousand"]];

export function numberWords(value, format = "plain") {
  if (blank(value)) return "";
  const number = Math.round(Number(value));
  if (!Number.isFinite(number)) return "";
  if (format === "year") return String(number);
  let rest = Math.abs(number);
  if (rest < 1000) return `${number < 0 ? `${MINUS}` : ""}${rest}`;
  const parts = [];
  for (const [size, word] of GROUPS) {
    const count = Math.floor(rest / size);
    if (count) parts.push(`${count} ${word}`);
    rest -= count * size;
  }
  if (rest) parts.push(String(rest));
  return `${number < 0 ? MINUS : ""}${parts.join(" ")}`;
}

// ---------------------------------------------------------------------------------------------
// The number pad (docs §8: 0–9, ⌫, 000; no system keyboard). The draft is a string of digits.

export const PAD_MAX_DIGITS = 9;
// Top to bottom, left to right: a 3 × 4 grid.
export const PAD_KEYS = Object.freeze(["1", "2", "3", "4", "5", "6", "7", "8", "9", "000", "0", "back"]);

// The draft after pressing `key` ("0"–"9", "000", "back", "clear"). Leading zeros go; "000" on an
// empty draft (or a lone 0) does nothing; a press that would pass PAD_MAX_DIGITS is refused.
export function padPress(digits, key) {
  const draft = String(digits ?? "").replace(/\D/g, "").replace(/^0+(?=\d)/, "");
  if (key === "back") return draft.slice(0, -1);
  if (key === "clear") return "";
  if (!/^(\d|000)$/.test(String(key))) return draft;
  if (key === "000" && (draft === "" || draft === "0")) return draft;
  const next = `${draft}${key}`.replace(/^0+(?=\d)/, "");
  return next.length > PAD_MAX_DIGITS ? draft : next;
}

// The draft as a number (null when empty).
export function padValue(digits) {
  const draft = String(digits ?? "").replace(/\D/g, "");
  return draft ? Number(draft) : null;
}

// May this value be sent? {ok, error} with the server's own wording (docs §2) for a value outside
// min–max; an empty pad is not ok but says nothing.
export function guessCheck(value, question = {}) {
  if (value === null || value === undefined || value === "" || !Number.isFinite(Number(value))) return { ok: false, error: "" };
  const number = Number(value);
  if (!Number.isInteger(number)) return { ok: false, error: "whole numbers only" };
  const min = Number(question.min);
  const max = Number(question.max);
  if ((Number.isFinite(min) && number < min) || (Number.isFinite(max) && number > max)) {
    return { ok: false, error: `guess between ${formatNumber(min, question.format)} and ${formatNumber(max, question.format)}` };
  }
  return { ok: true, error: "" };
}

// Which keys can do anything now ({key: disabled}).
export function padDisabled(digits) {
  const draft = String(digits ?? "");
  const full = draft.length >= PAD_MAX_DIGITS;
  const out = {};
  for (const key of PAD_KEYS) {
    if (key === "back") out[key] = draft.length === 0;
    else if (key === "000") out[key] = draft === "" || draft === "0" || draft.length + 3 > PAD_MAX_DIGITS;
    else out[key] = full;
  }
  return out;
}

export function guessAction(state, value) {
  return { type: "guess", q: Number(state?.question?.seq) || 0, value: Number(value) };
}

// ---------------------------------------------------------------------------------------------
// Odds (docs §3.3): middle slots pay 2×, each step outward one more, up to 5×.

export function oddsFor(index, count) {
  return Math.min(5, 2 + Math.floor(Math.abs(index - (count - 1) / 2)));
}

export function oddsLadder(count) {
  return Array.from({ length: Math.max(0, count) }, (_, index) => oddsFor(index, count));
}

export function oddsLabel(odds) {
  const number = Number(odds);
  return Number.isFinite(number) && number > 0 ? `${number}×` : "";
}

export function slotsOf(state) {
  return Array.isArray(state?.question?.slots) ? state.question.slots : [];
}

// ---------------------------------------------------------------------------------------------
// The bet (docs §2, §8): two free chips on any slots (both may go on one), a stake of 0 to
// you.stakeMax on a slot holding one of your chips. The phone starts with both chips on your own
// guess (the middle slot when you didn't guess), so one tap confirms.
// A bet draft: {key, chips: [a, b], stake, stakeSlot}.

export const CHIPS = 2;

export function betKey(state) {
  return `${state?.code || ""}|${state?.question?.seq ?? 0}`;
}

// The lower middle when the count is even, as the server's Easy computers use (bots.go).
export function middleSlot(count) {
  return Math.max(0, Math.floor((count - 1) / 2));
}

// The viewer's own slot (−1 when they didn't guess or the line isn't out yet).
export function ownSlot(state, viewerId = state?.you?.id ?? state?.viewerId) {
  if (!viewerId) return -1;
  const slot = slotsOf(state).find((candidate) => (candidate.ownerIds || []).includes(viewerId));
  return slot ? slot.index : -1;
}

export function stakeMaxOf(state) {
  return Math.max(0, Math.min(3, Math.floor(Number(state?.you?.stakeMax) || 0)));
}

export function betStart(state) {
  const count = slotsOf(state).length;
  const own = ownSlot(state);
  const slot = own >= 0 ? own : middleSlot(count);
  return { key: betKey(state), chips: [slot, slot], stake: 0, stakeSlot: slot };
}

// The draft for this question's bet: the one being made, else a fresh start. Anything stale or
// out of range (a new question, a lower stakeMax) is put right.
export function betDraft(state, draft) {
  const start = betStart(state);
  if (!draft || draft.key !== start.key) return start;
  const count = slotsOf(state).length;
  const valid = (slot) => Number.isInteger(slot) && slot >= 0 && slot < count;
  const chips = Array.isArray(draft.chips) && draft.chips.length === CHIPS && draft.chips.every(valid) ? [...draft.chips] : start.chips;
  const stake = clampStake(draft.stake, stakeMaxOf(state));
  const stakeSlot = chips.includes(draft.stakeSlot) ? draft.stakeSlot : chips[0];
  return { key: start.key, chips, stake, stakeSlot };
}

function clampStake(value, max) {
  const number = Math.round(Number(value) || 0);
  return Math.max(0, Math.min(Math.max(0, max), number));
}

// Move chip `which` (0 or 1) to `slot`. The stake stays where it was while a chip is still there,
// else it follows the moved chip.
export function placeChip(bet, which, slot) {
  if (!bet || ![0, 1].includes(which) || !Number.isInteger(slot) || slot < 0) return bet;
  const chips = [...bet.chips];
  chips[which] = slot;
  const stakeSlot = chips.includes(bet.stakeSlot) ? bet.stakeSlot : slot;
  return { ...bet, chips, stakeSlot };
}

export function setStake(bet, stake, stakeMax) {
  if (!bet) return bet;
  return { ...bet, stake: clampStake(stake, stakeMax) };
}

export function stepStake(bet, direction, stakeMax) {
  if (!bet) return bet;
  return setStake(bet, (bet.stake || 0) + Math.sign(Number(direction) || 0), stakeMax);
}

// The stake may only go on a slot holding one of your chips.
export function setStakeSlot(bet, slot) {
  if (!bet || !bet.chips.includes(slot)) return bet;
  return { ...bet, stakeSlot: slot };
}

// {ok, error} with the server's wording (docs §2).
export function betCheck(bet, state) {
  const count = slotsOf(state).length;
  const max = stakeMaxOf(state);
  if (!bet || !Array.isArray(bet.chips) || bet.chips.length !== CHIPS || !bet.chips.every((slot) => Number.isInteger(slot) && slot >= 0 && slot < count)) return { ok: false, error: "pick two slots" };
  if (!Number.isInteger(bet.stake) || bet.stake < 0 || bet.stake > max) return { ok: false, error: `stake between 0 and ${max}` };
  if (bet.stake > 0 && !bet.chips.includes(bet.stakeSlot)) return { ok: false, error: "put the stake on one of your chips" };
  return { ok: true, error: "" };
}

// The action for a bet: stakeSlot only goes with a stake (the server ignores it at 0).
export function betAction(state, bet) {
  const action = { type: "bet", q: Number(state?.question?.seq) || 0, chips: [...bet.chips], stake: bet.stake || 0 };
  if (action.stake > 0) action.stakeSlot = bet.stakeSlot;
  return action;
}

// The worst a stake can cost (docs §8: "you could lose 3").
export function riskText(stake) {
  const number = Math.max(0, Math.round(Number(stake) || 0));
  return number ? `you could lose ${number}` : "free chips never lose";
}

// What a winning stake pays (not doubled on the last question, docs §4.1).
export function stakeWin(stake, odds) {
  return Math.max(0, Math.round(Number(stake) || 0)) * Math.max(0, Number(odds) || 0);
}

// ---------------------------------------------------------------------------------------------
// Scores and wording

export function fmtScore(value) {
  const number = Math.round(Number(value) || 0);
  const text = Math.abs(number).toLocaleString("en-GB");
  return number < 0 ? `${MINUS}${text}` : text;
}

export function signedPoints(value) {
  const number = Math.round(Number(value) || 0);
  if (number === 0) return "±0";
  return number > 0 ? `+${fmtScore(number)}` : fmtScore(number);
}

export function isLastQuestion(state) {
  const question = state?.question;
  if (!question) return false;
  return Boolean(question.double) || (Number(state.questionCount) > 0 && Number(question.no) === Number(state.questionCount));
}

// "QUESTION 2 OF 7" · "LAST QUESTION · DOUBLE POINTS" (`short`: always "QUESTION 7 OF 7", for the
// table screen's header, where the question card already says double).
export function questionLabel(state, { short = false } = {}) {
  const no = Number(state?.question?.no ?? state?.questionNo) || 0;
  const count = Number(state?.questionCount) || 0;
  if (!no) return count ? `${count} QUESTIONS` : "";
  if (isLastQuestion(state) && !short) return "LAST QUESTION · DOUBLE POINTS";
  return count ? `QUESTION ${no} OF ${count}` : `QUESTION ${no}`;
}

function nameOf(state, id, viewerId) {
  const player = playerOf(state, id);
  if (!player) return "";
  return viewerId && player.id === viewerId ? "YOU" : upper(player.name);
}

// Winning slots and second-closest slots at the reveal.
export function winningSlots(state) {
  return slotsOf(state).filter((slot) => slot.result === "win");
}

export function secondSlots(state) {
  return slotsOf(state).filter((slot) => slot.result === "second");
}

// The big line for the phase. `viewerId` turns their name into YOU; `short` keeps it to the
// table header's pill (the question card says double).
export function phaseLine(state, { viewerId = null, short = false } = {}) {
  if (!state) return "";
  if (state.status === "finished") return "GAME OVER";
  switch (state.phase) {
    case "lobby":
      return "WAITING TO START";
    case "intro":
      return gameTitle(state);
    case "guess":
      if (isLastQuestion(state)) return short ? "LAST GUESS" : "LAST GUESS · DOUBLE POINTS";
      return "GUESS THE NUMBER";
    case "lineup":
      return "THE GUESSES";
    case "bet":
      return "PLACE YOUR CHIPS";
    case "chips":
      return "CHIPS DOWN";
    case "reveal": {
      const question = state.question || {};
      if (question.nobody) return "NOBODY GUESSED";
      const wins = winningSlots(state);
      if (wins.length > 1) return "A TIE: BOTH WIN";
      const owners = (wins[0]?.ownerIds || []).map((id) => nameOf(state, id, viewerId)).filter(Boolean);
      if (!owners.length) return "THE ANSWER";
      if (owners.length === 1) return owners[0] === "YOU" ? "YOU'RE CLOSEST" : `${owners[0]} IS CLOSEST`;
      return `${owners.join(" AND ")} ARE CLOSEST`;
    }
    default:
      return "";
  }
}

// ---------------------------------------------------------------------------------------------
// Plates (docs §8: GUESSED, BET IN, AWAY): {tag, tone}. LEFT outranks everything; done in this
// phase outranks AWAY (a player who guessed then lost the connection still guessed).

export function plateTag(state, player) {
  if (!player) return { tag: "", tone: "" };
  if (player.status === "left") return { tag: "LEFT", tone: "left" };
  const question = state?.question || {};
  if (state?.phase === "guess" && (player.guessed || (question.guessed || []).includes(player.id))) return { tag: "GUESSED", tone: "locked" };
  if (state?.phase === "bet" && (player.betIn || (question.betsIn || []).includes(player.id))) return { tag: "BET IN", tone: "locked" };
  if (player.online === false) return { tag: "AWAY", tone: "away" };
  return { tag: "", tone: "" };
}

// The score change a plate shows at the reveal ("" when none).
export function plateDelta(state, playerId) {
  if (state?.phase !== "reveal") return "";
  const row = resultOf(state, playerId);
  if (!row || !Number(row.delta)) return "";
  return signedPoints(row.delta);
}

export function resultOf(state, playerId) {
  return (state?.question?.results || []).find((row) => row.playerId === playerId) || null;
}

// ---------------------------------------------------------------------------------------------
// The reveal breakdown for one player: {parts: [{kind, label, points}], total, text}.
// "+1 second closest · +3 chips · +4 stake", "no points", "no guess", "nobody guessed".

export function breakdown(state, playerId) {
  const question = state?.question || {};
  if (question.nobody) return { parts: [], total: 0, text: "nobody guessed" };
  const row = resultOf(state, playerId);
  if (!row) return { parts: [], total: 0, text: "" };
  const parts = [];
  const bonus = Number(row.bonus) || 0;
  if (bonus) {
    const slot = slotsOf(state)[row.slot];
    const word = slot?.result === "second" ? "second closest" : Number(slot?.off) === 0 ? "exact" : "closest";
    parts.push({ kind: "bonus", label: `${signedPoints(bonus)} ${word}`, points: bonus });
  }
  const chips = Number(row.chipPoints) || 0;
  if (chips) parts.push({ kind: "chips", label: `${signedPoints(chips)} chips`, points: chips });
  const stake = Number(row.stakePoints) || 0;
  if (stake) parts.push({ kind: "stake", label: `${signedPoints(stake)} stake`, points: stake });
  const total = Number(row.delta) || parts.reduce((sum, part) => sum + part.points, 0);
  const text = parts.length ? parts.map((part) => part.label).join(" · ") : row.guess === null || row.guess === undefined ? "no guess, no points" : "no points";
  return { parts, total, text };
}

// Where the answer falls along an evenly spaced line of slots, as a slot position: 2.17 is a
// sixth of the way from slot 2 to slot 3; below the first slot −0.5, past the last k − 0.5.
export function answerPosition(slots, answer) {
  const values = (slots || []).map((slot) => Number(slot.value));
  const target = Number(answer);
  if (!values.length || !Number.isFinite(target)) return 0;
  if (target < values[0]) return -0.5;
  if (target > values[values.length - 1]) return values.length - 0.5;
  for (let index = 0; index < values.length; index += 1) {
    if (target === values[index]) return index;
    const next = values[index + 1];
    if (next !== undefined && target > values[index] && target < next) return index + (target - values[index]) / (next - values[index]);
  }
  return values.length - 1;
}

// "off by 1,300" ("exact" at 0).
export function offText(off, format = "plain") {
  const number = Number(off);
  if (!Number.isFinite(number)) return "";
  return number === 0 ? "exact" : `off by ${formatNumber(number, format === "year" ? "plain" : format)}`;
}

// ---------------------------------------------------------------------------------------------
// Phase timing (docs §4). The line-up and the reveal play inside the phase time: the screens
// work out how far in they are from phaseEndsAt, so a late screen joins part-way through.

export const INTRO_MS = 4000;
export const CHIPS_MS = 3500;
export const REVEAL_MS = 8000;
export const REVEAL_LAST_MS = 9000;

export function lineupMs(slotCount) {
  return Math.max(3000, Math.min(6500, 1500 + 600 * Math.max(0, slotCount)));
}

// The fixed length of a timed phase (null for guess and bet, which run on settings and hurry-up).
export function phaseDuration(state) {
  switch (state?.phase) {
    case "intro":
      return INTRO_MS;
    case "lineup":
      return lineupMs(slotsOf(state).length);
    case "chips":
      return CHIPS_MS;
    case "reveal":
      return isLastQuestion(state) ? REVEAL_LAST_MS : REVEAL_MS;
    default:
      return null;
  }
}

// How far into a fixed phase `now` is (ms, 0 to its length); 0 when it has no length.
export function phaseElapsed(state, now) {
  const duration = phaseDuration(state);
  if (!duration || !(Number(state?.phaseEndsAt) > 0)) return 0;
  return Math.max(0, Math.min(duration, duration - (state.phaseEndsAt - Number(now))));
}

// When each slot comes in during the line-up (ms from the phase start), low to high: the first
// after a short beat, at most 600 ms apart, the last with time to read before the bets open.
export const LINEUP_LEAD_MS = 300;
export const LINEUP_REST_MS = 1200;

export function lineupDelays(count, duration = lineupMs(count)) {
  if (count <= 0) return [];
  if (count === 1) return [LINEUP_LEAD_MS];
  const room = Math.max(0, duration - LINEUP_LEAD_MS - LINEUP_REST_MS);
  const step = Math.min(600, room / (count - 1));
  return Array.from({ length: count }, (_, index) => Math.round(LINEUP_LEAD_MS + step * index));
}

// The reveal's beats (ms from the phase start), scaled to its length: the answer appears, slides
// along the line, "off by" shows on each slot, the winning slots light, the payouts, then the fact.
const REVEAL_BEATS = Object.freeze({ answer: 300, slide: 600, slideEnd: 2200, off: 2500, wins: 3400, payouts: 4400, fact: 5400 });

export function revealSteps(duration = REVEAL_MS) {
  const scale = Math.max(0, Number(duration) || REVEAL_MS) / REVEAL_MS;
  const out = {};
  for (const [beat, at] of Object.entries(REVEAL_BEATS)) out[beat] = Math.round(at * scale);
  return out;
}

// ---------------------------------------------------------------------------------------------
// Settings (docs §2.1): each chip cycles through its values when the host taps it.

export const SETTING_OPTIONS = Object.freeze({
  length: [{ value: "quick", label: "QUICK", detail: "7 questions · ~10 min" }, { value: "full", label: "FULL", detail: "12 questions · ~17 min" }],
  guessSeconds: [{ value: 20, label: "20 S" }, { value: 30, label: "30 S" }, { value: 45, label: "45 S" }],
  betSeconds: [{ value: 15, label: "15 S" }, { value: 25, label: "25 S" }, { value: 35, label: "35 S" }],
  fill: [{ value: true, label: "ON", detail: "up to 4 players" }, { value: false, label: "OFF" }],
});
const SETTING_NAMES = { length: "GAME", guessSeconds: "TO GUESS", betSeconds: "TO BET", fill: "COMPUTERS FILL" };
export const DEFAULT_SETTINGS = Object.freeze({ length: "quick", guessSeconds: 30, betSeconds: 25, fill: true });

// [{key, name, value, label, detail, next, patch, custom}], as the quiz's chips.
export function settingChips(state) {
  const settings = { ...DEFAULT_SETTINGS, ...(state?.settings || {}) };
  return Object.entries(SETTING_OPTIONS).map(([key, options]) => {
    const at = Math.max(0, options.findIndex((option) => option.value === settings[key]));
    const option = options[at];
    const next = options[(at + 1) % options.length].value;
    return { key, name: SETTING_NAMES[key], value: option.value, label: option.label, detail: option.detail || "", next, patch: { [key]: next }, custom: option.value !== DEFAULT_SETTINGS[key] };
  });
}

// "QUICK GAME · 30 S TO GUESS · 25 S TO BET · COMPUTERS FILL ON".
export function settingsLine(state) {
  return settingChips(state).map((chip) => (chip.key === "length" ? `${chip.label} GAME` : chip.key === "fill" ? `${chip.name} ${chip.label}` : `${chip.label} ${chip.name}`)).join(" · ");
}

// The lobby's note on computers filling to four (docs §2.1), "" when nobody will be added.
export const FILL_TO = 4;

export function fillCount(state) {
  const settings = { ...DEFAULT_SETTINGS, ...(state?.settings || {}) };
  if (!settings.fill) return 0;
  return Math.max(0, FILL_TO - seatedPlayers(state).length);
}

export function fillNote(state) {
  const count = fillCount(state);
  if (!count) return "";
  return `${count} ${count === 1 ? "computer" : "computers"} will join so there are ${FILL_TO} guesses`;
}

// Places: state.standings when finished, else by score then most closest (ties share a place).
export function rankings(state) {
  if (Array.isArray(state?.standings) && state.standings.length) {
    return state.standings.map((row) => ({ player: playerOf(state, row.id) || { id: row.id, name: row.name }, place: row.place, score: row.score, closest: row.closest ?? 0, left: Boolean(row.left) }));
  }
  const players = seatedPlayers(state);
  const better = (a, b) => (b.score - a.score) || ((b.closest || 0) - (a.closest || 0));
  return [...players]
    .sort((a, b) => better(a, b) || players.indexOf(a) - players.indexOf(b))
    .map((player) => ({ player, place: 1 + players.filter((other) => better(player, other) > 0).length, score: player.score, closest: player.closest || 0, left: false }));
}

export function placeWord(place) {
  return upper(ordinal(place));
}

// "4 CLOSEST" for the final table.
export function closestWord(count) {
  const number = Math.max(0, Number(count) || 0);
  return `${number} CLOSEST`;
}

// ---------------------------------------------------------------------------------------------
// How to play and the credits

export function howToPlayHtml(state) {
  const settings = { ...DEFAULT_SETTINGS, ...(state?.settings || {}) };
  const count = settings.length === "full" ? 12 : 7;
  return `<ol class="qhow">
    <li>Each question has a number for an answer. Everyone types a <b>guess</b> on their phone (${settings.guessSeconds} seconds). Nobody sees the others' guesses yet.</li>
    <li>The guesses line up low to high as <b>slots</b>. The middle slots pay 2×, and each step outward pays one more, up to 5×.</li>
    <li>Put your <b>2 free chips</b> on the slots you think are closest, in secret (${settings.betSeconds} seconds). Both can go on one slot. You may also <b>stake</b> 1 to 3 of your points on one of your chips.</li>
    <li>The <b>closest guess</b> wins, over or under: its owner scores 3 (5 if exact) and chips on it pay its odds. The <b>second closest</b> pays too: 1 to its owner, 1 for each chip on it.</li>
    <li>Free chips never lose. A stake on the winning slot pays the stake times the odds; anywhere else you lose the stake.</li>
    <li>${count} questions, and the <b>last one pays double</b>. Highest score wins; a tie goes to whoever was closest most often.</li>
  </ol>`;
}

export function creditsHtml(state) {
  return quizCreditsHtml(state);
}

// ---------------------------------------------------------------------------------------------
// Sounds (docs §8): the trigger list, in one place. Names are web/shared/sound.js sounds.

export const CLOSEST_SOUNDS = Object.freeze({
  start: { name: "go", gain: 0.8 }, // the intro
  question: { name: "card", gain: 0.7 }, // a new question opens
  lockIn: { name: "quizLock", gain: 0.8 }, // your guess or bet goes in (the phone plays it on the tap)
  othersIn: { name: "quizLock", gain: 0.45 }, // someone else guessed or bet
  slotPop: { name: "hop", gain: 0.6 }, // each slot of the line-up (opts.step pitches them up)
  chipsLand: { name: "chips", gain: 0.85 }, // every chip lands at once
  answerSlide: { name: "dealCard", gain: 0.7 }, // the answer slides along the line
  win: { name: "quizRight", gain: 1 }, // the closest is lit (yours: full; someone's: softer)
  miss: { name: "quizWrong", gain: 0.5 }, // nothing for you, or nobody guessed
  tick: { name: "tick", gain: 0.7 }, // each of the last 5 seconds to guess or bet (opts.last)
  gameOver: { name: "win", gain: 1 }, // the final places
});

const hit = (trigger, opts = {}, gain = CLOSEST_SOUNDS[trigger].gain) => ({ trigger, name: CLOSEST_SOUNDS[trigger].name, opts, gain });

// What a new state plays at once: [{trigger, name, opts, gain}]. Timed sounds (slot pops, the
// slide, the win, the ticks) follow the clock: closestTimedSounds.
export function closestSounds(prev, next, { viewerId = null } = {}) {
  const hits = [];
  if (!isClosestState(prev) || !isClosestState(next) || prev.code !== next.code) return hits;
  const entered = (phase) => next.phase === phase && (prev.phase !== phase || prev.question?.seq !== next.question?.seq);
  if (entered("intro")) hits.push(hit("start"));
  if (entered("guess")) hits.push(hit("question"));
  const fresh = (list, before) => (list || []).filter((id) => !(before || []).includes(id) && id !== viewerId);
  const sameQuestion = prev.question?.seq === next.question?.seq;
  if (next.phase === "guess" && sameQuestion && fresh(next.question?.guessed, prev.question?.guessed).length) hits.push(hit("othersIn"));
  if (next.phase === "bet" && sameQuestion && prev.phase === "bet" && fresh(next.question?.betsIn, prev.question?.betsIn).length) hits.push(hit("othersIn"));
  if (entered("chips")) {
    const chips = slotsOf(next).reduce((sum, slot) => sum + (slot.chips || []).reduce((n, chip) => n + (Number(chip.count) || 0), 0), 0);
    hits.push(hit("chipsLand", { amount: chips >= 12 ? 1000 : chips >= 6 ? 200 : 50 }));
  }
  if (next.status === "finished" && prev.status !== "finished") hits.push(hit("gameOver"));
  return hits;
}

// The sounds a state schedules on the clock: [{key, at, trigger, name, opts, gain}], `at` in server
// ms. The screens schedule the ones still ahead and cancel them when the phase changes. `ticks`:
// false on a phone that has already guessed or bet.
export function closestTimedSounds(state, { viewerId = null, ticks = true } = {}) {
  if (!isClosestState(state) || state.status !== "playing" || !(Number(state.phaseEndsAt) > 0)) return [];
  const out = [];
  const ends = Number(state.phaseEndsAt);
  const add = (at, trigger, opts, gain) => out.push({ key: `${trigger}@${Math.round(at)}`, at, ...hit(trigger, opts, gain) });
  const duration = phaseDuration(state);
  const start = duration ? ends - duration : 0;
  if (state.phase === "lineup") {
    lineupDelays(slotsOf(state).length, duration).forEach((delay, index) => add(start + delay, "slotPop", { step: index + 1 }));
  }
  if (state.phase === "reveal") {
    const steps = revealSteps(duration);
    if (state.question?.nobody) {
      add(start + steps.answer, "miss");
    } else {
      add(start + steps.slide, "answerSlide");
      const mine = viewerId ? resultOf(state, viewerId) : null;
      if (viewerId && mine) add(start + steps.wins, Number(mine.delta) > 0 ? "win" : "miss");
      else add(start + steps.wins, "win", {}, viewerId ? 0.5 : CLOSEST_SOUNDS.win.gain);
    }
  }
  if (ticks && ["guess", "bet"].includes(state.phase)) {
    for (const before of [5000, 4000, 3000, 2000, 1000]) add(ends - before, "tick", { last: before === 1000 });
  }
  return out;
}

// Music: the game's ordinary moods (our own tracks, web/shared/music.js).
export function closestMood(state) {
  if (!isClosestState(state)) return null;
  if (state.status === "lobby") return "lobby";
  if (state.status === "finished") return "victory";
  return "play";
}

// ---------------------------------------------------------------------------------------------
// Models for the looks (web/shared/closest-looks.js). Plain data: a look may lay these out any way.

function ownersOf(state, ids) {
  return (ids || []).map((id) => playerOf(state, id)).filter(Boolean);
}

// The number pad: the draft, its words, the range, the error, the keys and whether LOCK IN works.
// A draft under the minimum is not an error yet (typing more digits can fix it: the 4 of 4,000):
// LOCK IN just stays off under the range line. Over the maximum it is, as no digit can fix that.
export function padModel(state, { digits = "", error = "", sending = false } = {}) {
  const question = state?.question || {};
  const value = padValue(digits);
  const check = guessCheck(value, question);
  const disabled = padDisabled(digits);
  const you = state?.you || {};
  return {
    seq: question.seq,
    text: question.text || "",
    unit: question.unit || "",
    format: question.format || "plain",
    range: rangeText(question),
    digits: String(digits || ""),
    value,
    display: value === null ? "" : formatNumber(value, question.format),
    words: value === null ? "" : numberWords(value, question.format),
    error: error || (value !== null && !check.ok && !(value < Number(question.min)) ? check.error : ""),
    canLock: check.ok && !sending,
    changing: you.guess !== null && you.guess !== undefined,
    previous: you.guess !== null && you.guess !== undefined ? withUnit(you.guess, question) : "",
    keys: PAD_KEYS.map((key) => ({ key, label: key === "back" ? "⌫" : key, aria: key === "back" ? "Delete the last digit" : key === "000" ? "Three zeros" : key, disabled: Boolean(disabled[key]) })),
  };
}

// The line of slots (TV): each slot's number, unit, odds, owners, and from the chips phase its
// chips; at the reveal off-by and result. `delays` are the line-up entrances relative to now (ms;
// negative = already in), so a re-render never restarts them.
export function lineModel(state, { elapsed = Infinity } = {}) {
  const question = state?.question || {};
  const slots = slotsOf(state);
  const phase = state?.phase;
  const delays = phase === "lineup" ? lineupDelays(slots.length, phaseDuration(state)) : [];
  const showChips = ["chips", "reveal"].includes(phase) || state?.status === "finished";
  const showReveal = phase === "reveal" || (state?.status === "finished" && slots.some((slot) => slot.result !== null && slot.result !== undefined));
  return {
    unit: question.unit || "",
    format: question.format || "plain",
    count: slots.length,
    showChips,
    showReveal,
    slots: slots.map((slot, index) => ({
      index: slot.index ?? index,
      value: slot.value,
      text: formatNumber(slot.value, question.format),
      unit: question.unit || "",
      odds: slot.odds ?? oddsFor(index, slots.length),
      oddsLabel: oddsLabel(slot.odds ?? oddsFor(index, slots.length)),
      owners: ownersOf(state, slot.ownerIds),
      chips: showChips ? (slot.chips || []).map((chip) => ({ player: playerOf(state, chip.playerId) || { id: chip.playerId, name: "" }, count: Number(chip.count) || 1, stake: Number(chip.stake) || 0 })) : [],
      stakeTotal: showChips ? (slot.chips || []).reduce((sum, chip) => sum + (Number(chip.stake) || 0), 0) : 0,
      off: showReveal && Number.isFinite(Number(slot.off)) ? Number(slot.off) : null,
      offText: showReveal ? offText(slot.off, question.format) : "",
      result: showReveal ? slot.result || "" : "",
      delay: phase === "lineup" ? Math.round((delays[index] ?? 0) - (Number.isFinite(elapsed) ? elapsed : 1e9)) : null,
    })),
  };
}

// The bet screen (phone): every slot as a row with where your chips are and where the stake is.
export function betModel(state, bet) {
  const question = state?.question || {};
  const draft = betDraft(state, bet);
  const stakeMax = stakeMaxOf(state);
  const own = ownSlot(state);
  const slots = slotsOf(state);
  const stakeSlot = draft.stake > 0 ? draft.stakeSlot : null;
  const stakeOdds = stakeSlot === null ? 0 : slots.find((slot) => slot.index === stakeSlot)?.odds ?? 0;
  return {
    key: draft.key,
    bet: draft,
    unit: question.unit || "",
    stake: draft.stake,
    stakeMax,
    canStake: stakeMax > 0,
    split: draft.chips[0] !== draft.chips[1],
    risk: riskText(draft.stake),
    stakeWins: stakeSlot === null ? 0 : stakeWin(draft.stake, stakeOdds),
    check: betCheck(draft, state),
    rows: slots.map((slot, index) => {
      const at = slot.index ?? index;
      const chipCount = draft.chips.filter((chip) => chip === at).length;
      return {
        index: at,
        text: formatNumber(slot.value, question.format),
        unit: question.unit || "",
        odds: slot.odds,
        oddsLabel: oddsLabel(slot.odds),
        owners: ownersOf(state, slot.ownerIds),
        mine: at === own,
        chipA: draft.chips[0] === at,
        chipB: draft.chips[1] === at,
        chips: chipCount,
        stakeHere: stakeSlot === at,
      };
    }),
  };
}

// The reveal (TV): the answer, where it falls, the fact, the beats, and each player's breakdown.
export function revealModel(state, { elapsed = Infinity } = {}) {
  const question = state?.question || {};
  const duration = phaseDuration({ ...state, phase: "reveal" }) || REVEAL_MS;
  const steps = revealSteps(duration);
  const line = lineModel({ ...state, phase: "reveal" }, { elapsed });
  const results = (question.results || []).map((row) => ({ player: playerOf(state, row.playerId) || { id: row.playerId, name: "" }, row, breakdown: breakdown(state, row.playerId) }));
  const time = Number.isFinite(elapsed) ? elapsed : 1e9;
  return {
    nobody: Boolean(question.nobody),
    answer: question.answer,
    answerText: withUnit(question.answer, question),
    answerWords: question.format === "year" ? "" : numberWords(question.answer, question.format),
    position: answerPosition(slotsOf(state), question.answer),
    fact: question.fact || "",
    double: isLastQuestion(state),
    steps,
    // Each beat relative to now (ms, negative = already happened), for CSS delays.
    beats: Object.fromEntries(Object.entries(steps).map(([beat, at]) => [beat, Math.round(at - time)])),
    line,
    winners: winningSlots(state).flatMap((slot) => ownersOf(state, slot.ownerIds)),
    results,
  };
}
