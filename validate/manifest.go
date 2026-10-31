package validate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/internal/memo"
	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

// ManifestFormat is the format Manifest.Write writes and ReadManifest
// reads. Bump it when a field's name or meaning changes, so an old
// manifest is refused, not misread.
const ManifestFormat = "gotogether.run/3" // 3: compiled artifacts select evaluator-only replay

// A Manifest says what a validated run was judged on and what came of
// it, so a failure of a run gotogether did not drive (a browser
// explorer's, a production log's) travels: the tools' versions, the
// model and binding, the inputs with their hashes and losses, the
// verdict and its failing step, and the driver and artifacts the caller
// names. gotogether validate -replay judges the run again from it,
// refusing an input whose hash changed.
//
// Paths are absolute in memory. Write stores those under the manifest's
// directory relative to it, so a directory holding a manifest and its
// files can be moved whole, and ReadManifest resolves them back; Bundle
// copies the files there first.
//
// A manifest records what it is given verbatim: the driver's arguments
// and origin, absolute paths (often with a user's name), the failing
// step's ref, and its inputs are the run's whole step logs. Sanitize logs
// before sharing them (gotogether steps -pseudonymize, qtrace.Filter).
type Manifest struct {
	// Format names this format, ManifestFormat: a reader refuses another.
	Format string `json:"format"`
	// Receipt describes the actor judgments recorded in this manifest.
	Receipt *receipt.Summary `json:"receipt,omitempty"`

	// Session identifies the suite invocation, from GOTOGETHER_SESSION.
	Session string `json:"session,omitempty"`
	// Name is what ran: a test's name (qtest), the caller's (validate
	// -run-info), else the manifest's file name.
	Name string `json:"name,omitempty"`
	// Package is a test's package below its module, as evidence names it
	// ("qtest"; "_" the root), for a manifest qtest wrote.
	Package string `json:"package,omitempty"`
	// Import is a test's package's import path: evidence -expect names a
	// manifest's check by it, as an exploration's (qtest.Evidence.Import).
	Import  string     `json:"import,omitempty"`
	Created time.Time  `json:"created"`
	Tools   Tools      `json:"tools"`
	Model   RunModel   `json:"model"`
	Check   RunCheck   `json:"check"`
	Inputs  []Input    `json:"inputs"`
	Verdict RunVerdict `json:"verdict"`
	// Driver is what drove the run, if the caller says (gotogether drove
	// none of it).
	Driver *Driver `json:"driver,omitempty"`
	// Artifacts are the driver's own records of the run (its trace,
	// screenshots), named and hashed. No verdict reads them, so a changed
	// one is reported by Verify, not refused.
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// Tools are the versions a run was judged with.
type Tools struct {
	Gotogether string `json:"gotogether"`      // the module's version (build info), or its commit
	Quint      string `json:"quint,omitempty"` // `quint --version`
	// Backend is the quint backend chosen (Options.Backend, else
	// QUINT_BACKEND); "" is validate's default (rust, typescript for a
	// batch's chunk). Verdict cache keys depend on it, so a replay judges
	// on it.
	Backend string `json:"backend,omitempty"`
}

// A File is a file a run's verdict depends on, with its hash.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// RunModel is the instance a run was judged against.
type RunModel struct {
	// Compiled selects evaluator-only replay by this hashed artifact; nil uses Quint.
	Compiled *File `json:"compiled,omitempty"`
	Binding  *File `json:"binding,omitempty"`
	// Spec is the model's main file (the binding's spec, or an ITF
	// input's), and Imports the files it imports, transitively, as quint
	// resolves them (relative to the importing file).
	Spec    *File  `json:"spec,omitempty"`
	Imports []File `json:"imports,omitempty"`
	Module  string `json:"module,omitempty"`
	// Instance is an ITF trace's instance module (validate -instance), if
	// it was judged against one rather than constants.
	Instance string `json:"instance,omitempty"`
	// Convert is the rule file OTLP/JSON inputs were converted with.
	Convert *File `json:"convert,omitempty"`
	// Constants are the instance's, the binding's with any override;
	// Invariants those checked (the binding's, or an override).
	Constants  map[string]string `json:"constants,omitempty"`
	Invariants []string          `json:"invariants"`
}

// RunCheck is how a run was judged, beyond the model: what validate's
// flags (or qtest's options) asked for.
type RunCheck struct {
	// ChoiceLimit is the effective finite model-choice expansion bound.
	ChoiceLimit    int    `json:"choiceLimit"`
	Linearize      bool   `json:"linearize,omitempty"`
	Key            string `json:"linearizeKey,omitempty"`
	Budget         int    `json:"linearizeBudget,omitempty"`
	ResolvePending bool   `json:"resolvePending,omitempty"`
	// RequireFinish: the finish check was judged (validate -require-finish,
	// or a complete binding's default in the CLI; qtest's Validate and
	// Record judge none, a test claims Finishes). A replay judges it as
	// recorded, whatever the binding's complete says now.
	RequireFinish bool `json:"requireFinish,omitempty"`
	// LostPrefix and LostSuffix are the recorders whose losses were
	// declared (validate -lost-prefix, -lost-suffix), not in the logs.
	LostPrefix []string `json:"lostPrefix,omitempty"`
	LostSuffix []string `json:"lostSuffix,omitempty"`
	// CompleteThrough are the watermarks stated (validate
	// -complete-through), "recorder=T" each, not in the logs.
	CompleteThrough []string `json:"completeThrough,omitempty"`
	// Format is the inputs' format as given ("auto", "steps", ...).
	Format string `json:"format,omitempty"`
	// NoVars is an ITF input's (validate -novars).
	NoVars bool `json:"noVars,omitempty"`
}

// SearchOptions are the searches the check asked for.
func (c RunCheck) SearchOptions() SearchOptions {
	return SearchOptions{Linearize: c.Linearize, Key: c.Key, Budget: c.Budget, ResolvePending: c.ResolvePending}
}

// An Input is a step log (or OTLP/JSON, or ITF) a run was judged on.
type Input struct {
	File
	Format string `json:"format,omitempty"` // "steps", "otlp", "itf"
	Steps  int    `json:"steps"`            // model steps read from it (records aside)
	// Lost are the loss records it holds (a torn tail's, MarkLost's
	// written into it): what its steps cannot show.
	Lost []Loss `json:"lost,omitempty"`
	// Model is an ITF input's instance (ITFModel): the spec its trace
	// names (or the one given), hashed with its imports, its module,
	// constants and invariants.
	Model *RunModel `json:"model,omitempty"`
}

// A Loss is a loss record of an input (qtrace.Step.Lost).
type Loss struct {
	Recorder string    `json:"recorder"`
	Kind     string    `json:"kind"`  // "suffix" or "prefix"
	After    uint64    `json:"after"` // the seq the loss is after (a suffix) or up to (a prefix)
	Before   time.Time `json:"before,omitzero"`
}

// RunVerdict is a run's verdict: its actors' (Report.Verdict).
type RunVerdict struct {
	// Verdict is Fail if an actor fails, else NotEvaluated if one has no
	// verdict, else Pass.
	Verdict Verdict `json:"verdict"`
	// Reason is the first failing actor's (Result.Reason, or why it is
	// incomplete), else the first's with no verdict.
	Reason  string         `json:"reason,omitempty"`
	Actors  []ActorVerdict `json:"actors"`
	Failing *FailingStep   `json:"failing,omitempty"`
}

// An ActorVerdict is one actor's verdict and why.
type ActorVerdict struct {
	Actor   string  `json:"actor"`
	Verdict Verdict `json:"verdict"`
	Reason  string  `json:"reason,omitempty"`
	// Action is the action of the step a failing actor fails at (its
	// mismatch's, its rejection's, its violation's), as its reason names
	// it: RunVerdict.Same compares it for every actor, not only the
	// first, whatever the reason's wording.
	Action string `json:"action,omitempty"`
	// JudgedBy is who passed a passing actor when quint did not alone
	// (Result.JudgedBy: "evaluator", the Go evaluator with no quint
	// replay; "both", a pass quint sampled and agreed with): a pass of
	// the evaluator's alone is no quint verdict.
	JudgedBy string `json:"judgedBy,omitempty"`
}

// FailingStep is the step a run's first failure is at, as recorded: the
// actor's whose reason is the run's (RunVerdict.Reason).
type FailingStep struct {
	Actor    string `json:"actor"`
	Recorder string `json:"recorder"`
	Seq      uint64 `json:"seq"`
	Action   string `json:"action"`
	Ref      string `json:"ref,omitempty"` // qtrace.Step.Ref: the driver's record of it
}

// Driver is what drove a run: the caller fills it.
type Driver struct {
	Name    string   `json:"name"`
	Version string   `json:"version,omitempty"`
	Seed    string   `json:"seed,omitempty"`
	Origin  string   `json:"origin,omitempty"` // where it ran: a CI job's URL, a host
	Args    []string `json:"args,omitempty"`
}

// An Artifact is a record the driver kept of the run (its trace, a
// screenshot, a video). Its hash is filled when the manifest is made (NewManifest).
type Artifact struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
}

