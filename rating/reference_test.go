package rating

import (
	"reflect"
	"testing"

	"github.com/sfactor/scoring/ubki"
)

func openLoan(lender string) ubki.Deal {
	return lenderDeal(lender, snap(daysAgo(10), daysAgo(500), ubki.StatusOpen, 0, 0, uah(500)))
}

func closedLoan(lender string) ubki.Deal {
	return lenderDeal(lender, snap(daysAgo(10), daysAgo(500), ubki.StatusClosed, 0, 0, 0))
}

func TestFlags(t *testing.T) {
	start := daysAgo(900)
	tests := []struct {
		name     string
		sections []ubki.Section
		want     Flag
	}{
		{
			name: "a written-off debt",
			sections: []ubki.Section{dealsSection(loan(
				snap(daysAgo(300), start, ubki.StatusOpen, 90, uah(5000), 0),
				snap(daysAgo(200), start, ubki.StatusWrittenOff, 0, 0, 0),
			))},
			want: FlagWriteOff,
		},
		{
			name: "a debt sold on, whatever happened to it since",
			sections: []ubki.Section{dealsSection(loan(
				snap(daysAgo(300), start, ubki.StatusSold, 0, 0, 0),
				snap(daysAgo(200), start, ubki.StatusClosed, 0, 0, 0),
			))},
			want: FlagSold,
		},
		{
			name: "an open enforcement proceeding",
			sections: []ubki.Section{
				dealsSection(openLoan(ubki.CreditorBank)),
				{ID: ubki.SectionEnforcement, Enforcement: &ubki.Enforcement{Active: 2}},
			},
			want: FlagEnforcement,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if res := rate(reportOf(tt.sections...), 0); !hasFlag(res.Flags, tt.want) {
				t.Errorf("flags = %v, want %q", res.Flags, tt.want)
			}
		})
	}
}

func TestMFOPressure(t *testing.T) {
	applications := func(n int) ubki.Section {
		qs := make([]ubki.Inquiry, n)
		for i := range qs {
			qs[i] = application(ubki.CreditorMFO, 1+i)
		}
		return inquiriesSection(qs...)
	}
	mfoLoans := func(n int) []ubki.Deal {
		deals := make([]ubki.Deal, n)
		for i := range deals {
			deals[i] = openLoan(ubki.CreditorMFO)
		}
		return deals
	}
	bankLoan := []ubki.Deal{openLoan(ubki.CreditorBank)}

	tests := []struct {
		name         string
		deals        []ubki.Deal
		applications int
		want         bool
	}{
		// The inquiry-based flag must use the same boundary as indicator 9.
		{"sixteen applications", bankLoan, 16, true},
		{"exactly fifteen applications", bankLoan, 15, false},
		{"two live microfinance loans", mfoLoans(2), 3, true},
		{"one live microfinance loan", mfoLoans(1), 3, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := rate(reportOf(dealsSection(tt.deals...), applications(tt.applications)), 0)
			if got := hasFlag(res.Flags, FlagMFOPressure); got != tt.want {
				t.Errorf("flags = %v; mfo_pressure %v, want %v", res.Flags, got, tt.want)
			}
		})
	}
}

func TestLenderBreakdown(t *testing.T) {
	res := rate(reportOf(
		dealsSection(openLoan("BNK"), closedLoan("BNK"), openLoan("MFO"), openLoan("MFO"), closedLoan("FIN")),
		inquiriesSection(
			application("MFO", 10),
			application("MFO", 20),
			application("BNK", 30),
			application("OWN", 40), // exclude OWN refresh requests
		),
	), 0)

	want := &MFOProfile{
		DealsByDonor:   map[string]int{"BNK": 2, "MFO": 2, "FIN": 1},
		ActiveByDonor:  map[string]int{"BNK": 1, "MFO": 2},
		InquiriesByOrg: map[string]int{"MFO": 2, "BNK": 1},
		ActiveMFODeals: 2,
		Inquiries6m:    3,
	}
	if !reflect.DeepEqual(res.MFO, want) {
		t.Errorf("breakdown = %+v, want %+v", res.MFO, want)
	}
	if p, _ := res.Parameter(ParamInquiries); p.Value != float64(want.Inquiries6m) {
		t.Errorf("indicator 9 says %v, the breakdown %d: one number, two stories", p.Value, want.Inquiries6m)
	}
}

