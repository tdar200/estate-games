package closest

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"slices"
	"sort"
	"strings"
	"time"
)

// Statuses.
const (
	StatusLobby    = "lobby"
	StatusPlaying  = "playing"
	StatusFinished = "finished"
)

// Phases (docs/CLOSEST-API.md §4).
const (
	PhaseLobby    = "lobby"
	PhaseIntro    = "intro"    // the title, the length, "the last question pays double"
	PhaseGuess    = "guess"    // everyone types a guess
	PhaseLineup   = "lineup"   // the guesses come in as slots, low to high
	PhaseBet      = "bet"      // everyone places two chips (and maybe a stake) in secret
	PhaseChips    = "chips"    // every chip lands at once
	PhaseReveal   = "reveal"   // the answer, the wins and the payouts
	PhaseFinished = "finished" // standings
)

// Player kinds.
const (
	KindHuman = "human"
	KindAI    = "ai"
)

// TableHost is the requester the server uses when the table screen (the host
// of its room, with no seat of its own) sends a host command while no person
// is seated, so nobody holds HostID (as in the quiz).
const TableHost = "@table"

// Event types (Event.Type, docs/CLOSEST-API.md §5).
const (
	EvJoin     = "join"
	EvLeave    = "leave"
	EvStart    = "start"
	EvQuestion = "question" // question Q (number No) opens for guesses
	EvGuessed  = "guessed"  // PlayerID has guessed (the value stays secret)
	EvLineup   = "lineup"   // the guesses are on the line: Slots slots
	EvBetIn    = "betIn"    // PlayerID has bet (the chips stay secret)
	EvChips    = "chips"    // every chip is shown
	EvReveal   = "reveal"   // the answer; Guesses and Bets are every guess and bet, for records
	EvGameOver = "gameOver" // WinnerIDs won
)

// Timers (docs/CLOSEST-API.md §4), in milliseconds.
const (
	introMs          = 4000
	hurryMs          = 10000 // the timer drops to this when one player is left to act
	lineupBaseMs     = 1500
	lineupPerSlotMs  = 600
	lineupMinMs      = 3000
	lineupMaxMs      = 6500
	chipsMs          = 3500
	revealMs         = 8000
	revealLastMs     = 9000
	computerMarginMs = 2000 // a computer acts at least this long before its phase's deadline
)

const (
	maxLog    = 80
	maxEvents = 300
	maxName   = 24
	maxSteps  = 1000 // most timed steps one Advance runs (a guard)
)

// Player is a seat.
type Player struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Level string `json:"level,omitempty"` // a computer's level
	Score int    `json:"score"`           // never below 0
	// Closest counts the questions where they owned a winning slot: the
	// tie-break on places.
	Closest int  `json:"closest"`
	Place   int  `json:"place"` // set when the game ends
	Left    bool `json:"left"`
	// Online is whether their phone is connected (the server says);
	// computers are always online. A player who is away when the bets close
	// gets no chips placed for them.
	Online bool `json:"online"`
	// Plan is a computer's next move in the current phase (never shown).
	Plan *Plan `json:"plan,omitempty"`
}

func (p *Player) computer() bool { return p.Kind == KindAI }

// Plan is a computer's next move: due At; for a guess, the value (drawn when
// the question opens).
type Plan struct {
	At    int64 `json:"at"`
	Value int   `json:"value,omitempty"`
}

// Guess is a player's guess on the current question: the latest value, and
// how long after the question opened it came. The list keeps guess order:
// a changed guess moves to the end.
type Guess struct {
	PlayerID string `json:"playerId"`
	Value    int    `json:"value"`
	AfterMs  int64  `json:"afterMs"`
	Changed  bool   `json:"changed"`
}

// Bet is a player's two chips (slot indexes, maybe the same) and their stake
// on one of them. Auto: placed by the bet timer (both chips on their own
// slot).
type Bet struct {
	PlayerID  string `json:"playerId"`
	Chips     [2]int `json:"chips"`
	Stake     int    `json:"stake"`
	StakeSlot *int   `json:"stakeSlot"` // null when the stake is 0
	Auto      bool   `json:"auto"`
}

