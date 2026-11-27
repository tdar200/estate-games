package ai

import (
	"math"
	"sort"
	"strings"
)

// Loan strategy for the house rule "loans" (engine loans.go). Nothing in this
// file changes a decision unless settings.rules.loans is on: every entry point
// returns early without it, and the collateral bookkeeping (ctx.pledged,
// ctx.pledgedGroup, loanCommitment) is empty because g.Loans is.
//
// Answering an offer (phase loan_offer, the AI is loanOffer.targetId):
//
//   - as lender (direction "borrow", someone asks the AI for money):
//     lenderAccepts;
//   - as borrower (direction "lend", someone offers the AI money):
//     borrowerAccepts.
//
// Asking (phase debt): chooseLoanRequest proposes offer_loan
// {direction:"borrow"} once per debt when the debt would otherwise cost
// buildings on a strong set or the game.
//
// Repaying: loans due within loanCommitTurns of the borrower's turns are added
// to its adaptive reserve (so it does not spend the repayment), a loan due at
// the next turn start is repaid at the end of the turn (chooseDueRepayment),
// and a loan whose collateral blocks building on a complete set is repaid
// early when the AI can then afford a tier (chooseUnblockingRepayment).
//
// Collateral: pledged properties are never put in trades or mortgaged
// (mortgageCandidates leaves them out), and colour groups holding one are
// never built on: the engine refuses all three. A lender only counts
// unmortgaged collateral as security.
//
// Rates are compared in basis points with integer maths: a loan of principal P
// repaying R over T turns has rate (R-P)/P.

// Engine loan limits (engine loans.go).
const (
	maxLoanPrincipal   = 10_000
	maxLoanRepayFactor = 3
	maxLoanTurns       = 20
)

// Strategy constants.
const (
	// loanCommitTurns: repayments due within this many of the borrower's
	// turns are held back from spending (added to adaptiveReserve).
	loanCommitTurns = 2

	// Lender: the minimum rate is lenderBaseRateBP for a term up to
	// lenderBaseTerm turns plus lenderRateStepBP per extra turn; collateral
	// worth half the repayment cuts it by a quarter, worth all of it by half.
	lenderBaseRateBP = 1000
	lenderBaseTerm   = 5
	lenderRateStepBP = 200
	// lenderWorthCover: the borrower's net worth (cash + portfolio value,
	// net of any debt and other loans) must be at least this × repay.
	lenderWorthCover = 2
	// lenderHitDivisor: after repaying, the borrower's liquidity must still
	// cover 1/lenderHitDivisor of the highest rent it could land on.
	lenderHitDivisor = 3

	// Borrower: the AI pays at most borrowerRateStepBP per turn of term
	// (terms shorter than borrowerMinRateTerm count as that), capped at
	// borrowerRateCapBP in total.
	borrowerRateStepBP  = 600
	borrowerMinRateTerm = 3
	borrowerRateCapBP   = 5000
	// A loan for building must buy a build-tier gain worth at least
	// borrowerBuildGainCover × the interest, over at least
	// borrowerMinBuildTerm turns (so the repayment is not held back at once).
	borrowerBuildGainCover = 1.0
	borrowerMinBuildTerm   = 3

	// Requests: repay = principal + 1/requestRateDivisor (20%), and at most
	// requestMaxCollateral pledged properties.
	requestRateDivisor   = 5
	requestMaxCollateral = 3
)

// lenderRequiredRateBP is the lowest rate (basis points) the AI lends at.
func lenderRequiredRateBP(dueTurns, security, repay int) int {
	bp := lenderBaseRateBP + lenderRateStepBP*max(0, dueTurns-lenderBaseTerm)
	switch {
	case security >= repay:
		bp /= 2
	case 2*security >= repay:
		bp = bp * 3 / 4
	}
	return bp
}

