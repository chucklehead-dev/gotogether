package qeval

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Cancel on a chosen cancellation checkpoint, without timing or goroutine
// scheduling. Done is still the underlying context's real cancellation signal.
type cancelAtCheckpoint struct {
	context.Context
	cancel    context.CancelFunc
	calls, at int
}

func (c *cancelAtCheckpoint) Err() error {
	c.calls++
	if c.calls == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func checkpointContext(t *testing.T, at int) *cancelAtCheckpoint {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	return &cancelAtCheckpoint{Context: ctx, cancel: cancel, at: at}
}

func TestModelContextIsolationAndGraphLifetime(t *testing.T) {
	m := compile(t, "../../../validate/testdata/graph.qnt", "graph", "init", "step")
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	view := m.WithContext(ctx)
	g, err := view.Explore(nil, "init", "step", Bound{}, nil)
	if err != nil || !g.Complete {
		t.Fatalf("graph: %v %+v", err, g)
	}
	if m.ctx != nil || g.m.ctx != nil {
		t.Fatal("request context retained by cached model or returned graph")
	}
	cancel()
	for _, scoped := range []*Model{view, view.AsReplayed(), view.Tracking(&Footprint{})} {
		if _, err := scoped.Init(nil, "init"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled view: %v", err)
		}
	}
	for _, live := range []*Model{m, m.WithContext(t.Context()), g.m} {
		if _, err := live.Successors(nil, nil, "init"); err != nil {
			t.Fatalf("canceled sibling poisoned reusable model: %v", err)
		}
	}
	deadline, stop := context.WithDeadline(t.Context(), time.Time{})
	defer stop()
	if _, err := m.WithContext(deadline).Successors(nil, nil, "init"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline identity lost: %v", err)
	}
	if len(g.Path(len(g.States)-1)) == 0 {
		t.Fatal("returned graph lost its path")
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		first := true
		_, err := m.WithContext(t.Context()).Explore(nil, "init", "step", Bound{}, func(events []Event) []Event {
			if first {
				first = false
				close(entered)
				<-resume
			}
			return events
		})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("sibling stopped before synchronization: %v", err)
	}
	_, canceled := view.Explore(nil, "init", "step", Bound{}, nil)
	close(resume)
	other := <-done
	if !errors.Is(canceled, context.Canceled) || other != nil {
		t.Fatalf("concurrent request ownership leaked: canceled=%v sibling=%v", canceled, other)
	}
}

func TestModelContextCancelsSingleEvaluation(t *testing.T) {
	m := compile(t, "cancellation.qnt", "cancellation", "init", "step")
	state, err := m.Init(nil, "init")
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"action", "unused let"} {
		t.Run(operation, func(t *testing.T) {
			ctx := checkpointContext(t, 3)
			view := m.WithContext(ctx)
			var err error
			if operation == "action" {
				_, err = view.Transitions(nil, state, "expand")
			} else {
				_, err = view.Eval(nil, state, "unused")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("single evaluation ignored cancellation (%d checks): %v", ctx.calls, err)
			}
			if _, err := m.Transitions(nil, state, "expand"); err != nil {
				t.Fatalf("retry poisoned: %v", err)
			}
		})
	}
}

func TestModelContextStaticMetadata(t *testing.T) {
	m := compile(t, "folds.qnt", "folds", "init", "step")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// Static confirmation belongs to the compiled model and its shared memo,
	// rather than one request. A canceled view must neither panic nor poison it.
	if why := m.WithContext(ctx).Unconfirmed("sumBig"); why != "" {
		t.Fatalf("request cancellation changed static confirmation: %s", why)
	}
	if why := m.Unconfirmed("sumBig"); why != "" {
		t.Fatalf("static memo poisoned: %s", why)
	}
}

func TestModelContextCancelsSearch(t *testing.T) {
	m := compile(t, "cancellation.qnt", "cancellation", "init", "step")
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	seen := 0
	g, err := m.WithContext(ctx).Explore(nil, "init", "step", Bound{States: 1000}, func(events []Event) []Event {
		seen++
		if seen == 3 {
			cancel()
		}
		return events
	})
	if !errors.Is(err, context.Canceled) || g.Complete || g.Cut != "canceled" || len(g.States) == 0 {
		t.Fatalf("canceled graph became a normal result: %v %+v", err, g)
	}

	ctx2, cancel2 := context.WithCancel(t.Context())
	t.Cleanup(cancel2)
	start, err := m.Init(nil, "init")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.WithContext(ctx2).Reach(nil, start, "step", []Goal{{Holds: func(State) (bool, error) { cancel2(); return true, nil }}}, Bound{States: 10})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("goal completion masked cancellation: %v", err)
	}
}

func TestHotComponentsContextCancellation(t *testing.T) {
	const n = 1000
	g := &Graph{Out: make([][]Edge, n), Expanded: make([]bool, n)}
	hot := make([]bool, n)
	for i := range n {
		g.Out[i] = []Edge{{Label: 0, To: (i + 1) % n}}
		g.Expanded[i], hot[i] = true, true
	}
	fam := Families{Of: []int{0}, Fair: []Fairness{WeakFair}}
	ctx := checkpointContext(t, 2)
	if out, err := g.HotComponents(ctx, hot, fam); !errors.Is(err, context.Canceled) || out != nil {
		t.Fatalf("canceled cycle search returned verdict: %v %+v", err, out)
	}
	if out, err := g.HotComponents(t.Context(), hot, fam); err != nil || len(out) != 1 || !out[0].Fair() {
		t.Fatalf("graph retained canceled ownership: %v %+v", err, out)
	}
}

func BenchmarkExploreRequestContext(b *testing.B) {
	ctx, cancel := context.WithCancel(b.Context())
	defer cancel()
	for _, c := range benchModels(b) {
		for _, scoped := range []bool{false, true} {
			name := c.name + "/base"
			m := c.m
			if scoped {
				name, m = c.name+"/request", c.m.WithContext(ctx)
			}
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := m.Explore(c.consts, "init", "step", Bound{States: c.states}, nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
