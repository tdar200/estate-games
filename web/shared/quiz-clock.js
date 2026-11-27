// The quiz's clock and buzzer timing (docs/QUIZ-API.md §1.1 and §6). Pure functions and small
// timers with injectable clocks, so web/tests/quiz.check.mjs runs them in Node.
//
// Fair answering needs three things from the screens:
//   1. A shared idea of "now". Every pong carries the server's clock; the sample with the lowest
//      round trip of the last 8 is the most trustworthy (least time in the network), so its offset
//      wins. Synced server time = Date.now() + offset.
//   2. The choices appear when the synced clock reaches clue.openAt, on every phone and the table
//      at the same moment, whatever moment the state arrived in. A state that arrives late shows
//      them at once.
//   3. The phone times the person, not the network: it notes when the choices were painted (the
//      requestAnimationFrame after they appear) and sends ms = tap.timeStamp − paintTime.

export const PING_SAMPLES = 8;
export const PING_FAST_MS = 2_000; // while a quiz room is playing
export const PING_SLOW_MS = 15_000; // everything else (the old heartbeat)

// One pong → { rtt, offset } (docs §1.1): rtt = receivedAt − t, offset = now + rtt/2 − receivedAt.
export function clockSample(sentAt, serverNow, receivedAt) {
  const rtt = receivedAt - sentAt;
  return { rtt, offset: serverNow + rtt / 2 - receivedAt };
}

// The sample with the lowest round trip (the first one on a tie), or null.
export function bestSample(samples) {
  let best = null;
  for (const sample of samples || []) {
    if (!sample || !Number.isFinite(sample.rtt) || !Number.isFinite(sample.offset)) continue;
    if (!best || sample.rtt < best.rtt) best = sample;
  }
  return best;
}

// The clock for one room connection. pong() takes the server's {t, now}; seen() takes a state's
// `now` and is only used until the first pong (it misses half a round trip, but beats nothing).
export function createClockSync({ limit = PING_SAMPLES, clientNow = () => Date.now() } = {}) {
  const samples = [];
  let fallback = null;
  const clock = {
    samples,
    pong(message, receivedAt = clientNow()) {
      const sentAt = Number(message?.t);
      const serverNow = Number(message?.now);
      if (!Number.isFinite(sentAt) || !Number.isFinite(serverNow) || message?.t === undefined) return null;
      const sample = clockSample(sentAt, serverNow, receivedAt);
      // A negative or absurd round trip is a clock that jumped, not a measurement.
      if (sample.rtt < 0 || sample.rtt > 60_000) return null;
      samples.push(sample);
      while (samples.length > limit) samples.shift();
      return sample;
    },
    seen(serverNow, receivedAt = clientNow()) {
      if (Number.isFinite(serverNow) && serverNow > 0) fallback = serverNow - receivedAt;
    },
    synced: () => samples.length > 0,
    offset() {
      const best = bestSample(samples);
      return best ? best.offset : fallback ?? 0;
    },
    // The best round trip so far (sent with each ping as `rtt`), or null before the first pong.
    rtt() {
      const best = bestSample(samples);
      return best ? best.rtt : null;
    },
    now(at = clientNow()) {
      return at + clock.offset();
    },
    reset() {
      samples.length = 0;
      fallback = null;
    },
  };
  return clock;
}

// The ping to send: {type, t} plus the best rtt once there is one (rounded, ms).
export function pingMessage(clock, sentAt = Date.now()) {
  const message = { type: "ping", t: sentAt };
  const rtt = clock?.rtt?.();
  if (Number.isFinite(rtt)) message.rtt = Math.max(0, Math.round(rtt));
  return message;
}

// How often to ping for this state: every 2 s while a quiz plays or an Estate timed auction runs
// (its countdown is on the server's clock, docs/API.md §3c), else every 15 s.
export function pingInterval(state) {
  if (state?.game === "quiz" && state.status === "playing") return PING_FAST_MS;
  if (state?.phase === "auction" && Number(state.auction?.clockMs) > 0) return PING_FAST_MS;
  return PING_SLOW_MS;
}

// ---------------------------------------------------------------------------------------------
// Which clue is open, and when

const BOARD_PHASES = ["read", "answer", "reveal"];
const FINAL_PHASES = ["finalRead", "finalAnswer", "finalReveal"];

// The clue being read, answered or revealed: the board clue or the final, with its `final` flag,
// or null. Both share text / readAt / openAt / closesAt / choices.
export function activeClue(state) {
  if (!state) return null;
  if (BOARD_PHASES.includes(state.phase) && state.clue?.text) return { ...state.clue, final: false, key: `${state.code}|c${state.clue.seq}` };
  if (FINAL_PHASES.includes(state.phase) && state.final?.text) return { ...state.final, final: true, key: `${state.code}|f${state.final.seq}` };
  return null;
}

