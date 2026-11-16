# The counter, walked through

The counter of [model/counter.qnt](../model/counter.qnt) counts up to its
cap. Its invariants, where the code enforces each and which tests check
it (generated: `gotogether invariants -spec model/counter.qnt -write
docs/walkthrough.md ./counter`, from the fixture's directory; the test
`TestInvariantsGolden` fails when it is stale):

<!-- gotogether:invariants -->
| property | meaning | enforced by | checked by |
|---|---|---|---|
| `withinCap` | n never exceeds the cap. | `Counter.Inc` [../counter/counter.go:25](../counter/counter.go#L25) | `TestInc` [../counter/counter_test.go:13](../counter/counter_test.go#L13)<br>`TestReset` [../counter/counter_test.go:29](../counter/counter_test.go#L29) |
| `nonNegative` | n is never negative. | `Counter.Reset` [../counter/counter.go:37](../counter/counter.go#L37) | `TestReset` [../counter/counter_test.go:29](../counter/counter_test.go#L29) |
| `atCap` | | — | — |
<!-- /gotogether:invariants -->

`atCap` is no invariant: nothing enforces it, and no test checks it.

Its actions, the code that records each step and the drivers that run
it (written by the same command):

<!-- gotogether:actions -->
| action | recorded by | driven by |
|---|---|---|
| `inc` | `Counter.Inc` [../counter/counter.go:25](../counter/counter.go#L25) | `bound` [../counter/counter_test.go:38](../counter/counter_test.go#L38) |
| `reset` | `Counter.Reset` [../counter/counter.go:38](../counter/counter.go#L38) | `TestModel` [../counter/mbt_test.go:16](../counter/mbt_test.go#L16) |
| `idle` | — | — |
<!-- /gotogether:actions -->

Nothing implements `idle`.
