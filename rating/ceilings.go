package rating

import "time"

// ceiling holds a score cap and its reason.
type ceiling struct {
	score  int
	reason CapReason
}

// ceilingFor returns the first applicable cap, in priority order:
//
//   - Current arrears of at least CapMinOverdueAmount use the hard cap.
//   - A sale or write-off within TerminalLookbackMonths also uses the hard cap,
//     including debts with no reported amount.
//   - Older unresolved terminal debts within TerminalSoftLookbackMonths use the soft cap.
func ceilingFor(deals []deal, book portfolio, asOf time.Time, cfg Config) (ceiling, bool) {
	if book.overdueDeals > 0 && book.overdueAmount >= cfg.CapMinOverdueAmount {
		return ceiling{cfg.CapScore, CapCurrentOverdue}, true
	}
	if !cfg.CapTerminalDebt {
		return ceiling{}, false
	}
	if anyTerminalWithin(deals, asOf, cfg.TerminalLookbackMonths) {
		return ceiling{cfg.CapScore, CapTerminalDebt}, true
	}
	if cfg.CapTerminalSoftScore > 0 && cfg.TerminalSoftLookbackMonths > 0 &&
		anyTerminalWithin(deals, asOf, cfg.TerminalSoftLookbackMonths) {
		return ceiling{cfg.CapTerminalSoftScore, CapTerminalDebtAged}, true
	}
	return ceiling{}, false
}

func anyTerminalWithin(deals []deal, asOf time.Time, months int) bool {
	for _, d := range deals {
		if d.terminal.within(asOf, months) {
			return true
		}
	}
	return false
}
