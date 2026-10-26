package qhegel_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qtest"
	"hegel.dev/go/hegel"
)

// fatalTB records a Fatalf and ends its goroutine, as testing.T does.
type fatalTB struct {
	testing.TB
	msg string
}

func (f *fatalTB) Helper() {}
func (f *fatalTB) Fatalf(format string, a ...any) {
	f.msg = fmt.Sprintf(format, a...)
	runtime.Goexit()
}
func (f *fatalTB) Fatal(a ...any) {
	f.msg = fmt.Sprint(a...)
	runtime.Goexit()
}
func (f *fatalTB) Skip(a ...any) {
	f.msg = "skip: " + fmt.Sprint(a...)
	runtime.Goexit()
}
func (f *fatalTB) Skipf(format string, a ...any) { f.Skip(fmt.Sprintf(format, a...)) }

// Linearize in a case's options does nothing (a case is checked in the
// order it recorded): FindViolation refuses it, naming why.
func TestRefusesLinearize(t *testing.T) {
	f := &fatalTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok runs outside any simulation; Goexit ends it on Fatalf
		defer close(done)
		qhegel.FindViolation(f, qhegel.ViolationConfig{Binding: "testdata/bank.yaml", Opts: []qtest.Option{qtest.Linearize()}},
			func(hegel.TestCase, *qhegel.Case) {})
	}()
	<-done
	if !strings.Contains(f.msg, "Linearize() does nothing in qhegel.FindViolation") {
		t.Errorf("got %q", f.msg)
	}
}

// Shrink's replays are an exploration's runs: what an exploration ignores
// (Global) it refuses before the search, where the replays would have
// failed quietly and left the violation unshrunk.
func TestShrinkRefusesGlobal(t *testing.T) {
	f := &fatalTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok runs outside any simulation; Goexit ends it on Fatalf
		defer close(done)
		sys := qtest.System{Binding: "testdata/bank.yaml", Opts: []qtest.Option{qtest.Global()}, Run: func(qenv.Chooser) error { return nil }}
		qhegel.Shrink(f, sys)
	}()
	<-done
	if !strings.Contains(f.msg, "Global() does nothing in qhegel.Shrink") {
		t.Errorf("got %q", f.msg)
	}
}

// Shrink honours a System's SkipIfEmpty: a system that records nothing
// (an unwoven build) skips the test, before any search and without an
// exploration of its own (which would renumber the test's). Under
// GOTOGETHER_STRICT=1 the skip fails, with its message.
func TestShrinkSkipsWhenEmpty(t *testing.T) {
	for _, strict := range []string{"", "1", "true", "yes"} {
		t.Setenv("GOTOGETHER_STRICT", strict)
		f := &fatalTB{TB: t}
		done := make(chan struct{})
		go func() { //qenv:ok runs outside any simulation; Goexit ends it on Skip
			defer close(done)
			sys := qtest.System{Binding: "testdata/bank.yaml", Opts: []qtest.Option{qtest.SkipIfEmpty()}, Run: func(qenv.Chooser) error { return nil }}
			qhegel.Shrink(f, sys)
		}()
		<-done
		skipped := strings.HasPrefix(f.msg, "skip: ")
		says := map[string]string{"1": "GOTOGETHER_STRICT is set", "true": "GOTOGETHER_STRICT is set", "yes": `GOTOGETHER_STRICT="yes": set it to 1`}[strict]
		if skipped != (strict == "") || !strings.Contains(f.msg, "recorded no step") || !strings.Contains(f.msg, says) {
			t.Errorf("GOTOGETHER_STRICT=%q: got %q", strict, f.msg)
		}
	}
}

// When Hegel finds nothing, an expectation is judged on a conforming run,
// by its type: AnyOf(Conforms(), ...) holds, Rejects does not. Their
// descriptions were compared, and AnyOf's failed.
func TestNothingFoundJudgedByType(t *testing.T) {
	qtest.RequireQuint(t)
	nothing := func(t testing.TB) *qhegel.Violation {
		return qhegel.FindViolation(t, qhegel.ViolationConfig{Binding: "testdata/bank.yaml", AllowEmpty: true},
			func(hegel.TestCase, *qhegel.Case) {}, hegel.WithTestCases(3))
	}
	nothing(t).Expect(t, qtest.AnyOf(qtest.Conforms(), qtest.Rejects("deposit")))
	f := &fatalTB{TB: t}
	done := make(chan struct{})
	go func() { //qenv:ok runs outside any simulation; Goexit ends it on Fatalf
		defer close(done)
		nothing(f).Expect(f, qtest.Rejects("deposit"))
	}()
	<-done
	if !strings.Contains(f.msg, "every test case satisfied the model") {
		t.Errorf("got %q", f.msg)
	}
}
