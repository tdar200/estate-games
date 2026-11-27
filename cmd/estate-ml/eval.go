package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// eval: run cmd/arena for one matchup, then record its result in
// status/eval.json (and eval/<slug>.json). Arena flags follow "--". The win
// rate, CI and timing are the first -seats entry's; Elo is a Bradley-Terry
// fit over every recorded matchup together (Normal = 1500).

type matchup struct {
	Name     string     `json:"name"`
	Seats    string     `json:"seats"`
	Rules    string     `json:"rules"`
	Budget   string     `json:"budget"`
	Games    int        `json:"games"`
	WinRate  float64    `json:"winRate"`
	CI95     [2]float64 `json:"ci95"`
	Elo      float64    `json:"elo"`
	P50ms    float64    `json:"p50ms"`
	P95ms    float64    `json:"p95ms"`
	Errors   int        `json:"errors"`
	Pairs    []pairRec  `json:"pairs"`
	Seconds  float64    `json:"seconds"`
	Finished int64      `json:"finishedAt"`
}

type pairRec struct {
	Winner string `json:"winner"`
	Loser  string `json:"loser"`
	Count  int    `json:"count"`
}

type arenaReport struct {
	Config struct {
		Seats  string `json:"seats"`
		Rules  string `json:"rules"`
		Budget string `json:"budget"`
	} `json:"config"`
	Games  int `json:"games"`
	Errors int `json:"errors"`
	Seats  []struct {
		Level   string  `json:"level"`
		Wins    int     `json:"wins"`
		WinRate float64 `json:"winRate"`
		CILow   float64 `json:"ci95Low"`
		CIHigh  float64 `json:"ci95High"`
	} `json:"seats"`
	Timing []struct {
		Level string  `json:"level"`
		P50ms float64 `json:"p50ms"`
		P95ms float64 `json:"p95ms"`
	} `json:"timing"`
	MCByLevel []struct {
		Level string  `json:"level"`
		P50ms float64 `json:"p50ms"`
		P95ms float64 `json:"p95ms"`
	} `json:"monteCarloByLevel"`
	Pairs       []pairRec `json:"pairs"`
	WallSeconds float64   `json:"wallSeconds"`
}

type evalFile struct {
	UpdatedAt int64                         `json:"updatedAt"`
	Matchups  []matchup                     `json:"matchups"`
	Elo       map[string]float64            `json:"elo"`        // classic rules
	EloRules  map[string]map[string]float64 `json:"eloByRules"` // one fit per rule set
}

var progressRE = regexp.MustCompile(`(\d+)/(\d+) games`)

