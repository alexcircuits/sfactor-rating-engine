package rating

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// plural selects the Ukrainian noun form. Numbers ending in 11–14 use many; otherwise the
// last digit determines the form.
func plural(n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	if r := n % 100; r >= 11 && r <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	default:
		return many
	}
}

// countOf formats a count with the matching noun form, such as "3 кредити".
func countOf(n int, one, few, many string) string {
	return strconv.Itoa(n) + " " + plural(n, one, few, many)
}

// countDisplay returns a formatter for the given noun forms.
func countDisplay(one, few, many string) func(float64) string {
	return func(v float64) string {
		return countOf(int(math.Round(v)), one, few, many)
	}
}

// daysDisplay uses days below 60, months below 730 and years after that. Month and year
// displays also include the exact day count.
func daysDisplay(v float64) string {
	n := int(math.Round(v))
	days := countOf(n, "день", "дні", "днів")
	switch {
	case n < 60:
		return days
	case n < 730:
		return countOf(n/30, "місяць", "місяці", "місяців") + " (" + days + ")"
	default:
		return countOf(n/365, "рік", "роки", "років") + " (" + days + ")"
	}
}

// uahDisplay rounds to whole hryvnias and groups thousands with spaces.
func uahDisplay(v float64) string {
	n := int64(math.Round(v))
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	digits := strconv.FormatInt(n, 10)

	var b strings.Builder
	for i := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteByte(digits[i])
	}
	return sign + b.String() + " ₴"
}

// percentDisplay rounds a percentage for display.
func percentDisplay(v float64) string { return fmt.Sprintf("%.0f%%", v) }
