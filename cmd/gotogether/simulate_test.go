package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
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
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not installed")
	}
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
