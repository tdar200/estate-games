// Command ludo-arena plays Ludo bots against each other on the real rules
// engine (internal/ludo) and prints how often each one wins, with a 95%
// confidence interval, and its average finishing place.
//
// Seats rotate every game, so each bot plays from every arm equally often
// (who rolls first is random anyway).
//
//	go run ./cmd/ludo-arena -games 4000 -seats hard,normal
//	go run ./cmd/ludo-arena -games 2000 -seats hard,normal,easy,normal
//	go run ./cmd/ludo-arena -games 2000 -seats normal,easy -rules kids,noSafe
//	go run ./cmd/ludo-arena -tune -rounds 40 -games 600    # search Hard's weights against Normal
//
// Seat specs: easy, normal, hard. Rules: kids (any roll brings a piece out),
// noHomeBonus, noSafe, noThreeSixes, captureFirst, blocks, firstWins;
// -pieces 2|3|4 and -board auto|4|6 as in the lobby.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/tdar200/estate-games/internal/ludo"
)

type tally struct {
	Label  string  `json:"label"`
	Seats  int     `json:"seats"` // seat-games played
	Wins   int     `json:"wins"`
	Places int     `json:"-"`
	Rate   float64 `json:"winRate"`
	CI95   float64 `json:"ci95"`
	Place  float64 `json:"avgPlace"`
}

func main() {
	games := flag.Int("games", 2000, "games to play")
	seatsFlag := flag.String("seats", "hard,normal", "comma-separated bots, one per seat (2–6)")
	pieces := flag.Int("pieces", 4, "pieces per player (2, 3 or 4)")
	board := flag.String("board", ludo.BoardAuto, "board: auto, 4 or 6")
	rulesFlag := flag.String("rules", "", "house rules: kids,noHomeBonus,noSafe,noThreeSixes,captureFirst,blocks,firstWins")
	seed := flag.Uint64("seed", 1, "random seed (dice and bots)")
	workers := flag.Int("workers", max(1, runtime.NumCPU()/2), "games played in parallel")
	asJSON := flag.Bool("json", false, "print JSON")
	tune := flag.Bool("tune", false, "search Hard's weights against Normal (2 players) instead")
	rounds := flag.Int("rounds", 30, "tuning rounds (with -tune)")
	flag.Parse()

	settings := ludo.DefaultSettings()
	settings.Pieces, settings.Board = *pieces, *board
	for _, rule := range strings.Split(*rulesFlag, ",") {
		switch strings.TrimSpace(rule) {
		case "":
		case "kids":
			settings.AnyRollOut = true
		case "noHomeBonus":
			settings.HomeBonus = false
		case "noSafe":
			settings.SafeSquares = false
		case "noThreeSixes":
			settings.ThreeSixes = false
		case "captureFirst":
			settings.CaptureFirst = true
		case "blocks":
			settings.Blocks = true
		case "firstWins":
			settings.PlayOn = false
		default:
			fail("unknown rule %q", rule)
		}
	}
	if err := settings.Validate(); err != nil {
		fail("%v", err)
	}
	if *tune {
		runTune(settings, *games, *rounds, *seed, *workers)
		return
	}
	specs := strings.Split(*seatsFlag, ",")
	if len(specs) < ludo.MinPlayers || len(specs) > ludo.MaxPlayers {
		fail("need 2–6 seats, got %d", len(specs))
	}
	makers := make([]func(uint64) ludo.Bot, len(specs))
	for i, spec := range specs {
		spec := strings.TrimSpace(spec)
		if _, err := ludo.NewBot(spec, 0); err != nil {
			fail("%v", err)
		}
		makers[i] = func(s uint64) ludo.Bot { b, _ := ludo.NewBot(spec, s); return b }
		specs[i] = spec
	}
	results := play(settings, makers, *games, *seed, *workers)
	byLabel := map[string]*tally{}
	var order []string
	for i, spec := range specs {
		t := byLabel[spec]
		if t == nil {
			t = &tally{Label: spec}
			byLabel[spec] = t
			order = append(order, spec)
		}
		t.Seats += results[i].Seats
		t.Wins += results[i].Wins
		t.Places += results[i].Places
	}
	out := make([]*tally, 0, len(order))
	for _, label := range order {
		t := byLabel[label]
		p := float64(t.Wins) / float64(t.Seats)
		t.Rate = p
		t.CI95 = 1.96 * math.Sqrt(p*(1-p)/float64(t.Seats))
		t.Place = float64(t.Places) / float64(t.Seats)
		out = append(out, t)
	}
	if *asJSON {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"games": *games, "seats": specs, "settings": settings, "results": out})
		return
	}
	fmt.Printf("%d games, seats %s, %d pieces, board %s, rules %q\n", *games, strings.Join(specs, ","), settings.Pieces, settings.Board, *rulesFlag)
	fmt.Printf("fair share of wins per seat: %.1f%%\n", 100/float64(len(specs)))
	for _, t := range out {
		fmt.Printf("  %-7s seats %6d  wins %5.1f%% ± %.1f  avg place %.2f\n", t.Label, t.Seats, 100*t.Rate, 100*t.CI95, t.Place)
	}
}

