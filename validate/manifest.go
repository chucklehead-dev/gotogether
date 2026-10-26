package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// ManifestFormat is the format Manifest.Write writes and ReadManifest
// reads. Bump it when a field's name or meaning changes, so an old
// manifest is refused, not misread.
const ManifestFormat = "gotogether.run/1"

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
// files can be moved whole, and ReadManifest resolves them back.
type Manifest struct {
	// Format names this format, ManifestFormat: a reader refuses another.
	Format string `json:"format"`
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
	Gotogether string `json:"gotogether"`        // the module's version (build info), or its commit
	Quint      string `json:"quint,omitempty"`   // `quint --version`
	Backend    string `json:"backend,omitempty"` // quint's backend, when one was set
}

// A File is a file a run's verdict depends on, with its hash.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// RunModel is the instance a run was judged against.
type RunModel struct {
	Binding *File `json:"binding,omitempty"`
	// Spec is the model's main file (the binding's spec, or an ITF
	// input's): the files it imports are not hashed.
	Spec   *File  `json:"spec,omitempty"`
	Module string `json:"module,omitempty"`
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
	Linearize      bool   `json:"linearize,omitempty"`
	Key            string `json:"linearizeKey,omitempty"`
	Budget         int    `json:"linearizeBudget,omitempty"`
	ResolvePending bool   `json:"resolvePending,omitempty"`
	RequireSettle  bool   `json:"requireSettle,omitempty"`
	// LostPrefix and LostSuffix are the recorders whose losses were
	// declared (validate -lost-prefix, -lost-suffix), not in the logs.
	LostPrefix []string `json:"lostPrefix,omitempty"`
	LostSuffix []string `json:"lostSuffix,omitempty"`
	// Format is the inputs' format as given ("auto", "steps", ...).
	Format string `json:"format,omitempty"`
	// Instance and NoVars are an ITF input's (validate -instance,
	// -novars).
	Instance string `json:"instance,omitempty"`
	NoVars   bool   `json:"noVars,omitempty"`
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
}

// A Loss is a loss record of an input (qtrace.Step.Lost).
type Loss struct {
	Recorder string    `json:"recorder"`
	Kind     string    `json:"kind"`  // "suffix" or "prefix"
	After    uint64    `json:"after"` // the seq the loss is after (a suffix) or up to (a prefix)
	Before   time.Time `json:"before,omitzero"`
}

// RunVerdict is a run's verdict: its actors' (VerdictOf).
type RunVerdict struct {
	// Verdict is Fail if an actor fails, else NotEvaluated if one has no
	// verdict, else Pass.
	Verdict Verdict `json:"verdict"`
	// Reason is the first actor's that did not pass (Result.Reason, or
	// why it is incomplete).
	Reason  string         `json:"reason,omitempty"`
	Actors  []ActorVerdict `json:"actors"`
	Failing *FailingStep   `json:"failing,omitempty"`
}

// An ActorVerdict is one actor's verdict and why.
type ActorVerdict struct {
	Actor   string  `json:"actor"`
	Verdict Verdict `json:"verdict"`
	Reason  string  `json:"reason,omitempty"`
}

// FailingStep is the step a run's first failure is at, as recorded.
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
// screenshot, a video). Its hash is filled when it is added (AddArtifact).
type Artifact struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
}

