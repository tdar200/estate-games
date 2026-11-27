// Ludo animations on the board SVG (ludo-board.js), shared by the table screen and the phone: the die
// tumbling, a piece hopping square by square, a captured piece flying back to its yard with a
// burst in its colour, and a small celebration when a piece gets home. The server waits the same
// lengths before a computer's next move (internal/server/ludo.go), so people can follow the game.
//
// How it fits with morph: a new state's move is played on the board that is still showing the old
// state (the piece walks from where it is), and only then is the new state drawn, which leaves the
// piece exactly where the walk ended. A roll is the other way round: the new state is drawn (the
// die shows the new number) and the die then tumbles onto it.

import { dieHtml } from "./dice.js";
import { cellPoint, geometry, piecePoints } from "./ludo-board.js";

export const TIMING = Object.freeze({ diceMs: 700, hopMs: 150, captureMs: 650, homeMs: 800 });

const esc = (value) => (globalThis.CSS?.escape ? CSS.escape(value) : String(value).replace(/"/g, '\\"'));
const SVG = "http://www.w3.org/2000/svg";
const sleep = (ms) => new Promise((done) => setTimeout(done, ms));

function place(point, scale = 1) {
  return `translate(${point.x}px, ${point.y}px) scale(${scale})`;
}

function pieceEl(root, playerId, index) {
  return root.querySelector(`.lboard .lp[data-key="lp-${esc(playerId)}-${index}"]`);
}

// Where a piece is drawn now: a running or held animation's transform when there is one, else the
// board's own transform attribute (which includes the spread for pieces sharing a square).
function drawnAt(el) {
  const css = getComputedStyle(el).transform;
  if (css && css !== "none" && globalThis.DOMMatrixReadOnly) {
    const m = new DOMMatrixReadOnly(css);
    return { x: m.e, y: m.f };
  }
  return markedAt(el);
}

function markedAt(el) {
  const m = el.transform?.baseVal?.consolidate?.()?.matrix;
  return m ? { x: m.e, y: m.f } : null;
}

// Our own animations on a piece (hops, capture flights, slides), not the CSS ones (the halo).
function ownAnimations(el) {
  return el.getAnimations().filter((a) => !(globalThis.CSSAnimation && a instanceof CSSAnimation) && !(globalThis.CSSTransition && a instanceof CSSTransition));
}

// The spot the board gives a piece in a state: its square, spread when it shares the square.
function spotIn(state, playerId, index) {
  const item = piecePoints(state).find((it) => it.player.id === playerId && it.piece.index === index);
  return item ? { x: item.x, y: item.y } : null;
}

async function run(animation) {
  try {
    await animation.finished;
  } catch {
    // cancelled: the next state is drawn anyway
  }
}

// The fx for one screen. deps: sound(name, opts), speed() → 1 normal, < 1 faster.
export function createLudoFx(deps = {}) {
  const speed = () => Math.max(0.35, Math.min(1.5, Number(deps.speed?.() ?? 1) || 1));
  const sound = (name, opts) => deps.sound?.(name, opts);

  // The die in the middle tumbles through a few faces onto the number rolled.
  async function tumble(root, value) {
    const box = root.querySelector(".lcenter__die");
    if (!box) return;
    const ms = TIMING.diceMs * speed();
    box.classList.add("js-tumble");
    const faces = [3, 5, 2, 6, 1, 4];
    const started = performance.now();
    let i = 0;
    while (performance.now() - started < ms - 120) {
      box.innerHTML = dieHtml(faces[(i + value) % 6], { className: "die--ldbig" });
      i += 1;
      await sleep(70);
    }
    box.innerHTML = dieHtml(value, { className: "die--ldbig" });
    box.classList.remove("js-tumble");
    box.classList.add("js-landed");
    setTimeout(() => box.classList.remove("js-landed"), 400);
  }

  // A burst of rings and dots at a board point, in a colour (capture, home).
  function burst(svg, point, color, { big = false } = {}) {
    if (!svg || !point) return;
    const layer = document.createElementNS(SVG, "g");
    layer.setAttribute("class", "lfx");
    layer.setAttribute("transform", `translate(${point.x} ${point.y})`);
    svg.append(layer);
    const ring = document.createElementNS(SVG, "circle");
    ring.setAttribute("r", "0.4");
    ring.setAttribute("class", "lfx__ring");
    ring.style.stroke = color;
    layer.append(ring);
    ring.animate([{ transform: "scale(0.6)", opacity: 1 }, { transform: `scale(${big ? 4 : 2.6})`, opacity: 0 }], { duration: 650 * speed(), easing: "ease-out", fill: "forwards" });
    const n = big ? 14 : 8;
    for (let k = 0; k < n; k += 1) {
      const dot = document.createElementNS(SVG, "circle");
      dot.setAttribute("r", big ? "0.12" : "0.1");
      dot.setAttribute("class", "lfx__dot");
      dot.style.fill = k % 3 === 2 ? "#F2C14E" : color;
      layer.append(dot);
      const a = (k / n) * Math.PI * 2;
      const d = big ? 1.9 : 1.2;
      dot.animate([{ transform: "translate(0px, 0px) scale(1)", opacity: 1 }, { transform: `translate(${Math.cos(a) * d}px, ${Math.sin(a) * d}px) scale(0.4)`, opacity: 0 }], { duration: 700 * speed(), easing: "cubic-bezier(.2,.7,.3,1)", fill: "forwards" });
    }
    setTimeout(() => layer.remove(), 900 * speed() + 200);
  }

  // Walk a piece along its path, one square per hop, with a step sound.
  async function hop(root, prev, move, next) {
    const geom = geometry(prev?.board?.arms);
    const el = pieceEl(root, move.playerId, move.piece);
    const svg = root.querySelector(".lboard");
    if (!el || !svg) return;
    el.parentNode.append(el); // on top while it moves
    el.classList.add("js-moving");
    let from = drawnAt(el) || cellPoint(geom, move.from) || { x: 0, y: 0 };
    // The last hop lands where the next board puts the piece: beside any piece already there.
    const landing = spotIn(next, move.playerId, move.piece);
    const ms = TIMING.hopMs * speed();
    let step = 0;
    for (const cell of move.path || []) {
      const last = step + 1 === move.path.length;
      const to = (last && landing) || cellPoint(geom, cell);
      if (!to) continue;
      step += 1;
      const mid = { x: (from.x + to.x) / 2, y: (from.y + to.y) / 2 - 0.22 };
      await run(el.animate([{ transform: place(from) }, { transform: place(mid, 1.22), offset: 0.45 }, { transform: place(to) }], { duration: last ? ms * 1.5 : ms, easing: "ease-in-out", fill: "forwards" }));
      el.style.transform = place(to);
      sound("hop", { step, volume: 0.55 });
      from = to;
    }
    sound("land", { volume: 0.8 });
    el.classList.remove("js-moving");
  }

  // Captured pieces fly home to their yard spot, spinning, after a burst where they stood.
  async function captures(root, prev, move) {
    const geom = geometry(prev?.board?.arms);
    const svg = root.querySelector(".lboard");
    const flights = [];
    for (const hit of move.captured || []) {
      const el = pieceEl(root, hit.playerId, hit.piece);
      const at = cellPoint(geom, hit.cell);
      const yard = cellPoint(geom, hit.yard);
      if (!el || !at || !yard) continue;
      burst(svg, at, el.style.getPropertyValue("--pc") || "#E8232A");
      el.parentNode.append(el);
      const mid = { x: (at.x + yard.x) / 2, y: (at.y + yard.y) / 2 - 1.6 };
      flights.push(run(el.animate([{ transform: place(at) }, { transform: `${place(mid, 1.5)} rotate(200deg)`, offset: 0.5 }, { transform: `${place(yard)} rotate(360deg)` }], { duration: TIMING.captureMs * speed(), easing: "cubic-bezier(.3,.6,.3,1)", fill: "forwards" })).then(() => { el.style.transform = place(yard); }));
    }
    if (flights.length) sound("jail", { volume: 0.7 });
    await Promise.all(flights);
  }

  async function home(root, prev, move) {
    const geom = geometry(prev?.board?.arms);
    const svg = root.querySelector(".lboard");
    const el = pieceEl(root, move.playerId, move.piece);
    burst(svg, cellPoint(geom, move.cell), el?.style.getPropertyValue("--pc") || "#F2C14E", { big: true });
    sound("go", { volume: 0.8 });
    await sleep(Math.min(TIMING.homeMs, 520) * speed());
  }

  // Between two states of the same game: animate what happened, drawing `next` (render()) at the
  // right moment. Resolves when the screen shows `next` and its animations are done.
  // Draw the next state, then hand every piece back to the board: the hops and flights end held
  // (fill: forwards), and a held animation outranks the board's transform, so a piece that had
  // moved stayed in the middle of its square and hid any piece it shared the square with. Pieces
  // whose spot changed (one already on the square a piece landed on, now beside it) slide there.
  function draw(root, render, { slide = true } = {}) {
    const pieces = root ? [...root.querySelectorAll(".lboard .lp")] : [];
    const before = new Map(pieces.map((el) => [el.dataset.key, drawnAt(el)]));
    render();
    if (!root) return;
    for (const el of root.querySelectorAll(".lboard .lp")) {
      const was = before.get(el.dataset.key);
      for (const animation of ownAnimations(el)) animation.cancel();
      el.style.removeProperty("transform");
      const now = markedAt(el);
      if (!slide || !was || !now || Math.hypot(was.x - now.x, was.y - now.y) < 0.01) continue;
      el.animate([{ transform: place(was) }, { transform: place(now) }], { duration: 220 * speed(), easing: "ease-out" });
    }
  }

  async function play(root, prev, next, rawRender, { reduced = false } = {}) {
    const same = prev?.game === "ludo" && next?.game === "ludo" && prev.code === next.code && prev.status !== "lobby" && next.status !== "lobby";
    const render = () => draw(root, rawRender, { slide: same && !reduced });
    if (!same || reduced || !root) {
      render();
      return;
    }
    const move = next.lastMove && next.lastMove.seq !== prev.lastMove?.seq ? next.lastMove : null;
    const rolled = (next.rollSeq || 0) > (prev.rollSeq || 0);
    if (move) {
      await hop(root, prev, move, next);
      if (move.captured?.length) await captures(root, prev, move);
      render();
      if (move.home) await home(root, next, move);
      if (rolled && next.lastMove?.seq === move.seq && next.diceBy && next.rollSeq > prev.rollSeq + 1) await tumble(root, next.dice);
      return;
    }
    render();
    if (rolled) await tumble(root, next.dice);
  }

  return { play, tumble, burst };
}
