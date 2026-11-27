package ludo

import (
	"encoding/json"
	"fmt"
	mrand "math/rand/v2"
	"testing"
)

// Property tests: random games under random house rules, 2–6 players, mixed
// bots, always end; after every action no piece is lost or duplicated, every
// piece is somewhere legal, two players never share an unsafe square, homes
// only fill up, and the final places are 1…n with the winner first. A
// snapshot taken mid-game restores to the same game.

func randomSettings(rng *mrand.Rand) Settings {
	s := DefaultSettings()
	s.Pieces = 2 + rng.IntN(3)
	s.Board = []string{BoardAuto, BoardAuto, Board6, Board4}[rng.IntN(4)]
	s.AnyRollOut = rng.IntN(4) == 0
	s.HomeBonus = rng.IntN(4) != 0
	s.SafeSquares = rng.IntN(4) != 0
	s.ThreeSixes = rng.IntN(4) != 0
	s.CaptureFirst = rng.IntN(4) == 0
	s.Blocks = rng.IntN(3) == 0
	s.PlayOn = rng.IntN(4) != 0
	return s
}

// checkInvariants fails the test when the game breaks a rule invariant.
func checkInvariants(t *testing.T, g *Game, homes map[string]int, label string) {
	t.Helper()
	b := g.Board()
	owners := map[int]map[string]int{}
	for _, p := range g.Players {
		if len(p.Pieces) != g.Settings.Pieces {
			t.Fatalf("%s: %s has %d pieces, want %d", label, p.ID, len(p.Pieces), g.Settings.Pieces)
		}
		for i, pc := range p.Pieces {
			switch pc.Zone {
			case ZoneYard, ZoneHome:
				if pc.Step != 0 {
					t.Fatalf("%s: %s piece %d in %s with step %d", label, p.ID, i, pc.Zone, pc.Step)
				}
			case ZoneTrack:
				if pc.Step < 0 || pc.Step >= b.Ring || (pc.Step > b.LastStep() && !g.Settings.CaptureFirst) {
					t.Fatalf("%s: %s piece %d at track step %d", label, p.ID, i, pc.Step)
				}
				if !p.Left {
					sq := b.Square(p.Arm, pc.Step)
					if owners[sq] == nil {
						owners[sq] = map[string]int{}
					}
					owners[sq][p.ID]++
				}
			case ZoneColumn:
				if pc.Step < 1 || pc.Step > ColumnLen {
					t.Fatalf("%s: %s piece %d at column step %d", label, p.ID, i, pc.Step)
				}
			default:
				t.Fatalf("%s: %s piece %d in zone %q", label, p.ID, i, pc.Zone)
			}
		}
		if h := p.HomeCount(); h < homes[p.ID] {
			t.Fatalf("%s: %s went from %d pieces home to %d", label, p.ID, homes[p.ID], h)
		} else {
			homes[p.ID] = h
		}
		if p.Place > 0 && !p.Left && g.Status == StatusPlaying && !p.allHome() {
			t.Fatalf("%s: %s placed while still playing", label, p.ID)
		}
	}
	for sq, byPlayer := range owners {
		if len(byPlayer) > 1 && !(g.Settings.SafeSquares && b.Safe(sq)) {
			t.Fatalf("%s: square %d (unsafe) holds pieces of %d players", label, sq, len(byPlayer))
		}
	}
	if g.Status == StatusPlaying {
		cur := g.Current()
		if cur == nil || !cur.active() {
			t.Fatalf("%s: the player to act is not playing", label)
		}
		if (g.Phase == PhaseMove) != (len(g.Moves) > 0) {
			t.Fatalf("%s: phase %s with %d moves", label, g.Phase, len(g.Moves))
		}
	}
}

