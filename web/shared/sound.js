// Estate's sound: every effect is synthesised with WebAudio (no audio files). Warm and short:
// wood, felt, paper, brass and small bells, never arcade bleeps.
//
// Graph: sources → effects bus ─┐
//                 music bus ────┼→ master (mute) → limiter → speakers
//                 small room reverb (sends from both)
// The context starts on the first user gesture (browser autoplay rules): call unlockAudio() from
// a gesture or installAudioUnlock(window) once. Music (music.js) plays into musicOutput().
//
// Settings (localStorage, every access wrapped): "estate.muted" = "1" | "0" (the old mute, kept)
// and "estate.sound" = { effects: 0–1, music: 0–1, effectsOn, musicOn }.
//
// play(name, opts) — the names (SOUND_NAMES):
//   dice      dice rattle in the hand, then two settling clacks
//   hop       one step of a token's hop; opts.step (1, 2, …) pitches each step a little higher
//   land      the token sets down on its square
//   go        passing PAYDAY: a three-note chime
//   coins     paying or collecting; opts.amount scales it (a clink … a cascade), opts.collect
//             true rises, false falls
//   buy       deed stamp and a coin
//   gavel     auction gavel (opts.sold: three knocks for "sold")
//   bid       a new bid: one short tick
//   house     a house set down (opts.step pitches successive houses up)
//   hotel     a hotel set down
//   mortgage  paper shuffle
//   card      a card drawn and flipped
//   jail      the cell door: metal clang and latch
//   offer     a trade or loan offer arrives: a desk bell
//   accept    deal done: two rising notes
//   decline   deal refused: two soft low notes
//   loan      a loan paid out or repaid: coins and a warm chord
//   bankrupt  a long falling tone and a thud
//   win       the winner's fanfare
//   turn      your turn: a soft ping
//   tap       UI tap
// Poker (synthesised here too, nothing sampled):
//   dealCard  a card slid off the deck and snapped down; opts.count cards in a row
//   chips     clay chips set on the felt; opts.amount scales it, opts.allIn pushes a whole stack
//   knock     a check: two knuckle raps on the rail
//   fold      cards tossed face down: a soft swish
//   tick      the last seconds of your turn: a small wood tick (opts.last: the final one)
//   potWin    the pot pushed to the winner: a sweep of chips (opts.mine adds a warm chord)
// Quiz (the last three seconds of a clue use tick):
//   quizOpen  the four choices appear: two quick bright bells, rising
//   quizLock  an answer locked in: a soft wooden click with a small high ping
//   quizRight a right answer: three rising bells
//   quizWrong a wrong answer: two low, soft falling notes
//   quizGold  a gold square found: a shimmer of small bells over a warm chord
// Every name takes opts.volume (0–1, default 1) on top of the effects volume. Aliases (ALIASES):
// step → hop, build → house, deal → accept, declined → decline, yourTurn → turn; coins also
// takes opts.out (true = paying out, the same as collect: false).

export const SOUND_NAMES = Object.freeze([
  "dice", "hop", "land", "go", "coins", "buy", "gavel", "bid", "house", "hotel", "mortgage", "card",
  "jail", "offer", "accept", "decline", "loan", "bankrupt", "win", "turn", "tap",
  "dealCard", "chips", "knock", "fold", "tick", "potWin",
  "quizOpen", "quizLock", "quizRight", "quizWrong", "quizGold",
]);

export const ALIASES = Object.freeze({ step: "hop", build: "house", deal: "accept", declined: "decline", yourTurn: "turn" });

const MUTE_KEY = "estate.muted";
const SETTINGS_KEY = "estate.sound";
export const DEFAULT_SETTINGS = Object.freeze({ effects: 0.8, music: 0.45, effectsOn: true, musicOn: true });

function store() {
  try {
    return globalThis.localStorage || null;
  } catch {
    return null;
  }
}

