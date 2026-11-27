package ai

import (
	"encoding/json"
	"fmt"
	"math"
)

// Params are the heuristic's tunable knobs. DefaultParams reproduces src/ai.js
// exactly (the fixture and engine-trace parity tests run with it); other
// values change the strategy without changing its structure. Every field is a
// float64 so a tuner can treat the set as one vector (see Knobs); integer
// knobs (cash amounts, counts) are rounded where they are used.
//
// The knobs touch the decisions as follows:
//
//   - value: how the AI prices spaces and positions (auctions, purchases,
//     trades, the leader test): colour-group multipliers, railroad and
//     utility values, the EV-to-cash factor, denial and leader weights;
//   - reserve: the cash kept back (adaptiveReserve), by game stage, and its
//     adjustments when opponents are developed;
//   - auction/buy: scales on the auction ceiling and on the purchase test;
//   - build: the build ordering multipliers and the reserve kept when
//     building;
//   - mortgage: reserves for lifting mortgages, and which holdings to
//     mortgage or which houses to sell when cash is short;
//   - jail: when to leave jail early;
//   - trade: reserves, the minimum gain of a proposal, and the fairness share
//     the AI demands before accepting.
//
// Trade proposals are priced against the target's acceptance test with
// DefaultParams (the AI assumes its opponents play the default heuristic); its
// own gain and its answers to trades use its own Params.
type Params struct {
	// value
	GroupComplete     [groupCount]float64 // COLOR_GROUP_STRATEGY[g].complete
	GroupPartial      [groupCount]float64 // COLOR_GROUP_STRATEGY[g].partial
	BaseValue         float64             // auction value of a colour space that builds nothing (x price)
	EVCashFactor      float64             // STRATEGIC_EV_CASH_FACTOR
	RailroadEV        float64             // railroad EV gain to cash (auction value)
	RailroadCapScale  float64             // scales RAILROAD_HARD_CAPS
	UtilityBase       float64             // utility base value (x price)
	UtilityEV         float64             // utility EV gain to cash
	DenialScale       float64             // scales DENIAL_WEIGHTS (auctions, blocker value)
	LeaderScale       float64             // scales LEADER_WEIGHTS
	HouseValue        float64             // portfolio value of a building (x cost)
	MortgagedValue    float64             // portfolio value of a mortgaged space (x price)
	RailroadPortfolio float64             // scales the railroad portfolio totals
	UtilityPortfolio  float64             // scales the utility portfolio totals
	AcquisitionOpen   float64             // unowned spaces that keep the game in its acquisition stage

	// reserve
	ReserveScale      [4]float64 // RESERVE_SCHEDULE row scale per stage (acquisition, formation, development, endgame)
	ReserveHostileDev float64    // extra reserve when an opponent has three houses on a group
	ReserveLuxury     float64    // extra reserve facing a green or dark blue hotel with little to sell
	ReserveMin        float64    // floor of the reserve
	CompleteOrange    float64    // reserve discount when the purchase completes orange
	CompleteStrong    float64    // ... red, yellow or dark blue

	// auction / buy
	AuctionScale float64 // scales the auction ceiling's value
	BuyScale     float64 // scales the value in the buy-at-list-price test

	// build
	BuildTierLow   float64 // ordering weight of tiers 1-2
	BuildTierThree float64 // ... of the third house
	BuildTierHigh  float64 // ... of the fourth house and the hotel
	ShortageBonus  float64 // build bonus while the bank runs short of houses
	HotelPenalty   float64 // hotel weight while houses are short and an opponent can build
	BuildReserve   float64 // scales the reserve kept when building

	// mortgage
	UnmortgageLow    float64 // extra reserve to lift a mortgage on a loose holding
	UnmortgageMid    float64 // ... on a nearly complete group or two railroads
	UnmortgageHigh   float64 // ... on three railroads, both utilities or a complete group
	LiquidateStrong  float64 // strategic loss of mortgaging a strong complete group
	LiquidateWeak    float64 // ... of another complete group
	LiquidateNear    float64 // ... of a nearly complete group
	SellThreePenalty float64 // reluctance to sell the third house
	SellHighPenalty  float64 // reluctance to sell the fourth house or hotel

	// jail
	JailOpenSpaces  float64 // leave jail while at least this many spaces are unowned
	JailHitFraction float64 // stay if the worst rent exceeds this share of the cushion
	JailIncomeRatio float64 // stay if hostile income exceeds own income times this (with a developed opponent)

	// trade
	TradeReserve      float64 // AI_TRADE_RESERVE
	MinTradeGain      float64 // AI_MIN_TRADE_GAIN
	FairnessShare     float64 // share of the proposer's gain the target demands
	FairnessShare2P   float64 // ... with two players left
	LeaderPremium     float64 // extra share against the leader (x leader weight)
	ThreatShare2P     float64 // share demanded against a three-house threat, two players
	ThreatShareLeader float64 // ... against a leader's three-house threat
	LiquidityFactor   float64 // cash the target keeps after a trade (x reserve)
	LiquidityMin      float64 // ... at least this much
	AcceptMargin      float64 // extra gain the target demands (absolute)
}

