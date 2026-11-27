// Estate's background music: an original, procedural lounge-jazz score (WebAudio, no files). Soft
// electric-piano chords, a walking bass, brushes and a little vibraphone melody that varies from
// phrase to phrase. Five moods, crossfaded on the bar:
//   lobby    light: two-feel bass, held chords, brushes, sparse melody (88 bpm, F major)
//   play     relaxed groove: walking bass, Charleston comping, ride and brushes (102 bpm, B♭)
//   tension  tighter and minor: straight eighths, bass ostinato, offbeat stabs, hats (124 bpm, C minor)
//   victory  a short brass sting, then a happy loop (116 bpm, C major)
//   thinking the quiz's final: slow and minor, held chords, a soft clock-like tick (72 bpm, D minor)
//
//   start(mood)     begin (or keep) playing; it waits for the first user gesture (sound.unlockAudio)
//   setMood(mood)   switch at the next bar with a crossfade
//   stop()          fade out and stop scheduling
//   setEnabled(on)  music on/off (saved with the sound settings); isEnabled()
//   setVolume(v)    music volume 0–1 (saved)
//   moodFor(state)  which mood a game state wants (pure)
//
// Cheap by design: a 150 ms timer schedules the next half second of notes on the AudioContext
// clock; nothing runs per frame. Hidden tabs pause.

import { audioContext, getSettings, musicOutput, onSettingsChange, onUnlock, reverbInput, setMusicEnabled, setMusicVolume } from "./sound.js";
import { quizMood } from "./quiz-view.js";
import { closestMood } from "./closest-view.js";

export const MOODS = Object.freeze(["lobby", "play", "tension", "victory", "thinking"]);

// Music follows the game: lobby, play, tension (an auction, someone in debt, a big rent just
// hit), victory. null when there is no game.
export function moodFor(state) {
  if (!state) return null;
  if (state.game === "quiz") return quizMood(state); // "thinking" through the final (quiz-view.js)
  if (state.game === "closest") return closestMood(state); // lobby, play, victory (closest-view.js)
  if (state.status === "lobby") return "lobby";
  if (state.status === "finished") return "victory";
  if (state.phase === "auction" || state.phase === "debt") return "tension";
  if (state.lastLanding?.kind === "rent" && state.lastLanding.amount >= 500 && state.phase === "turn_complete") return "tension";
  return "play";
}

// ---------------------------------------------------------------------------------------------
// The score

const QUALITY = {
  maj7: [0, 4, 7, 11], maj9: [0, 4, 7, 11, 14], six: [0, 4, 7, 9], m7: [0, 3, 7, 10], m9: [0, 3, 7, 10, 14],
  dom7: [0, 4, 7, 10], dom9: [0, 4, 7, 10, 14], m7b5: [0, 3, 6, 10], dom7b9: [0, 4, 7, 10, 13], m6: [0, 3, 7, 9],
};
// Chord = [root (MIDI, around octave 3), quality].
const STYLES = {
  lobby: {
    bpm: 88, swing: 0.62, comp: "pad", bass: "two", drums: "brush", melody: 0.35, level: 0.8,
    chords: [[53, "maj9"], [50, "m7"], [55, "m9"], [48, "dom9"], [57, "m7"], [50, "dom7"], [55, "m7"], [48, "dom9"]],
    scale: [0, 2, 4, 7, 9], key: 53,
  },
  play: {
    bpm: 102, swing: 0.64, comp: "charleston", bass: "walk", drums: "ride", melody: 0.5, level: 0.85,
    chords: [[58, "maj7"], [55, "dom7b9"], [48, "m9"], [53, "dom9"], [50, "m7"], [55, "dom7"], [48, "m7"], [53, "dom7"]],
    scale: [0, 2, 4, 7, 9], key: 58,
  },
  tension: {
    bpm: 124, swing: 0.5, comp: "stabs", bass: "ostinato", drums: "hats", melody: 0.25, level: 0.8,
    chords: [[48, "m9"], [56, "dom7"], [50, "m7b5"], [55, "dom7b9"]],
    scale: [0, 3, 5, 7, 10], key: 48,
  },
  victory: {
    bpm: 116, swing: 0.58, comp: "charleston", bass: "walk", drums: "ride", melody: 0.6, level: 0.9, sting: 2,
    chords: [[48, "maj9"], [57, "m7"], [50, "m9"], [55, "dom9"]],
    scale: [0, 2, 4, 7, 9], key: 48,
  },
  // The quiz's final (bets, the clue, the answers): slow, minor and suspended, a clock ticking.
  thinking: {
    bpm: 72, swing: 0.5, comp: "pad", bass: "two", drums: "clock", melody: 0.2, level: 0.7,
    chords: [[50, "m9"], [55, "m7"], [58, "maj7"], [57, "dom7b9"]],
    scale: [0, 3, 5, 7, 10], key: 50,
  },
};

