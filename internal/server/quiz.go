package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tdar200/estate-games/internal/quiz"
)

// Quiz rooms (docs/QUIZ-API.md). The engine owns every timer and plays its
// own computers; the server's part is to wake it at the exact moment its
// next step is due (the choices opening, a clue being judged, a computer's
// tap), to tell it each person's latency and whether their phone is
// connected (the fair buzzer, §6), and to remember the room's played
// categories across rematches. There is no stand-in: an away person simply
// does not answer, and the pick timer covers an away chooser. Final bets are
// secret, so every player needs their own phone.

const msgQuizPhones = "the quiz needs a phone per player"

func quizOf(r *room) *quizGame {
	g, _ := r.game.(*quizGame)
	return g
}

func (r *room) isQuiz() bool { return r.gameType == GameQuiz }

// quizLevels are the computer levels quiz rooms offer (state.aiLevels).
var quizLevels = append([]string(nil), quiz.Levels...)

// quizHostActions may be sent by the table screen (or a host without a
// seat) as the host.
var quizHostActions = map[string]bool{"start": true, "settings": true}

// commitQuiz is commit for quiz rooms: refresh GameInfo, presence and
// friends, then send the engine's new events and every viewer's state.
func (s *Server) commitQuiz(r *room) {
	q := quizOf(r)
	if q == nil {
		return
	}
	now := s.now()
	s.feedQuizLatency(r, q, now) // so each phone's state shows what the server believes
	data, err := q.Snapshot()
	if err != nil {
		s.log.Printf("room %s: snapshot failed: %v", r.code, err)
		return
	}
	prev := r.info
	info := q.Info()
	r.info, r.lastJSON = &info, data
	s.openTableRecord(r, data, now)
	if sig := s.presenceSignature(r, now); sig != r.presenceSig {
		r.presenceSig = sig
		// Clients skip states whose version they already have.
		if prev != nil && info.Version == prev.Version && (r.table || len(r.conns) > 0) {
			q.Touch()
			if touched, err := q.Snapshot(); err == nil {
				info = q.Info()
				r.info, r.lastJSON = &info, touched
			}
		}
	}
	var events [][]byte
	for _, e := range q.g.EventsSince(r.eventSeq) {
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

// scheduleQuiz runs whatever the engine has due, then arms the room's one
// timer for the engine's next deadline. Called from scheduleAI with the
// timer stopped. Deadlines are kept to the millisecond (the choices open on
// every phone at openAt, and a tap's fairness depends on when it is
// judged), but the timer never sleeps longer than the poker tick, so a
// wall-clock jump (a laptop waking up) is caught up quickly.
func (s *Server) scheduleQuiz(r *room, seq uint64) {
	q := quizOf(r)
	if q == nil {
		return
	}
	s.feedQuizLatency(r, q, s.now())
	if q.g.Advance(q.Now()) {
		s.commit(r) // timer moves don't count as use of the room (pruneRooms)
	}
	deadline := q.g.NextDeadline()
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

// feedQuizLatency tells the engine each person's latency and whether their
// phone is connected, which the fair buzzer works from (docs/QUIZ-API.md
// §1.1, §6). It runs before the engine is asked to do anything (an action,
// the room's timer) and at every commit; it never bumps the game's version.
func (s *Server) feedQuizLatency(r *room, q *quizGame, now time.Time) {
	for _, p := range q.g.Players {
		if p.Kind == quiz.KindHuman && !p.Left {
			q.g.SetLatency(p.ID, r.playerLatency(p.ID), s.playerOnline(r, p.ID, p.Kind, now))
		}
	}
}

// playerLatency is a player's latency (docs/QUIZ-API.md §1.1): the lowest
// round trip their open room sockets reported over their last pings, or
// quiz.UnknownLatency when none has (no socket, or an older client that
// sends no rtt). Requires s.mu.
func (r *room) playerLatency(playerID string) int {
	best, known := 0, false
	for c := range r.conns {
		sess := r.sessions[c.token]
		if sess == nil || sess.role != "player" || sess.playerID != playerID {
			continue
		}
		if rtt, ok := c.bestRTT(); ok && (!known || rtt < best) {
			best, known = rtt, true
		}
	}
	if !known {
		return quiz.UnknownLatency
	}
	return best
}

// quizStateFor is stateFor for quiz rooms: the engine's View for this viewer
// plus the fields the server adds (docs/QUIZ-API.md §3.1).
func (s *Server) quizStateFor(r *room, sess *session) (json.RawMessage, error) {
	q := quizOf(r)
	if q == nil {
		return nil, badRequest(msgUnknownGameType)
	}
	now := s.now()
	viewer := Viewer{Role: sess.role, Name: sess.name, SpectatorID: sess.spectatorID, IsHost: sess.isHost, Spectators: s.spectatorList(r, now)}
	raw, err := q.PublicState(sess.playerID, viewer)
	if err != nil {
		return nil, err
	}
	fields, err := splitObject(raw)
	if err != nil {
		return nil, err
	}
	for i := range fields {
		if fields[i].key == "players" {
			if fields[i].value, err = s.quizPlayerFields(r, q, fields[i].value, now); err != nil {
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

// quizPlayerFields adds the server's fields to each player in a quiz view:
// input, tokenKind, color, online, difficulty (computers, from the engine)
// and autopilot, which is always false because the quiz has no stand-in.
func (s *Server) quizPlayerFields(r *room, q *quizGame, raw json.RawMessage, now time.Time) (json.RawMessage, error) {
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
		p := q.g.Player(head.ID)
		if p == nil {
			return nil, fmt.Errorf("quiz state lists an unknown player %q", head.ID)
		}
		tokenKind, color, difficulty := "", "", ""
		if m := r.meta[p.ID]; m != nil {
			tokenKind = m.tokenKind
		}
		if tokenKind != "" {
			color = s.tokenColor(tokenKind)
		}
		if p.Kind == quiz.KindAI {
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

// rematchQuiz rebuilds a finished quiz room's game: the players still at the
// table in the same order (computers at their levels), the same host and
// rules, and the room's memory of played categories, so the new game deals
// categories this room has not seen yet. It returns the old → new player
// ids.
func (s *Server) rematchQuiz(r *room) (map[string]string, error) {
	old := quizOf(r)
	engine, err := s.engineFor(GameQuiz)
	if err != nil {
		return nil, err
	}
	fresh, _ := engine.NewGame(r.code).(*quizGame)
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
		if p.Kind == quiz.KindAI {
			id, err = fresh.g.AddComputer(p.Name, p.Level)
		} else {
			id, err = fresh.g.AddPlayer(p.Name, quiz.KindHuman)
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
