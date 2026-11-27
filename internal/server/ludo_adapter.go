package server

import (
	"encoding/json"
	"errors"

	"github.com/tdar200/estate-games/internal/ludo"
)

// LudoEngine runs Ludo games (internal/ludo). Like the poker table, the
// game was shaped after Game, so this adapter is thin. Configure it as
// Config.Engines["ludo"].
type LudoEngine struct {
	// Clock returns Unix milliseconds; nil uses the server's clock.
	Clock func() int64
	// Seed fixes the dice of every game (tests and fixtures only); nil seeds
	// each game from crypto/rand.
	Seed *[32]byte
}

func (e LudoEngine) options() ludo.Options { return ludo.Options{Clock: e.Clock, Seed: e.Seed} }

// NewGame implements Engine.
func (e LudoEngine) NewGame(code string) Game {
	return &ludoGame{g: ludo.NewGame(code, e.options())}
}

// LoadGame implements GameLoader: the snapshot holds the whole game,
// including the dice stream, so a restored game rolls what it would have.
func (e LudoEngine) LoadGame(code string, snapshot []byte) (Game, error) {
	g, err := ludo.Restore(snapshot, e.options())
	if err != nil {
		return nil, err
	}
	if g.Code != code {
		return nil, errors.New("saved ludo game has a different code")
	}
	return &ludoGame{g: g}, nil
}

// ludoGame adapts ludo.Game to Game. Snapshot (with the dice stream) is for
// persistence only; clients get PublicState and bots get View.
type ludoGame struct{ g *ludo.Game }

func (l *ludoGame) AddPlayer(name, kind string) (string, error) { return l.g.AddPlayer(name, kind) }
func (l *ludoGame) RemovePlayer(requesterID, playerID string) error {
	return l.g.RemovePlayer(requesterID, playerID)
}

// SwitchPlayerToSpectator is only reached in the lobby (setRole checks),
// where leaving frees the seat.
func (l *ludoGame) SwitchPlayerToSpectator(playerID, _ string) error {
	return l.g.RemovePlayer(playerID, playerID)
}

func (l *ludoGame) SwitchSpectatorToPlayer(name string, isHost bool) (string, error) {
	id, err := l.g.AddPlayer(name, ludo.KindHuman)
	if err == nil && isHost {
		l.g.SetHost(id)
	}
	return id, err
}

func (l *ludoGame) PerformAction(playerID string, action json.RawMessage) error {
	return l.g.PerformAction(playerID, action)
}

func (l *ludoGame) CompleteOpeningOrder(int64) bool { return false }

func (l *ludoGame) PublicState(viewerID string, v Viewer) ([]byte, error) {
	return l.g.PublicState(viewerID, toLudoViewer(v))
}

func toLudoViewer(v Viewer) ludo.Viewer {
	spectators := make([]ludo.Spectator, len(v.Spectators))
	for i, sp := range v.Spectators {
		spectators[i] = ludo.Spectator{ID: sp.ID, Name: sp.Name, IsHost: sp.IsHost}
	}
	return ludo.Viewer{Role: v.Role, Name: v.Name, SpectatorID: v.SpectatorID, IsHost: v.IsHost, Spectators: spectators}
}

func (l *ludoGame) Snapshot() ([]byte, error) { return l.g.Snapshot() }
func (l *ludoGame) SetHost(playerID string)   { l.g.SetHost(playerID) }
func (l *ludoGame) Touch()                    { l.g.Touch() }
func (l *ludoGame) Now() int64                { return l.g.Now() }

// EndGame implements GameEnder: players home keep their places, the rest are
// ranked by how far their pieces got.
func (l *ludoGame) EndGame() (string, error) { return l.g.EndGame() }

// Info implements Informer. Players who left mid-game are listed as out.
func (l *ludoGame) Info() GameInfo {
	g := l.g
	info := GameInfo{
		Status: g.Status, Phase: g.Phase, HostID: g.HostID, WinnerID: g.WinnerID, Version: g.Version,
		MaxSeats: g.SeatLimit(), Players: make([]PlayerInfo, 0, len(g.Players)),
	}
	if g.Status == ludo.StatusPlaying {
		info.Turn = g.TurnNumber
	}
	seated := 0
	for _, p := range g.Players {
		if !p.Left {
			seated++
		}
		info.Players = append(info.Players, PlayerInfo{ID: p.ID, Name: p.Name, Kind: p.Kind, Out: p.Left, Place: p.Place})
	}
	if g.Status == ludo.StatusLobby {
		info.OpenSeats = max(0, g.SeatLimit()-seated)
	}
	info.Settings, _ = json.Marshal(g.Settings)
	return info
}
