package holdem

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The PokerKit cross-check replays thousands of random seeded hands in
// PokerKit (tools/pokerkit_check.py) on a machine you reach over ssh and compares
// every decision's legal options, the final stacks and the pot boundaries.
//
//	POKER_CROSSCHECK_SSH     ssh host with PokerKit installed (unset or "off" skips)
//	POKER_CROSSCHECK_PYTHON  remote interpreter (default ~/poker/.venv/bin/python)
//	POKER_CROSSCHECK_HANDS   number of hands (default 3000)
//	POKER_CROSSCHECK_SEED    first seed (default 1)
//
// It is skipped with -short, when POKER_CROSSCHECK_SSH is not set, or when the host is unreachable.

type xcStep struct {
	Actor int    `json:"actor"`
	Call  int    `json:"call"`
	Fold  bool   `json:"fold"`
	Raise bool   `json:"raise"`
	Min   int    `json:"min"`
	Max   int    `json:"max"`
	Act   string `json:"act"`
	To    int    `json:"to,omitempty"`
}

type xcHand struct {
	ID     int      `json:"id"`
	SB     int      `json:"sb"`
	BB     int      `json:"bb"`
	Ante   int      `json:"ante"`
	Stacks []int    `json:"stacks"`
	Holes  []string `json:"holes"`
	Board  string   `json:"board"`
	Steps  []xcStep `json:"steps"`
	Final  []int    `json:"final"`
	Pots   []int    `json:"pots"`
	Show   bool     `json:"showdown"`
}

// randomConfig draws a table with uneven stacks so side pots, short blinds,
// short antes and short all-in raises are common.
func randomConfig(rng *rand.Rand) HandConfig {
	n := 2 + rng.IntN(9)
	if rng.IntN(4) == 0 {
		n = 2
	}
	bbs := []int{2, 10, 20, 50, 100}
	bb := bbs[rng.IntN(len(bbs))]
	sb := bb / 2
	switch rng.IntN(6) {
	case 0:
		sb = 1 + rng.IntN(bb)
	case 1:
		sb = bb
	}
	ante := 0
	if rng.IntN(3) == 0 {
		ante = 1 + rng.IntN(max(1, bb/2))
	}
	stacks := make([]int, n)
	for i := range stacks {
		switch r := rng.IntN(20); {
		case r < 3:
			stacks[i] = 1 + rng.IntN(bb+ante)
		case r < 8:
			stacks[i] = bb + rng.IntN(10*bb)
		case r < 16:
			stacks[i] = 10*bb + rng.IntN(90*bb)
		default:
			stacks[i] = 100*bb + rng.IntN(200*bb)
		}
	}
	return HandConfig{SmallBlind: sb, BigBlind: bb, Ante: ante, Stacks: stacks, Deck: ShuffledDeck(rng)}
}

// randomAction picks a legal action, biased towards raises and all-ins.
func randomAction(rng *rand.Rand, l Legal) Action {
	r := rng.Float64()
	switch {
	case l.CanFold && r < 0.18:
		return Action{Type: ActFold}
	case l.CanRaise && r < 0.50:
		to := l.MinTo
		switch rng.IntN(10) {
		case 0, 1, 2:
			to = l.MaxTo
		case 3, 4, 5, 6:
			to = l.MinTo + rng.IntN(l.MaxTo-l.MinTo+1)
		}
		t := ActRaise
		if l.IsBet {
			t = ActBet
		}
		return Action{Type: t, To: to}
	case l.CanCheck:
		return Action{Type: ActCheck}
	default:
		return Action{Type: ActCall}
	}
}

// playRandomHand plays one hand to the end, recording it for PokerKit.
func playRandomHand(t testing.TB, id int, rng *rand.Rand) (*Hand, xcHand) {
	cfg := randomConfig(rng)
	h, err := NewHand(cfg)
	if err != nil {
		t.Fatalf("hand %d: %v", id, err)
	}
	rec := xcHand{ID: id, SB: cfg.SmallBlind, BB: cfg.BigBlind, Ante: cfg.Ante, Stacks: cfg.Stacks, Steps: []xcStep{}}
	for !h.Done {
		l := h.Legal()
		if l.Player < 0 {
			t.Fatalf("hand %d: no actor but hand not done", id)
		}
		a := randomAction(rng, l)
		step := xcStep{Actor: l.Player, Call: l.CallAmount, Fold: l.CanFold, Raise: l.CanRaise, Min: l.MinTo, Max: l.MaxTo, Act: string(a.Type), To: a.To}
		if err := h.Act(a); err != nil {
			t.Fatalf("hand %d: legal action %+v rejected: %v", id, a, err)
		}
		rec.Steps = append(rec.Steps, step)
		checkInvariants(t, h, cfg)
	}
	for _, hc := range h.Hole {
		rec.Holes = append(rec.Holes, CardsString(hc))
	}
	rec.Board = CardsString(h.Board)
	rec.Final = append([]int(nil), h.Stacks...)
	rec.Show = h.Result.Showdown
	for _, p := range h.Result.Pots {
		rec.Pots = append(rec.Pots, p.Amount)
	}
	return h, rec
}

