package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Status files for the training dashboard (cmd/mldash reads <dir>/status/*)
// and the ledger of self-play games (<dir>/ledger/<job>.json, one file per
// job, summed for the running total).

func nowMS() int64 { return time.Now().UnixMilli() }

// writeJSONAtomic writes v to path through a temporary file and a rename, so
// readers never see a partial file.
func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp%d", path, os.Getpid())
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var appendMu sync.Mutex

// appendJSONL appends v as one JSON line to path.
func appendJSONL(path string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	appendMu.Lock()
	defer appendMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// ledger records the self-play games of one job.
type ledger struct {
	dir string
	job string
}

type ledgerEntry struct {
	Job       string `json:"job"`
	Games     int64  `json:"games"`
	UpdatedAt int64  `json:"updatedAt"`
}

func (l ledger) path() string { return filepath.Join(l.dir, "ledger", l.job+".json") }

// load returns the games already recorded for this job (0 if none).
func (l ledger) load() int64 {
	var e ledgerEntry
	data, err := os.ReadFile(l.path())
	if err != nil || json.Unmarshal(data, &e) != nil {
		return 0
	}
	return e.Games
}

func (l ledger) save(games int64) {
	if err := writeJSONAtomic(l.path(), ledgerEntry{Job: l.job, Games: games, UpdatedAt: nowMS()}); err != nil {
		logf("ledger: %v", err)
	}
}

// ledgerTotal sums the games of every job, and returns the per-job counts.
func ledgerTotal(dir string) (int64, map[string]int64) {
	paths, _ := filepath.Glob(filepath.Join(dir, "ledger", "*.json"))
	var total int64
	jobs := map[string]int64{}
	for _, p := range paths {
		var e ledgerEntry
		data, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(data, &e) != nil {
			continue
		}
		total += e.Games
		jobs[strings.TrimSuffix(filepath.Base(p), ".json")] = e.Games
	}
	return total, jobs
}

// runStatus is status/run.json.
type runStatus struct {
	Phase       string  `json:"phase"`
	PhaseLabel  string  `json:"phaseLabel"`
	StartedAt   int64   `json:"startedAt"`
	UpdatedAt   int64   `json:"updatedAt"`
	GamesTotal  int64   `json:"gamesTotal"`
	GamesTarget int64   `json:"gamesTarget"`
	GamesPerSec float64 `json:"gamesPerSec"`
	EtaSec      *int64  `json:"etaSec"`
	Message     string  `json:"message"`
	CPUTempC    *int    `json:"cpuTempC,omitempty"` // CPU package temperature (Linux hwmon), if readable
}

// reporter keeps status/run.json fresh (every few seconds) for one job and
// records the job's games in the ledger.
type reporter struct {
	dir    string
	phase  string
	led    ledger
	start  int64
	mu     sync.Mutex
	label  string
	msg    string
	eta    *int64
	games  int64 // this job's games, including earlier runs of it
	rateT  time.Time
	rateG  int64
	rate   float64
	stop   chan struct{}
	done   chan struct{}
	extras func() // called on every tick (more status files)
	tempAt time.Time
}

func newReporter(dir, phase, job string) *reporter {
	start := nowMS()
	var old runStatus // keep the start of the whole run (a finished run does not count)
	if readJSON(filepath.Join(dir, "status", "run.json"), &old) == nil && old.StartedAt > 0 && old.StartedAt < start && old.Phase != "done" {
		start = old.StartedAt
	}
	r := &reporter{dir: dir, phase: phase, led: ledger{dir: dir, job: job}, start: start,
		stop: make(chan struct{}), done: make(chan struct{})}
	r.games = r.led.load()
	r.rateT, r.rateG = time.Now(), r.games
	go r.loop()
	return r
}

func (r *reporter) loop() {
	defer close(r.done)
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			r.flush()
			return
		case <-t.C:
			r.flush()
		}
	}
}

// addGames counts finished games.
func (r *reporter) addGames(n int64) {
	r.mu.Lock()
	r.games += n
	r.mu.Unlock()
}

func (r *reporter) jobGames() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.games
}

// set updates the label, message and ETA (nil = unknown).
func (r *reporter) set(label, msg string, eta *int64) {
	r.mu.Lock()
	r.label, r.msg, r.eta = label, msg, eta
	r.mu.Unlock()
}

func (r *reporter) flush() {
	r.mu.Lock()
	games := r.games
	now := time.Now()
	if dt := now.Sub(r.rateT).Seconds(); dt >= 20 {
		inst := float64(games-r.rateG) / dt
		if r.rate == 0 {
			r.rate = inst
		} else {
			r.rate = 0.5*r.rate + 0.5*inst
		}
		r.rateT, r.rateG = now, games
	}
	st := runStatus{Phase: r.phase, PhaseLabel: r.label, StartedAt: r.start, UpdatedAt: nowMS(),
		GamesTarget: 1_000_000, GamesPerSec: r.rate, EtaSec: r.eta, Message: r.msg}
	extras := r.extras
	r.mu.Unlock()
	r.led.save(games)
	st.GamesTotal, _ = ledgerTotal(r.dir)
	st.CPUTempC = cpuTemp()
	if st.CPUTempC != nil && time.Since(r.tempAt) >= time.Minute {
		r.tempAt = time.Now()
		load := ""
		if b, err := os.ReadFile("/proc/loadavg"); err == nil {
			load = strings.Fields(string(b))[0]
		}
		_ = appendJSONL(filepath.Join(r.dir, "status", "temps.jsonl"), map[string]any{"at": nowMS(), "cpuTempC": *st.CPUTempC, "load": load, "phase": r.phase})
	}
	if err := writeJSONAtomic(filepath.Join(r.dir, "status", "run.json"), st); err != nil {
		logf("status: %v", err)
	}
	if extras != nil {
		extras()
	}
}

// close writes a last status and stops the ticker.
func (r *reporter) close() {
	close(r.stop)
	<-r.done
}

// event appends a plain-English line to status/events.jsonl (and the log).
func event(dir, level, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	logf("[%s] %s", level, text)
	_ = appendJSONL(filepath.Join(dir, "status", "events.jsonl"), map[string]any{"at": nowMS(), "level": level, "text": text})
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

func etaPtr(sec float64) *int64 {
	if sec <= 0 || sec != sec || sec > 1e9 {
		return nil
	}
	v := int64(sec)
	return &v
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// cpuTemp reads the CPU package temperature from Linux hwmon (coretemp
// "Package id 0", else the hottest coretemp sensor); nil where unavailable.
func cpuTemp() *int {
	dirs, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	best := -1
	for _, d := range dirs {
		name, err := os.ReadFile(filepath.Join(d, "name"))
		if err != nil || strings.TrimSpace(string(name)) != "coretemp" {
			continue
		}
		inputs, _ := filepath.Glob(filepath.Join(d, "temp*_input"))
		for _, in := range inputs {
			raw, err := os.ReadFile(in)
			if err != nil {
				continue
			}
			var milli int
			if _, err := fmt.Sscan(strings.TrimSpace(string(raw)), &milli); err != nil {
				continue
			}
			label, _ := os.ReadFile(strings.TrimSuffix(in, "_input") + "_label")
			if strings.HasPrefix(strings.TrimSpace(string(label)), "Package") {
				v := milli / 1000
				return &v
			}
			best = max(best, milli/1000)
		}
	}
	if best < 0 {
		return nil
	}
	return &best
}
