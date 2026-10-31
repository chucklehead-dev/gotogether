package qlint

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/convert"
	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/schemas"
)

// Options selects independent lint targets. Empty fields select no check.
// Source requires Bindings because source actions are compared with their union.
type Options struct {
	Bindings    []string
	Source      string
	Simulation  string
	Weave       string
	Conversions []string
	Quint       string
}

// Inspect performs selected nonwriting checks and retains every requested phase,
// including phases blocked by bad inputs. Independent targets continue after a
// failure. It scans syntax and typechecks Quint models; it does not run Go code,
// Quint tests, model search, or conformance validation.
func Inspect(o Options) *Report {
	r := &Report{Format: formats.Lint.Name(), Scope: "selected_static_checks", Limits: []string{"trace conformance and application behavior were not checked"}, Checks: []Check{}, Findings: []Diagnostic{}, Evidence: []Evidence{}}
	if o.Source != "" || o.Simulation != "" || o.Weave != "" {
		r.Limits = append(r.Limits, "Go source checks inspect syntax without resolving types; typed qanalysis checks are separate")
	}
	if o.Source != "" {
		r.Limits = append(r.Limits, "source recording inventories cover annotations and literal action names; dynamic recording names are not covered")
	}
	plan := func(name, target string) int {
		r.Checks = append(r.Checks, Check{Name: name, Target: target, Status: CheckBlocked, Reason: "not performed"})
		return len(r.Checks) - 1
	}
	type phases struct{ schema, load, spec, model, lint, choices int }
	var bindings []phases
	var conversions [][2]int
	for _, p := range o.Conversions {
		conversions = append(conversions, [2]int{plan("conversion.schema", p), plan("conversion.load", p)})
	}
	weave, sim := -1, -1
	if o.Weave != "" {
		weave = plan("weave.syntax", o.Weave)
	}
	if o.Simulation != "" {
		sim = plan("simulation.syntax", o.Simulation)
	}
	for _, p := range o.Bindings {
		bindings = append(bindings, phases{plan("binding.schema", p), plan("binding.load", p), plan("binding.spec", p), plan("binding.model", p), plan("binding.compatibility", p), plan("binding.choices", p)})
	}
	annotations, records, source := -1, -1, -1
	if o.Source != "" {
		annotations = plan("source.annotations", o.Source)
		records = plan("source.records", o.Source)
		source = plan("source.bindings", o.Source)
	}
	if len(r.Checks) == 0 {
		at := plan("request", "")
		r.block(at, "select at least one lint target", true)
		r.find("request.invalid", Error, "select at least one lint target", "", "select -binding, -convert, -sim or -weave", "request")
		return r
	}
	for i, p := range o.Conversions {
		x := conversions[i]
		if !r.schema(x[0], p, schemas.Convert()) {
			r.block(x[1], "conversion schema check did not pass", false)
			continue
		}
		if _, err := convert.Load(p); err != nil {
			r.fail(x[1], "conversion.load", err, p, "correct the conversion rule values", "schema", false)
		} else {
			r.pass(x[1])
			r.Evidence = append(r.Evidence, Evidence{Kind: "conversion", Target: p, Count: 1})
		}
	}
	for _, item := range []struct {
		at         int
		path, code string
		scan       func(string) ([]Finding, error)
	}{{weave, o.Weave, "weave.unsupported", Woven}, {sim, o.Simulation, "simulation.escape", Sim}} {
		if item.at < 0 {
			continue
		}
		fs, err := item.scan(item.path)
		for _, f := range fs {
			r.Findings = append(r.Findings, Diagnostic{Code: f.Code, Severity: Error, Message: f.What, Path: f.file, Line: f.line, Column: f.col, Fix: f.Fix, Confidence: "syntax"})
		}
		if err != nil {
			r.scanFailure(item.at, item.code+".scan", err, item.path)
		} else {
			r.pass(item.at)
			if len(fs) > 0 {
				r.Checks[item.at].Status = CheckFail
			}
		}
	}
	var bound []binding.Bound
	allBound := true
	for i, p := range o.Bindings {
		x := bindings[i]
		blockRest := func(reason string, from int) {
			for _, at := range []int{x.load, x.spec, x.model, x.lint, x.choices} {
				if at >= from {
					r.block(at, reason, false)
				}
			}
			allBound = false
		}
		if !r.schema(x.schema, p, schemas.Binding()) {
			blockRest("binding schema check did not pass", x.load)
			continue
		}
		b, err := binding.Load(p)
		if err != nil {
			r.fail(x.load, "binding.load", err, p, "correct the binding values", "binding", false)
			blockRest("binding could not be loaded", x.spec)
			continue
		}
		r.pass(x.load)
		_, statErr := os.Stat(b.SpecPath())
		err = b.CheckSpec()
		if err == nil {
			err = statErr
		}
		if err != nil {
			r.fail(x.spec, "binding.spec", err, p, "correct spec relative to the binding file's directory", "binding", statErr != nil)
			blockRest("binding spec check did not pass", x.model)
			continue
		}
		r.pass(x.spec)
		m, err := binding.LoadModel(o.Quint, b.SpecPath(), b.Module)
		if err != nil {
			operational := modelOperational(err)
			r.fail(x.model, "binding.model", err, b.SpecPath(), "correct the model/module or make the selected Quint executable available", "model", operational)
			blockRest("model loading/typechecking did not pass", x.lint)
			continue
		}
		r.pass(x.model)
		r.bindingFindings(x.lint, b.LintFindings(m), p)
		r.bindingFindings(x.choices, b.LintChoiceFindings(m), p)
		for _, property := range slices.Sorted(maps.Keys(m.Diagnostics)) {
			r.Evidence = append(r.Evidence, Evidence{Kind: "model.diagnostic", Target: p, Count: 1, Items: []EvidenceItem{{Path: b.SpecPath(), Symbol: property, Action: m.Diagnostics[property]}}})
		}
		bound = append(bound, binding.Bound{Binding: b, Model: m})
		r.Evidence = append(r.Evidence, Evidence{Kind: "binding", Target: p, Count: len(b.Actions)})
	}
	if o.Source != "" {
		anns, aerr := binding.ScanAnnotations(o.Source)
		recs, rerr := binding.ScanRecords(o.Source)
		if aerr != nil {
			r.scanFailure(annotations, "source.annotations.scan", aerr, o.Source)
		} else {
			r.pass(annotations)
			e := Evidence{Kind: "source.annotations", Target: o.Source, Count: len(anns)}
			for _, a := range anns {
				e.Items = append(e.Items, EvidenceItem{Path: a.Path, Line: a.Line, Column: a.Column, Symbol: a.Func, Action: a.Action})
			}
			r.Evidence = append(r.Evidence, e)
		}
		if rerr != nil {
			r.scanFailure(records, "source.records.scan", rerr, o.Source)
		} else {
			r.pass(records)
			e := Evidence{Kind: "source.records", Target: o.Source, Count: len(recs)}
			for _, a := range recs {
				e.Items = append(e.Items, EvidenceItem{Path: a.Path, Line: a.Line, Column: a.Column, Symbol: a.Func, Action: a.Action})
			}
			r.Evidence = append(r.Evidence, e)
		}
		switch {
		case len(o.Bindings) == 0:
			r.block(source, "source checks require at least one binding", true)
			r.find("request.source_binding", Error, "source checks require at least one binding", o.Source, "select -binding with -src", "request")
		case !allBound:
			r.block(source, "not every requested binding/model could be loaded; checking a partial union would invent unknown actions", false)
		case aerr != nil || rerr != nil:
			r.block(source, "source inventory could not be completed", false)
		default:
			r.bindingFindings(source, binding.LintSourceFindings(bound, anns, recs), o.Source)
		}
	}
	return r
}