// seatResult is what one seat index (of the spec list) did over all games.
type seatResult struct{ Seats, Wins, Places int }

// play runs the games, rotating which arm each spec sits on.
func play(settings ludo.Settings, makers []func(uint64) ludo.Bot, games int, seed uint64, workers int) []seatResult {
	n := len(makers)
	totals := make([]seatResult, n)
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]seatResult, n)
			for game := range jobs {
				places := playOne(settings, makers, game, seed)
				for spec, place := range places {
					local[spec].Seats++
					local[spec].Places += place
					if place == 1 {
						local[spec].Wins++
					}
				}
			}
			mu.Lock()
			for i := range totals {
				totals[i].Seats += local[i].Seats
				totals[i].Wins += local[i].Wins
				totals[i].Places += local[i].Places
			}
			mu.Unlock()
		}()
	}
	for game := 0; game < games; game++ {
		jobs <- game
	}
	close(jobs)
	wg.Wait()
	return totals
}

// playOne plays a game and returns each spec's place.
func playOne(settings ludo.Settings, makers []func(uint64) ludo.Bot, game int, seed uint64) []int {
	n := len(makers)
	var key [32]byte
	src := mrand.NewPCG(seed, uint64(game))
	for i := range key {
		key[i] = byte(src.Uint64())
	}
	g := ludo.NewGame("ARENA", ludo.Options{Seed: &key, Clock: func() int64 { return 0 }})
	raw, _ := json.Marshal(settings)
	host := ""
	seatSpec := make([]int, n) // seat → spec index
	bots := make([]ludo.Bot, n)
	for seat := 0; seat < n; seat++ {
		spec := (seat + game) % n
		seatSpec[seat] = spec
		id, err := g.AddPlayer(fmt.Sprintf("bot%d", seat), ludo.KindHuman)
		if err != nil {
			fail("%v", err)
		}
		if host == "" {
			host = id
		}
		bots[seat] = makers[spec](seed*1_000_003 + uint64(game)*7 + uint64(seat))
	}
	if err := g.Do(host, ludo.Command{Type: "settings", Settings: raw}); err != nil {
		fail("%v", err)
	}
	if err := g.Do(host, ludo.Command{Type: "start"}); err != nil {
		fail("%v", err)
	}
	for steps := 0; g.Status == ludo.StatusPlaying; steps++ {
		if steps > 200000 {
			fail("game %d did not end", game)
		}
		seat := g.Turn
		c, err := ludo.Choose(bots[seat], g)
		if err != nil {
			fail("game %d: %v", game, err)
		}
		if err := g.Do(g.Players[seat].ID, c); err != nil {
			fail("game %d: %v (%+v)", game, err, c)
		}
	}
	places := make([]int, n)
	for seat, p := range g.Players {
		places[seatSpec[seat]] = p.Place
	}
	return places
}

// runTune is a small (1+1) evolution strategy over Hard's weights: each
// round perturbs the incumbent and keeps the candidate if it beats Normal
// more often over the same games (common random numbers keep the comparison
// fair). Modest on purpose: a few minutes on a laptop.
func runTune(settings ludo.Settings, games, rounds int, seed uint64, workers int) {
	normal := func(s uint64) ludo.Bot { b, _ := ludo.NewBot(ludo.Normal, s); return b }
	score := func(w ludo.Weights, round uint64) float64 {
		hard := func(s uint64) ludo.Bot { return ludo.NewHard(w, s) }
		r := play(settings, []func(uint64) ludo.Bot{hard, normal}, games, seed+round*977, workers)
		return float64(r[0].Wins) / float64(r[0].Seats)
	}
	best := ludo.HardWeights
	rng := mrand.New(mrand.NewPCG(seed, 99))
	sigma := 0.25
	for round := 1; round <= rounds; round++ {
		cand := perturb(best, rng, sigma)
		a, b := score(best, uint64(round)), score(cand, uint64(round))
		verdict := "keep"
		if b > a {
			best, verdict = cand, "TAKE"
			sigma = min(sigma*1.2, 0.6)
		} else {
			sigma = max(sigma*0.92, 0.05)
		}
		fmt.Printf("round %2d  incumbent %.3f  candidate %.3f  %s  sigma %.2f\n", round, a, b, verdict, sigma)
	}
	final := score(best, 100000)
	out, _ := json.Marshal(best)
	fmt.Printf("best weights %s\nwin rate vs Normal on fresh games: %.3f\n", out, final)
}

func perturb(w ludo.Weights, rng *mrand.Rand, sigma float64) ludo.Weights {
	f := func(x float64) float64 { return math.Max(0, x*math.Exp(sigma*rng.NormFloat64())) }
	return ludo.Weights{Step: w.Step, Out: f(w.Out), Column: f(w.Column), Home: f(w.Home), Risk: f(w.Risk), Opp: f(w.Opp),
		Leader: f(w.Leader), Spread: f(w.Spread), Blocker: f(w.Blocker)}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ludo-arena: "+format+"\n", args...)
	os.Exit(1)
}
