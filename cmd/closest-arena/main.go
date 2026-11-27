// Command closest-arena plays Near Thing's computer levels, and a modelled
// average person, against each other on the real rules engine
// (internal/closest) over many seeded 4-player games, one of level A against
// three of level B, and prints how often A wins with a 95% confidence
// interval. It is how the computer table in docs/CLOSEST-API.md §6 is tuned:
// the person should win about 25–35% of Quick games against three Normals
// (a fair share is 25%), and Hard should clearly beat Normal.
//
//	go run ./cmd/closest-arena                          # the standard matchups, 4000 games each
//	go run ./cmd/closest-arena -games 10000 -length full
//	go run ./cmd/closest-arena -match person:hard,easy:normal
//	go run ./cmd/closest-arena -packs internal/closest/testdata/packs
//
// Levels: easy, normal, hard and person (the modelled average person: a
// little wider than Normal guessing, betting and staking as Normal does).
// A's seat moves round the table game by game; a first place shared by k
// players counts 1/k of a win for each. Games use the embedded question
// packs (or -packs) and a simulated clock, so thousands of games take
// seconds.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/tdar200/estate-games/internal/closest"
)

const seats = 4

// result is one matchup's outcome, from side A's point of view.
type result struct {
	A       string  `json:"a"`
	B       string  `json:"b"`
	Games   int     `json:"games"`
	Wins    int     `json:"wins"` // A alone in first place
	Shared  int     `json:"shared"`
	Share   float64 `json:"share"` // (wins + shared firsts split k ways) / games
	CI95    float64 `json:"ci95"`
	AScore  float64 `json:"aScore"` // A's average final score
	BScore  float64 `json:"bScore"` // a B's average final score
	Minutes float64 `json:"minutes"`
}

func main() {
	games := flag.Int("games", 4000, "games per matchup")
	match := flag.String("match", "person:normal,hard:normal,easy:normal,person:hard,person:easy", "comma-separated a:b matchups (one a against three b)")
	length := flag.String("length", closest.LengthQuick, "game length: quick or full")
	packs := flag.String("packs", "", "a folder of question packs to use instead of the embedded ones")
	seed := flag.Uint64("seed", 1, "random seed")
	workers := flag.Int("workers", runtime.NumCPU(), "games played in parallel")
	asJSON := flag.Bool("json", false, "print JSON")
	flag.Parse()

	lib, err := closest.DefaultLibrary()
	if *packs != "" {
		lib, err = closest.LoadLibrary(os.DirFS(*packs))
	}
	if err != nil {
		fail("%v", err)
	}
	if len(lib.Questions) == 0 {
		fail("no questions: the embedded packs are empty, so pass -packs internal/closest/testdata/packs")
	}
	settings := closest.DefaultSettings()
	settings.Length, settings.Fill = *length, false
	if err := settings.Validate(); err != nil {
		fail("%v", err)
	}
	var results []result
	for _, m := range strings.Split(*match, ",") {
		a, b, ok := strings.Cut(strings.TrimSpace(m), ":")
		if !ok {
			fail("matchup %q is not a:b", m)
		}
		results = append(results, playMatch(lib, settings, a, b, *games, *seed, *workers))
	}
	if *asJSON {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"settings": settings, "questions": len(lib.Questions), "results": results})
		return
	}
	fmt.Printf("%d games per matchup, %s games, %d questions, seed %d (one A against three B; fair share 25%%)\n",
		*games, settings.Length, len(lib.Questions), *seed)
	for _, r := range results {
		fmt.Printf("  %-6s v 3 %-6s wins %5.1f%% ± %.1f  (shared %4.1f%%)  avg score %5.1f v %5.1f  %4.1f min\n",
			r.A, r.B, 100*r.Share, 100*r.CI95, 100*float64(r.Shared)/float64(r.Games), r.AScore, r.BScore, r.Minutes)
	}
}

// playMatch plays games of one a against three b in parallel.
func playMatch(lib *closest.Library, settings closest.Settings, a, b string, games int, seed uint64, workers int) result {
	r := result{A: a, B: b, Games: games}
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan int)
	share := 0.0
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local result
			localShare := 0.0
			for game := range jobs {
				o := playOne(lib, settings, a, b, game, seed)
				switch {
				case o.firsts == 1 && o.aFirst:
					local.Wins++
					localShare++
				case o.aFirst:
					local.Shared++
					localShare += 1 / float64(o.firsts)
				}
				local.AScore += o.aScore
				local.BScore += o.bScore
				local.Minutes += o.minutes
			}
			mu.Lock()
			r.Wins, r.Shared, share = r.Wins+local.Wins, r.Shared+local.Shared, share+localShare
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
	r.Share = share / n
	r.CI95 = 1.96 * math.Sqrt(r.Share*(1-r.Share)/n)
	r.AScore, r.BScore, r.Minutes = r.AScore/n, r.BScore/n, r.Minutes/n
	return r
}

type outcome struct {
	aFirst         bool
	firsts         int // players sharing first place
	aScore, bScore float64
	minutes        float64
}

// playOne plays one game on a simulated clock: jump to each deadline and
// let the engine run it.
func playOne(lib *closest.Library, settings closest.Settings, a, b string, game int, seed uint64) outcome {
	var key [32]byte
	src := mrand.NewPCG(seed, uint64(game))
	for i := range key {
		key[i] = byte(src.Uint64())
	}
	const t0 = int64(1_800_000_000_000)
	now := t0
	g := closest.NewGame("ARENA", closest.Options{Seed: &key, Clock: func() int64 { return now }, Library: lib})
	seat := game % seats // A's seat moves round the table
	var aID string
	for i := range seats {
		level := b
		if i == seat {
			level = a
		}
		id, err := g.AddComputer(fmt.Sprintf("%s %d", level, i+1), level)
		if err != nil {
			fail("%v", err)
		}
		if i == seat {
			aID = id
		}
	}
	raw, _ := json.Marshal(map[string]any{"type": "settings", "settings": settings})
	if err := g.PerformAction(closest.TableHost, raw); err != nil {
		fail("%v", err)
	}
	if err := g.PerformAction(closest.TableHost, json.RawMessage(`{"type":"start"}`)); err != nil {
		fail("%v", err)
	}
	for g.Status == closest.StatusPlaying {
		at := g.NextDeadline()
		if at == 0 {
			fail("game %d stalled in phase %s", game, g.Phase)
		}
		now = max(now, at)
		g.Advance(now)
	}
	o := outcome{firsts: len(g.WinnerIDs), aFirst: slices.Contains(g.WinnerIDs, aID), minutes: float64(now-t0) / 60000}
	for _, p := range g.Players {
		if p.ID == aID {
			o.aScore = float64(p.Score)
		} else {
			o.bScore += float64(p.Score) / (seats - 1)
		}
	}
	return o
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "closest-arena: "+format+"\n", args...)
	os.Exit(1)
}
