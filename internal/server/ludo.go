package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/tdar200/estate-games/internal/ludo"
)

// Ludo rooms (docs/LUDO-API.md). Ludo has no timers of its own: the room's
// single timer plays computer seats after a short pause, plays a person's
// move when there is nothing to choose (one piece can move, or every piece
// would come out of the yard), and switches on the stand-in for an away
// player, who then plays as Normal would until they are back. Unlike poker,
// Ludo has no secret cards, so the PC's keyboard and controller seats work.

// Ludo pauses. Screens animate a roll and hop pieces square by square
// (web/shared/ludo-fx.js uses the same numbers); computers wait for that
// before their next move so people can follow the game.
const (
	ludoDiceMs       = 700 * time.Millisecond // the die tumbling
	ludoHopMs        = 150 * time.Millisecond // each square a piece hops
	ludoCaptureMs    = 650 * time.Millisecond // a captured piece flying home
	ludoHomeMs       = 800 * time.Millisecond // the home celebration
	ludoForcedPause  = 950 * time.Millisecond // a person's only move, after their roll lands
	ludoRollPause    = 1.0                    // × aiDelay before a computer rolls
	ludoMovePause    = 1.3                    // × aiDelay before a computer moves (after its roll lands)
	defaultLudoLevel = ludo.Normal
)

func ludoOf(r *room) *ludoGame {
	g, _ := r.game.(*ludoGame)
	return g
}

func (r *room) isLudo() bool { return r.gameType == GameLudo }

// ludoPlan is the next move the room's timer will play: a computer seat's
// roll or move, an away player's stand-in move, or a person's forced move. It
// is keyed by the engine's event sequence, which moves with every change to
// the game (not with presence), so a plan survives rescheduling and is
// dropped as soon as the game moves on without it.
type ludoPlan struct {
	playerID string
	eventSeq int64
	standIn  bool
	forced   bool
	at       time.Time
	action   json.RawMessage
}

func (s *Server) newLudoBot(difficulty string) ludo.Bot {
	b, err := ludo.NewBot(difficulty, uint64(randomSeed()))
	if err != nil {
		b, _ = ludo.NewBot(defaultLudoLevel, uint64(randomSeed()))
	}
	return b
}

// commitLudo is commit for Ludo rooms: refresh GameInfo, presence and
// friends, then send the engine's new events and every viewer's state.
func (s *Server) commitLudo(r *room) {
	l := ludoOf(r)
	if l == nil {
		return
	}
	now := s.now()
	data, err := l.Snapshot()
	if err != nil {
		s.log.Printf("room %s: snapshot failed: %v", r.code, err)
		return
	}
	prev := r.info
	info := l.Info()
	r.info, r.lastJSON = &info, data
	s.refreshAutopilot(r, now)
	s.openTableRecord(r, data, now)
	if sig := s.presenceSignature(r, now); sig != r.presenceSig {
		r.presenceSig = sig
		// Clients skip states whose version they already have.
		if prev != nil && info.Version == prev.Version && (r.table || len(r.conns) > 0) {
			l.Touch()
			if touched, err := l.Snapshot(); err == nil {
				info = l.Info()
				r.info, r.lastJSON = &info, touched
			}
		}
	}
	var events [][]byte
	speed := r.speedFactor()
	for _, e := range l.g.EventsSince(r.eventSeq) {
		if event, err := marshalJS(e); err == nil {
			events = append(events, messageJSON("event", "event", event))
			s.recordTableEvent(r, e.Seq, event, now)
		}
		r.eventSeq = e.Seq
		// Computers wait for what the screens are showing.
		var hold time.Duration
		switch e.Type {
		case ludo.EvRoll:
			hold = ludoDiceMs
		case ludo.EvMove:
			hold = time.Duration(len(e.Path)) * ludoHopMs
		case ludo.EvCapture:
			hold = ludoCaptureMs
		case ludo.EvHome:
			hold = ludoHomeMs
		}
		if hold > 0 {
			r.ludoHoldUntil = maxTime(r.ludoHoldUntil, now).Add(time.Duration(float64(hold) * min(speed, 1) * s.ludoPace))
		}
	}
	s.tableRecordCommit(r, data, now)
	s.trackGameProgress(r, prev, r.info, now)
	s.broadcastMessages(r, events)
	s.checkDevicePresence(r, now)
	s.markRoomsDirty()
}

