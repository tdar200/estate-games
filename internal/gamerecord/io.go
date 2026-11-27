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
	"path/filepath"
)

// Read loads a record from a .json.gz file (or plain .json).
func Read(path string) (*Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// Decode parses a record, gzipped or not.
func Decode(data []byte) (*Record, error) {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		plain, err := io.ReadAll(zr)
		if err != nil {
			return nil, err
		}
		data = plain
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	if rec.Format != Format {
		return nil, fmt.Errorf("not an Estate game record (format %q)", rec.Format)
	}
	if rec.FormatVersion > FormatVersion {
		return nil, fmt.Errorf("record format %d is newer than this program understands (%d)", rec.FormatVersion, FormatVersion)
	}
	return &rec, nil
}

// Encode returns the record as gzipped JSON.
func Encode(rec *Record) ([]byte, error) {
	rec.Format, rec.FormatVersion = Format, FormatVersion
	data, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Write saves the record gzipped at path, atomically.
func Write(path string, rec *Record) error {
	data, err := Encode(rec)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data)
}

// WriteFileAtomic writes data to a temp file next to path, syncs it and
// renames it into place.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// ---- Journal: the record of a game still being played ----------------------
//
// While a game runs the server appends one JSON line per event to
// <data>/games/live/<id>.jsonl: a header line, then step, standin and end
// lines. A crash loses at most a line being written (Read skips a damaged
// last line); the journal is turned into the final .json.gz when the game
// ends.

// Journal line types.
const (
	lineHeader  = "header"
	lineStep    = "step"
	lineStandIn = "standin"
	lineEnd     = "end"
)

type journalLine struct {
	Type     string          `json:"type"`
	Record   *Record         `json:"record,omitempty"`
	Step     *Step           `json:"step,omitempty"`
	PlayerID string          `json:"playerId,omitempty"`
	On       bool            `json:"on,omitempty"`
	StepN    int             `json:"at,omitempty"`
	Time     int64           `json:"t,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Note     string          `json:"note,omitempty"`
	Final    json.RawMessage `json:"final,omitempty"`
}

// StandInEvent is the computer starting (On) or stopping to play for an away
// person, before step Step.
type StandInEvent struct {
	PlayerID string
	On       bool
	Step     int
	At       int64
}

// EndEvent is the end of a game as the server saw it.
type EndEvent struct {
	Reason string
	Note   string
	At     int64
	Final  json.RawMessage
}

// Journal is a parsed journal.
type Journal struct {
	Record  *Record // header, initial state and steps
	StandIn []StandInEvent
	End     *EndEvent
	// Damaged counts lines that could not be read (a crash mid-write).
	Damaged int
}

func line(v journalLine) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		// Only unencodable floats could get here; keep the journal readable.
		data, _ = json.Marshal(journalLine{Type: "error", Note: err.Error()})
	}
	return append(data, '\n')
}

// HeaderLine is the first line of a journal: the record without steps.
func HeaderLine(rec *Record) []byte {
	head := *rec
	head.Format, head.FormatVersion = Format, FormatVersion
	head.Steps = nil
	return line(journalLine{Type: lineHeader, Record: &head})
}

// StepLine is one step.
func StepLine(st *Step) []byte { return line(journalLine{Type: lineStep, Step: st}) }

// StandInLine records the computer starting or stopping to play for playerID
// before step stepN.
func StandInLine(playerID string, on bool, stepN int, at int64) []byte {
	return line(journalLine{Type: lineStandIn, PlayerID: playerID, On: on, StepN: stepN, Time: at})
}

// EndLine records how the game ended; final is its last state.
func EndLine(e EndEvent) []byte {
	return line(journalLine{Type: lineEnd, Reason: e.Reason, Note: e.Note, Time: e.At, Final: e.Final})
}

// ReadJournal parses a journal file.
func ReadJournal(path string) (*Journal, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	j := &Journal{}
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

func (j *Journal) add(raw []byte) {
	var l journalLine
	if json.Unmarshal(raw, &l) != nil {
		j.Damaged++
		return
	}
	switch l.Type {
	case lineHeader:
		if l.Record != nil && j.Record == nil {
			j.Record = l.Record
			j.Record.Steps = []Step{}
		}
	case lineStep:
		if j.Record == nil || l.Step == nil || l.Step.N != len(j.Record.Steps) {
			j.Damaged++
			return
		}
		j.Record.Steps = append(j.Record.Steps, *l.Step)
	case lineStandIn:
		j.StandIn = append(j.StandIn, StandInEvent{PlayerID: l.PlayerID, On: l.On, Step: l.StepN, At: l.Time})
	case lineEnd:
		j.End = &EndEvent{Reason: l.Reason, Note: l.Note, At: l.Time, Final: l.Final}
	default:
		j.Damaged++
	}
}

// Truncate drops every step from n on (and the stand-in events after them).
func (j *Journal) Truncate(n int) {
	if n < len(j.Record.Steps) {
		j.Record.Steps = j.Record.Steps[:n]
	}
	kept := j.StandIn[:0]
	for _, e := range j.StandIn {
		if e.Step <= n {
			kept = append(kept, e)
		}
	}
	j.StandIn = kept
	j.End = nil
}

// Bytes encodes the journal again (after Truncate).
func (j *Journal) Bytes() []byte {
	var buf bytes.Buffer
	buf.Write(HeaderLine(j.Record))
	events := j.StandIn
	for i := range j.Record.Steps {
		for len(events) > 0 && events[0].Step <= i {
			buf.Write(StandInLine(events[0].PlayerID, events[0].On, events[0].Step, events[0].At))
			events = events[1:]
		}
		buf.Write(StepLine(&j.Record.Steps[i]))
	}
	for _, e := range events {
		buf.Write(StandInLine(e.PlayerID, e.On, e.Step, e.At))
	}
	if j.End != nil {
		buf.Write(EndLine(*j.End))
	}
	return buf.Bytes()
}

// Finish turns the journal into a record (not yet annotated): the stand-in
// periods, and the end if there is one.
func (j *Journal) Finish() *Record {
	rec := j.Record
	rec.Format, rec.FormatVersion = Format, FormatVersion
	rec.StandIns = nil
	open := map[string]int{}
	for _, e := range j.StandIn {
		i, isOpen := open[e.PlayerID]
		switch {
		case e.On && !isOpen:
			rec.StandIns = append(rec.StandIns, StandIn{PlayerID: e.PlayerID, FromStep: e.Step, ToStep: -1, From: e.At})
			open[e.PlayerID] = len(rec.StandIns) - 1
		case !e.On && isOpen:
			rec.StandIns[i].ToStep, rec.StandIns[i].To = e.Step, e.At
			delete(open, e.PlayerID)
		}
	}
	if j.End != nil {
		rec.Outcome = &Outcome{Reason: j.End.Reason, Note: j.End.Note}
		rec.EndedAt = j.End.At
		if len(j.End.Final) > 0 {
			rec.Final = j.End.Final
		}
	}
	if rec.EndedAt == 0 && len(rec.Steps) > 0 {
		rec.EndedAt = rec.Steps[len(rec.Steps)-1].At
	}
	return rec
}
