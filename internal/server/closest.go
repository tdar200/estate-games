package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tdar200/estate-games/internal/closest"
)

// Near Thing rooms (docs/CLOSEST-API.md). The engine owns every timer and
// plays its own computers; the server's part is to wake it when its next
// step is due (a phase timer, a computer's guess or bet), to tell it whose
// phone is connected (an away player's chips are not placed for them when
// the bets close), to give the computers the fill setting adds their seats'
// tokens, and to remember the room's played questions across rematches.
// Guesses and bets are secret, so every player needs their own phone; there
// is no stand-in.

const msgClosestPhones = closest.Title + " needs a phone per player"

func closestOf(r *room) *closestGame {
	g, _ := r.game.(*closestGame)
	return g
}

func (r *room) isClosest() bool { return r.gameType == GameClosest }

// closestLevels are the computer levels Near Thing rooms offer
// (state.aiLevels).
var closestLevels = append([]string(nil), closest.Levels...)

// closestHostActions may be sent by the table screen (or a host without a
// seat) as the host.
var closestHostActions = map[string]bool{"start": true, "settings": true}

// commitClosest is commit for Near Thing rooms: refresh GameInfo,
// presence and friends, then send the engine's new events and every
// viewer's state.
func (s *Server) commitClosest(r *room) {
	c := closestOf(r)
	if c == nil {
		return
	}
	now := s.now()
	s.seatFilledComputers(r, c)
	s.feedClosestOnline(r, c, now)
	data, err := c.Snapshot()
	if err != nil {
		s.log.Printf("room %s: snapshot failed: %v", r.code, err)
		return
	}
	prev := r.info
	info := c.Info()
	r.info, r.lastJSON = &info, data
	s.openTableRecord(r, data, now)
	if sig := s.presenceSignature(r, now); sig != r.presenceSig {
		r.presenceSig = sig
		// Clients skip states whose version they already have.
		if prev != nil && info.Version == prev.Version && (r.table || len(r.conns) > 0) {
			c.Touch()
			if touched, err := c.Snapshot(); err == nil {
				info = c.Info()
				r.info, r.lastJSON = &info, touched
			}
		}
	}
	var events [][]byte
	for _, e := range c.g.EventsSince(r.eventSeq) {
		if event, err := marshalJS(e); err == nil {
			events = append(events, messageJSON("event", "event", event))
			s.recordTableEvent(r, e.Seq, event, now)
		}
		r.eventSeq = e.Seq
	}
	s.tableRecordCommit(r, data, now)
	s.trackGameProgress(r, prev, r.info, now)
	s.broadcastMessages(r, events)
	s.checkDevicePresence(r, now)
	s.markRoomsDirty()
}

// seatFilledComputers gives the computers the engine seated itself (the
// fill setting, at the start) what a computer seat added from the lobby
// gets: a token and its level.
func (s *Server) seatFilledComputers(r *room, c *closestGame) {
	for _, p := range c.g.Players {
		if p.Kind == closest.KindAI && r.meta[p.ID] == nil {
			r.meta[p.ID] = &playerMeta{input: "ai", tokenKind: r.pickToken(""), difficulty: p.Level}
		}
	}
}

// scheduleClosest runs whatever the engine has due, then arms the room's
// one timer for the engine's next deadline. Called from scheduleAI with the
// timer stopped. The timer never sleeps longer than the poker tick, so a
// wall-clock jump (a laptop waking up) is caught up quickly.
func (s *Server) scheduleClosest(r *room, seq uint64) {
	c := closestOf(r)
	if c == nil {
		return
	}
	s.feedClosestOnline(r, c, s.now())
	if c.g.Advance(c.Now()) {
		s.commit(r) // timer moves don't count as use of the room (pruneRooms)
	}
	deadline := c.g.NextDeadline()
	if r.info.Status != "playing" || deadline == 0 {
		return
	}
	delay := min(max(0, time.UnixMilli(deadline).Sub(s.now())), s.pokerTick)
	r.timer = time.AfterFunc(delay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.recoverAI(r)
		if s.closed || r.timerSeq != seq || s.rooms[r.code] != r {
			return
		}
		r.timer = nil
		s.scheduleAI(r) // runs what is due and arms the next timer
	})
}

// feedClosestOnline tells the engine whether each person's phone is
// connected: when the bets close, a player who guessed but did not bet gets
// both chips on their own slot only if they are online (docs/CLOSEST-API.md
// §4). It runs before the engine is asked to do anything (an action, the
// room's timer) and at every commit; it never bumps the game's version.
func (s *Server) feedClosestOnline(r *room, c *closestGame, now time.Time) {
	for _, p := range c.g.Players {
		if p.Kind == closest.KindHuman && !p.Left {
			c.g.SetOnline(p.ID, s.playerOnline(r, p.ID, p.Kind, now))
		}
	}
}

