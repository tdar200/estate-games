// Package server is the Estate HTTP/WebSocket server: the Node-compatible
// room API (docs/API.md §1), party mode (tables, phones, devices, friends,
// invites, §2), live WebSocket updates and the embedded static apps.
//
// All mutable state (rooms, sessions, devices) is guarded by one mutex,
// Server.mu, mirroring the single-threaded Node implementation. Rules are
// delegated to an Engine and computer players to an AI (see engine.go).
package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"io"
	"io/fs"
	"log"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/tdar200/estate-games/internal/engine"
)

// Limits. The server is reachable on a public link, so everything a stranger
// can create is capped (see also ratelimit.go and claims.go).
const (
	roomTTL          = 6 * time.Hour    // since a person (not a computer) last used the room
	emptyRoomTTL     = 30 * time.Minute // rooms nobody ever sat down in
	maxRooms         = 100              // a friends server (Node allowed 500)
	maxRoomsPerIP    = 6                // live rooms one address may have made (loopback exempt)
	maxRoomsPerDev   = 3                // live rooms one device may have made
	maxSpectators    = 24
	maxRoomSessions  = 64 // sessions per room: players, spectators, the table
	spectatorWindow  = 60 * time.Second
	staleSession     = 10 * time.Minute // unused spectator and duplicate sessions are dropped after this
	onlineWindow     = 20 * time.Second
	botIdleGrace     = 2 * time.Minute // computers stop when no person has been connected this long
	defaultAIDelay   = 520 * time.Millisecond
	maxAIFailures    = 10
	aiRetryDelay     = 15 * time.Second // pause after maxAIFailures rejected computer moves in a row, or a decision that gave no move
	maxThinking      = 2                // computer decisions computed at once (each Hard one uses every core)
	sweepInterval    = 5 * time.Second
	roomAlphabet     = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	maxPlayersInGame = engine.MaxPlayers
	maxSocketsPerIP  = 40
)

// aiNames are the computer players' names, one for each of the eight seats, in the order they
// are added. Seven letters at most, so they fit the TV's action card and plates whole (review
// 4 Nov #9: the eighth used to be "AI 7", and "Blue Whale" / "North Star" were cut short).
var aiNames = []string{"Alpha", "Orca", "Redwood", "Kestrel", "Skylark", "Juniper", "Harbour", "Birch"}

// freeAIName is the name the next computer seat takes, given the names at the
// table: the first of aiNames nobody has, else the first free "Alpha 2",
// "Orca 2", ... (people use some of the names).
func freeAIName(used map[string]bool) string {
	for _, candidate := range aiNames {
		if !used[candidate] {
			return candidate
		}
	}
	for _, candidate := range aiNames {
		if !used[candidate+" 2"] {
			return candidate + " 2"
		}
	}
	return ""
}

// tokenKinds are the eight player tokens, in default assignment order.
var tokenKinds = []string{"rocket", "teapot", "bicycle", "lighthouse", "key", "umbrella", "anchor", "kite"}

// retiredTokenKinds maps a token kind that is no longer handed out to the one
// that replaced it. Saved devices, rooms and game records may still name it,
// so it stays valid and is stored as its replacement. None are retired now.
var retiredTokenKinds = map[string]string{}

// canonicalTokenKind is kind, or the token that replaced a retired kind.
func canonicalTokenKind(kind string) string {
	if replacement, ok := retiredTokenKinds[kind]; ok {
		return replacement
	}
	return kind
}

