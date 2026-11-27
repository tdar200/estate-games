package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
)

// joinPhone seats a phone player at a table from its own address.
func joinPhone(t *testing.T, h *harness, tb table, name, token, ip string) person {
	t.Helper()
	d := h.device(name, token, ip)
	r := h.post("/api/rooms/"+tb.code+"/join", map[string]any{"name": name, "tokenKind": token}, d.opt(), fromIP(ip))
	expectStatus(t, r, http.StatusOK)
	return person{playerID: r.str("playerId"), token: r.str("token"), ip: ip}
}

// playPast plays the people's moves (declining every deed) until done holds.
func playPast(t *testing.T, h *harness, code string, people []person, timeout time.Duration, done func(*gameView) bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		view := h.view(code)
		if done(view) {
			return
		}
		if view.Status != "playing" {
			t.Fatalf("the game ended (%s)", view.Status)
		}
		for _, p := range people {
			if ids, _ := actorsToMove(view); view.Phase == "awaiting_purchase" && slices.Contains(ids, p.playerID) {
				h.personAct(code, p, map[string]any{"type": "decline"})
				continue
			}
			h.personMove(code, p, view)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}

// The timed open auction over the server: anyone may bid at any moment
// (currentPlayerId is null), a computer bids too, and when nobody bids for
// the auction clock the server closes it with a recorded close_auction step;
// the record replays.
func TestTimedAuctionOnServer(t *testing.T) {
	dir := t.TempDir()
	h := recordingHarness(t, dir)
	tb := h.createTable()
	alice := joinPhone(t, h, tb, "Alice", "key", "198.51.100.71")
	bob := joinPhone(t, h, tb, "Bob", "anchor", "198.51.100.72")
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), http.StatusOK)
	h.dial("/ws/rooms/"+tb.code+"?token="+alice.token, nil).next(2 * time.Second)
	h.dial("/ws/rooms/"+tb.code+"?token="+bob.token, nil).next(2 * time.Second)
	st := h.state(tb.code, tb.token)
	if rules, _ := st["rules"].(map[string]any); rules["auctionClock"] != float64(engine.DefaultAuctionClock) {
		t.Fatalf("a new table's rules: %v", st["rules"])
	}
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "set_rules", "rules": map[string]any{"auctionClock": engine.MinAuctionClock}}), http.StatusOK)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	people := []person{alice, bob}
	playPast(t, h, tb.code, people, 60*time.Second, func(v *gameView) bool { return v.Phase == "auction" })

	view := h.view(tb.code)
	space := view.Auction.SpaceIndex
	auction, _ := h.state(tb.code, alice.token)["auction"].(map[string]any)
	if auction["clockMs"] != float64(engine.MinAuctionClock*1000) || auction["currentPlayerId"] != nil || auction["endsAt"].(float64) <= 0 {
		t.Fatalf("auction in state: %v", auction)
	}
	// Alice bids out of turn (whoever's seat is next in turn order).
	bid := false
	for i := 0; i < 20 && !bid; i++ {
		v := h.view(tb.code)
		if v.Phase != "auction" {
			t.Fatal("the auction ended before Alice bid")
		}
		if v.Auction.BidderID == alice.playerID {
			bid = true
			break
		}
		bid = h.personAct(tb.code, alice, map[string]any{"type": "bid", "amount": v.Auction.CurrentBid + 1}).status == http.StatusOK
	}
	if !bid {
		t.Fatal("Alice could not bid out of turn")
	}
	// A client can't close the auction itself.
	expectError(t, h.personAct(tb.code, bob, map[string]any{"type": "close_auction"}), http.StatusBadRequest, msgServerOnly)
	// Nobody else bids for the clock (the computer at most outbids her up to
	// its limit): the server closes the auction.
	waitFor(t, "the clock to close the auction", 30*time.Second, func() bool { return h.view(tb.code).Phase != "auction" })
	view = h.view(tb.code)
	winner := view.Properties[strconv.Itoa(space)].OwnerID
	if winner == "" || winner == bob.playerID {
		t.Fatalf("deed %d went to %q", space, winner)
	}

	endGame(t, h, tb.code, tb.token)
	names := finishedRecords(t, h, dir)
	if len(names) != 1 {
		t.Fatalf("records %v", names)
	}
	rec := readRecord(t, dir, names[0])
	if _, err := gamerecord.Verify(rec); err != nil {
		t.Fatalf("replay: %v", err)
	}
	closes := 0
	for _, step := range rec.Steps {
		if step.ActionType() != engine.ActionCloseAuction {
			continue
		}
		closes++
		var action struct {
			Now int64 `json:"now"`
		}
		if json.Unmarshal(step.Action, &action) != nil || step.Source != gamerecord.SourceServer || step.By != "" || action.Now < step.At-1000 || step.Error != "" {
			t.Fatalf("close step %+v (%s)", step, step.Action)
		}
	}
	if closes != 1 {
		t.Fatalf("%d close_auction steps", closes)
	}
	if a := rec.Auctions[0]; a.WinnerID != winner || len(a.Bids) == 0 {
		t.Fatalf("auction summary %+v", a)
	}
}

