package qeval

import (
	"errors"
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

// AsReplayed refuses a step some of its choice's values disable, which a
// quint replay takes or not at random; the model as compiled takes it.
// A choice every value of which enables the step is taken either way.
func TestAsReplayedRefusesPartialChoices(t *testing.T) {
	m := compile(t, "choice.qnt", "choice", "init", "step")
	s, err := m.Init(nil, "init")
	if err != nil {
		t.Fatal(err)
	}
	if next, ok, err := m.Step(nil, s, "pickSeven"); err != nil || !ok || next["choice::v"] != Int(7) && next["v"] != Int(7) {
		t.Errorf("pickSeven as compiled: %v %v %v", next, ok, err)
	}
	r := m.AsReplayed()
	var u *Unsupported
	if _, _, err := r.Step(nil, s, "pickSeven"); !errors.As(err, &u) || !strings.Contains(err.Error(), "at random") {
		t.Errorf("pickSeven as replayed: %v, want Unsupported", err)
	}
	if _, ok, err := r.Step(nil, s, "pickAny"); err != nil || !ok {
		t.Errorf("pickAny as replayed: %v %v", ok, err)
	}
	if _, ok, err := m.Step(nil, s, "pickAny"); err != nil || !ok {
		t.Errorf("pickAny as compiled: %v %v", ok, err)
	}
}

// A set's fold is the evaluator's only when every order gives it (an
// operator known to commute, or a small set tried in every order), and
// then quint's, on both backends; otherwise it is quint's to give.
// Folded sorted and reversed alone, the round robin and the second
// element agreed both ways (review 11, F1).
func TestSetFoldsAgreeWithQuintOrAreRefused(t *testing.T) {
	m := compile(t, "folds.qnt", "folds", "init", "step")
	for _, name := range []string{"roundRobin", "second", "putBig", "notMin", "failsSome"} {
		_, err := m.Eval(nil, nil, name)
		if _, ok := err.(*Unsupported); !ok {
			t.Errorf("%s: %v, want Unsupported", name, err)
		}
	}
	s, err := m.Init(nil, "init")
	if err != nil {
		t.Fatal(err)
	}
	got := s["folds::out"]
	if got == nil {
		got = s["out"]
	}
	for _, backend := range []string{"rust", "typescript"} {
		dir := t.TempDir()
		out := filepath.Join(dir, "t.itf.json")
		abs, _ := filepath.Abs("testdata/folds.qnt")
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
		if Compare(want, got) != 0 {
			t.Errorf("%s: quint %s, the evaluator %s", backend, String(want), String(got))
		}
	}
	// the shapes known to commute are taken whatever the set's size: the
	// five over 20 elements were not tried in every order
	for _, name := range []string{"sumBig", "sumCond", "minBig", "maxBig", "unionBig"} {
		if !commutesIn(t, m, name) {
			t.Errorf("%s: not recognised as commuting", name)
		}
	}
	for _, name := range []string{"roundRobin", "second", "putSmall", "notMin", "failsSome"} {
		if commutesIn(t, m, name) {
			t.Errorf("%s: recognised as commuting", name)
		}
	}
}

// commutesIn reports whether the fold the val name applies has an
// operator commutes recognises.
func commutesIn(t *testing.T, m *Model, name string) bool {
	t.Helper()
	d, ok := m.Decl(name)
	if !ok {
		t.Fatalf("no val %s", name)
	}
	e := d.Expr
	for e.Kind == "app" && e.Opcode != "fold" && len(e.Args) > 0 {
		e = e.Args[0]
	}
	if e.Opcode != "fold" || e.Args[2].Kind != "lambda" {
		t.Fatalf("%s: no fold of a lambda", name)
	}
	ev := &evaluation{m: m}
	return ev.commutes(&Lambda{params: paramNames(e.Args[2]), body: e.Args[2].Expr})
}

// Unconfirmed names what makes a pass less sure: a set fold whose
// operator is of no commuting shape, reached through the definitions an
// action calls; a fold of a commuting one, or none, is "".
func TestUnconfirmedFolds(t *testing.T) {
	m := compile(t, "folds.qnt", "folds", "init", "step")
	for name, want := range map[string]bool{"sumBig": false, "minBig": false, "unionBig": false, "step": false,
		"putSmall": true, "countSmall": true, "roundRobin": true, "init": true} {
		if got := m.Unconfirmed(name) != ""; got != want {
			t.Errorf("Unconfirmed(%s) = %q, want it said: %v", name, m.Unconfirmed(name), want)
		}
	}
	// memoized, the same answer (and AsReplayed's copy shares it)
	if a, b := m.Unconfirmed("init"), m.AsReplayed().Unconfirmed("init"); a != b || a == "" {
		t.Errorf("Unconfirmed(init) = %q, then %q", a, b)
	}
}
