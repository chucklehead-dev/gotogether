package qenv_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/qhttp"
	"github.com/chucklehead-dev/gotogether/qenv/simlog"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// effectRecords are the effect records of kind in steps.
func effectRecords(steps []qtrace.Step, kind string) []qtrace.Step {
	var out []qtrace.Step
	for _, s := range steps {
		if s.Record.Effect == kind {
			out = append(out, s)
		}
	}
	return out
}

// A send under an open call's scope is the call's, on the call's
// recorder, though the simulation carries none: the Service's request and
// the Queue's publish are recorded as qobs.Effect records one (By "call"),
// where they once recorded nothing (qenv's effect asked qobs.From alone).
// Without the scope they are recorded nowhere, and their record costs no
// allocation. Not parallel: AllocsPerRun.
func TestDoublesRecordEffectsUnderAScope(t *testing.T) {
	sink := &qobs.MemorySink{}
	rec := qobs.NewRecorder(sink)
	s := qenv.New(qenv.Nominal{}) // no recorder in the simulation's context
	svc := qenv.NewService(s, "provider", nil, func(context.Context, capture) (string, error) { return "ok", nil })
	q := qenv.NewQueue(s, "mail", nil)
	var allocs float64
	s.Spawn("shop", func(ctx context.Context) {
		c := rec.Invoke(ctx, "pay")
		sc := c.Scope(ctx)
		if _, err := svc.Call(sc, capture{Key: "k1", Amount: 10}, time.Second); err != nil {
			t.Errorf("call: %v", err)
		}
		q.Publish(sc, map[string]any{"order": "o1"})
		qobs.Effect(sc, "manual.send", "to", "a")
		c.Done(nil)
		// no scope, no recorder: nothing records, nothing is built
		allocs = testing.AllocsPerRun(100, func() { qenv.SendEffect(ctx, "rpc", "provider", "request", capture{Key: "k3"}) })
		if _, err := svc.Call(ctx, capture{Key: "k2"}, time.Second); err != nil {
			t.Errorf("call: %v", err)
		}
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
	trs, _ := qtrace.Reconstruct(sink.Steps())
	if len(trs) != 1 || len(trs[0].Steps) != 1 {
		t.Fatalf("traces %v", trs)
	}
	pay := trs[0].Steps[0]
	var kinds []string
	for _, e := range pay.Effects {
		if e.By != "call" || e.Late {
			t.Errorf("pay's effect %v: not the call's", e)
		}
		kinds = append(kinds, e.Kind)
	}
	if got := strings.Join(kinds, " "); got != "rpc.provider.request queue.mail.publish manual.send" {
		t.Errorf("pay's effects %s, want the request, the publish and the manual send", got)
	}
	if n := len(effectRecords(sink.Steps(), "rpc.provider.request")); n != 1 {
		t.Errorf("%d requests recorded: the one outside the scope was recorded too", n)
	}
	if allocs != 0 {
		t.Errorf("a send no run records allocates %v times", allocs)
	}
}

// Every double's send records exactly where qobs.Effect, called beside it
// in the same context, does: on the same recorder (or none), under the
// same call. The contexts: a recorder in the simulation's context, an
// open call's scope only, neither, and a scope over WithRecorder(nil)
// (the explicit nil shadows the goroutine's run, never the call's).
func TestDoublesRecordWhereEffectDoes(t *testing.T) {
	t.Parallel()
	type double struct {
		kind  string
		setup func(s *qenv.Sim) func(ctx context.Context)
	}
	doubles := map[string]double{
		"service": {"rpc.provider.request", func(s *qenv.Sim) func(context.Context) {
			svc := qenv.NewService(s, "provider", nil, func(context.Context, capture) (string, error) { return "ok", nil })
			return func(ctx context.Context) { _, _ = svc.Call(ctx, capture{Key: "k"}, time.Second) }
		}},
		"service.Effect": {"rpc.provider.capture", func(s *qenv.Sim) func(context.Context) {
			svc := qenv.NewService(s, "provider", nil, func(context.Context, capture) (string, error) { return "ok", nil })
			svc.Effect = func(req capture) (string, []any) { return "rpc.provider.capture", []any{"key", req.Key} }
			return func(ctx context.Context) { _, _ = svc.Call(ctx, capture{Key: "k"}, time.Second) }
		}},
		"queue": {"queue.mail.publish", func(s *qenv.Sim) func(context.Context) {
			q := qenv.NewQueue(s, "mail", nil)
			return func(ctx context.Context) { q.Publish(ctx, "m") }
		}},
		"qhttp": {"http.api.request", func(s *qenv.Sim) func(context.Context) {
			var srv *qhttp.Server
			p := s.Process("api", false, func(ctx context.Context) {
				srv.Handle(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			})
			srv = qhttp.NewServer(s, "api", p)
			client := &http.Client{Transport: qhttp.NewTransport(srv)}
			return func(ctx context.Context) {
				req, err := http.NewRequestWithContext(ctx, "POST", "http://api/pay", strings.NewReader("{}"))
				if err != nil {
					panic(err)
				}
				if resp, err := client.Do(req); err == nil {
					_ = resp.Body.Close()
				}
			}
		}},
		"simlog": {"log.orders.produce", func(s *qenv.Sim) func(context.Context) {
			log := simlog.New(s, "orders", 1)
			return func(ctx context.Context) { log.Producer(true).Produce(ctx, "k", []byte("v"), func(error) {}) }
		}},
	}
	type mode struct {
		simRec   bool                                                          // the simulation's context carries the recorder
		scope    func(ctx context.Context, rec *qobs.Recorder) context.Context // the send's context from the task's
		recorded bool                                                          // the send is rec's
		byCall   bool                                                          // under the call
	}
	modes := map[string]mode{
		"recorder": {simRec: true, scope: func(ctx context.Context, _ *qobs.Recorder) context.Context { return ctx }, recorded: true},
		"scope": {scope: func(ctx context.Context, rec *qobs.Recorder) context.Context {
			return rec.Invoke(ctx, "pay").Scope(ctx)
		}, recorded: true, byCall: true},
		"neither": {scope: func(ctx context.Context, _ *qobs.Recorder) context.Context { return ctx }},
		"nil+scope": {scope: func(ctx context.Context, rec *qobs.Recorder) context.Context {
			ctx = qobs.WithRecorder(ctx, nil)
			return rec.Invoke(ctx, "pay").Scope(ctx)
		}, recorded: true, byCall: true},
	}
	for dn, d := range doubles {
		for mn, m := range modes {
			t.Run(dn+"/"+mn, func(t *testing.T) {
				t.Parallel()
				sink := &qobs.MemorySink{}
				rec := qobs.NewRecorder(sink)
				var c qenv.Chooser = qenv.Nominal{}
				if m.simRec {
					c = qenv.WithContext(c, qobs.WithRecorder(t.Context(), rec))
				}
				s := qenv.New(c)
				send := d.setup(s)
				s.Spawn("client", func(ctx context.Context) {
					ctx = m.scope(ctx, rec)
					send(ctx)
					qobs.Effect(ctx, "probe")
				})
				if err := s.Run(time.Minute); err != nil {
					t.Fatal(err)
				}
				s.Close()
				sent, probe := effectRecords(sink.Steps(), d.kind), effectRecords(sink.Steps(), "probe")
				if len(probe) != len(sent) || len(sent) > 1 {
					t.Fatalf("%d sends recorded, qobs.Effect %d", len(sent), len(probe))
				}
				if (len(sent) == 1) != m.recorded {
					t.Fatalf("recorded %v, want %v", len(sent) == 1, m.recorded)
				}
				if m.recorded && (sent[0].Call != probe[0].Call || (sent[0].Call != 0) != m.byCall) {
					t.Errorf("the send under call %d, qobs.Effect's under %d (want a call: %v)", sent[0].Call, probe[0].Call, m.byCall)
				}
			})
		}
	}
}

// A caller that crashes after a send under its call's scope leaves the
// call pending, the send its effect (§8.1, 5: an effect that landed
// says the call took effect), with no recorder in the simulation.
func TestPendingCallKeepsItsScopedSend(t *testing.T) {
	t.Parallel()
	sink := &qobs.MemorySink{}
	rec := qobs.NewRecorder(sink)
	s := qenv.New(qenv.Nominal{})
	svc := qenv.NewService(s, "provider", nil, func(context.Context, capture) (string, error) { return "ok", nil })
	var p *qenv.Process
	p = s.Process("shop", true, func(ctx context.Context) {
		c := rec.Invoke(ctx, "pay")
		if _, err := svc.Call(c.Scope(ctx), capture{Key: "k1", Amount: 10}, time.Second); err != nil {
			t.Errorf("call: %v", err)
		}
		p.Crash() // stops here: pay never completes
		c.Done(nil)
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
	trs, _ := qtrace.Reconstruct(sink.Steps())
	if len(trs) != 1 || len(trs[0].Steps) != 0 || len(trs[0].Pending) != 1 {
		t.Fatalf("traces %+v", trs)
	}
	if es := trs[0].Pending[0].Effects; len(es) != 1 || es[0].Kind != "rpc.provider.request" || es[0].By != "call" {
		t.Errorf("the pending pay's effects %v", es)
	}
}

// closingSink fails a test that writes to it after Close.
type closingSink struct {
	qobs.MemorySink
	mu     sync.Mutex
	closed bool
	t      *testing.T
}

func (c *closingSink) Record(ctx context.Context, s *qtrace.Step) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		c.t.Errorf("%s (%s) written to a closed sink", s.Action, s.Record.Effect)
	}
	c.MemorySink.Record(ctx, s)
}

func (c *closingSink) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// A goroutine a call started (qenv.Go, inheriting its scope) that sends
// after the call's Done: the send is a late effect of the call's step. One
// after the recorder's Close is kept as Late, never written to the closed
// sink. Not parallel: a write after Close is logged as escaped, which
// strict runs in parallel would see.
func TestLateScopedSends(t *testing.T) {
	sink := &closingSink{t: t}
	rec := qobs.NewRecorder(sink)
	s := qenv.New(qenv.Nominal{})
	q := qenv.NewQueue(s, "mail", nil)
	s.Spawn("shop", func(ctx context.Context) {
		c := rec.Invoke(ctx, "pay")
		sc := c.Scope(ctx)
		qenv.Go(sc, "notifier", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Second)
			q.Publish(ctx, "late")
			if err := rec.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
			q.Publish(ctx, "closed")
		})
		c.Done(nil)
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	s.Close()
	trs, _ := qtrace.Reconstruct(sink.Steps())
	if len(trs) != 1 || len(trs[0].Steps) != 1 {
		t.Fatalf("traces %+v", trs)
	}
	if es := trs[0].Steps[0].Effects; len(es) != 1 || !es[0].Late || es[0].By != "call" || es[0].Fields["value"] != "late" {
		t.Errorf("pay's effects %v, want the late publish", es)
	}
	if late := rec.Late(); len(late) != 1 || late[0].Record.Effect != "queue.mail.publish" || late[0].Args["value"] != "closed" {
		t.Errorf("late records %v, want the publish after Close", late)
	}
}
