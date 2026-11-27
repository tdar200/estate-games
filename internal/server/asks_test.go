package server

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/hardai"
)

// cheapBots are the real levels with small searches, so a test game plays
// quickly.
func cheapBots(level string, seed int64) Bot {
	l, err := hardai.ParseLevel(level)
	if err != nil || l == hardai.Normal {
		return RealAI{}
	}
	return EngineBotAdapter(hardai.NewBot(l, seed, hardai.Options{Budget: -1, Workers: 2, Rollouts: 12}))
}

func cheapHarness(t *testing.T) *harness {
	return newHarness(t, func(c *Config) {
		c.Engine = RealEngine{}
		c.AI = RealAI{}
		c.Bots = cheapBots
		c.AIDelay = time.Millisecond
	})
}

// seenAsk is one computer offer to the person, as the person saw it.
type seenAsk struct {
	bot, key string
	value    int
	turn     int // the person's turns so far
	deeds    []int
}

type pesterStats struct {
	asks, repeats, sameOrWorse, notAbove, deedTooSoon, maxPerSet int
	byKey                                                        map[string]int
}

// pester plays a table game of a person who declines every offer against
// four computers of level, for the person's first turns turns, and returns
// every computer offer the person got. prepare runs once the game has started.
func pester(t *testing.T, levels []string, mode string, turns int, prepare func(h *harness, code, ann string)) (pesterStats, []seenAsk) {
	h := cheapHarness(t)
	tb := h.createTable()
	pad := h.addSeat(tb, "keyboard", "Ann", "key", nil)
	expectStatus(t, pad, http.StatusCreated)
	ann := person{playerID: dig(pad.body, "state", "localSeats", "0", "playerId").(string), token: tb.token, seatID: pad.str("seatId"), ip: "198.51.100.40"}
	for _, level := range levels {
		expectStatus(t, h.post("/api/rooms/"+tb.code+"/ai", map[string]any{"token": tb.token, "difficulty": level}), http.StatusOK)
	}
	h.srv.mu.Lock()
	h.srv.rooms[tb.code].botOffers = mode
	h.srv.mu.Unlock()
	expectStatus(t, h.act(tb.code, tb.token, map[string]any{"type": "start"}), http.StatusOK)
	if prepare != nil {
		prepare(h, tb.code, ann.playerID) // after the start, which makes every deed for sale again
	}
	var asks []seenAsk
	seen := map[string]bool{}
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		view := h.view(tb.code)
		if view.Status != "playing" || view.TurnCounts[ann.playerID] >= turns {
			break
		}
		if tr := view.Trade; view.Phase == "trade" && tr != nil && tr.TargetID == ann.playerID && tr.CounterTo == "" && !seen[tr.ID] {
			if p := view.player(tr.ProposerID); p != nil && p.Kind == "ai" {
				seen[tr.ID] = true
				v := tr.Offer.Cash - tr.Request.Cash
				for _, i := range tr.Offer.Properties {
					price := engine.Board[i].Price
					if view.Properties[strconv.Itoa(i)].Mortgaged {
						price /= 2
					}
					v += price
				}
				asks = append(asks, seenAsk{bot: tr.ProposerID, key: askKey(tr.Offer.Properties, tr.Request.Properties), value: v,
					turn: view.TurnCounts[ann.playerID], deeds: tr.Request.Properties})
			}
		}
		if !h.personMove(tb.code, ann, view) {
			time.Sleep(2 * time.Millisecond)
		}
	}
	// Every offer was declined: count asks for a deed set already declined
	// at least as well (same or worse), re-asks not clearly above the best
	// declined, and asks for a deed within a round of the last.
	st := pesterStats{byKey: map[string]int{}}
	floor := map[string]int{}
	lastDeed := map[int]int{}
	for _, a := range asks {
		st.asks++
		st.byKey[a.key]++
		st.maxPerSet = max(st.maxPerSet, st.byKey[a.key])
		if f, ok := floor[a.key]; ok {
			st.repeats++
			if a.value <= f {
				st.sameOrWorse++
			}
			if !beats(a.value, f) {
				st.notAbove++
			}
		}
		for _, d := range a.deeds {
			if at, ok := lastDeed[d]; ok && a.turn < at+deedRounds {
				st.deedTooSoon++
			}
			lastDeed[d] = a.turn
		}
		if f, ok := floor[a.key]; !ok || a.value > f {
			floor[a.key] = a.value
		}
	}
	return st, asks
}

