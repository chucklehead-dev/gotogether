package qeval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
)

// compile compiles testdata's module name (with its init and step) to IR.
func compile(t *testing.T, file, main, init, step string) *Model {
	t.Helper()
	skip.Quint(t)
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

// Builtins at their edges (signs of % and /, closures of let operators,
// operators as arguments, duplicate map keys, records' field order,
// match's default, a fold over a set): the evaluator's values are quint's,
// on both its backends.
func TestEdgesAgreeWithQuint(t *testing.T) {
	m := compile(t, "edges.qnt", "edges", "init", "step")
	s, err := m.Init(nil, "init")
	if err != nil {
		t.Fatal(err)
	}
	got := s["edges::out"]
	if got == nil {
		got = s["out"]
	}
	for _, backend := range []string{"rust", "typescript"} {
		dir := t.TempDir()
		out := filepath.Join(dir, "t.itf.json")
		abs, _ := filepath.Abs("testdata/edges.qnt")
		cmd := exec.Command("quint", "run", "--backend", backend, "--max-steps", "0", "--max-samples", "1", "--out-itf", out, abs)
		cmd.Dir = filepath.Dir(abs)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("quint run (%s): %v\n%s", backend, err, b)
		}
		tr, err := itf.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		want, err := FromITF(tr.States[0].Vars["out"])
		if err != nil {
			t.Fatal(err)
		}
		wt, gt := want.(Tup), got.(Tup)
		for i := range wt {
			if Compare(wt[i], gt[i]) != 0 {
				t.Errorf("%s: value %d: quint %s, the evaluator %s", backend, i+1, String(wt[i]), String(gt[i]))
			}
		}
	}
}

// A constructor is a definition that makes its variant, not another
// definition of the name: a literal's capitalised name is one only then.
func TestConstructor(t *testing.T) {
	m := compile(t, "edges.qnt", "edges", "init", "step")
	for tag, want := range map[string]bool{"A": true, "B": true, "C": true, "mod1": false, "tag": false, "Z": false} {
		if got := m.Constructor(tag); got != want {
			t.Errorf("Constructor(%s) = %v", tag, got)
		}
	}
}

// A fold whose result depends on the set's order is refused: quint's
// backends fold in orders of their own.
func TestOrderDependentFoldRefused(t *testing.T) {
	m := compile(t, "edges.qnt", "edges", "init", "step")
	_, err := m.Eval(nil, nil, "foldOrder")
	if _, ok := err.(*Unsupported); !ok {
		t.Errorf("a fold into a list: %v, want Unsupported", err)
	}
}

// A range that counts down is refused: quint's Rust backend makes it
// empty, its TypeScript one counts down (a probe found the evaluator on
// Rust's side, which a TypeScript batch confirming it would contradict).
func TestRangeDownRefused(t *testing.T) {
	m := compile(t, "edges.qnt", "edges", "init", "step")
	if _, err := m.Eval(nil, nil, "rangeDown"); err == nil {
		t.Errorf("range(5, 2): no error, want Unsupported")
	} else if _, ok := err.(*Unsupported); !ok {
		t.Errorf("range(5, 2): %v, want Unsupported", err)
	}
}
