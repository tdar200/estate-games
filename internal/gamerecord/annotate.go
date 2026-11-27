package gamerecord

import (
	"encoding/json"
	"slices"
	"sort"

	"github.com/tdar200/estate-games/internal/engine"
)

// TurnNumber is 1 + the turns every player has completed (the number the
// screens show as "turn").
func TurnNumber(g *engine.Game) int {
	n := 1
	for _, c := range g.TurnCounts {
		n += c
	}
	return n
}

// before is what Annotate remembers of the state before a step.
type before struct {
	phase     string
	auction   *engine.Auction
	trade     *engine.Trade
	loanOffer *engine.LoanOffer
	bankrupt  map[string]bool
	turn      int
	owner     map[int]string
	cash      map[string]int
}

func remember(g *engine.Game) before {
	b := before{phase: g.Phase, turn: TurnNumber(g), bankrupt: map[string]bool{}, owner: map[int]string{}, cash: map[string]int{}}
	if g.Auction != nil {
		a := *g.Auction
		b.auction = &a
	}
	if g.Trade != nil {
		t := *g.Trade
		b.trade = &t
	}
	if g.LoanOffer != nil {
		o := *g.LoanOffer
		b.loanOffer = &o
	}
	for _, p := range g.Players {
		b.bankrupt[p.ID] = p.Bankrupt
		b.cash[p.ID] = p.Cash
	}
	for index, state := range g.Properties {
		if state != nil {
			b.owner[index] = state.OwnerID
		}
	}
	return b
}

// actionType returns a step's action type ("" for other kinds).
func actionType(st *Step) string {
	if st.Kind != StepAction {
		return ""
	}
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(st.Action, &head)
	return head.Type
}

// ActionType returns a step's action type ("" if it is not an action).
func (st *Step) ActionType() string { return actionType(st) }

// Annotate replays rec and fills in its derived fields: Verified and
// VerifyError, Turns, TurnOrder, Trades, Auctions, Loans, Gaps, the
// bankruptcy order and standings of Outcome (creating one with reason
// fallbackReason if there is none), and Final when it is missing. A record
// that does not replay keeps the summaries of the steps before the mismatch,
// but its turns, winner and standings come from its recorded final state.
func Annotate(rec *Record, fallbackReason string) {
	rec.Trades, rec.Auctions, rec.Loans, rec.Gaps, rec.TurnOrder = nil, nil, nil, nil, nil
	rec.Verified, rec.VerifyError = false, ""
	r, err := NewReplayer(rec)
	if err != nil {
		rec.VerifyError = err.Error()
		return
	}
	openTrade, openAuction, openLoan := -1, -1, -1
	outStep := map[string]int{}
	var outOrder []string
	var replayErr error
	for !r.Done() {
		prev := remember(r.G)
		st, err := r.Step()
		if err != nil {
			replayErr = err
			break
		}
		g := r.G
		if st.Kind == StepResync {
			rec.Gaps = append(rec.Gaps, st.N)
			openTrade, openAuction, openLoan = -1, -1, -1
			continue
		}
		kind := actionType(st)
		ok := st.Error == ""

		// Trades: an offer opens one; the target's answer (or anything that
		// clears it) closes it.
		if kind == engine.ActionOfferTrade && ok && g.Trade != nil {
			rec.Trades = append(rec.Trades, TradeSummary{
				Step: st.N, Turn: prev.turn, ProposerID: g.Trade.ProposerID, TargetID: g.Trade.TargetID,
				Offer: bundle(g.Trade.Offer), Request: bundle(g.Trade.Request), Answer: "open",
			})
			openTrade = len(rec.Trades) - 1
		} else if kind == engine.ActionCounterTrade && ok && g.Trade != nil {
			// A counter-offer answers the open offer and opens one of its own.
			counterOf := 0
			if openTrade >= 0 {
				t := &rec.Trades[openTrade]
				t.Answer, t.AnswerStep, t.AnsweredBy = "countered", st.N, st.Source
				counterOf = t.Step
			}
			rec.Trades = append(rec.Trades, TradeSummary{
				Step: st.N, Turn: prev.turn, ProposerID: g.Trade.ProposerID, TargetID: g.Trade.TargetID,
				Offer: bundle(g.Trade.Offer), Request: bundle(g.Trade.Request), Answer: "open", CounterOf: counterOf,
			})
			openTrade = len(rec.Trades) - 1
		} else if openTrade >= 0 && prev.trade != nil && g.Trade == nil {
			t := &rec.Trades[openTrade]
			switch {
			case kind == engine.ActionAcceptTrade && ok:
				t.Answer = "accepted"
			case kind == engine.ActionRejectTrade && ok:
				t.Answer = "rejected"
			default:
				t.Answer = "cancelled"
			}
			t.AnswerStep, t.AnsweredBy = st.N, st.Source
			openTrade = -1
		}

		// Loans (house rule).
		if kind == engine.ActionOfferLoan && ok && g.LoanOffer != nil {
			rec.Loans = append(rec.Loans, LoanSummary{Step: st.N, ProposerID: g.LoanOffer.ProposerID, TargetID: g.LoanOffer.TargetID, Action: st.Action, Answer: "open"})
			openLoan = len(rec.Loans) - 1
		} else if openLoan >= 0 && prev.loanOffer != nil && g.LoanOffer == nil {
			l := &rec.Loans[openLoan]
			switch {
			case kind == engine.ActionAcceptLoan && ok:
				l.Answer = "accepted"
			case kind == engine.ActionRejectLoan && ok:
				l.Answer = "rejected"
			default:
				l.Answer = "cancelled"
			}
			l.AnswerStep = st.N
			openLoan = -1
		}

		// Auctions: opened by a decline, closed when the phase moves on.
		if prev.auction == nil && g.Auction != nil {
			space := engine.Board[g.Auction.SpaceIndex]
			rec.Auctions = append(rec.Auctions, AuctionSummary{
				StartStep: st.N, EndStep: -1, Turn: prev.turn, SpaceIndex: space.Index, Space: space.Name, Price: space.Price, Bids: []AuctionBid{},
			})
			openAuction = len(rec.Auctions) - 1
		}
		if openAuction >= 0 && prev.auction != nil && ok {
			a := &rec.Auctions[openAuction]
			switch kind {
			case engine.ActionBid:
				amount := 0
				if g.Auction != nil {
					amount = g.Auction.CurrentBid
				} else {
					amount = actionAmount(st)
				}
				a.Bids = append(a.Bids, AuctionBid{Step: st.N, PlayerID: st.By, Amount: amount})
			case engine.ActionPassAuction:
				a.Bids = append(a.Bids, AuctionBid{Step: st.N, PlayerID: st.By, Pass: true})
			}
		}
		if openAuction >= 0 && prev.auction != nil && g.Auction == nil {
			a := &rec.Auctions[openAuction]
			a.EndStep = st.N
			if state := g.Properties[a.SpaceIndex]; state != nil && state.OwnerID != "" && prev.owner[a.SpaceIndex] == "" {
				a.WinnerID = state.OwnerID
				a.Paid = prev.cash[state.OwnerID] - cashOf(g, state.OwnerID)
				if a.Paid < 0 {
					a.Paid = prev.auction.CurrentBid
				}
			}
			openAuction = -1
		}

		for _, p := range g.Players {
			if p.Bankrupt && !prev.bankrupt[p.ID] {
				outOrder = append(outOrder, p.ID)
				outStep[p.ID] = st.N
			}
		}
		if rec.TurnOrder == nil && g.OpeningOrder != nil && len(g.OpeningOrder.Order) == len(g.Players) && g.Phase != engine.PhaseDeterminingOrder {
			rec.TurnOrder = append([]string{}, g.OpeningOrder.Order...)
		}
	}

	g := r.G
	if replayErr == nil {
		replayErr = checkFinal(rec, g)
	}
	if replayErr != nil {
		rec.VerifyError = replayErr.Error()
	} else {
		rec.Verified = true
	}
	// The replay stopped early, but the record still has the game's real end.
	if replayErr != nil {
		if final, err := LoadGame(rec.Final); err == nil {
			g = final
			for _, st := range rec.Steps[r.Next():] {
				if st.Kind == StepAction && st.ActionType() == engine.ActionBankrupt && !slices.Contains(outOrder, st.By) {
					outOrder = append(outOrder, st.By)
					outStep[st.By] = st.N
				}
			}
		}
	}
	rec.Turns = TurnNumber(g) - 1
	if len(rec.Final) == 0 && replayErr == nil {
		if data, err := json.Marshal(g); err == nil {
			rec.Final = data
		}
	}
	if rec.Outcome == nil {
		rec.Outcome = &Outcome{Reason: fallbackReason}
	}
	o := rec.Outcome
	if g.WinnerID != "" {
		o.WinnerID = g.WinnerID
	}
	if replayErr == nil && o.Reason == "" {
		o.Reason = fallbackReason
	}
	o.BankruptcyOrder = outOrder
	o.Standings = standings(g, o.WinnerID, outOrder, outStep)
}