// Verdict is the report's verdict, its actors' together: the first
// failing actor's reason and failing step, else the first with no
// verdict's (Result.Reason, Unfinished included), else a pass.
func (r *Report) Verdict() RunVerdict {
	v := runVerdictOf(r.Actors)
	if r.Composition != nil {
		status, reason := r.Composition.Problem()
		if v.Verdict != Fail && status != receipt.Pass {
			if status == receipt.Fail || status == receipt.Error {
				v.Verdict = Fail
			} else {
				v.Verdict = NotEvaluated
			}
			v.Reason = "composition: " + reason
			v.Failing = nil
		}
	}
	return v
}

// runVerdictOf is the verdict of results, an actor's or ITF trace's each.
func runVerdictOf(results []*Result) RunVerdict {
	v := RunVerdict{Verdict: Pass, Actors: []ActorVerdict{}}
	for _, r := range results {
		a := ActorVerdict{Actor: r.Actor, Verdict: Pass, Reason: r.Reason()}
		if a.Actor == "" {
			a.Actor = "default" // as a step log reads a step of no actor: the replay's
		}
		switch {
		case strings.HasPrefix(a.Reason, "no verdict"):
			a.Verdict = NotEvaluated
		case a.Reason != "":
			a.Verdict = Fail
		}
		if a.Verdict == Fail {
			a.Action = r.failingAction()
		}
		if by := r.JudgedBy(); a.Verdict == Pass && by != "quint" {
			a.JudgedBy = by
		}
		// the run's reason and failing step are its first actor's of the
		// run's verdict: a defect anywhere is the run's, ahead of an
		// earlier actor's no verdict (which once gave a FAIL its reason)
		if a.Verdict == Fail && v.Verdict != Fail || a.Verdict == NotEvaluated && v.Verdict == Pass {
			v.Verdict, v.Reason, v.Failing = a.Verdict, a.Reason, nil
			if f := firstFailure(r); f != nil && f.Step != nil {
				s := f.Step
				v.Failing = &FailingStep{Actor: a.Actor, Recorder: s.Recorder, Seq: s.Seq, Action: s.Action, Ref: s.Ref}
			}
		}
		v.Actors = append(v.Actors, a)
	}
	if len(results) == 0 {
		v.Verdict, v.Reason = NotEvaluated, "nothing was checked"
	}
	return v
}

