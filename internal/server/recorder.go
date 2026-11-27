package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
)

// Game records (docs/GAME-RECORDS.md): every Estate game is kept in
// <data>/games/YYYY-MM-DD-HHMM-<code>.json.gz once it ends, however it ends
// (a winner, the host's End game, the room closed or expired, a rematch).
//
// While a game runs, its record is a journal, <data>/games/live/<id>.jsonl,
// one line per step, appended by a background writer: the server lock only
// captures the step (the random numbers, ids and time the engine used) and
// hashes the snapshot commit makes anyway. rooms.json names each room's
// journal (recordId), so a restart picks the record up where it was: the
// journal is cut back to the step that produced the restored game, or, if
// the journal is behind it, continued after a resync step. Poker and Ludo
// games have their own, simpler records: see tablerecord.go.

const (
	recordsDirName      = "games"
	liveRecordsDirName  = "live"
	defaultRecordKeep   = 500
	defaultRecordMaxLen = 200 << 20 // bytes of finished records kept
)

// stepSource says who applied a step.
type stepSource struct {
	source   string // gamerecord.Source*
	via      string
	fallback bool
	think    time.Duration
}

// liveRecord is a room's record in progress.
type liveRecord struct {
	id       string
	path     string // the journal
	steps    int    // steps written
	lastHash string // state hash after the last step
	pending  *gamerecord.Step
	standIn  map[string]bool // players the computer plays for now
	endedBy  string          // set by End game: the reason to close with
}

// recordable reports whether a room's game is recorded: Estate on the real
// engine with a data directory.
func (s *Server) recordable(r *room) (*engineGame, bool) {
	if s.records == nil || !r.isEstate() {
		return nil, false
	}
	eg, ok := r.game.(*engineGame)
	return eg, ok
}

// applyAction performs an action for actor and records it. Every action of a
// person or a computer in an Estate room goes through here.
func (s *Server) applyAction(r *room, actor string, action json.RawMessage, src stepSource) error {
	eg, ok := s.recordable(r)
	if !ok {
		return r.game.PerformAction(actor, action)
	}
	s.settleRecord(r, nil)
	if r.rec == nil {
		if eg.g.Status != engine.StatusLobby || actionType(action) != engine.ActionStart {
			return r.game.PerformAction(actor, action)
		}
		initial, err := json.Marshal(eg.g)
		if err != nil {
			return r.game.PerformAction(actor, action)
		}
		st, err := gamerecord.CaptureAction(eg.g, actor, action)
		if err != nil || eg.g.Status != engine.StatusPlaying {
			return err // a refused start changes nothing
		}
		s.openRecord(r, eg, initial, false)
		s.queueStep(r, st, src)
		return nil
	}
	st, err := gamerecord.CaptureAction(eg.g, actor, action)
	if st.Kind != "" {
		s.queueStep(r, st, src)
	}
	return err
}

// completeOrder ends the turn-order countdown (the server's timer).
func (s *Server) completeOrder(r *room) bool {
	eg, ok := s.recordable(r)
	if !ok || r.rec == nil {
		return r.game.CompleteOpeningOrder(r.game.Now())
	}
	s.settleRecord(r, nil)
	st, done := gamerecord.CaptureOrder(eg.g, eg.g.Now())
	if done {
		s.queueStep(r, st, stepSource{source: gamerecord.SourceServer})
	}
	return done
}

// endRecorded is End game for a recorded room (by: the host's player id, ""
// for the table screen).
func (s *Server) endRecorded(r *room, ender GameEnder, by, via string) error {
	eg, ok := s.recordable(r)
	if !ok || r.rec == nil {
		if r.trec != nil {
			r.trec.endedBy = gamerecord.EndEnded
		}
		_, err := ender.EndGame()
		return err
	}
	s.settleRecord(r, nil)
	st, _, err := gamerecord.CaptureEnd(eg.g, by)
	if err != nil {
		return err
	}
	r.rec.endedBy = gamerecord.EndEnded
	s.queueStep(r, st, stepSource{source: gamerecord.SourceHost, via: via})
	return nil
}

