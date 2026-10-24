package kvstore

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/chucklehead-dev/gotogether/connect"
	"github.com/chucklehead-dev/gotogether/convert"
	"github.com/chucklehead-dev/gotogether/examples/kvstore/kv"
	"github.com/chucklehead-dev/gotogether/otelio"
	"github.com/chucklehead-dev/gotogether/qhegel"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"hegel.dev/go/hegel"
)

// service starts the HTTP service on a fresh store, its spans going to sr.
func service(t testing.TB, m kv.Mutation, sr *tracetest.SpanRecorder) *kv.Client {
	var opts []sdktrace.TracerProviderOption
	if sr != nil {
		opts = append(opts, sdktrace.WithSpanProcessor(sr))
	}
	srv := httptest.NewServer(kv.NewServer(kv.NewStore(m), sdktrace.NewTracerProvider(opts...)).Handler())
	t.Cleanup(srv.Close)
	return &kv.Client{Base: srv.URL}
}

// ---- 1. the running service reports, the model checks ---------------------

// Concurrent load against the service. The store's model steps are
// recorded (qtest) and must form a behaviour of the model: every CAS
// outcome, revision and read consistent with some serial history, namely
// the one the store's lock produced.
func TestServiceUnderLoadConforms(t *testing.T) {
	qtest.RequireQuint(t)
	qtest.Record(t, "kv.binding.yaml", func(*qobs.Recorder) {
		cl := service(t, kv.Faithful, nil)
		if err := kv.Load(context.Background(), cl.Base, 8, 40, []string{"a", "b"}, 1); err != nil {
			t.Fatal(err)
		}
	}, qtest.Global()).Expect(t, qtest.Conforms()) // the server's handlers run on its goroutines
}

// The server's plain OTel spans, from the same kind of load, converted by
// kv.convert.yaml. Spans of concurrent requests end in any order; kv.op
// (the store's linearization index) puts them back in the store's order.
func TestServiceSpansConform(t *testing.T) {
	qtest.RequireQuint(t)
	qtest.Validate(t, "kv.binding.yaml", loadSpans(t, kv.Faithful, 1)).Expect(t, qtest.Conforms())
}

// The stale-CAS race only shows under concurrency; its signature is a CAS
// that succeeded with an expected version the model's store no longer
// had. Both the recorded steps and the converted spans show it.
func TestStaleCASCaughtUnderLoad(t *testing.T) {
	qtest.RequireQuint(t)
	for attempt := uint64(1); attempt <= 5; attempt++ {
		run := qtest.Validate(t, "kv.binding.yaml", loadSpans(t, kv.StaleCAS, attempt))
		if run.OK() {
			continue // the race did not happen this time
		}
		run.Expect(t, qtest.Rejects("cas"))
		return
	}
	t.Fatal("five concurrent loads never exposed the stale CAS")
}

func loadSpans(t *testing.T, m kv.Mutation, seed uint64) []qtrace.Step {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	cl := service(t, m, sr)
	if err := kv.Load(context.Background(), cl.Base, 8, 40, []string{"a", "b"}, seed); err != nil {
		t.Fatal(err)
	}
	rules, err := convert.Load("kv.convert.yaml")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := rules.Steps([]otelio.TracesData{otelio.SpansToTracesData(sr.Ended())})
	if err != nil {
		t.Fatal(err)
	}
	return steps
}

// clientHistory runs a recording load (kv.LoadRecorded): what clients
// that cannot see the store's lock record, each operation when it returns
// with the time it was invoked. The store's own steps go nowhere: its
// handlers' contexts carry no recorder.
func clientHistory(t *testing.T, m kv.Mutation, clients, ops int, seed uint64) []qtrace.Step {
	t.Helper()
	mem := &qobs.MemorySink{}
	ctx := qobs.WithRecorder(context.Background(), qobs.NewRecorder(mem, qobs.WithActor("kv")))
	cl := service(t, m, nil)
	if err := kv.LoadRecorded(ctx, cl.Base, clients, ops, []string{"a", "b"}, seed); err != nil {
		t.Fatal(err)
	}
	return mem.Steps()
}

// A client's history of the faithful service: concurrent operations
// return in another order than the store applied them, and checked in
// that order the history is rejected (every load tried). Linearize finds
// an order the operations' intervals allow that the model accepts.
func TestClientHistoryLinearizes(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("searches orders, a quint check each")
	}
	qtest.Validate(t, "kv.binding.yaml", clientHistory(t, kv.Faithful, 3, 5, 1), qtest.LinearizeBudget(proofBudget)).Expect(t, qtest.Conforms())
}

// proofBudget decides a history of three clients' few operations: how
// many orders it takes depends on how much the run's real concurrency
// overlapped them (a hundred, or past the default 200 once in a few
// CI runs), not on the code. ROADMAP T2c's search by model state ends
// the dependence.
const proofBudget = 5000

// A stale read (StaleRead: reads from a lagging snapshot) began after a
// write returned and did not see it: no order real time allows explains
// it, which the complete search proves (a search cut short is no
// verdict), and the order that got furthest stops at a read. Its loads
// are small (3 clients, 4 operations each): a proof tries every order
// real time allows, usually about a hundred of 12 steps, where 32 steps'
// took thousands.
func TestStaleReadNotLinearizable(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("searches orders, a quint check each")
	}
	for seed := uint64(1); seed <= 10; seed++ {
		run := qtest.Validate(t, "kv.binding.yaml", clientHistory(t, kv.StaleRead, 3, 4, seed), qtest.LinearizeBudget(proofBudget))
		if run.OK() {
			continue // no read was stale this time
		}
		run.Expect(t, qtest.Mismatch("read"))
		return
	}
	t.Fatal("ten loads never read a stale snapshot")
}

