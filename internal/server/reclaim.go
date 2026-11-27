package server

import (
	"net/http"
	"time"
)

// Getting back in (docs/API.md "Getting back in"): a seat or table screen
// belongs to the browser that made it (its device id). The same browser gets
// back in by itself; another browser, phone or private tab can take over an
// offline seat or table once the game approves (claims.go), so leaving never
// locks anyone out of their own game and a stranger can't walk in.

const (
	msgSeatOnline     = "That seat is still connected. Close the game there first, or wait 20 seconds."
	msgSeatNotOwnable = "That seat can't be taken over"
	msgDeviceHasSeat  = "This device already has a seat at this table"
	msgSeatUnknown    = "That player is not at this table"
	msgTableOnline    = "This table is open on another screen. Close it there, or wait 20 seconds."
	msgNoTableScreen  = "This game has no table screen"
	msgSessionMoved   = "This session moved to another device"
)

func conflict(msg string) error { return &httpError{status: http.StatusConflict, msg: msg} }

// takeoverSeat reports whether a seat may move to another device: a human
// seat played from a phone or browser (keyboard and controller seats belong
// to the table, computers to the server) whose player is still in the game.
func (r *room) takeoverSeat(p *PlayerInfo) bool {
	if p == nil || p.Kind == "ai" || p.Out {
		return false
	}
	if m := r.meta[p.ID]; m != nil {
		switch m.input {
		case "controller", "keyboard", "ai":
			return false
		}
	}
	return true
}

// reclaimable reports whether a seat can be asked for right now.
func (s *Server) reclaimable(r *room, p *PlayerInfo, now time.Time) bool {
	return r.takeoverSeat(p) && !s.playerOnline(r, p.ID, p.Kind, now)
}

// reclaimSeat handles POST /reclaim {playerId, approvalCode?}: the device's
// own seat is a rejoin; an offline seat of someone else's device moves here
// once the game approves (202 with the pending request until then).
func (s *Server) reclaimSeat(r *room, device *deviceRecord, body *jsBody, ip string) (any, error) {
	if err := body.access("playerId"); err != nil {
		return nil, err
	}
	playerID := body.str("playerId")
	p := r.info.player(playerID)
	if p == nil {
		return nil, badRequest(msgSeatUnknown)
	}
	if !r.takeoverSeat(p) {
		return nil, badRequest(msgSeatNotOwnable)
	}
	held := r.devicePlayer(device.ID)
	if held == playerID {
		return s.rejoinRoom(r, device)
	}
	if other := r.info.player(held); other != nil && !other.Out {
		return nil, badRequest(msgDeviceHasSeat)
	}
	now := s.now()
	if s.playerOnline(r, playerID, p.Kind, now) {
		return nil, conflict(msgSeatOnline)
	}
	if !r.approversExist(claimSeat, playerID) {
		// Nobody else could ever say yes (a solo game against computers):
		// only the network the seat was last played from may take it.
		if last := r.seatAddress(playerID); last == "" || addressKey(last) != addressKey(ip) {
			return nil, forbidden(msgClaimNoApprover)
		}
		return s.takeOverSeat(r, device, p)
	}
	granted, c, err := s.requestClaim(r, device, claimSeat, playerID, body.str("approvalCode"))
	if err != nil {
		return nil, err
	}
	if !granted {
		return pendingResult(c), nil
	}
	return s.takeOverSeat(r, device, p)
}

// takeOverSeat moves an offline seat to device and returns a session for it.
// The seat's old sessions end, so a browser that comes back later is shown
// that its seat moved rather than playing alongside the new one.
func (s *Server) takeOverSeat(r *room, device *deviceRecord, p *PlayerInfo) (*entryResult, error) {
	playerID := p.ID
	now := s.now()
	isHost := r.info.HostID == playerID
	for _, sess := range append([]*session(nil), r.order...) {
		if sess.role == "player" && sess.playerID == playerID {
			isHost = isHost || sess.isHost
			s.endSession(r, sess.token, msgSessionMoved)
		}
	}
	m := r.meta[playerID]
	if m == nil {
		m = &playerMeta{tokenKind: r.pickToken("")}
		r.meta[playerID] = m
	}
	m.input = "phone"
	m.deviceID = device.ID
	r.dropClaims(claimSeat, playerID)
	sess := &session{role: "player", playerID: playerID, name: p.Name, isHost: isHost, deviceID: device.ID, input: m.input, tokenKind: m.tokenKind}
	token := s.createSession(r, sess)
	r.updatedAt = now
	s.log.Printf("room %s: %s moved to a new device", r.code, p.Name)
	s.commit(r)
	state, err := s.stateFor(r, sess)
	if err != nil {
		return nil, err
	}
	return &entryResult{Code: r.code, Token: token, PlayerID: playerID, State: state}, nil
}

// tableOnline reports whether a table screen is connected.
func (s *Server) tableOnline(r *room, now time.Time) bool {
	for _, sess := range r.order {
		if sess.role == "table" && sess.online(now) {
			return true
		}
	}
	return false
}

// resumeTable opens an existing table's PC screen in a new browser. One table
// screen at a time: the old (offline) one's session ends. The browser that
// was the table, and the host player's own device, get straight in; any other
// device needs the game's approval like a seat does (claims.go).
func (s *Server) resumeTable(code string, device *deviceRecord, body *jsBody, ip string) (any, error) {
	r, err := s.requireRoom(code)
	if err != nil {
		return nil, err
	}
	if !r.table {
		return nil, badRequest(msgNoTableScreen)
	}
	now := s.now()
	if s.tableOnline(r, now) {
		return nil, conflict(msgTableOnline)
	}
	old := r.tableSession()
	trusted := device != nil && ((old != nil && old.deviceID == device.ID) ||
		(r.info.HostID != "" && r.devicePlayer(device.ID) == r.info.HostID))
	if !trusted {
		if !r.approversExist(claimTable, "") {
			// Only keyboard/controller seats and computers: nobody to ask, so
			// the table may only come back on the network it was last used from.
			if old != nil && old.ip != "" && addressKey(old.ip) != addressKey(ip) {
				return nil, forbidden(msgTableNoApprover)
			}
		} else {
			if device == nil {
				return nil, unauthorized(msgDeviceNeeded)
			}
			granted, c, err := s.requestClaim(r, device, claimTable, "", body.str("approvalCode"))
			if err != nil {
				return nil, err
			}
			if !granted {
				return pendingResult(c), nil
			}
		}
	}
	for _, sess := range append([]*session(nil), r.order...) {
		if sess.role == "table" {
			s.endSession(r, sess.token, msgSessionMoved)
		}
	}
	r.dropClaims(claimTable, "")
	sess := &session{role: "table", name: "Table", isHost: true}
	if device != nil {
		sess.deviceID = device.ID
	}
	token := s.createSession(r, sess)
	r.updatedAt = now
	s.commit(r)
	state, err := s.stateFor(r, sess)
	if err != nil {
		return nil, err
	}
	return &entryResult{Code: r.code, TableToken: token, State: state}, nil
}