// Current is the question being played. Question holds the answer, so it
// travels only in the snapshot.
type Current struct {
	Seq      int       `json:"seq"`
	No       int       `json:"no"`
	Double   bool      `json:"double"`
	Question *Question `json:"question"`
	OpenedAt int64     `json:"openedAt"`
	// Playing is everyone still playing when it opened (results list them
	// all, including anyone who leaves during it).
	Playing  []string `json:"playing"`
	Guesses  []Guess  `json:"guesses"`
	Slots    []Slot   `json:"slots"`
	Bets     []Bet    `json:"bets"`
	Revealed bool     `json:"revealed"`
	Nobody   bool     `json:"nobody"`
	Winners  []int    `json:"winners"`
	Second   []int    `json:"second"`
	Results  []Result `json:"results"`
}

func (c *Current) guessOf(playerID string) *Guess {
	for i := range c.Guesses {
		if c.Guesses[i].PlayerID == playerID {
			return &c.Guesses[i]
		}
	}
	return nil
}

func (c *Current) betOf(playerID string) *Bet {
	for i := range c.Bets {
		if c.Bets[i].PlayerID == playerID {
			return &c.Bets[i]
		}
	}
	return nil
}

// slotOf is the slot holding a player's guess, or -1.
func (c *Current) slotOf(playerID string) int {
	for i, s := range c.Slots {
		if slices.Contains(s.OwnerIDs, playerID) {
			return i
		}
	}
	return -1
}

// LogEntry is a line of the game log.
type LogEntry struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Event is a public description of something that happened, for sounds and
// animations (screens draw from states, so a missed event never matters).
// Nothing secret goes into an event before its reveal; the reveal carries
// every guess and bet so game records can re-tune the odds and the
// computers later.
type Event struct {
	Seq        int64    `json:"seq"`
	Type       string   `json:"type"`
	PlayerID   string   `json:"playerId,omitempty"`
	Q          int      `json:"q,omitempty"`  // the question's seq (question, reveal)
	No         int      `json:"no,omitempty"` // the question's number (question)
	Slots      int      `json:"slots,omitempty"`
	QuestionID string   `json:"questionId,omitempty"` // the pack question (reveal)
	Answer     *int     `json:"answer,omitempty"`
	Winners    []int    `json:"winners,omitzero"`
	Second     []int    `json:"second,omitzero"`
	Nobody     bool     `json:"nobody,omitempty"`
	Guesses    []Guess  `json:"guesses,omitzero"`
	Bets       []Bet    `json:"bets,omitzero"`
	WinnerIDs  []string `json:"winnerIds,omitzero"`
}

// Options configure a new or restored game.
type Options struct {
	// Seed makes the game deterministic (tests, fixtures, the arena): the
	// deal and every computer decision; nil seeds from crypto/rand.
	Seed *[32]byte
	// Clock returns Unix milliseconds; nil uses time.Now.
	Clock func() int64
	// Library is the question packs to deal from; nil uses the embedded ones.
	Library *Library
	// ComputerName names a computer the fill setting adds, given the names
	// already at the table; nil uses "Computer 1", "Computer 2", ...
	ComputerName func(taken map[string]bool) string
}

// Game is one game, lobby to finish: the authoritative state. It is not
// safe for concurrent use; the server calls it under its own lock. Its
// snapshot holds every answer, so it never goes to a client.
type Game struct {
	Code     string    `json:"code"`
	Status   string    `json:"status"`
	Phase    string    `json:"phase"`
	HostID   string    `json:"hostId"`
	Settings Settings  `json:"settings"`
	Players  []*Player `json:"players"` // join order
	// Questions are dealt at the start; QuestionNo is the current one's
	// number (0 in the lobby and the intro), QuestionSeq its seq.
	Questions   []*Question `json:"questions"`
	QuestionNo  int         `json:"questionNo"`
	QuestionSeq int         `json:"questionSeq"`
	Cur         *Current    `json:"current"`
	PhaseEndsAt int64       `json:"phaseEndsAt"`
	// History is the room's memory of played question ids, least recently
	// played first (see Played).
	History    []string   `json:"played"`
	Credits    []string   `json:"credits"`
	WinnerIDs  []string   `json:"winnerIds"`
	StartedAt  int64      `json:"startedAt"`
	FinishedAt int64      `json:"finishedAt"`
	Log        []LogEntry `json:"log"`
	LogSeq     int        `json:"logSeq"`
	Events     []Event    `json:"events"`
	EventSeq   int64      `json:"eventSeq"`
	Version    int64      `json:"version"`
	PlayerSeq  int        `json:"playerSeq"`
	RNGState   []byte     `json:"rng"`

	src   *mrand.ChaCha8
	rng   *mrand.Rand
	clock func() int64
	lib   *Library
	name  func(taken map[string]bool) string
}

