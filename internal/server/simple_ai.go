package server

import (
	"encoding/json"
	"strconv"
	"sync"
)

// SimpleAI is a small rule-of-thumb computer player that only needs the
// game JSON. It is the default when Config.AI is nil (tests) and keeps games
// moving with any engine that follows the src/game.js JSON shape.
type SimpleAI struct {
	once  sync.Once
	board map[int]boardSpace
}

func (a *SimpleAI) loadBoard(game Game) {
	a.once.Do(func() {
		a.board = map[int]boardSpace{}
		raw, err := game.PublicState("", Viewer{})
		if err != nil {
			return
		}
		var doc struct {
			Board []boardSpace `json:"board"`
		}
		if json.Unmarshal(raw, &doc) == nil {
			for _, space := range doc.Board {
				a.board[space.Index] = space
			}
		}
	})
}

func act(kind string) json.RawMessage {
	return json.RawMessage(`{"type":"` + kind + `"}`)
}

// ChooseAction implements AI.
func (a *SimpleAI) ChooseAction(game Game, gameJSON []byte, playerID string) (json.RawMessage, error) {
	a.loadBoard(game)
	var g gameView
	if err := json.Unmarshal(gameJSON, &g); err != nil {
		return nil, err
	}
	me := g.player(playerID)
	if me == nil || me.Kind != "ai" || me.Bankrupt {
		return nil, nil
	}
	current := ""
	if g.CurrentIndex >= 0 && g.CurrentIndex < len(g.Players) {
		current = g.Players[g.CurrentIndex].ID
	}
	switch g.Phase {
	case "card_confirmation":
		if g.PendingCard != nil && g.PendingCard.PlayerID == playerID {
			return act("confirm_card"), nil
		}
	case "trade":
		if g.Trade != nil && g.Trade.TargetID == playerID {
			return act("reject_trade"), nil
		}
	case "loan_offer":
		if g.LoanOffer != nil && g.LoanOffer.TargetID == playerID {
			return act("reject_loan"), nil
		}
	case "auction":
		if due, _ := nextAIPlayer(&g); due != playerID || g.Auction == nil {
			return nil, nil
		}
		price := a.board[g.Auction.SpaceIndex].Price
		bid := g.Auction.CurrentBid + 10
		if bid*5 <= price*4 && me.Cash-bid >= 300 {
			return json.RawMessage(`{"type":"bid","amount":` + strconv.Itoa(bid) + `}`), nil
		}
		return act("pass_auction"), nil
	case "debt":
		if g.Debt == nil || g.Debt.PayerID != playerID {
			return nil, nil
		}
		if me.Cash >= g.Debt.Amount {
			return act("pay_debt"), nil
		}
		return a.raiseCash(&g, playerID), nil
	case "awaiting_roll":
		if current != playerID {
			return nil, nil
		}
		if me.InJail && me.JailCards > 0 {
			return act("use_jail_card"), nil
		}
		return act("roll"), nil
	case "awaiting_purchase":
		if current != playerID || g.PendingPurchase == nil {
			return nil, nil
		}
		if me.Cash-a.board[*g.PendingPurchase].Price >= 200 {
			return act("buy"), nil
		}
		return act("decline"), nil
	case "turn_complete":
		if current == playerID {
			return act("end_turn"), nil
		}
	}
	return nil, nil
}

// raiseCash sells the tallest building, then mortgages, then gives up.
func (a *SimpleAI) raiseCash(g *gameView, playerID string) json.RawMessage {
	best, bestHouses := -1, 0
	mortgage := -1
	for key, prop := range g.Properties {
		index, err := strconv.Atoi(key)
		if err != nil || prop.OwnerID != playerID {
			continue
		}
		if prop.Houses > bestHouses || (prop.Houses == bestHouses && prop.Houses > 0 && index > best) {
			best, bestHouses = index, prop.Houses
		}
		if !prop.Mortgaged && prop.Houses == 0 && (mortgage < 0 || index < mortgage) {
			mortgage = index
		}
	}
	if best >= 0 && bestHouses > 0 {
		return json.RawMessage(`{"type":"sell_building","spaceIndex":` + strconv.Itoa(best) + `}`)
	}
	if mortgage >= 0 {
		return json.RawMessage(`{"type":"mortgage","spaceIndex":` + strconv.Itoa(mortgage) + `}`)
	}
	return act("bankrupt")
}

// QuoteTrade implements AI.
func (a *SimpleAI) QuoteTrade(_ Game, _ []byte, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return nil, badRequest("Quotes are only available from an active AI player")
}
