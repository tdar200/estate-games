// Pure game-insight helpers: odds, risk, debt plans, payback and trade effects.
// They read the engine's publicState shape (board, properties, players, bank, turn) and never
// touch the DOM, so they can be unit-tested in Node.

import { colourWord, listNames, money, ordinal, pct, playerById, possessive } from "./format.js";

const BOARD_SIZE = 40;
const RAILROADS = [5, 15, 25, 35];
const UTILITIES = [12, 28];
const GO_TO_JAIL = 30;
const JAIL = 10;

// ---------------------------------------------------------------------------------------------
// Basic lookups

export function propState(state, index) {
  return state?.properties?.[index] ?? state?.properties?.[String(index)] ?? null;
}

export function spaceAt(state, index) {
  return state?.board?.[index] ?? null;
}

export function groupIndexes(state, group) {
  return (state?.board || []).filter((space) => space.type === "property" && space.group === group).map((space) => space.index);
}

export function ownableIndexes(state) {
  return (state?.board || []).filter((space) => ["property", "railroad", "utility"].includes(space.type)).map((space) => space.index);
}

export function ownedBy(state, playerId) {
  return ownableIndexes(state).filter((index) => propState(state, index)?.ownerId === playerId);
}

export function ownsGroup(state, playerId, group) {
  const indexes = groupIndexes(state, group);
  return indexes.length > 0 && indexes.every((index) => propState(state, index)?.ownerId === playerId);
}

export function fullSets(state, playerId) {
  const groups = [...new Set((state?.board || []).filter((space) => space.type === "property").map((space) => space.group))];
  return groups.filter((group) => ownsGroup(state, playerId, group));
}

function countOfType(state, ownerId, type) {
  const list = type === "railroad" ? RAILROADS : UTILITIES;
  return list.filter((index) => propState(state, index)?.ownerId === ownerId).length;
}

function groupHasBuildings(state, group) {
  return groupIndexes(state, group).some((index) => (propState(state, index)?.houses || 0) > 0);
}

// ---------------------------------------------------------------------------------------------
// Rent (mirrors calculateRent in src/game.js)

// Returns the rent a visitor would pay, or null when nobody owns it. Mortgaged → 0.
// Utilities need the dice total; without one the average roll (7) is used.
export function rentFor(state, index, { diceTotal = 7, railroadMultiplier = 1, utilityMultiplier = null } = {}) {
  const space = spaceAt(state, index);
  const prop = propState(state, index);
  if (!space || !prop?.ownerId) return null;
  if (prop.mortgaged) return 0;
  const owner = playerById(state, prop.ownerId);
  if (owner?.bankrupt) return 0;
  if (space.type === "railroad") {
    const count = countOfType(state, prop.ownerId, "railroad");
    return 25 * 2 ** (count - 1) * railroadMultiplier;
  }
  if (space.type === "utility") {
    const count = countOfType(state, prop.ownerId, "utility");
    return diceTotal * (utilityMultiplier || (count === 2 ? 10 : 4));
  }
  const houses = prop.houses || 0;
  let rent = space.rents[houses];
  if (houses === 0 && ownsGroup(state, prop.ownerId, space.group)) rent *= 2;
  return rent;
}

// Rent set by a "nearest" Headlines card (docs/API.md §3c "RENT ×2 / ×10"): the rent pay event
// and state.lastLanding carry base, multiplier (2 railroad card, 10 utility card) and, for the
// utility, the fresh roll as dice. {multiplier, base, dice, amount}, or null for ordinary rent.
export function rentMultiple(entry) {
  const multiplier = Number(entry?.multiplier);
  const base = Number(entry?.base);
  if (!(multiplier > 1) || !Number.isFinite(base)) return null;
  const dice = Array.isArray(entry.dice) && entry.dice.length === 2 && entry.dice.every(Number.isFinite) ? entry.dice : null;
  const amount = Number.isFinite(Number(entry.amount)) ? Number(entry.amount) : base * multiplier;
  return { multiplier, base, dice, amount };
}

export function utilityMultiplier(state, index) {
  const prop = propState(state, index);
  if (!prop?.ownerId) return null;
  return countOfType(state, prop.ownerId, "utility") === 2 ? 10 : 4;
}

// Short label for a board square: "$30", "4× DICE", "MORTGAGED", or "" when unowned.
export function rentLabel(state, index) {
  const space = spaceAt(state, index);
  const prop = propState(state, index);
  if (!space || !prop?.ownerId) return "";
  if (prop.mortgaged) return "MORTGAGED";
  if (space.type === "utility") return `${utilityMultiplier(state, index)}× DICE`;
  return money(rentFor(state, index));
}

// Words for the rent level: "RENT WITH HOTEL", "RENT WITH 3 HOUSES", "FULL-SET RENT", "RENT".
export function rentLevelLabel(state, index) {
  const space = spaceAt(state, index);
  const prop = propState(state, index);
  if (!space || !prop) return "RENT";
  if (space.type === "railroad") return `RENT · ${countOfType(state, prop.ownerId, "railroad")} RAILROADS`;
  if (space.type === "utility") return `RENT · ${utilityMultiplier(state, index)}× THE DICE`;
  if (prop.houses === 5) return "RENT WITH HOTEL";
  if (prop.houses > 0) return `RENT WITH ${prop.houses} HOUSE${prop.houses === 1 ? "" : "S"}`;
  return ownsGroup(state, prop.ownerId, space.group) ? "FULL-SET RENT" : "RENT";
}

// ---------------------------------------------------------------------------------------------
// Dice odds

export function diceOdds() {
  const odds = [];
  for (let total = 2; total <= 12; total += 1) odds.push({ total, p: (6 - Math.abs(total - 7)) / 36 });
  return odds;
}

// Where a 2d6 roll from `from` lands: [{ total, square, p }].
export function landingOdds(from) {
  return diceOdds().map(({ total, p }) => ({ total, p, square: (from + total) % BOARD_SIZE }));
}

export function probabilityOfReaching(from, squares) {
  const set = new Set(squares);
  return landingOdds(from).reduce((sum, outcome) => sum + (set.has(outcome.square) ? outcome.p : 0), 0);
}

// ---------------------------------------------------------------------------------------------
// Next-roll risk

function threatKey(space, ownerId) {
  if (space.type === "property") return `${ownerId}:${space.group}`;
  return `${ownerId}:${space.type}`;
}

function threatLabel(state, threat) {
  const owner = playerById(state, threat.ownerId);
  const name = possessive(owner?.name || "Someone");
  const first = spaceAt(state, threat.squares[0]);
  if (threat.kind === "property") {
    if (ownsGroup(state, threat.ownerId, threat.group)) return `${name} ${colourWord(threat.group)} set`;
    if (threat.squares.length === 1) return `${name} ${first.name}`;
    return `${name} ${colourWord(threat.group)} streets`;
  }
  if (threat.kind === "railroad") return threat.squares.length === 1 ? `${name} ${first.name}` : `${name} railroads`;
  return threat.squares.length === 1 ? `${name} ${first.name}` : `${name} utilities`;
}

