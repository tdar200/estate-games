// Package ai is a Go port of the computer player in src/ai.js.
//
// The port is decision-for-decision identical to the JavaScript AI. Parity is
// checked against fixtures recorded from the JS code (scripts/ai-fixtures.js
// writes testdata/ai/*.json; see fixtures_test.go).
//
// # Decoupling from the engine
//
// The package does not import internal/engine. It reads a snapshot of the
// game in its own view types (Game, Player, PropertyState, ...), whose JSON
// tags match the JS game object, i.e. the output of JSON.stringify(game) and
// of json.Marshal(*engine.Game). Build the snapshot with a JSON round trip:
//
//	data, _ := json.Marshal(engineGame)
//	var view ai.Game
//	_ = json.Unmarshal(data, &view)
//	action := ai.ChooseAction(&view, playerID) // nil == JS null
//
// Keys the AI does not read (decks, openingOrder, ...) are ignored; of the log
// only the text is decoded (see House rules). Nullable ids use "" for null.
// The board data is a private copy in board.go.
// The three game.js helpers the AI imports (aiTradeOnCooldown,
// auctionCurrentPlayerId, bankSupply) are reimplemented in helpers.go.
//
// # Exported API
//
//   - ChooseAction(g, playerID) *Action: chooseAiAction.
//   - QuoteTrade(g, proposerID, req) (Quote, error): quoteAiTrade, with the
//     JS error messages.
//   - AcceptsTrade(g, playerID) bool: aiAcceptsTrade.
//   - AcceptsLoan(g, playerID) bool: whether the AI accepts the pending
//     loanOffer (house rule; no JS counterpart).
//   - ChooseDebtAction(g, playerID) *Action: ChooseAction's way out of a
//     debt without the loan request (pay, sell, mortgage, else bankrupt), for
//     callers that cannot send offer_loan (no JS counterpart).
//
// Action marshals to the same JSON as the JS action objects, so it can be fed
// to the engine through its JSON form. Loan actions (offer_loan with
// Action.Loan terms, accept_loan, reject_loan, repay_loan with Action.LoanID)
// marshal to the engine's JSON for them.
//
// # House rules
//
// The engine's optional house rules (settings.rules, see docs/API.md §3a)
// have no counterpart in ai.js, so the AI's behaviour under them is new code
// in rules.go and loans.go, reached through hooks that do nothing with the
// default rules: a default game decodes to an empty Settings.Rules, no Loans
// and no LoanOffer, and every decision stays identical to src/ai.js (the
// fixture and engine-trace parity tests run unchanged). With the rules on:
//
//   - loans: answers loan offers addressed to it (accept_loan/reject_loan),
//     asks for a loan once per debt instead of selling buildings on a strong
//     set or going bankrupt, holds back cash for repayments due soon and repays
//     on time, and never trades or mortgages a pledged property or builds on
//     a colour group holding one. It reads the log text to remember a refused
//     request.
//   - auctions off: buys at list price when the property is worth it and half
//     its usual reserve remains, since declining no longer starts an auction.
//   - freeParkingJackpot and startingCash: no change.
//
// # Numerics
//
// JS numbers are float64. Values that are always integers in JS (cash, prices,
// counts, reserves) are Go ints. Wherever JS divides and then floors/ceils a
// non-negative integer, the Go code uses integer division, which gives the same
// result. The EV maths uses float64 with the same operand order as ai.js. All
// float products go through mul, so the compiler cannot fuse them into FMA
// instructions. Without it, GOAMD64=v3 (and arm64) builds turn a*b+c into
// VFMADD; with it, `GOAMD64=v3 go build -gcflags=-S ./internal/ai` emits no
// fused instruction. ai.js has no Math.round, so it needs no jsRound helper.
//
// Two deliberate differences follow rules the engine fixed. The cost of
// lifting a mortgage and the 10% transfer fee use the engine's integer maths
// (engine.UnmortgageCost and engine.MortgageTransferFee), not game.js's
// Math.ceil((price / 2) * 1.1), which charges $1 too much on some prices
// (review of 3 Nov 2026, B12). And in debt the AI also sells a hotel while
// the bank has fewer than four houses, which game.js refused and ai.js
// skipped (the engine breaks the hotel down to the houses left). The parity
// tests replay game.js recordings, so they switch both back to game.js for
// this package's tests (gameJSMaths and gameJSHotelShortage, main_test.go).
// Array.prototype.sort is stable, so the port uses sort.SliceStable with the
// same comparator, and JS Set iteration order (which decides float summation
// order) is kept by indexSet.
//
// # Side effect
//
// Like chooseAiAction, ChooseAction normalises g.Auction.TurnPos during an
// auction (auctionCurrentPlayerId mutates it). Nothing else in g is changed.
package ai