// Are the choices showing at server time `now`? In the answer phase always (the server only
// enters it at openAt, so a slow clock must not hide them); while reading, once the synced clock
// reaches openAt; at the reveal, yes.
export function choicesOpen(state, now) {
  const clue = activeClue(state);
  if (!clue || !Array.isArray(clue.choices) || clue.choices.length !== 4) return false;
  if (state.phase === "answer" || state.phase === "finalAnswer" || state.phase === "reveal" || state.phase === "finalReveal") return true;
  return Number.isFinite(clue.openAt) && clue.openAt > 0 && Number.isFinite(now) && now >= clue.openAt;
}

// Milliseconds until the choices open (0 when they already have).
export function msUntilOpen(state, now) {
  const clue = activeClue(state);
  if (!clue || choicesOpen(state, now)) return 0;
  return Math.max(0, (clue.openAt || 0) - now);
}

// 0–1: how far the reading bar has run at `now`.
export function readProgress(clue, now) {
  if (!clue || !Number.isFinite(clue.readAt) || !Number.isFinite(clue.openAt) || clue.openAt <= clue.readAt) return 1;
  return Math.max(0, Math.min(1, (now - clue.readAt) / (clue.openAt - clue.readAt)));
}

// Whole seconds left until `endsAt` (rounded up, never below 0).
export function secondsLeft(endsAt, now) {
  if (!Number.isFinite(endsAt) || endsAt <= 0) return 0;
  return Math.max(0, Math.ceil((endsAt - now) / 1000));
}

// ---------------------------------------------------------------------------------------------
// Timers

// Run `fn` once the synced clock reaches `target`. Late (or no target): at once, synchronously,
// so a state that arrives after openAt shows the choices in the same frame. setTimeout can fire a
// few ms early, so it checks again and waits the rest. Returns cancel().
export function scheduleAt(target, fn, { now = () => Date.now(), setTimer = setTimeout, clearTimer = clearTimeout } = {}) {
  let handle = null;
  let done = false;
  const run = () => {
    if (done) return;
    const left = target - now();
    if (left > 0) {
      handle = setTimer(run, Math.min(left, 2_147_000_000));
      return;
    }
    done = true;
    fn();
  };
  run();
  return () => {
    done = true;
    if (handle !== null) clearTimer(handle);
  };
}

// The tap's time from the choices being painted (docs §6.2), rounded, never negative. Without a
// paint time (a tap in the same frame) it counts from when they were shown. Null when it can't be
// measured: the tap is then sent without ms and the server times it by its arrival (§6.3);
// sending 0 would claim the best possible time.
export function tapMs(eventTimeStamp, paintTime, shownTime = null) {
  const from = Number.isFinite(paintTime) ? paintTime : shownTime;
  if (!Number.isFinite(eventTimeStamp) || !Number.isFinite(from)) return null;
  return Math.max(0, Math.round(eventTimeStamp - from));
}

// Note when something just un-hidden is painted: the next animation frame (docs §6.2). Calls
// back with performance.now() then.
export function notePaint(callback, { raf = globalThis.requestAnimationFrame, clock = () => globalThis.performance?.now?.() ?? Date.now() } = {}) {
  if (typeof raf !== "function") {
    callback(clock());
    return;
  }
  raf(() => callback(clock()));
}

// ---------------------------------------------------------------------------------------------
// Bets (gold squares and the final): whole points within [min, max]

export function clampBet(amount, min, max) {
  const low = Math.ceil(Number(min) || 0);
  const high = Math.max(low, Math.floor(Number(max) || 0));
  const value = Math.round(Number(amount));
  if (!Number.isFinite(value)) return low;
  return Math.min(high, Math.max(low, value));
}

// The stepper's step: small ranges move in 10s, the usual ones in 50s, big ones in 100s.
export function betStep(min, max) {
  const span = Math.max(0, (Number(max) || 0) - (Number(min) || 0));
  if (span <= 300) return 10;
  if (span <= 1500) return 50;
  return 100;
}

// One press of − or +: to the next multiple of the step, held within the range.
export function stepBet(amount, direction, min, max) {
  const step = betStep(min, max);
  const value = clampBet(amount, min, max);
  const next = direction > 0 ? Math.floor(value / step) * step + step : Math.ceil(value / step) * step - step;
  return clampBet(next, min, max);
}

// MIN, HALF and MAX. HALF is half the maximum, to the nearest 5.
export function betPresets(min, max) {
  const low = clampBet(min, min, max);
  const high = clampBet(max, min, max);
  return { min: low, half: clampBet(Math.round(high / 2 / 5) * 5, low, high), max: high };
}