// What the next roll could cost `playerId`, using plain 2d6 odds from `from` (default: position).
export function nextRollRisk(state, playerId, { from = null } = {}) {
  const player = playerById(state, playerId);
  const start = from ?? player?.position ?? 0;
  const outcomes = [];
  const threats = new Map();
  let pRent = 0;
  let pJail = 0;
  let maxRent = 0;
  let worst = null;
  for (const outcome of landingOdds(start)) {
    const space = spaceAt(state, outcome.square);
    const prop = propState(state, outcome.square);
    let rent = 0;
    let tax = 0;
    if (prop?.ownerId && prop.ownerId !== playerId) {
      rent = rentFor(state, outcome.square, { diceTotal: outcome.total }) || 0;
    }
    if (space?.type === "tax") tax = space.amount || 0;
    if (outcome.square === GO_TO_JAIL) pJail += outcome.p;
    const cost = rent + tax;
    outcomes.push({ ...outcome, rent, tax, cost });
    if (rent > 0) {
      pRent += outcome.p;
      maxRent = Math.max(maxRent, rent);
      const key = threatKey(space, prop.ownerId);
      const threat = threats.get(key) || { key, ownerId: prop.ownerId, kind: space.type, group: space.group || null, p: 0, max: 0, squares: [] };
      threat.p += outcome.p;
      threat.max = Math.max(threat.max, rent);
      if (!threat.squares.includes(outcome.square)) threat.squares.push(outcome.square);
      threats.set(key, threat);
    }
    if (!worst || cost > worst.cost) worst = { square: outcome.square, cost, rent, tax, p: outcome.p };
  }
  const list = [...threats.values()]
    .map((threat) => ({ ...threat, label: threatLabel(state, threat) }))
    .sort((a, b) => b.p * b.max - a.p * a.max);
  return { from: start, outcomes, threats: list, pRent, pJail, maxRent, worst, cash: player?.cash ?? 0 };
}

// Visual replacement for the risk sentence: squares 2–12 ahead that cost money (rent, tax,
// Busted), each with its landing chance and a 0–1 `level` for a heat wash (1 = the 7, the likeliest
// roll). `alert` is the worst cost when it is worth a "⚠ $X ahead" chip (big, or more than a
// quarter of the player's cash), else 0. Feed `squares` to Board#highlightSquares(…, "risk").
export function nextRollHeat(state, playerId, { from = null } = {}) {
  const risk = nextRollRisk(state, playerId, { from });
  const squares = [];
  for (const outcome of risk.outcomes) {
    const jail = outcome.square === GO_TO_JAIL;
    if (!outcome.cost && !jail) continue;
    if (squares.some((entry) => entry.index === outcome.square)) continue;
    squares.push({ index: outcome.square, p: outcome.p, cost: outcome.cost, jail, level: Math.min(1, outcome.p / (6 / 36)) });
  }
  const worst = squares.reduce((max, entry) => Math.max(max, entry.cost), 0);
  const alert = worst >= 150 || (worst > 0 && worst >= risk.cash * 0.25) ? worst : 0;
  return { squares, worst, alert, pJail: risk.pJail, cash: risk.cash };
}

// One sentence for the risk tip. tone: "warn" | "ok".
export function riskLine(state, playerId, { jail = false } = {}) {
  const risk = nextRollRisk(state, playerId);
  const top = risk.threats[0] || null;
  const cash = risk.cash;
  const big = top && (top.max >= 150 || top.max >= cash * 0.25);
  const squares = top ? top.squares : [];
  if (jail) {
    if (top && big && top.p >= 0.15) {
      return { tone: "warn", squares, text: `Rolling out could cost you: ${pct(top.p)} chance of ${top.label} (rent up to ${money(top.max)}).` };
    }
    if (top) return { tone: "ok", squares, text: `Staying in is safe right now: ${pct(top.p)} chance of ${top.label}.` };
    return { tone: "ok", squares, text: "Staying in is safe right now: nothing in reach charges rent." };
  }
  if (top && big) {
    const cover = cash >= top.max ? "You can cover it." : `That's more than your ${money(cash)} cash.`;
    return { tone: "warn", squares, text: `${pct(top.p)} chance of landing on ${top.label} (rent up to ${money(top.max)}). ${cover}` };
  }
  const jailNote = risk.pJail >= 0.05 ? ` Busted is ${pct(risk.pJail)} likely.` : "";
  if (risk.maxRent > 0) {
    return { tone: "ok", squares, text: `Next roll looks safe: nothing ahead charges more than ${money(risk.maxRent)}.${jailNote}` };
  }
  return { tone: "ok", squares, text: `Next roll looks safe: nothing ahead charges rent.${jailNote}` };
}

// Highest bid that still leaves enough cash for the worst square of the next roll.
export function bidCap(state, playerId) {
  const risk = nextRollRisk(state, playerId);
  return Math.max(0, risk.cash - (risk.worst?.cost || 0));
}

// ---------------------------------------------------------------------------------------------
// Purchases

// { strong, text } explaining why a square matters to `playerId`.
export function purchaseInsight(state, playerId, index) {
  const space = spaceAt(state, index);
  if (!space) return null;
  if (space.type === "railroad") {
    const mine = countOfType(state, playerId, "railroad");
    return {
      strong: `Railroad ${mine + 1} of 4.`,
      text: `With ${mine + 1} railroad${mine ? "s" : ""}, each one charges ${money(25 * 2 ** mine)}.`,
    };
  }
  if (space.type === "utility") {
    const mine = countOfType(state, playerId, "utility");
    return mine
      ? { strong: "Both utilities.", text: "Rent becomes 10× the dice on each." }
      : { strong: "Utility.", text: "Rent is 4× the dice, or 10× with both utilities." };
  }
  if (space.type !== "property") return null;
  const others = groupIndexes(state, space.group).filter((candidate) => candidate !== index);
  const owners = others.map((candidate) => propState(state, candidate)?.ownerId || null);
  const colour = colourWord(space.group);
  if (owners.every((ownerId) => ownerId === playerId)) {
    return { strong: "Completes your set.", text: `${capitalise(colour)} rent doubles, and you can start building.` };
  }
  const rivals = [...new Set(owners.filter((ownerId) => ownerId && ownerId !== playerId))];
  if (rivals.length === 1 && !owners.includes(playerId)) {
    const rival = playerById(state, rivals[0]);
    const theirs = others.filter((candidate) => propState(state, candidate)?.ownerId === rival.id).map((candidate) => spaceAt(state, candidate).name);
    return {
      strong: "Blocks a set.",
      text: `${rival.name} owns ${listNames(theirs)}; without ${space.name}, ${rival.name} can never complete ${colour}.`,
    };
  }
  const mineCount = owners.filter((ownerId) => ownerId === playerId).length;
  if (mineCount > 0) {
    return {
      strong: `${mineCount + 1} of ${others.length + 1}.`,
      text: rivals.length ? `Someone else holds the last ${colour} street, so you'd need a trade.` : `One more ${colour} street and you can build.`,
    };
  }
  return { strong: "Rent", text: `${money(space.rents[0])} now, up to ${money(space.rents[5])} with a hotel if you ever get the set.` };
}

