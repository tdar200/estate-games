// What a player can do with one deed from its card (0.10.1): tap any property — a board square, a
// deed in a list, a trade tile — and its detail shows these. Each action follows the engine's own
// rule (internal/engine build.go, auction.go, trade.go), so a button is live only when the server
// would take it, and says in one line why not when it isn't. Pure: both clients use it.
//
// deedActions(state, viewerId, index, { planTrades }) → { ownerId, mine, actions: [{ id, label,
// detail, act | cmd + arg, disabled, reason, tone }] }, in the order to show them. act is an engine
// action to send; cmd is a client command (deed-trade, deed-ask, deed-not-selling). planTrades: the
// client can save a trade for your turn (the phone), so TRADE / ASK aren't greyed off-turn.

import { colourWord, inputOf, money, playerById } from "./format.js";
import { canManage, groupIndexes, loanSecuredBy, ownsGroup, propState, spaceAt } from "./insights.js";

// The engine's unmortgage price: half the price plus 10%, rounded up, in whole numbers
// (UnmortgageCost, as insights.unmortgageCost).
export function unmortgagePrice(price) {
  return Math.ceil((price * 11) / 20);
}

function bankSupply(state) {
  if (state?.bank && typeof state.bank.houses === "number") return { houses: state.bank.houses, hotels: state.bank.hotels ?? 12 };
  let houses = 0;
  let hotels = 0;
  for (const prop of Object.values(state?.properties || {})) {
    if (prop?.houses === 5) hotels += 1;
    else houses += prop?.houses || 0;
  }
  return { houses: 32 - houses, hotels: 12 - hotels };
}

const isOwnable = (space) => ["property", "railroad", "utility"].includes(space?.type);
const streetName = (state, index) => state.board[index]?.name || "another street";

export function deedActions(state, viewerId, index, { planTrades = false } = {}) {
  const space = spaceAt(state, index);
  const prop = propState(state, index);
  const viewer = playerById(state, viewerId);
  const out = { ownerId: prop?.ownerId || null, mine: false, actions: [] };
  if (!space || !isOwnable(space) || !prop || !viewer || viewer.bankrupt || state?.status !== "playing") return out;
  const owner = playerById(state, prop.ownerId);
  const manage = canManage(state, viewerId);
  const inDebt = state.phase === "debt";
  const notYourTurn = inDebt ? "Not while someone else owes money" : "On your turn";
  const add = (action) => out.actions.push({ disabled: Boolean(action.reason), ...action });
  const group = space.type === "property" ? groupIndexes(state, space.group) : [index];
  const groupBuilt = space.type === "property" && group.some((i) => (propState(state, i)?.houses || 0) > 0);
  const colour = space.type === "property" ? colourWord(space.group) : "";
  const pledged = Boolean(loanSecuredBy(state, index));
  // offerTrade: the current player, before rolling or after, with no other trade pending.
  const tradeTurn = planTrades || (state.currentPlayerId === viewerId && ["awaiting_roll", "turn_complete"].includes(state.phase) && !state.trade)
    ? "" : state.phase === "trade" ? "Wait for the pending trade" : "Trade on your turn";

  if (owner && owner.id === viewerId) {
    out.mine = true;
    const cash = viewer.cash || 0;
    if (space.type === "property") {
      const houses = prop.houses || 0;
      const levels = group.map((i) => propState(state, i)?.houses || 0);
      const min = Math.min(...levels);
      const max = Math.max(...levels);
      if (houses < 5) {
        const cost = space.buildCost || 0;
        let reason = "";
        if (!ownsGroup(state, viewerId, space.group)) reason = `Own all of the ${colour} set to build`;
        else if (group.some((i) => propState(state, i)?.mortgaged)) reason = `Unmortgage the ${colour} set first`;
        else if (group.some((i) => loanSecuredBy(state, i))) reason = "Part of this set secures a loan";
        else if (inDebt) reason = "Not while you owe money";
        else if (!manage) reason = notYourTurn;
        else if (houses !== min) reason = `Build evenly: next on ${streetName(state, group.find((i) => (propState(state, i)?.houses || 0) === min))}`;
        else if (houses === 4 && bankSupply(state).hotels < 1) reason = "The bank has no hotels left";
        else if (houses < 4 && bankSupply(state).houses < 1) reason = "The bank has no houses left";
        else if (cash < cost) reason = `Costs ${money(cost)}; you have ${money(cash)}`;
        add({ id: "build", label: houses === 4 ? "BUILD A HOTEL" : "BUILD A HOUSE", detail: `−${money(cost)}`, act: { type: "build", spaceIndex: index }, reason, tone: "primary" });
      }
      if (houses > 0) {
        let reason = "";
        if (!manage) reason = notYourTurn;
        else if (houses !== max) reason = `Sell evenly: first from ${streetName(state, group.find((i) => (propState(state, i)?.houses || 0) === max))}`;
        add({ id: "sell", label: houses === 5 ? "SELL THE HOTEL" : "SELL A HOUSE", detail: `+${money(Math.floor((space.buildCost || 0) / 2))}`, act: { type: "sell_building", spaceIndex: index }, reason });
      }
    }
    if (!prop.mortgaged) {
      let reason = "";
      if (groupBuilt) reason = `Sell the buildings on the ${colour} set first`;
      else if (pledged) reason = "It secures a loan";
      else if (!manage) reason = notYourTurn;
      add({ id: "mortgage", label: "MORTGAGE", detail: `+${money(Math.floor(space.price / 2))}`, act: { type: "mortgage", spaceIndex: index }, reason });
    } else {
      const cost = unmortgagePrice(space.price);
      let reason = "";
      if (inDebt) reason = "Not while you owe money";
      else if (!manage) reason = notYourTurn;
      else if (cash < cost) reason = `Costs ${money(cost)}; you have ${money(cash)}`;
      add({ id: "unmortgage", label: "UNMORTGAGE", detail: `−${money(cost)} with interest`, act: { type: "unmortgage", spaceIndex: index }, reason });
    }
    add({ id: "trade", label: "TRADE THIS…", cmd: "deed-trade", arg: String(index),
      reason: groupBuilt ? `Sell the buildings on the ${colour} set first` : pledged ? "It secures a loan" : tradeTurn });
    if (inputOf(viewer) !== "ai") {
      const off = (viewer.notSelling || []).includes(index);
      add({ id: "not-selling", label: off ? "NOT SELLING TO COMPUTERS · SELL AGAIN" : "DON'T SELL TO COMPUTERS", cmd: "deed-not-selling", arg: `${index}:${off ? "false" : "true"}`, on: off });
    }
    return out;
  }
  if (owner && !owner.bankrupt) {
    add({ id: "ask", label: "ASK TO BUY…", detail: owner.name, cmd: "deed-ask", arg: String(index),
      reason: groupBuilt ? `It has buildings: ${owner.name} would have to sell them first` : pledged ? `It secures a loan of ${owner.name}'s` : tradeTurn });
    return out;
  }
  // Unowned: BUY when you are standing on it with the choice to make.
  if (!owner && state.phase === "awaiting_purchase" && state.currentPlayerId === viewerId && Number(state.pendingPurchase) === index) {
    const cash = viewer.cash || 0;
    add({ id: "buy", label: "BUY", detail: money(space.price), act: { type: "buy" }, reason: cash < space.price ? `Costs ${money(space.price)}; you have ${money(cash)}` : "", tone: "primary" });
  }
  return out;
}