func cmdEval(args []string) {
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "work directory")
	name := fs.String("name", "", "matchup name, e.g. \"EXPERT vs 3×HARD\"")
	arena := fs.String("arena", "", "arena binary (default <dir>/bin/arena)")
	file := fs.String("file", "", "results file (default <dir>/status/eval.json; experiments go elsewhere)")
	fs.Parse(args)
	arenaArgs := fs.Args()
	if *name == "" || len(arenaArgs) == 0 {
		fatalf("usage: estate-ml eval -name NAME -- <arena flags>")
	}
	if *arena == "" {
		*arena = filepath.Join(*dir, "bin", "arena")
	}
	slug := slugify(*name)
	outJSON := filepath.Join(*dir, "eval", slug+".json")
	_ = os.MkdirAll(filepath.Dir(outJSON), 0o755)
	cmd := exec.Command(*arena, append(append([]string{}, arenaArgs...), "-json", outJSON)...)
	logPath := filepath.Join(*dir, "eval", slug+".txt")
	stdout, err := os.Create(logPath)
	if err != nil {
		fatalf("eval: %v", err)
	}
	defer stdout.Close()
	cmd.Stdout = stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		fatalf("eval: %v", err)
	}
	rep := newReporter(*dir, "eval", "eval-"+slug)
	defer rep.close()
	event(*dir, "info", "Evaluation started: %s (%s)", *name, strings.Join(arenaArgs, " "))
	start := time.Now()
	if err := cmd.Start(); err != nil {
		fatalf("eval: %v", err)
	}
	label := "Evaluating · " + *name
	rep.set(label, "Starting "+*name, nil)
	sc := bufio.NewScanner(stderr)
	sc.Split(splitCRLF)
	last := 0
	for sc.Scan() {
		m := progressRE.FindStringSubmatch(sc.Text())
		if m == nil {
			if t := strings.TrimSpace(sc.Text()); t != "" {
				logf("arena: %s", t)
			}
			continue
		}
		done, _ := strconv.Atoi(m[1])
		total, _ := strconv.Atoi(m[2])
		if done > last {
			rep.addGames(int64(done - last))
			last = done
		}
		rate := float64(done) / time.Since(start).Seconds()
		rep.set(label, fmt.Sprintf("%s: %d/%d games", *name, done, total), etaPtr(float64(total-done)/rate))
	}
	if err := cmd.Wait(); err != nil {
		event(*dir, "warn", "Evaluation %s: arena exited with %v", *name, err)
	}
	var ar arenaReport
	if err := readJSON(outJSON, &ar); err != nil {
		fatalf("eval: no arena report: %v", err)
	}
	if ar.Games > last {
		rep.addGames(int64(ar.Games - last))
	}
	m := matchup{Name: *name, Seats: ar.Config.Seats, Rules: ar.Config.Rules, Budget: ar.Config.Budget, Games: ar.Games,
		Errors: ar.Errors, Pairs: ar.Pairs, Seconds: ar.WallSeconds, Finished: nowMS()}
	if m.Rules == "" {
		m.Rules = "classic"
	}
	if len(ar.Seats) > 0 {
		s := ar.Seats[0]
		m.WinRate, m.CI95 = s.WinRate, [2]float64{s.CILow, s.CIHigh}
		level := s.Level
		for _, t := range ar.Timing {
			if t.Level == level+" (all)" {
				m.P50ms, m.P95ms = t.P50ms, t.P95ms
			}
		}
		for _, t := range ar.MCByLevel {
			if t.Level == level+" (Monte Carlo)" {
				m.P50ms, m.P95ms = t.P50ms, t.P95ms
			}
		}
	}
	path := filepath.Join(*dir, "status", "eval.json")
	if *file != "" {
		path = *file
	}
	var ef evalFile
	_ = readJSON(path, &ef)
	replaced := false
	for i := range ef.Matchups {
		if ef.Matchups[i].Name == m.Name {
			ef.Matchups[i] = m
			replaced = true
		}
	}
	if !replaced {
		ef.Matchups = append(ef.Matchups, m)
	}
	// One Bradley-Terry fit per rule set; ablations (non-standard bots) are
	// left out so they do not blend into a level's rating.
	ef.EloRules = map[string]map[string]float64{}
	byRules := map[string][]matchup{}
	for _, m := range ef.Matchups {
		if !strings.HasPrefix(m.Name, "Ablation") && !strings.HasPrefix(m.Name, "AB") {
			byRules[m.Rules] = append(byRules[m.Rules], m)
		}
	}
	for rules, ms := range byRules {
		ef.EloRules[rules] = globalElo(ms)
	}
	ef.Elo = ef.EloRules["classic"]
	for i := range ef.Matchups {
		m := &ef.Matchups[i]
		lvl := strings.SplitN(m.Seats, ",", 2)[0]
		m.Elo = 0
		if fit, ok := ef.EloRules[m.Rules]; ok && !strings.HasPrefix(m.Name, "Ablation") {
			m.Elo = math.Round(fit[lvl])
		}
	}
	ef.UpdatedAt = nowMS()
	if err := writeJSONAtomic(path, ef); err != nil {
		fatalf("eval: %v", err)
	}
	event(*dir, "info", "%s: %.1f%% over %d games (95%% CI %.1f-%.1f), decision p50 %.0f ms, p95 %.0f ms",
		*name, 100*m.WinRate, m.Games, 100*m.CI95[0], 100*m.CI95[1], m.P50ms, m.P95ms)
	rep.set("Evaluation done · "+*name, fmt.Sprintf("%.1f%% (95%% CI %.1f-%.1f)", 100*m.WinRate, 100*m.CI95[0], 100*m.CI95[1]), nil)
}

// globalElo fits Bradley-Terry strengths to the winner-beats-loser counts
// of every matchup and maps them to Elo with Normal at 1500 (or the first
// level if Normal never played).
func globalElo(ms []matchup) map[string]float64 {
	pair := map[[2]string]float64{}
	levels := map[string]bool{}
	for _, m := range ms {
		for _, p := range m.Pairs {
			pair[[2]string{p.Winner, p.Loser}] += float64(p.Count)
			levels[p.Winner], levels[p.Loser] = true, true
		}
	}
	names := sortedKeys(levels)
	out := map[string]float64{}
	if len(names) == 0 {
		return out
	}
	strength := map[string]float64{}
	for _, n := range names {
		strength[n] = 1
	}
	for iter := 0; iter < 5000; iter++ {
		next := map[string]float64{}
		for _, i := range names {
			wins, denom := 0.0, 0.0
			for _, j := range names {
				if i == j {
					continue
				}
				wij, wji := pair[[2]string{i, j}], pair[[2]string{j, i}]
				wins += wij
				if g := wij + wji; g > 0 {
					denom += g / (strength[i] + strength[j])
				}
			}
			next[i] = (wins + 0.5) / (denom + 1/(strength[i]+1))
		}
		strength = next
	}
	anchor := names[0]
	if _, ok := strength["normal"]; ok {
		anchor = "normal"
	}
	for _, n := range names {
		out[n] = math.Round(1500 + 400*math.Log10(strength[n]/strength[anchor]))
	}
	return out
}

func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '×':
			b.WriteRune('x')
		default:
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// splitCRLF splits on \r or \n (arena rewrites its progress line with \r).
func splitCRLF(data []byte, atEOF bool) (int, []byte, error) {
	for i, c := range data {
		if c == '\r' || c == '\n' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

var _ = sort.Strings
