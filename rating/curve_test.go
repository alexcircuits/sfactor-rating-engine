package rating

import "testing"

func TestCurveInterpolatesLinearly(t *testing.T) {
	c := curve{{0, 0}, {100, 50}, {200, 100}}

	tests := []struct {
		value, want float64
	}{
		{0, 0}, {25, 12.5}, {50, 25}, {75, 37.5}, {100, 50}, {150, 75}, {200, 100},
	}
	for _, tt := range tests {
		if got := c.eval(tt.value); got != tt.want {
			t.Errorf("eval(%v) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestCurveIsFlatBeyondItsEnds(t *testing.T) {
	c := curve{{10, 20}, {100, 80}}

	if got := c.eval(-500); got != 20 {
		t.Errorf("eval(-500) = %v, want the first anchor's 20", got)
	}
	if got := c.eval(100000); got != 80 {
		t.Errorf("eval(100000) = %v, want the last anchor's 80", got)
	}
}

func TestEmptyCurveScoresZero(t *testing.T) {
	var c curve
	if got := c.eval(42); got != 0 {
		t.Errorf("eval(42) = %v, want 0", got)
	}
}
