package hardai

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tdar200/estate-games/internal/ai"
	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/valuenet"
	"github.com/tdar200/estate-games/internal/valuenet2"
)

// Level is a computer difficulty.
type Level int

// Difficulty levels.
const (
	// Easy is the heuristic with deliberate, seeded mistakes: it underbids
	// in auctions, sometimes declines good purchases, skips builds and
	// leaves jail at bad moments.
	Easy Level = iota
	// Normal is the heuristic AI (internal/ai) exactly.
	Normal
	// Hard decides the important moves by Monte Carlo rollouts.
	Hard
	// Expert is Hard's search with a self-play-tuned heuristic as its
	// baseline and rollout policy, and a learned value network scoring
	// short rollouts (see expert.go).
	Expert
	// Grandmaster is Expert without the ladder handicaps, with deeper and
	// more conservative trade answers, more thinking on big decisions and
	// a mix of opponent models (see grandmaster.go).
	Grandmaster
)

func (l Level) String() string {
	switch l {
	case Easy:
		return "easy"
	case Normal:
		return "normal"
	case Hard:
		return "hard"
	case Expert:
		return "expert"
	case Grandmaster:
		return "grandmaster"
	}
	return "level(" + strconv.Itoa(int(l)) + ")"
}

// ParseLevel parses "easy", "normal", "hard", "expert" or "grandmaster"
// (case-insensitive).
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "easy":
		return Easy, nil
	case "normal", "medium", "heuristic":
		return Normal, nil
	case "hard":
		return Hard, nil
	case "expert":
		return Expert, nil
	case "grandmaster", "gm":
		return Grandmaster, nil
	}
	return 0, fmt.Errorf("unknown level %q (want easy, normal, hard, expert or grandmaster)", s)
}

// Options tune the bots. The zero value gives the defaults.
type Options struct {
	// Budget is a safety cap on the wall-clock time a Hard or Expert
	// decision may take, counted from the start of Choose (no new rollout
	// starts after it; the first round always completes): 0 means 250ms;
	// negative means no time limit (only Rollouts caps the work, which makes
	// the bot fully deterministic given its seed). The strength of the levels
	// is set by Rollouts: on a normal machine the round count, not the
	// clock, ends a decision, so a level plays the same on a fast server, a
	// busy one or a laptop (the budget only binds on a badly overloaded one).
	Budget time.Duration
	// Rollouts caps the rounds per decision (a round is one rollout of every
	// candidate with the same dice); 0 means hardRollouts (Expert:
	// expertRollouts).
	Rollouts int
	// MinRounds is the evidence needed before the Hard bot overrides the
	// heuristic or prunes a candidate; 0 means 4.
	MinRounds int
	// Horizon is the rollout length in rounds of play (one turn per active
	// player); 0 means 16; negative means no full round: a rollout
	// stops when the next turn starts.
	Horizon int
	// Workers is the number of goroutines a Hard decision uses; 0 means
	// GOMAXPROCS.
	Workers int
	// Confidence is the paired z-score by which an alternative must beat the
	// heuristic's choice before Hard plays it; 0 means 1.
	Confidence float64
	// MaxCandidates caps the plans evaluated per decision; 0 means 10.
	MaxCandidates int
	// Eval selects the rollout evaluation (EvalNetWorth or EvalStrategic).
	Eval int
	// Disable lists decision kinds Hard leaves to the heuristic (for
	// ablation studies): a bit set of Decide* flags.
	Disable Decisions
	// NoMarketTrades stops Hard from proposing plain purchases and sales
	// (buying railroads and utilities, selling spare holdings above price at
	// the AI owner's acceptance price); it then only proposes trades that
	// complete its colour groups.
	NoMarketTrades bool
	// MistakeRate scales how often Easy blunders; 0 means 1 (the default
	// rates), negative means never.
	MistakeRate float64
	// Params are the heuristic's knobs for the deciding seat: its baseline
	// choice and its moves in rollouts (nil = ai.DefaultParams; Expert
	// defaults to the tuned knobs it ships with).
	Params *ai.Params
	// OpponentParams are the knobs rollouts play the other seats with (nil =
	// ai.DefaultParams, the Normal heuristic).
	OpponentParams *ai.Params
	// Net scores rollouts with Eval EvalValueNet (Expert defaults to the
	// embedded model).
	Net *valuenet.Net
	// Net2 is Grandmaster's 8-slot network (internal/valuenet2): when set,
	// its rollouts are scored by it (every game of up to 8 players fits);
	// nil = the shipped one if any, otherwise Net. Static trade screening
	// still uses Net.
	Net2 *valuenet2.Net
	// NoHumanPrices answers people's trade offers by search like a bot's
	// (before November 2026; analysis only). By default Hard, Expert and
	// Grandmaster answer a person by their own price (quote.go).
	NoHumanPrices bool
	// NoNetTrades stops Expert from proposing the trade ideas its value
	// network screens (nettrades.go); it then proposes only Hard's kinds.
	NoNetTrades bool
	// NetTradeScreen and NetTradeKeep size the screened trade ideas: how many
	// are priced, and how many join the search (0 = 8 and 3).
	NetTradeScreen, NetTradeKeep int
	// MaxDeals caps the trades a Hard or Expert bot proposes that one partner
	// accepts, per game (so it cannot strip one opponent of its deeds); 0
	// means defaultMaxDeals, negative means no cap. Refused offers have their
	// own cooldowns (trademem.go).
	MaxDeals int
	// GM holds Grandmaster's own settings (nil for the other levels).
	GM *GrandmasterOptions
	// Styles, if set, are the opponent models rollouts draw each other seat's
	// knobs from (Grandmaster); nil = OpponentParams for every seat.
	Styles []OpponentStyle
	// OnDecision, if set, is called after every Hard or Easy decision. It
	// must be safe for concurrent use if bots run concurrently.
	OnDecision func(DecisionInfo)
}

