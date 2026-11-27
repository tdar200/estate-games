package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// Action types accepted by PerformAction.
const (
	ActionStart        = "start"
	ActionRollForOrder = "roll_for_order"
	ActionRoll         = "roll"
	ActionConfirmCard  = "confirm_card"
	ActionSetDiceMode  = "set_dice_mode"
	ActionOfferTrade   = "offer_trade"
	ActionAcceptTrade  = "accept_trade"
	ActionRejectTrade  = "reject_trade"
	// ActionCounterTrade answers the pending trade with other terms: the
	// trade's target becomes the proposer of a new trade to the original
	// proposer (Trade.CounterTo), which that player accepts or declines.
	ActionCounterTrade = "counter_trade"
	ActionBuy          = "buy"
	ActionDecline      = "decline"
	ActionBid          = "bid"
	ActionPassAuction  = "pass_auction"
	ActionEndTurn      = "end_turn"
	ActionPayBail      = "pay_bail"
	ActionUseJailCard  = "use_jail_card"
	ActionPayDebt      = "pay_debt"
	ActionBankrupt     = "bankrupt"
	ActionBuild        = "build"
	ActionSellBuilding = "sell_building"
	ActionMortgage     = "mortgage"
	ActionUnmortgage   = "unmortgage"
)

// Action is a player command, e.g. {type: "build", spaceIndex: 3}.
//
// Build actions in Go by setting the typed fields. When an Action is decoded
// from JSON (UnmarshalJSON), the original JSON values are kept and the engine
// applies the same loose coercions as game.js (Number("5") === 5, a missing
// spaceIndex is NaN, a non-object action is "Invalid action format", ...);
// the typed fields are then only a best-effort view for callers.
type Action struct {
	Type       string       `json:"type"`
	Dice       []int        `json:"dice,omitempty"`       // roll in "choice" dice mode
	Mode       string       `json:"mode,omitempty"`       // set_dice_mode
	TargetID   string       `json:"targetId,omitempty"`   // offer_trade
	Offer      *TradeBundle `json:"offer,omitempty"`      // offer_trade: what the proposer gives
	Request    *TradeBundle `json:"request,omitempty"`    // offer_trade: what the proposer asks for
	Amount     int          `json:"amount,omitempty"`     // bid
	SpaceIndex int          `json:"spaceIndex,omitempty"` // build, sell_building, mortgage, unmortgage
	Reason     string       `json:"reason,omitempty"`     // reject_trade, counter_trade: a computer's one-line reason (logged)

	// House rules and loans (rules.go, loans.go).
	Rules      *RulesUpdate `json:"rules,omitempty"`      // set_rules
	Direction  string       `json:"direction,omitempty"`  // offer_loan: "lend" or "borrow"
	Principal  int          `json:"principal,omitempty"`  // offer_loan: cash lent
	Repay      int          `json:"repay,omitempty"`      // offer_loan: amount paid back
	DueTurns   int          `json:"dueTurns,omitempty"`   // offer_loan: borrower turns until due
	Collateral []int        `json:"collateral,omitempty"` // offer_loan: borrower's pledged spaces
	LoanID     string       `json:"loanId,omitempty"`     // repay_loan

	// Server and host actions (host.go).
	Now      int64  `json:"now,omitempty"`      // close_auction: the server's clock (Unix ms)
	PlayerID string `json:"playerId,omitempty"` // remove_player: who is taken out

	raw     map[string]any // original JSON object, if decoded
	invalid bool           // decoded JSON was not an object (or was null)
}

// UnmarshalJSON keeps the raw JSON values so that PerformAction can coerce
// them exactly like the JS engine. It never fails on well-formed JSON.
func (a *Action) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*a = Action{}
	object, ok := value.(map[string]any)
	if !ok {
		a.invalid = true
		return nil
	}
	a.raw = object
	// Best-effort typed view.
	if s, ok := object["type"].(string); ok {
		a.Type = s
	}
	if s, ok := object["mode"].(string); ok {
		a.Mode = s
	}
	if s, ok := object["targetId"].(string); ok {
		a.TargetID = s
	}
	if s, ok := object["reason"].(string); ok {
		a.Reason = s
	}
	if s, ok := object["playerId"].(string); ok {
		a.PlayerID = s
	}
	a.Now = a.nowValue()
	if values, ok := a.diceValues(); ok {
		for _, v := range values {
			if !isInteger(v) {
				a.Dice = nil
				break
			}
			a.Dice = append(a.Dice, int(v))
		}
	}
	if v := a.number("amount"); isInteger(v) && math.Abs(v) <= math.MaxInt32 {
		a.Amount = int(v)
	}
	if v, ok := boardIndex(a.number("spaceIndex")); ok {
		a.SpaceIndex = v
	}
	for key, target := range map[string]**TradeBundle{"offer": &a.Offer, "request": &a.Request} {
		if _, isObject := object[key].(map[string]any); isObject {
			if bundle, err := normalizeTradeBundle(a.bundle(key)); err == nil {
				*target = &bundle
			}
		}
	}
	a.decodeHouseRuleFields()
	return nil
}

