// The quiz's shared view helpers (docs/QUIZ-API.md): wording, colours, score formatting, the
// setting chips, how to play with the question credits, and which sounds a new state plays.
// Pure: state in, strings and plain objects out. The table screen (web/table/quiz-table.js) and the
// phone (web/phone/quiz-screens.js) both build on these, so the two always say the same thing.
//
// Look: Estate's, never a TV quiz show's. Category headers are deed cards with a property-group
// colour bar; clue values sit on ivory cards; gold is for gold squares and what you can do now.

import { escapeHtml, ordinal, upper } from "./format.js";
import { activeClue } from "./quiz-clock.js";

const e = escapeHtml;

export function isQuizState(state) {
  return state?.game === "quiz";
}

export function playerOf(state, id) {
  return (state?.players || []).find((player) => player.id === id) || null;
}

// Everyone still at the table (in join order).
export function seatedPlayers(state) {
  return (state?.players || []).filter((player) => player.status !== "left");
}

export const LETTERS = Object.freeze(["A", "B", "C", "D"]);

// Each board column gets a property group's colour bar, by position (a board never repeats one).
// Yellow is left out: yellow money on blue is exactly the TV look the game must not have.
export const CATEGORY_GROUPS = Object.freeze(["red", "orange", "green", "lightBlue", "pink", "darkBlue"]);
const GROUP_HEX = { red: "#E8232A", orange: "#F7941D", green: "#1FAE57", lightBlue: "#A9DCF2", pink: "#D3368C", darkBlue: "#1166B4" };
const GROUP_INK = { red: "#FFFFFF", orange: "#15191E", green: "#FFFFFF", lightBlue: "#15191E", pink: "#FFFFFF", darkBlue: "#FFFFFF" };

export function categoryColor(index) {
  const group = CATEGORY_GROUPS[((Number(index) || 0) % CATEGORY_GROUPS.length + CATEGORY_GROUPS.length) % CATEGORY_GROUPS.length];
  return { group, bg: GROUP_HEX[group], ink: GROUP_INK[group] };
}

// ---------------------------------------------------------------------------------------------
// Category titles that fit their card. A title is up to 24 characters ("MEDICAL BREAKTHROUGHS", or
// one long word); each card picks the largest whole font size, from its own size down to a floor,
// at which the title wraps between words into lines that fit the card. Worked out here from the
// font's widths rather than measured on screen, so a re-render (the table redraws every 250 ms)
// always gives the same size. At the floor a word too long for a line breaks inside it (the CSS has
// overflow-wrap: anywhere), so the title still fits.

// Oswald 700 advance widths in em (measured in Edge from /shared/fonts/Oswald-latin.woff2).
const OSWALD_700 = {
  " ": 0.256, A: 0.551, B: 0.588, C: 0.563, D: 0.586, E: 0.447, F: 0.434, G: 0.582, H: 0.61, I: 0.301, J: 0.349, K: 0.567, L: 0.443, M: 0.704,
  N: 0.561, O: 0.586, P: 0.571, Q: 0.586, R: 0.6, S: 0.498, T: 0.445, U: 0.588, V: 0.526, W: 0.697, X: 0.515, Y: 0.493, Z: 0.433,
  0: 0.55, 1: 0.385, 2: 0.514, 3: 0.514, 4: 0.524, 5: 0.509, 6: 0.538, 7: 0.439, 8: 0.521, 9: 0.538,
  "&": 0.57, "'": 0.17, "’": 0.17, ",": 0.238, "-": 0.324, ".": 0.244, ":": 0.278, "!": 0.257, "?": 0.483, "/": 0.422, "(": 0.338, ")": 0.328, '"': 0.36, "+": 0.449,
};
const WIDEST = 0.704; // anything not in the table (accents, symbols) counts as an M

function charEm(char, spacing) {
  return (OSWALD_700[char] ?? WIDEST) + spacing;
}

// How many lines `words` take at `size` px in a line `width` px wide, or null when a word is too
// long for a line and may not break.
function titleLines(words, size, width, spacing, breakWords) {
  const space = charEm(" ", spacing) * size;
  let lines = 0;
  let used = 0;
  for (const word of words) {
    const chars = [...word];
    const wide = chars.reduce((sum, char) => sum + charEm(char, spacing), 0) * size;
    if (lines && used + space + wide <= width) {
      used += space + wide;
    } else if (wide <= width) {
      lines += 1;
      used = wide;
    } else if (breakWords) {
      lines += 1;
      used = 0;
      for (const char of chars) {
        const step = charEm(char, spacing) * size;
        if (used && used + step > width) {
          lines += 1;
          used = 0;
        }
        used += step;
      }
    } else {
      return null;
    }
  }
  return lines;
}

