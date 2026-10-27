package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	ev "github.com/chucklehead-dev/gotogether/internal/evidence"
	"github.com/chucklehead-dev/gotogether/validate"
)

// suiteEvidence is what the evidence command reads of an exploration's
// evidence (internal/evidence's view of qtest.Evidence's JSON): the CLI
// does not import qtest, which brings testing and the simulation with it.
type suiteEvidence = ev.View

// evidence reads the evidence every exploration writes under
// GOTOGETHER_EVIDENCE (or GOTOGETHER_KEEP), and the manifests of the runs
// gotogether did not drive (role external), and reports the suite as a
// whole: what each test's runs showed, and what no test showed. A model action no test took, a fault
// point no test reached, a fault kind no test injected: each test's own
// report says so of itself, but only the suite can say that nothing tried
// it. With -strict such a gap fails (exit 1). With -expect a check the
// file names that wrote no evidence fails (exit 1): a test that skipped,
// was left out, or no longer explores says nothing, and its absence would
// pass unseen.
func evidence(args []string) (int, error) {
	fset := commandFlags("evidence")
	strict := fset.Bool("strict", false, "exit 1 if a model action, fault point or fault kind is untried by every test")
	expectFile := fset.String("expect", "", "a file of explorations that must have written evidence, one per line (a test's, TestX or TestX/sub; one exploration, TestX#2; either after its package: its import path or the end of it, examples/hello TestX), # comments (at a line's start or after a space): exit 1 naming each that wrote none. Only explorations write evidence (Explore, Exhaust, Sweep, Mutate, Runs' claims do not)")
	fset.Parse(args)
	var expected []string
	if *expectFile != "" {
		var err error
		if expected, err = readChecks(*expectFile); err != nil {
			return 2, err
		}
	}
	roots := fset.Args()
	if len(roots) == 0 {
		dir := os.Getenv("GOTOGETHER_EVIDENCE")
		if dir == "" {
			dir = os.Getenv("GOTOGETHER_KEEP")
		}
		if dir == "" {
			return 2, fmt.Errorf("evidence needs a directory (or GOTOGETHER_EVIDENCE): where tests run with GOTOGETHER_EVIDENCE=dir wrote their explorations' evidence")
		}
		roots = []string{dir}
	}
	var es []*suiteEvidence
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || path != root && (!strings.HasSuffix(d.Name(), ".json") || !ev.Is(path) && !validate.IsManifest(path)) {
				return nil // not evidence: an ITF trace, a corpus entry, a summary
			}
			var e *suiteEvidence
			if validate.IsManifest(path) { // a run gotogether did not drive (Validate's, Record's, validate -manifest's)
				m, err := validate.ReadManifest(path)
				if errors.Is(err, validate.ErrManifestFormat) { // another version's: one file, not the whole suite
					fmt.Printf("warning: %v; skipped\n", err)
					return nil
				}
				if err != nil {
					return err
				}
				e = ev.OfManifest(m)
			} else if e, err = ev.Read(path); err != nil {
				return err
			}
			if e.Test == "" {
				e.Test, _ = filepath.Rel(root, filepath.Dir(path))
			}
			es = append(es, e)
			return nil
		})
		if err != nil {
			return 2, err
		}
	}
	if len(es) == 0 && len(expected) == 0 {
		return 2, fmt.Errorf("no evidence under %s: run the tests with GOTOGETHER_EVIDENCE=dir", strings.Join(roots, ", "))
	}
	report, gaps := suiteReport(es)
	fmt.Print(report)
	missing := missingChecks(es, expected)
	if len(missing) > 0 {
		fmt.Printf("  expect  %d of %d checks in %s wrote no evidence: %s (each must run an exploration with GOTOGETHER_EVIDENCE set, the only checks that write it "+
			"(Validate, Record and Hegel's searches write none: list their tests' explorations only); a test that skipped, was left out by -run or a build tag, or no longer explores writes none; GOTOGETHER_STRICT=1 fails the skips)\n",
			len(missing), len(expected), *expectFile, strings.Join(missing, ", "))
	} else if len(expected) > 0 {
		fmt.Printf("  expect  every one of the %d checks in %s wrote evidence\n", len(expected), *expectFile)
	}
	if *strict && gaps > 0 || len(missing) > 0 {
		return 1, nil
	}
	return 0, nil
}

// readChecks reads an -expect file: a check per line, blank lines and
// comments left out. A comment is a # at the line's start or after a
// space: the # of an exploration's id (TestX#2) is no comment.
func readChecks(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		line, _, _ = strings.Cut(strings.ReplaceAll(line, "\t", " "), " #")
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("evidence -expect %s: no check in it: list a test (TestX) or an exploration (TestX#2) per line", path)
	}
	return out, nil
}

// missingChecks are the expected checks no evidence of es is from, in
// the file's order.
func missingChecks(es []*suiteEvidence, expected []string) []string {
	var out []string
	for _, c := range expected {
		found := false
		for _, e := range es {
			if fromCheck(e, c) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, c)
		}
	}
	return out
}

