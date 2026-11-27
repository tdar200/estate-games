package server

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
)

// The computer stands in for a player who has gone away, so one dropped phone
// never freezes the table. When an offline player has a decision pending for
// autopilotDelay, the server plays for them with the heuristic AI, safely (it
// never proposes or accepts trades or loans and never builds on their behalf),
// until they come back online. state.players[].autopilot is true meanwhile.
//
// A person who is still connected but has stopped playing (the phone left on
// the sofa) would freeze the table just the same, so the stand-in also takes
// over an Estate decision a connected person has sent no command for in
// idleStandInDelay; any command from their seat hands it back (noteCommand).
// Keyboard and controller seats are treated like phones: they count as
// connected while the table screen is, so without this a player who walked
// away from the TV would stall the game for good. Their commands come from
// the table with their seatId, so they hand the seat back the same way.

const (
	defaultAutopilotDelay = 30 * time.Second
	idleStandInDelay      = 120 * time.Second
)

// actorsToMove lists who the game is waiting for: everyone still to roll for
// the turn order, everyone who may bid in a timed auction (all still in but
// the leader, in turn order from the seat after the latest bid), or the
// single player who must act now.
func actorsToMove(g *gameView) (ids []string, orderRoll bool) {
	if g.Status != "playing" {
		return nil, false
	}
	if g.Phase == "determining_order" {
		if g.OpeningOrder == nil {
			return nil, false
		}
		return g.OpeningOrder.PendingIDs, true
	}
	playerID := ""
	switch g.Phase {
	case "trade":
		if g.Trade != nil {
			playerID = g.Trade.TargetID
		}
	case "loan_offer":
		if g.LoanOffer != nil {
			playerID = g.LoanOffer.TargetID
		}
	case "auction":
		auction := g.Auction
		if auction == nil {
			return nil, false
		}
		active := auction.ActiveIDs
		if len(active) == 1 && auction.BidderID == active[0] {
			return nil, false
		}
		for offset := 0; offset < len(active); offset++ {
			pos := (auction.TurnPos + offset) % len(active)
			if active[pos] != auction.BidderID {
				if timedAuction(g) {
					ids = append(ids, active[pos])
					continue
				}
				playerID = active[pos]
				break
			}
		}
		if timedAuction(g) {
			return ids, false
		}
	default:
		if g.CurrentIndex >= 0 && g.CurrentIndex < len(g.Players) {
			playerID = g.Players[g.CurrentIndex].ID
		}
	}
	if playerID == "" {
		return nil, false
	}
	return []string{playerID}, false
}

// nextAIPlayer is the computer player that must act now, if any.
func nextAIPlayer(g *gameView) (playerID string, orderRoll bool) {
	ids, orderRoll := actorsToMove(g)
	for _, id := range ids {
		if p := g.player(id); p != nil && p.Kind == "ai" {
			return id, orderRoll
		}
	}
	return "", false
}

// peopleWaitedOn is actorsToMove for the idle and away clocks: nobody in a
// timed auction, whose own clock moves it on (armAuctionClose closes it).
func peopleWaitedOn(g *gameView) (ids []string, orderRoll bool) {
	if timedAuction(g) {
		return nil, false
	}
	return actorsToMove(g)
}

// timedAuction reports whether the game is in a timed open auction: anyone
// still in but the leader may bid at any moment, and the server closes it
// when its clock runs out.
func timedAuction(g *gameView) bool {
	return g != nil && g.Status == "playing" && g.Phase == "auction" && g.Auction != nil && g.Auction.ClockMs > 0
}

// armAuctionClose keeps the timer that closes a timed open auction when its
// clock runs out, as a recorded close_auction step from the server. A bid
// moves the clock on, so every scheduleAI re-arms it; outside a timed
// auction it is stopped.
func (s *Server) armAuctionClose(r *room, view *gameView) {
	if r.auctionTimer != nil {
		r.auctionTimer.Stop()
		r.auctionTimer = nil
	}
	if !timedAuction(view) {
		return
	}
	delay := time.Duration(view.Auction.EndsAt-r.game.Now()) * time.Millisecond
	var timer *time.Timer
	timer = time.AfterFunc(max(0, delay), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.recoverAI(r)
		if s.closed || s.rooms[r.code] != r || r.auctionTimer != timer {
			return
		}
		r.auctionTimer = nil
		s.closeTimedAuction(r)
	})
	r.auctionTimer = timer
}

