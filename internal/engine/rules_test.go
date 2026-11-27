package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

// Tests for the house rules (rules.go). Helpers shared with loans_test.go.

func boolPtr(value bool) *bool { return &value }
func intPtr(value int) *int    { return &value }

func decodeAction(t *testing.T, raw string) Action {
	t.Helper()
	var action Action
	if err := json.Unmarshal([]byte(raw), &action); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return action
}

func jsonObject(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func lastLog(g *Game) string {
	if len(g.Log) == 0 {
		return ""
	}
	return g.Log[len(g.Log)-1].Text
}

func hasLog(g *Game, text string) bool {
	for _, entry := range g.Log {
		if entry.Text == text {
			return true
		}
	}
	return false
}

func assertLog(t *testing.T, g *Game, text string) {
	t.Helper()
	if !hasLog(g, text) {
		var texts []string
		for _, entry := range g.Log {
			texts = append(texts, entry.Text)
		}
		t.Fatalf("log line %q missing; log:\n  %s", text, strings.Join(texts, "\n  "))
	}
}

// rulesGame seats the named players (prefix "ai:" for computer players),
// applies the house rules as the host and plays the opening order so that
// the players keep their seating order and the first one is on turn.
func rulesGame(t *testing.T, update RulesUpdate, names ...string) (*Game, []*Player) {
	t.Helper()
	g := newTestGame("RULES")
	players := make([]*Player, len(names))
	for i, name := range names {
		kind := KindHuman
		if rest, isAI := strings.CutPrefix(name, "ai:"); isAI {
			name, kind = rest, KindAI
		}
		players[i] = mustAdd(t, g, name, kind)
	}
	act(t, g, players[0].ID, Action{Type: ActionSetRules, Rules: &update}, nil)
	act(t, g, players[0].ID, Action{Type: ActionStart}, constant(0.5))
	finishOpeningOrder(t, g)
	for i, player := range players {
		if g.Players[i] != player {
			t.Fatalf("seat %d is %s, want %s", i, g.Players[i].Name, player.Name)
		}
	}
	return g, players
}

var houseRuleKeys = []string{"freeParking", "loans", "loanOffer"}

// newGamePublicRules is publicState.rules of a game with the rules a new
// game starts with (the classic rules plus the timed auction).
var newGamePublicRules = map[string]any{
	"loans": false, "freeParkingJackpot": false, "auctions": true, "startingCash": 1500.0, "auctionClock": 8.0,
}

// assertNoHouseRuleKeys checks that a game (or a public state) plays the
// rules a new game starts with: none of the house rule keys, and the only
// stored rule is the timed auction's clock.
func assertNoHouseRuleKeys(t *testing.T, label string, object map[string]any) {
	t.Helper()
	for _, key := range houseRuleKeys {
		if _, present := object[key]; present {
			t.Fatalf("%s: unexpected key %q in %v", label, key, object[key])
		}
	}
	if rules, present := object["rules"]; present {
		equal(t, label+" rules", rules, any(newGamePublicRules))
	}
	if settings, ok := object["settings"].(map[string]any); ok {
		equal(t, label+" settings", settings, map[string]any{"diceMode": "random", "rules": map[string]any{"auctionClock": 8.0}})
	}
}

func TestHouseRulesDefaultsOmittedFromJSON(t *testing.T) {
	g := newTestGame("RULES")
	host := mustAdd(t, g, "Host", "")
	guest := mustAdd(t, g, "Guest", "")
	assertNoHouseRuleKeys(t, "new game", jsonObject(t, g))
	assertNoHouseRuleKeys(t, "new public", jsonObject(t, PublicState(g, host.ID, Viewer{})))
	equal(t, "effective", g.Settings.Rules.Effective(), NewGameRules())

	// Spelling out every default keeps only the auction clock stored.
	act(t, g, host.ID, Action{Type: ActionSetRules, Rules: &RulesUpdate{
		Loans: boolPtr(false), FreeParkingJackpot: boolPtr(false), Auctions: boolPtr(true), StartingCash: intPtr(1500),
	}}, nil)
	equal(t, "stored", g.Settings.Rules, HouseRules{AuctionClock: DefaultAuctionClock})
	equal(t, "log", lastLog(g), "Rules updated: loans off, City Park jackpot off, auctions on, starting cash $1500, auction clock 8 s")
	assertNoHouseRuleKeys(t, "defaults again", jsonObject(t, g))

	// The classic rules (turn-order auction) store nothing at all, like a
	// game saved before the clock existed, and log as they always did.
	classic := newTestGame("CLASSIC")
	classicHost := mustAdd(t, classic, "Host", "")
	act(t, classic, classicHost.ID, decodeAction(t, `{"type":"set_rules","rules":{"auctionClock":0}}`), nil)
	equal(t, "classic stored", classic.Settings.Rules, HouseRules{})
	equal(t, "classic effective", classic.Settings.Rules.Effective(), DefaultRules())
	equal(t, "classic log", lastLog(classic), "Rules updated: loans off, City Park jackpot off, auctions on, starting cash $1500")
	equal(t, "classic settings", jsonObject(t, classic)["settings"], any(map[string]any{"diceMode": "random"}))
	if _, present := jsonObject(t, PublicState(classic, classicHost.ID, Viewer{}))["rules"]; present {
		t.Fatal("classic rules should leave publicState.rules out")
	}

	// A default game played for a while still has none of the new keys.
	act(t, g, host.ID, Action{Type: ActionStart}, constant(0.5))
	finishOpeningOrder(t, g)
	act(t, g, host.ID, Action{Type: ActionRoll}, sequence(0, 0.4)) // Stamp Duty
	act(t, g, host.ID, Action{Type: ActionEndTurn}, nil)
	act(t, g, guest.ID, Action{Type: ActionRoll}, sequence(0, 0.2)) // Maple Bridge Street
	act(t, g, guest.ID, Action{Type: ActionDecline}, nil)
	equal(t, "auction", g.Phase, PhaseAuction)
	equal(t, "pot", g.FreeParking, 0)
	assertNoHouseRuleKeys(t, "default game", jsonObject(t, g))
	public := jsonObject(t, PublicState(g, guest.ID, Viewer{}))
	assertNoHouseRuleKeys(t, "default public", public)
	debt := jsonObject(t, Debt{PayerID: "p", Amount: 5, Reason: "x"})
	if _, present := debt["loanId"]; present {
		t.Fatal("debt.loanId should be omitted")
	}
}

func TestHouseRulesJSONWhenChanged(t *testing.T) {
	g := newTestGame("RULES")
	host := mustAdd(t, g, "Host", "")
	act(t, g, host.ID, decodeAction(t, `{"type":"set_rules","rules":{"loans":true,"auctions":false}}`), nil)
	equal(t, "stored", g.Settings.Rules.Effective(), RuleSet{Loans: true, Auctions: false, StartingCash: 1500, AuctionClock: 8})

	settings := jsonObject(t, g)["settings"].(map[string]any)
	equal(t, "settings.rules", settings["rules"], any(map[string]any{"loans": true, "auctions": false, "auctionClock": 8.0}))
	public := jsonObject(t, PublicState(g, host.ID, Viewer{}))
	equal(t, "public rules", public["rules"], any(map[string]any{
		"loans": true, "freeParkingJackpot": false, "auctions": false, "startingCash": 1500.0, "auctionClock": 8.0,
	}))
	for _, key := range houseRuleKeys {
		if _, present := public[key]; present {
			t.Fatalf("unused %q should still be omitted", key)
		}
	}

	// Keys left out keep their value; the full JSON round trip keeps the rules.
	act(t, g, host.ID, decodeAction(t, `{"type":"set_rules","rules":{"startingCash":"2000"}}`), nil)
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var restored Game
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	equal(t, "restored", restored.Settings.Rules.Effective(), RuleSet{Loans: true, Auctions: false, StartingCash: 2000, AuctionClock: 8})

	encoded, _ := json.Marshal(Action{Type: ActionSetRules, Rules: &RulesUpdate{Loans: boolPtr(true)}})
	equal(t, "marshal", string(encoded), `{"rules":{"loans":true},"type":"set_rules"}`)
	decoded := decodeAction(t, `{"type":"set_rules","rules":{"freeParkingJackpot":true,"startingCash":3000}}`)
	equal(t, "typed view", *decoded.Rules, RulesUpdate{FreeParkingJackpot: boolPtr(true), StartingCash: intPtr(3000)})
}

func TestSetRulesPermissionsAndValidation(t *testing.T) {
	const cashError = "Starting cash must be between $500 and $5000 in steps of $50"
	const clockError = "The auction clock must be 0 (turn order) or 5 to 30 seconds"
	g := newTestGame("RULES")
	host := mustAdd(t, g, "Host", "")
	guest := mustAdd(t, g, "Guest", "")
	cases := []struct {
		name   string
		player *Player
		action Action
		want   string
	}{
		{"not host", guest, Action{Type: ActionSetRules, Rules: &RulesUpdate{Loans: boolPtr(true)}}, "Only the host can change house rules"},
		{"go nil rules", host, Action{Type: ActionSetRules}, "Invalid house rules"},
		{"missing rules", host, decodeAction(t, `{"type":"set_rules"}`), "Invalid house rules"},
		{"rules not object", host, decodeAction(t, `{"type":"set_rules","rules":5}`), "Invalid house rules"},
		{"rules array", host, decodeAction(t, `{"type":"set_rules","rules":[true]}`), "Invalid house rules"},
		{"bool as string", host, decodeAction(t, `{"type":"set_rules","rules":{"loans":"yes"}}`), "Invalid house rules"},
		{"bool as number", host, decodeAction(t, `{"type":"set_rules","rules":{"auctions":0}}`), "Invalid house rules"},
		{"unknown key", host, decodeAction(t, `{"type":"set_rules","rules":{"jackpot":true}}`), "Unknown house rule: jackpot"},
		{"cash too low", host, decodeAction(t, `{"type":"set_rules","rules":{"startingCash":450}}`), cashError},
		{"cash too high", host, decodeAction(t, `{"type":"set_rules","rules":{"startingCash":5050}}`), cashError},
		{"cash off step", host, decodeAction(t, `{"type":"set_rules","rules":{"startingCash":1525}}`), cashError},
		{"cash fraction", host, decodeAction(t, `{"type":"set_rules","rules":{"startingCash":1500.5}}`), cashError},
		{"cash text", host, decodeAction(t, `{"type":"set_rules","rules":{"startingCash":"lots"}}`), cashError},
		{"cash bool", host, decodeAction(t, `{"type":"set_rules","rules":{"startingCash":true}}`), cashError},
		{"cash null", host, decodeAction(t, `{"type":"set_rules","rules":{"startingCash":null}}`), cashError},
		{"go cash", host, Action{Type: ActionSetRules, Rules: &RulesUpdate{StartingCash: intPtr(100)}}, cashError},
		{"bad key before good", host, decodeAction(t, `{"type":"set_rules","rules":{"loans":true,"auctions":"no"}}`), "Invalid house rules"},
		{"clock too short", host, decodeAction(t, `{"type":"set_rules","rules":{"auctionClock":4}}`), clockError},
		{"clock too long", host, decodeAction(t, `{"type":"set_rules","rules":{"auctionClock":31}}`), clockError},
		{"clock negative", host, decodeAction(t, `{"type":"set_rules","rules":{"auctionClock":-8}}`), clockError},
		{"clock fraction", host, decodeAction(t, `{"type":"set_rules","rules":{"auctionClock":7.5}}`), clockError},
		{"clock bool", host, decodeAction(t, `{"type":"set_rules","rules":{"auctionClock":true}}`), clockError},
		{"clock text", host, decodeAction(t, `{"type":"set_rules","rules":{"auctionClock":"fast"}}`), clockError},
		{"go clock", host, Action{Type: ActionSetRules, Rules: &RulesUpdate{AuctionClock: intPtr(2)}}, clockError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			version := g.Version
			err := PerformAction(g, tc.player.ID, tc.action, nil)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			equal(t, "rules unchanged", g.Settings.Rules, HouseRules{AuctionClock: DefaultAuctionClock})
			equal(t, "version unchanged", g.Version, version)
		})
	}

	for _, cash := range []int{500, 5000, 2350} {
		act(t, g, host.ID, Action{Type: ActionSetRules, Rules: &RulesUpdate{StartingCash: intPtr(cash)}}, nil)
		equal(t, "cash", g.Settings.Rules.StartingCashAmount(), cash)
	}
	act(t, g, host.ID, decodeAction(t, `{"type":"set_rules","rules":{"loans":true,"freeParkingJackpot":true,"auctions":false,"startingCash":2000}}`), nil)
	equal(t, "log", lastLog(g), "Rules updated: loans on, City Park jackpot on, auctions off, starting cash $2000, auction clock 8 s")
	for _, seconds := range []int{0, 5, 30, 12} {
		act(t, g, host.ID, Action{Type: ActionSetRules, Rules: &RulesUpdate{AuctionClock: intPtr(seconds)}}, nil)
		equal(t, "clock", g.Settings.Rules.AuctionClock, seconds)
	}
	act(t, g, host.ID, decodeAction(t, `{"type":"set_rules","rules":{"auctionClock":"8"}}`), nil)
	equal(t, "clock from text", g.Settings.Rules.AuctionClock, 8)

	act(t, g, host.ID, Action{Type: ActionStart}, constant(0.5))
	actFails(t, g, host.ID, Action{Type: ActionSetRules, Rules: &RulesUpdate{Loans: boolPtr(false)}}, nil,
		"House rules can only be changed before the game starts")
	equal(t, "rules kept", g.Settings.Rules.Effective(), RuleSet{Loans: true, FreeParkingJackpot: true, Auctions: false, StartingCash: 2000, AuctionClock: 8})
}

