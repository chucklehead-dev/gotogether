# The counter, walked through

The counter of [model/counter.qnt](../model/counter.qnt) counts up to its
cap. Its invariants, where the code enforces each and which tests check
it (generated: `gotogether invariants -spec model/counter.qnt -write
docs/walkthrough.md ./counter`, from the fixture's directory; the test
`TestInvariantsGolden` fails when it is stale):

<!-- gotogether:invariants -->
| property | meaning | enforced by | checked by |
|---|---|---|---|
| `withinCap` | n never exceeds the cap. | `Counter.Inc` [../counter/counter.go:25](../counter/counter.go#L25) | `TestInc` [../counter/counter_test.go:14](../counter/counter_test.go#L14)<br>`TestReset/from_the_cap` [../counter/counter_test.go:30](../counter/counter_test.go#L30) |
| `nonNegative` | n is never negative. | `Counter.Reset` [../counter/counter.go:37](../counter/counter.go#L37) | `TestReset/from_the_cap` [../counter/counter_test.go:30](../counter/counter_test.go#L30) |
| `atCap` | | — | — |
<!-- /gotogether:invariants -->

`atCap` is no invariant: nothing enforces it, and no test checks it.

Its actions, the code that records each step and the drivers that run
it (written by the same command):

<!-- gotogether:actions -->
| action | recorded by | driven by |
|---|---|---|
| `inc` | `Counter.Inc` [../counter/counter.go:25](../counter/counter.go#L25) | `bound` [../counter/counter_test.go:39](../counter/counter_test.go#L39) |
| `reset` | `Counter.Reset` [../counter/counter.go:38](../counter/counter.go#L38) | `TestModel` [../counter/mbt_test.go:16](../counter/mbt_test.go#L16) |
| `idle` | — | — |
<!-- /gotogether:actions -->

Nothing implements `idle`.

What the model leaves out, its `// @assume` lines, and the code that
goes past each on purpose or watches it in production (written by the
same command):

<!-- gotogether:assumptions -->
| assumption | statement | stated by | exercised by | monitored by |
|---|---|---|---|---|
| `noOverflow` | n + 1 never overflows; Go's int wraps past math.MaxInt | `belowMaxInt` | `TestIncPastMaxInt` [../counter/counter_test.go:46](../counter/counter_test.go#L46) | — |
| `oneCaller` | one caller at a time; the real Counter has no lock | — | — | `Busy` [../counter/counter.go:45](../counter/counter.go#L45) |
| `capFixed` | the cap never changes; the real Cap is a field anyone may set | — | — | — |
<!-- /gotogether:assumptions -->

Nothing targets `capFixed` yet.