// borrowerMaxRateBP is the highest rate (basis points) the AI borrows at.
func borrowerMaxRateBP(dueTurns int) int {
	return min(borrowerRateCapBP, borrowerRateStepBP*max(borrowerMinRateTerm, dueTurns))
}

// requestTerm is the term the AI asks for: longer for bigger loans.
func requestTerm(principal int) int {
	switch {
	case principal <= 200:
		return 4
	case principal <= 600:
		return 5
	}
	return 6
}

func isStrongGroup(g int) bool {
	for _, strong := range strongDevelopmentGroups {
		if strong == g {
			return true
		}
	}
	return false
}

// loanTerms is a loan from the parties' point of view (an offer or a request).
type loanTerms struct {
	lenderID, borrowerID string
	principal, repay     int
	dueTurns             int
	collateral           []int
}

// AcceptsLoan reports whether playerID would accept the pending g.LoanOffer:
// accept_loan or reject_loan in ChooseAction. It is false when there is no
// offer, loans are off, or playerID is neither its lender nor its borrower.
// Like AcceptsTrade it does not check that playerID is the offer's target.
func AcceptsLoan(g *Game, playerID string) bool {
	if g == nil || g.LoanOffer == nil {
		return false
	}
	return newCtx(g).acceptsLoan(g.LoanOffer, playerID)
}

// chooseLoanResponse answers a loan offer addressed to p (nil otherwise, e.g.
// while p is the proposer waiting for an answer).
func (c *ctx) chooseLoanResponse(p *Player) *Action {
	o := c.g.LoanOffer
	if o == nil || o.TargetID != p.ID {
		return nil
	}
	if c.acceptsLoan(o, p.ID) {
		return &Action{Type: ActionAcceptLoan}
	}
	return &Action{Type: ActionRejectLoan}
}

func (c *ctx) acceptsLoan(o *LoanOffer, playerID string) bool {
	if !c.loansOn {
		return false
	}
	p := c.activePlayer(playerID)
	if p == nil {
		return false
	}
	t := loanTerms{
		lenderID:   o.LenderID,
		borrowerID: o.BorrowerID,
		principal:  o.Principal,
		repay:      o.Repay,
		dueTurns:   o.DueTurns,
		collateral: o.Collateral,
	}
	switch playerID {
	case o.LenderID:
		return c.lenderAccepts(p, &t)
	case o.BorrowerID:
		return c.borrowerAccepts(p, &t)
	}
	return false
}

// ------------------------------------------------------------ lending ----

// lenderAccepts decides whether lender lends on terms t. All of:
//
//  1. the loan is valid for the engine (validLoan);
//  2. lending leaves the lender its adaptive reserve (counting a debt the
//     borrower owes the lender as paid straight back);
//  3. the rate is at least lenderRequiredRateBP (10% up to 5 turns, +2% per
//     extra turn; ×3/4 with collateral worth half the repayment, ×1/2 with
//     collateral worth all of it; collateral counts at its mortgage value,
//     mortgaged collateral at nothing);
//  4. the borrower's net worth (cash + portfolioValue, after the loan and any
//     pending debt, minus other loans owed) is at least 2 × repay; its
//     liquidity (that cash plus liquidationValue) covers the repayment unless
//     the collateral does; and what is left after repaying still covers a
//     third of the highest rent it could land on (maxHostileRent), because a
//     bankrupt borrower's loan is written off and its collateral released;
//  5. the cash is not worth more as the lender's own building money: the drop
//     in its developmentValue is at most the interest;
//  6. the loan does not strengthen the borrower dangerously
//     (loanStrengthensOpponent).
func (c *ctx) lenderAccepts(lender *Player, t *loanTerms) bool {
	if lender.ID != t.lenderID || !c.validLoan(t) {
		return false
	}
	borrower := c.activePlayer(t.borrowerID)
	interest := t.repay - t.principal
	debt := c.pendingDebt(borrower)

	outlay := t.principal
	if d := c.g.Debt; debt > 0 && d.CreditorID == lender.ID && borrower.Cash+t.principal >= debt {
		outlay -= debt // the borrower pays the lender straight back
	}
	if lender.Cash-outlay < c.adaptiveReserve(lender, noGroup) {
		return false
	}

	security := c.collateralValue(t.collateral)
	if interest*10_000 < lenderRequiredRateBP(t.dueTurns, security, t.repay)*t.principal {
		return false
	}

	owed := c.loanObligations(borrower.ID)
	cashAfter := borrower.Cash + t.principal - debt
	worth := float64(cashAfter-owed) + c.portfolioValue(c.ownedSet(borrower.ID))
	if worth < float64(lenderWorthCover*t.repay) {
		return false
	}
	liquidity := cashAfter + c.liquidationValue(borrower.ID) - owed
	if liquidity < t.repay && security < t.repay {
		return false
	}
	// A borrower one bad landing away from bankruptcy is likely to be written
	// off, and bankruptcy releases the collateral too.
	if liquidity-t.repay < c.maxHostileRent(borrower.ID)/lenderHitDivisor {
		return false
	}

	if outlay > 0 {
		own := c.ownedSet(lender.ID)
		lost := c.developmentValue(lender.ID, own, lender.Cash) - c.developmentValue(lender.ID, own, lender.Cash-outlay)
		if lost > float64(interest) {
			return false
		}
	}
	return !c.loanStrengthensOpponent(borrower, t.principal, interest)
}

