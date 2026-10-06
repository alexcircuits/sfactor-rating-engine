# Scoring engine cleanup Implementation Plan

**Goal:** Restructure the Go engine in `scoring/` so a rating can be followed from УБКІ XML to JSON top to bottom, and make `POST /rating` take the XML file plus the borrower's data as `multipart/form-data` and return the app's `Rating` object unchanged.

**Architecture:** Three layers, as today: `ubki` (the part of the report the rating reads, parsed leniently, money in kopiykas), `rating` (a pipeline of functions: classify deals → measure → score → ceiling → reference blocks), and `internal/httpapi` behind a thin `cmd/server`. Today's output is pinned by golden payloads in the first commit; every later commit must reproduce them byte for byte. The new `ubki` is built beside the old one (`scoring/ubkinext`) so every commit compiles.

**Tech Stack:** Go 1.26, standard library only (`encoding/xml`, `net/http`, `mime/multipart`, `log/slog`); `golangci-lint`.

**Spec:** `docs/specs/2026-09-28-scoring-engine-cleanup-design.md`. **Flokzy:** ENG-687.

**Working directory:** the worktree `sfactorapp-worktrees/scoring-engine`, branch `refactor/scoring-engine`. Go commands run in `scoring/`; git commands in the worktree root. Commit with an explicit pathspec.

---

## File structure

| Path | Responsibility |
|---|---|
| `scoring/ubki/doc.go` | What the report is; the leniency policy |
| `scoring/ubki/codes.go` | УБКІ code vocabulary: deal status, role, purpose, creditor type, inquiry reason, report type |
| `scoring/ubki/values.go` | Lenient attribute types: `Int`, `Money` (kopiykas, `ParseMoney`), `Date` |
| `scoring/ubki/report.go` | `Report`, its sections, `Parse`, one accessor per section read, `BuiltAt`, `Deal.Latest` |
| `scoring/rating/doc.go` | What the rating is and is not; the pipeline in one paragraph |
| `scoring/rating/rate.go` | `Input`, `Rate`: the pipeline; the reference date; the unavailable result |
| `scoring/rating/indicators.go` | The nine indicators, one table entry each (bands, curve, weight, advice); `scoreIndicators` |
| `scoring/rating/deals.go` | Which deals count and as what: `standing`, terminal events, recovered debt, the arrears predicate |
| `scoring/rating/measure.go` | The nine values: portfolio tally, history, clean streak, income, debt load, applications |
| `scoring/rating/ceilings.go` | The three ceilings |
| `scoring/rating/reference.go` | Flags, lender breakdown, monitoring: shown, never scored |
| `scoring/rating/result.go` | The JSON contract types |
| `scoring/rating/config.go` | Thresholds and windows |
| `scoring/rating/bands.go`, `curve.go`, `format.go` | Colour rules and total bands; scoring curves; Ukrainian number formatting |
| `scoring/rating/testdata/golden/*.json` | The pinned payloads |
| `scoring/internal/httpapi/httpapi.go` | `POST /rating` (multipart), `GET /health`, CORS |
| `scoring/cmd/server/main.go` | Flags, logging, listen, graceful shutdown |
| `scoring/cmd/score/main.go` | CLI: JSON or a table |
| `scoring/api/openapi.yaml` | The HTTP contract |
| `scoring/README.md` | What it is, how to run and call it, the model |

Deleted: `scoring/ubki/{attr,parse,types}.go` and their tests; `scoring/rating/{advice,features,levels,params}.go`; `scoring/cmd/server/handler{,_test}.go`.

---

### Task 1: Pin today's output with golden payloads

**Files:**
- Create: `scoring/rating/golden_test.go`
- Create (generated): `scoring/rating/testdata/golden/*.json` (10 files)

- [ ] **Step 1: Write the test, against today's API**

Create `scoring/rating/golden_test.go`:

```go
package rating

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/golden from the current output")

// goldenCase is one pinned rating: a fixture, a reference date and the
// verified income supplied with it, in whole hryvnias.
type goldenCase struct {
	name   string
	report string
	asOf   string
	income int
}

// goldenCases pin the whole payload, byte for byte, so a refactor cannot move
// any number the app renders without the diff showing it.
var goldenCases = []goldenCase{
	{name: "report1", report: "report1.xml", asOf: "2026-07-08"},
	{name: "report2", report: "report2.xml", asOf: "2026-07-08"},
	{name: "report3", report: "report3.xml", asOf: "2026-07-08"},
	{name: "report4", report: "report4.xml", asOf: "2026-07-08"},
	{name: "report5", report: "report5.xml", asOf: "2026-07-08"},
	{name: "report6", report: "report6.xml", asOf: "2026-07-08"},
	// The source of the app's DEMO_RATING_CLEAN (src/lib/ratingDemo.ts).
	{name: "report3_income_24000", report: "report3.xml", asOf: "2026-07-08", income: 24000},
	// The sold debt in report2, ageing through both terminal ceilings.
	{name: "report2_2027-07-08", report: "report2.xml", asOf: "2027-07-08"},
	{name: "report2_2028-07-08", report: "report2.xml", asOf: "2028-07-08"},
	{name: "report2_2031-07-08", report: "report2.xml", asOf: "2031-07-08"},
}

func TestGoldenPayloads(t *testing.T) {
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalGolden(t, rateGolden(t, tc))
			path := filepath.Join("testdata", "golden", tc.name+".json")

			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden payload: %v (run go test ./rating -update to create it)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("payload differs from %s.\nIf the change is intended, run go test ./rating -update and review the diff.\ngot:\n%s", path, got)
			}
		})
	}
}

// A report rated on its own build date must read exactly as when that date is
// passed explicitly: the reference date is a calendar date, not a timestamp.
func TestReportDateDefaultsToTheBuildDate(t *testing.T) {
	for _, tc := range goldenCases[:6] {
		t.Run(tc.name, func(t *testing.T) {
			explicit := marshalGolden(t, rateGolden(t, tc))
			implicit := marshalGolden(t, rateGolden(t, goldenCase{report: tc.report}))
			if !bytes.Equal(explicit, implicit) {
				t.Errorf("rating on the report's own date differs from rating with as_of=%s", tc.asOf)
			}
		})
	}
}

func rateGolden(t *testing.T, tc goldenCase) Result {
	t.Helper()
	cfg := DefaultConfig()
	if tc.asOf != "" {
		asOf, err := time.Parse("2006-01-02", tc.asOf)
		if err != nil {
			t.Fatalf("parse as-of: %v", err)
		}
		cfg.AsOf = asOf
	}
	return Rate(Input{Report: loadFixture(t, tc.report), MonthlyIncome: float64(tc.income)}, cfg)
}

func marshalGolden(t *testing.T, res Result) []byte {
	t.Helper()
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return append(b, '\n')
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./rating -run TestGoldenPayloads -count=1`
Expected: FAIL, ten times, with `read golden payload: open testdata/golden/report1.json: no such file or directory`.

- [ ] **Step 3: Capture the payloads from today's code**

Run: `go test ./rating -run TestGoldenPayloads -update -count=1`
Expected: `ok`. Ten files appear in `scoring/rating/testdata/golden/`.

- [ ] **Step 4: Verify they are what the README and the app say**

Run: `go test ./... -count=1`
Expected: every package `ok`, including `TestReportDateDefaultsToTheBuildDate`.

Run: `grep -h '"score"' rating/testdata/golden/*.json`
Expected scores, in file order (`report1`, `report2`, `report2_2027-07-08`, `report2_2028-07-08`, `report2_2031-07-08`, `report3`, `report3_income_24000`, `report4`, `report5`, `report6`): 31, 43, 45, 74, 90, 81, 83, 62, 45, 80. `report3_income_24000` must be the app's `DEMO_RATING_CLEAN` (score 83, raw 83.3).

- [ ] **Step 5: Commit**

```bash
git add scoring/rating/golden_test.go scoring/rating/testdata/golden
git commit -m "test: pin every rating payload byte for byte before the refactor"
```

---

### Task 2: The new `ubki` package, beside the old one

The new package is built in `scoring/ubkinext` (package name `ubki`) so the old engine keeps compiling; Task 3 moves it into place.

**Files:**
- Create: `scoring/ubkinext/doc.go`, `codes.go`, `values.go`, `report.go`
- Test: `scoring/ubkinext/values_test.go`, `report_test.go`

- [ ] **Step 1: Write the value tests**

Create `scoring/ubkinext/values_test.go`:

```go
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
		// ParseFloat reads these without complaint; an amount must not.
		{"NaN", `<x m="NaN"/>`},
		{"infinity", `<x m="Inf"/>`},
		{"negative infinity", `<x m="-Infinity"/>`},
		{"beyond any real amount", `<x m="1e300"/>`},
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

// Sums of kopiykas are exact where sums of float hryvnias are not: these three
// add up to 99.999999999999986 as float64.
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
```

- [ ] **Step 2: Write the report tests**

Create `scoring/ubkinext/report_test.go`:

```go
package ubki

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The real reports live beside the rating tests that score them.
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

// report1: a guarantee, a payday overdraft rolling 0→30→60→90 days past due,
// and a consumer loan written off (status 13).
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

// report4 declares two jobs, which is where its debt-load income comes from.
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

// report2: a restructured loan that rolls to 134 days past due and is then
// sold to a collector, with an open enforcement proceeding.
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

// An inquiry registry with no entries is a fact: nobody asked.
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
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./ubkinext -count=1`
Expected: FAIL to build: `undefined: Int`, `undefined: Money`, `undefined: Report`, ….

- [ ] **Step 4: Write the package**

Create `scoring/ubkinext/doc.go`:

```go
// Package ubki reads the credit report of УБКІ, the Ukrainian Bureau of Credit
// Histories: the part of its XML template 10 that the СФактор rating uses.
//
// The report is untrusted external data. Numeric and date attributes are often
// empty or hold placeholders such as 1900-01-01, so they are read leniently: an
// unreadable value becomes zero or unknown instead of failing the document.
// Only structurally broken XML is an error.
package ubki
```

Create `scoring/ubkinext/codes.go`:

```go
package ubki

// DealStatus is a deal's lifecycle code (dlflstat).
type DealStatus string

const (
	StatusOpen         DealStatus = "1"
	StatusClosed       DealStatus = "2"
	StatusSold         DealStatus = "3" // sold to a collector
	StatusRestructured DealStatus = "4"
	StatusWrittenOff   DealStatus = "13"
)

// Terminal reports whether the creditor gave up on the deal: sold it on or
// wrote it off.
func (s DealStatus) Terminal() bool { return s == StatusSold || s == StatusWrittenOff }

// Role is the subject's part in a deal (dlrolesub).
type Role string

const (
	RoleBorrower  Role = "1"
	RoleGuarantor Role = "2"
)

// PurposeCreditCard is the purpose code (dlcelcred) of a credit card.
const PurposeCreditCard = "31"

// Creditor types, as reported for deals (dldonor), inquiries and monitoring
// subscriptions (org).
const (
	CreditorBank    = "BNK"
	CreditorMFO     = "MFO" // microfinance
	CreditorFinance = "FIN"
	CreditorBureau  = "BCH"
	CreditorOwn     = "OWN" // whoever requested this very report
)

// Inquiry reasons (reqreason) that are applications for credit.
const (
	ReasonCredit       = "2"
	ReasonOnlineCredit = "4"
)

// ReportTypeIdentity is the report type (typereport) of an identity check,
// which carries no credit history.
const ReportTypeIdentity = "1"
```

Create `scoring/ubkinext/values.go`:

```go
package ubki

import (
	"encoding/xml"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

// Int is a whole-number attribute. Empty or unreadable input reads as 0.
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

// Money is an amount in kopiykas. Reports carry hryvnias with two decimals;
// whole kopiykas keep sums and threshold comparisons exact.
type Money int64

// Hryvnia is one hryvnia, so amounts read as 10 * ubki.Hryvnia.
const Hryvnia Money = 100

// maxHryvnias bounds a readable amount. Nothing in a credit report comes near
// it, and anything beyond it would overflow once turned into kopiykas.
const maxHryvnias = 1e12

var errNotAnAmount = errors.New("ubki: not an amount of hryvnias")

// ParseMoney reads a decimal amount of hryvnias ("1034.62") and rounds it to
// the nearest kopiyka. It rejects empty input, NaN, infinities and anything
// above a trillion hryvnias.
func ParseMoney(s string) (Money, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.Abs(v) > maxHryvnias {
		return 0, errNotAnAmount
	}
	return Money(math.Round(v * 100)), nil
}

// Hryvnias returns the amount in hryvnias.
func (m Money) Hryvnias() float64 { return float64(m) / 100 }

// UnmarshalXMLAttr implements xml.UnmarshalerAttr. An unreadable amount reads
// as zero.
func (m *Money) UnmarshalXMLAttr(attr xml.Attr) error {
	v, err := ParseMoney(attr.Value)
	if err != nil {
		v = 0
	}
	*m = v
	return nil
}

// dateLayout is the only date format УБКІ writes.
const dateLayout = "2006-01-02"

// placeholderDate is what the bureau writes for a date it does not know.
var placeholderDate = time.Date(1900, time.January, 1, 0, 0, 0, 0, time.UTC)

// Date is a calendar date from the report. The zero Date is unknown: the
// attribute was empty, unreadable or a placeholder.
type Date struct {
	t time.Time
}

// NewDate returns the Date of t; the zero time gives the unknown Date.
func NewDate(t time.Time) Date { return Date{t: t} }

// Time returns the date, and false when it is unknown.
func (d Date) Time() (time.Time, bool) { return d.t, !d.t.IsZero() }

// String returns the date as YYYY-MM-DD, or "" when it is unknown.
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
```

Create `scoring/ubkinext/report.go`:

```go
package ubki

import (
	"encoding/xml"
	"fmt"
	"time"
)

// Section ids (comp id) of the parts of the report the rating reads.
const (
	SectionIdentity    = 1  // the subject; their declared employment is the income fallback
	SectionDeals       = 2  // credit deals
	SectionEnforcement = 3  // court enforcement proceedings
	SectionInquiries   = 4  // who requested the subject's history, and why
	SectionMonitoring  = 6  // creditors subscribed to updates about the subject
	SectionSummary     = 78 // the bureau's own aggregates
)

// Report is a УБКІ credit report (<ubkidata>).
type Report struct {
	XMLName  xml.Name    `xml:"ubkidata"`
	Trace    []TraceStep `xml:"tech>trace>step"`
	Sections []Section   `xml:"comp"`
}

// TraceStep is one step of the bureau's report-building trace.
type TraceStep struct {
	Name     string `xml:"name,attr"`
	Finished string `xml:"ftm,attr"` // 2006-01-02 15:04:05.000
}

// Section is one <comp>. encoding/xml cannot choose a struct by the id
// attribute, so Section declares every child the rating reads and each comp
// fills only its own.
type Section struct {
	ID          int            `xml:"id,attr"`
	Subject     *Subject       `xml:"cki"`            // SectionIdentity
	Deals       []Deal         `xml:"crdeal"`         // SectionDeals
	Enforcement *Enforcement   `xml:"penaltiesCount"` // SectionEnforcement
	Inquiries   []Inquiry      `xml:"credres"`        // SectionInquiries
	Monitoring  []Subscription `xml:"moncredres"`     // SectionMonitoring
	Summary     *Summary       `xml:"creditSummary"`  // SectionSummary
}

// Subject is the person the report is about. Only their declared employment is
// read.
type Subject struct {
	Employment []Employment `xml:"work"`
}

// Employment is a declared job. Its income is self-reported.
type Employment struct {
	MonthlyIncome Money `xml:"wdohod,attr"`
	VerifiedOn    Date  `xml:"vdate,attr"`
}

// Deal is one credit line.
type Deal struct {
	Role       Role       `xml:"dlrolesub,attr"`
	LenderType string     `xml:"dldonor,attr"`   // CreditorBank, CreditorMFO, CreditorFinance
	Purpose    string     `xml:"dlcelcred,attr"` // PurposeCreditCard, …
	History    []Snapshot `xml:"deallife"`
}

// Snapshot is a deal as the bureau reported it for one month.
type Snapshot struct {
	ReportedOn  Date       `xml:"dldateclc,attr"`
	Status      DealStatus `xml:"dlflstat,attr"`
	StartedOn   Date       `xml:"dlds,attr"`
	DaysOverdue Int        `xml:"dldayexp,attr"`
	Overdue     Money      `xml:"dlamtexp,attr"`  // amount past due
	Balance     Money      `xml:"dlamtcur,attr"`  // outstanding balance
	Payment     Money      `xml:"dlamtpaym,attr"` // scheduled monthly payment
	Limit       Money      `xml:"dlamtlim,attr"`  // credit limit
}

// Enforcement counts the court enforcement proceedings against the subject.
type Enforcement struct {
	Active Int `xml:"activeCount,attr"`
}

// Inquiry is one request for the subject's credit history.
type Inquiry struct {
	Requester  string `xml:"org,attr"`        // CreditorBank, …, CreditorOwn
	Reason     string `xml:"reqreason,attr"`  // ReasonCredit, ReasonOnlineCredit, …
	ReportType string `xml:"typereport,attr"` // ReportTypeIdentity, …
	Date       Date   `xml:"redate,attr"`
}

// Subscription is a creditor's standing request to be told when the subject's
// history changes. УБКІ heads the section "не впливає на кредитний рейтинг".
type Subscription struct {
	Subscriber string `xml:"org,attr"`
	Start      Date   `xml:"startdate,attr"`
	End        Date   `xml:"enddate,attr"`
}

// Summary is the bureau's own aggregate over the subject's deals.
type Summary struct {
	MonthlyObligations Money `xml:"totalOblPay,attr"`
}

// Parse decodes a report. Only structurally broken XML is an error: unreadable
// values inside a well-formed report read as zero or unknown.
func Parse(data []byte) (*Report, error) {
	var r Report
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("ubki: parse report: %w", err)
	}
	return &r, nil
}

// Deals returns the subject's deals, including the ones they only guarantee.
func (r *Report) Deals() []Deal {
	if s := r.section(SectionDeals); s != nil {
		return s.Deals
	}
	return nil
}

// Inquiries returns the inquiry registry. The second result is false when the
// report has no registry at all, which means "unknown" rather than "none".
func (r *Report) Inquiries() ([]Inquiry, bool) {
	s := r.section(SectionInquiries)
	if s == nil {
		return nil, false
	}
	return s.Inquiries, true
}

// Monitoring returns the creditors' update subscriptions.
func (r *Report) Monitoring() []Subscription {
	if s := r.section(SectionMonitoring); s != nil {
		return s.Monitoring
	}
	return nil
}

// ActiveEnforcements returns the number of open enforcement proceedings.
func (r *Report) ActiveEnforcements() int {
	if s := r.section(SectionEnforcement); s != nil && s.Enforcement != nil {
		return int(s.Enforcement.Active)
	}
	return 0
}

// DeclaredEmployment returns the jobs the subject declared.
func (r *Report) DeclaredEmployment() []Employment {
	if s := r.section(SectionIdentity); s != nil && s.Subject != nil {
		return s.Subject.Employment
	}
	return nil
}

// MonthlyObligations returns the bureau's total of the subject's monthly
// payments, or zero when the report does not carry one.
func (r *Report) MonthlyObligations() Money {
	if s := r.section(SectionSummary); s != nil && s.Summary != nil {
		return s.Summary.MonthlyObligations
	}
	return 0
}

// buildStep is the trace step whose finish time is the report's own timestamp.
const buildStep = "build report"

// traceLayout is the trace's timestamp format. time.Parse accepts the
// fractional seconds the bureau appends without being told about them.
const traceLayout = "2006-01-02 15:04:05"

// BuiltAt returns when the bureau built the report: the finish of the build
// step, else of any readable step, else the date of the report's own OWN
// inquiry. The second result is false when the report carries none of them.
func (r *Report) BuiltAt() (time.Time, bool) {
	if r == nil {
		return time.Time{}, false
	}
	if t, ok := stepFinished(r.Trace, buildStep); ok {
		return t, true
	}
	if t, ok := stepFinished(r.Trace, ""); ok {
		return t, true
	}
	inquiries, _ := r.Inquiries()
	for _, q := range inquiries {
		if t, ok := q.Date.Time(); ok && q.Requester == CreditorOwn {
			return t, true
		}
	}
	return time.Time{}, false
}

// stepFinished returns the finish time of the first step called name, or of
// the first readable step when name is empty.
func stepFinished(steps []TraceStep, name string) (time.Time, bool) {
	for _, s := range steps {
		if name != "" && s.Name != name {
			continue
		}
		if t, err := time.Parse(traceLayout, s.Finished); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (r *Report) section(id int) *Section {
	if r == nil {
		return nil
	}
	for i := range r.Sections {
		if r.Sections[i].ID == id {
			return &r.Sections[i]
		}
	}
	return nil
}

// Latest returns the deal's most recent snapshot, or the zero Snapshot when it
// has none. The bureau writes snapshots in chronological order, so file order
// decides, except that a snapshot dated before one already seen never becomes
// the latest. Of two with the same date, the later entry wins.
func (d Deal) Latest() Snapshot {
	var latest Snapshot
	var newest time.Time
	for _, s := range d.History {
		at, dated := s.ReportedOn.Time()
		if dated && at.Before(newest) {
			continue
		}
		latest = s
		if dated {
			newest = at
		}
	}
	return latest
}
```

- [ ] **Step 5: Run everything**

Run: `go vet ./... && go test ./... -count=1`
Expected: every package `ok`, `ubkinext` included.

- [ ] **Step 6: Commit**

```bash
git add scoring/ubkinext
git commit -m "feat: model the part of the УБКІ report the rating reads, in plain names and kopiykas"
```

- [ ] **Step 7: Prove the `Latest` test pins the new rule**

