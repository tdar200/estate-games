package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/tdar200/estate-games/internal/poker/bots"
	"github.com/tdar200/estate-games/internal/poker/holdem"
)

// Poker rooms (docs/POKER-API.md). The engine owns every poker timer (turn
// timer, time bank, run-out, the pause before the next hand, rebuy offers,
// blind levels); the server only calls Advance when NextDeadline comes, on
// the room's single timer, which also plays computer seats and the
// check-or-fold stand-in for away players.

const (
	msgPokerPhones    = "poker needs a phone per player"
	msgPokerNoQuotes  = "Trade quotes are only for Estate games"
	defaultPokerTick  = 2 * time.Second  // longest the room timer sleeps (see schedulePoker)
	pokerThinkTimeout = 10 * time.Second // a bot's decision (an external bot may be slow)
)

// pokerHostActions may be sent by the table screen (or a host without a
// seat) as the host.
var pokerHostActions = map[string]bool{"start": true, "settings": true, "deal": true}

// PokerBotFactory creates the bot for a new poker computer seat. difficulty
// is one of the room's aiLevels; seed makes it reproducible.
type PokerBotFactory func(difficulty string, seed int64) bots.Bot

// PokerBotConfig configures RealPokerBots.
type PokerBotConfig struct {
	// External, when set, plays "hard" (and "expert") seats: the stronger AI
	// behind --poker-bot. It is wrapped so errors and illegal answers fall
	// back to the Normal bot.
	External bots.Bot
}

// RealPokerBots returns the production bot factory: Easy and Normal built
// in; Hard and Expert use the external bot when configured, otherwise play
// as Normal.
func RealPokerBots(cfg PokerBotConfig) PokerBotFactory {
	return func(difficulty string, seed int64) bots.Bot {
		normal, _ := bots.New(bots.Normal, uint64(seed))
		switch difficulty {
		case "hard", "expert":
			if cfg.External != nil {
				return bots.WithFallback(cfg.External, normal)
			}
			return normal
		}
		b, err := bots.New(difficulty, uint64(seed))
		if err != nil {
			return normal
		}
		return b
	}
}

func (s *Server) newPokerBot(difficulty string) bots.Bot {
	seed := randomSeed()
	if s.cfg.PokerBots != nil {
		if b := s.cfg.PokerBots(difficulty, seed); b != nil {
			return b
		}
	}
	return RealPokerBots(PokerBotConfig{})(difficulty, seed)
}

func pokerOf(r *room) *pokerGame {
	g, _ := r.game.(*pokerGame)
	return g
}

// pokerPlan is a computer decision in progress: an AI seat's bet or rebuy,
// or the stand-in's check-or-fold for an away player. It is keyed by the
// engine's event sequence, which moves with every change to the game (but
// not with presence or host changes), so a plan survives rescheduling and is
// dropped as soon as the game moves on without it.
type pokerPlan struct {
	playerID string
	kind     string // "act" or "rebuy"
	eventSeq int64
	standIn  bool
	at       time.Time       // when it may be played (the computers' pause)
	action   json.RawMessage // nil while the bot is still thinking
}

// commitPoker is commit for poker rooms: refresh GameInfo, presence and
// friends, then send the engine's new events and every viewer's state.
func (s *Server) commitPoker(r *room) {
	g := pokerOf(r)
	if g == nil {
		return
	}
	now := s.now()
	data, err := g.Snapshot()
	if err != nil {
		s.log.Printf("room %s: snapshot failed: %v", r.code, err)
		return
	}
	prev := r.info
	info := g.Info()
	r.info, r.lastJSON = &info, data
	s.refreshAutopilot(r, now)
	s.openTableRecord(r, data, now)
	if sig := s.presenceSignature(r, now); sig != r.presenceSig {
		r.presenceSig = sig
		// Clients skip states whose version they already have.
		if prev != nil && info.Version == prev.Version && (r.table || len(r.conns) > 0) {
			g.Touch()
			if touched, err := g.Snapshot(); err == nil {
				info = g.Info()
				r.info, r.lastJSON = &info, touched
			}
		}
	}
	var events [][]byte
	for _, e := range g.t.EventsSince(r.eventSeq) {
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

// schedulePoker arms the room's one timer for whatever comes first: an
// engine deadline, a computer move, or switching on the stand-in for an away
// player. Called from scheduleAI with the timer stopped. The timer never
// sleeps longer than the poker tick, so a wall-clock jump (a laptop waking
// up) is caught up quickly.
func (s *Server) schedulePoker(r *room, seq uint64) {
	g := pokerOf(r)
	if g == nil {
		return
	}
	if g.t.Advance(g.Now()) { // overdue engine work (a restart, a late timer)
		s.commit(r)
	}
	if r.info.Status != "playing" {
		r.pokerPlan, r.stallKey = nil, ""
		return
	}
	now := s.now()
	var due time.Time
	consider := func(t time.Time) {
		if !t.IsZero() && (due.IsZero() || t.Before(due)) {
			due = t
		}
	}
	if d := g.t.NextDeadline(); d > 0 {
		consider(time.UnixMilli(d))
	}
	waiting := g.t.Waiting()
	if plan := s.pokerPlanFor(r, g, waiting, now); plan != nil && plan.action != nil {
		consider(plan.at)
	}
	consider(s.pokerStandInDue(r, waiting, now))
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
		s.runPokerTimer(r)
	})
}