// loanStrengthensOpponent reports whether lending principal to borrower helps
// them more than the interest is worth:
//
//   - the current leader, and the only opponent in a two-player game, are
//     never funded when the cash lets them build a further tier on a complete
//     colour group (reachableGroupTier) or saves buildings they would
//     otherwise have to sell to pay their debt;
//   - for anyone, the gain in their build option (developmentValue at the
//     cash after the loan versus without it), weighted like an auction denial
//     threat (denialWeights), must not exceed the interest.
func (c *ctx) loanStrengthensOpponent(borrower *Player, principal, interest int) bool {
	debt := c.pendingDebt(borrower)
	cashWithout := borrower.Cash - debt
	cashWith := cashWithout + principal
	if borrower.ID == c.currentLeaderID() || c.active == 2 {
		savesBuildings := debt > borrower.Cash && borrower.Cash+c.mortgageableValue(borrower.ID) < debt
		for g := 0; g < groupCount; g++ {
			if !c.groupOwnedBy(borrower.ID, g) || c.groupHasMortgage(g) {
				continue
			}
			if savesBuildings && c.groupHasBuildings(g) {
				return true
			}
			if !c.pledgedGroup[g] && c.reachableGroupTier(borrower, g, cashWith) > c.reachableGroupTier(borrower, g, cashWithout) {
				return true
			}
		}
	}
	own := c.ownedSet(borrower.ID)
	gain := c.developmentValue(borrower.ID, own, cashWith) - c.developmentValue(borrower.ID, own, cashWithout)
	return mul(gain, weightOr(&denialWeights, c.active)) > float64(interest)
}

// ---------------------------------------------------------- borrowing ----

