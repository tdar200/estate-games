package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The training dashboard (mldash) reads ~/estate-ml/status/grandmaster.json
// ({updatedAt, phase, activity, eta, results: [{id, label, value, low, high,
// games}], notes}); estate-ml writes the progress fields, this tool merges
// its results (percent, 95% Wilson interval).

type summaryResult struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Low   float64 `json:"low"`
	High  float64 `json:"high"`
	Games int     `json:"games"`
	Unit  string  `json:"unit,omitempty"`
}

// mergeSummary sets results (by id) in the summary file at path.
func mergeSummary(path string, results ...summaryResult) {
	if path == "" {
		return
	}
	var s map[string]any
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	if s == nil {
		s = map[string]any{"phase": "", "activity": "", "eta": nil, "notes": []any{}}
	}
	var list []summaryResult
	if raw, err := json.Marshal(s["results"]); err == nil {
		_ = json.Unmarshal(raw, &list)
	}
	for _, r := range results {
		r.Unit = "%"
		found := false
		for i := range list {
			if list[i].ID == r.ID {
				list[i], found = r, true
			}
		}
		if !found {
			list = append(list, r)
		}
	}
	s["results"] = list
	s["updatedAt"] = time.Now().UnixMilli()
	data, _ := json.MarshalIndent(s, "", "  ")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if os.WriteFile(tmp, append(data, '\n'), 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

func resultOf(id, label string, k, n int) summaryResult {
	p, lo, hi := wilson(k, n)
	return summaryResult{ID: id, Label: label, Value: round1(100 * p), Low: round1(100 * lo), High: round1(100 * hi), Games: n}
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// arenasum: add up the first -seats entry's wins over several cmd/arena
// -json reports (chunks of one matchup) and print or record the total.
func cmdArenaSum(args []string) {
	fs := flag.NewFlagSet("arenasum", flag.ExitOnError)
	id := fs.String("id", "", "result id for the summary file")
	label := fs.String("label", "", "result label")
	summary := fs.String("summary", "", "grandmaster.json to merge the result into")
	fs.Parse(args)
	type rep struct {
		Games  int `json:"games"`
		Errors int `json:"errors"`
		Seats  []struct {
			Level string `json:"level"`
			Wins  int    `json:"wins"`
		} `json:"seats"`
	}
	wins, games := 0, 0
	var levels []string
	for _, path := range fs.Args() {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		var r rep
		if json.Unmarshal(data, &r) != nil || len(r.Seats) == 0 {
			fmt.Fprintln(os.Stderr, path+": not an arena report")
			continue
		}
		wins += r.Seats[0].Wins
		games += r.Games - r.Errors
		if levels == nil {
			for _, s := range r.Seats {
				levels = append(levels, s.Level)
			}
		}
	}
	p, lo, hi := wilson(wins, games)
	fmt.Printf("%s: %s wins %d of %d games, %s\n", strings.Join(levels, ","), firstOr(levels), wins, games, pct(p, lo, hi))
	if *id != "" {
		mergeSummary(*summary, resultOf(*id, *label, wins, games))
	}
}

func firstOr(list []string) string {
	if len(list) == 0 {
		return "?"
	}
	return list[0]
}
