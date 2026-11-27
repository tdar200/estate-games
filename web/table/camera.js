// Camera for the PC table: the board (laid out at LAYOUT.board px) sits inside the whole 1600 × 900
// stage and is moved with a single CSS transform, translate(x, y) scale(z), so the browser can run
// every move on the GPU. Everything here is pure maths (unit-tested in web/tests/table.check.mjs;
// web/tests/screens.browser.mjs samples the real thing in a browser); table.js applies the views.
//
//   overview  the whole board, as big as the stage height allows, in the space right of the plates:
//             where the camera is between moves and for every decision
//   follow    during a long hop (CAMERA = FOLLOW MOVES, the default: FOLLOW_MIN_STEPS squares or
//             more, past PAYDAY, or moved by a card): zoomed in at the ZOOM setting (1.6×),
//             panning with the token square by square, a short rest on the landing square
//             (FOLLOW_REST_MS), then back to the overview before money moves or anyone decides
//   focus     a short close-up on one square (a big rent), then back to the overview
//   fit       the smallest view that holds a set of squares (a drawer's deeds)

import { cardPreview } from "../shared/insights.js";

export const LAYOUT = Object.freeze({
  stage: Object.freeze({ w: 1600, h: 900 }),
  board: 860,
  // The camera's viewport starts right of the plates: the board is never drawn under them.
  clipLeft: 340,
  // Where the camera centres and clamps: right of the plates (left column), left of the action
  // card column. The board may slide under the plates, the card and the mini-map; those float.
  frame: Object.freeze({ left: 360, right: 1220, top: 0, bottom: 900 }),
  // While the trade drawer (640 px, right) is open.
  drawerFrame: Object.freeze({ left: 360, right: 920, top: 0, bottom: 900 }),
  focusZoom: 2,
  fitMaxZoom: 1.8,
  minZoom: 0.5,
  fitPad: 70,
});

const clamp = (value, lo, hi) => Math.max(lo, Math.min(hi, value));

// Keep a board span [pos, pos + size] covering [lo, hi] (no empty space inside the frame); when the
// board is smaller than the frame, centre it instead.
export function clampAxis(pos, size, lo, hi) {
  if (size <= hi - lo) return lo + (hi - lo - size) / 2;
  return Math.min(lo, Math.max(hi - size, pos));
}

export function frameOf(name, layout = LAYOUT) {
  return name === "drawer" ? layout.drawerFrame : layout.frame;
}

// The whole board, as large as the frame allows (never above 1×: the board is laid out at the size
// that reads well), centred in the frame.
export function overviewView(layout = LAYOUT, frame = layout.frame) {
  const z = Math.min(1, (frame.right - frame.left) / layout.board, (frame.bottom - frame.top) / layout.board);
  const size = layout.board * z;
  return { x: frame.left + (frame.right - frame.left - size) / 2, y: frame.top + (frame.bottom - frame.top - size) / 2, z };
}

// `point` (board px) in the middle of the frame at `zoom`, clamped so the frame never shows empty
// space past the board's edge.
export function focusView(point, { zoom = LAYOUT.focusZoom, frame = LAYOUT.frame, layout = LAYOUT } = {}) {
  const size = layout.board * zoom;
  const cx = (frame.left + frame.right) / 2;
  const cy = (frame.top + frame.bottom) / 2;
  return {
    x: clampAxis(cx - point.x * zoom, size, frame.left, frame.right),
    y: clampAxis(cy - point.y * zoom, size, frame.top, frame.bottom),
    z: zoom,
  };
}

// Union of board-px rects { x, y, w, h }.
export function boundsOf(rects) {
  const list = rects.filter(Boolean);
  if (!list.length) return null;
  const x0 = Math.min(...list.map((r) => r.x));
  const y0 = Math.min(...list.map((r) => r.y));
  const x1 = Math.max(...list.map((r) => r.x + r.w));
  const y1 = Math.max(...list.map((r) => r.y + r.h));
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
}

// The smallest view (within min/max zoom) that shows `rect` with some padding.
export function fitView(rect, { frame = LAYOUT.frame, layout = LAYOUT, maxZoom = layout.fitMaxZoom, minZoom = layout.minZoom, pad = layout.fitPad } = {}) {
  const fw = Math.max(1, frame.right - frame.left - 2 * pad);
  const fh = Math.max(1, frame.bottom - frame.top - 2 * pad);
  const z = clamp(Math.min(fw / Math.max(1, rect.w), fh / Math.max(1, rect.h)), minZoom, maxZoom);
  return focusView({ x: rect.x + rect.w / 2, y: rect.y + rect.h / 2 }, { zoom: z, frame, layout });
}

