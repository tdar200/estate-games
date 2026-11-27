package engine

import (
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"
)

// Regression tests for the rules bugs in docs/review/INDEPENDENT-REVIEW-2026-11-03.md
// (B1, B3, B4, B6, B10–B15) and the 8-player limit.

// dieFloat returns the rng float that rollDie maps to face.
func dieFloat(face int) float64 { return (float64(face) - 0.5) / 6 }

// diceRNG rolls the given faces in order (two per roll).
func diceRNG(faces ...int) RNG {
	values := make([]float64, len(faces))
	for i, face := range faces {
		values[i] = dieFloat(face)
	}
	return sequence(values...)
}

// seatedGame starts a game with the named seats kept in join order (prefix
// "ai:" for computer players); the first one is on turn.
func seatedGame(t *testing.T, names ...string) (*Game, []*Player) {
	t.Helper()
	return rulesGame(t, RulesUpdate{}, names...)
}

// B1: a housing shortage must not stop a hotel being sold.
func TestHotelSellsDownInHouseShortage(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, b := p[0], p[1]
	for _, i := range []int{37, 39} {
		g.Properties[i].OwnerID = a.ID
		g.Properties[i].Houses = 5
	}
	for _, i := range []int{1, 3, 16, 18, 19, 21, 23, 24} { // 32 houses: the bank has none
		g.Properties[i].OwnerID = b.ID
		g.Properties[i].Houses = 4
	}
	equal(t, "bank houses", BankSupply(g).Houses, 0)
	a.Cash = 10
	a.Position = 9
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(3, 4)) // 16 Pine Road, 4 houses
	equal(t, "phase", g.Phase, PhaseDebt)
	equal(t, "debt", g.Debt.Amount, 750)

	act(t, g, a.ID, Action{Type: ActionSellBuilding, SpaceIndex: 39}, nil)
	equal(t, "39 broken down to the bank's 0 houses", g.Properties[39].Houses, 0)
	equal(t, "cash after selling five levels", a.Cash, 10+5*Board[39].BuildCost/2)
	act(t, g, a.ID, Action{Type: ActionSellBuilding, SpaceIndex: 37}, nil)
	equal(t, "37 broken down", g.Properties[37].Houses, 0)
	equal(t, "hotels back in the bank", BankSupply(g).Hotels, 12)
	act(t, g, a.ID, Action{Type: ActionPayDebt}, nil)
	equal(t, "not bankrupt", a.Bankrupt, false)
	equal(t, "cash after paying", a.Cash, 10+1000-750)
}

func TestHotelSellsDownToTheHousesTheBankHas(t *testing.T) {
	g, p := seatedGame(t, "A", "B")
	a, b := p[0], p[1]
	for _, i := range []int{37, 39} {
		g.Properties[i].OwnerID = a.ID
		g.Properties[i].Houses = 5
	}
	// 30 houses on the board: the bank has 2.
	for _, i := range []int{1, 3, 16, 18, 19, 21, 23} {
		g.Properties[i].OwnerID = b.ID
		g.Properties[i].Houses = 4
	}
	g.Properties[24].OwnerID, g.Properties[24].Houses = b.ID, 2
	equal(t, "bank houses", BankSupply(g).Houses, 2)
	act(t, g, a.ID, Action{Type: ActionSellBuilding, SpaceIndex: 39}, nil)
	equal(t, "39 keeps the 2 houses the bank had", g.Properties[39].Houses, 2)
	equal(t, "paid for 3 levels", a.Cash, 1500+3*Board[39].BuildCost/2)
	equal(t, "bank houses now", BankSupply(g).Houses, 0)
	if !hasLog(g, "A sold the hotel on "+Board[39].Name+" for $300: the bank had only 2 houses to put back") {
		t.Fatalf("log: %q", lastLog(g))
	}
	// 39 is no longer the group maximum.
	actFails(t, g, a.ID, Action{Type: ActionSellBuilding, SpaceIndex: 39}, nil, "sold evenly")

	// With 4 or more houses in the bank a hotel still becomes 4 houses.
	g2, p2 := seatedGame(t, "A", "B")
	for _, i := range []int{37, 39} {
		g2.Properties[i].OwnerID = p2[0].ID
		g2.Properties[i].Houses = 5
	}
	act(t, g2, p2[0].ID, Action{Type: ActionSellBuilding, SpaceIndex: 39}, nil)
	equal(t, "normal hotel sale", g2.Properties[39].Houses, 4)
	equal(t, "normal log", lastLog(g2), "A sold one building on "+Board[39].Name)
}