func TestStartingCashRule(t *testing.T) {
	g := newTestGame("CASH1")
	host := mustAdd(t, g, "Host", "")
	guest := mustAdd(t, g, "Guest", "")
	equal(t, "default", host.Cash, 1500)

	act(t, g, host.ID, Action{Type: ActionSetRules, Rules: &RulesUpdate{StartingCash: intPtr(2500)}}, nil)
	equal(t, "host re-based", host.Cash, 2500)
	equal(t, "guest re-based", guest.Cash, 2500)
	late := mustAdd(t, g, "Late", "")
	equal(t, "new seat", late.Cash, 2500)
	if err := SwitchPlayerToSpectator(g, late.ID, "spectator_1"); err != nil {
		t.Fatal(err)
	}
	back, err := SwitchSpectatorToPlayer(g, "Late", false)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "spectator seated", back.Cash, 2500)

	// Changing another rule leaves cash alone.
	host.Cash = 2500
	act(t, g, host.ID, Action{Type: ActionSetRules, Rules: &RulesUpdate{Loans: boolPtr(true)}}, nil)
	equal(t, "cash kept", guest.Cash, 2500)

	act(t, g, host.ID, Action{Type: ActionStart}, constant(0.5))
	assertLog(t, g, "Game started; every player has $2500")
	finishOpeningOrder(t, g)
	for _, player := range g.Players {
		equal(t, player.Name+" cash", player.Cash, 2500)
	}

	// Back to the default: cash 1500 and the rule disappears from JSON.
	g2 := newTestGame("CASH2")
	h2 := mustAdd(t, g2, "Host", "")
	act(t, g2, h2.ID, Action{Type: ActionSetRules, Rules: &RulesUpdate{StartingCash: intPtr(3000)}}, nil)
	act(t, g2, h2.ID, Action{Type: ActionSetRules, Rules: &RulesUpdate{StartingCash: intPtr(1500)}}, nil)
	equal(t, "cash", h2.Cash, 1500)
	assertNoHouseRuleKeys(t, "reset", jsonObject(t, g2))
	mustAdd(t, g2, "Guest", "")
	act(t, g2, h2.ID, Action{Type: ActionStart}, constant(0.5))
	assertLog(t, g2, "Game started; every player has $1500")
}

