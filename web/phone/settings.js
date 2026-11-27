// Per-device preferences beyond sound (audio.js): vibration, animation speed, the follow camera and
// reduced motion. Pure, saved in localStorage under estate.prefs (reads and writes never throw).

export const PREFS_KEY = "estate.prefs";
export const DEFAULT_PREFS = Object.freeze({ vibration: true, speed: "normal", follow: true, motion: "auto" });
export const SPEEDS = ["normal", "fast"];
export const MOTION = ["auto", "reduce", "full"];
export const COMPUTER_SPEEDS = ["slow", "normal", "fast"];

export function normalisePrefs(raw) {
  const value = raw && typeof raw === "object" ? raw : {};
  return {
    vibration: typeof value.vibration === "boolean" ? value.vibration : DEFAULT_PREFS.vibration,
    speed: SPEEDS.includes(value.speed) ? value.speed : DEFAULT_PREFS.speed,
    follow: typeof value.follow === "boolean" ? value.follow : DEFAULT_PREFS.follow,
    motion: MOTION.includes(value.motion) ? value.motion : DEFAULT_PREFS.motion,
  };
}

export function loadPrefs(store) {
  try {
    return normalisePrefs(store?.get(PREFS_KEY));
  } catch {
    return { ...DEFAULT_PREFS };
  }
}

export function savePrefs(store, prefs) {
  const clean = normalisePrefs(prefs);
  try { store?.set(PREFS_KEY, clean); } catch { /* private mode: lasts this visit */ }
  return clean;
}

// Board animations at this pace (0.55 = "fast"). Card reading time is never shortened by it.
export function paceOf(prefs) {
  return prefs?.speed === "fast" ? 0.55 : 1;
}

// Reduced motion: the system setting unless overridden ("reduce" = always, "full" = never).
export function reducedMotion(prefs, systemReduced) {
  if (prefs?.motion === "reduce") return true;
  if (prefs?.motion === "full") return false;
  return Boolean(systemReduced);
}

// Net worth as the server counts it for END GAME (docs/API.md "Ending a game"): cash, deeds at
// price (half if mortgaged), buildings at cost, plus loans owed to them, minus loans they owe.
export function worthWithLoans(state, playerId, netWorth) {
  let total = netWorth(state, playerId);
  for (const loan of state?.loans || []) {
    if (loan.status !== "active") continue;
    if (loan.lenderId === playerId) total += loan.repay;
    if (loan.borrowerId === playerId) total -= loan.repay;
  }
  return total;
}

// Who would win if the game ended now: { player, worth } (the richest player still in, as the
// server counts it: loans, and a pending debt settled as if paid; insights.finalScore).
export function leaderNow(state, netWorth, score = null) {
  let best = null;
  for (const player of state?.players || []) {
    if (player.bankrupt) continue;
    const worth = score ? score(state, player.id) : worthWithLoans(state, player.id, netWorth);
    if (!best || worth > best.worth) best = { player, worth };
  }
  return best;
}
