package server

import (
	"encoding/json"
	"errors"

	"github.com/tdar200/estate-games/internal/quiz"
)

// QuizEngine runs quiz games (internal/quiz). Like Ludo's, the engine was
// shaped after Game, so this adapter is thin. Configure it as
// Config.Engines["quiz"].
type QuizEngine struct {
	// Clock returns Unix milliseconds; nil uses the server's clock.
	Clock func() int64
	// Seed fixes the deal, the choice order and the computers of every game
	// (tests and fixtures only); nil seeds each game from crypto/rand.
	Seed *[32]byte
	// Library is the question packs to deal from; nil uses the packs built
	// into the binary (tests use their own).
	Library *quiz.Library
}

func (e QuizEngine) options() quiz.Options {
	return quiz.Options{Clock: e.Clock, Seed: e.Seed, Library: e.Library}
}

// NewGame implements Engine.
func (e QuizEngine) NewGame(code string) Game {
	return &quizGame{g: quiz.NewGame(code, e.options())}
}

// LoadGame implements GameLoader: the snapshot holds the whole game,
// including the random stream and the room's played categories, so a
// restored game plays on exactly as it would have.
func (e QuizEngine) LoadGame(code string, snapshot []byte) (Game, error) {
	g, err := quiz.Restore(snapshot, e.options())
	if err != nil {
		return nil, err
	}
	if g.Code != code {
		return nil, errors.New("saved quiz game has a different code")
	}
	return &quizGame{g: g}, nil
}

// quizGame adapts quiz.Game to Game. Snapshot (answers, gold squares, bets)
// is for persistence only; clients get PublicState.
type quizGame struct{ g *quiz.Game }

func (q *quizGame) AddPlayer(name, kind string) (string, error) { return q.g.AddPlayer(name, kind) }
func (q *quizGame) RemovePlayer(requesterID, playerID string) error {
	return q.g.RemovePlayer(requesterID, playerID)
}

// SwitchPlayerToSpectator is only reached in the lobby (setRole checks),
// where leaving frees the seat.
func (q *quizGame) SwitchPlayerToSpectator(playerID, _ string) error {
	return q.g.RemovePlayer(playerID, playerID)
}

func (q *quizGame) SwitchSpectatorToPlayer(name string, isHost bool) (string, error) {
	id, err := q.g.AddPlayer(name, quiz.KindHuman)
	if err == nil && isHost {
		q.g.SetHost(id)
	}
	return id, err
}

func (q *quizGame) PerformAction(playerID string, action json.RawMessage) error {
	return q.g.PerformAction(playerID, action)
}

func (q *quizGame) CompleteOpeningOrder(int64) bool { return false }

func (q *quizGame) PublicState(viewerID string, v Viewer) ([]byte, error) {
	spectators := make([]quiz.Spectator, len(v.Spectators))
	for i, sp := range v.Spectators {
		spectators[i] = quiz.Spectator{ID: sp.ID, Name: sp.Name, IsHost: sp.IsHost}
	}
	return q.g.PublicState(viewerID, quiz.Viewer{Role: v.Role, Name: v.Name, SpectatorID: v.SpectatorID, IsHost: v.IsHost, Spectators: spectators})
}

func (q *quizGame) Snapshot() ([]byte, error) { return q.g.Snapshot() }
func (q *quizGame) SetHost(playerID string)   { q.g.SetHost(playerID) }
func (q *quizGame) Touch()                    { q.g.Touch() }
func (q *quizGame) Now() int64                { return q.g.Now() }

// EndGame implements GameEnder: places by score as they stand, the final
// unplayed.
func (q *quizGame) EndGame() (string, error) { return q.g.EndGame() }

// Info implements Informer. Players who left mid-game are listed as out; on
// a tie the first winner in seat order is the WinnerID.
func (q *quizGame) Info() GameInfo {
	g := q.g
	info := GameInfo{
		Status: g.Status, Phase: g.Phase, HostID: g.HostID, Version: g.Version,
		MaxSeats: g.SeatLimit(), Players: make([]PlayerInfo, 0, len(g.Players)),
	}
	if len(g.WinnerIDs) > 0 {
		info.WinnerID = g.WinnerIDs[0]
	}
	if g.Status == quiz.StatusPlaying {
		info.Turn = g.ClueSeq
	}
	for _, p := range g.Players {
		info.Players = append(info.Players, PlayerInfo{ID: p.ID, Name: p.Name, Kind: p.Kind, Out: p.Left, Place: p.Place})
	}
	if g.Status == quiz.StatusLobby {
		info.OpenSeats = max(0, g.SeatLimit()-len(g.Players)) // nobody has left a lobby: leaving frees the seat
	}
	info.Settings, _ = json.Marshal(g.Settings)
	return info
}
