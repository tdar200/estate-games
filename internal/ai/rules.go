package ai

import "math"

// House rules (engine rules.go). With the default rules none of this changes
// a decision: every hook below is a no-op unless its rule is switched on, so
// default games stay decision-for-decision identical to src/ai.js.
//
//	rule                AI behaviour when switched on
//	loans               loans.go: answers loan offers, asks for a loan in debt,
//	                    repays on time, respects pledged collateral
//	auctions = false    shouldBuyWithoutAuction: buys at list price more readily
//	freeParkingJackpot  nothing special (the pot is just a lucky landing)
//	startingCash        nothing special (every reserve is absolute, not relative)

// With auctions off the AI keeps only 1/stretchReserveDivisor of its adaptive
// reserve when buying, because declining would not start an auction.
const stretchReserveDivisor = 2

// initHouseRules fills the house-rule fields of a decision context: whether
// loans are on and which spaces and colour groups hold collateral on an
// active loan. Without loans g.Loans is empty and all of it stays false.
func (c *ctx) initHouseRules() {
	c.loansOn = c.g.Settings.Rules.Loans
	for _, loan := range c.g.Loans {
		if loan == nil || loan.Status != LoanActive {
			continue
		}
		for _, index := range loan.Collateral {
			if index < 0 || index >= boardSize {
				continue
			}
			c.pledged[index] = true
			if group := board[index].group; group != noGroup {
				c.pledgedGroup[group] = true
			}
		}
	}
}

// shouldBuyWithoutAuction is the extra buy rule for games with auctions
// switched off. ai.js declines a property whose ceiling (value capped by
// cash minus the adaptive reserve) is below the price and then bids for it
// at auction, often winning it below list price. Without auctions a decline
// leaves the property with the bank, so there is no cheap second chance: the
// AI buys whenever the property is worth its price to it (the uncapped
// auction value) and paying it still leaves half of its usual reserve
// (loan repayments due soon are held back in full).
func (c *ctx) shouldBuyWithoutAuction(p *Player, s *space) bool {
	if c.g.Settings.Rules.AuctionsEnabled() || s == nil || p.Cash < s.price {
		return false
	}
	value, hardCap, reserve := c.auctionValue(p, s)
	if math.Min(math.Floor(value), hardCap) < float64(s.price) {
		return false
	}
	committed := c.loanCommitment(p.ID, "")
	stretchReserve := max(80, (reserve-committed)/stretchReserveDivisor) + committed
	return p.Cash-s.price >= stretchReserve
}