// closestStateFor is stateFor for Near Thing rooms: the engine's View
// for this viewer plus the fields the server adds (docs/CLOSEST-API.md
// §3.1).
func (s *Server) closestStateFor(r *room, sess *session) (json.RawMessage, error) {
	c := closestOf(r)
	if c == nil {
		return nil, badRequest(msgUnknownGameType)
	}
	now := s.now()
	viewer := Viewer{Role: sess.role, Name: sess.name, SpectatorID: sess.spectatorID, IsHost: sess.isHost, Spectators: s.spectatorList(r, now)}
	raw, err := c.PublicState(sess.playerID, viewer)
	if err != nil {
		return nil, err
	}
	fields, err := splitObject(raw)
	if err != nil {
		return nil, err
	}
	for i := range fields {
		if fields[i].key == "players" {
			if fields[i].value, err = s.closestPlayerFields(r, c, fields[i].value, now); err != nil {
				return nil, err
			}
		}
	}
	fields, err = s.appendRoomFields(r, sess, fields, now)
	if err != nil {
		return nil, err
	}
	return unescapeHTMLInJSON(joinObject(fields)), nil
}

// closestPlayerFields adds the server's fields to each player in a view:
// input, tokenKind, color, online, difficulty (computers, from the engine)
// and autopilot, always false: there is no stand-in.
func (s *Server) closestPlayerFields(r *room, c *closestGame, raw json.RawMessage, now time.Time) (json.RawMessage, error) {
	var players []json.RawMessage
	if err := json.Unmarshal(raw, &players); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, view := range players {
		if i > 0 {
			buf.WriteByte(',')
		}
		var head struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(view, &head); err != nil {
			return nil, err
		}
		p := c.g.Player(head.ID)
		if p == nil {
			return nil, fmt.Errorf("%s state lists an unknown player %q", GameClosest, head.ID)
		}
		tokenKind, color, difficulty := "", "", ""
		if m := r.meta[p.ID]; m != nil {
			tokenKind = m.tokenKind
		}
		if tokenKind != "" {
			color = s.tokenColor(tokenKind)
		}
		if p.Kind == closest.KindAI {
			difficulty = p.Level
		}
		extra, _ := marshalJS(struct {
			Input      string `json:"input"`
			TokenKind  string `json:"tokenKind"`
			Color      string `json:"color,omitempty"`
			Online     bool   `json:"online"`
			Difficulty string `json:"difficulty,omitempty"`
			Autopilot  bool   `json:"autopilot"`
		}{r.playerInput(p.ID, p.Kind), tokenKind, color, s.playerOnline(r, p.ID, p.Kind, now), difficulty, false})
		buf.Write(appendFields(view, extra))
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

// rematchClosest rebuilds a finished room's game: the players still at the
// table in the same order (computers at their levels, the ones the fill
// setting added included), the same host and rules, and the room's memory
// of played questions, so the new game asks questions this room has not
// seen. It returns the old → new player ids.
func (s *Server) rematchClosest(r *room) (map[string]string, error) {
	old := closestOf(r)
	engine, err := s.engineFor(GameClosest)
	if err != nil {
		return nil, err
	}
	fresh, _ := engine.NewGame(r.code).(*closestGame)
	if old == nil || fresh == nil {
		return nil, badRequest(msgUnknownGameType)
	}
	fresh.g.Settings = old.g.Settings
	fresh.g.SetPlayed(old.g.Played())
	idMap := map[string]string{}
	meta := map[string]*playerMeta{}
	for _, p := range old.g.Players {
		if p.Left {
			continue
		}
		var id string
		if p.Kind == closest.KindAI {
			id, err = fresh.g.AddComputer(p.Name, p.Level)
		} else {
			id, err = fresh.g.AddPlayer(p.Name, closest.KindHuman)
		}
		if err != nil {
			return nil, err
		}
		idMap[p.ID] = id
		if m := r.meta[p.ID]; m != nil {
			meta[id] = m
		}
	}
	if host := idMap[old.g.HostID]; host != "" {
		fresh.g.SetHost(host)
	}
	// Clients drop states whose version they have seen.
	fresh.g.Version = max(fresh.g.Version, old.g.Version+1)
	r.stopTimer()
	r.game, r.meta = fresh, meta
	r.aiFailed, r.aiFailures = nil, 0
	r.eventSeq = fresh.g.EventSeq // the new seats' join events are old news
	return idMap, nil
}
