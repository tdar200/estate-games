// The debt screen's checklist (UX-04): OWE / CASH / STILL TO FIND, the deeds to mortgage and the
// houses to sell, each ticked or not, and PAY once the ticked rows cover the debt. Ticks start as
// the suggested plan (insights.debtPlan); paying runs the ticked sells, then the ticked mortgages,
// then pay_debt: existing actions only. Pure, shared by the TV and the phones.

import { colourWord, playerById } from "./format.js";
import { canMortgage, debtPlan, groupIndexes, mortgageValue, ownedBy, propState, rentFor, spaceAt } from "./insights.js";

const sellAction = (index) => ({ type: "sell_building", spaceIndex: index });

// Every building on a colour set this player owns, sold evenly (always from the tallest street).
function sellAllActions(state, playerId, group) {
  const levels = new Map(groupIndexes(state, group).filter((index) => propState(state, index)?.ownerId === playerId).map((index) => [index, propState(state, index).houses || 0]));
  const actions = [];
  for (;;) {
    let best = null;
    for (const [index, houses] of levels) if (houses > 0 && (best === null || houses > levels.get(best))) best = index;
    if (best === null) break;
    levels.set(best, levels.get(best) - 1);
    actions.push(sellAction(best));
  }
  return actions;
}

// The checklist for the player in debt. picked: the ticked keys ("sell:<group>",
// "mortgage:<index>"), or null for the suggested plan.
export function debtChecklist(state, playerId, picked = null) {
  const player = playerById(state, playerId);
  const owe = state?.debt?.payerId === playerId ? Number(state.debt.amount) || 0 : 0;
  const cash = player?.cash ?? 0;
  const short = Math.max(0, owe - cash);
  const plan = short > 0 ? debtPlan(state, playerId, short) : { actions: [] };
  const planSells = new Map();
  const planMortgages = new Set();
  for (const action of plan.actions) {
    if (action.type === "mortgage") planMortgages.add(action.spaceIndex);
    else {
      const group = spaceAt(state, action.spaceIndex)?.group;
      if (!planSells.has(group)) planSells.set(group, []);
      planSells.get(group).push(action);
    }
  }
  const defaults = new Set([...[...planSells.keys()].map((group) => `sell:${group}`), ...[...planMortgages].map((index) => `mortgage:${index}`)]);
  const ticked = picked ? new Set(picked) : defaults;

  // Houses: one row per colour set with buildings. A set the plan sells from sells what the plan
  // says (often not all); any other set sells everything.
  const owned = ownedBy(state, playerId);
  const groups = [...new Set(owned.map((index) => spaceAt(state, index)).filter((space) => space.type === "property" && (propState(state, space.index)?.houses || 0) > 0).map((space) => space.group))];
  const sell = groups.map((group) => {
    const actions = planSells.get(group) || sellAllActions(state, playerId, group);
    const houses = groupIndexes(state, group).reduce((sum, index) => sum + (propState(state, index)?.ownerId === playerId ? propState(state, index).houses || 0 : 0), 0);
    const amount = actions.reduce((sum, action) => sum + Math.floor(spaceAt(state, action.spaceIndex).buildCost / 2), 0);
    const names = [...new Set(actions.map((action) => spaceAt(state, action.spaceIndex).name))];
    const key = `sell:${group}`;
    return { key, kind: "sell", group, colour: colourWord(group), name: names.length === 1 ? names[0] : `${colourWord(group)} houses`, houses, sells: actions.length, after: houses - actions.length, amount, actions, ticked: ticked.has(key), blocked: false };
  });
  const soldOut = new Set(sell.filter((row) => row.ticked && row.after === 0).map((row) => row.group));

  // Deeds: every one that can be mortgaged now, plus those the ticked sells clear of houses.
  const mortgage = owned.filter((index) => !propState(state, index)?.mortgaged).map((index) => {
    const space = spaceAt(state, index);
    const free = canMortgage(state, playerId, index);
    const freedBySale = !free && space.type === "property" && soldOut.has(space.group);
    const key = `mortgage:${index}`;
    if (!free && !freedBySale && !planMortgages.has(index) && !sell.some((row) => row.group === space.group)) return null;
    const blocked = !free && !freedBySale;
    return { key, kind: "mortgage", index, group: space.group || space.type, name: space.name, rent: rentFor(state, index) || 0, amount: mortgageValue(space), actions: [{ type: "mortgage", spaceIndex: index }], ticked: ticked.has(key) && !blocked, blocked };
  }).filter(Boolean);

  const counted = [...sell, ...mortgage].filter((row) => row.ticked && !row.blocked);
  const raised = counted.reduce((sum, row) => sum + row.amount, 0);
  const toFind = Math.max(0, owe - cash - raised);
  const actions = [...sell.filter((row) => row.ticked), ...mortgage.filter((row) => row.ticked && !row.blocked)].flatMap((row) => row.actions);
  return {
    owe, cash, short, raised, toFind,
    covered: owe > 0 && toFind === 0,
    // Everything there is to raise (all rows ticked): when even that is short, only a loan or
    // bankruptcy is left.
    most: sell.reduce((sum, row) => sum + row.amount, 0) + mortgage.reduce((sum, row) => sum + row.amount, 0),
    sell, mortgage,
    picked: [...ticked],
    actions: toFind === 0 ? [...actions, { type: "pay_debt" }] : actions,
  };
}

// Tick or untick one row; returns the new list of picked keys.
export function toggleDebtRow(checklist, key) {
  const picked = new Set(checklist.picked);
  if (picked.has(key)) picked.delete(key);
  else picked.add(key);
  return [...picked];
}