// NewGame creates a game in the lobby.
func NewGame(code string, opts Options) *Game {
	g := &Game{
		Code: code, Status: StatusLobby, Phase: PhaseLobby, Settings: DefaultSettings(), Players: []*Player{},
		History: []string{}, Credits: []string{}, WinnerIDs: []string{}, Log: []LogEntry{}, Events: []Event{},
		Version: 1,
	}
	g.src = mrand.NewChaCha8(seedFrom(opts))
	g.rng = mrand.New(g.src)
	g.configure(opts)
	return g
}

func seedFrom(opts Options) [32]byte {
	var seed [32]byte
	if opts.Seed != nil {
		seed = *opts.Seed
	} else if _, err := rand.Read(seed[:]); err != nil {
		panic(err)
	}
	return seed
}

func (g *Game) configure(opts Options) {
	g.clock, g.lib, g.name = opts.Clock, opts.Library, opts.ComputerName
	if g.clock == nil {
		g.clock = func() int64 { return time.Now().UnixMilli() }
	}
	if g.name == nil {
		g.name = func(taken map[string]bool) string {
			for n := 1; ; n++ {
				if name := fmt.Sprintf("Computer %d", n); !taken[name] {
					return name
				}
			}
		}
	}
}

// Restore rebuilds a game from Snapshot output, random stream included, so a
// restored game plays its computers exactly as it would have.
func Restore(snapshot []byte, opts Options) (*Game, error) {
	g := &Game{}
	if err := json.Unmarshal(snapshot, g); err != nil {
		return nil, err
	}
	g.src = &mrand.ChaCha8{}
	if err := g.src.UnmarshalBinary(g.RNGState); err != nil {
		g.src = mrand.NewChaCha8(seedFrom(opts))
	}
	g.rng = mrand.New(g.src)
	g.configure(opts)
	for _, list := range []*[]string{&g.History, &g.Credits, &g.WinnerIDs} {
		if *list == nil {
			*list = []string{}
		}
	}
	if g.Players == nil {
		g.Players = []*Player{}
	}
	if g.Log == nil {
		g.Log = []LogEntry{}
	}
	if g.Events == nil {
		g.Events = []Event{}
	}
	if err := g.Settings.Validate(); err != nil {
		return nil, fmt.Errorf("saved %s game: %w", Title, err)
	}
	if g.Status == StatusPlaying && g.Phase != PhaseIntro && g.Cur == nil {
		return nil, fmt.Errorf("saved %s game: no current question in phase %s", Title, g.Phase)
	}
	// The current question is one of the dealt ones again (JSON made two
	// copies).
	if cur := g.Cur; cur != nil && cur.No >= 1 && cur.No <= len(g.Questions) && g.Questions[cur.No-1].ID == cur.Question.ID {
		cur.Question = g.Questions[cur.No-1]
	}
	return g, nil
}

// Snapshot is the whole game for persistence: answers, guesses, bets and the
// random stream, so it is for the server only.
func (g *Game) Snapshot() ([]byte, error) {
	state, err := g.src.MarshalBinary()
	if err != nil {
		return nil, err
	}
	g.RNGState = state
	return json.Marshal(g)
}

// Now is the game clock in Unix milliseconds.
func (g *Game) Now() int64 { return g.clock() }