// ---- 2. the model drives the service ---------------------------------------

type driver struct {
	cl    *kv.Client
	store *kv.Store
	m     kv.Mutation
	t     testing.TB
}

func (d *driver) Step(s *connect.Step) error {
	ctx := context.Background()
	key := func() string { return connect.MustPick[string](s, "key") }
	val := func() string { return connect.MustPick[string](s, "value") }
	ignore := func(err error) error {
		if kv.IsConflict(err) || kv.IsNotFound(err) {
			return nil // the model's outcome is checked through the state
		}
		return err
	}
	return connect.Switch{
		"init": func(*connect.Step) error {
			d.store = kv.NewStore(d.m)
			srv := httptest.NewServer(kv.NewServer(d.store, nil).Handler())
			d.t.Cleanup(srv.Close)
			d.cl = &kv.Client{Base: srv.URL}
			return nil
		},
		"write": func(*connect.Step) error { _, err := d.cl.Put(ctx, key(), val()); return err },
		"cas": func(s *connect.Step) error {
			_, err := d.cl.CAS(ctx, key(), connect.MustPick[int64](s, "expected"), val())
			return ignore(err)
		},
		"del":  func(*connect.Step) error { _, err := d.cl.Delete(ctx, key()); return ignore(err) },
		"read": func(*connect.Step) error { _, err := d.cl.Get(ctx, key()); return ignore(err) },
	}.Step(s)
}

// modelState is the model's state, decoded with itf.Decode: store's
// entries into kv.Entry (by its json field names), and the revision.
type modelState struct {
	Store    map[string]kv.Entry
	Revision int64
}

func (d *driver) State() (any, error) {
	data, rev := d.store.Snapshot()
	return modelState{Store: data, Revision: rev}, nil
}

// Model traces drive the service through its HTTP API; the store must
// match the model after every request.
func TestModelDrivesService(t *testing.T) {
	qtest.RequireQuint(t)
	connect.Run(t, connect.Config{Spec: "model/kv.qnt", Main: "kvDesign", MaxSamples: 30, MaxSteps: 25},
		func() connect.Driver { return &driver{t: t} })
}

// ---- 3. Hegel drives the service, the model judges --------------------------

type machine struct {
	cl       *kv.Client
	versions *hegel.Pool[int64] // versions the machine has seen: plausible CAS expectations
}

var (
	keys   = hegel.SampledFrom([]string{"a", "b"})
	values = hegel.SampledFrom([]string{"x", "y", "z"})
)

func (m *machine) RulePut(tc hegel.TestCase) {
	if r, err := m.cl.Put(context.Background(), hegel.Draw(tc, keys), hegel.Draw(tc, values)); err == nil {
		m.versions.Add(tc, r.Revision)
	}
}

func (m *machine) RuleCAS(tc hegel.TestCase) {
	exp := int64(0)
	if !m.versions.IsEmpty() && hegel.Draw(tc, hegel.Booleans()) {
		exp = hegel.Draw(tc, m.versions.ValuesReusable())
	}
	if r, err := m.cl.CAS(context.Background(), hegel.Draw(tc, keys), exp, hegel.Draw(tc, values)); err == nil {
		m.versions.Add(tc, r.Revision)
	}
}

func (m *machine) RuleDelete(tc hegel.TestCase) {
	_, _ = m.cl.Delete(context.Background(), hegel.Draw(tc, keys))
}
func (m *machine) RuleGet(tc hegel.TestCase) {
	_, _ = m.cl.Get(context.Background(), hegel.Draw(tc, keys))
}

// Hegel's rules call the service over HTTP; the store's model steps go to
// each case's recorder (Global) and the runs are checked after the test.
func TestHegelDrivesService(t *testing.T) {
	qtest.RequireQuint(t)
	qhegel.Test(t, qhegel.Config{Binding: "kv.binding.yaml", Mode: qhegel.After, Opts: []qtest.Option{qtest.Global()}, AllowEmpty: true},
		func(ht *hegel.T, _ *qhegel.Case) {
			hegel.RunStateful(ht, &machine{cl: service(ht, kv.Faithful, nil), versions: hegel.NewPool[int64](ht)},
				hegel.WithStatefulStepCount(15))
		}, hegel.WithTestCases(30), hegel.WithDatabase(""))
}

// The RevisionOnConflict mutant, with every case checked: Hegel shrinks to
// a failed CAS (the revision wrongly advances) caught by the next check of
// the revision.
func TestHegelShrinksRevisionOnConflict(t *testing.T) {
	qtest.RequireQuint(t)
	if testing.Short() {
		t.Skip("shrinking against the model runs quint dozens of times")
	}
	v := qhegel.FindViolation(t, qhegel.ViolationConfig{Binding: "kv.binding.yaml", Opts: []qtest.Option{qtest.Global()}}, func(tc hegel.TestCase, _ *qhegel.Case) {
		cl := service(t, kv.RevisionOnConflict, nil)
		hegel.RunStateful(tc, &machine{cl: cl, versions: hegel.NewPool[int64](tc)}, hegel.WithStatefulStepCount(10))
	}, hegel.WithTestCases(30), hegel.WithSeed(11))
	v.Expect(t, qtest.Mismatch("cas"))
	if len(v.Steps) != 1 { // shrunk to the failed CAS alone
		t.Errorf("shrunk to %d steps, want the failed CAS alone:\n%s", len(v.Steps), qhegel.Render(v.Steps))
	}
}
