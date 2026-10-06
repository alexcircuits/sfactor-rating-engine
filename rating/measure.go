package rating

import (
	"math"
	"time"

	"github.com/sfactor/scoring/ubki"
)

// measurements holds the indicator values and supporting data for the reference date.
type measurements struct {
	// values contains measured indicators only. Missing indicators are excluded from
	// weight normalization.
	values map[ParamKey]float64

	book         portfolio
	applications []ubki.Inquiry // applications counted by indicator 9
	incomeSource IncomeSource
}

// portfolio holds loan counts, arrears and scheduled payments.
type portfolio struct {
	open, repaid  int
	overdueDeals  int
	overdueAmount ubki.Money
	payments      ubki.Money // scheduled payments on active deals
}

func measure(in Input, deals []deal, asOf time.Time, cfg Config) measurements {
	window := asOf.AddDate(0, 0, -cfg.RecentWindowDays)
	book := tally(deals, asOf, cfg)
	m := measurements{
		book: book,
		// These four metrics can be measured even when their value is zero.
		values: map[ParamKey]float64{
			ParamOverdueDeals:  float64(book.overdueDeals),
			ParamOverdueAmount: book.overdueAmount.Hryvnias(),
			ParamActiveDeals:   float64(book.open),
			ParamClosedDeals:   float64(book.repaid),
		},
	}

	// History age and new-loan counts both require a known start date.
	first, known := firstCredit(deals)
	if known {
		m.values[ParamHistoryAge] = daysBetween(first, asOf)
		m.values[ParamNewDeals] = float64(startedWithin(deals, window, asOf))
	}
	if streak, ok := cleanStreak(deals, book.overdueDeals > 0, first, asOf, cfg); ok {
		m.values[ParamCleanStreak] = streak
	}
	if income, source := monthlyIncome(in, asOf, cfg); income > 0 {
		m.values[ParamDebtLoad] = debtLoad(in.Report, book, income)
		m.incomeSource = source
	}
	if apps, ok := creditApplications(in.Report, window, asOf); ok {
		m.values[ParamInquiries] = float64(len(apps))
		m.applications = apps
	}
	return m
}

// tally counts active, repaid and overdue deals and sums active loan payments.
func tally(deals []deal, asOf time.Time, cfg Config) portfolio {
	var p portfolio
	for _, d := range deals {
		if d.standing == standingRepaid {
			p.repaid++
		}
		if !d.open() {
			continue
		}
		p.open++
		p.payments += d.latest.Payment
		if overdue, amount := d.arrears(asOf, cfg); overdue {
			p.overdueDeals++
			p.overdueAmount += amount
		}
	}
	return p
}

// firstCredit returns the earliest known deal start date and whether one was found.
func firstCredit(deals []deal) (time.Time, bool) {
	var first time.Time
	for _, d := range deals {
		if !d.started.IsZero() && (first.IsZero() || d.started.Before(first)) {
			first = d.started
		}
	}
	return first, !first.IsZero()
}

// startedWithin counts deals starting within the inclusive date range.
func startedWithin(deals []deal, from, to time.Time) int {
	n := 0
	for _, d := range deals {
		if !d.started.IsZero() && !d.started.Before(from) && !d.started.After(to) {
			n++
		}
	}
	return n
}

// cleanStreak counts whole days since the last arrears snapshot. Current arrears reset it
// to zero, even if the snapshot is old. With no past arrears, it starts at the first
// credit date.
func cleanStreak(deals []deal, overdueNow bool, firstCredit time.Time, asOf time.Time, cfg Config) (float64, bool) {
	if overdueNow {
		return 0, true
	}
	if last, ok := lastArrears(deals, cfg); ok {
		return daysBetween(last, asOf), true
	}
	if firstCredit.IsZero() {
		return 0, false
	}
	return daysBetween(firstCredit, asOf), true
}

// lastArrears returns the most recent dated arrears snapshot across all deals.
func lastArrears(deals []deal, cfg Config) (time.Time, bool) {
	var last time.Time
	for _, d := range deals {
		for _, s := range d.history {
			if at, ok := s.ReportedOn.Time(); ok && overdueSnapshot(s, cfg) && at.After(last) {
				last = at
			}
		}
	}
	return last, !last.IsZero()
}

// monthlyIncome prefers verified income, then the latest positive declared income within
// IncomeMaxAgeMonths. Zero means neither source is usable.
func monthlyIncome(in Input, asOf time.Time, cfg Config) (ubki.Money, IncomeSource) {
	if in.MonthlyIncome > 0 {
		return in.MonthlyIncome, IncomeFromProfile
	}

	var income ubki.Money
	var declared time.Time
	for _, job := range in.Report.DeclaredEmployment() {
		on, ok := job.VerifiedOn.Time()
		if ok && job.MonthlyIncome > 0 && (income == 0 || on.After(declared)) {
			income, declared = job.MonthlyIncome, on
		}
	}
	if income == 0 || declared.Before(asOf.AddDate(0, -cfg.IncomeMaxAgeMonths, 0)) {
		return 0, ""
	}
	return income, IncomeFromUBKI
}

// debtLoad expresses monthly obligations as a percentage of income. It prefers the bureau
// total because revolving loans may omit scheduled payments.
func debtLoad(r *ubki.Report, book portfolio, income ubki.Money) float64 {
	obligations := book.payments
	if total := r.MonthlyObligations(); total > 0 {
		obligations = total
	}
	return float64(obligations) / float64(income) * 100
}

// creditApplications returns applications within the inclusive date range. The boolean
// distinguishes a missing inquiry registry from an empty one.
func creditApplications(r *ubki.Report, from, to time.Time) ([]ubki.Inquiry, bool) {
	inquiries, ok := r.Inquiries()
	if !ok {
		return nil, false
	}
	var apps []ubki.Inquiry
	for _, q := range inquiries {
		if at, dated := q.Date.Time(); dated && isCreditApplication(q) && !at.Before(from) && !at.After(to) {
			apps = append(apps, q)
		}
	}
	return apps, true
}

// isCreditApplication excludes OWN refreshes, identity-only reports and non-application
// reasons.
func isCreditApplication(q ubki.Inquiry) bool {
	if q.Requester == ubki.CreditorOwn || q.ReportType == ubki.ReportTypeIdentity {
		return false
	}
	return q.Reason == ubki.ReasonCredit || q.Reason == ubki.ReasonOnlineCredit
}

// daysBetween returns whole elapsed days, clamped to zero.
func daysBetween(a, b time.Time) float64 {
	return max(0, math.Floor(b.Sub(a).Hours()/24))
}