// VerdictOf is the verdict of results, an actor's or ITF trace's each.
// incomplete, if set, gives the reason of an actor that passed but is no
// finished run (validate -require-settle, Report.Finished): it fails with
// that reason, or has no verdict ("no verdict (lost tail)").
func VerdictOf(results []*Result, incomplete map[*Result]string) RunVerdict {
	v := RunVerdict{Verdict: Pass, Actors: []ActorVerdict{}}
	for _, r := range results {
		a := ActorVerdict{Actor: r.Actor, Verdict: Pass, Reason: r.Reason()}
		if a.Actor == "" {
			a.Actor = "default" // as a step log reads a step of no actor: the replay's
		}
		switch {
		case a.Reason == "" && strings.HasPrefix(incomplete[r], "no verdict"):
			a.Verdict, a.Reason = NotEvaluated, incomplete[r]
		case a.Reason == "" && incomplete[r] != "":
			a.Verdict, a.Reason = Fail, incomplete[r]
		case strings.HasPrefix(a.Reason, "no verdict"):
			a.Verdict = NotEvaluated
		case a.Reason != "":
			a.Verdict = Fail
		}
		if a.Verdict != Pass && v.Reason == "" {
			v.Reason = a.Reason
		}
		if a.Verdict == Fail || a.Verdict == NotEvaluated && v.Verdict == Pass {
			v.Verdict = a.Verdict
		}
		if f := firstFailure(r); f != nil && f.Step != nil && v.Failing == nil && a.Verdict != Pass {
			s := f.Step
			v.Failing = &FailingStep{Actor: a.Actor, Recorder: s.Recorder, Seq: s.Seq, Action: s.Action, Ref: s.Ref}
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

// Same reports whether v and w are one verdict: overall, by actor, and at
// the same failing step (its ref aside: no verdict reads it).
func (v RunVerdict) Same(w RunVerdict) bool {
	fs := func(f *FailingStep) string {
		if f == nil {
			return ""
		}
		return fmt.Sprintf("%s|%s|%d|%s", f.Actor, f.Recorder, f.Seq, f.Action)
	}
	return v.Verdict == w.Verdict && v.Reason == w.Reason && slices.Equal(v.Actors, w.Actors) && fs(v.Failing) == fs(w.Failing)
}

func (v RunVerdict) String() string {
	s := strings.ToUpper(string(v.Verdict))
	if v.Verdict == NotEvaluated {
		s = "NO VERDICT"
	}
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

// Manifest is the manifest of results, judged by c with the searches
// opt: its tools, model and verdict. The caller adds the inputs
// (AddInput), and the driver and artifacts if it knows them. c's
// binding must have been loaded from a file (binding.Load).
func (c *Checker) Manifest(results []*Result, opt SearchOptions) (*Manifest, error) {
	m := NewManifest(c.Options)
	if err := m.SetBinding(c.Binding); err != nil {
		return nil, err
	}
	m.Check = RunCheck{Linearize: opt.Linearize || opt.Key != "", Key: opt.Key, Budget: opt.Budget, ResolvePending: opt.ResolvePending}
	m.Verdict = VerdictOf(results, nil)
	return m, nil
}

// NewManifest is an empty manifest of this format, with the tools o runs
// (gotogether's version, quint's): what Checker.Manifest starts from, and
// a caller with no checker (an ITF input's) fills.
func NewManifest(o Options) *Manifest {
	return &Manifest{Format: ManifestFormat, Created: time.Now().UTC().Truncate(time.Second),
		Tools: Tools{Gotogether: ModuleVersion(), Quint: QuintVersion(o.Quint), Backend: o.Backend}}
}

// SetBinding sets the manifest's model to b's instance: the binding and
// its spec, hashed, its module, constants and invariants. b must have
// been loaded from a file (binding.Load).
func (m *Manifest) SetBinding(b *binding.Binding) error {
	if b.Path == "" {
		return errors.New("manifest: the binding was not loaded from a file (binding.Load, NewChecker): a manifest hashes it")
	}
	var err error
	if m.Model.Binding, err = hashed(b.Path); err != nil {
		return err
	}
	if err := m.SetSpec(b.SpecPath()); err != nil {
		return err
	}
	m.Model.Module = b.Module
	m.Model.Constants = maps.Clone(b.Constants)
	m.Model.Invariants = append([]string{}, b.Invariants...)
	return nil
}

// SetSpec sets the model's spec file, hashed.
func (m *Manifest) SetSpec(path string) (err error) {
	m.Model.Spec, err = hashed(path)
	return err
}

// AddConvert sets the rule file OTLP/JSON inputs were converted with,
// hashed.
func (m *Manifest) AddConvert(path string) (err error) {
	m.Model.Convert, err = hashed(path)
	return err
}

// AddInput adds the input at path, the steps read from it (recorded
// steps; none for an ITF trace), hashing the file.
func (m *Manifest) AddInput(path, format string, steps []qtrace.Step) error {
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

// AddArtifact adds an artifact of the driver's, hashing its file.
func (m *Manifest) AddArtifact(name, path string) error {
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
	for _, f := range []hashedFile{{"the binding", m.Model.Binding}, {"the spec", m.Model.Spec}, {"the convert rules", m.Model.Convert}} {
		if f.f != nil {
			inputs = append(inputs, f)
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
// manifest hashed: a changed or missing binding, spec, rule file or input
// is an error naming each, since a replay would judge another run. A
// changed artifact (no verdict reads one) is a warning.
func (m *Manifest) Verify() (warnings []string, err error) {
	inputs, artifacts := m.files()
	var errs []error
	for _, in := range inputs {
		h, herr := hashFile(in.f.Path)
		switch {
		case herr != nil:
			errs = append(errs, fmt.Errorf("%s %s: %v: replay judges the files the manifest hashed; put it back at that path (a manifest's directory moves whole)", in.what, in.f.Path, herr))
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
		if rel, err := filepath.Rel(dir, p); err == nil && filepath.IsAbs(p) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
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

// ReadManifest reads a manifest Write wrote, its relative paths resolved
// against its directory. A file of another format is refused with the
// fix.
func ReadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w (want a run manifest, %s: gotogether validate -manifest writes one)", path, err, ManifestFormat)
	}
	if m.Format != ManifestFormat {
		return nil, fmt.Errorf("%s: a file of format %q, not a run manifest of %q: write it anew with this version (gotogether validate -manifest, or the test under GOTOGETHER_EVIDENCE)", path, m.Format, ManifestFormat)
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
	return d.Decode(&format) == nil && strings.HasPrefix(format, "gotogether.run/")
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
	c.Model.Binding, c.Model.Spec, c.Model.Convert = cp(m.Model.Binding), cp(m.Model.Spec), cp(m.Model.Convert)
	c.Inputs = slices.Clone(m.Inputs)
	c.Artifacts = slices.Clone(m.Artifacts)
	return &c
}

// paths rewrites every file path of m with f.
func (m *Manifest) paths(f func(string) string) {
	for _, p := range []*File{m.Model.Binding, m.Model.Spec, m.Model.Convert} {
		if p != nil {
			p.Path = f(p.Path)
		}
	}
	for i := range m.Inputs {
		m.Inputs[i].Path = f(m.Inputs[i].Path)
	}
	for i := range m.Artifacts {
		m.Artifacts[i].Path = f(m.Artifacts[i].Path)
	}
}

// ModuleVersion is the toolkit's version in this binary, from its build
// info: the module's version, or its commit when it is built from a
// checkout ("commit <hash>"), else "(devel)".
func ModuleVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	const path = "github.com/chucklehead-dev/gotogether"
	v := ""
	if bi.Main.Path == path {
		v = bi.Main.Version
	}
	for _, d := range bi.Deps {
		if d.Path == path {
			v = d.Version
			if d.Replace != nil {
				v = d.Replace.Version // a replace => ../..: "" (a checkout)
			}
		}
	}
	if v == "" || v == "(devel)" {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				return "commit " + s.Value
			}
		}
		return "(devel)"
	}
	return v
}

// quintVersions caches `quint --version` by binary: one run per process.
// Process-wide.
var quintVersions sync.Map

// QuintVersion is `quint --version` of the binary (default "quint"),
// or "" if it does not run.
func QuintVersion(bin string) string {
	if bin == "" {
		bin = "quint"
	}
	if v, ok := quintVersions.Load(bin); ok {
		return v.(string)
	}
	out, err := exec.Command(bin, "--version").Output()
	v := ""
	if err == nil {
		v = strings.TrimSpace(string(out))
	}
	quintVersions.Store(bin, v)
	return v
}