func TestRandomGamesAlwaysEndAndKeepEveryPiece(t *testing.T) {
	games := 240
	if testing.Short() {
		games = 80
	}
	levels := []string{Easy, Normal, Hard}
	for game := 0; game < games; game++ {
		rng := mrand.New(mrand.NewPCG(uint64(game), 17))
		n := 2 + rng.IntN(5)
		s := randomSettings(rng)
		if s.Board == Board4 && n > 4 {
			s.Board = BoardAuto
		}
		g := NewGame("PROP", Options{Seed: seedOf(byte(game)), Clock: func() int64 { return 5 }})
		bots := make([]Bot, n)
		for i := 0; i < n; i++ {
			kind := KindAI
			if i == 0 {
				kind = KindHuman
			}
			if _, err := g.AddPlayer(fmt.Sprintf("P%d", i), kind); err != nil {
				t.Fatal(err)
			}
			bots[i], _ = NewBot(levels[rng.IntN(len(levels))], uint64(game*10+i))
		}
		raw, _ := json.Marshal(s)
		label := fmt.Sprintf("game %d (%d players, %+v)", game, n, s)
		if err := g.Do("p1", Command{Type: "settings", Settings: raw}); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if err := g.Do("p1", Command{Type: "start"}); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		homes := map[string]int{}
		leaveAt := -1
		if rng.IntN(5) == 0 {
			leaveAt = rng.IntN(300) // somebody walks out mid-game
		}
		steps := 0
		for ; g.Status == StatusPlaying; steps++ {
			if steps > 100000 {
				t.Fatalf("%s: no end after %d actions", label, steps)
			}
			if steps == leaveAt {
				victim := g.Players[rng.IntN(n)]
				if !victim.Left {
					if err := g.RemovePlayer(g.HostID, victim.ID); err != nil && g.HostID != "" {
						t.Fatalf("%s: remove: %v", label, err)
					}
					checkInvariants(t, g, homes, label)
					continue
				}
			}
			if steps == 150 {
				snap, err := g.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				back, err := Restore(snap, Options{Clock: func() int64 { return 5 }})
				if err != nil {
					t.Fatalf("%s: restore: %v", label, err)
				}
				again, _ := back.Snapshot()
				if string(again) != string(snap) {
					t.Fatalf("%s: snapshot round trip differs", label)
				}
			}
			seat := g.Turn
			c, err := Choose(bots[seat], g)
			if err != nil {
				t.Fatalf("%s: choose: %v", label, err)
			}
			if c.Type == "move" {
				if err := Validate(g.View(g.Players[seat].ID, Viewer{Role: "player"}), c); err != nil {
					t.Fatalf("%s: bot chose an illegal move: %v", label, err)
				}
			}
			if err := g.Do(g.Players[seat].ID, c); err != nil {
				t.Fatalf("%s: step %d %+v: %v", label, steps, c, err)
			}
			checkInvariants(t, g, homes, label)
		}
		// The end: places 1…n, each once; the winner is first.
		seen := map[int]bool{}
		for _, p := range g.Players {
			if p.Place < 1 || p.Place > n || seen[p.Place] {
				t.Fatalf("%s: bad place %d for %s", label, p.Place, p.ID)
			}
			seen[p.Place] = true
			if p.Place == 1 && p.ID != g.WinnerID {
				t.Fatalf("%s: winner %s is not first", label, g.WinnerID)
			}
		}
		if s.PlayOn && leaveAt < 0 {
			finished := 0
			for _, p := range g.Players {
				if p.allHome() {
					finished++
				}
			}
			if finished < n-1 {
				t.Fatalf("%s: playing on, only %d of %d got every piece home", label, finished, n)
			}
		}
		if g.Phase != PhaseFinished || g.Turn != -1 || len(g.Moves) != 0 {
			t.Fatalf("%s: finished state %s %d %d", label, g.Phase, g.Turn, len(g.Moves))
		}
	}
}

// Every legal move the engine offers is one the target function allows, and
// every piece the target function allows is offered (no move missing).
func TestLegalMovesMatchTheRules(t *testing.T) {
	for game := 0; game < 60; game++ {
		rng := mrand.New(mrand.NewPCG(uint64(game), 3))
		g := NewGame("LEGAL", Options{Seed: seedOf(byte(game + 100))})
		n := 2 + rng.IntN(3)
		for i := 0; i < n; i++ {
			g.AddPlayer(fmt.Sprintf("P%d", i), KindHuman)
		}
		raw, _ := json.Marshal(randomSettings(rng))
		if g.Do("p1", Command{Type: "settings", Settings: raw}) != nil {
			continue
		}
		g.Do("p1", Command{Type: "start"})
		bot, _ := NewBot(Easy, uint64(game))
		for steps := 0; steps < 400 && g.Status == StatusPlaying; steps++ {
			if g.Phase == PhaseMove {
				r := g.rules()
				want := 0
				for i := range g.Current().Pieces {
					if _, ok := r.target(g.Players, g.Turn, i, g.Dice); ok {
						want++
					}
				}
				if want != len(g.Moves) {
					t.Fatalf("game %d: %d moves offered, %d allowed", game, len(g.Moves), want)
				}
				for _, m := range g.Moves {
					if len(m.Path) != g.Dice && !m.Out {
						t.Fatalf("game %d: path of %d cells for a %d: %v", game, len(m.Path), g.Dice, m.Path)
					}
					if m.Path[len(m.Path)-1] != m.Cell {
						t.Fatalf("game %d: path ends at %s, move says %s", game, m.Path[len(m.Path)-1], m.Cell)
					}
				}
			}
			c, _ := Choose(bot, g)
			if err := g.Do(g.Current().ID, c); err != nil {
				t.Fatal(err)
			}
		}
	}
}
