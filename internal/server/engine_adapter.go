package server

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/hardai"
)

// RealEngine runs games on internal/engine (the Go port of src/game.js).
type RealEngine struct{}

// NewGame implements Engine.
func (RealEngine) NewGame(code string) Game {
	return &engineGame{g: engine.CreateGame(code)}
}

type engineGame struct {
	g *engine.Game
}

// turnOrderAuctioner is a lobby game that can go back to turn-order
// auctions without a log line (classic rooms: the classic client asks
// auction.currentPlayerId who may bid, so it can't take part in a timed
// open auction).
type turnOrderAuctioner interface {
	UseTurnOrderAuctions()
}

// UseTurnOrderAuctions implements turnOrderAuctioner.
func (e *engineGame) UseTurnOrderAuctions() {
	if e.g.Status == engine.StatusLobby {
		e.g.Settings.Rules.AuctionClock = 0
	}
}

func (e *engineGame) AddPlayer(name, kind string) (string, error) {
	p, err := engine.AddPlayer(e.g, name, kind)
	if err != nil {
		return "", err
	}
	return p.ID, nil
}

func (e *engineGame) RemovePlayer(requesterID, playerID string) error {
	return engine.RemovePlayer(e.g, requesterID, playerID)
}

func (e *engineGame) SwitchPlayerToSpectator(playerID, spectatorID string) error {
	return engine.SwitchPlayerToSpectator(e.g, playerID, spectatorID)
}

func (e *engineGame) SwitchSpectatorToPlayer(name string, isHost bool) (string, error) {
	p, err := engine.SwitchSpectatorToPlayer(e.g, name, isHost)
	if err != nil {
		return "", err
	}
	return p.ID, nil
}

func (e *engineGame) PerformAction(playerID string, raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	var action engine.Action
	if err := json.Unmarshal(raw, &action); err != nil {
		return errors.New("Invalid action format")
	}
	return engine.PerformAction(e.g, playerID, action, nil)
}

func (e *engineGame) CompleteOpeningOrder(nowMs int64) bool {
	return engine.CompleteOpeningOrder(e.g, nowMs)
}

func (e *engineGame) PublicState(viewerID string, v Viewer) ([]byte, error) {
	spectators := make([]engine.Spectator, len(v.Spectators))
	for i, sp := range v.Spectators {
		spectators[i] = engine.Spectator{ID: sp.ID, Name: sp.Name, IsHost: sp.IsHost}
	}
	state := engine.PublicState(e.g, viewerID, engine.Viewer{
		Role:        v.Role,
		Name:        v.Name,
		SpectatorID: v.SpectatorID,
		IsHost:      v.IsHost,
		Spectators:  spectators,
	})
	return json.Marshal(state)
}

func (e *engineGame) Snapshot() ([]byte, error) { return json.Marshal(e.g) }
func (e *engineGame) SetHost(playerID string)   { e.g.HostID = playerID }
func (e *engineGame) Touch()                    { e.g.Version++ }
func (e *engineGame) Now() int64                { return e.g.Now() }

// RealAI plays computer seats with internal/ai (the Go port of src/ai.js),
// decoding the engine's game JSON into ai.Game.
type RealAI struct{}

// ChooseAction implements AI.
func (RealAI) ChooseAction(_ Game, gameJSON []byte, playerID string) (json.RawMessage, error) {
	var g ai.Game
	if err := json.Unmarshal(gameJSON, &g); err != nil {
		return nil, err
	}
	action := ai.ChooseAction(&g, playerID)
	if action == nil {
		return loanFallback(gameJSON, playerID), nil
	}
	return json.Marshal(action)
}

// QuoteTrade implements AI.
func (RealAI) QuoteTrade(_ Game, gameJSON []byte, proposerID string, request json.RawMessage) (json.RawMessage, error) {
	var g ai.Game
	if err := json.Unmarshal(gameJSON, &g); err != nil {
		return nil, err
	}
	var req ai.QuoteRequest
	if len(request) > 0 && request[0] == '{' {
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, errInvalidJSON
		}
	}
	quote, err := ai.QuoteTrade(&g, proposerID, req)
	if err != nil {
		return nil, err
	}
	return marshalJS(quote)
}

// loanFallback declines a pending loan offer addressed to an AI that has no
// loan strategy, so house-rule games never stall waiting on a computer.
func loanFallback(gameJSON []byte, playerID string) json.RawMessage {
	var probe struct {
		Phase     string `json:"phase"`
		LoanOffer *struct {
			TargetID string `json:"targetId"`
		} `json:"loanOffer"`
	}
	if json.Unmarshal(gameJSON, &probe) != nil {
		return nil
	}
	if probe.Phase == "loan_offer" && probe.LoanOffer != nil && probe.LoanOffer.TargetID == playerID {
		return json.RawMessage(`{"type":"reject_loan"}`)
	}
	return nil
}