// auctionClockOut reports whether the game is in a timed auction whose clock
// has run out (the close is due).
func auctionClockOut(r *room, view *gameView) bool {
	return timedAuction(view) && r.game.Now() >= view.Auction.EndsAt
}

// closeTimedAuction ends a timed auction whose clock has run out: the top
// bidder buys the deed (or it stays with the bank when nobody bid).
func (s *Server) closeTimedAuction(r *room) {
	view := r.last
	if !timedAuction(view) {
		return
	}
	if !auctionClockOut(r, view) {
		s.armAuctionClose(r, view) // a bid moved the clock on
		return
	}
	action, _ := json.Marshal(struct {
		Type string `json:"type"`
		Now  int64  `json:"now"`
	}{engine.ActionCloseAuction, r.game.Now()})
	if err := s.applyAction(r, "", action, stepSource{source: gamerecord.SourceServer}); err != nil {
		s.log.Printf("room %s: closing the auction failed: %v", r.code, err)
		s.commitIfChanged(r)
		return
	}
	s.commit(r)
	s.scheduleAI(r)
}

// awayMark is players[].standIn: when the computer stand-in took over a
// person's seat, so their phone can list what happened meanwhile (log
// entries after logId, cash then and now). It stays after the seat is handed
// back (until) until the person's next command.
type awayMark struct {
	Since int64  `json:"since"`           // ms (game clock, like log[].at) when it took over
	LogID string `json:"logId"`           // newest log entry then ("" if none)
	Cash  int    `json:"cash"`            // their cash then
	By    string `json:"by"`              // away (offline) | idle (connected, not playing) | host (the host's Computer plays)
	Until int64  `json:"until,omitempty"` // when the seat was handed back; absent while the stand-in plays
}

// standInOn switches the computer stand-in on for person id, by "away",
// "idle" or "host", and marks where their while-away list starts. A mark
// they have not dismissed yet (a short return) keeps its start, so the list
// covers both spells.
func (s *Server) standInOn(r *room, id, by string) {
	m := r.meta[id]
	if m == nil {
		kind := "human"
		if p := r.info.player(id); p != nil {
			kind = p.Kind
		}
		m = &playerMeta{input: r.playerInput(id, kind)}
		r.meta[id] = m
	}
	m.autopilot, m.idle = true, by != "away"
	if m.away != nil {
		m.away.By, m.away.Until = by, 0
		return
	}
	mark := &awayMark{Since: r.game.Now(), By: by}
	if view := r.last; view != nil {
		if n := len(view.Log); n > 0 {
			mark.LogID = view.Log[n-1].ID
		}
		if p := view.player(id); p != nil {
			mark.Cash = p.Cash
		}
	}
	m.away = mark
}

// standInOff hands a seat back from the stand-in.
func (r *room) standInOff(m *playerMeta) {
	m.autopilot, m.idle = false, false
	if m.away != nil && m.away.Until == 0 {
		m.away.Until = r.game.Now()
	}
}

// skipping reports whether the host is skipping id's turn (r.skip).
func (r *room) skipping(id string) bool {
	_, ok := r.skip[id]
	return ok
}

// dropFinishedSkips ends each skip once the game has moved on from the
// person: it no longer waits on them and it is not their turn.
func dropFinishedSkips(r *room, view *gameView) {
	if len(r.skip) == 0 {
		return
	}
	ids, _ := actorsToMove(view)
	for id := range r.skip {
		if p := view.player(id); p != nil && !p.Bankrupt && (slices.Contains(ids, id) || turnOf(view) == id) {
			continue
		}
		delete(r.skip, id)
	}
}

// turnOf is the player whose turn it is ("" before the turn order is set
// or once the game is over).
func turnOf(view *gameView) string {
	if view.Status != "playing" || view.Phase == "determining_order" || view.Phase == "order_countdown" {
		return ""
	}
	if view.CurrentIndex >= 0 && view.CurrentIndex < len(view.Players) {
		return view.Players[view.CurrentIndex].ID
	}
	return ""
}

