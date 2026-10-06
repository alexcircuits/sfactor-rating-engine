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

// The CLI output must match the golden JSON used by the app.
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
		{"a date before the report was built", []string{"-as-of", "2026-07-07", report3}, false},
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
