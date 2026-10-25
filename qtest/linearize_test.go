package qtest

import (
	"context"
	"fmt"
	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A withdrawal that overlapped the deposit it depended on returned first:
// in recording order the model cannot take it, and Linearize finds the
// order the intervals allow, the deposit first.
func TestLinearizeOption(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) } // units beyond qtrace.RealTimeSlack
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "a", Seq: 1, Thread: "a", Invoked: at(0), Time: at(10),
			Args: map[string]any{"amount": int64(60)}, Obs: map[string]any{"balance": int64(60)}},
		{Action: "withdraw", Recorder: "b", Seq: 1, Thread: "b", Invoked: at(1), Time: at(5),
			Args: map[string]any{"amount": int64(50)}, Obs: map[string]any{"balance": int64(10)}},
	}
	recorded := Validate(t, "../validate/testdata/account.yaml", steps)
	recorded.Expect(t, Rejects("withdraw"))
	if !strings.Contains(fmt.Sprint(recorded.Report.Actors[0].Issues), "check in an order real time allows") {
		t.Errorf("a failing client history checked as recorded does not suggest Linearize: %v", recorded.Report.Actors[0].Issues)
	}
	// a budget of one order checks only the recording order: no verdict,
	// which is not a rejection
	short := Validate(t, "../validate/testdata/account.yaml", steps, LinearizeBudget(1))
	if short.Report.OK() || short.Report.Actors[0].Conforms != validate.NotEvaluated || !strings.Contains(fmt.Sprint(short.Report.Issues), "LinearizeBudget(4)") {
		t.Errorf("out of budget: %s %v", short.Report.Summary(), short.Report.Issues)
	}
	if Rejects("withdraw").check(short.Report) {
		t.Error("Rejects matched a search that ran out of budget")
	}
	run := Validate(t, "../validate/testdata/account.yaml", steps, Linearize())
	run.Expect(t, Conforms())
	if run.Steps[0].Action != "deposit" {
		t.Errorf("checked in the order %v", run.Steps)
	}
	// a withdrawal that began after the deposit returned, reporting a
	// balance no order real time allows explains
	steps[1].Invoked, steps[1].Time = at(11), at(12)
	steps[1].Obs["balance"] = int64(-50)
	Validate(t, "../validate/testdata/account.yaml", steps, Linearize()).Expect(t, Mismatch("withdraw"))
}

// Steps lost from a recorder fail a linearized run as they fail one
// checked as recorded: the order checked is re-sequenced onto one thread,
// where the gap once vanished.
func TestLinearizeKeepsGaps(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "a", Seq: 1, Thread: "a", Time: time.Unix(1, 0),
			Args: map[string]any{"amount": int64(60)}, Obs: map[string]any{"balance": int64(60)}},
		{Action: "deposit", Recorder: "a", Seq: 3, Thread: "a", Time: time.Unix(3, 0),
			Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(70)}},
	}
	run := Validate(t, "../validate/testdata/account.yaml", steps, Linearize())
	if run.Report.OK() || !strings.Contains(fmt.Sprint(run.Report.Actors[0].Issues), "missing seq [2]") {
		t.Fatalf("a gap passed: %s", run.Report.Summary())
	}
}

