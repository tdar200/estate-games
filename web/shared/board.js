// The 40-square board, shared by the PC table and the phones. Classic colour bars on the inner
// edge, an owner flag (band + round chip with the owner's token silhouette, in the player palette from
// tokens.js) on the outer edge, houses/hotels on the colour bar, hatched mortgages, metal tokens
// (PC) or round initial tokens (phone) that stand in a lane just inside each square so they never
// cover names, prices or owner chips. Cells are built once and updated in place so animations have
// stable targets.
//
// ── Board API (stable; the phone app consumes this) ─────────────────────────────────────────────
// Load /shared/tokens.css and /shared/board.css, call ensureMetalDefs() (tokens.js) once, then:
//
//   import { Board } from "/shared/board.js";
//   const board = new Board(rootEl, {
//     compact: false,       // true → phone board: no names or rent; colour bars, owner chips, houses
//                           //   and icons only; round tokens; root becomes a square, clipped viewport
//     labels: "none",       // compact only: "short" adds 3-letter names ("BEG", "MAP", …)
//     tokenStyle: null,     // "metal" (PC figures) | "chip" (round, token glyph); default: chip if compact
//     youPlayerId: null,    // gold ring + "YOU" tag on this player's token, gold edge on their deeds
//     camera: null,         // follow-cam wrapper for focusOn(); default: on when compact
//   });
//   board.update(state, { currentSquare })                 // owners, houses, mortgages, highlight
//   board.placeTokens(state, { skip, positions, animate }) // fan tokens out per square
//   await board.hop(playerId, path, { stepMs, follow, zoom, onStep, release })
//                                     // square-by-square hop (motion.hopToken), landing glow,
//                                     // optional follow-cam; resolves when the token has landed
//   board.highlightSquares(squares, kind, { ms })          // squares: [3, 5] or [{ index, level }]
//        kinds styled in board.css: "risk" (red wash, level 0–1 → opacity), "land" (gold glow),
//        "pick" (gold ring, e.g. squares in a trade), "target" (dashed gold, card destination)
//   board.clearHighlights(kind?)   board.landGlow(index, ms)
//   const off = board.onSquareTap((index, { space, event }) => …)  // cells become buttons
//   board.setYou(playerId)
//   board.focusOn(index, { zoom: 1.6, ms: 350 })   board.resetCamera()   // smooth CSS transform
//   board.setStage(html)   board.clearStage()   board.stage   // centre "stage" slot (dice, deed,
//                                                            // card…); board.slot is the same node
//   board.cell(index)   board.tokenEl(playerId)   board.point(index, slot, count)
//   board.tokenSize()   board.fx (effects layer: chips, notes, stamps)   board.destroy()
// Player colours: always use playerColor(state, player) from tokens.js (= the token's palette
// colour, the same value the server sends at party tables), never a hard-coded seat colour.
//
// Example (phone, 360–430 px wide):
//   const board = new Board(document.querySelector("#board"), { compact: true, youPlayerId: me.id });
//   board.update(state); board.placeTokens(state);
//   board.highlightSquares(nextRollHeat(state, me.id).squares, "risk");   // insights.js
//   await board.hop(event.playerId, event.path, { follow: true });   // zooms 1.6× and follows
//   board.setStage(diceHtml(event.dice, { className: "die--sm" }));
//   board.onSquareTap((index) => openDeedSheet(index));
// Demo of all of the above at 360/390/430 px: /shared/board-demo.html

import { GROUP_COLORS, GROUP_INK, escapeHtml, money, playerById, spacePhoto } from "./format.js";
import { propState, rentLabel } from "./insights.js";
import { hopToken, prefersReducedMotion } from "./motion.js";
import { inkOn, playerColor, tokenGlyphSvg, tokenKindOf, tokenSvg } from "./tokens.js";

const CORNERS = {
  go: { name: "PAYDAY", ink: "#1C8A47", sub: "COLLECT $200", short: "PAYDAY", corner: "br" },
  jail: { name: "JAIL", ink: "#15191E", sub: "", short: "JAIL", corner: "bl" },
  parking: { name: "CITY PARK", ink: "#15191E", sub: "FREE REST", short: "PARK", corner: "tl" },
  goToJail: { name: "BUSTED", ink: "#C8202A", sub: "OFF TO JAIL", short: "BUSTED", corner: "tr" },
};

