// Checks for the shared board (web/shared/board.js), the player palette (palette.json ↔ tokens.js ↔
// tokens.css) and the receipts module. Pure Node: the board's geometry runs against a fake 840 px
// grid built from the same CSS track sizes. Run: node web/tests/board.check.mjs

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

import { Board, abbrevName, cellPosition, fanLayout, miniDeedHtml, zoneLayout } from "../shared/board.js";
import { GROUP_COLORS } from "../shared/format.js";
import { nextRollHeat } from "../shared/insights.js";
import { receiptsFor } from "../shared/receipts.js";
import { TOKEN_COLORS, TOKEN_GLYPHS, TOKEN_KINDS, inkOn, initialOf, playerChipHtml, playerColor, tokenGlyphSvg, tokenKindOf } from "../shared/tokens.js";

const here = dirname(fileURLToPath(import.meta.url));
const read = (...parts) => readFileSync(join(here, "..", ...parts), "utf8");
const fixture = (name) => JSON.parse(read("fixtures", `${name}.json`));

// ---- colour maths (CIEDE2000, WCAG contrast) --------------------------------------------------
const lin = (c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
const rgb = (hex) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
function lab(hex) {
  const [r, g, b] = rgb(hex).map(lin);
  const f = (t) => (t > 0.008856 ? Math.cbrt(t) : 7.787 * t + 16 / 116);
  const x = f((r * 0.4124 + g * 0.3576 + b * 0.1805) / 0.95047);
  const y = f(r * 0.2126 + g * 0.7152 + b * 0.0722);
  const z = f((r * 0.0193 + g * 0.1192 + b * 0.9505) / 1.08883);
  return [116 * y - 16, 500 * (x - y), 200 * (y - z)];
}
function de00(h1, h2) {
  const [L1, a1, b1] = lab(h1);
  const [L2, a2, b2] = lab(h2);
  const rad = Math.PI / 180;
  const Cb = (Math.hypot(a1, b1) + Math.hypot(a2, b2)) / 2;
  const G = 0.5 * (1 - Math.sqrt(Cb ** 7 / (Cb ** 7 + 25 ** 7)));
  const a1p = (1 + G) * a1;
  const a2p = (1 + G) * a2;
  const C1 = Math.hypot(a1p, b1);
  const C2 = Math.hypot(a2p, b2);
  const h1p = (Math.atan2(b1, a1p) / rad + 360) % 360;
  const h2p = (Math.atan2(b2, a2p) / rad + 360) % 360;
  let dh = h2p - h1p;
  if (C1 * C2 === 0) dh = 0;
  else if (dh > 180) dh -= 360;
  else if (dh < -180) dh += 360;
  const dH = 2 * Math.sqrt(C1 * C2) * Math.sin((dh * rad) / 2);
  const Lb = (L1 + L2) / 2;
  const Cbp = (C1 + C2) / 2;
  let hb = h1p + h2p;
  if (C1 * C2 !== 0) hb = Math.abs(h1p - h2p) > 180 ? (h1p + h2p + 360) / 2 : (h1p + h2p) / 2;
  const T = 1 - 0.17 * Math.cos((hb - 30) * rad) + 0.24 * Math.cos(2 * hb * rad) + 0.32 * Math.cos((3 * hb + 6) * rad) - 0.2 * Math.cos((4 * hb - 63) * rad);
  const dTh = 30 * Math.exp(-(((hb - 275) / 25) ** 2));
  const Rc = 2 * Math.sqrt(Cbp ** 7 / (Cbp ** 7 + 25 ** 7));
  const SL = 1 + (0.015 * (Lb - 50) ** 2) / Math.sqrt(20 + (Lb - 50) ** 2);
  const SC = 1 + 0.045 * Cbp;
  const SH = 1 + 0.015 * Cbp * T;
  const RT = -Math.sin(2 * dTh * rad) * Rc;
  return Math.sqrt((L2 - L1) ** 2 / SL ** 2 + ((C2 - C1) / SC) ** 2 + (dH / SH) ** 2 + RT * ((C2 - C1) / SC) * (dH / SH));
}
const luminance = (hex) => {
  const [r, g, b] = rgb(hex).map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
};
const contrast = (a, b) => {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
};

test("palette: palette.json (server) is the same as TOKEN_COLORS (JS) and tokens.css", () => {
  const json = JSON.parse(read("shared", "palette.json"));
  assert.deepEqual(Object.keys(json.tokens).sort(), [...TOKEN_KINDS].sort());
  for (const kind of TOKEN_KINDS) {
    assert.match(json.tokens[kind], /^#[0-9A-Fa-f]{6}$/, `${kind} is #RRGGBB (the server rejects anything else)`);
    assert.equal(json.tokens[kind].toUpperCase(), TOKEN_COLORS[kind].toUpperCase(), `${kind}: palette.json and tokens.js differ`);
  }
  const css = read("shared", "tokens.css");
  for (const kind of TOKEN_KINDS) {
    const match = css.match(new RegExp(`--p-${kind}:\\s*(#[0-9A-Fa-f]{6})`));
    assert.ok(match, `tokens.css documents --p-${kind}`);
    assert.equal(match[1].toUpperCase(), TOKEN_COLORS[kind].toUpperCase(), `tokens.css --p-${kind}`);
  }
});

test("palette: no player colour can be mistaken for a property group", () => {
  const groups = Object.values(GROUP_COLORS);
  const groupMin = Math.min(...groups.flatMap((a, i) => groups.slice(i + 1).map((b) => de00(a, b))));
  for (const [kind, color] of Object.entries(TOKEN_COLORS)) {
    const nearest = Math.min(...groups.map((group) => de00(color, group)));
    assert.ok(nearest >= 18.5, `${kind} ${color} is only ${nearest.toFixed(1)} ΔE00 from a group colour`);
  }
  const players = Object.values(TOKEN_COLORS);
  for (let i = 0; i < players.length; i += 1) {
    for (let j = i + 1; j < players.length; j += 1) {
      assert.ok(de00(players[i], players[j]) > groupMin, `${players[i]} vs ${players[j]} closer than the groups are to each other`);
    }
  }
});

test("palette: chip initials are readable and playerColor follows the token", () => {
  for (const color of Object.values(TOKEN_COLORS)) assert.ok(contrast(color, inkOn(color)) >= 3, `${color} chip text contrast`);
  assert.equal(playerColor(null, { tokenKind: "key", color: "#d33f49" }), TOKEN_COLORS.key);
  assert.equal(playerColor(null, { color: "#d33f49" }), "#d33f49", "classic rooms keep the engine colour");
  assert.equal(initialOf("maya"), "M");
  assert.equal(initialOf(""), "?");
});

test("board: fan layouts shrink to fit and stay within the square", () => {
  assert.deepEqual(fanLayout(1, 69, 42).map((slot) => [slot.u, slot.r, slot.s]), [[0, 0, 1]]);
  for (let count = 1; count <= 8; count += 1) {
    const slots = fanLayout(count, 69, 42);
    assert.equal(slots.length, count);
    for (const slot of slots) {
      assert.ok(Math.abs(slot.u) + (42 * slot.s * 0.82) / 2 <= 69 / 2 + 0.5, `count ${count}: token pokes out of its square`);
      assert.ok(slot.s <= 1 && slot.s > 0.3);
    }
    if (count > 2) assert.ok(slots.some((slot) => slot.r > 0), "3+ tokens use a second row");
  }
  for (let count = 1; count <= 8; count += 1) {
    for (const slot of zoneLayout(count, 107, 43, 42)) {
      assert.ok(Math.abs(slot.dx) + (42 * slot.s * 0.86) / 2 <= 107 / 2 + 0.5);
      assert.ok(Math.abs(slot.dy) + (42 * slot.s * 0.94) / 2 <= 43 / 2 + 0.5);
    }
  }
  assert.equal(abbrevName({ type: "property", name: "Begonia Road" }), "BEG");
  assert.equal(abbrevName({ type: "railroad", name: "North Station Railroad" }), "N.RR");
});

// A Board wired to fake cells laid out like the 840 px CSS grid (no DOM needed for point()).
function fakeBoard(size = 840, options = {}) {
  const inner = size - 6;
  const gap = 1.5;
  const corner = options.compact ? 2 : 1.55;
  const fr = (inner - 10 * gap) / (9 + 2 * corner);
  const track = (n) => (n === 1 || n === 11 ? corner * fr : fr);
  const start = (n) => { let at = 0; for (let k = 1; k < n; k += 1) at += track(k) + gap; return at; };
  const node = () => ({ classList: { add() {}, toggle() {}, remove() {}, contains: () => false }, setAttribute() {}, dataset: {}, style: { setProperty() {} } });
  const root = node();
  root.clientWidth = inner;
  root.clientHeight = inner;
  root.ownerDocument = { createElement: node };
  root.replaceChildren = () => {};
  const board = new Board(root, { ...options, camera: false });
  board.cells = new Map();
  const cornerOf = { 0: "br", 10: "bl", 20: "tl", 30: "tr" };
  for (let index = 0; index < 40; index += 1) {
    const pos = cellPosition(index);
    board.cells.set(index, { offsetLeft: start(pos.col), offsetTop: start(pos.row), offsetWidth: track(pos.col), offsetHeight: track(pos.row), dataset: { side: pos.side, corner: cornerOf[index] } });
  }
  return { board, inner };
}

const overlap = (a, b) => Math.max(0, Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x)) * Math.max(0, Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y));