Temporarily replace the body of `Latest` in `scoring/ubkinext/report.go` with the old rule:

```go
	if len(d.History) == 0 {
		return Snapshot{}
	}
	best := d.History[0]
	for _, s := range d.History[1:] {
		st, sOK := s.ReportedOn.Time()
		bt, bOK := best.ReportedOn.Time()
		if !sOK || !bOK || st.After(bt) {
			best = s
		}
	}
	return best
```

Run: `go test ./ubkinext -run TestLatest -count=1`
Expected: FAIL on "a dated snapshot never loses to an earlier-dated one after an undated one" and "the later entry of two with the same date".

Restore: `git checkout -- scoring/ubkinext/report.go`, then `go test ./ubkinext -count=1` → `ok`.

---

### Task 3: Cut over: the rating as a pipeline on the new `ubki`

One commit, because the old engine cannot compile against the new model: `ubki` moves into place, `rating` is rewritten, the CLI gets its final form, and the server handler is adapted just enough to compile (Task 4 replaces it).

**Files:**
- Move: `scoring/ubkinext/*` → `scoring/ubki/` (the old `scoring/ubki` is deleted)
- Delete: `scoring/rating/*.go` (every old source and test)
- Create: `scoring/rating/{doc,config,result,bands,curve,format,indicators,deals,measure,ceilings,reference,rate}.go`
- Test: `scoring/rating/{helpers,golden,curve,format,indicators,deals,measure,reference,rate,fixtures}_test.go`
- Replace: `scoring/cmd/score/main.go`; Create: `scoring/cmd/score/main_test.go`
- Replace: `scoring/cmd/server/handler.go` (stopgap)

- [ ] **Step 1: Move `ubki` into place**

```bash
git rm -rq scoring/ubki
git mv scoring/ubkinext scoring/ubki
```

Run: `go build ./...`
Expected: FAIL: `rating` uses the old model (`undefined: ubki.CrDeal`, …).

- [ ] **Step 2: Remove the old rating sources and tests**

```bash
git rm -q scoring/rating/*.go
```

- [ ] **Step 3: Write the test helpers**

Create `scoring/rating/helpers_test.go`:

```go
package rating

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sfactor/scoring/ubki"
)

// testAsOf is the reference date of every synthetic report, so no test depends
// on the wall clock.
var testAsOf = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

func daysAgo(n int) time.Time { return testAsOf.AddDate(0, 0, -n) }

// uah writes an amount the way the report does, in hryvnias.
func uah(v float64) ubki.Money { return ubki.Money(math.Round(v * 100)) }

// snap builds one monthly snapshot.
func snap(when, start time.Time, status ubki.DealStatus, dpd int, overdue, payment ubki.Money) ubki.Snapshot {
	return ubki.Snapshot{
		ReportedOn:  ubki.NewDate(when),
		StartedOn:   ubki.NewDate(start),
		Status:      status,
		DaysOverdue: ubki.Int(dpd),
		Overdue:     overdue,
		Payment:     payment,
	}
}

// loan builds a microfinance deal the subject borrowed.
func loan(history ...ubki.Snapshot) ubki.Deal {
	return ubki.Deal{Role: ubki.RoleBorrower, LenderType: ubki.CreditorMFO, History: history}
}

// guarantee builds a deal the subject only guarantees for someone else.
func guarantee(history ...ubki.Snapshot) ubki.Deal {
	return ubki.Deal{Role: ubki.RoleGuarantor, LenderType: ubki.CreditorMFO, History: history}
}

// lenderDeal builds a borrower deal from the given type of lender.
func lenderDeal(lender string, history ...ubki.Snapshot) ubki.Deal {
	return ubki.Deal{Role: ubki.RoleBorrower, LenderType: lender, History: history}
}

func dealsSection(deals ...ubki.Deal) ubki.Section {
	return ubki.Section{ID: ubki.SectionDeals, Deals: deals}
}

func inquiriesSection(inquiries ...ubki.Inquiry) ubki.Section {
	return ubki.Section{ID: ubki.SectionInquiries, Inquiries: inquiries}
}

func monitoringSection(subs ...ubki.Subscription) ubki.Section {
	return ubki.Section{ID: ubki.SectionMonitoring, Monitoring: subs}
}

// application builds a credit application from the given type of lender.
func application(lender string, ago int) ubki.Inquiry {
	return ubki.Inquiry{
		Requester:  lender,
		Reason:     ubki.ReasonCredit,
		ReportType: "2",
		Date:       ubki.NewDate(daysAgo(ago)),
	}
}

func reportOf(sections ...ubki.Section) *ubki.Report {
	return &ubki.Report{Sections: sections}
}

// rate scores a synthetic report on testAsOf with the default configuration.
func rate(r *ubki.Report, income ubki.Money) Result {
	return rateWith(r, income, DefaultConfig())
}

func rateWith(r *ubki.Report, income ubki.Money, cfg Config) Result {
	return Rate(Input{Report: r, MonthlyIncome: income, AsOf: testAsOf}, cfg)
}

// measureOf measures a synthetic report on testAsOf with the default
// configuration.
func measureOf(r *ubki.Report, income ubki.Money) measurements {
	cfg := DefaultConfig()
	return measure(Input{Report: r, MonthlyIncome: income, AsOf: testAsOf}, borrowerDeals(r), testAsOf, cfg)
}

// wantValue asserts that an indicator was measured, at the expected value.
func wantValue(t *testing.T, m measurements, k ParamKey, want float64) {
	t.Helper()
	got, ok := m.values[k]
	if !ok {
		t.Fatalf("%s: not measured, want %v", k, want)
	}
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", k, got, want)
	}
}

func wantUnmeasured(t *testing.T, m measurements, k ParamKey) {
	t.Helper()
	if got, ok := m.values[k]; ok {
		t.Errorf("%s = %v, want it unmeasured", k, got)
	}
}

func hasFlag(flags []Flag, want Flag) bool { return slices.Contains(flags, want) }

func loadFixture(t *testing.T, name string) *ubki.Report {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	r, err := ubki.Parse(data)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return r
}

// fixtureNames lists the real and synthetic УБКІ reports in testdata.
func fixtureNames(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "*.xml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	names := make([]string, len(paths))
	for i, p := range paths {
		names[i] = filepath.Base(p)
	}
	return names
}
```

- [ ] **Step 4: Rewrite the golden test for the new `Input`**

Create `scoring/rating/golden_test.go` (the cases and the golden files are unchanged; only `rateGolden` moves `AsOf` into `Input` and income into kopiykas):

```go
package rating

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sfactor/scoring/ubki"
)

var update = flag.Bool("update", false, "rewrite testdata/golden from the current output")

// goldenCase is one pinned rating: a fixture, a reference date and the
// verified income supplied with it, in whole hryvnias.
type goldenCase struct {
	name   string
	report string
	asOf   string
	income int
}

// goldenCases pin the whole payload, byte for byte, so a refactor cannot move
// any number the app renders without the diff showing it.
var goldenCases = []goldenCase{
	{name: "report1", report: "report1.xml", asOf: "2026-07-08"},
	{name: "report2", report: "report2.xml", asOf: "2026-07-08"},
	{name: "report3", report: "report3.xml", asOf: "2026-07-08"},
	{name: "report4", report: "report4.xml", asOf: "2026-07-08"},
	{name: "report5", report: "report5.xml", asOf: "2026-07-08"},
	{name: "report6", report: "report6.xml", asOf: "2026-07-08"},
	// The source of the app's DEMO_RATING_CLEAN (src/lib/ratingDemo.ts).
	{name: "report3_income_24000", report: "report3.xml", asOf: "2026-07-08", income: 24000},
	// The sold debt in report2, ageing through both terminal ceilings.
	{name: "report2_2027-07-08", report: "report2.xml", asOf: "2027-07-08"},
	{name: "report2_2028-07-08", report: "report2.xml", asOf: "2028-07-08"},
	{name: "report2_2031-07-08", report: "report2.xml", asOf: "2031-07-08"},
}

func TestGoldenPayloads(t *testing.T) {
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalGolden(t, rateGolden(t, tc))
			path := filepath.Join("testdata", "golden", tc.name+".json")

			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden payload: %v (run go test ./rating -update to create it)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("payload differs from %s.\nIf the change is intended, run go test ./rating -update and review the diff.\ngot:\n%s", path, got)
			}
		})
	}
}

// A report rated on its own build date must read exactly as when that date is
// passed explicitly: the reference date is a calendar date, not a timestamp.
func TestReportDateDefaultsToTheBuildDate(t *testing.T) {
	for _, tc := range goldenCases[:6] {
		t.Run(tc.name, func(t *testing.T) {
			explicit := marshalGolden(t, rateGolden(t, tc))
			implicit := marshalGolden(t, rateGolden(t, goldenCase{report: tc.report}))
			if !bytes.Equal(explicit, implicit) {
				t.Errorf("rating on the report's own date differs from rating with as_of=%s", tc.asOf)
			}
		})
	}
}

func rateGolden(t *testing.T, tc goldenCase) Result {
	t.Helper()
	in := Input{
		Report:        loadFixture(t, tc.report),
		MonthlyIncome: ubki.Money(tc.income) * ubki.Hryvnia,
	}
	if tc.asOf != "" {
		asOf, err := time.Parse("2006-01-02", tc.asOf)
		if err != nil {
			t.Fatalf("parse as-of: %v", err)
		}
		in.AsOf = asOf
	}
	return Rate(in, DefaultConfig())
}

func marshalGolden(t *testing.T, res Result) []byte {
	t.Helper()
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return append(b, '\n')
}
```

- [ ] **Step 5: Write the indicator, curve and format tests**

Create `scoring/rating/indicators_test.go`:

```go
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

// The colour a borrower sees comes straight from the agreed table, so every
// boundary in it is pinned here. Moving a threshold means arguing with this.
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

// A better value never earns fewer points: otherwise a borrower could improve a
// number and watch their rating fall.
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

// Each ceiling sits on the top of its colour: a borrower capped for arrears
// reads red, one capped for an old unresolved debt reads amber, and one point
// more would already be the next colour.
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
```

Create `scoring/rating/curve_test.go`:

```go
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
```

Create `scoring/rating/format_test.go`:

```go
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
```

- [ ] **Step 6: Write the deal-classification tests**

Create `scoring/rating/deals_test.go`:

```go
package rating

import (
	"testing"
	"time"

	"github.com/sfactor/scoring/ubki"
)

const purposeConsumer = "7"

// balanceSnap builds a snapshot carrying the balance and the limit, which the
// terminal-debt and credit-card rules read.
func balanceSnap(when time.Time, status ubki.DealStatus, dpd int, overdue, balance, limit ubki.Money) ubki.Snapshot {
	return ubki.Snapshot{
		ReportedOn:  ubki.NewDate(when),
		StartedOn:   ubki.NewDate(daysAgo(900)),
		Status:      status,
		DaysOverdue: ubki.Int(dpd),
		Overdue:     overdue,
		Balance:     balance,
		Limit:       limit,
	}
}

func purposeDeal(purpose string, history ...ubki.Snapshot) ubki.Deal {
	return ubki.Deal{Role: ubki.RoleBorrower, LenderType: ubki.CreditorBank, Purpose: purpose, History: history}
}

func TestStatusDecidesStanding(t *testing.T) {
	tests := []struct {
		status ubki.DealStatus
		want   standing
	}{
		{ubki.StatusOpen, standingActive},
		{ubki.StatusRestructured, standingActive}, // the terms changed, the debt did not go away
		{ubki.StatusClosed, standingRepaid},
		{ubki.StatusSold, standingTerminal},
		{ubki.StatusWrittenOff, standingTerminal},
		{"99", standingOther},
	}
	for _, tt := range tests {
		d := classify(purposeDeal(purposeConsumer, balanceSnap(daysAgo(100), tt.status, 0, 0, 0, 0)))
		if d.standing != tt.want {
			t.Errorf("status %s: standing = %d, want %d", tt.status, d.standing, tt.want)
		}
	}
}

// A debt the creditor sold or wrote off is never repaid, even if the record is
// later closed.
func TestClosedAfterASaleIsNotRepaid(t *testing.T) {
	d := classify(purposeDeal(purposeConsumer,
		balanceSnap(daysAgo(300), ubki.StatusSold, 0, 0, 0, 0),
		balanceSnap(daysAgo(100), ubki.StatusClosed, 0, 0, 0, 0),
	))
	if d.standing != standingOther {
		t.Errorf("standing = %d, want neither open nor repaid", d.standing)
	}
}

// УБКІ zeroes the balance in the very snapshot that reports the sale, so the
// amount is dug out of the history: the borrower is shown the figure the
// bureau last reported.
func TestTerminalDebtIsRecoveredFromTheHistory(t *testing.T) {
	tests := []struct {
		name    string
		history []ubki.Snapshot
		want    ubki.Money
	}{
		{
			name: "the last reported arrears",
			history: []ubki.Snapshot{
				balanceSnap(daysAgo(300), ubki.StatusOpen, 40, uah(465.10), uah(2299.66), 0),
				balanceSnap(daysAgo(200), ubki.StatusOpen, 134, uah(1034.62), uah(2069.18), 0),
				balanceSnap(daysAgo(100), ubki.StatusSold, 0, 0, 0, 0),
			},
			want: uah(1034.62),
		},
		{
			name: "the outstanding balance when no arrears were ever reported",
			history: []ubki.Snapshot{
				balanceSnap(daysAgo(300), ubki.StatusOpen, 0, 0, uah(1834.56), 0),
				balanceSnap(daysAgo(200), ubki.StatusOpen, 0, 0, uah(2764.76), 0),
				balanceSnap(daysAgo(100), ubki.StatusWrittenOff, 0, 0, 0, 0),
			},
			want: uah(2764.76),
		},
		{
			name: "zero when neither was ever reported",
			history: []ubki.Snapshot{
				balanceSnap(daysAgo(300), ubki.StatusOpen, 0, 0, 0, uah(5000)),
				balanceSnap(daysAgo(100), ubki.StatusWrittenOff, 0, 0, 0, uah(5000)),
			},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(purposeDeal(purposeConsumer, tt.history...)).terminalArrears; got != tt.want {
				t.Errorf("terminalArrears = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCreditCardWithALiveLimitIsActive(t *testing.T) {
	tests := []struct {
		name string
		deal ubki.Deal
		want standing
	}{
		{
			name: "a closed card still holding a limit",
			deal: purposeDeal(ubki.PurposeCreditCard,
				balanceSnap(daysAgo(400), ubki.StatusOpen, 0, 0, 0, uah(15000)),
				balanceSnap(daysAgo(30), ubki.StatusClosed, 0, 0, 0, uah(15000)),
			),
			want: standingActive,
		},
		{
			name: "a limit that lapsed before the card closed does not revive it",
			deal: purposeDeal(ubki.PurposeCreditCard,
				balanceSnap(daysAgo(400), ubki.StatusOpen, 0, 0, 0, uah(300)),
				balanceSnap(daysAgo(370), ubki.StatusOpen, 0, 0, 0, 0),
				balanceSnap(daysAgo(340), ubki.StatusClosed, 0, 0, 0, 0),
			),
			want: standingRepaid,
		},
		{
			name: "the rule is about cards, not every closed deal",
			deal: purposeDeal(purposeConsumer,
				balanceSnap(daysAgo(30), ubki.StatusClosed, 0, 0, 0, uah(15000)),
			),
			want: standingRepaid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(tt.deal).standing; got != tt.want {
				t.Errorf("standing = %d, want %d", got, tt.want)
			}
		})
	}
}

// A sold card is a sold debt: otherwise the card rule would launder a collector
// debt into an ordinary credit line.
func TestSoldCardIsTerminalFirst(t *testing.T) {
	d := classify(purposeDeal(ubki.PurposeCreditCard,
		balanceSnap(daysAgo(200), ubki.StatusOpen, 90, uah(3200), uah(8000), uah(15000)),
		balanceSnap(daysAgo(100), ubki.StatusSold, 0, 0, 0, uah(15000)),
	))
	if d.standing != standingTerminal {
		t.Errorf("standing = %d, want terminal", d.standing)
	}
	if d.terminalArrears != uah(3200) {
		t.Errorf("terminalArrears = %d, want 3200 ₴", d.terminalArrears)
	}
}

func TestTerminalEventIsTheMostRecentOne(t *testing.T) {
	e := terminalEventOf([]ubki.Snapshot{
		balanceSnap(daysAgo(900), ubki.StatusSold, 0, 0, 0, 0),
		balanceSnap(daysAgo(300), ubki.StatusWrittenOff, 0, 0, 0, 0),
		balanceSnap(daysAgo(100), ubki.StatusClosed, 0, 0, 0, 0),
	})
	if !e.happened || !e.at.Equal(daysAgo(300)) {
		t.Errorf("event = %+v, want the write-off %v", e, daysAgo(300))
	}
}

// One undated terminal snapshot makes the event recent everywhere it is asked
// about, whatever date the latest snapshot carries: the arrears rule and the
// ceilings used to disagree on exactly this.
func TestUndatedTerminalSnapshotMakesTheEventRecent(t *testing.T) {
	start := ubki.NewDate(daysAgo(3000))
	d := classify(ubki.Deal{
		Role: ubki.RoleBorrower,
		History: []ubki.Snapshot{
			{StartedOn: start, Status: ubki.StatusOpen, DaysOverdue: 90, Overdue: uah(5000), ReportedOn: ubki.NewDate(daysAgo(2500))},
			{StartedOn: start, Status: ubki.StatusSold}, // no report date
			{StartedOn: start, Status: ubki.StatusSold, ReportedOn: ubki.NewDate(daysAgo(2400))},
		},
	})
	cfg := DefaultConfig()

	if !d.terminal.within(testAsOf, cfg.TerminalLookbackMonths) {
		t.Error("an undated sale is not shown to be old, so it is recent")
	}
	if overdue, _ := d.arrears(testAsOf, cfg); !overdue {
		t.Error("the arrears rule must agree with the ceilings about the same sale")
	}
}

// The windows are calendar months. A sale on 3 April 2025 is 36 months old on
// 3 April 2028, and not a day earlier.
func TestTerminalWindowIsCalendarMonths(t *testing.T) {
	sold := terminalEvent{happened: true, at: time.Date(2025, 4, 3, 0, 0, 0, 0, time.UTC)}

	if !sold.within(time.Date(2028, 4, 3, 0, 0, 0, 0, time.UTC), 36) {
		t.Error("exactly 36 months later the sale is still within 36 months")
	}
	if sold.within(time.Date(2028, 4, 4, 0, 0, 0, 0, time.UTC), 36) {
		t.Error("a day past 36 months the sale is outside the window")
	}
	if (terminalEvent{}).within(testAsOf, 36) {
		t.Error("a deal that was never sold or written off has no terminal event")
	}
}
```

- [ ] **Step 7: Write the measurement tests**

Create `scoring/rating/measure_test.go`:

```go
package rating

import (
	"testing"

	"github.com/sfactor/scoring/ubki"
)

// Surety for someone deep in arrears is not the subject's own overdue debt.
func TestGuaranteesAreNotTheSubjectsOwnCredit(t *testing.T) {
	m := measureOf(reportOf(dealsSection(
		guarantee(snap(daysAgo(10), daysAgo(400), ubki.StatusOpen, 120, uah(9000), uah(1500))),
		loan(snap(daysAgo(10), daysAgo(400), ubki.StatusOpen, 0, 0, uah(800))),
	)), 0)

	wantValue(t, m, ParamOverdueDeals, 0)
	wantValue(t, m, ParamOverdueAmount, 0)
	wantValue(t, m, ParamActiveDeals, 1)
}

// A debt the creditor sold or wrote off is still owed: never repaid, and a
// live, overdue obligation at the amount reported before the creditor zeroed it.
func TestSoldAndWrittenOffDebtsStayLiveAndOverdue(t *testing.T) {
	for _, status := range []ubki.DealStatus{ubki.StatusSold, ubki.StatusWrittenOff} {
		t.Run(string(status), func(t *testing.T) {
			start := daysAgo(500)
			m := measureOf(reportOf(dealsSection(loan(
				snap(daysAgo(200), start, ubki.StatusOpen, 90, uah(5000), uah(1000)),
				snap(daysAgo(100), start, status, 0, 0, 0),
			))), 0)

			wantValue(t, m, ParamClosedDeals, 0)
			wantValue(t, m, ParamActiveDeals, 1)
			wantValue(t, m, ParamOverdueDeals, 1)
			wantValue(t, m, ParamOverdueAmount, 5000)
			wantValue(t, m, ParamCleanStreak, 0)
		})
	}
}

// Past the terminal lookback, "not paying it today" is no longer something the
// report shows: the deal leaves indicators 3 and 4 and the clean streak runs
// again. It stays open: the obligation itself has not expired.
func TestTerminalDebtStopsBeingArrearsAfterTheLookback(t *testing.T) {
	writtenOff := func(monthsAgo int) *ubki.Report {
		at := testAsOf.AddDate(0, -monthsAgo, 0)
		start := daysAgo(2500)
		return reportOf(dealsSection(loan(
			snap(at.AddDate(0, 0, -60), start, ubki.StatusOpen, 120, uah(9000), uah(2000)),
			snap(at, start, ubki.StatusWrittenOff, 0, 0, 0),
		)))
	}

	t.Run("inside the lookback", func(t *testing.T) {
		m := measureOf(writtenOff(10), 0)
		wantValue(t, m, ParamOverdueDeals, 1)
		wantValue(t, m, ParamOverdueAmount, 9000)
		wantValue(t, m, ParamActiveDeals, 1)
		wantValue(t, m, ParamCleanStreak, 0)
	})

	t.Run("past the lookback", func(t *testing.T) {
		m := measureOf(writtenOff(40), 0)
		wantValue(t, m, ParamOverdueDeals, 0)
		wantValue(t, m, ParamOverdueAmount, 0)
		wantValue(t, m, ParamActiveDeals, 1)
		wantValue(t, m, ParamClosedDeals, 0)
		if streak := m.values[ParamCleanStreak]; streak <= 0 {
			t.Errorf("clean streak = %v, want it running from the last real arrears", streak)
		}
	})
}

// An undated terminal snapshot counts as current: nothing shows that it is old.
func TestUndatedTerminalSnapshotIsCurrentArrears(t *testing.T) {
	start := daysAgo(2500)
	m := measureOf(reportOf(dealsSection(loan(
		snap(daysAgo(1200), start, ubki.StatusOpen, 120, uah(9000), uah(2000)),
		ubki.Snapshot{StartedOn: ubki.NewDate(start), Status: ubki.StatusWrittenOff},
	))), 0)

	wantValue(t, m, ParamOverdueDeals, 1)
	wantValue(t, m, ParamOverdueAmount, 9000)
	wantValue(t, m, ParamActiveDeals, 1)
}

// A written-off debt whose amounts УБКІ never reported is still a debt: its
// "0 ₴" means "not reported", so the residue threshold must not swallow it.
func TestTerminalDebtIsOverdueEvenWithoutAnAmount(t *testing.T) {
	m := measureOf(reportOf(dealsSection(purposeDeal(purposeConsumer,
		balanceSnap(daysAgo(300), ubki.StatusOpen, 0, 0, 0, uah(5000)),
		balanceSnap(daysAgo(100), ubki.StatusWrittenOff, 0, 0, 0, uah(5000)),
	))), 0)

	wantValue(t, m, ParamOverdueDeals, 1)
	wantValue(t, m, ParamOverdueAmount, 0)
	wantValue(t, m, ParamActiveDeals, 1)
	wantValue(t, m, ParamClosedDeals, 0)
}

func TestClosedDealCountsAsRepaid(t *testing.T) {
	start := daysAgo(500)
	m := measureOf(reportOf(dealsSection(loan(
		snap(daysAgo(200), start, ubki.StatusOpen, 0, 0, uah(1000)),
		snap(daysAgo(100), start, ubki.StatusClosed, 0, 0, 0),
	))), 0)

	wantValue(t, m, ParamClosedDeals, 1)
	wantValue(t, m, ParamActiveDeals, 0)
}

func TestCleanStreak(t *testing.T) {
	start := daysAgo(700)
	tests := []struct {
		name    string
		history []ubki.Snapshot
		want    float64
	}{
		{
			name:    "never late: as long as the history",
			history: []ubki.Snapshot{snap(daysAgo(30), daysAgo(365), ubki.StatusOpen, 0, 0, uah(500))},
			want:    365,
		},
		{
			name: "from the last snapshot in arrears",
			history: []ubki.Snapshot{
				snap(daysAgo(400), start, ubki.StatusOpen, 45, uah(800), uah(500)),
				snap(daysAgo(250), start, ubki.StatusOpen, 0, 0, uah(500)),
				snap(daysAgo(20), start, ubki.StatusOpen, 0, 0, uah(500)),
			},
			want: 400,
		},
		{
			// The latest snapshot is 23 days old, and the borrower is late today.
			name:    "zero while overdue today",
			history: []ubki.Snapshot{snap(daysAgo(23), start, ubki.StatusOpen, 15, uah(700), uah(500))},
			want:    0,
		},
		{
			// A 4 ₴ residue must not wipe two years of on-time payments while
			// showing up in no indicator.
			name: "a residue does not break it",
			history: []ubki.Snapshot{
				snap(daysAgo(400), start, ubki.StatusOpen, 45, uah(800), uah(500)),
				snap(daysAgo(120), start, ubki.StatusOpen, 3, uah(4), uah(500)),
				snap(daysAgo(20), start, ubki.StatusOpen, 0, 0, uah(500)),
			},
			want: 400,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantValue(t, measureOf(reportOf(dealsSection(loan(tt.history...))), 0), ParamCleanStreak, tt.want)
		})
	}
}

func TestNewDealWindowIsSixMonthsInclusive(t *testing.T) {
	tests := []struct {
		startedAgo int
		want       float64
	}{
		{182, 1}, {183, 1}, {184, 0},
	}
	for _, tt := range tests {
		m := measureOf(reportOf(dealsSection(loan(
			snap(daysAgo(1), daysAgo(tt.startedAgo), ubki.StatusOpen, 0, 0, uah(500)),
		))), 0)
		wantValue(t, m, ParamNewDeals, tt.want)
	}
}

// Without a single start date there is no history age, no new-deal count and no
// streak to measure — and a gap is not a zero.
func TestHistoryIsUnmeasuredWithoutStartDates(t *testing.T) {
	m := measureOf(reportOf(dealsSection(ubki.Deal{
		Role:    ubki.RoleBorrower,
		History: []ubki.Snapshot{{Status: ubki.StatusOpen}},
	})), 0)

	wantUnmeasured(t, m, ParamHistoryAge)
	wantUnmeasured(t, m, ParamNewDeals)
	wantUnmeasured(t, m, ParamCleanStreak)
	wantValue(t, m, ParamActiveDeals, 1)
}

func TestOnlyCreditApplicationsCountAsInquiries(t *testing.T) {
	inquiry := func(requester, reason, reportType string, ago int) ubki.Inquiry {
		return ubki.Inquiry{Requester: requester, Reason: reason, ReportType: reportType, Date: ubki.NewDate(daysAgo(ago))}
	}
	m := measureOf(reportOf(
		dealsSection(loan(snap(daysAgo(1), daysAgo(400), ubki.StatusOpen, 0, 0, uah(500)))),
		inquiriesSection(
			inquiry("MFO", "2", "2", 10),  // counts: an application
			inquiry("BNK", "4", "2", 20),  // counts: an online application
			inquiry("OWN", "2", "2", 30),  // not: our own refresh pull
			inquiry("MFO", "6", "2", 40),  // not: a verification
			inquiry("MFO", "2", "1", 50),  // not: an identity-only report
			inquiry("MFO", "2", "2", 200), // not: older than six months
		),
	), 0)

	wantValue(t, m, ParamInquiries, 2)
}

func TestNoInquiryRegistryIsUnmeasuredNotZero(t *testing.T) {
	m := measureOf(reportOf(dealsSection(loan(
		snap(daysAgo(1), daysAgo(400), ubki.StatusOpen, 0, 0, uah(500)),
	))), 0)

	wantUnmeasured(t, m, ParamInquiries)
}

func TestDebtLoadIncome(t *testing.T) {
	declared := func(ago int) ubki.Section {
		return ubki.Section{ID: ubki.SectionIdentity, Subject: &ubki.Subject{Employment: []ubki.Employment{
			{MonthlyIncome: uah(10000), VerifiedOn: ubki.NewDate(daysAgo(ago))},
		}}}
	}
	deals := dealsSection(loan(snap(daysAgo(1), daysAgo(400), ubki.StatusOpen, 0, 0, uah(3000))))

	tests := []struct {
		name   string
		report *ubki.Report
		income ubki.Money
		want   float64
		source IncomeSource
	}{
		{"verified income comes first", reportOf(deals, declared(90)), uah(20000), 15, IncomeFromProfile},
		{"a recent declaration stands in", reportOf(deals, declared(90)), 0, 30, IncomeFromUBKI},
		{"a declaration exactly a year old still stands in", reportOf(deals, declared(365)), 0, 30, IncomeFromUBKI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := measureOf(tt.report, tt.income)
			wantValue(t, m, ParamDebtLoad, tt.want)
			if m.incomeSource != tt.source {
				t.Errorf("income source = %q, want %q", m.incomeSource, tt.source)
			}
		})
	}

	// Two years old, a declaration would make the load look better than it is.
	m := measureOf(reportOf(deals, declared(730)), 0)
	wantUnmeasured(t, m, ParamDebtLoad)
	if m.incomeSource != "" {
		t.Errorf("income source = %q, want none", m.incomeSource)
	}
}

// A revolving line reports no scheduled payment, so summing payments
// understates the load; the bureau's own total knows better.
func TestDebtLoadPrefersTheBureausObligationTotal(t *testing.T) {
	m := measureOf(reportOf(
		dealsSection(loan(snap(daysAgo(1), daysAgo(400), ubki.StatusOpen, 0, 0, 0))),
		ubki.Section{ID: ubki.SectionSummary, Summary: &ubki.Summary{MonthlyObligations: uah(6000)}},
	), uah(20000))

	wantValue(t, m, ParamDebtLoad, 30)
}

// A few hryvnias left after an early payoff are a rounding tail, not a default:
// at or below the threshold they vanish from every indicator at once.
func TestResidueIsNotArrears(t *testing.T) {
	tests := []struct {
		name    string
		residue ubki.Money
		deals   float64
		amount  float64
	}{
		{"below the threshold", uah(9.99), 0, 0},
		{"exactly at it", uah(10), 0, 0},
		{"just above it", uah(10.01), 1, 10.01},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := measureOf(reportOf(dealsSection(loan(
				snap(daysAgo(10), daysAgo(600), ubki.StatusOpen, 7, tt.residue, uah(500)),
			))), 0)
			wantValue(t, m, ParamOverdueDeals, tt.deals)
			wantValue(t, m, ParamOverdueAmount, tt.amount)
		})
	}
}

// Indicators 3 and 4 speak about the same deals. "One overdue loan" beside a sum
// that includes two other balances would cost the screen its credibility.
func TestOverdueCountAndAmountDescribeTheSameDeals(t *testing.T) {
	start := daysAgo(600)
	m := measureOf(reportOf(dealsSection(
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 12, uah(4.50), uah(500))), // residue
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 40, uah(2000), uah(900))), // arrears
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 0, uah(250), uah(700))),   // no days past due
		loan(snap(daysAgo(10), start, ubki.StatusClosed, 30, uah(800), 0)),       // not live
	)), 0)

	wantValue(t, m, ParamOverdueDeals, 1)
	wantValue(t, m, ParamOverdueAmount, 2000)
}

// Overdue balances that add up to exactly 100 or 1000 ₴ sit on a band edge. In
// float hryvnias they summed to 99.999999999999986 (green, beside "100 ₴") and
// 1000.0000000000001 (red, beside "1 000 ₴").
func TestOverdueAmountIsSummedExactly(t *testing.T) {
	overdue := func(amounts ...float64) measurements {
		var deals []ubki.Deal
		for _, a := range amounts {
			deals = append(deals, loan(snap(daysAgo(10), daysAgo(600), ubki.StatusOpen, 30, uah(a), uah(100))))
		}
		return measureOf(reportOf(dealsSection(deals...)), 0)
	}

	tests := []struct {
		amounts []float64
		want    ubki.Money
	}{
		{[]float64{10.14, 58.12, 31.74}, 100 * ubki.Hryvnia},
		{[]float64{11.44, 512.19, 476.37}, 1000 * ubki.Hryvnia},
	}
	for _, tt := range tests {
		m := overdue(tt.amounts...)
		if got := m.book.overdueAmount; got != tt.want {
			t.Errorf("%v: overdue amount = %d kopiykas, want exactly %d", tt.amounts, got, tt.want)
		}
		if level := levelOf(ParamOverdueAmount, m.values[ParamOverdueAmount]); level != LevelMedium {
			t.Errorf("%v: %v ₴ overdue reads %q, want medium (100–1000 ₴)", tt.amounts, tt.want.Hryvnias(), level)
		}
	}
}
```

- [ ] **Step 8: Write the reference-block tests**

Create `scoring/rating/reference_test.go`:

```go
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
		// The first branch fires exactly when indicator 9 turns red: "більше 15".
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
			application("OWN", 40), // our own refresh pull: not an application
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

// A subscription whose last day is the reference date is still watching.
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

// Rated on its own build date, a report is rated on that calendar day. The
// bureau's timestamp carries a time of day; a subscription ending on the build
// date used to read as finished for it, and as active with the same date given
// explicitly.
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
```

- [ ] **Step 9: Write the aggregation and ceiling tests**

Create `scoring/rating/rate_test.go`:

```go
package rating

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/sfactor/scoring/ubki"
)

// cleanBorrower is a five-year file: one loan serviced on time and four repaid,
// nothing overdue, no recent applications.
func cleanBorrower() *ubki.Report {
	deals := []ubki.Deal{loan(snap(daysAgo(10), daysAgo(1800), ubki.StatusOpen, 0, 0, uah(2000)))}
	for i := range 4 {
		start := daysAgo(1700 - i*100)
		deals = append(deals, loan(snap(start.AddDate(0, 0, 200), start, ubki.StatusClosed, 0, 0, 0)))
	}
	return reportOf(dealsSection(deals...), inquiriesSection())
}

// overdueBorrower is the same file with the live loan past due.
func overdueBorrower(amount ubki.Money) *ubki.Report {
	r := cleanBorrower()
	r.Sections[0].Deals[0] = loan(snap(daysAgo(10), daysAgo(1800), ubki.StatusOpen, 20, amount, uah(2000)))
	return r
}

// writtenOff is a file whose only debt was written off terminalAgo days ago,
// with no amount ever reported.
func writtenOff(terminalAgo int) *ubki.Report {
	start := daysAgo(1800)
	return reportOf(dealsSection(loan(
		snap(daysAgo(terminalAgo+60), start, ubki.StatusOpen, 0, 0, uah(2000)),
		snap(daysAgo(terminalAgo), start, ubki.StatusWrittenOff, 0, 0, 0),
	)), inquiriesSection())
}

// The whole promise of the system: the borrower adds up what they see and gets
// their own number back.
func TestContributionsAddUpToTheRating(t *testing.T) {
	for _, tt := range []struct {
		name   string
		report *ubki.Report
		income ubki.Money
	}{
		{"clean, with income", cleanBorrower(), uah(20000)},
		{"clean, without income", cleanBorrower(), 0},
		{"overdue", overdueBorrower(uah(5000)), uah(20000)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := rate(tt.report, tt.income)
			sum := 0.0
			for _, p := range res.Parameters {
				sum += p.Contribution
			}
			if math.Abs(sum-res.Raw) > 0.001 {
				t.Errorf("contributions add up to %v, raw is %v", sum, res.Raw)
			}
		})
	}
}

func TestMissingIncomeRedistributesWeightInsteadOfPenalizing(t *testing.T) {
	with := rate(cleanBorrower(), uah(20000))
	without := rate(cleanBorrower(), 0)

	load, _ := without.Parameter(ParamDebtLoad)
	if load.Level != LevelUnknown || load.Contribution != 0 {
		t.Errorf("debt load = %q contributing %v, want unknown contributing nothing", load.Level, load.Contribution)
	}
	if without.IncomeSource != "" {
		t.Errorf("income source = %q, want none", without.IncomeSource)
	}
	if without.Band != with.Band || without.Score < with.Score-3 {
		t.Errorf("missing income moved the rating from %d (%s) to %d (%s)",
			with.Score, with.Band, without.Score, without.Band)
	}
}

func TestIndicatorsAreListedInTableOrder(t *testing.T) {
	res := rate(cleanBorrower(), uah(20000))
	if len(res.Parameters) != len(indicators) {
		t.Fatalf("%d indicators, want %d", len(res.Parameters), len(indicators))
	}
	for i, p := range res.Parameters {
		if p.Key != indicators[i].key || p.Order != i+1 {
			t.Errorf("position %d holds %s (order %d), want %s (order %d)", i, p.Key, p.Order, indicators[i].key, i+1)
		}
	}
}

func TestCleanFileRatesHigh(t *testing.T) {
	res := rate(cleanBorrower(), uah(20000))
	if !res.Available || res.Band != BandHigh || res.Capped {
		t.Errorf("clean file: available=%v band=%s capped=%v (score %d)", res.Available, res.Band, res.Capped, res.Score)
	}
}

func TestLiveArrearsCapTheRatingInTheRed(t *testing.T) {
	res := rate(overdueBorrower(uah(5000)), uah(20000))

	if !res.Capped || res.CapReason != CapCurrentOverdue {
		t.Fatalf("capped=%v reason=%q, want %q", res.Capped, res.CapReason, CapCurrentOverdue)
	}
	if res.Score != DefaultConfig().CapScore || res.Band != BandLow {
		t.Errorf("score %d (%s), want the ceiling %d in the red", res.Score, res.Band, DefaultConfig().CapScore)
	}
}

// Three real arrears adding up to exactly 100 ₴ reach the ceiling's threshold;
// in float hryvnias they fell a hair short of it.
func TestArrearsOfExactlyTheThresholdCapTheRating(t *testing.T) {
	r := cleanBorrower()
	start := daysAgo(1800)
	r.Sections[0].Deals = append(r.Sections[0].Deals[1:],
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 30, uah(10.14), uah(700))),
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 30, uah(58.12), uah(700))),
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 30, uah(31.74), uah(700))),
	)
	cfg := DefaultConfig()
	cfg.CapScore = 1 // below anything the file scores, so the result shows whether the ceiling applies

	if res := rateWith(r, uah(20000), cfg); res.CapReason != CapCurrentOverdue {
		t.Errorf("cap reason = %q, want %q for 100.00 ₴ overdue", res.CapReason, CapCurrentOverdue)
	}
}

// Three hryvnias left over from an early payoff are not a default.
func TestResidueDoesNotCapTheRating(t *testing.T) {
	res := rate(overdueBorrower(uah(3)), uah(20000))
	if res.Capped || res.Score <= DefaultConfig().CapScore {
		t.Errorf("a 3 ₴ residue: capped=%v score=%d", res.Capped, res.Score)
	}
}

// A file already below the ceiling is left alone, and not labelled capped.
func TestCeilingNeverRaisesAScore(t *testing.T) {
	deals := make([]ubki.Deal, 8)
	for i := range deals {
		deals[i] = loan(snap(daysAgo(5), daysAgo(120-i*10), ubki.StatusOpen, 60, uah(4000), uah(3000)))
	}
	res := rate(reportOf(dealsSection(deals...), inquiriesSection()), uah(10000))

	if res.Score > DefaultConfig().CapScore {
		t.Fatalf("a deeply distressed file scored %d", res.Score)
	}
	if res.Capped {
		t.Error("capped although the ceiling changed nothing")
	}
}

// A written-off debt whose arrears were recovered needs no ceiling of its own
// to be seen: indicators 3 and 4 carry it, and the live-arrears ceiling bites
// for a reason the borrower can read off the table.
func TestRecoveredWriteOffCapsAsLiveArrears(t *testing.T) {
	start := daysAgo(1800)
	res := rate(reportOf(dealsSection(loan(
		snap(daysAgo(360), start, ubki.StatusOpen, 120, uah(9000), uah(2000)),
		snap(daysAgo(300), start, ubki.StatusWrittenOff, 0, 0, 0),
	)), inquiriesSection()), uah(20000))

	if res.CapReason != CapCurrentOverdue || res.Band != BandLow {
		t.Errorf("cap reason %q, band %s; want %q in the red", res.CapReason, res.Band, CapCurrentOverdue)
	}
}

// The terminal ceilings are the backstop for a debt the indicators cannot
// price: the bureau reported the write-off but no amount, so indicator 4 shows
// 0 ₴. They step down in two stages, so a default stops being red without
// becoming invisible on the same day.
func TestUnpricedWriteOffStepsDownThroughTwoCeilings(t *testing.T) {
	cfg := DefaultConfig()
	tests := []struct {
		name   string
		ago    int
		reason CapReason
		score  int
		band   Band
	}{
		{"ten months on: held in the red", 300, CapTerminalDebt, cfg.CapScore, BandLow},
		{"five years on: not red, not clear", 1825, CapTerminalDebtAged, cfg.CapTerminalSoftScore, BandMedium},
		{"past both windows: nothing left but the flag", 2400, "", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := rate(writtenOff(tt.ago), uah(20000))
			if res.CapReason != tt.reason {
				t.Fatalf("cap reason = %q, want %q (score %d)", res.CapReason, tt.reason, res.Score)
			}
			if tt.reason != "" && (res.Score != tt.score || res.Band != tt.band) {
				t.Errorf("score %d (%s), want %d (%s)", res.Score, res.Band, tt.score, tt.band)
			}
			if !hasFlag(res.Flags, FlagWriteOff) {
				t.Errorf("flags = %v: the write-off itself never stops being true", res.Flags)
			}
		})
	}
}

func TestTerminalCeilingsCanBeSwitchedOff(t *testing.T) {
	tests := []struct {
		name  string
		ago   int
		tweak func(*Config)
	}{
		{"the soft tier by its score", 1825, func(c *Config) { c.CapTerminalSoftScore = 0 }},
		{"the soft tier by its window", 1825, func(c *Config) { c.TerminalSoftLookbackMonths = 0 }},
		{"both tiers, recent debt", 300, func(c *Config) { c.CapTerminalDebt = false }},
		{"both tiers, aged debt", 1825, func(c *Config) { c.CapTerminalDebt = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.tweak(&cfg)
			if res := rateWith(writtenOff(tt.ago), uah(20000), cfg); res.Capped {
				t.Errorf("capped at %d by %q although switched off", res.Score, res.CapReason)
			}
		})
	}
}

// Live arrears outrank a past write-off: they are what the borrower can clear today.
func TestLiveArrearsOutrankAWriteOff(t *testing.T) {
	start := daysAgo(1800)
	res := rate(reportOf(dealsSection(
		loan(snap(daysAgo(300), start, ubki.StatusWrittenOff, 0, 0, 0)),
		loan(snap(daysAgo(10), start, ubki.StatusOpen, 30, uah(4000), uah(1500))),
	), inquiriesSection()), uah(20000))

	if res.CapReason != CapCurrentOverdue {
		t.Errorf("cap reason = %q, want %q", res.CapReason, CapCurrentOverdue)
	}
}

// The two hryvnia thresholds apply in order: OverdueIgnoreAmount decides
// whether an amount is arrears at all, CapMinOverdueAmount whether arrears are
// bad enough to cap. Lowering the second alone cannot revive a residue the
// first already discarded.
func TestOverdueThresholdsApplyInOrder(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CapMinOverdueAmount = 0
	if res := rateWith(overdueBorrower(uah(3)), uah(20000), cfg); res.Capped {
		t.Errorf("a 3 ₴ residue capped the rating at %d", res.Score)
	}

	cfg.OverdueIgnoreAmount = 0
	if res := rateWith(overdueBorrower(uah(3)), uah(20000), cfg); !res.Capped {
		t.Error("with both thresholds at zero, any arrears cap the rating")
	}
}

// With no credit deals there is nothing to rate, and a missing report reads the
// same as an empty one: both are shapes the app's type allows.
func TestNoCreditHistoryIsNotRated(t *testing.T) {
	for _, tt := range []struct {
		name   string
		report *ubki.Report
	}{
		{"no report", nil},
		{"a report without deals", reportOf(inquiriesSection())},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := Rate(Input{Report: tt.report, AsOf: testAsOf}, DefaultConfig())

			if res.Available || res.Reason != ReasonNoCreditHistory || res.Score != 0 {
				t.Errorf("available=%v reason=%q score=%d, want unavailable for %q",
					res.Available, res.Reason, res.Score, ReasonNoCreditHistory)
			}
			if res.AsOf != "2026-07-01" || res.Band != BandLow || res.BandLabel == "" {
				t.Errorf("as_of=%q band=%q label=%q, want the reference date and the low band", res.AsOf, res.Band, res.BandLabel)
			}
			if res.Parameters == nil || len(res.Parameters) != 0 {
				t.Errorf("parameters = %#v, want an empty list", res.Parameters)
			}
		})
	}
}

func TestScoreAndPointsStayInRange(t *testing.T) {
	for _, tt := range []struct {
		name   string
		report *ubki.Report
		income ubki.Money
	}{
		{"clean", cleanBorrower(), uah(20000)},
		{"overdue", overdueBorrower(uah(50000)), uah(5000)},
		{"no income", cleanBorrower(), 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := rate(tt.report, tt.income)
			if res.Score < 0 || res.Score > 100 {
				t.Errorf("score = %d", res.Score)
			}
			for _, p := range res.Parameters {
				if p.Points < 0 || p.Points > 100 {
					t.Errorf("%s: points = %v", p.Key, p.Points)
				}
			}
		})
	}
}

// The app renders these keys directly.
func TestResultJSONContract(t *testing.T) {
	raw, err := json.Marshal(rate(overdueBorrower(uah(5000)), uah(20000)))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"available", "as_of", "score", "raw", "band", "band_label",
		"capped", "cap_reason", "income_source", "mfo", "parameters",
	} {
		if _, ok := result[key]; !ok {
			t.Errorf("result has no %q", key)
		}
	}

	var params []map[string]json.RawMessage
	if err := json.Unmarshal(result["parameters"], &params); err != nil || len(params) != len(indicators) {
		t.Fatalf("parameters: %d entries (%v), want %d", len(params), err, len(indicators))
	}
	for _, key := range []string{
		"key", "order", "title", "value", "display", "level",
		"points", "weight", "contribution", "bands", "hint",
	} {
		if _, ok := params[0][key]; !ok {
			t.Errorf("parameter has no %q", key)
		}
	}
}
```

