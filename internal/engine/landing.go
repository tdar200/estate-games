package engine

import (
	"errors"
	"math"
)

// rentOptions carries the card multipliers of resolveLanding(..., options).
type rentOptions struct {
	railroadMultiplier int // 0 = unset
	utilityMultiplier  int // 0 = unset
	// rollForUtility: the "nearest utility" card, where the payment is ten
	// times a fresh roll of the dice (classic rule; game.js reused the roll
	// that brought the player to the Headlines square).
	rollForUtility bool
	utilityDice    int // the fresh roll's total, set by resolveLanding
}

func resolveLanding(g *Game, player *Player, options rentOptions) {
	space := &Board[player.Position]
	addLog(g, player.Name+" landed on "+space.Name)

	if IsOwnable(space.Type) {
		state := g.Properties[space.Index]
		if state.OwnerID == "" {
			setLastLanding(g, player, space, "purchase", space.Price, "")
			index := space.Index
			g.PendingPurchase = &index
			g.Phase = PhaseAwaitingPurchase
			return
		}
		if state.OwnerID == player.ID || state.Mortgaged {
			owner := g.PlayerByID(state.OwnerID)
			kind := "mortgaged"
			if state.OwnerID == player.ID {
				kind = "own"
			}
			recipient := ""
			if owner != nil {
				recipient = owner.ID
			}
			setLastLanding(g, player, space, kind, 0, recipient)
			if state.Mortgaged {
				addLog(g, space.Name+" is mortgaged; no rent is collected")
			}
			finishLanding(g)
			return
		}
		owner := g.PlayerByID(state.OwnerID)
		if owner == nil || owner.Bankrupt {
			setLastLanding(g, player, space, "noRent", 0, "")
			finishLanding(g)
			return
		}
		if options.rollForUtility && space.Type == SpaceUtility && !g.legacyRules {
			rng := g.actionRNG()
			first, second := rollDie(rng), rollDie(rng)
			options.utilityDice = first + second
			addLog(g, player.Name+" rolled "+itoa(first)+" + "+itoa(second)+" = "+itoa(first+second)+" for the utility")
		}
		rent := calculateRent(g, space, options)
		setLastLanding(g, player, space, "rent", rent, owner.ID)
		charge(g, player, rent, owner.ID, space.Name+" rent", &DebtAfter{Kind: AfterPhase, Phase: postLandingPhase(g)})
		return
	}

	switch space.Type {
	case SpaceTax:
		setLastLanding(g, player, space, "tax", space.Amount, "")
		charge(g, player, space.Amount, "", space.Name, &DebtAfter{Kind: AfterPhase, Phase: postLandingPhase(g)})
	case SpaceChance, SpaceChest:
		setLastLanding(g, player, space, space.Type, 0, "")
		drawCard(g, player, space.Type)
	case SpaceGoToJail:
		setLastLanding(g, player, space, "goToJail", 0, "")
		sendToJail(g, player)
	default:
		if space.Type == SpaceParking && collectJackpot(g, player, space) { // house rule
			finishLanding(g)
			return
		}
		setLastLanding(g, player, space, "neutral", 0, "")
		finishLanding(g)
	}
}

func setLastLanding(g *Game, player *Player, space *Space, kind string, amount int, recipientID string) {
	if kind == "" {
		kind = "neutral"
	}
	g.LastLanding = &Landing{
		ID:          g.uid("landing"),
		PlayerID:    player.ID,
		SpaceIndex:  space.Index,
		Kind:        kind,
		Amount:      amount,
		RecipientID: recipientID,
		At:          g.now(),
	}
}

func calculateRent(g *Game, space *Space, options rentOptions) int {
	state := g.Properties[space.Index]
	ownerID := state.OwnerID
	switch space.Type {
	case SpaceRailroad:
		count := ownedOfType(g, SpaceRailroad, ownerID)
		multiplier := options.railroadMultiplier
		if multiplier == 0 {
			multiplier = 1
		}
		return int(25 * math.Pow(2, float64(count-1)) * float64(multiplier))
	case SpaceUtility:
		count := ownedOfType(g, SpaceUtility, ownerID)
		diceTotal := 7
		if options.utilityDice > 0 {
			diceTotal = options.utilityDice
		} else if g.Turn.Dice != nil {
			diceTotal = g.Turn.Dice[0] + g.Turn.Dice[1]
		}
		multiplier := options.utilityMultiplier
		if multiplier == 0 {
			multiplier = 4
			if count == 2 {
				multiplier = 10
			}
		}
		return diceTotal * multiplier
	}
	rent := space.Rents[state.Houses]
	if state.Houses == 0 && ownsGroup(g, ownerID, space.Group) {
		rent *= 2
	}
	return rent
}

