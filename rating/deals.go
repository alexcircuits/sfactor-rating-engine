package rating

import (
	"time"

	"github.com/sfactor/scoring/ubki"
)

// standing classifies a deal for the rating calculations.
type standing int

const (
	// standingOther covers unknown statuses and deals closed after a sale or
	// write-off.
	standingOther standing = iota

	// standingActive covers open or restructured loans and credit cards with an
	// available limit.
	standingActive

	// standingTerminal covers sold and written-off debts. These count as active, not
	// repaid.
	standingTerminal

	// standingRepaid is a closed deal with no recorded sale or write-off.
	standingRepaid
)

// deal holds the fields needed to measure and score a credit agreement.
type deal struct {
	lenderType string
	history    []ubki.Snapshot
	latest     ubki.Snapshot
	started    time.Time // earliest known start date
	standing   standing

	// terminal records the sale or write-off, even if the latest status has changed.
	terminal terminalEvent

	// terminalArrears is the last positive amount reported before sale or write-off.
	// Zero means no amount was reported.
	terminalArrears ubki.Money
}

// open reports whether the deal counts as an active obligation.
func (d deal) open() bool {
	return d.standing == standingActive || d.standing == standingTerminal
}

// borrowerDeals excludes guarantees and classifies the remaining deals.
func borrowerDeals(r *ubki.Report) []deal {
	var deals []deal
	for _, d := range r.Deals() {
		if d.Role != ubki.RoleGuarantor {
			deals = append(deals, classify(d))
		}
	}
	return deals
}

func classify(d ubki.Deal) deal {
	latest := d.Latest()
	c := deal{
		lenderType: d.LenderType,
		history:    d.History,
		latest:     latest,
		started:    earliestStart(d.History),
		terminal:   terminalEventOf(d.History),
	}

	switch latest.Status {
	case ubki.StatusOpen, ubki.StatusRestructured:
		c.standing = standingActive
	case ubki.StatusSold, ubki.StatusWrittenOff:
		// Sold and written-off debts still count as active obligations.
		c.standing = standingTerminal
		c.terminalArrears = lastReportedDebt(d.History, latest.ReportedOn)
	case ubki.StatusClosed:
		if !c.terminal.happened {
			c.standing = standingRepaid
		}
	}

	// A credit card with a positive limit counts as active even when marked closed.
	// Terminal status takes precedence.
	if c.standing != standingTerminal && d.Purpose == ubki.PurposeCreditCard && latest.Limit > 0 {
		c.standing = standingActive
	}
	return c
}

// earliestStart returns the earliest readable start date across the snapshots.
func earliestStart(history []ubki.Snapshot) time.Time {
	var first time.Time
	for _, s := range history {
		if t, ok := s.StartedOn.Time(); ok && (first.IsZero() || t.Before(first)) {
			first = t
		}
	}
	return first
}

// lastReportedDebt recovers the amount before УБКІ zeroed it on sale or write-off. It
// prefers the last positive overdue amount, then the last positive balance, at or before
// the terminal snapshot.
func lastReportedDebt(history []ubki.Snapshot, terminalOn ubki.Date) ubki.Money {
	cutoff, dated := terminalOn.Time()
	var overdue, balance ubki.Money
	for _, s := range history {
		// Keep undated snapshots; they may contain the last reported amount.
		if at, ok := s.ReportedOn.Time(); ok && dated && at.After(cutoff) {
			continue
		}
		if s.Overdue > 0 {
			overdue = s.Overdue
		}
		if s.Balance > 0 {
			balance = s.Balance
		}
	}
	if overdue > 0 {
		return overdue
	}
	return balance
}

// terminalEvent records whether and when a deal was sold or written off.
type terminalEvent struct {
	happened bool

	// at is the latest terminal snapshot date. If any terminal snapshot is undated,
	// at is zero and the event is treated as recent.
	at time.Time
}

func terminalEventOf(history []ubki.Snapshot) terminalEvent {
	var e terminalEvent
	undated := false
	for _, s := range history {
		if !s.Status.Terminal() {
			continue
		}
		e.happened = true
		at, ok := s.ReportedOn.Time()
		switch {
		case !ok:
			undated = true
		case at.After(e.at):
			e.at = at
		}
	}
	if undated {
		e.at = time.Time{}
	}
	return e
}

// within checks the inclusive calendar-month lookback. Undated events are treated as
// within the window.
func (e terminalEvent) within(asOf time.Time, months int) bool {
	if !e.happened {
		return false
	}
	return e.at.IsZero() || !e.at.Before(asOf.AddDate(0, -months, 0))
}

// overdueSnapshot requires both overdue days and an amount above the residue threshold.
// The overdue count, amount and clean streak all use this rule.
func overdueSnapshot(s ubki.Snapshot, cfg Config) bool {
	return s.DaysOverdue > 0 && s.Overdue > cfg.OverdueIgnoreAmount
}

// arrears returns whether the deal is overdue and its amount. Active deals use the latest
// snapshot. Terminal debts count as overdue within TerminalLookbackMonths, including
// those with no reported amount.
func (d deal) arrears(asOf time.Time, cfg Config) (bool, ubki.Money) {
	switch d.standing {
	case standingActive:
		if overdueSnapshot(d.latest, cfg) {
			return true, d.latest.Overdue
		}
	case standingTerminal:
		if d.terminal.within(asOf, cfg.TerminalLookbackMonths) {
			return true, d.terminalArrears
		}
	}
	return false, 0
}
