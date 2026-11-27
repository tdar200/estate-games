package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// grandmaster.json: a small summary of the Grandmaster work for the training
// dashboard, mldash (status/grandmaster.json): what is running, and the results
// so far. Every writer merges into the file, so the loop's progress and the
// results recorded by other tools (gm-attack) live side by side.

type gmResult struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Low   float64 `json:"low"`
	High  float64 `json:"high"`
	Games int     `json:"games"`
	Unit  string  `json:"unit,omitempty"`
}

type gmSummary struct {
	UpdatedAt int64      `json:"updatedAt"`
	Phase     string     `json:"phase"`
	Activity  string     `json:"activity"`
	Eta       *int64     `json:"eta"` // seconds left in the current activity (null: unknown)
	Results   []gmResult `json:"results"`
	Notes     []string   `json:"notes"`
}

var gmSummaryMu sync.Mutex

// updateGMSummary reads status/grandmaster.json, applies f and writes it back.
func updateGMSummary(dir string, f func(s *gmSummary)) {
	gmSummaryMu.Lock()
	defer gmSummaryMu.Unlock()
	path := filepath.Join(dir, "status", "grandmaster.json")
	var s gmSummary
	_ = readJSON(path, &s)
	f(&s)
	s.UpdatedAt = nowMS()
	if s.Results == nil {
		s.Results = []gmResult{}
	}
	if s.Notes == nil {
		s.Notes = []string{}
	}
	if err := writeJSONAtomic(path, s); err != nil {
		logf("grandmaster.json: %v", err)
	}
}

// setGMProgress records what is running now (throttled to one write per 10 s
// unless the phase changes).
var gmProgressLast struct {
	at    time.Time
	phase string
}

func setGMProgress(dir, phase, activity string, eta *int64) {
	gmSummaryMu.Lock()
	recent := time.Since(gmProgressLast.at) < 10*time.Second && gmProgressLast.phase == phase
	if !recent {
		gmProgressLast.at, gmProgressLast.phase = time.Now(), phase
	}
	gmSummaryMu.Unlock()
	if recent {
		return
	}
	updateGMSummary(dir, func(s *gmSummary) {
		s.Phase, s.Activity, s.Eta = phase, activity, eta
	})
}

// gmsummary: set fields of status/grandmaster.json by hand.
//
//	estate-ml gmsummary -phase "Phase 1" -activity "arena: GM vs 3 Expert"
//	estate-ml gmsummary -result "g-expert|GM vs 3 Expert (4 players)|36.7|32.9|40.6|600"
//	estate-ml gmsummary -note "..."
func cmdGMSummary(args []string) {
	fs := flag.NewFlagSet("gmsummary", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "work directory (writes <dir>/status/grandmaster.json)")
	phase := fs.String("phase", "", "phase")
	activity := fs.String("activity", "", "activity")
	eta := fs.Int64("eta", -1, "seconds left (-1 = unknown)")
	var results, notes multiFlag
	fs.Var(&results, "result", `"id|label|value|low|high|games" (percent); repeatable`)
	fs.Var(&notes, "note", "a note to add; repeatable")
	clear := fs.Bool("clear-notes", false, "drop the existing notes first")
	fs.Parse(args)
	updateGMSummary(*dir, func(s *gmSummary) {
		if *phase != "" {
			s.Phase = *phase
		}
		if *activity != "" {
			s.Activity = *activity
		}
		if *eta >= 0 {
			v := *eta
			s.Eta = &v
		} else if *activity != "" {
			s.Eta = nil
		}
		for _, r := range results {
			p := strings.Split(r, "|")
			if len(p) != 6 {
				fatalf("gmsummary: bad -result %q", r)
			}
			res := gmResult{ID: p[0], Label: p[1], Unit: "%"}
			res.Value, _ = strconv.ParseFloat(p[2], 64)
			res.Low, _ = strconv.ParseFloat(p[3], 64)
			res.High, _ = strconv.ParseFloat(p[4], 64)
			res.Games, _ = strconv.Atoi(p[5])
			mergeResult(s, res)
		}
		if *clear {
			s.Notes = nil
		}
		s.Notes = append(s.Notes, notes...)
	})
	data, _ := json.MarshalIndent(readSummaryFile(*dir), "", "  ")
	fmt.Println(string(data))
}

func mergeResult(s *gmSummary, r gmResult) {
	for i := range s.Results {
		if s.Results[i].ID == r.ID {
			s.Results[i] = r
			return
		}
	}
	s.Results = append(s.Results, r)
}

func readSummaryFile(dir string) gmSummary {
	var s gmSummary
	data, err := os.ReadFile(filepath.Join(dir, "status", "grandmaster.json"))
	if err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// ------------------------------------------------------------ temperature

// maxTempC is the CPU package temperature above which heavy work pauses
// (-max-temp; the PC once froze under a 24-core load, so the jobs back off).
var maxTempC = 90

var thermalMu sync.Mutex

// thermalWait blocks while the CPU is at or above maxTempC, until it has
// cooled by 8°C (no-op where the temperature cannot be read).
func thermalWait(dir string) {
	t := cpuTemp()
	if t == nil || *t < maxTempC || maxTempC <= 0 {
		return
	}
	thermalMu.Lock()
	defer thermalMu.Unlock()
	t = cpuTemp()
	if t == nil || *t < maxTempC {
		return
	}
	event(dir, "warn", "CPU at %d°C: pausing until it cools to %d°C", *t, maxTempC-8)
	for {
		time.Sleep(20 * time.Second)
		if t = cpuTemp(); t == nil || *t <= maxTempC-8 {
			break
		}
	}
	event(dir, "info", "CPU cooled; resuming")
}
