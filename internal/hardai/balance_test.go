package hardai

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/valuenet"
)

// Balance and anti-exploit tests: Easy's bounded mistakes, sticky trade
// answers, the trade memory (no spam, no farming) and 7-8 player games.

// tradeSpot is a mid-game state at a player's roll, with a target holding a
// tradeable deed.
type tradeSpot struct {
	g        *engine.Game
	proposer string
	target   string
	deed     int // the target's dearest tradeable deed
}

// tradeSpots plays heuristic games and collects up to n spots where the
// current player could ask another player for a deed. The proposer is made a
// human (people are the ones who resubmit offers).
func tradeSpots(t *testing.T, n int, accept func(g *engine.Game, proposer, target string, deed int) bool) []tradeSpot {
	t.Helper()
	var spots []tradeSpot
	for game := 0; game < 60 && len(spots) < n; game++ {
		rng := newSplitMix(uint64(3000 + game))
		g, err := NewGame([]string{"A", "B", "C", "D"}, rng)
		if err != nil {
			t.Fatal(err)
		}
		d := heuristicDriver(g, rng, 400)
		for step := 0; step < 20000 && g.Status == engine.StatusPlaying && len(spots) < n; step++ {
			if err := d.Step(g); err != nil {
				t.Fatal(err)
			}
			if step < 150 || step%37 != 0 || g.Phase != engine.PhaseAwaitingRoll || g.Turn == nil {
				continue
			}
			cur := engine.CurrentPlayer(g)
			if cur == nil || cur.InJail || cur.Cash < 50 {
				continue
			}
			for _, p := range g.Players {
				if p.ID == cur.ID || p.Bankrupt {
					continue
				}
				deed, price := -1, 0
				for _, index := range engine.OwnableIndexes {
					grp := spaceGroup[index]
					if ownerOf(g, index) != p.ID || pledged(g, index) || (grp >= 0 && groupHasBuildings(g, grp)) {
						continue
					}
					if engine.Board[index].Price > price {
						deed, price = index, engine.Board[index].Price
					}
				}
				if deed >= 0 && accept(g, cur.ID, p.ID, deed) {
					c := Clone(g)
					c.PlayerByID(cur.ID).Kind = engine.KindHuman
					spots = append(spots, tradeSpot{g: c, proposer: cur.ID, target: p.ID, deed: deed})
					break
				}
			}
		}
	}
	if len(spots) < n {
		t.Fatalf("only %d trade spots found", len(spots))
	}
	return spots
}

// offerRepeatedly makes the proposer offer cash for the deed tries times in
// a row (a new turn every other try, as a patient human would) and returns
// how many times the bot accepted.
func offerRepeatedly(t *testing.T, s tradeSpot, bot Bot, cash, tries int) int {
	t.Helper()
	g := Clone(s.g)
	accepted := 0
	for try := 0; try < tries; try++ {
		if try > 0 && try%2 == 0 {
			g.TurnCounts[s.proposer]++ // a later turn of the proposer
		}
		offer := tradeAction(s.target, cash, nil, 0, []int{s.deed})
		if err := engine.PerformAction(g, s.proposer, offer, newSplitMix(1)); err != nil {
			t.Fatalf("offer: %v", err)
		}
		a, ok := bot.Choose(g, s.target)
		if !ok || (a.Type != engine.ActionAcceptTrade && a.Type != engine.ActionRejectTrade) {
			t.Fatalf("answer to a trade: %v %v", a, ok)
		}
		if a.Type == engine.ActionAcceptTrade {
			accepted++
			return accepted // the deed has moved
		}
		if err := engine.PerformAction(g, s.target, a, newSplitMix(1)); err != nil {
			t.Fatal(err)
		}
	}
	return accepted
}

// B2: a $1 bid for Easy's dearest deed used to be accepted in about 3 tries.
// Now Easy refuses it however often it is repeated, in every position.
func TestEasyRefusesLowballs(t *testing.T) {
	spots := tradeSpots(t, 20, func(*engine.Game, string, string, int) bool { return true })
	for i, s := range spots {
		easy := NewEasyBot(int64(100+i), Options{})
		if n := offerRepeatedly(t, s, easy, 1, 30); n > 0 {
			t.Fatalf("spot %d: Easy sold %s for $1", i, engine.Board[s.deed].Name)
		}
	}
}

