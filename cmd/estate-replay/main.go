// Command estate-replay checks and analyses Estate game records
// (docs/GAME-RECORDS.md).
//
//	estate-replay check FILE...       replay records, stop at the first mismatch
//	estate-replay info FILE           who played, how it ended, the trades
//	estate-replay analyse [flags] FILE
//	                                  win probabilities, blunders, best moves:
//	                                  writes FILE's id .analysis.md and .json
//	estate-replay repair [-out DIR] FILE...
//	                                  restore what records from 0.10.1 to
//	                                  0.11.1 lost, so they replay again
//	estate-replay hands FILE          a poker record, hand by hand
//	estate-replay demo [flags] -out FILE
//	                                  play a computer game and record it
//
// FILE is a finished record (.json.gz) or the journal of a game still being
// played (<data>/games/live/<id>.jsonl). Poker and Ludo records are not
// replayed: check and analyse skip them, info and hands read them.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gameanalysis"
	"github.com/tdar200/estate-games/internal/gamerecord"
	"github.com/tdar200/estate-games/internal/hardai"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "check":
		err = check(args)
	case "info":
		err = info(args)
	case "analyse", "analyze":
		err = analyse(args)
	case "repair":
		err = repair(args)
	case "hands":
		err = hands(args)
	case "demo":
		err = demo(args)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "estate-replay: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "estate-replay:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage:
  estate-replay check FILE...         replay each record through the engine; stop at the first mismatch
  estate-replay info FILE             players, outcome, trades and auctions of a record (poker, Ludo, quiz and closest: a summary)
  estate-replay hands FILE            a poker record, hand by hand: everyone's cards, the board, who won
  estate-replay repair [-out DIR] FILE...
                                      restore the computers' reasons that records from 0.10.1 to 0.11.1 lost,
                                      so they replay again; writes <id>.repaired.json.gz unless -out is given
  estate-replay analyse [flags] FILE  score every decision; write <id>.analysis.md and <id>.analysis.json
      -quick           a fast, rougher analysis (24 rollouts per position instead of 300)
      -rollouts N      rollouts per position (default 300; -quick: 24)
      -turns N         turn starts a rollout looks ahead (default: one full round)
      -trades          let the rollout policy propose trades too (several times slower)
      -net-only        the value network alone, no rollouts (fastest, crudest)
      -workers N       positions scored at once, one core each (default 4)
      -seed N          rollout seed (default 1)
      -out DIR         where to write the report (default: next to FILE)
  estate-replay demo [flags] -out FILE
      -seats LIST      computer levels, e.g. expert,hard,normal,easy (default)
      -max-turns N     end the game early after N turns (default 300)
      -seed N          dice and bots seed

