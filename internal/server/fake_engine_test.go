package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// fakeEngine is a tiny deterministic stand-in for internal/engine. It keeps
// the JSON shape of src/game.js for the fields the server reads and the JS
// error strings for the paths the tests exercise. Board: 0 is PAYDAY, odd
// squares are $100 streets (rent $10), even squares are plazas.
type fakeEngine struct {
	mu        sync.Mutex
	countdown time.Duration
	dice      [][2]int
	diceAt    int
	ids       int
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{countdown: 60 * time.Millisecond, dice: [][2]int{{3, 4}}}
}

func (e *fakeEngine) nextDice() [2]int {
	e.mu.Lock()
	defer e.mu.Unlock()
	d := e.dice[e.diceAt%len(e.dice)]
	e.diceAt++
	return d
}

func (e *fakeEngine) newID(prefix string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ids++
	return fmt.Sprintf("%s_%012x", prefix, e.ids)
}

func (e *fakeEngine) NewGame(code string) Game {
	g := &fakeGame{e: e, Code: code, Status: "lobby", Phase: "lobby", Players: []*fakePlayer{},
		Properties: map[string]*fakeProp{}, TurnCounts: map[string]int{},
		Settings: map[string]string{"diceMode": "random"}, Log: []fakeLog{}, Version: 1}
	for i := 1; i < 40; i += 2 {
		g.Properties[strconv.Itoa(i)] = &fakeProp{}
	}
	return g
}

var fakeColors = []string{"#d33f49", "#2d68c4", "#26966b", "#d58b20", "#8b55b7", "#168c9b", "#b5527a", "#6b5b3e"}

type fakePlayer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Color     string `json:"color"`
	Cash      int    `json:"cash"`
	Position  int    `json:"position"`
	InJail    bool   `json:"inJail"`
	JailTurns int    `json:"jailTurns"`
	JailCards int    `json:"jailCards"`
	Bankrupt  bool   `json:"bankrupt"`
}

type fakeProp struct {
	OwnerID   *string `json:"ownerId"`
	Mortgaged bool    `json:"mortgaged"`
	Houses    int     `json:"houses"`
}

type fakeMove struct {
	ID       string `json:"id"`
	PlayerID string `json:"playerId"`
	From     int    `json:"from"`
	To       int    `json:"to"`
	Path     []int  `json:"path"`
	At       int64  `json:"at"`
}

type fakeTurn struct {
	PlayerID  string      `json:"playerId"`
	Dice      []int       `json:"dice"`
	Movements []*fakeMove `json:"movements"`
}

type fakeOrder struct {
	Stage           string   `json:"stage"`
	PendingIDs      []string `json:"pendingIds"`
	CountdownEndsAt *int64   `json:"countdownEndsAt"`
}

type fakeLog struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	At   int64  `json:"at"`
}

type fakeGame struct {
	e               *fakeEngine
	Code            string               `json:"code"`
	Status          string               `json:"status"`
	Phase           string               `json:"phase"`
	HostID          *string              `json:"hostId"`
	Players         []*fakePlayer        `json:"players"`
	CurrentIndex    int                  `json:"currentIndex"`
	Properties      map[string]*fakeProp `json:"properties"`
	Turn            *fakeTurn            `json:"turn"`
	PendingPurchase *int                 `json:"pendingPurchase"`
	WinnerID        *string              `json:"winnerId"`
	LastMove        *fakeMove            `json:"lastMove"`
	TurnCounts      map[string]int       `json:"turnCounts"`
	OpeningOrder    *fakeOrder           `json:"openingOrder"`
	Settings        map[string]string    `json:"settings"`
	Log             []fakeLog            `json:"log"`
	Version         int                  `json:"version"`
}

func strp(s string) *string { return &s }

func (g *fakeGame) host() string {
	if g.HostID == nil {
		return ""
	}
	return *g.HostID
}

func (g *fakeGame) log(text string) {
	g.Log = append(g.Log, fakeLog{ID: g.e.newID("log"), Text: text, At: time.Now().UnixMilli()})
}

