#!/usr/bin/env node
// Records parity fixtures for the Go AI port (internal/ai, fixtures_test.go).
//
//   node scripts/ai-fixtures.js            # rewrite testdata/ai/*.json
//   node scripts/ai-fixtures.js --check    # print counts only, write nothing
//   node scripts/ai-fixtures.js --games=3  # only the first 3 games (quick runs)
//
// Seeded games are played with the real JS engine (src/game.js) and the real AI
// (src/ai.js). Seats are AI players, plus (in some games) "human" seats driven by
// a seeded random policy so that the AI also faces offers, bids and trades it
// would never make itself. Fixtures are taken from:
//
//   natural  every AI decision of those games: chooseAiAction(game, playerId).
//            The decision is computed on JSON.parse(JSON.stringify(game)) and
//            checked against the live game, which shows that a JSON round trip
//            (what the Go server does) loses nothing the AI reads.
//   probe    variations of natural states made on JSON clones: synthetic
//            auctions, purchases, debts, jail turns, other cash levels, trade
//            offers to an AI (engine-validated via performAction offer_trade),
//            null cases, and quoteAiTrade requests (valid, boundary and invalid
//            ones that throw).
//   boundary pairs of states on either side of a decision boundary, found by
//            binary search in JS: the highest current bid an AI still raises
//            (pins auctionCeiling to the dollar) and the cash level at which a
//            buy / build / unmortgage / bail / trade decision changes (pins the
//            adaptive reserves and thresholds).
//
// Kinds of fixture:
//   choose   {id, source, phase, playerId, game, expected, turnPosAfter?}
//            expected = chooseAiAction(...) (null for JS null); turnPosAfter is
//            game.auction.turnPos after the call (auctionCurrentPlayerId mutates it).
//   accepts  {id, source, playerId, game, expected}  expected = aiAcceptsTrade(...)
//   quote    {id, source, game, cases:[{proposerId, request, expected|error}]}
//            expected = quoteAiTrade(...) result, error = thrown message.
//
// `game` is JSON.parse(JSON.stringify(game)) with keys the AI never reads
// removed to keep testdata/ai small (STRIPPED_KEYS, turn.movements).
// full-shape.json keeps the untouched object for a sample of decisions.
//
// Determinism: crypto.randomBytes is patched to a counter (ids become
// prefix_000000000001, ...) and Date.now is a constant. Dice come from a seeded
// mulberry32 PRNG. Policy and probe choices use separate seeded streams, so
// changing the probes never changes the natural games.
"use strict";

const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const assert = require("node:assert");

const CLOCK = 1_700_000_000_000;
const FAR_FUTURE = 9_000_000_000_000_000;
const MAX_STEPS = 2600;

let idCounter = 0;
crypto.randomBytes = (size) => {
  idCounter += 1;
  const buffer = Buffer.alloc(size);
  buffer.writeUIntBE(idCounter, size - 6, 6);
  return buffer;
};
Date.now = () => CLOCK;

const engine = require("../src/game");
const ai = require("../src/ai");
const { BOARD } = require("../src/board");

const OUT_DIR = path.join(__dirname, "..", "testdata", "ai");
const CHECK_ONLY = process.argv.includes("--check");
const GAME_LIMIT = Number((process.argv.find((arg) => arg.startsWith("--games=")) || "").slice(8)) || Infinity;
const AI_NAMES = ["Alpha", "Blue Whale", "Redwood", "North Star", "Skylark", "AI 5"];
const HUMAN_NAMES = ["Ana", "Ben", "Cleo", "Dev", "Eli", "Fay"];
const STRIPPED_KEYS = [
  "log", "decks", "openingOrder", "turnSummary", "turnSummaryBaselines",
  "lastMove", "lastLanding", "lastCard", "lastBankruptcy",
];
const OWNABLE = new Set(["property", "railroad", "utility"]);
const MAX_FILE_BYTES = 6 * 1024 * 1024;

// Game configurations: [seed, total seats, human seats].
const CONFIGS = [];
{
  const layouts = [
    [2, 0], [2, 0], [2, 1], [3, 0], [3, 1], [4, 0], [4, 1], [4, 2],
    [5, 0], [5, 1], [6, 0], [6, 2], [2, 0], [3, 0], [4, 0], [5, 2],
    [6, 1], [3, 2], [2, 1], [4, 0], [5, 0], [6, 0], [3, 1], [2, 0],
  ];
  layouts.forEach(([seats, humans], index) => CONFIGS.push({ seed: 1001 + index * 7919, seats, humans }));
}

// Caps per bucket (source:phase:expectedType), applied after collection. They
// are then scaled down together until the choose files fit CHOOSE_BYTE_BUDGET
// (keeping at least MIN_PER_BUCKET of every bucket), so testdata/ai stays
// under ~30 MB while every decision kind stays represented.
const DEFAULT_CAP = 160;
const BUCKET_CAPS = {
  "natural:awaiting_roll:roll": 350,
  "natural:turn_complete:end_turn": 250,
  "natural:auction:bid": 250,
  "boundary:auction:bid": 250,
  "boundary:auction:pass_auction": 250,
  "natural:awaiting_roll:build": 200,
  "natural:debt:mortgage": 180,
  "natural:debt:sell_building": 180,
  "probe:trade:accept_trade": 200,
  "natural:card_confirmation:confirm_card": 50,
  "natural:determining_order:null": 20,
  "natural:finished:null": 40,
};
const CHOOSE_BYTE_BUDGET = 21_000_000;
const MIN_PER_BUCKET = 40;
const ACCEPTS_CAP = 1500;
const QUOTE_STATE_CAP = 900;
const FULL_SHAPE_CAP = 60;

