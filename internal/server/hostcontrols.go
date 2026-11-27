package server

import (
	"encoding/json"
	"slices"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
)

// Host controls during an Estate game in a party room (a table or a
// phone-hosted game; docs/API.md §3c). Classic rooms (POST /api/rooms) keep
// the Node server's behaviour: no mid-game removal. The host (their phone,
// or the table screen) may, for any person still in the game:
//
//   - Computer plays (POST /api/rooms/{code}/standin): switch the computer
//     stand-in on for them now. It stays until a command from their own seat
//     (I'M BACK: the action {"type":"back"}), as for an idle person.
//   - Skip turn (POST /api/rooms/{code}/skip): play the decision the game
//     waits on them for with the stand-in's safe moves (pass, decline,
//     reject, pay or raise cash, end the turn), until the game moves on
//     from them: it no longer waits on them and it is not their turn.
//   - Remove (POST /api/rooms/{code}/remove, which removes lobby players
//     too): take them (or a computer) out of the game. Their deeds and
//     buildings go back to the bank, their cash is gone, a debt they owe is
//     written off, and their seat closes (their phone's session ends).
//
// The game's own record keeps these as host steps (source "host").

const (
	// actionBack is I'M BACK: a person's command that hands their seat back
	// from the stand-in (or dismisses the while-away list) and does nothing
	// else.
	actionBack = "back"

	msgServerOnly       = "Only the server can do that"
	msgHostControlsGame = "Host controls are only for Estate party games in play"
	msgHostTarget       = "Choose a person still in the game"
	msgSkipNotWaiting   = "The game is not waiting on that player"
	msgRemovedFromGame  = "The host removed you from the game"
)

// hostTarget authenticates the host for a host control and finds the
// person (body.playerId) it acts on: a person, not a computer, still in an
// Estate game in play.
func (s *Server) hostTarget(code string, body *jsBody) (*room, *session, string, error) {
	if err := body.access("token"); err != nil {
		return nil, nil, "", err
	}
	r, sess, err := s.authenticateHost(code, sessionToken(body))
	if err != nil {
		return nil, nil, "", err
	}
	if !r.isEstate() || !r.table || r.info == nil || r.info.Status != "playing" || r.last == nil {
		return nil, nil, "", badRequest(msgHostControlsGame)
	}
	targetID, _ := body.exactString("playerId")
	p := r.last.player(targetID)
	if p == nil || p.Kind == "ai" || p.Bankrupt {
		return nil, nil, "", badRequest(msgHostTarget)
	}
	return r, sess, targetID, nil
}

// hostVia is the input a host control came from, for the game record.
func (r *room) hostVia(sess *session) string {
	if sess.role == "table" {
		return "table"
	}
	return r.playerInput(sess.playerID, "human")
}

// hostStandIn is POST /api/rooms/{code}/standin: the host's "Computer plays".
func (s *Server) hostStandIn(code string, body *jsBody) (json.RawMessage, error) {
	r, sess, targetID, err := s.hostTarget(code, body)
	if err != nil {
		return nil, err
	}
	s.standInOn(r, targetID, "host")
	if p := r.info.player(targetID); p != nil {
		s.log.Printf("room %s: the host has the computer play for %s", r.code, p.Name)
	}
	r.updatedAt = s.now()
	s.commit(r) // publishes players[].autopilot and standIn
	s.scheduleAI(r)
	return s.stateFor(r, sess)
}

// hostSkip is POST /api/rooms/{code}/skip: the host's "Skip turn".
func (s *Server) hostSkip(code string, body *jsBody) (json.RawMessage, error) {
	r, sess, targetID, err := s.hostTarget(code, body)
	if err != nil {
		return nil, err
	}
	ids, _ := actorsToMove(r.last)
	if !slices.Contains(ids, targetID) && turnOf(r.last) != targetID {
		return nil, badRequest(msgSkipNotWaiting)
	}
	if r.skip == nil {
		r.skip = map[string]string{}
	}
	r.skip[targetID] = r.hostVia(sess)
	if p := r.info.player(targetID); p != nil {
		s.log.Printf("room %s: the host skips %s's turn", r.code, p.Name)
	}
	r.updatedAt = s.now()
	s.touchPresence(r) // publishes players[].skipped
	s.scheduleAI(r)
	return s.stateFor(r, sess)
}

// removeFromGame is the host's "Remove" while an Estate game is in play
// (removePlayer): a recorded remove_player step, then the removed person's
// seat closes.
func (s *Server) removeFromGame(r *room, sess *session, targetID string) (json.RawMessage, error) {
	requester := "" // the table screen acts for the host
	if sess.role == "player" {
		requester = sess.playerID
	}
	action, _ := json.Marshal(struct {
		Type     string `json:"type"`
		PlayerID string `json:"playerId"`
	}{engine.ActionRemovePlayer, targetID})
	if err := s.applyAction(r, requester, action, stepSource{source: gamerecord.SourceHost, via: r.hostVia(sess)}); err != nil {
		s.commitIfChanged(r)
		return nil, err
	}
	if m := r.meta[targetID]; m != nil {
		// Kept for players[].tokenKind and outOrder; nobody plays the seat
		// and their device can't rejoin it.
		m.autopilot, m.idle, m.away = false, false, nil
		m.deviceID, m.seatID = "", ""
	}
	delete(r.skip, targetID)
	delete(r.idleFrom, targetID)
	if r.stallKey == targetID {
		r.stallKey = ""
	}
	removedHost := false
	for _, candidate := range append([]*session(nil), r.order...) {
		if candidate.role == "player" && candidate.playerID == targetID {
			if candidate.isHost {
				removedHost = true
			}
			s.endSession(r, candidate.token, msgRemovedFromGame)
		}
	}
	for i, seat := range r.seats {
		if seat.PlayerID == targetID {
			r.seats = append(r.seats[:i], r.seats[i+1:]...)
			break
		}
	}
	if removedHost && r.table {
		s.promoteHostPlayer(r)
	}
	if p := r.info.player(targetID); p != nil {
		s.log.Printf("room %s: the host removed %s from the game", r.code, p.Name)
	}
	r.updatedAt = s.now()
	s.commit(r)
	s.scheduleAI(r)
	return s.stateFor(r, sess)
}
