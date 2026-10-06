package rating

// anchor pairs a measured value with its score.
type anchor struct {
	value  float64
	points float64
}

// curve interpolates between anchors ordered by value. Values outside the anchor range
// use the nearest endpoint score.
type curve []anchor

// eval returns the score for a measured value.
func (c curve) eval(v float64) float64 {
	if len(c) == 0 {
		return 0
	}
	if v <= c[0].value {
		return c[0].points
	}
	for i := 1; i < len(c); i++ {
		if lo, hi := c[i-1], c[i]; v <= hi.value {
			return lo.points + (v-lo.value)/(hi.value-lo.value)*(hi.points-lo.points)
		}
	}
	return c[len(c)-1].points
}
