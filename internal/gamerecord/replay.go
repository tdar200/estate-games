package gamerecord

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/tdar200/estate-games/internal/engine"
)

// Mismatch is the first difference between a replay and its record.
type Mismatch struct {
	Step int    // index of the step (-1: the initial state or the record itself)
	Kind string // the step's kind
	What string
}

func (m *Mismatch) Error() string {
	if m.Step < 0 {
		return "record: " + m.What
	}
	return fmt.Sprintf("step %d (%s): %s", m.Step, m.Kind, m.What)
}

// Replayer applies a record's steps to a fresh copy of its initial state,
// checking each one against what was recorded.
type Replayer struct {
	rec *Record
	// G is the replayed game. A resync step replaces it with a new *Game.
	G    *engine.Game
	next int
}

// NewReplayer loads the record's initial state.
func NewReplayer(rec *Record) (*Replayer, error) {
	if rec.Format != "" && rec.Format != Format {
		return nil, &Mismatch{Step: -1, What: fmt.Sprintf("not a game record (format %q)", rec.Format)}
	}
	if rec.FormatVersion > FormatVersion {
		return nil, &Mismatch{Step: -1, What: fmt.Sprintf("format version %d is newer than this reader (%d)", rec.FormatVersion, FormatVersion)}
	}
	g, err := LoadGame(rec.Initial)
	if err != nil {
		return nil, &Mismatch{Step: -1, What: "initial state: " + err.Error()}
	}
	return &Replayer{rec: rec, G: g}, nil
}

