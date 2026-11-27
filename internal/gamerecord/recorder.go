package gamerecord

import (
	"encoding/json"
	"errors"

	"github.com/tdar200/estate-games/internal/engine"
)

// Recorder records a game played directly on an *engine.Game, for tools and
// tests (the server records its games itself, off its lock: see
// internal/server/recorder.go). Every change must go through the Recorder.
type Recorder struct {
	G   *engine.Game
	Rec *Record
}

// NewRecorder starts recording g, which must still be in the lobby; Start
// (or Action with a start) begins the game.
func NewRecorder(g *engine.Game, head Header) (*Recorder, error) {
	if g.Status != engine.StatusLobby {
		return nil, errors.New("gamerecord: the game has already started")
	}
	initial, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	if head.GameType == "" {
		head.GameType = "estate"
	}
	if head.Code == "" {
		head.Code = g.Code
	}
	if head.ID == "" {
		head.ID = head.Code
	}
	head.DiceMode = g.Settings.DiceMode
	head.Settings, _ = json.Marshal(g.Settings)
	if head.Players == nil {
		for i, p := range g.Players {
			head.Players = append(head.Players, Player{ID: p.ID, Seat: i, Name: p.Name, Kind: p.Kind, Color: p.Color, Host: p.ID == g.HostID})
		}
	}
	return &Recorder{G: g, Rec: &Record{Format: Format, FormatVersion: FormatVersion, Header: head, Initial: initial}}, nil
}

func (r *Recorder) lastHash() string {
	if n := len(r.Rec.Steps); n > 0 {
		return r.Rec.Steps[n-1].Hash
	}
	return HashSnapshot(r.Rec.Initial)
}

func (r *Recorder) push(st Step) {
	st.N = len(r.Rec.Steps)
	r.Rec.Steps = append(r.Rec.Steps, st)
	if r.Rec.StartedAt == 0 {
		r.Rec.StartedAt = st.At
	}
	r.Rec.EndedAt = st.At
}

// Action applies and records a raw JSON action; source is one of the
// Source* values (it may be empty).
func (r *Recorder) Action(playerID string, raw json.RawMessage, source string) error {
	st, record, err := PerformAction(r.G, playerID, raw, r.lastHash())
	if record {
		st.Source = source
		r.push(st)
	}
	return err
}

// Do is Action for a Go-built engine action.
func (r *Recorder) Do(playerID string, action engine.Action, source string) error {
	raw, err := json.Marshal(action)
	if err != nil {
		return err
	}
	return r.Action(playerID, raw, source)
}

// Order ends the turn-order countdown at now (at least its end time).
func (r *Recorder) Order(now int64) bool {
	if o := r.G.OpeningOrder; o != nil && o.CountdownEndsAt != nil {
		now = max(now, *o.CountdownEndsAt)
	}
	st, ok := CompleteOpeningOrder(r.G, now)
	if ok {
		st.Source = SourceServer
		r.push(st)
	}
	return ok
}

// End ends the game early (the richest player wins).
func (r *Recorder) End(by string) error {
	st, _, err := CaptureEnd(r.G, by)
	if err != nil {
		return err
	}
	st.Hash = StateHash(r.G)
	st.Source = SourceHost
	r.push(st)
	return nil
}

// Finish closes the record with reason (if the game is not over, it is still
// in play: pass EndExpired or similar) and fills in the summaries.
func (r *Recorder) Finish(reason string) *Record {
	if r.G.Status == engine.StatusFinished && reason == "" {
		reason = EndFinished
	}
	r.Rec.Outcome = &Outcome{Reason: reason}
	if data, err := json.Marshal(r.G); err == nil {
		r.Rec.Final = data
	}
	Annotate(r.Rec, reason)
	return r.Rec
}