// scheduleLudo arms the room's one timer for whatever comes first: the
// planned move or switching on the stand-in for an away player. Called from
// scheduleAI with the timer stopped. The timer never sleeps longer than the
// poker tick, so a wall-clock jump is caught up quickly.
func (s *Server) scheduleLudo(r *room, seq uint64) {
	l := ludoOf(r)
	if l == nil || r.info.Status != "playing" {
		r.ludoPlan, r.stallKey = nil, ""
		return
	}
	now := s.now()
	var due time.Time
	if plan := s.ludoPlanFor(r, l, now); plan != nil {
		due = plan.at
	} else {
		due = s.ludoStandInDue(r, l, now)
	}
	if due.IsZero() {
		return
	}
	delay := min(max(0, due.Sub(now)), s.pokerTick)
	r.timer = time.AfterFunc(delay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.recoverAI(r)
		if s.closed || r.timerSeq != seq || s.rooms[r.code] != r {
			return
		}
		r.timer = nil
		s.runLudoTimer(r)
	})
}

// runLudoTimer is the room timer firing (under s.mu): the stand-in switch,
// then a due move, then the next timer.
func (s *Server) runLudoTimer(r *room) {
	l := ludoOf(r)
	if l == nil {
		return
	}
	now := s.now()
	s.ludoSwitchStandIn(r, l, now)
	if plan := r.ludoPlan; plan != nil && !now.Before(plan.at) && plan.eventSeq == l.g.EventSeq {
		if !s.playLudoPlan(r, l, plan) {
			s.log.Printf("room %s: computers paused after %d rejected moves; trying again in %v", r.code, r.aiFailures, aiRetryDelay)
			s.retryAILater(r, r.timerSeq)
			return
		}
	}
	s.scheduleAI(r)
}

// ludoPlanFor returns the move the room's timer should play next, deciding
// it when it is new; nil when a person has to decide.
func (s *Server) ludoPlanFor(r *room, l *ludoGame, now time.Time) *ludoPlan {
	id, kind := l.g.Waiting()
	p := r.info.player(id)
	if p == nil {
		r.ludoPlan = nil
		return nil
	}
	m := r.meta[id]
	ai := p.Kind == ludo.KindAI
	standIn := !ai && m != nil && m.autopilot
	forced := false
	if !ai && !standIn {
		// A person rolls for themselves; the server only moves for them when
		// there is nothing to choose.
		if _, ok := l.g.ForcedMove(); kind != ludo.PhaseMove || !ok {
			r.ludoPlan = nil
			return nil
		}
		forced = true
	}
	if old := r.ludoPlan; old != nil && old.playerID == id && old.eventSeq == l.g.EventSeq && old.standIn == standIn && old.forced == forced {
		return old
	}
	plan := &ludoPlan{playerID: id, eventSeq: l.g.EventSeq, standIn: standIn, forced: forced}
	if forced {
		plan.at = maxTime(now.Add(time.Duration(float64(ludoForcedPause)*s.ludoPace)), r.ludoHoldUntil)
		piece, _ := l.g.ForcedMove()
		plan.action, _ = json.Marshal(ludo.MoveCommand(piece))
		r.ludoPlan = plan
		return plan
	}
	pause := ludoRollPause
	if kind == ludo.PhaseMove {
		pause = ludoMovePause
	}
	plan.at = maxTime(now.Add(time.Duration(float64(s.aiDelay)*pause*r.speedFactor())), r.ludoHoldUntil)
	plan.action = s.thinkLudo(r, l, id, m, standIn)
	if plan.action == nil {
		r.ludoPlan = nil
		return nil
	}
	r.ludoPlan = plan
	return plan
}

