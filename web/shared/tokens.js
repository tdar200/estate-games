// The eight metal tokens (the first six from the approved Token design; anchor and kite drawn
// to match for 8-player tables). Each sits on an oval base in the player's colour. The metal
// gradient is defined once per document by ensureMetalDefs(). The order is the server's default
// assignment order (internal/server tokenKinds).

export const TOKEN_KINDS = ["rocket", "teapot", "bicycle", "lighthouse", "key", "umbrella", "anchor", "kite"];

// Retired token kinds and the token that replaced them (internal/server retiredTokenKinds). Saved
// games, records and phone profiles may still name one; it shows as its replacement everywhere.
export const RETIRED_TOKEN_KINDS = {};

// Tokens the pickers mark NEW (UX-08).
export const NEW_TOKEN_KINDS = ["kite"];

// A token kind, with a retired one swapped for its replacement.
export function canonicalKind(kind) {
  return Object.hasOwn(RETIRED_TOKEN_KINDS, kind) ? RETIRED_TOKEN_KINDS[kind] : kind;
}

// Lets table[retired] read table[replacement] without listing the retired kind (keys, entries and
// equality checks only see the eight current tokens).
function withRetired(table) {
  for (const [old, now] of Object.entries(RETIRED_TOKEN_KINDS)) {
    Object.defineProperty(table, old, { get: () => table[now], enumerable: false });
  }
  return table;
}

// Most players at one table (the server's limit).
export const MAX_PLAYERS = TOKEN_KINDS.length;

// Player palette (one colour per token). JS twin of web/shared/palette.json, which the Go server
// reads to colour party-table players; web/tests/board.check.mjs fails if the two differ. Chosen to
// stay clear of the eight property-group colours (>= 19 CIEDE2000 from every group, also checked
// in the test) so an owner chip can never be mistaken for a colour bar. Table in tokens.css.
export const TOKEN_COLORS = withRetired({
  rocket: "#1EA0FF",
  teapot: "#7A1838",
  bicycle: "#6F7A1A",
  lighthouse: "#0E8C8C",
  key: "#8A5CF5",
  umbrella: "#1E2228",
  anchor: "#D88890",
  kite: "#727078",
});

export const TOKEN_COLOR_NAMES = withRetired({
  rocket: "Sky",
  teapot: "Wine",
  bicycle: "Olive",
  lighthouse: "Teal",
  key: "Violet",
  umbrella: "Onyx",
  anchor: "Rose",
  kite: "Slate",
});

export const TOKEN_LABELS = withRetired({
  rocket: "Rocket",
  teapot: "Teapot",
  bicycle: "Bicycle",
  lighthouse: "Lighthouse",
  key: "Key",
  umbrella: "Umbrella",
  anchor: "Anchor",
  kite: "Kite",
});

const METAL = "url(#estate-metal)";
const S = 'stroke="#2B323A"';

