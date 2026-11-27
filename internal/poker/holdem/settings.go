package holdem

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Game styles (Settings.Style).
const (
	// StyleTournament: blinds rise per the schedule, rebuys per the
	// settings, and the last player with chips wins.
	StyleTournament = "tournament"
	// StyleCash: blinds stay fixed, a busted player may rebuy at any time
	// (they sit out until they do), and the game runs until the host ends
	// it; players are then ranked by chips won (stack minus chips bought).
	StyleCash = "cash"
)

// Settings are chosen by the host in the lobby (action "settings").
type Settings struct {
	// Style is StyleTournament (default) or StyleCash. A cash game ignores
	// BlindSchedule, TargetMinutes, Antes, Rebuys and RebuyLevels.
	Style         string `json:"style"`
	MaxPlayers    int    `json:"maxPlayers"`    // seats, 2 up to the table's seat limit (default and usual limit 8)
	StartingStack int    `json:"startingStack"` // chips each player starts with, and the cash buy-in (default 1500)
	SmallBlind    int    `json:"smallBlind"`    // first level (default 10)
	BigBlind      int    `json:"bigBlind"`      // first level (default 20)
	Ante          int    `json:"ante"`          // first level ante (default 0)
	// BlindSchedule is "auto" (blinds rise so the game ends in about
	// TargetMinutes) or "off" (blinds never change). Default "auto".
	BlindSchedule string `json:"blindSchedule"`
	TargetMinutes int    `json:"targetMinutes"` // 15–240 (default 60)
	Antes         bool   `json:"antes"`         // auto schedule adds antes in later levels (default true)
	Rebuys        int    `json:"rebuys"`        // rebuys allowed per player, 0 = off (default 0, max 10)
	RebuyLevels   int    `json:"rebuyLevels"`   // rebuys only during the first N levels; 0 = any time
	ActionSeconds int    `json:"actionSeconds"` // turn timer per decision, 0 = off (default 20; else 10–300)
	// TimeBankSeconds is each player's reserve (default 30; 0 = off, at most
	// 120). When the turn timer runs out the bank runs before the timeout
	// checks or folds; it refills by TimeBankRefill seconds every
	// TimeBankRefillHands hands dealt in, up to this setting.
	TimeBankSeconds int  `json:"timeBankSeconds"`
	LateJoin        bool `json:"lateJoin"` // players may join after the start (default true)
	// FourColorDeck asks screens to draw a four-colour deck (spades black,
	// hearts red, diamonds blue, clubs green). Display only; a phone may
	// override it locally. Default false.
	FourColorDeck bool `json:"fourColorDeck"`
}

// DefaultSettings returns the lobby defaults.
func DefaultSettings() Settings {
	return Settings{
		Style:           StyleTournament,
		MaxPlayers:      8,
		StartingStack:   1500,
		SmallBlind:      10,
		BigBlind:        20,
		BlindSchedule:   "auto",
		TargetMinutes:   60,
		Antes:           true,
		ActionSeconds:   20,
		TimeBankSeconds: 30,
		LateJoin:        true,
	}
}

// Time bank refills: TimeBankRefill seconds every TimeBankRefillHands hands
// a player is dealt into, never above Settings.TimeBankSeconds.
const (
	TimeBankRefill      = 5
	TimeBankRefillHands = 10
)

// Cash reports whether the settings describe a cash game.
func (s Settings) Cash() bool { return s.Style == StyleCash }

// MaxSeats is the largest table the engine supports.
const MaxSeats = 10

// DefaultSeatLimit is the largest table a game may choose (the user's
// decision for the party game); Options.SeatLimit can raise it to MaxSeats.
const DefaultSeatLimit = 8

// Validate checks ranges; seatLimit is the most seats allowed (2–MaxSeats).
func (s Settings) Validate(seatLimit int) error {
	switch {
	case s.Style != StyleTournament && s.Style != StyleCash:
		return errors.New(`style must be "tournament" or "cash"`)
	case s.MaxPlayers < 2 || s.MaxPlayers > seatLimit:
		return fmt.Errorf("seats must be between 2 and %d", seatLimit)
	case s.BigBlind < 2 || s.BigBlind > 1_000_000:
		return errors.New("the big blind must be between 2 and 1,000,000")
	case s.SmallBlind < 1 || s.SmallBlind > s.BigBlind:
		return errors.New("the small blind must be between 1 and the big blind")
	case s.Ante < 0 || s.Ante > s.BigBlind:
		return errors.New("the ante must be between 0 and the big blind")
	case s.StartingStack < 10*s.BigBlind || s.StartingStack > 100_000_000:
		return errors.New("starting chips must be at least 10 big blinds")
	case s.BlindSchedule != "auto" && s.BlindSchedule != "off":
		return errors.New(`blind schedule must be "auto" or "off"`)
	case s.BlindSchedule == "auto" && (s.TargetMinutes < 15 || s.TargetMinutes > 240):
		return errors.New("target length must be 15–240 minutes")
	case s.Rebuys < 0 || s.Rebuys > 10:
		return errors.New("rebuys must be 0–10")
	case s.RebuyLevels < 0 || s.RebuyLevels > 50:
		return errors.New("rebuy levels must be 0–50")
	case s.ActionSeconds != 0 && (s.ActionSeconds < 10 || s.ActionSeconds > 300):
		return errors.New("the turn timer must be off or 10–300 seconds")
	case s.TimeBankSeconds < 0 || s.TimeBankSeconds > 120:
		return errors.New("the time bank must be 0–120 seconds")
	}
	return nil
}

