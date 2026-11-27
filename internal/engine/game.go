package engine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// PlayerColors are assigned to seats in order (PLAYER_COLORS).
var PlayerColors = []string{"#d33f49", "#2d68c4", "#26966b", "#d58b20", "#8b55b7", "#168c9b", "#8a5a32", "#5c6b7a"}

// maxLogEntries is the log length cap.
const maxLogEntries = 120

// DefaultClock returns the current time in Unix milliseconds. Games created
// by CreateGame use it unless Game.Clock is replaced.
var DefaultClock = func() int64 { return time.Now().UnixMilli() }

// DefaultNewID generates ids like JS uid(prefix): prefix + "_" + 12 hex chars
// from crypto/rand. Games created by CreateGame use it unless Game.NewID is
// replaced.
var DefaultNewID = func(prefix string) string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(buf[:])
}

// SequentialIDs returns an id generator that yields prefix + "_" + a 12-digit
// hex counter (1, 2, 3...). It is deterministic and handy for tests; the trace
// harness patches JS crypto.randomBytes to produce the same sequence.
func SequentialIDs() func(prefix string) string {
	var counter uint64
	return func(prefix string) string {
		counter++
		const digits = "0123456789abcdef"
		var buf [12]byte
		value := counter
		for i := len(buf) - 1; i >= 0; i-- {
			buf[i] = digits[value&0xf]
			value >>= 4
		}
		return prefix + "_" + string(buf[:])
	}
}

func (g *Game) now() int64 {
	if g.Clock != nil {
		return g.Clock()
	}
	return DefaultClock()
}

func (g *Game) uid(prefix string) string {
	if g.NewID != nil {
		return g.NewID(prefix)
	}
	return DefaultNewID(prefix)
}

// Now returns the game's current time in Unix milliseconds using its Clock.
func (g *Game) Now() int64 { return g.now() }

// CreateGame returns a new lobby game (createGame). Unlike game.js it starts
// with the timed open auction (house rule auctionClock, rules.go).
func CreateGame(code string) *Game {
	properties := make(map[int]*PropertyState, len(OwnableIndexes))
	for _, index := range OwnableIndexes {
		properties[index] = &PropertyState{}
	}
	return &Game{
		Code:                 code,
		Status:               StatusLobby,
		Phase:                PhaseLobby,
		Players:              []*Player{},
		Properties:           properties,
		AITradeCooldowns:     map[string]int{},
		TurnCounts:           map[string]int{},
		TurnSummaryBaselines: map[string]map[string]PlayerSpot{},
		Settings:             Settings{DiceMode: DiceModeRandom, Rules: HouseRules{AuctionClock: DefaultAuctionClock}},
		Decks:                Decks{Chance: []string{}, Chest: []string{}},
		Log:                  []LogEntry{},
		Version:              1,
		Clock:                DefaultClock,
		NewID:                DefaultNewID,
	}
}

// AddPlayer seats a new player in the lobby (addPlayer). kind is "human" or
// "ai"; "" means "human".
func AddPlayer(g *Game, name, kind string) (*Player, error) {
	if kind == "" {
		kind = KindHuman
	}
	if g.Status != StatusLobby {
		return nil, errors.New("The game has already started")
	}
	if len(g.Players) >= g.maxPlayers() {
		return nil, errors.New("Rooms support up to " + itoa(g.maxPlayers()) + " players")
	}
	prefix := "player"
	if kind == KindAI {
		prefix = "ai"
	}
	player := &Player{
		ID:    g.uid(prefix),
		Name:  name,
		Kind:  kind,
		Color: PlayerColors[len(g.Players)],
		Cash:  g.Settings.Rules.StartingCashAmount(), // 1500 unless a house rule changes it
	}
	g.Players = append(g.Players, player)
	g.TurnCounts[player.ID] = 0
	if g.HostID == "" && kind == KindHuman {
		g.HostID = player.ID
	}
	addLog(g, player.Name+" joined the room")
	touch(g)
	return player, nil
}

// RemovePlayer removes a seat in the lobby; only the host may do it
// (removePlayer).
func RemovePlayer(g *Game, requesterID, playerID string) error {
	if g.Status != StatusLobby {
		return errors.New("Players cannot be removed after the game starts")
	}
	if requesterID != g.HostID {
		return errors.New("Only the host can remove players")
	}
	index := g.playerIndex(playerID)
	if index < 0 {
		return errors.New("Player not found")
	}
	removed := g.Players[index]
	g.Players = append(g.Players[:index:index], g.Players[index+1:]...)
	if removed.ID == g.HostID {
		g.HostID = ""
		for _, player := range g.Players {
			if player.Kind == KindHuman {
				g.HostID = player.ID
				break
			}
		}
	}
	recolor(g)
	addLog(g, removed.Name+" left the room")
	touch(g)
	return nil
}

