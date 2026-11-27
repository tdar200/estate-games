// Formatting helpers shared by the table and phone apps. Pure: no DOM access.

export const GROUP_COLORS = {
  brown: "#8A4B2A",
  lightBlue: "#A9DCF2",
  pink: "#D3368C",
  orange: "#F7941D",
  red: "#E8232A",
  yellow: "#FDE21B",
  green: "#1FAE57",
  darkBlue: "#1166B4",
};

// Text colour that reads on each group colour (headers in the phone property tab).
export const GROUP_INK = {
  brown: "#FFFFFF",
  lightBlue: "#15191E",
  pink: "#FFFFFF",
  orange: "#15191E",
  red: "#FFFFFF",
  yellow: "#15191E",
  green: "#FFFFFF",
  darkBlue: "#FFFFFF",
};

export const COLOUR_WORDS = {
  brown: "brown",
  lightBlue: "light blue",
  pink: "pink",
  orange: "orange",
  red: "red",
  yellow: "yellow",
  green: "green",
  darkBlue: "dark blue",
};

const MINUS = "−";

export function money(value) {
  const number = Math.round(Number(value) || 0);
  const text = `$${Math.abs(number).toLocaleString("en-US")}`;
  return number < 0 ? `${MINUS}${text}` : text;
}

export function signedMoney(value) {
  const number = Math.round(Number(value) || 0);
  if (number === 0) return "$0";
  return number > 0 ? `+${money(number)}` : money(number);
}

export function pct(probability) {
  return `${Math.round(probability * 100)}%`;
}

export function ordinal(value) {
  const n = Math.abs(Math.round(value));
  const tens = n % 100;
  if (tens >= 11 && tens <= 13) return `${n}th`;
  return `${n}${{ 1: "st", 2: "nd", 3: "rd" }[n % 10] || "th"}`;
}

export function plural(count, word, pluralWord = `${word}s`) {
  return `${count} ${count === 1 ? word : pluralWord}`;
}

export function possessive(name) {
  return `${name}'s`;
}

export function listNames(names) {
  const items = names.filter(Boolean);
  if (items.length <= 1) return items.join("");
  return `${items.slice(0, -1).join(", ")} and ${items[items.length - 1]}`;
}

