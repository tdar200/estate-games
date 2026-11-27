package ludo

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"sort"
	"strings"
	"time"
)

// Statuses and phases.
const (
	StatusLobby    = "lobby"
	StatusPlaying  = "playing"
	StatusFinished = "finished"

	PhaseLobby    = "lobby"
	PhaseRoll     = "roll" // the player to act rolls
	PhaseMove     = "move" // the player to act picks one of Moves
	PhaseFinished = "finished"
)

// Player kinds.
const (
	KindHuman = "human"
	KindAI    = "ai"
)

// TableHost is the requester the server uses when the table screen (the host
// of its room, with no seat of its own) sends a host command while no person
// is seated, so nobody holds HostID (as in poker: a table of computers only
// can be set up and started).
const TableHost = "@table"

// Event types (Event.Type).
const (
	EvJoin       = "join"
	EvLeave      = "leave"
	EvStart      = "start"      // PlayerID goes first
	EvTurn       = "turn"       // PlayerID is to roll
	EvRoll       = "roll"       // PlayerID rolled Value; Bonus: it was a 6 (another roll follows a move)
	EvNoMove     = "noMove"     // PlayerID can't use the roll; Bonus: rolls again anyway (a 6)
	EvThreeSixes = "threeSixes" // PlayerID rolled a third 6 in a row: turn over
	EvMove       = "move"       // PlayerID moved Piece along Path to Cell
	EvCapture    = "capture"    // PlayerID sent VictimID's Piece back from Cell
	EvHome       = "home"       // PlayerID's Piece reached home
	EvFinish     = "finish"     // PlayerID has every piece home: Place
	EvAgain      = "again"      // PlayerID rolls again (a 6, a capture or a piece home)
	EvGameOver   = "gameOver"   // PlayerID won
)

const (
	maxLog    = 80
	maxEvents = 300
	maxName   = 24
)

// Player is a seat.
type Player struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	Arm    int     `json:"arm"` // the arm of the board they play from (set again at the start)
	Pieces []Piece `json:"pieces"`
	// Captures counts the other players' pieces this player sent home; it
	// also opens the home column under the capture-first house rule.
	Captures int  `json:"captures"`
	Lost     int  `json:"lost"`  // own pieces sent back to the yard
	Sixes    int  `json:"sixes"` // sixes rolled
	Place    int  `json:"place"` // finishing place, 0 while still playing
	Left     bool `json:"left"`  // removed during the game: their pieces left the board
	LeftSeq  int  `json:"leftSeq"`
}

// active reports whether the player still plays (not finished, not gone).
func (p *Player) active() bool { return !p.Left && p.Place == 0 }

// HomeCount is how many of the player's pieces are home.
func (p *Player) HomeCount() int {
	n := 0
	for _, pc := range p.Pieces {
		if pc.Zone == ZoneHome {
			n++
		}
	}
	return n
}

func (p *Player) allHome() bool { return len(p.Pieces) > 0 && p.HomeCount() == len(p.Pieces) }

// LogEntry is a line of the game log.
type LogEntry struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Event is a public description of something that happened, for sounds and
// animations.
type Event struct {
	Seq      int64    `json:"seq"`
	Type     string   `json:"type"`
	PlayerID string   `json:"playerId,omitempty"`
	Value    int      `json:"value,omitempty"`
	Piece    int      `json:"piece"`
	Path     []string `json:"path,omitempty"`
	Cell     string   `json:"cell,omitempty"`
	VictimID string   `json:"victimId,omitempty"`
	Place    int      `json:"place,omitempty"`
	Bonus    bool     `json:"bonus,omitempty"`
}

// Captured is a piece sent back to its yard by the last move.
type Captured struct {
	PlayerID string `json:"playerId"`
	Piece    int    `json:"piece"`
	Cell     string `json:"cell"` // where it was caught
	Yard     string `json:"yard"` // where it went back to
}