// queueStep keeps a captured step until commit hashes the game after it.
func (s *Server) queueStep(r *room, st gamerecord.Step, src stepSource) {
	st.Source, st.Via, st.Fallback = src.source, src.via, src.fallback
	if src.think > 0 {
		st.ThinkMs = int(src.think.Round(time.Millisecond) / time.Millisecond)
		if st.ThinkMs == 0 {
			st.ThinkMs = 1
		}
	}
	r.rec.pending = &st
}

// settleRecord gives the pending step its state hash, from data (the game
// snapshot commit just made) or a fresh snapshot, and writes it, unless it is
// a refused action that changed nothing. Requires s.mu.
func (s *Server) settleRecord(r *room, data []byte) {
	rec := r.rec
	if rec == nil || rec.pending == nil {
		return
	}
	st := rec.pending
	rec.pending = nil
	if data == nil {
		var err error
		if data, err = r.game.Snapshot(); err != nil {
			s.log.Printf("room %s: record: %v", r.code, err)
			return
		}
	}
	st.Hash = gamerecord.HashSnapshot(data)
	if st.Skip(rec.lastHash) {
		return
	}
	st.N = rec.steps
	rec.steps++
	rec.lastHash = st.Hash
	s.records.append(rec.path, gamerecord.StepLine(st))
}

// recordCommit is commit's part: settle the step just applied, note the
// computer standing in for people, and close the record when the game is
// over. data is the snapshot commit made. Requires s.mu.
func (s *Server) recordCommit(r *room, data []byte, now time.Time) {
	rec := r.rec
	if rec == nil {
		return
	}
	s.settleRecord(r, data)
	for _, p := range r.info.Players {
		on := false
		if m := r.meta[p.ID]; m != nil {
			on = m.autopilot
		}
		if on != rec.standIn[p.ID] {
			rec.standIn[p.ID] = on
			s.records.append(rec.path, gamerecord.StandInLine(p.ID, on, rec.steps, now.UnixMilli()))
		}
	}
	if r.info.Status == "finished" {
		reason := gamerecord.EndFinished
		if rec.endedBy != "" {
			reason = rec.endedBy
		}
		s.closeRecord(r, reason, "", data)
	}
}

// closeRecord ends the room's record: the end line, then the final file
// (written off the lock). final is the game's last snapshot (nil: make one).
// Requires s.mu.
func (s *Server) closeRecord(r *room, reason, note string, final []byte) {
	s.closeTableRecord(r, reason, note, final)
	rec := r.rec
	if rec == nil {
		return
	}
	s.settleRecord(r, final)
	if final == nil {
		final, _ = r.game.Snapshot()
	}
	s.records.append(rec.path, gamerecord.EndLine(gamerecord.EndEvent{Reason: reason, Note: note, At: s.now().UnixMilli(), Final: final}))
	s.records.finalize(rec.path, reason, note)
	r.rec = nil
	s.markRoomsDirty()
}

// openRecord starts a room's record. initial is the game before its first
// step (the lobby; or the current state for a record that starts mid-game).
func (s *Server) openRecord(r *room, eg *engineGame, initial []byte, midGame bool) {
	startedAt := s.now()
	if midGame && r.startedAt > 0 {
		startedAt = time.UnixMilli(r.startedAt)
	}
	var settings struct {
		DiceMode string `json:"diceMode"`
	}
	settingsJSON, _ := json.Marshal(eg.g.Settings)
	_ = json.Unmarshal(settingsJSON, &settings)
	head := gamerecord.Header{
		ID:            s.records.newID(startedAt, r.code),
		Code:          r.code,
		GameType:      GameEstate,
		AppVersion:    s.cfg.Version,
		StartedAt:     startedAt.UnixMilli(),
		Room:          r.hosting(),
		ComputerSpeed: r.speedName(),
		DiceMode:      settings.DiceMode,
		Settings:      settingsJSON,
		StartsMidGame: midGame,
	}
	for i, p := range eg.g.Players {
		player := gamerecord.Player{ID: p.ID, Seat: i, Name: p.Name, Kind: p.Kind, Color: p.Color, Host: p.ID == eg.g.HostID}
		if m := r.meta[p.ID]; m != nil {
			player.Input, player.TokenKind, player.Difficulty = m.input, m.tokenKind, m.difficulty
			if r.table && m.tokenKind != "" {
				if c := s.tokenColor(m.tokenKind); c != "" {
					player.Color = c
				}
			}
		}
		if player.Input == "" {
			player.Input = r.playerInput(p.ID, p.Kind)
		}
		if p.Kind == engine.KindAI && player.Difficulty == "" {
			player.Difficulty = "normal"
		}
		head.Players = append(head.Players, player)
	}
	rec := &gamerecord.Record{Header: head, Initial: initial}
	live := &liveRecord{
		id:       head.ID,
		path:     s.records.journalPath(head.ID),
		lastHash: gamerecord.HashSnapshot(initial),
		standIn:  map[string]bool{},
	}
	r.rec = live
	s.claimRecord(head.ID)
	s.records.append(live.path, gamerecord.HeaderLine(rec))
	s.markRoomsDirty()
}

