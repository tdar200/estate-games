// Near Thing demo scenes for both screens: `/?demo=closest-reveal-table`, `/play?demo=closest-bet-phone`,
// or any web/fixtures/closest/ fixture name (docs/CLOSEST-API.md §8). A name without -table / -phone
// tries it as written, then the -table fixture.
//
// Demos run on the fixture's own clock (the screens take state.now as "now") and show each
// phase's settled frame; `&play=1` plays the line-up and the reveal from the phase start.
//
// More scenes are made from the fixtures (CLOSEST_SCENES below) for the worst cases the fixture
// list skips: eight seats in the lobby and at the end, eight 7-digit slots while betting, with the
// chips down and at the reveal, all eight players' chips on one slot at the reveal, a line-up of
// one slot, the last (double) question at its reveal, and the phone at the moments the fixture
// list only shows on the table.

export async function fetchClosestFixture(name) {
  const safe = String(name || "").replace(/[^a-z0-9-]/gi, "");
  const response = await fetch(`/fixtures/closest/${safe}.json`, { cache: "no-store" });
  if (!response.ok) throw new Error(`Near Thing demo "${safe}" not found`);
  return response.json();
}

const clone = (value) => JSON.parse(JSON.stringify(value));

// The viewer's own guess on this question: their results row, else the slot they own.
function guessOf(state, id) {
  const row = (state.question?.results || []).find((result) => result.playerId === id);
  if (row) return row.guess ?? null;
  const slot = (state.question?.slots || []).find((candidate) => (candidate.ownerIds || []).includes(id));
  return slot ? slot.value : null;
}

// A table-screen state as the host's phone would get it.
export function asHostPhone(state, you = {}) {
  const host = (state.players || []).find((player) => player.id === state.hostId) || state.players?.[0];
  if (!host) return state;
  return {
    ...state,
    viewerId: host.id,
    viewerRole: "player",
    viewerName: host.name,
    viewerIsHost: true,
    you: { id: host.id, canGuess: false, guess: guessOf(state, host.id), changesLeft: 1, canBet: false, chips: null, stake: 0, stakeSlot: null, stakeMax: Math.max(0, Math.min(3, Number(host.score) || 0)), ...you },
  };
}

// Two more seats (Ada on a phone, Ben an Easy computer) for an eight-seat table.
const EXTRA_SEATS = [
  { id: "p7", name: "Ada", kind: "human", input: "phone", tokenKind: "anchor", color: "#D88890", difficulty: null },
  { id: "p8", name: "Ben", kind: "ai", input: "ai", tokenKind: "kite", color: "#727078", difficulty: "easy" },
];

function seatTwoMore(state, extra = [{}, {}]) {
  const base = state.players?.[0] || {};
  const status = state.status === "lobby" ? "waiting" : "playing";
  const seats = EXTRA_SEATS.map((seat, index) => ({ ...base, ...seat, status, score: 0, closest: 0, guessed: false, betIn: false, online: true, autopilot: false, ...extra[index] }));
  return { ...state, players: [...(state.players || []), ...seats] };
}

