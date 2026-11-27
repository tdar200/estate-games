package engine

import (
	"encoding/json"
	"testing"
)

// auctionOf39 seats the players, lets the first decline Crown Avenue (39) and
// returns the game in the auction (timed unless rules say otherwise).
func auctionOf39(t *testing.T, update RulesUpdate, names ...string) (*Game, []*Player) {
	t.Helper()
	g, p := rulesGame(t, update, names...)
	p[0].Position = 36
	act(t, g, p[0].ID, Action{Type: ActionRoll}, diceRNG(1, 2))
	act(t, g, p[0].ID, Action{Type: ActionDecline}, nil)
	equal(t, "auction", g.Phase, PhaseAuction)
	return g, p
}

func at(g *Game, ms int64) { g.Clock = func() int64 { return ms } }

func TestTimedAuctionOpenBidding(t *testing.T) {
	g, p := auctionOf39(t, RulesUpdate{}, "A", "B", "C", "D")
	a, b, c, d := p[0], p[1], p[2], p[3]
	auction := g.Auction
	equal(t, "clock", [2]any{auction.ClockMs, auction.EndsAt}, [2]any{8000, testClock + 8000})
	equal(t, "everyone may bid", AuctionBidderIDs(g), []string{b.ID, c.ID, d.ID, a.ID})
	equal(t, "no current player", PublicState(g, a.ID, Viewer{}).Auction.CurrentPlayerID, "")

	// D bids out of turn order; the clock restarts from the bid.
	at(g, testClock+2000)
	act(t, g, d.ID, Action{Type: ActionBid, Amount: 50}, nil)
	equal(t, "leader", [2]any{auction.BidderID, auction.CurrentBid}, [2]any{d.ID, 50})
	equal(t, "restarted", auction.EndsAt, testClock+2000+8000)
	equal(t, "bids", auction.Bids, map[string]int{d.ID: 50})
	equal(t, "asked next", AuctionBidderIDs(g), []string{a.ID, b.ID, c.ID})
	actFails(t, g, d.ID, Action{Type: ActionBid, Amount: 60}, nil, "You already have the top bid")
	actFails(t, g, d.ID, Action{Type: ActionPassAuction}, nil, "The top bidder cannot pass")
	actFails(t, g, b.ID, Action{Type: ActionBid, Amount: 50}, nil, "Your bid must exceed the current bid")
	actFails(t, g, b.ID, Action{Type: ActionBid, Amount: b.Cash + 1}, nil, "Your bid cannot exceed your cash")

	// The lander bids in their own auction; C drops out (the clock runs on).
	at(g, testClock+5000)
	act(t, g, a.ID, Action{Type: ActionBid, Amount: 60}, nil)
	at(g, testClock+6000)
	act(t, g, c.ID, Action{Type: ActionPassAuction}, nil)
	equal(t, "still in", auction.ActiveIDs, []string{b.ID, d.ID, a.ID})
	equal(t, "passed", auction.PassedIDs, []string{c.ID})
	equal(t, "clock untouched by a pass", auction.EndsAt, testClock+5000+8000)
	actFails(t, g, c.ID, Action{Type: ActionBid, Amount: 70}, nil, "You are not in this auction")
	equal(t, "bids", auction.Bids, map[string]int{d.ID: 50, a.ID: 60})

	// Only the server closes it, and only once the clock has run out.
	actFails(t, g, b.ID, Action{Type: ActionCloseAuction, Now: testClock + 99_000}, nil, "Only the server can close an auction")
	actFails(t, g, "", Action{Type: ActionCloseAuction, Now: testClock + 12_999}, nil, "The auction clock has not run out")
	at(g, testClock+13_000)
	actFails(t, g, b.ID, Action{Type: ActionBid, Amount: 70}, nil, "The auction has closed")
	cash := a.Cash
	act(t, g, "", Action{Type: ActionCloseAuction, Now: testClock + 13_000}, nil)
	equal(t, "a won", [3]any{g.Properties[39].OwnerID, a.Cash, g.Auction}, [3]any{a.ID, cash - 60, (*Auction)(nil)})
	equal(t, "back to the lander", [2]string{CurrentPlayer(g).ID, g.Phase}, [2]string{a.ID, PhaseTurnComplete})
	assertLog(t, g, "A won Crown Avenue for $60")
	actFails(t, g, "", Action{Type: ActionCloseAuction}, nil, "There is no timed auction to close")
}

