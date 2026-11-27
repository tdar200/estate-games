// Command poker-fixtures writes web/fixtures/poker/poker-*.json: real
// PublicState snapshots of a poker table in every phase, from the table
// screen's and a phone's point of view, for building and testing the UI
// without a server.
//
//	go run ./cmd/poker-fixtures            # writes ./web/fixtures/poker
//	go run ./cmd/poker-fixtures -out DIR
//
// The states come from the real engine driven through scripted hands, so
// they are always consistent. The fields Estate's server adds to every poker
// state (players[].input/tokenKind/online/difficulty/autopilot, gameType,
// aiLevels, roomSettings, localSeats; internal/server/poker.go
// pokerStateFor) are added here the same way and in the same key order.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tdar200/estate-games/internal/poker/holdem"
)

// fixture is one file, in the Estate fixture format.
type fixture struct {
	State  json.RawMessage `json:"state"`
	Viewer fixtureViewer   `json:"viewer"`
	Events []holdem.Event  `json:"events"` // this hand's events so far, for animation work
	Meta   fixtureMeta     `json:"meta"`
}

type fixtureViewer struct {
	Role     string `json:"role"`
	PlayerID string `json:"playerId,omitempty"`
	Name     string `json:"name,omitempty"`
}

type fixtureMeta struct {
	Description string `json:"description"`
	Seed        int    `json:"seed"`
	Hand        int    `json:"hand"`
}

type indexEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Viewer      string `json:"viewer"`
}

// playerExtras mirrors what Estate's server appends to each poker player.
type playerExtras struct {
	Input      string `json:"input"`
	TokenKind  string `json:"tokenKind"`
	Online     bool   `json:"online"`
	Difficulty string `json:"difficulty,omitempty"`
	Autopilot  bool   `json:"autopilot,omitempty"`
}

// pokerLevels is what the server offers for poker computer seats.
var pokerLevels = []string{"easy", "normal"}

var cast = []struct {
	name, kind, token, difficulty string
}{
	{"Maya", holdem.KindHuman, "rocket", ""},
	{"Sam", holdem.KindHuman, "teapot", ""},
	{"Redwood", holdem.KindAI, "lighthouse", "normal"},
	{"Priya", holdem.KindHuman, "bicycle", ""},
	{"Skylark", holdem.KindAI, "key", "easy"},
	{"Jonah", holdem.KindHuman, "umbrella", ""},
	{"Lena", holdem.KindHuman, "anchor", ""},
	{"North Star", holdem.KindAI, "kite", "normal"},
}

type scene struct {
	tb      *holdem.Table
	clock   *int64
	seed    int
	ids     map[string]string // name → id
	names   map[string]string // id → name
	offline map[string]bool
	away    map[string]bool // autopilot on
}

func newScene(seed int, players int, settings string) *scene {
	return newSceneOf(seed, castNames(players), settings)
}

func castNames(n int) []string {
	var out []string
	for _, c := range cast[:n] {
		out = append(out, c.name)
	}
	return out
}

