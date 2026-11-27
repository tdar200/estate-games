package gamerecord

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// Poker, Ludo, quiz and closest records (docs/GAME-RECORDS.md). An Estate record replays
// through the rules engine; these are the game's own event stream instead,
// with the things the events leave out: every player's hole cards, each
// seat's kind and how the game ended. They are for looking back at a game,
// so nothing replays them.

// TableFormat identifies a poker, Ludo, quiz or closest record file.
const (
	TableFormat        = "estate-table-record"
	TableFormatVersion = 1
)

// TableRecord is a poker, Ludo, quiz or closest game.
type TableRecord struct {
	Format        string `json:"format"`
	FormatVersion int    `json:"formatVersion"`
	Header
	// Initial is the game when the record starts (for poker, with the deck
	// and shuffle state).
	Initial json.RawMessage `json:"initial,omitempty"`
	// Events are the engine's events, in order.
	Events []TableEvent `json:"events"`
	// Holes are the hole cards of each poker hand, shown or not.
	Holes    []HoleCards     `json:"holes,omitempty"`
	StandIns []TableStandIn  `json:"standIns,omitempty"`
	Outcome  *TableOutcome   `json:"outcome,omitempty"`
	Final    json.RawMessage `json:"final,omitempty"`
}

// TableEvent is one engine event: t is when it was recorded, in Unix
// milliseconds, and event is the event as the screens get it.
type TableEvent struct {
	Seq   int64           `json:"seq"`
	At    int64           `json:"t"`
	Event json.RawMessage `json:"event"`
}

// HoleCards is every dealt player's cards in one poker hand.
type HoleCards struct {
	Hand  int                 `json:"hand"`
	Cards map[string][]string `json:"cards"`
}

// TableStandIn is the computer starting (On) or stopping to play for an away
// person, after the event numbered Seq.
type TableStandIn struct {
	PlayerID string `json:"playerId"`
	On       bool   `json:"on"`
	Seq      int64  `json:"seq"`
	At       int64  `json:"t"`
}

// TableOutcome is how the game ended.
type TableOutcome struct {
	Reason    string          `json:"reason"` // an End* constant
	Note      string          `json:"note,omitempty"`
	WinnerID  string          `json:"winnerId,omitempty"`
	Standings []TableStanding `json:"standings,omitempty"`
}

// TableStanding is one player at the end. Place is 0 for a game cut short
// with the player still in; Chips is their poker stack.
type TableStanding struct {
	PlayerID string `json:"playerId"`
	Place    int    `json:"place,omitempty"`
	Chips    int    `json:"chips,omitempty"`
	Out      bool   `json:"out,omitempty"`
}

// Table journal line types. A journal starts with lineTableHeader, which is
// how the server tells it from an Estate journal.
const (
	lineTableHeader = "tableHeader"
	lineTableEvent  = "event"
	lineTableHoles  = "holes"
	lineTablePlayer = "player"
	lineTableStand  = "standin"
	lineTableEnd    = "tableEnd"
)

type tableLine struct {
	Type    string          `json:"type"`
	Record  *TableRecord    `json:"record,omitempty"`
	Event   *TableEvent     `json:"e,omitempty"`
	Holes   *HoleCards      `json:"holes,omitempty"`
	Player  *Player         `json:"player,omitempty"`
	Stand   *TableStandIn   `json:"stand,omitempty"`
	Outcome *TableOutcome   `json:"outcome,omitempty"`
	Time    int64           `json:"t,omitempty"`
	Final   json.RawMessage `json:"final,omitempty"`
}

func tableJournalLine(v tableLine) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		data, _ = json.Marshal(tableLine{Type: "error"})
	}
	return append(data, '\n')
}

// TableHeaderLine is the first line of a table journal.
func TableHeaderLine(rec *TableRecord) []byte {
	head := *rec
	head.Format, head.FormatVersion = TableFormat, TableFormatVersion
	head.Events = nil
	return tableJournalLine(tableLine{Type: lineTableHeader, Record: &head})
}

// TableEventLine is one engine event.
func TableEventLine(e TableEvent) []byte {
	return tableJournalLine(tableLine{Type: lineTableEvent, Event: &e})
}

// TableHolesLine is one poker hand's hole cards.
func TableHolesLine(h HoleCards) []byte {
	return tableJournalLine(tableLine{Type: lineTableHoles, Holes: &h})
}

// TablePlayerLine adds a player who sat down after the header was written.
func TablePlayerLine(p Player) []byte {
	return tableJournalLine(tableLine{Type: lineTablePlayer, Player: &p})
}