// B4: ending the game counts an unpaid debt.
func TestEndGameCountsPendingDebt(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, b := p[0], p[1]
	for _, i := range []int{5, 15, 25, 35, 21, 23, 24, 26, 27, 29, 31, 32, 34} {
		g.Properties[i].OwnerID = a.ID
	}
	g.Properties[37].OwnerID, g.Properties[37].Houses = b.ID, 4
	g.Properties[39].OwnerID, g.Properties[39].Houses = b.ID, 5
	b.Cash = 0
	a.Cash = 500
	a.Position = 36
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 2)) // 39, hotel: $2000
	equal(t, "phase", g.Phase, PhaseDebt)
	equal(t, "debt", g.Debt.Amount, 2000)
	if NetWorth(g, a.ID) <= NetWorth(g, b.ID) {
		t.Fatal("setup: A must lead before the debt counts")
	}
	winner, err := EndGame(g)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "winner", winner.Name, "B")
	if !hasLog(g, "A's unpaid $2000 to B was counted") {
		t.Fatalf("log %v", g.Log[len(g.Log)-3:])
	}

	// A debt to the bank only lowers the debtor's score.
	g2, p2 := seatedGame(t, "A", "B")
	p2[0].Cash, p2[1].Cash = 1000, 900
	g2.Debt = &Debt{PayerID: p2[0].ID, Amount: 200, Reason: "Stamp Duty"}
	g2.Phase = PhaseDebt
	winner, err = EndGame(g2)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "bank debt winner", winner.Name, "B")
}

// B10: no bankruptcy while the cash covers the debt, for a computer (a
// person may resign at any time since 0.10.1: TestResign*).
func TestBankruptcyRefusedWhenSolvent(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, b := p[0], p[1]
	a.Kind = KindAI
	g.Properties[39].OwnerID = b.ID
	g.Properties[31].OwnerID = a.ID
	a.Cash = 20
	a.Position = 36
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 2)) // rent $50
	equal(t, "phase", g.Phase, PhaseDebt)
	act(t, g, a.ID, Action{Type: ActionMortgage, SpaceIndex: 31}, nil)
	actFails(t, g, a.ID, Action{Type: ActionBankrupt}, nil, "You have enough cash to pay this debt")
	equal(t, "still in", a.Bankrupt, false)
	act(t, g, a.ID, Action{Type: ActionPayDebt}, nil)
	equal(t, "B paid", b.Cash, 1550)
}

// B11: bail needs $50 in hand.
func TestPayBailNeedsCash(t *testing.T) {
	g, p := seatedGame(t, "A", "B")
	a := p[0]
	a.InJail, a.Position, a.Cash, a.JailCards = true, 10, 30, 1
	actFails(t, g, a.ID, Action{Type: ActionPayBail}, nil, "Not enough cash to pay bail")
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
	act(t, g, a.ID, Action{Type: ActionUseJailCard}, nil) // the other ways out still work
	equal(t, "out of jail", a.InJail, false)
}

