package bots

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/tdar200/estate-games/internal/poker/holdem"
)

func TestPreflopPercentileOrder(t *testing.T) {
	pct := func(s string) float64 {
		cs := holdem.MustCards(s)
		return PreflopPercentile(cs[0], cs[1])
	}
	if !(pct("AsAd") < pct("KsKd") && pct("KsKd") < pct("AsKs") && pct("AsKs") < pct("9c8c") && pct("9c8c") < pct("7d2c")) {
		t.Fatalf("AA %.3f KK %.3f AKs %.3f 98s %.3f 72o %.3f", pct("AsAd"), pct("KsKd"), pct("AsKs"), pct("9c8c"), pct("7d2c"))
	}
	if pct("7d2c") < 0.9 || pct("AsAd") > 0.01 {
		t.Fatalf("extremes: 72o %.3f AA %.3f", pct("7d2c"), pct("AsAd"))
	}
}

func TestEquityKnownMatchups(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 1))
	hole := func(s string) [2]holdem.Card {
		cs := holdem.MustCards(s)
		return [2]holdem.Card{cs[0], cs[1]}
	}
	// Aces against one random hand win about 85%.
	if e := Equity(rng, hole("AsAd"), nil, []Range{AnyTwo}, 20000); math.Abs(e-0.852) > 0.015 {
		t.Fatalf("AA vs random %.3f", e)
	}
	// A made flush on the river against a random hand.
	board := holdem.MustCards("Kh7h2h9c3d")
	if e := Equity(rng, hole("AhQh"), board, []Range{AnyTwo}, 4000); e < 0.97 {
		t.Fatalf("nut flush %.3f", e)
	}
	// A tight range beats 7-2 more often than a random hand does.
	loose := Equity(rng, hole("7d2c"), nil, []Range{AnyTwo}, 8000)
	tight := Equity(rng, hole("7d2c"), nil, []Range{{Top: 0.1}}, 8000)
	if tight >= loose {
		t.Fatalf("72o vs tight %.3f should be below vs random %.3f", tight, loose)
	}
}