// TableStandInLine records the computer starting or stopping to play for
// someone.
func TableStandInLine(s TableStandIn) []byte {
	return tableJournalLine(tableLine{Type: lineTableStand, Stand: &s})
}

// TableEndLine records how the game ended; final is its last state.
func TableEndLine(o TableOutcome, at int64, final json.RawMessage) []byte {
	return tableJournalLine(tableLine{Type: lineTableEnd, Outcome: &o, Time: at, Final: final})
}

// IsTableJournal reports whether a journal is a poker, Ludo, quiz or closest game's.
func IsTableJournal(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	// The header can be long, so read only as far as its type.
	head := make([]byte, 64)
	n, _ := io.ReadFull(f, head)
	return bytes.HasPrefix(head[:n], []byte(`{"type":"`+lineTableHeader+`"`))
}

// TableJournal is a parsed table journal.
type TableJournal struct {
	Record  *TableRecord
	Damaged int
	End     *TableOutcome
	EndedAt int64
	Final   json.RawMessage
}

// ReadTableJournal parses a table journal file.
func ReadTableJournal(path string) (*TableJournal, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	j := &TableJournal{}
	reader := bufio.NewReaderSize(f, 1<<16)
	for {
		raw, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(raw)) > 0 {
			j.add(raw)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if j.Record == nil {
		return nil, errors.New("journal has no header")
	}
	return j, nil
}

func (j *TableJournal) add(raw []byte) {
	var l tableLine
	if json.Unmarshal(raw, &l) != nil {
		j.Damaged++
		return
	}
	if l.Type != lineTableHeader && j.Record == nil {
		j.Damaged++
		return
	}
	switch {
	case l.Type == lineTableHeader && l.Record != nil && j.Record == nil:
		j.Record = l.Record
		j.Record.Events = []TableEvent{}
	case l.Type == lineTableEvent && l.Event != nil:
		j.Record.Events = append(j.Record.Events, *l.Event)
	case l.Type == lineTableHoles && l.Holes != nil:
		j.Record.Holes = append(j.Record.Holes, *l.Holes)
	case l.Type == lineTablePlayer && l.Player != nil:
		j.Record.Players = append(j.Record.Players, *l.Player)
	case l.Type == lineTableStand && l.Stand != nil:
		j.Record.StandIns = append(j.Record.StandIns, *l.Stand)
	case l.Type == lineTableEnd && l.Outcome != nil:
		j.End, j.EndedAt, j.Final = l.Outcome, l.Time, l.Final
	default:
		j.Damaged++
	}
}

// Truncate cuts the journal back to the game saved at event seq (a crash can
// leave the journal ahead of the saved game) and reopens it.
func (j *TableJournal) Truncate(seq int64) {
	rec := j.Record
	keep := sort.Search(len(rec.Events), func(i int) bool { return rec.Events[i].Seq > seq })
	rec.Events = rec.Events[:keep]
	hand := 0
	for _, e := range rec.Events {
		if n := eventFields(e).HandNumber; n > hand {
			hand = n
		}
	}
	holes := rec.Holes[:0]
	for _, h := range rec.Holes {
		if h.Hand <= hand {
			holes = append(holes, h)
		}
	}
	rec.Holes = holes
	stands := rec.StandIns[:0]
	for _, s := range rec.StandIns {
		if s.Seq <= seq {
			stands = append(stands, s)
		}
	}
	rec.StandIns = stands
	j.End, j.Final, j.EndedAt = nil, nil, 0
}

// Bytes encodes the journal again (after Truncate).
func (j *TableJournal) Bytes() []byte {
	var buf bytes.Buffer
	head := *j.Record
	head.Holes, head.StandIns = nil, nil
	buf.Write(TableHeaderLine(&head))
	for _, h := range j.Record.Holes {
		buf.Write(TableHolesLine(h))
	}
	for _, s := range j.Record.StandIns {
		buf.Write(TableStandInLine(s))
	}
	for _, e := range j.Record.Events {
		buf.Write(TableEventLine(e))
	}
	if j.End != nil {
		buf.Write(TableEndLine(*j.End, j.EndedAt, j.Final))
	}
	return buf.Bytes()
}

// Finish turns the journal into a record.
func (j *TableJournal) Finish() *TableRecord {
	rec := j.Record
	rec.Format, rec.FormatVersion = TableFormat, TableFormatVersion
	rec.Outcome = j.End
	rec.EndedAt = j.EndedAt
	if rec.EndedAt == 0 && len(rec.Events) > 0 {
		rec.EndedAt = rec.Events[len(rec.Events)-1].At
	}
	rec.Final = j.Final
	return rec
}

// WriteTable saves the record gzipped at path, atomically.
func WriteTable(path string, rec *TableRecord) error {
	rec.Format, rec.FormatVersion = TableFormat, TableFormatVersion
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err := zw.Write(data); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return WriteFileAtomic(path, buf.Bytes())
}

// ReadTable reads a finished poker, Ludo, quiz or closest record.
func ReadTable(path string) (*TableRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		if data, err = io.ReadAll(zr); err != nil {
			return nil, err
		}
	}
	var rec TableRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	if rec.Format != TableFormat {
		return nil, fmt.Errorf("not a poker, Ludo, quiz or closest record (format %q)", rec.Format)
	}
	if rec.FormatVersion > TableFormatVersion {
		return nil, fmt.Errorf("record format %d is newer than this program understands (%d)", rec.FormatVersion, TableFormatVersion)
	}
	return &rec, nil
}