// Hard defaults (used when the Options fields are zero; Expert's are in
// expert.go). An Expert facing more players than its value network reads
// searches with these too.
const (
	hardHorizon    = 16  // rollout rounds
	hardRollouts   = 12  // rounds per decision: sets Hard's strength (docs/ai/EXPERT.md, "Difficulty ladder")
	hardConfidence = 1.0 // paired z-score to override the heuristic
)

func (o Options) withDefaults() Options {
	if o.Budget == 0 {
		o.Budget = 250 * time.Millisecond
	}
	if o.Rollouts <= 0 {
		o.Rollouts = hardRollouts
	}
	if o.MinRounds <= 0 {
		o.MinRounds = 4
	}
	if o.Horizon == 0 {
		o.Horizon = hardHorizon
	}
	if o.Workers <= 0 {
		o.Workers = defaultWorkers()
	}
	if o.Confidence <= 0 {
		o.Confidence = hardConfidence
	}
	if o.MaxCandidates <= 1 {
		o.MaxCandidates = 10
	}
	if o.MistakeRate == 0 {
		o.MistakeRate = 1
	}
	if o.MaxDeals == 0 {
		o.MaxDeals = defaultMaxDeals
	}
	return o
}

// defaultMaxDeals is Options.MaxDeals' default.
const defaultMaxDeals = 2

// DecisionInfo describes one decision (for timing and diagnostics).
type DecisionInfo struct {
	PlayerID   string
	Phase      string
	Action     string        // type of the returned action
	Duration   time.Duration // time spent in Choose
	MonteCarlo bool          // rollouts were run
	Planned    bool          // continuation of an earlier decision's plan
	Rounds     int           // rollout rounds run
	Rollouts   int           // rollouts played (rounds x live candidates)
	Overhead   time.Duration // time before the first rollout (heuristic choice, candidate generation)
	Candidates int           // candidates evaluated
	Chosen     string        // candidate name (Hard) or mistake (Easy)
	Kind       string        // Hard: the chosen candidate's kind ("build max", "trade cash", ...)
	Heuristic  bool          // the choice equals the heuristic's
}

