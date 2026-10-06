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

// goldenCase defines a report, reference date and verified income in whole hryvnias.
type goldenCase struct {
	name   string
	report string
	asOf   string
	income int
}

// goldenCases lists the reports and inputs used to check the saved JSON output.
var goldenCases = []goldenCase{
	{name: "report1", report: "report1.xml", asOf: "2026-07-08"},
	{name: "report2", report: "report2.xml", asOf: "2026-07-08"},
	{name: "report3", report: "report3.xml", asOf: "2026-07-08"},
	{name: "report4", report: "report4.xml", asOf: "2026-07-08"},
	{name: "report5", report: "report5.xml", asOf: "2026-07-08"},
	{name: "report6", report: "report6.xml", asOf: "2026-07-08"},
	// This case supplies the companion app's clean demo rating.
	{name: "report3_income_24000", report: "report3.xml", asOf: "2026-07-08", income: 24000},
	// Check report2 across both terminal-debt cap windows.
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

// Using the report build date implicitly or explicitly should produce the same rating.
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