// borrowerAccepts decides whether borrower takes a loan on terms t. The loan
// must be valid, cost at most borrowerMaxRateBP (6% per turn of term, at least
// 18%, at most 50%) and have a high-value use:
//
//   - in debt (cash below the debt): the loan covers the debt, the debt would
//     otherwise cost buildings on a strong set or the game (debtNeedsLoan), and
//     the AI can repay from what is left plus its liquidation value;
//   - otherwise: the term is at least 3 turns, the AI could repay even after
//     losing half the loan on buildings, and the cash completes a further
//     build tier on a complete set worth at least the interest
//     (loanBuildGain; a group holding collateral does not count).
func (c *ctx) borrowerAccepts(borrower *Player, t *loanTerms) bool {
	if borrower.ID != t.borrowerID || !c.validLoan(t) {
		return false
	}
	interest := t.repay - t.principal
	if interest*10_000 > borrowerMaxRateBP(t.dueTurns)*t.principal {
		return false
	}
	liquid := borrower.Cash + c.liquidationValue(borrower.ID) - c.loanObligations(borrower.ID)
	if debt := c.pendingDebt(borrower); debt > borrower.Cash {
		return borrower.Cash+t.principal >= debt && c.debtNeedsLoan(borrower) &&
			liquid+t.principal-debt >= t.repay
	}
	if t.dueTurns < borrowerMinBuildTerm || liquid+t.principal/2 < t.repay {
		return false
	}
	gain := c.loanBuildGain(borrower, t)
	return gain > 0 && gain >= mul(borrowerBuildGainCover, float64(interest))
}

// loanBuildGain is the best gain in build-stage EV (same scale as
// developmentValue: EV × opponents × strategicEVCashFactor × buildability)
// that the extra principal buys on one of p's complete, unmortgaged groups
// that neither holds collateral nor would. Only strong groups or a reach of
// three houses count, and the bank must have the houses.
func (c *ctx) loanBuildGain(p *Player, t *loanTerms) float64 {
	var blocked [groupCount]bool
	for _, index := range t.collateral {
		if s := spaceAt(index); s != nil && s.group != noGroup {
			blocked[s.group] = true
		}
	}
	supply := c.bankSupply()
	opponents := float64(max(1, c.active-1))
	best := 0.0
	for g := 0; g < groupCount; g++ {
		if !c.groupOwnedBy(p.ID, g) || c.groupHasMortgage(g) || c.pledgedGroup[g] || blocked[g] {
			continue
		}
		without := c.reachableGroupTier(p, g, p.Cash)
		with := c.reachableGroupTier(p, g, p.Cash+t.principal)
		if with == 5 && supply.Hotels < len(groupSpaces[g]) {
			with = 4
		}
		for with > without && c.housesNeeded(g, min(with, 4)) > supply.Houses {
			with--
		}
		if with <= without || (!isStrongGroup(g) && with < 3) {
			continue
		}
		gain := mul(mul(mul(groupStageEV[g][with]-groupStageEV[g][without], opponents), strategicEVCashFactor), buildabilityWeights[with])
		best = math.Max(best, gain)
	}
	return best
}

// housesNeeded is the number of houses that bring group g to tier (<= 4).
func (c *ctx) housesNeeded(g, tier int) int {
	n := 0
	for _, s := range groupSpaces[g] {
		n += max(0, tier-c.props[s.index].Houses)
	}
	return n
}

// ------------------------------------------------------------- asking ----

