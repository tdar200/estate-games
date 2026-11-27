package engine

import (
	"errors"
	"math"
	"sort"
)

// House rules are optional variations the host picks in the lobby with the
// set_rules action. They live in Game.Settings.Rules in a sparse form whose
// zero value is the classic game: with default rules the game, its JSON and
// its publicState are exactly what they were before house rules existed.
//
//	rule                default  effect when changed
//	loans               false    offer_loan / accept_loan / reject_loan / repay_loan (loans.go)
//	freeParkingJackpot  false    bank fees go to Game.FreeParking; landing on City Park collects it
//	auctions            true     false: a declined property stays with the bank
//	startingCash        1500     cash of every seat (500..5000 in steps of 50)
//	auctionClock        0 / 8    seconds of the timed open auction; 0 = turn order
//
// auctionClock is the one rule whose stored zero is not what a new game
// plays: games saved before the timed auction existed (and the game.js
// parity traces) store nothing and keep the turn-order auction, while
// CreateGame gives every new game DefaultAuctionClock (auction.go). So a new
// game stores {"auctionClock":8} and its publicState carries "rules".
//
// Starting cash is applied when a seat is created (AddPlayer) and, because
// rules can only change in the lobby, set_rules re-bases the cash of every
// seat already in the lobby. So in the lobby every player always holds the
// starting cash, StartGame never touches cash, and its "Game started; every
// player has $N" log reports the configured amount.

// ActionSetRules is the host-only lobby action {type:"set_rules", rules:{…}}.
const ActionSetRules = "set_rules"

// Starting cash limits for set_rules.
const (
	MinStartingCash  = 500
	MaxStartingCash  = 5000
	StartingCashStep = 50
)

// Auction clock limits for set_rules (seconds; 0 = the turn-order auction).
const (
	DefaultAuctionClock = 8
	MinAuctionClock     = 5
	MaxAuctionClock     = 30
)

// LandingFreeParking is lastLanding.kind when a player collects the Free
// Parking jackpot (amount = the jackpot; recipientId = null).
const LandingFreeParking = "freeParking"

// HouseRules is the stored (sparse) form of the house rules. Every field's
// zero value is the default, so a default rule set marshals to nothing
// (Settings.Rules is omitted). Auctions is nil for the default (on); it is
// only ever stored as a pointer to false. StartingCash is 0 for the default
// (1500). Use the methods (or Effective) instead of reading fields directly.
type HouseRules struct {
	Loans              bool  `json:"loans,omitempty"`
	FreeParkingJackpot bool  `json:"freeParkingJackpot,omitempty"`
	Auctions           *bool `json:"auctions,omitempty"`     // nil = true
	StartingCash       int   `json:"startingCash,omitempty"` // 0 = 1500
	AuctionClock       int   `json:"auctionClock,omitempty"` // 0 = turn-order auction
}

// RuleSet is the effective (fully spelled out) house rules, as exposed in
// PublicGameState.Rules when they differ from the defaults.
type RuleSet struct {
	Loans              bool `json:"loans"`
	FreeParkingJackpot bool `json:"freeParkingJackpot"`
	Auctions           bool `json:"auctions"`
	StartingCash       int  `json:"startingCash"`
	AuctionClock       int  `json:"auctionClock"` // seconds; 0 = turn-order auction
}

// RulesUpdate is the payload of set_rules for Go callers. Nil fields keep
// their current value. JSON clients send the same keys:
// {"type":"set_rules","rules":{"loans":true,"startingCash":2000}}.
type RulesUpdate struct {
	Loans              *bool `json:"loans,omitempty"`
	FreeParkingJackpot *bool `json:"freeParkingJackpot,omitempty"`
	Auctions           *bool `json:"auctions,omitempty"`
	StartingCash       *int  `json:"startingCash,omitempty"`
	AuctionClock       *int  `json:"auctionClock,omitempty"`
}

// DefaultRules returns the classic rules: what a game that stores no rules
// plays (turn-order auctions). New games start from NewGameRules.
func DefaultRules() RuleSet {
	return RuleSet{Auctions: true, StartingCash: StartingCash}
}

// NewGameRules returns the rules CreateGame gives a new game: the classic
// rules with the timed open auction.
func NewGameRules() RuleSet {
	rules := DefaultRules()
	rules.AuctionClock = DefaultAuctionClock
	return rules
}

// TimedAuctions reports whether auctions are timed open bidding.
func (r HouseRules) TimedAuctions() bool { return r.AuctionClock > 0 }

// AuctionsEnabled reports whether declined properties go to auction.
func (r HouseRules) AuctionsEnabled() bool { return r.Auctions == nil || *r.Auctions }

