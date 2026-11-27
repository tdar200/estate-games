package engine

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Ports of the engine-only tests in test/game.test.js. Tests that need
// src/ai.js or src/rooms.js are not ported here (see the report / README of
// internal/ai and internal/server).

const testClock = int64(1_700_000_000_000)

// sequence mirrors the JS helper: values in order, then the last value forever.
func sequence(values ...float64) RNG {
	index := 0
	return RNGFunc(func() float64 {
		if index < len(values) {
			value := values[index]
			index++
			return value
		}
		if len(values) > 0 {
			return values[len(values)-1]
		}
		return 0
	})
}

func constant(value float64) RNG {
	return RNGFunc(func() float64 { return value })
}

func newTestGame(code string) *Game {
	g := CreateGame(code)
	g.Clock = func() int64 { return testClock }
	g.NewID = SequentialIDs()
	return g
}

func mustAdd(t *testing.T, g *Game, name, kind string) *Player {
	t.Helper()
	player, err := AddPlayer(g, name, kind)
	if err != nil {
		t.Fatalf("AddPlayer(%q): %v", name, err)
	}
	return player
}

func act(t *testing.T, g *Game, playerID string, action Action, rng RNG) {
	t.Helper()
	if err := PerformAction(g, playerID, action, rng); err != nil {
		t.Fatalf("PerformAction(%v): unexpected error %q", action, err)
	}
}

func actFails(t *testing.T, g *Game, playerID string, action Action, rng RNG, want string) {
	t.Helper()
	err := PerformAction(g, playerID, action, rng)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("PerformAction(%v): got error %v, want one containing %q", action, err, want)
	}
}

func finishOpeningOrder(t *testing.T, g *Game) {
	t.Helper()
	dicePairs := [][2]int{{6, 6}, {6, 5}, {6, 4}, {6, 3}, {6, 2}, {6, 1}}
	players := append([]*Player{}, g.Players...)
	for index, player := range players {
		dice := dicePairs[index]
		act(t, g, player.ID, Action{Type: ActionRollForOrder}, sequence(
			(float64(dice[0])-0.5)/6, (float64(dice[1])-0.5)/6,
		))
	}
	CompleteOpeningOrder(g, *g.OpeningOrder.CountdownEndsAt)
}

func startedGame(t *testing.T) (*Game, *Player, *Player) {
	t.Helper()
	g := newTestGame("TEST1")
	first := mustAdd(t, g, "Player A", "")
	second := mustAdd(t, g, "Player B", "")
	act(t, g, first.ID, Action{Type: ActionStart}, constant(0.5))
	finishOpeningOrder(t, g)
	return g, first, second
}

func equal[T any](t *testing.T, label string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", label, got, want)
	}
}

func landingSummary(l *Landing) Landing {
	return Landing{PlayerID: l.PlayerID, SpaceIndex: l.SpaceIndex, Kind: l.Kind, Amount: l.Amount, RecipientID: l.RecipientID}
}

func TestOpeningRollsSetTurnOrderWithCountdown(t *testing.T) {
	g := newTestGame("ORDER")
	first := mustAdd(t, g, "Player A", "")
	second := mustAdd(t, g, "Player B", "")
	act(t, g, first.ID, Action{Type: ActionStart}, constant(0.5))

	equal(t, "phase", g.Phase, PhaseDeterminingOrder)
	equal(t, "currentPlayerId", PublicState(g, first.ID, Viewer{}).CurrentPlayerID, "")
	actFails(t, g, first.ID, Action{Type: ActionRoll}, nil, "current phase")

	act(t, g, first.ID, Action{Type: ActionRollForOrder}, sequence(0, 0))
	equal(t, "pendingIds", g.OpeningOrder.PendingIDs, []string{second.ID})
	countdownStartedAt := g.Now()
	act(t, g, second.ID, Action{Type: ActionRollForOrder}, sequence(0.5, 0.5))

	equal(t, "phase", g.Phase, PhaseOrderCountdown)
	endsAt := *g.OpeningOrder.CountdownEndsAt
	if delta := endsAt - countdownStartedAt; delta < 2900 || delta > 3100 {
		t.Fatalf("countdown length %d", delta)
	}
	equal(t, "order", g.OpeningOrder.Order, []string{second.ID, first.ID})
	equal(t, "players[0]", g.Players[0].ID, second.ID)
	equal(t, "early complete", CompleteOpeningOrder(g, endsAt-1), false)
	equal(t, "phase", g.Phase, PhaseOrderCountdown)
	equal(t, "complete", CompleteOpeningOrder(g, endsAt), true)
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
	equal(t, "turn player", g.Turn.PlayerID, second.ID)
}

