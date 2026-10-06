package rating

import (
	"testing"

	"github.com/sfactor/scoring/ubki"
)

// Guarantees must not contribute to the borrower's arrears.
func TestGuaranteesAreNotTheSubjectsOwnCredit(t *testing.T) {
	m := measureOf(reportOf(dealsSection(
		guarantee(snap(daysAgo(10), daysAgo(400), ubki.StatusOpen, 120, uah(9000), uah(1500))),
		loan(snap(daysAgo(10), daysAgo(400), ubki.StatusOpen, 0, 0, uah(800))),
	)), 0)

	wantValue(t, m, ParamOverdueDeals, 0)
	wantValue(t, m, ParamOverdueAmount, 0)
	wantValue(t, m, ParamActiveDeals, 1)
}

// Terminal debts count as active and overdue using the amount reported before sale or
// write-off.
func TestSoldAndWrittenOffDebtsStayLiveAndOverdue(t *testing.T) {
	for _, status := range []ubki.DealStatus{ubki.StatusSold, ubki.StatusWrittenOff} {
		t.Run(string(status), func(t *testing.T) {
			start := daysAgo(500)
			m := measureOf(reportOf(dealsSection(loan(
				snap(daysAgo(200), start, ubki.StatusOpen, 90, uah(5000), uah(1000)),
				snap(daysAgo(100), start, status, 0, 0, 0),
			))), 0)

			wantValue(t, m, ParamClosedDeals, 0)
			wantValue(t, m, ParamActiveDeals, 1)
			wantValue(t, m, ParamOverdueDeals, 1)
			wantValue(t, m, ParamOverdueAmount, 5000)
			wantValue(t, m, ParamCleanStreak, 0)
		})
	}
}

// After the terminal lookback, the debt remains active but no longer counts as current
// arrears or resets the clean streak.
func TestTerminalDebtStopsBeingArrearsAfterTheLookback(t *testing.T) {
	writtenOff := func(monthsAgo int) *ubki.Report {
		at := testAsOf.AddDate(0, -monthsAgo, 0)
		start := daysAgo(2500)
		return reportOf(dealsSection(loan(
			snap(at.AddDate(0, 0, -60), start, ubki.StatusOpen, 120, uah(9000), uah(2000)),
			snap(at, start, ubki.StatusWrittenOff, 0, 0, 0),
		)))
	}

	t.Run("inside the lookback", func(t *testing.T) {
		m := measureOf(writtenOff(10), 0)
		wantValue(t, m, ParamOverdueDeals, 1)
		wantValue(t, m, ParamOverdueAmount, 9000)
		wantValue(t, m, ParamActiveDeals, 1)
		wantValue(t, m, ParamCleanStreak, 0)
	})

	t.Run("past the lookback", func(t *testing.T) {
		m := measureOf(writtenOff(40), 0)
		wantValue(t, m, ParamOverdueDeals, 0)
		wantValue(t, m, ParamOverdueAmount, 0)
		wantValue(t, m, ParamActiveDeals, 1)
		wantValue(t, m, ParamClosedDeals, 0)
		if streak := m.values[ParamCleanStreak]; streak <= 0 {
			t.Errorf("clean streak = %v, want it running from the last real arrears", streak)
		}
	})
}

// Undated terminal events are treated as current.
func TestUndatedTerminalSnapshotIsCurrentArrears(t *testing.T) {
	start := daysAgo(2500)
	m := measureOf(reportOf(dealsSection(loan(
		snap(daysAgo(1200), start, ubki.StatusOpen, 120, uah(9000), uah(2000)),
		ubki.Snapshot{StartedOn: ubki.NewDate(start), Status: ubki.StatusWrittenOff},
	))), 0)

	wantValue(t, m, ParamOverdueDeals, 1)
	wantValue(t, m, ParamOverdueAmount, 9000)
	wantValue(t, m, ParamActiveDeals, 1)
}

// A terminal debt with no reported amount must still count as overdue.
func TestTerminalDebtIsOverdueEvenWithoutAnAmount(t *testing.T) {
	m := measureOf(reportOf(dealsSection(purposeDeal(purposeConsumer,
		balanceSnap(daysAgo(300), ubki.StatusOpen, 0, 0, 0, uah(5000)),
		balanceSnap(daysAgo(100), ubki.StatusWrittenOff, 0, 0, 0, uah(5000)),
	))), 0)

	wantValue(t, m, ParamOverdueDeals, 1)
	wantValue(t, m, ParamOverdueAmount, 0)
	wantValue(t, m, ParamActiveDeals, 1)
	wantValue(t, m, ParamClosedDeals, 0)
}

