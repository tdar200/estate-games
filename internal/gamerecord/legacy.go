package gamerecord

import (
	"encoding/json"
	"strings"

	"github.com/tdar200/estate-games/internal/engine"
)

// Games recorded before the board got its own names logged the old square,
// deck and card wording. A step's recorded hash covers the log, so a step that
// does not match as replayed is checked once more with the game written in the
// old wording, and the replay carries on in that wording: it is the state the
// server held when it recorded the step. A game that was saved before the
// rename and finished after it holds both: old lines, then new ones.

// legacyCardTexts are the card texts before the rename, by card id.
var legacyCardTexts = map[string]string{
	"chance-go":        "Advance to GO and collect $200",
	"chance-crown":     "Advance to Crown Avenue",
	"chance-rail-1":    "Advance to the nearest railroad; if owned, pay double rent",
	"chance-utility":   "Advance to the nearest utility; if owned, pay ten times the dice total",
	"chance-dividend":  "The bank pays you a $50 dividend",
	"chance-jail-card": "Get Out of Jail Free",
	"chance-back":      "Move back three spaces",
	"chance-jail":      "Go directly to jail",
	"chance-repairs":   "Street repairs: pay $25 per house and $100 per hotel",
	"chance-fine":      "Pay a $15 traffic fine",
	"chance-star":      "Advance to Galaxy Avenue",
	"chance-loan":      "Your building loan matures; collect $150",
	"chance-garden":    "Advance to Garden Avenue; collect $200 if you pass GO",
	"chance-rail-2":    "Advance to the nearest railroad; if owned, pay double rent",
	"chance-tax":       "Pay a $40 city improvement fee",
	"chance-award":     "Receive a $100 civic award",
	"chest-go":         "Advance to GO and collect $200",
	"chest-error":      "Bank error in your favor; collect $200",
	"chest-doctor":     "Pay $50 in medical fees",
	"chest-stock":      "Sell stock and collect $50",
	"chest-jail-card":  "Get Out of Jail Free",
	"chest-jail":       "Go directly to jail",
	"chest-fund":       "Holiday fund matures; collect $100",
	"chest-refund":     "Receive a $20 income tax refund",
	"chest-insurance":  "Life insurance matures; collect $100",
	"chest-hospital":   "Pay $100 in hospital fees",
	"chest-school":     "Pay $50 in school fees",
	"chest-consult":    "Receive a $25 consultancy fee",
	"chest-repairs":    "Neighborhood repairs: pay $40 per house and $115 per hotel",
	"chest-prize":      "Win $10 in a community contest",
	"chest-inherit":    "Inherit $100",
	"chest-service":    "Receive a $60 utility deposit refund",
}

type rewording struct{ now, before string }

// legacyCards maps each current card text to its old one (two cards that
// share a text today shared it before too).
var legacyCards = func() []rewording {
	var out []rewording
	for _, deck := range [][]engine.Card{engine.ChanceCards, engine.ChestCards} {
		for _, c := range deck {
			if before, ok := legacyCardTexts[c.ID]; ok && before != c.Text {
				out = append(out, rewording{c.Text, before})
			}
		}
	}
	return out
}()

// legacyEndings are whole log lines and error texts, matched at the end.
var legacyEndings = []rewording{
	{" landed on PAYDAY", " landed on GO"},
	{" landed on Headlines", " landed on Chance"},
	{" landed on Post", " landed on Community Chest"},
	{" landed on Jail", " landed on Jail / Just Visiting"},
	{" landed on Busted", " landed on Go to Jail"},
	{" passed PAYDAY and collected $200", " passed GO and collected $200"},
	{" used a Jail pass", " used a Get Out of Jail Free card"},
	{" confirmed the Headlines card", " confirmed the Chance card"},
	{"No Jail pass available", "No Get Out of Jail Free card available"},
	{"Only the player who drew the Headlines card can confirm it", "Only the player who drew the Chance card can confirm it"},
}

// legacyNames are square names and phrases that read the same wherever they
// appear (a landing, a purchase, a tax, rent, a debt's reason, the rules).
var legacyNames = strings.NewReplacer(
	"Power Station", "Electric Company",
	"Reservoir", "Water Works",
	"Stamp Duty", "Income Tax",
	"Council Tax", "Luxury Tax",
	"City Park jackpot", "Free Parking jackpot",
)

// legacyText writes one engine text (a log line, card text, debt reason or
// error) in the old wording.
func legacyText(s string) string {
	if s == "" {
		return s
	}
	for _, c := range legacyCards {
		if strings.Contains(s, c.now) {
			s = strings.ReplaceAll(s, c.now, c.before)
		}
	}
	for _, e := range legacyEndings {
		if strings.HasSuffix(s, e.now) {
			s = strings.TrimSuffix(s, e.now) + e.before
			break
		}
	}
	return legacyNames.Replace(s)
}

// legacyGame returns a copy of g in the old wording, or nil if nothing in it
// changes.
func legacyGame(g *engine.Game) *engine.Game {
	data, err := json.Marshal(g)
	if err != nil {
		return nil
	}
	c, err := LoadGame(data)
	if err != nil {
		return nil
	}
	changed := false
	reword := func(s *string) {
		if before := legacyText(*s); before != *s {
			*s, changed = before, true
		}
	}
	for i := range c.Log {
		reword(&c.Log[i].Text)
	}
	if c.LastCard != nil {
		reword(&c.LastCard.Text)
	}
	if c.PendingCard != nil {
		reword(&c.PendingCard.Text)
	}
	if c.Debt != nil {
		reword(&c.Debt.Reason)
	}
	if !changed {
		return nil
	}
	return c
}