function capitalise(text) {
  return text ? text[0].toUpperCase() + text.slice(1) : text;
}

// ---------------------------------------------------------------------------------------------
// Building

function bankAllows(state, houses) {
  const bank = state?.bank || { houses: 32, hotels: 12 };
  return houses === 4 ? bank.hotels >= 1 : bank.houses >= 1;
}

// Best next house on a group (respecting even building). null if building is not possible.
export function nextBuild(state, playerId, group) {
  if (!ownsGroup(state, playerId, group)) return null;
  const indexes = groupIndexes(state, group);
  if (indexes.some((index) => propState(state, index).mortgaged)) return null;
  const levels = indexes.map((index) => propState(state, index).houses || 0);
  const min = Math.min(...levels);
  if (min >= 5) return null;
  const player = playerById(state, playerId);
  let best = null;
  for (const index of indexes) {
    if ((propState(state, index).houses || 0) !== min) continue;
    const payback = housePayback(state, index);
    if (!payback) continue;
    if (!best || payback.delta > best.delta) best = payback;
  }
  if (!best) return null;
  return { ...best, affordable: (player?.cash ?? 0) >= best.cost, bankOk: bankAllows(state, min) };
}

// Cost and rent gain of the next house/hotel on one street.
export function housePayback(state, index) {
  const space = spaceAt(state, index);
  const prop = propState(state, index);
  if (!space || space.type !== "property" || !prop?.ownerId) return null;
  const houses = prop.houses || 0;
  if (houses >= 5) return null;
  const rentNow = houses === 0 && ownsGroup(state, prop.ownerId, space.group) ? space.rents[0] * 2 : space.rents[houses];
  const rentNext = space.rents[houses + 1];
  const delta = rentNext - rentNow;
  return {
    index,
    cost: space.buildCost,
    houses,
    hotel: houses === 4,
    rentNow,
    rentNext,
    delta,
    landings: delta > 0 ? Math.ceil(space.buildCost / delta) : null,
  };
}

export function buildOptions(state, playerId) {
  return fullSets(state, playerId).map((group) => nextBuild(state, playerId, group)).filter(Boolean);
}

// The building to sell next on a group (even selling: from the tallest street).
export function nextSale(state, playerId, group) {
  const indexes = groupIndexes(state, group).filter((index) => propState(state, index)?.ownerId === playerId);
  const levels = indexes.map((index) => propState(state, index).houses || 0);
  const max = Math.max(0, ...levels);
  if (max === 0) return null;
  if (max === 5 && (state?.bank?.houses ?? 32) < 4) return null;
  const index = indexes.find((candidate) => (propState(state, candidate).houses || 0) === max);
  const space = spaceAt(state, index);
  return { index, refund: Math.floor(space.buildCost / 2), houses: max };
}

export function mortgageValue(space) {
  return Math.floor(space.price / 2);
}

// The engine's UnmortgageCost: half the price plus 10%, rounded up, in whole numbers. (The float
// version, ceil(price / 2 * 1.1), showed $1 too much for $100, $180, $200, $220 and $400 deeds.)
export function unmortgageCost(space) {
  return Math.ceil((space.price * 11) / 20);
}

export function canMortgage(state, playerId, index) {
  const space = spaceAt(state, index);
  const prop = propState(state, index);
  if (!space || !prop || prop.ownerId !== playerId || prop.mortgaged) return false;
  return space.type !== "property" || !groupHasBuildings(state, space.group);
}

// Can this player manage property right now (engine canManage)?
export function canManage(state, playerId) {
  if (!state || state.status !== "playing") return false;
  if (state.phase === "debt") return state.debt?.payerId === playerId;
  return state.currentPlayerId === playerId && ["awaiting_roll", "awaiting_purchase", "turn_complete"].includes(state.phase);
}

// ---------------------------------------------------------------------------------------------
// Debt plan: raise `needed` while losing the least rent, respecting even selling and the rule that
// streets in a group with buildings cannot be mortgaged.

export function debtPlan(state, playerId, needed) {
  const sim = { ...state, properties: { ...state.properties } };
  const setProp = (index, patch) => { sim.properties[index] = { ...propState(sim, index), ...patch }; };
  let bankHouses = state?.bank?.houses ?? 32;
  const raw = [];
  let raised = 0;

  const candidates = () => {
    const list = [];
    const groups = [...new Set(ownedBy(sim, playerId).map((index) => spaceAt(sim, index)).filter((space) => space.type === "property").map((space) => space.group))];
    for (const group of groups) {
      const indexes = groupIndexes(sim, group);
      const levels = indexes.map((index) => propState(sim, index).houses || 0);
      const max = Math.max(...levels);
      if (max === 0) continue;
      if (max === 5 && bankHouses < 4) continue;
      let best = null;
      for (const index of indexes) {
        const prop = propState(sim, index);
        if (prop.ownerId !== playerId || (prop.houses || 0) !== max) continue;
        const space = spaceAt(sim, index);
        const before = rentFor(sim, index);
        const after = max - 1 === 0 && ownsGroup(sim, playerId, group) ? space.rents[0] * 2 : space.rents[max - 1];
        const option = { kind: "sell", index, group, cash: Math.floor(space.buildCost / 2), loss: before - after };
        if (!best || option.loss < best.loss) best = option;
      }
      if (best) list.push(best);
    }
    for (const index of ownedBy(sim, playerId)) {
      if (!canMortgage(sim, playerId, index)) continue;
      const space = spaceAt(sim, index);
      list.push({ kind: "mortgage", index, group: space.group || space.type, cash: mortgageValue(space), loss: rentFor(sim, index) || 0 });
    }
    return list;
  };

  while (raised < needed) {
    const list = candidates();
    if (!list.length) break;
    const remaining = needed - raised;
    const ratio = (option) => option.loss / Math.max(1, option.cash);
    const byRatio = [...list].sort((a, b) => ratio(a) - ratio(b) || a.cash - b.cash);
    const covering = list.filter((option) => option.cash >= remaining).sort((a, b) => a.loss - b.loss || a.cash - b.cash);
    const pick = covering.length && covering[0].loss <= byRatio[0].loss ? covering[0] : byRatio[0];
    if (pick.kind === "sell") {
      const houses = propState(sim, pick.index).houses;
      if (houses === 5) bankHouses -= 4;
      else bankHouses += 1;
      setProp(pick.index, { houses: houses - 1 });
    } else {
      setProp(pick.index, { mortgaged: true });
    }
    raised += pick.cash;
    raw.push(pick);
  }

  // Present steps grouped by (kind, group), in order of first appearance (keeps sells before the
  // mortgages they unlock).
  const order = [];
  const buckets = new Map();
  for (const option of raw) {
    const key = `${option.kind}:${option.group}`;
    if (!buckets.has(key)) { buckets.set(key, []); order.push(key); }
    buckets.get(key).push(option);
  }
  const steps = order.map((key, number) => describeStep(state, sim, buckets.get(key), number + 1));
  return {
    steps,
    total: raised,
    needed,
    enough: raised >= needed,
    exact: raised === needed,
    short: Math.max(0, needed - raised),
    actions: raw.map((option) => ({ type: option.kind === "sell" ? "sell_building" : "mortgage", spaceIndex: option.index })),
  };
}

