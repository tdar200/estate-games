// Package gamerecord is the Estate game record: a complete, replayable
// account of one game (docs/GAME-RECORDS.md).
//
// A record holds the game as it stood just before it started (the lobby
// snapshot) and then every change to it, in order: each applied action with
// who sent it, the random numbers it drew, the ids it created, the time it was
// applied and a hash of the state it produced. Replaying the steps through
// internal/engine reproduces the game exactly, so the replay never depends on
// re-seeding anything; Replay checks every step against its hash.
//
// The server writes records (internal/server/recorder.go); cmd/estate-replay
// checks and analyses them. Records never contain session tokens, device ids,
// credentials or IP addresses: only what the engine's game state holds (names,
// player ids, the board) plus seat metadata (input, token, computer level).
package gamerecord

import (
	"encoding/json"
)

// Format identifies a record file; FormatVersion is bumped when the meaning of
// a field changes (readers refuse a newer major format).
const (
	Format        = "estate-game-record"
	FormatVersion = 1
)

// Step kinds.
const (
	// StepAction is an engine action (engine.PerformAction).
	StepAction = "action"
	// StepOrder is the end of the turn-order countdown
	// (engine.CompleteOpeningOrder), applied by the server's timer.
	StepOrder = "order"
	// StepEnd is the host ending the game early (engine.EndGame).
	StepEnd = "end"
	// StepResync replaces the whole game state with State. The server writes
	// one only when it could not keep the record continuous (a crash lost
	// steps between the last saved step and the saved game); the steps
	// before it still replay and are checked.
	StepResync = "resync"
)

// Step sources: who or what applied a step.
const (
	SourcePlayer  = "player"  // a person, from their phone, the classic client or a table seat
	SourceAI      = "ai"      // a computer seat
	SourceStandIn = "standin" // the computer playing for an away person (autopilot)
	SourceServer  = "server"  // the server itself (the turn-order countdown, close_auction when a timed auction's clock runs out)
	SourceHost    = "host"    // the host's End game, Remove (remove_player) and Skip turn (the skipped person's safe moves)
)

// End reasons (Outcome.Reason).
const (
	EndFinished  = "finished"  // one player left: everyone else went bankrupt
	EndEnded     = "ended"     // the host ended the game early (richest player wins)
	EndAbandoned = "abandoned" // the room was closed while the game was running
	EndExpired   = "expired"   // nobody came back: the room expired, or did not come back after a restart
	EndReset     = "reset"     // the room went back to the lobby (rematch) while the record was open
)

// Record is one game. Fields after Steps are derived when the record is
// finalised (Annotate) and are not needed to replay it.
type Record struct {
	Format        string `json:"format"`
	FormatVersion int    `json:"formatVersion"`
	Header

	// Initial is the full engine game JSON just before the start action (the
	// lobby: players in seat order, settings, host).
	Initial json.RawMessage `json:"initial"`
	// Steps are every change to the game from the start action on.
	Steps []Step `json:"steps"`
	// StandIns are the periods in which the computer played for an away
	// person, by step index.
	StandIns []StandIn `json:"standIns,omitempty"`

	// Outcome says how the game ended (nil while it is still being played).
	Outcome *Outcome `json:"outcome,omitempty"`
	// Final is the engine game JSON after the last step.
	Final json.RawMessage `json:"final,omitempty"`
	// Verified is true when Annotate replayed every step with no mismatch;
	// VerifyError is the first mismatch otherwise.
	Verified    bool   `json:"verified"`
	VerifyError string `json:"verifyError,omitempty"`
	// Turns is the number of turns played (the engine's turn counts).
	Turns int `json:"turns,omitempty"`
	// TurnOrder is the seat order after the turn-order roll (player ids).
	TurnOrder []string         `json:"turnOrder,omitempty"`
	Trades    []TradeSummary   `json:"trades,omitempty"`
	Auctions  []AuctionSummary `json:"auctions,omitempty"`
	Loans     []LoanSummary    `json:"loans,omitempty"`
	// Gaps lists the steps that are resyncs (lost steps before them).
	Gaps []int `json:"gaps,omitempty"`
}

