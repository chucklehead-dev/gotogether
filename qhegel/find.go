package qhegel

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
	"hegel.dev/go/hegel"
)

// A Violation is the run FindViolation found, shrunk: the steps of the
// smallest failing test case and the model's verdict on them. Its Run is
// an exploration's run's record: Report is its full verdict, for Expect
// (checked once more, since cached verdicts keep only their summary), and
// Choices, if it ran on Case.Env, replay it outside Hegel
// (qenv.Replay(v.Choices): a regression test, a run on gosim, a fuzz
// seed).
type Violation struct {
	qtest.Run
	Summary string // the validator's report on Steps
	// QuintRuns and CacheHits count the cases checked with quint and those
	// decided from earlier verdicts (see caseChecker).
	QuintRuns, CacheHits int64

	found    bool // a case violated the model
	expected bool // Expect was called
}

// ViolationConfig is what FindViolation checks the cases against: Config's
// fields that mean something for a search that expects a violation, which
// checks every case as it runs (Config's Mode and rounds do not apply).
type ViolationConfig struct {
	// Binding is the path of the binding file (see Config.Binding).
	Binding string
	// Opts configure the checks as qtest's do (see Config.Opts).
	Opts []qtest.Option
	// AllowEmpty accepts test cases that record no steps (a case that drew
	// no operations), as Config.AllowEmpty does; qtest.SkipIfEmpty, for a
	// whole test that recorded nothing, is another matter.
	AllowEmpty bool
}

// FindViolation runs Hegel expecting the model to reject some test case:
// a mutant, or a bug being reproduced. Every case is checked as it runs
// (Each mode, with the verdict cache), so Hegel shrinks the failure to its
// smallest run. It fails t when every case satisfies the model, and returns
// the shrunk run for the test to assert on:
//
//	v := qhegel.FindViolation(t, qhegel.ViolationConfig{Binding: "bank.binding.yaml"},
//		func(tc hegel.TestCase, rec *qhegel.Case) {
//			hegel.RunStateful(tc, &machine{bank: New(accounts, WithMutation(SelfTransfer))})
//		}, hegel.WithTestCases(50), hegel.WithSeed(3))
//
// When no case violates the model, the Violation is empty: Expect(t,
// qtest.Conforms()) says that is what the test wants (Hegel finds
// nothing), any other expectation fails, and without Expect the test
// fails at its end.
//
// hegel.Test cannot express an expected failure, so
// this uses hegel.Run, with no example database (a later WithDatabase in
// opts wins) and Hegel's "too slow" health check off: every case runs quint.
// Hegel runs quiet, without the reproduction blob of the failure it is
// expected to find (GOTOGETHER_VERBOSE=1 shows both); the shrunk run is
// logged.
func FindViolation(t testing.TB, cfg ViolationConfig, body func(tc hegel.TestCase, c *Case), opts ...hegel.Option) *Violation {
	t.Helper()
	v := findViolation(t, cfg, qtest.CheckCases(t, "qhegel.FindViolation", cfg.Binding, cfg.Opts...), body, opts...)
	if v.found {
		v.log(t)
	}
	return v
}

