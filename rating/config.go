package rating

import "github.com/sfactor/scoring/ubki"

// Config holds the rating thresholds and time windows. These are initial model
// assumptions and have not yet been fitted to repayment outcomes.
type Config struct {
	// OverdueIgnoreAmount excludes small residual balances from all arrears
	// indicators. Amounts at or below this threshold are ignored.
	OverdueIgnoreAmount ubki.Money

	// RecentWindowDays is the lookback in days for new loans and credit applications.
	RecentWindowDays int

	// TerminalLookbackMonths is the period in calendar months during which a sold or
	// written-off debt counts as overdue and qualifies for the hard cap.
	TerminalLookbackMonths int

	// CapScore is the hard score cap.
	CapScore int

	// CapMinOverdueAmount is the minimum total arrears needed for the hard cap, after
	// small residual balances have been excluded.
	CapMinOverdueAmount ubki.Money

	// CapTerminalDebt enables both caps for sold and written-off debts.
	CapTerminalDebt bool

	// CapTerminalSoftScore caps older terminal debts within
	// TerminalSoftLookbackMonths. A zero score or window disables this cap.
	CapTerminalSoftScore       int
	TerminalSoftLookbackMonths int

	// IncomeMaxAgeMonths limits the age of declared income used when verified income
	// is unavailable.
	IncomeMaxAgeMonths int

	// MFOPressureDeals is the active microfinance loan count that triggers
	// mfo_pressure.
	MFOPressureDeals int
}

// DefaultConfig returns the default model settings.
func DefaultConfig() Config {
	return Config{
		OverdueIgnoreAmount:        10 * ubki.Hryvnia,
		RecentWindowDays:           183,
		TerminalLookbackMonths:     36, // hard-cap lookback: 3 years
		CapScore:                   45,
		CapMinOverdueAmount:        100 * ubki.Hryvnia,
		CapTerminalDebt:            true,
		CapTerminalSoftScore:       74,
		TerminalSoftLookbackMonths: 72, // soft-cap lookback: 6 years
		IncomeMaxAgeMonths:         12,
		MFOPressureDeals:           2,
	}
}
