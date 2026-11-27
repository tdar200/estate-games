package server

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/gamerecord"
	"github.com/tdar200/estate-games/internal/poker/holdem"
)

func readTableRecord(t *testing.T, dir, name string) *gamerecord.TableRecord {
	t.Helper()
	rec, err := gamerecord.ReadTable(filepath.Join(dir, recordsDirName, name))
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func tableRecordID(h *harness, code string) string {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if r := h.srv.rooms[code]; r != nil && r.trec != nil {
		return r.trec.id
	}
	return ""
}

// startComputerPoker starts a PC poker cash game of computers, watched by
// the table screen so they play.
func startComputerPoker(t *testing.T, h *harness) (code, token string) {
	t.Helper()
	tb := h.post("/api/tables", map[string]any{"gameType": "poker"})
	expectStatus(t, tb, http.StatusCreated)
	code, token = tb.str("code"), tb.str("tableToken")
	for range 3 {
		expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": "easy"}), http.StatusOK)
	}
	// A cash game: three all-ins can end a tournament on the first hand.
	settings := dig(h.state(code, token), "settings").(map[string]any)
	settings["style"] = holdem.StyleCash
	expectStatus(t, h.act(code, token, map[string]any{"type": "settings", "settings": settings}), http.StatusOK)
	h.record(code, token)
	expectStatus(t, h.act(code, token, map[string]any{"type": "start"}), http.StatusOK)
	return code, token
}

func pokerHandsPlayed(h *harness, code string) int {
	n := 0
	h.withTable(code, func(t *holdem.Table) { n = t.HandNumber })
	return n
}

// describePoker says where a table is, for a test that timed out.
func describePoker(h *harness, code string) string {
	out := ""
	h.withTable(code, func(t *holdem.Table) {
		out = fmt.Sprintf("status %s phase %s hand %d waiting %+v", t.Status, t.Phase, t.HandNumber, t.Waiting())
		for _, p := range t.Players {
			out += fmt.Sprintf(" | %s stack %d busted %v out %v", p.Name, p.Stack, p.Busted, p.Out)
		}
	})
	return out
}

// A poker game is recorded from its first hand: who sat where, every hand's
// cards, shown or not, and how it ended.
func TestPokerRecorded(t *testing.T) {
	dir := t.TempDir()
	timing := fastTiming
	h := recordingHarness(t, dir, withPoker(&timing))
	code, token := startComputerPoker(t, h)
	if tableRecordID(h, code) == "" {
		t.Fatal("a poker game in play has no record")
	}
	waitFor(t, "a few hands", 30*time.Second, func() bool { return pokerHandsPlayed(h, code) >= 4 })
	expectStatus(t, h.post("/api/rooms/"+code+"/end", map[string]any{"token": token}), http.StatusOK)
	names := finishedRecords(t, h, dir)
	if len(names) != 1 {
		t.Fatalf("records %v", names)
	}
	rec := readTableRecord(t, dir, names[0])
	if rec.GameType != "poker" || rec.Code != code || rec.Room != "table" || rec.Outcome == nil || rec.Outcome.Reason != gamerecord.EndEnded {
		t.Fatalf("%s %s %s %+v", rec.GameType, rec.Code, rec.Room, rec.Outcome)
	}
	if len(rec.Players) != 3 || rec.Players[0].Difficulty != "easy" || len(rec.Initial) == 0 || len(rec.Final) == 0 {
		t.Fatalf("players %+v, initial %d, final %d", rec.Players, len(rec.Initial), len(rec.Final))
	}
	hands := gamerecord.PokerHands(rec)
	if len(hands) < 4 {
		t.Fatalf("%d hands", len(hands))
	}
	for _, hand := range hands {
		if len(hand.Holes) != 3 {
			t.Fatalf("hand %d has cards for %d players, want all 3", hand.N, len(hand.Holes))
		}
		for id, cards := range hand.Holes {
			if len(cards) != 2 {
				t.Fatalf("hand %d, %s: %v", hand.N, id, cards)
			}
		}
	}
	if len(hands[0].Steps) == 0 || len(hands[0].Wins) == 0 {
		t.Fatalf("first hand %+v", hands[0])
	}
	total := 0
	for _, s := range rec.Outcome.Standings {
		total += s.Chips
	}
	if len(rec.Outcome.Standings) != 3 || total == 0 {
		t.Fatalf("standings %+v", rec.Outcome.Standings)
	}
	if live := listNames(filepath.Join(dir, recordsDirName, liveRecordsDirName), ".jsonl"); len(live) != 0 {
		t.Fatalf("journals left behind: %v", live)
	}
}

// A lobby is not a game: nothing is recorded until it starts.
func TestTableLobbyNotRecorded(t *testing.T) {
	dir := t.TempDir()
	h := recordingHarness(t, dir, withPoker(nil))
	tb := h.post("/api/tables", map[string]any{"gameType": "poker"})
	expectStatus(t, tb, http.StatusCreated)
	if id := tableRecordID(h, tb.str("code")); id != "" {
		t.Fatalf("a lobby recorded as %s", id)
	}
	h.srv.records.waitIdle()
	if live := listNames(filepath.Join(dir, recordsDirName, liveRecordsDirName), ".jsonl"); len(live) != 0 {
		t.Fatalf("journals %v", live)
	}
}

// A whole Ludo game, then a rematch on the same table gets its own record.
func TestLudoRecorded(t *testing.T) {
	dir := t.TempDir()
	h := recordingHarness(t, dir, withLudo(2))
	tb := h.post("/api/tables", map[string]any{"gameType": "ludo"})
	code, token := tb.str("code"), tb.str("tableToken")
	for _, level := range []string{"easy", "normal", "hard"} {
		expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token, "difficulty": level}), http.StatusOK)
	}
	expectStatus(t, h.act(code, token, map[string]any{"type": "settings", "settings": map[string]any{"pieces": 2}}), http.StatusOK)
	watch := h.record(code, token)
	expectStatus(t, h.act(code, token, map[string]any{"type": "start"}), http.StatusOK)
	finished := func() bool {
		st := watch.state()
		return st != nil && st["status"] == "finished"
	}
	waitFor(t, "the computers to finish", 60*time.Second, finished)
	names := finishedRecords(t, h, dir)
	if len(names) != 1 {
		t.Fatalf("records %v", names)
	}
	rec := readTableRecord(t, dir, names[0])
	if rec.GameType != "ludo" || rec.Outcome.Reason != gamerecord.EndFinished || rec.Outcome.WinnerID == "" {
		t.Fatalf("%s %+v", rec.GameType, rec.Outcome)
	}
	places := map[int]bool{}
	for _, s := range rec.Outcome.Standings {
		places[s.Place] = true
	}
	if len(rec.Outcome.Standings) != 3 || !places[1] {
		t.Fatalf("standings %+v", rec.Outcome.Standings)
	}
	stats := gamerecord.LudoStats(rec)
	if len(stats) != 3 {
		t.Fatalf("stats %+v", stats)
	}
	home := 0
	for _, s := range stats {
		if s.Rolls == 0 {
			t.Fatalf("no rolls: %+v", stats)
		}
		home += s.Home
	}
	if home < 2 {
		t.Fatalf("%d pieces home: %+v", home, stats)
	}
	// The rematch's lobby records nothing; its game does, under a new name.
	expectStatus(t, h.post("/api/rooms/"+code+"/rematch", map[string]any{"token": token}), http.StatusOK)
	if id := tableRecordID(h, code); id != "" {
		t.Fatalf("the rematch lobby is recorded as %s", id)
	}
	expectStatus(t, h.act(code, token, map[string]any{"type": "start"}), http.StatusOK)
	id := tableRecordID(h, code)
	if id == "" || id+".json.gz" == names[0] {
		t.Fatalf("rematch record %q after %q", id, names[0])
	}
}

