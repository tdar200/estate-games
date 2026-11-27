package ludo

import "errors"

// Board choices (Settings.Board).
const (
	BoardAuto = "auto" // the 4-arm cross for 2–4 players, the 6-arm board for 5–6
	Board4    = "4"
	Board6    = "6"
)

// Player limits.
const (
	MinPlayers = 2
	MaxPlayers = 6
)

// Settings are the house rules, chosen by the host in the lobby (action
// "settings"). The defaults are the Pakistani/Indian style.
type Settings struct {
	// Pieces each player has: 2, 3 or 4 (default 4). Fewer is quicker.
	Pieces int `json:"pieces"`
	// Board is BoardAuto (default), Board4 or Board6. Auto picks the classic
	// cross for up to four players, because everyone knows it and it is the
	// most readable on a TV, and the 6-arm board only when five or six play.
	// The host may still choose six arms for 2–4 players (longer laps, more
	// room); four arms refuses a fifth player.
	Board string `json:"board"`
	// AnyRollOut lets any roll bring a piece out, not only a 6 (kids mode;
	// default false).
	AnyRollOut bool `json:"anyRollOut"`
	// HomeBonus gives another roll when a piece reaches home (default true).
	HomeBonus bool `json:"homeBonus"`
	// SafeSquares makes start and star squares safe (default true).
	SafeSquares bool `json:"safeSquares"`
	// ThreeSixes: a third 6 in a row ends the turn unplayed (default true).
	ThreeSixes bool `json:"threeSixes"`
	// CaptureFirst: a player's pieces may only turn into their home column
	// once that player has captured a piece; until then they go round again
	// (default false).
	CaptureFirst bool `json:"captureFirst"`
	// Blocks: two or more of one player's pieces on a ring square form a
	// block that no other player's piece may pass or land on (default false;
	// without it pieces never block and a capture takes every piece there).
	Blocks bool `json:"blocks"`
	// PlayOn keeps playing for second, third… place after the first player
	// gets every piece home (default true); off, the game ends at the first
	// winner and the rest are ranked by how far their pieces got.
	PlayOn bool `json:"playOn"`
}

// DefaultSettings are the lobby defaults.
func DefaultSettings() Settings {
	return Settings{Pieces: 4, Board: BoardAuto, HomeBonus: true, SafeSquares: true, ThreeSixes: true, PlayOn: true}
}

// Validate checks the settings themselves (not the number of players).
func (s Settings) Validate() error {
	if s.Pieces < 2 || s.Pieces > 4 {
		return errors.New("pieces must be 2, 3 or 4")
	}
	switch s.Board {
	case BoardAuto, Board4, Board6:
	default:
		return errors.New(`board must be "auto", "4" or "6"`)
	}
	return nil
}

// SeatLimit is the most players these settings seat: four on the 4-arm
// board, otherwise six.
func (s Settings) SeatLimit() int {
	if s.Board == Board4 {
		return 4
	}
	return MaxPlayers
}

// ArmsFor is the number of arms the board has for n players.
func (s Settings) ArmsFor(n int) int {
	switch s.Board {
	case Board4:
		return 4
	case Board6:
		return 6
	}
	if n > 4 {
		return 6
	}
	return 4
}
