package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/model"
)

// modelDiff compares the finite observation languages of two explicit model
// instances. Native replay confirms admission in a witness's source; exclusion
// rests on the complete target graph, never on a failed native sample.
func modelDiff(ctx context.Context, stdout io.Writer, args []string) (int, error) {
	var reports machineReports
	fs := commandFlags("model-diff")
	reports.flags(fs)
	before := fs.String("before", "", "binding of the model before the change")
	after := fs.String("after", "", "binding of the model after the change")
	observe := fs.String("observe", "", "required comma-separated model variables observed at init and after every transition (no stutter hiding)")
	states := fs.Int("states", model.DefaultSearchStates, "maximum states in each model graph; incomplete graphs give no comparison verdict")
	pairs := fs.Int("pairs", 100000, "maximum product nodes admitted in each directional trace-inclusion search")
	work := fs.Int("work", 1000000, "maximum target candidate operations in each directional trace-inclusion search")
	depth := fs.Int("depth", 0, "maximum model-graph path depth (default: binding's simulate.search.depth, else unbounded)")
	constants := fs.String("const", "", "constant expressions NAME=expr,... applied to both instances over each binding's configured constants")
	quint := fs.String("quint", "quint", "Quint binary for model loading and source witness confirmation")
	fs.Parse(args)
	var text io.Writer = stdout
	doc := validate.NewDocument("model-diff")
	if err := reports.resolve(&text); err != nil {
		return reports.write(doc, stdout, 2, err)
	}
	fields, err := comparisonFields(*observe)
	var constantValues []string
	if err == nil {
		constantValues, err = comparisonConstants(*constants)
	}
	if err == nil && (*before == "" || *after == "") {
		err = needs("model-diff", "-before old.binding.yaml and -after new.binding.yaml")
	}
	if err == nil && (*states <= 0 || *pairs <= 0 || *work <= 0 || *depth < 0) {
		err = fmt.Errorf("model-diff: -states, -pairs and -work must be positive; -depth must be nonnegative")
	}
	if err == nil && fs.NArg() != 0 {
		err = fmt.Errorf("model-diff: unexpected positional arguments %s; select bindings with -before and -after", strings.Join(fs.Args(), " "))
	}
	if err != nil {
		return reports.write(doc, stdout, 2, err)
	}
	doc.Comparison = &validate.DocComparison{Scope: "finite_observation_traces", Basis: "complete_go_graph_product", Status: "inconclusive", Observe: slices.Clone(fields), PairLimit: *pairs, WorkLimit: *work,
		Before: validate.DocComparisonGraph{Binding: *before}, After: validate.DocComparisonGraph{Binding: *after},
		Added: validate.DocComparisonDirection{Status: "inconclusive", Reason: "not compared"}, Removed: validate.DocComparisonDirection{Status: "inconclusive", Reason: "not compared"}}
	if err := ctx.Err(); err != nil {
		return reports.write(doc, stdout, 2, err)
	}
	left, err := validate.NewChecker(ctx, *before, validate.Options{Quint: *quint})
	if err != nil {
		return reports.write(doc, stdout, 2, fmt.Errorf("model-diff before %s: %w", *before, err))
	}
	defer left.Close()
	right, err := validate.NewChecker(ctx, *after, validate.Options{Quint: *quint})
	if err != nil {
		return reports.write(doc, stdout, 2, fmt.Errorf("model-diff after %s: %w", *after, err))
	}
	defer right.Close()
	cfg := model.GraphSearch{Constants: constantValues, States: *states, Depth: *depth, Invariants: []string{}, Eventually: []string{}}
	lg, err := model.For(left).Graph(cfg)
	if err != nil {
		return reports.write(doc, stdout, 2, fmt.Errorf("model-diff before %s: %w", *before, err))
	}
	if err := ctx.Err(); err != nil {
		return reports.write(doc, stdout, 2, err)
	}
	rg, err := model.For(right).Graph(cfg)
	if err != nil {
		return reports.write(doc, stdout, 2, fmt.Errorf("model-diff after %s: %w", *after, err))
	}
	comparison, err := model.Compare(ctx, lg, rg, model.CompareOptions{Observe: fields, Pairs: *pairs, Work: *work})
	if err == nil {
		err = model.ConfirmComparison(ctx, comparison)
	}
	if comparison != nil {
		doc = comparison.Doc()
		doc.Comparison.Before.Binding = *before
		doc.Comparison.After.Binding = *after
	}
	code := 2
	if err == nil {
		switch doc.Comparison.Status {
		case "equivalent":
			code, doc.Verdict = 0, "pass"
		case "different":
			code, doc.Verdict = 1, "fail"
		default:
			doc.Verdict = "no verdict"
		}
		writeModelComparison(text, doc.Comparison)
	}
	return reports.write(doc, stdout, code, err)
}

