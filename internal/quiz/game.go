package quiz

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
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

// Phases (docs/QUIZ-API.md §4).
const (
	PhaseLobby       = "lobby"
	PhaseIntro       = "intro"       // a board's titles are read out
	PhasePick        = "pick"        // the chooser picks a clue
	PhaseGold        = "gold"        // the picked clue is a gold square
	PhaseBet         = "bet"         // the gold square finder bets
	PhaseRead        = "read"        // the clue is read; the choices open at Question.OpenAt
	PhaseAnswer      = "answer"      // the choices are open
	PhaseReveal      = "reveal"      // the right answer and who won it
	PhaseRoundEnd    = "roundEnd"    // the board is done: scores
	PhaseFinalIntro  = "finalIntro"  // the final's category
	PhaseFinalBet    = "finalBet"    // everyone bets in secret
	PhaseFinalRead   = "finalRead"   // the final clue is read
	PhaseFinalAnswer = "finalAnswer" // everyone answers in secret
	PhaseFinalReveal = "finalReveal" // answers and bets, lowest score first
	PhaseFinished    = "finished"
)

// finalRound is Game.Round during the final.
const finalRound = 3

// Player kinds.
const (
	KindHuman = "human"
	KindAI    = "ai"
)

// TableHost is the requester the server uses when the table screen (the host
// of its room, with no seat of its own) sends a host command while no person
// is seated, so nobody holds HostID (as in poker and Ludo: a table of
// computers only can be set up and started).
const TableHost = "@table"

// Event types (Event.Type, docs/QUIZ-API.md §5).
const (
	EvJoin        = "join"
	EvLeave       = "leave"
	EvStart       = "start"
	EvIntro       = "intro"       // Round's board (or, round 3, the final's category) is shown
	EvPick        = "pick"        // PlayerID picked Category, Row worth Value
	EvGold        = "gold"        // PlayerID found a gold square
	EvBet         = "bet"         // PlayerID bets Amount on their gold square
	EvOpen        = "open"        // the choices of clue Clue are open
	EvTap         = "tap"         // PlayerID answered (which choice stays secret until the reveal)
	EvRight       = "right"       // PlayerID won Value
	EvWrong       = "wrong"       // PlayerID lost Value
	EvNoAnswer    = "noAnswer"    // nobody won the clue
	EvReveal      = "reveal"      // clue Clue was judged; Taps is every tap with its timing, for records
	EvRoundEnd    = "roundEnd"    // Round's board is done
	EvFinalBet    = "finalBet"    // PlayerID has bet in the final (the amount stays secret)
	EvFinalReveal = "finalReveal" // PlayerID's final: Correct, Bet; Tap is their answer's timing
	EvGameOver    = "gameOver"    // WinnerIDs won
)

// Timers (docs/QUIZ-API.md §4), in milliseconds.
const (
	introMs           = 3000
	introPerTitleMs   = 600 // the screen reads each category title in turn
	pickMs            = 12000
	goldMs            = 2500
	betMs             = 15000
	revealMs          = 3500
	revealLongMs      = 4500 // after a gold square, or when nobody won: time to take in the answer
	roundEndMs        = 5000
	finalIntroMs      = 4000
	finalBetMs        = 30000
	finalAnswerMs     = 20000
	finalRevealStepMs = 3000
	finalRevealEndMs  = 2000
)

// Bets.
const (
	goldMinBet  = 5
	finalTopBet = 500 // everyone may bet at least this much in the final, whatever their score
)

const (
	maxLog    = 80
	maxEvents = 300
	maxName   = 24
	maxSteps  = 1000 // most timed steps one Advance runs (a guard; a game has a few hundred in all)
)

// Player is a seat.
type Player struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Level, Strong and Weak are a computer's level and the topics it knows
	// better and worse this game (§8).
	Level   string   `json:"level,omitempty"`
	Strong  []string `json:"strong,omitempty"`
	Weak    []string `json:"weak,omitempty"`
	Score   int      `json:"score"` // may go below zero
	Correct int      `json:"correct"`
	Wrong   int      `json:"wrong"`
	Place   int      `json:"place"` // set when the game ends
	Left    bool     `json:"left"`
	// Latency is the player's round trip as the server measured it (§1.1)
	// and Online whether their phone is connected; computers have 0 and are
	// always online.
	Latency int  `json:"latency"`
	Online  bool `json:"online"`
	// Plan is a computer's next move in the current phase (never shown).
	Plan *Plan `json:"plan,omitempty"`
}