// StartingCashAmount is the cash every seat starts with.
func (r HouseRules) StartingCashAmount() int {
	if r.StartingCash <= 0 {
		return StartingCash
	}
	return r.StartingCash
}

// Effective spells out the rules with defaults filled in.
func (r HouseRules) Effective() RuleSet {
	return RuleSet{
		Loans:              r.Loans,
		FreeParkingJackpot: r.FreeParkingJackpot,
		Auctions:           r.AuctionsEnabled(),
		StartingCash:       r.StartingCashAmount(),
		AuctionClock:       max(0, r.AuctionClock),
	}
}

// IsZero reports whether the rules are the defaults; encoding/json's
// omitzero uses it to leave "rules" out of default games.
func (r HouseRules) IsZero() bool { return r.Effective() == DefaultRules() }

// Stored returns the canonical sparse form of an effective rule set.
func (r RuleSet) Stored() HouseRules {
	out := HouseRules{Loans: r.Loans, FreeParkingJackpot: r.FreeParkingJackpot}
	if !r.Auctions {
		off := false
		out.Auctions = &off
	}
	if r.StartingCash != StartingCash {
		out.StartingCash = r.StartingCash
	}
	out.AuctionClock = r.AuctionClock
	return out
}

// publicRules is PublicGameState.Rules: nil (omitted) for default rules.
func publicRules(g *Game) *RuleSet {
	if g.Settings.Rules.IsZero() {
		return nil
	}
	rules := g.Settings.Rules.Effective()
	return &rules
}

var (
	errInvalidRules = errors.New("Invalid house rules")
	errStartingCash = errors.New("Starting cash must be between " + money(MinStartingCash) + " and " +
		money(MaxStartingCash) + " in steps of " + money(StartingCashStep))
	errAuctionClock = errors.New("The auction clock must be 0 (turn order) or " + itoa(MinAuctionClock) + " to " +
		itoa(MaxAuctionClock) + " seconds")
)

func validAuctionClock(value float64) bool {
	return isInteger(value) && (value == 0 || (value >= MinAuctionClock && value <= MaxAuctionClock))
}

func validStartingCash(value float64) bool {
	return isInteger(value) && value >= MinStartingCash && value <= MaxStartingCash &&
		math.Mod(value, StartingCashStep) == 0
}

// rulesUpdate reads action.rules. JSON input must be an object whose keys are
// known rules; booleans must be real booleans and startingCash a number (or a
// numeric string, coerced with Number() like every other engine number).
func (a *Action) rulesUpdate() (RulesUpdate, error) {
	if a.raw == nil {
		if a.Rules == nil {
			return RulesUpdate{}, errInvalidRules
		}
		update := *a.Rules
		if update.StartingCash != nil && !validStartingCash(float64(*update.StartingCash)) {
			return RulesUpdate{}, errStartingCash
		}
		if update.AuctionClock != nil && !validAuctionClock(float64(*update.AuctionClock)) {
			return RulesUpdate{}, errAuctionClock
		}
		return update, nil
	}
	object, ok := a.raw["rules"].(map[string]any)
	if !ok {
		return RulesUpdate{}, errInvalidRules
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys) // deterministic error for several bad keys
	update := RulesUpdate{}
	for _, key := range keys {
		value := object[key]
		switch key {
		case "loans", "freeParkingJackpot", "auctions":
			flag, isBool := value.(bool)
			if !isBool {
				return RulesUpdate{}, errInvalidRules
			}
			switch key {
			case "loans":
				update.Loans = &flag
			case "freeParkingJackpot":
				update.FreeParkingJackpot = &flag
			default:
				update.Auctions = &flag
			}
		case "startingCash":
			number := jsNumber(value)
			if _, isBool := value.(bool); isBool || !validStartingCash(number) {
				return RulesUpdate{}, errStartingCash
			}
			cash := int(number)
			update.StartingCash = &cash
		case "auctionClock":
			number := jsNumber(value)
			if _, isBool := value.(bool); isBool || !validAuctionClock(number) {
				return RulesUpdate{}, errAuctionClock
			}
			seconds := int(number)
			update.AuctionClock = &seconds
		default:
			return RulesUpdate{}, errors.New("Unknown house rule: " + key)
		}
	}
	return update, nil
}

