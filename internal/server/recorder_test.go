package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/gamerecord"
)

// recordingHarness runs the real engine and bots with a data directory, so
// every Estate game is recorded under <dir>/games.
func recordingHarness(t *testing.T, dir string, opts ...harnessOption) *harness {
	t.Helper()
	base := func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.Bots = RealBots
		c.AIDelay = time.Millisecond
		c.DataDir = dir
		c.SaveDelay = 10 * time.Millisecond
	}
	return newHarness(t, append([]harnessOption{base}, opts...)...)
}

// finishedRecords returns the finished records in <dir>/games, oldest first.
func finishedRecords(t *testing.T, h *harness, dir string) []string {
	t.Helper()
	h.srv.records.waitIdle()
	return listNames(filepath.Join(dir, recordsDirName), ".json.gz")
}

func readRecord(t *testing.T, dir, name string) *gamerecord.Record {
	t.Helper()
	rec, err := gamerecord.Read(filepath.Join(dir, recordsDirName, name))
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// liveRecordID is the record in progress of a room ("" if none).
func liveRecordID(h *harness, code string) string {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if r := h.srv.rooms[code]; r != nil && r.rec != nil {
		return r.rec.id
	}
	return ""
}

// person is a human seat a test plays: a phone (its token) or a table seat
// (the table's token plus seatId), from its own client address.
type person struct {
	playerID, token, seatID, ip string
}

func (h *harness) personAct(code string, p person, action map[string]any) response {
	body := map[string]any{"token": p.token, "action": action}
	if p.seatID != "" {
		body["seatId"] = p.seatID
	}
	return h.post("/api/rooms/"+code+"/action", body, fromIP(p.ip))
}

// personMove makes p's move if the game is waiting on them: a plain,
// cautious player (buys what they can afford, passes in auctions, refuses
// offers). It reports whether it acted.
func (h *harness) personMove(code string, p person, view *gameView) bool {
	if view.Phase == "determining_order" {
		if view.OpeningOrder != nil && slices.Contains(view.OpeningOrder.PendingIDs, p.playerID) {
			h.personAct(code, p, map[string]any{"type": "roll_for_order"})
			return true
		}
		return false
	}
	if ids, _ := actorsToMove(view); !slices.Contains(ids, p.playerID) {
		return false
	}
	tries := map[string][]string{
		"awaiting_roll":     {"roll"},
		"awaiting_purchase": {"buy", "decline"},
		"auction":           {"pass_auction"},
		"debt":              {"pay_debt", "bankrupt"},
		"trade":             {"reject_trade"},
		"loan_offer":        {"reject_loan"},
		"card_confirmation": {"confirm_card"},
	}[view.Phase]
	if tries == nil {
		tries = []string{"end_turn"}
	}
	for _, kind := range tries {
		if h.personAct(code, p, map[string]any{"type": kind}).status == http.StatusOK {
			return true
		}
	}
	time.Sleep(50 * time.Millisecond) // refused: don't hammer the server
	return true
}

// playUntil lets the computers and people play until done holds, the game
// ends, or the time runs out.
func playUntil(t *testing.T, h *harness, code string, people []person, timeout time.Duration, done func(*gameView) bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		view := h.view(code)
		if view.Status != "playing" || done(view) {
			return
		}
		for _, p := range people {
			h.personMove(code, p, view)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func endGame(t *testing.T, h *harness, code, token string) {
	t.Helper()
	if h.view(code).Status == "playing" {
		expectStatus(t, h.post("/api/rooms/"+code+"/end", map[string]any{"token": token}), http.StatusOK)
	}
}

// A whole game of computers is recorded and replays with no mismatch.
func TestBotGameRecordedAndReplayed(t *testing.T) {
	dir := t.TempDir()
	h := recordingHarness(t, dir)
	tb := h.createTable()
	for _, level := range []string{"easy", "normal", "normal", "easy"} {
		expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": level}), http.StatusOK)
	}
	h.dial("/ws/rooms/"+tb.code+"?token="+tb.token, nil).next(2 * time.Second)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	if liveRecordID(h, tb.code) == "" {
		t.Fatal("no record started")
	}
	playUntil(t, h, tb.code, nil, 90*time.Second, func(v *gameView) bool { return v.turnNumber() > 400 })
	endGame(t, h, tb.code, tb.token) // a long game is ended by the host
	names := finishedRecords(t, h, dir)
	if len(names) != 1 || !strings.HasSuffix(names[0], "-"+tb.code+".json.gz") {
		t.Fatalf("records %v", names)
	}
	if live := listNames(filepath.Join(dir, recordsDirName, liveRecordsDirName), ".jsonl"); len(live) != 0 {
		t.Fatalf("journal left behind: %v", live)
	}
	rec := readRecord(t, dir, names[0])
	if _, err := gamerecord.Verify(rec); err != nil {
		t.Fatalf("replay: %v", err)
	}
	final := h.view(tb.code)
	if !rec.Verified || rec.Outcome == nil || rec.Outcome.WinnerID != final.WinnerID || rec.Code != tb.code || rec.Room != "table" {
		t.Fatalf("record: verified %v (%s) outcome %+v room %s", rec.Verified, rec.VerifyError, rec.Outcome, rec.Room)
	}
	if want := map[bool]string{true: gamerecord.EndFinished, false: gamerecord.EndEnded}[len(rec.Outcome.BankruptcyOrder) == 3]; rec.Outcome.Reason != want {
		t.Fatalf("reason %q, want %q (bankrupt %v)", rec.Outcome.Reason, want, rec.Outcome.BankruptcyOrder)
	}
	levels := map[string]int{}
	for _, p := range rec.Players {
		if p.Kind != "ai" || p.Input != "ai" || p.TokenKind == "" {
			t.Fatalf("player %+v", p)
		}
		levels[p.Difficulty]++
	}
	if levels["easy"] != 2 || levels["normal"] != 2 {
		t.Fatalf("levels %v", levels)
	}
	sources := map[string]int{}
	thought := 0
	for _, st := range rec.Steps {
		sources[st.Source]++
		if st.ThinkMs > 0 {
			thought++
		}
	}
	if sources[gamerecord.SourceAI] < 50 || sources[gamerecord.SourcePlayer] != 1 || sources[gamerecord.SourceServer] != 1 || thought == 0 {
		t.Fatalf("sources %v, %d steps with think times", sources, thought)
	}
	if rec.Steps[0].ActionType() != "start" || rec.Steps[0].Decks == nil || rec.Steps[0].Via != "table" {
		t.Fatalf("first step %+v", rec.Steps[0])
	}
	if len(rec.TurnOrder) != 4 || rec.Turns < 20 || len(rec.Auctions)+len(rec.Trades) == 0 || len(rec.Outcome.Standings) != 4 {
		t.Fatalf("summaries: order %v turns %d auctions %d trades %d", rec.TurnOrder, rec.Turns, len(rec.Auctions), len(rec.Trades))
	}
	info, _ := os.Stat(filepath.Join(dir, recordsDirName, names[0]))
	t.Logf("%d turns, %d steps, %d trades, %d auctions: %d bytes", rec.Turns, len(rec.Steps), len(rec.Trades), len(rec.Auctions), info.Size())
}

// A game recorded across a server restart is one continuous record, and a
// journal that is ahead of (or behind) the saved game is put right.
func TestRecordAcrossRestart(t *testing.T) {
	for _, mode := range []string{"clean", "journal ahead", "journal behind", "journal lost"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			h := recordingHarness(t, dir)
			tb := h.createTable()
			alice := h.device("Alice", "key", "198.51.100.61")
			a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice", "tokenKind": "key"}, alice.opt())
			expectStatus(t, a, http.StatusOK)
			al := []person{{playerID: a.str("playerId"), token: a.str("token"), ip: "198.51.100.61"}}
			for _, level := range []string{"normal", "easy"} {
				expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": level}), http.StatusOK)
			}
			expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
			// Play a few turns, then stop at a still moment: Alice to roll.
			playUntil(t, h, tb.code, al, 30*time.Second, func(v *gameView) bool {
				return v.turnNumber() >= 6 && v.Phase == "awaiting_roll" && v.Players[v.CurrentIndex].ID == al[0].playerID
			})
			id := liveRecordID(h, tb.code)
			if id == "" {
				t.Fatal("no record in progress")
			}
			if err := h.srv.Close(); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(dir, recordsDirName, liveRecordsDirName, id+".jsonl")
			before, err := gamerecord.ReadJournal(journal)
			if err != nil {
				t.Fatal(err)
			}
			stepsBefore := len(before.Record.Steps)
			var saved savedRooms
			data, _ := os.ReadFile(filepath.Join(dir, roomsFileName))
			if json.Unmarshal(data, &saved) != nil || len(saved.Rooms) != 1 || saved.Rooms[0].RecordID != id {
				t.Fatalf("rooms.json does not name the record: %s", data)
			}
			raw, _ := os.ReadFile(journal)
			lines := bytes.SplitAfter(bytes.TrimRight(raw, "\n"), []byte("\n"))
			switch mode {
			case "journal ahead":
				// A step the saved game never saw (the server died between
				// writing the journal and saving rooms.json).
				last := before.Record.Steps[stepsBefore-1]
				last.N, last.Hash = stepsBefore, "00000000deadbeef"
				raw = append(append(raw, gamerecord.StepLine(&last)...), []byte(`{"type":"step","st`)...)
			case "journal behind":
				// The last two steps never reached the journal.
				raw = bytes.Join(lines[:len(lines)-2], nil)
			}
			if mode == "journal lost" {
				// No record of this game: it is recorded from where it is.
				if err := os.Remove(journal); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(journal, raw, 0o600); err != nil {
				t.Fatal(err)
			}

			h2 := recordingHarness(t, dir)
			if got := liveRecordID(h2, tb.code); got == "" || (got != id && mode != "journal lost") {
				t.Fatalf("record after restart %q, want %q", got, id)
			}
			rj := h2.post("/api/rooms/"+tb.code+"/rejoin", map[string]any{}, alice.opt())
			expectStatus(t, rj, http.StatusOK)
			al[0].token = rj.str("token") // the rejoin retires her old, unused session
			h2.dial("/ws/rooms/"+tb.code+"?token="+tb.token, nil).next(2 * time.Second)
			start := h2.view(tb.code).turnNumber()
			playUntil(t, h2, tb.code, al, 30*time.Second, func(v *gameView) bool { return v.turnNumber() >= start+4 })
			endGame(t, h2, tb.code, tb.token)
			names := finishedRecords(t, h2, dir)
			if len(names) != 1 || (names[0] != id+".json.gz" && mode != "journal lost") {
				t.Fatalf("records %v, want %s", names, id)
			}
			rec := readRecord(t, dir, names[0])
			if !rec.Verified {
				t.Fatalf("record does not replay: %s", rec.VerifyError)
			}
			if _, err := gamerecord.Verify(rec); err != nil {
				t.Fatal(err)
			}
			if mode == "journal lost" {
				if !rec.StartsMidGame || rec.Steps[0].ActionType() == "start" || len(rec.Players) != 3 {
					t.Fatalf("mid-game record: startsMidGame %v, first step %s", rec.StartsMidGame, rec.Steps[0].Action)
				}
				return
			}
			if len(rec.Steps) <= stepsBefore || rec.Steps[0].ActionType() != "start" {
				t.Fatalf("%d steps after the restart, %d before", len(rec.Steps), stepsBefore)
			}
			wantGaps := 0
			if mode == "journal behind" {
				wantGaps = 1
			}
			if len(rec.Gaps) != wantGaps {
				t.Fatalf("gaps %v, want %d", rec.Gaps, wantGaps)
			}
			if mode == "journal ahead" && rec.Steps[stepsBefore].Hash == "00000000deadbeef" {
				t.Fatal("the step the saved game never saw was kept")
			}
		})
	}
}

// Going back to the lobby for a rematch keeps the finished game's record; the
// rematch is recorded as a game of its own.
func TestRematchKeepsRecord(t *testing.T) {
	dir := t.TempDir()
	h := recordingHarness(t, dir)
	tb := h.createTable()
	alice := h.device("Alice", "umbrella", "198.51.100.91")
	a := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": "Alice"}, alice.opt())
	pad := h.addSeat(tb, "keyboard", "Maya", "key", nil)
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": "easy"}), http.StatusOK)
	people := []person{
		{playerID: a.str("playerId"), token: a.str("token"), ip: "198.51.100.91"},
		{playerID: dig(pad.body, "state", "localSeats", "0", "playerId").(string), token: tb.token, seatID: pad.str("seatId"), ip: "198.51.100.92"},
	}
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	playUntil(t, h, tb.code, people, 30*time.Second, func(v *gameView) bool { return v.turnNumber() >= 6 })
	endGame(t, h, tb.code, tb.token)
	names := finishedRecords(t, h, dir)
	if len(names) != 1 {
		t.Fatalf("records when the game ended: %v", names)
	}
	path := filepath.Join(dir, recordsDirName, names[0])
	saved, _ := os.ReadFile(path)
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/rematch", map[string]any{"tableToken": tb.token}), http.StatusOK)
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, saved) {
		t.Fatalf("the rematch touched the record: %v", err)
	}
	first := readRecord(t, dir, names[0])
	if !first.Verified || first.Outcome.Reason != gamerecord.EndEnded || first.Outcome.WinnerID == "" {
		t.Fatalf("first game: verified %v %s, outcome %+v", first.Verified, first.VerifyError, first.Outcome)
	}
	vias := map[string]bool{}
	for _, st := range first.Steps {
		if st.Source == gamerecord.SourcePlayer || st.Source == gamerecord.SourceHost {
			vias[st.Via] = true
		}
	}
	if !vias["phone"] || !vias["keyboard"] || !vias["table"] {
		t.Fatalf("inputs recorded %v", vias)
	}
	inputs := map[string]string{}
	for _, p := range first.Players {
		inputs[p.Name] = p.Input
	}
	if inputs["Alice"] != "phone" || inputs["Maya"] != "keyboard" || first.Room != "table" {
		t.Fatalf("players %+v", first.Players)
	}

	// The rematch: a new record, the old one untouched.
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	second := liveRecordID(h, tb.code)
	if second == "" || second+".json.gz" == names[0] {
		t.Fatalf("rematch record %q", second)
	}
	playUntil(t, h, tb.code, people, 20*time.Second, func(v *gameView) bool { return v.turnNumber() >= 3 })
	endGame(t, h, tb.code, tb.token)
	names = finishedRecords(t, h, dir)
	if len(names) != 2 || !slices.Contains(names, second+".json.gz") {
		t.Fatalf("records %v", names)
	}
	again := readRecord(t, dir, second+".json.gz")
	if !again.Verified || again.Outcome.Reason != gamerecord.EndEnded {
		t.Fatalf("rematch record: verified %v %s outcome %+v", again.Verified, again.VerifyError, again.Outcome)
	}
}

// A game whose room expires, or is closed while in play, is still recorded.
func TestAbandonedGameRecorded(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{}
	h := recordingHarness(t, dir, func(c *Config) { c.Clock = clock.Now })
	tb := h.createTable()
	for range 2 {
		expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), http.StatusOK)
	}
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	waitFor(t, "a few turns", 20*time.Second, func() bool { return h.view(tb.code).turnNumber() >= 3 })
	clock.Advance(roomTTL + time.Hour)
	h.srv.mu.Lock()
	h.srv.pruneRooms(h.srv.now())
	h.srv.mu.Unlock()
	names := finishedRecords(t, h, dir)
	if len(names) != 1 {
		t.Fatalf("records %v", names)
	}
	rec := readRecord(t, dir, names[0])
	if !rec.Verified || rec.Outcome.Reason != gamerecord.EndExpired || rec.Outcome.WinnerID != "" {
		t.Fatalf("verified %v %s outcome %+v", rec.Verified, rec.VerifyError, rec.Outcome)
	}
}