func (p *Player) computer() bool { return p.Kind == KindAI }

// latency is what §6 uses for the player: computers tap with none.
func (p *Player) latency() int {
	if p.computer() {
		return 0
	}
	return p.Latency
}

// Plan is a computer's next move: due At; for a tap, the choice it taps and
// the time it took (drawn when the choices open, §6.5).
type Plan struct {
	At     int64 `json:"at"`
	Choice int   `json:"choice"`
	Ms     int   `json:"ms"`
}

// Question is a clue being read and answered: a board clue or the final.
// Choices are shuffled per clue; Answer is the right one's index, which no
// view shows before the reveal.
type Question struct {
	Text     string   `json:"text"`
	Choices  []string `json:"choices"`
	Answer   int      `json:"answer"`
	Topic    string   `json:"topic"`
	Tier     int      `json:"tier"`
	ReadAt   int64    `json:"readAt"`
	OpenAt   int64    `json:"openAt"`
	ClosesAt int64    `json:"closesAt"`
	Taps     []Tap    `json:"taps"` // arrival order until judged, then time order
	// DecideAt is when the taps are judged: the first correct tap plus the
	// §6 wait or, with none by then, the close plus the grace for taps still
	// on their way; 0 until one of those is known.
	DecideAt int64 `json:"decideAt"`
}

// tapOf returns the player's tap, or nil.
func (q *Question) tapOf(playerID string) *Tap {
	for i := range q.Taps {
		if q.Taps[i].PlayerID == playerID {
			return &q.Taps[i]
		}
	}
	return nil
}

func (q *Question) hasCorrect() bool {
	return slices.ContainsFunc(q.Taps, func(t Tap) bool { return t.Correct })
}

// Tap is one answer with its §6 timing.
type Tap struct {
	PlayerID string `json:"playerId"`
	Choice   int    `json:"choice"`
	// Reported is the ms the phone sent (-1 when missing or invalid);
	// Arrival the server time the tap arrived; Latency the player's latency
	// then; Effective the time the tap counts as (§6.3).
	Reported  int   `json:"reported"`
	Arrival   int64 `json:"arrival"`
	Latency   int   `json:"latency"`
	Effective int   `json:"effective"`
	Correct   bool  `json:"correct"`
	// Late and Delta are set when the clue is judged.
	Late  bool `json:"late"`
	Delta int  `json:"delta"`
}

// ClueState is the board clue being played.
type ClueState struct {
	Seq      int    `json:"seq"`
	Category int    `json:"category"` // board column
	Row      int    `json:"row"`
	Value    int    `json:"value"`
	Gold     bool   `json:"gold"`
	Finder   string `json:"finder,omitempty"` // a gold square's finder: the only one who answers it
	// Q is set from the reading on; Eligible is who may answer.
	Q        *Question `json:"question"`
	Eligible []string  `json:"eligible"`
	Decided  bool      `json:"decided"`
	WinnerID string    `json:"winnerId"`
}

// GoldBet is a gold square's bet (public once placed, as on TV).
type GoldBet struct {
	PlayerID string `json:"playerId"`
	Min      int    `json:"min"`
	Max      int    `json:"max"`
	Amount   int    `json:"amount"`
	Placed   bool   `json:"placed"`
}

// FinalState is the final round.
type FinalState struct {
	Seq      int        `json:"seq"`
	ID       string     `json:"id"`
	Category string     `json:"category"`
	Bets     []FinalBet `json:"bets"` // in the order placed; secret until each reveal
	Q        *Question  `json:"question"`
	// Order is the reveal order (lowest score first), Revealed how many are
	// revealed, RevealAt when the next one is.
	Order    []string      `json:"order"`
	Revealed int           `json:"revealed"`
	RevealAt int64         `json:"revealAt"`
	Results  []FinalResult `json:"results"`
}

// betOf returns a player's final bet (0 when they have none).
func (f *FinalState) betOf(playerID string) (int, bool) {
	for _, b := range f.Bets {
		if b.PlayerID == playerID {
			return b.Amount, true
		}
	}
	return 0, false
}

// FinalBet is one secret final bet.
type FinalBet struct {
	PlayerID string `json:"playerId"`
	Amount   int    `json:"amount"`
}

