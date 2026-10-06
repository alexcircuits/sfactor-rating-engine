package rating

import (
	"errors"
	"math"
	"time"

	"github.com/sfactor/scoring/ubki"
)

// Input contains the report, verified income and optional reference date.
type Input struct {
	// Report is the borrower's УБКІ credit report.
	Report *ubki.Report

	// MonthlyIncome is verified income in kopiykas. Zero uses the declared-income
	// fallback, if available.
	MonthlyIncome ubki.Money

	// AsOf is the reference date. Zero uses the report build date; dates before the
	// report are rejected by Validate.
	AsOf time.Time
}

// ErrAsOfBeforeReport indicates a reference date before the report was built. The report
// cannot be used to reconstruct an earlier credit file.
var ErrAsOfBeforeReport = errors.New("rating: the reference date is before the report was built")

// Validate checks that the reference date is valid for the report.
func (in Input) Validate() error {
	if in.AsOf.IsZero() {
		return nil
	}
	if built, ok := in.Report.BuiltAt(); ok && calendarDay(in.AsOf).Before(calendarDay(built)) {
		return ErrAsOfBeforeReport
	}
	return nil
}

// dateLayout is the date format used in JSON responses.
const dateLayout = "2006-01-02"

// Rate calculates the rating. Call Input.Validate first when accepting an explicit
// reference date.
func Rate(in Input, cfg Config) Result {
	asOf := referenceDate(in)
	if in.Report == nil {
		return unavailable(asOf)
	}
	deals := borrowerDeals(in.Report)
	if len(deals) == 0 {
		return unavailable(asOf)
	}

	m := measure(in, deals, asOf, cfg)
	params := scoreIndicators(m.values)
	raw := sumContributions(params)
	mfo := lenderBreakdown(deals, m.applications)

	res := Result{
		Available:    true,
		AsOf:         asOf.Format(dateLayout),
		Score:        int(math.Round(clamp(raw, 0, 100))),
		Raw:          raw,
		IncomeSource: m.incomeSource,
		Flags:        flagsFor(in.Report, deals, m.values, mfo.ActiveMFODeals, cfg),
		MFO:          mfo,
		Monitoring:   monitoringEntries(in.Report.Monitoring(), asOf),
		Parameters:   params,
	}
	// Mark the rating as capped only when the cap actually reduces the score.
	if c, ok := ceilingFor(deals, m.book, asOf, cfg); ok && res.Score > c.score {
		res.Score = c.score
		res.Capped = true
		res.CapReason = c.reason
	}
	res.Band, res.BandLabel = bandOf(res.Score)
	return res
}

// referenceDate uses AsOf, the report build date or today, in that order.
func referenceDate(in Input) time.Time {
	if !in.AsOf.IsZero() {
		return calendarDay(in.AsOf)
	}
	if built, ok := in.Report.BuiltAt(); ok {
		return calendarDay(built)
	}
	return calendarDay(time.Now())
}

// calendarDay preserves t's local date and represents it at midnight UTC.
func calendarDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// unavailable returns the response for a report with no eligible credit deals.
func unavailable(asOf time.Time) Result {
	band, label := bandOf(0)
	return Result{
		Reason:     ReasonNoCreditHistory,
		AsOf:       asOf.Format(dateLayout),
		Band:       band,
		BandLabel:  label,
		Parameters: []Parameter{},
	}
}

// sumContributions sums the displayed contributions and rounds the total to one decimal
// place.
func sumContributions(params []Parameter) float64 {
	sum := 0.0
	for _, p := range params {
		sum += p.Contribution
	}
	return round1(sum)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func clamp(v, lo, hi float64) float64 { return math.Min(hi, math.Max(lo, v)) }
