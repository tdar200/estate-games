// Command arena plays bots against each other and reports each seat's win
// rate in big blinds per 100 hands (bb/100) with a 95% confidence interval.
//
// Every hand starts with all stacks reset to the same depth (default 100
// big blinds), cash-game style, so results are comparable hand to hand.
// The button moves every hand, so each seat plays every position.
//
//	go run ./cmd/arena -hands 20000 -seats normal,easy,normal,easy,normal,easy
//	go run ./cmd/arena -seats normal,cmd:./mybot,normal        # external bot over stdin/stdout
//	go run ./cmd/arena -seats normal,http://127.0.0.1:7420/decide
//
// Seat specs: "easy", "normal", "cmd:<program> [args]" or an http(s) URL.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tdar200/estate-games/internal/poker/bots"
	"github.com/tdar200/estate-games/internal/poker/holdem"
)

type seatStats struct {
	Label string  `json:"label"`
	Hands int     `json:"hands"`
	Sum   float64 `json:"-"` // big blinds won
	SumSq float64 `json:"-"`
	BB100 float64 `json:"bb100"`
	CI95  float64 `json:"ci95"`
}

func (s *seatStats) add(bb float64) {
	s.Hands++
	s.Sum += bb
	s.SumSq += bb * bb
}

func (s *seatStats) merge(o *seatStats) {
	s.Hands += o.Hands
	s.Sum += o.Sum
	s.SumSq += o.SumSq
}

func (s *seatStats) finish() {
	if s.Hands == 0 {
		return
	}
	n := float64(s.Hands)
	mean := s.Sum / n
	variance := 0.0
	if s.Hands > 1 {
		variance = (s.SumSq - n*mean*mean) / (n - 1)
	}
	s.BB100 = mean * 100
	s.CI95 = 1.96 * math.Sqrt(math.Max(variance, 0)/n) * 100
}

func main() {
	hands := flag.Int("hands", 10000, "hands to play in total")
	seatsFlag := flag.String("seats", "normal,easy,normal,easy,normal,easy", "comma-separated seat specs (2–10 seats)")
	depth := flag.Int("stack", 100, "stack depth in big blinds at the start of every hand")
	bb := flag.Int("bb", 20, "big blind in chips")
	ante := flag.Int("ante", 0, "ante in chips")
	seed := flag.Uint64("seed", 1, "random seed (decks and bots)")
	workers := flag.Int("workers", max(1, runtime.NumCPU()/2), "tables played in parallel (external bots force 1)")
	asJSON := flag.Bool("json", false, "print JSON instead of a table")
	flag.Parse()

	specs := strings.Split(*seatsFlag, ",")
	if len(specs) < 2 || len(specs) > holdem.MaxSeats {
		fail("need 2–10 seats, got %d", len(specs))
	}
	external := false
	for _, s := range specs {
		if strings.HasPrefix(s, "cmd:") || strings.HasPrefix(s, "http") {
			external = true
		}
	}
	if external {
		*workers = 1
	}
	*workers = max(1, min(*workers, *hands))

	start := time.Now()
	results := make([][]*seatStats, *workers)
	var wg sync.WaitGroup
	errs := make(chan error, *workers)
	for w := 0; w < *workers; w++ {
		n := *hands / *workers
		if w < *hands%*workers {
			n++
		}
		wg.Add(1)
		go func(w, n int) {
			defer wg.Done()
			stats, err := runTable(specs, n, *depth, *bb, *ante, *seed+uint64(w)*1_000_003)
			if err != nil {
				errs <- err
				return
			}
			results[w] = stats
		}(w, n)
	}
	wg.Wait()
	close(errs)
	if err := <-errs; err != nil {
		fail("%v", err)
	}

	seats := make([]*seatStats, len(specs))
	for i, s := range specs {
		seats[i] = &seatStats{Label: s}
	}
	byBot := map[string]*seatStats{}
	for _, r := range results {
		for i, s := range r {
			seats[i].merge(s)
			if byBot[s.Label] == nil {
				byBot[s.Label] = &seatStats{Label: s.Label}
			}
			byBot[s.Label].merge(s)
		}
	}
	for _, s := range seats {
		s.finish()
	}
	var labels []string
	for l, s := range byBot {
		s.finish()
		labels = append(labels, l)
	}
	sort.Strings(labels)

	if *asJSON {
		out := map[string]any{"hands": *hands, "stackBB": *depth, "seed": *seed, "seats": seats}
		var agg []*seatStats
		for _, l := range labels {
			agg = append(agg, byBot[l])
		}
		out["bots"] = agg
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
		return
	}
	fmt.Printf("%d hands, %d seats, %d bb deep, %s\n\n", *hands, len(specs), *depth, time.Since(start).Round(time.Millisecond))
	fmt.Printf("%-5s %-28s %8s %10s %10s\n", "seat", "bot", "hands", "bb/100", "95% CI")
	for i, s := range seats {
		fmt.Printf("%-5d %-28s %8d %+10.1f %10s\n", i, s.Label, s.Hands, s.BB100, fmt.Sprintf("±%.1f", s.CI95))
	}
	if len(labels) < len(specs) {
		fmt.Printf("\n%-34s %8s %10s %10s\n", "by bot (seat-hands)", "hands", "bb/100", "95% CI")
		for _, l := range labels {
			s := byBot[l]
			fmt.Printf("%-34s %8d %+10.1f %10s\n", l, s.Hands, s.BB100, fmt.Sprintf("±%.1f", s.CI95))
		}
	}
	fmt.Println("\nA seat beats the table with ~95% confidence when bb/100 − CI > 0.")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "arena: "+format+"\n", args...)
	os.Exit(2)
}

