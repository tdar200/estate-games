// Outcome "receipts": who gained or lost what between two consecutive states, read from the new
// log lines. The PC table shows them as brief chips next to the player plates; the phone can show
// the same list as its receipt strip ("Stamp Duty −$200", "PAYDAY +$200", "Ravi accepted"). Pure.
//
//   receiptsFor(prev, next) → [{ key, playerId, otherId, kind, amount, label, spaceIndex }]
//   kind: "rent" | "tax" | "fee" | "go" | "buy" | "auction" | "build" | "sell" | "mortgage" |
//         "card" | "jackpot" | "loan" | "trade" | "declined" | "jail" | "bankrupt"
//   amount: signed cash change for playerId (0 for non-money outcomes such as a trade).

import { newLogEntries } from "./events.js";
import { parseLog, playerById } from "./format.js";

function labelForFee(reason) {
  if (/tax|duty/i.test(reason)) return { kind: "tax", label: reason.replace(/^the /, "").toUpperCase() }; // Stamp Duty, Council Tax (Income/Luxury Tax before the rename)
  if (/bail|jail fee/i.test(reason)) return { kind: "fee", label: "BAIL" };
  if (/card/i.test(reason)) return { kind: "card", label: "CARD" };
  if (/repairs/i.test(reason)) return { kind: "card", label: "REPAIRS" };
  if (/transfer fee/i.test(reason)) return { kind: "fee", label: "FEE" };
  return { kind: "fee", label: "PAID" };
}

function nameOf(state, id) {
  return playerById(state, id)?.name || "";
}

function playerNamed(state, name) {
  return (state?.players || []).find((player) => player.name === name) || null;
}

export function receiptsFor(prev, next) {
  if (!prev || !next || prev.code !== next.code) return [];
  const out = [];
  const push = (entry, playerId, fields) => {
    if (!playerId) return;
    out.push({ key: `${entry.id ?? entry.text}:${playerId}:${fields.kind}`, playerId, otherId: null, amount: 0, label: "", spaceIndex: null, ...fields });
  };
  for (const entry of newLogEntries(prev, next)) {
    const text = entry.text || "";
    const item = parseLog(entry, next);
    const actor = item.playerId;
    let match;
    switch (item.kind) {
      case "rent":
        push(entry, actor, { kind: "rent", amount: -item.amount, otherId: item.otherId, label: nameOf(next, item.otherId).toUpperCase() || "RENT", spaceIndex: item.spaceIndex });
        push(entry, item.otherId, { kind: "rent", amount: item.amount, otherId: actor, label: "RENT", spaceIndex: item.spaceIndex });
        break;
      case "pay": {
        match = text.match(/ paid \$\d+ \((.+)\)$/) || text.match(/ paid a \$\d+ (mortgage transfer fee)$/);
        const { kind, label } = labelForFee(match ? match[1] : "");
        push(entry, actor, { kind, amount: item.amount, label });
        break;
      }
      case "go":
        push(entry, actor, { kind: "go", amount: 200, label: "PAYDAY" });
        break;
      case "buy":
        push(entry, actor, { kind: /at auction$/.test(item.text) ? "auction" : "buy", amount: item.amount, label: /at auction$/.test(item.text) ? "WON" : "BOUGHT", spaceIndex: item.spaceIndex });
        break;
      case "build":
        push(entry, actor, { kind: "build", amount: item.amount, label: "HOUSE", spaceIndex: item.spaceIndex });
        break;
      case "sell":
        push(entry, actor, { kind: "sell", amount: item.amount, label: "SOLD" });
        break;
      case "mortgage":
        push(entry, actor, { kind: "mortgage", amount: item.amount, label: item.amount > 0 ? "MORTGAGE" : "LIFTED" });
        break;
      case "card":
        match = text.match(/collect \$(\d+)(?!.*if you pass)/i);
        if (match && !/advance to/i.test(text)) push(entry, actor, { kind: "card", amount: Number(match[1]), label: "CARD" });
        break;
      case "trade":
        if ((match = text.match(/^(.+) accepted (.+)'s trade$/))) {
          const proposer = playerNamed(next, match[2]);
          push(entry, actor, { kind: "trade", label: "DEAL", otherId: proposer?.id || null });
          push(entry, proposer?.id, { kind: "trade", label: "DEAL", otherId: actor });
        } else if (/ declined the trade$/.test(text)) {
          const proposer = next.trade?.proposerId || prev.trade?.proposerId;
          push(entry, proposer || actor, { kind: "declined", label: "DECLINED", otherId: proposer ? actor : null });
        }
        break;
      case "jail":
        if (/was sent to jail$/.test(text)) push(entry, actor, { kind: "jail", label: "JAIL" });
        break;
      case "bankrupt":
        push(entry, actor, { kind: "bankrupt", label: "OUT" });
        break;
      default:
        // "Free Parking": games saved before the rename.
        if ((match = text.match(/^(.+) collected the \$(\d+) (?:City Park|Free Parking) jackpot$/))) {
          push(entry, playerNamed(next, match[1])?.id, { kind: "jackpot", amount: Number(match[2]), label: "JACKPOT" });
        // The server writes "A lent B $300; B repays $360 by turn 14": the first amount counts.
        } else if ((match = text.match(/^(.+?) lent (.+?) \$(\d+)/))) {
          const lender = playerNamed(next, match[1]);
          const borrower = playerNamed(next, match[2]);
          push(entry, lender?.id, { kind: "loan", amount: -Number(match[3]), label: "LOAN", otherId: borrower?.id || null });
          push(entry, borrower?.id, { kind: "loan", amount: Number(match[3]), label: "LOAN", otherId: lender?.id || null });
        } else if ((match = text.match(/^(.+?) repaid (.+?) \$(\d+)/))) {
          const borrower = playerNamed(next, match[1]);
          const lender = playerNamed(next, match[2]);
          push(entry, borrower?.id, { kind: "loan", amount: -Number(match[3]), label: "REPAID", otherId: lender?.id || null });
          push(entry, lender?.id, { kind: "loan", amount: Number(match[3]), label: "REPAID", otherId: borrower?.id || null });
        }
        break;
    }
  }
  return out;
}
