// Every screen the player app can show, as demo scenes: `/play?demo=scene&s=<id>`. Each scene starts
// from a fixture in web/fixtures/, patches the state (pure functions below), sets the app's view
// state (tab, drafts, join form…) and may run one action after the first render (open a sheet or a
// confirm, show a toast or a cue chip, play the next state for an animation frame). Used for the
// mockups (docs/review/mockups/player-all/) and checked in web/tests/phone.check.mjs. Pure data and
// functions; phone.js runs them.

export const SECTIONS = ["Start", "Lobby", "Turn", "Money", "Trade & loans", "Deeds", "Menus & sheets", "Notices & errors", "End"];
const TOKENS = ["rocket", "teapot", "bicycle", "lighthouse", "key", "umbrella"];

// ---- Helpers (pure) ---------------------------------------------------------------------------

const P = (s, who) => s.players.find((player) => player.id === who || player.name === who);
const meOf = (s) => P(s, s.viewerId);
const idOf = (s, who) => P(s, who)?.id;

export function setPlayer(s, who, patch) {
  const target = P(s, who);
  return { ...s, players: s.players.map((player) => (player === target ? { ...player, ...patch } : player)) };
}

export function setProps(s, map) {
  const properties = { ...s.properties };
  for (const [index, patch] of Object.entries(map)) properties[index] = { ownerId: null, houses: 0, mortgaged: false, ...(properties[index] || {}), ...patch };
  return { ...s, properties };
}

export function addLog(s, lines) {
  const base = (s.log || []).length;
  return { ...s, log: [...(s.log || []), ...lines.map((text, k) => ({ id: `log_scene_${base + k}`, text, at: 0 }))] };
}

// Drop log lines from the current turn that would make the stage show something else.
function trimTurnLog(s, pattern) {
  return { ...s, log: (s.log || []).filter((entry) => !pattern.test(entry.text)) };
}

export function ownedBy(s, id) {
  return Object.entries(s.properties || {}).filter(([, prop]) => prop?.ownerId === id).map(([index]) => Number(index));
}

const groupOf = (s, index) => s.board.filter((space) => space.type === "property" && space.group === s.board[index].group).map((space) => space.index ?? s.board.indexOf(space));
const freeStreets = (s) => s.board.map((space, index) => ({ space, index })).filter(({ space, index }) => space.type === "property" && !s.properties?.[index]?.ownerId).map(({ index }) => index);

// GET /api/rooms/{code}/summary for this state (docs/API.md "Getting back in").
export function summaryOf(s, { offline = [], ids = true, exclude = [] } = {}) {
  return {
    code: s.code,
    status: s.status,
    host: (({ name, tokenKind, color }) => ({ name, tokenKind, color }))(P(s, s.hostId) || s.players[0]),
    players: s.players.filter((player) => !exclude.includes(player.name)).map((player) => {
      const pc = player.input === "controller" || player.input === "keyboard";
      const off = offline.includes(player.name);
      const entry = { name: player.name, tokenKind: player.tokenKind, color: player.color, input: player.input };
      if (!ids) return entry;
      return { id: player.id, ...entry, online: player.input === "ai" ? true : !off, reclaimable: off && !pc && player.input !== "ai" && !player.bankrupt };
    }),
    openSeats: s.status === "lobby" ? 6 - s.players.length : 0,
    tableOnline: false,
  };
}

const notMyTurn = (s) => ({ ...s, currentPlayerId: idOf(s, "Alpha"), phase: "awaiting_roll", turn: { ...(s.turn || {}), playerId: idOf(s, "Alpha"), dice: null, doublesCount: 0 } });
const landed = (s, index, kind, extra = {}) => {
  const me = meOf(s);
  const base = trimTurnLog(s, new RegExp(`^${me.name} (bought|won|landed|paid)`));
  return setPlayer({ ...base, phase: "turn_complete", lastLanding: { id: "landing_scene", playerId: me.id, spaceIndex: index, kind, amount: 0, recipientId: null, ...extra } }, me.id, { position: index });
};
const draft = (extra) => ({ targetId: null, offer: [], request: [], offerCash: 0, requestCash: 0, quote: null, planned: false, ...extra });
const loan = (extra) => ({ direction: "lend", targetId: null, principal: 200, rate: 0.2, dueTurns: 6, collateral: [], locked: false, security: false, ...extra });
const quoteKey = (d) => `${d.targetId || ""}|${[...d.offer].sort((a, b) => a - b).join(",")}|${[...d.request].sort((a, b) => a - b).join(",")}`;
const joinFor = (s, extra = {}) => {
  const summary = extra.summary || summaryOf(s, { exclude: ["Robin"] });
  const taken = new Set(summary.players.map((player) => player.tokenKind));
  return { code: s.code, lockedCode: true, name: "Priya", tokenKind: TOKENS.find((kind) => !taken.has(kind)), summary, ...extra };
};
const fullSet = (s, who, indexes, houses = 0) => setProps(s, Object.fromEntries(indexes.map((index) => [index, { ownerId: idOf(s, who), houses, mortgaged: false }])));

// UX round scenes: a fixed game clock (state.now) so the auction clock, IDLE m:ss and the stand-in
// timer render the same every time; log lines carry `at` on that clock.
const NOW = 1793560000000;
const logAt = (s, lines) => ({ ...s, log: [...(s.log || []), ...lines.map(([text, at], k) => ({ id: `log_ux_${k}`, text, at }))] });
const timed = (s, auction) => {
  const ids = Object.fromEntries(["Robin", "Sam", "Alpha", "Maya"].map((name) => [name, idOf(s, name)]));
  let next = { ...s, now: NOW, rules: { ...(s.rules || {}), auctionClock: 8 } };
  for (const [name, cash] of [["Robin", 1020], ["Sam", 1145], ["Alpha", 442], ["Maya", 683]]) next = setPlayer(next, name, { cash });
  next = setPlayer(next, "Robin", { position: 24 });
  return logAt({ ...next, auction: { spaceIndex: 24, turnPos: 0, returnPhase: "turn_complete", currentPlayerId: null, clockMs: 8000, endsAt: NOW + 6000, ...auction(ids) } },
    [["Galaxy Avenue entered an open auction", NOW - 6000], ["Alpha bid $160", NOW - 4000], ["Sam bid $180", NOW - 2600]]);
};
const samIdle = (s) => logAt({ ...s, now: NOW, phase: "awaiting_roll", currentPlayerId: idOf(s, "Sam"), lastLanding: null, turn: { playerId: idOf(s, "Sam"), dice: null, doublesCount: 0 } }, [["Sam's turn", NOW - 100000]]);
const rentX = (s, index, landing, card) => setProps(setPlayer(logAt({ ...s, now: NOW, lastCard: { deck: "chance", text: card }, lastLanding: { id: "landing_ux", playerId: s.viewerId, spaceIndex: index, kind: "rent", recipientId: idOf(s, "Sam"), ...landing } },
  [[`${meOf(s).name} paid ${landing.amount} rent`, NOW - 1000]]), s.viewerId, { position: index }), { [index]: { ownerId: idOf(s, "Sam"), houses: 0, mortgaged: false } });