- [ ] **Step 10: Write the fixture tests**

Create `scoring/rating/fixtures_test.go`:

```go
package rating

import (
	"math"
	"testing"
	"time"
)

var fixtureAsOf = time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)

// What each real report says, and why. The golden payloads pin every byte;
// this table says which numbers matter and where they come from.
func TestFixtureRatings(t *testing.T) {
	tests := []struct {
		file      string
		why       string
		score     int
		band      Band
		capReason CapReason
		flags     []Flag
		levels    map[ParamKey]Level
		values    map[ParamKey]float64
		watchers  int // monitoring subscriptions on file
		watching  int // of which still running
	}{
		{
			file:  "report1.xml",
			why:   "a payday loan 90 days past due, plus a written-off consumer loan УБКІ never priced",
			score: 31, band: BandLow,
			flags: []Flag{FlagWriteOff},
			levels: map[ParamKey]Level{
				ParamHistoryAge: LevelMedium, ParamCleanStreak: LevelBad, ParamOverdueDeals: LevelBad,
				ParamOverdueAmount: LevelBad, ParamClosedDeals: LevelBad, ParamDebtLoad: LevelUnknown,
			},
			values: map[ParamKey]float64{
				// The write-off counts as a second overdue loan at 0 ₴: all three of
				// its snapshots report zero, so there was no amount to recover.
				ParamOverdueDeals: 2, ParamOverdueAmount: 1500, ParamActiveDeals: 2,
				ParamInquiries: 15, // exactly 15: mfo_pressure needs more
			},
			watchers: 11,
		},
		{
			file:  "report2.xml",
			why:   "a debt sold to a collector, priced by the indicators at the 1034.62 ₴ reported before the sale",
			score: 43, band: BandLow,
			flags: []Flag{FlagSold, FlagEnforcement, FlagMFOPressure},
			levels: map[ParamKey]Level{
				ParamHistoryAge: LevelGood, ParamCleanStreak: LevelBad, ParamOverdueDeals: LevelMedium,
				ParamOverdueAmount: LevelBad, ParamActiveDeals: LevelGood, ParamInquiries: LevelBad,
			},
			values: map[ParamKey]float64{
				ParamOverdueDeals: 1, ParamOverdueAmount: 1034.6, ParamActiveDeals: 1, ParamCleanStreak: 0,
				ParamInquiries: 17, // red, so mfo_pressure lights
			},
			watchers: 7,
		},
		{
			file:  "report3.xml",
			why:   "clean and current, two active cards, nothing repaid yet",
			score: 81, band: BandHigh,
			levels: map[ParamKey]Level{
				ParamHistoryAge: LevelGood, ParamCleanStreak: LevelGood, ParamOverdueDeals: LevelGood,
				ParamClosedDeals: LevelBad, ParamInquiries: LevelMedium,
			},
			watchers: 8,
		},
		{
			file:  "report4.xml",
			why:   "a thin file, current; the declared income makes debt load measurable",
			score: 62, band: BandMedium,
			flags: []Flag{FlagEnforcement},
			levels: map[ParamKey]Level{
				ParamHistoryAge: LevelBad, ParamCleanStreak: LevelMedium, ParamDebtLoad: LevelMedium,
			},
			watchers: 8,
		},
		{
			file:  "report5.xml",
			why:   "nine years of history but overdue right now; the card closed with no limit stays repaid",
			score: 45, band: BandLow, capReason: CapCurrentOverdue,
			levels: map[ParamKey]Level{
				ParamHistoryAge: LevelGood, ParamCleanStreak: LevelBad, ParamOverdueAmount: LevelBad,
				ParamClosedDeals: LevelMedium,
			},
			values:   map[ParamKey]float64{ParamActiveDeals: 1, ParamClosedDeals: 1},
			watchers: 6,
		},
		{
			// Synthetic: the branches no real report reaches. Two residues (7.40 ₴
			// and exactly 10.00 ₴) stay out of indicators 2–4; a card reported
			// closed with a 15 000 ₴ limit is among the five active loans; two live
			// microfinance loans light mfo_pressure while indicator 9 is green.
			file:  "report6.xml",
			why:   "the branches the real reports do not exercise",
			score: 80, band: BandHigh,
			flags: []Flag{FlagMFOPressure},
			levels: map[ParamKey]Level{
				ParamOverdueDeals: LevelGood, ParamOverdueAmount: LevelGood, ParamActiveDeals: LevelMedium,
				ParamInquiries: LevelGood,
			},
			values: map[ParamKey]float64{
				ParamOverdueDeals: 0, ParamOverdueAmount: 0, ParamActiveDeals: 5, ParamClosedDeals: 0,
				ParamCleanStreak: 1155, ParamInquiries: 3,
			},
			watchers: 3, watching: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			res := Rate(Input{Report: loadFixture(t, tt.file), AsOf: fixtureAsOf}, DefaultConfig())

			if res.Score != tt.score || res.Band != tt.band || res.CapReason != tt.capReason {
				t.Errorf("%d %s cap=%q, want %d %s cap=%q: %s",
					res.Score, res.Band, res.CapReason, tt.score, tt.band, tt.capReason, tt.why)
			}
			if len(res.Flags) != len(tt.flags) {
				t.Errorf("flags = %v, want %v", res.Flags, tt.flags)
			}
			for _, f := range tt.flags {
				if !hasFlag(res.Flags, f) {
					t.Errorf("flags = %v, missing %q", res.Flags, f)
				}
			}
			for key, want := range tt.levels {
				if p, _ := res.Parameter(key); p.Level != want {
					t.Errorf("%s: level %q (value %v), want %q", key, p.Level, p.Value, want)
				}
			}
			for key, want := range tt.values {
				if p, _ := res.Parameter(key); p.Value != want {
					t.Errorf("%s = %v, want %v", key, p.Value, want)
				}
			}
			watching := 0
			for _, m := range res.Monitoring {
				if m.Active {
					watching++
				}
			}
			if len(res.Monitoring) != tt.watchers || watching != tt.watching {
				t.Errorf("monitoring: %d entries, %d active; want %d and %d",
					len(res.Monitoring), watching, tt.watchers, tt.watching)
			}
		})
	}
}

// report2's debt was sold on 2025-04-03 and never repaid. Walking the report
// forward shows the whole retention policy: what the indicators can still claim
// and what the ceilings still say as the sale recedes. It steps down; without
// the soft tier it would jump from 45 to 90 overnight.
func TestSoldDebtAgesThroughTwoCeilings(t *testing.T) {
	tests := []struct {
		asOf      string
		score     int
		capReason CapReason
		overdue   float64 // indicator 3
	}{
		{"2026-07-08", 43, "", 1},                  // 15 months: arrears; already below the ceiling
		{"2027-07-08", 45, CapCurrentOverdue, 1},   // 27 months: still arrears, and now it caps
		{"2028-07-08", 74, CapTerminalDebtAged, 0}, // past 36 months: not arrears, not resolved
		{"2031-07-08", 90, "", 0},                  // past 72 months
		{"2035-07-08", 90, "", 0},                  // a decade on: nothing left but the flag
	}
	for _, tt := range tests {
		t.Run(tt.asOf, func(t *testing.T) {
			asOf, _ := time.Parse("2006-01-02", tt.asOf)
			res := Rate(Input{Report: loadFixture(t, "report2.xml"), AsOf: asOf}, DefaultConfig())

			if res.Score != tt.score || res.CapReason != tt.capReason {
				t.Errorf("score %d cap=%q, want %d cap=%q", res.Score, res.CapReason, tt.score, tt.capReason)
			}
			value := func(k ParamKey) float64 { p, _ := res.Parameter(k); return p.Value }
			if got := value(ParamOverdueDeals); got != tt.overdue {
				t.Errorf("overdue deals = %v, want %v", got, tt.overdue)
			}
			// The obligation itself never expires, and the sale never stops being true.
			if value(ParamActiveDeals) != 1 || value(ParamClosedDeals) != 0 || !hasFlag(res.Flags, FlagSold) {
				t.Errorf("active=%v closed=%v flags=%v, want the sold debt still open, unrepaid and flagged",
					value(ParamActiveDeals), value(ParamClosedDeals), res.Flags)
			}
			if streak := value(ParamCleanStreak); (tt.overdue > 0) != (streak == 0) {
				t.Errorf("clean streak = %v with %v overdue deals", streak, tt.overdue)
			}
		})
	}
}

// The promise the system rests on: the borrower adds up the nine numbers and
// gets the rating back. Nothing shown beside them may be part of the sum.
func TestReferenceBlocksNeverMoveTheScore(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			res := Rate(Input{Report: loadFixture(t, name), AsOf: fixtureAsOf}, DefaultConfig())

			sum := 0.0
			for _, p := range res.Parameters {
				sum += p.Contribution
			}
			if math.Abs(sum-res.Raw) > 0.05 {
				t.Errorf("contributions add up to %.2f, raw is %.2f", sum, res.Raw)
			}
			if !res.Capped && res.Score != int(math.Round(res.Raw)) {
				t.Errorf("score %d, but raw %.1f rounds to %d with no ceiling", res.Score, res.Raw, int(math.Round(res.Raw)))
			}

			// Without the monitoring section, only the block changes.
			blind := loadFixture(t, name)
			for i := range blind.Sections {
				blind.Sections[i].Monitoring = nil
			}
			if got := Rate(Input{Report: blind, AsOf: fixtureAsOf}, DefaultConfig()); got.Score != res.Score || got.Raw != res.Raw {
				t.Errorf("removing monitoring moved the rating from %v to %v", res.Raw, got.Raw)
			}

			// With mfo_pressure lit everywhere, only the flag changes.
			loud := DefaultConfig()
			loud.MFOPressureDeals = 0
			lit := Rate(Input{Report: loadFixture(t, name), AsOf: fixtureAsOf}, loud)
			if !hasFlag(lit.Flags, FlagMFOPressure) {
				t.Fatalf("flags = %v, want mfo_pressure lit", lit.Flags)
			}
			if lit.Score != res.Score || lit.Raw != res.Raw {
				t.Errorf("lighting mfo_pressure moved the rating from %v to %v", res.Raw, lit.Raw)
			}
		})
	}
}

// The lender breakdown never becomes a second version of indicator 9: it adds
// back up to it, and our own refresh pulls stay out of both.
func TestLenderBreakdownAgreesWithIndicatorNine(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			res := Rate(Input{Report: loadFixture(t, name), AsOf: fixtureAsOf}, DefaultConfig())
			p, _ := res.Parameter(ParamInquiries)
			if p.Level != LevelUnknown && float64(res.MFO.Inquiries6m) != p.Value {
				t.Errorf("breakdown says %d, indicator 9 says %v", res.MFO.Inquiries6m, p.Value)
			}
			sum := 0
			for org, n := range res.MFO.InquiriesByOrg {
				if org == "OWN" {
					t.Error("our own refresh pulls leaked into the breakdown")
				}
				sum += n
			}
			if sum != res.MFO.Inquiries6m {
				t.Errorf("per-lender tally adds up to %d, the total is %d", sum, res.MFO.Inquiries6m)
			}
		})
	}
}

// Every fixture scores without the app having to special-case it.
func TestFixturesProduceCoherentPayloads(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			res := Rate(Input{Report: loadFixture(t, name), AsOf: fixtureAsOf}, DefaultConfig())
			for _, p := range res.Parameters {
				switch {
				case p.Level == LevelUnknown && (p.Display != "" || p.Contribution != 0):
					t.Errorf("%s: unmeasured, yet shown or scored", p.Key)
				case p.Level != LevelUnknown && p.Display == "":
					t.Errorf("%s: measured, with nothing to show", p.Key)
				case p.Level == LevelGood && p.Advice != "":
					t.Errorf("%s: a green value carries advice", p.Key)
				}
			}
		})
	}
}
```

- [ ] **Step 11: Run them to see them fail**

Run: `go vet ./rating`
Expected: FAIL to build: `undefined: Rate`, `undefined: Input`, `undefined: measure`, ….

- [ ] **Step 12: Write the contract and configuration**

Create `scoring/rating/doc.go`:

```go
// Package rating computes the СФактор financial-health rating: a number from 0
// to 100 built from nine credit-history indicators, each of which the borrower
// can see, check against its published bands and add up by hand.
//
// Rate runs one pipeline over a УБКІ report. It classifies the borrower's own
// deals, measures the nine indicators, scores each on its curve and weight,
// caps the total while the borrower is in default, and attaches reference
// blocks — flags, a breakdown by type of lender, monitoring subscriptions —
// that are shown beside the rating and never enter it.
//
// The rating is a financial-literacy tool, not a credit decision: it has no
// knockout factors such as wanted lists, sanctions or bankruptcy. The model is
// specified in docs/specs/2026-08-07-fhp-9-parameter-rating-design.md
// and docs/specs/2026-08-24-ubki-rating-rules-design.md.
package rating
```

Create `scoring/rating/result.go`:

```go
package rating

// Result is the rating as the app renders it. The app does no arithmetic of
// its own: every number it shows is here. src/lib/rating.ts mirrors this shape
// field for field, so it is a contract.
type Result struct {
	// Available is false when there are no credit deals to rate. A rating
	// assembled from "no arrears, no debt, no applications" would be good news
	// the borrower has not earned.
	Available bool   `json:"available"`
	Reason    Reason `json:"reason,omitempty"`

	AsOf      string  `json:"as_of"` // YYYY-MM-DD
	Score     int     `json:"score"`
	Raw       float64 `json:"raw"` // the sum of the contributions, before any ceiling
	Band      Band    `json:"band"`
	BandLabel string  `json:"band_label"`

	Capped    bool      `json:"capped,omitempty"`
	CapReason CapReason `json:"cap_reason,omitempty"`

	// IncomeSource is where the debt-load denominator came from; empty when
	// debt load could not be measured.
	IncomeSource IncomeSource `json:"income_source,omitempty"`

	// Flags, MFO and Monitoring are shown beside the rating and never enter it.
	Flags      []Flag            `json:"flags,omitempty"`
	MFO        *MFOProfile       `json:"mfo,omitempty"`
	Monitoring []MonitoringEntry `json:"monitoring,omitempty"`

	Parameters []Parameter `json:"parameters"`
}

// Reason says why a rating is unavailable.
type Reason string

const ReasonNoCreditHistory Reason = "no_credit_history"

// CapReason names the ceiling that lowered the score.
type CapReason string

const (
	CapCurrentOverdue   CapReason = "current_overdue"
	CapTerminalDebt     CapReason = "terminal_debt"
	CapTerminalDebtAged CapReason = "terminal_debt_aged"
)

// IncomeSource is where the monthly income behind debt load came from.
type IncomeSource string

const (
	IncomeFromProfile IncomeSource = "profile" // verified, from the accounting system
	IncomeFromUBKI    IncomeSource = "ubki"    // self-declared, and recent enough to use
)

// Flag is a fact the borrower must be told that no indicator scores.
type Flag string

const (
	FlagWriteOff    Flag = "write_off"
	FlagSold        Flag = "sold"
	FlagEnforcement Flag = "enforcement"
	FlagMFOPressure Flag = "mfo_pressure"
)

// Parameter is one indicator as the borrower sees it.
type Parameter struct {
	Key   ParamKey `json:"key"`
	Order int      `json:"order"`
	Title string   `json:"title"`

	// Value is the measured number and Display the same number for the screen.
	// Both are meaningless when Level is LevelUnknown.
	Value   float64 `json:"value"`
	Display string  `json:"display"`
	Level   Level   `json:"level"`

	// Points is what the value earned on the indicator's curve, Weight its
	// configured percent, and Contribution its share of Raw once the weights
	// are renormalized over the measured indicators.
	Points       float64 `json:"points"`
	Weight       int     `json:"weight"`
	Contribution float64 `json:"contribution"`

	Bands  Bands  `json:"bands"`
	Hint   string `json:"hint"`
	Advice string `json:"advice,omitempty"`
}

// Bands are the printed value ranges behind each colour.
type Bands struct {
	Bad    string `json:"bad"`
	Medium string `json:"medium"`
	Good   string `json:"good"`
}

// MFOProfile breaks the borrower's file down by type of lender.
type MFOProfile struct {
	DealsByDonor   map[string]int `json:"deals_by_donor"`   // lender type → deals on file
	ActiveByDonor  map[string]int `json:"active_by_donor"`  // lender type → deals still open
	InquiriesByOrg map[string]int `json:"inquiries_by_org"` // lender type → applications in the window
	ActiveMFODeals int            `json:"active_mfo_deals"`
	Inquiries6m    int            `json:"inquiries_6m"` // indicator 9's own number
}

// MonitoringEntry is one creditor's subscription to updates about the borrower.
type MonitoringEntry struct {
	Org       string `json:"org"`
	OrgLabel  string `json:"org_label"`
	StartDate string `json:"start_date"` // YYYY-MM-DD, or empty when unknown
	EndDate   string `json:"end_date"`

	// Active means the subscription still runs on the reference date. One with
	// no end date on file reads as finished: claiming a watcher that cannot be
	// shown to exist is the worse error.
	Active bool `json:"active"`
}

// Parameter returns the indicator with the given key.
func (r Result) Parameter(k ParamKey) (Parameter, bool) {
	for _, p := range r.Parameters {
		if p.Key == k {
			return p, true
		}
	}
	return Parameter{}, false
}
```

Create `scoring/rating/config.go`:

```go
package rating

import "github.com/sfactor/scoring/ubki"

// Config holds the rating's thresholds and windows. Every value is an expert
// prior: there is no outcome data to fit them to yet, so they live here and a
// recalibration is one edit.
type Config struct {
	// OverdueIgnoreAmount is the overdue balance at or below which a deal is
	// not in arrears in any indicator: the rounding tail an early payoff leaves.
	OverdueIgnoreAmount ubki.Money

	// RecentWindowDays is "the last six months" for new deals and for credit
	// applications.
	RecentWindowDays int

	// TerminalLookbackMonths is how long a sold or written-off debt counts as
	// arrears today, and how long it holds the hard ceiling. The debt itself
	// stays open for as long as the bureau reports it.
	TerminalLookbackMonths int

	// CapScore is the hard ceiling, the top of the red band.
	CapScore int

	// CapMinOverdueAmount is the smallest overdue balance that triggers the hard
	// ceiling. OverdueIgnoreAmount has already removed residues from the
	// indicators by the time this applies.
	CapMinOverdueAmount ubki.Money

	// CapTerminalDebt switches both terminal-debt ceilings on.
	CapTerminalDebt bool

	// CapTerminalSoftScore is the ceiling for a terminal debt past
	// TerminalLookbackMonths but within TerminalSoftLookbackMonths: the top of
	// the middle band. A zero score or a zero window switches this tier off.
	CapTerminalSoftScore       int
	TerminalSoftLookbackMonths int

	// IncomeMaxAgeMonths is how old a self-declared УБКІ income may be and still
	// stand in for a missing verified one.
	IncomeMaxAgeMonths int

	// MFOPressureDeals is how many live microfinance loans light mfo_pressure.
	MFOPressureDeals int
}

// DefaultConfig returns the launch configuration.
func DefaultConfig() Config {
	return Config{
		OverdueIgnoreAmount:        10 * ubki.Hryvnia,
		RecentWindowDays:           183,
		TerminalLookbackMonths:     36, // SCHUFA's retention of negative data
		CapScore:                   45,
		CapMinOverdueAmount:        100 * ubki.Hryvnia,
		CapTerminalDebt:            true,
		CapTerminalSoftScore:       74,
		TerminalSoftLookbackMonths: 72, // the UK ICO's six years
		IncomeMaxAgeMonths:         12,
		MFOPressureDeals:           2,
	}
}
```

- [ ] **Step 13: Write the colour bands, curves and formatting**

Create `scoring/rating/bands.go`:

```go
package rating

// Level is the colour of one indicator's value. It comes from the value against
// the agreed bands, never from the points, so recalibrating a curve cannot
// change the colour a borrower was shown.
type Level string

const (
	LevelBad     Level = "bad"
	LevelMedium  Level = "medium"
	LevelGood    Level = "good"
	LevelUnknown Level = "unknown" // the indicator could not be measured
)

// bandRule colours a value.
type bandRule func(v float64) Level

// higherIsBetter colours an indicator where more is better: v ≤ badMax is bad,
// v ≤ mediumMax is medium, anything larger is good.
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

// lowerIsBetter colours an indicator where less is better: v < goodBelow is
// good, v ≤ mediumMax is medium, anything larger is bad.
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

// Band is the colour of the rating as a whole.
type Band string

const (
	BandLow    Band = "low"
	BandMedium Band = "medium"
	BandHigh   Band = "high"
)

// The bands of the final score. The low band ends at the hard ceiling and the
// medium band at the soft one, so each ceiling lands on the top of its colour.
const (
	mediumBandFrom = 46
	highBandFrom   = 75
)

// bandOf returns the band of a final score and its label.
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
```

Create `scoring/rating/curve.go`:

```go
package rating

// anchor is one point of a scoring curve: a value and the points it earns.
type anchor struct {
	value  float64
	points float64
}

// curve turns a value into 0–100 points by linear interpolation between anchors
// in ascending order of value. Beyond the first and the last anchor it is flat:
// those are the saturation points ("three years of history is full marks").
type curve []anchor

// eval returns the points a value earns.
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
```

Create `scoring/rating/format.go`:

```go
package rating

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// plural picks the Ukrainian plural form for n: one (1, 21, 31…), few (2–4,
// 22–24…) or many (0, 5–20, 25–30…). src/lib/rating.ts mirrors it.
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

// countOf writes a count with its noun: "3 кредити".
func countOf(n int, one, few, many string) string {
	return strconv.Itoa(n) + " " + plural(n, one, few, many)
}

// countDisplay formats a count of the given noun.
func countDisplay(one, few, many string) func(float64) string {
	return func(v float64) string {
		return countOf(int(math.Round(v)), one, few, many)
	}
}

// daysDisplay writes a number of days in the unit a person thinks in: days up
// to two months, then months, then years once "113 місяців" stops meaning
// anything. The exact day count stays visible.
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

// uahDisplay writes whole hryvnias with space-separated thousands: "1 250 ₴".
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

// percentDisplay writes a value that is already a percentage.
func percentDisplay(v float64) string { return fmt.Sprintf("%.0f%%", v) }
```

- [ ] **Step 14: Write the indicator table**

Create `scoring/rating/indicators.go`:

```go
package rating

// ParamKey names an indicator. Its value is the "key" the app renders against,
// so it is part of the JSON contract.
type ParamKey string

const (
	ParamHistoryAge    ParamKey = "history_age"
	ParamCleanStreak   ParamKey = "clean_streak"
	ParamOverdueDeals  ParamKey = "overdue_deals"
	ParamOverdueAmount ParamKey = "overdue_amount"
	ParamActiveDeals   ParamKey = "active_deals"
	ParamClosedDeals   ParamKey = "closed_deals"
	ParamNewDeals      ParamKey = "new_deals_6m"
	ParamDebtLoad      ParamKey = "debt_load"
	ParamInquiries     ParamKey = "inquiries_6m"
)

// indicator is everything fixed about one of the nine indicators.
type indicator struct {
	key    ParamKey
	title  string
	hint   string
	bands  Bands                // the printed ranges behind each colour
	level  bandRule             // the same ranges, as numbers
	curve  curve                // value → 0–100 points
	weight int                  // percent; the nine add up to 100
	advice advice               // what to do while the value is not green
	format func(float64) string // the value as the borrower reads it
}

// advice is the one concrete step the borrower can take. medium falls back to
// bad when one sentence fits both.
type advice struct {
	bad, medium string
}

// forLevel returns the advice for a colour. Green and unmeasured values get
// none: telling someone to fix what is fine, or what was not measured, is noise.
func (a advice) forLevel(l Level) string {
	switch l {
	case LevelBad:
		return a.bad
	case LevelMedium:
		if a.medium != "" {
			return a.medium
		}
		return a.bad
	default:
		return ""
	}
}

var credits = countDisplay("кредит", "кредити", "кредитів")

// indicators are the nine, in the order of the agreed table and of the screen.
// Band thresholds, curve anchors and weights are expert priors; see §4–§6 of
// docs/specs/2026-08-07-fhp-9-parameter-rating-design.md.
var indicators = []indicator{
	{
		key:    ParamHistoryAge,
		title:  "Вік кредитної історії",
		hint:   "Що довша історія, то більше довіри з боку кредиторів.",
		bands:  Bands{Bad: "0–180 днів", Medium: "181–360 днів", Good: "більше 360 днів"},
		level:  higherIsBetter(180, 360),
		curve:  curve{{0, 0}, {180, 33}, {360, 66}, {1095, 100}},
		weight: 8,
		advice: advice{
			bad: "Цей показник зростає сам із часом — його не можна прискорити, тільки не переривати.",
		},
		format: daysDisplay,
	},
	{
		key:    ParamCleanStreak,
		title:  "Платежі без прострочень",
		hint:   "Найсильніший сигнал: скільки днів поспіль ви платите вчасно.",
		bands:  Bands{Bad: "0–90 днів", Medium: "91–180 днів", Good: "більше 180 днів"},
		level:  higherIsBetter(90, 180),
		curve:  curve{{0, 0}, {90, 33}, {180, 66}, {730, 100}},
		weight: 18,
		advice: advice{
			bad:    "Кожен вчасний платіж подовжує серію. Три місяці поспіль без прострочень — і показник стане жовтим.",
			medium: "Ви вже близько: після 180 днів поспіль без прострочень показник стане зеленим.",
		},
		format: daysDisplay,
	},
	{
		key:    ParamOverdueDeals,
		title:  "Поточні прострочені кредити",
		hint:   "Кожен прострочений кредит бачить будь-який кредитор.",
		bands:  Bands{Bad: "2 і більше", Medium: "1", Good: "0"},
		level:  lowerIsBetter(1, 1),
		curve:  curve{{0, 100}, {1, 50}, {2, 20}, {3, 8}, {4, 0}},
		weight: 15,
		advice: advice{
			bad: "Погасіть прострочені кредити — це найшвидший спосіб підняти рейтинг.",
		},
		format: credits,
	},
	{
		key:    ParamOverdueAmount,
		title:  "Сума поточного прострочення",
		hint:   "Навіть невелика прострочена сума псує всю картину.",
		bands:  Bands{Bad: "більше 1000 ₴", Medium: "100–1000 ₴", Good: "менше 100 ₴"},
		level:  lowerIsBetter(100, 1000),
		curve:  curve{{0, 100}, {100, 67}, {1000, 34}, {10000, 0}},
		weight: 12,
		advice: advice{
			bad: "Внесіть прострочену суму. Щойно залишок стане меншим за 100 ₴, показник позеленіє.",
		},
		format: uahDisplay,
	},
	{
		key:    ParamActiveDeals,
		title:  "Діючі кредити",
		hint:   "Багато кредитів одночасно — це високе навантаження на бюджет.",
		bands:  Bands{Bad: "більше 5", Medium: "3–5", Good: "менше 3"},
		level:  lowerIsBetter(3, 5),
		curve:  curve{{0, 100}, {2, 70}, {3, 60}, {5, 40}, {6, 28}, {10, 0}},
		weight: 10,
		advice: advice{
			bad:    "Закрийте найменший кредит замість того, щоб брати новий — це знімає навантаження одразу.",
			medium: "Не беріть новий кредит, поки не закриєте хоча б один із діючих.",
		},
		format: credits,
	},
	{
		key:    ParamClosedDeals,
		title:  "Погашені кредити",
		hint:   "Закриті кредити доводять, що ви повертаєте борги.",
		bands:  Bands{Bad: "0", Medium: "1–3", Good: "більше 3"},
		level:  higherIsBetter(0, 3),
		curve:  curve{{0, 0}, {1, 40}, {3, 60}, {4, 75}, {6, 90}, {8, 100}},
		weight: 7,
		advice: advice{
			bad:    "У вас ще немає повністю погашених кредитів. Перший закритий кредит помітно підніме рейтинг.",
			medium: "Кожен повністю погашений кредит додає балів — доведіть поточні до кінця.",
		},
		format: credits,
	},
	{
		key:    ParamNewDeals,
		title:  "Нові кредити за 6 місяців",
		hint:   "Кілька нових кредитів поспіль читаються як фінансовий стрес.",
		bands:  Bands{Bad: "більше 5", Medium: "3–5", Good: "менше 3"},
		level:  lowerIsBetter(3, 5),
		curve:  curve{{0, 100}, {2, 70}, {3, 60}, {5, 40}, {6, 28}, {10, 0}},
		weight: 8,
		advice: advice{
			bad: "Зробіть паузу в нових кредитах: показник відновиться сам за 6 місяців без нових оформлень.",
		},
		format: credits,
	},
	{
		key:    ParamDebtLoad,
		title:  "Кредитне навантаження",
		hint:   "Скільки з вашого доходу вже забирають щомісячні платежі.",
		bands:  Bands{Bad: "більше 60%", Medium: "30–60%", Good: "менше 30%"},
		level:  lowerIsBetter(30, 60),
		curve:  curve{{0, 100}, {30, 67}, {60, 34}, {100, 0}},
		weight: 12,
		advice: advice{
			bad:    "Платежі забирають забагато від доходу. Допоможе дострокове погашення або відмова від нового кредиту.",
			medium: "Навантаження помітне. Дострокове погашення навіть невеликої суми зменшить щомісячний платіж.",
		},
		format: percentDisplay,
	},
	{
		key:    ParamInquiries,
		title:  "Звернення за кредитами",
		hint:   "Часті заявки на кредит виглядають як пошук грошей будь-де.",
		bands:  Bands{Bad: "більше 15", Medium: "5–15", Good: "менше 5"},
		level:  lowerIsBetter(5, 15),
		curve:  curve{{0, 100}, {5, 67}, {15, 34}, {30, 0}},
		weight: 10,
		advice: advice{
			bad: "Не подавайте заявки «про всяк випадок» — кожна з них видима кредиторам протягом 6 місяців.",
		},
		format: countDisplay("звернення", "звернення", "звернень"),
	},
}

// levelOf colours a value with the indicator's own rule. A rule that has to
// agree with the colour on screen asks for it instead of restating a threshold.
func levelOf(k ParamKey, v float64) Level {
	for _, in := range indicators {
		if in.key == k {
			return in.level(v)
		}
	}
	return LevelUnknown
}

// scoreIndicators turns the measured values into the nine indicators as the
// borrower sees them. An indicator missing from values is listed as unknown and
// contributes nothing; its weight is shared out over the measured ones, so a
// gap in the data never costs the borrower points.
func scoreIndicators(values map[ParamKey]float64) []Parameter {
	measuredWeight := 0
	for _, in := range indicators {
		if _, ok := values[in.key]; ok {
			measuredWeight += in.weight
		}
	}

	params := make([]Parameter, 0, len(indicators))
	for i, in := range indicators {
		p := Parameter{
			Key:    in.key,
			Order:  i + 1,
			Title:  in.title,
			Level:  LevelUnknown,
			Weight: in.weight,
			Bands:  in.bands,
			Hint:   in.hint,
		}
		if v, ok := values[in.key]; ok {
			p.Value = round1(v)
			p.Display = in.format(v)
			p.Level = in.level(v)
			p.Points = round1(in.curve.eval(v))
			p.Advice = in.advice.forLevel(p.Level)
			p.Contribution = round1(p.Points * float64(in.weight) / float64(measuredWeight))
		}
		params = append(params, p)
	}
	return params
}
```

- [ ] **Step 15: Write the deal classification**

Create `scoring/rating/deals.go`:

```go
package rating

import (
	"time"

	"github.com/sfactor/scoring/ubki"
)

// standing is what a deal counts as, read off its latest snapshot.
type standing int

const (
	// standingOther is neither live nor repaid: a deal closed after it was sold
	// or written off, or one whose status code the rating does not know.
	standingOther standing = iota

	// standingActive is a live obligation: open, restructured, or a credit card
	// whose latest snapshot still carries a limit.
	standingActive

	// standingTerminal is a debt the creditor gave up on — sold to a collector
	// or written off. The borrower still owes it, so it stays open and is never
	// repaid.
	standingTerminal

	// standingRepaid is a deal closed in the ordinary way.
	standingRepaid
)

// deal is one of the borrower's own deals, reduced to what the rating asks of it.
type deal struct {
	lenderType string
	history    []ubki.Snapshot
	latest     ubki.Snapshot
	started    time.Time // the earliest start date on file; zero when there is none
	standing   standing

	// terminal is when the creditor gave up on the deal, if it ever did — also
	// for a deal whose status has moved on since.
	terminal terminalEvent

	// terminalArrears is what a terminal deal is shown as owing: the last amount
	// the bureau reported before it zeroed the deal on sale or write-off. Zero
	// means the amount was never reported, not that little is left.
	terminalArrears ubki.Money
}

// open reports whether the deal is a live obligation.
func (d deal) open() bool {
	return d.standing == standingActive || d.standing == standingTerminal
}

// borrowerDeals classifies the deals in which the subject is the borrower.
// Guarantees are left out throughout: surety for someone else is not the
// subject's own credit behaviour.
func borrowerDeals(r *ubki.Report) []deal {
	var deals []deal
	for _, d := range r.Deals() {
		if d.Role != ubki.RoleGuarantor {
			deals = append(deals, classify(d))
		}
	}
	return deals
}

func classify(d ubki.Deal) deal {
	latest := d.Latest()
	c := deal{
		lenderType: d.LenderType,
		history:    d.History,
		latest:     latest,
		started:    earliestStart(d.History),
		terminal:   terminalEventOf(d.History),
	}

	switch latest.Status {
	case ubki.StatusOpen, ubki.StatusRestructured:
		c.standing = standingActive
	case ubki.StatusSold, ubki.StatusWrittenOff:
		// The creditor closing their books does not close the debt.
		c.standing = standingTerminal
		c.terminalArrears = lastReportedDebt(d.History, latest.ReportedOn)
	case ubki.StatusClosed:
		if !c.terminal.happened {
			c.standing = standingRepaid
		}
	}

	// A card's status describes the contract; its limit describes the money. A
	// card whose latest snapshot still carries a limit can be drawn on today,
	// whatever its status says. A sold card is a sold debt first.
	if c.standing != standingTerminal && d.Purpose == ubki.PurposeCreditCard && latest.Limit > 0 {
		c.standing = standingActive
	}
	return c
}

// earliestStart returns the earliest start date on file. The bureau repeats it
// on every snapshot, not always readably.
func earliestStart(history []ubki.Snapshot) time.Time {
	var first time.Time
	for _, s := range history {
		if t, ok := s.StartedOn.Time(); ok && (first.IsZero() || t.Before(first)) {
			first = t
		}
	}
	return first
}

// lastReportedDebt digs a terminal deal's debt back out of its history. УБКІ
// zeroes the overdue and outstanding amounts in the very snapshot that reports
// the sale or write-off, so that snapshot claims the debt is nothing. Up to it,
// the last reported arrears win, then the last reported balance.
func lastReportedDebt(history []ubki.Snapshot, terminalOn ubki.Date) ubki.Money {
	cutoff, dated := terminalOn.Time()
	var overdue, balance ubki.Money
	for _, s := range history {
		// Undated snapshots stay in: the bureau writes them in order, and
		// dropping one would lose the amount it carries.
		if at, ok := s.ReportedOn.Time(); ok && dated && at.After(cutoff) {
			continue
		}
		if s.Overdue > 0 {
			overdue = s.Overdue
		}
		if s.Balance > 0 {
			balance = s.Balance
		}
	}
	if overdue > 0 {
		return overdue
	}
	return balance
}

// terminalEvent is when a creditor gave up on a deal.
type terminalEvent struct {
	happened bool

	// at is the date of the most recent sold or written-off snapshot. It is
	// zero when any such snapshot is undated: the event happened, and nothing
	// shows that it is old.
	at time.Time
}

func terminalEventOf(history []ubki.Snapshot) terminalEvent {
	var e terminalEvent
	undated := false
	for _, s := range history {
		if !s.Status.Terminal() {
			continue
		}
		e.happened = true
		at, ok := s.ReportedOn.Time()
		switch {
		case !ok:
			undated = true
		case at.After(e.at):
			e.at = at
		}
	}
	if undated {
		e.at = time.Time{}
	}
	return e
}

// within reports whether the event happened no more than the given number of
// calendar months before asOf. An undated event always has.
func (e terminalEvent) within(asOf time.Time, months int) bool {
	if !e.happened {
		return false
	}
	return e.at.IsZero() || !e.at.Before(asOf.AddDate(0, -months, 0))
}

// overdueSnapshot reports whether a snapshot shows arrears worth the name: days
// past due, and more than a rounding tail. It is the one question behind
// indicators 3 and 4 and the clean streak, so the three cannot disagree.
func overdueSnapshot(s ubki.Snapshot, cfg Config) bool {
	return s.DaysOverdue > 0 && s.Overdue > cfg.OverdueIgnoreAmount
}

// arrears reports whether the deal is overdue on asOf, and by how much.
//
// An active deal is overdue when its latest snapshot is. A terminal deal is
// overdue by definition, even at zero, which there means "never reported"
// rather than "almost repaid" — but only for TerminalLookbackMonths. After that
// nobody has reported on the debt for years, "not paying it today" is no longer
// something the report shows, and holding the clean streak at zero for the
// decade the bureau keeps the record would leave the borrower no way back.
func (d deal) arrears(asOf time.Time, cfg Config) (bool, ubki.Money) {
	switch d.standing {
	case standingActive:
		if overdueSnapshot(d.latest, cfg) {
			return true, d.latest.Overdue
		}
	case standingTerminal:
		if d.terminal.within(asOf, cfg.TerminalLookbackMonths) {
			return true, d.terminalArrears
		}
	}
	return false, 0
}
```

- [ ] **Step 16: Write the measurements**

Create `scoring/rating/measure.go`:

```go
package rating

import (
	"math"
	"time"

	"github.com/sfactor/scoring/ubki"
)

// measurements is everything read off the report on the reference date.
type measurements struct {
	// values are the nine indicators. One that could not be measured is
	// missing, and its weight goes to the others instead of costing points.
	values map[ParamKey]float64

	book         portfolio
	applications []ubki.Inquiry // the credit applications indicator 9 counted
	incomeSource IncomeSource
}

// portfolio is what the borrower's deals add up to on the reference date.
type portfolio struct {
	open, repaid  int
	overdueDeals  int
	overdueAmount ubki.Money
	payments      ubki.Money // the scheduled monthly payments of the open deals
}

func measure(in Input, deals []deal, asOf time.Time, cfg Config) measurements {
	window := asOf.AddDate(0, 0, -cfg.RecentWindowDays)
	book := tally(deals, asOf, cfg)
	m := measurements{
		book: book,
		// With deals on file, "nothing overdue" is a fact, not a gap, so these
		// four are always measured.
		values: map[ParamKey]float64{
			ParamOverdueDeals:  float64(book.overdueDeals),
			ParamOverdueAmount: book.overdueAmount.Hryvnias(),
			ParamActiveDeals:   float64(book.open),
			ParamClosedDeals:   float64(book.repaid),
		},
	}

	// History age and new deals both read start dates, so they are measured
	// or missing together.
	first, known := firstCredit(deals)
	if known {
		m.values[ParamHistoryAge] = daysBetween(first, asOf)
		m.values[ParamNewDeals] = float64(startedWithin(deals, window, asOf))
	}
	if streak, ok := cleanStreak(deals, book.overdueDeals > 0, first, asOf, cfg); ok {
		m.values[ParamCleanStreak] = streak
	}
	if income, source := monthlyIncome(in, asOf, cfg); income > 0 {
		m.values[ParamDebtLoad] = debtLoad(in.Report, book, income)
		m.incomeSource = source
	}
	if apps, ok := creditApplications(in.Report, window, asOf); ok {
		m.values[ParamInquiries] = float64(len(apps))
		m.applications = apps
	}
	return m
}

// tally counts the open, repaid and overdue deals (indicators 3–6) and sums
// the scheduled payments of the open ones.
func tally(deals []deal, asOf time.Time, cfg Config) portfolio {
	var p portfolio
	for _, d := range deals {
		if d.standing == standingRepaid {
			p.repaid++
		}
		if !d.open() {
			continue
		}
		p.open++
		p.payments += d.latest.Payment
		if overdue, amount := d.arrears(asOf, cfg); overdue {
			p.overdueDeals++
			p.overdueAmount += amount
		}
	}
	return p
}

// firstCredit returns the earliest start date across the deals, and false when
// none of them has one.
func firstCredit(deals []deal) (time.Time, bool) {
	var first time.Time
	for _, d := range deals {
		if !d.started.IsZero() && (first.IsZero() || d.started.Before(first)) {
			first = d.started
		}
	}
	return first, !first.IsZero()
}

// startedWithin counts the deals that started between from and to, inclusive.
func startedWithin(deals []deal, from, to time.Time) int {
	n := 0
	for _, d := range deals {
		if !d.started.IsZero() && !d.started.Before(from) && !d.started.After(to) {
			n++
		}
	}
	return n
}

// cleanStreak is indicator 2: whole days since the borrower was last in
// arrears. Being overdue today makes it zero, because the latest monthly
// snapshot can be weeks old and "23 days clean" is nonsense for someone late
// right now. Never late, the streak is as long as the history.
func cleanStreak(deals []deal, overdueNow bool, firstCredit time.Time, asOf time.Time, cfg Config) (float64, bool) {
	if overdueNow {
		return 0, true
	}
	if last, ok := lastArrears(deals, cfg); ok {
		return daysBetween(last, asOf), true
	}
	if firstCredit.IsZero() {
		return 0, false
	}
	return daysBetween(firstCredit, asOf), true
}

// lastArrears returns the date of the most recent snapshot, on any deal, that
// shows arrears.
func lastArrears(deals []deal, cfg Config) (time.Time, bool) {
	var last time.Time
	for _, d := range deals {
		for _, s := range d.history {
			if at, ok := s.ReportedOn.Time(); ok && overdueSnapshot(s, cfg) && at.After(last) {
				last = at
			}
		}
	}
	return last, !last.IsZero()
}

// monthlyIncome is the debt-load denominator: the verified income from the
// accounting system, else the latest self-declared УБКІ income as long as it is
// no older than IncomeMaxAgeMonths, since a stale declaration flatters the
// load. Zero means there is no usable income.
func monthlyIncome(in Input, asOf time.Time, cfg Config) (ubki.Money, IncomeSource) {
	if in.MonthlyIncome > 0 {
		return in.MonthlyIncome, IncomeFromProfile
	}

	var income ubki.Money
	var declared time.Time
	for _, job := range in.Report.DeclaredEmployment() {
		on, ok := job.VerifiedOn.Time()
		if ok && job.MonthlyIncome > 0 && (income == 0 || on.After(declared)) {
			income, declared = job.MonthlyIncome, on
		}
	}
	if income == 0 || declared.Before(asOf.AddDate(0, -cfg.IncomeMaxAgeMonths, 0)) {
		return 0, ""
	}
	return income, IncomeFromUBKI
}

// debtLoad is indicator 8: monthly obligations as a percentage of monthly
// income. The bureau's own obligations total is preferred to the sum of
// scheduled payments, which understates the load because revolving lines often
// report no scheduled payment at all.
func debtLoad(r *ubki.Report, book portfolio, income ubki.Money) float64 {
	obligations := book.payments
	if total := r.MonthlyObligations(); total > 0 {
		obligations = total
	}
	return float64(obligations) / float64(income) * 100
}

// creditApplications returns the credit applications made between from and to,
// inclusive, and false when the report has no inquiry registry at all, which
// is "unknown" rather than "none".
func creditApplications(r *ubki.Report, from, to time.Time) ([]ubki.Inquiry, bool) {
	inquiries, ok := r.Inquiries()
	if !ok {
		return nil, false
	}
	var apps []ubki.Inquiry
	for _, q := range inquiries {
		if at, dated := q.Date.Time(); dated && isCreditApplication(q) && !at.Before(from) && !at.After(to) {
			apps = append(apps, q)
		}
	}
	return apps, true
}

// isCreditApplication reports whether an inquiry is the borrower asking for
// credit elsewhere. The app's own refresh pulls (OWN), identity checks and
// limit reviews are not.
func isCreditApplication(q ubki.Inquiry) bool {
	if q.Requester == ubki.CreditorOwn || q.ReportType == ubki.ReportTypeIdentity {
		return false
	}
	return q.Reason == ubki.ReasonCredit || q.Reason == ubki.ReasonOnlineCredit
}

// daysBetween returns the whole days from a to b, and zero when b is earlier.
func daysBetween(a, b time.Time) float64 {
	return max(0, math.Floor(b.Sub(a).Hours()/24))
}
```

- [ ] **Step 17: Write the ceilings and the reference blocks**

Create `scoring/rating/ceilings.go`:

```go
package rating

import "time"

// ceiling caps the final score, with the reason the borrower is given.
type ceiling struct {
	score  int
	reason CapReason
}

// ceilingFor returns the ceiling that applies, strictest first. Without them
// the weighted average would show a borrower in default as healthy.
//
//   - current_overdue: a deal is overdue today by at least CapMinOverdueAmount.
//     It outranks the others because it is the one the borrower can clear today.
//   - terminal_debt: a debt was sold or written off within
//     TerminalLookbackMonths. It is the backstop for a debt whose amount the
//     bureau never reported: indicator 4 then honestly shows 0 ₴, which can
//     never reach CapMinOverdueAmount.
//   - terminal_debt_aged: the debt is older than that but within
//     TerminalSoftLookbackMonths, and unresolved. The report can no longer say
//     the borrower is not paying, nor that they paid: not red, not clear.
//     Without this tier the rating would jump some forty points on the day the
//     hard window closes, on no new information.
func ceilingFor(deals []deal, book portfolio, asOf time.Time, cfg Config) (ceiling, bool) {
	if book.overdueDeals > 0 && book.overdueAmount >= cfg.CapMinOverdueAmount {
		return ceiling{cfg.CapScore, CapCurrentOverdue}, true
	}
	if !cfg.CapTerminalDebt {
		return ceiling{}, false
	}
	if anyTerminalWithin(deals, asOf, cfg.TerminalLookbackMonths) {
		return ceiling{cfg.CapScore, CapTerminalDebt}, true
	}
	if cfg.CapTerminalSoftScore > 0 && cfg.TerminalSoftLookbackMonths > 0 &&
		anyTerminalWithin(deals, asOf, cfg.TerminalSoftLookbackMonths) {
		return ceiling{cfg.CapTerminalSoftScore, CapTerminalDebtAged}, true
	}
	return ceiling{}, false
}

func anyTerminalWithin(deals []deal, asOf time.Time, months int) bool {
	for _, d := range deals {
		if d.terminal.within(asOf, months) {
			return true
		}
	}
	return false
}
```

Create `scoring/rating/reference.go`:

```go
package rating

import (
	"slices"
	"time"

	"github.com/sfactor/scoring/ubki"
)

// The blocks in this file are shown beside the rating and never enter it: the
// nine contributions add up to Raw whether they are present or not.

// flagsFor collects what no indicator scores but the borrower must be told: a
// debt that was ever written off or sold, an open enforcement proceeding, and
// the signs of chasing money wherever it is offered.
func flagsFor(r *ubki.Report, deals []deal, values map[ParamKey]float64, activeMFODeals int, cfg Config) []Flag {
	var sold, writtenOff bool
	for _, d := range deals {
		for _, s := range d.history {
			sold = sold || s.Status == ubki.StatusSold
			writtenOff = writtenOff || s.Status == ubki.StatusWrittenOff
		}
	}

	var flags []Flag
	if writtenOff {
		flags = append(flags, FlagWriteOff)
	}
	if sold {
		flags = append(flags, FlagSold)
	}
	if r.ActiveEnforcements() > 0 {
		flags = append(flags, FlagEnforcement)
	}
	if mfoPressure(values, activeMFODeals, cfg) {
		flags = append(flags, FlagMFOPressure)
	}
	return flags
}

// mfoPressure reports applying for credit far more often than the table calls
// normal, or stacking microfinance loans. The first half is "indicator 9 is
// red" rather than a threshold of its own, so the flag and the colour beside it
// cannot tell different stories.
func mfoPressure(values map[ParamKey]float64, activeMFODeals int, cfg Config) bool {
	if n, ok := values[ParamInquiries]; ok && levelOf(ParamInquiries, n) == LevelBad {
		return true
	}
	return activeMFODeals >= cfg.MFOPressureDeals
}

// lenderBreakdown splits the file by type of lender: who the borrower owes, who
// they still borrow from, and whom they asked for credit lately. It counts the
// same deals and applications as the indicators, so the block and the table can
// never show two numbers for one thing.
func lenderBreakdown(deals []deal, applications []ubki.Inquiry) *MFOProfile {
	p := &MFOProfile{
		DealsByDonor:   map[string]int{},
		ActiveByDonor:  map[string]int{},
		InquiriesByOrg: map[string]int{},
		Inquiries6m:    len(applications),
	}
	for _, d := range deals {
		if d.lenderType == "" {
			continue // an unattributed deal would render as a blank lender row
		}
		p.DealsByDonor[d.lenderType]++
		if !d.open() {
			continue
		}
		p.ActiveByDonor[d.lenderType]++
		if d.lenderType == ubki.CreditorMFO {
			p.ActiveMFODeals++
		}
	}
	for _, q := range applications {
		if q.Requester != "" {
			p.InquiriesByOrg[q.Requester]++
		}
	}
	return p
}

// subscriberLabels name the subscriber types for the screen. An unmapped code
// is shown as itself: a blank would read as a bug.
var subscriberLabels = map[string]string{
	ubki.CreditorBank:    "Банк",
	ubki.CreditorMFO:     "МФО",
	ubki.CreditorFinance: "Фінкомпанія",
	ubki.CreditorBureau:  "Бюро",
}

// monitoringEntries lists every subscription, newest first, finished ones
// included: on all five real reports none is still running, and leaving them
// out would hide that eleven companies have been watching.
func monitoringEntries(subs []ubki.Subscription, asOf time.Time) []MonitoringEntry {
	if len(subs) == 0 {
		return nil
	}
	subs = slices.Clone(subs)
	slices.SortStableFunc(subs, newestFirst)

	entries := make([]MonitoringEntry, 0, len(subs))
	for _, s := range subs {
		label, ok := subscriberLabels[s.Subscriber]
		if !ok {
			label = s.Subscriber
		}
		end, known := s.End.Time()
		entries = append(entries, MonitoringEntry{
			Org:       s.Subscriber,
			OrgLabel:  label,
			StartDate: s.Start.String(),
			EndDate:   s.End.String(),
			Active:    known && !end.Before(asOf),
		})
	}
	return entries
}

// newestFirst orders subscriptions by start date, latest first. An unknown
// start is not a recent one, so those sink to the bottom.
func newestFirst(a, b ubki.Subscription) int {
	at, aKnown := a.Start.Time()
	bt, bKnown := b.Start.Time()
	switch {
	case aKnown && bKnown:
		return bt.Compare(at)
	case aKnown:
		return -1
	case bKnown:
		return 1
	default:
		return 0
	}
}
```

- [ ] **Step 18: Write the pipeline**

Create `scoring/rating/rate.go`:

```go
package rating

import (
	"math"
	"time"

	"github.com/sfactor/scoring/ubki"
)

// Input is everything one rating needs.
type Input struct {
	// Report is the borrower's УБКІ credit report.
	Report *ubki.Report

	// MonthlyIncome is the verified income from the accounting system's client
	// profile. Zero means unknown: debt load then falls back to a recent
	// self-declared income, or goes unmeasured.
	MonthlyIncome ubki.Money

	// AsOf is the reference date. Zero means the report's own build date.
	AsOf time.Time
}

// dateLayout is how the result writes dates.
const dateLayout = "2006-01-02"

// Rate computes the rating.
func Rate(in Input, cfg Config) Result {
	asOf := referenceDate(in)
	if in.Report == nil {
		return unavailable(asOf)
	}
	deals := borrowerDeals(in.Report)
	if len(deals) == 0 {
		return unavailable(asOf)
	}

	m := measure(in, deals, asOf, cfg)
	params := scoreIndicators(m.values)
	raw := sumContributions(params)
	mfo := lenderBreakdown(deals, m.applications)

	res := Result{
		Available:    true,
		AsOf:         asOf.Format(dateLayout),
		Score:        int(math.Round(clamp(raw, 0, 100))),
		Raw:          raw,
		IncomeSource: m.incomeSource,
		Flags:        flagsFor(in.Report, deals, m.values, mfo.ActiveMFODeals, cfg),
		MFO:          mfo,
		Monitoring:   monitoringEntries(in.Report.Monitoring(), asOf),
		Parameters:   params,
	}
	// A ceiling only ever lowers a score. A file already below it is left alone
	// and not labelled capped, because nothing was.
	if c, ok := ceilingFor(deals, m.book, asOf, cfg); ok && res.Score > c.score {
		res.Score = c.score
		res.Capped = true
		res.CapReason = c.reason
	}
	res.Band, res.BandLabel = bandOf(res.Score)
	return res
}

// referenceDate is the calendar day the rating describes: the one asked for,
// else the day the bureau built the report, else today.
func referenceDate(in Input) time.Time {
	t := in.AsOf
	if t.IsZero() {
		built, ok := in.Report.BuiltAt()
		if !ok {
			built = time.Now()
		}
		t = built
	}
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// unavailable is the result for a borrower with no credit history to rate.
func unavailable(asOf time.Time) Result {
	band, label := bandOf(0)
	return Result{
		Reason:     ReasonNoCreditHistory,
		AsOf:       asOf.Format(dateLayout),
		Band:       band,
		BandLabel:  label,
		Parameters: []Parameter{},
	}
}

// sumContributions adds up the contributions. The rating is the sum of the
// numbers the borrower can see, not a figure computed separately that they
// would then fail to reproduce.
func sumContributions(params []Parameter) float64 {
	sum := 0.0
	for _, p := range params {
		sum += p.Contribution
	}
	return round1(sum)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func clamp(v, lo, hi float64) float64 { return math.Min(hi, math.Max(lo, v)) }
```

- [ ] **Step 19: Run the rating tests**

Run: `go vet ./rating ./ubki && go test ./rating ./ubki -race -count=1`
Expected: `ok` for both. The golden payloads are byte-identical to Task 1's.

- [ ] **Step 20: Give the CLI its final form**

Replace `scoring/cmd/score/main.go`:

```go
// Command score rates a УБКІ XML report and prints the result as JSON, or as a
// table to read by eye.
//
//	score report.xml
//	score -income 25000 report.xml      verified monthly income, in hryvnias
//	score -as-of 2026-07-08 report.xml  reference date (default: the report's build date)
//	score -table report.xml
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sfactor/scoring/rating"
	"github.com/sfactor/scoring/ubki"
)

const usage = "usage: score [-income UAH] [-as-of YYYY-MM-DD] [-table] report.xml"

var errUsage = errors.New(usage)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "score:", err)
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("score", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	income := flags.String("income", "", "verified monthly income in hryvnias, from the client profile")
	asOf := flags.String("as-of", "", "reference date YYYY-MM-DD (default: the report's build date)")
	table := flags.Bool("table", false, "print a table instead of JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		return errUsage
	}

	var in rating.Input
	if *income != "" {
		m, err := ubki.ParseMoney(*income)
		if err != nil || m < 0 {
			return fmt.Errorf("-income %q is not a non-negative amount of hryvnias", *income)
		}
		in.MonthlyIncome = m
	}
	if *asOf != "" {
		t, err := time.Parse(time.DateOnly, *asOf)
		if err != nil {
			return fmt.Errorf("-as-of %q is not a YYYY-MM-DD date", *asOf)
		}
		in.AsOf = t
	}

	data, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		return err
	}
	if in.Report, err = ubki.Parse(data); err != nil {
		return err
	}

	res := rating.Rate(in, rating.DefaultConfig())
	if *table {
		_, err := io.WriteString(stdout, formatTable(res))
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

// formatTable renders the rating the way the borrower sees it.
func formatTable(res rating.Result) string {
	var b strings.Builder
	line := func(format string, args ...any) { b.WriteString(fmt.Sprintf(format, args...) + "\n") }

	if !res.Available {
		line("Рейтинг недоступний: %s", res.Reason)
		return b.String()
	}

	line("Рейтинг: %d/100 — %s (на %s)", res.Score, res.BandLabel, res.AsOf)
	if res.Capped {
		line("Обмежено до %d: %s", res.Score, res.CapReason)
	}
	if len(res.Flags) > 0 {
		flags := make([]string, len(res.Flags))
		for i, f := range res.Flags {
			flags[i] = string(f)
		}
		line("Увага: %s", strings.Join(flags, ", "))
	}
	line("")

	rule := strings.Repeat("-", 86)
	line("%-32s %-22s %-8s %6s %5s %7s", "Показник", "Значення", "Рівень", "Бали", "Вага", "Внесок")
	line("%s", rule)
	for _, p := range res.Parameters {
		value := p.Display
		if p.Level == rating.LevelUnknown {
			value = "немає даних"
		}
		line("%-32s %-22s %-8s %6.1f %4d%% %7.1f", p.Title, value, levelLabel(p.Level), p.Points, p.Weight, p.Contribution)
	}
	line("%s", rule)
	line("%-72s %7.1f", "Разом", res.Raw)
	return b.String()
}

func levelLabel(l rating.Level) string {
	switch l {
	case rating.LevelGood:
		return "добре"
	case rating.LevelMedium:
		return "середнє"
	case rating.LevelBad:
		return "погано"
	default:
		return "—"
	}
}
```

Create `scoring/cmd/score/main_test.go`:

```go
package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var report3 = filepath.Join("..", "..", "rating", "testdata", "report3.xml")

// The CLI prints exactly the pinned payload: it is how src/lib/ratingDemo.ts
// was produced, and how it would be produced again.
func TestJSONOutputIsThePinnedPayload(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-income", "24000", "-as-of", "2026-07-08", report3}, &out); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "rating", "testdata", "golden", "report3_income_24000.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("output differs from the golden payload:\n%s", out.String())
	}
}

func TestTableOutput(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-table", "-income", "24000", "-as-of", "2026-07-08", report3}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Рейтинг: 83/100 — Високий (на 2026-07-08)",
		"Вік кредитної історії",
		"Разом",
		"83.3",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table is missing %q:\n%s", want, out.String())
		}
	}
}

func TestBadInvocations(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		usage bool
	}{
		{"no report", nil, true},
		{"two reports", []string{report3, report3}, true},
		{"an unknown flag", []string{"-verbose", report3}, true},
		{"an income that is not an amount", []string{"-income", "lots", report3}, false},
		{"a negative income", []string{"-income", "-1", report3}, false},
		{"a date in the wrong form", []string{"-as-of", "08.07.2026", report3}, false},
		{"a missing file", []string{"no-such-report.xml"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args, &bytes.Buffer{})
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := errors.Is(err, errUsage); got != tt.usage {
				t.Errorf("usage error = %v, want %v (%v)", got, tt.usage, err)
			}
		})
	}
}
```

- [ ] **Step 21: Adapt the server handler just enough to compile**

Replace `scoring/cmd/server/handler.go` (a stopgap: the query-string API is unchanged, `AsOf` moves into `Input`, income becomes `ubki.Money`; Task 4 deletes this file):

```go
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/sfactor/scoring/rating"
	"github.com/sfactor/scoring/ubki"
)

// maxReportBytes bounds the request body. Real УБКІ reports run to tens of
// kilobytes; anything near this limit is a mistake or an attack.
const maxReportBytes = 4 << 20

// ratingHandler scores a УБКІ XML report posted as the request body.
type ratingHandler struct {
	corsOrigin string
}

// apiError is the error envelope. It never echoes report content back.
type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (h *ratingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.corsOrigin != "" {
		w.Header().Set("Access-Control-Allow-Origin", h.corsOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST with the УБКІ XML report as the body.")
		return
	}

	asOf, err := asOfFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	income, err := incomeFromQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxReportBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "report_too_large", "The report exceeds the size limit.")
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty_body", "Post the УБКІ XML report as the request body.")
		return
	}

	report, err := ubki.Parse(body)
	if err != nil {
		// The parse error can quote report content, so it is logged, not returned.
		log.Printf("rating: parse report: %v", err)
		writeError(w, http.StatusBadRequest, "invalid_report", "The report could not be parsed as УБКІ XML.")
		return
	}

	in := rating.Input{Report: report, MonthlyIncome: income, AsOf: asOf}
	writeJSON(w, http.StatusOK, rating.Rate(in, rating.DefaultConfig()))
}

// asOfFromQuery reads the optional reference date.
func asOfFromQuery(r *http.Request) (time.Time, error) {
	raw := r.URL.Query().Get("as_of")
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, errInvalidParam("as_of must be a date in YYYY-MM-DD form")
	}
	return t, nil
}

// incomeFromQuery reads the verified monthly income. Absent means "not
// supplied", which drops the debt-load indicator rather than scoring it badly.
func incomeFromQuery(r *http.Request) (ubki.Money, error) {
	raw := r.URL.Query().Get("income")
	if raw == "" {
		return 0, nil
	}
	income, err := ubki.ParseMoney(raw)
	if err != nil || income < 0 {
		return 0, errInvalidParam("income must be a non-negative number of UAH")
	}
	return income, nil
}

type errInvalidParam string

func (e errInvalidParam) Error() string { return string(e) }

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("rating: encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Error: code, Message: message})
}
```