// The auction ends at once when only the leader is left, or nobody.
func TestTimedAuctionEndsWhenOnlyLeaderLeft(t *testing.T) {
	g, p := auctionOf39(t, RulesUpdate{}, "A", "B", "C")
	a, b, c := p[0], p[1], p[2]
	act(t, g, c.ID, Action{Type: ActionBid, Amount: 10}, nil)
	act(t, g, a.ID, Action{Type: ActionPassAuction}, nil)
	act(t, g, b.ID, Action{Type: ActionPassAuction}, nil)
	equal(t, "c won", [2]any{g.Properties[39].OwnerID, g.Phase}, [2]any{c.ID, PhaseTurnComplete})

	g, p = auctionOf39(t, RulesUpdate{}, "A", "B")
	act(t, g, p[1].ID, Action{Type: ActionPassAuction}, nil)
	act(t, g, p[0].ID, Action{Type: ActionPassAuction}, nil)
	equal(t, "no bids", [2]any{g.Properties[39].OwnerID, g.Phase}, [2]any{"", PhaseTurnComplete})
	assertLog(t, g, "Crown Avenue received no bids")

	// Nobody bids before the clock runs out: the same outcome as today.
	g, _ = auctionOf39(t, RulesUpdate{}, "A", "B", "C")
	act(t, g, "", Action{Type: ActionCloseAuction, Now: g.Auction.EndsAt}, nil)
	equal(t, "no bids", [2]any{g.Properties[39].OwnerID, g.Phase}, [2]any{"", PhaseTurnComplete})
	assertLog(t, g, "Crown Avenue received no bids")
}

// A leader who resigns withdraws the bid, and the others get a full clock.
func TestTimedAuctionLeaderLeaves(t *testing.T) {
	g, p := auctionOf39(t, RulesUpdate{}, "A", "B", "C")
	b, c := p[1], p[2]
	act(t, g, c.ID, Action{Type: ActionBid, Amount: 10}, nil)
	act(t, g, b.ID, Action{Type: ActionBid, Amount: 20}, nil)
	at(g, testClock+4000)
	act(t, g, b.ID, Action{Type: ActionBankrupt}, nil)
	a := g.Auction
	equal(t, "withdrawn", [3]any{a.BidderID, a.CurrentBid, a.Bids}, [3]any{"", 0, map[string]int{c.ID: 10}})
	equal(t, "full clock", a.EndsAt, testClock+4000+8000)
	act(t, g, c.ID, Action{Type: ActionBid, Amount: 5}, nil)
}

func TestTimedAuctionJSON(t *testing.T) {
	g, p := auctionOf39(t, RulesUpdate{}, "A", "B", "C")
	act(t, g, p[2].ID, Action{Type: ActionBid, Amount: 10}, nil)
	act(t, g, p[1].ID, Action{Type: ActionPassAuction}, nil)
	equal(t, "game auction", jsonObject(t, g)["auction"], any(map[string]any{
		"spaceIndex": 39.0, "activeIds": []any{p[2].ID, p[0].ID}, "turnPos": 1.0, "currentBid": 10.0,
		"bidderId": p[2].ID, "returnPhase": PhaseTurnComplete, "clockMs": 8000.0, "endsAt": float64(testClock + 8000),
		"bids": map[string]any{p[2].ID: 10.0}, "passedIds": []any{p[1].ID},
	}))
	public := jsonObject(t, PublicState(g, p[0].ID, Viewer{}))["auction"].(map[string]any)
	equal(t, "public current player", public["currentPlayerId"], nil)
	equal(t, "public clock", [2]any{public["clockMs"], public["endsAt"]}, [2]any{8000.0, float64(testClock + 8000)})

	decoded := decodeAction(t, `{"type":"close_auction","now":1700000008000}`)
	equal(t, "decoded now", decoded.Now, int64(1_700_000_008_000))
	encoded, _ := json.Marshal(Action{Type: ActionCloseAuction, Now: 5})
	equal(t, "encoded", string(encoded), `{"now":5,"type":"close_auction"}`)
	encoded, _ = json.Marshal(Action{Type: ActionRemovePlayer, PlayerID: "player_x"})
	equal(t, "encoded remove", string(encoded), `{"playerId":"player_x","type":"remove_player"}`)
}

// auctionClock 0 keeps the turn-order auction, with none of the new keys.
func TestTurnOrderAuctionRule(t *testing.T) {
	g, p := auctionOf39(t, RulesUpdate{AuctionClock: intPtr(0)}, "A", "B", "C")
	a, b, c := p[0], p[1], p[2]
	equal(t, "turn order", [2]any{g.Auction.Timed(), AuctionBidderIDs(g)}, [2]any{false, []string{b.ID}})
	actFails(t, g, c.ID, Action{Type: ActionBid, Amount: 10}, nil, "It is not your turn to bid")
	actFails(t, g, "", Action{Type: ActionCloseAuction}, nil, "There is no timed auction to close")
	act(t, g, b.ID, Action{Type: ActionBid, Amount: 10}, nil)
	auction := jsonObject(t, g)["auction"].(map[string]any)
	for _, key := range []string{"clockMs", "endsAt", "bids", "passedIds"} {
		if _, present := auction[key]; present {
			t.Fatalf("turn-order auction has %q", key)
		}
	}
	equal(t, "current", PublicState(g, a.ID, Viewer{}).Auction.CurrentPlayerID, c.ID)
	equal(t, "turnPos", g.Auction.TurnPos, 1)
}