// Only the newest records are kept: at most recordKeep files and
// recordMaxBytes bytes.
func TestRecordRetention(t *testing.T) {
	dir := t.TempDir()
	h := recordingHarness(t, dir, func(c *Config) { c.recordKeep = 2 })
	tb := h.createTable()
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), http.StatusOK)
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), http.StatusOK)
	var ids []string
	for game := 0; game < 3; game++ {
		if game > 0 {
			expectStatus(t, h.post("/api/rooms/"+tb.code+"/rematch", map[string]any{"tableToken": tb.token}), http.StatusOK)
		}
		expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
		ids = append(ids, liveRecordID(h, tb.code))
		waitFor(t, "a turn", 10*time.Second, func() bool { return h.view(tb.code).turnNumber() >= 2 })
		endGame(t, h, tb.code, tb.token)
	}
	names := finishedRecords(t, h, dir)
	if len(names) != 2 || !slices.Contains(names, ids[1]+".json.gz") || !slices.Contains(names, ids[2]+".json.gz") {
		t.Fatalf("kept %v of %v", names, ids)
	}

	// By size: keep the newest files that fit.
	rs := h.srv.records
	sizes := []int{300, 500, 400, 200} // oldest first
	for i, size := range sizes {
		path := filepath.Join(rs.dir, "2030-01-0"+string(rune('1'+i))+"-1200-ZZZZZ.json.gz")
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range names {
		os.Remove(filepath.Join(rs.dir, name))
	}
	rs.keep, rs.maxBytes = 10, 650
	rs.prune()
	got := listNames(rs.dir, ".json.gz")
	if len(got) != 2 || got[0] != "2030-01-03-1200-ZZZZZ.json.gz" || got[1] != "2030-01-04-1200-ZZZZZ.json.gz" {
		t.Fatalf("kept by size %v", got)
	}
}