FILE is a finished record (<data>/games/*.json.gz) or the journal of a game in
progress (<data>/games/live/*.jsonl).
`)
}

// load reads a record or a journal.
func load(path string) (*gamerecord.Record, error) {
	if strings.HasSuffix(path, ".jsonl") {
		j, err := gamerecord.ReadJournal(path)
		if err != nil {
			return nil, err
		}
		rec := j.Finish()
		gamerecord.Annotate(rec, "in progress")
		return rec, nil
	}
	rec, err := gamerecord.Read(path)
	if err != nil {
		return nil, err
	}
	if !rec.Verified {
		// Records written before 0.11.2 kept the standings from where the
		// replay stopped; work them out again from the real end.
		reason := ""
		if rec.Outcome != nil {
			reason = rec.Outcome.Reason
		}
		gamerecord.Annotate(rec, reason)
	}
	return rec, nil
}

func check(args []string) error {
	if len(args) == 0 {
		return errors.New("check: no records given")
	}
	failed := 0
	for _, path := range args {
		if table, err := loadTable(path); err == nil {
			fmt.Printf("skip %s: %s records are not replayed\n", path, table.GameType)
			continue
		}
		rec, err := load(path)
		if err != nil {
			fmt.Printf("FAIL %s: %v\n", path, err)
			failed++
			continue
		}
		began := time.Now()
		g, err := gamerecord.Verify(rec)
		if err != nil {
			fmt.Printf("FAIL %s: %v\n", path, err)
			failed++
			continue
		}
		note := ""
		if len(rec.Gaps) > 0 {
			note = fmt.Sprintf(", %d gap(s) from a crash at step(s) %v", len(rec.Gaps), rec.Gaps)
		}
		fmt.Printf("ok   %s: %d steps, %d turns, %s%s (%s)\n", path, len(rec.Steps), gamerecord.TurnNumber(g)-1, g.Status, note, time.Since(began).Round(time.Millisecond))
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d record(s) do not replay", failed, len(args))
	}
	return nil
}

func info(args []string) error {
	if len(args) != 1 {
		return errors.New("info: give one record")
	}
	if table, err := loadTable(args[0]); err == nil {
		printTableInfo(table)
		return nil
	}
	rec, err := load(args[0])
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, p := range rec.Players {
		names[p.ID] = p.Name
	}
	fmt.Printf("%s  room %s (%s), app %s, dice %s\n", rec.ID, rec.Code, rec.Room, rec.AppVersion, rec.DiceMode)
	fmt.Printf("started %s", time.UnixMilli(rec.StartedAt).Format(time.DateTime))
	if rec.EndedAt > 0 {
		fmt.Printf(", ended %s (%s)", time.UnixMilli(rec.EndedAt).Format(time.DateTime), time.Duration(rec.EndedAt-rec.StartedAt)*time.Millisecond/time.Second*time.Second)
	}
	fmt.Printf("\n%d steps, %d turns, verified %v %s\n", len(rec.Steps), rec.Turns, rec.Verified, rec.VerifyError)
	if !rec.Verified {
		fmt.Println("This record does not replay to the end: the outcome is from its final state, but the trades, auctions and loans stop at that step. Try estate-replay repair.")
	}
	fmt.Println("\nPlayers:")
	for _, p := range rec.Players {
		who := p.Input
		if p.Kind == "ai" {
			who = "computer, " + p.Difficulty
		}
		fmt.Printf("  %d. %-14s %-22s %s\n", p.Seat+1, p.Name, who, p.TokenKind)
	}
	if o := rec.Outcome; o != nil {
		fmt.Printf("\nOutcome: %s", o.Reason)
		if o.WinnerID != "" {
			fmt.Printf(", %s won", names[o.WinnerID])
		}
		fmt.Println()
		for _, s := range o.Standings {
			fmt.Printf("  %d. %-14s cash $%d, net worth $%d", s.Place, names[s.PlayerID], s.Cash, s.NetWorth)
			if s.Bankrupt {
				fmt.Printf(", bankrupt at step %d", s.OutStep)
			}
			fmt.Println()
		}
	}
	for _, s := range rec.StandIns {
		fmt.Printf("The computer played for %s from step %d to %d\n", names[s.PlayerID], s.FromStep, s.ToStep)
	}
	if len(rec.Trades) > 0 {
		fmt.Println("\nTrades:")
		for _, t := range rec.Trades {
			fmt.Printf("  turn %3d  %s -> %s: gives %v + $%d for %v + $%d: %s\n", t.Turn, names[t.ProposerID], names[t.TargetID], t.Offer.Properties, t.Offer.Cash, t.Request.Properties, t.Request.Cash, t.Answer)
		}
	}
	fmt.Printf("\n%d auctions, %d loans, %d gaps\n", len(rec.Auctions), len(rec.Loans), len(rec.Gaps))
	return nil
}

func analyse(args []string) error {
	flags := flag.NewFlagSet("analyse", flag.ContinueOnError)
	quick := flags.Bool("quick", false, "fast, rough analysis")
	rollouts := flags.Int("rollouts", 0, "rollouts per position")
	turns := flags.Int("turns", 0, "turns per rollout (0: one round)")
	netOnly := flags.Bool("net-only", false, "value network only")
	trades := flags.Bool("trades", false, "rollout policy proposes trades")
	workers := flags.Int("workers", 4, "positions scored at once")
	outDir := flags.String("out", "", "output directory")
	seed := flags.Uint64("seed", 1, "rollout seed")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("analyse: give one record")
	}
	path := flags.Arg(0)
	rec, err := load(path)
	if err != nil {
		return err
	}
	// Each worker's rollouts run on one goroutine: keep the process to that.
	runtime.GOMAXPROCS(max(1, *workers))
	opts := gameanalysis.Options{Quick: *quick, Rollouts: *rollouts, Turns: *turns, Trades: *trades, Workers: *workers, Seed: *seed}
	if *netOnly {
		opts.NewOracle = func() (gameanalysis.Oracle, error) { return gameanalysis.NewNetOracle(nil) }
	}
	last := time.Now()
	opts.Progress = func(done, total int) {
		if time.Since(last) > 2*time.Second || done == total {
			last = time.Now()
			fmt.Fprintf(os.Stderr, "\rscored %d/%d positions", done, total)
			if done == total {
				fmt.Fprintln(os.Stderr)
			}
		}
	}
	a, err := gameanalysis.Analyse(rec, opts)
	if err != nil {
		return err
	}
	dir := *outDir
	if dir == "" {
		dir = filepath.Dir(path)
	}
	base := rec.ID
	if base == "" {
		base = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".gz"), ".json")
	}
	mdPath := filepath.Join(dir, base+".analysis.md")
	jsonPath := filepath.Join(dir, base+".analysis.json")
	if err := os.WriteFile(mdPath, []byte(a.Markdown(rec)), 0o644); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("%d positions scored in %.1f s\n%s\n%s\n", a.Positions, float64(a.ElapsedMs)/1000, mdPath, jsonPath)
	return nil
}

// demo plays a game of computers on the engine and records it.
func demo(args []string) error {
	flags := flag.NewFlagSet("demo", flag.ContinueOnError)
	seats := flags.String("seats", "expert,hard,normal,easy", "computer levels")
	maxTurns := flags.Int("max-turns", 300, "end the game after this many turns")
	seed := flags.Uint64("seed", uint64(time.Now().UnixNano()), "seed")
	out := flags.String("out", "", "record file (.json.gz)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("demo: -out is required")
	}
	names := []string{"Alpha", "Blue Whale", "Redwood", "North Star", "Skylark", "Juniper", "Harbour", "Willow"}
	levels := strings.Split(*seats, ",")
	if len(levels) < 2 || len(levels) > len(names) {
		return fmt.Errorf("demo: 2 to %d seats", len(names))
	}
	g := engine.CreateGame("DEMO" + fmt.Sprint(*seed%10))
	bots := map[string]hardai.Bot{}
	var players []gamerecord.Player
	for i, level := range levels {
		lvl, err := hardai.ParseLevel(level)
		if err != nil {
			return err
		}
		p, err := engine.AddPlayer(g, names[i], engine.KindAI)
		if err != nil {
			return err
		}
		bots[p.ID] = hardai.NewBot(lvl, int64(*seed)+int64(i), hardai.Options{Workers: 1})
		players = append(players, gamerecord.Player{ID: p.ID, Seat: i, Name: p.Name, Kind: p.Kind, Color: p.Color, Difficulty: lvl.String(), Input: "ai"})
	}
	g.HostID = g.Players[0].ID
	rec, err := gamerecord.NewRecorder(g, gamerecord.Header{ID: strings.TrimSuffix(filepath.Base(*out), ".json.gz"), Room: "demo", AppVersion: "estate-replay demo", Players: players})
	if err != nil {
		return err
	}
	if err := rec.Action(g.HostID, json.RawMessage(`{"type":"start"}`), gamerecord.SourceHost); err != nil {
		return err
	}
	fallback := hardai.NewHeuristicBot(false)
	for steps := 0; g.Status == engine.StatusPlaying && steps < 100000; steps++ {
		if gamerecord.TurnNumber(g) > *maxTurns {
			if err := rec.End(""); err != nil {
				return err
			}
			break
		}
		if g.Phase == engine.PhaseOrderCountdown {
			rec.Order(g.Now())
			continue
		}
		id, orderRoll := hardai.NextActor(g)
		if id == "" {
			return fmt.Errorf("demo: nobody can move (phase %s)", g.Phase)
		}
		if orderRoll {
			if err := rec.Do(id, engine.Action{Type: engine.ActionRollForOrder}, gamerecord.SourceAI); err != nil {
				return err
			}
			continue
		}
		action, ok := bots[id].Choose(g, id)
		if !ok || rec.Do(id, action, gamerecord.SourceAI) != nil {
			if action, ok = fallback.Choose(g, id); !ok {
				return fmt.Errorf("demo: no move for %s in %s", id, g.Phase)
			}
			if err := rec.Do(id, action, gamerecord.SourceAI); err != nil {
				return fmt.Errorf("demo: fallback %s: %w", action, err)
			}
		}
	}
	reason := ""
	if len(rec.Rec.Steps) > 0 && rec.Rec.Steps[len(rec.Rec.Steps)-1].Kind == gamerecord.StepEnd {
		reason = gamerecord.EndEnded
	}
	final := rec.Finish(reason)
	if err := gamerecord.Write(*out, final); err != nil {
		return err
	}
	fmt.Printf("%s: %d turns, %d steps, %s, verified %v\n", *out, final.Turns, len(final.Steps), final.Outcome.Reason, final.Verified)
	return nil
}
