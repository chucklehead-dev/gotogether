// Command gotogether checks Go programs against Quint models from their
// telemetry.
//
//	gotogether scaffold -spec model.qnt -module M          a binding skeleton for M (-out FILE: written there)
//	gotogether scaffold -system -binding b.yaml [-pkg p]   a qtest.System test for the binding (-out FILE)
//	gotogether lint     -binding b.yaml [-src DIR]         check a binding (and annotations, literal Record actions) against the model
//	gotogether lint     -sim DIR                           check simulation code for goroutines, wall-clock time and steps that escape it
//	gotogether lint     -weave DIR                         check code woven by qenv/weave for go statements and method values it cannot rewrite
//	gotogether steps    FILE...                            print the reconstructed per-actor traces
//	gotogether validate [-binding b.yaml] [-dir OUT] FILE...
//	                                                    reconstruct (or read), replay in Quint, report
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
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/convert"
	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/otelio"
	"github.com/chucklehead-dev/gotogether/qlint"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/skills"
	"github.com/chucklehead-dev/gotogether/validate"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	code := 0
	switch os.Args[1] {
	case "scaffold":
		err = scaffold(os.Args[2:])
	case "lint":
		code, err = lint(os.Args[2:])
	case "steps":
		err = steps(os.Args[2:])
	case "validate":
		code, err = runValidate(os.Args[2:])
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
	case "help", "-h", "-help", "--help":
		help()
	case "version":
		fmt.Println("gotogether", version())
	default:
		usage()
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
	fmt.Fprintln(w, `gotogether checks Go code against a Quint model. Start with a binding, then a test:
  gotogether scaffold -spec model.qnt -module M -out m.binding.yaml    (set its constants)
  gotogether lint -binding m.binding.yaml && gotogether simulate -binding m.binding.yaml
  gotogether scaffold -system -binding m.binding.yaml -out system_test.go
Each command's -h says what it is for, with an example. gotogether version prints the version.

usage:
  gotogether scaffold -spec model.qnt -module M [-out m.binding.yaml]
  gotogether scaffold -system -binding b.yaml [-pkg p] [-out system_test.go]
  gotogether simulate -binding b.yaml [-steps n] [-samples n] [-const N=e] [-seed s] [-verify n]
                   the model alone, with the binding's invariants (-verify: every trace of up to n steps, by Apalache)
  gotogether evidence [-strict] [DIR|FILE ...]            a suite's explorations (their evidence under GOTOGETHER_EVIDENCE):
                   each test's verdicts, and the model actions and fault kinds no test tried
  gotogether lint     -binding b.yaml [-src DIR] [-sim DIR]
  gotogether lint     -sim DIR | -weave DIR
  gotogether steps    [-actors a,b] [-actions a,b] [-since T] [-until T] [-pseudonymize f,g] [-binding b.yaml]
                   [-out steps.jsonl] FILE...   list, or select and sanitize (GOTOGETHER_SALT) into a step log
  gotogether validate [-binding b.yaml] [-dir OUT] [-backend rust|typescript] [-format auto|otlp|steps|itf]
                   [-spec M.qnt -module M -instance I] [-novars]
                   [-convert rules.yaml] [-invariants a,b] [-itf] FILE...
  gotogether itf      -binding b.yaml -out DIR FILE...      recorded steps -> model-level ITF per actor
  gotogether cover    -covdir D1,D2 [-sim EVIDENCE]       a real environment's coverage (binaries built with -cover),
                   against a simulation's (an exploration's evidence, or qcover.Summary JSON): what ran only for real, only simulated
  gotogether convert  -rules r.yaml [-out steps.jsonl] FILE...   an app's OTLP/JSON spans -> step log (see package convert)

  gotogether skills   [list | install [-target agents,claude,github,cursor|all] [-user] [-dir DIR] [-force]]
                   agent skills for this toolkit (default: .agents/skills and .claude/skills)

validate, steps and itf also take -convert r.yaml to convert OTLP/JSON spans on the way in.`)
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
	bpath := fset.String("binding", "", "binding file (with -system)")
	pkg := fset.String("pkg", "", "package of the test (with -system; default: that of the Go files in -out's directory, else app)")
	quint := fset.String("quint", "quint", "quint binary")
	out := fset.String("out", "", "write to this file (not overwritten) instead of standard output")
	fset.Parse(args)
	w, done, err := output(*out)
	if err != nil {
		return err
	}
	defer done()
	if *system {
		if *bpath == "" {
			return fmt.Errorf("scaffold -system needs -binding (write one first: gotogether scaffold -spec model.qnt -module M)")
		}
		b, err := binding.Load(*bpath)
		if err != nil {
			return err
		}
		m, err := binding.LoadModel(*quint, b.SpecPath(), b.Module)
		if err != nil {
			return err
		}
		if *pkg == "" {
			*pkg = packageIn(filepath.Dir(*out))
		}
		fmt.Fprint(w, systemTest(*pkg, *bpath, b, m))
		return nil
	}
	if *spec == "" || *module == "" {
		return fmt.Errorf("scaffold needs -spec and -module (or -system -binding b.yaml)")
	}
	m, err := binding.LoadModel(*quint, *spec, *module)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "# gotogether binding for %s (generated skeleton: set the constants; override an argument by uncommenting it)\nspec: %s   # relative to this file\nmodule: %s\n\nconstants:\n", *module, *spec, *module)
	for _, c := range sortedKeys(m.Consts) {
		fmt.Fprintf(w, "  %s: ''   # %s\n", c, m.Consts[c])
	}
	fmt.Fprintf(w, "\nintern: {}      # arg: actor|process, for opaque ids the model names by small integers\n")
	fmt.Fprintf(w, "outcomes: {}    # ok/error/<custom> -> Quint expression, used as {{.outcome}}\n")
	fmt.Fprintf(w, "restart: ''     # parameterless action inserted when an actor's process changes\n\nactions:\n")
	for _, a := range m.ActionNames() {
		if a == "init" || a == "step" {
			continue // the state machine itself, not steps
		}
		fmt.Fprintf(w, "  %s:   # %s\n", a, m.Signature(a))
		if ps := m.Actions[a]; len(ps) > 0 {
			// the defaults, commented out: uncomment to map a field differently
			fmt.Fprintf(w, "    args:   # default: the step field of the same name\n")
			for _, p := range ps {
				fmt.Fprintf(w, "      # %s: '%s'   # %s\n", p.Name, p.DefaultTemplate(), p.Type)
			}
		}
		fmt.Fprintf(w, "    # expect: ''  # Quint bool over the post-state, from observed .obs.<name>\n")
	}
	// every boolean val, checked and classified, so the skeleton checks
	// what the model says: drop a val that is no invariant
	vals := strings.Join(sortedKeys(m.Vals), ", ")
	fmt.Fprintf(w, "\ninvariants: [%s]  # the boolean vals of %s: drop those that are not invariants\n", vals, *module)
	fmt.Fprintf(w, "\nsimulate:        # gotogether simulate: what the model alone does with each invariant\n")
	fmt.Fprintf(w, "  holds: [%s]   # the model guarantees them (no trace violates them)\n", vals)
	fmt.Fprintf(w, "  allows: []      # move here those the model lets fail: requirements on the code, which must prevent it\n")
	return nil
}

