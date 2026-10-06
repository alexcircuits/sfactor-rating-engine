package rating

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/sfactor/scoring/ubki"
)

// cleanBorrower has five years of history, one current loan and four repaid loans, with
// no arrears or recent applications.
func cleanBorrower() *ubki.Report {
	deals := []ubki.Deal{loan(snap(daysAgo(10), daysAgo(1800), ubki.StatusOpen, 0, 0, uah(2000)))}
	for i := range 4 {
		start := daysAgo(1700 - i*100)
		deals = append(deals, loan(snap(start.AddDate(0, 0, 200), start, ubki.StatusClosed, 0, 0, 0)))
	}
	return reportOf(dealsSection(deals...), inquiriesSection())
}

// overdueBorrower uses the same file with arrears on the active loan.
func overdueBorrower(amount ubki.Money) *ubki.Report {
	r := cleanBorrower()
	r.Sections[0].Deals[0] = loan(snap(daysAgo(10), daysAgo(1800), ubki.StatusOpen, 20, amount, uah(2000)))
	return r
}

// writtenOff creates a debt written off terminalAgo days ago with no reported amount.
func writtenOff(terminalAgo int) *ubki.Report {
	start := daysAgo(1800)
	return reportOf(dealsSection(loan(
		snap(daysAgo(terminalAgo+60), start, ubki.StatusOpen, 0, 0, uah(2000)),
		snap(daysAgo(terminalAgo), start, ubki.StatusWrittenOff, 0, 0, 0),
	)), inquiriesSection())
}

// The raw score must equal the sum of the displayed contributions.
func TestContributionsAddUpToTheRating(t *testing.T) {
	for _, tt := range []struct {
		name   string
		report *ubki.Report
		income ubki.Money
	}{
		{"clean, with income", cleanBorrower(), uah(20000)},
		{"clean, without income", cleanBorrower(), 0},
		{"overdue", overdueBorrower(uah(5000)), uah(20000)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := rate(tt.report, tt.income)
			sum := 0.0
			for _, p := range res.Parameters {
				sum += p.Contribution
			}
			if math.Abs(sum-res.Raw) > 0.001 {
				t.Errorf("contributions add up to %v, raw is %v", sum, res.Raw)
			}
		})
	}
}

func TestMissingIncomeRedistributesWeightInsteadOfPenalizing(t *testing.T) {
	with := rate(cleanBorrower(), uah(20000))
	without := rate(cleanBorrower(), 0)

	load, _ := without.Parameter(ParamDebtLoad)
	if load.Level != LevelUnknown || load.Contribution != 0 {
		t.Errorf("debt load = %q contributing %v, want unknown contributing nothing", load.Level, load.Contribution)
	}
	if without.IncomeSource != "" {
		t.Errorf("income source = %q, want none", without.IncomeSource)
	}
	if without.Band != with.Band || without.Score < with.Score-3 {
		t.Errorf("missing income moved the rating from %d (%s) to %d (%s)",
			with.Score, with.Band, without.Score, without.Band)
	}
}

func TestIndicatorsAreListedInTableOrder(t *testing.T) {
	res := rate(cleanBorrower(), uah(20000))
	if len(res.Parameters) != len(indicators) {
		t.Fatalf("%d indicators, want %d", len(res.Parameters), len(indicators))
	}
	for i, p := range res.Parameters {
		if p.Key != indicators[i].key || p.Order != i+1 {
			t.Errorf("position %d holds %s (order %d), want %s (order %d)", i, p.Key, p.Order, indicators[i].key, i+1)
		}
	}
}

func TestCleanFileRatesHigh(t *testing.T) {
	res := rate(cleanBorrower(), uah(20000))
	if !res.Available || res.Band != BandHigh || res.Capped {
		t.Errorf("clean file: available=%v band=%s capped=%v (score %d)", res.Available, res.Band, res.Capped, res.Score)
	}
}

func TestLiveArrearsCapTheRatingInTheRed(t *testing.T) {
	res := rate(overdueBorrower(uah(5000)), uah(20000))

	if !res.Capped || res.CapReason != CapCurrentOverdue {
		t.Fatalf("capped=%v reason=%q, want %q", res.Capped, res.CapReason, CapCurrentOverdue)
	}
	if res.Score != DefaultConfig().CapScore || res.Band != BandLow {
		t.Errorf("score %d (%s), want the ceiling %d in the red", res.Score, res.Band, DefaultConfig().CapScore)
	}
}

