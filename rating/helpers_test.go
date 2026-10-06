package rating

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sfactor/scoring/ubki"
)

// testAsOf keeps synthetic tests independent of the current date.
var testAsOf = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

func daysAgo(n int) time.Time { return testAsOf.AddDate(0, 0, -n) }

// uah converts a test amount in hryvnias to kopiykas.
func uah(v float64) ubki.Money { return ubki.Money(math.Round(v * 100)) }

// snap creates a monthly report snapshot.
func snap(when, start time.Time, status ubki.DealStatus, dpd int, overdue, payment ubki.Money) ubki.Snapshot {
	return ubki.Snapshot{
		ReportedOn:  ubki.NewDate(when),
		StartedOn:   ubki.NewDate(start),
		Status:      status,
		DaysOverdue: ubki.Int(dpd),
		Overdue:     overdue,
		Payment:     payment,
	}
}

// loan creates a borrower-owned microfinance deal.
func loan(history ...ubki.Snapshot) ubki.Deal {
	return ubki.Deal{Role: ubki.RoleBorrower, LenderType: ubki.CreditorMFO, History: history}
}

// guarantee creates a deal where the subject is a guarantor.
func guarantee(history ...ubki.Snapshot) ubki.Deal {
	return ubki.Deal{Role: ubki.RoleGuarantor, LenderType: ubki.CreditorMFO, History: history}
}

// lenderDeal creates a borrower deal for the given lender type.
func lenderDeal(lender string, history ...ubki.Snapshot) ubki.Deal {
	return ubki.Deal{Role: ubki.RoleBorrower, LenderType: lender, History: history}
}

func dealsSection(deals ...ubki.Deal) ubki.Section {
	return ubki.Section{ID: ubki.SectionDeals, Deals: deals}
}

func inquiriesSection(inquiries ...ubki.Inquiry) ubki.Section {
	return ubki.Section{ID: ubki.SectionInquiries, Inquiries: inquiries}
}

func monitoringSection(subs ...ubki.Subscription) ubki.Section {
	return ubki.Section{ID: ubki.SectionMonitoring, Monitoring: subs}
}

// application creates a credit application for the given lender type.
func application(lender string, ago int) ubki.Inquiry {
	return ubki.Inquiry{
		Requester:  lender,
		Reason:     ubki.ReasonCredit,
		ReportType: "2",
		Date:       ubki.NewDate(daysAgo(ago)),
	}
}

func reportOf(sections ...ubki.Section) *ubki.Report {
	return &ubki.Report{Sections: sections}
}

// rate scores a synthetic report using testAsOf and the default settings.
func rate(r *ubki.Report, income ubki.Money) Result {
	return rateWith(r, income, DefaultConfig())
}

func rateWith(r *ubki.Report, income ubki.Money, cfg Config) Result {
	return Rate(Input{Report: r, MonthlyIncome: income, AsOf: testAsOf}, cfg)
}

// measureOf measures a synthetic report using testAsOf and the default settings.
func measureOf(r *ubki.Report, income ubki.Money) measurements {
	cfg := DefaultConfig()
	return measure(Input{Report: r, MonthlyIncome: income, AsOf: testAsOf}, borrowerDeals(r), testAsOf, cfg)
}

// wantValue checks that an indicator is present and has the expected value.
func wantValue(t *testing.T, m measurements, k ParamKey, want float64) {
	t.Helper()
	got, ok := m.values[k]
	if !ok {
		t.Fatalf("%s: not measured, want %v", k, want)
	}
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", k, got, want)
	}
}

func wantUnmeasured(t *testing.T, m measurements, k ParamKey) {
	t.Helper()
	if got, ok := m.values[k]; ok {
		t.Errorf("%s = %v, want it unmeasured", k, got)
	}
}

func hasFlag(flags []Flag, want Flag) bool { return slices.Contains(flags, want) }

func loadFixture(t *testing.T, name string) *ubki.Report {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	r, err := ubki.Parse(data)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return r
}

// fixtureNames lists the reports used by the fixture and golden tests.
func fixtureNames(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "*.xml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(p)
	}
	return names
}
