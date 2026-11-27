// Package closest is the rules engine for Near Thing (the display name lives
// in Title; the code calls the game "closest"), the fifth game type of the Estate party
// server (after Estate, Texas hold'em, Ludo and the quiz). Every question has
// a whole-number answer; everyone types a guess on their phone; the guesses
// become a row of slots, low to high, the middle ones paying 2× and each step
// outward one more, up to 5×; everyone places two free chips (and may stake
// 1–3 of their points) on the slots in secret; then the answer is shown and
// the closest guess wins (docs/CLOSEST-API.md). It knows nothing about rooms,
// phones or sockets: the server (internal/server/closest.go) calls it under
// its own lock, as it calls internal/quiz.
//
// The engine owns every timer. Nothing sleeps here: NextDeadline says when
// the next step is due (a phase timer or a computer's move) and the server
// calls Advance then. Every action first runs whatever is already due, so a
// guess that reaches the server a moment after its timer should have fired
// meets the state it would have met.
//
// Secrets: the snapshot holds every dealt question with its answer, every
// guess and every bet, so it is for the server only. PublicState builds each
// viewer's state from scratch and puts in only what that viewer may see
// (view.go): no other player's guess before the line-up, no other player's
// chips before they are shown, no answer, fact or sources before the reveal,
// no question before it is asked.
//
// Files: settings.go (the host's rules), packs.go (the embedded question
// packs and their rules), deal.go (choosing a game's questions, and the
// room's memory of played ones), game.go (seating, actions, the end),
// phases.go (the phase machine and its timers), score.go (slots, odds and
// payouts), bots.go (Easy, Normal and Hard computers), view.go (what each
// viewer sees), format.go (numbers as the screens write them).
package closest

// Title is the game's display name, picked on 22 Nov 2026. This
// constant (with GAME_NAME in web/shared/closest-name.js) is the only place it
// is written.
const Title = "Near Thing"