test("board: tokens never spill off the board and never sit on an edge square's text", () => {
  for (const compact of [false, true]) {
    const { board, inner } = fakeBoard(compact ? 370 : 840, { compact, tokenStyle: compact ? "chip" : "metal" });
    const base = board.tokenSize();
    for (let index = 0; index < 40; index += 1) {
      for (let count = 1; count <= 8; count += 1) {
        for (let slot = 0; slot < count; slot += 1) {
          const point = board.point(index, slot, count, { inJail: index === 10 && slot % 2 === 1 });
          const half = (base * point.scale) / 2;
          assert.ok(point.x - half >= -0.5 && point.x + half <= inner + 0.5 && point.y - half >= -0.5 && point.y + half <= inner + 0.5, `square ${index}, ${slot + 1}/${count} spills off the board (compact=${compact})`);
          if (index % 10 !== 0) {
            const box = board.rect(index);
            const token = { x: point.x - half * 0.8, y: point.y - half * 0.8, w: half * 1.6, h: half * 1.6 };
            assert.equal(overlap(token, box), 0, `square ${index}: token ${slot + 1}/${count} covers the square (compact=${compact})`);
          }
        }
      }
    }
    // Four players starting on PAYDAY: all inside the PAYDAY square, clear of the "PAYDAY" label (top part only).
    const go = board.rect(0);
    for (let slot = 0; slot < 4; slot += 1) {
      const point = board.point(0, slot, 4);
      assert.ok(point.x > go.x && point.x < go.x + go.w && point.y > go.y && point.y < go.y + go.h * 0.6, `PAYDAY slot ${slot} outside its zone`);
    }
    // Jailed tokens stand in the barred box, visitors outside it.
    const jail = board.rect(10);
    const inside = board.point(10, 0, 1, { inJail: true });
    const visiting = board.point(10, 0, 1);
    assert.ok(inside.x > jail.x + jail.w * 0.4 && inside.y < jail.y + jail.h * 0.6, "jailed token is in the box");
    assert.ok(visiting.y > jail.y + jail.h * 0.6, "visitor stands outside the box");
  }
});