// findViolation is FindViolation with its cases' checker and recording
// resolved (a System's for Shrink).
func findViolation(t testing.TB, cfg ViolationConfig, cases qtest.CaseCheck, body func(tc hegel.TestCase, c *Case), opts ...hegel.Option) *Violation {
	t.Helper()
	c := cases.Checker
	c.Options.NoITF = true
	var err error
	cc := newCaseChecker(c)
	var (
		mu    sync.Mutex
		found *Violation
	)
	base := []hegel.Option{hegel.SuppressHealthCheck(hegel.TooSlow), hegel.WithDatabase("")}
	quiet := os.Getenv("GOTOGETHER_VERBOSE") != "1"
	if quiet {
		base = append(base, hegel.WithVerbosity(hegel.VerbosityQuiet), hegel.WithReproductionBlob(false))
	}
	opts = append(base, opts...)
	if s, ok := seedFromEnv(t); ok {
		opts = append(opts, hegel.WithSeed(s)) // after the test's own: a nightly run's fresh seed wins
	}
	var hegelOut *captured
	if quiet {
		// Hegel replays the failure it is expected to find on os.Stdout
		// (draws, notes, the failing case's stack): captured, and shown
		// only when the outcome is not the expected one
		hegelOut = captureStdout()
	}
	var out string
	func() {
		defer func() { out = hegelOut.stop() }() // also when a case's t.Fatal ends the goroutine
		err = hegel.Run(func(tc hegel.TestCase) {
			rec := NewCase(tc, cases.Recorder...)
			defer rec.begin(tc, cases.Global)()
			body(tc, rec)
			steps := rec.Steps()
			if len(steps) == 0 && cfg.AllowEmpty {
				return
			}
			v, err := cc.Check(context.Background(), steps)
			if err != nil {
				tc.Errorf("qhegel: checking the run: %v", err)
				return
			}
			if rec.unsettled != "" {
				v.OK, v.Summary = false, rec.unsettled+"\n"+v.Summary
			}
			if !v.OK {
				// Hegel replays the shrunk case last, so the last failure is it
				mu.Lock()
				found = &Violation{Run: qtest.Run{Steps: steps, Choices: rec.Recorded(), Unsettled: rec.unsettled}, Summary: v.Summary}
				mu.Unlock()
				tc.Errorf("qhegel: the run does not satisfy the model:\n%s", v.Summary)
			}
		}, opts...)
	}()
	if found == nil && err != nil && out != "" {
		t.Logf("qhegel: Hegel's output:\n%s", out)
	}
	q, hits := cc.Stats()
	switch {
	case found == nil && err == nil:
		v := &Violation{QuintRuns: q, CacheHits: hits}
		t.Cleanup(func() {
			if !v.expected {
				t.Errorf("qhegel: every test case satisfied the model (%d checked with quint); want a violation, "+
					"or say none is wanted with Expect(t, qtest.Conforms())", q)
			}
		})
		t.Logf("qhegel: no case violated the model (%d checked with quint, %d decided from earlier verdicts)", q, hits)
		return v
	case found == nil:
		t.Fatalf("qhegel: no case violated the model, but the test failed: %v", err)
	}
	found.found = true
	found.QuintRuns, found.CacheHits = q, hits
	if found.Report, err = c.Check(context.Background(), found.Steps); err != nil {
		t.Fatalf("qhegel: checking the shrunk run: %v", err)
	}
	found.Report.Unsettled = found.Unsettled
	return found
}

// log logs the shrunk run: its size, its faults, and its failure in a
// line (qtest.Run.Brief), as a passing test shows its catch; the run's
// steps and whole report, and the counts of quint's runs, under
// GOTOGETHER_VERBOSE=1. The whole report on every expected shrink once
// buried what a newcomer was to read.
func (v *Violation) log(t testing.TB) {
	t.Helper()
	faults := ""
	if len(v.Choices) > 0 {
		var fs []string
		for _, c := range v.Faults() {
			fs = append(fs, c.String())
		}
		faults = "none"
		if len(fs) > 0 {
			faults = strings.Join(fs, ", ")
		}
	}
	if os.Getenv("GOTOGETHER_VERBOSE") == "1" {
		f := ""
		if faults != "" {
			f = "faults: " + faults + "\nreplay it outside Hegel: qenv.Replay(v.Choices)\n"
		}
		t.Logf("qhegel: %d runs checked with quint, %d decided from earlier verdicts; shrunk run:\n%s%s%s",
			v.QuintRuns, v.CacheHits, Render(v.Steps), f, v.Summary)
		return
	}
	n := 0
	for _, s := range v.Steps {
		if s.Model() {
			n++
		}
	}
	head := fmt.Sprintf("qhegel: shrunk to %s", plural(n, "step"))
	replay := ""
	if faults != "" {
		head += ", faults: " + faults
		replay = "replay it outside Hegel: qenv.Replay(v.Choices); "
	}
	t.Logf("%s:\n    %s\n    %sGOTOGETHER_VERBOSE=1 prints the run and its report", head, v.Run.Brief(), replay)
}

// plural is n what, with an s unless n is 1.
func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// Expect fails t unless the shrunk run's verdict meets every expectation
// (qtest's Rejects, Mismatch, Violates, AnyOf): what the failure Hegel
// found must look like, stated as for an exploration.
//
// Expect(t, qtest.Conforms()) wants no violation: Hegel found nothing.
func (v *Violation) Expect(t testing.TB, es ...qtest.RunClaim) {
	t.Helper()
	v.expected = true
	if !v.found {
		// nothing found is a conforming run: an expectation must hold of one
		none := &qtest.Run{Report: &validate.Report{Actors: []*validate.Result{{Conforms: validate.Pass}}}}
		for _, e := range es {
			if !e.Holds(none) {
				t.Fatalf("qhegel: every test case satisfied the model (%d checked with quint); want %s", v.QuintRuns, e)
			}
		}
		return
	}
	v.Run.Expect(t, es...)
}

// Last returns the last model step of the run (not an invocation or
// settle record), or the zero step if there is none.
func (v *Violation) Last() qtrace.Step {
	for i := len(v.Steps) - 1; i >= 0; i-- {
		if v.Steps[i].Model() {
			return v.Steps[i]
		}
	}
	return qtrace.Step{}
}