function describeRents(values) {
  const unique = [...new Set(values)];
  if (unique.length === 1) return money(unique[0]);
  if (values.length === 2) return `${money(values[0])} and ${money(values[1])}`;
  const sorted = [...values].sort((a, b) => a - b);
  return `${money(sorted[0])}–${sorted[sorted.length - 1].toLocaleString("en-US")}`;
}

function describeStep(state, sim, options, number) {
  const first = options[0];
  const amount = options.reduce((sum, option) => sum + option.cash, 0);
  const actions = options.map((option) => ({ type: option.kind === "sell" ? "sell_building" : "mortgage", spaceIndex: option.index }));
  if (first.kind === "sell") {
    const group = first.group;
    const colour = colourWord(group);
    const indexes = groupIndexes(state, group);
    const counts = new Map();
    for (const option of options) counts.set(option.index, (counts.get(option.index) || 0) + 1);
    const total = options.length;
    const before = indexes.reduce((sum, index) => sum + (propState(state, index).houses || 0), 0);
    let label;
    if (total === before) label = `Sell all ${total} ${colour} ${total === 1 ? "house" : "houses"}`;
    else if (counts.size === indexes.length && new Set(counts.values()).size === 1) {
      const each = [...counts.values()][0];
      label = `Sell ${each} ${each === 1 ? "house" : "houses"} from each ${colour} street`;
    } else if (counts.size === 1) {
      label = `Sell ${total === 1 ? "a house" : `${total} houses`} on ${spaceAt(state, first.index).name}`;
    } else label = `Sell ${total} ${colour} houses`;
    // Rent after this step only (later mortgages in the same group are separate steps).
    const after = { ...state, properties: { ...state.properties } };
    for (const index of indexes) {
      after.properties[index] = { ...propState(state, index), houses: (propState(state, index).houses || 0) - (counts.get(index) || 0) };
    }
    const rents = indexes.map((index) => rentFor(after, index));
    return { n: number, kind: "sell", group, label, cost: `${capitalise(colour)} rent drops to ${describeRents(rents)}`, amount, actions };
  }
  const spaces = options.map((option) => spaceAt(state, option.index));
  const payback = spaces.reduce((sum, space) => sum + unmortgageCost(space), 0);
  if (spaces[0].type === "property") {
    const colour = colourWord(spaces[0].group);
    const size = groupIndexes(state, spaces[0].group).length;
    let label;
    if (spaces.length === 1) label = `Mortgage ${spaces[0].name}`;
    else if (spaces.length === size && size === 2) label = `Mortgage both ${colour} streets`;
    else if (spaces.length === size) label = `Mortgage all ${size} ${colour} streets`;
    else label = `Mortgage ${listNames(spaces.map((space) => space.name))}`;
    const cost = spaces.length === 1
      ? `No rent from ${spaces[0].name} until you pay back ${money(payback)}`
      : `No ${colour} rent until you pay back ${money(payback)}`;
    return { n: number, kind: "mortgage", group: spaces[0].group, label, cost, amount, actions };
  }
  const label = `Mortgage ${listNames(spaces.map((space) => space.name))}`;
  return { n: number, kind: "mortgage", group: first.group, label, cost: `No rent from ${spaces.length === 1 ? "it" : "them"} until you pay back ${money(payback)}`, amount, actions };
}

// Everything a player could raise by selling all buildings and mortgaging everything.
export function liquidationValue(state, playerId) {
  return debtPlan(state, playerId, Number.MAX_SAFE_INTEGER).total;
}

// ---------------------------------------------------------------------------------------------
// Trades

export function tradeable(state, playerId) {
  return ownedBy(state, playerId).filter((index) => {
    const space = spaceAt(state, index);
    return space.type !== "property" || !groupHasBuildings(state, space.group);
  });
}

// Effects of a trade on colour sets and railroads, from `viewerId`'s point of view.
// trade = { proposerId, targetId, offer: {cash, properties}, request: {cash, properties} }
export function tradeEffects(state, trade, viewerId) {
  const after = { ...state, properties: { ...state.properties } };
  for (const index of trade.offer?.properties || []) after.properties[index] = { ...propState(state, index), ownerId: trade.targetId };
  for (const index of trade.request?.properties || []) after.properties[index] = { ...propState(state, index), ownerId: trade.proposerId };
  const effects = [];
  const groups = [...new Set((state.board || []).filter((space) => space.type === "property").map((space) => space.group))];
  for (const playerId of [trade.proposerId, trade.targetId]) {
    const player = playerById(state, playerId);
    if (!player) continue;
    const you = playerId === viewerId;
    for (const group of groups) {
      const before = ownsGroup(state, playerId, group);
      const later = ownsGroup(after, playerId, group);
      const colour = colourWord(group);
      if (!before && later) {
        effects.push(you
          ? { tone: "good", strong: `Completes your ${colour} set,`, text: "so you can build there." }
          : { tone: "warn", strong: `Gives ${player.name} the ${colour} set,`, text: `so ${player.name} can build there.` });
      } else if (before && !later) {
        effects.push(you
          ? { tone: "warn", strong: `Breaks your ${colour} set,`, text: "so you can't build there any more." }
          : { tone: "good", strong: `Breaks ${possessive(player.name)} ${colour} set,`, text: `so ${player.name} can't build there any more.` });
      }
    }
    const railsBefore = countOfType(state, playerId, "railroad");
    const railsAfter = countOfType(after, playerId, "railroad");
    if (railsAfter > railsBefore && railsAfter >= 2) {
      const who = you ? "You go" : `${player.name} goes`;
      effects.push({ tone: you ? "good" : "info", strong: `${who} to ${railsAfter} railroads,`, text: `${money(25 * 2 ** (railsAfter - 1))} rent on each.` });
    }
  }
  const viewerReceives = viewerId === trade.proposerId ? trade.request?.properties : viewerId === trade.targetId ? trade.offer?.properties : [];
  const fee = (viewerReceives || []).reduce((sum, index) => sum + (propState(state, index)?.mortgaged ? Math.ceil((spaceAt(state, index).price / 2) * 0.1) : 0), 0);
  if (fee > 0) effects.push({ tone: "info", strong: `${money(fee)} transfer fee`, text: "for taking on mortgaged property." });
  if (!effects.length) effects.push({ tone: "info", strong: "No set changes hands.", text: "" });
  return effects;
}

