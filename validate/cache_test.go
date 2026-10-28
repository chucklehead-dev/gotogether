package validate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// processes counts the log's lines of quint subcommand sub since the
// last count (countingQuint's log).
func processes(t *testing.T, log, sub string) int {
	t.Helper()
	data, _ := os.ReadFile(log)
	os.Remove(log)
	return strings.Count(string(data), sub+"\n")
}

// A model's typecheck and its evaluation module's compile are quint's
// once for every process: a second ask, as another process makes it
// (past the process's own memo), is answered from the cache, with no
// quint process, and the same model. A change to a file the spec
// imports, to a sibling .qnt file, or to quint's version, and
// GOTOGETHER_CACHE=off, ask quint again.
func TestCacheAcrossProcesses(t *testing.T) {
	skip.Quint(t)
	t.Setenv("GOTOGETHER_CACHE", t.TempDir())
	bin, log := countingQuint(t)
	dir := t.TempDir()
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lib.qnt", "module lib {\n  pure def inc(x: int): int = x + 1\n}\n")
	write("m.qnt", "module m {\n  import lib.* from \"./lib\"\n  var n: int\n  action init = n' = 0\n  action step = n' = inc(n)\n  val small: bool = n < 10\n}\n")
	write("b.yaml", "spec: m.qnt\nmodule: m\nactions:\n  step: {}\ninvariants: [small]\n")
	spec := filepath.Join(dir, "m.qnt")
	typechecks := func() (int, *binding.Model) {
		t.Helper()
		m, err := typecheck(bin, spec, "m") // a process's first load: the cache's, not the memo's
		if err != nil {
			t.Fatal(err)
		}
		return processes(t, log, "typecheck"), m
	}
	if n, m := typechecks(); n != 1 || !m.Vals["small"] {
		t.Fatalf("a first typecheck: %d quint processes, %v", n, m)
	}
	if n, m := typechecks(); n != 0 || len(m.Actions) != 2 || !m.Vals["small"] {
		t.Errorf("a second typecheck: %d quint processes (want the cache's), %v", n, m)
	}
	compiles := func() int {
		t.Helper()
		c, err := NewChecker(filepath.Join(dir, "b.yaml"), Options{Quint: bin})
		if err != nil {
			t.Fatal(err)
		}
		em, err := c.Binding.EvalModule(c.Model, filepath.Join(t.TempDir(), "x", "eval.qnt"))
		if err != nil {
			t.Fatal(err)
		}
		if e := compileEval(c, em, t.TempDir()); e.err != nil || e.m == nil {
			t.Fatalf("compile: %v", e.err)
		}
		return processes(t, log, "compile")
	}
	if n := compiles(); n != 1 {
		t.Errorf("a first compile: %d quint processes", n)
	}
	if n := compiles(); n != 0 {
		t.Errorf("a second compile: %d quint processes (want the cache's)", n)
	}
	write("lib.qnt", "module lib {\n  pure def inc(x: int): int = x + 2\n}\n") // an import's content
	if n, _ := typechecks(); n != 1 {
		t.Errorf("after an import changed: %d typechecks", n)
	}
	if n := compiles(); n != 1 {
		t.Errorf("after an import changed: %d compiles", n)
	}
	write("other.qnt", "module other {}\n") // a sibling no import names
	if n, _ := typechecks(); n != 1 {
		t.Errorf("after a sibling changed: %d typechecks", n)
	}
	t.Setenv("GOTOGETHER_CACHE", "off")
	if n, _ := typechecks(); n != 1 {
		t.Errorf("GOTOGETHER_CACHE=off: %d typechecks", n)
	}
	t.Setenv("GOTOGETHER_CACHE", filepath.Join(dir, "cache2"))
	typechecks()
	// another quint version: a wrapper that says another
	real, _ := exec.LookPath("quint")
	other := filepath.Join(t.TempDir(), "quint")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 0.0.0-other; exit 0; fi\necho \"$1\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(other, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := typecheck(other, spec, "m"); err != nil {
		t.Fatal(err)
	}
	if n := processes(t, log, "typecheck"); n != 1 {
		t.Errorf("another quint version: %d typechecks", n)
	}
}

// No key without every file the spec imports: an import that cannot be
// read leaves the typecheck to quint, uncached.
func TestCacheNeedsEveryImport(t *testing.T) {
	t.Setenv("GOTOGETHER_CACHE", t.TempDir())
	dir := t.TempDir()
	spec := filepath.Join(dir, "m.qnt")
	os.WriteFile(spec, []byte("module m {\n  import lib.* from \"./lib\"\n}\n"), 0o644)
	if _, ok := specFilesAll(spec); ok {
		t.Error("a key with ./lib.qnt missing")
	}
	os.WriteFile(filepath.Join(dir, "lib.qnt"), []byte("module lib {}\n"), 0o644)
	files, ok := specFilesAll(spec)
	if !ok || len(files) != 2 {
		t.Errorf("files %v, %v", files, ok)
	}
}

// Processes asking for one entry at once ask quint once between them:
// the lock's holder typechecks, the others wait for its entry.
func TestCacheLockOneAsks(t *testing.T) {
	skip.Quint(t)
	t.Setenv("GOTOGETHER_CACHE", t.TempDir())
	bin, log := countingQuint(t)
	spec, _ := filepath.Abs("testdata/account.qnt")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() { // as processes: the cache's lock is a file
			defer wg.Done()
			_, errs[i] = typecheck(bin, spec, "account")
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if n := processes(t, log, "typecheck"); n != 1 {
		t.Errorf("four at once: %d typechecks, want one", n)
	}
}
