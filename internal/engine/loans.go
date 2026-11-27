package engine

import (
	"errors"
	"math"
	"strings"
)

// Player-to-player loans (house rule "loans").
//
// A loan offer is a single pending proposal, like a trade. The proposer is
// always the current player:
//
//   - direction "lend": the proposer lends to targetId, in awaiting_roll or
//     turn_complete;
//   - direction "borrow": the proposer asks targetId for money, in
//     awaiting_roll or turn_complete, or in the debt phase when the proposer
//     is the debtor ("ask for a loan" instead of going bankrupt). In the debt
//     phase direction may be omitted and defaults to "borrow".
//
// offer_loan moves the game to phase loan_offer (remembering returnPhase).
// The target answers with accept_loan or reject_loan; the proposer cancels
// with reject_loan. Either way the game returns to returnPhase; a debt that
// was pending stays pending, and the debtor still presses pay_debt.
//
//	awaiting_roll | turn_complete | debt ──offer_loan──▶ loan_offer
//	loan_offer ──accept_loan (target) / reject_loan (target or proposer)──▶ returnPhase
//
// A loan's dueTurn counts the borrower's completed turns (turnCounts). At the
// start of each of the borrower's turns (beginTurn), every active loan with
// turnCounts[borrower] >= dueTurn is settled: repaid automatically if the
// borrower has the cash, otherwise its collateral passes to the lender
// (mortgages kept), otherwise the borrower owes the lender through the normal
// debt flow (debt.loanId set; sell/mortgage and pay_debt, or go bankrupt to
// the lender). The borrower may also repay early with repay_loan.
//
// AI hook: internal/ai ignores loans for now. AI players never propose, and
// an offer addressed to an AI is answered with reject_loan by the server's
// fallback, so the game never waits on a computer (the human proposer can
// always cancel with reject_loan as well). To add AI support, ChooseAction
// would (1) answer accept_loan / reject_loan when phase is loan_offer and
// loanOffer.targetId is the AI, and (2) in the debt phase try offer_loan
// {direction:"borrow"} before declaring bankruptcy.

// Loan action types.
const (
	ActionOfferLoan  = "offer_loan"
	ActionAcceptLoan = "accept_loan"
	ActionRejectLoan = "reject_loan"
	ActionRepayLoan  = "repay_loan"
)

// PhaseLoanOffer is the phase while a loan offer awaits an answer.
const PhaseLoanOffer = "loan_offer"

// Loan offer directions (from the proposer's point of view).
const (
	LoanLend   = "lend"
	LoanBorrow = "borrow"
)

// Loan statuses.
const (
	LoanActive    = "active"
	LoanRepaid    = "repaid"    // paid back (early or automatically when due)
	LoanDefaulted = "defaulted" // deadline missed (collateral taken or debt raised) or borrower bankrupt
	LoanCancelled = "cancelled" // lender went bankrupt; the borrower keeps the money
)

// Loan limits.
const (
	MaxLoanPrincipal   = 10_000
	MaxLoanRepayFactor = 3 // repay <= 3 × principal
	MaxLoanTurns       = 20
)

// Loan is an accepted loan. Loans stay in Game.Loans after they close so the
// table can see the history.
type Loan struct {
	ID         string `json:"id"`
	LenderID   string `json:"lenderId"`
	BorrowerID string `json:"borrowerId"`
	Principal  int    `json:"principal"`
	Repay      int    `json:"repay"`
	DueTurn    int    `json:"dueTurn"`    // due once turnCounts[borrowerId] >= dueTurn
	Collateral []int  `json:"collateral"` // board indexes pledged by the borrower
	Status     string `json:"status"`     // active | repaid | defaulted | cancelled
	CreatedAt  int64  `json:"createdAt"`
}

// LoanOffer is the pending loan proposal (Game.LoanOffer).
type LoanOffer struct {
	ID          string `json:"id"`
	Direction   string `json:"direction"` // "lend" or "borrow" (proposer's view)
	ProposerID  string `json:"proposerId"`
	TargetID    string `json:"targetId"` // the player who may accept
	LenderID    string `json:"lenderId"`
	BorrowerID  string `json:"borrowerId"`
	Principal   int    `json:"principal"`
	Repay       int    `json:"repay"`
	DueTurns    int    `json:"dueTurns"`
	Collateral  []int  `json:"collateral"`
	ReturnPhase string `json:"returnPhase"`
	CreatedAt   int64  `json:"createdAt"`
}

