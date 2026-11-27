package holdem

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

// Table statuses and phases.
const (
	StatusLobby    = "lobby"
	StatusPlaying  = "playing"
	StatusFinished = "finished"

	PhaseLobby    = "lobby"
	PhaseBetting  = "betting"  // a player is to act
	PhaseRunout   = "runout"   // all-in: the board is revealed street by street
	PhaseShowdown = "showdown" // hand over, cards compared; next hand at NextHandAt
	PhaseHandOver = "handOver" // hand over, everyone else folded; next hand at NextHandAt
	PhasePaused   = "paused"   // fewer than two players can be dealt in
	PhaseFinished = "finished"
)

// Player kinds.
const (
	KindHuman = "human"
	KindAI    = "ai"
)

// Player is a seat at the table. Stack is authoritative between hands;
// during a hand the Hand holds the live stack.
type Player struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Seat       int    `json:"seat"`
	Stack      int    `json:"stack"`
	SittingOut bool   `json:"sittingOut"`
	SitOutNext bool   `json:"sitOutNext"` // sit out from the next hand
	Busted     bool   `json:"busted"`     // no chips; may rebuy until RebuyUntil
	RebuyUntil int64  `json:"rebuyUntil"`
	Rebuys     int    `json:"rebuys"`
	Out        bool   `json:"out"`  // eliminated
	Left       bool   `json:"left"` // removed during the game
	Place      int    `json:"place"`
	Timeouts   int    `json:"timeouts"` // consecutive turn-timer timeouts
	OutHand    int    `json:"outHand"`  // hand in which they lost their last chip (or left)
	OutStack   int    `json:"outStack"` // chips they started that hand with
	OutSeq     int    `json:"outSeq"`
	// TimeBank is the seconds left in the player's time bank; BankHands
	// counts hands dealt in since the last refill.
	TimeBank  int `json:"timeBank"`
	BankHands int `json:"bankHands"`
	// BoughtIn is every chip the player brought to the table: the starting
	// stack (the cash buy-in) plus each rebuy.
	BoughtIn int `json:"boughtIn"`
}

// active reports whether the player is still in the game (not out, not left).
func (p *Player) active() bool { return !p.Out && !p.Left }

// LogEntry is a line of the table log.
type LogEntry struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Event is a structured, public description of something that happened,
// for animations and sounds. Events never contain hidden cards.
type Event struct {
	Seq        int64    `json:"seq"`
	Type       string   `json:"type"`
	HandNumber int      `json:"handNumber,omitempty"`
	PlayerID   string   `json:"playerId,omitempty"`
	Action     string   `json:"action,omitempty"`
	Amount     int      `json:"amount,omitempty"`
	To         int      `json:"to,omitempty"`
	AllIn      bool     `json:"allIn,omitempty"`
	Street     string   `json:"street,omitempty"`
	Cards      []string `json:"cards,omitempty"`
	Pot        int      `json:"pot,omitempty"` // pot index for "win" (0 = main pot)
	HandName   string   `json:"handName,omitempty"`
	Place      int      `json:"place,omitempty"`
	Level      *Level   `json:"level,omitempty"`
}

// Event types.
const (
	EvHandStart = "handStart" // HandNumber, PlayerID = button
	EvPost      = "post"      // forced bet: Action ante|smallBlind|bigBlind
	EvAction    = "action"    // Action fold|check|call|bet|raise, Amount, To, AllIn
	EvDeal      = "deal"      // Street, Cards (board cards)
	EvReveal    = "reveal"    // PlayerID, Cards, HandName (face-up hole cards)
	EvMuck      = "muck"      // PlayerID
	EvUncalled  = "uncalled"  // PlayerID, Amount returned
	EvWin       = "win"       // PlayerID, Amount, Pot, HandName (if shown)
	EvBust      = "bust"      // PlayerID (can rebuy) or with Place (out)
	EvRebuy     = "rebuy"     // PlayerID, Amount
	EvTimeBank  = "timeBank"  // PlayerID's turn timer ran out; their bank of Amount seconds runs
	EvLevelUp   = "levelUp"   // Level
	EvSitOut    = "sitOut"
	EvSitIn     = "sitIn"
	EvJoin      = "join"
	EvLeave     = "leave"
	EvGameOver  = "gameOver" // PlayerID = winner
)

const (
	maxLog    = 80
	maxEvents = 300
)

// Options configure a new or restored table.
type Options struct {
	// Seed makes shuffles deterministic (tests, simulations, replays). Nil
	// seeds from crypto/rand.
	Seed *[32]byte
	// Clock returns Unix milliseconds; nil uses time.Now.
	Clock func() int64
	// Timing overrides DefaultTiming (on restore too, if set).
	Timing *Timing
	// SeatLimit is the most seats a game may choose: 0 means
	// DefaultSeatLimit (8); at most MaxSeats (10).
	SeatLimit int
}

// Table is a multi-hand No-Limit Hold'em session: the party game's
// authoritative state. It is not safe for concurrent use; the server calls
// it under its own lock.
type Table struct {
	Code        string     `json:"code"`
	Status      string     `json:"status"`
	Phase       string     `json:"phase"`
	HostID      string     `json:"hostId"`
	Settings    Settings   `json:"settings"`
	Timing      Timing     `json:"timing"`
	Players     []*Player  `json:"players"` // join order
	Levels      []Level    `json:"levels"`
	LevelIndex  int        `json:"levelIndex"`
	LevelEndsAt int64      `json:"levelEndsAt"` // 0 when the level lasts forever
	HandNumber  int        `json:"handNumber"`
	ButtonSeat  int        `json:"buttonSeat"` // -1 before the first hand
	Hand        *Hand      `json:"hand"`
	HandIDs     []string   `json:"handIds"`    // hand index → player id
	Deadline    int64      `json:"deadline"`   // turn timer of the player to act, time bank included once it runs (0 = none)
	OnBank      bool       `json:"onBank"`     // the actor's turn timer ran out and their time bank is running
	BankFrom    int64      `json:"bankFrom"`   // when the actor's time bank started running
	RunoutAt    int64      `json:"runoutAt"`   // next run-out street
	NextHandAt  int64      `json:"nextHandAt"` // when the next hand is dealt
	StartedAt   int64      `json:"startedAt"`
	FinishedAt  int64      `json:"finishedAt"`
	WinnerID    string     `json:"winnerId"`
	Log         []LogEntry `json:"log"`
	LogSeq      int        `json:"logSeq"`
	Events      []Event    `json:"events"`
	EventSeq    int64      `json:"eventSeq"`
	Version     int64      `json:"version"`
	PlayerSeq   int        `json:"playerSeq"`
	OutSeq      int        `json:"outSeq"`
	SeatLimit   int        `json:"seatLimit"`
	// Odds are the live players' chances (by hand index) during an all-in
	// run-out, recomputed each street; nil otherwise. OddsBoard is the board
	// size they were computed for.
	Odds      []Odds `json:"odds"`
	OddsBoard int    `json:"oddsBoard"`
	RNGState  []byte `json:"rng"`

	src   *mrand.ChaCha8
	rng   *mrand.Rand
	clock func() int64
	// strength caches you.strength per player (see strengthFor); it is
	// deterministic, so it is not saved.
	strength map[string]strengthEntry
}

