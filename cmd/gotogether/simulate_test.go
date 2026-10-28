package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// splitTop splits -const's list at the commas outside brackets.
func TestSplitTop(t *testing.T) {
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

// quint samples the possibles the evaluator did not find in the
// invariant's own run: with the evaluator's search cut at once (-states
// 1), vending's predicate (a witness) and its sequence of names (in the
// run's traces) are reached by the one quint run of its one invariant,
// no run of their own; a failed possible names its binding for nav.
func TestSimulatePossibleSampledWithTheInvariant(t *testing.T) {
	skip.Quint(t)
	real, err := exec.LookPath("quint")
	if err != nil {
		t.Skip("no quint on PATH")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	shim := filepath.Join(dir, "quint")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\necho \"$@\" >> "+log+"\nexec "+real+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := must(filepath.Abs("../../validate/testdata/vending.qnt"))
	write := func(possible string) string {
		p := filepath.Join(dir, "b.yaml")
		y := "spec: " + spec + `
module: vending
constants: { PRICE: '2', STOCK: '2' }
actions:
  buy: { args: { result: '{{.outcome}}' } }
invariants: [nonNegative]
simulate:
  holds: [nonNegative]
  steps: 12
  samples: 300
  possible:
` + possible
		if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Remove(log)
		return p
	}
	runs := func() []string {
		b, _ := os.ReadFile(log)
		var out []string
		for l := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
			if strings.HasPrefix(l, "run ") {
				out = append(out, l)
			}
		}
		return out
	}
	p := write("    - soldOut\n    - name: bought then refunded\n      steps: [buy, insert, refund]\n")
	if code, err := simulate([]string{"-binding", p, "-states", "1", "-quint", shim}); code != 0 || err != nil {
		t.Fatalf("exit %d, %v; want both reached by quint", code, err)
	}
	if rs := runs(); len(rs) != 1 || !strings.Contains(rs[0], "--invariant nonNegative") || !strings.Contains(rs[0], "--witnesses") || !strings.Contains(rs[0], "--mbt") {
		t.Errorf("quint runs: %q; want the invariant's alone, sampling the possibles", rs)
	}
	// one the model forbids: still sampled to the end, and failed
	p = write("    - repair\n")
	out := captureStdout(t, func() {
		if code, err := simulate([]string{"-binding", p, "-states", "1", "-quint", shim}); code != 1 || err != nil {
			t.Errorf("repair: exit %d, %v; want 1", code, err)
		}
	})
	if !strings.Contains(out, "gotogether nav -binding "+p) {
		t.Errorf("a failed possible names no nav of its binding:\n%s", out)
	}
}

// captureStdout is what f prints to os.Stdout (simulate reports there).
// Not for parallel tests: os.Stdout is the process's.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() { os.Stdout = old }()
	f()
	w.Close()
	os.Stdout = old
	return <-done
}
