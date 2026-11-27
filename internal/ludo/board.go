package ludo

import (
	"fmt"
	"strconv"
	"strings"
)

// The board is a ring of squares with an arm per possible seat. Every arm is
// three columns of six squares, like the classic cross:
//
//   - the outer two columns and the middle square at the tip belong to the
//     ring, 13 squares per arm, travelled clockwise;
//   - the middle column is the arm's home column, five squares leading to
//     home (the centre);
//   - the yard, where an arm's pieces wait to come out, sits clockwise of it,
//     between this arm and the next.
//
// Within an arm, ring squares are numbered 0–12 from the centre outwards up
// the first column (0–5), the tip (6), then back in down the second column
// (7–12); ring square i of arm a is a*13+i. A piece comes out on its arm's
// start square (local 8: the second column, one in from the edge), goes all
// the way round, and turns into its home column from the tip of its own arm
// (local 6). That is Ring-1 squares of travel (51 on the 4-arm board, start
// square included), then five home-column squares, then home: 56 steps from
// the start square to home on the 4-arm board, 82 on the 6-arm board.
//
// The 4-arm board is the classic 15 × 15 cross for 2–4 players; the 6-arm
// board (a hexagon centre with six arms) seats 5 or 6. Clients draw both
// from the same numbering (web/shared/ludo-board.js).

// Board sizes.
const (
	ArmSquares = 13 // ring squares per arm
	ColumnLen  = 5  // home-column squares per arm (the sixth step is home)
	HomeDepth  = ColumnLen + 1
	startLocal = 8 // an arm's start square, in arm-local numbering
	tipLocal   = 6 // an arm's tip: the last ring square before its home column
	starAhead  = 8 // the star square is this many steps after each start square
	MaxArms    = 6
)

// Board is the geometry of a 4-arm or 6-arm board.
type Board struct {
	Arms int `json:"arms"`
	Ring int `json:"ring"`
}

// NewBoard returns the board with the given number of arms (4 or 6).
func NewBoard(arms int) Board {
	if arms != 6 {
		arms = 4
	}
	return Board{Arms: arms, Ring: arms * ArmSquares}
}

// Start is the ring square where an arm's pieces come out.
func (b Board) Start(arm int) int { return arm*ArmSquares + startLocal }

// Tip is the last ring square an arm's pieces cross before their home column.
func (b Board) Tip(arm int) int { return arm*ArmSquares + tipLocal }

// Star is the safe square starAhead steps after an arm's start square.
func (b Board) Star(arm int) int { return (b.Start(arm) + starAhead) % b.Ring }

// LastStep is the track step of a piece standing on its own tip: the
// furthest a piece goes round the ring before turning into its home column.
func (b Board) LastStep() int { return b.Ring - 2 }

// Square is the ring square of a piece of arm that has travelled step
// squares from its start square.
func (b Board) Square(arm, step int) int { return (b.Start(arm) + step) % b.Ring }

// Safe reports whether a ring square is a start or star square (every arm
// has both, whether or not anybody sits there).
func (b Board) Safe(square int) bool {
	local := square % ArmSquares
	return local == startLocal || local == (startLocal+starAhead)%ArmSquares
}

// SafeSquares lists every safe ring square in ring order.
func (b Board) SafeSquares() []int {
	var out []int
	for sq := 0; sq < b.Ring; sq++ {
		if b.Safe(sq) {
			out = append(out, sq)
		}
	}
	return out
}

// StepsHome is how far a piece of this board travels from its start square
// to home (56 on the 4-arm board).
func (b Board) StepsHome() int { return b.LastStep() + HomeDepth }

// ArmsFor returns which arms the players sit on, in turn order, for n players
// on this board. The seats are spread out: two players face each other, three
// on four arms leave one arm empty, and so on.
func (b Board) ArmsFor(n int) []int {
	layouts := map[int][][]int{
		4: {2: {0, 2}, 3: {0, 1, 2}, 4: {0, 1, 2, 3}},
		6: {2: {0, 3}, 3: {0, 2, 4}, 4: {0, 1, 3, 4}, 5: {0, 1, 2, 3, 4}, 6: {0, 1, 2, 3, 4, 5}},
	}
	rows := layouts[b.Arms]
	if n >= 0 && n < len(rows) && rows[n] != nil {
		return append([]int(nil), rows[n]...)
	}
	out := make([]int, 0, n)
	for i := 0; i < n && i < b.Arms; i++ {
		out = append(out, i)
	}
	return out
}

// Piece zones.
const (
	ZoneYard   = "yard"   // waiting to come out
	ZoneTrack  = "track"  // on the ring
	ZoneColumn = "column" // in its own home column
	ZoneHome   = "home"   // finished
)

// Piece is one of a player's pieces.
type Piece struct {
	Zone string `json:"zone"`
	// Step: on the track, squares travelled from the start square (0 = the
	// start square; LastStep = the tip of the piece's own arm; Ring-1 only
	// after going past the tip without turning in, under the capture-first
	// house rule). In the home column, 1–5 (5 is next to home). Otherwise 0.
	Step int `json:"step"`
}

// Distance is how many steps a piece still has to go to reach home on this
// board (a piece in the yard counts the step out as one).
func (b Board) Distance(p Piece) int {
	switch p.Zone {
	case ZoneYard:
		return b.StepsHome() + 1
	case ZoneTrack:
		if p.Step > b.LastStep() { // past its tip: once more round the ring
			return b.Ring - p.Step + b.StepsHome()
		}
		return b.StepsHome() - p.Step
	case ZoneColumn:
		return HomeDepth - p.Step
	}
	return 0
}

// Travelled is how far a piece has come, 0 in the yard up to StepsHome+1 at
// home (for ranking a game ended early and for progress bars).
func (b Board) Travelled(p Piece) int {
	return b.StepsHome() + 1 - min(b.Distance(p), b.StepsHome()+1)
}

// Cell names the spot a piece occupies, for clients and animations:
// "y<arm>.<slot>" a yard spot (slot = piece index), "r<square>" a ring
// square, "c<arm>.<step>" a home-column square, "h<arm>" home.
func (b Board) Cell(arm, slot int, p Piece) string {
	switch p.Zone {
	case ZoneTrack:
		return "r" + strconv.Itoa(b.Square(arm, p.Step))
	case ZoneColumn:
		return fmt.Sprintf("c%d.%d", arm, p.Step)
	case ZoneHome:
		return "h" + strconv.Itoa(arm)
	}
	return fmt.Sprintf("y%d.%d", arm, slot)
}

// ParseCell splits a cell name into its kind ('y', 'r', 'c' or 'h') and
// numbers; ok is false for anything else (tests and tools).
func ParseCell(cell string) (kind byte, a, b int, ok bool) {
	if len(cell) < 2 {
		return 0, 0, 0, false
	}
	kind = cell[0]
	rest := cell[1:]
	parts := strings.SplitN(rest, ".", 2)
	var err error
	if a, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, 0, false
	}
	if len(parts) == 2 {
		if b, err = strconv.Atoi(parts[1]); err != nil {
			return 0, 0, 0, false
		}
	}
	switch kind {
	case 'y', 'c':
		return kind, a, b, len(parts) == 2
	case 'r', 'h':
		return kind, a, 0, len(parts) == 1
	}
	return 0, 0, 0, false
}
