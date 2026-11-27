package engine

import "errors"

// errNoPending mirrors the TypeError JS raises for BOARD[null].index.
var errNoPending = errors.New("Cannot read properties of undefined (reading 'index')")

func buyProperty(g *Game, playerID string) error {
	player, err := assertCurrent(g, playerID)
	if err != nil {
		return err
	}
	if err := assertPhase(g, PhaseAwaitingPurchase); err != nil {
		return err
	}
	if g.PendingPurchase == nil || spaceAt(*g.PendingPurchase) == nil {
		return errNoPending
	}
	space := &Board[*g.PendingPurchase]
	state := g.Properties[space.Index]
	if state.OwnerID != "" {
		return errors.New("This property is already owned")
	}
	if player.Cash < space.Price {
		return errors.New("Not enough cash to buy this property")
	}
	player.Cash -= space.Price
	state.OwnerID = player.ID
	addLog(g, player.Name+" bought "+space.Name+" for "+money(space.Price))
	g.PendingPurchase = nil
	finishLanding(g)
	return nil
}

func declineProperty(g *Game, playerID string) error {
	if _, err := assertCurrent(g, playerID); err != nil {
		return err
	}
	if err := assertPhase(g, PhaseAwaitingPurchase); err != nil {
		return err
	}
	if g.PendingPurchase == nil || spaceAt(*g.PendingPurchase) == nil {
		// JS would also leave a half-built auction behind; Go stops before mutating.
		return errors.New("Cannot read properties of undefined (reading 'name')")
	}
	spaceIndex := *g.PendingPurchase
	g.PendingPurchase = nil
	if declineWithoutAuction(g, spaceIndex) { // house rule: auctions off
		return nil
	}
	startAuction(g, spaceIndex, postLandingPhase(g))
	return nil
}

func startAuction(g *Game, spaceIndex int, returnPhase string) {
	startIndex := g.CurrentIndex
	ordered := []string{}
	for offset := 1; offset <= len(g.Players); offset++ {
		player := g.Players[(startIndex+offset)%len(g.Players)]
		if !player.Bankrupt {
			ordered = append(ordered, player.ID)
		}
	}
	g.Auction = &Auction{
		SpaceIndex:  spaceIndex,
		ActiveIDs:   ordered,
		ReturnPhase: returnPhase,
	}
	if g.Settings.Rules.TimedAuctions() {
		g.Auction.ClockMs = g.Settings.Rules.AuctionClock * 1000
		g.Auction.EndsAt = g.now() + int64(g.Auction.ClockMs)
	}
	g.Phase = PhaseAuction
	addLog(g, Board[spaceIndex].Name+" entered an open auction")
}

// AuctionCurrentPlayerID returns the id of the player whose turn it is to bid
// or pass, or "" when nobody can act (auctionCurrentPlayerId). Like the JS
// original it normalises g.Auction.TurnPos as a side effect (idempotently).
// A timed auction has no turn: it returns "" (see AuctionBidderIDs).
func AuctionCurrentPlayerID(g *Game) string {
	auction := g.Auction
	if auction == nil || len(auction.ActiveIDs) == 0 || auction.Timed() {
		return ""
	}
	if len(auction.ActiveIDs) == 1 && auction.BidderID == auction.ActiveIDs[0] {
		return ""
	}
	for offset := 0; offset < len(auction.ActiveIDs); offset++ {
		pos := (auction.TurnPos + offset) % len(auction.ActiveIDs)
		candidate := auction.ActiveIDs[pos]
		if candidate != auction.BidderID {
			auction.TurnPos = pos
			return candidate
		}
	}
	return ""
}

// isAuctionTurn mirrors `auctionCurrentPlayerId(game) !== playerId` where a
// null result never equals a player id.
func isAuctionTurn(g *Game, playerID string) bool {
	current := AuctionCurrentPlayerID(g)
	return current != "" && current == playerID
}

// AuctionBidderIDs returns who may bid or pass now: in a turn-order auction
// the player whose turn it is, in a timed auction everyone still in except
// the leader, in turn order from TurnPos (the seat after the latest bidder),
// which is the order computers are asked in. Unlike AuctionCurrentPlayerID it
// never changes the game.
func AuctionBidderIDs(g *Game) []string {
	a := g.Auction
	if a == nil || g.Phase != PhaseAuction || len(a.ActiveIDs) == 0 {
		return nil
	}
	n := len(a.ActiveIDs)
	if !a.Timed() {
		if n == 1 && a.BidderID == a.ActiveIDs[0] {
			return nil
		}
		for offset := 0; offset < n; offset++ {
			if candidate := a.ActiveIDs[((a.TurnPos+offset)%n+n)%n]; candidate != a.BidderID {
				return []string{candidate}
			}
		}
		return nil
	}
	out := make([]string, 0, n)
	for offset := 0; offset < n; offset++ {
		if candidate := a.ActiveIDs[((a.TurnPos+offset)%n+n)%n]; candidate != a.BidderID {
			out = append(out, candidate)
		}
	}
	return out
}

func auctionIndex(a *Auction, playerID string) int {
	for i, id := range a.ActiveIDs {
		if id == playerID {
			return i
		}
	}
	return -1
}

// AuctionGoingOnceMs is when screens call "going once" in a timed auction:
// this many milliseconds before endsAt. It is presentation only; the engine
// does not use it.
const AuctionGoingOnceMs = 3000