// NewBot returns a bot of the given level. seed makes Easy's mistakes and
// Hard's rollouts reproducible.
func NewBot(level Level, seed int64, opts Options) Bot {
	switch level {
	case Easy:
		return NewEasyBot(seed, opts)
	case Hard:
		return NewHardBot(seed, opts)
	case Expert:
		return NewExpertBot(seed, opts)
	case Grandmaster:
		return NewGrandmasterBot(seed, opts)
	}
	return NewHeuristicBot(false)
}

// EasyBot is the heuristic with seeded deliberate mistakes. The mistakes are
// the kind a casual player makes (stopping short in an auction, passing on a
// purchase, not building this turn, a wrong jail call, a slightly bad trade),
// and each is bounded so that none hands a game away: Easy never lets its
// own colour group go in an auction or a purchase, never skips building two
// turns running, and its trade mistakes are capped (see badTradeBounded).
type EasyBot struct {
	h    *HeuristicBot
	seed uint64
	rate float64
	on   func(DecisionInfo)

	auctionRef   auctionKey
	auctionCap   int // max bid this auction (-1 = no cap)
	skipTurnRef  turnKey
	skipBuild    bool
	skippedBuild int // own turn count of the last turn it skipped building (-1: none)

	game      string
	badTrades map[string]int // proposer -> bad trades accepted from them this game
	mem       *tradeMemory   // its own offers (anti-spam)
}

// Mistake probabilities at MistakeRate 1. They were raised by half in
// November 2026, when the bounds below made each mistake cheaper: Easy then
// sits about 165 Elo under Normal (Normal wins 46% against three Easy bots,
// 33% before), a step like the others on the ladder (docs/ai/EXPERT.md).
const (
	easyUnderbid     = 0.9   // per auction: cap bids well below the heuristic's
	easySkipPurchase = 0.375 // decline a purchase the heuristic would make
	easySkipBuild    = 0.75  // per turn (never two running): build nothing
	easyJailMistake  = 0.525 // do the opposite of the heuristic's jail choice
	easyBadTrade     = 0.45  // accept a trade the heuristic would refuse (within bounds)
	easySkipTrade    = 0.9   // let a profitable trade idea go
)

// Bounds of Easy's trade mistakes.
const (
	easyTradeSlack    = 0.25 // accepts only deals the heuristic takes with this share of what Easy gives added in cash
	easyTradeSlackMax = 200  // ... and never more than this in cash
	easyBadTradesMax  = 1    // per proposer and game
	easyBailReserve   = 150  // pays bail by mistake only with this much left after
)

// NewEasyBot returns an Easy bot.
func NewEasyBot(seed int64, opts Options) *EasyBot {
	rate := opts.MistakeRate
	if rate == 0 {
		rate = 1
	}
	return &EasyBot{h: NewHeuristicBot(false), seed: uint64(seed), rate: max(0, rate), on: opts.OnDecision,
		skippedBuild: -1, mem: newTradeMemory(0)}
}

// chance draws a reproducible coin for this decision.
func (e *EasyBot) chance(g *engine.Game, playerID string, salt uint64, p float64) bool {
	return e.coin(p, mix(uint64(g.Version), hashString(playerID), salt))
}

// coin is true with probability p (times the mistake rate) for this key.
func (e *EasyBot) coin(p float64, key uint64) bool {
	if p*e.rate <= 0 {
		return false
	}
	r := splitMix{state: mix(e.seed, key)}
	return r.Float64() < p*e.rate
}