func newSceneOf(seed int, names []string, settings string) *scene {
	var s [32]byte
	s[0], s[1] = byte(seed), byte(seed>>8)
	now := int64(1_762_160_400_000) // 2025-11-03 09:00 UTC, fixed
	sc := &scene{clock: &now, seed: seed, ids: map[string]string{}, names: map[string]string{}, offline: map[string]bool{}, away: map[string]bool{}}
	tm := holdem.DefaultTiming()
	sc.tb = holdem.NewTable("KQ7TD", holdem.Options{Seed: &s, Clock: func() int64 { return *sc.clock }, Timing: &tm})
	for _, name := range names {
		kind := holdem.KindHuman
		for _, c := range cast {
			if c.name == name {
				kind = c.kind
			}
		}
		id, err := sc.tb.AddPlayer(name, kind)
		check(err)
		sc.ids[name] = id
		sc.names[id] = name
	}
	if settings != "" {
		check(sc.tb.PerformAction(sc.ids[names[0]], json.RawMessage(`{"type":"settings","settings":`+settings+`}`)))
	}
	return sc
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func (sc *scene) start() {
	check(sc.tb.PerformAction(sc.tb.HostID, json.RawMessage(`{"type":"start"}`)))
}

func (sc *scene) do(name, js string) {
	check(sc.tb.PerformAction(sc.ids[name], json.RawMessage(js)))
}

func (sc *scene) tick(ms int64) {
	*sc.clock += ms
	sc.tb.Advance(*sc.clock)
}

// actor returns the name of the player to act, or "".
func (sc *scene) actor() string {
	for _, w := range sc.tb.Waiting() {
		if w.Kind == "act" {
			return sc.names[w.PlayerID]
		}
	}
	return ""
}

func (sc *scene) view(name string) *holdem.View {
	return sc.tb.View(sc.ids[name], holdem.Viewer{Role: "player", Name: name, IsHost: sc.ids[name] == sc.tb.HostID})
}

func (sc *scene) street() string {
	return sc.tb.View("", holdem.Viewer{}).Street
}

// play runs the hand with a simple script until stop() is true. policy
// returns the action for a player.
func (sc *scene) play(policy func(name string, v *holdem.View) string, stop func() bool) bool {
	for guard := 0; guard < 400; guard++ {
		if stop() {
			return true
		}
		if sc.tb.Status != holdem.StatusPlaying {
			return false
		}
		name := sc.actor()
		if name == "" {
			next := sc.tb.NextDeadline()
			if next == 0 {
				return stop()
			}
			sc.tick(next - *sc.clock)
			continue
		}
		*sc.clock += 2300 // people take a moment
		sc.do(name, policy(name, sc.view(name)))
	}
	return false
}

func checkOrCall(v *holdem.View) string {
	if v.You.Legal.Check {
		return `{"type":"check"}`
	}
	return `{"type":"call"}`
}

func checkOrFold(v *holdem.View) string {
	if v.You.Legal.Check {
		return `{"type":"check"}`
	}
	return `{"type":"fold"}`
}

func betHalfPot(v *holdem.View) string {
	r := v.You.Legal.Raise
	if r == nil {
		return checkOrCall(v)
	}
	to := max(r.Min, min(r.Max, v.CurrentBet+v.TotalPot/2))
	return fmt.Sprintf(`{"type":"raise","to":%d}`, to)
}

// render builds the state for a viewer ("" = table screen) with the server's
// decorations, in the server's key order.
func (sc *scene) render(viewer string) (json.RawMessage, fixtureViewer) {
	var v *holdem.View
	fv := fixtureViewer{Role: "table"}
	if viewer == "" {
		v = sc.tb.View("", holdem.Viewer{Role: "table", IsHost: true})
	} else {
		v = sc.view(viewer)
		fv = fixtureViewer{Role: "player", PlayerID: sc.ids[viewer], Name: viewer}
	}
	raw, err := json.Marshal(v)
	check(err)
	fields := splitObject(raw)
	for i := range fields {
		if fields[i].key != "players" {
			continue
		}
		var buf bytes.Buffer
		buf.WriteByte('[')
		for j, p := range v.Players {
			if j > 0 {
				buf.WriteByte(',')
			}
			pj, err := json.Marshal(p)
			check(err)
			extra := playerExtras{Input: "phone", Online: !sc.offline[p.ID], Autopilot: sc.away[p.ID]}
			for _, c := range cast {
				if c.name == p.Name {
					extra.TokenKind = c.token
					if c.kind == holdem.KindAI {
						extra.Input, extra.Difficulty = "ai", c.difficulty
					}
				}
			}
			ej, err := json.Marshal(extra)
			check(err)
			buf.Write(pj[:len(pj)-1])
			buf.WriteByte(',')
			buf.Write(ej[1:])
		}
		buf.WriteByte(']')
		fields[i].value = buf.Bytes()
	}
	add := func(key string, value any) {
		data, err := json.Marshal(value)
		check(err)
		fields = append(fields, field{key, data})
	}
	add("gameType", "poker")
	add("aiLevels", pokerLevels)
	add("roomSettings", map[string]any{"computerSpeed": "normal", "listed": false})
	if viewer == "" {
		add("localSeats", []any{})
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			out.WriteByte(',')
		}
		k, _ := json.Marshal(f.key)
		out.Write(k)
		out.WriteByte(':')
		out.Write(f.value)
	}
	out.WriteByte('}')
	return out.Bytes(), fv
}