// fairCash is the least cash the heuristic owner accepts for the deed.
func fairCash(g *engine.Game, proposer, target string, deed int) (int, bool) {
	vb := &viewBuilder{}
	view := vb.build(g)
	markAI(view, target)
	q, err := ai.QuoteTrade(view, proposer, ai.NewQuoteRequest(target, nil, []int{deed}))
	if err != nil || q.OfferCash <= 0 {
		return 0, false
	}
	return q.OfferCash, true
}

// Easy still makes human mistakes: offered a little under its price for a
// deed that completes nobody's group, it sometimes accepts (Normal never
// does). The answer to one offer is fixed for the turn: re-offering the
// same deeds with any cash does not re-roll it.
func TestEasyBoundedTradeMistakes(t *testing.T) {
	spots := tradeSpots(t, 40, func(g *engine.Game, proposer, target string, deed int) bool {
		fair, ok := fairCash(g, proposer, target, deed)
		return ok && !completesFor(g, proposer, deed) && fair-engine.Board[deed].Price/10 <= g.PlayerByID(proposer).Cash
	})
	easyAccepts, normalAccepts := 0, 0
	for i, s := range spots {
		fair, _ := fairCash(s.g, s.proposer, s.target, s.deed)
		cash := fair - engine.Board[s.deed].Price/10 // 10% of the price short
		easyAccepts += offerRepeatedly(t, s, NewEasyBot(int64(200+i), Options{}), cash, 6)
		normalAccepts += offerRepeatedly(t, s, NewHeuristicBot(false), cash, 6)

		// Same deeds, same turn, any cash: one answer.
		g := Clone(s.g)
		easy := NewEasyBot(int64(200+i), Options{})
		var answers []string
		for _, c := range []int{cash, cash - 1, cash - 5} {
			offer := tradeAction(s.target, c, nil, 0, []int{s.deed})
			if err := engine.PerformAction(g, s.proposer, offer, newSplitMix(1)); err != nil {
				t.Fatal(err)
			}
			a, _ := easy.Choose(g, s.target)
			answers = append(answers, a.Type)
			if a.Type == engine.ActionAcceptTrade {
				break
			}
			if err := engine.PerformAction(g, s.target, a, newSplitMix(1)); err != nil {
				t.Fatal(err)
			}
		}
		if answers[0] == engine.ActionRejectTrade && len(answers) > 1 && answers[len(answers)-1] == engine.ActionAcceptTrade {
			t.Fatalf("spot %d: a cheaper re-offer in the same turn was accepted after a refusal: %v", i, answers)
		}
	}
	if normalAccepts != 0 {
		t.Fatalf("Normal accepted %d under-priced offers", normalAccepts)
	}
	if easyAccepts == 0 {
		t.Fatal("Easy never made a bounded trade mistake")
	}
	t.Logf("Easy accepted %d of %d slightly under-priced deals", easyAccepts, len(spots))
}

// Easy never gives away the deed that completes the proposer's colour group
// by mistake, even a little under its price.
func TestEasyKeepsSetCompletingDeeds(t *testing.T) {
	spots := tradeSpots(t, 20, func(g *engine.Game, proposer, target string, deed int) bool {
		return spaceGroup[deed] >= 0
	})
	for i, s := range spots {
		g := s.g
		grp := spaceGroup[s.deed]
		for _, index := range groupMembers[grp] {
			if index != s.deed {
				g.Properties[index].OwnerID = s.proposer
				g.Properties[index].Mortgaged = false
			}
		}
		g.PlayerByID(s.proposer).Cash = 3000
		fair, ok := fairCash(g, s.proposer, s.target, s.deed)
		if !ok {
			t.Fatalf("spot %d: no price for %s", i, engine.Board[s.deed].Name)
		}
		cash := fair - max(1, engine.Board[s.deed].Price/20)
		if n := offerRepeatedly(t, s, NewEasyBot(int64(300+i), Options{}), cash, 30); n > 0 {
			t.Fatalf("spot %d: Easy gave away %s, completing the proposer's group, under its price", i, engine.Board[s.deed].Name)
		}
	}
}