export function escapeHtml(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

export function upper(value) {
  return String(value ?? "").toUpperCase();
}

export function colourWord(group) {
  return COLOUR_WORDS[group] || group || "";
}

// The photo a deed card shows (web/shared/photos, credits in photos/CREDITS.txt): one per district,
// one for the stations, one each for the two utilities. "" for squares without a deed.
const GROUP_PHOTOS = {
  brown: "d-oldtown", lightBlue: "d-waterfront", pink: "d-arts", orange: "d-business",
  red: "d-central", yellow: "d-sunshine", green: "d-green", darkBlue: "d-skyline",
};
export function spacePhoto(space) {
  if (!space) return "";
  if (space.type === "property") return GROUP_PHOTOS[space.group] || "";
  if (space.type === "railroad") return "s-station";
  if (space.type === "utility") return /reservoir|water/i.test(space.name || "") ? "s-water" : "s-power";
  return "";
}

export function spaceColor(space) {
  if (!space) return null;
  if (space.type === "property") return GROUP_COLORS[space.group] || null;
  return null;
}

export function playerById(state, id) {
  return state?.players?.find((player) => player.id === id) || null;
}

export function viewerPlayer(state) {
  return playerById(state, state?.viewerId);
}

export function isLocalSeat(state, playerId) {
  return Boolean(state?.localSeats?.some((seat) => seat.playerId === playerId));
}

// What kind of device controls a player. Falls back sensibly when §3 fields are missing.
export function inputOf(player) {
  if (!player) return "classic";
  if (player.input) return player.input;
  return player.kind === "ai" ? "ai" : "classic";
}

// Computer difficulty levels (POST /ai {difficulty}); the lobby cycles through them.
export const AI_LEVELS = ["easy", "normal", "hard", "expert", "grandmaster"];

export function nextAiLevel(level) {
  return AI_LEVELS[(AI_LEVELS.indexOf(level) + 1) % AI_LEVELS.length];
}

export function aiLevelLabel(level) {
  return { easy: "Easy", normal: "Normal", hard: "Hard", expert: "Expert", grandmaster: "Grandmaster" }[level] || "Normal";
}

export function deviceLabel(state, player, { padName } = {}) {
  const input = inputOf(player);
  const host = state?.hostId === player.id ? " · host" : "";
  const offline = player.online === false && input !== "ai" ? " · offline" : "";
  if (input === "phone") return `Phone${host}${offline}`;
  if (input === "ai") return player.difficulty ? `Computer · ${aiLevelLabel(player.difficulty)}` : "Computer";
  if (input === "controller") return padName || "Controller";
  if (input === "keyboard") return "Keyboard";
  return `Browser${host}${offline}`;
}

export function phaseLabel(state) {
  const phase = state?.phase;
  return {
    lobby: "Lobby",
    determining_order: "Rolling for order",
    order_countdown: "Order set",
    awaiting_roll: "Roll",
    awaiting_purchase: "For sale",
    auction: "Auction",
    turn_complete: "Turn complete",
    trade: "Trade offer",
    card_confirmation: "Headlines",
    debt: "Debt",
    finished: "Game over",
  }[phase] || "";
}

// ---------------------------------------------------------------------------------------------
// Log parsing: turns the engine's English log lines into feed items with an actor and amount.

function namesByLength(state) {
  return [...(state?.players || [])].sort((a, b) => b.name.length - a.name.length);
}

function leadingPlayer(state, text) {
  for (const player of namesByLength(state)) {
    if (text === player.name || text.startsWith(`${player.name} `) || text.startsWith(`${player.name}'s `)) return player;
  }
  return null;
}

function spaceByName(state, name) {
  return state?.board?.find((space) => space.name === name) || null;
}

// Returns { kind, text, amount, tone, playerId, otherId, spaceIndex }.
// tone: "pos" (money arriving), "neg" (money leaving) or "" (no money).
export function parseLog(entry, state) {
  const raw = typeof entry === "string" ? entry : entry?.text || "";
  const actor = leadingPlayer(state, raw);
  const base = { kind: "info", text: raw, amount: 0, tone: "", playerId: actor?.id || null, otherId: null, spaceIndex: null, id: entry?.id || null };
  if (!actor) {
    let match = raw.match(/^(.+) entered an open auction$/);
    if (match) return { ...base, kind: "auction", text: `${match[1]} went to auction`, spaceIndex: spaceByName(state, match[1])?.index ?? null };
    match = raw.match(/^(.+) received no bids$/);
    if (match) return { ...base, kind: "auction", text: `${match[1]} got no bids` };
    match = raw.match(/^Turn order: (.+)$/);
    if (match) return { ...base, kind: "order", text: `Turn order: ${match[1]}` };
    match = raw.match(/^(.+) is mortgaged; no rent is collected$/);
    if (match) return { ...base, kind: "land", text: `${match[1]} is mortgaged: no rent` };
    if (/^Game started/.test(raw)) return { ...base, kind: "system", text: "Game started · everyone has $1,500" };
    if (/tied and will roll again/.test(raw)) return { ...base, kind: "order" };
    return { ...base, kind: "system" };
  }
  const name = actor.name;
  const rest = raw.slice(name.length);
  let match;

  if (rest === "'s turn") return { ...base, kind: "turn", text: `${name}'s turn` };
  if ((match = rest.match(/^ rolled (\d) \+ (\d) = (\d+) for turn order$/))) {
    return { ...base, kind: "order", text: `${name} rolled ${match[3]} for turn order` };
  }
  if ((match = rest.match(/^ rolled (\d) \+ (\d) = (\d+)$/))) {
    const double = match[1] === match[2] ? " · doubles" : "";
    return { ...base, kind: "roll", text: `${name} rolled ${match[1]} + ${match[2]}${double}` };
  }
  if ((match = rest.match(/^ landed on (.+)$/))) {
    return { ...base, kind: "land", text: `${name} landed on ${match[1]}`, spaceIndex: spaceByName(state, match[1])?.index ?? null };
  }
  if ((match = rest.match(/^ bought (.+) for \$(\d+)$/))) {
    return { ...base, kind: "buy", text: `${name} bought ${match[1]}`, amount: -Number(match[2]), tone: "neg", spaceIndex: spaceByName(state, match[1])?.index ?? null };
  }
  if ((match = rest.match(/^ won (.+) for \$(\d+)$/))) {
    return { ...base, kind: "buy", text: `${name} won ${match[1]} at auction`, amount: -Number(match[2]), tone: "neg", spaceIndex: spaceByName(state, match[1])?.index ?? null };
  }
  if ((match = rest.match(/^ paid \$(\d+) \((.+) rent\)$/))) {
    const space = spaceByName(state, match[2]);
    const owner = space ? playerById(state, state.properties?.[space.index]?.ownerId) : null;
    const to = owner && owner.id !== actor.id ? `${owner.name} ` : "";
    return { ...base, kind: "rent", text: `${name} paid ${to}rent on ${match[2]}`, amount: Number(match[1]), tone: "pos", otherId: owner?.id || null, spaceIndex: space?.index ?? null };
  }
  if ((match = rest.match(/^ paid \$(\d+) \((.+)\)$/))) {
    const reason = match[2]
      .replace("third failed doubles roll; jail fee", "the jail fee")
      .replace("jail fee", "bail")
      .replace("card charge", "a card charge")
      .replace("building repairs", "for repairs");
    return { ...base, kind: "pay", text: `${name} paid ${reason}`, amount: -Number(match[1]), tone: "neg" };
  }
  if ((match = rest.match(/^ paid a \$(\d+) mortgage transfer fee$/))) {
    return { ...base, kind: "pay", text: `${name} paid a mortgage transfer fee`, amount: -Number(match[1]), tone: "neg" };
  }
  if ((match = rest.match(/^ must raise \$(\d+) to pay (.+)$/))) {
    return { ...base, kind: "debt", text: `${name} owes ${money(match[1])}: ${match[2]}` };
  }
  // Games saved before the rename logged the old wording (GO, Chance, Get Out of Jail Free).
  if (rest === " passed PAYDAY and collected $200" || rest === " passed GO and collected $200") return { ...base, kind: "go", text: `${name} passed PAYDAY`, amount: 200, tone: "pos" };
  if (rest === " was sent to jail") return { ...base, kind: "jail", text: `${name} was sent to jail` };
  if (rest === " rolled doubles three times in a row") return { ...base, kind: "jail", text: `${name} rolled three doubles` };
  if (rest === " rolled doubles and left jail") return { ...base, kind: "jail", text: `${name} rolled doubles and left jail` };
  if ((match = rest.match(/^ did not roll doubles and remains in jail \((\d)\/3\)$/))) {
    return { ...base, kind: "jail", text: `${name} stays in jail (${match[1]} of 3)` };
  }
  if (rest === " used a Jail pass" || rest === " used a Get Out of Jail Free card") return { ...base, kind: "jail", text: `${name} used a Jail pass` };
  if (rest === " paid bail and left jail") return { ...base, kind: "jail", text: `${name} left jail` };
  if ((match = rest.match(/^ paid bail, left jail, and moved (\d+) spaces$/))) {
    return { ...base, kind: "jail", text: `${name} left jail and moved ${match[1]}` };
  }
  if ((match = rest.match(/^ drew: (.+)$/))) return { ...base, kind: "card", text: `${name} drew “${match[1]}”` };
  if (rest === " confirmed the Headlines card" || rest === " confirmed the Chance card") return { ...base, kind: "confirm" };
  if ((match = rest.match(/^ bid \$(\d+)$/))) return { ...base, kind: "bid", text: `${name} bid ${money(match[1])}`, bid: Number(match[1]) };
  if (rest === " passed in the auction") return { ...base, kind: "pass", text: `${name} passed` };
  if ((match = rest.match(/^ proposed a trade to (.+)$/))) {
    const other = leadingPlayer(state, match[1]);
    return { ...base, kind: "trade", text: `${name} offered ${match[1]} a trade`, otherId: other?.id || null };
  }
  if ((match = rest.match(/^ accepted (.+)'s trade$/))) return { ...base, kind: "trade", text: `${name} accepted ${match[1]}'s trade` };
  if ((match = rest.match(/^ (cancelled|declined) the trade$/))) return { ...base, kind: "trade", text: `${name} ${match[1]} the trade` };
  if ((match = rest.match(/^ countered (.+)'s offer$/))) {
    const other = leadingPlayer(state, match[1]);
    return { ...base, kind: "trade", text: `${name} made ${match[1]} a counter-offer`, otherId: other?.id || null };
  }
  // A computer's one line about its answer ("Alpha: I'd do it for $650 + Reservoir.").
  if ((match = rest.match(/^: (.+)$/))) return { ...base, kind: "say", text: `${name}: “${match[1]}”` };
  if ((match = rest.match(/^ built a (house|hotel) on (.+)$/))) {
    const space = spaceByName(state, match[2]);
    return { ...base, kind: "build", text: `${name} built a ${match[1]} on ${match[2]}`, amount: -(space?.buildCost || 0), tone: space ? "neg" : "", spaceIndex: space?.index ?? null };
  }
  if ((match = rest.match(/^ sold one building on (.+)$/))) {
    const space = spaceByName(state, match[1]);
    return { ...base, kind: "sell", text: `${name} sold a building on ${match[1]}`, amount: Math.floor((space?.buildCost || 0) / 2), tone: space ? "pos" : "" };
  }
  if ((match = rest.match(/^ mortgaged (.+) for \$(\d+)$/))) {
    return { ...base, kind: "mortgage", text: `${name} mortgaged ${match[1]}`, amount: Number(match[2]), tone: "pos" };
  }
  if ((match = rest.match(/^ unmortgaged (.+) for \$(\d+)$/))) {
    return { ...base, kind: "mortgage", text: `${name} paid off ${match[1]}`, amount: -Number(match[2]), tone: "neg" };
  }
  if ((match = rest.match(/^ declared bankruptcy(.*)$/))) {
    const tail = match[1].includes("transferred to") ? `; ${match[1].split("transferred to ")[1]} takes everything` : "";
    return { ...base, kind: "bankrupt", text: `${name} went bankrupt${tail}` };
  }
  if (rest === " wins the game") return { ...base, kind: "win", text: `${name} wins the game` };
  if (rest === " joined the room") return { ...base, kind: "join", text: `${name} took a seat` };
  if (rest === " left the room") return { ...base, kind: "join", text: `${name} left` };
  return base;
}

const QUIET_KINDS = new Set(["land", "confirm", "system"]);

// Newest first. Skips noise lines ("landed on", confirmations).
export function feedItems(state, { limit = 6, kinds = null } = {}) {
  const items = [];
  const log = state?.log || [];
  for (let index = log.length - 1; index >= 0 && items.length < limit; index -= 1) {
    const item = parseLog(log[index], state);
    if (QUIET_KINDS.has(item.kind)) continue;
    if (kinds && !kinds.includes(item.kind)) continue;
    items.push(item);
  }
  return items;
}

// Log entries since the viewer's previous turn began (exclusive), oldest first.
export function logSinceLastTurn(state, playerId) {
  const player = playerById(state, playerId);
  const log = state?.log || [];
  if (!player) return [];
  const marker = `${player.name}'s turn`;
  const starts = [];
  log.forEach((entry, index) => { if (entry.text === marker) starts.push(index); });
  if (starts.length === 0) return [];
  const current = starts[starts.length - 1];
  const isMyTurnNow = state.currentPlayerId === playerId;
  const from = isMyTurnNow ? (starts.length >= 2 ? starts[starts.length - 2] : -1) : current;
  const to = isMyTurnNow ? current : log.length;
  // Skip my own previous turn: start from the first other player's turn after it.
  let begin = from + 1;
  for (let index = begin; index < to; index += 1) {
    if (/'s turn$/.test(log[index].text) && log[index].text !== marker) { begin = index; break; }
  }
  return log.slice(begin, to);
}

// "Since your last turn" items, amounts relative to the viewer (+ received, − paid).
export function sinceItems(state, playerId, limit = 3) {
  const entries = logSinceLastTurn(state, playerId);
  const items = [];
  // Offers between computers (and their refusals) are left out; their accepted deals stay.
  const computer = (id) => inputOf(playerById(state, id)) === "ai";
  let offer = null;
  for (const entry of entries) {
    const item = parseLog(entry, state);
    if (item.kind === "trade" && / proposed a trade to /.test(entry.text)) offer = { from: item.playerId, to: item.otherId };
    if (item.kind === "trade" && !/ accepted .+'s trade$/.test(entry.text) && offer && computer(offer.from) && computer(offer.to)) continue;
    if (item.kind === "rent" && item.otherId === playerId) {
      const payer = playerById(state, item.playerId);
      items.push({ text: `${payer?.name || "Someone"} landed on ${state.board[item.spaceIndex]?.name || "your property"}`, amount: item.amount, tone: "pos" });
    } else if (item.kind === "rent" && item.playerId === playerId) {
      items.push({ text: item.text.replace(/^\S+ paid/, "You paid"), amount: -item.amount, tone: "neg" });
    } else if (["buy", "bankrupt", "jail", "trade", "build"].includes(item.kind) && item.playerId !== playerId) {
      items.push({ text: item.text, amount: 0, tone: "" });
    } else if (item.kind === "trade" && item.playerId === playerId) {
      items.push({ text: item.text.replace(/^\S+ /, "You "), amount: 0, tone: "" });
    }
  }
  const withMoney = items.filter((item) => item.amount);
  const without = items.filter((item) => !item.amount);
  return [...withMoney.slice(-limit), ...without.slice(-(limit - Math.min(limit, withMoney.length)))].slice(0, limit);
}

// How long a drawn card stays on screen to be read: max(3.5 s, 2 s + 55 ms per character), at
// most 9 s. (A card a person must confirm stays until they do.)
export function cardReadMs(text) {
  const length = String(text || "").length;
  return Math.min(9000, Math.max(3500, 2000 + 55 * length));
}

// ---- A computer's trade price (POST /trade-quote) -------------------------------------------
// quote: { loading } | { error } | { offerCash, requestCash }. The price is net cash: what the
// computer wants from you (negative: what it pays you). Your draft's cash is compared with the
// last quote on the client, so typing cash never asks the server again. Every server answer for
// "no price" starts with "No deal" (Hard, Expert, Grandmaster) or is the Normal heuristic's
// "not willing" / "enough cash" message (Easy, Normal).
export function quoteState(quote, offerCash = 0, requestCash = 0) {
  if (!quote || quote.loading) return { kind: "wait", text: "THINKING" };
  if (quote.error) {
    const msg = String(quote.error);
    if (/not for any price you can pay|enough cash/i.test(msg)) return { kind: "short", text: "MORE THAN YOU HAVE", detail: "Its price is more cash than you have" };
    if (/^no deal|not willing|won't/i.test(msg)) {
      const why = msg.replace(/^no deal:?\s*/i, "").replace(/\.$/, "");
      return { kind: "never", text: "WON'T TRADE AT ANY PRICE", detail: why && !/^at any price$/i.test(why) && !/not willing/i.test(why) ? why.charAt(0).toUpperCase() + why.slice(1) : "" };
    }
    return { kind: "error", text: "" };
  }
  const net = (quote.offerCash || 0) - (quote.requestCash || 0);
  const mine = (offerCash || 0) - (requestCash || 0);
  const text = net > 0 ? `ACCEPTS AT ${money(net)}` : net < 0 ? `PAYS YOU UP TO ${money(-net)}` : "ACCEPTS AS IS";
  if (mine >= net) return { kind: "ok", text, status: "WILL ACCEPT", net, offerCash: Math.max(0, net), requestCash: Math.max(0, -net) };
  return { kind: "need", text, status: `${money(net - mine)} MORE NEEDED`, need: net - mine, net, offerCash: Math.max(0, net), requestCash: Math.max(0, -net) };
}

// What changed between a person's offer and a computer's counter-offer, from the person's side:
// [{ text: "+$348", tone: "bad" | "good" }] (more cash or deeds from you is "bad").
export function counterChanges(state, trade, viewerId) {
  const original = trade?.original;
  if (!original || trade.targetId !== viewerId) return [];
  // The original offer was the viewer's: they gave original.offer and got original.request. In
  // the counter they give trade.request and get trade.offer.
  const before = (original.offer?.cash || 0) - (original.request?.cash || 0);
  const after = (trade.request?.cash || 0) - (trade.offer?.cash || 0);
  const out = [];
  if (after !== before) out.push({ text: `${after > before ? "+" : "−"}${money(Math.abs(after - before))}`, tone: after > before ? "bad" : "good" });
  const name = (index) => state?.board?.[index]?.name || `#${index}`;
  const diff = (was, now) => ({ added: now.filter((i) => !was.includes(i)), removed: was.filter((i) => !now.includes(i)) });
  const give = diff(original.offer?.properties || [], trade.request?.properties || []);
  const get = diff(original.request?.properties || [], trade.offer?.properties || []);
  for (const i of give.added) out.push({ text: `+ ${name(i)} from you`, tone: "bad" });
  for (const i of give.removed) out.push({ text: `keeps ${name(i)} with you`, tone: "good" });
  for (const i of get.added) out.push({ text: `+ ${name(i)} for you`, tone: "good" });
  for (const i of get.removed) out.push({ text: `without ${name(i)}`, tone: "bad" });
  return out;
}

// The computer's last word in the log ("Alpha: I'd do it for $650."), for the counter view.
export function lastReply(state, playerId) {
  const name = (state?.players || []).find((p) => p.id === playerId)?.name;
  if (!name) return "";
  const log = state?.log || [];
  for (let i = log.length - 1; i >= 0 && i >= log.length - 6; i--) {
    const text = log[i]?.text || "";
    if (text.startsWith(`${name}: `)) return text.slice(name.length + 2);
  }
  return "";
}