// Header describes the game: who played it, where and under which rules.
type Header struct {
	// ID is the record's name: YYYY-MM-DD-HHMM-<code> (start time, server's
	// local time), also its file name without .json.gz.
	ID         string `json:"id"`
	Code       string `json:"code"`
	GameType   string `json:"gameType"` // "estate"
	AppVersion string `json:"appVersion"`
	// StartedAt and EndedAt are Unix milliseconds.
	StartedAt int64 `json:"startedAt"`
	EndedAt   int64 `json:"endedAt,omitempty"`
	// Room is how the game was hosted: "table" (a PC table screen),
	// "phone" (a phone-hosted party game) or "classic" (the classic client).
	Room          string `json:"room"`
	ComputerSpeed string `json:"computerSpeed,omitempty"`
	// DiceMode is "random" or "choice"; Settings is the engine's settings
	// object (dice mode and house rules) as it was at the start.
	DiceMode string          `json:"diceMode"`
	Settings json.RawMessage `json:"settings"`
	Players  []Player        `json:"players"`
	// StartsMidGame is set when Initial is not the lobby but a game already
	// in play: the server found a running game with no record (one saved
	// before records existed, or whose record was lost) and recorded the
	// rest of it.
	StartsMidGame bool `json:"startsMidGame,omitempty"`
}

// Player is one seat, in seat order (the lobby order; see Record.TurnOrder
// for the order of play).
type Player struct {
	ID    string `json:"id"`
	Seat  int    `json:"seat"`
	Name  string `json:"name"`
	Kind  string `json:"kind"` // "human" or "ai"
	Color string `json:"color,omitempty"`
	// Difficulty is the computer level (easy, normal, hard, expert).
	Difficulty string `json:"difficulty,omitempty"`
	// Input is how the seat was played: phone, keyboard, controller,
	// classic or ai.
	Input string `json:"input,omitempty"`
	// TokenKind is the player's token on the board (rocket, teapot, ...).
	TokenKind string `json:"tokenKind,omitempty"`
	Host      bool   `json:"host,omitempty"`
}

// Step is one change to the game.
type Step struct {
	N    int    `json:"n"`    // index in Steps
	Kind string `json:"kind"` // StepAction, StepOrder, StepEnd or StepResync
	// At is the engine clock during the step (Unix ms). The engine reads its
	// clock several times per action; while recording, every read in one
	// step returns At.
	At int64 `json:"t"`
	// By is the engine player id that acted (actions; the host for End).
	By string `json:"by,omitempty"`
	// Source and Via say who sent it: Source is one of the Source* values;
	// Via is the input of a person's seat (phone, keyboard, controller,
	// classic) or "table" for the table screen acting as host.
	Source string `json:"src,omitempty"`
	Via    string `json:"via,omitempty"`
	// Fallback is set when the engine refused a computer's chosen move and
	// the server played its safe fallback instead.
	Fallback bool `json:"fallback,omitempty"`
	// ThinkMs is how long a computer took to choose this move.
	ThinkMs int `json:"thinkMs,omitempty"`
	// Action is the action JSON as applied (unknown keys removed).
	Action json.RawMessage `json:"action,omitempty"`
	// Error is the engine's message when it refused the action. A refused
	// action is recorded only if it still changed the game.
	Error string `json:"error,omitempty"`
	// Now is the time passed to CompleteOpeningOrder (StepOrder).
	Now int64 `json:"now,omitempty"`
	// Version and VersionAfter are the game version before and after. The
	// server also bumps the version between steps (presence changes), so a
	// replay sets Version before applying each step.
	Version      int64 `json:"v"`
	VersionAfter int64 `json:"v2"`
	// RNG is every random number the step drew, in order (floats in [0,1),
	// like Math.random): two per die roll pair, thirty for the start's deck
	// shuffles. IDs are the ids it created, in order.
	RNG []float64 `json:"rng,omitempty"`
	IDs []string  `json:"ids,omitempty"`
	// Dice are the random dice the step rolled, as pairs (a roll, a turn
	// order roll, the nearest-utility re-roll). Dice a person chose in
	// "choose dice" mode are in Action.
	Dice [][2]int `json:"dice,omitempty"`
	// Cards are the cards the step drew, in order.
	Cards []CardDraw `json:"cards,omitempty"`
	// Decks is the deck order after the start's shuffle (start only).
	Decks *Decks `json:"decks,omitempty"`
	// Hash is StateHash of the game after the step.
	Hash string `json:"hash"`
	// State is the full game JSON (StepResync only).
	State json.RawMessage `json:"state,omitempty"`
}

