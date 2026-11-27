package ludo

import "strconv"

// The move rules act on the board, the house rules and every player's
// pieces. Game uses them for the real game; bots use the same functions on
// copies of the players while they search (sim.go), so a bot can never
// believe in a move the game would refuse.

// rules is what the move rules need besides the players.
type rules struct {
	b Board
	s Settings
}

// Move is one legal move for the roll on the table. Moves are public: every
// screen highlights the pieces the player to act may move.
type Move struct {
	Piece int `json:"piece"`
	// From and Cell are the piece's cell now and after the move; Path is
	// every cell it passes through, the destination last (one hop each).
	From string   `json:"from"`
	To   Piece    `json:"to"`
	Cell string   `json:"cell"`
	Path []string `json:"path"`
	Out  bool     `json:"out,omitempty"`  // comes out of the yard
	Home bool     `json:"home,omitempty"` // reaches home
	// Capture is how many of other players' pieces it sends back to their
	// yards.
	Capture int `json:"capture,omitempty"`
	// Safe: it ends where it can't be captured (a safe square, the home
	// column or home).
	Safe bool `json:"safe,omitempty"`
}

// mayEnter reports whether a player's pieces may turn into their home column
// (always, unless the capture-first house rule holds them back).
func (r rules) mayEnter(p *Player) bool { return !r.s.CaptureFirst || p.Captures > 0 }

// onSquare reports whether a piece stands on a given ring square.
func (r rules) onSquare(p *Player, pc Piece, square int) bool {
	return pc.Zone == ZoneTrack && r.b.Square(p.Arm, pc.Step) == square
}

// blocked reports whether another player than me has a block (two or more
// pieces) on a ring square. Without the blocks house rule nothing blocks.
func (r rules) blocked(players []*Player, me, square int) bool {
	if !r.s.Blocks {
		return false
	}
	for i, p := range players {
		if i == me || p.Left {
			continue
		}
		n := 0
		for _, pc := range p.Pieces {
			if r.onSquare(p, pc, square) {
				n++
			}
		}
		if n >= 2 {
			return true
		}
	}
	return false
}

// target is where a piece ends up with a roll, or ok=false when it can't
// move: still in the yard without the number to come out, an overshoot of
// home (the exact number is needed), home already, or a block in the way.
func (r rules) target(players []*Player, me, piece, roll int) (Piece, bool) {
	p := players[me]
	pc := p.Pieces[piece]
	switch pc.Zone {
	case ZoneYard:
		if roll != 6 && !r.s.AnyRollOut {
			return Piece{}, false
		}
		if r.blocked(players, me, r.b.Start(p.Arm)) {
			return Piece{}, false
		}
		return Piece{Zone: ZoneTrack}, true
	case ZoneTrack:
		last := r.b.LastStep()
		if r.mayEnter(p) && pc.Step <= last && pc.Step+roll > last {
			depth := pc.Step + roll - last
			if depth > HomeDepth {
				return Piece{}, false // overshoots home: the exact number is needed
			}
			for step := pc.Step + 1; step <= last; step++ {
				if r.blocked(players, me, r.b.Square(p.Arm, step)) {
					return Piece{}, false
				}
			}
			if depth == HomeDepth {
				return Piece{Zone: ZoneHome}, true
			}
			return Piece{Zone: ZoneColumn, Step: depth}, true
		}
		// Round the ring (past the tip too when the piece may not turn in yet).
		for k := 1; k <= roll; k++ {
			if r.blocked(players, me, r.b.Square(p.Arm, (pc.Step+k)%r.b.Ring)) {
				return Piece{}, false
			}
		}
		return Piece{Zone: ZoneTrack, Step: (pc.Step + roll) % r.b.Ring}, true
	case ZoneColumn:
		depth := pc.Step + roll
		switch {
		case depth > HomeDepth:
			return Piece{}, false
		case depth == HomeDepth:
			return Piece{Zone: ZoneHome}, true
		}
		return Piece{Zone: ZoneColumn, Step: depth}, true
	}
	return Piece{}, false
}