// EngineBot is a bot that reads the live *engine.Game directly; it is the
// same interface as hardai.Bot, so any hardai bot plugs in unchanged.
type EngineBot interface {
	Choose(g *engine.Game, playerID string) (engine.Action, bool)
}

// EngineBotAdapter turns an EngineBot into a Bot for this server. The server
// runs its decisions on a copy of the game, outside the server lock (see
// thinkAsync), because a Hard decision can take a quarter of a second.
func EngineBotAdapter(bot EngineBot) Bot { return &engineBot{bot: bot} }

// thinkingBot is a Bot the server lets think off the lock on a game copy.
type thinkingBot interface {
	Bot
	// decide chooses for playerID on g, a private copy of the game; gameJSON
	// is the same state as JSON. gen identifies the decision for discard.
	// gate (nil: none) may hold back offers to people (asks.go).
	decide(g *engine.Game, gameJSON []byte, playerID string, gate func(*engine.Game, engine.Action) bool) (action json.RawMessage, gen uint64, err error)
	// discard tells the bot decision gen was not played (the game moved on),
	// so it drops any multi-step plan made with it.
	discard(gen uint64)
}

type engineBot struct {
	mu  sync.Mutex // bots keep state between decisions; one decision at a time
	bot EngineBot
	gen uint64
}

func (b *engineBot) ChooseAction(game Game, gameJSON []byte, playerID string) (json.RawMessage, error) {
	eg, ok := game.(*engineGame)
	if !ok {
		return nil, errors.New("engine bots need the real engine")
	}
	action, _, err := b.decide(eg.g, gameJSON, playerID, nil)
	return action, err
}

func (b *engineBot) decide(g *engine.Game, gameJSON []byte, playerID string, gate func(*engine.Game, engine.Action) bool) (json.RawMessage, uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gen++
	if gb, ok := b.bot.(offerGated); ok {
		gb.SetOfferGate(gate)
		defer gb.SetOfferGate(nil)
	}
	action, ok := b.bot.Choose(g, playerID)
	if !ok {
		// Like hardai's driver: nothing to say means the heuristic decides.
		raw, err := RealAI{}.ChooseAction(nil, gameJSON, playerID)
		return raw, b.gen, err
	}
	raw, err := json.Marshal(action)
	return raw, b.gen, err
}

func (b *engineBot) discard(gen uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if gen != b.gen {
		return // a newer decision has been made since; its plan stands
	}
	if f, ok := b.bot.(interface{ Forget() }); ok {
		f.Forget()
	}
}

// gameCopy returns a private deep copy of a real engine game.
func gameCopy(game Game) (*engine.Game, bool) {
	eg, ok := game.(*engineGame)
	if !ok {
		return nil, false
	}
	return hardai.Clone(eg.g), true
}

// RealBots is the production BotFactory: "easy", "hard", "expert" and
// "grandmaster" are internal/hardai bots (Hard decides key moves by Monte Carlo
// rollouts; Expert searches the same way with a self-play-tuned heuristic and a
// learned value network, see docs/ai/EXPERT.md; Grandmaster thinks longer, up to
// 2.5 s, answers trades conservatively and names its own trade price, see
// docs/ai/GRANDMASTER.md); "normal" is internal/ai, the original heuristic.
func RealBots(level string, seed int64) Bot {
	lvl, err := hardai.ParseLevel(level)
	if err != nil || lvl == hardai.Normal {
		return RealAI{}
	}
	return EngineBotAdapter(hardai.NewBot(lvl, seed, hardai.Options{}))
}

// ownPricer is a hardai bot that names its own trade price (Hard, Expert,
// Grandmaster).
type ownPricer interface {
	QuotesOwnPrice() bool
	QuoteFor(g *engine.Game, me, proposer string, offer, request []int) (q hardai.Quote, valid bool)
}

// quotesOwnPrice reports whether this computer prices deals itself
// (quotes.go botQuote); the others are quoted by the Normal heuristic.
func (b *engineBot) quotesOwnPrice() bool {
	p, ok := b.bot.(ownPricer)
	return ok && p.QuotesOwnPrice()
}

// quoteDeal is the computer's own price for a deal proposer is drafting with
// it (the net cash it wants, or why no price works; see hardai.Quote), on g,
// a private copy of the game. One decision or quote at a time per bot.
func (b *engineBot) quoteDeal(g *engine.Game, me, proposer string, offer, request []int) hardai.Quote {
	p, ok := b.bot.(ownPricer)
	if !ok {
		return hardai.Quote{Reason: msgQuoteNoDeal}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	q, valid := p.QuoteFor(g, me, proposer, offer, request)
	if !valid {
		return hardai.Quote{Reason: msgQuoteMovedOn}
	}
	return q
}

// offerGated is a hardai bot that can hold back offers the server's rules
// refuse (asks.go).
type offerGated interface {
	SetOfferGate(func(*engine.Game, engine.Action) bool)
}