// The font size (whole px, max down to min) for a category title in a box of width × height px.
// `slack` keeps a little room for rounding.
export function fitTitleSize(title, { width, height, max, min, lineHeight = 1.08, spacing = 0.04, slack = 0.95 } = {}) {
  const words = String(title || "").toUpperCase().trim().split(/\s+/).filter(Boolean);
  if (!words.length) return max;
  const room = width * slack;
  for (let size = max; size >= min; size -= 1) {
    const lines = titleLines(words, size, room, spacing, size === min);
    if (lines !== null && lines * size * lineHeight <= height) return size;
  }
  return min;
}

const MINUS = "−";

// 1,200 · −200 (scores may go below zero).
export function fmtScore(value) {
  const number = Math.round(Number(value) || 0);
  const text = Math.abs(number).toLocaleString("en-GB");
  return number < 0 ? `${MINUS}${text}` : text;
}

// +400 · −400 · ±0
export function signedPoints(value) {
  const number = Math.round(Number(value) || 0);
  if (number === 0) return "±0";
  return number > 0 ? `+${fmtScore(number)}` : fmtScore(number);
}

// ---------------------------------------------------------------------------------------------
// Settings (docs §2.1): each chip cycles through its values when the host taps it.

export const SETTING_OPTIONS = Object.freeze({
  length: [{ value: "quick", label: "QUICK", detail: "one board · ~12 min" }, { value: "full", label: "FULL", detail: "two boards · ~22 min" }],
  readSpeed: [{ value: "slow", label: "SLOW" }, { value: "normal", label: "NORMAL" }, { value: "fast", label: "FAST" }],
  answerSeconds: [{ value: 6, label: "6 S" }, { value: 8, label: "8 S" }, { value: 12, label: "12 S" }],
});
const SETTING_NAMES = { length: "GAME", readSpeed: "READING", answerSeconds: "TO ANSWER" };
export const DEFAULT_SETTINGS = Object.freeze({ length: "quick", readSpeed: "normal", answerSeconds: 8 });

// [{key, name, value, label, detail, next, patch, custom}] for the three settings.
export function settingChips(state) {
  const settings = { ...DEFAULT_SETTINGS, ...(state?.settings || {}) };
  return Object.entries(SETTING_OPTIONS).map(([key, options]) => {
    const at = Math.max(0, options.findIndex((option) => option.value === settings[key]));
    const option = options[at];
    const next = options[(at + 1) % options.length].value;
    return { key, name: SETTING_NAMES[key], value: option.value, label: option.label, detail: option.detail || "", next, patch: { [key]: next }, custom: option.value !== DEFAULT_SETTINGS[key] };
  });
}

// "QUICK · NORMAL READING · 8 S TO ANSWER" for non-hosts and lists.
export function settingsLine(state) {
  return settingChips(state).map((chip) => (chip.key === "length" ? `${chip.label} GAME` : `${chip.label} ${chip.name}`)).join(" · ");
}

// ---------------------------------------------------------------------------------------------
// Wording

export function roundTitle(state) {
  const round = Number(state?.round) || 0;
  if (round >= 3) return "THE FINAL";
  if (round === 2) return "ROUND 2";
  return "ROUND 1";
}

// Under the round title on the intro: what this round is worth.
export function roundSubtitle(state) {
  if ((Number(state?.round) || 0) === 2) return "DOUBLE VALUES · 200 TO 1,000";
  const rounds = Number(state?.rounds) || 1;
  return rounds > 1 ? "100 TO 500 · TWO BOARDS, THEN THE FINAL" : "100 TO 500 · ONE BOARD, THEN THE FINAL";
}

export function cluesLeft(board) {
  let left = 0;
  for (const category of board?.categories || []) for (const cell of category.cells || []) if (!cell.played) left += 1;
  return left;
}

function nameOf(state, id, viewerId) {
  const player = playerOf(state, id);
  if (!player) return "";
  return viewerId && player.id === viewerId ? "YOU" : upper(player.name);
}