// firstFailure is r's conformance failure, else its first invariant's.
func firstFailure(r *Result) *Failure {
	if fs := r.failures(); len(fs) > 0 {
		return fs[0]
	}
	return nil
}

// Same reports whether v and w are one verdict: overall, by actor, the
// kind of each actor's claim (a mismatch, a rejection, which invariant
// is violated, incomplete or no verdict and why: no settle or pending
// calls, a lost tail or uncollected steps) and the action each failing
// actor fails at, and at the same failing step (its ref aside: no
// verdict reads it). Reasons worded otherwise, as a newer gotogether may
// word them, are the same verdict: note says so. When they are not one
// verdict, note says where they differ.
func (v RunVerdict) Same(w RunVerdict) (same bool, note string) {
	fs := func(f *FailingStep) string {
		if f == nil {
			return "none"
		}
		return fmt.Sprintf("%s at %s, recorder %s seq %d", f.Actor, f.Action, f.Recorder, f.Seq)
	}
	switch {
	case v.Verdict != w.Verdict:
		return false, fmt.Sprintf("the verdict is %s, now %s", verdictWord(v.Verdict), verdictWord(w.Verdict))
	case reasonKind(v.Reason) != reasonKind(w.Reason):
		return false, fmt.Sprintf("the reason is %q, now %q", v.Reason, w.Reason)
	case len(v.Actors) != len(w.Actors):
		return false, fmt.Sprintf("%d actors were judged, now %d", len(v.Actors), len(w.Actors))
	case fs(v.Failing) != fs(w.Failing):
		return false, fmt.Sprintf("the first failing step is %s, now %s", fs(v.Failing), fs(w.Failing))
	}
	worded := v.Reason != w.Reason
	for i, a := range v.Actors {
		b := w.Actors[i]
		switch {
		case a.Actor != b.Actor:
			return false, fmt.Sprintf("actor %d is %s, now %s", i+1, a.Actor, b.Actor)
		case a.Verdict != b.Verdict || reasonKind(a.Reason) != reasonKind(b.Reason):
			return false, fmt.Sprintf("actor %s: %s (%s), now %s (%s)", a.Actor, verdictWord(a.Verdict), a.Reason, verdictWord(b.Verdict), b.Reason)
		case a.failingAction() != b.failingAction():
			return false, fmt.Sprintf("actor %s fails at %s (%s), now at %s (%s)", a.Actor, a.failingAction(), a.Reason, b.failingAction(), b.Reason)
		}
		worded = worded || a.Reason != b.Reason
	}
	if worded {
		note = fmt.Sprintf("the reasons are worded otherwise (%q, now %q): the same kind of verdict, said by another gotogether", v.Reason, w.Reason)
	}
	return true, note
}

