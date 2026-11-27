package engine

// Card effect types.
const (
	EffectMoveTo         = "moveTo"
	EffectNearestRail    = "nearestRail"
	EffectNearestUtility = "nearestUtility"
	EffectMoney          = "money"
	EffectJailCard       = "jailCard"
	EffectMove           = "move"
	EffectJail           = "jail"
	EffectRepairs        = "repairs"
)

// CardEffect describes what a card does. Only the fields relevant to Type are
// meaningful.
type CardEffect struct {
	Type   string
	Target int    // moveTo
	Amount int    // money (negative = charge)
	Steps  int    // move
	Deck   string // jailCard
	House  int    // repairs: cost per house
	Hotel  int    // repairs: cost per hotel
}

// Card is a Headlines (deck "chance") or Post (deck "chest") card.
type Card struct {
	ID     string
	Text   string
	Effect CardEffect
}

// ChanceCards is the Headlines deck definition (CHANCE_CARDS in game.js).
var ChanceCards = []Card{
	{"chance-go", "Bonus season comes early: advance to PAYDAY and collect $200", CardEffect{Type: EffectMoveTo, Target: 0}},
	{"chance-crown", "Royal visit: head straight to Crown Avenue", CardEffect{Type: EffectMoveTo, Target: 39}},
	{"chance-rail-1", "Strike called off: go to the nearest railroad; if owned, pay double rent", CardEffect{Type: EffectNearestRail}},
	{"chance-utility", "Heatwave: go to the nearest utility; if owned, pay ten times the dice total", CardEffect{Type: EffectNearestUtility}},
	{"chance-dividend", "Your shares in a local bakery pay out: collect $50", CardEffect{Type: EffectMoney, Amount: 50}},
	{"chance-jail-card", "Jail pass: keep this card to leave jail free", CardEffect{Type: EffectJailCard, Deck: "chance"}},
	{"chance-back", "Roadworks ahead: go back three spaces", CardEffect{Type: EffectMove, Steps: -3}},
	{"chance-jail", "Caught speeding on camera: off to jail", CardEffect{Type: EffectJail}},
	{"chance-repairs", "Storm damage: pay $25 per house and $100 per hotel", CardEffect{Type: EffectRepairs, House: 25, Hotel: 100}},
	{"chance-fine", "Caught in a bus lane: pay a $15 fine", CardEffect{Type: EffectMoney, Amount: -15}},
	{"chance-star", "Film premiere tonight: go to Galaxy Avenue", CardEffect{Type: EffectMoveTo, Target: 24}},
	{"chance-loan", "Council grant for your loft conversion: collect $150", CardEffect{Type: EffectMoney, Amount: 150}},
	{"chance-garden", "Flower show on Garden Avenue: go there; collect $200 if you pass PAYDAY", CardEffect{Type: EffectMoveTo, Target: 11}},
	{"chance-rail-2", "Engineering works: go to the nearest railroad; if owned, pay double rent", CardEffect{Type: EffectNearestRail}},
	{"chance-tax", "Pothole levy: pay $40 towards road repairs", CardEffect{Type: EffectMoney, Amount: -40}},
	{"chance-award", "Named Citizen of the Year: collect $100", CardEffect{Type: EffectMoney, Amount: 100}},
}

// ChestCards is the Post deck definition (CHEST_CARDS in game.js).
var ChestCards = []Card{
	{"chest-go", "Your boss writes: pay comes early. Advance to PAYDAY and collect $200", CardEffect{Type: EffectMoveTo, Target: 0}},
	{"chest-error", "Refund letter: you were overcharged. Collect $200", CardEffect{Type: EffectMoney, Amount: 200}},
	{"chest-doctor", "Dentist's bill: pay $50", CardEffect{Type: EffectMoney, Amount: -50}},
	{"chest-stock", "Shares you forgot about pay out: collect $50", CardEffect{Type: EffectMoney, Amount: 50}},
	{"chest-jail-card", "Jail pass: keep this card to leave jail free", CardEffect{Type: EffectJailCard, Deck: "chest"}},
	{"chest-jail", "Court summons in the post: off to jail", CardEffect{Type: EffectJail}},
	{"chest-fund", "Your Christmas savings club pays out: collect $100", CardEffect{Type: EffectMoney, Amount: 100}},
	{"chest-refund", "Tax rebate cheque: collect $20", CardEffect{Type: EffectMoney, Amount: 20}},
	{"chest-insurance", "An old pension pot pays out: collect $100", CardEffect{Type: EffectMoney, Amount: 100}},
	{"chest-hospital", "Vet's bill for the family dog: pay $100", CardEffect{Type: EffectMoney, Amount: -100}},
	{"chest-school", "School trip letter: pay $50", CardEffect{Type: EffectMoney, Amount: -50}},
	{"chest-consult", "A neighbour thanks you for feeding the cat: collect $25", CardEffect{Type: EffectMoney, Amount: 25}},
	{"chest-repairs", "Damp survey: pay $40 per house and $115 per hotel", CardEffect{Type: EffectRepairs, House: 40, Hotel: 115}},
	{"chest-prize", "Your ticket wins the village raffle: collect $10", CardEffect{Type: EffectMoney, Amount: 10}},
	{"chest-inherit", "A great-aunt remembers you in her will: collect $100", CardEffect{Type: EffectMoney, Amount: 100}},
	{"chest-service", "Energy bill refund: collect $60", CardEffect{Type: EffectMoney, Amount: 60}},
}

func findCard(cards []Card, id string) *Card {
	for i := range cards {
		if cards[i].ID == id {
			return &cards[i]
		}
	}
	return nil
}

func cardIDs(cards []Card) []string {
	ids := make([]string, len(cards))
	for i, card := range cards {
		ids[i] = card.ID
	}
	return ids
}

// jailCardIDs are the two Jail pass cards, Headlines ("chance") first.
var jailCardIDs = [...]struct{ deck, id string }{{SpaceChance, "chance-jail-card"}, {SpaceChest, "chest-jail-card"}}

// returnJailCard puts a Jail pass card that was used (or freed by
// a bankruptcy to the bank) back at the bottom of its deck. Held cards are
// only counted (Player.JailCards), so the card returned is whichever one is
// missing from its deck, Headlines first; the two are interchangeable.
func returnJailCard(g *Game) {
	for _, card := range jailCardIDs {
		deck := &g.Decks.Chest
		if card.deck == SpaceChance {
			deck = &g.Decks.Chance
		}
		if !containsString(*deck, card.id) {
			*deck = append(*deck, card.id)
			return
		}
	}
}