test("board: mini deed cards mark selection without relying on colour", () => {
  const { state } = fixture("roll");
  const owned = Object.keys(state.properties).map(Number).find((index) => state.properties[index].ownerId);
  const plain = miniDeedHtml(state, owned);
  const picked = miniDeedHtml(state, owned, { selected: true, badge: "COMPLETES PINK", tag: "button", attrs: 'type="button"' });
  assert.doesNotMatch(plain, /is-on/);
  assert.match(picked, /class="mdeed is-on/);
  assert.match(picked, /mdeed__tick/);
  assert.match(picked, /^<button /);
  assert.match(picked, /COMPLETES PINK/);
});

test("insights: next-roll heat is graded and darkest on the 7", () => {
  const { state } = fixture("pad-turn");
  const heat = nextRollHeat(state, state.currentPlayerId);
  for (const entry of heat.squares) {
    assert.ok(entry.level > 0 && entry.level <= 1);
    assert.ok(entry.cost > 0 || entry.jail);
  }
  const seven = (state.players.find((player) => player.id === state.currentPlayerId).position + 7) % 40;
  const at7 = heat.squares.find((entry) => entry.index === seven);
  if (at7) assert.equal(at7.level, 1);
});

test("receipts: PAYDAY, tax and rent outcomes per player from the replay", () => {
  const replay = fixture("replay");
  const states = replay.replay.map((state) => ({ ...state, board: replay.board, groups: replay.groups }));
  const all = [];
  for (let index = 1; index < states.length; index += 1) all.push(...receiptsFor(states[index - 1], states[index]));
  assert.ok(all.some((receipt) => receipt.kind === "go" && receipt.amount === 200 && receipt.label === "PAYDAY"), "PAYDAY +$200");
  assert.ok(all.some((receipt) => receipt.kind === "tax" && receipt.amount === -200 && /STAMP DUTY/.test(receipt.label)), "Stamp Duty −$200");
  const rents = all.filter((receipt) => receipt.kind === "rent");
  assert.ok(rents.length >= 2);
  assert.equal(rents.reduce((sum, receipt) => sum + receipt.amount, 0), 0, "every rent payment has a matching receipt for the owner");
  assert.ok(all.some((receipt) => receipt.kind === "trade"), "accepted trade shows on both sides");
  assert.equal(new Set(all.map((receipt) => receipt.key)).size, all.length, "keys are unique");
});

test("owner chips and chip tokens show the token's silhouette, so 'Ana' and 'Alpha' never look alike", () => {
  assert.deepEqual(Object.keys(TOKEN_GLYPHS).sort(), [...TOKEN_KINDS].sort(), "a glyph for every token");
  assert.equal(new Set(Object.values(TOKEN_GLYPHS)).size, TOKEN_KINDS.length, "all six glyphs differ");
  const state = structuredClone(fixture("pad-turn").state);
  const owned = Object.entries(state.properties).find(([, prop]) => prop.ownerId);
  const owner = state.players.find((player) => player.id === owned[1].ownerId);
  // A Board whose cells are just enough fake DOM for update(): one owned square with an owner flag.
  const chipNode = { innerHTML: "", textContent: "" };
  const flag = { hidden: true, dataset: {}, style: { setProperty() {} }, querySelector: () => chipNode };
  const cell = { classList: { toggle() {}, add() {}, remove() {} }, setAttribute() {}, querySelector: (selector) => (selector === ".cell__owner" ? flag : null) };
  const node = () => ({ classList: { add() {}, toggle() {}, remove() {} }, setAttribute() {}, dataset: {}, style: { setProperty() {} } });
  const root = node();
  root.ownerDocument = { createElement: node };
  const board = new Board(root, { camera: false });
  board.built = true;
  board.cells = new Map([[Number(owned[0]), cell]]);
  board.update(state);
  assert.equal(flag.hidden, false);
  assert.equal(chipNode.innerHTML, tokenGlyphSvg(tokenKindOf(state, owner)));
  assert.doesNotMatch(chipNode.innerHTML, new RegExp(`>${initialOf(owner.name)}<`));
  const chipToken = new Board(root, { camera: false, tokenStyle: "chip" }).tokenBody(state, owner, tokenKindOf(state, owner));
  assert.match(chipToken, /class="tok-chip"[^>]*><svg class="glyph-svg/);
  // Player chips keep the initial unless asked for the glyph (the phone can opt in).
  assert.match(playerChipHtml(state, owner), new RegExp(`>${initialOf(owner.name)}</span>$`));
  assert.match(playerChipHtml(state, owner, { glyph: true }), /pchip--glyph[^>]*><svg class="glyph-svg/);
});