// CAMERA setting (settings.js): "follow" = FOLLOW MOVES (follow each hop, then the whole board),
// "closeups" = CLOSE-UPS ONLY (the whole board but a short close-up on a big rent), "off" = always
// the whole board. Reduced motion (the system's or this screen's) turns the camera off.
export const CAMERA_MODES = Object.freeze(["follow", "closeups", "off"]);

// The follow camera rests on the landing square this long before easing out (longer for a big
// rent: it stands in for the close-up, so a followed landing isn't zoomed into twice).
export const FOLLOW_REST_MS = 500;
export const FOLLOW_BIG_REST_MS = 900;
// Zoom in on the token before it starts hopping, and ease back out (ms). At 420 ms with a strong
// ease-out most of the zoom happened in under 120 ms and read as a cut (review F9).
export const FOLLOW_IN_MS = 650;
export const FOLLOW_OUT_MS = 600;
// The camera's ease for zooms and moves: in-out, so it glides off and settles.
export const CAMERA_EASE = "cubic-bezier(.45,0,.2,1)";
// Only long hops are followed: zooming in and out on every short move was tiring (review F9, U1).
export const FOLLOW_MIN_STEPS = 7;

// Does a path pass (or land on) PAYDAY? Squares count up 0–39 and wrap.
export function passesGo(path = []) {
  for (let index = 1; index < path.length; index += 1) {
    if (path[index] === 0 || path[index] < path[index - 1]) return true;
  }
  return false;
}

// Is this hop followed? Only a long one: FOLLOW_MIN_STEPS squares or more, past PAYDAY, or a move a
// card made (`card`). A jump to jail (one long flight, not a hop) and short moves play on the
// whole board, as does everything while a drawer is open, the tab is hidden or motion is reduced.
export function followsHop({ mode = "follow", reduced = false, hidden = false, docked = false, path = [], jail = false, card = false } = {}) {
  if (mode !== "follow" || reduced || hidden || docked || jail || !Array.isArray(path) || path.length < 2) return false;
  return card || path.length - 1 >= FOLLOW_MIN_STEPS || passesGo(path);
}

// The camera's view for each square of a hop: `points` are the token's board-px points along the
// path (the first is where it stands). Each view centres the token at `zoom`, clamped at the board.
export function hopShots(points, { zoom = LAYOUT.focusZoom, layout = LAYOUT, frame = layout.frame } = {}) {
  return points.map((point) => focusView(point, { zoom, frame, layout }));
}

// Each pan during a hop: linear, as long as the step it tracks (the last step lands at 1.6×), so
// the camera moves with the token at an even speed instead of lurching square by square.
export function panMs(stepMs, last = false) {
  return Math.max(60, Math.round(stepMs * (last ? 1.6 : 1)));
}

// Big moments that earn a short close-up while the whole board is otherwise in view: rent of
// CLOSE_UP_RENT or more. Decisions (buying, auctions, cards, trades, debt) play on the whole
// board, with the deed / card / deal shown big beside it (review U1: the old camera sat at 2× in
// 10 of 12 samples because it framed every decision; now only the hop itself is followed).
export const CLOSE_UP_RENT = 100;
export const CLOSE_UP_MS = 1800;

export function closeUpSquare(state) {
  const landing = state?.lastLanding;
  if (!landing || landing.playerId !== state.currentPlayerId) return null;
  if (landing.kind === "rent" && landing.amount >= CLOSE_UP_RENT && Number.isInteger(landing.spaceIndex)) return landing.spaceIndex;
  return null;
}

