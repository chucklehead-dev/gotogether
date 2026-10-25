package qtest

import (
	"fmt"
)

// Linearize makes Validate and Record check steps in an order their
// real-time intervals allow, searched for one the model accepts
// (qtrace.Linearize), rather than in the order they were recorded. It is
// for steps recorded where an operation was invoked and returned (a
// client's, with "invoked": qobs.Record's key, or converted spans, whose
// start is their invocation): concurrent operations then complete in
// another order than they took effect, and the recording order rejects
// correct code. A step recorded where it takes effect needs none of it.
//
// The run's Steps become the order checked: one in which every invariant
// holds, if a conforming one does (a run must not look violating by the
// order picked). The report gains an issue
// per actor: "info" with the number of orders tried when one conformed,
// "warn" when none did, the order shown then being the one that got
// furthest. A search that stopped there, no move getting further, is
// strong evidence of a bug: the actor fails as that order does. One that
// ran out of its budget (validate.DefaultLinearizeBudget orders, LinearizeBudget)
// first is no verdict: the actor's conformance is n/a, which Conforms
// fails and Rejects and Mismatch do not match. Record, Validate and
// explorations run it (each run's search on its own); qhegel's cases,
// checked as they record, refuse it. In a simulation, steps carry its
// virtual time, so a client history there has its intervals.
func Linearize() Option { return option(optLinearize, func(c *settings) { c.Linearize = true }) }

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
	return option(optLinearizeByKey, func(c *settings) { c.Linearize, c.linearizeKey = true, arg })
}

// LinearizeBudget is Linearize with a budget of n orders per actor (per
// partition with LinearizeByKey; at least 1), for its guided search and as
// many again for the complete one that follows it when it finds none: a run whose search ran out of validate.DefaultLinearizeBudget's says
// to raise it.
func LinearizeBudget(n int) Option {
	if n < 1 {
		panic(fmt.Sprintf("qtest: LinearizeBudget(%d): the budget is a number of orders to check, at least 1", n))
	}
	return option(optLinearizeBudget, func(c *settings) { c.Linearize, c.linearizeBudget = true, n })
}