// A restart keeps one continuous record, with no event twice.
func TestTableRecordAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	timing := fastTiming
	h := recordingHarness(t, dir, withPoker(&timing))
	code, token := startComputerPoker(t, h)
	waitFor(t, "a few hands", 30*time.Second, func() bool { return pokerHandsPlayed(h, code) >= 3 })
	id := tableRecordID(h, code)
	if err := h.srv.Close(); err != nil {
		t.Fatal(err)
	}
	h2 := recordingHarness(t, dir, withPoker(&timing))
	if got := tableRecordID(h2, code); got != id {
		t.Fatalf("restored record %q, want %q", got, id)
	}
	h2.record(code, token)
	first := pokerHandsPlayed(h2, code)
	waitFor(t, "more hands", 30*time.Second, func() bool { return pokerHandsPlayed(h2, code) >= first+3 })
	expectStatus(t, h2.post("/api/rooms/"+code+"/end", map[string]any{"token": token}), http.StatusOK)
	names := finishedRecords(t, h2, dir)
	if len(names) != 1 || names[0] != id+".json.gz" {
		t.Fatalf("records %v, want %s", names, id)
	}
	rec := readTableRecord(t, dir, names[0])
	seen := map[int64]bool{}
	var last int64
	for _, e := range rec.Events {
		if seen[e.Seq] || e.Seq <= last {
			t.Fatalf("event %d out of order or repeated", e.Seq)
		}
		seen[e.Seq], last = true, e.Seq
	}
	if hands := gamerecord.PokerHands(rec); hands[len(hands)-1].N < first+3 {
		t.Fatalf("the record stops at hand %d", hands[len(hands)-1].N)
	}
}