func TestTiedOpeningOrderRollsTieBreak(t *testing.T) {
	g := newTestGame("TIE01")
	first := mustAdd(t, g, "Player A", "")
	second := mustAdd(t, g, "Player B", "")
	act(t, g, first.ID, Action{Type: ActionStart}, constant(0.5))

	act(t, g, first.ID, Action{Type: ActionRollForOrder}, sequence(0, 0.5))
	act(t, g, second.ID, Action{Type: ActionRollForOrder}, sequence(0.34, 0.2))
	equal(t, "round", g.OpeningOrder.Round, 2)
	equal(t, "pendingIds", g.OpeningOrder.PendingIDs, []string{first.ID, second.ID})
	act(t, g, first.ID, Action{Type: ActionRollForOrder}, sequence(0, 0))
	equal(t, "pendingIds", g.OpeningOrder.PendingIDs, []string{second.ID})
	actFails(t, g, first.ID, Action{Type: ActionRollForOrder}, sequence(0, 0), "already rolled for order this round")
	act(t, g, second.ID, Action{Type: ActionRollForOrder}, sequence(0.99, 0.99))
	equal(t, "phase", g.Phase, PhaseOrderCountdown)
	equal(t, "order", g.OpeningOrder.Order, []string{second.ID, first.ID})
	var lengths []int
	for _, entry := range g.OpeningOrder.Entries {
		lengths = append(lengths, len(entry.Rolls))
	}
	equal(t, "roll counts", lengths, []int{2, 2})
}

func TestBuyPropertyAndPayRent(t *testing.T) {
	g, first, second := startedGame(t)
	act(t, g, first.ID, Action{Type: ActionRoll}, sequence(0, 0.2))
	equal(t, "pendingPurchase", *g.PendingPurchase, 3)
	equal(t, "lastLanding", landingSummary(g.LastLanding),
		Landing{PlayerID: first.ID, SpaceIndex: 3, Kind: "purchase", Amount: 60, RecipientID: ""})
	act(t, g, first.ID, Action{Type: ActionBuy}, nil)
	equal(t, "owner", g.Properties[3].OwnerID, first.ID)
	equal(t, "first cash", first.Cash, 1440)

	act(t, g, first.ID, Action{Type: ActionEndTurn}, nil)
	act(t, g, second.ID, Action{Type: ActionRoll}, sequence(0, 0.2))
	equal(t, "second cash", second.Cash, 1496)
	equal(t, "first cash", first.Cash, 1444)
	equal(t, "lastLanding", landingSummary(g.LastLanding),
		Landing{PlayerID: second.ID, SpaceIndex: 3, Kind: "rent", Amount: 4, RecipientID: first.ID})
	equal(t, "phase", g.Phase, PhaseTurnComplete)
}

func TestDecliningStartsAuction(t *testing.T) {
	g, first, second := startedGame(t)
	act(t, g, first.ID, Action{Type: ActionRoll}, sequence(0, 0.2))
	act(t, g, first.ID, Action{Type: ActionDecline}, nil)
	equal(t, "phase", g.Phase, PhaseAuction)
	equal(t, "first bidder", g.Auction.ActiveIDs[0], second.ID)

	act(t, g, second.ID, Action{Type: ActionPassAuction}, nil)
	act(t, g, first.ID, Action{Type: ActionBid, Amount: 10}, nil)
	equal(t, "owner", g.Properties[3].OwnerID, first.ID)
	equal(t, "cash", first.Cash, 1490)
	equal(t, "phase", g.Phase, PhaseTurnComplete)
}

