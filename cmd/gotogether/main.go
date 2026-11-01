// Command gotogether checks Go programs against Quint models from their
// telemetry.
//
//	gotogether init     -kind http NAME                    a new HTTP service's project: model, binding, simulated and real tests, a mutant
//	gotogether init     -kind library NAME                 a synchronous library, observed snapshot checks, model and source mutant
//	gotogether scaffold -spec model.qnt -module M -invariants safe [-eventually done]
//	                                                    a binding with explicitly selected properties (-out FILE: written there)
//	gotogether scaffold -spec model.qnt -module M -no-properties
//	                                                    an explicitly conformance-only binding skeleton
//	gotogether scaffold -system -binding b.yaml [-pkg p]   a qtest.System test for the binding (-out FILE; -http: on qenv/qhttp)
//	gotogether lint     -binding b.yaml [-src DIR]         check a binding (and annotations, literal Record actions) against the model
//	gotogether lint     -sim DIR                           check simulation code for goroutines, wall-clock time and steps that escape it
//	gotogether lint     -weave DIR                         check code woven by qenv/weave for go statements and method values it cannot rewrite
//	gotogether steps    FILE...                            print the reconstructed per-actor traces
//	gotogether validate [-binding b.yaml] [-dir OUT] FILE...
//	                                                    reconstruct (or read), replay in Quint, report
//	gotogether validate -replay run.json               judge a run again from its manifest (validate -manifest's)
//	gotogether bundle -out DIR run.json                verified portable evidence, with its native manifest
//	gotogether shrink   -binding b.yaml -actions a.json -- DRIVER {actions} {steps}
//	                                                    shrink a driver's failing actions by rerunning it
//	gotogether itf      -binding b.yaml -out DIR FILE...   recorded steps -> model-level ITF, per actor
//	gotogether convert  -rules r.yaml [-out steps.jsonl] FILE...
//	                                                    an app's own OTLP/JSON spans -> a native step log
//	gotogether skills   [list | install [-target T] [-user] [-dir DIR] [-force]]
//	                                                    the toolkit's agent skills, into .agents/skills and .claude/skills
//
// FILE is detected by content: OTLP/JSON (e.g. the collector's file
// exporter output), a native step log (qobs.JSONLSink), or a model-level
// ITF trace (gotogether's, or `quint run --mbt` output with -spec/-module/-instance).
// Exit status of validate: 0 all actors conform and satisfy the invariants,
// 1 some do not, 2 usage or tool error.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/convert"
	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/internal/quintnames"
	"github.com/chucklehead-dev/gotogether/otelio"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/skills"
	"github.com/chucklehead-dev/gotogether/validate"
	"github.com/chucklehead-dev/gotogether/validate/receipt"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	code := 0
	switch os.Args[1] {
	case "init":
		err = runInit(os.Stdout, os.Stderr, os.Args[2:])
	case "scaffold":
		err = scaffold(os.Args[2:])
	case "lint":
		code, err = lint(os.Args[2:])
	case "steps":
		err = steps(os.Args[2:])
	case "validate":
		code, err = runValidate(os.Args[2:])
	case "bundle":
		err = bundleTo(os.Stdout, os.Args[2:])
	case "itf":
		err = toITF(os.Args[2:])
	case "convert":
		err = runConvert(os.Args[2:])
	case "skills":
		err = runSkills(os.Args[2:])
	case "cover":
		err = runCover(os.Args[2:])
	case "simulate":
		code, err = simulate(os.Args[2:])
	case "evidence":
		code, err = evidence(os.Args[2:])
	case "shrink":
		code, err = shrinkTo(os.Stdout, os.Args[2:])
	case "nav":
		code, err = nav(os.Stdout, os.Args[2:])
	case "compile":
		err = compileTo(os.Stdout, os.Args[2:])
	case "doctor":
		code, err = doctor(os.Stdout, os.Args[2:])
	case "help", "-h", "-help", "--help":
		help()
	case "version":
		fmt.Println("gotogether", version())
	default:
		fmt.Fprintln(os.Stderr, "gotogether:", unknownCommand(os.Args[1]))
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gotogether:", err)
		os.Exit(2)
	}
	os.Exit(code)
}

