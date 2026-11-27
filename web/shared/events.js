// Derives animation events from two consecutive states, in the same shapes the server sends as
// WebSocket `event` messages (API.md §2): dice, move, pay, card, buy, build, auction, jail,
// bankrupt, win. Used when the WebSocket is unavailable (polling) or sends no events. Pure.

import { parseLog } from "./format.js";

export function newLogEntries(prev, next) {
  const seen = new Set((prev?.log || []).map((entry) => entry.id));
  return (next?.log || []).filter((entry) => !seen.has(entry.id));
}

export function diffEvents(prev, next) {
  if (!prev || !next || prev.code !== next.code) return [];
  const events = [];
  const entries = newLogEntries(prev, next);
  const seenMoves = new Set((prev.turn?.movements || []).map((movement) => movement.id));
  const moves = (next.turn?.movements || []).filter((movement) => !seenMoves.has(movement.id));
  const pendingGo = new Map();
  const landedType = new Map();

  const takeMove = (playerId) => {
    const index = moves.findIndex((movement) => movement.playerId === playerId);
    return index >= 0 ? moves.splice(index, 1)[0] : null;
  };
  const flushGo = (playerId) => {
    if (pendingGo.get(playerId)) {
      events.push({ type: "pay", from: null, to: playerId, amount: 200, reason: "go" });
      pendingGo.delete(playerId);
    }
  };

  for (const entry of entries) {
    const item = parseLog(entry, next);
    const playerId = item.playerId;
    switch (item.kind) {
      case "roll": {
        const match = entry.text.match(/rolled (\d) \+ (\d) = /);
        if (match) events.push({ type: "dice", playerId, dice: [Number(match[1]), Number(match[2])] });
        break;
      }
      case "order": {
        const match = entry.text.match(/rolled (\d) \+ (\d) = \d+ for turn order$/);
        if (match) events.push({ type: "dice", playerId, dice: [Number(match[1]), Number(match[2])], order: true });
        break;
      }
      case "go":
        pendingGo.set(playerId, true);
        break;
      case "land": {
        const movement = takeMove(playerId);
        if (movement) events.push({ type: "move", playerId, path: movement.path });
        flushGo(playerId);
        if (item.spaceIndex !== null) landedType.set(playerId, next.board?.[item.spaceIndex]?.type);
        break;
      }
      case "jail":
        if (/was sent to jail$/.test(entry.text)) {
          const movement = takeMove(playerId);
          if (movement) events.push({ type: "move", playerId, path: movement.path, jail: true });
          events.push({ type: "jail", playerId });
        }
        break;
      case "card": {
        const text = entry.text.replace(/^.*? drew: /, "");
        const deck = landedType.get(playerId) === "chest" ? "chest" : landedType.get(playerId) === "chance" ? "chance" : next.lastCard?.text === text ? next.lastCard.deck : "chance";
        events.push({ type: "card", playerId, deck, text });
        break;
      }
      case "rent":
        events.push({ type: "pay", from: playerId, to: item.otherId, amount: item.amount, reason: "rent", spaceIndex: item.spaceIndex });
        break;
      case "pay":
        events.push({ type: "pay", from: playerId, to: null, amount: Math.abs(item.amount), reason: "fee" });
        break;
      case "buy":
        if (item.spaceIndex !== null) events.push({ type: "buy", playerId, spaceIndex: item.spaceIndex, price: Math.abs(item.amount) });
        break;
      case "build":
        if (item.spaceIndex !== null) {
          events.push({
            type: "build",
            playerId,
            spaceIndex: item.spaceIndex,
            houses: next.properties?.[item.spaceIndex]?.houses ?? 0,
            from: prev.properties?.[item.spaceIndex]?.houses ?? 0,
          });
        }
        break;
      case "bid":
        events.push({ type: "auction", spaceIndex: next.auction?.spaceIndex ?? prev.auction?.spaceIndex ?? null, bid: item.bid, bidderId: playerId });
        break;
      case "bankrupt":
        events.push({ type: "bankrupt", playerId, creditorId: next.lastBankruptcy?.creditorId ?? null });
        break;
      case "win":
        events.push({ type: "win", playerId });
        break;
      default:
        break;
    }
  }
  for (const movement of moves) events.push({ type: "move", playerId: movement.playerId, path: movement.path });
  for (const playerId of pendingGo.keys()) flushGo(playerId);
  if (next.status === "finished" && prev.status !== "finished" && !events.some((event) => event.type === "win")) {
    events.push({ type: "win", playerId: next.winnerId });
  }
  // Merge builds on the same square into one event (several houses dropped in a row).
  const merged = [];
  for (const event of events) {
    const last = merged[merged.length - 1];
    if (event.type === "build" && last?.type === "build" && last.spaceIndex === event.spaceIndex) {
      last.houses = event.houses;
      continue;
    }
    merged.push(event);
  }
  return merged;
}