var errLoansDisabled = errors.New("Loans are not enabled in this game")

func assertLoansEnabled(g *Game) error {
	if !g.Settings.Rules.Loans {
		return errLoansDisabled
	}
	return nil
}

// ActiveLoans returns the loans that are still running.
func ActiveLoans(g *Game) []*Loan {
	var out []*Loan
	for _, loan := range g.Loans {
		if loan.Status == LoanActive {
			out = append(out, loan)
		}
	}
	return out
}

// FindLoan returns the loan with the given id, or nil.
func FindLoan(g *Game, id string) *Loan {
	for _, loan := range g.Loans {
		if loan.ID == id {
			return loan
		}
	}
	return nil
}

// LoanSecuredBy returns the active loan that holds spaceIndex as collateral,
// or nil. Such a property cannot be traded and its colour group cannot be
// built on until the loan closes.
func LoanSecuredBy(g *Game, spaceIndex int) *Loan {
	for _, loan := range g.Loans {
		if loan.Status != LoanActive {
			continue
		}
		for _, index := range loan.Collateral {
			if index == spaceIndex {
				return loan
			}
		}
	}
	return nil
}

// collateralError is "<name> is security for a loan".
type collateralError struct{ name string }

func (e collateralError) Error() string { return e.name + " is security for a loan" }

func securityError(index int) error {
	return collateralError{Board[index].Name}
}

// noteRefusedAITrade runs when offer_trade fails validation. internal/ai does
// not know about collateral yet, so an AI asking for a pledged property would
// propose the same refused trade again and again (aiTradeAttempted is only
// set by a successful offer). Counting the refused attempt for the turn stops
// that loop. Only collateral refusals are affected, so games without loans
// behave exactly as before.
func noteRefusedAITrade(g *Game, proposer *Player, err error) {
	var pledged collateralError
	if errors.As(err, &pledged) && proposer.Kind == KindAI && g.Turn != nil {
		g.Turn.AITradeAttempted = true
	}
}

// collateralBuildCheck blocks building in a colour group that contains a
// property pledged on an active loan (collateral must stay building-free so
// it can pass to the lender like a traded property).
func collateralBuildCheck(g *Game, space *Space) error {
	for _, item := range GroupSpaces(space.Group) {
		if LoanSecuredBy(g, item.Index) != nil {
			return securityError(item.Index)
		}
	}
	return nil
}

// collateralTradeCheck blocks trading a pledged property.
func collateralTradeCheck(g *Game, index int) error {
	if LoanSecuredBy(g, index) != nil {
		return securityError(index)
	}
	return nil
}

func activePlayerByID(g *Game, id string) *Player {
	for _, player := range g.Players {
		if player.ID == id && !player.Bankrupt {
			return player
		}
	}
	return nil
}

func hasActiveLoanBetween(g *Game, first, second string) bool {
	for _, loan := range ActiveLoans(g) {
		if (loan.LenderID == first && loan.BorrowerID == second) ||
			(loan.LenderID == second && loan.BorrowerID == first) {
			return true
		}
	}
	return false
}

// ---- action input -----------------------------------------------------------

// loanNumber mirrors Number(action[key]) for the loan keys.
func (a *Action) loanNumber(key string) float64 {
	if a.raw != nil {
		return a.number(key)
	}
	switch key {
	case "principal":
		return float64(a.Principal)
	case "repay":
		return float64(a.Repay)
	case "dueTurns":
		return float64(a.DueTurns)
	}
	return math.NaN()
}

// loanDirection returns action.direction; ok is false when it is present but
// not a string.
func (a *Action) loanDirection() (string, bool) {
	if a.raw == nil {
		return a.Direction, true
	}
	value, present := a.raw["direction"]
	if !present || value == nil {
		return "", true
	}
	s, isString := value.(string)
	return s, isString
}

// loanCollateral returns action.collateral as coerced numbers; a missing or
// null collateral is an empty list, anything but an array is invalid.
func (a *Action) loanCollateral() ([]float64, bool) {
	if a.raw == nil {
		values := make([]float64, len(a.Collateral))
		for i, index := range a.Collateral {
			values[i] = float64(index)
		}
		return values, true
	}
	value, present := a.raw["collateral"]
	if !present || value == nil {
		return nil, true
	}
	list, isList := value.([]any)
	if !isList {
		return nil, false
	}
	values := make([]float64, len(list))
	for i, item := range list {
		values[i] = jsNumber(item)
	}
	return values, true
}