func (r *Report) pass(at int) { r.Checks[at].Status = CheckPass; r.Checks[at].Reason = "" }
func (r *Report) block(at int, reason string, operational bool) {
	r.Checks[at].Status = CheckBlocked
	r.Checks[at].Reason = reason
	r.Checks[at].Operational = operational
}
func (r *Report) find(code string, severity Severity, message, path, fix, confidence string) {
	r.Findings = append(r.Findings, Diagnostic{Code: code, Severity: severity, Message: message, Path: path, Fix: fix, Confidence: confidence})
}
func (r *Report) fail(at int, code string, err error, path, fix, confidence string, operational bool) {
	r.Checks[at].Status = CheckFail
	if operational {
		r.Checks[at].Status = CheckBlocked
	}
	r.Checks[at].Reason = err.Error()
	r.Checks[at].Operational = operational
	r.find(code, Error, err.Error(), path, fix, confidence)
}
func (r *Report) schema(at int, path string, schema []byte) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		r.fail(at, r.Checks[at].Name+".read", err, path, "make the selected file readable", "schema", true)
		return false
	}
	ps, err := schemas.Check(schema, data)
	if err != nil {
		r.fail(at, r.Checks[at].Name, err, path, "correct the document syntax", "schema", false)
		return false
	}
	r.pass(at)
	for _, p := range ps {
		r.Checks[at].Status = CheckFail
		r.Findings = append(r.Findings, Diagnostic{Code: r.Checks[at].Name, Severity: Error, Message: fmt.Sprintf("%s: %s", p.Path, p.Message), Path: path, Line: p.Line, Column: p.Column, Symbol: p.Path, Fix: "correct the field to match the current schema", Confidence: "schema"})
	}
	return len(ps) == 0
}
func (r *Report) bindingFindings(at int, fs []binding.LintFinding, path string) {
	r.pass(at)
	for _, f := range fs {
		if f.Path == "" {
			f.Path = path
		}
		r.Findings = append(r.Findings, Diagnostic{Code: f.Code, Severity: Severity(f.Severity), Message: f.Message, Path: f.Path, Line: f.Line, Column: f.Column, Symbol: f.Symbol, Fix: f.Fix, Confidence: f.Confidence})
		if Severity(f.Severity) == Error {
			r.Checks[at].Status = CheckFail
		}
	}
}
func modelOperational(err error) bool {
	var missing *exec.Error
	var path *os.PathError
	var exit *exec.ExitError
	return errors.As(err, &missing) || errors.As(err, &path) || (errors.As(err, &exit) && exit.ExitCode() < 0)
}

func (r *Report) scanFailure(at int, code string, err error, path string) {
	var io *os.PathError
	operational := errors.As(err, &io)
	r.fail(at, code, err, path, "make the source tree readable and valid Go syntax", "syntax", operational)
	r.Checks[at].Status = CheckBlocked
}