// usage prints the commands to standard error and exits 2: the command
// line was wrong.
func usage() {
	usageTo(os.Stderr)
	os.Exit(2)
}

func usageTo(w io.Writer) {
	fmt.Fprint(w, `gotogether checks Go code against a Quint model. A new HTTP service: init, below; code you have:
  gotogether scaffold -spec model.qnt -module M -invariants safe -out m.binding.yaml    (choose property names; set constants)
  gotogether lint -binding m.binding.yaml && gotogether simulate -binding m.binding.yaml
  gotogether scaffold -system -binding m.binding.yaml -out system_test.go    (-http: an HTTP service on qenv/qhttp)
grist generates a model and bindings: go run github.com/chucklehead-dev/gotogether/grist/cmd/grist@latest new <name> (docs/grist.md)
commands (gotogether <command> -h: what it does, an example, every flag):
`)
	for _, name := range helpOrder {
		c := commands[name]
		ex, _, _ := strings.Cut(c.example, "\n")
		fmt.Fprintf(w, "  %-9s %s\n            %s\n", name, c.short, ex)
	}
	fmt.Fprintln(w, "  version   the toolkit's version")
	fmt.Fprintln(w, "validate, steps and itf also take -convert r.yaml to convert OTLP/JSON spans on the way in.")
}

// callForm is a possible's call of action a naming its last parameter
// (a result, an outcome, often), its value left for the newcomer: the
// form that says which way the action goes.
func callForm(a string, ps []binding.Param) string {
	if len(ps) == 0 {
		return a
	}
	p := ps[len(ps)-1]
	v := "0"
	switch {
	case p.Type == "str":
		v = `"?"`
	case p.Type == "bool":
		v = "true"
	}
	return fmt.Sprintf("%s(%s = %s)", a, p.Name, v)
}

// packageIn is the package of the Go files in dir, a test's package
// without its _test, or "app" when there are none: a scaffolded test in
// another package's directory would not build.
func packageIn(dir string) string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, f := range files {
		if p, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.PackageClauseOnly); err == nil {
			return strings.TrimSuffix(p.Name.Name, "_test")
		}
	}
	return "app"
}

func scaffold(args []string) error {
	fset := commandFlags("scaffold")
	spec := fset.String("spec", "", "Quint spec")
	module := fset.String("module", "", "the model's module (its name after `module` in the spec)")
	system := fset.Bool("system", false, "print a qtest.System test for -binding instead of a binding")
	http := fset.Bool("http", false, "with -system: an HTTP service on qenv/qhttp (its server's process first, a client that retries a refused connection)")
	bpath := fset.String("binding", "", "binding file (with -system)")
	pkg := fset.String("pkg", "", "package of the test (with -system; default: that of the Go files in -out's directory, else app)")
	quint := fset.String("quint", "quint", "quint binary")
	out := fset.String("out", "", "write to this file (not overwritten) instead of standard output")
	invariants := fset.String("invariants", "", "comma-separated Boolean vals checked after every observed step")
	eventually := fset.String("eventually", "", "comma-separated Boolean vals checked only when the run settles")
	holds := fset.String("holds", "", "selected invariants the model guarantees (optional model-only classification)")
	allows := fset.String("allows", "", "selected invariants the model permits to fail (optional model-only classification)")
	noProperties := fset.Bool("no-properties", false, "explicitly scaffold conformance-only validation without selected properties")
	fset.Parse(args)
	properties := scaffoldProperties{invariants: *invariants, eventually: *eventually, holds: *holds, allows: *allows, noProperties: *noProperties}
	fset.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "invariants", "eventually", "holds", "allows", "no-properties":
			properties.provided = true
		}
	})
	if err := checkOut(*out); err != nil {
		return err // before quint runs: the refusal comes first
	}
	// rendered whole before a file is made: a failed scaffold (a model
	// quint rejects) once left an empty file its retry refused
	var buf bytes.Buffer
	if err := scaffoldTo(&buf, *out, *spec, *module, *system, *http, *bpath, *pkg, *quint, properties); err != nil {
		return err
	}
	return writeOut(*out, buf.Bytes())
}