// Post (deck "chest"): an envelope. Headlines (deck "chance"): a newspaper.
const CHEST_ICON = '<svg class="cell__icon" width="22" height="20" viewBox="0 0 24 22" aria-hidden="true"><rect x="2" y="5" width="20" height="14" rx="1.5" fill="#2F6FB5" stroke="#15191E" stroke-width="1.6"/><path d="M2.6 5.6 L12 13 L21.4 5.6" fill="#3C86D1" stroke="#15191E" stroke-width="1.6" stroke-linejoin="round"/></svg>';
const NEWS_ICON = '<svg class="cell__icon" width="22" height="20" viewBox="0 0 24 22" aria-hidden="true"><rect x="2" y="3" width="20" height="16" rx="1.5" fill="#FFF6EA" stroke="#15191E" stroke-width="1.6"/><rect x="5" y="6" width="14" height="3.2" fill="#E97A1F"/><path d="M5 12h6M5 15.5h6" stroke="#15191E" stroke-width="1.5"/><rect x="13" y="11" width="6" height="5" fill="#F08A2B" stroke="#15191E" stroke-width="1.2"/></svg>';
const RAIL_ICON = '<svg class="cell__icon" width="24" height="20" viewBox="0 0 24 20" aria-hidden="true"><rect x="4" y="2" width="16" height="12" rx="3" fill="#15191E"/><rect x="7" y="4" width="10" height="5" rx="1" fill="#DCE6EE"/><circle cx="8" cy="17" r="2" fill="#15191E"/><circle cx="16" cy="17" r="2" fill="#15191E"/></svg>';
const BOLT_ICON = '<svg class="cell__icon cell__icon--util" width="18" height="20" viewBox="0 0 18 20" aria-hidden="true"><path d="M10 1 L2 12 H8 L6 19 L16 7 H10 Z" fill="#F2C14E" stroke="#15191E" stroke-width="1.5" stroke-linejoin="round"/></svg>';
const TAP_ICON = '<svg class="cell__icon cell__icon--util" width="18" height="20" viewBox="0 0 18 20" aria-hidden="true"><path d="M9 2 C9 2 3 9 3 13 A6 6 0 0 0 15 13 C15 9 9 2 9 2 Z" fill="#3C86D1" stroke="#15191E" stroke-width="1.5"/></svg>';
const TAX_ICON = '<svg class="cell__icon cell__icon--tax" width="20" height="20" viewBox="0 0 20 20" aria-hidden="true"><circle cx="10" cy="10" r="8.5" fill="#F2C14E" stroke="#15191E" stroke-width="1.5"/><text x="10" y="14.5" text-anchor="middle" font-family="Oswald, Arial Narrow, sans-serif" font-weight="700" font-size="12" fill="#15191E">$</text></svg>';

// Grid position of each square (PAYDAY bottom-right, play runs clockwise from the viewer's left).
export function cellPosition(index) {
  if (index <= 10) return { row: 11, col: 11 - index, side: "bottom" };
  if (index < 20) return { row: 11 - (index - 10), col: 1, side: "left" };
  if (index <= 30) return { row: 1, col: index - 19, side: "top" };
  return { row: index - 29, col: 11, side: "right" };
}

function isCorner(index) {
  return index % 10 === 0;
}

// Short label for compact boards: "Begonia Road" → "BEG", "North Station Railroad" → "N.RR".
export function abbrevName(space) {
  if (!space) return "";
  if (space.type === "railroad") return `${String(space.name || "").trim()[0] || ""}.RR`.toUpperCase();
  if (space.type === "chance") return "NEWS";
  if (space.type === "chest") return "POST";
  if (space.type === "tax") return "TAX";
  const word = String(space.name || "").replace(/[^A-Za-z ]/g, "").trim().split(/\s+/)[0] || "";
  return word.slice(0, 3).toUpperCase();
}

// How `count` tokens sharing a square fan out. `along` is the space available along the edge, `base`
// the full token size. Returns [{ u, r, s, rows }]: offset along the edge from its middle (px), row
// (0 nearest the square; later rows stand further into the board) and scale. Pure, unit-tested.
export function fanLayout(count, along, base) {
  const n = Math.max(1, count);
  const cols = n <= 2 ? n : n <= 4 ? 2 : 3;
  const rows = Math.ceil(n / cols);
  let s = Math.min(1, (along * 0.96) / (base * 0.82 * cols));
  if (n > 2) s = Math.min(s, 0.78);
  if (n > 4) s = Math.min(s, 0.66);
  const step = base * s * 0.82;
  const slots = [];
  for (let index = 0; index < n; index += 1) {
    const r = Math.floor(index / cols);
    const inRow = Math.min(cols, n - r * cols);
    const c = index % cols;
    slots.push({ u: (c - (inRow - 1) / 2) * step, r, s, rows });
  }
  return slots;
}

// Best-fit grid for `count` tokens in a w × h zone (corner squares): picks the column count that
// gives the biggest tokens. Returns [{ dx, dy, s }] offsets from the zone centre. Pure, unit-tested.
export function zoneLayout(count, w, h, base, { round = false } = {}) {
  const n = Math.max(1, count);
  const fx = round ? 1 : 0.86;
  const fy = round ? 1 : 0.94;
  let best = null;
  for (let cols = 1; cols <= n; cols += 1) {
    const rows = Math.ceil(n / cols);
    const s = Math.min(1, w / (cols * base * fx), h / (rows * base * fy));
    if (!best || s > best.s + 1e-6) best = { cols, rows, s };
  }
  const { cols, rows, s } = best;
  const dx = Math.min(w / cols, base * s * 1.1);
  const dy = Math.min(h / rows, base * s * fy);
  return Array.from({ length: n }, (_, index) => {
    const r = Math.floor(index / cols);
    const inRow = Math.min(cols, n - r * cols);
    return { dx: ((index % cols) - (inRow - 1) / 2) * dx, dy: (r - (rows - 1) / 2) * dy, s };
  });
}

