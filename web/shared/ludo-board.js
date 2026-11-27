// The Ludo board, drawn as one SVG from the state: geometry of the 4-arm cross and the 6-arm board,
// the cells, the yards, home, and every piece. Pure (state in, markup out), shared by the table
// screen and the phone, and checked in Node (web/tests/ludo.check.mjs). Styles: web/shared/ludo.css.
//
// Numbering (the server's, internal/ludo/board.go): every arm is three columns of six squares.
// Ring square i belongs to arm floor(i / 13); within the arm, squares 0–5 run outwards up the
// first column, 6 is the tip, 7–12 run back in down the second column. An arm's start square is
// its local 8 and its star its local 3 (eight steps on from the previous arm's start); its home
// column is the middle column, five squares, and home is its triangle of the centre. Its yard sits
// clockwise of it, between this arm and the next.
//
// Geometry is in board units (one square = 1) with the centre at 0,0. Arm 0 points down; arm a is
// turned a·360/N degrees clockwise, so play runs clockwise as on any Ludo board. The inner end of
// every arm sits 1.5 / tan(180°/N) from the centre: 1.5 on the cross (a 3 × 3 home square), 2.6 on
// the six-arm board (a hexagon).
//
// Cells are named as the server names them: "r12" ring square, "c2.4" arm 2's home column square 4,
// "h2" arm 2's home, "y2.1" arm 2's yard spot for piece 1.
//
// Every piece is a <g data-key="lp-<player>-<index>"> moved by its transform, so morph updates it in
// place and the hop animation (ludo-fx.js) can walk it from square to square.

import { escapeHtml } from "./format.js";
import { TOKEN_GLYPHS, inkOn, playerColor, tokenKindOf } from "./tokens.js";

const e = escapeHtml;
export const ARM_SQUARES = 13;
export const COLUMN = 5;

const cache = new Map();

function rotate([x, y], angle) {
  const c = Math.cos(angle);
  const s = Math.sin(angle);
  return [x * c - y * s, x * s + y * c];
}

const round = (value) => Math.round(value * 1000) / 1000;

// Board geometry for 4 or 6 arms (cached): cell centres, the outlines needed to draw them, yards,
// home triangles and the extent of the board.
export function geometry(arms = 4) {
  const n = Number(arms) === 6 ? 6 : 4;
  if (cache.has(n)) return cache.get(n);
  const d0 = 1.5 / Math.tan(Math.PI / n);
  const step = (2 * Math.PI) / n;
  const cells = new Map();
  const at = (arm, col, row) => {
    const [x, y] = rotate([col - 1, d0 + row + 0.5], arm * step);
    return { x: round(x), y: round(y), angle: (arm * 360) / n, arm };
  };
  const ringCR = (local) => (local <= 5 ? [2, local] : local === 6 ? [1, 5] : [0, 12 - local]);
  for (let arm = 0; arm < n; arm += 1) {
    for (let local = 0; local < ARM_SQUARES; local += 1) {
      const [col, row] = ringCR(local);
      cells.set(`r${arm * ARM_SQUARES + local}`, { ...at(arm, col, row), kind: "ring", local });
    }
    for (let d = 1; d <= COLUMN; d += 1) cells.set(`c${arm}.${d}`, { ...at(arm, 1, COLUMN - d), kind: "column", step: d });
  }
  // Home: each arm's triangle of the centre (the arm's inner edge to the centre point).
  const triangles = [];
  for (let arm = 0; arm < n; arm += 1) {
    const a = rotate([-1.5, d0], arm * step);
    const b = rotate([1.5, d0], arm * step);
    triangles.push([[0, 0], a, b].map(([x, y]) => [round(x), round(y)]));
    // Pieces home sit out towards the arm, clear of the die in the middle.
    const [hx, hy] = rotate([0, d0 * (n === 4 ? 0.8 : 0.74)], arm * step);
    cells.set(`h${arm}`, { x: round(hx), y: round(hy), angle: (arm * 360) / n, arm, kind: "home" });
  }
  // Yards: between arm a and arm a+1. On the cross a 6 × 6 corner square; on six arms a disc in
  // the gap between two arms, pushed out until it clears both.
  const yards = [];
  const yardR = n === 4 ? 3 : 2.25;
  const yardDist = n === 4 ? Math.SQRT2 * 4.5 : 7.7;
  for (let arm = 0; arm < n; arm += 1) {
    const [x, y] = rotate([0, yardDist], arm * step + step / 2);
    const yard = { arm, x: round(x), y: round(y), r: yardR, spots: [] };
    const gap = n === 4 ? 1.15 : 0.82;
    for (const [dx, dy] of [[-1, -1], [1, -1], [-1, 1], [1, 1]]) yard.spots.push({ x: round(x + dx * gap), y: round(y + dy * gap) });
    yards.push(yard);
    yard.spots.forEach((spot, slot) => cells.set(`y${arm}.${slot}`, { ...spot, arm, kind: "yard" }));
  }
  const extent = n === 4 ? 7.5 : round(yardDist + yardR + 0.35);
  const geom = { arms: n, d0: round(d0), step, cells, triangles, yards, extent, ring: n * ARM_SQUARES };
  cache.set(n, geom);
  return geom;
}