// scaffoldTo writes the scaffold of a binding (or with system, of a
// qtest.System test) to w, its paths relative to out's directory, where
// the file is read from (the working directory without out).
func scaffoldTo(w io.Writer, out, spec, module string, system, http bool, bpath, pkg, quint string, propertyOptions ...scaffoldProperties) error {
	var properties scaffoldProperties
	if len(propertyOptions) > 0 {
		properties = propertyOptions[0]
	}
	if system && (properties.provided || properties.noProperties || properties.invariants != "" || properties.eventually != "" || properties.holds != "" || properties.allows != "") {
		return fmt.Errorf("scaffold -system uses the binding's property selections: remove -invariants, -eventually, -holds, -allows and -no-properties")
	}
	if http && !system {
		return fmt.Errorf("scaffold -http is a shape of -system's test: gotogether scaffold -system -http -binding b.yaml (for a whole new HTTP service: gotogether init -kind http NAME)")
	}
	if system {
		if bpath == "" {
			return fmt.Errorf("scaffold -system needs -binding (write one first with scaffold -spec model.qnt -module M and explicit property roles)")
		}
		b, err := binding.Load(bpath)
		if err != nil {
			return err
		}
		if err := b.CheckSpec(); err != nil {
			return err
		}
		m, err := binding.LoadModel(quint, b.SpecPath(), b.Module)
		if err != nil {
			return err
		}
		if pkg == "" {
			pkg = packageIn(filepath.Dir(out))
		}
		// a test runs in its package's directory, where qtest.System reads
		// its Binding from
		fmt.Fprint(w, systemTest(pkg, relativeTo(out, bpath), b, m, http))
		return nil
	}
	if spec == "" || module == "" {
		return fmt.Errorf("scaffold needs -spec and -module (or -system -binding b.yaml)")
	}
	m, err := binding.LoadModel(quint, spec, module)
	if err != nil {
		return err
	}
	selected, err := selectScaffoldProperties(m, properties)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "# gotogether binding for %s (generated skeleton: set the constants; override an argument by uncommenting it)\nspec: %s   # relative to this file\nmodule: %s\n\n# each constant a Quint expression, or a template over the run: '{{ set \"key\" }}' (a field's values),\n# '{{ max \"amount\" }}', '{{ count \"write\" }}' (an action's steps), '{{ procs }}', add, atLeast\nconstants:\n", module, relativeTo(out, spec), module)
	for _, c := range slices.Sorted(maps.Keys(m.Consts)) {
		fmt.Fprintf(w, "  %s: ''   # %s\n", c, m.Consts[c])
	}
	fmt.Fprintf(w, "\nintern: {}      # arg: actor|process, for opaque ids the model names by small integers\n")
	fmt.Fprintf(w, "outcomes: {}    # ok/error/<custom> -> Quint expression, used as {{.outcome}}\n")
	fmt.Fprintf(w, "restart: ''     # parameterless action inserted when an actor's process changes\n\nactions:\n")
	for _, a := range m.ActionNames() {
		if a == "init" || a == "step" {
			continue // the state machine itself, not steps
		}
		if m.Helpers[a] {
			fmt.Fprintf(w, "  # %s: a helper other actions call and step does not: no step records it\n", a)
			continue
		}
		fmt.Fprintf(w, "  %s:   # %s\n", a, m.Signature(a))
		if ps := m.Actions[a]; len(ps) > 0 {
			// the defaults, commented out: uncomment to map a field differently
			fmt.Fprintf(w, "    args:   # default: the step field of the same name\n")
			for _, p := range ps {
				if f, ok := quintnames.Original(p.Name); ok {
					// renamed for quint, which keeps the field's own name
					// (item, to): the step records it, so it is mapped
					fmt.Fprintf(w, "      %s: '{{.%s}}'   # %s; the step's field %s, a name quint keeps for a built-in (QNT101)\n", p.Name, f, p.Type, f)
					continue
				}
				fmt.Fprintf(w, "      # %s: '%s'   # %s\n", p.Name, p.DefaultTemplate(), p.Type)
			}
		}
		fmt.Fprintf(w, "    # expect: ''  # Quint bool over the post-state, from observed .obs.<name>\n")
	}
	writeScaffoldProperties(w, m, selected)

	return nil
}

