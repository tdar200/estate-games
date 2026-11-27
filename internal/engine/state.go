package engine

import "encoding/json"

// Game statuses and phases (string values match game.js).
const (
	StatusLobby    = "lobby"
	StatusPlaying  = "playing"
	StatusFinished = "finished"

	PhaseLobby              = "lobby"
	PhaseDeterminingOrder   = "determining_order"
	PhaseOrderCountdown     = "order_countdown"
	PhaseAwaitingRoll       = "awaiting_roll"
	PhaseAwaitingPurchase   = "awaiting_purchase"
	PhaseAuction            = "auction"
	PhaseCardConfirmation   = "card_confirmation"
	PhaseTrade              = "trade"
	PhaseDebt               = "debt"
	PhaseTurnComplete       = "turn_complete"
	PhaseFinished           = "finished"
	KindHuman               = "human"
	KindAI                  = "ai"
	DiceModeRandom          = "random"
	DiceModeChoice          = "choice"
	OpeningStageRolling     = "rolling"
	OpeningStageCountdown   = "countdown"
	OpeningStageComplete    = "complete"
	StartingCash            = 1500
	MaxPlayers              = 8
	OpeningOrderCountdownMS = 3000
)

// Nullable string convention: every *ID field that JS can set to null is a
// plain Go string where "" means null. MarshalJSON methods below emit null
// for "" so the wire format matches the JS objects exactly.

// Game is the full authoritative game state (the object built by createGame).
// It marshals to the same JSON shape as the JS object.
type Game struct {
	Code                 string                           `json:"code"`
	Status               string                           `json:"status"`
	Phase                string                           `json:"phase"`
	HostID               string                           `json:"hostId"` // "" = null
	Players              []*Player                        `json:"players"`
	CurrentIndex         int                              `json:"currentIndex"`
	Properties           map[int]*PropertyState           `json:"properties"` // keyed by board index (ownable spaces only)
	Turn                 *Turn                            `json:"turn"`
	PendingPurchase      *int                             `json:"pendingPurchase"`
	Auction              *Auction                         `json:"auction"`
	Debt                 *Debt                            `json:"debt"`
	WinnerID             string                           `json:"winnerId"` // "" = null
	LastCard             *LastCard                        `json:"lastCard"`
	PendingCard          *PendingCard                     `json:"pendingCard"`
	LastLanding          *Landing                         `json:"lastLanding"`
	LastBankruptcy       *Bankruptcy                      `json:"lastBankruptcy"`
	LastMove             *Movement                        `json:"lastMove"`
	Trade                *Trade                           `json:"trade"`
	AITradeCooldowns     map[string]int                   `json:"aiTradeCooldowns"` // "proposer|target|goal" -> expiry turn count
	TurnCounts           map[string]int                   `json:"turnCounts"`       // player id -> completed turns
	OpeningOrder         *OpeningOrder                    `json:"openingOrder"`
	TurnSummary          *TurnSummary                     `json:"turnSummary"`
	TurnSummaryBaselines map[string]map[string]PlayerSpot `json:"turnSummaryBaselines"`
	Settings             Settings                         `json:"settings"`
	Decks                Decks                            `json:"decks"`
	Log                  []LogEntry                       `json:"log"`
	Version              int                              `json:"version"`

	// House-rule state (rules.go, loans.go). Omitted from JSON while unused,
	// so default games marshal exactly like the JS object.
	FreeParking int        `json:"freeParking,omitempty"` // City Park jackpot pot
	Loans       []*Loan    `json:"loans,omitempty"`       // every accepted loan, in creation order
	LoanOffer   *LoanOffer `json:"loanOffer,omitempty"`   // pending offer (phase loan_offer)

	// Clock returns the current time in Unix milliseconds (JS Date.now()).
	// Nil means DefaultClock.
	Clock func() int64 `json:"-"`
	// NewID returns a fresh id such as "player_1a2b3c4d5e6f" (JS uid(prefix)).
	// Nil means DefaultNewID.
	NewID func(prefix string) string `json:"-"`

	// rng is the RNG of the action being performed (PerformAction), for
	// rolls deep inside the rules such as the nearest-utility card.
	rng RNG

	// legacyRules replays game.js exactly, bugs included (see legacy.go).
	legacyRules bool
}

