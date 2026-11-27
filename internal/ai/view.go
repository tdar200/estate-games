package ai

// Read-only view of the game, mirroring the JSON of the JS game object
// (JSON.stringify(game) of src/game.js createGame/addPlayer/...). Only the keys
// the AI reads are declared; every other key (decks, openingOrder,
// turnSummary, lastMove, ...) is ignored when decoding.
//
// Nullable ids follow the engine convention: JSON null decodes to "" and ""
// means null. Real ids are never empty.

// Game is the decoded game snapshot handed to the AI.
type Game struct {
	Code             string                 `json:"code"`
	Status           string                 `json:"status"`
	Phase            string                 `json:"phase"`
	HostID           string                 `json:"hostId"`
	Players          []*Player              `json:"players"`
	CurrentIndex     int                    `json:"currentIndex"`
	Properties       map[int]*PropertyState `json:"properties"` // keyed by board index, ownable spaces only
	Turn             *Turn                  `json:"turn"`
	PendingPurchase  *int                   `json:"pendingPurchase"`
	Auction          *Auction               `json:"auction"`
	Debt             *Debt                  `json:"debt"`
	WinnerID         string                 `json:"winnerId"`
	PendingCard      *PendingCard           `json:"pendingCard"`
	Trade            *Trade                 `json:"trade"`
	AITradeCooldowns map[string]int         `json:"aiTradeCooldowns"` // "proposer|target|goal" -> expiry turn count
	TurnCounts       map[string]int         `json:"turnCounts"`       // player id -> completed turns
	Settings         Settings               `json:"settings"`
	Version          int                    `json:"version"`

	// House-rule state (engine rules.go and loans.go). The engine omits these
	// keys in default games, so there they decode to their zero values.
	FreeParking int        `json:"freeParking"` // City Park jackpot pot
	Loans       []*Loan    `json:"loans"`       // every accepted loan, in creation order
	LoanOffer   *LoanOffer `json:"loanOffer"`   // pending offer (phase "loan_offer")
	// Log is the game log (text only). The AI reads it to remember that it
	// already asked for a loan during the current debt (see loans.go).
	Log []LogEntry `json:"log"`
}

// Player is a seat.
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
}

// PropertyState is the mutable state of an ownable space.
type PropertyState struct {
	OwnerID   string `json:"ownerId"` // "" = null (bank)
	Mortgaged bool   `json:"mortgaged"`
	Houses    int    `json:"houses"` // 5 = hotel
}

// Turn is the current turn. Only aiTradeAttempted matters to the AI.
type Turn struct {
	PlayerID         string `json:"playerId"`
	Dice             []int  `json:"dice"`
	DoublesCount     int    `json:"doublesCount"`
	ExtraRoll        bool   `json:"extraRoll"`
	AITradeAttempted bool   `json:"aiTradeAttempted"`
}

// Auction is an open auction.
type Auction struct {
	SpaceIndex  int      `json:"spaceIndex"`
	ActiveIDs   []string `json:"activeIds"`
	TurnPos     int      `json:"turnPos"`
	CurrentBid  int      `json:"currentBid"`
	BidderID    string   `json:"bidderId"` // "" = null
	ReturnPhase string   `json:"returnPhase"`
	ClockMs     int      `json:"clockMs"` // > 0: a timed open auction (no turn order)
}

// Debt is an unpaid charge.
type Debt struct {
	PayerID    string `json:"payerId"`
	Amount     int    `json:"amount"`
	CreditorID string `json:"creditorId"` // "" = null (bank)
	Reason     string `json:"reason"`
	LoanID     string `json:"loanId"` // set when the debt is an overdue loan
}

// PendingCard is a drawn Headlines card awaiting confirmation.
type PendingCard struct {
	PlayerID string `json:"playerId"`
	Deck     string `json:"deck"`
	CardID   string `json:"cardId"`
	Text     string `json:"text"`
}

// TradeBundle is one side of a trade ({cash, properties}).
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
}

// Settings are the host options.
type Settings struct {
	DiceMode string     `json:"diceMode"`
	Rules    HouseRules `json:"rules"` // absent (zero) in default games
}

// HouseRules mirrors settings.rules in the engine's stored, sparse form:
// every field's zero value is the classic rule, so a missing "rules" key
// means the defaults.
type HouseRules struct {
	Loans              bool  `json:"loans"`
	FreeParkingJackpot bool  `json:"freeParkingJackpot"`
	Auctions           *bool `json:"auctions"`     // nil = on (only ever stored as false)
	StartingCash       int   `json:"startingCash"` // 0 = 1500
	AuctionClock       int   `json:"auctionClock"` // seconds of a timed auction; 0 = turn order
}

// AuctionsEnabled reports whether a declined property goes to auction.
func (r HouseRules) AuctionsEnabled() bool { return r.Auctions == nil || *r.Auctions }

// Loan statuses and offer directions (engine loans.go).
const (
	LoanActive    = "active"
	LoanRepaid    = "repaid"
	LoanDefaulted = "defaulted"
	LoanCancelled = "cancelled"

	LoanLend   = "lend"   // the proposer lends to targetId
	LoanBorrow = "borrow" // the proposer asks targetId for money
)

// Loan is an accepted player-to-player loan (game.loans[]).
type Loan struct {
	ID         string `json:"id"`
	LenderID   string `json:"lenderId"`
	BorrowerID string `json:"borrowerId"`
	Principal  int    `json:"principal"`
	Repay      int    `json:"repay"`
	DueTurn    int    `json:"dueTurn"`    // due once turnCounts[borrowerId] >= dueTurn
	Collateral []int  `json:"collateral"` // board indexes pledged by the borrower
	Status     string `json:"status"`     // LoanActive, LoanRepaid, LoanDefaulted or LoanCancelled
}

// LoanOffer is the pending loan proposal (game.loanOffer).
type LoanOffer struct {
	ID          string `json:"id"`
	Direction   string `json:"direction"` // LoanLend or LoanBorrow, from the proposer's view
	ProposerID  string `json:"proposerId"`
	TargetID    string `json:"targetId"` // the only player who may accept
	LenderID    string `json:"lenderId"`
	BorrowerID  string `json:"borrowerId"`
	Principal   int    `json:"principal"`
	Repay       int    `json:"repay"`
	DueTurns    int    `json:"dueTurns"`
	Collateral  []int  `json:"collateral"`
	ReturnPhase string `json:"returnPhase"`
}

// LogEntry is one game log line (only its text is decoded).
type LogEntry struct {
	Text string `json:"text"`
}

// Supply is the bank's remaining building stock (bankSupply in game.js).
type Supply struct {
	Houses int `json:"houses"`
	Hotels int `json:"hotels"`
}
