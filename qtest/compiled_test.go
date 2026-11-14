package qtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// compiledCounter copies testdata's counter into a directory of its own
// and compiles its model beside the binding (validate.CompiledFor),
// returning the binding's path. It needs quint, to compile.
func compiledCounter(t *testing.T) string {
	t.Helper()
	skip.Quint(t)
	dir := t.TempDir()
	for _, f := range []string{"counter.qnt", "counter.yaml"} {
		data, err := os.ReadFile(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b := filepath.Join(dir, "counter.yaml")
	cm, err := validate.Compile(b, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.Write(validate.CompiledFor(b)); err != nil {
		t.Fatal(err)
	}
	return b
}

// two increments: atMostOne fails after the second
func overCount() []qtrace.Step {
	return []qtrace.Step{{Action: "inc", Seq: 1}, {Action: "inc", Seq: 2}}
}

func judgedBy(t *testing.T, r *Run) string {
	t.Helper()
	if r.Report == nil || len(r.Report.Actors) != 1 {
		t.Fatalf("report %+v: want one actor", r.Report)
	}
	return r.Report.Actors[0].JudgedBy()
}

// With GOTOGETHER_COMPILED=1 a binding's runs are judged by its compiled
// model beside it, the Go evaluator alone, the same verdicts as quint's:
// a failure is the evaluator's (with quint, a failure is quint's). With 0,
// quint judges though the artifact is there. Not parallel: it sets the
// environment.
func TestCompiledModelJudgesTests(t *testing.T) {
	b := compiledCounter(t)
	t.Setenv(compiledEnv, "1")
	run := Validate(t, b, overCount())
	run.Expect(t, Violates("atMostOne"))
	if got := judgedBy(t, run); got != "evaluator" {
		t.Errorf("compiled: the failure judged by %q, want the evaluator's alone", got)
	}
	Validate(t, b, []qtrace.Step{{Action: "inc", Seq: 1}, {Action: "dec", Seq: 2}}).Expect(t, Conforms())

	t.Setenv(compiledEnv, "0")
	run = Validate(t, b, overCount())
	run.Expect(t, Violates("atMostOne"))
	if got := judgedBy(t, run); got != "quint" {
		t.Errorf("GOTOGETHER_COMPILED=0: the failure judged by %q, want quint's", got)
	}
}

// A compiled model older than its source is refused with the fix, and
// GOTOGETHER_COMPILED=1 without one fails naming where it was looked for:
// neither falls back to quint, which would hide the missing or stale
// artifact in CI's job without quint. RequireModel does not skip a
// binding with one. Not parallel: it sets the environment.
func TestCompiledModelRefused(t *testing.T) {
	b := compiledCounter(t)
	t.Setenv(compiledEnv, "1")
	RequireModel(t, b) // it has one: no skip

	spec := filepath.Join(filepath.Dir(b), "counter.qnt")
	data, err := os.ReadFile(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec, append(data, "\n// edited\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &failTB{TB: t}
	runFail(func() { Validate(f, b, overCount()) })
	if !f.failed || len(f.errs) == 0 || !strings.Contains(f.errs[0], "is stale") || !strings.Contains(f.errs[0], "gotogether compile -binding") {
		t.Errorf("stale artifact: errors %q", f.errs)
	}

	if err := os.Remove(validate.CompiledFor(b)); err != nil {
		t.Fatal(err)
	}
	f = &failTB{TB: t}
	runFail(func() { Validate(f, b, overCount()) })
	if !f.failed || len(f.errs) == 0 || !strings.Contains(f.errs[0], "has none at "+validate.CompiledFor(b)) {
		t.Errorf("missing artifact: errors %q", f.errs)
	}
}

// runFail runs fn on a goroutine of its own, so that failTB's Fatalf
// (runtime.Goexit) ends it alone.
func runFail(fn func()) {
	done := make(chan struct{})
	go func() { //qenv:ok failTB's Fatalf ends the goroutine (runtime.Goexit)
		defer close(done)
		fn()
	}()
	<-done
}

// CompiledFor is the convention's path: the stem beside the binding.
func TestCompiledFor(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"stock.binding.yaml":        "stock.model.json",
		"dir/stock.binding.yml":     filepath.Join("dir", "stock.model.json"),
		"dir/account.yaml":          filepath.Join("dir", "account.model.json"),
		"payments.day.binding.yaml": "payments.day.model.json",
	} {
		if got := validate.CompiledFor(in); got != want {
			t.Errorf("CompiledFor(%q) = %q, want %q", in, got, want)
		}
	}
}