func readSteps(files []string, rules *convert.Rules) ([]qtrace.Step, error) {
	var all []qtrace.Step
	var read []*input.File
	for _, f := range files {
		if rules != nil {
			steps, ok, err := convertFile(f, rules)
			if err != nil {
				return nil, err
			}
			if ok {
				all = append(all, steps...)
				continue
			}
		}
		in, err := input.Read(f, input.Unknown)
		if err != nil {
			return nil, err
		}
		if in.Format == input.ITF {
			return nil, fmt.Errorf("%s is an ITF trace, not recorded steps", f)
		}
		read = append(read, in)
		all = append(all, in.Steps...)
	}
	return all, input.Distinct(read...)
}

func steps(args []string) error {
	fset := commandFlags("steps")
	conv := fset.String("convert", "", "convert OTLP/JSON spans with this rule file")
	out := fset.String("out", "", "write the selected, sanitized steps to this step log instead of listing them")
	actors := fset.String("actors", "", "keep these actors (comma-separated path.Match patterns)")
	actions := fset.String("actions", "", "keep these actions (comma-separated patterns); a log missing other actions' steps is no whole history (validate mismatches where they changed the state): for listing, or -actors and -since to sample")
	since := fset.String("since", "", "keep steps recorded at or after this RFC 3339 time")
	until := fset.String("until", "", "keep steps recorded before this RFC 3339 time")
	pseud := fset.String("pseudonymize", "", "fields whose values become salted pseudonyms (arguments, obs.<name>, actor)")
	bpath := fset.String("binding", "", "keep only the fields this binding reads (and drop error texts)")
	quint := fset.String("quint", "quint", "quint binary (with -binding)")
	keepRefs := fset.Bool("keep-refs", false, "keep steps' refs (quint.ref) while sanitizing (-pseudonymize, -binding), which drops them otherwise: a ref is free text, often a path with user data")
	fset.Parse(args)
	rules, err := loadRules(*conv)
	if err != nil {
		return err
	}
	all, err := readSteps(fset.Args(), rules)
	if err != nil {
		return err
	}
	f := qtrace.Filter{Actors: fields(*actors), Actions: fields(*actions),
		Pseudonymize: fields(*pseud), Salt: os.Getenv("GOTOGETHER_SALT"), KeepRefs: *keepRefs}
	for _, t := range []struct {
		s  string
		to *time.Time
	}{{*since, &f.Since}, {*until, &f.Until}} {
		if t.s != "" {
			if *t.to, err = time.Parse(time.RFC3339, t.s); err != nil {
				return fmt.Errorf("steps: %w", err)
			}
		}
	}
	if len(f.Pseudonymize) > 0 && f.Salt == "" {
		fmt.Fprintln(os.Stderr, "gotogether: GOTOGETHER_SALT is not set: pseudonyms can be reversed by guessing the values")
	}
	if *bpath != "" {
		b, err := binding.Load(*bpath)
		if err != nil {
			return err
		}
		if err := b.CheckSpec(); err != nil {
			return err
		}
		m, err := binding.LoadModel(*quint, b.SpecPath(), b.Module)
		if err != nil {
			return err
		}
		f.Keep = b.UsedFields(m)
	}
	all = f.Apply(all)
	if *out != "" {
		w, err := os.Create(*out)
		if err != nil {
			return err
		}
		if err := qtrace.WriteJSONL(w, all); err != nil {
			w.Close()
			return err
		}
		fmt.Printf("%d steps written to %s\n", len(all), *out)
		return w.Close()
	}
	traces, issues := qtrace.Reconstruct(all)
	for _, i := range issues {
		fmt.Println("issue:", i)
	}
	for _, t := range traces {
		fmt.Printf("actor %s: %d steps, processes %v\n", t.Actor, len(t.Steps), t.Processes)
		for _, i := range t.Issues {
			fmt.Println("  issue:", i)
		}
		for _, l := range t.Lost { // a lost prefix is an issue too; a tail only here
			fmt.Println("  lost:", l)
		}
		for i, s := range t.Steps {
			ref := ""
			if s.Ref != "" {
				ref = "  ref " + s.Ref
			}
			fmt.Printf("  [%2d] seq %-3d %-10.10s %s%s\n", i, s.Seq, s.Process, s.String(), ref)
		}
	}
	return nil
}