// Scores a question as the engine does (docs §4.1), for scenes that move a state on to its reveal.
export function settle(state, answer) {
  const question = state.question || {};
  const m = question.double ? 2 : 1;
  const slots = (question.slots || []).map((slot) => ({ ...slot, chips: slot.chips || [], off: Math.abs(slot.value - answer) }));
  const best = Math.min(...slots.map((slot) => slot.off));
  const wins = slots.filter((slot) => slot.off === best);
  const rest = slots.filter((slot) => slot.off > best);
  const next = rest.length ? Math.min(...rest.map((slot) => slot.off)) : null;
  const seconds = wins.length === 1 && next !== null ? rest.filter((slot) => slot.off === next) : [];
  for (const slot of slots) slot.result = wins.includes(slot) ? "win" : seconds.includes(slot) ? "second" : "";
  const players = state.players || [];
  const results = players.filter((player) => player.status === "playing").map((player) => {
    const mine = slots.find((slot) => slot.ownerIds.includes(player.id)) || null;
    let bonus = 0;
    if (mine?.result === "win") bonus = (mine.off === 0 ? 5 : 3) * m;
    else if (mine?.result === "second") bonus = m;
    let chipPoints = 0;
    let stakePoints = 0;
    for (const slot of slots) {
      for (const chip of slot.chips.filter((candidate) => candidate.playerId === player.id)) {
        if (slot.result === "win") chipPoints += chip.count * slot.odds * m;
        else if (slot.result === "second") chipPoints += chip.count * m;
        if (chip.stake) stakePoints += slot.result === "win" ? chip.stake * slot.odds : -chip.stake;
      }
    }
    const delta = bonus + chipPoints + stakePoints;
    const before = Number(player.score) || 0;
    return { playerId: player.id, guess: mine ? mine.value : null, slot: mine ? mine.index : null, bonus, chipPoints, stakePoints, delta, before, after: Math.max(0, before + delta) };
  });
  const after = new Map(results.map((row) => [row.playerId, row]));
  return {
    ...state,
    phase: "reveal",
    phaseEndsAt: state.now + (question.double ? 9000 : 8000),
    question: { ...question, slots, answer, fact: question.fact || `The made-up answer is ${answer}.`, src: question.src || ["https://example.org/made-up"], results, nobody: false, betsIn: undefined, guessed: undefined },
    players: players.map((player) => {
      const row = after.get(player.id);
      return { ...player, guessed: false, betIn: false, score: row ? row.after : player.score, closest: (player.closest || 0) + (row && wins.some((slot) => slot.index === row.slot) ? 1 : 0) };
    }),
  };
}

// The eight 7-digit guesses (closest-lineup-big-table) while betting: three bets in.
function bigBet(state) {
  const betsIn = ["p2", "p5", "p7"];
  return { ...state, phase: "bet", phaseEndsAt: state.now + 18000, question: { ...state.question, betsIn }, players: state.players.map((player) => ({ ...player, betIn: betsIn.includes(player.id) })) };
}

// ... and with every chip down: stakes, pairs and splits across the line.
const BIG_BETS = [
  ["p1", [3, 3], 3, 3], ["p2", [3, 4], 0, null], ["p3", [0, 0], 1, 0], ["p4", [7, 3], 2, 3],
  ["p5", [4, 4], 0, null], ["p6", [5, 6], 0, null], ["p7", [2, 2], 1, 2], ["p8", [3, 1], 0, null],
];

function placeBets(state, bets) {
  const slots = clone(state.question.slots).map((slot) => ({ ...slot, chips: [] }));
  for (const [playerId, chips, stake, stakeSlot] of bets) {
    const counts = new Map();
    for (const slot of chips) counts.set(slot, (counts.get(slot) || 0) + 1);
    for (const [slot, count] of counts) slots[slot].chips.push({ playerId, count, stake: stake && slot === stakeSlot ? stake : 0 });
  }
  return { ...state, phase: "chips", phaseEndsAt: state.now + 3500, question: { ...state.question, slots, betsIn: undefined }, players: state.players.map((player) => ({ ...player, betIn: false })) };
}

// Players with points to stake (the 7-digit fixture starts everyone at 0).
const withScores = (state, scores) => ({ ...state, players: state.players.map((player, index) => ({ ...player, score: scores[index] ?? player.score })) });
const BIG_SCORES = [14, 11, 9, 9, 8, 6, 5, 3];

// A line-up of one (closest-reveal-oneslot-table before its answer).
function oneSlotLineup(state) {
  const { answer, fact, src, results, nobody, ...question } = state.question;
  return {
    ...state,
    phase: "lineup",
    phaseEndsAt: state.now + 3000,
    question: { ...question, slots: question.slots.map(({ off, result, ...slot }) => ({ ...slot, chips: undefined, result: null })) },
  };
}

// The last question (it pays double) at its reveal: the finished game one step back.
function lastReveal(state) {
  return { ...state, status: "playing", phase: "reveal", phaseEndsAt: state.now + 9000, standings: null, winnerIds: [], players: state.players.map(({ place, ...player }) => player) };
}

// Eight at the end: Ada third, Ben last.
function fullFinished(state) {
  const seated = seatTwoMore(state, [{ score: 25, closest: 1 }, { score: 2, closest: 0 }]);
  const standings = (state.standings || []).map((row) => (row.place >= 3 ? { ...row, place: row.place + 1 } : row));
  standings.splice(2, 0, { id: "p7", name: "Ada", place: 3, score: 25, closest: 1 });
  standings.push({ id: "p8", name: "Ben", place: 8, score: 2, closest: 0 });
  const placeOf = new Map(standings.map((row) => [row.id, row.place]));
  return { ...seated, standings, players: seated.players.map((player) => ({ ...player, place: placeOf.get(player.id) ?? player.place })) };
}