// MarshalJSON writes the raw JSON object if the action was decoded, otherwise
// the type plus the fields relevant to it.
func (a Action) MarshalJSON() ([]byte, error) {
	if a.raw != nil {
		return json.Marshal(a.raw)
	}
	if a.invalid {
		return []byte("null"), nil
	}
	out := map[string]any{"type": a.Type}
	a.encodeHouseRuleFields(out)
	switch a.Type {
	case ActionRoll:
		if a.Dice != nil {
			out["dice"] = a.Dice
		}
	case ActionSetDiceMode:
		out["mode"] = a.Mode
	case ActionOfferTrade:
		out["targetId"] = a.TargetID
		if a.Offer != nil {
			out["offer"] = a.Offer
		}
		if a.Request != nil {
			out["request"] = a.Request
		}
	case ActionCounterTrade:
		if a.Offer != nil {
			out["offer"] = a.Offer
		}
		if a.Request != nil {
			out["request"] = a.Request
		}
		if a.Reason != "" {
			out["reason"] = a.Reason
		}
	case ActionRejectTrade:
		if a.Reason != "" {
			out["reason"] = a.Reason
		}
	case ActionBid:
		out["amount"] = a.Amount
	case ActionCloseAuction:
		if a.Now != 0 {
			out["now"] = a.Now
		}
	case ActionRemovePlayer:
		out["playerId"] = a.PlayerID
	case ActionBuild, ActionSellBuilding, ActionMortgage, ActionUnmortgage:
		out["spaceIndex"] = a.SpaceIndex
	}
	return json.Marshal(out)
}

// String renders the action as JSON for logs and test messages.
func (a Action) String() string {
	data, err := a.MarshalJSON()
	if err != nil {
		return fmt.Sprintf("%#v", a)
	}
	return string(data)
}

// typeValue mirrors `typeof action.type === "string"`.
func (a *Action) typeValue() (string, bool) {
	if a.invalid {
		return "", false
	}
	if a.raw != nil {
		s, ok := a.raw["type"].(string)
		return s, ok
	}
	// A Go-built Action without a type is treated like a missing action.
	return a.Type, a.Type != ""
}

// number mirrors Number(action[key]); a missing key is undefined (NaN).
func (a *Action) number(key string) float64 {
	if a.raw != nil {
		value, present := a.raw[key]
		if !present {
			return math.NaN()
		}
		return jsNumber(value)
	}
	switch key {
	case "amount":
		return float64(a.Amount)
	case "spaceIndex":
		return float64(a.SpaceIndex)
	}
	return math.NaN()
}

// diceValues mirrors Array.isArray(dice) && dice.map(Number).
func (a *Action) diceValues() ([]float64, bool) {
	if a.raw != nil {
		list, ok := a.raw["dice"].([]any)
		if !ok {
			return nil, false
		}
		values := make([]float64, len(list))
		for i, item := range list {
			values[i] = jsNumber(item)
		}
		return values, true
	}
	if a.Dice == nil {
		return nil, false
	}
	values := make([]float64, len(a.Dice))
	for i, die := range a.Dice {
		values[i] = float64(die)
	}
	return values, true
}

// modeValue returns action.mode when it is a string.
func (a *Action) modeValue() (string, bool) {
	if a.raw != nil {
		s, ok := a.raw["mode"].(string)
		return s, ok
	}
	return a.Mode, true
}

// targetID returns action.targetId when it is a string (strict equality in JS
// means a non-string never matches a player).
func (a *Action) targetID() (string, bool) {
	if a.raw != nil {
		s, ok := a.raw["targetId"].(string)
		return s, ok
	}
	return a.TargetID, true
}

// rawBundle is a trade bundle after JS coercion but before validation.
type rawBundle struct {
	cash       float64
	properties []float64
}