// checkOpenBidder is the timed auction's check that playerID may bid or
// pass: they are still in and do not hold the top bid.
func checkOpenBidder(g *Game, playerID string) error {
	a := g.Auction
	if auctionIndex(a, playerID) < 0 {
		return errors.New("You are not in this auction")
	}
	if a.BidderID == playerID {
		return errors.New("You already have the top bid")
	}
	return nil
}

func bidAuction(g *Game, playerID string, amount float64) error {
	if err := assertPhase(g, PhaseAuction); err != nil {
		return err
	}
	if g.Auction.Timed() {
		return bidOpenAuction(g, playerID, amount)
	}
	if !isAuctionTurn(g, playerID) {
		return errors.New("It is not your turn to bid")
	}
	player := g.PlayerByID(playerID)
	if !isInteger(amount) || amount <= float64(g.Auction.CurrentBid) {
		return errors.New("Your bid must exceed the current bid")
	}
	if amount > float64(player.Cash) {
		return errors.New("Your bid cannot exceed your cash")
	}
	bid := int(amount)
	g.Auction.CurrentBid = bid
	g.Auction.BidderID = playerID
	addLog(g, player.Name+" bid "+money(bid))
	g.Auction.TurnPos = (g.Auction.TurnPos + 1) % len(g.Auction.ActiveIDs)
	settleAuctionIfReady(g)
	return nil
}

// bidOpenAuction is a bid in a timed auction: anyone still in may bid at any
// moment before the clock runs out, and every bid restarts the clock.
func bidOpenAuction(g *Game, playerID string, amount float64) error {
	if err := checkOpenBidder(g, playerID); err != nil {
		return err
	}
	a := g.Auction
	now := g.now()
	if now >= a.EndsAt {
		return errors.New("The auction has closed")
	}
	player := g.PlayerByID(playerID)
	if !isInteger(amount) || amount <= float64(a.CurrentBid) {
		return errors.New("Your bid must exceed the current bid")
	}
	if amount > float64(player.Cash) {
		return errors.New("Your bid cannot exceed your cash")
	}
	bid := int(amount)
	a.CurrentBid = bid
	a.BidderID = playerID
	if a.Bids == nil {
		a.Bids = map[string]int{}
	}
	a.Bids[playerID] = bid
	a.TurnPos = (auctionIndex(a, playerID) + 1) % len(a.ActiveIDs)
	a.EndsAt = now + int64(a.ClockMs)
	addLog(g, player.Name+" bid "+money(bid))
	settleAuctionIfReady(g)
	return nil
}

// passOpenAuction drops playerID out of a timed auction. The leader cannot
// pass; passing does not touch the clock.
func passOpenAuction(g *Game, playerID string) error {
	if err := checkOpenBidder(g, playerID); err != nil {
		if g.Auction.BidderID == playerID {
			return errors.New("The top bidder cannot pass")
		}
		return err
	}
	a := g.Auction
	index := auctionIndex(a, playerID)
	a.ActiveIDs = append(a.ActiveIDs[:index:index], a.ActiveIDs[index+1:]...)
	if index < a.TurnPos {
		a.TurnPos--
	}
	if len(a.ActiveIDs) > 0 {
		a.TurnPos %= len(a.ActiveIDs)
	}
	a.PassedIDs = append(a.PassedIDs, playerID)
	addLog(g, g.PlayerByID(playerID).Name+" passed in the auction")
	settleAuctionIfReady(g)
	return nil
}

func passAuction(g *Game, playerID string) error {
	if err := assertPhase(g, PhaseAuction); err != nil {
		return err
	}
	if g.Auction.Timed() {
		return passOpenAuction(g, playerID)
	}
	if !isAuctionTurn(g, playerID) {
		return errors.New("It is not your turn to act")
	}
	player := g.PlayerByID(playerID)
	auction := g.Auction
	index := -1
	for i, id := range auction.ActiveIDs {
		if id == playerID {
			index = i
			break
		}
	}
	// index is never -1 here: the current auction player is in ActiveIDs.
	auction.ActiveIDs = append(auction.ActiveIDs[:index:index], auction.ActiveIDs[index+1:]...)
	if index < auction.TurnPos {
		auction.TurnPos--
	}
	if len(auction.ActiveIDs) > 0 {
		auction.TurnPos %= len(auction.ActiveIDs)
	}
	addLog(g, player.Name+" passed in the auction")
	settleAuctionIfReady(g)
	return nil
}

func settleAuctionIfReady(g *Game) {
	auction := g.Auction
	if auction == nil {
		return
	}
	if len(auction.ActiveIDs) == 0 {
		endAuction(g, "")
		return
	}
	if len(auction.ActiveIDs) == 1 && auction.BidderID == auction.ActiveIDs[0] {
		endAuction(g, auction.BidderID)
	}
}

func endAuction(g *Game, winnerID string) {
	auction := g.Auction
	space := &Board[auction.SpaceIndex]
	if winnerID != "" {
		winner := g.PlayerByID(winnerID)
		winner.Cash -= auction.CurrentBid
		g.Properties[space.Index].OwnerID = winner.ID
		addLog(g, winner.Name+" won "+space.Name+" for "+money(auction.CurrentBid))
	} else {
		addLog(g, space.Name+" received no bids")
	}
	g.Auction = nil
	g.Phase = auction.ReturnPhase
	passTurnOfResigned(g)
}
