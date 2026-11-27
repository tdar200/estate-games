package ludo

import (
	"fmt"
	"testing"
)

// The bots: only legal answers, from the View the server gives them; Normal
// does what a decent player does in the textbook spots; Normal beats Easy
// and Hard beats Normal over enough games (cmd/ludo-arena measures it
// properly; these runs are small and the margins wide).

func TestBotsDecideFromTheirView(t *testing.T) {
	for _, level := range Levels {
		g := started(t, 2, nil)
		put(g, 0, 0, ZoneTrack, 3)
		b, err := NewBot(level, 1)
		if err != nil {
			t.Fatal(err)
		}
		v := g.View("p1", Viewer{Role: "player"})
		c, err := b.Decide(v)
		if err != nil || c.Type != "roll" || Validate(v, c) != nil {
			t.Fatalf("%s: to roll: %+v %v", level, c, err)
		}
		g.QueueDice(6)
		mustDo(t, g, "p1", Command{Type: "roll"})
		v = g.View("p1", Viewer{Role: "player"})
		c, err = b.Decide(v)
		if err != nil || c.Type != "move" || Validate(v, c) != nil {
			t.Fatalf("%s: to move: %+v %v", level, c, err)
		}
		if _, err := b.Decide(g.View("p2", Viewer{Role: "player"})); err == nil {
			t.Fatalf("%s: decided for a seat that isn't to act", level)
		}
		if _, err := b.Decide(g.View("", Viewer{Role: "table"})); err == nil {
			t.Fatalf("%s: decided without a seat", level)
		}
	}
	if _, err := NewBot("grandmaster", 1); err == nil {
		t.Fatal("unknown level")
	}
	v := &View{You: &YouView{ToMove: true}, Moves: []Move{{Piece: 2}}}
	if c, ok := Safe(v); !ok || *c.Piece != 2 {
		t.Fatal("the safe fallback moves the first movable piece")
	}
	if Validate(v, MoveCommand(1)) == nil || Validate(v, Command{Type: "roll"}) == nil || Validate(v, Command{Type: "fly"}) == nil {
		t.Fatal("validate refuses what the view doesn't allow")
	}
}

// pick asks a Normal bot (and Hard) for its choice with a queued roll.
func pick(t *testing.T, g *Game, level string, roll int) int {
	t.Helper()
	rollAs(t, g, turnID(g), roll)
	b, _ := NewBot(level, 3)
	c, err := Choose(b, g)
	if err != nil || c.Piece == nil {
		t.Fatalf("%s: %+v %v", level, c, err)
	}
	return *c.Piece
}

func TestNormalAndHardPlayTheTextbookMoves(t *testing.T) {
	for _, level := range []string{Normal, Hard} {
		// Capture rather than plod on.
		g := started(t, 2, nil)
		put(g, 0, 0, ZoneTrack, 20)
		put(g, 0, 1, ZoneTrack, 2)  // square 10
		put(g, 1, 0, ZoneTrack, 31) // square 13
		if got := pick(t, g, level, 3); got != 1 {
			t.Fatalf("%s: capture with piece 1, chose %d", level, got)
		}
		// Get a piece home.
		g = started(t, 2, nil)
		put(g, 0, 0, ZoneColumn, 3)
		put(g, 0, 1, ZoneTrack, 10)
		if got := pick(t, g, level, 3); got != 0 {
			t.Fatalf("%s: take the piece home, chose %d", level, got)
		}
		// Run from a piece right behind you (p2 two squares behind piece 0) onto a star. (Hard
		// may instead reckon on p2 overtaking and being chased; its game record decides.)
		g = started(t, 2, nil)
		put(g, 0, 0, ZoneTrack, 5)  // square 13
		put(g, 0, 1, ZoneTrack, 30) // far from everyone
		put(g, 1, 0, ZoneTrack, 29) // square 11: two behind piece 0
		if got := pick(t, g, level, 3); got != 0 && level == Normal {
			t.Fatalf("%s: escape to the star (square 16), chose %d", level, got)
		}
		// Bring a piece out on a 6 when nothing better is on.
		g = started(t, 2, nil)
		put(g, 0, 0, ZoneColumn, 1)
		if got := pick(t, g, level, 6); got == 0 {
			t.Fatalf("%s: come out with a 6 rather than inch up the column", level)
		}
	}
}

func TestThreatCountsPiecesBehindAndYards(t *testing.T) {
	g := started(t, 2, nil)
	pos := &position{r: g.rules(), players: g.Players, me: 0}
	put(g, 1, 0, ZoneTrack, 29) // square 11
	if got := pos.threat(0, Piece{Zone: ZoneTrack, Step: 5}); got < 0.16 || got > 0.17 {
		t.Fatalf("one piece two behind: 1/6, got %.3f", got)
	}
	if got := pos.threat(0, Piece{Zone: ZoneTrack, Step: 8}); got != 0 {
		t.Fatal("a star is safe")
	}
	if got := pos.threat(0, Piece{Zone: ZoneTrack, Step: 1}); got != 0 {
		t.Fatalf("nobody behind square 9 within reach: %.3f", got)
	}
	// p2's yard pieces threaten the squares just after p2's start (a 6, then the roll).
	if got := pos.threat(0, Piece{Zone: ZoneTrack, Step: 27}); got <= 0 { // square 35, one past p2's start
		t.Fatal("pieces in a yard threaten the squares after its start")
	}
}

func playMatch(t *testing.T, a, b string, games int) float64 {
	t.Helper()
	wins := 0
	for game := 0; game < games; game++ {
		g := NewGame("MATCH", Options{Seed: seedOf(byte(game))})
		g.Settings.Pieces = 4
		levels := []string{a, b}
		if game%2 == 1 {
			levels = []string{b, a}
		}
		bots := make([]Bot, 2)
		for i, level := range levels {
			g.AddPlayer(fmt.Sprintf("%s%d", level, i), KindHuman)
			bots[i], _ = NewBot(level, uint64(game*2+i))
		}
		g.Do("p1", Command{Type: "start"})
		for g.Status == StatusPlaying {
			c, err := Choose(bots[g.Turn], g)
			if err != nil {
				t.Fatal(err)
			}
			if err := g.Do(g.Current().ID, c); err != nil {
				t.Fatal(err)
			}
		}
		if levels[0] == a && g.Players[0].Place == 1 || levels[1] == a && g.Players[1].Place == 1 {
			wins++
		}
	}
	return float64(wins) / float64(games)
}

func TestNormalBeatsEasyAndHardHoldsItsOwn(t *testing.T) {
	if testing.Short() {
		t.Skip("plays a few hundred games")
	}
	if rate := playMatch(t, Normal, Easy, 200); rate < 0.75 {
		t.Fatalf("Normal won %.0f%% against Easy (cmd/ludo-arena: about 93%%)", 100*rate)
	}
	if rate := playMatch(t, Hard, Normal, 300); rate < 0.48 {
		t.Fatalf("Hard won %.0f%% against Normal (cmd/ludo-arena: about 60%%)", 100*rate)
	}
}