// A linearized history keeps what is no step: a call never completed is
// still pending (Completes fails), and a settle still lets the eventual
// properties be judged, as without Linearize.
func TestLinearizeKeepsPendingAndSettle(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "a", Seq: 1, Thread: "a", Invoked: at(0), Time: at(10),
			Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(10)}},
		{Action: "withdraw", Recorder: "a", Seq: 2, Thread: "a", Invocation: true, Time: at(11),
			Args: map[string]any{"amount": int64(10)}},
		{Action: "settled", Recorder: "a", Seq: 3, Settled: true, Time: at(12)},
	}
	funds := "../validate/testdata/funds.binding.yaml"
	for _, opts := range [][]Option{nil, {Linearize()}} {
		run := Validate(t, funds, steps, opts...)
		run.Expect(t, Conforms(), Settles())
		if a := run.Report.Actors[0]; Completes().Holds(run) || len(a.Pending) != 1 || !a.Settled {
			t.Errorf("%d options: pending %v, settled %v", len(opts), a.Pending, a.Settled)
		}
	}
	// the withdrawal completed: the account is empty when the run settles
	done := append(steps[:2:2], qtrace.Step{Action: "withdraw", Recorder: "a", Seq: 3, Thread: "a", Call: 2, Time: at(13),
		Args: map[string]any{"amount": int64(10)}, Obs: map[string]any{"balance": int64(0)}},
		qtrace.Step{Action: "settled", Recorder: "a", Seq: 4, Settled: true, Time: at(14)})
	for _, opts := range [][]Option{nil, {Linearize()}} {
		Validate(t, funds, done, opts...).Expect(t, Completes(), Violates(validate.EventuallyName("funded")))
	}
	// with a step after the settle, the run did not end settled: nothing judged
	late := append(append([]qtrace.Step(nil), done...), qtrace.Step{Action: "deposit", Recorder: "a", Seq: 5, Thread: "a", Time: at(15),
		Args: map[string]any{"amount": int64(5)}, Obs: map[string]any{"balance": int64(5)}})
	run := Validate(t, funds, late)
	run.Expect(t, Conforms())
	if Settles().Holds(run) {
		t.Error("Settles held of a run that went on after its settle")
	}
}

// Independent keys partition a history: per key a write and a read that
// returned before it (but overlapped it) and saw its value. Searched
// whole, the overlapping operations of three keys take more orders than a
// small budget; searched per key, each key's two do.
func TestLinearizeByKey(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	var steps []qtrace.Step
	for i, k := range []string{"a", "b", "c"} {
		steps = append(steps,
			qtrace.Step{Action: "read", Recorder: "r" + k, Seq: 1, Thread: "r" + k, Invoked: at(i), Time: at(5 + i),
				Args: map[string]any{"key": k}, Obs: map[string]any{"value": int64(i + 1)}},
			qtrace.Step{Action: "write", Recorder: "w" + k, Seq: 1, Thread: "w" + k, Invoked: at(i), Time: at(10 + i),
				Args: map[string]any{"key": k, "value": int64(i + 1)}})
	}
	whole := Validate(t, "testdata/registers.yaml", steps, LinearizeBudget(3))
	if whole.Report.Actors[0].Conforms != validate.NotEvaluated {
		t.Fatalf("the whole history in a budget of 3 orders: %s", whole.Report.Summary())
	}
	parts := Validate(t, "testdata/registers.yaml", steps, LinearizeByKey("key"), LinearizeBudget(3))
	parts.Expect(t, Conforms())
	if n := len(parts.Report.Actors); n != 3 || parts.Report.Actors[0].Actor != "default key=a" {
		t.Errorf("%d actors (%s), want one per key", n, parts.Report.Summary())
	}
	// a stale read is still found in its key's partition
	steps[2].Obs["value"] = int64(7) // b's read
	Validate(t, "testdata/registers.yaml", steps, LinearizeByKey("key")).Expect(t, Mismatch("read"))
}

// In a simulation calls take no virtual time unless something sleeps:
// one client's write and its read after it share an instant, which real
// time with slack cannot order, and its read that missed its own write
// once passed linearized. One recorder orders its steps exactly (the
// write's completion before the read's invocation record).
func TestLinearizeKeepsARecordersOrder(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	run := func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		s.Spawn("client", func(ctx context.Context) {
			qobs.Invoke(ctx, "write", "key", "a", "value", 1).Done(nil)
			qobs.Invoke(ctx, "read", "key", "a").Done(nil, "obs.value", 0) // missed its own write
		})
		return s.Run(time.Minute)
	}
	sys := System{Binding: "testdata/registers.yaml", Rates: qenv.NoFaults, Opts: []Option{Linearize()}, Run: run}
	sys.Explore(t, ExploreConfig{Seeds: 2}).Expect(t, All(Mismatch("read")))
}

