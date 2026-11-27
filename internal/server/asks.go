package server

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"github.com/tdar200/estate-games/internal/engine"
)

// Computers' trade offers to people (0.10.1). Every level obeys the same
// rules, enforced here, at the one place a computer's move is played:
//
//   - a deal a person declined is never offered to them again in the same or
//     a worse form, by any computer: per person and deed set asked for, the
//     ledger keeps the best terms the person declined (what they would have
//     received, cash plus deeds at list value, mortgaged ones at half, less
//     any cash asked of them), and a new offer for that set must beat it by
//     10% and at least $20 (the floor is shared by all computers);
//   - at most maxReasks such improved offers per deed set, computer and game;
//   - one ask per deed per round: after any computer asks a person for a
//     deed, none asks for it again until the person has had another turn;
//   - a deed the person marked "not selling" is never asked for;
//   - the room setting botOffers: normal (these rules), fewer (also at most
//     one computer offer per person between two turns of theirs), off (none).
//
// Hard, Expert, Grandmaster and Easy see the rules while they choose
// (hardai's trade memory asks the gate), so a blocked idea gives way to
// their next one, and a computer whose own price can't beat the floor stops
// asking for that deed set; Normal's choice is checked afterwards and
// replaced by its move without a trade. A computer's counter-offer answers
// the person's own offer (one negotiation), so it is not an ask. Offers
// between computers are not affected.

const (
	botOffersNormal = "normal"
	botOffersFewer  = "fewer"
	botOffersOff    = "off"
	// botOffersUnlimited is the behaviour before 0.10.1 (no rules), for
	// measurements only: the settings API never accepts it.
	botOffersUnlimited = "unlimited"

	reaskGain  = 0.10 // a new offer beats the best one declined by this share ...
	reaskMin   = 20   // ... and by at least this many dollars
	maxReasks  = 2    // improved offers per deed set, computer and game
	deedRounds = 1    // of the person's turns between two asks for one deed
)

var botOfferModes = map[string]bool{botOffersNormal: true, botOffersFewer: true, botOffersOff: true}

func (r *room) botOffersMode() string {
	if r.botOffers == botOffersUnlimited {
		return r.botOffers
	}
	if botOfferModes[r.botOffers] {
		return r.botOffers
	}
	return botOffersNormal
}

// askLedger remembers computers' offers to people in one game.
type askLedger struct {
	floors  map[string]int // person|deed set -> the best terms declined (value to the person)
	reasks  map[string]int // bot|person|deed set -> its offers above a declined floor
	deeds   map[string]int // person|deed -> the person's turn count at the last ask for it
	perTurn map[string]int // person|their turn count -> computer offers
	open    *openAsk       // the computer offer to a person now pending
}

type openAsk struct {
	bot, person, key, tradeID string
	value                     int
}

func (l *askLedger) init() {
	if l.floors == nil {
		l.floors, l.reasks, l.deeds, l.perTurn = map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	}
}

// askRules is the ledger as a snapshot for one decision (no lock needed).
type askRules struct {
	mode                           string
	notSelling                     map[string]map[int]bool
	floors, reasks, deeds, perTurn map[string]int
}

// askKey names the deeds a computer asks a person for (a sale: the deeds it
// offers).
func askKey(offer, request []int) string {
	if len(request) == 0 {
		return "sale " + fmt.Sprint(sortedInts(offer))
	}
	return fmt.Sprint(sortedInts(request))
}

// offerValue is what an offer is worth to the person it is made to: the
// cash and the deeds the computer gives (list value, half when mortgaged),
// less the cash it asks for.
func offerValue(g *engine.Game, offer, request *engine.TradeBundle) int {
	v := 0
	if offer != nil {
		v += offer.Cash
		for _, i := range offer.Properties {
			price := engine.Board[i].Price
			if st := g.Properties[i]; st != nil && st.Mortgaged {
				price /= 2
			}
			v += price
		}
	}
	if request != nil {
		v -= request.Cash
	}
	return v
}

// beats reports whether value is clearly better than floor.
func beats(value, floor int) bool {
	return value >= floor+max(reaskMin, int(math.Ceil(reaskGain*math.Abs(float64(floor)))))
}

func turnsOf(g *engine.Game, id string) int {
	if g == nil || g.TurnCounts == nil {
		return 0
	}
	return g.TurnCounts[id]
}

