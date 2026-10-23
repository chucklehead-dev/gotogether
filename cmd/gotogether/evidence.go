package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chucklehead-dev/gotogether/qtest"
)

// evidence reads the evidence every exploration writes under
// GOTOGETHER_KEEP and reports the suite as a whole: what each test's runs
// showed, and what no test showed. A model action no test took, a fault
// point no test reached, a fault kind no test injected: each test's own
// report says so of itself, but only the suite can say that nothing tried
// it. With -strict such a gap fails (exit 1).
func evidence(args []string) (int, error) {
	fset := flag.NewFlagSet("evidence", flag.ExitOnError)
	strict := fset.Bool("strict", false, "exit 1 if a model action, fault point or fault kind is untried by every test")
	fset.Parse(args)
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
	var es []*qtest.Evidence
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || path != root && (!strings.HasSuffix(d.Name(), ".json") || !qtest.IsEvidence(path)) {
				return nil // not evidence: an ITF trace, a corpus entry, a summary
			}
			e, err := qtest.ReadEvidence(path)
			if err != nil {
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
	if len(es) == 0 {
		return 2, fmt.Errorf("no evidence under %s: run the tests with GOTOGETHER_EVIDENCE=dir", strings.Join(roots, ", "))
	}
	report, gaps := suiteReport(es)
	fmt.Print(report)
	if *strict && gaps > 0 {
		return 1, nil
	}
	return 0, nil
}

// suiteReport is the suite's evidence: a line per test, then what no test
// tried, and how many such gaps there are.
func suiteReport(es []*qtest.Evidence) (string, int) {
	sort.Slice(es, func(i, j int) bool { return es[i].Test < es[j].Test })
	var b strings.Builder
	runs := 0
	for _, e := range es {
		runs += e.Runs
		fmt.Fprintf(&b, "%s: %d runs, %d conform", e.Test, e.Runs, e.Conform)
		for _, r := range sortedKeys(e.FailingBy) {
			fmt.Fprintf(&b, ", %d %s", e.FailingBy[r], r)
		}
		if e.Search != nil && !e.Search.Complete {
			b.WriteString(" (search truncated)")
		}
		if e.Role != qtest.RoleFaithful {
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
		if e.Role == qtest.RoleFaithful {
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
	for _, spec := range sortedKeys(actions) {
		var never []string
		for _, a := range sortedKeys(actions[spec]) {
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
			if p.Faulted() > 0 {
				faulted[p.Kind] = true
			}
		}
	}
	var unreached, unfaulted []string
	for _, k := range sortedKeys(declared) {
		if !reached[k] {
			unreached = append(unreached, k)
		}
	}
	for _, k := range sortedKeys(reached) {
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
