package validate

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/diagnostic"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// A command's report for machines (I20): validate -json and simulate
// -json write a Document, gotogether.report/1, which alert routing, CI
// and a job summary read instead of the text's lines; JUnit and SARIF are
// renderings of it for CI's test and code-scanning views. The text report
// stays the person's: the document carries what it says (the failing
// record by its one id, with its wall time, recorder and trace, the
// model's expectation and the state's diff) as fields, and the text of
// each failure as the person reads it.

// ReportFormat names a command's machine-readable report, a Document
// (internal/formats' Report).
const ReportFormat = "gotogether.report/1"

// A Document is a command's report as data (validate -json, simulate
// -json): gotogether.report/1. A field added later is optional; a field
// changed or removed is a new major.
type Document struct {
	// Receipt describes the history judgment, including folded partitions.
	Receipt *receipt.Summary `json:"receipt,omitempty"`
	Format  string           `json:"format"`  // ReportFormat
	Command string           `json:"command"` // "validate", "simulate"
	// Verdict is "pass", "fail", "no verdict" (every failure has none:
	// lost or dropped steps, a search's budget) or "error" (the tool
	// failed: Error says how); Exit the command's exit status.
	Verdict string `json:"verdict"`
	Exit    int    `json:"exit"`
	// Reason is the first failure's, in the claims' words ("violates
	// refundsWithinCapture", "rejects void"); Result the result line's
	// text after its verdict.
	Reason  string `json:"reason,omitempty"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
	Binding string `json:"binding,omitempty"`
	// Inputs are what was read, each with its warnings.
	Inputs []DocInput `json:"inputs,omitempty"`
	// Artifacts is where the generated modules and failing runs' ITF are.
	Artifacts string `json:"artifacts,omitempty"`
	// Issues are the run's reconstruction issues, as the report prints them.
	Issues []string `json:"issues,omitempty"`
	// Actors are validate's: each actor's (or entity's) result, but the
	// passing partitions of a partitioned log, folded into Partitions.
	Actors     []DocActor     `json:"actors,omitempty"`
	Partitions []DocPartition `json:"partitions,omitempty"`
	// Checks are simulate's: each invariant's, possible's and the graph's
	// verdict.
	Checks []DocCheck `json:"checks,omitempty"`
	// Notes are what else the report says: warnings, the finish check, a
	// replay's comparison with its manifest, where the manifest went.
	Notes []string `json:"notes,omitempty"`
}

// A DocInput is one input file a command read.
type DocInput struct {
	Path     string   `json:"path"`
	Format   string   `json:"format"` // "otlp", "steps", "requests", "itf", "" (empty)
	Steps    int      `json:"steps"`
	Warnings []string `json:"warnings,omitempty"`
}

// A DocActor is one actor's result (Result) as data.
type DocActor struct {
	Receipt   *receipt.Actor `json:"receipt,omitempty"`
	Actor     string         `json:"actor"`
	Partition string         `json:"partition,omitempty"`
	Steps     int            `json:"steps"`
	// Verdict is "pass", "fail" or "no verdict"; Reason why it is not a
	// pass (Result.Reason).
	Verdict     string         `json:"verdict"`
	Reason      string         `json:"reason,omitempty"`
	Conformance string         `json:"conformance"` // "pass", "fail", "n/a"
	Invariants  []DocInvariant `json:"invariants,omitempty"`
	// Failures are its conformance's failure and its invariants', the
	// failing ones first.
	Failures  []DocFailure `json:"failures,omitempty"`
	Issues    []string     `json:"issues,omitempty"`
	Pending   []DocRecord  `json:"pending,omitempty"`
	Absent    []DocAbsence `json:"absent,omitempty"`
	Constants []string     `json:"constants,omitempty"`
	JudgedBy  string       `json:"judgedBy,omitempty"` // "quint", "evaluator", "both"
	Module    string       `json:"module,omitempty"`
	ITF       string       `json:"itf,omitempty"`
	// Nav is the gotogether nav command that opens the model before the
	// rejected step, when the CLI can name one.
	Nav string `json:"nav,omitempty"`
}

// A DocInvariant is one invariant's, eventual val's or bounded
// response's verdict: "pass", "fail", "n/a" (not checked, or no verdict).
type DocInvariant struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // "invariant", "eventually", "within"
	Verdict string `json:"verdict"`
	// Cause says why an n/a has no verdict, when it has a cause.
	Cause string `json:"cause,omitempty"`
}

// A DocFailure is one failure (Failure) as data.
type DocFailure struct {
	// Check is what failed: "conformance", or the invariant's name.
	Check string `json:"check"`
	// Kind is "disabled" (the model cannot take the step), "state" (the
	// state after it differs from what was observed), "invariant",
	// "within" or "error" (quint could not evaluate the step).
	Kind string `json:"kind"`
	// Verdict is "fail", or "n/a" for a failure that is no verdict.
	Verdict string `json:"verdict"`
	// At names where it is, as the report does: "record day.jsonl:12",
	// or "step 3" for an inferred step.
	At string `json:"at"`
	// Record is the failing step's record, when it has one.
	Record *DocRecord `json:"record,omitempty"`
	Quint  string     `json:"quint,omitempty"`  // the model's call
	Expect string     `json:"expect,omitempty"` // a mismatch's post-state check, as rendered
	// ExpectTemplate is the binding's expect that rendered it.
	ExpectTemplate string `json:"expectTemplate,omitempty"`
	// Why is why the model cannot take a rejected step (its Expected and
	// Changes: the model's answer to the request, and what it changes).
	Why *Explanation `json:"why,omitempty"`
	// Changes are what a mismatched or violating step changed in the
	// model's state.
	Changes []Change `json:"changes,omitempty"`
	// State is the model's state at the failure (before a rejected step,
	// after any other), each variable as a Quint literal, whole.
	State      map[string]string `json:"state,omitempty"`
	StateAfter bool              `json:"stateAfter,omitempty"`
	Note       string            `json:"note,omitempty"`
	// Brief is the failure in a line, for an alert or a summary's table:
	// the step observed, then the model's answer to it or what it
	// changed ("void(id=ch_2) -> invalid_state; the model expects
	// void(id = "ch_2", result = "ok")").
	Brief string `json:"brief"`
	// Text is the failure as the text report prints it.
	Text string `json:"text"`
}

// A DocRecord is a step's record: ID is the one index a report names
// it by (RecordID); Step its place in the replay (1-based; 0 none) and
// Seq its recorder's number, the model author's indices.
type DocRecord struct {
	// Ambiguous marks a pending call whose effect is assumed by a search.
	Ambiguous bool           `json:"ambiguous,omitempty"`
	ID        string         `json:"id"`
	Step      int            `json:"step,omitempty"`
	Seq       uint64         `json:"seq"`
	Recorder  string         `json:"recorder,omitempty"`
	Process   string         `json:"process,omitempty"`
	Actor     string         `json:"actor,omitempty"`
	Thread    string         `json:"thread,omitempty"`
	Time      string         `json:"time,omitempty"`    // RFC 3339, UTC
	Invoked   string         `json:"invoked,omitempty"` // RFC 3339, UTC
	Action    string         `json:"action"`
	Args      map[string]any `json:"args,omitempty"`
	Obs       map[string]any `json:"obs,omitempty"`
	Outcome   string         `json:"outcome,omitempty"`
	Error     string         `json:"error,omitempty"`
	Source    string         `json:"source,omitempty"` // where it was read
	Ref       string         `json:"ref,omitempty"`    // the driver's link to its artifact
	TraceID   string         `json:"traceId,omitempty"`
	SpanID    string         `json:"spanId,omitempty"`
	// Text is the step as a report prints it.
	Text string `json:"text"`
}

// A DocAbsence is a step the run was expected to show and does not
// (Absence).
type DocAbsence struct {
	// Diagnostic supplies optional model-authored detail, without changing Cause.
	Diagnostic *diagnostic.Result `json:"diagnostic,omitempty"`
	Kind       string             `json:"kind"`
	Name       string             `json:"name"`
	Cause      string             `json:"cause"`
	Why        string             `json:"why"`
}

// A DocPartition is the passing partitions of one actor of a log judged
// by entity, folded as the text report folds them.
type DocPartition struct {
	Actor string `json:"actor"`
	Key   string `json:"key"`
	Pass  int    `json:"pass"`
	Of    int    `json:"of"`
	Steps int    `json:"steps"`
}

// A DocCheck is one of simulate's verdicts: an invariant's ("holds",
// "allows", "bounded", "unchecked" or "fail"), a possible's ("reached",
// "fail") or the graph's (its deadlocks, the actions it never enables).
type DocCheck struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // "invariant", "possible", "graph"
	Verdict string `json:"verdict"`
	Detail  string `json:"detail,omitempty"`
}

// NewDocument is an empty document of command's.
func NewDocument(command string) *Document {
	return &Document{Format: ReportFormat, Command: command}
}

// AddReport adds rep's issues and actors to d, the passing partitions
// folded.
func (d *Document) AddReport(rep *Report) {
	summary := rep.Receipt()
	if d.Receipt != nil {
		summary = mergeReceipts(*d.Receipt, summary)
	}
	d.Receipt = &summary
	for _, i := range rep.Issues {
		if i.Severity == "info" && i.Kind == "actors" {
			continue // as Summary: another binding's actors, by its declaration
		}
		d.Issues = append(d.Issues, i.String())
	}
	if rep.Unsettled != "" {
		d.Notes = append(d.Notes, rep.Unsettled)
	}
	folds := map[string]int{}
	_, rest := foldPartitions(rep.Actors)
	kept := map[*Result]bool{}
	for _, r := range rest {
		kept[r] = true
	}
	for _, r := range rep.Actors {
		if kept[r] {
			d.Actors = append(d.Actors, r.Doc())
			continue
		}
		w := partitionOf(r)
		i, ok := folds[w]
		if !ok {
			i = len(d.Partitions)
			folds[w] = i
			d.Partitions = append(d.Partitions, DocPartition{Actor: w, Key: strings.SplitN(r.Partition, "=", 2)[0]})
		}
		d.Partitions[i].Pass++
		d.Partitions[i].Steps += r.Steps
	}
	for _, r := range rep.Actors {
		if w := partitionOf(r); w != "" {
			if i, ok := folds[w]; ok {
				d.Partitions[i].Of++
			}
		}
	}
}

// Doc is r as data.
func (r *Result) Doc() DocActor {
	summary := r.Receipt()
	a := DocActor{Receipt: &summary, Actor: r.Actor, Partition: r.Partition, Steps: r.Steps, Verdict: string(Pass), Reason: r.Reason(),
		Conformance: string(r.Conforms), Constants: r.Constants, JudgedBy: r.JudgedBy()}
	switch {
	case strings.HasPrefix(a.Reason, "no verdict"):
		a.Verdict = "no verdict"
	case a.Reason != "":
		a.Verdict = string(Fail)
	}
	if !r.OK() {
		a.Module, a.ITF = r.Module, r.ITF
	}
	for _, i := range r.Issues {
		a.Issues = append(a.Issues, i.String())
	}
	for _, inv := range r.Invariants {
		kind := "invariant"
		switch {
		case inv.Eventually:
			kind = "eventually"
		case inv.Within:
			kind = "within"
		}
		cause := inv.cause
		if inv.Verdict == NotEvaluated && cause == "" {
			cause = r.noVerdict
		}
		if inv.Verdict != NotEvaluated {
			cause = ""
		}
		a.Invariants = append(a.Invariants, DocInvariant{Name: inv.Name, Kind: kind, Verdict: string(inv.Verdict), Cause: cause})
	}
	for _, f := range r.failures() {
		check, verdict := "conformance", r.Conforms
		for _, inv := range r.Invariants {
			if inv.Failure == f {
				check, verdict = inv.Name, inv.Verdict
			}
		}
		a.Failures = append(a.Failures, f.Doc(check, verdict))
	}
	for i := range r.Pending {
		a.Pending = append(a.Pending, recordDoc(&r.Pending[i], -1))
	}
	for _, ab := range r.Absent {
		a.Absent = append(a.Absent, DocAbsence{Kind: ab.Kind, Name: ab.Name, Cause: string(ab.Cause), Why: ab.Why, Diagnostic: ab.Diagnostic})
	}
	return a
}

// Doc is f as data: check is what failed, verdict its verdict.
func (f *Failure) Doc(check string, verdict Verdict) DocFailure {
	d := DocFailure{Check: check, Kind: f.Kind, Verdict: string(verdict), At: f.At(), Quint: f.Call, Why: f.Why, Changes: f.Changes,
		StateAfter: f.StateAfter, Text: f.describe("", false)}
	if verdict != Fail {
		d.Verdict = string(NotEvaluated)
	}
	if f.Kind == "state" {
		d.Expect, d.ExpectTemplate = f.Expect, f.ExpectTemplate
	}
	if f.Step != nil {
		r := recordDoc(f.Step, f.Index)
		d.Record = &r
	} else {
		d.Note = f.Note
	}
	d.Brief = f.brief()
	if len(f.State) > 0 {
		d.State = map[string]string{}
		for _, k := range slices.Sorted(maps.Keys(f.State)) {
			if !strings.HasPrefix(k, "mbt::") {
				d.State[k] = f.State[k].String()
			}
		}
	}
	return d
}

// brief is f in a line (DocFailure.Brief).
func (f *Failure) brief() string {
	what := f.Call
	if f.Step != nil {
		what = f.Step.String()
	}
	if what == "" {
		what = f.At()
	}
	switch {
	case f.Why != nil && f.Why.Expected != "":
		return what + "; the model expects " + f.Why.Expected
	case f.Why != nil && len(f.Why.Conditions) > 0:
		c := f.Why.Conditions[len(f.Why.Conditions)-1]
		return what + "; the model requires " + c.Expr
	case len(f.Changes) == 1:
		return what + ": " + f.Changes[0].String()
	case len(f.Changes) > 1:
		return fmt.Sprintf("%s: %s, and %d more changes", what, f.Changes[0], len(f.Changes)-1)
	case f.Kind == "state" && f.Expect != "":
		return what + "; expected " + f.Expect
	}
	return what
}

// recordDoc is step s's record, index its place in the replay (0-based;
// -1 none).
func recordDoc(s *qtrace.Step, index int) DocRecord {
	r := DocRecord{Ambiguous: s.Ambiguous, ID: RecordID(s), Seq: s.Seq, Recorder: s.Recorder, Process: s.Process, Actor: s.Actor, Thread: s.Thread,
		Action: s.Action, Args: s.Args, Obs: s.Obs, Outcome: s.Outcome, Error: s.Error, Source: s.Source, Ref: s.Ref, Text: s.String()}
	if index >= 0 {
		r.Step = index + 1
	}
	if !s.Time.IsZero() {
		r.Time = s.Time.UTC().Format(time.RFC3339Nano)
	}
	if !s.Invoked.IsZero() {
		r.Invoked = s.Invoked.UTC().Format(time.RFC3339Nano)
	}
	r.SpanID, r.TraceID = sourceIDs(s.Source)
	return r
}

// JSON is d as indented JSON, with a newline.
func (d *Document) JSON() []byte { return marshal(d) }

// marshal is v as indented JSON with a newline, < and > as themselves
// (a Quint map's ->, read by people in a job's log).
func marshal(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil { // a document of strings, numbers and steps' scalars
		panic(fmt.Sprintf("validate: a report does not marshal: %v", err))
	}
	return b.Bytes()
}

// A testcase of JUnit's: one check of an actor, or one of simulate's.
type junitCase struct {
	XMLName   xml.Name  `xml:"testcase"`
	Name      string    `xml:"name,attr"`
	ClassName string    `xml:"classname,attr"`
	Failure   *junitMsg `xml:"failure,omitempty"`
	Skipped   *junitMsg `xml:"skipped,omitempty"`
}

type junitMsg struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

type junitSuite struct {
	XMLName  xml.Name    `xml:"testsuite"`
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Errors   int         `xml:"errors,attr"`
	Cases    []junitCase `xml:"testcase"`
	Out      string      `xml:"system-out,omitempty"`
}

// JUnit is d as a JUnit XML test suite, for CI's test views: a test
// case per actor's check (its conformance, each invariant) and per
// passing partitions' fold, or per simulate's check; a failure carries
// its text report, a check with no verdict is skipped.
func (d *Document) JUnit() []byte {
	s := junitSuite{Name: "gotogether " + d.Command}
	add := func(c junitCase) {
		s.Tests++
		switch {
		case c.Failure != nil:
			s.Failures++
		case c.Skipped != nil:
			s.Skipped++
		}
		s.Cases = append(s.Cases, c)
	}
	for _, a := range d.Actors {
		texts := map[string]DocFailure{}
		for _, f := range a.Failures {
			texts[f.Check] = f
		}
		verdictCase := func(name, verdict, cause string) junitCase {
			c := junitCase{Name: name, ClassName: a.Actor}
			f, failed := texts[name]
			switch {
			case verdict == string(Fail) && failed:
				c.Failure = &junitMsg{Message: strings.TrimSpace(firstLine(f.Text)), Type: f.Kind, Body: f.Text}
			case verdict == string(Fail):
				c.Failure = &junitMsg{Message: a.Reason, Body: a.Reason}
			case verdict == string(NotEvaluated) && failed:
				c.Skipped = &junitMsg{Message: "no verdict: " + strings.TrimSpace(firstLine(f.Text))}
			case verdict == string(NotEvaluated):
				msg := "not checked"
				if cause != "" {
					msg = "no verdict (" + cause + ")"
				}
				c.Skipped = &junitMsg{Message: msg}
			}
			return c
		}
		add(verdictCase("conformance", a.Conformance, ""))
		for _, inv := range a.Invariants {
			add(verdictCase(inv.Name, inv.Verdict, inv.Cause))
		}
	}
	for _, p := range d.Partitions {
		add(junitCase{Name: fmt.Sprintf("%d of %d partitions by %s pass (%d steps)", p.Pass, p.Of, p.Key, p.Steps), ClassName: p.Actor})
	}
	for _, c := range d.Checks {
		jc := junitCase{Name: c.Name, ClassName: d.Command + "." + c.Kind}
		if c.Verdict == "fail" {
			jc.Failure = &junitMsg{Message: firstLine(c.Detail), Body: c.Detail}
		}
		add(jc)
	}
	if d.Error != "" {
		s.Errors++
	}
	s.Out = strings.Join(append(append([]string{}, d.Notes...), d.Error), "\n")
	b, err := xml.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("validate: a JUnit report does not marshal: %v", err))
	}
	return append(append([]byte(xml.Header), b...), '\n')
}

// SARIF is d as a SARIF 2.1.0 log, for code scanning: a result per
// failure (a rule per kind: rejects, mismatch, violates, ...), located at
// its record's file and line when it was read from a step log, else at
// the binding; simulate's failing checks at the binding.
func (d *Document) SARIF() []byte {
	type region struct {
		StartLine int `json:"startLine"`
	}
	type physical struct {
		ArtifactLocation struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region *region `json:"region,omitempty"`
	}
	type location struct {
		PhysicalLocation physical `json:"physicalLocation"`
	}
	type message struct {
		Text string `json:"text"`
	}
	type result struct {
		RuleID    string         `json:"ruleId"`
		Level     string         `json:"level"`
		Message   message        `json:"message"`
		Locations []location     `json:"locations,omitempty"`
		Props     map[string]any `json:"properties,omitempty"`
	}
	type rule struct {
		ID               string  `json:"id"`
		ShortDescription message `json:"shortDescription"`
	}
	rules := map[string]string{
		"rejects":     "a recorded step the model cannot take from the state reached",
		"mismatch":    "the state after a recorded step differs from what it observed",
		"violates":    "an invariant of the model is false after a recorded step",
		"within":      "a bounded response not met in time",
		"error":       "quint could not evaluate a step",
		"no-verdict":  "a failure that may depend on lost or dropped steps: no verdict",
		"simulate":    "the model on its own does not show what its binding says",
		"tool-failed": "the command could not judge the run",
	}
	at := func(uri string, line int) []location {
		if uri == "" {
			return nil
		}
		var l location
		l.PhysicalLocation.ArtifactLocation.URI = filepath.ToSlash(uri)
		if line > 0 {
			l.PhysicalLocation.Region = &region{StartLine: line}
		}
		return []location{l}
	}
	var results []result
	for _, a := range d.Actors {
		for _, f := range a.Failures {
			id := map[string]string{"disabled": "rejects", "state": "mismatch", "invariant": "violates", "within": "within", "error": "error"}[f.Kind]
			if id == "" {
				id = "violates"
			}
			level := "error"
			if f.Verdict != string(Fail) {
				id, level = "no-verdict", "warning"
			}
			r := result{RuleID: id, Level: level, Message: message{Text: fmt.Sprintf("actor %s: %s: %s", a.Actor, f.Check, f.Text)},
				Props: map[string]any{"actor": a.Actor, "check": f.Check}}
			if rec := f.Record; rec != nil {
				r.Props["record"] = rec.ID
				if rec.Time != "" {
					r.Props["time"] = rec.Time
				}
				if rec.TraceID != "" {
					r.Props["traceId"] = rec.TraceID
				}
				if file, line, ok := fileLine(rec.Source); ok {
					r.Locations = at(file, line)
				}
			}
			if r.Locations == nil {
				r.Locations = at(d.Binding, 0)
			}
			results = append(results, r)
		}
	}
	for _, c := range d.Checks {
		if c.Verdict == "fail" {
			results = append(results, result{RuleID: "simulate", Level: "error", Message: message{Text: c.Name + ": " + c.Detail}, Locations: at(d.Binding, 0)})
		}
	}
	if d.Error != "" {
		results = append(results, result{RuleID: "tool-failed", Level: "error", Message: message{Text: d.Error}, Locations: at(d.Binding, 0)})
	}
	var rs []rule
	used := map[string]bool{}
	for _, r := range results {
		used[r.RuleID] = true
	}
	for _, id := range slices.Sorted(maps.Keys(rules)) {
		if used[id] {
			rs = append(rs, rule{ID: id, ShortDescription: message{Text: rules[id]}})
		}
	}
	type driver struct {
		Name           string `json:"name"`
		InformationURI string `json:"informationUri"`
		Rules          []rule `json:"rules"`
	}
	type run struct {
		Tool struct {
			Driver driver `json:"driver"`
		} `json:"tool"`
		Results []result `json:"results"`
	}
	var ru run
	ru.Tool.Driver = driver{Name: "gotogether", InformationURI: "https://github.com/chucklehead-dev/gotogether", Rules: rs}
	ru.Results = results
	if ru.Results == nil {
		ru.Results = []result{}
	}
	if ru.Tool.Driver.Rules == nil {
		ru.Tool.Driver.Rules = []rule{}
	}
	return marshal(map[string]any{"$schema": "https://json.schemastore.org/sarif-2.1.0.json", "version": "2.1.0", "runs": []run{ru}})
}

// fileLine splits a step log's source, path:line, as package input
// writes it.
func fileLine(src string) (string, int, bool) {
	i := strings.LastIndex(src, ":")
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(src[i+1:])
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return src[:i], n, true
}