function cornerMarkup(space, compact) {
  const corner = CORNERS[space.type];
  if (space.type === "jail") {
    return `<div class="jailbox" aria-hidden="true"><span class="jailbox__bars"></span><span class="jailbox__label">${compact ? "JAIL" : "IN JAIL"}</span></div>
      <div class="corner__visit">JAIL</div>`;
  }
  const sub = space.type === "parking" || (!compact && corner.sub) ? `<span class="corner__sub">${compact ? "" : corner.sub}</span>` : "";
  return `<div class="corner__label" style="color:${corner.ink}"><span class="corner__name">${compact ? corner.short : corner.name}</span>${sub}</div>`;
}

function cellMarkup(space, compact) {
  const pos = cellPosition(space.index);
  const corner = CORNERS[space.type];
  const style = `grid-row:${pos.row};grid-column:${pos.col}`;
  if (corner && isCorner(space.index)) {
    return `<div class="cell cell--corner cell--${space.type}" data-index="${space.index}" data-side="${pos.side}" data-corner="${corner.corner}" style="${style}">
      ${cornerMarkup(space, compact)}
      <div class="cell__current" aria-hidden="true"></div>
    </div>`;
  }
  const strip = space.type === "property"
    ? `<div class="cell__strip" style="background:${GROUP_COLORS[space.group] || "#999"}"><span class="cell__houses"></span></div>`
    : "";
  let icon = "";
  if (space.type === "chance") icon = NEWS_ICON;
  else if (space.type === "chest") icon = CHEST_ICON;
  else if (space.type === "railroad") icon = RAIL_ICON;
  else if (space.type === "utility" && compact) icon = space.index === 28 || /water|reservoir/i.test(space.name) ? TAP_ICON : BOLT_ICON;
  else if (space.type === "tax" && compact) icon = TAX_ICON;
  const name = compact ? abbrevName(space) : space.name;
  return `<div class="cell cell--${space.type}" data-index="${space.index}" data-side="${pos.side}" style="${style}">
    ${strip}
    <div class="cell__owner" aria-hidden="true" hidden><span class="cell__chip"></span></div>
    ${icon}
    <div class="cell__name">${escapeHtml(name)}</div>
    <div class="cell__sub"></div>
    <div class="cell__current" aria-hidden="true"></div>
  </div>`;
}

function baseSub(space) {
  if (space.type === "tax") return `PAY ${money(space.amount)}`;
  if (space.price) return money(space.price);
  return "";
}

// Token zones inside corner squares, as fractions of the square (x, y, w, h). Corner labels sit
// in the other part, so tokens never hide "PAYDAY" or "JAIL". The jail square has two zones: the barred
// box towards the board centre (in jail) and the outer strip (passing through).
const CORNER_ZONES = {
  br: { x: 0, y: 0.13, w: 1, h: 0.4 },
  bl: { x: 0, y: 0.62, w: 0.92, h: 0.38 },
  tl: { x: 0, y: 0.5, w: 1, h: 0.5 },
  tr: { x: 0, y: 0.5, w: 1, h: 0.5 },
  jail: { x: 0.42, y: 0.15, w: 0.58, h: 0.45 },
};

// Inward direction (towards the board centre) and along-edge direction for each side.
const SIDES = {
  bottom: { n: [0, -1], t: [1, 0] },
  top: { n: [0, 1], t: [1, 0] },
  left: { n: [1, 0], t: [0, 1] },
  right: { n: [-1, 0], t: [0, 1] },
};

export class Board {
  constructor(root, options = {}) {
    this.root = root;
    this.options = { compact: false, labels: "none", tokenStyle: null, youPlayerId: null, camera: null, ...options };
    this.compact = Boolean(this.options.compact);
    this.tokenStyle = this.options.tokenStyle || (this.compact ? "chip" : "metal");
    this.camera = Boolean(this.options.camera ?? this.compact);
    this.youId = this.options.youPlayerId || null;
    this.state = null;
    this.tokens = new Map();
    this.hidden = new Set();
    this.highlights = new Map();
    this.timers = new Map();
    this.view = { x: 0, y: 0, z: 1 };
    if (this.camera) {
      root.classList.add("board-viewport");
      this.grid = root.ownerDocument.createElement("div");
      root.replaceChildren(this.grid);
    } else {
      this.grid = root;
    }
    this.grid.classList.add("board");
    this.grid.classList.toggle("board--compact", this.compact);
    this.grid.classList.toggle("board--labels", this.compact && this.options.labels === "short");
    this.grid.dataset.tokens = this.tokenStyle;
    root.setAttribute("role", "group");
    root.setAttribute("aria-label", "Game board");
    if (typeof ResizeObserver === "function") {
      this.resizer = new ResizeObserver(() => this.relayout());
      this.resizer.observe(this.grid);
    }
  }