func TestClosedDealCountsAsRepaid(t *testing.T) {
	start := daysAgo(500)
	m := measureOf(reportOf(dealsSection(loan(
		snap(daysAgo(200), start, ubki.StatusOpen, 0, 0, uah(1000)),
		snap(daysAgo(100), start, ubki.StatusClosed, 0, 0, 0),
	))), 0)

	wantValue(t, m, ParamClosedDeals, 1)
	wantValue(t, m, ParamActiveDeals, 0)
}

func TestCleanStreak(t *testing.T) {
	start := daysAgo(700)
	tests := []struct {
		name    string
		history []ubki.Snapshot
		want    float64
	}{
		{
			name:    "never late: as long as the history",
			history: []ubki.Snapshot{snap(daysAgo(30), daysAgo(365), ubki.StatusOpen, 0, 0, uah(500))},
			want:    365,
		},
		{
			name: "from the last snapshot in arrears",
			history: []ubki.Snapshot{
				snap(daysAgo(400), start, ubki.StatusOpen, 45, uah(800), uah(500)),
				snap(daysAgo(250), start, ubki.StatusOpen, 0, 0, uah(500)),
				snap(daysAgo(20), start, ubki.StatusOpen, 0, 0, uah(500)),
			},
			want: 400,
		},
		{
			// An old snapshot still shows current arrears.
			name:    "zero while overdue today",
			history: []ubki.Snapshot{snap(daysAgo(23), start, ubki.StatusOpen, 15, uah(700), uah(500))},
			want:    0,
		},
		{
			// A 4 UAH residue must not reset the clean streak.
			name: "a residue does not break it",
			history: []ubki.Snapshot{
				snap(daysAgo(400), start, ubki.StatusOpen, 45, uah(800), uah(500)),
				snap(daysAgo(120), start, ubki.StatusOpen, 3, uah(4), uah(500)),
				snap(daysAgo(20), start, ubki.StatusOpen, 0, 0, uah(500)),
			},
			want: 400,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantValue(t, measureOf(reportOf(dealsSection(loan(tt.history...))), 0), ParamCleanStreak, tt.want)
		})
	}
}

func TestNewDealWindowIsSixMonthsInclusive(t *testing.T) {
	tests := []struct {
		startedAgo int
		want       float64
	}{
		{182, 1}, {183, 1}, {184, 0},
	}
	for _, tt := range tests {
		m := measureOf(reportOf(dealsSection(loan(
			snap(daysAgo(1), daysAgo(tt.startedAgo), ubki.StatusOpen, 0, 0, uah(500)),
		))), 0)
		wantValue(t, m, ParamNewDeals, tt.want)
	}
}

// Missing start dates leave history age, new loans and a never-overdue streak unmeasured.
func TestHistoryIsUnmeasuredWithoutStartDates(t *testing.T) {
	m := measureOf(reportOf(dealsSection(ubki.Deal{
		Role:    ubki.RoleBorrower,
		History: []ubki.Snapshot{{Status: ubki.StatusOpen}},
	})), 0)

	wantUnmeasured(t, m, ParamHistoryAge)
	wantUnmeasured(t, m, ParamNewDeals)
	wantUnmeasured(t, m, ParamCleanStreak)
	wantValue(t, m, ParamActiveDeals, 1)
}

func TestOnlyCreditApplicationsCountAsInquiries(t *testing.T) {
	inquiry := func(requester, reason, reportType string, ago int) ubki.Inquiry {
		return ubki.Inquiry{Requester: requester, Reason: reason, ReportType: reportType, Date: ubki.NewDate(daysAgo(ago))}
	}
	m := measureOf(reportOf(
		dealsSection(loan(snap(daysAgo(1), daysAgo(400), ubki.StatusOpen, 0, 0, uah(500)))),
		inquiriesSection(
			inquiry("MFO", "2", "2", 10),  // counts: an application
			inquiry("BNK", "4", "2", 20),  // counts: an online application
			inquiry("OWN", "2", "2", 30),  // not: our own refresh pull
			inquiry("MFO", "6", "2", 40),  // not: a verification
			inquiry("MFO", "2", "1", 50),  // not: an identity-only report
			inquiry("MFO", "2", "2", 200), // not: older than six months
		),
	), 0)

	wantValue(t, m, ParamInquiries, 2)
}

func TestNoInquiryRegistryIsUnmeasuredNotZero(t *testing.T) {
	m := measureOf(reportOf(dealsSection(loan(
		snap(daysAgo(1), daysAgo(400), ubki.StatusOpen, 0, 0, uah(500)),
	))), 0)

	wantUnmeasured(t, m, ParamInquiries)
}