// standInActor returns a person the game waits on whom the computer is
// playing for (or whose turn the host skips). Otherwise, when the game waits
// on people, it arms a timer that switches the stand-in on for the first one
// due: an offline person autopilotDelay after the game started waiting on
// them, a connected one idleStandInDelay after the wait began or their last
// command. It returns "" then.
func (s *Server) standInActor(r *room, view *gameView, seq uint64) (string, bool) {
	ids, orderRoll := actorsToMove(view)
	for _, id := range ids {
		if r.skipping(id) {
			return id, orderRoll
		}
	}
	if timedAuction(view) {
		// The auction's own clock moves it on: nobody's idle or away clock
		// runs, and the stand-in bids only for people it already plays for.
		r.stallKey = ""
		for _, id := range ids {
			if m := r.meta[id]; m != nil && m.autopilot {
				return id, false
			}
		}
		return "", false
	}
	now := s.now()
	var next time.Time
	stalled := false
	for _, id := range ids {
		p := view.player(id)
		if p == nil || p.Kind == "ai" || p.Bankrupt {
			continue
		}
		if m := r.meta[id]; m != nil && m.autopilot {
			return id, orderRoll
		}
		var at time.Time
		switch {
		case s.playerOnline(r, id, p.Kind, now):
			at = r.idleSince(id, now).Add(idleStandInDelay)
		case !stalled:
			// The first away person is timed (several can be due only while
			// rolling for the turn order; the next one's wait starts after).
			stalled = true
			if r.stallKey != id {
				r.stallKey, r.stallSince = id, now
			}
			at = r.stallSince.Add(s.autopilotDelay)
		default:
			continue
		}
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	if !stalled {
		r.stallKey = ""
	}
	if next.IsZero() {
		return "", false
	}
	r.timer = time.AfterFunc(max(0, next.Sub(now)), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.recoverAI(r)
		if s.closed || r.timerSeq != seq || s.rooms[r.code] != r {
			return
		}
		r.timer = nil
		if s.switchOnStandIns(r, s.now(), true) {
			s.commit(r) // publishes players[].autopilot
		}
		s.scheduleAI(r)
	})
	return "", false
}

// switchOnStandIns turns the stand-in on for each person the game has waited
// on too long: the timed away person (r.stallKey) autopilotDelay after the
// wait on them began, a connected one idleStandInDelay after their idle clock
// started (r.idleFrom). away false checks only connected people (the sweep
// leaves away ones to their timer). Reports whether any switched on.
func (s *Server) switchOnStandIns(r *room, now time.Time, away bool) bool {
	if r.last == nil {
		return false
	}
	ids, _ := peopleWaitedOn(r.last)
	changed := false
	for _, id := range ids {
		p := r.last.player(id)
		if p == nil || p.Kind == "ai" || p.Bankrupt {
			continue
		}
		m := r.meta[id]
		if m != nil && m.autopilot {
			continue
		}
		idle := s.playerOnline(r, id, p.Kind, now)
		if idle {
			if since, ok := r.idleFrom[id]; !ok || now.Before(since.Add(idleStandInDelay)) {
				continue
			}
		} else if !away || r.stallKey != id || now.Before(r.stallSince.Add(s.autopilotDelay)) {
			continue
		}
		if idle {
			s.standInOn(r, id, "idle")
			s.log.Printf("room %s: the computer plays for %s, who hasn't played for %v", r.code, p.Name, idleStandInDelay)
		} else {
			s.standInOn(r, id, "away")
			s.log.Printf("room %s: the computer plays for %s while they're away", r.code, p.Name)
		}
		changed = true
	}
	return changed
}