// chooseLoanRequest asks another player for a loan in the debt phase, at most
// once per debt, when the debt would otherwise cost buildings on a strong set
// or the game (debtNeedsLoan). Terms: principal = the shortfall rounded up to
// $10, repay = principal + 20%, 4 to 6 turns (requestTerm), no collateral if
// the lender accepts without, otherwise the least strategic pledgeable
// property, otherwise enough of them to cover the repayment.
//
// Lenders: every other solvent player that can pay the principal, has no
// active loan with the AI and is not the creditor of the debt. An AI lender is
// asked only if it would accept (lenderAccepts is exact for AIs); a human is
// ranked by the same test as a proxy. Order: would accept, not the leader,
// most cash, seat order. With no lender the normal debt logic runs.
func (c *ctx) chooseLoanRequest(p *Player) *Action {
	d := c.g.Debt
	if !c.loansOn || d == nil || d.PayerID != p.ID || p.Cash >= d.Amount || c.g.LoanOffer != nil {
		return nil
	}
	if c.askedForLoanThisDebt(p) || !c.debtNeedsLoan(p) {
		return nil
	}
	principal := (d.Amount - p.Cash + 9) / 10 * 10
	if principal > maxLoanPrincipal {
		return nil
	}
	repay := principal + (principal+requestRateDivisor-1)/requestRateDivisor
	dueTurns := requestTerm(principal)
	variants := c.collateralVariants(p.ID, repay)
	leaderID := c.currentLeaderID()

	type candidate struct {
		lender     *Player
		collateral []int
		accepts    bool
		leader     bool
	}
	var candidates []candidate
	for _, lender := range c.g.Players {
		if lender.Bankrupt || lender.ID == p.ID || lender.ID == d.CreditorID ||
			lender.Cash < principal || c.hasActiveLoanBetween(lender.ID, p.ID) {
			continue
		}
		var collateral []int
		accepts := false
		for _, variant := range variants {
			collateral = variant
			t := loanTerms{lenderID: lender.ID, borrowerID: p.ID, principal: principal, repay: repay, dueTurns: dueTurns, collateral: variant}
			if c.lenderAccepts(lender, &t) {
				accepts = true
				break
			}
		}
		if !accepts && lender.Kind == "ai" {
			continue // it would decline: do not waste the one request
		}
		candidates = append(candidates, candidate{lender, collateral, accepts, lender.ID == leaderID})
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.accepts != b.accepts {
			return a.accepts
		}
		if a.leader != b.leader {
			return !a.leader
		}
		return a.lender.Cash > b.lender.Cash
	})
	best := candidates[0]
	return &Action{
		Type:     ActionOfferLoan,
		TargetID: best.lender.ID,
		Loan: &LoanTerms{
			Direction:  LoanBorrow,
			Principal:  principal,
			Repay:      repay,
			DueTurns:   dueTurns,
			Collateral: append([]int{}, best.collateral...),
		},
	}
}

// debtNeedsLoan reports whether p's pending debt can only be paid by selling
// buildings on a strong developed set (a colour group with three or more
// houses on a property, or one of the strong groups) or not at all: its cash,
// every possible mortgage and the buildings on weaker sets fall short.
func (c *ctx) debtNeedsLoan(p *Player) bool {
	debt := c.pendingDebt(p)
	if debt <= p.Cash {
		return false
	}
	available := p.Cash + c.mortgageableValue(p.ID)
	for g := 0; g < groupCount; g++ {
		if !c.groupHasBuildings(g) || !c.groupOwnedBy(p.ID, g) {
			continue
		}
		if c.groupMaxHouses(g) >= 3 || isStrongGroup(g) {
			continue
		}
		for _, s := range groupSpaces[g] {
			available += s.buildCost * c.props[s.index].Houses / 2
		}
	}
	return available < debt
}

// askedForLoanThisDebt reports whether p already asked for a loan since its
// current debt began. The engine keeps no record of declined offers, so this
// reads the log backwards: "<name> asked <target> for a $N loan" is a request
// and "<name> must raise $N to pay …" opens the debt. Without the debt entry
// (no log in the view) it answers true, so the AI never asks twice.
func (c *ctx) askedForLoanThisDebt(p *Player) bool {
	asked := p.Name + " asked "
	raised := p.Name + " must raise "
	for i := len(c.g.Log) - 1; i >= 0; i-- {
		text := c.g.Log[i].Text
		if strings.HasPrefix(text, asked) && strings.HasSuffix(text, " loan") {
			return true
		}
		if strings.HasPrefix(text, raised) {
			return false
		}
	}
	return true
}

// collateralVariants lists the collateral sets a request tries, in order: none,
// the least strategic pledgeable property, then the least strategic ones until
// their mortgage value covers the repayment (at most requestMaxCollateral).
func (c *ctx) collateralVariants(playerID string, repay int) [][]int {
	eligible := c.collateralCandidates(playerID)
	variants := [][]int{{}}
	if len(eligible) == 0 {
		return variants
	}
	variants = append(variants, []int{eligible[0]})
	var secured []int
	value := 0
	for _, index := range eligible {
		if len(secured) == requestMaxCollateral || value >= repay {
			break
		}
		secured = append(secured, index)
		value += board[index].price / 2
	}
	if len(secured) > 1 {
		variants = append(variants, secured)
	}
	return variants
}