func (g *fakeGame) player(id string) *fakePlayer {
	for _, p := range g.Players {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (g *fakeGame) recolor() {
	for i, p := range g.Players {
		p.Color = fakeColors[i]
	}
}

func (g *fakeGame) AddPlayer(name, kind string) (string, error) {
	if g.Status != "lobby" {
		return "", errors.New("The game has already started")
	}
	if len(g.Players) >= 8 {
		return "", errors.New("Rooms support up to 8 players")
	}
	prefix := "player"
	if kind == "ai" {
		prefix = "ai"
	}
	p := &fakePlayer{ID: g.e.newID(prefix), Name: name, Kind: kind, Color: fakeColors[len(g.Players)], Cash: 1500}
	g.Players = append(g.Players, p)
	g.TurnCounts[p.ID] = 0
	if g.HostID == nil && kind == "human" {
		g.HostID = strp(p.ID)
	}
	g.log(name + " joined the room")
	g.Version++
	return p.ID, nil
}

func (g *fakeGame) RemovePlayer(requesterID, playerID string) error {
	if g.Status != "lobby" {
		return errors.New("Players cannot be removed after the game starts")
	}
	if requesterID != g.host() {
		return errors.New("Only the host can remove players")
	}
	for i, p := range g.Players {
		if p.ID != playerID {
			continue
		}
		g.Players = append(g.Players[:i], g.Players[i+1:]...)
		if p.ID == g.host() {
			g.HostID = nil
			for _, other := range g.Players {
				if other.Kind == "human" {
					g.HostID = strp(other.ID)
					break
				}
			}
		}
		g.recolor()
		g.log(p.Name + " left the room")
		g.Version++
		return nil
	}
	return errors.New("Player not found")
}

func (g *fakeGame) SwitchPlayerToSpectator(playerID, spectatorID string) error {
	if g.Status != "lobby" {
		return errors.New("Participation can only be changed before the game starts")
	}
	for i, p := range g.Players {
		if p.ID == playerID && p.Kind == "human" {
			g.Players = append(g.Players[:i], g.Players[i+1:]...)
			delete(g.TurnCounts, p.ID)
			if g.host() == p.ID {
				g.HostID = strp(spectatorID)
			}
			g.recolor()
			g.log(p.Name + " is now a spectator")
			g.Version++
			return nil
		}
	}
	return errors.New("Player not found")
}

func (g *fakeGame) SwitchSpectatorToPlayer(name string, isHost bool) (string, error) {
	if g.Status != "lobby" {
		return "", errors.New("Participation can only be changed before the game starts")
	}
	id, err := g.AddPlayer(name, "human")
	if err != nil {
		return "", err
	}
	if isHost {
		g.HostID = strp(id)
	}
	g.log(name + " is now a player")
	g.Version++
	return id, nil
}

func (g *fakeGame) current() *fakePlayer {
	if g.CurrentIndex < len(g.Players) {
		return g.Players[g.CurrentIndex]
	}
	return nil
}

func (g *fakeGame) assertCurrent(playerID string) (*fakePlayer, error) {
	p := g.current()
	if p == nil || p.ID != playerID {
		return nil, errors.New("It is not your turn yet")
	}
	return p, nil
}

var errPhase = errors.New("This action is not available during the current phase")

func (g *fakeGame) beginTurn() {
	p := g.current()
	g.Phase = "awaiting_roll"
	g.Turn = &fakeTurn{PlayerID: p.ID, Movements: []*fakeMove{}}
	g.PendingPurchase = nil
	g.log(p.Name + "'s turn")
}

func spaceName(i int) string {
	switch {
	case i == 0:
		return "PAYDAY"
	case i%2 == 1:
		return fmt.Sprintf("Street %d", i)
	}
	return fmt.Sprintf("Plaza %d", i)
}

func (g *fakeGame) PerformAction(playerID string, raw json.RawMessage) error {
	var action struct {
		Type *string `json:"type"`
		Mode string  `json:"mode"`
	}
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &action) != nil || action.Type == nil {
		return errors.New("Invalid action format")
	}
	kind := *action.Type
	if g.Status != "playing" && kind != "start" && kind != "set_dice_mode" {
		return errors.New("The game has not started")
	}
	switch kind {
	case "start":
		if playerID != g.host() {
			return errors.New("Only the host can start the game")
		}
		if g.Status != "lobby" {
			return errors.New("The game has already started")
		}
		if len(g.Players) < 2 {
			return errors.New("At least 2 players are required")
		}
		g.Status, g.Phase = "playing", "determining_order"
		order := &fakeOrder{Stage: "rolling"}
		for _, p := range g.Players {
			order.PendingIDs = append(order.PendingIDs, p.ID)
		}
		g.OpeningOrder = order
		g.log("Game started; every player has $1500")
	case "roll_for_order":
		if g.Phase != "determining_order" {
			return errPhase
		}
		index := -1
		for i, id := range g.OpeningOrder.PendingIDs {
			if id == playerID {
				index = i
			}
		}
		if index < 0 {
			return errors.New("You have already rolled for order this round")
		}
		d := g.e.nextDice()
		g.log(fmt.Sprintf("%s rolled %d + %d = %d for turn order", g.player(playerID).Name, d[0], d[1], d[0]+d[1]))
		g.OpeningOrder.PendingIDs = append(g.OpeningOrder.PendingIDs[:index], g.OpeningOrder.PendingIDs[index+1:]...)
		if len(g.OpeningOrder.PendingIDs) == 0 {
			ends := time.Now().Add(g.e.countdown).UnixMilli()
			g.OpeningOrder.Stage, g.OpeningOrder.CountdownEndsAt = "countdown", &ends
			g.Phase = "order_countdown"
		}
	case "roll":
		p, err := g.assertCurrent(playerID)
		if err != nil {
			return err
		}
		if g.Phase != "awaiting_roll" {
			return errPhase
		}
		d := g.e.nextDice()
		g.Turn.Dice = []int{d[0], d[1]}
		g.log(fmt.Sprintf("%s rolled %d + %d = %d", p.Name, d[0], d[1], d[0]+d[1]))
		from := p.Position
		path := []int{from}
		for step := 1; step <= d[0]+d[1]; step++ {
			path = append(path, (from+step)%40)
		}
		if from+d[0]+d[1] >= 40 {
			p.Cash += 200
			g.log(p.Name + " passed PAYDAY and collected $200")
		}
		p.Position = (from + d[0] + d[1]) % 40
		move := &fakeMove{ID: g.e.newID("move"), PlayerID: p.ID, From: from, To: p.Position, Path: path}
		g.LastMove = move
		g.Turn.Movements = append(g.Turn.Movements, move)
		g.log(p.Name + " landed on " + spaceName(p.Position))
		g.Phase = "turn_complete"
		if prop := g.Properties[strconv.Itoa(p.Position)]; prop != nil {
			switch {
			case prop.OwnerID == nil:
				index := p.Position
				g.PendingPurchase = &index
				g.Phase = "awaiting_purchase"
			case *prop.OwnerID != p.ID:
				p.Cash -= 10
				g.player(*prop.OwnerID).Cash += 10
				g.log(fmt.Sprintf("%s paid $10 (%s rent)", p.Name, spaceName(p.Position)))
			}
		}
	case "buy":
		p, err := g.assertCurrent(playerID)
		if err != nil {
			return err
		}
		if g.Phase != "awaiting_purchase" {
			return errPhase
		}
		p.Cash -= 100
		g.Properties[strconv.Itoa(*g.PendingPurchase)].OwnerID = strp(p.ID)
		g.log(fmt.Sprintf("%s bought %s for $100", p.Name, spaceName(*g.PendingPurchase)))
		g.PendingPurchase = nil
		g.Phase = "turn_complete"
	case "decline":
		if _, err := g.assertCurrent(playerID); err != nil {
			return err
		}
		if g.Phase != "awaiting_purchase" {
			return errPhase
		}
		g.PendingPurchase = nil
		g.Phase = "turn_complete"
	case "end_turn":
		if _, err := g.assertCurrent(playerID); err != nil {
			return err
		}
		if g.Phase != "turn_complete" {
			return errPhase
		}
		g.TurnCounts[playerID]++
		g.advance()
	case "set_dice_mode":
		if g.Status != "lobby" {
			return errors.New("Dice mode can only be changed before the game starts")
		}
		if playerID != g.host() {
			return errors.New("Only the host can change dice mode")
		}
		if action.Mode != "random" && action.Mode != "choice" {
			return errors.New("Invalid dice mode")
		}
		g.Settings["diceMode"] = action.Mode
	case "bankrupt":
		p, err := g.assertCurrent(playerID)
		if err != nil {
			return err
		}
		p.Bankrupt = true
		g.log(p.Name + " declared bankruptcy; assets returned to the bank")
		var remaining []*fakePlayer
		for _, other := range g.Players {
			if !other.Bankrupt {
				remaining = append(remaining, other)
			}
		}
		if len(remaining) == 1 {
			g.Status, g.Phase = "finished", "finished"
			g.WinnerID = strp(remaining[0].ID)
			g.log(remaining[0].Name + " wins the game")
		} else {
			g.advance()
		}
	default:
		return errors.New("Unknown action")
	}
	g.Version++
	return nil
}

func (g *fakeGame) advance() {
	for offset := 1; offset <= len(g.Players); offset++ {
		next := (g.CurrentIndex + offset) % len(g.Players)
		if !g.Players[next].Bankrupt {
			g.CurrentIndex = next
			g.beginTurn()
			return
		}
	}
}

func (g *fakeGame) CompleteOpeningOrder(nowMs int64) bool {
	if g.Phase != "order_countdown" || g.OpeningOrder == nil || nowMs < *g.OpeningOrder.CountdownEndsAt {
		return false
	}
	g.OpeningOrder.Stage = "complete"
	g.beginTurn()
	g.Version++
	return true
}

func (g *fakeGame) PublicState(viewerID string, v Viewer) ([]byte, error) {
	role := v.Role
	if role == "" {
		role = "player"
	}
	spectators := v.Spectators
	if spectators == nil {
		spectators = []Spectator{}
	}
	var current *string
	if g.Phase != "determining_order" && g.Phase != "order_countdown" {
		if p := g.current(); p != nil {
			current = strp(p.ID)
		}
	}
	board := make([]map[string]any, 40)
	for i := range board {
		space := map[string]any{"index": i, "type": "plaza", "name": spaceName(i)}
		if i == 0 {
			space["type"] = "go"
		} else if i%2 == 1 {
			space["type"], space["price"] = "property", 100
		}
		board[i] = space
	}
	nullable := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	// Field order follows publicState in src/game.js.
	return json.Marshal(struct {
		Code            string               `json:"code"`
		Status          string               `json:"status"`
		Phase           string               `json:"phase"`
		HostID          *string              `json:"hostId"`
		Players         []*fakePlayer        `json:"players"`
		CurrentIndex    int                  `json:"currentIndex"`
		CurrentPlayerID *string              `json:"currentPlayerId"`
		Properties      map[string]*fakeProp `json:"properties"`
		Turn            *fakeTurn            `json:"turn"`
		PendingPurchase *int                 `json:"pendingPurchase"`
		WinnerID        *string              `json:"winnerId"`
		LastMove        *fakeMove            `json:"lastMove"`
		OpeningOrder    *fakeOrder           `json:"openingOrder"`
		Settings        map[string]string    `json:"settings"`
		Log             []fakeLog            `json:"log"`
		Version         int                  `json:"version"`
		Board           []map[string]any     `json:"board"`
		ViewerID        *string              `json:"viewerId"`
		ViewerRole      string               `json:"viewerRole"`
		ViewerName      *string              `json:"viewerName"`
		SpectatorID     *string              `json:"spectatorId"`
		ViewerIsHost    bool                 `json:"viewerIsHost"`
		Spectators      []Spectator          `json:"spectators"`
	}{g.Code, g.Status, g.Phase, g.HostID, g.Players, g.CurrentIndex, current, g.Properties, g.Turn,
		g.PendingPurchase, g.WinnerID, g.LastMove, g.OpeningOrder, g.Settings, g.Log, g.Version, board,
		nullable(viewerID), role, nullable(v.Name), nullable(v.SpectatorID), v.IsHost, spectators})
}

func (g *fakeGame) Snapshot() ([]byte, error) { return json.Marshal(g) }
func (g *fakeGame) SetHost(playerID string)   { g.HostID = strp(playerID) }
func (g *fakeGame) Touch()                    { g.Version++ }
func (g *fakeGame) Now() int64                { return time.Now().UnixMilli() }

// fakeQuoteAI wraps SimpleAI with a canned trade quote.
type fakeQuoteAI struct{ SimpleAI }

func (a *fakeQuoteAI) QuoteTrade(_ Game, _ []byte, proposerID string, request json.RawMessage) (json.RawMessage, error) {
	var req struct {
		TargetID string `json:"targetId"`
	}
	_ = json.Unmarshal(request, &req)
	if !strings.HasPrefix(req.TargetID, "ai_") {
		return nil, errors.New("Quotes are only available from an active AI player")
	}
	return json.RawMessage(`{"offerCash":0,"requestCash":40,"targetId":"` + req.TargetID + `"}`), nil
}
