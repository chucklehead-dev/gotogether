package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/formats"
	lintcheck "github.com/chucklehead-dev/gotogether/lint"
)

func lint(ctx context.Context, args []string) (int, error) {
	fs := commandFlagsWithHandling("lint", flag.ContinueOnError)
	bindings := fs.String("binding", "", "binding files, comma-separated; source actions are checked against their union")
	source := fs.String("src", "", "Go source tree: action annotations and literal Record calls (syntactic inspection)")
	simulation := fs.String("sim", "", "Go source tree to inspect for escapes from simulation")
	weave := fs.String("weave", "", "Go source tree to inspect for unsupported weaving")
	conversions := fs.String("convert", "", "span conversion rule files, comma-separated")
	models := fs.String("model", "", "Quint model files checked without a binding (an assignment's precedence, reserved names, quint's typecheck), comma-separated")
	quint := fs.String("quint", "quint", "Quint binary for model metadata compilation")
	asJSON := false
	fs.BoolFunc("json", "emit the structured lint report, including blocked checks and fixes; put first for request errors", func(value string) error {
		selected, err := strconv.ParseBool(value)
		if err == nil {
			asJSON = selected
		}
		return err
	})
	// Parse errors become request findings rather than exiting before the
	// selected output format can be emitted. Only parsed flags select JSON:
	// a later -json may be a string value, or follow -- or an earlier error.
	fs.SetOutput(io.Discard)
	parseErr := fs.Parse(args)
	if errors.Is(parseErr, flag.ErrHelp) {
		fs.SetOutput(os.Stderr)
		fs.Usage()
		return 0, nil
	}
	split := func(value string) []string {
		if value == "" {
			return nil
		}
		return strings.Split(value, ",")
	}
	var report *lintcheck.Report
	switch {
	case parseErr != nil:
		report = lintRequestError("request.flags", parseErr.Error(), "use gotogether lint -h for valid flags; put -json first for machine-readable request errors")
	case fs.NArg() > 0:
		report = lintRequestError("request.arguments", "unexpected positional arguments: "+strings.Join(fs.Args(), " "), "select input paths with -binding, -model, -src, -sim, -weave or -convert")
	default:
		report = lintcheck.Inspect(ctx, lintcheck.Options{Bindings: split(*bindings), Source: *source, Simulation: *simulation, Weave: *weave, Conversions: split(*conversions), Models: split(*models), Quint: *quint})
	}
	if asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return 2, err
		}
	} else {
		printLintReport(report)
	}
	return report.ExitCode(), nil
}

// lintRequestError describes a refused request without claiming any target
// was read. Inspection findings and evidence are reserved for valid requests.
func lintRequestError(code, reason, fix string) *lintcheck.Report {
	return &lintcheck.Report{
		Format: formats.Lint.Name(), Scope: "request", Limits: []string{"no lint checks were performed"},
		Checks:   []lintcheck.Check{{Name: code, Status: lintcheck.CheckBlocked, Reason: reason, Operational: true}},
		Findings: []lintcheck.Diagnostic{{Code: code, Severity: lintcheck.Error, Message: reason, Fix: fix, Confidence: "request"}},
		Evidence: []lintcheck.Evidence{},
	}
}

func printLintReport(report *lintcheck.Report) {
	for _, finding := range report.Findings {
		location := finding.Path
		if finding.Line > 0 {
			location += fmt.Sprintf(":%d:%d", finding.Line, finding.Column)
		}
		if location != "" {
			location += ": "
		}
		fmt.Printf("%s: %s%s [%s]\n", finding.Severity, location, finding.Message, finding.Code)
		if finding.Fix != "" {
			fmt.Printf("  fix: %s\n", finding.Fix)
		}
	}
	for _, evidence := range report.Evidence {
		switch evidence.Kind {
		case "source.annotations":
			fmt.Printf("%d //quint:action annotations under %s\n", evidence.Count, evidence.Target)
			for _, item := range evidence.Items {
				fmt.Printf("  %s:%d:%d %-14s %s\n", item.Path, item.Line, item.Column, item.Action, item.Symbol)
			}
		case "source.records":
			fmt.Printf("%d literal Record calls under %s\n", evidence.Count, evidence.Target)
		case "binding":
			fmt.Printf("binding %s: %d actions inspected against model metadata\n", evidence.Target, evidence.Count)
		case "conversion":
			fmt.Printf("%s fits its schema and loads\n", evidence.Target)
		case "model.diagnostic":
			for _, item := range evidence.Items {
				fmt.Printf("diagnostic %s -> %s", item.Symbol, item.Value)
				if item.Active != nil {
					fmt.Printf(" (active=%v)", *item.Active)
				}
				fmt.Println()
			}
		}
	}
	for _, check := range report.Checks {
		fmt.Printf("check %s %s: %s", check.Name, check.Target, check.Status)
		if check.Reason != "" {
			fmt.Printf("; %s", check.Reason)
		}
		fmt.Println()
	}
	fmt.Println("scope:", report.Scope)
	for _, limit := range report.Limits {
		fmt.Println("  limit:", limit)
	}
}