// A completion takes its invocation record's time: a stale read recorded
// by Invoke and Done alone, on recorders of their own, is bounded by real
// time. Steps of which none has an invocation time are warned of: every
// order is then allowed.
func TestLinearizeTakesInvocationTimes(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	inv := func(rec string, u int, action string, args map[string]any) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: rec, Seq: 1, Thread: rec, Time: at(u), Invocation: true, Args: args}
	}
	done := func(rec string, u int, action string, args, obs map[string]any) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: rec, Seq: 2, Thread: rec, Time: at(u), Call: 1, Args: args, Obs: obs}
	}
	w, r := map[string]any{"key": "a", "value": int64(1)}, map[string]any{"key": "a"}
	steps := []qtrace.Step{
		inv("w", 0, "write", w), done("w", 1, "write", w, nil),
		inv("r", 5, "read", r), done("r", 6, "read", r, map[string]any{"value": int64(0)}),
	}
	Validate(t, "testdata/registers.yaml", steps, Linearize()).Expect(t, Mismatch("read"))

	plain := []qtrace.Step{steps[1], steps[3]}
	for i := range plain {
		plain[i].Seq, plain[i].Call = 1, 0
	}
	run := Validate(t, "testdata/registers.yaml", plain, Linearize())
	run.Expect(t, Conforms()) // no bound: the read may come first
	if !strings.Contains(fmt.Sprint(run.Report.Issues), "real time bounds none of its 2 steps") {
		t.Errorf("no warning that real time bounds nothing: %v", run.Report.Issues)
	}
}

// A search cut by its budget is no verdict for what the order shown
// violated either: an order that conformed step by step but failed an
// eventual val was a candidate the search went on from, and was once
// reported as the run's violation.
func TestLinearizeCutJudgesNoInvariant(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	steps := []qtrace.Step{
		{Action: "deposit", Recorder: "a", Seq: 1, Thread: "a", Invoked: at(0), Time: at(10),
			Args: map[string]any{"amount": int64(60)}, Obs: map[string]any{"balance": int64(60)}},
		{Action: "withdraw", Recorder: "b", Seq: 1, Thread: "b", Invoked: at(1), Time: at(11),
			Args: map[string]any{"amount": int64(60)}, Obs: map[string]any{"balance": int64(0)}},
		{Action: "settled", Recorder: "b", Seq: 2, Thread: "b", Time: at(12), Settled: true},
	}
	run := Validate(t, "../validate/testdata/funds.binding.yaml", steps, LinearizeBudget(1))
	a := run.Report.Actors[0]
	if a.Conforms != validate.NotEvaluated {
		t.Errorf("conformance %s, want n/a: %s", a.Conforms, run.Report.Summary())
	}
	for _, inv := range a.Invariants {
		if inv.Verdict == validate.Fail {
			t.Errorf("%s failed in a search cut short: %s", inv.Name, run.Report.Summary())
		}
	}
	if Violates(validate.EventuallyName("funded")).check(run.Report) {
		t.Error("Violates matched a search that ran out of budget")
	}
	// with the budget to finish, every order ends empty: the violation
	Validate(t, "../validate/testdata/funds.binding.yaml", steps, Linearize()).Expect(t, Violates(validate.EventuallyName("funded")))
}

// Linearize takes an order in which the invariants hold when one
// conforms: two increments and a decrement, all concurrent, exceed one
// in their recorded order and not with the decrement between them. The
// first conforming order was once taken, a violation by the order picked.
func TestLinearizePrefersInvariantsHolding(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	op := func(rec, action string) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: rec, Seq: 1, Thread: rec, Invoked: at(0), Time: at(10)}
	}
	steps := []qtrace.Step{op("a", "inc"), op("b", "inc"), op("c", "dec")}
	Validate(t, "testdata/counter.yaml", steps).Expect(t, Violates("atMostOne"))
	Validate(t, "testdata/counter.yaml", steps, Linearize()).Expect(t, Conforms())
}

