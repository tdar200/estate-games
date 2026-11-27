package server

import (
	"encoding/json"
)

// Engine creates games. The production implementation wraps internal/engine
// (see engine_adapter.go); tests use an in-memory fake.
type Engine interface {
	NewGame(code string) Game
}

// Game is one authoritative game, mirroring the functions of src/game.js that
// src/rooms.js calls. Errors carry the exact English messages of the JS
// engine. All methods are called with the server lock held.
type Game interface {
	// AddPlayer adds a seat; kind is "human" or "ai". Returns the player id.
	AddPlayer(name, kind string) (string, error)
	RemovePlayer(requesterID, playerID string) error
	SwitchPlayerToSpectator(playerID, spectatorID string) error
	SwitchSpectatorToPlayer(name string, isHost bool) (string, error)
	// PerformAction applies a raw JSON action ({"type":"roll"}, ...). A nil or
	// empty action behaves like JS undefined.
	PerformAction(playerID string, action json.RawMessage) error
	// CompleteOpeningOrder ends the order countdown once nowMs has passed.
	CompleteOpeningOrder(nowMs int64) bool
	// PublicState returns the publicState JSON for one viewer.
	PublicState(viewerID string, viewer Viewer) ([]byte, error)
	// Snapshot returns the full game JSON (same shape as the JS game object).
	Snapshot() ([]byte, error)
	// SetHost makes playerID the game's host (party mode: the first phone
	// player of a table room).
	SetHost(playerID string)
	// Touch bumps the game version so clients that de-duplicate states by
	// version notice server-side changes (presence, seats, spectators).
	Touch()
	// Now returns the game clock in Unix milliseconds.
	Now() int64
}

// AI chooses moves for computer players and prices trades (src/ai.js).
type AI interface {
	// ChooseAction returns the next action JSON for an AI player, or nil.
	ChooseAction(game Game, gameJSON []byte, playerID string) (json.RawMessage, error)
	// QuoteTrade answers POST …/trade-quote. request is the whole request
	// body ({token, targetId, offerProperties, requestProperties}).
	QuoteTrade(game Game, gameJSON []byte, proposerID string, request json.RawMessage) (json.RawMessage, error)
}

// Bot decides for one AI seat. AI implements Bot, so by default every seat
// uses Config.AI; Config.Bots can supply per-difficulty bots (internal/hardai).
type Bot interface {
	ChooseAction(game Game, gameJSON []byte, playerID string) (json.RawMessage, error)
}

// BotFactory creates the bot for a new AI seat. difficulty is "easy",
// "normal" or "hard"; seed lets stateful bots be reproducible.
type BotFactory func(difficulty string, seed int64) Bot

// Viewer is the third argument of publicState.
type Viewer struct {
	Role        string
	Name        string
	SpectatorID string
	IsHost      bool
	Spectators  []Spectator
}

// Spectator is one entry of publicState.spectators.
type Spectator struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IsHost bool   `json:"isHost"`
}

// gameView is the subset of the engine's game JSON the server reads (AI
// scheduling, events, summaries, friends). Null ids decode as "".
type gameView struct {
	Code            string                  `json:"code"`
	Status          string                  `json:"status"`
	Phase           string                  `json:"phase"`
	HostID          string                  `json:"hostId"`
	Players         []playerView            `json:"players"`
	CurrentIndex    int                     `json:"currentIndex"`
	Properties      map[string]propertyView `json:"properties"`
	Turn            *turnView               `json:"turn"`
	PendingPurchase *int                    `json:"pendingPurchase"`
	Auction         *auctionView            `json:"auction"`
	Debt            *debtView               `json:"debt"`
	WinnerID        string                  `json:"winnerId"`
	LastCard        *struct {
		Deck string `json:"deck"`
		Text string `json:"text"`
	} `json:"lastCard"`
	PendingCard *struct {
		PlayerID string `json:"playerId"`
	} `json:"pendingCard"`
	LastBankruptcy *struct {
		PlayerID   string `json:"playerId"`
		CreditorID string `json:"creditorId"`
	} `json:"lastBankruptcy"`
	LastMove     *movementView   `json:"lastMove"`
	Trade        *tradeView      `json:"trade"`
	LoanOffer    *tradeView      `json:"loanOffer"`
	OpeningOrder *openingView    `json:"openingOrder"`
	TurnCounts   map[string]int  `json:"turnCounts"`
	Settings     json.RawMessage `json:"settings"`
	Log          []logEntryView  `json:"log"`
	Version      int64           `json:"version"`
}

type playerView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Color     string `json:"color"`
	Cash      int    `json:"cash"`
	Position  int    `json:"position"`
	InJail    bool   `json:"inJail"`
	JailCards int    `json:"jailCards"`
	Bankrupt  bool   `json:"bankrupt"`
}

type propertyView struct {
	OwnerID   string `json:"ownerId"`
	Mortgaged bool   `json:"mortgaged"`
	Houses    int    `json:"houses"`
}

type turnView struct {
	PlayerID  string         `json:"playerId"`
	Dice      []int          `json:"dice"`
	Movements []movementView `json:"movements"`
}

type movementView struct {
	ID       string `json:"id"`
	PlayerID string `json:"playerId"`
	From     int    `json:"from"`
	To       int    `json:"to"`
	Path     []int  `json:"path"`
}

type auctionView struct {
	SpaceIndex int      `json:"spaceIndex"`
	ActiveIDs  []string `json:"activeIds"`
	TurnPos    int      `json:"turnPos"`
	CurrentBid int      `json:"currentBid"`
	BidderID   string   `json:"bidderId"`
	ClockMs    int      `json:"clockMs"` // timed open auction; 0 = turn order
	EndsAt     int64    `json:"endsAt"`  // when it closes, ms on the game clock
}

type debtView struct {
	PayerID    string `json:"payerId"`
	Amount     int    `json:"amount"`
	CreditorID string `json:"creditorId"`
	Reason     string `json:"reason"`
}

type tradeView struct {
	ID         string          `json:"id"`
	CounterTo  string          `json:"counterTo,omitempty"`
	ProposerID string          `json:"proposerId"`
	TargetID   string          `json:"targetId"`
	Offer      tradeBundleView `json:"offer"`
	Request    tradeBundleView `json:"request"`
}

type tradeBundleView struct {
	Cash       int   `json:"cash"`
	Properties []int `json:"properties"`
}

type openingView struct {
	Stage           string   `json:"stage"`
	PendingIDs      []string `json:"pendingIds"`
	CountdownEndsAt *int64   `json:"countdownEndsAt"`
}

type logEntryView struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// boardSpace is one entry of publicState.board.
type boardSpace struct {
	Index    int    `json:"index"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Group    string `json:"group,omitempty"`
	Price    int    `json:"price,omitempty"`
	BuildCst int    `json:"buildCost,omitempty"`
}

func decodeGameView(data []byte) (*gameView, error) {
	var view gameView
	if err := json.Unmarshal(data, &view); err != nil {
		return nil, err
	}
	return &view, nil
}

func (g *gameView) player(id string) *playerView {
	if id == "" {
		return nil
	}
	for i := range g.Players {
		if g.Players[i].ID == id {
			return &g.Players[i]
		}
	}
	return nil
}

// turnNumber is 1 + total completed turns (state.turnNumber).
func (g *gameView) turnNumber() int {
	total := 1
	for _, count := range g.TurnCounts {
		total += count
	}
	return total
}

// playingTurn is turnNumber while a game is running, else 0.
func (g *gameView) playingTurn() int {
	if g.Status != "playing" {
		return 0
	}
	return g.turnNumber()
}