// Arrears totalling exactly 100 UAH must reach the cap threshold.
func TestArrearsOfExactlyTheThresholdCapTheRating(t *testing.T) {
	r := cleanBorrower()
	start := daysAgo(1800)
	r.Sections[0].Deals = append(r.Sections[0].Deals[1:],
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 30, uah(10.14), uah(700))),
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 30, uah(58.12), uah(700))),
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 30, uah(31.74), uah(700))),
	)
	cfg := DefaultConfig()
	cfg.CapScore = 1 // low enough to make an applied cap visible

	if res := rateWith(r, uah(20000), cfg); res.CapReason != CapCurrentOverdue {
		t.Errorf("cap reason = %q, want %q for 100.00 ₴ overdue", res.CapReason, CapCurrentOverdue)
	}
}

// A 3 UAH residue must not trigger the arrears cap.
func TestResidueDoesNotCapTheRating(t *testing.T) {
	res := rate(overdueBorrower(uah(3)), uah(20000))
	if res.Capped || res.Score <= DefaultConfig().CapScore {
		t.Errorf("a 3 ₴ residue: capped=%v score=%d", res.Capped, res.Score)
	}
}

// A score already below the cap must not be marked capped.
func TestCeilingNeverRaisesAScore(t *testing.T) {
	deals := make([]ubki.Deal, 8)
	for i := range deals {
		deals[i] = loan(snap(daysAgo(5), daysAgo(120-i*10), ubki.StatusOpen, 60, uah(4000), uah(3000)))
	}
	res := rate(reportOf(dealsSection(deals...), inquiriesSection()), uah(10000))

	if res.Score > DefaultConfig().CapScore {
		t.Fatalf("a deeply distressed file scored %d", res.Score)
	}
	if res.Capped {
		t.Error("capped although the ceiling changed nothing")
	}
}

// When a terminal debt has a recoverable arrears amount, the current-overdue cap can
// apply without the terminal cap.
func TestRecoveredWriteOffCapsAsLiveArrears(t *testing.T) {
	start := daysAgo(1800)
	res := rate(reportOf(dealsSection(loan(
		snap(daysAgo(360), start, ubki.StatusOpen, 120, uah(9000), uah(2000)),
		snap(daysAgo(300), start, ubki.StatusWrittenOff, 0, 0, 0),
	)), inquiriesSection()), uah(20000))

	if res.CapReason != CapCurrentOverdue || res.Band != BandLow {
		t.Errorf("cap reason %q, band %s; want %q in the red", res.CapReason, res.Band, CapCurrentOverdue)
	}
}

// Terminal debts with no reported amount still qualify for a cap. Check both the hard and
// soft lookback windows.
func TestUnpricedWriteOffStepsDownThroughTwoCeilings(t *testing.T) {
	cfg := DefaultConfig()
	tests := []struct {
		name   string
		ago    int
		reason CapReason
		score  int
		band   Band
	}{
		{"ten months on: held in the red", 300, CapTerminalDebt, cfg.CapScore, BandLow},
		{"five years on: not red, not clear", 1825, CapTerminalDebtAged, cfg.CapTerminalSoftScore, BandMedium},
		{"past both windows: nothing left but the flag", 2400, "", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := rate(writtenOff(tt.ago), uah(20000))
			if res.CapReason != tt.reason {
				t.Fatalf("cap reason = %q, want %q (score %d)", res.CapReason, tt.reason, res.Score)
			}
			if tt.reason != "" && (res.Score != tt.score || res.Band != tt.band) {
				t.Errorf("score %d (%s), want %d (%s)", res.Score, res.Band, tt.score, tt.band)
			}
			if !hasFlag(res.Flags, FlagWriteOff) {
				t.Errorf("flags = %v: the write-off itself never stops being true", res.Flags)
			}
		})
	}
}

func TestTerminalCeilingsCanBeSwitchedOff(t *testing.T) {
	tests := []struct {
		name  string
		ago   int
		tweak func(*Config)
	}{
		{"the soft tier by its score", 1825, func(c *Config) { c.CapTerminalSoftScore = 0 }},
		{"the soft tier by its window", 1825, func(c *Config) { c.TerminalSoftLookbackMonths = 0 }},
		{"both tiers, recent debt", 300, func(c *Config) { c.CapTerminalDebt = false }},
		{"both tiers, aged debt", 1825, func(c *Config) { c.CapTerminalDebt = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.tweak(&cfg)
			if res := rateWith(writtenOff(tt.ago), uah(20000), cfg); res.Capped {
				t.Errorf("capped at %d by %q although switched off", res.Score, res.CapReason)
			}
		})
	}
}

// Current arrears take precedence over a terminal-debt cap.
func TestLiveArrearsOutrankAWriteOff(t *testing.T) {
	start := daysAgo(1800)
	res := rate(reportOf(dealsSection(
		loan(snap(daysAgo(300), start, ubki.StatusWrittenOff, 0, 0, 0)),
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 30, uah(4000), uah(1500))),
	), inquiriesSection()), uah(20000))

	if res.CapReason != CapCurrentOverdue {
		t.Errorf("cap reason = %q, want %q", res.CapReason, CapCurrentOverdue)
	}
}

