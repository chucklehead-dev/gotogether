package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The simulation module imports the binding's instance, with its
// constants overridden, relative to where it is written.
func TestSimulationModule(t *testing.T) {
	b := &binding.Binding{Spec: "model/m.qnt", Module: "m", Dir: "/repo/ex",
		Constants: map[string]string{"N": "{{ set \"node\" }}", "K": "2"}, Defs: "def two = 2"}
	if _, err := simulationModule(b, nil, "/tmp/s/simulate.qnt"); err == nil || !strings.Contains(err.Error(), "-const N=expr") {
		t.Errorf("a template constant: %v, want the fix", err)
	}
	src, err := simulationModule(b, splitTop(`N=Set("a", "b")`), "/repo/ex/out/simulate.qnt")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`import m(K = 2, N = Set("a", "b")).* from "../model/m"`, "  def two = 2"} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q in:\n%s", want, src)
		}
	}
	if got := splitTop(`A=Set(1, 2),B=(1, 2),C=3`); len(got) != 3 || got[0] != "A=Set(1, 2)" {
		t.Errorf("splitTop: %q", got)
	}
}

// simulate checks each invariant against the binding's classification:
// hello's exactlyOnce holds in its model; claimed as allowed, it fails,
// saying the check on the code would prove nothing.
func TestSimulateClassifies(t *testing.T) {
	skip.Quint(t)
	if code, err := simulate([]string{"-binding", "../../examples/hello/hello.binding.yaml", "-samples", "200"}); code != 0 || err != nil {
		t.Fatalf("hello: exit %d, %v", code, err)
	}
	b, err := os.ReadFile("../../examples/hello/hello.binding.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	wrong := strings.Replace(string(b), "holds: [exactlyOnce]", "allows: [exactlyOnce]", 1)
	wrong = strings.Replace(wrong, "spec: model/hello.qnt", "spec: "+filepath.Join(must(filepath.Abs("../../examples/hello")), "model/hello.qnt"), 1)
	p := filepath.Join(dir, "b.yaml")
	if err := os.WriteFile(p, []byte(wrong), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, err := simulate([]string{"-binding", p, "-samples", "200"}); code != 1 || err != nil {
		t.Fatalf("exactlyOnce claimed allowed: exit %d, %v; want 1", code, err)
	}
}

func must(s string, err error) string {
	if err != nil {
		panic(err)
	}
	return s
}

// A binding's verifySkip says why -verify leaves its model out, with no
// quint run at all (the quint binary given does not exist).
func TestSimulateVerifySkip(t *testing.T) {
	code, err := simulate([]string{"-binding", "../../examples/twophase/twophase.binding.yaml", "-verify", "8", "-quint", "/nonexistent/quint"})
	if code != 0 || err != nil {
		t.Errorf("twophase -verify: exit %d, %v; want a skip", code, err)
	}
}

// With -verify, Apalache checks every trace up to the bound: hello's
// exactlyOnce holds in all of them, and lease's noStaleWrite has a
// counterexample, as each binding says. It downloads Apalache on first
// use, so it runs only with GOTOGETHER_VERIFY=1 (the nightly run).
func TestSimulateVerify(t *testing.T) {
	if os.Getenv("GOTOGETHER_VERIFY") != "1" {
		t.Skip("GOTOGETHER_VERIFY=1 runs quint verify (Apalache, Java)")
	}
	for _, b := range []string{"../../examples/hello/hello.binding.yaml", "../../examples/lease/lease.binding.yaml"} {
		if code, err := simulate([]string{"-binding", b, "-verify", "5"}); code != 0 || err != nil {
			t.Errorf("%s: exit %d, %v", b, code, err)
		}
	}
}

// A binding with only eventual vals has nothing to sample (they are
// judged after a run settles): simulate says so and succeeds, with no
// quint run, where CI's loop over every binding once failed on it.
func TestSimulateOnlyEventualVals(t *testing.T) {
	code, err := simulate([]string{"-binding", "../../examples/quorumkv/quorumkv.binding.yaml", "-quint", "/nonexistent/quint"})
	if code != 0 || err != nil {
		t.Errorf("quorumkv: exit %d, %v; want nothing to simulate", code, err)
	}
}

// A binding's possibles: vending's are reached (exit 0); its strict
// variant's are not (exit 1), the model forbidding each. Each possible
// quint can check (a predicate, a sequence of names) gets the same
// answer from quint's sampling and the Go evaluator's search, and a
// sequence naming arguments is the evaluator's alone.
func TestSimulatePossible(t *testing.T) {
	skip.Quint(t)
	for b, want := range map[string]int{"vending.yaml": 0, "vending_strict.yaml": 1} {
		path := filepath.Join("../../validate/testdata", b)
		if code, err := simulate([]string{"-binding", path}); code != want || err != nil {
			t.Errorf("%s: exit %d, %v; want %d", b, code, err, want)
		}
		c, err := validate.NewChecker(path, validate.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		cs, err := resolvePossibles(c, c.Binding.Simulate.Possible)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		file := filepath.Join(dir, "simulate.qnt")
		src, err := simulationModule(c.Binding, nil, file)
		if err != nil {
			t.Fatal(err)
		}
		src = strings.TrimSuffix(src, "}\n") + possibleVals(cs) + "}\n"
		if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := quintPossibles("quint", dir, file, cs, 12, 300, "1", 0); err != nil {
			t.Fatal(err)
		}
		rs, err := c.Possible(c.Binding.Simulate.Possible, validate.PossibleSearch{Depth: 12})
		if err != nil {
			t.Fatal(err)
		}
		for i, pc := range cs {
			r := rs[i]
			switch {
			case pc.quintSkip != "":
				if pc.when != "" || len(pc.steps[0].Params) == 0 && len(pc.steps) == 1 {
					t.Errorf("%s: %s: quint skipped it (%s)", b, pc.p.Text(), pc.quintSkip)
				}
			case pc.quintFound != r.Found:
				t.Errorf("%s: %s: quint found %v, the evaluator %v (%s)", b, pc.p.Text(), pc.quintFound, r.Found, r.Verdict())
			}
			if want == 1 && (r.Found || !r.Complete) {
				t.Errorf("%s: %s: %s, want impossible", b, pc.p.Text(), r.Verdict())
			}
			if want == 0 && !r.Found {
				t.Errorf("%s: %s: %s, want reached", b, pc.p.Text(), r.Verdict())
			}
		}
	}
}
