package ubki

import (
	"encoding/xml"
	"testing"
	"time"
)

type attrs struct {
	I Int   `xml:"i,attr"`
	M Money `xml:"m,attr"`
	D Date  `xml:"d,attr"`
}

func decodeAttrs(t *testing.T, doc string) attrs {
	t.Helper()
	var a attrs
	if err := xml.Unmarshal([]byte(doc), &a); err != nil {
		t.Fatalf("an unreadable attribute must not fail the document: %v", err)
	}
	return a
}

func TestUnreadableAttributesReadAsZeroOrUnknown(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{"empty", `<x i="" m="" d=""/>`},
		{"garbage", `<x i="abc" m="x.y" d="not-a-date"/>`},
		{"placeholder date", `<x d="1900-01-01"/>`},
		{"zero date", `<x d="0001-01-01"/>`},
		// Reject non-finite values even when strconv.ParseFloat accepts them.
		{"NaN", `<x m="NaN"/>`},
		{"infinity", `<x m="Inf"/>`},
		{"negative infinity", `<x m="-Infinity"/>`},
		{"beyond any real amount", `<x m="1e300"/>`},
		// Negative report amounts should become zero.
		{"a negative amount", `<x m="-500"/>`},
		{"fractional count", `<x i="12.5"/>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := decodeAttrs(t, tt.doc)
			if a.I != 0 {
				t.Errorf("Int = %d, want 0", a.I)
			}
			if a.M != 0 {
				t.Errorf("Money = %d, want 0", a.M)
			}
			if _, ok := a.D.Time(); ok {
				t.Errorf("Date = %v, want unknown", a.D)
			}
		})
	}
}

func TestReadableAttributes(t *testing.T) {
	a := decodeAttrs(t, `<x i=" 42 " m="1034.62" d="2026-07-08"/>`)

	if a.I != 42 {
		t.Errorf("Int = %d, want 42", a.I)
	}
	if a.M != 103462 {
		t.Errorf("Money = %d kopiykas, want 103462", a.M)
	}
	got, ok := a.D.Time()
	if !ok || !got.Equal(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v (known %v), want 2026-07-08", got, ok)
	}
}

func TestParseMoneyIsExactToTheKopiyka(t *testing.T) {
	tests := []struct {
		in   string
		want Money
	}{
		{"0", 0},
		{"0.02", 2},
		{"7.40", 740},
		{"10.00", 1000},
		{"64.07", 6407},
		{"35.91", 3591},
		{"1034.62", 103462},
		{"25000", 2500000},
		{"-500", -50000},
		{" 12.5 ", 1250},
		{"24999.999999999996", 2500000}, // float noise from a JSON client rounds away
	}
	for _, tt := range tests {
		got, err := ParseMoney(tt.in)
		if err != nil {
			t.Errorf("ParseMoney(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseMoney(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestParseMoneyRejectsWhatIsNotAnAmount(t *testing.T) {
	for _, in := range []string{"", "   ", "abc", "1,5", "NaN", "Inf", "-Inf", "1e13"} {
		if got, err := ParseMoney(in); err == nil {
			t.Errorf("ParseMoney(%q) = %d, want an error", in, got)
		}
	}
}

// Kopiyka arithmetic must preserve an exact sum of 100 UAH at the band boundary.
func TestMoneySumsExactly(t *testing.T) {
	var sum Money
	for _, s := range []string{"10.14", "58.12", "31.74"} {
		m, err := ParseMoney(s)
		if err != nil {
			t.Fatal(err)
		}
		sum += m
	}
	if sum != 100*Hryvnia {
		t.Errorf("sum = %d kopiykas, want exactly %d", sum, 100*Hryvnia)
	}
	if sum.Hryvnias() != 100 {
		t.Errorf("Hryvnias() = %v, want 100", sum.Hryvnias())
	}
}

func TestDateString(t *testing.T) {
	if got := NewDate(time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)).String(); got != "2026-07-08" {
		t.Errorf("String() = %q, want 2026-07-08", got)
	}
	if got := (Date{}).String(); got != "" {
		t.Errorf("unknown date String() = %q, want empty", got)
	}
}