const away = (s, { autopilot, until = 0 }) => {
  const me = meOf(s);
  const mark = (s.log || []).at(-1)?.id || "";
  const next = logAt({ ...s, now: NOW }, [[`${me.name} rolled 3 + 4 = 7`, NOW - 90000], [`${me.name} landed on City Park`, NOW - 89000], [`${me.name} ended the turn`, NOW - 88000]]);
  return setPlayer(next, me.id, { autopilot, standIn: { since: NOW - 120000, logId: mark, cash: me.cash, by: "idle", ...(until ? { until } : {}) } });
};

// ---- Scenes -----------------------------------------------------------------------------------
// { id, title, section, fixture, session?, spectator?, tab?, patch?(s), ui?(s), then?, capture?, note? }
//   ui(s) → { join, games ("demo" | list), lastTable, trade, plan, loanDraft, inviteOpen, tableOnline,
//             muted, audio, prefs, friends ("fixture"), sent, copied, server, remote, invitedView, outSeen }
//   then  → { sheet: square } | { confirm: "leave" | "bankrupt" | "end-game" } | { settings } | { toast, error } | { cue } |
//           { offline } | { stamp } | { next(s) → state played as the next state }
//   capture → { when: "<JS condition>" } for animation frames (default: settle, then shoot)

export const SCENES = [
  // ---------------------------------------------------------------- Start
  { id: "start-loading", title: "Opening the app (Finding your table…)", section: "Start", boot: true },
  { id: "home-loading", title: "Home · looking for games", section: "Start", session: false, ui: () => ({ games: { list: null, error: "" } }) },
  { id: "home-games", title: "Home · active games", section: "Start", session: false, ui: () => ({ games: "demo" }) },
  { id: "home-empty", title: "Home · no games yet", section: "Start", session: false, ui: () => ({ games: { list: [], error: "" } }) },
  { id: "home-back", title: "Home · BACK TO TABLE (last table not listed)", section: "Start", session: false, ui: () => ({ games: { list: [], error: "" }, lastTable: { code: "NXGNT" } }) },
  { id: "home-code-typing", title: "Home · typing a table code", section: "Start", session: false, ui: () => ({ games: "demo", join: { code: "NXG" } }) },
  { id: "home-no-table", title: "Home · no table with that code", section: "Start", session: false, ui: () => ({ games: { list: [], error: "" }, join: { code: "ZZZZZ", missing: true, error: "No table ZZZZZ. Check the code on the TV." } }) },
  { id: "home-closed", title: "Home · the last table has closed", section: "Start", session: false, ui: () => ({ games: "demo", join: { note: "Table LW262 has closed." } }) },
  { id: "new-game", title: "New game · name and token", section: "Start", session: false, ui: () => ({ join: { mode: "create", name: "Ana", tokenKind: "rocket" } }) },
  { id: "new-game-error", title: "New game · name missing", section: "Start", session: false, ui: () => ({ join: { mode: "create", name: "", tokenKind: "rocket", error: "Enter your name." } }) },
  { id: "join-link", title: "Join from a link or QR · name and token", section: "Start", fixture: "lobby", session: false, ui: (s) => ({ join: joinFor(s) }), note: "Same screen over the internet or on the same Wi-Fi (the difference is only in the invite link, see lobby-invite-lan)." },
  { id: "join-typed-code", title: "Join with a typed code · name and token", section: "Start", fixture: "lobby", session: false, ui: (s) => ({ join: joinFor(s, { lockedCode: false }) }) },
  { id: "join-busy", title: "Join · joining…", section: "Start", fixture: "lobby", session: false, ui: (s) => ({ join: joinFor(s, { busy: true }) }) },
  { id: "join-error", title: "Join · error (table full)", section: "Start", fixture: "lobby", session: false, ui: (s) => ({ join: joinFor(s, { error: "This table is full" }) }) },
  { id: "invited", title: "Invited over the internet (landing page)", section: "Start", fixture: "lobby", session: false, ui: (s) => ({ remote: true, invitedView: true, join: joinFor(s, { summary: { ...summaryOf(s), players: summaryOf(s).players.slice(0, 3) } }) }) },
  { id: "who-started", title: "WHO ARE YOU? · game in play", section: "Start", fixture: "waiting", session: false, ui: (s) => ({ join: { code: s.code, lockedCode: true, summary: summaryOf(s, { offline: ["Robin"] }) } }) },
  { id: "who-lobby", title: "WHO ARE YOU? · in the lobby", section: "Start", fixture: "lobby", session: false, ui: (s) => ({ join: { code: s.code, lockedCode: true, name: "Sam", summary: summaryOf(s, { offline: ["Sam"] }) } }) },
  { id: "who-taking", title: "WHO ARE YOU? · taking the seat", section: "Start", fixture: "waiting", session: false, ui: (s) => ({ join: { code: s.code, lockedCode: true, busy: true, reclaim: { playerId: idOf(s, "Robin") }, summary: summaryOf(s, { offline: ["Robin"] }) } }) },
  { id: "who-409", title: "WHO ARE YOU? · that seat is still connected (409)", section: "Start", fixture: "waiting", session: false, ui: (s) => ({ join: { code: s.code, lockedCode: true, reclaim: { playerId: idOf(s, "Sam"), status: 409, error: "That seat is still connected." }, summary: summaryOf(s, { offline: ["Robin"] }) } }) },
  { id: "who-moved", title: "Your seat moved to another phone", section: "Start", fixture: "waiting", session: false, ui: (s) => ({ join: { code: s.code, lockedCode: true, note: "Your seat moved to another phone.", summary: summaryOf(s) } }) },
  { id: "who-old-server", title: "Older server · GET MY SEAT BACK", section: "Start", fixture: "waiting", session: false, ui: (s) => ({ join: { code: s.code, lockedCode: true, summary: summaryOf(s, { ids: false }) } }) },

  // ---------------------------------------------------------------- Lobby
  { id: "lobby-host", title: "Lobby · host, no PC", section: "Lobby", fixture: "lobby", ui: () => ({ tableOnline: false }) },
  { id: "lobby-host-pc", title: "Lobby · host, a PC shows the table", section: "Lobby", fixture: "lobby", ui: () => ({ tableOnline: true }) },
  { id: "lobby-alone", title: "Lobby · host alone (START needs 2)", section: "Lobby", fixture: "lobby", patch: (s) => ({ ...s, players: s.players.filter((player) => player.id === s.viewerId) }), ui: () => ({ tableOnline: false }) },
  {
    id: "lobby-full", title: "Lobby · full table (6 seats)", section: "Lobby", fixture: "lobby", ui: () => ({ tableOnline: false }),
    patch: (s) => {
      const free = TOKENS.filter((kind) => !s.players.some((player) => player.tokenKind === kind));
      const extra = ["Leo", "Eve"].map((name, k) => ({ ...s.players[2], id: `player_scene_${k}`, name, tokenKind: free[k], color: undefined }));
      return { ...s, players: [...s.players, ...extra] };
    },
  },
  { id: "lobby-rules", title: "Lobby · house rules on (loans)", section: "Lobby", fixture: "lobby-rules", ui: () => ({ tableOnline: false }) },
  { id: "lobby-guest", title: "Lobby · guest (waiting for the host)", section: "Lobby", fixture: "lobby", patch: (s) => ({ ...s, hostId: idOf(s, "Sam"), viewerIsHost: false }), ui: () => ({ tableOnline: false }) },
  { id: "lobby-spectator", title: "Lobby · watching", section: "Lobby", fixture: "lobby", spectator: true, ui: () => ({ tableOnline: true }) },
  { id: "lobby-invite", title: "Invite sheet · QR, link and friends online", section: "Lobby", fixture: "lobby", ui: () => ({ inviteOpen: true, friends: "fixture" }), note: "The QR is a placeholder in demos (it is served by the server)." },
  { id: "lobby-invite-lan", title: "Invite sheet · same Wi-Fi link", section: "Lobby", fixture: "lobby", ui: () => ({ inviteOpen: true, server: { lanURL: "http://192.168.0.12:3210", publicURL: null } }) },
  { id: "lobby-rematch", title: "Lobby after PLAY AGAIN (same seats and rules)", section: "Lobby", fixture: "lobby-rules", ui: () => ({ tableOnline: false }), note: "Every phone lands here when the host taps PLAY AGAIN; same screen as a fresh lobby." },

  // ---------------------------------------------------------------- Turn
  {
    id: "order-first", title: "Roll for turn order · your roll", section: "Turn", fixture: "order",
    patch: (s) => ({ ...s, openingOrder: { ...s.openingOrder, round: 1, pendingIds: [s.viewerId, idOf(s, "Maya")], entries: s.openingOrder.entries.map((entry) => ([s.viewerId, idOf(s, "Maya")].includes(entry.playerId) ? { ...entry, rolls: [] } : entry)) } }),
  },
  {
    id: "order-waiting", title: "Roll for turn order · waiting for others", section: "Turn", fixture: "order",
    patch: (s) => ({ ...s, openingOrder: { ...s.openingOrder, round: 1, pendingIds: [idOf(s, "Maya")], entries: s.openingOrder.entries.map((entry) => (entry.playerId === idOf(s, "Maya") ? { ...entry, rolls: [] } : entry.playerId === s.viewerId ? { ...entry, rolls: [entry.rolls[0]] } : entry)) } }),
  },
  { id: "order-tiebreak", title: "Roll for turn order · tie-break", section: "Turn", fixture: "order" },
  { id: "order-set", title: "Turn order set (countdown)", section: "Turn", fixture: "order-countdown" },
  { id: "turn-stamp", title: "YOUR TURN stamp (turn starts)", section: "Turn", fixture: "roll", then: { stamp: true } },
  { id: "your-turn", title: "Your turn · roll", section: "Turn", fixture: "roll" },
  { id: "dice-frame", title: "Dice rolling (animation frame)", section: "Turn", replay: 13, capture: { when: "document.querySelector('[data-roll] .die')" } },
  { id: "hop-frame", title: "Token hopping, camera following (animation frame)", section: "Turn", replay: 13, capture: { when: "(() => { const g = document.querySelector('#board .board-viewport > .board'); const t = g && getComputedStyle(g).transform; return t && t !== 'none' && !/matrix\\(1, 0, 0, 1, 0, 0\\)/.test(t); })()" } },
  { id: "after-hop", title: "After a hop (landed and paid)", section: "Turn", replay: 13, capture: { when: "!window.__phone.ui.draining && !window.__phone.ui.playing && window.__phone.ui.state?.phase === 'turn_complete'" } },
  {
    id: "pass-go", title: "Passing PAYDAY (+$200 chip)", section: "Turn", fixture: "roll",
    patch: (s) => setPlayer(s, s.viewerId, { position: 37 }),
    then: {
      next: (s) => {
        const me = meOf(s);
        const moved = addLog(setPlayer(s, me.id, { position: 4 }), [`${me.name} rolled 3 + 4 = 7`, `${me.name} passed PAYDAY and collected $200`, `${me.name} landed on Stamp Duty`, `${me.name} paid $200 (Stamp Duty)`]);
        return { ...moved, phase: "turn_complete", version: (s.version || 0) + 1, turn: { playerId: me.id, dice: [3, 4], doublesCount: 0, movements: [{ id: "move_scene_go", playerId: me.id, from: 37, to: 4, path: [37, 38, 39, 0, 1, 2, 3, 4] }] }, lastLanding: { id: "landing_scene", playerId: me.id, spaceIndex: 4, kind: "tax", amount: 200, recipientId: null } };
      },
    },
    capture: { when: "document.querySelector('.rise-chip--pos')" },
  },
  { id: "doubles", title: "Doubles · roll again", section: "Turn", fixture: "doubles" },
  { id: "land-go", title: "Landed on PAYDAY", section: "Turn", fixture: "turn-done", patch: (s) => landed(s, 0, "go", { amount: 200 }) },
  { id: "land-tax", title: "Paid tax", section: "Turn", fixture: "turn-done", patch: (s) => landed(s, 4, "tax", { amount: 200 }) },
  {
    id: "go-to-jail", title: "Sent to jail", section: "Turn", fixture: "roll",
    patch: (s) => addLog(setPlayer({ ...s, phase: "turn_complete", lastLanding: { id: "landing_scene", playerId: s.viewerId, spaceIndex: 30, kind: "jail", amount: 0 } }, s.viewerId, { position: 10, inJail: true, jailTurns: 0 }), [`${meOf(s).name} landed on Busted`, `${meOf(s).name} was sent to jail`]),
  },
  { id: "land-parking", title: "City Park · free rest", section: "Turn", fixture: "turn-done", patch: (s) => landed(s, 20, "freeParking", { amount: 0 }) },
  { id: "land-jackpot", title: "City Park jackpot", section: "Turn", fixture: "turn-done", patch: (s) => ({ ...landed(s, 20, "freeParking", { amount: 140 }), rules: { ...(s.rules || {}), freeParkingJackpot: true } }) },
  { id: "land-own", title: "Landed on your own street", section: "Turn", fixture: "turn-done", patch: (s) => landed(s, 19, "own") },
  { id: "land-mortgaged", title: "Landed on a mortgaged street", section: "Turn", fixture: "turn-done", patch: (s) => setProps(landed(s, 3, "mortgaged"), { 3: { mortgaged: true } }) },
  { id: "card-confirm", title: "Headlines card · confirm the move", section: "Turn", fixture: "card" },
  { id: "card-result", title: "Post card · result", section: "Turn", fixture: "turn-done", patch: (s) => ({ ...landed(s, 17, "chest"), lastCard: { deck: "chest", text: "Refund letter: you were overcharged. Collect $200" } }) },
  { id: "jail-options", title: "In jail · roll, pay $50 or use a card", section: "Turn", fixture: "jail" },
  { id: "jail-no-cash", title: "In jail · not enough for bail", section: "Turn", fixture: "jail", patch: (s) => setPlayer(s, s.viewerId, { cash: 30, jailCards: 0 }) },
  { id: "jail-last-try", title: "In jail · last try", section: "Turn", fixture: "jail", patch: (s) => setPlayer(s, s.viewerId, { jailTurns: 2, jailCards: 0 }) },
  { id: "done-raise-cash", title: "Turn done · short of cash (RAISE CASH)", section: "Turn", fixture: "turn-done", patch: (s) => setPlayer(s, s.viewerId, { cash: 20 }) },
  {
    id: "done-build-plan", title: "Turn done · BUILD chip and SEND TRADE chip", section: "Turn", fixture: "turn-done",
    patch: (s) => fullSet(s, s.viewerId, [37, 39]),
    ui: (s) => ({ plan: draft({ targetId: idOf(s, "Sam"), offer: ownedBy(s, s.viewerId).filter((index) => ![37, 39].includes(index)).slice(0, 1), request: ownedBy(s, idOf(s, "Sam")).slice(0, 1), offerCash: 50, planned: true }) }),
  },
  { id: "watch-rolling", title: "Someone else's turn · about to roll", section: "Turn", fixture: "waiting", patch: (s) => ({ ...s, phase: "awaiting_roll", turn: { ...s.turn, playerId: s.currentPlayerId, dice: null, doublesCount: 0 } }) },
  { id: "watch-doubles", title: "Someone else rolled doubles", section: "Turn", fixture: "waiting", patch: (s) => ({ ...s, phase: "awaiting_roll", turn: { ...s.turn, playerId: s.currentPlayerId, dice: [3, 3], doublesCount: 1 } }) },
  { id: "watch-buying", title: "Someone else deciding to buy", section: "Turn", fixture: "waiting", patch: (s) => ({ ...s, phase: "awaiting_purchase", pendingPurchase: freeStreets(s)[0] }) },
  { id: "watch-card", title: "Someone else reading a card", section: "Turn", fixture: "waiting", patch: (s) => ({ ...s, phase: "card_confirmation", pendingCard: { playerId: s.currentPlayerId, deck: "chance", cardId: "chance-go", text: "Bonus season comes early: advance to PAYDAY and collect $200" } }) },
  { id: "watch-jail", title: "Someone else in jail", section: "Turn", fixture: "waiting", patch: (s) => setPlayer({ ...s, phase: "awaiting_roll", turn: { ...s.turn, dice: null } }, s.currentPlayerId, { inJail: true, jailTurns: 1, position: 10 }) },
  { id: "watch-debt", title: "Someone else owes money", section: "Turn", fixture: "waiting", patch: (s) => ({ ...s, phase: "debt", debt: { payerId: s.currentPlayerId, amount: 300, creditorId: idOf(s, "Sam"), reason: "rent" } }) },
  { id: "watch-trade", title: "Two other players trading", section: "Turn", fixture: "waiting", patch: (s) => ({ ...s, phase: "trade", trade: { proposerId: idOf(s, "Sam"), targetId: idOf(s, "Maya"), offer: { cash: 0, properties: [ownedBy(s, idOf(s, "Sam"))[0]] }, request: { cash: 100, properties: [] } } }) },
  { id: "watch-loan", title: "Two other players with a loan offer", section: "Turn", fixture: "waiting", patch: (s) => ({ ...s, phase: "loan_offer", loanOffer: { id: "loanoffer_scene", proposerId: idOf(s, "Sam"), targetId: s.currentPlayerId, direction: "lend", lenderId: idOf(s, "Sam"), borrowerId: s.currentPlayerId, principal: 200, repay: 240, dueTurns: 6, collateral: [] } }) },
  { id: "watch-done", title: "Someone else's turn · what happened", section: "Turn", fixture: "waiting" },
  { id: "spectator", title: "Watching (spectator)", section: "Turn", fixture: "waiting", spectator: true },
  { id: "out-watching", title: "You're out (bankrupt), watching", section: "Turn", fixture: "waiting", patch: (s) => setPlayer(s, s.viewerId, { bankrupt: true, cash: 0 }) },
  { id: "away-autopilot", title: "An away player: offline plate, computer playing (robot)", section: "Turn", fixture: "waiting", patch: (s) => setPlayer(setPlayer(s, "Sam", { online: false, autopilot: true }), "Maya", { online: false }) },

  // ---------------------------------------------------------------- Money
  { id: "buy", title: "Buy or auction", section: "Money", fixture: "buy" },
  { id: "buy-short", title: "Buy · not enough cash", section: "Money", fixture: "buy", patch: (s) => setPlayer(s, s.viewerId, { cash: 50 }) },
  { id: "buy-no-auction", title: "Buy · auctions off (PASS)", section: "Money", fixture: "buy", patch: (s) => ({ ...s, rules: { ...(s.rules || {}), auctions: false } }) },
  { id: "buy-completes", title: "Buy · completes your set", section: "Money", fixture: "buy", patch: (s) => fullSet(s, s.viewerId, groupOf(s, s.pendingPurchase).filter((index) => index !== s.pendingPurchase)) },
  { id: "bought", title: "Bought a street (YOURS NOW)", section: "Money", fixture: "turn-done" },
  { id: "auction-outbid", title: "Auction · outbid, your bid", section: "Money", fixture: "auction" },
  { id: "auction-first", title: "Auction · no bids yet, your bid", section: "Money", fixture: "auction", patch: (s) => ({ ...s, auction: { ...s.auction, currentBid: 0, bidderId: null } }) },
  { id: "auction-leading", title: "Auction · you lead, others bidding", section: "Money", fixture: "auction", patch: (s) => ({ ...s, auction: { ...s.auction, bidderId: s.viewerId, currentBid: 70, currentPlayerId: idOf(s, "Maya") } }) },
  { id: "auction-other", title: "Auction · someone else's bid", section: "Money", fixture: "auction", patch: (s) => ({ ...s, auction: { ...s.auction, currentPlayerId: idOf(s, "Alpha") } }) },
  { id: "auction-passed", title: "Auction · you passed", section: "Money", fixture: "auction", patch: (s) => ({ ...s, auction: { ...s.auction, activeIds: s.auction.activeIds.filter((id) => id !== s.viewerId), currentPlayerId: idOf(s, "Alpha") } }) },
  { id: "auction-broke", title: "Auction · you can't bid higher", section: "Money", fixture: "auction", patch: (s) => setPlayer(s, s.viewerId, { cash: 25 }) },
  { id: "auction-won", title: "Won the auction (YOURS NOW)", section: "Money", fixture: "turn-done", patch: (s) => ({ ...s, log: s.log.map((entry) => ({ ...entry, text: entry.text.replace(/ bought (Crown Avenue) for \$\d+$/, (match, name) => ` won ${name} for $230`) })) }) },
  {
    id: "auction-sold-other", title: "Sold at auction to someone else (SOLD · SAM)", section: "Money", fixture: "waiting",
    patch: (s) => {
      const index = s.lastLanding.spaceIndex;
      return setProps(trimTurnLog(s, / bought /), { [index]: { ownerId: idOf(s, "Sam") } });
    },
  },
  { id: "rent", title: "Rent paid", section: "Money", fixture: "rent" },
  { id: "rent-big", title: "Big rent paid", section: "Money", fixture: "rent", patch: (s) => ({ ...s, lastLanding: { ...s.lastLanding, amount: 950 } }) },
  { id: "debt-plan", title: "Debt · the plan", section: "Money", fixture: "debt" },
  { id: "debt-can-pay", title: "Debt · enough cash (PAY)", section: "Money", fixture: "debt", patch: (s) => setPlayer(s, s.viewerId, { cash: 600 }) },
  { id: "debt-short", title: "Debt · nothing left to sell", section: "Money", fixture: "debt", patch: (s) => setProps(s, Object.fromEntries(ownedBy(s, s.viewerId).map((index) => [index, { mortgaged: true, houses: 0 }]))) },
  { id: "debt-loans", title: "Debt · ask for a loan", section: "Money", fixture: "debt-loans" },
  { id: "bankrupt-confirm", title: "Declare bankruptcy? (confirm)", section: "Money", fixture: "debt", then: { confirm: "bankrupt" } },
  { id: "bankrupt-any-time", title: "Declare bankruptcy at any time, from MORE (confirm, 0.10.1)", section: "Money", fixture: "roll", tab: "friends", then: { confirm: "bankrupt" } },
  {
    id: "handover", title: "Someone goes out · their deeds come to you (animation frame)", section: "Money", fixture: "bankrupt",
    patch: (s) => {
      const out = s.lastBankruptcy;
      const before = setProps(setPlayer(s, out.playerId, { bankrupt: false, cash: 120 }), Object.fromEntries(out.properties.map((index) => [index, { ownerId: out.playerId }])));
      return { ...before, log: before.log.filter((entry) => !/declared bankruptcy/.test(entry.text)), lastBankruptcy: null };
    },
    then: { next: null, nextFromFixture: true },
    capture: { when: "document.querySelector('.pev--out')" },
  },

  // ---------------------------------------------------------------- Trade & loans
  { id: "trade-empty", title: "Trade · nobody picked", section: "Trade & loans", fixture: "roll", tab: "trade" },
  { id: "trade-partner", title: "Trade · partner picked", section: "Trade & loans", fixture: "roll", tab: "trade", ui: (s) => ({ trade: draft({ targetId: idOf(s, "Sam") }) }) },
  { id: "trade-ask", title: "Trade · filled, computer's price (ASK $82)", section: "Trade & loans", fixture: "roll", tab: "trade", ui: (s) => { const d = draft({ targetId: idOf(s, "Alpha"), offer: [19], request: [13, 14] }); return { trade: { ...d, quote: { key: quoteKey(d), offerCash: 82, requestCash: 0 } } }; } },
  { id: "trade-ask-used", title: "Trade · computer's price added", section: "Trade & loans", fixture: "roll", tab: "trade", ui: (s) => { const d = draft({ targetId: idOf(s, "Alpha"), offer: [19], request: [13, 14], offerCash: 82 }); return { trade: { ...d, quote: { key: quoteKey(d), offerCash: 82, requestCash: 0 } } }; } },
  { id: "trade-fair", title: "Trade · the computer would take it as it is", section: "Trade & loans", fixture: "roll", tab: "trade", ui: (s) => { const d = draft({ targetId: idOf(s, "Alpha"), offer: [39], request: [13] }); return { trade: { ...d, quote: { key: quoteKey(d), offerCash: 0, requestCash: 0 } } }; } },
  { id: "trade-pays", title: "Trade · the computer would pay you", section: "Trade & loans", fixture: "roll", tab: "trade", ui: (s) => { const d = draft({ targetId: idOf(s, "Alpha"), offer: [39], request: [] }); return { trade: { ...d, quote: { key: quoteKey(d), offerCash: 0, requestCash: 40 } } }; } },
  { id: "trade-short", title: "Trade · its price, $ more needed (0.10.1)", section: "Trade & loans", fixture: "roll", tab: "trade", ui: (s) => { const d = draft({ targetId: idOf(s, "Alpha"), offer: [19], request: [13, 14], offerCash: 50 }); return { trade: { ...d, quote: { key: quoteKey(d), offerCash: 82, requestCash: 0 } } }; } },
  { id: "trade-wont", title: "Trade · won't trade at any price (0.10.1)", section: "Trade & loans", fixture: "roll", tab: "trade", ui: (s) => { const d = draft({ targetId: idOf(s, "Alpha"), offer: [19], request: [13, 14] }); return { trade: { ...d, quote: { key: quoteKey(d), error: "No deal: that would give you a full set." } } }; } },
  { id: "trade-counter", title: "Trade · a computer's counter-offer (0.10.1)", section: "Trade & loans", fixture: "roll", tab: "trade",
    patch: (s) => addLog({ ...setProps(s, { 39: { ownerId: idOf(s, "Alpha") }, 28: { ownerId: s.viewerId } }), phase: "trade",
      trade: { id: "trade_9", proposerId: idOf(s, "Alpha"), targetId: s.viewerId, offer: { cash: 0, properties: [39] }, request: { cash: 650, properties: [28] }, returnPhase: "awaiting_roll",
        counterTo: "trade_8", counterRound: 1, original: { offer: { cash: 302, properties: [28] }, request: { cash: 0, properties: [39] } } } },
    [`Alpha countered ${meOf(s).name}'s offer`, "Alpha: I'd do it for $650 + Reservoir."]) },
  { id: "trade-not-selling", title: "Trade · a computer asks for your deed: NOT SELLING (0.10.1)", section: "Trade & loans", fixture: "roll", tab: "trade",
    patch: (s) => ({ ...setProps(s, { 28: { ownerId: s.viewerId } }), phase: "trade",
      trade: { id: "trade_7", proposerId: idOf(s, "Alpha"), targetId: s.viewerId, offer: { cash: 300, properties: [] }, request: { cash: 0, properties: [28] }, returnPhase: "awaiting_roll" } }) },
  { id: "trade-effect", title: "Trade · effect line (gives a set away)", section: "Trade & loans", fixture: "trade", tab: "trade", patch: (s) => ({ ...s, phase: "awaiting_roll", trade: null, currentPlayerId: s.viewerId }), ui: (s) => ({ trade: draft({ targetId: idOf(s, "Maya"), offer: [19], request: [23] }) }) },
  { id: "trade-no-deeds", title: "Trade · you own no deeds", section: "Trade & loans", fixture: "roll", tab: "trade", patch: (s) => setProps(s, Object.fromEntries(ownedBy(s, s.viewerId).map((index) => [index, { ownerId: null }]))), ui: (s) => ({ trade: draft({ targetId: idOf(s, "Sam") }) }) },
  { id: "trade-locked", title: "Trade · locked deeds (houses, loan security)", section: "Trade & loans", fixture: "active-loan", tab: "trade", patch: (s) => setProps(s, { [ownedBy(s, idOf(s, "Sam"))[0]]: { houses: 1 } }), ui: (s) => ({ trade: draft({ targetId: idOf(s, "Sam") }) }) },
  { id: "trade-dropped", title: "Trade · a deed changed hands and left the deal", section: "Trade & loans", fixture: "roll", tab: "trade", ui: (s) => ({ trade: draft({ targetId: idOf(s, "Sam"), offer: [19, ownedBy(s, idOf(s, "Alpha"))[0]] }) }) },
  { id: "trade-save", title: "Trade · not your turn (SAVE FOR MY TURN)", section: "Trade & loans", fixture: "roll", tab: "trade", patch: notMyTurn, ui: (s) => ({ trade: draft({ targetId: idOf(s, "Sam"), offer: [19], request: [3], offerCash: 50 }) }) },
  { id: "trade-planned", title: "Trade · a plan saved for your turn (card)", section: "Trade & loans", fixture: "roll", tab: "trade", patch: notMyTurn, ui: (s) => ({ plan: draft({ targetId: idOf(s, "Sam"), offer: [19], request: [3], planned: true }) }) },
  { id: "trade-plan-ready", title: "Your turn · SEND TRADE chip for the plan", section: "Trade & loans", fixture: "roll", ui: (s) => ({ plan: draft({ targetId: idOf(s, "Sam"), offer: [19], request: [3], planned: true }) }) },
  { id: "trade-impact", title: "Trade · building it with WHAT THIS DEAL DOES open", section: "Trade & loans", fixture: "roll", tab: "trade", ui: (s) => ({ tradeInfo: true, trade: draft({ targetId: idOf(s, "Alpha"), offer: [19], request: [13, 14], offerCash: 50 }) }) },
  { id: "trade-sent", title: "Offer sent · waiting for the answer", section: "Trade & loans", fixture: "trade-sent" },
  { id: "trade-incoming", title: "Incoming trade offer", section: "Trade & loans", fixture: "trade" },
  { id: "trade-incoming-impact", title: "Incoming offer · verdicts, impact and map open", section: "Trade & loans", fixture: "trade", ui: () => ({ tradeInfo: true }) },
  { id: "trade-accepted", title: "Your trade was accepted (cue)", section: "Trade & loans", fixture: "roll", then: { cue: { kind: "trade", icon: "deal", text: "Sam accepted your trade", tone: "pos" } } },
  { id: "trade-declined", title: "Your trade was declined (cue)", section: "Trade & loans", fixture: "roll", then: { cue: { kind: "trade", icon: "cross", text: "Alpha declined the trade", tone: "" } } },
  { id: "loan-lend", title: "Loan · lend (stepper and chips)", section: "Trade & loans", fixture: "active-loan", tab: "trade", ui: (s) => ({ loanDraft: loan({ targetId: idOf(s, "Sam") }) }) },
  { id: "loan-borrow", title: "Loan · borrow while in debt", section: "Trade & loans", fixture: "debt-loans", ui: (s) => ({ loanDraft: loan({ direction: "borrow", targetId: idOf(s, "Sam"), principal: 160, locked: true }) }) },
  { id: "loan-security", title: "Loan · borrow with security", section: "Trade & loans", fixture: "active-loan", tab: "trade", ui: (s) => ({ loanDraft: loan({ direction: "borrow", targetId: idOf(s, "Sam"), security: true, collateral: [39] }) }) },
  { id: "loan-offer", title: "A loan offered to you", section: "Trade & loans", fixture: "loan-offer" },
  {
    id: "loan-asked", title: "Someone asks to borrow from you", section: "Trade & loans", fixture: "loan-offer",
    patch: (s) => ({ ...s, loanOffer: { ...s.loanOffer, direction: "borrow", lenderId: s.viewerId, borrowerId: s.loanOffer.proposerId } }),
  },
  { id: "loan-waiting", title: "You asked for a loan · waiting", section: "Trade & loans", fixture: "loan-request" },
  { id: "loans-active", title: "Active loans with REPAY (MORE)", section: "Trade & loans", fixture: "active-loan", tab: "friends" },
  { id: "loan-due", title: "A loan due next turn", section: "Trade & loans", fixture: "active-loan", tab: "friends", patch: (s) => ({ ...s, turnCounts: { ...(s.turnCounts || {}), [s.viewerId]: 13 } }) },
  { id: "loan-repaid", title: "A loan repaid to you (cue)", section: "Trade & loans", fixture: "active-loan", then: { cue: { kind: "loan", icon: "loan", text: "Sam repaid you", tone: "pos", amount: 240 } } },

  // ---------------------------------------------------------------- Deeds
  { id: "deeds", title: "MY DEEDS", section: "Deeds", fixture: "roll", tab: "property" },
  { id: "deeds-empty", title: "MY DEEDS · none yet", section: "Deeds", fixture: "roll", tab: "property", patch: (s) => setProps(s, Object.fromEntries(ownedBy(s, s.viewerId).map((index) => [index, { ownerId: null }]))) },
  { id: "deeds-buildings", title: "MY DEEDS · full sets, houses, a hotel, BUILD", section: "Deeds", fixture: "roll", tab: "property", patch: (s) => setProps(fullSet(fullSet(s, s.viewerId, [37, 39]), s.viewerId, [16, 18, 19], 1), { 39: { houses: 5 }, 37: { houses: 4 } }) },
  { id: "deeds-mortgaged", title: "MY DEEDS · mortgaged, UNMORTGAGE", section: "Deeds", fixture: "jail", tab: "property" },
  { id: "deeds-view-only", title: "MY DEEDS · not your turn (view only)", section: "Deeds", fixture: "roll", tab: "property", patch: (s) => notMyTurn(setProps(fullSet(s, s.viewerId, [16, 18, 19], 2), {})) },
  { id: "deeds-loans", title: "MY DEEDS · loans, security, jail pass", section: "Deeds", fixture: "active-loan", tab: "property", patch: (s) => setPlayer(s, s.viewerId, { jailCards: 1 }) },
  { id: "sheet-yours-build", title: "Deed sheet · yours, build and sell", section: "Deeds", fixture: "roll", patch: (s) => setProps(fullSet(s, s.viewerId, [16, 18, 19], 1), {}), then: { sheet: 19 } },
  { id: "sheet-yours-mortgaged", title: "Deed sheet · yours, mortgaged", section: "Deeds", fixture: "roll", patch: (s) => setProps(s, { 39: { mortgaged: true } }), then: { sheet: 39 } },
  { id: "sheet-other", title: "Deed sheet · someone else's", section: "Deeds", fixture: "roll", then: { sheet: 3 } },
  { id: "sheet-unowned", title: "Deed sheet · for sale", section: "Deeds", fixture: "roll", then: { sheet: "free" } },
  { id: "sheet-railroad", title: "Deed sheet · a railroad", section: "Deeds", fixture: "roll", then: { sheet: 5 } },
  { id: "sheet-utility", title: "Deed sheet · a utility", section: "Deeds", fixture: "roll", then: { sheet: 12 } },
  { id: "sheet-corner", title: "Square sheet · jail, with who is there", section: "Deeds", fixture: "roll", patch: (s) => setPlayer(s, "Sam", { position: 10, inJail: true }), then: { sheet: 10 } },
  { id: "sheet-tax", title: "Square sheet · tax", section: "Deeds", fixture: "roll", then: { sheet: 4 } },
  { id: "players", title: "PLAYERS (spectators and players who are out)", section: "Deeds", fixture: "waiting", spectator: true, tab: "players" },

  // ---------------------------------------------------------------- Menus & sheets
  { id: "more", title: "MORE · table code, rules, invite, leave, sound", section: "Menus & sheets", fixture: "waiting", tab: "friends" },
  { id: "how-to-play", title: "HOW TO PLAY · five steps (from MORE)", section: "Menus & sheets", fixture: "waiting", tab: "friends", then: { help: true } },
  { id: "how-to-play-first", title: "HOW TO PLAY · shown once on the first join (LET'S PLAY)", section: "Menus & sheets", fixture: "lobby", ui: () => ({ tableOnline: false }), then: { help: "first" } },
  { id: "settings-host", title: "SETTINGS · host in a game (computer speed)", section: "Menus & sheets", fixture: "waiting", tab: "friends", patch: (s) => ({ ...s, roomSettings: { computerSpeed: "normal" } }), then: { settings: true } },
  { id: "settings-custom", title: "SETTINGS · music on, effects off, fast, reduced motion (guest)", section: "Menus & sheets", fixture: "waiting", tab: "friends", patch: (s) => ({ ...s, hostId: idOf(s, "Sam"), roomSettings: { computerSpeed: "fast" } }), ui: () => ({ audio: { music: true, effects: false, volume: 0.5 }, prefs: { vibration: false, speed: "fast", follow: false, motion: "reduce" } }), then: { settings: true } },
  { id: "settings-sound-off", title: "SETTINGS · all sound off", section: "Menus & sheets", fixture: "roll", ui: () => ({ muted: true }), then: { settings: true } },
  { id: "settings-lobby", title: "SETTINGS from the lobby gear", section: "Menus & sheets", fixture: "lobby", patch: (s) => ({ ...s, roomSettings: { computerSpeed: "slow" } }), ui: () => ({ tableOnline: false }), then: { settings: true } },
  { id: "settings-home", title: "SETTINGS from the home screen gear", section: "Menus & sheets", session: false, ui: () => ({ games: "demo" }), then: { settings: true } },
  { id: "lobby-leave", title: "Leave the lobby? (confirm)", section: "Menus & sheets", fixture: "lobby", ui: () => ({ tableOnline: false }), then: { confirm: "leave" } },
  { id: "end-game-confirm", title: "End the game for everyone? (host confirm, names the leader)", section: "Menus & sheets", fixture: "waiting", tab: "friends", then: { confirm: "end-game" } },
  { id: "more-spectator", title: "MORE · spectator", section: "Menus & sheets", fixture: "waiting", spectator: true, tab: "friends" },
  { id: "leave-confirm", title: "Leave the table? (confirm)", section: "Menus & sheets", fixture: "waiting", tab: "friends", then: { confirm: "leave" } },
  { id: "invite-in-game", title: "Invite sheet in a game · friends online", section: "Menus & sheets", fixture: "waiting", tab: "friends", ui: () => ({ inviteOpen: true, friends: "fixture" }) },
  { id: "invite-sent", title: "Invite sheet · invite sent, link copied", section: "Menus & sheets", fixture: "waiting", tab: "friends", ui: () => ({ inviteOpen: true, friends: "fixture", sent: ["Priya"], copied: true }) },

  // ---------------------------------------------------------------- Notices & errors
  { id: "toast-autopilot", title: "Back after being away (the computer played for you)", section: "Notices & errors", fixture: "roll", then: { toast: "The computer played for you" } },
  { id: "toast-offline", title: "Lost the table · retrying", section: "Notices & errors", fixture: "waiting", then: { offline: true, toast: "Lost the table. Retrying…", error: true } },
  { id: "toast-error", title: "An action refused (error toast)", section: "Notices & errors", fixture: "waiting", then: { toast: "It's not your turn for that.", error: true } },
  { id: "toast-plan-ready", title: "Your planned trade is ready (toast)", section: "Notices & errors", fixture: "roll", ui: (s) => ({ plan: draft({ targetId: idOf(s, "Sam"), offer: [19], request: [3], planned: true }) }), then: { toast: "Your trade for Sam is ready: tap SEND TRADE" } },
  { id: "cue-sold", title: "Cue chip · you won an auction", section: "Notices & errors", fixture: "turn-done", then: { cue: { kind: "auction", icon: "hammer", text: "SOLD Crown Ave → You", tone: "neg", amount: -230 } } },
  { id: "cue-out", title: "Cue chip · someone is out, all to you", section: "Notices & errors", fixture: "bankrupt", then: { cue: { kind: "bankrupt", icon: "out", text: "Sam is out · all to you", tone: "pos" } } },

  // ---------------------------------------------------------------- UX round (boards UX-01..08, phone)
  { id: "ux-tokens", title: "Join · token grid, 4 OF 8 FREE, taken tiles named, kite NEW (UX-08)", section: "Start", fixture: "lobby", session: false, ui: (s) => ({ join: joinFor(s, { name: "Kai", tokenKind: "kite", summary: summaryOf(s) }) }) },
  { id: "ux-standin", title: "COMPUTER PLAYING · playing for you, I'M BACK (UX-07)", section: "Turn", fixture: "roll", patch: (s) => away({ ...setPlayer(s, s.viewerId, { position: 20 }), phase: "turn_complete", turn: { playerId: s.viewerId, dice: [3, 4], doublesCount: 0 }, lastLanding: { id: "landing_ux", playerId: s.viewerId, spaceIndex: 20, kind: "freeParking", amount: 0 } }, { autopilot: true }) },
  { id: "ux-youre-back", title: "YOU'RE BACK · what the computer did (UX-07)", section: "Turn", fixture: "roll", patch: (s) => away(setPlayer(s, s.viewerId, { position: 20 }), { autopilot: false, until: NOW - 2000 }) },
  { id: "ux-host-idle-board", title: "Host · idle bar on the board (SAM IDLE 1:40 · PLAYERS) (UX-02)", section: "Turn", fixture: "waiting", patch: samIdle },
  { id: "ux-auction-timed", title: "Timed auction · one tap = bid, clock running (UX-03)", section: "Money", fixture: "auction", patch: (s) => timed(s, (id) => ({ activeIds: [id.Sam, id.Alpha, id.Robin], passedIds: [id.Maya], currentBid: 180, bidderId: id.Sam, bids: { [id.Sam]: 180, [id.Alpha]: 160 } })) },
  { id: "ux-auction-timed-lead", title: "Timed auction · you lead, GOING ONCE (UX-03)", section: "Money", fixture: "auction", patch: (s) => timed(s, (id) => ({ activeIds: [id.Sam, id.Alpha, id.Robin], passedIds: [id.Maya], currentBid: 230, bidderId: id.Robin, endsAt: NOW + 2000, bids: { [id.Sam]: 180, [id.Alpha]: 160, [id.Robin]: 230 } })) },
  { id: "ux-auction-timed-passed", title: "Timed auction · you passed (UX-03)", section: "Money", fixture: "auction", patch: (s) => timed(s, (id) => ({ activeIds: [id.Sam, id.Alpha], passedIds: [id.Maya, id.Robin], currentBid: 180, bidderId: id.Sam, bids: { [id.Sam]: 180, [id.Alpha]: 160 } })) },
  { id: "ux-debt-none", title: "Debt · checklist with nothing ticked (FIND $) (UX-04)", section: "Money", fixture: "debt", ui: (s) => ({ debtPicks: { key: `${s.debt.payerId}:${s.debt.creditorId || "bank"}:${s.debt.amount}`, picked: [] } }) },
  { id: "ux-rent-x2", title: "Rent ×2 · a card's double railroad rent (UX-05)", section: "Money", fixture: "rent", patch: (s) => rentX(s, 15, { amount: 50, base: 25, multiplier: 2 }, "Strike called off: go to the nearest railroad; if owned, pay double rent") },
  { id: "ux-rent-x10", title: "Rent ×10 · a card's utility rent on a new roll (UX-05)", section: "Money", fixture: "rent", patch: (s) => rentX(s, 28, { amount: 70, base: 7, multiplier: 10, dice: [3, 4] }, "Heatwave: go to the nearest utility; if owned, pay ten times the dice total") },
  { id: "ux-loan-terms", title: "MY LOANS · the terms on one card, due in 2 turns (UX-04)", section: "Trade & loans", fixture: "active-loan", tab: "friends", patch: (s) => ({ ...s, turnCounts: { ...(s.turnCounts || {}), [s.viewerId]: 12 } }) },
  { id: "ux-host-players", title: "Host · PLAYERS in MORE, Sam idle, controls open (UX-02)", section: "Menus & sheets", fixture: "waiting", tab: "friends", patch: samIdle, ui: (s) => ({ hostOpen: idOf(s, "Sam") }) },
  { id: "ux-host-remove", title: "Host · REMOVE SAM? (what goes back to the bank) (UX-02)", section: "Menus & sheets", fixture: "waiting", tab: "friends", patch: (s) => setProps(samIdle(s), { 6: { ownerId: idOf(s, "Sam"), houses: 2 }, 8: { ownerId: idOf(s, "Sam"), houses: 2 }, 9: { ownerId: idOf(s, "Sam"), houses: 2 } }), ui: (s) => ({ hostOpen: idOf(s, "Sam") }), then: { remove: "Sam" } },

  // ---------------------------------------------------------------- End
  { id: "victory-host", title: "Game over · you won (host: PLAY AGAIN)", section: "End", fixture: "victory" },
  { id: "defeat-guest", title: "Game over · you lost (guest)", section: "End", fixture: "defeat", patch: (s) => ({ ...s, hostId: idOf(s, "Sam"), viewerIsHost: false }) },
  { id: "defeat-host", title: "Game over · you went bankrupt (host)", section: "End", fixture: "defeat" },
  { id: "final-deeds", title: "Final holdings (MY DEEDS after the game)", section: "End", fixture: "victory", tab: "property" },
  { id: "game-over-spectator", title: "Game over · spectator", section: "End", fixture: "victory", spectator: true },
];

export function sceneById(id) {
  return SCENES.find((scene) => scene.id === id) || null;
}

// The state and view a scene starts from (no DOM). `fixtures` maps fixture name → parsed JSON.
export function buildScene(scene, fixtures) {
  if (scene.boot || scene.replay !== undefined) return { state: null, ui: {} };
  const fixture = fixtures[scene.fixture || "roll"];
  let state = structuredClone(fixture.state);
  if (scene.spectator) state = { ...state, viewerId: null, viewerRole: "spectator", viewerName: "Guest" };
  if (scene.patch) state = scene.patch(state);
  const ui = scene.ui ? scene.ui(state) : {};
  return { state, ui };
}

// Fixture names a scene needs (for the demo loader and the checks).
export function sceneFixtures(scene) {
  if (scene.boot || scene.replay !== undefined) return [];
  return [scene.fixture || "roll"];
}
