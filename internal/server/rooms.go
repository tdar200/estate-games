package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/tdar200/estate-games/internal/closest"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
	"github.com/tdar200/estate-games/internal/hardai"
	"github.com/tdar200/estate-games/internal/ludo"
	"github.com/tdar200/estate-games/internal/poker/bots"
	"github.com/tdar200/estate-games/internal/poker/holdem"
	"github.com/tdar200/estate-games/internal/quiz"
)

// room is one game plus its sessions (src/rooms.js room object) and the
// party-mode metadata the engine does not know about.
type room struct {
	code      string
	gameType  string // GameEstate, GamePoker, GameLudo, GameQuiz or GameClosest ("" = Estate, from saves before game types)
	game      Game
	sessions  map[string]*session
	order     []*session // creation order (JS Map iteration order)
	table     bool       // created by POST /api/tables
	seats     []*localSeat
	meta      map[string]*playerMeta // engine player id -> metadata
	updatedAt time.Time
	timer     *time.Timer
	timerSeq  uint64
	// auctionTimer closes a timed open auction when its clock runs out
	// (armAuctionClose, standin.go); it runs beside timer, so a computer
	// thinking or about to bid never holds the auction open.
	auctionTimer *time.Timer
	aiFailures   int
	// AI actions the engine rejected at aiFailedVersion (never retried).
	aiFailed        map[string]bool
	aiFailedVersion int64
	// The computer (or stand-in) decision being made (aiStepKey) and how
	// many times in a row it gave no move (aiStepFailed); thinking counts
	// decisions being made off the lock (thinkAsync).
	aiStepKey   string
	aiStepFails int
	thinking    int
	conns       map[*wsConn]struct{}

	// info is the game as of the last commit, for game-type-independent
	// code. last is Estate's game JSON as of then (nil in poker rooms);
	// lastJSON is the snapshot (server-only: it may hold hidden cards).
	info        *GameInfo
	last        *gameView
	lastJSON    []byte
	board       []boardSpace
	presenceSig string
	deviceSig   string
	deviceIDs   []string
	startedAt   int64

	quotes []tradeQuoteRecord // prices computers named this turn (quotes.go)

	// Computers' offers to people (asks.go): the room setting (normal,
	// fewer, off) and what they asked for this game.
	botOffers  string
	asksLedger askLedger

	// Computers wait until cardHoldUntil so everyone can read a drawn card and
	// see the dice, hops and payments land (showHoldAt, pace.go).
	cardHoldUntil time.Time

	computerSpeed string // slow | normal | fast ("" = normal), see endgame.go

	// The game has been waiting on away player stallKey since stallSince.
	stallKey   string
	stallSince time.Time
	// idleFrom is, for each connected person the game waits on, when the
	// wait began or they last sent a command: the stand-in takes over
	// idleStandInDelay after it (standin.go).
	idleFrom map[string]time.Time
	// skip holds the people whose turn the host skips (standin.go): the
	// server plays their safe moves until the game moves on from them. The
	// value is the host's input, recorded as the steps' "via".
	skip map[string]string

	claims     []*claim    // requests to move a seat or the table to a new device (claims.go)
	wrongCodes []time.Time // wrong approval codes typed for this game within wrongCodeWindow (claims.go)

	// Who made the room, for the per-client room quotas (createRoom).
	creatorIP     string
	creatorDevice string

	listed  bool                 // host opted in to showing the game to everyone in /api/games
	invited map[string]time.Time // devices invited to this game (they see it in /api/games)

	personSeenAt time.Time // last time a person (table screen or human player) was connected
	aiPaused     bool      // computers wait: nobody has been connected for botIdleGrace

	// Poker, Ludo, quiz and closest rooms: the last engine event sent to
	// clients (eventSeq), and the computer decision in progress (poker.go,
	// ludo.go; quiz and closest computers live in their engines). Ludo computers also wait until
	// ludoHoldUntil for the dice and the hopping pieces on the screens.
	eventSeq      int64
	pokerPlan     *pokerPlan
	ludoPlan      *ludoPlan
	ludoHoldUntil time.Time

	rec *liveRecord // the game record in progress (recorder.go); nil when not recording
	// trec is the record in progress of a poker, Ludo, quiz or closest game (tablerecord.go).
	trec *liveTable
	// restoring is set during the commit that brings a saved room back.
	restoring bool
}

type session struct {
	token       string
	role        string // "player", "spectator" or "table"
	playerID    string
	spectatorID string
	name        string
	isHost      bool
	lastSeen    time.Time
	conns       int    // open room WebSockets
	ip          string // client address last seen using this session

	// Party metadata carried across player/spectator switches.
	deviceID  string
	input     string
	tokenKind string
}

type playerMeta struct {
	input     string // phone, controller, keyboard, ai, classic
	tokenKind string
	deviceID  string
	seatID    string
	pad       *int

	difficulty string // AI seats: easy, normal, hard
	bot        Bot
	pokerBot   bots.Bot // poker AI seats (they see only their own View)
	ludoBot    ludo.Bot // Ludo AI seats

	autopilot  bool         // the computer plays for this away player (standin.go)
	idle       bool         // ...switched on because they sat idle while connected, or by the host (standin.go)
	away       *awayMark    // Estate: what clients need to list what happened while the stand-in played (standin.go)
	notSelling map[int]bool // deeds this person won't sell to computers (asks.go)
	outOrder   int          // 1 = first player to go bankrupt in this game, 0 = still in
}

type localSeat struct {
	SeatID   string `json:"seatId"`
	PlayerID string `json:"playerId"`
	Input    string `json:"input"`
	Pad      *int   `json:"pad"`
}

func (r *room) stopTimer() {
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if r.auctionTimer != nil {
		r.auctionTimer.Stop()
		r.auctionTimer = nil
	}
	r.timerSeq++
}

func (sess *session) online(now time.Time) bool {
	return sess.conns > 0 || now.Sub(sess.lastSeen) < onlineWindow
}

func (sess *session) seen(now time.Time) bool {
	return sess.conns > 0 || now.Sub(sess.lastSeen) < spectatorWindow
}

// ---- Lookup and authentication (src/rooms.js) ------------------------------------------

func normalizeCode(code string) string {
	return strings.ToUpper(jsTrim(code))
}

func (s *Server) requireRoom(code string) (*room, error) {
	r := s.rooms[normalizeCode(code)]
	if r == nil {
		return nil, badRequest(msgRoomMissing)
	}
	return r, nil
}

func (s *Server) authenticate(code, token string) (*room, *session, error) {
	r, err := s.requireRoom(code)
	if err != nil {
		return nil, nil, err
	}
	sess := r.sessions[token]
	if sess == nil {
		return nil, nil, badRequest(msgSessionInvalid)
	}
	return r, sess, nil
}

func (s *Server) authenticateHost(code, token string) (*room, *session, error) {
	r, sess, err := s.authenticate(code, token)
	if err != nil {
		return nil, nil, err
	}
	if !sess.isHost {
		return nil, nil, badRequest(msgOnlyHost)
	}
	return r, sess, nil
}

func (s *Server) createCode() (string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		var b strings.Builder
		for i := 0; i < 5; i++ {
			b.WriteByte(roomAlphabet[randomIndex(len(roomAlphabet))])
		}
		code := b.String()
		if s.rooms[code] == nil {
			return code, nil
		}
	}
	return "", badRequest(msgCodeExhausted)
}

// pruneRooms deletes rooms nobody has used for roomTTL (computer moves and
// spectators don't count as use; an open player or table screen does), empty
// rooms after emptyRoomTTL, and stale sessions. Requires s.mu.
func (s *Server) pruneRooms(now time.Time) {
	for code, r := range s.rooms {
		s.pruneSessions(r, now)
		if s.personConnected(r) {
			r.updatedAt = now
			s.markRoomsDirty() // keep the saved activity time fresh (crash, power cut)
			continue
		}
		ttl := roomTTL
		if r.info != nil && (len(r.info.Players) == 0 || r.info.AbandonableLobby()) {
			ttl = emptyRoomTTL
		}
		if now.Sub(r.updatedAt) < ttl {
			continue
		}
		s.deleteRoom(code, r, gamerecord.EndExpired)
	}
}

// personConnected reports an open WebSocket of the table screen or of a
// player (spectators alone don't keep a room alive).
func (s *Server) personConnected(r *room) bool {
	for c := range r.conns {
		if sess := r.sessions[c.token]; sess != nil && (sess.role == "table" || sess.role == "player") {
			return true
		}
	}
	return false
}

