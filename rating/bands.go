package rating

// Level describes an indicator value as good, medium, bad or unknown. It uses the value
// thresholds, independently of the scoring curve.
type Level string

const (
	LevelBad     Level = "bad"
	LevelMedium  Level = "medium"
	LevelGood    Level = "good"
	LevelUnknown Level = "unknown" // the indicator could not be measured
)

// bandRule assigns a level to a measured value.
type bandRule func(v float64) Level

// higherIsBetter uses inclusive upper bounds for the bad and medium levels.
func higherIsBetter(badMax, mediumMax float64) bandRule {
	return func(v float64) Level {
		switch {
		case v <= badMax:
			return LevelBad
		case v <= mediumMax:
			return LevelMedium
		default:
			return LevelGood
		}
	}
}

// lowerIsBetter uses an exclusive upper bound for good and an inclusive upper bound for
// medium.
func lowerIsBetter(goodBelow, mediumMax float64) bandRule {
	return func(v float64) Level {
		switch {
		case v < goodBelow:
			return LevelGood
		case v <= mediumMax:
			return LevelMedium
		default:
			return LevelBad
		}
	}
}

// Band is the overall rating category.
type Band string

const (
	BandLow    Band = "low"
	BandMedium Band = "medium"
	BandHigh   Band = "high"
)

// Default score caps match the upper bounds of the low and medium bands.
const (
	mediumBandFrom = 46
	highBandFrom   = 75
)

// bandOf returns the score category and its Ukrainian label.
func bandOf(score int) (Band, string) {
	switch {
	case score >= highBandFrom:
		return BandHigh, "Високий"
	case score >= mediumBandFrom:
		return BandMedium, "Середній"
	default:
		return BandLow, "Низький"
	}
}
