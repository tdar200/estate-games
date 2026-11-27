package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tdar200/estate-games/internal/gamerecord"
)

// Poker, Ludo, quiz and closest records (docs/GAME-RECORDS.md). Like Estate's, a game's
// record is a journal under <data>/games/live while it runs and <id>.json.gz
// once it ends, however it ends, and rooms.json names the journal so a
// restart picks it up. They differ in what they hold: the engine's event
// stream, each poker hand's hole cards and how the game ended, written from
// commitPoker, commitLudo, commitQuiz and commitClosest (a quiz's reveal
// events carry every tap with its timing, a closest reveal every guess and
// bet). Nothing here decides anything in the game, and
// the disk work happens off the lock, as it does for Estate.

// liveTable is a poker, Ludo, quiz or closest room's record in progress.
type liveTable struct {
	id      string
	path    string
	hand    int // the last hand whose hole cards are written
	lastSeq int64
	players map[string]bool // players written so far
	standIn map[string]bool // players the computer plays for now
	endedBy string          // set by End game: the reason to close with
}

// tableRecordable reports whether a room's games are recorded here.
func (s *Server) tableRecordable(r *room) bool {
	return s.records != nil && !r.isEstate()
}

// tablePlayer describes a seat for the record.
func (s *Server) tablePlayer(r *room, p PlayerInfo, seat int) gamerecord.Player {
	player := gamerecord.Player{ID: p.ID, Seat: seat, Name: p.Name, Kind: p.Kind, Host: r.info != nil && p.ID == r.info.HostID}
	if m := r.meta[p.ID]; m != nil {
		player.Input, player.TokenKind, player.Difficulty = m.input, m.tokenKind, m.difficulty
	}
	if player.Input == "" {
		player.Input = r.playerInput(p.ID, p.Kind)
	}
	return player
}

// openTableRecord starts the record of a game that has just begun (data is
// its snapshot), before commit writes that first batch of events. A game a
// restart brings back with no journal has the rest of it recorded.
func (s *Server) openTableRecord(r *room, data []byte, now time.Time) {
	if !s.tableRecordable(r) || r.trec != nil || r.info == nil || r.info.Status != "playing" {
		return
	}
	startedAt := now
	if r.restoring && r.startedAt > 0 {
		startedAt = time.UnixMilli(r.startedAt)
	}
	id := s.records.newID(startedAt, r.code)
	rec := &gamerecord.TableRecord{
		Header: gamerecord.Header{
			ID: id, Code: r.code, GameType: r.gameTypeName(), AppVersion: s.cfg.Version, StartedAt: startedAt.UnixMilli(),
			Room: r.hosting(), ComputerSpeed: r.speedName(), Settings: r.info.Settings, StartsMidGame: r.restoring,
		},
		Initial: append(json.RawMessage(nil), data...),
	}
	live := &liveTable{id: id, path: s.records.journalPath(id), players: map[string]bool{}, standIn: map[string]bool{}}
	for i, p := range r.info.Players {
		rec.Players = append(rec.Players, s.tablePlayer(r, p, i))
		live.players[p.ID] = true
	}
	r.trec = live
	s.claimRecord(id)
	s.records.append(live.path, gamerecord.TableHeaderLine(rec))
	s.markRoomsDirty()
}

// recordTableEvent writes one engine event, as the screens got it.
func (s *Server) recordTableEvent(r *room, seq int64, event []byte, now time.Time) {
	rec := r.trec
	if rec == nil || seq <= rec.lastSeq {
		return
	}
	rec.lastSeq = seq
	s.records.append(rec.path, gamerecord.TableEventLine(gamerecord.TableEvent{Seq: seq, At: now.UnixMilli(), Event: append(json.RawMessage(nil), event...)}))
}

// tableRecordCommit is the end of commitPoker, commitLudo, commitQuiz and commitClosest: note new
// players, the hand just dealt and the computer standing in for people, and
// close the record when the game is over. data is the commit's snapshot.
func (s *Server) tableRecordCommit(r *room, data []byte, now time.Time) {
	rec := r.trec
	if rec == nil || r.info == nil {
		return
	}
	for _, p := range r.info.Players {
		if !rec.players[p.ID] {
			rec.players[p.ID] = true
			s.records.append(rec.path, gamerecord.TablePlayerLine(s.tablePlayer(r, p, len(rec.players)-1)))
		}
		on := false
		if m := r.meta[p.ID]; m != nil {
			on = m.autopilot
		}
		if on != rec.standIn[p.ID] {
			rec.standIn[p.ID] = on
			s.records.append(rec.path, gamerecord.TableStandInLine(gamerecord.TableStandIn{PlayerID: p.ID, On: on, Seq: rec.lastSeq, At: now.UnixMilli()}))
		}
	}
	if g := pokerOf(r); g != nil {
		if hand, cards := g.t.HoleCards(); hand > rec.hand && len(cards) > 0 {
			rec.hand = hand
			s.records.append(rec.path, gamerecord.TableHolesLine(gamerecord.HoleCards{Hand: hand, Cards: cards}))
		}
	}
	if r.info.Status == "finished" {
		reason := gamerecord.EndFinished
		if rec.endedBy != "" {
			reason = rec.endedBy
		}
		s.closeTableRecord(r, reason, "", data)
	}
}