// Player is a seat in the game.
type Player struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // "human" or "ai"
	Color     string `json:"color"`
	Cash      int    `json:"cash"`
	Position  int    `json:"position"`
	InJail    bool   `json:"inJail"`
	JailTurns int    `json:"jailTurns"`
	JailCards int    `json:"jailCards"`
	Bankrupt  bool   `json:"bankrupt"`
	// Left is set when the host removed the player in play (remove_player):
	// they are out like a bankrupt player (Bankrupt is set too), their
	// assets went to the bank, and their seat is closed.
	Left bool `json:"left,omitempty"`
}

// PropertyState is the mutable state of an ownable space.
type PropertyState struct {
	OwnerID   string `json:"ownerId"` // "" = null (bank)
	Mortgaged bool   `json:"mortgaged"`
	Houses    int    `json:"houses"` // 5 = hotel
}

// Turn is the state of the current player's turn.
type Turn struct {
	PlayerID         string      `json:"playerId"`
	Dice             []int       `json:"dice"` // nil = null (not rolled yet)
	DoublesCount     int         `json:"doublesCount"`
	ExtraRoll        bool        `json:"extraRoll"`
	AITradeAttempted bool        `json:"aiTradeAttempted"`
	Movements        []*Movement `json:"movements"`
}

// Movement records one token movement (lastMove / turn.movements).
type Movement struct {
	ID       string `json:"id"`
	PlayerID string `json:"playerId"`
	From     int    `json:"from"`
	To       int    `json:"to"`
	Path     []int  `json:"path"`
	At       int64  `json:"at"`
}

// Auction is an open auction for a declined property.
type Auction struct {
	SpaceIndex  int      `json:"spaceIndex"`
	ActiveIDs   []string `json:"activeIds"`
	TurnPos     int      `json:"turnPos"`
	CurrentBid  int      `json:"currentBid"`
	BidderID    string   `json:"bidderId"` // "" = null
	ReturnPhase string   `json:"returnPhase"`

	// Timed open bidding (house rule auctionClock, auction.go): everyone
	// still in may bid at any moment. All omitted in a turn-order auction, so
	// games saved before the clock existed marshal as they did.
	ClockMs   int            `json:"clockMs,omitempty"`   // the clock's length; every bid restarts it
	EndsAt    int64          `json:"endsAt,omitempty"`    // Unix ms when the clock runs out
	Bids      map[string]int `json:"bids,omitempty"`      // player id -> their latest (highest) bid
	PassedIDs []string       `json:"passedIds,omitempty"` // who dropped out, in order
}

// Timed reports whether a is a timed open auction (nil: no auction).
func (a *Auction) Timed() bool { return a != nil && a.ClockMs > 0 }

// Debt kinds of DebtAfter.Kind.
const (
	AfterPhase     = "phase"
	AfterLeaveJail = "leaveJail"
	AfterJailMove  = "jailMove"
)

// DebtAfter says how play resumes once a charge is paid.
type DebtAfter struct {
	Kind  string `json:"kind"`
	Phase string `json:"phase,omitempty"` // kind "phase"
	Dice  []int  `json:"dice,omitempty"`  // kind "jailMove"
}

// Debt is an unpaid charge that the payer must raise cash for.
type Debt struct {
	PayerID    string     `json:"payerId"`
	Amount     int        `json:"amount"`
	CreditorID string     `json:"creditorId"` // "" = null (bank)
	Reason     string     `json:"reason"`
	After      *DebtAfter `json:"after,omitempty"`  // nil = undefined in JS
	LoanID     string     `json:"loanId,omitempty"` // set when the debt is an overdue loan
}

