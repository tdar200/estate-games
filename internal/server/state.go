package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// stateFor ports RoomStore.stateFor and appends the additive §3 fields:
// players[].input/tokenKind/online and, for the table, localSeats. Key order
// of the engine's JSON is preserved; new keys are appended. Poker rooms have
// their own (pokerStateFor), and so have Ludo rooms (ludoStateFor), quiz
// rooms (quizStateFor) and Near Thing rooms (closestStateFor).
func (s *Server) stateFor(r *room, sess *session) (json.RawMessage, error) {
	if r.isLudo() {
		return s.ludoStateFor(r, sess)
	}
	if r.isQuiz() {
		return s.quizStateFor(r, sess)
	}
	if r.isClosest() {
		return s.closestStateFor(r, sess)
	}
	if r.isPoker() {
		return s.pokerStateFor(r, sess)
	}
	now := s.now()
	viewer := Viewer{
		Role:        sess.role,
		Name:        sess.name,
		SpectatorID: sess.spectatorID,
		IsHost:      sess.isHost,
		Spectators:  s.spectatorList(r, now),
	}
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
			tokenKind := ""
			if m := r.meta[head.ID]; m != nil {
				tokenKind = m.tokenKind
			}
			difficulty := ""
			if m := r.meta[head.ID]; m != nil && head.Kind == "ai" {
				difficulty = m.difficulty
				if difficulty == "" {
					difficulty = "normal"
				}
			}
			autopilot, outOrder := false, 0
			var away *awayMark
			if m := r.meta[head.ID]; m != nil {
				autopilot, outOrder, away = m.autopilot, m.outOrder, m.away
			}
			extra, _ := marshalJS(struct {
				Input      string    `json:"input"`
				TokenKind  string    `json:"tokenKind"`
				Online     bool      `json:"online"`
				Difficulty string    `json:"difficulty,omitempty"`
				Autopilot  bool      `json:"autopilot,omitempty"`
				OutOrder   int       `json:"outOrder,omitempty"`
				NotSelling []int     `json:"notSelling,omitempty"`
				StandIn    *awayMark `json:"standIn,omitempty"` // while-away list (standin.go)
				Skipped    bool      `json:"skipped,omitempty"` // the host skips their turn (hostcontrols.go)
			}{r.playerInput(head.ID, head.Kind), tokenKind, s.playerOnline(r, head.ID, head.Kind, now), difficulty, autopilot, outOrder, notSellingList(r.meta[head.ID]), away, r.skipping(head.ID)})
			// Party tables show each player in their token's colour (the one they
			// picked when joining). Classic rooms keep the engine's seat colours so
			// the compatibility API stays byte-identical to the Node server.
			if r.table && tokenKind != "" {
				if color := s.tokenColor(tokenKind); color != "" {
					quoted, _ := marshalJS(color)
					p = replaceField(p, "color", quoted)
				}
			}
			buf.Write(appendFields(p, extra))
		}
		buf.WriteByte(']')
		fields[i].value = buf.Bytes()
	}
	fields = jsKeyOrder(fields)
	for i := range fields {
		switch fields[i].key {
		case "groups":
			fields[i].value = orderGroups(fields[i].value, r.board)
		case "auction":
			fields[i].value = orderKeys(fields[i].value, auctionOrder)
		case "lastLanding":
			fields[i].value = landingCardRent(fields[i].value, r.last, r.board) // RENT ×2 / ×10 (cardrent.go)
		}
	}
	if r.last != nil {
		counts := make([]objectField, 0, len(r.last.Players))
		seen := map[string]bool{}
		for _, p := range r.last.Players {
			seen[p.ID] = true
			counts = append(counts, objectField{key: p.ID, value: json.RawMessage(itoa(r.last.TurnCounts[p.ID]))})
		}
		for _, id := range sortedKeys(r.last.TurnCounts) {
			if !seen[id] {
				counts = append(counts, objectField{key: id, value: json.RawMessage(itoa(r.last.TurnCounts[id]))})
			}
		}
		fields = append(fields,
			objectField{key: "turnCounts", value: joinObject(counts)},
			objectField{key: "turnNumber", value: json.RawMessage(itoa(r.last.turnNumber()))})
	}
	fields, err = s.appendRoomFields(r, sess, fields, now)
	if err != nil {
		return nil, err
	}
	return unescapeHTMLInJSON(joinObject(fields)), nil
}

