package server

import (
	"encoding/json"

	"github.com/tdar200/estate-games/internal/engine"
)

// Ending a game early and room settings (docs/API.md "Ending a game, room settings").

const (
	msgEndNotPlaying  = "The game is not in progress"
	msgEndUnsupported = "Ending a game early is not supported on this server"
	msgBadSpeed       = "Computer speed must be slow, normal or fast"
	msgBadListed      = "listed must be true or false"
)

// GameEnder is a Game that can be finished early (richest player wins).
type GameEnder interface {
	EndGame() (winnerID string, err error)
}

// EndGame implements GameEnder.
func (e *engineGame) EndGame() (string, error) {
	winner, err := engine.EndGame(e.g)
	if err != nil {
		return "", err
	}
	return winner.ID, nil
}

// endGame stops the game for everyone (host player or table only).
func (s *Server) endGame(code string, body *jsBody) (json.RawMessage, error) {
	if err := body.access("token"); err != nil {
		return nil, err
	}
	r, sess, err := s.authenticateHost(code, sessionToken(body))
	if err != nil {
		return nil, err
	}
	if r.info.Status != "playing" {
		return nil, badRequest(msgEndNotPlaying)
	}
	ender, ok := r.game.(GameEnder)
	if !ok {
		return nil, badRequest(msgEndUnsupported)
	}
	by, via := sess.playerID, "table"
	if sess.role != "table" {
		via = r.playerInput(by, "human")
	}
	if err := s.endRecorded(r, ender, by, via); err != nil {
		return nil, badRequest(err.Error())
	}
	r.stopTimer()
	r.updatedAt = s.now()
	s.commit(r)
	return s.stateFor(r, sess)
}

// Computer speed scales the pause before each computer move and the card
// reading time (Hard still thinks for its full budget).
var computerSpeeds = map[string]float64{"slow": 1.7, "normal": 1, "fast": 0.4}

func (r *room) speedFactor() float64 {
	if f, ok := computerSpeeds[r.computerSpeed]; ok {
		return f
	}
	return 1
}

func (r *room) speedName() string {
	if _, ok := computerSpeeds[r.computerSpeed]; ok {
		return r.computerSpeed
	}
	return "normal"
}

// roomSettings changes room-wide settings (host player or table):
// computerSpeed, botOffers (computers' trade offers to people: normal,
// fewer or off; asks.go), and listed (show the game to everyone in
// /api/games).
func (s *Server) roomSettings(code string, body *jsBody) (json.RawMessage, error) {
	if err := body.access("token"); err != nil {
		return nil, err
	}
	r, sess, err := s.authenticateHost(code, sessionToken(body))
	if err != nil {
		return nil, err
	}
	if raw := body.raw("computerSpeed"); len(raw) > 0 {
		speed, _ := body.exactString("computerSpeed")
		if _, ok := computerSpeeds[speed]; !ok {
			return nil, badRequest(msgBadSpeed)
		}
		r.computerSpeed = speed
	}
	if raw := body.raw("botOffers"); len(raw) > 0 {
		mode, _ := body.exactString("botOffers")
		if !botOfferModes[mode] {
			return nil, badRequest(msgBadBotOffers)
		}
		r.botOffers = mode
	}
	if raw := body.raw("listed"); len(raw) > 0 {
		switch string(raw) {
		case "true":
			r.listed = true
		case "false":
			r.listed = false
		default:
			return nil, badRequest(msgBadListed)
		}
	}
	r.updatedAt = s.now()
	s.commit(r)
	if r.isPoker() || r.isLudo() {
		s.scheduleAI(r) // the computers' pause follows the new speed
	}
	return s.stateFor(r, sess)
}