func TestFreeParkingJackpot(t *testing.T) {
	type scenario struct {
		name    string
		setup   func(g *Game, a, b *Player)
		actions func(t *testing.T, g *Game, a, b *Player)
		pot     int
		cash    int // a's cash afterwards
	}
	rollTo := func(t *testing.T, g *Game, player *Player, from int, first, second float64) {
		player.Position = from
		act(t, g, player.ID, Action{Type: ActionRoll}, sequence(first, second))
	}
	scenarios := []scenario{
		{"stamp duty", nil, func(t *testing.T, g *Game, a, _ *Player) {
			rollTo(t, g, a, 0, 0, 0.4) // 1 + 3 = 4
		}, 200, 1300},
		{"council tax", nil, func(t *testing.T, g *Game, a, _ *Player) {
			rollTo(t, g, a, 35, 0, 0.2) // 1 + 2 = 38
		}, 100, 1400},
		{"bail", func(_ *Game, a, _ *Player) { a.InJail = true; a.Position = 10 }, func(t *testing.T, g *Game, a, _ *Player) {
			act(t, g, a.ID, Action{Type: ActionPayBail}, nil)
		}, 50, 1450},
		{"third failed jail roll", func(_ *Game, a, _ *Player) { a.InJail = true; a.Position = 10; a.JailTurns = 2 },
			func(t *testing.T, g *Game, a, _ *Player) {
				act(t, g, a.ID, Action{Type: ActionRoll}, sequence(0, 0.2)) // 1 + 2: fee, then move to 13
			}, 50, 1450},
		{"card fee", func(g *Game, _, _ *Player) { g.Decks.Chest = []string{"chest-doctor"} }, func(t *testing.T, g *Game, a, _ *Player) {
			rollTo(t, g, a, 14, 0, 0.2) // Post at 17
		}, 50, 1450},
		{"chance fee", func(g *Game, _, _ *Player) { g.Decks.Chance = []string{"chance-tax"} }, func(t *testing.T, g *Game, a, _ *Player) {
			rollTo(t, g, a, 19, 0, 0.2) // Headlines at 22
			act(t, g, a.ID, Action{Type: ActionConfirmCard}, nil)
		}, 40, 1460},
		{"building repairs", func(g *Game, a, _ *Player) {
			g.Decks.Chest = []string{"chest-repairs"}
			g.Properties[1].OwnerID, g.Properties[1].Houses = a.ID, 2
			g.Properties[3].OwnerID, g.Properties[3].Houses = a.ID, 5
		}, func(t *testing.T, g *Game, a, _ *Player) {
			rollTo(t, g, a, 14, 0, 0.2)
		}, 2*40 + 115, 1500 - 195},
		{"tax paid from debt", func(g *Game, a, _ *Player) {
			a.Cash = 50
			g.Properties[39].OwnerID = a.ID
		}, func(t *testing.T, g *Game, a, _ *Player) {
			rollTo(t, g, a, 35, 0, 0.2) // Council Tax 100 with $50
			equal(t, "phase", g.Phase, PhaseDebt)
			equal(t, "pot before paying", g.FreeParking, 0)
			act(t, g, a.ID, Action{Type: ActionMortgage, SpaceIndex: 39}, nil)
			act(t, g, a.ID, Action{Type: ActionPayDebt}, nil)
		}, 100, 150},
		{"rent is not bank money", func(g *Game, _, b *Player) { g.Properties[3].OwnerID = b.ID }, func(t *testing.T, g *Game, a, _ *Player) {
			rollTo(t, g, a, 0, 0, 0.2)
		}, 0, 1496},
		{"purchases are not fees", nil, func(t *testing.T, g *Game, a, _ *Player) {
			rollTo(t, g, a, 0, 0, 0.2)
			act(t, g, a.ID, Action{Type: ActionBuy}, nil)
		}, 0, 1440},
		{"pot accumulates", func(g *Game, _, _ *Player) { g.FreeParking = 300 }, func(t *testing.T, g *Game, a, _ *Player) {
			rollTo(t, g, a, 0, 0, 0.4)
		}, 500, 1300},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			g, players := rulesGame(t, RulesUpdate{FreeParkingJackpot: boolPtr(true)}, "Ann", "Ben")
			a, b := players[0], players[1]
			if sc.setup != nil {
				sc.setup(g, a, b)
			}
			sc.actions(t, g, a, b)
			equal(t, "pot", g.FreeParking, sc.pot)
			equal(t, "cash", a.Cash, sc.cash)
			object := jsonObject(t, g)
			public := jsonObject(t, PublicState(g, b.ID, Viewer{}))
			if sc.pot > 0 {
				equal(t, "json pot", object["freeParking"], any(float64(sc.pot)))
				equal(t, "public pot", public["freeParking"], any(float64(sc.pot)))
			} else if _, present := object["freeParking"]; present {
				t.Fatal("empty pot should be omitted")
			}
		})
	}

	t.Run("collect", func(t *testing.T) {
		g, players := rulesGame(t, RulesUpdate{FreeParkingJackpot: boolPtr(true)}, "Ann", "Sam")
		ann, sam := players[0], players[1]
		act(t, g, ann.ID, Action{Type: ActionRoll}, sequence(0, 0.4)) // Stamp Duty: pot 200
		act(t, g, ann.ID, Action{Type: ActionEndTurn}, nil)
		sam.InJail = true
		sam.Position = 10
		act(t, g, sam.ID, Action{Type: ActionPayBail}, nil) // pot 250
		g.FreeParking += 200                                // more fees from earlier turns
		act(t, g, sam.ID, Action{Type: ActionRoll}, sequence(0.55, 0.9))
		equal(t, "position", sam.Position, 20)
		equal(t, "cash", sam.Cash, 1500-50+450)
		equal(t, "pot", g.FreeParking, 0)
		equal(t, "landing", landingSummary(g.LastLanding),
			Landing{PlayerID: sam.ID, SpaceIndex: 20, Kind: LandingFreeParking, Amount: 450})
		equal(t, "log", lastLog(g), "Sam collected the $450 City Park jackpot")
		equal(t, "phase", g.Phase, PhaseTurnComplete)
		if _, present := jsonObject(t, g)["freeParking"]; present {
			t.Fatal("emptied pot should be omitted")
		}

		// An empty pot is an ordinary visit.
		act(t, g, sam.ID, Action{Type: ActionEndTurn}, nil)
		ann.Position = 15
		act(t, g, ann.ID, Action{Type: ActionRoll}, sequence(0.2, 0.4))
		equal(t, "empty pot landing", g.LastLanding.Kind, "neutral")
		equal(t, "ann cash", ann.Cash, 1300)
	})

	t.Run("rule off", func(t *testing.T) {
		g, first, _ := startedGame(t)
		act(t, g, first.ID, Action{Type: ActionRoll}, sequence(0, 0.4))
		equal(t, "pot", g.FreeParking, 0)
		g.FreeParking = 100 // even a stray pot is ignored without the rule
		act(t, g, first.ID, Action{Type: ActionEndTurn}, nil)
		second := CurrentPlayer(g)
		second.Position = 15
		act(t, g, second.ID, Action{Type: ActionRoll}, sequence(0.2, 0.4))
		equal(t, "landing", g.LastLanding.Kind, "neutral")
		equal(t, "cash", second.Cash, 1500)
	})
}

