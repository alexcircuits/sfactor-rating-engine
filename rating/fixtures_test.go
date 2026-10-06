package rating

import (
	"math"
	"testing"
	"time"
)

var fixtureAsOf = time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)

// These fixture checks cover the measured values and reference blocks. Golden tests cover
// the full JSON output.
func TestFixtureRatings(t *testing.T) {
	tests := []struct {
		file      string
		why       string
		score     int
		band      Band
		capReason CapReason
		flags     []Flag
		levels    map[ParamKey]Level
		values    map[ParamKey]float64
		watchers  int // monitoring subscriptions on file
		watching  int // active subscriptions
	}{
		{
			file:  "report1.xml",
			why:   "a payday loan 90 days past due, plus a written-off consumer loan УБКІ never priced",
			score: 31, band: BandLow,
			flags: []Flag{FlagWriteOff},
			levels: map[ParamKey]Level{
				ParamHistoryAge: LevelMedium, ParamCleanStreak: LevelBad, ParamOverdueDeals: LevelBad,
				ParamOverdueAmount: LevelBad, ParamClosedDeals: LevelBad, ParamDebtLoad: LevelUnknown,
			},
			values: map[ParamKey]float64{
				// The write-off counts as overdue with an unknown amount;
				// all three snapshots report zero.
				ParamOverdueDeals: 2, ParamOverdueAmount: 1500, ParamActiveDeals: 2,
				ParamInquiries: 15, // inquiry flag starts above 15
			},
			watchers: 11,
		},
		{
			file:  "report2.xml",
			why:   "a debt sold to a collector, priced by the indicators at the 1034.62 ₴ reported before the sale",
			score: 43, band: BandLow,
			flags: []Flag{FlagSold, FlagEnforcement, FlagMFOPressure},
			levels: map[ParamKey]Level{
				ParamHistoryAge: LevelGood, ParamCleanStreak: LevelBad, ParamOverdueDeals: LevelMedium,
				ParamOverdueAmount: LevelBad, ParamActiveDeals: LevelGood, ParamInquiries: LevelBad,
			},
			values: map[ParamKey]float64{
				ParamOverdueDeals: 1, ParamOverdueAmount: 1034.6, ParamActiveDeals: 1, ParamCleanStreak: 0,
				ParamInquiries: 17, // bad inquiry level triggers the flag
			},
			watchers: 7,
		},
		{
			file:  "report3.xml",
			why:   "clean and current, two active cards, nothing repaid yet",
			score: 81, band: BandHigh,
			levels: map[ParamKey]Level{
				ParamHistoryAge: LevelGood, ParamCleanStreak: LevelGood, ParamOverdueDeals: LevelGood,
				ParamClosedDeals: LevelBad, ParamInquiries: LevelMedium,
			},
			watchers: 8,
		},
		{
			file:  "report4.xml",
			why:   "a thin file, current; the declared income makes debt load measurable",
			score: 62, band: BandMedium,
			flags: []Flag{FlagEnforcement},
			levels: map[ParamKey]Level{
				ParamHistoryAge: LevelBad, ParamCleanStreak: LevelMedium, ParamDebtLoad: LevelMedium,
			},
			watchers: 8,
		},
		{
			file:  "report5.xml",
			why:   "nine years of history but overdue right now; the card closed with no limit stays repaid",
			score: 45, band: BandLow, capReason: CapCurrentOverdue,
			levels: map[ParamKey]Level{
				ParamHistoryAge: LevelGood, ParamCleanStreak: LevelBad, ParamOverdueAmount: LevelBad,
				ParamClosedDeals: LevelMedium,
			},
			values:   map[ParamKey]float64{ParamActiveDeals: 1, ParamClosedDeals: 1},
			watchers: 6,
		},
		{
			// Synthetic coverage: 7.40 and 10.00 UAH residues are excluded
			// from arrears; a closed card with a 15,000 UAH limit counts as
			// active; two MFO loans trigger mfo_pressure despite a good
			// inquiry level.
			file:  "report6.xml",
			why:   "the branches the real reports do not exercise",
			score: 80, band: BandHigh,
			flags: []Flag{FlagMFOPressure},
			levels: map[ParamKey]Level{
				ParamOverdueDeals: LevelGood, ParamOverdueAmount: LevelGood, ParamActiveDeals: LevelMedium,
				ParamInquiries: LevelGood,
			},
			values: map[ParamKey]float64{
				ParamOverdueDeals: 0, ParamOverdueAmount: 0, ParamActiveDeals: 5, ParamClosedDeals: 0,
				ParamCleanStreak: 1155, ParamInquiries: 3,
			},
			watchers: 3, watching: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			res := Rate(Input{Report: loadFixture(t, tt.file), AsOf: fixtureAsOf}, DefaultConfig())

			if res.Score != tt.score || res.Band != tt.band || res.CapReason != tt.capReason {
				t.Errorf("%d %s cap=%q, want %d %s cap=%q: %s",
					res.Score, res.Band, res.CapReason, tt.score, tt.band, tt.capReason, tt.why)
			}
			if len(res.Flags) != len(tt.flags) {
				t.Errorf("flags = %v, want %v", res.Flags, tt.flags)
			}
			for _, f := range tt.flags {
				if !hasFlag(res.Flags, f) {
					t.Errorf("flags = %v, missing %q", res.Flags, f)
				}
			}
			for key, want := range tt.levels {
				if p, _ := res.Parameter(key); p.Level != want {
					t.Errorf("%s: level %q (value %v), want %q", key, p.Level, p.Value, want)
				}
			}
			for key, want := range tt.values {
				if p, _ := res.Parameter(key); p.Value != want {
					t.Errorf("%s = %v, want %v", key, p.Value, want)
				}
			}
			watching := 0
			for _, m := range res.Monitoring {
				if m.Active {
					watching++
				}
			}
			if len(res.Monitoring) != tt.watchers || watching != tt.watching {
				t.Errorf("monitoring: %d entries, %d active; want %d and %d",
					len(res.Monitoring), watching, tt.watchers, tt.watching)
			}
		})
	}
}

