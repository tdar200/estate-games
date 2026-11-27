// Quiz demo scenes for both screens: `/?demo=quiz-answer-table`, `/play?demo=quiz-answer-phone`, or
// any web/fixtures/quiz/ fixture name (docs/QUIZ-API.md §9). A name without -table / -phone tries
// it as written, then the -table fixture.
//
// Demos run on the fixture's own clock: the screens take state.now as "now", so a reading stays
// mid-bar and the choices of an answer fixture show at once (its now is past openAt) without a
// server or a ping.
//
// A few more scenes are made from the fixtures (QUIZ_SCENES below) for the moments the fixture list
// skips: the final's category and its reading, the phone during the intro, the gold square, the
// round end and the final reveal, a reading whose openAt has just passed (the choices appear
// before the server's answer state arrives), and a full table of eight at the final reveal and
// the final scores.

export async function fetchQuizFixture(name) {
  const safe = String(name || "").replace(/[^a-z0-9-]/gi, "");
  const response = await fetch(`/fixtures/quiz/${safe}.json`, { cache: "no-store" });
  if (!response.ok) throw new Error(`Quiz demo "${safe}" not found`);
  return response.json();
}

// A table-screen state as the host's phone would get it.
function asHostPhone(state, you = {}) {
  const host = (state.players || []).find((player) => player.id === state.hostId) || state.players?.[0];
  if (!host) return state;
  return {
    ...state,
    viewerId: host.id,
    viewerRole: "player",
    viewerName: host.name,
    viewerIsHost: true,
    you: { id: host.id, canPick: false, canAnswer: false, choice: -1, canBet: false, betMin: 0, betMax: 0, bet: null, finalChoice: -1, latencyMs: 42, ...you },
  };
}

const unlock = (players) => (players || []).map((player) => ({ ...player, lockedIn: false }));
const noYou = (you) => (you ? { ...you, canBet: false, betMin: 0, betMax: 0, canAnswer: false } : you);

function finalIntro(state) {
  const { seq, category } = state.final || {};
  return { ...state, phase: "finalIntro", phaseEndsAt: state.now + 4000, final: { seq, category }, players: unlock(state.players), you: noYou(state.you) };
}

function finalRead(state) {
  const { answersIn, ...final } = state.final || {};
  const now = final.readAt + Math.round((final.openAt - final.readAt) * 0.55);
  return { ...state, phase: "finalRead", now, phaseEndsAt: final.openAt, final, players: unlock(state.players), you: noYou(state.you) };
}

// A full table: the fixture game seats six, the room seats eight. Two more players (Ada on a
// phone, Ben a computer), so the layout checks see the longest lists: the final reveal and the
// final scores.
const EXTRA_SEATS = [
  { id: "p7", name: "Ada", kind: "human", input: "phone", tokenKind: "anchor", color: "#D88890" },
  { id: "p8", name: "Ben", kind: "ai", input: "ai", tokenKind: "kite", color: "#727078" },
];

function seatTwoMore(state, scores) {
  const base = state.players?.[0] || {};
  const extra = EXTRA_SEATS.map((seat, i) => ({ ...base, ...seat, ...scores[i], status: "playing", isChooser: false, lockedIn: false, online: true, autopilot: false }));
  return { ...state, players: [...(state.players || []), ...extra] };
}

// The packs' longest final (core-1/final-09), the most the question card ever has to hold.
const LONGEST_FINAL = {
  category: "EPIC TALES",
  text: "Which ancient Mesopotamian poem, one of the oldest surviving works of literature, follows a king's quest for immortality?",
  answer: "The Epic of Gilgamesh",
  wrong: ["The Odyssey", "The Aeneid", "The Mahabharata"],
};