// NewTable creates a table in the lobby.
func NewTable(code string, opts Options) *Table {
	t := &Table{
		Code:       code,
		Status:     StatusLobby,
		Phase:      PhaseLobby,
		Settings:   DefaultSettings(),
		Timing:     DefaultTiming(),
		Players:    []*Player{},
		ButtonSeat: -1,
		Log:        []LogEntry{},
		Events:     []Event{},
		Version:    1,
	}
	var seed [32]byte
	if opts.Seed != nil {
		seed = *opts.Seed
	} else if _, err := rand.Read(seed[:]); err != nil {
		panic(err)
	}
	t.src = mrand.NewChaCha8(seed)
	t.rng = mrand.New(t.src)
	t.apply(opts)
	return t
}

func (t *Table) apply(opts Options) {
	t.clock = opts.Clock
	if t.clock == nil {
		t.clock = func() int64 { return time.Now().UnixMilli() }
	}
	if opts.Timing != nil {
		t.Timing = *opts.Timing
	}
	if opts.SeatLimit != 0 || t.SeatLimit == 0 {
		t.SeatLimit = opts.SeatLimit
		if t.SeatLimit == 0 {
			t.SeatLimit = DefaultSeatLimit
		}
		t.SeatLimit = min(max(t.SeatLimit, 2), MaxSeats)
	}
	if t.Status == StatusLobby && t.Settings.MaxPlayers > t.SeatLimit && t.seated() <= t.SeatLimit {
		t.Settings.MaxPlayers = t.SeatLimit
	}
}

// Restore rebuilds a table from Snapshot output.
func Restore(snapshot []byte, opts Options) (*Table, error) {
	t := &Table{}
	if err := json.Unmarshal(snapshot, t); err != nil {
		return nil, err
	}
	t.src = &mrand.ChaCha8{}
	if err := t.src.UnmarshalBinary(t.RNGState); err != nil {
		if opts.Seed == nil {
			var seed [32]byte
			if _, err := rand.Read(seed[:]); err != nil {
				panic(err)
			}
			t.src = mrand.NewChaCha8(seed)
		} else {
			t.src = mrand.NewChaCha8(*opts.Seed)
		}
	}
	t.rng = mrand.New(t.src)
	if t.Settings.Style == "" {
		t.Settings.Style = StyleTournament // saved before game styles existed
	}
	t.apply(opts)
	if t.Players == nil {
		t.Players = []*Player{}
	}
	return t, nil
}

// Snapshot is the full state (including the deck and hidden cards) for
// persistence. Never send it to clients.
func (t *Table) Snapshot() ([]byte, error) {
	state, err := t.src.MarshalBinary()
	if err != nil {
		return nil, err
	}
	t.RNGState = state
	return json.Marshal(t)
}

// Now is the table clock in Unix milliseconds.
func (t *Table) Now() int64 { return t.clock() }

// Touch bumps the version so clients notice server-side changes.
func (t *Table) Touch() { t.Version++ }

// SetHost makes playerID the host.
func (t *Table) SetHost(playerID string) {
	if t.HostID != playerID {
		t.HostID = playerID
		t.Version++
	}
}