// claimRecord keeps a journal started during startup from being finished as
// an orphan the moment it reaches the disk.
func (s *Server) claimRecord(id string) {
	if s.recordClaims != nil {
		s.recordClaims[id] = true
	}
}

// hosting says how a room is played: from a PC table screen, a phone-hosted
// party game, or the classic client.
func (r *room) hosting() string {
	switch {
	case r.tableSession() != nil:
		return "table"
	case r.table:
		return "phone"
	}
	return "classic"
}

// actionVia names the input a person's action came from.
func (r *room) actionVia(sess *session, actor string, body *jsBody) string {
	if sess.role == "table" {
		if seatID, _ := body.exactString("seatId"); seatID == "" {
			return "table"
		}
	}
	return r.playerInput(actor, engine.KindHuman)
}

// resumeRecord reattaches a restored room's record (startup, s.mu held):
// the journal named in rooms.json, else the newest unclaimed journal of
// this room whose steps reach the restored game. A game still in play with
// no usable journal starts a new record from its current state.
func (s *Server) resumeRecord(r *room, recordID string, claimed map[string]bool) {
	eg, ok := s.recordable(r)
	if !ok || len(r.lastJSON) == 0 {
		return
	}
	hash := gamerecord.HashSnapshot(r.lastJSON)
	try := func(id string, mustMatch bool) bool {
		path := s.records.journalPath(id)
		j, err := gamerecord.ReadJournal(path)
		if err != nil {
			if recordID == id {
				s.log.Printf("room %s: record %s could not be read (%v)", r.code, id, err)
			}
			return false
		}
		steps := j.Record.Steps
		match := -1
		for i := len(steps) - 1; i >= 0; i-- {
			if steps[i].Hash == hash {
				match = i
				break
			}
		}
		if match < 0 && mustMatch {
			return false
		}
		if eg.g.Status == engine.StatusLobby && match < 0 {
			return false // a new lobby: that game was never saved as playing
		}
		claimed[id] = true
		live := &liveRecord{id: id, path: path, standIn: map[string]bool{}}
		rewrite := j.Damaged > 0 || j.End != nil
		if match >= 0 {
			if match+1 < len(steps) {
				s.log.Printf("room %s: record %s: %d step(s) after the saved game dropped", r.code, id, len(steps)-match-1)
				rewrite = true
			}
			j.Truncate(match + 1)
			live.lastHash = hash
		} else {
			// The journal is behind the saved game: continue after a resync.
			s.log.Printf("room %s: record %s is behind the saved game; resyncing", r.code, id)
			j.End = nil
			n := len(j.Record.Steps)
			j.Record.Steps = append(j.Record.Steps, gamerecord.Step{
				N: n, Kind: gamerecord.StepResync, At: eg.g.Now(), Version: int64(eg.g.Version), VersionAfter: int64(eg.g.Version),
				Hash: hash, State: append(json.RawMessage(nil), r.lastJSON...),
			})
			rewrite = true
			live.lastHash = hash
		}
		live.steps = len(j.Record.Steps)
		for _, e := range j.StandIn {
			live.standIn[e.PlayerID] = e.On
		}
		if rewrite {
			if err := gamerecord.WriteFileAtomic(path, j.Bytes()); err != nil {
				s.log.Printf("room %s: record %s: %v", r.code, id, err)
			}
		}
		r.rec = live
		return true
	}
	if recordID != "" && try(recordID, false) {
		return
	}
	for _, id := range s.records.liveIDs(r.code) {
		if !claimed[id] && try(id, true) {
			return
		}
	}
	if eg.g.Status == engine.StatusPlaying {
		// No journal reaches this game (saved before records existed, or
		// the journal was lost): record the rest of it.
		s.openRecord(r, eg, append([]byte(nil), r.lastJSON...), true)
	}
}