const bigChips = (state) => placeBets(withScores(state, BIG_SCORES), BIG_BETS);
const pileReveal = (state) => settle(state, 5200);

export const CLOSEST_SCENES = Object.freeze({
  // Eight seats.
  "closest-lobby-full-table": { from: "closest-lobby-table", make: (state) => seatTwoMore(state) },
  "closest-lobby-full-phone": { from: "closest-lobby-phone", make: (state) => seatTwoMore(state) },
  "closest-finished-full-table": { from: "closest-finished-table", make: fullFinished },
  "closest-finished-full-phone": { from: "closest-finished-phone", make: fullFinished },
  // Eight big 7-digit slots.
  "closest-bet-big-table": { from: "closest-lineup-big-table", make: bigBet },
  "closest-bet-big-phone": { from: "closest-lineup-big-table", make: (state) => asHostPhone(bigBet(state), { canBet: true, stakeMax: 0 }) },
  "closest-chips-big-table": { from: "closest-lineup-big-table", make: bigChips },
  "closest-reveal-big-table": { from: "closest-lineup-big-table", make: (state) => settle(bigChips(state), 4_350_000) },
  "closest-reveal-big-phone": { from: "closest-lineup-big-table", make: (state) => asHostPhone(settle(bigChips(state), 4_350_000), { chips: [3, 3], stake: 3, stakeSlot: 3 }) },
  "closest-lineup-big-phone": { from: "closest-lineup-big-table", make: (state) => asHostPhone(state, { stakeMax: 0 }) },
  // All eight players' chips on one slot.
  "closest-reveal-pile-table": { from: "closest-chips-pile-table", make: pileReveal },
  "closest-reveal-pile-phone": { from: "closest-chips-pile-table", make: (state) => asHostPhone(pileReveal(state), { chips: [4, 4], stake: 1, stakeSlot: 4 }) },
  "closest-chips-pile-phone": { from: "closest-chips-pile-table", make: (state) => asHostPhone(state, { chips: [4, 4], stake: 1, stakeSlot: 4 }) },
  // One slot.
  "closest-lineup-oneslot-table": { from: "closest-reveal-oneslot-table", make: oneSlotLineup },
  "closest-reveal-oneslot-phone": { from: "closest-reveal-oneslot-table", make: (state) => asHostPhone(state) },
  // A tie, nobody guessed.
  "closest-reveal-tie-phone": { from: "closest-reveal-tie-table", make: (state) => asHostPhone(state, { chips: [2, 2], stake: 1, stakeSlot: 2 }) },
  "closest-reveal-nobody-phone": { from: "closest-reveal-nobody-table", make: (state) => asHostPhone(state) },
  // The last (double) question.
  "closest-last-guess-phone": { from: "closest-last-guess-table", make: (state) => asHostPhone(state, { canGuess: true }) },
  "closest-reveal-last-table": { from: "closest-finished-table", make: lastReveal },
  "closest-reveal-last-phone": { from: "closest-finished-phone", make: lastReveal },
  // The phone at moments the fixtures show on the table only.
  "closest-intro-phone": { from: "closest-intro-table", make: (state) => asHostPhone(state) },
  "closest-chips-phone": { from: "closest-chips-table", make: (state) => asHostPhone(state, { chips: [2, 3], stake: 2, stakeSlot: 2 }) },
});

export async function buildClosestScene(name, load = fetchClosestFixture) {
  const scene = CLOSEST_SCENES[name];
  if (scene) {
    const fixture = await load(scene.from);
    return { state: scene.make(clone(fixture.state)), title: `${name} (made from ${scene.from})` };
  }
  try {
    const fixture = await load(name);
    return { state: fixture.state, title: fixture.meta?.description || name };
  } catch (error) {
    if (/-(table|phone)$/.test(name)) throw error;
    return buildClosestScene(`${name}-table`, load);
  }
}

// The clock a demo screen runs on: the fixture's server time, frozen.
export function demoClock(state) {
  const now = Number(state?.now);
  return Number.isFinite(now) && now > 0 ? now : Date.now();
}
