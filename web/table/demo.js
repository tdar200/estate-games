// Demo fixtures for the PC table (?demo=<name>). Pure: no DOM. A fixture is either a plain one
// ({ state } or { replay }) or builds on another:
//
//   {
//     "extends": "pad-turn",                 base fixture (may itself extend another)
//     "patch": { … },                        merged into the base state (see applyPatch)
//     "seats": { "Maya": "keyboard" },       who sits at this screen: keyboard | controller seats
//                                            (also phone | ai to change a player's device)
//     "then": [{ "patch": { … }, "after": 900 }, …]
//                                            later states, each relative to the one before; the
//                                            table animates into them like a live update
//     "ctx": { "pads", "games", "server", "status", "aiLevel", "watching" }
//                                            screen context set before the first render
//     "ui": { "open", "focus", "type", "toast", "gate", "loading", "watchPrompt", "close" }
//                                            what the screen does once the states are in
//     "meta": { "title", "section", "description", "capture" }
//   }
//
// Patch rules: objects merge key by key; arrays and plain values replace; a key ending in "+"
// appends to an array ("log+": [...]); null sets null. "players" may be an object keyed by name
// (or id) to change those players, with null removing one; "players+" adds players. Any string
// "@Name" becomes that player's id. Log entries and moves without an id get one.

const isObject = (value) => value !== null && typeof value === "object" && !Array.isArray(value);
let serial = 0;

function merge(base, patch) {
  if (!isObject(patch)) return patch;
  const out = isObject(base) ? { ...base } : {};
  for (const [key, value] of Object.entries(patch)) {
    if (key.endsWith("+") && Array.isArray(value)) {
      const name = key.slice(0, -1);
      out[name] = [...(Array.isArray(out[name]) ? out[name] : []), ...value];
    } else out[key] = merge(out[key], value);
  }
  return out;
}

function resolver(players) {
  const ids = new Map(players.map((player) => [player.name, player.id]));
  const resolve = (value) => {
    if (typeof value === "string" && value.startsWith("@") && ids.has(value.slice(1))) return ids.get(value.slice(1));
    if (Array.isArray(value)) return value.map(resolve);
    if (isObject(value)) return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, resolve(item)]));
    return value;
  };
  return resolve;
}

function findPlayer(players, key) {
  return players.findIndex((player) => player.id === key || player.name === key);
}

// Ids for log entries and moves written without one (receipts and animations key on them).
function stampIds(state) {
  for (const entry of state.log || []) if (!entry.id) entry.id = `log_demo_${(serial += 1)}`;
  for (const move of state.turn?.movements || []) if (!move.id) move.id = `move_demo_${(serial += 1)}`;
  if (state.lastMove && !state.lastMove.id) state.lastMove.id = `move_demo_${(serial += 1)}`;
  if (state.lastLanding && !state.lastLanding.id) state.lastLanding.id = `landing_demo_${(serial += 1)}`;
  return state;
}

export function applyPatch(state, patch = {}) {
  let players = (state.players || []).map((player) => ({ ...player }));
  if (Array.isArray(patch.players)) players = patch.players.map((player) => ({ ...player }));
  else if (isObject(patch.players)) {
    for (const [key, change] of Object.entries(patch.players)) {
      const at = findPlayer(players, key);
      if (at < 0) continue;
      if (change === null) players.splice(at, 1);
      else players[at] = merge(players[at], change);
    }
  }
  if (Array.isArray(patch["players+"])) players.push(...patch["players+"].map((player) => ({ ...player })));
  const resolve = resolver(players);
  players = players.map((player) => resolve(player));
  let next = { ...state, players };
  for (const [key, value] of Object.entries(patch)) {
    if (key === "players" || key === "players+") continue;
    next = merge(next, { [key]: resolve(value) });
  }
  return stampIds(structuredClone(next));
}

// Who sits at this screen. Keyboard/controller players become local seats (pads numbered in seat
// order); "phone" / "ai" change a player's device.
export function withSeats(state, seats) {
  if (!seats) return state;
  const players = state.players.map((player) => {
    const input = seats[player.name] ?? seats[player.id];
    if (!input) return player;
    return { ...player, input, kind: input === "ai" ? "ai" : "human" };
  });
  let pad = 0;
  const localSeats = players
    .filter((player) => player.input === "keyboard" || player.input === "controller")
    .map((player) => ({ seatId: `seat_${player.id}`, playerId: player.id, input: player.input, pad: player.input === "controller" ? pad++ : null }));
  return { ...state, players, viewerRole: "table", viewerId: null, localSeats };
}

function baseState(fixture) {
  if (fixture.state) return fixture.state;
  if (Array.isArray(fixture.replay) && fixture.replay.length) {
    return { ...fixture.replay[fixture.replay.length - 1], board: fixture.board, groups: fixture.groups };
  }
  return null;
}

// Load a fixture and everything it extends. `load(name)` fetches one fixture's JSON.
export async function composeFixture(name, load, depth = 0) {
  const fixture = await load(name);
  if (!fixture.extends || depth > 4) return { ...fixture, then: [], ctx: fixture.ctx || {}, ui: fixture.ui || {} };
  const base = await composeFixture(fixture.extends, load, depth + 1);
  let state = baseState(base);
  if (!state) throw new Error(`Demo fixture "${fixture.extends}" has no state`);
  state = withSeats(applyPatch(state, fixture.patch || {}), fixture.seats);
  const then = [];
  let previous = state;
  for (const step of fixture.then || []) {
    const next = withSeats(applyPatch(previous, step.patch || {}), step.seats || fixture.seats);
    next.version = (Number(previous.version) || 0) + 1;
    then.push({ state: next, after: step.after ?? 900 });
    previous = next;
  }
  return {
    meta: fixture.meta || {},
    state,
    then,
    ctx: { ...(base.ctx || {}), ...(fixture.ctx || {}) },
    ui: fixture.ui || {},
  };
}

// "@Name" strings → player ids (for UI steps such as a trade draft's partner).
export function resolveNames(value, players = []) {
  return resolver(players)(value);
}