func subscription(subscriber string, startAgo, endAgo int) ubki.Subscription {
	return ubki.Subscription{
		Subscriber: subscriber,
		Start:      ubki.NewDate(daysAgo(startAgo)),
		End:        ubki.NewDate(daysAgo(endAgo)),
	}
}

func TestMonitoringIsListedNewestFirstAndLabelled(t *testing.T) {
	res := rate(reportOf(
		dealsSection(openLoan(ubki.CreditorBank)),
		monitoringSection(
			subscription("BCH", 400, 200),
			subscription("BNK", 100, -30), // ends a month from now
			subscription("XYZ", 700, 500),
			ubki.Subscription{Subscriber: "FIN"}, // no dates on file
			subscription("MFO", 250, 1),          // ended yesterday
		),
	), 0)

	want := []struct {
		org, label string
		active     bool
	}{
		{"BNK", "Банк", true},
		{"MFO", "МФО", false},
		{"BCH", "Бюро", false},
		{"XYZ", "XYZ", false}, // an unknown code is shown as itself
		{"FIN", "Фінкомпанія", false},
	}
	if len(res.Monitoring) != len(want) {
		t.Fatalf("monitoring = %d entries, want all %d: the app decides how many to show", len(res.Monitoring), len(want))
	}
	for i, w := range want {
		got := res.Monitoring[i]
		if got.Org != w.org || got.OrgLabel != w.label || got.Active != w.active {
			t.Errorf("entry %d = %s %q active=%v, want %s %q active=%v",
				i, got.Org, got.OrgLabel, got.Active, w.org, w.label, w.active)
		}
	}
	if got := res.Monitoring[0].StartDate; got != daysAgo(100).Format("2006-01-02") {
		t.Errorf("start_date = %q, want %s", got, daysAgo(100).Format("2006-01-02"))
	}
	if last := res.Monitoring[4]; last.StartDate != "" || last.EndDate != "" {
		t.Errorf("unknown dates = %q–%q, want empty", last.StartDate, last.EndDate)
	}
}

// A subscription remains active on its end date.
func TestMonitoringRunsThroughItsLastDay(t *testing.T) {
	tests := []struct {
		name   string
		endAgo int
		want   bool
	}{
		{"ends today", 0, true},
		{"ended yesterday", 1, false},
		{"ends next week", -7, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := rate(reportOf(
				dealsSection(openLoan(ubki.CreditorBank)),
				monitoringSection(subscription("BNK", 300, tt.endAgo)),
			), 0)
			if got := res.Monitoring[0].Active; got != tt.want {
				t.Errorf("active = %v, want %v", got, tt.want)
			}
		})
	}
}

// Implicit and explicit build dates must give the same subscription status, regardless of
// the time in the build timestamp.
func TestReportsOwnDateIsACalendarDay(t *testing.T) {
	r := reportOf(
		dealsSection(openLoan(ubki.CreditorBank)),
		monitoringSection(subscription("BNK", 300, 0)),
	)
	r.Trace = []ubki.TraceStep{{Name: "build report", Finished: testAsOf.Format("2006-01-02") + " 13:16:40.356"}}

	res := Rate(Input{Report: r}, DefaultConfig())

	if res.AsOf != testAsOf.Format("2006-01-02") {
		t.Fatalf("as_of = %s, want the build date %s", res.AsOf, testAsOf.Format("2006-01-02"))
	}
	if !res.Monitoring[0].Active {
		t.Error("a subscription ending on the build date reads as finished on that date")
	}
}

func TestNoMonitoringSectionMeansNoBlock(t *testing.T) {
	if res := rate(reportOf(dealsSection(openLoan(ubki.CreditorBank))), 0); res.Monitoring != nil {
		t.Errorf("monitoring = %v, want none", res.Monitoring)
	}
}