// failingAction is the action of the step r first fails at, its
// conformance's or an invariant's (ActorVerdict.Action): the step's, or
// for an inferred step (a restart) the model's call's, as Reason names
// it; "" if none.
func (r *Result) failingAction() string {
	switch f := firstFailure(r); {
	case f != nil && f.Step != nil:
		return f.Step.Action
	case f != nil && f.Call != "":
		action, _, _ := strings.Cut(f.Call, "(")
		return action
	}
	return ""
}

// failingAction is the action a failing actor fails at: Action, or for
// a manifest written before Action was, the one its reason names.
func (a ActorVerdict) failingAction() string {
	if a.Verdict != Fail || a.Action != "" {
		return a.Action
	}
	for _, p := range []string{"mismatch after ", "rejects ", "evaluation error at "} {
		if act, ok := strings.CutPrefix(a.Reason, p); ok {
			return act
		}
	}
	return ""
}

// reasonKind is the kind of claim a reason states: its first word (a
// mismatch, a rejection), "evaluation error", for a violation the
// invariant violated, and for incomplete and no verdict their cause
// (causeKind): a run that never settled is not one with pending calls,
// nor a lost tail uncollected steps.
func reasonKind(r string) string {
	switch {
	case strings.HasPrefix(r, "violates "):
		return r
	case strings.HasPrefix(r, "no verdict"):
		return "no verdict" + causeKind(strings.TrimPrefix(r, "no verdict"))
	case strings.HasPrefix(r, "incomplete"):
		return "incomplete" + causeKind(strings.TrimPrefix(r, "incomplete"))
	case strings.HasPrefix(r, "evaluation error"):
		return "evaluation error"
	}
	k, _, _ := strings.Cut(r, " ")
	return k
}