// Check how report2 changes as the unresolved sale dated 2025-04-03 ages through the hard
// and soft cap windows.
func TestSoldDebtAgesThroughTwoCeilings(t *testing.T) {
	tests := []struct {
		asOf      string
		score     int
		capReason CapReason
		overdue   float64 // indicator 3
	}{
		{"2026-07-08", 43, "", 1},                  // 15 months: arrears; already below the ceiling
		{"2027-07-08", 45, CapCurrentOverdue, 1},   // 27 months: current-arrears cap applies
		{"2028-07-08", 74, CapTerminalDebtAged, 0}, // past 36 months: soft cap applies
		{"2031-07-08", 90, "", 0},                  // past 72 months
		{"2035-07-08", 90, "", 0},                  // past both cap windows; sale flag remains
	}
	for _, tt := range tests {
		t.Run(tt.asOf, func(t *testing.T) {
			asOf, _ := time.Parse("2006-01-02", tt.asOf)
			res := Rate(Input{Report: loadFixture(t, "report2.xml"), AsOf: asOf}, DefaultConfig())

			if res.Score != tt.score || res.CapReason != tt.capReason {
				t.Errorf("score %d cap=%q, want %d cap=%q", res.Score, res.CapReason, tt.score, tt.capReason)
			}
			value := func(k ParamKey) float64 { p, _ := res.Parameter(k); return p.Value }
			if got := value(ParamOverdueDeals); got != tt.overdue {
				t.Errorf("overdue deals = %v, want %v", got, tt.overdue)
			}
			// Ageing removes the caps but keeps the debt active and the sale
			// flag present.
			if value(ParamActiveDeals) != 1 || value(ParamClosedDeals) != 0 || !hasFlag(res.Flags, FlagSold) {
				t.Errorf("active=%v closed=%v flags=%v, want the sold debt still open, unrepaid and flagged",
					value(ParamActiveDeals), value(ParamClosedDeals), res.Flags)
			}
			if streak := value(ParamCleanStreak); (tt.overdue > 0) != (streak == 0) {
				t.Errorf("clean streak = %v with %v overdue deals", streak, tt.overdue)
			}
		})
	}
}

