package rating

import (
	"testing"
	"time"

	"github.com/sfactor/scoring/ubki"
)

const purposeConsumer = "7"

// balanceSnap creates a snapshot with the balance and limit fields used by the debt and
// card rules.
func balanceSnap(when time.Time, status ubki.DealStatus, dpd int, overdue, balance, limit ubki.Money) ubki.Snapshot {
	return ubki.Snapshot{
		ReportedOn:  ubki.NewDate(when),
		StartedOn:   ubki.NewDate(daysAgo(900)),
		Status:      status,
		DaysOverdue: ubki.Int(dpd),
		Overdue:     overdue,
		Balance:     balance,
		Limit:       limit,
	}
}

func purposeDeal(purpose string, history ...ubki.Snapshot) ubki.Deal {
	return ubki.Deal{Role: ubki.RoleBorrower, LenderType: ubki.CreditorBank, Purpose: purpose, History: history}
}

func TestStatusDecidesStanding(t *testing.T) {
	tests := []struct {
		status ubki.DealStatus
		want   standing
	}{
		{ubki.StatusOpen, standingActive},
		{ubki.StatusRestructured, standingActive}, // restructured loans remain active
		{ubki.StatusClosed, standingRepaid},
		{ubki.StatusSold, standingTerminal},
		{ubki.StatusWrittenOff, standingTerminal},
		{"99", standingOther},
	}
	for _, tt := range tests {
		d := classify(purposeDeal(purposeConsumer, balanceSnap(daysAgo(100), tt.status, 0, 0, 0, 0)))
		if d.standing != tt.want {
			t.Errorf("status %s: standing = %d, want %d", tt.status, d.standing, tt.want)
		}
	}
}

// Closing a previously sold or written-off deal must not count it as repaid.
func TestClosedAfterASaleIsNotRepaid(t *testing.T) {
	d := classify(purposeDeal(purposeConsumer,
		balanceSnap(daysAgo(300), ubki.StatusSold, 0, 0, 0, 0),
		balanceSnap(daysAgo(100), ubki.StatusClosed, 0, 0, 0, 0),
	))
	if d.standing != standingOther {
		t.Errorf("standing = %d, want neither open nor repaid", d.standing)
	}
}

