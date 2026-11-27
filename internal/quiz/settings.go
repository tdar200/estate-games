package quiz

import "errors"

// Game lengths (Settings.Length).
const (
	LengthQuick = "quick" // one board, then the final
	LengthFull  = "full"  // two boards (the second at double values), then the final
)

// Reading speeds (Settings.ReadSpeed).
const (
	SpeedSlow   = "slow"
	SpeedNormal = "normal"
	SpeedFast   = "fast"
)

// Player limits.
const (
	MinPlayers = 1
	MaxPlayers = 8
)

// Settings are the rules the host chooses in the lobby (action "settings",
// docs/QUIZ-API.md §2.1).
type Settings struct {
	// Length is LengthQuick (default, about 12 minutes) or LengthFull (about
	// 22 minutes).
	Length string `json:"length"`
	// ReadSpeed scales the reading time before the choices open: slow ×1.4,
	// normal ×1, fast ×0.75. Slow suits younger players or a second language.
	ReadSpeed string `json:"readSpeed"`
	// AnswerSeconds is how long the choices stay open on a board clue: 6, 8
	// (default) or 12. The final always gets 20 seconds.
	AnswerSeconds int `json:"answerSeconds"`
}

// DefaultSettings are the lobby defaults.
func DefaultSettings() Settings {
	return Settings{Length: LengthQuick, ReadSpeed: SpeedNormal, AnswerSeconds: 8}
}

// Validate checks every setting.
func (s Settings) Validate() error {
	switch s.Length {
	case LengthQuick, LengthFull:
	default:
		return errors.New(`length must be "quick" or "full"`)
	}
	switch s.ReadSpeed {
	case SpeedSlow, SpeedNormal, SpeedFast:
	default:
		return errors.New(`readSpeed must be "slow", "normal" or "fast"`)
	}
	switch s.AnswerSeconds {
	case 6, 8, 12:
	default:
		return errors.New("answerSeconds must be 6, 8 or 12")
	}
	return nil
}

// Rounds is the number of boards these settings play before the final.
func (s Settings) Rounds() int {
	if s.Length == LengthFull {
		return 2
	}
	return 1
}

// readFactor scales the reading time.
func (s Settings) readFactor() float64 {
	switch s.ReadSpeed {
	case SpeedSlow:
		return 1.4
	case SpeedFast:
		return 0.75
	}
	return 1
}