// ---------------------------------------------------------------------------------------------
// Standings

export function netWorth(state, playerId) {
  const player = playerById(state, playerId);
  if (!player || player.bankrupt) return 0;
  let total = player.cash;
  for (const index of ownedBy(state, playerId)) {
    const space = spaceAt(state, index);
    const prop = propState(state, index);
    total += prop.mortgaged ? space.price - mortgageValue(space) : space.price;
    if (space.type === "property") total += (prop.houses || 0) * space.buildCost;
  }
  return total;
}

// What the server ranks players by when a game ends (engine FinalScore): net worth, plus active
// loans owed to them, minus loans they owe, with a pending debt settled as if it were paid (the
// debtor loses it, a player creditor gains it). The end-game preview and the final standings use
// it, so they always name the same winner as the server.
export function finalScore(state, playerId) {
  const player = playerById(state, playerId);
  if (!player || player.bankrupt) return 0;
  let score = netWorth(state, playerId);
  for (const loan of state?.loans || []) {
    if (loan?.status !== "active") continue;
    if (loan.lenderId === playerId) score += loan.repay || 0;
    if (loan.borrowerId === playerId) score -= loan.repay || 0;
  }
  const debt = state?.debt;
  const payer = debt ? playerById(state, debt.payerId) : null;
  if (debt && debt.amount > 0 && payer && !payer.bankrupt) {
    if (debt.payerId === playerId) score -= debt.amount;
    if (debt.creditorId === playerId) score += debt.amount;
  }
  return score;
}

// Order of bankruptcies from the log (names), earliest first.
export function bankruptcyOrder(state) {
  const names = [];
  for (const entry of state?.log || []) {
    const match = entry.text.match(/^(.+) declared bankruptcy/);
    if (match && !names.includes(match[1])) names.push(match[1]);
  }
  return names;
}

// Places: active players by net worth, then bankrupt players, the last one out ranked highest.
// The server numbers bankruptcies (players[].outOrder, 1 = first out); older servers don't, so
// the engine log (which keeps only the latest entries) is the fallback.
export function hasOutOrder(state) {
  const out = (state?.players || []).filter((player) => player.bankrupt);
  return out.length > 0 && out.every((player) => Number.isInteger(player.outOrder));
}

export function standings(state) {
  const out = bankruptcyOrder(state);
  const exact = hasOutOrder(state);
  const outRank = (player) => {
    if (exact) return player.outOrder;
    const index = out.indexOf(player.name);
    return index < 0 ? -1 : index;
  };
  return (state?.players || [])
    .map((player) => ({ player, worth: finalScore(state, player.id), outOrder: player.bankrupt ? outRank(player) : null }))
    .sort((a, b) => Number(a.player.bankrupt) - Number(b.player.bankrupt) || (a.player.bankrupt ? b.outOrder - a.outOrder : b.worth - a.worth))
    .map((entry, index) => ({ ...entry, place: index + 1, placeLabel: ordinal(index + 1) }));
}

// Seats until `playerId` plays again: 1 = next.
export function turnsUntil(state, playerId) {
  const players = state?.players || [];
  const start = state?.currentIndex ?? 0;
  let count = 0;
  for (let offset = 1; offset <= players.length; offset += 1) {
    const candidate = players[(start + offset) % players.length];
    if (candidate.bankrupt) continue;
    count += 1;
    if (candidate.id === playerId) return count;
  }
  return null;
}

export function nextPlayerId(state) {
  const players = state?.players || [];
  const start = state?.currentIndex ?? 0;
  for (let offset = 1; offset <= players.length; offset += 1) {
    const candidate = players[(start + offset) % players.length];
    if (!candidate.bankrupt) return candidate.id;
  }
  return null;
}

// ---------------------------------------------------------------------------------------------
// Headlines cards (deck "chance"; ids mirror src/game.js CHANCE_CARDS; only they need confirming)

export const CHANCE_EFFECTS = {
  "chance-go": { type: "moveTo", target: 0 },
  "chance-crown": { type: "moveTo", target: 39 },
  "chance-rail-1": { type: "nearestRail" },
  "chance-rail-2": { type: "nearestRail" },
  "chance-utility": { type: "nearestUtility" },
  "chance-dividend": { type: "money", amount: 50 },
  "chance-jail-card": { type: "jailCard" },
  "chance-back": { type: "move", steps: -3 },
  "chance-jail": { type: "jail" },
  "chance-repairs": { type: "repairs", house: 25, hotel: 100 },
  "chance-fine": { type: "money", amount: -15 },
  "chance-star": { type: "moveTo", target: 24 },
  "chance-loan": { type: "money", amount: 150 },
  "chance-garden": { type: "moveTo", target: 11 },
  "chance-tax": { type: "money", amount: -40 },
  "chance-award": { type: "money", amount: 100 },
};

function arrivalNote(state, playerId, target, options = {}) {
  const space = spaceAt(state, target);
  const prop = propState(state, target);
  if (!prop) return null;
  if (!prop.ownerId) return { k: "For sale", v: `${space.name} is unowned: ${money(space.price)}.` };
  if (prop.ownerId === playerId) return { k: "Rent", v: "It's yours, so no rent." };
  const owner = playerById(state, prop.ownerId);
  const rent = rentFor(state, target, options);
  if (!rent) return { k: "Rent", v: `${owner?.name} owns it, but it's mortgaged: no rent.` };
  const extra = options.railroadMultiplier === 2 ? " (double rent)" : options.utilityMultiplier ? " (10× the dice)" : "";
  return { k: "Rent", v: `${owner?.name} owns it: you pay ${money(rent)}${extra}.` };
}

function passGoNote(target) {
  const p = landingOdds(target).reduce((sum, outcome) => sum + (target + outcome.total >= BOARD_SIZE ? outcome.p : 0), 0);
  if (p >= 0.999) return { k: "Next roll", v: "You pass PAYDAY and collect $200." };
  if (p > 0) return { k: "Next roll", v: `${pct(p)} chance you pass PAYDAY and collect $200.` };
  return null;
}