// fromCheck reports whether evidence e was written by check: its
// exploration (TestX#2), its test (TestX), or a test it is a subtest of
// (TestX for TestX/a#1), each optionally after its package: its import
// path, or whole segments at the end of it ("examples/hello TestX" for
// github.com/x/repo/examples/hello), or Package as the report names it
// ("_" a module's root).
func fromCheck(e *suiteEvidence, check string) bool {
	name := check
	if pkg, rest, ok := strings.Cut(check, " "); ok {
		if pkg != e.Package && !(pkg == "_" && e.Package == "") && e.Import != pkg && !strings.HasSuffix(e.Import, "/"+pkg) {
			return false
		}
		name = rest
	}
	if e.Test == name {
		return true
	}
	test := e.Test
	if i := strings.LastIndex(test, "#"); i >= 0 && i < len(test)-1 && strings.Trim(test[i+1:], "0123456789") == "" {
		test = test[:i] // the exploration's number
	}
	return test == name || strings.HasPrefix(test, name+"/")
}

// suiteReport is the suite's evidence: a line per test, then what no test
// tried, and how many such gaps there are.
func suiteReport(es []*suiteEvidence) (string, int) {
	sort.Slice(es, func(i, j int) bool { return es[i].Test < es[j].Test })
	var b strings.Builder
	runs := 0
	for _, e := range es {
		runs += e.Runs
		name := e.Test
		if e.Package != "" && e.Package != "_" {
			name = e.Package + " " + e.Test // two packages' tests may share a name
		}
		fmt.Fprintf(&b, "%s: %d runs, %d conform", name, e.Runs, e.Conform)
		for _, r := range slices.Sorted(maps.Keys(e.FailingBy)) {
			fmt.Fprintf(&b, ", %d %s", e.FailingBy[r], r)
		}
		if e.Exhaust != nil && !e.Exhaust.Complete {
			b.WriteString(" (search truncated)")
		}
		if e.Role != ev.RoleFaithful {
			fmt.Fprintf(&b, " (%s: not counted below)", e.Role)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "suite: %d explorations, %d runs\n", len(es), runs)
	gaps := 0
	// what the suite tried is what its faithful explorations did: a
	// mutant takes paths the system does not, a replay repeats a run
	// counted where it was found
	all := es
	es = nil
	for _, e := range all {
		if e.Role == ev.RoleFaithful {
			es = append(es, e)
		}
	}
	// model actions, by model: taken by some test, else never
	taken, actions := map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, e := range es {
		if e.Model == nil {
			continue
		}
		spec := e.Spec
		if taken[spec] == nil {
			taken[spec], actions[spec] = map[string]bool{}, map[string]bool{}
		}
		for a, n := range e.Model.Taken {
			actions[spec][a] = true
			if n > 0 {
				taken[spec][a] = true
			}
		}
		for _, a := range e.Model.Never {
			actions[spec][a] = true
		}
	}
	for _, spec := range slices.Sorted(maps.Keys(actions)) {
		var never []string
		for _, a := range slices.Sorted(maps.Keys(actions[spec])) {
			if !taken[spec][a] {
				never = append(never, a)
			}
		}
		name := spec
		if name == "" {
			name = "the model"
		}
		if len(never) == 0 {
			fmt.Fprintf(&b, "  model   %s: every action taken by some test\n", name)
			continue
		}
		gaps += len(never)
		fmt.Fprintf(&b, "  model   %s: no test took %s\n", name, strings.Join(never, ", "))
	}
	// fault points: reached by some test, and faulted by some test
	reached, faulted, declared := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range es {
		if e.Faults == nil {
			continue
		}
		for _, p := range e.Faults.Points {
			if len(p.Sites) > 0 {
				declared[p.Kind] = true
			}
			if p.Runs > 0 {
				reached[p.Kind] = true
			}
			if ev.Faulted(p.Injected) > 0 {
				faulted[p.Kind] = true
			}
		}
	}
	var unreached, unfaulted []string
	for _, k := range slices.Sorted(maps.Keys(declared)) {
		if !reached[k] {
			unreached = append(unreached, k)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(reached)) {
		if !faulted[k] {
			unfaulted = append(unfaulted, k)
		}
	}
	if len(unreached) > 0 {
		gaps += len(unreached)
		fmt.Fprintf(&b, "  faults  declared and reached by no test: %s\n", strings.Join(unreached, ", "))
	}
	if len(unfaulted) > 0 {
		gaps += len(unfaulted)
		fmt.Fprintf(&b, "  faults  reached and never faulted by any test: %s\n", strings.Join(unfaulted, ", "))
	}
	if len(reached) > 0 && len(unreached)+len(unfaulted) == 0 {
		fmt.Fprintf(&b, "  faults  every kind reached and faulted by some test (%d kinds)\n", len(reached))
	}
	return b.String(), gaps
}