// computerDue reports whether the game waits on a computer, on a person the
// stand-in plays for or whose turn the host skips, on the turn-order
// countdown, or on a timed auction's clock with no close timer armed:
// something the server itself must move.
func computerDue(r *room) bool {
	view := r.last
	if view == nil {
		return false
	}
	if view.Phase == "order_countdown" || (timedAuction(view) && r.auctionTimer == nil) {
		return true
	}
	ids, _ := actorsToMove(view)
	for _, id := range ids {
		p := view.player(id)
		if p == nil || p.Bankrupt {
			continue
		}
		if m := r.meta[id]; p.Kind == "ai" || (m != nil && m.autopilot) || r.skipping(id) {
			return true
		}
	}
	return false
}

// idleSince is when the idle clock of a connected person the game waits on
// started: when the wait began, or their last command since. Requires the
// person to be due now; their clock starts now if it hasn't yet.
func (r *room) idleSince(id string, now time.Time) time.Time {
	if r.idleFrom == nil {
		r.idleFrom = map[string]time.Time{}
	}
	since, ok := r.idleFrom[id]
	if !ok {
		since = now
		r.idleFrom[id] = now
	}
	return since
}

// noteWaiting keeps the idle clocks of the connected people the game waits
// on (Estate commits). Anyone else's clock is dropped, so it starts over the
// next time the game waits on them, or when they come back online.
func (s *Server) noteWaiting(r *room, view *gameView, now time.Time) {
	ids, _ := peopleWaitedOn(view)
	waiting := map[string]bool{}
	for _, id := range ids {
		if p := view.player(id); p != nil && p.Kind != "ai" && !p.Bankrupt && s.playerOnline(r, id, p.Kind, now) {
			waiting[id] = true
			r.idleSince(id, now)
		}
	}
	for id := range r.idleFrom {
		if !waiting[id] {
			delete(r.idleFrom, id)
		}
	}
}

// noteCommand records a command from a person's own seat in an Estate game
// (an action, even a refused one, a trade quote, a not-selling mark): their
// idle clock starts over, and a stand-in playing for them hands the seat
// back (its while-away mark stays, with until set). Otherwise a while-away
// mark already handed back is dismissed, and a skip of their turn by the
// host ends. A host acting from the table or as a spectator is not their
// seat. Reports whether any of that changed (the caller publishes it and
// reschedules).
func (s *Server) noteCommand(r *room, sess *session, body *jsBody, actor string) bool {
	if !r.isEstate() || actor == "" {
		return false
	}
	switch sess.role {
	case "player":
	case "table":
		if seatID, _ := body.exactString("seatId"); seatID == "" {
			return false
		}
	default:
		return false
	}
	if _, ok := r.idleFrom[actor]; ok {
		r.idleFrom[actor] = s.now()
	}
	changed := r.skipping(actor)
	delete(r.skip, actor)
	m := r.meta[actor]
	if m == nil {
		return changed
	}
	if !m.autopilot {
		if m.away != nil {
			m.away = nil
			changed = true
		}
		return changed
	}
	r.standInOff(m)
	if p := r.info.player(actor); p != nil {
		s.log.Printf("room %s: %s plays again; the computer stops standing in", r.code, p.Name)
	}
	return true
}

// refreshAutopilot hands seats back to players who are online again (any
// game type; r.info must be current). A stand-in switched on because the
// person sat idle while connected stays until a command of theirs
// (noteCommand) or the end of the game.
func (s *Server) refreshAutopilot(r *room, now time.Time) {
	if r.stallKey != "" {
		// Back (even briefly): the wait starts over next time they leave.
		if p := r.info.player(r.stallKey); p == nil || s.playerOnline(r, p.ID, p.Kind, now) {
			r.stallKey = ""
		}
	}
	for id, m := range r.meta {
		if r.info.Status == "lobby" {
			m.away = nil // a rematch starts with no while-away list
		}
		if !m.autopilot {
			continue
		}
		p := r.info.player(id)
		switch {
		case p == nil || p.Out:
		case m.idle:
			if r.info.Status == "playing" {
				continue
			}
		case !s.playerOnline(r, id, p.Kind, now):
			continue
		}
		if r.isEstate() {
			r.standInOff(m)
		} else {
			m.autopilot, m.idle = false, false
		}
	}
}

