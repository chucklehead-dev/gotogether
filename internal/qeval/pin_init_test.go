package qeval

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// Confirm initial transitions without supplying a pre-state: assignments and
// constraints must come from the original initializer, rather than the target.
func TestPinActionConfirmsFiniteInitialTransitions(t *testing.T) {
	skip.Quint(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "pin.qnt")
	const model = `module pin {
  var x: int
  var y: int
  pure val starts = Set(1, 2, 3)
  action init = { nondet n = starts.oneOf(); any {
    all { n == 1, x' = n, y' = n + 10 },
    all { n >= 2, n <= 3, x' = n, y' = n + 20 }
  } }
  action step = all { x' = x, y' = y }
}`
	if err := os.WriteFile(path, []byte(model), 0o600); err != nil {
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
	vars := map[string]string{}
	for _, v := range m.Vars {
		vars[v] = m.Display(v)
	}
	states, err := m.Successors(nil, nil, "init")
	if err != nil || len(states) != 3 {
		t.Fatalf("initial successors: %v, %v", states, err)
	}
	var body strings.Builder
	var guardedPin string
	for i, state := range states {
		pin, err := m.AsReplayed().PinAction(nil, nil, "init", nil, state, vars)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(pin, "nondet") || strings.Contains(pin, "any {") || !strings.Contains(pin, "Set(1, 2, 3).contains(") {
			t.Fatalf("initializer was not pinned with its original domain: %s", pin)
		}
		var expected []string
		for _, v := range m.Vars {
			expected = append(expected, vars[v]+" == "+String(state[v]))
		}
		fmt.Fprintf(&body, "action chosen%d = %s\nrun test%d = chosen%d.expect(and { %s })\n", i, pin, i, i, strings.Join(expected, ", "))
		for _, v := range m.Vars {
			if vars[v] == "x" && Compare(state[v], Int(1)) == 0 {
				guardedPin = pin
			}
		}
	}
	guard := regexp.MustCompile(`\b1\s*==\s*1\b`)
	if !guard.MatchString(guardedPin) || !strings.Contains(guardedPin, "contains(1)") {
		t.Fatalf("expected guarded initial choice's condition and membership: %s", guardedPin)
	}
	fmt.Fprintf(&body, "action outsideDomain = %s\nrun badDomain = outsideDomain.expect(true)\n", strings.Replace(guardedPin, "contains(1)", "contains(9)", 1))
	fmt.Fprintf(&body, "action falseGuard = %s\nrun badGuard = falseGuard.expect(true)\n", guard.ReplaceAllString(guardedPin, "1 == 9"))
	for _, backend := range []string{"rust", "typescript"} {
		t.Run(backend, func(t *testing.T) {
			runPinnedQuint(t, dir, body.String(), "^test[0-9]+$", backend, true)
			for _, name := range []string{"badDomain", "badGuard"} {
				runPinnedQuint(t, dir, body.String(), "^"+name+"$", backend, false)
			}
		})
	}
	bad := State{}
	for _, v := range m.Vars {
		bad[v] = Int(999)
	}
	if _, err := m.PinAction(nil, nil, "init", nil, bad, vars); err == nil {
		t.Fatal("invented an initializer from a target the original action cannot reach")
	}
}