// tableOutcome is how a game stands right now.
func (s *Server) tableOutcome(r *room, reason, note string) gamerecord.TableOutcome {
	o := gamerecord.TableOutcome{Reason: reason, Note: note}
	if r.info == nil {
		return o
	}
	o.WinnerID = r.info.WinnerID
	chips := map[string]int{}
	if g := pokerOf(r); g != nil {
		for _, p := range g.t.Players {
			chips[p.ID] = p.Stack
		}
	}
	for _, p := range r.info.Players {
		o.Standings = append(o.Standings, gamerecord.TableStanding{PlayerID: p.ID, Place: p.Place, Chips: chips[p.ID], Out: p.Out})
	}
	return o
}

// closeTableRecord ends a room's record: the end line, then the final file,
// written off the lock. final is the game's last snapshot (nil: make one).
func (s *Server) closeTableRecord(r *room, reason, note string, final []byte) {
	rec := r.trec
	if rec == nil {
		return
	}
	if final == nil {
		final, _ = r.game.Snapshot()
	}
	s.records.append(rec.path, gamerecord.TableEndLine(s.tableOutcome(r, reason, note), s.now().UnixMilli(), final))
	s.records.finalize(rec.path, reason, note)
	r.trec = nil
	s.markRoomsDirty()
}

// resumeTableRecord reattaches a restored room's journal (startup, before
// its first commit). The journal is cut back to the saved game: a crash can
// leave it ahead, and those events are about to happen differently. A game
// with no usable journal gets a new record from its first commit.
func (s *Server) resumeTableRecord(r *room, recordID string, claimed map[string]bool) {
	if !s.tableRecordable(r) || recordID == "" {
		return
	}
	informer, ok := r.game.(Informer)
	if !ok || informer.Info().Status == "lobby" {
		return // that game was never saved as playing
	}
	path := s.records.journalPath(recordID)
	j, err := gamerecord.ReadTableJournal(path)
	if err != nil {
		s.log.Printf("room %s: record %s could not be read (%v)", r.code, recordID, err)
		return
	}
	claimed[recordID] = true
	events := len(j.Record.Events)
	ahead := events > 0 && j.Record.Events[events-1].Seq > r.eventSeq
	damaged := j.Damaged > 0 || j.End != nil
	j.Truncate(r.eventSeq)
	if ahead || damaged {
		if ahead {
			s.log.Printf("room %s: record %s: %d event(s) after the saved game dropped", r.code, recordID, events-len(j.Record.Events))
		}
		if err := gamerecord.WriteFileAtomic(path, j.Bytes()); err != nil {
			s.log.Printf("room %s: record %s: %v", r.code, recordID, err)
		}
	}
	live := &liveTable{id: recordID, path: path, lastSeq: r.eventSeq, players: map[string]bool{}, standIn: map[string]bool{}}
	for _, p := range j.Record.Players {
		live.players[p.ID] = true
	}
	for _, h := range j.Record.Holes {
		live.hand = max(live.hand, h.Hand)
	}
	for _, stand := range j.Record.StandIns {
		live.standIn[stand.PlayerID] = stand.On
	}
	r.trec = live
}

// finishTable turns a poker, Ludo, quiz or closest journal into <id>.json.gz.
func (rs *recordStore) finishTable(job recordJob) {
	id := strings.TrimSuffix(filepath.Base(job.path), ".jsonl")
	j, err := gamerecord.ReadTableJournal(job.path)
	if err != nil {
		rs.log.Printf("game record %s: %v", id, err)
		return
	}
	rec := j.Finish()
	if rec.Outcome == nil {
		rec.Outcome = &gamerecord.TableOutcome{Reason: job.reason, Note: job.note}
	}
	if len(rec.Events) == 0 {
		os.Remove(job.path) // nothing happened: not worth keeping
		return
	}
	if err := gamerecord.WriteTable(rs.finalPath(id), rec); err != nil {
		rs.log.Printf("game record %s: %v", id, err)
		return
	}
	os.Remove(job.path)
	rs.prune()
}
