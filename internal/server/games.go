package server

import (
	"net/http"
	"sort"
	"time"

	"github.com/tdar200/estate-games/internal/gamerecord"
)

// Games without a PC, the list of games on this server
// (docs/API.md "Phone-hosted games and the games list"), and the room quotas.

const msgRoomQuota = "You already have several games open on this server. Finish or leave one first."

// roomQuota identifies who is creating a room: their client address (empty
// when the request comes from the server machine itself, which has no quota)
// and their device, if any.
type roomQuota struct {
	ip       string
	deviceID string
}

func quotaFor(r *http.Request, device *deviceRecord) roomQuota {
	q := roomQuota{}
	if !localRequest(r) {
		q.ip = addressKey(clientIP(r))
	}
	if device != nil {
		q.deviceID = device.ID
	}
	return q
}

// checkRoomQuota enforces the global room cap and the per-address and
// per-device caps on live rooms. A creator at their cap first loses their
// own abandoned rooms (nobody connected, not in play, or idle for an hour).
// Requires s.mu.
func (s *Server) checkRoomQuota(q roomQuota) error {
	now := s.now()
	s.pruneRooms(now)
	mine := func(r *room) bool {
		return (q.ip != "" && r.creatorIP == q.ip) || (q.deviceID != "" && r.creatorDevice == q.deviceID)
	}
	over := func() bool {
		byIP, byDevice := 0, 0
		for _, r := range s.rooms {
			if q.ip != "" && r.creatorIP == q.ip {
				byIP++
			}
			if q.deviceID != "" && r.creatorDevice == q.deviceID {
				byDevice++
			}
		}
		return byIP >= maxRoomsPerIP || byDevice >= maxRoomsPerDev
	}
	if over() {
		for code, r := range s.rooms {
			if !mine(r) || s.personOnline(r, now) {
				continue
			}
			if r.info == nil || r.info.Status != "playing" || now.Sub(r.personSeenAt) >= time.Hour {
				s.deleteRoom(code, r, gamerecord.EndAbandoned)
			}
		}
		if over() {
			return tooMany(msgRoomQuota)
		}
	}
	if len(s.rooms) >= maxRooms {
		return &httpError{status: http.StatusServiceUnavailable, msg: msgRoomLimit}
	}
	return nil
}

// createGame starts a party game from a phone: the same kind of room a PC
// table makes, with the creator as its host player and no table screen yet (a
// PC can show it later with /api/tables/{code}/resume, once the host approves).
// body.gameType chooses the game: "estate" (default) or "poker".
func (s *Server) createGame(body *jsBody, device *deviceRecord, quota roomQuota) (*entryResult, error) {
	if device == nil {
		return nil, unauthorized(msgDeviceNeeded)
	}
	if err := body.access("name"); err != nil {
		return nil, err
	}
	if _, err := cleanName(body.str("name")); err != nil {
		return nil, err
	}
	gameType, err := s.requestedGameType(body)
	if err != nil {
		return nil, err
	}
	engine, err := s.engineFor(gameType)
	if err != nil {
		return nil, err
	}
	if err := s.checkRoomQuota(quota); err != nil {
		return nil, err
	}
	code, err := s.createCode()
	if err != nil {
		return nil, err
	}
	r := s.newRoom(code, gameType, engine.NewGame(code), true)
	r.creatorIP, r.creatorDevice = quota.ip, quota.deviceID
	s.rooms[code] = r
	s.commit(r)
	result, err := s.joinRoom(code, body, device)
	if err != nil {
		s.deleteRoom(code, r, gamerecord.EndAbandoned)
		return nil, err
	}
	return result, nil
}

// deviceRelation says how a device is involved in a room: it holds a seat
// (mine), is or was its table screen, is watching, or was invited.
func (s *Server) deviceRelation(r *room, deviceID string) (mine, member bool) {
	if deviceID == "" {
		return false, false
	}
	if r.devicePlayer(deviceID) != "" {
		return true, true
	}
	for _, sess := range r.order {
		if sess.deviceID == deviceID {
			return false, true
		}
	}
	_, invited := r.invited[deviceID]
	return false, invited
}

// listGames returns the games this device is part of (a seat, its table
// screen, watching, or an invite) plus games their host opened to everyone
// (roomSettings.listed). Strangers see only open games, and never which
// seats could be taken over. Empty lobbies whose table screen has gone, and
// finished games nobody is looking at, are left out.
func (s *Server) listGames(device *deviceRecord) map[string]any {
	now := s.now()
	type entry struct {
		info    map[string]any
		mine    bool
		rank    int
		updated time.Time
	}
	deviceID := ""
	if device != nil {
		deviceID = device.ID
	}
	var entries []entry
	for code, r := range s.rooms {
		if r.info == nil {
			continue
		}
		mine, member := s.deviceRelation(r, deviceID)
		if !member && !r.listed {
			continue
		}
		online := s.tableOnline(r, now)
		for _, p := range r.info.Players {
			if s.playerOnline(r, p.ID, p.Kind, now) && p.Kind != "ai" {
				online = true
			}
		}
		if (len(r.info.Players) == 0 || r.info.AbandonableLobby()) && !online {
			continue // nobody waiting: an empty room, or a lobby of computers only
		}
		if r.info.Status == "finished" && !online {
			continue
		}
		info, err := s.roomSummary(code)
		if err != nil {
			continue
		}
		if !member {
			// An open game shown to someone who isn't part of it: no seat ids
			// and nothing to take over.
			if players, ok := info["players"].([]summaryPlayer); ok {
				for i := range players {
					players[i].Reclaimable = false
				}
			}
		}
		e := entry{info: info, updated: r.updatedAt, mine: mine}
		if mine {
			info["playerId"] = r.devicePlayer(deviceID)
		}
		info["mine"] = e.mine
		info["online"] = online
		info["listed"] = r.listed
		info["updatedAt"] = r.updatedAt.UnixMilli()
		switch r.info.Status {
		case "lobby":
			e.rank = 0
		case "playing":
			e.rank = 1
		default:
			e.rank = 2
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.mine != b.mine {
			return a.mine
		}
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if !a.updated.Equal(b.updated) {
			return a.updated.After(b.updated)
		}
		return a.info["code"].(string) < b.info["code"].(string)
	})
	games := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		games = append(games, e.info)
	}
	return map[string]any{"games": games}
}
