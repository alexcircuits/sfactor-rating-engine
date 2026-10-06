// Command score reads a УБКІ XML report and prints a rating as JSON or a table.
//
//	score report.xml
//	score -income 25000 report.xml
//	score -as-of 2026-07-08 report.xml
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
	if err := in.Validate(); err != nil {
		return fmt.Errorf("-as-of %s: %w", *asOf, err)
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

// formatTable formats the rating and its indicators for terminal output.
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
