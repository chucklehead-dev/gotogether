package qeval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

const pinModel = `module pinMath { pure def bump(v: int): int = v + 1 }
module otherMath { pure def bump(v: int): int = v + 100 }
module pin {
  import pinMath as P
  import otherMath as Wrong
  var x: int
  var y: int
  pure def plus(v: int): int = P::bump(v)
  action init = all { x' = 0, y' = 0 }
  action choose = { nondet n = 1.to(3).oneOf(); all { n == 2, x' = plus(x) + n, y' = y } }
  action nested = { nondet n = Set(1, 2).oneOf(); any {
    all { n == 1, x' = x + 1, y' = y },
    all { n == 2, x' = plus(x) + n, y' = y }
  } }
  action helper(v: int): bool = { nondet n = 1.to(3).oneOf(); all { n >= v, x' = plus(x) + n, y' = y } }
  action nestedHelper = helper(2)
  action shadow = { nondet n = Set(1, 2).oneOf(); all {
    x' = n, { nondet n = Set(3, 4).oneOf(); y' = n }
  } }
  action guarded = if (x == 0) all { x' = 2, y' = 0 } else all { x' = 9, y' = y }
  action lambda = { nondet n = Set(1, 2).oneOf(); all { x' = Set(x, n).map(v => v + 1).size(), y' = y + n } }
  action membership = { nondet n = Set(1, 2).oneOf(); all { x' = n, y' = y } }
  action incomplete = x' = 3
  action unrelated = all { x' = Wrong::bump(x), y' = y }
  action step = any { choose, nested }
}`

func pinFixture(t *testing.T) (*Model, string, State, map[string]string) {
	t.Helper()
	skip.Quint(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.qnt")
	if err := os.WriteFile(path, []byte(pinModel), 0o600); err != nil {
		t.Fatal(err)
	}
	ir := filepath.Join(dir, "ir.json")
	f, err := os.Create(ir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := quintcmd.Command(t.Context(), "quint", dir, "compile", "--target", "json", "--main", "pin", path)
	cmd.Stdout = f
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	f.Close()
	if err != nil {
		t.Fatalf("quint compile: %v\n%s", err, stderr.String())
	}
	data, err := os.ReadFile(ir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Init(nil, "init")
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{}
	for _, v := range m.Vars {
		vars[v] = m.Display(v)
	}
	return m, dir, state, vars
}

func runPinnedQuint(t *testing.T, dir, body, match, backend string, wantPass bool) {
	t.Helper()
	path := filepath.Join(dir, "confirmation.qnt")
	text := "module confirmation { import pin.* from \"./pin\"\n" + body + "\n}\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := quintcmd.Command(t.Context(), "quint", dir, "test", path, "--main", "confirmation", "--match", match, "--max-samples", "1", "--backend", backend)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if (err == nil) != wantPass {
		t.Fatalf("Quint %s pass=%t: %v\n%s\n%s", backend, wantPass, err, out, text)
	}
	if wantPass && !strings.Contains(string(out), " passing") {
		t.Fatalf("no selected tests ran: %s", out)
	}
	if !wantPass && (!strings.Contains(string(out), "1 failed") || !strings.Contains(string(out), "QNT508")) {
		t.Fatalf("negative control was a tool/compile error: %v\n%s\n%s", err, out, text)
	}
}

func TestPinActionConfirmsOriginalFiniteTransitions(t *testing.T) {
	m, dir, start, vars := pinFixture(t)
	var body strings.Builder
	i := 0
	for _, action := range []string{"choose", "nested", "nestedHelper", "shadow", "guarded", "lambda", "membership"} {
		ts, err := m.Transitions(nil, start, action)
		if err != nil || len(ts) == 0 {
			t.Fatalf("%s transitions: %v %v", action, ts, err)
		}
		for _, tr := range ts {
			pin, err := m.AsReplayed().PinAction(nil, start, action, nil, tr.State, vars)
			if err != nil {
				t.Fatalf("%s pin: %v", action, err)
			}
			if strings.Contains(pin, "nondet") || strings.Contains(pin, "any {") {
				t.Fatalf("pin still samples: %s", pin)
			}
			if action != "guarded" && !strings.Contains(pin, "contains(") {
				t.Fatalf("pin discarded original domain: %s", pin)
			}
			var expected []string
			for _, v := range m.Vars {
				expected = append(expected, vars[v]+" == "+String(tr.State[v]))
			}
			fmt.Fprintf(&body, "action chosen%d = %s\nrun test%d = init.then(chosen%d).expect(and { %s })\n", i, pin, i, i, strings.Join(expected, ", "))
			i++
		}
	}
	for _, backend := range []string{"rust", "typescript"} {
		t.Run(backend, func(t *testing.T) { runPinnedQuint(t, dir, body.String(), "^test[0-9]+$", backend, true) })
	}
}

func TestPinActionRetainsGuardAndDomain(t *testing.T) {
	m, dir, start, vars := pinFixture(t)
	tr, err := m.Transitions(nil, start, "guarded")
	if err != nil {
		t.Fatal(err)
	}
	guard, err := m.PinAction(nil, start, "guarded", nil, tr[0].State, vars)
	if err != nil {
		t.Fatal(err)
	}
	tr, err = m.Transitions(nil, start, "membership")
	if err != nil {
		t.Fatal(err)
	}
	member, err := m.PinAction(nil, start, "membership", nil, tr[0].State, vars)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(member, "contains(1)") {
		t.Fatalf("unexpected membership pin %s", member)
	}
	member = strings.Replace(member, "contains(1)", "contains(9)", 1)
	body := fmt.Sprintf("action badStart = all { x' = 1, y' = 0 }\naction pinnedGuard = %s\naction member = %s\nrun badGuard = badStart.then(pinnedGuard).expect(x == 2)\nrun badDomain = init.then(member).expect(x == 1)\n", guard, member)
	for _, backend := range []string{"rust", "typescript"} {
		for _, test := range []string{"badGuard", "badDomain"} {
			t.Run(backend+"/"+test, func(t *testing.T) { runPinnedQuint(t, dir, body, "^"+test+"$", backend, false) })
		}
	}
}

func TestPinActionRefusesGuessedTargetsAndNames(t *testing.T) {
	m, _, start, vars := pinFixture(t)
	bad := State{}
	for k, v := range start {
		bad[k] = v
	}
	for k := range bad {
		bad[k] = Int(999)
	}
	if _, err := m.PinAction(nil, start, "choose", nil, bad, vars); err == nil {
		t.Fatal("invented a successor from target state")
	}
	tr, err := m.Transitions(nil, start, "choose")
	if err != nil {
		t.Fatal(err)
	}
	for k := range vars {
		vars[k] = "duplicate"
	}
	if _, err := m.PinAction(nil, start, "choose", nil, tr[0].State, vars); err == nil {
		t.Fatal("guessed colliding source variable names")
	}
	vars = map[string]string{}
	for _, v := range m.Vars {
		vars[v] = m.Display(v)
	}
	if _, err := m.PinAction(nil, start, "incomplete", nil, start, vars); err == nil {
		t.Fatal("pinned a partial assignment as a complete transition")
	}
}