// FinalResult is one revealed final answer.
type FinalResult struct {
	PlayerID string `json:"playerId"`
	Bet      int    `json:"bet"`
	Choice   int    `json:"choice"` // -1: no answer (counts as wrong)
	Correct  bool   `json:"correct"`
	Before   int    `json:"before"`
	After    int    `json:"after"`
}

// LogEntry is a line of the game log.
type LogEntry struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Event is a public description of something that happened, for sounds and
// animations (screens draw from states, so a missed event never matters).
// Nothing secret goes into an event before its reveal.
type Event struct {
	Seq       int64       `json:"seq"`
	Type      string      `json:"type"`
	PlayerID  string      `json:"playerId,omitempty"`
	Round     int         `json:"round,omitempty"`
	Category  *int        `json:"category,omitempty"`
	Row       *int        `json:"row,omitempty"`
	Value     int         `json:"value,omitempty"`
	Amount    int         `json:"amount,omitempty"`
	Clue      int         `json:"clue,omitempty"` // the clue's seq (clue.seq or final.seq)
	Correct   *bool       `json:"correct,omitempty"`
	Bet       *int        `json:"bet,omitempty"`
	WinnerIDs []string    `json:"winnerIds,omitzero"`
	Taps      []TapRecord `json:"taps,omitzero"`
	Tap       *TapRecord  `json:"tap,omitempty"`
}

// TapRecord is a tap as game records keep it (§6.6), for tuning the
// computers and the clue tiers later.
type TapRecord struct {
	PlayerID string `json:"playerId"`
	Choice   int    `json:"choice"`
	Correct  bool   `json:"correct"`
	// Ms is what the phone reported (absent when it sent nothing usable);
	// Arrival is the arrival time after the choices opened.
	Ms        *int `json:"ms,omitempty"`
	Arrival   int  `json:"arrival"`
	Latency   int  `json:"latency"`
	Effective int  `json:"effective"`
}

// Options configure a new or restored game.
type Options struct {
	// Seed makes the game deterministic (tests, fixtures, the arena): the
	// deal, the choice order and every computer decision; nil seeds from
	// crypto/rand.
	Seed *[32]byte
	// Clock returns Unix milliseconds; nil uses time.Now.
	Clock func() int64
	// Library is the question packs to deal from; nil uses the embedded ones.
	Library *Library
}

// Game is one quiz, lobby to finish: the authoritative state. It is not safe
// for concurrent use; the server calls it under its own lock. Its snapshot
// holds every answer, so it never goes to a client.
type Game struct {
	Code     string    `json:"code"`
	Status   string    `json:"status"`
	Phase    string    `json:"phase"`
	HostID   string    `json:"hostId"`
	Settings Settings  `json:"settings"`
	Players  []*Player `json:"players"` // join order
	// Round is 0 in the lobby, the board round (1, 2) and finalRound in the
	// final.
	Round int `json:"round"`
	// Boards (one per board round) and FinalClue are dealt at the start.
	Boards    []*Board `json:"boards"`
	FinalClue *Final   `json:"finalClue"`
	ChooserID string   `json:"chooserId"`
	// LastCategory is the column of the last pick, which the pick timer
	// prefers; -1 before the first.
	LastCategory int         `json:"lastCategory"`
	Clue         *ClueState  `json:"clue"`
	Bet          *GoldBet    `json:"bet"`
	Final        *FinalState `json:"final"`
	ClueSeq      int         `json:"clueSeq"`
	PhaseEndsAt  int64       `json:"phaseEndsAt"`
	// History is the room's memory of played category and final ids, least
	// recently played first (see Played).
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
}

// NewGame creates a game in the lobby.
func NewGame(code string, opts Options) *Game {
	g := &Game{
		Code: code, Status: StatusLobby, Phase: PhaseLobby, Settings: DefaultSettings(), Players: []*Player{},
		LastCategory: -1, History: []string{}, Credits: []string{}, WinnerIDs: []string{},
		Log: []LogEntry{}, Events: []Event{}, Version: 1,
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
	g.clock, g.lib = opts.Clock, opts.Library
	if g.clock == nil {
		g.clock = func() int64 { return time.Now().UnixMilli() }
	}
}

// Restore rebuilds a game from Snapshot output, random stream included, so a
// restored game deals, shuffles and plays its computers exactly as it would
// have.
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
		return nil, fmt.Errorf("saved quiz game: %w", err)
	}
	return g, nil
}