// CardDraw is one card drawn from a deck.
type CardDraw struct {
	Deck string `json:"deck"` // "chance" or "chest"
	Card string `json:"card"` // card id, e.g. "chance-go"
}

// Decks is the order of both decks, top card first.
type Decks struct {
	Chance []string `json:"chance"`
	Chest  []string `json:"chest"`
}

// StandIn is a period in which the computer played for an away person:
// from step FromStep (the first step after it took over) to ToStep (the first
// step after the person was back; -1 if they never came back).
type StandIn struct {
	PlayerID string `json:"playerId"`
	FromStep int    `json:"fromStep"`
	ToStep   int    `json:"toStep"`
	From     int64  `json:"from"`
	To       int64  `json:"to,omitempty"`
}

// Outcome is how the game ended.
type Outcome struct {
	Reason   string `json:"reason"`
	WinnerID string `json:"winnerId,omitempty"`
	// BankruptcyOrder lists the players who went bankrupt, first out first.
	BankruptcyOrder []string `json:"bankruptcyOrder,omitempty"`
	// Standings ranks every player: the winner, then those still in by
	// final score, then the bankrupt ones, last out first.
	Standings []Standing `json:"standings,omitempty"`
	Note      string     `json:"note,omitempty"`
}

// Standing is one player's place at the end.
type Standing struct {
	PlayerID string `json:"playerId"`
	Place    int    `json:"place"`
	Bankrupt bool   `json:"bankrupt,omitempty"`
	// OutStep is the step at which they went bankrupt.
	OutStep  int `json:"outStep,omitempty"`
	Cash     int `json:"cash"`
	NetWorth int `json:"netWorth"`
	// Score is engine.FinalScore (net worth with a pending debt settled).
	Score int `json:"score"`
}

// TradeSummary is one trade offer and its answer.
type TradeSummary struct {
	Step       int         `json:"step"` // the offer
	Turn       int         `json:"turn"`
	ProposerID string      `json:"proposerId"`
	TargetID   string      `json:"targetId"`
	Offer      TradeBundle `json:"offer"`               // what the proposer gives
	Request    TradeBundle `json:"request"`             // what the proposer asks for
	Answer     string      `json:"answer"`              // accepted, rejected, countered (a counter-offer follows), or open (never answered)
	CounterOf  int         `json:"counterOf,omitempty"` // a counter-offer: the step of the offer it answers
	AnswerStep int         `json:"answerStep,omitempty"`
	AnsweredBy string      `json:"answeredBy,omitempty"` // source of the answer (player, ai, standin)
}

// TradeBundle is one side of a trade.
type TradeBundle struct {
	Cash       int   `json:"cash"`
	Properties []int `json:"properties"`
}

// AuctionSummary is one auction with every bid.
type AuctionSummary struct {
	StartStep  int          `json:"startStep"` // the decline that opened it
	EndStep    int          `json:"endStep"`
	Turn       int          `json:"turn"`
	SpaceIndex int          `json:"spaceIndex"`
	Space      string       `json:"space"`
	Price      int          `json:"price"` // list price
	Bids       []AuctionBid `json:"bids"`
	WinnerID   string       `json:"winnerId,omitempty"`
	Paid       int          `json:"paid,omitempty"`
}

// AuctionBid is one bid or pass.
type AuctionBid struct {
	Step     int    `json:"step"`
	PlayerID string `json:"playerId"`
	Amount   int    `json:"amount,omitempty"` // 0 = pass
	Pass     bool   `json:"pass,omitempty"`
}

// LoanSummary is one loan offer (house rule) and its answer.
type LoanSummary struct {
	Step       int             `json:"step"`
	ProposerID string          `json:"proposerId"`
	TargetID   string          `json:"targetId"`
	Action     json.RawMessage `json:"action"`
	Answer     string          `json:"answer"`
	AnswerStep int             `json:"answerStep,omitempty"`
}

// PlayerByID returns the header's player with this id, or nil.
func (h *Header) PlayerByID(id string) *Player {
	for i := range h.Players {
		if h.Players[i].ID == id {
			return &h.Players[i]
		}
	}
	return nil
}
