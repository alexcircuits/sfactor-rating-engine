package ubki

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The report fixtures are shared with the rating tests.
func load(t *testing.T, name string) *Report {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "rating", "testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	r, err := Parse(data)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return r
}

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func maxDaysOverdue(deals []Deal) Int {
	var most Int
	for _, d := range deals {
		for _, s := range d.History {
			most = max(most, s.DaysOverdue)
		}
	}
	return most
}

func hasStatus(deals []Deal, status DealStatus) bool {
	for _, d := range deals {
		for _, s := range d.History {
			if s.Status == status {
				return true
			}
		}
	}
	return false
}

// report1 contains a guarantee, an overdraft with increasing arrears and a written-off
// consumer loan.
func TestParseReport1(t *testing.T) {
	r := load(t, "report1.xml")

	deals := r.Deals()
	if len(deals) != 3 {
		t.Fatalf("deals = %d, want 3", len(deals))
	}
	if deals[0].Role != RoleGuarantor {
		t.Errorf("deal 0 role = %q, want the guarantee", deals[0].Role)
	}
	if got := maxDaysOverdue(deals); got != 90 {
		t.Errorf("most days past due = %d, want 90", got)
	}
	if !hasStatus(deals, StatusWrittenOff) {
		t.Error("expected the written-off consumer loan")
	}
}

// report4 has two employment records for the declared-income fallback.
func TestParseReport4DeclaredEmployment(t *testing.T) {
	jobs := load(t, "report4.xml").DeclaredEmployment()
	if len(jobs) != 2 {
		t.Fatalf("declared jobs = %d, want 2", len(jobs))
	}
	for _, job := range jobs {
		if job.MonthlyIncome != 4000*Hryvnia || job.VerifiedOn.String() != "2026-01-15" {
			t.Errorf("job = %d kopiykas verified %s, want 4000 ₴ verified 2026-01-15",
				job.MonthlyIncome, job.VerifiedOn)
		}
	}
}

// report2 contains a restructured loan sold after 134 overdue days, plus an open
// enforcement proceeding.
func TestParseReport2(t *testing.T) {
	r := load(t, "report2.xml")

	if got := maxDaysOverdue(r.Deals()); got != 134 {
		t.Errorf("most days past due = %d, want 134", got)
	}
	if !hasStatus(r.Deals(), StatusSold) || !hasStatus(r.Deals(), StatusRestructured) {
		t.Error("expected the restructured deal that was sold")
	}
	if got := r.ActiveEnforcements(); got != 1 {
		t.Errorf("active enforcements = %d, want 1", got)
	}
}

func TestParseMonitoring(t *testing.T) {
	subs := load(t, "report1.xml").Monitoring()
	if len(subs) != 11 {
		t.Fatalf("subscriptions = %d, want 11", len(subs))
	}
	first := subs[0]
	if first.Subscriber != CreditorBank || first.Start.String() != "2022-11-23" || first.End.String() != "2024-10-11" {
		t.Errorf("first subscription = %s %s–%s, want BNK 2022-11-23–2024-10-11",
			first.Subscriber, first.Start, first.End)
	}
}

func TestSectionsMayBeMissing(t *testing.T) {
	for _, r := range []*Report{nil, {}} {
		if r.Deals() != nil || r.Monitoring() != nil || r.DeclaredEmployment() != nil {
			t.Error("a missing section must read as empty")
		}
		if r.ActiveEnforcements() != 0 || r.MonthlyObligations() != 0 {
			t.Error("a missing section must read as zero")
		}
		if _, ok := r.Inquiries(); ok {
			t.Error("a missing inquiry registry must read as unknown")
		}
		if _, ok := r.BuiltAt(); ok {
			t.Error("a report with no trace and no inquiries has no build time")
		}
	}
}

// An empty inquiry section is known data, with zero requests.
func TestEmptyInquiryRegistryIsKnown(t *testing.T) {
	r := &Report{Sections: []Section{{ID: SectionInquiries}}}
	if inquiries, ok := r.Inquiries(); !ok || len(inquiries) != 0 {
		t.Errorf("Inquiries() = %v, %v; want none, known", inquiries, ok)
	}
}

func TestParseRejectsBrokenXML(t *testing.T) {
	if _, err := Parse([]byte(`<ubkidata><comp`)); err == nil {
		t.Error("expected an error for broken XML")
	}
	if _, err := Parse([]byte(`<report/>`)); err == nil {
		t.Error("expected an error for a document that is not <ubkidata>")
	}
}

func TestBuiltAt(t *testing.T) {
	tests := []struct {
		name   string
		report Report
		want   time.Time
	}{
		{
			name: "the build step, whatever order the steps are in",
			report: Report{Trace: []TraceStep{
				{Name: "load data", Finished: "2026-07-01 08:00:00.123"},
				{Name: "build report", Finished: "2026-07-08 13:16:40.356"},
			}},
			want: day("2026-07-08").Add(13*time.Hour + 16*time.Minute + 40*time.Second + 356*time.Millisecond),
		},
		{
			name:   "any readable step when there is no build step",
			report: Report{Trace: []TraceStep{{Name: "load data", Finished: "2026-07-01 08:00:00"}}},
			want:   day("2026-07-01").Add(8 * time.Hour),
		},
		{
			name: "the report's own inquiry when there is no trace",
			report: Report{Sections: []Section{{ID: SectionInquiries, Inquiries: []Inquiry{
				{Requester: CreditorBank, Date: NewDate(day("2026-06-01"))},
				{Requester: CreditorOwn, Date: NewDate(day("2026-07-02"))},
			}}}},
			want: day("2026-07-02"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.report.BuiltAt()
			if !ok || !got.Equal(tt.want) {
				t.Errorf("BuiltAt() = %v, %v; want %v", got, ok, tt.want)
			}
		})
	}
}

func TestLatest(t *testing.T) {
	dated := func(status DealStatus, on string) Snapshot {
		return Snapshot{Status: status, ReportedOn: NewDate(day(on))}
	}
	undated := func(status DealStatus) Snapshot { return Snapshot{Status: status} }

	tests := []struct {
		name    string
		history []Snapshot
		want    DealStatus
	}{
		{"by date, not by file order",
			[]Snapshot{dated("a", "2025-03-01"), dated("b", "2025-05-01"), dated("c", "2025-04-01")}, "b"},
		{"an undated snapshot after the newest one",
			[]Snapshot{dated("a", "2025-05-01"), undated("b")}, "b"},
		{"a dated snapshot never loses to an earlier-dated one after an undated one",
			[]Snapshot{dated("a", "2025-05-01"), undated("b"), dated("c", "2025-03-01")}, "b"},
		{"the later entry of two with the same date",
			[]Snapshot{dated("a", "2025-05-01"), dated("b", "2025-05-01")}, "b"},
		{"no snapshots", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Deal{History: tt.history}).Latest().Status; got != tt.want {
				t.Errorf("Latest() = %q, want %q", got, tt.want)
			}
		})
	}
}