// thinkLudo asks the seat's bot (the stand-in plays as Normal) for its move.
// Ludo bots decide in well under a millisecond, so this runs under the lock;
// a crash or an illegal answer becomes the safe move (roll, or the first
// movable piece).
func (s *Server) thinkLudo(r *room, l *ludoGame, playerID string, m *playerMeta, standIn bool) json.RawMessage {
	var bot ludo.Bot
	switch {
	case standIn:
		bot = s.newLudoBot(ludo.Normal)
	case m != nil && m.ludoBot != nil:
		bot = m.ludoBot
	default:
		difficulty := defaultLudoLevel
		if m != nil && m.difficulty != "" {
			difficulty = m.difficulty
		}
		bot = s.newLudoBot(difficulty)
		if m != nil {
			m.ludoBot = bot
		}
	}
	view := l.g.View(playerID, ludo.Viewer{Role: "player"})
	cmd, err := func() (cmd ludo.Command, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.log.Printf("room %s: computer %s crashed while thinking: %v\n%s", r.code, playerID, recovered, debug.Stack())
				err = fmt.Errorf("computer crashed: %v", recovered)
			}
		}()
		return bot.Decide(view)
	}()
	if err == nil {
		err = ludo.Validate(view, cmd)
	}
	if err != nil {
		s.log.Printf("room %s: ludo computer %s: %v; playing safe", r.code, playerID, err)
		safe, ok := ludo.Safe(view)
		if !ok {
			return nil
		}
		cmd = safe
	}
	action, _ := json.Marshal(cmd)
	return action
}

// playLudoPlan plays a due move. It reports false after maxAIFailures
// rejected moves in a row.
func (s *Server) playLudoPlan(r *room, l *ludoGame, plan *ludoPlan) bool {
	r.ludoPlan = nil
	if plan.standIn {
		if m := r.meta[plan.playerID]; m == nil || !m.autopilot {
			return true // they came back before the stand-in moved
		}
	}
	err := l.PerformAction(plan.playerID, plan.action)
	if err != nil {
		s.log.Printf("room %s: ludo move %s for %s failed: %v", r.code, plan.action, plan.playerID, err)
		if safe, ok := ludo.Safe(l.g.View(plan.playerID, ludo.Viewer{Role: "player"})); ok {
			raw, _ := json.Marshal(safe)
			err = l.PerformAction(plan.playerID, raw)
		}
	}
	if err != nil {
		r.aiFailures++
		s.commitIfChanged(r)
		return r.aiFailures < maxAIFailures
	}
	r.aiFailures = 0
	s.commit(r) // timer moves don't count as use of the room (pruneRooms)
	return true
}

// ludoStandInDue returns when the stand-in should take over for the away
// person the game is waiting on (autopilotDelay after the wait on them
// began), or zero when nobody qualifies.
func (s *Server) ludoStandInDue(r *room, l *ludoGame, now time.Time) time.Time {
	id, _ := l.g.Waiting()
	p := r.info.player(id)
	if p == nil || p.Kind == ludo.KindAI || p.Out || s.playerOnline(r, p.ID, p.Kind, now) {
		r.stallKey = ""
		return time.Time{}
	}
	if m := r.meta[p.ID]; m != nil && m.autopilot {
		return time.Time{} // already on: ludoPlanFor plays for them
	}
	if r.stallKey != p.ID {
		r.stallKey, r.stallSince = p.ID, now
	}
	return r.stallSince.Add(s.autopilotDelay)
}

// ludoSwitchStandIn turns the stand-in on for the away person the game has
// been waiting on for autopilotDelay. It plays for them (as Normal would)
// and stops as soon as they are back online.
func (s *Server) ludoSwitchStandIn(r *room, l *ludoGame, now time.Time) {
	id := r.stallKey
	if id == "" || now.Before(r.stallSince.Add(s.autopilotDelay)) {
		return
	}
	if waiting, _ := l.g.Waiting(); waiting != id {
		return // no longer their turn
	}
	p := r.info.player(id)
	if p == nil || p.Out || p.Kind == ludo.KindAI || s.playerOnline(r, id, p.Kind, now) {
		return
	}
	m := r.meta[id]
	if m == nil {
		m = &playerMeta{input: r.playerInput(id, p.Kind)}
		r.meta[id] = m
	}
	if !m.autopilot {
		m.autopilot = true
		s.log.Printf("room %s: the computer plays for %s while they're away", r.code, p.Name)
		s.commit(r) // publishes players[].autopilot
	}
}