// Body of each token, without the base ellipse.
const BODIES = withRetired({
  rocket: `<path d="M50 8 C64 20 68 40 66 62 L34 62 C32 40 36 20 50 8 Z" fill="${METAL}" ${S} stroke-width="3.5"/><circle cx="50" cy="36" r="8" fill="#2B323A"/><circle cx="50" cy="36" r="5" fill="#9FD3F0"/><path d="M34 48 L22 66 L34 64 Z M66 48 L78 66 L66 64 Z" fill="${METAL}" ${S} stroke-width="3.5" stroke-linejoin="round"/><path d="M42 62 L44 84 L56 84 L58 62" fill="${METAL}" ${S} stroke-width="3.5"/>`,
  teapot: `<path d="M72 50 C86 46 88 66 74 70" fill="none" ${S} stroke-width="9"/><path d="M72 50 C86 46 88 66 74 70" fill="none" stroke="${METAL}" stroke-width="3.5"/><path d="M28 54 L12 40 L17 37 L31 47" fill="${METAL}" ${S} stroke-width="3.5" stroke-linejoin="round"/><path d="M28 44 C28 34 72 34 72 44 L74 68 C74 78 62 84 50 84 C38 84 26 78 26 68 Z" fill="${METAL}" ${S} stroke-width="3.5"/><path d="M36 38 C36 27 64 27 64 38 Z" fill="${METAL}" ${S} stroke-width="3.5"/><circle cx="50" cy="24" r="5" fill="${METAL}" ${S} stroke-width="3"/>`,
  bicycle: `<circle cx="27" cy="62" r="17" fill="none" ${S} stroke-width="9"/><circle cx="27" cy="62" r="17" fill="none" stroke="${METAL}" stroke-width="4"/><circle cx="73" cy="62" r="17" fill="none" ${S} stroke-width="9"/><circle cx="73" cy="62" r="17" fill="none" stroke="${METAL}" stroke-width="4"/><path d="M27 62 L43 36 L66 36 L73 62 M43 36 L51 62 L27 62 M66 36 L62 25 M38 28 L50 28" fill="none" ${S} stroke-width="7" stroke-linecap="round" stroke-linejoin="round"/><path d="M27 62 L43 36 L66 36 L73 62 M43 36 L51 62 L27 62 M66 36 L62 25 M38 28 L50 28" fill="none" stroke="${METAL}" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>`,
  lighthouse: `<path d="M40 30 L60 30 L66 82 L34 82 Z" fill="${METAL}" ${S} stroke-width="3.5" stroke-linejoin="round"/><path d="M38 48 L62 48 M36 65 L64 65" ${S} stroke-width="3"/><rect x="40" y="18" width="20" height="12" fill="#FFE9A8" ${S} stroke-width="3.5"/><path d="M36 18 L50 7 L64 18 Z" fill="${METAL}" ${S} stroke-width="3.5" stroke-linejoin="round"/><rect x="28" y="80" width="44" height="7" rx="2" fill="${METAL}" ${S} stroke-width="3"/>`,
  key: `<circle cx="30" cy="48" r="19" fill="${METAL}" ${S} stroke-width="3.5"/><circle cx="30" cy="48" r="7" fill="#2B323A"/><path d="M48 43 H90 V53 H84 V65 H75 V53 H68 V61 H60 V53 H48 Z" fill="${METAL}" ${S} stroke-width="3.5" stroke-linejoin="round"/>`,
  anchor: `<path d="M21 55 C23 73 36 82 50 82 C64 82 77 73 79 55" fill="none" ${S} stroke-width="9" stroke-linecap="round"/><path d="M21 55 C23 73 36 82 50 82 C64 82 77 73 79 55" fill="none" stroke="${METAL}" stroke-width="4" stroke-linecap="round"/><path d="M13 61 L19 46 L29 59 Z M87 61 L81 46 L71 59 Z" fill="${METAL}" ${S} stroke-width="3.5" stroke-linejoin="round"/><rect x="45" y="25" width="10" height="56" rx="3" fill="${METAL}" ${S} stroke-width="3.5"/><rect x="31" y="29" width="38" height="9" rx="3.5" fill="${METAL}" ${S} stroke-width="3.5"/><circle cx="50" cy="16" r="8" fill="none" ${S} stroke-width="9"/><circle cx="50" cy="16" r="8" fill="none" stroke="${METAL}" stroke-width="4"/>`,
  kite: `<path d="M50 5 L79 30 L50 62 L21 30 Z" fill="${METAL}" ${S} stroke-width="3.5" stroke-linejoin="round"/><path d="M50 6.5 L22.5 30 L50 30 Z M50 30 L77.5 30 L50 60.5 Z" fill="#2B323A" fill-opacity="0.42"/><path d="M50 5 V62 M21 30 H79" fill="none" ${S} stroke-width="2.5"/><path d="M50 62 C64 68 36 77 50 86" fill="none" ${S} stroke-width="6.5" stroke-linecap="round"/><path d="M50 62 C64 68 36 77 50 86" fill="none" stroke="${METAL}" stroke-width="2.5" stroke-linecap="round"/><path d="M46.5 63.5 L53.5 68 L46.5 72.5 Z M60.5 63.5 L53.5 68 L60.5 72.5 Z M39 75 L46 79.3 L39 83.6 Z M53 75 L46 79.3 L53 83.6 Z" fill="${METAL}" ${S} stroke-width="2.5" stroke-linejoin="round"/>`,
  umbrella: `<path d="M50 46 V76 C50 85 39 85 39 77" fill="none" ${S} stroke-width="8" stroke-linecap="round"/><path d="M50 46 V76 C50 85 39 85 39 77" fill="none" stroke="${METAL}" stroke-width="3.5" stroke-linecap="round"/><path d="M12 46 C12 24 30 10 50 10 C70 10 88 24 88 46 C82 40 76 40 70 46 C64 40 56 40 50 46 C44 40 36 40 30 46 C24 40 18 40 12 46 Z" fill="${METAL}" ${S} stroke-width="3.5" stroke-linejoin="round"/><path d="M50 10 V46 M30 15 C26 26 28 38 30 46 M70 15 C74 26 72 38 70 46" fill="none" ${S} stroke-width="2"/>`,
});

