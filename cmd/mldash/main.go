// Command mldash serves a live dashboard of the self-play training run: games
// played, the step plan, the tuning curve, the value network, strength results
// and the machine's load and temperature. It reads what the training jobs write
// under <dir>/status, <dir>/eval and <dir>/ledger (see docs/ai/EXPERT.md) and
// never writes anything.
//
//	mldash -addr :3299 -dir ~/estate-ml
package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var indexHTML []byte

func main() {
	home, _ := os.UserHomeDir()
	addr := flag.String("addr", ":3299", "listen address")
	dir := flag.String("dir", filepath.Join(home, "estate-ml"), "training directory (reads <dir>/status)")
	flag.Parse()

	sys := &sampler{}
	go sys.run()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(indexHTML)
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		status := collect(*dir, sys)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(status)
	})
	log.Printf("training dashboard on %s (reading %s)", *addr, filepath.Join(*dir, "status"))
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// collect gathers every status file under base; missing or half-written files
// are skipped. status/plan.json, when the training writes it, is the step list
// the dashboard shows; without it the page infers the steps itself.
func collect(base string, sys *sampler) map[string]any {
	dir := filepath.Join(base, "status")
	out := map[string]any{"serverTime": time.Now().UnixMilli()}
	for _, name := range []string{"run", "params", "data", "eval", "plan", "gm", "grandmaster"} {
		if v := readJSON(filepath.Join(dir, name+".json")); v != nil {
			out[name] = v
		}
	}
	out["tune"] = readJSONL(filepath.Join(dir, "tune.jsonl"), 2000)
	out["train"] = readJSONL(filepath.Join(dir, "train.jsonl"), 2000)
	out["events"] = readJSONL(filepath.Join(dir, "events.jsonl"), 300)
	out["temps"] = readJSONL(filepath.Join(dir, "temps.jsonl"), 480)
	out["evalFiles"] = readMatchupFiles(filepath.Join(base, "eval"))
	out["ledger"] = readDirJSON(filepath.Join(base, "ledger"))
	out["system"] = sys.snapshot()
	return out
}

// readMatchupFiles returns the result files in dir that hold a "matchups" list,
// keyed by file name.
func readMatchupFiles(dir string) map[string]any {
	out := map[string]any{}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, p := range paths {
		v, ok := readJSON(p).(map[string]any)
		if !ok {
			continue
		}
		if _, ok := v["matchups"]; ok {
			out[strings.TrimSuffix(filepath.Base(p), ".json")] = v
		}
	}
	return out
}

// readDirJSON returns every JSON file in dir (the training's job ledger).
func readDirJSON(dir string) []any {
	rows := []any{}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, p := range paths {
		if v := readJSON(p); v != nil {
			rows = append(rows, v)
		}
	}
	return rows
}

// cpuTemp reads the CPU package temperature in °C from the coretemp sensor.
func cpuTemp() (float64, bool) {
	hwmons, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, h := range hwmons {
		name, err := os.ReadFile(filepath.Join(h, "name"))
		if err != nil || strings.TrimSpace(string(name)) != "coretemp" {
			continue
		}
		labels, _ := filepath.Glob(filepath.Join(h, "temp*_label"))
		for _, l := range labels {
			label, err := os.ReadFile(l)
			if err != nil || !strings.HasPrefix(string(label), "Package") {
				continue
			}
			raw, err := os.ReadFile(strings.TrimSuffix(l, "_label") + "_input")
			if err != nil {
				continue
			}
			milli, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
			if err == nil {
				return milli / 1000, true
			}
		}
	}
	return 0, false
}

func readJSON(path string) any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var v any
	if json.Unmarshal(data, &v) != nil {
		return nil
	}
	return v
}

// readJSONL returns the last max valid lines.
func readJSONL(path string, max int) []any {
	data, err := os.ReadFile(path)
	if err != nil {
		return []any{}
	}
	var rows []any
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var v any
		if json.Unmarshal(line, &v) == nil {
			rows = append(rows, v)
		}
	}
	if len(rows) > max {
		rows = rows[len(rows)-max:]
	}
	if rows == nil {
		rows = []any{}
	}
	return rows
}

// sampler measures CPU use from /proc/stat every 2 s (Linux; elsewhere it
// reports only the core count).
type sampler struct {
	mu         sync.Mutex
	cpuPercent float64
	have       bool
}

func (s *sampler) run() {
	prevIdle, prevTotal, ok := readCPU()
	for {
		time.Sleep(2 * time.Second)
		idle, total, ok2 := readCPU()
		if ok && ok2 && total > prevTotal {
			busy := 1 - float64(idle-prevIdle)/float64(total-prevTotal)
			s.mu.Lock()
			s.cpuPercent, s.have = busy*100, true
			s.mu.Unlock()
		}
		prevIdle, prevTotal, ok = idle, total, ok2
	}
}

func readCPU() (idle, total uint64, ok bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, f := range fields[1:] {
		n, _ := strconv.ParseUint(f, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	return idle, total, true
}

func (s *sampler) snapshot() map[string]any {
	out := map[string]any{"cores": runtime.NumCPU()}
	s.mu.Lock()
	if s.have {
		out["cpuPercent"] = s.cpuPercent
	}
	s.mu.Unlock()
	if t, ok := cpuTemp(); ok {
		out["cpuTempC"] = t
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(data)); len(f) > 0 {
			out["load1"], _ = strconv.ParseFloat(f[0], 64)
		}
	}
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		var total, avail float64
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			v, _ := strconv.ParseFloat(f[1], 64)
			switch f[0] {
			case "MemTotal:":
				total = v
			case "MemAvailable:":
				avail = v
			}
		}
		if total > 0 {
			out["memTotalGB"] = total / 1024 / 1024
			out["memUsedGB"] = (total - avail) / 1024 / 1024
		}
	}
	return out
}