// Reference blocks must not change the score or the sum of indicator contributions.
func TestReferenceBlocksNeverMoveTheScore(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			res := Rate(Input{Report: loadFixture(t, name), AsOf: fixtureAsOf}, DefaultConfig())

			sum := 0.0
			for _, p := range res.Parameters {
				sum += p.Contribution
			}
			if math.Abs(sum-res.Raw) > 0.05 {
				t.Errorf("contributions add up to %.2f, raw is %.2f", sum, res.Raw)
			}
			if !res.Capped && res.Score != int(math.Round(res.Raw)) {
				t.Errorf("score %d, but raw %.1f rounds to %d with no ceiling", res.Score, res.Raw, int(math.Round(res.Raw)))
			}

			// Removing monitoring should only change the monitoring block.
			blind := loadFixture(t, name)
			for i := range blind.Sections {
				blind.Sections[i].Monitoring = nil
			}
			if got := Rate(Input{Report: blind, AsOf: fixtureAsOf}, DefaultConfig()); got.Score != res.Score || got.Raw != res.Raw {
				t.Errorf("removing monitoring moved the rating from %v to %v", res.Raw, got.Raw)
			}

			// Changing the MFO pressure threshold should only change the
			// flag.
			loud := DefaultConfig()
			loud.MFOPressureDeals = 0
			lit := Rate(Input{Report: loadFixture(t, name), AsOf: fixtureAsOf}, loud)
			if !hasFlag(lit.Flags, FlagMFOPressure) {
				t.Fatalf("flags = %v, want mfo_pressure lit", lit.Flags)
			}
			if lit.Score != res.Score || lit.Raw != res.Raw {
				t.Errorf("lighting mfo_pressure moved the rating from %v to %v", res.Raw, lit.Raw)
			}
		})
	}
}

// Lender inquiry counts must sum to indicator 9, excluding OWN refresh requests.
func TestLenderBreakdownAgreesWithIndicatorNine(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			res := Rate(Input{Report: loadFixture(t, name), AsOf: fixtureAsOf}, DefaultConfig())
			p, _ := res.Parameter(ParamInquiries)
			if p.Level != LevelUnknown && float64(res.MFO.Inquiries6m) != p.Value {
				t.Errorf("breakdown says %d, indicator 9 says %v", res.MFO.Inquiries6m, p.Value)
			}
			sum := 0
			for org, n := range res.MFO.InquiriesByOrg {
				if org == "OWN" {
					t.Error("our own refresh pulls leaked into the breakdown")
				}
				sum += n
			}
			if sum != res.MFO.Inquiries6m {
				t.Errorf("per-lender tally adds up to %d, the total is %d", sum, res.MFO.Inquiries6m)
			}
		})
	}
}

// Every fixture must return a complete, usable rating payload.
func TestFixturesProduceCoherentPayloads(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			res := Rate(Input{Report: loadFixture(t, name), AsOf: fixtureAsOf}, DefaultConfig())
			for _, p := range res.Parameters {
				switch {
				case p.Level == LevelUnknown && (p.Display != "" || p.Contribution != 0):
					t.Errorf("%s: unmeasured, yet shown or scored", p.Key)
				case p.Level != LevelUnknown && p.Display == "":
					t.Errorf("%s: measured, with nothing to show", p.Key)
				case p.Level == LevelGood && p.Advice != "":
					t.Errorf("%s: a green value carries advice", p.Key)
				}
			}
		})
	}
}
