// The player app's board: the shared Board (web/shared/board.js) mounted once outside #main so
// render() never rebuilds it, plus the app's own data on top — the centre stage, risk wash,
// card targets, and tap-a-square. Animations (hops, chips) are driven by playback.js through
// the same instance. Small boards use the compact phone board; from FULL_BOARD_MIN px (tablets,
// laptops) it is the full board with names and prices. Both follow the hopping token.

import { Board } from "../shared/board.js";

export class PhoneBoard {
  constructor(root, { onSquareTap = null, mode = "compact" } = {}) {
    this.root = root;
    this.onSquareTap = onSquareTap;
    this.mode = null;
    this.setMode(mode);
  }

  // "compact" or "full". Rebuilds the shared Board in a fresh mount; the next show() redraws it.
  setMode(mode) {
    if (mode === this.mode) return false;
    this.board?.destroy?.();
    const mount = this.root.ownerDocument.createElement("div");
    mount.className = "pboard__mount";
    this.root.replaceChildren(mount);
    this.root.dataset.mode = mode;
    this.mode = mode;
    this.board = new Board(mount, mode === "full" ? { compact: false, camera: true } : { compact: true });
    this.viewerId = null;
    this.stageHtml = null;
    this.marksKey = "";
    this.heatKey = "";
    if (this.onSquareTap && typeof this.board.onSquareTap === "function") this.board.onSquareTap((index) => this.onSquareTap(index));
    return true;
  }

  get fx() {
    return this.board.fx;
  }

  get built() {
    return Boolean(this.board.built);
  }

  cell(index) {
    return this.board.cell(index);
  }

  tokenEl(playerId) {
    return this.board.tokenEl(playerId);
  }

  // Layout point of a player's token (board px), for chips that rise over it.
  tokenPoint(playerId) {
    const token = this.board.tokenEl(playerId);
    if (!token || !token.dataset.x) return null;
    return { x: Number(token.dataset.x), y: Number(token.dataset.y) - 6 };
  }

  centre() {
    const grid = this.board.grid || this.root;
    return { x: (grid.clientWidth || this.root.clientWidth) / 2, y: (grid.clientHeight || this.root.clientHeight) / 2 };
  }

  setViewer(viewerId) {
    if (viewerId === this.viewerId) return;
    this.viewerId = viewerId;
    if (typeof this.board.setYou === "function") this.board.setYou(viewerId);
  }

  // Ownership, houses and tokens for `state` (tokens in `skip` are left where they are).
  show(state, { positions = null, skip = new Set(), animate = true } = {}) {
    this.setViewer(state.viewerId || null);
    this.board.update(state, { currentSquare: null });
    this.board.placeTokens(state, { positions, skip, animate });
  }

  setStage(html) {
    if (html === this.stageHtml) return;
    this.stageHtml = html;
    this.board.setStage(html || "");
  }

  // Forget the cached stage (after an animation wrote into the slot directly). The slot's own cache
  // gets a value no render produces, so even an empty stage clears what the animation left there
  // ("" would match an empty stage and keep an old "ALPHA ROLLS 6" on the board; the table uses
  // the same sentinel).
  dirtyStage() {
    this.stageHtml = null;
    if (this.board.slot) this.board.slot.dataset.html = "(dirty)";
  }

  stageEl() {
    return this.board.stage || this.board.slot || null;
  }

  // Board data from screens.js: { heat: [{index, level}], marks: { target, path } }.
  apply(data) {
    const heat = data?.heat || [];
    const heatKey = heat.map((entry) => `${entry.index}:${entry.level}`).join(",");
    if (heatKey !== this.heatKey) {
      this.heatKey = heatKey;
      this.board.highlightSquares?.(heat, "risk");
    }
    const marks = data?.marks || null;
    const marksKey = marks ? `${marks.target ?? ""}|${(marks.path || []).join(",")}` : "";
    if (marksKey !== this.marksKey) {
      this.marksKey = marksKey;
      const path = (marks?.path || []).slice(1, -1);
      this.board.highlightSquares?.(path, "pick");
      this.board.highlightSquares?.(marks && marks.target !== undefined && marks.target !== null ? [marks.target] : [], "target");
    }
    this.setStage(data?.stage || "");
  }

  clearMarks() {
    this.heatKey = "";
    this.marksKey = "";
    this.board.clearHighlights?.();
  }

  hop(playerId, path, options) {
    return this.board.hop(playerId, path, options);
  }

  landGlow(index, ms) {
    this.board.landGlow?.(index, ms);
  }

  resetCamera() {
    // Eased, not snapped: a cut-short hop leaves the camera zoomed, and a jump reads as a flash.
    this.board.resetCamera?.({ ms: 240 });
  }

  // Stop one-shot animations cut short by the time budget, so `fill: forwards` can't fight the
  // positions the next render sets. Endless idle loops are left running.
  cancelEffects() {
    for (const animation of this.root.getAnimations?.({ subtree: true }) || []) {
      if (animation.effect?.getTiming?.().iterations !== Infinity) animation.cancel();
    }
    this.board.fx?.replaceChildren?.();
    this.resetCamera();
    this.dirtyStage();
  }
}