// Snapshot is the whole game for persistence: answers, gold squares, bets
// and the random stream, so it is for the server only.
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

// SeatLimit is the most players a quiz seats.
func (g *Game) SeatLimit() int { return MaxPlayers }

// Played is the room's memory of played category and final ids, least
// recently played first, including this game's. The room hands it to its
// next game (SetPlayed), so a rematch deals categories the room has not seen.
func (g *Game) Played() []string { return slices.Clone(g.History) }

// SetPlayed gives a game in the lobby the room's memory (Played of the
// room's previous game).
func (g *Game) SetPlayed(ids []string) {
	if g.Status == StatusLobby {
		g.History = slices.Clone(ids)
	}
}

// SetLatency tells the engine a player's latency (the server's measure, §1.1;
// pass 250 when unknown) and whether their phone is connected. It does not
// bump the version: only the player's own you.latencyMs shows it, and the
// next state carries it.
func (g *Game) SetLatency(playerID string, ms int, online bool) {
	p := g.Player(playerID)
	if p == nil || p.computer() {
		return
	}
	p.Latency, p.Online = min(max(ms, 0), maxLatency), online
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
	for i, p := range g.Players {
		if p.ID == id {
			return i
		}
	}
	return -1
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

// board is the board of the current round (nil outside the board rounds).
func (g *Game) board() *Board {
	if g.Round < 1 || g.Round > len(g.Boards) {
		return nil
	}
	return g.Boards[g.Round-1]
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
		return g.seatPlayer(name, &Player{Kind: KindHuman, Latency: UnknownLatency, Online: true})
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
// playing and keep their score, ranked below everyone still playing; a
// chooser who leaves hands control to the highest score still playing.
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
		g.advance(now) // judge anything already due before they go
		if g.Status != StatusPlaying {
			// That finished the game: leaving now changes nothing but the seat.
			p.Left = true
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

// highestScore is who takes control when the chooser leaves: the highest
// score still playing (ties: the earliest seat).
func (g *Game) highestScore() string {
	var best *Player
	for _, p := range g.playing() {
		if best == nil || p.Score > best.Score {
			best = p
		}
	}
	if best == nil {
		return ""
	}
	return best.ID
}

// lowestScore picks first on the second board (ties: the earliest seat).
func (g *Game) lowestScore() string {
	var best *Player
	for _, p := range g.playing() {
		if best == nil || p.Score < best.Score {
			best = p
		}
	}
	if best == nil {
		return ""
	}
	return best.ID
}

// ---------------------------------------------------------------- actions

// Command is a decoded action (docs/QUIZ-API.md §2).
type Command struct {
	Type     string          `json:"type"`
	Settings json.RawMessage `json:"settings,omitempty"`
	Category *int            `json:"category,omitempty"`
	Row      *int            `json:"row,omitempty"`
	Amount   *int            `json:"amount,omitempty"`
	Clue     *int            `json:"clue,omitempty"`
	Choice   *int            `json:"choice,omitempty"`
	// Ms is how long the person took (§6.2). It is read leniently: anything
	// that is not a sensible number counts as missing, never as an error.
	Ms json.RawMessage `json:"ms,omitempty"`
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
// arrives just before the server's timer fires meets the state it should.
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
	case "pick":
		err = g.pick(p, c.Category, c.Row, now)
	case "bet":
		err = g.bet(p, c.Amount, now)
	case "answer":
		err = g.answer(p, c, now)
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
		return errors.New("the quiz questions could not be loaded")
	}
	d, err := lib.deal(g.rng, g.Settings.Rounds(), g.History)
	if err != nil {
		return err
	}
	g.Boards, g.FinalClue, g.Credits = d.boards, d.final, append([]string{}, d.credits...)
	for _, q := range g.Players {
		if q.computer() {
			q.Strong, q.Weak = g.drawTopics()
		}
	}
	g.Status, g.StartedAt = StatusPlaying, now
	g.ChooserID = g.Players[g.rng.IntN(len(g.Players))].ID
	g.emit(Event{Type: EvStart})
	g.logf("The quiz starts. %s picks first", g.Player(g.ChooserID).Name)
	g.startRound(1, now)
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
	g.Clue, g.Bet = nil, nil
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

// place ranks everyone by score (ties share a place); players who left rank
// below everyone who stayed. Everyone placed first wins.
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
				if q.Score > p.Score {
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