func ownedOfType(g *Game, spaceType, ownerID string) int {
	count := 0
	for _, candidate := range Board {
		if candidate.Type == spaceType && g.Properties[candidate.Index].OwnerID == ownerID {
			count++
		}
	}
	return count
}

func ownsGroup(g *Game, playerID, group string) bool {
	for _, space := range GroupSpaces(group) {
		if g.Properties[space.Index].OwnerID != playerID {
			return false
		}
	}
	return true
}

func sendToJail(g *Game, player *Player) {
	oldPosition := player.Position
	player.Position = 10
	recordMove(g, player, oldPosition, player.Position, []int{oldPosition, player.Position})
	player.InJail = true
	player.JailTurns = 0
	g.Turn.ExtraRoll = false
	g.Phase = PhaseTurnComplete
	setLastLanding(g, player, &Board[10], "jail", 0, "")
	addLog(g, player.Name+" was sent to jail")
}

func drawCard(g *Game, player *Player, deckName string) {
	definitions := ChestCards
	deck := &g.Decks.Chest
	if deckName == SpaceChance {
		definitions = ChanceCards
		deck = &g.Decks.Chance
	}
	var cardID string
	if len(*deck) > 0 {
		cardID = (*deck)[0]
		*deck = append([]string{}, (*deck)[1:]...)
	}
	card := findCard(definitions, cardID)
	if card == nil {
		// Unreachable with valid decks. JS throws from deep inside the landing
		// chain and keeps earlier mutations; PerformAction recovers this.
		panic(thrown{errors.New("Invalid card data")})
	}
	if card.Effect.Type != EffectJailCard {
		*deck = append(*deck, cardID)
	}
	g.LastCard = &LastCard{Deck: deckName, Text: card.Text}
	addLog(g, player.Name+" drew: "+card.Text)
	if deckName == SpaceChance {
		g.PendingCard = &PendingCard{PlayerID: player.ID, Deck: deckName, CardID: cardID, Text: card.Text}
		g.Phase = PhaseCardConfirmation
		return
	}
	applyCard(g, player, card.Effect)
}

func confirmCard(g *Game, playerID string) error {
	if err := assertPhase(g, PhaseCardConfirmation); err != nil {
		return err
	}
	pending := g.PendingCard
	if pending == nil || pending.PlayerID != playerID {
		return errors.New("Only the player who drew the Headlines card can confirm it")
	}
	var player *Player
	for _, candidate := range g.Players {
		if candidate.ID == playerID && !candidate.Bankrupt {
			player = candidate
			break
		}
	}
	card := findCard(ChanceCards, pending.CardID)
	if player == nil || card == nil {
		return errors.New("Invalid card data")
	}
	g.PendingCard = nil
	addLog(g, player.Name+" confirmed the Headlines card")
	applyCard(g, player, card.Effect)
	return nil
}

func applyCard(g *Game, player *Player, effect CardEffect) {
	switch effect.Type {
	case EffectMoney:
		if effect.Amount >= 0 {
			player.Cash += effect.Amount
			finishLanding(g)
		} else {
			charge(g, player, -effect.Amount, "", "card charge", &DebtAfter{Kind: AfterPhase, Phase: postLandingPhase(g)})
		}
	case EffectMoveTo:
		moveTo(g, player, effect.Target)
		resolveLanding(g, player, rentOptions{})
	case EffectMove:
		movePlayer(g, player, effect.Steps, false)
		resolveLanding(g, player, rentOptions{})
	case EffectJail:
		sendToJail(g, player)
	case EffectJailCard:
		player.JailCards++
		finishLanding(g)
	case EffectNearestRail, EffectNearestUtility:
		targets := []int{12, 28}
		options := rentOptions{utilityMultiplier: 10, rollForUtility: true}
		if effect.Type == EffectNearestRail {
			targets = []int{5, 15, 25, 35}
			options = rentOptions{railroadMultiplier: 2}
		}
		target := targets[0]
		for _, index := range targets {
			if index > player.Position {
				target = index
				break
			}
		}
		moveTo(g, player, target)
		resolveLanding(g, player, options)
	case EffectRepairs:
		amount := 0
		for _, index := range OwnableIndexes {
			state := g.Properties[index]
			if state.OwnerID != player.ID || Board[index].Type != SpaceProperty {
				continue
			}
			if state.Houses == 5 {
				amount += effect.Hotel
			} else {
				amount += state.Houses * effect.House
			}
		}
		charge(g, player, amount, "", "building repairs", &DebtAfter{Kind: AfterPhase, Phase: postLandingPhase(g)})
	}
}