- [ ] **Step 22: Run everything**

Run: `go vet ./... && go test ./... -race -count=1 && golangci-lint run ./...`
Expected: every package `ok`; `0 issues.`

- [ ] **Step 23: Commit**

```bash
git add -A scoring
git commit -F - <<'EOF'
refactor: the rating as a pipeline you can read top to bottom

Rate now reads as its steps: classify the borrower's deals, measure the nine
indicators, score them, apply at most one ceiling, attach the reference blocks.
Each step is a function returning a value, instead of one struct filled by six
methods in an order only a comment enforced. Each indicator is one table entry,
weight and advice included, and a deal's state is one enum instead of three
booleans. AsOf moves from Config to Input: it describes the request.

Fixed on the way, each with a test that fails without the fix: the reference
date is a calendar day (the report's build time of day made a subscription
ending on that day read as finished); money is summed in kopiykas (10.14 +
58.12 + 31.74 ₴ summed to 99.999999999999986 and missed the 100 ₴ ceiling);
windows are calendar months; one terminal date per deal feeds both the arrears
rule and the ceilings; a missing report and an empty one return one shape.

The golden payloads are byte-identical to the previous commit's.
EOF
```

- [ ] **Step 24: Prove each fix test fails without its fix**

For each mutation: apply it, run the named test and see it FAIL with the quoted message, then restore with `git checkout -- <file>`.

1. Reference date keeps the time of day. In `scoring/rating/rate.go`, `referenceDate`, replace the last two lines with `return t`.
   Run: `go test ./rating -run TestReportsOwnDateIsACalendarDay -count=1`
   Expected: FAIL `a subscription ending on the build date reads as finished on that date`.
2. 30.44-day months. In `scoring/rating/deals.go`, `within`, replace the return with
   `return e.at.IsZero() || asOf.Sub(e.at).Hours()/24/30.44 <= float64(months)`.
   Run: `go test ./rating -run 'TestTerminalWindowIsCalendarMonths|TestGoldenPayloads' -count=1`
   Expected: FAIL `exactly 36 months later the sale is still within 36 months`; the golden payloads still pass (no fixture sits on this edge).
3. Two terminal dates. In `scoring/rating/deals.go`, `arrears`, replace the `standingTerminal` case body with
   `at, _ := d.latest.ReportedOn.Time(); if (terminalEvent{happened: true, at: at}).within(asOf, cfg.TerminalLookbackMonths) { return true, d.terminalArrears }`.
   Run: `go test ./rating -run TestUndatedTerminalSnapshotMakesTheEventRecent -count=1`
   Expected: FAIL `the arrears rule must agree with the ceilings about the same sale`.
4. Two unavailable shapes. In `scoring/rating/rate.go`, `Rate`, make the nil-report branch `return Result{Reason: ReasonNoCreditHistory, Parameters: []Parameter{}}`.
   Run: `go test ./rating -run TestNoCreditHistoryIsNotRated -count=1`
   Expected: FAIL `as_of="" band="" label="", want the reference date and the low band`.
5. Float money. In `scoring/rating/measure.go`, `tally`, sum overdue amounts as `hryvnias += amount.Hryvnias()` and set `p.overdueAmount = ubki.Money(hryvnias * 100)` after the loop.
   Run: `go test ./rating -run 'TestOverdueAmountIsSummedExactly|TestArrearsOfExactlyTheThresholdCapTheRating' -count=1`
   Expected: FAIL `overdue amount = 9999 kopiykas, want exactly 10000` and `cap reason = "", want "current_overdue"`.

Finally `git status --short` is empty and `go test ./... -count=1` is `ok`.

---

### Task 4: `POST /rating` takes the XML file and the borrower's data as multipart

**Files:**
- Create: `scoring/internal/httpapi/httpapi.go`, `httpapi_test.go`
- Replace: `scoring/cmd/server/main.go`; Create: `scoring/cmd/server/main_test.go`
- Delete: `scoring/cmd/server/handler.go`, `handler_test.go`

- [ ] **Step 1: Write the API tests**

Create `scoring/internal/httpapi/httpapi_test.go`:

```go
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "rating", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(data)
}

// part is one field of a multipart body.
type part struct {
	name, value string
	file        bool
}

func report(xml string) part        { return part{name: "report", value: xml, file: true} }
func field(name, value string) part { return part{name: name, value: value} }

func form(t *testing.T, parts ...part) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range parts {
		var w io.Writer
		var err error
		if p.file {
			w, err = mw.CreateFormFile(p.name, "report.xml")
		} else {
			w, err = mw.CreateFormField(p.name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, p.value); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, mw.FormDataContentType()
}

func post(t *testing.T, h http.Handler, parts ...part) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := form(t, parts...)
	req := httptest.NewRequest(http.MethodPost, "/rating", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rec.Body)
	}
	return out
}

// The response is the pinned payload the app renders, not a lookalike.
func TestRateReturnsTheAppsPayload(t *testing.T) {
	rec := post(t, New(Options{}),
		report(fixture(t, "report3.xml")),
		field("monthly_income", "24000"),
		field("as_of", "2026-07-08"),
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
	var want map[string]any
	golden, err := os.ReadFile(filepath.Join("..", "..", "rating", "testdata", "golden", "report3_income_24000.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatal(err)
	}
	if got := decode(t, rec); !reflect.DeepEqual(got, want) {
		t.Errorf("payload differs from rating/testdata/golden/report3_income_24000.json:\n%s", rec.Body)
	}
	for header, want := range map[string]string{
		"Content-Type":  "application/json; charset=utf-8",
		"Cache-Control": "no-store",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// Without the optional fields the report's own build date is the reference
// date and debt load goes unmeasured; an empty field reads as an absent one.
func TestOptionalFieldsMayBeAbsentOrEmpty(t *testing.T) {
	for _, parts := range [][]part{
		{report(fixture(t, "report3.xml"))},
		{report(fixture(t, "report3.xml")), field("monthly_income", ""), field("as_of", " ")},
	} {
		rec := post(t, New(Options{}), parts...)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body)
		}
		got := decode(t, rec)
		if got["as_of"] != "2026-07-08" || got["score"] != float64(81) {
			t.Errorf("as_of = %v, score = %v; want the build date 2026-07-08 and 81", got["as_of"], got["score"])
		}
		if _, ok := got["income_source"]; ok {
			t.Errorf("income_source = %v with no income supplied", got["income_source"])
		}
	}
}

func TestRejectsBadRequests(t *testing.T) {
	valid := report(fixture(t, "report3.xml"))
	tests := []struct {
		name   string
		parts  []part
		status int
		code   string
	}{
		{"no report part", []part{field("monthly_income", "25000")}, 400, "missing_report"},
		{"an empty report", []part{report("")}, 400, "missing_report"},
		{"broken XML", []part{report("<ubkidata><comp")}, 400, "invalid_report"},
		{"not a УБКІ document", []part{report("<report/>")}, 400, "invalid_report"},
		{"an unknown part", []part{valid, field("income", "25000")}, 400, "invalid_request"},
		{"a repeated part", []part{valid, field("as_of", "2026-07-08"), field("as_of", "2026-07-09")}, 400, "invalid_request"},
		{"an income that is not a number", []part{valid, field("monthly_income", "lots")}, 400, "invalid_request"},
		{"a negative income", []part{valid, field("monthly_income", "-5")}, 400, "invalid_request"},
		{"an infinite income", []part{valid, field("monthly_income", "Inf")}, 400, "invalid_request"},
		{"a NaN income", []part{valid, field("monthly_income", "NaN")}, 400, "invalid_request"},
		{"a date in the wrong form", []part{valid, field("as_of", "08.07.2026")}, 400, "invalid_request"},
		{"an overlong field", []part{valid, field("as_of", strings.Repeat("2", 65))}, 400, "invalid_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := post(t, New(Options{}), tt.parts...)
			if rec.Code != tt.status {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tt.status, rec.Body)
			}
			if got := decode(t, rec); got["error"] != tt.code || got["message"] == "" {
				t.Errorf("error = %v (%v), want %q with a message", got["error"], got["message"], tt.code)
			}
		})
	}
}

func TestRejectsBodiesThatAreNotMultipart(t *testing.T) {
	for _, contentType := range []string{"", "application/xml", "application/json"} {
		req := httptest.NewRequest(http.MethodPost, "/rating", strings.NewReader(fixture(t, "report3.xml")))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		New(Options{}).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnsupportedMediaType || decode(t, rec)["error"] != "unsupported_media_type" {
			t.Errorf("Content-Type %q: status %d (body: %s), want 415", contentType, rec.Code, rec.Body)
		}
	}
}

func TestMalformedMultipartIsABadRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/rating", strings.NewReader("--other\r\n\r\nnot a part"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=expected")
	rec := httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || decode(t, rec)["error"] != "invalid_request" {
		t.Errorf("status %d (body: %s), want 400 invalid_request", rec.Code, rec.Body)
	}
}

func TestOversizedBodyIs413(t *testing.T) {
	rec := post(t, New(Options{}), report(strings.Repeat("x", maxRequestBytes)))

	if rec.Code != http.StatusRequestEntityTooLarge || decode(t, rec)["error"] != "report_too_large" {
		t.Errorf("status %d (body: %s), want 413 report_too_large", rec.Code, rec.Body)
	}
}

// failingReader stands in for a client that drops the connection mid-upload.
type failingReader struct{ r io.Reader }

func (f *failingReader) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, errors.New("connection reset by peer")
	}
	return n, err
}

// A body that fails to arrive is the caller's broken request, not a large one.
func TestReadFailureIsNotReportedAsTooLarge(t *testing.T) {
	body, contentType := form(t, report(fixture(t, "report3.xml")))
	truncated := bytes.NewReader(body.Bytes()[:body.Len()/2])
	req := httptest.NewRequest(http.MethodPost, "/rating", &failingReader{truncated})
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || decode(t, rec)["error"] != "invalid_request" {
		t.Errorf("status %d (body: %s), want 400 invalid_request", rec.Code, rec.Body)
	}
}

// A parse failure can quote the report; the response must not carry any of it.
func TestParseFailureDoesNotEchoTheReport(t *testing.T) {
	rec := post(t, New(Options{}), report(`<ubkidata><cki inn="1234567890" lname="Петренко"`))

	if body := rec.Body.String(); strings.Contains(body, "1234567890") || strings.Contains(body, "Петренко") {
		t.Errorf("the error response echoed the report: %s", body)
	}
}

// A report whose amounts read "NaN" still produces a payload that encodes.
func TestNonFiniteAmountsStillEncode(t *testing.T) {
	poisoned := strings.Replace(fixture(t, "report3.xml"), `dlamtpaym="`, `dlamtpaym="NaN`, 1)
	rec := post(t, New(Options{}), report(poisoned))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rec.Code, rec.Body)
	}
	decode(t, rec)
}

func TestOnlyPostRates(t *testing.T) {
	rec := httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/rating", nil))

	if rec.Code != http.StatusMethodNotAllowed || decode(t, rec)["error"] != "method_not_allowed" {
		t.Errorf("status %d (body: %s), want 405 method_not_allowed", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Allow"); got != "POST, OPTIONS" {
		t.Errorf("Allow = %q", got)
	}
}

func TestCORS(t *testing.T) {
	const origin = "https://app.example"

	preflight := httptest.NewRecorder()
	New(Options{CORSOrigin: origin}).ServeHTTP(preflight, httptest.NewRequest(http.MethodOptions, "/rating", nil))
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Errorf("preflight: status %d, origin %q; want 204 and %q",
			preflight.Code, preflight.Header().Get("Access-Control-Allow-Origin"), origin)
	}

	if got := post(t, New(Options{CORSOrigin: origin}), report(fixture(t, "report3.xml"))).Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("POST with CORS on: Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
	if got := post(t, New(Options{}), report(fixture(t, "report3.xml"))).Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("POST with CORS off: Access-Control-Allow-Origin = %q, want none", got)
	}
}

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	New(Options{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK || decode(t, rec)["status"] != "ok" {
		t.Errorf("status %d (body: %s), want 200 {\"status\":\"ok\"}", rec.Code, rec.Body)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/httpapi -count=1`
Expected: FAIL to build: `undefined: New`, `undefined: Options`, `undefined: maxRequestBytes`.

- [ ] **Step 3: Write the handler**

Create `scoring/internal/httpapi/httpapi.go`:

```go
// Package httpapi serves the rating over HTTP:
//
//	POST /rating  multipart/form-data: report (the УБКІ XML), monthly_income, as_of
//	GET  /health
//
// api/openapi.yaml is the contract. The service is unauthenticated by design:
// it sits behind the gateway, which identifies callers.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/sfactor/scoring/rating"
	"github.com/sfactor/scoring/ubki"
)

// maxRequestBytes bounds the request body. Real reports run to tens of
// kilobytes; a body near this size is a mistake or an attack.
const maxRequestBytes = 4 << 20

// maxFieldBytes bounds each form field other than the report.
const maxFieldBytes = 64

// The parts of a POST /rating body.
const (
	fieldReport        = "report"
	fieldMonthlyIncome = "monthly_income"
	fieldAsOf          = "as_of"
)

// Options configure the handler.
type Options struct {
	// CORSOrigin is the one browser origin allowed to call the API directly.
	// Empty sends no CORS headers.
	CORSOrigin string

	// Logger receives what callers are not told, such as why a report did not
	// parse. Nil discards it.
	Logger *slog.Logger
}

// New returns the API's handler.
func New(opts Options) http.Handler {
	s := &server{logger: opts.Logger, cfg: rating.DefaultConfig()}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /rating", s.rate)
	mux.HandleFunc("OPTIONS /rating", preflight)
	mux.HandleFunc("/rating", s.methodNotAllowed)
	mux.HandleFunc("GET /health", s.health)
	return withCORS(opts.CORSOrigin, mux)
}

type server struct {
	logger *slog.Logger
	cfg    rating.Config
}

func (s *server) rate(w http.ResponseWriter, r *http.Request) {
	req, apiErr := readRequest(w, r)
	if apiErr != nil {
		s.writeError(w, apiErr)
		return
	}
	report, err := ubki.Parse(req.report)
	if err != nil {
		// The parser's message can quote the report, so only the log sees it.
		s.logger.Warn("report does not parse", "error", err)
		s.writeError(w, &apiError{http.StatusBadRequest, "invalid_report", "The report is not УБКІ XML."})
		return
	}

	res := rating.Rate(rating.Input{Report: report, MonthlyIncome: req.monthlyIncome, AsOf: req.asOf}, s.cfg)
	w.Header().Set("Cache-Control", "no-store") // a credit rating must not sit in a shared cache
	s.writeJSON(w, http.StatusOK, res)
}

// request is a POST /rating body, read and checked.
type request struct {
	report        []byte
	monthlyIncome ubki.Money
	asOf          time.Time
}

// readRequest reads the multipart body part by part. Failures say what the
// caller sent wrong, never what the report contains.
func readRequest(w http.ResponseWriter, r *http.Request) (request, *apiError) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		return request{}, &apiError{http.StatusUnsupportedMediaType, "unsupported_media_type",
			`Send multipart/form-data with the report in a part named "report".`}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	parts, err := r.MultipartReader()
	if err != nil {
		return request{}, invalidRequest("The body is not valid multipart/form-data.")
	}

	var req request
	seen := make(map[string]bool)
	for {
		part, err := parts.NextPart()
		// A bare io.EOF is the closing boundary. A body that ends without one
		// comes back wrapped, and is malformed rather than finished.
		if err == io.EOF {
			break
		}
		if err != nil {
			return request{}, bodyError(err)
		}
		name := part.FormName()
		if seen[name] {
			return request{}, invalidRequest(fmt.Sprintf("The %q part is repeated.", name))
		}
		seen[name] = true
		if err := req.read(name, part); err != nil {
			return request{}, bodyError(err)
		}
	}
	if len(req.report) == 0 {
		return request{}, &apiError{http.StatusBadRequest, "missing_report",
			`Attach the УБКІ XML report in a part named "report".`}
	}
	return req, nil
}

// read takes in one part. An empty optional field reads as an absent one.
func (req *request) read(name string, part io.Reader) error {
	switch name {
	case fieldReport:
		report, err := io.ReadAll(part)
		req.report = report
		return err

	case fieldMonthlyIncome:
		raw, err := readField(part)
		if err != nil || raw == "" {
			return err
		}
		income, err := ubki.ParseMoney(raw)
		if err != nil || income < 0 {
			return invalidRequest("monthly_income must be a non-negative amount of hryvnias, such as 25000 or 25000.50.")
		}
		req.monthlyIncome = income

	case fieldAsOf:
		raw, err := readField(part)
		if err != nil || raw == "" {
			return err
		}
		asOf, err := time.Parse(time.DateOnly, raw)
		if err != nil {
			return invalidRequest("as_of must be a date in YYYY-MM-DD form.")
		}
		req.asOf = asOf

	default:
		return invalidRequest(fmt.Sprintf("Unknown part %q: send report, and optionally monthly_income and as_of.", name))
	}
	return nil
}

// readField reads a short form field and trims it.
func readField(part io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(part, maxFieldBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxFieldBytes {
		return "", invalidRequest(fmt.Sprintf("Form fields other than report are at most %d bytes.", maxFieldBytes))
	}
	return strings.TrimSpace(string(b)), nil
}

// apiError is a failure the caller is told about.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.code + ": " + e.message }

func invalidRequest(message string) *apiError {
	return &apiError{http.StatusBadRequest, "invalid_request", message}
}

// bodyError says what went wrong while reading the body. Only an oversized body
// is a 413; any other read failure is a malformed request.
func bodyError(err error) *apiError {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return &apiError{http.StatusRequestEntityTooLarge, "report_too_large", "The request is larger than 4 MiB."}
	}
	return invalidRequest("The body could not be read as multipart/form-data.")
}

func (s *server) methodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "POST, OPTIONS")
	s.writeError(w, &apiError{http.StatusMethodNotAllowed, "method_not_allowed", "Use POST with a multipart/form-data body."})
}

// preflight answers a CORS preflight; withCORS supplies the headers.
func preflight(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// errorBody is every error response.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func (s *server) writeError(w http.ResponseWriter, e *apiError) {
	s.writeJSON(w, e.status, errorBody{Error: e.code, Message: e.message})
}

func (s *server) writeJSON(w http.ResponseWriter, status int, body any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.logger.Error("write response", "error", err)
	}
}

// withCORS allows the one configured browser origin, or adds nothing.
func withCORS(origin string, next http.Handler) http.Handler {
	if origin == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type")
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Run the API tests**

Run: `go test ./internal/httpapi -race -count=1`
Expected: `ok`.

- [ ] **Step 5: Replace the server's wiring, test first**

```bash
git rm -q scoring/cmd/server/handler.go scoring/cmd/server/handler_test.go
```

Create `scoring/cmd/server/main_test.go`:

```go
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/sfactor/scoring/internal/httpapi"
)

// The server answers until it is told to stop, then stops cleanly: a clean
// stop is what lets a container orchestrator restart it without alarms.
func TestServeAnswersUntilItsContextEnds(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() {
		stopped <- serve(ctx, ln, httpapi.New(httpapi.Options{}), slog.New(slog.DiscardHandler))
	}()

	resp, err := http.Get("http://" + ln.Addr().String() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("serve returned %v after its context ended, want a clean stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop after its context ended")
	}
}

func TestRunReportsAPortInUse(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = taken.Close() })

	if err := run(taken.Addr().String(), httpapi.Options{}, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("run on a port already in use returned no error")
	}
}
```

Run: `go test ./cmd/server -count=1`
Expected: FAIL to build: `undefined: ratingHandler` (the old `main.go` still wires the deleted handler) and `undefined: serve`.

Replace `scoring/cmd/server/main.go`:

```go
// Command server serves the СФактор financial-health rating over HTTP. The
// routes are in internal/httpapi and the contract in api/openapi.yaml.
//
//	server -port 8080 -cors-origin https://app.example
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sfactor/scoring/internal/httpapi"
)

// shutdownGrace bounds how long requests in flight may finish after SIGTERM.
const shutdownGrace = 30 * time.Second

