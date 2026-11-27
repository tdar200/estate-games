package engine

import "testing"

func TestEndGameRichestWins(t *testing.T) {
	g := CreateGame("ENDGM")
	ann, _ := AddPlayer(g, "Ann", KindHuman)
	bob, _ := AddPlayer(g, "Bob", KindHuman)
	if _, err := EndGame(g); err == nil {
		t.Fatal("a lobby can't be ended")
	}
	g.Status, g.Phase = StatusPlaying, PhaseAwaitingRoll
	ann.Cash, bob.Cash = 900, 600
	// Bob owns Boardwalk's twin (index 39) with two houses, mortgaged nothing.
	g.Properties[39] = &PropertyState{OwnerID: bob.ID, Houses: 2}
	g.Properties[1] = &PropertyState{OwnerID: ann.ID, Mortgaged: true}
	if w := NetWorth(g, bob.ID); w != 600+Board[39].Price+2*Board[39].BuildCost {
		t.Fatalf("Bob's worth %d", w)
	}
	if w := NetWorth(g, ann.ID); w != 900+Board[1].Price/2 {
		t.Fatalf("Ann's worth %d", w)
	}
	g.Auction = &Auction{SpaceIndex: 5}
	version := g.Version
	winner, err := EndGame(g)
	if err != nil {
		t.Fatal(err)
	}
	if winner.ID != bob.ID || g.WinnerID != bob.ID || g.Status != StatusFinished || g.Phase != PhaseFinished || g.Auction != nil {
		t.Fatalf("after EndGame: winner %v status %s phase %s auction %v", winner.Name, g.Status, g.Phase, g.Auction)
	}
	if g.Version != version+1 {
		t.Fatalf("version %d → %d", version, g.Version)
	}
	if _, err := EndGame(g); err == nil {
		t.Fatal("a finished game can't be ended again")
	}
}
