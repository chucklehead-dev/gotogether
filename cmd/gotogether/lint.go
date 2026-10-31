package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	lintcheck "github.com/chucklehead-dev/gotogether/lint"
)

func lint(args []string) (int, error) {
	fs := commandFlags("lint")
	bindings := fs.String("binding", "", "binding files, comma-separated; source actions are checked against their union")
	source := fs.String("src", "", "Go source tree: action annotations and literal Record calls (syntactic inspection)")
	simulation := fs.String("sim", "", "Go source tree to inspect for escapes from simulation")
	weave := fs.String("weave", "", "Go source tree to inspect for unsupported weaving")
	conversions := fs.String("convert", "", "span conversion rule files, comma-separated")
	quint := fs.String("quint", "quint", "Quint binary for model metadata compilation")
	asJSON := fs.Bool("json", false, "emit the structured lint report, including blocked checks and fixes")
	fs.Parse(args)
	split := func(value string) []string {
		if value == "" {
			return nil
		}
		return strings.Split(value, ",")
	}
	report := lintcheck.Inspect(lintcheck.Options{Bindings: split(*bindings), Source: *source, Simulation: *simulation, Weave: *weave, Conversions: split(*conversions), Quint: *quint})
	if fs.NArg() > 0 {
		reason := "unexpected positional arguments: " + strings.Join(fs.Args(), " ")
		report.Checks = append(report.Checks, lintcheck.Check{Name: "request.arguments", Status: lintcheck.CheckBlocked, Reason: reason, Operational: true})
		report.Findings = append(report.Findings, lintcheck.Diagnostic{Code: "request.arguments", Severity: lintcheck.Error, Message: reason, Fix: "select input paths with -binding, -src, -sim, -weave or -convert", Confidence: "request"})
	}
	if *asJSON {
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