func TestAuctionsOffDeclineKeepsPropertyWithBank(t *testing.T) {
	g, players := rulesGame(t, RulesUpdate{Auctions: boolPtr(false)}, "Ann", "Ben")
	ann := players[0]
	act(t, g, ann.ID, Action{Type: ActionRoll}, sequence(0, 0.2))
	equal(t, "phase", g.Phase, PhaseAwaitingPurchase)
	act(t, g, ann.ID, Action{Type: ActionDecline}, nil)
	equal(t, "phase", g.Phase, PhaseTurnComplete)
	equal(t, "auction", g.Auction, (*Auction)(nil))
	equal(t, "pending", g.PendingPurchase, (*int)(nil))
	equal(t, "owner", g.Properties[3].OwnerID, "")
	equal(t, "cash", ann.Cash, 1500)
	equal(t, "log", lastLog(g), "Ann declined Maple Bridge Street; it stays with the bank")

	// After doubles the player rolls again.
	act(t, g, ann.ID, Action{Type: ActionEndTurn}, nil)
	ben := players[1]
	act(t, g, ben.ID, Action{Type: ActionRoll}, sequence(0.34, 0.34)) // 3 + 3 = Bluestone Street
	act(t, g, ben.ID, Action{Type: ActionDecline}, nil)
	equal(t, "doubles phase", g.Phase, PhaseAwaitingRoll)
	equal(t, "owner 6", g.Properties[6].OwnerID, "")
}