type field struct {
	key   string
	value json.RawMessage
}

// splitObject parses a JSON object into ordered key/value pairs.
func splitObject(data []byte) []field {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		panic(err)
	}
	var out []field
	for dec.More() {
		tok, err := dec.Token()
		check(err)
		var value json.RawMessage
		check(dec.Decode(&value))
		out = append(out, field{tok.(string), value})
	}
	return out
}

type output struct {
	dir   string
	index []indexEntry
	log   io.Writer
}

func (o *output) write(name, description string, sc *scene, viewer string) {
	state, fv := sc.render(viewer)
	var events []holdem.Event
	for _, e := range sc.tb.EventsSince(0) {
		if e.HandNumber == sc.tb.HandNumber || sc.tb.HandNumber == 0 {
			events = append(events, e)
		}
	}
	if events == nil {
		events = []holdem.Event{}
	}
	f := fixture{State: state, Viewer: fv, Events: events, Meta: fixtureMeta{Description: description, Seed: sc.seed, Hand: sc.tb.HandNumber}}
	data, err := json.MarshalIndent(f, "", "  ")
	check(err)
	leakCheck(name, sc, viewer, data)
	check(os.WriteFile(filepath.Join(o.dir, name+".json"), append(data, '\n'), 0o644))
	who := "table"
	if viewer != "" {
		who = viewer
	}
	o.index = append(o.index, indexEntry{Name: name, Description: description, Viewer: who})
	fmt.Fprintf(o.log, "%-34s %s\n", name, description)
}

// pair writes a scene's table and phone fixtures.
func (o *output) pair(name, what string, sc *scene, phone, phoneWhat string) {
	o.write(name+"-table", what, sc, "")
	o.write(name+"-phone", phoneWhat, sc, phone)
}

// leakCheck fails if a fixture shows hole cards its viewer may not see, or
// a hand strength to anyone but its owner.
func leakCheck(name string, sc *scene, viewer string, data []byte) {
	if viewer == "" && bytes.Contains(data, []byte(`"strength"`)) {
		panic(name + ": the table screen got a hand strength")
	}
	if n := bytes.Count(data, []byte(`"strength"`)); n > 1 {
		panic(fmt.Sprintf("%s: %d strength fields", name, n))
	}
	h := sc.tb.Hand
	if h == nil || sc.tb.Status == holdem.StatusLobby {
		return
	}
	board := map[string]bool{}
	for _, c := range h.Board {
		board[holdem.CardString(c)] = true
	}
	for i, id := range sc.tb.HandIDs {
		if h.Shown[i] || (viewer != "" && id == sc.ids[viewer]) {
			continue
		}
		for _, c := range h.Hole[i] {
			cs := holdem.CardString(c)
			if !board[cs] && bytes.Contains(data, []byte(`"`+cs+`"`)) {
				panic(fmt.Sprintf("%s: hidden card %s of %s leaks", name, cs, sc.names[id]))
			}
		}
	}
}

func main() {
	out := flag.String("out", filepath.Join("web", "fixtures", "poker"), "output directory")
	flag.Parse()
	n := generate(*out, os.Stdout)
	fmt.Printf("\n%d fixtures written to %s\n", n, strings.TrimSpace(*out))
}

