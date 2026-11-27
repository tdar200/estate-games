package hardai

import (
	"bytes"
	"encoding/json"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
)

// allRules turns on every house rule, to exercise the loan, jackpot and
// no-auction paths.
func allRules() *engine.RulesUpdate {
	on, off, cash := true, false, 2000
	return &engine.RulesUpdate{Loans: &on, FreeParkingJackpot: &on, Auctions: &off, StartingCash: &cash}
}

// loansOnly turns on loans (auctions stay on).
func loansOnly() *engine.RulesUpdate {
	on := true
	return &engine.RulesUpdate{Loans: &on}
}

// newRulesGame is NewGame with every house rule on.
func newRulesGame(t testing.TB, names []string, rng engine.RNG) *engine.Game {
	t.Helper()
	g, err := NewGameWithRules(names, allRules(), rng)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// forEachState plays heuristic games and calls visit before every action
// (and once at the end).
func forEachState(t *testing.T, games int, rules bool, visit func(g *engine.Game)) {
	t.Helper()
	for i := 0; i < games; i++ {
		rng := newSplitMix(uint64(1000 + i))
		names := []string{"A", "B", "C", "D"}[:2+i%3]
		var g *engine.Game
		if rules {
			g = newRulesGame(t, names, rng)
		} else {
			var err error
			if g, err = NewGame(names, rng); err != nil {
				t.Fatal(err)
			}
		}
		d := &Driver{RNG: rng, MaxTurns: 400}
		h := NewHeuristicBot(false)
		for _, p := range g.Players {
			d.Seats = append(d.Seats, Seat{PlayerID: p.ID, Bot: h})
		}
		for step := 0; step < 20000 && g.Status == engine.StatusPlaying; step++ {
			visit(g)
			if err := d.Step(g); err != nil {
				t.Fatalf("game %d step %d: %v", i, step, err)
			}
		}
		visit(g)
	}
}

func roundTrip(t *testing.T, g *engine.Game) *engine.Game {
	t.Helper()
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var out engine.Game
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return &out
}

func withoutFuncs(g *engine.Game) *engine.Game {
	c := *g
	c.Clock, c.NewID = nil, nil
	return &c
}

func TestCloneMatchesJSON(t *testing.T) {
	for _, rules := range []bool{false, true} {
		checked := 0
		step := 0
		forEachState(t, 6, rules, func(g *engine.Game) {
			step++
			if step%7 != 0 && g.Status == engine.StatusPlaying {
				return // a sample of states keeps the test fast
			}
			checked++
			want, _ := json.Marshal(g)
			c := Clone(g)
			got, _ := json.Marshal(c)
			if !bytes.Equal(got, want) {
				t.Fatalf("clone JSON differs at step %d:\n got %s\nwant %s", step, got, want)
			}
			if !reflect.DeepEqual(withoutFuncs(c), withoutFuncs(roundTrip(t, g))) {
				t.Fatalf("clone differs from the JSON round trip at step %d (phase %s)", step, g.Phase)
			}
			// The clone shares nothing: playing it on must not touch g.
			d := &Driver{RNG: newSplitMix(uint64(step)), MaxTurns: 3}
			c.NewID = newSimIDs("x")
			d.Play(c)
			if after, _ := json.Marshal(g); !bytes.Equal(after, want) {
				t.Fatalf("playing the clone changed the original at step %d", step)
			}
		})
		if checked < 50 {
			t.Fatalf("only %d states checked", checked)
		}
	}
}

func TestViewMatchesJSON(t *testing.T) {
	for _, rules := range []bool{false, true} {
		vb := &viewBuilder{}
		checked := 0
		forEachState(t, 6, rules, func(g *engine.Game) {
			got := vb.build(g)
			want := jsonView(g)
			if !reflect.DeepEqual(got, want) {
				gj, _ := json.Marshal(got)
				wj, _ := json.Marshal(want)
				t.Fatalf("view differs (phase %s):\n got %s\nwant %s", g.Phase, gj, wj)
			}
			checked++
		})
		if checked < 100 {
			t.Fatalf("only %d states checked", checked)
		}
	}
}

// TestOnlyMoveMatchesHeuristic checks the rollout shortcuts against the AI.
func TestOnlyMoveMatchesHeuristic(t *testing.T) {
	h := NewHeuristicBot(false)
	shortcuts := 0
	forEachState(t, 8, false, func(g *engine.Game) {
		id, orderRoll := NextActor(g)
		if id == "" || orderRoll {
			return
		}
		for _, tradesHandled := range []bool{false, true} {
			a, ok := onlyMove(g, id, tradesHandled)
			if !ok {
				continue
			}
			shortcuts++
			view := h.vb.build(g)
			if tradesHandled && view.Turn != nil {
				view.Turn.AITradeAttempted = true
			}
			want, wantOK := toEngineAction(ai.ChooseAction(view, id))
			if !wantOK || want.String() != a.String() {
				t.Fatalf("phase %s: shortcut %s, heuristic %s", g.Phase, a, want)
			}
		}
	})
	if shortcuts < 200 {
		t.Fatalf("only %d shortcuts exercised", shortcuts)
	}
}

func TestNextActorMatchesServerRule(t *testing.T) {
	forEachState(t, 3, false, func(g *engine.Game) {
		id, orderRoll := NextActor(g)
		switch {
		case g.Status != engine.StatusPlaying:
			if id != "" {
				t.Fatalf("finished game names actor %q", id)
			}
		case g.Phase == engine.PhaseDeterminingOrder:
			if !orderRoll || id == "" {
				t.Fatal("expected an order roll")
			}
		case g.Phase == engine.PhaseAuction:
			// A timed auction (new games' default) has no turn, so
			// AuctionCurrentPlayerID is ""; the server asks the first who may
			// bid from TurnPos (engine.AuctionBidderIDs).
			want := ""
			if ids := engine.AuctionBidderIDs(g); len(ids) > 0 {
				want = ids[0]
			}
			if !g.Auction.Timed() {
				want = engine.AuctionCurrentPlayerID(Clone(g))
			}
			if id != want {
				t.Fatalf("auction actor %q, engine says %q (timed %v)", id, want, g.Auction.Timed())
			}
		case g.Phase == engine.PhaseTrade:
			if id != g.Trade.TargetID {
				t.Fatal("trade actor is not the target")
			}
		}
	})
}

// TestDriverHeuristicGames: 100 all-heuristic games run without engine
// errors or refused actions.
func TestDriverHeuristicGames(t *testing.T) {
	games := 100
	if testing.Short() {
		games = 20
	}
	type outcome struct {
		res MatchResult
		err error
	}
	results := make([]outcome, games)
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i := 0; i < games; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			levels := []Level{Normal, Normal, Normal, Normal}[:2+i%3]
			res, err := PlayMatch(MatchConfig{Levels: levels, Seed: int64(i), MaxTurns: 2000})
			results[i] = outcome{res, err}
		}(i)
	}
	wg.Wait()
	finished := 0
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("game %d: %v", i, r.err)
		}
		for _, s := range r.res.Seats {
			if s.Rejected != 0 {
				t.Fatalf("game %d: %d heuristic actions refused", i, s.Rejected)
			}
		}
		if r.res.Finished {
			finished++
		}
	}
	t.Logf("%d/%d games finished by bankruptcy (the rest hit the 2000-turn cap)", finished, games)
	if finished < games*9/10 {
		t.Fatalf("only %d/%d games finished", finished, games)
	}
}

