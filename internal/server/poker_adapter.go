package server

import (
	"encoding/json"
	"errors"

	"github.com/tdar200/estate-games/internal/poker/holdem"
)

// PokerEngine runs No-Limit Hold'em tables (internal/poker/holdem). The
// table was shaped after Game, so this adapter is thin. Configure it as
// Config.Engines["poker"].
type PokerEngine struct {
	// Clock returns Unix milliseconds; nil uses the server's clock (so tests
	// with a fake clock drive the poker timers too).
	Clock func() int64
	// Timing overrides the engine's pauses (run-out 1.8 s per street,
	// showdown 5 s, rebuy offer 30 s); nil keeps them.
	Timing *holdem.Timing
	// Seed fixes the shuffles of every table (tests and replays only); nil
	// seeds each table from crypto/rand.
	Seed *[32]byte
}

func (e PokerEngine) options() holdem.Options {
	return holdem.Options{Clock: e.Clock, Timing: e.Timing, Seed: e.Seed}
}

// NewGame implements Engine.
func (e PokerEngine) NewGame(code string) Game {
	return &pokerGame{t: holdem.NewTable(code, e.options())}
}

// LoadGame implements GameLoader: the snapshot holds the whole table,
// including the deck and the shuffle state, so a hand resumes exactly.
func (e PokerEngine) LoadGame(code string, snapshot []byte) (Game, error) {
	t, err := holdem.Restore(snapshot, e.options())
	if err != nil {
		return nil, err
	}
	if t.Code != code {
		return nil, errors.New("saved poker game has a different code")
	}
	return &pokerGame{t: t}, nil
}

// pokerGame adapts holdem.Table to Game. Snapshot (deck, hidden cards, RNG)
// is for persistence only; clients get PublicState and bots get View.
type pokerGame struct{ t *holdem.Table }

func (g *pokerGame) AddPlayer(name, kind string) (string, error) { return g.t.AddPlayer(name, kind) }
func (g *pokerGame) RemovePlayer(requesterID, playerID string) error {
	return g.t.RemovePlayer(requesterID, playerID)
}

// SwitchPlayerToSpectator is only reached in the lobby (setRole checks), where
// leaving frees the seat.
func (g *pokerGame) SwitchPlayerToSpectator(playerID, _ string) error {
	return g.t.RemovePlayer(playerID, playerID)
}

func (g *pokerGame) SwitchSpectatorToPlayer(name string, isHost bool) (string, error) {
	id, err := g.t.AddPlayer(name, holdem.KindHuman)
	if err == nil && isHost {
		g.t.SetHost(id)
	}
	return id, err
}

func (g *pokerGame) PerformAction(playerID string, action json.RawMessage) error {
	return g.t.PerformAction(playerID, action)
}

func (g *pokerGame) CompleteOpeningOrder(int64) bool { return false }

func (g *pokerGame) PublicState(viewerID string, v Viewer) ([]byte, error) {
	return g.t.PublicState(viewerID, toHoldemViewer(v))
}

func toHoldemViewer(v Viewer) holdem.Viewer {
	spectators := make([]holdem.Spectator, len(v.Spectators))
	for i, sp := range v.Spectators {
		spectators[i] = holdem.Spectator{ID: sp.ID, Name: sp.Name, IsHost: sp.IsHost}
	}
	return holdem.Viewer{Role: v.Role, Name: v.Name, SpectatorID: v.SpectatorID, IsHost: v.IsHost, Spectators: spectators}
}

func (g *pokerGame) Snapshot() ([]byte, error) { return g.t.Snapshot() }
func (g *pokerGame) SetHost(playerID string)   { g.t.SetHost(playerID) }
func (g *pokerGame) Touch()                    { g.t.Touch() }
func (g *pokerGame) Now() int64                { return g.t.Now() }

// EndGame implements GameEnder: a hand in progress is called off and its
// chips returned; a tournament ranks by chips, a cash game by chips won.
func (g *pokerGame) EndGame() (string, error) { return g.t.EndGame() }

// Info implements Informer. Players who left the table are not listed (the
// server dropped their seat when they were removed).
func (g *pokerGame) Info() GameInfo {
	t := g.t
	info := GameInfo{
		Status: t.Status, Phase: t.Phase, HostID: t.HostID, WinnerID: t.WinnerID, Version: t.Version,
		MaxSeats: t.Settings.MaxPlayers, Players: make([]PlayerInfo, 0, len(t.Players)),
	}
	if t.Status == holdem.StatusPlaying {
		info.Turn = t.HandNumber
	}
	seated := 0
	for _, p := range t.Players {
		if p.Left {
			continue
		}
		if !p.Out {
			seated++
		}
		info.Players = append(info.Players, PlayerInfo{ID: p.ID, Name: p.Name, Kind: p.Kind, Out: p.Out, Place: p.Place})
	}
	if t.Status == holdem.StatusLobby || (t.Status == holdem.StatusPlaying && t.Settings.LateJoin) {
		info.OpenSeats = max(0, t.Settings.MaxPlayers-seated)
	}
	info.Settings, _ = json.Marshal(t.Settings)
	return info
}