// The big line for the phase: who is doing what. `viewerId` turns their name into YOU.
export function phaseLine(state, { viewerId = null } = {}) {
  if (!state) return "";
  const chooser = nameOf(state, state.chooserId, viewerId);
  const you = (id) => viewerId && id === viewerId;
  switch (state.phase) {
    case "lobby":
      return "WAITING TO START";
    case "intro":
      return roundTitle(state);
    case "pick":
      return you(state.chooserId) ? "YOUR PICK" : chooser ? `${chooser} IS PICKING` : "PICKING";
    case "gold":
      return "GOLD SQUARE";
    case "bet":
      return you(state.bet?.playerId) ? "YOUR BET" : `${nameOf(state, state.bet?.playerId, viewerId) || chooser} IS BETTING`;
    case "read":
      return "READ THE CLUE";
    case "answer":
      return "ANSWER NOW";
    case "reveal": {
      const clue = state.clue || {};
      if (clue.winnerId) return you(clue.winnerId) ? `YOU WIN ${fmtScore(winnerDelta(clue))}` : `${nameOf(state, clue.winnerId, viewerId)} WINS ${fmtScore(winnerDelta(clue))}`;
      return clue.gold ? "NOT THIS TIME" : "NOBODY GOT IT";
    }
    case "roundEnd":
      return `${roundTitle(state)} COMPLETE`;
    case "finalIntro":
      return "THE FINAL";
    case "finalBet":
      return "PLACE YOUR BETS";
    case "finalRead":
      return "THE FINAL CLUE";
    case "finalAnswer":
      return "FINAL ANSWERS";
    case "finalReveal":
      return "THE RESULTS";
    case "finished":
      return "GAME OVER";
    default:
      return "";
  }
}

function winnerDelta(clue) {
  const hit = (clue.results || []).find((result) => result.playerId === clue.winnerId && result.correct && !result.late);
  return Math.abs(hit?.delta ?? clue.value ?? 0);
}

// What a player got from the revealed clue: {result, outcome, delta, noAnswer}. outcome: "won" |
// "lost" | "late" | "right" | "missed" | "none" (right: right but not first and not late; missed:
// eligible, didn't tap, no change). delta is the score change (docs §4 scoring). A gold square's
// finder who never taps has no results row (§3.3 lists taps only) yet still loses the bet, unless
// they left: that is "lost" with noAnswer set.
export function outcomeFor(state, playerId) {
  const clue = state?.clue;
  if (!clue || state.phase !== "reveal") return { result: null, outcome: "none", delta: 0, noAnswer: false };
  const result = (clue.results || []).find((row) => row.playerId === playerId) || null;
  if (!result) {
    const eligible = (clue.eligible || []).includes(playerId);
    const finder = Boolean(clue.gold && playerId && state.bet?.playerId === playerId);
    if (finder && !clue.winnerId && playerOf(state, playerId)?.status !== "left") {
      return { result: null, outcome: "lost", delta: -Math.abs(Number(state.bet.amount) || 0), noAnswer: true };
    }
    return { result: null, outcome: eligible ? "missed" : "none", delta: 0, noAnswer: eligible };
  }
  const delta = Number(result.delta) || 0;
  if (result.late) return { result, outcome: "late", delta: 0, noAnswer: false };
  if (result.correct) return { result, outcome: clue.winnerId === playerId ? "won" : "right", delta, noAnswer: false };
  return { result, outcome: delta < 0 ? "lost" : "late", delta, noAnswer: false };
}

// The players by score for a table of places: state.standings when finished, else by score
// (ties share a place), each {player, place, score}.
export function rankings(state) {
  if (Array.isArray(state?.standings) && state.standings.length) {
    return state.standings.map((row) => ({ player: playerOf(state, row.id) || { id: row.id, name: row.name }, place: row.place, score: row.score, correct: row.correct, wrong: row.wrong, left: Boolean(row.left) }));
  }
  const players = seatedPlayers(state);
  return [...players]
    .sort((a, b) => (b.score - a.score) || players.indexOf(a) - players.indexOf(b))
    .map((player) => ({ player, place: 1 + players.filter((other) => other.score > player.score).length, score: player.score, correct: player.correct, wrong: player.wrong, left: false }));
}

export function placeWord(place) {
  return upper(ordinal(place));
}

// ---------------------------------------------------------------------------------------------
// Your own state (phones)

// May the viewer tap a choice on this clue now (given the choices are showing)? While reading
// the server's canAnswer is still false, so eligibility decides; from the answer phase on the
// server's word does.
export function canAnswerNow(state) {
  const you = state?.you;
  if (!you) return false;
  const clue = activeClue(state);
  if (!clue) return false;
  if (clue.final) {
    if (you.finalChoice >= 0) return false;
    if (state.phase === "finalAnswer") return you.canAnswer !== false;
    return state.phase === "finalRead" && playerOf(state, you.id)?.status === "playing";
  }
  if (you.choice >= 0) return false;
  if (state.phase === "answer") return you.canAnswer !== false;
  return state.phase === "read" && (clue.eligible || []).includes(you.id);
}

// The viewer's tap on the active clue (−1 none).
export function myChoice(state) {
  const clue = activeClue(state);
  if (!clue || !state?.you) return -1;
  const value = clue.final ? state.you.finalChoice : state.you.choice;
  return Number.isInteger(value) ? value : -1;
}