// soakOptions keep Hard decisions cheap and deterministic in tests.
var soakOptions = Options{Budget: -1, Rollouts: 6, MinRounds: 2, Horizon: 6, Workers: 2}

// TestHardNeverIllegal soaks Hard (and Easy) seats, with default and with
// house rules: the engine must never refuse one of their actions.
func TestHardNeverIllegal(t *testing.T) {
	games := 9
	if testing.Short() {
		games = 3
	}
	var mu sync.Mutex
	decisions, mc := 0, 0
	opts := soakOptions
	opts.OnDecision = func(d DecisionInfo) {
		mu.Lock()
		decisions++
		if d.MonteCarlo {
			mc++
		}
		mu.Unlock()
	}
	var wg sync.WaitGroup
	errs := make([]error, games)
	rejected := make([]int, games)
	for i := 0; i < games; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			levels := []Level{Hard, Normal, Easy, Hard}[:3+i%2]
			// Classic rules, every house rule (auctions off), loans only.
			rules := []*engine.RulesUpdate{nil, allRules(), loansOnly()}[i%3]
			cfg := MatchConfig{Levels: levels, Seed: int64(500 + i), Options: opts, MaxTurns: 400, Rules: rules}
			res, err := PlayMatch(cfg)
			errs[i] = err
			for _, s := range res.Seats {
				if s.Level != Normal {
					rejected[i] += s.Rejected
				}
			}
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("game %d: %v", i, errs[i])
		}
		if rejected[i] != 0 {
			t.Fatalf("game %d: %d Hard/Easy actions refused by the engine", i, rejected[i])
		}
	}
	t.Logf("%d Hard/Easy decisions, %d by Monte Carlo, none refused", decisions, mc)
	if mc < 20 {
		t.Fatalf("only %d Monte Carlo decisions exercised", mc)
	}
}