// Residue filtering happens before the cap threshold check. Lowering the cap threshold
// must not include an already excluded residue.
func TestOverdueThresholdsApplyInOrder(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CapMinOverdueAmount = 0
	if res := rateWith(overdueBorrower(uah(3)), uah(20000), cfg); res.Capped {
		t.Errorf("a 3 ₴ residue capped the rating at %d", res.Score)
	}

	cfg.OverdueIgnoreAmount = 0
	if res := rateWith(overdueBorrower(uah(3)), uah(20000), cfg); !res.Capped {
		t.Error("with both thresholds at zero, any arrears cap the rating")
	}
}

// A missing report and a report with no credit deals should return the same unavailable
// response.
func TestNoCreditHistoryIsNotRated(t *testing.T) {
	for _, tt := range []struct {
		name   string
		report *ubki.Report
	}{
		{"no report", nil},
		{"a report without deals", reportOf(inquiriesSection())},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := Rate(Input{Report: tt.report, AsOf: testAsOf}, DefaultConfig())

			if res.Available || res.Reason != ReasonNoCreditHistory || res.Score != 0 {
				t.Errorf("available=%v reason=%q score=%d, want unavailable for %q",
					res.Available, res.Reason, res.Score, ReasonNoCreditHistory)
			}
			if res.AsOf != "2026-07-01" || res.Band != BandLow || res.BandLabel == "" {
				t.Errorf("as_of=%q band=%q label=%q, want the reference date and the low band", res.AsOf, res.Band, res.BandLabel)
			}
			if res.Parameters == nil || len(res.Parameters) != 0 {
				t.Errorf("parameters = %#v, want an empty list", res.Parameters)
			}
		})
	}
}

// Reject reference dates before the report build date, since later snapshots would
// otherwise affect the result.
func TestAsOfCannotPrecedeTheReport(t *testing.T) {
	deals := dealsSection(loan(snap(daysAgo(10), daysAgo(400), ubki.StatusOpen, 0, 0, uah(500))))
	built := reportOf(deals)
	built.Trace = []ubki.TraceStep{{Name: "build report", Finished: "2026-07-08 13:16:40.356"}}
	day := func(s string) time.Time {
		t.Helper()
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	tests := []struct {
		name string
		in   Input
		want error
	}{
		{"the report's own date, by default", Input{Report: built}, nil},
		{"the build date itself", Input{Report: built, AsOf: day("2026-07-08")}, nil},
		{"a later date, ageing the report", Input{Report: built, AsOf: day("2028-07-08")}, nil},
		{"the day before the report was built", Input{Report: built, AsOf: day("2026-07-07")}, ErrAsOfBeforeReport},
		{"a report that does not say when it was built", Input{Report: reportOf(deals), AsOf: day("2020-01-01")}, nil},
		{"no report at all", Input{AsOf: day("2020-01-01")}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.in.Validate(); !errors.Is(err, tt.want) {
				t.Errorf("Validate() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestScoreAndPointsStayInRange(t *testing.T) {
	for _, tt := range []struct {
		name   string
		report *ubki.Report
		income ubki.Money
	}{
		{"clean", cleanBorrower(), uah(20000)},
		{"overdue", overdueBorrower(uah(50000)), uah(5000)},
		{"no income", cleanBorrower(), 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := rate(tt.report, tt.income)
			if res.Score < 0 || res.Score > 100 {
				t.Errorf("score = %d", res.Score)
			}
			for _, p := range res.Parameters {
				if p.Points < 0 || p.Points > 100 {
					t.Errorf("%s: points = %v", p.Key, p.Points)
				}
			}
		})
	}
}

// Check the JSON fields consumed by the app.
func TestResultJSONContract(t *testing.T) {
	raw, err := json.Marshal(rate(overdueBorrower(uah(5000)), uah(20000)))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"available", "as_of", "score", "raw", "band", "band_label",
		"capped", "cap_reason", "income_source", "mfo", "parameters",
	} {
		if _, ok := result[key]; !ok {
			t.Errorf("result has no %q", key)
		}
	}

	var params []map[string]json.RawMessage
	if err := json.Unmarshal(result["parameters"], &params); err != nil || len(params) != len(indicators) {
		t.Fatalf("parameters: %d entries (%v), want %d", len(params), err, len(indicators))
	}
	for _, key := range []string{
		"key", "order", "title", "value", "display", "level",
		"points", "weight", "contribution", "bands", "hint",
	} {
		if _, ok := params[0][key]; !ok {
			t.Errorf("parameter has no %q", key)
		}
	}
}