  get stage() {
    return this.slot || null;
  }

  build(state) {
    const compact = this.compact;
    const decks = compact ? "" : `<div class="deck deck--chance" aria-hidden="true"><span></span><span></span><div>HEADLINES</div></div>
        <div class="deck deck--chest" aria-hidden="true"><span></span><span></span><div>POST</div></div>`;
    this.grid.innerHTML = `${state.board.map((space) => cellMarkup(space, compact)).join("")}
      <div class="board__center" style="grid-row:2/11;grid-column:2/11">
        ${decks}
        <div class="wordmark" aria-hidden="true"><span class="wordmark__rule"></span><span class="wordmark__text">ESTATE</span><span class="wordmark__rule"></span></div>
        <div class="board__slot" aria-live="polite"></div>
      </div>
      <div class="board__tokens" aria-hidden="true"></div>
      <div class="board__fx" aria-hidden="true"></div>`;
    this.cells = [...this.grid.querySelectorAll(".cell")].reduce((map, el) => map.set(Number(el.dataset.index), el), new Map());
    this.center = this.grid.querySelector(".board__center");
    this.slot = this.grid.querySelector(".board__slot");
    this.tokenLayer = this.grid.querySelector(".board__tokens");
    this.fx = this.grid.querySelector(".board__fx");
    this.tokens = new Map();
    this.highlights = new Map();
    this.built = true;
    if (this.tapHandler) this.markTappable(true);
    this.measure();
  }

  cell(index) {
    return this.cells?.get(index) || null;
  }

  // Width of one ordinary square (the grid is 9 squares plus two wider corners).
  unit() {
    const width = this.grid.clientWidth || 0;
    return width / (9 + 2 * (this.compact ? 2 : 1.55));
  }

  measure() {
    const unit = this.unit();
    if (unit > 0) this.grid.style.setProperty("--u", `${unit.toFixed(2)}px`);
    this.grid.style.setProperty("--tok", `${this.tokenSize()}px`);
  }

  // Ownership flags, rent, buildings, mortgages and the current-square highlight.
  update(state, { currentSquare = null } = {}) {
    if (!this.built) this.build(state);
    this.state = state;
    this.currentSquare = currentSquare;
    for (const space of state.board) {
      const el = this.cell(space.index);
      if (!el) continue;
      const prop = propState(state, space.index);
      const owner = prop?.ownerId ? playerById(state, prop.ownerId) : null;
      const flag = el.querySelector(".cell__owner");
      if (flag) {
        flag.hidden = !owner;
        if (owner) {
          const color = playerColor(state, owner);
          const kind = tokenKindOf(state, owner);
          const key = `${owner.id}|${color}|${kind}`;
          if (flag.dataset.key !== key) {
            flag.style.setProperty("--pc", color);
            flag.style.setProperty("--pc-ink", inkOn(color));
            // The owner's token silhouette, not their initial: initials collide (Ana / Alpha).
            flag.querySelector(".cell__chip").innerHTML = tokenGlyphSvg(kind);
            flag.dataset.key = key;
          }
        } else flag.dataset.key = "";
      }
      el.classList.toggle("is-owned", Boolean(owner));
      el.classList.toggle("is-mine", Boolean(owner && this.youId && owner.id === this.youId));
      el.classList.toggle("is-mortgaged", Boolean(prop?.mortgaged));
      const sub = el.querySelector(".cell__sub");
      if (space.type === "parking") {
        const pot = el.querySelector(".corner__sub");
        const jackpot = state.settings?.rules?.freeParkingJackpot;
        if (pot) pot.textContent = jackpot ? `POT ${money(state.freeParking || 0)}` : this.compact ? "" : "FREE REST";
      }
      if (sub && !isCorner(space.index)) {
        sub.textContent = owner ? (prop.mortgaged ? "MORTGAGED" : `RENT ${rentLabel(state, space.index)}`) : baseSub(space);
      }
      const houses = el.querySelector(".cell__houses");
      if (houses) {
        const count = prop?.houses || 0;
        const markup = count === 5 ? '<span class="hotel"></span>' : '<span class="house"></span>'.repeat(count);
        if (houses.dataset.count !== String(count)) {
          houses.innerHTML = markup;
          houses.dataset.count = String(count);
        }
      }
      el.classList.toggle("is-current", currentSquare === space.index);
      const building = prop?.houses === 5 ? "hotel" : prop?.houses ? `${prop.houses} house${prop.houses > 1 ? "s" : ""}` : "";
      const label = [space.name, owner ? `owned by ${owner.name}` : "", prop?.mortgaged ? "mortgaged" : "", building, owner && !prop.mortgaged ? `rent ${rentLabel(state, space.index)}` : ""].filter(Boolean).join(", ");
      el.setAttribute("title", label);
      if (this.tapHandler) el.setAttribute("aria-label", label);
    }
  }