// The search for an order in which the invariants hold, cut by the
// budget, is no verdict for them: it once reported the violation of the
// order found first, T1b's case again.
func TestLinearizeInvariantSearchCut(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	op := func(rec, action string) qtrace.Step {
		return qtrace.Step{Action: action, Recorder: rec, Seq: 1, Thread: rec, Invoked: at(0), Time: at(10)}
	}
	steps := []qtrace.Step{op("a", "inc"), op("b", "inc"), op("c", "dec")}
	for _, budget := range []int{1, 2} {
		run := Validate(t, "testdata/counter.yaml", steps, LinearizeBudget(budget))
		if Violates("atMostOne").check(run.Report) {
			t.Errorf("budget %d: a violation from a search cut short:\n%s", budget, run.Report.Summary())
		}
	}
}

// A run's settle, re-sequenced after each partition's order, keeps its
// time: without it the other partitions' steps were all later than it,
// and since a settle settles nothing another recorder worked past, no
// partitioned run settled.
func TestLinearizeByKeyKeepsTheSettle(t *testing.T) {
	t.Parallel()
	RequireQuint(t)
	at := func(u int) time.Time { return time.Unix(0, 0).Add(time.Duration(u) * 10 * time.Millisecond) }
	w := func(seq uint64, key string, u int) qtrace.Step {
		return qtrace.Step{Action: "write", Recorder: "r", Seq: seq, Thread: "c", Invoked: at(u), Time: at(u + 1),
			Args: map[string]any{"key": key, "value": int64(1)}}
	}
	steps := []qtrace.Step{w(1, "a", 0), w(2, "b", 2), {Action: "settled", Recorder: "r", Seq: 3, Settled: true, Time: at(5)}}
	run := Validate(t, "testdata/registers.yaml", steps, LinearizeByKey("key"))
	run.Expect(t, Conforms())
	if !Settles().Holds(run) {
		t.Errorf("a settled run, partitioned, does not settle:\n%s", run.Report.Summary())
	}
}

// An exploration confirms the Go evaluator's orders by its runs' reports:
// an evaluator that accepts every candidate gives an order quint rejects,
// which a run's failing report catches, and the run is searched and
// judged again with quint alone, saying so. Not parallel: the evaluator
// is made wrong process-wide.
func TestExploreConfirmsTheEvaluatorsOrders(t *testing.T) {
	RequireQuint(t)
	hooks.TamperEval.Store(2)
	defer hooks.TamperEval.Store(0)
	run := func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		// overlapping calls: real time allows both orders
		s.Spawn("writer", func(ctx context.Context) {
			c := qobs.Invoke(ctx, "write", "key", "a", "value", 1)
			qenv.Sleep(ctx, time.Millisecond)
			c.Done(nil)
		})
		s.Spawn("reader", func(ctx context.Context) {
			c := qobs.Invoke(ctx, "read", "key", "a")
			qenv.Sleep(ctx, time.Millisecond)
			c.Done(nil, "obs.value", 9) // no write of 9
		})
		return s.Run(time.Minute)
	}
	sys := System{Binding: "testdata/registers.yaml", Rates: qenv.NoFaults, Opts: []Option{Linearize()}, Run: run}
	x := sys.Explore(t, ExploreConfig{Seeds: 2})
	x.Expect(t, All(Mismatch("read")))
	for _, r := range x.Runs {
		if !strings.Contains(fmt.Sprint(r.Report.Issues), "does not conform for quint") {
			t.Errorf("run %v: issues %v, want the evaluator's wrong order said", r.origin, r.Report.Issues)
		}
	}
}
