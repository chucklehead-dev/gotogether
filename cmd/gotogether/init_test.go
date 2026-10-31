package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/schemas"
)

// gotogether init -kind http writes a project whose `go test -short
// ./...` passes as written, on its own go.mod: the faithful service
// conforms in the simulation and over a real socket, its runs exercise
// every fault, and its mutant is caught as TestMutant says.
func TestInitIsGreen(t *testing.T) {
	if testing.Short() {
		t.Skip("full mode: builds and tests a new module")
	}
	skip.Quint(t)
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "shop")
	var out bytes.Buffer
	if err := runInit(&out, &out, []string{"-kind", "http", "-dir", dir, "-replace", repo, "shop"}); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	for _, f := range []string{"go.mod", "shop.go", "client.go", "model/shop.qnt", "shop.binding.yaml", "sim_test.go", "real_test.go",
		"testdata/mutants/offByOne/shop.go", "run-demo.sh", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("no %s: %v", f, err)
		}
	}
	// the mutant is the service's file with its one line changed
	svc, _ := os.ReadFile(filepath.Join(dir, "shop.go"))
	mut, _ := os.ReadFile(filepath.Join(dir, "testdata/mutants/offByOne/shop.go"))
	if d := lineDiff(string(svc), string(mut)); d != 1 {
		t.Errorf("the mutant differs from shop.go in %d lines, want 1", d)
	}
	cmd := exec.Command("go", "test", "-short", "-count=1", "-v", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test -short ./... in the new module: %v\n%s", err, b)
	}
	for _, want := range []string{"--- PASS: TestConforms", "--- PASS: TestExercised", "--- PASS: TestRealConforms", "--- PASS: TestMutants/offByOne", "30 reject reserve"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("no %q in the new module's tests:\n%s", want, b)
		}
	}
	if fmtOut, _ := exec.Command("gofmt", "-l", dir).CombinedOutput(); len(bytes.TrimSpace(fmtOut)) > 0 {
		t.Errorf("gofmt -l: %s", fmtOut)
	}
	vet := exec.Command("go", "vet", "./...")
	vet.Dir, vet.Env = dir, cmd.Env
	if b, err := vet.CombinedOutput(); err != nil {
		t.Errorf("go vet: %v\n%s", err, b)
	}
}

// lineDiff counts the lines of a and b that differ, position by position.
func lineDiff(a, b string) int {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	n := max(len(al), len(bl)) - min(len(al), len(bl))
	for i := range min(len(al), len(bl)) {
		if al[i] != bl[i] {
			n++
		}
	}
	return n
}

// The project's binding fits its model, and the model can do what the
// binding says it must (simulate.possible): quint runs, but no module is
// built.
func TestInitBindingFits(t *testing.T) {
	skip.Quint(t)
	dir := filepath.Join(t.TempDir(), "orders")
	if err := runInit(io.Discard, io.Discard, []string{"-dir", dir, "-no-tidy", "orders"}); err != nil {
		t.Fatal(err)
	}
	b, err := binding.Load(filepath.Join(dir, "orders.binding.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := binding.LoadModel("quint", b.SpecPath(), b.Module)
	if err != nil {
		t.Fatal(err)
	}
	if errs := b.Lint(m); len(errs) > 0 {
		t.Errorf("the binding does not fit its model: %v", errs)
	}
	if b.Format != binding.FormatName {
		t.Errorf("format %q, want %s", b.Format, binding.FormatName)
	}
	// an editor validates it against its schema, named on its first line
	data, _ := os.ReadFile(filepath.Join(dir, "orders.binding.yaml"))
	if first, _, _ := strings.Cut(string(data), "\n"); first != "# yaml-language-server: $schema="+schemas.BindingURL {
		t.Errorf("first line %q names no schema", first)
	}
	if ps, err := schemas.Check(schemas.Binding(), data); err != nil || len(ps) > 0 {
		t.Errorf("the binding breaks its schema: %v %v", ps, err)
	}
}

// init refuses a name no package, module or host could have, a kind it
// does not write (naming the example to start from), a directory in use
// and a -replace with no checkout, before writing anything.
func TestInitRefuses(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"Stock"}, "lower-case letters and digits"},
		{[]string{"my-shop"}, "lower-case letters and digits"},
		{[]string{"func"}, "Go's own"},
		{[]string{"main"}, "Go's own"},
		{[]string{"item"}, "quint keeps"},
		{[]string{"-kind", "grpc", "ledger"}, "examples/grpcledger"},
		{[]string{"-kind", "queue", "jobs"}, "examples/streams"},
		{[]string{"-kind", "soap", "x"}, "the kinds are http, library, grpc and queue"},
		{[]string{"-replace", t.TempDir(), "-dir", filepath.Join(t.TempDir(), "s"), "shop"}, "no go.mod there"},
		{nil, "init needs a project's name"},
	} {
		err := runInit(io.Discard, io.Discard, c.args)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("init %v: %v, want %q", c.args, err, c.want)
		}
	}
	used := t.TempDir()
	os.WriteFile(filepath.Join(used, "x"), nil, 0o644)
	if err := runInit(io.Discard, io.Discard, []string{"-dir", used, "-no-tidy", "shop"}); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("a directory in use: %v", err)
	}
	if entries, _ := os.ReadDir(used); len(entries) != 1 {
		t.Errorf("init wrote into a directory in use: %d entries", len(entries))
	}
}

// scaffold -system -http deploys an HTTP service on qenv/qhttp: the
// server's process created before the client's task (one created first
// had every request refused until the server listened), a client whose
// requests retry a refused connection, the records in the handler; it
// builds as written and fails until the handler records.
func TestScaffoldSystemHTTP(t *testing.T) {
	skip.Quint(t)
	b, err := binding.Load("../../examples/bank/bank.binding.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := binding.LoadModel("quint", b.SpecPath(), b.Module)
	if err != nil {
		t.Fatal(err)
	}
	src := systemTest("scaffolded", "../bank/bank.binding.yaml", b, m, true)
	proc, spawn := strings.Index(src, `s.Process("bank", true`), strings.Index(src, `s.Spawn("client"`)
	if proc < 0 || spawn < 0 || proc > spawn {
		t.Errorf("want the server's process (at %d) before the client's task (at %d):\n%s", proc, spawn, src)
	}
	for _, want := range []string{
		`srv.Handle(ctx, handler())`,
		`errors.Is(err, syscall.ECONNREFUSED)`,
		`// qobs.Record(r.Context(), "deposit", err, "account", account, "amount", amount, "obs.balance", observedBalance)`,
		`tr.Refuse = true`,
		`Expect(t, qtest.Exercised())`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %s in:\n%s", want, src)
		}
	}
	dir, err := os.MkdirTemp("../../examples", "_scaffolded") // _: no ./... pattern sees it
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "system_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "-count=1", "-run", "TestConforms$", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "recorded a step") {
		t.Fatalf("want TestConforms to fail saying no run recorded a step, got (%v):\n%s", err, out)
	}
	if strings.Contains(string(out), "refused") {
		t.Errorf("a run's requests were refused: the server must listen before the client sends:\n%s", out)
	}
}

// scaffold -http without -system says what it is a shape of.
func TestScaffoldHTTPNeedsSystem(t *testing.T) {
	err := scaffoldTo(io.Discard, "", "m.qnt", "m", false, true, "", "", "quint")
	if err == nil || !strings.Contains(err.Error(), "scaffold -system -http") {
		t.Errorf("scaffold -http alone: %v", err)
	}
}
