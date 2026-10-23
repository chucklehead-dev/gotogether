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

// Linearize in a case's options does nothing (a case is checked in the
// order it recorded): FindViolation refuses it, naming why.
func TestRefusesLinearize(t *testing.T) {
	f := &fatalTB{TB: t}
	done := make(chan struct{})
	go func() {
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
	go func() {
		defer close(done)
		sys := qtest.System{Binding: "testdata/bank.yaml", Opts: []qtest.Option{qtest.Global()}, Run: func(qenv.Chooser) error { return nil }}
		qhegel.Shrink(f, sys, qenv.Rates{})
	}()
	<-done
	if !strings.Contains(f.msg, "Global() does nothing in qhegel.Shrink") {
		t.Errorf("got %q", f.msg)
	}
}
