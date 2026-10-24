package qtest

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// DefaultLinearizeBudget is how many orders Linearize checks per actor at
// most, unless LinearizeBudget says otherwise.
const DefaultLinearizeBudget = 200

// Linearize makes Validate and Record check steps in an order their
// real-time intervals allow, searched for one the model accepts
// (qtrace.Linearize), rather than in the order they were recorded. It is
// for steps recorded where an operation was invoked and returned (a
// client's, with "invoked": qobs.Record's key, or converted spans, whose
// start is their invocation): concurrent operations then complete in
// another order than they took effect, and the recording order rejects
// correct code. A step recorded where it takes effect needs none of it.
//
// The run's Steps become the order checked. The report gains an issue
// per actor: "info" with the number of orders tried when one conformed,
// "warn" when none did, the order shown then being the one that got
// furthest. A search that stopped there, no move getting further, is
// strong evidence of a bug: the actor fails as that order does. One that
// ran out of its budget (DefaultLinearizeBudget orders, LinearizeBudget)
// first is no verdict: the actor's conformance is n/a, which Conforms
// fails and Rejects and Mismatch do not match. It is Record's and
// Validate's: an exploration and qhegel's cases refuse it.
func Linearize() Option { return option("Linearize", func(c *settings) { c.Linearize = true }) }

// LinearizeByKey is Linearize with each actor's history partitioned by
// the value of one argument (a key-value store's "key"), each partition
// searched alone and reported as its own actor ("default key=a"), as
// Porcupine partitions a history: the orders of independent keys
// multiply, so a history of k keys with m operations each needs the
// search of each key's m, not of their k·m together. It is sound only if
// the model treats keys independently (a step on one key cannot enable,
// disable or change another's) and the model's initial state is every
// key's: a replay of one partition starts the model from init. So an
// invariant or eventual val over every key is judged in each partition
// with the other keys at init, and a step on two keys (a transfer's src
// and dest) belongs to both, which a partition by one cannot say: it
// lands in src's alone and dest's then shows a false Mismatch. Keep such
// histories on Linearize. A step without the argument fails the test with
// the fix. LinearizeBudget is then per partition.
func LinearizeByKey(arg string) Option {
	if arg == "" {
		panic("qtest: LinearizeByKey(\"\"): name the argument whose value partitions the history (\"key\"), or use Linearize")
	}
	return option("LinearizeByKey", func(c *settings) { c.Linearize, c.linearizeKey = true, arg })
}

// LinearizeBudget is Linearize with a budget of n orders per actor (per
// partition with LinearizeByKey; at least 1): a run whose search ran out of DefaultLinearizeBudget's says
// to raise it.
func LinearizeBudget(n int) Option {
	if n < 1 {
		panic(fmt.Sprintf("qtest: LinearizeBudget(%d): the budget is a number of orders to check, at least 1", n))
	}
	return option("LinearizeBudget", func(c *settings) { c.Linearize, c.linearizeBudget = true, n })
}

// linearize orders each actor's steps by the search, checking candidate
// orders with ch in batches, at most budget per actor. With key, each
// actor's history is partitioned by that argument's value first, and
// each partition searched alone, as its own actor ("acct key=a"): the
// orders of independent keys multiply, a partition's are its own.
// undecided are the actors whose search ran out of the budget. kept are
// the issues of each actor's steps as recorded that no order removes (a
// gap: steps were lost; a cycle: their constraints contradict each
// other), for its result: the order checked is re-sequenced onto one
// thread, where neither shows.
func linearize(t testing.TB, ch *validate.Checker, steps []qtrace.Step, budget int, key string) (out []qtrace.Step, issues []qtrace.Issue, undecided map[string]bool, kept map[string][]qtrace.Issue) {
	t.Helper()
	traces, issues := qtrace.Reconstruct(steps) // deduplicated, gaps found across actors
	undecided, kept = map[string]bool{}, map[string][]qtrace.Issue{}
	for _, tr := range traces {
		for _, part := range partitions(t, tr, key) {
			actor := part.Actor // as the report names it ("" for no actor)
			for _, i := range tr.Issues {
				if i.Kind == "gap" || i.Kind == "cycle" {
					kept[actor] = append(kept[actor], i)
				}
			}
			o, issue, cut := order(t, ch, part, budget, key != "")
			issues = append(issues, issue)
			if cut {
				undecided[actor] = true
			}
			out = append(out, o...)
			// what is no step goes on after the order, on its recorder: the
			// calls never completed, then the settle, if the run recorded one
			rec := len(o)
			if len(o) > 0 {
				rec = int(o[len(o)-1].Seq)
			}
			after := func(s qtrace.Step) {
				rec++
				s.Recorder, s.Thread, s.Seq, s.Actor = "linearized:"+actor, "linearized", uint64(rec), actor
				out = append(out, s)
			}
			for _, p := range part.Pending {
				after(p)
			}
			if part.Settled {
				after(qtrace.Step{Action: "settled", Settled: true})
			}
		}
	}
	return out, issues, undecided, kept
}