// Where a cell is (board units), or null for a cell this board doesn't have.
export function cellPoint(geom, cell) {
  const found = geom.cells.get(String(cell));
  return found ? { x: found.x, y: found.y } : null;
}

// The ring square a local square of an arm is.
export const startSquare = (arm) => arm * ARM_SQUARES + 8;
export const starSquare = (arm) => arm * ARM_SQUARES + 3;

// ---------------------------------------------------------------------------------------------
// Colours

function luminance(hex) {
  const value = String(hex || "").replace("#", "");
  if (!/^[0-9a-f]{6}$/i.test(value)) return 0.5;
  const channel = (index) => {
    const c = parseInt(value.slice(index, index + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(0) + 0.7152 * channel(2) + 0.0722 * channel(4);
}

function mix(hex, other, share) {
  const a = String(hex).replace("#", "");
  const b = String(other).replace("#", "");
  const out = [0, 2, 4].map((i) => Math.round(parseInt(a.slice(i, i + 2), 16) * (1 - share) + parseInt(b.slice(i, i + 2), 16) * share));
  return `#${out.map((v) => v.toString(16).padStart(2, "0")).join("")}`.toUpperCase();
}

// The colour a player's areas are painted in on the ivory board. A near-white colour (a palette override) would vanish,
// so very light colours are toned down to a warm grey that still reads as theirs.
export function areaColor(color) {
  return luminance(color) > 0.7 ? mix(color, "#8F8775", 0.42) : color;
}

// Each arm's owner (or null), for colouring.
export function armOwners(state) {
  const arms = Number(state?.board?.arms) === 6 ? 6 : 4;
  const owners = Array(arms).fill(null);
  for (const player of state?.players || []) {
    if (Number.isInteger(player.arm) && player.arm >= 0 && player.arm < arms && player.status !== "left") owners[player.arm] = player;
  }
  return owners;
}

// ---------------------------------------------------------------------------------------------
// Pieces

// Every piece on the board with its point: pieces sharing a cell (stacked, or several players on a
// safe square) are spread in a small cluster and drawn smaller.
export function piecePoints(state) {
  const geom = geometry(state?.board?.arms);
  const byCell = new Map();
  for (const player of state?.players || []) {
    if (player.status === "left") continue;
    for (const piece of player.pieces || []) {
      const list = byCell.get(piece.cell) || [];
      list.push({ player, piece });
      byCell.set(piece.cell, list);
    }
  }
  const out = [];
  for (const [cell, list] of byCell) {
    const base = cellPoint(geom, cell);
    if (!base) continue;
    const spread = list.length === 1 ? [[0, 0]] : list.length === 2 ? [[-0.2, 0], [0.2, 0]] : list.length === 3 ? [[0, -0.2], [-0.2, 0.16], [0.2, 0.16]]
      : list.map((_, index) => { const a = (index / list.length) * Math.PI * 2; return [0.24 * Math.cos(a), 0.24 * Math.sin(a)]; });
    const scale = list.length === 1 ? 1 : list.length === 2 ? 0.78 : 0.66;
    list.forEach(({ player, piece }, index) => {
      out.push({ player, piece, cell, x: round(base.x + spread[index][0]), y: round(base.y + spread[index][1]), scale, stack: list.length });
    });
  }
  return out;
}

function glyphPaths(state, player) {
  return TOKEN_GLYPHS[tokenKindOf(state, player)] || "";
}

// One piece: a disc in the player's colour with a white rim and their token's silhouette, so it
// never relies on colour alone.
export function pieceSvg(state, item, { movable = false, focused = false, size = 1 } = {}) {
  const { player, piece } = item;
  const color = playerColor(state, player);
  const ink = inkOn(color);
  const s = round(item.scale * size);
  const classes = ["lp", movable ? "is-movable" : "", focused ? "is-focus" : "", player.isTurn ? "is-turn" : ""].filter(Boolean).join(" ");
  return `<g class="${classes}" data-key="lp-${e(player.id)}-${piece.index}" data-player="${e(player.id)}" data-piece="${piece.index}" data-cell="${e(item.cell)}" transform="translate(${item.x} ${item.y})" style="--pc:${color}">
    <g class="lp__body" transform="scale(${s})">
      <circle class="lp__halo" r="0.62"/>
      <ellipse class="lp__shadow" cx="0.04" cy="0.12" rx="0.4" ry="0.36"/>
      <circle class="lp__disc" r="0.38" fill="${color}"/>
      <circle class="lp__rim" r="0.3"/>
      <svg class="lp__glyph" x="-0.22" y="-0.22" width="0.44" height="0.44" viewBox="0 0 24 24" fill="${ink}" style="color:${ink}">${glyphPaths(state, player)}</svg>
      ${movable ? '<circle class="lp__ring" r="0.5"/>' : ""}
    </g>
  </g>`;
}

// ---------------------------------------------------------------------------------------------
// The board

const STAR = (x, y, r) => {
  const pts = [];
  for (let i = 0; i < 10; i += 1) {
    const a = -Math.PI / 2 + (i * Math.PI) / 5;
    const rr = i % 2 ? r * 0.45 : r;
    pts.push(`${round(x + rr * Math.cos(a))},${round(y + rr * Math.sin(a))}`);
  }
  return pts.join(" ");
};

function squareSvg(cell, cls, fill = "") {
  return `<rect class="${cls}" x="-0.5" y="-0.5" width="1" height="1" transform="translate(${cell.x} ${cell.y}) rotate(${cell.angle})"${fill ? ` style="fill:${fill}"` : ""}/>`;
}

// The pieces that glow: the player to act's movable ones. A phone shows only its own (everyone
// else's turn is theirs to watch); the table screen shows everybody's (showAll), so the room can
// follow, and a keyboard or controller seat there also gets a focused piece and its landing square.
export function movableSet(state, { viewerId = null, local = [], showAll = false } = {}) {
  if (state?.phase !== "move" || !Array.isArray(state.moves)) return new Set();
  const mine = viewerId && state.turnId === viewerId;
  if (!mine && !showAll && !local.includes(state.turnId)) return new Set();
  return new Set(state.moves.map((move) => move.piece));
}

// The whole board as SVG. opts: viewerId (the phone's player: their movable pieces glow),
// showAll (the table screen: whoever is to move, their pieces glow), local (player ids playing on
// this screen: keyboard and controller seats), focus (the piece a keyboard or controller has
// selected: its landing square is marked), label (accessible name).
export function boardSvg(state, opts = {}) {
  const geom = geometry(state?.board?.arms);
  const owners = armOwners(state);
  const safe = new Set(state?.board?.safe || []);
  const ext = geom.extent;
  const parts = [];
  // Ground: an ivory board with a felt-dark rim.
  if (geom.arms === 4) parts.push(`<rect class="lb-ground" x="${-ext}" y="${-ext}" width="${2 * ext}" height="${2 * ext}" rx="0.45"/>`);
  else {
    const pts = Array.from({ length: 6 }, (_, i) => rotate([0, ext + 0.4], (i + 0.5) * geom.step)).map(([x, y]) => `${round(x)},${round(y)}`).join(" ");
    parts.push(`<polygon class="lb-ground" points="${pts}" stroke-linejoin="round"/>`);
  }
  // Yards.
  for (const yard of geom.yards) {
    const owner = owners[yard.arm];
    const fill = owner ? areaColor(playerColor(state, owner)) : "";
    const turn = owner && owner.id === state?.turnId ? " is-turn" : "";
    if (geom.arms === 4) {
      const half = yard.r - 0.18;
      parts.push(`<g class="lb-yard${owner ? " is-owned" : ""}${turn}" data-arm="${yard.arm}"><rect class="lb-yard__bed" x="${round(yard.x - half)}" y="${round(yard.y - half)}" width="${round(2 * half)}" height="${round(2 * half)}" rx="0.5"${fill ? ` style="fill:${fill}"` : ""}/><rect class="lb-yard__inner" x="${round(yard.x - 2.05)}" y="${round(yard.y - 2.05)}" width="4.1" height="4.1" rx="0.4"/>`);
    } else {
      parts.push(`<g class="lb-yard${owner ? " is-owned" : ""}${turn}" data-arm="${yard.arm}"><circle class="lb-yard__bed" cx="${yard.x}" cy="${yard.y}" r="${yard.r}"${fill ? ` style="fill:${fill}"` : ""}/><circle class="lb-yard__inner" cx="${yard.x}" cy="${yard.y}" r="${round(yard.r - 0.42)}"/>`);
    }
    // A spot per piece (2, 3 or 4 each, by the house rules).
    for (const spot of yard.spots.slice(0, Math.min(4, Math.max(2, Number(state?.settings?.pieces) || 4)))) parts.push(`<circle class="lb-spot" cx="${spot.x}" cy="${spot.y}" r="0.42"${fill ? ` style="stroke:${fill}"` : ""}/>`);
    parts.push("</g>");
  }
  // Ring, starts, stars and home columns.
  for (const [id, cell] of geom.cells) {
    if (cell.kind === "ring") {
      const square = Number(id.slice(1));
      const arm = Math.floor(square / ARM_SQUARES);
      const local = square % ARM_SQUARES;
      const owner = owners[arm];
      if (local === 8) {
        const fill = owner ? areaColor(playerColor(state, owner)) : "";
        parts.push(squareSvg(cell, `lb-sq lb-sq--start${owner ? " is-owned" : ""}`, fill));
        // An arrow showing which way pieces come out.
        const [ax, ay] = rotate([0, -0.24], (cell.angle * Math.PI) / 180);
        const [bx, by] = rotate([0.2, 0.12], (cell.angle * Math.PI) / 180);
        const [cx, cy] = rotate([-0.2, 0.12], (cell.angle * Math.PI) / 180);
        parts.push(`<polygon class="lb-arrow${owner ? " is-owned" : ""}" points="${round(cell.x + ax)},${round(cell.y + ay)} ${round(cell.x + bx)},${round(cell.y + by)} ${round(cell.x + cx)},${round(cell.y + cy)}"${owner ? ` style="fill:${inkOn(areaColor(playerColor(state, owner)))}"` : ""}/>`);
      } else {
        parts.push(squareSvg(cell, "lb-sq"));
        if (local === 3 && safe.has(square)) parts.push(`<polygon class="lb-star" points="${STAR(cell.x, cell.y, 0.34)}"/>`);
      }
      if (local === 8 && safe.has(square)) parts.push(`<circle class="lb-safe-ring" cx="${cell.x}" cy="${cell.y}" r="0.44"/>`);
    } else if (cell.kind === "column") {
      const owner = owners[cell.arm];
      parts.push(squareSvg(cell, `lb-sq lb-sq--col${owner ? " is-owned" : ""}`, owner ? areaColor(playerColor(state, owner)) : ""));
    }
  }
  // Home: a triangle per arm, and a gold medallion in the middle.
  geom.triangles.forEach((tri, arm) => {
    const owner = owners[arm];
    parts.push(`<polygon class="lb-home${owner ? " is-owned" : ""}" points="${tri.map((p) => p.join(",")).join(" ")}"${owner ? ` style="fill:${areaColor(playerColor(state, owner))}"` : ""}/>`);
  });
  parts.push(`<circle class="lb-medal" r="${round(geom.d0 * 0.34)}"/>`);
  // Where the focused piece would land.
  const movable = movableSet(state, opts);
  const focusMove = Number.isInteger(opts.focus) && movable.has(opts.focus) ? state.moves.find((move) => move.piece === opts.focus) : null;
  if (focusMove) {
    const to = cellPoint(geom, focusMove.cell);
    if (to) parts.push(`<g class="lb-target" transform="translate(${to.x} ${to.y})"><circle r="0.5"/><circle class="lb-target__dot" r="0.12"/></g>`);
    for (const cell of focusMove.path.slice(0, -1)) {
      const p = cellPoint(geom, cell);
      if (p) parts.push(`<circle class="lb-trail" cx="${p.x}" cy="${p.y}" r="0.09"/>`);
    }
  }
  // Pieces last (on top), the movable ones above the rest.
  const items = piecePoints(state);
  const turnPlayer = state?.turnId;
  const pieceMarkup = items
    .sort((a, b) => (movable.has(a.piece.index) && a.player.id === turnPlayer) - (movable.has(b.piece.index) && b.player.id === turnPlayer))
    .map((item) => {
      const canMove = item.player.id === turnPlayer && movable.has(item.piece.index);
      return pieceSvg(state, item, { movable: canMove, focused: canMove && opts.focus === item.piece.index });
    });
  const label = opts.label || `Ludo board, ${geom.arms} arms`;
  return `<svg class="lboard lboard--${geom.arms}" viewBox="${-ext - 0.3} ${-ext - 0.3} ${round(2 * ext + 0.6)} ${round(2 * ext + 0.6)}" role="img" aria-label="${e(label)}" data-arms="${geom.arms}">
    <g class="lb-static">${parts.join("")}</g>
    <g class="lb-pieces">${pieceMarkup.join("")}</g>
  </svg>`;
}
