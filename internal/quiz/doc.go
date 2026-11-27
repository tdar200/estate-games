// Package quiz is the rules engine for the quiz, the fourth game type of the
// Estate party server (after Estate, Texas hold'em and Ludo): a board of six
// categories of five clues, four choices that open on every phone at the same
// instant, the first correct tap wins, hidden gold squares where the finder
// bets alone, and a final round with secret bets (docs/QUIZ-API.md). It
// knows nothing about rooms, phones or sockets: the server
// (internal/server/quiz.go) calls it under its own lock, as it calls
// internal/ludo.
//
// The engine owns every timer. As in the poker engine, nothing sleeps here:
// NextDeadline says when the next step is due (a phase timer, a computer's
// move or tap, judging a clue) and the server calls Advance then. Every
// action first runs whatever is already due, so a tap that reaches the
// server a moment before its timer fires is judged against the right state.
//
// Fair answering (answer.go, §6) judges a tap by the time the person took,
// as their phone reports it, but never trusts more than the network can
// explain, and waits a moment after the first correct tap in case a faster
// one is still on its way from a slower connection.
//
// Secrets: the snapshot holds every answer, every gold square and every
// final bet, so it is for the server only. PublicState builds each viewer's
// state from scratch and puts in only what that viewer may see (view.go).
// Choices are shuffled per clue with the game's seeded random stream, which
// the snapshot carries, so a restored game deals, shuffles and plays its
// computers exactly as it would have.
//
// Files: settings.go (the host's rules), packs.go (the embedded question
// packs and their rules), deal.go (boards, gold squares, the final, and the
// room's memory of played categories), game.go (seating, actions, the end),
// phases.go (the phase machine and its timers), answer.go (taps and
// judging), bots.go (Easy, Normal and Hard computers), view.go (what each
// viewer sees).
package quiz