// DefaultParams reproduces src/ai.js.
var DefaultParams = Params{
	GroupComplete:     [groupCount]float64{1.25, 1.65, 1.9, 2.25, 2.1, 1.9, 1.6, 1.35},
	GroupPartial:      [groupCount]float64{1.05, 1.15, 1.2, 1.3, 1.25, 1.2, 1.1, 1.05},
	BaseValue:         1.1,
	EVCashFactor:      strategicEVCashFactor,
	RailroadEV:        18,
	RailroadCapScale:  1,
	UtilityBase:       0.85,
	UtilityEV:         12,
	DenialScale:       1,
	LeaderScale:       1,
	HouseValue:        0.7,
	MortgagedValue:    0.55,
	RailroadPortfolio: 1,
	UtilityPortfolio:  1,
	AcquisitionOpen:   8,

	ReserveScale:      [4]float64{1, 1, 1, 1},
	ReserveHostileDev: 150,
	ReserveLuxury:     250,
	ReserveMin:        80,
	CompleteOrange:    140,
	CompleteStrong:    100,

	AuctionScale: 1,
	BuyScale:     1,

	BuildTierLow:   1.15,
	BuildTierThree: 1.8,
	BuildTierHigh:  0.78,
	ShortageBonus:  1.18,
	HotelPenalty:   0.45,
	BuildReserve:   1,

	UnmortgageLow:    250,
	UnmortgageMid:    100,
	UnmortgageHigh:   0,
	LiquidateStrong:  45,
	LiquidateWeak:    20,
	LiquidateNear:    12,
	SellThreePenalty: 3,
	SellHighPenalty:  1.35,

	JailOpenSpaces:  8,
	JailHitFraction: 0.6,
	JailIncomeRatio: 1.25,

	TradeReserve:      aiTradeReserve,
	MinTradeGain:      aiMinTradeGain,
	FairnessShare:     0.3,
	FairnessShare2P:   0.4,
	LeaderPremium:     0.25,
	ThreatShare2P:     0.9,
	ThreatShareLeader: 0.75,
	LiquidityFactor:   0.7,
	LiquidityMin:      180,
	AcceptMargin:      0,
}

// Knob describes one tunable parameter: its name, the decision group it
// belongs to, its default and the range a tuner should search.
type Knob struct {
	Name    string
	Group   string
	Default float64
	Min     float64
	Max     float64
	field   func(p *Params) *float64
}

// Get returns the knob's value in p.
func (k Knob) Get(p *Params) float64 { return *k.field(p) }

// Set stores v (clamped to the knob's range) in p.
func (k Knob) Set(p *Params, v float64) { *k.field(p) = math.Max(k.Min, math.Min(k.Max, v)) }

// Knobs lists every tunable parameter in a fixed order (the tuner's vector
// layout).
var Knobs = buildKnobs()

