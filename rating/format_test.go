package rating

import "testing"

func TestPluralPicksTheUkrainianForm(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "днів"}, {1, "день"}, {2, "дні"}, {4, "дні"}, {5, "днів"},
		{11, "днів"}, {12, "днів"}, {14, "днів"}, // 11–14 take "many" despite ending in 1–4
		{21, "день"}, {22, "дні"}, {25, "днів"}, {101, "день"}, {111, "днів"},
	}
	for _, tt := range tests {
		if got := plural(tt.n, "день", "дні", "днів"); got != tt.want {
			t.Errorf("plural(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestDaysDisplayMovesToMonthsAndYears(t *testing.T) {
	tests := []struct {
		days float64
		want string
	}{
		{0, "0 днів"},
		{1, "1 день"},
		{3, "3 дні"},
		{59, "59 днів"},
		{60, "2 місяці (60 днів)"},
		{218, "7 місяців (218 днів)"},
		{412, "13 місяців (412 днів)"},
		{729, "24 місяці (729 днів)"},
		{730, "2 роки (730 днів)"},
		{3418, "9 років (3418 днів)"},
	}
	for _, tt := range tests {
		if got := daysDisplay(tt.days); got != tt.want {
			t.Errorf("daysDisplay(%v) = %q, want %q", tt.days, got, tt.want)
		}
	}
}

func TestUAHDisplaySeparatesThousands(t *testing.T) {
	tests := []struct {
		amount float64
		want   string
	}{
		{0, "0 ₴"},
		{99, "99 ₴"},
		{1250, "1 250 ₴"},
		{12500.4, "12 500 ₴"},
		{1000000, "1 000 000 ₴"},
		{-500, "-500 ₴"},
	}
	for _, tt := range tests {
		if got := uahDisplay(tt.amount); got != tt.want {
			t.Errorf("uahDisplay(%v) = %q, want %q", tt.amount, got, tt.want)
		}
	}
}

func TestPercentDisplayRounds(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{0, "0%"}, {29.9, "30%"}, {42.4, "42%"}, {42.6, "43%"}, {140, "140%"},
	}
	for _, tt := range tests {
		if got := percentDisplay(tt.value); got != tt.want {
			t.Errorf("percentDisplay(%v) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestCountDisplayAgreesWithItsNoun(t *testing.T) {
	tests := []struct {
		n    float64
		want string
	}{
		{0, "0 кредитів"}, {1, "1 кредит"}, {2, "2 кредити"}, {5, "5 кредитів"},
	}
	for _, tt := range tests {
		if got := credits(tt.n); got != tt.want {
			t.Errorf("credits(%v) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