// LastCard is the most recently drawn card.
type LastCard struct {
	Deck string `json:"deck"`
	Text string `json:"text"`
}

// PendingCard is a drawn Headlines (deck "chance") card awaiting confirmation.
type PendingCard struct {
	PlayerID string `json:"playerId"`
	Deck     string `json:"deck"`
	CardID   string `json:"cardId"`
	Text     string `json:"text"`
}

// Landing describes the most recent landing (lastLanding).
type Landing struct {
	ID          string `json:"id"`
	PlayerID    string `json:"playerId"`
	SpaceIndex  int    `json:"spaceIndex"`
	Kind        string `json:"kind"`
	Amount      int    `json:"amount"`
	RecipientID string `json:"recipientId"` // "" = null
	At          int64  `json:"at"`
}

// Bankruptcy describes the most recent bankruptcy (lastBankruptcy).
type Bankruptcy struct {
	ID         string `json:"id"`
	PlayerID   string `json:"playerId"`
	CreditorID string `json:"creditorId"` // "" = null (bank)
	Properties []int  `json:"properties"`
	At         int64  `json:"at"`
}

// TradeBundle is one side of a trade.
type TradeBundle struct {
	Cash       int   `json:"cash"`
	Properties []int `json:"properties"`
}

// Trade is a pending trade proposal.
type Trade struct {
	ID          string      `json:"id"`
	ProposerID  string      `json:"proposerId"`
	TargetID    string      `json:"targetId"`
	Offer       TradeBundle `json:"offer"`
	Request     TradeBundle `json:"request"`
	ReturnPhase string      `json:"returnPhase"`
	CreatedAt   int64       `json:"createdAt"`

	// A counter-offer (counter_trade) answers the offer CounterTo, whose
	// terms were Original (its proposer is this trade's target). Round is
	// how many counters the chain has had.
	CounterTo string      `json:"counterTo,omitempty"`
	Original  *TradeTerms `json:"original,omitempty"`
	Round     int         `json:"counterRound,omitempty"`
}

// TradeTerms are the two sides of a trade offer, as its proposer made it.
type TradeTerms struct {
	Offer   TradeBundle `json:"offer"`
	Request TradeBundle `json:"request"`
}

// OpeningRoll is one roll made while determining turn order.
type OpeningRoll struct {
	Round int   `json:"round"`
	Dice  []int `json:"dice"`
	Total int   `json:"total"`
}

// OpeningEntry holds a player's turn-order rolls.
type OpeningEntry struct {
	PlayerID string        `json:"playerId"`
	Rolls    []OpeningRoll `json:"rolls"`
}

// OpeningOrder tracks the roll-for-turn-order stage.
type OpeningOrder struct {
	Stage           string          `json:"stage"`
	Round           int             `json:"round"`
	PendingIDs      []string        `json:"pendingIds"`
	Order           []string        `json:"order"`
	CountdownEndsAt *int64          `json:"countdownEndsAt"` // nil = null
	Entries         []*OpeningEntry `json:"entries"`

	// Estate additions (not in game.js) so screens never have to guess who
	// is rolling and why. RollingIDs is everyone rolling in the current round
	// (all players in round 1, then only the tied ones); unlike PendingIDs it
	// does not shrink as they roll, and it is empty once the order is set.
	// Ties lists every tie that caused a re-roll, oldest first.
	RollingIDs []string     `json:"rollingIds"`
	Ties       []OpeningTie `json:"ties"`
}

// OpeningTie is a group of players who rolled the same total in a round of
// the turn-order roll (with the same totals in earlier rounds) and so roll
// again.
type OpeningTie struct {
	Round     int      `json:"round"`     // the round they tied in
	Total     int      `json:"total"`     // the total each of them rolled in that round
	PlayerIDs []string `json:"playerIds"` // in seat order
}