// Records never hold session tokens, device ids or secrets, or addresses.
func TestRecordsHoldNoSecrets(t *testing.T) {
	dir := t.TempDir()
	h := recordingHarness(t, dir)
	alice := h.device("Alice", "rocket", "198.51.100.71")
	bob := h.device("Bob", "teapot", "203.0.113.72")
	game := h.post("/api/games", map[string]any{"name": "Alice"}, alice.opt(), fromIP("198.51.100.71"))
	expectStatus(t, game, http.StatusCreated)
	code := game.str("code")
	b := h.post("/api/rooms/"+code+"/join", map[string]any{"name": "Bob"}, bob.opt(), fromIP("203.0.113.72"))
	expectStatus(t, b, http.StatusOK)
	expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": game.str("token"), "difficulty": "easy"}), http.StatusOK)
	watcher := h.post("/api/rooms/"+code+"/watch", map[string]any{"name": "Viv"}, fromIP("192.0.2.73"))
	expectStatus(t, watcher, http.StatusOK)
	expectStatus(t, h.act(code, game.str("token"), map[string]any{"type": "start", "token": game.str("token"), "deviceId": alice.id}), http.StatusOK)
	people := []person{
		{playerID: game.str("playerId"), token: game.str("token"), ip: "198.51.100.71"},
		{playerID: b.str("playerId"), token: b.str("token"), ip: "203.0.113.72"},
	}
	playUntil(t, h, code, people, 20*time.Second, func(v *gameView) bool { return v.turnNumber() >= 6 })
	id := liveRecordID(h, code)
	h.srv.records.waitIdle()
	live, err := os.ReadFile(filepath.Join(dir, recordsDirName, liveRecordsDirName, id+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	endGame(t, h, code, game.str("token"))
	names := finishedRecords(t, h, dir)
	if len(names) != 1 {
		t.Fatalf("records %v", names)
	}
	gz, _ := os.ReadFile(filepath.Join(dir, recordsDirName, names[0]))
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	final, _ := io.ReadAll(zr)
	rec := readRecord(t, dir, names[0])
	if !rec.Verified || rec.Room != "phone" {
		t.Fatalf("verified %v %s room %s", rec.Verified, rec.VerifyError, rec.Room)
	}
	secrets := []string{
		game.str("token"), b.str("token"), watcher.str("token"), alice.id, alice.secret, bob.id, bob.secret,
		"198.51.100.71", "203.0.113.72", "192.0.2.73", "127.0.0.1", `"deviceId"`, `"ip"`, `"token":`,
	}
	for name, data := range map[string][]byte{"journal": live, "record": final} {
		for _, secret := range secrets {
			if secret != "" && bytes.Contains(data, []byte(secret)) {
				t.Errorf("%s contains %q", name, secret)
			}
		}
	}
}
