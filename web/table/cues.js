// Sound cues for the PC table that the animation events don't cover (pure, unit-tested): an
// auction opening and being won, a trade or loan offer arriving, a deal done or refused, a loan,
// a mortgage, cash from a card or the jackpot, and "your turn" for a seat at this screen.
// The animation events (dice, hops, rent, buy, build, jail, bankrupt) play their own sounds.

// Effects for one update: [{ name, opts }], each name at most once.
//   prev, next  the states before and after
//   receipts    receiptsFor(prev, next) (web/shared/receipts.js)
//   localIds    player ids seated at this screen (keyboard / controller)
import { inputOf } from "../shared/format.js";

const computer = (state, id) => inputOf(state?.players?.find((player) => player.id === id)) === "ai";

export function soundCues(prev, next, receipts = [], localIds = []) {
  if (!prev || !next || prev.code !== next.code) return [];
  const out = new Map();
  const add = (name, opts = {}) => {
    if (!out.has(name)) out.set(name, { name, opts });
  };
  if (next.phase === "auction" && prev.phase !== "auction") add("gavel");
  // Computers trading among themselves stay silent (an accepted deal still sounds).
  if (next.phase === "trade" && prev.phase !== "trade" && !(computer(next, next.trade?.proposerId) && computer(next, next.trade?.targetId))) add("offer");
  if (next.phase === "loan_offer" && prev.phase !== "loan_offer") add("offer");
  for (const receipt of receipts) {
    switch (receipt.kind) {
      case "auction": add("gavel", { sold: true }); break;
      case "trade": add("accept"); break;
      case "declined":
        if (!(computer(next, receipt.playerId) && computer(next, receipt.otherId))) add("decline");
        break;
      case "loan": add("loan"); break;
      case "mortgage": add("mortgage"); break;
      case "sell": add("coins", { amount: Math.abs(receipt.amount), collect: true }); break;
      case "jackpot":
      case "card":
        if (receipt.amount > 0) add("coins", { amount: receipt.amount, collect: true });
        break;
      default: break;
    }
  }
  const turnStarts = next.status === "playing" && next.phase === "awaiting_roll"
    && (prev.currentPlayerId !== next.currentPlayerId || prev.phase === "determining_order" || prev.phase === "order_countdown");
  if (turnStarts && localIds.includes(next.currentPlayerId)) add("turn");
  return [...out.values()];
}