func TestThreeDoublesSendToJail(t *testing.T) {
	g, first, _ := startedGame(t)
	g.Decks.Chest = []string{"chest-error"}
	doubleOnes := constant(0)
	act(t, g, first.ID, Action{Type: ActionRoll}, doubleOnes)
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
	act(t, g, first.ID, Action{Type: ActionRoll}, doubleOnes)
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
	act(t, g, first.ID, Action{Type: ActionRoll}, doubleOnes)
	equal(t, "inJail", first.InJail, true)
	equal(t, "position", first.Position, 10)
	equal(t, "phase", g.Phase, PhaseTurnComplete)
}

func TestMovementPathsKeptForTurn(t *testing.T) {
	g, first, second := startedGame(t)
	g.Decks.Chance = []string{"chance-back"}

	act(t, g, first.ID, Action{Type: ActionRoll}, sequence(0.34, 0.51))
	equal(t, "position", first.Position, 7)
	equal(t, "phase", g.Phase, PhaseCardConfirmation)
	equal(t, "pending player", g.PendingCard.PlayerID, first.ID)
	equal(t, "movements", len(g.Turn.Movements), 1)
	act(t, g, first.ID, Action{Type: ActionConfirmCard}, nil)

	equal(t, "position", first.Position, 4)
	equal(t, "movements", len(g.Turn.Movements), 2)
	equal(t, "path 0", g.Turn.Movements[0].Path, []int{0, 1, 2, 3, 4, 5, 6, 7})
	equal(t, "path 1", g.Turn.Movements[1].Path, []int{7, 6, 5, 4})
	equal(t, "lastMove", g.LastMove.ID, g.Turn.Movements[1].ID)

	act(t, g, first.ID, Action{Type: ActionEndTurn}, nil)
	equal(t, "current", g.Players[g.CurrentIndex].ID, second.ID)
	equal(t, "movements", g.Turn.Movements, []*Movement{})
}

func TestChanceCardsResolveAfterConfirmation(t *testing.T) {
	g, first, second := startedGame(t)
	g.Decks.Chance = []string{"chance-dividend"}

	act(t, g, first.ID, Action{Type: ActionRoll}, sequence(0.34, 0.51))
	equal(t, "position", first.Position, 7)
	equal(t, "cash", first.Cash, 1500)
	equal(t, "phase", g.Phase, PhaseCardConfirmation)
	equal(t, "pending text", PublicState(g, second.ID, Viewer{}).PendingCard.Text, "Your shares in a local bakery pay out: collect $50")
	actFails(t, g, second.ID, Action{Type: ActionConfirmCard}, nil, "Only the player who drew the Headlines card")

	act(t, g, first.ID, Action{Type: ActionConfirmCard}, nil)
	equal(t, "cash", first.Cash, 1550)
	equal(t, "pendingCard", g.PendingCard, (*PendingCard)(nil))
	equal(t, "phase", g.Phase, PhaseTurnComplete)
}

func TestCommunityChestResolvesImmediately(t *testing.T) {
	g, first, _ := startedGame(t)
	g.Decks.Chest = []string{"chest-error"}

	act(t, g, first.ID, Action{Type: ActionRoll}, sequence(0, 0))
	equal(t, "position", first.Position, 2)
	equal(t, "cash", first.Cash, 1700)
	equal(t, "landing kind", g.LastLanding.Kind, "chest")
	equal(t, "pendingCard", g.PendingCard, (*PendingCard)(nil))
	if g.Phase == PhaseCardConfirmation {
		t.Fatal("chest card entered card_confirmation")
	}
}

func TestHousesBuiltEvenly(t *testing.T) {
	g, first, _ := startedGame(t)
	g.Properties[1].OwnerID = first.ID
	g.Properties[3].OwnerID = first.ID

	act(t, g, first.ID, Action{Type: ActionBuild, SpaceIndex: 1}, nil)
	equal(t, "houses[1]", g.Properties[1].Houses, 1)
	actFails(t, g, first.ID, Action{Type: ActionBuild, SpaceIndex: 1}, nil, "distributed evenly")
	act(t, g, first.ID, Action{Type: ActionBuild, SpaceIndex: 3}, nil)
	equal(t, "houses[3]", g.Properties[3].Houses, 1)
	actFails(t, g, first.ID, Action{Type: ActionMortgage, SpaceIndex: 1}, nil, "Sell every building in the color group")
}