// ludoStateFor is stateFor for Ludo rooms: the engine's View for this viewer
// plus the fields the server adds (docs/LUDO-API.md §3.6).
func (s *Server) ludoStateFor(r *room, sess *session) (json.RawMessage, error) {
	now := s.now()
	viewer := Viewer{Role: sess.role, Name: sess.name, SpectatorID: sess.spectatorID, IsHost: sess.isHost, Spectators: s.spectatorList(r, now)}
	raw, err := r.game.PublicState(sess.playerID, viewer)
	if err != nil {
		return nil, err
	}
	fields, err := splitObject(raw)
	if err != nil {
		return nil, err
	}
	for i := range fields {
		if fields[i].key != "players" {
			continue
		}
		var players []json.RawMessage
		if err := json.Unmarshal(fields[i].value, &players); err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		buf.WriteByte('[')
		for j, p := range players {
			if j > 0 {
				buf.WriteByte(',')
			}
			var head struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal(p, &head); err != nil {
				return nil, err
			}
			tokenKind, difficulty, autopilot := "", "", false
			if m := r.meta[head.ID]; m != nil {
				tokenKind, autopilot = m.tokenKind, m.autopilot
				if head.Kind == ludo.KindAI {
					difficulty = m.difficulty
					if difficulty == "" {
						difficulty = defaultLudoLevel
					}
				}
			}
			color := ""
			if tokenKind != "" {
				color = s.tokenColor(tokenKind)
			}
			extra, _ := marshalJS(struct {
				Input      string `json:"input"`
				TokenKind  string `json:"tokenKind"`
				Color      string `json:"color,omitempty"`
				Online     bool   `json:"online"`
				Difficulty string `json:"difficulty,omitempty"`
				Autopilot  bool   `json:"autopilot,omitempty"`
			}{r.playerInput(head.ID, head.Kind), tokenKind, color, s.playerOnline(r, head.ID, head.Kind, now), difficulty, autopilot})
			buf.Write(appendFields(p, extra))
		}
		buf.WriteByte(']')
		fields[i].value = buf.Bytes()
	}
	fields, err = s.appendRoomFields(r, sess, fields, now)
	if err != nil {
		return nil, err
	}
	return unescapeHTMLInJSON(joinObject(fields)), nil
}

// rematchLudo rebuilds a finished Ludo room's game: same players in the same
// order (those still at the table), same host, same rules, same local
// seats. It returns the old → new player ids.
func (s *Server) rematchLudo(r *room) (map[string]string, error) {
	old := ludoOf(r)
	engine, err := s.engineFor(GameLudo)
	if err != nil {
		return nil, err
	}
	game := engine.NewGame(r.code)
	l, _ := game.(*ludoGame)
	if old == nil || l == nil {
		return nil, badRequest(msgUnknownGameType)
	}
	// The rules first, so the new seats get the right number of pieces.
	settings, _ := json.Marshal(old.g.Settings)
	l.g.Settings = old.g.Settings
	idMap := map[string]string{}
	meta := map[string]*playerMeta{}
	for _, p := range old.g.Players {
		if p.Left {
			continue
		}
		id, err := l.AddPlayer(p.Name, p.Kind)
		if err != nil {
			return nil, err
		}
		idMap[p.ID] = id
		if m := r.meta[p.ID]; m != nil {
			meta[id] = m
			m.autopilot = false
		}
	}
	if host := idMap[old.g.HostID]; host != "" {
		l.SetHost(host)
		if err := l.PerformAction(host, json.RawMessage(`{"type":"settings","settings":`+string(settings)+`}`)); err != nil {
			s.log.Printf("room %s: rematch rules not kept: %v", r.code, err)
		}
	}
	for l.g.Version <= old.g.Version { // clients drop states whose version they have seen
		l.Touch()
	}
	seats := r.seats[:0]
	for _, seat := range r.seats {
		if id := idMap[seat.PlayerID]; id != "" {
			seat.PlayerID = id
			seats = append(seats, seat)
		}
	}
	r.seats = seats
	for _, m := range meta {
		if m.seatID != "" && r.seat(m.seatID) == nil {
			m.seatID = ""
		}
	}
	r.stopTimer()
	r.game, r.meta = game, meta
	r.aiFailed, r.aiFailures = nil, 0
	r.ludoPlan, r.stallKey = nil, ""
	r.ludoHoldUntil = time.Time{}
	r.eventSeq = l.g.EventSeq // the new seats' join events are old news
	return idMap, nil
}

// ludoLevels are the computer levels Ludo rooms offer (state.aiLevels).
var ludoLevels = append([]string(nil), ludo.Levels...)

// ludoHostActions may be sent by the table screen (or a host without a
// seat) as the host.
var ludoHostActions = map[string]bool{"start": true, "settings": true}