// appendRoomFields adds the room-level fields every party state carries:
// gameType and aiLevels (party rooms; the classic API keeps Node's shape),
// roomSettings, claims and, for the table screen, localSeats.
func (s *Server) appendRoomFields(r *room, sess *session, fields []objectField, now time.Time) ([]objectField, error) {
	if r.table {
		gameType, _ := marshalJS(r.gameTypeName())
		levels, _ := marshalJS(s.aiLevels(r))
		settings, _ := marshalJS(struct {
			ComputerSpeed string `json:"computerSpeed"`
			Listed        bool   `json:"listed"`
			BotOffers     string `json:"botOffers"`
		}{r.speedName(), r.listed, r.botOffersMode()})
		fields = append(fields,
			objectField{key: "gameType", value: gameType},
			objectField{key: "aiLevels", value: levels},
			objectField{key: "roomSettings", value: settings})
	}
	// Requests to move a seat or the table to a new device, for the people
	// who may answer them (claims.go); omitted when there are none.
	if claims := s.claimsFor(r, sess, now); len(claims) > 0 {
		value, err := marshalJS(claims)
		if err != nil {
			return nil, err
		}
		fields = append(fields, objectField{key: "claims", value: value})
	}
	if sess.role == "table" {
		seats := r.seats
		if seats == nil {
			seats = []*localSeat{}
		}
		value, err := marshalJS(seats)
		if err != nil {
			return nil, err
		}
		fields = append(fields, objectField{key: "localSeats", value: value})
	}
	return fields, nil
}

// publicStateOrder is the key order of publicState in src/game.js.
var publicStateOrder = map[string]int{}

func init() {
	for i, key := range []string{"code", "status", "phase", "hostId", "players", "currentIndex", "currentPlayerId",
		"properties", "turn", "pendingPurchase", "auction", "debt", "winnerId", "lastCard", "pendingCard",
		"lastLanding", "lastBankruptcy", "lastMove", "trade", "openingOrder", "turnSummary", "settings", "bank",
		"log", "version", "board", "groups", "viewerId", "viewerRole", "viewerName", "spectatorId", "viewerIsHost",
		"spectators"} {
		publicStateOrder[key] = i
	}
}

// jsKeyOrder restores the JS key order (the Go engine emits nullable ids
// last); keys unknown to game.js (house rules) follow in their own order.
func jsKeyOrder(fields []objectField) []objectField {
	sort.SliceStable(fields, func(i, j int) bool {
		ri, iok := publicStateOrder[fields[i].key]
		rj, jok := publicStateOrder[fields[j].key]
		if iok && jok {
			return ri < rj
		}
		return iok && !jok
	})
	return fields
}

// orderGroups lists groups in board order, the insertion order of GROUPS in
// src/board.js (Go maps marshal with sorted keys).
func orderGroups(raw json.RawMessage, board []boardSpace) json.RawMessage {
	fields, err := splitObject(raw)
	if err != nil || len(board) == 0 {
		return raw
	}
	rank := map[string]int{}
	for _, space := range board {
		if _, seen := rank[space.Group]; space.Group != "" && !seen {
			rank[space.Group] = len(rank)
		}
	}
	sort.SliceStable(fields, func(i, j int) bool {
		ri, iok := rank[fields[i].key]
		rj, jok := rank[fields[j].key]
		if iok && jok {
			return ri < rj
		}
		return iok && !jok
	})
	return joinObject(fields)
}

// auctionOrder is the key order of {...game.auction, currentPlayerId}.
var auctionOrder = []string{"spaceIndex", "activeIds", "turnPos", "currentBid", "bidderId", "returnPhase", "currentPlayerId", "clockMs", "endsAt", "bids", "passedIds"}

// orderKeys sorts an object's keys by a fixed list; other keys follow.
func orderKeys(raw json.RawMessage, order []string) json.RawMessage {
	if len(raw) == 0 || raw[0] != '{' {
		return raw
	}
	fields, err := splitObject(raw)
	if err != nil {
		return raw
	}
	rank := map[string]int{}
	for i, key := range order {
		rank[key] = i
	}
	sort.SliceStable(fields, func(i, j int) bool {
		ri, iok := rank[fields[i].key]
		rj, jok := rank[fields[j].key]
		if iok && jok {
			return ri < rj
		}
		return iok && !jok
	})
	return joinObject(fields)
}

type objectField struct {
	key   string
	value json.RawMessage
}

// splitObject parses a JSON object into ordered key/value pairs.
func splitObject(data []byte) ([]objectField, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("state is not a JSON object")
	}
	var fields []objectField
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("invalid object key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields = append(fields, objectField{key: key, value: value})
	}
	return fields, nil
}

func joinObject(fields []objectField) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := marshalJS(f.key)
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(f.value)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// appendFields merges the members of the JSON object extra into the end of
// the JSON object obj.
func appendFields(obj, extra []byte) []byte {
	obj = bytes.TrimSpace(obj)
	extra = bytes.TrimSpace(extra)
	inner := bytes.TrimSpace(extra[1 : len(extra)-1])
	if len(inner) == 0 {
		return obj
	}
	body := bytes.TrimSpace(obj[:len(obj)-1])
	out := make([]byte, 0, len(obj)+len(inner)+2)
	out = append(out, body...)
	if len(body) > 1 {
		out = append(out, ',')
	}
	out = append(out, inner...)
	return append(out, '}')
}
