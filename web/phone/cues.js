// Outcome cues: the engine log turned into short visual chips ("Stamp Duty −$200", "PAYDAY +$200",
// "Alpha → Maya $10", "SOLD Skyline Rd → Alpha $140", "Jail · 3 doubles"). Pure: no DOM, so the
// phone can render them anywhere and the checks can run them in Node.

import { escapeHtml, inputOf, money, parseLog, playerById } from "../shared/format.js";
import { inkOn, playerChipHtml } from "../shared/tokens.js";
import { icon } from "./icons.js";

export function shortName(name) {
  return String(name || "")
    .replace(/ Railroad$/, "")
    .replace(/ Street$/, " St")
    .replace(/ Avenue$/, " Ave")
    .replace(/ Road$/, " Rd");
}

function shortCard(text) {
  const clean = String(text || "").replace(/;.*$/, "").replace(/\.$/, "");
  return clean.length > 34 ? `${clean.slice(0, 32).trimEnd()}…` : clean;
}

const TURN_MARK = /'s turn$/;

// Index of the entry that started the current turn (or -1).
export function turnStartIndex(state) {
  const log = state?.log || [];
  for (let index = log.length - 1; index >= 0; index -= 1) {
    if (TURN_MARK.test(log[index].text)) return index;
  }
  return -1;
}

