// Which layout the player app uses for a viewport, and what the board does at that size. Pure.
//   phone  — under 700 px wide: the phone column (strip, board, status, buttons, bottom nav).
//   tablet — 700 px and wider, portrait: the same column, wider; the board grows to the width.
//   desk   — 700 px and wider, landscape (laptops, PCs, landscape tablets and phones): two panes,
//            the board as a square on the left at full height, a panel on the right with tabs,
//            the players, the status line, the buttons and the tab's content.
// html[data-layout] carries the choice; phone.css does the rest.

export const LAYOUTS = ["phone", "tablet", "desk"];

export function layoutFor(width, height) {
  if (!(width >= 700)) return "phone";
  return width > height ? "desk" : "tablet";
}

// Below this many pixels the board is the compact phone board (colour bars, owner chips, round
// tokens); from it up, the full board with street names, prices, decks and metal tokens.
export const FULL_BOARD_MIN = 560;

export function boardModeFor(boardPx) {
  return boardPx >= FULL_BOARD_MIN ? "full" : "compact";
}

// Follow-cam zoom while a token hops: closer on small boards, gentler on big ones.
export function hopZoom(boardPx) {
  if (boardPx < 400) return 1.6;
  if (boardPx < 700) return 1.35;
  return 1.2;
}

// Screens that keep the board on the left in the desk layout (everything in a game except the
// lobby and the ways in).
export function deskGameFrame(screen, state) {
  return Boolean(state) && state.status !== "lobby" && !["join", "invited", "lobby"].includes(screen);
}
