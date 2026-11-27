package hardai

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
)

// The heuristic AI (internal/ai) reads a snapshot of the game in its own view
// types, documented as json.Marshal(engine.Game) -> json.Unmarshal(ai.Game).
// That round trip costs tens of microseconds, far too much for Monte Carlo
// rollouts, so viewBuilder fills an ai.Game directly, reusing its memory
// between calls. TestViewMatchesJSON checks that the direct view equals the
// JSON round trip, field for field, with default and with house rules.
//
// internal/ai keeps growing (it learnt loans and house rules while this
// package was written). fastViewOK guards against the next addition: at
// start-up it walks ai.Game's JSON keys by reflection and, if any of them is
// one the builder does not fill, every view goes through the JSON round
// trip instead. Slower, never wrong.

// fastKeys lists, per ai view type, the JSON keys build fills.
var fastKeys = map[string]string{
	"Game": "code status phase hostId players currentIndex properties turn pendingPurchase auction debt " +
		"winnerId pendingCard trade aiTradeCooldowns turnCounts settings version freeParking loans loanOffer log",
	"Player":        "id name kind color cash position inJail jailTurns jailCards bankrupt",
	"PropertyState": "ownerId mortgaged houses",
	"Turn":          "playerId dice doublesCount extraRoll aiTradeAttempted",
	"Auction":       "spaceIndex activeIds turnPos currentBid bidderId returnPhase clockMs",
	"Debt":          "payerId amount creditorId reason loanId",
	"PendingCard":   "playerId deck cardId text",
	"Trade":         "id proposerId targetId offer request returnPhase createdAt",
	"TradeBundle":   "cash properties",
	"Settings":      "diceMode rules",
	"HouseRules":    "loans freeParkingJackpot auctions startingCash auctionClock",
	"Loan":          "id lenderId borrowerId principal repay dueTurn collateral status",
	"LoanOffer":     "id direction proposerId targetId lenderId borrowerId principal repay dueTurns collateral returnPhase",
	"LogEntry":      "text",
}

// fastViewOK reports whether build may fill views directly (see above).
var fastViewOK = viewKeysKnown(reflect.TypeOf(ai.Game{}), map[reflect.Type]bool{})

func viewKeysKnown(t reflect.Type, seen map[reflect.Type]bool) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t.PkgPath() != reflect.TypeOf(ai.Game{}).PkgPath() || seen[t] {
		return true
	}
	seen[t] = true
	known, ok := fastKeys[t.Name()]
	if !ok {
		return false
	}
	keys := map[string]bool{}
	for _, k := range strings.Fields(known) {
		keys[k] = true
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if !keys[name] || !viewKeysKnown(f.Type, seen) {
			return false
		}
	}
	return true
}

// viewBuilder converts engine games to ai.Game snapshots. It is not safe for
// concurrent use; every goroutine needs its own.
type viewBuilder struct {
	view    ai.Game
	players []ai.Player
	ptrs    []*ai.Player
	props   [engine.BoardSize]ai.PropertyState
	turn    ai.Turn
	auction ai.Auction
	debt    ai.Debt
	card    ai.PendingCard
	trade   ai.Trade
	pending int
	active  []string
	log     []ai.LogEntry
	loans   []ai.Loan
	loanPtr []*ai.Loan
	offer   ai.LoanOffer
	auct    bool

	forceJSON bool // always use the JSON round trip (tests, debugging)

	// The engine creates one PropertyState per ownable space in CreateGame
	// and only ever mutates them in place, so their pointers are cached per
	// game instead of iterating the map on every call. The cache holds
	// strong references, so a cached address can never be reused by a new
	// game while it is cached.
	srcGame *engine.Game
	src     [engine.BoardSize]*engine.PropertyState
	propMap map[int]*ai.PropertyState // keys = ownable indexes, values = &props[i]
}

// defaultRules reports whether g plays the classic rules (no house rules
// and no house-rule state). The auction clock does not count: it changes
// only how an auction is run, and onlyMove never answers an auction.
func defaultRules(g *engine.Game) bool {
	rules := g.Settings.Rules
	rules.AuctionClock = 0
	return rules.IsZero() && g.Loans == nil && g.LoanOffer == nil && g.FreeParking == 0
}

// sourceValid reports whether the cached property pointers belong to g.
func (b *viewBuilder) sourceValid(g *engine.Game) bool {
	if b.srcGame != g || len(g.Properties) != len(engine.OwnableIndexes) {
		return false
	}
	first, last := engine.OwnableIndexes[0], engine.OwnableIndexes[len(engine.OwnableIndexes)-1]
	return g.Properties[first] == b.src[first] && g.Properties[last] == b.src[last]
}

