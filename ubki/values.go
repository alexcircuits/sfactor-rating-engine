package ubki

import (
	"encoding/xml"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

// Int parses an integer attribute, using zero for empty or invalid input.
type Int int

// UnmarshalXMLAttr implements xml.UnmarshalerAttr.
func (i *Int) UnmarshalXMLAttr(attr xml.Attr) error {
	v, err := strconv.Atoi(strings.TrimSpace(attr.Value))
	if err != nil {
		v = 0
	}
	*i = Int(v)
	return nil
}

// Money stores whole kopiykas to keep sums and threshold comparisons exact.
type Money int64

// Hryvnia is the number of kopiykas in one hryvnia.
const Hryvnia Money = 100

// maxHryvnias limits parsed amounts before conversion to kopiykas.
const maxHryvnias = 1e12

var errNotAnAmount = errors.New("ubki: not an amount of hryvnias")

// ParseMoney converts decimal hryvnias to the nearest kopiyka. It rejects invalid input,
// NaN, infinity and magnitudes above maxHryvnias.
func ParseMoney(s string) (Money, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.Abs(v) > maxHryvnias {
		return 0, errNotAnAmount
	}
	return Money(math.Round(v * 100)), nil
}

// Hryvnias returns the amount in hryvnias.
func (m Money) Hryvnias() float64 { return float64(m) / 100 }

// UnmarshalXMLAttr parses a monetary attribute. Invalid or negative amounts become zero.
func (m *Money) UnmarshalXMLAttr(attr xml.Attr) error {
	v, err := ParseMoney(attr.Value)
	if err != nil || v < 0 {
		v = 0
	}
	*m = v
	return nil
}

// dateLayout is the format of УБКІ date attributes.
const dateLayout = "2006-01-02"

// placeholderDate represents an unknown date in bureau reports.
var placeholderDate = time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)

// Date is a report date. Its zero value means empty, invalid or placeholder input.
type Date struct {
	t time.Time
}

// NewDate wraps t as a report date. A zero time remains unknown.
func NewDate(t time.Time) Date { return Date{t: t} }

// Time returns the date and whether it is known.
func (d Date) Time() (time.Time, bool) { return d.t, !d.t.IsZero() }

// String returns YYYY-MM-DD, or an empty string for an unknown date.
func (d Date) String() string {
	if d.t.IsZero() {
		return ""
	}
	return d.t.Format(dateLayout)
}

// UnmarshalXMLAttr implements xml.UnmarshalerAttr.
func (d *Date) UnmarshalXMLAttr(attr xml.Attr) error {
	t, err := time.Parse(dateLayout, strings.TrimSpace(attr.Value))
	if err != nil || t.Equal(placeholderDate) {
		t = time.Time{}
	}
	*d = Date{t: t}
	return nil
}