func buildKnobs() []Knob {
	var ks []Knob
	add := func(name, group string, lo, hi float64, f func(p *Params) *float64) {
		ks = append(ks, Knob{Name: name, Group: group, Default: *f(&DefaultParams), Min: lo, Max: hi, field: f})
	}
	for g := 0; g < groupCount; g++ {
		g := g
		add("complete."+groupNames[g], "value", 0.8, 4, func(p *Params) *float64 { return &p.GroupComplete[g] })
	}
	for g := 0; g < groupCount; g++ {
		g := g
		add("partial."+groupNames[g], "value", 0.7, 2.2, func(p *Params) *float64 { return &p.GroupPartial[g] })
	}
	add("baseValue", "value", 0.6, 1.8, func(p *Params) *float64 { return &p.BaseValue })
	add("evCashFactor", "value", 1, 7, func(p *Params) *float64 { return &p.EVCashFactor })
	add("railroadEV", "value", 4, 45, func(p *Params) *float64 { return &p.RailroadEV })
	add("railroadCapScale", "value", 0.4, 2.2, func(p *Params) *float64 { return &p.RailroadCapScale })
	add("utilityBase", "value", 0.3, 1.5, func(p *Params) *float64 { return &p.UtilityBase })
	add("utilityEV", "value", 2, 35, func(p *Params) *float64 { return &p.UtilityEV })
	add("denialScale", "value", 0, 2.5, func(p *Params) *float64 { return &p.DenialScale })
	add("leaderScale", "value", 0, 2.5, func(p *Params) *float64 { return &p.LeaderScale })
	add("houseValue", "value", 0.2, 1.3, func(p *Params) *float64 { return &p.HouseValue })
	add("mortgagedValue", "value", 0.2, 1, func(p *Params) *float64 { return &p.MortgagedValue })
	add("railroadPortfolio", "value", 0.4, 2, func(p *Params) *float64 { return &p.RailroadPortfolio })
	add("utilityPortfolio", "value", 0.4, 2, func(p *Params) *float64 { return &p.UtilityPortfolio })
	add("acquisitionOpen", "value", 2, 20, func(p *Params) *float64 { return &p.AcquisitionOpen })
	for i, stage := range []string{"acquisition", "formation", "development", "endgame"} {
		i := i
		add("reserve."+stage, "reserve", 0.1, 2.5, func(p *Params) *float64 { return &p.ReserveScale[i] })
	}
	add("reserveHostileDev", "reserve", 0, 450, func(p *Params) *float64 { return &p.ReserveHostileDev })
	add("reserveLuxury", "reserve", 0, 700, func(p *Params) *float64 { return &p.ReserveLuxury })
	add("reserveMin", "reserve", 0, 300, func(p *Params) *float64 { return &p.ReserveMin })
	add("completeOrange", "reserve", 0, 400, func(p *Params) *float64 { return &p.CompleteOrange })
	add("completeStrong", "reserve", 0, 400, func(p *Params) *float64 { return &p.CompleteStrong })
	add("auctionScale", "auction", 0.5, 1.7, func(p *Params) *float64 { return &p.AuctionScale })
	add("buyScale", "auction", 0.5, 1.7, func(p *Params) *float64 { return &p.BuyScale })
	add("buildTierLow", "build", 0.2, 4, func(p *Params) *float64 { return &p.BuildTierLow })
	add("buildTierThree", "build", 0.2, 5, func(p *Params) *float64 { return &p.BuildTierThree })
	add("buildTierHigh", "build", 0.1, 4, func(p *Params) *float64 { return &p.BuildTierHigh })
	add("shortageBonus", "build", 0.6, 2.5, func(p *Params) *float64 { return &p.ShortageBonus })
	add("hotelPenalty", "build", 0.05, 2, func(p *Params) *float64 { return &p.HotelPenalty })
	add("buildReserve", "build", 0, 2.5, func(p *Params) *float64 { return &p.BuildReserve })
	add("unmortgageLow", "mortgage", -100, 900, func(p *Params) *float64 { return &p.UnmortgageLow })
	add("unmortgageMid", "mortgage", -150, 700, func(p *Params) *float64 { return &p.UnmortgageMid })
	add("unmortgageHigh", "mortgage", -200, 500, func(p *Params) *float64 { return &p.UnmortgageHigh })
	add("liquidateStrong", "mortgage", 0, 200, func(p *Params) *float64 { return &p.LiquidateStrong })
	add("liquidateWeak", "mortgage", 0, 120, func(p *Params) *float64 { return &p.LiquidateWeak })
	add("liquidateNear", "mortgage", 0, 80, func(p *Params) *float64 { return &p.LiquidateNear })
	add("sellThreePenalty", "mortgage", 0.5, 10, func(p *Params) *float64 { return &p.SellThreePenalty })
	add("sellHighPenalty", "mortgage", 0.3, 5, func(p *Params) *float64 { return &p.SellHighPenalty })
	add("jailOpenSpaces", "jail", 1, 24, func(p *Params) *float64 { return &p.JailOpenSpaces })
	add("jailHitFraction", "jail", 0.05, 2, func(p *Params) *float64 { return &p.JailHitFraction })
	add("jailIncomeRatio", "jail", 0.3, 4, func(p *Params) *float64 { return &p.JailIncomeRatio })
	add("tradeReserve", "trade", 0, 700, func(p *Params) *float64 { return &p.TradeReserve })
	add("minTradeGain", "trade", 0, 250, func(p *Params) *float64 { return &p.MinTradeGain })
	add("fairnessShare", "trade", 0, 1.2, func(p *Params) *float64 { return &p.FairnessShare })
	add("fairnessShare2P", "trade", 0, 1.5, func(p *Params) *float64 { return &p.FairnessShare2P })
	add("leaderPremium", "trade", 0, 1.2, func(p *Params) *float64 { return &p.LeaderPremium })
	add("threatShare2P", "trade", 0.2, 2, func(p *Params) *float64 { return &p.ThreatShare2P })
	add("threatShareLeader", "trade", 0.2, 2, func(p *Params) *float64 { return &p.ThreatShareLeader })
	add("liquidityFactor", "trade", 0, 2, func(p *Params) *float64 { return &p.LiquidityFactor })
	add("liquidityMin", "trade", 0, 600, func(p *Params) *float64 { return &p.LiquidityMin })
	add("acceptMargin", "trade", -100, 300, func(p *Params) *float64 { return &p.AcceptMargin })
	return ks
}