// LoadGame decodes an engine game JSON.
func LoadGame(data json.RawMessage) (*engine.Game, error) {
	if len(data) == 0 {
		return nil, errors.New("missing")
	}
	var g engine.Game
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// Next is the index of the step the next call to Step applies.
func (r *Replayer) Next() int { return r.next }

// Done reports whether every step has been applied.
func (r *Replayer) Done() bool { return r.next >= len(r.rec.Steps) }

// Step applies the next step and checks it. It returns io.EOF after the last
// step, or a *Mismatch.
func (r *Replayer) Step() (*Step, error) {
	if r.next >= len(r.rec.Steps) {
		return nil, io.EOF
	}
	st := &r.rec.Steps[r.next]
	if st.N != r.next {
		return st, &Mismatch{Step: r.next, Kind: st.Kind, What: fmt.Sprintf("step is numbered %d", st.N)}
	}
	if err := r.apply(st); err != nil {
		return st, err
	}
	r.next++
	return st, nil
}

// replayRNG returns a step's recorded random numbers in order.
type replayRNG struct {
	vals    []float64
	used    int
	overrun bool
}

func (r *replayRNG) Float64() float64 {
	if r.used >= len(r.vals) {
		r.overrun = true
		return 0.5
	}
	f := r.vals[r.used]
	r.used++
	return f
}

func (r *Replayer) apply(st *Step) error {
	mismatch := func(format string, args ...any) error {
		return &Mismatch{Step: st.N, Kind: st.Kind, What: fmt.Sprintf(format, args...)}
	}
	if st.Kind == StepResync {
		g, err := LoadGame(st.State)
		if err != nil {
			return mismatch("resync state: %v", err)
		}
		if got := StateHash(g); got != st.Hash {
			return mismatch("resync state hashes to %s, recorded %s", got, st.Hash)
		}
		r.G = g
		return nil
	}
	g := r.G
	g.Version = int(st.Version)
	var idProblem string
	used := 0
	g.Clock = func() int64 { return st.At }
	g.NewID = func(prefix string) string {
		if used >= len(st.IDs) {
			if idProblem == "" {
				idProblem = fmt.Sprintf("the engine created more ids than the %d recorded (a %q id)", len(st.IDs), prefix)
			}
			used++
			return fmt.Sprintf("%s_replay%06d", prefix, used)
		}
		id := st.IDs[used]
		used++
		if !strings.HasPrefix(id, prefix+"_") && idProblem == "" {
			idProblem = fmt.Sprintf("id %d is %q but the engine asked for a %q id", used-1, id, prefix)
		}
		return id
	}
	defer func() { g.Clock, g.NewID = nil, nil }()
	rng := &replayRNG{vals: st.RNG}
	chanceTop, chestTop := topCard(g.Decks.Chance), topCard(g.Decks.Chest)

	var err error
	switch st.Kind {
	case StepAction:
		action, decodeErr := DecodeAction(st.Action)
		if decodeErr != nil {
			err = decodeErr
		} else {
			err = engine.PerformAction(g, st.By, action, rng)
		}
	case StepOrder:
		if !engine.CompleteOpeningOrder(g, st.Now) {
			return mismatch("the turn-order countdown did not end at %d", st.Now)
		}
	case StepEnd:
		_, err = engine.EndGame(g)
	default:
		return mismatch("unknown step kind %q", st.Kind)
	}

	got := ""
	if err != nil {
		got = err.Error()
	}
	switch {
	case got != st.Error && st.Error == "":
		return mismatch("the engine refused it: %s", got)
	case got != st.Error && got == "":
		return mismatch("the engine accepted it but it was refused when recorded (%s)", st.Error)
	case got != st.Error:
		return mismatch("the engine refused it with %q, recorded %q", got, st.Error)
	}
	if rng.overrun {
		return mismatch("the engine drew more random numbers than the %d recorded", len(st.RNG))
	}
	if rng.used != len(st.RNG) {
		return mismatch("the engine drew %d random numbers, %d were recorded", rng.used, len(st.RNG))
	}
	if idProblem != "" {
		return mismatch("%s", idProblem)
	}
	if used != len(st.IDs) {
		return mismatch("the engine created %d ids, %d were recorded", used, len(st.IDs))
	}
	if int64(g.Version) != st.VersionAfter {
		return mismatch("version %d after the step, recorded %d", g.Version, st.VersionAfter)
	}
	if cards := drawnCards(g, chanceTop, chestTop); !slices.Equal(cards, st.Cards) {
		return mismatch("drew %v, recorded %v", cards, st.Cards)
	}
	if st.Decks != nil {
		if !slices.Equal(g.Decks.Chance, st.Decks.Chance) || !slices.Equal(g.Decks.Chest, st.Decks.Chest) {
			return mismatch("decks after the shuffle differ from the recorded order")
		}
	} else if dice := dicePairs(st.RNG); !slices.Equal(dice, st.Dice) {
		return mismatch("dice %v do not match the recorded random numbers (%v)", st.Dice, dice)
	}
	if hash := StateHash(g); hash != st.Hash {
		return mismatch("state hash %s, recorded %s%s", hash, st.Hash, r.hint(st))
	}
	return nil
}

// hint describes the replayed state briefly, to help find a mismatch.
func (r *Replayer) hint(st *Step) string {
	g := r.G
	var b strings.Builder
	fmt.Fprintf(&b, " (after %s by %s: phase %s", actionLabel(st), st.By, g.Phase)
	for _, p := range g.Players {
		fmt.Fprintf(&b, ", %s $%d @%d", p.Name, p.Cash, p.Position)
	}
	b.WriteString(")")
	return b.String()
}

func actionLabel(st *Step) string {
	if st.Kind != StepAction {
		return st.Kind
	}
	return string(st.Action)
}

// Verify replays the whole record. It returns the final game and the first
// mismatch, if any; a record with a final state must end in it.
func Verify(rec *Record) (*engine.Game, error) {
	r, err := NewReplayer(rec)
	if err != nil {
		return nil, err
	}
	for {
		if _, err := r.Step(); err == io.EOF {
			break
		} else if err != nil {
			return r.G, err
		}
	}
	if len(rec.Final) > 0 {
		final, err := LoadGame(rec.Final)
		if err != nil {
			return r.G, &Mismatch{Step: -1, What: "final state: " + err.Error()}
		}
		if got, want := StateHash(r.G), StateHash(final); got != want {
			return r.G, &Mismatch{Step: -1, What: fmt.Sprintf("the replay ends in state %s, the recorded final state is %s", got, want)}
		}
	}
	if o := rec.Outcome; o != nil && o.WinnerID != "" && r.G.WinnerID != o.WinnerID {
		return r.G, &Mismatch{Step: -1, What: fmt.Sprintf("the replay's winner is %q, recorded %q", r.G.WinnerID, o.WinnerID)}
	}
	return r.G, nil
}

// Try checks st as the next step on a copy of the replayed game, leaving the
// replayer where it was. Nil means st fits.
func (r *Replayer) Try(st *Step) error {
	data, err := json.Marshal(r.G)
	if err != nil {
		return err
	}
	g, err := LoadGame(data)
	if err != nil {
		return err
	}
	trial := &Replayer{rec: r.rec, G: g, next: r.next}
	return trial.apply(st)
}