// LastMove describes the most recent move, so screens can hop the piece
// square by square (clients compare Seq).
type LastMove struct {
	Seq      int        `json:"seq"`
	PlayerID string     `json:"playerId"`
	Piece    int        `json:"piece"`
	Roll     int        `json:"roll"`
	From     string     `json:"from"`
	Cell     string     `json:"cell"`
	Path     []string   `json:"path"`
	Captured []Captured `json:"captured"`
	Out      bool       `json:"out"`
	Home     bool       `json:"home"`
	// Again: the move earned another roll (a 6, a capture or a piece home).
	Again bool `json:"again"`
}

// Options configure a new or restored game.
type Options struct {
	// Seed makes the dice deterministic (tests, fixtures, simulations); nil
	// seeds from crypto/rand.
	Seed *[32]byte
	// Clock returns Unix milliseconds; nil uses time.Now.
	Clock func() int64
}

// Game is one Ludo game, lobby to finish: the authoritative state. It is not
// safe for concurrent use; the server calls it under its own lock.
type Game struct {
	Code     string    `json:"code"`
	Status   string    `json:"status"`
	Phase    string    `json:"phase"`
	HostID   string    `json:"hostId"`
	Settings Settings  `json:"settings"`
	Arms     int       `json:"arms"`    // 4 or 6: the board in play (in the lobby, the board the seated players would get)
	Players  []*Player `json:"players"` // join order, which is also turn order (clockwise round the board)
	Turn     int       `json:"turn"`    // index in Players of the player to act; -1 when nobody is
	// TurnNumber counts turns (a bonus roll is the same turn).
	TurnNumber int        `json:"turnNumber"`
	Dice       int        `json:"dice"` // the last roll, 0 before the first
	DiceBy     string     `json:"diceBy"`
	RollSeq    int        `json:"rollSeq"` // rolls so far (screens animate each new one)
	Sixes      int        `json:"sixes"`   // 6s in a row in the current turn
	Moves      []Move     `json:"moves"`   // legal moves for Dice while Phase is move
	LastMove   *LastMove  `json:"lastMove"`
	MoveSeq    int        `json:"moveSeq"`
	WinnerID   string     `json:"winnerId"`
	Placed     int        `json:"placed"` // places handed out so far
	LeftSeq    int        `json:"leftSeq"`
	StartedAt  int64      `json:"startedAt"`
	FinishedAt int64      `json:"finishedAt"`
	Log        []LogEntry `json:"log"`
	LogSeq     int        `json:"logSeq"`
	Events     []Event    `json:"events"`
	EventSeq   int64      `json:"eventSeq"`
	Version    int64      `json:"version"`
	PlayerSeq  int        `json:"playerSeq"`
	RNGState   []byte     `json:"rng"`
	// Forced are dice queued by QueueDice (tests and fixtures only).
	Forced []int `json:"forced,omitempty"`

	src   *mrand.ChaCha8
	rng   *mrand.Rand
	clock func() int64
}

// NewGame creates a game in the lobby.
func NewGame(code string, opts Options) *Game {
	g := &Game{
		Code: code, Status: StatusLobby, Phase: PhaseLobby, Settings: DefaultSettings(),
		Arms: 4, Players: []*Player{}, Turn: -1, Log: []LogEntry{}, Events: []Event{}, Version: 1,
	}
	var seed [32]byte
	if opts.Seed != nil {
		seed = *opts.Seed
	} else if _, err := rand.Read(seed[:]); err != nil {
		panic(err)
	}
	g.src = mrand.NewChaCha8(seed)
	g.rng = mrand.New(g.src)
	g.setClock(opts.Clock)
	return g
}

func (g *Game) setClock(clock func() int64) {
	g.clock = clock
	if g.clock == nil {
		g.clock = func() int64 { return time.Now().UnixMilli() }
	}
}

