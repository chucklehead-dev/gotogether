package qhegel

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/chucklehead-dev/gotogether/connect"
	"github.com/chucklehead-dev/gotogether/itf"
	"hegel.dev/go/hegel"
)

// Traces generates model traces once per test binary and Config (`quint run
// --mbt`, see connect.GenerateRun) and returns them decoded, for Replay.
func Traces(t testing.TB, c connect.Config) []*itf.Trace {
	t.Helper()
	key := fmt.Sprintf("%+v", c)
	if v, ok := traceCache.Load(key); ok {
		return v.([]*itf.Trace)
	}
	files, rc, err := connect.GenerateRun(context.Background(), c)
	if err != nil {
		t.Fatalf("qhegel: generating traces: %v", err)
	}
	var out []*itf.Trace
	for _, f := range files {
		tr, err := itf.ReadFile(f)
		if err != nil {
			t.Fatalf("qhegel: %v", err)
		}
		out = append(out, tr)
	}
	t.Logf("qhegel: %d model traces from %s (seed %s)", len(out), c.Spec, rc.Seed)
	traceCache.Store(key, out)
	return out
}

var traceCache sync.Map

// VaryOptions configure Replay.
type VaryOptions struct {
	connect.ReplayOptions
	// Prefix lets Hegel choose how many steps of the trace to replay, so a
	// failure shrinks to the shortest failing prefix as well.
	Prefix bool
}

// VaryTraces is one Hegel test case of model-driven testing: Hegel draws one of
// traces (and, with Prefix, a prefix of it), and the trace is replayed on
// the driver newDriver returns. newDriver gets the test case, so handlers
// can hegel.Draw the concrete details the model leaves abstract; Hegel then
// shrinks those along with the choice of trace. A failing step fails the
// test case with the step and, for state mismatches, the field-level diff.
//
//	traces := qhegel.Traces(t, connect.Config{Spec: "bank.qnt", MaxSamples: 50, MaxSteps: 20})
//	hegel.Test(t, func(ht *hegel.T) {
//		qhegel.VaryTraces(ht, traces, func(tc hegel.TestCase) connect.Driver { return newDriver(tc) }, qhegel.VaryOptions{Prefix: true})
//	})
func VaryTraces(tc hegel.TestCase, traces []*itf.Trace, newDriver func(tc hegel.TestCase) connect.Driver, opt VaryOptions) {
	if len(traces) == 0 {
		tc.Errorf("qhegel: no traces to replay")
	}
	idx := hegel.Draw(tc, hegel.Integers(0, len(traces)-1))
	tr := traces[idx]
	if opt.Prefix && len(tr.States) > 1 {
		n := hegel.Draw(tc, hegel.Integers(1, len(tr.States)-1))
		cut := *tr
		cut.States = tr.States[:n+1]
		tr = &cut
	}
	ro := opt.ReplayOptions
	ro.Trace = idx
	inner := ro.OnStep
	ro.OnStep = func(s *connect.Step) {
		tc.Note(fmt.Sprintf("model step %d: %s", s.Index, summary(s)))
		if inner != nil {
			inner(s)
		}
	}
	if err := connect.Replay(tr, newDriver(tc), ro); err != nil {
		if se, ok := errors.AsType[*connect.StepError](err); ok && se.Kind != "trace" {
			tc.Errorf("qhegel: %v\n%s", err, se.Step)
		}
		tc.Errorf("qhegel: %v", err)
	}
}

func summary(s *connect.Step) string {
	out := s.Action
	if len(s.Picks) > 0 {
		out += "("
		first := true
		for _, k := range slices.Sorted(maps.Keys(s.Picks)) {
			if !first {
				out += ", "
			}
			first = false
			out += k + "=" + s.Picks[k].String()
		}
		out += ")"
	}
	return out
}