// tradeCoin decides the bad-trade mistake once per offer and turn: its key is
// the proposer, the deeds on both sides and the turn, not the cash or the
// game version, so offering the same deeds again (with any cash) in the same
// turn gets the same answer instead of a fresh 30% chance.
func (e *EasyBot) tradeCoin(g *engine.Game, me string, t *engine.Trade) bool {
	return e.coin(easyBadTrade, mix(hashString(me), tradeShape(t), uint64(totalTurns(g)), 5))
}

// badTradeBounded reports whether accepting t (which the heuristic refuses)
// is a mistake Easy may make: a deal at most a little worse than one it
// would take. It must not
//   - give the proposer a complete colour group,
//   - break up a colour group Easy owns,
//   - be one the heuristic refuses even with easyTradeSlack of what Easy
//     gives (deeds at list price, mortgaged at half, plus cash) added to
//     Easy's side in cash (at most easyTradeSlackMax),
//
// and Easy makes at most easyBadTradesMax such mistakes per proposer and game.
func (e *EasyBot) badTradeBounded(g *engine.Game, me string, t *engine.Trade) bool {
	if e.badTrades[t.ProposerID] >= easyBadTradesMax {
		return false
	}
	proposerAfter := map[int]bool{}
	for _, index := range engine.OwnableIndexes {
		if ownerOf(g, index) == t.ProposerID {
			proposerAfter[index] = true
		}
	}
	for _, index := range t.Offer.Properties {
		delete(proposerAfter, index)
	}
	given := t.Request.Cash
	for _, index := range t.Request.Properties {
		proposerAfter[index] = true
		price := engine.Board[index].Price
		if st := g.Properties[index]; st != nil && st.Mortgaged {
			price /= 2
		}
		given += price
	}
	for _, index := range t.Request.Properties {
		grp := spaceGroup[index]
		if grp < 0 {
			continue
		}
		if ownsGroup(g, me, grp) {
			return false // breaks Easy's own group
		}
		complete := true
		for _, i := range groupMembers[grp] {
			complete = complete && proposerAfter[i]
		}
		if complete {
			return false // completes the proposer's group
		}
	}
	slack := min(easyTradeSlackMax, int(easyTradeSlack*float64(given)))
	if slack <= 0 {
		return false
	}
	view := e.h.vb.build(g)
	if view == nil || view.Trade == nil {
		return false
	}
	markAI(view, me)
	view.Trade.Offer.Cash += slack
	for _, p := range view.Players {
		if p.ID == t.ProposerID {
			p.Cash += slack // the proposer can pay it: only the price changes
		}
	}
	return ai.AcceptsTradeParams(view, me, e.h.params)
}

// Choose implements Bot.
func (e *EasyBot) Choose(g *engine.Game, playerID string) (engine.Action, bool) {
	start := time.Now()
	action, ok, mistake := e.choose(g, playerID)
	if e.on != nil {
		e.on(DecisionInfo{PlayerID: playerID, Phase: g.Phase, Action: action.Type, Duration: time.Since(start),
			Chosen: mistake, Heuristic: mistake == ""})
	}
	return action, ok
}

