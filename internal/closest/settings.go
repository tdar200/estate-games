package closest

import "errors"

// Game lengths (Settings.Length).
const (
	LengthQuick = "quick" // 7 questions, about 8–11 minutes
	LengthFull  = "full"  // 12 questions, about 13–19 minutes
)

// Player limits.
const (
	MinPlayers = 1
	MaxPlayers = 8
	// FillTo is how many players the fill setting makes up with Normal
	// computers at the start: fewer guesses than this make the slots pay
	// alike, so betting means nothing (docs/CLOSEST-API.md §2.1).
	FillTo = 4
)

// Settings are the rules the host chooses in the lobby (action "settings",
// docs/CLOSEST-API.md §2.1).
type Settings struct {
	// Length is LengthQuick (default) or LengthFull.
	Length string `json:"length"`
	// GuessSeconds is the time to guess: 20, 30 (default) or 45.
	GuessSeconds int `json:"guessSeconds"`
	// BetSeconds is the time to bet: 15, 25 (default) or 35.
	BetSeconds int `json:"betSeconds"`
	// Fill adds Normal computers at the start until there are FillTo players.
	Fill bool `json:"fill"`
}

// DefaultSettings are the lobby defaults.
func DefaultSettings() Settings {
	return Settings{Length: LengthQuick, GuessSeconds: 30, BetSeconds: 25, Fill: true}
}

// Validate checks every setting.
func (s Settings) Validate() error {
	switch s.Length {
	case LengthQuick, LengthFull:
	default:
		return errors.New(`length must be "quick" or "full"`)
	}
	switch s.GuessSeconds {
	case 20, 30, 45:
	default:
		return errors.New("guessSeconds must be 20, 30 or 45")
	}
	switch s.BetSeconds {
	case 15, 25, 35:
	default:
		return errors.New("betSeconds must be 15, 25 or 35")
	}
	return nil
}

// QuestionCount is the number of questions these settings ask.
func (s Settings) QuestionCount() int {
	if s.Length == LengthFull {
		return 12
	}
	return 7
}

// topicCap is the most questions one topic may have in a game (§7).
func (s Settings) topicCap() int {
	if s.Length == LengthFull {
		return 3
	}
	return 2
}

// yearCap is the most year questions a game may have (§7).
func (s Settings) yearCap() int {
	if s.Length == LengthFull {
		return 4
	}
	return 2
}
