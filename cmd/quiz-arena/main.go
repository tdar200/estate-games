// Command quiz-arena plays the quiz's computer levels, and a modelled average
// person, against each other on the real rules engine (internal/quiz) over
// many seeded 1-v-1 games, and prints how often the first side wins with a
// 95% confidence interval. It is how the computer table in docs/QUIZ-API.md
// §8 is tuned: the person should beat Normal in about 40–50% of Quick games
// and Hard in about 25–30%, and Hard should clearly beat Normal.
//
//	go run ./cmd/quiz-arena                         # the standard matchups, 4000 games each
//	go run ./cmd/quiz-arena -games 10000 -length full
//	go run ./cmd/quiz-arena -match person:hard,easy:normal
//
// Levels: easy, normal, hard and person (the modelled average person, who
// picks and bets as Normal does). The two sides swap seats every game; a tie
// counts as half a win for each. Games use the embedded question packs and a
// simulated clock, so thousands of games take seconds.
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

	"github.com/tdar200/estate-games/internal/quiz"
)

// result is one matchup's outcome, from side A's point of view.
type result struct {
	A       string  `json:"a"`
	B       string  `json:"b"`
	Games   int     `json:"games"`
	Wins    int     `json:"wins"` // A alone in first place
	Ties    int     `json:"ties"`
	Share   float64 `json:"share"` // (wins + ties/2) / games
	CI95    float64 `json:"ci95"`
	AScore  float64 `json:"aScore"` // average final score
	BScore  float64 `json:"bScore"`
	Minutes float64 `json:"minutes"` // average game length
}

func main() {
	games := flag.Int("games", 4000, "games per matchup")
	match := flag.String("match", "person:easy,person:normal,person:hard,hard:normal", "comma-separated a:b matchups")
	length := flag.String("length", quiz.LengthQuick, "game length: quick or full")
	seed := flag.Uint64("seed", 1, "random seed")
	workers := flag.Int("workers", runtime.NumCPU(), "games played in parallel")
	asJSON := flag.Bool("json", false, "print JSON")
	flag.Parse()

	if _, err := quiz.DefaultLibrary(); err != nil {
		fail("%v", err)
	}
	settings := quiz.DefaultSettings()
	settings.Length = *length
	if err := settings.Validate(); err != nil {
		fail("%v", err)
	}
	var results []result
	for _, m := range strings.Split(*match, ",") {
		a, b, ok := strings.Cut(strings.TrimSpace(m), ":")
		if !ok {
			fail("matchup %q is not a:b", m)
		}
		results = append(results, playMatch(settings, a, b, *games, *seed, *workers))
	}
	if *asJSON {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"settings": settings, "results": results})
		return
	}
	fmt.Printf("%d games per matchup, %s games, seed %d\n", *games, settings.Length, *seed)
	for _, r := range results {
		fmt.Printf("  %-6s beats %-6s %5.1f%% ± %.1f  (ties %4.1f%%)  avg score %5.0f v %5.0f  %4.1f min\n",
			r.A, r.B, 100*r.Share, 100*r.CI95, 100*float64(r.Ties)/float64(r.Games), r.AScore, r.BScore, r.Minutes)
	}
}

// playMatch plays games of a against b in parallel.
func playMatch(settings quiz.Settings, a, b string, games int, seed uint64, workers int) result {
	r := result{A: a, B: b, Games: games}
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan int)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local result
			for game := range jobs {
				o := playOne(settings, a, b, game, seed)
				switch {
				case o.aScore > o.bScore:
					local.Wins++
				case o.aScore == o.bScore:
					local.Ties++
				}
				local.AScore += float64(o.aScore)
				local.BScore += float64(o.bScore)
				local.Minutes += o.minutes
			}
			mu.Lock()
			r.Wins, r.Ties = r.Wins+local.Wins, r.Ties+local.Ties
			r.AScore, r.BScore, r.Minutes = r.AScore+local.AScore, r.BScore+local.BScore, r.Minutes+local.Minutes
			mu.Unlock()
		}()
	}
	for game := range games {
		jobs <- game
	}
	close(jobs)
	wg.Wait()
	n := float64(games)
	r.Share = (float64(r.Wins) + float64(r.Ties)/2) / n
	r.CI95 = 1.96 * math.Sqrt(r.Share*(1-r.Share)/n)
	r.AScore, r.BScore, r.Minutes = r.AScore/n, r.BScore/n, r.Minutes/n
	return r
}

type outcome struct {
	aScore, bScore int
	minutes        float64
}

// playOne plays one game on a simulated clock: jump to each deadline and
// let the engine run it.
func playOne(settings quiz.Settings, a, b string, game int, seed uint64) outcome {
	var key [32]byte
	src := mrand.NewPCG(seed, uint64(game))
	for i := range key {
		key[i] = byte(src.Uint64())
	}
	const t0 = int64(1_800_000_000_000)
	now := t0
	g := quiz.NewGame("ARENA", quiz.Options{Seed: &key, Clock: func() int64 { return now }})
	levels := []string{a, b}
	if game%2 == 1 {
		levels = []string{b, a} // swap seats: seat order breaks ties
	}
	ids := make([]string, 2)
	for i, level := range levels {
		id, err := g.AddComputer(fmt.Sprintf("%s %d", level, i+1), level)
		if err != nil {
			fail("%v", err)
		}
		ids[i] = id
	}
	raw, _ := json.Marshal(map[string]any{"type": "settings", "settings": settings})
	if err := g.PerformAction(quiz.TableHost, raw); err != nil {
		fail("%v", err)
	}
	if err := g.PerformAction(quiz.TableHost, json.RawMessage(`{"type":"start"}`)); err != nil {
		fail("%v", err)
	}
	for g.Status == quiz.StatusPlaying {
		at := g.NextDeadline()
		if at == 0 {
			fail("game %d stalled in phase %s", game, g.Phase)
		}
		now = max(now, at)
		g.Advance(now)
	}
	aID, bID := ids[0], ids[1]
	if game%2 == 1 {
		aID, bID = bID, aID
	}
	return outcome{aScore: g.Player(aID).Score, bScore: g.Player(bID).Score, minutes: float64(now-t0) / 60000}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "quiz-arena: "+format+"\n", args...)
	os.Exit(1)
}
