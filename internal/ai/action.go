package ai

import "encoding/json"

// Action types the AI can return (the engine's performAction action.type).
const (
	ActionConfirmCard  = "confirm_card"
	ActionAcceptTrade  = "accept_trade"
	ActionRejectTrade  = "reject_trade"
	ActionBid          = "bid"
	ActionPassAuction  = "pass_auction"
	ActionBuy          = "buy"
	ActionDecline      = "decline"
	ActionMortgage     = "mortgage"
	ActionUnmortgage   = "unmortgage"
	ActionBuild        = "build"
	ActionSellBuilding = "sell_building"
	ActionOfferTrade   = "offer_trade"
	ActionRoll         = "roll"
	ActionUseJailCard  = "use_jail_card"
	ActionPayBail      = "pay_bail"
	ActionEndTurn      = "end_turn"
	ActionPayDebt      = "pay_debt"
	ActionBankrupt     = "bankrupt"

	// Loans (house rule "loans"; see loans.go).
	ActionOfferLoan  = "offer_loan"
	ActionAcceptLoan = "accept_loan"
	ActionRejectLoan = "reject_loan"
	ActionRepayLoan  = "repay_loan"
)

// Action is an AI decision. It marshals to exactly the JSON of the JS action
// object: only the fields that belong to Type are emitted, e.g.
//
//	{"type":"roll"}
//	{"type":"bid","amount":120}
//	{"type":"build","spaceIndex":39}
//	{"type":"offer_trade","targetId":"ai_…","offer":{"cash":40,"properties":[12]},"request":{"cash":0,"properties":[28]}}
//	{"type":"offer_loan","targetId":"ai_…","direction":"borrow","principal":120,"repay":144,"dueTurns":4,"collateral":[]}
//	{"type":"repay_loan","loanId":"loan_…"}
//
// Action stays comparable (==): the loan terms hang off a pointer.
type Action struct {
	Type       string
	Amount     int          // bid
	SpaceIndex int          // build, sell_building, mortgage, unmortgage
	TargetID   string       // offer_trade, offer_loan
	Offer      *TradeBundle // offer_trade
	Request    *TradeBundle // offer_trade
	Loan       *LoanTerms   // offer_loan
	LoanID     string       // repay_loan
}

// LoanTerms are the terms of an offer_loan action. Direction is LoanBorrow
// when the proposer asks targetId for money and LoanLend when it lends;
// Collateral lists the borrower's pledged board indexes.
type LoanTerms struct {
	Direction  string `json:"direction"`
	Principal  int    `json:"principal"`
	Repay      int    `json:"repay"`
	DueTurns   int    `json:"dueTurns"`
	Collateral []int  `json:"collateral"`
}

// MarshalJSON implements json.Marshaler.
func (a Action) MarshalJSON() ([]byte, error) {
	switch a.Type {
	case ActionBid:
		return json.Marshal(struct {
			Type   string `json:"type"`
			Amount int    `json:"amount"`
		}{a.Type, a.Amount})
	case ActionBuild, ActionSellBuilding, ActionMortgage, ActionUnmortgage:
		return json.Marshal(struct {
			Type       string `json:"type"`
			SpaceIndex int    `json:"spaceIndex"`
		}{a.Type, a.SpaceIndex})
	case ActionOfferTrade:
		return json.Marshal(struct {
			Type     string      `json:"type"`
			TargetID string      `json:"targetId"`
			Offer    TradeBundle `json:"offer"`
			Request  TradeBundle `json:"request"`
		}{a.Type, a.TargetID, bundleJSON(a.Offer), bundleJSON(a.Request)})
	case ActionOfferLoan:
		terms := LoanTerms{}
		if a.Loan != nil {
			terms = *a.Loan
		}
		return json.Marshal(struct {
			Type       string `json:"type"`
			TargetID   string `json:"targetId"`
			Direction  string `json:"direction,omitempty"`
			Principal  int    `json:"principal"`
			Repay      int    `json:"repay"`
			DueTurns   int    `json:"dueTurns"`
			Collateral []int  `json:"collateral"`
		}{a.Type, a.TargetID, terms.Direction, terms.Principal, terms.Repay, terms.DueTurns, nonNil(terms.Collateral)})
	case ActionRepayLoan:
		return json.Marshal(struct {
			Type   string `json:"type"`
			LoanID string `json:"loanId"`
		}{a.Type, a.LoanID})
	}
	return json.Marshal(struct {
		Type string `json:"type"`
	}{a.Type})
}

// UnmarshalJSON implements json.Unmarshaler (the inverse of MarshalJSON).
func (a *Action) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type       string       `json:"type"`
		Amount     int          `json:"amount"`
		SpaceIndex int          `json:"spaceIndex"`
		TargetID   string       `json:"targetId"`
		Offer      *TradeBundle `json:"offer"`
		Request    *TradeBundle `json:"request"`
		LoanID     string       `json:"loanId"`
		LoanTerms
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*a = Action{
		Type:       raw.Type,
		Amount:     raw.Amount,
		SpaceIndex: raw.SpaceIndex,
		TargetID:   raw.TargetID,
		Offer:      raw.Offer,
		Request:    raw.Request,
		LoanID:     raw.LoanID,
	}
	if raw.Type == ActionOfferLoan {
		terms := raw.LoanTerms
		a.Loan = &terms
	}
	return nil
}

// bundleJSON returns b with a non-nil properties slice so that it marshals as
// {"cash":0,"properties":[]} like the JS object.
func bundleJSON(b *TradeBundle) TradeBundle {
	if b == nil {
		return TradeBundle{Properties: []int{}}
	}
	out := *b
	if out.Properties == nil {
		out.Properties = []int{}
	}
	return out
}

// Quote is the answer to a trade-quote request (quoteAiTrade):
// {"offerCash":…,"requestCash":…,"targetId":…}.
type Quote struct {
	OfferCash   int    `json:"offerCash"`
	RequestCash int    `json:"requestCash"`
	TargetID    string `json:"targetId"`
}