func lint(args []string) (int, error) {
	fset := commandFlags("lint")
	bpath := fset.String("binding", "", "binding file")
	src := fset.String("src", "", "Go source tree to scan for //quint:action annotations and literal qobs.Record actions")
	sim := fset.String("sim", "", "Go source tree of simulation code (files importing qenv) to check for what escapes the simulation")
	weave := fset.String("weave", "", "Go source tree woven by qenv/weave to check for what weaving cannot rewrite")
	quint := fset.String("quint", "quint", "quint binary")
	fset.Parse(args)
	simFindings := 0
	if *weave != "" {
		fs, err := qlint.Woven(*weave)
		if err != nil {
			return 2, err
		}
		for _, f := range fs {
			fmt.Printf("%s: %s (%s)\n", f.Pos, f.What, f.Fix)
		}
		fmt.Printf("%d places under %s that weaving cannot rewrite\n", len(fs), *weave)
		simFindings += len(fs)
		if *bpath == "" && *sim == "" {
			if simFindings > 0 {
				return 1, nil
			}
			return 0, nil
		}
	}
	if *sim != "" {
		fs, err := qlint.Sim(*sim)
		if err != nil {
			return 2, err
		}
		for _, f := range fs {
			fmt.Printf("%s: %s (%s)\n", f.Pos, f.What, f.Fix)
		}
		fmt.Printf("%d places under %s escape the simulation\n", len(fs), *sim)
		simFindings += len(fs)
		if *bpath == "" {
			if simFindings > 0 {
				return 1, nil
			}
			return 0, nil
		}
	}
	b, err := binding.Load(*bpath)
	if err != nil {
		return 2, err
	}
	m, err := binding.LoadModel(*quint, b.SpecPath(), b.Module)
	if err != nil {
		return 2, err
	}
	errs := b.Lint(m)
	var warns []string
	if *src != "" {
		anns, err := binding.ScanAnnotations(*src)
		if err != nil {
			return 2, err
		}
		fmt.Printf("%d //quint:action annotations under %s\n", len(anns), *src)
		for _, a := range anns {
			fmt.Printf("  %-28s %-14s %s\n", a.Pos[strings.LastIndex(a.Pos, "/")+1:], a.Action, a.Func)
		}
		recs, err := binding.ScanRecords(*src)
		if err != nil {
			return 2, err
		}
		fmt.Printf("%d literal Record calls under %s\n", len(recs), *src)
		e, w := b.LintSource(m, anns, recs)
		errs, warns = append(errs, e...), w
	}
	for _, w := range warns {
		fmt.Println("warning:", w)
	}
	for _, e := range errs {
		fmt.Println("error:", e)
	}
	if len(errs) > 0 || simFindings > 0 {
		return 1, nil
	}
	fmt.Printf("binding %s fits %s (%d actions bound)\n", *bpath, b.Module, len(b.Actions))
	return 0, nil
}