// collateralCandidates lists what playerID may pledge, least strategic first:
// unmortgaged, unpledged holdings in building-free groups (the engine's rule),
// except complete colour groups (a pledge would stop building there); sorted
// by assetStrategicTier, then in mortgageCandidates order.
func (c *ctx) collateralCandidates(playerID string) []int {
	type candidate struct{ index, tier int }
	var list []candidate
	for _, s := range c.mortgageCandidates(playerID, noGroup) {
		if c.pledged[s.index] || (s.group != noGroup && c.groupOwnedBy(playerID, s.group)) {
			continue
		}
		list = append(list, candidate{s.index, c.assetStrategicTier(playerID, s)})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].tier < list[j].tier })
	out := make([]int, len(list))
	for i, item := range list {
		out[i] = item.index
	}
	return out
}

// ---------------------------------------------------------- repaying ----

// chooseDueRepayment repays, at the end of the AI's turn, a loan that falls
// due when its next turn starts, if it has the cash. The engine would settle
// it then anyway; paying now means a trade or bid between turns can never turn
// an affordable repayment into a default (and lost collateral).
func (c *ctx) chooseDueRepayment(p *Player) *Action {
	if !c.loansOn {
		return nil
	}
	turns := c.g.TurnCounts[p.ID]
	for _, loan := range c.g.Loans {
		if loan == nil || loan.Status != LoanActive || loan.BorrowerID != p.ID {
			continue
		}
		if turns+1 >= loan.DueTurn && p.Cash >= loan.Repay {
			return &Action{Type: ActionRepayLoan, LoanID: loan.ID}
		}
	}
	return nil
}

// chooseUnblockingRepayment repays a loan early when its collateral is all
// that stops the AI building on one of its complete colour groups and, after
// repaying, it can still afford a full tier there above its reserve.
func (c *ctx) chooseUnblockingRepayment(p *Player) *Action {
	if !c.loansOn {
		return nil
	}
	supply := c.bankSupply()
	for _, loan := range c.g.Loans {
		if loan == nil || loan.Status != LoanActive || loan.BorrowerID != p.ID || p.Cash < loan.Repay {
			continue
		}
		reserve := c.adaptiveReserve(p, noGroup) - c.loanCommitment(p.ID, "") + c.loanCommitment(p.ID, loan.ID)
		for _, index := range loan.Collateral {
			s := spaceAt(index)
			if s == nil || s.group == noGroup {
				continue
			}
			g := s.group
			if !c.groupOwnedBy(p.ID, g) || c.groupHasMortgage(g) || c.groupMinHouses(g) >= 5 || c.groupPledgedExcept(g, loan.ID) {
				continue
			}
			spaces := groupSpaces[g]
			tierCost := len(spaces) * spaces[0].buildCost
			if supply.Houses >= len(spaces) && p.Cash-loan.Repay-tierCost >= reserve {
				return &Action{Type: ActionRepayLoan, LoanID: loan.ID}
			}
		}
	}
	return nil
}

// ----------------------------------------------------------- helpers ----

// loanCommitment is the cash borrowerID holds back for its active loans due
// within loanCommitTurns turns (except exceptLoanID). It is part of
// adaptiveReserve, and 0 in games without loans.
func (c *ctx) loanCommitment(borrowerID, exceptLoanID string) int {
	if len(c.g.Loans) == 0 {
		return 0
	}
	turns := c.g.TurnCounts[borrowerID]
	total := 0
	for _, loan := range c.g.Loans {
		if loan == nil || loan.Status != LoanActive || loan.BorrowerID != borrowerID || loan.ID == exceptLoanID {
			continue
		}
		if turns+loanCommitTurns >= loan.DueTurn {
			total += loan.Repay
		}
	}
	return total
}