// Card reading time: the screens show a drawn card after the dice and the hop,
// then keep it up for max(3.5 s, 2 s + 55 ms per character), capped at 9 s
// (web/shared and web/phone use the same rule). Computers wait that long
// before their next move. The computer speed setting scales only the dice and
// hop lead (and lengthens reading at "slow"): the reading time itself never
// shrinks, because the screens always show the card that long. Everything
// scales with the computer move delay, so tests with a tiny delay stay fast.
const (
	cardLead     = 2 * time.Second // dice and hop before the card shows
	cardReadMin  = 3500 * time.Millisecond
	cardReadMax  = 9 * time.Second
	cardReadBase = 2 * time.Second
	cardReadChar = 55 * time.Millisecond
)

func cardReadTime(text string) time.Duration {
	read := cardReadBase + time.Duration(len([]rune(text)))*cardReadChar
	return min(max(read, cardReadMin), cardReadMax)
}

func (s *Server) cardHold(text string) time.Duration {
	return s.cardHoldAt(text, 1)
}

// cardHoldAt is the hold at a computer speed factor (endgame.go).
func (s *Server) cardHoldAt(text string, speed float64) time.Duration {
	hold := float64(cardLead)*speed + float64(cardReadTime(text))*max(1, speed)
	return time.Duration(hold * float64(s.aiDelay) / float64(defaultAIDelay))
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// recordBankruptcies numbers players in the order they go bankrupt
// (players[].outOrder), which the log can't tell once it is trimmed.
func recordBankruptcies(r *room, prev, next *gameView) {
	if next.Status == "lobby" {
		for _, m := range r.meta {
			m.outOrder = 0
		}
		return
	}
	if prev == nil {
		return
	}
	out := 0
	for _, m := range r.meta {
		out = max(out, m.outOrder)
	}
	for _, p := range next.Players {
		if !p.Bankrupt {
			continue
		}
		if was := prev.player(p.ID); was != nil && was.Bankrupt {
			continue
		}
		m := r.meta[p.ID]
		if m == nil {
			m = &playerMeta{input: r.playerInput(p.ID, p.Kind)}
			r.meta[p.ID] = m
		}
		if m.outOrder == 0 {
			out++
			m.outOrder = out
		}
	}
}

// withChosenDice adds random dice to a roll for a person in "choose dice"
// mode: the engine requires them from people, and the stand-in (and the
// fallback move) would otherwise send a bare roll that is refused forever.
func withChosenDice(view *gameView, playerID string, action json.RawMessage) json.RawMessage {
	if action == nil || actionType(action) != engine.ActionRoll {
		return action
	}
	if p := view.player(playerID); p == nil || p.Kind == "ai" {
		return action
	}
	var settings struct {
		DiceMode string `json:"diceMode"`
	}
	if json.Unmarshal(view.Settings, &settings) != nil || settings.DiceMode != engine.DiceModeChoice {
		return action
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(action, &fields) != nil {
		return action
	}
	if _, ok := fields["dice"]; ok {
		return action
	}
	fields["dice"], _ = json.Marshal([]int{1 + randomIndex(6), 1 + randomIndex(6)})
	out, err := json.Marshal(fields)
	if err != nil {
		return action
	}
	return out
}

// standInAction is what the computer does for an away player: the heuristic's
// choice, except that it never makes deals or builds for them; offers to them
// are declined and their own deal-making turns into moving the game along.
func standInAction(view *gameView, gameJSON []byte, playerID string) (json.RawMessage, error) {
	var g ai.Game
	if err := json.Unmarshal(gameJSON, &g); err != nil {
		return nil, err
	}
	for _, p := range g.Players {
		if p != nil && p.ID == playerID {
			p.Kind = "ai" // internal/ai only plays computer seats
		}
	}
	action := ai.ChooseAction(&g, playerID)
	if action == nil {
		return aiFallback(view, playerID), nil
	}
	raw, err := json.Marshal(action)
	if err != nil {
		return nil, err
	}
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &head)
	switch head.Type {
	case engine.ActionOfferTrade, engine.ActionAcceptTrade, engine.ActionOfferLoan, engine.ActionAcceptLoan, engine.ActionBuild:
		return aiFallback(view, playerID), nil
	}
	return raw, nil
}