// safeAt reports whether a piece can be captured where it stands.
func (r rules) safeAt(p *Player, pc Piece) bool {
	if pc.Zone != ZoneTrack {
		return true
	}
	return r.s.SafeSquares && r.b.Safe(r.b.Square(p.Arm, pc.Step))
}

// victims counts the other players' pieces a piece arriving at to would
// capture: every piece of another player on that ring square, unless it is
// a safe square.
func (r rules) victims(players []*Player, me int, to Piece) int {
	p := players[me]
	if r.safeAt(p, to) {
		return 0
	}
	square := r.b.Square(p.Arm, to.Step)
	n := 0
	for i, q := range players {
		if i == me || q.Left {
			continue
		}
		for _, pc := range q.Pieces {
			if r.onSquare(q, pc, square) {
				n++
			}
		}
	}
	return n
}

// hit is a piece sent back to its yard.
type hit struct{ player, piece int }

// apply moves a piece to its target and sends captured pieces back to their
// yards, updating Captures and Lost. It returns the captured pieces (appended
// to buf, which may be nil) — the caller has already checked the move.
func (r rules) apply(players []*Player, me, piece int, to Piece, buf []hit) []hit {
	p := players[me]
	p.Pieces[piece] = to
	if r.safeAt(p, to) {
		return buf
	}
	square := r.b.Square(p.Arm, to.Step)
	for i, q := range players {
		if i == me || q.Left {
			continue
		}
		for j, pc := range q.Pieces {
			if r.onSquare(q, pc, square) {
				q.Pieces[j] = Piece{Zone: ZoneYard}
				q.Lost++
				p.Captures++
				buf = append(buf, hit{i, j})
			}
		}
	}
	return buf
}

// path lists the cells a piece passes through from its place to `to`.
func (r rules) path(p *Player, piece int, from, to Piece, roll int) []string {
	cell := func(pc Piece) string { return r.b.Cell(p.Arm, piece, pc) }
	switch from.Zone {
	case ZoneYard:
		return []string{cell(to)}
	case ZoneColumn:
		out := make([]string, 0, roll)
		for d := from.Step + 1; d <= from.Step+roll; d++ {
			if d >= HomeDepth {
				out = append(out, cell(Piece{Zone: ZoneHome}))
			} else {
				out = append(out, cell(Piece{Zone: ZoneColumn, Step: d}))
			}
		}
		return out
	}
	out := make([]string, 0, roll)
	if to.Zone == ZoneTrack {
		for k := 1; k <= roll; k++ {
			out = append(out, "r"+strconv.Itoa(r.b.Square(p.Arm, (from.Step+k)%r.b.Ring)))
		}
		return out
	}
	last := r.b.LastStep()
	for step := from.Step + 1; step <= last; step++ {
		out = append(out, "r"+strconv.Itoa(r.b.Square(p.Arm, step)))
	}
	for d := 1; len(out) < roll; d++ {
		if d >= HomeDepth {
			out = append(out, cell(Piece{Zone: ZoneHome}))
		} else {
			out = append(out, cell(Piece{Zone: ZoneColumn, Step: d}))
		}
	}
	return out
}

// legal lists every move the player may make with a roll, pieces in order.
func (r rules) legal(players []*Player, me, roll int) []Move {
	p := players[me]
	var moves []Move
	for i, pc := range p.Pieces {
		to, ok := r.target(players, me, i, roll)
		if !ok {
			continue
		}
		moves = append(moves, Move{
			Piece: i, From: r.b.Cell(p.Arm, i, pc), To: to, Cell: r.b.Cell(p.Arm, i, to),
			Path: r.path(p, i, pc, to, roll), Out: pc.Zone == ZoneYard, Home: to.Zone == ZoneHome,
			Capture: r.victims(players, me, to), Safe: r.safeAt(p, to),
		})
	}
	return moves
}

// sameMoves reports whether every move does the same thing (several pieces
// in the yard coming out on a 6, or two pieces stacked on one square), so
// there is nothing to choose: the server plays such a move for people too.
func sameMoves(p *Player, moves []Move) bool {
	if len(moves) == 0 {
		return false
	}
	first := p.Pieces[moves[0].Piece]
	for _, m := range moves[1:] {
		if p.Pieces[m.Piece] != first {
			return false
		}
	}
	return true
}