// failSummary says how many of results fail and why, in the claims' words
// (validate.Result.Reason, an unfinished result's included): "2 of 5
// actors: 1 rejects replay, 1 mismatch after apply".
func failSummary(results []*validate.Result) string {
	by := map[string]int{}
	bad := 0
	for _, r := range results {
		if why := r.Reason(); why != "" { // an unfinished one's too
			by[why]++
			bad++
		}
	}
	whys := slices.Sorted(maps.Keys(by))
	for i, w := range whys {
		whys[i] = fmt.Sprintf("%d %s", by[w], w)
	}
	if len(results) == 0 {
		return "nothing was checked"
	}
	return fmt.Sprintf("%d of %s: %s", bad, judgmentSummary(results), strings.Join(whys, ", "))
}

// passSummary says what passing results showed: "3 actors conform to the
// model and satisfy its 2 invariants", the invariants only if any were
// checked.
func passSummary(results []*validate.Result) string {
	invs := map[string]bool{}
	for _, r := range results {
		for _, i := range r.Invariants {
			invs[i.Name] = true
		}
	}
	conform, satisfy := "conform", "satisfy"
	if len(results) == 1 {
		conform, satisfy = "conforms", "satisfies"
	}
	s := fmt.Sprintf("%s %s to the model", judgmentSummary(results), conform)
	switch len(invs) {
	case 0:
		return s + " (no invariant checked)"
	case 1:
		return fmt.Sprintf("%s and %s its invariant", s, satisfy)
	}
	return fmt.Sprintf("%s and %s its %d invariants", s, satisfy, len(invs))
}

