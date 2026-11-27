package main

import (
	"flag"
	"path/filepath"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/valuenet"
)

// status: write run.json by hand (between jobs) and optionally log an event.
func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "work directory")
	phase := fs.String("phase", "setup", "phase")
	label := fs.String("label", "", "phase label")
	msg := fs.String("msg", "", "message")
	ev := fs.String("event", "", "event text to append")
	level := fs.String("level", "info", "event level")
	fs.Parse(args)
	total, _ := ledgerTotal(*dir)
	started := nowMS()
	var old runStatus
	if readJSON(filepath.Join(*dir, "status", "run.json"), &old) == nil && old.StartedAt > 0 {
		started = old.StartedAt
	}
	st := runStatus{Phase: *phase, PhaseLabel: *label, StartedAt: started, UpdatedAt: nowMS(), GamesTotal: total,
		GamesTarget: 1_000_000, Message: *msg, CPUTempC: cpuTemp()}
	if err := writeJSONAtomic(filepath.Join(*dir, "status", "run.json"), st); err != nil {
		fatalf("status: %v", err)
	}
	if *ev != "" {
		event(*dir, *level, "%s", *ev)
	}
}

// initnet writes an untrained network (a placeholder model file).
func cmdInitNet(args []string) {
	fs := flag.NewFlagSet("initnet", flag.ExitOnError)
	out := fs.String("out", "model.bin", "output path")
	h1 := fs.Int("h1", 128, "first hidden layer")
	h2 := fs.Int("h2", 64, "second hidden layer")
	fs.Parse(args)
	net := valuenet.NewNet(valuenet.NumFeatures, *h1, *h2, 1)
	if err := net.Save(*out); err != nil {
		fatalf("initnet: %v", err)
	}
}

// paramstatus: write status/params.json for a knob file (the chosen knobs).
func cmdParamStatus(args []string) {
	fs := flag.NewFlagSet("paramstatus", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "work directory")
	path := fs.String("params", "", "knobs JSON (default <dir>/tune/best.json)")
	fs.Parse(args)
	if *path == "" {
		*path = filepath.Join(*dir, "tune", "best.json")
	}
	p := ai.DefaultParams
	if err := readJSON(*path, &p); err != nil {
		fatalf("paramstatus: %v", err)
	}
	writeParamsStatus(*dir, normalise(p.Vector()))
}