// One cue per interesting log entry from `from` on. `ctx` entries before `from` give jail reasons.
export function logCues(state, viewerId, { from = 0 } = {}) {
  const log = state?.log || [];
  const cues = [];
  const context = new Map(); // playerId → { land, card, triple }
  const ctxFor = (id) => {
    if (!context.has(id)) context.set(id, { land: "", card: "", triple: false });
    return context.get(id);
  };
  // Offers between two computers, and their refusals and counters, stay out of the strip (an
  // accepted deal still shows): they are in the log and the game record.
  const computer = (id) => inputOf(playerById(state, id)) === "ai";
  let offer = null;
  for (let index = Math.max(0, from - 8); index < log.length; index += 1) {
    const entry = log[index];
    const item = parseLog(entry, state);
    if (item.kind === "trade" && / proposed a trade to /.test(entry.text)) offer = { from: item.playerId, to: item.otherId };
    let cue = cueFor(entry, item, state, viewerId, item.playerId ? ctxFor(item.playerId) : null);
    if (cue && item.kind === "trade" && !/ accepted .+'s trade$/.test(entry.text) && offer && computer(offer.from) && computer(offer.to)) cue = { ...cue, quiet: true };
    if (item.playerId) {
      const c = ctxFor(item.playerId);
      if (item.kind === "turn") context.set(item.playerId, { land: "", card: "", triple: false });
      else if (item.kind === "land") c.land = entry.text.replace(/^.* landed on /, "");
      else if (item.kind === "card") c.card = entry.text.replace(/^.*? drew: /, "");
      else if (/rolled doubles three times/.test(entry.text)) c.triple = true;
    }
    if (cue && index >= from) cues.push({ ...cue, id: entry.id || `log_${index}`, at: entry.at || 0 });
  }
  return cues;
}

export function turnCues(state, viewerId, { limit = 3, includeQuiet = false } = {}) {
  const start = turnStartIndex(state);
  const cues = logCues(state, viewerId, { from: start < 0 ? 0 : start + 1 }).filter((cue) => includeQuiet || !cue.quiet);
  return cues.slice(-limit);
}

// Cues for log entries that are new in `next` (for toasts after a state arrives).
export function newCues(prev, next, viewerId) {
  if (!prev || !next || prev.code !== next.code) return [];
  const seen = new Set((prev.log || []).map((entry) => entry.id));
  const log = next.log || [];
  let from = log.length;
  for (let index = 0; index < log.length; index += 1) {
    if (!seen.has(log[index].id)) { from = index; break; }
  }
  return logCues(next, viewerId, { from }).filter((cue) => !cue.quiet);
}

function who(state, id, viewerId) {
  if (id && id === viewerId) return "You";
  return playerById(state, id)?.name || "Someone";
}

// amount: money moved; dir: +1 viewer gains, −1 viewer pays, 0 viewer not involved.
function moneyCue(base, amount, dir) {
  return { ...base, amount: dir === 0 ? Math.abs(amount) : dir * Math.abs(amount), tone: dir > 0 ? "pos" : dir < 0 ? "neg" : "" };
}

function cueFor(entry, item, state, viewerId, ctx) {
  const text = entry.text;
  const actor = item.playerId;
  const mine = actor && actor === viewerId;
  const base = { kind: item.kind, icon: "coin", text: "", amount: null, tone: "", playerId: actor, otherId: null, spaceIndex: item.spaceIndex ?? null, important: false, quiet: false };
  let match;
  switch (item.kind) {
    case "turn":
    case "confirm":
    case "system":
    case "order":
    case "join":
      return null;
    case "land":
      return { ...base, quiet: true, icon: "pin", text: `${who(state, actor, viewerId)} → ${shortName(text.replace(/^.* landed on /, ""))}` };
    case "roll": {
      match = text.match(/rolled (\d) \+ (\d) = (\d+)$/);
      if (!match) return null;
      const dice = [Number(match[1]), Number(match[2])];
      return { ...base, quiet: true, icon: "dice", dice, total: Number(match[3]), doubles: dice[0] === dice[1], text: `${who(state, actor, viewerId)} rolled ${match[3]}` };
    }
    case "go":
      return moneyCue({ ...base, icon: "go", text: mine ? "PAYDAY" : `${who(state, actor, viewerId)} · PAYDAY` }, 200, mine ? 1 : 0);
    case "rent": {
      const to = item.otherId;
      const amount = Number(text.match(/paid \$(\d+)/)?.[1] || 0);
      const square = shortName(state.board?.[item.spaceIndex]?.name || "");
      const dir = mine ? -1 : to === viewerId ? 1 : 0;
      const label = mine ? `Rent → ${who(state, to, viewerId)}` : to === viewerId ? `Rent ← ${who(state, actor, viewerId)}` : `${who(state, actor, viewerId)} → ${who(state, to, viewerId)}`;
      return moneyCue({ ...base, kind: "rent", icon: "swap", text: label, sub: square, otherId: to, important: dir !== 0 }, amount, dir);
    }
    case "pay": {
      const amount = Math.abs(item.amount);
      const reason = text.match(/\((.+)\)$/)?.[1] || "";
      let label = "Paid";
      let glyph = "coin";
      if (/(?:Tax|Duty)$/.test(reason)) { label = reason; glyph = "bank"; } // Stamp Duty, Council Tax
      else if (/jail fee/.test(reason)) { label = "Bail"; glyph = "unlock"; }
      else if (reason === "card charge") { label = "Card"; glyph = "card"; }
      else if (reason === "building repairs") { label = "Repairs"; glyph = "wrench"; }
      else if (/^loan repayment/.test(reason)) { label = "Loan repaid"; glyph = "loan"; }
      else if (/transfer fee/.test(text)) { label = "Transfer fee"; glyph = "coin"; }
      return moneyCue({ ...base, icon: glyph, text: mine ? label : `${who(state, actor, viewerId)} · ${label}` }, amount, mine ? -1 : 0);
    }
    case "buy": {
      const square = shortName(state.board?.[item.spaceIndex]?.name || text.replace(/^.* (?:bought|won) (.+) for .*$/, "$1"));
      const price = Math.abs(item.amount);
      if (/ won /.test(text)) {
        return moneyCue({ ...base, kind: "auction", icon: "hammer", text: `SOLD ${square} → ${who(state, actor, viewerId)}`, important: true }, price, mine ? -1 : 0);
      }
      return moneyCue({ ...base, icon: "house", text: mine ? `Bought ${square}` : `${who(state, actor, viewerId)} bought ${square}` }, price, mine ? -1 : 0);
    }
    case "auction":
      if (/got no bids/.test(item.text)) return { ...base, icon: "hammer", text: `No bids · ${shortName(item.text.replace(/ got no bids$/, ""))}`, important: true };
      return { ...base, icon: "hammer", quiet: true, text: `Auction · ${shortName(item.text.replace(/ went to auction$/, ""))}` };
    case "bid":
      return { ...base, quiet: true, icon: "hammer", text: `${who(state, actor, viewerId)} bid ${money(item.bid)}` };
    case "pass":
      return { ...base, quiet: true, icon: "cross", text: `${who(state, actor, viewerId)} passed` };
    case "card":
      return { ...base, icon: "card", text: shortCard(text.replace(/^.*? drew: /, "")), deck: ctx?.land === "Post" || ctx?.land === "Community Chest" ? "chest" : "chance" };
    case "jail": {
      if (/was sent to jail$/.test(text)) {
        let reason = "";
        if (ctx?.triple) reason = "3 doubles";
        else if (ctx?.card && /jail/i.test(ctx.card)) reason = "card";
        else if (ctx?.land === "Busted" || ctx?.land === "Go to Jail") reason = "Busted";
        return { ...base, icon: "lock", text: `${mine ? "Jail" : `${who(state, actor, viewerId)} · jail`}${reason ? ` · ${reason}` : ""}`, reason, important: true };
      }
      if (/rolled doubles three times/.test(text)) return { ...base, quiet: true, icon: "dice", text: "3 doubles" };
      if (/left jail|used a Jail pass|used a Get Out of Jail/.test(text)) return { ...base, icon: "unlock", text: `${who(state, actor, viewerId)} ${mine ? "are" : "is"} out of jail` };
      match = text.match(/remains in jail \((\d)\/3\)/);
      if (match) return { ...base, icon: "lock", text: `${mine ? "Still in jail" : `${who(state, actor, viewerId)} stays in jail`} · ${match[1]}/3` };
      return null;
    }
    case "trade": {
      if ((match = text.match(/ accepted (.+)'s trade$/))) {
        const proposer = state.players?.find((player) => player.name === match[1]);
        const involved = actor === viewerId || proposer?.id === viewerId;
        const label = proposer?.id === viewerId ? `${who(state, actor, viewerId)} accepted your trade` : actor === viewerId ? "You accepted the trade" : `${who(state, actor, viewerId)} accepted ${match[1]}'s trade`;
        return { ...base, icon: "deal", tone: involved ? "pos" : "", text: label, otherId: proposer?.id || null, important: true, result: "accepted" };
      }
      if ((match = text.match(/ (cancelled|declined) the trade$/))) {
        return { ...base, icon: "cross", tone: "", text: `${who(state, actor, viewerId)} ${match[1]} the trade`, important: true, result: match[1] };
      }
      if (/ countered .+'s offer$/.test(text)) {
        return { ...base, icon: "deal", text: `${who(state, actor, viewerId)} made ${item.otherId === viewerId ? "you" : who(state, item.otherId, viewerId)} a counter-offer`, otherId: item.otherId, important: item.otherId === viewerId };
      }
      return { ...base, icon: "deal", text: `${who(state, actor, viewerId)} offered ${item.otherId === viewerId ? "you" : who(state, item.otherId, viewerId)} a trade`, otherId: item.otherId, important: false };
    }
    case "build": {
      const square = shortName(state.board?.[item.spaceIndex]?.name || "");
      const hotel = / hotel /.test(text);
      return moneyCue({ ...base, icon: "house", text: `${mine ? "" : `${who(state, actor, viewerId)} · `}${hotel ? "Hotel" : "House"} on ${square}` }, Math.abs(item.amount), mine ? -1 : 0);
    }
    case "sell":
      return moneyCue({ ...base, icon: "house", text: mine ? "Sold a building" : `${who(state, actor, viewerId)} sold a building` }, Math.abs(item.amount), mine ? 1 : 0);
    case "mortgage": {
      const paidOff = item.amount < 0;
      const square = shortName(text.replace(/^.* (?:un)?mortgaged (.+) for .*$/, "$1"));
      return moneyCue({ ...base, icon: "bank", text: `${mine ? "" : `${who(state, actor, viewerId)} · `}${paidOff ? "Paid off" : "Mortgaged"} ${square}` }, Math.abs(item.amount), mine ? (paidOff ? -1 : 1) : 0);
    }
    case "debt":
      return { ...base, icon: "warn", tone: mine ? "neg" : "", text: `${who(state, actor, viewerId)} ${mine ? "owe" : "owes"} ${money(text.match(/raise \$(\d+)/)?.[1] || 0)}`, important: mine };
    case "bankrupt": {
      // "Dev is out · all to you" for the creditor; the board card shows the deeds moving.
      const heir = state.players?.find((player) => player.name === (text.match(/assets transferred to (.+)$/)?.[1] || ""));
      const tail = heir?.id === viewerId ? " · all to you" : heir ? ` · all to ${heir.name}` : "";
      return { ...base, icon: "out", tone: heir?.id === viewerId ? "pos" : "neg", text: `${who(state, actor, viewerId)} ${mine ? "are" : "is"} out${tail}`, otherId: heir?.id || null, important: true };
    }
    case "win":
      return { ...base, icon: "trophy", tone: "pos", text: `${who(state, actor, viewerId)} ${mine ? "win" : "wins"}!`, important: true };
    default:
      break;
  }
  if ((match = text.match(/ collected the \$(\d+) (?:City Park|Free Parking) jackpot$/))) {
    return moneyCue({ ...base, kind: "jackpot", icon: "park", text: mine ? "City Park" : `${who(state, actor, viewerId)} · City Park` }, Number(match[1]), mine ? 1 : 0);
  }
  if ((match = text.match(/ lent (.+?) \$(\d+)/))) {
    const borrower = state.players?.find((player) => player.name === match[1]);
    const dir = mine ? -1 : borrower?.id === viewerId ? 1 : 0;
    return moneyCue({ ...base, kind: "loan", icon: "loan", text: `${who(state, actor, viewerId)} lent ${borrower?.id === viewerId ? "you" : match[1]}`, otherId: borrower?.id || null, important: dir !== 0 }, Number(match[2]), dir);
  }
  if ((match = text.match(/ repaid (.+?) \$(\d+)/))) {
    const lender = state.players?.find((player) => player.name === match[1]);
    const dir = mine ? -1 : lender?.id === viewerId ? 1 : 0;
    return moneyCue({ ...base, kind: "loan", icon: "loan", text: `${who(state, actor, viewerId)} repaid ${lender?.id === viewerId ? "you" : match[1]}`, otherId: lender?.id || null, important: dir !== 0 }, Number(match[2]), dir);
  }
  if (/ (declined|cancelled) the loan offer$/.test(text)) return { ...base, kind: "loan", icon: "cross", text: `${who(state, actor, viewerId)} ${/declined/.test(text) ? "declined" : "cancelled"} the loan`, important: true };
  if (/ missed the loan deadline/.test(text)) return { ...base, kind: "loan", icon: "warn", tone: mine ? "neg" : "", text: `${who(state, actor, viewerId)} missed a loan deadline`, important: true };
  if ((match = text.match(/ declined (.+); it stays with the bank$/))) return { ...base, kind: "pass", icon: "cross", text: `${who(state, actor, viewerId)} passed on ${shortName(match[1])}` };
  return null;
}

// --- Rendering ----------------------------------------------------------------------------------

export function inkFor(hex) {
  return inkOn(hex);
}

export function dotHtml(state, playerId, { size = 16 } = {}) {
  const player = playerById(state, playerId);
  if (!player) return "";
  return playerChipHtml(state, player, { size, className: "pdot" });
}

export function cueAmount(cue) {
  if (cue.amount === null || cue.amount === undefined) return "";
  if (cue.tone === "pos") return `+${money(Math.abs(cue.amount))}`;
  if (cue.tone === "neg") return money(-Math.abs(cue.amount));
  return money(Math.abs(cue.amount));
}

export function cueHtml(cue, state, { className = "" } = {}) {
  const amount = cueAmount(cue);
  const dots = cue.kind === "rent" ? `${dotHtml(state, cue.playerId, { size: 14 })}${dotHtml(state, cue.otherId, { size: 14 })}` : "";
  const deed = Number.isInteger(cue.spaceIndex) && ["property", "railroad", "utility"].includes(state?.board?.[cue.spaceIndex]?.type);
  return `<span class="cue cue--${cue.tone || "plain"} cue--${cue.kind} ${className}"${deed ? ` data-sheet="${cue.spaceIndex}" role="button" tabindex="0"` : ""}>${icon(cue.icon)}${dots}<span class="cue__text">${escapeHtml(cue.text)}</span>${amount ? `<span class="cue__amt">${amount}</span>` : ""}</span>`;
}
