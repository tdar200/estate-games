package engine

import "errors"

func beginTurn(g *Game) {
	player := CurrentPlayer(g)
	g.TurnSummary = buildTurnSummary(g, player.ID)
	g.Phase = PhaseAwaitingRoll
	g.Turn = &Turn{PlayerID: player.ID, Movements: []*Movement{}}
	g.PendingPurchase = nil
	g.Auction = nil
	g.Debt = nil
	g.Trade = nil
	g.LastCard = nil
	g.PendingCard = nil
	g.LastLanding = nil
	addLog(g, player.Name+"'s turn")
	loansAtTurnStart(g, player) // house rule: settle loans due now; no-op without loans
}

func playerSnapshot(g *Game) map[string]PlayerSpot {
	snapshot := make(map[string]PlayerSpot, len(g.Players))
	for _, player := range g.Players {
		snapshot[player.ID] = PlayerSpot{Position: player.Position, Cash: player.Cash}
	}
	return snapshot
}

func buildTurnSummary(g *Game, playerID string) *TurnSummary {
	snapshot, ok := g.TurnSummaryBaselines[playerID]
	if !ok || snapshot == nil {
		return nil
	}
	changes := []TurnSummaryEntry{}
	for _, player := range g.Players {
		before, ok := snapshot[player.ID]
		if !ok {
			before = PlayerSpot{Position: player.Position, Cash: player.Cash}
		}
		entry := TurnSummaryEntry{
			PlayerID:     player.ID,
			FromPosition: before.Position,
			ToPosition:   player.Position,
			CashBefore:   before.Cash,
			CashAfter:    player.Cash,
			CashDelta:    player.Cash - before.Cash,
		}
		if entry.FromPosition != entry.ToPosition || entry.CashDelta != 0 {
			changes = append(changes, entry)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	return &TurnSummary{PlayerID: playerID, At: g.now(), Players: changes}
}

func completeTurnSummary(g *Game) {
	if player := CurrentPlayer(g); player != nil {
		g.TurnSummaryBaselines[player.ID] = playerSnapshot(g)
	}
}

func roll(g *Game, playerID string, rng RNG, action *Action) error {
	player, err := assertCurrent(g, playerID)
	if err != nil {
		return err
	}
	if err := assertPhase(g, PhaseAwaitingRoll); err != nil {
		return err
	}
	var dice []int
	if g.Settings.DiceMode == DiceModeChoice && player.Kind == KindHuman {
		if dice, err = validateChosenDice(action); err != nil {
			return err
		}
	} else {
		first := rollDie(rng)
		second := rollDie(rng)
		dice = []int{first, second}
	}
	total := dice[0] + dice[1]
	isDouble := dice[0] == dice[1]
	g.Turn.Dice = dice
	addLog(g, player.Name+" rolled "+itoa(dice[0])+" + "+itoa(dice[1])+" = "+itoa(total))

	if player.InJail {
		handleJailRoll(g, player, dice, isDouble)
		return nil
	}

	if isDouble {
		g.Turn.DoublesCount++
		if g.Turn.DoublesCount >= 3 {
			addLog(g, player.Name+" rolled doubles three times in a row")
			sendToJail(g, player)
			return nil
		}
		g.Turn.ExtraRoll = true
	} else {
		g.Turn.ExtraRoll = false
	}

	movePlayer(g, player, total, true)
	resolveLanding(g, player, rentOptions{})
	return nil
}

func validateChosenDice(action *Action) ([]int, error) {
	values, ok := action.diceValues()
	if !ok || len(values) != 2 {
		return nil, errors.New("Choose a value for both dice")
	}
	dice := make([]int, len(values))
	for i, value := range values {
		if !isInteger(value) || value < 1 || value > 6 {
			return nil, errors.New("Each die must be between 1 and 6")
		}
		dice[i] = int(value)
	}
	return dice, nil
}

func handleJailRoll(g *Game, player *Player, dice []int, isDouble bool) {
	total := dice[0] + dice[1]
	g.Turn.ExtraRoll = false
	if isDouble {
		player.InJail = false
		player.JailTurns = 0
		addLog(g, player.Name+" rolled doubles and left jail")
		movePlayer(g, player, total, true)
		resolveLanding(g, player, rentOptions{})
		return
	}

	player.JailTurns++
	if player.JailTurns < 3 {
		addLog(g, player.Name+" did not roll doubles and remains in jail ("+itoa(player.JailTurns)+"/3)")
		g.Phase = PhaseTurnComplete
		return
	}

	charge(g, player, 50, "", "third failed doubles roll; jail fee", &DebtAfter{Kind: AfterJailMove, Dice: dice})
}

func payBail(g *Game, playerID string) error {
	player, err := assertCurrent(g, playerID)
	if err != nil {
		return err
	}
	if err := assertPhase(g, PhaseAwaitingRoll); err != nil {
		return err
	}
	if !player.InJail {
		return errors.New("The current player is not in jail")
	}
	if player.Cash < 50 && !g.legacyRules {
		// Paying bail is voluntary: with less than $50 it would trap the
		// player in a debt they can't leave (rolling or a card still work).
		return errors.New("Not enough cash to pay bail")
	}
	charge(g, player, 50, "", "jail fee", &DebtAfter{Kind: AfterLeaveJail})
	return nil
}

func useJailCard(g *Game, playerID string) error {
	player, err := assertCurrent(g, playerID)
	if err != nil {
		return err
	}
	if err := assertPhase(g, PhaseAwaitingRoll); err != nil {
		return err
	}
	if !player.InJail {
		return errors.New("The current player is not in jail")
	}
	if player.JailCards < 1 {
		return errors.New("No Jail pass available")
	}
	player.JailCards--
	player.InJail = false
	player.JailTurns = 0
	addLog(g, player.Name+" used a Jail pass")
	if !g.legacyRules {
		returnJailCard(g)
	}
	return nil
}

func wrapPosition(position int) int {
	return ((position % BoardSize) + BoardSize) % BoardSize
}

func movePlayer(g *Game, player *Player, steps int, collectGo bool) {
	oldPosition := player.Position
	rawPosition := oldPosition + steps
	if collectGo && steps > 0 && rawPosition >= BoardSize {
		player.Cash += 200
		addLog(g, player.Name+" passed PAYDAY and collected $200")
	}
	player.Position = wrapPosition(rawPosition)
	path := []int{oldPosition}
	direction := 1
	if steps < 0 {
		direction = -1
	}
	distance := steps
	if distance < 0 {
		distance = -distance
	}
	for offset := 1; offset <= distance; offset++ {
		path = append(path, wrapPosition(oldPosition+offset*direction))
	}
	recordMove(g, player, oldPosition, player.Position, path)
}

func moveTo(g *Game, player *Player, target int) {
	oldPosition := player.Position
	if target < player.Position {
		player.Cash += 200
		addLog(g, player.Name+" passed PAYDAY and collected $200")
	}
	player.Position = target
	recordMove(g, player, oldPosition, player.Position, boardPath(oldPosition, target))
}

func boardPath(from, to int) []int {
	path := []int{from}
	current := from
	for current != to && len(path) <= BoardSize {
		current = (current + 1) % BoardSize
		path = append(path, current)
	}
	return path
}

func recordMove(g *Game, player *Player, from, to int, path []int) {
	movement := &Movement{
		ID:       g.uid("move"),
		PlayerID: player.ID,
		From:     from,
		To:       to,
		Path:     path,
		At:       g.now(),
	}
	g.LastMove = movement
	if g.Turn != nil && g.Turn.Movements != nil {
		g.Turn.Movements = append(g.Turn.Movements, movement)
	}
}

func postLandingPhase(g *Game) string {
	if g.Turn.ExtraRoll {
		return PhaseAwaitingRoll
	}
	return PhaseTurnComplete
}

func finishLanding(g *Game) {
	g.Phase = postLandingPhase(g)
}

func endTurn(g *Game, playerID string) error {
	if _, err := assertCurrent(g, playerID); err != nil {
		return err
	}
	if err := assertPhase(g, PhaseTurnComplete); err != nil {
		return err
	}
	if g.TurnCounts == nil {
		g.TurnCounts = map[string]int{}
	}
	g.TurnCounts[playerID]++
	advanceTurn(g)
	return nil
}

func advanceTurn(g *Game) {
	completeTurnSummary(g)
	for offset := 1; offset <= len(g.Players); offset++ {
		nextIndex := (g.CurrentIndex + offset) % len(g.Players)
		if !g.Players[nextIndex].Bankrupt {
			g.CurrentIndex = nextIndex
			beginTurn(g)
			return
		}
	}
}