// { action: {label, detail}, notes: [{k, v}] } for a pending Headlines card.
export function cardPreview(state, playerId, cardId) {
  const effect = CHANCE_EFFECTS[cardId];
  const player = playerById(state, playerId);
  if (!effect || !player) return { action: { label: "OK", detail: "" }, notes: [] };
  const pos = player.position;
  const cash = player.cash;
  const dice = state.turn?.dice ? state.turn.dice[0] + state.turn.dice[1] : 7;
  const moveNotes = (target, collect, arrival) => {
    const steps = (target - pos + BOARD_SIZE) % BOARD_SIZE;
    const name = spaceAt(state, target).name;
    let tail = " You don't pass PAYDAY.";
    if (target === 0) tail = " Collect $200.";
    else if (collect && target < pos) tail = " You pass PAYDAY and collect $200.";
    return [{ k: "Move", v: `${steps} squares to ${name}.${tail}` }, arrival, passGoNote(target)].filter(Boolean);
  };
  switch (effect.type) {
    case "moveTo": {
      const steps = (effect.target - pos + BOARD_SIZE) % BOARD_SIZE;
      const name = spaceAt(state, effect.target).name;
      return {
        target: effect.target,
        action: effect.target === 0 ? { label: "ADVANCE TO PAYDAY", detail: "Collect $200" } : { label: `MOVE TO ${name.toUpperCase()}`, detail: `${steps} squares` },
        notes: moveNotes(effect.target, true, arrivalNote(state, playerId, effect.target)),
      };
    }
    case "nearestRail":
    case "nearestUtility": {
      const list = effect.type === "nearestRail" ? RAILROADS : UTILITIES;
      const target = list.find((index) => index > pos) ?? list[0];
      const options = effect.type === "nearestRail" ? { railroadMultiplier: 2 } : { utilityMultiplier: 10, diceTotal: dice };
      const steps = (target - pos + BOARD_SIZE) % BOARD_SIZE;
      return {
        target,
        action: { label: `MOVE TO ${spaceAt(state, target).name.toUpperCase()}`, detail: `${steps} squares` },
        notes: moveNotes(target, true, arrivalNote(state, playerId, target, options)),
      };
    }
    case "move": {
      const target = (pos + effect.steps + BOARD_SIZE) % BOARD_SIZE;
      const name = spaceAt(state, target).name;
      return {
        target,
        action: { label: "MOVE BACK 3", detail: `to ${name}` },
        notes: [{ k: "Move", v: `Back 3 squares to ${name}. No PAYDAY money.` }, arrivalNote(state, playerId, target)].filter(Boolean),
      };
    }
    case "money": {
      if (effect.amount >= 0) {
        return { action: { label: `COLLECT ${money(effect.amount)}`, detail: `Cash ${money(cash)} → ${money(cash + effect.amount)}` }, notes: [] };
      }
      const owe = -effect.amount;
      return {
        action: { label: `PAY ${money(owe)}`, detail: cash >= owe ? `Cash ${money(cash)} → ${money(cash - owe)}` : `You'll need to raise ${money(owe - cash)}` },
        notes: [],
      };
    }
    case "jailCard":
      return { action: { label: "KEEP THE PASS", detail: "Leave jail free later" }, notes: [{ k: "Keep", v: "Use it the next time you're in jail." }] };
    case "jail":
      return {
        target: JAIL,
        action: { label: "OFF TO JAIL", detail: "Don't pass PAYDAY" },
        notes: [{ k: "Move", v: "Straight to jail. You don't pass PAYDAY or collect $200." }, { k: "Rent", v: "You still collect rent while you're in jail." }],
      };
    case "repairs": {
      let houses = 0;
      let hotels = 0;
      for (const index of ownedBy(state, playerId)) {
        const prop = propState(state, index);
        if (spaceAt(state, index).type !== "property") continue;
        if (prop.houses === 5) hotels += 1;
        else houses += prop.houses || 0;
      }
      const amount = houses * effect.house + hotels * effect.hotel;
      if (!amount) return { action: { label: "NOTHING TO PAY", detail: "You have no buildings" }, notes: [] };
      return {
        action: { label: `PAY ${money(amount)}`, detail: `${houses} houses · ${hotels} hotels` },
        notes: [{ k: "Repairs", v: `${money(effect.house)} a house and ${money(effect.hotel)} a hotel.` }],
      };
    }
    default:
      return { action: { label: "OK", detail: "" }, notes: [] };
  }
}

export const BOARD_CONSTANTS = { BOARD_SIZE, RAILROADS, UTILITIES, GO_TO_JAIL, JAIL };

// ---------------------------------------------------------------------------------------------
// House rules and loans (Go engine additions; all optional in the state)

export const DEFAULT_RULES = { loans: false, freeParkingJackpot: false, auctions: true, startingCash: 1500 };

export function rulesOf(state) {
  return { ...DEFAULT_RULES, ...(state?.settings?.rules || {}) };
}

export function activeLoans(state, playerId = null) {
  return (state?.loans || []).filter((loan) => loan.status === "active" && (!playerId || loan.lenderId === playerId || loan.borrowerId === playerId));
}

// The active loan a property secures, if any.
export function loanSecuredBy(state, index) {
  return activeLoans(state).find((loan) => (loan.collateral || []).includes(Number(index))) || null;
}

function you(state, id, viewerId) {
  return id === viewerId ? "you" : playerById(state, id)?.name || "someone";
}

function cap(text) {
  return text ? text[0].toUpperCase() + text.slice(1) : text;
}

// "in 3 turns" when the state exposes turn counts, else "by turn 29" (the borrower's own turns).
export function loanDue(state, loan) {
  const counts = state?.turnCounts;
  if (counts && typeof counts[loan.borrowerId] === "number" && typeof loan.dueTurn === "number") {
    const left = loan.dueTurn - counts[loan.borrowerId];
    if (left <= 0) return "due now";
    return `due in ${left} turn${left === 1 ? "" : "s"}`;
  }
  return typeof loan.dueTurn === "number" ? `due by turn ${loan.dueTurn}` : "";
}

// Lender/borrower of an offer: direction "lend" = the proposer lends to the target.
export function offerParties(offer) {
  return offer.direction === "borrow"
    ? { lenderId: offer.targetId, borrowerId: offer.proposerId }
    : { lenderId: offer.proposerId, borrowerId: offer.targetId };
}

