package hardai

import (
	"sort"
	"strconv"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
)

// Trade proposal memory (anti-spam).
//
// The engine cools down an AI's request for a colour group (or for railroads,
// utilities) for 4 of its turns after the target refuses it (and, since
// November 2026, a sale on the deeds offered), but a hopeless deal comes back
// every 4 turns all game, and nothing stops the next-best variant of a
// refused deal. Bots used to offer the same deal to the same bot about 20
// times a game, each one shown on the table. A bot with a tradeMemory remembers its
// own offers and how they ended, and holds back:
//
//   - the same or a similar deal (one asking for any of the same deeds, or
//     for a sale, offering any of the same deeds) to a target that refused
//     it, for dealCooldown of its own turns, doubling with every refusal of
//     the same deed by the same target (up to dealCooldownMax), so even a
//     very long game sees a hopeless deal only a few times;
//   - any offer to a target that refused it twice within targetWindow of its
//     turns, for targetBackoff turns;
//   - any offer at all on its next turn after a refusal (at most one refused
//     offer every other turn), on top of the engine's one offer per turn;
//   - any offer to a partner it already closed maxDeals deals with this game
//     (no farming one opponent's deeds away).
//
// The clock is the proposer's completed-turn count (engine TurnCounts), as
// for the engine's own cooldown.
type tradeMemory struct {
	pending  *offerMemo
	deeds    map[string]int   // "target|deed" -> turn count until which it rests
	deedNo   map[string]int   // "target|deed" -> refusals so far
	targets  map[string]int   // target -> turn count until which it rests
	refusals map[string][]int // target -> turn counts of recent refusals
	deals    map[string]int   // target -> accepted deals this game
	pause    int              // no offers while my turn count is below this
	game     string           // the game this memory belongs to
	maxDeals int              // per partner and game (<= 0: no cap)

	// gate, when set (by the server, for one decision), may hold back an
	// offer to a person (its rules for computers' offers to people).
	gate func(g *engine.Game, a engine.Action) bool
}

// offerMemo is an offer made, awaiting its outcome.
type offerMemo struct {
	target  string
	offer   []int
	request []int
	turn    int // my turn count when offered
}

const (
	dealCooldown    = 8  // own turns a refused deal (and similar ones) rests ...
	dealCooldownMax = 64 // ... doubling with each repeat refusal, up to this
	targetWindow    = 6  // two refusals within this many own turns ...
	targetBackoff   = 4  // ... rest every offer to that target this long
)

func newTradeMemory(maxDeals int) *tradeMemory {
	return &tradeMemory{maxDeals: maxDeals}
}

func (m *tradeMemory) reset(code string) {
	*m = tradeMemory{game: code, maxDeals: m.maxDeals, gate: m.gate,
		deeds: map[string]int{}, deedNo: map[string]int{}, targets: map[string]int{}, refusals: map[string][]int{}, deals: map[string]int{}}
}

func deedKey(target string, index int) string { return target + "|" + strconv.Itoa(index) }

// observe settles the pending offer once the trade is over: accepted if the
// deeds moved, otherwise refused (or never made). Call it on every decision.
func (m *tradeMemory) observe(g *engine.Game, me string) {
	if m.game != g.Code || m.deeds == nil {
		m.reset(g.Code)
	}
	p := m.pending
	if p == nil || (g.Trade != nil && g.Trade.ProposerID == me) {
		return
	}
	m.pending = nil
	accepted := true
	for _, index := range p.request {
		accepted = accepted && ownerOf(g, index) == me
	}
	for _, index := range p.offer {
		accepted = accepted && ownerOf(g, index) == p.target
	}
	if accepted {
		m.deals[p.target]++
		return
	}
	now := g.TurnCounts[me]
	keys := p.request
	if len(keys) == 0 {
		keys = p.offer // a sale: rest the deeds offered
	}
	for _, index := range keys {
		key := deedKey(p.target, index)
		m.deedNo[key]++
		m.deeds[key] = now + min(dealCooldownMax, dealCooldown<<min(m.deedNo[key]-1, 8))
	}
	recent := []int{now}
	for _, t := range m.refusals[p.target] {
		if now-t < targetWindow {
			recent = append(recent, t)
		}
	}
	m.refusals[p.target] = recent
	if len(recent) >= 2 {
		m.targets[p.target] = now + targetBackoff
	}
	m.pause = max(m.pause, p.turn+2)
}

