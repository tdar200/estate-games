#!/usr/bin/env node
// Generates web/fixtures/<name>.json from real engine states.
//
// Every fixture is produced by playing the JS engine (src/game.js) with a seeded RNG, letting the
// engine's own AI (src/ai.js) make every decision, and stopping the first time a scenario appears.
// Nothing about the game state is hand-edited: only the additive API.md §3 fields (input, tokenKind,
// online, viewerRole, localSeats) and the player colours (which the Go server takes from each
// device profile) are added afterwards.
//
//   node scripts/fixtures.js            # writes web/fixtures/*.json
//   node scripts/fixtures.js --list     # prints what each fixture contains
//
// Output: { state, viewer, meta } where state = publicState(...) for that viewer.

const fs = require("node:fs");
const path = require("node:path");
const nodeCrypto = require("node:crypto");

// Deterministic ids (the engine uses crypto.randomBytes for ids) and a fake clock, so the files
// are stable between runs and diffs stay readable.
let idCounter = 0;
nodeCrypto.randomBytes = (size) => {
  idCounter += 1;
  return Buffer.from(String(idCounter).padStart(size * 2, "0").slice(-size * 2), "hex");
};
let clock = Date.UTC(2026, 10, 1, 19, 0, 0);
Date.now = () => clock;

const { createGame, addPlayer, performAction, publicState, completeOpeningOrder, auctionCurrentPlayerId } = require("../src/game");
const { chooseAiAction } = require("../src/ai");

const OUT_DIR = path.join(__dirname, "..", "web", "fixtures");
const CODE = "NXGNT";

const ROSTER = [
  { name: "Robin", kind: "human", input: "phone", tokenKind: "rocket", color: "#2F6FE0" },
  { name: "Alpha", kind: "ai", input: "ai", tokenKind: "teapot", color: "#D9343A" },
  { name: "Sam", kind: "human", input: "phone", tokenKind: "bicycle", color: "#E8A21A" },
  { name: "Maya", kind: "human", input: "controller", tokenKind: "lighthouse", color: "#1E9E8F" },
];