// personOnline reports whether the table screen or a human player (bankrupt
// ones still watch) is connected now. Computers only play while someone is.
func (s *Server) personOnline(r *room, now time.Time) bool {
	for _, sess := range r.order {
		if !sess.online(now) {
			continue
		}
		if sess.role == "table" {
			return true
		}
		if sess.role == "player" && r.info != nil {
			if p := r.info.player(sess.playerID); p != nil && p.Kind != "ai" {
				return true
			}
		}
	}
	return false
}

// notePeople records that a person is connected. Requires s.mu.
func (s *Server) notePeople(r *room, now time.Time) {
	if s.personOnline(r, now) {
		r.personSeenAt = now
	}
}

// pruneSessions drops spectator sessions unused for staleSession and, when a
// room is near its session cap, a player's older duplicate sessions (each
// player keeps their newest one, which may be their only way back in).
func (s *Server) pruneSessions(r *room, now time.Time) {
	newest := map[string]*session{}
	for _, sess := range r.order {
		if sess.role == "player" {
			if cur := newest[sess.playerID]; cur == nil || !sess.lastSeen.Before(cur.lastSeen) {
				newest[sess.playerID] = sess
			}
		}
	}
	crowded := len(r.sessions) >= maxRoomSessions*3/4
	for _, sess := range append([]*session(nil), r.order...) {
		if sess.conns > 0 || sess.isHost || now.Sub(sess.lastSeen) < staleSession {
			continue
		}
		switch {
		case sess.role == "spectator":
			s.endSession(r, sess.token, msgSessionInvalid)
		case sess.role == "player" && crowded && newest[sess.playerID] != sess:
			s.endSession(r, sess.token, msgSessionInvalid)
		}
	}
}

// deleteRoom closes a room; a game still in play is recorded as ending for
// reason (gamerecord.EndExpired or EndAbandoned).
func (s *Server) deleteRoom(code string, r *room, reason string) {
	s.closeRecord(r, reason, "", nil)
	r.stopTimer()
	delete(s.rooms, code)
	s.markRoomsDirty()
	for c := range r.conns {
		c.fail(msgRoomMissing)
	}
	if len(r.deviceIDs) > 0 {
		s.pushFriendsAbout(r.deviceIDs)
	}
}

func (s *Server) newRoom(code, gameType string, game Game, table bool) *room {
	if gameType == GameEstate {
		gameType = "" // Estate rooms save without a type, as before game types
	}
	r := &room{
		code:      code,
		gameType:  gameType,
		game:      game,
		sessions:  map[string]*session{},
		table:     table,
		meta:      map[string]*playerMeta{},
		updatedAt: s.now(),
		conns:     map[*wsConn]struct{}{},
	}
	r.personSeenAt = r.updatedAt
	return r
}

func (s *Server) createSession(r *room, sess *session) string {
	now := s.now()
	if len(r.sessions) >= maxRoomSessions {
		s.pruneSessions(r, now.Add(staleSession)) // make room: drop every unused spare
	}
	token := randomToken(24)
	sess.token = token
	sess.lastSeen = now
	if sess.ip == "" {
		sess.ip = s.reqIP
	}
	r.sessions[token] = sess
	r.order = append(r.order, sess)
	s.markRoomsDirty()
	return token
}

func (s *Server) deleteSession(r *room, token string) {
	s.endSession(r, token, msgSessionInvalid)
}