func readSteps(files []string, rules *convert.Rules) ([]qtrace.Step, error) {
	var all []qtrace.Step
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
		all = append(all, in.Steps...)
	}
	return all, nil
}

func steps(args []string) error {
	fset := commandFlags("steps")
	conv := fset.String("convert", "", "convert OTLP/JSON spans with this rule file")
	out := fset.String("out", "", "write the selected, sanitized steps to this step log instead of listing them")
	actors := fset.String("actors", "", "keep these actors (comma-separated path.Match patterns)")
	actions := fset.String("actions", "", "keep these actions (comma-separated patterns)")
	since := fset.String("since", "", "keep steps recorded at or after this RFC 3339 time")
	until := fset.String("until", "", "keep steps recorded before this RFC 3339 time")
	pseud := fset.String("pseudonymize", "", "fields whose values become salted pseudonyms (arguments, obs.<name>, actor)")
	bpath := fset.String("binding", "", "keep only the fields this binding reads (and drop error texts)")
	quint := fset.String("quint", "quint", "quint binary (with -binding)")
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
		Pseudonymize: fields(*pseud), Salt: os.Getenv("GOTOGETHER_SALT")}
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
		for i, s := range t.Steps {
			fmt.Printf("  [%2d] seq %-3d %-10.10s %s\n", i, s.Seq, s.Process, s.String())
		}
	}
	return nil
}

