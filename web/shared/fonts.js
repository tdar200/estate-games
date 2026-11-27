// The two typefaces (Oswald, Inter) are served by Estate (shared/fonts.css, web/shared/fonts) with
// font-display: swap, and preloaded by each page: text paints at once in
// a fallback face and changes when the real one arrives. A face that arrived after the first game
// render re-flowed every name and number on screen (measured: Inter 600 landed about a second after
// first paint, after the board was drawn). So both apps ask for every face they use up front and
// let the first real render wait for them, for at most `ms` (they come from this server, so
// normally well inside it); if they are slow, the fallback is used and nothing changes later.

export const FACES = Object.freeze([
  "500 16px Oswald", "600 16px Oswald", "700 16px Oswald",
  "400 16px Inter", "500 16px Inter", "600 16px Inter", "700 16px Inter", "800 16px Inter",
]);

// The tabletop look's faces (shared/fonts-tabletop.css): the TV table waits for these too.
export const TABLETOP_FACES = Object.freeze([
  "500 16px 'Barlow Condensed'", "600 16px 'Barlow Condensed'", "700 16px 'Barlow Condensed'", "800 16px 'Barlow Condensed'",
  "400 16px Barlow", "500 16px Barlow", "600 16px Barlow", "700 16px Barlow",
]);

export function fontsReady(ms = 1200, doc = globalThis.document, faces = FACES) {
  const fonts = doc?.fonts;
  if (!fonts?.load) return Promise.resolve(false);
  const all = Promise.all(faces.map((face) => fonts.load(face).catch(() => []))).then(() => true);
  return Promise.race([all, new Promise((resolve) => setTimeout(() => resolve(false), ms))]);
}
