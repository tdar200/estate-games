package server

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/hardai"
)

// A computer keeps its word: when someone asks a computer for its price
// (trade-quote) and then offers that deal, the computer accepts it. Quotes come
// from internal/ai while Easy/Hard computers judge offers with internal/hardai,
// so without this a Hard computer could refuse the price it had just named.
// A quote is good for the rest of the turn it was given in, as long as the
// deeds it covers (and their colour groups) are as they were: mortgaging one,
// building, or completing a set since makes the computer judge afresh.

type tradeQuoteRecord struct {
	proposerID string
	targetID   string
	offer      []int // properties the proposer gives (sorted)
	request    []int // properties the proposer asks for (sorted)
	net        int   // cash the proposer must pay overall (offerCash - requestCash)
	turn       int
	deeds      string // quoteFingerprint when quoted
}

// rememberQuote records a quote given to proposerID. Requires s.mu.
func (r *room) rememberQuote(proposerID string, request, quote []byte) {
	var req struct {
		TargetID          string `json:"targetId"`
		OfferProperties   []int  `json:"offerProperties"`
		RequestProperties []int  `json:"requestProperties"`
	}
	var got struct {
		OfferCash   int    `json:"offerCash"`
		RequestCash int    `json:"requestCash"`
		TargetID    string `json:"targetId"`
	}
	if json.Unmarshal(request, &req) != nil || json.Unmarshal(quote, &got) != nil {
		return
	}
	target := got.TargetID
	if target == "" {
		target = req.TargetID
	}
	if p := r.last.player(target); p == nil || p.Kind != "ai" {
		return
	}
	rec := tradeQuoteRecord{
		proposerID: proposerID, targetID: target,
		offer: sortedInts(req.OfferProperties), request: sortedInts(req.RequestProperties),
		net: got.OfferCash - got.RequestCash, turn: r.last.turnNumber(),
	}
	rec.deeds = r.quoteFingerprint(r.last, rec.offer, rec.request)
	kept := r.quotes[:0]
	for _, q := range r.quotes {
		if q.turn == rec.turn && !(q.proposerID == rec.proposerID && q.targetID == rec.targetID &&
			slices.Equal(q.offer, rec.offer) && slices.Equal(q.request, rec.request)) {
			kept = append(kept, q)
		}
	}
	r.quotes = append(kept, rec)
}

// quotedTrade reports whether the open trade to computer playerID meets a
// price that computer quoted this turn.
func (r *room) quotedTrade(view *gameView, playerID string) bool {
	t := view.Trade
	if view.Phase != "trade" || t == nil || t.TargetID != playerID {
		return false
	}
	offer, request := sortedInts(t.Offer.Properties), sortedInts(t.Request.Properties)
	for _, q := range r.quotes {
		if q.turn == view.turnNumber() && q.proposerID == t.ProposerID && q.targetID == playerID &&
			slices.Equal(q.offer, offer) && slices.Equal(q.request, request) &&
			t.Offer.Cash-t.Request.Cash >= q.net && (len(offer) > 0 || len(request) > 0) &&
			q.deeds == r.quoteFingerprint(view, offer, request) {
			return true
		}
	}
	return false
}

// quoteFingerprint describes the deeds of a deal and every deed in their
// colour groups: owner, mortgage and buildings.
func (r *room) quoteFingerprint(view *gameView, offer, request []int) string {
	group := map[int]string{}
	for _, space := range r.board {
		switch {
		case space.Group != "":
			group[space.Index] = space.Group
		case space.Type == "railroad" || space.Type == "utility":
			group[space.Index] = space.Type // rent grows with how many you own
		}
	}
	wanted := map[int]bool{}
	for _, list := range [][]int{offer, request} {
		for _, index := range list {
			wanted[index] = true
			if g := group[index]; g != "" {
				for other, og := range group {
					if og == g {
						wanted[other] = true
					}
				}
			}
		}
	}
	indexes := make([]int, 0, len(wanted))
	for index := range wanted {
		indexes = append(indexes, index)
	}
	slices.Sort(indexes)
	var b strings.Builder
	for _, index := range indexes {
		p := view.Properties[strconv.Itoa(index)]
		fmt.Fprintf(&b, "%d:%s:%t:%d;", index, p.OwnerID, p.Mortgaged, p.Houses)
	}
	return b.String()
}

func sortedInts(in []int) []int {
	out := slices.Clone(in)
	slices.Sort(out)
	if out == nil {
		out = []int{}
	}
	return out
}