// loanID returns action.loanId when it is a string.
func (a *Action) loanID() string {
	if a.raw == nil {
		return a.LoanID
	}
	s, _ := a.raw["loanId"].(string)
	return s
}

// ---- offers -------------------------------------------------------------------

func offerLoan(g *Game, proposerID string, action *Action) error {
	if err := assertLoansEnabled(g); err != nil {
		return err
	}
	proposer, err := assertCurrent(g, proposerID)
	if err != nil {
		return err
	}
	direction, directionOK := action.loanDirection()
	if directionOK && direction == "" && g.Phase == PhaseDebt {
		direction = LoanBorrow
	}
	if !directionOK || (direction != LoanLend && direction != LoanBorrow) {
		return errors.New("Choose whether to lend or borrow")
	}
	phases := []string{PhaseAwaitingRoll, PhaseTurnComplete}
	if direction == LoanBorrow {
		phases = append(phases, PhaseDebt)
	}
	if err := assertPhase(g, phases...); err != nil {
		return err
	}
	if g.Phase == PhaseDebt && (g.Debt == nil || g.Debt.PayerID != proposer.ID) {
		return errors.New("Only the player in debt can ask for a loan right now")
	}
	if g.LoanOffer != nil {
		return errors.New("Another loan offer is already pending")
	}
	targetID, targetOK := action.targetID()
	var target *Player
	for _, player := range g.Players {
		if targetOK && player.ID == targetID && !player.Bankrupt && player.ID != proposer.ID {
			target = player
			break
		}
	}
	if target == nil {
		return errors.New("Invalid loan partner")
	}
	// AI hook: an AI target is allowed; until internal/ai has a loan strategy
	// the server answers reject_loan for it (see the comment at the top of this file).

	principal, err := loanAmount(action.loanNumber("principal"))
	if err != nil {
		return err
	}
	repay := action.loanNumber("repay")
	if !isInteger(repay) || repay < float64(principal) || repay > float64(principal*MaxLoanRepayFactor) {
		return errors.New("Repayment must be a whole number from " + money(principal) + " to " +
			money(principal*MaxLoanRepayFactor))
	}
	dueTurns := action.loanNumber("dueTurns")
	if !isInteger(dueTurns) || dueTurns < 1 || dueTurns > MaxLoanTurns {
		return errors.New("The loan term must be 1 to " + itoa(MaxLoanTurns) + " turns")
	}
	collateral, err := normalizeCollateral(action.loanCollateral())
	if err != nil {
		return err
	}

	offer := &LoanOffer{
		ID:          g.uid("loanoffer"),
		Direction:   direction,
		ProposerID:  proposer.ID,
		TargetID:    target.ID,
		LenderID:    proposer.ID,
		BorrowerID:  target.ID,
		Principal:   principal,
		Repay:       int(repay),
		DueTurns:    int(dueTurns),
		Collateral:  collateral,
		ReturnPhase: g.Phase,
		CreatedAt:   g.now(),
	}
	if direction == LoanBorrow {
		offer.LenderID, offer.BorrowerID = target.ID, proposer.ID
	}
	if err := validateLoan(g, offer); err != nil {
		return err
	}
	g.LoanOffer = offer
	g.Phase = PhaseLoanOffer
	if direction == LoanLend {
		addLog(g, proposer.Name+" offered "+target.Name+" a "+money(principal)+" loan")
	} else {
		addLog(g, proposer.Name+" asked "+target.Name+" for a "+money(principal)+" loan")
	}
	return nil
}

func loanAmount(value float64) (int, error) {
	if !isInteger(value) || value < 1 || value > MaxLoanPrincipal {
		return 0, errors.New("The loan amount must be a whole number from $1 to " + money(MaxLoanPrincipal))
	}
	return int(value), nil
}

