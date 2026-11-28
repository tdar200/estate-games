package server

import (
	"encoding/json"
	"slices"

	"github.com/tdar200/estate-games/internal/closest"
	"github.com/tdar200/estate-games/internal/quiz"
)

// The quiz deals its categories, and Near Thing asks its questions, stalest
// first from a memory of what was played (the games' History, least recently
// played first). That memory used to live only in the room: PLAY AGAIN kept
// it, but every new table (a new code: NEW TABLE, switching to another game
// and back, a screen started afresh) began blank, so a household playing on
// the same TV met the same categories and questions again.
//
// The device that made the room (the table screen, or the host's phone for a
// phone game) now keeps the memory in its device record (estate.json): it is
// merged into the game when the game starts, and saved back as it plays.
// Only the table whose game started last saves (PlayedBy): an older table
// left open on the same device keeps playing from its own memory, but can't
// take the device's back to before the newer one.

// maxPlayedMemory bounds one device's memory per game type (the packs have
// fewer ids than this; a longer memory only drops the stalest).
const maxPlayedMemory = 400

// playedGame is a game dealt from a played memory.
type playedGame interface {
	played() []string
	setPlayed(ids []string) // only takes effect in the lobby
	started() bool
}

func (q *quizGame) played() []string       { return q.g.Played() }
func (q *quizGame) setPlayed(ids []string) { q.g.SetPlayed(ids) }
func (q *quizGame) started() bool          { return q.g.Status != quiz.StatusLobby }

func (c *closestGame) played() []string       { return c.g.Played() }
func (c *closestGame) setPlayed(ids []string) { c.g.SetPlayed(ids) }
func (c *closestGame) started() bool          { return c.g.Status != closest.StatusLobby }

// playedMemory returns a room's played game and the device record that keeps
// its memory, or nils.
func (s *Server) playedMemory(r *room) (playedGame, *deviceRecord) {
	pg, ok := r.game.(playedGame)
	if !ok || r.creatorDevice == "" {
		return nil, nil
	}
	d := s.devices[r.creatorDevice]
	if d == nil {
		return nil, nil
	}
	return pg, d
}

// seedPlayed runs before an action reaches a quiz or Near Thing game: a start
// from the lobby first gets the device's memory, with the room's own (a
// rematch's) as the most recent part of it.
func (s *Server) seedPlayed(r *room, action json.RawMessage) {
	pg, d := s.playedMemory(r)
	if pg == nil || pg.started() || actionType(action) != "start" {
		return
	}
	if d.PlayedBy[r.gameType] != r.code {
		if d.PlayedBy == nil {
			d.PlayedBy = map[string]string{}
		}
		d.PlayedBy[r.gameType] = r.code
		s.markDirty()
	}
	mine := d.Played[r.gameType]
	if len(mine) == 0 {
		return
	}
	room := pg.played()
	merged := slices.DeleteFunc(slices.Clone(mine), func(id string) bool { return slices.Contains(room, id) })
	pg.setPlayed(append(merged, room...))
}

// savePlayed runs after a quiz or Near Thing room changes: once the game has
// started, its memory (the device's, merged in at the start, plus what this
// game has asked) is the device's memory.
func (s *Server) savePlayed(r *room) {
	pg, d := s.playedMemory(r)
	if pg == nil || !pg.started() || d.PlayedBy[r.gameType] != r.code {
		return
	}
	ids := pg.played()
	if len(ids) > maxPlayedMemory {
		ids = ids[len(ids)-maxPlayedMemory:]
	}
	if slices.Equal(d.Played[r.gameType], ids) {
		return
	}
	if d.Played == nil {
		d.Played = map[string][]string{}
	}
	d.Played[r.gameType] = ids
	s.markDirty()
}