  setSlot(html) {
    if (this.slot && this.slot.dataset.html !== html) {
      this.slot.innerHTML = html;
      this.slot.dataset.html = html;
    }
  }

  setStage(html) {
    this.setSlot(html);
  }

  clearStage() {
    this.setSlot("");
  }

  // Full token size in px (fanned tokens scale down from this).
  tokenSize() {
    const unit = this.unit();
    if (this.tokenStyle === "chip") return unit ? Math.round(Math.max(14, Math.min(28, unit * 0.8))) : 22;
    if (!this.compact) return 42;
    return unit ? Math.round(Math.max(18, unit * 1.05)) : 30;
  }

  tokenEl(playerId) {
    return this.tokens.get(playerId) || null;
  }

  rect(index) {
    const el = this.cell(index);
    if (!el) return null;
    return { x: el.offsetLeft, y: el.offsetTop, w: el.offsetWidth, h: el.offsetHeight };
  }

  // Visual centre (board layout px) and scale for token `slot` of `count` sharing square `index`.
  // Edge squares: a lane just inside the square's inner edge. Corners: a zone clear of the label;
  // `inJail` picks the barred box on the jail square. Always clamped inside the board.
  point(index, slot = 0, count = 1, { inJail = false } = {}) {
    const box = this.rect(index);
    if (!box) return { x: 0, y: 0, scale: 1 };
    const base = this.tokenSize();
    const chip = this.tokenStyle === "chip";
    const gap = this.compact ? 1.5 : 3;
    let x;
    let y;
    let scale;
    if (isCorner(index)) {
      const zone = index === 10 && inJail ? CORNER_ZONES.jail : CORNER_ZONES[this.cell(index).dataset.corner] || CORNER_ZONES.br;
      const zw = box.w * zone.w;
      const zh = box.h * zone.h;
      const item = zoneLayout(count, zw, zh, base, { round: chip })[Math.min(slot, Math.max(0, count - 1))];
      scale = item.s;
      x = box.x + box.w * zone.x + zw / 2 + item.dx;
      y = box.y + box.h * zone.y + zh / 2 + item.dy;
    } else {
      const side = SIDES[this.cell(index).dataset.side] || SIDES.bottom;
      const along = side.t[0] ? box.w : box.h;
      const layout = fanLayout(count, along, base);
      const item = layout[Math.min(slot, layout.length - 1)];
      scale = item.s;
      const extent = base * scale * (chip ? 1 : 0.9);
      const depth = gap + extent / 2 + item.r * base * scale * (chip ? 0.92 : 0.8);
      // Midpoint of the square's inner edge.
      const ex = box.x + box.w / 2 + (side.n[0] * box.w) / 2;
      const ey = box.y + box.h / 2 + (side.n[1] * box.h) / 2;
      // Squares next to a corner lean away from it so neighbouring lanes don't collide.
      let lean = 0;
      const neighbour = index % 10 === 1 ? index - 1 : index % 10 === 9 ? (index + 1) % 40 : null;
      if (neighbour !== null && count <= 2) {
        const other = this.rect(neighbour);
        if (other) {
          const dx = box.x + box.w / 2 - (other.x + other.w / 2);
          const dy = box.y + box.h / 2 - (other.y + other.h / 2);
          lean = Math.sign(dx * side.t[0] + dy * side.t[1]) * along * 0.14;
        }
      }
      x = ex + side.n[0] * depth + side.t[0] * (item.u + lean);
      y = ey + side.n[1] * depth + side.t[1] * (item.u + lean);
    }
    const half = (base * scale) / 2;
    const width = this.grid.clientWidth || x + half;
    const height = this.grid.clientHeight || y + half;
    return { x: Math.max(half, Math.min(width - half, x)), y: Math.max(half, Math.min(height - half, y)), scale };
  }

  // CSS transform that puts a token's visual centre on `point` (see .board-token in board.css).
  placeTransform(point) {
    const base = this.tokenSize();
    const oy = this.tokenStyle === "chip" ? 0.5 : 0.52;
    return `translate(${(point.x - base / 2).toFixed(1)}px, ${(point.y - base * oy).toFixed(1)}px) scale(${Number(point.scale ?? 1).toFixed(3)})`;
  }

  // Which side of the board a token stands on ("top" lanes hang their YOU tag below the token).
  laneOf(index) {
    const el = this.cell(index);
    return el ? (isCorner(index) ? `corner-${el.dataset.corner}` : el.dataset.side) : "";
  }

  tokenBody(state, player, kind) {
    const color = playerColor(state, player);
    if (this.tokenStyle === "chip") {
      return `<span class="tok-chip" style="--pc:${color};--pc-ink:${inkOn(color)}">${tokenGlyphSvg(kind)}</span>`;
    }
    return tokenSvg(kind, color, { size: 100 });
  }