func TestBankruptcyAndLastPlayerWins(t *testing.T) {
	g, first, second := startedGame(t)
	g.Properties[39].OwnerID = second.ID
	g.Properties[1].OwnerID = first.ID
	g.Properties[5].OwnerID = first.ID
	first.Cash = 20
	first.Position = 33

	act(t, g, first.ID, Action{Type: ActionRoll}, constant(0.34))
	equal(t, "phase", g.Phase, PhaseDebt)
	equal(t, "debt", g.Debt.Amount, 50)
	act(t, g, first.ID, Action{Type: ActionBankrupt}, nil)
	equal(t, "bankrupt", first.Bankrupt, true)
	equal(t, "owner 1", g.Properties[1].OwnerID, second.ID)
	equal(t, "owner 5", g.Properties[5].OwnerID, second.ID)
	equal(t, "bankruptcy player", g.LastBankruptcy.PlayerID, first.ID)
	equal(t, "bankruptcy creditor", g.LastBankruptcy.CreditorID, second.ID)
	equal(t, "bankruptcy properties", g.LastBankruptcy.Properties, []int{1, 5})
	equal(t, "public lastBankruptcy", PublicState(g, second.ID, Viewer{}).LastBankruptcy, g.LastBankruptcy)
	equal(t, "status", g.Status, StatusFinished)
	equal(t, "winner", g.WinnerID, second.ID)
}

func TestChooseDiceMode(t *testing.T) {
	g := newTestGame("PICK1")
	first := mustAdd(t, g, "Player A", "")
	second := mustAdd(t, g, "Player B", "")

	actFails(t, g, second.ID, Action{Type: ActionSetDiceMode, Mode: DiceModeChoice}, nil, "Only the host")
	act(t, g, first.ID, Action{Type: ActionSetDiceMode, Mode: DiceModeChoice}, nil)
	equal(t, "dice mode", g.Settings.DiceMode, DiceModeChoice)
	act(t, g, first.ID, Action{Type: ActionStart}, constant(0.5))
	finishOpeningOrder(t, g)

	actFails(t, g, first.ID, Action{Type: ActionRoll, Dice: []int{0, 7}}, nil, "1 and 6")
	act(t, g, first.ID, Action{Type: ActionRoll, Dice: []int{1, 2}}, nil)
	equal(t, "dice", g.Turn.Dice, []int{1, 2})
	equal(t, "position", first.Position, 3)
	equal(t, "lastMove", [3]any{g.LastMove.PlayerID, g.LastMove.From, g.LastMove.To}, [3]any{first.ID, 0, 3})
}

func TestMultiPropertyTradeIsAtomic(t *testing.T) {
	g, first, second := startedGame(t)
	g.Properties[1].OwnerID = first.ID
	g.Properties[3].OwnerID = first.ID
	g.Properties[6].OwnerID = second.ID
	g.Properties[8].OwnerID = second.ID

	act(t, g, first.ID, Action{
		Type:     ActionOfferTrade,
		TargetID: second.ID,
		Offer:    &TradeBundle{Cash: 100, Properties: []int{1, 3}},
		Request:  &TradeBundle{Cash: 25, Properties: []int{6, 8}},
	}, nil)
	equal(t, "phase", g.Phase, PhaseTrade)
	act(t, g, second.ID, Action{Type: ActionAcceptTrade}, nil)

	equal(t, "owner 1", g.Properties[1].OwnerID, second.ID)
	equal(t, "owner 3", g.Properties[3].OwnerID, second.ID)
	equal(t, "owner 6", g.Properties[6].OwnerID, first.ID)
	equal(t, "owner 8", g.Properties[8].OwnerID, first.ID)
	equal(t, "first cash", first.Cash, 1425)
	equal(t, "second cash", second.Cash, 1575)
	equal(t, "phase", g.Phase, PhaseAwaitingRoll)
	equal(t, "trade", g.Trade, (*Trade)(nil))
}