// endSession removes a session; its open sockets get msg and close.
func (s *Server) endSession(r *room, token, msg string) {
	sess := r.sessions[token]
	if sess == nil {
		return
	}
	delete(r.sessions, token)
	for i, candidate := range r.order {
		if candidate == sess {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	for c := range r.conns {
		if c.token == token {
			c.fail(msg)
		}
	}
	s.markRoomsDirty()
}

func (s *Server) spectatorCount(r *room, now time.Time) int {
	count := 0
	for _, sess := range r.order {
		if sess.role == "spectator" && sess.seen(now) {
			count++
		}
	}
	return count
}

func (s *Server) spectatorList(r *room, now time.Time) []Spectator {
	list := []Spectator{}
	for _, sess := range r.order {
		if sess.role == "spectator" && sess.seen(now) {
			list = append(list, Spectator{ID: sess.spectatorID, Name: sess.name, IsHost: sess.isHost})
		}
	}
	return list
}

// pickToken returns the requested token if it is free in this room,
// otherwise the first free one.
func (r *room) pickToken(requested string) string {
	requested = canonicalTokenKind(requested)
	used := map[string]bool{}
	for _, m := range r.meta {
		used[canonicalTokenKind(m.tokenKind)] = true
	}
	if validTokenKind(requested) && !used[requested] {
		return requested
	}
	for _, kind := range tokenKinds {
		if !used[kind] {
			return kind
		}
	}
	if validTokenKind(requested) {
		return requested
	}
	return tokenKinds[0]
}

func (r *room) seat(seatID string) *localSeat {
	for _, seat := range r.seats {
		if seat.SeatID == seatID {
			return seat
		}
	}
	return nil
}

// devicePlayer returns the player seat a device holds in this room.
func (r *room) devicePlayer(deviceID string) string {
	if deviceID == "" || r.info == nil {
		return ""
	}
	for _, p := range r.info.Players {
		if m := r.meta[p.ID]; m != nil && m.deviceID == deviceID {
			return p.ID
		}
	}
	return ""
}

func (r *room) hasHostPlayer() bool {
	for _, sess := range r.order {
		if sess.role != "table" && sess.isHost {
			return true
		}
	}
	return false
}

func (r *room) tableSession() *session {
	for _, sess := range r.order {
		if sess.role == "table" {
			return sess
		}
	}
	return nil
}

func (s *Server) playerOnline(r *room, playerID, kind string, now time.Time) bool {
	if kind == "ai" {
		return true
	}
	if m := r.meta[playerID]; m != nil && (m.input == "controller" || m.input == "keyboard") {
		table := r.tableSession()
		return table != nil && table.online(now)
	}
	for _, sess := range r.order {
		if sess.playerID == playerID && sess.role == "player" && sess.online(now) {
			return true
		}
	}
	return false
}

func (r *room) playerInput(playerID, kind string) string {
	if m := r.meta[playerID]; m != nil && m.input != "" {
		return m.input
	}
	if kind == "ai" {
		return "ai"
	}
	return "classic"
}

// commit refreshes the cached game view after a change, records party
// bookkeeping (friends, recent tables) and pushes events and states to every
// WebSocket viewer of the room.
func (s *Server) commit(r *room) {
	if r.isLudo() {
		s.commitLudo(r)
		return
	}
	if r.isPoker() {
		s.commitPoker(r)
		return
	}
	if r.isQuiz() {
		s.commitQuiz(r)
		return
	}
	if r.isClosest() {
		s.commitClosest(r)
		return
	}
	now := s.now()
	data, err := r.game.Snapshot()
	if err != nil {
		s.log.Printf("room %s: snapshot failed: %v", r.code, err)
		return
	}
	view, err := decodeGameView(data)
	if err != nil {
		s.log.Printf("room %s: decode failed: %v", r.code, err)
		return
	}
	prev, prevInfo := r.last, r.info
	r.last, r.lastJSON = view, data // presence is computed over the new player list
	r.info = estateInfo(view)
	s.refreshAutopilot(r, now)
	s.noteWaiting(r, view, now)
	dropFinishedSkips(r, view)
	recordBankruptcies(r, prev, view)
	s.noteTrades(r, prev, view)
	s.recordCommit(r, data, now)
	sig := s.presenceSignature(r, now)
	if sig != r.presenceSig {
		r.presenceSig = sig
		// Clients skip states whose version they already have, so publish
		// presence/seat changes under a new version. Rooms used only by
		// polling classic clients keep Node's exact version sequence.
		if prev != nil && view.Version == prev.Version && (r.table || len(r.conns) > 0) {
			r.game.Touch()
			if touched, err := r.game.Snapshot(); err == nil {
				if fresh, err := decodeGameView(touched); err == nil {
					view, data = fresh, touched
					r.last, r.lastJSON = view, data
					r.info = estateInfo(view)
				}
			}
		}
	}
	if r.board == nil {
		r.board = s.loadBoard(r)
	}
	var events []event
	if prev != nil {
		events = diffEvents(prev, view, r.board)
	}
	for _, e := range events {
		if e.kind() == "card" {
			text, _ := e.get("text").(string)
			hold := s.cardHoldAt(text, r.speedFactor())
			r.cardHoldUntil = maxTime(r.cardHoldUntil, now.Add(hold))
		}
	}
	// Let the screens show the roll, the hop and the money before the next computer move.
	if hold := s.showHoldAt(events, r.speedFactor()); hold > 0 {
		r.cardHoldUntil = maxTime(r.cardHoldUntil, now.Add(hold))
	}
	s.trackGameProgress(r, prevInfo, r.info, now)
	s.broadcast(r, events)
	s.checkDevicePresence(r, now)
	s.markRoomsDirty()
}

func (s *Server) loadBoard(r *room) []boardSpace {
	raw, err := r.game.PublicState("", Viewer{})
	if err != nil {
		return nil
	}
	var doc struct {
		Board []boardSpace `json:"board"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	return doc.Board
}

// presenceSignature captures every server-side input of publicState that the
// engine's version does not cover (online flags, spectators, seats, hosts).
func (s *Server) presenceSignature(r *room, now time.Time) string {
	var b strings.Builder
	if r.info != nil {
		for _, p := range r.info.Players {
			b.WriteString(p.ID)
			if s.playerOnline(r, p.ID, p.Kind, now) {
				b.WriteString("+")
			} else {
				b.WriteString("-")
			}
			if m := r.meta[p.ID]; m != nil && m.autopilot {
				b.WriteString("A")
			}
			if m := r.meta[p.ID]; m != nil && m.away != nil {
				b.WriteString(fmt.Sprintf("W%s%d", m.away.By, m.away.Until))
			}
			if r.skipping(p.ID) {
				b.WriteString("S")
			}
		}
	}
	b.WriteString("|")
	for _, sess := range r.order {
		if sess.role == "spectator" && sess.seen(now) {
			b.WriteString(sess.spectatorID + ":" + sess.name + ";")
		}
		if sess.isHost {
			b.WriteString("h" + sess.token[:6] + ";")
		}
	}
	b.WriteString("|")
	for _, seat := range r.seats {
		b.WriteString(seat.SeatID + ";")
	}
	if table := r.tableSession(); table != nil && table.online(now) {
		b.WriteString("|table")
	}
	b.WriteString("|speed:" + r.speedName())
	if r.listed {
		b.WriteString("|listed")
	}
	b.WriteString("|claims:" + r.claimsSignature())
	return b.String()
}

// touchPresence pushes a state if presence changed (connect, disconnect,
// polling resumed).
func (s *Server) touchPresence(r *room) {
	if s.presenceSignature(r, s.now()) != r.presenceSig {
		s.commit(r)
		s.presenceChanged(r)
	}
}

// presenceChanged re-checks who the game waits on after someone left or came
// back, arming or dropping the computer stand-in (standin.go). Computer moves
// in progress are left alone, but a computer that is due with nothing
// scheduled (its last step was lost) is started again.
func (s *Server) presenceChanged(r *room) {
	if r.info == nil || r.info.Status != "playing" {
		return
	}
	if r.aiPaused {
		if s.personOnline(r, s.now()) {
			s.log.Printf("room %s: someone is back; computers play again", r.code)
			s.scheduleAI(r)
		}
		return
	}
	if !r.isEstate() {
		// A computer decision in progress survives (poker.go); the quiz
		// learns who is online before it next judges a clue.
		s.scheduleAI(r)
		return
	}
	if id, _ := nextAIPlayer(r.last); id == "" || (r.timer == nil && r.thinking == 0) {
		s.scheduleAI(r)
	}
}

// ---- Compatibility operations (src/rooms.js) ------------------------------------------

type entryResult struct {
	Code        string          `json:"code"`
	Token       string          `json:"token,omitempty"`
	TableToken  string          `json:"tableToken,omitempty"`
	PlayerID    string          `json:"playerId,omitempty"`
	SpectatorID string          `json:"spectatorId,omitempty"`
	State       json.RawMessage `json:"state"`
}

func (s *Server) createRoom(body *jsBody, device *deviceRecord, quota roomQuota) (*entryResult, error) {
	if err := body.access("name"); err != nil {
		return nil, err
	}
	if err := s.checkRoomQuota(quota); err != nil {
		return nil, err
	}
	code, err := s.createCode()
	if err != nil {
		return nil, err
	}
	game := s.cfg.Engine.NewGame(code) // the classic API is Estate-only
	if g, ok := game.(turnOrderAuctioner); ok {
		g.UseTurnOrderAuctions() // the classic client bids in turn
	}
	name, err := cleanName(body.str("name"))
	if err != nil {
		return nil, err
	}
	playerID, err := game.AddPlayer(name, "human")
	if err != nil {
		return nil, err
	}
	r := s.newRoom(code, GameEstate, game, false)
	r.creatorIP, r.creatorDevice = quota.ip, quota.deviceID
	input, deviceID, requested := entryIdentity(body, device)
	tokenKind := r.pickToken(requested)
	r.meta[playerID] = &playerMeta{input: input, tokenKind: tokenKind, deviceID: deviceID}
	sess := &session{role: "player", playerID: playerID, name: name, isHost: true, deviceID: deviceID, input: input, tokenKind: tokenKind}
	token := s.createSession(r, sess)
	s.rooms[code] = r
	s.rememberProfile(device, name, tokenKind)
	s.commit(r)
	state, err := s.stateFor(r, sess)
	if err != nil {
		return nil, err
	}
	return &entryResult{Code: code, Token: token, PlayerID: playerID, State: state}, nil
}

// entryIdentity decides how a joining player is controlled: a registered
// device or a token choice means the phone app, otherwise the classic client.
func entryIdentity(body *jsBody, device *deviceRecord) (input, deviceID, tokenKind string) {
	requested, hasToken := body.exactString("tokenKind")
	if device != nil {
		if !hasToken {
			requested = device.TokenKind
		}
		return "phone", device.ID, requested
	}
	if hasToken {
		return "phone", "", requested
	}
	return "classic", "", ""
}

func (s *Server) joinRoom(code string, body *jsBody, device *deviceRecord) (*entryResult, error) {
	if err := body.access("name"); err != nil {
		return nil, err
	}
	r, err := s.requireRoom(code)
	if err != nil {
		return nil, err
	}
	if device != nil && r.devicePlayer(device.ID) != "" {
		// Scanning the code again returns the seat this phone already holds.
		return s.rejoinRoom(r, device)
	}
	// Poker lets the engine decide: with late join on and a free seat, a
	// newcomer is dealt in from the next hand.
	if !r.isPoker() && r.info.Status != "lobby" {
		return nil, badRequest(msgAlreadyStarted)
	}
	name, err := cleanName(body.str("name"))
	if err != nil {
		return nil, err
	}
	playerID, err := r.game.AddPlayer(name, "human")
	if err != nil {
		return nil, err
	}
	input, deviceID, requested := entryIdentity(body, device)
	tokenKind := r.pickToken(requested)
	r.meta[playerID] = &playerMeta{input: input, tokenKind: tokenKind, deviceID: deviceID}
	isHost := false
	if r.table && input == "phone" && !r.hasHostPlayer() {
		// The first phone player at a table is the host player.
		isHost = true
		r.game.SetHost(playerID)
	}
	sess := &session{role: "player", playerID: playerID, name: name, isHost: isHost, deviceID: deviceID, input: input, tokenKind: tokenKind}
	token := s.createSession(r, sess)
	r.updatedAt = s.now()
	s.rememberProfile(device, name, tokenKind)
	s.commit(r)
	if r.isPoker() {
		s.scheduleAI(r) // a late joiner can un-pause the table
	}
	state, err := s.stateFor(r, sess)
	if err != nil {
		return nil, err
	}
	return &entryResult{Code: r.code, Token: token, PlayerID: playerID, State: state}, nil
}

func (s *Server) watchRoom(code string, body *jsBody, device *deviceRecord) (*entryResult, error) {
	if err := body.access("name"); err != nil {
		return nil, err
	}
	r, err := s.requireRoom(code)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if s.spectatorCount(r, now) >= maxSpectators {
		return nil, badRequest(msgSpectatorLimit)
	}
	name, err := cleanName(body.str("name"))
	if err != nil {
		return nil, err
	}
	sess := &session{role: "spectator", spectatorID: uid("spectator"), name: name}
	if device != nil {
		sess.deviceID = device.ID
		sess.input = "phone"
		sess.tokenKind = device.TokenKind
	}
	token := s.createSession(r, sess)
	r.updatedAt = s.now()
	s.commit(r)
	state, err := s.stateFor(r, sess)
	if err != nil {
		return nil, err
	}
	return &entryResult{Code: r.code, Token: token, SpectatorID: sess.spectatorID, State: state}, nil
}

func (s *Server) addAI(code string, body *jsBody) (json.RawMessage, error) {
	if err := body.access("token"); err != nil {
		return nil, err
	}
	r, sess, err := s.authenticateHost(code, sessionToken(body))
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, p := range r.info.Players {
		used[p.Name] = true
	}
	name := freeAIName(used)
	difficulty := "normal"
	if raw := body.raw("difficulty"); len(raw) > 0 && string(raw) != "null" {
		difficulty, _ = body.exactString("difficulty")
		if levels := s.aiLevels(r); !validDifficulty(difficulty) || !contains(levels, difficulty) {
			return nil, badRequest(levelChoice(levels))
		}
	}
	var playerID string
	if q := quizOf(r); q != nil {
		playerID, err = q.g.AddComputer(name, difficulty) // the engine plays its own computers
	} else if c := closestOf(r); c != nil {
		playerID, err = c.g.AddComputer(name, difficulty) // so does Near Thing's
	} else {
		playerID, err = r.game.AddPlayer(name, "ai")
	}
	if err != nil {
		return nil, err
	}
	m := &playerMeta{input: "ai", tokenKind: r.pickToken(""), difficulty: difficulty}
	switch {
	case r.isQuiz(), r.isClosest(): // no bot: the engine has the level
	case r.isLudo():
		m.ludoBot = s.newLudoBot(difficulty)
	case r.isPoker():
		m.pokerBot = s.newPokerBot(difficulty)
	default:
		m.bot = s.newBot(difficulty)
	}
	r.meta[playerID] = m
	r.updatedAt = s.now()
	s.commit(r)
	if r.isPoker() {
		s.scheduleAI(r) // a computer joining a running game
	}
	return s.stateFor(r, sess)
}

func (s *Server) removePlayer(code string, body *jsBody) (json.RawMessage, error) {
	if err := body.access("token"); err != nil {
		return nil, err
	}
	r, sess, err := s.authenticateHost(code, sessionToken(body))
	if err != nil {
		return nil, err
	}
	targetID, _ := body.exactString("playerId")
	if r.isEstate() && r.table && r.info.Status == "playing" {
		return s.removeFromGame(r, sess, targetID) // hostcontrols.go; classic rooms keep the lobby-only remove
	}
	if err := r.game.RemovePlayer(r.info.HostID, targetID); err != nil {
		return nil, err
	}
	removedHost := false
	for _, candidate := range append([]*session(nil), r.order...) {
		if candidate.role == "player" && candidate.playerID == targetID {
			if candidate.isHost {
				removedHost = true
			}
			s.deleteSession(r, candidate.token)
		}
	}
	delete(r.meta, targetID)
	for i, seat := range r.seats {
		if seat.PlayerID == targetID {
			r.seats = append(r.seats[:i], r.seats[i+1:]...)
			break
		}
	}
	if removedHost && r.table {
		s.promoteHostPlayer(r)
	}
	r.updatedAt = s.now()
	s.commit(r)
	if !r.isEstate() {
		s.scheduleAI(r) // their hand was folded, the turn or the pick may have moved on
	}
	return s.stateFor(r, sess)
}

// promoteHostPlayer hands host rights to the earliest remaining phone player
// of a table room.
func (s *Server) promoteHostPlayer(r *room) {
	for _, candidate := range r.order {
		if candidate.role == "player" && candidate.input == "phone" {
			candidate.isHost = true
			r.game.SetHost(candidate.playerID)
			return
		}
	}
}

func (s *Server) roomState(code, token string) (json.RawMessage, error) {
	r, sess, err := s.authenticate(code, token)
	if err != nil {
		return nil, err
	}
	now := s.now()
	sess.lastSeen = now
	sess.ip = s.reqIP
	r.updatedAt = now
	s.touchPresence(r)
	return s.stateFor(r, sess)
}

// hostActions may be sent by a host that holds no player seat (a spectator
// host in Node; the table in party mode).
var hostActions = map[string]bool{"start": true, "set_dice_mode": true, "set_rules": true}

var nodeHostSpectatorActions = map[string]bool{"start": true, "set_dice_mode": true}

// hostAction reports whether kind is a host action of this room's game.
func (r *room) hostAction(kind string) bool {
	if r.isLudo() {
		return ludoHostActions[kind]
	}
	if r.isPoker() {
		return pokerHostActions[kind]
	}
	if r.isQuiz() {
		return quizHostActions[kind]
	}
	if r.isClosest() {
		return closestHostActions[kind]
	}
	return hostActions[kind]
}

// withoutPrivateFields drops an action's top-level fields whose names start
// with "_": such fields are the server's to add, so a client can never pass
// one off as the server's. Any other action is returned exactly as sent.
func withoutPrivateFields(action json.RawMessage) json.RawMessage {
	if len(action) == 0 || action[0] != '{' {
		return action
	}
	fields, err := splitObject(action)
	if err != nil {
		return action
	}
	kept := make([]objectField, 0, len(fields))
	for _, f := range fields {
		if !strings.HasPrefix(f.key, "_") {
			kept = append(kept, f)
		}
	}
	if len(kept) == len(fields) {
		return action
	}
	return joinObject(kept)
}

func actionType(raw json.RawMessage) string {
	var probe struct {
		Type json.RawMessage `json:"type"`
	}
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &probe) != nil {
		return ""
	}
	var t string
	if json.Unmarshal(probe.Type, &t) != nil {
		return ""
	}
	return t
}

// actorFor resolves which engine player a session acts as.
func (s *Server) actorFor(r *room, sess *session, body *jsBody, kind string, allowHost bool) (string, error) {
	switch sess.role {
	case "player":
		return sess.playerID, nil
	case "table":
		if seatID, _ := body.exactString("seatId"); seatID != "" {
			seat := r.seat(seatID)
			if seat == nil {
				return "", badRequest(msgSeatMissing)
			}
			return seat.PlayerID, nil
		}
		if allowHost && r.hostAction(kind) {
			if !r.isEstate() && r.info.HostID == "" {
				// Nobody (no person) is seated yet, so nobody holds the
				// game's HostID: the table itself is the host (0.10.2), so
				// it can set the rules and start a table of computers only.
				return r.tableHost(), nil
			}
			return r.info.HostID, nil
		}
		return "", badRequest(msgTableNoSeat)
	default:
		if allowHost && sess.isHost && ((r.isEstate() && nodeHostSpectatorActions[kind]) || r.hostAction(kind)) {
			return r.info.HostID, nil
		}
		return "", badRequest(msgSpectatorsCannot)
	}
}

// tableHost is the requester the table screen acts as in a poker, Ludo,
// quiz or closest room whose game has no host yet (no person seated).
func (r *room) tableHost() string {
	switch {
	case r.isLudo():
		return ludo.TableHost
	case r.isQuiz():
		return quiz.TableHost
	case r.isClosest():
		return closest.TableHost
	}
	return holdem.TableHost
}

// roomAction is POST /api/rooms/{code}/action, and an action sent over a
// room socket (socketAction).
func (s *Server) roomAction(code string, body *jsBody) (json.RawMessage, error) {
	if err := body.access("token"); err != nil {
		return nil, err
	}
	r, sess, err := s.authenticate(code, sessionToken(body))
	if err != nil {
		return nil, err
	}
	sess.ip = s.reqIP
	action := withoutPrivateFields(body.raw("action"))
	actor, err := s.actorFor(r, sess, body, actionType(action), true)
	if err != nil {
		return nil, err
	}
	if len(action) == 0 {
		action = json.RawMessage("null")
	}
	if r.isEstate() {
		switch actionType(action) {
		case engine.ActionCloseAuction, engine.ActionRemovePlayer:
			// The server's clock and the host's Remove (POST .../remove)
			// send these, never a client.
			return nil, badRequest(msgServerOnly)
		case actionBack:
			// I'M BACK: hands the seat back from the stand-in (or dismisses
			// the while-away list) and does nothing else.
			if s.noteCommand(r, sess, body, actor) {
				s.touchPresence(r)
				s.scheduleAI(r)
			}
			return s.stateFor(r, sess)
		}
	}
	handedBack := s.noteCommand(r, sess, body, actor)
	if q := quizOf(r); q != nil {
		s.feedQuizLatency(r, q, s.now()) // a tap is judged by its player's latency now
	}
	if c := closestOf(r); c != nil {
		s.feedClosestOnline(r, c, s.now())
	}
	src := stepSource{source: gamerecord.SourcePlayer, via: r.actionVia(sess, actor, body)}
	if err := s.applyAction(r, actor, action, src); err != nil {
		// A JS throw keeps earlier mutations; publish them if any (a poker
		// table or a quiz catches up overdue timers before it checks an
		// action).
		s.commitIfChanged(r)
		if !r.isEstate() {
			s.scheduleAI(r)
		} else if handedBack {
			s.touchPresence(r) // screens drop the stand-in badge
			s.scheduleAI(r)    // and the game waits for them again
		}
		return nil, err
	}
	r.updatedAt = s.now()
	s.commit(r)
	s.scheduleAI(r)
	return s.stateFor(r, sess)
}

func (s *Server) commitIfChanged(r *room) {
	data, err := r.game.Snapshot()
	if err == nil {
		s.settleRecord(r, data) // a refused action that changed nothing is not recorded
	}
	if err == nil && string(data) != string(r.lastJSON) {
		s.commit(r)
	}
}

func (s *Server) tradeQuote(code string, body *jsBody, rawBody []byte) (json.RawMessage, error) {
	if err := body.access("token"); err != nil {
		return nil, err
	}
	r, sess, err := s.authenticate(code, sessionToken(body))
	if err != nil {
		return nil, err
	}
	if !r.isEstate() {
		return nil, badRequest(msgPokerNoQuotes)
	}
	actor, err := s.actorFor(r, sess, body, "", false)
	if err != nil {
		return nil, err
	}
	if s.noteCommand(r, sess, body, actor) {
		s.touchPresence(r)
		s.scheduleAI(r)
	}
	if len(rawBody) == 0 {
		rawBody = []byte("{}")
	}
	// A computer that prices deals itself (Grandmaster) answers on its own
	// (quotes.go botQuote); the others get the Normal heuristic's quote.
	if quote, handled, err := s.botQuote(r, actor, rawBody); handled {
		return quote, err
	}
	quote, err := s.ai.QuoteTrade(r.game, r.lastJSON, actor, rawBody)
	if err == nil {
		r.rememberQuote(actor, rawBody, quote)
	}
	return quote, err
}

func (s *Server) setRole(code string, body *jsBody) (json.RawMessage, error) {
	if err := body.access("token"); err != nil {
		return nil, err
	}
	r, sess, err := s.authenticate(code, sessionToken(body))
	if err != nil {
		return nil, err
	}
	if r.info.Status != "lobby" {
		return nil, badRequest(msgLobbyOnlyRole)
	}
	role, _ := body.exactString("role")
	if role != "player" && role != "spectator" {
		return nil, badRequest(msgInvalidRole)
	}
	if sess.role == role {
		return s.stateFor(r, sess)
	}
	if sess.role == "table" {
		return nil, badRequest(msgTableRole)
	}
	now := s.now()
	if role == "spectator" {
		if s.spectatorCount(r, now) >= maxSpectators {
			return nil, badRequest(msgSpectatorLimit)
		}
		spectatorID := sess.spectatorID
		if spectatorID == "" {
			spectatorID = uid("spectator")
		}
		if err := r.game.SwitchPlayerToSpectator(sess.playerID, spectatorID); err != nil {
			return nil, err
		}
		delete(r.meta, sess.playerID)
		sess.role = "spectator"
		sess.spectatorID = spectatorID
		sess.playerID = ""
	} else {
		playerID, err := r.game.SwitchSpectatorToPlayer(sess.name, sess.isHost)
		if err != nil {
			return nil, err
		}
		input := sess.input
		if input == "" {
			input = "classic"
		}
		sess.tokenKind = r.pickToken(sess.tokenKind)
		r.meta[playerID] = &playerMeta{input: input, tokenKind: sess.tokenKind, deviceID: sess.deviceID}
		sess.role = "player"
		sess.playerID = playerID
		sess.spectatorID = ""
	}
	sess.lastSeen = now
	r.updatedAt = now
	s.commit(r)
	return s.stateFor(r, sess)
}

// ---- Party mode: tables, seats, rejoin, summary ------------------------------------

func (s *Server) createTable(body *jsBody, device *deviceRecord, quota roomQuota) (*entryResult, error) {
	gameType, err := s.requestedGameType(body)
	if err != nil {
		return nil, err
	}
	engine, err := s.engineFor(gameType)
	if err != nil {
		return nil, err
	}
	s.closeReplacedTable(body)
	if err := s.checkRoomQuota(quota); err != nil {
		return nil, err
	}
	code, err := s.createCode()
	if err != nil {
		return nil, err
	}
	r := s.newRoom(code, gameType, engine.NewGame(code), true)
	r.creatorIP, r.creatorDevice = quota.ip, quota.deviceID
	sess := &session{role: "table", name: "Table", isHost: true}
	if device != nil {
		sess.deviceID = device.ID
	}
	token := s.createSession(r, sess)
	s.rooms[code] = r
	s.commit(r)
	state, err := s.stateFor(r, sess)
	if err != nil {
		return nil, err
	}
	return &entryResult{Code: code, TableToken: token, State: state}, nil
}

// closeReplacedTable closes the table a screen is leaving for a new one
// (replaceCode and replaceToken in POST /api/tables), if it is an empty lobby
// that nobody else is watching. Switching games in the lobby makes a new
// table each time, and these used to sit around until they were pruned.
func (s *Server) closeReplacedTable(body *jsBody) {
	code, token := normalizeCode(body.str("replaceCode")), body.str("replaceToken")
	if code == "" || token == "" {
		return
	}
	r := s.rooms[code]
	if r == nil {
		return
	}
	sess := r.sessions[token]
	if sess == nil || sess.role != "table" || !r.info.AbandonableLobby() {
		return
	}
	now := s.now()
	for _, other := range r.order {
		if other != sess && other.online(now) {
			return
		}
	}
	s.deleteRoom(code, r, gamerecord.EndAbandoned)
}

func (s *Server) addSeat(code string, body *jsBody) (map[string]any, error) {
	if err := body.access("tableToken"); err != nil {
		return nil, err
	}
	r, err := s.requireRoom(code)
	if err != nil {
		return nil, err
	}
	sess := r.sessions[body.str("tableToken")]
	if sess == nil {
		return nil, badRequest(msgSessionInvalid)
	}
	if sess.role != "table" {
		return nil, forbidden(msgTableOnly)
	}
	if r.isPoker() {
		// Hole cards must stay private: no controller or keyboard seats.
		return nil, badRequest(msgPokerPhones)
	}
	if r.isQuiz() {
		// So must final bets and answers; and everyone answers at once.
		return nil, badRequest(msgQuizPhones)
	}
	if r.isClosest() {
		// So must guesses and bets.
		return nil, badRequest(msgClosestPhones)
	}
	if r.info.Status != "lobby" {
		return nil, badRequest(msgSeatLobby)
	}
	input, _ := body.exactString("input")
	if input != "controller" && input != "keyboard" {
		return nil, badRequest(msgSeatInput)
	}
	name, err := cleanName(body.str("name"))
	if err != nil {
		return nil, err
	}
	var pad *int
	if input == "controller" {
		if raw := body.raw("pad"); len(raw) > 0 && string(raw) != "null" {
			var n float64
			if json.Unmarshal(raw, &n) != nil || n != float64(int(n)) || n < 0 || n > 15 {
				return nil, badRequest("Invalid controller number")
			}
			value := int(n)
			pad = &value
			for _, seat := range r.seats {
				if seat.Pad != nil && *seat.Pad == value {
					return nil, badRequest(msgPadTaken)
				}
			}
		}
	}
	playerID, err := r.game.AddPlayer(name, "human")
	if err != nil {
		return nil, err
	}
	requested, _ := body.exactString("tokenKind")
	seat := &localSeat{SeatID: uid("seat"), PlayerID: playerID, Input: input, Pad: pad}
	r.seats = append(r.seats, seat)
	r.meta[playerID] = &playerMeta{input: input, tokenKind: r.pickToken(requested), seatID: seat.SeatID, pad: pad}
	if r.hasHostPlayer() {
		// Keep the phone host player as the engine host.
		for _, candidate := range r.order {
			if candidate.role == "player" && candidate.isHost {
				r.game.SetHost(candidate.playerID)
				break
			}
		}
	}
	r.updatedAt = s.now()
	s.commit(r)
	state, err := s.stateFor(r, sess)
	if err != nil {
		return nil, err
	}
	return map[string]any{"seatId": seat.SeatID, "state": state}, nil
}

func (s *Server) rejoinRoom(r *room, device *deviceRecord) (*entryResult, error) {
	playerID := r.devicePlayer(device.ID)
	if playerID == "" {
		return nil, notFound(msgNoSeat)
	}
	player := r.info.player(playerID)
	isHost := false
	for _, candidate := range r.order {
		if candidate.playerID == playerID && candidate.isHost {
			isHost = true
		}
	}
	m := r.meta[playerID]
	now := s.now()
	// This browser is back: its earlier sessions that are no longer in use
	// (closed tabs, reloads) go, so repeated rejoins don't pile up sessions.
	for _, old := range append([]*session(nil), r.order...) {
		if old.role == "player" && old.playerID == playerID && old.deviceID == device.ID && old.conns == 0 && !old.online(now) {
			s.endSession(r, old.token, msgSessionInvalid)
		}
	}
	sess := &session{role: "player", playerID: playerID, name: player.Name, isHost: isHost, deviceID: device.ID, input: m.input, tokenKind: m.tokenKind}
	token := s.createSession(r, sess)
	r.updatedAt = now
	s.commit(r)
	state, err := s.stateFor(r, sess)
	if err != nil {
		return nil, err
	}
	return &entryResult{Code: r.code, Token: token, PlayerID: playerID, State: state}, nil
}

type summaryPlayer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TokenKind   string `json:"tokenKind"`
	Color       string `json:"color"`
	Input       string `json:"input"`
	Online      bool   `json:"online"`
	Reclaimable bool   `json:"reclaimable"`
}

type summaryHost struct {
	Name      string  `json:"name"`
	TokenKind *string `json:"tokenKind"`
	Color     *string `json:"color"`
}

func (s *Server) roomSummary(code string) (map[string]any, error) {
	r := s.rooms[normalizeCode(code)]
	if r == nil {
		return nil, notFound(msgRoomMissing)
	}
	view := r.info
	now := s.now()
	players := []summaryPlayer{}
	var host *summaryHost
	for i := range view.Players {
		p := &view.Players[i]
		tokenKind := ""
		if m := r.meta[p.ID]; m != nil {
			tokenKind = m.tokenKind
		}
		color := p.Color
		if r.table && tokenKind != "" {
			if c := s.tokenColor(tokenKind); c != "" {
				color = c
			}
		}
		players = append(players, summaryPlayer{
			ID: p.ID, Name: p.Name, TokenKind: tokenKind, Color: color, Input: r.playerInput(p.ID, p.Kind),
			Online: s.playerOnline(r, p.ID, p.Kind, now), Reclaimable: s.reclaimable(r, p, now),
		})
		if p.ID == view.HostID {
			tk := tokenKind
			host = &summaryHost{Name: p.Name, TokenKind: &tk, Color: &color}
		}
	}
	if host == nil && view.HostID != "" {
		for _, sess := range r.order {
			if sess.role == "spectator" && sess.spectatorID == view.HostID {
				host = &summaryHost{Name: sess.name}
			}
		}
	}
	rules := view.Settings
	if len(rules) == 0 {
		rules = json.RawMessage("{}")
	}
	// turn is Estate's turn or poker's hand number; poker's openSeats count
	// while playing too when late join is on, and rules are its settings.
	return map[string]any{
		"code":        r.code,
		"gameType":    r.gameTypeName(),
		"status":      view.Status,
		"phase":       view.Phase,
		"turn":        view.Turn,
		"host":        host,
		"players":     players,
		"openSeats":   view.OpenSeats,
		"rules":       rules,
		"tableOnline": r.table && s.tableOnline(r, now),
	}, nil
}

// ---- AI scheduling (scheduleAi in src/rooms.js) ---------------------------------------

func (s *Server) scheduleAI(r *room) {
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.timerSeq++
	seq := r.timerSeq
	if s.closed || r.info == nil {
		return
	}
	if r.info.Status == "playing" {
		now := s.now()
		s.notePeople(r, now)
		if now.Sub(r.personSeenAt) >= botIdleGrace {
			// Nobody is connected: computers wait instead of playing an
			// empty room forever (presenceChanged resumes them). A poker
			// table's timers wait too and catch up when someone is back.
			if !r.aiPaused {
				s.log.Printf("room %s: nobody is connected; computers wait", r.code)
			}
			r.aiPaused = true
			return
		}
	}
	r.aiPaused = false
	if r.isLudo() {
		s.scheduleLudo(r, seq)
		return
	}
	if r.isPoker() {
		s.schedulePoker(r, seq)
		return
	}
	if r.isQuiz() {
		s.scheduleQuiz(r, seq)
		return
	}
	if r.isClosest() {
		s.scheduleClosest(r, seq)
		return
	}
	view := r.last
	if view == nil {
		return
	}
	s.armAuctionClose(r, view)
	if auctionClockOut(r, view) {
		return // the close timer (due now) ends it; bids would be refused
	}
	if view.Phase == "order_countdown" {
		var delay time.Duration
		if view.OpeningOrder != nil && view.OpeningOrder.CountdownEndsAt != nil {
			delay = time.Duration(*view.OpeningOrder.CountdownEndsAt-r.game.Now()) * time.Millisecond
		}
		if delay < 0 {
			delay = 0
		}
		r.timer = time.AfterFunc(delay, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			defer s.recoverAI(r)
			if s.closed || r.timerSeq != seq || s.rooms[r.code] != r {
				return
			}
			r.timer = nil
			if s.completeOrder(r) {
				r.updatedAt = s.now()
				s.commit(r)
			}
			s.scheduleAI(r)
		})
		return
	}
	playerID, orderRoll := nextAIPlayer(view)
	standIn := false
	if playerID == "" {
		playerID, orderRoll = s.standInActor(r, view, seq)
		if playerID == "" {
			return
		}
		standIn = true
	}
	if key := aiStepKey(view, playerID); key != r.aiStepKey {
		r.aiStepKey, r.aiStepFails = key, 0
	}
	var action json.RawMessage
	var think time.Duration
	switch {
	case orderRoll:
		action = json.RawMessage(`{"type":"roll_for_order"}`)
	case !standIn && r.quotedTrade(view, playerID):
		action = json.RawMessage(`{"type":"accept_trade"}`) // honour the quoted price
	case standIn && r.skipping(playerID):
		// The host skips this person's turn: the stand-in's safe moves.
		s.playSafeMove(r, seq, view, playerID)
		return
	case r.aiStepFails >= maxAIStepFailures:
		// This decision gave no move every time it was asked for: asking
		// again would stall the table for good, so play the safe move.
		s.playSafeMove(r, seq, view, playerID)
		return
	case standIn:
		chosen, err := s.chooseSafely(r, playerID, func() (json.RawMessage, error) {
			return standInAction(view, r.lastJSON, playerID)
		})
		if err == nil && (len(chosen) == 0 || string(chosen) == "null") {
			err = errNoMove
		}
		if err != nil {
			s.aiStepFailed(r, seq, view, playerID, err)
			return
		}
		action = s.keepSolvent(r, view, playerID, withChosenDice(view, playerID, chosen))
	default:
		var bot Bot = s.ai
		if m := r.meta[playerID]; m != nil && m.bot != nil {
			bot = m.bot
		}
		if slow, ok := bot.(thinkingBot); ok {
			if g, ok := gameCopy(r.game); ok {
				s.thinkAsync(r, seq, view, playerID, slow, g, r.lastJSON)
				return
			}
		}
		started := time.Now()
		chosen, err := s.chooseSafely(r, playerID, func() (json.RawMessage, error) {
			chosen, err := bot.ChooseAction(r.game, r.lastJSON, playerID)
			if eg, ok := r.game.(*engineGame); ok && err == nil {
				chosen = gateAction(eg.g, r.lastJSON, playerID, chosen, s.offerGate(r, playerID))
			}
			return chosen, err
		})
		think = time.Since(started)
		if err == nil && (len(chosen) == 0 || string(chosen) == "null") {
			err = errNoMove
		}
		if err != nil {
			s.aiStepFailed(r, seq, view, playerID, err)
			return
		}
		action = chosen
	}
	s.queueAIAction(r, seq, view, playerID, action, s.aiDelay, think, false)
}

// maxAIStepFailures is how many times in a row one computer decision may
// give no move (a crash, an error) before the safe move is played instead.
const maxAIStepFailures = 2

// errNoMove is a computer decision that chose nothing although the game
// waits on it.
var errNoMove = errors.New("it chose no move")

// aiStepKey names the decision a computer (or the stand-in) is making: who,
// in which phase, after which log entry. Unlike the version it survives the
// bumps presence changes cause (Touch), so repeated failures of one
// decision are counted.
func aiStepKey(view *gameView, playerID string) string {
	last := ""
	if n := len(view.Log); n > 0 {
		last = view.Log[n-1].ID
	}
	return playerID + "|" + view.Phase + "|" + last
}

// aiStepFailed handles a computer (or stand-in) decision that gave no move:
// it crashed, failed or chose nothing. Nothing else would wake the table
// (presence changes leave computer moves alone), so it is tried again after
// a pause, and once the same decision has failed maxAIStepFailures times in
// a row the safe move is played instead.
func (s *Server) aiStepFailed(r *room, seq uint64, view *gameView, playerID string, why error) {
	r.aiStepFails++
	s.log.Printf("room %s: no move from the computer for %s (%d in a row): %v", r.code, playerID, r.aiStepFails, why)
	if r.aiStepFails >= maxAIStepFailures {
		s.playSafeMove(r, seq, view, playerID)
		return
	}
	s.retryAILater(r, seq)
}

// playSafeMove queues the safe move for playerID (safeMove), or tries again
// later when even that has been refused.
func (s *Server) playSafeMove(r *room, seq uint64, view *gameView, playerID string) {
	action := s.safeMove(r, view, playerID)
	if action == nil {
		s.log.Printf("room %s: no safe move for %s; trying again in %v", r.code, playerID, s.aiRetryWait())
		s.retryAILater(r, seq)
		return
	}
	s.queueAIAction(r, seq, view, playerID, action, s.aiDelay, 0, true)
}

// safeMove keeps the game going when a computer's (or the stand-in's) own
// choice can't be used. In debt it raises cash while anything can be sold or
// mortgaged (raiseCashAction), so nobody goes bankrupt who could pay;
// otherwise it is aiFallback. nil if the engine already refused it.
func (s *Server) safeMove(r *room, view *gameView, playerID string) json.RawMessage {
	refused := func(action json.RawMessage) bool {
		return r.aiFailedVersion == view.Version && r.aiFailed[playerID+"|"+string(action)]
	}
	if raise := raiseCashFor(r, view, playerID, refused); raise != nil {
		return raise
	}
	fallback := withChosenDice(view, playerID, aiFallback(view, playerID))
	if fallback == nil || refused(fallback) {
		return nil
	}
	return fallback
}

// keepSolvent stops the stand-in from declaring an away person bankrupt
// while they could still pay. With the loans house rule the heuristic asks
// for a loan first, which the stand-in never does for them, and the move it
// falls back to is bankruptcy: selling and mortgaging come first.
func (s *Server) keepSolvent(r *room, view *gameView, playerID string, action json.RawMessage) json.RawMessage {
	if actionType(action) != engine.ActionBankrupt {
		return action
	}
	if raise := raiseCashFor(r, view, playerID, nil); raise != nil {
		return raise
	}
	return action
}

// raiseCashFor is raiseCashAction for playerID when the game waits on them
// to pay a debt they can't cover from cash; nil otherwise.
func raiseCashFor(r *room, view *gameView, playerID string, refused func(json.RawMessage) bool) json.RawMessage {
	if view.Phase != engine.PhaseDebt || view.Debt == nil || view.Debt.PayerID != playerID {
		return nil
	}
	if me := view.player(playerID); me == nil || me.Bankrupt || me.Cash >= view.Debt.Amount {
		return nil
	}
	return raiseCashAction(r.game, playerID, refused)
}

// raiseCashAction is a legal move that raises cash for playerID in debt:
// sell a building (the tallest first, as the engine sells a colour group
// evenly), else mortgage a deed (one free of buildings and not pledged for
// a loan). Each candidate is tried on a copy of the game, so the engine's
// own rules decide; refused (may be nil) skips moves already refused. nil
// when nothing can be sold or mortgaged: only then may the stand-in, or a
// computer whose own move was refused, go bankrupt.
func raiseCashAction(game Game, playerID string, refused func(json.RawMessage) bool) json.RawMessage {
	eg, ok := game.(*engineGame)
	if !ok || eg.g == nil {
		return nil
	}
	g := eg.g
	var sells, mortgages []int
	for _, index := range engine.OwnableIndexes {
		st := g.Properties[index]
		switch {
		case st == nil || st.OwnerID != playerID:
		case st.Houses > 0:
			sells = append(sells, index)
		case !st.Mortgaged:
			mortgages = append(mortgages, index)
		}
	}
	sort.SliceStable(sells, func(i, j int) bool { return g.Properties[sells[i]].Houses > g.Properties[sells[j]].Houses })
	legal := func(kind string, index int) (action json.RawMessage) {
		action = json.RawMessage(fmt.Sprintf(`{"type":%q,"spaceIndex":%d}`, kind, index))
		if refused != nil && refused(action) {
			return nil
		}
		defer func() {
			if recover() != nil {
				action = nil // an engine bug on the copy: not a move to make
			}
		}()
		trial := &engineGame{g: hardai.Clone(g)}
		if trial.PerformAction(playerID, action) != nil {
			return nil
		}
		return action
	}
	for _, index := range sells {
		if action := legal(engine.ActionSellBuilding, index); action != nil {
			return action
		}
	}
	for _, index := range mortgages {
		if action := legal(engine.ActionMortgage, index); action != nil {
			return action
		}
	}
	return nil
}

// thinkAsync lets a bot that may think for a while (Hard: about 250 ms of
// Monte Carlo rollouts) decide on a private copy of the game without holding
// the server lock, so phones and the table stay responsive. The move is played
// only if the game is still where the bot saw it; otherwise the decision is
// dropped (whoever changed the game has scheduled a new one).
func (s *Server) thinkAsync(r *room, seq uint64, view *gameView, playerID string, bot thinkingBot, g *engine.Game, gameJSON []byte) {
	started := time.Now()
	gate := s.offerGate(r, playerID)
	r.thinking++
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		select {
		case s.thinkSlots <- struct{}{}:
		case <-s.ctx.Done():
			s.mu.Lock()
			r.thinking--
			s.mu.Unlock()
			return
		}
		var think time.Duration
		action, gen, err := func() (action json.RawMessage, gen uint64, err error) {
			defer func() { <-s.thinkSlots }()
			began := time.Now()
			defer func() { think = time.Since(began) }()
			defer func() {
				if recovered := recover(); recovered != nil {
					s.log.Printf("room %s: computer %s crashed while thinking: %v\n%s", r.code, playerID, recovered, debug.Stack())
					action, err = nil, fmt.Errorf("computer crashed: %v", recovered)
				}
			}()
			action, gen, err = bot.decide(g, gameJSON, playerID, gate)
			if err == nil {
				action = gateAction(g, gameJSON, playerID, action, gate)
			}
			return action, gen, err
		}()

		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.recoverAI(r)
		r.thinking--
		if s.closed || s.rooms[r.code] != r {
			return
		}
		if r.timerSeq != seq || r.last == nil || r.last.Version != view.Version {
			bot.discard(gen)
			if r.timerSeq == seq && r.timer == nil {
				s.scheduleAI(r) // the game changed without a new schedule
			}
			return
		}
		if err == nil && (len(action) == 0 || string(action) == "null") {
			err = errNoMove
		}
		if err != nil {
			s.aiStepFailed(r, seq, view, playerID, err)
			return
		}
		s.queueAIAction(r, seq, view, playerID, action, max(0, s.aiDelay-time.Since(started)), think, false)
	}()
}

// queueAIAction plays a computer's chosen action after delay (the pause that
// lets people follow what the computer does). think is how long the
// computer took to choose it (for the game record); fellBack marks the safe
// move played instead of the computer's own choice.
func (s *Server) queueAIAction(r *room, seq uint64, view *gameView, playerID string, action json.RawMessage, delay, think time.Duration, fellBack bool) {
	if r.aiFailedVersion != view.Version {
		r.aiFailed = nil
	}
	if r.aiFailed[playerID+"|"+string(action)] {
		// The engine rejected this exact choice already (e.g. a trade or
		// build blocked by loan collateral, or a mortgage of a deed pledged
		// for a loan): take the safe way out instead.
		fallback := s.safeMove(r, view, playerID)
		if fallback == nil {
			s.aiStepFailed(r, seq, view, playerID, errors.New("the engine refused every move it tried"))
			return
		}
		action, fellBack = fallback, true
	}
	delay = time.Duration(float64(delay) * r.speedFactor())
	if wait := r.cardHoldUntil.Sub(s.now()); wait > delay {
		delay = wait // let everyone read the card first
	}
	if timedAuction(view) {
		// Bid well before the clock runs out, however slow the speed.
		left := time.Duration(view.Auction.EndsAt-r.game.Now()) * time.Millisecond
		delay = max(0, min(delay, left/2))
	}
	r.timer = time.AfterFunc(delay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.recoverAI(r)
		if s.closed || r.timerSeq != seq || s.rooms[r.code] != r {
			return
		}
		r.timer = nil
		if auctionClockOut(r, r.last) {
			s.closeTimedAuction(r) // too late to bid
			return
		}
		src := stepSource{source: gamerecord.SourceAI, fallback: fellBack, think: think}
		if p := r.last.player(playerID); p != nil && p.Kind != "ai" {
			switch m := r.meta[playerID]; {
			case r.skipping(playerID):
				src.source, src.via = gamerecord.SourceHost, r.skip[playerID]
			case m == nil || !m.autopilot:
				s.scheduleAI(r) // they came back before the stand-in moved
				return
			default:
				src.source = gamerecord.SourceStandIn
			}
		}
		if err := s.applyAction(r, playerID, action, src); err != nil {
			r.aiFailures++
			s.log.Printf("room %s: AI action %s failed: %v", r.code, action, err)
			if r.aiFailed == nil || r.aiFailedVersion != r.last.Version {
				r.aiFailed = map[string]bool{}
				r.aiFailedVersion = r.last.Version
			}
			r.aiFailed[playerID+"|"+string(action)] = true
			s.commitIfChanged(r)
			if r.aiFailures >= maxAIFailures {
				s.log.Printf("room %s: computers paused after %d rejected moves; trying again in %v", r.code, r.aiFailures, s.aiRetryWait())
				s.retryAILater(r, seq)
				return
			}
		} else {
			// Computer moves don't count as use of the room (pruneRooms).
			r.aiFailures, r.aiStepFails = 0, 0
			s.commit(r)
		}
		s.scheduleAI(r)
	})
}

// aiRetryWait is aiRetryDelay scaled like every other computer pause (by
// the move delay), so tests with a tiny delay retry quickly too.
func (s *Server) aiRetryWait() time.Duration {
	return time.Duration(float64(aiRetryDelay) * float64(s.aiDelay) / float64(defaultAIDelay))
}

// retryAILater gives computers another go after a run of rejected moves or
// a decision that gave no move (instead of stopping for good): the game may
// have moved on by then.
func (s *Server) retryAILater(r *room, seq uint64) {
	r.timer = time.AfterFunc(s.aiRetryWait(), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.recoverAI(r)
		if s.closed || r.timerSeq != seq || s.rooms[r.code] != r {
			return
		}
		r.timer = nil
		r.aiFailures = 0
		r.aiFailed = nil
		s.scheduleAI(r)
	})
}

// recoverAI keeps a crash in computer play (a bug in a bot or the engine)
// from taking the whole server down: it is logged, and computers try again
// after aiRetryWait. It counts as a failed step, so a decision that keeps
// crashing gets the safe move (aiStepFailed). Deferred inside timer
// callbacks that hold s.mu.
func (s *Server) recoverAI(r *room) {
	recovered := recover()
	if recovered == nil {
		return
	}
	s.log.Printf("room %s: computer play crashed: %v\n%s", r.code, recovered, debug.Stack())
	if s.closed || s.rooms[r.code] != r {
		return
	}
	r.aiStepFails++
	r.stopTimer()
	s.retryAILater(r, r.timerSeq)
}

// chooseSafely runs a computer's decision (fn), turning a panic into an
// error.
func (s *Server) chooseSafely(r *room, playerID string, fn func() (json.RawMessage, error)) (action json.RawMessage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Printf("room %s: computer %s crashed: %v\n%s", r.code, playerID, recovered, debug.Stack())
			action, err = nil, fmt.Errorf("computer crashed: %v", recovered)
		}
	}()
	return fn()
}

// aiFallback is the safe action for an AI whose chosen action the engine
// rejected: decline what is offered, otherwise move the turn along.
func aiFallback(g *gameView, playerID string) json.RawMessage {
	me := g.player(playerID)
	if me == nil || me.Bankrupt {
		return nil
	}
	current := ""
	if g.CurrentIndex >= 0 && g.CurrentIndex < len(g.Players) {
		current = g.Players[g.CurrentIndex].ID
	}
	switch g.Phase {
	case "trade":
		if g.Trade != nil && g.Trade.TargetID == playerID {
			return act("reject_trade")
		}
	case "loan_offer":
		if g.LoanOffer != nil && g.LoanOffer.TargetID == playerID {
			return act("reject_loan")
		}
	case "turn_complete":
		if current == playerID {
			return act("end_turn")
		}
	case "awaiting_roll":
		if current == playerID {
			return act("roll")
		}
	case "auction":
		return act("pass_auction")
	case "awaiting_purchase":
		if current == playerID {
			return act("decline")
		}
	case "card_confirmation":
		if g.PendingCard != nil && g.PendingCard.PlayerID == playerID {
			return act("confirm_card")
		}
	case "debt":
		if g.Debt != nil && g.Debt.PayerID == playerID {
			if me.Cash >= g.Debt.Amount {
				return act("pay_debt")
			}
			return act("bankrupt")
		}
	}
	return nil
}

// sessionToken reads body.token (Node) or, for the table, body.tableToken.
func sessionToken(body *jsBody) string {
	if token := body.str("token"); token != "" {
		return token
	}
	return body.str("tableToken")
}

func validDifficulty(level string) bool {
	return level == "easy" || level == "normal" || level == "hard" || level == "expert" || level == "grandmaster"
}

func (s *Server) newBot(difficulty string) Bot {
	if s.cfg.Bots == nil {
		return nil
	}
	return s.cfg.Bots(difficulty, randomSeed())
}

// rematch starts a fresh lobby in the same room: same code, same players in
// the same order (devices, local seats, AI difficulty), same sessions,
// spectators and settings.
func (s *Server) rematch(code string, body *jsBody) (json.RawMessage, error) {
	if err := body.access("token"); err != nil {
		return nil, err
	}
	r, sess, err := s.authenticateHost(code, sessionToken(body))
	if err != nil {
		return nil, err
	}
	if r.info.Status != "finished" {
		return nil, badRequest("The game is not finished yet")
	}
	// A poker, Ludo, quiz or closest game's record was written when it ended; one
	// still open (it should not be) is kept before the room goes back to the
	// lobby.
	s.closeTableRecord(r, gamerecord.EndReset, "the room went back to the lobby", nil)
	var rebuild func(*room) (map[string]string, error)
	switch {
	case r.isLudo():
		rebuild = s.rematchLudo // same players in the same order, same host, same rules and local seats
	case r.isPoker():
		rebuild = s.rematchPoker // same players, same host and the same settings
	case r.isQuiz():
		rebuild = s.rematchQuiz // same players, host and rules; categories this room has not played
	case r.isClosest():
		rebuild = s.rematchClosest // same players, host and rules; questions this room has not played
	}
	if rebuild != nil {
		idMap, err := rebuild(r)
		if err != nil {
			return nil, err
		}
		for _, candidate := range r.order {
			if candidate.role == "player" {
				candidate.playerID = idMap[candidate.playerID]
			}
		}
		r.updatedAt = s.now()
		s.commit(r)
		return s.stateFor(r, sess)
	}
	// The finished game's record was written when it ended; one still open
	// (it should not be) is kept before the room goes back to the lobby.
	s.closeRecord(r, gamerecord.EndReset, "the room went back to the lobby", nil)
	old := r.last
	game := s.cfg.Engine.NewGame(r.code)
	idMap := map[string]string{}
	meta := map[string]*playerMeta{}
	for _, p := range old.Players {
		kind := p.Kind
		if kind != "ai" {
			kind = "human"
		}
		id, err := game.AddPlayer(p.Name, kind)
		if err != nil {
			return nil, err
		}
		idMap[p.ID] = id
		if m := r.meta[p.ID]; m != nil {
			meta[id] = m
		}
	}
	r.stopTimer()
	r.game = game
	r.meta = meta
	r.aiFailed, r.aiFailures = nil, 0
	for _, seat := range r.seats {
		seat.PlayerID = idMap[seat.PlayerID]
	}
	for _, candidate := range r.order {
		if candidate.role == "player" {
			candidate.playerID = idMap[candidate.playerID]
		}
	}
	host := old.HostID
	if mapped, ok := idMap[host]; ok {
		host = mapped
	}
	if host != "" {
		game.SetHost(host)
	}
	// Keep the table's rules (dice mode, house rules).
	var settings map[string]json.RawMessage
	if json.Unmarshal(old.Settings, &settings) == nil {
		if mode := settings["diceMode"]; len(mode) > 0 && string(mode) != `"random"` {
			_ = game.PerformAction(host, json.RawMessage(`{"type":"set_dice_mode","mode":`+string(mode)+`}`))
		}
		// The rules are stored sparse, so a table that played turn-order
		// auctions (auctionClock 0, stored as nothing) says so explicitly:
		// new games default to the timed auction.
		rules := map[string]json.RawMessage{}
		if raw := settings["rules"]; len(raw) > 0 && string(raw) != "null" {
			_ = json.Unmarshal(raw, &rules)
		}
		if _, ok := rules["auctionClock"]; !ok {
			rules["auctionClock"] = json.RawMessage("0")
		}
		if data, err := json.Marshal(rules); err == nil && string(data) != fmt.Sprintf(`{"auctionClock":%d}`, engine.DefaultAuctionClock) {
			_ = game.PerformAction(host, json.RawMessage(`{"type":"set_rules","rules":`+string(data)+`}`))
		}
	}
	// Clients drop states whose version they have seen; start above it.
	if fresh, err := game.Snapshot(); err == nil {
		if view, err := decodeGameView(fresh); err == nil {
			for v := view.Version; v <= old.Version; v++ {
				game.Touch()
			}
		}
	}
	r.updatedAt = s.now()
	s.commit(r)
	return s.stateFor(r, sess)
}

func itoa(n int) string {
	return jsNumberString(float64(n))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