// Classic rooms keep the Node server's behaviour: turn-order auctions (the
// classic client asks auction.currentPlayerId who may bid), no mid-game
// removal and no host controls.
func TestClassicRoomKeepsTurnOrderAuctions(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.Engine = RealEngine{} })
	host := h.createRoom("Ann")
	ben := h.join(host.code, "Ben")
	st := h.state(host.code, host.token)
	if st["rules"] != nil || dig(st, "settings", "rules") != nil {
		t.Fatalf("classic room rules %v, settings %v", st["rules"], st["settings"])
	}
	expectStatus(t, h.act(host.code, host.token, map[string]any{"type": "start"}), http.StatusOK)
	expectError(t, h.post("/api/rooms/"+host.code+"/remove", map[string]any{"token": host.token, "playerId": ben.playerID}), http.StatusBadRequest, "Players cannot be removed after the game starts")
	expectError(t, h.post("/api/rooms/"+host.code+"/standin", map[string]any{"token": host.token, "playerId": ben.playerID}), http.StatusBadRequest, msgHostControlsGame)
}

// uxTable is a started table with two connected phones (Alice and Bob) and
// a computer, past the turn order.
func uxTable(t *testing.T) (*harness, table, person, person, *socket, *room) {
	t.Helper()
	h := newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.AIDelay = 2 * time.Millisecond
	})
	tb := h.createTable()
	alice := joinPhone(t, h, tb, "Alice", "key", "198.51.100.81")
	bob := joinPhone(t, h, tb, "Bob", "anchor", "198.51.100.82")
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token}), http.StatusOK)
	aSock := h.dial("/ws/rooms/"+tb.code+"?token="+alice.token, nil)
	aSock.next(2 * time.Second)
	h.dial("/ws/rooms/"+tb.code+"?token="+bob.token, nil).next(2 * time.Second)
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	playPast(t, h, tb.code, []person{alice, bob}, 30*time.Second, func(v *gameView) bool {
		return v.Phase == "awaiting_roll" || v.Phase == "turn_complete"
	})
	h.srv.mu.Lock()
	r := h.srv.rooms[tb.code]
	h.srv.mu.Unlock()
	return h, tb, alice, bob, aSock, r
}

// waitsOn reports whether the game waits on playerID alone.
func waitsOn(v *gameView, playerID string) bool {
	ids, _ := actorsToMove(v)
	return len(ids) == 1 && ids[0] == playerID
}

