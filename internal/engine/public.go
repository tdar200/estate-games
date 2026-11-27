package engine

import "encoding/json"

// Viewer describes who a public state is rendered for (the third argument of
// publicState). Zero values match JS defaults: Role "" becomes "player".
type Viewer struct {
	Role        string // "player" or "spectator"
	Name        string
	SpectatorID string
	IsHost      bool
	Spectators  []Spectator
}

// Spectator is an entry of the public spectators list.
type Spectator struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IsHost bool   `json:"isHost"`
}

// PublicAuction is an auction plus the id of the player due to act.
type PublicAuction struct {
	Auction
	CurrentPlayerID string `json:"currentPlayerId"` // "" = null
}

// MarshalJSON implements json.Marshaler (Auction's own MarshalJSON would
// otherwise be promoted and drop currentPlayerId).
func (a PublicAuction) MarshalJSON() ([]byte, error) {
	type plain Auction
	return json.Marshal(struct {
		plain
		BidderID        nullID `json:"bidderId"`
		CurrentPlayerID nullID `json:"currentPlayerId"`
	}{plain(a.Auction), nullID(a.BidderID), nullID(a.CurrentPlayerID)})
}

// PublicGameState is the JSON document sent to clients (publicState). It
// references the live game objects, so marshal it before mutating the game.
type PublicGameState struct {
	Code            string                 `json:"code"`
	Status          string                 `json:"status"`
	Phase           string                 `json:"phase"`
	HostID          string                 `json:"hostId"`
	Players         []*Player              `json:"players"`
	CurrentIndex    int                    `json:"currentIndex"`
	CurrentPlayerID string                 `json:"currentPlayerId"`
	Properties      map[int]*PropertyState `json:"properties"`
	Turn            *Turn                  `json:"turn"`
	PendingPurchase *int                   `json:"pendingPurchase"`
	Auction         *PublicAuction         `json:"auction"`
	Debt            *Debt                  `json:"debt"`
	WinnerID        string                 `json:"winnerId"`
	LastCard        *LastCard              `json:"lastCard"`
	PendingCard     *PendingCard           `json:"pendingCard"`
	LastLanding     *Landing               `json:"lastLanding"`
	LastBankruptcy  *Bankruptcy            `json:"lastBankruptcy"`
	LastMove        *Movement              `json:"lastMove"`
	Trade           *Trade                 `json:"trade"`
	OpeningOrder    *OpeningOrder          `json:"openingOrder"`
	TurnSummary     *TurnSummary           `json:"turnSummary"`
	Settings        Settings               `json:"settings"`
	Bank            Supply                 `json:"bank"`
	Log             []LogEntry             `json:"log"`
	Version         int                    `json:"version"`
	Board           []Space                `json:"board"`
	Groups          map[string]Group       `json:"groups"`
	ViewerID        string                 `json:"viewerId"`
	ViewerRole      string                 `json:"viewerRole"`
	ViewerName      string                 `json:"viewerName"`
	SpectatorID     string                 `json:"spectatorId"`
	ViewerIsHost    bool                   `json:"viewerIsHost"`
	Spectators      []Spectator            `json:"spectators"`

	// House rules (rules.go, loans.go); all omitted for default games.
	Rules       *RuleSet   `json:"rules,omitempty"`       // effective rules, only when not the defaults
	FreeParking int        `json:"freeParking,omitempty"` // jackpot pot
	Loans       []*Loan    `json:"loans,omitempty"`       // every loan (public at the table)
	LoanOffer   *LoanOffer `json:"loanOffer,omitempty"`   // pending offer
}

// MarshalJSON implements json.Marshaler, writing "" ids/names as null.
func (s PublicGameState) MarshalJSON() ([]byte, error) {
	type plain PublicGameState
	return json.Marshal(struct {
		plain
		HostID          nullID `json:"hostId"`
		CurrentPlayerID nullID `json:"currentPlayerId"`
		WinnerID        nullID `json:"winnerId"`
		ViewerID        nullID `json:"viewerId"`
		ViewerName      nullID `json:"viewerName"`
		SpectatorID     nullID `json:"spectatorId"`
	}{
		plain(s),
		nullID(s.HostID),
		nullID(s.CurrentPlayerID),
		nullID(s.WinnerID),
		nullID(s.ViewerID),
		nullID(s.ViewerName),
		nullID(s.SpectatorID),
	})
}

// PublicState builds the client view of the game (publicState). viewerID ""
// means null. Like JS it calls AuctionCurrentPlayerID, which may normalise
// the live auction's turnPos; the returned auction copy keeps the
// pre-normalisation turnPos, exactly as the JS spread does.
func PublicState(g *Game, viewerID string, viewer Viewer) *PublicGameState {
	var auction *PublicAuction
	if g.Auction != nil {
		auction = &PublicAuction{Auction: *g.Auction}
		auction.CurrentPlayerID = AuctionCurrentPlayerID(g)
	}
	currentPlayerID := ""
	if g.Phase != PhaseDeterminingOrder && g.Phase != PhaseOrderCountdown {
		if player := CurrentPlayer(g); player != nil {
			currentPlayerID = player.ID
		}
	}
	role := viewer.Role
	if role == "" {
		role = "player"
	}
	spectators := viewer.Spectators
	if spectators == nil {
		spectators = []Spectator{}
	}
	return &PublicGameState{
		Code:            g.Code,
		Status:          g.Status,
		Phase:           g.Phase,
		HostID:          g.HostID,
		Players:         g.Players,
		CurrentIndex:    g.CurrentIndex,
		CurrentPlayerID: currentPlayerID,
		Properties:      g.Properties,
		Turn:            g.Turn,
		PendingPurchase: g.PendingPurchase,
		Auction:         auction,
		Debt:            g.Debt,
		WinnerID:        g.WinnerID,
		LastCard:        g.LastCard,
		PendingCard:     g.PendingCard,
		LastLanding:     g.LastLanding,
		LastBankruptcy:  g.LastBankruptcy,
		LastMove:        g.LastMove,
		Trade:           g.Trade,
		OpeningOrder:    g.OpeningOrder,
		TurnSummary:     g.TurnSummary,
		Settings:        g.Settings,
		Bank:            BankSupply(g),
		Log:             g.Log,
		Version:         g.Version,
		Board:           Board,
		Groups:          Groups,
		ViewerID:        viewerID,
		ViewerRole:      role,
		ViewerName:      viewer.Name,
		SpectatorID:     viewer.SpectatorID,
		ViewerIsHost:    viewer.IsHost,
		Spectators:      spectators,
		Rules:           publicRules(g),
		FreeParking:     g.FreeParking,
		Loans:           g.Loans,
		LoanOffer:       g.LoanOffer,
	}
}
