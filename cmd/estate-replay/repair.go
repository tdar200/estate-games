package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
	"github.com/tdar200/estate-games/internal/hardai"
)

// repair restores the computers' reasons that records from 0.10.1 to 0.11.1
// lost, so they replay and can be analysed again. The original is left as it
// is: the repaired record is written to -out, or next to it as
// <id>.repaired.json.gz.
func repair(args []string) error {
	flags := flag.NewFlagSet("repair", flag.ContinueOnError)
	outDir := flags.String("out", "", "where to write the repaired records")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return errors.New("repair: no records given")
	}
	failed := 0
	for _, path := range flags.Args() {
		fixed, out, err := repairOne(path, *outDir)
		switch {
		case err != nil:
			fmt.Printf("FAIL %s: %v\n", path, err)
			failed++
		case fixed == 0:
			fmt.Printf("ok   %s: nothing to repair\n", path)
		default:
			fmt.Printf("ok   %s: %d reason(s) restored, wrote %s\n", path, fixed, out)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d record(s) could not be repaired", failed, flags.NArg())
	}
	return nil
}

func repairOne(path, outDir string) (int, string, error) {
	rec, err := gamerecord.Read(path)
	if err != nil {
		return 0, "", err
	}
	fixed, err := gamerecord.RepairReasons(rec, guessReasons(rec))
	if err != nil {
		return fixed, "", err
	}
	if fixed == 0 {
		return 0, "", nil
	}
	reason := gamerecord.EndFinished
	if rec.Outcome != nil && rec.Outcome.Reason != "" {
		reason = rec.Outcome.Reason
	}
	gamerecord.Annotate(rec, reason)
	if !rec.Verified {
		return fixed, "", fmt.Errorf("still does not replay: %s", rec.VerifyError)
	}
	name := strings.TrimSuffix(filepath.Base(path), ".json.gz")
	out := filepath.Join(filepath.Dir(path), name+".repaired.json.gz")
	if outDir != "" {
		out = filepath.Join(outDir, name+".json.gz")
	}
	return fixed, out, gamerecord.Write(out, rec)
}

type tradeTerms struct {
	Offer   engine.TradeBundle `json:"offer"`
	Request engine.TradeBundle `json:"request"`
}

// maxGuessedPrice bounds the search for a refused deal's price; no price
// above it is payable in a real game.
const maxGuessedPrice = 6000

// guessReasons returns the texts to try for a step. A counter-offer's price
// is in the step. A refusal only names a price after two counters, so the
// counters already in the record are tried first, then every price.
func guessReasons(rec *gamerecord.Record) gamerecord.ReasonGuesser {
	return func(g *engine.Game, st *gamerecord.Step) []string {
		var a tradeTerms
		if json.Unmarshal(st.Action, &a) != nil {
			return nil
		}
		if st.ActionType() == engine.ActionCounterTrade {
			return hardai.CounterReasons(a.Request.Properties, a.Offer.Properties, a.Request.Cash-a.Offer.Cash)
		}
		if g.Trade == nil {
			return nil
		}
		var nets []int
		for _, earlier := range rec.Steps[:st.N] {
			var c tradeTerms
			if earlier.ActionType() == engine.ActionCounterTrade && json.Unmarshal(earlier.Action, &c) == nil {
				nets = append(nets, c.Request.Cash-c.Offer.Cash)
			}
		}
		for net := 1; net <= maxGuessedPrice; net++ {
			nets = append(nets, net)
		}
		return hardai.RefusalReasons(g.Trade.Offer.Properties, g.Trade.Request.Properties, nets)
	}
}

// hands prints a poker record hand by hand (table.go has the rest).
func hands(args []string) error {
	if len(args) != 1 {
		return errors.New("hands: give one poker record")
	}
	return printHands(args[0])
}