func (e *EasyBot) choose(g *engine.Game, me string) (engine.Action, bool, string) {
	if e.game != g.Code || e.badTrades == nil {
		e.game, e.badTrades, e.skippedBuild = g.Code, map[string]int{}, -1
	}
	e.mem.observe(g, me)
	h, ok := e.h.Choose(g, me)
	if !ok {
		return h, ok, ""
	}
	if h.Type == engine.ActionOfferTrade && !e.mem.allowed(g, me, h) {
		// The same or a similar offer was just refused: play on without it.
		if alt, ok := e.h.chooseNoTrade(g, me); ok {
			h = alt
		}
	}
	switch h.Type {
	case engine.ActionBid:
		key := auctionKey{turn: turnOf(g), space: g.Auction.SpaceIndex, turns: totalTurns(g)}
		if e.auctionRef != key {
			e.auctionRef = key
			e.auctionCap = -1
			// Never on a space that completes its own colour group.
			if !completesFor(g, me, g.Auction.SpaceIndex) && e.chance(g, me, 1, easyUnderbid) {
				// Stop between 40% and 90% of the list price.
				r := splitMix{state: mix(e.seed, uint64(g.Version), 7)}
				e.auctionCap = int(float64(engine.Board[g.Auction.SpaceIndex].Price) * (0.4 + 0.5*r.Float64()))
			}
		}
		if e.auctionCap >= 0 && h.Amount > e.auctionCap {
			return engine.Action{Type: engine.ActionPassAuction}, true, "underbid"
		}
	case engine.ActionBuy:
		pending := g.PendingPurchase
		if pending != nil && !completesFor(g, me, *pending) && e.chance(g, me, 2, easySkipPurchase) {
			return engine.Action{Type: engine.ActionDecline}, true, "skip purchase"
		}
	case engine.ActionOfferTrade:
		if e.chance(g, me, 6, easySkipTrade) {
			if g.Phase == engine.PhaseAwaitingRoll {
				return engine.Action{Type: engine.ActionRoll}, true, "skip trade"
			}
			return engine.Action{Type: engine.ActionEndTurn}, true, "skip trade"
		}
		e.mem.record(g, me, h)
	case engine.ActionBuild:
		if key := turnOf(g); key != e.skipTurnRef {
			e.skipTurnRef = key
			own := g.TurnCounts[me]
			// Never two turns running, so Easy always gets round to building.
			e.skipBuild = e.skippedBuild != own-1 && e.chance(g, me, 3, easySkipBuild)
			if e.skipBuild {
				e.skippedBuild = own
			}
		}
		if e.skipBuild {
			if g.Phase == engine.PhaseAwaitingRoll {
				return engine.Action{Type: engine.ActionRoll}, true, "skip build"
			}
			return engine.Action{Type: engine.ActionEndTurn}, true, "skip build"
		}
	case engine.ActionRoll, engine.ActionPayBail, engine.ActionUseJailCard:
		p := g.PlayerByID(me)
		if p != nil && p.InJail && g.Phase == engine.PhaseAwaitingRoll && e.chance(g, me, 4, easyJailMistake) {
			if h.Type == engine.ActionRoll && p.Cash >= 50+easyBailReserve {
				return engine.Action{Type: engine.ActionPayBail}, true, "jail"
			}
			if h.Type != engine.ActionRoll {
				return engine.Action{Type: engine.ActionRoll}, true, "jail"
			}
		}
	case engine.ActionRejectTrade:
		if t := g.Trade; t != nil && t.TargetID == me && e.tradeCoin(g, me, t) && e.badTradeBounded(g, me, t) {
			e.badTrades[t.ProposerID]++
			return engine.Action{Type: engine.ActionAcceptTrade}, true, "bad trade"
		}
	}
	return h, true, ""
}

func itoa(v int) string { return strconv.Itoa(v) }

// Decisions is a set of Hard decision kinds (Options.Disable).
type Decisions uint

// Decision kinds.
const (
	DecideAuction   Decisions = 1 << iota // auction ceilings
	DecidePurchase                        // buy / decline
	DecideTrade                           // answering trades
	DecideRoll                            // before rolling (jail, management)
	DecideComplete                        // end of turn (building, mortgages)
	DecideProposals                       // trades Hard proposes itself
)

var decisionNames = map[string]Decisions{
	"auction": DecideAuction, "purchase": DecidePurchase, "trade": DecideTrade,
	"roll": DecideRoll, "complete": DecideComplete, "proposals": DecideProposals,
}

// ParseDecisions parses a comma-separated list of decision kinds (auction,
// purchase, trade, roll, complete, proposals).
func ParseDecisions(s string) (Decisions, error) {
	var d Decisions
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		bit, ok := decisionNames[part]
		if !ok {
			return 0, fmt.Errorf("unknown decision kind %q", part)
		}
		d |= bit
	}
	return d, nil
}
