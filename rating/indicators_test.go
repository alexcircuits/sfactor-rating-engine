package rating

import "testing"

func indicatorByKey(t *testing.T, k ParamKey) indicator {
	t.Helper()
	for _, in := range indicators {
		if in.key == k {
			return in
		}
	}
	t.Fatalf("no indicator %q", k)
	return indicator{}
}

func TestEveryIndicatorIsFullyDefined(t *testing.T) {
	if len(indicators) != 9 {
		t.Fatalf("%d indicators, want the nine of the agreed table", len(indicators))
	}
	total := 0
	for _, in := range indicators {
		if in.title == "" || in.hint == "" {
			t.Errorf("%s: missing title or hint", in.key)
		}
		if in.bands.Bad == "" || in.bands.Medium == "" || in.bands.Good == "" {
			t.Errorf("%s: incomplete band labels", in.key)
		}
		if in.format == nil || in.level == nil || len(in.curve) == 0 {
			t.Errorf("%s: missing format, band rule or curve", in.key)
		}
		if in.weight <= 0 {
			t.Errorf("%s: weight %d", in.key, in.weight)
		}
		total += in.weight
		if in.advice.forLevel(LevelBad) == "" {
			t.Errorf("%s: a red value gets no advice", in.key)
		}
		if in.advice.forLevel(LevelGood) != "" || in.advice.forLevel(LevelUnknown) != "" {
			t.Errorf("%s: advice for a green or unmeasured value is noise", in.key)
		}
	}
	if total != 100 {
		t.Errorf("weights add up to %d, want 100", total)
	}
}

// Check each band boundary against the model specification.
func TestLevelBoundariesMatchTheAgreedTable(t *testing.T) {
	tests := []struct {
		key   ParamKey
		value float64
		want  Level
	}{
		// 1. Днів з моменту оформлення першого кредиту: 0–180 / 181–360 / >360
		{ParamHistoryAge, 0, LevelBad},
		{ParamHistoryAge, 180, LevelBad},
		{ParamHistoryAge, 181, LevelMedium},
		{ParamHistoryAge, 360, LevelMedium},
		{ParamHistoryAge, 361, LevelGood},
		// 2. Днів поспіль без прострочень: 0–90 / 91–180 / >180
		{ParamCleanStreak, 0, LevelBad},
		{ParamCleanStreak, 90, LevelBad},
		{ParamCleanStreak, 91, LevelMedium},
		{ParamCleanStreak, 180, LevelMedium},
		{ParamCleanStreak, 181, LevelGood},
		// 3. Поточних прострочених кредитів: >1 / 1 / 0
		{ParamOverdueDeals, 0, LevelGood},
		{ParamOverdueDeals, 1, LevelMedium},
		{ParamOverdueDeals, 2, LevelBad},
		// 4. Сума прострочення: >1000 / 100–1000 / <100
		{ParamOverdueAmount, 0, LevelGood},
		{ParamOverdueAmount, 99.99, LevelGood},
		{ParamOverdueAmount, 100, LevelMedium},
		{ParamOverdueAmount, 1000, LevelMedium},
		{ParamOverdueAmount, 1000.01, LevelBad},
		// 5. Діючих кредитів: >5 / 3–5 / <3
		{ParamActiveDeals, 2, LevelGood},
		{ParamActiveDeals, 3, LevelMedium},
		{ParamActiveDeals, 5, LevelMedium},
		{ParamActiveDeals, 6, LevelBad},
		// 6. Погашених кредитів: 0 / 1–3 / >3
		{ParamClosedDeals, 0, LevelBad},
		{ParamClosedDeals, 1, LevelMedium},
		{ParamClosedDeals, 3, LevelMedium},
		{ParamClosedDeals, 4, LevelGood},
		// 7. Нових кредитів за 6 місяців: >5 / 3–5 / <3
		{ParamNewDeals, 2, LevelGood},
		{ParamNewDeals, 3, LevelMedium},
		{ParamNewDeals, 5, LevelMedium},
		{ParamNewDeals, 6, LevelBad},
		// 8. Кредитне навантаження: >60% / 30–60% / <30%
		{ParamDebtLoad, 29.9, LevelGood},
		{ParamDebtLoad, 30, LevelMedium},
		{ParamDebtLoad, 60, LevelMedium},
		{ParamDebtLoad, 60.1, LevelBad},
		// 9. Звернень за кредитами: >15 / 5–15 / <5
		{ParamInquiries, 4, LevelGood},
		{ParamInquiries, 5, LevelMedium},
		{ParamInquiries, 15, LevelMedium},
		{ParamInquiries, 16, LevelBad},
	}
	for _, tt := range tests {
		if got := indicatorByKey(t, tt.key).level(tt.value); got != tt.want {
			t.Errorf("%s(%v) = %q, want %q", tt.key, tt.value, got, tt.want)
		}
	}
}