// runPokerTimer is the room timer firing (under s.mu): engine deadlines,
// then the stand-in switch, then a due computer move, then the next timer.
func (s *Server) runPokerTimer(r *room) {
	g := pokerOf(r)
	if g == nil {
		return
	}
	if g.t.Advance(g.Now()) {
		s.commit(r) // timer moves don't count as use of the room (pruneRooms)
	}
	now := s.now()
	s.pokerSwitchStandIn(r, g, now)
	if plan := r.pokerPlan; plan != nil && plan.action != nil && !now.Before(plan.at) && plan.eventSeq == g.t.EventSeq {
		if !s.playPokerPlan(r, g, plan) {
			s.log.Printf("room %s: computers paused after %d rejected moves; trying again in %v", r.code, r.aiFailures, aiRetryDelay)
			s.retryAILater(r, r.timerSeq)
			return
		}
	}
	s.scheduleAI(r)
}

// pokerPlanFor returns the computer decision the table is waiting for (an AI
// seat to bet or rebuy, or an away player whose stand-in is on), starting a
// bot's thinking when it is new; nil when the computer has nothing to do.
func (s *Server) pokerPlanFor(r *room, g *pokerGame, waiting []holdem.Pending, now time.Time) *pokerPlan {
	for _, w := range waiting {
		p := r.info.player(w.PlayerID)
		if p == nil {
			continue
		}
		m := r.meta[w.PlayerID]
		ai := p.Kind == holdem.KindAI
		standIn := !ai && w.Kind == "act" && m != nil && m.autopilot
		if !ai && !standIn {
			continue // a person decides (the stand-in never decides a rebuy)
		}
		if old := r.pokerPlan; old != nil && old.playerID == w.PlayerID && old.kind == w.Kind && old.eventSeq == g.t.EventSeq && old.standIn == standIn {
			return old
		}
		plan := &pokerPlan{playerID: w.PlayerID, kind: w.Kind, eventSeq: g.t.EventSeq, standIn: standIn,
			at: now.Add(time.Duration(float64(s.aiDelay) * r.speedFactor()))}
		r.pokerPlan = plan
		if standIn {
			plan.action = g.t.SafeAction(w.PlayerID) // check if free, otherwise fold
			if plan.action == nil {
				r.pokerPlan = nil
				continue
			}
			return plan
		}
		s.thinkPoker(r, g, plan, m)
		return plan
	}
	r.pokerPlan = nil
	return nil
}

// thinkPoker asks the seat's bot for its decision off the server lock. The
// bot gets only the seat's own View (what its phone would show), never the
// game snapshot.
func (s *Server) thinkPoker(r *room, g *pokerGame, plan *pokerPlan, m *playerMeta) {
	var bot bots.Bot
	if m != nil {
		bot = m.pokerBot
	}
	if bot == nil {
		difficulty := "normal"
		if m != nil && m.difficulty != "" {
			difficulty = m.difficulty
		}
		bot = s.newPokerBot(difficulty)
		if m != nil {
			m.pokerBot = bot
		}
	}
	view := g.t.View(plan.playerID, holdem.Viewer{Role: "player"})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		select {
		case s.thinkSlots <- struct{}{}:
		case <-s.ctx.Done():
			return
		}
		cmd, err := func() (cmd holdem.Command, err error) {
			defer func() { <-s.thinkSlots }()
			defer func() {
				if recovered := recover(); recovered != nil {
					s.log.Printf("room %s: computer %s crashed while thinking: %v\n%s", r.code, plan.playerID, recovered, debug.Stack())
					err = fmt.Errorf("computer crashed: %v", recovered)
				}
			}()
			ctx, cancel := context.WithTimeout(s.ctx, pokerThinkTimeout)
			defer cancel()
			return bot.Decide(ctx, view)
		}()
		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.recoverAI(r)
		if s.closed || s.rooms[r.code] != r || r.pokerPlan != plan {
			return // the game moved on; whoever moved it scheduled anew
		}
		if err == nil {
			err = bots.Validate(view, cmd)
		}
		if err != nil {
			s.log.Printf("room %s: poker computer %s: %v; playing safe", r.code, plan.playerID, err)
			cmd = bots.Safe(view)
		}
		plan.action, _ = json.Marshal(cmd)
		s.scheduleAI(r)
	}()
}