func main() {
	port := flag.String("port", "8080", "port to listen on")
	corsOrigin := flag.String("cors-origin", "", "the one browser origin allowed by CORS; empty sends no CORS headers")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	opts := httpapi.Options{CORSOrigin: *corsOrigin, Logger: logger}
	if err := run(":"+*port, opts, logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// run listens on addr and serves until SIGINT or SIGTERM.
func run(addr string, opts httpapi.Options, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return serve(ctx, ln, httpapi.New(opts), logger)
}

// serve answers requests on ln until ctx is done, then drains the ones in
// flight, so a container stop does not hand a caller a reset connection.
func serve(ctx context.Context, ln net.Listener, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	failed := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", ln.Addr().String())
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	}()

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
```

- [ ] **Step 6: Run everything**

Run: `go vet ./... && go test ./... -race -count=1 && golangci-lint run ./...`
Expected: every package `ok`; `0 issues.`

- [ ] **Step 7: Call the real server**

```bash
tmp=$(mktemp -d)
go build -o "$tmp/server" ./cmd/server
"$tmp/server" -port 18080 &
curl -sS -F report=@rating/testdata/report3.xml -F monthly_income=24000 -D - http://localhost:18080/rating -o "$tmp/rating.json"
python3 -c "import json,sys; print(json.load(open(sys.argv[1])) == json.load(open(sys.argv[2])))" "$tmp/rating.json" rating/testdata/golden/report3_income_24000.json
curl -sS -F report=@rating/testdata/report3.xml -F income=24000 http://localhost:18080/rating
curl -sS http://localhost:18080/health
kill %1
```

Expected: `200 OK` with `Cache-Control: no-store`; `True` (the response is the golden payload, score 83); the third call answers `{"error":"invalid_request","message":"Unknown part \"income\": …"}`; health is `{"status":"ok"}`.

- [ ] **Step 8: Commit**

```bash
git add -A scoring
git commit -m "feat: POST /rating takes the УБКІ XML file and the borrower's data as multipart"
```

---

### Task 5: The OpenAPI contract, kept honest by a test

**Files:**
- Create: `scoring/api/openapi.yaml`
- Test: `scoring/internal/httpapi/openapi_test.go`

- [ ] **Step 1: Write the test**

Create `scoring/internal/httpapi/openapi_test.go`:

```go
package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dataKeyed are the payload objects whose keys are data (lender codes), not
// fields of the contract.
var dataKeyed = map[string]bool{"deals_by_donor": true, "active_by_donor": true, "inquiries_by_org": true}

// collectKeys adds every object key in a decoded JSON value to keys.
func collectKeys(v any, keys map[string]bool) {
	switch v := v.(type) {
	case map[string]any:
		for k, child := range v {
			keys[k] = true
			if !dataKeyed[k] {
				collectKeys(child, keys)
			}
		}
	case []any:
		for _, child := range v {
			collectKeys(child, keys)
		}
	}
}

// api/openapi.yaml is what a caller reads, so it must not fall behind the code:
// every key a pinned payload carries, and every error code, is documented.
func TestOpenAPIDocumentsTheWholeContract(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	documented := make(map[string]bool)
	for _, m := range regexp.MustCompile(`(?m)^\s+([a-z0-9_]+):`).FindAllStringSubmatch(string(spec), -1) {
		documented[m[1]] = true
	}

	goldens, err := filepath.Glob(filepath.Join("..", "..", "rating", "testdata", "golden", "*.json"))
	if err != nil || len(goldens) == 0 {
		t.Fatalf("no golden payloads found: %v", err)
	}
	keys := make(map[string]bool)
	for _, path := range goldens {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var payload any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		collectKeys(payload, keys)
	}
	for key := range keys {
		if !documented[key] {
			t.Errorf("the payload carries %q, which api/openapi.yaml does not document", key)
		}
	}

	for _, code := range []string{
		"invalid_request", "missing_report", "invalid_report",
		"method_not_allowed", "report_too_large", "unsupported_media_type",
	} {
		if !strings.Contains(string(spec), code) {
			t.Errorf("error code %q is not documented", code)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/httpapi -run TestOpenAPIDocumentsTheWholeContract -count=1`
Expected: FAIL `open ../../api/openapi.yaml: no such file or directory`.

- [ ] **Step 3: Write the contract**

Create `scoring/api/openapi.yaml`:

```yaml
openapi: 3.0.3
info:
  title: СФактор rating engine
  version: 1.0.0
  description: |
    Turns a borrower's УБКІ credit report, and the monthly income the
    accounting system verified, into the financial-health rating the СФактор
    app renders. The response is the `Rating` object of `src/lib/rating.ts`,
    field for field: the app does no arithmetic of its own.

    The service is unauthenticated by design. It sits behind the gateway,
    which identifies callers; do not expose it to the internet on its own.
servers:
  - url: http://localhost:8080
paths:
  /rating:
    post:
      operationId: rate
      summary: Rate a УБКІ report
      requestBody:
        required: true
        content:
          multipart/form-data:
            schema:
              type: object
              required: [report]
              additionalProperties: false
              properties:
                report:
                  type: string
                  format: binary
                  description: The УБКІ XML report, template 10, UTF-8.
                monthly_income:
                  type: string
                  example: '25000.50'
                  description: |
                    Verified monthly income in hryvnias, as a decimal, rounded
                    to the kopiyka. Absent, empty or `0`: debt load falls back to
                    a self-declared УБКІ income no older than 12 months
                    (`income_source: ubki`), and failing that is left
                    unmeasured, its weight going to the other indicators.
                as_of:
                  type: string
                  format: date
                  example: '2026-07-08'
                  description: |
                    Reference date. Absent or empty: the report's own build
                    date. It does not rewind the report — snapshots after it
                    still count — so use it for reproducibility and ageing,
                    not to look back.
            encoding:
              report:
                contentType: application/xml, text/xml, application/octet-stream
      responses:
        '200':
          description: The rating, exactly as the app renders it.
          headers:
            Cache-Control:
              schema:
                type: string
                enum: [no-store]
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Rating'
        '400':
          description: |
            `invalid_request`: malformed multipart; an unknown or repeated part;
            a bad `monthly_income` or `as_of` (the message names it).
            `missing_report`: no `report` part, or an empty one.
            `invalid_report`: the report is not УБКІ XML.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Error'
        '405':
          description: '`method_not_allowed`: any method but POST and OPTIONS.'
          headers:
            Allow:
              schema:
                type: string
                enum: ['POST, OPTIONS']
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Error'
        '413':
          description: '`report_too_large`: the request body exceeds 4 MiB.'
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Error'
        '415':
          description: '`unsupported_media_type`: the body is not multipart/form-data.'
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Error'
    options:
      operationId: ratePreflight
      summary: CORS preflight
      description: Answered when the server runs with `-cors-origin`, which names the one browser origin allowed.
      responses:
        '204':
          description: Preflight accepted.
  /health:
    get:
      operationId: health
      summary: Liveness
      responses:
        '200':
          description: The server is up.
          content:
            application/json:
              schema:
                type: object
                required: [status]
                properties:
                  status:
                    type: string
                    enum: [ok]
components:
  schemas:
    Rating:
      type: object
      required: [available, as_of, score, raw, band, band_label, parameters]
      properties:
        available:
          type: boolean
          description: False when there are no credit deals to rate.
        reason:
          type: string
          enum: [no_credit_history]
          description: Why the rating is unavailable. Present only then.
        as_of:
          type: string
          format: date
          description: The reference date.
        score:
          type: integer
          minimum: 0
          maximum: 100
          description: The rating, after any ceiling. Zero when unavailable.
        raw:
          type: number
          description: The sum of every contribution, before any ceiling.
        band:
          type: string
          enum: [high, medium, low]
          description: 75–100 high, 46–74 medium, 0–45 low.
        band_label:
          type: string
          example: Високий
        capped:
          type: boolean
          description: Present, and true, only when a ceiling lowered the score.
        cap_reason:
          type: string
          enum: [current_overdue, terminal_debt, terminal_debt_aged]
        income_source:
          type: string
          enum: [profile, ubki]
          description: Where debt load's income came from. Absent when debt load is unmeasured.
        flags:
          type: array
          description: Facts shown beside the rating that never move it.
          items:
            type: string
            enum: [write_off, sold, enforcement, mfo_pressure]
        mfo:
          $ref: '#/components/schemas/MFOProfile'
        monitoring:
          type: array
          description: Every creditor subscription on file, newest first, finished ones included.
          items:
            $ref: '#/components/schemas/MonitoringEntry'
        parameters:
          type: array
          description: The nine indicators in display order. Empty when unavailable.
          items:
            $ref: '#/components/schemas/Parameter'
    Parameter:
      type: object
      required: [key, order, title, value, display, level, points, weight, contribution, bands, hint]
      properties:
        key:
          type: string
          enum:
            - history_age
            - clean_streak
            - overdue_deals
            - overdue_amount
            - active_deals
            - closed_deals
            - new_deals_6m
            - debt_load
            - inquiries_6m
        order:
          type: integer
          minimum: 1
          maximum: 9
        title:
          type: string
        value:
          type: number
          description: The measured value, rounded to 0.1. Meaningless when level is unknown.
        display:
          type: string
          description: The value as the borrower reads it. Empty when level is unknown.
        level:
          type: string
          enum: [good, medium, bad, unknown]
          description: The colour, from the value against the bands; unknown when unmeasured.
        points:
          type: number
          minimum: 0
          maximum: 100
        weight:
          type: integer
          description: Configured weight, in percent; the nine add up to 100.
        contribution:
          type: number
          description: Points × weight ÷ the weight of the measured indicators. These add up to raw.
        bands:
          $ref: '#/components/schemas/Bands'
        hint:
          type: string
        advice:
          type: string
          description: One step the borrower can take. Absent for green and unmeasured values.
    Bands:
      type: object
      required: [bad, medium, good]
      description: The printed value ranges behind each colour.
      properties:
        bad:
          type: string
        medium:
          type: string
        good:
          type: string
    MFOProfile:
      type: object
      description: The file by type of lender (BNK, MFO, FIN). Reference only.
      required: [deals_by_donor, active_by_donor, inquiries_by_org, active_mfo_deals, inquiries_6m]
      properties:
        deals_by_donor:
          type: object
          additionalProperties:
            type: integer
        active_by_donor:
          type: object
          additionalProperties:
            type: integer
        inquiries_by_org:
          type: object
          description: Credit applications in the last six months; never OWN.
          additionalProperties:
            type: integer
        active_mfo_deals:
          type: integer
        inquiries_6m:
          type: integer
          description: The inquiries_6m indicator's own number.
    MonitoringEntry:
      type: object
      required: [org, org_label, start_date, end_date, active]
      properties:
        org:
          type: string
        org_label:
          type: string
          example: Банк
        start_date:
          type: string
          description: YYYY-MM-DD, or empty when unknown.
        end_date:
          type: string
          description: YYYY-MM-DD, or empty when unknown.
        active:
          type: boolean
          description: Still running on as_of.
    Error:
      type: object
      required: [error, message]
      properties:
        error:
          type: string
          enum:
            - invalid_request
            - missing_report
            - invalid_report
            - method_not_allowed
            - report_too_large
            - unsupported_media_type
        message:
          type: string
          description: An English sentence for the developer. It never quotes the report.
```

- [ ] **Step 4: Run it**

Run: `go test ./internal/httpapi -count=1`
Expected: `ok`.

Run: `python3 -c "import yaml; yaml.safe_load(open('api/openapi.yaml'))"`
Expected: no output (the document parses).

- [ ] **Step 5: Commit**

```bash
git add scoring/api scoring/internal/httpapi/openapi_test.go
git commit -m "docs: an OpenAPI contract for the rating call, checked against the pinned payloads"
```

---

### Task 6: The README says what the engine is, not how it got here

**Files:**
- Replace: `scoring/README.md`
- Modify: `docs/specs/2026-09-28-scoring-engine-cleanup-design.md` (§7.2, row 2)

- [ ] **Step 1: Replace the README**

Replace `scoring/README.md`:

````markdown
# СФактор rating engine

The borrower's **financial-health rating**: a number from 0 to 100 built from
nine credit-history indicators that the borrower can see, check against
published bands and add up by hand. Input is the УБКІ credit report (XML
template 10) plus the monthly income the accounting system verified. The
output is the JSON the app renders as is — `Rating` in `src/lib/rating.ts` —
so the app does no arithmetic of its own.

Specs: [the nine indicators](../docs/specs/2026-08-07-fhp-9-parameter-rating-design.md),
[which deals count](../docs/specs/2026-08-24-ubki-rating-rules-design.md),
[this layout and API](../docs/specs/2026-09-28-scoring-engine-cleanup-design.md).

## Layout

```
ubki/              the part of the УБКІ report the rating reads, parsed leniently
rating/            the rating: classify deals → measure → score → ceilings → reference blocks
internal/httpapi/  POST /rating, GET /health
cmd/server/        the HTTP server
cmd/score/         the CLI: a report in, JSON or a table out
api/openapi.yaml   the HTTP contract
```

`rating/rate.go` reads top to bottom as the pipeline; each indicator is one entry
in `rating/indicators.go`; every threshold and window is in `rating/config.go`.

## Run

```bash
go test ./...                                      # unit, fixture and golden tests
go run ./cmd/score -table rating/testdata/report1.xml
go run ./cmd/score -income 25000 -as-of 2026-07-08 report.xml
go run ./cmd/server -port 8080
```

## API

### `POST /rating`

A `multipart/form-data` body of at most 4 MiB:

| Part | | |
|---|---|---|
| `report` | required | The УБКІ XML report |
| `monthly_income` | optional | Verified monthly income in hryvnias (`25000`, `25000.50`). Absent: debt load uses a self-declared УБКІ income up to 12 months old, or goes unmeasured |
| `as_of` | optional | Reference date `YYYY-MM-DD`. Default: the report's build date |

```bash
curl -F report=@report.xml -F monthly_income=25000 http://localhost:8080/rating
```

```js
const form = new FormData();
form.set('report', new Blob([reportXml], { type: 'application/xml' }), 'report.xml');
form.set('monthly_income', '25000');
const res = await fetch(`${SCORING_URL}/rating`, { method: 'POST', body: form });
const body = await res.json(); // res.ok ? Rating (src/lib/rating.ts) : { error, message }
```

`200` is the rating, with `Cache-Control: no-store`. Errors are
`{"error": code, "message": sentence}` and never quote the report:

| Status | `error` | |
|---|---|---|
| 400 | `invalid_request` | Malformed multipart; an unknown or repeated part; a bad `monthly_income` or `as_of` |
| 400 | `missing_report` | No `report` part, or an empty one |
| 400 | `invalid_report` | The report is not УБКІ XML |
| 405 | `method_not_allowed` | Not POST |
| 413 | `report_too_large` | Over 4 MiB |
| 415 | `unsupported_media_type` | Not `multipart/form-data` |

`GET /health` answers `{"status":"ok"}`. The full schema is
[`api/openapi.yaml`](api/openapi.yaml), and a test keeps it in step with the code.

## The nine indicators

| # | Indicator | 🔴 | 🟡 | 🟢 | Weight |
|---|-----------|----|----|----|------:|
| 1 | Days since the first credit | 0–180 | 181–360 | >360 | 8% |
| 2 | Days paid without arrears | 0–90 | 91–180 | >180 | 18% |
| 3 | Loans past due now | >1 | 1 | 0 | 15% |
| 4 | Amount past due now, ₴ | >1000 | 100–1000 | <100 | 12% |
| 5 | Active loans | >5 | 3–5 | <3 | 10% |
| 6 | Repaid loans | 0 | 1–3 | >3 | 7% |
| 7 | New loans in 6 months | >5 | 3–5 | <3 | 8% |
| 8 | Debt load, payments ÷ income | >60% | 30–60% | <30% | 12% |
| 9 | Credit applications in 6 months | >15 | 5–15 | <5 | 10% |

- **Points** come from a piecewise-linear curve per indicator, so the rating moves
  every month instead of jumping between three steps.
- **Colour** comes from the value against the bands, never from the points: a
  recalibrated curve cannot change the colour a borrower was shown.
- **`score = Σ contribution`**, where `contribution = points × weight ÷ Σ weight`
  over the indicators that could be measured. A gap in the data redistributes
  weight instead of costing points.
- **Bands of the total:** 0–45 Низький, 46–74 Середній, 75–100 Високий.
- No deals at all: `available: false`. There is nothing to rate.

## Which deals count, and as what

Guarantees are left out: surety for someone else is not the borrower's own
behaviour. Each remaining deal gets one standing from its latest snapshot:

| Standing | When | Active (5) | Repaid (6) |
|---|---|---|---|
| active | open or restructured; or a credit card whose latest snapshot still carries a limit | yes | no |
| terminal | sold to a collector (3) or written off (13) | yes | no |
| repaid | closed (2), never sold or written off | no | yes |
| other | closed after a sale or write-off; unknown codes | no | no |

- **Arrears** (indicators 3, 4 and the streak of indicator 2) means days past due
  *and* more than `OverdueIgnoreAmount` (10 ₴): the tail of an early payoff is not
  a default. Indicator 4 sums exactly the deals indicator 3 counted.
- **A terminal debt is arrears** at the last amount the bureau reported before it
  zeroed the deal on sale, and even at 0 ₴ when it never reported one — for
  `TerminalLookbackMonths` (36) after the sale. The debt stays active and unrepaid
  for as long as the bureau carries it.
- Money is whole kopiykas and windows are calendar months, so every band edge
  is exact.

## Ceilings

| `cap_reason` | When | Cap |
|---|---|--:|
| `current_overdue` | arrears of at least `CapMinOverdueAmount` (100 ₴) today | 45 |
| `terminal_debt` | a debt sold or written off in the last 36 months | 45 |
| `terminal_debt_aged` | …in the last 72 months, and never resolved | 74 |

Strictest first; a ceiling only lowers a score, and a file already below it is
not marked `capped`. The two terminal tiers exist because the evidence decays in
two steps: past 36 months the report can no longer say the borrower is not
paying, nor that they paid. `report2`, whose debt was sold on 2025-04-03, walks
down instead of jumping:

| `as_of` | Score | `cap_reason` |
|---|--:|---|
| 2026-07-08 | 43 | — (already below the cap) |
| 2027-07-08 | 45 | `current_overdue` |
| 2028-07-08 | 74 | `terminal_debt_aged` |
| 2031-07-08 | 90 | — |

## Shown beside the rating, never in it

- **`flags`**: `write_off`, `sold`, `enforcement`, `mfo_pressure`. The last lights
  when indicator 9 is red or `MFOPressureDeals` (2) microfinance loans are live.
- **`mfo`**: deals and applications by type of lender; `inquiries_6m` is
  indicator 9's own number.
- **`monitoring`**: every creditor subscribed to updates about the borrower
  (УБКІ: "не впливає на кредитний рейтинг"), newest first, finished ones included.

`TestReferenceBlocksNeverMoveTheScore` holds this.

## Fixtures

Five real УБКІ reports and one synthetic, rated as of 2026-07-08 without income.
`rating/testdata/golden/` pins every payload byte for byte; `go test ./rating
-update` rewrites them, and the diff is the review.

| Report | Score | Band | Cap | Flags | Why |
|---|--:|---|---|---|---|
| report1 | 31 | Низький | — | `write_off` | a payday loan 90 days past due, plus a written-off loan never priced |
| report2 | 43 | Низький | — | `sold`, `enforcement`, `mfo_pressure` | a debt sold to a collector, at the 1034.62 ₴ reported before the sale |
| report3 | 81 | Високий | — | — | clean and current (83 with 24 000 ₴ income: the app's demo) |
| report4 | 62 | Середній | — | `enforcement` | a thin file, current; declared income makes debt load measurable |
| report5 | 45 | Низький | `current_overdue` | — | nine years of history, overdue now |
| report6 | 80 | Високий | — | `mfo_pressure` | synthetic: residues, a closed card with a live limit, two МФО loans |

## Calibration

Every weight, curve anchor and threshold is an expert prior; there is no
booked-and-matured outcome data yet. Open questions:

1. Refit weights and curves against 6–12 month outcomes.
2. The app's own report refreshes must arrive tagged `org=OWN`, or indicator 9
   counts each one as a credit application.
3. The accounting system's income may be stale; its refresh cadence decides how
   far indicator 8 can be trusted.
4. The 45 ceiling, the 100 ₴ and 10 ₴ thresholds and the two-МФО-loan flag need
   checking against the real distribution of СФ borrowers.
5. Display rounding can contradict a band at its edge: 99.60 ₴ shows as "100 ₴" in
   green beside «менше 100 ₴». A product decision, not yet taken.

## Not a credit decision

The rating has no knockout factors — wanted lists, sanctions, bankruptcy, the
gambling registry — and no term-normalized delinquency; both were dropped with
the earlier scorecard and are in git history. A borrower on a wanted list with a
clean payment record rates normally (`report4`: 62). That is acceptable for a
financial-literacy tool and not for a lending decision.

## Security

`POST /rating` is unauthenticated: it belongs behind the gateway, which
identifies callers. CORS headers are sent only for the one origin named by
`-cors-origin`. Bodies are capped at 4 MiB; unreadable, `NaN` or infinite amounts
read as zero; responses never quote the report or the income, and the log records
only why a report did not parse. Responses carry `Cache-Control: no-store`. The server
drains requests in flight on SIGTERM, and the Dockerfile runs a static binary as
a non-root user on a distroless image.
````

- [ ] **Step 2: Correct the spec's money example**

The spec's §7.2 row 2 cites 0.02 + 64.07 + 35.91 ₴, but 0.02 ₴ is below the 10 ₴ residue threshold and never reaches the sum. Replace the row

```
| 2 | Float sums miss exact thresholds | Overdue balances of 0.02 + 64.07 + 35.91 ₴ sum to 99.99999999999999: no 100 ₴ ceiling, and a green indicator 4 displayed as "100 ₴" | Integer kopiykas |
```

with

```
| 2 | Float sums miss exact thresholds | Arrears of 10.14 + 58.12 + 31.74 ₴ sum to 99.999999999999986: no 100 ₴ ceiling, and a green indicator 4 displayed as "100 ₴". Arrears of 11.44 + 512.19 + 476.37 ₴ land above 1 000 and paint it red | Integer kopiykas |
```

And in §6.1, after "Any other part is rejected, and so is a repeated one.", add: "An empty optional part reads as an absent one, since HTML forms send blank fields."

- [ ] **Step 3: Commit**

```bash
git add scoring/README.md docs/specs/2026-09-28-scoring-engine-cleanup-design.md
git commit -m "docs: rewrite the engine README as a reference, and correct the spec's money example"
```

---

### Task 7: Verify and hand over

- [ ] **Step 1: The whole module**

Run: `go vet ./... && go test ./... -race -count=1 -cover && golangci-lint run ./... && gofmt -l . && govulncheck ./...`
Expected: every package `ok` (coverage: `rating` ≥ 97%, `ubki` ≥ 97%, `internal/httpapi` ≥ 95%); `0 issues.`; no gofmt output; `No vulnerabilities found.`

- [ ] **Step 2: Review**

Review `git diff 9d224e6...HEAD -- scoring`. Fix critical and high-priority findings with regression tests, each in its own commit; record the remaining findings in the issue.

- [ ] **Step 3: Record**

Comment on ENG-687 with the commits, the verification output and the corrected money example; move it to In Review.
