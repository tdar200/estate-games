// Sound, music and card-reading time for the phone. Pure: which effect an event or cue makes on THIS
// phone (yours at full volume, other players' quieter or silent, so a room of phones doesn't all
// play the same sound), how long a drawn card stays readable, which music mood fits the game, and
// the saved audio settings. phone.js and playback.js play them through the shared engine
// (web/shared/sound.js `play(name, opts)`, web/shared/music.js), with fallbacks for older builds.

import * as format from "../shared/format.js";

export const AUDIO_KEY = "estate.audio";
// Music is off by default on phones (the PC usually plays it); a remote player can turn it on.
export const DEFAULT_AUDIO = Object.freeze({ music: false, effects: true, volume: 0.8 });

export function normaliseAudio(raw) {
  const value = raw && typeof raw === "object" ? raw : {};
  const volume = Number(value.volume);
  return {
    music: typeof value.music === "boolean" ? value.music : DEFAULT_AUDIO.music,
    effects: typeof value.effects === "boolean" ? value.effects : DEFAULT_AUDIO.effects,
    volume: Number.isFinite(volume) ? Math.max(0, Math.min(1, Math.round(volume * 100) / 100)) : DEFAULT_AUDIO.volume,
  };
}

export function loadAudio(store) {
  try {
    return normaliseAudio(store?.get(AUDIO_KEY));
  } catch {
    return { ...DEFAULT_AUDIO };
  }
}

export function saveAudio(store, settings) {
  const clean = normaliseAudio(settings);
  try { store?.set(AUDIO_KEY, clean); } catch { /* private mode: settings last this visit */ }
  return clean;
}

// How long a drawn card stays on the board: readable for max(3.5 s, 2 s + 55 ms per character),
// at most 9 s.
export function cardReadMs(text) {
  if (typeof format.cardReadMs === "function") return format.cardReadMs(text);
  const length = String(text || "").length;
  return Math.min(9000, Math.max(3500, 2000 + 55 * length));
}

// A card that needs confirming stays on the stage until it is confirmed, so it needs no hold.
export function cardHoldMs(event, next) {
  if (next?.phase === "card_confirmation" && next.pendingCard) return 0;
  return cardReadMs(event?.text);
}

const QUIET = 0.3;

// Who an animation event is about, from this phone: "me" or "other".
export function eventVoice(event, viewerId) {
  if (!viewerId) return "other";
  if (event.type === "pay") return event.from === viewerId || event.to === viewerId ? "me" : "other";
  if (event.type === "bankrupt") return event.playerId === viewerId || event.creditorId === viewerId ? "me" : "other";
  return event.playerId === viewerId ? "me" : "other";
}

// The effect for one animation event: { name, opts, gain } (gain 1 = your own, < 1 = someone
// else's, quieter), or null for silence. `step` marks each hop of a move (last = landing).
export function sfxForEvent(event, viewerId, { step = null } = {}) {
  const mine = eventVoice(event, viewerId) === "me";
  switch (event.type) {
    case "dice": return { name: "dice", opts: {}, gain: mine ? 1 : QUIET };
    case "move":
      if (!mine) return null;
      return step?.last ? { name: "land", opts: {}, gain: 1 } : { name: "step", opts: {}, gain: 0.7 };
    case "pay":
      if (!mine) return null;
      if (event.reason === "go") return { name: "go", opts: { amount: event.amount }, gain: 1 };
      return { name: "coins", opts: { amount: event.amount, out: event.from === viewerId }, gain: 1 };
    case "card": return { name: "card", opts: {}, gain: mine ? 1 : QUIET };
    case "buy": return { name: "buy", opts: { amount: event.price }, gain: mine ? 1 : QUIET };
    case "build": return mine ? { name: event.houses === 5 ? "hotel" : "build", opts: {}, gain: 1 } : null;
    case "jail": return { name: "jail", opts: {}, gain: mine ? 1 : QUIET };
    case "bankrupt": return { name: "bankrupt", opts: {}, gain: mine ? 1 : QUIET };
    case "win": return { name: "win", opts: {}, gain: event.playerId === viewerId ? 1 : 0.5 };
    default: return null;
  }
}

// The effect for an outcome cue (cues.js) that animations don't cover: offers to you, deals you
// are in, loans, an auction you won.
export function sfxForCue(cue, viewerId) {
  if (!viewerId) return null;
  const mine = cue.playerId === viewerId || cue.otherId === viewerId;
  if (!mine) return null;
  switch (cue.kind) {
    case "trade":
      if (cue.result === "accepted") return { name: "deal", opts: {}, gain: 1 };
      if (cue.result === "declined" || cue.result === "cancelled") return cue.playerId === viewerId ? null : { name: "declined", opts: {}, gain: 1 };
      return cue.otherId === viewerId ? { name: "offer", opts: {}, gain: 1 } : null;
    case "loan": return { name: "loan", opts: { amount: Math.abs(cue.amount || 0) }, gain: 1 };
    case "auction": return cue.playerId === viewerId ? { name: "gavel", opts: {}, gain: 1 } : null;
    default: return null;
  }
}

// Your own taps on money buttons the board doesn't animate.
export function sfxForAction(action) {
  switch (action?.type) {
    case "mortgage":
    case "unmortgage": return { name: "mortgage", opts: {}, gain: 1 };
    case "sell_building": return { name: "coins", opts: {}, gain: 0.8 };
    case "bid": return { name: "bid", opts: { amount: action.amount }, gain: 1 };
    default: return null;
  }
}

// Music follows the game: lobby, play, tension (an auction, someone in debt, a big rent just hit),
// victory. null when there is no game.
export function moodFor(state) {
  if (!state) return null;
  if (state.status === "lobby") return "lobby";
  if (state.status === "finished") return "victory";
  if (state.phase === "auction" || state.phase === "debt") return "tension";
  if (state.lastLanding?.kind === "rent" && state.lastLanding.amount >= 500 && state.phase === "turn_complete") return "tension";
  return "play";
}

// What older sound.js builds (no play(name)) can do instead.
export const FALLBACK_SOUNDS = {
  dice: "dice", step: "tap", land: "tap", go: "coins", coins: "coins", buy: "stamp", gavel: "stamp", bid: "tap", build: "pop", hotel: "pop",
  mortgage: "click", card: "snap", jail: "thud", offer: "pop", deal: "stamp", declined: "click", loan: "coins", bankrupt: "thud", win: "fanfare",
  yourTurn: "pop", tap: "tap",
};