// What the camera should look at for a state. Pure: { kind, squares, frame, hold }.
//   kind   "overview" | "focus" (one square, close) | "fit" (all of `squares`)
//   hold   ms after which the camera eases back to the overview (0 = while this state lasts)
//   inspect  a drawer (property, loan) is open: { focus: square with focus or null, squares: the
//            ones it is about }. The focused deed is framed; otherwise all of `squares`.
//   calm   (the default) the whole board except for a brief close-up on a big moment; false is
//          the older follow camera that framed every decision.
export function cameraPlan(state, { trading = false, draft = [], inspect = null, calm = true } = {}) {
  const overview = (frame = "main") => ({ kind: "overview", squares: [], frame, hold: 0 });
  if (trading) return draft.length ? { kind: "fit", squares: [...draft], frame: "drawer", hold: 0 } : overview("drawer");
  if (inspect) {
    if (Number.isInteger(inspect.focus)) return { kind: "fit", squares: [inspect.focus], frame: "drawer", hold: 0 };
    const squares = (inspect.squares || []).filter(Number.isInteger);
    return squares.length ? { kind: "fit", squares, frame: "drawer", hold: 0 } : overview("drawer");
  }
  if (!state || state.status !== "playing") return overview();
  if (calm) {
    const square = state.phase === "turn_complete" ? closeUpSquare(state) : null;
    return Number.isInteger(square) ? { kind: "focus", squares: [square], frame: "main", hold: CLOSE_UP_MS } : overview();
  }
  const player = (id) => state.players?.find((candidate) => candidate.id === id) || null;
  const focus = (index, hold = 0) => (Number.isInteger(index) ? { kind: "focus", squares: [index], frame: "main", hold } : overview());
  const fit = (squares) => {
    const list = [...new Set(squares.filter(Number.isInteger))];
    if (!list.length) return overview();
    return list.length === 1 ? focus(list[0]) : { kind: "fit", squares: list, frame: "main", hold: 0 };
  };
  switch (state.phase) {
    case "awaiting_purchase":
      return focus(state.pendingPurchase);
    case "auction":
      return focus(state.auction?.spaceIndex);
    case "card_confirmation": {
      const card = state.pendingCard;
      const at = player(card?.playerId)?.position;
      let target = null;
      try {
        target = cardPreview(state, card?.playerId, card?.cardId).target;
      } catch {
        target = null;
      }
      return Number.isInteger(target) && target !== at ? fit([at, target]) : focus(at);
    }
    case "debt":
      return state.debt?.loanId ? overview() : focus(player(state.debt?.payerId)?.position);
    case "trade":
      return fit([...(state.trade?.offer?.properties || []), ...(state.trade?.request?.properties || [])]);
    case "turn_complete": {
      // The landing square while it matters (rent paid, bought, taxed, jailed), then back out.
      const landing = state.lastLanding?.playerId === state.currentPlayerId ? state.lastLanding.spaceIndex : null;
      return focus(Number.isInteger(landing) ? landing : player(state.currentPlayerId)?.position, 4000);
    }
    default:
      return overview();
  }
}

// The view for a plan. `rectOf(index)` gives a square's board-px rect.
export function viewFor(plan, rectOf, layout = LAYOUT) {
  const frame = frameOf(plan?.frame, layout);
  if (!plan || plan.kind === "overview") return overviewView(layout, frame);
  const rects = plan.squares.map((index) => rectOf(index)).filter(Boolean);
  const box = boundsOf(rects);
  if (!box) return overviewView(layout, frame);
  if (plan.kind === "focus") return focusView({ x: box.x + box.w / 2, y: box.y + box.h / 2 }, { zoom: layout.focusZoom, frame, layout });
  return fitView(box, { frame, layout });
}

// The part of the board (board px) visible on the stage for `view`, for the mini-map's rectangle.
export function visibleRect(view, layout = LAYOUT) {
  const x0 = clamp(((layout.clipLeft || 0) - view.x) / view.z, 0, layout.board);
  const y0 = clamp((0 - view.y) / view.z, 0, layout.board);
  const x1 = clamp((layout.stage.w - view.x) / view.z, 0, layout.board);
  const y1 = clamp((layout.stage.h - view.y) / view.z, 0, layout.board);
  return { x: x0, y: y0, w: x1 - x0, h: y1 - y0 };
}

export function sameView(a, b, eps = 0.5) {
  return Boolean(a && b) && Math.abs(a.x - b.x) < eps && Math.abs(a.y - b.y) < eps && Math.abs(a.z - b.z) < 0.005;
}

// The CSS transform for the board inside the camera element, which starts at `clipLeft`.
export function viewTransform(view, layout = LAYOUT) {
  return `translate(${(view.x - (layout.clipLeft || 0)).toFixed(1)}px, ${view.y.toFixed(1)}px) scale(${view.z.toFixed(4)})`;
}

// How long a camera move should take: longer for bigger moves, capped; 0 when it must not animate.
export function moveMs(from, to, { reduced = false, hidden = false, base = 520 } = {}) {
  if (reduced || hidden || !from) return 0;
  const distance = Math.hypot(to.x - from.x, to.y - from.y) + Math.abs(to.z - from.z) * 600;
  return Math.round(clamp(base * (0.6 + distance / 1400), 260, 900));
}