// Host controls: Computer plays (and I'M BACK), Skip turn, and the
// while-away mark.
// (At a table the first phone to join, Alice, is the host player; the
// table screen acts as host too.)
func TestHostStandInSkipAndBack(t *testing.T) {
	h, tb, alice, bob, _, r := uxTable(t)
	post := func(path string, token, playerID string) response {
		return h.post("/api/rooms/"+tb.code+"/"+path, map[string]any{"token": token, "playerId": playerID})
	}
	expectError(t, post("standin", bob.token, alice.playerID), http.StatusBadRequest, msgOnlyHost)
	expectError(t, post("skip", bob.token, alice.playerID), http.StatusBadRequest, msgOnlyHost)
	aiID := ""
	for _, p := range h.view(tb.code).Players {
		if p.Kind == "ai" {
			aiID = p.ID
		}
	}
	expectError(t, post("standin", tb.token, aiID), http.StatusBadRequest, msgHostTarget)

	// Wait for Bob's decision, then the host has the computer play for him.
	playPast(t, h, tb.code, []person{alice}, 30*time.Second, func(v *gameView) bool { return waitsOn(v, bob.playerID) })
	before := h.view(tb.code)
	expectStatus(t, post("standin", tb.token, bob.playerID), http.StatusOK)
	p := playerByID(h.state(tb.code, tb.token), bob.playerID)
	mark, _ := p["standIn"].(map[string]any)
	if p["autopilot"] != true || mark["by"] != "host" || mark["logId"] != before.Log[len(before.Log)-1].ID || mark["until"] != nil || mark["since"].(float64) <= 0 {
		t.Fatalf("Bob after Computer plays: %v", p)
	}
	waitFor(t, "the stand-in to move for Bob", 10*time.Second, func() bool { return !waitsOn(h.view(tb.code), bob.playerID) })
	if !autopilotOn(h, r, bob.playerID) {
		t.Fatal("the stand-in stopped by itself (a host stand-in stays until Bob's command)")
	}

	// Let the computers play on until the game waits on Alice (a person): only then
	// does the log stand still, so a new line can only come from I'M BACK.
	waitFor(t, "the game to wait on Alice", 30*time.Second, func() bool { return waitsOn(h.view(tb.code), alice.playerID) })

	// I'M BACK hands the seat back and does nothing else; the mark stays
	// (with until) for the while-away list, until Bob's next command.
	version := h.view(tb.code).Version
	log := len(h.view(tb.code).Log)
	expectStatus(t, h.personAct(tb.code, bob, map[string]any{"type": "back"}), http.StatusOK)
	if autopilotOn(h, r, bob.playerID) || len(h.view(tb.code).Log) != log {
		t.Fatal("I'M BACK did more than hand the seat back")
	}
	p = playerByID(h.state(tb.code, bob.token), bob.playerID)
	if mark, _ := p["standIn"].(map[string]any); p["autopilot"] == true || mark == nil || mark["until"] == nil {
		t.Fatalf("Bob after I'M BACK: %v", p)
	}
	if h.view(tb.code).Version == version {
		t.Fatal("I'M BACK was not published")
	}
	expectStatus(t, h.personAct(tb.code, bob, map[string]any{"type": "back"}), http.StatusOK)
	if p := playerByID(h.state(tb.code, bob.token), bob.playerID); p["standIn"] != nil {
		t.Fatalf("the while-away mark outlived Bob's next command: %v", p)
	}

	// Skip turn: only when the game waits on that person (or it's their turn).
	playPast(t, h, tb.code, []person{bob}, 30*time.Second, func(v *gameView) bool { return waitsOn(v, alice.playerID) })
	expectError(t, post("skip", tb.token, bob.playerID), http.StatusBadRequest, msgSkipNotWaiting)
	turns := h.view(tb.code).TurnCounts[alice.playerID]
	expectStatus(t, post("skip", tb.token, alice.playerID), http.StatusOK)
	// The computer plays out Alice's turn; Bob answers anything it asks of him (her
	// doubles can land on an auction that waits on his bid).
	playPast(t, h, tb.code, []person{bob}, 30*time.Second, func(v *gameView) bool {
		return turnOf(v) != alice.playerID && !slices.Contains(func() []string { ids, _ := actorsToMove(v); return ids }(), alice.playerID)
	})
	h.srv.mu.Lock()
	skipping := r.skipping(alice.playerID)
	h.srv.mu.Unlock()
	if skipping || autopilotOn(h, r, alice.playerID) || h.view(tb.code).TurnCounts[alice.playerID] < turns {
		t.Fatalf("after the skip: skipping %v, autopilot %v", skipping, autopilotOn(h, r, alice.playerID))
	}
	if p := playerByID(h.state(tb.code, tb.token), alice.playerID); p["skipped"] != nil || p["standIn"] != nil {
		t.Fatalf("Alice after the skip: %v", p)
	}
}