// checkFinal compares the replay's end with the recorded final state and
// winner (the same checks as Verify).
func checkFinal(rec *Record, g *engine.Game) error {
	if len(rec.Final) > 0 {
		final, err := LoadGame(rec.Final)
		if err != nil {
			return &Mismatch{Step: -1, What: "final state: " + err.Error()}
		}
		if got, want := StateHash(g), StateHash(final); got != want {
			return &Mismatch{Step: -1, What: "the replay does not end in the recorded final state"}
		}
	}
	return nil
}

func bundle(b engine.TradeBundle) TradeBundle {
	props := append([]int{}, b.Properties...)
	return TradeBundle{Cash: b.Cash, Properties: props}
}

func actionAmount(st *Step) int {
	var a struct {
		Amount float64 `json:"amount"`
	}
	_ = json.Unmarshal(st.Action, &a)
	return int(a.Amount)
}

func cashOf(g *engine.Game, id string) int {
	if p := g.PlayerByID(id); p != nil {
		return p.Cash
	}
	return 0
}

// standings ranks the players at the end of g.
func standings(g *engine.Game, winnerID string, outOrder []string, outStep map[string]int) []Standing {
	var alive, out []Standing
	for _, p := range g.Players {
		s := Standing{PlayerID: p.ID, Bankrupt: p.Bankrupt, Cash: p.Cash, NetWorth: engine.NetWorth(g, p.ID), Score: engine.FinalScore(g, p.ID)}
		if p.Bankrupt {
			s.OutStep = outStep[p.ID]
			out = append(out, s)
		} else {
			alive = append(alive, s)
		}
	}
	sort.SliceStable(alive, func(i, j int) bool {
		if (alive[i].PlayerID == winnerID) != (alive[j].PlayerID == winnerID) {
			return alive[i].PlayerID == winnerID
		}
		return alive[i].Score > alive[j].Score
	})
	// Bankrupt players: last out ranks highest.
	sort.SliceStable(out, func(i, j int) bool {
		return slices.Index(outOrder, out[i].PlayerID) > slices.Index(outOrder, out[j].PlayerID)
	})
	all := append(alive, out...)
	for i := range all {
		all[i].Place = i + 1
	}
	return all
}