func TestEveryCurveHitsItsAnchors(t *testing.T) {
	for _, in := range indicators {
		for _, a := range in.curve {
			if got := in.curve.eval(a.value); got != a.points {
				t.Errorf("%s: eval(%v) = %v, want the anchor's %v", in.key, a.value, got, a.points)
			}
		}
	}
}

func TestEveryCurveIsOrderedAndInRange(t *testing.T) {
	for _, in := range indicators {
		for i, a := range in.curve {
			if a.points < 0 || a.points > 100 {
				t.Errorf("%s: anchor %v scores %v, outside 0–100", in.key, a.value, a.points)
			}
			if i > 0 && a.value <= in.curve[i-1].value {
				t.Errorf("%s: anchor %d (%v) does not follow anchor %d (%v)",
					in.key, i, a.value, i-1, in.curve[i-1].value)
			}
		}
	}
}

// Improving an indicator value must never reduce its points.
func TestEveryCurveIsMonotone(t *testing.T) {
	for _, in := range indicators {
		first, last := in.curve[0], in.curve[len(in.curve)-1]
		rising := last.points > first.points
		step := (last.value - first.value) / 500

		prev := in.curve.eval(first.value)
		for v := first.value; v <= last.value; v += step {
			cur := in.curve.eval(v)
			if (rising && cur < prev) || (!rising && cur > prev) {
				t.Fatalf("%s: curve turns back at %v (%v → %v)", in.key, v, prev, cur)
			}
			prev = cur
		}
	}
}

func TestAdviceFallsBackToTheRedSentence(t *testing.T) {
	a := advice{bad: "red"}
	if got := a.forLevel(LevelMedium); got != "red" {
		t.Errorf("medium advice = %q, want the red sentence", got)
	}
	a.medium = "amber"
	if got := a.forLevel(LevelMedium); got != "amber" {
		t.Errorf("medium advice = %q, want its own sentence", got)
	}
}

func TestBandOfTheFinalScore(t *testing.T) {
	tests := []struct {
		score int
		want  Band
	}{
		{0, BandLow}, {45, BandLow}, {46, BandMedium}, {74, BandMedium}, {75, BandHigh}, {100, BandHigh},
	}
	for _, tt := range tests {
		if got, _ := bandOf(tt.score); got != tt.want {
			t.Errorf("bandOf(%d) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

// Each default cap should be the highest score in its band.
func TestCeilingsSitOnTheTopOfTheirBands(t *testing.T) {
	cfg := DefaultConfig()
	for _, tt := range []struct {
		ceiling int
		band    Band
	}{
		{cfg.CapScore, BandLow},
		{cfg.CapTerminalSoftScore, BandMedium},
	} {
		if got, _ := bandOf(tt.ceiling); got != tt.band {
			t.Errorf("ceiling %d is %q, want %q", tt.ceiling, got, tt.band)
		}
		if got, _ := bandOf(tt.ceiling + 1); got == tt.band {
			t.Errorf("ceiling %d is not the top of %q", tt.ceiling, tt.band)
		}
	}
}