// Player returns a player by id.
func (t *Table) Player(id string) *Player {
	for _, p := range t.Players {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (t *Table) handIndex(id string) int {
	if t.Hand == nil {
		return -1
	}
	for i, hid := range t.HandIDs {
		if hid == id {
			return i
		}
	}
	return -1
}

// Level is the current blind level.
func (t *Table) Level() Level {
	if len(t.Levels) == 0 {
		return Level{SmallBlind: t.Settings.SmallBlind, BigBlind: t.Settings.BigBlind, Ante: t.Settings.Ante}
	}
	return t.Levels[min(t.LevelIndex, len(t.Levels)-1)]
}

func (t *Table) logf(format string, args ...any) {
	t.LogSeq++
	t.Log = append(t.Log, LogEntry{ID: fmt.Sprintf("l%d", t.LogSeq), Text: fmt.Sprintf(format, args...)})
	if len(t.Log) > maxLog {
		t.Log = append([]LogEntry(nil), t.Log[len(t.Log)-maxLog:]...)
	}
}

func (t *Table) emit(e Event) {
	t.EventSeq++
	e.Seq = t.EventSeq
	if e.HandNumber == 0 {
		e.HandNumber = t.HandNumber
	}
	t.Events = append(t.Events, e)
	if len(t.Events) > maxEvents {
		t.Events = append([]Event(nil), t.Events[len(t.Events)-maxEvents:]...)
	}
}

// EventsSince returns events with Seq > seq (bounded history).
func (t *Table) EventsSince(seq int64) []Event {
	k := sort.Search(len(t.Events), func(i int) bool { return t.Events[i].Seq > seq })
	return append([]Event(nil), t.Events[k:]...)
}

// HoleCards returns the current hand's number and every dealt player's hole
// cards by player id, shown or not. It is for the server's game record only:
// these must never reach a client.
func (t *Table) HoleCards() (int, map[string][]string) {
	if t.Hand == nil {
		return 0, nil
	}
	cards := make(map[string][]string, len(t.HandIDs))
	for i, id := range t.HandIDs {
		if i < len(t.Hand.Hole) {
			cards[id] = cardStrings(t.Hand.Hole[i])
		}
	}
	return t.HandNumber, cards
}

func (t *Table) name(id string) string {
	if p := t.Player(id); p != nil {
		return p.Name
	}
	return "?"
}

func chips(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return "-" + chips(-n)
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// ---------------------------------------------------------------- seating

// AddPlayer seats a new player ("human" or "ai") and returns the id. In a
// running game (late join) the player is dealt in from the next hand.
func (t *Table) AddPlayer(name, kind string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("a name is required")
	}
	if len([]rune(name)) > 24 {
		name = string([]rune(name)[:24])
	}
	if kind != KindHuman && kind != KindAI {
		return "", errors.New(`kind must be "human" or "ai"`)
	}
	switch t.Status {
	case StatusFinished:
		return "", errors.New("the game is over")
	case StatusPlaying:
		if !t.Settings.LateJoin {
			return "", errors.New("joining after the start is turned off")
		}
	}
	seat := t.freeSeat()
	if seat < 0 {
		return "", errors.New("the table is full")
	}
	t.PlayerSeq++
	p := &Player{ID: fmt.Sprintf("p%d", t.PlayerSeq), Name: name, Kind: kind, Seat: seat, Stack: t.Settings.StartingStack,
		BoughtIn: t.Settings.StartingStack, TimeBank: t.Settings.TimeBankSeconds}
	t.Players = append(t.Players, p)
	if t.HostID == "" && kind == KindHuman {
		t.HostID = p.ID
	}
	t.emit(Event{Type: EvJoin, PlayerID: p.ID})
	if t.Status == StatusPlaying {
		t.logf("%s joins the game", p.Name)
		t.maybeResume(t.Now())
	}
	t.Version++
	return p.ID, nil
}

func (t *Table) seated() int {
	n := 0
	for _, p := range t.Players {
		if !p.Left && !p.Out {
			n++
		}
	}
	return n
}

func (t *Table) freeSeat() int {
	taken := map[int]bool{}
	for _, p := range t.Players {
		if p.active() {
			taken[p.Seat] = true
		}
	}
	for s := 0; s < t.Settings.MaxPlayers; s++ {
		if !taken[s] {
			return s
		}
	}
	return -1
}

// RemovePlayer removes a player (the host may remove anyone; players may
// remove themselves). In the lobby the seat is freed; during a game the
// player's hand is folded and they are placed as if eliminated.
func (t *Table) RemovePlayer(requesterID, playerID string) error {
	p := t.Player(playerID)
	if p == nil || p.Left {
		return errors.New("no such player")
	}
	if requesterID != playerID && requesterID != t.HostID {
		return errors.New("only the host can remove other players")
	}
	now := t.Now()
	if t.Status == StatusLobby {
		for i, q := range t.Players {
			if q == p {
				t.Players = append(t.Players[:i], t.Players[i+1:]...)
				break
			}
		}
		t.emit(Event{Type: EvLeave, PlayerID: p.ID})
		if t.HostID == p.ID {
			t.HostID = t.firstHuman()
		}
		t.Version++
		return nil
	}
	if i := t.handIndex(p.ID); i >= 0 && !t.Hand.Done {
		t.Hand.ForceFold(i)
		p.Stack = t.Hand.Stacks[i] // what they take away (a cash game's cash-out)
		t.logf("%s folds and leaves", p.Name)
	} else {
		t.logf("%s leaves", p.Name)
	}
	if !p.Out && !p.Busted {
		p.OutHand, p.OutStack = t.HandNumber, p.Stack
		if i := t.handIndex(p.ID); i >= 0 {
			p.OutStack = t.Hand.StartStacks[i]
		}
	}
	if !p.Out {
		t.OutSeq++
		p.OutSeq = t.OutSeq
	}
	p.Left = true
	p.Busted = false
	t.rankEliminated()
	t.emit(Event{Type: EvLeave, PlayerID: p.ID, Place: p.Place})
	if t.HostID == p.ID {
		t.HostID = t.firstHuman()
	}
	if t.Status == StatusPlaying {
		t.afterHandChange(now)
		t.checkGameOver(now)
	}
	t.Version++
	return nil
}

func (t *Table) firstHuman() string {
	for _, p := range t.Players {
		if p.Kind == KindHuman && p.active() {
			return p.ID
		}
	}
	return ""
}

// contenders counts players still in the game (busted players who may
// still rebuy included).
func (t *Table) contenders() int {
	n := 0
	for _, p := range t.Players {
		if p.active() {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- actions

// Command is a decoded action. See docs/API.md for the JSON forms.
type Command struct {
	Type     string          `json:"type"`
	To       int             `json:"to,omitempty"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

// PerformAction applies a JSON action from playerID.
func (t *Table) PerformAction(playerID string, raw json.RawMessage) error {
	var c Command
	if len(raw) == 0 || json.Unmarshal(raw, &c) != nil || c.Type == "" {
		return errors.New("invalid action format")
	}
	return t.Do(playerID, c)
}

// TableHost is the requester the server uses when a table screen (the host
// of its room, with no seat of its own) sends a host command while no person
// is seated, so nobody holds HostID. It passes the host checks of start,
// settings and deal without a seat, so a table of computers only can be set
// up and started (0.10.2: "there should be no restrictions").
const TableHost = "@table"

// hostCommands are the commands TableHost may send.
var hostCommands = map[string]bool{"start": true, "settings": true, "deal": true}

// Do applies a command.
func (t *Table) Do(playerID string, c Command) error {
	now := t.Now()
	t.advance(now)
	var p *Player
	if playerID == TableHost {
		if !hostCommands[c.Type] {
			return errors.New("the table screen can only start, deal or change the settings")
		}
	} else {
		p = t.Player(playerID)
		if p == nil || p.Left {
			return errors.New("you are not seated at this table")
		}
	}
	var err error
	switch c.Type {
	case "start":
		err = t.start(p, now)
	case "settings":
		err = t.updateSettings(p, c.Settings)
	case "fold", "check", "call", "bet", "raise", "allin":
		err = t.bet(p, Action{Type: ActionType(c.Type), To: c.To}, now)
	case "show":
		err = t.show(p)
	case "sitOut":
		err = t.sitOut(p)
	case "sitIn":
		err = t.sitIn(p, now)
	case "rebuy":
		err = t.rebuy(p, now)
	case "declineRebuy":
		if !p.Busted {
			err = errors.New("you have nothing to decline")
		} else {
			t.eliminate(p, now)
			t.checkGameOver(now)
		}
	case "deal":
		err = t.dealNow(p, now)
	default:
		err = fmt.Errorf("unknown action %q", c.Type)
	}
	if err != nil {
		return err
	}
	t.Version++
	return nil
}

func (t *Table) start(p *Player, now int64) error {
	if t.Status != StatusLobby {
		return errors.New("the game has already started")
	}
	if p != nil && p.ID != t.HostID { // nil: TableHost
		return errors.New("only the host can start the game")
	}
	if t.seated() < 2 {
		return errors.New("at least 2 players are required")
	}
	if err := t.Settings.Validate(t.SeatLimit); err != nil {
		return err
	}
	t.Status = StatusPlaying
	t.StartedAt = now
	t.Levels = BuildSchedule(t.Settings, t.seated())
	t.LevelIndex = 0
	t.LevelEndsAt = 0
	if m := t.Levels[0].Minutes; m > 0 {
		t.LevelEndsAt = now + int64(m)*60_000
	}
	for _, q := range t.Players {
		q.Stack, q.BoughtIn = t.Settings.StartingStack, t.Settings.StartingStack
		q.TimeBank, q.BankHands = t.Settings.TimeBankSeconds, 0
	}
	lv := t.Level()
	if t.Settings.Cash() {
		t.logf("The cash game starts: %s chips each, blinds %s/%s", chips(t.Settings.StartingStack), chips(lv.SmallBlind), chips(lv.BigBlind))
	} else {
		t.logf("The game starts: %s chips each, blinds %s/%s", chips(t.Settings.StartingStack), chips(lv.SmallBlind), chips(lv.BigBlind))
	}
	t.startHand(now)
	return nil
}

func (t *Table) updateSettings(p *Player, raw json.RawMessage) error {
	if t.Status != StatusLobby {
		return errors.New("settings can only change in the lobby")
	}
	if p != nil && p.ID != t.HostID { // nil: TableHost
		return errors.New("only the host can change the settings")
	}
	next := t.Settings
	if len(raw) == 0 || json.Unmarshal(raw, &next) != nil {
		return errors.New("invalid settings")
	}
	if err := next.Validate(t.SeatLimit); err != nil {
		return err
	}
	if next.MaxPlayers < t.seated() {
		return errors.New("more players are seated than that")
	}
	t.Settings = next
	for _, q := range t.Players {
		q.Stack, q.BoughtIn = next.StartingStack, next.StartingStack
		q.TimeBank = next.TimeBankSeconds
	}
	return nil
}

func (t *Table) bet(p *Player, a Action, now int64) error {
	if t.Phase != PhaseBetting || t.Hand == nil {
		return errors.New("there is no betting right now")
	}
	i := t.handIndex(p.ID)
	if i < 0 || t.Hand.Actor() != i {
		return ErrNotYourTurn
	}
	used := t.bankUsed(now)
	if err := t.applyHandAction(i, a, now); err != nil {
		return err
	}
	p.TimeBank = max(0, p.TimeBank-used)
	p.Timeouts = 0
	return nil
}

// bankUsed is how many whole seconds (rounded up) of the actor's time bank
// have run by now; 0 while their ordinary turn timer is still running.
func (t *Table) bankUsed(now int64) int {
	if !t.OnBank || now <= t.BankFrom {
		return 0
	}
	return int((now - t.BankFrom + 999) / 1000)
}

// clearClock stops the turn timer and the time bank.
func (t *Table) clearClock() {
	t.Deadline, t.OnBank, t.BankFrom = 0, false, 0
}

// applyHandAction runs a decision through the hand and records it.
func (t *Table) applyHandAction(i int, a Action, now int64) error {
	h := t.Hand
	before := len(h.Log)
	street := h.Street
	if err := h.Act(a); err != nil {
		return err
	}
	t.recordHandLog(before, street)
	t.afterHandChange(now)
	return nil
}

// recordHandLog turns new hand-log entries into log lines and events.
func (t *Table) recordHandLog(from int, _ Street) {
	h := t.Hand
	for _, r := range h.Log[from:] {
		id := t.HandIDs[r.Player]
		n := t.name(id)
		ev := Event{Type: EvAction, PlayerID: id, Action: string(r.Type), Amount: r.Amount, To: r.To, AllIn: r.AllIn, Street: r.Street.String()}
		switch r.Type {
		case ActAnte, ActSmallBlind, ActBigBlind:
			ev.Type = EvPost
		}
		t.emit(ev)
		allIn := ""
		if r.AllIn {
			allIn = " and is all-in"
		}
		switch r.Type {
		case ActFold:
			if !r.Forced {
				t.logf("%s folds", n)
			}
		case ActCheck:
			t.logf("%s checks", n)
		case ActCall:
			t.logf("%s calls %s%s", n, chips(r.Amount), allIn)
		case ActBet:
			t.logf("%s bets %s%s", n, chips(r.To), allIn)
		case ActRaise:
			t.logf("%s raises to %s%s", n, chips(r.To), allIn)
		case ActSmallBlind:
			t.logf("%s posts the small blind %s%s", n, chips(r.Amount), allIn)
		case ActBigBlind:
			t.logf("%s posts the big blind %s%s", n, chips(r.Amount), allIn)
		}
	}
}

// afterHandChange updates the phase after the hand moved on: new streets,
// run-outs, the end of the hand and the shot clock.
func (t *Table) afterHandChange(now int64) {
	h := t.Hand
	if h == nil || t.Status != StatusPlaying {
		return
	}
	t.announceBoard()
	if h.Done {
		t.Odds, t.OddsBoard = nil, 0
		if t.Phase == PhaseBetting || t.Phase == PhaseRunout {
			t.finishHand(now)
		}
		return
	}
	if h.NeedsDeal() {
		if t.Phase != PhaseRunout {
			t.Phase = PhaseRunout
			t.clearClock()
			t.revealRunout()
			t.RunoutAt = now + int64(t.Timing.RunoutStepMs)
		}
		if t.Odds == nil || t.OddsBoard != len(h.Board) {
			// Win chances for the TV-style display; every live hand is face
			// up, so they reveal nothing. Deterministic per hand and street.
			t.Odds = h.Odds(uint64(t.HandNumber)<<8 | uint64(len(h.Board)))
			t.OddsBoard = len(h.Board)
		}
		return
	}
	t.Odds, t.OddsBoard = nil, 0
	t.Phase = PhaseBetting
	t.RunoutAt = 0
	t.clearClock()
	if t.Settings.ActionSeconds > 0 {
		t.Deadline = now + int64(t.Settings.ActionSeconds)*1000
	}
}

// boardShown tracks how many board cards have been announced.
func (t *Table) announceBoard() {
	h := t.Hand
	announced := 0
	for _, e := range t.Events {
		if e.Type == EvDeal && e.HandNumber == t.HandNumber {
			announced += len(e.Cards)
		}
	}
	for announced < len(h.Board) {
		var cards []Card
		street := "flop"
		switch announced {
		case 0:
			cards = h.Board[0:3]
		case 3:
			cards, street = h.Board[3:4], "turn"
		default:
			cards, street = h.Board[4:5], "river"
		}
		t.emit(Event{Type: EvDeal, Street: street, Cards: cardStrings(cards)})
		t.logf("%s: %s", strings.ToUpper(street[:1])+street[1:], strings.Join(cardStrings(cards), " "))
		announced += len(cards)
	}
}

// revealRunout tables the hands of everyone still in when betting is over.
func (t *Table) revealRunout() {
	h := t.Hand
	for i := 0; i < h.N; i++ {
		if !h.Folded[i] && h.Shown[i] {
			id := t.HandIDs[i]
			t.emit(Event{Type: EvReveal, PlayerID: id, Cards: cardStrings(h.Hole[i])})
			t.logf("%s shows %s", t.name(id), strings.Join(cardStrings(h.Hole[i]), " "))
		}
	}
}

func (t *Table) finishHand(now int64) {
	h := t.Hand
	res := h.Result
	t.clearClock()
	t.RunoutAt = 0
	for _, u := range h.Uncalled {
		id := t.HandIDs[u.Player]
		t.emit(Event{Type: EvUncalled, PlayerID: id, Amount: u.Amount})
		t.logf("%s takes back %s uncalled", t.name(id), chips(u.Amount))
	}
	if res.Showdown {
		// Reveal in show order (riverboat's reveal logic, see hand.go).
		live := []int{}
		for i := 0; i < h.N; i++ {
			if !h.Folded[i] {
				live = append(live, i)
			}
		}
		start := 0
		for k, i := range live {
			if i == res.FirstToShow {
				start = k
			}
		}
		alreadyShown := t.Phase == PhaseRunout
		for k := range live {
			i := live[(start+k)%len(live)]
			id := t.HandIDs[i]
			if h.Shown[i] {
				if !alreadyShown {
					t.emit(Event{Type: EvReveal, PlayerID: id, Cards: cardStrings(h.Hole[i]), HandName: res.Names[i]})
				}
				t.logf("%s has %s", t.name(id), res.Names[i])
			} else {
				t.emit(Event{Type: EvMuck, PlayerID: id})
				t.logf("%s mucks", t.name(id))
			}
		}
	}
	for k, pot := range res.Pots {
		label := ""
		if len(res.Pots) > 1 {
			if k == 0 {
				label = " from the main pot"
			} else {
				label = fmt.Sprintf(" from side pot %d", k)
			}
		}
		for j, w := range pot.Winners {
			id := t.HandIDs[w]
			name := ""
			if res.Showdown && h.Shown[w] {
				name = res.Names[w]
			}
			t.emit(Event{Type: EvWin, PlayerID: id, Amount: pot.Shares[j], Pot: k, HandName: name})
			with := ""
			if name != "" {
				with = " with " + name
			}
			t.logf("%s wins %s%s%s", t.name(id), chips(pot.Shares[j]), label, with)
		}
	}
	for i, id := range t.HandIDs {
		if p := t.Player(id); p != nil && !p.Left {
			p.Stack = h.Stacks[i]
		}
	}
	t.handleBusts(now)
	if res.Showdown {
		t.Phase = PhaseShowdown
		t.NextHandAt = now + int64(t.Timing.ShowdownMs+1500*max(0, len(res.Pots)-1))
	} else {
		t.Phase = PhaseHandOver
		t.NextHandAt = now + int64(t.Timing.HandOverMs)
	}
	t.checkGameOver(now)
}

func (t *Table) rebuyOpen() bool {
	s := t.Settings
	if s.Cash() {
		return true
	}
	return s.Rebuys > 0 && (s.RebuyLevels == 0 || t.LevelIndex < s.RebuyLevels)
}

// canRebuy reports whether a busted player may rebuy now.
func (t *Table) canRebuy(p *Player) bool {
	if !p.Busted || !p.active() {
		return false
	}
	return t.Settings.Cash() || (t.rebuyOpen() && p.Rebuys < t.Settings.Rebuys)
}

// handleBusts marks players who ran out of chips: they may rebuy, or they
// are out. In a cash game they may rebuy whenever they like (no deadline)
// and sit out until they do.
func (t *Table) handleBusts(now int64) {
	h := t.Hand
	var out []*Player
	for i, id := range t.HandIDs {
		p := t.Player(id)
		if p == nil || p.Left || p.Stack != 0 {
			continue
		}
		p.OutHand, p.OutStack = t.HandNumber, h.StartStacks[i]
		if t.Settings.Cash() {
			p.Busted, p.RebuyUntil = true, 0
			t.emit(Event{Type: EvBust, PlayerID: p.ID})
			t.logf("%s is out of chips and may rebuy", p.Name)
			continue
		}
		if t.rebuyOpen() && p.Rebuys < t.Settings.Rebuys {
			p.Busted = true
			p.RebuyUntil = now + int64(t.Timing.RebuyMs)
			t.emit(Event{Type: EvBust, PlayerID: p.ID})
			t.logf("%s is out of chips and may rebuy", p.Name)
		} else {
			t.OutSeq++
			p.Out, p.OutSeq = true, t.OutSeq
			out = append(out, p)
		}
	}
	t.rankEliminated()
	for _, p := range out {
		t.emit(Event{Type: EvBust, PlayerID: p.ID, Place: p.Place})
		t.logf("%s is out in %s place", p.Name, ordinal(p.Place))
	}
}

// rankEliminated places everyone who is out below the players still in:
// whoever lost their last chip later ranks higher; players busting in the
// same hand rank by the stack they started it with. A player who declines a
// rebuy is placed by the hand they busted in, not by when they declined.
// Cash games place everyone only at the end, by chips won (rankByNet).
func (t *Table) rankEliminated() {
	if t.Settings.Cash() {
		return
	}
	var gone []*Player
	for _, p := range t.Players {
		if !p.active() {
			gone = append(gone, p)
		}
	}
	sort.SliceStable(gone, func(a, b int) bool {
		x, y := gone[a], gone[b]
		if x.OutHand != y.OutHand {
			return x.OutHand > y.OutHand
		}
		if x.OutStack != y.OutStack {
			return x.OutStack > y.OutStack
		}
		return x.OutSeq > y.OutSeq
	})
	base := t.contenders()
	for k, p := range gone {
		p.Place = base + 1 + k
	}
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

// eliminate ends a busted player's game (rebuy declined or expired).
func (t *Table) eliminate(p *Player, now int64) {
	if !p.Busted {
		return
	}
	p.Busted = false
	t.OutSeq++
	p.Out, p.OutSeq = true, t.OutSeq
	t.rankEliminated()
	t.emit(Event{Type: EvBust, PlayerID: p.ID, Place: p.Place})
	if p.Place > 0 {
		t.logf("%s is out in %s place", p.Name, ordinal(p.Place))
	} else {
		t.logf("%s is out of the game", p.Name)
	}
	t.maybeResume(now)
}

// checkGameOver finishes a tournament when one player has all the chips. A
// cash game runs until the host ends it, or until fewer than two players are
// left in it (everyone else left or declined to rebuy).
func (t *Table) checkGameOver(now int64) {
	if t.Status != StatusPlaying {
		return
	}
	if t.Hand != nil && !t.Hand.Done {
		return
	}
	if t.Settings.Cash() {
		if t.contenders() < 2 {
			t.finishGame(t.rankByNet(), now)
		}
		return
	}
	withChips, pending := []*Player{}, 0
	for _, p := range t.Players {
		if !p.active() {
			continue
		}
		if p.Busted {
			pending++
		} else if p.Stack > 0 {
			withChips = append(withChips, p)
		}
	}
	if pending > 0 || len(withChips) > 1 {
		return
	}
	winner := ""
	if len(withChips) == 1 {
		winner = withChips[0].ID
		withChips[0].Place = 1
	}
	t.finishGame(winner, now)
}

func (t *Table) finishGame(winner string, now int64) {
	t.Status = StatusFinished
	t.Phase = PhaseFinished
	t.WinnerID = winner
	t.FinishedAt = now
	t.clearClock()
	t.RunoutAt, t.NextHandAt = 0, 0
	t.emit(Event{Type: EvGameOver, PlayerID: winner})
	if winner != "" {
		t.logf("%s wins the game!", t.name(winner))
	} else {
		t.logf("The game is over")
	}
}

// EndGame stops a running game now (host "end game"). A hand in progress is
// called off and every chip goes back; players are ranked by chips.
func (t *Table) EndGame() (string, error) {
	if t.Status != StatusPlaying {
		return "", errors.New("the game is not running")
	}
	now := t.Now()
	if h := t.Hand; h != nil && !h.Done {
		for i, id := range t.HandIDs {
			if p := t.Player(id); p != nil && !p.Left {
				p.Stack = h.StartStacks[i]
			}
		}
		t.logf("Hand #%d is called off", t.HandNumber)
		h.Done = true
		h.Actors = nil
	}
	var in []*Player
	for _, p := range t.Players {
		if p.active() {
			p.Busted = false
			in = append(in, p)
		}
	}
	winner := ""
	if t.Settings.Cash() {
		winner = t.rankByNet()
	} else {
		sort.SliceStable(in, func(a, b int) bool { return in[a].Stack > in[b].Stack })
		for k, p := range in {
			p.Place = k + 1
			if k > 0 && p.Stack == in[k-1].Stack {
				p.Place = in[k-1].Place
			}
		}
		if len(in) > 0 && (len(in) == 1 || in[0].Stack > in[1].Stack) {
			winner = in[0].ID
		}
	}
	t.finishGame(winner, now)
	t.Version++
	return winner, nil
}

// Net is a player's chips won so far: their stack minus every chip they
// bought (negative when they are down).
func (p *Player) Net() int { return p.Stack - p.BoughtIn }

// rankByNet places every player who sat down, including those who left or
// gave up, by chips won (a cash game's result); equal results share a place.
// It returns the winner's id, or "" when the best result is shared.
func (t *Table) rankByNet() string {
	all := append([]*Player(nil), t.Players...)
	sort.SliceStable(all, func(a, b int) bool { return all[a].Net() > all[b].Net() })
	for k, p := range all {
		p.Place = k + 1
		if k > 0 && p.Net() == all[k-1].Net() {
			p.Place = all[k-1].Place
		}
	}
	if len(all) == 1 || (len(all) > 1 && all[0].Net() > all[1].Net()) {
		return all[0].ID
	}
	return ""
}

func (t *Table) show(p *Player) error {
	h := t.Hand
	i := t.handIndex(p.ID)
	if h == nil || !h.Done || i < 0 || (t.Phase != PhaseShowdown && t.Phase != PhaseHandOver && t.Phase != PhasePaused && t.Phase != PhaseFinished) {
		return errors.New("you can show your cards once the hand is over")
	}
	if h.Shown[i] {
		return errors.New("your cards are already face up")
	}
	h.Show(i)
	name := ""
	if len(h.Board) >= 3 {
		_, _, name = DescribeCards(append([]Card{h.Hole[i][0], h.Hole[i][1]}, h.Board...))
	}
	t.emit(Event{Type: EvReveal, PlayerID: p.ID, Cards: cardStrings(h.Hole[i]), HandName: name})
	t.logf("%s shows %s", p.Name, strings.Join(cardStrings(h.Hole[i]), " "))
	return nil
}

func (t *Table) sitOut(p *Player) error {
	if t.Status == StatusFinished || p.Out {
		return errors.New("you are not in the game")
	}
	if p.SittingOut || p.SitOutNext {
		return errors.New("you are already sitting out")
	}
	inHand := t.handIndex(p.ID) >= 0 && t.Hand != nil && !t.Hand.Done
	if inHand || t.Status == StatusLobby {
		p.SitOutNext = true
	} else {
		p.SittingOut = true
	}
	t.emit(Event{Type: EvSitOut, PlayerID: p.ID})
	t.logf("%s sits out", p.Name)
	return nil
}

func (t *Table) sitIn(p *Player, now int64) error {
	if !p.SittingOut && !p.SitOutNext {
		return errors.New("you are not sitting out")
	}
	p.SittingOut, p.SitOutNext = false, false
	p.Timeouts = 0
	t.emit(Event{Type: EvSitIn, PlayerID: p.ID})
	t.logf("%s is back", p.Name)
	t.maybeResume(now)
	return nil
}

func (t *Table) rebuy(p *Player, now int64) error {
	if t.Status != StatusPlaying {
		return errors.New("the game is not running")
	}
	if !p.Busted {
		return errors.New("you can only rebuy when you are out of chips")
	}
	if !t.canRebuy(p) {
		return errors.New("rebuys are closed")
	}
	p.Busted, p.RebuyUntil = false, 0
	p.Rebuys++
	p.Stack = t.Settings.StartingStack
	p.BoughtIn += t.Settings.StartingStack
	t.emit(Event{Type: EvRebuy, PlayerID: p.ID, Amount: p.Stack})
	t.logf("%s rebuys for %s", p.Name, chips(p.Stack))
	t.maybeResume(now)
	return nil
}

func (t *Table) dealNow(p *Player, now int64) error {
	if p != nil && p.ID != t.HostID { // nil: TableHost
		return errors.New("only the host can deal the next hand")
	}
	if t.Status != StatusPlaying || (t.Phase != PhaseShowdown && t.Phase != PhaseHandOver && t.Phase != PhasePaused) {
		return errors.New("a hand is in progress")
	}
	t.NextHandAt = now
	t.startHand(now)
	return nil
}

// maybeResume deals when a paused table can play again.
func (t *Table) maybeResume(now int64) {
	if t.Status == StatusPlaying && t.Phase == PhasePaused && now >= t.NextHandAt {
		t.startHand(now)
	}
}

// ---------------------------------------------------------------- hands

func (t *Table) eligible(p *Player) bool {
	return p.active() && !p.Busted && !p.SittingOut && p.Stack > 0
}

// startHand deals the next hand, or pauses if fewer than two can play.
func (t *Table) startHand(now int64) {
	if t.Status != StatusPlaying {
		return
	}
	// Blind levels change between hands.
	for t.LevelEndsAt > 0 && now >= t.LevelEndsAt && t.LevelIndex < len(t.Levels)-1 {
		t.LevelIndex++
		lv := t.Levels[t.LevelIndex]
		if lv.Minutes > 0 {
			t.LevelEndsAt += int64(lv.Minutes) * 60_000
		} else {
			t.LevelEndsAt = 0
		}
		lvCopy := lv
		t.emit(Event{Type: EvLevelUp, Level: &lvCopy})
		ante := ""
		if lv.Ante > 0 {
			ante = fmt.Sprintf(", ante %s", chips(lv.Ante))
		}
		t.logf("Blinds are up: %s/%s%s", chips(lv.SmallBlind), chips(lv.BigBlind), ante)
	}
	for _, p := range t.Players {
		if p.SitOutNext {
			p.SitOutNext = false
			p.SittingOut = true
		}
	}
	var seats []*Player
	for _, p := range t.Players {
		if t.eligible(p) {
			seats = append(seats, p)
		}
	}
	if len(seats) < 2 {
		t.Phase = PhasePaused
		t.clearClock()
		t.RunoutAt = 0
		t.checkGameOver(now)
		return
	}
	sort.Slice(seats, func(a, b int) bool { return seats[a].Seat < seats[b].Seat })
	// The button moves to the next player clockwise.
	btn := 0
	if t.ButtonSeat < 0 {
		btn = t.rng.IntN(len(seats))
	} else {
		btn = -1
		for k, p := range seats {
			if p.Seat > t.ButtonSeat {
				btn = k
				break
			}
		}
		if btn < 0 {
			btn = 0
		}
	}
	t.ButtonSeat = seats[btn].Seat
	order := make([]*Player, 0, len(seats))
	for k := 1; k <= len(seats); k++ {
		order = append(order, seats[(btn+k)%len(seats)])
	}
	lv := t.Level()
	stacks := make([]int, len(order))
	t.HandIDs = make([]string, len(order))
	for i, p := range order {
		stacks[i] = p.Stack
		t.HandIDs[i] = p.ID
		// The time bank refills a little for every TimeBankRefillHands hands
		// dealt in.
		if p.BankHands++; p.BankHands >= TimeBankRefillHands {
			p.BankHands = 0
			p.TimeBank = min(p.TimeBank+TimeBankRefill, t.Settings.TimeBankSeconds)
		}
	}
	h, err := NewHand(HandConfig{
		SmallBlind:  lv.SmallBlind,
		BigBlind:    lv.BigBlind,
		Ante:        lv.Ante,
		Stacks:      stacks,
		Deck:        ShuffledDeck(t.rng),
		PauseRunout: true,
	})
	if err != nil {
		panic(err) // inputs are validated above
	}
	t.Hand = h
	t.HandNumber++
	t.NextHandAt = 0
	t.emit(Event{Type: EvHandStart, PlayerID: seats[btn].ID})
	t.logf("Hand #%d: %s has the button", t.HandNumber, seats[btn].Name)
	if lv.Ante > 0 {
		t.logf("Everyone antes %s", chips(lv.Ante))
	}
	t.Phase = PhaseBetting
	t.recordHandLog(0, Preflop)
	t.afterHandChange(now)
}

// ---------------------------------------------------------------- clock

// Advance runs everything that is due at now: shot-clock timeouts, run-out
// streets, the next hand and rebuy deadlines. It reports whether anything
// changed (callers then publish the new state).
func (t *Table) Advance(now int64) bool {
	before := t.Version
	t.advance(now)
	return t.Version != before
}

func (t *Table) advance(now int64) {
	for guard := 0; guard < 1000 && t.Status == StatusPlaying; guard++ {
		if !t.step(now) {
			return
		}
		t.Version++
	}
}

// step performs one due transition.
func (t *Table) step(now int64) bool {
	for _, p := range t.Players {
		if p.Busted && p.active() && p.RebuyUntil > 0 && now >= p.RebuyUntil {
			t.eliminate(p, now)
			t.checkGameOver(now)
			return true
		}
	}
	switch t.Phase {
	case PhaseBetting:
		if t.Deadline > 0 && now >= t.Deadline {
			i := t.Hand.Actor()
			p := t.Player(t.HandIDs[i])
			if !t.OnBank && p.TimeBank > 0 {
				// The turn timer ran out: the time bank runs from that moment.
				t.OnBank, t.BankFrom = true, t.Deadline
				t.Deadline += int64(p.TimeBank) * 1000
				t.emit(Event{Type: EvTimeBank, PlayerID: p.ID, Amount: p.TimeBank})
				return true
			}
			if t.OnBank {
				p.TimeBank = 0
			}
			a := Action{Type: ActFold}
			if t.Hand.Legal().CanCheck {
				a.Type = ActCheck
			}
			t.logf("%s ran out of time", p.Name)
			p.Timeouts++
			if p.Timeouts >= 2 && !p.SitOutNext {
				p.SitOutNext = true
				t.logf("%s will sit out", p.Name)
			}
			if err := t.applyHandAction(i, a, now); err != nil {
				panic(err)
			}
			return true
		}
	case PhaseRunout:
		if now >= t.RunoutAt {
			t.Hand.DealNext()
			t.RunoutAt = now + int64(t.Timing.RunoutStepMs)
			t.afterHandChange(now)
			return true
		}
	case PhaseShowdown, PhaseHandOver:
		if now >= t.NextHandAt {
			t.startHand(now)
			return true
		}
	}
	return false
}

// NextDeadline is the earliest time Advance has work to do, or 0.
func (t *Table) NextDeadline() int64 {
	if t.Status != StatusPlaying {
		return 0
	}
	var next int64
	consider := func(ts int64) {
		if ts > 0 && (next == 0 || ts < next) {
			next = ts
		}
	}
	for _, p := range t.Players {
		if p.Busted && p.active() {
			consider(p.RebuyUntil)
		}
	}
	switch t.Phase {
	case PhaseBetting:
		consider(t.Deadline)
	case PhaseRunout:
		consider(t.RunoutAt)
	case PhaseShowdown, PhaseHandOver:
		consider(t.NextHandAt)
	}
	return next
}

// ---------------------------------------------------------------- helpers for servers and bots

// Pending is a decision the table is waiting for.
type Pending struct {
	PlayerID string `json:"playerId"`
	Kind     string `json:"kind"` // "act" (bet/fold) or "rebuy"
	Deadline int64  `json:"deadline,omitempty"`
}

// Waiting lists who the table waits on: the player to act, and busted
// players who may rebuy (in a cash game with no deadline: Deadline 0).
func (t *Table) Waiting() []Pending {
	var out []Pending
	if t.Status != StatusPlaying {
		return out
	}
	if t.Phase == PhaseBetting && t.Hand != nil {
		if i := t.Hand.Actor(); i >= 0 {
			out = append(out, Pending{PlayerID: t.HandIDs[i], Kind: "act", Deadline: t.Deadline})
		}
	}
	now := t.Now()
	for _, p := range t.Players {
		if t.canRebuy(p) && (t.Settings.Cash() || now < p.RebuyUntil) {
			out = append(out, Pending{PlayerID: p.ID, Kind: "rebuy", Deadline: p.RebuyUntil})
		}
	}
	return out
}

// SafeAction is what a stand-in plays for an absent player: check if that
// is free, otherwise fold. It never decides a rebuy (the offer simply runs
// out). Nil when the player has no betting decision pending.
func (t *Table) SafeAction(playerID string) json.RawMessage {
	for _, w := range t.Waiting() {
		if w.PlayerID != playerID || w.Kind != "act" {
			continue
		}
		if t.Hand.Legal().CanCheck {
			return json.RawMessage(`{"type":"check"}`)
		}
		return json.RawMessage(`{"type":"fold"}`)
	}
	return nil
}

// ResetStacks sets every player's stack between hands. It exists for
// simulations (cmd/arena measures bb/100 at a fixed depth); games never
// call it.
func (t *Table) ResetStacks(stack int) error {
	if t.Hand != nil && !t.Hand.Done {
		return errors.New("a hand is in progress")
	}
	for _, p := range t.Players {
		if p.active() {
			p.Stack = stack
			p.Busted = false
		}
	}
	return nil
}