const NOTE = (midi) => 440 * 2 ** ((midi - 69) / 12);

function rng(seed) {
  let value = seed >>> 0;
  return () => {
    value = (value + 0x6d2b79f5) >>> 0;
    let t = value;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// A rootless voicing around middle C: the chord's colour tones, kept in 55–72.
function voicing([root, quality]) {
  const tones = QUALITY[quality].slice(1).map((interval) => {
    let pitch = root + interval;
    while (pitch < 55) pitch += 12;
    while (pitch > 72) pitch -= 12;
    return pitch;
  });
  return [...new Set(tones)].sort((a, b) => a - b);
}

function inRange(pitch, low, high) {
  let value = pitch;
  while (value < low) value += 12;
  while (value > high) value -= 12;
  return value;
}

// ---------------------------------------------------------------------------------------------
// Instruments (each note: a couple of oscillators into the mood's bus)

let noiseBuffer = null;
let scheduled = 0;

function envelope(ctx, gain, t0, attack, peak, end) {
  gain.gain.setValueAtTime(0.0001, t0);
  gain.gain.exponentialRampToValueAtTime(Math.max(0.0002, peak), t0 + attack);
  gain.gain.exponentialRampToValueAtTime(0.0001, end);
}

function osc(ctx, out, { type = "sine", freq, t0, attack = 0.01, peak = 0.1, end, detune = 0, lowpass = 0 }) {
  const node = ctx.createOscillator();
  node.type = type;
  node.frequency.value = freq;
  node.detune.value = detune;
  const amp = ctx.createGain();
  envelope(ctx, amp, t0, attack, peak, end);
  let head = node;
  if (lowpass) {
    const filter = ctx.createBiquadFilter();
    filter.type = "lowpass";
    filter.frequency.value = lowpass;
    head = node.connect(filter);
  }
  head.connect(amp).connect(out);
  node.start(t0);
  node.stop(end + 0.05);
  scheduled += 1;
}

function hiss(ctx, out, { t0, dur, peak, filter, type = "bandpass", q = 0.8, attack = 0.004 }) {
  if (!noiseBuffer || noiseBuffer.sampleRate !== ctx.sampleRate) {
    noiseBuffer = ctx.createBuffer(1, ctx.sampleRate, ctx.sampleRate);
    const data = noiseBuffer.getChannelData(0);
    for (let index = 0; index < data.length; index += 1) data[index] = Math.random() * 2 - 1;
  }
  const source = ctx.createBufferSource();
  source.buffer = noiseBuffer;
  source.loop = true;
  const shape = ctx.createBiquadFilter();
  shape.type = type;
  shape.frequency.value = filter;
  shape.Q.value = q;
  const amp = ctx.createGain();
  envelope(ctx, amp, t0, attack, peak, t0 + dur);
  source.connect(shape).connect(amp).connect(out);
  source.start(t0, Math.random() * 0.5);
  source.stop(t0 + dur + 0.05);
  scheduled += 1;
}

const PLAY = {
  ep(ctx, out, t0, midi, dur, vel) {
    const f = NOTE(midi);
    osc(ctx, out, { type: "sine", freq: f, t0, attack: 0.012, peak: 0.055 * vel, end: t0 + dur + 0.5 });
    osc(ctx, out, { type: "triangle", freq: f * 2, t0, attack: 0.006, peak: 0.012 * vel, end: t0 + Math.min(0.5, dur + 0.15), lowpass: 2400 });
  },
  bass(ctx, out, t0, midi, dur, vel) {
    const f = NOTE(midi);
    osc(ctx, out, { type: "triangle", freq: f, t0, attack: 0.012, peak: 0.16 * vel, end: t0 + dur * 0.92, lowpass: 650 });
    osc(ctx, out, { type: "sine", freq: f, t0, attack: 0.01, peak: 0.1 * vel, end: t0 + dur * 0.92 });
  },
  vibes(ctx, out, t0, midi, dur, vel) {
    const f = NOTE(midi);
    osc(ctx, out, { type: "sine", freq: f, t0, attack: 0.004, peak: 0.05 * vel, end: t0 + Math.max(0.5, dur) + 0.6 });
    osc(ctx, out, { type: "sine", freq: f * 4, t0, attack: 0.002, peak: 0.008 * vel, end: t0 + 0.25 });
    osc(ctx, out, { type: "sine", freq: f, t0, attack: 0.02, peak: 0.015 * vel, end: t0 + Math.max(0.5, dur) + 0.4, detune: 7 });
  },
  brass(ctx, out, t0, midi, dur, vel) {
    const f = NOTE(midi);
    osc(ctx, out, { type: "sawtooth", freq: f, t0, attack: 0.035, peak: 0.045 * vel, end: t0 + dur, lowpass: 1900 });
    osc(ctx, out, { type: "sawtooth", freq: f, t0, attack: 0.04, peak: 0.03 * vel, end: t0 + dur, lowpass: 1500, detune: 8 });
  },
  brush(ctx, out, t0, dur, vel) { hiss(ctx, out, { t0, dur, peak: 0.03 * vel, filter: 4200, q: 0.6, attack: 0.03 }); },
  ride(ctx, out, t0, vel) { hiss(ctx, out, { t0, dur: 0.12, peak: 0.018 * vel, filter: 7200, type: "highpass" }); },
  hat(ctx, out, t0, vel) { hiss(ctx, out, { t0, dur: 0.035, peak: 0.022 * vel, filter: 8200, type: "highpass" }); },
  rim(ctx, out, t0, vel) {
    hiss(ctx, out, { t0, dur: 0.03, peak: 0.04 * vel, filter: 1900, q: 4 });
    osc(ctx, out, { type: "sine", freq: 820, t0, attack: 0.002, peak: 0.02 * vel, end: t0 + 0.03 });
  },
  kick(ctx, out, t0, vel) {
    const node = ctx.createOscillator();
    const amp = ctx.createGain();
    node.frequency.setValueAtTime(95, t0);
    node.frequency.exponentialRampToValueAtTime(44, t0 + 0.16);
    envelope(ctx, amp, t0, 0.004, 0.12 * vel, t0 + 0.2);
    node.connect(amp).connect(out);
    node.start(t0);
    node.stop(t0 + 0.25);
    scheduled += 1;
  },
};

// ---------------------------------------------------------------------------------------------
// One bar of one mood

function melodyPhrase(random) {
  // A motif: up to five notes on eighth positions, each a height (0–1) and a length in eighths.
  const notes = [];
  let pos = Math.floor(random() * 2);
  while (pos < 8 && notes.length < 5) {
    notes.push({ pos, height: 0.25 + random() * 0.6, len: 1 + Math.floor(random() * 3) });
    pos += 1 + Math.floor(random() * 3);
  }
  return notes;
}

function vary(motif, random) {
  return motif
    .filter(() => random() > 0.12)
    .map((note) => (random() < 0.35 ? { ...note, height: Math.max(0.1, Math.min(0.95, note.height + (random() - 0.5) * 0.4)) } : note));
}

function melodyPitch(style, chord, height) {
  const low = 67;
  const high = 84;
  const target = low + height * (high - low);
  const pool = new Set();
  for (const interval of QUALITY[chord[1]]) for (let octave = -12; octave <= 24; octave += 12) pool.add(chord[0] + interval + octave);
  for (const step of style.scale) for (let octave = -12; octave <= 36; octave += 12) pool.add(style.key + step + octave);
  let best = null;
  for (const pitch of pool) {
    if (pitch < low || pitch > high) continue;
    if (best === null || Math.abs(pitch - target) < Math.abs(best - target)) best = pitch;
  }
  return best ?? 72;
}

function scheduleBar(ctx, voice, t0) {
  const style = STYLES[voice.mood];
  const beat = 60 / style.bpm;
  const bar = voice.bar;
  const random = voice.random;
  const eighth = (index) => t0 + Math.floor(index / 2) * beat + (index % 2 ? style.swing * beat : 0);
  const out = voice.bus;

  // Victory opens with a brass sting before the loop.
  if (style.sting && bar < style.sting) {
    if (bar === 0) {
      [[0, [60, 64, 67, 72]], [3, [60, 65, 69, 72]], [6, [62, 67, 71, 74]]].forEach(([at, chord]) => chord.forEach((midi) => PLAY.brass(ctx, out, eighth(at), midi, beat * 0.9, 1)));
      PLAY.kick(ctx, out, t0, 1);
      PLAY.bass(ctx, out, t0, 36, beat * 1.5, 1);
      PLAY.bass(ctx, out, eighth(6), 43, beat, 1);
    } else {
      [60, 64, 67, 71, 74].forEach((midi) => PLAY.brass(ctx, out, t0, midi, beat * 3.2, 0.9));
      PLAY.vibes(ctx, out, t0, 84, beat * 2, 1);
      PLAY.bass(ctx, out, t0, 36, beat * 3.5, 1);
      PLAY.brush(ctx, out, t0, beat * 2, 1);
    }
    return;
  }

  const index = (bar - (style.sting || 0)) % style.chords.length;
  const chord = style.chords[index];
  const next = style.chords[(index + 1) % style.chords.length];
  const voices = voicing(chord);

  // Comping
  if (style.comp === "pad") {
    voices.forEach((midi) => PLAY.ep(ctx, out, t0, midi, beat * 2.4, 0.9));
    if (random() < 0.55) voices.forEach((midi) => PLAY.ep(ctx, out, eighth(5), midi, beat * 1.2, 0.55));
  } else if (style.comp === "charleston") {
    voices.forEach((midi) => PLAY.ep(ctx, out, t0, midi, beat * 0.9, 0.85));
    voices.forEach((midi) => PLAY.ep(ctx, out, eighth(3), midi, beat * 1.4, 0.7));
    if (random() < 0.3) voices.forEach((midi) => PLAY.ep(ctx, out, eighth(7), midi, beat * 0.6, 0.45));
  } else {
    [0, 3, 7].forEach((at, hit) => voices.forEach((midi) => PLAY.ep(ctx, out, eighth(at), midi, beat * (hit ? 0.28 : 0.5), hit ? 0.7 : 0.9)));
  }

  // Bass
  const root = inRange(chord[0], 36, 47);
  const tones = QUALITY[chord[1]];
  if (style.bass === "two") {
    PLAY.bass(ctx, out, t0, root, beat * 2.6, 0.9);
    PLAY.bass(ctx, out, t0 + beat * 3, inRange(root + 7, 36, 50), beat * 0.95, 0.75);
  } else if (style.bass === "walk") {
    const third = root + tones[1];
    const fifth = root + tones[2];
    const target = inRange(next[0], 36, 47);
    const approach = random() < 0.5 ? target - 1 : target + 1;
    const line = [root, random() < 0.5 ? third : fifth, random() < 0.5 ? fifth : root + 12 - (random() < 0.5 ? 0 : 2), approach];
    line.forEach((midi, at) => PLAY.bass(ctx, out, t0 + at * beat, inRange(midi, 34, 52), beat * 0.95, at === 0 ? 0.95 : 0.8));
  } else {
    [0, 0, 7, 0, 0, 0, 10, 7].forEach((step, at) => PLAY.bass(ctx, out, t0 + at * beat * 0.5, inRange(root + step, 33, 50), beat * 0.42, at % 2 ? 0.65 : 0.85));
  }

  // Drums
  if (style.drums === "brush") {
    PLAY.brush(ctx, out, t0 + beat, beat * 0.8, 0.8);
    PLAY.brush(ctx, out, t0 + beat * 3, beat * 0.8, 0.8);
  } else if (style.drums === "clock") {
    // Tick, tock: a quiet rim on every beat, the off-beats softer.
    for (let at = 0; at < 4; at += 1) PLAY.rim(ctx, out, t0 + at * beat, at % 2 ? 0.32 : 0.48);
  } else if (style.drums === "ride") {
    PLAY.kick(ctx, out, t0, 0.55);
    PLAY.kick(ctx, out, t0 + beat * 2, 0.45);
    PLAY.brush(ctx, out, t0 + beat, beat * 0.5, 0.7);
    PLAY.brush(ctx, out, t0 + beat * 3, beat * 0.5, 0.7);
    [0, 2, 3, 4, 6, 7].forEach((at) => PLAY.ride(ctx, out, eighth(at), at % 2 ? 0.6 : 0.9));
  } else {
    PLAY.kick(ctx, out, t0, 0.8);
    PLAY.kick(ctx, out, t0 + beat * 2, 0.7);
    PLAY.rim(ctx, out, t0 + beat, 0.8);
    PLAY.rim(ctx, out, t0 + beat * 3, 0.8);
    for (let at = 0; at < 8; at += 1) PLAY.hat(ctx, out, t0 + at * beat * 0.5, at % 2 ? 0.55 : 0.85);
  }

  // Melody: a motif per four-bar phrase, varied when it comes back; the last bar breathes.
  const phrase = Math.floor(bar / 4);
  if (bar % 4 === 0) {
    voice.motif = phrase % 2 === 0 || !voice.motif ? melodyPhrase(random) : vary(voice.motif, random);
  }
  if (bar % 4 !== 3 && voice.motif && random() < style.melody + 0.3) {
    for (const note of voice.motif) {
      if (random() > style.melody + 0.35) continue;
      PLAY.vibes(ctx, out, eighth(note.pos), melodyPitch(style, chord, note.height), beat * 0.5 * note.len, 0.9);
    }
  }
}

// ---------------------------------------------------------------------------------------------
// Scheduling

const LOOKAHEAD = 0.5;
const TICK_MS = 150;
let started = false;
let wanted = "lobby";
let voice = null; // { mood, bus, bar, nextBar, random, motif }
// Each page load plays its own variations (review F6: every session started on the same phrase).
const SESSION_SEED = Math.floor(Math.random() * 0x7fffffff);
// Where each mood left off, so the game's "play" music carries on after an auction or a debt
// instead of starting again from bar 0: { mood: { bar, random, motif } }.
const resumeAt = {};
let timer = null;
let hidden = Boolean(globalThis.document?.hidden);

function makeVoice(ctx, mood, at, fade) {
  const style = STYLES[mood];
  const bus = ctx.createGain();
  bus.gain.setValueAtTime(0.0001, at);
  bus.gain.exponentialRampToValueAtTime(style.level, at + fade);
  bus.connect(musicOutput());
  const send = ctx.createGain();
  send.gain.value = 0.22;
  const room = reverbInput();
  if (room) bus.connect(send).connect(room);
  const saved = mood === "victory" ? null : resumeAt[mood];
  delete resumeAt[mood];
  // Pick up at the next four-bar phrase after where this mood stopped.
  if (saved) return { mood, bus, bar: Math.ceil(saved.bar / 4) * 4, nextBar: at, random: saved.random, motif: saved.motif };
  return { mood, bus, bar: 0, nextBar: at, random: rng(MOODS.indexOf(mood) * 7919 + 17 + SESSION_SEED), motif: null };
}

function remember(old) {
  if (old && old.mood !== "victory") resumeAt[old.mood] = { bar: old.bar, random: old.random, motif: old.motif };
}

function fadeOut(ctx, old, seconds) {
  if (!old) return;
  const now = ctx.currentTime;
  old.bus.gain.cancelScheduledValues(now);
  old.bus.gain.setValueAtTime(Math.max(0.0002, old.bus.gain.value), now);
  old.bus.gain.exponentialRampToValueAtTime(0.0001, now + seconds);
  setTimeout(() => {
    try { old.bus.disconnect(); } catch { /* already gone */ }
  }, (seconds + LOOKAHEAD + 0.5) * 1000);
}

function tick() {
  const ctx = audioContext();
  if (!ctx || !started || hidden || !isEnabled()) return;
  const now = ctx.currentTime;
  if (!voice) voice = makeVoice(ctx, wanted, now + 0.05, 1.6);
  // Back from a stall (suspended context, slow tab): pick up from now.
  if (voice.nextBar < now - 0.1) voice.nextBar = now + 0.05;
  while (voice.nextBar < now + LOOKAHEAD) {
    // A new mood starts on the bar line and crossfades.
    if (wanted !== voice.mood) {
      const at = voice.nextBar;
      const old = voice;
      remember(old);
      voice = makeVoice(ctx, wanted, at, wanted === "victory" ? 0.25 : 1.2);
      fadeOut(ctx, old, wanted === "victory" ? 0.6 : 1.4);
    }
    const style = STYLES[voice.mood];
    try {
      scheduleBar(ctx, voice, voice.nextBar);
    } catch {
      // A failed note must never stop the score.
    }
    voice.nextBar += (60 / style.bpm) * 4;
    voice.bar += 1;
  }
}

function sync() {
  const run = started && !hidden && isEnabled();
  if (run && !timer) {
    timer = setInterval(tick, TICK_MS);
    tick();
  } else if (!run && timer) {
    clearInterval(timer);
    timer = null;
    const ctx = audioContext();
    if (ctx && voice) fadeOut(ctx, voice, 0.4);
    remember(voice);
    voice = null;
  }
}

export function start(mood = wanted) {
  if (MOODS.includes(mood)) wanted = mood;
  started = true;
  sync();
}

export function setMood(mood) {
  if (!MOODS.includes(mood)) return;
  wanted = mood;
}

export function stop() {
  started = false;
  sync();
}

export function setEnabled(on) {
  setMusicEnabled(Boolean(on));
  sync();
}

export function isEnabled() {
  return getSettings().musicOn;
}

export function setVolume(value) {
  setMusicVolume(value);
}

// For checks: what is playing and how many notes have been scheduled so far.
export function debug() {
  return { started, playing: Boolean(timer), mood: voice?.mood || null, wanted, bar: voice?.bar ?? 0, scheduled };
}

onUnlock(() => sync());
onSettingsChange(() => sync());
globalThis.document?.addEventListener?.("visibilitychange", () => {
  hidden = Boolean(globalThis.document.hidden);
  sync();
});
