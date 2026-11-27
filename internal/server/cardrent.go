package server

import (
	"encoding/json"
	"regexp"
	"sort"

	"github.com/tdar200/estate-games/internal/engine"
)

// Rent from the two "nearest" Headlines cards is a multiple: double the
// railroad's rent, or ten times a fresh roll of the dice for a utility. The
// engine charges the total; screens show it as base × multiplier (RENT ×2,
// RENT ×10). The engine's JSON stays as it was (saved games hash it), so the
// server reads the multiple off the log: the payer drew the card, then
// landed on the space (perhaps passing PAYDAY on the way) and, for a
// utility, rolled the dice. The pay event (events.go) and
// publicState.lastLanding (state.go) carry it.

// cardRent is the multiple a card rent came to: amount = base × multiplier.
type cardRent struct {
	Base       int   `json:"base"`
	Multiplier int   `json:"multiplier"`
	Dice       []int `json:"dice,omitempty"` // nearest utility: the fresh roll (base is its total)
}

var reUtilityRoll = regexp.MustCompile(`^ rolled (\d) \+ (\d) = (\d+) for the utility$`)

// nearestCards maps the text of each "nearest railroad / utility" card to
// its effect.
var nearestCards = func() map[string]string {
	out := map[string]string{}
	for _, deck := range [][]engine.Card{engine.ChanceCards, engine.ChestCards} {
		for _, card := range deck {
			if card.Effect.Type == engine.EffectNearestRail || card.Effect.Type == engine.EffectNearestUtility {
				out[card.Text] = card.Effect.Type
			}
		}
	}
	return out
}()

// cardRentAt reads the card multiple of the landing logged at log[landed]
// ("<name> landed on <space>") by player p, whose rent came to amount. It is
// nil for rent not set by a card.
func (c *eventContext) cardRentAt(log []logEntryView, landed int, p *playerView, amount int) *cardRent {
	effect := ""
	for i := landed - 1; i >= 0; i-- {
		who := c.leadingPlayer(log[i].Text)
		if who == nil || who.ID != p.ID {
			continue // a system line, or another player (a trade offer)
		}
		rest := log[i].Text[len(who.Name):]
		switch rest {
		case " confirmed the Headlines card", // a Headlines card waits to be confirmed
			" passed PAYDAY and collected $200":
			continue
		}
		if m := reDrew.FindStringSubmatch(rest); m != nil {
			effect = nearestCards[m[1]]
		}
		break
	}
	switch effect {
	case engine.EffectNearestRail:
		if amount > 0 && amount%2 == 0 {
			return &cardRent{Base: amount / 2, Multiplier: 2}
		}
	case engine.EffectNearestUtility:
		for i := landed + 1; i < len(log); i++ {
			who := c.leadingPlayer(log[i].Text)
			if who == nil || who.ID != p.ID {
				continue
			}
			if m := reUtilityRoll.FindStringSubmatch(log[i].Text[len(who.Name):]); m != nil && 10*atoi(m[3]) == amount {
				return &cardRent{Base: atoi(m[3]), Multiplier: 10, Dice: []int{atoi(m[1]), atoi(m[2])}}
			}
			break
		}
	}
	return nil
}

// landedBefore is the index of the latest "landed on" line of player p
// before log[end] (-1 if none): the landing a rent line at end pays for.
func (c *eventContext) landedBefore(log []logEntryView, end int, p *playerView) int {
	for i := min(end, len(log)) - 1; i >= 0; i-- {
		if who := c.leadingPlayer(log[i].Text); who != nil && who.ID == p.ID && reLanded.MatchString(log[i].Text[len(who.Name):]) {
			return i
		}
	}
	return -1
}

// newEventContext is the context diffEvents parses log lines in.
func newEventContext(next *gameView, board []boardSpace) *eventContext {
	ctx := &eventContext{next: next, board: board, names: append([]playerView(nil), next.Players...)}
	sort.SliceStable(ctx.names, func(i, j int) bool { return len(ctx.names[i].Name) > len(ctx.names[j].Name) })
	return ctx
}

// landingCardRent adds base, multiplier and dice to publicState.lastLanding
// when its rent was set by a nearest railroad or utility card.
func landingCardRent(raw json.RawMessage, view *gameView, board []boardSpace) json.RawMessage {
	if view == nil || len(raw) == 0 || raw[0] != '{' {
		return raw
	}
	var landing struct {
		PlayerID   string `json:"playerId"`
		SpaceIndex int    `json:"spaceIndex"`
		Kind       string `json:"kind"`
		Amount     int    `json:"amount"`
	}
	if json.Unmarshal(raw, &landing) != nil || landing.Kind != "rent" {
		return raw
	}
	p := view.player(landing.PlayerID)
	if p == nil {
		return raw
	}
	ctx := newEventContext(view, board)
	landed := ctx.landedBefore(view.Log, len(view.Log), p)
	if landed < 0 {
		return raw
	}
	if m := reLanded.FindStringSubmatch(view.Log[landed].Text[len(p.Name):]); m == nil || !spaceNamed(board, landing.SpaceIndex, m[1]) {
		return raw
	}
	rent := ctx.cardRentAt(view.Log, landed, p, landing.Amount)
	if rent == nil {
		return raw
	}
	extra, err := marshalJS(rent)
	if err != nil {
		return raw
	}
	return appendFields(raw, extra)
}

// spaceNamed reports whether board space index is called name.
func spaceNamed(board []boardSpace, index int, name string) bool {
	for _, space := range board {
		if space.Index == index {
			return space.Name == name
		}
	}
	return false
}

// rentFields are the pay event's base, multiplier and dice for a card rent.
func rentFields(rent *cardRent) []eventField {
	if rent == nil {
		return nil
	}
	fields := []eventField{f("base", rent.Base), f("multiplier", rent.Multiplier)}
	if rent.Dice != nil {
		fields = append(fields, f("dice", rent.Dice))
	}
	return fields
}
