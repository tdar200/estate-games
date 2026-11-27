package gamerecord

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"

	"github.com/tdar200/estate-games/internal/engine"
)

// StateHash is a 64-bit FNV-1a hash (16 hex digits) of the game's JSON
// (json.Marshal, the server's Snapshot) with the top-level version number
// read as 0. The version is left out because the server bumps it between
// steps for presence changes; steps record it separately.
func StateHash(g *engine.Game) string {
	data, err := json.Marshal(g)
	if err != nil {
		return "error:" + err.Error()
	}
	return HashSnapshot(data)
}

// HashSnapshot is StateHash of a game from its JSON, as json.Marshal wrote it
// (the server hashes the snapshot it makes after every change anyway).
func HashSnapshot(data []byte) string {
	h := fnv.New64a()
	if start, end := topLevelValue(data, "version"); start >= 0 {
		h.Write(data[:start])
		h.Write([]byte{'0'})
		h.Write(data[end:])
	} else {
		h.Write(data)
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// topLevelValue finds the value of key in a JSON object's top level and
// returns its byte range (-1, -1 if absent). It scans once, skipping strings
// and nested values.
func topLevelValue(data []byte, key string) (start, end int) {
	depth := 0
	inString, escaped := false, false
	pattern := `"` + key + `":`
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			if depth == 1 && (data[i-1] == '{' || data[i-1] == ',') && bytes.HasPrefix(data[i:], []byte(pattern)) {
				start = i + len(pattern)
				end = start
				for end < len(data) && data[end] != ',' && data[end] != '}' {
					end++
				}
				return start, end
			}
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	return -1, -1
}

// recordingRNG draws from src and remembers every float.
type recordingRNG struct {
	src engine.RNG
	got []float64
}

func (r *recordingRNG) Float64() float64 {
	f := r.src.Float64()
	r.got = append(r.got, f)
	return f
}

// capture runs apply (one engine change) on g with the clock frozen, ids and
// random numbers recorded, and returns the step describing it (Kind,
// RNG, IDs, Cards, versions, At; not Hash). apply must use the RNG it is given.
func capture(g *engine.Game, kind string, apply func(rng engine.RNG) error) (step Step, err error) {
	at := g.Now()
	clock, newID := g.Clock, g.NewID
	var ids []string
	g.Clock = func() int64 { return at }
	g.NewID = func(prefix string) string {
		var id string
		if newID != nil {
			id = newID(prefix)
		} else {
			id = engine.DefaultNewID(prefix)
		}
		ids = append(ids, id)
		return id
	}
	defer func() { g.Clock, g.NewID = clock, newID }()
	rng := &recordingRNG{src: engine.DefaultRNG}
	chanceTop, chestTop := topCard(g.Decks.Chance), topCard(g.Decks.Chest)
	step = Step{Kind: kind, At: at, Version: int64(g.Version)}

	err = apply(rng)

	step.VersionAfter = int64(g.Version)
	step.RNG = rng.got
	step.IDs = ids
	step.Cards = drawnCards(g, chanceTop, chestTop)
	return step, err
}

// topCard is a deck's top card ("" when empty).
func topCard(deck []string) string {
	if len(deck) == 0 {
		return ""
	}
	return deck[0]
}

// drawnCards lists the cards drawn since the decks' tops were chanceTop and
// chestTop. A draw moves the top card away (to the bottom, or into a
// player's hand for a Jail pass) and every card is unique, so a
// deck was drawn from exactly when its top card changed; one action draws at
// most once from each deck (Headlines first: "go back three spaces" can land
// on Post).
func drawnCards(g *engine.Game, chanceTop, chestTop string) []CardDraw {
	var out []CardDraw
	if chanceTop != "" && topCard(g.Decks.Chance) != chanceTop {
		out = append(out, CardDraw{Deck: engine.SpaceChance, Card: chanceTop})
	}
	if chestTop != "" && topCard(g.Decks.Chest) != chestTop {
		out = append(out, CardDraw{Deck: engine.SpaceChest, Card: chestTop})
	}
	return out
}

// dicePairs turns a step's random numbers into die pairs (every float a
// non-start step draws is a die).
func dicePairs(rng []float64) [][2]int {
	var out [][2]int
	for i := 0; i+1 < len(rng); i += 2 {
		out = append(out, [2]int{die(rng[i]), die(rng[i+1])})
	}
	return out
}

func die(f float64) int { return int(math.Floor(f*6)) + 1 }

// DecodeAction decodes an action the way the server's engine adapter does: an
// empty action is JSON null, malformed JSON is "Invalid action format".
func DecodeAction(raw json.RawMessage) (engine.Action, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	var action engine.Action
	if err := json.Unmarshal(raw, &action); err != nil {
		return engine.Action{}, errors.New("Invalid action format")
	}
	return action, nil
}

// CaptureAction applies a raw JSON action for playerID to g while recording
// it, like engine.PerformAction with the server's decoding. The step's Hash
// is left empty: the caller sets it from the game after the step (StateHash,
// or HashSnapshot of a snapshot it makes anyway), and then uses Skip to drop
// a refused action that changed nothing.
func CaptureAction(g *engine.Game, playerID string, raw json.RawMessage) (Step, error) {
	action, err := DecodeAction(raw)
	if err != nil {
		return Step{}, err
	}
	actionType := action.Type
	step, err := capture(g, StepAction, func(rng engine.RNG) error {
		return engine.PerformAction(g, playerID, action, rng)
	})
	step.By = playerID
	step.Action = CleanAction(raw)
	if err != nil {
		step.Error = err.Error()
	}
	if actionType == engine.ActionStart && err == nil {
		step.Decks = &Decks{Chance: append([]string{}, g.Decks.Chance...), Chest: append([]string{}, g.Decks.Chest...)}
	} else {
		step.Dice = dicePairs(step.RNG)
	}
	return step, err
}

// Skip reports whether a captured step can be left out of the record: the
// engine refused it, it drew no random numbers and created no ids, and the
// state hash is still prevHash (the hash after the last recorded step). A
// refused action that did change the game (the engine keeps changes made
// before a rule deep inside it refused) is recorded with its Error.
func (st *Step) Skip(prevHash string) bool {
	return st.Error != "" && len(st.RNG) == 0 && len(st.IDs) == 0 && st.Hash == prevHash
}

// PerformAction is CaptureAction plus the state hash: record is false when
// the step should be left out (Skip).
func PerformAction(g *engine.Game, playerID string, raw json.RawMessage, prevHash string) (step Step, record bool, err error) {
	step, err = CaptureAction(g, playerID, raw)
	if step.Kind == "" {
		return step, false, err // not even decodable: nothing happened
	}
	step.Hash = StateHash(g)
	return step, !step.Skip(prevHash), err
}

// CaptureOrder ends the turn-order countdown at now while recording it
// (Hash left empty, as in CaptureAction). ok is false, and nothing changed,
// when the countdown has not run out.
func CaptureOrder(g *engine.Game, now int64) (step Step, ok bool) {
	step, _ = capture(g, StepOrder, func(engine.RNG) error {
		ok = engine.CompleteOpeningOrder(g, now)
		return nil
	})
	if !ok {
		return Step{}, false
	}
	step.Now = now
	step.Dice = dicePairs(step.RNG)
	return step, true
}

// CompleteOpeningOrder is CaptureOrder plus the state hash.
func CompleteOpeningOrder(g *engine.Game, now int64) (Step, bool) {
	step, ok := CaptureOrder(g, now)
	if ok {
		step.Hash = StateHash(g)
	}
	return step, ok
}

// CaptureEnd ends g early (the richest player wins) while recording it (Hash
// left empty). by is the host's player id ("" for a table screen).
func CaptureEnd(g *engine.Game, by string) (step Step, winnerID string, err error) {
	var winner *engine.Player
	step, err = capture(g, StepEnd, func(engine.RNG) error {
		var e error
		winner, e = engine.EndGame(g)
		return e
	})
	if err != nil {
		return Step{}, "", err
	}
	step.By = by
	return step, winner.ID, nil
}

// actionKeys are the keys of an action object the engine reads. Everything
// else a client sends is dropped from the record (the engine ignores it).
var actionKeys = map[string]bool{
	"type": true, "dice": true, "mode": true, "targetId": true, "offer": true, "request": true,
	"amount": true, "spaceIndex": true, "rules": true, "direction": true, "principal": true,
	"repay": true, "dueTurns": true, "collateral": true, "loanId": true, "reason": true,
	"now": true, "playerId": true, // close_auction, remove_player
}

// CleanAction returns the action JSON with only the keys the engine reads
// (and, in trade bundles, only cash and properties). Values are kept byte for
// byte, so the engine reads the cleaned action exactly like the original.
func CleanAction(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return json.RawMessage("null")
	}
	if trimmed[0] != '{' {
		var compact bytes.Buffer
		if json.Compact(&compact, trimmed) != nil {
			return json.RawMessage("null")
		}
		return compact.Bytes()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(trimmed, &fields) != nil {
		return json.RawMessage("null")
	}
	for key, value := range fields {
		if !actionKeys[key] {
			delete(fields, key)
			continue
		}
		if key == "offer" || key == "request" {
			fields[key] = cleanBundle(value)
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return json.RawMessage("null")
	}
	return out
}

func cleanBundle(raw json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		// Not an object: the engine treats it as an empty bundle.
		return json.RawMessage("null")
	}
	for key := range fields {
		if key != "cash" && key != "properties" {
			delete(fields, key)
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return json.RawMessage("null")
	}
	return out
}
