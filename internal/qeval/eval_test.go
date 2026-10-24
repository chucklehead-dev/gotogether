package qeval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// compile compiles testdata's module name (with its init and step) to IR.
func compile(t *testing.T, file, main, init, step string) *Model {
	t.Helper()
	if _, err := exec.LookPath("quint"); err != nil {
		t.Skip("quint not on PATH")
	}
	out := filepath.Join(t.TempDir(), "ir.json")
	abs, _ := filepath.Abs(filepath.Join("testdata", file))
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	// to stdout, a file: --out writes the modules unflattened, and a pipe
	// loses what quint has not written when it exits
	cmd := exec.Command("quint", "compile", "--target", "json", "--main", main, "--init", init, "--step", step, abs)
	cmd.Dir, cmd.Stdout = filepath.Dir(abs), f
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	f.Close()
	if err != nil {
		t.Fatalf("quint compile: %v\n%s", err, stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func strs(ss ...string) *Set {
	vs := make([]Value, len(ss))
	for i, s := range ss {
		vs[i] = Str(s)
	}
	return NewSet(vs)
}

// The wallet's model, through a module that imports it with its
// constants passed through: its actions stepped, its vals and the
// binding's expect evaluated, as a replay would.
func TestWallet(t *testing.T) {
	m := compile(t, "gtg.qnt", "__gtg", "__gtg_init", "__gtg_step")
	consts := map[string]Value{"__gtg_ACCOUNTS": strs("a1", "a2", "b"), "__gtg_JOINT": strs("a1", "a2"), "__gtg_INITIAL": Int(100)}
	s, err := m.Init(consts, "__gtg_init")
	if err != nil {
		t.Fatal(err)
	}
	if got := String(s["__gtg::wallet::bal"]); got != `Map("a1" -> 100, "a2" -> 100, "b" -> 100)` {
		t.Fatalf("bal after init: %s", got)
	}
	s, ok, err := m.Step(consts, s, "__gtg_act_transfer", Int(1), Str("a1"), Str("b"), Int(150))
	if err != nil || !ok {
		t.Fatalf("transfer: %v %v", ok, err)
	}
	if got := String(s["__gtg::wallet::bal"]); got != `Map("a1" -> -50, "a2" -> 100, "b" -> 250)` {
		t.Errorf("bal after a transfer the credit line covers: %s", got)
	}
	v, err := m.Eval(consts, s, "__gtg_expect_transfer", Int(1), Bool(true))
	if err != nil || v != Bool(true) {
		t.Errorf("the transfer's expect: %v %v", v, err)
	}
	// the line's other account now has 50 of credit: a second transfer of
	// 100 is refused, and a retry of the first answers as it did
	s2, ok, err := m.Step(consts, s, "__gtg_act_transfer", Int(2), Str("a2"), Str("b"), Int(100))
	if err != nil || !ok {
		t.Fatal(err)
	}
	if v, _ := m.Eval(consts, s2, "__gtg_expect_transfer", Int(2), Bool(false)); v != Bool(true) {
		t.Errorf("a transfer past the line answered ok")
	}
	if v, err := m.Eval(consts, s2, "__gtg_inv_0"); err != nil || v != Bool(true) {
		t.Errorf("noOverdraft: %v %v", v, err)
	}
	if v, err := m.Eval(consts, s2, "__gtg_ev_0"); err != nil || v != Bool(false) {
		t.Errorf("allPosted before any post: %v %v", v, err)
	}
}