func TestBuildingsBlockTradesAndMortgageFee(t *testing.T) {
	g, first, second := startedGame(t)
	g.Properties[1].OwnerID = first.ID
	g.Properties[3].OwnerID = first.ID
	g.Properties[6].OwnerID = second.ID
	g.Properties[1].Houses = 1

	offer := Action{
		Type:     ActionOfferTrade,
		TargetID: second.ID,
		Offer:    &TradeBundle{Properties: []int{1}},
		Request:  &TradeBundle{Properties: []int{6}},
	}
	actFails(t, g, first.ID, offer, nil, "sell them all first")

	g.Properties[1].Houses = 0
	g.Properties[1].Mortgaged = true
	act(t, g, first.ID, offer, nil)
	act(t, g, second.ID, Action{Type: ActionAcceptTrade}, nil)
	equal(t, "second cash", second.Cash, 1497)
	equal(t, "mortgaged", g.Properties[1].Mortgaged, true)
}

func TestTurnSummaryReportsChangesSinceLastTurn(t *testing.T) {
	g, first, second := startedGame(t)
	act(t, g, first.ID, Action{Type: ActionRoll}, sequence(0, 0.2))
	act(t, g, first.ID, Action{Type: ActionBuy}, nil)
	act(t, g, first.ID, Action{Type: ActionEndTurn}, nil)

	equal(t, "summary player", g.TurnSummary.PlayerID, second.ID)
	equal(t, "summary", g.TurnSummary.Players, []TurnSummaryEntry{{
		PlayerID: first.ID, FromPosition: 0, ToPosition: 3, CashBefore: 1500, CashAfter: 1440, CashDelta: -60,
	}})

	firstSummary := *g.TurnSummary
	firstSummary.Players = append([]TurnSummaryEntry{}, g.TurnSummary.Players...)
	act(t, g, second.ID, Action{Type: ActionRoll}, sequence(0, 0.2))
	equal(t, "summary unchanged", *g.TurnSummary, firstSummary)

	act(t, g, second.ID, Action{Type: ActionEndTurn}, nil)
	equal(t, "summary player", g.TurnSummary.PlayerID, first.ID)
	var deltas []int
	for _, entry := range g.TurnSummary.Players {
		deltas = append(deltas, entry.CashDelta)
	}
	equal(t, "deltas", deltas, []int{4, -4})
}

// ---- Go-only checks -------------------------------------------------------

// The JSON shape must match the JS object the browser client consumes.
func TestGameJSONShape(t *testing.T) {
	g := newTestGame("SHAPE")
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{
		"code", "status", "phase", "hostId", "players", "currentIndex", "properties", "turn",
		"pendingPurchase", "auction", "debt", "winnerId", "lastCard", "pendingCard", "lastLanding",
		"lastBankruptcy", "lastMove", "trade", "aiTradeCooldowns", "turnCounts", "openingOrder",
		"turnSummary", "turnSummaryBaselines", "settings", "decks", "log", "version",
	}
	if len(decoded) != len(wantKeys) {
		t.Fatalf("got %d keys, want %d: %v", len(decoded), len(wantKeys), decoded)
	}
	for _, key := range wantKeys {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("missing key %q", key)
		}
	}
	for _, key := range []string{"hostId", "turn", "pendingPurchase", "auction", "debt", "winnerId", "trade", "openingOrder"} {
		if decoded[key] != nil {
			t.Fatalf("%s should be null, got %v", key, decoded[key])
		}
	}
	properties := decoded["properties"].(map[string]any)
	if len(properties) != 28 {
		t.Fatalf("got %d properties", len(properties))
	}
	equal(t, "property 1", properties["1"], any(map[string]any{"ownerId": nil, "mortgaged": false, "houses": 0.0}))
	equal(t, "players", decoded["players"], any([]any{}))
	equal(t, "decks", decoded["decks"], any(map[string]any{"chance": []any{}, "chest": []any{}}))
}

