package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
)

// Games survive a server restart (an update, a reboot, a crash): every room is
// saved to <data>/rooms.json, debounced like device data, and restored at
// startup with the same codes and session tokens, so phones and the table
// screen simply reconnect. Rooms idle longer than roomTTL are not restored.

const roomsFileName = "rooms.json"

// GameLoader is an Engine that can rebuild a game from its Snapshot JSON.
type GameLoader interface {
	LoadGame(code string, snapshot []byte) (Game, error)
}

// LoadGame implements GameLoader: the engine's game JSON round-trips exactly
// (hardai's TestCloneMatchesJSON checks this).
func (RealEngine) LoadGame(code string, snapshot []byte) (Game, error) {
	var g engine.Game
	if err := json.Unmarshal(snapshot, &g); err != nil {
		return nil, err
	}
	if g.Code != code {
		return nil, fmt.Errorf("saved game is for room %q", g.Code)
	}
	return &engineGame{g: &g}, nil
}

type savedRooms struct {
	Version int         `json:"version"`
	Rooms   []savedRoom `json:"rooms"`
}

type savedRoom struct {
	Code string `json:"code"`
	// GameType is absent for Estate (files from before game types load as
	// Estate), else "poker", "ludo", "quiz" or "closest"; EventSeq is the last engine
	// event sent, so a restart doesn't send old events again.
	GameType  string               `json:"gameType,omitempty"`
	EventSeq  int64                `json:"eventSeq,omitempty"`
	Table     bool                 `json:"table"`
	Game      json.RawMessage      `json:"game"`
	Sessions  []savedSession       `json:"sessions"`
	Seats     []*localSeat         `json:"seats"`
	Meta      map[string]savedMeta `json:"meta"`
	UpdatedAt int64                `json:"updatedAt"`
	StartedAt int64                `json:"startedAt"`
	Speed     string               `json:"computerSpeed,omitempty"`
	Listed    bool                 `json:"listed,omitempty"`
	BotOffers string               `json:"botOffers,omitempty"`
	CreatorIP string               `json:"creatorIp,omitempty"`
	CreatorDv string               `json:"creatorDevice,omitempty"`
	// RecordID names the game record in progress (<data>/games/live/<id>.jsonl).
	RecordID string `json:"recordId,omitempty"`
}

type savedSession struct {
	Token       string `json:"token"`
	Role        string `json:"role"`
	PlayerID    string `json:"playerId,omitempty"`
	SpectatorID string `json:"spectatorId,omitempty"`
	Name        string `json:"name"`
	IsHost      bool   `json:"isHost,omitempty"`
	DeviceID    string `json:"deviceId,omitempty"`
	Input       string `json:"input,omitempty"`
	TokenKind   string `json:"tokenKind,omitempty"`
	IP          string `json:"ip,omitempty"`
}

type savedMeta struct {
	Input      string `json:"input,omitempty"`
	TokenKind  string `json:"tokenKind,omitempty"`
	DeviceID   string `json:"deviceId,omitempty"`
	SeatID     string `json:"seatId,omitempty"`
	Pad        *int   `json:"pad,omitempty"`
	Difficulty string `json:"difficulty,omitempty"`
	OutOrder   int    `json:"outOrder,omitempty"`
	NotSelling []int  `json:"notSelling,omitempty"`
}

// markRoomsDirty schedules a save of every room. Requires s.mu.
func (s *Server) markRoomsDirty() {
	if s.roomStore != nil && !s.closed {
		s.roomStore.markDirty(s.snapshotRooms)
	}
}

// snapshotRooms serialises every room (takes s.mu).
func (s *Server) snapshotRooms() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	codes := make([]string, 0, len(s.rooms))
	for code := range s.rooms {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	doc := savedRooms{Version: 1, Rooms: make([]savedRoom, 0, len(codes))}
	for _, code := range codes {
		r := s.rooms[code]
		if len(r.lastJSON) == 0 {
			continue
		}
		updated := r.updatedAt
		if s.personConnected(r) {
			updated = s.now() // someone is playing or showing it: the game is in use
		}
		saved := savedRoom{
			Code: r.code, GameType: r.gameType, EventSeq: r.eventSeq, Table: r.table, Game: json.RawMessage(r.lastJSON), Seats: r.seats,
			Meta: map[string]savedMeta{}, UpdatedAt: updated.UnixMilli(), StartedAt: r.startedAt, Speed: r.computerSpeed,
			Listed: r.listed, BotOffers: r.botOffers, CreatorIP: r.creatorIP, CreatorDv: r.creatorDevice,
		}
		for _, sess := range r.order {
			saved.Sessions = append(saved.Sessions, savedSession{
				Token: sess.token, Role: sess.role, PlayerID: sess.playerID, SpectatorID: sess.spectatorID,
				Name: sess.name, IsHost: sess.isHost, DeviceID: sess.deviceID, Input: sess.input, TokenKind: sess.tokenKind, IP: sess.ip,
			})
		}
		if r.rec != nil {
			saved.RecordID = r.rec.id
		}
		if r.trec != nil {
			saved.RecordID = r.trec.id
		}
		for id, m := range r.meta {
			saved.Meta[id] = savedMeta{Input: m.input, TokenKind: m.tokenKind, DeviceID: m.deviceID, SeatID: m.seatID, Pad: m.pad, Difficulty: m.difficulty, OutOrder: m.outOrder, NotSelling: notSellingList(m)}
		}
		doc.Rooms = append(doc.Rooms, saved)
	}
	return json.Marshal(doc)
}