// TestHardDeterministic: with no time budget, a seed fixes the whole game,
// whatever the number of rollout goroutines.
func TestHardDeterministic(t *testing.T) {
	play := func(workers int) []byte {
		opts := soakOptions
		opts.Workers = workers
		res, err := PlayMatch(MatchConfig{Levels: []Level{Hard, Normal, Easy}, Seed: 42, Options: opts, MaxTurns: 150})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(res.Game)
		return data
	}
	first := play(1)
	if again := play(1); !bytes.Equal(first, again) {
		t.Fatal("same seed, different games")
	}
	if parallel := play(4); !bytes.Equal(first, parallel) {
		t.Fatal("the number of workers changed the game")
	}
}

// TestHardChoiceIsLegal checks single decisions from many states: whatever
// Hard returns, the engine accepts.
func TestHardChoiceIsLegal(t *testing.T) {
	hb := NewHardBot(7, Options{Budget: -1, Rollouts: 3, MinRounds: 2, Horizon: 3, Workers: 1})
	checked := 0
	step := 0
	forEachState(t, 3, false, func(g *engine.Game) {
		step++
		id, orderRoll := NextActor(g)
		if id == "" || orderRoll || step%5 != 0 {
			return
		}
		a, ok := hb.Choose(g, id)
		if !ok {
			t.Fatalf("phase %s: Hard had no action", g.Phase)
		}
		c := Clone(g)
		if err := engine.PerformAction(c, id, a, newSplitMix(1)); err != nil {
			t.Fatalf("phase %s: Hard chose %s: %v", g.Phase, a, err)
		}
		hb.plan = nil // the next state is unrelated
		checked++
	})
	if checked < 50 {
		t.Fatalf("only %d decisions checked", checked)
	}
}

func TestEasyDeterministicAndFallible(t *testing.T) {
	var mu sync.Mutex
	mistakes := 0
	opts := Options{OnDecision: func(d DecisionInfo) {
		if d.Chosen != "" {
			mu.Lock()
			mistakes++
			mu.Unlock()
		}
	}}
	play := func() []byte {
		res, err := PlayMatch(MatchConfig{Levels: []Level{Easy, Normal, Easy}, Seed: 9, Options: opts, MaxTurns: 300})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(res.Game)
		return data
	}
	if !bytes.Equal(play(), play()) {
		t.Fatal("Easy is not deterministic")
	}
	if mistakes == 0 {
		t.Fatal("Easy made no mistakes")
	}
}

func TestParseLevel(t *testing.T) {
	for _, l := range []Level{Easy, Normal, Hard, Expert} {
		got, err := ParseLevel(l.String())
		if err != nil || got != l {
			t.Fatalf("ParseLevel(%q) = %v, %v", l.String(), got, err)
		}
	}
	if _, err := ParseLevel("brutal"); err == nil {
		t.Fatal("expected an error")
	}
}

