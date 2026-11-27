#!/usr/bin/env node
// Records reference games from the JS engine for the Go parity test
// (internal/engine/trace_test.go, TestTraceParity).
//
//   node scripts/trace.js            # rewrite testdata/traces/*.json
//   node scripts/trace.js --check    # print coverage only, write nothing
//   node scripts/trace.js --scripted-only  # only the 3 hand-written traces (fast;
//                                          # replaces the directory contents!)
//
// Every engine call is recorded as a step: the operation, the actor, the
// action (exactly as sent over the wire), the RNG floats it consumed, the
// error message (if any), the log texts it added, and a delta of the
// canonical state snapshot. Snapshots are the full JSON of the game object
// minus ids, timestamps and the log; deltas are recursive object diffs
// (arrays are replaced whole, {"$del":true} deletes a key).
//
// Determinism: crypto.randomBytes is patched to a counter (ids become
// prefix_000000000001, ...; mirrored by engine.SequentialIDs) and Date.now is
// a constant (mirrored by a constant Game.Clock). Randomness comes from a
// seeded mulberry32 PRNG whose floats are recorded per step.
"use strict";

const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");

const CLOCK = 1_700_000_000_000;
const FAR_FUTURE = 9_000_000_000_000_000;
const MAX_STEPS = 3000;

let idCounter = 0;
crypto.randomBytes = (size) => {
  idCounter += 1;
  const buffer = Buffer.alloc(size);
  buffer.writeUIntBE(idCounter, size - 6, 6);
  return buffer;
};
Date.now = () => CLOCK;

const game = require("../src/game");
const { chooseAiAction } = require("../src/ai");
const { BOARD } = require("../src/board");

const {
  addPlayer,
  bankSupply,
  completeOpeningOrder,
  createGame,
  performAction,
  publicState,
  removePlayer,
  switchPlayerToSpectator,
  switchSpectatorToPlayer,
} = game;

const OUT_DIR = path.join(__dirname, "..", "testdata", "traces");
const AI_NAMES = ["Alpha", "Blue Whale", "Redwood", "North Star", "Skylark", "AI 5"];
const HUMAN_NAMES = ["Ana", "Ben", "Cleo", "Dev", "Eli", "Fay"];

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

class Recorder {
  constructor(source) {
    this.source = source;
    this.taken = [];
    this.rng = () => {
      const value = this.source();
      this.taken.push(value);
      return value;
    };
  }

  take() {
    const taken = this.taken;
    this.taken = [];
    return taken;
  }
}

// ----------------------------------------------------------- snapshots ----

const STRIPPED_KEYS = new Set(["id", "at", "createdAt"]);

function strip(value) {
  if (Array.isArray(value)) return value.map(strip);
  if (value && typeof value === "object") {
    const out = {};
    for (const [key, item] of Object.entries(value)) {
      if (!STRIPPED_KEYS.has(key)) out[key] = strip(item);
    }
    return out;
  }
  return value;
}

// Canonical game snapshot. Mirrored by canonicalGame in trace_test.go.
function canonicalGame(state) {
  const raw = JSON.parse(JSON.stringify(state));
  const players = raw.players;
  delete raw.players;
  delete raw.log;
  const out = strip(raw);
  out.players = Object.fromEntries(players.map((player, index) => [String(index), player]));
  return out;
}

