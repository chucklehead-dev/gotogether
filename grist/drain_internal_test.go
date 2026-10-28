package grist

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/memstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// A poller stopped by its context's end is no work in hand: once a
// node's work is cancelled its pollers end and the node is idle, so a
// drain's settle can find it quiet. Counted as running for ever, a node
// whose context ended at the signal (counters' stopNodeAtSignal) was
// never idle, and its every drain waited out the settle's bound.
func TestStoppedPollersAreIdle(t *testing.T) {
	type Noted struct {
		Topic string `json:"topic" grist:"tag"`
	}
	app := New("stops")
	noted := NewEvent[Noted](app, "noted")
	NewProcessor(app, "skip", noted, func(ctx context.Context, r *Reads, e Noted, m Meta) Result { return Skip() })
	st := memstore.New("stops")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := st.CreateTenant(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	var n *node
	if err := app.Start(ctx, st, StartOption{apply: func(c *startConfig) { c.onNode = func(x *node) { n = x } }}); err != nil {
		t.Fatal(err)
	}
	cancel()
	for deadline := time.Now().Add(10 * time.Second); !n.idle(); time.Sleep(10 * time.Millisecond) { //qenv:ok a real node's pollers, outside any simulation
		if time.Now().After(deadline) { //qenv:ok the real wait's deadline
			t.Fatalf("10 s after its context ended the node is not idle: %d pollers, %d waiting", n.pollers.Load(), n.waiting.Load())
		}
	}
}

// Terminating's context ends at Main's signal (the context Main puts in
// the node's, which keeps the values but not the cancellation), and in a
// simulation at the process's termination; elsewhere it is ctx itself.
func TestTerminating(t *testing.T) {
	sig, signal := context.WithCancel(context.Background())
	node := context.WithoutCancel(context.WithValue(sig, signalKey{}, sig))
	term := Terminating(node)
	if term.Err() != nil {
		t.Fatal("cancelled before the signal")
	}
	signal()
	select { //qenv:ok Main's signal, outside any simulation: one case is the test's timeout
	case <-term.Done():
	case <-time.After(10 * time.Second): //qenv:ok the real wait's bound
		t.Fatal("not cancelled 10 s after the signal")
	}
	if node.Err() != nil {
		t.Error("the node's own context ended with the signal")
	}
	if bg := context.Background(); Terminating(bg) != bg {
		t.Error("without Main's signal, not ctx itself")
	}
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	var ended bool
	p := s.Process("node", false, func(ctx context.Context) {
		term := Terminating(ctx)
		qenv.WaitIdle(ctx, func() bool { return term.Err() != nil })
		ended = true
		qenv.ProcessOf(ctx).Exit()
	})
	s.Spawn("operator", func(ctx context.Context) {
		qenv.Sleep(ctx, time.Second)
		if p.Incarnation == 1 {
			p.Terminate()
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if !ended {
		t.Error("in a simulation, not cancelled at the process's termination")
	}
}

// A processor's lag is the positions from its checkpoint to the head
// while one of its triggers waits, and 0 once it handled every one: its
// checkpoint stays at the last trigger it handled while the head moves
// on with other events (as first built, head minus checkpoint, a
// processor that had caught up was never at 0 while other events came).
func TestProcessorLag(t *testing.T) {
	type A struct{}
	type B struct{}
	app := New("lags")
	a := NewEvent[A](app, "a")
	NewEvent[B](app, "b")
	NewProcessor(app, "on-a", a, func(ctx context.Context, r *Reads, e A, m Meta) Result { return Skip() })
	st := memstore.New("lags")
	ctx := context.Background()
	if err := st.CreateTenant(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	h := &health{app: app}
	h.started(st, nil, nil)
	lag := func() string {
		for _, l := range strings.Split(h.metrics(ctx, time.Now()), "\n") { //qenv:ok the scrape's time, outside any simulation
			if strings.HasPrefix(l, "grist_processor_lag{") {
				return l[strings.LastIndex(l, " ")+1:]
			}
		}
		return "none"
	}
	appendAs := func(typ string, cp *store.CheckpointMove) {
		if _, err := st.Append(ctx, "t1", store.Batch{Events: []store.Event{{Type: typ, Body: []byte(`{}`)}}, Checkpoint: cp}); err != nil {
			t.Fatal(err)
		}
	}
	appendAs("a", nil)
	if got := lag(); got != "1" {
		t.Errorf("a trigger waiting: lag %s, want 1", got)
	}
	appendAs("b", &store.CheckpointMove{Processor: "on-a", From: 0, To: 1}) // the trigger handled, then another event
	if got := lag(); got != "0" {
		t.Errorf("every trigger handled, another event after it: lag %s, want 0", got)
	}
	appendAs("a", nil)
	if got := lag(); got != "2" {
		t.Errorf("a trigger waiting after the other event: lag %s, want 2", got)
	}
}