func normalizeCollateral(values []float64, ok bool) ([]int, error) {
	if !ok {
		return nil, errors.New("Invalid loan collateral")
	}
	collateral := []int{}
	seen := map[int]bool{}
	for _, value := range values {
		index, onBoard := boardIndex(value)
		if !onBoard || !IsOwnable(Board[index].Type) {
			return nil, errors.New("Invalid loan collateral")
		}
		if !seen[index] {
			seen[index] = true
			collateral = append(collateral, index)
		}
	}
	return collateral, nil
}

// validateLoan checks the parts of an offer that depend on the game state; it
// runs when the offer is made and again when it is accepted.
func validateLoan(g *Game, offer *LoanOffer) error {
	lender := activePlayerByID(g, offer.LenderID)
	borrower := activePlayerByID(g, offer.BorrowerID)
	if lender == nil || borrower == nil || lender.ID == borrower.ID {
		return errors.New("Invalid loan partner")
	}
	if hasActiveLoanBetween(g, lender.ID, borrower.ID) {
		return errors.New(lender.Name + " and " + borrower.Name + " already have an active loan")
	}
	for _, index := range offer.Collateral {
		space := &Board[index]
		if g.Properties[index].OwnerID != borrower.ID {
			return errors.New(space.Name + " does not belong to " + borrower.Name)
		}
		if space.Type == SpaceProperty && groupHasBuildings(g, space.Group) {
			return errors.New(Groups[space.Group].Name + " has buildings; sell them all first")
		}
		if LoanSecuredBy(g, index) != nil {
			return errors.New(space.Name + " is already security for a loan")
		}
	}
	if lender.Cash < offer.Principal {
		return errors.New(lender.Name + " does not have enough cash")
	}
	return nil
}

func acceptLoan(g *Game, playerID string) error {
	if err := assertPhase(g, PhaseLoanOffer); err != nil {
		return err
	}
	offer := g.LoanOffer
	if offer == nil || offer.TargetID != playerID {
		return errors.New("Only the recipient can accept this loan offer")
	}
	if err := validateLoan(g, offer); err != nil {
		return err
	}
	lender := g.PlayerByID(offer.LenderID)
	borrower := g.PlayerByID(offer.BorrowerID)
	lender.Cash -= offer.Principal
	borrower.Cash += offer.Principal
	loan := &Loan{
		ID:         g.uid("loan"),
		LenderID:   lender.ID,
		BorrowerID: borrower.ID,
		Principal:  offer.Principal,
		Repay:      offer.Repay,
		DueTurn:    g.TurnCounts[borrower.ID] + offer.DueTurns,
		Collateral: append([]int{}, offer.Collateral...),
		Status:     LoanActive,
		CreatedAt:  g.now(),
	}
	g.Loans = append(g.Loans, loan)
	g.LoanOffer = nil
	g.Phase = offer.ReturnPhase
	addLog(g, lender.Name+" lent "+borrower.Name+" "+money(loan.Principal)+"; "+
		borrower.Name+" repays "+money(loan.Repay)+" by turn "+itoa(loan.DueTurn))
	if len(loan.Collateral) > 0 {
		addLog(g, propertyNames(loan.Collateral)+" "+verb(len(loan.Collateral), "secures", "secure")+" the loan")
	}
	return nil
}

func rejectLoan(g *Game, playerID string) error {
	if err := assertPhase(g, PhaseLoanOffer); err != nil {
		return err
	}
	offer := g.LoanOffer
	if offer == nil || (playerID != offer.ProposerID && playerID != offer.TargetID) {
		return errors.New("The current player cannot respond to this loan offer")
	}
	actor := g.PlayerByID(playerID)
	g.LoanOffer = nil
	g.Phase = offer.ReturnPhase
	if playerID == offer.ProposerID {
		addLog(g, actor.Name+" cancelled the loan offer")
	} else {
		addLog(g, actor.Name+" declined the loan offer")
	}
	return nil
}

// ---- repayment and deadlines ------------------------------------------------

func repayLoan(g *Game, playerID string, loanID string) error {
	if err := assertLoansEnabled(g); err != nil {
		return err
	}
	player, err := assertCurrent(g, playerID)
	if err != nil {
		return err
	}
	if err := assertPhase(g, PhaseAwaitingRoll, PhaseAwaitingPurchase, PhaseTurnComplete); err != nil {
		return err
	}
	loan := FindLoan(g, loanID)
	if loan == nil {
		return errors.New("Loan not found")
	}
	if loan.BorrowerID != player.ID {
		return errors.New("Only the borrower can repay this loan")
	}
	if loan.Status != LoanActive {
		return errors.New("This loan is no longer active")
	}
	if player.Cash < loan.Repay {
		return errors.New("Not enough cash to repay this loan")
	}
	payLoan(g, loan, player, "")
	return nil
}