// Base widths differ slightly per token in the design (bicycle and key are wider).
const BASE_RX = withRetired({ rocket: 34, teapot: 34, bicycle: 40, lighthouse: 34, key: 38, umbrella: 34, anchor: 38, kite: 34 });

export function metalDefsMarkup() {
  return '<svg class="metal-defs" width="0" height="0" aria-hidden="true" focusable="false" style="position:absolute;width:0;height:0;overflow:hidden"><defs><linearGradient id="estate-metal" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#FBFCFD"/><stop offset="0.42" stop-color="#B9C1C9"/><stop offset="0.7" stop-color="#8A949E"/><stop offset="1" stop-color="#4E5862"/></linearGradient></defs></svg>';
}

export function ensureMetalDefs(doc = globalThis.document) {
  if (!doc || doc.getElementById("estate-metal")) return;
  doc.body.insertAdjacentHTML("afterbegin", metalDefsMarkup());
}

export function normaliseKind(kind, fallbackIndex = 0) {
  kind = canonicalKind(kind);
  return TOKEN_KINDS.includes(kind) ? kind : TOKEN_KINDS[Math.abs(fallbackIndex) % TOKEN_KINDS.length];
}

// Seat colours the JS engine assigns in join order (src/game.js PLAYER_COLORS). They survive the
// turn-order shuffle, so they give servers without tokenKind a stable token per player.
const ENGINE_COLORS = ["#d33f49", "#2d68c4", "#26966b", "#d58b20", "#8b55b7", "#168c9b", "#8a5a32", "#5c6b7a"];

// Token kind for a player: the §3 tokenKind, else stable from the seat colour, else seat order.
export function tokenKindOf(state, player) {
  const kind = canonicalKind(player?.tokenKind);
  if (kind && TOKEN_KINDS.includes(kind)) return kind;
  const byColor = ENGINE_COLORS.indexOf(String(player?.color || "").toLowerCase());
  if (byColor >= 0) return TOKEN_KINDS[byColor];
  const index = Math.max(0, (state?.players || []).findIndex((candidate) => candidate.id === player?.id));
  return TOKEN_KINDS[index % TOKEN_KINDS.length];
}

function escapeAttr(value) {
  return String(value ?? "").replaceAll("&", "&amp;").replaceAll('"', "&quot;").replaceAll("<", "&lt;");
}