// Restore rebuilds a game from Snapshot output, dice stream included.
func Restore(snapshot []byte, opts Options) (*Game, error) {
	g := &Game{}
	if err := json.Unmarshal(snapshot, g); err != nil {
		return nil, err
	}
	g.src = &mrand.ChaCha8{}
	if err := g.src.UnmarshalBinary(g.RNGState); err != nil {
		var seed [32]byte
		if opts.Seed != nil {
			seed = *opts.Seed
		} else if _, err := rand.Read(seed[:]); err != nil {
			panic(err)
		}
		g.src = mrand.NewChaCha8(seed)
	}
	g.rng = mrand.New(g.src)
	g.setClock(opts.Clock)
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
		return nil, fmt.Errorf("saved ludo game: %w", err)
	}
	return g, nil
}

// Snapshot is the whole game for persistence (the dice stream included, so
// it is for the server only: a client could predict the next roll from it).
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

// QueueDice makes the next rolls come out as given (tests and fixtures).
func (g *Game) QueueDice(values ...int) {
	for _, v := range values {
		if v >= 1 && v <= 6 {
			g.Forced = append(g.Forced, v)
		}
	}
}

// Board is the board in play (or, in the lobby, the one the seated players
// would get).
func (g *Game) Board() Board { return NewBoard(g.Arms) }

func (g *Game) rules() rules { return rules{b: g.Board(), s: g.Settings} }

