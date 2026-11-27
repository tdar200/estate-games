// Poker demo scenes for ?demo=<name> on the phone and the table screen, screenshots and tests.
// Real engine states come from web/fixtures/poker/*.json (go run ./cmd/poker-fixtures); a scene adds
// only phone UI on top (a ticked pre-action, the raise panel open) or, where the engine has no such
// fixture, derives one in memory (your turn at a full eight-seat table). Fixture files are never
// changed here.
//
//   buildPokerScene(name, load) → { state, pk, title }   load(name) returns a fixture ({state, …})

export const POKER_FIXTURE_DIR = "/fixtures/poker";

export async function fetchPokerFixture(name) {
  const safe = String(name || "").replace(/[^a-z0-9-]/gi, "");
  const response = await fetch(`${POKER_FIXTURE_DIR}/${safe}.json`, { cache: "no-store" });
  if (!response.ok) throw new Error(`Poker fixture "${safe}" not found`);
  return response.json();
}

const clone = (value) => JSON.parse(JSON.stringify(value));

// Two more players so a six-seat state fills all eight seats: Ana (a phone) and Otto (a computer),
// both folded before the flop. Jonah goes quiet and the stand-in plays for him.
export function eightSeats(state) {
  const s = clone(state);
  s.settings.maxPlayers = 8;
  const extra = [
    { id: "p7", name: "Ana", kind: "human", tokenKind: "anchor", input: "phone" },
    { id: "p8", name: "Otto", kind: "ai", tokenKind: "kite", input: "ai", difficulty: "normal" },
  ];
  for (const [index, base] of extra.entries()) {
    if (s.players.some((player) => player.id === base.id)) continue;
    s.players.push({
      ...base, seat: 6 + index, stack: 1500 + index * 230, bet: 0, status: "folded", inHand: true, hasCards: false, cards: null, shown: false,
      isButton: false, isSmallBlind: false, isBigBlind: false, odds: null, rebuys: 0, timeBank: 30, boughtIn: 1500, online: true,
      lastAction: { playerId: base.id, street: "preflop", type: "fold", amount: 0, to: 0 },
    });
  }
  const jonah = s.players.find((player) => player.name === "Jonah");
  if (jonah) {
    jonah.online = false;
    jonah.autopilot = true;
  }
  s.players.sort((a, b) => a.seat - b.seat);
  return s;
}

export const POKER_SCENES = [
  { name: "poker-8-pre", title: "8 seats: someone else to act, CALL $50 ticked", base: "poker-flop8-phone", pk: { pre: { kind: "call", amount: 50 } } },
  { name: "poker-8-turn", title: "8 seats: your turn facing a bet", base: "poker-flop-phone", make: eightSeats },
  { name: "poker-8-raise", title: "8 seats: your turn, raise panel open", base: "poker-flop-phone", make: eightSeats, pk: { raiseOpen: true, raiseTo: 235 } },
  { name: "poker-8-showdown", title: "8 seats: showdown with a side pot", base: "poker-showdown-phone", make: eightSeats },
  { name: "poker-8-runout", title: "8 seats: all-in run-out with win chances", base: "poker-runout-phone", make: eightSeats },
  { name: "poker-8-table", title: "Table screen: 8 seats on the flop", base: "poker-flop8-table" },
  { name: "poker-lobby-guest", title: "Lobby on a guest phone (Sam)", base: "poker-lobby-phone", make: (state) => { const s = clone(state); s.viewerId = "p2"; s.viewerName = "Sam"; s.viewerIsHost = false; s.you = { ...s.you, id: "p2", seat: 1, isHost: false }; return s; } },
];

export function pokerSceneByName(name) {
  return POKER_SCENES.find((scene) => scene.name === name) || null;
}

// A scene (or a plain fixture name) → { state, pk, title }.
export async function buildPokerScene(name, load = fetchPokerFixture) {
  const scene = pokerSceneByName(name);
  if (!scene) {
    const fixture = await load(name);
    return { state: fixture.state, pk: {}, title: fixture.meta?.description || name };
  }
  const fixture = await load(scene.base);
  const state = scene.make ? scene.make(fixture.state) : clone(fixture.state);
  const pk = { ...(scene.pk || {}) };
  if (pk.pre) pk.pre = { ...pk.pre, hand: state.handNumber || 0 };
  return { state, pk, title: scene.title };
}