// judgmentSummary counts source histories separately from the instances judged.
func judgmentSummary(results []*validate.Result) string {
	summary := receipt.Summary{}
	for _, r := range results {
		summary.Actors = append(summary.Actors, r.Receipt())
	}
	counts := summary.Counts()
	if counts.UnknownSourceJudgments > 0 {
		return fmt.Sprintf("%s (%s; source identities unavailable for %s)", plural(counts.Judgments, "judgment"), plural(counts.Partitions, "partition"), plural(counts.UnknownSourceJudgments, "judgment"))
	}
	if counts.Partitions == 0 {
		if counts.Actors == counts.Judgments {
			return plural(counts.Judgments, "actor")
		}
		return fmt.Sprintf("%s for %s", plural(counts.Judgments, "actor judgment"), plural(counts.Actors, "source actor"))
	}
	if counts.Partitions == counts.Judgments {
		return fmt.Sprintf("%s of %s", plural(counts.Partitions, "partition"), plural(counts.Actors, "actor"))
	}
	return fmt.Sprintf("%s (%s, %s; %s)", plural(counts.Judgments, "judgment"), plural(counts.Judgments-counts.Partitions, "actor trace"), plural(counts.Partitions, "partition"), plural(counts.Actors, "source actor"))
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// toITF writes one model-level ITF per actor from recorded steps.
func toITF(args []string) error { return itfTo(os.Stdout, args) }

// itfTo is the itf command, what it says written to w (a test's own, so
// its tests run in parallel).
func itfTo(w io.Writer, args []string) error {
	fset := commandFlags("itf")
	bpath := fset.String("binding", "", "binding file")
	out := fset.String("out", ".", "output directory: <actor>.itf.json per actor")
	conv := fset.String("convert", "", "convert OTLP/JSON spans with this rule file")
	fset.Parse(args)
	rules, err := loadRules(*conv)
	if err != nil {
		return err
	}
	if *bpath == "" || fset.NArg() == 0 {
		return needs("itf", "-binding and the FILEs of recorded steps")
	}
	b, err := binding.Load(*bpath)
	if err != nil {
		return err
	}
	if err := b.CheckSpec(); err != nil {
		return err
	}
	all, err := readSteps(fset.Args(), rules)
	if err != nil {
		return err
	}
	traces, issues, err := b.StepsToITF(all)
	for _, i := range issues {
		fmt.Fprintln(w, "issue:", i)
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	for actor, data := range traces {
		f := filepath.Join(*out, strings.Map(func(r rune) rune {
			if r == '/' || r == ' ' {
				return '_'
			}
			return r
		}, actor)+".itf.json")
		if err := os.WriteFile(f, data, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(w, "actor %s -> %s\n", actor, f)
	}
	return nil
}

func loadRules(p string) (*convert.Rules, error) {
	if p == "" {
		return nil, nil
	}
	return convert.Load(p)
}

// convertFile converts an OTLP/JSON file; ok is false for other formats.
func convertFile(path string, rules *convert.Rules) ([]qtrace.Step, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	if input.Sniff(data) != input.OTLP {
		return nil, false, nil
	}
	tds, err := otelio.ReadTracesData(bytes.NewReader(data))
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	steps, err := rules.Steps(tds)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	un, err := rules.Unmatched(tds)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	if msg := unmatchedSpans(path, un); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	return steps, true, nil
}

// unmatchedSpans says which spans of a file no rule matched ("" if every
// one did): operations the model does not name, or outside its context.
func unmatchedSpans(path string, un []convert.Unmatched) string {
	if len(un) == 0 {
		return ""
	}
	var names []string
	for _, u := range un {
		names = append(names, fmt.Sprintf("%q (%d)", u.Name, u.Count))
	}
	return fmt.Sprintf("convert: %s: spans that no rule matches: %s. Each is an operation the model does not name "+
		"(a rule mapping it to an action) or one outside the model's context (a rule with drop: true says so)", path, strings.Join(names, ", "))
}

func runConvert(args []string) error {
	fset := commandFlags("convert")
	rpath := fset.String("rules", "", "rule file mapping spans to model actions")
	out := fset.String("out", "-", "native step log to write (- for stdout)")
	fset.Parse(args)
	if *rpath == "" || fset.NArg() == 0 {
		return needs("convert", "-rules and the FILEs of OTLP/JSON spans")
	}
	rules, err := convert.Load(*rpath)
	if err != nil {
		return err
	}
	var all []qtrace.Step
	for _, f := range fset.Args() {
		steps, ok, err := convertFile(f, rules)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%s is not OTLP/JSON", f)
		}
		all = append(all, steps...)
	}
	w := os.Stdout
	if *out != "-" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	if err := qtrace.WriteJSONL(w, all); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%d steps\n", len(all))
	return nil
}

func runSkills(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "-help" || args[0] == "--help") || len(args) > 1 && args[0] == "list" {
		// skills and skills list take no flags: their help, not the usage
		fs := commandFlags("skills")
		fs.SetOutput(os.Stdout)
		fs.Usage()
		return nil
	}
	if len(args) == 0 || args[0] == "list" {
		list, err := skills.List()
		if err != nil {
			return err
		}
		for _, s := range list {
			fmt.Printf("%s\n    %s\n", s.Name, s.Description)
		}
		fmt.Println("\ninstall into a project: gotogether skills install   (.agents/skills and .claude/skills; -target, -user, -dir)")
		return nil
	}
	if args[0] != "install" {
		return fmt.Errorf("skills: %q is no subcommand: gotogether skills list, or gotogether skills install (its -h lists its flags)", args[0])
	}
	fset := commandFlags("skills install")
	target := fset.String("target", "", "comma-separated: agents (.agents/skills), claude (.claude/skills), github, cursor, or all (default agents,claude)")
	dir := fset.String("dir", "", "install into this skills directory instead of the targets")
	user := fset.Bool("user", false, "install for the user (under $HOME) instead of the current project")
	force := fset.Bool("force", false, "overwrite existing skill files")
	fset.Parse(args[1:])
	dirs := []string{*dir}
	if *dir == "" {
		root := "."
		if *user {
			h, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			root = h
		}
		var names []string
		if *target != "" {
			names = strings.Split(*target, ",")
		}
		var err error
		if dirs, err = skills.TargetDirs(root, names); err != nil {
			return err
		}
	}
	for _, d := range dirs {
		paths, err := skills.Install(d, *force)
		for _, p := range paths {
			fmt.Println("wrote", p)
		}
		if err != nil {
			return err
		}
		old, err := skills.RemoveRenamed(d)
		for _, p := range old {
			fmt.Println("removed", p, "(renamed)")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// output is the file at path, created (an existing one is not
// overwritten: a scaffold is filled in by hand), or standard output.
// checkOut refuses an -out that exists (a scaffold is filled in by hand
// and never overwritten) or whose directory does not, before quint runs:
// a missing directory once failed after it, as if the file existed.
func checkOut(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("scaffold: %s exists (a scaffold never overwrites: remove it or pick another -out)", path)
	}
	if dir := filepath.Dir(path); !isDir(dir) {
		return fmt.Errorf("scaffold: -out %s: directory %s does not exist (create it, or pick another -out)", path, dir)
	}
	return nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// writeOut writes a rendered scaffold to path, a new file, or to
// standard output without one, and says what it wrote.
func writeOut(path string, data []byte) error {
	if path == "" {
		_, err := os.Stdout.Write(data)
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("scaffold: %w (a scaffold never overwrites: remove it or pick another -out)", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("scaffold: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return fmt.Errorf("scaffold: %w", err)
	}
	fmt.Fprintln(os.Stderr, "wrote", path)
	return nil
}

// relativeTo is path as the file out will read it: relative to out's
// directory (path given relative to the working directory), as is
// without out (standard output, read from where it was written) or for
// an absolute path. Where a symlink makes the path relative to out's
// directory as named differ from the one relative to it as resolved
// (out in link/, link -> far/real: ../m.qnt from link/ is ../../proj/m.qnt
// from far/real), it is absolute, right from either.
func relativeTo(out, path string) string {
	if out == "" || filepath.IsAbs(path) {
		return path
	}
	dir, err := filepath.Abs(filepath.Dir(out))
	if err != nil {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(dir, abs)
	if err != nil {
		return path
	}
	rdir, err1 := filepath.EvalSymlinks(dir)
	rabs, err2 := filepath.EvalSymlinks(abs)
	if err1 != nil || err2 != nil {
		return filepath.ToSlash(rel) // (a path not there yet: as named)
	}
	if real, err := filepath.Rel(rdir, rabs); err != nil || real != rel {
		return abs
	}
	return filepath.ToSlash(rel)
}

// fields parses a comma-separated list, for flags.
func fields(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for f := range strings.SplitSeq(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// scaffoldProperties records explicit author decisions. Runtime property roles
// and model guarantees are separate: neither follows from a Boolean val's name.
type scaffoldProperties struct {
	invariants, eventually, holds, allows string
	noProperties, provided                bool
}

type scaffoldSelection struct {
	invariants, eventually, holds, allows, unselected []string
	simulation                                        bool
}

func selectScaffoldProperties(m *binding.Model, p scaffoldProperties) (scaffoldSelection, error) {
	selected := scaffoldSelection{}
	parse := func(flag, value string) ([]string, error) {
		if value == "" {
			return nil, nil
		}
		var names []string
		seen := map[string]bool{}
		for _, raw := range strings.Split(value, ",") {
			name := strings.TrimSpace(raw)
			if name == "" {
				return nil, fmt.Errorf("scaffold -%s contains an empty property name: name Boolean vals separated by commas", flag)
			}
			if seen[name] {
				return nil, fmt.Errorf("scaffold -%s repeats %s: list each property once", flag, name)
			}
			if !m.Vals[name] {
				return nil, fmt.Errorf("scaffold -%s: %s is not a Boolean val of %s; available: %s", flag, name, m.Module, strings.Join(slices.Sorted(maps.Keys(m.Vals)), ", "))
			}
			seen[name] = true
			names = append(names, name)
		}
		slices.Sort(names)
		return names, nil
	}
	var err error
	for _, item := range []struct {
		name, raw string
		out       *[]string
	}{{"invariants", p.invariants, &selected.invariants}, {"eventually", p.eventually, &selected.eventually}, {"holds", p.holds, &selected.holds}, {"allows", p.allows, &selected.allows}} {
		*item.out, err = parse(item.name, item.raw)
		if err != nil {
			return selected, err
		}
	}
	if p.noProperties && len(selected.invariants)+len(selected.eventually)+len(selected.holds)+len(selected.allows) > 0 {
		return selected, fmt.Errorf("scaffold -no-properties selects conformance only: remove property selections or remove -no-properties")
	}
	if !p.noProperties && len(m.Vals) > 0 && len(selected.invariants)+len(selected.eventually) == 0 {
		return selected, fmt.Errorf("scaffold: select properties of %s with -invariants or -eventually, or explicitly use -no-properties for conformance only; Boolean vals: %s", m.Module, strings.Join(slices.Sorted(maps.Keys(m.Vals)), ", "))
	}
	for _, name := range selected.eventually {
		if slices.Contains(selected.invariants, name) {
			return selected, fmt.Errorf("scaffold: %s is selected by both -invariants and -eventually: choose safety or settled completion", name)
		}
	}
	selected.simulation = len(selected.holds)+len(selected.allows) > 0
	for _, group := range []struct {
		name string
		vals []string
	}{{"holds", selected.holds}, {"allows", selected.allows}} {
		for _, name := range group.vals {
			if !slices.Contains(selected.invariants, name) {
				return selected, fmt.Errorf("scaffold -%s: %s is not a selected invariant; model-only safety classification applies only to -invariants", group.name, name)
			}
			if slices.Contains(selected.holds, name) && slices.Contains(selected.allows, name) {
				return selected, fmt.Errorf("scaffold: %s is selected by both -holds and -allows: choose one model guarantee", name)
			}
		}
	}
	if selected.simulation {
		for _, name := range selected.invariants {
			if !slices.Contains(selected.holds, name) && !slices.Contains(selected.allows, name) {
				return selected, fmt.Errorf("scaffold: selected invariant %s has no model classification: name every selected invariant in -holds or -allows, or omit both flags", name)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(m.Vals)) {
		if !slices.Contains(selected.invariants, name) && !slices.Contains(selected.eventually, name) {
			selected.unselected = append(selected.unselected, name)
		}
	}
	return selected, nil
}

func writeScaffoldProperties(w io.Writer, m *binding.Model, s scaffoldSelection) {
	fmt.Fprintf(w, "\ninvariants: [%s]  # checked after every observed step\neventually: [%s]  # checked only after recorded settle\n", strings.Join(s.invariants, ", "), strings.Join(s.eventually, ", "))
	fmt.Fprintf(w, "# unselected Boolean vals (not runtime checks): [%s]\n", strings.Join(s.unselected, ", "))
	if !s.simulation {
		fmt.Fprintln(w, "\n# Model-only guarantees were not selected. To classify safety properties, regenerate")
		fmt.Fprintln(w, "# with -holds and/or -allows, or deliberately edit the following section.")
	}
	prefix := ""
	if !s.simulation {
		prefix = "# "
	}
	fmt.Fprintf(w, "\n%ssimulate:        # model-only safety classification; never inferred from names\n", prefix)
	fmt.Fprintf(w, "%s  holds: [%s]   # the model guarantees these selected invariants\n", prefix, strings.Join(s.holds, ", "))
	fmt.Fprintf(w, "%s  allows: [%s]  # the model permits their violation; the code must prevent it\n", prefix, strings.Join(s.allows, ", "))
	fmt.Fprintln(w, "# Completion assumptions for model graph cycles require a separate, deliberate edit.")
	fmt.Fprintf(w, "%s  # possible:    # scenarios the model must be able to perform\n", prefix)
	for _, action := range m.ActionNames() {
		if action == "init" || action == "step" || m.Helpers[action] {
			continue
		}
		fmt.Fprintf(w, "%s  #   - '%s'   # some trace takes %s so (set the value)\n", prefix, callForm(action, m.Actions[action]), action)
	}
}