// A person who declines everything is never offered the same deal again in
// the same or a worse form, a re-offer always clears the best one declined
// (+10%, +$20), and no deed is asked for twice in one round; before 0.10.1
// the same deals came back. (The numbers are in the test log: go test -run
// TestPeopleAreNotPestered -v.)
func TestPeopleAreNotPestered(t *testing.T) {
	if testing.Short() {
		t.Skip("plays two games")
	}
	const turns, games = 60, 6
	var before, after pesterStats
	var asks []seenAsk
	for game := 0; game < games; game++ {
		levels := []string{"expert", "expert", "normal", "easy"}
		b, _ := pester(t, levels, botOffersUnlimited, turns, nil)
		a, seen := pester(t, levels, botOffersNormal, turns, nil)
		before, after = before.add(b), after.add(a)
		asks = append(asks, seen...)
	}
	t.Logf("before (no rules): %d offers, %d for a deed set asked before, %d of them same or worse, %d asks for a deed within a round, at most %d for one set",
		before.asks, before.repeats, before.sameOrWorse, before.deedTooSoon, before.maxPerSet)
	t.Logf("after (0.10.1):    %d offers, %d for a deed set asked before, %d of them same or worse, %d asks for a deed within a round, at most %d for one set",
		after.asks, after.repeats, after.sameOrWorse, after.deedTooSoon, after.maxPerSet)
	if after.sameOrWorse != 0 || after.notAbove != 0 || after.deedTooSoon != 0 {
		t.Fatalf("after: %+v\n%v", after, asks)
	}
	if after.maxPerSet > 1+3*maxReasks {
		t.Fatalf("one deed set asked %d times", after.maxPerSet)
	}
}

// "Not selling" deeds are never asked for, and "off" stops every computer
// offer to people.
func TestNotSellingAndOff(t *testing.T) {
	if testing.Short() {
		t.Skip("plays two games")
	}
	notSelling, asks := pester(t, fourExperts, botOffersNormal, 25, func(h *harness, code, ann string) {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		m := h.srv.rooms[code].meta[ann]
		m.notSelling = map[int]bool{}
		for _, i := range engine.OwnableIndexes {
			m.notSelling[i] = true
		}
	})
	for _, a := range asks {
		if len(a.deeds) > 0 {
			t.Fatalf("asked for not-selling deeds %v", a.deeds)
		}
	}
	off, _ := pester(t, fourExperts, botOffersOff, 25, nil)
	if off.asks != 0 {
		t.Fatalf("%d computer offers with offers off", off.asks)
	}
	t.Logf("not selling: %d offers (sales only); off: %d", notSelling.asks, off.asks)
}

// The not-selling endpoint and the botOffers setting.
func TestNotSellingAPI(t *testing.T) {
	h := cheapHarness(t)
	tb := h.createTable()
	pad := h.addSeat(tb, "keyboard", "Ann", "key", nil)
	ann := dig(pad.body, "state", "localSeats", "0", "playerId").(string)
	r := h.post("/api/rooms/"+tb.code+"/not-selling", map[string]any{"token": tb.token, "seatId": pad.str("seatId"), "spaceIndex": 39, "on": true})
	expectStatus(t, r, http.StatusOK)
	for _, p := range players(r.body) {
		if p["id"] == ann && fmt.Sprint(p["notSelling"]) != "[39]" {
			t.Fatalf("notSelling %v", p["notSelling"])
		}
	}
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/not-selling", map[string]any{"token": tb.token, "seatId": pad.str("seatId"), "spaceIndex": 0}), http.StatusBadRequest)
	r = h.post("/api/rooms/"+tb.code+"/not-selling", map[string]any{"token": tb.token, "seatId": pad.str("seatId"), "spaceIndex": 39, "on": false})
	expectStatus(t, r, http.StatusOK)
	h.srv.mu.Lock()
	left := len(h.srv.rooms[tb.code].meta[ann].notSelling)
	h.srv.mu.Unlock()
	if left != 0 {
		t.Fatal("not-selling was not turned off")
	}
	r = h.post("/api/rooms/"+tb.code+"/settings", map[string]any{"token": tb.token, "botOffers": "fewer"})
	expectStatus(t, r, http.StatusOK)
	if got := dig(r.body, "roomSettings", "botOffers"); got != "fewer" {
		t.Fatalf("roomSettings.botOffers = %v", got)
	}
	expectStatus(t, h.post("/api/rooms/"+tb.code+"/settings", map[string]any{"token": tb.token, "botOffers": botOffersUnlimited}), http.StatusBadRequest)
}