// Recover the last reported amount when the sale snapshot has a zero balance.
func TestTerminalDebtIsRecoveredFromTheHistory(t *testing.T) {
	tests := []struct {
		name    string
		history []ubki.Snapshot
		want    ubki.Money
	}{
		{
			name: "the last reported arrears",
			history: []ubki.Snapshot{
				balanceSnap(daysAgo(300), ubki.StatusOpen, 40, uah(465.10), uah(2299.66), 0),
				balanceSnap(daysAgo(200), ubki.StatusOpen, 134, uah(1034.62), uah(2069.18), 0),
				balanceSnap(daysAgo(100), ubki.StatusSold, 0, 0, 0, 0),
			},
			want: uah(1034.62),
		},
		{
			name: "the outstanding balance when no arrears were ever reported",
			history: []ubki.Snapshot{
				balanceSnap(daysAgo(300), ubki.StatusOpen, 0, 0, uah(1834.56), 0),
				balanceSnap(daysAgo(200), ubki.StatusOpen, 0, 0, uah(2764.76), 0),
				balanceSnap(daysAgo(100), ubki.StatusWrittenOff, 0, 0, 0, 0),
			},
			want: uah(2764.76),
		},
		{
			name: "zero when neither was ever reported",
			history: []ubki.Snapshot{
				balanceSnap(daysAgo(300), ubki.StatusOpen, 0, 0, 0, uah(5000)),
				balanceSnap(daysAgo(100), ubki.StatusWrittenOff, 0, 0, 0, uah(5000)),
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(purposeDeal(purposeConsumer, tt.history...)).terminalArrears; got != tt.want {
				t.Errorf("terminalArrears = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCreditCardWithALiveLimitIsActive(t *testing.T) {
	tests := []struct {
		name string
		deal ubki.Deal
		want standing
	}{
		{
			name: "a closed card still holding a limit",
			deal: purposeDeal(ubki.PurposeCreditCard,
				balanceSnap(daysAgo(400), ubki.StatusOpen, 0, 0, 0, uah(15000)),
				balanceSnap(daysAgo(30), ubki.StatusClosed, 0, 0, 0, uah(15000)),
			),
			want: standingActive,
		},
		{
			name: "a limit that lapsed before the card closed does not revive it",
			deal: purposeDeal(ubki.PurposeCreditCard,
				balanceSnap(daysAgo(400), ubki.StatusOpen, 0, 0, 0, uah(300)),
				balanceSnap(daysAgo(370), ubki.StatusOpen, 0, 0, 0, 0),
				balanceSnap(daysAgo(340), ubki.StatusClosed, 0, 0, 0, 0),
			),
			want: standingRepaid,
		},
		{
			name: "the rule is about cards, not every closed deal",
			deal: purposeDeal(purposeConsumer,
				balanceSnap(daysAgo(30), ubki.StatusClosed, 0, 0, 0, uah(15000)),
			),
			want: standingRepaid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(tt.deal).standing; got != tt.want {
				t.Errorf("standing = %d, want %d", got, tt.want)
			}
		})
	}
}

// A positive card limit must not override terminal status.
func TestSoldCardIsTerminalFirst(t *testing.T) {
	d := classify(purposeDeal(ubki.PurposeCreditCard,
		balanceSnap(daysAgo(200), ubki.StatusOpen, 90, uah(3200), uah(8000), uah(15000)),
		balanceSnap(daysAgo(100), ubki.StatusSold, 0, 0, 0, uah(15000)),
	))
	if d.standing != standingTerminal {
		t.Errorf("standing = %d, want terminal", d.standing)
	}
	if d.terminalArrears != uah(3200) {
		t.Errorf("terminalArrears = %d, want 3200 ₴", d.terminalArrears)
	}
}

func TestTerminalEventIsTheMostRecentOne(t *testing.T) {
	e := terminalEventOf([]ubki.Snapshot{
		balanceSnap(daysAgo(900), ubki.StatusSold, 0, 0, 0, 0),
		balanceSnap(daysAgo(300), ubki.StatusWrittenOff, 0, 0, 0, 0),
		balanceSnap(daysAgo(100), ubki.StatusClosed, 0, 0, 0, 0),
	})
	if !e.happened || !e.at.Equal(daysAgo(300)) {
		t.Errorf("event = %+v, want the write-off %v", e, daysAgo(300))
	}
}

// An undated terminal snapshot must be treated as recent by both arrears and cap checks.
func TestUndatedTerminalSnapshotMakesTheEventRecent(t *testing.T) {
	start := ubki.NewDate(daysAgo(3000))
	d := classify(ubki.Deal{
		Role: ubki.RoleBorrower,
		History: []ubki.Snapshot{
			{StartedOn: start, Status: ubki.StatusOpen, DaysOverdue: 90, Overdue: uah(5000), ReportedOn: ubki.NewDate(daysAgo(2500))},
			{StartedOn: start, Status: ubki.StatusSold}, // no report date
			{StartedOn: start, Status: ubki.StatusSold, ReportedOn: ubki.NewDate(daysAgo(2400))},
		},
	})
	cfg := DefaultConfig()

	if !d.terminal.within(testAsOf, cfg.TerminalLookbackMonths) {
		t.Error("an undated sale is not shown to be old, so it is recent")
	}
	if overdue, _ := d.arrears(testAsOf, cfg); !overdue {
		t.Error("the arrears rule must agree with the ceilings about the same sale")
	}
}

// A sale on 3 April 2025 reaches the 36-month boundary on 3 April 2028.
func TestTerminalWindowIsCalendarMonths(t *testing.T) {
	sold := terminalEvent{happened: true, at: time.Date(2025, 4, 3, 0, 0, 0, 0, time.UTC)}

	if !sold.within(time.Date(2028, 4, 3, 0, 0, 0, 0, time.UTC), 36) {
		t.Error("exactly 36 months later the sale is still within 36 months")
	}
	if sold.within(time.Date(2028, 4, 4, 0, 0, 0, 0, time.UTC), 36) {
		t.Error("a day past 36 months the sale is outside the window")
	}
	if (terminalEvent{}).within(testAsOf, 36) {
		t.Error("a deal that was never sold or written off has no terminal event")
	}
}