// Canonical public state. Mirrored by canonicalPublic in trace_test.go.
function canonicalPublic(view, full) {
  const raw = JSON.parse(JSON.stringify(view));
  const players = raw.players;
  delete raw.players;
  const logTexts = raw.log.map((entry) => entry.text);
  delete raw.log;
  if (!full) {
    delete raw.board;
    delete raw.groups;
  }
  const out = strip(raw);
  out.players = players;
  out.logTexts = logTexts;
  return out;
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function deepEqual(left, right) {
  return JSON.stringify(left) === JSON.stringify(right) && sameKeys(left, right);
}

function sameKeys(left, right) {
  if (isObject(left) && isObject(right)) {
    const leftKeys = Object.keys(left).sort();
    const rightKeys = Object.keys(right).sort();
    if (leftKeys.join("\u0000") !== rightKeys.join("\u0000")) return false;
    return leftKeys.every((key) => sameKeys(left[key], right[key]));
  }
  return true;
}

// diff(before, after) -> patch or undefined. Mirrored by applyPatch in Go.
function diff(before, after) {
  if (isObject(before) && isObject(after)) {
    const out = {};
    let changed = false;
    for (const key of Object.keys(after)) {
      if (!(key in before)) {
        out[key] = after[key];
        changed = true;
        continue;
      }
      const patch = diff(before[key], after[key]);
      if (patch !== undefined) {
        out[key] = patch;
        changed = true;
      }
    }
    for (const key of Object.keys(before)) {
      if (!(key in after)) {
        out[key] = { $del: true };
        changed = true;
      }
    }
    return changed ? out : undefined;
  }
  return deepEqual(before, after) ? undefined : after;
}

// --------------------------------------------------------------- trace ----

// Distinguishes state shapes worth a raw capture: phase, debt continuation
// and which nullable sub-objects are present.
function rawSignature(state) {
  return [
    state.phase,
    state.debt ? state.debt.after?.kind ?? "none" : "-",
    ...["turn", "trade", "auction", "pendingCard", "lastCard", "lastBankruptcy", "turnSummary", "openingOrder"]
      .map((key) => (state[key] ? 1 : 0)),
    state.pendingPurchase === null ? 0 : 1,
  ].join("|");
}

class Trace {
  constructor(name, description, code, seed) {
    idCounter = 0;
    this.name = name;
    this.description = description;
    this.seed = seed;
    this.recorder = new Recorder(mulberry32(seed));
    this.game = createGame(code);
    this.previous = canonicalGame(this.game);
    this.data = {
      name,
      description,
      code,
      clock: CLOCK,
      seed,
      initial: this.previous,
      steps: [],
    };
    this.consecutiveAiErrors = 0;
    // Raw JSON.stringify(game) captures (ids, timestamps and log included),
    // one per distinct state signature, for TestGameJSONMatchesJS.
    this.rawSignatures = new Set();
    this.rawLimit = 0;
  }

  captureRaw(enabled = 10) {
    this.rawLimit = enabled;
    return this;
  }

  lastLogId() {
    return this.game.log.at(-1)?.id;
  }

  record(step, run) {
    const lastId = this.lastLogId();
    try {
      const result = run();
      if (result !== undefined) step.result = result;
    } catch (error) {
      step.error = error.message;
    }
    const floats = this.recorder.take();
    if (floats.length) step.floats = floats;
    const index = lastId === undefined ? -1 : this.game.log.findIndex((entry) => entry.id === lastId);
    const logs = this.game.log.slice(index + 1).map((entry) => entry.text);
    if (logs.length) step.logs = logs;
    const next = canonicalGame(this.game);
    const delta = diff(this.previous, next);
    if (delta !== undefined) step.delta = delta;
    this.previous = next;
    const signature = rawSignature(this.game);
    if (this.rawSignatures.size < this.rawLimit && !this.rawSignatures.has(signature)) {
      this.rawSignatures.add(signature);
      step.raw = JSON.parse(JSON.stringify(this.game));
    }
    this.data.steps.push(step);
    return step;
  }

  addPlayer(name, kind) {
    const step = { op: "addPlayer", name };
    if (kind !== undefined) step.kind = kind;
    return this.record(step, () => addPlayer(this.game, name, kind).id);
  }

  removePlayer(requesterId, playerId) {
    return this.record({ op: "removePlayer", requester: requesterId ?? null, target: playerId }, () => {
      removePlayer(this.game, requesterId, playerId);
    });
  }

  toSpectator(playerId, spectatorId) {
    return this.record({ op: "toSpectator", playerId, spectatorId }, () => {
      switchPlayerToSpectator(this.game, playerId, spectatorId);
    });
  }

  toPlayer(name, isHost) {
    return this.record({ op: "toPlayer", name, isHost }, () => switchSpectatorToPlayer(this.game, name, isHost).id);
  }

  completeOrder(now) {
    return this.record({ op: "completeOpeningOrder", now }, () => completeOpeningOrder(this.game, now));
  }

  // Sends an action exactly as the wire would deliver it (JSON round trip).
  action(actorId, action) {
    const wire = action === undefined ? null : JSON.parse(JSON.stringify(action));
    const seat = this.game.players.findIndex((player) => player.id === actorId);
    const step = { op: "action", actor: actorId ?? null, seat, action: wire };
    return this.record(step, () => {
      performAction(this.game, actorId, wire, this.recorder.rng);
    });
  }

  publicView(viewerId, viewer, full = false) {
    const step = { op: "publicState", viewerId: viewerId ?? null, viewer, full };
    return this.record(step, () => canonicalPublic(publicState(this.game, viewerId, viewer), full));
  }

  // Direct state edits, like the JS unit tests do. Each entry sets the value
  // at a path of keys (array indexes for players) on the game object.
  mutate(sets) {
    return this.record({ op: "mutate", set: sets }, () => {
      for (const { path: keys, value } of sets) {
        let target = this.game;
        for (const key of keys.slice(0, -1)) target = target[key];
        target[keys.at(-1)] = value === null ? null : JSON.parse(JSON.stringify(value));
      }
    });
  }

  write() {
    const game = this.game;
    this.data.finished = game.status === "finished";
    this.data.winnerId = game.winnerId;
    this.data.stepCount = this.data.steps.length;
    const { steps, ...header } = this.data;
    const lines = steps.map((step) => JSON.stringify(step));
    const json = `${JSON.stringify(header).slice(0, -1)},"steps":[\n${lines.join(",\n")}\n]}\n`;
    if (!CHECK_ONLY) fs.writeFileSync(path.join(OUT_DIR, `${this.name}.json`), json);
    return json.length;
  }
}

// --------------------------------------------------------- AI driving -----

// Who may act next; mirrors nextAiAction in src/rooms.js but for any kind.
function nextActorId(state) {
  if (state.status !== "playing") return null;
  if (state.phase === "determining_order") {
    const pending = state.openingOrder?.pendingIds || [];
    return pending.find((id) => state.players.find((player) => player.id === id)?.kind === "ai") || pending[0] || null;
  }
  if (state.phase === "trade") return state.trade?.targetId || null;
  if (state.phase === "auction") {
    const auction = state.auction;
    if (!auction) return null;
    const active = auction.activeIds;
    if (active.length === 1 && auction.bidderId === active[0]) return null;
    for (let offset = 0; offset < active.length; offset += 1) {
      const pos = (auction.turnPos + offset) % active.length;
      if (active[pos] !== auction.bidderId) return active[pos];
    }
    return null;
  }
  return state.players[state.currentIndex]?.id || null;
}

// ------------------------------------------------------- human policy -----

function ownedIndexes(state, playerId) {
  return Object.keys(state.properties).map(Number).filter((index) => state.properties[index].ownerId === playerId);
}

function groupOf(index) {
  return BOARD.filter((space) => space.group && space.group === BOARD[index].group);
}

function groupHasBuildings(state, index) {
  return BOARD[index].type === "property" && groupOf(index).some((space) => state.properties[space.index].houses > 0);
}

function sellable(state, playerId) {
  const supply = bankSupply(state);
  return ownedIndexes(state, playerId).filter((index) => {
    const houses = state.properties[index].houses;
    if (BOARD[index].type !== "property" || houses < 1) return false;
    const maximum = Math.max(...groupOf(index).map((space) => state.properties[space.index].houses));
    return houses === maximum && (houses !== 5 || supply.houses >= 4);
  });
}

function mortgageable(state, playerId) {
  return ownedIndexes(state, playerId).filter(
    (index) => !state.properties[index].mortgaged && !groupHasBuildings(state, index),
  );
}

function buildable(state, player) {
  return ownedIndexes(state, player.id).filter((index) => {
    const space = BOARD[index];
    if (space.type !== "property") return false;
    const group = groupOf(index);
    if (!group.every((item) => state.properties[item.index].ownerId === player.id)) return false;
    if (group.some((item) => state.properties[item.index].mortgaged)) return false;
    const minimum = Math.min(...group.map((item) => state.properties[item.index].houses));
    const houses = state.properties[index].houses;
    return houses === minimum && houses < 5 && player.cash >= space.buildCost + 60;
  });
}

function tradeable(state, playerId) {
  return ownedIndexes(state, playerId).filter((index) => !groupHasBuildings(state, index));
}

const ALL_TYPES = [
  "start", "roll_for_order", "roll", "confirm_card", "set_dice_mode", "offer_trade", "accept_trade",
  "reject_trade", "buy", "decline", "bid", "pass_auction", "end_turn", "pay_bail", "use_jail_card",
  "pay_debt", "bankrupt", "build", "sell_building", "mortgage", "unmortgage", "teleport",
];

function makePolicy(seed) {
  const rand = mulberry32(seed);
  const chance = (probability) => rand() < probability;
  const pick = (items) => items[Math.floor(rand() * items.length)];
  const die = () => 1 + Math.floor(rand() * 6);

  function rollAction(state, player) {
    if (state.settings.diceMode !== "choice") return { type: "roll" };
    if (chance(0.04)) return pick([
      { type: "roll", dice: [0, 7] },
      { type: "roll", dice: [3] },
      { type: "roll" },
      { type: "roll", dice: [2.5, 3] },
      { type: "roll", dice: [String(die()), String(die())] },
    ]);
    if (player.inJail && chance(0.3)) {
      const value = die();
      return { type: "roll", dice: [value, value] };
    }
    return { type: "roll", dice: [die(), die()] };
  }

  function manageAction(state, player) {
    const options = [];
    const builds = buildable(state, player);
    if (builds.length) options.push({ type: "build", spaceIndex: pick(builds) });
    const mortgaged = ownedIndexes(state, player.id).filter((index) => state.properties[index].mortgaged);
    const affordable = mortgaged.filter((index) => player.cash >= Math.ceil((BOARD[index].price / 2) * 1.1) + 150);
    if (affordable.length) options.push({ type: "unmortgage", spaceIndex: pick(affordable) });
    const sells = sellable(state, player.id);
    if (sells.length && chance(0.25)) options.push({ type: "sell_building", spaceIndex: pick(sells) });
    const mortgages = mortgageable(state, player.id);
    if (mortgages.length && chance(0.15)) options.push({ type: "mortgage", spaceIndex: pick(mortgages) });
    return options.length ? pick(options) : null;
  }

  function tradeAction(state, player) {
    const targets = state.players.filter((candidate) => candidate.id !== player.id && !candidate.bankrupt);
    if (!targets.length) return null;
    const target = pick(targets);
    const mine = tradeable(state, player.id);
    const theirs = tradeable(state, target.id);
    const offerProperties = [];
    const requestProperties = [];
    if (mine.length && chance(0.6)) offerProperties.push(pick(mine));
    if (mine.length > 1 && chance(0.25)) offerProperties.push(pick(mine));
    if (theirs.length && chance(0.7)) requestProperties.push(pick(theirs));
    const offerCash = chance(0.5) ? Math.floor(rand() * Math.min(player.cash, 400)) : 0;
    const requestCash = chance(0.3) ? Math.floor(rand() * Math.min(target.cash, 200)) : 0;
    return {
      type: "offer_trade",
      targetId: target.id,
      offer: { cash: offerCash, properties: offerProperties },
      request: { cash: requestCash, properties: requestProperties },
    };
  }

  function randomAction(state) {
    const actor = pick(state.players);
    const type = pick(ALL_TYPES);
    const action = { type };
    if (chance(0.5)) action.spaceIndex = pick([1, 3, 5, 12, 39, 0, 40, -1, "6", "x", 2.5, null]);
    if (chance(0.3)) action.amount = pick([10, 50, 1e6, -5, "20", "abc", null, 7.5]);
    if (chance(0.2)) action.dice = pick([[1, 2], [6, 6], ["4", "4"], [0, 1], "12"]);
    if (chance(0.2)) action.mode = pick(["choice", "random", "fast", 1]);
    if (type === "offer_trade" || chance(0.1)) {
      const other = pick(state.players);
      action.targetId = pick([other.id, actor.id, "nobody", 5]);
      action.offer = pick([{ cash: 10 }, { cash: "15", properties: ["1", 3] }, { properties: [2] }, {}, null, "x"]);
      action.request = pick([{ cash: 5 }, { properties: [39] }, { cash: -1 }, { cash: 2e6 }, { cash: true }]);
    }
    return { actorId: actor.id, action };
  }

  // Returns { actorId, action } for a human who may act now.
  function decide(state, player) {
    if (chance(0.03)) return randomAction(state);
    const act = (action) => ({ actorId: player.id, action });
    switch (state.phase) {
      case "determining_order":
        return act({ type: "roll_for_order" });
      case "card_confirmation":
        return act({ type: "confirm_card" });
      case "trade": {
        const trade = state.trade;
        const proposer = state.players.find((candidate) => candidate.id === trade.proposerId);
        if (proposer.kind === "human" && chance(0.2)) return { actorId: proposer.id, action: { type: "reject_trade" } };
        return act({ type: chance(0.45) ? "accept_trade" : "reject_trade" });
      }
      case "auction": {
        const auction = state.auction;
        const bid = auction.currentBid + 1 + Math.floor(rand() * 40);
        const ceiling = BOARD[auction.spaceIndex].price * 1.2;
        if (bid <= player.cash && bid <= ceiling && chance(0.6)) return act({ type: "bid", amount: bid });
        return act({ type: "pass_auction" });
      }
      case "debt": {
        const debt = state.debt;
        if (player.cash >= debt.amount && chance(0.9)) return act({ type: "pay_debt" });
        if (chance(0.04)) return act({ type: "bankrupt" });
        const sells = sellable(state, player.id);
        if (sells.length && chance(0.7)) return act({ type: "sell_building", spaceIndex: pick(sells) });
        const mortgages = mortgageable(state, player.id);
        if (mortgages.length) return act({ type: "mortgage", spaceIndex: pick(mortgages) });
        if (sells.length) return act({ type: "sell_building", spaceIndex: pick(sells) });
        if (player.cash >= debt.amount) return act({ type: "pay_debt" });
        return act({ type: "bankrupt" });
      }
      case "awaiting_purchase": {
        const space = BOARD[state.pendingPurchase];
        if (player.cash >= space.price && chance(0.75)) return act({ type: "buy" });
        const mortgages = mortgageable(state, player.id);
        if (player.cash < space.price && mortgages.length && chance(0.4)) {
          return act({ type: "mortgage", spaceIndex: pick(mortgages) });
        }
        return act({ type: "decline" });
      }
      case "awaiting_roll": {
        if (chance(0.3)) {
          const manage = manageAction(state, player);
          if (manage) return act(manage);
        }
        if (chance(0.06)) {
          const trade = tradeAction(state, player);
          if (trade) return act(trade);
        }
        if (player.inJail) {
          if (player.jailCards > 0 && chance(0.6)) return act({ type: "use_jail_card" });
          if (player.cash >= 50 && chance(0.4)) return act({ type: "pay_bail" });
        }
        return act(rollAction(state, player));
      }
      case "turn_complete": {
        if (chance(0.35)) {
          const manage = manageAction(state, player);
          if (manage) return act(manage);
        }
        if (chance(0.06)) {
          const trade = tradeAction(state, player);
          if (trade) return act(trade);
        }
        return act({ type: "end_turn" });
      }
      default:
        return null;
    }
  }

  return { decide, chance };
}

// --------------------------------------------------------------- games ----

function playGame({ name, description, seed, kinds, diceMode = "random", viaSpectatorHost = false, publicEvery = 0, raw = 0 }) {
  const trace = new Trace(name, description, `T${String(seed).padStart(4, "0")}`, seed).captureRaw(raw);
  const policy = makePolicy(seed ^ 0x5bd1e995);
  let hostSpectator = null;
  if (viaSpectatorHost) {
    // rooms.js flow: the creator joins, switches to spectator, then adds AIs.
    const host = trace.addPlayer("Host");
    hostSpectator = "spectator_host";
    trace.toSpectator(host.result, hostSpectator);
  }
  let aiIndex = 0;
  let humanIndex = 0;
  for (const kind of kinds) {
    if (kind === "ai") trace.addPlayer(AI_NAMES[aiIndex++], "ai");
    else trace.addPlayer(HUMAN_NAMES[humanIndex++]);
  }
  const hostId = trace.game.hostId;
  if (diceMode !== "random") trace.action(hostId, { type: "set_dice_mode", mode: diceMode });
  trace.action(hostId, { type: "start" });

  let steps = 0;
  let stuck = null;
  while (trace.game.status === "playing" && steps < MAX_STEPS) {
    steps += 1;
    const state = trace.game;
    if (publicEvery && steps % publicEvery === 0) {
      const viewer = state.players[steps % state.players.length];
      trace.publicView(viewer.id, { role: "player", name: viewer.name, isHost: viewer.id === state.hostId });
      continue;
    }
    if (state.phase === "order_countdown") {
      trace.completeOrder(FAR_FUTURE);
      continue;
    }
    const actorId = nextActorId(state);
    const actor = state.players.find((player) => player.id === actorId);
    if (!actor) {
      stuck = `no actor in phase ${state.phase}`;
      break;
    }
    if (actor.kind === "ai") {
      // rooms.js nextAiAction: AIs roll for order without consulting ai.js.
      const action = state.phase === "determining_order"
        ? { type: "roll_for_order" }
        : chooseAiAction(state, actor.id);
      if (!action) {
        stuck = `AI returned no action in phase ${state.phase}`;
        break;
      }
      const step = trace.action(actor.id, action);
      if (step.error) {
        trace.consecutiveAiErrors += 1;
        if (trace.consecutiveAiErrors >= 2) {
          stuck = `AI action keeps failing: ${step.error}`;
          break;
        }
      } else {
        trace.consecutiveAiErrors = 0;
      }
      continue;
    }
    const decision = policy.decide(state, actor);
    if (!decision) {
      stuck = `human policy has no action in phase ${state.phase}`;
      break;
    }
    trace.action(decision.actorId, decision.action);
  }
  if (stuck) trace.data.stuck = stuck;
  return trace;
}

// Hand-written lobby and edge-case sequence.
function scriptedLobby() {
  const trace = new Trace("scripted-lobby", "Lobby operations, host changes, validation errors and countdown timing", "LOBBY", 4242).captureRaw(12);
  const t = trace;
  t.action(null, { type: "roll" }); // not started
  t.action(null, undefined); // invalid format (null)
  t.action(null, "roll"); // invalid format (string)
  t.action(null, { type: 7 }); // invalid format (number type)
  t.action(null, { type: "start" }); // fewer than 2 players (host null)
  const ana = t.addPlayer("Ana").result;
  const alpha = t.addPlayer("Alpha", "ai").result;
  const ben = t.addPlayer("Ben").result;
  t.action(ben, { type: "start" }); // only the host
  t.action(ben, { type: "set_dice_mode", mode: "choice" }); // only the host
  t.action(ana, { type: "set_dice_mode", mode: "fast" }); // invalid
  t.action(ana, { type: "set_dice_mode", mode: 3 }); // invalid (non-string)
  t.action(ana, { type: "set_dice_mode", mode: "choice" });
  t.action(ana, { type: "set_dice_mode", mode: "random" });
  t.action(ana, { type: "end_turn" }); // not started
  t.removePlayer(ben, alpha); // not host
  t.removePlayer(ana, "missing"); // not found
  t.removePlayer(ana, alpha);
  t.toSpectator(alpha, "spectator_x"); // only humans
  t.toSpectator(ana, "spectator_ana"); // host -> spectator id
  t.toPlayer("Ana", true);
  const cleo = t.addPlayer("Cleo").result;
  t.removePlayer(t.game.hostId, t.game.hostId); // host removes self -> next human
  t.addPlayer("Dev", "ai");
  t.addPlayer("Eli");
  t.addPlayer("Fay", "ai");
  t.addPlayer("Gus");
  t.addPlayer("Hal"); // room full
  t.toPlayer("Ivy", false); // room full
  t.toSpectator(cleo, "spectator_cleo");
  t.toPlayer("Cleo", false);
  t.publicView(null, { role: "spectator", name: "Watcher", spectatorId: "spectator_w", spectators: [{ id: "spectator_w", name: "Watcher", isHost: false }] }, true);
  const host = t.game.hostId;
  t.action(host, { type: "start" });
  t.action(host, { type: "start" }); // already started
  t.addPlayer("Late"); // already started
  t.removePlayer(host, cleo); // already started
  t.toSpectator(host, "spectator_late");
  t.toPlayer("Late", false);
  t.action(host, { type: "set_dice_mode", mode: "choice" }); // already started
  t.action(host, { type: "roll" }); // wrong phase
  t.action(host, { type: "teleport" }); // unknown action
  t.action(host, { type: "" }); // unknown action (empty type)
  t.publicView(host, { role: "player", name: "x" });
  // Roll for order: everyone in seat order, with a forced tie in round one.
  const ids = t.game.players.map((player) => player.id);
  const forced = [0.5, 0.5, 0.5, 0.5]; // first two players tie on 4+4
  trace.recorder.source = (() => {
    const base = mulberry32(99);
    let used = 0;
    return () => (used < forced.length ? forced[used++] : base());
  })();
  for (const id of ids) t.action(id, { type: "roll_for_order" });
  t.action(ids[2], { type: "roll_for_order" }); // already rolled
  while (t.game.phase === "determining_order") {
    t.action(t.game.openingOrder.pendingIds[0], { type: "roll_for_order" });
  }
  t.publicView(ids[0], { role: "player" });
  t.completeOrder(0); // too early
  t.completeOrder(t.game.openingOrder.countdownEndsAt - 1); // too early
  t.completeOrder(t.game.openingOrder.countdownEndsAt);
  t.completeOrder(FAR_FUTURE); // already complete
  t.publicView(t.game.players[1].id, { role: "player", isHost: true }, true);
  return trace;
}

// Hand-written JS coercion sequence (values as a raw client could send).
function scriptedCoercion() {
  const trace = new Trace("scripted-coercion", "Loose client inputs coerced with JS Number() semantics", "COERC", 777).captureRaw(10);
  const t = trace;
  const a = t.addPlayer("Ana").result;
  const b = t.addPlayer("Ben").result;
  t.action(a, { type: "set_dice_mode", mode: "choice" });
  t.action(a, { type: "start" });
  trace.recorder.source = (() => {
    const values = [0.99, 0.99, 0.1, 0.1];
    let used = 0;
    const base = mulberry32(5);
    return () => (used < values.length ? values[used++] : base());
  })();
  t.action(a, { type: "roll_for_order" });
  t.action(b, { type: "roll_for_order" });
  t.completeOrder(FAR_FUTURE);
  const first = t.game.players[0].id;
  const second = t.game.players[1].id;
  for (const dice of [undefined, null, "34", [1], [1, 2, 3], [0, 3], [7, 1], [1.5, 2], ["x", 1], [null, 2], [[], 2], [{}, 1], [[3], [2, 1]], ["", 2]]) {
    t.action(first, dice === undefined ? { type: "roll" } : { type: "roll", dice });
  }
  t.action(first, { type: "roll", dice: [true, "0x2"] }); // 1 + 2 -> Maple Bridge Street
  for (const spaceIndex of [undefined, null, "", "3", " 3 ", "3.0", "0x3", "1e0", 3.5, -0, "-0", [3], ["3"], [[3]], [], [3, 4], {}, true, false, "Infinity", 40, -1]) {
    t.action(first, spaceIndex === undefined ? { type: "mortgage" } : { type: "mortgage", spaceIndex });
  }
  t.action(first, { type: "buy" });
  t.action(first, { type: "mortgage", spaceIndex: " 3 " });
  t.action(first, { type: "unmortgage", spaceIndex: "3" });
  t.action(first, { type: "build", spaceIndex: "3" });
  t.action(first, { type: "sell_building", spaceIndex: ["3"] });
  // Trade bundle coercion.
  const offers = [
    { cash: "abc" }, { cash: "" }, { cash: "0" }, { cash: 1.5 }, { cash: -1 }, { cash: 1000001 }, { cash: true },
    { cash: [] }, { cash: [7] }, { cash: null, properties: "3" }, { properties: ["3", 3, "3.0"] },
    { properties: [2] }, { properties: [40] }, { properties: ["x"] }, { properties: [null] }, "cash", [1], null,
  ];
  for (const offer of offers) {
    t.action(first, { type: "offer_trade", targetId: second, offer, request: { cash: "10" } });
    if (t.game.phase === "trade") t.action(first, { type: "reject_trade" });
  }
  for (const targetId of [5, null, first, "nobody", [second]]) {
    t.action(first, { type: "offer_trade", targetId, offer: { cash: 5 }, request: { cash: 5 } });
  }
  t.action(first, { type: "offer_trade", targetId: second, offer: { cash: "25", properties: ["3"] }, request: { cash: " 10 " } });
  t.action(second, { type: "accept_trade" });
  t.action(first, { type: "end_turn" });
  // Land on a property and auction it with coerced bids.
  t.action(second, { type: "roll", dice: [" 2 ", "4"] }); // to 6
  t.action(second, { type: "decline" });
  const bids = [undefined, null, "", "abc", "0", 0, -5, 2.5, "2.5", [3], "1e1", 1e9, "1e999"];
  for (const amount of bids) {
    const current = auctionActor(t.game);
    t.action(current, amount === undefined ? { type: "bid" } : { type: "bid", amount });
  }
  t.action(auctionActor(t.game), { type: "bid", amount: " 40 " });
  t.action(auctionActor(t.game), { type: "bid", amount: "0x30" });
  t.action(auctionActor(t.game), { type: "pass_auction" });
  t.publicView(second, { role: "player" });
  return trace;
}

// Hand-written rare branches: bail debt, jail-fee debt, bank supply limits,
// repairs, jail cards, utility rent, trade re-validation on accept, a throw
// after partial mutation, bankruptcy to the bank, and the game ending.
function scriptedEdges() {
  const trace = new Trace("scripted-edges", "Rare rule branches set up with direct state edits", "EDGES", 31337).captureRaw(25);
  const t = trace;
  const g = t.game;
  const ana = t.addPlayer("Ana").result;
  t.addPlayer("Ben");
  t.addPlayer("Cleo");
  t.action(ana, { type: "set_dice_mode", mode: "choice" });
  t.action(ana, { type: "start" });
  while (g.phase === "determining_order") t.action(g.openingOrder.pendingIds[0], { type: "roll_for_order" });
  t.completeOrder(FAR_FUTURE);

  const seatOf = (id) => g.players.findIndex((player) => player.id === id);
  const cur = () => g.players[g.currentIndex];
  const other = (offset) => g.players[(g.currentIndex + offset) % g.players.length];
  const expectPhase = (phase) => {
    if (g.phase !== phase) throw new Error(`scripted-edges: expected ${phase}, got ${g.phase}`);
  };
  const P = (seat, key, value) => ({ path: ["players", seat, key], value });
  const prop = (index, key, value) => ({ path: ["properties", String(index), key], value });
  const set = (key, value) => ({ path: [key], value });
  const deckWithTop = (deck, top) => [top, ...g.decks[deck].filter((id) => id !== top)];
  function finishTurn() {
    const id = cur().id;
    for (let guard = 0; guard < 50 && g.status === "playing" && cur().id === id; guard += 1) {
      const me = cur();
      switch (g.phase) {
        case "awaiting_roll": t.action(id, { type: "roll", dice: [1, 2] }); break;
        case "awaiting_purchase": t.action(id, { type: "decline" }); break;
        case "auction": t.action(nextActorId(g), { type: "pass_auction" }); break;
        case "card_confirmation": t.action(g.pendingCard.playerId, { type: "confirm_card" }); break;
        case "debt": t.action(id, { type: me.cash >= g.debt.amount ? "pay_debt" : "bankrupt" }); break;
        case "trade": t.action(g.trade.proposerId, { type: "reject_trade" }); break;
        case "turn_complete": t.action(id, { type: "end_turn" }); break;
        default: throw new Error(`finishTurn: phase ${g.phase}`);
      }
    }
  }

  // 1. Paying bail without the cash: debt with after.kind = "leaveJail".
  const p = cur();
  const ps = seatOf(p.id);
  const b1 = other(1);
  const c1 = other(2);
  t.action(p.id, { type: "pay_bail" }); // not in jail
  t.action(p.id, { type: "use_jail_card" }); // not in jail
  t.mutate([P(ps, "inJail", true), P(ps, "position", 10), P(ps, "cash", 20), prop(1, "ownerId", p.id), prop(3, "ownerId", p.id)]);
  t.action(p.id, { type: "use_jail_card" }); // no card
  t.action(p.id, { type: "pay_bail" });
  expectPhase("debt");
  t.action(p.id, { type: "build", spaceIndex: 1 }); // in debt
  t.action(p.id, { type: "unmortgage", spaceIndex: 1 }); // in debt
  t.action(b1.id, { type: "mortgage", spaceIndex: 1 }); // not your turn
  t.action(b1.id, { type: "pay_debt" }); // someone else's debt
  t.action(b1.id, { type: "bankrupt" }); // cannot declare
  t.action(p.id, { type: "end_turn" }); // wrong phase
  t.action(p.id, { type: "pay_debt" }); // not enough cash
  t.action(p.id, { type: "mortgage", spaceIndex: 1 });
  t.action(p.id, { type: "pay_debt" });
  expectPhase("awaiting_roll");
  t.action(p.id, { type: "unmortgage", spaceIndex: 1 }); // not enough cash
  t.action(p.id, { type: "roll", dice: [1, 2] }); // Theater Street
  expectPhase("awaiting_purchase");
  t.action(p.id, { type: "buy" }); // not enough cash
  t.action(p.id, { type: "build", spaceIndex: 3 }); // group has a mortgage
  t.action(p.id, { type: "decline" });
  expectPhase("auction");
  t.action(b1.id, { type: "bid", amount: 10 });
  t.action(c1.id, { type: "pass_auction" });
  t.action(p.id, { type: "bid", amount: 1 }); // must exceed
  t.action(p.id, { type: "pass_auction" });
  expectPhase("turn_complete");
  t.action(p.id, { type: "end_turn" });

  // 2. Third failed jail roll without the fee: debt with after.kind = "jailMove".
  const q = cur();
  const qs = seatOf(q.id);
  t.mutate([P(qs, "inJail", true), P(qs, "position", 10), P(qs, "jailTurns", 2), P(qs, "cash", 10), prop(39, "ownerId", q.id)]);
  t.action(q.id, { type: "roll", dice: [1, 2] });
  expectPhase("debt");
  t.action(q.id, { type: "mortgage", spaceIndex: 39 });
  t.action(q.id, { type: "pay_debt" });
  finishTurn();

  // 3. Bank supply limits and build/sell/mortgage guards.
  const r = cur();
  const rs = seatOf(r.id);
  const supplySets = [P(rs, "cash", 5000)];
  for (const index of [1, 3, 6, 8, 9, 11, 13, 14, 16, 18, 19]) {
    supplySets.push(prop(index, "ownerId", r.id), prop(index, "mortgaged", false));
  }
  for (const index of [1, 3, 6, 8, 9, 11, 13, 14]) supplySets.push(prop(index, "houses", 4));
  t.mutate(supplySets);
  t.action(r.id, { type: "build", spaceIndex: 16 }); // no houses left
  t.action(r.id, { type: "build", spaceIndex: 1 }); // hotel
  t.action(r.id, { type: "build", spaceIndex: 1 }); // uneven
  t.action(r.id, { type: "build", spaceIndex: 3 }); // hotel
  t.action(r.id, { type: "build", spaceIndex: 3 }); // already a hotel
  t.mutate([prop(16, "houses", 2), prop(18, "houses", 2), prop(19, "houses", 2)]);
  t.action(r.id, { type: "sell_building", spaceIndex: 1 }); // cannot break down the hotel
  t.action(r.id, { type: "sell_building", spaceIndex: 16 });
  t.action(r.id, { type: "sell_building", spaceIndex: 16 }); // uneven
  t.action(r.id, { type: "mortgage", spaceIndex: 18 }); // group has buildings
  const holder = g.players.find((player) => player.id !== r.id);
  const hotelSets = [];
  for (const index of [21, 23, 24, 26, 27, 29, 31, 32, 34, 37]) {
    hotelSets.push(prop(index, "ownerId", holder.id), prop(index, "houses", 5));
  }
  t.mutate(hotelSets);
  t.action(r.id, { type: "build", spaceIndex: 6 }); // no hotels left
  t.mutate([prop(19, "mortgaged", true)]);
  t.action(r.id, { type: "build", spaceIndex: 16 }); // group has a mortgage
  t.mutate([P(rs, "cash", 10), prop(19, "mortgaged", false), prop(39, "ownerId", r.id)]);
  t.action(r.id, { type: "build", spaceIndex: 16 }); // not enough cash
  t.action(r.id, { type: "build", spaceIndex: 5 }); // not a property
  t.action(r.id, { type: "build", spaceIndex: 21 }); // not the owner
  t.action(r.id, { type: "build", spaceIndex: 39 }); // incomplete group
  t.action(r.id, { type: "mortgage", spaceIndex: 2 }); // not ownable
  t.action(r.id, { type: "mortgage", spaceIndex: 21 }); // not the owner
  t.action(r.id, { type: "mortgage", spaceIndex: 39 }); // already mortgaged
  t.action(r.id, { type: "unmortgage", spaceIndex: 2 });
  t.action(r.id, { type: "sell_building", spaceIndex: 5 });
  t.action(r.id, { type: "sell_building", spaceIndex: 39 });
  t.mutate([P(rs, "cash", 5000), P(rs, "position", 4), { path: ["decks", "chance"], value: deckWithTop("chance", "chance-repairs") }]);
  t.action(r.id, { type: "roll", dice: [1, 2] });
  expectPhase("card_confirmation");
  t.action(q.id, { type: "confirm_card" }); // not the drawer
  t.action(r.id, { type: "confirm_card" }); // street repairs with houses and hotels
  finishTurn();

  // 4. Chest jail card, utility rent, a throw after partial mutation.
  const u = cur();
  const us = seatOf(u.id);
  t.mutate([
    P(us, "cash", 1000), P(us, "position", 0),
    { path: ["decks", "chest"], value: deckWithTop("chest", "chest-jail-card") },
    prop(12, "ownerId", r.id), prop(28, "ownerId", r.id), prop(12, "mortgaged", false), prop(28, "mortgaged", false),
  ]);
  t.action(u.id, { type: "roll", dice: [1, 1] }); // Post -> Jail pass
  expectPhase("awaiting_roll");
  t.action(u.id, { type: "roll", dice: [4, 6] }); // Power Station, both utilities owned
  expectPhase("turn_complete");
  const goodChest = g.decks.chest.slice();
  t.mutate([set("phase", "awaiting_roll"), { path: ["decks", "chest"], value: ["bogus-card"] }, P(us, "position", 15)]);
  t.action(u.id, { type: "roll", dice: [1, 1] }); // throws "Invalid card data" after moving
  t.mutate([{ path: ["decks", "chest"], value: goodChest }, set("phase", "card_confirmation"),
    set("pendingCard", { playerId: u.id, deck: "chance", cardId: "bogus", text: "Bogus" })]);
  t.action(u.id, { type: "confirm_card" }); // invalid card
  t.mutate([set("phase", "turn_complete"), set("pendingCard", null), P(us, "bankrupt", true)]);
  t.action(u.id, { type: "end_turn" }); // bankrupt player
  t.mutate([P(us, "bankrupt", false), P(us, "inJail", true), P(us, "position", 10), set("phase", "awaiting_roll")]);
  t.action(u.id, { type: "use_jail_card" });
  expectPhase("awaiting_roll");
  finishTurn();

  // 5. Trades re-validated on accept.
  const v = cur();
  const vs = seatOf(v.id);
  const w = other(1);
  const ws = seatOf(w.id);
  t.mutate([
    prop(21, "houses", 0), prop(23, "houses", 0), prop(24, "houses", 0),
    prop(24, "ownerId", w.id), prop(24, "mortgaged", true), P(vs, "cash", 1000), P(ws, "cash", 1000),
  ]);
  const offer = { type: "offer_trade", targetId: w.id, offer: { cash: 100, properties: [] }, request: { cash: 0, properties: [24] } };
  t.action(v.id, offer);
  expectPhase("trade");
  t.action(v.id, { type: "accept_trade" }); // only the recipient
  t.action(other(2).id, { type: "reject_trade" }); // not a party
  t.mutate([P(vs, "cash", 50)]);
  t.action(w.id, { type: "accept_trade" }); // proposer lacks cash
  t.mutate([P(vs, "cash", 105)]);
  t.action(w.id, { type: "accept_trade" }); // cannot pay the transfer fee
  t.mutate([prop(24, "ownerId", v.id)]);
  t.action(w.id, { type: "accept_trade" }); // no longer belongs
  t.mutate([prop(24, "ownerId", w.id), P(ws, "bankrupt", true)]);
  t.action(w.id, { type: "accept_trade" }); // invalid players
  t.mutate([P(ws, "bankrupt", false), P(vs, "cash", 1000)]);
  t.action(w.id, { type: "accept_trade" });
  expectPhase("awaiting_roll");
  t.mutate([set("trade", {
    id: "trade_x", proposerId: w.id, targetId: v.id, offer: { cash: 1, properties: [] },
    request: { cash: 1, properties: [] }, returnPhase: "awaiting_roll", createdAt: 0,
  })]);
  t.action(v.id, offer); // another trade pending
  t.mutate([set("trade", null), prop(27, "ownerId", w.id)]);
  t.action(v.id, { type: "offer_trade", targetId: w.id, offer: { cash: 10 }, request: { properties: [27] } });
  t.action(v.id, { type: "offer_trade", targetId: w.id, offer: { cash: 5000 }, request: { cash: 1 } });
  t.action(v.id, { type: "offer_trade", targetId: w.id, offer: { cash: 1 }, request: { cash: 5000 } });
  t.action(v.id, { type: "offer_trade", targetId: w.id, offer: { cash: 1, properties: [5] }, request: { cash: 1 } });
  t.action(v.id, { type: "offer_trade", targetId: w.id, offer: { cash: 0 }, request: { cash: 1 } });
  finishTurn();

  // 6. Bankruptcy to the bank with buildings and a mortgage.
  const x = cur();
  const xs = seatOf(x.id);
  const bankruptSets = [P(xs, "cash", 0), P(xs, "position", 2), P(xs, "inJail", false), prop(5, "ownerId", x.id), prop(5, "mortgaged", true)];
  for (const index of [6, 8, 9]) bankruptSets.push(prop(index, "ownerId", x.id), prop(index, "houses", 3), prop(index, "mortgaged", false));
  t.mutate(bankruptSets);
  t.action(x.id, { type: "roll", dice: [1, 1] }); // Stamp Duty
  expectPhase("debt");
  t.action(x.id, { type: "bankrupt" });
  t.publicView(cur().id, { role: "player" });

  // 7. Final bankruptcy to a player ends the game.
  const y = cur();
  const ys = seatOf(y.id);
  const winner = g.players.find((player) => player.id !== y.id && !player.bankrupt);
  t.mutate([P(ys, "cash", 0), P(ys, "position", 37), prop(39, "ownerId", winner.id), prop(39, "mortgaged", false), prop(37, "ownerId", null)]);
  t.action(y.id, { type: "roll", dice: [1, 1] });
  expectPhase("debt");
  t.action(y.id, { type: "bankrupt" });
  t.action(winner.id, { type: "roll" }); // finished
  t.publicView(winner.id, { role: "player", name: winner.name }, true);
  if (g.status !== "finished") throw new Error("scripted-edges: game did not finish");
  return trace;
}

function auctionActor(state) {
  return nextActorId(state);
}

// ----------------------------------------------------------------- main ---

const CHECK_ONLY = process.argv.includes("--check");
const ONLY_SCRIPTED = process.argv.includes("--scripted-only");

function main() {
  if (!CHECK_ONLY) {
    fs.mkdirSync(OUT_DIR, { recursive: true });
    for (const file of fs.readdirSync(OUT_DIR)) {
      if (file.endsWith(".json")) fs.unlinkSync(path.join(OUT_DIR, file));
    }
  }
  const games = [];
  const aiCounts = [2, 3, 4, 5, 6];
  let seed = 1;
  for (let round = 0; round < 6; round += 1) {
    for (const count of aiCounts) {
      games.push({
        name: `ai-${count}p-s${seed}`,
        description: `${count} AI players, random dice`,
        seed,
        kinds: Array(count).fill("ai"),
        viaSpectatorHost: round % 2 === 0,
        publicEvery: round === 0 ? 97 : 0,
        raw: round === 0 ? 10 : 0,
      });
      seed += 1;
    }
  }
  const mixed = [
    ["human", "ai"], ["ai", "human", "ai"], ["human", "human", "ai", "ai"], ["human", "ai", "human", "ai", "human"],
    ["human", "ai", "ai", "ai", "ai", "human"], ["human", "human"], ["human", "human", "human"],
  ];
  for (const diceMode of ["random", "choice"]) {
    for (const kinds of mixed) {
      const label = kinds.every((kind) => kind === "human") ? "human" : "mixed";
      games.push({
        name: `${label}-${kinds.length}p-${diceMode}-s${seed}`,
        description: `${kinds.join("/")} players (humans follow a seeded random policy), ${diceMode} dice`,
        seed,
        kinds,
        diceMode,
        publicEvery: 211,
        raw: 10,
      });
      seed += 1;
    }
  }

  const traces = [scriptedLobby(), scriptedCoercion(), scriptedEdges(), ...(ONLY_SCRIPTED ? [] : games.map(playGame))];
  let totalBytes = 0;
  let totalSteps = 0;
  const coverage = new Map();
  for (const trace of traces) {
    const bytes = trace.write();
    totalBytes += bytes;
    totalSteps += trace.data.steps.length;
    for (const step of trace.data.steps) {
      const key = step.op === "action"
        ? `${typeof step.action?.type === "string" ? step.action.type : "?"}${step.error ? " (error)" : ""}`
        : step.op;
      coverage.set(key, (coverage.get(key) || 0) + 1);
      for (const text of step.logs || []) {
        for (const [label, pattern] of LOG_PATTERNS) {
          if (pattern.test(text)) coverage.set(`log: ${label}`, (coverage.get(`log: ${label}`) || 0) + 1);
        }
      }
    }
    const status = trace.data.finished ? "finished" : (trace.data.stuck || (trace.name.startsWith("scripted") ? "scripted" : "capped"));
    console.log(`${trace.name.padEnd(28)} steps=${String(trace.data.steps.length).padStart(5)} ${status} ${(bytes / 1024).toFixed(0)}KB`);
  }
  console.log(`\n${traces.length} traces, ${totalSteps} steps, ${(totalBytes / 1024 / 1024).toFixed(2)} MB`);
  for (const [key, count] of [...coverage.entries()].sort()) console.log(`  ${key.padEnd(44)} ${count}`);
}

const LOG_PATTERNS = [
  ["mortgage transfer fee", /mortgage transfer fee/],
  ["bankrupt to bank", /assets returned to the bank/],
  ["bankrupt to player", /assets transferred to/],
  ["paid bail", /paid bail and left jail/],
  ["third failed roll", /third failed doubles roll/],
  ["jail move after fee", /paid bail, left jail, and moved/],
  ["jail pass used", /used a Jail pass/],
  ["doubles left jail", /rolled doubles and left jail/],
  ["three doubles", /rolled doubles three times/],
  ["trade cancelled", /cancelled the trade/],
  ["trade declined", /declined the trade/],
  ["trade accepted", /accepted .*'s trade/],
  ["building sold", /sold one building/],
  ["hotel built", /built a hotel/],
  ["unmortgaged", /unmortgaged/],
  ["auction won", /won .* for \$/],
  ["no bids", /received no bids/],
  ["must raise", /must raise/],
  ["repairs", /building repairs/],
  ["tie reroll", /tied and will roll again/],
  ["wins", /wins the game/],
  ["mortgaged no rent", /is mortgaged; no rent/],
  ["passed PAYDAY", /passed PAYDAY/],
];

main();
