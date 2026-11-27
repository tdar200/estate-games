package server

import (
	"encoding/json"
	"strings"
)

// One server hosts several game types (docs/API.md "Game types"): Estate,
// the default, poker (poker.go), Ludo (ludo.go), the quiz (quiz.go) and
// Near Thing (closest.go). A
// room keeps its type for life; the generic code (presence, reclaim,
// summaries, the games list, friends and recent games, End Game, rematch)
// reads GameInfo instead of Estate's game JSON, so it never misreads another
// game.

// Game types.
const (
	GameEstate  = "estate"
	GamePoker   = "poker"
	GameLudo    = "ludo"
	GameQuiz    = "quiz"
	GameClosest = "closest"
)

const msgUnknownGameType = "unknown game type"

// GameInfo is what game-type-independent code needs to know about a game.
type GameInfo struct {
	// WinnerID is the winner (a quiz tie: the first winner in seat order).
	Status, Phase, HostID, WinnerID string
	Version                         int64
	// Turn is Estate's turn number, poker's hand number, Ludo's turn or the
	// quiz's clue number (Near Thing's question number) while the game is
	// running, else 0.
	Turn int
	// MaxSeats is the most players the game takes; OpenSeats is how many a
	// newcomer could take now (0 when joining is closed).
	MaxSeats, OpenSeats int
	Players             []PlayerInfo
	// Settings are the game's rules for summaries (null when none).
	Settings json.RawMessage
}

// AbandonableLobby reports a game nobody is really waiting in: still in the
// lobby with no person seated (no seats at all, or computers only). The PC's
// NEW TABLE leaves such lobbies behind (0.10.2); they are hidden from the
// games list once nobody is online and pruned like empty rooms.
func (g *GameInfo) AbandonableLobby() bool {
	if g == nil || g.Status != "lobby" {
		return false
	}
	for _, p := range g.Players {
		if p.Kind != "ai" {
			return false
		}
	}
	return true
}

// PlayerInfo is one player as generic code sees them.
type PlayerInfo struct {
	ID, Name, Kind string
	Color          string // Estate's seat colour ("" for the other game types)
	// Out: no longer in the game (Estate: bankrupt; poker: eliminated; Ludo,
	// the quiz and Near Thing: left). Such a seat can't be reclaimed or stood in for.
	Out   bool
	Place int
}

// Informer is a Game that describes itself (poker, Ludo, the quiz, Closest
// Guess). Estate
// games are read from their snapshot instead (estateInfo).
type Informer interface {
	Info() GameInfo
}

func (g *GameInfo) player(id string) *PlayerInfo {
	if g == nil || id == "" {
		return nil
	}
	for i := range g.Players {
		if g.Players[i].ID == id {
			return &g.Players[i]
		}
	}
	return nil
}

// estateInfo is GameInfo from Estate's game JSON.
func estateInfo(v *gameView) *GameInfo {
	info := &GameInfo{
		Status: v.Status, Phase: v.Phase, HostID: v.HostID, WinnerID: v.WinnerID, Version: v.Version,
		Turn: v.playingTurn(), MaxSeats: maxPlayersInGame, Settings: v.Settings,
		Players: make([]PlayerInfo, 0, len(v.Players)),
	}
	if v.Status == "lobby" {
		info.OpenSeats = maxPlayersInGame - len(v.Players)
	}
	for _, p := range v.Players {
		info.Players = append(info.Players, PlayerInfo{ID: p.ID, Name: p.Name, Kind: p.Kind, Color: p.Color, Out: p.Bankrupt})
	}
	return info
}

// gameTypeName is the room's game type ("estate" for rooms saved before
// game types existed).
func (r *room) gameTypeName() string {
	if r.gameType == "" {
		return GameEstate
	}
	return r.gameType
}

func (r *room) isPoker() bool { return r.gameType == GamePoker }

// isEstate reports an Estate room (the classic API, trade quotes, game
// records and the Estate AI apply only there).
func (r *room) isEstate() bool { return r.gameType == "" || r.gameType == GameEstate }

// engineFor returns the engine of a game type ("" means Estate).
func (s *Server) engineFor(gameType string) (Engine, error) {
	if gameType == "" || gameType == GameEstate {
		return s.cfg.Engine, nil
	}
	if e := s.engines[gameType]; e != nil {
		return e, nil
	}
	return nil, badRequest(msgUnknownGameType)
}

// requestedGameType reads body.gameType: absent or null means Estate; an
// unknown type is a 400.
func (s *Server) requestedGameType(body *jsBody) (string, error) {
	raw := body.raw("gameType")
	if len(raw) == 0 || string(raw) == "null" {
		return GameEstate, nil
	}
	gameType, ok := body.exactString("gameType")
	if !ok {
		return "", badRequest(msgUnknownGameType)
	}
	if _, err := s.engineFor(gameType); err != nil {
		return "", err
	}
	return gameType, nil
}

// aiLevels are the computer levels a room offers (state.aiLevels).
func (s *Server) aiLevels(r *room) []string {
	if r.isPoker() {
		return s.pokerLevels
	}
	if r.isLudo() {
		return ludoLevels
	}
	if r.isQuiz() {
		return quizLevels
	}
	if r.isClosest() {
		return closestLevels
	}
	return []string{"easy", "normal", "hard", "expert", "grandmaster"}
}

// levelChoice is the error for a level the room doesn't offer: "Choose
// easy, normal, hard, expert or grandmaster".
func levelChoice(levels []string) string {
	if len(levels) < 2 {
		return "Choose " + strings.Join(levels, "")
	}
	return "Choose " + strings.Join(levels[:len(levels)-1], ", ") + " or " + levels[len(levels)-1]
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
