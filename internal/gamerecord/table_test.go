package gamerecord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func pokerEvent(seq int64, body string) TableEvent {
	return TableEvent{Seq: seq, At: 1000 + seq, Event: json.RawMessage(body)}
}

func sampleTableJournal(t *testing.T, path string) {
	t.Helper()
	rec := &TableRecord{Header: Header{ID: "t", Code: "TEST1", GameType: "poker", Players: []Player{{ID: "a", Name: "Ann"}}}}
	lines := [][]byte{
		TableHeaderLine(rec),
		TablePlayerLine(Player{ID: "b", Seat: 1, Name: "Bob"}),
		TableEventLine(pokerEvent(1, `{"seq":1,"type":"handStart","handNumber":1}`)),
		TableHolesLine(HoleCards{Hand: 1, Cards: map[string][]string{"a": {"As", "Kd"}, "b": {"2c", "2d"}}}),
		TableEventLine(pokerEvent(2, `{"seq":2,"type":"action","playerId":"a","action":"raise","amount":50}`)),
		TableEventLine(pokerEvent(3, `{"seq":3,"type":"win","playerId":"a","amount":75}`)),
		TableEventLine(pokerEvent(4, `{"seq":4,"type":"handStart","handNumber":2}`)),
		TableHolesLine(HoleCards{Hand: 2, Cards: map[string][]string{"a": {"Qh", "Qs"}}}),
		TableStandInLine(TableStandIn{PlayerID: "b", On: true, Seq: 4, At: 1004}),
		TableEndLine(TableOutcome{Reason: EndEnded, WinnerID: "a"}, 2000, json.RawMessage(`{"over":true}`)),
	}
	var all []byte
	for _, l := range lines {
		all = append(all, l...)
	}
	if err := os.WriteFile(path, all, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTableJournalRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	sampleTableJournal(t, path)
	if !IsTableJournal(path) {
		t.Fatal("not seen as a table journal")
	}
	j, err := ReadTableJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := j.Finish()
	if j.Damaged != 0 || len(rec.Players) != 2 || len(rec.Events) != 4 || len(rec.Holes) != 2 || rec.Outcome.WinnerID != "a" || rec.EndedAt != 2000 {
		t.Fatalf("%+v", rec)
	}
	out := filepath.Join(t.TempDir(), "t.json.gz")
	if err := WriteTable(out, rec); err != nil {
		t.Fatal(err)
	}
	back, err := ReadTable(out)
	if err != nil {
		t.Fatal(err)
	}
	hands := PokerHands(back)
	if len(hands) != 2 || hands[0].Holes["b"][0] != "2c" || len(hands[0].Steps) != 1 || hands[0].Wins[0].Amount != 75 {
		t.Fatalf("%+v", hands)
	}
	if _, err := Read(out); err == nil {
		t.Fatal("a table record must not read as an Estate record")
	}
}

// An Estate journal is not mistaken for a table journal.
func TestIsTableJournalIgnoresEstateJournals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e.jsonl")
	if err := os.WriteFile(path, HeaderLine(&Record{Header: Header{ID: "e"}}), 0o600); err != nil {
		t.Fatal(err)
	}
	if IsTableJournal(path) {
		t.Fatal("an Estate journal read as a table journal")
	}
	if IsTableJournal(filepath.Join(t.TempDir(), "missing.jsonl")) {
		t.Fatal("a missing file read as a table journal")
	}
}

// After a crash the journal is cut back to the saved game, and reopened.
func TestTableJournalTruncate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	sampleTableJournal(t, path)
	j, err := ReadTableJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	j.Truncate(3)
	if len(j.Record.Events) != 3 || len(j.Record.Holes) != 1 || len(j.Record.StandIns) != 0 || j.End != nil {
		t.Fatalf("%+v", j.Record)
	}
	back := filepath.Join(t.TempDir(), "back.jsonl")
	if err := os.WriteFile(back, j.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := ReadTableJournal(back)
	if err != nil {
		t.Fatal(err)
	}
	if k.Damaged != 0 || len(k.Record.Events) != 3 || len(k.Record.Holes) != 1 || len(k.Record.Players) != 2 {
		t.Fatalf("damaged %d, %+v", k.Damaged, k.Record)
	}
}

func TestLudoStats(t *testing.T) {
	rec := &TableRecord{Header: Header{Players: []Player{{ID: "a"}, {ID: "b"}}}}
	for i, body := range []string{
		`{"type":"roll","playerId":"a","value":6}`,
		`{"type":"move","playerId":"a"}`,
		`{"type":"capture","playerId":"a","victimId":"b"}`,
		`{"type":"roll","playerId":"b","value":2}`,
		`{"type":"home","playerId":"a"}`,
	} {
		rec.Events = append(rec.Events, TableEvent{Seq: int64(i + 1), Event: json.RawMessage(body)})
	}
	got := fmt.Sprintf("%+v", LudoStats(rec))
	want := "[{PlayerID:a Rolls:1 Sixes:1 Moves:1 Captures:1 Captured:0 Home:1} {PlayerID:b Rolls:1 Sixes:0 Moves:0 Captures:0 Captured:1 Home:0}]"
	if got != want {
		t.Fatalf("got %s", got)
	}
}