func validTokenKind(kind string) bool {
	kind = canonicalTokenKind(kind)
	for _, k := range tokenKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Config configures a Server.
type Config struct {
	// Engine runs Estate, the default game type.
	Engine Engine
	// Engines adds game types by name, e.g. {"poker": PokerEngine{}, "ludo":
	// LudoEngine{}, "quiz": QuizEngine{}, "closest": ClosestEngine{}}. Rooms pick one with gameType when
	// they are created (gameinfo.go).
	Engines map[string]Engine
	// PokerBots supplies poker computer seats by difficulty; nil uses the
	// built-in Easy and Normal bots (RealPokerBots).
	PokerBots PokerBotFactory
	// PokerLevels are the computer levels poker rooms offer (state.aiLevels);
	// nil means easy and normal.
	PokerLevels []string
	// AI plays computer seats and prices trades; nil uses the built-in SimpleAI.
	AI AI
	// Bots optionally supplies a bot per AI seat by difficulty; nil means
	// every seat uses AI.
	Bots BotFactory
	// Assets holds the static apps: web/ and public/ at its root. Nil serves
	// no static files.
	Assets fs.FS
	// DataDir stores devices and friendships (estate.json). Empty disables
	// persistence.
	DataDir string
	// PublicURL (e.g. the Tailscale Funnel URL) and LANURL are reported by
	// /api/server and used for QR codes.
	PublicURL string
	LANURL    string
	Version   string
	Logger    *log.Logger
	// Clock overrides time.Now (tests).
	Clock func() time.Time
	// AIDelay is the pause before each computer move (default 520 ms).
	AIDelay time.Duration
	// AutopilotDelay is how long the game waits on an away player before the
	// computer plays for them (default 30 s; see standin.go).
	AutopilotDelay time.Duration
	// SaveDelay debounces persistence writes (default 1 s).
	SaveDelay time.Duration

	// recordKeep and recordMaxBytes cap the finished game records kept in
	// <data>/games (defaults 500 records and 200 MB, whichever is reached
	// first; tests lower them).
	recordKeep     int
	recordMaxBytes int64

	// pokerTick caps how long a poker, quiz or closest room's timer sleeps (default 2 s;
	// tests shorten it to follow a fake clock).
	pokerTick time.Duration

	// ludoPace scales Ludo's waits for the screens (dice, hops, a person's
	// forced move; default 1; tests shrink it to play whole games quickly).
	ludoPace float64
}

// Server implements http.Handler.
type Server struct {
	cfg     Config
	log     *log.Logger
	ai      AI
	aiDelay time.Duration

	autopilotDelay time.Duration
	engines        map[string]Engine // extra game types (Config.Engines)
	pokerLevels    []string
	pokerTick      time.Duration
	ludoPace       float64
	static         *staticFiles
	palette        map[string]string // token kind → display colour (see palette.go)
	handler        http.Handler

	limiter       *rateLimiter
	inviteLimiter *rateLimiter
	wsLimiter     *rateLimiter
	deviceLimiter *rateLimiter // new device records per address

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	thinkSlots chan struct{} // caps concurrent computer decisions (maxThinking)

	mu           sync.Mutex
	closed       bool
	rooms        map[string]*room
	devices      map[string]*deviceRecord
	deviceConns  map[string]map[*wsConn]struct{}
	friendSig    map[string]string
	store        *persister
	roomStore    *persister      // saved games (roomstore.go); nil without a data dir
	records      *recordStore    // game records (recorder.go); nil without a data dir
	recordClaims map[string]bool // journals reattached to restored rooms (startup)
	sockets      map[string]int  // open WebSockets per client address (maxSocketsPerIP)

	// reqIP is the client address of the API request being dispatched (set
	// and read only under mu, inside dispatch).
	reqIP string
}

// New creates a Server and loads persisted device data.
func New(cfg Config) (*Server, error) {
	if cfg.Engine == nil {
		panic("server: Config.Engine is required")
	}
	s := &Server{
		cfg:           cfg,
		log:           cfg.Logger,
		ai:            cfg.AI,
		aiDelay:       cfg.AIDelay,
		limiter:       newRateLimiter(),
		inviteLimiter: newRateLimiter(),
		wsLimiter:     newRateLimiter(),
		deviceLimiter: newRateLimiter(),
		sockets:       map[string]int{},
		rooms:         map[string]*room{},
		devices:       map[string]*deviceRecord{},
		deviceConns:   map[string]map[*wsConn]struct{}{},
		friendSig:     map[string]string{},
		palette:       loadPalette(cfg.Assets),
		thinkSlots:    make(chan struct{}, maxThinking),
	}
	if s.log == nil {
		s.log = log.New(io.Discard, "", 0)
	}
	if s.ai == nil {
		s.ai = &SimpleAI{}
	}
	if s.aiDelay <= 0 {
		s.aiDelay = defaultAIDelay
	}
	s.autopilotDelay = cfg.AutopilotDelay
	if s.autopilotDelay <= 0 {
		s.autopilotDelay = defaultAutopilotDelay
	}
	s.engines = map[string]Engine{}
	for name, e := range cfg.Engines {
		if e == nil || name == "" || name == GameEstate {
			continue
		}
		if pe, ok := e.(PokerEngine); ok && pe.Clock == nil {
			pe.Clock = func() int64 { return s.now().UnixMilli() } // poker timers follow the server clock
			e = pe
		}
		if le, ok := e.(LudoEngine); ok && le.Clock == nil {
			le.Clock = func() int64 { return s.now().UnixMilli() } // Ludo's clock (state.now) too
			e = le
		}
		if qe, ok := e.(QuizEngine); ok && qe.Clock == nil {
			qe.Clock = func() int64 { return s.now().UnixMilli() } // the quiz's timers and every tap's arrival too
			e = qe
		}
		if ce, ok := e.(ClosestEngine); ok && ce.Clock == nil {
			ce.Clock = func() int64 { return s.now().UnixMilli() } // Near Thing's timers too
			e = ce
		}
		s.engines[name] = e
	}
	s.pokerLevels = cfg.PokerLevels
	if len(s.pokerLevels) == 0 {
		s.pokerLevels = []string{"easy", "normal"}
	}
	s.ludoPace = cfg.ludoPace
	if s.ludoPace <= 0 {
		s.ludoPace = 1
	}
	s.pokerTick = cfg.pokerTick
	if s.pokerTick <= 0 {
		s.pokerTick = defaultPokerTick
	}
	if cfg.Version == "" {
		s.cfg.Version = "dev"
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	if cfg.DataDir != "" {
		delay := cfg.SaveDelay
		if delay <= 0 {
			delay = time.Second
		}
		store, devices, err := openPersister(cfg.DataDir, delay, s.log)
		if err != nil {
			return nil, err
		}
		s.store = store
		s.devices = devices
		records, err := openRecordStore(cfg.DataDir, s.log, cfg.recordKeep, cfg.recordMaxBytes)
		if err != nil {
			return nil, err
		}
		s.records = records
		s.recordClaims = map[string]bool{}
		s.openRoomStore(cfg.DataDir, delay)
		s.mu.Lock()
		s.finishOrphanRecords(s.recordClaims)
		s.recordClaims = nil
		s.mu.Unlock()
	}
	if cfg.Assets != nil {
		static, err := newStaticFiles(cfg.Assets)
		if err != nil {
			if s.records != nil {
				s.records.close()
			}
			return nil, err
		}
		s.static = static
	}
	s.handler = s.routes()
	s.wg.Add(1)
	go s.sweepLoop()
	return s, nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// Close stops timers, closes every WebSocket and flushes persisted data.
// http.Server.Shutdown does not track hijacked WebSocket connections, so
// call Close after (or alongside) it.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	now := s.now()
	for _, r := range s.rooms {
		r.stopTimer()
		if s.personConnected(r) {
			r.updatedAt = now // open screens keep a game alive, even if nobody moved
		}
	}
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
	if s.records != nil {
		// Steps still queued reach the journals first, so a game saved in
		// rooms.json is never ahead of its record.
		s.records.close()
	}
	var roomErr error
	if s.roomStore != nil {
		// Always save on shutdown, so the restore sees fresh activity times.
		s.roomStore.markDirty(s.snapshotRooms)
		roomErr = s.roomStore.flush(s.snapshotRooms)
	}
	if s.store != nil {
		if err := s.store.flush(s.snapshotDevices); err != nil {
			return err
		}
	}
	return roomErr
}

func (s *Server) now() time.Time {
	if s.cfg.Clock != nil {
		return s.cfg.Clock()
	}
	return time.Now()
}

func (s *Server) sweepLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	lastPrune := s.now()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		if s.sweepOnce(&lastPrune) {
			return
		}
		now := s.now()
		for _, l := range []*rateLimiter{s.limiter, s.inviteLimiter, s.wsLimiter, s.deviceLimiter} {
			l.prune(now)
		}
	}
}

