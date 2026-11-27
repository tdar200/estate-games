// The timed open auction (docs/API.md §3c "Timed open auction", UX-03). With the house rule
// auctionClock on, state.auction has clockMs and endsAt, nobody has the turn (currentPlayerId is
// null) and anyone still in except the leader may bid or pass at any moment. Turn-order auctions
// (auctionClock 0, old games, classic rooms) have neither field and keep working as before.
//
// Pure: every function takes the server's clock ("now", Unix ms) from the caller, which is the
// socket's synced clock live (LiveRoom.clock.now()) and the fixture's `now` in the demos.

import { playerById } from "./format.js";

// "Going once" is called when this much or less is left (engine.AuctionGoingOnceMs). Presentation
// only: the server sends nothing for it.
export const GOING_ONCE_MS = 3000;

// The quick bids on every screen: +$10 (A), +$50 (X), +$100 (Y).
export const QUICK_STEPS = [10, 50, 100];

// A timed auction (an auction object, or a state in the auction phase).
export function isTimed(auctionOrState) {
  const auction = auctionOrState?.auction !== undefined ? auctionOrState.auction : auctionOrState;
  if (auctionOrState?.phase !== undefined && auctionOrState.phase !== "auction") return false;
  return Boolean(auction) && Number(auction.clockMs) > 0 && Number.isFinite(Number(auction.endsAt));
}

// Milliseconds left on the clock at server time `now`: never below 0 and never above the clock's
// length (a screen whose clock runs behind would otherwise show more than the clock holds).
export function msLeft(auction, now) {
  if (!isTimed(auction) || !Number.isFinite(now)) return 0;
  return Math.max(0, Math.min(Number(auction.clockMs), Number(auction.endsAt) - now));
}

// Whole seconds to show (6.2 s left reads 7, 0.4 s reads 1, 0 reads 0).
export function secondsLeft(ms) {
  return ms > 0 ? Math.ceil(ms / 1000) : 0;
}

// "0:06", "0:30".
export function clockText(ms) {
  const seconds = secondsLeft(ms);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

// The share of the clock still left (1 → just bid, 0 → closing).
export function clockFraction(auction, now) {
  return isTimed(auction) ? msLeft(auction, now) / Number(auction.clockMs) : 0;
}

export function goingOnce(auction, now) {
  const left = msLeft(auction, now);
  return isTimed(auction) && left > 0 && left <= GOING_ONCE_MS;
}

// May this player bid (or pass) right now? Timed: anyone still in except the leader. Turn order:
// only the player whose bid it is.
export function canBid(state, playerId) {
  const auction = state?.auction;
  if (!auction || state.phase !== "auction" || !playerId) return false;
  if (!(auction.activeIds || []).includes(playerId)) return false;
  if (isTimed(auction)) return auction.bidderId !== playerId;
  return auction.currentPlayerId === playerId;
}

// The quick bids this player can make: {step, amount} with amount = currentBid + step. A step the
// player can't afford is left out; none at all when they may not bid.
export function quickBids(state, playerId) {
  const player = playerById(state, playerId);
  if (!player || !canBid(state, playerId)) return [];
  const current = Number(state.auction.currentBid) || 0;
  return QUICK_STEPS.map((step) => ({ step, amount: current + step })).filter((bid) => bid.amount <= player.cash);
}

// Who is in this auction, in seat order: {player, bid, passed, leads}. Timed auctions list
// everyone still in and everyone who passed; turn-order ones everyone not out of the game.
export function auctionRoster(state) {
  const auction = state?.auction;
  if (!auction) return [];
  const active = auction.activeIds || [];
  const passedIds = auction.passedIds || [];
  const timed = isTimed(auction);
  return (state.players || [])
    .filter((player) => (timed ? active.includes(player.id) || passedIds.includes(player.id) : !player.bankrupt))
    .map((player) => ({
      player,
      bid: Number.isFinite(auction.bids?.[player.id]) ? auction.bids[player.id] : null,
      passed: !active.includes(player.id),
      leads: auction.bidderId === player.id,
    }));
}

// Who may still bid in a timed auction (everyone in but the leader); the bidder in turn order.
export function auctionActors(state) {
  const auction = state?.auction;
  if (!auction) return [];
  if (isTimed(auction)) return (auction.activeIds || []).filter((id) => id !== auction.bidderId);
  return [auction.currentPlayerId].filter(Boolean);
}

// Server time for a screen: the fixture's frozen `now` in a demo, the socket's synced clock live.
export function screenNow(state, { demo = false, clock = null } = {}) {
  if (demo && Number.isFinite(Number(state?.now)) && Number(state.now) > 0) return Number(state.now);
  return clock?.now ? clock.now() : Date.now();
}

// Bring a rendered clock up to date without a re-render: every [data-auc-clock] element inside
// `root` gets --left (0–1), its [data-auc-secs] text, its [data-auc-text] text and the is-going
// class. Returns true while the clock still runs (so the caller can keep its timer going).
export function tickAuctionClock(root, auction, now) {
  if (!root?.querySelectorAll || !isTimed(auction)) return false;
  const left = msLeft(auction, now);
  const going = goingOnce(auction, now);
  for (const node of root.querySelectorAll("[data-auc-clock]")) {
    node.style.setProperty("--left", clockFraction(auction, now).toFixed(3));
    node.classList.toggle("is-going", going);
    node.classList.toggle("is-closed", left <= 0);
    for (const secs of node.querySelectorAll("[data-auc-secs]")) secs.textContent = String(secondsLeft(left));
    for (const text of node.querySelectorAll("[data-auc-text]")) text.textContent = clockText(left);
  }
  return left > 0;
}
