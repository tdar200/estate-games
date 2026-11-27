package server

import (
	"encoding/json"
	"errors"

	"github.com/tdar200/estate-games/internal/closest"
)

// ClosestEngine runs Near Thing games (internal/closest, docs/CLOSEST-API.md).
// Like the quiz's, the engine was shaped after Game, so this adapter is
// thin. Configure it as Config.Engines["closest"].
type ClosestEngine struct {
	// Clock returns Unix milliseconds; nil uses the server's clock.
	Clock func() int64
	// Seed fixes the deal and the computers of every game (tests and
	// fixtures only); nil seeds each game from crypto/rand.
	Seed *[32]byte
	// Library is the question packs to deal from; nil uses the packs built
	// into the binary (tests use their own made-up ones).
	Library *closest.Library
}

func (e ClosestEngine) options() closest.Options {
	// The computers the fill setting adds take the names the room's own
	// computer seats would (Alpha, Orca, ...).
	return closest.Options{Clock: e.Clock, Seed: e.Seed, Library: e.Library, ComputerName: freeAIName}
}

// NewGame implements Engine.
func (e ClosestEngine) NewGame(code string) Game {
	return &closestGame{g: closest.NewGame(code, e.options())}
}

// LoadGame implements GameLoader: the snapshot holds the whole game,
// including the random stream and the room's played questions, so a
// restored game plays on exactly as it would have.
func (e ClosestEngine) LoadGame(code string, snapshot []byte) (Game, error) {
	g, err := closest.Restore(snapshot, e.options())
	if err != nil {
		return nil, err
	}
	if g.Code != code {
		return nil, errors.New("saved " + GameClosest + " game has a different code")
	}
	return &closestGame{g: g}, nil
}

// closestGame adapts closest.Game to Game. Snapshot (answers, guesses,
// bets) is for persistence only; clients get PublicState.
type closestGame struct{ g *closest.Game }

func (c *closestGame) AddPlayer(name, kind string) (string, error) { return c.g.AddPlayer(name, kind) }
func (c *closestGame) RemovePlayer(requesterID, playerID string) error {
	return c.g.RemovePlayer(requesterID, playerID)
}

// SwitchPlayerToSpectator is only reached in the lobby (setRole checks),
// where leaving frees the seat.
func (c *closestGame) SwitchPlayerToSpectator(playerID, _ string) error {
	return c.g.RemovePlayer(playerID, playerID)
}

func (c *closestGame) SwitchSpectatorToPlayer(name string, isHost bool) (string, error) {
	id, err := c.g.AddPlayer(name, closest.KindHuman)
	if err == nil && isHost {
		c.g.SetHost(id)
	}
	return id, err
}

func (c *closestGame) PerformAction(playerID string, action json.RawMessage) error {
	return c.g.PerformAction(playerID, action)
}

func (c *closestGame) CompleteOpeningOrder(int64) bool { return false }

func (c *closestGame) PublicState(viewerID string, v Viewer) ([]byte, error) {
	spectators := make([]closest.Spectator, len(v.Spectators))
	for i, sp := range v.Spectators {
		spectators[i] = closest.Spectator{ID: sp.ID, Name: sp.Name, IsHost: sp.IsHost}
	}
	return c.g.PublicState(viewerID, closest.Viewer{Role: v.Role, Name: v.Name, SpectatorID: v.SpectatorID, IsHost: v.IsHost, Spectators: spectators})
}

func (c *closestGame) Snapshot() ([]byte, error) { return c.g.Snapshot() }
func (c *closestGame) SetHost(playerID string)   { c.g.SetHost(playerID) }
func (c *closestGame) Touch()                    { c.g.Touch() }
func (c *closestGame) Now() int64                { return c.g.Now() }

// EndGame implements GameEnder: places by score as they stand.
func (c *closestGame) EndGame() (string, error) { return c.g.EndGame() }

// Info implements Informer. Players who left mid-game are listed as out; on
// a shared first place the first winner in seat order is the WinnerID.
func (c *closestGame) Info() GameInfo {
	g := c.g
	info := GameInfo{
		Status: g.Status, Phase: g.Phase, HostID: g.HostID, Version: g.Version,
		MaxSeats: g.SeatLimit(), Players: make([]PlayerInfo, 0, len(g.Players)),
	}
	if len(g.WinnerIDs) > 0 {
		info.WinnerID = g.WinnerIDs[0]
	}
	if g.Status == closest.StatusPlaying {
		info.Turn = g.QuestionSeq
	}
	for _, p := range g.Players {
		info.Players = append(info.Players, PlayerInfo{ID: p.ID, Name: p.Name, Kind: p.Kind, Out: p.Left, Place: p.Place})
	}
	if g.Status == closest.StatusLobby {
		info.OpenSeats = max(0, g.SeatLimit()-len(g.Players))
	}
	info.Settings, _ = json.Marshal(g.Settings)
	return info
}