// MarshalJSON implements json.Marshaler: the Estate list fields are always
// lists (games saved before they existed decode them as nil).
func (o OpeningOrder) MarshalJSON() ([]byte, error) {
	type plain OpeningOrder
	if o.RollingIDs == nil {
		o.RollingIDs = []string{}
	}
	if o.Ties == nil {
		o.Ties = []OpeningTie{}
	}
	return json.Marshal(plain(o))
}

// PlayerSpot is a position/cash snapshot used for turn summaries.
type PlayerSpot struct {
	Position int `json:"position"`
	Cash     int `json:"cash"`
}

// TurnSummaryEntry is a per-player change since the viewer's last turn.
type TurnSummaryEntry struct {
	PlayerID     string `json:"playerId"`
	FromPosition int    `json:"fromPosition"`
	ToPosition   int    `json:"toPosition"`
	CashBefore   int    `json:"cashBefore"`
	CashAfter    int    `json:"cashAfter"`
	CashDelta    int    `json:"cashDelta"`
}

// TurnSummary summarises changes since the current player's previous turn.
type TurnSummary struct {
	PlayerID string             `json:"playerId"`
	At       int64              `json:"at"`
	Players  []TurnSummaryEntry `json:"players"`
}

// Settings are host-configurable options.
type Settings struct {
	DiceMode string     `json:"diceMode"`       // "random" or "choice"
	Rules    HouseRules `json:"rules,omitzero"` // house rules (rules.go); omitted while all default
}

// Decks holds the remaining card ids; index 0 is the top card.
type Decks struct {
	Chance []string `json:"chance"`
	Chest  []string `json:"chest"`
}

// LogEntry is one game log line.
type LogEntry struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	At   int64  `json:"at"`
}

// Supply is the bank's remaining building stock (bankSupply).
type Supply struct {
	Houses int `json:"houses"`
	Hotels int `json:"hotels"`
}

// nullID marshals "" as JSON null.
type nullID string

func (s nullID) MarshalJSON() ([]byte, error) {
	if s == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(s))
}

// The MarshalJSON methods below shadow nullable string fields with nullID
// (a shallower field with the same JSON name wins in encoding/json).

// MarshalJSON implements json.Marshaler.
func (g Game) MarshalJSON() ([]byte, error) {
	type plain Game
	return json.Marshal(struct {
		plain
		HostID   nullID `json:"hostId"`
		WinnerID nullID `json:"winnerId"`
	}{plain(g), nullID(g.HostID), nullID(g.WinnerID)})
}

// MarshalJSON implements json.Marshaler.
func (p PropertyState) MarshalJSON() ([]byte, error) {
	type plain PropertyState
	return json.Marshal(struct {
		OwnerID nullID `json:"ownerId"`
		plain
	}{nullID(p.OwnerID), plain(p)})
}

// MarshalJSON implements json.Marshaler.
func (a Auction) MarshalJSON() ([]byte, error) {
	type plain Auction
	return json.Marshal(struct {
		plain
		BidderID nullID `json:"bidderId"`
	}{plain(a), nullID(a.BidderID)})
}

// MarshalJSON implements json.Marshaler.
func (d Debt) MarshalJSON() ([]byte, error) {
	type plain Debt
	return json.Marshal(struct {
		plain
		CreditorID nullID `json:"creditorId"`
	}{plain(d), nullID(d.CreditorID)})
}

// MarshalJSON implements json.Marshaler.
func (l Landing) MarshalJSON() ([]byte, error) {
	type plain Landing
	return json.Marshal(struct {
		plain
		RecipientID nullID `json:"recipientId"`
	}{plain(l), nullID(l.RecipientID)})
}

// MarshalJSON implements json.Marshaler.
func (b Bankruptcy) MarshalJSON() ([]byte, error) {
	type plain Bankruptcy
	return json.Marshal(struct {
		plain
		CreditorID nullID `json:"creditorId"`
	}{plain(b), nullID(b.CreditorID)})
}