// Touch bumps the version so clients notice server-side changes.
func (g *Game) Touch() { g.Version++ }

// SetHost makes playerID the host.
func (g *Game) SetHost(playerID string) {
	if g.HostID != playerID {
		g.HostID = playerID
		g.Version++
	}
}

// SeatLimit is the most players a game seats.
func (g *Game) SeatLimit() int { return MaxPlayers }

// Played is the room's memory of played question ids, least recently played
// first, including this game's. The room hands it to its next game
// (SetPlayed), so a rematch asks questions the room has not seen.
func (g *Game) Played() []string { return slices.Clone(g.History) }

// SetPlayed gives a game in the lobby the room's memory (Played of the
// room's previous game).
func (g *Game) SetPlayed(ids []string) {
	if g.Status == StatusLobby {
		g.History = slices.Clone(ids)
	}
}

// SetOnline tells the engine whether a person's phone is connected (§4: an
// away player's chips are not placed for them when the bets close). It does
// not bump the version: nothing in any state shows it.
func (g *Game) SetOnline(playerID string, online bool) {
	if p := g.Player(playerID); p != nil && !p.computer() {
		p.Online = online
	}
}

// Player returns a player by id.
func (g *Game) Player(id string) *Player {
	for _, p := range g.Players {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (g *Game) seat(id string) int {
	return slices.IndexFunc(g.Players, func(p *Player) bool { return p.ID == id })
}

// playing lists the players still in the game, in seat order.
func (g *Game) playing() []*Player {
	var out []*Player
	for _, p := range g.Players {
		if !p.Left {
			out = append(out, p)
		}
	}
	return out
}

func (g *Game) playingIDs() []string {
	ids := []string{}
	for _, p := range g.playing() {
		ids = append(ids, p.ID)
	}
	return ids
}

func (g *Game) library() (*Library, error) {
	if g.lib != nil {
		return g.lib, nil
	}
	return DefaultLibrary()
}

func (g *Game) logf(format string, args ...any) {
	g.LogSeq++
	g.Log = append(g.Log, LogEntry{ID: fmt.Sprintf("l%d", g.LogSeq), Text: fmt.Sprintf(format, args...)})
	if len(g.Log) > maxLog {
		g.Log = append([]LogEntry(nil), g.Log[len(g.Log)-maxLog:]...)
	}
}

func (g *Game) emit(e Event) {
	g.EventSeq++
	e.Seq = g.EventSeq
	g.Events = append(g.Events, e)
	if len(g.Events) > maxEvents {
		g.Events = append([]Event(nil), g.Events[len(g.Events)-maxEvents:]...)
	}
}

// EventsSince returns the events with Seq > seq (bounded history).
func (g *Game) EventsSince(seq int64) []Event {
	k := sort.Search(len(g.Events), func(i int) bool { return g.Events[i].Seq > seq })
	return append([]Event(nil), g.Events[k:]...)
}

// between draws a whole number of milliseconds in [lo, hi].
func (g *Game) between(lo, hi int) int64 { return int64(lo + g.rng.IntN(hi-lo+1)) }

// ---------------------------------------------------------------- seating

// AddPlayer seats a new player ("human" or "ai") in the lobby. A computer
// plays at the Normal level until SetLevel says otherwise.
func (g *Game) AddPlayer(name, kind string) (string, error) {
	switch kind {
	case KindHuman:
		return g.seatPlayer(name, &Player{Kind: KindHuman, Online: true})
	case KindAI:
		return g.AddComputer(name, LevelNormal)
	}
	return "", errors.New(`kind must be "human" or "ai"`)
}

// AddComputer seats a computer player of the given level in the lobby.
func (g *Game) AddComputer(name, level string) (string, error) {
	if _, ok := profiles[level]; !ok {
		return "", fmt.Errorf("unknown level %q", level)
	}
	return g.seatPlayer(name, &Player{Kind: KindAI, Level: level, Online: true})
}

// SetLevel changes a computer's level in the lobby.
func (g *Game) SetLevel(playerID, level string) error {
	p := g.Player(playerID)
	switch {
	case p == nil || !p.computer():
		return errors.New("no such computer player")
	case g.Status != StatusLobby:
		return errors.New("the game has already started")
	}
	if _, ok := profiles[level]; !ok {
		return fmt.Errorf("unknown level %q", level)
	}
	p.Level = level
	g.Version++
	return nil
}

func (g *Game) seatPlayer(name string, p *Player) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("a name is required")
	}
	if r := []rune(name); len(r) > maxName {
		name = string(r[:maxName])
	}
	if g.Status != StatusLobby {
		return "", errors.New("the game has already started")
	}
	if len(g.Players) >= g.SeatLimit() {
		return "", errors.New("the table is full")
	}
	g.PlayerSeq++
	p.ID, p.Name = fmt.Sprintf("p%d", g.PlayerSeq), name
	g.Players = append(g.Players, p)
	if g.HostID == "" && p.Kind == KindHuman {
		g.HostID = p.ID
	}
	g.emit(Event{Type: EvJoin, PlayerID: p.ID})
	g.Version++
	return p.ID, nil
}