func runValidate(args []string) (int, error) {
	fset := commandFlags("validate")
	bpath := fset.String("binding", "", "binding file (optional for ITF written by gotogether)")
	dir := fset.String("dir", "", "where to write generated modules and ITF (default: a new temp dir, printed)")
	backend := fset.String("backend", "", "quint test backend (default $QUINT_BACKEND or rust)")
	quint := fset.String("quint", "quint", "quint binary")
	format := fset.String("format", "auto", "input format: auto, otlp, steps (native step log) or itf")
	spec := fset.String("spec", "", "ITF input: the model, if neither the trace nor a binding names it")
	module := fset.String("module", "", "ITF input: the parameterised module")
	instance := fset.String("instance", "", "ITF input: replay against this instance module (e.g. currentDesign), e.g. for quint run --mbt traces")
	novars := fset.Bool("novars", false, "ITF input: do not compare model variables carried by the trace")
	verbose := fset.Bool("v", false, "print quint's output")
	conv := fset.String("convert", "", "convert OTLP/JSON spans with this rule file (see gotogether convert)")
	invs := fset.String("invariants", "", "comma-separated model invariants to check instead of the binding's")
	withITF := fset.Bool("itf", false, "also write the ITF of conforming runs, the model's state at every step (slow for models with a large state); failing runs always get theirs")
	noITF := fset.Bool("noitf", false, "gone: no ITF for conforming runs is the default")
	lin := fset.Bool("linearize", false, "check each actor's steps in an order real time allows, searched for one the model accepts (a client history: steps recorded when calls returned)")
	linKey := fset.String("linearize-key", "", "linearize each actor's history partitioned by this argument's value (independent keys), each partition alone")
	linBudget := fset.Int("linearize-budget", 0, "orders a linearization checks per actor or partition (default 200)")
	resolvePending := fset.Bool("resolve-pending", false, "search what each call left pending (invoked, never completed) may have done")
	history := fset.String("history", "", "write the history drawn as SVG to this file (lanes of calls, the order checked numbered, the failing step red)")
	fset.Parse(args)
	if *noITF {
		return 2, fmt.Errorf("validate: -noitf is the default now: drop it (-itf writes the ITF of conforming runs)")
	}
	rules, err := loadRules(*conv)
	if err != nil {
		return 2, err
	}
	if fset.NArg() == 0 {
		usage()
	}
	var b *binding.Binding
	if *bpath != "" {
		if b, err = binding.Load(*bpath); err != nil {
			return 2, err
		}
	}
	var steps []qtrace.Step
	var itfs []*input.File
	for _, f := range fset.Args() {
		if rules != nil {
			cs, ok, err := convertFile(f, rules)
			if err != nil {
				return 2, err
			}
			if ok {
				fmt.Printf("input %s: OTLP/JSON spans, converted with %s: %d steps\n", f, *conv, len(cs))
				steps = append(steps, cs...)
				continue
			}
		}
		in, err := input.Read(f, input.Format(*format))
		if err != nil {
			return 2, err
		}
		fmt.Printf("input %s: %s\n", f, map[input.Format]string{input.OTLP: "OTLP/JSON spans", input.StepLog: "native step log",
			input.ITF: "model-level ITF", input.Unknown: "empty"}[in.Format])
		if in.Torn != nil {
			fmt.Printf("  warning: %v\n", in.Torn)
		}
		if in.Format == input.ITF {
			itfs = append(itfs, in)
		} else {
			steps = append(steps, in.Steps...)
		}
	}
	if *dir == "" { // one root for every actor's and input's modules, named in the output
		d, err := os.MkdirTemp("", "gotogether-validate-")
		if err != nil {
			return 2, err
		}
		*dir = d
	}
	fmt.Printf("artifacts (generated modules, failing runs' ITF): %s\n", *dir)
	opts := validate.Options{Quint: *quint, Backend: *backend, Dir: *dir, NoITF: !*withITF}
	ok := true
	var results []*validate.Result // every actor's and ITF's, for the result line
	if len(steps) > 0 || len(itfs) == 0 {
		if *bpath == "" {
			return 2, fmt.Errorf("recorded steps need -binding")
		}
		c, err := validate.NewChecker(*bpath, opts)
		if err != nil {
			return 2, err
		}
		if *invs != "" {
			if c, err = c.WithInvariants(strings.Split(*invs, ",")...); err != nil {
				return 2, err
			}
		}
		search, err := c.Search(steps, validate.SearchOptions{Linearize: *lin || *linKey != "", Key: *linKey, Budget: *linBudget, ResolvePending: *resolvePending,
			ConfirmByCheck: true})
		if err != nil {
			return 2, err
		}
		rep, err := c.Check(context.Background(), search.Steps)
		if err != nil {
			return 2, err
		}
		// a failing actor whose order only the Go evaluator judged: quint
		// checks it, and a search again with quint alone replaces it if wrong
		if again, err := search.Confirm(rep); err != nil {
			return 2, err
		} else if again != nil {
			if rep, err = c.Check(context.Background(), again.Steps); err != nil {
				return 2, err
			}
			search = again
		}
		search.Mark(rep)
		if *history != "" {
			if err := os.WriteFile(*history, rep.HistorySVG(steps, search.Steps), 0o644); err != nil {
				return 2, err
			}
		}
		fmt.Print(rep.Summary())
		if *verbose {
			for _, a := range rep.Actors {
				fmt.Println(a.Output)
			}
		}
		ok = ok && rep.OK()
		results = append(results, rep.Actors...)
	}
	for i, in := range itfs {
		o := opts
		if len(itfs) > 1 {
			o.Dir = fmt.Sprintf("%s/itf%d", o.Dir, i)
		}
		r, err := validate.ValidateITF(context.Background(), in.ITF, validate.ITFOptions{Options: o, Binding: b,
			Spec: *spec, Module: *module, Instance: *instance, NoVarCheck: *novars})
		if err != nil {
			return 2, fmt.Errorf("%s: %w", in.Path, err)
		}
		fmt.Print(r.Summary())
		if *verbose {
			fmt.Println(r.Output)
		}
		ok = ok && r.OK()
		results = append(results, r)
	}
	if ok {
		fmt.Printf("RESULT: PASS (%s)\n", passSummary(results))
		return 0, nil
	}
	fmt.Printf("RESULT: FAIL (%s)\n", failSummary(results))
	return 1, nil
}

