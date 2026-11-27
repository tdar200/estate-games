// Poker helpers for the table screen and the phone. Plain ES module, no dependencies; it sits next to
// Estate's web/shared/client.js and uses its EstateClient for transport (see docs/INTEGRATION.md §6).
// State and action shapes: docs/API.md.

const RANKS = { "2": 2, "3": 3, "4": 4, "5": 5, "6": 6, "7": 7, "8": 8, "9": 9, T: 10, J: 11, Q: 12, K: 13, A: 14 };
const RANK_LABEL = { T: "10" };
const SUITS = {
  c: { name: "clubs", symbol: "♣", red: false, four: "green" },
  d: { name: "diamonds", symbol: "♦", red: true, four: "blue" },
  h: { name: "hearts", symbol: "♥", red: true, four: "red" },
  s: { name: "spades", symbol: "♠", red: false, four: "black" },
};

// "Ts" → { code: "Ts", rank: "T", value: 10, label: "10", suit: "s", suitName: "spades", symbol: "♠", red: false,
//          color: "black", text: "10♠" }
// color is "red"/"black", or with fourColor (settings.fourColorDeck or the phone's own preference)
// spades black, hearts red, diamonds blue, clubs green.
export function parseCard(code, { fourColor = false } = {}) {
  const rank = String(code || "").charAt(0).toUpperCase();
  const suit = String(code || "").charAt(1).toLowerCase();
  if (!RANKS[rank] || !SUITS[suit]) return null;
  const label = RANK_LABEL[rank] || rank;
  const s = SUITS[suit];
  const color = fourColor ? s.four : s.red ? "red" : "black";
  return { code: rank + suit, rank, value: RANKS[rank], label, suit, suitName: s.name, symbol: s.symbol, red: s.red, color, text: label + s.symbol };
}

// 1500 → "1,500"
export function formatChips(n) {
  return Number(n || 0).toLocaleString("en-GB");
}

// players[].lastAction (or an `action` event) → short badge text.
export function actionLabel(a) {
  if (!a) return "";
  const allIn = a.allIn ? " (all-in)" : "";
  switch (a.type || a.action) {
    case "fold": return "Fold";
    case "check": return "Check";
    case "call": return a.allIn ? `All-in ${formatChips(a.to)}` : `Call ${formatChips(a.amount)}`;
    case "bet": return a.allIn ? `All-in ${formatChips(a.to)}` : `Bet ${formatChips(a.to)}`;
    case "raise": return a.allIn ? `All-in ${formatChips(a.to)}` : `Raise to ${formatChips(a.to)}`;
    case "smallBlind": return `Small blind ${formatChips(a.amount)}${allIn}`;
    case "bigBlind": return `Big blind ${formatChips(a.amount)}${allIn}`;
    case "ante": return `Ante ${formatChips(a.amount)}`;
    default: return "";
  }
}

// The phone's action buttons for the current state, or [] when it is not the viewer's turn.
// Each entry: { kind: "fold"|"check"|"call"|"raise"|"allin", label, action, preset? }.
export function actionButtons(state) {
  const legal = state?.you?.toAct ? state.you.legal : null;
  if (!legal) return [];
  const buttons = [];
  if (legal.fold) buttons.push({ kind: "fold", label: "Fold", action: { type: "fold" } });
  if (legal.check) buttons.push({ kind: "check", label: "Check", action: { type: "check" } });
  else if (legal.call > 0) {
    const allIn = !legal.raise && legal.allIn === (state.players.find((p) => p.id === state.you.id)?.bet || 0) + legal.call;
    buttons.push({ kind: "call", label: `${allIn ? "All-in" : "Call"} ${formatChips(legal.call)}`, action: { type: "call" } });
  }
  if (legal.raise) {
    const verb = legal.raise.isBet ? "Bet" : "Raise to";
    for (const p of legal.raise.presets) {
      if (p.to === legal.raise.max) continue;
      buttons.push({ kind: "raise", label: `${verb} ${formatChips(p.to)}`, preset: p.label, action: { type: legal.raise.isBet ? "bet" : "raise", to: p.to } });
    }
    buttons.push({ kind: "allin", label: `All-in ${formatChips(legal.raise.max)}`, action: { type: "allin" } });
  }
  return buttons;
}

// Keep a slider value legal: whole chips within [min, max].
export function clampRaise(state, to) {
  const r = state?.you?.legal?.raise;
  if (!r) return null;
  return Math.min(r.max, Math.max(r.min, Math.round(Number(to) || 0)));
}

// Countdown helper that corrects for the difference between server and device clocks.
// Call clock.sync(state) whenever a state arrives, then clock.left(state.actionDeadline).
export function serverClock() {
  let offset = 0;
  return {
    sync(state) {
      if (state?.now) offset = state.now - Date.now();
    },
    now() {
      return Date.now() + offset;
    },
    // Seconds left until a server timestamp (0 when past or unset).
    left(deadlineMs) {
      if (!deadlineMs) return 0;
      return Math.max(0, Math.ceil((deadlineMs - (Date.now() + offset)) / 1000));
    },
  };
}

// Players arranged around the table so `viewerId` (if seated) sits at the bottom: returns
// [{ player, slot }] with slot 0 = bottom centre, then clockwise. maxSeats = settings.maxPlayers.
export function seatLayout(state, viewerId = state?.viewerId) {
  const max = state?.settings?.maxPlayers || 10;
  const me = state?.players?.find((p) => p.id === viewerId);
  const base = me ? me.seat : 0;
  return (state?.players || [])
    .filter((p) => p.status !== "left")
    .map((player) => ({ player, slot: (player.seat - base + max) % max }));
}

// Everything the table screen needs to know about the pot right now.
export function potSummary(state) {
  const bets = (state?.players || []).reduce((sum, p) => sum + (p.bet || 0), 0);
  return {
    total: state?.totalPot || 0,
    inFront: bets,
    pots: (state?.pots || []).map((p, i) => ({ ...p, label: i === 0 ? "Main pot" : `Side pot ${i}` })),
  };
}

// Action senders bound to one seat. `client` is an EstateClient; seatId is for table-screen seats.
export class PokerActions {
  constructor(client, { code, token, seatId = null }) {
    this.client = client;
    this.code = code;
    this.token = token;
    this.seatId = seatId;
  }
  send(action) {
    return this.client.act(this.code, this.token, action, this.seatId);
  }
  start() { return this.send({ type: "start" }); }
  settings(patch) { return this.send({ type: "settings", settings: patch }); }
  fold() { return this.send({ type: "fold" }); }
  check() { return this.send({ type: "check" }); }
  call() { return this.send({ type: "call" }); }
  bet(to) { return this.send({ type: "bet", to }); }
  raise(to) { return this.send({ type: "raise", to }); }
  allIn() { return this.send({ type: "allin" }); }
  show() { return this.send({ type: "show" }); }
  sitOut() { return this.send({ type: "sitOut" }); }
  sitIn() { return this.send({ type: "sitIn" }); }
  rebuy() { return this.send({ type: "rebuy" }); }
  declineRebuy() { return this.send({ type: "declineRebuy" }); }
  deal() { return this.send({ type: "deal" }); }
}

// Win chances during an all-in run-out (players[].odds) → { win: "62%", tie: "3%" | "" }.
// Percentages are rounded; anything below 1% but above 0 shows as "<1%".
export function oddsLabel(odds) {
  if (!odds) return null;
  const pct = (x) => (x <= 0 ? "0%" : x < 0.01 ? "<1%" : `${Math.round(x * 100)}%`);
  return { win: pct(odds.win), tie: odds.tie > 0 ? pct(odds.tie) : "" };
}
