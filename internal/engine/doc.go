// Package engine is a faithful Go port of the Classic Estate rules engine
// (src/board.js and src/game.js).
//
// The API mirrors the JS module exports: CreateGame, AddPlayer, RemovePlayer,
// SwitchPlayerToSpectator, SwitchSpectatorToPlayer, StartGame, PerformAction,
// CompleteOpeningOrder, CurrentPlayer, PublicState, AuctionCurrentPlayerID,
// AITradeOnCooldown, BankSupply, Board and Groups. Functions that throw in JS
// return an error whose message is the identical English text.
//
// State lives in exported structs (Game, Player, PropertyState, Turn, ...)
// whose JSON encoding matches the JS objects key for key, including null
// for unset values. Nullable ids are plain strings where "" means null.
//
// Determinism: randomness is injected via RNG (floats in [0,1), consumed
// with the same arithmetic as Math.random in game.js); time via Game.Clock
// (Unix milliseconds) and ids via Game.NewID. Replaying the floats recorded
// from the JS engine reproduces its games exactly; see TestTraceParity and
// scripts/trace.js.
package engine