// B12: unmortgaging costs the mortgage value plus 10%, rounded up, exactly.
func TestUnmortgageCostAndTransferFeeAreExact(t *testing.T) {
	ceilRat := func(num, den int64) int {
		q := new(big.Int)
		m := new(big.Int)
		q.DivMod(big.NewInt(num), big.NewInt(den), m)
		if m.Sign() != 0 {
			q.Add(q, big.NewInt(1))
		}
		return int(q.Int64())
	}
	seen := map[int]bool{}
	for _, space := range Board {
		if !IsOwnable(space.Type) || seen[space.Price] {
			continue
		}
		seen[space.Price] = true
		// mortgage value = price/2; cost = ceil(mv * 11/10); fee = ceil(mv / 10).
		mvTimes2 := int64(space.Price)
		equal(t, fmt.Sprintf("UnmortgageCost(%d)", space.Price), UnmortgageCost(space.Price), ceilRat(mvTimes2*11, 20))
		equal(t, fmt.Sprintf("MortgageTransferFee(%d)", space.Price), MortgageTransferFee(space.Price), ceilRat(mvTimes2, 20))
	}
	equal(t, "railroad", UnmortgageCost(200), 110)
	equal(t, "220 fee", MortgageTransferFee(220), 11)
}

// B13: a used Jail pass goes back under its deck.
func TestJailCardReturnsToDeck(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a := p[0]
	g.Decks.Chest = []string{"chest-jail-card", "chest-error"}
	a.Position = 39
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 2)) // 2: Post
	equal(t, "jail cards", a.JailCards, 1)
	equal(t, "deck after draw", g.Decks.Chest, []string{"chest-error"})
	g.Phase = PhaseAwaitingRoll
	a.InJail, a.Position = true, 10
	g.Turn = &Turn{PlayerID: a.ID, Movements: []*Movement{}}
	act(t, g, a.ID, Action{Type: ActionUseJailCard}, nil)
	equal(t, "card back at the bottom", g.Decks.Chest, []string{"chest-error", "chest-jail-card"})
}

func TestJailCardsOnBankruptcy(t *testing.T) {
	// To a player: the creditor gets the cards.
	g, p := seatedGame(t, "A", "B", "C")
	a, b := p[0], p[1]
	g.Decks.Chance = slices.DeleteFunc(append([]string{}, g.Decks.Chance...), func(id string) bool { return id == "chance-jail-card" })
	a.JailCards = 1
	g.Properties[39].OwnerID = b.ID
	a.Position, a.Cash = 36, 0
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 2)) // 39: rent $50
	act(t, g, a.ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "creditor's cards", b.JailCards, 1)
	equal(t, "debtor's cards", a.JailCards, 0)

	// To the bank: back under the decks.
	g2, p2 := seatedGame(t, "A", "B", "C")
	g2.Decks.Chance = slices.DeleteFunc(append([]string{}, g2.Decks.Chance...), func(id string) bool { return id == "chance-jail-card" })
	g2.Decks.Chest = slices.DeleteFunc(append([]string{}, g2.Decks.Chest...), func(id string) bool { return id == "chest-jail-card" })
	p2[0].JailCards = 2
	p2[0].Position, p2[0].Cash = 1, 0
	act(t, g2, p2[0].ID, Action{Type: ActionRoll}, diceRNG(1, 2)) // 4: Stamp Duty
	act(t, g2, p2[0].ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "chance bottom", g2.Decks.Chance[len(g2.Decks.Chance)-1], "chance-jail-card")
	equal(t, "chest bottom", g2.Decks.Chest[len(g2.Decks.Chest)-1], "chest-jail-card")
	equal(t, "cards", p2[0].JailCards, 0)
}

// B14: pledged collateral can't be mortgaged.
func TestCollateralCannotBeMortgaged(t *testing.T) {
	g, p := loanGame(t)
	sam := p[1]
	g.Properties[39].OwnerID = sam.ID
	lendAndAccept(t, g, sam, 400, 500, 2, 39)
	skipTo(t, g, sam)
	actFails(t, g, sam.ID, Action{Type: ActionMortgage, SpaceIndex: 39}, nil, Board[39].Name+" is security for a loan")
	equal(t, "not mortgaged", g.Properties[39].Mortgaged, false)
}