// RemovePlayer takes a player off (the host may remove anyone; players may
// remove themselves). In the lobby the seat goes. In a game they stop
// playing and keep their score, ranked below everyone still playing; their
// guess stays on the line for the question being played (others may have
// bet on it), but they score nothing more.
func (g *Game) RemovePlayer(requesterID, playerID string) error {
	i := g.seat(playerID)
	if i < 0 || g.Players[i].Left {
		return errors.New("no such player")
	}
	p := g.Players[i]
	if requesterID != playerID && requesterID != g.HostID && requesterID != TableHost {
		return errors.New("only the host can remove other players")
	}
	now := g.Now()
	switch g.Status {
	case StatusLobby:
		g.Players = slices.Delete(g.Players, i, i+1)
		g.emit(Event{Type: EvLeave, PlayerID: p.ID})
	case StatusPlaying:
		g.advance(now) // run anything already due before they go
		if g.Status != StatusPlaying {
			p.Left = true // that finished the game: leaving changes nothing but the seat
			break
		}
		p.Left, p.Plan = true, nil
		g.logf("%s leaves the game", p.Name)
		g.emit(Event{Type: EvLeave, PlayerID: p.ID})
		g.afterLeave(p, now)
	default:
		p.Left = true
	}
	if g.HostID == p.ID {
		g.HostID = g.firstHuman()
	}
	g.Version++
	return nil
}

func (g *Game) firstHuman() string {
	for _, p := range g.Players {
		if p.Kind == KindHuman && !p.Left {
			return p.ID
		}
	}
	return ""
}

// ---------------------------------------------------------------- actions

// Command is a decoded action (docs/CLOSEST-API.md §2).
type Command struct {
	Type     string          `json:"type"`
	Settings json.RawMessage `json:"settings,omitempty"`
	// Q is the question's seq (question.seq), so an action meant for an
	// earlier question is refused instead of landing on this one.
	Q         *int     `json:"q,omitempty"`
	Value     *float64 `json:"value,omitempty"`
	Chips     []int    `json:"chips,omitempty"`
	Stake     *int     `json:"stake,omitempty"`
	StakeSlot *int     `json:"stakeSlot,omitempty"`
}

// PerformAction applies a JSON action from playerID.
func (g *Game) PerformAction(playerID string, raw json.RawMessage) error {
	var c Command
	if len(raw) == 0 || json.Unmarshal(raw, &c) != nil || c.Type == "" {
		return errors.New("invalid action format")
	}
	return g.Do(playerID, c)
}

var hostCommands = map[string]bool{"start": true, "settings": true}