// The host removes a player who is not on turn: everything they had goes to
// the bank and the game goes on.
func TestRemovePlayerMidGame(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, b, c := p[0], p[1], p[2]
	g.Properties[1].OwnerID, g.Properties[3].OwnerID = b.ID, b.ID
	g.Properties[1].Houses, g.Properties[3].Houses = 2, 1
	g.Properties[5].OwnerID, g.Properties[5].Mortgaged = b.ID, true
	b.JailCards = 1
	actFails(t, g, c.ID, Action{Type: ActionRemovePlayer, PlayerID: b.ID}, nil, "Only the host can remove players")
	actFails(t, g, a.ID, Action{Type: ActionRemovePlayer, PlayerID: "nobody"}, nil, "Player not found")
	cashA, cashC := a.Cash, c.Cash
	act(t, g, a.ID, decodeAction(t, `{"type":"remove_player","playerId":"`+b.ID+`"}`), nil)
	equal(t, "out", [3]any{b.Bankrupt, b.Left, b.Cash}, [3]any{true, true, 0})
	for _, i := range []int{1, 3, 5} {
		st := g.Properties[i]
		equal(t, "to the bank", [3]any{st.OwnerID, st.Houses, st.Mortgaged}, [3]any{"", 0, false})
	}
	equal(t, "nobody paid", [2]int{a.Cash, c.Cash}, [2]int{cashA, cashC})
	equal(t, "jail card back", b.JailCards, 0)
	equal(t, "still a's turn", [2]string{CurrentPlayer(g).ID, g.Phase}, [2]string{a.ID, PhaseAwaitingRoll})
	assertLog(t, g, "B left the game; assets returned to the bank")
	equal(t, "json", jsonObject(t, g)["players"].([]any)[1].(map[string]any)["left"], any(true))
	if _, present := jsonObject(t, g)["players"].([]any)[0].(map[string]any)["left"]; present {
		t.Fatal("left should be omitted for players still in")
	}
	actFails(t, g, a.ID, Action{Type: ActionRemovePlayer, PlayerID: b.ID}, nil, "That player is already out of the game")

	// The table screen (no player id) may remove too; one left wins.
	act(t, g, "", Action{Type: ActionRemovePlayer, PlayerID: a.ID}, nil)
	equal(t, "finished", [3]string{g.Status, g.Phase, g.WinnerID}, [3]string{StatusFinished, PhaseFinished, c.ID})
	actFails(t, g, "", Action{Type: ActionRemovePlayer, PlayerID: c.ID}, nil, "The game has not started")

	lobby := newTestGame("LOBBY")
	host := mustAdd(t, lobby, "Host", "")
	actFails(t, lobby, host.ID, Action{Type: ActionRemovePlayer, PlayerID: host.ID}, nil, "The game has not started")
}

// Removing the player on turn in debt writes the debt off (the creditor gets
// nothing, everything goes to the bank) and passes the turn on.
func TestRemovePlayerInDebt(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, b, c := p[0], p[1], p[2]
	g.Properties[5].OwnerID = a.ID
	a.Position = 1
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 3)) // own railroad
	act(t, g, a.ID, Action{Type: ActionEndTurn}, nil)
	g.Properties[39].OwnerID = a.ID
	b.Position, b.Cash = 36, 10
	act(t, g, b.ID, Action{Type: ActionRoll}, diceRNG(1, 2))
	equal(t, "debt", [2]any{g.Phase, g.Debt.PayerID}, [2]any{PhaseDebt, b.ID})
	cashA := a.Cash
	act(t, g, a.ID, Action{Type: ActionRemovePlayer, PlayerID: b.ID}, nil)
	equal(t, "written off", [3]any{g.Debt, a.Cash, b.Bankrupt}, [3]any{(*Debt)(nil), cashA, true})
	equal(t, "c's turn", [2]string{CurrentPlayer(g).ID, g.Phase}, [2]string{c.ID, PhaseAwaitingRoll})
	assertLog(t, g, "B's debt of $50 was written off")
	equal(t, "to the bank", g.LastBankruptcy.CreditorID, "")
}

// Removing the leading bidder of a timed auction withdraws the bid.
func TestRemovePlayerMidAuction(t *testing.T) {
	g, p := auctionOf39(t, RulesUpdate{}, "A", "B", "C")
	a, b, c := p[0], p[1], p[2]
	act(t, g, b.ID, Action{Type: ActionBid, Amount: 30}, nil)
	act(t, g, a.ID, Action{Type: ActionRemovePlayer, PlayerID: b.ID}, nil)
	equal(t, "withdrawn", [3]any{g.Auction.BidderID, g.Auction.CurrentBid, g.Auction.ActiveIDs}, [3]any{"", 0, []string{c.ID, a.ID}})
	act(t, g, c.ID, Action{Type: ActionBid, Amount: 5}, nil)
	act(t, g, a.ID, Action{Type: ActionPassAuction}, nil)
	equal(t, "c won", g.Properties[39].OwnerID, c.ID)
}
