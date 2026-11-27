package ludo

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- helpers

func seedOf(n byte) *[32]byte {
	var s [32]byte
	s[0] = n
	return &s
}

// lobby makes a lobby with n human players p1…pn (p1 hosts).
func lobby(t *testing.T, n int) *Game {
	t.Helper()
	g := NewGame("LUDO1", Options{Seed: seedOf(7), Clock: func() int64 { return 1_000 }})
	for i := 0; i < n; i++ {
		if _, err := g.AddPlayer(fmt.Sprintf("P%d", i+1), KindHuman); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func setRules(t *testing.T, g *Game, edit func(*Settings)) {
	t.Helper()
	s := g.Settings
	edit(&s)
	raw, _ := json.Marshal(s)
	if err := g.Do(g.HostID, Command{Type: "settings", Settings: raw}); err != nil {
		t.Fatal(err)
	}
}

// started is a started game with n players where p1 rolls first.
func started(t *testing.T, n int, edit func(*Settings)) *Game {
	t.Helper()
	g := lobby(t, n)
	if edit != nil {
		setRules(t, g, edit)
	}
	if err := g.Do("p1", Command{Type: "start"}); err != nil {
		t.Fatal(err)
	}
	g.Turn = 0
	return g
}

func put(g *Game, player, piece int, zone string, step int) {
	g.Players[player].Pieces[piece] = Piece{Zone: zone, Step: step}
}

func mustDo(t *testing.T, g *Game, id string, c Command) {
	t.Helper()
	if err := g.Do(id, c); err != nil {
		t.Fatalf("%s %+v: %v", id, c, err)
	}
}

func rollAs(t *testing.T, g *Game, id string, v int) {
	t.Helper()
	g.QueueDice(v)
	mustDo(t, g, id, Command{Type: "roll"})
}

func moveAs(t *testing.T, g *Game, id string, piece int) {
	t.Helper()
	mustDo(t, g, id, MoveCommand(piece))
}

func movable(g *Game) []int {
	var out []int
	for _, m := range g.Moves {
		out = append(out, m.Piece)
	}
	return out
}

func turnID(g *Game) string {
	if p := g.Current(); p != nil {
		return p.ID
	}
	return ""
}

// ---------------------------------------------------------------- board

func TestBoardGeometry(t *testing.T) {
	b := NewBoard(4)
	if b.Ring != 52 || b.LastStep() != 50 || b.StepsHome() != 56 {
		t.Fatalf("4-arm board: ring %d last %d home %d", b.Ring, b.LastStep(), b.StepsHome())
	}
	if got := []int{b.Start(0), b.Start(1), b.Start(2), b.Start(3)}; !reflect.DeepEqual(got, []int{8, 21, 34, 47}) {
		t.Fatalf("starts %v", got)
	}
	if got := b.SafeSquares(); !reflect.DeepEqual(got, []int{3, 8, 16, 21, 29, 34, 42, 47}) {
		t.Fatalf("safe squares %v (want the four starts and the four stars eight steps on)", got)
	}
	for arm := 0; arm < 4; arm++ {
		if b.Square(arm, b.LastStep()) != b.Tip(arm) {
			t.Fatalf("arm %d: last step lands on %d, not its tip %d", arm, b.Square(arm, b.LastStep()), b.Tip(arm))
		}
		if b.Star(arm) != b.Square(arm, 8) || !b.Safe(b.Star(arm)) || !b.Safe(b.Start(arm)) {
			t.Fatalf("arm %d: star %d", arm, b.Star(arm))
		}
		// The square just before a start is never visited by its own pieces except when circling.
		if b.Square(arm, b.Ring-1) != (b.Start(arm)+b.Ring-1)%b.Ring {
			t.Fatal("ring arithmetic")
		}
	}
	h := NewBoard(6)
	if h.Ring != 78 || h.StepsHome() != 82 || len(h.SafeSquares()) != 12 {
		t.Fatalf("6-arm board: ring %d home %d safe %d", h.Ring, h.StepsHome(), len(h.SafeSquares()))
	}
	if NewBoard(5).Arms != 4 {
		t.Fatal("only 4 or 6 arms")
	}
	layouts := map[[2]int][]int{{4, 2}: {0, 2}, {4, 3}: {0, 1, 2}, {4, 4}: {0, 1, 2, 3}, {6, 2}: {0, 3}, {6, 3}: {0, 2, 4}, {6, 4}: {0, 1, 3, 4}, {6, 5}: {0, 1, 2, 3, 4}, {6, 6}: {0, 1, 2, 3, 4, 5}}
	for key, want := range layouts {
		if got := NewBoard(key[0]).ArmsFor(key[1]); !reflect.DeepEqual(got, want) {
			t.Fatalf("%d arms, %d players: %v want %v", key[0], key[1], got, want)
		}
	}
	// Distances: a fresh piece is StepsHome+1 away (the step out counts), home is 0.
	if b.Distance(Piece{Zone: ZoneYard}) != 57 || b.Distance(Piece{Zone: ZoneTrack}) != 56 || b.Distance(Piece{Zone: ZoneTrack, Step: 50}) != 6 ||
		b.Distance(Piece{Zone: ZoneColumn, Step: 5}) != 1 || b.Distance(Piece{Zone: ZoneHome}) != 0 {
		t.Fatal("distances")
	}
	if b.Distance(Piece{Zone: ZoneTrack, Step: 51}) != 1+56 {
		t.Fatal("a piece past its tip has a whole lap to go")
	}
	for _, cell := range []string{"y2.3", "r51", "c1.5", "h3"} {
		if _, _, _, ok := ParseCell(cell); !ok {
			t.Fatalf("cell %s", cell)
		}
	}
	for _, bad := range []string{"", "x1", "r", "y2", "c1", "h1.2", "rq"} {
		if _, _, _, ok := ParseCell(bad); ok {
			t.Fatalf("bad cell %q parsed", bad)
		}
	}
	if b.Cell(1, 2, Piece{Zone: ZoneTrack, Step: 0}) != "r21" || b.Cell(1, 2, Piece{Zone: ZoneYard}) != "y1.2" ||
		b.Cell(1, 2, Piece{Zone: ZoneColumn, Step: 4}) != "c1.4" || b.Cell(1, 2, Piece{Zone: ZoneHome}) != "h1" {
		t.Fatal("cell names")
	}
}

// ---------------------------------------------------------------- lobby

func TestLobbySeatsRulesAndStart(t *testing.T) {
	g := lobby(t, 1)
	if g.HostID != "p1" {
		t.Fatal("first person hosts")
	}
	if err := g.Do("p1", Command{Type: "start"}); err == nil || !strings.Contains(err.Error(), "at least 2") {
		t.Fatalf("start alone: %v", err)
	}
	if _, err := g.AddPlayer("  ", KindHuman); err == nil {
		t.Fatal("empty name")
	}
	if _, err := g.AddPlayer("Bot", "robot"); err == nil {
		t.Fatal("bad kind")
	}
	id, _ := g.AddPlayer(strings.Repeat("x", 40), KindAI)
	if len(g.Player(id).Name) != maxName {
		t.Fatal("long names are cut")
	}
	if err := g.Do(id, Command{Type: "start"}); err == nil {
		t.Fatal("only the host starts")
	}
	if err := g.Do(id, Command{Type: "settings", Settings: json.RawMessage(`{"pieces":2}`)}); err == nil {
		t.Fatal("only the host sets the rules")
	}
	// Partial settings keep the rest; bad values are refused.
	mustDo(t, g, "p1", Command{Type: "settings", Settings: json.RawMessage(`{"pieces":3}`)})
	if g.Settings.Pieces != 3 || !g.Settings.SafeSquares || !g.Settings.ThreeSixes || g.Settings.Board != BoardAuto {
		t.Fatalf("partial settings: %+v", g.Settings)
	}
	for _, bad := range []string{`{"pieces":1}`, `{"pieces":5}`, `{"board":"5"}`, `nope`} {
		if err := g.Do("p1", Command{Type: "settings", Settings: json.RawMessage(bad)}); err == nil {
			t.Fatalf("settings %s accepted", bad)
		}
	}
	if len(g.Players[0].Pieces) != 3 {
		t.Fatal("lobby players hold the pieces they will play with")
	}
	// The table screen may start and set rules when nobody holds the host seat.
	if err := g.Do(TableHost, Command{Type: "roll"}); err == nil {
		t.Fatal("the table can't play")
	}
	mustDo(t, g, TableHost, Command{Type: "settings", Settings: json.RawMessage(`{"pieces":4}`)})
	mustDo(t, g, TableHost, Command{Type: "start"})
	if g.Status != StatusPlaying || g.Phase != PhaseRoll || g.Turn < 0 || g.TurnNumber != 1 {
		t.Fatalf("started: %s %s turn %d", g.Status, g.Phase, g.Turn)
	}
	if _, err := g.AddPlayer("Late", KindHuman); err == nil {
		t.Fatal("no joining after the start")
	}
	if err := g.Do("p1", Command{Type: "settings", Settings: json.RawMessage(`{"pieces":2}`)}); err == nil {
		t.Fatal("rules are fixed once playing")
	}
	if err := g.Do("p1", Command{Type: "start"}); err == nil {
		t.Fatal("can't start twice")
	}
	if err := g.Do("nobody", Command{Type: "roll"}); err == nil {
		t.Fatal("strangers can't act")
	}
	if err := g.PerformAction("p1", json.RawMessage(`{}`)); err == nil {
		t.Fatal("an action needs a type")
	}
	if err := g.Do(turnID(g), Command{Type: "dance"}); err == nil {
		t.Fatal("unknown action")
	}
}

func TestBoardChoice(t *testing.T) {
	g := lobby(t, 4)
	if g.Arms != 4 {
		t.Fatal("four players: the cross")
	}
	g.AddPlayer("P5", KindHuman)
	if g.Arms != 6 {
		t.Fatal("five players: six arms")
	}
	// Four arms can't take five.
	if err := g.Do("p1", Command{Type: "settings", Settings: json.RawMessage(`{"board":"4"}`)}); err == nil {
		t.Fatal("the 4-arm board refuses five players")
	}
	g.RemovePlayer("p1", "p5")
	mustDo(t, g, "p1", Command{Type: "settings", Settings: json.RawMessage(`{"board":"4"}`)})
	if g.SeatLimit() != 4 {
		t.Fatal("4-arm seat limit")
	}
	if _, err := g.AddPlayer("P5", KindHuman); err == nil {
		t.Fatal("table full at four on the cross")
	}
	// Six arms for two players: they face each other.
	two := lobby(t, 2)
	setRules(t, two, func(s *Settings) { s.Board = Board6 })
	mustDo(t, two, "p1", Command{Type: "start"})
	if two.Arms != 6 || two.Players[0].Arm != 0 || two.Players[1].Arm != 3 {
		t.Fatalf("six arms for two: arms %d, %d/%d", two.Arms, two.Players[0].Arm, two.Players[1].Arm)
	}
	// Six players fill the hexagon.
	six := lobby(t, 6)
	mustDo(t, six, "p1", Command{Type: "start"})
	for i, p := range six.Players {
		if p.Arm != i {
			t.Fatalf("player %d on arm %d", i, p.Arm)
		}
	}
	if six.Board().StepsHome() != 82 {
		t.Fatal("hexagon lap")
	}
}

// ---------------------------------------------------------------- coming out, sixes

func TestSixBringsAPieceOutAndRollsAgain(t *testing.T) {
	g := started(t, 2, nil)
	rollAs(t, g, "p1", 4)
	if turnID(g) != "p2" || g.Phase != PhaseRoll {
		t.Fatalf("a 4 with every piece in the yard: no move, turn passes (turn %s)", turnID(g))
	}
	g.Turn = 0
	rollAs(t, g, "p1", 6)
	if g.Phase != PhaseMove || !reflect.DeepEqual(movable(g), []int{0, 1, 2, 3}) {
		t.Fatalf("a 6 can bring any piece out: %v", movable(g))
	}
	if p, ok := g.ForcedMove(); !ok || p != 0 {
		t.Fatal("every piece in the yard: nothing to choose")
	}
	moveAs(t, g, "p1", 2)
	if got := g.Players[0].Pieces[2]; got != (Piece{Zone: ZoneTrack}) {
		t.Fatalf("out onto the start square: %+v", got)
	}
	if turnID(g) != "p1" || g.Phase != PhaseRoll || !g.LastMove.Again || !g.LastMove.Out {
		t.Fatal("a 6 gives another roll")
	}
	if g.LastMove.Cell != "r8" || !reflect.DeepEqual(g.LastMove.Path, []string{"r8"}) || g.LastMove.From != "y0.2" {
		t.Fatalf("last move %+v", g.LastMove)
	}
	rollAs(t, g, "p1", 3)
	if g.Phase != PhaseMove {
		t.Fatal("the piece out can move 3")
	}
	if _, ok := g.ForcedMove(); !ok {
		t.Fatal("only one piece can move: forced")
	}
	moveAs(t, g, "p1", 2)
	if g.Players[0].Pieces[2].Step != 3 || turnID(g) != "p2" {
		t.Fatal("a 3 moves three squares and ends the turn")
	}
	if !reflect.DeepEqual(g.LastMove.Path, []string{"r9", "r10", "r11"}) {
		t.Fatalf("hops square by square: %v", g.LastMove.Path)
	}
}

func TestKidsModeAnyRollBringsAPieceOut(t *testing.T) {
	g := started(t, 2, func(s *Settings) { s.AnyRollOut = true })
	rollAs(t, g, "p1", 2)
	if g.Phase != PhaseMove || len(g.Moves) != 4 || !g.Moves[0].Out {
		t.Fatal("kids mode: a 2 brings a piece out")
	}
	moveAs(t, g, "p1", 0)
	if g.Players[0].Pieces[0] != (Piece{Zone: ZoneTrack}) || turnID(g) != "p2" {
		t.Fatal("onto the start square; no extra roll on a 2")
	}
}

func TestASixThatCantMoveStillRollsAgain(t *testing.T) {
	g := started(t, 2, nil)
	for i := range g.Players[0].Pieces {
		put(g, 0, i, ZoneHome, 0)
	}
	put(g, 0, 3, ZoneColumn, 2) // needs exactly 4
	rollAs(t, g, "p1", 6)
	if turnID(g) != "p1" || g.Phase != PhaseRoll {
		t.Fatal("a 6 with no move: roll again")
	}
	rollAs(t, g, "p1", 5)
	if turnID(g) != "p2" {
		t.Fatal("a 5 with no move: turn passes")
	}
}

func TestThreeSixesLoseTheTurn(t *testing.T) {
	g := started(t, 2, nil)
	put(g, 0, 0, ZoneTrack, 0)
	rollAs(t, g, "p1", 6)
	moveAs(t, g, "p1", 0)
	rollAs(t, g, "p1", 6)
	moveAs(t, g, "p1", 0)
	if g.Sixes != 2 || g.Players[0].Pieces[0].Step != 12 {
		t.Fatalf("two sixes played: sixes %d step %d", g.Sixes, g.Players[0].Pieces[0].Step)
	}
	rollAs(t, g, "p1", 6)
	if turnID(g) != "p2" || g.Phase != PhaseRoll || g.Players[0].Pieces[0].Step != 12 {
		t.Fatal("the third 6 is not played and the turn passes; the first two moves stand")
	}
	if g.Sixes != 0 || g.Players[0].Sixes != 3 {
		t.Fatal("counters")
	}
	// Off: the third six is played like any other.
	h := started(t, 2, func(s *Settings) { s.ThreeSixes = false })
	put(h, 0, 0, ZoneTrack, 0)
	for i := 0; i < 3; i++ {
		rollAs(t, h, "p1", 6)
		moveAs(t, h, "p1", 0)
	}
	if h.Players[0].Pieces[0].Step != 18 || turnID(h) != "p1" {
		t.Fatal("three sixes rule off: all three played, still rolling")
	}
	// A non-6 in between breaks the chain (a capture's extra roll keeps the turn).
	k := started(t, 2, nil)
	put(k, 0, 0, ZoneTrack, 0)
	put(k, 1, 0, ZoneTrack, 31) // square 13: p1's step 5
	rollAs(t, k, "p1", 6)
	moveAs(t, k, "p1", 0)       // step 6, square 14
	put(k, 1, 0, ZoneTrack, 34) // square 16 is a star; move it to 17: arm 2 step 35
	put(k, 1, 0, ZoneTrack, 35)
	rollAs(t, k, "p1", 3) // step 9 = square 17: capture
	moveAs(t, k, "p1", 0)
	if k.Sixes != 0 || turnID(k) != "p1" {
		t.Fatal("capture: another roll, and the sixes chain was broken by the 3")
	}
}

// ---------------------------------------------------------------- captures and safe squares

func TestCaptureSendsAPieceHomeAndRollsAgain(t *testing.T) {
	g := started(t, 2, nil)
	put(g, 0, 0, ZoneTrack, 2)  // square 10
	put(g, 1, 0, ZoneTrack, 31) // arm 2: square (34+31)%52 = 13
	rollAs(t, g, "p1", 3)
	if len(g.Moves) != 1 || g.Moves[0].Capture != 1 || g.Moves[0].Safe {
		t.Fatalf("the move shows the capture: %+v", g.Moves)
	}
	moveAs(t, g, "p1", 0)
	if g.Players[1].Pieces[0].Zone != ZoneYard || g.Players[0].Captures != 1 || g.Players[1].Lost != 1 {
		t.Fatal("captured piece back in its yard")
	}
	if turnID(g) != "p1" || !g.LastMove.Again || len(g.LastMove.Captured) != 1 {
		t.Fatal("a capture gives another roll")
	}
	c := g.LastMove.Captured[0]
	if c.PlayerID != "p2" || c.Cell != "r13" || c.Yard != "y2.0" {
		t.Fatalf("captured %+v", c)
	}
}

func TestSafeSquaresProtect(t *testing.T) {
	for _, safe := range []bool{true, false} {
		g := started(t, 2, func(s *Settings) { s.SafeSquares = safe })
		put(g, 0, 0, ZoneTrack, 5)  // square 13
		put(g, 1, 0, ZoneTrack, 34) // square 16: the star of arm 0
		rollAs(t, g, "p1", 3)
		moveAs(t, g, "p1", 0)
		captured := g.Players[1].Pieces[0].Zone == ZoneYard
		if captured == safe {
			t.Fatalf("safe squares %v: captured %v", safe, captured)
		}
		if safe && turnID(g) != "p2" {
			t.Fatal("sharing a star square gives no extra roll")
		}
	}
	// Start squares are safe too: coming out next to an opponent on your start doesn't capture.
	g := started(t, 2, nil)
	put(g, 1, 0, ZoneTrack, 26) // arm 2 step 26 = square 8, p1's start
	rollAs(t, g, "p1", 6)
	moveAs(t, g, "p1", 0)
	if g.Players[1].Pieces[0].Zone != ZoneTrack {
		t.Fatal("start squares are safe")
	}
	// Without safe squares, coming out captures there.
	h := started(t, 2, func(s *Settings) { s.SafeSquares = false })
	put(h, 1, 0, ZoneTrack, 26)
	rollAs(t, h, "p1", 6)
	if h.Moves[0].Capture != 1 {
		t.Fatal("coming out onto an opponent captures when nothing is safe")
	}
	moveAs(t, h, "p1", 0)
	if h.Players[1].Pieces[0].Zone != ZoneYard {
		t.Fatal("captured on the start square")
	}
	if len(NewGame("X", Options{Seed: seedOf(1)}).View("", Viewer{}).Board.Safe) != 8 {
		t.Fatal("the view lists the safe squares")
	}
	off := started(t, 2, func(s *Settings) { s.SafeSquares = false })
	if len(off.View("", Viewer{}).Board.Safe) != 0 {
		t.Fatal("no safe squares in the view when the rule is off")
	}
}

func TestOwnPiecesStackAndCaptureTakesEveryPieceThere(t *testing.T) {
	g := started(t, 2, nil)
	put(g, 0, 0, ZoneTrack, 2)
	put(g, 0, 1, ZoneTrack, 5)
	rollAs(t, g, "p1", 3)
	moveAs(t, g, "p1", 0)
	if g.Players[0].Pieces[0] != g.Players[0].Pieces[1] {
		t.Fatal("your own pieces share a square")
	}
	// Without blocks, two of p1's pieces together (square 13) are no protection.
	put(g, 1, 0, ZoneTrack, 29) // arm 2 step 29 = square 11
	rollAs(t, g, "p2", 2)
	if g.Moves[0].Capture != 2 {
		t.Fatalf("landing on a pair captures both: %+v", g.Moves)
	}
	moveAs(t, g, "p2", 0)
	if g.Players[0].Pieces[0].Zone != ZoneYard || g.Players[0].Pieces[1].Zone != ZoneYard {
		t.Fatal("both of p1's pieces went back")
	}
}

func TestBlocks(t *testing.T) {
	setup := func(blocks bool) *Game {
		g := started(t, 2, func(s *Settings) { s.Blocks = blocks })
		put(g, 0, 0, ZoneTrack, 2)  // square 10
		put(g, 1, 0, ZoneTrack, 30) // square 12
		put(g, 1, 1, ZoneTrack, 30) // a pair on square 12
		return g
	}
	g := setup(true)
	rollAs(t, g, "p1", 3) // would pass square 12
	if g.Phase != PhaseRoll || turnID(g) != "p2" {
		t.Fatal("a block can't be passed")
	}
	g = setup(true)
	rollAs(t, g, "p1", 2) // would land on it
	if turnID(g) != "p2" {
		t.Fatal("a block can't be landed on")
	}
	g = setup(true)
	rollAs(t, g, "p1", 1)
	if g.Phase != PhaseMove {
		t.Fatal("up to the block is fine")
	}
	// Your own block doesn't stop you.
	own := started(t, 2, func(s *Settings) { s.Blocks = true })
	put(own, 0, 0, ZoneTrack, 2)
	put(own, 0, 1, ZoneTrack, 4)
	put(own, 0, 2, ZoneTrack, 4)
	rollAs(t, own, "p1", 4)
	if own.Phase != PhaseMove || len(own.Moves) != 3 {
		t.Fatalf("own pieces pass their own block: %v", movable(own))
	}
	// A block on your start square keeps your pieces in.
	start := started(t, 2, func(s *Settings) { s.Blocks = true })
	put(start, 1, 0, ZoneTrack, 26)
	put(start, 1, 1, ZoneTrack, 26)
	rollAs(t, start, "p1", 6)
	if start.Phase != PhaseRoll || turnID(start) != "p1" {
		t.Fatal("blocked start: no piece comes out (the 6 still rolls again)")
	}
	// Without the rule: passing is fine and landing captures both.
	free := setup(false)
	rollAs(t, free, "p1", 2)
	if free.Moves[0].Capture != 2 {
		t.Fatal("no blocks: landing on a pair captures both")
	}
	moveAs(t, free, "p1", 0)
	if free.Players[1].Pieces[0].Zone != ZoneYard || free.Players[1].Pieces[1].Zone != ZoneYard || free.Players[0].Captures != 2 {
		t.Fatal("both captured")
	}
}

// ---------------------------------------------------------------- going home

func TestExactNumberToReachHome(t *testing.T) {
	g := started(t, 2, nil)
	put(g, 0, 0, ZoneColumn, 3)
	rollAs(t, g, "p1", 4) // 3+4 = 7 > 6: overshoot
	if turnID(g) != "p2" {
		t.Fatal("overshooting home is not allowed")
	}
	g.Turn = 0
	rollAs(t, g, "p1", 2)
	moveAs(t, g, "p1", 0)
	if g.Players[0].Pieces[0] != (Piece{Zone: ZoneColumn, Step: 5}) {
		t.Fatal("moves up the home column")
	}
	g.Turn = 0
	g.Phase = PhaseRoll
	rollAs(t, g, "p1", 1)
	if !g.Moves[0].Home || !g.Moves[0].Safe {
		t.Fatal("the move says it reaches home")
	}
	moveAs(t, g, "p1", 0)
	if g.Players[0].Pieces[0].Zone != ZoneHome || turnID(g) != "p1" {
		t.Fatal("home, and another roll (home bonus on by default)")
	}
	// From the track into the column, and straight home on the exact number.
	h := started(t, 2, nil)
	put(h, 0, 0, ZoneTrack, 48)
	put(h, 0, 1, ZoneTrack, 50)
	rollAs(t, h, "p1", 6)
	to := map[int]Piece{}
	for _, m := range h.Moves {
		to[m.Piece] = m.To
	}
	if to[0] != (Piece{Zone: ZoneColumn, Step: 4}) || to[1] != (Piece{Zone: ZoneHome}) {
		t.Fatalf("track to column %+v, tip to home %+v", to[0], to[1])
	}
	for _, m := range h.Moves {
		if m.Piece == 0 && !reflect.DeepEqual(m.Path, []string{"r5", "r6", "c0.1", "c0.2", "c0.3", "c0.4"}) {
			t.Fatalf("path into the column %v", m.Path)
		}
		if m.Piece == 1 && !reflect.DeepEqual(m.Path, []string{"c0.1", "c0.2", "c0.3", "c0.4", "c0.5", "h0"}) {
			t.Fatalf("path home %v", m.Path)
		}
	}
	k := started(t, 2, nil)
	put(k, 0, 0, ZoneTrack, 47)
	rollAs(t, k, "p1", 3)
	moveAs(t, k, "p1", 0)
	if k.Players[0].Pieces[0] != (Piece{Zone: ZoneTrack, Step: 50}) {
		t.Fatal("exactly onto the tip stays on the ring")
	}
}

func TestHomeBonusSwitch(t *testing.T) {
	for _, bonus := range []bool{true, false} {
		g := started(t, 2, func(s *Settings) { s.HomeBonus = bonus })
		put(g, 0, 0, ZoneColumn, 4)
		rollAs(t, g, "p1", 2)
		moveAs(t, g, "p1", 0)
		if (turnID(g) == "p1") != bonus {
			t.Fatalf("home bonus %v: still p1's turn = %v", bonus, turnID(g) == "p1")
		}
	}
}

func TestCaptureFirstSendsPiecesRoundAgain(t *testing.T) {
	g := started(t, 2, func(s *Settings) { s.CaptureFirst = true })
	put(g, 0, 0, ZoneTrack, 48)
	rollAs(t, g, "p1", 4)
	if g.Moves[0].To != (Piece{Zone: ZoneTrack, Step: 0}) {
		t.Fatalf("no capture yet: past the tip and round again, %+v", g.Moves[0].To)
	}
	if !reflect.DeepEqual(g.Moves[0].Path, []string{"r5", "r6", "r7", "r8"}) {
		t.Fatalf("path past the tip %v", g.Moves[0].Path)
	}
	if g.View("p1", Viewer{}).Players[0].CanGoHome {
		t.Fatal("the view says the home column is still shut")
	}
	// Circling from the square before the start.
	put(g, 0, 1, ZoneTrack, 51)
	g.Phase, g.Moves = PhaseRoll, nil
	rollAs(t, g, "p1", 2)
	for _, m := range g.Moves {
		if m.Piece == 1 && m.To != (Piece{Zone: ZoneTrack, Step: 1}) {
			t.Fatalf("from step 51 a 2 goes to step 1, got %+v", m.To)
		}
	}
	// After a capture the column opens.
	h := started(t, 2, func(s *Settings) { s.CaptureFirst = true })
	h.Players[0].Captures = 1
	put(h, 0, 0, ZoneTrack, 48)
	rollAs(t, h, "p1", 4)
	if h.Moves[0].To != (Piece{Zone: ZoneColumn, Step: 2}) {
		t.Fatalf("after a capture: into the column, %+v", h.Moves[0].To)
	}
}

// ---------------------------------------------------------------- pieces, places, the end

func TestFewerPiecesAndPlacingOrder(t *testing.T) {
	g := started(t, 3, func(s *Settings) { s.Pieces = 2 })
	for _, p := range g.Players {
		if len(p.Pieces) != 2 {
			t.Fatal("two pieces each")
		}
	}
	put(g, 0, 0, ZoneHome, 0)
	put(g, 0, 1, ZoneColumn, 5)
	rollAs(t, g, "p1", 1)
	moveAs(t, g, "p1", 1)
	if g.Players[0].Place != 1 || g.WinnerID != "p1" || g.Status != StatusPlaying {
		t.Fatal("first home is first; play goes on for places")
	}
	if turnID(g) != "p2" {
		t.Fatal("a finished player gets no extra roll; the turn passes")
	}
	put(g, 1, 0, ZoneHome, 0)
	put(g, 1, 1, ZoneColumn, 4)
	rollAs(t, g, "p2", 2)
	moveAs(t, g, "p2", 1)
	if g.Status != StatusFinished || g.Players[1].Place != 2 || g.Players[2].Place != 3 {
		t.Fatalf("second home ends a three-player game: places %d %d %d", g.Players[0].Place, g.Players[1].Place, g.Players[2].Place)
	}
	v := g.View("", Viewer{})
	if len(v.Standings) != 3 || v.Standings[0].ID != "p1" || v.Standings[2].ID != "p3" {
		t.Fatalf("standings %+v", v.Standings)
	}
	if err := g.Do("p3", Command{Type: "roll"}); err == nil {
		t.Fatal("no rolling once it's over")
	}
	// Turn order skips finished players.
	k := started(t, 3, func(s *Settings) { s.Pieces = 2 })
	put(k, 1, 0, ZoneHome, 0)
	put(k, 1, 1, ZoneHome, 0)
	k.Players[1].Place = 1
	k.Placed = 1
	rollAs(t, k, "p1", 2)
	if turnID(k) != "p3" {
		t.Fatal("the finished p2 is skipped")
	}
}

func TestFirstWinnerEndsTheGameWhenNotPlayingOn(t *testing.T) {
	g := started(t, 3, func(s *Settings) { s.PlayOn = false; s.Pieces = 2 })
	put(g, 0, 0, ZoneHome, 0)
	put(g, 0, 1, ZoneColumn, 5)
	put(g, 1, 0, ZoneTrack, 10) // p2 further than p3
	put(g, 2, 0, ZoneTrack, 3)
	rollAs(t, g, "p1", 1)
	moveAs(t, g, "p1", 1)
	if g.Status != StatusFinished || g.WinnerID != "p1" || g.Players[1].Place != 2 || g.Players[2].Place != 3 {
		t.Fatalf("first winner ends it; the rest by progress: %d %d", g.Players[1].Place, g.Players[2].Place)
	}
}

func TestLeavingAndEndingEarly(t *testing.T) {
	g := started(t, 3, nil)
	put(g, 1, 0, ZoneTrack, 12)
	if err := g.RemovePlayer("p2", "p1"); err == nil {
		t.Fatal("only the host removes others")
	}
	mustDo(t, g, "p1", Command{Type: "roll"}) // whatever comes up
	if err := g.RemovePlayer("p1", "p1"); err != nil {
		t.Fatal(err)
	}
	if !g.Players[0].Left || turnID(g) == "p1" {
		t.Fatal("leaving on your turn passes it")
	}
	if g.HostID != "p2" {
		t.Fatal("the next person hosts")
	}
	// p2's piece on square 12 is no longer threatened or blocked by p1's pieces: they're gone.
	b := g.Board()
	for _, m := range g.rules().legal(g.Players, 2, 6) {
		if m.Capture > 0 {
			t.Fatal("pieces of someone who left can't be captured")
		}
	}
	_ = b
	if err := g.RemovePlayer("p2", "p3"); err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusFinished || g.WinnerID != "p2" || g.Players[0].Place != 3 || g.Players[2].Place != 2 {
		t.Fatalf("one left: game over (p2 1st, p3 left last so 2nd, p1 3rd): %d %d %d", g.Players[0].Place, g.Players[1].Place, g.Players[2].Place)
	}
	// END GAME: by how far the pieces got.
	e := started(t, 3, nil)
	put(e, 1, 0, ZoneColumn, 2)
	put(e, 2, 0, ZoneTrack, 30)
	put(e, 2, 1, ZoneTrack, 30)
	winner, err := e.EndGame()
	if err != nil || winner != "p3" || e.Players[1].Place != 2 || e.Players[0].Place != 3 {
		t.Fatalf("end game: winner %s err %v", winner, err)
	}
	if _, err := e.EndGame(); err == nil {
		t.Fatal("only a game in progress ends")
	}
	// Removing in the lobby frees the seat.
	l := lobby(t, 3)
	if err := l.RemovePlayer("p1", "p2"); err != nil || len(l.Players) != 2 {
		t.Fatal("lobby removal")
	}
	if err := l.RemovePlayer(TableHost, "p3"); err != nil {
		t.Fatal("the table host may remove seats")
	}
	if err := l.RemovePlayer("p1", "p9"); err == nil {
		t.Fatal("no such player")
	}
}

func TestTurnErrors(t *testing.T) {
	g := started(t, 2, nil)
	if err := g.Do("p2", Command{Type: "roll"}); err == nil || err.Error() != "it is not your turn" {
		t.Fatalf("p2 rolls out of turn: %v", err)
	}
	if err := g.Do("p1", MoveCommand(0)); err == nil {
		t.Fatal("move before rolling")
	}
	put(g, 0, 0, ZoneTrack, 0)
	put(g, 0, 1, ZoneTrack, 4)
	rollAs(t, g, "p1", 3)
	if err := g.Do("p1", Command{Type: "roll"}); err == nil {
		t.Fatal("roll again before moving")
	}
	if err := g.Do("p1", MoveCommand(3)); err == nil || err.Error() != "that piece can't move" {
		t.Fatalf("move a piece in the yard on a 3: %v", err)
	}
	if err := g.Do("p1", Command{Type: "move"}); err == nil {
		t.Fatal("move needs a piece")
	}
	if _, ok := g.ForcedMove(); ok {
		t.Fatal("two different moves: a real choice")
	}
	v := g.View("p1", Viewer{Role: "player"})
	if !v.You.ToMove || !reflect.DeepEqual(v.You.Movable, []int{0, 1}) || v.TurnID != "p1" {
		t.Fatalf("you panel %+v", v.You)
	}
	if g.View("", Viewer{Role: "table"}).You != nil {
		t.Fatal("the table has no you panel")
	}
}

// ---------------------------------------------------------------- snapshot and restore

func TestSnapshotRestoreKeepsTheDice(t *testing.T) {
	g := started(t, 4, nil)
	// Hard bots (deterministic but for a seeded hair of noise): each game gets its own set, so both make the
	// same choices from the same position.
	botsFor := func() []Bot {
		var out []Bot
		for i := 0; i < 4; i++ {
			b, _ := NewBot(Hard, uint64(i))
			out = append(out, b)
		}
		return out
	}
	play := func(g *Game, bots []Bot, steps int) {
		for i := 0; i < steps && g.Status == StatusPlaying; i++ {
			c, err := Choose(bots[g.Turn], g)
			if err != nil {
				t.Fatal(err)
			}
			mustDo(t, g, g.Current().ID, c)
		}
	}
	play(g, botsFor(), 60)
	snap, err := g.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	copyOf, err := Restore(snap, Options{Clock: func() int64 { return 1_000 }})
	if err != nil {
		t.Fatal(err)
	}
	play(g, botsFor(), 80)
	play(copyOf, botsFor(), 80)
	a, _ := g.Snapshot()
	b, _ := copyOf.Snapshot()
	if string(a) != string(b) {
		t.Fatal("a restored game rolls exactly what the original rolls")
	}
	if _, err := Restore([]byte(`{"settings":{"pieces":9,"board":"auto"}}`), Options{}); err == nil {
		t.Fatal("bad saved settings are refused")
	}
	if _, err := Restore([]byte(`nope`), Options{}); err == nil {
		t.Fatal("garbage refused")
	}
	// The public state never carries the dice stream.
	raw, _ := g.PublicState("p1", Viewer{Role: "player"})
	if strings.Contains(string(raw), `"rng"`) || strings.Contains(string(raw), `"forced"`) {
		t.Fatal("the dice stream leaked into the public state")
	}
}

func TestEventsDescribeTheMove(t *testing.T) {
	g := started(t, 2, nil)
	put(g, 0, 0, ZoneTrack, 2)
	put(g, 1, 0, ZoneTrack, 31)
	seq := g.EventSeq
	rollAs(t, g, "p1", 3)
	moveAs(t, g, "p1", 0)
	var kinds []string
	for _, e := range g.EventsSince(seq) {
		kinds = append(kinds, e.Type)
	}
	if !reflect.DeepEqual(kinds, []string{EvRoll, EvMove, EvCapture, EvAgain}) {
		t.Fatalf("events %v", kinds)
	}
	if len(g.EventsSince(g.EventSeq)) != 0 {
		t.Fatal("nothing newer")
	}
}