// bundle mirrors normalizeTradeBundle's reads: Number(bundle?.cash || 0) and
// (Array.isArray(bundle?.properties) ? bundle.properties : []).map(Number).
func (a *Action) bundle(key string) rawBundle {
	out := rawBundle{}
	if a.raw != nil {
		object, ok := a.raw[key].(map[string]any)
		if !ok {
			return out
		}
		if cash := object["cash"]; jsTruthy(cash) {
			out.cash = jsNumber(cash)
		}
		if list, ok := object["properties"].([]any); ok {
			for _, item := range list {
				out.properties = append(out.properties, jsNumber(item))
			}
		}
		return out
	}
	source := a.Offer
	if key == "request" {
		source = a.Request
	}
	if source != nil {
		out.cash = float64(source.Cash)
		for _, index := range source.Properties {
			out.properties = append(out.properties, float64(index))
		}
	}
	return out
}

// thrown carries an error raised deep inside the rules (a JS throw that
// keeps earlier mutations); PerformAction recovers it.
type thrown struct{ err error }

// PerformAction applies a player's action to the game (performAction). The
// game is mutated in place; on error the returned message is the same text
// the JS engine throws. rng is consumed by start, roll_for_order and random
// rolls; nil uses DefaultRNG.
func PerformAction(g *Game, playerID string, action Action, rng RNG) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t, ok := recovered.(thrown)
			if !ok {
				panic(recovered)
			}
			err = t.err
		}
	}()
	rng = orDefault(rng)
	g.rng = rng
	defer func() { g.rng = nil }()
	actionType, ok := action.typeValue()
	if !ok {
		return errors.New("Invalid action format")
	}
	if g.Status != StatusPlaying && actionType != ActionStart && actionType != ActionSetDiceMode && actionType != ActionSetRules {
		return errors.New("The game has not started")
	}
	switch actionType {
	case ActionStart:
		err = StartGame(g, playerID, rng)
	case ActionRollForOrder:
		err = rollForOpeningOrder(g, playerID, rng)
	case ActionRoll:
		err = roll(g, playerID, rng, &action)
	case ActionConfirmCard:
		err = confirmCard(g, playerID)
	case ActionSetDiceMode:
		mode, modeOK := action.modeValue()
		err = setDiceMode(g, playerID, mode, modeOK)
	case ActionOfferTrade:
		err = offerTrade(g, playerID, &action)
	case ActionAcceptTrade:
		err = acceptTrade(g, playerID)
	case ActionRejectTrade:
		err = rejectTrade(g, playerID, &action)
	case ActionCounterTrade:
		err = counterTrade(g, playerID, &action)
	case ActionBuy:
		err = buyProperty(g, playerID)
	case ActionDecline:
		err = declineProperty(g, playerID)
	case ActionBid:
		err = bidAuction(g, playerID, action.number("amount"))
	case ActionPassAuction:
		err = passAuction(g, playerID)
	case ActionEndTurn:
		err = endTurn(g, playerID)
	case ActionPayBail:
		err = payBail(g, playerID)
	case ActionUseJailCard:
		err = useJailCard(g, playerID)
	case ActionPayDebt:
		err = payDebt(g, playerID)
	case ActionBankrupt:
		err = declareBankruptcy(g, playerID)
	case ActionBuild:
		err = build(g, playerID, actionSpaceIndex(&action))
	case ActionSellBuilding:
		err = sellBuilding(g, playerID, actionSpaceIndex(&action))
	case ActionMortgage:
		err = mortgage(g, playerID, actionSpaceIndex(&action))
	case ActionUnmortgage:
		err = unmortgage(g, playerID, actionSpaceIndex(&action))
	case ActionSetRules:
		err = setRules(g, playerID, &action)
	case ActionOfferLoan:
		err = offerLoan(g, playerID, &action)
	case ActionAcceptLoan:
		err = acceptLoan(g, playerID)
	case ActionRejectLoan:
		err = rejectLoan(g, playerID)
	case ActionRepayLoan:
		err = repayLoan(g, playerID, action.loanID())
	case ActionCloseAuction:
		err = closeAuction(g, playerID, action.nowValue())
	case ActionRemovePlayer:
		err = removeFromGame(g, playerID, action.playerIDValue())
	default:
		err = errors.New("Unknown action")
	}
	if err != nil {
		return err
	}
	touch(g)
	return nil
}

// actionSpaceIndex is BOARD-indexing of Number(action.spaceIndex): -1 when it
// addresses no space.
func actionSpaceIndex(action *Action) int {
	index, _ := boardIndex(action.number("spaceIndex"))
	return index
}