// The final reveal with eight and the longest final: Ada (−100 → 200) and Ben (600 → 0) shown,
// the two leaders to come.
function fullFinalReveal(state) {
  const seated = seatTwoMore(state, [{ score: 200, correct: 4, wrong: 3 }, { score: 0, correct: 5, wrong: 2 }]);
  const right = Number.isInteger(state.final?.answerIndex) ? state.final.answerIndex : 1;
  const choices = [...LONGEST_FINAL.wrong];
  choices.splice(right, 0, LONGEST_FINAL.answer);
  const final = { ...(state.final || {}), category: LONGEST_FINAL.category, text: LONGEST_FINAL.text, choices };
  const order = [...(final.order || [])];
  order.splice(Math.max(0, order.indexOf("p3")), 0, "p7", "p8");
  const results = [...(final.results || [])];
  const at = results.findIndex((result) => result.playerId === "p3");
  results.splice(at < 0 ? results.length : at, 0,
    { playerId: "p7", bet: 300, choice: right, correct: true, before: -100, after: 200 },
    { playerId: "p8", bet: 600, choice: (right + 2) % 4, correct: false, before: 600, after: 0 });
  return {
    ...seated,
    final: { ...final, order, results, revealed: (final.revealed || 0) + 2, betsIn: [...(final.betsIn || []), "p7", "p8"], answersIn: [...(final.answersIn || []), "p7", "p8"] },
  };
}

// The final scores with eight: Ada third, Ben last.
function fullFinished(state) {
  const seated = seatTwoMore(state, [{ score: 200, correct: 4, wrong: 3 }, { score: -1200, correct: 5, wrong: 6 }]);
  const standings = (state.standings || []).map((row) => (row.place >= 3 ? { ...row, place: row.place + 1 } : row));
  standings.splice(2, 0, { id: "p7", name: "Ada", place: 3, score: 200, correct: 4, wrong: 3 });
  standings.push({ id: "p8", name: "Ben", place: 8, score: -1200, correct: 5, wrong: 6 });
  const placeOf = new Map(standings.map((row) => [row.id, row.place]));
  return { ...seated, standings, players: seated.players.map((player) => ({ ...player, place: placeOf.get(player.id) ?? player.place })) };
}

export const QUIZ_SCENES = Object.freeze({
  "quiz-finalreveal-full-table": { from: "quiz-finalreveal-table", make: fullFinalReveal },
  "quiz-finished-full-table": { from: "quiz-finished-table", make: fullFinished },
  "quiz-intro-phone": { from: "quiz-intro-table", make: (state) => asHostPhone(state) },
  "quiz-gold-phone": { from: "quiz-gold-table", make: (state) => asHostPhone(state) },
  "quiz-roundend-phone": { from: "quiz-roundend-table", make: (state) => asHostPhone(state) },
  "quiz-finalintro-table": { from: "quiz-finalbet-table", make: finalIntro },
  "quiz-finalintro-phone": { from: "quiz-finalbet-phone", make: finalIntro },
  "quiz-finalread-table": { from: "quiz-finalanswer-table", make: finalRead },
  "quiz-finalread-phone": { from: "quiz-finalanswer-phone", make: (state) => ({ ...finalRead(state), you: { ...noYou(state.you), bet: state.you?.bet ?? null } }) },
  "quiz-finalanswered-phone": {
    from: "quiz-finalanswer-phone",
    make: (state) => ({
      ...state,
      final: { ...state.final, answersIn: [...new Set([...(state.final?.answersIn || []), state.viewerId])] },
      players: state.players.map((player) => (player.id === state.viewerId ? { ...player, lockedIn: true } : player)),
      you: { ...state.you, canAnswer: false, finalChoice: 2 },
    }),
  },
  "quiz-finalreveal-phone": {
    from: "quiz-finalreveal-table",
    make: (state) => {
      const mine = (state.final?.results || []).find((result) => result.playerId === state.hostId);
      return asHostPhone(state, { bet: mine?.bet ?? 1301, finalChoice: mine?.choice ?? 2 });
    },
  },
  "quiz-read-open-table": { from: "quiz-read-table", make: (state) => ({ ...state, now: state.clue.openAt + 400 }) },
  "quiz-read-open-phone": { from: "quiz-read-phone", make: (state) => ({ ...state, now: state.clue.openAt + 400 }) },
});

export async function buildQuizScene(name, load = fetchQuizFixture) {
  const scene = QUIZ_SCENES[name];
  if (scene) {
    const fixture = await load(scene.from);
    return { state: scene.make(fixture.state), title: `${name} (made from ${scene.from})` };
  }
  try {
    const fixture = await load(name);
    return { state: fixture.state, title: fixture.meta?.description || name };
  } catch (error) {
    if (/-(table|phone)$/.test(name)) throw error;
    return buildQuizScene(`${name}-table`, load);
  }
}

// The clock a demo screen runs on: the fixture's server time, frozen.
export function demoClock(state) {
  const now = Number(state?.now);
  return Number.isFinite(now) && now > 0 ? now : Date.now();
}