// A computer that names its own price (Hard, Expert, Grandmaster) is never
// bound to the Normal heuristic's quote: its trade-quote comes from the
// computer itself (hardai.HardBot.Quote: the least it would take, from its
// own trade search), computed on a copy of the game outside the server lock,
// and that price is remembered and honoured like any other quote. The same
// computer answers a person's offer by the same price (accepted at or above
// it, a counter-offer at it below; hardai/quote.go). Easy and Normal keep the
// Normal heuristic's quote, which is their own judgement.

// msgQuoteNoDeal is the answer when the computer takes the deal at no price
// (a computer's own reason replaces it when it gives one; every such answer
// starts with "No deal", which the clients show as "Won't trade").
const msgQuoteNoDeal = "No deal at any price."

// msgQuoteMovedOn is the answer when the game changed while it thought.
const msgQuoteMovedOn = "The game moved on; ask again"

// botQuote answers a trade-quote addressed to a computer that prices deals
// itself; handled is false for every other target (the Normal quote then
// applies). It is called with s.mu held and releases it while the computer
// thinks (in one of the thinking slots, like a decision), taking it back
// before returning.
func (s *Server) botQuote(r *room, actor string, rawBody []byte) (quote json.RawMessage, handled bool, err error) {
	var req struct {
		TargetID          string `json:"targetId"`
		OfferProperties   []int  `json:"offerProperties"`
		RequestProperties []int  `json:"requestProperties"`
	}
	if json.Unmarshal(rawBody, &req) != nil {
		return nil, false, nil // the Normal path reports a malformed request
	}
	m := r.meta[req.TargetID]
	if m == nil {
		return nil, false, nil
	}
	bot, ok := m.bot.(*engineBot)
	if !ok || !bot.quotesOwnPrice() {
		return nil, false, nil
	}
	// The Normal quote's checks (whose turn, the phase, who owns the deeds)
	// apply as they are; only its price is replaced.
	if _, err := s.ai.QuoteTrade(r.game, r.lastJSON, actor, rawBody); err != nil && !normalJudgement(err) {
		return nil, true, err
	}
	g, ok := gameCopy(r.game)
	if !ok || r.last == nil {
		return nil, false, nil
	}
	// The price holds while the turn and the deeds involved (with their
	// colour groups) are unchanged, as for any remembered quote; other
	// changes (a seat reconnecting, a chat line) do not void it.
	offer, request := sortedInts(req.OfferProperties), sortedInts(req.RequestProperties)
	turn, deeds := r.last.turnNumber(), r.quoteFingerprint(r.last, offer, request)
	s.mu.Unlock()
	q, stopped := s.thinkQuote(bot, g, req.TargetID, actor, req.OfferProperties, req.RequestProperties)
	s.mu.Lock()
	if stopped || s.closed || s.rooms[r.code] != r || r.last == nil ||
		r.last.turnNumber() != turn || r.quoteFingerprint(r.last, offer, request) != deeds {
		return nil, true, badRequest(msgQuoteMovedOn)
	}
	if !q.OK {
		reason := q.Reason
		if reason == msgQuoteMovedOn {
			return nil, true, badRequest(reason)
		}
		if !strings.HasPrefix(reason, "No deal") {
			reason = msgQuoteNoDeal
		}
		return nil, true, badRequest(reason)
	}
	offerCash, requestCash := max(0, q.Net), max(0, -q.Net)
	quote, _ = json.Marshal(map[string]any{"offerCash": offerCash, "requestCash": requestCash, "targetId": req.TargetID})
	r.rememberQuote(actor, rawBody, quote)
	return quote, true, nil
}

// thinkQuote runs the computer's pricing in a thinking slot (without s.mu).
func (s *Server) thinkQuote(bot *engineBot, g *engine.Game, me, proposer string, offer, request []int) (q hardai.Quote, stopped bool) {
	select {
	case s.thinkSlots <- struct{}{}:
	case <-s.ctx.Done():
		return hardai.Quote{}, true
	}
	defer func() { <-s.thinkSlots }()
	defer func() {
		if recover() != nil {
			q = hardai.Quote{Reason: msgQuoteNoDeal}
		}
	}()
	return bot.quoteDeal(g, me, proposer, offer, request), false
}

// normalJudgement reports whether a Normal-quote error is the heuristic's
// judgement of the deal (it would not take it, or the proposer cannot pay
// its price) rather than a check of the request; a computer that prices
// deals itself makes its own judgement.
func normalJudgement(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "not willing") || strings.Contains(msg, "enough cash to complete")
}