// Level is one blind level.
type Level struct {
	SmallBlind int `json:"smallBlind"`
	BigBlind   int `json:"bigBlind"`
	Ante       int `json:"ante"`
	Minutes    int `json:"minutes"` // 0 = lasts forever
}

// niceBlinds are the big blinds a schedule may use (all even, so the small
// blind is exactly half).
var niceBlinds []int

func init() {
	for mult := 1; mult <= 100_000_000; mult *= 10 {
		for _, base := range []int{2, 3, 4, 5, 6, 8, 10, 12, 15} {
			v := base * mult
			if v%2 == 0 && v >= 2 {
				niceBlinds = append(niceBlinds, v)
			}
		}
	}
	sort.Ints(niceBlinds)
	// dedupe
	out := niceBlinds[:0]
	for i, v := range niceBlinds {
		if i == 0 || v != niceBlinds[i-1] {
			out = append(out, v)
		}
	}
	niceBlinds = out
}

// niceAbove returns the nice blind nearest x (log scale) that is > prev.
func niceAbove(x float64, prev int) int {
	best, bestDist := 0, math.Inf(1)
	for _, v := range niceBlinds {
		if v <= prev {
			continue
		}
		d := math.Abs(math.Log(float64(v)) - math.Log(x))
		if d < bestDist {
			best, bestDist = v, d
		}
		if float64(v) > x*2 {
			break
		}
	}
	if best == 0 {
		best = prev * 2
	}
	return best
}

func niceAnte(bb int) int {
	a := bb / 8
	if a < 1 {
		return 1
	}
	steps := []int{1, 2, 3, 4, 5, 6, 8, 10, 12, 15, 20, 25, 30, 40, 50, 60, 75, 80, 100}
	scale := 1
	for a > 100*scale {
		scale *= 10
	}
	best := steps[0] * scale
	for _, s := range steps {
		if s*scale <= a {
			best = s * scale
		}
	}
	return best
}

// BuildSchedule makes the blind levels. A cash game, or "off", has a single
// level that never ends. With "auto", levels last
// TargetMinutes/8 minutes (3–20) and the big blind grows geometrically so it
// reaches about a twentieth of all chips in play at the target time, when
// the average heads-up stack is ten big blinds and the game ends quickly.
// Levels keep rising after the target so a game always finishes.
func BuildSchedule(s Settings, players int) []Level {
	first := Level{SmallBlind: s.SmallBlind, BigBlind: s.BigBlind, Ante: s.Ante}
	if s.BlindSchedule != "auto" || s.Cash() {
		return []Level{first}
	}
	players = max(players, 2)
	minutes := min(max(int(math.Round(float64(s.TargetMinutes)/8)), 3), 20)
	steps := max(int(math.Round(float64(s.TargetMinutes)/float64(minutes))), 2)
	total := float64(players * s.StartingStack)
	goal := total / 20
	ratio := math.Pow(goal/float64(s.BigBlind), 1/float64(steps))
	ratio = min(max(ratio, 1.15), 2.0)
	anteFrom := max(steps/3, 2)
	first.Minutes = minutes
	levels := []Level{first}
	bbf := float64(s.BigBlind)
	for len(levels) < 40 {
		prev := levels[len(levels)-1]
		bbf *= ratio
		bb := niceAbove(bbf, prev.BigBlind)
		bbf = max(bbf, float64(bb)) // keep the curve from lagging behind rounding
		lv := Level{SmallBlind: bb / 2, BigBlind: bb, Ante: s.Ante, Minutes: minutes}
		if s.Antes && len(levels) >= anteFrom {
			lv.Ante = max(s.Ante, niceAnte(bb))
		}
		levels = append(levels, lv)
		if float64(bb) >= total/2 {
			break
		}
	}
	levels[len(levels)-1].Minutes = 0 // the last level lasts until the end
	return levels
}

// Timing holds the pauses the table inserts so people can see what
// happened. Tests and simulations set them to zero.
type Timing struct {
	RunoutStepMs int `json:"runoutStepMs"` // between streets of an all-in run-out (default 1800)
	HandOverMs   int `json:"handOverMs"`   // after an uncontested pot (default 2500)
	ShowdownMs   int `json:"showdownMs"`   // after a showdown (default 5000, +1500 per side pot)
	RebuyMs      int `json:"rebuyMs"`      // how long a busted player may rebuy (default 30000)
}

// DefaultTiming is used by NewTable unless Options.Timing is set.
func DefaultTiming() Timing {
	return Timing{RunoutStepMs: 1800, HandOverMs: 2500, ShowdownMs: 5000, RebuyMs: 30000}
}