// Player returns a player by id.
func (g *Game) Player(id string) *Player {
	for _, p := range g.Players {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (g *Game) index(id string) int {
	for i, p := range g.Players {
		if p.ID == id {
			return i
		}
	}
	return -1
}

// Current is the player to act, or nil.
func (g *Game) Current() *Player {
	if g.Status != StatusPlaying || g.Turn < 0 || g.Turn >= len(g.Players) {
		return nil
	}
	return g.Players[g.Turn]
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

// ---------------------------------------------------------------- seating

// seated counts players still at the table.
func (g *Game) seated() int {
	n := 0
	for _, p := range g.Players {
		if !p.Left {
			n++
		}
	}
	return n
}

// SeatLimit is the most players the current settings take.
func (g *Game) SeatLimit() int { return g.Settings.SeatLimit() }

// arrange gives lobby players the arms and pieces they would play with, so
// the lobby can show the board they'll get.
func (g *Game) arrange() {
	if g.Status != StatusLobby {
		return
	}
	g.Arms = g.Settings.ArmsFor(len(g.Players))
	arms := NewBoard(g.Arms).ArmsFor(len(g.Players))
	for i, p := range g.Players {
		if i < len(arms) {
			p.Arm = arms[i]
		}
		p.Pieces = make([]Piece, g.Settings.Pieces)
		for j := range p.Pieces {
			p.Pieces[j] = Piece{Zone: ZoneYard}
		}
	}
}

// AddPlayer seats a new player ("human" or "ai") in the lobby.
func (g *Game) AddPlayer(name, kind string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("a name is required")
	}
	if r := []rune(name); len(r) > maxName {
		name = string(r[:maxName])
	}
	if kind != KindHuman && kind != KindAI {
		return "", errors.New(`kind must be "human" or "ai"`)
	}
	if g.Status != StatusLobby {
		return "", errors.New("the game has already started")
	}
	if g.seated() >= g.SeatLimit() {
		return "", errors.New("the table is full")
	}
	g.PlayerSeq++
	p := &Player{ID: fmt.Sprintf("p%d", g.PlayerSeq), Name: name, Kind: kind}
	g.Players = append(g.Players, p)
	if g.HostID == "" && kind == KindHuman {
		g.HostID = p.ID
	}
	g.arrange()
	g.emit(Event{Type: EvJoin, PlayerID: p.ID})
	g.Version++
	return p.ID, nil
}

// RemovePlayer takes a player off (the host may remove anyone; players may
// remove themselves). In the lobby the seat goes; in a game their pieces
// leave the board, they rank below everyone still playing, and the turn
// moves on if it was theirs.
func (g *Game) RemovePlayer(requesterID, playerID string) error {
	i := g.index(playerID)
	if i < 0 || g.Players[i].Left {
		return errors.New("no such player")
	}
	p := g.Players[i]
	if requesterID != playerID && requesterID != g.HostID && requesterID != TableHost {
		return errors.New("only the host can remove other players")
	}
	if g.Status == StatusLobby {
		g.Players = append(g.Players[:i], g.Players[i+1:]...)
		g.arrange()
		g.emit(Event{Type: EvLeave, PlayerID: p.ID})
		if g.HostID == p.ID {
			g.HostID = g.firstHuman()
		}
		g.Version++
		return nil
	}
	if g.Status == StatusPlaying {
		wasTurn := g.Turn == i
		g.LeftSeq++
		p.Left, p.LeftSeq = true, g.LeftSeq
		g.logf("%s leaves the game", p.Name)
		g.emit(Event{Type: EvLeave, PlayerID: p.ID})
		if !g.checkGameOver() && wasTurn {
			g.nextTurn()
		}
	} else {
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

// Command is a decoded action (docs/LUDO-API.md §2).
type Command struct {
	Type     string          `json:"type"`
	Piece    *int            `json:"piece,omitempty"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

// MoveCommand is the command that moves piece.
func MoveCommand(piece int) Command { return Command{Type: "move", Piece: &piece} }

// PerformAction applies a JSON action from playerID.
func (g *Game) PerformAction(playerID string, raw json.RawMessage) error {
	var c Command
	if len(raw) == 0 || json.Unmarshal(raw, &c) != nil || c.Type == "" {
		return errors.New("invalid action format")
	}
	return g.Do(playerID, c)
}

var hostCommands = map[string]bool{"start": true, "settings": true}

// Do applies a command.
func (g *Game) Do(playerID string, c Command) error {
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
		err = g.start(p)
	case "settings":
		err = g.updateSettings(p, c.Settings)
	case "roll":
		err = g.roll(p)
	case "move":
		if c.Piece == nil {
			err = errors.New("say which piece to move")
		} else {
			err = g.move(p, *c.Piece)
		}
	default:
		err = fmt.Errorf("unknown action %q", c.Type)
	}
	if err != nil {
		return err
	}
	g.Version++
	return nil
}

func (g *Game) start(p *Player) error {
	if g.Status != StatusLobby {
		return errors.New("the game has already started")
	}
	if p != nil && p.ID != g.HostID {
		return errors.New("only the host can start the game")
	}
	n := g.seated()
	if n < MinPlayers {
		return errors.New("at least 2 players are required")
	}
	if n > g.SeatLimit() {
		return errors.New("the 4-arm board takes 4 players at most")
	}
	g.arrange()
	g.Status, g.StartedAt = StatusPlaying, g.Now()
	g.Turn = g.rng.IntN(len(g.Players))
	g.TurnNumber = 1
	g.Phase = PhaseRoll
	first := g.Players[g.Turn]
	pieces := fmt.Sprintf("%d pieces each", g.Settings.Pieces)
	g.logf("The game starts on the %d-arm board, %s. %s goes first", g.Arms, pieces, first.Name)
	g.emit(Event{Type: EvStart, PlayerID: first.ID})
	g.emit(Event{Type: EvTurn, PlayerID: first.ID})
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
	if g.seated() > next.SeatLimit() {
		return errors.New("more players are seated than the 4-arm board takes")
	}
	g.Settings = next
	g.arrange()
	return nil
}

// die rolls 1–6 (a queued value first, in tests and fixtures).
func (g *Game) die() int {
	if len(g.Forced) > 0 {
		v := g.Forced[0]
		g.Forced = g.Forced[1:]
		if len(g.Forced) == 0 {
			g.Forced = nil
		}
		return v
	}
	return 1 + g.rng.IntN(6)
}

var errNotYourTurn = errors.New("it is not your turn")

func (g *Game) roll(p *Player) error {
	if g.Status != StatusPlaying {
		return errors.New("the game is not in progress")
	}
	if g.Current() != p {
		return errNotYourTurn
	}
	if g.Phase != PhaseRoll {
		return errors.New("move a piece first")
	}
	v := g.die()
	g.Dice, g.DiceBy = v, p.ID
	g.RollSeq++
	if v == 6 {
		g.Sixes++
		p.Sixes++
	} else {
		g.Sixes = 0
	}
	g.emit(Event{Type: EvRoll, PlayerID: p.ID, Value: v, Bonus: v == 6})
	if v == 6 && g.Settings.ThreeSixes && g.Sixes >= 3 {
		g.logf("%s rolls a third 6 in a row and loses the turn", p.Name)
		g.emit(Event{Type: EvThreeSixes, PlayerID: p.ID})
		g.nextTurn()
		return nil
	}
	moves := g.rules().legal(g.Players, g.Turn, v)
	if len(moves) == 0 {
		if v == 6 {
			g.logf("%s rolls a 6 but can't move, and rolls again", p.Name)
			g.emit(Event{Type: EvNoMove, PlayerID: p.ID, Value: v, Bonus: true})
			return nil // still their roll
		}
		g.logf("%s rolls %d and can't move", p.Name, v)
		g.emit(Event{Type: EvNoMove, PlayerID: p.ID, Value: v})
		g.nextTurn()
		return nil
	}
	g.Moves = moves
	g.Phase = PhaseMove
	return nil
}

func (g *Game) move(p *Player, piece int) error {
	if g.Status != StatusPlaying {
		return errors.New("the game is not in progress")
	}
	if g.Current() != p {
		return errNotYourTurn
	}
	if g.Phase != PhaseMove {
		return errors.New("roll the die first")
	}
	var m *Move
	for i := range g.Moves {
		if g.Moves[i].Piece == piece {
			m = &g.Moves[i]
		}
	}
	if m == nil {
		return errors.New("that piece can't move")
	}
	r := g.rules()
	hits := r.apply(g.Players, g.Turn, piece, m.To, nil)
	g.MoveSeq++
	last := &LastMove{Seq: g.MoveSeq, PlayerID: p.ID, Piece: piece, Roll: g.Dice, From: m.From, Cell: m.Cell,
		Path: append([]string(nil), m.Path...), Captured: []Captured{}, Out: m.Out, Home: m.Home}
	g.emit(Event{Type: EvMove, PlayerID: p.ID, Piece: piece, Path: last.Path, Cell: m.Cell})
	switch {
	case m.Out:
		g.logf("%s brings a piece out", p.Name)
	case m.Home:
		g.logf("%s gets a piece home", p.Name)
	}
	for _, h := range hits {
		victim := g.Players[h.player]
		yard := r.b.Cell(victim.Arm, h.piece, Piece{Zone: ZoneYard})
		last.Captured = append(last.Captured, Captured{PlayerID: victim.ID, Piece: h.piece, Cell: m.Cell, Yard: yard})
		g.emit(Event{Type: EvCapture, PlayerID: p.ID, VictimID: victim.ID, Piece: h.piece, Cell: m.Cell})
	}
	if len(hits) == 1 {
		g.logf("%s captures %s's piece", p.Name, g.Players[hits[0].player].Name)
	} else if len(hits) > 1 {
		g.logf("%s captures %d of %s's pieces", p.Name, len(hits), g.Players[hits[0].player].Name)
	}
	if m.Home {
		g.emit(Event{Type: EvHome, PlayerID: p.ID, Piece: piece})
	}
	g.Moves = nil
	g.LastMove = last
	if p.allHome() {
		g.finish(p)
		if !g.checkGameOver() {
			g.nextTurn()
		}
		return nil
	}
	again := g.Dice == 6 || len(hits) > 0 || (m.Home && g.Settings.HomeBonus)
	if again {
		last.Again = true
		g.Phase = PhaseRoll
		g.emit(Event{Type: EvAgain, PlayerID: p.ID})
		return nil
	}
	g.nextTurn()
	return nil
}

// nextTurn hands the die to the next player still playing, clockwise.
func (g *Game) nextTurn() {
	g.Sixes = 0
	g.Moves = nil
	n := len(g.Players)
	for k := 1; k <= n; k++ {
		i := (g.Turn + k + n) % n
		if g.Players[i].active() {
			g.Turn = i
			g.TurnNumber++
			g.Phase = PhaseRoll
			g.emit(Event{Type: EvTurn, PlayerID: g.Players[i].ID})
			return
		}
	}
	g.Turn = -1 // nobody left (checkGameOver ends such games first)
}

func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// finish places a player who has every piece home.
func (g *Game) finish(p *Player) {
	g.Placed++
	p.Place = g.Placed
	if p.Place == 1 {
		g.WinnerID = p.ID
	}
	g.logf("%s has every piece home: %s place", p.Name, ordinal(p.Place))
	g.emit(Event{Type: EvFinish, PlayerID: p.ID, Place: p.Place})
}

// checkGameOver ends the game when it is decided: the first winner when the
// game doesn't play on, or once at most one player is still playing. It
// reports whether the game ended.
func (g *Game) checkGameOver() bool {
	if g.Status != StatusPlaying {
		return false
	}
	still := 0
	for _, p := range g.Players {
		if p.active() {
			still++
		}
	}
	if (!g.Settings.PlayOn && g.Placed > 0) || still <= 1 {
		g.end()
		return true
	}
	return false
}

// Progress is how far a player's pieces have come in all, for ranking a game
// that ends before everyone is home.
func (g *Game) Progress(p *Player) int {
	b := g.Board()
	n := 0
	for _, pc := range p.Pieces {
		n += b.Travelled(pc)
	}
	return n
}

// end finishes the game: players still playing are placed after those who
// finished, by how far their pieces got (then captures, then seat order);
// players who left come last, the last to leave highest.
func (g *Game) end() {
	var rest, gone []*Player
	for _, p := range g.Players {
		switch {
		case p.Left:
			gone = append(gone, p)
		case p.Place == 0:
			rest = append(rest, p)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool {
		a, b := g.Progress(rest[i]), g.Progress(rest[j])
		if a != b {
			return a > b
		}
		return rest[i].Captures > rest[j].Captures
	})
	sort.SliceStable(gone, func(i, j int) bool { return gone[i].LeftSeq > gone[j].LeftSeq })
	for _, p := range append(rest, gone...) {
		g.Placed++
		p.Place = g.Placed
		if p.Place == 1 {
			g.WinnerID = p.ID
		}
	}
	g.Status, g.Phase = StatusFinished, PhaseFinished
	g.Turn = -1
	g.Moves = nil
	g.FinishedAt = g.Now()
	if w := g.Player(g.WinnerID); w != nil {
		g.logf("%s wins the game", w.Name)
	}
	g.emit(Event{Type: EvGameOver, PlayerID: g.WinnerID})
}

// EndGame finishes a game in progress early (the host's END GAME): players
// who are home keep their places; the rest are ranked by how far their
// pieces got. It returns the winner.
func (g *Game) EndGame() (string, error) {
	if g.Status != StatusPlaying {
		return "", errors.New("the game is not in progress")
	}
	g.logf("The game was ended early")
	g.end()
	g.Version++
	return g.WinnerID, nil
}

// ---------------------------------------------------------------- for servers and bots

// Waiting reports who the game waits on and for what ("roll" or "move"), or
// "" when nobody (lobby, finished).
func (g *Game) Waiting() (playerID, kind string) {
	p := g.Current()
	if p == nil {
		return "", ""
	}
	return p.ID, g.Phase
}

// ForcedMove is the piece to move when every legal move does the same thing
// (one piece can move, or several pieces would come out of the yard), so
// there is nothing for the player to choose; ok is false otherwise.
func (g *Game) ForcedMove() (piece int, ok bool) {
	p := g.Current()
	if p == nil || g.Phase != PhaseMove || !sameMoves(p, g.Moves) {
		return 0, false
	}
	return g.Moves[0].Piece, true
}