// failSummary says how many of results fail and why, in the claims' words
// (validate.Result.Reason): "2 of 5 actors: 1 rejects replay, 1 mismatch
// after apply".
func failSummary(results []*validate.Result) string {
	by := map[string]int{}
	bad := 0
	for _, r := range results {
		if why := r.Reason(); why != "" {
			by[why]++
			bad++
		}
	}
	whys := make([]string, 0, len(by))
	for w := range by {
		whys = append(whys, w)
	}
	sort.Strings(whys)
	for i, w := range whys {
		whys[i] = fmt.Sprintf("%d %s", by[w], w)
	}
	if len(results) == 0 {
		return "nothing was checked"
	}
	return fmt.Sprintf("%d of %s: %s", bad, plural(len(results), "actor"), strings.Join(whys, ", "))
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
	s := fmt.Sprintf("%s %s to the model", plural(len(results), "actor"), conform)
	switch len(invs) {
	case 0:
		return s + " (no invariant checked)"
	case 1:
		return fmt.Sprintf("%s and %s its invariant", s, satisfy)
	}
	return fmt.Sprintf("%s and %s its %d invariants", s, satisfy, len(invs))
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// toITF writes one model-level ITF per actor from recorded steps.
func toITF(args []string) error {
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
		usage()
	}
	b, err := binding.Load(*bpath)
	if err != nil {
		return err
	}
	all, err := readSteps(fset.Args(), rules)
	if err != nil {
		return err
	}
	traces, issues, err := b.StepsToITF(all)
	for _, i := range issues {
		fmt.Println("issue:", i)
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
		fmt.Printf("actor %s -> %s\n", actor, f)
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
	return steps, true, nil
}

func runConvert(args []string) error {
	fset := commandFlags("convert")
	rpath := fset.String("rules", "", "rule file mapping spans to model actions")
	out := fset.String("out", "-", "native step log to write (- for stdout)")
	fset.Parse(args)
	if *rpath == "" || fset.NArg() == 0 {
		usage()
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
		usage()
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

func sortedKeys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// output is the file at path, created (an existing one is not
// overwritten: a scaffold is filled in by hand), or standard output.
func output(path string) (w io.Writer, done func(), err error) {
	if path == "" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("scaffold: %v (a scaffold never overwrites: remove it or pick another -out)", err)
	}
	return f, func() {
		if err := f.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "gotogether:", err)
		}
		fmt.Fprintln(os.Stderr, "wrote", path)
	}, nil
}

