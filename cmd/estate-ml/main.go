// Command estate-ml trains the EXPERT computer player by self-play.
//
//	estate-ml tune   [-dir D] [-gens 80] [-pop 24] [-seeds 100]   tune the heuristic's knobs (sep-CMA-ES)
//	estate-ml data   [-dir D] [-games N] [-params P]              play games and record positions
//	estate-ml train  [-dir D] [-epochs E]                         train the value network
//	estate-ml eval   [-dir D] -name N -- <arena flags>             run cmd/arena and record the result
//	estate-ml gm     [-dir D] [-gens 4] [-games 8000]              Grandmaster's network: league data, train, gate (loop)
//	estate-ml knobs                                                list the knobs
//
// Everything lives under -dir (default ~/estate-ml): tune/, data/, net/,
// ledger/ (self-play games per job; their sum is the running total), status/
// (files read by the training dashboard, cmd/mldash) and logs/. Every job
// checkpoints and resumes where it stopped when started again with the same
// -dir. See docs/ai/EXPERT.md.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/tdar200/estate-games/internal/ai"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	debug.SetGCPercent(400)
	args := os.Args[2:]
	switch os.Args[1] {
	case "tune":
		cmdTune(args)
	case "data":
		cmdData(args)
	case "train":
		cmdTrain(args)
	case "eval":
		cmdEval(args)
	case "status":
		cmdStatus(args)
	case "evalnet":
		cmdEvalNet(args)
	case "paramstatus":
		cmdParamStatus(args)
	case "gm":
		cmdGM(args)
	case "gmsummary":
		cmdGMSummary(args)
	case "initnet":
		cmdInitNet(args)
	case "knobs":
		for _, k := range ai.Knobs {
			fmt.Printf("%-22s %-9s default %8.3f range [%g, %g]\n", k.Name, k.Group, k.Default, k.Min, k.Max)
		}
		fmt.Printf("%d knobs\n", len(ai.Knobs))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: estate-ml tune|data|train|eval|status|knobs [flags]")
	os.Exit(2)
}

func defaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "estate-ml"
	}
	return filepath.Join(home, "estate-ml")
}

func fatalf(format string, args ...any) {
	logf(format, args...)
	os.Exit(1)
}