// finishOrphanRecords finalises journals no restored room claimed (startup).
func (s *Server) finishOrphanRecords(claimed map[string]bool) {
	for _, id := range s.records.liveIDs("") {
		if !claimed[id] {
			s.records.finalize(s.records.journalPath(id), gamerecord.EndExpired, "the room did not come back after a server restart")
		}
	}
}

// ---- recordStore: the disk side, off the server lock ----------------------------------

// recordStore owns <data>/games. append and finalize only queue work; one
// background goroutine writes, in order.
type recordStore struct {
	dir      string
	liveDir  string
	log      *log.Logger
	keep     int
	maxBytes int64

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []recordJob
	busy    bool
	closed  bool
	stopped chan struct{}
	ids     map[string]bool // record ids in use (finished or live)
}

type recordJob struct {
	path     string
	data     []byte // a line to append
	finalize bool
	reason   string
	note     string
}

func openRecordStore(dataDir string, logger *log.Logger, keep int, maxBytes int64) (*recordStore, error) {
	dir := filepath.Join(dataDir, recordsDirName)
	liveDir := filepath.Join(dir, liveRecordsDirName)
	if err := os.MkdirAll(liveDir, 0o700); err != nil {
		return nil, fmt.Errorf("game records: %w", err)
	}
	if keep <= 0 {
		keep = defaultRecordKeep
	}
	if maxBytes <= 0 {
		maxBytes = defaultRecordMaxLen
	}
	rs := &recordStore{dir: dir, liveDir: liveDir, log: logger, keep: keep, maxBytes: maxBytes, stopped: make(chan struct{}), ids: map[string]bool{}}
	rs.cond = sync.NewCond(&rs.mu)
	for _, name := range listNames(dir, ".json.gz") {
		rs.ids[strings.TrimSuffix(name, ".json.gz")] = true
	}
	for _, name := range listNames(liveDir, ".jsonl") {
		rs.ids[strings.TrimSuffix(name, ".jsonl")] = true
	}
	go rs.run()
	return rs, nil
}