// The host removes a person mid-game: deeds, buildings and cash go to the
// bank, the seat closes, and a left event goes out.
func TestHostRemovesPlayerMidGame(t *testing.T) {
	h, tb, alice, bob, aSock, _ := uxTable(t)
	h.srv.mu.Lock()
	g := h.srv.rooms[tb.code].game.(*engineGame).g
	g.Properties[6] = &engine.PropertyState{OwnerID: bob.playerID, Houses: 1}
	g.Properties[8] = &engine.PropertyState{OwnerID: bob.playerID}
	g.Properties[9] = &engine.PropertyState{OwnerID: bob.playerID}
	g.Version++
	h.srv.commit(h.srv.rooms[tb.code])
	h.srv.mu.Unlock()

	expectError(t, h.post("/api/rooms/"+tb.code+"/remove", map[string]any{"token": bob.token, "playerId": alice.playerID}), http.StatusBadRequest, msgOnlyHost)
	expectError(t, h.personAct(tb.code, bob, map[string]any{"type": "remove_player", "playerId": bob.playerID}), http.StatusBadRequest, msgServerOnly)
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/remove", map[string]any{"token": tb.token, "playerId": bob.playerID}), http.StatusOK)

	st := h.state(tb.code, tb.token)
	p := playerByID(st, bob.playerID)
	if p == nil || p["left"] != true || p["bankrupt"] != true || p["cash"] != float64(0) {
		t.Fatalf("Bob after removal: %v", p)
	}
	for _, index := range []int{6, 8, 9} {
		if prop := h.view(tb.code).Properties[strconv.Itoa(index)]; prop.OwnerID != "" || prop.Houses != 0 {
			t.Fatalf("deed %d after removal: %+v", index, prop)
		}
	}
	if r := h.get("/api/rooms/" + tb.code + "/state?token=" + bob.token); r.status == http.StatusOK {
		t.Fatal("Bob's seat is still open")
	}
	aSock.until(5*time.Second, func(m map[string]any) bool {
		return m["type"] == "event" && dig(m, "event", "type") == "left" && dig(m, "event", "playerId") == bob.playerID
	})
	expectError(t, h.post("/api/rooms/"+tb.code+"/remove", map[string]any{"token": tb.token, "playerId": bob.playerID}), http.StatusBadRequest, "That player is already out of the game")
	// The game goes on without him.
	playPast(t, h, tb.code, []person{alice}, 30*time.Second, func(v *gameView) bool { return v.turnNumber() > 3 || v.Status != "playing" })
}

// rentGame is a two-player engine game past the turn order: Ann (current,
// at position from) owns nothing, Ben owns target; the Headlines deck has
// cardEffect's card on top.
func rentGame(t *testing.T, from, target int, cardEffect string) (*engine.Game, *engine.Player) {
	t.Helper()
	g := engine.CreateGame("RENT")
	ann, _ := engine.AddPlayer(g, "Ann", engine.KindHuman)
	ben, _ := engine.AddPlayer(g, "Ben", engine.KindHuman)
	g.HostID = ann.ID
	if err := engine.StartGame(g, ann.ID, nil); err != nil {
		t.Fatal(err)
	}
	g.Status, g.Phase, g.OpeningOrder, g.CurrentIndex = engine.StatusPlaying, engine.PhaseAwaitingRoll, nil, 0
	g.Turn = &engine.Turn{PlayerID: ann.ID}
	ann.Position = from
	g.Properties[target] = &engine.PropertyState{OwnerID: ben.ID}
	for _, card := range engine.ChanceCards {
		if card.Effect.Type == cardEffect {
			deck := []string{card.ID}
			for _, id := range g.Decks.Chance {
				if id != card.ID {
					deck = append(deck, id)
				}
			}
			g.Decks.Chance = deck
			break
		}
	}
	return g, ann
}