// B15: the nearest-utility card rolls again for the 10x payment.
func TestNearestUtilityCardRollsAgain(t *testing.T) {
	g, p := seatedGame(t, "A", "B", "C")
	a, b := p[0], p[1]
	g.Properties[12].OwnerID = b.ID
	g.Decks.Chance = []string{"chance-utility"}
	a.Position = 4
	act(t, g, a.ID, Action{Type: ActionRoll}, diceRNG(1, 2)) // 7: Headlines
	act(t, g, a.ID, Action{Type: ActionConfirmCard}, diceRNG(5, 6))
	equal(t, "rent is 10 x the new roll", b.Cash-1500, 110)
	if !hasLog(g, "A rolled 5 + 6 = 11 for the utility") {
		t.Fatalf("log %v", g.Log[len(g.Log)-4:])
	}
	equal(t, "turn dice unchanged", g.Turn.Dice, []int{1, 2})

	// Unowned or own utility: no roll.
	g2, p2 := seatedGame(t, "A", "B")
	g2.Decks.Chance = []string{"chance-utility"}
	p2[0].Position = 4
	act(t, g2, p2[0].ID, Action{Type: ActionRoll}, diceRNG(1, 2))
	act(t, g2, p2[0].ID, Action{Type: ActionConfirmCard}, RNGFunc(func() float64 { t.Fatal("rolled for an unowned utility"); return 0 }))
	equal(t, "purchase offered", g2.Phase, PhaseAwaitingPurchase)
}

// B6: a refused computer sale cools down like a refused purchase.
func TestRefusedAISaleCoolsDown(t *testing.T) {
	g, p := seatedGame(t, "Ann", "ai:Bot")
	ann, bot := p[0], p[1]
	skipTo(t, g, bot)
	g.Properties[1].OwnerID = bot.ID
	act(t, g, bot.ID, Action{Type: ActionOfferTrade, TargetID: ann.ID,
		Offer: &TradeBundle{Properties: []int{1}}, Request: &TradeBundle{Cash: 100}}, nil)
	act(t, g, ann.ID, Action{Type: ActionRejectTrade}, nil)
	equal(t, "sale on cooldown", AITradeOnCooldown(g, bot.ID, ann.ID, []int{1}), true)
	equal(t, "other group free", AITradeOnCooldown(g, bot.ID, ann.ID, []int{6}), false)
}