// Plain-English terms of a loan offer or active loan, from `viewerId`'s point of view.
export function loanTerms(state, loan, viewerId) {
  const { lenderId, borrowerId } = loan.lenderId ? loan : offerParties(loan);
  const lender = you(state, lenderId, viewerId);
  const borrower = you(state, borrowerId, viewerId);
  const borrowerYou = borrowerId === viewerId;
  const lenderPossessive = lenderId === viewerId ? "yours" : `${possessive(lender)}`;
  const due = loan.dueTurns ? `within ${loan.dueTurns} turn${loan.dueTurns === 1 ? "" : "s"}` : loanDue(state, loan).replace(/^due /, "");
  const names = (loan.collateral || []).map((index) => spaceAt(state, index)?.name).filter(Boolean);
  const head = `${cap(borrower)} ${borrowerYou ? "get" : "gets"} ${money(loan.principal)} now and ${borrowerYou ? "owe" : "owes"} ${lender} ${money(loan.repay)} ${due}.`;
  const security = names.length
    ? ` If ${borrower} can't pay, ${listNames(names)} ${names.length === 1 ? "becomes" : "become"} ${lenderPossessive}, then the usual bankruptcy rules apply.`
    : ` There's no security: if ${borrower} can't pay, the usual bankruptcy rules apply.`;
  return `${head}${security}`;
}

export function loanInterest(loan) {
  return Math.max(0, (loan.repay || 0) - (loan.principal || 0));
}

// Sensible starting terms for a loan request: the shortfall, +20% rounded up to $10, 6 turns.
export function suggestLoan(amount) {
  const principal = Math.max(10, Math.ceil(amount / 10) * 10);
  return { principal, repay: Math.ceil((principal * 1.2) / 10) * 10, dueTurns: 6 };
}

// Properties a borrower can pledge: owned, unmortgaged, no buildings, not already pledged.
export function pledgeable(state, playerId) {
  return tradeable(state, playerId).filter((index) => !propState(state, index)?.mortgaged && !loanSecuredBy(state, index));
}

// ---------------------------------------------------------------------------------------------
// What a trade does to each side (PC trade modal, phone). Pure.

// Deeds a player owns but can't trade right now: { index, reason: "HOUSES" | "PLEDGED" }.
export function untradeable(state, playerId) {
  const open = new Set(tradeable(state, playerId));
  return ownedBy(state, playerId)
    .filter((index) => !open.has(index) || loanSecuredBy(state, index))
    .map((index) => ({ index, reason: loanSecuredBy(state, index) ? "PLEDGED" : "HOUSES" }));
}

// trade = { proposerId, targetId, offer: { cash, properties }, request: { cash, properties } }
// → { proposer: side, target: side, after } where side = {
//     playerId, name, gives: [index], gets: [index], cashBefore, cashAfter, worthBefore, worthAfter,
//     fee (transfer fee for mortgaged deeds received), setsCompleted / setsBroken: [{ group, name }],
//     rentChanges: [{ key, label, before, after, text }], railroads / utilities: { before, after },
//     blocked: [{ index, reason }] (deeds in the deal it can't trade), verdicts: [string] }
// options.viewerId writes "your" for that player in the verdicts.
export function tradeImpact(state, trade, { viewerId = null } = {}) {
  const offer = trade?.offer || {};
  const request = trade?.request || {};
  const give = { [trade.proposerId]: offer.properties || [], [trade.targetId]: request.properties || [] };
  const cash = { [trade.proposerId]: offer.cash || 0, [trade.targetId]: request.cash || 0 };
  const other = { [trade.proposerId]: trade.targetId, [trade.targetId]: trade.proposerId };
  const fees = {};
  const after = { ...state, properties: { ...(state?.properties || {}) }, players: (state?.players || []).map((player) => ({ ...player })) };
  for (const id of [trade.proposerId, trade.targetId]) {
    for (const index of give[id]) after.properties[index] = { ...propState(state, index), ownerId: other[id] };
    fees[other[id]] = (fees[other[id]] || 0) + give[id].reduce((sum, index) => sum + (propState(state, index)?.mortgaged ? Math.ceil(mortgageValue(spaceAt(state, index)) * 0.1) : 0), 0);
  }
  for (const player of after.players) {
    if (player.id === trade.proposerId || player.id === trade.targetId) {
      player.cash = player.cash - cash[player.id] + cash[other[player.id]] - (fees[player.id] || 0);
    }
  }
  const groups = [...new Set([...give[trade.proposerId], ...give[trade.targetId]].map((index) => spaceAt(state, index)).filter((space) => space?.type === "property").map((space) => space.group))];
  const touches = (type) => [...give[trade.proposerId], ...give[trade.targetId]].some((index) => spaceAt(state, index)?.type === type);
  const groupRent = (snapshot, playerId, group) => {
    const rents = groupIndexes(snapshot, group).filter((index) => propState(snapshot, index)?.ownerId === playerId).map((index) => rentFor(snapshot, index) || 0);
    return rents.length ? Math.max(...rents) : 0;
  };
  const side = (playerId) => {
    const player = playerById(state, playerId);
    const name = player?.name || "";
    const you = playerId === viewerId;
    const whose = you ? "your" : possessive(name);
    const whoGoes = you ? "You go" : `${name} goes`;
    const setsCompleted = [];
    const setsBroken = [];
    const rentChanges = [];
    for (const group of groups) {
      const before = ownsGroup(state, playerId, group);
      const later = ownsGroup(after, playerId, group);
      const label = colourWord(group).toUpperCase();
      if (!before && later) setsCompleted.push({ group, name: label });
      if (before && !later) setsBroken.push({ group, name: label });
      const rentBefore = groupRent(state, playerId, group);
      const rentAfter = groupRent(after, playerId, group);
      if (rentBefore !== rentAfter) rentChanges.push({ key: group, label, before: rentBefore, after: rentAfter, text: `${colourWord(group).replace(/^./, (c) => c.toUpperCase())} rent ${rentBefore ? money(rentBefore) : "—"} → ${rentAfter ? money(rentAfter) : "—"}` });
    }
    const railroads = { before: countOfType(state, playerId, "railroad"), after: countOfType(after, playerId, "railroad") };
    const utilities = { before: countOfType(state, playerId, "utility"), after: countOfType(after, playerId, "utility") };
    const railRent = (count) => (count ? 25 * 2 ** (count - 1) : 0);
    if (touches("railroad") && railroads.before !== railroads.after) {
      rentChanges.push({ key: "railroad", label: "RAILROADS", before: railRent(railroads.before), after: railRent(railroads.after), text: `Railroads ${railroads.before} → ${railroads.after} (rent ${railRent(railroads.before) ? money(railRent(railroads.before)) : "—"} → ${railRent(railroads.after) ? money(railRent(railroads.after)) : "—"})` });
    }
    const utilityTimes = (count) => (count === 2 ? 10 : count === 1 ? 4 : 0);
    if (touches("utility") && utilities.before !== utilities.after) {
      rentChanges.push({ key: "utility", label: "UTILITIES", before: utilityTimes(utilities.before), after: utilityTimes(utilities.after), text: `Utilities ${utilities.before} → ${utilities.after} (${utilityTimes(utilities.before) ? `${utilityTimes(utilities.before)}×` : "—"} → ${utilityTimes(utilities.after) ? `${utilityTimes(utilities.after)}×` : "—"} dice)` });
    }
    const blockedAll = untradeable(state, playerId);
    const blocked = blockedAll.filter((entry) => give[playerId].includes(entry.index));
    const verdicts = [
      ...setsCompleted.map((set) => `Completes ${whose} ${set.name} set`),
      ...setsBroken.map((set) => `Breaks ${whose} ${set.name} set`),
    ];
    if (railroads.after > railroads.before && railroads.after >= 2) verdicts.push(`${whoGoes} to ${railroads.after} railroads (${money(railRent(railroads.after))} rent each)`);
    if (utilities.after === 2 && utilities.before < 2) verdicts.push(`${you ? "You get" : `${name} gets`} both utilities (10× dice)`);
    for (const entry of blocked) verdicts.push(`${spaceAt(state, entry.index)?.name || "A deed"} can't be traded (${entry.reason === "PLEDGED" ? "pledged to a loan" : "houses on the set"})`);
    return {
      playerId,
      name,
      gives: [...give[playerId]],
      gets: [...give[other[playerId]]],
      cashBefore: player?.cash ?? 0,
      cashAfter: after.players.find((candidate) => candidate.id === playerId)?.cash ?? 0,
      worthBefore: netWorth(state, playerId),
      worthAfter: netWorth(after, playerId),
      fee: fees[playerId] || 0,
      setsCompleted,
      setsBroken,
      rentChanges,
      railroads,
      utilities,
      blocked,
      verdicts,
    };
  };
  return { proposer: side(trade.proposerId), target: side(trade.targetId), after };
}