func payLoan(g *Game, loan *Loan, borrower *Player, suffix string) {
	borrower.Cash -= loan.Repay
	lenderName := "the lender"
	if lender := g.PlayerByID(loan.LenderID); lender != nil {
		lender.Cash += loan.Repay
		lenderName = lender.Name
	}
	loan.Status = LoanRepaid
	addLog(g, borrower.Name+" repaid "+lenderName+" "+money(loan.Repay)+suffix)
}

// loansAtTurnStart runs from beginTurn for the player whose turn starts.
func loansAtTurnStart(g *Game, player *Player) {
	g.LoanOffer = nil
	settleDueLoans(g, player)
}

// settleDueLoans settles the borrower's loans that have reached their
// deadline, oldest first. A loan that needs a debt stops the loop; the rest
// are settled once that debt is paid (see loanDebtPaid).
func settleDueLoans(g *Game, borrower *Player) {
	for _, loan := range g.Loans {
		if loan.Status != LoanActive || loan.BorrowerID != borrower.ID || g.TurnCounts[borrower.ID] < loan.DueTurn {
			continue
		}
		lender := g.PlayerByID(loan.LenderID)
		if lender == nil || lender.Bankrupt {
			loan.Status = LoanCancelled // unreachable: lender bankruptcy cancels loans
			continue
		}
		if borrower.Cash >= loan.Repay {
			payLoan(g, loan, borrower, " (loan due)")
			continue
		}
		var pledged []int
		for _, index := range loan.Collateral {
			if g.Properties[index].OwnerID == borrower.ID {
				pledged = append(pledged, index)
			}
		}
		loan.Status = LoanDefaulted
		if len(pledged) > 0 {
			for _, index := range pledged {
				g.Properties[index].OwnerID = lender.ID // mortgaged state is kept
			}
			addLog(g, borrower.Name+" missed the loan deadline; "+propertyNames(pledged)+" "+
				verb(len(pledged), "passes", "pass")+" to "+lender.Name)
			continue
		}
		addLog(g, borrower.Name+" missed the loan deadline")
		paid := charge(g, borrower, loan.Repay, lender.ID, "loan repayment to "+lender.Name,
			&DebtAfter{Kind: AfterPhase, Phase: PhaseAwaitingRoll})
		if !paid {
			g.Debt.LoanID = loan.ID
			return
		}
	}
}

// loanDebtPaid runs after pay_debt settled a debt; once a loan debt is paid,
// any further loans due this turn are settled.
func loanDebtPaid(g *Game, player *Player, debt *Debt) {
	if debt.LoanID == "" || g.Phase != PhaseAwaitingRoll {
		return
	}
	if current := CurrentPlayer(g); current == nil || current.ID != player.ID {
		return
	}
	settleDueLoans(g, player)
}

// closeLoansOnBankruptcy runs when player goes bankrupt: their loans as
// borrower are written off (the lender is a creditor only through the
// current debt, if it is theirs) and their loans as lender are cancelled
// (the borrower keeps the money). Collateral no longer applies.
func closeLoansOnBankruptcy(g *Game, player *Player) {
	g.LoanOffer = nil
	for _, loan := range g.Loans {
		if loan.Status != LoanActive {
			continue
		}
		switch player.ID {
		case loan.BorrowerID:
			loan.Status = LoanDefaulted
			if lender := g.PlayerByID(loan.LenderID); lender != nil {
				addLog(g, player.Name+"'s loan from "+lender.Name+" was written off")
			}
		case loan.LenderID:
			loan.Status = LoanCancelled
			if borrower := g.PlayerByID(loan.BorrowerID); borrower != nil {
				addLog(g, player.Name+"'s loan to "+borrower.Name+" was cancelled; "+borrower.Name+" keeps the money")
			}
		}
	}
}

// propertyNames joins space names: "A", "A and B", "A, B and C".
func propertyNames(indexes []int) string {
	names := make([]string, len(indexes))
	for i, index := range indexes {
		names[i] = Board[index].Name
	}
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func verb(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
