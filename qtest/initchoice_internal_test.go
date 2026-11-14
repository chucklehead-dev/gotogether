package qtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/validate"
)

// logT is a test whose logs are kept.
type logT struct {
	*testing.T
	logs []string
}

func (l *logT) Logf(format string, args ...any) {
	l.logs = append(l.logs, fmt.Sprintf(format, args...))
}

// A test whose binding's init makes a choice is told so once, when it
// first gets the binding's checker, not at every case of a Hegel test.
func TestCheckerSaysInitsChoiceOnce(t *testing.T) {
	RequireQuint(t)
	dir := t.TempDir()
	model := "module m {\n  var x: int\n  action init = { nondet v = Set(1, 2).oneOf() x' = v }\n  action inc = x' = x + 1\n  action step = inc\n  val pos = x > 0\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "m.qnt"), []byte(model), 0o600); err != nil {
		t.Fatal(err)
	}
	bp := filepath.Join(dir, "m.binding.yaml")
	if err := os.WriteFile(bp, []byte("spec: m.qnt\nmodule: m\nactions:\n  inc: {}\ninvariants: [pos]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lt := &logT{T: t}
	checker(lt, bp, validate.Options{Dir: t.TempDir()})
	checker(lt, bp, validate.Options{Dir: t.TempDir()})
	if len(lt.logs) != 1 || !strings.Contains(lt.logs[0], "m.binding.yaml: m's init makes a choice of its own (nondet v)") {
		t.Fatalf("want one note of init's choice, got %q", lt.logs)
	}
}