  // Create/update token elements and place them on their squares (except those being animated).
  placeTokens(state, { skip = new Set(), positions = null, animate = true } = {}) {
    if (!this.built) this.build(state);
    this.state = state;
    this.measure();
    const active = state.players.filter((player) => !player.bankrupt);
    const groups = new Map();
    const squareOf = (player) => positions?.get(player.id) ?? player.position;
    const keyOf = (player) => (squareOf(player) === 10 && player.inJail ? "10:in" : String(squareOf(player)));
    for (const player of active) {
      const key = keyOf(player);
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key).push(player.id);
    }
    const currentId = state.status === "playing" ? state.currentPlayerId : null;
    for (const player of state.players) {
      let el = this.tokens.get(player.id);
      if (player.bankrupt) {
        if (el) el.classList.add("is-out");
        continue;
      }
      if (!el) {
        el = this.grid.ownerDocument.createElement("div");
        el.className = "board-token";
        el.dataset.playerId = player.id;
        this.tokenLayer.append(el);
        this.tokens.set(player.id, el);
      }
      const kind = tokenKindOf(state, player);
      const key = `${this.tokenStyle}|${kind}|${playerColor(state, player)}|${player.name}`;
      if (el.dataset.key !== key) {
        el.innerHTML = `<div class="board-token__halo"></div><div class="board-token__body">${this.tokenBody(state, player, kind)}</div><div class="jail-bars" hidden><span></span><span></span><span></span><span></span></div><span class="board-token__you">YOU</span>`;
        el.dataset.key = key;
      }
      el.classList.remove("is-out");
      el.classList.toggle("is-you", Boolean(this.youId && player.id === this.youId));
      el.classList.toggle("is-current", player.id === currentId);
      el.querySelector(".jail-bars").hidden = !player.inJail;
      if (skip.has(player.id)) continue;
      const group = groups.get(keyOf(player)) || [player.id];
      const point = this.point(squareOf(player), group.indexOf(player.id), group.length, { inJail: squareOf(player) === 10 && player.inJail });
      el.dataset.lane = this.laneOf(squareOf(player));
      this.moveTokenTo(el, point, { animate });
    }
    for (const [id, el] of this.tokens) {
      if (!state.players.some((player) => player.id === id)) {
        el.remove();
        this.tokens.delete(id);
      }
    }
  }

  moveTokenTo(el, point, { animate = false } = {}) {
    const transform = this.placeTransform(point);
    const previous = el.style.transform;
    if (animate && previous && previous !== transform && el.animate && !prefersReducedMotion()) {
      el.animate([{ transform: previous }, { transform }], { duration: 260, easing: "cubic-bezier(.2,.8,.2,1)" });
    }
    el.style.transform = transform;
    el.style.zIndex = String(Math.round(point.y));
    el.dataset.x = String(point.x);
    el.dataset.y = String(point.y);
    el.dataset.scale = String(point.scale ?? 1);
  }

  // Square-by-square hop along `path` (board indices; the first is where the token stands). Same
  // timing as the PC (120 ms a square, 85 ms when moving 15+, 420 ms to jail). With `follow` the
  // camera zooms in and pans with the token, then eases back out 400 ms after landing (`release`).
  async hop(playerId, path, { stepMs = null, follow = false, zoom = 1.6, onStep = null, release = true, land = true, jail = false } = {}) {
    const token = this.tokenEl(playerId);
    if (!token || !Array.isArray(path) || path.length < 2) return;
    const ms = stepMs ?? (jail ? 420 : path.length > 14 ? 85 : 120);
    const last = path.length - 1;
    const points = path.map((square, index) => this.point(square, 0, 1, { inJail: jail && index === last }));
    if (token.dataset.x) points[0] = { x: Number(token.dataset.x), y: Number(token.dataset.y), scale: Number(token.dataset.scale || 1) };
    const camera = follow && this.camera && !prefersReducedMotion();
    if (camera) this.focusOn(path[0], { zoom, ms: 260, point: points[0] });
    token.classList.add("is-hopping");
    token.style.zIndex = "1000";
    try {
      await hopToken(token, points, {
        stepMs: ms,
        place: (point) => this.placeTransform(point),
        onStep: (index, isLast) => {
          token.dataset.lane = this.laneOf(path[index]);
          if (camera) this.focusOn(path[index], { zoom, ms: Math.round(ms * 1.4), point: points[index] });
          onStep?.(index, isLast, path[index]);
        },
      });
    } finally {
      token.classList.remove("is-hopping");
    }
    const end = points[last];
    token.style.transform = this.placeTransform(end);
    token.style.zIndex = String(Math.round(end.y));
    token.dataset.x = String(end.x);
    token.dataset.y = String(end.y);
    token.dataset.scale = String(end.scale ?? 1);
    if (land) this.landGlow(path[last]);
    if (camera && release) {
      await new Promise((resolve) => setTimeout(resolve, 400));
      this.resetCamera();
    }
  }

  // Mark squares: kind "risk" | "land" | "pick" | "target" (any other kind gets .is-hl-<kind>).
  // `squares` is [index] or [{ index, level }] (level 0–1 drives the risk wash). Replaces the
  // previous set of that kind; `ms` clears it again after a while. An empty list clears.
  highlightSquares(squares = [], kind = "risk", { ms = 0 } = {}) {
    this.clearHighlights(kind);
    const set = new Set();
    for (const entry of squares || []) {
      const index = typeof entry === "number" ? entry : entry?.index;
      const el = this.cell(index);
      if (!el) continue;
      el.classList.add(`is-hl-${kind}`);
      const level = typeof entry === "object" && entry.level !== undefined ? entry.level : 1;
      el.style.setProperty(`--hl-${kind}`, String(Math.max(0, Math.min(1, level))));
      set.add(index);
    }
    this.highlights.set(kind, set);
    if (ms && set.size) this.timers.set(kind, setTimeout(() => this.clearHighlights(kind), ms));
  }

  clearHighlights(kind = null) {
    const kinds = kind ? [kind] : [...this.highlights.keys()];
    for (const name of kinds) {
      clearTimeout(this.timers.get(name));
      this.timers.delete(name);
      for (const index of this.highlights.get(name) || []) {
        const el = this.cell(index);
        el?.classList.remove(`is-hl-${name}`);
        el?.style.removeProperty(`--hl-${name}`);
      }
      this.highlights.delete(name);
    }
  }

  landGlow(index, ms = 1100) {
    this.highlightSquares([index], "land", { ms });
  }

  // Tap/click (and Enter/Space) on a square. Returns an unsubscribe function.
  onSquareTap(callback) {
    this.offTap?.();
    const handler = (event) => {
      const cell = event.target?.closest?.(".cell");
      if (!cell || !this.grid.contains(cell)) return;
      if (event.type === "keydown") {
        if (event.key !== "Enter" && event.key !== " ") return;
        event.preventDefault();
      }
      const index = Number(cell.dataset.index);
      callback(index, { space: this.state?.board?.[index] || null, event });
    };
    this.grid.addEventListener("click", handler);
    this.grid.addEventListener("keydown", handler);
    this.tapHandler = callback;
    this.markTappable(true);
    this.offTap = () => {
      this.grid.removeEventListener("click", handler);
      this.grid.removeEventListener("keydown", handler);
      this.tapHandler = null;
      this.markTappable(false);
      this.offTap = null;
    };
    return this.offTap;
  }

  markTappable(on) {
    this.grid.classList.toggle("is-tappable", on);
    for (const el of this.cells?.values() || []) {
      if (on) {
        el.setAttribute("role", "button");
        el.setAttribute("tabindex", "0");
        el.setAttribute("aria-label", el.getAttribute("title") || this.state?.board?.[Number(el.dataset.index)]?.name || "");
      } else {
        el.removeAttribute("role");
        el.removeAttribute("tabindex");
        el.removeAttribute("aria-label");
      }
    }
  }

  setYou(playerId) {
    this.youId = playerId || null;
    for (const [id, el] of this.tokens) el.classList.toggle("is-you", Boolean(this.youId && id === this.youId));
    if (this.state) this.update(this.state, { currentSquare: this.currentSquare ?? null });
  }

  // Follow-cam: zoom around a square (or an explicit layout point) with a smooth CSS transform of
  // the grid inside the clipped viewport. Needs `camera` (default when compact). Reduced motion:
  // never zooms. `focusOn(null)` is the same as resetCamera().
  focusOn(index, { zoom = 1.6, ms = 350, point = null } = {}) {
    if (!this.camera) return;
    if (index === null || index === undefined || zoom <= 1 || prefersReducedMotion()) {
      this.resetCamera({ ms });
      return;
    }
    const target = point || this.point(index);
    const width = this.root.clientWidth || this.grid.offsetWidth;
    const height = this.root.clientHeight || this.grid.offsetHeight;
    const clamp = (value, min) => Math.max(min, Math.min(0, value));
    const x = clamp(width / 2 - target.x * zoom, width - this.grid.offsetWidth * zoom);
    const y = clamp(height / 2 - target.y * zoom, height - this.grid.offsetHeight * zoom);
    this.applyView({ x, y, z: zoom }, ms);
  }

  resetCamera({ ms = 420 } = {}) {
    if (!this.camera) return;
    this.applyView({ x: 0, y: 0, z: 1 }, ms);
  }

  applyView(view, ms) {
    this.view = view;
    this.grid.style.transition = `transform ${Math.max(0, ms)}ms cubic-bezier(.25,.8,.25,1)`;
    this.grid.style.transform = view.z === 1 && !view.x && !view.y ? "" : `translate(${view.x.toFixed(1)}px, ${view.y.toFixed(1)}px) scale(${view.z})`;
    this.grid.classList.toggle("is-zoomed", view.z !== 1);
  }

  // Re-measure after a resize (phone rotation, window change) and put tokens back without motion.
  relayout() {
    if (!this.built || !this.state) return;
    this.measure();
    const skip = new Set([...this.tokens].filter(([, el]) => el.classList.contains("is-hopping")).map(([id]) => id));
    this.placeTokens(this.state, { skip, animate: false });
  }

  destroy() {
    this.resizer?.disconnect();
    this.offTap?.();
    this.clearHighlights();
  }
}