// B17: Hard (and Expert) refuse a refused offer again, without a new search
// that could accept it by luck.
// (A computer proposer: people get its price and counter-offers instead;
// TestHumanOffersAnsweredByPrice.)
func TestHardRefusalIsSticky(t *testing.T) {
	spots := tradeSpots(t, 5, func(*engine.Game, string, string, int) bool { return true })
	for i, s := range spots {
		s.g = Clone(s.g)
		s.g.PlayerByID(s.proposer).Kind = engine.KindAI
		var last DecisionInfo
		opts := soakOptions
		opts.OnDecision = func(d DecisionInfo) { last = d }
		hard := NewHardBot(int64(i), opts)
		g := Clone(s.g)
		for try := 0; try < 4; try++ {
			offer := tradeAction(s.target, 1, nil, 0, []int{s.deed})
			if err := engine.PerformAction(g, s.proposer, offer, newSplitMix(1)); err != nil {
				t.Fatal(err)
			}
			a, _ := hard.Choose(g, s.target)
			if a.Type != engine.ActionRejectTrade {
				t.Fatalf("spot %d try %d: Hard accepted $1 for %s", i, try, engine.Board[s.deed].Name)
			}
			if try > 0 && (last.MonteCarlo || last.Chosen != "refused before") {
				t.Fatalf("spot %d try %d: a repeated offer was searched again (%+v)", i, try, last)
			}
			if err := engine.PerformAction(g, s.target, a, newSplitMix(1)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestTradeMemory(t *testing.T) {
	g, err := NewGame([]string{"A", "B", "C"}, newSplitMix(1))
	if err != nil {
		t.Fatal(err)
	}
	me, a, b := g.Players[0].ID, g.Players[1].ID, g.Players[2].ID
	g.Properties[1].OwnerID, g.Properties[3].OwnerID, g.Properties[5].OwnerID = a, a, me
	m := newTradeMemory(2)
	m.observe(g, me)
	buy := tradeAction(a, 100, nil, 0, []int{1})
	sale := tradeAction(a, 0, []int{5}, 200, nil)
	if !m.allowed(g, me, buy) || !m.allowed(g, me, sale) {
		t.Fatal("fresh memory holds offers back")
	}
	// A refused purchase: that deed rests for dealCooldown turns; the
	// next turn nothing is offered at all.
	m.record(g, me, buy)
	m.observe(g, me) // trade over, deed did not move: refused
	g.TurnCounts[me] += 2
	if m.allowed(g, me, tradeAction(a, 150, []int{5}, 0, []int{1})) {
		t.Fatal("a similar deal (same deed asked for) was allowed right after a refusal")
	}
	if !m.allowed(g, me, tradeAction(a, 50, nil, 0, []int{3})) || !m.allowed(g, me, tradeAction(b, 50, nil, 0, []int{1})) {
		t.Fatal("unrelated deals were held back")
	}
	g.TurnCounts[me] -= 1
	if !m.paused(g, me) {
		t.Fatal("no pause on the turn after a refusal")
	}
	g.TurnCounts[me] += dealCooldown
	if !m.allowed(g, me, buy) {
		t.Fatal("the cooldown never ends")
	}
	// A refused sale rests too (the engine's cooldown ignores sales).
	m.record(g, me, sale)
	m.observe(g, me)
	g.TurnCounts[me] += 2
	if m.allowed(g, me, tradeAction(a, 0, []int{5}, 150, nil)) {
		t.Fatal("a refused sale was offered again at once")
	}
	// Two refusals within targetWindow: the target rests altogether.
	m.record(g, me, tradeAction(a, 50, nil, 0, []int{3}))
	m.observe(g, me)
	g.TurnCounts[me] += 2
	if m.allowed(g, me, tradeAction(a, 50, nil, 0, []int{39})) {
		t.Fatal("two refusals in a row did not rest the target")
	}
	if !m.allowed(g, me, tradeAction(b, 50, nil, 0, []int{39})) {
		t.Fatal("another target rests too")
	}
	g.TurnCounts[me] += targetBackoff
	if !m.allowed(g, me, tradeAction(a, 50, nil, 0, []int{39})) {
		t.Fatal("the target rests for ever")
	}
	// The same deed refused again by the same target rests twice as long
	// each time (8, 16, 32, 64 turns).
	m4 := newTradeMemory(0)
	m4.observe(g, me)
	ask := tradeAction(b, 10, nil, 0, []int{1})
	for _, rest := range []int{dealCooldown, 2 * dealCooldown, 4 * dealCooldown, dealCooldownMax, dealCooldownMax} {
		start := g.TurnCounts[me]
		m4.record(g, me, ask)
		m4.observe(g, me)
		g.TurnCounts[me] = start + rest - 1
		if m4.allowed(g, me, ask) {
			t.Fatalf("a deal refused again rested less than %d turns", rest)
		}
		g.TurnCounts[me] = start + rest
		if !m4.allowed(g, me, ask) {
			t.Fatalf("a deal refused again rested more than %d turns", rest)
		}
	}
	// Accepted deals count against the partner's cap.
	m2 := newTradeMemory(2)
	m2.observe(g, me)
	for i, index := range []int{1, 3} {
		m2.record(g, me, tradeAction(b, 10, nil, 0, []int{index}))
		g.Properties[index].OwnerID = me
		m2.observe(g, me)
		if i == 0 && !m2.allowed(g, me, tradeAction(b, 10, nil, 0, []int{5})) {
			t.Fatal("one accepted deal used up the cap of 2")
		}
	}
	if m2.allowed(g, me, tradeAction(b, 10, []int{5}, 0, nil)) {
		t.Fatal("a third deal with the same partner was allowed")
	}
	if !m2.allowed(g, me, tradeAction(a, 10, []int{5}, 0, nil)) {
		t.Fatal("the cap applies per partner")
	}
}

// dealKey identifies an offer's proposer, target and deeds.
func dealKey(proposer string, a engine.Action) string {
	key := proposer + ">" + a.TargetID
	for _, b := range []*engine.TradeBundle{a.Offer, a.Request} {
		props := append([]int(nil), bundleProps(b)...)
		sort.Ints(props)
		key += fmt.Sprint(props)
	}
	return key
}

// bundleProps are the deeds of a trade bundle (nil = none).
func bundleProps(b *engine.TradeBundle) []int {
	if b == nil {
		return nil
	}
	return b.Properties
}

// dealKeys are the deeds a refusal rests: those asked for, or for a sale
// those offered.
func dealKeys(a engine.Action) []int {
	if keys := bundleProps(a.Request); len(keys) > 0 {
		return keys
	}
	return bundleProps(a.Offer)
}

// B6: bots with a memory never offer a refused deal (or one asking for the
// same deed) to the same bot again within the cooldown, sales included, and
// close at most MaxDeals deals with one partner.
func TestBotsDoNotSpamTrades(t *testing.T) {
	games := 6
	if testing.Short() {
		games = 3
	}
	var wg sync.WaitGroup
	errs := make([]error, games)
	for i := 0; i < games; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			levels := [][]Level{{Expert, Hard, Easy, Normal}, {Hard, Hard, Easy}, {Expert, Easy, Easy}}[i%3]
			type refusal struct {
				keys []int
				turn int
			}
			refused := map[string]refusal{} // proposer>target -> last refusal
			deals := map[string]int{}
			var pending struct {
				proposer string
				a        engine.Action
			}
			levelOf := map[string]Level{}
			var failure error
			onAction := func(g *engine.Game, id string, a engine.Action) {
				if len(levelOf) == 0 {
					for _, p := range g.Players {
						name, _, _ := cutDash(p.Name)
						levelOf[p.ID], _ = ParseLevel(name)
					}
				}
				switch a.Type {
				case engine.ActionOfferTrade:
					pending.proposer, pending.a = id, a
					if levelOf[id] == Normal || failure != nil {
						return
					}
					pair := id + ">" + a.TargetID
					if levelOf[id] != Easy && deals[pair] >= defaultMaxDeals {
						failure = fmt.Errorf("%s offered a deal to a partner after %d accepted deals", id, deals[pair])
					}
					keys := dealKeys(a)
					if r, ok := refused[pair]; ok && g.TurnCounts[id] < r.turn+dealCooldown {
						for _, k := range keys {
							for _, rk := range r.keys {
								if k == rk {
									failure = fmt.Errorf("%s re-offered %s to %s %d turns after a refusal (%s)",
										id, engine.Board[k].Name, a.TargetID, g.TurnCounts[id]-r.turn, dealKey(id, a))
								}
							}
						}
					}
				case engine.ActionAcceptTrade:
					deals[pending.proposer+">"+pending.a.TargetID]++
				case engine.ActionRejectTrade:
					refused[pending.proposer+">"+pending.a.TargetID] = refusal{keys: dealKeys(pending.a), turn: g.TurnCounts[pending.proposer]}
				}
			}
			_, err := PlayMatch(MatchConfig{Levels: levels, Seed: int64(700 + i), Options: soakOptions, MaxTurns: 300, OnAction: onAction})
			if err == nil {
				err = failure
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("game %d: %v", i, err)
		}
	}
}

func cutDash(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// ------------------------------------------------------------ 7-8 players

// newGameN is NewGame for any number of seats. While engine.MaxPlayers is
// below len(names) (it was 6 before 8-player rooms) the extra seats are added
// directly, which only this test helper does.
func newGameN(t testing.TB, names []string, rng engine.RNG) *engine.Game {
	t.Helper()
	if len(names) <= engine.MaxPlayers {
		g, err := NewGame(names, rng)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	g := engine.CreateGame("ARENA")
	g.Clock = fixedClock
	g.NewID = newSimIDs("")
	for i, name := range names {
		if i < engine.MaxPlayers {
			if _, err := engine.AddPlayer(g, name, engine.KindAI); err != nil {
				t.Fatal(err)
			}
			continue
		}
		p := &engine.Player{ID: g.NewID("ai"), Name: name, Kind: engine.KindAI, Color: "#777777", Cash: 1500}
		g.Players = append(g.Players, p)
		g.TurnCounts[p.ID] = 0
	}
	g.HostID = g.Players[0].ID
	if err := engine.StartGame(g, g.HostID, rng); err != nil {
		t.Fatal(err)
	}
	return g
}

// Every level plays 7- and 8-player games without errors or refused moves.
func TestEightPlayerGames(t *testing.T) {
	for _, n := range []int{7, 8} {
		for seed := 0; seed < 2; seed++ {
			names := make([]string, n)
			levels := []Level{Expert, Hard, Normal, Easy, Expert, Hard, Normal, Easy}
			for i := range names {
				names[i] = levels[i].String() + "-" + strconv.Itoa(i+1)
			}
			rng := newSplitMix(uint64(40 + 10*n + seed))
			g := newGameN(t, names, rng)
			d := &Driver{RNG: rng, TrimLog: true, MaxTurns: 160 + 80*seed}
			sawFallback, sawNet := false, false
			for i, p := range g.Players {
				opts := soakOptions
				var bot Bot = NewBot(levels[i], int64(i), opts)
				if hb, ok := bot.(*HardBot); ok && levels[i] == Expert {
					bot = probeBot{hb, func(g *engine.Game) {
						if hb.searchFor(g).useNet {
							sawNet = true
						} else {
							sawFallback = true
						}
					}}
				}
				d.Seats = append(d.Seats, Seat{PlayerID: p.ID, Bot: bot})
			}
			res, err := d.Play(g)
			if err != nil {
				t.Fatalf("%d players: %v", n, err)
			}
			for id, k := range d.Rejected {
				if k > 0 {
					t.Fatalf("%d players: %d moves of %s refused by the engine", n, k, id)
				}
			}
			if !sawFallback {
				t.Fatalf("%d players: Expert never fell back to Hard's search", n)
			}
			t.Logf("%d players: %d turns, finished %v, Expert used the network at some point: %v", n, res.Turns, res.Finished, sawNet)
		}
	}
}

// probeBot calls see before every decision of a Hard bot.
type probeBot struct {
	*HardBot
	see func(g *engine.Game)
}

func (p probeBot) Choose(g *engine.Game, id string) (engine.Action, bool) {
	p.see(g)
	return p.HardBot.Choose(g, id)
}

// Expert uses the network exactly while it can read the game: at most 6
// players, or at most 6 still in.
func TestExpertNetworkSeats(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	g := newGameN(t, names, newSplitMix(9))
	heuristicDriver(g, newSplitMix(9), 30).Play(g)
	x := NewExpertBot(1, soakOptions)
	if x.searchFor(g).useNet || valuenet.Fits(g) {
		t.Fatal("8 players in: the network cannot read the game")
	}
	if s := x.searchFor(g); s.horizon != hardHorizon || s.confidence != hardConfidence {
		t.Fatalf("fallback search %+v is not Hard's", s)
	}
	if len(x.netTradeCandidates(g, g.Players[0].ID)) != 0 {
		t.Fatal("network trade ideas in a game the network cannot read")
	}
	// Two players out: the six left fill the network's slots.
	for _, i := range []int{2, 5} {
		p := g.Players[i]
		p.Bankrupt = true
		for _, index := range engine.OwnableIndexes {
			if ownerOf(g, index) == p.ID {
				g.Properties[index].OwnerID = ""
				g.Properties[index].Houses = 0
			}
		}
	}
	if !x.searchFor(g).useNet {
		t.Fatal("6 players still in: the network should be used")
	}
	ev := valuenet.NewEvaluator(x.opts.Net)
	sum := 0.0
	for _, p := range g.Players {
		v := ev.WinProb(g, p.ID)
		if p.Bankrupt && v != 0 {
			t.Fatalf("bankrupt player %s has win probability %v", p.ID, v)
		}
		sum += v
	}
	if sum < 0.999 || sum > 1.001 {
		t.Fatalf("win probabilities sum to %v", sum)
	}
	pos := valuenet.FromGame(g)
	if pos.NPlayers != 6 {
		t.Fatalf("position has %d players, want 6", pos.NPlayers)
	}
	for k, index := range engine.OwnableIndexes {
		if owner := ownerOf(g, index); owner != "" {
			if s := pos.Props[k].Owner; s < 0 || g.Players[seatIndex(t, g, int(s))].ID != owner {
				t.Fatalf("%s: owner slot %d does not map to %s", engine.Board[index].Name, s, owner)
			}
		}
	}
}

func seatIndex(t *testing.T, g *engine.Game, slot int) int {
	seats, n, ok := valuenet.Seats(g)
	if !ok || slot >= n {
		t.Fatalf("slot %d of %d (fits %v)", slot, n, ok)
	}
	return seats[slot]
}

// PlayMatch takes up to engine.MaxPlayers seats (8 once 8-player rooms land).
func TestMatchUpToMaxPlayers(t *testing.T) {
	if engine.MaxPlayers < 8 {
		t.Skip("engine.MaxPlayers is still", engine.MaxPlayers)
	}
	levels := []Level{Expert, Hard, Normal, Easy, Normal, Easy, Hard, Normal}
	res, err := PlayMatch(MatchConfig{Levels: levels, Seed: 81, Options: soakOptions, MaxTurns: 120})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Seats) != 8 {
		t.Fatalf("%d seats", len(res.Seats))
	}
	for _, s := range res.Seats {
		if s.Rejected > 0 {
			t.Fatalf("%s: %d moves refused", s.PlayerID, s.Rejected)
		}
	}
	if _, err := PlayMatch(MatchConfig{Levels: append(levels, Normal), Seed: 1}); err == nil {
		t.Fatal("9 seats accepted")
	}
}
