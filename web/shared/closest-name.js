// The display name of the number-guessing game, in one place (docs/CLOSEST-API.md §0). No imports,
// so any module can use it without an import cycle. The Go twin is closest.Title; the server also
// sends it as state.title, which closest-view.js gameTitle() prefers.
export const GAME_NAME = "Near Thing";
export const GAME_TITLE = GAME_NAME.toUpperCase();