// B3: only tied players roll again, and the order data says who and why.
func TestOpeningOrderOnlyTiedPlayersRollAgain(t *testing.T) {
	g := newTestGame("TIES1")
	names := []string{"A", "B", "C", "D", "E", "F"}
	var ps []*Player
	for _, name := range names {
		ps = append(ps, mustAdd(t, g, name, ""))
	}
	act(t, g, ps[0].ID, Action{Type: ActionStart}, constant(0.5))
	order := g.OpeningOrder
	ids := func(players ...*Player) []string {
		out := []string{}
		for _, p := range players {
			out = append(out, p.ID)
		}
		return out
	}
	equal(t, "rollingIds round 1", order.RollingIDs, ids(ps...))
	equal(t, "no ties yet", order.Ties, []OpeningTie{})

	// Round 1: A=4, B=4, C=9, D=9, E=12, F=4.
	rolls := map[*Player][2]int{ps[0]: {1, 3}, ps[1]: {2, 2}, ps[2]: {4, 5}, ps[3]: {3, 6}, ps[4]: {6, 6}, ps[5]: {3, 1}}
	for _, p := range ps {
		d := rolls[p]
		act(t, g, p.ID, Action{Type: ActionRollForOrder}, diceRNG(d[0], d[1]))
	}
	equal(t, "round", order.Round, 2)
	// Tie groups in order of first appearance (as game.js), each in seat order.
	round2 := ids(ps[0], ps[1], ps[5], ps[2], ps[3])
	equal(t, "pending: only the tied", order.PendingIDs, round2)
	equal(t, "rolling", order.RollingIDs, round2)
	equal(t, "ties", order.Ties, []OpeningTie{
		{Round: 1, Total: 4, PlayerIDs: ids(ps[0], ps[1], ps[5])},
		{Round: 1, Total: 9, PlayerIDs: ids(ps[2], ps[3])},
	})
	actFails(t, g, ps[4].ID, Action{Type: ActionRollForOrder}, diceRNG(1, 1), "already rolled for order this round")

	// Round 2: A=5, B=5, F=7 (A and B tie again), C=8, D=3.
	round2Rolls := []struct {
		p *Player
		d [2]int
	}{{ps[3], [2]int{1, 2}}, {ps[0], [2]int{2, 3}}, {ps[5], [2]int{3, 4}}, {ps[2], [2]int{4, 4}}, {ps[1], [2]int{1, 4}}}
	for i, r := range round2Rolls {
		act(t, g, r.p.ID, Action{Type: ActionRollForOrder}, diceRNG(r.d[0], r.d[1]))
		if i < len(round2Rolls)-1 {
			equal(t, "rolling stays fixed while they roll", order.RollingIDs, round2)
			equal(t, "pending shrinks", len(order.PendingIDs), len(round2)-i-1)
		}
	}
	equal(t, "round 3", order.Round, 3)
	equal(t, "pending round 3", order.PendingIDs, ids(ps[0], ps[1]))
	equal(t, "rolling round 3", order.RollingIDs, ids(ps[0], ps[1]))
	equal(t, "tie history", order.Ties[2], OpeningTie{Round: 2, Total: 5, PlayerIDs: ids(ps[0], ps[1])})
	for _, p := range []*Player{ps[2], ps[3], ps[4], ps[5]} {
		actFails(t, g, p.ID, Action{Type: ActionRollForOrder}, diceRNG(1, 1), "already rolled for order this round")
	}

	act(t, g, ps[0].ID, Action{Type: ActionRollForOrder}, diceRNG(1, 1))
	act(t, g, ps[1].ID, Action{Type: ActionRollForOrder}, diceRNG(6, 5))
	equal(t, "phase", g.Phase, PhaseOrderCountdown)
	// E 12; C 9→8; D 9→3; then the 4s: F 4→7; B 4→5→11; A 4→5→2.
	equal(t, "order", order.Order, ids(ps[4], ps[2], ps[3], ps[5], ps[1], ps[0]))
	equal(t, "rolling after the order is set", order.RollingIDs, []string{})
}

func TestOpeningOrderJSONHasCleanFields(t *testing.T) {
	g := newTestGame("TIES2")
	a := mustAdd(t, g, "A", "")
	mustAdd(t, g, "B", "")
	act(t, g, a.ID, Action{Type: ActionStart}, constant(0.5))
	data := string(mustJSON(t, g.OpeningOrder))
	for _, want := range []string{`"rollingIds":[`, `"ties":[]`} {
		if !strings.Contains(data, want) {
			t.Fatalf("openingOrder JSON %s lacks %s", data, want)
		}
	}
	// Games saved before these fields existed still marshal lists, not null.
	old := &OpeningOrder{Stage: OpeningStageRolling, Round: 1}
	if data := string(mustJSON(t, old)); !strings.Contains(data, `"rollingIds":[]`) || !strings.Contains(data, `"ties":[]`) {
		t.Fatalf("old order JSON %s", data)
	}
}

// Rooms seat up to eight players, each with its own seat colour.
func TestEightPlayers(t *testing.T) {
	g := newTestGame("EIGHT")
	for i := range 8 {
		mustAdd(t, g, fmt.Sprintf("P%d", i+1), "")
	}
	_, err := AddPlayer(g, "P9", "")
	if err == nil || err.Error() != "Rooms support up to 8 players" {
		t.Fatalf("ninth player: %v", err)
	}
	colors := map[string]bool{}
	for _, p := range g.Players {
		colors[p.Color] = true
	}
	equal(t, "distinct colours", len(colors), 8)
	equal(t, "palette size", len(PlayerColors), MaxPlayers)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