// A journal ahead of the saved game (a crash between the two) is cut back,
// and a game whose journal is gone records the rest of it.
func TestTableRecordCrashRecovery(t *testing.T) {
	for _, mode := range []string{"journal ahead", "journal lost"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			timing := fastTiming
			h := recordingHarness(t, dir, withPoker(&timing))
			code, token := startComputerPoker(t, h)
			t.Cleanup(func() {
				if t.Failed() {
					t.Log(describePoker(h, code))
				}
			})
			waitFor(t, "a hand", 8*time.Second, func() bool { return pokerHandsPlayed(h, code) >= 2 })
			id := tableRecordID(h, code)
			if err := h.srv.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, recordsDirName, liveRecordsDirName, id+".jsonl")
			if mode == "journal lost" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				ahead := gamerecord.TableEventLine(gamerecord.TableEvent{Seq: 1 << 40, At: 1, Event: json.RawMessage(`{"type":"ghost"}`)})
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				f.Write(ahead)
				f.Close()
			}
			h2 := recordingHarness(t, dir, withPoker(&timing))
			got := tableRecordID(h2, code)
			if got == "" {
				t.Fatal("no record after the restart")
			}
			h2.record(code, token)
			expectStatus(t, h2.post("/api/rooms/"+code+"/end", map[string]any{"token": token}), http.StatusOK)
			names := finishedRecords(t, h2, dir)
			if len(names) != 1 {
				t.Fatalf("records %v", names)
			}
			rec := readTableRecord(t, dir, names[0])
			if rec.StartsMidGame != (mode == "journal lost") {
				t.Fatalf("startsMidGame %v", rec.StartsMidGame)
			}
			for _, e := range rec.Events {
				if e.Seq == 1<<40 {
					t.Fatal("the event after the saved game was kept")
				}
			}
		})
	}
}

// A game that dies with its room is still kept.
func TestTableRecordKeptWhenRoomCloses(t *testing.T) {
	dir := t.TempDir()
	timing := fastTiming
	h := recordingHarness(t, dir, withPoker(&timing))
	code, _ := startComputerPoker(t, h)
	waitFor(t, "the first hand", 30*time.Second, func() bool { return pokerHandsPlayed(h, code) >= 1 })
	h.srv.mu.Lock()
	h.srv.deleteRoom(code, h.srv.rooms[code], gamerecord.EndExpired)
	h.srv.mu.Unlock()
	names := finishedRecords(t, h, dir)
	if len(names) != 1 {
		t.Fatalf("records %v", names)
	}
	if rec := readTableRecord(t, dir, names[0]); rec.Outcome.Reason != gamerecord.EndExpired {
		t.Fatalf("%+v", rec.Outcome)
	}
}

// Records hold what the game holds, never sessions, devices or addresses.
func TestTableRecordsHoldNoSecrets(t *testing.T) {
	dir := t.TempDir()
	timing := fastTiming
	h := recordingHarness(t, dir, withPoker(&timing))
	tb := h.post("/api/tables", map[string]any{"gameType": "poker"})
	code, token := tb.str("code"), tb.str("tableToken")
	ann := h.device("Ann", "rocket", "198.51.100.81")
	joined := h.post("/api/rooms/"+code+"/join", map[string]any{"name": "Ann"}, ann.opt())
	expectStatus(t, joined, http.StatusOK)
	expectStatus(t, h.post("/api/rooms/"+code+"/ai", map[string]any{"token": token}), http.StatusOK)
	expectStatus(t, h.act(code, token, map[string]any{"type": "start"}), http.StatusOK)
	expectStatus(t, h.post("/api/rooms/"+code+"/end", map[string]any{"token": token}), http.StatusOK)
	names := finishedRecords(t, h, dir)
	if len(names) != 1 {
		t.Fatalf("records %v", names)
	}
	f, err := os.Open(filepath.Join(dir, recordsDirName, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{token, joined.str("token"), "198.51.100.81"} {
		if strings.Contains(string(plain), secret) {
			t.Fatalf("the record holds %q", secret)
		}
	}
}

// Switching games in the lobby closes the empty table it leaves, and nothing
// else: not one with someone seated, and not someone else's.
func TestNewTableClosesTheEmptyOne(t *testing.T) {
	h := newHarness(t, withPoker(nil), withLudo(1))
	first := h.post("/api/tables", map[string]any{"gameType": "poker"})
	expectStatus(t, first, http.StatusCreated)
	code, token := first.str("code"), first.str("tableToken")
	exists := func(c string) bool {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		return h.srv.rooms[c] != nil
	}
	wrongToken := h.post("/api/tables", map[string]any{"gameType": "ludo", "replaceCode": code, "replaceToken": "nope"})
	expectStatus(t, wrongToken, http.StatusCreated)
	if !exists(code) {
		t.Fatal("a wrong token closed the table")
	}
	second := h.post("/api/tables", map[string]any{"gameType": "ludo", "replaceCode": code, "replaceToken": token})
	expectStatus(t, second, http.StatusCreated)
	if exists(code) {
		t.Fatal("the empty table was left behind")
	}
	// Someone has sat down: the table stays.
	busy := second.str("code")
	ann := h.device("Ann", "rocket", "198.51.100.91")
	expectStatus(t, h.post("/api/rooms/"+busy+"/join", map[string]any{"name": "Ann"}, ann.opt()), http.StatusOK)
	third := h.post("/api/tables", map[string]any{"gameType": "poker", "replaceCode": busy, "replaceToken": second.str("tableToken")})
	expectStatus(t, third, http.StatusCreated)
	if !exists(busy) {
		t.Fatal("a table with someone seated was closed")
	}
}