// Mini deed for the board centre or the phone (rent table + price). `eyebrow: ""` drops the line.
export function deedHtml(state, index, { eyebrow = "DEED", rows = "short", nowLevel = null, className = "" } = {}) {
  const space = state.board[index];
  if (!space) return "";
  const color = space.type === "property" ? GROUP_COLORS[space.group] : "#FBFAF4";
  const ink = space.type === "property" ? GROUP_INK[space.group] || "#FFFFFF" : "#15191E";
  let body = "";
  if (space.type === "property") {
    const list = rows === "full"
      ? [["Rent", space.rents[0]], ["With 1 house", space.rents[1]], ["With 2 houses", space.rents[2]], ["With 3 houses", space.rents[3]], ["With 4 houses", space.rents[4]], ["With hotel", space.rents[5]]]
      : [["Rent", space.rents[0]], ["With hotel", space.rents[5]]];
    body = list.map(([label, value], level) => `<div class="deed__row${nowLevel === level ? " is-now" : ""}"><span>${label}</span><b>${money(value)}</b></div>`).join("");
  } else if (space.type === "railroad") {
    body = [["1 railroad", 25], ["2 railroads", 50], ["3 railroads", 100], ["4 railroads", 200]]
      .slice(0, rows === "full" ? 4 : 2).map(([label, value]) => `<div class="deed__row"><span>${label}</span><b>${money(value)}</b></div>`).join("");
  } else if (space.type === "utility") {
    body = '<div class="deed__row"><span>One utility</span><b>4× dice</b></div><div class="deed__row"><span>Both utilities</span><b>10× dice</b></div>';
  }
  const price = `<div class="deed__row deed__price"><span>Price</span><span>${money(space.price)}</span></div>`;
  const foot = rows === "full" && space.type === "property"
    ? `<div class="deed__foot"><span>Mortgage ${money(Math.floor(space.price / 2))}</span><span>Houses ${money(space.buildCost)} each</span></div>`
    : rows === "full" ? `<div class="deed__foot"><span>Mortgage ${money(Math.floor(space.price / 2))}</span><span></span></div>` : "";
  const photo = spacePhoto(space);
  return `<div class="deed ${className}"${photo ? ` data-photo="${photo}"` : ""}>
    <div class="deed__head" style="background:${color};color:${ink}">${eyebrow ? `<div class="deed__eyebrow">${escapeHtml(eyebrow)}</div>` : ""}<div class="deed__name">${escapeHtml(space.name)}</div></div>
    <div class="deed__rows">${body}${rows === "full" ? "" : price}</div>${foot}
  </div>`;
}