function readItem(key) {
  try {
    return store()?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

function writeItem(key, value) {
  try {
    store()?.setItem(key, value);
  } catch {
    // Storage can be unavailable (private mode, blocked site data): settings still work for this page.
  }
}

const unit = (value, fallback) => (value !== null && value !== "" && Number.isFinite(Number(value)) ? Math.max(0, Math.min(1, Number(value))) : fallback);

// The saved settings (or the defaults), read fresh from storage.
export function loadSettings() {
  let saved = {};
  try {
    saved = JSON.parse(readItem(SETTINGS_KEY) || "{}") || {};
  } catch {
    saved = {};
  }
  return {
    effects: unit(saved.effects, DEFAULT_SETTINGS.effects),
    music: unit(saved.music, DEFAULT_SETTINGS.music),
    effectsOn: typeof saved.effectsOn === "boolean" ? saved.effectsOn : DEFAULT_SETTINGS.effectsOn,
    musicOn: typeof saved.musicOn === "boolean" ? saved.musicOn : DEFAULT_SETTINGS.musicOn,
    muted: readItem(MUTE_KEY) === "1",
  };
}

const settings = loadSettings();
let muted = settings.muted;
const muteListeners = new Set();
const settingsListeners = new Set();
const unlockListeners = new Set();
const audioStateListeners = new Set();

// The current settings: { effects, music, effectsOn, musicOn, muted }.
export function getSettings() {
  return { effects: settings.effects, music: settings.music, effectsOn: settings.effectsOn, musicOn: settings.musicOn, muted };
}

export function onSettingsChange(listener) {
  settingsListeners.add(listener);
  return () => settingsListeners.delete(listener);
}

function changed() {
  writeItem(SETTINGS_KEY, JSON.stringify({ effects: settings.effects, music: settings.music, effectsOn: settings.effectsOn, musicOn: settings.musicOn }));
  applyGains();
  const snapshot = getSettings();
  settingsListeners.forEach((listener) => listener(snapshot));
}

export function setEffectsVolume(value) {
  settings.effects = unit(value, settings.effects);
  changed();
}

export function setMusicVolume(value) {
  settings.music = unit(value, settings.music);
  changed();
}

export function setEffectsEnabled(value) {
  settings.effectsOn = Boolean(value);
  changed();
}

export function setMusicEnabled(value) {
  settings.musicOn = Boolean(value);
  changed();
}

export function isMuted() {
  return muted;
}

export function setMuted(value) {
  muted = Boolean(value);
  writeItem(MUTE_KEY, muted ? "1" : "0");
  applyGains();
  muteListeners.forEach((listener) => listener(muted));
  const snapshot = getSettings();
  settingsListeners.forEach((listener) => listener(snapshot));
}

export function toggleMuted() {
  setMuted(!muted);
  return muted;
}

export function onMuteChange(listener) {
  muteListeners.add(listener);
  return () => muteListeners.delete(listener);
}

// ---------------------------------------------------------------------------------------------
// Audio graph

let context = null;
let master = null;
let fxBus = null;
let musicBus = null;
let reverbIn = null;

// Loudness follows the square of the slider, which feels even to the ear.
const curve = (value) => value * value;
// The music bus at full slider (was 0.7: +12 dB).
export const MUSIC_LEVEL = 2.8;

function applyGains() {
  if (!context) return;
  const now = context.currentTime;
  const set = (node, value) => {
    node.gain.cancelScheduledValues(now);
    node.gain.setTargetAtTime(value, now, 0.04);
  };
  set(master, muted ? 0 : 0.9);
  set(fxBus, settings.effectsOn ? curve(settings.effects) : 0);
  // Music sat about 15 LU under the effects at about -42 LUFS (review F6); this bus level plus the
  // EQ in build() brings it to about -30 LUFS at the default volume (measured, 40 s per mood),
  // still a little under the effects (about -27).
  set(musicBus, settings.musicOn ? curve(settings.music) * MUSIC_LEVEL : 0);
}

function roomImpulse(ctx, seconds = 1.4) {
  const length = Math.floor(ctx.sampleRate * seconds);
  const buffer = ctx.createBuffer(2, length, ctx.sampleRate);
  for (let channel = 0; channel < 2; channel += 1) {
    const data = buffer.getChannelData(channel);
    for (let index = 0; index < length; index += 1) data[index] = (Math.random() * 2 - 1) * (1 - index / length) ** 3;
  }
  return buffer;
}

function build(AudioContextClass) {
  context = new AudioContextClass();
  master = context.createGain();
  fxBus = context.createGain();
  musicBus = context.createGain();
  master.gain.value = 0;
  fxBus.gain.value = 0;
  musicBus.gain.value = 0;
  // A gentle limiter so stacked effects and music never clip.
  const limiter = context.createDynamicsCompressor();
  limiter.threshold.value = -10;
  limiter.knee.value = 8;
  limiter.ratio.value = 4;
  limiter.attack.value = 0.004;
  limiter.release.value = 0.2;
  const reverb = context.createConvolver();
  reverb.buffer = roomImpulse(context);
  reverbIn = context.createGain();
  reverbIn.gain.value = 1;
  reverbIn.connect(reverb).connect(master);
  fxBus.connect(master);
  // Music: cut the sub-bass that TV and phone speakers can't play (it only eats headroom) and lift
  // the top so the chords and vibes come through small speakers (review F6).
  const lowCut = context.createBiquadFilter();
  lowCut.type = "highpass";
  lowCut.frequency.value = 90;
  lowCut.Q.value = 0.7;
  const air = context.createBiquadFilter();
  air.type = "highshelf";
  air.frequency.value = 2200;
  air.gain.value = 5;
  musicBus.connect(lowCut).connect(air).connect(master);
  master.connect(limiter).connect(context.destination);
  // The browser can start (autoplay allowed), suspend or resume the context on its own: tell the
  // page (the TV's "press any key for sound" pill) and start the music once it runs.
  context.onstatechange = () => {
    if (context?.state === "running") unlockListeners.forEach((listener) => listener(context));
    audioStateListeners.forEach((listener) => listener(context?.state || "none"));
  };
  applyGains();
}

function makeContext() {
  const AudioContextClass = globalThis.AudioContext || globalThis.webkitAudioContext;
  if (!AudioContextClass) return false;
  if (context) return true;
  try {
    build(AudioContextClass);
    return true;
  } catch {
    context = null;
    return false;
  }
}

// Call from any user gesture (keydown, pointerdown, gamepad press) to allow audio.
export function unlockAudio() {
  if (!makeContext()) return;
  const notify = () => unlockListeners.forEach((listener) => listener(context));
  if (context.state === "suspended") context.resume().then(notify).catch(() => {});
  else notify();
}

// The TV calls this at page load: where the browser allows sound without a gesture (the kiosk
// launch passes --autoplay-policy=no-user-gesture-required; Chrome also allows it on a site that
// has often played sound) the context runs at once. Otherwise it waits, suspended, for
// unlockAudio() from the first click or key press, and audioLocked() stays true until then.
export function prepareAudio() {
  if (!makeContext()) return;
  if (context.state === "running") unlockListeners.forEach((listener) => listener(context));
  else context.resume?.().catch?.(() => {});
}

// True while the browser is holding sound back (no context yet, or suspended until a gesture).
// False when there is no WebAudio at all (nothing to unlock).
export function audioLocked() {
  const AudioContextClass = globalThis.AudioContext || globalThis.webkitAudioContext;
  if (!AudioContextClass) return false;
  return !context || context.state !== "running";
}

// Called with the context's state ("running", "suspended", ...) whenever it changes.
export function onAudioState(listener) {
  audioStateListeners.add(listener);
  return () => audioStateListeners.delete(listener);
}

export function installAudioUnlock(target = globalThis) {
  const unlock = () => unlockAudio();
  target.addEventListener?.("pointerdown", unlock, { passive: true });
  target.addEventListener?.("keydown", unlock);
}

// Called (with the AudioContext) each time a gesture unlocks or resumes audio.
export function onUnlock(listener) {
  unlockListeners.add(listener);
  return () => unlockListeners.delete(listener);
}

// For music.js: the running context (null before the first gesture), the music bus and the
// reverb input.
export function audioContext() {
  return context && context.state === "running" ? context : null;
}

export function musicOutput() {
  return musicBus;
}

export function reverbInput() {
  return reverbIn;
}

// Per-play loudness (opts.volume) while a recipe runs.
let level = 1;

function ready() {
  return Boolean(context && master && !muted && settings.effectsOn && settings.effects > 0 && context.state === "running");
}

// ---------------------------------------------------------------------------------------------
// Building blocks. `at` is seconds from now. `wet` sends some of it to the room.

function voice(node, gainNode, t0, end, wet) {
  gainNode.connect(fxBus);
  if (wet) {
    const send = context.createGain();
    send.gain.value = wet;
    gainNode.connect(send).connect(reverbIn);
  }
  node.start(t0);
  node.stop(end);
}

function tone({ freq = 440, to = null, type = "sine", at = 0, dur = 0.15, gain = 0.3, attack = 0.005, lowpass = 0, detune = 0, wet = 0 }) {
  const t0 = context.currentTime + at;
  const osc = context.createOscillator();
  const amp = context.createGain();
  osc.type = type;
  osc.detune.value = detune;
  osc.frequency.setValueAtTime(freq, t0);
  if (to) osc.frequency.exponentialRampToValueAtTime(to, t0 + dur);
  amp.gain.setValueAtTime(0.0001, t0);
  amp.gain.exponentialRampToValueAtTime(Math.max(0.0002, gain * level), t0 + attack);
  amp.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
  let head = osc;
  if (lowpass) {
    const filter = context.createBiquadFilter();
    filter.type = "lowpass";
    filter.frequency.value = lowpass;
    head = osc.connect(filter);
  }
  head.connect(amp);
  voice(osc, amp, t0, t0 + dur + 0.03, wet);
}

let noiseBuffer = null;
function noise({ at = 0, dur = 0.08, gain = 0.25, filter = 2000, to = null, q = 1, type = "bandpass", attack = 0.002, wet = 0 }) {
  if (!noiseBuffer) {
    noiseBuffer = context.createBuffer(1, context.sampleRate, context.sampleRate);
    const data = noiseBuffer.getChannelData(0);
    for (let index = 0; index < data.length; index += 1) data[index] = Math.random() * 2 - 1;
  }
  const t0 = context.currentTime + at;
  const source = context.createBufferSource();
  source.buffer = noiseBuffer;
  source.loop = true;
  const shape = context.createBiquadFilter();
  shape.type = type;
  shape.frequency.setValueAtTime(filter, t0);
  if (to) shape.frequency.exponentialRampToValueAtTime(to, t0 + dur);
  shape.Q.value = q;
  const amp = context.createGain();
  amp.gain.setValueAtTime(0.0001, t0);
  amp.gain.exponentialRampToValueAtTime(Math.max(0.0002, gain * level), t0 + attack);
  amp.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);
  source.connect(shape).connect(amp);
  voice(source, amp, t0, t0 + dur + 0.03, wet);
}

// A small piece of wood or bakelite knocked: a short pitched body and a click.
function knock(at, freq, gain = 0.3, body = 0.07) {
  tone({ freq, to: freq * 0.82, type: "sine", at, dur: body, gain });
  noise({ at, dur: 0.025, gain: gain * 0.9, filter: freq * 4, q: 3 });
}

// A coin: inharmonic metal partials, fast decay.
function clink(at, freq, gain = 0.12) {
  tone({ freq, type: "sine", at, dur: 0.22, gain, wet: 0.15 });
  tone({ freq: freq * 2.76, type: "sine", at, dur: 0.12, gain: gain * 0.45 });
  tone({ freq: freq * 5.4, type: "sine", at, dur: 0.06, gain: gain * 0.2 });
  noise({ at, dur: 0.015, gain: gain * 0.6, filter: 7000, q: 1, type: "highpass" });
}

// A soft bell (struck sine with a few partials).
function bell(at, freq, gain = 0.16, dur = 1.1) {
  tone({ freq, type: "sine", at, dur, gain, attack: 0.003, wet: 0.35 });
  tone({ freq: freq * 2, type: "sine", at, dur: dur * 0.6, gain: gain * 0.35, attack: 0.003, wet: 0.35 });
  tone({ freq: freq * 3.01, type: "sine", at, dur: dur * 0.35, gain: gain * 0.15, attack: 0.003 });
}

const NOTE = (midi) => 440 * 2 ** ((midi - 69) / 12);
const jitter = (spread) => (Math.random() - 0.5) * spread;

// ---------------------------------------------------------------------------------------------
// Recipes

const RECIPES = {
  dice() {
    let at = 0;
    for (let index = 0; index < 9; index += 1) {
      at += 0.035 + Math.random() * 0.05;
      knock(at, 900 + Math.random() * 700, 0.12 + Math.random() * 0.08, 0.035);
    }
    knock(0.62, 520, 0.32, 0.09);
    knock(0.76, 610, 0.24, 0.08);
    knock(0.84, 700, 0.1, 0.05);
  },
  hop({ step = 1 } = {}) {
    const freq = 480 * 2 ** (Math.min(step, 14) / 28);
    tone({ freq, to: freq * 0.9, type: "triangle", dur: 0.05, gain: 0.09, lowpass: 2200 });
    noise({ dur: 0.02, gain: 0.05, filter: 2600, q: 2 });
  },
  land() {
    tone({ freq: 150, to: 68, type: "sine", dur: 0.2, gain: 0.32 });
    noise({ dur: 0.07, gain: 0.12, filter: 420, q: 0.8, type: "lowpass" });
  },
  go() {
    [72, 76, 79].forEach((midi, index) => bell(index * 0.11, NOTE(midi), 0.13, 1.2));
    clink(0.36, 2600, 0.06);
  },
  coins({ amount = 50, collect = true, out = null } = {}) {
    if (out !== null && out !== undefined) collect = !out;
    const count = amount >= 500 ? 12 : amount >= 200 ? 7 : amount >= 50 ? 4 : 2;
    for (let index = 0; index < count; index += 1) {
      const lean = collect ? index / count : 1 - index / count;
      clink(index * (0.045 + Math.random() * 0.03), 2200 + lean * 900 + jitter(300), 0.1 + Math.random() * 0.04);
    }
    if (amount >= 500) {
      tone({ freq: 110, to: 70, type: "sine", dur: 0.35, gain: 0.25 });
      bell(count * 0.05, NOTE(collect ? 84 : 76), 0.08, 0.9);
    }
  },
  buy() {
    tone({ freq: 170, to: 62, type: "sine", dur: 0.22, gain: 0.45 });
    noise({ dur: 0.09, gain: 0.25, filter: 900, q: 1 });
    clink(0.2, 2500, 0.1);
    bell(0.26, NOTE(81), 0.06, 0.7);
  },
  gavel({ sold = false } = {}) {
    const knocks = sold ? [0, 0.18, 0.36] : [0, 0.16];
    knocks.forEach((at, index) => {
      tone({ freq: 300, to: 220, type: "sine", at, dur: 0.12, gain: 0.4 - index * 0.06 });
      noise({ at, dur: 0.05, gain: 0.3, filter: 1300, q: 2.5, wet: 0.2 });
    });
  },
  bid() {
    tone({ freq: 1250, type: "sine", dur: 0.04, gain: 0.08 });
    noise({ dur: 0.012, gain: 0.08, filter: 5000, q: 2 });
  },
  house({ step = 0 } = {}) {
    const freq = 210 * 2 ** (Math.min(step, 4) * 2 / 12);
    tone({ freq, to: freq * 0.7, type: "sine", dur: 0.16, gain: 0.32 });
    noise({ dur: 0.04, gain: 0.22, filter: 900, q: 3 });
    tone({ freq: freq * 2.5, type: "triangle", at: 0.01, dur: 0.12, gain: 0.05, lowpass: 3000 });
  },
  hotel() {
    tone({ freq: 160, to: 90, type: "sine", dur: 0.28, gain: 0.42 });
    tone({ freq: 95, to: 60, type: "sine", at: 0.04, dur: 0.32, gain: 0.3 });
    noise({ dur: 0.07, gain: 0.25, filter: 700, q: 2 });
    bell(0.18, NOTE(79), 0.07, 0.9);
  },
  mortgage() {
    for (let index = 0; index < 6; index += 1) noise({ at: index * 0.035 + Math.random() * 0.02, dur: 0.06, gain: 0.07, filter: 3500 + Math.random() * 2500, q: 0.7, type: "highpass" });
    tone({ freq: 220, to: 196, type: "triangle", at: 0.12, dur: 0.3, gain: 0.05, lowpass: 900 });
  },
  card() {
    noise({ dur: 0.2, gain: 0.12, filter: 1400, to: 4200, q: 0.8 });
    noise({ at: 0.2, dur: 0.035, gain: 0.22, filter: 3800, q: 1.4 });
    tone({ freq: 760, to: 560, type: "triangle", at: 0.2, dur: 0.06, gain: 0.06 });
  },
  jail() {
    [182, 271, 433, 617].forEach((freq, index) => tone({ freq, type: index % 2 ? "square" : "sawtooth", dur: 0.9 - index * 0.15, gain: 0.05, attack: 0.002, lowpass: 2400, wet: 0.3 }));
    tone({ freq: 90, to: 55, type: "sine", dur: 0.4, gain: 0.4 });
    noise({ dur: 0.08, gain: 0.25, filter: 1500, q: 1.5 });
    knock(0.52, 1400, 0.12, 0.03);
  },
  offer() {
    bell(0, NOTE(88), 0.14, 1.3);
  },
  accept() {
    bell(0, NOTE(76), 0.12, 0.6);
    bell(0.13, NOTE(81), 0.14, 0.9);
  },
  decline() {
    tone({ freq: NOTE(57), type: "sine", dur: 0.28, gain: 0.14, lowpass: 900 });
    tone({ freq: NOTE(53), type: "sine", at: 0.16, dur: 0.38, gain: 0.12, lowpass: 900 });
  },
  loan() {
    RECIPES.coins({ amount: 120, collect: true });
    [67, 71, 74].forEach((midi) => tone({ freq: NOTE(midi), type: "triangle", at: 0.12, dur: 0.8, gain: 0.05, attack: 0.04, lowpass: 2000, wet: 0.3 }));
  },
  bankrupt() {
    tone({ freq: 440, to: 98, type: "sawtooth", dur: 1.3, gain: 0.1, attack: 0.05, lowpass: 1100, wet: 0.25 });
    tone({ freq: 445, to: 100, type: "sawtooth", dur: 1.3, gain: 0.06, attack: 0.05, lowpass: 900, detune: 12 });
    tone({ freq: 110, to: 45, type: "sine", at: 1.05, dur: 0.5, gain: 0.5 });
  },
  win() {
    const brass = (midi, at, dur, gain = 0.09) => {
      tone({ freq: NOTE(midi), type: "sawtooth", at, dur, gain, attack: 0.03, lowpass: 2200, wet: 0.3 });
      tone({ freq: NOTE(midi), type: "sawtooth", at, dur, gain: gain * 0.6, attack: 0.03, lowpass: 1800, detune: 9 });
    };
    [[60, 0, 0.18], [64, 0.18, 0.18], [67, 0.36, 0.18], [72, 0.54, 1.1]].forEach(([midi, at, dur]) => brass(midi, at, dur));
    [48, 55, 64].forEach((midi) => brass(midi, 0.54, 1.2, 0.05));
    bell(0.54, NOTE(84), 0.08, 1.4);
  },
  turn() {
    bell(0, NOTE(88), 0.1, 0.55);
    tone({ freq: NOTE(76), type: "sine", dur: 0.4, gain: 0.05, wet: 0.2 });
  },
  tap() {
    tone({ freq: 640, to: 560, type: "triangle", dur: 0.045, gain: 0.08, lowpass: 2600 });
  },
  // ---- Poker ----
  dealCard({ count = 1 } = {}) {
    const n = Math.max(1, Math.min(5, Number(count) || 1));
    for (let index = 0; index < n; index += 1) {
      const at = index * 0.09;
      noise({ at, dur: 0.07, gain: 0.07, filter: 2600, to: 5200, q: 0.9 });
      noise({ at: at + 0.065, dur: 0.02, gain: 0.12, filter: 4200, q: 1.6 });
      tone({ freq: 900 + jitter(80), to: 700, type: "triangle", at: at + 0.065, dur: 0.035, gain: 0.03 });
    }
  },
  chips({ amount = 50, allIn = false } = {}) {
    // Clay chips: a dull click with a short ring, lower than a coin.
    const count = allIn ? 9 : amount >= 1000 ? 6 : amount >= 200 ? 4 : amount >= 50 ? 3 : 2;
    for (let index = 0; index < count; index += 1) {
      const at = index * (0.035 + Math.random() * 0.03);
      const freq = 1500 + Math.random() * 700;
      tone({ freq, to: freq * 0.92, type: "sine", at, dur: 0.06, gain: 0.07 });
      tone({ freq: freq * 2.3, type: "sine", at, dur: 0.035, gain: 0.025 });
      noise({ at, dur: 0.018, gain: 0.09, filter: 3800, q: 2 });
    }
    if (allIn) tone({ freq: 120, to: 70, type: "sine", at: count * 0.045, dur: 0.3, gain: 0.25 });
  },
  knock() {
    knock(0, 190, 0.3, 0.08);
    knock(0.12, 176, 0.24, 0.07);
  },
  fold() {
    noise({ dur: 0.16, gain: 0.07, filter: 3200, to: 900, q: 0.7, attack: 0.01 });
    noise({ at: 0.13, dur: 0.03, gain: 0.05, filter: 1200, q: 1 });
  },
  tick({ last = false } = {}) {
    knock(0, last ? 1500 : 1250, 0.09, 0.03);
  },
  potWin({ mine = false } = {}) {
    for (let index = 0; index < 10; index += 1) {
      const at = 0.02 + index * 0.028 + Math.random() * 0.02;
      const freq = 1400 + Math.random() * 900;
      tone({ freq, to: freq * 0.9, type: "sine", at, dur: 0.05, gain: 0.05 });
      noise({ at, dur: 0.015, gain: 0.06, filter: 4000, q: 2 });
    }
    noise({ dur: 0.35, gain: 0.04, filter: 1800, to: 900, q: 0.6, attack: 0.04 });
    if (mine) [72, 76, 79].forEach((midi, index) => bell(0.32 + index * 0.08, NOTE(midi), 0.09, 0.9));
  },
  // ---- Quiz ----
  quizOpen() {
    bell(0, NOTE(84), 0.1, 0.5);
    bell(0.07, NOTE(91), 0.08, 0.6);
    noise({ dur: 0.05, gain: 0.03, filter: 6000, q: 0.8, type: "highpass" });
  },
  quizLock() {
    knock(0, 820, 0.16, 0.04);
    tone({ freq: NOTE(93), type: "sine", at: 0.01, dur: 0.09, gain: 0.035 });
  },
  quizRight() {
    [76, 79, 84].forEach((midi, index) => bell(index * 0.09, NOTE(midi), 0.11, 0.9));
    tone({ freq: NOTE(60), type: "triangle", at: 0.18, dur: 0.6, gain: 0.05, attack: 0.03, lowpass: 1600, wet: 0.25 });
  },
  quizWrong() {
    tone({ freq: NOTE(55), to: NOTE(54), type: "triangle", dur: 0.22, gain: 0.1, lowpass: 800 });
    tone({ freq: NOTE(50), to: NOTE(48), type: "triangle", at: 0.17, dur: 0.38, gain: 0.1, lowpass: 700 });
    noise({ at: 0.17, dur: 0.06, gain: 0.04, filter: 500, q: 0.8, type: "lowpass" });
  },
  quizGold() {
    for (let index = 0; index < 7; index += 1) clink(index * 0.05 + jitter(0.01), 2800 + index * 160 + jitter(120), 0.06);
    [67, 71, 74, 79].forEach((midi) => tone({ freq: NOTE(midi), type: "triangle", at: 0.1, dur: 1.1, gain: 0.04, attack: 0.06, lowpass: 2200, wet: 0.35 }));
    bell(0.38, NOTE(86), 0.09, 1.2);
  },
  // Not in SOUND_NAMES: kept for the original stamp() and thud().
  stamp() {
    tone({ freq: 170, to: 62, type: "sine", dur: 0.22, gain: 0.5 });
    noise({ dur: 0.09, gain: 0.3, filter: 900, q: 1 });
  },
  thud() {
    tone({ freq: 110, to: 45, type: "sine", dur: 0.45, gain: 0.6 });
    tone({ freq: 196, to: 130, type: "sawtooth", at: 0.15, dur: 0.6, gain: 0.05, attack: 0.08, lowpass: 900 });
  },
};

// Play a named effect (see the list at the top). Unknown names and a locked, muted or switched-off
// context are ignored (returns false), so callers never need to check.
export function play(name, opts = {}) {
  const recipe = RECIPES[ALIASES[name] || name];
  const volume = unit(opts?.volume, 1);
  if (!recipe || !ready() || volume <= 0) return false;
  level = volume;
  try {
    recipe(opts || {});
    return true;
  } catch {
    return false;
  } finally {
    level = 1;
  }
}

// ---------------------------------------------------------------------------------------------
// The original API (the phone uses these): each maps onto the richer set.

export function dice() {
  play("dice");
}

// Soft tap per square; `firm` for the landing.
export function tap(firm = false) {
  play(firm ? "land" : "hop");
}

export function coins(amount = 120) {
  play("coins", { amount, collect: true });
}

export function stamp() {
  play("stamp");
}

// A house set down, a little higher per house (`step` = 0, 1, 2…).
export function pop(step = 0) {
  play("house", { step });
}

// Low thud (big rent, jail, bankruptcy).
export function thud() {
  play("thud");
}

export function fanfare() {
  play("win");
}

export function snap() {
  play("card");
}

export function click() {
  play("tap");
}

export const sounds = { dice, tap, coins, stamp, pop, thud, fanfare, snap, click };