// Vector returns p's knob values in Knobs order.
func (p *Params) Vector() []float64 {
	out := make([]float64, len(Knobs))
	for i, k := range Knobs {
		out[i] = k.Get(p)
	}
	return out
}

// ParamsFromVector builds Params from knob values in Knobs order (each
// clamped to its range).
func ParamsFromVector(v []float64) (Params, error) {
	if len(v) != len(Knobs) {
		return Params{}, fmt.Errorf("ai: %d knob values, want %d", len(v), len(Knobs))
	}
	p := DefaultParams
	for i, k := range Knobs {
		k.Set(&p, v[i])
	}
	return p, nil
}

// MarshalJSON writes the knobs as a {"name": value} object.
func (p Params) MarshalJSON() ([]byte, error) {
	m := make(map[string]float64, len(Knobs))
	for _, k := range Knobs {
		m[k.Name] = k.Get(&p)
	}
	return json.Marshal(m)
}

// UnmarshalJSON reads a {"name": value} object; knobs it does not name keep
// their default values, unknown names are an error.
func (p *Params) UnmarshalJSON(data []byte) error {
	var m map[string]float64
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	out := DefaultParams
	byName := make(map[string]Knob, len(Knobs))
	for _, k := range Knobs {
		byName[k.Name] = k
	}
	for name, v := range m {
		k, ok := byName[name]
		if !ok {
			return fmt.Errorf("ai: unknown knob %q", name)
		}
		*k.field(&out) = v
	}
	*p = out
	return nil
}

// roundInt rounds a knob holding a cash amount or a count.
func roundInt(v float64) int { return int(math.Round(v)) }

// scaleInt scales an integer amount by a knob (exactly the amount at 1).
func scaleInt(v int, f float64) int {
	if f == 1 {
		return v
	}
	return int(math.Round(float64(v) * f))
}