// Shrink runs Hegel against the system, expecting the model to reject
// some test case, and shrinks it (FindViolation): every choice of the
// system's environment is Hegel's (Choices) at the system's Rates, as its
// explorations' (sys.WithRates for others), recorded with the run's steps
// (Case.Env), so the shrunk Violation replays outside Hegel. The system's
// Opts apply, as in its explorations.
//
//	qhegel.Shrink(t, sys, hegel.WithTestCases(50)).Expect(t, qtest.Rejects("ship"))
func Shrink(t testing.TB, sys qtest.System, opts ...hegel.Option) *Violation {
	t.Helper()
	rates := sys.FaultRates()
	// its replays are an exploration's runs: what an exploration ignores,
	// it refuses now, before the search, not in a replay that fails
	// quietly; the nominal run first, which skips an unwoven build that
	// records nothing (qtest.SkipIfEmpty), as an exploration does
	cases := sys.CheckCases(t, "qhegel.Shrink (its replays are an exploration's runs)")
	if r := sys.Judge(t, qenv.Nominal{}); r.Err == nil && len(r.Steps) == 0 && cases.SkipIfEmpty {
		optional(t, "qhegel: the system recorded no step on its nominal run (qtest.SkipIfEmpty): an unwoven build?")
	}
	v := findViolation(t, ViolationConfig{Binding: sys.Binding, Opts: sys.Opts}, cases, func(tc hegel.TestCase, rec *Case) {
		c := Choices(tc)
		c.Rates = rates
		err := sys.Run(rec.Env(c))
		if u, ok := errors.AsType[*qenv.Unsettled](err); ok {
			rec.unsettled = u.Error() // an outcome, judged as an exploration judges it
		} else if err != nil {
			tc.Errorf("qhegel: the system did not run: %v", err)
		}
	}, opts...)
	if v.found {
		minimize(t, sys, v)
		v.log(t) // the faults the failure needs
	}
	return v
}

// stdoutMu serializes the redirections of os.Stdout: one Hegel run's at a
// time.
var stdoutMu sync.Mutex

// captured is os.Stdout redirected into a buffer until stop.
type captured struct {
	old, w *os.File
	done   chan string
}

// captureStdout redirects os.Stdout into a buffer (nil if it cannot).
// Output of other tests in parallel lands there too, which is the price
// of a quiet expected failure: Hegel writes to os.Stdout and nowhere else.
func captureStdout() *captured {
	stdoutMu.Lock()
	r, w, err := os.Pipe()
	if err != nil {
		stdoutMu.Unlock()
		return nil
	}
	c := &captured{old: os.Stdout, w: w, done: make(chan string)}
	go func() { //qenv:ok drains Hegel's stdout pipe, outside any simulation, so writers never block
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		r.Close()
		c.done <- b.String()
	}()
	os.Stdout = w
	return c
}

// stop restores os.Stdout and returns what was written.
func (c *captured) stop() string {
	if c == nil {
		return ""
	}
	os.Stdout = c.old
	c.w.Close()
	out := <-c.done
	stdoutMu.Unlock()
	return out
}

// seedFromEnv is GOTOGETHER_HEGEL_SEED's seed: a number, or "random" for
// a fresh one each run (a nightly run's), logged so a failure can be
// replayed with GOTOGETHER_HEGEL_SEED=<it>. Tests pin their seeds
// (hegel.WithSeed) so CI sees the same search every push; this widens it.
func seedFromEnv(t testing.TB) (int64, bool) {
	v := os.Getenv("GOTOGETHER_HEGEL_SEED")
	if v == "" {
		return 0, false
	}
	var s int64
	if v == "random" {
		var b [8]byte
		_, _ = crand.Read(b[:]) //qenv:ok a fresh Hegel seed, logged, chosen outside any simulation
		s = int64(binary.LittleEndian.Uint64(b[:]) >> 1)
	} else {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("qhegel: GOTOGETHER_HEGEL_SEED=%q: want a number or random", v)
		}
		s = n
	}
	t.Logf("qhegel: Hegel's seed %d (GOTOGETHER_HEGEL_SEED=%d replays this search)", s, s)
	return s, true
}

// optional is qtest.Optional: a skip, a failure under GOTOGETHER_STRICT=1
// (or true; another value but 0 or false fails, naming it), with the same
// message. Its own copy, so qhegel needs no core newer than the one it
// requires to build.
func optional(t testing.TB, reason string) {
	t.Helper()
	switch v := os.Getenv("GOTOGETHER_STRICT"); strings.ToLower(v) {
	case "", "0", "false":
		t.Skip(reason)
	case "1", "true":
		t.Fatal(reason + " (GOTOGETHER_STRICT is set, so a skip fails: give the job what the test needs, or unset GOTOGETHER_STRICT where the skip is legitimate)")
	default:
		t.Fatal(reason + fmt.Sprintf(" (and GOTOGETHER_STRICT=%q: set it to 1 (or true) to make skips fail, or unset it)", v))
	}
}