// systemTest is a qtest.System test for the binding: a Run that deploys
// nothing yet, one commented qobs.Record per bound or modelled action with
// the fields the binding reads, a conformance test that fails until steps
// are recorded, and a mutant claim to fill in.
func systemTest(pkg, bpath string, b *binding.Binding, m *binding.Model) string {
	var rec strings.Builder
	for _, a := range m.ActionNames() {
		if a == "init" || a == "step" || a == b.Restart {
			continue
		}
		// the parameters' fields in the signature's order, then the rest
		// (observations) as Fields sorts them
		var fields []string
		rest := map[string]bool{}
		for _, f := range b.Actions[a].Fields(m.Actions[a]...) {
			rest[f] = true
		}
		for _, p := range m.Actions[a] {
			if rest[p.Name] {
				fields = append(fields, p.Name)
				delete(rest, p.Name)
			}
		}
		fields = append(fields, sortedKeys(rest)...)
		kv := ""
		for _, f := range fields {
			v := f
			if o, ok := strings.CutPrefix(f, "obs."); ok {
				v = "observed" + strings.ToUpper(o[:1]) + o[1:]
			}
			kv += fmt.Sprintf(", %q, %s", f, v)
		}
		fmt.Fprintf(&rec, "\t\t\t// qobs.Record(ctx, %q, err%s) // %s\n", a, kv, m.Signature(a))
	}
	claim := "qtest.Some(qtest.Rejects(\"ACTION\"))"
	if len(b.Invariants) > 0 {
		claim = fmt.Sprintf("qtest.Some(qtest.Violates(%q))", b.Invariants[0])
	}
	// what the faithful runs must also show: every action taken and fault
	// faulted, and (when an invariant has an antecedent) none held vacuously
	faithful := "qtest.Conforms(), qtest.Exercised()"
	for _, inv := range b.Invariants {
		if b.Antecedents[inv] != "" || m.Antecedents[inv] != "" {
			faithful += ", qtest.NotVacuous()"
			break
		}
	}
	// a binding with eventual vals has them judged only after a settle
	end := "\t\treturn s.Run(time.Minute) // a task waiting forever is an error (qenv.ErrStuck)\n"
	if len(b.Eventually) > 0 {
		end = "\t\tif err := s.Run(time.Minute); err != nil {\n\t\t\treturn err // a task waiting forever is an error (qenv.ErrStuck)\n\t\t}\n" +
			"\t\t// faults off until quiet, then a settle in the trace: the binding's\n\t\t// eventual vals (" + strings.Join(b.Eventually, ", ") +
			") are judged then; an *Unsettled is the run's outcome\n\t\treturn s.Settle(time.Minute)\n"
		faithful += ", qtest.All(qtest.Settles())"
	}
	return fmt.Sprintf(`package %[1]s

import (
	"context"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

// Mutation is a bug the application can be built with, to show that the
// checks catch it: Faithful is none.
type Mutation int

const (
	Faithful Mutation = iota
	Mutant            // name it for the bug it injects
)

// system deploys the application, with mutation m, in a simulation made
// from c. Every workflow runs it: Explore (seeds), qhegel.Shrink (Hegel),
// Sweep, Fuzz, Mutate.
func system(m Mutation) qtest.System {
	s := qtest.System{Binding: %[2]q, Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		// Deploy the application here: its services (qenv.NewService),
		// stores (qenv.NewStore), queues (qenv.NewQueue) and crashable
		// processes (s.Process), its code written against those seams and
		// qenv.Go, qenv.Now, qenv.Sleep. Record each model step where it
		// takes effect, with the fields the binding reads:
		s.Spawn("client", func(ctx context.Context) {
%[3]s		})
%[6]s	}}
	if m != Faithful {
		s.Mutant = "Mutant" // its runs are not the application's: a suite's evidence leaves them out
	}
	return s
}

// The faithful application conforms through every seed's faults, and the
// runs exercise what the model and the environment offer: a check of
// actions never taken or faults never injected says nothing of them.
func TestConforms(t *testing.T) {
	qtest.RequireQuint(t)
	// until Run records steps, the exploration fails saying so
	system(Faithful).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, %[5]s)
}

// A mutant must be caught, and how: say which check fails (Rejects,
// Mismatch, Violates; over runs Some, All, None). A check no mutant can
// fail proves nothing.
func TestMutantIsCaught(t *testing.T) {
	qtest.RequireQuint(t)
	t.Skip("scaffold: inject a bug as Mutant, state how it shows, and remove this line")
	system(Mutant).Explore(t, qtest.ExploreConfig{Seeds: 30}).Expect(t, %[4]s)
}
`, pkg, bpath, rec.String(), claim, faithful, end)
}

// fields parses a comma-separated list, for flags.
func fields(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