// causes are the causes of incomplete and no verdict, each by the words
// that name it, the more particular first (a lost tail before lost
// steps): a cause reworded keeps its words, and one gotogether does not
// know yet is compared as worded. Read-only.
var causes = []struct {
	kind  string
	words []string
}{
	{"lost tail", []string{"tail"}},
	{"uncollected", []string{"uncollected"}},
	{"dropped steps", []string{"dropped"}},
	{"lost steps", []string{"lost"}},
	{"pending calls", []string{"pending"}},
	{"no settle", []string{"settle"}},
	{"eventual val false", []string{"eventual", "false"}},
	{"no eventual val", []string{"eventual"}},
	{"linearize budget", []string{"budget"}},
	{"snapshot overlap", []string{"overlap"}},
	{"conformance n/a", []string{"conformance"}},
	{"ITF trace", []string{"itf"}},
}

// causeKind is the cause of the rest of an incomplete or no verdict
// reason, " (no settle)": " (" its kind ")", "" for none.
func causeKind(rest string) string {
	c, ok := strings.CutPrefix(strings.TrimSpace(rest), "(")
	if !ok {
		return ""
	}
	c, _, _ = strings.Cut(c, ")")
	lc := strings.ToLower(c)
	for _, k := range causes {
		all := true
		for _, w := range k.words {
			all = all && strings.Contains(lc, w)
		}
		if all {
			return " (" + k.kind + ")"
		}
	}
	return " (" + c + ")"
}

// verdictWord is a verdict as a result line says it: PASS, FAIL, NO VERDICT.
func verdictWord(v Verdict) string {
	if v == NotEvaluated {
		return "NO VERDICT"
	}
	return strings.ToUpper(string(v))
}

func (v RunVerdict) String() string {
	s := verdictWord(v.Verdict)
	if v.Reason != "" {
		s += " (" + v.Reason + ")"
	}
	if f := v.Failing; f != nil {
		s += fmt.Sprintf(" at %s, recorder %s seq %d (actor %s)", f.Action, f.Recorder, f.Seq, f.Actor)
		if f.Ref != "" {
			s += ", ref " + f.Ref
		}
	}
	return s
}

// A RunRecord is what a manifest is made of (NewManifest): how the run
// was judged, its results, and the files its verdict depends on, each
// hashed as the manifest is made.
type RunRecord struct {
	// Compiled is the evaluator-only artifact used to judge the recorded steps.
	Compiled string
	// Composition contains additional checks registered for the same history.
	Composition *receipt.Composition
	// Options are the tools the run was judged with: quint's version and
	// the backend go in the manifest.
	Options Options
	// Binding is the instance judged: the binding and its spec, hashed,
	// its module, constants and invariants (a Checker's, with any
	// override). It must have been loaded from a file (binding.Load). nil:
	// ITF inputs alone, each naming its own (RunInput.Model).
	Binding *binding.Binding
	// Check is how the run was judged beyond the model, and Search its
	// searches (filling Check's).
	Check  RunCheck
	Search SearchOptions
	// Results are every actor's and ITF trace's: the verdict's
	// (Report.Verdict).
	Results []*Result
	// Inputs are the files judged, and Convert the rule file OTLP/JSON
	// ones were converted with ("": none).
	Inputs  []RunInput
	Convert string
	// Artifacts are the driver's records of the run, by Name and Path.
	Artifacts []Artifact
}

// A RunInput is one input of a RunRecord: its file, its format
// ("steps", "otlp", "itf"), the steps read from it (none for ITF), and
// an ITF trace's instance (ITFModel).
type RunInput struct {
	Path, Format string
	Steps        []qtrace.Step
	Model        *RunModel
}