// ---------------------------------------------------------------------------------------------
// How to play and the credits

export function howToPlayHtml(state) {
  const seconds = Number(state?.settings?.answerSeconds) || 8;
  return `<ol class="qhow">
    <li>Whoever has <b>control</b> picks a clue on their phone. Bigger numbers, harder clues.</li>
    <li>Everyone reads the clue. When the bar runs out, <b>four answers</b> appear on every phone at the same moment (${seconds} seconds to answer).</li>
    <li>The <b>first right tap</b> wins the points and control. A <b>wrong tap</b> loses the points and locks you out of that clue.</li>
    <li>A few <b>gold squares</b> hide on the board. Find one and you bet first, then answer it alone.</li>
    <li><b>The final:</b> everyone bets in secret, then answers one last clue. Right adds your bet, wrong takes it away.</li>
    <li>Highest score wins. Each phone times you from the moment the answers appear on it, so the fastest <b>person</b> wins, not the fastest Wi-Fi.</li>
  </ol>`;
}

// The question packs' attribution lines (docs §7: licences that need it), or our own line.
export function creditsHtml(state) {
  const lines = (Array.isArray(state?.credits) ? state.credits : []).filter((line) => String(line || "").trim());
  const items = lines.length ? lines.map((line) => `<li>${e(line)}</li>`).join("") : "<li>Questions written and checked for Estate.</li>";
  return `<ul class="qcredits">${items}</ul>`;
}

// ---------------------------------------------------------------------------------------------
// Sounds: what a new state plays (the open chime and the last-seconds ticks follow the clock and
// are played by the screens' timers, not here). [{name, opts, gain}]

export function quizSounds(prev, next, { viewerId = null } = {}) {
  const hits = [];
  if (!isQuizState(prev) || !isQuizState(next) || prev.code !== next.code) return hits;
  const add = (name, opts = {}, gain = 1) => hits.push({ name, opts, gain });
  const entered = (phase) => next.phase === phase && prev.phase !== phase;
  if (entered("intro") || entered("finalIntro")) add("go", {}, 0.8);
  if (entered("pick") && viewerId && next.chooserId === viewerId) add("turn", {}, 0.8);
  if (entered("gold")) add("quizGold");
  if (entered("read") && !next.clue?.gold) add("card", {}, 0.7);
  if (next.phase === "read" && prev.phase === "bet" && next.bet?.amount) add("chips", { amount: next.bet.amount }, 0.8);
  // Taps coming in (who, not what): a soft lock tick for each new one, not for your own (the
  // phone already ticked when you tapped).
  const answered = (state) => (state.phase === "finalAnswer" || state.phase === "finalRead" ? state.final?.answersIn : state.clue?.answered) || [];
  const before = new Set(answered(prev));
  const fresh = answered(next).filter((id) => !before.has(id) && id !== viewerId);
  if (fresh.length && ["answer", "finalAnswer"].includes(next.phase)) add("quizLock", {}, 0.45);
  const betsBefore = new Set(prev.final?.betsIn || []);
  if (next.phase === "finalBet" && (next.final?.betsIn || []).some((id) => !betsBefore.has(id) && id !== viewerId)) add("chips", { amount: 50 }, 0.4);
  if (entered("reveal")) {
    const clue = next.clue || {};
    if (viewerId && next.you) {
      const { outcome } = outcomeFor(next, viewerId);
      if (outcome === "won" || outcome === "right") add("quizRight");
      else if (outcome === "lost") add("quizWrong");
      else if (clue.winnerId) add("quizRight", {}, 0.5);
      else add("quizWrong", {}, 0.5);
    } else if (clue.winnerId) add("quizRight");
    else add("quizWrong", {}, 0.7);
  }
  if (next.phase === "finalReveal") {
    const was = prev.phase === "finalReveal" ? prev.final?.revealed || 0 : 0;
    const now = next.final?.revealed || 0;
    for (const result of (next.final?.results || []).slice(was, now)) {
      const mine = viewerId && result.playerId === viewerId;
      add(result.correct ? "quizRight" : "quizWrong", {}, mine ? 1 : 0.6);
    }
  }
  if (entered("roundEnd")) add("accept", {}, 0.8);
  if (next.status === "finished" && prev.status !== "finished") add("win");
  return hits;
}

// Music: "thinking" from the final's category to its last answer, a little tension while the
// final is revealed, else the game's ordinary moods.
export function quizMood(state) {
  if (!isQuizState(state)) return null;
  if (state.status === "lobby") return "lobby";
  if (state.status === "finished") return "victory";
  if (["finalIntro", "finalBet", "finalRead", "finalAnswer"].includes(state.phase)) return "thinking";
  if (state.phase === "finalReveal") return "tension";
  return "play";
}