// sweepOnce is one pass of the sweep; it reports true once the server is
// closed. A crash in it is logged and the lock released (underLock), so the
// sweep, and the server, carry on.
func (s *Server) sweepOnce(lastPrune *time.Time) (closed bool) {
	s.underLock("the sweep", func() {
		if s.closed {
			closed = true
			return
		}
		now := s.now()
		if now.Sub(*lastPrune) >= time.Minute {
			*lastPrune = now
			s.pruneRooms(now)
		}
		for _, r := range s.rooms {
			s.sweepRoom(r, now)
		}
	})
	return closed
}

// sweepRoom refreshes one room's presence and requests, switches on idle
// stand-ins and wakes computers nothing is scheduled for. A crash here is
// confined to this room, so one broken game doesn't stop the sweep for the
// others. Requires s.mu.
func (s *Server) sweepRoom(r *room, now time.Time) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Printf("room %s: sweep crashed: %v\n%s", r.code, recovered, debug.Stack())
		}
	}()
	s.notePeople(r, now)
	s.expireClaims(r, now)
	if s.presenceSignature(r, now) != r.presenceSig {
		s.commit(r)
		s.presenceChanged(r)
	}
	if !r.isEstate() || r.info == nil || r.info.Status != "playing" || r.aiPaused {
		return
	}
	if s.switchOnStandIns(r, now, false) {
		s.commit(r) // publishes players[].autopilot
		s.scheduleAI(r)
	}
	if r.timer == nil && r.thinking == 0 && computerDue(r) {
		s.log.Printf("room %s: a computer move was due with nothing scheduled; starting it again", r.code)
		s.scheduleAI(r)
	}
}

// underLock runs fn holding s.mu and always releases it, even if fn panics:
// a bug in presence or state code must not leave every request waiting on
// the lock forever. The panic is logged and reported as false.
func (s *Server) underLock(what string, fn func()) (ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Printf("panic in %s: %v\n%s", what, recovered, debug.Stack())
			ok = false
		}
	}()
	fn()
	return true
}

// markDirty schedules a save of device data. Requires s.mu.
func (s *Server) markDirty() {
	if s.store != nil {
		s.store.markDirty(s.snapshotDevices)
	}
}

func randomToken(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// uid ports uid(prefix) from src/rooms.js.
func uid(prefix string) string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(buf)
}

// randomSeed returns a random non-negative int64.
func randomSeed() int64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return int64(binary.BigEndian.Uint64(b[:]) >> 1)
}

func randomIndex(n int) int {
	var b [1]byte
	limit := 256 - 256%n
	for {
		if _, err := rand.Read(b[:]); err != nil {
			panic(err)
		}
		if int(b[0]) < limit {
			return int(b[0]) % n
		}
	}
}