// NewManifest is the manifest of the run r records (gotogether.run/3):
// the tools, the instance, how it was judged, each input and artifact
// hashed with the inputs' losses, and the verdict. The caller names the
// run (Name, Package, Import) and its Driver. A file that cannot be read
// is an error.
func NewManifest(r RunRecord) (*Manifest, error) {
	for _, result := range r.Results {
		if result != nil && result.compiled != (r.Compiled != "") {
			return nil, errors.New("manifest: judgment evaluation mode does not match RunRecord.Compiled: provide the exact compiled artifact used by compiled results, and none for source results")
		}
	}
	var m *Manifest
	if r.Compiled != "" {
		m = &Manifest{Session: os.Getenv("GOTOGETHER_SESSION"), Format: ManifestFormat, Created: time.Now().UTC().Truncate(time.Second), Tools: Tools{Gotogether: toolkitVersion()}}
	} else {
		m = emptyManifest(r.Options)
	}
	m.Model.Invariants = []string{}
	if r.Compiled != "" {
		if err := m.setCompiledBinding(r.Binding, r.Compiled); err != nil {
			return nil, err
		}
	} else if r.Binding != nil {
		if err := m.setBinding(r.Binding); err != nil {
			return nil, err
		}
	}
	if r.Compiled != "" {
		if err := m.matchCompiledResults(r.Results); err != nil {
			return nil, err
		}
	}
	m.Check = r.Check
	m.Check.ChoiceLimit = r.Options.ChoiceLimit
	if m.Check.ChoiceLimit == 0 {
		m.Check.ChoiceLimit = DefaultChoiceLimit
	}
	o := r.Search
	m.Check.Linearize, m.Check.Key, m.Check.Budget, m.Check.ResolvePending = o.Linearize || o.Key != "", o.Key, o.Budget, o.ResolvePending
	report := &Report{Actors: r.Results, Composition: r.Composition}
	m.Verdict = report.Verdict()
	summary := report.Receipt()
	m.Receipt = &summary
	if r.Convert != "" {
		var err error
		if m.Model.Convert, err = hashed(r.Convert); err != nil {
			return nil, err
		}
	}
	for _, in := range r.Inputs {
		if err := m.addInput(in.Path, in.Format, in.Steps); err != nil {
			return nil, err
		}
		m.Inputs[len(m.Inputs)-1].Model = in.Model
	}
	for _, a := range r.Artifacts {
		if err := m.addArtifact(a.Name, a.Path); err != nil {
			return nil, fmt.Errorf("artifact %s: %w", a.Name, err)
		}
	}
	if err := m.checkEvaluation(); err != nil {
		return nil, err
	}
	return m, nil
}

// emptyManifest is a manifest of this format, with the tools o runs
// (gotogether's version, quint's).
func emptyManifest(o Options) *Manifest {
	backend := o.envBackend()
	return &Manifest{Session: os.Getenv("GOTOGETHER_SESSION"), Format: ManifestFormat, Created: time.Now().UTC().Truncate(time.Second),
		Tools: Tools{Gotogether: toolkitVersion(), Quint: quintVersion(o.Quint), Backend: backend}}
}

// CurrentTools are the tools a run judged with o would be judged with
// now: what a replay compares with its manifest's.
func CurrentTools(o Options) Tools { return emptyManifest(o).Tools }

// setBinding sets the manifest's model to b's instance: the binding and
// its spec, hashed, its module, constants and invariants.
func (m *Manifest) setBinding(b *binding.Binding) error {
	if b.Path == "" {
		return errors.New("manifest: the binding was not loaded from a file (binding.Load, NewChecker): a manifest hashes it")
	}
	var err error
	if m.Model.Binding, err = hashed(b.Path); err != nil {
		return err
	}
	if m.Model.Spec, m.Model.Imports, err = specFiles(b.SpecPath()); err != nil {
		return err
	}
	m.Model.Module = b.Module
	m.Model.Constants = maps.Clone(b.Constants)
	m.Model.Invariants = append([]string{}, b.Invariants...)
	return nil
}

// addInput adds the input at path, the steps read from it (recorded
// steps; none for an ITF trace), hashing the file.
func (m *Manifest) addInput(path, format string, steps []qtrace.Step) error {
	f, err := hashed(path)
	if err != nil {
		return err
	}
	in := Input{File: *f, Format: format}
	for _, s := range steps {
		switch {
		case s.Lost != "":
			in.Lost = append(in.Lost, Loss{Recorder: s.Recorder, Kind: s.Lost, After: s.LostAfter, Before: s.LostBefore})
		case s.Model():
			in.Steps++
		}
	}
	m.Inputs = append(m.Inputs, in)
	return nil
}