// dice is an RNG that rolls the given die faces in turn.
func dice(faces ...int) engine.RNG {
	i := 0
	return engine.RNGFunc(func() float64 {
		face := faces[i%len(faces)]
		i++
		return (float64(face) - 0.5) / 6
	})
}

func viewOf(t *testing.T, g *engine.Game) *gameView {
	t.Helper()
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	view, err := decodeGameView(data)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func testBoard() []boardSpace {
	board := make([]boardSpace, len(engine.Board))
	for i, space := range engine.Board {
		board[i] = boardSpace{Index: space.Index, Type: space.Type, Name: space.Name}
	}
	return board
}

// RENT ×2 / ×10: the pay event and lastLanding of rent set by the nearest
// railroad or utility card carry base and multiplier (and the utility's
// fresh dice); the amount is the engine's, unchanged.
func TestCardRentMultiplier(t *testing.T) {
	board := testBoard()
	for _, tc := range []struct {
		name               string
		from, target       int
		effect             string
		roll               []int // the move, then (utility) the fresh roll
		amount, base, mult int
		dice               []any
	}{
		{"nearest railroad", 4, 15, engine.EffectNearestRail, []int{1, 2}, 50, 25, 2, nil},
		{"nearest railroad past PAYDAY", 33, 5, engine.EffectNearestRail, []int{1, 2}, 50, 25, 2, nil},
		{"nearest utility", 4, 12, engine.EffectNearestUtility, []int{1, 2, 2, 5}, 70, 7, 10, []any{float64(2), float64(5)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, ann := rentGame(t, tc.from, tc.target, tc.effect)
			prev := viewOf(t, g)
			rng := dice(tc.roll...)
			if err := engine.PerformAction(g, ann.ID, engine.Action{Type: engine.ActionRoll}, rng); err != nil {
				t.Fatal(err)
			}
			if err := engine.PerformAction(g, ann.ID, engine.Action{Type: engine.ActionConfirmCard}, rng); err != nil {
				t.Fatal(err)
			}
			if g.LastLanding == nil || g.LastLanding.Kind != "rent" || g.LastLanding.Amount != tc.amount || g.LastLanding.SpaceIndex != tc.target {
				t.Fatalf("landing %+v", g.LastLanding)
			}
			next := viewOf(t, g)
			var pay map[string]any
			for _, e := range diffEvents(prev, next, board) {
				data, _ := json.Marshal(e)
				var m map[string]any
				_ = json.Unmarshal(data, &m)
				if m["type"] == "pay" && m["reason"] == "rent" {
					pay = m
				}
			}
			if pay == nil || pay["amount"] != float64(tc.amount) || pay["base"] != float64(tc.base) || pay["multiplier"] != float64(tc.mult) || !jsonEqual(pay["dice"], tc.dice) {
				t.Fatalf("pay event %v", pay)
			}
			raw, _ := json.Marshal(g.LastLanding)
			var landing map[string]any
			_ = json.Unmarshal(landingCardRent(raw, next, board), &landing)
			if landing["amount"] != float64(tc.amount) || landing["base"] != float64(tc.base) || landing["multiplier"] != float64(tc.mult) || !jsonEqual(landing["dice"], tc.dice) {
				t.Fatalf("lastLanding %v", landing)
			}
		})
	}

	// Ordinary rent carries neither.
	g, ann := rentGame(t, 12, 15, engine.EffectNearestRail)
	prev := viewOf(t, g)
	if err := engine.PerformAction(g, ann.ID, engine.Action{Type: engine.ActionRoll}, dice(1, 2)); err != nil {
		t.Fatal(err)
	}
	next := viewOf(t, g)
	for _, e := range diffEvents(prev, next, board) {
		if e.kind() == "pay" && (e.get("base") != nil || e.get("multiplier") != nil) {
			t.Fatalf("ordinary rent event %v", e)
		}
	}
	raw, _ := json.Marshal(g.LastLanding)
	if got := landingCardRent(raw, next, board); string(got) != string(raw) {
		t.Fatalf("ordinary lastLanding %s", got)
	}
}