// partitions splits a trace by the value of argument key, in order of
// first appearance, each named "<actor> <key>=<value>" (key "": the
// trace whole). A step without the argument cannot be put in one: it
// fails the test, as partitioning would be unsound.
func partitions(t testing.TB, tr *qtrace.Trace, key string) []*qtrace.Trace {
	t.Helper()
	if key == "" {
		return []*qtrace.Trace{tr}
	}
	var out []*qtrace.Trace
	by := map[string]*qtrace.Trace{}
	of := func(s qtrace.Step) *qtrace.Trace {
		v, ok := s.Args[key]
		if !ok {
			t.Fatalf("gotogether: LinearizeByKey(%q): step %s has no %s argument: partition only a history whose every step names one key "+
				"(a step over several keys, a scan, makes them dependent: use Linearize)", key, s.String(), key)
		}
		actor := tr.Actor
		if actor == "" {
			actor = "default" // as a step log's steps name it
		}
		name := fmt.Sprintf("%s %s=%v", actor, key, v)
		id := fmt.Sprintf("%T:%v", v, v) // 1 and "1" are two keys
		p := by[id]
		if p == nil {
			if slices.ContainsFunc(out, func(o *qtrace.Trace) bool { return o.Actor == name }) {
				name += fmt.Sprintf(" (%T)", v)
			}
			p = &qtrace.Trace{Actor: name, Settled: tr.Settled}
			by[id] = p
			out = append(out, p)
		}
		return p
	}
	for _, s := range tr.Steps {
		p := of(s)
		s.Actor = p.Actor
		p.Steps = append(p.Steps, s)
	}
	for _, s := range tr.Pending {
		p := of(s)
		s.Actor = p.Actor
		p.Pending = append(p.Pending, s)
	}
	return out
}

// order finds an order of one actor's steps the model accepts, within
// budget, and says how it went: an issue, and whether the search was cut
// short (no verdict).
func order(t testing.TB, ch *validate.Checker, tr *qtrace.Trace, budget int, partitioned bool) ([]qtrace.Step, qtrace.Issue, bool) {
	t.Helper()
	a := tr.Actor // as messages name it
	if a == "" {
		a = "default"
	}
	check := func(orders [][]qtrace.Step) []int {
		reps, errs := ch.CheckBatch(context.Background(), orders, 0)
		fs := make([]int, len(orders))
		for i := range orders {
			if errs[i] != nil {
				t.Fatalf("gotogether: linearizing actor %s: %v", a, errs[i])
			}
			fs[i] = failedAt(reps[i], len(orders[i]))
		}
		return fs
	}
	if len(tr.Steps) == 0 {
		return nil, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no steps to order", a)}, false
	}
	o, ok, tried, cut := qtrace.Linearize(tr.Steps, budget, check)
	switch {
	case ok:
		return o, qtrace.Issue{Severity: "info", Kind: "linearize", Msg: fmt.Sprintf("actor %s: an order of its %d steps conforms (%d orders tried)", a, len(o), tried)}, false
	case cut:
		return o, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no order of its %d steps conformed in the %d orders of the budget, and the search had moves left: "+
			"no verdict (conformance n/a). Raise the budget (qtest.LinearizeBudget(%d)), %sor record its steps where they take effect", a, len(o), tried, 4*budget, partitionHint(partitioned))}, true
	default:
		return o, qtrace.Issue{Severity: "warn", Kind: "linearize", Msg: fmt.Sprintf("actor %s: no order of its %d steps that real time allows conformed in %d orders tried; "+
			"the order shown got furthest. A step the model rejects in every order is a bug (a stale read, a lost write), unless the invocation times are wrong", a, len(o), tried)}, false
	}
}

// failedAt is the index of the first step of a one-actor order the model
// rejects, or -1 when it conforms (an invariant it violates is not the
// order's fault).
func failedAt(rep *validate.Report, n int) int {
	for _, a := range rep.Actors {
		if a.Conforms == validate.Pass {
			continue
		}
		if a.Failure != nil && a.Failure.Step != nil && a.Failure.Step.Seq >= 1 {
			return int(a.Failure.Step.Seq) - 1 // the order's position: re-sequenced from 1
		}
		return n - 1
	}
	return -1
}

// partitionHint suggests LinearizeByKey to a search not partitioned.
func partitionHint(partitioned bool) string {
	if partitioned {
		return ""
	}
	return "partition independent keys (qtest.LinearizeByKey), "
}