func makeBot(spec string, seed uint64) (holdem.Command, bots.Bot, func(), error) {
	switch {
	case strings.HasPrefix(spec, "cmd:"):
		parts := strings.Fields(strings.TrimPrefix(spec, "cmd:"))
		if len(parts) == 0 {
			return holdem.Command{}, nil, nil, fmt.Errorf("empty command in %q", spec)
		}
		p, err := bots.StartProcess(parts[0], parts[1:]...)
		if err != nil {
			return holdem.Command{}, nil, nil, err
		}
		fallback, _ := bots.New(bots.Normal, seed)
		return holdem.Command{}, bots.WithFallback(p, fallback), func() { p.Close() }, nil
	case strings.HasPrefix(spec, "http://"), strings.HasPrefix(spec, "https://"):
		fallback, _ := bots.New(bots.Normal, seed)
		return holdem.Command{}, bots.WithFallback(&bots.HTTPBot{URL: spec}, fallback), func() {}, nil
	default:
		b, err := bots.New(spec, seed)
		return holdem.Command{}, b, func() {}, err
	}
}

// runTable plays n hands at one table and returns per-seat results.
func runTable(specs []string, n, depth, bb, ante int, seed uint64) ([]*seatStats, error) {
	var s [32]byte
	for i := 0; i < 8; i++ {
		s[i] = byte(seed >> (8 * i))
	}
	now := int64(1_700_000_000_000)
	// 1 ms pauses so the loop sees every finished hand before the next deal;
	// a long rebuy window so busted seats wait for ResetStacks.
	tm := holdem.Timing{ShowdownMs: 1, HandOverMs: 1, RebuyMs: 1 << 30}
	tb := holdem.NewTable("ARENA", holdem.Options{Seed: &s, Clock: func() int64 { return now }, Timing: &tm, SeatLimit: holdem.MaxSeats})
	ids := make([]string, len(specs))
	seatBots := map[string]bots.Bot{}
	stats := make([]*seatStats, len(specs))
	stack := depth * bb
	settings, _ := json.Marshal(map[string]any{
		"maxPlayers": len(specs), "startingStack": max(stack, 10*bb), "smallBlind": bb / 2, "bigBlind": bb,
		"ante": ante, "blindSchedule": "off", "rebuys": 10, // busted seats wait for ResetStacks instead of leaving
	})
	for i, spec := range specs {
		id, err := tb.AddPlayer(fmt.Sprintf("Seat %d", i), holdem.KindAI)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			// The first seat hosts; size the table before the others sit down.
			tb.SetHost(id)
			if err := tb.Do(id, holdem.Command{Type: "settings", Settings: settings}); err != nil {
				return nil, err
			}
		}
		_, b, closer, err := makeBot(spec, seed*31+uint64(i))
		if err != nil {
			return nil, err
		}
		defer closer()
		ids[i] = id
		seatBots[id] = b
		stats[i] = &seatStats{Label: spec}
	}
	if err := tb.ResetStacks(stack); err != nil {
		return nil, err
	}
	if err := tb.Do(ids[0], holdem.Command{Type: "start"}); err != nil {
		return nil, err
	}
	ctx := context.Background()
	played, recorded := 0, 0
	for guard := 0; played < n; guard++ {
		if guard > n*400 {
			return nil, fmt.Errorf("arena stalled at hand %d (phase %s)", tb.HandNumber, tb.Phase)
		}
		if tb.Phase == holdem.PhaseShowdown || tb.Phase == holdem.PhaseHandOver || tb.Phase == holdem.PhaseFinished {
			// Record the finished hand once, then reset stacks for the next one.
			if tb.HandNumber != recorded {
				recorded = tb.HandNumber
				total := 0
				for i, id := range ids {
					p := tb.Player(id)
					total += p.Stack - stack
					stats[i].add(float64(p.Stack-stack) / float64(bb))
				}
				if total != 0 {
					if os.Getenv("ARENA_DEBUG") != "" {
						for _, p := range tb.Players {
							fmt.Fprintln(os.Stderr, *p)
						}
						fmt.Fprintln(os.Stderr, "hand", tb.Hand.Stacks, tb.Hand.StartStacks, tb.HandIDs)
						for _, l := range tb.Log[max(0, len(tb.Log)-30):] {
							fmt.Fprintln(os.Stderr, l.Text)
						}
					}
					return nil, fmt.Errorf("hand %d: chips not conserved (%+d)", tb.HandNumber, total)
				}
				played++
			}
			if played >= n {
				break
			}
			if tb.Phase == holdem.PhaseFinished {
				return nil, fmt.Errorf("game finished unexpectedly")
			}
			if err := tb.ResetStacks(stack); err != nil {
				return nil, err
			}
			now = max(now+1, tb.NextDeadline())
			tb.Advance(now)
			continue
		}
		w := tb.Waiting()
		if len(w) == 0 {
			now = max(now+1, tb.NextDeadline())
			tb.Advance(now)
			continue
		}
		id := w[0].PlayerID
		v := tb.View(id, holdem.Viewer{Role: "player"})
		c, err := seatBots[id].Decide(ctx, v)
		if err != nil || bots.Validate(v, c) != nil {
			c = bots.Safe(v)
		}
		if err := tb.Do(id, c); err != nil {
			return nil, fmt.Errorf("hand %d: %s %+v: %v", tb.HandNumber, id, c, err)
		}
	}
	return stats, nil
}