function mulberry32(seed) {
  let a = seed >>> 0;
  return function rng() {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function newLobby() {
  idCounter = 0;
  clock = Date.UTC(2026, 10, 1, 19, 0, 0);
  const game = createGame(CODE);
  for (const entry of ROSTER) {
    const player = addPlayer(game, entry.name, entry.kind);
    player.color = entry.color;
    clock += 4_000;
  }
  return game;
}

const byName = (game, name) => game.players.find((player) => player.name === name);
const ownedCount = (game) => Object.values(game.properties).filter((state) => state.ownerId).length;
const houseCount = (game) => Object.values(game.properties).reduce((sum, state) => sum + state.houses, 0);

// Same scheduling as src/rooms.js nextAiAction, but every seat is driven by the engine's AI.
function nextAction(game) {
  if (game.status !== "playing") return null;
  if (game.phase === "determining_order") {
    const playerId = game.openingOrder?.pendingIds[0];
    return playerId ? { playerId, action: { type: "roll_for_order" } } : null;
  }
  let playerId = null;
  if (game.phase === "trade") playerId = game.trade?.targetId || null;
  else if (game.phase === "auction") playerId = auctionCurrentPlayerId(game);
  else playerId = game.players[game.currentIndex]?.id;
  if (!playerId) return null;
  const action = chooseAiAction(game, playerId);
  return action ? { playerId, action } : null;
}

// While simulating, every seat is marked "ai" so chooseAiAction will decide for it.
function withAllAi(game, fn) {
  const kinds = game.players.map((player) => player.kind);
  game.players.forEach((player) => { player.kind = "ai"; });
  try {
    return fn();
  } finally {
    game.players.forEach((player, index) => { player.kind = kinds[index]; });
  }
}

function step(game, rng) {
  clock += 1_400;
  if (game.phase === "order_countdown") {
    completeOpeningOrder(game, Number.POSITIVE_INFINITY);
    return { playerId: null, action: { type: "countdown" } };
  }
  return withAllAi(game, () => {
    const next = nextAction(game);
    if (!next) return null;
    performAction(game, next.playerId, next.action, rng);
    return next;
  });
}

// ---------------------------------------------------------------------------------------------
// Scenarios. `when(game, last)` is checked after every engine step; the first match is kept.
// `viewer` names whose publicState is written ("table" = the PC table screen).

const T = (game) => byName(game, "Robin");
const isCurrent = (game, name) => game.players[game.currentIndex]?.name === name;

const SCENARIOS = [
  {
    name: "order",
    viewer: "Robin",
    description: "Rolling for turn order; Alpha and Sam have rolled, Robin has not",
    when: (game) => game.phase === "determining_order"
      && game.openingOrder.pendingIds.includes(T(game).id)
      && game.openingOrder.entries.filter((entry) => entry.rolls.length).length >= 2,
  },
  {
    name: "order-countdown",
    viewer: "Robin",
    description: "Turn order settled; three-second countdown before the first turn",
    when: (game) => game.phase === "order_countdown",
  },
  {
    name: "roll",
    viewer: "Robin",
    description: "Robin's turn, mid-game, waiting to roll",
    when: (game) => game.phase === "awaiting_roll" && isCurrent(game, "Robin") && !T(game).inJail
      && game.turn.doublesCount === 0 && ownedCount(game) >= 16 && T(game).cash > 300,
  },
  {
    name: "doubles",
    viewer: "Robin",
    description: "Robin rolled doubles and rolls again",
    when: (game) => game.phase === "awaiting_roll" && isCurrent(game, "Robin") && !T(game).inJail
      && game.turn.doublesCount >= 1 && ownedCount(game) >= 10,
  },
  {
    name: "buy",
    viewer: "Robin",
    description: "Robin landed on an unowned street and must buy or auction",
    when: (game) => game.phase === "awaiting_purchase" && isCurrent(game, "Robin")
      && ownedCount(game) >= 10
      && T(game).cash >= 400,
  },
  {
    name: "auction",
    viewer: "Robin",
    description: "Live auction; someone has bid and it is Robin's turn to bid",
    when: (game) => game.phase === "auction" && auctionCurrentPlayerId(game) === T(game).id
      && game.auction.currentBid > 0 && game.auction.bidderId !== T(game).id,
  },
  {
    name: "rent",
    viewer: "Robin",
    description: "Robin paid rent this turn and can end the turn",
    when: (game) => game.phase === "turn_complete" && isCurrent(game, "Robin")
      && game.lastLanding?.kind === "rent" && game.lastLanding.playerId === T(game).id
      && game.lastLanding.amount >= 40,
  },
  {
    name: "turn-done",
    viewer: "Robin",
    description: "Robin bought a property this turn and can end the turn",
    when: (game, last) => game.phase === "turn_complete" && isCurrent(game, "Robin")
      && last?.action?.type === "buy" && ownedCount(game) >= 8,
  },
  {
    name: "card",
    viewer: "Robin",
    description: "Robin drew a Headlines card that moves the token and must confirm it",
    when: (game) => game.phase === "card_confirmation" && game.pendingCard?.playerId === T(game).id
      && /Advance|nearest|Move back/.test(game.pendingCard.text),
  },
  {
    name: "jail",
    viewer: "Robin",
    description: "Robin is in jail at the start of a turn",
    when: (game) => game.phase === "awaiting_roll" && isCurrent(game, "Robin") && T(game).inJail
      && T(game).jailTurns >= 1,
  },
  {
    name: "debt",
    viewer: "Robin",
    description: "Robin owes more than their cash and must raise money or go bankrupt",
    when: (game) => game.phase === "debt" && game.debt?.payerId === T(game).id
      && Object.values(game.properties).some((state) => state.ownerId === T(game).id),
  },
  {
    name: "trade",
    viewer: "Robin",
    description: "Another player offered Robin a trade",
    when: (game) => game.phase === "trade" && game.trade?.targetId === T(game).id,
  },
  {
    name: "trade-sent",
    viewer: "Robin",
    description: "Robin offered a trade and is waiting for the answer",
    when: (game) => game.phase === "trade" && game.trade?.proposerId === T(game).id,
  },
  {
    name: "waiting",
    viewer: "Robin",
    description: "Alpha's turn; Robin watches from the phone",
    when: (game) => game.phase === "turn_complete" && isCurrent(game, "Alpha") && game.turn?.dice
      && ownedCount(game) >= 14 && !T(game).bankrupt,
  },
  {
    name: "pad-turn",
    viewer: "table",
    description: "PC table: Maya (controller seat) is about to roll",
    when: (game) => game.phase === "awaiting_roll" && isCurrent(game, "Maya") && !byName(game, "Maya").inJail
      && game.turn.doublesCount === 0 && ownedCount(game) >= 16,
  },
  {
    name: "phone-turn",
    viewer: "table",
    description: "PC table: Robin (phone) is deciding whether to buy",
    when: (game) => game.phase === "awaiting_purchase" && isCurrent(game, "Robin") && ownedCount(game) >= 10,
  },
  {
    name: "table-auction",
    viewer: "table",
    description: "PC table: live auction with bids",
    when: (game) => game.phase === "auction" && game.auction.currentBid >= 60 && game.auction.activeIds.length >= 2,
  },
  {
    name: "pad-debt",
    viewer: "table",
    description: "PC table: a controller seat (Maya) is in debt",
    when: (game) => game.phase === "debt" && game.debt?.payerId === byName(game, "Maya").id
      && Object.values(game.properties).some((state) => state.ownerId === byName(game, "Maya").id),
  },
  {
    name: "bankrupt",
    viewer: "Robin",
    description: "Someone has just gone bankrupt",
    when: (game, last) => last?.action?.type === "bankrupt" && game.status === "playing",
  },
  {
    name: "victory",
    viewer: "Robin",
    description: "Game over (prefers a game Robin won)",
    when: (game) => game.status === "finished" && game.winnerId === T(game).id,
  },
  {
    name: "defeat",
    viewer: "Robin",
    description: "Game over and Robin did not win",
    when: (game) => game.status === "finished" && game.winnerId !== T(game).id,
  },
];

// ---------------------------------------------------------------------------------------------

function snapshotFor(game, viewerName, extras = {}) {
  const viewerPlayer = viewerName === "table" ? null : byName(game, viewerName);
  const viewer = viewerName === "table"
    ? { role: "table", name: "Table", isHost: true }
    : { role: "player", name: viewerPlayer.name, isHost: game.hostId === viewerPlayer.id };
  const state = JSON.parse(JSON.stringify(publicState(game, viewerPlayer?.id || null, viewer)));
  for (const player of state.players) {
    const entry = ROSTER.find((candidate) => candidate.name === player.name);
    player.input = entry.input;
    player.tokenKind = entry.tokenKind;
    player.online = extras.offline ? !extras.offline.includes(player.name) : true;
  }
  if (viewerName === "table") {
    state.viewerRole = "table";
    const maya = state.players.find((player) => player.name === "Maya");
    state.localSeats = maya ? [{ seatId: "seat_000000000001", playerId: maya.id, input: "controller", pad: 0 }] : [];
  }
  return {
    state,
    viewer: viewerName === "table"
      ? { role: "table" }
      : { role: "player", playerId: viewerPlayer.id, name: viewerPlayer.name },
  };
}

function lobbyFixtures() {
  const game = newLobby();
  const out = {};
  out.lobby = { ...snapshotFor(game, "Robin"), meta: { description: "Lobby with four seats; Robin is the host" } };
  out["table-lobby"] = { ...snapshotFor(game, "table"), meta: { description: "PC table join screen with four seats" } };
  const small = createGame(CODE);
  const host = addPlayer(small, "Robin");
  host.color = ROSTER[0].color;
  const solo = snapshotFor(small, "table");
  out["table-empty"] = { ...solo, meta: { description: "PC table join screen, only the host has joined" } };
  return out;
}

function run({ maxSeeds = 400, maxSteps = 5000 } = {}) {
  const found = new Map();
  const replay = { states: [], seed: null };
  const wanted = () => SCENARIOS.filter((scenario) => !found.has(scenario.name));

  for (let seed = 1; seed <= maxSeeds && wanted().length; seed += 1) {
    const rng = mulberry32(seed);
    const game = newLobby();
    performAction(game, game.hostId, { type: "start" }, rng);
    let last = null;
    const recordReplay = replay.seed === null;
    const replayStates = [];
    for (let count = 0; count < maxSteps; count += 1) {
      for (const scenario of wanted()) {
        if (scenario.when(game, last)) {
          found.set(scenario.name, {
            ...snapshotFor(game, scenario.viewer, scenario.extras),
            meta: { description: scenario.description, seed, step: count },
          });
        }
      }
      // A short continuous stretch of play for ?demo=replay (animations against real diffs).
      if (recordReplay && ownedCount(game) >= 12 && replayStates.length < 36) {
        replayStates.push(JSON.parse(JSON.stringify(publicState(game, null, { role: "table", isHost: true }))));
      }
      if (game.status === "finished") break;
      last = step(game, rng);
      if (!last) break;
    }
    if (recordReplay && replayStates.length >= 36) {
      replay.seed = seed;
      replay.states = replayStates;
    }
  }
  return { found, replay };
}

function decorateReplay(replay) {
  const states = replay.states.map((state) => {
    const copy = { ...state };
    delete copy.board;
    delete copy.groups;
    for (const player of copy.players) {
      const entry = ROSTER.find((candidate) => candidate.name === player.name);
      player.input = entry.input;
      player.tokenKind = entry.tokenKind;
      player.online = true;
    }
    copy.viewerRole = "table";
    const maya = copy.players.find((player) => player.name === "Maya");
    copy.localSeats = [{ seatId: "seat_000000000001", playerId: maya.id, input: "controller", pad: 0 }];
    return copy;
  });
  const first = replay.states[0];
  return {
    replay: states,
    board: first.board,
    groups: first.groups,
    viewer: { role: "table" },
    meta: { description: "36 consecutive table states for animation QA (board/groups stored once)", seed: replay.seed },
  };
}

// The JS engine has no house rules or loans (the Go engine adds them), so these fixtures are real
// captured states with the new fields added by hand. Each is marked meta.handEdited.
const LOAN_RULES = { loans: true, freeParkingJackpot: true, auctions: true, startingCash: 1500 };

function handEdited(outputs) {
  const clone = (name) => (outputs[name] ? JSON.parse(JSON.stringify(outputs[name])) : null);
  const byPlayer = (state, name) => state.players.find((player) => player.name === name);
  const ownedFree = (state, playerId) => Object.entries(state.properties)
    .filter(([, prop]) => prop.ownerId === playerId && !prop.mortgaged && !prop.houses)
    .map(([index]) => Number(index));
  const extra = {};

  const lobby = clone("lobby");
  if (lobby) {
    lobby.state.settings.rules = { ...LOAN_RULES, freeParkingJackpot: false };
    lobby.meta = { description: "Lobby with house rules set (loans on)", handEdited: true };
    extra["lobby-rules"] = lobby;
  }

  const waiting = clone("waiting");
  if (waiting) {
    const { state } = waiting;
    const alpha = byPlayer(state, "Alpha");
    const robin = byPlayer(state, "Robin");
    state.settings.rules = LOAN_RULES;
    state.freeParking = 140;
    state.loanOffer = {
      id: "loanoffer_000000000001", proposerId: alpha.id, targetId: robin.id, direction: "lend",
      principal: 300, repay: 360, dueTurns: 6, collateral: ownedFree(state, robin.id).slice(0, 1), returnPhase: state.phase,
    };
    state.phase = "loan_offer";
    waiting.meta = { description: "Alpha offers Robin a loan (phase loan_offer)", handEdited: true, from: "waiting" };
    extra["loan-offer"] = waiting;
  }

  const debt = clone("debt");
  if (debt) {
    const { state } = debt;
    state.settings.rules = LOAN_RULES;
    extra["debt-loans"] = { ...JSON.parse(JSON.stringify(debt)), meta: { description: "Debt with the loans house rule on", handEdited: true, from: "debt" } };
    const robin = byPlayer(state, "Robin");
    const sam = byPlayer(state, "Sam");
    const short = Math.max(10, Math.ceil((state.debt.amount - robin.cash) / 10) * 10);
    state.loanOffer = {
      id: "loanoffer_000000000002", proposerId: robin.id, targetId: sam.id, direction: "borrow",
      principal: short, repay: Math.ceil((short * 1.2) / 10) * 10, dueTurns: 6, collateral: ownedFree(state, robin.id).slice(0, 1), returnPhase: "debt",
    };
    state.phase = "loan_offer";
    debt.meta = { description: "In debt, Robin asked Sam for a loan and waits for the answer", handEdited: true, from: "debt" };
    extra["loan-request"] = debt;
  }

  const roll = clone("roll");
  if (roll) {
    const { state } = roll;
    const alpha = byPlayer(state, "Alpha");
    const robin = byPlayer(state, "Robin");
    const sam = byPlayer(state, "Sam");
    state.settings.rules = LOAN_RULES;
    state.freeParking = 140;
    state.loans = [
      { id: "loan_000000000001", lenderId: alpha.id, borrowerId: robin.id, principal: 400, repay: 480, dueTurn: 14, collateral: ownedFree(state, robin.id).slice(0, 1), status: "active" },
      { id: "loan_000000000002", lenderId: robin.id, borrowerId: sam.id, principal: 200, repay: 240, dueTurn: 16, collateral: [], status: "active" },
      { id: "loan_000000000003", lenderId: sam.id, borrowerId: alpha.id, principal: 100, repay: 120, dueTurn: 9, collateral: [], status: "repaid" },
    ];
    roll.meta = { description: "Robin's turn with two active loans (one owed, one owing) and a City Park pot", handEdited: true, from: "roll" };
    extra["active-loan"] = roll;
  }
  return extra;
}

// Sample payloads for endpoints that have no engine state behind them (API.md §2).
function staticFixtures() {
  return {
    friends: {
      friends: [
        { deviceId: "dev_priya", name: "Priya", tokenKind: "umbrella", color: "#D0487F", status: "online", lastPlayedAt: Date.UTC(2026, 9, 30) },
        { deviceId: "dev_jordan", name: "Jordan", tokenKind: "key", color: "#7B4FD6", status: "online", lastPlayedAt: Date.UTC(2026, 9, 25) },
        { deviceId: "dev_leo", name: "Leo", tokenKind: "lighthouse", color: "#1E9E8F", status: "in_game", code: "KQ7RT", turn: 31, lastPlayedAt: Date.UTC(2026, 9, 20) },
        { deviceId: "dev_aisha", name: "Aisha", tokenKind: "teapot", color: "#D9343A", status: "offline", lastPlayedAt: Date.UTC(2026, 9, 30), lastResult: "won" },
        { deviceId: "dev_ben", name: "Ben", tokenKind: "bicycle", color: "#E8A21A", status: "offline", lastPlayedAt: Date.UTC(2026, 9, 24) },
      ],
      meta: { description: "Sample GET /api/friends response" },
    },
    server: {
      server: { lanURL: "http://192.168.1.20:3210", publicURL: "https://estate.example.ts.net:8443", version: "demo" },
      meta: { description: "Sample GET /api/server response" },
    },
  };
}

function main() {
  if (process.argv.includes("--list")) {
    for (const scenario of SCENARIOS) console.log(`${scenario.name.padEnd(16)} ${scenario.viewer.padEnd(8)} ${scenario.description}`);
    return;
  }
  fs.mkdirSync(OUT_DIR, { recursive: true });
  const outputs = { ...lobbyFixtures() };
  const { found, replay } = run();
  for (const scenario of SCENARIOS) {
    if (found.has(scenario.name)) outputs[scenario.name] = found.get(scenario.name);
    else console.warn(`! scenario not reached: ${scenario.name}`);
  }
  if (replay.states.length) outputs.replay = decorateReplay(replay);
  Object.assign(outputs, handEdited(outputs), staticFixtures());

  const index = [];
  for (const [name, payload] of Object.entries(outputs)) {
    fs.writeFileSync(path.join(OUT_DIR, `${name}.json`), `${JSON.stringify(payload)}\n`);
    index.push({ name, description: payload.meta?.description || "", viewer: payload.viewer?.role === "table" ? "table" : payload.viewer?.name || "" });
  }
  fs.writeFileSync(path.join(OUT_DIR, "index.json"), `${JSON.stringify(index, null, 2)}\n`);
  for (const entry of index) console.log(`${entry.name.padEnd(16)} ${String(entry.viewer).padEnd(8)} ${entry.description}`);
}

main();