// bindSource caches g's property pointers; false if g's property map is not
// exactly one non-nil state per ownable space.
func (b *viewBuilder) bindSource(g *engine.Game) bool {
	b.srcGame = nil
	if len(g.Properties) != len(engine.OwnableIndexes) {
		return false
	}
	for _, index := range engine.OwnableIndexes {
		state := g.Properties[index]
		if state == nil {
			return false
		}
		b.src[index] = state
	}
	if b.propMap == nil {
		b.propMap = make(map[int]*ai.PropertyState, len(engine.OwnableIndexes))
		for _, index := range engine.OwnableIndexes {
			b.propMap[index] = &b.props[index]
		}
	}
	b.srcGame = g
	return true
}

// build returns the AI view of g. The result is owned by the builder and is
// overwritten by the next call.
func (b *viewBuilder) build(g *engine.Game) *ai.Game {
	if b.forceJSON || !fastViewOK {
		return jsonView(g)
	}
	v := &b.view
	*v = ai.Game{
		Code: g.Code, Status: g.Status, Phase: g.Phase, HostID: g.HostID,
		CurrentIndex: g.CurrentIndex, WinnerID: g.WinnerID, Version: g.Version,
		FreeParking: g.FreeParking,
		// The AI only reads these maps.
		AITradeCooldowns: g.AITradeCooldowns,
		TurnCounts:       g.TurnCounts,
	}
	v.Settings.DiceMode = g.Settings.DiceMode
	r := g.Settings.Rules
	v.Settings.Rules = ai.HouseRules{Loans: r.Loans, FreeParkingJackpot: r.FreeParkingJackpot, StartingCash: r.StartingCash, AuctionClock: r.AuctionClock}
	if r.Auctions != nil {
		b.auct = *r.Auctions
		v.Settings.Rules.Auctions = &b.auct
	}

	if g.Players != nil {
		n := len(g.Players)
		if cap(b.players) < n {
			b.players = make([]ai.Player, n)
			b.ptrs = make([]*ai.Player, n)
		}
		b.players = b.players[:n]
		b.ptrs = b.ptrs[:n]
		for i, p := range g.Players {
			b.players[i] = ai.Player{
				ID: p.ID, Name: p.Name, Kind: p.Kind, Color: p.Color, Cash: p.Cash,
				Position: p.Position, InJail: p.InJail, JailTurns: p.JailTurns,
				JailCards: p.JailCards, Bankrupt: p.Bankrupt,
			}
			b.ptrs[i] = &b.players[i]
		}
		v.Players = b.ptrs
	}

	if g.Properties != nil {
		if !b.sourceValid(g) && !b.bindSource(g) {
			return jsonView(g) // a property map the engine never builds
		}
		for _, index := range engine.OwnableIndexes {
			state := b.src[index]
			b.props[index] = ai.PropertyState{OwnerID: state.OwnerID, Mortgaged: state.Mortgaged, Houses: state.Houses}
		}
		v.Properties = b.propMap
	}

	if t := g.Turn; t != nil {
		b.turn = ai.Turn{
			PlayerID: t.PlayerID, Dice: t.Dice, // read-only for the AI
			DoublesCount: t.DoublesCount, ExtraRoll: t.ExtraRoll, AITradeAttempted: t.AITradeAttempted,
		}
		v.Turn = &b.turn
	}
	if g.PendingPurchase != nil {
		b.pending = *g.PendingPurchase
		v.PendingPurchase = &b.pending
	}
	if a := g.Auction; a != nil {
		// ChooseAction normalises Auction.TurnPos; ActiveIDs is copied so the
		// view never aliases engine memory the AI might touch.
		b.active = append(b.active[:0], a.ActiveIDs...)
		if a.ActiveIDs == nil {
			b.active = nil
		}
		b.auction = ai.Auction{
			SpaceIndex: a.SpaceIndex, ActiveIDs: b.active, TurnPos: a.TurnPos,
			CurrentBid: a.CurrentBid, BidderID: a.BidderID, ReturnPhase: a.ReturnPhase, ClockMs: a.ClockMs,
		}
		v.Auction = &b.auction
	}
	if d := g.Debt; d != nil {
		b.debt = ai.Debt{PayerID: d.PayerID, Amount: d.Amount, CreditorID: d.CreditorID, Reason: d.Reason, LoanID: d.LoanID}
		v.Debt = &b.debt
	}
	if c := g.PendingCard; c != nil {
		b.card = ai.PendingCard{PlayerID: c.PlayerID, Deck: c.Deck, CardID: c.CardID, Text: c.Text}
		v.PendingCard = &b.card
	}
	if t := g.Trade; t != nil {
		b.trade = ai.Trade{
			ID: t.ID, ProposerID: t.ProposerID, TargetID: t.TargetID,
			Offer:       ai.TradeBundle{Cash: t.Offer.Cash, Properties: t.Offer.Properties},
			Request:     ai.TradeBundle{Cash: t.Request.Cash, Properties: t.Request.Properties},
			ReturnPhase: t.ReturnPhase, CreatedAt: t.CreatedAt,
		}
		v.Trade = &b.trade
	}
	if g.Loans != nil {
		n := len(g.Loans)
		if cap(b.loans) < n {
			b.loans = make([]ai.Loan, n, n+4)
			b.loanPtr = make([]*ai.Loan, n, n+4)
		}
		b.loans, b.loanPtr = b.loans[:n], b.loanPtr[:n]
		for i, l := range g.Loans {
			b.loans[i] = ai.Loan{
				ID: l.ID, LenderID: l.LenderID, BorrowerID: l.BorrowerID, Principal: l.Principal,
				Repay: l.Repay, DueTurn: l.DueTurn, Collateral: l.Collateral, Status: l.Status,
			}
			b.loanPtr[i] = &b.loans[i]
		}
		v.Loans = b.loanPtr
	}
	if o := g.LoanOffer; o != nil {
		b.offer = ai.LoanOffer{
			ID: o.ID, Direction: o.Direction, ProposerID: o.ProposerID, TargetID: o.TargetID,
			LenderID: o.LenderID, BorrowerID: o.BorrowerID, Principal: o.Principal, Repay: o.Repay,
			DueTurns: o.DueTurns, Collateral: o.Collateral, ReturnPhase: o.ReturnPhase,
		}
		v.LoanOffer = &b.offer
	}
	if g.Log != nil {
		b.log = b.log[:0]
		for _, entry := range g.Log {
			b.log = append(b.log, ai.LogEntry{Text: entry.Text})
		}
		if b.log == nil {
			b.log = []ai.LogEntry{}
		}
		v.Log = b.log
	}
	return v
}