func listNames(dir, suffix string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func (rs *recordStore) journalPath(id string) string { return filepath.Join(rs.liveDir, id+".jsonl") }

func (rs *recordStore) finalPath(id string) string { return filepath.Join(rs.dir, id+".json.gz") }

// newID names a record YYYY-MM-DD-HHMM-<code> (local time), unique.
func (rs *recordStore) newID(start time.Time, code string) string {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	base := start.Local().Format("2006-01-02-1504") + "-" + code
	id := base
	for n := 2; rs.ids[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	rs.ids[id] = true
	return id
}

// liveIDs lists the journals on disk, newest first; code filters by room.
func (rs *recordStore) liveIDs(code string) []string {
	var out []string
	for _, name := range listNames(rs.liveDir, ".jsonl") {
		id := strings.TrimSuffix(name, ".jsonl")
		if code == "" || strings.HasSuffix(id, "-"+code) || strings.Contains(id, "-"+code+"-") {
			out = append(out, id)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

func (rs *recordStore) push(job recordJob) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.closed {
		return
	}
	rs.queue = append(rs.queue, job)
	rs.cond.Broadcast()
}

// append queues a journal line.
func (rs *recordStore) append(path string, line []byte) {
	rs.push(recordJob{path: path, data: line})
}

// finalize queues turning a journal into its final record.
func (rs *recordStore) finalize(path, reason, note string) {
	rs.push(recordJob{path: path, finalize: true, reason: reason, note: note})
}

// waitIdle blocks until everything queued so far is on disk.
func (rs *recordStore) waitIdle() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for len(rs.queue) > 0 || rs.busy {
		rs.cond.Wait()
	}
}

// close writes what is queued and stops the writer. Journals of games still
// in play stay on disk for the next start.
func (rs *recordStore) close() {
	rs.mu.Lock()
	rs.closed = true
	rs.cond.Broadcast()
	rs.mu.Unlock()
	<-rs.stopped
}

func (rs *recordStore) run() {
	defer close(rs.stopped)
	for {
		rs.mu.Lock()
		for len(rs.queue) == 0 && !rs.closed {
			rs.cond.Wait()
		}
		if len(rs.queue) == 0 && rs.closed {
			rs.mu.Unlock()
			return
		}
		jobs := rs.queue
		rs.queue = nil
		rs.busy = true
		rs.mu.Unlock()

		rs.process(jobs)

		rs.mu.Lock()
		rs.busy = false
		rs.cond.Broadcast()
		rs.mu.Unlock()
	}
}

// process runs jobs in order, batching consecutive appends to one file.
func (rs *recordStore) process(jobs []recordJob) {
	for i := 0; i < len(jobs); {
		job := jobs[i]
		if job.finalize {
			rs.finish(job)
			i++
			continue
		}
		var batch []byte
		j := i
		for ; j < len(jobs) && !jobs[j].finalize && jobs[j].path == job.path; j++ {
			batch = append(batch, jobs[j].data...)
		}
		if err := appendFile(job.path, batch); err != nil {
			rs.log.Printf("game record %s: %v", filepath.Base(job.path), err)
		}
		i = j
	}
}

func appendFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// finish turns a journal into <id>.json.gz: replayed and checked, with the
// summaries filled in. The journal is removed once the record is written.
func (rs *recordStore) finish(job recordJob) {
	if gamerecord.IsTableJournal(job.path) {
		rs.finishTable(job)
		return
	}
	id := strings.TrimSuffix(filepath.Base(job.path), ".jsonl")
	j, err := gamerecord.ReadJournal(job.path)
	if err != nil {
		rs.log.Printf("game record %s: %v", id, err)
		return
	}
	rec := j.Finish()
	if rec.Outcome == nil {
		rec.Outcome = &gamerecord.Outcome{Reason: job.reason, Note: job.note}
	}
	if len(rec.Steps) == 0 {
		os.Remove(job.path) // nothing happened: not worth keeping
		return
	}
	gamerecord.Annotate(rec, job.reason)
	if !rec.Verified {
		rs.log.Printf("game record %s does not replay: %s", id, rec.VerifyError)
	}
	if err := gamerecord.Write(rs.finalPath(id), rec); err != nil {
		rs.log.Printf("game record %s: %v", id, err)
		return
	}
	os.Remove(job.path)
	rs.prune()
}

// prune keeps the newest finished records: at most keep files and maxBytes
// bytes, whichever is reached first. Newest means the latest start (the
// YYYY-MM-DD-HHMM a name starts with), then the latest written.
func (rs *recordStore) prune() {
	type file struct {
		name     string
		size     int64
		modified time.Time
	}
	var files []file
	for _, name := range listNames(rs.dir, ".json.gz") {
		info, err := os.Stat(filepath.Join(rs.dir, name))
		if err != nil {
			continue
		}
		files = append(files, file{name, info.Size(), info.ModTime()})
	}
	startKey := func(name string) string {
		if len(name) >= len("2006-01-02-1504") {
			return name[:len("2006-01-02-1504")]
		}
		return name
	}
	sort.SliceStable(files, func(i, j int) bool {
		a, b := startKey(files[i].name), startKey(files[j].name)
		if a != b {
			return a > b
		}
		return files[i].modified.After(files[j].modified)
	})
	var total int64
	for i, f := range files {
		total += f.size
		if i < rs.keep && total <= rs.maxBytes {
			continue
		}
		if os.Remove(filepath.Join(rs.dir, f.name)) == nil {
			rs.mu.Lock()
			delete(rs.ids, strings.TrimSuffix(f.name, ".json.gz"))
			rs.mu.Unlock()
		}
	}
}