// TestFastViewCoversAIView fails when internal/ai.Game gains a JSON key the
// direct view builder does not fill: views then silently fall back to the
// (correct but ~10x slower) JSON round trip. Add the key to fastKeys and to
// viewBuilder.build; TestViewMatchesJSON checks the result.
func TestFastViewCoversAIView(t *testing.T) {
	if !fastViewOK {
		t.Fatal("internal/ai view types have JSON keys that view.go does not know; see fastKeys")
	}
}

// TestViewMatchesJSONWithLoans builds a mid-game state with an active loan
// secured by collateral, a pending loan offer, a City Park pot and a loan
// debt marker, and checks the direct AI view against the JSON round trip.
func TestViewMatchesJSONWithLoans(t *testing.T) {
	rng := newSplitMix(77)
	g := newRulesGame(t, []string{"A", "B", "C"}, rng)
	d := &Driver{RNG: rng}
	h := NewHeuristicBot(false)
	for _, p := range g.Players {
		d.Seats = append(d.Seats, Seat{PlayerID: p.ID, Bot: h})
	}
	vb := &viewBuilder{}
	check := func(label string) {
		t.Helper()
		got, want := vb.build(g), jsonView(g)
		if !reflect.DeepEqual(got, want) {
			gj, _ := json.Marshal(got)
			wj, _ := json.Marshal(want)
			t.Fatalf("%s: view differs:\n got %s\nwant %s", label, gj, wj)
		}
	}
	// Play until the current player owns a building-free property and is
	// about to roll.
	collateral := -1
	for step := 0; step < 5000 && collateral < 0; step++ {
		if err := d.Step(g); err != nil {
			t.Fatal(err)
		}
		cur := engine.CurrentPlayer(g)
		if g.Phase != engine.PhaseAwaitingRoll || cur == nil || g.LoanOffer != nil {
			continue
		}
		for _, index := range engine.OwnableIndexes {
			grp := spaceGroup[index]
			if ownerOf(g, index) == cur.ID && (grp < 0 || !groupHasBuildings(g, grp)) && !pledged(g, index) {
				collateral = index
				break
			}
		}
	}
	if collateral < 0 {
		t.Fatal("no state with collateral found")
	}
	borrower := engine.CurrentPlayer(g)
	var lender *engine.Player
	for _, p := range g.Players {
		if p.ID != borrower.ID && !p.Bankrupt && p.Cash >= 100 {
			lender = p
			break
		}
	}
	if lender == nil {
		t.Fatal("no lender")
	}
	offer := engine.Action{Type: engine.ActionOfferLoan, Direction: engine.LoanBorrow, TargetID: lender.ID,
		Principal: 100, Repay: 130, DueTurns: 6, Collateral: []int{collateral}}
	if err := engine.PerformAction(g, borrower.ID, offer, rng); err != nil {
		t.Fatal(err)
	}
	check("pending borrow offer")
	if err := engine.PerformAction(g, lender.ID, engine.Action{Type: engine.ActionAcceptLoan}, rng); err != nil {
		t.Fatal(err)
	}
	check("active loan")
	// A second offer (lend) on top of the active loan.
	var other *engine.Player
	for _, p := range g.Players {
		if p.ID != borrower.ID && p.ID != lender.ID && !p.Bankrupt {
			other = p
		}
	}
	if other != nil && borrower.Cash >= 50 {
		lend := engine.Action{Type: engine.ActionOfferLoan, Direction: engine.LoanLend, TargetID: other.ID,
			Principal: 50, Repay: 60, DueTurns: 3}
		if err := engine.PerformAction(g, borrower.ID, lend, rng); err != nil {
			t.Fatal(err)
		}
		check("active loan plus pending lend offer")
	}
	g.FreeParking = 175
	if g.Debt == nil {
		g.Debt = &engine.Debt{PayerID: borrower.ID, Amount: 130, CreditorID: lender.ID, Reason: "loan", LoanID: g.Loans[0].ID}
	}
	check("jackpot and loan debt")
	if len(g.Loans) == 0 || g.LoanOffer == nil && other != nil {
		t.Fatal("expected an active loan and a pending offer")
	}
}
