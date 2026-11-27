package gameanalysis

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tdar200/estate-games/internal/engine"
	"github.com/tdar200/estate-games/internal/gamerecord"
	"github.com/tdar200/estate-games/internal/hardai"
)

func spaceName(index int) string {
	if index >= 0 && index < len(engine.Board) {
		return engine.Board[index].Name
	}
	return fmt.Sprintf("space %d", index)
}

func money(n int) string {
	s := fmt.Sprint(n)
	if n >= 10000 || n <= -10000 {
		var b strings.Builder
		for i, c := range s {
			if i > 0 && (len(s)-i)%3 == 0 && c != '-' {
				b.WriteByte(',')
			}
			b.WriteRune(c)
		}
		s = b.String()
	}
	return "$" + s
}

// bundleText lists one side of a trade: "Crown Avenue, Galaxy Avenue and $100".
func bundleText(b gamerecord.TradeBundle) string {
	var parts []string
	for _, index := range b.Properties {
		parts = append(parts, spaceName(index))
	}
	if b.Cash > 0 {
		parts = append(parts, money(b.Cash))
	}
	switch len(parts) {
	case 0:
		return "nothing"
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

func engineBundle(b engine.TradeBundle) gamerecord.TradeBundle {
	return gamerecord.TradeBundle{Cash: b.Cash, Properties: b.Properties}
}

// tradeText: "Ann gives Crown Avenue for Bob's Galaxy Avenue and $100".
func tradeText(t gamerecord.TradeSummary, names map[string]string) string {
	return fmt.Sprintf("%s gives %s for %s's %s", names[t.ProposerID], bundleText(t.Offer), names[t.TargetID], bundleText(t.Request))
}

// describe says what a step did, in plain English; g is the game before it.
func describe(g *engine.Game, st *gamerecord.Step, names map[string]string) string {
	who := names[st.By]
	if who == "" {
		who = st.By
	}
	switch st.Kind {
	case gamerecord.StepOrder:
		return "the turn order was settled"
	case gamerecord.StepEnd:
		return "the host ended the game"
	case gamerecord.StepResync:
		return "(steps lost in a crash; the record continues from the saved game)"
	}
	action, err := gamerecord.DecodeAction(st.Action)
	if err != nil {
		return who + " sent an invalid action"
	}
	text := func() string {
		switch action.Type {
		case engine.ActionStart:
			return who + " started the game"
		case engine.ActionRollForOrder:
			return who + " rolled for the turn order"
		case engine.ActionRoll:
			dice := st.Dice
			if len(action.Dice) == 2 {
				dice = append([][2]int{{action.Dice[0], action.Dice[1]}}, dice...)
			}
			if len(dice) > 0 {
				return fmt.Sprintf("%s rolled %d + %d", who, dice[0][0], dice[0][1])
			}
			return who + " rolled"
		case engine.ActionBuy:
			if g.PendingPurchase != nil {
				space := engine.Board[*g.PendingPurchase]
				return fmt.Sprintf("%s bought %s for %s", who, space.Name, money(space.Price))
			}
			return who + " bought"
		case engine.ActionDecline:
			if g.PendingPurchase != nil {
				space := engine.Board[*g.PendingPurchase]
				return fmt.Sprintf("%s did not buy %s (%s)", who, space.Name, money(space.Price))
			}
			return who + " did not buy"
		case engine.ActionBid:
			if g.Auction != nil {
				return fmt.Sprintf("%s bid %s for %s", who, money(action.Amount), spaceName(g.Auction.SpaceIndex))
			}
			return fmt.Sprintf("%s bid %s", who, money(action.Amount))
		case engine.ActionPassAuction:
			if g.Auction != nil {
				return fmt.Sprintf("%s dropped out of the auction for %s at %s", who, spaceName(g.Auction.SpaceIndex), money(g.Auction.CurrentBid))
			}
			return who + " passed"
		case engine.ActionOfferTrade:
			target := names[action.TargetID]
			gives, asks := "nothing", "nothing"
			if action.Offer != nil {
				gives = bundleText(engineBundle(*action.Offer))
			}
			if action.Request != nil {
				asks = bundleText(engineBundle(*action.Request))
			}
			return fmt.Sprintf("%s offered %s %s for %s", who, target, gives, asks)
		case engine.ActionCounterTrade:
			gives, asks := "nothing", "nothing"
			if action.Offer != nil {
				gives = bundleText(engineBundle(*action.Offer))
			}
			if action.Request != nil {
				asks = bundleText(engineBundle(*action.Request))
			}
			return fmt.Sprintf("%s countered: %s for %s", who, gives, asks)
		case engine.ActionAcceptTrade, engine.ActionRejectTrade:
			verb := "accepted"
			if action.Type == engine.ActionRejectTrade {
				verb = "refused"
			}
			if t := g.Trade; t != nil {
				return fmt.Sprintf("%s %s %s's offer of %s for %s", who, verb, names[t.ProposerID], bundleText(engineBundle(t.Offer)), bundleText(engineBundle(t.Request)))
			}
			return who + " " + verb + " a trade"
		case engine.ActionBuild:
			what := "a house"
			if state := g.Properties[action.SpaceIndex]; state != nil && state.Houses == 4 {
				what = "a hotel"
			}
			return fmt.Sprintf("%s built %s on %s", who, what, spaceName(action.SpaceIndex))
		case engine.ActionSellBuilding:
			return fmt.Sprintf("%s sold a building on %s", who, spaceName(action.SpaceIndex))
		case engine.ActionMortgage:
			return fmt.Sprintf("%s mortgaged %s", who, spaceName(action.SpaceIndex))
		case engine.ActionUnmortgage:
			return fmt.Sprintf("%s paid off the mortgage on %s", who, spaceName(action.SpaceIndex))
		case engine.ActionPayBail:
			return who + " paid bail to leave jail"
		case engine.ActionUseJailCard:
			return who + " used a Jail pass"
		case engine.ActionConfirmCard:
			if g.PendingCard != nil {
				return fmt.Sprintf("%s: %s", who, g.PendingCard.Text)
			}
			return who + " confirmed a card"
		case engine.ActionEndTurn:
			return who + " ended the turn"
		case engine.ActionPayDebt:
			if d := g.Debt; d != nil {
				return fmt.Sprintf("%s paid %s (%s)", who, money(d.Amount), d.Reason)
			}
			return who + " paid a debt"
		case engine.ActionBankrupt:
			if d := g.Debt; d != nil {
				to := "the bank"
				if name := names[d.CreditorID]; name != "" {
					to = name
				}
				return fmt.Sprintf("%s went bankrupt owing %s to %s", who, money(d.Amount), to)
			}
			return who + " went bankrupt"
		case engine.ActionOfferLoan:
			return fmt.Sprintf("%s offered %s a loan (%s, repay %s)", who, names[action.TargetID], money(action.Principal), money(action.Repay))
		case engine.ActionAcceptLoan:
			return who + " accepted a loan offer"
		case engine.ActionRejectLoan:
			return who + " refused a loan offer"
		case engine.ActionRepayLoan:
			return who + " repaid a loan"
		}
		return who + " did " + action.Type
	}()
	if st.Error != "" {
		text += " (refused: " + st.Error + ")"
	}
	return text
}

// policyMove describes what the Expert policy would have done in st's
// position ("" when it is the same move as actual, the step's own text). g is
// the game before the step; it is not changed.
func policyMove(policy *hardai.HeuristicBot, g *engine.Game, st *gamerecord.Step, names map[string]string, trades bool, actual string) string {
	sim := hardai.Clone(g)
	if !trades {
		noTrades(sim) // as in the rollouts that score the position
	}
	action, ok := policy.Choose(sim, st.By)
	if !ok {
		return ""
	}
	raw, err := json.Marshal(action)
	if err != nil {
		return ""
	}
	alt := gamerecord.Step{Kind: gamerecord.StepAction, By: st.By, Action: raw}
	text := describe(sim, &alt, names)
	if text == actual {
		return ""
	}
	return text
}

// luckText sums up what happened in a stretch of forced and chance steps:
// the log lines that matter most (money changing hands, cards, jail).
func luckText(steps []stepInfo, names map[string]string) string {
	var picked []string
	for _, s := range steps {
		for _, line := range s.events {
			if important(line) {
				picked = append(picked, strings.TrimSuffix(line, "."))
			}
		}
	}
	if len(picked) == 0 {
		for _, s := range steps {
			if s.text != "" {
				picked = append(picked, s.text)
			}
		}
	}
	if len(picked) > 3 {
		picked = append(picked[:3], fmt.Sprintf("%d more", len(picked)-3))
	}
	return strings.Join(picked, "; ")
}

func important(line string) bool {
	l := strings.ToLower(line)
	for _, word := range []string{"rent", "paid", "drew", "jail", "bankrupt", "won", "tax", "collect"} {
		if strings.Contains(l, word) {
			return true
		}
	}
	return false
}