// addArtifact adds an artifact of the driver's, hashing its file.
func (m *Manifest) addArtifact(name, path string) error {
	f, err := hashed(path)
	if err != nil {
		return err
	}
	m.Artifacts = append(m.Artifacts, Artifact{Name: name, Path: f.Path, SHA256: f.SHA256})
	return nil
}

// hashed is the file at path, made absolute, with its hash.
func hashed(path string) (*File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	h, err := hashFile(abs)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	return &File{Path: abs, SHA256: h}, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// A hashedFile is a file a verdict depends on, named for messages.
type hashedFile struct {
	what string
	f    *File
}

// files are the manifest's files: the hashed ones a verdict depends on,
// then its artifacts.
func (m *Manifest) files() (inputs []hashedFile, artifacts []*Artifact) {
	for _, md := range m.models() { // the specs and imports Verify checks as the replay resolves them (verifyModel)
		for _, f := range []hashedFile{{"the binding", md.Binding}, {"the compiled model", md.Compiled}, {"the convert rules", md.Convert}} {
			if f.f != nil {
				inputs = append(inputs, f)
			}
		}
	}
	for i := range m.Inputs {
		inputs = append(inputs, hashedFile{"the input", &m.Inputs[i].File})
	}
	for i := range m.Artifacts {
		artifacts = append(artifacts, &m.Artifacts[i])
	}
	return inputs, artifacts
}

// Verify checks that the files the run was judged on are the ones the
// manifest hashed: a changed or missing binding, spec, import, rule file
// or input is an error naming each, since a replay would judge another
// run. The spec checked is the one the replay judges: the binding's spec
// as it resolves now (a moved binding's relative spec: another file,
// perhaps), and its imports as quint resolves them. A changed artifact
// (no verdict reads one) is a warning.
func (m *Manifest) Verify() (warnings []string, err error) {
	inputs, artifacts := m.files()
	var errs []error
	if err := m.checkEvaluation(); err != nil {
		errs = append(errs, err)
	}
	for _, md := range m.models() {
		errs = append(errs, md.verify()...)
	}
	for _, in := range inputs {
		h, herr := hashFile(in.f.Path)
		switch {
		case herr != nil:
			errs = append(errs, fmt.Errorf("%s %s: %w: replay judges the files the manifest hashed; put it back at that path (a manifest's directory moves whole)", in.what, in.f.Path, herr))
		case h != in.f.SHA256:
			errs = append(errs, fmt.Errorf("%s %s changed since the run was judged (sha256 %s, now %s): replay judges the files the manifest hashed; restore it, or validate the new files anew (gotogether validate, -manifest for a new manifest)",
				in.what, in.f.Path, short(in.f.SHA256), short(h)))
		}
	}
	for _, a := range artifacts {
		if a.SHA256 == "" {
			continue
		}
		if h, herr := hashFile(a.Path); herr != nil || h != a.SHA256 {
			warnings = append(warnings, fmt.Sprintf("artifact %s (%s) is missing or changed since the run: no verdict reads it, but it may no longer show the run", a.Name, a.Path))
		}
	}
	return warnings, errors.Join(errs...)
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// Write writes the manifest to path as JSON: paths under path's
// directory relative to it, others absolute.
func (m *Manifest) Write(path string) error {
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return err
	}
	c := m.copy()
	c.paths(func(p string) string {
		if rel, err := filepath.Rel(dir, p); err == nil && filepath.IsAbs(p) && within(dir, p) {
			return filepath.ToSlash(rel)
		}
		return p
	})
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ErrManifestFormat is ReadManifest's error (wrapped) for a manifest of
// another format version: a tool listing manifests skips it, naming it.
// Read-only.
var ErrManifestFormat = errors.New("another manifest format")

// ReadManifest reads a manifest Write wrote, its relative paths resolved
// against its directory. A file of another format is refused with the
// fix (ErrManifestFormat).
func ReadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w (want a run manifest, %s: gotogether validate -manifest writes one)", path, err, ManifestFormat)
	}
	if _, err := formats.Manifest.Check(m.Format); err != nil {
		if formats.Manifest.Is(m.Format) && !formats.Corpus.Is(m.Format) { // another version's
			return nil, fmt.Errorf("%s: %w: %w", path, ErrManifestFormat, err)
		}
		return nil, fmt.Errorf("%s: %w: a file of format %q, not a run manifest of %q: write it anew with this version (gotogether validate -manifest, or the test under GOTOGETHER_EVIDENCE)", path, ErrManifestFormat, m.Format, ManifestFormat)
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	m.paths(func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(dir, filepath.FromSlash(p))
	})
	if err := m.checkEvaluation(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// IsManifest reports whether the JSON file at path is a run manifest of
// any format version: what a tool walking a directory of evidence reads.
func IsManifest(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	d := json.NewDecoder(f)
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	if key, err := d.Token(); err != nil || key != "format" {
		return false
	}
	var format string
	return d.Decode(&format) == nil && formats.Manifest.Is(format)
}

// copy is a copy of m whose files can be rewritten.
func (m *Manifest) copy() *Manifest {
	c := *m
	cp := func(f *File) *File {
		if f == nil {
			return nil
		}
		g := *f
		return &g
	}
	cpModel := func(r RunModel) RunModel {
		r.Binding, r.Spec, r.Convert, r.Compiled = cp(r.Binding), cp(r.Spec), cp(r.Convert), cp(r.Compiled)
		r.Imports = slices.Clone(r.Imports)
		return r
	}
	c.Model = cpModel(m.Model)
	c.Inputs = slices.Clone(m.Inputs)
	for i := range c.Inputs {
		if im := c.Inputs[i].Model; im != nil {
			cm := cpModel(*im)
			c.Inputs[i].Model = &cm
		}
	}
	c.Artifacts = slices.Clone(m.Artifacts)
	return &c
}

// models are m's models: the run's, then each ITF input's.
func (m *Manifest) models() []*RunModel {
	out := []*RunModel{&m.Model}
	for i := range m.Inputs {
		if im := m.Inputs[i].Model; im != nil {
			out = append(out, im)
		}
	}
	return out
}

// paths rewrites every file path of m with f.
func (m *Manifest) paths(f func(string) string) {
	for _, md := range m.models() {
		for _, p := range []*File{md.Binding, md.Spec, md.Convert, md.Compiled} {
			if p != nil {
				p.Path = f(p.Path)
			}
		}
		for i := range md.Imports {
			md.Imports[i].Path = f(md.Imports[i].Path)
		}
	}
	for i := range m.Inputs {
		m.Inputs[i].Path = f(m.Inputs[i].Path)
	}
	for i := range m.Artifacts {
		m.Artifacts[i].Path = f(m.Artifacts[i].Path)
	}
}

// toolkitVersion is the toolkit's version in this binary, from its build
// info (moduleVersion).
func toolkitVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return moduleVersion(bi)
}

// moduleVersion is the toolkit's version in a binary of build info bi:
// a dependency's version (a consumer's binary, its test binaries
// included), a replaced one's replacement ("(devel: ../..)" for a
// directory); the main module's version, or its commit when built from a
// checkout ("commit <hash>"); else "(devel)", as the repository's own
// test binaries are, which carry no version control stamp.
func moduleVersion(bi *debug.BuildInfo) string {
	const path = "github.com/chucklehead-dev/gotogether"
	for _, d := range bi.Deps {
		if d.Path != path {
			continue
		}
		if r := d.Replace; r != nil {
			if r.Version != "" {
				return r.Path + "@" + r.Version
			}
			return "(devel: " + r.Path + ")"
		}
		return d.Version
	}
	if bi.Main.Path == path && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	if bi.Main.Path == path {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				return "commit " + s.Value
			}
		}
	}
	return "(devel)"
}

// quintVersions caches `quint --version` by binary: one run per process.
// Process-wide.
var quintVersions memo.Map[string, string]

// quintVersion is `quint --version` of the binary (default "quint"),
// or "" if it does not run.
func quintVersion(bin string) string {
	if bin == "" {
		bin = "quint"
	}
	return quintVersions.Once(bin, func() string {
		out, err := quintcmd.Command(context.Background(), bin, "", "--version").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	})
}