// Do applies a command. Anything already due runs first, so an action that
// arrives just after the server's timer should have fired meets the state
// it should.
func (g *Game) Do(playerID string, c Command) error {
	now := g.Now()
	g.advance(now)
	var p *Player
	if playerID == TableHost {
		if !hostCommands[c.Type] {
			return errors.New("the table screen can only start the game or change the rules")
		}
	} else if p = g.Player(playerID); p == nil || p.Left {
		return errors.New("you are not playing in this game")
	}
	var err error
	switch c.Type {
	case "start":
		err = g.start(p, now)
	case "settings":
		err = g.updateSettings(p, c.Settings)
	case "guess":
		err = g.guess(p, c, now)
	case "bet":
		err = g.bet(p, c, now)
	default:
		err = fmt.Errorf("unknown action %q", c.Type)
	}
	if err != nil {
		return err
	}
	g.Version++
	return nil
}

var errNotPlaying = errors.New("the game is not in progress")

func (g *Game) start(p *Player, now int64) error {
	if g.Status != StatusLobby {
		return errors.New("the game has already started")
	}
	if p != nil && p.ID != g.HostID {
		return errors.New("only the host can start the game")
	}
	if len(g.Players) < MinPlayers {
		return errors.New("add a player first")
	}
	lib, err := g.library()
	if err != nil {
		return fmt.Errorf("the %s questions could not be loaded", Title)
	}
	questions, credits, err := lib.deal(g.rng, g.Settings, g.History)
	if err != nil {
		return err
	}
	if g.Settings.Fill {
		for len(g.Players) < FillTo {
			taken := map[string]bool{}
			for _, q := range g.Players {
				taken[q.Name] = true
			}
			if _, err := g.AddComputer(g.name(taken), LevelNormal); err != nil {
				return err
			}
		}
	}
	g.Questions, g.Credits = questions, append([]string{}, credits...)
	g.Status, g.StartedAt = StatusPlaying, now
	g.setPhase(PhaseIntro, now+introMs)
	g.emit(Event{Type: EvStart})
	g.logf("The game starts: %d questions, and the last one pays double", len(g.Questions))
	return nil
}

func (g *Game) updateSettings(p *Player, raw json.RawMessage) error {
	if g.Status != StatusLobby {
		return errors.New("the rules can only change in the lobby")
	}
	if p != nil && p.ID != g.HostID {
		return errors.New("only the host can change the rules")
	}
	next := g.Settings
	if len(raw) == 0 || json.Unmarshal(raw, &next) != nil {
		return errors.New("invalid settings")
	}
	if err := next.Validate(); err != nil {
		return err
	}
	g.Settings = next
	return nil
}

// stakeMax is the most a player may stake now: their score, at most 3.
func stakeMax(p *Player) int { return max(0, min(maxStake, p.Score)) }

// guess records a guess, or changes it once.
func (g *Game) guess(p *Player, c Command, now int64) error {
	cur := g.Cur
	switch {
	case g.Status != StatusPlaying:
		return errNotPlaying
	case cur == nil || (c.Q != nil && *c.Q != cur.Seq):
		return errors.New("that question is over")
	case g.Phase != PhaseGuess:
		return errors.New("guesses are closed")
	case !slices.Contains(cur.Playing, p.ID):
		return errors.New("you are not playing this question")
	}
	q := cur.Question
	if c.Value == nil || *c.Value != math.Trunc(*c.Value) || *c.Value < float64(q.Min) || *c.Value > float64(q.Max) {
		return fmt.Errorf("guess between %s and %s", formatNumber(q.Min, q.Format), formatNumber(q.Max, q.Format))
	}
	if gs := cur.guessOf(p.ID); gs != nil && gs.Changed {
		return errors.New("you have already changed your guess")
	}
	g.placeGuess(p, int(*c.Value), now)
	return nil
}

// placeGuess records a valid guess (a person's or a computer's) and closes
// the guesses when everyone is in.
func (g *Game) placeGuess(p *Player, value int, now int64) {
	cur := g.Cur
	gs := Guess{PlayerID: p.ID, Value: value, AfterMs: max(0, now-cur.OpenedAt)}
	if i := slices.IndexFunc(cur.Guesses, func(x Guess) bool { return x.PlayerID == p.ID }); i >= 0 {
		gs.Changed = true
		cur.Guesses = slices.Delete(cur.Guesses, i, i+1) // a changed guess goes to the back of the order
	} else {
		g.emit(Event{Type: EvGuessed, PlayerID: p.ID})
	}
	cur.Guesses = append(cur.Guesses, gs)
	g.checkGuesses(now)
}