// SwitchPlayerToSpectator turns a human lobby player into a spectator; if they
// were the host, hostId becomes spectatorID (switchPlayerToSpectator).
func SwitchPlayerToSpectator(g *Game, playerID, spectatorID string) error {
	if g.Status != StatusLobby {
		return errors.New("Participation can only be changed before the game starts")
	}
	index := -1
	for i, player := range g.Players {
		if player.ID == playerID && player.Kind == KindHuman {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("Player not found")
	}
	player := g.Players[index]
	g.Players = append(g.Players[:index:index], g.Players[index+1:]...)
	delete(g.TurnCounts, player.ID)
	if g.HostID == player.ID {
		g.HostID = spectatorID
	}
	recolor(g)
	addLog(g, player.Name+" is now a spectator")
	touch(g)
	return nil
}

// SwitchSpectatorToPlayer seats a lobby spectator as a human player
// (switchSpectatorToPlayer).
func SwitchSpectatorToPlayer(g *Game, name string, isHost bool) (*Player, error) {
	if g.Status != StatusLobby {
		return nil, errors.New("Participation can only be changed before the game starts")
	}
	player, err := AddPlayer(g, name, KindHuman)
	if err != nil {
		return nil, err
	}
	if isHost {
		g.HostID = player.ID
	}
	addLog(g, player.Name+" is now a player")
	touch(g)
	return player, nil
}

func recolor(g *Game) {
	for index, player := range g.Players {
		player.Color = PlayerColors[index]
	}
}

// StartGame shuffles the decks and enters the turn-order roll (startGame).
// It consumes 30 floats from rng (15 per deck). A nil rng uses DefaultRNG.
func StartGame(g *Game, requesterID string, rng RNG) error {
	rng = orDefault(rng)
	if requesterID != g.HostID {
		return errors.New("Only the host can start the game")
	}
	if g.Status != StatusLobby {
		return errors.New("The game has already started")
	}
	if len(g.Players) < 2 {
		return errors.New("At least 2 players are required")
	}
	g.Status = StatusPlaying
	g.Decks.Chance = shuffle(cardIDs(ChanceCards), rng)
	g.Decks.Chest = shuffle(cardIDs(ChestCards), rng)
	g.CurrentIndex = 0
	g.Phase = PhaseDeterminingOrder
	order := &OpeningOrder{
		Stage:      OpeningStageRolling,
		Round:      1,
		PendingIDs: []string{},
		Order:      []string{},
		Entries:    []*OpeningEntry{},
		RollingIDs: []string{},
		Ties:       []OpeningTie{},
	}
	for _, player := range g.Players {
		order.PendingIDs = append(order.PendingIDs, player.ID)
		order.RollingIDs = append(order.RollingIDs, player.ID)
		order.Entries = append(order.Entries, &OpeningEntry{PlayerID: player.ID, Rolls: []OpeningRoll{}})
	}
	g.OpeningOrder = order
	addLog(g, "Game started; every player has "+money(g.Settings.Rules.StartingCashAmount()))
	addLog(g, "All players roll to determine turn order")
	touch(g)
	return nil
}

func setDiceMode(g *Game, requesterID string, mode string, modeOK bool) error {
	if g.Status != StatusLobby {
		return errors.New("Dice mode can only be changed before the game starts")
	}
	if requesterID != g.HostID {
		return errors.New("Only the host can change dice mode")
	}
	if !modeOK || (mode != DiceModeRandom && mode != DiceModeChoice) {
		return errors.New("Invalid dice mode")
	}
	g.Settings.DiceMode = mode
	label := "random dice"
	if mode == DiceModeChoice {
		label = "choose dice"
	}
	addLog(g, "Dice mode set to "+label)
	return nil
}

// CurrentPlayer returns players[currentIndex], or nil (currentPlayer).
func CurrentPlayer(g *Game) *Player {
	if g.CurrentIndex < 0 || g.CurrentIndex >= len(g.Players) {
		return nil
	}
	return g.Players[g.CurrentIndex]
}

// PlayerByID returns the player with the given id, or nil.
func (g *Game) PlayerByID(id string) *Player {
	for _, player := range g.Players {
		if player.ID == id {
			return player
		}
	}
	return nil
}

func (g *Game) playerIndex(id string) int {
	for index, player := range g.Players {
		if player.ID == id {
			return index
		}
	}
	return -1
}

func activePlayers(g *Game) []*Player {
	var out []*Player
	for _, player := range g.Players {
		if !player.Bankrupt {
			out = append(out, player)
		}
	}
	return out
}

func assertCurrent(g *Game, playerID string) (*Player, error) {
	player := CurrentPlayer(g)
	if player == nil || player.ID != playerID {
		return nil, errors.New("It is not your turn yet")
	}
	if player.Bankrupt {
		return nil, errors.New("This player is bankrupt")
	}
	return player, nil
}

var errPhase = errors.New("This action is not available during the current phase")

func assertPhase(g *Game, phases ...string) error {
	for _, phase := range phases {
		if g.Phase == phase {
			return nil
		}
	}
	return errPhase
}

func addLog(g *Game, text string) {
	g.Log = append(g.Log, LogEntry{ID: g.uid("log"), Text: text, At: g.now()})
	if len(g.Log) > maxLogEntries {
		g.Log = append([]LogEntry{}, g.Log[len(g.Log)-maxLogEntries:]...)
	}
}

func touch(g *Game) {
	g.Version++
}

// money formats a dollar amount like the JS template `$${amount}`.
func money(amount int) string {
	return "$" + strconv.Itoa(amount)
}

func itoa(value int) string { return strconv.Itoa(value) }
