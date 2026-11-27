package gamerecord

import (
	"encoding/json"

	"github.com/tdar200/estate-games/internal/engine"
)

// ReasonGuesser lists what a computer's lost reason may have said at a step,
// given the game just before it.
type ReasonGuesser func(g *engine.Game, st *Step) []string

// RepairReasons puts back the reasons records from 0.10.1 to 0.11.1 lost.
// Those versions dropped "reason" from stored actions, but the engine logs
// it, so a computer's answer with a reason stopped the replay at that step.
// The text itself is gone, so each one is guessed; a guess is only kept if
// the replay then reproduces the step's recorded state hash, which covers
// the log, so a wrong text can't get through. It returns how many steps it
// repaired, and the mismatch if a step can't be repaired.
func RepairReasons(rec *Record, guess ReasonGuesser) (int, error) {
	r, err := NewReplayer(rec)
	if err != nil {
		return 0, err
	}
	fixed := 0
	for !r.Done() {
		st := &rec.Steps[r.Next()]
		if mayHaveLostReason(st) {
			if err := r.Try(st); err != nil {
				original := st.Action
				found := false
				for _, reason := range guess(r.G, st) {
					patched, ok := withReason(original, reason)
					if !ok {
						continue
					}
					st.Action = patched
					if r.Try(st) == nil {
						found = true
						break
					}
				}
				if !found {
					st.Action = original
					return fixed, err
				}
				fixed++
			}
		}
		if _, err := r.Step(); err != nil {
			return fixed, err
		}
	}
	return fixed, nil
}

// mayHaveLostReason reports a computer's answer to a trade, the only actions
// that carry a reason.
func mayHaveLostReason(st *Step) bool {
	if st.Kind != StepAction || st.Source == SourcePlayer {
		return false
	}
	switch st.ActionType() {
	case engine.ActionCounterTrade, engine.ActionRejectTrade:
		return true
	}
	return false
}

func withReason(action json.RawMessage, reason string) (json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(action, &fields) != nil {
		return nil, false
	}
	text, err := json.Marshal(reason)
	if err != nil {
		return nil, false
	}
	fields["reason"] = text
	out, err := json.Marshal(fields)
	return out, err == nil
}