// eventBody holds the fields of poker, Ludo, quiz and closest events that summaries read.
type eventBody struct {
	Type       string   `json:"type"`
	HandNumber int      `json:"handNumber"`
	PlayerID   string   `json:"playerId"`
	Action     string   `json:"action"`
	Amount     int      `json:"amount"`
	To         int      `json:"to"`
	AllIn      bool     `json:"allIn"`
	Street     string   `json:"street"`
	Cards      []string `json:"cards"`
	HandName   string   `json:"handName"`
	Value      int      `json:"value"`
	VictimID   string   `json:"victimId"`
	Place      int      `json:"place"`
}

func eventFields(e TableEvent) eventBody {
	var b eventBody
	_ = json.Unmarshal(e.Event, &b)
	return b
}

// PokerStep is one line of a hand's history.
type PokerStep struct {
	PlayerID string
	Action   string // fold, check, call, bet, raise, or a forced bet
	Amount   int
	AllIn    bool
}

// PokerHand is one hand, rebuilt from a record's events.
type PokerHand struct {
	N     int
	Holes map[string][]string
	Board []string
	Steps []PokerStep
	Wins  []PokerWin
}

// PokerWin is a pot won.
type PokerWin struct {
	PlayerID string
	Amount   int
	HandName string
}

// PokerHands rebuilds a poker record's hands.
func PokerHands(rec *TableRecord) []PokerHand {
	holes := map[int]map[string][]string{}
	for _, h := range rec.Holes {
		holes[h.Hand] = h.Cards
	}
	var hands []PokerHand
	current := func() *PokerHand {
		if len(hands) == 0 {
			return nil
		}
		return &hands[len(hands)-1]
	}
	for _, e := range rec.Events {
		ev := eventFields(e)
		if ev.Type == "handStart" {
			hands = append(hands, PokerHand{N: ev.HandNumber, Holes: holes[ev.HandNumber]})
			continue
		}
		h := current()
		if h == nil {
			continue
		}
		switch ev.Type {
		case "post", "action":
			h.Steps = append(h.Steps, PokerStep{PlayerID: ev.PlayerID, Action: ev.Action, Amount: ev.Amount, AllIn: ev.AllIn})
		case "deal":
			h.Board = append(h.Board, ev.Cards...)
		case "win":
			h.Wins = append(h.Wins, PokerWin{PlayerID: ev.PlayerID, Amount: ev.Amount, HandName: ev.HandName})
		}
	}
	return hands
}

// LudoStat is one player's numbers in a Ludo record.
type LudoStat struct {
	PlayerID string
	Rolls    int
	Sixes    int
	Moves    int
	Captures int
	Captured int // own pieces sent back
	Home     int // pieces brought home
}

// LudoStats counts each player's rolls, captures and pieces home.
func LudoStats(rec *TableRecord) []LudoStat {
	stats := map[string]*LudoStat{}
	order := []string{}
	of := func(id string) *LudoStat {
		if stats[id] == nil {
			stats[id] = &LudoStat{PlayerID: id}
			order = append(order, id)
		}
		return stats[id]
	}
	for _, p := range rec.Players {
		of(p.ID)
	}
	for _, e := range rec.Events {
		ev := eventFields(e)
		switch ev.Type {
		case "roll":
			s := of(ev.PlayerID)
			s.Rolls++
			if ev.Value == 6 {
				s.Sixes++
			}
		case "move":
			of(ev.PlayerID).Moves++
		case "capture":
			of(ev.PlayerID).Captures++
			if ev.VictimID != "" {
				of(ev.VictimID).Captured++
			}
		case "home":
			of(ev.PlayerID).Home++
		}
	}
	out := make([]LudoStat, 0, len(order))
	for _, id := range order {
		out = append(out, *stats[id])
	}
	return out
}