// jsonView is the documented conversion: json.Marshal then json.Unmarshal.
func jsonView(g *engine.Game) *ai.Game {
	data, err := json.Marshal(g)
	if err != nil {
		return nil
	}
	var view ai.Game
	if err := json.Unmarshal(data, &view); err != nil {
		return nil
	}
	return &view
}

// toEngineAction converts an AI decision to an engine action. The action
// types internal/ai has always produced are mapped field by field; anything
// else (loan actions, or whatever it learns next) goes through JSON, which
// is the documented ai.Action -> engine.Action path.
func toEngineAction(a *ai.Action) (engine.Action, bool) {
	if a == nil {
		return engine.Action{}, false
	}
	switch a.Type {
	case ai.ActionConfirmCard, ai.ActionAcceptTrade, ai.ActionRejectTrade, ai.ActionPassAuction,
		ai.ActionBuy, ai.ActionDecline, ai.ActionRoll, ai.ActionUseJailCard, ai.ActionPayBail,
		ai.ActionEndTurn, ai.ActionPayDebt, ai.ActionBankrupt:
		return engine.Action{Type: a.Type}, true
	case ai.ActionBid:
		return engine.Action{Type: a.Type, Amount: a.Amount}, true
	case ai.ActionBuild, ai.ActionSellBuilding, ai.ActionMortgage, ai.ActionUnmortgage:
		return engine.Action{Type: a.Type, SpaceIndex: a.SpaceIndex}, true
	case ai.ActionOfferTrade:
		return engine.Action{
			Type: a.Type, TargetID: a.TargetID,
			Offer: engineBundle(a.Offer), Request: engineBundle(a.Request),
		}, true
	}
	data, err := json.Marshal(a)
	if err != nil {
		return engine.Action{}, false
	}
	var out engine.Action
	if err := json.Unmarshal(data, &out); err != nil {
		return engine.Action{}, false
	}
	return out, true
}

func engineBundle(b *ai.TradeBundle) *engine.TradeBundle {
	out := &engine.TradeBundle{Properties: []int{}}
	if b != nil {
		out.Cash = b.Cash
		out.Properties = append(out.Properties, b.Properties...)
	}
	return out
}