// loanObligations is the total repayment borrowerID owes on active loans.
func (c *ctx) loanObligations(borrowerID string) int {
	total := 0
	for _, loan := range c.g.Loans {
		if loan != nil && loan.Status == LoanActive && loan.BorrowerID == borrowerID {
			total += loan.Repay
		}
	}
	return total
}

func (c *ctx) hasActiveLoanBetween(first, second string) bool {
	for _, loan := range c.g.Loans {
		if loan == nil || loan.Status != LoanActive {
			continue
		}
		if (loan.LenderID == first && loan.BorrowerID == second) || (loan.LenderID == second && loan.BorrowerID == first) {
			return true
		}
	}
	return false
}

// groupPledgedExcept reports whether an active loan other than exceptLoanID
// holds a property of group g as collateral.
func (c *ctx) groupPledgedExcept(g int, exceptLoanID string) bool {
	for _, loan := range c.g.Loans {
		if loan == nil || loan.Status != LoanActive || loan.ID == exceptLoanID {
			continue
		}
		for _, index := range loan.Collateral {
			if s := spaceAt(index); s != nil && s.group == g {
				return true
			}
		}
	}
	return false
}

// pendingDebt is the amount p owes in the current debt phase (0 if none).
func (c *ctx) pendingDebt(p *Player) int {
	if d := c.g.Debt; d != nil && d.PayerID == p.ID {
		return d.Amount
	}
	return 0
}

// mortgageableValue is the cash playerID could still raise by mortgaging
// (mortgageCandidates' filter, half the price each).
func (c *ctx) mortgageableValue(playerID string) int {
	value := 0
	for i := 0; i < boardSize; i++ {
		if !c.hasProp[i] || c.props[i].OwnerID != playerID || c.props[i].Mortgaged || c.pledged[i] {
			continue
		}
		if s := &board[i]; s.kind != typeProperty || !c.groupHasBuildings(s.group) {
			value += s.price / 2
		}
	}
	return value
}

// collateralValue is the security a lender counts: the mortgage value of the
// unmortgaged pledged properties (a mortgaged one raises no more cash).
func (c *ctx) collateralValue(indexes []int) int {
	var seen [boardSize]bool
	value := 0
	for _, index := range indexes {
		if index < 0 || index >= boardSize || seen[index] || !c.hasProp[index] {
			continue
		}
		seen[index] = true
		if !c.props[index].Mortgaged {
			value += board[index].price / 2
		}
	}
	return value
}

func (c *ctx) activePlayer(id string) *Player {
	for _, p := range c.g.Players {
		if p.ID == id && !p.Bankrupt {
			return p
		}
	}
	return nil
}

// validLoan mirrors the engine's checks on an offer (offerLoan's limits and
// validateLoan), so the AI never proposes or accepts a loan the engine
// refuses.
func (c *ctx) validLoan(t *loanTerms) bool {
	lender, borrower := c.activePlayer(t.lenderID), c.activePlayer(t.borrowerID)
	if lender == nil || borrower == nil || lender.ID == borrower.ID {
		return false
	}
	if t.principal < 1 || t.principal > maxLoanPrincipal ||
		t.repay < t.principal || t.repay > t.principal*maxLoanRepayFactor ||
		t.dueTurns < 1 || t.dueTurns > maxLoanTurns {
		return false
	}
	if c.hasActiveLoanBetween(lender.ID, borrower.ID) {
		return false
	}
	for _, index := range t.collateral {
		if index < 0 || index >= boardSize || !c.hasProp[index] ||
			c.props[index].OwnerID != borrower.ID || c.pledged[index] {
			return false
		}
		if s := &board[index]; s.kind == typeProperty && c.groupHasBuildings(s.group) {
			return false
		}
	}
	return lender.Cash >= t.principal
}