func TestPublicStateAuctionCopyKeepsTurnPos(t *testing.T) {
	g, first, second := startedGame(t)
	g.Settings.Rules.AuctionClock = 0 // a turn-order auction (a timed one has no current player)
	act(t, g, first.ID, Action{Type: ActionRoll}, sequence(0, 0.2))
	act(t, g, first.ID, Action{Type: ActionDecline}, nil)
	act(t, g, second.ID, Action{Type: ActionBid, Amount: 5}, nil)
	// turnPos now points at the bidder; publicState normalises the live value
	// but reports the pre-normalisation copy, like the JS spread.
	g.Auction.TurnPos = 0
	view := PublicState(g, first.ID, Viewer{Role: "player"})
	equal(t, "copy turnPos", view.Auction.TurnPos, 0)
	equal(t, "current", view.Auction.CurrentPlayerID, first.ID)
	equal(t, "live turnPos", g.Auction.TurnPos, 1)
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	auction := decoded["auction"].(map[string]any)
	equal(t, "json currentPlayerId", auction["currentPlayerId"], any(first.ID))
	equal(t, "json bidderId", auction["bidderId"], any(second.ID))
	equal(t, "viewerName", decoded["viewerName"], any(nil))
	equal(t, "spectators", decoded["spectators"], any([]any{}))
}

func TestActionJSONCoercion(t *testing.T) {
	g, first, _ := startedGame(t)
	decode := func(raw string) Action {
		var action Action
		if err := json.Unmarshal([]byte(raw), &action); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		return action
	}
	for raw, want := range map[string]string{
		`null`:                                   "Invalid action format",
		`"roll"`:                                 "Invalid action format",
		`{"type":5}`:                             "Invalid action format",
		`{"type":"fly"}`:                         "Unknown action",
		`{"type":"mortgage"}`:                    "This property cannot be mortgaged",
		`{"type":"mortgage","spaceIndex":"x"}`:   "This property cannot be mortgaged",
		`{"type":"mortgage","spaceIndex":" 3 "}`: "You do not own this property",
		`{"type":"mortgage","spaceIndex":[3]}`:   "You do not own this property",
	} {
		err := PerformAction(g, first.ID, decode(raw), nil)
		if err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %q", raw, err, want)
		}
	}
	act(t, g, first.ID, decode(`{"type":"roll","dice":["1","2"]}`), sequence(0, 0.2))
	equal(t, "position", first.Position, 3)
	encoded, _ := json.Marshal(Action{Type: ActionBuild, SpaceIndex: 0})
	equal(t, "marshal", string(encoded), `{"spaceIndex":0,"type":"build"}`)
}

func TestLobbyParticipationChanges(t *testing.T) {
	g := newTestGame("LOBBY")
	host := mustAdd(t, g, "Host", "")
	ai := mustAdd(t, g, "Alpha", KindAI)
	if err := SwitchPlayerToSpectator(g, ai.ID, "spectator_x"); err == nil || err.Error() != "Player not found" {
		t.Fatalf("AI cannot become a spectator: %v", err)
	}
	if err := SwitchPlayerToSpectator(g, host.ID, "spectator_host"); err != nil {
		t.Fatal(err)
	}
	equal(t, "hostId", g.HostID, "spectator_host")
	equal(t, "color", g.Players[0].Color, PlayerColors[0])
	_, hasCount := g.TurnCounts[host.ID]
	equal(t, "turnCounts deleted", hasCount, false)
	back, err := SwitchSpectatorToPlayer(g, "Host", true)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "hostId", g.HostID, back.ID)
	if err := RemovePlayer(g, ai.ID, back.ID); err == nil || err.Error() != "Only the host can remove players" {
		t.Fatalf("got %v", err)
	}
	if err := RemovePlayer(g, back.ID, back.ID); err != nil {
		t.Fatal(err)
	}
	equal(t, "hostId after host leaves", g.HostID, "")
	equal(t, "version", g.Version, 7)
}