func TestDebtLoadIncome(t *testing.T) {
	declared := func(ago int) ubki.Section {
		return ubki.Section{ID: ubki.SectionIdentity, Subject: &ubki.Subject{Employment: []ubki.Employment{
			{MonthlyIncome: uah(10000), VerifiedOn: ubki.NewDate(daysAgo(ago))},
		}}}
	}
	deals := dealsSection(loan(snap(daysAgo(1), daysAgo(400), ubki.StatusOpen, 0, 0, uah(3000))))

	tests := []struct {
		name   string
		report *ubki.Report
		income ubki.Money
		want   float64
		source IncomeSource
	}{
		{"verified income comes first", reportOf(deals, declared(90)), uah(20000), 15, IncomeFromProfile},
		{"a recent declaration stands in", reportOf(deals, declared(90)), 0, 30, IncomeFromUBKI},
		{"a declaration exactly a year old still stands in", reportOf(deals, declared(365)), 0, 30, IncomeFromUBKI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := measureOf(tt.report, tt.income)
			wantValue(t, m, ParamDebtLoad, tt.want)
			if m.incomeSource != tt.source {
				t.Errorf("income source = %q, want %q", m.incomeSource, tt.source)
			}
		})
	}

	// The declared income is too old to use.
	m := measureOf(reportOf(deals, declared(730)), 0)
	wantUnmeasured(t, m, ParamDebtLoad)
	if m.incomeSource != "" {
		t.Errorf("income source = %q, want none", m.incomeSource)
	}
}

// Use the bureau total when scheduled payments omit revolving obligations.
func TestDebtLoadPrefersTheBureausObligationTotal(t *testing.T) {
	m := measureOf(reportOf(
		dealsSection(loan(snap(daysAgo(1), daysAgo(400), ubki.StatusOpen, 0, 0, 0))),
		ubki.Section{ID: ubki.SectionSummary, Summary: &ubki.Summary{MonthlyObligations: uah(6000)}},
	), uah(20000))

	wantValue(t, m, ParamDebtLoad, 30)
}

// Residues at or below the threshold are excluded from every arrears metric.
func TestResidueIsNotArrears(t *testing.T) {
	tests := []struct {
		name    string
		residue ubki.Money
		deals   float64
		amount  float64
	}{
		{"below the threshold", uah(9.99), 0, 0},
		{"exactly at it", uah(10), 0, 0},
		{"just above it", uah(10.01), 1, 10.01},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := measureOf(reportOf(dealsSection(loan(
				snap(daysAgo(10), daysAgo(600), ubki.StatusOpen, 7, tt.residue, uah(500)),
			))), 0)
			wantValue(t, m, ParamOverdueDeals, tt.deals)
			wantValue(t, m, ParamOverdueAmount, tt.amount)
		})
	}
}

// The overdue amount must include exactly the deals counted as overdue.
func TestOverdueCountAndAmountDescribeTheSameDeals(t *testing.T) {
	start := daysAgo(600)
	m := measureOf(reportOf(dealsSection(
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 12, uah(4.50), uah(500))), // residue
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 40, uah(2000), uah(900))), // arrears
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 0, uah(250), uah(700))),   // no days past due
		loan(snap(daysAgo(10), start, ubki.StatusClosed, 30, uah(800), 0)),       // not live
	)), 0)

	wantValue(t, m, ParamOverdueDeals, 1)
	wantValue(t, m, ParamOverdueAmount, 2000)
}

// Integer kopiykas keep sums exactly on the 100 and 1,000 UAH band boundaries; float sums
// previously fell on the wrong side.
func TestOverdueAmountIsSummedExactly(t *testing.T) {
	overdue := func(amounts ...float64) measurements {
		var deals []ubki.Deal
		for _, a := range amounts {
			deals = append(deals, loan(snap(daysAgo(10), daysAgo(600), ubki.StatusOpen, 30, uah(a), uah(100))))
		}
		return measureOf(reportOf(dealsSection(deals...)), 0)
	}

	tests := []struct {
		amounts []float64
		want    ubki.Money
	}{
		{[]float64{10.14, 58.12, 31.74}, 100 * ubki.Hryvnia},
		{[]float64{11.44, 512.19, 476.37}, 1000 * ubki.Hryvnia},
	}
	for _, tt := range tests {
		m := overdue(tt.amounts...)
		if got := m.book.overdueAmount; got != tt.want {
			t.Errorf("%v: overdue amount = %d kopiykas, want exactly %d", tt.amounts, got, tt.want)
		}
		if level := levelOf(ParamOverdueAmount, m.values[ParamOverdueAmount]); level != LevelMedium {
			t.Errorf("%v: %v ₴ overdue reads %q, want medium (100–1000 ₴)", tt.amounts, tt.want.Hryvnias(), level)
		}
	}
}