func copyInts(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// offerGate is the rules for one computer's decision: it reports whether
// bot may make offer a. It may run without s.mu; building it requires s.mu.
func (s *Server) offerGate(r *room, bot string) func(g *engine.Game, a engine.Action) bool {
	l := &r.asksLedger
	l.init()
	rules := askRules{mode: r.botOffersMode(), notSelling: map[string]map[int]bool{},
		floors: copyInts(l.floors), reasks: copyInts(l.reasks), deeds: copyInts(l.deeds), perTurn: copyInts(l.perTurn)}
	for id, m := range r.meta {
		if len(m.notSelling) > 0 {
			set := map[int]bool{}
			for i := range m.notSelling {
				set[i] = true
			}
			rules.notSelling[id] = set
		}
	}
	return func(g *engine.Game, a engine.Action) bool { return rules.allowed(g, bot, a) }
}

// allowed applies the rules to one offer by bot.
func (k askRules) allowed(g *engine.Game, bot string, a engine.Action) bool {
	if a.Type != engine.ActionOfferTrade {
		return true
	}
	target := g.PlayerByID(a.TargetID)
	if target == nil || target.Kind != engine.KindHuman {
		return true
	}
	if k.mode == botOffersOff {
		return false
	}
	if k.mode == botOffersUnlimited {
		return true
	}
	var offer, request []int
	if a.Offer != nil {
		offer = a.Offer.Properties
	}
	if a.Request != nil {
		request = a.Request.Properties
	}
	for _, i := range request {
		if k.notSelling[target.ID][i] {
			return false
		}
	}
	turn := turnsOf(g, target.ID)
	if k.mode == botOffersFewer && k.perTurn[fmt.Sprint(target.ID, "|", turn)] > 0 {
		return false
	}
	key := askKey(offer, request)
	if floor, ok := k.floors[target.ID+"|"+key]; ok {
		if !beats(offerValue(g, a.Offer, a.Request), floor) || k.reasks[bot+"|"+target.ID+"|"+key] >= maxReasks {
			return false
		}
	}
	for _, i := range request {
		if at, ok := k.deeds[fmt.Sprint(target.ID, "|", i)]; ok && turn < at+deedRounds {
			return false
		}
	}
	return true
}

// noteTrades updates the ledger from one committed change of the game:
// a computer's new offer to a person is an ask; the person declining it
// (the deeds stayed put, and no counter) raises the floor for its deed set.
// Requires s.mu.
func (s *Server) noteTrades(r *room, prev, next *gameView) {
	eg, ok := r.game.(*engineGame)
	if !ok || prev == nil || next == nil {
		return
	}
	l := &r.asksLedger
	if prev.Status != "playing" && next.Status == "playing" {
		// A new game: nothing asked yet, every deed for sale again.
		*l = askLedger{}
		for _, m := range r.meta {
			m.notSelling = nil
		}
	}
	l.init()
	g := eg.g
	if o := l.open; o != nil && (next.Trade == nil || next.Trade.ID != o.tradeID) {
		l.open = nil
		countered := next.Trade != nil && next.Trade.CounterTo == o.tradeID
		if prev.Trade != nil && prev.Trade.ID == o.tradeID && !countered && !tradeSettled(prev, next) {
			key := o.person + "|" + o.key
			if floor, ok := l.floors[key]; !ok || o.value > floor {
				l.floors[key] = o.value
			}
		}
	}
	t := next.Trade
	if t == nil || next.Phase != "trade" || t.CounterTo != "" || (prev.Trade != nil && prev.Trade.ID == t.ID) {
		return
	}
	bot, person := next.player(t.ProposerID), next.player(t.TargetID)
	if bot == nil || person == nil || bot.Kind != "ai" || person.Kind != "human" {
		return
	}
	key := askKey(t.Offer.Properties, t.Request.Properties)
	if _, ok := l.floors[person.ID+"|"+key]; ok {
		l.reasks[bot.ID+"|"+person.ID+"|"+key]++
	}
	turn := turnsOf(g, person.ID)
	for _, i := range t.Request.Properties {
		l.deeds[fmt.Sprint(person.ID, "|", i)] = turn
	}
	l.perTurn[fmt.Sprint(person.ID, "|", turn)]++
	offer := &engine.TradeBundle{Cash: t.Offer.Cash, Properties: t.Offer.Properties}
	request := &engine.TradeBundle{Cash: t.Request.Cash, Properties: t.Request.Properties}
	l.open = &openAsk{bot: bot.ID, person: person.ID, key: key, tradeID: t.ID, value: offerValue(g, offer, request)}
}

// tradeSettled reports whether the pending trade of prev was accepted (its
// deeds moved).
func tradeSettled(prev, next *gameView) bool {
	t := prev.Trade
	for _, i := range t.Request.Properties {
		if p := next.Properties[fmt.Sprint(i)]; p.OwnerID != t.ProposerID {
			return false
		}
	}
	for _, i := range t.Offer.Properties {
		if p := next.Properties[fmt.Sprint(i)]; p.OwnerID != t.TargetID {
			return false
		}
	}
	return len(t.Request.Properties)+len(t.Offer.Properties) > 0
}

// withoutTrades is gameJSON with this turn's computer offer used up, so the
// Normal heuristic picks its move without one.
func withoutTrades(gameJSON []byte) []byte {
	var doc map[string]json.RawMessage
	if json.Unmarshal(gameJSON, &doc) != nil {
		return gameJSON
	}
	var turn map[string]json.RawMessage
	if json.Unmarshal(doc["turn"], &turn) != nil || turn == nil {
		return gameJSON
	}
	turn["aiTradeAttempted"] = json.RawMessage("true")
	doc["turn"], _ = json.Marshal(turn)
	out, err := json.Marshal(doc)
	if err != nil {
		return gameJSON
	}
	return out
}

// notSelling marks a deed of the person's as not for sale to computers (or
// for sale again): POST /api/rooms/{code}/not-selling {token, seatId?,
// spaceIndex, on}.
func (s *Server) notSellingDeed(code string, body *jsBody) (json.RawMessage, error) {
	if err := body.access("token"); err != nil {
		return nil, err
	}
	r, sess, err := s.authenticate(code, sessionToken(body))
	if err != nil {
		return nil, err
	}
	if !r.isEstate() {
		return nil, badRequest(msgPokerNoQuotes)
	}
	actor, err := s.actorFor(r, sess, body, "", false)
	if err != nil {
		return nil, err
	}
	var index int
	err = json.Unmarshal(body.raw("spaceIndex"), &index)
	ok := err == nil
	if !ok || index < 0 || index >= len(engine.Board) || !engine.IsOwnable(engine.Board[index].Type) {
		return nil, badRequest(msgBadNotSelling)
	}
	on := string(body.raw("on")) != "false"
	handedBack := s.noteCommand(r, sess, body, actor)
	m := r.meta[actor]
	if m == nil {
		m = &playerMeta{}
		r.meta[actor] = m
	}
	if m.notSelling == nil {
		m.notSelling = map[int]bool{}
	}
	if on {
		m.notSelling[index] = true
	} else {
		delete(m.notSelling, index)
	}
	r.updatedAt = s.now()
	s.commit(r)
	if handedBack {
		s.scheduleAI(r) // the game waits for them again
	}
	return s.stateFor(r, sess)
}

// msgBadNotSelling is the answer to a not-selling request without a deed.
const msgBadNotSelling = "Choose one of the board's deeds"

// msgBadBotOffers is the answer to an unknown botOffers setting.
const msgBadBotOffers = "Choose normal, fewer or off"

func notSellingList(m *playerMeta) []int {
	if m == nil || len(m.notSelling) == 0 {
		return nil
	}
	out := make([]int, 0, len(m.notSelling))
	for i := range m.notSelling {
		out = append(out, i)
	}
	slices.Sort(out)
	return out
}

// gateAction checks a computer's chosen move against the rules; a blocked
// offer to a person gives way to the Normal heuristic's move without a
// trade this turn.
func gateAction(g *engine.Game, gameJSON []byte, playerID string, action json.RawMessage, gate func(*engine.Game, engine.Action) bool) json.RawMessage {
	var a engine.Action
	if gate == nil || json.Unmarshal(action, &a) != nil || a.Type != engine.ActionOfferTrade || gate(g, a) {
		return action
	}
	alt, err := RealAI{}.ChooseAction(nil, withoutTrades(gameJSON), playerID)
	if err != nil || len(alt) == 0 || string(alt) == "null" {
		return action // the engine and aiFallback take it from here
	}
	if json.Unmarshal(alt, &a) == nil && a.Type == engine.ActionOfferTrade {
		return action
	}
	return alt
}