// randomView plays a random table forward and returns the actor's view.
func playWithBots(t *testing.T, seed uint64, bots map[string]Bot, hands int) *holdem.Table {
	t.Helper()
	var s [32]byte
	s[0] = byte(seed)
	now := int64(1_700_000_000_000)
	tm := holdem.Timing{RebuyMs: 5000}
	tb := holdem.NewTable("BOTS", holdem.Options{Seed: &s, Clock: func() int64 { return now }, Timing: &tm})
	var ids []string
	for k := 0; k < len(bots); k++ {
		id, _ := tb.AddPlayer(fmt.Sprintf("P%d", k), holdem.KindHuman)
		ids = append(ids, id)
	}
	_ = tb.PerformAction(ids[0], json.RawMessage(`{"type":"settings","settings":{"rebuys":2,"targetMinutes":15}}`))
	if err := tb.PerformAction(ids[0], json.RawMessage(`{"type":"start"}`)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for step := 0; step < 100000 && tb.Status == holdem.StatusPlaying && tb.HandNumber <= hands; step++ {
		w := tb.Waiting()
		if len(w) == 0 {
			if next := tb.NextDeadline(); next > 0 {
				now = max(now, next)
			}
			tb.Advance(now)
			continue
		}
		id := w[0].PlayerID
		v := tb.View(id, holdem.Viewer{Role: "player"})
		bot := bots[fmt.Sprintf("P%d", indexOf(ids, id))]
		c, err := bot.Decide(ctx, v)
		if err != nil {
			t.Fatalf("decide: %v", err)
		}
		if err := Validate(v, c); err != nil {
			t.Fatalf("bot returned illegal %+v: %v (legal %+v)", c, err, v.You.Legal)
		}
		if err := tb.Do(id, c); err != nil {
			t.Fatalf("table rejected %+v: %v", c, err)
		}
	}
	return tb
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

func TestBotsPlayLegalGames(t *testing.T) {
	for seed := uint64(1); seed <= 4; seed++ {
		bots := map[string]Bot{}
		for k := 0; k < 6; k++ {
			d := Normal
			if k%2 == 1 {
				d = Easy
			}
			b, _ := New(d, seed*10+uint64(k))
			bots[fmt.Sprintf("P%d", k)] = b
		}
		tb := playWithBots(t, seed, bots, 150)
		if tb.HandNumber < 20 {
			t.Fatalf("only %d hands played", tb.HandNumber)
		}
	}
}

func TestBotsAreReproducible(t *testing.T) {
	run := func() string {
		bots := map[string]Bot{}
		for k := 0; k < 3; k++ {
			b, _ := New(Easy, uint64(k))
			bots[fmt.Sprintf("P%d", k)] = b
		}
		tb := playWithBots(t, 9, bots, 30)
		snap, _ := json.Marshal(tb.Players)
		return string(snap)
	}
	if run() != run() {
		t.Fatal("seeded bots should replay identically")
	}
}

func TestFallbackOnIllegalAnswer(t *testing.T) {
	bots := map[string]Bot{}
	for k := 0; k < 3; k++ {
		normal, _ := New(Normal, uint64(k))
		bots[fmt.Sprintf("P%d", k)] = WithFallback(badBot{}, normal)
	}
	playWithBots(t, 3, bots, 20)
}

type badBot struct{}

func (badBot) Decide(context.Context, *holdem.View) (holdem.Command, error) {
	return holdem.Command{Type: "raise", To: -5}, nil
}

// TestHelperBotProcess is the external bot used by TestProcessBot: it
// checks or calls every time.
func TestHelperBotProcess(t *testing.T) {
	if os.Getenv("POKER_HELPER_BOT") != "1" {
		t.Skip("helper process")
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var req Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			fmt.Printf(`{"error":"bad request"}` + "\n")
			continue
		}
		a := holdem.Command{Type: "call"}
		if req.View.You.CanRebuy && !req.View.You.ToAct {
			a = holdem.Command{Type: "rebuy"}
		} else if req.View.You.Legal.Check {
			a = holdem.Command{Type: "check"}
		}
		out, _ := json.Marshal(Reply{ID: req.ID, Action: a})
		fmt.Println(string(out))
	}
	os.Exit(0)
}

func TestProcessBot(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	os.Setenv("POKER_HELPER_BOT", "1")
	defer os.Unsetenv("POKER_HELPER_BOT")
	p, err := StartProcess(exe, "-test.run=TestHelperBotProcess")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	normal, _ := New(Normal, 1)
	bots := map[string]Bot{"P0": p, "P1": normal, "P2": normal}
	tb := playWithBots(t, 5, bots, 10)
	if tb.HandNumber < 5 {
		t.Fatalf("hands %d", tb.HandNumber)
	}
}

func TestHTTPBot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad", 400)
			return
		}
		a := holdem.Command{Type: "fold"}
		if req.View.You.CanRebuy && !req.View.You.ToAct {
			a = holdem.Command{Type: "declineRebuy"}
		} else if req.View.You.Legal.Check {
			a = holdem.Command{Type: "check"}
		}
		json.NewEncoder(w).Encode(Reply{ID: req.ID, Action: a})
	}))
	defer srv.Close()
	normal, _ := New(Normal, 2)
	bots := map[string]Bot{"P0": &HTTPBot{URL: srv.URL}, "P1": normal}
	playWithBots(t, 6, bots, 10)
}

// BenchmarkDecide measures one decision on a busy flop (3 opponents).
func BenchmarkDecide(b *testing.B) {
	for _, d := range []string{Easy, Normal} {
		b.Run(d, func(b *testing.B) {
			var s [32]byte
			now := int64(1_700_000_000_000)
			tm := holdem.Timing{}
			tb := holdem.NewTable("BENCH", holdem.Options{Seed: &s, Clock: func() int64 { return now }, Timing: &tm})
			var ids []string
			for k := 0; k < 4; k++ {
				id, _ := tb.AddPlayer(fmt.Sprintf("P%d", k), holdem.KindHuman)
				ids = append(ids, id)
			}
			tb.PerformAction(ids[0], json.RawMessage(`{"type":"start"}`))
			for tb.Hand.Street == holdem.Preflop {
				w := tb.Waiting()[0].PlayerID
				tb.PerformAction(w, json.RawMessage(`{"type":"call"}`)) // checks when nothing to call
			}
			id := tb.Waiting()[0].PlayerID
			v := tb.View(id, holdem.Viewer{Role: "player"})
			bot, _ := New(d, 1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				bot.Decide(context.Background(), v)
			}
		})
	}
}