// comparisonFields requires explicit observation ownership: empty or repeated
// variables are usually a command typo and must not silently alter projection.
func comparisonFields(value string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, field := range strings.Split(value, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, fmt.Errorf("model-diff needs -observe variable,... with no empty variable names")
		}
		if seen[field] {
			return nil, fmt.Errorf("model-diff -observe: variable %q is repeated; name each observed variable once", field)
		}
		seen[field] = true
		out = append(out, field)
	}
	return out, nil
}

// comparisonConstants splits only commas outside Quint strings and containers:
// LABEL="a,b" is one expression, and quoted brackets do not affect nesting.
func comparisonConstants(value string) ([]string, error) {
	constants, err := comparisonConstantEntries(value)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for index, constant := range constants {
		name, expression, ok := strings.Cut(constant, "=")
		name, expression = strings.TrimSpace(name), strings.TrimSpace(expression)
		if !ok || name == "" || expression == "" {
			return nil, fmt.Errorf("model-diff -const %q: want NAME=expression with a nonempty name and value", constant)
		}
		if seen[name] {
			return nil, fmt.Errorf("model-diff -const: constant %q is repeated; set each constant once", name)
		}
		seen[name] = true
		constants[index] = name + "=" + expression
	}
	return constants, nil
}

func comparisonConstantEntries(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var entries []string
	var delimiters []rune
	quoted, escaped, start := false, false, 0
	for index, character := range value {
		if quoted {
			switch {
			case escaped:
				escaped = false
			case character == '\\':
				escaped = true
			case character == '"':
				quoted = false
			}
			continue
		}
		switch character {
		case '"':
			quoted = true
		case '(', '[', '{':
			delimiters = append(delimiters, character)
		case ')', ']', '}':
			opening := map[rune]rune{')': '(', ']': '[', '}': '{'}[character]
			if len(delimiters) == 0 || delimiters[len(delimiters)-1] != opening {
				return nil, fmt.Errorf("model-diff -const: unmatched delimiter %q", character)
			}
			delimiters = delimiters[:len(delimiters)-1]
		case ',':
			if len(delimiters) == 0 {
				entries = append(entries, strings.TrimSpace(value[start:index]))
				start = index + 1
			}
		}
	}
	if quoted || len(delimiters) != 0 {
		return nil, fmt.Errorf("model-diff -const: unclosed string or container expression")
	}
	return append(entries, strings.TrimSpace(value[start:])), nil
}

func writeModelComparison(out io.Writer, comparison *validate.DocComparison) {
	fmt.Fprintf(out, "model-diff: %s for these finite instances on [%s]\n", comparison.Status, strings.Join(comparison.Observe, ", "))
	fmt.Fprintf(out, "  basis: %s\n", comparison.Basis)
	fmt.Fprintln(out, "  scope: observation at init and after every transition; no hidden stutters, action identity, fairness or refinement claim")
	for _, graph := range []struct {
		name string
		data validate.DocComparisonGraph
	}{{"before", comparison.Before}, {"after", comparison.After}} {
		var instance []string
		for _, constant := range graph.data.Constants {
			instance = append(instance, constant.Name+"="+constant.Value)
		}
		fmt.Fprintf(out, "  %s %s: %s(%s), %d states, %d starts, %d transitions, complete=%v; bounds states=%d depth=%d", graph.name, graph.data.Binding, graph.data.Module, strings.Join(instance, ", "), graph.data.States, graph.data.Starts, graph.data.Transitions, graph.data.Complete, graph.data.Bound.States, graph.data.Bound.Depth)
		if graph.data.Cut != "" {
			fmt.Fprintf(out, "; cut=%s %s", graph.data.Cut, graph.data.Stopped)
		}
		fmt.Fprintln(out)
	}
	for _, direction := range []struct {
		name string
		data validate.DocComparisonDirection
	}{{"added", comparison.Added}, {"removed", comparison.Removed}} {
		fmt.Fprintf(out, "  %s: %s (%d product nodes, limit %d; %d target operations, limit %d)", direction.name, direction.data.Status, direction.data.Explored, comparison.PairLimit, direction.data.Work, comparison.WorkLimit)
		if direction.data.Reason != "" {
			fmt.Fprintf(out, "; %s", direction.data.Reason)
		}
		fmt.Fprintln(out)
		if witness := direction.data.Witness; witness != nil {
			fmt.Fprintf(out, "    source native replay: %s; target exclusion: %s\n", witness.Native, witness.ExclusionBasis)
			if witness.NativeReason != "" {
				fmt.Fprintf(out, "    %s\n", witness.NativeReason)
			}
			if witness.Source != nil {
				fmt.Fprintf(out, "    original-model replay: %s\n", witness.Source.Replay)
			}
			for index, observation := range witness.Observed {
				var values []string
				for _, value := range observation {
					values = append(values, value.Name+" = "+value.Value)
				}
				fmt.Fprintf(out, "    %d: %s\n", index, strings.Join(values, ", "))
			}
		}
	}
}