// SVG markup for a token. `label` makes it an image with an accessible name; otherwise decorative.
export function tokenSvg(kind, base, { size = 100, label = "", className = "" } = {}) {
  const safeKind = normaliseKind(kind);
  const fill = /^#[0-9a-f]{3,8}$/i.test(String(base || "")) ? base : TOKEN_COLORS[safeKind];
  const a11y = label ? `role="img" aria-label="${escapeAttr(label)}"` : 'aria-hidden="true" focusable="false"';
  return `<svg class="token-svg ${escapeAttr(className)}" width="${size}" height="${size}" viewBox="0 0 100 100" ${a11y}><ellipse cx="50" cy="89" rx="${BASE_RX[safeKind]}" ry="9" fill="${fill}" stroke="#0E1216" stroke-width="3"/>${BODIES[safeKind]}</svg>`;
}

// The colour a player is shown in everywhere (board owner chips, token bases, plates, feed dots).
// Party tables already send players[].color = TOKEN_COLORS[tokenKind] (server reads palette.json);
// the token kind wins here too so demo fixtures and older servers look the same. Classic rooms
// (no tokenKind) keep the colour the engine assigned.
export function playerColor(state, player) {
  if (!player) return "#8A929C";
  if (player.tokenKind && TOKEN_COLORS[player.tokenKind]) return TOKEN_COLORS[player.tokenKind];
  return /^#[0-9a-f]{6}$/i.test(String(player.color || "")) ? player.color : TOKEN_COLORS[tokenKindOf(state, player)];
}