// setRules applies set_rules: host only, lobby only. Keys left out of the
// payload keep their current value.
func setRules(g *Game, requesterID string, action *Action) error {
	if g.Status != StatusLobby {
		return errors.New("House rules can only be changed before the game starts")
	}
	if requesterID != g.HostID {
		return errors.New("Only the host can change house rules")
	}
	update, err := action.rulesUpdate()
	if err != nil {
		return err
	}
	rules := g.Settings.Rules.Effective()
	previousCash := rules.StartingCash
	if update.Loans != nil {
		rules.Loans = *update.Loans
	}
	if update.FreeParkingJackpot != nil {
		rules.FreeParkingJackpot = *update.FreeParkingJackpot
	}
	if update.Auctions != nil {
		rules.Auctions = *update.Auctions
	}
	if update.StartingCash != nil {
		rules.StartingCash = *update.StartingCash
	}
	if update.AuctionClock != nil {
		rules.AuctionClock = *update.AuctionClock
	}
	g.Settings.Rules = rules.Stored()
	if rules.StartingCash != previousCash {
		for _, player := range g.Players {
			player.Cash = rules.StartingCash
		}
	}
	addLog(g, "Rules updated: "+describeRules(rules))
	return nil
}

// describeRules renders e.g. "loans on, City Park jackpot off, auctions
// on, starting cash $1500, auction clock 8 s". A turn-order auction (clock
// 0) adds nothing, so games recorded before the clock existed log the same.
func describeRules(r RuleSet) string {
	onOff := func(on bool) string {
		if on {
			return "on"
		}
		return "off"
	}
	return "loans " + onOff(r.Loans) +
		", City Park jackpot " + onOff(r.FreeParkingJackpot) +
		", auctions " + onOff(r.Auctions) +
		", starting cash " + money(r.StartingCash) + describeAuctionClock(r.AuctionClock)
}

func describeAuctionClock(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	return ", auction clock " + itoa(seconds) + " s"
}

// payToJackpot adds a payment to the bank (creditorID "") to the City
// Park pot when that rule is on. charge and payDebt call it, which covers
// taxes, card fees, jail fees and building repairs.
func payToJackpot(g *Game, creditorID string, amount int) {
	if creditorID == "" && amount > 0 && g.Settings.Rules.FreeParkingJackpot {
		g.FreeParking += amount
	}
}

// collectJackpot pays the pot to a player who landed on the parking space.
// It reports whether anything was collected (otherwise the landing is the
// usual "neutral" one).
func collectJackpot(g *Game, player *Player, space *Space) bool {
	if !g.Settings.Rules.FreeParkingJackpot || g.FreeParking <= 0 {
		return false
	}
	amount := g.FreeParking
	g.FreeParking = 0
	player.Cash += amount
	setLastLanding(g, player, space, LandingFreeParking, amount, "")
	addLog(g, player.Name+" collected the "+money(amount)+" City Park jackpot")
	return true
}

// declineWithoutAuction handles decline when the auctions rule is off: the
// property simply stays with the bank. It reports whether it handled it.
func declineWithoutAuction(g *Game, spaceIndex int) bool {
	if g.Settings.Rules.AuctionsEnabled() {
		return false
	}
	name := "A player"
	if player := CurrentPlayer(g); player != nil {
		name = player.Name
	}
	addLog(g, name+" declined "+Board[spaceIndex].Name+"; it stays with the bank")
	finishLanding(g)
	return true
}

// decodeHouseRuleFields fills the best-effort typed view of the set_rules
// and loan fields of a decoded action (see Action.UnmarshalJSON).
func (a *Action) decodeHouseRuleFields() {
	object := a.raw
	if _, isObject := object["rules"].(map[string]any); isObject {
		if update, err := a.rulesUpdate(); err == nil {
			a.Rules = &update
		}
	}
	if s, ok := object["direction"].(string); ok {
		a.Direction = s
	}
	if s, ok := object["loanId"].(string); ok {
		a.LoanID = s
	}
	for key, target := range map[string]*int{"principal": &a.Principal, "repay": &a.Repay, "dueTurns": &a.DueTurns} {
		if v := a.number(key); isInteger(v) && math.Abs(v) <= math.MaxInt32 {
			*target = int(v)
		}
	}
	if values, ok := a.loanCollateral(); ok {
		for _, v := range values {
			if index, onBoard := boardIndex(v); onBoard {
				a.Collateral = append(a.Collateral, index)
			}
		}
	}
}

// encodeHouseRuleFields adds the set_rules and loan fields of a Go-built
// action to its JSON form (see Action.MarshalJSON).
func (a *Action) encodeHouseRuleFields(out map[string]any) {
	switch a.Type {
	case ActionSetRules:
		if a.Rules != nil {
			out["rules"] = a.Rules
		}
	case ActionOfferLoan:
		out["targetId"] = a.TargetID
		if a.Direction != "" {
			out["direction"] = a.Direction
		}
		out["principal"] = a.Principal
		out["repay"] = a.Repay
		out["dueTurns"] = a.DueTurns
		collateral := a.Collateral
		if collateral == nil {
			collateral = []int{}
		}
		out["collateral"] = collateral
	case ActionRepayLoan:
		out["loanId"] = a.LoanID
	}
}