// loaderFor returns the engine that can restore a saved game of this type.
func (s *Server) loaderFor(gameType string) GameLoader {
	engine, err := s.engineFor(gameType)
	if err != nil {
		return nil
	}
	loader, _ := engine.(GameLoader)
	return loader
}

// restoreRooms loads saved rooms. Requires s.mu; called once from New.
func (s *Server) restoreRooms() {
	data, err := os.ReadFile(s.roomStore.path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		s.log.Printf("reading %s failed: %v", s.roomStore.path, err)
		return
	}
	var doc savedRooms
	if err := json.Unmarshal(data, &doc); err != nil {
		aside := s.roomStore.path + ".corrupt-" + time.Now().Format("20060102-150405")
		_ = os.Rename(s.roomStore.path, aside)
		s.log.Printf("saved games in %s are damaged (%v); moved to %s", s.roomStore.path, err, aside)
		return
	}
	now := s.now()
	restored := 0
	for _, saved := range doc.Rooms {
		updated := time.UnixMilli(saved.UpdatedAt)
		if saved.Code == "" || now.Sub(updated) >= roomTTL || s.rooms[saved.Code] != nil {
			continue
		}
		loader := s.loaderFor(saved.GameType)
		if loader == nil {
			s.log.Printf("room %s not restored: this server can't load %q games", saved.Code, saved.GameType)
			continue
		}
		game, err := loader.LoadGame(saved.Code, saved.Game)
		if err != nil {
			s.log.Printf("room %s not restored: %v", saved.Code, err)
			continue
		}
		r := s.newRoom(saved.Code, saved.GameType, game, saved.Table)
		r.eventSeq = saved.EventSeq
		if l := ludoOf(r); l != nil && r.eventSeq == 0 {
			r.eventSeq = l.g.EventSeq // never replay events already shown
		}
		if g := pokerOf(r); g != nil && r.eventSeq == 0 {
			r.eventSeq = g.t.EventSeq // never replay events already shown
		}
		if q := quizOf(r); q != nil && r.eventSeq == 0 {
			r.eventSeq = q.g.EventSeq // never replay events already shown
		}
		if c := closestOf(r); c != nil && r.eventSeq == 0 {
			r.eventSeq = c.g.EventSeq // never replay events already shown
		}
		r.updatedAt = updated
		r.startedAt = saved.StartedAt
		r.computerSpeed = saved.Speed
		r.listed = saved.Listed
		r.botOffers = saved.BotOffers
		r.creatorIP, r.creatorDevice = saved.CreatorIP, saved.CreatorDv
		r.seats = saved.Seats
		for id, m := range saved.Meta {
			meta := &playerMeta{input: m.Input, tokenKind: canonicalTokenKind(m.TokenKind), deviceID: m.DeviceID, seatID: m.SeatID, pad: m.Pad, difficulty: m.Difficulty, outOrder: m.OutOrder}
			for _, i := range m.NotSelling {
				if meta.notSelling == nil {
					meta.notSelling = map[int]bool{}
				}
				meta.notSelling[i] = true
			}
			if meta.input == "ai" && r.isLudo() {
				meta.ludoBot = s.newLudoBot(m.Difficulty)
			} else if meta.input == "ai" && r.isPoker() {
				meta.pokerBot = s.newPokerBot(m.Difficulty)
			} else if meta.input == "ai" && r.isEstate() {
				meta.bot = s.newBot(m.Difficulty) // (quiz and closest computers live in the engine)
			}
			r.meta[id] = meta
		}
		for _, ss := range saved.Sessions {
			if ss.Token == "" || r.sessions[ss.Token] != nil {
				continue
			}
			sess := &session{
				token: ss.Token, role: ss.Role, playerID: ss.PlayerID, spectatorID: ss.SpectatorID, name: ss.Name,
				isHost: ss.IsHost, deviceID: ss.DeviceID, input: ss.Input, tokenKind: canonicalTokenKind(ss.TokenKind), ip: ss.IP,
				lastSeen: now.Add(-onlineWindow), // offline until they reconnect
			}
			r.sessions[sess.token] = sess
			r.order = append(r.order, sess)
		}
		s.rooms[r.code] = r
		if s.records != nil {
			s.resumeTableRecord(r, saved.RecordID, s.recordClaims)
		}
		r.restoring = true
		s.commit(r)
		r.restoring = false
		if s.records != nil {
			s.resumeRecord(r, saved.RecordID, s.recordClaims)
			if r.rec != nil && r.info != nil && r.info.Status == "finished" {
				s.closeRecord(r, gamerecord.EndFinished, "", nil)
			}
		}
		s.scheduleAI(r)
		restored++
	}
	if restored > 0 {
		s.log.Printf("restored %d game(s) from %s", restored, s.roomStore.path)
	}
}

// openRoomStore prepares <dir>/rooms.json when any configured engine can
// restore games (each saved room is restored by its own type's engine).
func (s *Server) openRoomStore(dir string, delay time.Duration) {
	canLoad := s.loaderFor(GameEstate) != nil
	for name := range s.engines {
		canLoad = canLoad || s.loaderFor(name) != nil
	}
	if !canLoad {
		return
	}
	s.roomStore = &persister{path: filepath.Join(dir, roomsFileName), delay: delay, log: s.log}
	s.mu.Lock()
	s.restoreRooms()
	s.mu.Unlock()
}