// playPokerPlan plays a due computer decision. It reports false after
// maxAIFailures rejected moves in a row.
func (s *Server) playPokerPlan(r *room, g *pokerGame, plan *pokerPlan) bool {
	r.pokerPlan = nil
	if plan.standIn {
		if m := r.meta[plan.playerID]; m == nil || !m.autopilot {
			return true // they came back before the stand-in moved
		}
	}
	err := g.PerformAction(plan.playerID, plan.action)
	if err != nil && plan.kind == "act" {
		// Last resort: check if free, otherwise fold.
		s.log.Printf("room %s: poker move %s for %s failed: %v", r.code, plan.action, plan.playerID, err)
		if safe := g.t.SafeAction(plan.playerID); safe != nil {
			err = g.PerformAction(plan.playerID, safe)
		}
	}
	if err != nil {
		r.aiFailures++
		s.log.Printf("room %s: poker move for %s failed: %v", r.code, plan.playerID, err)
		s.commitIfChanged(r)
		return r.aiFailures < maxAIFailures
	}
	r.aiFailures = 0
	s.commit(r)
	return true
}

// pokerStandInDue returns when the stand-in should take over for an away
// player the table is waiting on to act (autopilotDelay after the wait on
// them began), or zero when nobody qualifies.
func (s *Server) pokerStandInDue(r *room, waiting []holdem.Pending, now time.Time) time.Time {
	for _, w := range waiting {
		if w.Kind != "act" {
			continue
		}
		p := r.info.player(w.PlayerID)
		if p == nil || p.Kind == holdem.KindAI || p.Out || s.playerOnline(r, p.ID, p.Kind, now) {
			continue
		}
		if m := r.meta[p.ID]; m != nil && m.autopilot {
			return time.Time{} // already on: pokerPlanFor plays for them
		}
		if r.stallKey != p.ID {
			r.stallKey, r.stallSince = p.ID, now
		}
		return r.stallSince.Add(s.autopilotDelay)
	}
	r.stallKey = ""
	return time.Time{}
}

// pokerSwitchStandIn turns the stand-in on for the away player the table
// has been waiting on for autopilotDelay. It only ever checks or folds for
// them; they stay dealt in, and it stops as soon as they are back online.
func (s *Server) pokerSwitchStandIn(r *room, g *pokerGame, now time.Time) {
	id := r.stallKey
	if id == "" || now.Before(r.stallSince.Add(s.autopilotDelay)) {
		return
	}
	p := r.info.player(id)
	if p == nil || p.Out || p.Kind == holdem.KindAI || s.playerOnline(r, id, p.Kind, now) {
		return
	}
	if g.t.SafeAction(id) == nil {
		return // no longer their turn
	}
	m := r.meta[id]
	if m == nil {
		m = &playerMeta{input: r.playerInput(id, p.Kind)}
		r.meta[id] = m
	}
	if !m.autopilot {
		m.autopilot = true
		s.log.Printf("room %s: the stand-in checks or folds for %s while they're away", r.code, p.Name)
		s.commit(r) // publishes players[].autopilot
	}
}

// pokerStateFor is stateFor for poker rooms: the engine's View for this
// viewer plus the fields the server adds (docs/POKER-API.md §3.9). The
// table screen and spectators have no player id, so they see face-up cards
// only.
func (s *Server) pokerStateFor(r *room, sess *session) (json.RawMessage, error) {
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
				if head.Kind == holdem.KindAI {
					difficulty = m.difficulty
					if difficulty == "" {
						difficulty = "normal"
					}
				}
			}
			extra, _ := marshalJS(struct {
				Input      string `json:"input"`
				TokenKind  string `json:"tokenKind"`
				Online     bool   `json:"online"`
				Difficulty string `json:"difficulty,omitempty"`
				Autopilot  bool   `json:"autopilot,omitempty"`
			}{r.playerInput(head.ID, head.Kind), tokenKind, s.playerOnline(r, head.ID, head.Kind, now), difficulty, autopilot})
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

// rematchPoker rebuilds a finished poker room's table: same players (those
// still at the table), same host, same settings.
func (s *Server) rematchPoker(r *room) (map[string]string, error) {
	old := pokerOf(r)
	engine, err := s.engineFor(GamePoker)
	if err != nil {
		return nil, err
	}
	game := engine.NewGame(r.code)
	g, _ := game.(*pokerGame)
	if old == nil || g == nil {
		return nil, badRequest(msgUnknownGameType)
	}
	idMap := map[string]string{}
	meta := map[string]*playerMeta{}
	for _, p := range old.t.Players {
		if p.Left {
			continue
		}
		id, err := g.AddPlayer(p.Name, p.Kind)
		if err != nil {
			return nil, err
		}
		idMap[p.ID] = id
		if m := r.meta[p.ID]; m != nil {
			meta[id] = m
			m.autopilot = false
		}
	}
	host := idMap[old.t.HostID]
	if host != "" {
		g.SetHost(host)
		settings, _ := json.Marshal(old.t.Settings)
		if err := g.PerformAction(host, json.RawMessage(`{"type":"settings","settings":`+string(settings)+`}`)); err != nil {
			s.log.Printf("room %s: rematch settings not kept: %v", r.code, err)
		}
	}
	for g.t.Version <= old.t.Version { // clients drop states whose version they have seen
		g.Touch()
	}
	r.stopTimer()
	r.game, r.meta = game, meta
	r.aiFailed, r.aiFailures = nil, 0
	r.pokerPlan, r.stallKey = nil, ""
	r.eventSeq = g.t.EventSeq // the new seats' join events are old news
	return idMap, nil
}
