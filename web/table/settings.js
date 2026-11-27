// This screen's own settings (camera, animation speed, motion), saved in localStorage. Sound and
// music volumes live with the sound engine (web/shared/sound.js); computer speed is the room's
// (POST /settings). Every storage access is wrapped: private mode just forgets them.

export const SETTINGS_KEY = "estate.table.settings";
export const ZOOMS = Object.freeze([1.6, 2, 2.4]);
export const SPEEDS = Object.freeze(["normal", "fast"]);
export const MOTIONS = Object.freeze(["system", "reduce", "full"]);
export const COMPUTER_SPEEDS = Object.freeze(["slow", "normal", "fast"]);
// camera: CAMERA in settings. "follow" = FOLLOW MOVES (the camera zooms in on each hop and follows
// the token, rests on the landing square, then shows the whole board), "closeups" = CLOSE-UPS ONLY
// (the whole board, with a short close-up on a big rent), "off" = always the whole board.
// zoom: how close the camera goes.
export const CAMERAS = Object.freeze(["follow", "closeups", "off"]);
export const DEFAULT_SCREEN = Object.freeze({ camera: "follow", zoom: 1.6, speed: "normal", motion: "system" });
// Saved settings carry this version. Before 2 (0.11.5) the default zoom was 2×, which read as a cut
// (review F9): a screen saved then at 2× moves to the new 1.6× default; 2.4× (picked) stays.
export const SCREEN_VERSION = 2;

export function normaliseScreen(raw) {
  const value = raw && typeof raw === "object" ? raw : {};
  const zoom = Number(value.zoom);
  // Saved before 0.10.1 (no `camera`): `follow` only switched the big-rent close-up and 1.6× was the
  // default zoom, so such a screen starts on the current defaults; a zoom the owner picked (2×,
  // 2.4×) stays.
  const legacy = !("camera" in value);
  return {
    camera: CAMERAS.includes(value.camera) ? value.camera : DEFAULT_SCREEN.camera,
    zoom: ZOOMS.includes(zoom) && !(legacy && zoom === 1.6) ? zoom : DEFAULT_SCREEN.zoom,
    speed: SPEEDS.includes(value.speed) ? value.speed : DEFAULT_SCREEN.speed,
    motion: MOTIONS.includes(value.motion) ? value.motion : DEFAULT_SCREEN.motion,
  };
}

export function loadScreen(storage = globalThis.localStorage) {
  try {
    const saved = JSON.parse(storage?.getItem(SETTINGS_KEY) || "{}") || {};
    // Saved before SCREEN_VERSION 2 at the old 2× default: take the new default instead.
    if (typeof saved === "object" && !(Number(saved.v) >= SCREEN_VERSION) && Number(saved.zoom) === 2) delete saved.zoom;
    return normaliseScreen(saved);
  } catch {
    return { ...DEFAULT_SCREEN };
  }
}

export function saveScreen(settings, storage = globalThis.localStorage) {
  const clean = normaliseScreen(settings);
  try {
    storage?.setItem(SETTINGS_KEY, JSON.stringify({ ...clean, v: SCREEN_VERSION }));
  } catch {
    // Private mode: the settings last this visit.
  }
  return clean;
}
