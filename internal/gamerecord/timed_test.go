package gamerecord

import (
	"strconv"
	"testing"

	"github.com/tdar200/estate-games/internal/engine"
)

// A game with the timed auction, a clock-closed auction (close_auction, as
// the server's timer sends it) and a host removal (remove_player) records and
// replays like any other.
func TestTimedAuctionAndRemovalReplay(t *testing.T) {
	lp := newLivePlay(t, []string{"ai", "ai", "ai", "ai"}, "", "")
	if !lp.g.Settings.Rules.TimedAuctions() {
		t.Fatal("a new game should have the timed auction")
	}
	closed := false
	for i := 0; i < 6000 && !closed && lp.g.Status == engine.StatusPlaying; i++ {
		if lp.g.Phase == engine.PhaseAuction && lp.g.Auction.Timed() {
			now := lp.g.Auction.EndsAt
			if err := lp.apply(t, "", `{"type":"close_auction","now":`+strconv.FormatInt(now, 10)+`}`, SourceServer); err != nil {
				t.Fatal(err)
			}
			closed = true
			break
		}
		if !lp.step(t) {
			break
		}
	}
	if !closed {
		t.Fatal("no auction to close")
	}
	lp.play(t, 50)
	var victim string
	for _, p := range lp.g.Players {
		if !p.Bankrupt && p.ID != lp.g.HostID {
			victim = p.ID
			break
		}
	}
	if err := lp.apply(t, lp.g.HostID, `{"type":"remove_player","playerId":"`+victim+`","token":"secret"}`, SourceHost); err != nil {
		t.Fatal(err)
	}
	if p := lp.g.PlayerByID(victim); !p.Left || !p.Bankrupt {
		t.Fatalf("not removed: %+v", p)
	}
	lp.play(t, 3000)

	g, err := Verify(lp.rec)
	if err != nil {
		t.Fatal(err)
	}
	if StateHash(g) != StateHash(lp.g) {
		t.Fatal("replay ended in a different state")
	}
	Annotate(lp.rec, EndExpired)
	if !lp.rec.Verified {
		t.Fatalf("annotate: %s", lp.rec.VerifyError)
	}
	sawClose, sawRemove := false, false
	for _, st := range lp.rec.Steps {
		switch st.ActionType() {
		case engine.ActionCloseAuction:
			sawClose = st.By == "" && st.Source == SourceServer
		case engine.ActionRemovePlayer:
			sawRemove = string(st.Action) == `{"playerId":"`+victim+`","type":"remove_player"}`
		}
	}
	if !sawClose || !sawRemove {
		t.Fatalf("steps: close %v remove %v", sawClose, sawRemove)
	}
	for _, a := range lp.rec.Auctions {
		if a.EndStep < 0 && lp.g.Auction == nil {
			t.Fatalf("auction left open: %+v", a)
		}
	}
	found := false
	for _, id := range lp.rec.Outcome.BankruptcyOrder {
		found = found || id == victim
	}
	if !found {
		t.Fatalf("removed player missing from the out order %v", lp.rec.Outcome.BankruptcyOrder)
	}
}