// ---------------------------------------------------------------- PRNG ----

function mulberry32(seed) {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randInt(r, low, high) {
  return low + Math.floor(r() * (high - low + 1));
}

function pick(r, items) {
  return items.length ? items[Math.floor(r() * items.length)] : undefined;
}

function sample(r, items, count) {
  const pool = [...items];
  const out = [];
  while (pool.length && out.length < count) out.push(pool.splice(Math.floor(r() * pool.length), 1)[0]);
  return out;
}

// ------------------------------------------------------------- helpers ----

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function slim(snapshot) {
  const out = clone(snapshot);
  for (const key of STRIPPED_KEYS) delete out[key];
  if (out.turn) delete out.turn.movements;
  return out;
}

function player(game, id) {
  return game.players.find((candidate) => candidate.id === id);
}

function active(game) {
  return game.players.filter((candidate) => !candidate.bankrupt);
}

function ownedBy(game, id) {
  return BOARD.filter((space) => game.properties[space.index]?.ownerId === id).map((space) => space.index);
}

function groupHasBuildings(game, space) {
  return space.type === "property"
    && BOARD.some((candidate) => candidate.group === space.group && game.properties[candidate.index].houses > 0);
}

function tradeable(game, id) {
  return ownedBy(game, id).filter((index) => !groupHasBuildings(game, BOARD[index]));
}

function unowned(game) {
  return BOARD.filter((space) => OWNABLE.has(space.type) && game.properties[space.index].ownerId === null)
    .map((space) => space.index);
}

// The actor rooms.js nextAiAction would ask (without its kind filter).
function nextActor(game) {
  if (game.phase === "trade") return game.trade?.targetId || null;
  if (game.phase === "auction") {
    const auction = game.auction;
    if (!auction) return null;
    const ids = auction.activeIds;
    if (ids.length === 1 && auction.bidderId === ids[0]) return null;
    for (let offset = 0; offset < ids.length; offset += 1) {
      const pos = (auction.turnPos + offset) % ids.length;
      if (ids[pos] !== auction.bidderId) return ids[pos];
    }
    return null;
  }
  return game.players[game.currentIndex]?.id || null;
}

// --------------------------------------------------------------- sinks ----

const chooseFixtures = [];
const acceptsFixtures = [];
const quoteFixtures = [];
const fullShapeFixtures = [];
const seen = new Set();
let fixtureSerial = 0;

function digest(value) {
  return crypto.createHash("sha1").update(JSON.stringify(value)).digest("hex");
}

function dedupeKey(kind, slimGame, extra) {
  const copy = { ...slimGame };
  delete copy.version;
  return digest([kind, copy, extra]);
}

// Evaluates chooseAiAction on a JSON copy of `game` and records it.
function recordChoose(game, playerId, source, tag) {
  const json = JSON.stringify(game);
  const work = JSON.parse(json);
  const expected = ai.chooseAiAction(work, playerId) ?? null;
  const stored = slim(JSON.parse(json));
  const key = dedupeKey("choose", stored, playerId);
  if (!seen.has(key)) {
    seen.add(key);
    const fixture = {
      id: `${tag}-c${(fixtureSerial += 1)}`,
      source,
      phase: stored.phase,
      playerId,
      game: stored,
      expected,
    };
    if (stored.phase === "auction" && work.auction) fixture.turnPosAfter = work.auction.turnPos;
    chooseFixtures.push(fixture);
    if (source === "natural" && fullShapeFixtures.length < 4000 && fixtureSerial % 7 === 0) {
      const full = { ...fixture, id: `${fixture.id}-full`, game: JSON.parse(json) };
      fullShapeFixtures.push(full);
    }
  }
  return expected;
}

function recordAccepts(game, playerId, source, tag) {
  const json = JSON.stringify(game);
  const expected = ai.aiAcceptsTrade(JSON.parse(json), playerId);
  const stored = slim(JSON.parse(json));
  const key = dedupeKey("accepts", stored, playerId);
  if (!seen.has(key)) {
    seen.add(key);
    acceptsFixtures.push({ id: `${tag}-a${(fixtureSerial += 1)}`, source, playerId, game: stored, expected });
  }
  return expected;
}

// Runs quoteAiTrade for each [proposerId, request] on one state.
function recordQuotes(game, requests, source, tag) {
  const json = JSON.stringify(game);
  const stored = slim(JSON.parse(json));
  const cases = [];
  const results = [];
  for (const [proposerId, rawRequest] of requests) {
    const request = JSON.parse(JSON.stringify(rawRequest ?? null));
    const entry = { proposerId, request };
    try {
      entry.expected = ai.quoteAiTrade(JSON.parse(json), proposerId, request);
    } catch (error) {
      entry.error = error.message;
    }
    results.push(entry);
    const key = dedupeKey("quote", stored, [proposerId, request]);
    if (seen.has(key)) continue;
    seen.add(key);
    cases.push(entry);
  }
  if (cases.length) quoteFixtures.push({ id: `${tag}-q${(fixtureSerial += 1)}`, source, game: stored, cases });
  return results;
}

// --------------------------------------------------------- human seats ----

// Asks the AI logic on behalf of a "human" seat (kind flipped temporarily).
// The human never makes AI-style trade offers (aiTradeAttempted forced on).
function aiLikeAction(game, id) {
  const seat = player(game, id);
  const work = clone(game);
  const workSeat = player(work, id);
  workSeat.kind = "ai";
  if (work.turn) work.turn.aiTradeAttempted = true;
  return ai.chooseAiAction(work, seat.id);
}

function randomTradeAction(game, proposer, r) {
  const targets = active(game).filter((candidate) => candidate.id !== proposer.id);
  const target = pick(r, targets.filter((candidate) => candidate.kind === "ai")) || pick(r, targets);
  if (!target) return null;
  const offer = sample(r, tradeable(game, proposer.id), randInt(r, 0, 2));
  const request = sample(r, tradeable(game, target.id), randInt(r, 0, 2));
  const offerCash = r() < 0.5 ? 0 : randInt(r, 0, Math.max(0, Math.min(proposer.cash, 600)));
  const requestCash = r() < 0.7 ? 0 : randInt(r, 0, Math.max(0, Math.min(target.cash, 400)));
  return {
    type: "offer_trade",
    targetId: target.id,
    offer: { cash: offerCash, properties: offer },
    request: { cash: requestCash, properties: request },
  };
}

function humanAction(game, id, r, state) {
  const seat = player(game, id);
  switch (game.phase) {
    case "card_confirmation":
      return { type: "confirm_card" };
    case "trade":
      return { type: r() < 0.5 ? "accept_trade" : "reject_trade" };
    case "auction": {
      const bid = game.auction.currentBid + randInt(r, 1, 45);
      if (r() < 0.45 && bid <= seat.cash) return { type: "bid", amount: bid };
      return { type: "pass_auction" };
    }
    case "awaiting_purchase": {
      const space = BOARD[game.pendingPurchase];
      if (seat.cash >= space.price && r() < 0.6) return { type: "buy" };
      return { type: "decline" };
    }
    case "debt":
      return aiLikeAction(game, id) || { type: "bankrupt" };
    case "awaiting_roll":
    case "turn_complete": {
      if (state.tradesThisTurn < 2 && r() < 0.18) {
        state.tradesThisTurn += 1;
        const action = randomTradeAction(game, seat, r);
        if (action) return action;
      }
      const action = aiLikeAction(game, id);
      if (action && action.type !== "offer_trade") return action;
      return { type: game.phase === "awaiting_roll" ? "roll" : "end_turn" };
    }
    default:
      return null;
  }
}

function fallbackAction(game) {
  switch (game.phase) {
    case "auction": return { type: "pass_auction" };
    case "awaiting_purchase": return { type: "decline" };
    case "trade": return { type: "reject_trade" };
    case "awaiting_roll": return { type: "roll" };
    case "turn_complete": return { type: "end_turn" };
    case "debt": return { type: "bankrupt" };
    case "card_confirmation": return { type: "confirm_card" };
    default: return null;
  }
}

// -------------------------------------------------------------- probes ----

// Makes the current seat an AI player (moving currentIndex if needed).
function withCurrentAi(game, r) {
  const current = game.players[game.currentIndex];
  if (current && current.kind === "ai" && !current.bankrupt) return current;
  const candidates = game.players
    .map((candidate, index) => ({ candidate, index }))
    .filter(({ candidate }) => candidate.kind === "ai" && !candidate.bankrupt);
  const chosen = pick(r, candidates);
  if (!chosen) return null;
  game.currentIndex = chosen.index;
  if (game.turn) game.turn.playerId = chosen.candidate.id;
  return chosen.candidate;
}

function clearPending(game) {
  game.pendingPurchase = null;
  game.auction = null;
  game.debt = null;
  game.trade = null;
  game.pendingCard = null;
}

// An unowned ownable square, freeing a building-free one if none is left.
function freeSquare(game, r) {
  const open = unowned(game);
  if (open.length && r() < 0.8) return pick(r, open);
  const candidates = BOARD.filter((space) => OWNABLE.has(space.type)
    && !groupHasBuildings(game, space)).map((space) => space.index);
  const index = pick(r, candidates.length ? candidates : open);
  if (index === undefined) return undefined;
  game.properties[index].ownerId = null;
  game.properties[index].mortgaged = false;
  return index;
}

function varyCash(seat, r) {
  const roll = r();
  if (roll < 0.25) seat.cash = randInt(r, 0, 400);
  else if (roll < 0.5) seat.cash = randInt(r, 150, 900);
  else if (roll < 0.75) seat.cash = randInt(r, 300, 2500);
  else seat.cash = Math.max(0, seat.cash + randInt(r, -300, 300));
}

function probeAuction(base, r, tag) {
  const game = clone(base);
  clearPending(game);
  const spaceIndex = freeSquare(game, r);
  if (spaceIndex === undefined) return;
  const players = active(game);
  if (players.length < 2) return;
  const start = randInt(r, 0, game.players.length - 1);
  let ids = [];
  for (let offset = 1; offset <= game.players.length; offset += 1) {
    const candidate = game.players[(start + offset) % game.players.length];
    if (!candidate.bankrupt) ids.push(candidate.id);
  }
  if (ids.length > 2 && r() < 0.35) ids = sample(r, ids, randInt(r, 2, ids.length)).sort((a, b) => ids.indexOf(a) - ids.indexOf(b));
  const price = BOARD[spaceIndex].price;
  const maxCash = Math.max(...players.map((candidate) => candidate.cash));
  const roll = r();
  let currentBid = 0;
  if (roll < 0.25) currentBid = 0;
  else if (roll < 0.7) currentBid = randInt(r, 1, Math.round(price * 1.6));
  else currentBid = randInt(r, 1, Math.max(1, Math.round(maxCash * 1.1)));
  if (r() < 0.5) currentBid = Math.round(currentBid / 10) * 10;
  for (const candidate of game.players) if (r() < 0.3) varyCash(candidate, r);
  game.auction = {
    spaceIndex,
    activeIds: ids,
    turnPos: randInt(r, 0, ids.length - 1),
    currentBid,
    bidderId: currentBid > 0 ? pick(r, ids) : null,
    returnPhase: r() < 0.5 ? "turn_complete" : "awaiting_roll",
  };
  game.phase = "auction";
  const due = engine.auctionCurrentPlayerId(clone(game));
  const dueSeat = due && player(game, due);
  if (dueSeat?.kind === "ai") recordChoose(game, due, "probe", tag);
  const other = pick(r, game.players.filter((candidate) => candidate.kind === "ai" && candidate.id !== due));
  if (other && r() < 0.15) recordChoose(game, other.id, "probe", tag);
}

function probePurchase(base, r, tag) {
  const game = clone(base);
  clearPending(game);
  const seat = withCurrentAi(game, r);
  if (!seat) return;
  const index = freeSquare(game, r);
  if (index === undefined) return;
  const price = BOARD[index].price;
  const roll = r();
  if (roll < 0.35) seat.cash = randInt(r, 0, price + 40);
  else if (roll < 0.6) varyCash(seat, r);
  game.phase = "awaiting_purchase";
  game.pendingPurchase = index;
  recordChoose(game, seat.id, "probe", tag);
}

function probeDebt(base, r, tag) {
  const game = clone(base);
  clearPending(game);
  const seat = withCurrentAi(game, r);
  if (!seat) return;
  const creditor = pick(r, active(game).filter((candidate) => candidate.id !== seat.id));
  const roll = r();
  const amount = roll < 0.2 ? randInt(r, 1, Math.max(1, seat.cash)) : seat.cash + randInt(r, 1, roll < 0.7 ? 400 : 2000);
  game.phase = "debt";
  game.debt = {
    payerId: seat.id,
    amount,
    creditorId: r() < 0.7 && creditor ? creditor.id : null,
    reason: "probe charge",
    after: { kind: "phase", phase: "turn_complete" },
  };
  recordChoose(game, seat.id, "probe", tag);
}

function probeJail(base, r, tag) {
  const game = clone(base);
  clearPending(game);
  const seat = withCurrentAi(game, r);
  if (!seat || !game.turn) return;
  seat.inJail = true;
  seat.position = 10;
  seat.jailTurns = randInt(r, 0, 2);
  seat.jailCards = r() < 0.3 ? 1 : 0;
  if (r() < 0.5) varyCash(seat, r);
  game.phase = "awaiting_roll";
  game.turn.aiTradeAttempted = r() < 0.75;
  recordChoose(game, seat.id, "probe", tag);
}

function probeManage(base, r, tag) {
  const game = clone(base);
  clearPending(game);
  const seat = withCurrentAi(game, r);
  if (!seat || !game.turn) return;
  varyCash(seat, r);
  game.phase = r() < 0.5 ? "awaiting_roll" : "turn_complete";
  game.turn.aiTradeAttempted = r() < 0.3;
  recordChoose(game, seat.id, "probe", tag);
}

// States that favour the trade search: spare cash, cooldowns sometimes cleared.
function probeTradeSearch(base, r, tag) {
  const game = clone(base);
  clearPending(game);
  const seat = withCurrentAi(game, r);
  if (!seat || !game.turn) return;
  if (r() < 0.7) seat.cash = randInt(r, 500, 3200);
  if (r() < 0.5) game.aiTradeCooldowns = {};
  for (const candidate of game.players) if (candidate.id !== seat.id && r() < 0.3) varyCash(candidate, r);
  game.phase = r() < 0.5 ? "awaiting_roll" : "turn_complete";
  game.turn.aiTradeAttempted = false;
  recordChoose(game, seat.id, "probe", tag);
}

// Null cases: humans, bankrupt seats, unknown ids, seats that are not due.
function probeNull(base, r, tag) {
  const game = clone(base);
  const roll = r();
  let id;
  if (roll < 0.3) id = pick(r, game.players.filter((candidate) => candidate.kind !== "ai"))?.id;
  else if (roll < 0.5) id = pick(r, game.players.filter((candidate) => candidate.bankrupt))?.id;
  else if (roll < 0.6) id = "ai_ffffffffffff";
  else id = pick(r, game.players.filter((candidate, index) => candidate.kind === "ai" && index !== game.currentIndex))?.id;
  if (id) recordChoose(game, id, "probe", tag);
}

function randomQuoteRequest(game, proposer, r) {
  const others = game.players.filter((candidate) => candidate.id !== proposer.id);
  const aiTargets = others.filter((candidate) => candidate.kind === "ai" && !candidate.bankrupt);
  const roll = r();
  let target;
  let targetId;
  if (roll < 0.82) target = pick(r, aiTargets) || pick(r, others);
  else if (roll < 0.87) target = pick(r, others.filter((candidate) => candidate.kind !== "ai")) || pick(r, others);
  else if (roll < 0.9) target = proposer;
  else if (roll < 0.93) target = pick(r, others.filter((candidate) => candidate.bankrupt)) || pick(r, others);
  if (target) targetId = target.id;
  else if (roll < 0.96) targetId = "ai_ffffffffffff";
  else targetId = r() < 0.5 ? 5 : null;

  const ownerOfRequest = target || pick(r, others) || proposer;
  const offerPool = r() < 0.85 ? tradeable(game, proposer.id) : ownedBy(game, proposer.id);
  const requestPool = r() < 0.85 ? tradeable(game, ownerOfRequest.id) : ownedBy(game, ownerOfRequest.id);
  const sizeRoll = () => {
    const x = r();
    return x < 0.3 ? 0 : x < 0.75 ? 1 : x < 0.93 ? 2 : 3;
  };
  let offer = sample(r, offerPool, sizeRoll());
  let request = sample(r, requestPool, sizeRoll());
  if (!offer.length && !request.length && r() < 0.85) {
    if (requestPool.length) request = sample(r, requestPool, 1);
    else if (offerPool.length) offer = sample(r, offerPool, 1);
  }

  const mutate = (list) => {
    const x = r();
    if (x < 0.03) return [...list, pick(r, [40, -1, 0, 2, 7, 10, 5.5, 1e21, "abc", true, {}, [1, 2]])];
    if (x < 0.06 && list.length) return list.map((index) => (r() < 0.5 ? String(index) : index));
    if (x < 0.08 && list.length) return [...list, list[0]];
    if (x < 0.09 && list.length) return list.map((index) => [index]);
    if (x < 0.1 && list.length) return list.map((index) => ` ${index} `);
    if (x < 0.11) return pick(r, ["5", 5, {}, null, undefined, "[1]"]);
    if (x < 0.14) {
      const foreign = BOARD.filter((space) => OWNABLE.has(space.type)
        && game.properties[space.index].ownerId !== proposer.id
        && game.properties[space.index].ownerId !== ownerOfRequest.id).map((space) => space.index);
      if (foreign.length) return [...list, pick(r, foreign)];
    }
    return list;
  };
  const request_ = { targetId, offerProperties: mutate(offer), requestProperties: mutate(request) };
  if (r() < 0.02) request_.token = "ignored";
  if (r() < 0.01) delete request_.offerProperties;
  return request_;
}

function probeQuotes(base, r, tag, count) {
  const game = clone(base);
  if (!["awaiting_roll", "turn_complete"].includes(game.phase) && game.status === "playing") {
    if (r() > 0.2) return [];
    count = 1; // a few "Trading is not available during this phase" cases
  }
  const current = game.players[game.currentIndex];
  const requests = [];
  for (let index = 0; index < count; index += 1) {
    let proposer = current;
    if (!proposer || r() < 0.05) proposer = pick(r, game.players);
    if (!proposer) return [];
    const proposerId = r() < 0.01 ? "player_ffffffffffff" : proposer.id;
    requests.push([proposerId, r() < 0.01 ? null : randomQuoteRequest(game, proposer, r)]);
  }
  return recordQuotes(game, requests, "probe", tag);
}

// Engine-validated trade offers to an AI, plus the quote boundaries.
function probeOffers(base, r, tag) {
  if (!["awaiting_roll", "turn_complete"].includes(base.phase) || base.trade) return;
  const proposer = base.players[base.currentIndex];
  if (!proposer || proposer.bankrupt) return;
  const attempts = [];
  const target = pick(r, active(base).filter((candidate) => candidate.kind === "ai" && candidate.id !== proposer.id));
  if (!target) return;

  if (r() < 0.6) {
    // Boundary offers around a quote.
    const offer = sample(r, tradeable(base, proposer.id), randInt(r, 0, 2));
    const request = sample(r, tradeable(base, target.id), randInt(r, offer.length ? 0 : 1, 2));
    const [result] = recordQuotes(base, [[proposer.id, { targetId: target.id, offerProperties: offer, requestProperties: request }]], "probe", tag);
    const quote = result?.expected;
    if (quote) {
      if (quote.offerCash > 0) {
        attempts.push([quote.offerCash, 0], [quote.offerCash - 1, 0], [quote.offerCash + 1, 0]);
      } else {
        attempts.push([0, quote.requestCash], [0, quote.requestCash + 1]);
        if (quote.requestCash > 0) attempts.push([0, quote.requestCash - 1]);
      }
      for (const [offerCash, requestCash] of attempts) {
        tryOffer(base, proposer, target, { cash: offerCash, properties: offer }, { cash: requestCash, properties: request }, r, tag);
      }
      return;
    }
  }
  const action = randomTradeAction(base, proposer, r);
  if (action && action.targetId === target.id) tryOffer(base, proposer, target, action.offer, action.request, r, tag);
  else if (action) tryOffer(base, proposer, player(base, action.targetId), action.offer, action.request, r, tag);
}

function tryOffer(base, proposer, target, offer, request, r, tag) {
  if (!target) return;
  const game = clone(base);
  try {
    engine.performAction(game, proposer.id, { type: "offer_trade", targetId: target.id, offer, request });
  } catch {
    return;
  }
  if (game.phase !== "trade") return;
  if (target.kind === "ai") {
    recordAccepts(game, target.id, "probe", tag);
    recordChoose(game, target.id, "probe", tag);
  }
  if (r() < 0.2) {
    const other = pick(r, game.players.filter((candidate) => candidate.id !== target.id && candidate.id !== proposer.id));
    if (other) recordAccepts(game, other.id, "probe", tag);
  }
}

// ------------------------------------------------------ boundary probes ----

function decisionAt(game, seatId) {
  return JSON.stringify(ai.chooseAiAction(clone(game), seatId) ?? null);
}

// Records the states on both sides of the cash level where seatId's decision
// first changes (searching [0, 4000]).
function cashBoundary(game, seatId, tag) {
  const at = (cash) => {
    const copy = clone(game);
    player(copy, seatId).cash = cash;
    return copy;
  };
  const base = decisionAt(at(0), seatId);
  let low = 0;
  let high = 4000;
  if (decisionAt(at(high), seatId) === base) return;
  while (high - low > 1) {
    const middle = (low + high) >> 1;
    if (decisionAt(at(middle), seatId) === base) low = middle;
    else high = middle;
  }
  recordChoose(at(low), seatId, "boundary", tag);
  recordChoose(at(high), seatId, "boundary", tag);
}

// For an auction state, records the highest current bid that dueId still
// raises and the next one (where it passes).
function auctionBoundary(base, tag) {
  const game = clone(base);
  const auction = game.auction;
  if (!auction || auction.activeIds.length < 2) return;
  const dueId = engine.auctionCurrentPlayerId(clone(game));
  if (!dueId || player(game, dueId)?.kind !== "ai") return;
  auction.turnPos = auction.activeIds.indexOf(dueId);
  auction.bidderId = auction.activeIds.find((id) => id !== dueId);
  const at = (bid) => {
    const copy = clone(game);
    copy.auction.currentBid = bid;
    return copy;
  };
  const raises = (bid) => ai.chooseAiAction(at(bid), dueId)?.type === "bid";
  if (!raises(1)) {
    recordChoose(at(1), dueId, "boundary", tag);
    return;
  }
  let low = 1;
  let high = player(game, dueId).cash + 20;
  while (high - low > 1) {
    const middle = (low + high) >> 1;
    if (raises(middle)) low = middle;
    else high = middle;
  }
  recordChoose(at(low), dueId, "boundary", tag);
  recordChoose(at(high), dueId, "boundary", tag);
}

function probeBoundary(base, r, tag) {
  const game = clone(base);
  clearPending(game);
  const seat = withCurrentAi(game, r);
  if (!seat || !game.turn) return;
  const roll = r();
  if (roll < 0.3) {
    const index = freeSquare(game, r);
    if (index === undefined) return;
    game.phase = "awaiting_purchase";
    game.pendingPurchase = index;
  } else if (roll < 0.5) {
    seat.inJail = true;
    seat.position = 10;
    seat.jailTurns = randInt(r, 0, 2);
    seat.jailCards = r() < 0.2 ? 1 : 0;
    game.phase = "awaiting_roll";
    game.turn.aiTradeAttempted = true;
  } else {
    game.phase = r() < 0.5 ? "awaiting_roll" : "turn_complete";
    game.turn.aiTradeAttempted = r() < 0.85;
  }
  cashBoundary(game, seat.id, tag);
}

function probeAuctionBoundary(base, r, tag) {
  const game = clone(base);
  clearPending(game);
  const spaceIndex = freeSquare(game, r);
  if (spaceIndex === undefined) return;
  const ids = active(game).map((candidate) => candidate.id);
  if (ids.length < 2) return;
  for (const candidate of game.players) if (r() < 0.3) varyCash(candidate, r);
  game.auction = {
    spaceIndex,
    activeIds: ids,
    turnPos: randInt(r, 0, ids.length - 1),
    currentBid: 0,
    bidderId: null,
    returnPhase: "turn_complete",
  };
  game.phase = "auction";
  auctionBoundary(game, tag);
}

const PROBES = [
  [probeAuction, 0.2],
  [probePurchase, 0.14],
  [probeDebt, 0.12],
  [probeJail, 0.08],
  [probeManage, 0.1],
  [probeOffers, 0.2],
  [probeNull, 0.04],
  [probeTradeSearch, 0.12],
  [probeBoundary, 0.22],
  [probeAuctionBoundary, 0.14],
];

function runProbes(game, r, tag) {
  const savedIdCounter = idCounter; // probes must not shift ids in the natural game
  if (r() < 0.55) {
    let x = r() * PROBES.reduce((sum, [, weight]) => sum + weight, 0);
    for (const [probe, weight] of PROBES) {
      x -= weight;
      if (x <= 0) {
        probe(game, r, tag);
        break;
      }
    }
  }
  if (r() < 0.25) probeQuotes(game, r, tag, randInt(r, 2, 5));
  if (game.phase === "auction" && r() < 0.5) auctionBoundary(game, tag);
  idCounter = savedIdCounter;
}

// ---------------------------------------------------------------- play ----

function playGame(config, gameIndex) {
  idCounter = 0;
  const rng = mulberry32(config.seed);
  const policy = mulberry32(config.seed ^ 0x5bd1e995);
  const probe = mulberry32(config.seed ^ 0x27d4eb2f);
  const tag = `g${gameIndex}`;
  const game = engine.createGame("AIFIX");
  const humanSeats = new Set(sample(policy, [...Array(config.seats).keys()], config.humans));
  for (let seat = 0; seat < config.seats; seat += 1) {
    const human = humanSeats.has(seat);
    engine.addPlayer(game, human ? HUMAN_NAMES[seat] : AI_NAMES[seat], human ? "human" : "ai");
  }
  engine.startGame(game, game.hostId, rng);
  for (const candidate of game.players) {
    if (candidate.kind === "ai") recordChoose(game, candidate.id, "natural", tag); // null: determining_order
  }
  let guard = 0;
  while (game.phase === "determining_order" && guard++ < 200) {
    engine.performAction(game, game.openingOrder.pendingIds[0], { type: "roll_for_order" }, rng);
  }
  if (game.players[0].kind === "ai") recordChoose(game, game.players[0].id, "natural", tag); // null: order_countdown
  engine.completeOpeningOrder(game, FAR_FUTURE);

  const humanState = { turn: null, tradesThisTurn: 0 };
  const stats = { steps: 0 };
  while (game.status === "playing" && stats.steps < MAX_STEPS) {
    const actorId = nextActor(game);
    if (!actorId) throw new Error(`${tag}: no actor in phase ${game.phase}`);
    const actor = player(game, actorId);
    if (humanState.turn !== game.turn) {
      humanState.turn = game.turn; // beginTurn creates a new turn object
      humanState.tradesThisTurn = 0;
    }

    let action;
    if (actor.kind === "ai") {
      if (stats.steps % 2 === 0 || game.phase !== "awaiting_roll") runProbes(game, probe, `${tag}s${stats.steps}`);
      const expected = recordChoose(game, actorId, "natural", `${tag}s${stats.steps}`);
      if (stats.steps % 4 === 0) {
        // The live object must give the same answer as its JSON copy.
        assert.deepStrictEqual(ai.chooseAiAction(game, actorId) ?? null, expected, `${tag}: JSON round trip changed the AI decision`);
      }
      action = expected;
      if (!action) throw new Error(`${tag}: AI returned null in phase ${game.phase}`);
    } else {
      if (probe() < 0.3) runProbes(game, probe, `${tag}s${stats.steps}`);
      action = humanAction(game, actorId, policy, humanState) || fallbackAction(game);
    }
    try {
      engine.performAction(game, actorId, action, rng);
    } catch (error) {
      if (actor.kind === "ai") throw new Error(`${tag}: AI action ${JSON.stringify(action)} failed: ${error.message}`);
      engine.performAction(game, actorId, fallbackAction(game), rng);
    }
    stats.steps += 1;
  }
  if (game.status === "finished") {
    for (const candidate of game.players) {
      if (candidate.kind === "ai") recordChoose(game, candidate.id, "natural", `${tag}end`);
    }
    probeQuotes(game, probe, `${tag}end`, 2);
  }
  return { status: game.status, steps: stats.steps, winner: game.winnerId };
}

// --------------------------------------------------------------- output ----

function bucketOf(fixture) {
  const type = fixture.expected ? fixture.expected.type : "null";
  const phase = fixture.expected === null && fixture.source === "probe" ? "other" : fixture.phase;
  return `${fixture.source}:${phase}:${type}`;
}

// Keeps at most `cap` items, evenly spread over the input order.
function capEvenly(items, cap) {
  if (items.length <= cap) return items;
  const out = [];
  for (let index = 0; index < cap; index += 1) out.push(items[Math.floor((index * items.length) / cap)]);
  return out;
}

function capByBucket(fixtures) {
  const buckets = new Map();
  const sizes = new Map();
  for (const fixture of fixtures) {
    const key = bucketOf(fixture);
    if (!buckets.has(key)) buckets.set(key, []);
    buckets.get(key).push(fixture);
    sizes.set(fixture, JSON.stringify(fixture).length + 2);
  }
  const select = (scale) => {
    const kept = new Set();
    let bytes = 0;
    for (const [key, list] of buckets) {
      const cap = Math.max(MIN_PER_BUCKET, Math.floor((BUCKET_CAPS[key] ?? DEFAULT_CAP) * scale));
      for (const fixture of capEvenly(list, cap)) {
        kept.add(fixture);
        bytes += sizes.get(fixture);
      }
    }
    return { kept, bytes };
  };
  let scale = 1;
  let choice = select(scale);
  while (choice.bytes > CHOOSE_BYTE_BUDGET && scale > 0.05) {
    scale -= 0.05;
    choice = select(scale);
  }
  console.log(`bucket cap scale ${scale.toFixed(2)} (${(choice.bytes / 1e6).toFixed(1)} MB of choose fixtures)`);
  return { kept: fixtures.filter((fixture) => choice.kept.has(fixture)), buckets };
}

function writeSet(baseName, kind, fixtures) {
  const files = [];
  let chunk = [];
  let bytes = 0;
  const flush = () => {
    if (!chunk.length) return;
    const name = `${baseName}-${String(files.length + 1).padStart(2, "0")}.json`;
    const body = `{"generatedBy":"scripts/ai-fixtures.js","kind":"${kind}","count":${chunk.length},"fixtures":[\n${chunk.join(",\n")}\n]}\n`;
    if (!CHECK_ONLY) fs.writeFileSync(path.join(OUT_DIR, name), body);
    files.push({ name, count: chunk.length, bytes: body.length });
    chunk = [];
    bytes = 0;
  };
  for (const fixture of fixtures) {
    const line = JSON.stringify(fixture);
    if (bytes + line.length > MAX_FILE_BYTES) flush();
    chunk.push(line);
    bytes += line.length + 2;
  }
  flush();
  return files;
}

function main() {
  const started = process.hrtime.bigint();
  CONFIGS.slice(0, GAME_LIMIT).forEach((config, index) => {
    const t0 = process.hrtime.bigint();
    const result = playGame(config, index);
    console.error(`game ${index} seed=${config.seed} seats=${config.seats} humans=${config.humans}: ${result.status} after ${result.steps} steps (${(Number(process.hrtime.bigint() - t0) / 1e9).toFixed(1)} s); fixtures so far choose=${chooseFixtures.length} accepts=${acceptsFixtures.length} quoteStates=${quoteFixtures.length}`);
  });

  const { kept, buckets } = capByBucket(chooseFixtures);
  const accepts = capEvenly(acceptsFixtures, ACCEPTS_CAP);
  const quotes = capEvenly(quoteFixtures, QUOTE_STATE_CAP);
  const full = capEvenly(fullShapeFixtures, FULL_SHAPE_CAP);

  console.log("choose buckets (kept/collected):");
  for (const key of [...buckets.keys()].sort()) {
    const keptCount = kept.filter((fixture) => bucketOf(fixture) === key).length;
    console.log(`  ${key.padEnd(48)} ${String(keptCount).padStart(5)} / ${buckets.get(key).length}`);
  }
  const quoteCases = quotes.reduce((sum, fixture) => sum + fixture.cases.length, 0);
  const quoteErrors = {};
  let quoteOk = 0;
  for (const fixture of quotes) {
    for (const entry of fixture.cases) {
      if (entry.error) quoteErrors[entry.error.replace(/^.* (no longer belongs to|belongs to a color group).*/, "<space> $1 ...")] = (quoteErrors[entry.error.replace(/^.* (no longer belongs to|belongs to a color group).*/, "<space> $1 ...")] || 0) + 1;
      else quoteOk += 1;
    }
  }
  console.log(`accepts: ${accepts.length} (true=${accepts.filter((fixture) => fixture.expected).length})`);
  console.log(`quotes: ${quotes.length} states, ${quoteCases} cases (ok=${quoteOk})`);
  for (const [message, count] of Object.entries(quoteErrors)) console.log(`  error ${count}x ${message}`);

  if (!CHECK_ONLY) {
    fs.mkdirSync(OUT_DIR, { recursive: true });
    for (const name of fs.readdirSync(OUT_DIR)) {
      if (name.endsWith(".json")) fs.unlinkSync(path.join(OUT_DIR, name));
    }
  }
  const byPhase = new Map();
  for (const fixture of kept) {
    const phase = fixture.phase;
    if (!byPhase.has(phase)) byPhase.set(phase, []);
    byPhase.get(phase).push(fixture);
  }
  const files = [];
  for (const [phase, list] of [...byPhase].sort()) files.push(...writeSet(`choose-${phase.replace(/_/g, "-")}`, "choose", list));
  files.push(...writeSet("accepts", "accepts", accepts));
  files.push(...writeSet("quotes", "quote", quotes));
  files.push(...writeSet("full-shape", "choose", full));
  let total = 0;
  for (const file of files) {
    total += file.bytes;
    console.log(`  ${file.name.padEnd(36)} ${String(file.count).padStart(5)} fixtures ${(file.bytes / 1e6).toFixed(2)} MB`);
  }
  console.log(`choose fixtures: ${kept.length} (+${full.length} full-shape); total ${(total / 1e6).toFixed(2)} MB in ${files.length} files; ${(Number(process.hrtime.bigint() - started) / 1e9).toFixed(0)} s${CHECK_ONLY ? " (check only, nothing written)" : ""}`);
}

main();