// The ask rules, one at a time.
func TestAskRules(t *testing.T) {
	g := engine.CreateGame("RULES")
	ann, _ := engine.AddPlayer(g, "Ann", engine.KindHuman)
	bot, _ := engine.AddPlayer(g, "Bot", engine.KindAI)
	other, _ := engine.AddPlayer(g, "Other", engine.KindAI)
	g.TurnCounts = map[string]int{ann.ID: 5}
	ask := func(from string, cash int, deeds ...int) engine.Action {
		return engine.Action{Type: engine.ActionOfferTrade, TargetID: ann.ID,
			Offer: &engine.TradeBundle{Cash: cash}, Request: &engine.TradeBundle{Properties: deeds}}
	}
	key := ann.ID + "|" + askKey(nil, []int{21})
	k := askRules{mode: botOffersNormal, notSelling: map[string]map[int]bool{},
		floors: map[string]int{key: 451}, reasks: map[string]int{}, deeds: map[string]int{}, perTurn: map[string]int{}}
	for _, c := range []struct {
		cash int
		ok   bool
	}{{349, false}, {451, false}, {480, false}, {496, false}, {497, true}, {530, true}} {
		if got := k.allowed(g, bot.ID, ask(bot.ID, c.cash, 21)); got != c.ok {
			t.Fatalf("$%d after $451 was declined: allowed %v", c.cash, got)
		}
		if got := k.allowed(g, other.ID, ask(other.ID, c.cash, 21)); got != c.ok {
			t.Fatalf("another computer, $%d after $451 was declined: allowed %v", c.cash, got)
		}
	}
	k.reasks[bot.ID+"|"+key] = maxReasks
	if k.allowed(g, bot.ID, ask(bot.ID, 900, 21)) {
		t.Fatal("a third improved re-offer was allowed")
	}
	k.deeds[fmt.Sprint(ann.ID, "|", 23)] = 5
	if k.allowed(g, other.ID, ask(other.ID, 300, 23)) {
		t.Fatal("asked for a deed twice in one round")
	}
	g.TurnCounts[ann.ID] = 6
	if !k.allowed(g, other.ID, ask(other.ID, 300, 23)) {
		t.Fatal("a round later the deed can be asked for")
	}
	k.notSelling[ann.ID] = map[int]bool{24: true}
	if k.allowed(g, bot.ID, ask(bot.ID, 300, 24)) {
		t.Fatal("asked for a not-selling deed")
	}
	k.mode = botOffersFewer
	k.perTurn[fmt.Sprint(ann.ID, "|", 6)] = 1
	if k.allowed(g, bot.ID, ask(bot.ID, 300, 25)) {
		t.Fatal("fewer: a second offer between two of the person's turns")
	}
	k.mode = botOffersOff
	if k.allowed(g, bot.ID, ask(bot.ID, 300, 26)) {
		t.Fatal("off: an offer")
	}
	toBot := engine.Action{Type: engine.ActionOfferTrade, TargetID: other.ID, Offer: &engine.TradeBundle{Cash: 1}, Request: &engine.TradeBundle{Properties: []int{24}}}
	if !k.allowed(g, bot.ID, toBot) {
		t.Fatal("offers between computers are not affected")
	}
}

var fourExperts = []string{"expert", "expert", "expert", "expert"}

func (s pesterStats) add(o pesterStats) pesterStats {
	s.asks += o.asks
	s.repeats += o.repeats
	s.sameOrWorse += o.sameOrWorse
	s.notAbove += o.notAbove
	s.deedTooSoon += o.deedTooSoon
	s.maxPerSet = max(s.maxPerSet, o.maxPerSet)
	return s
}