// ---------------------------------------------------------------------------------------------
// Opening roll for turn order (review B3: screens showed each player's latest roll, mixing rounds,
// and told everyone "TIE-BREAK · ROLL AGAIN" whether or not they were tied).
//
// openingRows(state) → one row per player, in seat order:
//   { playerId, first: round-1 total | null, extra: [tie-break totals], shown: "7" | "2 · 5",
//     pending: still to roll this round, tied: in the current tie-break, tieTotal: the total they
//     tied on (or null) }
// openingTie(state) → the current tie-break { round, ids, totals: [tied totals], names } or null.
// Uses openingOrder.rollingIds and .ties from the server when present; derives them otherwise.

function tieOf(order) {
  const round = order?.round || 1;
  if (round <= 1) return null;
  const pending = order.pendingIds || [];
  const ids = (order.rollingIds && order.rollingIds.length ? order.rollingIds : (order.entries || [])
    .filter((entry) => pending.includes(entry.playerId) || (entry.rolls || []).some((roll) => (roll.round ?? 1) === round))
    .map((entry) => entry.playerId));
  if (!ids.length) return null;
  const ties = (order.ties || []).filter((tie) => tie.round === round - 1);
  return { round, ids, ties };
}

export function openingTie(state) {
  if (state?.phase !== "determining_order") return null;
  const tie = tieOf(state.openingOrder);
  if (!tie) return null;
  const rows = openingRows(state);
  const totals = [...new Set(rows.filter((row) => row.tied).map((row) => row.tieTotal).filter((total) => total !== null))];
  return { round: tie.round, ids: tie.ids, totals, names: tie.ids.map((id) => playerById(state, id)?.name || "") };
}

export function openingRows(state) {
  const order = state?.openingOrder;
  if (!order) return [];
  const live = state.phase === "determining_order";
  const tie = live ? tieOf(order) : null;
  const tied = new Set(tie?.ids || []);
  return (state.players || []).map((player) => {
    const rolls = order.entries?.find((entry) => entry.playerId === player.id)?.rolls || [];
    const roundOf = (roll, index) => roll.round ?? index + 1;
    const first = rolls.find((roll, index) => roundOf(roll, index) === 1)?.total ?? null;
    const extra = rolls.filter((roll, index) => roundOf(roll, index) > 1).map((roll) => roll.total);
    const pending = live && (order.pendingIds || []).includes(player.id);
    // The total they tied on: the server's ties when it sends them, else the roll before this round.
    let tieTotal = null;
    if (tied.has(player.id)) {
      const listed = tie.ties.find((entry) => (entry.playerIds || []).includes(player.id));
      const before = rolls.filter((roll, index) => roundOf(roll, index) < tie.round).at(-1);
      tieTotal = listed?.total ?? before?.total ?? null;
    }
    const parts = [first, ...extra].filter((total) => total !== null && total !== undefined);
    // A tie-break still to come reads "7 · RE-ROLL" (review 4 Nov: "7 · …" looked cut off).
    if (pending && (first !== null || tied.has(player.id))) parts.push(parts.length ? "RE-ROLL" : "…");
    return { playerId: player.id, first, extra, shown: parts.length ? parts.join(" · ") : pending ? "…" : "–", pending, tied: tied.has(player.id), tieTotal };
  });
}

// ---------------------------------------------------------------------------------------------
// How a deal weighs for one side (review U8: the value bar compared list prices only, so a deal
// that completes your set showed mostly red).
//
// dealBalance(state, trade, viewerId) → {
//   give, get: list value each way (deeds at price, half if mortgaged, plus cash),
//   tone: "good" | "bad" | "even" for the viewer — sets made or broken outweigh list prices,
//   sets: [{ group, gain, you, name, rent }] sets that change hands (rent: the hotel rent there),
// }
export function dealBalance(state, trade, viewerId) {
  const youGive = viewerId === trade?.proposerId ? trade.offer : trade?.request;
  const youGet = viewerId === trade?.proposerId ? trade.request : trade?.offer;
  const value = (side) => (side?.properties || []).reduce((sum, index) => {
    const space = spaceAt(state, index);
    return sum + (propState(state, index)?.mortgaged ? Math.floor((space?.price || 0) / 2) : space?.price || 0);
  }, 0) + (side?.cash || 0);
  const give = value(youGive);
  const get = value(youGet);
  const impact = tradeImpact(state, trade, { viewerId });
  const sets = [];
  let score = 0;
  for (const side of [impact.proposer, impact.target]) {
    const you = side.playerId === viewerId;
    for (const [list, gain] of [[side.setsCompleted, true], [side.setsBroken, false]]) {
      for (const set of list || []) {
        const rent = Math.max(0, ...groupIndexes(state, set.group).map((index) => spaceAt(state, index)?.rents?.[5] || 0));
        sets.push({ group: set.group, gain, you, name: side.name, rent });
        score += (you ? 1 : -1) * (gain ? 1 : -1);
      }
    }
  }
  let tone = "even";
  if (score > 0) tone = "good";
  else if (score < 0) tone = "bad";
  else if (get > give * 1.1) tone = "good";
  else if (give > get * 1.1) tone = "bad";
  return { give, get, tone, sets };
}