// Small deed card for pickers and trade piles: colour header, name, one value row. Shared so the
// phone's trade flow can use the same card. `selected` lifts it with a gold ring and a tick,
// `locked` greys it out with `note`, `badge` is a small green tag ("COMPLETES PINK"). `tag` and
// `attrs` make it a button: miniDeedHtml(state, 6, { tag: "button", attrs: 'type="button" …' }).
export function miniDeedHtml(state, index, { selected = false, locked = false, badge = "", note = "", value = "rent", tag = "span", attrs = "" } = {}) {
  const space = state.board[index];
  if (!space) return "";
  const color = space.type === "property" ? GROUP_COLORS[space.group] : space.type === "railroad" ? "#2B323A" : "#5B6470";
  const ink = space.type === "property" ? GROUP_INK[space.group] || "#FFFFFF" : "#FFFFFF";
  const prop = propState(state, index);
  let label = "price";
  let right = money(space.price);
  if (value !== "price" && prop?.mortgaged) {
    label = "";
    right = "mortgaged";
  } else if (value !== "price" && prop?.ownerId) {
    label = "rent";
    right = rentLabel(state, index);
  }
  const classes = `mdeed${selected ? " is-on" : ""}${locked ? " is-locked" : ""}${prop?.mortgaged ? " is-mortgaged" : ""}`;
  return `<${tag} class="${classes}" style="--gc:${color};--gc-ink:${ink}" ${attrs}>
    <span class="mdeed__head">${escapeHtml(space.name)}</span>
    <span class="mdeed__row"><span>${escapeHtml(label)}</span><b>${escapeHtml(String(right))}</b></span>
    ${badge ? `<span class="mdeed__badge">${escapeHtml(badge)}</span>` : ""}
    ${locked && note ? `<span class="mdeed__note">${escapeHtml(note)}</span>` : ""}
    <span class="mdeed__tick" aria-hidden="true">✓</span>
  </${tag}>`;
}