// record notes an offer the bot is about to make.
func (m *tradeMemory) record(g *engine.Game, me string, a engine.Action) {
	if a.Type != engine.ActionOfferTrade {
		return
	}
	memo := &offerMemo{target: a.TargetID, turn: g.TurnCounts[me]}
	if a.Offer != nil {
		memo.offer = append([]int(nil), a.Offer.Properties...)
	}
	if a.Request != nil {
		memo.request = append([]int(nil), a.Request.Properties...)
	}
	m.pending = memo
}

// paused reports whether the bot makes no offer at all this turn.
func (m *tradeMemory) paused(g *engine.Game, me string) bool {
	return m.deeds != nil && m.game == g.Code && g.TurnCounts[me] < m.pause
}

// allowed reports whether offer a may be made now.
func (m *tradeMemory) allowed(g *engine.Game, me string, a engine.Action) bool {
	if a.Type == engine.ActionOfferTrade && m.gate != nil && !m.gate(g, a) {
		return false
	}
	if a.Type != engine.ActionOfferTrade || m.deeds == nil || m.game != g.Code {
		return true
	}
	now := g.TurnCounts[me]
	if now < m.pause || now < m.targets[a.TargetID] {
		return false
	}
	if m.maxDeals > 0 && m.deals[a.TargetID] >= m.maxDeals {
		return false
	}
	var keys []int
	if a.Request != nil {
		keys = a.Request.Properties
	}
	if len(keys) == 0 && a.Offer != nil {
		keys = a.Offer.Properties
	}
	for _, index := range keys {
		if now < m.deeds[deedKey(a.TargetID, index)] {
			return false
		}
	}
	return true
}

// filter drops the candidates that open with an offer not allowed now.
func (m *tradeMemory) filter(g *engine.Game, me string, cands []candidate) []candidate {
	out := cands[:0]
	for _, c := range cands {
		if len(c.actions) > 0 && !m.allowed(g, me, c.actions[0]) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// chooseNoTrade is the heuristic's move with trade offers off for this turn
// (what it plays when its offer is held back).
func (h *HeuristicBot) chooseNoTrade(g *engine.Game, me string) (engine.Action, bool) {
	view := h.vb.build(g)
	if view == nil {
		return engine.Action{}, false
	}
	markAI(view, me)
	if view.Turn != nil {
		view.Turn.AITradeAttempted = true
	}
	return toEngineAction(ai.ChooseActionParams(view, me, h.params))
}

// tradeShape identifies a trade's deeds and proposer (not its cash).
func tradeShape(t *engine.Trade) uint64 {
	h := hashString(t.ProposerID)
	for _, list := range [][]int{t.Offer.Properties, t.Request.Properties} {
		sorted := append([]int(nil), list...)
		sort.Ints(sorted)
		for _, index := range sorted {
			h = mix(h, uint64(index)+1)
		}
		h = mix(h, 0xfeed)
	}
	return h
}

// SetOfferGate makes the bot hold back the offers gate refuses (nil: none),
// for the decisions until it is changed. The server uses it for its rules
// on computers' offers to people (internal/server/asks.go); bots facing
// bots never get one.
func (b *HardBot) SetOfferGate(gate func(g *engine.Game, a engine.Action) bool) { b.mem.gate = gate }

// SetOfferGate is HardBot.SetOfferGate for Easy.
func (e *EasyBot) SetOfferGate(gate func(g *engine.Game, a engine.Action) bool) { e.mem.gate = gate }