// generate writes every fixture and index.json into dir.
func generate(dir string, log io.Writer) int {
	check(os.MkdirAll(dir, 0o755))
	o := &output{dir: dir, log: log}

	// Lobby: five seated, host settings visible.
	{
		sc := newScene(1, 5, `{"rebuys":1,"actionSeconds":30,"targetMinutes":60,"fourColorDeck":true}`)
		sc.offline[sc.ids["Priya"]] = true
		o.write("poker-lobby-table", "Lobby with five seats (two computer players), four-colour deck on; Priya's phone is offline", sc, "")
		o.write("poker-lobby-phone", "Lobby on the host's phone (Maya): settings and Start", sc, "Maya")
	}

	// Preflop, flop, turn and river: Maya, Sam and Redwood play on, the
	// others fold. Each street stops when Maya faces a bet.
	streetScene := func(street string) *scene {
		for seed := 1; seed < 500; seed++ {
			sc := newScene(seed, 6, "")
			sc.start()
			raised := false
			policy := func(name string, v *holdem.View) string {
				live := name == "Maya" || name == "Sam" || name == "Redwood"
				if !live {
					return checkOrFold(v)
				}
				if name != "Maya" && v.CurrentBet == 0 || (v.Street == "preflop" && !raised && name != "Maya") {
					raised = raised || v.Street == "preflop"
					return betHalfPot(v)
				}
				return checkOrCall(v)
			}
			ok := sc.play(policy, func() bool {
				v := sc.tb.View("", holdem.Viewer{})
				return sc.tb.Phase == holdem.PhaseBetting && v.Street == street && sc.actor() == "Maya" && v.CurrentBet > 0
			})
			if ok {
				return sc
			}
		}
		panic("no seed reaches " + street)
	}
	for _, street := range []string{"preflop", "flop", "turn", "river"} {
		sc := streetScene(street)
		what := map[string]string{
			"preflop": "Preflop: a raise in front of Maya, who is to act (20-second turn timer running)",
			"flop":    "Flop: Sam bets, Maya to act; three players in the hand",
			"turn":    "Turn: Maya faces a bet with three players in",
			"river":   "River: Maya faces a bet; last decision of the hand",
		}[street]
		o.pair("poker-"+street, what, sc, "Maya", what+" (Maya's phone with her options and hand strength)")
	}

	// Time bank: on the flop Maya lets her 20-second timer run out; her
	// 30-second bank is running.
	{
		sc := streetScene("flop")
		sc.tick(sc.tb.Deadline - *sc.clock) // the timer runs out
		sc.tick(6_000)                      // six seconds into the bank
		if !sc.tb.OnBank {
			panic("time bank scene: the bank is not running")
		}
		o.pair("poker-timebank", "Flop: Maya's turn timer ran out and her time bank is running (actionTimeBank), facing Sam's bet", sc,
			"Maya", "Maya's phone on her time bank, facing a bet")
	}

	// Eight seats on the flop: Sam bets, Maya is still to act after someone
	// else (her phone shows pre-action buttons).
	{
		var found *scene
		for seed := 1; seed < 800 && found == nil; seed++ {
			sc := newScene(seed, 8, "")
			sc.start()
			out := map[string]bool{"Jonah": true, "Lena": true, "North Star": true}
			bet := false
			policy := func(name string, v *holdem.View) string {
				if out[name] {
					return checkOrFold(v)
				}
				if v.Street == "flop" && name == "Sam" && !bet {
					bet = true
					return betHalfPot(v)
				}
				return checkOrCall(v)
			}
			ok := sc.play(policy, func() bool {
				if sc.tb.HandNumber != 1 || sc.tb.Phase != holdem.PhaseBetting || sc.street() != "flop" || !bet {
					return false
				}
				a := sc.actor()
				if a == "" || a == "Maya" || a == "Sam" {
					return false
				}
				v := sc.view("Maya")
				if v.CurrentBet == 0 {
					return false
				}
				me := v.Players[0]
				for _, p := range v.Players {
					if p.ID == sc.ids["Maya"] {
						me = p
					}
				}
				return me.HasCards && (me.LastAction == nil || me.LastAction.Street != "flop")
			})
			if ok {
				found = sc
			}
		}
		if found == nil {
			panic("no seed for the eight-seat flop")
		}
		sc := found
		sc.offline[sc.ids["Lena"]] = true
		o.pair("poker-flop8", "Eight seats on the flop: Sam bet, "+sc.actor()+" is to act, Maya acts later; Lena's phone is offline", sc,
			"Maya", "Maya's phone while someone else is to act: she can pre-select check/fold, call or call any")
	}

	// All-in run-out and showdown with side pots: three uneven stacks all-in
	// preflop. Choose a deal where the main pot and the side pot have
	// different winners.
	sidePotScene := func(rebuys bool) *scene {
		settings := `{"blindSchedule":"off"}`
		if rebuys {
			settings = `{"blindSchedule":"off","rebuys":1}`
		}
		for seed := 1; seed < 2000; seed++ {
			sc := newScene(seed, 3, settings)
			sc.start()
			stacks := map[string]int{"Maya": 1500, "Sam": 600, "Redwood": 2400}
			// Uneven stacks for this hand (fixture set-up only).
			h := sc.tb.Hand
			for i, id := range sc.tb.HandIDs {
				diff := stacks[sc.names[id]] - 1500
				h.Stacks[i] += diff
				h.StartStacks[i] += diff
			}
			ok := sc.play(func(name string, v *holdem.View) string { return `{"type":"allin"}` },
				func() bool { return sc.tb.Phase == holdem.PhaseRunout })
			if !ok {
				continue
			}
			// Check the outcome on a copy.
			snap, _ := sc.tb.Snapshot()
			cpNow := *sc.clock
			cp, _ := holdem.Restore(snap, holdem.Options{Clock: func() int64 { return cpNow }})
			for guard := 0; cp.Phase == holdem.PhaseRunout && guard < 10; guard++ {
				cpNow = cp.NextDeadline()
				cp.Advance(cpNow)
			}
			res := cp.Hand.Result
			if res == nil || len(res.Pots) < 2 || len(res.Pots[0].Winners) != 1 || len(res.Pots[1].Winners) != 1 {
				continue
			}
			samLost := res.Won[indexOf(cp.HandIDs, sc.ids["Sam"])] == 0
			if rebuys && !samLost {
				continue // the busted fixture needs Sam to lose
			}
			if !rebuys && res.Pots[0].Winners[0] == res.Pots[1].Winners[0] {
				continue // the showdown fixture wants two different winners
			}
			return sc
		}
		panic("no side-pot seed")
	}
	{
		sc := sidePotScene(false)
		sc.tick(1800) // the flop is turned over
		o.write("poker-runout-table", "All-in run-out: three players all-in preflop, hands face up with win chances, the flop is out", sc, "")
		o.write("poker-runout-phone", "All-in run-out on Sam's phone (the short stack)", sc, "Sam")
		for guard := 0; sc.tb.Phase == holdem.PhaseRunout && guard < 10; guard++ {
			sc.tick(1800)
		}
		o.write("poker-showdown-table", "Showdown with a main pot and a side pot won by different players", sc, "")
		o.write("poker-showdown-phone", "Showdown on Maya's phone, with her hand result", sc, "Maya")
	}
	{
		sc := sidePotScene(true)
		for guard := 0; sc.tb.Phase == holdem.PhaseRunout && guard < 10; guard++ {
			sc.tick(1800)
		}
		var busted []string
		for _, p := range sc.tb.Players {
			if p.Busted {
				busted = append(busted, p.Name)
			}
		}
		o.write("poker-busted-table", strings.Join(busted, " and ")+" lost all their chips and may rebuy (30-second offer)", sc, "")
		o.write("poker-busted-phone", "Sam's phone: out of chips, with the Rebuy option", sc, "Sam")
	}

	// Hand over without a showdown: a raise and everyone folds.
	{
		sc := newScene(3, 6, "")
		sc.start()
		first := ""
		sc.play(func(name string, v *holdem.View) string {
			if first == "" {
				first = name
				return betHalfPot(v)
			}
			return `{"type":"fold"}`
		}, func() bool { return sc.tb.Phase == holdem.PhaseHandOver })
		o.write("poker-handover-table", first+" raised and everyone folded: the pot is won without a showdown", sc, "")
		o.write("poker-handover-phone", "Hand over on the winner's phone ("+first+"), who may show their cards", sc, first)
	}

	// Maya wins uncontested: she raises, everyone folds; her phone offers Show.
	{
		sc := newScene(5, 6, "")
		sc.start()
		sc.play(func(name string, v *holdem.View) string {
			if name == "Maya" {
				return betHalfPot(v)
			}
			return checkOrFold(v)
		}, func() bool { return sc.tb.Phase == holdem.PhaseHandOver })
		if !sc.view("Maya").You.CanShow {
			panic("uncontested scene: Maya cannot show")
		}
		o.pair("poker-uncontested", "Maya raised and everyone folded: she wins without a showdown, her cards face down", sc,
			"Maya", "Maya's phone after winning uncontested: Show or keep her cards hidden (you.canShow)")
	}

	// Sitting out: Maya sat out during hand 1; hand 2 is dealt without her.
	{
		sc := newScene(6, 6, "")
		sc.start()
		sc.do("Maya", `{"type":"sitOut"}`)
		sc.play(func(_ string, v *holdem.View) string { return checkOrFold(v) }, func() bool { return sc.tb.HandNumber == 2 && sc.tb.Phase == holdem.PhaseBetting })
		if !sc.view("Maya").You.SittingOut {
			panic("sit-out scene: Maya is not sitting out")
		}
		o.pair("poker-sitout", "Hand 2 dealt without Maya, who is sitting out", sc,
			"Maya", "Maya's phone while she sits out: I'm back (sitIn)")
	}

	// Heads-up: two players; the button posts the small blind and acts first
	// before the flop.
	{
		sc := newScene(7, 2, "")
		sc.start()
		if sc.actor() != "Maya" {
			sc.do(sc.actor(), `{"type":"call"}`) // Sam completes; Maya (big blind) to act
		}
		o.pair("poker-headsup", "Heads-up: Maya and Sam, the button posts the small blind and acts first preflop", sc,
			"Maya", "Heads-up on Maya's phone, her turn")
	}

	// Cash game lobby.
	{
		sc := newScene(8, 4, `{"style":"cash","startingStack":2000,"smallBlind":10,"bigBlind":20}`)
		o.pair("poker-cash-lobby", "Cash-game lobby: fixed blinds 10/20, buy-in 2,000, rebuy any time", sc,
			"Maya", "Cash-game lobby on the host's phone (Maya)")
	}

	// Cash game mid-hand: Jonah busted and rebought earlier, Sam is busted
	// now and may rebuy whenever he likes (no deadline).
	{
		duel := func(sc *scene, a, b string) {
			sc.play(func(name string, v *holdem.View) string {
				if name == a || name == b {
					return `{"type":"allin"}`
				}
				return checkOrFold(v)
			}, func() bool { return sc.tb.Phase == holdem.PhaseShowdown || sc.tb.Phase == holdem.PhaseHandOver })
		}
		var found *scene
		for seed := 1; seed < 400 && found == nil; seed++ {
			sc := newSceneOf(seed, []string{"Maya", "Sam", "Redwood", "Priya", "Jonah"}, `{"style":"cash","startingStack":1000}`)
			sc.start()
			duel(sc, "Jonah", "Maya")
			if !sc.tb.Player(sc.ids["Jonah"]).Busted {
				continue
			}
			sc.do("Jonah", `{"type":"rebuy"}`)
			sc.tick(sc.tb.NextHandAt - *sc.clock)
			duel(sc, "Sam", "Redwood")
			if !sc.tb.Player(sc.ids["Sam"]).Busted {
				continue
			}
			sc.tick(sc.tb.NextHandAt - *sc.clock)
			if sc.tb.Phase != holdem.PhaseBetting {
				continue
			}
			found = sc
		}
		if found == nil {
			panic("no seed for the cash game")
		}
		o.pair("poker-cash-busted", "Cash game mid-hand: Sam is out of chips and sits out until he rebuys (no deadline); Jonah has rebought once", found,
			"Sam", "Sam's phone: busted in a cash game, Rebuy whenever he likes")
	}

	// Game over: three players, all-in every hand until one has every chip.
	{
		sc := newScene(4, 3, `{"blindSchedule":"off"}`)
		sc.start()
		sc.play(func(name string, v *holdem.View) string { return `{"type":"allin"}` },
			func() bool { return sc.tb.Status == holdem.StatusFinished })
		winner := sc.names[sc.tb.WinnerID]
		o.write("poker-gameover-table", winner+" has every chip: final standings", sc, "")
		loser := "Maya"
		if winner == "Maya" {
			loser = "Sam"
		}
		o.write("poker-gameover-phone", "Game over on "+loser+"'s phone (not the winner)", sc, loser)
	}

	data, err := json.MarshalIndent(o.index, "", "  ")
	check(err)
	check(os.WriteFile(filepath.Join(dir, "index.json"), append(data, '\n'), 0o644))
	return len(o.index)
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}