// Text colour for a chip on a background colour: white whenever it reaches 3:1 (WCAG large/bold
// text; chips carry one bold initial), otherwise ink.
export function inkOn(hex) {
  const value = String(hex || "").replace("#", "");
  if (!/^[0-9a-f]{6}$/i.test(value)) return "#FFFFFF";
  const channel = (index) => {
    const c = parseInt(value.slice(index, index + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  const luminance = 0.2126 * channel(0) + 0.7152 * channel(2) + 0.0722 * channel(4);
  return 1.05 / (luminance + 0.05) >= 3 ? "#FFFFFF" : "#15191E";
}

// First letter of a name for owner chips and compact tokens ("?" when there is none).
export function initialOf(name) {
  const match = String(name || "").trim().match(/[\p{L}\p{N}]/u);
  return match ? match[0].toUpperCase() : "?";
}

export function playerTokenSvg(state, player, options = {}) {
  return tokenSvg(tokenKindOf(state, player), playerColor(state, player), options);
}

// One-colour silhouettes of the eight tokens (24 × 24, currentColor) for small round chips: board
// owner flags, chip tokens, player chips. Two players never share a token at a party table, so a
// glyph is unique where initials collide ("Ana" and "Alpha" are both "A").
export const TOKEN_GLYPHS = withRetired({
  rocket: '<path fill-rule="evenodd" d="M12 1.6c3.5 3 5 7 4.5 11.9h-9C7 8.6 8.5 4.6 12 1.6zm0 4.1a2 2 0 1 0 0 4 2 2 0 0 0 0-4z"/><path d="M7.6 9.6L4.2 15.4l3.6-.9zM16.4 9.6l3.4 5.8-3.6-.9zM9.4 14.6h5.2l-.8 6.2h-3.6z"/>',
  teapot: '<path d="M5.4 10.6h13.2l-.7 6.4c-.3 2.4-2.6 3.8-5.9 3.8s-5.6-1.4-5.9-3.8zM8 10.2c0-2.6 8-2.6 8 0z"/><circle cx="12" cy="6.3" r="1.5"/><path d="M6.4 12.8L1.9 9.1l-1 1.2 4.6 4.8z"/><path d="M18.1 11.6c3.2-.8 4.1 4 .6 5" fill="none" stroke="currentColor" stroke-width="2"/>',
  bicycle: '<g fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><circle cx="5.8" cy="15.6" r="4"/><circle cx="18.2" cy="15.6" r="4"/><path d="M5.8 15.6l4-7.2h6.4l2 7.2M9.8 8.4l2.6 7.2H5.8M16.2 8.4l-1-2.8M8.4 5.6h3.4"/></g>',
  lighthouse: '<path fill-rule="evenodd" d="M9.3 8h5.4l1.7 12.2H7.6zm-.2 4.4h5.8v1.8H9.1z"/><path d="M9.8 4.6h4.4v2.8H9.8zM8.4 4.4L12 1.4l3.6 3zM6.2 20.4h11.6v2.2H6.2z"/>',
  key: '<path fill-rule="evenodd" d="M7.4 6.6a5.1 5.1 0 1 1 0 10.2 5.1 5.1 0 0 1 0-10.2zm0 3.2a1.9 1.9 0 1 0 0 3.8 1.9 1.9 0 0 0 0-3.8z"/><path d="M12 10.3h10.6v2.8H21v3.1h-2.6v-3.1h-1.5v2.1h-2.4v-2.1H12z"/>',
  umbrella: '<path d="M2.2 12.2C2.2 6.4 6.6 2.8 12 2.8s9.8 3.6 9.8 9.4c-1.5-1.4-3.1-1.4-4.6 0-1.5-1.4-3.6-1.4-5.2 0-1.6-1.4-3.7-1.4-5.2 0-1.5-1.4-3.1-1.4-4.6 0z"/><path d="M12 11.6v7c0 2.6-3.4 2.6-3.4.4" fill="none" stroke="currentColor" stroke-width="2.1" stroke-linecap="round"/>',
  anchor: '<path fill-rule="evenodd" d="M12 1.2a2.6 2.6 0 1 1 0 5.2 2.6 2.6 0 0 1 0-5.2zm0 1.4a1.2 1.2 0 1 0 0 2.4 1.2 1.2 0 0 0 0-2.4z"/><path d="M10.8 6.2h2.4v14.2h-2.4zM7.2 7.4h9.6v2.2H7.2z"/><path d="M5 13.2c.6 4.4 3.6 7.2 7 7.2s6.4-2.8 7-7.2" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"/><path d="M2.8 14.8l2-4.6 2.8 3.8zM21.2 14.8l-2-4.6-2.8 3.8z"/>',
  kite: '<path d="M11.4 1.9L5.6 8h5.8zM12.6 1.9L18.4 8h-5.8zM5.5 9.2h5.9v7.1zM18.5 9.2h-5.9v7.1z"/><path d="M12 16.4c-2.4 1.6 2.4 3.6 0 5.6" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/><path d="M9.2 17.8l2.8 1.25-2.8 1.25zM14.8 17.8L12 19.05l2.8 1.25z"/>',
});

export function tokenGlyphSvg(kind, { className = "", label = "" } = {}) {
  const safeKind = normaliseKind(kind);
  const a11y = label ? `role="img" aria-label="${escapeAttr(label)}"` : 'aria-hidden="true" focusable="false"';
  return `<svg class="glyph-svg ${escapeAttr(className)}" viewBox="0 0 24 24" fill="currentColor" ${a11y}>${TOKEN_GLYPHS[safeKind]}</svg>`;
}

export function playerGlyphSvg(state, player, options = {}) {
  return tokenGlyphSvg(tokenKindOf(state, player), options);
}

// Round chip in the player's colour: the owner marker on board squares and the compact (phone)
// token. A different shape from the square colour bars, so ownership never relies on colour alone.
// Shows the player's initial by default; `glyph: true` shows their token's silhouette instead,
// which never collides. Styles: .pchip in tokens.css.
export function playerChipHtml(state, player, { size = 22, className = "", label = "", glyph = false } = {}) {
  const color = playerColor(state, player);
  const a11y = label ? `role="img" aria-label="${escapeAttr(label)}"` : 'aria-hidden="true"';
  const inner = glyph ? playerGlyphSvg(state, player) : escapeAttr(initialOf(player?.name));
  return `<span class="pchip${glyph ? " pchip--glyph" : ""} ${escapeAttr(className)}" style="--pc:${color};--pc-ink:${inkOn(color)};--pc-size:${size}px" ${a11y}>${inner}</span>`;
}