// bet places a player's chips and stake.
func (g *Game) bet(p *Player, c Command, now int64) error {
	cur := g.Cur
	switch {
	case g.Status != StatusPlaying:
		return errNotPlaying
	case cur == nil || (c.Q != nil && *c.Q != cur.Seq):
		return errors.New("that question is over")
	case g.Phase != PhaseBet:
		return errors.New("bets are closed")
	case !slices.Contains(cur.Playing, p.ID):
		return errors.New("you are not playing this question")
	case cur.betOf(p.ID) != nil:
		return errors.New("you have already bet")
	}
	if len(c.Chips) != chipsPerBet || slices.ContainsFunc(c.Chips, func(i int) bool { return i < 0 || i >= len(cur.Slots) }) {
		return errors.New("pick two slots")
	}
	top := stakeMax(p)
	stake := 0
	if c.Stake != nil {
		stake = *c.Stake
	}
	if stake < 0 || stake > top {
		return fmt.Errorf("stake between 0 and %d", top)
	}
	b := Bet{PlayerID: p.ID, Chips: [2]int{c.Chips[0], c.Chips[1]}, Stake: stake}
	if stake > 0 {
		if c.StakeSlot == nil || !slices.Contains(c.Chips, *c.StakeSlot) {
			return errors.New("put the stake on one of your chips")
		}
		slot := *c.StakeSlot
		b.StakeSlot = &slot
	}
	g.placeBet(b, now)
	return nil
}

// placeBet records a valid bet and shows the chips when everyone is in.
func (g *Game) placeBet(b Bet, now int64) {
	g.Cur.Bets = append(g.Cur.Bets, b)
	g.emit(Event{Type: EvBetIn, PlayerID: b.PlayerID})
	g.checkBets(now)
}

// EndGame ends a running game now: the scores so far decide the places (the
// End Game button). It returns the winner (the first, when several share
// first place).
func (g *Game) EndGame() (string, error) {
	if g.Status != StatusPlaying {
		return "", errNotPlaying
	}
	g.logf("The game was ended early")
	g.finish(g.Now())
	g.Version++
	if len(g.WinnerIDs) == 0 {
		return "", nil
	}
	return g.WinnerIDs[0], nil
}

// finish ends the game: places, winners and gameOver.
func (g *Game) finish(now int64) {
	g.setPhase(PhaseFinished, 0)
	g.Status, g.FinishedAt = StatusFinished, now
	g.place()
	var names []string
	top := 0
	for _, id := range g.WinnerIDs {
		p := g.Player(id)
		names, top = append(names, p.Name), p.Score
	}
	switch len(names) {
	case 0:
		g.logf("Game over")
	case 1:
		g.logf("%s wins with %d points", names[0], top)
	default:
		g.logf("%s share the win with %d points", strings.Join(names, " and "), top)
	}
	g.emit(Event{Type: EvGameOver, WinnerIDs: slices.Clone(g.WinnerIDs)})
}

// ahead reports whether a places above b: more points, or as many and more
// closest guesses.
func ahead(a, b *Player) bool {
	return a.Score > b.Score || (a.Score == b.Score && a.Closest > b.Closest)
}

// place ranks everyone by score, then most closest guesses (still level:
// a shared place); players who left rank below everyone who stayed.
// Everyone placed first wins.
func (g *Game) place() {
	var stayed, gone []*Player
	for _, p := range g.Players {
		if p.Left {
			gone = append(gone, p)
		} else {
			stayed = append(stayed, p)
		}
	}
	rank := func(group []*Player, above int) {
		for _, p := range group {
			higher := 0
			for _, q := range group {
				if ahead(q, p) {
					higher++
				}
			}
			p.Place = above + higher + 1
		}
	}
	rank(stayed, 0)
	rank(gone, len(stayed))
	g.WinnerIDs = []string{}
	for _, p := range stayed {
		if p.Place == 1 {
			g.WinnerIDs = append(g.WinnerIDs, p.ID)
		}
	}
}