// checkInvariants: chips are conserved and nobody goes negative.
func checkInvariants(t testing.TB, h *Hand, cfg HandConfig) {
	t.Helper()
	total, start := 0, 0
	for i := 0; i < h.N; i++ {
		if h.Stacks[i] < 0 || h.Bets[i] < 0 || h.Contrib[i] < 0 {
			t.Fatalf("negative chips: stacks %v bets %v contrib %v", h.Stacks, h.Bets, h.Contrib)
		}
		start += cfg.Stacks[i]
		total += h.Stacks[i]
		if !h.Done {
			total += h.Contrib[i]
		}
	}
	if total != start {
		t.Fatalf("chips not conserved: %d != %d (stacks %v contrib %v)", total, start, h.Stacks, h.Contrib)
	}
	if h.Done {
		potSum := 0
		for _, p := range h.Result.Pots {
			potSum += p.Amount
			s := 0
			for _, x := range p.Shares {
				s += x
			}
			if s != p.Amount {
				t.Fatalf("pot %+v shares do not add up", p)
			}
		}
		contrib := 0
		for _, c := range h.Contrib {
			contrib += c
		}
		if potSum != contrib {
			t.Fatalf("pots %d != contributions %d", potSum, contrib)
		}
	}
}

// TestRandomHandsInvariants runs without PokerKit: legality and chip
// conservation over many random hands.
func TestRandomHandsInvariants(t *testing.T) {
	rng := rand.New(rand.NewPCG(99, 7))
	n := 20000
	if testing.Short() {
		n = 2000
	}
	for i := 0; i < n; i++ {
		playRandomHand(t, i, rng)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestPokerKitCrossCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("PokerKit cross-check skipped with -short")
	}
	host := envOr("POKER_CROSSCHECK_SSH", "off")
	if host == "off" {
		t.Skip("set POKER_CROSSCHECK_SSH to an ssh host with PokerKit to run the cross-check")
	}
	python := envOr("POKER_CROSSCHECK_PYTHON", "~/poker/.venv/bin/python")
	count, _ := strconv.Atoi(envOr("POKER_CROSSCHECK_HANDS", "3000"))
	seed, _ := strconv.ParseUint(envOr("POKER_CROSSCHECK_SEED", "1"), 10, 64)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	probe := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", host,
		python+" -c 'import pokerkit'")
	if out, err := probe.CombinedOutput(); err != nil {
		cancel()
		t.Skipf("PokerKit host %q unreachable or PokerKit missing: %v %s", host, err, strings.TrimSpace(string(out)))
	}
	cancel()

	script, err := os.ReadFile("../../../tools/pokerkit_check.py")
	if err != nil {
		t.Fatal(err)
	}
	var input bytes.Buffer
	enc := json.NewEncoder(&input)
	rng := rand.New(rand.NewPCG(seed, 0x5eed))
	hands := map[int]xcHand{}
	for i := 0; i < count; i++ {
		_, rec := playRandomHand(t, i, rng)
		hands[i] = rec
		if err := enc.Encode(rec); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	// The script travels base64-encoded as the first stdin line (nothing is
	// written on the remote machine); the hands follow it.
	b64 := base64.StdEncoding.EncodeToString(script)
	stdin := io.MultiReader(strings.NewReader(b64+"\n"), &input)
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", host,
		fmt.Sprintf("nice -n 10 %s -I -c 'import base64,sys;exec(base64.b64decode(sys.stdin.readline()))'", python))
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pokerkit run failed: %v\n%s", err, stderr.String())
	}
	var summary map[string]int
	failures := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var line struct {
			Summary map[string]int `json:"summary"`
			ID      int            `json:"id"`
			Errors  []string       `json:"errors"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("bad output line %q: %v", sc.Text(), err)
		}
		if line.Summary != nil {
			summary = line.Summary
			continue
		}
		failures++
		if failures <= 10 {
			h := hands[line.ID]
			js, _ := json.Marshal(h)
			t.Errorf("hand %d mismatches PokerKit: %v\n  hand: %s", line.ID, line.Errors, js)
		}
	}
	if summary == nil {
		t.Fatalf("no summary from PokerKit\n%s", stderr.String())
	}
	t.Logf("PokerKit cross-check: %d hands, %d decisions, %d ok, %d failed, %d split pots re-divided by the odd-chip rule",
		summary["hands"], summary["decisions"], summary["ok"], summary["failed"], summary["oddchip_adjusted"])
	if summary["hands"] != count {
		t.Errorf("PokerKit saw %d hands, sent %d", summary["hands"], count)
	}
}
